package atmui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindChromeOrSkip(t *testing.T) {
	_, err := findChrome()
	if err != nil {
		t.Skip(err.Error())
	}
}

func TestKioskProfileDirCreates(t *testing.T) {
	t.Setenv("ATM_KIOSK_PROFILE", filepath.Join(t.TempDir(), "chrome-prof"))
	d := kioskProfileDir(nil)
	if d == "" {
		t.Fatal("empty profile dir")
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		t.Fatalf("profile dir missing: %v", err)
	}
}
