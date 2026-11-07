package atm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fdt/internal/datasvc"
	"fdt/internal/importer"
	"fdt/internal/manifest"
	"fdt/internal/model"
	"fdt/internal/store"
	"fdt/internal/token"
)

var (
	ErrImportNotConfigured = errors.New("atm: 导入未配置（Importer 为空）")
	ErrImportTokenInvalid  = errors.New("atm: 导入 QR#2 校验失败")
	ErrImportNoSelection   = errors.New("atm: 请先从 U 盘选择要导入的文件或目录")
)

// ImportStartResult is returned after indexing the source USB and creating the
// import request: the QR#1 frame to show for scanning.
type ImportStartResult struct {
	ReqID      string
	QR1Frame   string
	Mode       model.ManifestMode
	Count      int
	MerkleRoot string
	USBSerial  string
	Paths      []string
}

// StartImport indexes selected paths on the inserted source USB, builds the
// manifest, and asks the intranet importer to create the request + sign QR#1.
func (k *Kiosk) StartImport(ctx context.Context, applicant model.Applicant, reason string, cls model.Classification, paths []string) (*ImportStartResult, error) {
	if k.Importer == nil {
		return nil, ErrImportNotConfigured
	}
	dev, err := k.USB.Detect()
	if err != nil {
		return nil, err
	}
	if dev == nil {
		k.setState(StateIdleNoUSB)
		k.recordErr(ctx, "", "import_no_usb", ErrNoUSB)
		return nil, ErrNoUSB
	}
	if len(paths) == 0 {
		return nil, ErrImportNoSelection
	}
	paths = normalizeImportPaths(paths)
	if len(paths) == 0 {
		return nil, ErrImportNoSelection
	}

	dlp := k.ImportDLP
	if dlp == nil {
		dlp = manifest.NoopDLP{}
	}
	res, err := manifest.BuildPaths(dev.MountPath(), paths, dlp, k.ImportOpts)
	if err != nil {
		err = fmt.Errorf("%w", friendlyUSBReadErr(err))
		k.fail(ctx, "", dev.Serial(), "import_index_failed", err)
		return nil, err
	}

	out, err := k.Importer.CreateImport(ctx, importer.ImportRequest{
		Applicant: applicant, Reason: reason, Cls: cls,
		Dataset: res.Dataset, Entries: res.Entries, USBSerial: dev.Serial(),
	})
	if err != nil {
		k.fail(ctx, "", dev.Serial(), "import_create_failed", err)
		return nil, err
	}
	k.setImportPaths(out.ReqID, paths)
	_, _ = k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, out.ReqID, "import_indexed", map[string]any{
		"usbSerial": dev.Serial(), "count": out.Count, "merkle": out.MerkleRoot, "mode": out.Mode,
		"paths": paths,
	})
	return &ImportStartResult{
		ReqID: out.ReqID, QR1Frame: out.QR1Frame, Mode: out.Mode,
		Count: out.Count, MerkleRoot: out.MerkleRoot, USBSerial: dev.Serial(),
		Paths: paths,
	}, nil
}

// CompleteImport verifies QR#2 locally (fail fast), then streams the selected
// source paths to the intranet importer which re-hashes (anti-swap), scans and releases.
func (k *Kiosk) CompleteImport(ctx context.Context, reqID, qr2Frame string) (*importer.ImportResult, error) {
	if k.Importer == nil {
		return nil, ErrImportNotConfigured
	}
	dev, err := k.USB.Detect()
	if err != nil {
		return nil, err
	}
	if dev == nil {
		k.setState(StateIdleNoUSB)
		k.recordErr(ctx, reqID, "import_no_usb", ErrNoUSB)
		return nil, ErrNoUSB
	}
	if !IsFDTPayload(qr2Frame) {
		k.fail(ctx, reqID, dev.Serial(), "bad_payload", ErrBadPayload)
		return nil, ErrBadPayload
	}
	// Local pre-check: reject bad/expired/wrong-direction tokens before streaming.
	rel, err := token.OpenRelease(qr2Frame, k.GatewayPub)
	if err != nil {
		k.fail(ctx, reqID, dev.Serial(), "token_invalid", fmt.Errorf("%w: %v", ErrImportTokenInvalid, err))
		return nil, ErrImportTokenInvalid
	}
	if err := token.CheckValidity(rel.Iat, rel.Exp, time.Now()); err != nil {
		k.fail(ctx, reqID, dev.Serial(), "token_expired", err)
		return nil, err
	}
	if rel.ReqID != reqID || rel.Dir != model.DirImport {
		k.fail(ctx, reqID, dev.Serial(), "import_token_mismatch", ErrImportTokenInvalid)
		return nil, ErrImportTokenInvalid
	}

	paths := k.getImportPaths(reqID)
	if len(paths) == 0 {
		return nil, ErrImportNoSelection
	}

	k.setState(StateWriting)
	started := time.Now()
	pr, pw := io.Pipe()
	go func() {
		werr := datasvc.WriteTarPaths(pw, dev.MountPath(), paths)
		_ = pw.CloseWithError(werr)
	}()

	res, err := k.Importer.UploadAndRelease(ctx, reqID, qr2Frame, pr)
	if err != nil {
		result := "failed"
		if errors.Is(err, importer.ErrBlocked) {
			result = "blocked"
		}
		_ = k.recordImportOp(ctx, reqID, dev.Serial(), started, result)
		k.fail(ctx, reqID, dev.Serial(), "import_"+result, err)
		return res, err
	}
	k.clearImportPaths(reqID)
	_ = k.recordImportOp(ctx, reqID, dev.Serial(), started, "released")
	if _, aerr := k.Store.AppendAudit(ctx, "atm:"+k.ID, k.ID, reqID, "import_released", map[string]any{
		"usbSerial": dev.Serial(), "target": res.ReleasedDir, "files": len(res.Released),
	}); aerr != nil {
		return res, aerr
	}
	k.setState(StateDone)
	k.mu.Lock()
	k.lastErr = nil
	k.mu.Unlock()
	return res, nil
}

func (k *Kiosk) setImportPaths(reqID string, paths []string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.importPaths == nil {
		k.importPaths = map[string][]string{}
	}
	cp := append([]string(nil), paths...)
	k.importPaths[reqID] = cp
}

func (k *Kiosk) getImportPaths(reqID string) []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.importPaths[reqID]...)
}

func (k *Kiosk) clearImportPaths(reqID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.importPaths, reqID)
}

func friendlyUSBReadErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "input/output error") || strings.Contains(msg, "I/O error") {
		return fmt.Errorf("U 盘读取失败（目录可能损坏或有坏块）：%v。请只勾选具体文件，或避开无法打开的目录后重试", err)
	}
	return err
}

func normalizeImportPaths(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range in {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
		p = strings.TrimPrefix(p, "/")
		p = filepath.ToSlash(filepath.Clean(p))
		if p == "" || p == ".." || strings.HasPrefix(p, "../") {
			continue
		}
		if p == "." {
			return []string{"."}
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// ListUSBDir lists one directory under the mounted USB (relative path).
func (k *Kiosk) ListUSBDir(rel string) (mount string, parent string, entries []USBEntry, err error) {
	dev, err := k.USB.Detect()
	if err != nil {
		return "", "", nil, err
	}
	if dev == nil || dev.MountPath() == "" {
		return "", "", nil, ErrNoUSB
	}
	root := dev.MountPath()
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "."
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", nil, fmt.Errorf("atm: unsafe path")
	}
	abs := root
	if clean != "." {
		abs = filepath.Join(root, clean)
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		return "", "", nil, err
	}
	rootAbs, _ := filepath.Abs(root)
	relOut, err := filepath.Rel(rootAbs, abs)
	if err != nil || relOut == ".." || strings.HasPrefix(relOut, ".."+string(filepath.Separator)) {
		return "", "", nil, fmt.Errorf("atm: path outside usb")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", nil, err
	}
	if !info.IsDir() {
		return "", "", nil, fmt.Errorf("atm: not a directory")
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return "", "", nil, err
	}
	cur := filepath.ToSlash(relOut)
	if cur == "." {
		cur = ""
	}
	parent = ""
	if cur != "" {
		parent = filepath.ToSlash(filepath.Dir(cur))
		if parent == "." {
			parent = ""
		}
	}
	out := make([]USBEntry, 0, len(ents))
	for _, e := range ents {
		name := e.Name()
		if name == "." || name == ".." {
			continue
		}
		p := name
		if cur != "" {
			p = cur + "/" + name
		}
		typ := "file"
		var size int64
		if e.IsDir() {
			typ = "dir"
		} else if e.Type()&os.ModeSymlink != 0 {
			continue
		} else if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		out = append(out, USBEntry{Name: name, Path: p, Type: typ, Size: size})
	}
	return root, parent, out, nil
}

// USBEntry is one listing row under the USB mount.
type USBEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // file | dir
	Size int64  `json:"size,omitempty"`
}

func (k *Kiosk) recordImportOp(ctx context.Context, reqID, serial string, started time.Time, result string) error {
	return k.Store.RecordUSBOp(ctx, &store.USBOp{
		RequestID: reqID, ATMID: k.ID, USBSerial: serial, Dir: "import",
		StartedAt: started, FinishedAt: time.Now(), Result: result,
	})
}
