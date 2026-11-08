// Command fdt-atm-gui is a standalone ATM desktop app for standard (non-admin) users:
// first run → setup wizard → write user-local fdt.env; later runs start service + window.
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
)

func main() {
	envFlag := flag.String("env", "", "ATM 配置文件（默认: 用户目录 FDT-ATM/fdt.env）")
	listen := flag.String("listen", "", "覆盖 ATM_UI_LISTEN")
	forceSetup := flag.Bool("setup", false, "强制打开首次配置向导")
	flag.Parse()

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
	})
	if err != nil {
		fatal("启动 ATM 服务失败: %v\n  可运行: %s -setup  重新配置", err, selfName())
	}
	defer func() { _ = h.Shutdown(context.Background()) }()

	uiURL := withQuery(h.URL+"/", "run", "1")
	fmt.Printf("FDT ATM GUI\n  服务 %s\n  设备 %s\n  配置 %s\n  数据目录 %s\n", h.URL, h.DeviceID, h.EnvPath, userATMRoot())
	fmt.Println("  维护密码可在界面「维护模式」中修改；改密后无需管理员权限。")

	if err := openWindow("FDT ATM", uiURL); err != nil {
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
	// Keep browser/WebView profile under the same user ATM home.
	d := filepath.Join(userATMRoot(), "gui-profile")
	_ = os.MkdirAll(d, 0o755)
	return d
}
