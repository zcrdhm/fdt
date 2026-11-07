// Package atm implements the ferry terminal (kiosk): the state machine that
// enforces "insert USB before scanning", verifies QR#2 offline against PK_网关,
// pulls data from the intranet data service, writes it to the USB while
// re-hashing, verifies the Merkle root, and only then performs the one-shot
// consume (design §9).
//
// USB and scanner access go through interfaces. Mock implementations are
// provided so the full flow runs offline; production plugs in udev-backed USB
// handling and a serial-mode scanner.
package atm

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrNoUSB             = errors.New("atm: 未检测到USB存储设备，请先插入U盘")
	ErrUSBNotWritable    = errors.New("atm: U盘只读或写保护")
	ErrInsufficientSpace = errors.New("atm: U盘可用空间不足")
)

// Device is an inserted USB storage device.
type Device interface {
	Serial() string
	// MountPath is where the device filesystem is mounted (writable target).
	MountPath() string
	Writable() bool
	CapacityBytes() int64
	FreeBytes() int64
	// FileSystem reports the fs type (e.g. vfat/exfat/ntfs) for large-file checks.
	FileSystem() string
	Eject() error
}

// Manager detects USB insertion/removal.
type Manager interface {
	// Detect returns the currently inserted device or nil if none.
	Detect() (Device, error)
}

// MockDevice is a directory-backed USB device for development/tests.
type MockDevice struct {
	serial    string
	mountPath string
	writable  bool
	capacity  int64
	fs        string
}

func (d *MockDevice) Serial() string       { return d.serial }
func (d *MockDevice) MountPath() string    { return d.mountPath }
func (d *MockDevice) Writable() bool       { return d.writable }
func (d *MockDevice) CapacityBytes() int64 { return d.capacity }
func (d *MockDevice) FileSystem() string   { return d.fs }

func (d *MockDevice) FreeBytes() int64 {
	var used int64
	_ = filepath.WalkDir(d.mountPath, func(_ string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		if fi, ierr := e.Info(); ierr == nil {
			used += fi.Size()
		}
		return nil
	})
	free := d.capacity - used
	if free < 0 {
		free = 0
	}
	return free
}

func (d *MockDevice) Eject() error { return nil }

// MockManager simulates hot-plug: Insert/Remove drive Detect results.
type MockManager struct {
	mu     sync.Mutex
	device *MockDevice
}

func NewMockManager() *MockManager { return &MockManager{} }

// Insert simulates plugging in a USB backed by dir (created if absent).
func (m *MockManager) Insert(serial, dir string, capacity int64, writable bool, fs string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.device = &MockDevice{serial: serial, mountPath: dir, writable: writable, capacity: capacity, fs: fs}
	return nil
}

// Remove simulates unplugging the USB.
func (m *MockManager) Remove() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.device = nil
}

func (m *MockManager) Detect() (Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.device == nil {
		return nil, nil // no device present
	}
	return m.device, nil
}
