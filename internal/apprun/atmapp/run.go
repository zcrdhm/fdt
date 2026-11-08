package atmapp

import (
	"bufio"
	"context"
	"fdt/internal/apprun"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/atm"
	"fdt/internal/crypto"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
)

// RunATM starts the ATM UI or CLI. Args are flag-style (no leading "atm" word).
func RunATM(args []string) error {
	envPath := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-env" {
			envPath = args[i+1]
			break
		}
	}
	env := loadATMEnv(envPath)
	keys := apprun.EnvOr(env, "KEYS_DIR", "keys")
	dataDir := apprun.EnvOr(env, "DATA_DIR", ".")
	fs := flag.NewFlagSet("atm", flag.ExitOnError)
	_ = fs.String("env", envPath, "ATM 配置文件（默认探测 /opt/fdt-atm/etc/fdt.env）")
	db := fs.String("db", filepath.Join(dataDir, "fdt.db"), "")
	gwPub := fs.String("gateway-pub", filepath.Join(keys, "pk_gateway.pem"), "")
	deviceID := fs.String("device-id", apprun.EnvOr(env, "DEVICE_ID", "atm-01"), "")
	secret := fs.String("device-secret", apprun.EnvOr(env, "DEVICE_SECRET", "dev-secret"), "")
	dataURL := fs.String("datasvc-url", apprun.EnvOr(env, "DATA_URL", "http://127.0.0.1:8081/data"), "Portal ATM 数据口（固定 :8081/data）")
	impURL := fs.String("importer-url", apprun.EnvOr(env, "IMPORT_URL", "http://127.0.0.1:8081/import"), "Portal ATM 导入口（固定 :8081/import）")
	usbMode := fs.String("usb", apprun.EnvOr(env, "USB", defaultUSBMode()), "usb 检测: windows|sysfs|mock")
	usbDir := fs.String("usb-dir", apprun.EnvOr(env, "USB_DIR", ""), "")
	qr2 := fs.String("qr2", "", "")
	scannerDev := fs.String("scanner", apprun.EnvOr(env, "SCANNER", ""), "串口扫码枪，如 /dev/ttyACM0")
	scannerBaud := fs.Int("scanner-baud", 9600, "")
	cameraDev := fs.String("camera", apprun.EnvOr(env, "CAMERA", ""), "摄像头，如 /dev/video0（需 ffmpeg）")
	ui := fs.Bool("ui", false, "启动 ATM 触屏/网页界面")
	listen := fs.String("listen", apprun.EnvOr(env, "ATM_UI_LISTEN", "0.0.0.0:9080"), "ATM UI 监听（可改 0.0.0.0:80 / :443）")
	_ = fs.Bool("ui-tls", apprun.EnvTruthy(env, "ATM_UI_TLS") || apprun.EnvTruthy(env, "TLS"), "ATM UI 启用 HTTPS")
	_ = fs.String("ui-tls-cert", apprun.FirstNonEmpty(env["ATM_UI_TLS_CERT"], env["TLS_CERT"], filepath.Join(keys, "server.pem")), "ATM UI TLS 证书 PEM")
	_ = fs.String("ui-tls-key", apprun.FirstNonEmpty(env["ATM_UI_TLS_KEY"], env["TLS_KEY"], filepath.Join(keys, "server.key")), "ATM UI TLS 私钥 PEM")
	_ = fs.String("ui-url", apprun.EnvOr(env, "ATM_UI_URL", ""), "ATM UI 对外 origin；空则按 listen/TLS 推导")
	_ = fs.String("maint-password", apprun.EnvOr(env, "ATM_MAINT_PASSWORD", "fdt-maint"), "维护模式密码")
	_ = fs.String("maint-password-file", apprun.EnvOr(env, "ATM_MAINT_PASSWORD_FILE", filepath.Join(dataDir, "atm_maint_password")), "维护密码持久化文件")
	defaultImportFields := "/opt/fdt-atm/etc/import-fields.json"
	if envPath != "" {
		defaultImportFields = filepath.Join(filepath.Dir(envPath), "import-fields.json")
	}
	_ = fs.String("import-fields", apprun.EnvOr(env, "ATM_IMPORT_FIELDS", defaultImportFields), "ATM 导入表单 JSON")
	mode := fs.String("mode", "export", "")
	phase := fs.String("import-phase", "start", "")
	reqID := fs.String("req-id", "", "")
	reason := fs.String("reason", "import", "")
	cls := fs.String("cls", "internal", "")
	qrOut := fs.String("qr-out", "qr1.png", "")
	_ = fs.Parse(args)

	if *ui {
		h, err := StartUI(UIOptions{EnvPath: envPath, Listen: *listen})
		if err != nil {
			return err
		}
		fmt.Printf("ATM Kiosk UI: %s/\n", h.URL)
		fmt.Printf("  设备 %s · Portal %s\n", h.DeviceID, *dataURL)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		return h.Shutdown(context.Background())
	}

	pub, err := crypto.ReadPublicPEM(*gwPub)
	if err != nil {
		return err
	}
	st, err := store.OpenSQLite(*db)
	if err != nil {
		return err
	}
	defer st.Close()

	var usb atm.Manager
	switch strings.ToLower(*usbMode) {
	case "sysfs":
		mount := apprun.EnvOr(env, "USB_MOUNT", "/mnt/fdt-usb")
		usb = atm.SysfsUSB{AutoMount: mount}
		_ = os.MkdirAll(mount, 0o755)
	case "windows":
		usb = atm.WindowsUSB{}
	default: // mock
		m := atm.NewMockManager()
		if *usbDir != "" {
			_ = m.Insert("USB-DEV", *usbDir, 1<<30, true, "exfat")
		}
		usb = m
	}
	k := atm.NewKiosk(*deviceID, st, pub, nil, usb)
	k.Fetcher = &atm.HTTPFetcher{BaseURL: *dataURL, DeviceID: *deviceID, Secret: *secret}
	k.Importer = &importer.HTTPClient{BaseURL: *impURL, DeviceID: *deviceID, Secret: *secret}
	k.ImportDLP = manifest.ContentEngine{}

	ctx := context.Background()
	switch *mode {
	case "export":
		frame, err := readATMFrame(*qr2, *cameraDev, *scannerDev, *scannerBaud)
		if err != nil {
			return err
		}
		res, err := k.ProcessScan(ctx, frame)
		if err != nil {
			return err
		}
		fmt.Printf("OK %s -> %s (%d bytes)\n", res.ReqID, res.OutDir, res.WrittenBytes)
	case "import":
		if *phase == "start" {
			start, err := k.StartImport(ctx, model.Applicant{ID: *deviceID, Name: *deviceID}, *reason, model.Classification(*cls), []string{"."}, nil)
			if err != nil {
				return err
			}
			if png, err := qrcode.Encode(start.QR1Frame, qrcode.Low, 512); err == nil {
				_ = os.WriteFile(*qrOut, png, 0o644)
			}
			fmt.Printf("reqId=%s\n%s\n", start.ReqID, start.QR1Frame)
		} else {
			if *reqID == "" {
				return fmt.Errorf("complete 需要 -req-id")
			}
			frame, err := readATMFrame(*qr2, *cameraDev, *scannerDev, *scannerBaud)
			if err != nil {
				return err
			}
			res, err := k.CompleteImport(ctx, *reqID, frame)
			if err != nil {
				return err
			}
			fmt.Printf("released %s -> %s\n", res.ReqID, res.ReleasedDir)
		}
	default:
		return fmt.Errorf("unknown mode")
	}
	return nil
}

func portOnly(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i:]
	}
	return ":9080"
}

func deriveATMUIPublicBase(listen string, https bool) string {
	scheme := "http"
	if https {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = strings.TrimPrefix(portOnly(listen), ":")
		if port == "" {
			port = "9080"
		}
		host = "127.0.0.1"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	if (https && port == "443") || (!https && port == "80") {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + port
}

func loadATMEnv(explicit string) map[string]string {
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
	}
	candidates = append(candidates, "fdt.env", "/etc/fdt/fdt.env")
	for _, p := range candidates {
		env, err := apprun.LoadEnv(p)
		if err != nil || env == nil {
			continue
		}
		if role := env["ROLE"]; role != "" && role != "atm" && p == "/etc/fdt/fdt.env" {
			continue
		}
		return env
	}
	return map[string]string{}
}

func readATMFrame(qr2, camera, scanner string, baud int) (string, error) {
	if qr2 != "" {
		return qr2, nil
	}
	if camera != "" {
		sc, err := atm.OpenCamera(camera)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fmt.Printf("请将 QR#2 对准摄像头（%s）…\n", camera)
		return sc.Read(ctx)
	}
	if scanner != "" {
		sc, err := atm.OpenSerial(scanner, baud)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fmt.Printf("请扫描 QR#2（%s）…\n", scanner)
		return sc.Read(ctx)
	}
	fmt.Print("粘贴 QR#2 帧后回车: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func defaultUSBMode() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "linux":
		return "sysfs"
	default:
		return "mock"
	}
}
