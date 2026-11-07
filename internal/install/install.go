// Package install provides one-command role setup: directories, keys, one env
// file, systemd unit, and role-specific key bundles for USB transfer.
package install

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"fdt/internal/crypto"
	"fdt/internal/portal"
)

// Options controls a one-shot install.
type Options struct {
	Role     string // intranet | gateway | atm
	Prefix   string // default /opt/fdt (binary) + /etc/fdt + /var/lib/fdt
	Binary   string // path to the fdt binary being installed (usually os.Executable)
	DeviceID string // ATM id when Role=intranet or atm
	Tenant   string
	Listen   string // intranet unified listen, default 0.0.0.0:8080
	Gateway  string // ATM: base URL of intranet (e.g. http://10.0.0.5:8080)
	Bundle   string // path to role bundle .tgz to import (gateway/atm)
	Tenants  string // gateway: multi-tenant JSON (installs under /etc/fdt/tenants.json)
	Domain   string // gateway: public DNS name for Let's Encrypt
	Email    string // gateway: ACME account email
	Mock     bool   // gateway: enable mock Feishu
	NoSystemd bool
	Force    bool // overwrite existing keys
}

type Layout struct {
	Etc     string
	Var     string
	Keys    string
	Outbox  string
	Bin     string
	EnvFile string
	Unit    string
}

func layout(prefix string) Layout {
	if prefix == "" {
		prefix = "/opt/fdt"
	}
	// Production default keeps FHS paths. A custom --prefix (laptop / CI)
	// keeps everything self-contained under that tree — no root required.
	etc, v := "/etc/fdt", "/var/lib/fdt"
	unit := "/etc/systemd/system/fdt.service"
	if prefix != "/opt/fdt" {
		etc = filepath.Join(prefix, "etc")
		v = filepath.Join(prefix, "var")
		unit = filepath.Join(prefix, "fdt.service")
	}
	return Layout{
		Etc: etc, Var: v,
		Keys:    filepath.Join(etc, "keys"),
		Outbox:  filepath.Join(v, "outbox"),
		Bin:     filepath.Join(prefix, "bin", "fdt"),
		EnvFile: filepath.Join(etc, "fdt.env"),
		Unit:    unit,
	}
}

// Run performs the install for Options.Role.
func Run(opt Options) error {
	opt.Role = strings.ToLower(strings.TrimSpace(opt.Role))
	if opt.Tenant == "" {
		opt.Tenant = "default"
	}
	if opt.DeviceID == "" {
		opt.DeviceID = "atm-01"
	}
	if opt.Listen == "" {
		opt.Listen = "0.0.0.0:8080"
	}
	lay := layout(opt.Prefix)

	switch opt.Role {
	case "intranet", "portal":
		return installIntranet(opt, lay)
	case "gateway", "dmz":
		return installGateway(opt, lay)
	case "atm":
		return installATM(opt, lay)
	default:
		return fmt.Errorf("install: unknown role %q (intranet|gateway|atm)", opt.Role)
	}
}

func installIntranet(opt Options, lay Layout) error {
	for _, d := range []string{lay.Etc, lay.Keys, lay.Outbox, filepath.Dir(lay.Bin),
		filepath.Join(lay.Var, "staging"), filepath.Join(lay.Var, "quarantine"),
		filepath.Join(lay.Var, "released"), filepath.Join(lay.Var, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.Chmod(lay.Keys, 0o700); err != nil {
		return err
	}
	if err := copyBinary(opt.Binary, lay.Bin); err != nil {
		return err
	}
	if err := ensureTrustKeys(lay.Keys, opt.Force); err != nil {
		return err
	}
	if err := ensureDeviceMaterial(lay.Keys, opt.DeviceID, opt.Listen, opt.Force); err != nil {
		return err
	}
	secret, err := deviceSecret(lay.Keys, opt.DeviceID)
	if err != nil {
		return err
	}
	portalAdminPass, err := randomPassword(16)
	if err != nil {
		return err
	}
	env := envMap{
		"ROLE": "intranet", "LISTEN": opt.Listen, "TENANT": opt.Tenant,
		"DATA_DIR": lay.Var, "KEYS_DIR": lay.Keys,
		"DEVICE_ID": opt.DeviceID, "DEVICE_SECRET": secret,
		"IDENTITY": "mock", "CLAMD": "", "TLS": "0",
		"LDAP_CONFIG": filepath.Join(lay.Etc, "ldap.json"),
		"ENV_FILE":    lay.EnvFile,
		"PORTAL_ADMIN_USER":     "admin",
		"PORTAL_ADMIN_PASSWORD": portalAdminPass,
		"PORTAL_ADMIN_USERS":    "", // optional: directory accounts with admin access on main portal
		"FORM_FIELDS_FILE":      filepath.Join(lay.Etc, "form-fields.json"),
	}
	_ = os.WriteFile(env["LDAP_CONFIG"], []byte("{\n  \"enabled\": false,\n  \"url\": \"\",\n  \"searchBase\": \"\"\n}\n"), 0o600)
	_ = portal.SaveFormFields(env["FORM_FIELDS_FILE"], portal.DefaultFormFields())
	if err := writeEnv(lay.EnvFile, env); err != nil {
		return err
	}
	if err := writeBundles(lay); err != nil {
		return err
	}
	if !opt.NoSystemd {
		if err := writeUnit(lay, "intranet"); err != nil {
			return err
		}
		_ = systemctl("daemon-reload")
		_ = systemctl("enable", "--now", "fdt.service")
	}
	fmt.Println()
	fmt.Println("✅ 内网一键安装完成")
	fmt.Printf("  配置: %s\n", lay.EnvFile)
	fmt.Printf("  数据: %s\n", lay.Var)
	fmt.Printf("  Portal UI: http://<本机IP>%s/  （LISTEN 可改；勿对 ATM 网段开放）\n", portOf(opt.Listen))
	fmt.Println("  ATM 数据口: 0.0.0.0:8081  （固定；仅 /data /import，无页面）")
	fmt.Printf("  管理后台: http://<本机IP>%s/admin/  用户 admin\n", portOf(opt.Listen))
	fmt.Printf("  管理密码: %s  （PORTAL_ADMIN_PASSWORD，可改 fdt.env）\n", portalAdminPass)
	fmt.Println()
	fmt.Println("用 U 盘带走这两个包（只含对方需要的密钥，文件很少）：")
	fmt.Printf("  网关机: %s\n", filepath.Join(lay.Outbox, "gateway.tgz"))
	fmt.Printf("  ATM机:  %s\n", filepath.Join(lay.Outbox, opt.DeviceID+".tgz"))
	fmt.Println()
	fmt.Println("网关机: sudo ./fdt install gateway --bundle gateway.tgz")
	fmt.Printf("ATM机:  sudo ./fdt install atm --bundle %s.tgz --server http://<内网IP>:8081\n", opt.DeviceID)
	return nil
}

func installGateway(opt Options, lay Layout) error {
	for _, d := range []string{lay.Etc, lay.Keys, filepath.Join(lay.Var, "sessions"), filepath.Dir(lay.Bin)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	_ = os.Chmod(lay.Keys, 0o700)
	if err := copyBinary(opt.Binary, lay.Bin); err != nil {
		return err
	}

	var tenantsCfg string
	var tenantIDs []string
	multi := opt.Tenants != ""
	if multi {
		var err error
		tenantsCfg, tenantIDs, err = materializeTenants(opt.Tenants, lay, opt.Mock)
		if err != nil {
			return err
		}
	} else if opt.Bundle != "" {
		if err := extractBundle(opt.Bundle, lay.Keys); err != nil {
			return err
		}
	} else if err := ensureTrustKeys(lay.Keys, opt.Force); err != nil {
		return fmt.Errorf("无 --bundle/--tenants 时需本机已有密钥，或先在内网安装后拷贝 gateway.tgz: %w", err)
	}

	tlsDir := filepath.Join(lay.Etc, "tls")
	tenantsDir := filepath.Join(lay.Etc, "tenants")
	_ = os.MkdirAll(tlsDir, 0o755)
	_ = os.MkdirAll(tenantsDir, 0o700)
	adminPass, err := randomPassword(16)
	if err != nil {
		return err
	}
	if tenantsCfg == "" {
		tenantsCfg = filepath.Join(lay.Etc, "tenants.json")
		if _, err := os.Stat(tenantsCfg); os.IsNotExist(err) {
			_ = os.WriteFile(tenantsCfg, []byte("{\n  \"releaseTTL\": \"72h\",\n  \"tenants\": []\n}\n"), 0o600)
		}
	}
	acmeCache := filepath.Join(lay.Var, "acme")
	_ = os.MkdirAll(acmeCache, 0o700)
	env := envMap{
		"ROLE": "gateway",
		"DATA_DIR": lay.Var, "KEYS_DIR": lay.Keys,
		"MOCK_FEISHU": boolStr(opt.Mock && !multi),
		"FEISHU_APP_ID": "", "FEISHU_APP_SECRET": "", "FEISHU_APPROVAL_CODE": "",
		"FEISHU_ENCRYPT_KEY": "", "FEISHU_VERIFY_TOKEN": "",
		"TLS_CERT": filepath.Join(tlsDir, "fullchain.pem"),
		"TLS_KEY":  filepath.Join(tlsDir, "privkey.pem"),
		"TENANTS_FILE": tenantsCfg,
		"TENANTS_DIR":  tenantsDir,
		"ADMIN_USER":   "admin",
		"ADMIN_PASSWORD": adminPass,
		"ACME": "0", "ACME_DOMAIN": opt.Domain, "ACME_EMAIL": opt.Email,
		"ACME_CACHE": acmeCache, "ACME_HTTP": "0.0.0.0:80",
		"ACME_CONFIG": filepath.Join(lay.Etc, "acme.json"),
		"DOMAIN_CONFIG": filepath.Join(lay.Etc, "domain.json"),
	}
	if multi {
		env["MOCK_FEISHU"] = "0"
	}
	useACME := !opt.Mock && opt.Domain != ""
	if opt.Domain != "" {
		_ = os.WriteFile(env["DOMAIN_CONFIG"], []byte(fmt.Sprintf(
			"{\n  \"domain\": %q,\n  \"email\": %q\n}\n", opt.Domain, opt.Email)), 0o600)
	}
	if opt.Mock {
		env["LISTEN"] = "0.0.0.0:8090"
		env["TLS"] = "0"
	} else if useACME {
		env["LISTEN"] = "0.0.0.0:443"
		env["TLS"] = "1"
		env["ACME"] = "1"
		_ = os.WriteFile(env["ACME_CONFIG"], []byte(fmt.Sprintf(
			"{\n  \"enabled\": true,\n  \"domain\": %q,\n  \"email\": %q,\n  \"cache\": %q,\n  \"httpAddr\": \"0.0.0.0:80\"\n}\n",
			opt.Domain, opt.Email, acmeCache)), 0o600)
	} else {
		env["LISTEN"] = "0.0.0.0:443"
		env["TLS"] = "1"
	}
	if err := writeEnv(lay.EnvFile, env); err != nil {
		return err
	}
	if !opt.NoSystemd {
		if err := writeUnit(lay, "gateway"); err != nil {
			return err
		}
		_ = systemctl("daemon-reload")
		_ = systemctl("enable", "--now", "fdt.service")
	}
	fmt.Println()
	fmt.Println("✅ 网关一键安装完成")
	fmt.Printf("  配置: %s\n", lay.EnvFile)
	fmt.Println("  运营后台: /admin/  用户 admin")
	fmt.Printf("  初始密码: %s  （登录 /admin 后可改密，或改 fdt.env 中 ADMIN_PASSWORD）\n", adminPass)
	fmt.Println("  流程: /admin 建租户发注册链接 → 客户 /register 设账号 → /console 上传 PEM/绑飞书")

	if multi {
		fmt.Printf("  多租户预置: %s → %v\n", tenantsCfg, tenantIDs)
		fmt.Println("  客户内网: sudo ./fdt install intranet --tenant <id>")
		fmt.Println("  回调 URL: https://<域名>/callback/<tenantId>")
	} else if opt.Mock {
		fmt.Println("  ⚠️ MOCK 飞书已开启（自动审批）。生产请 MOCK_FEISHU=0 并填 FEISHU_* 或走后台开户")
		fmt.Printf("  联调: http://<本机IP>:8090/admin/ 与 /app/\n")
	} else {
		fmt.Println("  单租户也可填 FEISHU_*；或只用后台添加多个租户")
	}
	if useACME {
		fmt.Printf("  Let's Encrypt: ACME=1 domain=%s（需公网 80/443 可达）\n", opt.Domain)
		fmt.Println("  首次用 https://域名 访问会自动申请证书并续期")
	} else if !opt.Mock {
		fmt.Println("  TLS: 可放手动证书到 TLS_CERT/TLS_KEY，或:")
		fmt.Println("    在 /admin → 绑定域名 → 申请 Let's Encrypt")
		fmt.Println("    或重装: sudo ./fdt install gateway --domain 你的域名 --email 你@邮箱")
		fmt.Println("  然后: systemctl restart fdt")
	}
	if opt.Domain != "" {
		fmt.Printf("  已绑定域名: https://%s （/admin 可改绑、检测 DNS）\n", opt.Domain)
	}
	fmt.Println("  飞书「网页应用」主页填 https://<域名>/app/ ，并配置 H5 可信域名（无需 nginx）")
	return nil
}

func randomPassword(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func installATM(opt Options, lay Layout) error {
	if opt.Gateway == "" {
		return fmt.Errorf("install atm: 需要 --server http://内网Portal地址")
	}
	for _, d := range []string{lay.Etc, lay.Keys, lay.Var, filepath.Dir(lay.Bin)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	_ = os.Chmod(lay.Keys, 0o700)
	if err := copyBinary(opt.Binary, lay.Bin); err != nil {
		return err
	}
	if opt.Bundle != "" {
		if err := extractBundle(opt.Bundle, lay.Keys); err != nil {
			return err
		}
	}
	secret, _ := deviceSecret(lay.Keys, opt.DeviceID)
	if secret == "" {
		secret = "dev-secret"
	}
	base := strings.TrimRight(opt.Gateway, "/")
	// Co-located with intranet (default FHS or same host): share the portal DB
	// so QR#2 redeem can Claim/Consume tokens. Separate ATM hosts should mount
	// or replicate that DB, or keep DATA_DIR local only for import-side tests.
	dataDir := lay.Var
	if opt.Prefix == "" || opt.Prefix == "/opt/fdt" || opt.Prefix == "/opt/fdt-atm" {
		if _, err := os.Stat("/var/lib/fdt/fdt.db"); err == nil {
			dataDir = "/var/lib/fdt"
		}
	}
	usbDir := filepath.Join(lay.Var, "usb") // only used when USB=mock
	_ = os.MkdirAll(usbDir, 0o755)
	_ = os.MkdirAll("/mnt/fdt-usb", 0o755)
	env := envMap{
		"ROLE": "atm", "DATA_DIR": dataDir, "KEYS_DIR": lay.Keys,
		"DEVICE_ID": opt.DeviceID, "DEVICE_SECRET": secret,
		"DATA_URL": base + "/data", "IMPORT_URL": base + "/import",
		"USB": "sysfs", "USB_MOUNT": "/mnt/fdt-usb", "USB_DIR": usbDir,
		"SCANNER": "", "CAMERA": "/dev/video0",
		"ATM_UI_LISTEN": "0.0.0.0:9080",
		"ATM_MAINT_PASSWORD": "fdt-maint",
		"ATM_MAINT_PASSWORD_FILE": filepath.Join(lay.Var, "atm_maint_password"),
		"ATM_IMPORT_FIELDS": filepath.Join(lay.Etc, "import-fields.json"),
	}
	if err := writeEnv(lay.EnvFile, env); err != nil {
		return err
	}
	if err := writeATMImportFieldsExample(filepath.Join(lay.Etc, "import-fields.json")); err != nil {
		return err
	}
	// ATM is interactive; install helper scripts instead of a long-running unit.
	binDir := filepath.Dir(lay.Bin)
	helper := filepath.Join(binDir, "fdt-atm-export")
	script := "#!/bin/sh\nexec " + lay.Bin + " atm -env " + lay.EnvFile + " \"$@\"\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		return err
	}
	uiHelper := filepath.Join(binDir, "fdt-atm-ui")
	uiScript := "#!/bin/sh\nexec " + lay.Bin + " atm -env " + lay.EnvFile + " -ui \"$@\"\n"
	if err := os.WriteFile(uiHelper, []byte(uiScript), 0o755); err != nil {
		return err
	}
	// Chromium/Chrome kiosk launcher: OS-level lock beyond in-page key blocking.
	kioskHelper := filepath.Join(binDir, "fdt-atm-kiosk")
	kioskScript := `#!/bin/sh
set -e
UI="${ATM_UI_URL:-http://127.0.0.1:9080/?kiosk=1&run=1}"
case "$UI" in
  *kiosk=*) ;;
  *\?*) UI="${UI}&kiosk=1&run=1" ;;
  */) UI="${UI}?kiosk=1&run=1" ;;
  *) UI="${UI}/?kiosk=1&run=1" ;;
esac
# start ATM API/UI if not already up
BASE=$(printf '%s' "$UI" | sed 's/[?#].*//')
if ! curl -sf -o /dev/null "$BASE"; then
  nohup "` + lay.Bin + `" atm -env "` + lay.EnvFile + `" -ui >/tmp/fdt-atm-ui.log 2>&1 &
  for i in 1 2 3 4 5 6 7 8 9 10; do
    curl -sf -o /dev/null "$BASE" && break
    sleep 0.4
  done
fi
CHROME=""
for c in google-chrome-stable google-chrome chromium-browser chromium; do
  if command -v "$c" >/dev/null 2>&1; then CHROME="$c"; break; fi
done
if [ -z "$CHROME" ]; then
  echo "未找到 Chromium/Chrome。请安装后重试。" >&2
  exit 1
fi
# Dedicated profile is required: otherwise Chrome reuses the existing session and ignores --kiosk.
PROFILE="${ATM_KIOSK_PROFILE:-${HOME}/.cache/fdt-atm-chrome-kiosk}"
mkdir -p "$PROFILE"
exec "$CHROME" --kiosk --app="$UI" --user-data-dir="$PROFILE" \
  --no-first-run --noerrdialogs --disable-session-crashed-bubble \
  --disable-infobars --disable-translate --disable-features=TranslateUI \
  --check-for-update-interval=31536000 --password-store=basic \
  --autoplay-policy=no-user-gesture-required --overscroll-history-navigation=0 \
  --disable-pinch --start-maximized "$@"
`
	if err := os.WriteFile(kioskHelper, []byte(kioskScript), 0o755); err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("✅ ATM 一键安装完成")
	fmt.Printf("  配置: %s\n", lay.EnvFile)
	fmt.Println("  USB:       sysfs 真机检测；插入后自动挂到 /mnt/fdt-usb（需权限或 udisks）")
	fmt.Println("  网页界面:  " + uiHelper + "   →  http://127.0.0.1:9080/")
	fmt.Println("  触屏锁屏:  " + kioskHelper + "   （Chromium --kiosk，配合页内锁键）")
	fmt.Println("  维护密码:  fdt-maint（可在 UI 维护模式改密，或改 fdt.env 中 ATM_MAINT_PASSWORD）")
	fmt.Println("  导出兑付:  " + helper + " -qr2 'FDT1....'")
	fmt.Println("  摄像头扫:  " + helper + " -camera /dev/video0")
	fmt.Println("  导入发起:  " + helper + " -mode import -import-phase start -reason '...'")
	return nil
}

func ensureTrustKeys(dir string, force bool) error {
	need := []string{"sk_internal.pem", "pk_internal.pem", "sk_gateway.pem", "pk_gateway.pem"}
	all := true
	for _, n := range need {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			all = false
			break
		}
	}
	if all && !force {
		return nil
	}
	for _, name := range []struct{ priv, pub string }{
		{"sk_internal.pem", "pk_internal.pem"},
		{"sk_gateway.pem", "pk_gateway.pem"},
	} {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := crypto.WritePrivatePEM(filepath.Join(dir, name.priv), kp.Priv); err != nil {
			return err
		}
		if err := crypto.WritePublicPEM(filepath.Join(dir, name.pub), kp.Pub); err != nil {
			return err
		}
	}
	return nil
}

func ensureDeviceMaterial(dir, deviceID, listen string, force bool) error {
	caPath, caKey := filepath.Join(dir, "device_ca.pem"), filepath.Join(dir, "device_ca.key")
	var ca *crypto.CA
	if _, err := os.Stat(caPath); err == nil && !force {
		ca, err = crypto.LoadCA(caPath, caKey)
		if err != nil {
			return err
		}
	} else {
		var err error
		ca, err = crypto.GenerateCA("FDT Device CA")
		if err != nil {
			return err
		}
		if err := crypto.WriteCertPEM(caPath, ca.CertPEM, caKey, ca.KeyPEM); err != nil {
			return err
		}
	}
	devCert := filepath.Join(dir, "device_"+deviceID+".pem")
	if _, err := os.Stat(devCert); err != nil || force {
		certPEM, keyPEM, _, err := ca.IssueDevice(deviceID)
		if err != nil {
			return err
		}
		if err := crypto.WriteCertPEM(devCert, certPEM, filepath.Join(dir, "device_"+deviceID+".key"), keyPEM); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "server.pem")); err != nil || force {
		host, _, _ := net.SplitHostPort(listen)
		if host == "" || host == "0.0.0.0" {
			host = "localhost"
		}
		certPEM, keyPEM, err := ca.IssueServer("fdt-intranet", []string{host, "localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
		if err != nil {
			return err
		}
		return crypto.WriteCertPEM(filepath.Join(dir, "server.pem"), certPEM, filepath.Join(dir, "server.key"), keyPEM)
	}
	return nil
}

func deviceSecret(keysDir, deviceID string) (string, error) {
	p := filepath.Join(keysDir, "device_"+deviceID+".pem")
	if _, err := os.Stat(p); err != nil {
		return "dev-secret", nil
	}
	return crypto.CertFingerprintFile(p)
}

func writeBundles(lay Layout) error {
	gw := []string{"pk_internal.pem", "sk_gateway.pem", "pk_gateway.pem"}
	if err := writeTGZ(filepath.Join(lay.Outbox, "gateway.tgz"), lay.Keys, gw); err != nil {
		return err
	}
	// Discover device_* certs
	entries, _ := os.ReadDir(lay.Keys)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "device_") || !strings.HasSuffix(name, ".pem") || strings.Contains(name, "device_ca") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "device_"), ".pem")
		files := []string{"pk_gateway.pem", "device_ca.pem", name, "device_" + id + ".key"}
		if err := writeTGZ(filepath.Join(lay.Outbox, id+".tgz"), lay.Keys, files); err != nil {
			return err
		}
	}
	return nil
}

func writeTGZ(out, root string, files []string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, name := range files {
		p := filepath.Join(root, name)
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		hdr, err := tar.FileInfoHeader(fi, "")
		if err != nil {
			return err
		}
		hdr.Name = name
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, in)
		in.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractBundle(path, dest string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Base(hdr.Name)
		if name == "." || strings.Contains(name, "..") {
			continue
		}
		out := filepath.Join(dest, name)
		mode := os.FileMode(0o600)
		if strings.HasSuffix(name, ".pem") && !strings.HasPrefix(name, "sk_") && !strings.HasSuffix(name, ".key") {
			mode = 0o644
		}
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, tr)
		w.Close()
		if err != nil {
			return err
		}
	}
}

func writeATMImportFieldsExample(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	const body = `{
  "version": 1,
  "fields": [
    {
      "key": "reason",
      "label": "事由",
      "type": "text",
      "required": true,
      "default": "导入资料",
      "order": 10,
      "enabled": true,
      "builtin": true
    },
    {
      "key": "cls",
      "label": "密级",
      "type": "select",
      "required": true,
      "options": ["internal", "confidential", "secret"],
      "default": "confidential",
      "order": 20,
      "enabled": true,
      "builtin": true
    }
  ]
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

type envMap map[string]string

func writeEnv(path string, m envMap) error {
	var b strings.Builder
	b.WriteString("# FDT one-file config — edit and: systemctl restart fdt\n")
	keys := []string{
		"ROLE", "LISTEN", "TENANT", "DATA_DIR", "KEYS_DIR", "TLS", "TLS_CERT", "TLS_KEY",
		"ACME", "ACME_DOMAIN", "ACME_EMAIL", "ACME_CACHE", "ACME_HTTP", "ACME_CONFIG", "DOMAIN_CONFIG",
		"TENANTS_FILE", "TENANTS_DIR", "ADMIN_USER", "ADMIN_PASSWORD",
		"DEVICE_ID", "DEVICE_SECRET", "IDENTITY", "CLAMD",
		"LDAP_CONFIG", "LDAP_URL", "LDAP_BASE", "LDAP_BIND_DN", "LDAP_BIND_PASSWORD",
		"LDAP_STARTTLS", "LDAP_INSECURE", "LDAP_FILTER", "ENV_FILE", "PORTAL_ADMIN_PASSWORD", "PORTAL_ADMIN_USERS",
		"MOCK_FEISHU", "FEISHU_APP_ID", "FEISHU_APP_SECRET", "FEISHU_APPROVAL_CODE",
		"FEISHU_ENCRYPT_KEY", "FEISHU_VERIFY_TOKEN",
		"DATA_URL", "IMPORT_URL", "USB", "SCANNER",
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if v, ok := m[k]; ok {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
			seen[k] = true
		}
	}
	for k, v := range m {
		if !seen[k] {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func writeUnit(lay Layout, role string) error {
	tmpl := `[Unit]
Description=FDT {{.Role}}
After=network.target

[Service]
Type=simple
EnvironmentFile={{.Env}}
ExecStart={{.Bin}} run
Restart=on-failure
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
`
	t := template.Must(template.New("u").Parse(tmpl))
	f, err := os.Create(lay.Unit)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, map[string]string{"Role": role, "Env": lay.EnvFile, "Bin": lay.Bin})
}

func copyBinary(src, dst string) error {
	if src == "" {
		var err error
		src, err = os.Executable()
		if err != nil {
			return err
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func portOf(listen string) string {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return ":8080"
	}
	return ":" + p
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// SetEnvValue updates one KEY=VAL in an existing env file (or creates a minimal file).
func SetEnvValue(path, key, value string) error {
	m, err := LoadEnv(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		m = map[string]string{}
	}
	m[key] = value
	return writeEnv(path, m)
}

// LoadEnv reads KEY=VAL lines into a map.
func LoadEnv(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}
