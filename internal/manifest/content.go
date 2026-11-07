package manifest

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"fdt/internal/av"
	"fdt/internal/model"
)

// idCard matches an 18-digit PRC resident identity number. It is a classification
// signal for the approver, not a block by itself.
var idCard = regexp.MustCompile(`[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[\dXx]`)

// ContentEngine classifies by extension and by file bytes, and optionally asks
// clamd for an antivirus verdict. AV stays "not_scanned" when no clamd is set,
// so a missing engine is not reported as clean.
type ContentEngine struct {
	RuleBasedDLP
	Clam *av.Client
}

// ScanPaths samples content of the listed absolute files only.
func (e ContentEngine) ScanPaths(paths []string) (string, []string, error) {
	var hits []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		st, err := os.Lstat(path)
		if err != nil {
			// Skip unreadable selected files for content hints; hashing already failed harder upstream.
			continue
		}
		if st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			continue
		}
		rel := filepath.Base(path)
		f, oerr := os.Open(path)
		if oerr != nil {
			continue
		}
		buf := make([]byte, 1<<20)
		n, _ := io.ReadFull(f, buf)
		_ = f.Close()
		sample := string(buf[:n])
		if idCard.MatchString(sample) {
			hits = append(hits, "疑似身份证号: "+rel)
		}
		lower := strings.ToLower(sample)
		for _, kw := range e.SensitiveKeywords {
			if kw == "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(kw)) {
				hits = append(hits, "内容含敏感词 "+kw+": "+rel)
			}
		}
	}
	if e.Clam == nil {
		return "not_scanned", hits, nil
	}
	// ClamAV still prefers directory scan when possible; for selective paths scan each file.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var infected bool
	for _, path := range paths {
		st, err := os.Lstat(path)
		if err != nil || st.IsDir() {
			continue
		}
		sig, err := e.Clam.ScanFile(ctx, path)
		if err != nil {
			continue
		}
		if sig != "" {
			infected = true
			hits = append(hits, "病毒: "+sig)
		}
	}
	if infected {
		return "infected", hits, nil
	}
	return "clean", hits, nil
}

// ScanTree walks root but skips unreadable entries (USB bad blocks / NTFS I/O errors).
func (e ContentEngine) ScanTree(root string) (string, []string, error) {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return e.ScanPaths(paths)
}

// Classify delegates to the extension rules, then ScanTree fills AV.
func (e ContentEngine) Classify(entries []model.FileEntry) model.DLPSummary {
	sum := e.RuleBasedDLP.Classify(entries)
	sum.AV = "not_scanned"
	return sum
}
