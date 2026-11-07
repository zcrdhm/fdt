package feishu

import "testing"

func TestApprovalCodeForDirection(t *testing.T) {
	c := LiveConfig{
		ApprovalCode:       "EXPORT",
		ApprovalCodeImport: "IMPORT",
	}
	if got := c.approvalCodeFor("export"); got != "EXPORT" {
		t.Fatalf("export: %q", got)
	}
	if got := c.approvalCodeFor("import"); got != "IMPORT" {
		t.Fatalf("import: %q", got)
	}
	c.ApprovalCodeImport = ""
	if got := c.approvalCodeFor("import"); got != "EXPORT" {
		t.Fatalf("import fallback: %q", got)
	}
}
