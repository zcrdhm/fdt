package manifest

import (
	"path/filepath"
	"strings"

	"fdt/internal/model"
)

// Engine classifies a manifest for the approver.
type Engine interface {
	Classify(entries []model.FileEntry) model.DLPSummary
}

// FileScanner is implemented by engines that read file bytes (content DLP and
// ClamAV). Build calls it after Classify when the engine supports it.
type FileScanner interface {
	ScanTree(root string) (avStatus string, hits []string, err error)
}

// FilePathScanner scans only the given absolute file paths (preferred for
// selective import so we never walk unrelated / corrupt USB directories).
type FilePathScanner interface {
	ScanPaths(paths []string) (avStatus string, hits []string, err error)
}

// NoopDLP performs no classification.
type NoopDLP struct{}

func (NoopDLP) Classify([]model.FileEntry) model.DLPSummary {
	return model.DLPSummary{AV: "n/a"}
}

// RuleBasedDLP is a development classifier: it buckets files by extension and
// flags filename keywords. Its AV field is a stub and MUST be replaced by a
// real antivirus engine in production — it does not scan content.
type RuleBasedDLP struct {
	// SensitiveKeywords, when present in a path, raise a hit for review.
	SensitiveKeywords []string
}

var extCategories = map[string]string{
	".go": "源代码", ".py": "源代码", ".c": "源代码", ".cpp": "源代码", ".h": "源代码",
	".java": "源代码", ".js": "源代码", ".ts": "源代码", ".rs": "源代码",
	".v": "EDA/硬件设计", ".sv": "EDA/硬件设计", ".vhd": "EDA/硬件设计",
	".gds": "EDA/版图", ".gds2": "EDA/版图", ".oas": "EDA/版图",
	".lib": "EDA/库", ".lef": "EDA/库", ".def": "EDA/库", ".sdf": "EDA/库",
	".doc": "文档", ".docx": "文档", ".pdf": "文档", ".ppt": "文档", ".pptx": "文档",
	".xls": "表格", ".xlsx": "表格", ".csv": "表格",
	".png": "图片", ".jpg": "图片", ".jpeg": "图片", ".tif": "图片", ".bmp": "图片",
	".zip": "压缩包", ".tar": "压缩包", ".gz": "压缩包", ".tgz": "压缩包", ".7z": "压缩包", ".rar": "压缩包",
}

func (r RuleBasedDLP) Classify(entries []model.FileEntry) model.DLPSummary {
	cats := map[string]int{}
	var hits []string
	seen := map[string]bool{}
	for _, e := range entries {
		if e.InArchive != "" {
			continue // classify physical files only; members are display-only
		}
		ext := strings.ToLower(filepath.Ext(e.Path))
		if c, ok := extCategories[ext]; ok {
			cats[c]++
		} else {
			cats["其他"]++
		}
		lower := strings.ToLower(e.Path)
		for _, kw := range r.SensitiveKeywords {
			if kw == "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(kw)) && !seen[kw] {
				seen[kw] = true
				hits = append(hits, "文件名含敏感词: "+kw)
			}
		}
	}
	return model.DLPSummary{
		Categories: cats,
		AV:         "pending", // STUB: replace with real AV scan result
		Hits:       hits,
	}
}
