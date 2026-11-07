// Package datasvc streams staged request data to authenticated ATM kiosks over
// the intranet LAN. It is the only component that serves file bytes, and only
// to a registered device presenting its credential (risk M1). Production uses
// mTLS client certificates; the bearer-fingerprint check here is the dev seam.
package datasvc

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fdt/internal/deviceauth"
	"fdt/internal/merkle"
	"fdt/internal/model"
	"fdt/internal/store"
)

var (
	ErrUnauthenticated = errors.New("datasvc: device not authenticated")
	ErrRequestNotFound = errors.New("datasvc: request not found")
)

// Service authenticates devices and serves data.
type Service struct {
	Store *store.Store
}

func New(s *store.Store) *Service { return &Service{Store: s} }

// Router returns the HTTP routes.
func (s *Service) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /requests/{id}/manifest", s.auth(s.handleManifest))
	mux.HandleFunc("GET /requests/{id}/stream", s.auth(s.handleStream))
	return mux
}

// authenticate accepts an mTLS device certificate or, on plain HTTP, the dev
// bearer credential. Both must match the registered device row.
func (s *Service) authenticate(r *http.Request) (*store.Device, error) {
	dev, err := deviceauth.Authenticate(r, s.Store)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	return dev, nil
}

func (s *Service) auth(next func(http.ResponseWriter, *http.Request, *store.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, err := s.authenticate(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err)
			return
		}
		next(w, r, dev)
	}
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

type manifestResp struct {
	ReqID      string            `json:"reqId"`
	MerkleRoot string            `json:"merkleRoot"`
	Count      int               `json:"count"`
	Files      []model.FileEntry `json:"files"`
}

func (s *Service) handleManifest(w http.ResponseWriter, r *http.Request, _ *store.Device) {
	id := r.PathValue("id")
	req, err := s.Store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, ErrRequestNotFound)
		return
	}
	all, err := s.Store.GetRequestFiles(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Only physical files (Leaf >= 0) are streamed; archive members are display-only.
	physical := make([]model.FileEntry, 0, len(all))
	for _, e := range all {
		if e.Leaf >= 0 {
			physical = append(physical, e)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(manifestResp{
		ReqID: id, MerkleRoot: req.MerkleRoot, Count: req.FileCount, Files: physical,
	})
}

func (s *Service) handleStream(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	id := r.PathValue("id")
	req, err := s.Store.GetRequest(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, ErrRequestNotFound)
		return
	}
	if req.DataDir == "" {
		writeErr(w, http.StatusInternalServerError, errors.New("datasvc: request has no staged data dir"))
		return
	}
	_, _ = s.Store.AppendAudit(r.Context(), "datasvc", dev.ID, id, "data_stream_start", map[string]any{
		"dataDir": req.DataDir,
	})
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("X-Request-Id", id)
	if err := WriteTar(w, req.DataDir); err != nil {
		// headers already sent; record the failure
		_, _ = s.Store.AppendAudit(context.Background(), "datasvc", dev.ID, id, "data_stream_error",
			map[string]string{"err": err.Error()})
	}
}

// WriteTar streams a tar of rootDir with slash-relative paths matching the
// manifest, skipping symlinks. The consumer hashes each file while extracting.
func WriteTar(w io.Writer, rootDir string) error {
	return WriteTarPaths(w, rootDir, nil)
}

// WriteTarPaths streams a tar of selected relative paths under rootDir.
// If relPaths is empty, the whole tree is written (same as WriteTar).
func WriteTarPaths(w io.Writer, rootDir string, relPaths []string) error {
	tw := tar.NewWriter(w)
	defer tw.Close()
	if len(relPaths) == 0 {
		return filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return writeTarEntry(tw, rootDir, path, d)
		})
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, raw := range relPaths {
		raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		raw = strings.TrimPrefix(raw, "/")
		if raw == "" || raw == "." {
			return filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				return writeTarEntry(tw, rootDir, path, d)
			})
		}
		clean := filepath.Clean(raw)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("datasvc: unsafe path %q", raw)
		}
		abs := filepath.Join(absRoot, clean)
		abs, err = filepath.Abs(abs)
		if err != nil {
			return err
		}
		relCheck, err := filepath.Rel(absRoot, abs)
		if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return fmt.Errorf("datasvc: path outside root: %q", raw)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					if path == abs {
						return err
					}
					if d != nil && d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				rel, _ := filepath.Rel(absRoot, path)
				rel = filepath.ToSlash(rel)
				if _, ok := seen[rel]; ok {
					return nil
				}
				if err := writeTarEntry(tw, absRoot, path, d); err != nil {
					return err
				}
				if !d.IsDir() {
					seen[rel] = struct{}{}
				}
				return nil
			})
			if err != nil {
				return err
			}
			continue
		}
		rel := filepath.ToSlash(relCheck)
		if _, ok := seen[rel]; ok {
			continue
		}
		hdr := &tar.Header{
			Name: rel, Size: info.Size(), Mode: 0o644,
			ModTime: info.ModTime(), Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		_ = f.Close()
		if copyErr != nil {
			return copyErr
		}
		seen[rel] = struct{}{}
	}
	return nil
}

func writeTarEntry(tw *tar.Writer, rootDir, path string, d fs.DirEntry) error {
	if d.Type()&fs.ModeSymlink != 0 {
		return nil
	}
	rel, err := filepath.Rel(rootDir, path)
	if err != nil {
		return err
	}
	name := filepath.ToSlash(rel)
	if d.IsDir() {
		if name == "." {
			return nil
		}
		return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755})
	}
	info, err := d.Info()
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name: name, Size: info.Size(), Mode: 0o644,
		ModTime: info.ModTime(), Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

// DirMerkleRoot recomputes the Merkle root of a directory tree the same way the
// manifest pipeline does, so an ATM can verify written data independently.
// Leaves are SHA-256 of each physical file, ordered by slash-relative path.
func DirMerkleRoot(rootDir string) (string, error) {
	type fe struct {
		rel  string
		hash []byte
	}
	var files []fe
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		files = append(files, fe{rel: filepath.ToSlash(rel), hash: h.Sum(nil)})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	leaves := make([][]byte, len(files))
	for i, f := range files {
		leaves[i] = f.hash
	}
	root, err := merkle.Root(leaves)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(root), nil
}
