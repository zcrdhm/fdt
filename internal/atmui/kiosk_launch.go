package atmui

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// chromeCandidates are tried in order for the ATM kiosk window.
var chromeCandidates = []string{
	"google-chrome-stable",
	"google-chrome",
	"chromium-browser",
	"chromium",
	"/usr/bin/google-chrome-stable",
	"/usr/bin/google-chrome",
	"/usr/bin/chromium-browser",
	"/usr/bin/chromium",
}

var (
	kioskMu     sync.Mutex
	kioskCmd    *exec.Cmd
	kioskLastAt time.Time
)

// findChrome returns the first available Chromium/Chrome binary.
func findChrome() (string, error) {
	for _, c := range chromeCandidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
		if filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("未找到 Chromium/Chrome，请安装 google-chrome 或 chromium")
}

// sessionUser is the interactive desktop account Chrome must run as.
// ATM UI often runs as root (USB mount); Chrome cannot use a root-owned profile
// on the user's X/Wayland session.
func sessionUser() *user.User {
	for _, name := range []string{
		os.Getenv("ATM_KIOSK_USER"),
		os.Getenv("SUDO_USER"),
		os.Getenv("USER"),
	} {
		if name == "" || name == "root" {
			continue
		}
		if u, err := user.Lookup(name); err == nil {
			return u
		}
	}
	if os.Geteuid() != 0 {
		if u, err := user.Current(); err == nil && u.Username != "root" {
			return u
		}
	}
	// Fall back: owner of the graphical seat (uid 1000 is common on single-user ATMs).
	if u, err := user.LookupId("1000"); err == nil {
		return u
	}
	return nil
}

func chownTree(path string, u *user.User) {
	if u == nil || os.Geteuid() != 0 {
		return
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return
	}
	_ = filepath.Walk(path, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chown(p, uid, gid)
		return nil
	})
}

func kioskProfileDir(u *user.User) string {
	var d string
	if p := os.Getenv("ATM_KIOSK_PROFILE"); p != "" {
		d = p
	} else if u != nil && u.HomeDir != "" {
		d = filepath.Join(u.HomeDir, ".cache", "fdt-atm-chrome-kiosk")
	} else {
		d = filepath.Join(os.TempDir(), "fdt-atm-chrome-kiosk")
	}
	_ = os.MkdirAll(d, 0o755)
	chownTree(d, u)
	return d
}

func detectDisplay() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	// Prefer highest numbered X socket (GDM often uses :1).
	best := ""
	entries, _ := os.ReadDir("/tmp/.X11-unix")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "X") {
			continue
		}
		d := ":" + strings.TrimPrefix(name, "X")
		if d > best {
			best = d
		}
	}
	if best != "" {
		return best
	}
	return ":0"
}

func displayEnv(u *user.User) []string {
	env := os.Environ()
	display := detectDisplay()
	xauth := os.Getenv("XAUTHORITY")
	if xauth == "" && u != nil {
		cand := filepath.Join("/run/user", u.Uid, "gdm", "Xauthority")
		if _, err := os.Stat(cand); err == nil {
			xauth = cand
		} else {
			cand = filepath.Join(u.HomeDir, ".Xauthority")
			if _, err := os.Stat(cand); err == nil {
				xauth = cand
			}
		}
	}
	// Ensure DISPLAY / XAUTHORITY are set for a root→user drop.
	set := func(k, v string) {
		if v == "" {
			return
		}
		prefix := k + "="
		for i, e := range env {
			if strings.HasPrefix(e, prefix) {
				env[i] = prefix + v
				return
			}
		}
		env = append(env, prefix+v)
	}
	set("DISPLAY", display)
	set("XAUTHORITY", xauth)
	if u != nil {
		set("HOME", u.HomeDir)
		set("USER", u.Username)
		set("LOGNAME", u.Username)
		if xdg := filepath.Join("/run/user", u.Uid); true {
			set("XDG_RUNTIME_DIR", xdg)
		}
	}
	return env
}

// LaunchKioskBrowser starts Chromium in true --kiosk mode against uiURL.
// Ordinary browser Fullscreen API cannot reliably block Esc; OS kiosk can.
func LaunchKioskBrowser(uiURL string) (chrome string, err error) {
	if uiURL == "" {
		uiURL = "http://127.0.0.1:9080/?kiosk=1&run=1"
	}
	chrome, err = findChrome()
	if err != nil {
		return "", err
	}
	u := sessionUser()
	profile := kioskProfileDir(u)

	kioskMu.Lock()
	defer kioskMu.Unlock()
	if time.Since(kioskLastAt) < 2*time.Second && kioskCmd != nil && kioskCmd.Process != nil {
		return chrome, nil
	}
	if kioskCmd != nil && kioskCmd.Process != nil {
		_ = kioskCmd.Process.Kill()
		_, _ = kioskCmd.Process.Wait()
		kioskCmd = nil
	}

	args := []string{
		"--kiosk",
		"--app=" + uiURL,
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--noerrdialogs",
		"--disable-session-crashed-bubble",
		"--disable-infobars",
		"--disable-translate",
		"--disable-features=TranslateUI",
		"--check-for-update-interval=31536000",
		"--password-store=basic",
		"--autoplay-policy=no-user-gesture-required",
		"--overscroll-history-navigation=0",
		"--disable-pinch",
		"--start-maximized",
	}

	var cmd *exec.Cmd
	// Drop root → desktop user so profile + X11 auth work.
	if os.Geteuid() == 0 && u != nil && u.Uid != "0" {
		if _, err := exec.LookPath("runuser"); err == nil {
			runArgs := append([]string{"-u", u.Username, "--", chrome}, args...)
			cmd = exec.Command("runuser", runArgs...)
		} else {
			runArgs := append([]string{"-u", u.Username, "--", chrome}, args...)
			cmd = exec.Command("sudo", runArgs...)
		}
	} else {
		cmd = exec.Command(chrome, args...)
	}
	cmd.Env = displayEnv(u)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return chrome, fmt.Errorf("启动 kiosk 失败: %w", err)
	}
	kioskCmd = cmd
	kioskLastAt = time.Now()
	go func() { _, _ = cmd.Process.Wait() }()
	return chrome, nil
}
