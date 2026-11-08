package portal

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/buildinfo"
	"fdt/internal/identity"
	"fdt/internal/model"
	"fdt/internal/store"
	"fdt/internal/webui"
)

// Handler exposes the portal over HTTP and serves its own frontend at "/".
//
// Auth: cookie session (UI login/logout) or HTTP Basic (API/tools). When
// Identity is set, authenticated user becomes the applicant; FeishuBindings may
// override Applicant.ID with a bound Feishu user_id/open_id.
type Handler struct {
	Svc        *Service
	WebDir     string
	Identity   identity.Provider
	Realm      string
	Sessions   *Sessions
	Bindings   *FeishuBindings
	LDAPFile       string
	FormFieldsFile string // /etc/fdt/form-fields.json
	EnvFile        string // optional: update IDENTITY=ldap when enabling LDAP
	AdminUser      string // PORTAL_ADMIN_USER; default "admin"
	AdminPass      string // PORTAL_ADMIN_PASSWORD; local admin page login
	AdminUsers     map[string]bool // PORTAL_ADMIN_USERS directory accounts (optional auto admin)
	IdentityMode   string
	PAMService     string // PAM_SERVICE; default password-auth
	ReleasedRoot   string // import release dir (DATA_DIR/released); download endpoint roots here
	mu             sync.Mutex

	// MaxUploadBytes bounds multipart uploads; 0 => 8 GiB.
	MaxUploadBytes int64
}

type ctxKey struct{}

func withUser(ctx context.Context, u *identity.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

// UserFromContext returns the authenticated directory user, if any.
func UserFromContext(ctx context.Context) (*identity.User, bool) {
	u, ok := ctx.Value(ctxKey{}).(*identity.User)
	return u, ok && u != nil
}

func (h *Handler) realm() string {
	if h.Realm == "" {
		return "FDT Portal"
	}
	return h.Realm
}

func (h *Handler) sessions() *Sessions {
	if h.Sessions == nil {
		h.Sessions = NewSessions()
	}
	return h.Sessions
}

func (h *Handler) provider() identity.Provider {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.Identity
}

func (h *Handler) setProvider(p identity.Provider, mode string) {
	h.mu.Lock()
	h.Identity = p
	if mode != "" {
		h.IdentityMode = mode
	}
	h.mu.Unlock()
}

func (h *Handler) resolveUser(r *http.Request) *identity.User {
	idp := h.provider()
	if idp == nil {
		return nil
	}
	if tok := h.sessions().TokenFromRequest(r); tok != "" {
		if u := h.sessions().User(tok); u != nil {
			return u
		}
	}
	account, pass, ok := r.BasicAuth()
	if !ok {
		return nil
	}
	u, err := idp.Authenticate(r.Context(), account, pass)
	if err != nil {
		return nil
	}
	return u
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.provider() == nil {
			next(w, r)
			return
		}
		u := h.resolveUser(r)
		if u == nil {
			// Prefer JSON 401 for XHR; Basic challenge only when browser asks.
			if r.Header.Get("X-Requested-With") == "XMLHttpRequest" ||
				strings.Contains(r.Header.Get("Accept"), "application/json") ||
				r.Header.Get("Content-Type") == "application/json" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="`+h.realm()+`"`)
			http.Error(w, "需要登录", http.StatusUnauthorized)
			return
		}
		next(w, r.WithContext(withUser(r.Context(), u)))
	}
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.logout)
	mux.HandleFunc("GET /api/me", h.meAPI)
	mux.HandleFunc("POST /api/admin/login", h.adminLogin)
	mux.Handle("POST /api/admin/password", h.auth(h.changeAdminPassword))
	mux.Handle("POST /api/admin/unlock", h.auth(h.unlockAdmin)) // legacy; prefer /admin/
	mux.Handle("POST /api/admin/lock", h.auth(h.lockAdmin))
	mux.Handle("POST /api/feishu-id", h.auth(h.bindFeishuID))
	mux.Handle("GET /api/settings/ldap", h.auth(h.getLDAP))
	mux.Handle("POST /api/settings/ldap", h.auth(h.saveLDAP))
	mux.Handle("POST /api/settings/ldap/test", h.auth(h.testLDAP))
	mux.Handle("GET /api/form-fields", h.auth(h.getFormFieldsPublic))
	mux.Handle("GET /api/settings/form-fields", h.auth(h.getFormFieldsAdmin))
	mux.Handle("POST /api/settings/form-fields", h.auth(h.saveFormFields))
	mux.Handle("GET /api/settings/form-fields/feishu-export", h.auth(h.exportFeishuForm))

	mux.Handle("GET /me", h.auth(h.me))
	mux.Handle("GET /requests", h.auth(h.listRequests))
	mux.Handle("POST /requests", h.auth(h.createRequest))
	mux.Handle("GET /requests/{id}", h.auth(h.getRequest))
	mux.Handle("GET /requests/{id}/qr1", h.auth(h.getQR1))
	mux.Handle("GET /requests/{id}/qr1.png", h.auth(h.getQR1PNG))
	mux.Handle("GET /requests/{id}/download", h.auth(h.downloadReleased))

	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("GET /admin/", func(w http.ResponseWriter, r *http.Request) { serveUI(w, "admin.html") })
	mux.HandleFunc("GET /admin.js", func(w http.ResponseWriter, r *http.Request) { serveUI(w, "admin.js") })

	if h.WebDir != "" {
		mux.Handle("GET /", http.FileServer(http.Dir(h.WebDir)))
	} else {
		mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { serveUI(w, "index.html") })
		mux.HandleFunc("GET /app.css", func(w http.ResponseWriter, r *http.Request) { serveUI(w, "app.css") })
		mux.HandleFunc("GET /app.js", func(w http.ResponseWriter, r *http.Request) { serveUI(w, "app.js") })
		mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
			serveUI(w, strings.TrimPrefix(r.PathValue("path"), "/"))
		})
	}
	return mux
}

func (h *Handler) adminUserName() string {
	if strings.TrimSpace(h.AdminUser) == "" {
		return "admin"
	}
	return strings.TrimSpace(h.AdminUser)
}

func (h *Handler) adminPass() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.AdminPass
}

func (h *Handler) setAdminPass(pass string) {
	h.mu.Lock()
	h.AdminPass = pass
	h.mu.Unlock()
}

func (h *Handler) adminLogin(w http.ResponseWriter, r *http.Request) {
	cur := h.adminPass()
	if cur == "" {
		http.Error(w, "未配置 PORTAL_ADMIN_PASSWORD，无法使用管理后台", http.StatusForbidden)
		return
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(body.User)
	if !strings.EqualFold(user, h.adminUserName()) || body.Password != cur {
		http.Error(w, "管理员用户名或密码错误", http.StatusUnauthorized)
		return
	}
	u := &identity.User{
		Account: h.adminUserName(),
		ID:      "portal-admin",
		Name:    "本地管理员",
		Dept:    "系统",
	}
	tok, err := h.sessions().Create(u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.sessions().SetAdmin(tok, true)
	h.sessions().SetCookie(w, tok)
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r.WithContext(withUser(r.Context(), u))))
}

func (h *Handler) changeAdminPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	cur := h.adminPass()
	if cur == "" {
		http.Error(w, "未配置 PORTAL_ADMIN_PASSWORD", http.StatusForbidden)
		return
	}
	if body.OldPassword != cur {
		http.Error(w, "旧密码不正确", http.StatusUnauthorized)
		return
	}
	if len(body.NewPassword) < 8 {
		http.Error(w, "新密码至少 8 位", http.StatusBadRequest)
		return
	}
	if body.NewPassword == body.OldPassword {
		http.Error(w, "新密码不能与旧密码相同", http.StatusBadRequest)
		return
	}
	h.setAdminPass(body.NewPassword)
	if h.EnvFile != "" {
		if err := setEnvValue(h.EnvFile, "PORTAL_ADMIN_PASSWORD", body.NewPassword); err != nil {
			// Roll back memory so login stays consistent with file.
			h.setAdminPass(cur)
			http.Error(w, "写入 fdt.env 失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "message": "密码已修改并写入配置，重启后仍有效。",
	})
}

func serveUI(w http.ResponseWriter, name string) {
	if name == "" || strings.Contains(name, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	b, err := fs.ReadFile(webui.FS, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch {
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(name, ".ico"):
		w.Header().Set("Content-Type", "image/x-icon")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	idp := h.provider()
	if idp == nil {
		http.Error(w, "当前未启用身份认证（IDENTITY=none）", http.StatusBadRequest)
		return
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	u, err := idp.Authenticate(r.Context(), body.User, body.Password)
	if err != nil {
		http.Error(w, "用户名或密码错误", http.StatusUnauthorized)
		return
	}
	tok, err := h.sessions().Create(u)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if h.isListedAdmin(u) {
		_ = h.sessions().SetAdmin(tok, true)
	}
	h.sessions().SetCookie(w, tok)
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r.WithContext(withUser(r.Context(), u))))
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.sessions().Logout(h.sessions().TokenFromRequest(r))
	h.sessions().ClearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) meAPI(w http.ResponseWriter, r *http.Request) {
	u := h.resolveUser(r)
	if u == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated":   false,
			"identityMode":    h.IdentityMode,
			"authRequired":    h.provider() != nil,
			"adminConfigured": h.adminPass() != "" || len(h.AdminUsers) > 0,
			"adminUser":       h.adminUserName(),
			"version":         buildinfo.Version,
			"copyright":       buildinfo.Copyright,
		})
		return
	}
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r))
}

func (h *Handler) unlockAdmin(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "请先登录", http.StatusUnauthorized)
		return
	}
	if h.adminPass() == "" {
		http.Error(w, "未配置 PORTAL_ADMIN_PASSWORD，无法在线管理 LDAP（请改 fdt.env 或编辑 ldap.json）", http.StatusForbidden)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Password != h.adminPass() {
		http.Error(w, "管理员密码错误", http.StatusForbidden)
		return
	}
	tok := h.sessions().TokenFromRequest(r)
	if !h.sessions().SetAdmin(tok, true) {
		http.Error(w, "会话无效，请重新登录", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r))
}

func (h *Handler) lockAdmin(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "请先登录", http.StatusUnauthorized)
		return
	}
	_ = h.sessions().SetAdmin(h.sessions().TokenFromRequest(r), false)
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r))
}

func (h *Handler) mePayload(u *identity.User) map[string]any {
	return h.mePayloadWithReq(u, nil)
}

func (h *Handler) mePayloadWithReq(u *identity.User, r *http.Request) map[string]any {
	feishuID := ""
	if h.Bindings != nil {
		feishuID = h.Bindings.Get(u.Account)
	}
	effective := u.ID
	if feishuID != "" {
		effective = feishuID
	}
	localAdmin := u.ID == "portal-admin"
	canManage := localAdmin || h.isListedAdmin(u)
	if !canManage && r != nil {
		canManage = h.canManageSettings(r, u)
	}
	return map[string]any{
		"authenticated":     true,
		"account":           u.Account,
		"id":                u.ID,
		"name":              u.Name,
		"dept":              u.Dept,
		"mail":              u.Mail,
		"title":             u.Title,
		"feishuId":          feishuID,
		"applicantId":       effective,
		"identityMode":      h.IdentityMode,
		"authRequired":      true,
		"canManageSettings": canManage,
		"isLocalAdmin":      localAdmin,
		"adminConfigured":   h.adminPass() != "" || len(h.AdminUsers) > 0,
		"adminUser":         h.adminUserName(),
		"version":           buildinfo.Version,
		"copyright":         buildinfo.Copyright,
	}
}

func (h *Handler) isListedAdmin(u *identity.User) bool {
	if u == nil || len(h.AdminUsers) == 0 {
		return false
	}
	return h.AdminUsers[strings.ToLower(u.Account)]
}

func (h *Handler) canManageSettings(r *http.Request, u *identity.User) bool {
	if u == nil {
		u = h.resolveUser(r)
	}
	if u == nil {
		return false
	}
	if h.isListedAdmin(u) {
		return true
	}
	if h.sessions().IsAdmin(h.sessions().TokenFromRequest(r)) {
		return true
	}
	if pass := h.adminPass(); pass != "" && r.Header.Get("X-Portal-Admin") == pass {
		return true
	}
	return false
}

func (h *Handler) bindFeishuID(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		FeishuID string `json:"feishuId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if h.Bindings == nil {
		http.Error(w, "bindings not configured", http.StatusServiceUnavailable)
		return
	}
	if err := h.Bindings.Set(u.Account, body.FeishuID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, h.mePayloadWithReq(u, r))
}

func (h *Handler) requireSettings(w http.ResponseWriter, r *http.Request) bool {
	u := h.resolveUser(r)
	if u == nil {
		http.Error(w, "请先登录", http.StatusUnauthorized)
		return false
	}
	if h.canManageSettings(r, u) {
		return true
	}
	if h.adminPass() == "" && len(h.AdminUsers) == 0 {
		http.Error(w, "LDAP 配置仅管理员可访问：请在 fdt.env 设置 PORTAL_ADMIN_PASSWORD 或 PORTAL_ADMIN_USERS", http.StatusForbidden)
		return false
	}
	http.Error(w, "需要管理员权限（导航栏「管理解锁」或账号列入 PORTAL_ADMIN_USERS）", http.StatusForbidden)
	return false
}

func (h *Handler) getLDAP(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	cfg, _ := LoadLDAPFile(h.LDAPFile)
	if cfg.URL == "" && h.EnvFile != "" {
		// soft merge display from env if file empty — skip, UI starts blank
	}
	pamService := strings.TrimSpace(h.PAMService)
	if pamService == "" {
		pamService = identity.DefaultPAMService
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ldap": cfg.Public(), "identityMode": h.IdentityMode, "ldapFile": h.LDAPFile,
		"pam": map[string]any{
			"service":   pamService,
			"available": identity.PAMAvailable(),
		},
	})
}

func (h *Handler) saveLDAP(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	var body struct {
		LDAPFile
		IdentityMode string `json:"identityMode"`
		PAMService   string `json:"pamService"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	prev, _ := LoadLDAPFile(h.LDAPFile)
	if body.ServicePassword == "" {
		body.ServicePassword = prev.ServicePassword
	}
	mode := strings.ToLower(strings.TrimSpace(body.IdentityMode))
	switch mode {
	case "ldap", "none", "mock", "pam":
	default:
		// 兼容旧客户端：仅传 enabled 时，关闭 LDAP 默认 none（禁止悄悄回退 mock）
		if body.Enabled {
			mode = "ldap"
		} else {
			mode = "none"
		}
	}
	body.Enabled = mode == "ldap"
	pamService := strings.TrimSpace(body.PAMService)
	if pamService == "" {
		pamService = identity.DefaultPAMService
	}
	if h.LDAPFile == "" {
		h.LDAPFile = "/etc/fdt/ldap.json"
	}
	if err := SaveLDAPFile(h.LDAPFile, body.LDAPFile); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	switch mode {
	case "ldap":
		p, err := identity.NewLDAPProvider(body.ToIdentity())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.setProvider(p, "ldap")
	case "pam":
		p, err := identity.NewPAMProvider(identity.PAMConfig{Service: pamService})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.PAMService = pamService
		h.setProvider(p, "pam")
	case "none":
		h.setProvider(nil, "none")
	default: // mock — 仅联调
		h.setProvider(identity.NewMockProvider(), "mock")
	}
	if h.EnvFile != "" {
		_ = setEnvValue(h.EnvFile, "IDENTITY", mode)
		if mode == "pam" {
			_ = setEnvValue(h.EnvFile, "PAM_SERVICE", pamService)
		}
	}
	msg := "已保存。身份源已热切换为 " + mode + "；建议确认登录后再重启 Portal。"
	switch mode {
	case "mock":
		msg = "已保存为 mock（含弱口令样例账号，禁止生产）。请尽快改回 ldap / pam / none。"
	case "pam":
		msg = "已保存为本机 PAM（服务 " + pamService + "）。请用系统账号测试登录。"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "identityMode": mode, "ldap": body.Public(),
		"pam": map[string]any{"service": pamService, "available": identity.PAMAvailable()},
		"message": msg,
	})
}

func (h *Handler) testLDAP(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	var body struct {
		LDAPFile
		IdentityMode string `json:"identityMode"`
		PAMService   string `json:"pamService"`
		Account      string `json:"account"`
		Password     string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(body.IdentityMode))
	if mode == "" {
		if body.Enabled {
			mode = "ldap"
		} else {
			mode = h.IdentityMode
		}
	}
	if mode == "pam" {
		svc := strings.TrimSpace(body.PAMService)
		if svc == "" {
			svc = h.PAMService
		}
		p, err := identity.NewPAMProvider(identity.PAMConfig{Service: svc})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Account == "" {
			if strings.TrimSpace(svc) == "" {
				svc = identity.DefaultPAMService
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "message": "PAM 可用（服务 " + svc + "；未做用户登录测试）",
			})
			return
		}
		u, err := p.Authenticate(r.Context(), body.Account, body.Password)
		if err != nil {
			http.Error(w, "测试失败: "+err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "message": "PAM 登录成功",
			"user": map[string]string{
				"account": u.Account, "id": u.ID, "name": u.Name, "dept": u.Dept, "mail": u.Mail,
			},
		})
		return
	}
	prev, _ := LoadLDAPFile(h.LDAPFile)
	if body.ServicePassword == "" {
		body.ServicePassword = prev.ServicePassword
	}
	p, err := identity.NewLDAPProvider(body.LDAPFile.ToIdentity())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Account == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "LDAP 配置可解析（未做用户登录测试）"})
		return
	}
	u, err := p.Authenticate(r.Context(), body.Account, body.Password)
	if err != nil {
		http.Error(w, "测试失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "user": map[string]string{
			"account": u.Account, "id": u.ID, "name": u.Name, "dept": u.Dept, "mail": u.Mail,
		},
	})
}

func setEnvValue(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(path, []byte(key+"="+value+"\n"), 0o600)
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		k, _, ok := strings.Cut(trim, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		lines[i] = key + "=" + value
		found = true
		break
	}
	if !found {
		if len(lines) == 0 || lines[len(lines)-1] != "" {
			lines = append(lines, key+"="+value)
		} else {
			lines = append(lines[:len(lines)-1], key+"="+value, "")
		}
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

type createReqBody struct {
	Applicant model.Applicant      `json:"applicant"`
	Reason    string               `json:"reason"`
	Cls       model.Classification `json:"cls"`
	SourceDir string               `json:"sourceDir"`
	Fields    map[string]string    `json:"fields"`
}

func (h *Handler) formFieldsPath() string {
	if h.FormFieldsFile != "" {
		return h.FormFieldsFile
	}
	return "/etc/fdt/form-fields.json"
}

func (h *Handler) loadFormFields() FormFieldsFile {
	f, err := LoadFormFields(h.formFieldsPath())
	if err != nil {
		return DefaultFormFields()
	}
	return f
}

func (h *Handler) getFormFieldsPublic(w http.ResponseWriter, r *http.Request) {
	f := h.loadFormFields()
	writeJSON(w, http.StatusOK, map[string]any{"fields": f.UserFields()})
}

func (h *Handler) getFormFieldsAdmin(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	f := h.loadFormFields()
	writeJSON(w, http.StatusOK, map[string]any{
		"form": f, "path": h.formFieldsPath(),
		"feishuExport": f.FeishuWidgetsExport(),
	})
}

func (h *Handler) saveFormFields(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	var body FormFieldsFile
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := SaveFormFields(h.formFieldsPath(), body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f := h.loadFormFields()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "form": f, "feishuExport": f.FeishuWidgetsExport(),
		"message": "已保存。用户 Portal 表单立即生效；请把「飞书表单导出」同步到网关 feishu-form.json，并把 id 改成飞书后台真实控件 id。",
	})
}

func (h *Handler) exportFeishuForm(w http.ResponseWriter, r *http.Request) {
	if !h.requireSettings(w, r) {
		return
	}
	f := h.loadFormFields()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="feishu-form.json"`)
	_ = json.NewEncoder(w).Encode(f.FeishuWidgetsExport())
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, h.mePayload(u))
}

func (h *Handler) createRequest(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	var (
		applicant model.Applicant
		sourceDir string
		staging   string
		values    map[string]string
		err       error
	)
	if strings.HasPrefix(ct, "multipart/form-data") {
		applicant, values, staging, err = h.readMultipart(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		var body createReqBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		applicant, sourceDir = body.Applicant, body.SourceDir
		values = map[string]string{}
		for k, v := range body.Fields {
			values[k] = v
		}
		if body.Reason != "" {
			values["reason"] = body.Reason
		}
		if body.Cls != "" {
			values["cls"] = string(body.Cls)
		}
	}
	ff := h.loadFormFields()
	reason, clsStr, extras, err := ff.NormalizeExtras(values)
	if err != nil {
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cls := model.Classification(clsStr)
	if cls == "" {
		cls = model.ClassInternal
	}
	if u, ok := UserFromContext(r.Context()); ok {
		applicant = model.Applicant{ID: u.ID, Name: u.Name, Dept: u.Dept}
		if h.Bindings != nil {
			fid := h.Bindings.Get(u.Account)
			if fid == "" {
				if staging != "" {
					_ = os.RemoveAll(staging)
				}
				http.Error(w, "尚未绑定飞书 ID，请先在「飞书 ID」页完成绑定后再发起导出", http.StatusBadRequest)
				return
			}
			applicant.ID = fid
		}
	}
	res, err := h.Svc.CreateExportRequest(r.Context(), CreateInput{
		Applicant: applicant, Reason: reason, Cls: cls, SourceDir: sourceDir, StagingDir: staging, Extras: extras,
	})
	if err != nil {
		if staging != "" {
			_ = os.RemoveAll(staging)
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if u, ok := UserFromContext(r.Context()); ok {
		_, _ = h.Svc.Store.AppendAudit(r.Context(), "portal:"+u.Account, "", res.ReqID, "request_created_auth",
			map[string]string{"account": u.Account, "dept": u.Dept, "applicantId": applicant.ID})
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) readMultipart(r *http.Request) (model.Applicant, map[string]string, string, error) {
	limit := h.MaxUploadBytes
	if limit <= 0 {
		limit = 8 << 30
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return model.Applicant{}, nil, "", err
	}
	values := map[string]string{}
	for k, vs := range r.MultipartForm.Value {
		if len(vs) > 0 {
			values[k] = vs[0]
		}
	}
	applicant := model.Applicant{
		ID: r.FormValue("applicantId"), Name: r.FormValue("applicantName"), Dept: r.FormValue("applicantDept"),
	}
	if h.Svc.StagingRoot == "" {
		return model.Applicant{}, nil, "", ErrNoStaging
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return model.Applicant{}, nil, "", err
	}
	staging := filepath.Join(h.Svc.StagingRoot, ".upload-"+hex.EncodeToString(id[:]))
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return model.Applicant{}, nil, "", err
	}
	var written int64
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		_ = os.RemoveAll(staging)
		return model.Applicant{}, nil, "", ErrEmptySrc
	}
	for _, fh := range files {
		n, err := saveUpload(staging, fh)
		if err != nil {
			_ = os.RemoveAll(staging)
			return model.Applicant{}, nil, "", err
		}
		written += n
		if written > limit {
			_ = os.RemoveAll(staging)
			return model.Applicant{}, nil, "", fmt.Errorf("portal: upload exceeds %d bytes", limit)
		}
	}
	return applicant, values, staging, nil
}

func saveUpload(root string, fh *multipart.FileHeader) (int64, error) {
	name := filepath.Base(fh.Filename)
	if name == "." || name == "" || strings.Contains(name, "..") {
		name = "upload.bin"
	}
	rel := strings.TrimPrefix(fh.Filename, "/")
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		rel = name
	}
	dest, ok := safeJoin(root, rel)
	if !ok {
		return 0, ErrSourceOutsideRoot
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	src, err := fh.Open()
	if err != nil {
		return 0, err
	}
	defer src.Close()
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	n, err := io.Copy(out, src)
	return n, err
}

func safeJoin(root, rel string) (string, bool) {
	target := filepath.Join(root, rel)
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", false
	}
	relOut, err := filepath.Rel(absRoot, absTarget)
	if err != nil || relOut == ".." || strings.HasPrefix(relOut, ".."+string(filepath.Separator)) {
		return "", false
	}
	return absTarget, true
}

func (h *Handler) applicantIDsFor(u *identity.User) []string {
	if u == nil {
		return nil
	}
	ids := []string{u.ID, u.Account}
	if h.Bindings != nil {
		if fid := h.Bindings.Get(u.Account); fid != "" {
			ids = append(ids, fid)
		}
	}
	return ids
}

func (h *Handler) canViewAllHistory(r *http.Request, u *identity.User) bool {
	if u == nil {
		u = h.resolveUser(r)
	}
	if u == nil {
		// IDENTITY=none：无认证环境可看全部（联调）。
		return h.provider() == nil
	}
	return h.canManageSettings(r, u)
}

func (h *Handler) canAccessRequest(r *http.Request, reqID string) bool {
	u, _ := UserFromContext(r.Context())
	if h.canViewAllHistory(r, u) {
		return true
	}
	if u == nil {
		return false
	}
	ok, err := h.Svc.Store.RequestOwnedBy(r.Context(), reqID, "portal:"+u.Account, h.applicantIDsFor(u))
	return err == nil && ok
}

func (h *Handler) listRequests(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFromContext(r.Context())
	all := h.canViewAllHistory(r, u)
	opt := store.ListRequestsOpt{All: all, Limit: 200}
	if !all {
		if u == nil {
			http.Error(w, "请先登录", http.StatusUnauthorized)
			return
		}
		opt.ApplicantIDs = h.applicantIDsFor(u)
		opt.PortalActor = "portal:" + u.Account
	}
	list, err := h.Svc.Store.ListRequestsOpt(r.Context(), opt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(list))
	for _, req := range list {
		ids = append(ids, req.ID)
	}
	fileNames, _ := h.Svc.Store.PhysicalFileNames(r.Context(), ids, 12)
	items := make([]map[string]any, 0, len(list))
	for _, req := range list {
		names := fileNames[req.ID]
		if names == nil {
			names = []string{}
		}
		items = append(items, map[string]any{
			"id":            req.ID,
			"dir":           req.Dir,
			"applicantId":   req.ApplicantID,
			"applicantName": req.ApplicantName,
			"dept":          req.Dept,
			"reason":        req.Reason,
			"cls":           req.Cls,
			"status":        req.Status,
			"fileCount":     req.FileCount,
			"totalSize":     req.TotalSize,
			"merkleRoot":    req.MerkleRoot,
			"createdAt":     req.CreatedAt,
			"expiresAt":     req.ExpiresAt,
			"redeemedAt":    req.RedeemedAt,
			"redeemAtm":     req.RedeemATM,
			"usbSerial":     req.USBSerial,
			"fsApproval":    req.FsApproval,
			"extras":        req.Extras,
			"fileNames":     names,
			"downloadable":  h.isDownloadable(req),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"scope": map[string]any{"all": all, "count": len(items)},
	})
}

func (h *Handler) getRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := h.Svc.Store.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !h.canAccessRequest(r, id) {
		http.Error(w, "无权查看该申请单", http.StatusForbidden)
		return
	}
	names := []string{}
	if m, err := h.Svc.Store.PhysicalFileNames(r.Context(), []string{id}, 50); err == nil {
		if got := m[id]; len(got) > 0 {
			names = got
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ID": req.ID, "id": req.ID,
		"Dir": req.Dir, "dir": req.Dir,
		"ApplicantID": req.ApplicantID, "applicantId": req.ApplicantID,
		"ApplicantName": req.ApplicantName, "applicantName": req.ApplicantName,
		"Dept": req.Dept, "dept": req.Dept,
		"Reason": req.Reason, "reason": req.Reason,
		"Cls": req.Cls, "cls": req.Cls,
		"Status": req.Status, "status": req.Status,
		"FileCount": req.FileCount, "fileCount": req.FileCount,
		"TotalSize": req.TotalSize, "totalSize": req.TotalSize,
		"MerkleRoot": req.MerkleRoot, "merkleRoot": req.MerkleRoot,
		"DataDir": req.DataDir, "dataDir": req.DataDir,
		"CreatedAt": req.CreatedAt, "createdAt": req.CreatedAt,
		"ExpiresAt": req.ExpiresAt, "expiresAt": req.ExpiresAt,
		"RedeemedAt": req.RedeemedAt, "redeemedAt": req.RedeemedAt,
		"RedeemATM": req.RedeemATM, "redeemAtm": req.RedeemATM,
		"USBSerial": req.USBSerial, "usbSerial": req.USBSerial,
		"FsApproval": req.FsApproval, "fsApproval": req.FsApproval,
		"Extras": req.Extras, "extras": req.Extras,
		"fileNames":    names,
		"downloadable": h.isDownloadable(req),
		"downloadUrl":  "/requests/" + req.ID + "/download",
	})
}

func (h *Handler) isDownloadable(req *store.Request) bool {
	if req == nil || req.Dir != model.DirImport || req.Status != store.StatusReleased {
		return false
	}
	dir, ok := h.releasedDir(req)
	if !ok {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

func (h *Handler) releasedDir(req *store.Request) (string, bool) {
	if req == nil {
		return "", false
	}
	root := strings.TrimSpace(h.ReleasedRoot)
	if root == "" && h.Svc != nil && h.Svc.StagingRoot != "" {
		// fallback: sibling of staging named "released"
		root = filepath.Join(filepath.Dir(h.Svc.StagingRoot), "released")
	}
	if root == "" {
		return "", false
	}
	// Prefer DataDir when it already points under ReleasedRoot (post-release).
	if req.DataDir != "" {
		absData, err := filepath.Abs(req.DataDir)
		absRoot, rerr := filepath.Abs(root)
		if err == nil && rerr == nil {
			rel, e := filepath.Rel(absRoot, absData)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if st, err := os.Stat(absData); err == nil && st.IsDir() {
					return absData, true
				}
			}
		}
	}
	return safeJoin(root, req.ID)
}

func (h *Handler) downloadReleased(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := h.Svc.Store.GetRequest(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !h.canAccessRequest(r, id) {
		http.Error(w, "无权下载该申请单", http.StatusForbidden)
		return
	}
	if req.Dir != model.DirImport {
		http.Error(w, "仅导入单可下载释放文件", http.StatusBadRequest)
		return
	}
	if req.Status != store.StatusReleased {
		http.Error(w, "尚未释放或已拦截，无法下载", http.StatusConflict)
		return
	}
	dir, ok := h.releasedDir(req)
	if !ok {
		http.Error(w, "释放目录不可用", http.StatusNotFound)
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		http.Error(w, "释放目录不存在", http.StatusNotFound)
		return
	}
	name := "fdt-import-" + id + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	defer zw.Close()
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("unsafe path")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = rel
		hdr.Method = zip.Deflate
		out, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	})
	if err != nil {
		// headers may already be sent; best-effort log via trailing body
		_, _ = io.WriteString(w, "")
		return
	}
	if u, ok := UserFromContext(r.Context()); ok {
		_, _ = h.Svc.Store.AppendAudit(r.Context(), "portal:"+u.Account, "", id, "import_downloaded", map[string]string{
			"account": u.Account,
		})
	}
}

func (h *Handler) getQR1(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.canAccessRequest(r, id) {
		http.Error(w, "无权查看该申请单", http.StatusForbidden)
		return
	}
	frame, err := h.Svc.GetQR1Frame(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"reqId": id, "qr1Frame": frame})
}

func (h *Handler) getQR1PNG(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !h.canAccessRequest(r, id) {
		http.Error(w, "无权查看该申请单", http.StatusForbidden)
		return
	}
	frame, err := h.Svc.GetQR1Frame(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	png, err := qrcode.Encode(frame, qrcode.Medium, 1024)
	if err != nil {
		http.Error(w, "qr encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
