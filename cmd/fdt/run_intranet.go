package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
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

func runIntranet(env map[string]string) error {
	listen := envOr(env, "LISTEN", "0.0.0.0:8080")
	dataDir := envOr(env, "DATA_DIR", "/var/lib/fdt")
	keys := envOr(env, "KEYS_DIR", "/etc/fdt/keys")
	tenant := envOr(env, "TENANT", "default")
	deviceID := envOr(env, "DEVICE_ID", "atm-01")
	deviceSecret := envOr(env, "DEVICE_SECRET", "")
	identityMode := envOr(env, "IDENTITY", "mock")
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
		Opts: manifest.Options{SingleFrameBudget: 1600},
		StagingRoot: staging, QRTTL: 10 * time.Minute, Tenant: tenant,
		SourceRoot: filepath.Join(dataDir, "uploads"),
	}
	_ = os.MkdirAll(svc.SourceRoot, 0o755)

	etcDir := filepath.Dir(keys)
	ldapFile := envOr(env, "LDAP_CONFIG", filepath.Join(etcDir, "ldap.json"))
	formFieldsFile := envOr(env, "FORM_FIELDS_FILE", filepath.Join(etcDir, "form-fields.json"))
	envFile := envOr(env, "ENV_FILE", filepath.Join(etcDir, "fdt.env"))
	idp, resolvedMode, err := portal.BuildIdentityProvider(identityMode, ldapFile, env)
	if err != nil {
		log.Printf("identity 配置警告: %v；回退 mock", err)
		idp = identity.NewMockProvider()
		resolvedMode = "mock"
	}
	log.Printf("identity mode=%s ldapFile=%s formFields=%s", resolvedMode, ldapFile, formFieldsFile)
	if _, err := os.Stat(formFieldsFile); os.IsNotExist(err) {
		_ = portal.SaveFormFields(formFieldsFile, portal.DefaultFormFields())
	}

	portalH := (&portal.Handler{
		Svc: svc, Identity: idp, Realm: "FDT Portal",
		Sessions: portal.NewSessions(),
		Bindings: &portal.FeishuBindings{Path: filepath.Join(dataDir, "feishu_bindings.json")},
		LDAPFile: ldapFile, FormFieldsFile: formFieldsFile, EnvFile: envFile,
		AdminUser:    envOr(env, "PORTAL_ADMIN_USER", "admin"),
		AdminPass:    env["PORTAL_ADMIN_PASSWORD"],
		AdminUsers:   parseAdminUsers(env["PORTAL_ADMIN_USERS"]),
		IdentityMode: resolvedMode,
		ReleasedRoot: target,
	}).Router()
	dataH := datasvc.New(st).Router()

	root := http.NewServeMux()
	root.Handle("/", portalH)
	root.Handle("/data/", http.StripPrefix("/data", dataH))

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
		root.Handle("/import/", http.StripPrefix("/import", (&importer.Handler{Svc: imp}).Router()))
	} else {
		log.Printf("import 禁用: %v", err)
	}

	srv := &http.Server{Addr: listen, Handler: root}
	// TLS is opt-in (TLS=1 in fdt.env). Default HTTP keeps one-click deploy simple;
	// turn on when you have terminated TLS or want ATM mTLS on the wire.
	if env["TLS"] == "1" || env["TLS"] == "true" {
		tlsCert := filepath.Join(keys, "server.pem")
		tlsKey := filepath.Join(keys, "server.key")
		ca := filepath.Join(keys, "device_ca.pem")
		cert, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
		if err != nil {
			return fmt.Errorf("TLS=1 but server cert missing: %w", err)
		}
		cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		if _, err := os.Stat(ca); err == nil {
			mtls, err := deviceauth.ServerTLSConfig(mustReadFile(ca))
			if err != nil {
				return err
			}
			cfg.ClientAuth = tls.VerifyClientCertIfGiven
			cfg.ClientCAs = mtls.ClientCAs
		}
		srv.TLSConfig = cfg
	}

	go func() {
		log.Printf("intranet listening on %s (UI /  data /data/  import /import/)", listen)
		var err error
		if srv.TLSConfig != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
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
