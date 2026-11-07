package manifest

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fdt/internal/model"
)

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("src/main.go", "package main\nfunc main(){}\n")
	mustWrite("src/util.go", "package main\n")
	mustWrite("docs/readme.md", "# hello\n")
	mustWrite("design/top.v", "module top; endmodule\n")

	// build a zip archive with two members
	zpath := filepath.Join(dir, "bundle.zip")
	zf, err := os.Create(zpath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for _, m := range []struct{ name, body string }{
		{"a.txt", "alpha"}, {"nested/b.bin", strings.Repeat("x", 100)},
	} {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(m.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuildFullMode(t *testing.T) {
	dir := writeFixture(t)
	dlp := RuleBasedDLP{SensitiveKeywords: []string{"secret"}}
	res, err := Build(dir, dlp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 5 physical files: main.go, util.go, readme.md, top.v, bundle.zip
	if res.Dataset.Count != 5 {
		t.Fatalf("count = %d, want 5", res.Dataset.Count)
	}
	if res.Dataset.Mode != model.ModeFull {
		t.Fatalf("mode = %s, want full", res.Dataset.Mode)
	}
	if len(res.Dataset.MerkleRoot) != 64 {
		t.Fatalf("bad merkle root: %q", res.Dataset.MerkleRoot)
	}
	if len(res.LeafHashes) != 5 {
		t.Fatalf("leaf hashes = %d, want 5", len(res.LeafHashes))
	}
	// archive members must appear as display-only entries bound to the archive
	var sawMember, sawDLP bool
	for _, e := range res.Entries {
		if e.InArchive == "bundle.zip" && strings.Contains(e.Path, "a.txt") {
			sawMember = true
			if e.Leaf != -1 {
				t.Fatalf("archive member should have Leaf=-1, got %d", e.Leaf)
			}
		}
	}
	for c := range res.Dataset.DLP.Categories {
		if c == "源代码" || c == "EDA/硬件设计" {
			sawDLP = true
		}
	}
	if !sawMember {
		t.Fatal("zip member a.txt not listed")
	}
	if !sawDLP {
		t.Fatalf("DLP categories not populated: %v", res.Dataset.DLP.Categories)
	}
}

func TestSummaryModeOnTinyBudget(t *testing.T) {
	dir := writeFixture(t)
	res, err := Build(dir, NoopDLP{}, Options{SingleFrameBudget: 60})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dataset.Mode != model.ModeSummary {
		t.Fatalf("mode = %s, want summary under tiny budget", res.Dataset.Mode)
	}
	if len(res.Dataset.Entries) != 0 {
		t.Fatal("summary mode must not carry per-file entries")
	}
	if len(res.Dataset.Tree) == 0 {
		t.Fatal("summary mode must carry a top-level tree")
	}
	// full internal detail is still retained regardless of mode
	if len(res.Entries) < 5 {
		t.Fatalf("internal entries truncated: %d", len(res.Entries))
	}
}

func TestMerkleRootStableAcrossRuns(t *testing.T) {
	dir := writeFixture(t)
	r1, err := Build(dir, NoopDLP{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := Build(dir, NoopDLP{}, Options{})
	if r1.Dataset.MerkleRoot != r2.Dataset.MerkleRoot {
		t.Fatal("merkle root not deterministic")
	}
}

func TestEmptyDirRejected(t *testing.T) {
	dir := t.TempDir()
	if _, err := Build(dir, NoopDLP{}, Options{}); err != ErrNoFiles {
		t.Fatalf("want ErrNoFiles, got %v", err)
	}
}

func TestBuildPathsOnlySelectedWithContentEngine(t *testing.T) {
	dir := writeFixture(t)
	// Sibling junk dir must not be required for selective import indexing.
	junk := filepath.Join(dir, "junk-sibling")
	if err := os.MkdirAll(filepath.Join(junk, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "nested", "x.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := BuildPaths(dir, []string{"docs/readme.md"}, ContentEngine{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Dataset.Count != 1 {
		t.Fatalf("count=%d want 1 (selected file only)", res.Dataset.Count)
	}
	if res.Dataset.DLP.AV == "" {
		t.Fatal("expected AV status from ContentEngine.ScanPaths")
	}
}
