package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// UserInfo is the Feishu identity of the person who opened the H5 and authorized.
type UserInfo struct {
	OpenID  string
	UserID  string
	UnionID string
	Name    string
}

// IdentityFromCode exchanges a Feishu H5 auth code for the logged-in user.
// LiveClient implements this; MockClient does not (offline demos keep QR#1 applicant).
type IdentityFromCode interface {
	UserFromAuthCode(ctx context.Context, code string) (*UserInfo, error)
	// ApplicantIDFor returns the id field CreateApprovalInstance should use
	// for this user (open_id / user_id / union_id per tenant config).
	ApplicantIDFor(u *UserInfo) string
}

func (c *LiveClient) appAccessToken(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"app_id": c.cfg.AppID, "app_secret": c.cfg.AppSecret,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/open-apis/auth/v3/app_access_token/internal", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code           int    `json:"code"`
		Msg            string `json:"msg"`
		AppAccessToken string `json:"app_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.AppAccessToken == "" {
		return "", fmt.Errorf("feishu: app token error %d %s", out.Code, out.Msg)
	}
	return out.AppAccessToken, nil
}

// UserFromAuthCode turns a tt.requestAuthCode result into Feishu user identity.
func (c *LiveClient) UserFromAuthCode(ctx context.Context, code string) (*UserInfo, error) {
	if code == "" {
		return nil, fmt.Errorf("feishu: empty auth code")
	}
	appTok, err := c.appAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{
		"app_access_token": appTok,
		"grant_type":       "authorization_code",
		"code":             code,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/open-apis/authen/v1/access_token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID  string `json:"open_id"`
			UserID  string `json:"user_id"`
			UnionID string `json:"union_id"`
			Name    string `json:"name"`
			EnName  string `json:"en_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("feishu: auth code exchange error %d %s", out.Code, out.Msg)
	}
	name := out.Data.Name
	if name == "" {
		name = out.Data.EnName
	}
	u := &UserInfo{
		OpenID: out.Data.OpenID, UserID: out.Data.UserID,
		UnionID: out.Data.UnionID, Name: name,
	}
	if c.ApplicantIDFor(u) == "" {
		return nil, fmt.Errorf("feishu: auth response missing %s", c.cfg.UserIDType)
	}
	return u, nil
}

func (c *LiveClient) ApplicantIDFor(u *UserInfo) string {
	if u == nil {
		return ""
	}
	switch c.cfg.UserIDType {
	case "user_id":
		return u.UserID
	case "union_id":
		return u.UnionID
	default:
		return u.OpenID
	}
}

var _ IdentityFromCode = (*LiveClient)(nil)
