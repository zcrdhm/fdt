package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"fdt/internal/feishu"
	"fdt/internal/model"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// SessionStore persists approval sessions so a gateway restart can still hand
// out a QR#2 that was minted but not yet scanned.
type SessionStore interface {
	Save(ctx context.Context, sess *session) error
	LoadAll(ctx context.Context) ([]*session, error)
}

const sessionSchema = `
CREATE TABLE IF NOT EXISTS gateway_sessions (
  req_id TEXT PRIMARY KEY,
  tenant TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  req_token TEXT NOT NULL,
  manifest_text TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  approver TEXT NOT NULL DEFAULT '',
  qr2_frame TEXT NOT NULL DEFAULT '',
  qr2_exp BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_gw_inst ON gateway_sessions(instance_id);
`

// SQLSessionStore is the durable session store (SQLite or Postgres).
type SQLSessionStore struct {
	db     *sql.DB
	driver string
}

// OpenSessionStore opens driver ("sqlite" or "postgres") at dsn and creates the table.
func OpenSessionStore(driver, dsn string) (*SQLSessionStore, error) {
	drv := driver
	switch strings.ToLower(driver) {
	case "", "sqlite", "sqlite3":
		drv = "sqlite"
	case "pgx", "postgresql":
		drv = "postgres"
	}
	db, err := sql.Open(drv, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionSchema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLSessionStore{db: db, driver: drv}, nil
}

func (s *SQLSessionStore) Close() error { return s.db.Close() }

func (s *SQLSessionStore) bind(q string) string {
	if s.driver != "postgres" {
		return q
	}
	var b strings.Builder
	n := 1
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		b.WriteByte(q[i])
	}
	return b.String()
}

func (s *SQLSessionStore) Save(ctx context.Context, sess *session) error {
	if sess == nil || sess.ReqToken == nil {
		return nil
	}
	raw, err := json.Marshal(sess.ReqToken)
	if err != nil {
		return err
	}
	var exp int64
	if !sess.QR2Exp.IsZero() {
		exp = sess.QR2Exp.Unix()
	}
	_, err = s.db.ExecContext(ctx, s.bind(`
INSERT INTO gateway_sessions
  (req_id,tenant,instance_id,req_token,manifest_text,status,approver,qr2_frame,qr2_exp)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT (req_id) DO UPDATE SET
  tenant=excluded.tenant, instance_id=excluded.instance_id, req_token=excluded.req_token,
  manifest_text=excluded.manifest_text, status=excluded.status, approver=excluded.approver,
  qr2_frame=excluded.qr2_frame, qr2_exp=excluded.qr2_exp`),
		sess.ReqToken.ReqID, sess.Tenant, sess.InstanceID, string(raw), sess.ManifestText,
		string(sess.Status), sess.Approver, sess.QR2Frame, exp)
	return err
}

func (s *SQLSessionStore) LoadAll(ctx context.Context) ([]*session, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT req_id,tenant,instance_id,req_token,manifest_text,status,approver,qr2_frame,qr2_exp
FROM gateway_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*session
	for rows.Next() {
		var reqID, tenant, inst, raw, manifest, status, approver, qr2 string
		var exp int64
		if err := rows.Scan(&reqID, &tenant, &inst, &raw, &manifest, &status, &approver, &qr2, &exp); err != nil {
			return nil, err
		}
		var tok model.RequestToken
		if err := json.Unmarshal([]byte(raw), &tok); err != nil {
			return nil, err
		}
		if tok.ReqID == "" {
			tok.ReqID = reqID
		}
		sess := &session{
			Tenant: tenant, ReqToken: &tok, InstanceID: inst, ManifestText: manifest,
			Status: feishu.ApprovalStatus(status), Approver: approver, QR2Frame: qr2,
		}
		if exp != 0 {
			sess.QR2Exp = time.Unix(exp, 0)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}
