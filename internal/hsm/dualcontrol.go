package hsm

import (
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

var (
	ErrQuorumNotMet = errors.New("hsm: dual-control quorum not met")
	ErrDuplicate    = errors.New("hsm: approver already authorized this round")
)

// DualControlSigner wraps a Signer and requires m distinct approvals within a
// time window before each Sign succeeds. This models the two-person rule for
// minting QR#2 release tokens (technical design §3.1 / risk C2).
type DualControlSigner struct {
	inner   Signer
	m       int
	window  time.Duration
	mu      sync.Mutex
	round   map[string]time.Time
	pending bool
}

func NewDualControlSigner(inner Signer, m int, window time.Duration) *DualControlSigner {
	if m < 1 {
		m = 1
	}
	return &DualControlSigner{inner: inner, m: m, window: window, round: map[string]time.Time{}}
}

// Approve records one authorization for the current signing round.
func (d *DualControlSigner) Approve(by string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gc()
	if _, ok := d.round[by]; ok {
		return ErrDuplicate
	}
	d.round[by] = time.Now()
	d.pending = true
	return nil
}

func (d *DualControlSigner) gc() {
	cutoff := time.Now().Add(-d.window)
	for k, t := range d.round {
		if t.Before(cutoff) {
			delete(d.round, k)
		}
	}
}

// Sign only proceeds once quorum is reached; it then resets the round so each
// release token needs fresh approvals.
func (d *DualControlSigner) Sign(msg []byte) ([]byte, error) {
	d.mu.Lock()
	d.gc()
	if len(d.round) < d.m {
		d.mu.Unlock()
		return nil, ErrQuorumNotMet
	}
	d.round = map[string]time.Time{}
	d.pending = false
	d.mu.Unlock()
	return d.inner.Sign(msg)
}

func (d *DualControlSigner) KID() string               { return d.inner.KID() }
func (d *DualControlSigner) Public() ed25519.PublicKey { return d.inner.Public() }

// Pending reports whether any approvals are collected but quorum not yet met.
func (d *DualControlSigner) Pending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending
}
