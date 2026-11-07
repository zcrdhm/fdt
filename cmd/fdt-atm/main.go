// Command fdt-atm is the ferry terminal kiosk harness.
//
//	export (default): scan QR#2 -> verify -> pull data -> write USB -> consume
//	import: phase=start|complete
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"fdt/internal/atm"
	"fdt/internal/crypto"
	"fdt/internal/deviceauth"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
)

func main() {
	db := flag.String("db", "fdt.db", "SQLite database path (shared with the intranet)")
	driver := flag.String("db-driver", "sqlite", "database driver")
	gwPub := flag.String("gateway-pub", "keys/pk_gateway.pem", "PK_网关 PEM (verify QR#2)")
	deviceID := flag.String("device-id", "atm-01", "this ATM's registered device id")
	deviceSecret := flag.String("device-secret", "", "device credential (dev bearer)")
	atmName := flag.String("name", "atm-01", "ATM identifier for audit")
	usbDir := flag.String("usb-dir", "", "mock USB mount dir; ignored when -usb=sysfs")
	usbSerial := flag.String("usb-serial", "USB-SER-DEV", "simulated USB serial")
	usbCapacity := flag.Int64("usb-capacity", 1<<30, "simulated USB capacity bytes")
	usbMode := flag.String("usb", "mock", "USB manager: mock | sysfs")
	scannerDev := flag.String("scanner", "", "serial scanner device path (empty = camera / stdin / -qr2)")
	scannerBaud := flag.Int("scanner-baud", 9600, "serial scanner baud")
	cameraDev := flag.String("camera", "", "V4L2 camera path for QR scan (e.g. /dev/video0; needs ffmpeg)")
	usbPorts := flag.String("kiosk-usb-ports", "", "comma-separated USB ports to authorize (enables lockdown)")
	allowCIDRs := flag.String("kiosk-allow-cidrs", "", "comma-separated CIDRs for nft rules printout")
	applyNFT := flag.Bool("kiosk-apply-nft", false, "print nft rules to stdout (does not apply)")

	mode := flag.String("mode", "export", "export | import")
	dataURL := flag.String("datasvc-url", "http://127.0.0.1:8081", "intranet data service base URL")
	qr2 := flag.String("qr2", "", "QR#2 frame (default: camera / scanner / stdin)")

	importerURL := flag.String("importer-url", "http://127.0.0.1:8082", "intranet importer base URL")
	phase := flag.String("import-phase", "start", "import phase: start | complete")
	reqID := flag.String("req-id", "", "import request id (complete phase)")
	qrOut := flag.String("qr-out", "qr1.png", "where to write the QR#1 PNG (start phase)")
	appID := flag.String("applicant-id", "", "applicant employee id")
	appName := flag.String("applicant-name", "", "applicant name")
	appDept := flag.String("applicant-dept", "", "applicant dept")
	reason := flag.String("reason", "", "import reason")
	cls := flag.String("cls", "internal", "classification")

	clientCert := flag.String("client-cert", "", "ATM client certificate PEM (mTLS)")
	clientKey := flag.String("client-key", "", "ATM client key PEM")
	caFile := flag.String("ca", "", "intranet CA PEM to trust")
	flag.Parse()

	if *usbPorts != "" {
		ports := splitCSV(*usbPorts)
		if err := (atm.Lockdown{DataPorts: ports}).Apply(); err != nil {
			log.Fatalf("kiosk lockdown: %v", err)
		}
		log.Printf("USB lockdown applied for ports %v", ports)
	}
	if *allowCIDRs != "" {
		rules, err := atm.NFTRules(splitCSV(*allowCIDRs))
		if err != nil {
			log.Fatalf("nft rules: %v", err)
		}
		if *applyNFT {
			fmt.Print(rules)
		} else {
			log.Printf("kiosk nft rules (pass -kiosk-apply-nft to print):\n%s", rules)
		}
	}

	pub, err := crypto.ReadPublicPEM(*gwPub)
	if err != nil {
		log.Fatalf("load PK_网关: %v", err)
	}
	st, err := store.Open(*driver, dsn(*driver, *db))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	usbMgr, err := buildUSB(*usbMode, *usbDir, *usbSerial, *usbCapacity)
	if err != nil {
		log.Fatalf("usb: %v", err)
	}

	httpClient := http.DefaultClient
	if *clientCert != "" {
		c, err := deviceauth.NewHTTPClient(*clientCert, *clientKey, *caFile)
		if err != nil {
			log.Fatalf("mTLS client: %v", err)
		}
		httpClient = c
		log.Println("ATM mTLS 客户端已启用")
	}

	ctx := context.Background()
	scanOpt := scanOptions{qr2: *qr2, scannerDev: *scannerDev, baud: *scannerBaud, camera: *cameraDev}
	switch *mode {
	case "export":
		runExport(ctx, st, pub, usbMgr, *atmName, *deviceID, *deviceSecret, *dataURL, scanOpt, httpClient)
	case "import":
		runImport(ctx, st, pub, usbMgr, *atmName, *deviceID, *deviceSecret, *importerURL,
			*phase, *reqID, *qrOut, scanOpt, httpClient,
			model.Applicant{ID: *appID, Name: *appName, Dept: *appDept}, *reason, model.Classification(*cls))
	default:
		log.Fatalf("unknown -mode %q (export|import)", *mode)
	}
}

type scanOptions struct {
	qr2, scannerDev, camera string
	baud                    int
}

func buildUSB(mode, dir, serial string, capacity int64) (atm.Manager, error) {
	switch mode {
	case "sysfs":
		log.Println("USB: SysfsUSB（仅 Mass Storage，拒绝 HID 复合设备）")
		return atm.SysfsUSB{}, nil
	case "mock":
		m := atm.NewMockManager()
		if dir != "" {
			if err := m.Insert(serial, dir, capacity, true, "exfat"); err != nil {
				return nil, err
			}
			fmt.Printf("已插入U盘 %s -> %s\n", serial, dir)
		} else {
			fmt.Println("未插入U盘（操作将被拒绝）")
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown -usb %q (mock|sysfs)", mode)
	}
}

func newKiosk(st *store.Store, pub ed25519.PublicKey, usbMgr atm.Manager, name string) *atm.Kiosk {
	k := atm.NewKiosk(name, st, pub, nil, usbMgr)
	k.Progress = func(p atm.WriteProgress) {
		if p.Total > 0 {
			name := p.FileName
			if name == "" {
				name = "…"
			}
			fmt.Printf("\r写入中 %s  %d/%d 字节 (%d%%)   ", name, p.Written, p.Total, p.Written*100/p.Total)
		}
	}
	return k
}

func readFrame(opt scanOptions) (string, error) {
	if opt.qr2 != "" {
		return opt.qr2, nil
	}
	if opt.camera != "" {
		sc, err := atm.OpenCamera(opt.camera)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fmt.Printf("请将 QR#2 对准摄像头（%s）…\n", opt.camera)
		return sc.Read(ctx)
	}
	if opt.scannerDev != "" {
		sc, err := atm.OpenSerial(opt.scannerDev, opt.baud)
		if err != nil {
			return "", err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		fmt.Printf("等待扫码枪输入（%s）…\n", opt.scannerDev)
		return sc.Read(ctx)
	}
	return promptLine("扫描 QR#2（粘贴令牌后回车）: "), nil
}

func runExport(ctx context.Context, st *store.Store, pub ed25519.PublicKey, usbMgr atm.Manager,
	name, deviceID, secret, dataURL string, scanOpt scanOptions, httpClient *http.Client) {
	kiosk := newKiosk(st, pub, usbMgr, name)
	kiosk.Fetcher = &atm.HTTPFetcher{BaseURL: dataURL, DeviceID: deviceID, Secret: secret, Client: httpClient}

	frame, err := readFrame(scanOpt)
	if err != nil {
		log.Fatalf("scan: %v", err)
	}
	if frame == "" {
		log.Fatal("未提供 QR#2")
	}
	fmt.Printf("当前状态: %s\n", kiosk.State())
	res, err := kiosk.ProcessScan(ctx, frame)
	if err != nil {
		fmt.Printf("\n兑付失败 [状态=%s]: %v\n", kiosk.State(), err)
		os.Exit(1)
	}
	fmt.Printf("\n✅ 导出兑付成功，可安全拔出U盘\n")
	fmt.Printf("申请单: %s\nU盘: %s\n写入: %d 字节 / %d 文件\nMerkle: %s\n飞书审批: %s\n输出目录: %s\n",
		res.ReqID, res.USBSerial, res.WrittenBytes, res.FileCount, res.MerkleRoot, res.FsApprovalID, res.OutDir)
}

func runImport(ctx context.Context, st *store.Store, pub ed25519.PublicKey, usbMgr atm.Manager,
	name, deviceID, secret, importerURL, phase, reqID, qrOut string, scanOpt scanOptions, httpClient *http.Client,
	applicant model.Applicant, reason string, cls model.Classification) {
	kiosk := newKiosk(st, pub, usbMgr, name)
	kiosk.Importer = &importer.HTTPClient{BaseURL: importerURL, DeviceID: deviceID, Secret: secret, HTTP: httpClient}
	kiosk.ImportDLP = manifest.ContentEngine{RuleBasedDLP: manifest.RuleBasedDLP{}}

	switch phase {
	case "start":
		start, err := kiosk.StartImport(ctx, applicant, reason, cls, []string{"."}, nil)
		if err != nil {
			fmt.Printf("导入发起失败 [状态=%s]: %v\n", kiosk.State(), err)
			os.Exit(1)
		}
		if png, err := qrcode.Encode(start.QR1Frame, qrcode.Low, 512); err == nil {
			if err := os.WriteFile(qrOut, png, 0o644); err == nil {
				fmt.Printf("QR#1 图片已写入: %s\n", qrOut)
			}
		}
		fmt.Printf("✅ 已索引U盘并创建导入申请\n申请单: %s\n文件数: %d\nMerkle根: %s\n\nQR#1 令牌:\n%s\n",
			start.ReqID, start.Count, start.MerkleRoot, start.QR1Frame)
		fmt.Println("\n用飞书网页应用（网关 /app/）扫上面的二维码发起审批；审批通过后用 -import-phase complete 完成导入。")

	case "complete":
		if reqID == "" {
			log.Fatal("complete 阶段需要 -req-id")
		}
		frame, err := readFrame(scanOpt)
		if err != nil {
			log.Fatalf("scan: %v", err)
		}
		if frame == "" {
			log.Fatal("未提供 QR#2")
		}
		res, err := kiosk.CompleteImport(ctx, reqID, frame)
		if err != nil {
			status := "FAILED"
			if res != nil {
				status = string(res.Status)
			}
			fmt.Printf("\n导入未完成 [%s]: %v\n", status, err)
			if res != nil && len(res.Findings) > 0 {
				fmt.Printf("扫描命中: %v\n", res.Findings)
			}
			os.Exit(1)
		}
		fmt.Printf("\n✅ 导入完成，已释放到内网\n申请单: %s\n状态: %s\n目标目录: %s\n释放文件数: %d\nMerkle: %s\n",
			res.ReqID, res.Status, res.ReleasedDir, len(res.Released), res.MerkleRoot)

	default:
		log.Fatalf("unknown -import-phase %q (start|complete)", phase)
	}
}

func promptLine(prompt string) string {
	fmt.Print(prompt)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dsn(driver, db string) string {
	if driver == "sqlite" {
		if db == ":memory:" {
			return "file::memory:"
		}
		return "file:" + db + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	}
	return db
}
