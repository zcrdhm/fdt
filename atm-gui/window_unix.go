//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func openWindow(title, uiURL string) error {
	bin, err := findBrowser()
	if err != nil {
		return err
	}
	profile := filepath.Join(profileDir(), "chromium-profile")
	_ = os.MkdirAll(profile, 0o755)
	args := []string{
		"--app=" + uiURL,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=TranslateUI",
		"--class=FDT-ATM-GUI",
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动浏览器窗口失败 (%s): %w", bin, err)
	}
	_ = title
	err = cmd.Wait()
	if err != nil {
		// User closing the window often yields non-zero; treat as normal exit.
		if ee, ok := err.(*exec.ExitError); ok {
			_ = ee
			return nil
		}
		return err
	}
	return nil
}

func findBrowser() (string, error) {
	candidates := []string{
		os.Getenv("ATM_GUI_BROWSER"),
		"google-chrome-stable",
		"google-chrome",
		"chromium-browser",
		"chromium",
		"microsoft-edge",
		"microsoft-edge-stable",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/google-chrome",
		"/usr/bin/chromium-browser",
		"/usr/bin/chromium",
	}
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		if filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 Chrome/Chromium/Edge，请安装后重试（或设 ATM_GUI_BROWSER）")
}

func showError(msg string) {
	// Best-effort desktop notification / zenity; always also printed by fatal().
	if path, err := exec.LookPath("zenity"); err == nil {
		_ = exec.Command(path, "--error", "--title=FDT ATM", "--text="+msg).Run()
		return
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(path, "FDT ATM", msg).Run()
	}
}

func acquireSingleInstance() (func(), error) {
	dir := profileDir()
	lockPath := filepath.Join(dir, "atm-gui.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}, nil
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("already running")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
