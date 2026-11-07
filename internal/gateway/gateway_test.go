package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/hsm"
	"fdt/internal/model"
	"fdt/internal/token"
)

type tenantKeys struct {
	intKP  *crypto.KeyPair
	gwKP   *crypto.KeyPair
	intSig token.Signer
	gwSig  token.Signer
}

func newTenantKeys(t *testing.T) tenantKeys {
	t.Helper()
	ik, _ := crypto.GenerateKeyPair()
	gk, _ := crypto.GenerateKeyPair()
	return tenantKeys{
		intKP: ik, gwKP: gk,
		intSig: hsm.NewSoftwareSigner("int", ik.Priv),
		gwSig:  hsm.NewSoftwareSigner("gw", gk.Priv),
	}
}

func signQR1(t *testing.T, signer token.Signer, tenant, reqID string) string {
	t.Helper()
	tok := &model.RequestToken{
		Tenant: tenant, ReqID: reqID, Dir: model.DirExport,
		Applicant: model.Applicant{ID: "e1", Name: "张三", Dept: "设计部"},
		Reason:    "test", Cls: model.ClassSecret,
		Dataset: model.Dataset{
			Count: 1, TotalSize: 10, MerkleRoot: strings.Repeat("a", 64), Mode: model.ModeFull,
			DLP:     model.DLPSummary{AV: "clean"},
			Entries: []model.FileEntry{{Path: "f.txt", Size: 10, Type: "text/plain", HashHex: "bb"}},
		},
		Nonce: "n", Iat: time.Now().Unix(), Exp: time.Now().Add(10 * time.Minute).Unix(),
	}
	frame, err := token.SignRequest(signer, tok)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestMultiTenantRoutingAndIsolation(t *testing.T) {
	ctx := context.Background()
	a := newTenantKeys(t)
	b := newTenantKeys(t)

	svc := New(10 * time.Minute)
	fsA := feishu.NewMockClient(false, "approverA")
	fsB := feishu.NewMockClient(false, "approverB")
	if err := svc.AddTenant("acme", TenantConfig{InternalPub: a.intKP.Pub, Signer: a.gwSig, Feishu: fsA, FeishuTenantKey: "fs-acme"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddTenant("beta", TenantConfig{InternalPub: b.intKP.Pub, Signer: b.gwSig, Feishu: fsB, FeishuTenantKey: "fs-beta"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.Tenants(); len(got) != 2 || got[0] != "acme" || got[1] != "beta" {
		t.Fatalf("tenants = %v", got)
	}

	// Tenant acme submits its own QR#1 — routes and verifies fine.
	frameA := signQR1(t, a.intSig, "acme", "req-acme-1")
	sub, err := svc.SubmitQR1(ctx, frameA, "")
	if err != nil {
		t.Fatalf("acme submit failed: %v", err)
	}
	if sub.Tenant != "acme" || sub.InstanceID == "" {
		t.Fatalf("bad submission: %+v", sub)
	}

	// Approve in acme's Feishu, then QR#2 must be signed with acme's gateway key.
	if err := fsA.Approve(sub.InstanceID, "approverA"); err != nil {
		t.Fatal(err)
	}
	q2, err := svc.PollQR2(ctx, "req-acme-1")
	if err != nil {
		t.Fatal(err)
	}
	if q2.QR2Frame == "" {
		t.Fatalf("acme QR#2 not minted (status=%s)", q2.Status)
	}
	rel, err := token.OpenRelease(q2.QR2Frame, a.gwKP.Pub) // acme's PK_网关
	if err != nil {
		t.Fatalf("acme QR#2 must verify with acme PK_网关: %v", err)
	}
	if rel.Tenant != "acme" || rel.ReqID != "req-acme-1" {
		t.Fatalf("bad release token: %+v", rel)
	}
	// acme's QR#2 must NOT verify with beta's gateway key.
	if _, err := token.OpenRelease(q2.QR2Frame, b.gwKP.Pub); err == nil {
		t.Fatal("acme QR#2 wrongly verified with beta PK_网关")
	}

	// Cross-tenant forgery: take acme's frame, flip the tenant prefix to beta.
	forged := strings.Replace(frameA, "FDT1.acme.", "FDT1.beta.", 1)
	if _, err := svc.SubmitQR1(ctx, forged, ""); err == nil {
		t.Fatal("cross-tenant frame must be rejected")
	}

	// Unknown tenant prefix.
	unknown := strings.Replace(frameA, "FDT1.acme.", "FDT1.ghost.", 1)
	if _, err := svc.SubmitQR1(ctx, unknown, ""); err == nil {
		t.Fatal("unknown tenant must be rejected")
	}
}

func TestSingleTenantConvenience(t *testing.T) {
	ctx := context.Background()
	k := newTenantKeys(t)
	fs := feishu.NewMockClient(true, "auto")
	svc := NewSingleTenant(k.intKP.Pub, k.gwSig, fs, 5*time.Minute)
	frame := signQR1(t, k.intSig, "default", "req-1")
	sub, err := svc.SubmitQR1(ctx, frame, "")
	if err != nil {
		t.Fatal(err)
	}
	q2, err := svc.PollQR2(ctx, sub.ReqID)
	if err != nil || q2.QR2Frame == "" {
		t.Fatalf("single-tenant QR#2 not minted: %v %v", q2, err)
	}
}

func TestSubmitRejectsExpiredSubscription(t *testing.T) {
	ctx := context.Background()
	k := newTenantKeys(t)
	fs := feishu.NewMockClient(true, "auto")
	svc := New(5 * time.Minute)
	if err := svc.AddTenant("acme", TenantConfig{
		InternalPub: k.intKP.Pub, Signer: k.gwSig, Feishu: fs,
		ExpiresAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	frame := signQR1(t, k.intSig, "acme", "req-expired")
	if _, err := svc.SubmitQR1(ctx, frame, ""); !errors.Is(err, ErrSubscriptionExpired) {
		t.Fatalf("want ErrSubscriptionExpired, got %v", err)
	}
	st := svc.LookupSubscription("")
	if !st.Expired || st.Tenant != "acme" {
		t.Fatalf("lookup = %+v", st)
	}
}
