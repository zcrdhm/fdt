package atm

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"fdt/internal/codec"
)

var ErrNoScan = errors.New("atm: no scan available")

// Scanner yields raw scanned strings. The kiosk treats every scan strictly as
// DATA (validated + signature-verified before use) and never routes it to a
// shell, mitigating HID/QR injection (risk H1). Production scanners should run
// in USB CDC/serial mode; the mock feeds frames directly.
type Scanner interface {
	// Read blocks until a code is scanned or ctx is done.
	Read(ctx context.Context) (string, error)
}

// MockScanner replays a queue of preset frames.
type MockScanner struct {
	mu    sync.Mutex
	queue []string
}

func NewMockScanner(frames ...string) *MockScanner {
	return &MockScanner{queue: frames}
}

func (m *MockScanner) Push(frame string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, frame)
}

func (m *MockScanner) Read(ctx context.Context) (string, error) {
	for {
		m.mu.Lock()
		if len(m.queue) > 0 {
			f := m.queue[0]
			m.queue = m.queue[1:]
			m.mu.Unlock()
			return f, nil
		}
		m.mu.Unlock()
		// A real scanner blocks on serial I/O; the mock polls briefly.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// IsFDTPayload reports whether a scanned string looks like an FDT token frame.
// Anything else is rejected before cryptographic parsing (defence in depth
// against injected keystrokes / unrelated codes).
func IsFDTPayload(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), codec.Magic)
}
