package token

import (
	"testing"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/hsm"
	"fdt/internal/model"
)

func TestRequestTokenRoundTrip(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	signer := hsm.NewSoftwareSigner("int-2026-01", kp.Priv)

	in := &model.RequestToken{
		ReqID:     "req-123",
		Dir:       model.DirExport,
		Applicant: model.Applicant{ID: "e1", Name: "张三", Dept: "设计部"},
		Reason:    "交付客户A版图",
		Cls:       model.ClassSecret,
		Dataset: model.Dataset{
			Count: 2, TotalSize: 30, MerkleRoot: "aa", Mode: model.ModeFull,
			DLP:     model.DLPSummary{AV: "clean", Categories: map[string]int{"源代码": 2}},
			Entries: []model.FileEntry{{Path: "a.go", Size: 10, Type: "text/x-go", HashHex: "11"}},
		},
		Nonce: "n1", Iat: time.Now().Unix(), Exp: time.Now().Add(10 * time.Minute).Unix(),
	}
	frame, err := SignRequest(signer, in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OpenRequest(frame, kp.Pub)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	if out.ReqID != in.ReqID || out.Dataset.MerkleRoot != in.Dataset.MerkleRoot {
		t.Fatalf("payload mismatch: %+v", out)
	}
	if out.Dataset.Entries[0].Path != "a.go" {
		t.Fatal("entries not preserved")
	}
	if in.V != Version || out.V != Version {
		t.Fatal("version not stamped")
	}
}

func TestTamperedFrameRejected(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	other, _ := crypto.GenerateKeyPair()
	signer := hsm.NewSoftwareSigner("int", kp.Priv)
	frame, err := SignRequest(signer, &model.RequestToken{ReqID: "r", Nonce: "n", Dataset: model.Dataset{MerkleRoot: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	// verify against the wrong public key must fail
	if _, err := OpenRequest(frame, other.Pub); err == nil {
		t.Fatal("expected verification failure with wrong public key")
	}
}

func TestReleaseTokenRoundTrip(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	signer := hsm.NewSoftwareSigner("gw-2026-01", kp.Priv)
	in := &model.ReleaseToken{
		ReqID: "req-123", Dir: model.DirExport, MerkleRoot: "aa",
		FsApprovalID: "fs-9", Approver: model.Applicant{ID: "m1", Name: "李四"},
		Nonce: "n2", Iat: time.Now().Unix(), Exp: time.Now().Add(5 * time.Minute).Unix(),
	}
	frame, err := SignRelease(signer, in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := OpenRelease(frame, kp.Pub)
	if err != nil {
		t.Fatal(err)
	}
	if out.FsApprovalID != "fs-9" || out.MerkleRoot != "aa" {
		t.Fatalf("release payload mismatch: %+v", out)
	}
}

func TestCheckValidity(t *testing.T) {
	now := time.Now()
	if err := CheckValidity(now.Add(-time.Minute).Unix(), now.Add(time.Minute).Unix(), now); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
	if err := CheckValidity(now.Add(-2*time.Minute).Unix(), now.Add(-time.Minute).Unix(), now); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	if err := CheckValidity(now.Add(time.Hour).Unix(), now.Add(2*time.Hour).Unix(), now); err != ErrNotYet {
		t.Fatalf("want ErrNotYet, got %v", err)
	}
}
