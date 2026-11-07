package identity

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAPConfig configures the AD/LDAP provider.
type LDAPConfig struct {
	URL                string // ldap://dc:389 or ldaps://dc:636
	StartTLS           bool   // upgrade a ldap:// connection with StartTLS
	CACertFile         string // PEM to verify the DC cert (empty => system pool)
	InsecureSkipVerify bool   // DEV ONLY: skip DC cert verification
	ServiceBindDN      string // service account for the search step (often required)
	ServicePassword    string
	SearchBase         string // e.g. DC=corp,DC=example
	SearchFilter       string // %s is the escaped account; default (sAMAccountName=%s)
	// Attribute names; defaults suit Active Directory.
	AttrName    string // displayName
	AttrID      string // employeeID
	AttrDept    string // department
	AttrMail    string // mail
	AttrTitle   string // title
	AttrAccount string // sAMAccountName
	DialTimeout int    // seconds; 0 => 10
}

func (c *LDAPConfig) withDefaults() {
	if c.SearchFilter == "" {
		c.SearchFilter = "(sAMAccountName=%s)"
	}
	if c.AttrName == "" {
		c.AttrName = "displayName"
	}
	if c.AttrID == "" {
		c.AttrID = "employeeID"
	}
	if c.AttrDept == "" {
		c.AttrDept = "department"
	}
	if c.AttrMail == "" {
		c.AttrMail = "mail"
	}
	if c.AttrTitle == "" {
		c.AttrTitle = "title"
	}
	if c.AttrAccount == "" {
		c.AttrAccount = "sAMAccountName"
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 10
	}
}

// LDAPProvider authenticates users against AD via search-then-bind.
type LDAPProvider struct {
	cfg LDAPConfig
}

func NewLDAPProvider(cfg LDAPConfig) (*LDAPProvider, error) {
	if cfg.URL == "" || cfg.SearchBase == "" {
		return nil, fmt.Errorf("identity: LDAPConfig requires URL and SearchBase")
	}
	cfg.withDefaults()
	return &LDAPProvider{cfg: cfg}, nil
}

func (p *LDAPProvider) tlsConfig() (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.cfg.InsecureSkipVerify}
	if p.cfg.CACertFile != "" {
		pem, err := os.ReadFile(p.cfg.CACertFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("identity: bad CA cert file %s", p.cfg.CACertFile)
		}
		tc.RootCAs = pool
	}
	return tc, nil
}

func (p *LDAPProvider) connect() (*ldap.Conn, error) {
	tc, err := p.tlsConfig()
	if err != nil {
		return nil, err
	}
	dialer := ldap.DialWithDialer(&net.Dialer{Timeout: time.Duration(p.cfg.DialTimeout) * time.Second})
	conn, err := ldap.DialURL(p.cfg.URL, dialer, ldap.DialWithTLSConfig(tc))
	if err != nil {
		return nil, err
	}
	if p.cfg.StartTLS && strings.HasPrefix(p.cfg.URL, "ldap://") {
		if err := conn.StartTLS(tc); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// Authenticate performs search-then-bind: locate the user DN with the service
// account, then bind as the user with the supplied password to verify it.
func (p *LDAPProvider) Authenticate(ctx context.Context, account, password string) (*User, error) {
	if account == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	conn, err := p.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if p.cfg.ServiceBindDN != "" {
		if err := conn.Bind(p.cfg.ServiceBindDN, p.cfg.ServicePassword); err != nil {
			return nil, fmt.Errorf("identity: service bind: %w", err)
		}
	}

	entry, err := p.search(conn, account)
	if err != nil {
		return nil, err
	}
	// Bind as the user to verify the password. An invalid-password bind is the
	// only place we accept a credential failure.
	if err := conn.Bind(entry.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("identity: user bind: %w", err)
	}
	return entry, nil
}

// Lookup resolves a user by account without verifying a password.
func (p *LDAPProvider) Lookup(ctx context.Context, account string) (*User, error) {
	conn, err := p.connect()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if p.cfg.ServiceBindDN != "" {
		if err := conn.Bind(p.cfg.ServiceBindDN, p.cfg.ServicePassword); err != nil {
			return nil, err
		}
	}
	return p.search(conn, account)
}

func (p *LDAPProvider) search(conn *ldap.Conn, account string) (*User, error) {
	// EscapeFilter prevents LDAP injection via the account name.
	filter := fmt.Sprintf(p.cfg.SearchFilter, ldap.EscapeFilter(account))
	req := ldap.NewSearchRequest(
		p.cfg.SearchBase, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases,
		1, p.cfg.DialTimeout, false, filter,
		[]string{p.cfg.AttrName, p.cfg.AttrID, p.cfg.AttrDept, p.cfg.AttrMail, p.cfg.AttrTitle, p.cfg.AttrAccount},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		return nil, fmt.Errorf("identity: search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, userNotFoundError(account)
	}
	e := res.Entries[0]
	u := &User{
		DN:      e.DN,
		Account: firstNonEmpty(e.GetAttributeValue(p.cfg.AttrAccount), account),
		Name:    e.GetAttributeValue(p.cfg.AttrName),
		ID:      e.GetAttributeValue(p.cfg.AttrID),
		Dept:    e.GetAttributeValue(p.cfg.AttrDept),
		Mail:    e.GetAttributeValue(p.cfg.AttrMail),
		Title:   e.GetAttributeValue(p.cfg.AttrTitle),
	}
	if u.ID == "" {
		u.ID = u.Account // fall back to login name if employeeID unset
	}
	return u, nil
}

func (p *LDAPProvider) Close() error { return nil }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var _ Provider = (*LDAPProvider)(nil)
