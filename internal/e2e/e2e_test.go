package e2e

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fdt/internal/model"
	"fdt/internal/store"
)

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("design/top.v", "module top; endmodule\n")
	write("design/sub/util.v", "module util; endmodule\n")
	write("docs/spec.md", "# confidential spec\n")
	write("data/records.csv", "id,name\n1,alice\n2,bob\n")

	zp := filepath.Join(dir, "bundle.zip")
	zf, err := os.Create(zp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for _, m := range [][2]string{{"readme.txt", "hello"}, {"inner/note.md", "note"}} {
		w, _ := zw.Create(m[0])
		_, _ = w.Write([]byte(m[1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEndToEndExport(t *testing.T) {
	ctx := context.Background()
	fx := fixture(t)
	work := t.TempDir()

	rep, err := Run(ctx, Config{FixtureDir: fx, WorkDir: work, AutoApprove: true})
	if err != nil {
		t.Fatalf("e2e run failed: %v", err)
	}

	if rep.FinalStatus != store.StatusRedeemed {
		t.Fatalf("final status = %s, want REDEEMED", rep.FinalStatus)
	}
	if rep.Mode != model.ModeFull && rep.Mode != model.ModeSummary {
		t.Fatalf("unexpected manifest mode: %s", rep.Mode)
	}
	if len(rep.MerkleRoot) != 64 {
		t.Fatalf("bad merkle root: %q", rep.MerkleRoot)
	}
	if rep.WrittenBytes == 0 || len(rep.WrittenFiles) == 0 {
		t.Fatalf("nothing written to USB: %+v", rep)
	}
	if !rep.AuditChainOK {
		t.Fatal("audit chain not verified")
	}
	if rep.NoUSBErr == nil {
		t.Fatal("expected no-USB scan to be rejected")
	}
	if rep.DoubleScanErr == nil {
		t.Fatal("expected replay to be rejected")
	}
	// The physical files (not archive members) must all land on the USB.
	want := []string{"design/top.v", "design/sub/util.v", "docs/spec.md", "data/records.csv", "bundle.zip"}
	joined := strings.Join(rep.WrittenFiles, ",")
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Fatalf("missing %q on USB; got %v", w, rep.WrittenFiles)
		}
	}
	t.Logf("steps:\n%s", strings.Join(rep.Steps, "\n"))
}
