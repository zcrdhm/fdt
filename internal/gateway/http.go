package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/feishu"
	"fdt/internal/hsm"
	"fdt/webapp"
)

// Handler exposes the gateway over HTTP. The Feishu web app POSTs the
// scanned QR#1 frame to /submit and polls /qr2; Feishu pushes events to
// /callback. The H5 shell is served at /app/. CallbackAllowCIDRs enforces
// the source-IP allowlist (design §8); leave empty only for offline development.
type Handler struct {
	Svc *Service
	// MaxCallbackBytes bounds the callback body read.
	MaxCallbackBytes int64
	// CallbackAllowCIDRs, when non-empty, rejects /callback from other sources.
	CallbackAllowCIDRs []*net.IPNet
	// DualControl, when set, exposes POST /hsm/approve for the two-person rule.
	DualControl *hsm.DualControlSigner
	// ApproveToken is a shared secret required by /hsm/approve (header X-Approve-Token).
	ApproveToken string
	// JSAPI signs Feishu H5 config for scanCode; nil falls back to mock.
	JSAPI feishu.JSAPISigner
	// MountAdmin, when set, registers /admin and /onboard on this mux.
	MountAdmin func(mux *http.ServeMux)
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /submit", h.submit)
	mux.HandleFunc("POST /whoami", h.whoami)
	mux.HandleFunc("GET /subscription", h.subscription)
	mux.HandleFunc("GET /qr2", h.qr2)
	mux.HandleFunc("GET /qr2/png", h.qr2PNG)
	mux.HandleFunc("GET /history", h.history)
	mux.HandleFunc("GET /jsapi/config", h.jsapiConfig)
	mux.HandleFunc("POST /callback/{tenant}", h.callback)
	mux.HandleFunc("POST /callback", h.callbackDefault)
	if h.DualControl != nil {
		mux.HandleFunc("POST /hsm/approve", h.approve)
	}
	// Serve H5 at /app (no trailing slash). Feishu requestAuthCode matches the
	// page URL against 重定向 URL exactly; keep this stable as https://host/app
	mux.HandleFunc("GET /app/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app", http.StatusFound)
	})
	mux.HandleFunc("GET /app", serveWebAppIndex)
	mux.HandleFunc("GET /app/{path...}", serveWebAppAsset)
	if h.MountAdmin != nil {
		h.MountAdmin(mux)
	}
	return mux
}

func serveWebAppIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(webapp.FS, "index.html")
	if err != nil {
		http.Error(w, "webapp missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func serveWebAppAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.PathValue("path"), "/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(webapp.FS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(name, ".ico"):
		w.Header().Set("Content-Type", "image/x-icon")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func (h *Handler) jsapiConfig(w http.ResponseWriter, r *http.Request) {
	pageURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if pageURL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}
	signer := h.JSAPI
	// Prefer a live tenant's Feishu app (console may bind after process start).
	if h.Svc != nil {
		if live := h.Svc.FirstLiveJSAPI(); live != nil {
			signer = live
		}
	}
	if signer == nil {
		signer = feishu.MockJSAPI{}
	}
	cfg, err := signer.SignJSAPI(r.Context(), pageURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func writeSubscriptionExpired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":   "subscription_expired",
		"message": "订阅已到期，请联系管理员续期",
	})
}

func (h *Handler) subscription(w http.ResponseWriter, r *http.Request) {
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		if frame := strings.TrimSpace(r.URL.Query().Get("frame")); frame != "" {
			if id, err := h.Svc.PeekTenantID(frame); err == nil {
				tenant = id
			}
		}
	}
	st := h.Svc.LookupSubscription(tenant)
	if st.Expired {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK) // informational; clients branch on expired
		_ = json.NewEncoder(w).Encode(st)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Frame    string `json:"frame"`
		AuthCode string `json:"authCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Frame == "" {
		http.Error(w, "frame required", http.StatusBadRequest)
		return
	}
	sub, err := h.Svc.SubmitQR1(r.Context(), body.Frame, body.AuthCode)
	if err != nil {
		if errors.Is(err, ErrSubscriptionExpired) {
			writeSubscriptionExpired(w)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// whoami resolves the Feishu H5 authCode to the logged-in user (for webapp identity panel).
func (h *Handler) whoami(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AuthCode string `json:"authCode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.AuthCode) == "" {
		http.Error(w, "authCode required", http.StatusBadRequest)
		return
	}
	info, err := h.Svc.ResolveIdentity(r.Context(), body.AuthCode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (h *Handler) qr2(w http.ResponseWriter, r *http.Request) {
	reqID := r.URL.Query().Get("reqId")
	if reqID == "" {
		http.Error(w, "reqId required", http.StatusBadRequest)
		return
	}
	res, err := h.Svc.PollQR2(r.Context(), reqID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionExpired) {
			writeSubscriptionExpired(w)
			return
		}
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) history(w http.ResponseWriter, r *http.Request) {
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 200 {
				limit = 200
			}
		}
	}
	items := h.Svc.ListHistory(tenant, limit)
	if items == nil {
		items = []HistoryItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) qr2PNG(w http.ResponseWriter, r *http.Request) {
	reqID := r.URL.Query().Get("reqId")
	if reqID == "" {
		http.Error(w, "reqId required", http.StatusBadRequest)
		return
	}
	res, err := h.Svc.PollQR2(r.Context(), reqID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionExpired) {
			writeSubscriptionExpired(w)
			return
		}
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if res.QR2Frame == "" {
		http.Error(w, "not approved yet", http.StatusAccepted)
		return
	}
	png, err := qrcode.Encode(res.QR2Frame, qrcode.Medium, 1024)
	if err != nil {
		http.Error(w, "qr encode: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (h *Handler) callbackDefault(w http.ResponseWriter, r *http.Request) {
	h.handleCallback(w, r, "default")
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	h.handleCallback(w, r, r.PathValue("tenant"))
}

func (h *Handler) handleCallback(w http.ResponseWriter, r *http.Request, tenant string) {
	if len(h.CallbackAllowCIDRs) > 0 {
		ip := remoteIP(r.RemoteAddr)
		if xff := r.Header.Get("X-Real-IP"); xff != "" {
			if parsed := net.ParseIP(xff); parsed != nil {
				ip = parsed
			}
		}
		if !ipAllowed(ip, h.CallbackAllowCIDRs) {
			http.Error(w, "callback source not allowed", http.StatusForbidden)
			return
		}
	}
	limit := h.MaxCallbackBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, limit))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Feishu URL 校验必须回 JSON {"challenge":...}；租户尚未 active 时也要能过。
	if ch, ok := feishu.PeekURLChallenge(raw); ok {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": ch})
		return
	}
	challenge, err := h.Svc.HandleCallback(r.Context(), tenant, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if challenge != "" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	if h.ApproveToken != "" && r.Header.Get("X-Approve-Token") != h.ApproveToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body struct {
		By string `json:"by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.By == "" {
		http.Error(w, "by required", http.StatusBadRequest)
		return
	}
	if err := h.DualControl.Approve(body.By); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pending": h.DualControl.Pending()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
