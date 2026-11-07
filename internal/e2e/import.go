package e2e

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"fdt/internal/atm"
	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/gateway"
	"fdt/internal/hsm"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
)

// ImportReport captures the inbound-direction run for printing/assertion.
type ImportReport struct {
	Steps        []string
	ReqID        string
	Count        int
	MerkleRoot   string
	Status       store.Status
	ReleasedDir  string
	Released     []string
	NoUSBErr     error
	BlockedReqID string
	BlockedStat  store.Status
	BlockedFind  []string
	AuditChainOK bool
}

func (r *ImportReport) log(f string, a ...any) { r.Steps = append(r.Steps, fmt.Sprintf(f, a...)) }

// ImportConfig controls a demo/test import run.
type ImportConfig struct {
	WorkDir  string
	Approver string
	// Files placed on the simulated source USB for the happy path.
	Files map[string]string
}

// RunImport exercises the full inbound flow offline: ATM indexes a source USB ->
// intranet signs import QR#1 -> gateway/Feishu approve -> QR#2 -> ATM streams the
// USB into quarantine -> anti-swap Merkle recheck -> AV/DLP scan -> release. It
// also demonstrates the no-USB rejection and the scanner-blocked path.
func RunImport(ctx context.Context, cfg ImportConfig) (*ImportReport, error) {
	rep := &ImportReport{}
	if cfg.Approver == "" {
		cfg.Approver = "李四"
	}
	if cfg.Files == nil {
		cfg.Files = map[string]string{
			"docs/readme.md":   "# hello\n",
			"data/records.csv": "id,name\n1,alice\n",
			"notes.txt":        "plain text\n",
		}
	}
	must := func(step string, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
		rep.log("✓ %s", step)
		return nil
	}

	// Work dirs.
	srcDir := filepath.Join(cfg.WorkDir, "src-usb")
	quarantine := filepath.Join(cfg.WorkDir, "quarantine")
	target := filepath.Join(cfg.WorkDir, "target")
	for _, d := range []string{quarantine, target} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := writeFiles(srcDir, cfg.Files); err != nil {
		return nil, err
	}

	// Keys + store + device.
	intKP, _ := crypto.GenerateKeyPair()
	gwKP, _ := crypto.GenerateKeyPair()
	intSigner := hsm.NewSoftwareSigner("int-2026-01", intKP.Priv)
	gwSigner := hsm.NewDualControlSigner(hsm.NewSoftwareSigner("gw-2026-01", gwKP.Priv), 2, time.Minute)

	st, err := store.OpenSQLite(filepath.Join(cfg.WorkDir, "fdt.db"))
	if err != nil {
		return nil, err
	}
	defer st.Close()
	const atmID, secret = "atm-01", "dev-secret"
	if err := st.RegisterDevice(ctx, &store.Device{ID: atmID, Name: "一号摆渡机", CertFingerprint: secret, Status: "active"}); err != nil {
		return nil, err
	}

	// Importer service over HTTP (intranet).
	impSvc := &importer.Service{
		Store: st, Signer: intSigner, GatewayPub: gwKP.Pub,
		Scanner:        importer.RuleBasedScanner{},
		QuarantineRoot: quarantine, TargetRoot: target,
		Tenant: "default", QRTTL: 10 * time.Minute,
	}
	impSrv := httptest.NewServer((&importer.Handler{Svc: impSvc}).Router())
	defer impSrv.Close()
	if err := must("启动 importer 服务（隔离区+扫描+释放）", nil); err != nil {
		return nil, err
	}

	// Gateway (DMZ) with mock Feishu.
	fs := feishu.NewMockClient(false, cfg.Approver)
	gw := gateway.NewSingleTenant(intKP.Pub, gwSigner, fs, 10*time.Minute)

	// ATM kiosk wired for import.
	usbMgr := atm.NewMockManager()
	kiosk := atm.NewKiosk(atmID, st, gwKP.Pub, nil, usbMgr)
	kiosk.Importer = &importer.HTTPClient{BaseURL: impSrv.URL, DeviceID: atmID, Secret: secret}
	kiosk.ImportDLP = manifest.RuleBasedDLP{}

	// Negative: no USB -> StartImport rejected.
	if _, err := kiosk.StartImport(ctx, model.Applicant{ID: "e1234", Name: "张三", Dept: "设计部"}, "导入", model.ClassConfidential, []string{"."}, nil); err != atm.ErrNoUSB {
		return nil, fmt.Errorf("expected ErrNoUSB without a device, got %v", err)
	}
	rep.NoUSBErr = atm.ErrNoUSB
	rep.log("✓ 无U盘发起导入被拒绝（%v）", atm.ErrNoUSB)

	// Insert source USB and index -> QR#1.
	if err := usbMgr.Insert("USB-SRC-0001", srcDir, 1<<30, true, "exfat"); err != nil {
		return nil, err
	}
	start, err := kiosk.StartImport(ctx, model.Applicant{ID: "e1234", Name: "张三", Dept: "设计部"}, "导入客户资料", model.ClassConfidential, []string{"."}, nil)
	if err != nil {
		return nil, err
	}
	rep.ReqID, rep.Count, rep.MerkleRoot = start.ReqID, start.Count, start.MerkleRoot
	if err := must(fmt.Sprintf("插入源U盘+索引+签 QR#1（%d文件, QR#1=%dB）", start.Count, len(start.QR1Frame)), nil); err != nil {
		return nil, err
	}

	// Approve through the gateway.
	qr2, err := approveThroughGateway(ctx, gw, gwSigner, fs, start.QR1Frame)
	if err != nil {
		return nil, err
	}
	if err := must("网关验签+飞书审批通过+双控签 QR#2", nil); err != nil {
		return nil, err
	}

	// Complete: stream USB -> quarantine -> verify -> scan -> release.
	res, err := kiosk.CompleteImport(ctx, start.ReqID, qr2)
	if err != nil {
		return nil, fmt.Errorf("CompleteImport: %w", err)
	}
	rep.Status, rep.ReleasedDir, rep.Released = res.Status, res.ReleasedDir, res.Released
	if res.Status != store.StatusReleased {
		return nil, fmt.Errorf("import status = %s, want RELEASED", res.Status)
	}
	if err := must(fmt.Sprintf("上传→隔离区→重算Merkle防换盘→扫描通过→释放（%d文件到 %s）", len(res.Released), res.ReleasedDir), nil); err != nil {
		return nil, err
	}

	// Blocked path: an executable triggers the scanner.
	blockedSrc := filepath.Join(cfg.WorkDir, "src-usb-bad")
	if err := writeFiles(blockedSrc, map[string]string{"ok.txt": "fine\n", "payload.exe": "MZ\x90\x00"}); err != nil {
		return nil, err
	}
	usbMgr.Remove()
	if err := usbMgr.Insert("USB-SRC-0002", blockedSrc, 1<<30, true, "exfat"); err != nil {
		return nil, err
	}
	bstart, err := kiosk.StartImport(ctx, model.Applicant{ID: "e1234", Name: "张三"}, "导入(含可执行)", model.ClassInternal, []string{"."}, nil)
	if err != nil {
		return nil, err
	}
	bqr2, err := approveThroughGateway(ctx, gw, gwSigner, fs, bstart.QR1Frame)
	if err != nil {
		return nil, err
	}
	bres, berr := kiosk.CompleteImport(ctx, bstart.ReqID, bqr2)
	if berr == nil {
		return nil, fmt.Errorf("expected blocked import to fail")
	}
	rep.BlockedReqID, rep.BlockedStat, rep.BlockedFind = bstart.ReqID, bres.Status, bres.Findings
	if bres.Status != store.StatusBlocked {
		return nil, fmt.Errorf("blocked status = %s", bres.Status)
	}
	rep.log("✓ 含可执行文件的导入被 AV/DLP 拦截（状态 %s，命中 %v），未释放、未核销", bres.Status, bres.Findings)

	if err := st.VerifyAuditChain(ctx); err != nil {
		return nil, fmt.Errorf("audit chain broken: %w", err)
	}
	rep.AuditChainOK = true
	rep.log("✓ 审计哈希链校验通过")
	return rep, nil
}

// approveThroughGateway drives a QR#1 through submit -> dual-control -> Feishu
// approve -> poll, returning the QR#2 frame.
func approveThroughGateway(ctx context.Context, gw *gateway.Service, gwSigner *hsm.DualControlSigner, fs *feishu.MockClient, qr1 string) (string, error) {
	sub, err := gw.SubmitQR1(ctx, qr1, "")
	if err != nil {
		return "", err
	}
	if err := gwSigner.Approve("officer-A"); err != nil {
		return "", err
	}
	if err := gwSigner.Approve("officer-B"); err != nil {
		return "", err
	}
	if err := fs.Approve(sub.InstanceID, "李四"); err != nil {
		return "", err
	}
	q2, err := gw.PollQR2(ctx, sub.ReqID)
	if err != nil {
		return "", err
	}
	if q2.QR2Frame == "" {
		return "", fmt.Errorf("no QR#2 (status=%s)", q2.Status)
	}
	return q2.QR2Frame, nil
}

func writeFiles(dir string, files map[string]string) error {
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}
