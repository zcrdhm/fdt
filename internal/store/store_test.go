package store

import (
	"context"
	"testing"
	"time"

	"fdt/internal/model"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedRequest(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	r := &Request{
		ID: id, Dir: model.DirExport, ApplicantID: "e1", ApplicantName: "张三",
		Cls: model.ClassSecret, Status: StatusPending, FileCount: 2, TotalSize: 100,
		MerkleRoot: "root", ManifestMode: model.ModeFull, DataDir: "/tmp/x",
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.CreateRequest(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureToken(ctx, id, "nonce1"); err != nil {
		t.Fatal(err)
	}
}

func TestListRequestsOptFiltersByApplicantAndActor(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-a")
	r2 := &Request{
		ID: "req-b", Dir: model.DirExport, ApplicantID: "e2", ApplicantName: "李四",
		Cls: model.ClassInternal, Status: StatusPending, FileCount: 1, TotalSize: 10,
		MerkleRoot: "root2", ManifestMode: model.ModeFull, DataDir: "/tmp/y",
		CreatedAt: time.Now().Add(time.Second), ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := s.CreateRequest(ctx, r2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAudit(ctx, "portal:lisi", "", "req-b", "request_created_auth",
		map[string]string{"account": "lisi"}); err != nil {
		t.Fatal(err)
	}

	mine, err := s.ListRequestsOpt(ctx, ListRequestsOpt{ApplicantIDs: []string{"e1"}, Limit: 50})
	if err != nil || len(mine) != 1 || mine[0].ID != "req-a" {
		t.Fatalf("by applicant: got %+v err=%v", idsOf(mine), err)
	}
	byActor, err := s.ListRequestsOpt(ctx, ListRequestsOpt{PortalActor: "portal:lisi", Limit: 50})
	if err != nil || len(byActor) != 1 || byActor[0].ID != "req-b" {
		t.Fatalf("by actor: got %+v err=%v", idsOf(byActor), err)
	}
	all, err := s.ListRequestsOpt(ctx, ListRequestsOpt{All: true, Limit: 50})
	if err != nil || len(all) != 2 {
		t.Fatalf("all: got %d err=%v", len(all), err)
	}
	ok, err := s.RequestOwnedBy(ctx, "req-b", "portal:lisi", nil)
	if err != nil || !ok {
		t.Fatalf("owned by actor: ok=%v err=%v", ok, err)
	}
	ok, err = s.RequestOwnedBy(ctx, "req-a", "portal:lisi", nil)
	if err != nil || ok {
		t.Fatalf("not owned: ok=%v err=%v", ok, err)
	}
}

func idsOf(list []*Request) []string {
	out := make([]string, len(list))
	for i, r := range list {
		out[i] = r.ID
	}
	return out
}

func TestConsumeIsOneShotCAS(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-1")

	ok, err := s.Consume(ctx, "req-1", "atm-A", "USB-SER-1", "fs-1", "root")
	if err != nil || !ok {
		t.Fatalf("first consume should succeed: ok=%v err=%v", ok, err)
	}
	// second consume (another ATM) must fail the CAS => duplicate egress signal
	ok2, err := s.Consume(ctx, "req-1", "atm-B", "USB-SER-2", "fs-1", "root")
	if err != nil {
		t.Fatal(err)
	}
	if ok2 {
		t.Fatal("second consume must not succeed (one-shot)")
	}
	r, err := s.GetRequest(ctx, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusRedeemed || r.USBSerial != "USB-SER-1" || r.RedeemATM != "atm-A" {
		t.Fatalf("request not marked redeemed by first ATM: %+v", r)
	}
}

func TestLeasePreventsConcurrentClaim(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-2")

	got, err := s.Claim(ctx, "req-2", "atm-A", 90*time.Second)
	if err != nil || !got {
		t.Fatalf("atm-A should acquire lease: got=%v err=%v", got, err)
	}
	got2, err := s.Claim(ctx, "req-2", "atm-B", 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got2 {
		t.Fatal("atm-B must not acquire lease while atm-A holds it")
	}
	// same holder can renew
	got3, _ := s.Claim(ctx, "req-2", "atm-A", 90*time.Second)
	if !got3 {
		t.Fatal("holder should be able to renew its own lease")
	}
	// release lets another ATM claim
	if err := s.Release(ctx, "req-2", "atm-A"); err != nil {
		t.Fatal(err)
	}
	got4, _ := s.Claim(ctx, "req-2", "atm-B", 90*time.Second)
	if !got4 {
		t.Fatal("atm-B should claim after release")
	}
}

func TestConsumeAfterLeaseExpiredAllowsOther(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-3")
	if _, err := s.Claim(ctx, "req-3", "atm-A", -time.Second); err != nil { // already-expired lease
		t.Fatal(err)
	}
	got, _ := s.Claim(ctx, "req-3", "atm-B", 90*time.Second)
	if !got {
		t.Fatal("atm-B should claim an expired lease")
	}
}

func TestAuditChainIntegrityAndTamper(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-4")
	for _, ev := range []string{"created", "qr1_issued", "redeemed"} {
		if _, err := s.AppendAudit(ctx, "system", "atm-A", "req-4", ev, map[string]string{"k": "v"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.VerifyAuditChain(ctx); err != nil {
		t.Fatalf("valid chain failed verification: %v", err)
	}
	// tamper with a stored event; verification must catch it
	if _, err := s.db.ExecContext(ctx, `UPDATE audit_events SET event='tampered' WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyAuditChain(ctx); err == nil {
		t.Fatal("tampered chain should fail verification")
	}
}

func TestFilesPersisted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	seedRequest(t, s, "req-5")
	entries := []model.FileEntry{
		{Path: "a.go", Size: 10, Type: "text/x-go", HashHex: "aa", Leaf: 0},
		{Path: "z.zip#m.txt", Size: 5, Type: "text/plain", HashHex: "bb", InArchive: "z.zip", Leaf: -1},
	}
	if err := s.AddFiles(ctx, "req-5", entries); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRequestFiles(ctx, "req-5")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("files = %d, want 2", len(got))
	}
	names, err := s.PhysicalFileNames(ctx, []string{"req-5"}, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(names["req-5"]) != 1 || names["req-5"][0] != "a.go" {
		t.Fatalf("physical names = %#v", names["req-5"])
	}
}
