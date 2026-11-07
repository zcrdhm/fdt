package portal

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"fdt/internal/identity"
)

const portalSessionCookie = "fdt_portal_session"

type portalSess struct {
	User  *identity.User
	Exp   time.Time
	Admin bool // unlocked portal settings (LDAP etc.)
}

// Sessions is an in-memory cookie session store for the Portal UI.
type Sessions struct {
	TTL time.Duration
	mu  sync.Mutex
	m   map[string]portalSess
}

func NewSessions() *Sessions {
	return &Sessions{TTL: 12 * time.Hour, m: map[string]portalSess{}}
}

func (s *Sessions) Create(u *identity.User) (string, error) {
	tok, err := randomHex(24)
	if err != nil {
		return "", err
	}
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	cp := *u
	s.mu.Lock()
	s.m[tok] = portalSess{User: &cp, Exp: time.Now().Add(ttl)}
	s.mu.Unlock()
	return tok, nil
}

func (s *Sessions) get(tok string) (portalSess, bool) {
	if tok == "" {
		return portalSess{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[tok]
	if !ok || time.Now().After(sess.Exp) {
		delete(s.m, tok)
		return portalSess{}, false
	}
	return sess, true
}

func (s *Sessions) User(tok string) *identity.User {
	sess, ok := s.get(tok)
	if !ok || sess.User == nil {
		return nil
	}
	cp := *sess.User
	return &cp
}

// IsAdmin reports whether the session has unlocked portal settings.
func (s *Sessions) IsAdmin(tok string) bool {
	sess, ok := s.get(tok)
	return ok && sess.Admin
}

// SetAdmin marks (or clears) settings admin on an existing session.
func (s *Sessions) SetAdmin(tok string, admin bool) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[tok]
	if !ok || time.Now().After(sess.Exp) {
		delete(s.m, tok)
		return false
	}
	sess.Admin = admin
	s.m[tok] = sess
	return true
}

func (s *Sessions) Logout(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

func (s *Sessions) SetCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name: portalSessionCookie, Value: tok, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(s.TTL.Seconds()),
	})
}

func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: portalSessionCookie, Value: "", Path: "/",
		HttpOnly: true, MaxAge: -1,
	})
}

func (s *Sessions) TokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(portalSessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
