//go:build !linux && !windows

package atm

import "errors"

// SysfsUSB is only implemented on Linux. Other platforms keep the mock manager.
type SysfsUSB struct {
	SysRoot   string
	MountInfo string
	AutoMount string
}

func (SysfsUSB) Detect() (Device, error) {
	return nil, errors.New("atm: udev USB detection is only supported on linux")
}

// WindowsUSB is only implemented on Windows.
type WindowsUSB struct {
	SkipLetters string
}

func (WindowsUSB) Detect() (Device, error) {
	return nil, errors.New("atm: Windows USB detection is only supported on windows")
}
