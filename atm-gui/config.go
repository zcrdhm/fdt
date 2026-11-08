package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"fdt/internal/apprun"
)

// userATMRoot is a per-user data directory (no admin/root required).
// Override with FDT_ATM_HOME.
func userATMRoot() string {
	if v := strings.TrimSpace(os.Getenv("FDT_ATM_HOME")); v != "" {
		return v
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = os.TempDir()
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "FDT-ATM")
}

func defaultEnvPath() string {
	return filepath.Join(userATMRoot(), "fdt.env")
}

// ATMConfig is the first-run / persisted ATM GUI settings.
type ATMConfig struct {
	PortalBase   string // e.g. http://192.168.0.135:8081
	DeviceID     string
	DeviceSecret string
	GatewayPub   string // PEM text of pk_gateway.pem
	MaintPass    string // initial only; stored as bcrypt file
	USB          string // windows|sysfs|mock
	USBDir       string // for mock
}

func (c ATMConfig) DataURL() string {
	return strings.TrimRight(strings.TrimSpace(c.PortalBase), "/") + "/data"
}

func (c ATMConfig) ImportURL() string {
	return strings.TrimRight(strings.TrimSpace(c.PortalBase), "/") + "/import"
}

func (c ATMConfig) Validate() error {
	if strings.TrimSpace(c.PortalBase) == "" {
		return fmt.Errorf("请填写 Portal 地址（含端口，如 http://10.0.0.5:8081）")
	}
	if !strings.HasPrefix(c.PortalBase, "http://") && !strings.HasPrefix(c.PortalBase, "https://") {
		return fmt.Errorf("Portal 地址需以 http:// 或 https:// 开头")
	}
	if strings.TrimSpace(c.DeviceID) == "" {
		return fmt.Errorf("请填写设备 ID")
	}
	if strings.TrimSpace(c.DeviceSecret) == "" {
		return fmt.Errorf("请填写设备密钥 DEVICE_SECRET")
	}
	pem := strings.TrimSpace(c.GatewayPub)
	if pem == "" || !strings.Contains(pem, "BEGIN") {
		return fmt.Errorf("请粘贴 pk_gateway.pem 公钥内容")
	}
	if strings.TrimSpace(c.MaintPass) == "" {
		return fmt.Errorf("请设置维护密码")
	}
	if len(strings.TrimSpace(c.MaintPass)) < 4 {
		return fmt.Errorf("维护密码至少 4 位")
	}
	return nil
}

func ensureLayout() (root, envPath, keysDir, dataDir, maintFile, importFields string, err error) {
	root = userATMRoot()
	envPath = defaultEnvPath()
	keysDir = filepath.Join(root, "keys")
	dataDir = filepath.Join(root, "var")
	maintFile = filepath.Join(dataDir, "atm_maint_password")
	importFields = filepath.Join(root, "import-fields.json")
	for _, d := range []string{root, keysDir, dataDir} {
		if err = os.MkdirAll(d, 0o755); err != nil {
			return "", "", "", "", "", "", fmt.Errorf("创建用户目录 %s: %w（请确认当前用户有写权限）", d, err)
		}
	}
	return root, envPath, keysDir, dataDir, maintFile, importFields, nil
}

// configReady reports whether a usable fdt.env + gateway pubkey exist.
func configReady(envPath string) bool {
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	env, err := apprun.LoadEnv(envPath)
	if err != nil || env == nil {
		return false
	}
	if env["DATA_URL"] == "" || env["IMPORT_URL"] == "" {
		return false
	}
	if env["DEVICE_ID"] == "" || env["DEVICE_SECRET"] == "" {
		return false
	}
	keys := apprun.EnvOr(env, "KEYS_DIR", filepath.Join(filepath.Dir(envPath), "keys"))
	if _, err := os.Stat(filepath.Join(keys, "pk_gateway.pem")); err != nil {
		return false
	}
	return true
}

func saveATMConfig(c ATMConfig) (envPath string, err error) {
	if err = c.Validate(); err != nil {
		return "", err
	}
	root, envPath, keysDir, dataDir, maintFile, importFields, err := ensureLayout()
	if err != nil {
		return "", err
	}
	_ = root

	usb := strings.TrimSpace(c.USB)
	if usb == "" {
		usb = defaultUSBForUser()
	}
	usbDir := strings.TrimSpace(c.USBDir)
	if usb == "mock" && usbDir == "" {
		usbDir = filepath.Join(dataDir, "usb")
		_ = os.MkdirAll(usbDir, 0o755)
	}

	pubPath := filepath.Join(keysDir, "pk_gateway.pem")
	pem := strings.TrimSpace(c.GatewayPub) + "\n"
	if err := os.WriteFile(pubPath, []byte(pem), 0o644); err != nil {
		return "", fmt.Errorf("写入公钥: %w", err)
	}

	// Seed maint password file as plaintext once; atmui migrates to bcrypt on StartUI.
	if err := os.WriteFile(maintFile, []byte(strings.TrimSpace(c.MaintPass)+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入维护密码文件: %w", err)
	}

	if _, err := os.Stat(importFields); err != nil {
		_ = os.WriteFile(importFields, []byte(defaultImportFieldsJSON), 0o644)
	}

	lines := []string{
		"# FDT ATM GUI — 用户目录配置（无需管理员权限）",
		"ROLE=atm",
		"DATA_DIR=" + dataDir,
		"KEYS_DIR=" + keysDir,
		"DEVICE_ID=" + strings.TrimSpace(c.DeviceID),
		"DEVICE_SECRET=" + strings.TrimSpace(c.DeviceSecret),
		"DATA_URL=" + c.DataURL(),
		"IMPORT_URL=" + c.ImportURL(),
		"USB=" + usb,
		"ATM_UI_LISTEN=127.0.0.1:9080",
		"ATM_UI_TLS=0",
		"ATM_MAINT_PASSWORD_FILE=" + maintFile,
		"ATM_IMPORT_FIELDS=" + importFields,
		"# 维护密码仅存哈希文件；请在 GUI 维护模式修改密码",
	}
	if usbDir != "" {
		lines = append(lines, "USB_DIR="+usbDir)
	}
	body := strings.Join(lines, "\n") + "\n"
	tmp := envPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, envPath); err != nil {
		return "", err
	}
	return envPath, nil
}

func defaultUSBForUser() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "linux":
		// sysfs auto-mount often needs root; non-root GUI defaults to mock folder.
		if runningAsRoot() {
			return "sysfs"
		}
		return "mock"
	default:
		return "mock"
	}
}

func loadConfigForForm(envPath string) ATMConfig {
	c := ATMConfig{
		DeviceID: "atm-01",
		USB:      defaultUSBForUser(),
	}
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	env, err := apprun.LoadEnv(envPath)
	if err != nil {
		return c
	}
	c.DeviceID = apprun.EnvOr(env, "DEVICE_ID", c.DeviceID)
	c.DeviceSecret = env["DEVICE_SECRET"]
	c.USB = apprun.EnvOr(env, "USB", c.USB)
	c.USBDir = env["USB_DIR"]
	if u := env["DATA_URL"]; strings.HasSuffix(u, "/data") {
		c.PortalBase = strings.TrimSuffix(u, "/data")
	}
	keys := apprun.EnvOr(env, "KEYS_DIR", filepath.Join(filepath.Dir(envPath), "keys"))
	if b, err := os.ReadFile(filepath.Join(keys, "pk_gateway.pem")); err == nil {
		c.GatewayPub = string(b)
	}
	return c
}

const defaultImportFieldsJSON = `{
  "version": 1,
  "fields": [
    {"key": "reason", "label": "事由", "type": "text", "required": true, "default": "导入资料", "order": 10, "enabled": true, "builtin": true},
    {"key": "cls", "label": "密级", "type": "select", "required": true, "options": ["internal", "confidential", "secret"], "default": "confidential", "order": 20, "enabled": true, "builtin": true}
  ]
}
`
