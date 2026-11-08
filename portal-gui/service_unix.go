//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const userUnitName = "fdt-portal.service"

func serviceUnitPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, userUnitName), nil
}

func installPortalService(envPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	envPath, err = filepath.Abs(envPath)
	if err != nil {
		return err
	}
	unitPath, err := serviceUnitPath()
	if err != nil {
		return err
	}
	work := filepath.Dir(envPath)
	unit := fmt.Sprintf(`[Unit]
Description=FDT Portal (intranet UI + ATM data plane)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s -service -no-browser -env %s
WorkingDirectory=%s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, systemdQuote(exe), systemdQuote(envPath), work)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	if out, err := exec.Command("systemctl", "--user", "enable", "--now", userUnitName).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user enable --now: %v\n%s\n提示: 若无用户总线，可先执行 loginctl enable-linger $USER，或改用 -foreground", err, out)
	}
	return nil
}

// systemdQuote wraps a path for ExecStart when it contains spaces/specials.
func systemdQuote(p string) string {
	if p == "" || strings.ContainsAny(p, " \t\"'\\") {
		return strconv.Quote(p)
	}
	return p
}

func uninstallPortalService() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", userUnitName).Run()
	unitPath, err := serviceUnitPath()
	if err == nil {
		_ = os.Remove(unitPath)
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	return nil
}

func startPortalService() error {
	out, err := exec.Command("systemctl", "--user", "start", userUnitName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("start: %v\n%s", err, out)
	}
	return nil
}

func portalServiceInstalled() bool {
	p, err := serviceUnitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func portalServiceRunning() bool {
	out, err := exec.Command("systemctl", "--user", "is-active", userUnitName).Output()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

func serviceManageHint() string {
	return "管理服务: systemctl --user status|stop|restart fdt-portal\n" +
		"开机自启(用户未登录也跑): loginctl enable-linger $USER\n" +
		"卸载服务: fdt-portal-gui -uninstall-service"
}
