package portal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FeishuBindings maps portal login account → Feishu user_id / open_id used in QR#1.
type FeishuBindings struct {
	Path string
	mu   sync.Mutex
}

type bindingFile struct {
	UpdatedAt time.Time         `json:"updatedAt"`
	Users     map[string]string `json:"users"` // account -> feishuUserId
}

func (b *FeishuBindings) path() string {
	if b.Path != "" {
		return b.Path
	}
	return "/var/lib/fdt/feishu_bindings.json"
}

func (b *FeishuBindings) load() (bindingFile, error) {
	var f bindingFile
	raw, err := os.ReadFile(b.path())
	if err != nil {
		if os.IsNotExist(err) {
			return bindingFile{Users: map[string]string{}}, nil
		}
		return f, err
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return f, err
	}
	if f.Users == nil {
		f.Users = map[string]string{}
	}
	return f, nil
}

func (b *FeishuBindings) save(f bindingFile) error {
	f.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(b.path()), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path())
}

// Get returns the bound Feishu user id for account (empty if unbound).
func (b *FeishuBindings) Get(account string) string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := b.load()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(f.Users[strings.ToLower(strings.TrimSpace(account))])
}

// Set binds account to feishuUserId. Empty feishuUserId clears the binding.
func (b *FeishuBindings) Set(account, feishuUserID string) error {
	if b == nil {
		return os.ErrInvalid
	}
	account = strings.ToLower(strings.TrimSpace(account))
	feishuUserID = strings.TrimSpace(feishuUserID)
	if account == "" {
		return os.ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := b.load()
	if err != nil {
		return err
	}
	if feishuUserID == "" {
		delete(f.Users, account)
	} else {
		f.Users[account] = feishuUserID
	}
	return b.save(f)
}
