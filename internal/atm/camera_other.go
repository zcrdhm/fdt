//go:build !linux

package atm

import (
	"context"
	"fmt"
)

// CameraScanner is unavailable off Linux (no V4L2).
type CameraScanner struct {
	Device string
}

// OpenCamera reports that camera scan needs Linux.
func OpenCamera(path string) (*CameraScanner, error) {
	return nil, fmt.Errorf("atm: camera scan is only supported on Linux (got path %q)", path)
}

// Read always fails off Linux.
func (c *CameraScanner) Read(ctx context.Context) (string, error) {
	return "", fmt.Errorf("atm: camera scan is only supported on Linux")
}
