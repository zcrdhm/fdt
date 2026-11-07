// Command fdt-gateway runs the DMZ approval bridge. It supports two modes:
//
//   - Self-hosted single tenant: pass -internal-pub/-gateway-key/-feishu-* flags.
//   - Shared multi-tenant SaaS: pass -tenants <config.json> describing one entry
//     per customer site/org (each with its own PK_内网, SK_网关 and Feishu client).
//
// Use -mock (or per-tenant "mode":"mock") for offline development; supply real
// Feishu credentials for production. The approval engine is always native Feishu.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/gateway"
	"fdt/internal/hsm"
	"fdt/internal/token"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "gateway listen address")
	releaseTTL := flag.Duration("release-ttl", 72*time.Hour, "QR#2 / 历史放行码 validity window")
	tenantsPath := flag.String("tenants", "", "multi-tenant config JSON (empty = single-tenant from flags)")

	mock := flag.Bool("mock", false, "DEV ONLY: offline mock Feishu (auto-approve)")
	internalPub := flag.String("internal-pub", "keys/pk_internal.pem", "single-tenant: PK_内网 PEM")
	gatewayKey := flag.String("gateway-key", "keys/sk_gateway.pem", "single-tenant: SK_网关 PEM (ignored when -pkcs11-module is set)")
	gatewayPub := flag.String("gateway-pub", "keys/pk_gateway.pem", "PK_网关 PEM (required for PKCS#11)")
	appID := flag.String("feishu-app-id", "", "single-tenant live: Feishu app id")
	appSecret := flag.String("feishu-app-secret", "", "single-tenant live: Feishu app secret")
	approvalCode := flag.String("feishu-approval-code", "", "single-tenant live: export approval definition code")
	approvalCodeImport := flag.String("feishu-approval-code-import", "", "single-tenant live: import approval definition code (empty = use export code)")
	encryptKey := flag.String("feishu-encrypt-key", "", "single-tenant live: callback encrypt key")
	verifyToken := flag.String("feishu-verification-token", "", "single-tenant live: callback verification token")
	formFile := flag.String("feishu-form", "", "optional JSON FormWidget list for the approval definition")

	sessionDriver := flag.String("session-db-driver", "sqlite", "gateway session store driver: sqlite | postgres")
	sessionDB := flag.String("session-db", "", "durable session DB path/DSN (empty = memory only)")
	callbackCIDRs := flag.String("callback-allow", "", "CIDR file for /callback source IP allowlist (empty = unrestricted, DEV only)")
	approveToken := flag.String("approve-token", "", "shared secret for POST /hsm/approve (X-Approve-Token)")
	dualN := flag.Int("dual-control", 0, "require N distinct approvals before minting QR#2 (0 = off)")
	dualWindow := flag.Duration("dual-window", 5*time.Minute, "dual-control approval window")

	pkcs11Module := flag.String("pkcs11-module", "", "PKCS#11 module path for SK_网关 (empty = software PEM)")
	pkcs11Slot := flag.String("pkcs11-slot", "", "PKCS#11 slot index (empty = first token)")
	pkcs11Label := flag.String("pkcs11-label", "", "PKCS#11 private key CKA_LABEL")
	pkcs11PIN := flag.String("pkcs11-pin", "", "PKCS#11 user PIN")
	pkcs11KID := flag.String("pkcs11-kid", "gw-hsm", "key id stamped into QR#2")
	flag.Parse()

	var svc *gateway.Service
	var dual *hsm.DualControlSigner
	var jsapi feishu.JSAPISigner = feishu.MockJSAPI{}
	if *tenantsPath != "" {
		res, err := gateway.LoadTenantsFile(*tenantsPath)
		if err != nil {
			log.Fatalf("load tenants: %v", err)
		}
		svc = res.Service
		jsapi = res.JSAPI
		log.Printf("多租户模式：已加载 %d 个租户 %v", len(svc.Tenants()), svc.Tenants())
	} else {
		pub, err := crypto.ReadPublicPEM(*internalPub)
		if err != nil {
			log.Fatalf("load PK_内网: %v", err)
		}
		signer, err := buildSigner(*pkcs11Module, *pkcs11Slot, *pkcs11Label, *pkcs11PIN, *pkcs11KID, *gatewayKey, *gatewayPub)
		if err != nil {
			log.Fatalf("load SK_网关: %v", err)
		}
		if *dualN > 0 {
			dual = hsm.NewDualControlSigner(asHSM(signer), *dualN, *dualWindow)
			signer = dual
			log.Printf("双控签名已启用：需要 %d 人批准（POST /hsm/approve）", *dualN)
		}
		var client feishu.Client
		if *mock {
			log.Println("⚠️⚠️ 危险：-mock 已启用，飞书审批被自动放行（无真实审批）！仅限离线开发，生产严禁使用。")
			client = feishu.NewMockClient(true, "mock-approver")
		} else {
			if *appID == "" || *appSecret == "" || *approvalCode == "" {
				log.Fatal("live 模式需要 -feishu-app-id/-feishu-app-secret/-feishu-approval-code")
			}
			cfg := feishu.LiveConfig{
				AppID: *appID, AppSecret: *appSecret, ApprovalCode: *approvalCode,
				ApprovalCodeImport: *approvalCodeImport,
				EncryptKey: *encryptKey, VerificationToken: *verifyToken,
			}
			if *formFile != "" {
				ws, err := feishu.LoadFormWidgets(*formFile)
				if err != nil {
					log.Fatalf("feishu form: %v", err)
				}
				cfg.FormWidgets = ws
			}
			live := feishu.NewLiveClient(cfg, nil)
			client = live
			jsapi = live
		}
		svc = gateway.NewSingleTenant(pub, signer, client, *releaseTTL)
		log.Println("单租户（自部署）模式")
	}

	if *sessionDB != "" {
		dsn := *sessionDB
		if *sessionDriver == "sqlite" {
			dsn = "file:" + *sessionDB + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		}
		ss, err := gateway.OpenSessionStore(*sessionDriver, dsn)
		if err != nil {
			log.Fatalf("session store: %v", err)
		}
		svc.Sessions = ss
		if err := svc.RestoreSessions(context.Background()); err != nil {
			log.Fatalf("restore sessions: %v", err)
		}
		log.Printf("网关会话已持久化到 %s (%s)", *sessionDB, *sessionDriver)
	}

	handler := &gateway.Handler{Svc: svc, DualControl: dual, ApproveToken: *approveToken, JSAPI: jsapi}
	if *callbackCIDRs != "" {
		nets, err := gateway.ParseCIDRFile(*callbackCIDRs)
		if err != nil {
			log.Fatalf("callback allowlist: %v", err)
		}
		handler.CallbackAllowCIDRs = nets
		log.Printf("回调来源限制：%d 条 CIDR", len(nets))
	} else {
		log.Println("⚠️ 警告：未设置 -callback-allow，/callback 不限源（仅限离线开发）")
	}

	srv := &http.Server{Addr: *addr, Handler: handler.Router()}
	go func() {
		log.Printf("gateway listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("gateway: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func buildSigner(module, slotStr, label, pin, kid, keyPath, pubPath string) (token.Signer, error) {
	if module == "" {
		return hsm.NewSoftwareSignerFromFile(orDefault(kid, "gw-2026-01"), keyPath)
	}
	pub, err := crypto.ReadPublicPEM(pubPath)
	if err != nil {
		return nil, fmt.Errorf("PKCS#11 needs -gateway-pub: %w", err)
	}
	cfg := hsm.PKCS11Config{
		KID: orDefault(kid, "gw-hsm"), ModulePath: module, KeyLabel: label, PIN: pin, PublicKey: pub,
	}
	if slotStr != "" {
		n, err := strconv.ParseUint(slotStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("pkcs11-slot: %w", err)
		}
		cfg.Slot, cfg.SlotSet = uint(n), true
	}
	log.Printf("使用 PKCS#11 签名：module=%s label=%s", module, label)
	return hsm.NewPKCS11Signer(cfg), nil
}

func asHSM(s token.Signer) hsm.Signer {
	hs, ok := s.(hsm.Signer)
	if !ok {
		log.Fatal("signer must implement hsm.Signer (Public())")
	}
	return hs
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

