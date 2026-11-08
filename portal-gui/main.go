// Command fdt-portal-gui is a standalone Portal desktop app for standard users:
// first run → setup wizard (or -setup-cli) → install as OS service → open browser.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fdt/internal/apprun"
	"fdt/internal/apprun/inapp"
	"fdt/internal/buildinfo"
)

func main() {
	showVer := flag.Bool("version", false, "打印版本并退出")
	envFlag := flag.String("env", "", "Portal 配置文件（默认: 用户目录 FDT-Portal/fdt.env）")
	forceSetup := flag.Bool("setup", false, "打开图形化首次配置向导")
	setupCLI := flag.Bool("setup-cli", false, "命令行写入配置后退出（不启动服务）")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	installSvc := flag.Bool("install-service", false, "安装并启动系统/用户服务后退出")
	uninstallSvc := flag.Bool("uninstall-service", false, "停止并卸载服务后退出")
	serviceMode := flag.Bool("service", false, "服务模式（由 systemd/计划任务调用，前台跑 Portal）")
	foreground := flag.Bool("foreground", false, "前台运行（不装服务、不托管）")

	listen := flag.String("listen", "", "Portal UI 监听，如 0.0.0.0:8080")
	dataListen := flag.String("data-listen", "", "ATM 数据口，如 0.0.0.0:8081")
	domain := flag.String("domain", "", "域名或主机名")
	publicBase := flag.String("public-base", "", "对外根地址 https://…")
	tenant := flag.String("tenant", "", "租户 ID")
	https := flag.Bool("https", false, "启用 HTTPS")
	dataDir := flag.String("data-dir", "", "数据目录")
	keysDir := flag.String("keys-dir", "", "密钥目录")
	adminUser := flag.String("admin-user", "", "管理用户")
	adminPass := flag.String("admin-pass", "", "管理密码")
	adminUsers := flag.String("admin-users", "", "额外管理员，逗号分隔")
	identity := flag.String("identity", "", "mock|ldap|pam|none")
	deviceID := flag.String("device-id", "", "预注册 ATM 设备 ID")
	deviceSecret := flag.String("device-secret", "", "设备密钥")
	clamd := flag.String("clamd", "", "ClamAV 地址")
	formFields := flag.String("form-fields", "", "导出表单 JSON 文件路径")
	importFields := flag.String("import-fields", "", "导入表单模板 JSON 路径")
	genKeys := flag.Bool("gen-keys", false, "强制重新生成密钥对")
	exportPK := flag.String("export-pk-internal", "", "另存 pk_internal.pem 路径")
	tlsCertFile := flag.String("tls-cert-file", "", "TLS 证书 PEM 文件")
	tlsKeyFile := flag.String("tls-key-file", "", "TLS 私钥 PEM 文件")
	flag.Parse()
	if *showVer {
		fmt.Println(buildinfo.Summary("fdt-portal-gui"))
		return
	}

	envPath := strings.TrimSpace(*envFlag)
	if envPath == "" {
		envPath = defaultEnvPath()
	}

	if *uninstallSvc {
		if err := uninstallPortalService(); err != nil {
			fatal("卸载服务失败: %v", err)
		}
		fmt.Println("已卸载 Portal 服务")
		return
	}

	if *setupCLI {
		c := loadConfigForForm(envPath)
		applyCLIFlags(&c, listen, dataListen, domain, publicBase, tenant, https,
			dataDir, keysDir, adminUser, adminPass, adminUsers, identity,
			deviceID, deviceSecret, clamd, formFields, importFields, genKeys, exportPK,
			tlsCertFile, tlsKeyFile)
		path, err := savePortalConfig(c)
		if err != nil {
			fatal("%v", err)
		}
		fmt.Printf("已写入 Portal 配置: %s\n  目录: %s\n", path, userPortalRoot())
		fmt.Printf("  公钥: %s\n", filepath.Join(apprunKeysHint(c), "pk_internal.pem"))
		if *installSvc {
			if err := installPortalService(path); err != nil {
				fatal("安装服务失败: %v", err)
			}
			fmt.Println("已安装并启动服务")
			fmt.Println(serviceManageHint())
		}
		return
	}

	if *forceSetup || !configReady(envPath) {
		fmt.Println("需要配置 Portal（首次启动或配置不完整）…")
		path, err := runSetupWizard(*forceSetup || !configReady(envPath))
		if err != nil {
			fatal("%v", err)
		}
		envPath = path
	}

	env, err := apprun.LoadEnv(envPath)
	if err != nil {
		fatal("读取配置失败: %v\n  可运行: %s -setup", err, selfName())
	}
	if err := apprun.RequireRole(env, "intranet", "portal"); err != nil {
		if env["ROLE"] == "" {
			env["ROLE"] = "intranet"
		} else {
			fatal("%v", err)
		}
	}
	if env["ENV_FILE"] == "" {
		env["ENV_FILE"] = envPath
	}

	uiURL := publicPortalURL(env)
	printBanner(env, envPath, uiURL)

	// Service worker entry: just run Portal in foreground.
	if *serviceMode {
		if err := inapp.RunIntranet(env); err != nil {
			fatal("%v", err)
		}
		return
	}

	if *installSvc || !*foreground {
		if err := installPortalService(envPath); err != nil {
			if *installSvc {
				fatal("安装服务失败: %v\n  可改用: %s -foreground", err, selfName())
			}
			fmt.Fprintf(os.Stderr, "warn: 安装服务失败，改为前台运行: %v\n", err)
		} else {
			fmt.Println("已安装并启动为系统服务（后台常驻）")
			fmt.Println(serviceManageHint())
			if !*noBrowser {
				go func() {
					time.Sleep(800 * time.Millisecond)
					_ = openBrowser("FDT Portal", uiURL)
				}()
				time.Sleep(1200 * time.Millisecond)
			}
			if *installSvc {
				return
			}
			// Default: service hosts Portal; this process only opened the browser.
			fmt.Println("Portal 已由服务托管，本窗口可关闭。")
			return
		}
	}

	// Foreground fallback / explicit -foreground
	if !*noBrowser {
		go func() {
			time.Sleep(600 * time.Millisecond)
			_ = openBrowser("FDT Portal", uiURL)
		}()
	}
	if err := inapp.RunIntranet(env); err != nil {
		fatal("%v", err)
	}
}

func printBanner(env map[string]string, envPath, uiURL string) {
	fmt.Printf("FDT Portal GUI\n  UI %s\n  ATM 数据口 %s\n  配置 %s\n  目录 %s\n",
		apprun.EnvOr(env, "LISTEN", "0.0.0.0:8080"),
		apprun.EnvOr(env, "DATA_LISTEN", "0.0.0.0:8081"),
		envPath, userPortalRoot())
	fmt.Printf("  浏览器: %s\n  管理: %sadmin/  用户 %s\n",
		uiURL, uiURL, apprun.EnvOr(env, "PORTAL_ADMIN_USER", "admin"))
}

func applyCLIFlags(c *PortalConfig, listen, dataListen, domain, publicBase, tenant *string, https *bool,
	dataDir, keysDir, adminUser, adminPass, adminUsers, identity,
	deviceID, deviceSecret, clamd, formFields, importFields *string, genKeys *bool, exportPK,
	tlsCertFile, tlsKeyFile *string) {
	if v := strings.TrimSpace(*listen); v != "" {
		c.Listen = v
	}
	if v := strings.TrimSpace(*dataListen); v != "" {
		c.DataListen = v
	}
	if v := strings.TrimSpace(*domain); v != "" {
		c.Domain = v
	}
	if v := strings.TrimSpace(*publicBase); v != "" {
		c.PublicBase = v
	}
	if v := strings.TrimSpace(*tenant); v != "" {
		c.Tenant = v
	}
	if *https {
		c.HTTPS = true
	}
	if v := strings.TrimSpace(*dataDir); v != "" {
		c.DataDir = v
	}
	if v := strings.TrimSpace(*keysDir); v != "" {
		c.KeysDir = v
	}
	if v := strings.TrimSpace(*adminUser); v != "" {
		c.AdminUser = v
	}
	if v := strings.TrimSpace(*adminPass); v != "" {
		c.AdminPass = v
	}
	if v := strings.TrimSpace(*adminUsers); v != "" {
		c.AdminUsers = v
	}
	if v := strings.TrimSpace(*identity); v != "" {
		c.Identity = v
	}
	if v := strings.TrimSpace(*deviceID); v != "" {
		c.DeviceID = v
	}
	if v := strings.TrimSpace(*deviceSecret); v != "" {
		c.DeviceSecret = v
	}
	if v := strings.TrimSpace(*clamd); v != "" {
		c.Clamd = v
	}
	c.GenKeys = *genKeys
	if v := strings.TrimSpace(*exportPK); v != "" {
		c.ExportPKInternal = v
	}
	if p := strings.TrimSpace(*formFields); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			c.FormFieldsJSON = string(b)
		} else {
			fatal("读取 -form-fields: %v", err)
		}
	}
	if p := strings.TrimSpace(*importFields); p != "" {
		if b, err := os.ReadFile(p); err == nil {
			c.ImportFieldsJSON = string(b)
		} else {
			fatal("读取 -import-fields: %v", err)
		}
	}
	if p := strings.TrimSpace(*tlsCertFile); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fatal("读取 -tls-cert-file: %v", err)
		}
		c.TLSCertPEM = string(b)
	}
	if p := strings.TrimSpace(*tlsKeyFile); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			fatal("读取 -tls-key-file: %v", err)
		}
		c.TLSKeyPEM = string(b)
	}
	if strings.TrimSpace(c.AdminPass) == "" {
		fatal("-setup-cli 需要 -admin-pass")
	}
}

func selfName() string {
	exe, err := os.Executable()
	if err != nil {
		return "fdt-portal-gui"
	}
	return filepath.Base(exe)
}

func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "error:", msg)
	showError(msg)
	os.Exit(1)
}
