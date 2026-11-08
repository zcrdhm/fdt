package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"fdt/internal/apprun"
	"fdt/internal/atmui"
	"fdt/internal/crypto"
	"fdt/internal/portal"
)

// userPortalRoot is a per-user data directory (no admin/root required).
// Override with FDT_PORTAL_HOME.
func userPortalRoot() string {
	if v := strings.TrimSpace(os.Getenv("FDT_PORTAL_HOME")); v != "" {
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
	return filepath.Join(base, "FDT-Portal")
}

func defaultEnvPath() string {
	return filepath.Join(userPortalRoot(), "fdt.env")
}

// PortalConfig is first-run / persisted Portal GUI settings.
type PortalConfig struct {
	Listen           string `json:"listen"`
	DataListen       string `json:"dataListen"`
	Domain           string `json:"domain"`
	PublicBase       string `json:"publicBase"`
	Tenant           string `json:"tenant"`
	HTTPS            bool   `json:"https"`
	TLSCertPEM       string `json:"tlsCertPEM"`
	TLSKeyPEM        string `json:"tlsKeyPEM"`
	DataDir          string `json:"dataDir"`
	KeysDir          string `json:"keysDir"`
	AdminUser        string `json:"adminUser"`
	AdminPass        string `json:"adminPass"`
	AdminUsers       string `json:"adminUsers"`
	Identity         string `json:"identity"`
	DeviceID         string `json:"deviceId"`
	DeviceSecret     string `json:"deviceSecret"`
	Clamd            string `json:"clamd"`
	FormFieldsJSON   string `json:"formFieldsJSON"`
	ImportFieldsJSON string `json:"importFieldsJSON"`
	GenKeys          bool   `json:"genKeys"`
	ExportPKInternal string `json:"exportPKInternal"`
	InternalPubPEM   string `json:"internalPubPEM"`
	InternalPrivPEM  string `json:"internalPrivPEM"`
	GatewayPubPEM    string `json:"gatewayPubPEM"`
	GatewayPrivPEM   string `json:"gatewayPrivPEM"`
}

func (c *PortalConfig) applyDefaults() {
	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = "0.0.0.0:8080"
	}
	if strings.TrimSpace(c.DataListen) == "" {
		c.DataListen = "0.0.0.0:8081"
	}
	if strings.TrimSpace(c.Tenant) == "" {
		c.Tenant = "default"
	}
	if strings.TrimSpace(c.AdminUser) == "" {
		c.AdminUser = "admin"
	}
	if strings.TrimSpace(c.Identity) == "" {
		c.Identity = "mock"
	}
	if strings.TrimSpace(c.DeviceID) == "" {
		c.DeviceID = "atm-01"
	}
}

func (c PortalConfig) Validate() error {
	c.applyDefaults()
	if _, _, err := splitListen(c.Listen); err != nil {
		return fmt.Errorf("Portal 监听地址无效: %w", err)
	}
	if _, _, err := splitListen(c.DataListen); err != nil {
		return fmt.Errorf("ATM 数据口地址无效: %w", err)
	}
	_, pUI, _ := splitListen(c.Listen)
	_, pData, _ := splitListen(c.DataListen)
	if pUI == pData {
		return fmt.Errorf("Portal 端口与 ATM 数据口不能相同（%s）", pUI)
	}
	if strings.TrimSpace(c.AdminPass) == "" {
		return fmt.Errorf("请设置 Portal 管理密码")
	}
	if len(strings.TrimSpace(c.AdminPass)) < 4 {
		return fmt.Errorf("管理密码至少 4 位")
	}
	switch strings.ToLower(strings.TrimSpace(c.Identity)) {
	case "mock", "ldap", "pam", "none":
	default:
		return fmt.Errorf("IDENTITY 须为 mock / ldap / pam / none")
	}
	if c.HTTPS {
		hasFiles := false // checked later against keys dir after write
		_ = hasFiles
		if (strings.TrimSpace(c.TLSCertPEM) == "") != (strings.TrimSpace(c.TLSKeyPEM) == "") {
			return fmt.Errorf("HTTPS：证书与私钥需成对粘贴，或都留空并事先放入 keys/server.pem + server.key")
		}
	}
	if c.PublicBase != "" {
		b := strings.TrimSpace(c.PublicBase)
		if !strings.HasPrefix(b, "http://") && !strings.HasPrefix(b, "https://") {
			return fmt.Errorf("PUBLIC_BASE 需以 http:// 或 https:// 开头")
		}
	}
	return nil
}

func splitListen(addr string) (host, port string, err error) {
	return netSplitHostPort(addr)
}

func ensureLayout() (root, envPath, keysDir, dataDir, formFields, importFields string, err error) {
	root = userPortalRoot()
	envPath = defaultEnvPath()
	keysDir = filepath.Join(root, "keys")
	dataDir = filepath.Join(root, "var")
	formFields = filepath.Join(root, "form-fields.json")
	importFields = filepath.Join(root, "import-fields.json")
	for _, d := range []string{root, keysDir, dataDir} {
		if err = os.MkdirAll(d, 0o755); err != nil {
			return "", "", "", "", "", "", fmt.Errorf("创建目录 %s: %w", d, err)
		}
	}
	return root, envPath, keysDir, dataDir, formFields, importFields, nil
}

func configReady(envPath string) bool {
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	env, err := apprun.LoadEnv(envPath)
	if err != nil || env == nil {
		return false
	}
	if env["ROLE"] != "" && env["ROLE"] != "intranet" && env["ROLE"] != "portal" {
		return false
	}
	if env["PORTAL_ADMIN_PASSWORD"] == "" {
		return false
	}
	keys := apprun.EnvOr(env, "KEYS_DIR", filepath.Join(filepath.Dir(envPath), "keys"))
	if _, err := os.Stat(filepath.Join(keys, "sk_internal.pem")); err != nil {
		return false
	}
	return true
}

func savePortalConfig(c PortalConfig) (envPath string, err error) {
	c.applyDefaults()
	if err = c.Validate(); err != nil {
		return "", err
	}
	root, envPath, keysDir, dataDir, formFieldsPath, importFieldsPath, err := ensureLayout()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(c.DataDir) != "" {
		dataDir = strings.TrimSpace(c.DataDir)
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return "", fmt.Errorf("DATA_DIR: %w", err)
		}
	}
	if strings.TrimSpace(c.KeysDir) != "" {
		keysDir = strings.TrimSpace(c.KeysDir)
		if err := os.MkdirAll(keysDir, 0o755); err != nil {
			return "", fmt.Errorf("KEYS_DIR: %w", err)
		}
	}
	_ = root

	if err := ensurePortalKeys(keysDir, c); err != nil {
		return "", err
	}
	if c.HTTPS {
		if err := writeOptionalTLS(keysDir, c); err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(keysDir, "server.pem")); err != nil {
			if c.TLSCertPEM == "" {
				return "", fmt.Errorf("HTTPS=1 需要 keys/server.pem + server.key（可粘贴证书或事先放入）")
			}
		}
	}

	if err := writeFormFieldsFile(formFieldsPath, c.FormFieldsJSON); err != nil {
		return "", err
	}
	if err := writeATMImportFieldsFile(importFieldsPath, c.ImportFieldsJSON); err != nil {
		return "", err
	}

	if dest := strings.TrimSpace(c.ExportPKInternal); dest != "" {
		if err := copyFile(filepath.Join(keysDir, "pk_internal.pem"), dest); err != nil {
			return "", fmt.Errorf("另存 pk_internal.pem: %w", err)
		}
	}

	tlsVal := "0"
	if c.HTTPS {
		tlsVal = "1"
	}
	publicBase := strings.TrimSpace(c.PublicBase)
	if publicBase == "" && strings.TrimSpace(c.Domain) != "" {
		scheme := "http"
		if c.HTTPS {
			scheme = "https"
		}
		_, port, _ := splitListen(c.Listen)
		host := strings.TrimSpace(c.Domain)
		if (port == "80" && scheme == "http") || (port == "443" && scheme == "https") {
			publicBase = scheme + "://" + host
		} else {
			publicBase = fmt.Sprintf("%s://%s:%s", scheme, host, port)
		}
	}

	lines := []string{
		"# FDT Portal GUI — 用户目录配置（无需管理员权限）",
		"ROLE=intranet",
		"LISTEN=" + strings.TrimSpace(c.Listen),
		"DATA_LISTEN=" + strings.TrimSpace(c.DataListen),
		"DATA_DIR=" + dataDir,
		"KEYS_DIR=" + keysDir,
		"TENANT=" + strings.TrimSpace(c.Tenant),
		"DOMAIN=" + strings.TrimSpace(c.Domain),
		"PUBLIC_BASE=" + publicBase,
		"TLS=" + tlsVal,
		"TLS_CERT=" + filepath.Join(keysDir, "server.pem"),
		"TLS_KEY=" + filepath.Join(keysDir, "server.key"),
		"IDENTITY=" + strings.ToLower(strings.TrimSpace(c.Identity)),
		"DEVICE_ID=" + strings.TrimSpace(c.DeviceID),
		"DEVICE_SECRET=" + strings.TrimSpace(c.DeviceSecret),
		"PORTAL_ADMIN_USER=" + strings.TrimSpace(c.AdminUser),
		"PORTAL_ADMIN_PASSWORD=" + strings.TrimSpace(c.AdminPass),
		"PORTAL_ADMIN_USERS=" + strings.TrimSpace(c.AdminUsers),
		"FORM_FIELDS_FILE=" + formFieldsPath,
		"ATM_IMPORT_FIELDS_TEMPLATE=" + importFieldsPath,
		"ENV_FILE=" + envPath,
		"LDAP_CONFIG=" + filepath.Join(filepath.Dir(keysDir), "ldap.json"),
	}
	if strings.TrimSpace(c.Clamd) != "" {
		lines = append(lines, "CLAMD="+strings.TrimSpace(c.Clamd))
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

func ensurePortalKeys(keysDir string, c PortalConfig) error {
	skInt := filepath.Join(keysDir, "sk_internal.pem")
	pkInt := filepath.Join(keysDir, "pk_internal.pem")
	skGw := filepath.Join(keysDir, "sk_gateway.pem")
	pkGw := filepath.Join(keysDir, "pk_gateway.pem")

	if pem := strings.TrimSpace(c.InternalPrivPEM); pem != "" {
		if err := os.WriteFile(skInt, []byte(ensureNL(pem)), 0o600); err != nil {
			return err
		}
	}
	if pem := strings.TrimSpace(c.InternalPubPEM); pem != "" {
		if err := os.WriteFile(pkInt, []byte(ensureNL(pem)), 0o644); err != nil {
			return err
		}
	}
	if pem := strings.TrimSpace(c.GatewayPrivPEM); pem != "" {
		if err := os.WriteFile(skGw, []byte(ensureNL(pem)), 0o600); err != nil {
			return err
		}
	}
	if pem := strings.TrimSpace(c.GatewayPubPEM); pem != "" {
		if err := os.WriteFile(pkGw, []byte(ensureNL(pem)), 0o644); err != nil {
			return err
		}
	}

	// Already on disk and not forcing → keep existing (e.g. generated via「生成并下载」).
	return generatePortalKeyFiles(keysDir, c.GenKeys)
}

// generatePortalKeyFiles writes internal+gateway keypairs.
// force=false: only create missing files (never overwrite).
// force=true: regenerate both pairs.
func generatePortalKeyFiles(keysDir string, force bool) error {
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		return err
	}
	skInt := filepath.Join(keysDir, "sk_internal.pem")
	pkInt := filepath.Join(keysDir, "pk_internal.pem")
	skGw := filepath.Join(keysDir, "sk_gateway.pem")
	pkGw := filepath.Join(keysDir, "pk_gateway.pem")

	needInt := force
	if _, err := os.Stat(skInt); err != nil {
		needInt = true
	}
	if _, err := os.Stat(pkInt); err != nil {
		needInt = true
	}
	if needInt {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := crypto.WritePrivatePEM(skInt, kp.Priv); err != nil {
			return err
		}
		if err := crypto.WritePublicPEM(pkInt, kp.Pub); err != nil {
			return err
		}
	}

	needGw := force
	if _, err := os.Stat(skGw); err != nil {
		needGw = true
	}
	if _, err := os.Stat(pkGw); err != nil {
		needGw = true
	}
	if needGw {
		kp, err := crypto.GenerateKeyPair()
		if err != nil {
			return err
		}
		if err := crypto.WritePrivatePEM(skGw, kp.Priv); err != nil {
			return err
		}
		if err := crypto.WritePublicPEM(pkGw, kp.Pub); err != nil {
			return err
		}
	}
	return nil
}

func portalKeysPresent(keysDir string) bool {
	_, e1 := os.Stat(filepath.Join(keysDir, "sk_internal.pem"))
	_, e2 := os.Stat(filepath.Join(keysDir, "pk_internal.pem"))
	return e1 == nil && e2 == nil
}

func writeOptionalTLS(keysDir string, c PortalConfig) error {
	if strings.TrimSpace(c.TLSCertPEM) == "" {
		return nil
	}
	if err := os.WriteFile(filepath.Join(keysDir, "server.pem"), []byte(ensureNL(c.TLSCertPEM)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(keysDir, "server.key"), []byte(ensureNL(c.TLSKeyPEM)), 0o600)
}

func writeFormFieldsFile(path, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return portal.SaveFormFields(path, portal.DefaultFormFields())
	}
	var f portal.FormFieldsFile
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return fmt.Errorf("form-fields.json 不是合法 JSON: %w", err)
	}
	return portal.SaveFormFields(path, f)
}

func writeATMImportFieldsFile(path, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return atmui.SaveImportFields(path, atmui.DefaultImportFields())
	}
	var f atmui.ImportFieldsFile
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return fmt.Errorf("import-fields.json 不是合法 JSON: %w", err)
	}
	return atmui.SaveImportFields(path, f)
}

func defaultFormFieldsJSON() string {
	b, err := json.MarshalIndent(portal.DefaultFormFields(), "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

func defaultImportFieldsJSON() string {
	b, err := json.MarshalIndent(atmui.DefaultImportFields(), "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

func loadConfigForForm(envPath string) PortalConfig {
	c := PortalConfig{
		Listen:     "0.0.0.0:8080",
		DataListen: "0.0.0.0:8081",
		Tenant:     "default",
		AdminUser:  "admin",
		Identity:   "mock",
		DeviceID:   "atm-01",
	}
	if envPath == "" {
		envPath = defaultEnvPath()
	}
	env, err := apprun.LoadEnv(envPath)
	if err != nil {
		return c
	}
	c.Listen = apprun.EnvOr(env, "LISTEN", c.Listen)
	c.DataListen = apprun.EnvOr(env, "DATA_LISTEN", c.DataListen)
	c.Tenant = apprun.EnvOr(env, "TENANT", c.Tenant)
	c.Domain = env["DOMAIN"]
	c.PublicBase = env["PUBLIC_BASE"]
	c.HTTPS = env["TLS"] == "1" || env["TLS"] == "true"
	c.DataDir = env["DATA_DIR"]
	c.KeysDir = env["KEYS_DIR"]
	c.AdminUser = apprun.EnvOr(env, "PORTAL_ADMIN_USER", c.AdminUser)
	c.AdminPass = env["PORTAL_ADMIN_PASSWORD"]
	c.AdminUsers = env["PORTAL_ADMIN_USERS"]
	c.Identity = apprun.EnvOr(env, "IDENTITY", c.Identity)
	c.DeviceID = apprun.EnvOr(env, "DEVICE_ID", c.DeviceID)
	c.DeviceSecret = env["DEVICE_SECRET"]
	c.Clamd = env["CLAMD"]
	if p := env["FORM_FIELDS_FILE"]; p != "" {
		if b, err := os.ReadFile(p); err == nil {
			c.FormFieldsJSON = string(b)
		}
	}
	if p := env["ATM_IMPORT_FIELDS_TEMPLATE"]; p != "" {
		if b, err := os.ReadFile(p); err == nil {
			c.ImportFieldsJSON = string(b)
		}
	}
	return c
}

func ensureNL(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func profileDir() string {
	if p := os.Getenv("PORTAL_GUI_PROFILE"); p != "" {
		_ = os.MkdirAll(p, 0o755)
		return p
	}
	d := filepath.Join(userPortalRoot(), "gui-profile")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func publicPortalURL(env map[string]string) string {
	if b := strings.TrimRight(strings.TrimSpace(env["PUBLIC_BASE"]), "/"); b != "" {
		return b + "/"
	}
	listen := apprun.EnvOr(env, "LISTEN", "0.0.0.0:8080")
	host, port, err := splitListen(listen)
	if err != nil {
		return "http://127.0.0.1:8080/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	if env["TLS"] == "1" || env["TLS"] == "true" {
		scheme = "https"
	}
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return scheme + "://" + host + "/"
	}
	return fmt.Sprintf("%s://%s:%s/", scheme, host, port)
}

func goosHint() string { return runtime.GOOS }

func netSplitHostPort(addr string) (host, port string, err error) {
	return net.SplitHostPort(addr)
}
