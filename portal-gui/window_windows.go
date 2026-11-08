//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

// openWindow blocks until the window is closed (setup wizard).
func openWindow(title, uiURL string) error {
	return launchWebView(title, uiURL, true)
}

// openBrowser opens Portal UI; on Windows uses a detached default-browser start
// so the service goroutine is not blocked by WebView2.Run.
func openBrowser(title, uiURL string) error {
	_ = title
	cmd := exec.Command("cmd", "/c", "start", "", uiURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return launchWebView(title, uiURL, false)
	}
	return nil
}

func launchWebView(title, uiURL string, _ bool) error {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  filepath.Join(profileDir(), "webview2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  1280,
			Height: 860,
			Center: true,
		},
	})
	if w == nil {
		cmd := exec.Command("cmd", "/c", "start", "", uiURL)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("无法打开窗口（请安装 WebView2 Runtime）: %w", err)
		}
		return nil
	}
	w.Navigate(uiURL)
	w.Run()
	w.Destroy()
	return nil
}

func showError(msg string) {
	t, _ := windows.UTF16PtrFromString("FDT Portal")
	m, _ := windows.UTF16PtrFromString(msg)
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}
