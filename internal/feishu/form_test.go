package feishu

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildApprovalFormIsWidgetArray(t *testing.T) {
	raw, err := BuildApprovalForm(DefaultFormWidgets(), CreateInstanceRequest{
		ReqID: "r1", ApplicantName: "张三", Dept: "设计部", Reason: "交付",
		Classification: "secret", Direction: "import", MerkleRoot: "abc", FileCount: 2, TotalSize: 30,
		ManifestText: "清单",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "[") {
		t.Fatalf("form must be a JSON array, got %s", raw)
	}
	var items []map[string]string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 10 || items[0]["id"] != "req_id" || items[0]["value"] != "r1" {
		t.Fatalf("items = %#v", items)
	}
	if items[1]["id"] != "direction" || items[1]["value"] != "导入" {
		t.Fatalf("direction widget = %#v", items[1])
	}
	if items[9]["type"] != "textarea" || items[9]["value"] != "清单" {
		t.Fatalf("manifest widget = %#v", items[9])
	}
}

func TestBuildApprovalFormExtras(t *testing.T) {
	widgets := append(DefaultFormWidgets(), FormWidget{ID: "receiver_w", Type: "input", Field: "receiver"})
	raw, err := BuildApprovalForm(widgets, CreateInstanceRequest{
		ReqID: "r1", ApplicantName: "张三", Reason: "交付", Classification: "secret",
		Extras: map[string]string{"receiver": "客户A"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]string
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	last := items[len(items)-1]
	if last["id"] != "receiver_w" || last["value"] != "客户A" {
		t.Fatalf("extras widget = %#v", last)
	}
}
