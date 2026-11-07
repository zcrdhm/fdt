package hsm

import (
	"testing"
	"time"

	"fdt/internal/crypto"
)

func TestDualControlRequiresQuorum(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	inner := NewSoftwareSigner("gw", kp.Priv)
	dc := NewDualControlSigner(inner, 2, time.Minute)

	if _, err := dc.Sign([]byte("release")); err != ErrQuorumNotMet {
		t.Fatalf("sign without quorum should fail, got %v", err)
	}
	if err := dc.Approve("A"); err != nil {
		t.Fatal(err)
	}
	if _, err := dc.Sign([]byte("release")); err != ErrQuorumNotMet {
		t.Fatalf("1 of 2 approvals should still fail, got %v", err)
	}
	if err := dc.Approve("A"); err != ErrDuplicate {
		t.Fatalf("duplicate approver should be rejected, got %v", err)
	}
	if err := dc.Approve("B"); err != nil {
		t.Fatal(err)
	}
	sig, err := dc.Sign([]byte("release"))
	if err != nil {
		t.Fatalf("sign with quorum failed: %v", err)
	}
	if len(sig) == 0 {
		t.Fatal("empty signature")
	}
	// Round resets: a second sign needs fresh approvals.
	if _, err := dc.Sign([]byte("release2")); err != ErrQuorumNotMet {
		t.Fatalf("quorum should reset after signing, got %v", err)
	}
}

func TestDualControlApprovalExpiry(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	dc := NewDualControlSigner(NewSoftwareSigner("gw", kp.Priv), 2, 10*time.Millisecond)
	_ = dc.Approve("A")
	_ = dc.Approve("B")
	time.Sleep(20 * time.Millisecond) // approvals expire
	if _, err := dc.Sign([]byte("x")); err != ErrQuorumNotMet {
		t.Fatalf("expired approvals should not meet quorum, got %v", err)
	}
}

func TestSoftwareSignerVerifies(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	s := NewSoftwareSigner("kid-1", kp.Priv)
	msg := []byte("payload")
	sig, err := s.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if s.KID() != "kid-1" {
		t.Fatalf("kid = %s", s.KID())
	}
	if err := crypto.Verify(s.Public(), msg, sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestPKCS11StubUnavailable(t *testing.T) {
	s := NewPKCS11Signer(PKCS11Config{KID: "hsm-1"})
	if _, err := s.Sign([]byte("x")); err != ErrPKCS11Unavailable {
		t.Fatalf("stub should report unavailable, got %v", err)
	}
}
