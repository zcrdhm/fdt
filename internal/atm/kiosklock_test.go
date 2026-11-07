package atm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLockdownAuthorizesOnlyDataPort(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "usb1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "1-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usb1", "authorized_default"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "1-1", "authorized"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Lockdown{USBDevices: root, DataPorts: []string{"1-1"}}).Apply(); err != nil {
		t.Fatal(err)
	}
	def, _ := os.ReadFile(filepath.Join(root, "usb1", "authorized_default"))
	auth, _ := os.ReadFile(filepath.Join(root, "1-1", "authorized"))
	if strings.TrimSpace(string(def)) != "0" || strings.TrimSpace(string(auth)) != "1" {
		t.Fatalf("def=%q auth=%q", def, auth)
	}
	if err := (Lockdown{USBDevices: root, DataPorts: []string{"../evil"}}).Apply(); err == nil {
		t.Fatal("path escape should fail")
	}
}

func TestNFTRules(t *testing.T) {
	got, err := NFTRules([]string{"10.0.0.0/8", "192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "policy drop") || !strings.Contains(got, "ip daddr 10.0.0.0/8 accept") {
		t.Fatalf("ruleset:\n%s", got)
	}
	if _, err := NFTRules([]string{"10.0.0.0/8; drop"}); err == nil {
		t.Fatal("injection should fail")
	}
}
