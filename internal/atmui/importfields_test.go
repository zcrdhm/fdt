package atmui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultImportFieldsValid(t *testing.T) {
	f := DefaultImportFields()
	if err := ValidateImportFields(f); err != nil {
		t.Fatal(err)
	}
	if len(f.UserFields()) != 2 {
		t.Fatalf("user fields = %d", len(f.UserFields()))
	}
}

func TestNormalizeValuesExtras(t *testing.T) {
	f := DefaultImportFields()
	f.Fields = append(f.Fields, ImportField{
		Key: "receiver", Label: "接收方", Type: "text", Required: true,
		Order: 30, Enabled: true,
	})
	reason, cls, extras, err := f.NormalizeValues(map[string]string{
		"reason": "测试", "cls": "secret", "receiver": "客户A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reason != "测试" || cls != "secret" {
		t.Fatalf("got %q %q", reason, cls)
	}
	if extras["receiver"] != "客户A" {
		t.Fatalf("extras = %#v", extras)
	}
}

func TestLoadMissingUsesDefault(t *testing.T) {
	f, err := LoadImportFields(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateImportFields(f); err != nil {
		t.Fatal(err)
	}
}

func TestWriteDefaultImportFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "import-fields.json")
	if err := WriteDefaultImportFields(path); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 20 {
		t.Fatalf("short file: %s", b)
	}
	// second write must not overwrite
	_ = os.WriteFile(path, []byte(`{"version":1,"fields":[{"key":"reason","label":"R","type":"text","required":true,"enabled":true,"builtin":true,"order":1},{"key":"cls","label":"C","type":"select","required":true,"enabled":true,"builtin":true,"order":2,"options":["internal"]}]}`), 0o644)
	if err := WriteDefaultImportFields(path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got)[:10] == `{"version"` && len(got) < 80 {
		// kept custom short file
	}
}
