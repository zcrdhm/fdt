package gwadmin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetEnvValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fdt.env")
	if err := os.WriteFile(path, []byte("# c\nROLE=gateway\nADMIN_PASSWORD=old\nLISTEN=0.0.0.0:443\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setEnvValue(path, "ADMIN_PASSWORD", "newpass123"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, "ADMIN_PASSWORD=newpass123") || strings.Contains(s, "ADMIN_PASSWORD=old") {
		t.Fatalf("%s", s)
	}
	if !strings.Contains(s, "ROLE=gateway") {
		t.Fatalf("lost other keys: %s", s)
	}
	if err := setEnvValue(path, "NEW_KEY", "v"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "NEW_KEY=v") {
		t.Fatalf("%s", b)
	}
}
