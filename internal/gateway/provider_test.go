package gateway

import "testing"

func TestNormalizedProvider(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"", "feishu", true},
		{"Feishu", "feishu", true},
		{"lark", "feishu", true},
		{"dingtalk", "dingtalk", false},
		{"ding", "dingtalk", false},
		{"wecom", "wecom", false},
		{"wework", "wecom", false},
	}
	for _, c := range cases {
		f := TenantFeishuFile{Provider: c.in}
		if got := f.NormalizedProvider(); got != c.want {
			t.Fatalf("%q: got %q want %q", c.in, got, c.want)
		}
		if f.ProviderSupported() != c.ok {
			t.Fatalf("%q: supported=%v want %v", c.in, f.ProviderSupported(), c.ok)
		}
	}
}
