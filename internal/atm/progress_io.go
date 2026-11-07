package atm

import (
	"io"
	"sync"
	"time"
)

// progressReader counts bytes read and emits WriteProgress (throttled).
type progressReader struct {
	r     io.Reader
	total int64
	phase string
	emit  func(WriteProgress)

	mu      sync.Mutex
	written int64
	lastAt  time.Time
	lastN   int64
}

func newProgressReader(r io.Reader, total int64, phase string, emit func(WriteProgress)) *progressReader {
	return &progressReader{r: r, total: total, phase: phase, emit: emit}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.emit != nil {
		p.mu.Lock()
		p.written += int64(n)
		written, total := p.written, p.total
		now := time.Now()
		due := written-p.lastN >= 256*1024 || now.Sub(p.lastAt) >= 200*time.Millisecond || err != nil
		if due {
			p.lastAt = now
			p.lastN = written
			p.mu.Unlock()
			p.emit(WriteProgress{Phase: p.phase, Written: written, Total: total})
		} else {
			p.mu.Unlock()
		}
	}
	if err == io.EOF && p.emit != nil {
		p.mu.Lock()
		written, total := p.written, p.total
		p.mu.Unlock()
		if total > 0 && written < total {
			written = total
		}
		p.emit(WriteProgress{Phase: p.phase, Written: written, Total: total})
	}
	return n, err
}
