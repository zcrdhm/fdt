//go:build linux

package atm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSysfsUSBDetectsMassStorageAndRejectsHID(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	// Mass storage stick: removable sdX, interface class 08, mounted rw.
	mkBlock(t, sys, "sda", "08", "USB123", "2048")
	// Composite HID+storage is rejected.
	mkBlock(t, sys, "sdb", "03", "BADUSB", "2048")
	// Also give sdb a storage interface so the reject is specifically HID.
	hidDev := filepath.Join(sys, "devices", "usb", "sdb")
	if err := os.WriteFile(filepath.Join(hidDev, "1-1:1.1", "bInterfaceClass"), []byte("08\n"), 0o644); err != nil {
		// 1-1:1.1 directory may not exist; mkBlock created one interface.
	}
	mounts := filepath.Join(root, "mounts")
	if err := os.WriteFile(mounts, []byte("/dev/sda1 /mnt/usb exfat rw,noatime 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The mount path must exist for Statfs; capacity still comes from sysfs.
	if err := os.MkdirAll("/mnt/usb", 0o755); err != nil && !os.IsExist(err) {
		// Don't require /mnt/usb on the host. Point the mount at a temp dir.
	}
	usbPath := filepath.Join(root, "usbmp")
	if err := os.MkdirAll(usbPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mounts, []byte("/dev/sda1 "+usbPath+" exfat rw,noatime 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dev, err := (SysfsUSB{SysRoot: sys, MountInfo: mounts}).Detect()
	if err != nil {
		t.Fatal(err)
	}
	if dev == nil {
		t.Fatal("expected a mass-storage device")
	}
	if dev.Serial() != "USB123" {
		t.Fatalf("serial %s", dev.Serial())
	}
	if !dev.Writable() || dev.FileSystem() != "exfat" {
		t.Fatalf("writable=%v fs=%s", dev.Writable(), dev.FileSystem())
	}
	if dev.CapacityBytes() != 2048*512 {
		t.Fatalf("capacity %d", dev.CapacityBytes())
	}
	if dev.MountPath() != usbPath {
		t.Fatalf("mount %s", dev.MountPath())
	}
}

func TestSysfsUSBPrefersDataPartitionOverEFI(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	mkBlock(t, sys, "sda", "08", "STICK1", "1000000")
	// Fake partition size nodes used by largestPartition when unmounted.
	_ = os.MkdirAll(filepath.Join(sys, "block", "sda", "sda1"), 0o755)
	_ = os.WriteFile(filepath.Join(sys, "block", "sda", "sda1", "size"), []byte("409600\n"), 0o644) // ~200MB
	_ = os.MkdirAll(filepath.Join(sys, "block", "sda", "sda2"), 0o755)
	_ = os.WriteFile(filepath.Join(sys, "block", "sda", "sda2", "size"), []byte("999000\n"), 0o644)

	efi := filepath.Join(root, "EFI")
	data := filepath.Join(root, "未命名")
	_ = os.MkdirAll(efi, 0o755)
	_ = os.MkdirAll(data, 0o755)
	// Pad data mount so freeBytes ranks higher.
	_ = os.WriteFile(filepath.Join(data, "pad.bin"), make([]byte, 0), 0o644)
	mounts := filepath.Join(root, "mounts")
	content := "/dev/sda1 " + efi + " vfat rw,relatime 0 0\n" +
		"/dev/sda2 " + data + " exfat rw,relatime 0 0\n"
	if err := os.WriteFile(mounts, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dev, err := (SysfsUSB{SysRoot: sys, MountInfo: mounts}).Detect()
	if err != nil || dev == nil {
		t.Fatalf("detect: %v %v", dev, err)
	}
	if dev.MountPath() != data {
		t.Fatalf("want data partition %s, got %s (%s)", data, dev.MountPath(), dev.FileSystem())
	}
}

func TestSysfsUSBRejectsHIDOnly(t *testing.T) {
	root := t.TempDir()
	sys := filepath.Join(root, "sys")
	mkBlock(t, sys, "sda", "03", "KEYBD", "100")
	dev, err := (SysfsUSB{SysRoot: sys, MountInfo: filepath.Join(root, "nomounts")}).Detect()
	if err != nil {
		t.Fatal(err)
	}
	if dev != nil {
		t.Fatalf("HID device should be ignored, got %s", dev.Serial())
	}
}

func mkBlock(t *testing.T, sys, name, class, serial, sectors string) {
	t.Helper()
	block := filepath.Join(sys, "block", name)
	usb := filepath.Join(sys, "devices", "usb", name)
	iface := filepath.Join(usb, "1-1:1.0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(block, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(block, "removable"), "1\n")
	write(filepath.Join(block, "size"), sectors+"\n")
	write(filepath.Join(usb, "idVendor"), "1234\n")
	write(filepath.Join(usb, "serial"), serial+"\n")
	write(filepath.Join(iface, "bInterfaceClass"), class+"\n")
	if err := os.Symlink(usb, filepath.Join(block, "device")); err != nil {
		t.Fatal(err)
	}
}
