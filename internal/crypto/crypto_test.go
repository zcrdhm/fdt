package crypto

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestSignVerify(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("request-token-payload")
	sig := kp.Sign(msg)
	if err := Verify(kp.Pub, msg, sig); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if err := Verify(kp.Pub, []byte("tampered"), sig); err == nil {
		t.Fatal("verify should fail on tampered message")
	}
}

func TestKeyPEMRoundTrip(t *testing.T) {
	dir := t.TempDir()
	kp, _ := GenerateKeyPair()
	privPath := filepath.Join(dir, "sk.pem")
	pubPath := filepath.Join(dir, "pk.pem")
	if err := WritePrivatePEM(privPath, kp.Priv); err != nil {
		t.Fatal(err)
	}
	if err := WritePublicPEM(pubPath, kp.Pub); err != nil {
		t.Fatal(err)
	}
	// private key file must be owner-only.
	if fi, err := os.Stat(privPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key too permissive: %v", fi.Mode().Perm())
	}
	priv2, err := ReadPrivatePEM(privPath)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := ReadPublicPEM(pubPath)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("round-trip")
	sig := ed25519.Sign(priv2, msg)
	if err := Verify(pub2, msg, sig); err != nil {
		t.Fatalf("verify after reload failed: %v", err)
	}
}
