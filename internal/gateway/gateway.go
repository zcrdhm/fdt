// Package gateway is the DMZ approval bridge — the only internet-facing
// component. It is multi-tenant: one shared gateway can serve many customer
// sites/orgs. Per tenant it holds that tenant's PK_内网 (verify QR#1), SK_网关
// signer (mint QR#2) and Feishu client. The tenant is carried in the QR frame's
// plaintext prefix and cross-checked against the signed payload, so a frame is
// always verified with its own tenant's key and can never be cross-routed.
//
// Self-hosted single-tenant deployments use one tenant entry ("default"); the
// SaaS deployment registers one entry per customer. The gateway never touches
// file data — only signed metadata.
package gateway

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"fdt/internal/feishu"
	"fdt/internal/model"
	"fdt/internal/token"
)

var (
	ErrUnknownReq           = errors.New("gateway: unknown request/session")
	ErrUnknownTenant        = errors.New("gateway: unknown tenant")
	ErrTenantExists         = errors.New("gateway: tenant already registered")
	ErrNotApproved          = errors.New("gateway: approval not yet granted")
	ErrNotRequested         = errors.New("gateway: no QR#1 submitted for this instance")
	ErrSubscriptionExpired  = errors.New("gateway: 订阅已到期，请联系管理员续期")
)

// TenantConfig is everything the gateway needs for one customer site/org.
type TenantConfig struct {
	InternalPub     ed25519.PublicKey // PK_内网 — verify this tenant's QR#1
	Signer          token.Signer      // SK_网关 — sign this tenant's QR#2 (HSM label)
	Feishu          feishu.Client     // self-built per tenant, or a shared ISVClient
	FeishuTenantKey string            // customer's Feishu org key (ISV model)
	// ExpiresAt is the subscription end (inclusive). Zero means never expires.
	ExpiresAt time.Time
}

// SubscriptionExpired reports whether the tenant subscription is past ExpiresAt.
func (c *TenantConfig) SubscriptionExpired(now time.Time) bool {
	if c == nil || c.ExpiresAt.IsZero() {
		return false
	}
	return now.After(c.ExpiresAt)
}

// Service is the multi-tenant gateway core.
type Service struct {
	ReleaseTTL time.Duration
	// Sessions, when set, persists approval state so a restart can still mint
	// or re-serve QR#2. Memory maps remain the hot path.
	Sessions SessionStore

	mu      sync.Mutex
	tenants map[string]*TenantConfig
	byReq   map[string]*session
	byInst  map[string]*session
}

type session struct {
	Tenant       string
	ReqToken     *model.RequestToken
	InstanceID   string
	ManifestText string
	Status       feishu.ApprovalStatus
	Approver     string
	QR2Frame     string
	QR2Exp       time.Time
}

// New creates an empty multi-tenant gateway.
func New(releaseTTL time.Duration) *Service {
	if releaseTTL <= 0 {
		releaseTTL = 72 * time.Hour // historical release codes remain redeemable for 3 days
	}
	return &Service{
		ReleaseTTL: releaseTTL,
		tenants:    map[string]*TenantConfig{},
		byReq:      map[string]*session{},
		byInst:     map[string]*session{},
	}
}

// AddTenant registers a tenant. IDs must be codec-safe (alnum/-/_).
func (s *Service) AddTenant(id string, cfg TenantConfig) error {
	if id == "" {
		return ErrUnknownTenant
	}
	if cfg.InternalPub == nil || cfg.Signer == nil || cfg.Feishu == nil {
		return fmt.Errorf("gateway: tenant %s missing InternalPub/Signer/Feishu", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; ok {
		return ErrTenantExists
	}
	cp := cfg
	s.tenants[id] = &cp
	return nil
}

// ReplaceTenant inserts or overwrites a tenant config (admin hot-reload).
func (s *Service) ReplaceTenant(id string, cfg TenantConfig) error {
	if id == "" {
		return ErrUnknownTenant
	}
	if cfg.InternalPub == nil || cfg.Signer == nil || cfg.Feishu == nil {
		return fmt.Errorf("gateway: tenant %s missing InternalPub/Signer/Feishu", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := cfg
	s.tenants[id] = &cp
	return nil
}

// RemoveTenant drops a tenant from the live registry (does not wipe sessions).
func (s *Service) RemoveTenant(id string) error {
	if id == "" {
		return ErrUnknownTenant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tenants[id]; !ok {
		return ErrUnknownTenant
	}
	delete(s.tenants, id)
	return nil
}

// HasTenant reports whether id is registered in the live service.
func (s *Service) HasTenant(id string) bool {
	_, ok := s.tenant(id)
	return ok
}

// NewSingleTenant is a convenience for self-hosted deployments: one tenant
// registered under the default id.
func NewSingleTenant(pub ed25519.PublicKey, signer token.Signer, fs feishu.Client, releaseTTL time.Duration) *Service {
	s := New(releaseTTL)
	_ = s.AddTenant("default", TenantConfig{InternalPub: pub, Signer: signer, Feishu: fs})
	return s
}

func (s *Service) tenant(id string) (*TenantConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	return t, ok
}

// Tenants lists registered tenant ids (for ops/health).
// FirstLiveJSAPI returns a JSAPI signer from the first live Feishu tenant, if any.
// Used so H5 scanCode works after console binds live credentials without restart.
func (s *Service) FirstLiveJSAPI() feishu.JSAPISigner {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.tenants))
	for id := range s.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if live, ok := s.tenants[id].Feishu.(feishu.JSAPISigner); ok {
			// MockJSAPI also implements JSAPISigner; skip pure mocks via type.
			if _, isMock := s.tenants[id].Feishu.(*feishu.MockClient); isMock {
				continue
			}
			if _, isMockJS := live.(feishu.MockJSAPI); isMockJS {
				continue
			}
			return live
		}
	}
	return nil
}

// IdentityInfo is the logged-in Feishu user resolved from an H5 auth code.
type IdentityInfo struct {
	Name         string `json:"name"`
	ApplicantID  string `json:"applicantId"` // id used for审批申请人（按租户 userIdType）
	UserIDType   string `json:"userIdType"`
	UserID       string `json:"userId,omitempty"`
	OpenID       string `json:"openId,omitempty"`
	UnionID      string `json:"unionId,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
}

// ResolveIdentity exchanges authCode via the first live Feishu tenant.
func (s *Service) ResolveIdentity(ctx context.Context, authCode string) (*IdentityInfo, error) {
	if strings.TrimSpace(authCode) == "" {
		return nil, fmt.Errorf("gateway: authCode required")
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.tenants))
	for id := range s.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var idp feishu.IdentityFromCode
	tenantID := ""
	userIDType := ""
	for _, id := range ids {
		cfg := s.tenants[id]
		if _, isMock := cfg.Feishu.(*feishu.MockClient); isMock {
			continue
		}
		live, ok := cfg.Feishu.(feishu.IdentityFromCode)
		if !ok {
			continue
		}
		idp = live
		tenantID = id
		if lc, ok := cfg.Feishu.(*feishu.LiveClient); ok {
			userIDType = lc.UserIDType()
		}
		break
	}
	s.mu.Unlock()
	if idp == nil {
		return nil, fmt.Errorf("gateway: 未配置 live 飞书租户，无法解析身份")
	}
	u, err := idp.UserFromAuthCode(ctx, authCode)
	if err != nil {
		return nil, err
	}
	if userIDType == "" {
		userIDType = "user_id"
	}
	// Portal 绑定优先飞书 user_id；通讯录权限未生效前可暂用 open_id。
	bindID := u.UserID
	if bindID == "" {
		bindID = idp.ApplicantIDFor(u)
	}
	if bindID == "" {
		bindID = u.OpenID
	}
	return &IdentityInfo{
		Name: u.Name, ApplicantID: bindID, UserIDType: userIDType,
		UserID: u.UserID, OpenID: u.OpenID, UnionID: u.UnionID, Tenant: tenantID,
	}, nil
}

func (s *Service) Tenants() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.tenants))
	for id := range s.tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Submission is the result of accepting a scanned QR#1.
type Submission struct {
	Tenant       string                `json:"tenant"`
	ReqID        string                `json:"reqId"`
	InstanceID   string                `json:"instanceId"`
	Status       feishu.ApprovalStatus `json:"status"`
	ManifestText string                `json:"manifestText"`
	Direction    string                `json:"direction,omitempty"` // export | import
}

// SubmitQR1 routes by the frame's tenant prefix, verifies with that tenant's
// PK_内网, and creates the Feishu approval instance for that tenant.
//
// authCode is the Feishu H5 tt.requestAuthCode result. When the tenant uses a
// live Feishu app, the logged-in phone user becomes the approval applicant
// ("谁扫码谁申请") so ATM/Portal need not collect Feishu IDs for import.
// Empty authCode is allowed only for mock / offline Feishu clients.
func (s *Service) SubmitQR1(ctx context.Context, frame, authCode string) (*Submission, error) {
	tenantID, err := token.PeekTenant(frame)
	if err != nil {
		return nil, fmt.Errorf("gateway: frame tenant: %w", err)
	}
	cfg, ok := s.tenant(tenantID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTenant, tenantID)
	}
	if cfg.SubscriptionExpired(time.Now()) {
		return nil, ErrSubscriptionExpired
	}
	tok, err := token.OpenRequest(frame, cfg.InternalPub)
	if err != nil {
		return nil, fmt.Errorf("gateway: QR#1 verify: %w", err)
	}
	if err := token.CheckValidity(tok.Iat, tok.Exp, time.Now()); err != nil {
		return nil, fmt.Errorf("gateway: QR#1 validity: %w", err)
	}

	applicantID := tok.Applicant.ID
	applicantName := tok.Applicant.Name
	if idp, ok := cfg.Feishu.(feishu.IdentityFromCode); ok {
		if authCode == "" {
			return nil, fmt.Errorf("gateway: 请在飞书客户端打开本应用并授权后提交（谁扫码谁申请）")
		}
		u, err := idp.UserFromAuthCode(ctx, authCode)
		if err != nil {
			return nil, fmt.Errorf("gateway: 飞书身份授权失败: %w", err)
		}
		applicantID = idp.ApplicantIDFor(u)
		if applicantID == "" {
			// 通讯录 user_id 权限未生效时，回退 open_id，避免扫码提交直接失败。
			applicantID = u.OpenID
		}
		if applicantID == "" {
			return nil, fmt.Errorf("gateway: 飞书身份缺少 user_id/open_id")
		}
		if u.Name != "" {
			applicantName = u.Name
		}
		tok.Applicant.ID = applicantID
		tok.Applicant.Name = applicantName
	}

	manifestText := RenderManifest(tok.Dataset, tok.Reason, tok.Cls, tok.Dir)

	inst, err := cfg.Feishu.CreateApprovalInstance(ctx, feishu.CreateInstanceRequest{
		ReqID: tok.ReqID, ApplicantID: applicantID, ApplicantName: applicantName,
		Dept: tok.Applicant.Dept, Reason: tok.Reason, Classification: string(tok.Cls),
		Direction: string(tok.Dir),
		FeishuTenantKey: cfg.FeishuTenantKey, ManifestText: manifestText,
		MerkleRoot: tok.Dataset.MerkleRoot, FileCount: tok.Dataset.Count, TotalSize: tok.Dataset.TotalSize,
		Extras: tok.Extras,
	})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	sess := &session{
		Tenant: tenantID, ReqToken: tok, InstanceID: inst.ID, ManifestText: manifestText,
		Status: inst.Status, Approver: inst.Approver,
	}
	s.byReq[tok.ReqID] = sess
	s.byInst[inst.ID] = sess
	if inst.Status == feishu.StatusApproved {
		if err := s.mintQR2Locked(sess); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	s.persistLocked(sess)
	s.mu.Unlock()

	return &Submission{Tenant: tenantID, ReqID: tok.ReqID, InstanceID: inst.ID,
		Status: inst.Status, ManifestText: manifestText, Direction: string(tok.Dir)}, nil
}

// QR2Result is returned when polling for the release token.
type QR2Result struct {
	Tenant    string                `json:"tenant"`
	ReqID     string                `json:"reqId"`
	Status    feishu.ApprovalStatus `json:"status"`
	QR2Frame  string                `json:"qr2Frame,omitempty"`
	Approver  string                `json:"approver,omitempty"`
	Exp       int64                 `json:"exp,omitempty"`
	Direction string                `json:"direction,omitempty"` // export | import
}

// HistoryItem is a compact session summary for the H5 history list.
type HistoryItem struct {
	Tenant    string                `json:"tenant"`
	ReqID     string                `json:"reqId"`
	Status    feishu.ApprovalStatus `json:"status"`
	HasQR2    bool                  `json:"hasQR2"`
	QR2Valid  bool                  `json:"qr2Valid"`
	Approver  string                `json:"approver,omitempty"`
	Applicant string                `json:"applicant,omitempty"`
	Reason    string                `json:"reason,omitempty"`
	Exp       int64                 `json:"exp,omitempty"`
	Direction string                `json:"direction,omitempty"` // export | import
	FileCount int                   `json:"fileCount,omitempty"`
	TotalSize int64                 `json:"totalSize,omitempty"`
	FileNames []string              `json:"fileNames,omitempty"` // physical paths from QR#1 (capped)
}

// historyFileNames picks physical file paths from the QR#1 dataset for H5 display.
func historyFileNames(ds model.Dataset, max int) []string {
	if max <= 0 {
		max = 3
	}
	out := make([]string, 0, max)
	for _, e := range ds.Entries {
		if strings.TrimSpace(e.InArchive) != "" {
			continue
		}
		p := strings.TrimSpace(e.Path)
		if p == "" || p == "." {
			continue
		}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	return out
}

// ListHistory returns recent sessions (newest first). tenant filters when non-empty.
func (s *Service) ListHistory(tenant string, limit int) []HistoryItem {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	items := make([]HistoryItem, 0, len(s.byReq))
	for _, sess := range s.byReq {
		if sess == nil || sess.ReqToken == nil {
			continue
		}
		if tenant != "" && sess.Tenant != tenant {
			continue
		}
		tok := sess.ReqToken
		it := HistoryItem{
			Tenant: sess.Tenant, ReqID: tok.ReqID, Status: sess.Status,
			HasQR2: sess.QR2Frame != "", Approver: sess.Approver,
			Applicant: tok.Applicant.Name, Reason: tok.Reason,
			Direction: string(tok.Dir),
			FileCount: tok.Dataset.Count, TotalSize: tok.Dataset.TotalSize,
			FileNames: historyFileNames(tok.Dataset, 3),
		}
		if !sess.QR2Exp.IsZero() {
			it.Exp = sess.QR2Exp.Unix()
			it.QR2Valid = sess.QR2Frame != "" && now.Before(sess.QR2Exp)
		} else if sess.QR2Frame != "" {
			it.QR2Valid = true
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Exp != items[j].Exp {
			return items[i].Exp > items[j].Exp
		}
		return items[i].ReqID > items[j].ReqID
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// PollQR2 checks the tenant's Feishu status and, once approved, returns QR#2.
func (s *Service) PollQR2(ctx context.Context, reqID string) (*QR2Result, error) {
	s.mu.Lock()
	sess, ok := s.byReq[reqID]
	if !ok {
		s.mu.Unlock()
		return nil, ErrUnknownReq
	}
	cfg := s.tenants[sess.Tenant]
	if sess.QR2Frame == "" && sess.Status != feishu.StatusApproved && cfg != nil {
		inst, err := cfg.Feishu.GetApprovalStatus(ctx, sess.InstanceID)
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		sess.Status = inst.Status
		if inst.Approver != "" {
			sess.Approver = inst.Approver
		}
		if inst.Status == feishu.StatusApproved {
			if err := s.mintQR2Locked(sess); err != nil {
				if !errors.Is(err, ErrSubscriptionExpired) {
					s.mu.Unlock()
					return nil, err
				}
				// Keep approval status but do not mint QR#2 after subscription end.
			}
		}
		s.persistLocked(sess)
	}
	if sess.QR2Frame == "" {
		if cfg := s.tenants[sess.Tenant]; cfg != nil && cfg.SubscriptionExpired(time.Now()) {
			s.mu.Unlock()
			return nil, ErrSubscriptionExpired
		}
	}
	res := &QR2Result{Tenant: sess.Tenant, ReqID: reqID, Status: sess.Status,
		QR2Frame: sess.QR2Frame, Approver: sess.Approver}
	if sess.ReqToken != nil {
		res.Direction = string(sess.ReqToken.Dir)
	}
	if !sess.QR2Exp.IsZero() {
		res.Exp = sess.QR2Exp.Unix()
	}
	s.mu.Unlock()
	return res, nil
}

// HandleCallback processes a Feishu event push. verifyTenant selects which
// tenant's Feishu client decrypts/verifies the callback (self-built apps have
// per-tenant keys; an ISV deployment passes its shared vendor tenant id).
// A non-empty challenge means URL verification — the caller must echo it.
func (s *Service) HandleCallback(ctx context.Context, verifyTenant string, rawBody []byte) (challenge string, err error) {
	cfg, ok := s.tenant(verifyTenant)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownTenant, verifyTenant)
	}
	ev, err := cfg.Feishu.VerifyCallback(ctx, rawBody)
	if err != nil {
		return "", err
	}
	if ev.Type == "url_verification" {
		return ev.InstanceID, nil // InstanceID carries the challenge in our adapter
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byInst[ev.InstanceID]
	if !ok {
		return "", ErrNotRequested
	}
	sess.Status = ev.Status
	if ev.Approver != "" {
		sess.Approver = ev.Approver
	}
	if ev.Status == feishu.StatusApproved && sess.QR2Frame == "" {
		if err := s.mintQR2Locked(sess); err != nil && !errors.Is(err, ErrSubscriptionExpired) {
			return "", err
		}
	}
	s.persistLocked(sess)
	return "", nil
}

// RestoreSessions loads durable sessions into memory (call once at startup).
func (s *Service) RestoreSessions(ctx context.Context) error {
	if s.Sessions == nil {
		return nil
	}
	list, err := s.Sessions.LoadAll(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sess := range list {
		if sess == nil || sess.ReqToken == nil {
			continue
		}
		s.byReq[sess.ReqToken.ReqID] = sess
		if sess.InstanceID != "" {
			s.byInst[sess.InstanceID] = sess
		}
	}
	return nil
}

func (s *Service) persistLocked(sess *session) {
	if s.Sessions == nil || sess == nil {
		return
	}
	// Best-effort: a failed write leaves the in-memory session usable until restart.
	_ = s.Sessions.Save(context.Background(), sess)
}

// SubscriptionStatus describes whether a tenant may still start/finish approvals.
type SubscriptionStatus struct {
	Tenant        string `json:"tenant,omitempty"`
	Expired       bool   `json:"expired"`
	ExpiresAtDate string `json:"expiresAtDate,omitempty"`
	NeverExpires  bool   `json:"neverExpires,omitempty"`
	Ambiguous     bool   `json:"ambiguous,omitempty"` // multi-tenant and no id given
	Message       string `json:"message,omitempty"`
}

// PeekTenantID exposes the frame's plaintext tenant for HTTP helpers.
func (s *Service) PeekTenantID(frame string) (string, error) {
	return token.PeekTenant(frame)
}

// LookupSubscription returns expiry info. Empty tenant uses the sole live tenant
// when exactly one is registered (typical single-customer gateway).
func (s *Service) LookupSubscription(tenant string) SubscriptionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		ids := make([]string, 0, len(s.tenants))
		for id := range s.tenants {
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return SubscriptionStatus{NeverExpires: true}
		}
		if len(ids) > 1 {
			return SubscriptionStatus{Ambiguous: true}
		}
		tenant = ids[0]
	}
	cfg, ok := s.tenants[tenant]
	if !ok || cfg == nil {
		return SubscriptionStatus{Tenant: tenant, Message: "unknown tenant"}
	}
	st := SubscriptionStatus{Tenant: tenant}
	if cfg.ExpiresAt.IsZero() {
		st.NeverExpires = true
		return st
	}
	st.ExpiresAtDate = cfg.ExpiresAt.In(time.Local).Format("2006-01-02")
	if cfg.SubscriptionExpired(time.Now()) {
		st.Expired = true
		st.Message = "订阅已到期，请联系管理员续期"
	}
	return st
}

// mintQR2Locked signs QR#2 with the SESSION's tenant key. Caller holds s.mu.
func (s *Service) mintQR2Locked(sess *session) error {
	if sess.QR2Frame != "" {
		return nil
	}
	cfg := s.tenants[sess.Tenant]
	if cfg == nil {
		return fmt.Errorf("%w: %s", ErrUnknownTenant, sess.Tenant)
	}
	if cfg.SubscriptionExpired(time.Now()) {
		return ErrSubscriptionExpired
	}
	now := time.Now()
	exp := now.Add(s.ReleaseTTL)
	rel := &model.ReleaseToken{
		Tenant:       sess.Tenant,
		ReqID:        sess.ReqToken.ReqID,
		Dir:          sess.ReqToken.Dir,
		MerkleRoot:   sess.ReqToken.Dataset.MerkleRoot,
		FsApprovalID: sess.InstanceID,
		Approver:     model.Applicant{Name: sess.Approver},
		Applicant:    sess.ReqToken.Applicant, // Feishu identity after authCode
		Nonce:        fmt.Sprintf("%x", now.UnixNano()),
		Iat:          now.Unix(),
		Exp:          exp.Unix(),
	}
	frame, err := token.SignRelease(cfg.Signer, rel)
	if err != nil {
		return err
	}
	sess.QR2Frame = frame
	sess.QR2Exp = exp
	return nil
}

// RenderManifest formats the dataset into human-readable text for the approval
// form. Full mode lists files; summary mode shows the aggregated tree. The
// Merkle root is always included so the approval binds exact bytes.
func RenderManifest(ds model.Dataset, reason string, cls model.Classification, dir model.Direction) string {
	var b strings.Builder
	dirLabel := "导出"
	if dir == model.DirImport {
		dirLabel = "导入"
	}
	fmt.Fprintf(&b, "方向: %s\n事由: %s\n密级: %s\n文件数: %d  总大小: %d 字节\nMerkle根: %s\n",
		dirLabel, reason, cls, ds.Count, ds.TotalSize, ds.MerkleRoot)
	if len(ds.DLP.Categories) > 0 {
		keys := make([]string, 0, len(ds.DLP.Categories))
		for k := range ds.DLP.Categories {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s×%d", k, ds.DLP.Categories[k]))
		}
		fmt.Fprintf(&b, "DLP分类: %s\n", strings.Join(parts, ", "))
	}
	if ds.DLP.AV != "" {
		fmt.Fprintf(&b, "AV: %s\n", ds.DLP.AV)
	}
	for _, h := range ds.DLP.Hits {
		fmt.Fprintf(&b, "命中: %s\n", h)
	}
	switch ds.Mode {
	case model.ModeFull:
		b.WriteString("--- 文件清单 ---\n")
		for _, e := range ds.Entries {
			if e.InArchive != "" {
				fmt.Fprintf(&b, "  [包内] %s (%d B, %s)\n", e.Path, e.Size, e.HashHex)
			} else {
				fmt.Fprintf(&b, "  %s (%d B, %s, %s)\n", e.Path, e.Size, e.Type, e.HashHex)
			}
		}
	case model.ModeSummary:
		b.WriteString("--- 目录摘要(完整清单仅内网留存) ---\n")
		for _, n := range ds.Tree {
			fmt.Fprintf(&b, "  %s/  文件%d  %d B  子根:%s\n", n.Name, n.Count, n.Size, short(n.MerkleChild))
		}
	}
	return b.String()
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
