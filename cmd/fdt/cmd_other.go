package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/atm"
	"fdt/internal/atmui"
	"fdt/internal/crypto"
	"fdt/internal/e2e"
	"fdt/internal/importer"
	"fdt/internal/install"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
)

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	work := fs.String("work", "", "working dir")
	_ = fs.Parse(args)
	dir := *work
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "fdt-demo-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
	}
	fixture := filepath.Join(dir, "fixture")
	_ = os.MkdirAll(filepath.Join(fixture, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(fixture, "docs", "readme.txt"), []byte("hello fdt"), 0o644)
	fmt.Println("=== FDT 离线演示 ===")
	rep, err := e2e.Run(context.Background(), e2e.Config{WorkDir: dir, FixtureDir: fixture, Approver: "李四"})
	if err != nil {
		return err
	}
	for _, s := range rep.Steps {
		fmt.Println(s)
	}
	fmt.Printf("\n导出 OK req=%s merkle=%s\n", rep.ReqID, rep.MerkleRoot)
	irep, err := e2e.RunImport(context.Background(), e2e.ImportConfig{WorkDir: filepath.Join(dir, "import"), Approver: "李四"})
	if err != nil {
		return err
	}
	for _, s := range irep.Steps {
		fmt.Println(s)
	}
	fmt.Printf("导入 OK status=%s\n", irep.Status)
	return nil
}

func cmdKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	out := fs.String("out", "keys", "output dir")
	_ = fs.Parse(args)
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	for _, name := range []struct{ priv, pub string }{
		{"sk_internal.pem", "pk_internal.pem"},
		{"sk_gateway.pem", "pk_gateway.pem"},
	} {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := crypto.WritePrivatePEM(filepath.Join(*out, name.priv), kp.Priv); err != nil {
			return err
		}
		if err := crypto.WritePublicPEM(filepath.Join(*out, name.pub), kp.Pub); err != nil {
			return err
		}
		fmt.Println("wrote", name.priv, name.pub)
	}
	fmt.Println("提示: 完整一键安装请用  sudo fdt install intranet")
	return nil
}

func cmdPortal(args []string) error {
	fmt.Fprintln(os.Stderr, "推荐: sudo fdt install intranet && systemctl status fdt")
	return fmt.Errorf("请使用 fdt run（安装后）")
}

func cmdGateway(args []string) error {
	fmt.Fprintln(os.Stderr, "推荐: sudo fdt install gateway --bundle gateway.tgz")
	return fmt.Errorf("请使用 fdt run（安装后）")
}

func cmdATM(args []string) error {
	// Pre-parse -env so defaults come from the ATM install (may coexist with intranet).
	envPath := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-env" {
			envPath = args[i+1]
			break
		}
	}
	env := loadATMEnv(envPath)
	keys := envOr(env, "KEYS_DIR", "keys")
	dataDir := envOr(env, "DATA_DIR", ".")
	fs := flag.NewFlagSet("atm", flag.ExitOnError)
	_ = fs.String("env", envPath, "ATM 配置文件（默认探测 /opt/fdt-atm/etc/fdt.env）")
	db := fs.String("db", filepath.Join(dataDir, "fdt.db"), "")
	gwPub := fs.String("gateway-pub", filepath.Join(keys, "pk_gateway.pem"), "")
	deviceID := fs.String("device-id", envOr(env, "DEVICE_ID", "atm-01"), "")
	secret := fs.String("device-secret", envOr(env, "DEVICE_SECRET", "dev-secret"), "")
	dataURL := fs.String("datasvc-url", envOr(env, "DATA_URL", "http://127.0.0.1:8080/data"), "")
	impURL := fs.String("importer-url", envOr(env, "IMPORT_URL", "http://127.0.0.1:8080/import"), "")
	usbMode := fs.String("usb", envOr(env, "USB", "mock"), "")
	usbDir := fs.String("usb-dir", envOr(env, "USB_DIR", ""), "")
	qr2 := fs.String("qr2", "", "")
	scannerDev := fs.String("scanner", envOr(env, "SCANNER", ""), "串口扫码枪，如 /dev/ttyACM0")
	scannerBaud := fs.Int("scanner-baud", 9600, "")
	cameraDev := fs.String("camera", envOr(env, "CAMERA", ""), "摄像头，如 /dev/video0（需 ffmpeg）")
	ui := fs.Bool("ui", false, "启动 ATM 触屏/网页界面")
	listen := fs.String("listen", envOr(env, "ATM_UI_LISTEN", "0.0.0.0:9080"), "ATM UI 监听地址（配合 -ui）")
	maintPass := fs.String("maint-password", envOr(env, "ATM_MAINT_PASSWORD", "fdt-maint"), "维护模式密码（退出全屏用）")
	maintPassFile := fs.String("maint-password-file", envOr(env, "ATM_MAINT_PASSWORD_FILE", filepath.Join(dataDir, "atm_maint_password")), "维护密码持久化文件")
	mode := fs.String("mode", "export", "")
	phase := fs.String("import-phase", "start", "")
	reqID := fs.String("req-id", "", "")
	reason := fs.String("reason", "import", "")
	cls := fs.String("cls", "internal", "")
	qrOut := fs.String("qr-out", "qr1.png", "")
	_ = fs.Parse(args)

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
	switch *usbMode {
	case "sysfs":
		mount := envOr(env, "USB_MOUNT", "/mnt/fdt-usb")
		usb = atm.SysfsUSB{AutoMount: mount}
		_ = os.MkdirAll(mount, 0o755)
	default:
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

	if *ui {
		srv := atmui.NewWithOptions(k, atmui.Options{
			Camera:            *cameraDev,
			MaintPassword:     *maintPass,
			MaintPasswordFile: *maintPassFile,
		})
		fmt.Printf("ATM Kiosk UI: http://127.0.0.1%s/\n", portOnly(*listen))
		fmt.Printf("  监听 %s · 设备 %s · 摄像头 %s\n", *listen, *deviceID, *cameraDev)
		fmt.Printf("  维护密码文件 %s（默认密码见 ATM_MAINT_PASSWORD）\n", *maintPassFile)
		return http.ListenAndServe(*listen, srv.Handler())
	}

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
			start, err := k.StartImport(ctx, model.Applicant{ID: *deviceID, Name: *deviceID}, *reason, model.Classification(*cls), []string{"."})
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

// loadATMEnv finds the ATM role config without clobbering a co-located intranet
// /etc/fdt/fdt.env (ROLE=intranet). Prefer explicit -env, then FDT_ENV, then
// /opt/fdt-atm/etc/fdt.env, then an fdt.env next to the binary, and only then
// /etc/fdt/fdt.env when ROLE=atm.
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
		env, err := install.LoadEnv(p)
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

// readATMFrame preference: -qr2 > -camera > -scanner > stdin paste.
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
		fmt.Printf("等待扫码枪（%s）…\n", scanner)
		return sc.Read(ctx)
	}
	fmt.Print("扫描 QR#2（粘贴令牌后回车）: ")
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(make([]byte, 1<<20), 1<<20)
	if s.Scan() {
		return strings.TrimSpace(s.Text()), nil
	}
	return "", fmt.Errorf("需要 -qr2 / -camera / -scanner 或粘贴令牌")
}
