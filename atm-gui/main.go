// Command fdt-atm-gui is a standalone ATM desktop app for standard (non-admin) users:
// first run → setup wizard → write user-local fdt.env; later runs start service + kiosk window.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"fdt/internal/apprun/atmapp"
	"fdt/internal/buildinfo"
)

func main() {
	showVer := flag.Bool("version", false, "打印版本并退出")
	envFlag := flag.String("env", "", "ATM 配置文件（默认: 用户目录 FDT-ATM/fdt.env）")
	listen := flag.String("listen", "", "覆盖 ATM_UI_LISTEN")
	forceSetup := flag.Bool("setup", false, "强制打开首次配置向导")
	flag.Parse()
	if *showVer {
		fmt.Println(buildinfo.Summary("fdt-atm-gui"))
		return
	}

	unlock, err := acquireSingleInstance()
	if err != nil {
		fatal("FDT ATM 已在运行（或无法加锁）: %v", err)
	}
	defer unlock()

	envPath := strings.TrimSpace(*envFlag)
	if envPath == "" {
		envPath = defaultEnvPath()
	}

	if *forceSetup || !configReady(envPath) {
		fmt.Println("需要配置 ATM（首次启动或配置不完整）…")
		path, err := runSetupWizard(*forceSetup || !configReady(envPath))
		if err != nil {
			fatal("%v", err)
		}
		envPath = path
	}

	h, err := atmapp.StartUI(atmapp.UIOptions{
		EnvPath: envPath,
		Listen:  *listen,
		Local:   true,
		OnQuitGUI: func() {
			requestQuit()
		},
	})
	if err != nil {
		fatal("启动 ATM 服务失败: %v\n  可运行: %s -setup  重新配置", err, selfName())
	}
	defer func() { _ = h.Shutdown(context.Background()) }()

	uiURL := withQuery(h.URL+"/", "run", "1")
	uiURL = withQuery(uiURL, "kiosk", "1")
	fmt.Printf("FDT ATM GUI（防退出）\n  服务 %s\n  设备 %s\n  配置 %s\n  数据目录 %s\n", h.URL, h.DeviceID, h.EnvPath, userATMRoot())
	fmt.Println("  退出：维护模式 →「退出程序」→ 输入维护密码。关窗/热键会被拦截或自动重开。")

	if err := openKioskWindow("FDT ATM", uiURL); err != nil {
		fatal("打开窗口失败: %v", err)
	}
}

func selfName() string {
	exe, err := os.Executable()
	if err != nil {
		return "fdt-atm-gui"
	}
	return filepath.Base(exe)
}

func withQuery(raw, k, v string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if strings.Contains(raw, "?") {
			return raw + "&" + k + "=" + v
		}
		return raw + "?" + k + "=" + v
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "error:", msg)
	showError(msg)
	os.Exit(1)
}

func profileDir() string {
	if p := os.Getenv("ATM_GUI_PROFILE"); p != "" {
		_ = os.MkdirAll(p, 0o755)
		return p
	}
	d := filepath.Join(userATMRoot(), "gui-profile")
	_ = os.MkdirAll(d, 0o755)
	return d
}
