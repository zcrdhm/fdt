package av

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// fakeClamd speaks just enough of nINSTREAM to classify a stream that contains
// the EICAR marker as infected and everything else as clean.
func fakeClamd(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				line, err := br.ReadString('\n')
				if err != nil || !strings.HasPrefix(line, "nINSTREAM") {
					return
				}
				var body []byte
				for {
					var hdr [4]byte
					if _, err := io.ReadFull(br, hdr[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint32(hdr[:])
					if n == 0 {
						break
					}
					chunk := make([]byte, n)
					if _, err := io.ReadFull(br, chunk); err != nil {
						return
					}
					body = append(body, chunk...)
				}
				if strings.Contains(string(body), "EICAR-STANDARD-ANTIVIRUS-TEST-FILE") {
					_, _ = io.WriteString(c, "stream: Eicar-Test-Signature FOUND\n")
					return
				}
				_, _ = io.WriteString(c, "stream: OK\n")
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestClamdScanDir(t *testing.T) {
	addr := fakeClamd(t)
	c, err := New("tcp://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := osWrite(filepath.Join(dir, "ok.txt"), "hello"); err != nil {
		t.Fatal(err)
	}
	if err := osWrite(filepath.Join(dir, "bad.txt"), "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"); err != nil {
		t.Fatal(err)
	}
	sigs, err := c.ScanDir(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sigs) != 1 || !strings.Contains(sigs[0], "Eicar-Test-Signature") {
		t.Fatalf("sigs = %v", sigs)
	}
}

func osWrite(path, body string) error {
	return writeFile(path, []byte(body))
}
