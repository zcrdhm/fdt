package portal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FormField describes one portal submission / Feishu-mapped field.
type FormField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // input | textarea | select | number
	Required    bool     `json:"required"`
	Source      string   `json:"source"` // user | system
	Options     []string `json:"options,omitempty"`
	Default     string   `json:"default,omitempty"`
	MaxLen      int      `json:"maxLen,omitempty"`
	Order       int      `json:"order"`
	Enabled     bool     `json:"enabled"`
	FeishuField string   `json:"feishuField"`          // logical key used by gateway FormWidget.Field
	FeishuType  string   `json:"feishuType,omitempty"` // input | textarea | number
	FeishuID    string   `json:"feishuId,omitempty"`   // Feishu widget id (for gateway export)
	Builtin     bool     `json:"builtin,omitempty"`    // reason / cls cannot delete or rename key
	Hint        string   `json:"hint,omitempty"`
}

// FormFieldsFile is saved at /etc/fdt/form-fields.json.
type FormFieldsFile struct {
	Version int         `json:"version"`
	Fields  []FormField `json:"fields"`
}

var formKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// DefaultFormFields matches today's Portal form + Feishu default widgets.
func DefaultFormFields() FormFieldsFile {
	return FormFieldsFile{
		Version: 1,
		Fields: []FormField{
			{Key: "reason", Label: "事由", Type: "textarea", Required: true, Source: "user",
				FeishuField: "reason", FeishuType: "textarea", FeishuID: "reason",
				MaxLen: 200, Order: 10, Enabled: true, Builtin: true,
				Default: "交付客户A的版图数据", Hint: "用途、接收方等"},
			{Key: "cls", Label: "密级", Type: "select", Required: true, Source: "user",
				Options: []string{"internal", "confidential", "secret", "topsecret"},
				Default: "secret", FeishuField: "class", FeishuType: "input", FeishuID: "class",
				Order: 20, Enabled: true, Builtin: true},
			{Key: "req_id", Label: "申请单号", Type: "input", Source: "system",
				FeishuField: "req_id", FeishuType: "input", FeishuID: "req_id", Order: 100, Enabled: true, Builtin: true},
			{Key: "applicant", Label: "申请人", Type: "input", Source: "system",
				FeishuField: "applicant", FeishuType: "input", FeishuID: "applicant", Order: 110, Enabled: true, Builtin: true},
			{Key: "dept", Label: "部门", Type: "input", Source: "system",
				FeishuField: "dept", FeishuType: "input", FeishuID: "dept", Order: 120, Enabled: true, Builtin: true},
			{Key: "merkle_root", Label: "Merkle 根", Type: "input", Source: "system",
				FeishuField: "merkle_root", FeishuType: "input", FeishuID: "merkle_root", Order: 130, Enabled: true, Builtin: true},
			{Key: "file_count", Label: "文件数", Type: "number", Source: "system",
				FeishuField: "file_count", FeishuType: "number", FeishuID: "file_count", Order: 140, Enabled: true, Builtin: true},
			{Key: "total_size", Label: "总大小", Type: "number", Source: "system",
				FeishuField: "total_size", FeishuType: "number", FeishuID: "total_size", Order: 150, Enabled: true, Builtin: true},
			{Key: "manifest", Label: "清单摘要", Type: "textarea", Source: "system",
				FeishuField: "manifest", FeishuType: "textarea", FeishuID: "manifest", Order: 160, Enabled: true, Builtin: true},
		},
	}
}

func LoadFormFields(path string) (FormFieldsFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultFormFields(), nil
		}
		return FormFieldsFile{}, err
	}
	var f FormFieldsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return FormFieldsFile{}, err
	}
	if len(f.Fields) == 0 {
		return DefaultFormFields(), nil
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return f, nil
}

func SaveFormFields(path string, f FormFieldsFile) error {
	if err := ValidateFormFields(f); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if f.Version == 0 {
		f.Version = 1
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ValidateFormFields(f FormFieldsFile) error {
	if len(f.Fields) == 0 {
		return fmt.Errorf("至少需要一个字段")
	}
	seen := map[string]bool{}
	seenFeishu := map[string]bool{}
	hasReason, hasCls := false, false
	for i, field := range f.Fields {
		key := strings.TrimSpace(field.Key)
		if !formKeyRe.MatchString(key) {
			return fmt.Errorf("字段 #%d key 非法（需小写字母开头，仅 a-z0-9_）: %q", i+1, field.Key)
		}
		if seen[key] {
			return fmt.Errorf("重复字段 key: %s", key)
		}
		seen[key] = true
		if strings.TrimSpace(field.Label) == "" {
			return fmt.Errorf("字段 %s 缺少 label", key)
		}
		src := field.Source
		if src == "" {
			src = "user"
		}
		if src != "user" && src != "system" {
			return fmt.Errorf("字段 %s source 须为 user 或 system", key)
		}
		typ := field.Type
		if typ == "" {
			typ = "input"
		}
		switch typ {
		case "input", "textarea", "select", "number":
		default:
			return fmt.Errorf("字段 %s type 不支持: %s", key, typ)
		}
		if typ == "select" && src == "user" && len(field.Options) == 0 {
			return fmt.Errorf("字段 %s 为 select 时需要 options", key)
		}
		ff := strings.TrimSpace(field.FeishuField)
		if ff == "" {
			ff, _, _ = DeriveFeishu(key, typ)
		}
		if !formKeyRe.MatchString(ff) {
			return fmt.Errorf("字段 %s 推导的飞书 field 非法: %q", key, ff)
		}
		if seenFeishu[ff] {
			return fmt.Errorf("重复飞书 field: %s（由 key/feishuField 推导）", ff)
		}
		seenFeishu[ff] = true
		if field.MaxLen < 0 || field.MaxLen > 2000 {
			return fmt.Errorf("字段 %s maxLen 超出范围", key)
		}
		if key == "reason" {
			hasReason = true
		}
		if key == "cls" {
			hasCls = true
		}
	}
	if !hasReason || !hasCls {
		return fmt.Errorf("必须保留内置字段 reason 与 cls")
	}
	return nil
}

// UserFields returns enabled user-facing fields sorted by order.
func (f FormFieldsFile) UserFields() []FormField {
	var out []FormField
	for _, field := range f.Fields {
		if !field.Enabled || field.Source == "system" {
			continue
		}
		out = append(out, field)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// DeriveFeishu maps a portal field key/type onto Feishu widget field/type/id.
// cls → class keeps compatibility with the default Feishu form; others use key.
func DeriveFeishu(key, typ string) (field, feishuType, id string) {
	key = strings.TrimSpace(key)
	field = key
	if key == "cls" {
		field = "class"
	}
	switch typ {
	case "textarea":
		feishuType = "textarea"
	case "number":
		feishuType = "number"
	default:
		feishuType = "input"
	}
	id = field
	return field, feishuType, id
}

// FeishuWidgetsExport builds a gateway feishu-form.json template from enabled fields.
func (f FormFieldsFile) FeishuWidgetsExport() []map[string]string {
	fields := append([]FormField(nil), f.Fields...)
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].Order < fields[j].Order })
	out := make([]map[string]string, 0, len(fields))
	for _, field := range fields {
		if !field.Enabled {
			continue
		}
		ff, typ, id := DeriveFeishu(field.Key, field.Type)
		if field.FeishuField != "" {
			ff = field.FeishuField
		}
		if field.FeishuType != "" {
			typ = field.FeishuType
		}
		if field.FeishuID != "" {
			id = field.FeishuID
		}
		out = append(out, map[string]string{"id": id, "type": typ, "field": ff})
	}
	return out
}

// NormalizeExtras maps submitted portal values onto Feishu logical fields.
// Builtin reason/cls are returned separately; remaining go into extras.
func (f FormFieldsFile) NormalizeExtras(values map[string]string) (reason string, cls string, extras map[string]string, err error) {
	extras = map[string]string{}
	for _, field := range f.UserFields() {
		raw := strings.TrimSpace(values[field.Key])
		if raw == "" {
			raw = strings.TrimSpace(field.Default)
		}
		if field.Required && raw == "" {
			return "", "", nil, fmt.Errorf("请填写「%s」", field.Label)
		}
		max := field.MaxLen
		if max <= 0 {
			max = 200
		}
		if len([]rune(raw)) > max {
			return "", "", nil, fmt.Errorf("「%s」超过 %d 字", field.Label, max)
		}
		if field.Type == "select" && raw != "" && len(field.Options) > 0 {
			ok := false
			for _, o := range field.Options {
				if o == raw {
					ok = true
					break
				}
			}
			if !ok {
				return "", "", nil, fmt.Errorf("「%s」选项无效", field.Label)
			}
		}
		ff, _, _ := DeriveFeishu(field.Key, field.Type)
		if field.FeishuField != "" {
			ff = field.FeishuField
		}
		switch field.Key {
		case "reason":
			reason = raw
		case "cls":
			cls = raw
		default:
			if raw != "" {
				extras[ff] = raw
			}
		}
	}
	if reason == "" {
		reason = values["reason"]
	}
	if cls == "" {
		cls = values["cls"]
	}
	return reason, cls, extras, nil
}
