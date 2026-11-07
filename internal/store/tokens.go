package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Token is the persisted lifecycle row for a request's QR tokens.
type Token struct {
	RequestID   string
	QR1Nonce    string
	QR1Frame    string
	QR2Nonce    string
	QR2Exp      time.Time
	LeaseATM    string
	LeaseUntil  time.Time
	Consumed    bool
	ConsumedAt  time.Time
	ConsumedATM string
}

// EnsureToken creates the token row if absent (idempotent).
func (s *Store) EnsureToken(ctx context.Context, reqID, qr1Nonce string) error {
	_, err := s.exec(ctx, `
INSERT INTO tokens (request_id, qr1_nonce) VALUES (?,?)
ON CONFLICT (request_id) DO UPDATE SET qr1_nonce=excluded.qr1_nonce`, reqID, qr1Nonce)
	return err
}

// SaveQR1Frame persists the generated single-frame QR#1 string.
func (s *Store) SaveQR1Frame(ctx context.Context, reqID, frame string) error {
	_, err := s.exec(ctx, `UPDATE tokens SET qr1_frame=? WHERE request_id=?`, frame, reqID)
	return err
}

// SetRelease records the QR#2 nonce/expiry once the gateway has issued it. In a
// fully air-gapped deployment this is called by the ATM when it first sees a
// valid QR#2 (the intranet never learns of approval from the gateway directly).
func (s *Store) SetRelease(ctx context.Context, reqID, qr2Nonce string, exp time.Time) error {
	_, err := s.exec(ctx, `
UPDATE tokens SET qr2_nonce=?, qr2_exp=? WHERE request_id=?`, qr2Nonce, unix(exp), reqID)
	return err
}

// Claim acquires a short lease so two ATMs cannot both write the same request.
// Returns true if the lease was granted. A holder may renew by claiming again.
func (s *Store) Claim(ctx context.Context, reqID, atmID string, ttl time.Duration) (bool, error) {
	now := time.Now()
	res, err := s.exec(ctx, `
UPDATE tokens
SET lease_atm=?, lease_until=?
WHERE request_id=? AND consumed=0 AND (lease_until < ? OR lease_atm=?)`,
		atmID, unix(now.Add(ttl)), reqID, unix(now), atmID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// Release drops a lease without consuming (used on write failure / rollback).
func (s *Store) Release(ctx context.Context, reqID, atmID string) error {
	_, err := s.exec(ctx, `
UPDATE tokens SET lease_atm='', lease_until=0 WHERE request_id=? AND lease_atm=? AND consumed=0`,
		reqID, atmID)
	return err
}

// Consume performs the one-shot redemption CAS. It succeeds only if the token
// has not already been consumed; on success it also marks the request REDEEMED.
// This is the "确认落盘才失效" rule: callers invoke it only after the data has
// been written to USB and re-read/verified. A false result means another ATM
// already redeemed it (a duplicate-egress incident to be alarmed by the caller).
func (s *Store) Consume(ctx context.Context, reqID, atmID, usbSerial, fsApproval, verifiedRoot string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	now := time.Now()
	res, err := tx.ExecContext(ctx, s.bind(`
UPDATE tokens SET consumed=1, consumed_at=?, consumed_atm=?, lease_atm='', lease_until=0
WHERE request_id=? AND consumed=0`), unix(now), atmID, reqID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil // already consumed elsewhere
	}
	if _, err := tx.ExecContext(ctx, s.bind(`
UPDATE requests SET status=?, redeemed_at=?, redeem_atm=?, usb_serial=?, fs_approval=?, merkle_root=?
WHERE id=?`), string(StatusRedeemed), unix(now), atmID, usbSerial, fsApproval, verifiedRoot, reqID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetToken loads the token lifecycle row.
func (s *Store) GetToken(ctx context.Context, reqID string) (*Token, error) {
	var t Token
	var qr2exp, leaseUntil, consumedAt int64
	var consumed int
	err := s.queryRow(ctx, `
SELECT request_id,qr1_nonce,qr1_frame,qr2_nonce,qr2_exp,lease_atm,lease_until,consumed,consumed_at,consumed_atm
FROM tokens WHERE request_id=?`, reqID).
		Scan(&t.RequestID, &t.QR1Nonce, &t.QR1Frame, &t.QR2Nonce, &qr2exp, &t.LeaseATM, &leaseUntil,
			&consumed, &consumedAt, &t.ConsumedATM)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.QR2Exp = fromUnix(qr2exp)
	t.LeaseUntil = fromUnix(leaseUntil)
	t.ConsumedAt = fromUnix(consumedAt)
	t.Consumed = consumed == 1
	return &t, nil
}
