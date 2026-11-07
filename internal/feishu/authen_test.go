package feishu

import "testing"

func TestApplicantIDForUserIDType(t *testing.T) {
	u := &UserInfo{OpenID: "ou_1", UserID: "uid_1", UnionID: "un_1", Name: "张三"}
	c := NewLiveClient(LiveConfig{UserIDType: "open_id"}, nil)
	if got := c.ApplicantIDFor(u); got != "ou_1" {
		t.Fatalf("open_id: got %q", got)
	}
	c.cfg.UserIDType = "user_id"
	if got := c.ApplicantIDFor(u); got != "uid_1" {
		t.Fatalf("user_id: got %q", got)
	}
	c.cfg.UserIDType = "union_id"
	if got := c.ApplicantIDFor(u); got != "un_1" {
		t.Fatalf("union_id: got %q", got)
	}
}
