package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// AuditEvent is one link in the tamper-evident chain (design §10).
type AuditEvent struct {
	Seq       int64
	Ts        time.Time
	Actor     string
	ATMID     string
	RequestID string
	Event     string
	Detail    json.RawMessage
	PrevHash  string
	Hash      string
}

var ErrAuditChainBroken = errors.New("store: audit hash chain verification failed")

// AppendAudit adds an event, chaining its hash to the previous event so any
// edit or deletion is detectable. Production should mirror this to WORM storage.
func (s *Store) AppendAudit(ctx context.Context, actor, atmID, reqID, event string, detail any) (string, error) {
	var detailJSON []byte
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			return "", err
		}
		detailJSON = b
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var prevHash string
	var lastSeq int64
	err = tx.QueryRowContext(ctx, s.bind(`SELECT seq, hash FROM audit_events ORDER BY seq DESC LIMIT 1`)).
		Scan(&lastSeq, &prevHash)
	if errors.Is(err, sql.ErrNoRows) {
		prevHash, lastSeq = "", 0
	} else if err != nil {
		return "", err
	}
	seq := lastSeq + 1
	now := time.Now()
	hash := auditHash(seq, now, actor, atmID, reqID, event, detailJSON, prevHash)

	if _, err := tx.ExecContext(ctx, s.bind(`
INSERT INTO audit_events (seq,ts,actor,atm_id,request_id,event,detail,prev_hash,hash)
VALUES (?,?,?,?,?,?,?,?,?)`),
		seq, now.UnixNano(), actor, atmID, reqID, event, nullableJSON(detailJSON), prevHash, hash); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return hash, nil
}

func auditHash(seq int64, ts time.Time, actor, atmID, reqID, event string, detail []byte, prev string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d|%d|%s|%s|%s|%s|%s|%s",
		seq, ts.UnixNano(), actor, atmID, reqID, event, string(detail), prev)
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyAuditChain recomputes every hash to detect tampering.
func (s *Store) VerifyAuditChain(ctx context.Context) error {
	rows, err := s.query(ctx, `
SELECT seq,ts,actor,atm_id,request_id,event,detail,prev_hash,hash FROM audit_events ORDER BY seq ASC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var prevHash string
	var expectSeq int64 = 1
	for rows.Next() {
		var e AuditEvent
		var detail sql.NullString
		var ts int64
		if err := rows.Scan(&e.Seq, &ts, &e.Actor, &e.ATMID, &e.RequestID, &e.Event,
			&detail, &e.PrevHash, &e.Hash); err != nil {
			return err
		}
		e.Ts = fromUnixNano(ts)
		if detail.Valid {
			e.Detail = json.RawMessage(detail.String)
		}
		if e.Seq != expectSeq {
			return fmt.Errorf("%w: seq gap at %d", ErrAuditChainBroken, e.Seq)
		}
		if e.PrevHash != prevHash {
			return fmt.Errorf("%w: prev-hash mismatch at seq %d", ErrAuditChainBroken, e.Seq)
		}
		want := auditHash(e.Seq, e.Ts, e.Actor, e.ATMID, e.RequestID, e.Event, e.Detail, e.PrevHash)
		if want != e.Hash {
			return fmt.Errorf("%w: hash mismatch at seq %d", ErrAuditChainBroken, e.Seq)
		}
		prevHash = e.Hash
		expectSeq++
	}
	return rows.Err()
}

// RecentAudit returns the last n events (newest first) for display.
func (s *Store) RecentAudit(ctx context.Context, n int) ([]*AuditEvent, error) {
	rows, err := s.query(ctx, `
SELECT seq,ts,actor,atm_id,request_id,event,detail,prev_hash,hash
FROM audit_events ORDER BY seq DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditEvent
	for rows.Next() {
		var e AuditEvent
		var detail sql.NullString
		var ts int64
		if err := rows.Scan(&e.Seq, &ts, &e.Actor, &e.ATMID, &e.RequestID, &e.Event,
			&detail, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		e.Ts = fromUnixNano(ts)
		if detail.Valid {
			e.Detail = json.RawMessage(detail.String)
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

func fromUnixNano(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
