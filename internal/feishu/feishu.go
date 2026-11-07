// Package feishu abstracts the Feishu (Lark) approval platform. The gateway
// uses a Client to create approval instances from a verified QR#1 token and to
// authenticate approval-completion callbacks before minting QR#2.
//
// Two implementations:
//   - MockClient: offline, deterministic, used by tests and the end-to-end demo.
//   - LiveClient: real Feishu OpenAPI calls (needs app credentials + network).
//
// The approval ENGINE is always native Feishu; this package only initiates
// instances and reads results. No approval workflow is reimplemented here.
package feishu

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ApprovalStatus is the lifecycle of a Feishu approval instance.
type ApprovalStatus string

const (
	StatusPending  ApprovalStatus = "PENDING"
	StatusApproved ApprovalStatus = "APPROVED"
	StatusRejected ApprovalStatus = "REJECTED"
	StatusCanceled ApprovalStatus = "CANCELED"
)

// CreateInstanceRequest is the metadata the gateway pushes into a Feishu
// approval form. It carries only manifest metadata — never file bytes.
type CreateInstanceRequest struct {
	ReqID          string
	ApplicantID    string
	ApplicantName  string
	Dept           string
	Reason         string
	Classification string
	// Direction selects which Feishu approval definition to use (export vs import).
	Direction string // "export" | "import"
	// FeishuTenantKey identifies the customer's Feishu organization for the ISV
	// (multi-company SaaS) model; empty for a single-org self-built app.
	FeishuTenantKey string
	// ManifestText is the human-readable file list or summary rendered into the
	// approval form so the approver can judge without seeing file content.
	ManifestText string
	MerkleRoot   string
	FileCount    int
	TotalSize    int64
	// Extras carries custom portal form values keyed by FormWidget.Field.
	Extras map[string]string
}

// Instance is a created approval instance.
type Instance struct {
	ID        string
	ReqID     string
	Status    ApprovalStatus
	Approver  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ApprovalEvent is the normalized result of a verified completion callback.
type ApprovalEvent struct {
	InstanceID string
	ReqID      string
	Status     ApprovalStatus
	ApproverID string
	Approver   string
	Type       string // e.g. "approval_instance"
}

// Client is the Feishu approval boundary.
type Client interface {
	CreateApprovalInstance(ctx context.Context, req CreateInstanceRequest) (*Instance, error)
	GetApprovalStatus(ctx context.Context, instanceID string) (*Instance, error)
	// VerifyCallback authenticates a Feishu event callback (decrypt + token +
	// source checks) and normalizes it. Returns ErrBadCallback if invalid.
	VerifyCallback(ctx context.Context, body []byte) (*ApprovalEvent, error)
}

var (
	ErrBadCallback = errors.New("feishu: callback verification failed")
	ErrUnknownInst = errors.New("feishu: unknown approval instance")
)

// MockClient is an offline, deterministic Client. CreateApprovalInstance can
// auto-approve (AutoApprove) to exercise the full flow without Feishu. It is a
// test/dev double, clearly not a production integration.
type MockClient struct {
	mu          sync.Mutex
	AutoApprove bool
	Approver    string
	seq         int
	instances   map[string]*Instance
	byReq       map[string]string
	// CallbackBody, if set, is returned verbatim by VerifyCallback for tests.
	nextEvent *ApprovalEvent
}

func NewMockClient(autoApprove bool, approver string) *MockClient {
	return &MockClient{
		AutoApprove: autoApprove, Approver: approver,
		instances: map[string]*Instance{}, byReq: map[string]string{},
	}
}

func (m *MockClient) CreateApprovalInstance(_ context.Context, req CreateInstanceRequest) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq++
	id := "fs-inst-" + req.ReqID
	now := time.Now()
	inst := &Instance{
		ID: id, ReqID: req.ReqID, Status: StatusPending,
		CreatedAt: now, UpdatedAt: now,
	}
	if m.AutoApprove {
		inst.Status = StatusApproved
		inst.Approver = m.Approver
	}
	m.instances[id] = inst
	m.byReq[req.ReqID] = id
	return inst, nil
}

func (m *MockClient) GetApprovalStatus(_ context.Context, instanceID string) (*Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[instanceID]
	if !ok {
		return nil, ErrUnknownInst
	}
	cp := *inst
	return &cp, nil
}

// Approve flips an instance to approved (simulates the approver acting).
func (m *MockClient) Approve(instanceID, approver string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.instances[instanceID]
	if !ok {
		return ErrUnknownInst
	}
	inst.Status = StatusApproved
	inst.Approver = approver
	inst.UpdatedAt = time.Now()
	return nil
}

// SetNextEvent arms VerifyCallback to return a specific event (test helper).
func (m *MockClient) SetNextEvent(ev *ApprovalEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextEvent = ev
}

func (m *MockClient) VerifyCallback(_ context.Context, body []byte) (*ApprovalEvent, error) {
	if ch, ok := PeekURLChallenge(body); ok {
		return &ApprovalEvent{Type: "url_verification", InstanceID: ch}, nil
	}
	if _, challenge, err := VerifyCallbackBody(body, "", ""); err == nil && challenge != "" {
		return &ApprovalEvent{Type: "url_verification", InstanceID: challenge}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nextEvent == nil {
		return nil, ErrBadCallback
	}
	ev := m.nextEvent
	m.nextEvent = nil
	return ev, nil
}
