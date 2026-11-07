// Package store is the internal persistence layer: request records, the
// single-frame token lifecycle (lease + one-shot consumption CAS), the
// hash-chained audit log, registered ATM devices and USB operations.
//
// Development runs on SQLite (pure-Go driver). The DDL is written to be
// portable to PostgreSQL for production; swap types as noted in schema.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"fdt/internal/model"
)

// Status is the internal request state (design §7). There is no "approved"
// state internally: the air-gapped intranet never observes the Feishu result.
type Status string

const (
	StatusDraft       Status = "DRAFT"
	StatusPending     Status = "PENDING_APPROVAL"
	StatusRedeemed    Status = "REDEEMED"
	StatusExpired     Status = "EXPIRED"
	StatusCancelled   Status = "CANCELLED"
	StatusQuarantined Status = "QUARANTINED"
	StatusScanning    Status = "SCANNING"
	StatusReleased    Status = "RELEASED"
	StatusBlocked     Status = "BLOCKED"
)

var (
	ErrNotFound    = errors.New("store: not found")
	ErrNotConsumed = errors.New("store: consume affected no row (already consumed)")
	ErrNoLease     = errors.New("store: lease not acquired (in progress elsewhere)")
)

// Request is the persisted ferry request.
type Request struct {
	ID            string
	Dir           model.Direction
	ApplicantID   string
	ApplicantName string
	Dept          string
	Reason        string
	Cls           model.Classification
	Status        Status
	FileCount     int
	TotalSize     int64
	MerkleRoot    string
	ManifestMode  model.ManifestMode
	AVStatus      string
	DLPSummary    json.RawMessage
	// DataDir is the internal quarantine path holding the staged bytes; the data
	// service streams from here to an authenticated ATM. Never leaves the intranet.
	DataDir    string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	FsApproval string
	RedeemedAt time.Time
	RedeemATM  string
	USBSerial  string
	// Extras is custom portal form values (Feishu logical field → value).
	Extras map[string]string
}

// Store wraps a SQL database handle.
type Store struct {
	db     *sql.DB
	driver string // "sqlite" or "postgres"
}

// schema is idempotent. SQLite types shown; Postgres equivalents noted inline.
const schema = `
CREATE TABLE IF NOT EXISTS requests (
  id             TEXT PRIMARY KEY,
  dir            TEXT NOT NULL,
  applicant_id   TEXT, applicant_name TEXT, dept TEXT,
  reason         TEXT, cls TEXT,
  status         TEXT NOT NULL,
  file_count     INTEGER NOT NULL DEFAULT 0,
  total_size     BIGINT  NOT NULL DEFAULT 0,
  merkle_root    TEXT NOT NULL DEFAULT '',
  manifest_mode  TEXT NOT NULL DEFAULT '',
  av_status      TEXT NOT NULL DEFAULT '',
  dlp_summary    TEXT,                    -- jsonb in Postgres
  data_dir       TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,        -- timestamptz in Postgres
  expires_at     INTEGER NOT NULL DEFAULT 0,
  fs_approval    TEXT NOT NULL DEFAULT '',
  redeemed_at    INTEGER NOT NULL DEFAULT 0,
  redeem_atm     TEXT NOT NULL DEFAULT '',
  usb_serial     TEXT NOT NULL DEFAULT '',
  extras         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS request_files (
  request_id TEXT NOT NULL,
  leaf       INTEGER NOT NULL,
  path       TEXT NOT NULL,
  size       BIGINT NOT NULL,
  type       TEXT,
  sha256     TEXT,
  in_archive TEXT,
  PRIMARY KEY (request_id, leaf, path)    -- leaf=-1 members keyed by path
);
CREATE TABLE IF NOT EXISTS tokens (
  request_id   TEXT PRIMARY KEY,
  qr1_nonce    TEXT NOT NULL DEFAULT '',
  qr1_frame    TEXT NOT NULL DEFAULT '',
  qr2_nonce    TEXT NOT NULL DEFAULT '',
  qr2_exp      INTEGER NOT NULL DEFAULT 0,
  lease_atm    TEXT NOT NULL DEFAULT '',
  lease_until  INTEGER NOT NULL DEFAULT 0,
  consumed     INTEGER NOT NULL DEFAULT 0,
  consumed_at  INTEGER NOT NULL DEFAULT 0,
  consumed_atm TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY, name TEXT, location TEXT,
  cert_fingerprint TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active',
  registered_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS usb_ops (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  request_id TEXT, atm_id TEXT, usb_serial TEXT, dir TEXT,
  started_at INTEGER, finished_at INTEGER, written_bytes BIGINT,
  verified_merkle TEXT, result TEXT
);
CREATE TABLE IF NOT EXISTS audit_events (
  seq INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL, actor TEXT, atm_id TEXT, request_id TEXT,
  event TEXT NOT NULL, detail TEXT, prev_hash TEXT NOT NULL, hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_req ON request_files(request_id);
CREATE INDEX IF NOT EXISTS idx_audit_req ON audit_events(request_id);
`

// Open connects to the database and ensures the schema exists.
// driver is "sqlite" (dev) or "postgres" (prod); dsn is driver-specific.
func Open(driver, dsn string) (*Store, error) {
	drv := normalizeDriver(driver)
	db, err := sql.Open(drv, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	ddl := schema
	if drv == "postgres" {
		ddl = schemaPostgres
	}
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, driver: drv}
	s.ensureExtrasColumn()
	return s, nil
}

// ensureExtrasColumn upgrades older DBs created before the extras column existed.
func (s *Store) ensureExtrasColumn() {
	_, _ = s.db.Exec(`ALTER TABLE requests ADD COLUMN extras TEXT NOT NULL DEFAULT ''`)
}

// OpenSQLite opens a SQLite database file (use ":memory:" for tests).
func OpenSQLite(path string) (*Store, error) {
	dsn := "file:" + path
	if path == ":memory:" {
		dsn = "file::memory:"
	} else {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	}
	s, err := Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		// A shared in-memory DB is per-connection; pin to one connection so all
		// queries see the same database.
		s.db.SetMaxOpenConns(1)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func fromUnix(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// CreateRequest inserts a new request row.
func (s *Store) CreateRequest(ctx context.Context, r *Request) error {
	extras := ""
	if len(r.Extras) > 0 {
		b, err := json.Marshal(r.Extras)
		if err != nil {
			return err
		}
		extras = string(b)
	}
	_, err := s.exec(ctx, `
INSERT INTO requests (id,dir,applicant_id,applicant_name,dept,reason,cls,status,
  file_count,total_size,merkle_root,manifest_mode,av_status,dlp_summary,data_dir,
  created_at,expires_at,extras)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, string(r.Dir), r.ApplicantID, r.ApplicantName, r.Dept, r.Reason, string(r.Cls),
		string(r.Status), r.FileCount, r.TotalSize, r.MerkleRoot, string(r.ManifestMode),
		r.AVStatus, nullableJSON(r.DLPSummary), r.DataDir, unix(r.CreatedAt), unix(r.ExpiresAt), extras)
	return err
}

func nullableJSON(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// AddFiles bulk-inserts manifest entries for a request.
func (s *Store) AddFiles(ctx context.Context, reqID string, entries []model.FileEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, s.bind(`
INSERT INTO request_files (request_id,leaf,path,size,type,sha256,in_archive)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT (request_id, leaf, path) DO UPDATE SET
  size=excluded.size, type=excluded.type, sha256=excluded.sha256, in_archive=excluded.in_archive`))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, reqID, e.Leaf, e.Path, e.Size, e.Type, e.HashHex, e.InArchive); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func scanRequest(sc interface{ Scan(...any) error }) (*Request, error) {
	var r Request
	var dir, cls, status, mode, dlp, extras sql.NullString
	var created, expires, redeemed int64
	err := sc.Scan(&r.ID, &dir, &r.ApplicantID, &r.ApplicantName, &r.Dept, &r.Reason, &cls,
		&status, &r.FileCount, &r.TotalSize, &r.MerkleRoot, &mode, &r.AVStatus, &dlp, &r.DataDir,
		&created, &expires, &r.FsApproval, &redeemed, &r.RedeemATM, &r.USBSerial, &extras)
	if err != nil {
		return nil, err
	}
	r.Dir = model.Direction(dir.String)
	r.Cls = model.Classification(cls.String)
	r.Status = Status(status.String)
	r.ManifestMode = model.ManifestMode(mode.String)
	if dlp.Valid {
		r.DLPSummary = json.RawMessage(dlp.String)
	}
	if extras.Valid && extras.String != "" {
		_ = json.Unmarshal([]byte(extras.String), &r.Extras)
	}
	r.CreatedAt = fromUnix(created)
	r.ExpiresAt = fromUnix(expires)
	r.RedeemedAt = fromUnix(redeemed)
	return &r, nil
}

const reqCols = `id,dir,applicant_id,applicant_name,dept,reason,cls,status,file_count,
total_size,merkle_root,manifest_mode,av_status,dlp_summary,data_dir,created_at,
expires_at,fs_approval,redeemed_at,redeem_atm,usb_serial,extras`

// GetRequest loads a request by id.
func (s *Store) GetRequest(ctx context.Context, id string) (*Request, error) {
	row := s.queryRow(ctx, `SELECT `+reqCols+` FROM requests WHERE id=?`, id)
	r, err := scanRequest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// ListRequests returns all requests, newest first.
func (s *Store) ListRequests(ctx context.Context) ([]*Request, error) {
	return s.ListRequestsOpt(ctx, ListRequestsOpt{All: true})
}

// ListRequestsOpt filters portal/history listings.
type ListRequestsOpt struct {
	All          bool     // true → no ownership filter
	ApplicantIDs []string // match requests.applicant_id
	PortalActor  string   // e.g. "portal:zhangsan" via audit request_created_auth
	Limit        int      // 0 → 200
}

// ListRequestsOpt returns requests newest first, optionally scoped to a user.
func (s *Store) ListRequestsOpt(ctx context.Context, opt ListRequestsOpt) ([]*Request, error) {
	limit := opt.Limit
	if limit <= 0 {
		limit = 200
	}
	if opt.All {
		rows, err := s.query(ctx, `SELECT `+reqCols+` FROM requests ORDER BY created_at DESC LIMIT ?`, limit)
		if err != nil {
			return nil, err
		}
		return scanRequestRows(rows)
	}
	ids := uniqueNonEmpty(opt.ApplicantIDs)
	actor := strings.TrimSpace(opt.PortalActor)
	if len(ids) == 0 && actor == "" {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+2)
	var b strings.Builder
	b.WriteString(`SELECT ` + reqCols + ` FROM requests WHERE `)
	parts := 0
	if len(ids) > 0 {
		b.WriteString(`applicant_id IN (`)
		for i, id := range ids {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteByte('?')
			args = append(args, id)
		}
		b.WriteByte(')')
		parts++
	}
	if actor != "" {
		if parts > 0 {
			b.WriteString(` OR `)
		}
		b.WriteString(`id IN (SELECT request_id FROM audit_events WHERE actor=? AND event='request_created_auth')`)
		args = append(args, actor)
		parts++
	}
	b.WriteString(` ORDER BY created_at DESC LIMIT ?`)
	args = append(args, limit)
	rows, err := s.query(ctx, b.String(), args...)
	if err != nil {
		return nil, err
	}
	return scanRequestRows(rows)
}

// RequestOwnedBy reports whether reqID was created by portalActor or matches applicantIDs.
func (s *Store) RequestOwnedBy(ctx context.Context, reqID, portalActor string, applicantIDs []string) (bool, error) {
	req, err := s.GetRequest(ctx, reqID)
	if err != nil {
		return false, err
	}
	for _, id := range applicantIDs {
		if id != "" && req.ApplicantID == id {
			return true, nil
		}
	}
	if portalActor == "" {
		return false, nil
	}
	var n int
	err = s.queryRow(ctx, `
SELECT COUNT(1) FROM audit_events WHERE request_id=? AND actor=? AND event='request_created_auth'`,
		reqID, portalActor).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanRequestRows(rows *sql.Rows) ([]*Request, error) {
	defer rows.Close()
	var out []*Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func uniqueNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// GetRequestFiles returns persisted manifest entries (physical + members).
func (s *Store) GetRequestFiles(ctx context.Context, reqID string) ([]model.FileEntry, error) {
	rows, err := s.query(ctx,
		`SELECT leaf,path,size,type,sha256,in_archive FROM request_files WHERE request_id=? ORDER BY leaf, path`, reqID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FileEntry
	for rows.Next() {
		var e model.FileEntry
		var inArch sql.NullString
		if err := rows.Scan(&e.Leaf, &e.Path, &e.Size, &e.Type, &e.HashHex, &inArch); err != nil {
			return nil, err
		}
		e.InArchive = inArch.String
		out = append(out, e)
	}
	return out, rows.Err()
}
