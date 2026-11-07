package atm

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSanitizeScanRejectsInjection(t *testing.T) {
	if _, ok := SanitizeScan("rm -rf /\n"); ok {
		t.Fatal("shell payload accepted")
	}
	if _, ok := SanitizeScan("hello"); ok {
		t.Fatal("non-FDT payload accepted")
	}
	got, ok := SanitizeScan("\x1bFDT1.default.ABC\r")
	if !ok || got != "FDT1.default.ABC" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestSerialScannerSkipsGarbageThenReturnsFrame(t *testing.T) {
	r := strings.NewReader("id\n;reboot\nFDT1.acme.PAYLOAD\n")
	s := NewSerialScanner(r)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "FDT1.acme.PAYLOAD" {
		t.Fatalf("got %q", got)
	}
}
