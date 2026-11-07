//go:build linux

package atm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CameraScanner captures frames from a V4L2 webcam (via ffmpeg) and decodes
// FDT QR frames with gozxing. Prefer a USB CDC serial scanner in production;
// camera mode is for kiosks without a dedicated gun (or as a backup).
//
// Requires ffmpeg on PATH (common on ATM images). Kept CGO-free so the
// single-binary release stays CGO_ENABLED=0.
type CameraScanner struct {
	Device   string
	Width    int
	Height   int
	Interval time.Duration
}

// OpenCamera prepares a camera scanner for path (e.g. /dev/video0).
// The device is opened lazily on the first Read so callers can show a prompt first.
func OpenCamera(path string) (*CameraScanner, error) {
	if path == "" {
		path = "/dev/video0"
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("atm: camera %s: %w", path, err)
	}
	if !hasFFmpeg() {
		return nil, fmt.Errorf("atm: camera scan needs ffmpeg on PATH (install ffmpeg)")
	}
	return &CameraScanner{
		Device:   path,
		Width:    640,
		Height:   480,
		Interval: 250 * time.Millisecond,
	}, nil
}

// Read blocks until an FDT QR is decoded from the camera or ctx is done.
func (c *CameraScanner) Read(ctx context.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("atm: nil camera")
	}
	w, h := c.Width, c.Height
	if w == 0 {
		w = 640
	}
	if h == 0 {
		h = 480
	}
	interval := c.Interval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	size := fmt.Sprintf("%dx%d", w, h)

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		frame, err := c.grabFrame(ctx, size)
		if err == nil {
			if text, err := DecodeQRBytes(frame); err == nil {
				return text, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(interval):
		}
	}
}

func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func (c *CameraScanner) grabFrame(ctx context.Context, size string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "v4l2",
		"-video_size", size,
		"-i", c.Device,
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("atm: ffmpeg capture: %s", msg)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("atm: ffmpeg produced empty frame")
	}
	return stdout.Bytes(), nil
}
