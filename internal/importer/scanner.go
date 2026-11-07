// Package importer implements the inbound ferry direction: an ATM indexes a
// source USB, the intranet signs an import QR#1, and after Feishu approval the
// ATM streams the data into an internal quarantine where it is re-hashed (must
// match the approved Merkle root), AV/DLP scanned, and only then released to the
// target directory (design §7 import flow).
package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ScanResult is the verdict of an AV/DLP scan over a quarantined directory.
type ScanResult struct {
	Clean      bool
	Findings   []string       // human-readable hits
	Categories map[string]int // DLP category counts
	Engine     string         // which engine produced this
}

// Scanner inspects a quarantined directory before release. Production wires a
// real AV/DLP engine; RuleBasedScanner is a dev stand-in.
type Scanner interface {
	Scan(ctx context.Context, dir string) (ScanResult, error)
}

// eicar is the standard antivirus test signature; RuleBasedScanner flags it so
// the block path can be exercised without a real engine.
const eicar = "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"

// RuleBasedScanner is a development scanner: it flags the EICAR test string and
// executable extensions. It is NOT a real antivirus — replace before go-live.
type RuleBasedScanner struct {
	BlockExt []string // default: .exe .dll .so .bat .sh .scr .com .msi
}

func (s RuleBasedScanner) blockExts() []string {
	if len(s.BlockExt) > 0 {
		return s.BlockExt
	}
	return []string{".exe", ".dll", ".so", ".bat", ".sh", ".scr", ".com", ".msi"}
}

func (s RuleBasedScanner) Scan(ctx context.Context, dir string) (ScanResult, error) {
	res := ScanResult{Clean: true, Categories: map[string]int{}, Engine: "rule-based(dev)"}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		ext := strings.ToLower(filepath.Ext(rel))
		for _, b := range s.blockExts() {
			if ext == b {
				res.Clean = false
				res.Findings = append(res.Findings, "可执行文件: "+rel)
				res.Categories["executable"]++
				break
			}
		}
		// Content check for the EICAR test signature (bounded read).
		if f, oerr := os.Open(path); oerr == nil {
			head := make([]byte, 4096)
			n, _ := io.ReadFull(f, head)
			f.Close()
			if strings.Contains(string(head[:n]), eicar) {
				res.Clean = false
				res.Findings = append(res.Findings, "EICAR 测试病毒: "+rel)
				res.Categories["virus"]++
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	return res, nil
}

// MockScanner always returns a fixed verdict (for tests).
type MockScanner struct {
	Clean    bool
	Findings []string
}

func (m MockScanner) Scan(context.Context, string) (ScanResult, error) {
	return ScanResult{Clean: m.Clean, Findings: m.Findings, Engine: "mock", Categories: map[string]int{}}, nil
}

// HashFile returns the hex SHA-256 of a file (used for per-file import records).
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
