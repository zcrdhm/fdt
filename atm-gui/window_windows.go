//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	activeWVMu sync.Mutex
	activeWV   webview2.WebView
)

// openWindow opens a normal closable window (first-run setup wizard).
func openWindow(title, uiURL string) error {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		DataPath:  filepath.Join(profileDir(), "webview2-setup"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  960,
			Height: 720,
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

// openKioskWindow runs the ATM GUI with anti-exit: keyboard hook, no close button,
// and automatic relaunch if the window is closed without a maint-authenticated quit.
func openKioskWindow(title, uiURL string) error {
	unhook, err := installKioskKeyboardHook()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: 键盘钩子未启用: %v\n", err)
	} else {
		defer unhook()
	}
	restore := installDesktopHotkeyShield()
	defer restore()

	go func() {
		<-quitSignal()
		activeWVMu.Lock()
		w := activeWV
		activeWVMu.Unlock()
		if w != nil {
			w.Terminate()
		}
	}()

	for !shouldQuit() {
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
		hardenWindow(uintptr(w.Window()))

		activeWVMu.Lock()
		activeWV = w
		activeWVMu.Unlock()

		w.Navigate(uiURL)
		w.Run()

		activeWVMu.Lock()
		activeWV = nil
		activeWVMu.Unlock()
		w.Destroy()

		if shouldQuit() {
			break
		}
		fmt.Fprintln(os.Stderr, "ATM 窗口被关闭，正在重新打开…（维护模式「退出程序」+ 密码才能真正退出）")
		time.Sleep(400 * time.Millisecond)
	}
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
