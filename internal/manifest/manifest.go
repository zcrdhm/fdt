// Package manifest walks a data set, hashes every file, safely expands archives
// into a member listing, runs DLP classification, builds the Merkle root and
// produces the Dataset that goes into QR#1 — degrading from a full per-file
// list to a summary when the single-frame budget is exceeded (design §5).
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fdt/internal/codec"
	"fdt/internal/merkle"
	"fdt/internal/model"
)

var (
	ErrTooManyFiles = errors.New("manifest: file count exceeds policy limit")
	ErrTooLarge     = errors.New("manifest: total size exceeds policy limit")
	ErrNoFiles      = errors.New("manifest: no files found")
)

// Options bounds the pipeline. Zero values fall back to the defaults below.
type Options struct {
	MaxFiles          int   // hard cap on physical file count (else ErrTooManyFiles)
	MaxTotalBytes     int64 // hard cap on dataset size (else ErrTooLarge)
	SingleFrameBudget int   // target QR string length; full list kept only if it fits
	MaxArchiveEntries int   // per-archive member listing cap
	MaxArchiveDepth   int   // nesting depth for archives-in-archives
	MaxArchiveBytes   int64 // refuse to expand archives larger than this
	HashTruncateBytes int   // hash bytes kept in QR entries (default 16 = 128-bit)
}

func (o Options) withDefaults() Options {
	if o.MaxFiles <= 0 {
		o.MaxFiles = 200000
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = 4 << 40 // 4 TiB
	}
	if o.SingleFrameBudget <= 0 {
		o.SingleFrameBudget = 1600
	}
	if o.MaxArchiveEntries <= 0 {
		o.MaxArchiveEntries = 5000
	}
	if o.MaxArchiveDepth <= 0 {
		o.MaxArchiveDepth = 2
	}
	if o.MaxArchiveBytes <= 0 {
		o.MaxArchiveBytes = 8 << 30 // 8 GiB
	}
	if o.HashTruncateBytes <= 0 || o.HashTruncateBytes > 32 {
		o.HashTruncateBytes = 16
	}
	return o
}

// Result carries everything the pipeline produced.
type Result struct {
	// Entries is the full per-file detail (physical files + archive members).
	// Always persisted internally regardless of what fits into the QR.
	Entries []model.FileEntry
	// LeafHashes are the 32-byte Merkle leaves for the physical files, in the
	// canonical (sorted-by-path) order matching Dataset.MerkleRoot.
	LeafHashes [][]byte
	// Dataset is the compact form embedded in QR#1 (full or summary).
	Dataset model.Dataset
}

// Build runs the full pipeline over rootDir.
func Build(rootDir string, dlp Engine, opts Options) (*Result, error) {
	o := opts.withDefaults()
	files, err := walk(rootDir, o)
	if err != nil {
		return nil, err
	}
	return buildFromFiles(rootDir, files, dlp, o)
}

// BuildPaths builds a manifest for selected relative paths under rootDir.
// Each path may be a file or a directory (directory expands to all files under it).
func BuildPaths(rootDir string, relPaths []string, dlp Engine, opts Options) (*Result, error) {
	o := opts.withDefaults()
	files, err := walkSelected(rootDir, relPaths, o)
	if err != nil {
		return nil, err
	}
	return buildFromFiles(rootDir, files, dlp, o)
}

func buildFromFiles(rootDir string, files []walkedFile, dlp Engine, o Options) (*Result, error) {
	if dlp == nil {
		dlp = NoopDLP{}
	}
	if len(files) == 0 {
		return nil, ErrNoFiles
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })

	var totalSize int64
	leafHashes := make([][]byte, 0, len(files))
	entries := make([]model.FileEntry, 0, len(files))

	for idx, f := range files {
		sum, size, head, err := hashFile(f.abs)
		if err != nil {
			return nil, err
		}
		totalSize += size
		leafHashes = append(leafHashes, sum)
		typ := detectType(f.rel, head)
		entries = append(entries, model.FileEntry{
			Path:    f.rel,
			Size:    size,
			Type:    typ,
			HashHex: hex.EncodeToString(sum),
			Leaf:    idx,
		})
		// Expand archives into display-only member entries (Leaf stays -1 so the
		// archive file itself remains the single Merkle leaf that binds its bytes).
		if isArchive(f.rel, typ) {
			members := listArchive(f.abs, f.rel, typ, o)
			for _, m := range members {
				m.Leaf = -1
				entries = append(entries, m)
			}
		}
	}

	root, err := merkle.Root(leafHashes)
	if err != nil {
		return nil, err
	}
	rootHex := hex.EncodeToString(root)

	dlpSummary := dlp.Classify(entries)
	// Prefer scanning only selected/indexed files — never walk the whole USB
	// (corrupt sibling dirs like bad NTFS folders would otherwise fail QR#1).
	if ps, ok := dlp.(FilePathScanner); ok {
		abs := make([]string, 0, len(files))
		for _, f := range files {
			abs = append(abs, f.abs)
		}
		avStatus, hits, err := ps.ScanPaths(abs)
		if err != nil {
			return nil, err
		}
		if avStatus != "" {
			dlpSummary.AV = avStatus
		}
		dlpSummary.Hits = append(dlpSummary.Hits, hits...)
	} else if fs, ok := dlp.(FileScanner); ok {
		avStatus, hits, err := fs.ScanTree(rootDir)
		if err != nil {
			return nil, err
		}
		if avStatus != "" {
			dlpSummary.AV = avStatus
		}
		dlpSummary.Hits = append(dlpSummary.Hits, hits...)
	}

	trunc := o.HashTruncateBytes * 2
	ds := model.Dataset{
		Count:      len(files),
		TotalSize:  totalSize,
		MerkleRoot: rootHex,
		Mode:       model.ModeFull,
		DLP:        dlpSummary,
	}
	// Try full mode with truncated hashes; fall back to summary if over budget.
	full := ds
	full.Entries = truncateHashes(entries, trunc)
	if frameFits(full, o.SingleFrameBudget) {
		ds = full
	} else {
		ds.Mode = model.ModeSummary
		ds.Tree = summarizeTree(files, leafHashes, entries)
	}

	return &Result{Entries: entries, LeafHashes: leafHashes, Dataset: ds}, nil
}

type walkedFile struct {
	abs string
	rel string
}

func walk(rootDir string, o Options) ([]walkedFile, error) {
	var files []walkedFile
	var total int64
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// USB/NTFS often has unreadable dirs; skip them instead of failing the whole tree.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Do not follow symlinks: they could point outside the quarantine area.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		if total > o.MaxTotalBytes {
			return ErrTooLarge
		}
		if len(files) >= o.MaxFiles {
			return ErrTooManyFiles
		}
		rel, rerr := filepath.Rel(rootDir, path)
		if rerr != nil {
			rel = path
		}
		files = append(files, walkedFile{abs: path, rel: filepath.ToSlash(rel)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// walkSelected expands selected relative paths (files or directories) into a
// deduplicated file list confined under rootDir.
func walkSelected(rootDir string, relPaths []string, o Options) ([]walkedFile, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var files []walkedFile
	var total int64
	addFile := func(abs, rel string, size int64) error {
		rel = filepath.ToSlash(rel)
		if _, ok := seen[rel]; ok {
			return nil
		}
		total += size
		if total > o.MaxTotalBytes {
			return ErrTooLarge
		}
		if len(files) >= o.MaxFiles {
			return ErrTooManyFiles
		}
		seen[rel] = struct{}{}
		files = append(files, walkedFile{abs: abs, rel: rel})
		return nil
	}
	for _, raw := range relPaths {
		raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		raw = strings.TrimPrefix(raw, "/")
		if raw == "" || raw == "." {
			return walk(rootDir, o)
		}
		clean := filepath.Clean(raw)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("manifest: unsafe path %q", raw)
		}
		abs := filepath.Join(absRoot, clean)
		abs, err = filepath.Abs(abs)
		if err != nil {
			return nil, err
		}
		relCheck, err := filepath.Rel(absRoot, abs)
		if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("manifest: path outside root: %q", raw)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("manifest: %s: %w", raw, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					// Selected dir root itself unreadable → fail; nested bad dirs → skip.
					if path == abs {
						return fmt.Errorf("无法读取目录 %s: %w", raw, err)
					}
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
					return nil
				}
				fi, err := d.Info()
				if err != nil {
					return nil
				}
				rel, err := filepath.Rel(absRoot, path)
				if err != nil {
					return err
				}
				return addFile(path, rel, fi.Size())
			})
			if err != nil {
				return nil, err
			}
			continue
		}
		if err := addFile(abs, relCheck, info.Size()); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func hashFile(path string) ([]byte, int64, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer f.Close()
	h := sha256.New()
	head := make([]byte, 512)
	var headN int
	var size int64
	buf := make([]byte, 1<<20)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if headN < len(head) {
				headN += copy(head[headN:], buf[:n])
			}
			h.Write(buf[:n])
			size += int64(n)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, 0, nil, rerr
		}
	}
	return h.Sum(nil), size, head[:headN], nil
}

func detectType(rel string, head []byte) string {
	if len(head) > 0 {
		if ct := http.DetectContentType(head); ct != "" && ct != "application/octet-stream" {
			return ct
		}
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return "text/x-go"
	case ".py":
		return "text/x-python"
	case ".c":
		return "text/x-c"
	case ".v":
		return "text/x-verilog"
	case ".gds", ".gds2":
		return "application/x-gdsii"
	case ".lib":
		return "application/x-spice-lib"
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz":
		return "application/gzip"
	}
	if len(head) > 0 {
		return http.DetectContentType(head)
	}
	return "application/octet-stream"
}

func isArchive(rel, typ string) bool {
	switch typ {
	case "application/zip", "application/x-tar", "application/gzip",
		"application/x-gtar", "application/x-compressed-tar":
		return true
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".zip", ".tar", ".gz", ".tgz", ".tar.gz":
		return true
	}
	return false
}

func truncateHashes(entries []model.FileEntry, hexLen int) []model.FileEntry {
	out := make([]model.FileEntry, len(entries))
	for i, e := range entries {
		e.HashHex = truncateHex(e.HashHex, hexLen)
		out[i] = e
	}
	return out
}

func truncateHex(h string, hexLen int) string {
	if len(h) > hexLen {
		return h[:hexLen]
	}
	return h
}

func frameFits(ds model.Dataset, budget int) bool {
	body, err := codec.MarshalCanonical(ds)
	if err != nil {
		return false
	}
	// Dataset CBOR compressed into a frame, plus the rest of the token
	// (applicant, reason, nonce, 64-byte signature, deflate overhead).
	n, err := codec.FrameSizeBytes(body)
	if err != nil {
		return false
	}
	return n+180 <= budget
}

// summarizeTree aggregates physical files by top-level path segment. Each node
// carries its own subtree Merkle root so the summary still binds structure.
func summarizeTree(files []walkedFile, leafHashes [][]byte, entries []model.FileEntry) []*model.TreeNode {
	type agg struct {
		count  int
		size   int64
		leaves [][]byte
	}
	order := []string{}
	m := map[string]*agg{}
	for i, f := range files {
		top := f.rel
		if idx := strings.IndexByte(top, '/'); idx >= 0 {
			top = top[:idx]
		}
		a, ok := m[top]
		if !ok {
			a = &agg{}
			m[top] = a
			order = append(order, top)
		}
		a.count++
		a.size += leafSize(entries, i)
		a.leaves = append(a.leaves, leafHashes[i])
	}
	sort.Strings(order)
	const maxNodes = 40
	nodes := make([]*model.TreeNode, 0, len(order))
	var otherCount, otherSize int64
	var otherLeaves [][]byte
	for i, name := range order {
		a := m[name]
		if i >= maxNodes {
			otherCount += int64(a.count)
			otherSize += a.size
			otherLeaves = append(otherLeaves, a.leaves...)
			continue
		}
		mr, _ := merkle.RootHex(a.leaves)
		nodes = append(nodes, &model.TreeNode{Name: name, Count: a.count, Size: a.size, MerkleChild: mr})
	}
	if len(otherLeaves) > 0 {
		mr, _ := merkle.RootHex(otherLeaves)
		nodes = append(nodes, &model.TreeNode{Name: "(其他)", Count: int(otherCount), Size: otherSize, MerkleChild: mr})
	}
	return nodes
}

func leafSize(entries []model.FileEntry, leafIdx int) int64 {
	for _, e := range entries {
		if e.Leaf == leafIdx {
			return e.Size
		}
	}
	return 0
}
