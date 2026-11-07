package atmui

import (
	"path/filepath"
	"testing"
)

func TestMaintAuthCheckAndChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pass")
	m := newMaintAuth(path, "secret1")
	if !m.check("secret1") {
		t.Fatal("bootstrap password should work")
	}
	if m.check("wrong") {
		t.Fatal("wrong password must fail")
	}
	if err := m.change("secret1", "secret2"); err != nil {
		t.Fatal(err)
	}
	m2 := newMaintAuth(path, "ignored")
	if !m2.check("secret2") {
		t.Fatal("persisted password should load")
	}
	if err := m2.change("bad", "secret9"); err != errMaintBadOld {
		t.Fatalf("want bad old, got %v", err)
	}
}
