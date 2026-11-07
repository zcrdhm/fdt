package importer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"fdt/internal/datasvc"
	"fdt/internal/manifest"
)

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuleBasedScanner(t *testing.T) {
	ctx := context.Background()
	s := RuleBasedScanner{}

	clean := t.TempDir()
	writeTree(t, clean, map[string]string{"a.txt": "hello", "docs/b.md": "# ok"})
	res, err := s.Scan(ctx, clean)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Clean {
		t.Fatalf("benign dir flagged: %v", res.Findings)
	}

	bad := t.TempDir()
	writeTree(t, bad, map[string]string{"good.txt": "x", "mal.exe": "MZ", "run.sh": "#!/bin/sh"})
	res, err = s.Scan(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	if res.Clean {
		t.Fatal("executables should be flagged")
	}
	if len(res.Findings) < 2 {
		t.Fatalf("expected .exe and .sh flagged, got %v", res.Findings)
	}

	eicarDir := t.TempDir()
	writeTree(t, eicarDir, map[string]string{"eicar.txt": eicar})
	res, _ = s.Scan(ctx, eicarDir)
	if res.Clean {
		t.Fatal("EICAR test signature should be flagged")
	}
}

func TestMockScanner(t *testing.T) {
	m := MockScanner{Clean: false, Findings: []string{"x"}}
	res, err := m.Scan(context.Background(), t.TempDir())
	if err != nil || res.Clean || len(res.Findings) != 1 {
		t.Fatalf("mock scanner misbehaved: %+v %v", res, err)
	}
}

func TestImporterSafeJoin(t *testing.T) {
	root := "/q/req"
	for _, bad := range []string{"/etc/passwd", "../x", "..", "a/../../b"} {
		if p, ok := safeJoin(root, bad); ok {
			t.Errorf("safeJoin(%q) should reject, got %q", bad, p)
		}
	}
	if p, ok := safeJoin(root, "a/b.txt"); !ok || p != filepath.Join(root, "a/b.txt") {
		t.Errorf("safeJoin normal failed: %q %v", p, ok)
	}
}

// TestExtractAndHashMatchesManifest verifies the importer's ingest-time Merkle
// root equals the manifest pipeline's root for the same data — this is what makes
// the anti-swap check sound (ATM indexes with manifest.Build, importer re-hashes
// the uploaded tar with extractAndHash).
func TestExtractAndHashMatchesManifest(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{
		"a.txt":        "alpha",
		"dir/b.bin":    string(bytes.Repeat([]byte{0x01, 0x02}, 100)),
		"dir/sub/c.md": "# c",
		"empty.txt":    "",
	})
	want, err := manifest.Build(src, manifest.NoopDLP{}, manifest.Options{})
	if err != nil {
		t.Fatal(err)
	}

	// Tar the source (as the ATM would stream it) then extract+hash into dest.
	var buf bytes.Buffer
	if err := datasvc.WriteTar(&buf, src); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	written, rootHex, err := extractAndHash(&buf, dest)
	if err != nil {
		t.Fatal(err)
	}
	if rootHex != want.Dataset.MerkleRoot {
		t.Fatalf("merkle mismatch: extract=%s manifest=%s", rootHex, want.Dataset.MerkleRoot)
	}
	if written == 0 {
		t.Fatal("nothing written")
	}
	// Files must actually exist at dest.
	for _, rel := range []string{"a.txt", "dir/b.bin", "dir/sub/c.md", "empty.txt"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
}

func TestMoveDir(t *testing.T) {
	src := t.TempDir()
	writeTree(t, src, map[string]string{"x/y.txt": "data"})
	dstParent := t.TempDir()
	dst := filepath.Join(dstParent, "released")
	files, err := moveDir(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "x/y.txt" {
		t.Fatalf("moved files = %v", files)
	}
	if _, err := os.Stat(filepath.Join(dst, "x/y.txt")); err != nil {
		t.Fatalf("dest missing file: %v", err)
	}
}
