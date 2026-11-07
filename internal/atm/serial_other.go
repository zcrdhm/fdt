//go:build !linux

package atm

import "errors"

// OpenSerial is only implemented on Linux.
func OpenSerial(string, int) (*SerialScanner, error) {
	return nil, errors.New("atm: serial scanner is only supported on linux")
}
