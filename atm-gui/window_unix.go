//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	activeCmdMu sync.Mutex
	activeCmd   *exec.Cmd
)

// openWindow opens a normal closable app window (first-run setup wizard).
func openWindow(title, uiURL string) error {
	bin, err := findBrowser()
	if err != nil {
		return err
	}
	profile := filepath.Join(profileDir(), "chromium-setup")
	_ = os.MkdirAll(profile, 0o755)
	args := []string{
		"--app=" + uiURL,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=TranslateUI",
		"--class=FDT-ATM-Setup",
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动浏览器窗口失败 (%s): %w", bin, err)
	}
	_ = title
	_ = cmd.Wait()
	return nil
}

// openKioskWindow runs Chromium --kiosk and relaunches if closed without maint quit.
func openKioskWindow(title, uiURL string) error {
	bin, err := findBrowser()
	if err != nil {
		return err
	}
	_ = title
	unhook, _ := installKioskKeyboardHook()
	defer unhook()
	restore := installDesktopHotkeyShield()
	defer restore()

	profile := filepath.Join(profileDir(), "chromium-kiosk")
	_ = os.MkdirAll(profile, 0o755)
	args := []string{
		"--kiosk",
		"--app=" + uiURL,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--noerrdialogs",
		"--disable-session-crashed-bubble",
		"--disable-infobars",
		"--disable-translate",
		"--disable-features=TranslateUI",
		"--check-for-update-interval=31536000",
		"--password-store=basic",
		"--overscroll-history-navigation=0",
		"--disable-pinch",
		"--class=FDT-ATM-GUI",
	}

	go func() {
		<-quitSignal()
		activeCmdMu.Lock()
		cmd := activeCmd
		activeCmdMu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	for !shouldQuit() {
		cmd := exec.Command(bin, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("启动 kiosk 窗口失败 (%s): %w", bin, err)
		}
		activeCmdMu.Lock()
		activeCmd = cmd
		activeCmdMu.Unlock()

		_ = cmd.Wait()

		activeCmdMu.Lock()
		activeCmd = nil
		activeCmdMu.Unlock()

		if shouldQuit() {
			break
		}
		fmt.Fprintln(os.Stderr, "ATM 窗口被关闭，正在重新打开…（维护模式「退出程序」+ 密码才能真正退出）")
		time.Sleep(400 * time.Millisecond)
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
