package portal

import (
	"path/filepath"
	"testing"
)

func TestDefaultFormFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "form-fields.json")
	def := DefaultFormFields()
	if err := SaveFormFields(path, def); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFormFields(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.UserFields()) != 2 {
		t.Fatalf("user fields = %d", len(got.UserFields()))
	}
	exp := got.FeishuWidgetsExport()
	if len(exp) < 9 {
		t.Fatalf("feishu export too short: %d", len(exp))
	}
}

func TestNormalizeExtrasCustom(t *testing.T) {
	f := DefaultFormFields()
	f.Fields = append(f.Fields, FormField{
		Key: "receiver", Label: "接收方", Type: "input", Source: "user",
		Required: true, Enabled: true, Order: 30,
		FeishuField: "receiver", FeishuType: "input", MaxLen: 64,
	})
	reason, cls, extras, err := f.NormalizeExtras(map[string]string{
		"reason": "交付", "cls": "secret", "receiver": "客户A",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reason != "交付" || cls != "secret" || extras["receiver"] != "客户A" {
		t.Fatalf("got reason=%s cls=%s extras=%v", reason, cls, extras)
	}
}
