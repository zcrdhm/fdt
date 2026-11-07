package importer

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fdt/internal/merkle"
	"fdt/internal/model"
	"fdt/internal/store"
	"fdt/internal/token"
)

var (
	ErrImportMismatch = errors.New("importer: 上传数据与已批准的 Merkle 指纹不符（疑似审批后换盘）")
	ErrBlocked        = errors.New("importer: 扫描命中，已拦截，未释放")
	ErrAlreadyDone    = errors.New("importer: 该导入已处理")
	ErrWrongDirection = errors.New("importer: 令牌方向不是导入")
	ErrReqMismatch    = errors.New("importer: 令牌与申请单不匹配")
)

// Service handles the inbound direction on the intranet.
type Service struct {
	Store          *store.Store
	Signer         token.Signer      // SK_内网 — sign import QR#1
	GatewayPub     ed25519.PublicKey // PK_网关 — verify QR#2 server-side
	Scanner        Scanner           // AV/DLP
	QuarantineRoot string            // isolated landing zone
	TargetRoot     string            // released destination
	Tenant         string
	QRTTL          time.Duration
	LeaseTTL       time.Duration
}

// ImportRequest is what the ATM sends after indexing the source USB.
type ImportRequest struct {
	Applicant model.Applicant
	Reason    string
	Cls       model.Classification
	Dataset   model.Dataset     // manifest built by the ATM from the USB
	Entries   []model.FileEntry // full per-file detail to persist
	USBSerial string
}

// ImportCreateResult is returned to the ATM: the QR#1 frame to show for scanning.
type ImportCreateResult struct {
	ReqID      string             `json:"reqId"`
	QR1Frame   string             `json:"qr1Frame"`
	Mode       model.ManifestMode `json:"mode"`
	Count      int                `json:"count"`
	MerkleRoot string             `json:"merkleRoot"`
}

func newHexID(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateImportRequest records the import request and signs QR#1 over the
// ATM-supplied manifest (whose Merkle root binds the exact bytes to be uploaded).
func (s *Service) CreateImportRequest(ctx context.Context, in ImportRequest) (*ImportCreateResult, error) {
	reqID, err := newHexID(16)
	if err != nil {
		return nil, err
	}
	nonce, err := newHexID(16)
	if err != nil {
		return nil, err
	}
	ttl := s.QRTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	now := time.Now()
	quarantine := filepath.Join(s.QuarantineRoot, reqID)

	r := &store.Request{
		ID: reqID, Dir: model.DirImport,
		ApplicantID: in.Applicant.ID, ApplicantName: in.Applicant.Name, Dept: in.Applicant.Dept,
		Reason: in.Reason, Cls: in.Cls, Status: store.StatusPending,
		FileCount: in.Dataset.Count, TotalSize: in.Dataset.TotalSize,
		MerkleRoot: in.Dataset.MerkleRoot, ManifestMode: in.Dataset.Mode,
		AVStatus: in.Dataset.DLP.AV, DataDir: quarantine,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if err := s.Store.CreateRequest(ctx, r); err != nil {
		return nil, err
	}
	if err := s.Store.AddFiles(ctx, reqID, in.Entries); err != nil {
		return nil, err
	}
	if err := s.Store.EnsureToken(ctx, reqID, nonce); err != nil {
		return nil, err
	}

	tok := &model.RequestToken{
		ReqID: reqID, Dir: model.DirImport, Applicant: in.Applicant,
		Reason: in.Reason, Cls: in.Cls, Dataset: in.Dataset, Tenant: s.Tenant,
		Nonce: nonce, Iat: now.Unix(), Exp: now.Add(ttl).Unix(),
	}
	frame, err := token.SignRequest(s.Signer, tok)
	if err != nil {
		return nil, err
	}
	if err := s.Store.SaveQR1Frame(ctx, reqID, frame); err != nil {
		return nil, err
	}
	if _, err := s.Store.AppendAudit(ctx, "importer", "", reqID, "import_created", map[string]any{
		"usbSerial": in.USBSerial, "count": in.Dataset.Count, "merkle": in.Dataset.MerkleRoot,
	}); err != nil {
		return nil, err
	}
	return &ImportCreateResult{
		ReqID: reqID, QR1Frame: frame, Mode: in.Dataset.Mode,
		Count: in.Dataset.Count, MerkleRoot: in.Dataset.MerkleRoot,
	}, nil
}

// ImportResult is the outcome of a release attempt.
type ImportResult struct {
	ReqID        string       `json:"reqId"`
	Status       store.Status `json:"status"` // RELEASED | BLOCKED
	ReleasedDir  string       `json:"releasedDir,omitempty"`
	Released     []string     `json:"released,omitempty"`
	Findings     []string     `json:"findings,omitempty"`
	WrittenBytes int64        `json:"writtenBytes"`
	MerkleRoot   string       `json:"merkleRoot"`
}

// ReceiveAndRelease verifies QR#2, ingests the uploaded tar into quarantine,
// re-hashes it (must equal the approved Merkle root — this is the anti-swap
// check), scans, and releases or blocks. Consumption is one-shot via CAS.
func (s *Service) ReceiveAndRelease(ctx context.Context, reqID, qr2Frame string, body io.Reader, atmID string) (*ImportResult, error) {
	rel, err := token.OpenRelease(qr2Frame, s.GatewayPub)
	if err != nil {
		return nil, fmt.Errorf("importer: QR#2 verify: %w", err)
	}
	if err := token.CheckValidity(rel.Iat, rel.Exp, time.Now()); err != nil {
		return nil, err
	}
	if rel.ReqID != reqID {
		return nil, ErrReqMismatch
	}
	if rel.Dir != model.DirImport {
		return nil, ErrWrongDirection
	}

	req, err := s.Store.GetRequest(ctx, reqID)
	if err != nil {
		return nil, err
	}
	if req.Dir != model.DirImport {
		return nil, ErrWrongDirection
	}
	if req.Status == store.StatusReleased || req.Status == store.StatusBlocked {
		return nil, ErrAlreadyDone
	}
	tok, err := s.Store.GetToken(ctx, reqID)
	if err != nil {
		return nil, err
	}
	if tok.Consumed {
		return nil, ErrAlreadyDone
	}
	leaseTTL := s.LeaseTTL
	if leaseTTL == 0 {
		leaseTTL = 5 * time.Minute
	}
	if ok, err := s.Store.Claim(ctx, reqID, atmID, leaseTTL); err != nil || !ok {
		return nil, fmt.Errorf("importer: claim lease: ok=%v err=%v", ok, err)
	}

	started := time.Now()
	_ = s.Store.SetStatus(ctx, reqID, store.StatusQuarantined)
	quarantine := filepath.Join(s.QuarantineRoot, reqID)
	_ = os.RemoveAll(quarantine)
	if err := os.MkdirAll(quarantine, 0o755); err != nil {
		return nil, err
	}

	written, rootHex, err := extractAndHash(body, quarantine)
	if err != nil {
		_ = os.RemoveAll(quarantine)
		_ = s.Store.Release(ctx, reqID, atmID)
		_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_ingest_failed", map[string]string{"err": err.Error()})
		return nil, err
	}
	// Anti-swap: uploaded bytes must match the fingerprint that was approved.
	if rootHex != req.MerkleRoot {
		_ = os.RemoveAll(quarantine)
		_ = s.Store.Release(ctx, reqID, atmID)
		_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_merkle_mismatch", map[string]string{
			"expected": req.MerkleRoot, "got": rootHex,
		})
		return nil, ErrImportMismatch
	}

	_ = s.Store.SetStatus(ctx, reqID, store.StatusScanning)
	scan, err := s.Scanner.Scan(ctx, quarantine)
	if err != nil {
		_ = s.Store.Release(ctx, reqID, atmID)
		_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_scan_failed", map[string]string{"err": err.Error()})
		return nil, err
	}
	if !scan.Clean {
		_ = s.Store.SetStatus(ctx, reqID, store.StatusBlocked)
		_ = s.Store.RecordUSBOp(ctx, &store.USBOp{
			RequestID: reqID, ATMID: atmID, Dir: "import", StartedAt: started,
			FinishedAt: time.Now(), WrittenBytes: written, VerifiedMerkle: rootHex, Result: "blocked",
		})
		_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_blocked", map[string]any{
			"findings": scan.Findings, "engine": scan.Engine,
		})
		// Quarantine is retained for forensics; lease released, token NOT consumed.
		_ = s.Store.Release(ctx, reqID, atmID)
		return &ImportResult{ReqID: reqID, Status: store.StatusBlocked, Findings: scan.Findings,
			WrittenBytes: written, MerkleRoot: rootHex}, ErrBlocked
	}

	// Release: move quarantine -> target/<reqId>.
	target := filepath.Join(s.TargetRoot, reqID)
	released, err := moveDir(quarantine, target)
	if err != nil {
		_ = s.Store.Release(ctx, reqID, atmID)
		return nil, err
	}
	ok, err := s.Store.Consume(ctx, reqID, atmID, "", rel.FsApprovalID, rootHex)
	if err != nil {
		return nil, err
	}
	if !ok {
		_ = os.RemoveAll(target)
		_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_duplicate", nil)
		return nil, ErrAlreadyDone
	}
	_ = s.Store.SetStatus(ctx, reqID, store.StatusReleased)
	_ = s.Store.SetDataDir(ctx, reqID, target)
	_ = s.Store.RecordUSBOp(ctx, &store.USBOp{
		RequestID: reqID, ATMID: atmID, Dir: "import", StartedAt: started,
		FinishedAt: time.Now(), WrittenBytes: written, VerifiedMerkle: rootHex, Result: "success",
	})
	_, _ = s.Store.AppendAudit(ctx, "importer", atmID, reqID, "import_released", map[string]any{
		"target": target, "files": len(released), "merkle": rootHex, "fsApprovalId": rel.FsApprovalID,
		"scanEngine": scan.Engine,
	})
	return &ImportResult{ReqID: reqID, Status: store.StatusReleased, ReleasedDir: target,
		Released: released, WrittenBytes: written, MerkleRoot: rootHex}, nil
}

// extractAndHash writes a tar into dest, hashing each file, and returns bytes
// written plus the recomputed Merkle root hex (leaves sorted by relative path).
func extractAndHash(r io.Reader, dest string) (int64, string, error) {
	tr := tar.NewReader(r)
	type leaf struct {
		path string
		hash []byte
	}
	var leaves []leaf
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, "", err
		}
		clean, ok := safeJoin(dest, hdr.Name)
		if !ok {
			return 0, "", fmt.Errorf("importer: unsafe tar path %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, 0o755); err != nil {
				return 0, "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return 0, "", err
			}
			h, n, err := writeHashFile(clean, tr)
			if err != nil {
				return 0, "", err
			}
			written += n
			relp, _ := filepath.Rel(dest, clean)
			leaves = append(leaves, leaf{path: filepath.ToSlash(relp), hash: h})
		default:
			continue // skip symlinks/devices, matching the manifest which skipped them
		}
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path < leaves[j].path })
	hashes := make([][]byte, len(leaves))
	for i, l := range leaves {
		hashes[i] = l.hash
	}
	root, err := merkle.Root(hashes)
	if err != nil {
		return 0, "", err
	}
	return written, hex.EncodeToString(root), nil
}

func writeHashFile(path string, r io.Reader) ([]byte, int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err != nil {
		return nil, 0, err
	}
	if err := f.Sync(); err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

func safeJoin(root, name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(name)))
	rc := filepath.Clean(root)
	if clean != rc && !strings.HasPrefix(clean, rc+string(os.PathSeparator)) {
		return "", false
	}
	return clean, true
}

// moveDir relocates src to dst (rename when possible, else copy+remove) and
// returns the relative file list now under dst.
func moveDir(src, dst string) ([]string, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		// cross-device: fall back to copy
		if cerr := copyTree(src, dst); cerr != nil {
			return nil, cerr
		}
		_ = os.RemoveAll(src)
	}
	var files []string
	err := filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dst, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
