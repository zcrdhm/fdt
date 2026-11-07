package gwadmin

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"fdt/internal/gateway"
	"fdt/internal/gwacme"
	"fdt/internal/gwdomain"
	"fdt/webadmin"
)

// Handler serves /admin, /register and /console.
type Handler struct {
	Store       *Store
	Auth        *Auth
	Tenants     *TenantSessions
	ACME        *gwacme.Manager
	ACMEFile    string
	DomainFile  string
	EnvFile     string // fdt.env path; used to persist ADMIN_PASSWORD
	ApplyACME   func(gwacme.Config) error
	ApplyDomain func(gwdomain.Config) error
}

func (h *Handler) Mount(mux *http.ServeMux) {
	if h.Tenants == nil {
		h.Tenants = NewTenantSessions()
	}

	mux.HandleFunc("POST /admin/api/login", h.login)
	mux.HandleFunc("POST /admin/api/logout", h.logout)
	mux.HandleFunc("GET /admin/api/me", h.me)
	mux.HandleFunc("GET /admin/api/tenants", h.requireAdmin(h.listTenants))
	mux.HandleFunc("POST /admin/api/tenants", h.requireAdmin(h.createTenant))
	mux.HandleFunc("GET /admin/api/tenants/{id}", h.requireAdmin(h.getTenant))
	mux.HandleFunc("PATCH /admin/api/tenants/{id}", h.requireAdmin(h.patchTenant))
	mux.HandleFunc("DELETE /admin/api/tenants/{id}", h.requireAdmin(h.deleteTenant))
	mux.HandleFunc("POST /admin/api/tenants/{id}/invite", h.requireAdmin(h.reinvite))
	mux.HandleFunc("POST /admin/api/password", h.requireAdmin(h.adminPassword))
	mux.HandleFunc("GET /admin/api/domain", h.requireAdmin(h.domainStatus))
	mux.HandleFunc("POST /admin/api/domain", h.requireAdmin(h.domainBind))
	mux.HandleFunc("POST /admin/api/domain/check", h.requireAdmin(h.domainCheck))
	mux.HandleFunc("GET /admin/api/acme", h.requireAdmin(h.acmeStatus))
	mux.HandleFunc("POST /admin/api/acme", h.requireAdmin(h.acmeApply))

	mux.HandleFunc("GET /register/api/{token}", h.registerInfo)
	mux.HandleFunc("POST /register/api/{token}", h.registerSubmit)

	mux.HandleFunc("POST /console/api/login", h.consoleLogin)
	mux.HandleFunc("POST /console/api/logout", h.consoleLogout)
	mux.HandleFunc("GET /console/api/me", h.requireTenant(h.consoleMe))
	mux.HandleFunc("POST /console/api/profile", h.requireTenant(h.consoleProfile))
	mux.HandleFunc("POST /console/api/password", h.requireTenant(h.consolePassword))
	mux.HandleFunc("POST /console/api/pk-internal", h.requireTenant(h.consoleUploadPub))
	mux.HandleFunc("POST /console/api/feishu", h.requireTenant(h.consoleFeishu))
	mux.HandleFunc("GET /console/api/pk-gateway", h.requireTenant(h.consoleDownloadPub))
	mux.HandleFunc("POST /console/api/regen-gateway", h.requireTenant(h.consoleRegen))

	mux.HandleFunc("GET /admin/{$}", func(w http.ResponseWriter, r *http.Request) { serveEmbedded(w, "admin.html") })
	mux.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/", http.StatusFound)
	})
	mux.HandleFunc("GET /admin/{path...}", func(w http.ResponseWriter, r *http.Request) {
		serveEmbedded(w, strings.TrimPrefix(r.PathValue("path"), "/"))
	})

	mux.HandleFunc("GET /register/{$}", func(w http.ResponseWriter, r *http.Request) { serveEmbedded(w, "register.html") })
	mux.HandleFunc("GET /register", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/register/", http.StatusFound)
	})
	mux.HandleFunc("GET /register/{path...}", func(w http.ResponseWriter, r *http.Request) {
		serveEmbedded(w, strings.TrimPrefix(r.PathValue("path"), "/"))
	})

	mux.HandleFunc("GET /console/{$}", func(w http.ResponseWriter, r *http.Request) { serveEmbedded(w, "console.html") })
	mux.HandleFunc("GET /console", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/console/", http.StatusFound)
	})
	mux.HandleFunc("GET /console/{path...}", func(w http.ResponseWriter, r *http.Request) {
		serveEmbedded(w, strings.TrimPrefix(r.PathValue("path"), "/"))
	})

	// Legacy onboard → register
	mux.HandleFunc("GET /onboard/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/register/"+r.URL.RawQuery+r.URL.Fragment, http.StatusFound)
	})
	mux.HandleFunc("GET /onboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/register/", http.StatusFound)
	})
}

func (h *Handler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.Auth == nil || !h.Auth.Valid(h.Auth.TokenFromRequest(r)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireTenant(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := h.Tenants.TenantID(h.Tenants.TokenFromRequest(r))
		if id == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Set("X-FDT-Tenant", id)
		next(w, r)
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	tok, err := h.Auth.Login(body.User, body.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	h.Auth.SetCookie(w, tok)
	writeJSON(w, map[string]any{"ok": true, "user": h.Auth.User})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if h.Auth != nil {
		h.Auth.Logout(h.Auth.TokenFromRequest(r))
		h.Auth.ClearCookie(w)
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	if h.Auth == nil || !h.Auth.Valid(h.Auth.TokenFromRequest(r)) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"user": h.Auth.User})
}

func (h *Handler) adminPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := h.Auth.ChangePassword(body.OldPassword, body.NewPassword); err != nil {
		if err == ErrUnauthorized {
			http.Error(w, "旧密码不正确", http.StatusUnauthorized)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.EnvFile != "" {
		if err := setEnvValue(h.EnvFile, "ADMIN_PASSWORD", body.NewPassword); err != nil {
			http.Error(w, "密码已生效但写入 fdt.env 失败: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "message": "密码已修改（已写入配置，重启后仍有效）"})
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []PublicView{}
	}
	base := h.loadDomain().PublicBase()
	writeJSON(w, map[string]any{"tenants": list, "publicBase": base})
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	v, err := h.Store.CreateTenant(body.ID, body.DisplayName)
	if err != nil {
		status := http.StatusBadRequest
		if err == ErrExists {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) getTenant(w http.ResponseWriter, r *http.Request) {
	v, err := h.Store.Get(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) patchTenant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Status *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Status != nil {
		if err := h.Store.SetStatus(id, *body.Status); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	v, err := h.Store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) deleteTenant(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.Delete(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) reinvite(w http.ResponseWriter, r *http.Request) {
	v, err := h.Store.Reinvite(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) loadDomain() gwdomain.Config {
	if h.DomainFile == "" {
		return gwdomain.Config{}
	}
	c, _ := gwdomain.LoadFile(h.DomainFile)
	return c
}

func (h *Handler) domainStatus(w http.ResponseWriter, r *http.Request) {
	st := h.loadDomain().ToStatus()
	writeJSON(w, st)
}

func (h *Handler) domainBind(w http.ResponseWriter, r *http.Request) {
	var body gwdomain.Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body = gwdomain.Normalize(body)
	if body.Domain == "" {
		http.Error(w, "domain required", http.StatusBadRequest)
		return
	}
	if h.ApplyDomain == nil {
		http.Error(w, "domain bind not configured", http.StatusServiceUnavailable)
		return
	}
	if err := h.ApplyDomain(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st := body.ToStatus()
	writeJSON(w, map[string]any{
		"ok":     true,
		"status": st,
		"message": "已绑定域名 " + body.Domain + "。请将 DNS A/AAAA 指向本机后点「检测解析」，再申请 Let's Encrypt。飞书主页填 " +
			st.PublicBase + "/app/ ，H5 可信域名填 " + body.Domain + "。",
	})
}

func (h *Handler) domainCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Domain  string   `json:"domain"`
		Aliases []string `json:"aliases"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	cfg := h.loadDomain()
	if body.Domain != "" {
		cfg.Domain = body.Domain
		cfg.Aliases = body.Aliases
	}
	cfg = gwdomain.Normalize(cfg)
	if cfg.Domain == "" {
		http.Error(w, "尚未绑定域名", http.StatusBadRequest)
		return
	}
	var checks []gwdomain.CheckResult
	allOK := true
	for _, host := range cfg.Hosts() {
		c := gwdomain.CheckDNS(host)
		checks = append(checks, c)
		if !c.Matched && len(c.Resolved) == 0 {
			allOK = false
		}
	}
	writeJSON(w, map[string]any{
		"ok":       true,
		"checks":   checks,
		"resolved": allOK,
		"message":  "请确认解析指向本机公网入口；经 NAT/LB 时本机网卡可能匹配不上，只要公网可达即可。",
	})
}

func (h *Handler) acmeStatus(w http.ResponseWriter, r *http.Request) {
	if h.ACME == nil {
		writeJSON(w, gwacme.Status{Message: "ACME 未挂载"})
		return
	}
	st := h.ACME.Status()
	cfg := h.ACME.Config()
	if !cfg.Enabled {
		if fileCfg, err := gwacme.LoadFile(h.ACMEFile); err == nil {
			st.Domain = fileCfg.Domain
			st.Aliases = fileCfg.Aliases
			st.Email = fileCfg.Email
			st.Enabled = fileCfg.Enabled
			st.Cache = fileCfg.Cache
		}
	}
	if st.Domain == "" {
		if d := h.loadDomain(); d.Domain != "" {
			st.Domain = d.Domain
			st.Aliases = d.Aliases
			if st.Email == "" {
				st.Email = d.Email
			}
		}
	}
	writeJSON(w, st)
}

func (h *Handler) acmeApply(w http.ResponseWriter, r *http.Request) {
	var body gwacme.Config
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	body.Enabled = true
	body.Domain = strings.TrimSpace(body.Domain)
	body.Email = strings.TrimSpace(body.Email)
	if body.Domain == "" {
		if d := h.loadDomain(); d.Domain != "" {
			body.Domain = d.Domain
			body.Aliases = d.Aliases
			if body.Email == "" {
				body.Email = d.Email
			}
		}
	}
	if body.Domain == "" {
		http.Error(w, "请先绑定域名", http.StatusBadRequest)
		return
	}
	if h.ApplyACME == nil {
		http.Error(w, "ACME apply not configured", http.StatusServiceUnavailable)
		return
	}
	// Keep domain binding in sync.
	if h.ApplyDomain != nil {
		_ = h.ApplyDomain(gwdomain.Config{Domain: body.Domain, Aliases: body.Aliases, Email: body.Email})
	}
	if err := h.ApplyACME(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{
		"ok":      true,
		"status":  h.ACME.Status(),
		"message": "已启用 ACME。请确认 LISTEN=0.0.0.0:443、防火墙放行 80/443，并用 https://" + body.Domain + " 访问以触发签发；然后 systemctl restart fdt（若当前仍在 HTTP 端口）。",
	})
}

func (h *Handler) registerInfo(w http.ResponseWriter, r *http.Request) {
	meta, err := h.Store.LookupInvite(r.PathValue("token"))
	if err != nil {
		http.Error(w, "invalid registration link", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{
		"id": meta.ID, "displayName": meta.DisplayName,
		"inviteExpiry": meta.InviteExpiry,
	})
}

func (h *Handler) registerSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User         string `json:"user"`
		Password     string `json:"password"`
		ContactName  string `json:"contactName"`
		ContactEmail string `json:"contactEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	v, err := h.Store.RegisterWithInvite(r.PathValue("token"), body.User, body.Password, body.ContactName, body.ContactEmail)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"ok": true, "tenant": v,
		"consoleUrl": "/console/",
		"message":    "注册成功，请前往租户后台登录并上传 PK_内网、绑定飞书",
	})
}

func (h *Handler) consoleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	id, err := h.Store.AuthenticateTenant(body.User, body.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	tok, err := h.Tenants.Create(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Tenants.SetCookie(w, tok)
	writeJSON(w, map[string]any{"ok": true, "tenantId": id})
}

func (h *Handler) consoleLogout(w http.ResponseWriter, r *http.Request) {
	h.Tenants.Logout(h.Tenants.TokenFromRequest(r))
	h.Tenants.ClearCookie(w)
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) consoleMe(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	v, err := h.Store.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) consoleProfile(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	var body struct {
		DisplayName  string `json:"displayName"`
		ContactName  string `json:"contactName"`
		ContactEmail string `json:"contactEmail"`
		Notes        string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	v, err := h.Store.UpdateProfile(id, body.DisplayName, body.ContactName, body.ContactEmail, body.Notes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) consolePassword(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	var body struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := h.Store.ChangeOperatorPassword(id, body.OldPassword, body.NewPassword); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (h *Handler) consoleUploadPub(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	var pem []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("pk_internal")
		if err != nil {
			http.Error(w, "pk_internal required", http.StatusBadRequest)
			return
		}
		defer f.Close()
		pem, _ = io.ReadAll(io.LimitReader(f, 1<<16))
	} else {
		var body struct {
			Pem string `json:"pem"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pem = []byte(body.Pem)
	}
	v, err := h.Store.UploadInternalPub(id, pem)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) consoleFeishu(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	var body gateway.TenantFeishuFile
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	v, err := h.Store.UpdateFeishuFromConsole(id, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, v)
}

func (h *Handler) consoleDownloadPub(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	pem, err := h.Store.GatewayPubPEM(id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=pk_gateway.pem")
	_, _ = w.Write(pem)
}

func (h *Handler) consoleRegen(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-FDT-Tenant")
	v, err := h.Store.RegenerateGatewayKeys(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func serveEmbedded(w http.ResponseWriter, name string) {
	if name == "" || strings.Contains(name, "..") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	b, err := fs.ReadFile(webadmin.FS, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	switch {
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
