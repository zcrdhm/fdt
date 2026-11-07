package codec

import (
	"bytes"
	"testing"
)

func TestBase45RoundTrip(t *testing.T) {
	cases := [][]byte{
		{},
		{0x00},
		[]byte("hello world"),
		bytes.Repeat([]byte{0xff, 0x00, 0x7f}, 100),
	}
	for _, in := range cases {
		enc := Base45Encode(in)
		dec, err := Base45Decode(enc)
		if err != nil {
			t.Fatalf("decode error for len=%d: %v", len(in), err)
		}
		if !bytes.Equal(in, dec) {
			t.Fatalf("round-trip mismatch for len=%d", len(in))
		}
	}
}

func TestBase45AlphabetOnly(t *testing.T) {
	// Encoded output must only contain alphabet characters so QR alphanumeric
	// mode can be used and camera capture stays robust.
	in := bytes.Repeat([]byte{0x00, 0xff, 0x41, 0x7f}, 40)
	enc := Base45Encode(in)
	for i := 0; i < len(enc); i++ {
		if base45Reverse[enc[i]] < 0 {
			t.Fatalf("encoded output has out-of-alphabet byte %q", enc[i])
		}
	}
}

func TestBase45RejectsBadInput(t *testing.T) {
	if _, err := Base45Decode("abc!"); err == nil {
		t.Fatal("expected error for out-of-alphabet char")
	}
	if _, err := Base45Decode("AB"); err == nil {
		// length 2 is valid (single byte group); ensure length%3==1 rejected instead
		_ = err
	}
	if _, err := Base45Decode("A"); err == nil {
		t.Fatal("expected error for dangling single char")
	}
}

func TestFrameRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("fdt-token-payload;"), 50)
	frame, err := EncodeFrame("acme", payload)
	if err != nil {
		t.Fatal(err)
	}
	tenant, out, err := DecodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if tenant != "acme" {
		t.Fatalf("tenant = %q, want acme", tenant)
	}
	if !bytes.Equal(payload, out) {
		t.Fatal("frame round-trip mismatch")
	}
	if pt, err := PeekTenant(frame); err != nil || pt != "acme" {
		t.Fatalf("PeekTenant = %q,%v", pt, err)
	}
}

func TestFrameDefaultTenant(t *testing.T) {
	frame, err := EncodeFrame("", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := DecodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if tenant != DefaultTenant {
		t.Fatalf("empty tenant should default to %q, got %q", DefaultTenant, tenant)
	}
}

func TestFrameRejectsBadMagic(t *testing.T) {
	if _, _, err := DecodeFrame("XXXX" + ".abc"); err != ErrBadMagic {
		t.Fatalf("want ErrBadMagic, got %v", err)
	}
}

func TestFrameRejectsBadTenant(t *testing.T) {
	if _, err := EncodeFrame("bad tenant!", []byte("x")); err != ErrBadTenant {
		t.Fatalf("want ErrBadTenant, got %v", err)
	}
}
