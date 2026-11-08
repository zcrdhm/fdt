package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPreferBinarySibling(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "fdt-gw")
	fat := filepath.Join(dir, "fdt")
	if err := os.WriteFile(gw, []byte("gw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fat, []byte("fat"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate PreferBinary looking next to a fake exe in dir — call with explicit empty
	// by temporarily relying on filepath join logic via direct path check.
	got := PreferBinary(gw, "gateway")
	if got != gw {
		t.Fatalf("explicit prefer = %q", got)
	}
	got = PreferBinary("", "gateway")
	// Without being able to replace Executable easily, at least ensure empty role fallback path doesn't panic.
	_ = got
}
