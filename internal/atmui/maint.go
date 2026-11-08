package atmui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const defaultMaintPassword = "fdt-maint"

// bcryptCost balances ATM kiosk CPU vs brute-force resistance.
const maintBcryptCost = 12

// maintAuth stores the kiosk maintenance password as a bcrypt hash (file-backed).
// ATM_MAINT_PASSWORD in fdt.env is only a one-shot bootstrap plaintext when the
// hash file is missing; after first start the file holds $2a$… only.
type maintAuth struct {
	mu   sync.Mutex
	path string
	hash string // bcrypt hash, never plaintext
}

func newMaintAuth(path, envPass string) *maintAuth {
	m := &maintAuth{path: path}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			line := strings.TrimSpace(string(raw))
			if line != "" {
				if looksLikeBcrypt(line) {
					m.hash = line
					return m
				}
				// Legacy plaintext file → migrate to hash immediately.
				if h, err := hashMaintPassword(line); err == nil {
					m.hash = h
					_ = m.persistLocked()
					return m
				}
			}
		}
	}
	p := strings.TrimSpace(envPass)
	if p == "" {
		p = defaultMaintPassword
	}
	// Allow deploying a precomputed hash via env (no plaintext on disk/env after install).
	if looksLikeBcrypt(p) {
		m.hash = p
	} else {
		h, err := hashMaintPassword(p)
		if err != nil {
			// Extremely unlikely; fall back to hashing the default.
			h, _ = hashMaintPassword(defaultMaintPassword)
		}
		m.hash = h
	}
	_ = m.persistLocked()
	return m
}

func (m *maintAuth) check(got string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(m.hash), []byte(got)) == nil
}

func (m *maintAuth) change(old, neu string) error {
	neu = strings.TrimSpace(neu)
	if neu == "" {
		return errMaintEmpty
	}
	if len(neu) < 4 {
		return errMaintShort
	}
	if !m.check(old) {
		return errMaintBadOld
	}
	h, err := hashMaintPassword(neu)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hash = h
	return m.persistLocked()
}

func (m *maintAuth) persistLocked() error {
	if m.path == "" || m.hash == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(m.hash+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func hashMaintPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), maintBcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func looksLikeBcrypt(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

type maintError string

func (e maintError) Error() string { return string(e) }

const (
	errMaintEmpty  = maintError("新密码不能为空")
	errMaintShort  = maintError("新密码至少 4 位")
	errMaintBadOld = maintError("当前密码不正确")
)
