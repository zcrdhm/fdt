package gateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/feishu"
	"fdt/internal/hsm"
)

// TenantsFile is the on-disk multi-tenant gateway config (see config/tenants.example.json).
type TenantsFile struct {
	ReleaseTTL     string             `json:"releaseTTL"`
	JSAPIAppID     string             `json:"jsapiAppId,omitempty"`
	JSAPIAppSecret string             `json:"jsapiAppSecret,omitempty"`
	Tenants        []TenantFileEntry  `json:"tenants"`
}

// TenantFileEntry is one customer site on a shared gateway.
type TenantFileEntry struct {
	ID          string            `json:"id"`
	InternalPub string            `json:"internalPub"`
	GatewayKey  string            `json:"gatewayKey"`
	Feishu      TenantFeishuFile  `json:"feishu"`
}

// TenantFeishuFile is per-tenant approval-channel credentials (or mock).
// Historically Feishu-only; Provider selects 飞书 / 钉钉 / 企业微信.
type TenantFeishuFile struct {
	Mode     string `json:"mode"`
	Provider string `json:"provider,omitempty"` // feishu | dingtalk | wecom；空=feishu
	AutoApprove       bool   `json:"autoApprove"`
	Approver          string `json:"approver"`
	AppID             string `json:"appId"`
	AppSecret         string `json:"appSecret"`
	ApprovalCode       string `json:"approvalCode"`             // 导出审批定义
	ApprovalCodeImport string `json:"approvalCodeImport,omitempty"` // 导入审批定义；空则沿用 approvalCode
	EncryptKey         string `json:"encryptKey"`
	VerificationToken  string `json:"verificationToken"`
	FeishuTenantKey    string `json:"feishuTenantKey"`
	FormFile           string `json:"formFile"`
	// UserIDType: open_id (default) | user_id | union_id — must match Applicant.ID in QR#1.
	UserIDType string `json:"userIdType,omitempty"`
}

// NormalizedProvider returns feishu | dingtalk | wecom.
func (c TenantFeishuFile) NormalizedProvider() string {
	p := strings.ToLower(strings.TrimSpace(c.Provider))
	switch p {
	case "", "feishu", "lark":
		return "feishu"
	case "ding", "dingtalk", "dingding":
		return "dingtalk"
	case "wecom", "wechat", "wework", "wxwork", "weixin":
		return "wecom"
	default:
		return p
	}
}

// ProviderLabel is a short Chinese name for UI / errors.
func (c TenantFeishuFile) ProviderLabel() string {
	switch c.NormalizedProvider() {
	case "dingtalk":
		return "钉钉"
	case "wecom":
		return "企业微信"
	default:
		return "飞书"
	}
}

// ProviderSupported is true when live approval can actually be created.
// Only Feishu is implemented today; others may be saved for future wiring.
func (c TenantFeishuFile) ProviderSupported() bool {
	return c.NormalizedProvider() == "feishu"
}

// LoadResult is a multi-tenant Service plus optional H5 JSAPI signer.
type LoadResult struct {
	Service *Service
	JSAPI   feishu.JSAPISigner
	File    TenantsFile
}

// LoadTenantsFile builds a multi-tenant Service from JSON.
// JSAPI prefers top-level jsapiAppId/jsapiAppSecret (shared web app);
// otherwise the first live tenant's LiveClient is used.
func LoadTenantsFile(path string) (*LoadResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf TenantsFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	ttl := 72 * time.Hour
	if tf.ReleaseTTL != "" {
		if d, err := time.ParseDuration(tf.ReleaseTTL); err == nil {
			ttl = d
		}
	}
	svc := New(ttl)
	var firstLive *feishu.LiveClient
	for _, t := range tf.Tenants {
		if t.ID == "" {
			return nil, fmt.Errorf("gateway: tenant entry missing id")
		}
		pub, err := crypto.ReadPublicPEM(t.InternalPub)
		if err != nil {
			return nil, fmt.Errorf("tenant %s: %w", t.ID, err)
		}
		signer, err := hsm.NewSoftwareSignerFromFile("gw-"+t.ID, t.GatewayKey)
		if err != nil {
			return nil, fmt.Errorf("tenant %s: %w", t.ID, err)
		}
		client, live, err := buildTenantFeishu(t.Feishu)
		if err != nil {
			return nil, fmt.Errorf("tenant %s: %w", t.ID, err)
		}
		if firstLive == nil && live != nil {
			firstLive = live
		}
		if err := svc.AddTenant(t.ID, TenantConfig{
			InternalPub: pub, Signer: signer, Feishu: client, FeishuTenantKey: t.Feishu.FeishuTenantKey,
		}); err != nil {
			return nil, fmt.Errorf("tenant %s: %w", t.ID, err)
		}
	}
	if len(svc.Tenants()) == 0 {
		return nil, fmt.Errorf("gateway: no tenants configured in %s", path)
	}
	jsapi := feishu.JSAPISigner(feishu.MockJSAPI{})
	if tf.JSAPIAppID != "" && tf.JSAPIAppSecret != "" {
		jsapi = feishu.NewLiveClient(feishu.LiveConfig{
			AppID: tf.JSAPIAppID, AppSecret: tf.JSAPIAppSecret, ApprovalCode: "unused",
		}, nil)
	} else if firstLive != nil {
		jsapi = firstLive
	}
	return &LoadResult{Service: svc, JSAPI: jsapi, File: tf}, nil
}

func buildTenantFeishu(c TenantFeishuFile) (feishu.Client, *feishu.LiveClient, error) {
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
		cfg := feishu.LiveConfig{
			AppID: c.AppID, AppSecret: c.AppSecret, ApprovalCode: c.ApprovalCode,
			ApprovalCodeImport: c.ApprovalCodeImport,
			EncryptKey: c.EncryptKey, VerificationToken: c.VerificationToken,
			UserIDType: c.UserIDType,
		}
		if c.FormFile != "" {
			ws, err := feishu.LoadFormWidgets(c.FormFile)
			if err != nil {
				return nil, nil, err
			}
			cfg.FormWidgets = ws
		}
		live := feishu.NewLiveClient(cfg, nil)
		return live, live, nil
	default:
		return nil, nil, fmt.Errorf("未知 feishu.mode %q（mock|live）", c.Mode)
	}
}
