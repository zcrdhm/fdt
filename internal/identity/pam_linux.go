//go:build linux && cgo

package identity

import (
	"context"
	"fmt"
	"os/user"
	"strings"

	"github.com/msteinert/pam"
)

const pamAvailable = true

// PAMProvider authenticates applicants against the host PAM stack
// (local accounts, SSSD, pam_ldap, … depending on /etc/pam.d/<service>).
type PAMProvider struct {
	service string
}

func NewPAMProvider(cfg PAMConfig) (*PAMProvider, error) {
	cfg = cfg.withDefaults()
	return &PAMProvider{service: cfg.Service}, nil
}

func (p *PAMProvider) Authenticate(_ context.Context, account, password string) (*User, error) {
	account = strings.TrimSpace(account)
	if account == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	t, err := pam.StartFunc(p.service, account, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return password, nil
		case pam.PromptEchoOn:
			return account, nil
		case pam.ErrorMsg, pam.TextInfo:
			return "", nil
		default:
			return "", fmt.Errorf("unsupported PAM style %v: %s", s, msg)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("pam start (%s): %w", p.service, err)
	}

	if err := t.Authenticate(0); err != nil {
		return nil, ErrInvalidCredentials
	}
	if err := t.AcctMgmt(0); err != nil {
		return nil, fmt.Errorf("pam account: %w", err)
	}
	return lookupPAMUser(account)
}

func (p *PAMProvider) Lookup(_ context.Context, account string) (*User, error) {
	return lookupPAMUser(strings.TrimSpace(account))
}

func (p *PAMProvider) Close() error { return nil }

func lookupPAMUser(account string) (*User, error) {
	if account == "" {
		return nil, ErrUserNotFound
	}
	u, err := user.Lookup(account)
	if err != nil {
		// Auth succeeded but NSS lookup failed — still return a minimal identity.
		return &User{Account: account, ID: account, Name: account}, nil
	}
	name := account
	if u.Name != "" {
		// GECOS may be "Full Name,room,work,home"
		name = strings.TrimSpace(strings.Split(u.Name, ",")[0])
		if name == "" {
			name = account
		}
	}
	id := u.Uid
	if id == "" {
		id = account
	}
	return &User{
		Account: u.Username,
		ID:      id,
		Name:    name,
		Mail:    "",
		Dept:    "",
		Title:   "",
	}, nil
}

var _ Provider = (*PAMProvider)(nil)
