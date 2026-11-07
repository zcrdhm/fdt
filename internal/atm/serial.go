package atm

import (
	"bufio"
	"context"
	"io"
	"strings"
	"unicode"
)

// SerialScanner reads newline-delimited barcodes from r. Control characters are
// stripped and only strings that match the FDT frame prefix are returned, so a
// scanner (or a hostile QR) cannot inject a shell command.
type SerialScanner struct {
	r io.Reader
}

// NewSerialScanner wraps an already-opened port or test reader.
func NewSerialScanner(r io.Reader) *SerialScanner { return &SerialScanner{r: r} }

// Read blocks until a well-formed FDT frame is scanned or ctx is done.
func (s *SerialScanner) Read(ctx context.Context) (string, error) {
	lines := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(s.r)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if frame, ok := SanitizeScan(sc.Text()); ok {
				lines <- frame
				return
			}
		}
		if err := sc.Err(); err != nil {
			errs <- err
			return
		}
		errs <- io.EOF
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case err := <-errs:
		return "", err
	case line := <-lines:
		return line, nil
	}
}

// SanitizeScan strips control characters and accepts only an FDT frame.
func SanitizeScan(s string) (string, bool) {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	if !IsFDTPayload(cleaned) {
		return "", false
	}
	return cleaned, true
}
