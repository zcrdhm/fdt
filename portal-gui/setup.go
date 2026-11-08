package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"fdt/internal/apprun"
)

// runSetupWizard opens a local page to collect Portal settings.
func runSetupWizard(force bool) (envPath string, err error) {
	_, envPath, _, _, _, _, err = ensureLayout()
	if err != nil {
		return "", err
	}
	if !force && configReady(envPath) {
		return envPath, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(setupHTML))
	})
	mux.HandleFunc("GET /api/setup", func(w http.ResponseWriter, r *http.Request) {
		c := loadConfigForForm(envPath)
		formJSON := strings.TrimSpace(c.FormFieldsJSON)
		if formJSON == "" {
			formJSON = strings.TrimSpace(defaultFormFieldsJSON())
		}
		impJSON := strings.TrimSpace(c.ImportFieldsJSON)
		if impJSON == "" {
			impJSON = strings.TrimSpace(defaultImportFieldsJSON())
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"home":                 userPortalRoot(),
			"envPath":              envPath,
			"listen":               c.Listen,
			"dataListen":           c.DataListen,
			"domain":               c.Domain,
			"publicBase":           c.PublicBase,
			"tenant":               c.Tenant,
			"https":                c.HTTPS,
			"dataDir":              c.DataDir,
			"keysDir":              c.KeysDir,
			"adminUser":            c.AdminUser,
			"adminPass":            c.AdminPass,
			"adminUsers":           c.AdminUsers,
			"identity":             c.Identity,
			"deviceId":             c.DeviceID,
			"deviceSecret":         c.DeviceSecret,
			"clamd":                c.Clamd,
			"formFieldsJSON":       formJSON,
			"formFieldsDefault":    strings.TrimSpace(defaultFormFieldsJSON()),
			"importFieldsJSON":     impJSON,
			"importFieldsDefault":  strings.TrimSpace(defaultImportFieldsJSON()),
			"hasInternalKey":       portalKeysPresent(apprunKeysHint(c)),
			"pkInternalPath":       filepath.Join(apprunKeysHint(c), "pk_internal.pem"),
			"defaultKeysDir":       filepath.Join(userPortalRoot(), "keys"),
			"os":                   goosHint(),
		})
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var body PortalConfig
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		path, err := savePortalConfig(body)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		pkPath := ""
		if env, err := loadEnvMap(path); err == nil {
			keys := env["KEYS_DIR"]
			pkPath = filepath.Join(keys, "pk_internal.pem")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ok": "1", "envPath": path, "pkInternal": pkPath, "home": userPortalRoot(),
		})
	})
	mux.HandleFunc("GET /api/setup/pk-internal", func(w http.ResponseWriter, r *http.Request) {
		keys := strings.TrimSpace(r.URL.Query().Get("keysDir"))
		if keys == "" {
			c := loadConfigForForm(envPath)
			keys = apprunKeysHint(c)
		} else {
			keys = resolveSetupDir(keys, filepath.Join(userPortalRoot(), "keys"))
		}
		pkPath := filepath.Join(keys, "pk_internal.pem")
		b, err := os.ReadFile(pkPath)
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("尚无 pk_internal.pem。请先点「生成并下载」。"))
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Header().Set("Content-Disposition", "attachment; filename=pk_internal.pem")
		w.Header().Set("X-Keys-Dir", keys)
		w.Header().Set("X-PK-Internal-Path", pkPath)
		w.Header().Set("Access-Control-Expose-Headers", "X-Keys-Dir, X-PK-Internal-Path")
		_, _ = w.Write(b)
	})
	// Generate keys now (no overwrite unless force); used by「生成并下载」.
	mux.HandleFunc("POST /api/setup/generate-keys", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			KeysDir string `json:"keysDir"`
			Force   bool   `json:"force"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		keys := resolveSetupDir(body.KeysDir, filepath.Join(userPortalRoot(), "keys"))
		created := !portalKeysPresent(keys)
		if err := generatePortalKeyFiles(keys, body.Force); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":             true,
			"keysDir":        keys,
			"created":        created || body.Force,
			"hasInternalKey": portalKeysPresent(keys),
			"pkInternal":     filepath.Join(keys, "pk_internal.pem"),
			"message":        "密钥已就绪，保存配置时将直接使用，不会覆盖",
		})
	})
	mux.HandleFunc("POST /api/setup/pick-dir", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
			Start string `json:"start"`
			Kind  string `json:"kind"` // data | keys
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		title := strings.TrimSpace(body.Title)
		if title == "" {
			if body.Kind == "keys" {
				title = "选择密钥目录 KEYS_DIR"
			} else {
				title = "选择数据目录 DATA_DIR"
			}
		}
		start := strings.TrimSpace(body.Start)
		if start == "" {
			if body.Kind == "keys" {
				start = filepath.Join(userPortalRoot(), "keys")
			} else {
				start = filepath.Join(userPortalRoot(), "var")
			}
		}
		picked, err := pickDirectory(title, start)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": picked, "cancelled": picked == ""})
	})
	mux.HandleFunc("POST /api/setup/test-perm", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fallback := filepath.Join(userPortalRoot(), "var")
		if body.Kind == "keys" {
			fallback = filepath.Join(userPortalRoot(), "keys")
		}
		path := resolveSetupDir(body.Path, fallback)
		res := testDirPermissions(path)
		code := http.StatusOK
		if !res.OK {
			code = http.StatusBadRequest
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(res)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close(); _ = ln.Close() }()

	url := "http://" + ln.Addr().String() + "/"
	fmt.Printf("Portal 首次配置向导: %s\n  配置目录: %s\n", url, userPortalRoot())
	if err := openWindow("FDT Portal 首次配置", url); err != nil {
		return "", fmt.Errorf("打开配置窗口: %w", err)
	}
	if !configReady(envPath) {
		return "", fmt.Errorf("配置未完成，请重新运行并保存（目录: %s）", userPortalRoot())
	}
	return envPath, nil
}

func apprunKeysHint(c PortalConfig) string {
	if strings.TrimSpace(c.KeysDir) != "" {
		return strings.TrimSpace(c.KeysDir)
	}
	return filepath.Join(userPortalRoot(), "keys")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func loadEnvMap(path string) (map[string]string, error) {
	return apprun.LoadEnv(path)
}
