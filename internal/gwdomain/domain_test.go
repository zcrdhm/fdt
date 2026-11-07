package gwdomain

import (
	"path/filepath"
	"testing"
)

func TestNormalizeAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "domain.json")
	c := Config{
		Domain:  "HTTPS://FDT-GW.Example.com/path",
		Aliases: []string{"www.fdt-gw.example.com", "fdt-gw.example.com", ""},
		Email:   " ops@x.com ",
	}
	if err := SaveFile(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "fdt-gw.example.com" {
		t.Fatalf("domain=%q", got.Domain)
	}
	if len(got.Aliases) != 1 || got.Aliases[0] != "www.fdt-gw.example.com" {
		t.Fatalf("aliases=%v", got.Aliases)
	}
	if got.Email != "ops@x.com" {
		t.Fatalf("email=%q", got.Email)
	}
	st := got.ToStatus()
	if !st.Bound || st.PublicBase != "https://fdt-gw.example.com" {
		t.Fatalf("status=%+v", st)
	}
}

func TestCheckDNSEmpty(t *testing.T) {
	r := CheckDNS("")
	if r.Matched || r.Message == "" {
		t.Fatalf("%+v", r)
	}
}
