//go:build windows

package main

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL = 13
	wmKeydown    = 0x0100
	wmSyskeydown = 0x0104
	vkLwin       = 0x5B
	vkRwin       = 0x5C
	vkEscape     = 0x1B
	vkF4         = 0x73
	vkTab        = 0x09
	vkLmenu      = 0xA4
	vkRmenu      = 0xA5
	vkControl    = 0x11
	vkDelete     = 0x2E
	llkhfAltDown = 0x20
)

type kbdLLHookStruct struct {
	VKCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	hookMu     sync.Mutex
	hookHandle windows.Handle
	callNext   *windows.LazyProc
)

func installKioskKeyboardHook() (func(), error) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	setHook := user32.NewProc("SetWindowsHookExW")
	unhook := user32.NewProc("UnhookWindowsHookEx")
	callNext = user32.NewProc("CallNextHookEx")
	getMod := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

	cb := syscall.NewCallback(lowLevelKeyboardProc)
	mod, _, _ := getMod.Call(0)
	h, _, err := setHook.Call(uintptr(whKeyboardLL), cb, mod, 0)
	if h == 0 {
		return nil, fmt.Errorf("SetWindowsHookEx: %v", err)
	}
	hookMu.Lock()
	hookHandle = windows.Handle(h)
	hookMu.Unlock()

	return func() {
		hookMu.Lock()
		h := hookHandle
		hookHandle = 0
		hookMu.Unlock()
		if h != 0 {
			_, _, _ = unhook.Call(uintptr(h))
		}
	}, nil
}

func lowLevelKeyboardProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= 0 && (wParam == wmKeydown || wParam == wmSyskeydown) {
		kb := (*kbdLLHookStruct)(unsafe.Pointer(lParam))
		vk := kb.VKCode
		alt := kb.Flags&llkhfAltDown != 0 || keyDown(vkLmenu) || keyDown(vkRmenu)
		ctrl := keyDown(vkControl)

		block := false
		switch vk {
		case vkLwin, vkRwin, vkEscape:
			block = true
		case vkF4:
			if alt {
				block = true
			}
		case vkTab:
			if alt {
				block = true
			}
		}
		if ctrl && vk == vkEscape {
			block = true
		}
		if ctrl && alt && vk == vkDelete {
			block = true
		}
		if block {
			return 1
		}
	}
	hookMu.Lock()
	h := hookHandle
	hookMu.Unlock()
	r, _, _ := callNext.Call(uintptr(h), uintptr(nCode), wParam, lParam)
	return r
}

func keyDown(vk uint32) bool {
	user32 := windows.NewLazySystemDLL("user32.dll")
	getAsync := user32.NewProc("GetAsyncKeyState")
	r, _, _ := getAsync.Call(uintptr(vk))
	return r&0x8000 != 0
}

func hardenWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	getLong := user32.NewProc("GetWindowLongW")
	setLong := user32.NewProc("SetWindowLongW")
	setPos := user32.NewProc("SetWindowPos")
	show := user32.NewProc("ShowWindow")
	const (
		gwlStyle        = ^uintptr(15) // -16
		wsSysmenu       = 0x00080000
		wsMinimizeBox   = 0x00020000
		wsMaximizeBox   = 0x00010000
		swpNoSize       = 0x0001
		swpNoMove       = 0x0002
		swpNoZOrder     = 0x0004
		swpFrameChanged = 0x0020
		swMaximize      = 3
	)
	style, _, _ := getLong.Call(hwnd, gwlStyle)
	style &^= wsSysmenu | wsMinimizeBox | wsMaximizeBox
	_, _, _ = setLong.Call(hwnd, gwlStyle, style)
	_, _, _ = setPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoZOrder|swpFrameChanged)
	_, _, _ = show.Call(hwnd, swMaximize)
}

// installDesktopHotkeyShield is a no-op on Windows (LL hook covers Win key).
func installDesktopHotkeyShield() func() { return func() {} }
