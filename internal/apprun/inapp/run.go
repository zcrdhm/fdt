package inapp

import (
	"context"
	"crypto/tls"
	"fdt/internal/apprun"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fdt/internal/av"
	"fdt/internal/crypto"
	"fdt/internal/datasvc"
	"fdt/internal/deviceauth"
	"fdt/internal/hsm"
	"fdt/internal/identity"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/portal"
	"fdt/internal/store"
)

func RunIntranet(env map[string]string) error {
	// Portal UI：LISTEN（默认 8080）。ATM 数据口：DATA_LISTEN（默认 8081），永不挂页面。
	listen := apprun.EnvOr(env, "LISTEN", "0.0.0.0:8080")
	atmListen := apprun.EnvOr(env, "DATA_LISTEN", "0.0.0.0:8081")
	if sameHostPort(listen, atmListen) {
		return fmt.Errorf("intranet: LISTEN(%s) 与 DATA_LISTEN(%s) 不能相同", listen, atmListen)
	}
	dataDir := apprun.EnvOr(env, "DATA_DIR", "/var/lib/fdt")
	keys := apprun.EnvOr(env, "KEYS_DIR", "/etc/fdt/keys")
	tenant := apprun.EnvOr(env, "TENANT", "default")
	publicBase := strings.TrimRight(apprun.EnvOr(env, "PUBLIC_BASE", apprun.EnvOr(env, "DOMAIN", "")), "/")
	deviceID := apprun.EnvOr(env, "DEVICE_ID", "atm-01")
	deviceSecret := apprun.EnvOr(env, "DEVICE_SECRET", "")
	identityMode := apprun.EnvOr(env, "IDENTITY", "mock")
	clamdAddr := env["CLAMD"]

	dbPath := filepath.Join(dataDir, "fdt.db")
	staging := filepath.Join(dataDir, "staging")
	quarantine := filepath.Join(dataDir, "quarantine")
	target := filepath.Join(dataDir, "released")
	for _, d := range []string{staging, quarantine, target} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	signer, err := hsm.NewSoftwareSignerFromFile("int-2026-01", filepath.Join(keys, "sk_internal.pem"))
	if err != nil {
		return err
	}
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	if deviceID != "" {
		fp := deviceSecret
		certPath := filepath.Join(keys, "device_"+deviceID+".pem")
		if _, err := os.Stat(certPath); err == nil {
			fp, err = crypto.CertFingerprintFile(certPath)
			if err != nil {
				return err
			}
		}
		if fp == "" {
			fp = "dev-secret"
		}
		_ = st.RegisterDevice(context.Background(), &store.Device{
			ID: deviceID, Name: deviceID, CertFingerprint: fp, Status: "active",
		})
	}

	dlp := manifest.ContentEngine{RuleBasedDLP: manifest.RuleBasedDLP{
		SensitiveKeywords: []string{"secret", "confidential", "身份证"},
	}}
	if clamdAddr != "" {
		c, err := av.New(clamdAddr)
		if err != nil {
			return err
		}
		if err := c.Ping(context.Background()); err != nil {
			return fmt.Errorf("clamd: %w", err)
		}
		dlp.Clam = c
	}

	svc := &portal.Service{
		Store: st, Signer: signer, DLP: dlp,
		Opts:        manifest.Options{SingleFrameBudget: 1600},
		StagingRoot: staging, QRTTL: 10 * time.Minute, Tenant: tenant,
		SourceRoot: filepath.Join(dataDir, "uploads"),
	}
	_ = os.MkdirAll(svc.SourceRoot, 0o755)

	etcDir := filepath.Dir(keys)
	ldapFile := apprun.EnvOr(env, "LDAP_CONFIG", filepath.Join(etcDir, "ldap.json"))
	formFieldsFile := apprun.EnvOr(env, "FORM_FIELDS_FILE", filepath.Join(etcDir, "form-fields.json"))
	envFile := apprun.EnvOr(env, "ENV_FILE", filepath.Join(etcDir, "fdt.env"))
	idp, resolvedMode, err := portal.BuildIdentityProvider(identityMode, ldapFile, env)
	if err != nil {
		wantProd := strings.EqualFold(strings.TrimSpace(identityMode), "ldap") ||
			strings.EqualFold(strings.TrimSpace(identityMode), "pam") ||
			strings.EqualFold(resolvedMode, "ldap") ||
			strings.EqualFold(resolvedMode, "pam")
		if wantProd || strings.EqualFold(env["ALLOW_MOCK_IDENTITY"], "0") ||
			strings.EqualFold(env["ALLOW_MOCK_IDENTITY"], "false") {
			// 已要求 LDAP/PAM / 明确禁止 mock 时，绝不静默回退到弱口令样例账号
			log.Fatalf("identity 配置失败（拒绝回退 mock）: %v", err)
		}
		log.Printf("identity 配置警告: %v；回退 mock（仅联调；生产请 IDENTITY=ldap|pam 或 ALLOW_MOCK_IDENTITY=0）", err)
		idp = identity.NewMockProvider()
		resolvedMode = "mock"
	}
	if resolvedMode == "mock" {
		log.Printf("⚠️ identity=mock：内置弱口令样例账号，禁止生产；请在管理后台切到 ldap 或 pam")
	}
	pamService := apprun.EnvOr(env, "PAM_SERVICE", identity.DefaultPAMService)
	log.Printf("identity mode=%s ldapFile=%s pamService=%s formFields=%s", resolvedMode, ldapFile, pamService, formFieldsFile)
	if _, err := os.Stat(formFieldsFile); os.IsNotExist(err) {
		_ = portal.SaveFormFields(formFieldsFile, portal.DefaultFormFields())
	}

	portalH := (&portal.Handler{
		Svc: svc, Identity: idp, Realm: "FDT Portal",
		Sessions: portal.NewSessions(),
		Bindings: &portal.FeishuBindings{Path: filepath.Join(dataDir, "feishu_bindings.json")},
		LDAPFile: ldapFile, FormFieldsFile: formFieldsFile, EnvFile: envFile,
		AdminUser:    apprun.EnvOr(env, "PORTAL_ADMIN_USER", "admin"),
		AdminPass:    env["PORTAL_ADMIN_PASSWORD"],
		AdminUsers:   parseAdminUsers(env["PORTAL_ADMIN_USERS"]),
		IdentityMode: resolvedMode,
		PAMService:   pamService,
		ReleasedRoot: target,
	}).Router()
	dataH := datasvc.New(st).Router()

	uiMux := http.NewServeMux()
	uiMux.Handle("/", portalH)
	uiMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	atmMux := http.NewServeMux()
	atmMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	atmMux.Handle("/data/", http.StripPrefix("/data", dataH))
	gwPubPath := filepath.Join(keys, "pk_gateway.pem")
	if gwPub, err := crypto.ReadPublicPEM(gwPubPath); err == nil {
		var scanner importer.Scanner = importer.RuleBasedScanner{}
		if dlp.Clam != nil {
			scanner = importer.ClamScanner{Clam: dlp.Clam}
		}
		imp := &importer.Service{
			Store: st, Signer: signer, GatewayPub: gwPub, Scanner: scanner,
			QuarantineRoot: quarantine, TargetRoot: target,
			Tenant: tenant, QRTTL: 10 * time.Minute,
		}
		atmMux.Handle("/import/", http.StripPrefix("/import", (&importer.Handler{Svc: imp}).Router()))
	} else {
		log.Printf("import 禁用: %v", err)
	}
	// 数据口明确拒绝页面路径，防止误挂 Portal。
	atmMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") ||
			strings.HasPrefix(r.URL.Path, "/app") {
			http.Error(w, "portal UI is on LISTEN, not on ATM DATA_LISTEN", http.StatusNotFound)
			return
		}
		http.NotFound(w, r)
	})

	tlsCfg, err := intranetTLSConfig(env, keys)
	if err != nil {
		return err
	}

	srvUI := &http.Server{Addr: listen, Handler: uiMux, TLSConfig: tlsCfg}
	srvATM := &http.Server{Addr: atmListen, Handler: atmMux, TLSConfig: tlsCfg}
	go serveIntranet(srvUI, "portal-ui", listen)
	go serveIntranet(srvATM, "atm-data", atmListen)
	if publicBase != "" {
		log.Printf("intranet: public base %s", publicBase)
	}
	log.Printf("intranet: Portal UI %s  |  ATM data %s (/data /import only)", listen, atmListen)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srvUI.Shutdown(ctx)
	_ = srvATM.Shutdown(ctx)
	return nil
}

func sameHostPort(a, b string) bool {
	_, pa, ea := net.SplitHostPort(a)
	_, pb, eb := net.SplitHostPort(b)
	if ea != nil || eb != nil {
		return a == b
	}
	return pa == pb
}

func intranetTLSConfig(env map[string]string, keys string) (*tls.Config, error) {
	if env["TLS"] != "1" && env["TLS"] != "true" {
		return nil, nil
	}
	tlsCert := apprun.FirstNonEmpty(env["TLS_CERT"], filepath.Join(keys, "server.pem"))
	tlsKey := apprun.FirstNonEmpty(env["TLS_KEY"], filepath.Join(keys, "server.key"))
	ca := apprun.FirstNonEmpty(env["TLS_CLIENT_CA"], filepath.Join(keys, "device_ca.pem"))
	cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
	if err != nil {
		return nil, fmt.Errorf("TLS=1 but server cert missing (%s / %s): %w", tlsCert, tlsKey, err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if _, err := os.Stat(ca); err == nil {
		mtls, err := deviceauth.ServerTLSConfig(mustReadFile(ca))
		if err != nil {
			return nil, err
		}
		cfg.ClientAuth = tls.VerifyClientCertIfGiven
		cfg.ClientCAs = mtls.ClientCAs
	}
	return cfg, nil
}

func serveIntranet(srv *http.Server, name, addr string) {
	mode := "http"
	if srv.TLSConfig != nil {
		mode = "https"
	}
	log.Printf("%s listening on %s (%s)", name, addr, mode)
	var err error
	if srv.TLSConfig != nil {
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func mustReadFile(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		log.Fatal(err)
	}
	return b
}

// parseAdminUsers turns "alice,Bob" into a lower-cased account set.
func parseAdminUsers(csv string) map[string]bool {
	out := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out[p] = true
		}
	}
	return out
}
