package atm

import (
	"path/filepath"
	"testing"
)

func TestSafeJoinRejectsTraversal(t *testing.T) {
	root := "/mnt/usb/out"
	bad := []string{
		"/etc/passwd",
		"../../etc/passwd",
		"..",
		"../secret",
		"a/../../b",
	}
	for _, name := range bad {
		if p, ok := safeJoin(root, name); ok {
			t.Errorf("safeJoin(%q) should be rejected, got %q", name, p)
		}
	}
}

func TestSafeJoinAcceptsNormal(t *testing.T) {
	root := "/mnt/usb/out"
	good := map[string]string{
		"a.txt":        filepath.Join(root, "a.txt"),
		"dir/b.bin":    filepath.Join(root, "dir/b.bin"),
		"./c":          filepath.Join(root, "c"),
		"deep/x/y/z.v": filepath.Join(root, "deep/x/y/z.v"),
	}
	for name, want := range good {
		p, ok := safeJoin(root, name)
		if !ok {
			t.Errorf("safeJoin(%q) should be accepted", name)
			continue
		}
		if p != want {
			t.Errorf("safeJoin(%q) = %q, want %q", name, p, want)
		}
	}
}

func TestIsFDTPayload(t *testing.T) {
	if !IsFDTPayload("FDT1.default.abc123") {
		t.Error("valid frame not recognized")
	}
	if !IsFDTPayload("  FDT1.default.abc123  ") {
		t.Error("valid frame with whitespace not recognized")
	}
	// A HID-injection style payload must NOT be treated as an FDT token.
	for _, bad := range []string{"; rm -rf /", "FDT2.x", "", "echo pwned"} {
		if IsFDTPayload(bad) {
			t.Errorf("non-FDT payload %q wrongly accepted", bad)
		}
	}
}

func TestNoUSBState(t *testing.T) {
	m := NewMockManager()
	dev, err := m.Detect()
	if err != nil || dev != nil {
		t.Fatalf("empty manager should report no device: dev=%v err=%v", dev, err)
	}
	dir := t.TempDir()
	if err := m.Insert("SER1", dir, 1<<20, true, "exfat"); err != nil {
		t.Fatal(err)
	}
	dev, err = m.Detect()
	if err != nil || dev == nil {
		t.Fatalf("after insert should report a device: %v %v", dev, err)
	}
	if dev.Serial() != "SER1" || !dev.Writable() {
		t.Fatalf("bad device: %+v", dev)
	}
	m.Remove()
	if dev, _ := m.Detect(); dev != nil {
		t.Fatal("after remove should report no device")
	}
}
