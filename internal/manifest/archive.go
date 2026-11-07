package manifest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"fdt/internal/model"
)

const maxMemberHashBytes = 64 << 20 // only hash members up to 64 MiB

// listArchive expands an archive into display-only member entries. It never
// writes anything to disk and enforces strict safety limits (design §5.3).
func listArchive(abs, rel, typ string, o Options) []model.FileEntry {
	var members []model.FileEntry
	var err error
	switch {
	case typ == "application/zip" || strings.HasSuffix(strings.ToLower(rel), ".zip"):
		members, err = listZip(abs, rel, o)
	default:
		members, err = listTar(abs, rel, o)
	}
	if err != nil {
		// Record the failure inline so the approver sees it rather than a silent gap.
		return []model.FileEntry{{
			Path: rel + " [无法展开]", Type: typ, InArchive: rel,
			HashHex: "", Size: 0,
		}}
	}
	return members
}

// safeMemberPath rejects path traversal and absolute paths.
func safeMemberPath(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") {
		return "", false
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return name, true
}

func listZip(abs, rel string, o Options) ([]model.FileEntry, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if fi.Size() > o.MaxArchiveBytes {
		return nil, fmt.Errorf("archive too large to expand: %d bytes", fi.Size())
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			return nil, err
		}
		return nil, err
	}
	defer zr.Close()

	out := make([]model.FileEntry, 0, len(zr.File))
	var totalUncompressed uint64
	for i, f := range zr.File {
		if i >= o.MaxArchiveEntries {
			out = append(out, model.FileEntry{
				Path: rel + "#…", InArchive: rel,
				Type: "inode/directory",
			})
			break
		}
		name, ok := safeMemberPath(f.Name)
		if !ok {
			out = append(out, model.FileEntry{Path: rel + "#[非法路径已拦截]", InArchive: rel})
			continue
		}
		if f.FileInfo().IsDir() {
			out = append(out, model.FileEntry{Path: rel + "#" + name, InArchive: rel, Type: "dir"})
			continue
		}
		// zip bomb guard: cumulative uncompressed size and compression ratio.
		totalUncompressed += f.UncompressedSize64
		if totalUncompressed > 4<<40 || (f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > 200 && f.UncompressedSize64 > 1<<30) {
			return nil, errors.New("possible zip bomb detected")
		}
		entry := model.FileEntry{
			Path: rel + "#" + name, InArchive: rel,
			Size: int64(f.UncompressedSize64), Type: detectType(name, nil),
		}
		if f.Flags&0x1 != 0 { // encrypted
			entry.Type = "encrypted"
			entry.HashHex = ""
			out = append(out, entry)
			continue
		}
		if h, herr := hashZipMember(f); herr == nil {
			entry.HashHex = hex.EncodeToString(h)
		}
		out = append(out, entry)
	}
	return out, nil
}

func hashZipMember(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if f.UncompressedSize64 > maxMemberHashBytes {
		return nil, errors.New("member too large to hash")
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(rc, maxMemberHashBytes+1)); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func listTar(abs, rel string, o Options) ([]model.FileEntry, error) {
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if fi.Size() > o.MaxArchiveBytes {
		return nil, fmt.Errorf("archive too large to expand: %d bytes", fi.Size())
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	lower := strings.ToLower(rel)
	if strings.HasSuffix(lower, ".gz") || strings.HasSuffix(lower, ".tgz") {
		gz, gerr := gzip.NewReader(f)
		if gerr != nil {
			return nil, gerr
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	out := make([]model.FileEntry, 0, 64)
	var totalUncompressed int64
	for i := 0; ; i++ {
		hdr, herr := tr.Next()
		if herr == io.EOF {
			break
		}
		if herr != nil {
			return nil, herr
		}
		if i >= o.MaxArchiveEntries {
			out = append(out, model.FileEntry{Path: rel + "#…", InArchive: rel})
			break
		}
		name, ok := safeMemberPath(hdr.Name)
		if !ok {
			out = append(out, model.FileEntry{Path: rel + "#[非法路径已拦截]", InArchive: rel})
			continue
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			out = append(out, model.FileEntry{Path: rel + "#" + name, InArchive: rel, Type: "symlink(未跟随)"})
			continue
		}
		if hdr.Typeflag == tar.TypeDir {
			out = append(out, model.FileEntry{Path: rel + "#" + name, InArchive: rel, Type: "dir"})
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		totalUncompressed += hdr.Size
		if totalUncompressed > 4<<40 {
			return nil, errors.New("possible tar bomb detected")
		}
		entry := model.FileEntry{
			Path: rel + "#" + name, InArchive: rel,
			Size: hdr.Size, Type: detectType(name, nil),
		}
		if hdr.Size <= maxMemberHashBytes {
			h := sha256.New()
			if _, cerr := io.Copy(h, io.LimitReader(tr, maxMemberHashBytes+1)); cerr == nil {
				entry.HashHex = hex.EncodeToString(h.Sum(nil))
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// nestedPeek is reserved for depth-limited archives-in-archives; kept explicit
// so the recursion depth policy is visible and bounded.
func nestedPeek(content []byte, depth int, o Options) []model.FileEntry {
	if depth >= o.MaxArchiveDepth || len(content) == 0 {
		return nil
	}
	if zr, err := zip.NewReader(bytes.NewReader(content), int64(len(content))); err == nil {
		out := make([]model.FileEntry, 0, len(zr.File))
		for i, f := range zr.File {
			if i >= o.MaxArchiveEntries {
				break
			}
			name, ok := safeMemberPath(f.Name)
			if !ok {
				continue
			}
			out = append(out, model.FileEntry{Path: name, Size: int64(f.UncompressedSize64), Type: "nested"})
		}
		return out
	}
	return nil
}
