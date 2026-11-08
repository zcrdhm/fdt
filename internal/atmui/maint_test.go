package atmui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaintAuthCheckAndChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	m := newMaintAuth(path, "secret1")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stored := strings.TrimSpace(string(raw))
	if !looksLikeBcrypt(stored) {
		t.Fatalf("file must store bcrypt hash, got %q", stored)
	}
	if stored == "secret1" {
		t.Fatal("plaintext must not be written to file")
	}
	if !m.check("secret1") {
		t.Fatal("bootstrap password should work")
	}
	if m.check("wrong") {
		t.Fatal("wrong password must fail")
	}
	if err := m.change("secret1", "secret2"); err != nil {
		t.Fatal(err)
	}
	m2 := newMaintAuth(path, "ignored-plaintext-should-not-override-file")
	if !m2.check("secret2") {
		t.Fatal("persisted hash should load")
	}
	if m2.check("secret1") {
		t.Fatal("old password must no longer work")
	}
	if err := m2.change("bad", "secret9"); err != errMaintBadOld {
		t.Fatalf("want bad old, got %v", err)
	}
}

func TestMaintAuthMigrateLegacyPlaintextFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	if err := os.WriteFile(path, []byte("legacy-plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := newMaintAuth(path, "unused")
	raw, _ := os.ReadFile(path)
	if !looksLikeBcrypt(strings.TrimSpace(string(raw))) {
		t.Fatalf("legacy file should be migrated to bcrypt, got %q", raw)
	}
	if !m.check("legacy-plain") {
		t.Fatal("migrated password should still verify")
	}
}

func TestMaintAuthEnvHashBootstrap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	h, err := hashMaintPassword("from-hash")
	if err != nil {
		t.Fatal(err)
	}
	m := newMaintAuth(path, h)
	if !m.check("from-hash") {
		t.Fatal("env bcrypt hash bootstrap should work")
	}
}
