package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LiveClient talks to the real Feishu OpenAPI. It requires a self-built app
// with approval scope and network egress from the DMZ gateway. The approval
// workflow itself is native Feishu; this client only creates instances and
// reads results.
type LiveClient struct {
	cfg      LiveConfig
	http     *http.Client
	resolver func(internalID string) (openID string, err error)
	ticket   ticketCache // jsapi_ticket cache for H5 webapp auth
}

type LiveConfig struct {
	AppID             string
	AppSecret         string
	ApprovalCode      string // export 审批定义 code（QR#1 dir=export）
	ApprovalCodeImport string // import 审批定义 code；空则回退到 ApprovalCode
	EncryptKey        string // callback encryption key
	VerificationToken string // callback verification token
	BaseURL           string // default https://open.feishu.cn
	// UserIDType for create-instance: "open_id" | "user_id" | "union_id".
	UserIDType string
	// FormWidgets maps logical fields onto the approval definition. Empty uses
	// DefaultFormWidgets; production should load the ids Feishu assigned.
	FormWidgets []FormWidget
}

func (c LiveConfig) approvalCodeFor(dir string) string {
	if dir == "import" && c.ApprovalCodeImport != "" {
		return c.ApprovalCodeImport
	}
	return c.ApprovalCode
}

// NewLiveClient builds a LiveClient. applicantResolver maps an internal
// employee id to a Feishu open_id (deployment-specific); if nil, ApplicantID is
// passed through as the configured UserIDType value.
func NewLiveClient(cfg LiveConfig, applicantResolver func(string) (string, error)) *LiveClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://open.feishu.cn"
	}
	if cfg.UserIDType == "" {
		cfg.UserIDType = "open_id"
	}
	return &LiveClient{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}, resolver: applicantResolver}
}

func (c *LiveClient) tenantToken(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id": c.cfg.AppID, "app_secret": c.cfg.AppSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/open-apis/auth/v3/tenant_access_token/internal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.TenantAccessToken == "" {
		return "", fmt.Errorf("feishu: tenant token error %d %s", out.Code, out.Msg)
	}
	return out.TenantAccessToken, nil
}

func (c *LiveClient) CreateApprovalInstance(ctx context.Context, r CreateInstanceRequest) (*Instance, error) {
	tok, err := c.tenantToken(ctx)
	if err != nil {
		return nil, err
	}
	uid := r.ApplicantID
	if c.resolver != nil {
		if uid, err = c.resolver(r.ApplicantID); err != nil {
			return nil, err
		}
	}
	formJSON, err := BuildApprovalForm(c.cfg.FormWidgets, r)
	if err != nil {
		return nil, err
	}
	code := c.cfg.approvalCodeFor(r.Direction)
	if code == "" {
		return nil, fmt.Errorf("feishu: 未配置审批定义 code（direction=%s）", r.Direction)
	}
	payload := map[string]any{
		"approval_code":  code,
		c.cfg.UserIDType: uid,
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
		return nil, fmt.Errorf("feishu: create instance error %d %s", out.Code, out.Msg)
	}
	return &Instance{
		ID: out.Data.InstanceCode, ReqID: r.ReqID, Status: StatusPending, CreatedAt: time.Now(),
	}, nil
}

func (c *LiveClient) GetApprovalStatus(ctx context.Context, instanceID string) (*Instance, error) {
	tok, err := c.tenantToken(ctx)
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
			Status   string `json:"status"`
			Approval string `json:"approval_code"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("feishu: get instance error %d %s", out.Code, out.Msg)
	}
	return &Instance{ID: instanceID, Status: normalizeStatus(out.Data.Status)}, nil
}

// VerifyCallback decrypts and token-verifies a raw callback body. Source-IP
// allowlisting and mTLS are enforced at the HTTP layer (see internal/gateway).
func (c *LiveClient) VerifyCallback(_ context.Context, body []byte) (*ApprovalEvent, error) {
	ev, challenge, err := VerifyCallbackBody(body, c.cfg.EncryptKey, c.cfg.VerificationToken)
	if err != nil {
		return nil, err
	}
	if challenge != "" {
		// URL-verification handshake; caller must echo the challenge.
		return &ApprovalEvent{Type: "url_verification", InstanceID: challenge}, nil
	}
	return ev, nil
}

var _ Client = (*LiveClient)(nil)
var _ Client = (*MockClient)(nil)
