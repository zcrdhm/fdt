package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// runSetupWizard opens a local page to collect ATM settings and write fdt.env.
// Call when config is missing, or when force is true (-setup).
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"portalBase":   c.PortalBase,
			"deviceId":     c.DeviceID,
			"deviceSecret": c.DeviceSecret,
			"gatewayPub":   c.GatewayPub,
			"usb":          c.USB,
			"usbDir":       c.USBDir,
			"home":         userATMRoot(),
			"envPath":      envPath,
		})
	})
	mux.HandleFunc("POST /api/setup", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PortalBase   string `json:"portalBase"`
			DeviceID     string `json:"deviceId"`
			DeviceSecret string `json:"deviceSecret"`
			GatewayPub   string `json:"gatewayPub"`
			MaintPass    string `json:"maintPass"`
			USB          string `json:"usb"`
			USBDir       string `json:"usbDir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		c := ATMConfig{
			PortalBase:   strings.TrimSpace(body.PortalBase),
			DeviceID:     strings.TrimSpace(body.DeviceID),
			DeviceSecret: strings.TrimSpace(body.DeviceSecret),
			GatewayPub:   body.GatewayPub,
			MaintPass:    body.MaintPass,
			USB:          body.USB,
			USBDir:       body.USBDir,
		}
		path, err := saveATMConfig(c)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1", "envPath": path})
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close(); _ = ln.Close() }()

	url := "http://" + ln.Addr().String() + "/"
	fmt.Printf("首次配置向导: %s\n  配置目录: %s\n", url, userATMRoot())
	if err := openWindow("FDT ATM 首次配置", url); err != nil {
		return "", fmt.Errorf("打开配置窗口: %w", err)
	}
	if !configReady(envPath) {
		return "", fmt.Errorf("配置未完成，请重新运行并保存（目录: %s）", userATMRoot())
	}
	return envPath, nil
}
