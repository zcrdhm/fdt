//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openWindow opens a browser window and waits until it is closed (setup wizard).
func openWindow(title, uiURL string) error {
	return launchBrowser(title, uiURL, true)
}

// openBrowser opens the Portal UI without blocking the service.
func openBrowser(title, uiURL string) error {
	return launchBrowser(title, uiURL, false)
}

func launchBrowser(title, uiURL string, wait bool) error {
	bin, err := findBrowser()
	if err != nil {
		for _, c := range []string{"xdg-open", "open"} {
			if p, e := exec.LookPath(c); e == nil {
				cmd := exec.Command(p, uiURL)
				if err := cmd.Start(); err != nil {
					return err
				}
				_ = title
				if wait {
					_ = cmd.Wait()
				}
				return nil
			}
		}
		return err
	}
	profile := filepath.Join(profileDir(), "chromium-profile")
	if wait {
		profile = filepath.Join(profileDir(), "chromium-setup")
	}
	_ = os.MkdirAll(profile, 0o755)
	args := []string{
		"--app=" + uiURL,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=TranslateUI",
		"--class=FDT-Portal-GUI",
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动浏览器失败 (%s): %w", bin, err)
	}
	_ = title
	if wait {
		_ = cmd.Wait()
	}
	return nil
}

func findBrowser() (string, error) {
	candidates := []string{
		os.Getenv("PORTAL_GUI_BROWSER"),
		"google-chrome-stable", "google-chrome",
		"chromium-browser", "chromium",
		"microsoft-edge", "microsoft-edge-stable",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
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
	return "", fmt.Errorf("未找到 Chrome/Chromium/Edge（可设 PORTAL_GUI_BROWSER）")
}

func showError(msg string) {
	if path, err := exec.LookPath("zenity"); err == nil {
		_ = exec.Command(path, "--error", "--title=FDT Portal", "--text="+msg).Run()
		return
	}
	if path, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(path, "FDT Portal", msg).Run()
	}
}
