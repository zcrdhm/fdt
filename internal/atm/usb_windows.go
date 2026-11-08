//go:build windows

package atm

import (
	"encoding/binary"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

const (
	fileReadOnlyVolume        = 0x00080000
	ioctlStorageQueryProperty = 0x002d1400 // CTL_CODE(IOCTL_STORAGE_BASE, 0x500, METHOD_BUFFERED, FILE_ANY_ACCESS)
	ioctlStorageMediaRemoval  = 0x002d4804
	ioctlStorageEjectMedia    = 0x002d4808 // CTL_CODE(IOCTL_STORAGE_BASE, 0x202, METHOD_BUFFERED, FILE_READ_ACCESS)
	fsctlLockVolume           = 0x00090018
	fsctlDismountVolume       = 0x00090020
	busTypeUSB                = 0x07
)

// SysfsUSB is Linux-only; stub keeps atmapp references compiling on Windows.
type SysfsUSB struct {
	SysRoot   string
	MountInfo string
	AutoMount string
}

func (SysfsUSB) Detect() (Device, error) {
	return nil, fmt.Errorf("atm: udev USB detection is only supported on linux")
}

// WindowsUSB detects removable / USB-attached volumes by drive letter (D:–Z:).
// C: and typical floppy letters A:/B: are skipped. No CGO required.
type WindowsUSB struct {
	// SkipLetters are roots to ignore (default "ABC").
	SkipLetters string
}

func (m WindowsUSB) skipSet() map[byte]bool {
	s := m.SkipLetters
	if s == "" {
		s = "ABC"
	}
	out := map[byte]bool{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c >= 'A' && c <= 'Z' {
			out[c] = true
		}
	}
	return out
}

// Detect returns the first suitable USB stick volume, or (nil, nil) if none.
func (m WindowsUSB) Detect() (Device, error) {
	skip := m.skipSet()
	for letter := byte('A'); letter <= 'Z'; letter++ {
		if skip[letter] {
			continue
		}
		root := string(letter) + `:\`
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		dt := windows.GetDriveType(rootPtr)
		switch dt {
		case windows.DRIVE_REMOVABLE:
			// OK — typical USB flash.
		case windows.DRIVE_FIXED:
			// Some USB disks appear as FIXED; accept only USB bus.
			if !isUSBDriveLetter(letter) {
				continue
			}
		default:
			continue
		}
		dev, err := probeWinVolume(letter, root, rootPtr)
		if err != nil || dev == nil {
			continue
		}
		return dev, nil
	}
	return nil, nil
}

func probeWinVolume(letter byte, root string, rootPtr *uint16) (*winDevice, error) {
	var (
		volName [261]uint16
		fsName  [261]uint16
		serial  uint32
		maxComp uint32
		flags   uint32
	)
	if err := windows.GetVolumeInformation(rootPtr, &volName[0], uint32(len(volName)), &serial, &maxComp, &flags, &fsName[0], uint32(len(fsName))); err != nil {
		// Not ready (empty card reader, etc.).
		return nil, nil
	}
	var freeCaller, total, freeTotal uint64
	if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeCaller, &total, &freeTotal); err != nil {
		return nil, nil
	}
	fs := strings.ToLower(windows.UTF16ToString(fsName[:]))
	serialStr := fmt.Sprintf("%08X", serial)
	if hw := hardwareSerial(letter); hw != "" {
		serialStr = hw
	}
	writable := flags&fileReadOnlyVolume == 0
	return &winDevice{
		serial:   serialStr,
		mount:    root,
		writable: writable,
		capacity: int64(total),
		free:     int64(freeCaller),
		fs:       fs,
		letter:   letter,
	}, nil
}

func isUSBDriveLetter(letter byte) bool {
	path := `\\.\` + string(letter) + `:`
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(pathPtr, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	// STORAGE_PROPERTY_QUERY: PropertyId=StorageDeviceProperty(0), QueryType=PropertyStandardQuery(0)
	query := make([]byte, 12)
	buf := make([]byte, 1024)
	var ret uint32
	if err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, &query[0], uint32(len(query)), &buf[0], uint32(len(buf)), &ret, nil); err != nil {
		return false
	}
	if ret < 29 {
		return false
	}
	bus := buf[28]
	removable := buf[10] != 0
	return bus == busTypeUSB || removable
}

func hardwareSerial(letter byte) string {
	path := `\\.\` + string(letter) + `:`
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	h, err := windows.CreateFile(pathPtr, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	query := make([]byte, 12)
	buf := make([]byte, 1024)
	var ret uint32
	if err := windows.DeviceIoControl(h, ioctlStorageQueryProperty, &query[0], uint32(len(query)), &buf[0], uint32(len(buf)), &ret, nil); err != nil {
		return ""
	}
	if ret < 28 {
		return ""
	}
	off := binary.LittleEndian.Uint32(buf[24:28]) // SerialNumberOffset
	if off == 0 || int(off) >= int(ret) {
		return ""
	}
	// Serial is a null-terminated ANSI string at offset.
	s := string(buf[off:ret])
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

type winDevice struct {
	serial   string
	mount    string
	writable bool
	capacity int64
	free     int64
	fs       string
	letter   byte
}

func (d *winDevice) Serial() string       { return d.serial }
func (d *winDevice) MountPath() string    { return d.mount }
func (d *winDevice) Writable() bool       { return d.writable }
func (d *winDevice) CapacityBytes() int64 { return d.capacity }
func (d *winDevice) FreeBytes() int64     { return d.free }
func (d *winDevice) FileSystem() string   { return d.fs }

func (d *winDevice) Eject() error {
	// 1) Shell "Eject" — works for standard users (no admin); same as Explorer 弹出.
	if err := shellEject(d.letter); err == nil {
		return nil
	} else if err2 := ioctlEject(d.letter); err2 == nil {
		return nil
	} else {
		return fmt.Errorf("atm: 安全拔出 %s 失败（请关闭占用该盘的窗口后再试）: %v / %v", d.mount, err, err2)
	}
}

func shellEject(letter byte) error {
	drive := string(letter) + ":"
	// Namespace 17 = "This PC"; InvokeVerb('Eject') matches Explorer.
	ps := fmt.Sprintf(
		`$ErrorActionPreference='Stop'; $d='%s'; $s=New-Object -ComObject Shell.Application; $n=$s.NameSpace(17).ParseName($d); if($null -eq $n){ throw 'volume not found' }; $n.InvokeVerb('Eject'); Start-Sleep -Milliseconds 400`,
		drive,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", ps)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("shell eject: %s", msg)
	}
	return nil
}

func ioctlEject(letter byte) error {
	path := `\\.\` + string(letter) + `:`
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	// Prefer read+write for lock/dismount; fall back to 0-access open.
	h, err := windows.CreateFile(pathPtr, windows.GENERIC_READ|windows.GENERIC_WRITE, share, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		h, err = windows.CreateFile(pathPtr, 0, share, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return fmt.Errorf("open volume: %w", err)
		}
	}
	defer windows.CloseHandle(h)

	var ret uint32
	// Best-effort flush / lock / dismount before eject (ignore lock failures — volume may be busy).
	_ = windows.DeviceIoControl(h, fsctlLockVolume, nil, 0, nil, 0, &ret, nil)
	_ = windows.DeviceIoControl(h, fsctlDismountVolume, nil, 0, nil, 0, &ret, nil)
	allow := []byte{0} // PREVENT_MEDIA_REMOVAL.PreventMediaRemoval = FALSE
	_ = windows.DeviceIoControl(h, ioctlStorageMediaRemoval, &allow[0], 1, nil, 0, &ret, nil)
	if err := windows.DeviceIoControl(h, ioctlStorageEjectMedia, nil, 0, nil, 0, &ret, nil); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}
