package portal

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMultipartUploadCreatesRequest(t *testing.T) {
	svc := newTestService(t)
	h := &Handler{Svc: svc}
	srv := httptest.NewServer(h.Router())
	defer srv.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("reason", "upload test")
	_ = w.WriteField("cls", "internal")
	fw, err := w.CreateFormFile("files", "docs/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("hello upload"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/requests", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// Staging should contain the uploaded bytes under the request id.
	entries, _ := os.ReadDir(svc.StagingRoot)
	if len(entries) == 0 {
		t.Fatal("expected staged request dir")
	}
	found := false
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		// mime/multipart CreateFormFile uses filepath.Base, so the leaf is a.txt.
		if _, err := os.Stat(filepath.Join(svc.StagingRoot, e.Name(), "a.txt")); err == nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("uploaded file not found under staging; entries=%v", entries)
	}
}
