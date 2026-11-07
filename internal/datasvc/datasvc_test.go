package datasvc

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"fdt/internal/manifest"
	"fdt/internal/store"
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

// TestDirMerkleRootMatchesManifest ensures the ATM-side recomputation
// (DirMerkleRoot) equals the Portal-side manifest root — the basis of the
// export write verification.
func TestDirMerkleRootMatchesManifest(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.txt":        "alpha",
		"sub/b.bin":    "beta-bytes",
		"sub/deep/c.v": "module c; endmodule",
	})
	got, err := DirMerkleRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Build(dir, manifest.NoopDLP{}, manifest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got != m.Dataset.MerkleRoot {
		t.Fatalf("DirMerkleRoot=%s manifest=%s", got, m.Dataset.MerkleRoot)
	}
}

func TestWriteTarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"x.txt": "hello", "d/y.txt": "world"})
	var buf bytes.Buffer
	if err := WriteTar(&buf, dir); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			seen[hdr.Name] = string(b)
		}
	}
	if seen["x.txt"] != "hello" || seen["d/y.txt"] != "world" {
		t.Fatalf("tar round-trip mismatch: %v", seen)
	}
}

func TestDeviceAuthRequired(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.RegisterDevice(ctx, &store.Device{ID: "atm-1", CertFingerprint: "secret-abc", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(st).Router())
	defer srv.Close()

	// no creds -> 401
	resp, err := http.Get(srv.URL + "/requests/any/manifest")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-creds status = %d, want 401", resp.StatusCode)
	}

	// wrong secret -> 401
	req, _ := http.NewRequest("GET", srv.URL+"/requests/any/manifest", nil)
	req.Header.Set("X-Device-ID", "atm-1")
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-secret status = %d, want 401", resp.StatusCode)
	}

	// correct creds -> auth passes; request unknown -> 404 (not 401)
	req, _ = http.NewRequest("GET", srv.URL+"/requests/unknown/manifest", nil)
	req.Header.Set("X-Device-ID", "atm-1")
	req.Header.Set("Authorization", "Bearer secret-abc")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("good-creds unknown-req status = %d, want 404", resp.StatusCode)
	}
}
