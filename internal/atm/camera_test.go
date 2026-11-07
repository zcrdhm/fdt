package atm

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

func TestDecodeQRImageRoundTrip(t *testing.T) {
	const payload = "FDT1.default.testpayload1234567890"
	pngBytes, err := qrcode.Encode(payload, qrcode.Medium, 256)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := DecodeQRBytes(pngBytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if frame != payload {
		t.Fatalf("got %q want %q", frame, payload)
	}
}

func TestDecodeQRRejectsNonFDT(t *testing.T) {
	pngBytes, err := qrcode.Encode("https://example.com/not-fdt", qrcode.Medium, 256)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeQRImage(img); err == nil {
		t.Fatal("expected non-FDT QR to be rejected")
	}
}

func TestDecodeQRFile(t *testing.T) {
	const payload = "FDT1.tenant.abc"
	pngBytes, err := qrcode.Encode(payload, qrcode.Low, 128)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "qr.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeQRFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != payload {
		t.Fatalf("got %q want %q", got, payload)
	}
}
