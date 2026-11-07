// Command fdt-portal runs the intranet side: the upload Portal HTTP API and the
// data service that streams approved data to authenticated ATMs, both backed by
// the shared store. Deploy these inside the air-gapped network only.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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

func main() {
	portalAddr := flag.String("portal-addr", "127.0.0.1:8080", "Portal listen address")
	dataAddr := flag.String("datasvc-addr", "127.0.0.1:8081", "data service listen address")
	db := flag.String("db", "fdt.db", "SQLite database path (prod: use Postgres DSN)")
	driver := flag.String("db-driver", "sqlite", "database driver: sqlite | postgres")
	internalKey := flag.String("internal-key", "keys/sk_internal.pem", "SK_内网 PEM path")
	staging := flag.String("staging", "staging", "quarantine staging root")
	deviceID := flag.String("register-device", "", "optional: register an ATM device id on startup")
	deviceSecret := flag.String("device-secret", "", "device credential / cert fingerprint")
	deviceCert := flag.String("register-device-cert", "", "optional: PEM cert whose fingerprint registers the device")
	budget := flag.Int("frame-budget", 1600, "single-frame QR budget in bytes")
	webDir := flag.String("web-dir", "", "optional custom frontend dir; empty serves the embedded UI")
	tenant := flag.String("tenant", "default", "this site's tenant id (multi-tenant gateway routing)")
	sourceRoot := flag.String("source-root", "", "confine legacy sourceDir under this root")

	importerAddr := flag.String("importer-addr", "127.0.0.1:8082", "import service listen address")
	gatewayPub := flag.String("gateway-pub", "keys/pk_gateway.pem", "PK_网关 PEM (verify import QR#2)")
	quarantine := flag.String("quarantine", "quarantine", "import quarantine landing root")
	targetRoot := flag.String("target", "released", "import release destination root")

	clamdAddr := flag.String("clamd", "", "clamd address (unix:///var/run/clamd.sock or host:3310); empty = no AV")
	sensitive := flag.String("dlp-keywords", "secret,confidential,身份证", "comma-separated content DLP keywords")

	tlsCert := flag.String("tls-cert", "", "server TLS certificate PEM (datasvc+importer)")
	tlsKey := flag.String("tls-key", "", "server TLS key PEM")
	tlsClientCA := flag.String("tls-client-ca", "", "CA PEM that signed ATM client certs (enables mTLS)")

	identityMode := flag.String("identity", "none", "applicant identity source: none | mock | ldap")
	ldapURL := flag.String("ldap-url", "", "LDAP URL, e.g. ldaps://dc.corp:636")
	ldapStartTLS := flag.Bool("ldap-starttls", false, "upgrade ldap:// with StartTLS")
	ldapCA := flag.String("ldap-ca", "", "CA PEM to verify the DC cert")
	ldapInsecure := flag.Bool("ldap-insecure", false, "DEV ONLY: skip DC cert verification")
	ldapServiceDN := flag.String("ldap-service-dn", "", "service account DN for the search step")
	ldapServicePass := flag.String("ldap-service-pass", "", "service account password")
	ldapBase := flag.String("ldap-base", "", "search base")
	ldapFilter := flag.String("ldap-filter", "(sAMAccountName=%s)", "search filter")
	flag.Parse()

	signer, err := hsm.NewSoftwareSignerFromFile("int-2026-01", *internalKey)
	if err != nil {
		log.Fatalf("load SK_内网: %v", err)
	}
	st, err := store.Open(*driver, dsn(*driver, *db))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if *deviceID != "" {
		fp := *deviceSecret
		if *deviceCert != "" {
			fp, err = crypto.CertFingerprintFile(*deviceCert)
			if err != nil {
				log.Fatalf("device cert: %v", err)
			}
		}
		if fp == "" {
			log.Fatal("register-device needs -device-secret or -register-device-cert")
		}
		if err := st.RegisterDevice(context.Background(), &store.Device{
			ID: *deviceID, Name: *deviceID, CertFingerprint: fp, Status: "active",
		}); err != nil {
			log.Fatalf("register device: %v", err)
		}
		log.Printf("registered device %s fingerprint=%s", *deviceID, fp)
	}

	if err := os.MkdirAll(*staging, 0o755); err != nil {
		log.Fatalf("staging: %v", err)
	}

	dlp := manifest.ContentEngine{
		RuleBasedDLP: manifest.RuleBasedDLP{SensitiveKeywords: splitCSV(*sensitive)},
	}
	if *clamdAddr != "" {
		c, err := av.New(*clamdAddr)
		if err != nil {
			log.Fatalf("clamd: %v", err)
		}
		if err := c.Ping(context.Background()); err != nil {
			log.Fatalf("clamd ping: %v", err)
		}
		dlp.Clam = c
		log.Printf("clamd 已连接: %s", *clamdAddr)
	} else {
		log.Println("⚠️ 警告：未设置 -clamd，导出 DLP 的 AV 字段为 not_scanned")
	}

	svc := &portal.Service{
		Store: st, Signer: signer,
		DLP:         dlp,
		Opts:        manifest.Options{SingleFrameBudget: *budget},
		StagingRoot: *staging, QRTTL: 10 * time.Minute, Tenant: *tenant,
		SourceRoot: *sourceRoot,
	}
	if *sourceRoot == "" {
		log.Println("⚠️ 警告：未设置 -source-root；JSON sourceDir 可读任意目录。生产请用 multipart 上传或设置 -source-root。")
	}

	var idp identity.Provider
	switch *identityMode {
	case "none":
		log.Println("identity=none：申请人取自请求体，仅限离线开发；生产请配 ldap")
	case "mock":
		idp = identity.NewMockProvider()
		log.Println("⚠️ 警告：identity=mock 使用内置弱口令样例账号，仅限离线开发")
	case "ldap":
		p, err := identity.NewLDAPProvider(identity.LDAPConfig{
			URL: *ldapURL, StartTLS: *ldapStartTLS, CACertFile: *ldapCA,
			InsecureSkipVerify: *ldapInsecure, ServiceBindDN: *ldapServiceDN,
			ServicePassword: *ldapServicePass, SearchBase: *ldapBase, SearchFilter: *ldapFilter,
		})
		if err != nil {
			log.Fatalf("ldap provider: %v", err)
		}
		idp = p
		log.Printf("identity=ldap：AD 认证已启用（%s）", *ldapURL)
	default:
		log.Fatalf("unknown -identity mode %q (none|mock|ldap)", *identityMode)
	}

	portalSrv := &http.Server{Addr: *portalAddr, Handler: (&portal.Handler{
		Svc: svc, WebDir: *webDir, Identity: idp, Realm: "FDT Portal",
	}).Router()}
	dataSrv := &http.Server{Addr: *dataAddr, Handler: datasvc.New(st).Router()}
	if err := applyTLS(dataSrv, *tlsCert, *tlsKey, *tlsClientCA); err != nil {
		log.Fatalf("datasvc tls: %v", err)
	}

	var importSrv *http.Server
	if gwPub, err := crypto.ReadPublicPEM(*gatewayPub); err == nil {
		for _, d := range []string{*quarantine, *targetRoot} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				log.Fatalf("import dirs: %v", err)
			}
		}
		var scanner importer.Scanner = importer.RuleBasedScanner{}
		if dlp.Clam != nil {
			scanner = importer.ClamScanner{Clam: dlp.Clam}
			log.Println("importer 使用 ClamScanner（fail-closed）")
		} else {
			log.Println("⚠️ 警告：importer 使用 RuleBasedScanner（开发用）；生产请配 -clamd")
		}
		impSvc := &importer.Service{
			Store: st, Signer: signer, GatewayPub: gwPub,
			Scanner:        scanner,
			QuarantineRoot: *quarantine, TargetRoot: *targetRoot,
			Tenant: *tenant, QRTTL: 10 * time.Minute,
		}
		importSrv = &http.Server{Addr: *importerAddr, Handler: (&importer.Handler{Svc: impSvc}).Router()}
		if err := applyTLS(importSrv, *tlsCert, *tlsKey, *tlsClientCA); err != nil {
			log.Fatalf("importer tls: %v", err)
		}
		go serve(importSrv, "importer")
	} else {
		log.Printf("import 方向已禁用（未找到 PK_网关 %s: %v）", *gatewayPub, err)
	}

	go serve(portalSrv, "Portal")
	go serve(dataSrv, "datasvc")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = portalSrv.Shutdown(ctx)
	_ = dataSrv.Shutdown(ctx)
	if importSrv != nil {
		_ = importSrv.Shutdown(ctx)
	}
}

func serve(srv *http.Server, name string) {
	log.Printf("%s listening on %s", name, srv.Addr)
	var err error
	if srv.TLSConfig != nil {
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("%s: %v", name, err)
	}
}

func applyTLS(srv *http.Server, certFile, keyFile, clientCA string) error {
	if certFile == "" && keyFile == "" && clientCA == "" {
		return nil
	}
	if certFile == "" || keyFile == "" {
		return errors.New("both -tls-cert and -tls-key are required when enabling TLS")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if clientCA != "" {
		mtls, err := deviceauth.ServerTLSConfig(mustRead(clientCA))
		if err != nil {
			return err
		}
		cfg.ClientCAs = mtls.ClientCAs
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		log.Printf("%s: mTLS 客户端证书校验已启用", srv.Addr)
	}
	srv.TLSConfig = cfg
	return nil
}

func mustRead(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	return b
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := s[start:i]
			for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
				part = part[1:]
			}
			for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func dsn(driver, db string) string {
	if driver == "sqlite" {
		if db == ":memory:" {
			return "file::memory:"
		}
		return "file:" + db + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	return db
}
