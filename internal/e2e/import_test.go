package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

type importRig struct {
	ctx      context.Context
	store    *store.Store
	gw       *gateway.Service
	fs       *feishu.MockClient
	gwSigner *hsm.DualControlSigner
	kiosk    *atm.Kiosk
	usbMgr   *atm.MockManager
	srcDir   string
	work     string
}

func setupImport(t *testing.T, files map[string]string) *importRig {
	t.Helper()
	ctx := context.Background()
	work := t.TempDir()
	srcDir := filepath.Join(work, "usb")
	for rel, body := range files {
		p := filepath.Join(srcDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	intKP, _ := crypto.GenerateKeyPair()
	gwKP, _ := crypto.GenerateKeyPair()
	intSigner := hsm.NewSoftwareSigner("int", intKP.Priv)
	gwSigner := hsm.NewDualControlSigner(hsm.NewSoftwareSigner("gw", gwKP.Priv), 2, time.Minute)

	st, err := store.OpenSQLite(filepath.Join(work, "fdt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	const devID, secret = "atm-01", "dev-secret"
	if err := st.RegisterDevice(ctx, &store.Device{ID: devID, Name: "atm", CertFingerprint: secret, Status: "active"}); err != nil {
		t.Fatal(err)
	}

	impSvc := &importer.Service{
		Store: st, Signer: intSigner, GatewayPub: gwKP.Pub,
		Scanner:        importer.RuleBasedScanner{},
		QuarantineRoot: filepath.Join(work, "quarantine"),
		TargetRoot:     filepath.Join(work, "target"),
		Tenant:         "default", QRTTL: 10 * time.Minute,
	}
	impSrv := httptest.NewServer((&importer.Handler{Svc: impSvc}).Router())
	t.Cleanup(impSrv.Close)

	fs := feishu.NewMockClient(false, "李四")
	gw := gateway.NewSingleTenant(intKP.Pub, gwSigner, fs, 10*time.Minute)

	usbMgr := atm.NewMockManager()
	if err := usbMgr.Insert("USB-SRC-1", srcDir, 1<<30, true, "exfat"); err != nil {
		t.Fatal(err)
	}
	kiosk := atm.NewKiosk(devID, st, gwKP.Pub, nil, usbMgr)
	kiosk.Importer = &importer.HTTPClient{BaseURL: impSrv.URL, DeviceID: devID, Secret: secret}
	kiosk.ImportDLP = manifest.RuleBasedDLP{}

	return &importRig{ctx: ctx, store: st, gw: gw, fs: fs, gwSigner: gwSigner,
		kiosk: kiosk, usbMgr: usbMgr, srcDir: srcDir, work: work}
}

// approveQR1 drives QR#1 through the gateway + mock Feishu and returns QR#2.
func (r *importRig) approveQR1(t *testing.T, qr1 string) string {
	t.Helper()
	sub, err := r.gw.SubmitQR1(r.ctx, qr1, "")
	if err != nil {
		t.Fatalf("submit QR#1: %v", err)
	}
	if err := r.gwSigner.Approve("officer-A"); err != nil {
		t.Fatal(err)
	}
	if err := r.gwSigner.Approve("officer-B"); err != nil {
		t.Fatal(err)
	}
	if err := r.fs.Approve(sub.InstanceID, "李四"); err != nil {
		t.Fatal(err)
	}
	q2, err := r.gw.PollQR2(r.ctx, sub.ReqID)
	if err != nil {
		t.Fatal(err)
	}
	if q2.QR2Frame == "" {
		t.Fatalf("no QR#2 (status=%s)", q2.Status)
	}
	return q2.QR2Frame
}

func TestImportReleaseHappyPath(t *testing.T) {
	r := setupImport(t, map[string]string{
		"docs/readme.md":   "# hello\n",
		"data/records.csv": "id,name\n1,alice\n",
		"notes.txt":        "plain text\n",
	})
	applicant := model.Applicant{ID: "e1", Name: "张三", Dept: "设计部"}

	start, err := r.kiosk.StartImport(r.ctx, applicant, "导入客户资料", model.ClassConfidential, []string{"."})
	if err != nil {
		t.Fatalf("StartImport: %v", err)
	}
	if start.Count != 3 || len(start.MerkleRoot) != 64 {
		t.Fatalf("bad index result: %+v", start)
	}
	qr2 := r.approveQR1(t, start.QR1Frame)

	res, err := r.kiosk.CompleteImport(r.ctx, start.ReqID, qr2)
	if err != nil {
		t.Fatalf("CompleteImport: %v", err)
	}
	if res.Status != store.StatusReleased {
		t.Fatalf("status = %s, want RELEASED", res.Status)
	}
	// released files must exist under target/<reqId>
	for _, f := range []string{"docs/readme.md", "data/records.csv", "notes.txt"} {
		if _, err := os.Stat(filepath.Join(res.ReleasedDir, f)); err != nil {
			t.Fatalf("released file missing %s: %v", f, err)
		}
	}
	req, _ := r.store.GetRequest(r.ctx, start.ReqID)
	if req.Status != store.StatusReleased {
		t.Fatalf("request status = %s", req.Status)
	}
	tok, _ := r.store.GetToken(r.ctx, start.ReqID)
	if !tok.Consumed {
		t.Fatal("token not consumed after release")
	}
	if err := r.store.VerifyAuditChain(r.ctx); err != nil {
		t.Fatalf("audit chain: %v", err)
	}
}

func TestImportBlockedByScanner(t *testing.T) {
	r := setupImport(t, map[string]string{
		"good.txt":    "ok\n",
		"payload.exe": "MZ\x90\x00 not really an exe",
	})
	applicant := model.Applicant{ID: "e1", Name: "张三"}
	start, err := r.kiosk.StartImport(r.ctx, applicant, "导入", model.ClassInternal, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	qr2 := r.approveQR1(t, start.QR1Frame)

	res, err := r.kiosk.CompleteImport(r.ctx, start.ReqID, qr2)
	if err == nil {
		t.Fatal("expected blocked error")
	}
	if res == nil || res.Status != store.StatusBlocked {
		t.Fatalf("status = %v, want BLOCKED", res)
	}
	if len(res.Findings) == 0 {
		t.Fatal("expected findings for the executable")
	}
	req, _ := r.store.GetRequest(r.ctx, start.ReqID)
	if req.Status != store.StatusBlocked {
		t.Fatalf("request status = %s, want BLOCKED", req.Status)
	}
	// nothing released to target
	if _, err := os.Stat(filepath.Join(r.work, "target", start.ReqID)); !os.IsNotExist(err) {
		t.Fatal("blocked import must not release to target")
	}
	// token NOT consumed on block
	tok, _ := r.store.GetToken(r.ctx, start.ReqID)
	if tok.Consumed {
		t.Fatal("blocked import must not consume the token")
	}
}

func TestImportRejectsSwappedUSB(t *testing.T) {
	r := setupImport(t, map[string]string{"a.txt": "original\n"})
	applicant := model.Applicant{ID: "e1", Name: "张三"}
	start, err := r.kiosk.StartImport(r.ctx, applicant, "导入", model.ClassInternal, []string{"."})
	if err != nil {
		t.Fatal(err)
	}
	qr2 := r.approveQR1(t, start.QR1Frame)

	// Attacker swaps the USB contents after indexing but before upload.
	if err := os.WriteFile(filepath.Join(r.srcDir, "a.txt"), []byte("TAMPERED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.srcDir, "extra.bin"), []byte("surprise"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = r.kiosk.CompleteImport(r.ctx, start.ReqID, qr2)
	if err == nil {
		t.Fatal("expected merkle mismatch after swap")
	}
	req, _ := r.store.GetRequest(r.ctx, start.ReqID)
	if req.Status == store.StatusReleased {
		t.Fatal("swapped data must not be released")
	}
	if _, err := os.Stat(filepath.Join(r.work, "target", start.ReqID)); !os.IsNotExist(err) {
		t.Fatal("swapped import must not reach target")
	}
}

func TestImportNoUSBRejected(t *testing.T) {
	r := setupImport(t, map[string]string{"a.txt": "x"})
	r.usbMgr.Remove()
	_, err := r.kiosk.StartImport(r.ctx, model.Applicant{ID: "e1"}, "导入", model.ClassInternal, []string{"."})
	if err != atm.ErrNoUSB {
		t.Fatalf("want ErrNoUSB, got %v", err)
	}
}
