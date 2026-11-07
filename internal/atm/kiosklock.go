package atm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Lockdown is the kiosk USB policy from design §9.2 / §9.4: default-deny new
// USB devices, then authorize only the named data port. HID interfaces are
// still rejected later by SysfsUSB; this stops a newly inserted device from
// being bound at all until it is on the allowed port.
type Lockdown struct {
	// USBDevices is the sysfs directory, default /sys/bus/usb/devices.
	USBDevices string
	// DataPorts are sysfs names to authorize, e.g. "1-1" or "2-1.2".
	DataPorts []string
}

func (l Lockdown) root() string {
	if l.USBDevices != "" {
		return l.USBDevices
	}
	return "/sys/bus/usb/devices"
}

// Apply writes authorized_default=0 on every host controller and authorized=1
// on DataPorts. It fails if sysfs is not writable, so a kiosk does not start
// half-locked.
func (l Lockdown) Apply() error {
	if len(l.DataPorts) == 0 {
		return errors.New("atm: kiosk lockdown requires at least one USB data port")
	}
	entries, err := os.ReadDir(l.root())
	if err != nil {
		return fmt.Errorf("atm: usb sysfs: %w", err)
	}
	var defaults int
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "usb") {
			continue
		}
		p := filepath.Join(l.root(), e.Name(), "authorized_default")
		if err := os.WriteFile(p, []byte("0\n"), 0o644); err != nil {
			return fmt.Errorf("atm: set %s: %w", p, err)
		}
		defaults++
	}
	if defaults == 0 {
		return errors.New("atm: no USB host controllers under " + l.root())
	}
	for _, port := range l.DataPorts {
		if strings.Contains(port, "..") || strings.Contains(port, "/") {
			return fmt.Errorf("atm: bad usb port %q", port)
		}
		p := filepath.Join(l.root(), port, "authorized")
		if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil {
			return fmt.Errorf("atm: authorize %s: %w", port, err)
		}
	}
	return nil
}

// NFTRules renders a default-drop ruleset that allows established traffic and
// the given destination CIDRs (the intranet datasvc / importer). The ATM
// process does not apply it; the kiosk flag does, and only when asked.
func NFTRules(cidrs []string) (string, error) {
	if len(cidrs) == 0 {
		return "", errors.New("atm: empty kiosk network allowlist")
	}
	var b strings.Builder
	b.WriteString("table inet fdt_kiosk {\n")
	b.WriteString("  chain output {\n")
	b.WriteString("    type filter hook output priority 0; policy drop;\n")
	b.WriteString("    oif lo accept\n")
	b.WriteString("    ct state established,related accept\n")
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" || strings.ContainsAny(c, " \t\n;{}") {
			return "", fmt.Errorf("atm: bad cidr %q", c)
		}
		fmt.Fprintf(&b, "    ip daddr %s accept\n", c)
	}
	b.WriteString("  }\n}\n")
	return b.String(), nil
}
