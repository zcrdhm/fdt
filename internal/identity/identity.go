// Package identity resolves the authenticated applicant from a directory
// (Active Directory over LDAP). The Portal authenticates the user against AD and
// reads name / employee id / department from the directory, so the applicant in
// QR#1 is a verified identity rather than a self-typed string (prevents
// spoofing and enables department/classification-based approval routing).
//
// Implementations: LDAPProvider (AD), PAMProvider (本机 PAM), MockProvider (offline).
package identity

import (
	"context"
	"errors"
	"fmt"
)

// User is the resolved directory identity.
type User struct {
	Account string // sAMAccountName (login)
	ID      string // employeeID -> 工号
	Name    string // displayName -> 姓名
	Dept    string // department -> 部门
	Mail    string // mail
	Title   string // title
	DN      string // distinguishedName
}

var (
	ErrInvalidCredentials = errors.New("identity: 用户名或密码错误")
	ErrUserNotFound       = errors.New("identity: 目录中未找到该用户")
	ErrNotConfigured      = errors.New("identity: provider not configured")
)

// Provider authenticates and looks up directory users.
type Provider interface {
	// Authenticate verifies the credentials and returns the resolved user.
	Authenticate(ctx context.Context, account, password string) (*User, error)
	// Lookup resolves a user by account name without a password (e.g. from a
	// Kerberos reverse-proxy header). Not used by the LDAP-bind flow.
	Lookup(ctx context.Context, account string) (*User, error)
	Close() error
}

// MockProvider is an offline directory for development/tests. It is a test
// double, clearly not a production identity source.
type MockProvider struct {
	Users map[string]User // key: account (lowercased); password stored separately
	pass  map[string]string
}

func NewMockProvider() *MockProvider {
	m := &MockProvider{Users: map[string]User{}, pass: map[string]string{}}
	// A couple of sample accounts so the dev Portal is usable out of the box.
	m.Add("zhangsan", "S3!config", User{Account: "zhangsan", ID: "g976f94f", Name: "张三", Dept: "设计部", Mail: "zhangsan@corp.local", Title: "工程师"})
	m.Add("lisi", "S3!config", User{Account: "lisi", ID: "m0001", Name: "李四", Dept: "管理部", Mail: "lisi@corp.local", Title: "经理"})
	return m
}

func (m *MockProvider) Add(account, password string, u User) {
	u.Account = account
	m.Users[account] = u
	m.pass[account] = password
}

func (m *MockProvider) Authenticate(_ context.Context, account, password string) (*User, error) {
	u, ok := m.Users[account]
	if !ok {
		return nil, ErrUserNotFound
	}
	if m.pass[account] != password {
		return nil, ErrInvalidCredentials
	}
	cp := u
	return &cp, nil
}

func (m *MockProvider) Lookup(_ context.Context, account string) (*User, error) {
	u, ok := m.Users[account]
	if !ok {
		return nil, ErrUserNotFound
	}
	cp := u
	return &cp, nil
}

func (m *MockProvider) Close() error { return nil }

var _ Provider = (*MockProvider)(nil)

// userNotFoundError wraps a missing-entry result for clearer logs.
func userNotFoundError(account string) error {
	return fmt.Errorf("%w: %s", ErrUserNotFound, account)
}
