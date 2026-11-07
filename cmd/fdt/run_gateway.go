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

	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/gateway"
	"fdt/internal/gwacme"
	"fdt/internal/gwadmin"
	"fdt/internal/gwdomain"
	"fdt/internal/hsm"
)

func runGateway(env map[string]string) error {
	listen := envOr(env, "LISTEN", "0.0.0.0:8090")
	keys := envOr(env, "KEYS_DIR", "/etc/fdt/keys")
	dataDir := envOr(env, "DATA_DIR", "/var/lib/fdt")
	etcDir := filepath.Dir(keys)
	mock := env["MOCK_FEISHU"] == "1" || env["MOCK_FEISHU"] == "true"
	useTLS := env["TLS"] == "1" || env["TLS"] == "true"
	tenantsFile := strings.TrimSpace(env["TENANTS_FILE"])
	tenantsDir := strings.TrimSpace(env["TENANTS_DIR"])
	if tenantsDir == "" {
		tenantsDir = filepath.Join(etcDir, "tenants")
	}
	adminPass := env["ADMIN_PASSWORD"]
	adminUser := envOr(env, "ADMIN_USER", "admin")
	enableAdmin := adminPass != ""
	acmeFile := envOr(env, "ACME_CONFIG", filepath.Join(etcDir, "acme.json"))
	domainFile := envOr(env, "DOMAIN_CONFIG", filepath.Join(etcDir, "domain.json"))

	svc, jsapi, err := buildGatewayService(env, keys, tenantsFile, mock)
	if err != nil {
		return err
	}

	var adminStore *gwadmin.Store
	if enableAdmin || tenantsFile != "" {
		if tenantsFile == "" {
			tenantsFile = filepath.Join(etcDir, "tenants.json")
		}
		adminStore = &gwadmin.Store{Dir: tenantsDir, Index: tenantsFile, Service: svc}
		_ = adminStore.EnsureLayout()
		if err := adminStore.SyncAll(); err != nil {
			log.Printf("gwadmin sync: %v", err)
		}
	}

	sessPath := filepath.Join(dataDir, "sessions", "gw.db")
	_ = os.MkdirAll(filepath.Dir(sessPath), 0o755)
	ss, err := gateway.OpenSessionStore("sqlite", "file:"+sessPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return err
	}
	svc.Sessions = ss
	if err := svc.RestoreSessions(context.Background()); err != nil {
		return err
	}

	acmeMgr := &gwacme.Manager{}
	acmeCfg := gwacme.FromEnv(env, filepath.Join(dataDir, "acme"))
	if domainCfg, err := gwdomain.LoadFile(domainFile); err == nil && domainCfg.Domain != "" {
		if acmeCfg.Domain == "" {
			acmeCfg.Domain = domainCfg.Domain
		}
		if len(acmeCfg.Aliases) == 0 {
			acmeCfg.Aliases = domainCfg.Aliases
		}
		if acmeCfg.Email == "" {
			acmeCfg.Email = domainCfg.Email
		}
		log.Printf("已绑定域名 %s publicBase=%s", domainCfg.Domain, domainCfg.PublicBase())
	}
	if fileCfg, err := gwacme.LoadFile(acmeFile); err == nil && fileCfg.Domain != "" {
		// File overrides env when present (admin UI writes here).
		if fileCfg.Cache == "" {
			fileCfg.Cache = acmeCfg.Cache
		}
		if fileCfg.HTTPAddr == "" {
			fileCfg.HTTPAddr = acmeCfg.HTTPAddr
		}
		if !acmeCfg.Enabled && fileCfg.Enabled {
			acmeCfg = fileCfg
		} else if fileCfg.Enabled || acmeCfg.Enabled {
			acmeCfg.Enabled = true
			if fileCfg.Domain != "" {
				acmeCfg.Domain = fileCfg.Domain
			}
			if len(fileCfg.Aliases) > 0 {
				acmeCfg.Aliases = fileCfg.Aliases
			}
			if fileCfg.Email != "" {
				acmeCfg.Email = fileCfg.Email
			}
			if fileCfg.Cache != "" {
				acmeCfg.Cache = fileCfg.Cache
			}
			if fileCfg.HTTPAddr != "" {
				acmeCfg.HTTPAddr = fileCfg.HTTPAddr
			}
		}
	}

	h := &gateway.Handler{Svc: svc, JSAPI: jsapi}
	if enableAdmin && adminStore != nil {
		auth := gwadmin.NewAuth(adminUser, adminPass)
		saveDomain := func(c gwdomain.Config) error {
			return gwdomain.SaveFile(domainFile, c)
		}
		envFile := envOr(env, "ENV_FILE", filepath.Join(etcDir, "fdt.env"))
		admin := &gwadmin.Handler{
			Store: adminStore, Auth: auth, Tenants: gwadmin.NewTenantSessions(),
			ACME: acmeMgr, ACMEFile: acmeFile, DomainFile: domainFile, EnvFile: envFile,
			ApplyDomain: saveDomain,
			ApplyACME: func(c gwacme.Config) error {
				if c.Cache == "" {
					c.Cache = filepath.Join(dataDir, "acme")
				}
				if c.HTTPAddr == "" {
					c.HTTPAddr = "0.0.0.0:80"
				}
				if err := gwacme.SaveFile(acmeFile, c); err != nil {
					return err
				}
				return acmeMgr.Start(c)
			},
		}
		h.MountAdmin = admin.Mount
		log.Printf("运营后台 /admin/ 用户=%s ；注册 /register/ ；租户后台 /console/", adminUser)
	}

	srv := &http.Server{Addr: listen, Handler: h.Router()}

	if acmeCfg.Enabled && acmeCfg.Domain != "" {
		if err := acmeMgr.Start(acmeCfg); err != nil {
			return fmt.Errorf("ACME: %w", err)
		}
		srv.TLSConfig = acmeMgr.TLSConfig()
		if listen == "0.0.0.0:8090" || listen == ":8090" {
			listen = "0.0.0.0:443"
			srv.Addr = listen
		}
		log.Printf("Let's Encrypt ACME 已启用 domain=%s email=%s cache=%s http-01=%s",
			acmeCfg.Domain, acmeCfg.Email, acmeCfg.Cache, acmeCfg.HTTPAddr)
	} else if useTLS {
		certFile := envOr(env, "TLS_CERT", filepath.Join(etcDir, "tls", "fullchain.pem"))
		keyFile := envOr(env, "TLS_KEY", filepath.Join(etcDir, "tls", "privkey.pem"))
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("TLS=1 但证书不可用（%s / %s）: %w；或改用 ACME=1 + ACME_DOMAIN", certFile, keyFile, err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	go func() {
		var err error
		if srv.TLSConfig != nil {
			log.Printf("gateway HTTPS listening on %s", listen)
			err = srv.ListenAndServeTLS("", "")
		} else {
			log.Printf("gateway HTTP listening on %s", listen)
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	acmeMgr.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func buildGatewayService(env map[string]string, keys, tenantsFile string, mock bool) (*gateway.Service, feishu.JSAPISigner, error) {
	jsapi := feishu.JSAPISigner(feishu.MockJSAPI{})
	if tenantsFile != "" {
		if st, err := os.Stat(tenantsFile); err == nil && !st.IsDir() {
			b, _ := os.ReadFile(tenantsFile)
			if len(b) > 10 {
				res, err := gateway.LoadTenantsFile(tenantsFile)
				if err == nil && len(res.Service.Tenants()) > 0 {
					log.Printf("多租户：%v（%s）", res.Service.Tenants(), tenantsFile)
					return res.Service, res.JSAPI, nil
				}
				if err != nil {
					log.Printf("TENANTS_FILE 暂不可用（%v），尝试单租户密钥或空注册表", err)
				}
			}
		}
	}

	pubPath := filepath.Join(keys, "pk_internal.pem")
	keyPath := filepath.Join(keys, "sk_gateway.pem")
	if _, err := os.Stat(pubPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			pub, err := crypto.ReadPublicPEM(pubPath)
			if err != nil {
				return nil, nil, err
			}
			signer, err := hsm.NewSoftwareSignerFromFile("gw-2026-01", keyPath)
			if err != nil {
				return nil, nil, err
			}
			var client feishu.Client
			if mock {
				log.Println("⚠️ MOCK 飞书自动审批已启用")
				client = feishu.NewMockClient(true, "mock-approver")
			} else {
				appID := env["FEISHU_APP_ID"]
				secret := env["FEISHU_APP_SECRET"]
				code := env["FEISHU_APPROVAL_CODE"]
				codeImport := env["FEISHU_APPROVAL_CODE_IMPORT"]
				if appID == "" || secret == "" || code == "" {
					if env["ADMIN_PASSWORD"] != "" {
						client = feishu.NewMockClient(false, "pending")
						log.Println("单租户密钥存在但未配 FEISHU_*；default 租户用 mock(pending)，请用后台添加租户或填飞书凭据")
					} else {
						return nil, nil, errFeishuCreds
					}
				} else {
					live := feishu.NewLiveClient(feishu.LiveConfig{
						AppID: appID, AppSecret: secret, ApprovalCode: code,
						ApprovalCodeImport: codeImport,
						EncryptKey: env["FEISHU_ENCRYPT_KEY"], VerificationToken: env["FEISHU_VERIFY_TOKEN"],
					}, nil)
					client = live
					jsapi = live
				}
			}
			log.Println("单租户网关（default）")
			return gateway.NewSingleTenant(pub, signer, client, releaseTTLFromEnv(env)), jsapi, nil
		}
	}

	if env["ADMIN_PASSWORD"] != "" || tenantsFile != "" {
		log.Println("空多租户注册表（通过 /admin 添加租户）")
		return gateway.New(releaseTTLFromEnv(env)), jsapi, nil
	}
	return nil, nil, fmt.Errorf("缺少网关密钥（%s）或 TENANTS_FILE / ADMIN_PASSWORD", keys)
}

func releaseTTLFromEnv(env map[string]string) time.Duration {
	if v := env["RELEASE_TTL"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 72 * time.Hour
}

type simpleError string

func (e simpleError) Error() string { return string(e) }

const errFeishuCreds = simpleError("请在 /etc/fdt/fdt.env 填写 FEISHU_APP_ID / FEISHU_APP_SECRET / FEISHU_APPROVAL_CODE，或 install 时加 --mock / 设置 ADMIN_PASSWORD")
