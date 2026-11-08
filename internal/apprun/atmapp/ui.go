package atmapp

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fdt/internal/apprun"
	"fdt/internal/atm"
	"fdt/internal/atmui"
	"fdt/internal/crypto"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/store"
)

// UIOptions configures the embedded ATM HTTP UI.
type UIOptions struct {
	EnvPath   string // optional explicit fdt.env
	Listen    string // override ATM_UI_LISTEN; empty → env/default
	Local     bool   // force 127.0.0.1 host (GUI mode)
	OnQuitGUI func() // ATM GUI: maint-authenticated process exit
}

// UIHandle is a running ATM UI server.
type UIHandle struct {
	URL      string // browser origin, e.g. http://127.0.0.1:9080
	EnvPath  string
	DeviceID string
	shutdown func() error
}

// Shutdown stops the HTTP server and closes the DB.
func (h *UIHandle) Shutdown(ctx context.Context) error {
	if h == nil || h.shutdown == nil {
		return nil
	}
	return h.shutdown()
}

// StartUI boots ATM kiosk HTTP UI in the background (for fdt-atm -ui and atm-gui).
func StartUI(opts UIOptions) (*UIHandle, error) {
	envPath := opts.EnvPath
	env := loadATMEnv(envPath)
	if used := findATMEnvPath(envPath); used != "" {
		envPath = used
	}
	keys := apprun.EnvOr(env, "KEYS_DIR", "keys")
	dataDir := apprun.EnvOr(env, "DATA_DIR", ".")
	dbPath := filepath.Join(dataDir, "fdt.db")
	gwPub := filepath.Join(keys, "pk_gateway.pem")
	deviceID := apprun.EnvOr(env, "DEVICE_ID", "atm-01")
	secret := apprun.EnvOr(env, "DEVICE_SECRET", "dev-secret")
	dataURL := apprun.EnvOr(env, "DATA_URL", "http://127.0.0.1:8081/data")
	impURL := apprun.EnvOr(env, "IMPORT_URL", "http://127.0.0.1:8081/import")
	usbMode := apprun.EnvOr(env, "USB", defaultUSBMode())
	usbDir := env["USB_DIR"]
	cameraDev := env["CAMERA"]
	listen := opts.Listen
	if listen == "" {
		listen = apprun.EnvOr(env, "ATM_UI_LISTEN", "0.0.0.0:9080")
	}
	if opts.Local {
		listen = forceLocalListen(listen)
	}
	uiTLS := apprun.EnvTruthy(env, "ATM_UI_TLS") || apprun.EnvTruthy(env, "TLS")
	uiTLSCert := apprun.FirstNonEmpty(env["ATM_UI_TLS_CERT"], env["TLS_CERT"], filepath.Join(keys, "server.pem"))
	uiTLSKey := apprun.FirstNonEmpty(env["ATM_UI_TLS_KEY"], env["TLS_KEY"], filepath.Join(keys, "server.key"))
	uiPublic := env["ATM_UI_URL"]
	maintPass := apprun.EnvOr(env, "ATM_MAINT_PASSWORD", "fdt-maint")
	maintPassFile := apprun.EnvOr(env, "ATM_MAINT_PASSWORD_FILE", filepath.Join(dataDir, "atm_maint_password"))
	importFields := apprun.EnvOr(env, "ATM_IMPORT_FIELDS", "")
	if importFields == "" {
		if envPath != "" {
			importFields = filepath.Join(filepath.Dir(envPath), "import-fields.json")
		} else {
			importFields = "/opt/fdt-atm/etc/import-fields.json"
		}
	}

	pub, err := crypto.ReadPublicPEM(gwPub)
	if err != nil {
		return nil, fmt.Errorf("读 PK_网关 %s: %w", gwPub, err)
	}
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, err
	}

	var usb atm.Manager
	switch strings.ToLower(usbMode) {
	case "sysfs":
		mount := apprun.EnvOr(env, "USB_MOUNT", "/mnt/fdt-usb")
		usb = atm.SysfsUSB{AutoMount: mount}
		_ = os.MkdirAll(mount, 0o755)
	case "windows":
		usb = atm.WindowsUSB{}
	default:
		m := atm.NewMockManager()
		if usbDir != "" {
			_ = m.Insert("USB-DEV", usbDir, 1<<30, true, "exfat")
		}
		usb = m
	}
	k := atm.NewKiosk(deviceID, st, pub, nil, usb)
	k.Fetcher = &atm.HTTPFetcher{BaseURL: dataURL, DeviceID: deviceID, Secret: secret}
	k.Importer = &importer.HTTPClient{BaseURL: impURL, DeviceID: deviceID, Secret: secret}
	k.ImportDLP = manifest.ContentEngine{}

	_ = atmui.WriteDefaultImportFields(importFields)
	publicBase := strings.TrimRight(strings.TrimSpace(uiPublic), "/")
	if publicBase == "" {
		publicBase = deriveATMUIPublicBase(listen, uiTLS)
	}
	srv := atmui.NewWithOptions(k, atmui.Options{
		Camera:            cameraDev,
		MaintPassword:     maintPass,
		MaintPasswordFile: maintPassFile,
		ImportFieldsFile:  importFields,
		PublicBase:        publicBase,
		OnQuitGUI:         opts.OnQuitGUI,
	})

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		// Port busy: fall back to random local port for GUI.
		if opts.Local || strings.Contains(err.Error(), "address already in use") {
			fallback := "127.0.0.1:0"
			ln, err = net.Listen("tcp", fallback)
			if err != nil {
				_ = st.Close()
				return nil, fmt.Errorf("ATM UI 监听失败 (%s / %s): %w", listen, fallback, err)
			}
			listen = ln.Addr().String()
			publicBase = deriveATMUIPublicBase(listen, uiTLS)
		} else {
			_ = st.Close()
			return nil, fmt.Errorf("ATM UI 监听 %s: %w", listen, err)
		}
	}

	httpSrv := &http.Server{Handler: srv.Handler()}
	if uiTLS {
		cert, err := tls.LoadX509KeyPair(uiTLSCert, uiTLSKey)
		if err != nil {
			_ = ln.Close()
			_ = st.Close()
			return nil, fmt.Errorf("ATM UI TLS: 无法加载证书 %s / %s: %w", uiTLSCert, uiTLSKey, err)
		}
		httpSrv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if httpSrv.TLSConfig != nil {
			log.Printf("atm-ui listening on %s (https)", listen)
			serveErr = httpSrv.ServeTLS(ln, "", "")
		} else {
			log.Printf("atm-ui listening on %s (http)", listen)
			serveErr = httpSrv.Serve(ln)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- serveErr
		}
	}()

	url := strings.TrimRight(publicBase, "/")
	if err := waitHTTPReady(url, 8*time.Second); err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = httpSrv.Shutdown(ctx)
		cancel()
		_ = st.Close()
		return nil, fmt.Errorf("ATM UI 未就绪: %w", err)
	}

	h := &UIHandle{URL: url, EnvPath: envPath, DeviceID: deviceID}
	h.shutdown = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		return st.Close()
	}
	select {
	case err := <-errCh:
		_ = h.Shutdown(context.Background())
		return nil, err
	default:
	}
	return h, nil
}

func forceLocalListen(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = strings.TrimPrefix(portOnly(listen), ":")
		if port == "" {
			port = "9080"
		}
	}
	return net.JoinHostPort("127.0.0.1", port)
}

func findATMEnvPath(explicit string) string {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if v := os.Getenv("FDT_ENV"); v != "" {
		candidates = append(candidates, v)
	}
	candidates = append(candidates, "/opt/fdt-atm/etc/fdt.env")
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "etc", "fdt.env"))
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "fdt.env"))
	}
	candidates = append(candidates, `C:\fdt-atm\etc\fdt.env`, "fdt.env", "/etc/fdt/fdt.env")
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			env, err := apprun.LoadEnv(p)
			if err != nil || env == nil {
				continue
			}
			if role := env["ROLE"]; role != "" && role != "atm" && p == "/etc/fdt/fdt.env" {
				continue
			}
			return p
		}
	}
	return explicit
}

func waitHTTPReady(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 800 * time.Millisecond}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode > 0 {
				return nil
			}
		} else {
			last = err
		}
		time.Sleep(150 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("timeout")
	}
	return last
}
