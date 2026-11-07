// Package av talks to a ClamAV clamd daemon. The private key and the file
// bytes stay on the host that runs clamd; this client only ships a stream and
// reads the verdict.
package av

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client is a clamd INSTREAM client.
type Client struct {
	Network string // "unix" or "tcp"
	Address string
	Timeout time.Duration
}

// New parses a clamd address: unix:///path, tcp://host:port, or host:port.
func New(addr string) (*Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("av: empty clamd address")
	}
	c := &Client{Timeout: 2 * time.Minute}
	switch {
	case strings.HasPrefix(addr, "unix://"):
		c.Network, c.Address = "unix", strings.TrimPrefix(addr, "unix://")
	case strings.HasPrefix(addr, "tcp://"):
		c.Network, c.Address = "tcp", strings.TrimPrefix(addr, "tcp://")
	default:
		c.Network, c.Address = "tcp", addr
	}
	if c.Address == "" {
		return nil, errors.New("av: empty clamd address")
	}
	return c, nil
}

func (c *Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 2 * time.Minute
	}
	return c.Timeout
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := net.Dialer{Timeout: c.timeout()}
	return d.DialContext(ctx, c.Network, c.Address)
}

// Ping checks that clamd answers PONG. Call it at process start.
func (c *Client) Ping(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("av: clamd dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout()))
	if _, err := conn.Write([]byte("zPING\x00")); err != nil {
		return err
	}
	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("av: clamd ping: %w", err)
	}
	if !strings.Contains(string(buf[:n]), "PONG") {
		return fmt.Errorf("av: clamd ping: %q", string(buf[:n]))
	}
	return nil
}

// ScanFile streams one file. A non-empty signature means clamd reported FOUND.
func (c *Client) ScanFile(ctx context.Context, path string) (signature string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return c.ScanReader(ctx, f)
}

// ScanReader implements clamd nINSTREAM. signature is empty when the stream is clean.
func (c *Client) ScanReader(ctx context.Context, r io.Reader) (signature string, err error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return "", fmt.Errorf("av: clamd dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout()))
	if _, err := io.WriteString(conn, "nINSTREAM\n"); err != nil {
		return "", err
	}
	buf := make([]byte, 64*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(n))
			if _, err := conn.Write(hdr[:]); err != nil {
				return "", err
			}
			if _, err := conn.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	var zero [4]byte
	if _, err := conn.Write(zero[:]); err != nil {
		return "", err
	}
	resp, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("av: clamd response: %w", err)
	}
	resp = strings.TrimSpace(resp)
	switch {
	case strings.HasSuffix(resp, " OK"):
		return "", nil
	case strings.HasSuffix(resp, " FOUND"):
		sig := strings.TrimSuffix(resp, " FOUND")
		if i := strings.Index(sig, ": "); i >= 0 {
			sig = sig[i+2:]
		}
		return sig, nil
	case resp == "":
		return "", errors.New("av: empty clamd response")
	default:
		return "", fmt.Errorf("av: clamd: %s", resp)
	}
}

// ScanDir scans every regular file. The first dial error aborts the scan
// (fail closed). signatures are "relpath: SigName" for infected files.
func (c *Client) ScanDir(ctx context.Context, dir string) (signatures []string, err error) {
	return c.scanDir(ctx, dir, dir)
}

func (c *Client) scanDir(ctx context.Context, root, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var sigs []string
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			sub, err := c.scanDir(ctx, root, p)
			if err != nil {
				return nil, err
			}
			sigs = append(sigs, sub...)
			continue
		}
		sig, err := c.ScanFile(ctx, p)
		if err != nil {
			return nil, err
		}
		if sig != "" {
			rel, _ := filepath.Rel(root, p)
			sigs = append(sigs, rel+": "+sig)
		}
	}
	return sigs, nil
}
