package codec

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"strings"
)

// Magic prefixes every FDT QR payload. The full frame format is:
//
//	FDT1.<tenant>.<base45(deflate(payload))>
//
// The tenant segment is PLAINTEXT so a multi-tenant gateway can pick the right
// verification key before decoding; token signing covers the tenant inside the
// payload too, and Open* cross-checks the two (so a tenant hint cannot be
// forged without breaking the signature).
const Magic = "FDT1."

var (
	ErrBadMagic   = errors.New("codec: missing FDT magic prefix")
	ErrBadTenant  = errors.New("codec: invalid tenant segment")
	ErrTooLarge   = errors.New("codec: inflated payload exceeds safety limit")
	ErrEmptyIn    = errors.New("codec: empty input")
	maxInflated   = int64(4 << 20) // 4 MiB hard cap on a single-frame token
	DefaultTenant = "default"
)

// EncodeFrame compresses payload with DEFLATE and wraps it as a single-frame QR
// string: Magic || tenant || "." || base45(deflate(payload)).
func EncodeFrame(tenant string, payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", ErrEmptyIn
	}
	if tenant == "" {
		tenant = DefaultTenant
	}
	if !ValidTenant(tenant) {
		return "", ErrBadTenant
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(payload); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return Magic + tenant + "." + Base45Encode(buf.Bytes()), nil
}

// PeekTenant returns the plaintext tenant segment without decoding the payload,
// so a gateway can select the verification key first.
func PeekTenant(s string) (string, error) {
	if !strings.HasPrefix(s, Magic) {
		return "", ErrBadMagic
	}
	rest := s[len(Magic):]
	i := strings.IndexByte(rest, '.')
	if i < 0 {
		return "", ErrBadTenant
	}
	return rest[:i], nil
}

// DecodeFrame reverses EncodeFrame: returns the tenant and the inflated payload.
func DecodeFrame(s string) (string, []byte, error) {
	if !strings.HasPrefix(s, Magic) {
		return "", nil, ErrBadMagic
	}
	rest := s[len(Magic):]
	i := strings.IndexByte(rest, '.')
	if i < 0 {
		return "", nil, ErrBadTenant
	}
	tenant, body := rest[:i], rest[i+1:]
	if !ValidTenant(tenant) {
		return "", nil, ErrBadTenant
	}
	raw, err := Base45Decode(body)
	if err != nil {
		return "", nil, err
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, maxInflated+1))
	if err != nil {
		return "", nil, err
	}
	if int64(len(out)) > maxInflated {
		return "", nil, ErrTooLarge
	}
	return tenant, out, nil
}

// FrameSizeBytes returns the encoded single-frame length for a representative
// tenant, used by the manifest pipeline to decide full vs summary mode.
func FrameSizeBytes(payload []byte) (int, error) {
	s, err := EncodeFrame(DefaultTenant, payload)
	if err != nil {
		return 0, err
	}
	return len(s), nil
}

// ValidTenant restricts the plaintext segment to a URL/QR-safe token so it can
// never contain the "." separator or whitespace.
func ValidTenant(t string) bool {
	if t == "" || len(t) > 64 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return false
		}
	}
	return true
}
