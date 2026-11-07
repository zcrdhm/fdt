package atm

import (
	"errors"
	"strings"
	"testing"

	"fdt/internal/model"
)

func TestCheckUSBForExportSpace(t *testing.T) {
	dev := &MockDevice{serial: "s", mountPath: t.TempDir(), writable: true, capacity: 1000, fs: "exfat"}
	files := []model.FileEntry{{Path: "a.bin", Size: 2000}}
	_, _, err := checkUSBForExport(dev, files)
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "QR#2 未核销") {
		t.Fatalf("msg = %v", err)
	}
}

func TestCheckUSBForExportFATLimit(t *testing.T) {
	dev := &MockDevice{serial: "s", mountPath: t.TempDir(), writable: true, capacity: 20 << 30, fs: "vfat"}
	files := []model.FileEntry{{Path: "big.iso", Size: 5 << 30}}
	_, _, err := checkUSBForExport(dev, files)
	if !errors.Is(err, ErrFilesystemLimit) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "4GB") || !strings.Contains(err.Error(), "big.iso") {
		t.Fatalf("msg = %v", err)
	}
}

func TestCheckUSBForExportExFATOK(t *testing.T) {
	dev := &MockDevice{serial: "s", mountPath: t.TempDir(), writable: true, capacity: 20 << 30, fs: "exfat"}
	files := []model.FileEntry{{Path: "big.iso", Size: 5 << 30}}
	total, names, err := checkUSBForExport(dev, files)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5<<30 || len(names) != 1 {
		t.Fatalf("total=%d names=%v", total, names)
	}
}

func TestIsFATLimitedFS(t *testing.T) {
	if !isFATLimitedFS("vfat") || !isFATLimitedFS("FAT32") {
		t.Fatal("expected FAT limited")
	}
	if isFATLimitedFS("exfat") || isFATLimitedFS("ntfs") {
		t.Fatal("exfat/ntfs should allow large files")
	}
}
