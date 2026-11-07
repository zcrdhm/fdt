package store

// schemaPostgres is the production DDL. It matches schema (SQLite) in columns
// and constraints. Timestamps stay unix seconds (bigint). usb_ops.id is
// bigserial because inserts omit it; audit seq is assigned by the application.
const schemaPostgres = `
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
  dlp_summary    TEXT,
  data_dir       TEXT NOT NULL DEFAULT '',
  created_at     BIGINT NOT NULL,
  expires_at     BIGINT NOT NULL DEFAULT 0,
  fs_approval    TEXT NOT NULL DEFAULT '',
  redeemed_at    BIGINT NOT NULL DEFAULT 0,
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
  PRIMARY KEY (request_id, leaf, path)
);
CREATE TABLE IF NOT EXISTS tokens (
  request_id   TEXT PRIMARY KEY,
  qr1_nonce    TEXT NOT NULL DEFAULT '',
  qr1_frame    TEXT NOT NULL DEFAULT '',
  qr2_nonce    TEXT NOT NULL DEFAULT '',
  qr2_exp      BIGINT NOT NULL DEFAULT 0,
  lease_atm    TEXT NOT NULL DEFAULT '',
  lease_until  BIGINT NOT NULL DEFAULT 0,
  consumed     INTEGER NOT NULL DEFAULT 0,
  consumed_at  BIGINT NOT NULL DEFAULT 0,
  consumed_atm TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS devices (
  id TEXT PRIMARY KEY, name TEXT, location TEXT,
  cert_fingerprint TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active',
  registered_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS usb_ops (
  id BIGSERIAL PRIMARY KEY,
  request_id TEXT, atm_id TEXT, usb_serial TEXT, dir TEXT,
  started_at BIGINT, finished_at BIGINT, written_bytes BIGINT,
  verified_merkle TEXT, result TEXT
);
CREATE TABLE IF NOT EXISTS audit_events (
  seq BIGINT PRIMARY KEY,
  ts BIGINT NOT NULL, actor TEXT, atm_id TEXT, request_id TEXT,
  event TEXT NOT NULL, detail TEXT, prev_hash TEXT NOT NULL, hash TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_files_req ON request_files(request_id);
CREATE INDEX IF NOT EXISTS idx_audit_req ON audit_events(request_id);
`
