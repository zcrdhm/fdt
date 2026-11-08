//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strings"
)

// installKioskKeyboardHook is Windows-only; Unix relies on Chromium --kiosk + Super shield.
func installKioskKeyboardHook() (func(), error) { return func() {}, nil }

// installDesktopHotkeyShield best-effort disables the GNOME Super/overlay key while ATM GUI runs.
func installDesktopHotkeyShield() func() {
	prev := gsettingsGet("org.gnome.mutter", "overlay-key")
	if prev == "" && !gsettingsHas("org.gnome.mutter", "overlay-key") {
		return func() {}
	}
	_ = gsettingsSet("org.gnome.mutter", "overlay-key", "''")
	return func() {
		if prev == "" {
			_ = gsettingsSet("org.gnome.mutter", "overlay-key", "''")
			return
		}
		// Restore previous value (typically 'Super_L').
		_ = gsettingsSet("org.gnome.mutter", "overlay-key", quoteGSettings(prev))
	}
}

func gsettingsHas(schema, key string) bool {
	path, err := exec.LookPath("gsettings")
	if err != nil {
		return false
	}
	cmd := exec.Command(path, "writable", schema, key)
	cmd.Env = os.Environ()
	return cmd.Run() == nil
}

func gsettingsGet(schema, key string) string {
	path, err := exec.LookPath("gsettings")
	if err != nil {
		return ""
	}
	out, err := exec.Command(path, "get", schema, key).Output()
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	// gsettings returns quoted strings like 'Super_L' or ''
	s = strings.TrimPrefix(s, "'")
	s = strings.TrimSuffix(s, "'")
	return s
}

func gsettingsSet(schema, key, value string) error {
	path, err := exec.LookPath("gsettings")
	if err != nil {
		return err
	}
	cmd := exec.Command(path, "set", schema, key, value)
	cmd.Env = os.Environ()
	return cmd.Run()
}

func quoteGSettings(v string) string {
	if v == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}
