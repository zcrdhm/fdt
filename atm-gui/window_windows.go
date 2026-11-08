//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

func openWindow(title, uiURL string) error {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  filepath.Join(profileDir(), "webview2"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  1280,
			Height: 800,
			Center: true,
		},
	})
	if w == nil {
		return fmt.Errorf("无法创建 WebView2 窗口（请安装 Microsoft Edge WebView2 Runtime）")
	}
	defer w.Destroy()
	w.Navigate(uiURL)
	w.Run()
	return nil
}

func showError(msg string) {
	t, _ := windows.UTF16PtrFromString("FDT ATM")
	m, _ := windows.UTF16PtrFromString(msg)
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10) // MB_ICONERROR
}

func acquireSingleInstance() (func(), error) {
	name, err := windows.UTF16PtrFromString("Local\\FDT_ATM_GUI_SINGLETON")
	if err != nil {
		return func() {}, nil
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		if err == windows.ERROR_ALREADY_EXISTS {
			return nil, fmt.Errorf("already running")
		}
		// Some systems return ERROR_ALREADY_EXISTS via GetLastError after success.
		if errno, ok := err.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			return nil, fmt.Errorf("already running")
		}
	}
	if h == 0 {
		return func() {}, nil
	}
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("already running")
	}
	return func() { _ = windows.CloseHandle(h) }, nil
}
