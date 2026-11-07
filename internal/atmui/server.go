package atmui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/atm"
	"fdt/internal/model"
)

// Server is the local ATM kiosk HTTP UI (touchscreen / browser).
type Server struct {
	Kiosk  *atm.Kiosk
	Camera string // e.g. /dev/video0; empty disables server-side camera
	// PublicBase is scheme://host[:port] used when launching Chromium kiosk.
	PublicBase string

	maint *maintAuth

	importFields ImportFieldsFile

	mu         sync.Mutex
	written    int64
	total      int64
	busy       bool
	lastMsg    string
	importID   string
	progress   atm.WriteProgress
	progressAt time.Time
}

// Options configures the ATM UI server beyond the kiosk itself.
type Options struct {
	Camera            string
	MaintPassword     string // ATM_MAINT_PASSWORD bootstrap (default fdt-maint)
	MaintPasswordFile string
	ImportFieldsFile  string // ATM_IMPORT_FIELDS; empty → defaults
	// PublicBase is the browser origin for kiosk launch, e.g. https://127.0.0.1 or http://127.0.0.1:9080.
	// Empty → derive from the incoming request (loopback preferred).
	PublicBase string
}

// New wires progress reporting onto the kiosk.
func New(k *atm.Kiosk, camera string) *Server {
	return NewWithOptions(k, Options{Camera: camera})
}

// NewWithOptions creates the UI server with maintenance-password settings.
func NewWithOptions(k *atm.Kiosk, opt Options) *Server {
	fields, err := LoadImportFields(opt.ImportFieldsFile)
	if err != nil {
		fields = DefaultImportFields()
	}
	s := &Server{
		Kiosk:        k,
		Camera:       opt.Camera,
		PublicBase:   strings.TrimRight(strings.TrimSpace(opt.PublicBase), "/"),
		maint:        newMaintAuth(opt.MaintPasswordFile, opt.MaintPassword),
		importFields: fields,
	}
	k.Progress = func(p atm.WriteProgress) {
		s.mu.Lock()
		s.written, s.total = p.Written, p.Total
		s.progress = p
		if s.progressAt.IsZero() {
			s.progressAt = time.Now()
		}
		s.mu.Unlock()
	}
	return s
}

// Handler serves UI + JSON API on a single mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/usb/rescan", s.rescanUSB)
	mux.HandleFunc("POST /api/usb/eject", s.ejectUSB)
	mux.HandleFunc("GET /api/usb/list", s.listUSB)
	mux.HandleFunc("POST /api/redeem", s.redeem)
	mux.HandleFunc("POST /api/scan/camera", s.scanCamera)
	mux.HandleFunc("GET /api/import/fields", s.importFieldsAPI)
	mux.HandleFunc("POST /api/import/start", s.importStart)
	mux.HandleFunc("POST /api/import/complete", s.importComplete)
	mux.HandleFunc("POST /api/maint/unlock", s.maintUnlock)
	mux.HandleFunc("POST /api/maint/password", s.maintChangePassword)
	mux.HandleFunc("POST /api/maint/kiosk", s.maintLaunchKiosk)
	mux.HandleFunc("GET /{$}", s.ui("index.html"))
	mux.HandleFunc("GET /app.css", s.ui("app.css"))
	mux.HandleFunc("GET /app.js", s.ui("app.js"))
	mux.HandleFunc("GET /jsQR.min.js", s.ui("jsQR.min.js"))
	mux.HandleFunc("GET /logo.png", s.ui("logo.png"))
	mux.HandleFunc("GET /favicon.ico", s.ui("favicon.ico"))
	return mux
}

func (s *Server) ui(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(FS, name)
		if err != nil {
			http.NotFound(w, r)
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
			// Allow Keyboard Lock / fullscreen in Chromium when available.
			w.Header().Set("Permissions-Policy", "fullscreen=*, keyboard-map=*")
		}
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.statusPayload())
}

// rescanUSB forces a USB Detect (including AutoMount) and returns fresh status.
func (s *Server) rescanUSB(w http.ResponseWriter, r *http.Request) {
	out := s.statusPayload()
	if out["usb"] == nil {
		s.setMsg("未检测到 USB 存储设备")
	} else if usb, _ := out["usb"].(map[string]any); usb["mounted"] == true {
		s.setMsg("已检测到 U 盘 " + strAny(usb["serial"]))
	} else {
		s.setMsg("U 盘已插入但未挂载成功，请检查分区/权限")
	}
	out = s.statusPayload()
	writeJSON(w, http.StatusOK, out)
}

// ejectUSB syncs and unmounts the current USB so it can be removed safely.
func (s *Server) ejectUSB(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	busy := s.busy
	s.mu.Unlock()
	if busy {
		http.Error(w, "正在写入，请稍后再安全拔出", http.StatusConflict)
		return
	}
	dev, err := s.Kiosk.USB.Detect()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if dev == nil || dev.MountPath() == "" {
		s.setMsg("没有已挂载的 U 盘")
		writeJSON(w, http.StatusOK, s.statusPayload())
		return
	}
	serial := dev.Serial()
	if err := dev.Eject(); err != nil {
		s.setMsg("卸载失败: " + err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.setMsg("已安全卸载 " + serial + "，可以拔出 U 盘")
	writeJSON(w, http.StatusOK, s.statusPayload())
}

func strAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func (s *Server) statusPayload() map[string]any {
	st := s.Kiosk.State()
	dev, _ := s.Kiosk.USB.Detect()
	s.mu.Lock()
	written, total, busy, msg, imp := s.written, s.total, s.busy, s.lastMsg, s.importID
	prog := s.progress
	started := s.progressAt
	s.mu.Unlock()
	pct := 0
	if total > 0 {
		pct = int(written * 100 / total)
		if pct > 100 {
			pct = 100
		}
	}
	out := map[string]any{
		"state": string(st), "busy": busy, "message": msg,
		"written": written, "total": total, "percent": pct,
		"importReqId": imp,
		"camera":      s.Camera,
		"deviceId":    s.Kiosk.ID,
		"progress":    prog,
	}
	if busy && written > 0 && !started.IsZero() {
		elapsed := time.Since(started).Seconds()
		if elapsed >= 0.4 {
			rate := float64(written) / elapsed
			out["rateBps"] = int64(rate)
			if total > written && rate > 0 {
				out["etaSec"] = int64(float64(total-written) / rate)
			}
		}
	}
	if err := s.Kiosk.LastErr(); err != nil {
		out["lastError"] = err.Error()
	}
	if dev != nil {
		out["usb"] = map[string]any{
			"serial":   dev.Serial(),
			"path":     dev.MountPath(),
			"writable": dev.Writable(),
			"mounted":  dev.MountPath() != "",
			"free":     dev.FreeBytes(),
			"capacity": dev.CapacityBytes(),
			"fs":       dev.FileSystem(),
		}
	} else {
		out["usb"] = nil
	}
	return out
}

func (s *Server) redeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Frame string `json:"frame"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Frame) == "" {
		http.Error(w, "需要 frame", http.StatusBadRequest)
		return
	}
	if !s.begin() {
		http.Error(w, "忙，请稍候", http.StatusConflict)
		return
	}
	defer s.end()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	res, err := s.Kiosk.ProcessScan(ctx, body.Frame)
	if err != nil {
		s.setMsg(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.setMsg("兑付成功，可安全拔出 U 盘")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) scanCamera(w http.ResponseWriter, r *http.Request) {
	if s.Camera == "" {
		http.Error(w, "未配置摄像头（CAMERA）", http.StatusBadRequest)
		return
	}
	sc, err := atm.OpenCamera(s.Camera)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Cap open time so a kiosk cannot leave the camera running indefinitely.
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	frame, err := sc.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			http.Error(w, "扫码超时（60 秒内未识别到二维码），摄像头已关闭", http.StatusGatewayTimeout)
			return
		}
		http.Error(w, err.Error(), http.StatusGatewayTimeout)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"frame": frame})
}

func (s *Server) listUSB(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	mount, parent, entries, err := s.Kiosk.ListUSBDir(path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mount": mount, "path": path, "parent": parent, "entries": entries,
	})
}

func (s *Server) importFieldsAPI(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.importFields.Version,
		"fields":  s.importFields.UserFields(),
	})
}

func (s *Server) importStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string            `json:"reason"`
		Cls    string            `json:"cls"`
		Fields map[string]string `json:"fields"`
		ID     string            `json:"applicantId"`
		Name   string            `json:"applicantName"`
		Dept   string            `json:"applicantDept"`
		Paths  []string          `json:"paths"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	values := body.Fields
	if values == nil {
		values = map[string]string{}
	}
	if body.Reason != "" {
		values["reason"] = body.Reason
	}
	if body.Cls != "" {
		values["cls"] = body.Cls
	}
	reason, cls, extras, err := s.importFields.NormalizeValues(values)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		body.ID = s.Kiosk.ID
	}
	if body.Name == "" {
		body.Name = body.ID
	}
	if len(body.Paths) == 0 {
		http.Error(w, "请先从 U 盘选择要导入的文件或目录", http.StatusBadRequest)
		return
	}
	if !s.begin() {
		http.Error(w, "忙，请稍候", http.StatusConflict)
		return
	}
	defer s.end()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	start, err := s.Kiosk.StartImport(ctx, model.Applicant{
		ID: body.ID, Name: body.Name, Dept: body.Dept,
	}, reason, model.Classification(cls), body.Paths, extras)
	if err != nil {
		s.setMsg(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.importID = start.ReqID
	s.mu.Unlock()
	s.setMsg("已上传隔离区并签发 QR#1，可用飞书扫码；之后可离开，凭 QR#2 回来放行")
	png, _ := qrcode.Encode(start.QR1Frame, qrcode.Medium, 1600)
	writeJSON(w, http.StatusOK, map[string]any{
		"reqId": start.ReqID, "qr1Frame": start.QR1Frame,
		"count": start.Count, "merkleRoot": start.MerkleRoot,
		"usbSerial": start.USBSerial, "paths": start.Paths, "qr1PngBase64": png,
	})
}

func (s *Server) importComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReqID string `json:"reqId"`
		Frame string `json:"frame"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if body.Frame == "" {
		http.Error(w, "需要 QR#2 frame", http.StatusBadRequest)
		return
	}
	if body.ReqID == "" {
		s.mu.Lock()
		body.ReqID = s.importID
		s.mu.Unlock()
	}
	if !s.begin() {
		http.Error(w, "忙，请稍候", http.StatusConflict)
		return
	}
	defer s.end()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	res, err := s.Kiosk.CompleteImport(ctx, body.ReqID, body.Frame)
	if err != nil {
		s.setMsg(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.setMsg("导入完成")
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) maintUnlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.maint == nil || !s.maint.check(body.Password) {
		http.Error(w, "维护密码不正确", http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) maintChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old string `json:"oldPassword"`
		New string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if s.maint == nil {
		http.Error(w, "维护密码未配置", http.StatusServiceUnavailable)
		return
	}
	if err := s.maint.change(body.Old, body.New); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "维护密码已更新"})
}

// maintLaunchKiosk starts Chromium --kiosk with a dedicated profile.
// Page Fullscreen API cannot stop Esc; OS kiosk is the supported ATM lock.
func (s *Server) maintLaunchKiosk(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(strings.TrimSpace(s.PublicBase), "/")
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		host := r.Host
		if host == "" {
			host = "127.0.0.1:9080"
		}
		// Prefer loopback so Keyboard Lock (secure context) works.
		if h, _, ok := strings.Cut(host, ":"); ok && (h == "127.0.0.1" || h == "localhost" || h == "::1") {
			base = scheme + "://" + host
		} else if host == "127.0.0.1" || host == "localhost" {
			base = scheme + "://" + host
		} else {
			port := "9080"
			if _, p, err := splitHostPortSafe(host); err == nil && p != "" {
				port = p
			}
			base = scheme + "://127.0.0.1:" + port
			if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
				base = scheme + "://127.0.0.1"
			}
		}
	}
	ui := base + "/?kiosk=1&run=1"
	chrome, err := LaunchKioskBrowser(ui)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "chrome": chrome, "url": ui,
		"message": "已启动 Chromium 锁屏窗口（--kiosk）。请在该窗口操作；普通浏览器无法真正屏蔽 Esc。",
	})
}

func splitHostPortSafe(hostport string) (host, port string, err error) {
	// net.SplitHostPort needs brackets for IPv6; Host rarely includes them here.
	if strings.Count(hostport, ":") == 1 {
		i := strings.LastIndex(hostport, ":")
		return hostport[:i], hostport[i+1:], nil
	}
	return hostport, "", fmt.Errorf("no port")
}

func (s *Server) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	s.written, s.total = 0, 0
	s.progress = atm.WriteProgress{}
	s.progressAt = time.Now()
	s.lastMsg = ""
	return true
}

func (s *Server) end() {
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *Server) setMsg(m string) {
	s.mu.Lock()
	s.lastMsg = m
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
