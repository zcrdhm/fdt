// Package gwadmin provides gateway operator console and per-tenant customer
// consoles. Admin creates a tenant and a registration link; the customer
// registers an account, then manages PEM / Feishu bindings in their own console.
package gwadmin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"fdt/internal/codec"
	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/gateway"
	"fdt/internal/hsm"
)

var (
	ErrNotFound     = errors.New("gwadmin: tenant not found")
	ErrBadInvite    = errors.New("gwadmin: invalid or expired registration link")
	ErrBadID        = errors.New("gwadmin: invalid tenant id")
	ErrExists       = errors.New("gwadmin: tenant already exists")
	ErrUnauthorized = errors.New("gwadmin: unauthorized")
	ErrNotReady     = errors.New("gwadmin: tenant missing keys or feishu")
	ErrRegistered   = errors.New("gwadmin: already registered")
)

const (
	StatusPending    = "pending"    // awaiting customer registration
	StatusRegistered = "registered" // has console account; may still lack PEM/Feishu
	StatusActive     = "active"     // live on gateway
	StatusDisabled   = "disabled"
)

// Meta is persisted under tenants/<id>/meta.json.
type Meta struct {
	ID               string                   `json:"id"`
	DisplayName      string                   `json:"displayName,omitempty"`
	Status           string                   `json:"status"`
	CreatedAt        time.Time                `json:"createdAt"`
	UpdatedAt        time.Time                `json:"updatedAt"`
	// ExpiresAt is the subscription end (inclusive). Zero = never expires.
	ExpiresAt        time.Time                `json:"expiresAt,omitempty"`
	InviteToken      string                   `json:"inviteToken,omitempty"`
	InviteExpiry     time.Time                `json:"inviteExpiry,omitempty"`
	OperatorUser     string                   `json:"operatorUser,omitempty"`
	OperatorPassHash string                   `json:"operatorPassHash,omitempty"`
	ContactEmail     string                   `json:"contactEmail,omitempty"`
	ContactName      string                   `json:"contactName,omitempty"`
	Notes            string                   `json:"notes,omitempty"`
	Feishu           gateway.TenantFeishuFile `json:"feishu"`
}

// PublicView is secret-scrubbed for admin / console UIs.
type PublicView struct {
	ID                   string    `json:"id"`
	DisplayName          string    `json:"displayName,omitempty"`
	Status               string    `json:"status"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
	ExpiresAt            time.Time `json:"expiresAt,omitempty"`
	ExpiresAtDate        string    `json:"expiresAtDate,omitempty"` // YYYY-MM-DD for forms
	SubscriptionExpired  bool      `json:"subscriptionExpired"`
	HasInternalPub       bool      `json:"hasInternalPub"`
	HasGatewayKey        bool      `json:"hasGatewayKey"`
	FeishuBound          bool      `json:"feishuBound"`
	FeishuMode           string    `json:"feishuMode,omitempty"`
	ApprovalProvider     string    `json:"approvalProvider,omitempty"` // feishu|dingtalk|wecom
	ApprovalProviderNote string    `json:"approvalProviderNote,omitempty"`
	FeishuAppID          string    `json:"feishuAppId,omitempty"`
	FeishuApprovalCode       string `json:"feishuApprovalCode,omitempty"`
	FeishuApprovalCodeImport string `json:"feishuApprovalCodeImport,omitempty"`
	HasFeishuAppSecret       bool   `json:"hasFeishuAppSecret"`
	HasFeishuEncryptKey  bool      `json:"hasFeishuEncryptKey"`
	HasFeishuVerifyToken bool      `json:"hasFeishuVerifyToken"`
	OperatorUser         string    `json:"operatorUser,omitempty"`
	ContactEmail         string    `json:"contactEmail,omitempty"`
	ContactName          string    `json:"contactName,omitempty"`
	Notes                string    `json:"notes,omitempty"`
	RegisterURL          string    `json:"registerUrlPath,omitempty"`
	InviteExpiry         time.Time `json:"inviteExpiry,omitempty"`
}

// Store manages tenant directories and hot-reloads into gateway.Service.
type Store struct {
	Dir       string
	Index     string
	Service   *gateway.Service
	InviteTTL time.Duration
	mu        sync.Mutex
}

func (s *Store) inviteTTL() time.Duration {
	if s.InviteTTL > 0 {
		return s.InviteTTL
	}
	return 7 * 24 * time.Hour
}

func (s *Store) tenantDir(id string) string { return filepath.Join(s.Dir, id) }
func (s *Store) metaPath(id string) string  { return filepath.Join(s.tenantDir(id), "meta.json") }

func (s *Store) EnsureLayout() error { return os.MkdirAll(s.Dir, 0o700) }

// ParseExpiryDate parses YYYY-MM-DD as end-of-day local time. Empty → zero (never).
func ParseExpiryDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("有效期日期格式应为 YYYY-MM-DD")
	}
	// Inclusive through 23:59:59.999 that day.
	return t.Add(24*time.Hour - time.Millisecond), nil
}

// CreateTenant mints SK_网关 and a registration link (status=pending).
// expiresAt zero means never expires.
func (s *Store) CreateTenant(id, displayName string, expiresAt time.Time) (*PublicView, error) {
	if !codec.ValidTenant(id) {
		return nil, ErrBadID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.tenantDir(id)
	if _, err := os.Stat(dir); err == nil {
		return nil, ErrExists
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := crypto.WritePrivatePEM(filepath.Join(dir, "sk_gateway.pem"), kp.Priv); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := crypto.WritePublicPEM(filepath.Join(dir, "pk_gateway.pem"), kp.Pub); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	tok, err := randomToken(32)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	now := time.Now().UTC()
	meta := Meta{
		ID: id, DisplayName: displayName, Status: StatusPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: expiresAt,
		InviteToken: tok, InviteExpiry: now.Add(s.inviteTTL()),
		Feishu: gateway.TenantFeishuFile{Mode: ""},
	}
	if err := s.writeMetaLocked(meta); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	_ = s.rewriteIndexLocked()
	return s.viewLocked(meta), nil
}

// SetExpiresAt updates the subscription end date (zero clears / never expires).
func (s *Store) SetExpiresAt(id string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return err
	}
	meta.ExpiresAt = expiresAt
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return err
	}
	_ = s.rewriteIndexLocked()
	return s.syncLiveLocked(id, *meta)
}

func (s *Store) List() ([]PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PublicView
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil {
			continue
		}
		out = append(out, *s.viewLocked(*meta))
	}
	return out, nil
}

func (s *Store) Get(id string) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	return s.viewLocked(*meta), nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(s.tenantDir(id)); err != nil {
		return err
	}
	if s.Service != nil {
		_ = s.Service.RemoveTenant(id)
	}
	return s.rewriteIndexLocked()
}

func (s *Store) SetStatus(id, status string) error {
	switch status {
	case StatusPending, StatusRegistered, StatusActive, StatusDisabled:
	default:
		return fmt.Errorf("gwadmin: bad status %q", status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return err
	}
	meta.Status = status
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return err
	}
	_ = s.rewriteIndexLocked()
	return s.syncLiveLocked(id, *meta)
}

// Reinvite issues a new registration link (clears only if still pending / re-open).
func (s *Store) Reinvite(id string) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	tok, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	meta.InviteToken = tok
	meta.InviteExpiry = time.Now().UTC().Add(s.inviteTTL())
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	return s.viewLocked(*meta), nil
}

func (s *Store) LookupInvite(token string) (*Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookupInviteLocked(token)
}

// RegisterWithInvite creates the tenant operator account from a registration link.
func (s *Store) RegisterWithInvite(token, user, password, contactName, contactEmail string) (*PublicView, error) {
	if user == "" || len(password) < 8 {
		return nil, fmt.Errorf("gwadmin: user required and password >= 8 chars")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.lookupInviteLocked(token)
	if err != nil {
		return nil, err
	}
	if meta.OperatorPassHash != "" {
		return nil, ErrRegistered
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	meta.OperatorUser = user
	meta.OperatorPassHash = string(hash)
	meta.ContactName = contactName
	meta.ContactEmail = contactEmail
	meta.InviteToken = ""
	meta.InviteExpiry = time.Time{}
	meta.Status = StatusRegistered
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	return s.viewLocked(*meta), nil
}

// AuthenticateTenant validates console login; returns tenant id.
func (s *Store) AuthenticateTenant(user, password string) (tenantID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return "", ErrUnauthorized
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil || meta.OperatorUser == "" || meta.OperatorPassHash == "" {
			continue
		}
		if meta.OperatorUser != user {
			continue
		}
		if meta.Status == StatusDisabled {
			return "", ErrUnauthorized
		}
		if bcrypt.CompareHashAndPassword([]byte(meta.OperatorPassHash), []byte(password)) != nil {
			return "", ErrUnauthorized
		}
		return meta.ID, nil
	}
	return "", ErrUnauthorized
}

// UploadInternalPub replaces pk_internal.pem and refreshes live config if ready.
func (s *Store) UploadInternalPub(id string, pem []byte) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	pubPath := filepath.Join(s.tenantDir(id), "pk_internal.pem")
	tmp := pubPath + ".tmp"
	if err := os.WriteFile(tmp, pem, 0o644); err != nil {
		return nil, err
	}
	if _, err := crypto.ReadPublicPEM(tmp); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("gwadmin: bad pk_internal: %w", err)
	}
	if err := os.Rename(tmp, pubPath); err != nil {
		return nil, err
	}
	s.promoteIfReadyLocked(meta)
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	_ = s.rewriteIndexLocked()
	_ = s.syncLiveLocked(id, *meta)
	return s.viewLocked(*meta), nil
}

// UpdateFeishuFromConsole updates Feishu binding for a registered tenant.
// Empty secret fields keep the previously stored values so the UI can omit them.
func (s *Store) UpdateFeishuFromConsole(id string, f gateway.TenantFeishuFile) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	if f.Mode == "" {
		f.Mode = "live"
	}
	prev := meta.Feishu
	if f.Provider == "" {
		f.Provider = prev.Provider
	}
	f.Provider = f.NormalizedProvider()
	if f.Mode == "live" {
		if f.AppSecret == "" {
			f.AppSecret = prev.AppSecret
		}
		if f.EncryptKey == "" {
			f.EncryptKey = prev.EncryptKey
		}
		if f.VerificationToken == "" {
			f.VerificationToken = prev.VerificationToken
		}
		if f.AppID == "" {
			f.AppID = prev.AppID
		}
		if f.ApprovalCode == "" {
			f.ApprovalCode = prev.ApprovalCode
		}
		if f.ApprovalCodeImport == "" {
			f.ApprovalCodeImport = prev.ApprovalCodeImport
		}
	}
	meta.Feishu = f
	s.promoteIfReadyLocked(meta)
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	_ = s.rewriteIndexLocked()
	_ = s.syncLiveLocked(id, *meta)
	return s.viewLocked(*meta), nil
}

// UpdateProfile updates contact fields.
func (s *Store) UpdateProfile(id, displayName, contactName, contactEmail, notes string) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	if displayName != "" {
		meta.DisplayName = displayName
	}
	meta.ContactName = contactName
	meta.ContactEmail = contactEmail
	meta.Notes = notes
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	return s.viewLocked(*meta), nil
}

// ChangeOperatorPassword updates console password.
func (s *Store) ChangeOperatorPassword(id, oldPass, newPass string) error {
	if len(newPass) < 8 {
		return fmt.Errorf("gwadmin: password >= 8 chars")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(meta.OperatorPassHash), []byte(oldPass)) != nil {
		return ErrUnauthorized
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	meta.OperatorPassHash = string(hash)
	meta.UpdatedAt = time.Now().UTC()
	return s.writeMetaLocked(*meta)
}

func (s *Store) GatewayPubPEM(id string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.tenantDir(id), "pk_gateway.pem"))
}

// RegenerateGatewayKeys issues a new SK/PK_网关 (ATM must re-download pubkey).
func (s *Store) RegenerateGatewayKeys(id string) (*PublicView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.readMetaLocked(id)
	if err != nil {
		return nil, err
	}
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	dir := s.tenantDir(id)
	if err := crypto.WritePrivatePEM(filepath.Join(dir, "sk_gateway.pem"), kp.Priv); err != nil {
		return nil, err
	}
	if err := crypto.WritePublicPEM(filepath.Join(dir, "pk_gateway.pem"), kp.Pub); err != nil {
		return nil, err
	}
	meta.UpdatedAt = time.Now().UTC()
	if err := s.writeMetaLocked(*meta); err != nil {
		return nil, err
	}
	_ = s.rewriteIndexLocked()
	_ = s.syncLiveLocked(id, *meta)
	return s.viewLocked(*meta), nil
}

func (s *Store) promoteIfReadyLocked(meta *Meta) {
	if meta.Status == StatusDisabled || meta.Status == StatusPending {
		return
	}
	dir := s.tenantDir(meta.ID)
	_, errPub := os.Stat(filepath.Join(dir, "pk_internal.pem"))
	feishuOK := (meta.Feishu.Mode == "mock") ||
		(meta.Feishu.Mode == "live" && meta.Feishu.ProviderSupported() &&
			meta.Feishu.AppID != "" && meta.Feishu.AppSecret != "" && meta.Feishu.ApprovalCode != "")
	if errPub == nil && feishuOK {
		meta.Status = StatusActive
	} else if meta.OperatorPassHash != "" {
		meta.Status = StatusRegistered
	}
}

func (s *Store) lookupInviteLocked(token string) (*Meta, error) {
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, ErrBadInvite
	}
	now := time.Now().UTC()
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil {
			continue
		}
		if meta.InviteToken == token && (meta.InviteExpiry.IsZero() || !now.After(meta.InviteExpiry)) {
			return meta, nil
		}
	}
	return nil, ErrBadInvite
}

func (s *Store) readMetaLocked(id string) (*Meta, error) {
	b, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) writeMetaLocked(m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath(m.ID), append(b, '\n'), 0o600)
}

func (s *Store) viewLocked(m Meta) *PublicView {
	dir := s.tenantDir(m.ID)
	_, errPub := os.Stat(filepath.Join(dir, "pk_internal.pem"))
	_, errKey := os.Stat(filepath.Join(dir, "sk_gateway.pem"))
	provider := m.Feishu.NormalizedProvider()
	bound := m.Feishu.Mode == "mock" ||
		(m.Feishu.Mode == "live" && m.Feishu.ProviderSupported() &&
			m.Feishu.AppID != "" && m.Feishu.ApprovalCode != "")
	note := ""
	if m.Feishu.Mode == "live" && !m.Feishu.ProviderSupported() {
		note = m.Feishu.ProviderLabel() + "审批渠道即将支持，配置可先保存，当前仅飞书可发起审批"
	}
	v := &PublicView{
		ID: m.ID, DisplayName: m.DisplayName, Status: m.Status,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		ExpiresAt: m.ExpiresAt,
		SubscriptionExpired: !m.ExpiresAt.IsZero() && time.Now().After(m.ExpiresAt),
		HasInternalPub: errPub == nil, HasGatewayKey: errKey == nil,
		FeishuBound: bound, FeishuMode: m.Feishu.Mode,
		ApprovalProvider: provider, ApprovalProviderNote: note,
		FeishuAppID: m.Feishu.AppID,
		FeishuApprovalCode: m.Feishu.ApprovalCode,
		FeishuApprovalCodeImport: m.Feishu.ApprovalCodeImport,
		HasFeishuAppSecret:   m.Feishu.AppSecret != "",
		HasFeishuEncryptKey:  m.Feishu.EncryptKey != "",
		HasFeishuVerifyToken: m.Feishu.VerificationToken != "",
		OperatorUser: m.OperatorUser, ContactEmail: m.ContactEmail, ContactName: m.ContactName,
		Notes: m.Notes, InviteExpiry: m.InviteExpiry,
	}
	if !m.ExpiresAt.IsZero() {
		v.ExpiresAtDate = m.ExpiresAt.In(time.Local).Format("2006-01-02")
	}
	if m.InviteToken != "" {
		v.RegisterURL = "/register/#" + m.InviteToken
	}
	return v
}

func (s *Store) rewriteIndexLocked() error {
	if s.Index == "" {
		return nil
	}
	ents, err := os.ReadDir(s.Dir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	tf := gateway.TenantsFile{ReleaseTTL: "72h"}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil || meta.Status != StatusActive {
			continue
		}
		dir := s.tenantDir(meta.ID)
		pub := filepath.Join(dir, "pk_internal.pem")
		key := filepath.Join(dir, "sk_gateway.pem")
		if _, err := os.Stat(pub); err != nil {
			continue
		}
		if _, err := os.Stat(key); err != nil {
			continue
		}
		tf.Tenants = append(tf.Tenants, gateway.TenantFileEntry{
			ID: meta.ID, InternalPub: pub, GatewayKey: key, Feishu: meta.Feishu,
		})
	}
	b, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Index, append(b, '\n'), 0o600)
}

func (s *Store) syncLiveLocked(id string, meta Meta) error {
	if s.Service == nil {
		return nil
	}
	if meta.Status != StatusActive {
		_ = s.Service.RemoveTenant(id)
		return nil
	}
	cfg, err := s.buildConfigLocked(meta)
	if err != nil {
		_ = s.Service.RemoveTenant(id)
		return err
	}
	return s.Service.ReplaceTenant(id, *cfg)
}

func (s *Store) buildConfigLocked(meta Meta) (*gateway.TenantConfig, error) {
	dir := s.tenantDir(meta.ID)
	pub, err := crypto.ReadPublicPEM(filepath.Join(dir, "pk_internal.pem"))
	if err != nil {
		return nil, ErrNotReady
	}
	signer, err := hsm.NewSoftwareSignerFromFile("gw-"+meta.ID, filepath.Join(dir, "sk_gateway.pem"))
	if err != nil {
		return nil, err
	}
	client, _, err := buildFeishu(meta.Feishu)
	if err != nil {
		return nil, err
	}
	return &gateway.TenantConfig{
		InternalPub: pub, Signer: signer, Feishu: client, FeishuTenantKey: meta.Feishu.FeishuTenantKey,
		ExpiresAt: meta.ExpiresAt,
	}, nil
}

func buildFeishu(c gateway.TenantFeishuFile) (feishu.Client, *feishu.LiveClient, error) {
	switch c.Mode {
	case "", "mock":
		approver := c.Approver
		if approver == "" {
			approver = "mock-approver"
		}
		return feishu.NewMockClient(c.AutoApprove, approver), nil, nil
	case "live":
		if !c.ProviderSupported() {
			return nil, nil, fmt.Errorf("%s审批渠道即将支持，当前仅飞书可用", c.ProviderLabel())
		}
		if c.AppID == "" || c.AppSecret == "" || c.ApprovalCode == "" {
			return nil, nil, fmt.Errorf("live 模式需要 appId/appSecret/approvalCode")
		}
		live := feishu.NewLiveClient(feishu.LiveConfig{
			AppID: c.AppID, AppSecret: c.AppSecret, ApprovalCode: c.ApprovalCode,
			ApprovalCodeImport: c.ApprovalCodeImport,
			EncryptKey: c.EncryptKey, VerificationToken: c.VerificationToken,
			UserIDType: c.UserIDType,
		}, nil)
		return live, live, nil
	default:
		return nil, nil, fmt.Errorf("未知 feishu.mode %q", c.Mode)
	}
}

func (s *Store) SyncAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ents, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		meta, err := s.readMetaLocked(e.Name())
		if err != nil {
			continue
		}
		_ = s.syncLiveLocked(meta.ID, *meta)
	}
	return s.rewriteIndexLocked()
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
