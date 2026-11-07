package atm

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fdt/internal/datasvc"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/merkle"
	"fdt/internal/store"
	"fdt/internal/token"
)

// State is the kiosk UI/logic state (design §9.1).
type State string

const (
	StateIdleNoUSB State = "IDLE_NO_USB"
	StateReady     State = "READY"
	StateVerifying State = "VERIFYING"
	StateWriting   State = "WRITING"
	StateDone      State = "DONE"
	StateError     State = "ERROR"
)

var (
	ErrAlreadyConsumed = errors.New("atm: 令牌已核销（重复兑付）")
	ErrRootMismatch    = errors.New("atm: Merkle 根不匹配，数据与审批不符")
	ErrBusy            = errors.New("atm: 兑付进行中（他机持有租约）")
	ErrVerifyFailed    = errors.New("atm: 落盘校验失败，已回滚")
	ErrDuplicate       = errors.New("atm: 重复导出事件（他机已核销）")
	ErrBadPayload      = errors.New("atm: 非法二维码内容")
	ErrTokenInvalid    = errors.New("atm: QR#2 验签或有效期校验失败")
)

// Kiosk is one ferry terminal.
type Kiosk struct {
	ID         string
	Store      *store.Store
	GatewayPub ed25519.PublicKey // PK_网关 — verify QR#2 offline
	Fetcher    DataFetcher
	USB        Manager
	LeaseTTL   time.Duration
	// Import (inbound) wiring; nil Importer disables the import direction.
	Importer   importer.Client
	ImportDLP  manifest.Engine
	ImportOpts manifest.Options
	// Progress, if set, receives write progress (bytes, current file, out dir).
	Progress func(p WriteProgress)

	mu          sync.Mutex
	state       State
	lastErr     error
	importPaths map[string][]string // reqID → selected USB-relative paths for inbound import
}

// WriteProgress is emitted during export redeem / import upload for UI bars.
type WriteProgress struct {
	Phase     string   `json:"phase"` // indexing|creating|uploading|verifying|writing|releasing|done
	Written   int64    `json:"written"`
	Total     int64    `json:"total"`
	FileName  string   `json:"fileName,omitempty"`
	FileIndex int      `json:"fileIndex,omitempty"`
	FileCount int      `json:"fileCount,omitempty"`
	OutDir    string   `json:"outDir,omitempty"`
	Files     []string `json:"files,omitempty"`
}

func NewKiosk(id string, st *store.Store, gwPub ed25519.PublicKey, f DataFetcher, usb Manager) *Kiosk {
	return &Kiosk{ID: id, Store: st, GatewayPub: gwPub, Fetcher: f, USB: usb, LeaseTTL: 90 * time.Second, state: StateIdleNoUSB}
}

// RedeemResult summarizes a successful redemption.
type RedeemResult struct {
	ReqID        string   `json:"reqId"`
	USBSerial    string   `json:"usbSerial"`
	WrittenBytes int64    `json:"writtenBytes"`
	FileCount    int      `json:"fileCount"`
	MerkleRoot   string   `json:"merkleRoot"`
	FsApprovalID string   `json:"fsApprovalId"`
	OutDir       string   `json:"outDir"`
	Files        []string `json:"files,omitempty"`
}

// State returns the current state, refreshing USB presence when idle.
func (k *Kiosk) State() State {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.state == StateIdleNoUSB || k.state == StateReady || k.state == StateError || k.state == StateDone {
		dev, _ := k.USB.Detect()
		if dev == nil {
			k.state = StateIdleNoUSB
		} else if k.state == StateIdleNoUSB || k.state == StateReady || k.state == StateError {
			// ERROR is shown via UI modal; recover so the status bar is not stuck on failure.
			k.state = StateReady
			k.lastErr = nil
		}
	}
	return k.state
}

func (k *Kiosk) setState(s State) {
	k.mu.Lock()
	k.state = s
	k.mu.Unlock()
}

// ProcessScan is the single entry point for a scanned QR#2. It enforces the
// "insert USB before scanning" rule first: with no device present the scan is
// rejected outright (design §9.1). Only after a verified write + readback does
// it consume the token (one-shot, "确认落盘才失效").
func (k *Kiosk) ProcessScan(ctx context.Context, frame string) (*RedeemResult, error) {
	// 1. USB presence gate — before any token parsing.
	dev, err := k.USB.Detect()
	if err != nil {
		return nil, err
	}
	if dev == nil {
		k.setState(StateIdleNoUSB)
		k.recordErr(ctx, "", "scan_rejected_no_usb", ErrNoUSB)
		return nil, ErrNoUSB
	}
	if !dev.Writable() {
		k.fail(ctx, "", dev.Serial(), "usb_not_writable", ErrUSBNotWritable)
		return nil, ErrUSBNotWritable
	}

	// 2. Treat the scan strictly as data; reject anything that is not an FDT frame.
	if !IsFDTPayload(frame) {
		k.fail(ctx, "", dev.Serial(), "bad_payload", ErrBadPayload)
		return nil, ErrBadPayload
	}
	rel, err := token.OpenRelease(strings.TrimSpace(frame), k.GatewayPub)
	if err != nil {
		k.fail(ctx, "", dev.Serial(), "token_invalid", fmt.Errorf("%w: %v", ErrTokenInvalid, err))
		return nil, ErrTokenInvalid
	}
	if err := token.CheckValidity(rel.Iat, rel.Exp, time.Now()); err != nil {
		k.fail(ctx, rel.ReqID, dev.Serial(), "token_expired", err)
		return nil, err
	}

	k.setState(StateVerifying)
	reqID := rel.ReqID
	k.emitProgress(WriteProgress{Phase: "verifying"})

	// 3. Reject an already-consumed token (replay).
	tok, err := k.Store.GetToken(ctx, reqID)
	if err != nil {
		k.fail(ctx, reqID, dev.Serial(), "token_unknown", err)
		return nil, err
	}
	if tok.Consumed {
		k.fail(ctx, reqID, dev.Serial(), "already_consumed", ErrAlreadyConsumed)
		return nil, ErrAlreadyConsumed
	}

	// 4. Pull the expected manifest and cross-check the Merkle root.
	man, err := k.Fetcher.FetchManifest(ctx, reqID)
	if err != nil {
		k.fail(ctx, reqID, dev.Serial(), "manifest_fetch_failed", err)
		return nil, err
	}
	if man.MerkleRoot != rel.MerkleRoot {
		k.fail(ctx, reqID, dev.Serial(), "root_mismatch_manifest", ErrRootMismatch)
		return nil, ErrRootMismatch
	}

	// 5. Capacity / FS pre-check before claiming/writing.
	// Soft-reject only: never Claim/Consume here so the same QR#2 can retry after swapping USB.
	totalSize, fileNames, preErr := checkUSBForExport(dev, man.Files)
	if preErr != nil {
		event := "usb_precheck_failed"
		if errors.Is(preErr, ErrInsufficientSpace) {
			event = "insufficient_space"
		} else if errors.Is(preErr, ErrFilesystemLimit) {
			event = "filesystem_limit"
		}
		k.softReject(ctx, reqID, dev.Serial(), event, preErr)
		return nil, preErr
	}

	// 6. Lease to narrow the concurrent-double-write window.
	got, err := k.Store.Claim(ctx, reqID, k.ID, k.LeaseTTL)
	if err != nil {
		k.fail(ctx, reqID, dev.Serial(), "claim_error", err)
		return nil, err
	}
	if !got {
		k.fail(ctx, reqID, dev.Serial(), "busy", ErrBusy)
		return nil, ErrBusy
	}

	// 7. Stream + write + hash.
	k.setState(StateWriting)
	started := time.Now()
	outDir := filepath.Join(dev.MountPath(), "fdt-"+reqID[:8])
	k.emitProgress(WriteProgress{
		Phase: "writing", Total: totalSize, OutDir: outDir,
		FileCount: len(fileNames), Files: fileNames,
	})
	written, fileCount, rootHex, err := k.streamAndWrite(ctx, reqID, outDir, totalSize, fileNames)
	if err != nil {
		_ = os.RemoveAll(outDir) // rollback partial write
		_ = k.Store.Release(ctx, reqID, k.ID)
		k.fail(ctx, reqID, dev.Serial(), "write_failed", err)
		return nil, err
	}
	if rootHex != rel.MerkleRoot {
		_ = os.RemoveAll(outDir)
		_ = k.Store.Release(ctx, reqID, k.ID)
		k.fail(ctx, reqID, dev.Serial(), "root_mismatch_written", ErrRootMismatch)
		return nil, ErrRootMismatch
	}

	// 8. Read-back verification: recompute the root from what is actually on the USB.
	readback, err := datasvc.DirMerkleRoot(outDir)
	if err != nil || readback != rel.MerkleRoot {
		_ = os.RemoveAll(outDir)
		_ = k.Store.Release(ctx, reqID, k.ID)
		k.fail(ctx, reqID, dev.Serial(), "readback_mismatch",
			fmt.Errorf("%w: readback=%s err=%v", ErrVerifyFailed, readback, err))
		return nil, ErrVerifyFailed
	}

	// 9. One-shot consume CAS — the token dies only now, on confirmed landing.
	ok, err := k.Store.Consume(ctx, reqID, k.ID, dev.Serial(), rel.FsApprovalID, readback)
	if err != nil {
		k.fail(ctx, reqID, dev.Serial(), "consume_error", err)
		return nil, err
	}
	if !ok {
		// Another ATM redeemed it first: this is a duplicate-egress incident.
		_ = os.RemoveAll(outDir)
		_ = k.recordUSBOp(ctx, reqID, dev.Serial(), started, written, readback, "duplicate")
		k.fail(ctx, reqID, dev.Serial(), "duplicate_egress", ErrDuplicate)
		return nil, ErrDuplicate
	}

	_ = k.recordUSBOp(ctx, reqID, dev.Serial(), started, written, readback, "success")
	if _, err := k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, reqID, "redeemed", map[string]any{
		"usbSerial": dev.Serial(), "writtenBytes": written, "fileCount": fileCount,
		"merkleRoot": readback, "fsApprovalId": rel.FsApprovalID,
	}); err != nil {
		return nil, err
	}
	k.setState(StateDone)
	k.mu.Lock()
	k.lastErr = nil
	k.mu.Unlock()
	k.emitProgress(WriteProgress{
		Phase: "done", Written: written, Total: totalSize, OutDir: outDir,
		FileCount: fileCount, Files: fileNames,
	})
	return &RedeemResult{
		ReqID: reqID, USBSerial: dev.Serial(), WrittenBytes: written, FileCount: fileCount,
		MerkleRoot: readback, FsApprovalID: rel.FsApprovalID, OutDir: outDir, Files: fileNames,
	}, nil
}

func (k *Kiosk) emitProgress(p WriteProgress) {
	if k.Progress != nil {
		k.Progress(p)
	}
}

// streamAndWrite pulls the tar and extracts it to outDir, hashing each file.
// It returns bytes written, file count and the recomputed Merkle root hex.
func (k *Kiosk) streamAndWrite(ctx context.Context, reqID, outDir string, total int64, planned []string) (int64, int, string, error) {
	rc, err := k.Fetcher.FetchTar(ctx, reqID)
	if err != nil {
		return 0, 0, "", err
	}
	defer rc.Close()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, 0, "", err
	}
	tr := tar.NewReader(rc)
	type leaf struct {
		path string
		hash []byte
	}
	var leaves []leaf
	var written int64
	count := 0
	fileTotal := len(planned)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, "", err
		}
		// zip-slip guard: resolve within outDir only.
		clean, ok := safeJoin(outDir, hdr.Name)
		if !ok {
			return 0, 0, "", fmt.Errorf("atm: unsafe tar path %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, 0o755); err != nil {
				return 0, 0, "", err
			}
			continue
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return 0, 0, "", err
			}
			relName := filepath.ToSlash(hdr.Name)
			idx := count + 1
			baseWritten := written
			h, n, err := writeHashFile(clean, tr, func(delta int64) {
				cur := baseWritten + delta
				k.emitProgress(WriteProgress{
					Phase: "writing", Written: cur, Total: total,
					FileName: relName, FileIndex: idx, FileCount: fileTotal,
					OutDir: outDir, Files: planned,
				})
			})
			if err != nil {
				return 0, 0, "", err
			}
			written += n
			count++
			rel, _ := filepath.Rel(outDir, clean)
			leaves = append(leaves, leaf{path: filepath.ToSlash(rel), hash: h})
			k.emitProgress(WriteProgress{
				Phase: "writing", Written: written, Total: total,
				FileName: relName, FileIndex: count, FileCount: fileTotal,
				OutDir: outDir, Files: planned,
			})
		default:
			// symlinks/devices are not written (matches manifest which skipped them)
			continue
		}
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path < leaves[j].path })
	hashes := make([][]byte, len(leaves))
	for i, l := range leaves {
		hashes[i] = l.hash
	}
	root, err := merkle.Root(hashes)
	if err != nil {
		return 0, 0, "", err
	}
	return written, count, hex.EncodeToString(root), nil
}

func writeHashFile(path string, r io.Reader, onDelta func(n int64)) ([]byte, int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 256*1024)
	var n int64
	var sinceReport int64
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			nw, ew := f.Write(buf[:nr])
			if nw > 0 {
				_, _ = h.Write(buf[:nw])
				n += int64(nw)
				sinceReport += int64(nw)
				if onDelta != nil && sinceReport >= 512*1024 {
					onDelta(n)
					sinceReport = 0
				}
			}
			if ew != nil {
				return nil, n, ew
			}
			if nr != nw {
				return nil, n, io.ErrShortWrite
			}
		}
		if er == io.EOF {
			break
		}
		if er != nil {
			return nil, n, er
		}
	}
	if onDelta != nil {
		onDelta(n)
	}
	if err := f.Sync(); err != nil {
		return nil, 0, err
	}
	return h.Sum(nil), n, nil
}

// safeJoin resolves name under root, rejecting absolute paths and traversal.
func safeJoin(root, name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
		return "", false
	}
	clean := filepath.Clean(filepath.Join(root, filepath.FromSlash(name)))
	if clean != filepath.Clean(root) && !strings.HasPrefix(clean, filepath.Clean(root)+string(os.PathSeparator)) {
		return "", false
	}
	return clean, true
}

func (k *Kiosk) recordUSBOp(ctx context.Context, reqID, serial string, started time.Time, written int64, root, result string) error {
	return k.Store.RecordUSBOp(ctx, &store.USBOp{
		RequestID: reqID, ATMID: k.ID, USBSerial: serial, Dir: "export",
		StartedAt: started, FinishedAt: time.Now(), WrittenBytes: written,
		VerifiedMerkle: root, Result: result,
	})
}

func (k *Kiosk) fail(ctx context.Context, reqID, serial, event string, err error) {
	k.setState(StateError)
	k.mu.Lock()
	k.lastErr = err
	k.mu.Unlock()
	_, _ = k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, reqID, event, map[string]string{
		"usbSerial": serial, "err": err.Error(),
	})
}

// softReject records a recoverable USB/media problem without consuming the token.
// State stays READY so the operator can swap the stick and scan the same QR#2 again.
func (k *Kiosk) softReject(ctx context.Context, reqID, serial, event string, err error) {
	k.mu.Lock()
	k.lastErr = err
	k.state = StateReady
	k.mu.Unlock()
	_, _ = k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, reqID, event, map[string]string{
		"usbSerial": serial, "err": err.Error(), "tokenConsumed": "false",
	})
}

func (k *Kiosk) recordErr(ctx context.Context, reqID, event string, err error) {
	k.mu.Lock()
	k.lastErr = err
	k.mu.Unlock()
	_, _ = k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, reqID, event, map[string]string{"err": err.Error()})
}

// LastErr returns the most recent error for display in the kiosk UI.
func (k *Kiosk) LastErr() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.lastErr
}
