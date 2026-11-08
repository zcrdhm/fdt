//go:build linux

package atm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// SysfsUSB is a Linux USB mass-storage detector. It reads sysfs and the mount
// table; it does not open HID interfaces. Composite devices that also expose
// HID are rejected (BadUSB / keyboard injection).
//
// If AutoMount is set (e.g. /mnt/fdt-usb) and a stick is present but not
// mounted, Detect tries udisksctl then mount(8) so a kiosk can run without a
// desktop automounter.
type SysfsUSB struct {
	// SysRoot defaults to /sys. Tests point it at a fixture.
	SysRoot string
	// MountInfo defaults to /proc/mounts.
	MountInfo string
	// AutoMount, if non-empty, is the directory used to mount an unmounted stick.
	AutoMount string
}

func (m SysfsUSB) sys() string {
	if m.SysRoot != "" {
		return m.SysRoot
	}
	return "/sys"
}

func (m SysfsUSB) mounts() string {
	if m.MountInfo != "" {
		return m.MountInfo
	}
	return "/proc/mounts"
}

// Detect returns the first inserted USB mass-storage filesystem, or (nil, nil)
// when none is present.
func (m SysfsUSB) Detect() (Device, error) {
	block := filepath.Join(m.sys(), "block")
	entries, err := os.ReadDir(block)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	table, err := readMounts(m.mounts())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "zram") {
			continue
		}
		base := filepath.Join(block, name)
		rem, _ := os.ReadFile(filepath.Join(base, "removable"))
		if strings.TrimSpace(string(rem)) != "1" {
			continue
		}
		devPath, err := filepath.EvalSymlinks(filepath.Join(base, "device"))
		if err != nil {
			continue
		}
		classes := interfaceClasses(devPath)
		if !contains(classes, "08") || contains(classes, "03") {
			// Not mass-storage, or a composite device that also speaks HID.
			continue
		}
		serial := readSerial(devPath)
		if serial == "" {
			serial = name
		}
		partDev, mp, fs, writable := resolveUSBMount(table, name, base)
		if mp == "" && m.AutoMount != "" {
			if err := os.MkdirAll(m.AutoMount, 0o755); err == nil {
				if mounted, mfs, merr := tryAutoMount(partDev, m.AutoMount); merr == nil && mounted != "" {
					mp, fs, writable = mounted, mfs, true
					// Refresh mounts for free-space / options.
					if t2, err := readMounts(m.mounts()); err == nil {
						table = t2
						if p, f, w := findMount(table, partDev); p != "" {
							mp, fs, writable = p, f, w
						}
					}
				}
			}
		}
		sizeSectors := readInt(filepath.Join(base, "size"))
		capBytes := sizeSectors * 512
		dev := &sysfsDevice{
			serial: serial, mount: mp, writable: writable && mp != "",
			capacity: capBytes, fs: fs, block: name, node: partDev,
		}
		if mp != "" {
			dev.free = freeBytes(mp)
		} else {
			dev.free = capBytes
		}
		return dev, nil
	}
	return nil, nil
}

// resolveUSBMount finds the best mounted data partition for block name (e.g.
// sda), skipping EFI/boot slices. If nothing is mounted, returns the preferred
// partition node to AutoMount.
func resolveUSBMount(table []mnt, name, sysBlock string) (partDev, path, fs string, writable bool) {
	prefix := "/dev/" + name
	type cand struct {
		dev, path, fs string
		writable      bool
		score         int64
	}
	var cands []cand
	for _, m := range table {
		if m.dev != prefix && !strings.HasPrefix(m.dev, prefix) {
			continue
		}
		if isBootishMount(m.path, m.fs) {
			continue
		}
		cands = append(cands, cand{
			dev: m.dev, path: m.path, fs: m.fs, writable: mountWritable(m.opts),
			score: scoreMount(m),
		})
	}
	if len(cands) > 0 {
		best := cands[0]
		for _, c := range cands[1:] {
			if c.score > best.score {
				best = c
			}
		}
		return best.dev, best.path, best.fs, best.writable
	}
	part := largestPartition(sysBlock, name)
	if part != "" {
		return "/dev/" + part, "", "", false
	}
	return prefix, "", "", false
}

func isBootishMount(path, fs string) bool {
	p := strings.ToLower(path)
	if strings.Contains(p, "/efi") || strings.HasSuffix(p, "/boot") || strings.Contains(p, "/boot/") {
		return true
	}
	// Tiny vfat ESP leftovers sometimes mount as "EFI".
	base := strings.ToLower(filepath.Base(path))
	if base == "efi" || base == "boot" || base == "system" {
		return true
	}
	_ = fs
	return false
}

func scoreMount(m mnt) int64 {
	var score int64
	switch strings.ToLower(m.fs) {
	case "exfat", "ntfs", "ntfs3", "fuseblk", "ext4", "xfs", "btrfs":
		score += 1 << 40
	case "vfat", "msdos":
		score += 1 << 20 // bootable FS are common on ESP; deprioritize
	}
	if mountWritable(m.opts) {
		score += 1 << 30
	}
	if free := freeBytes(m.path); free > 0 {
		score += free
	}
	return score
}

func largestPartition(sysBlock, name string) string {
	entries, err := os.ReadDir(sysBlock)
	if err != nil {
		return ""
	}
	var best string
	var bestSize int64
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, name) || n == name {
			continue
		}
		sz := readInt(filepath.Join(sysBlock, n, "size")) // sectors
		if sz > bestSize {
			bestSize = sz
			best = n
		}
	}
	return best
}

func tryAutoMount(devNode, mountpoint string) (mountedPath, fs string, err error) {
	if devNode == "" {
		return "", "", fmt.Errorf("atm: empty device node")
	}
	// 1) udisks2 — works for desktop users without root.
	if p, err := exec.LookPath("udisksctl"); err == nil {
		out, err := exec.Command(p, "mount", "-b", devNode).CombinedOutput()
		text := string(out)
		if err == nil || strings.Contains(text, "Already mounted") {
			if mp := parseUdisksMount(text); mp != "" {
				return mp, guessFS(mp), nil
			}
			// "Already mounted at `/run/media/...`."
			if mp := parseUdisksMount(text); mp != "" {
				return mp, guessFS(mp), nil
			}
		}
		// Fall through to mount(8) on failure.
		_ = err
	}
	// 2) mount(8) into the kiosk mountpoint (needs privileges).
	_ = exec.Command("umount", mountpoint).Run()
	cmd := exec.Command("mount", "-o", "rw,sync,nosuid,nodev", devNode, mountpoint)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Try common FS types explicitly.
		for _, t := range []string{"exfat", "vfat", "ntfs", "ntfs3", "ext4"} {
			_ = exec.Command("umount", mountpoint).Run()
			if exec.Command("mount", "-t", t, "-o", "rw,sync,nosuid,nodev", devNode, mountpoint).Run() == nil {
				return mountpoint, t, nil
			}
		}
		return "", "", fmt.Errorf("atm: mount %s: %v (%s)", devNode, err, strings.TrimSpace(stderr.String()))
	}
	return mountpoint, guessFS(mountpoint), nil
}

func parseUdisksMount(text string) string {
	// "Mounted /dev/sdb1 at /run/media/user/LABEL."
	// "Already mounted at `/run/media/user/LABEL`."
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " at "); i >= 0 {
			rest := strings.TrimSpace(line[i+4:])
			rest = strings.Trim(rest, ".`\"'")
			if strings.HasPrefix(rest, "/") {
				return rest
			}
		}
	}
	return ""
}

func guessFS(path string) string {
	table, err := readMounts("/proc/mounts")
	if err != nil {
		return ""
	}
	for _, m := range table {
		if m.path == path {
			return m.fs
		}
	}
	return ""
}

type sysfsDevice struct {
	serial   string
	mount    string
	writable bool
	capacity int64
	free     int64
	fs       string
	block    string
	node     string
}

func (d *sysfsDevice) Serial() string       { return d.serial }
func (d *sysfsDevice) MountPath() string    { return d.mount }
func (d *sysfsDevice) Writable() bool       { return d.writable }
func (d *sysfsDevice) CapacityBytes() int64 { return d.capacity }
func (d *sysfsDevice) FreeBytes() int64     { return d.free }
func (d *sysfsDevice) FileSystem() string   { return d.fs }
func (d *sysfsDevice) Eject() error {
	if d.mount == "" {
		return nil
	}
	// Flush dirty pages before unmount so "safe remove" is meaningful.
	_ = exec.Command("sync").Run()
	var last error
	if d.node != "" {
		if out, err := exec.Command("udisksctl", "unmount", "-b", d.node).CombinedOutput(); err != nil {
			last = fmt.Errorf("udisksctl unmount: %v (%s)", err, strings.TrimSpace(string(out)))
		} else {
			_ = exec.Command("udisksctl", "power-off", "-b", d.node).Run()
			return nil
		}
	}
	if err := unix.Unmount(d.mount, 0); err != nil {
		if err2 := unix.Unmount(d.mount, unix.MNT_DETACH); err2 != nil {
			if last != nil {
				return fmt.Errorf("%v; umount: %v", last, err2)
			}
			return err2
		}
	}
	if d.node != "" {
		_ = exec.Command("udisksctl", "power-off", "-b", d.node).Run()
	}
	return nil
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// interfaceClasses walks up from a block device and collects bInterfaceClass
// values of the USB interfaces on the same USB device.
func interfaceClasses(devPath string) []string {
	dir := devPath
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			return classesUnder(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return classesUnder(filepath.Dir(devPath))
}

func classesUnder(usbDev string) []string {
	entries, err := os.ReadDir(usbDev)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(usbDev, e.Name(), "bInterfaceClass"))
		if err != nil {
			continue
		}
		out = append(out, strings.TrimSpace(string(b)))
	}
	return out
}

func readSerial(devPath string) string {
	dir := devPath
	for i := 0; i < 8; i++ {
		b, err := os.ReadFile(filepath.Join(dir, "serial"))
		if err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func readInt(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

type mnt struct {
	dev, path, fs, opts string
}

func readMounts(path string) ([]mnt, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []mnt
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		out = append(out, mnt{dev: unescape(fields[0]), path: unescape(fields[1]), fs: fields[2], opts: fields[3]})
	}
	return out, sc.Err()
}

func unescape(s string) string {
	s = strings.ReplaceAll(s, "\\040", " ")
	s = strings.ReplaceAll(s, "\\011", "\t")
	s = strings.ReplaceAll(s, "\\012", "\n")
	s = strings.ReplaceAll(s, "\\134", "\\")
	return s
}

func findMount(table []mnt, dev string) (path, fs string, writable bool) {
	for _, m := range table {
		if m.dev == dev {
			return m.path, m.fs, mountWritable(m.opts)
		}
	}
	return "", "", false
}

func findMountPrefix(table []mnt, dev string) (string, string, bool) {
	for _, m := range table {
		if strings.HasPrefix(m.dev, dev) {
			return m.path, m.fs, mountWritable(m.opts)
		}
	}
	return "", "", false
}

func mountWritable(opts string) bool {
	for _, o := range strings.Split(opts, ",") {
		if o == "ro" {
			return false
		}
	}
	return strings.Contains(","+opts+",", ",rw,")
}

func freeBytes(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return int64(st.Bavail) * int64(st.Bsize)
}

// WindowsUSB is only implemented on Windows; stub keeps cross-package references compiling on Linux.
type WindowsUSB struct {
	SkipLetters string
}

func (WindowsUSB) Detect() (Device, error) {
	return nil, errors.New("atm: Windows USB detection is only supported on windows")
}
