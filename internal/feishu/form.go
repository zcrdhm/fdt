package feishu

import (
	"encoding/json"
	"fmt"
	"os"
)

// FormWidget is one control on the Feishu approval definition. ID is the widget
// id assigned in the Feishu admin console (not a name we invent). Field selects
// which request value is written into it.
type FormWidget struct {
	ID    string `json:"id"`
	Type  string `json:"type"` // input | textarea | number
	Field string `json:"field"`
}

// DefaultFormWidgets is the field set the approval definition must contain.
// Replace ID with the widget id Feishu assigns; the field names are stable.
func DefaultFormWidgets() []FormWidget {
	return []FormWidget{
		{ID: "req_id", Type: "input", Field: "req_id"},
		{ID: "direction", Type: "input", Field: "direction"},
		{ID: "applicant", Type: "input", Field: "applicant"},
		{ID: "dept", Type: "input", Field: "dept"},
		{ID: "reason", Type: "textarea", Field: "reason"},
		{ID: "class", Type: "input", Field: "class"},
		{ID: "merkle_root", Type: "input", Field: "merkle_root"},
		{ID: "file_count", Type: "number", Field: "file_count"},
		{ID: "total_size", Type: "number", Field: "total_size"},
		{ID: "manifest", Type: "textarea", Field: "manifest"},
	}
}

// LoadFormWidgets reads a JSON array of FormWidget.
func LoadFormWidgets(path string) ([]FormWidget, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ws []FormWidget
	if err := json.Unmarshal(b, &ws); err != nil {
		return nil, err
	}
	if len(ws) == 0 {
		return nil, fmt.Errorf("feishu: form file %s has no widgets", path)
	}
	return ws, nil
}

// BuildApprovalForm renders the Feishu v4 form string: a JSON array of
// {id,type,value}. A map is rejected by the OpenAPI.
func BuildApprovalForm(widgets []FormWidget, r CreateInstanceRequest) (string, error) {
	if len(widgets) == 0 {
		widgets = DefaultFormWidgets()
	}
	dirLabel := r.Direction
	switch r.Direction {
	case "import":
		dirLabel = "导入"
	case "export":
		dirLabel = "导出"
	}
	values := map[string]string{
		"req_id":      r.ReqID,
		"direction":   dirLabel,
		"applicant":   r.ApplicantName,
		"dept":        r.Dept,
		"reason":      r.Reason,
		"class":       r.Classification,
		"merkle_root": r.MerkleRoot,
		"file_count":  fmt.Sprintf("%d", r.FileCount),
		"total_size":  fmt.Sprintf("%d", r.TotalSize),
		"manifest":    r.ManifestText,
	}
	for k, v := range r.Extras {
		if k == "" {
			continue
		}
		if _, exists := values[k]; !exists {
			values[k] = v
		}
	}
	items := make([]map[string]string, 0, len(widgets))
	for _, w := range widgets {
		if w.ID == "" || w.Field == "" {
			return "", fmt.Errorf("feishu: widget missing id or field")
		}
		typ := w.Type
		if typ == "" {
			typ = "input"
		}
		val, ok := values[w.Field]
		if !ok {
			// Custom / optional Feishu widgets may be empty.
			val = ""
		}
		items = append(items, map[string]string{"id": w.ID, "type": typ, "value": val})
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
