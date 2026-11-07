package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ISVClient serves MANY customer Feishu organizations through ONE vendor ISV
// (marketplace) app — the multi-company SaaS model. Each approval call uses the
// target organization's tenant_access_token, obtained via TenantTokenSource.
//
// Feishu's ISV token flow (app_ticket -> app_access_token -> per-tenant
// tenant_access_token) requires live credentials and periodic app_ticket pushes,
// so token acquisition is injected via TenantTokenSource rather than hard-coded
// here. Wire it to your ISV token store; the rest (create/read instance,
// callback verification) is implemented.
type ISVClient struct {
	cfg  ISVConfig
	http *http.Client

	// TenantTokenSource returns a valid tenant_access_token for the given
	// customer Feishu tenant key (the operator implements the app_ticket flow).
	TenantTokenSource func(ctx context.Context, tenantKey string) (string, error)

	mu         sync.Mutex
	instTenant map[string]string // instanceID -> tenantKey (for status lookups)
}

type ISVConfig struct {
	AppID             string
	EncryptKey        string // vendor-level callback encrypt key
	VerificationToken string
	BaseURL           string // default https://open.feishu.cn
	// ApprovalCode resolves the export approval definition per tenant; if a tenant is
	// absent, DefaultApprovalCode is used.
	ApprovalCodeByTenant map[string]string
	DefaultApprovalCode  string
	// Import codes (optional). Empty → fall back to export code for that tenant.
	ApprovalCodeImportByTenant map[string]string
	DefaultApprovalCodeImport  string
	UserIDType                 string
	FormWidgets                []FormWidget
}

func NewISVClient(cfg ISVConfig, tokenSrc func(context.Context, string) (string, error)) *ISVClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://open.feishu.cn"
	}
	if cfg.UserIDType == "" {
		cfg.UserIDType = "open_id"
	}
	return &ISVClient{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second},
		TenantTokenSource: tokenSrc, instTenant: map[string]string{}}
}

func (c *ISVClient) approvalCode(tenantKey, dir string) string {
	if dir == "import" {
		if code, ok := c.cfg.ApprovalCodeImportByTenant[tenantKey]; ok && code != "" {
			return code
		}
		if c.cfg.DefaultApprovalCodeImport != "" {
			return c.cfg.DefaultApprovalCodeImport
		}
	}
	if code, ok := c.cfg.ApprovalCodeByTenant[tenantKey]; ok && code != "" {
		return code
	}
	return c.cfg.DefaultApprovalCode
}

func (c *ISVClient) CreateApprovalInstance(ctx context.Context, r CreateInstanceRequest) (*Instance, error) {
	if c.TenantTokenSource == nil {
		return nil, fmt.Errorf("feishu: ISV TenantTokenSource not configured")
	}
	tok, err := c.TenantTokenSource(ctx, r.FeishuTenantKey)
	if err != nil {
		return nil, err
	}
	formJSON, err := BuildApprovalForm(c.cfg.FormWidgets, r)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"approval_code":  c.approvalCode(r.FeishuTenantKey, r.Direction),
		c.cfg.UserIDType: r.ApplicantID,
		"form":           formJSON,
	}
	body, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/open-apis/approval/v4/instances", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			InstanceCode string `json:"instance_code"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("feishu(isv): create instance error %d %s", out.Code, out.Msg)
	}
	c.mu.Lock()
	c.instTenant[out.Data.InstanceCode] = r.FeishuTenantKey
	c.mu.Unlock()
	return &Instance{ID: out.Data.InstanceCode, ReqID: r.ReqID, Status: StatusPending, CreatedAt: time.Now()}, nil
}

func (c *ISVClient) GetApprovalStatus(ctx context.Context, instanceID string) (*Instance, error) {
	c.mu.Lock()
	tenantKey := c.instTenant[instanceID]
	c.mu.Unlock()
	if c.TenantTokenSource == nil {
		return nil, fmt.Errorf("feishu: ISV TenantTokenSource not configured")
	}
	tok, err := c.TenantTokenSource(ctx, tenantKey)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.BaseURL+"/open-apis/approval/v4/instances/"+instanceID, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("feishu(isv): get instance error %d %s", out.Code, out.Msg)
	}
	return &Instance{ID: instanceID, Status: normalizeStatus(out.Data.Status)}, nil
}

// VerifyCallback uses the vendor-level encrypt key / verification token shared
// by all installed tenants.
func (c *ISVClient) VerifyCallback(_ context.Context, body []byte) (*ApprovalEvent, error) {
	ev, challenge, err := VerifyCallbackBody(body, c.cfg.EncryptKey, c.cfg.VerificationToken)
	if err != nil {
		return nil, err
	}
	if challenge != "" {
		return &ApprovalEvent{Type: "url_verification", InstanceID: challenge}, nil
	}
	return ev, nil
}

var _ Client = (*ISVClient)(nil)
