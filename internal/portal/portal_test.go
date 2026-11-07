package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/hsm"
	"fdt/internal/identity"
	"fdt/internal/manifest"
	"fdt/internal/store"
)

func TestCheckSourceRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "a/b")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Service{SourceRoot: root}
	if err := s.checkSourceRoot(inside); err != nil {
		t.Fatalf("inside root should pass: %v", err)
	}
	if err := s.checkSourceRoot(filepath.Join(root, "..", "etc")); err != ErrSourceOutsideRoot {
		t.Fatalf("outside root should be rejected, got %v", err)
	}
	if err := s.checkSourceRoot("/etc"); err != ErrSourceOutsideRoot {
		t.Fatalf("absolute outside should be rejected, got %v", err)
	}
	// Empty SourceRoot = unrestricted (dev).
	s2 := &Service{}
	if err := s2.checkSourceRoot("/anywhere"); err != nil {
		t.Fatalf("empty root should allow (dev), got %v", err)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	kp, _ := crypto.GenerateKeyPair()
	return &Service{
		Store: st, Signer: hsm.NewSoftwareSigner("int", kp.Priv),
		DLP: manifest.NoopDLP{}, StagingRoot: t.TempDir(), QRTTL: time.Minute,
	}
}

func TestHandlerRequiresAuthWhenIdentitySet(t *testing.T) {
	svc := newTestService(t)
	h := &Handler{Svc: svc, Identity: identity.NewMockProvider()}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	// /me without creds -> 401
	resp, err := http.Get(srv.URL + "/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me no-creds = %d, want 401", resp.StatusCode)
	}

	// /me with creds -> identity
	req, _ := http.NewRequest("GET", srv.URL+"/me", nil)
	req.SetBasicAuth("zhangsan", "pass123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/me with creds = %d", resp.StatusCode)
	}
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if me["authenticated"] != true || me["name"] != "张三" {
		t.Fatalf("bad /me: %v", me)
	}
}

func TestAdminChangePassword(t *testing.T) {
	svc := newTestService(t)
	envFile := filepath.Join(t.TempDir(), "fdt.env")
	if err := os.WriteFile(envFile, []byte("PORTAL_ADMIN_PASSWORD=oldpass12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		Svc: svc, Identity: identity.NewMockProvider(),
		AdminUser: "admin", AdminPass: "oldpass12",
		Sessions: NewSessions(), EnvFile: envFile,
	}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	loginBody, _ := json.Marshal(map[string]string{"user": "admin", "password": "oldpass12"})
	resp, err := http.Post(srv.URL+"/api/admin/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	cookie := resp.Header.Get("Set-Cookie")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || cookie == "" {
		t.Fatalf("login = %d", resp.StatusCode)
	}

	chg, _ := json.Marshal(map[string]string{"oldPassword": "oldpass12", "newPassword": "newpass99"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/admin/password", bytes.NewReader(chg))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change password = %d", resp.StatusCode)
	}
	b, _ := os.ReadFile(envFile)
	if !strings.Contains(string(b), "PORTAL_ADMIN_PASSWORD=newpass99") {
		t.Fatalf("env not updated: %s", b)
	}

	bad, _ := json.Marshal(map[string]string{"user": "admin", "password": "oldpass12"})
	resp, _ = http.Post(srv.URL+"/api/admin/login", "application/json", bytes.NewReader(bad))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password should fail: %d", resp.StatusCode)
	}
	ok, _ := json.Marshal(map[string]string{"user": "admin", "password": "newpass99"})
	resp, _ = http.Post(srv.URL+"/api/admin/login", "application/json", bytes.NewReader(ok))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new password should work: %d", resp.StatusCode)
	}
}

func TestAdminLoginSeesAllHistory(t *testing.T) {
	svc := newTestService(t)
	h := &Handler{
		Svc: svc, Identity: identity.NewMockProvider(),
		AdminUser: "admin", AdminPass: "secret-admin",
		Sessions: NewSessions(),
	}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	// Ordinary user creates a request.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"reason": "r", "cls": "internal", "sourceDir": src})
	req, _ := http.NewRequest("POST", srv.URL+"/requests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("zhangsan", "pass123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d", resp.StatusCode)
	}

	// Admin login via local credentials.
	loginBody, _ := json.Marshal(map[string]string{"user": "admin", "password": "secret-admin"})
	resp, err = http.Post(srv.URL+"/api/admin/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login = %d", resp.StatusCode)
	}
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if me["canManageSettings"] != true || me["isLocalAdmin"] != true {
		t.Fatalf("bad admin me: %v", me)
	}
	cookie := resp.Header.Get("Set-Cookie")
	if cookie == "" {
		t.Fatal("missing session cookie")
	}

	listReq, _ := http.NewRequest("GET", srv.URL+"/requests", nil)
	listReq.Header.Set("Cookie", strings.Split(cookie, ";")[0])
	listReq.Header.Set("Accept", "application/json")
	listReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	listResp, err := http.DefaultClient.Do(listReq)
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list = %d", listResp.StatusCode)
	}
	var listed map[string]any
	_ = json.NewDecoder(listResp.Body).Decode(&listed)
	scope, _ := listed["scope"].(map[string]any)
	if scope["all"] != true {
		t.Fatalf("admin should see all: %v", listed)
	}
	items, _ := listed["items"].([]any)
	if len(items) < 1 {
		t.Fatalf("expected history items, got %v", listed)
	}

	bad, _ := json.Marshal(map[string]string{"user": "admin", "password": "wrong"})
	badResp, err := http.Post(srv.URL+"/api/admin/login", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password = %d", badResp.StatusCode)
	}
}

func TestCreateRequestUsesAuthenticatedIdentity(t *testing.T) {
	svc := newTestService(t)
	h := &Handler{Svc: svc, Identity: identity.NewMockProvider()}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	// A source dir to ferry.
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		// Client tries to spoof the applicant; server must ignore it.
		"applicant": map[string]string{"id": "hacker", "name": "假名字", "dept": "黑户"},
		"reason":    "test", "cls": "internal", "sourceDir": src,
	})
	req, _ := http.NewRequest("POST", srv.URL+"/requests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("zhangsan", "pass123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d", resp.StatusCode)
	}
	var out CreateResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	// The persisted request must carry the AD identity, not the spoofed body.
	r, err := svc.Store.GetRequest(context.Background(), out.ReqID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ApplicantName != "张三" || r.ApplicantID != "g976f94f" || r.Dept != "设计部" {
		t.Fatalf("applicant not taken from AD: %+v", r)
	}
	if out.QR1Frame == "" || out.Count != 1 {
		t.Fatalf("bad create result: %+v", out)
	}
}

func TestCreateRequestRequiresFeishuBinding(t *testing.T) {
	svc := newTestService(t)
	bindings := &FeishuBindings{Path: filepath.Join(t.TempDir(), "feishu_bindings.json")}
	h := &Handler{Svc: svc, Identity: identity.NewMockProvider(), Bindings: bindings}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"reason": "r", "cls": "internal", "sourceDir": src})
	req, _ := http.NewRequest("POST", srv.URL+"/requests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("zhangsan", "pass123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unbound create = %d %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "飞书") {
		t.Fatalf("expected feishu bind error, got %s", raw)
	}

	if err := bindings.Set("zhangsan", "ou_bind_test"); err != nil {
		t.Fatal(err)
	}
	req2, _ := http.NewRequest("POST", srv.URL+"/requests", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.SetBasicAuth("zhangsan", "pass123")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("bound create = %d", resp2.StatusCode)
	}
	var out CreateResult
	if err := json.NewDecoder(resp2.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Store.GetRequest(context.Background(), out.ReqID)
	if err != nil {
		t.Fatal(err)
	}
	if r.ApplicantID != "ou_bind_test" {
		t.Fatalf("applicant id = %q, want bound feishu id", r.ApplicantID)
	}
}
