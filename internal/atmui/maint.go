package atmui

import (
	"crypto/subtle"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultMaintPassword = "fdt-maint"

// maintAuth stores the kiosk maintenance password (file-backed, env bootstrap).
type maintAuth struct {
	mu   sync.Mutex
	path string
	pass string
}

func newMaintAuth(path, envPass string) *maintAuth {
	m := &maintAuth{path: path}
	if path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			if p := strings.TrimSpace(string(raw)); p != "" {
				m.pass = p
				return m
			}
		}
	}
	p := strings.TrimSpace(envPass)
	if p == "" {
		p = defaultMaintPassword
	}
	m.pass = p
	_ = m.persistLocked()
	return m
}

func (m *maintAuth) check(got string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a := []byte(m.pass)
	b := []byte(got)
	if len(a) != len(b) {
		// still compare to keep timing roughly flat
		_ = subtle.ConstantTimeCompare(a, a)
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pass = neu
	return m.persistLocked()
}

func (m *maintAuth) persistLocked() error {
	if m.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(m.pass+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

type maintError string

func (e maintError) Error() string { return string(e) }

const (
	errMaintEmpty  = maintError("新密码不能为空")
	errMaintShort  = maintError("新密码至少 4 位")
	errMaintBadOld = maintError("当前密码不正确")
)
