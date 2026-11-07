package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Device is a registered ATM kiosk. Only registered devices with a valid
// certificate fingerprint may pull data from the data service (risk M1).
type Device struct {
	ID              string
	Name            string
	Location        string
	CertFingerprint string
	Status          string
	RegisteredAt    time.Time
}

func (s *Store) RegisterDevice(ctx context.Context, d *Device) error {
	_, err := s.exec(ctx, `
INSERT INTO devices (id,name,location,cert_fingerprint,status,registered_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT (id) DO UPDATE SET name=excluded.name, location=excluded.location,
  cert_fingerprint=excluded.cert_fingerprint, status=excluded.status`,
		d.ID, d.Name, d.Location, d.CertFingerprint, orDefault(d.Status, "active"), unix(time.Now()))
	return err
}

func (s *Store) GetDevice(ctx context.Context, id string) (*Device, error) {
	var d Device
	var registered int64
	err := s.queryRow(ctx,
		`SELECT id,name,location,cert_fingerprint,status,registered_at FROM devices WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Location, &d.CertFingerprint, &d.Status, &registered)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.RegisteredAt = fromUnix(registered)
	return &d, nil
}

// USBOp records one export/import attempt at an ATM.
type USBOp struct {
	RequestID      string
	ATMID          string
	USBSerial      string
	Dir            string
	StartedAt      time.Time
	FinishedAt     time.Time
	WrittenBytes   int64
	VerifiedMerkle string
	Result         string // success | failed | duplicate | blocked | rolledback
}

func (s *Store) RecordUSBOp(ctx context.Context, op *USBOp) error {
	_, err := s.exec(ctx, `
INSERT INTO usb_ops (request_id,atm_id,usb_serial,dir,started_at,finished_at,written_bytes,verified_merkle,result)
VALUES (?,?,?,?,?,?,?,?,?)`,
		op.RequestID, op.ATMID, op.USBSerial, op.Dir, unix(op.StartedAt), unix(op.FinishedAt),
		op.WrittenBytes, op.VerifiedMerkle, op.Result)
	return err
}

// SetStatus transitions a request's internal state (design §7).
func (s *Store) SetStatus(ctx context.Context, reqID string, st Status) error {
	res, err := s.exec(ctx, `UPDATE requests SET status=? WHERE id=?`, string(st), reqID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetDataDir updates the on-disk path for a request (e.g. quarantine → released).
func (s *Store) SetDataDir(ctx context.Context, reqID, dir string) error {
	res, err := s.exec(ctx, `UPDATE requests SET data_dir=? WHERE id=?`, dir, reqID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpireStale marks still-pending requests past their expiry as EXPIRED.
func (s *Store) ExpireStale(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.exec(ctx,
		`UPDATE requests SET status=? WHERE status=? AND expires_at>0 AND expires_at<?`,
		string(StatusExpired), string(StatusPending), unix(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
