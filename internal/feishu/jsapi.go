package feishu

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// JSAPIConfig is what the H5 page passes to window.h5sdk.config.
type JSAPIConfig struct {
	AppID     string `json:"appId"`
	Timestamp int64  `json:"timestamp"` // milliseconds
	NonceStr  string `json:"nonceStr"`
	Signature string `json:"signature"`
	Mock      bool   `json:"mock,omitempty"`
}

// JSAPISigner issues Feishu H5 JSAPI auth parameters for a page URL.
type JSAPISigner interface {
	SignJSAPI(ctx context.Context, pageURL string) (*JSAPIConfig, error)
}

// ticketCache holds a cached jsapi_ticket for LiveClient.
type ticketCache struct {
	mu     sync.Mutex
	ticket string
	exp    time.Time
}

// SignJSAPI implements JSAPISigner for LiveClient.
// verifyStr = jsapi_ticket=…&noncestr=…&timestamp=…&url=… then sha1 hex.
func (c *LiveClient) SignJSAPI(ctx context.Context, pageURL string) (*JSAPIConfig, error) {
	if pageURL == "" {
		return nil, fmt.Errorf("feishu: jsapi url required")
	}
	ticket, err := c.jsapiTicket(ctx)
	if err != nil {
		return nil, err
	}
	nonce, err := randomNonce(16)
	if err != nil {
		return nil, err
	}
	ts := time.Now().UnixMilli()
	verify := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", ticket, nonce, ts, pageURL)
	sum := sha1.Sum([]byte(verify))
	return &JSAPIConfig{
		AppID:     c.cfg.AppID,
		Timestamp: ts,
		NonceStr:  nonce,
		Signature: hex.EncodeToString(sum[:]),
	}, nil
}

func (c *LiveClient) jsapiTicket(ctx context.Context) (string, error) {
	c.ticket.mu.Lock()
	defer c.ticket.mu.Unlock()
	if c.ticket.ticket != "" && time.Now().Before(c.ticket.exp) {
		return c.ticket.ticket, nil
	}
	tok, err := c.tenantToken(ctx)
	if err != nil {
		return "", err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		c.cfg.BaseURL+"/open-apis/jssdk/ticket/get", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Ticket   string `json:"ticket"`
			ExpireIn int    `json:"expire_in"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Code != 0 || out.Data.Ticket == "" {
		return "", fmt.Errorf("feishu: jsapi ticket error %d %s", out.Code, out.Msg)
	}
	ttl := time.Duration(out.Data.ExpireIn) * time.Second
	if ttl <= 0 {
		ttl = 90 * time.Minute
	}
	if ttl > 30*time.Minute {
		ttl -= 10 * time.Minute
	}
	c.ticket.ticket = out.Data.Ticket
	c.ticket.exp = time.Now().Add(ttl)
	return c.ticket.ticket, nil
}

func randomNonce(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// MockJSAPI reports mock mode so the H5 falls back to paste during offline demos.
type MockJSAPI struct{}

func (MockJSAPI) SignJSAPI(context.Context, string) (*JSAPIConfig, error) {
	return &JSAPIConfig{Mock: true, AppID: "mock"}, nil
}

var _ JSAPISigner = (*LiveClient)(nil)
var _ JSAPISigner = MockJSAPI{}
