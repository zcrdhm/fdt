package gwadmin

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Auth holds operator credentials and in-memory session tokens (platform admin).
type Auth struct {
	User     string
	Password string
	TTL      time.Duration

	mu       sync.Mutex
	sessions map[string]time.Time
}

func NewAuth(user, password string) *Auth {
	if user == "" {
		user = "admin"
	}
	return &Auth{
		User: user, Password: password, TTL: 12 * time.Hour,
		sessions: map[string]time.Time{},
	}
}

func (a *Auth) Enabled() bool {
	return a != nil && a.Password != ""
}

// ChangePassword verifies the old password and updates the in-memory credential.
func (a *Auth) ChangePassword(oldPass, newPass string) error {
	if !a.Enabled() {
		return ErrUnauthorized
	}
	if len(newPass) < 8 {
		return fmt.Errorf("gwadmin: password >= 8 chars")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(oldPass), []byte(a.Password)) != 1 {
		return ErrUnauthorized
	}
	a.Password = newPass
	return nil
}

func (a *Auth) Login(user, password string) (cookie string, err error) {
	if !a.Enabled() {
		return "", ErrUnauthorized
	}
	a.mu.Lock()
	wantUser, wantPass := a.User, a.Password
	a.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(user), []byte(wantUser)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(wantPass)) != 1 {
		return "", ErrUnauthorized
	}
	tok, err := randomToken(24)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.sessions[tok] = time.Now().Add(a.TTL)
	a.mu.Unlock()
	return tok, nil
}

func (a *Auth) Logout(tok string) {
	a.mu.Lock()
	delete(a.sessions, tok)
	a.mu.Unlock()
}

func (a *Auth) Valid(tok string) bool {
	if tok == "" || !a.Enabled() {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, tok)
		return false
	}
	return true
}

const sessionCookie = "fdt_admin_session"
const tenantCookie = "fdt_tenant_session"

func (a *Auth) SetCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/admin",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(a.TTL.Seconds()),
	})
}

func (a *Auth) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/admin",
		HttpOnly: true, MaxAge: -1,
	})
}

func (a *Auth) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// TenantSessions maps session token → tenant id for customer consoles.
type TenantSessions struct {
	TTL time.Duration
	mu  sync.Mutex
	m   map[string]tenantSess
}

type tenantSess struct {
	TenantID string
	Exp      time.Time
}

func NewTenantSessions() *TenantSessions {
	return &TenantSessions{TTL: 12 * time.Hour, m: map[string]tenantSess{}}
}

func (t *TenantSessions) Create(tenantID string) (string, error) {
	tok, err := randomToken(24)
	if err != nil {
		return "", err
	}
	ttl := t.TTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	t.mu.Lock()
	t.m[tok] = tenantSess{TenantID: tenantID, Exp: time.Now().Add(ttl)}
	t.mu.Unlock()
	return tok, nil
}

func (t *TenantSessions) TenantID(tok string) string {
	if tok == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.m[tok]
	if !ok || time.Now().After(s.Exp) {
		delete(t.m, tok)
		return ""
	}
	return s.TenantID
}

func (t *TenantSessions) Logout(tok string) {
	t.mu.Lock()
	delete(t.m, tok)
	t.mu.Unlock()
}

func (t *TenantSessions) SetCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name: tenantCookie, Value: tok, Path: "/console",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(t.TTL.Seconds()),
	})
}

func (t *TenantSessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: tenantCookie, Value: "", Path: "/console",
		HttpOnly: true, MaxAge: -1,
	})
}

func (t *TenantSessions) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(tenantCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
