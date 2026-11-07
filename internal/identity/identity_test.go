package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func TestMockProviderAuthenticate(t *testing.T) {
	p := NewMockProvider()
	ctx := context.Background()

	u, err := p.Authenticate(ctx, "zhangsan", "pass123")
	if err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
	if u.Name != "张三" || u.ID != "g976f94f" || u.Dept != "设计部" {
		t.Fatalf("bad attrs: %+v", u)
	}
	if _, err := p.Authenticate(ctx, "zhangsan", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("want ErrInvalidCredentials, got %v", err)
	}
	if _, err := p.Authenticate(ctx, "nobody", "x"); err != ErrUserNotFound {
		t.Fatalf("want ErrUserNotFound, got %v", err)
	}
	if _, err := p.Lookup(ctx, "lisi"); err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
}

func TestLDAPFilterEscapingPreventsInjection(t *testing.T) {
	// A malicious account must not be able to alter the search filter semantics.
	malicious := "admin)(|(objectClass=*"
	escaped := ldap.EscapeFilter(malicious)
	// go-ldap escapes RFC4515 special chars as \xx hex; the raw metacharacters
	// must no longer appear unescaped.
	if strings.Contains(escaped, "(") || strings.Contains(escaped, ")") || strings.Contains(escaped, "*") {
		t.Fatalf("special chars not escaped: %q", escaped)
	}
	if !strings.Contains(escaped, `\28`) || !strings.Contains(escaped, `\2a`) {
		t.Fatalf("expected hex-escaped ( and * : %q", escaped)
	}
}

func TestNewLDAPProviderValidation(t *testing.T) {
	if _, err := NewLDAPProvider(LDAPConfig{}); err == nil {
		t.Fatal("expected error for missing URL/SearchBase")
	}
	p, err := NewLDAPProvider(LDAPConfig{URL: "ldaps://dc:636", SearchBase: "DC=corp,DC=example"})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.SearchFilter != "(sAMAccountName=%s)" || p.cfg.AttrID != "employeeID" {
		t.Fatalf("defaults not applied: %+v", p.cfg)
	}
}
