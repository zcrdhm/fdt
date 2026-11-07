//go:build camera

package atm

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run with: go test -tags camera ./internal/atm/ -run Live -v -count=1
func TestLiveCameraOpenAndTimeout(t *testing.T) {
	if _, err := os.Stat("/dev/video0"); err != nil {
		t.Skip("no /dev/video0")
	}
	sc, err := OpenCamera("/dev/video0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err = sc.Read(ctx)
	if err == nil {
		t.Log("decoded a frame (unexpected but ok if a QR was in view)")
		return
	}
	if ctx.Err() == nil {
		t.Fatalf("expected timeout waiting for QR, got %v", err)
	}
	t.Logf("camera open+capture loop ok: %v", err)
}
