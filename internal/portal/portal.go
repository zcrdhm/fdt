// Package portal is the intranet upload station: it stages uploaded data into
// quarantine, runs the manifest pipeline, persists the request and mints the
// signed single-frame QR#1 token (design §6).
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
	"fdt/internal/token"
)

var (
	ErrNoStaging         = errors.New("portal: staging root not configured")
	ErrEmptySrc          = errors.New("portal: source directory empty or missing")
	ErrSourceOutsideRoot = errors.New("portal: sourceDir 越界，必须位于 -source-root 之内")
)

// checkSourceRoot confines a dev sourceDir under SourceRoot when configured,
// blocking arbitrary server-directory reads.
func (s *Service) checkSourceRoot(sourceDir string) error {
	if s.SourceRoot == "" {
		return nil // unrestricted (dev); production must set SourceRoot
	}
	rootAbs, err := filepath.Abs(s.SourceRoot)
	if err != nil {
		return err
	}
	srcAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(rootAbs, srcAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrSourceOutsideRoot
	}
	return nil
}

// Service holds the portal dependencies.
type Service struct {
	Store       *store.Store
	Signer      token.Signer // SK_内网
	DLP         manifest.Engine
	Opts        manifest.Options
	StagingRoot string        // quarantine root; "" stages in place (dev only)
	QRTTL       time.Duration // QR#1 validity window
	Tenant      string        // this site's tenant id; stamped into QR#1 (empty => "default")
	// SourceRoot confines dev sourceDir requests: when set, SourceDir must resolve
	// inside it, preventing arbitrary server-directory reads. Production should
	// replace sourceDir with multipart upload entirely (design §3.4).
	SourceRoot string
}

// CreateInput describes a new export request.
type CreateInput struct {
	Applicant model.Applicant
	Reason    string
	Cls       model.Classification
	SourceDir string // legacy: server-local path (must be under SourceRoot when set)
	// StagingDir, when set, is already a quarantine tree for this request
	// (multipart upload). SourceDir is ignored.
	StagingDir string
	// Extras are custom form values keyed by Feishu logical field names.
	Extras map[string]string
}

// CreateResult is returned to the UI: the QR#1 frame plus a human summary.
type CreateResult struct {
	ReqID      string             `json:"reqId"`
	QR1Frame   string             `json:"qr1Frame"`
	Mode       model.ManifestMode `json:"mode"`
	Count      int                `json:"count"`
	TotalSize  int64              `json:"totalSize"`
	MerkleRoot string             `json:"merkleRoot"`
	DLP        model.DLPSummary   `json:"dlp"`
	ExpiresAt  time.Time          `json:"expiresAt"`
}

// NewRequestID returns a random 128-bit hex id (risk M2: no enumerable ids).
func NewRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateExportRequest stages the data, builds the manifest and issues QR#1.
func (s *Service) CreateExportRequest(ctx context.Context, in CreateInput) (*CreateResult, error) {
	if in.StagingDir == "" && in.SourceDir == "" {
		return nil, ErrEmptySrc
	}
	if in.StagingDir == "" {
		if err := s.checkSourceRoot(in.SourceDir); err != nil {
			return nil, err
		}
		if _, err := os.Stat(in.SourceDir); err != nil {
			return nil, fmt.Errorf("portal: source dir: %w", err)
		}
	}
	reqID, err := NewRequestID()
	if err != nil {
		return nil, err
	}
	nonce, err := newNonce()
	if err != nil {
		return nil, err
	}

	// Stage into quarantine so the exported bytes are frozen at approval time.
	var dataDir string
	switch {
	case in.StagingDir != "":
		if s.StagingRoot == "" {
			return nil, ErrNoStaging
		}
		dataDir = filepath.Join(s.StagingRoot, reqID)
		if in.StagingDir != dataDir {
			if err := os.Rename(in.StagingDir, dataDir); err != nil {
				if err := copyDir(in.StagingDir, dataDir); err != nil {
					return nil, fmt.Errorf("portal: stage upload: %w", err)
				}
				_ = os.RemoveAll(in.StagingDir)
			}
		}
	case s.StagingRoot != "":
		dataDir = filepath.Join(s.StagingRoot, reqID)
		if err := copyDir(in.SourceDir, dataDir); err != nil {
			return nil, fmt.Errorf("portal: staging: %w", err)
		}
	default:
		dataDir = in.SourceDir
	}

	res, err := manifest.Build(dataDir, s.DLP, s.Opts)
	if err != nil {
		return nil, err
	}

	ttl := s.QRTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	now := time.Now()
	dlpJSON, _ := json.Marshal(res.Dataset.DLP)

	r := &store.Request{
		ID: reqID, Dir: model.DirExport,
		ApplicantID: in.Applicant.ID, ApplicantName: in.Applicant.Name, Dept: in.Applicant.Dept,
		Reason: in.Reason, Cls: in.Cls, Status: store.StatusPending,
		FileCount: res.Dataset.Count, TotalSize: res.Dataset.TotalSize,
		MerkleRoot: res.Dataset.MerkleRoot, ManifestMode: res.Dataset.Mode,
		AVStatus: res.Dataset.DLP.AV, DLPSummary: dlpJSON, DataDir: dataDir,
		CreatedAt: now, ExpiresAt: now.Add(ttl), Extras: in.Extras,
	}
	if err := s.Store.CreateRequest(ctx, r); err != nil {
		return nil, err
	}
	if err := s.Store.AddFiles(ctx, reqID, res.Entries); err != nil {
		return nil, err
	}
	if err := s.Store.EnsureToken(ctx, reqID, nonce); err != nil {
		return nil, err
	}

	tok := &model.RequestToken{
		ReqID: reqID, Dir: model.DirExport, Applicant: in.Applicant,
		Reason: in.Reason, Cls: in.Cls, Dataset: res.Dataset, Tenant: s.Tenant,
		Extras: in.Extras, Nonce: nonce, Iat: now.Unix(), Exp: now.Add(ttl).Unix(),
	}
	frame, err := token.SignRequest(s.Signer, tok)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SaveQR1Frame(ctx, reqID, frame); err != nil {
		return nil, err
	}
	if _, err := s.Store.AppendAudit(ctx, "portal:"+in.Applicant.ID, "", reqID, "created", map[string]any{
		"mode": res.Dataset.Mode, "count": res.Dataset.Count, "merkle": res.Dataset.MerkleRoot,
	}); err != nil {
		return nil, err
	}
	if _, err := s.Store.AppendAudit(ctx, "portal", "", reqID, "qr1_issued", map[string]any{"exp": tok.Exp}); err != nil {
		return nil, err
	}

	return &CreateResult{
		ReqID: reqID, QR1Frame: frame, Mode: res.Dataset.Mode,
		Count: res.Dataset.Count, TotalSize: res.Dataset.TotalSize,
		MerkleRoot: res.Dataset.MerkleRoot, DLP: res.Dataset.DLP, ExpiresAt: r.ExpiresAt,
	}, nil
}

// GetQR1Frame returns the stored QR#1 frame for display/re-display.
func (s *Service) GetQR1Frame(ctx context.Context, reqID string) (string, error) {
	t, err := s.Store.GetToken(ctx, reqID)
	if err != nil {
		return "", err
	}
	if t.QR1Frame == "" {
		return "", errors.New("portal: QR#1 not issued")
	}
	return t.QR1Frame, nil
}

// copyDir recursively copies src into dst, skipping symlinks (never followed).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
