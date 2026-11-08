package atmui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ImportField is one user-visible field on the ATM「导入申请」page.
// USB path picker is fixed in the UI and not listed here.
type ImportField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | textarea | select | number
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"` // for select; value==label unless "value|label"
	Default  string   `json:"default,omitempty"`
	Hint     string   `json:"hint,omitempty"`
	Order    int      `json:"order"`
	Enabled  bool     `json:"enabled"`
	// Builtin keys reason / cls map to QR#1 core fields; others go to extras.
	Builtin bool `json:"builtin,omitempty"`
}

// ImportFieldsFile is the ATM-local config (default /opt/fdt-atm/etc/import-fields.json).
type ImportFieldsFile struct {
	Version int           `json:"version"`
	Fields  []ImportField `json:"fields"`
}

var importKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// DefaultImportFields matches today's hardcoded ATM import form.
func DefaultImportFields() ImportFieldsFile {
	return ImportFieldsFile{
		Version: 1,
		Fields: []ImportField{
			{Key: "reason", Label: "事由", Type: "textarea", Required: true, Builtin: true,
				Default: "导入资料", Order: 10, Enabled: true},
			{Key: "cls", Label: "密级", Type: "select", Required: true, Builtin: true,
				Options: []string{"internal", "confidential", "secret"},
				Default: "confidential", Order: 20, Enabled: true},
		},
	}
}

// promoteReasonTextarea upgrades legacy reason type=text → textarea (multi-line).
func promoteReasonTextarea(f *ImportFieldsFile) {
	if f == nil {
		return
	}
	for i := range f.Fields {
		if f.Fields[i].Key == "reason" && f.Fields[i].Type == "text" {
			f.Fields[i].Type = "textarea"
		}
	}
}

// LoadImportFields reads path; missing file → defaults.
func LoadImportFields(path string) (ImportFieldsFile, error) {
	if path == "" {
		return DefaultImportFields(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultImportFields(), nil
		}
		return ImportFieldsFile{}, err
	}
	var f ImportFieldsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return ImportFieldsFile{}, err
	}
	if len(f.Fields) == 0 {
		return DefaultImportFields(), nil
	}
	if f.Version == 0 {
		f.Version = 1
	}
	promoteReasonTextarea(&f)
	if err := ValidateImportFields(f); err != nil {
		return ImportFieldsFile{}, err
	}
	return f, nil
}

// ValidateImportFields checks keys/types; reason+cls must exist and be enabled.
func ValidateImportFields(f ImportFieldsFile) error {
	seen := map[string]bool{}
	hasReason, hasCls := false, false
	for _, field := range f.Fields {
		if !field.Enabled {
			continue
		}
		if !importKeyRe.MatchString(field.Key) {
			return fmt.Errorf("import-fields: invalid key %q", field.Key)
		}
		if seen[field.Key] {
			return fmt.Errorf("import-fields: duplicate key %q", field.Key)
		}
		seen[field.Key] = true
		switch field.Type {
		case "text", "textarea", "select", "number":
		default:
			return fmt.Errorf("import-fields: %s bad type %q", field.Key, field.Type)
		}
		if field.Type == "select" && len(field.Options) == 0 {
			return fmt.Errorf("import-fields: %s select needs options", field.Key)
		}
		if field.Label == "" {
			return fmt.Errorf("import-fields: %s needs label", field.Key)
		}
		if field.Key == "reason" {
			hasReason = true
		}
		if field.Key == "cls" {
			hasCls = true
		}
	}
	if !hasReason || !hasCls {
		return fmt.Errorf("import-fields: enabled fields must include reason and cls")
	}
	return nil
}

// UserFields returns enabled fields sorted by Order (for UI).
func (f ImportFieldsFile) UserFields() []ImportField {
	out := make([]ImportField, 0, len(f.Fields))
	for _, field := range f.Fields {
		if field.Enabled {
			out = append(out, field)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// NormalizeValues maps submitted values → reason, cls, extras.
func (f ImportFieldsFile) NormalizeValues(values map[string]string) (reason, cls string, extras map[string]string, err error) {
	if values == nil {
		values = map[string]string{}
	}
	extras = map[string]string{}
	for _, field := range f.UserFields() {
		v := strings.TrimSpace(values[field.Key])
		if v == "" {
			v = field.Default
		}
		if field.Required && v == "" {
			return "", "", nil, fmt.Errorf("请填写「%s」", field.Label)
		}
		if field.Type == "select" && v != "" && !optionValueAllowed(field.Options, v) {
			return "", "", nil, fmt.Errorf("「%s」取值无效", field.Label)
		}
		switch field.Key {
		case "reason":
			reason = v
		case "cls":
			cls = v
		default:
			if v != "" {
				extras[field.Key] = v
			}
		}
	}
	if reason == "" {
		reason = "import"
	}
	if cls == "" {
		cls = "internal"
	}
	return reason, cls, extras, nil
}

func optionValueAllowed(opts []string, v string) bool {
	for _, o := range opts {
		val, _ := splitOption(o)
		if val == v {
			return true
		}
	}
	return false
}

func splitOption(o string) (value, label string) {
	o = strings.TrimSpace(o)
	if i := strings.IndexByte(o, '|'); i >= 0 {
		return strings.TrimSpace(o[:i]), strings.TrimSpace(o[i+1:])
	}
	return o, o
}

// SaveImportFields validates and writes the ATM import form config.
func SaveImportFields(path string, f ImportFieldsFile) error {
	if path == "" {
		return fmt.Errorf("未配置 ATM_IMPORT_FIELDS，无法保存")
	}
	promoteReasonTextarea(&f)
	if f.Version == 0 {
		f.Version = 1
	}
	if err := ValidateImportFields(f); err != nil {
		return err
	}
	// Ensure builtin flags for core keys.
	for i := range f.Fields {
		if f.Fields[i].Key == "reason" || f.Fields[i].Key == "cls" {
			f.Fields[i].Builtin = true
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WriteDefaultImportFields writes defaults if path does not exist.
func WriteDefaultImportFields(path string) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return SaveImportFields(path, DefaultImportFields())
}
