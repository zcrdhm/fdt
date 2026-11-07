// Package e2e wires every module into a single offline end-to-end run: intranet
// Portal -> signed QR#1 -> DMZ gateway -> native Feishu approval (mock) ->
// signed QR#2 -> ATM verifies + streams data over HTTP + writes to USB +
// one-shot consume. Only the true hardware/Feishu boundary is mocked; all
// crypto, hashing, Merkle, CAS and audit logic is real.
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
	"fdt/internal/datasvc"
	"fdt/internal/feishu"
	"fdt/internal/gateway"
	"fdt/internal/hsm"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/portal"
	"fdt/internal/store"
)

// Config controls a run. FixtureDir is the data to ferry; WorkDir holds the
// database, staging area and the simulated USB.
type Config struct {
	FixtureDir  string
	WorkDir     string
	AutoApprove bool
	Approver    string
}

// Report captures the outcome of every step for assertion/printing.
type Report struct {
	Steps         []string
	ReqID         string
	Mode          model.ManifestMode
	FileCount     int
	QR1Len        int
	QR2Len        int
	MerkleRoot    string
	USBSerial     string
	WrittenBytes  int64
	FinalStatus   store.Status
	AuditChainOK  bool
	NoUSBErr      error
	NoUSBState    atm.State
	DoubleScanErr error
	WrittenFiles  []string
}

func (r *Report) log(format string, a ...any) {
	r.Steps = append(r.Steps, fmt.Sprintf(format, a...))
}

// Run executes the full flow and returns a report. It returns an error only for
// setup failures; expected negative-case errors are captured in the Report.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	rep := &Report{}
	if cfg.Approver == "" {
		cfg.Approver = "李四"
	}
	must := func(step string, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", step, err)
		}
		rep.log("✓ %s", step)
		return nil
	}

	// Work dirs.
	dbPath := filepath.Join(cfg.WorkDir, "fdt.db")
	staging := filepath.Join(cfg.WorkDir, "staging")
	usbDir := filepath.Join(cfg.WorkDir, "usb")
	for _, d := range []string{staging, usbDir, filepath.Dir(dbPath)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	// Keys: SK_内网 signs QR#1, SK_网关 signs QR#2 (HSM/dual-control seam).
	intKP, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	gwKP, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	intSigner := hsm.NewSoftwareSigner("int-2026-01", intKP.Priv)
	gwSigner := hsm.NewDualControlSigner(hsm.NewSoftwareSigner("gw-2026-01", gwKP.Priv), 2, time.Minute)
	rep.log("✓ 生成 SK_内网 / SK_网关（网关侧双控 m=2）")

	// Store + registered ATM device.
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	const atmID, devSecret = "atm-01", "dev-cert-fingerprint-abc123"
	if err := st.RegisterDevice(ctx, &store.Device{
		ID: atmID, Name: "一号摆渡机", Location: "一号门", CertFingerprint: devSecret, Status: "active",
	}); err != nil {
		return nil, err
	}
	if err := must("注册 ATM 设备并入库", nil); err != nil {
		return nil, err
	}

	// 1) Portal: stage data, build manifest, sign QR#1.
	dlp := manifest.RuleBasedDLP{SensitiveKeywords: []string{"secret", "confidential"}}
	portalSvc := &portal.Service{
		Store: st, Signer: intSigner, DLP: dlp,
		Opts: manifest.Options{SingleFrameBudget: 1600}, StagingRoot: staging, QRTTL: 10 * time.Minute,
	}
	created, err := portalSvc.CreateExportRequest(ctx, portal.CreateInput{
		Applicant: model.Applicant{ID: "e1234", Name: "张三", Dept: "设计部"},
		Reason:    "交付客户A的版图数据", Cls: model.ClassSecret, SourceDir: cfg.FixtureDir,
	})
	if err != nil {
		return nil, err
	}
	rep.ReqID, rep.Mode, rep.FileCount = created.ReqID, created.Mode, created.Count
	rep.QR1Len, rep.MerkleRoot = len(created.QR1Frame), created.MerkleRoot
	if err := must(fmt.Sprintf("Portal 建单+清单+签QR#1（%d文件, mode=%s, QR#1=%dB）",
		created.Count, created.Mode, rep.QR1Len), nil); err != nil {
		return nil, err
	}

	// 2) Gateway (DMZ): verify QR#1, create Feishu approval (pending), then the
	//    approver acts and two officers authorize the HSM to sign QR#2.
	fs := feishu.NewMockClient(false, cfg.Approver)
	gw := gateway.NewSingleTenant(intKP.Pub, gwSigner, fs, 10*time.Minute)
	sub, err := gw.SubmitQR1(ctx, created.QR1Frame, "")
	if err != nil {
		return nil, err
	}
	if err := must(fmt.Sprintf("网关验QR#1签名+建飞书审批实例 %s（状态 %s）", sub.InstanceID, sub.Status), nil); err != nil {
		return nil, err
	}
	// Dual-control: two authorized officers approve the release-signing round.
	if err := gwSigner.Approve("officer-A"); err != nil {
		return nil, err
	}
	if err := gwSigner.Approve("officer-B"); err != nil {
		return nil, err
	}
	// Approver grants the approval in Feishu.
	if err := fs.Approve(sub.InstanceID, cfg.Approver); err != nil {
		return nil, err
	}
	q2, err := gw.PollQR2(ctx, created.ReqID)
	if err != nil {
		return nil, err
	}
	if q2.QR2Frame == "" {
		return nil, fmt.Errorf("gateway did not mint QR#2 (status=%s)", q2.Status)
	}
	rep.QR2Len = len(q2.QR2Frame)
	if err := must(fmt.Sprintf("飞书审批通过(%s)+双控HSM签名→QR#2（%dB）", q2.Status, rep.QR2Len), nil); err != nil {
		return nil, err
	}

	// 3) Data service over real HTTP on localhost (ephemeral port).
	ds := httptest.NewServer(datasvc.New(st).Router())
	defer ds.Close()
	if err := must("启动 datasvc（ATM 经 HTTP 拉数据，设备凭据鉴权）", nil); err != nil {
		return nil, err
	}

	// 4) ATM: negative case — scan with no USB must be rejected.
	usbMgr := atm.NewMockManager()
	fetcher := &atm.HTTPFetcher{BaseURL: ds.URL, DeviceID: atmID, Secret: devSecret}
	kiosk := atm.NewKiosk(atmID, st, gwKP.Pub, fetcher, usbMgr)
	kiosk.Progress = func(p atm.WriteProgress) {}

	_, noUsbErr := kiosk.ProcessScan(ctx, q2.QR2Frame)
	rep.NoUSBErr = noUsbErr
	rep.NoUSBState = kiosk.State()
	if noUsbErr != atm.ErrNoUSB {
		return nil, fmt.Errorf("expected ErrNoUSB without a device, got %v", noUsbErr)
	}
	rep.log("✓ 无U盘扫码被拒绝（%v），状态=%s", noUsbErr, rep.NoUSBState)

	// 5) Insert USB and redeem for real.
	if err := usbMgr.Insert("USB-SER-0001", usbDir, 1<<30, true, "exfat"); err != nil {
		return nil, err
	}
	if err := must("插入U盘（USB-SER-0001, 1GiB, exfat）", nil); err != nil {
		return nil, err
	}
	res, err := kiosk.ProcessScan(ctx, q2.QR2Frame)
	if err != nil {
		return nil, fmt.Errorf("redeem failed: %w", err)
	}
	rep.USBSerial, rep.WrittenBytes = res.USBSerial, res.WrittenBytes
	if err := must(fmt.Sprintf("ATM 验QR#2+拉数据+写盘+回读校验+CAS核销（%dB, %d文件）",
		res.WrittenBytes, res.FileCount), nil); err != nil {
		return nil, err
	}

	// 6) Verify final state: request redeemed, files on USB, audit chain intact.
	req, err := st.GetRequest(ctx, created.ReqID)
	if err != nil {
		return nil, err
	}
	rep.FinalStatus = req.Status
	if req.Status != store.StatusRedeemed {
		return nil, fmt.Errorf("request status = %s, want REDEEMED", req.Status)
	}
	rep.WrittenFiles, _ = listFiles(res.OutDir)
	if err := must(fmt.Sprintf("申请单状态=REDEEMED，U盘落盘 %d 个文件", len(rep.WrittenFiles)), nil); err != nil {
		return nil, err
	}
	if err := st.VerifyAuditChain(ctx); err != nil {
		return nil, fmt.Errorf("audit chain broken: %w", err)
	}
	rep.AuditChainOK = true
	rep.log("✓ 审计哈希链校验通过（防篡改）")

	// 7) Replay the same QR#2 — must be rejected as already consumed.
	if _, err := kiosk.ProcessScan(ctx, q2.QR2Frame); err != nil {
		rep.DoubleScanErr = err
	}
	if rep.DoubleScanErr != atm.ErrAlreadyConsumed {
		return nil, fmt.Errorf("expected ErrAlreadyConsumed on replay, got %v", rep.DoubleScanErr)
	}
	rep.log("✓ 同一QR#2重放被拒（一次性核销生效）")

	return rep, nil
}

func listFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
