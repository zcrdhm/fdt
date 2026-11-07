// Package gwdomain persists the gateway's public domain binding and helps
// operators verify DNS before Let's Encrypt issuance.
package gwdomain

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the operator-bound public hostname(s) for this gateway.
type Config struct {
	Domain  string    `json:"domain"`
	Aliases []string  `json:"aliases,omitempty"`
	Email   string    `json:"email,omitempty"`
	BoundAt time.Time `json:"boundAt,omitempty"`
}

// Status is returned to the admin UI.
type Status struct {
	Config
	Bound      bool     `json:"bound"`
	PublicBase string   `json:"publicBase,omitempty"`
	Hosts      []string `json:"hosts,omitempty"`
	Message    string   `json:"message,omitempty"`
}

// CheckResult is a DNS resolution report for one hostname.
type CheckResult struct {
	Host       string   `json:"host"`
	Resolved   []string `json:"resolved"`
	LocalIPs   []string `json:"localIps"`
	Matched    bool     `json:"matched"`
	Message    string   `json:"message"`
	CheckedAt  time.Time `json:"checkedAt"`
}

// LoadFile reads domain.json; missing file is empty Config.
func LoadFile(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	return Normalize(c), nil
}

// SaveFile writes domain.json atomically.
func SaveFile(path string, c Config) error {
	c = Normalize(c)
	if c.Domain == "" {
		return fmt.Errorf("gwdomain: domain required")
	}
	if c.BoundAt.IsZero() {
		c.BoundAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Normalize lowercases and dedupes hostnames.
func Normalize(c Config) Config {
	c.Domain = cleanHost(c.Domain)
	seen := map[string]bool{}
	if c.Domain != "" {
		seen[c.Domain] = true
	}
	out := make([]string, 0, len(c.Aliases))
	for _, a := range c.Aliases {
		a = cleanHost(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	c.Aliases = out
	c.Email = strings.TrimSpace(c.Email)
	return c
}

func cleanHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		// strip accidental port unless IPv6
		if !strings.Contains(h, "]") {
			h = h[:i]
		}
	}
	return strings.TrimSuffix(h, ".")
}

// Hosts returns primary + aliases.
func (c Config) Hosts() []string {
	c = Normalize(c)
	if c.Domain == "" {
		return nil
	}
	return append([]string{c.Domain}, c.Aliases...)
}

// PublicBase is https://primary-domain when bound.
func (c Config) PublicBase() string {
	c = Normalize(c)
	if c.Domain == "" {
		return ""
	}
	return "https://" + c.Domain
}

// ToStatus builds UI status.
func (c Config) ToStatus() Status {
	c = Normalize(c)
	st := Status{Config: c, Bound: c.Domain != "", Hosts: c.Hosts(), PublicBase: c.PublicBase()}
	if !st.Bound {
		st.Message = "尚未绑定公网域名"
	} else {
		st.Message = "已绑定 " + c.Domain
		if len(c.Aliases) > 0 {
			st.Message += "（别名 " + strings.Join(c.Aliases, ", ") + "）"
		}
	}
	return st
}

// CheckDNS looks up host and compares with this machine's non-loopback addresses.
func CheckDNS(host string) CheckResult {
	host = cleanHost(host)
	res := CheckResult{Host: host, CheckedAt: time.Now().UTC(), LocalIPs: localIPs()}
	if host == "" {
		res.Message = "域名为空"
		return res
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		res.Message = "DNS 解析失败: " + err.Error()
		return res
	}
	localSet := map[string]bool{}
	for _, ip := range res.LocalIPs {
		localSet[ip] = true
	}
	for _, ip := range ips {
		s := ip.String()
		res.Resolved = append(res.Resolved, s)
		if localSet[s] {
			res.Matched = true
		}
	}
	if len(res.Resolved) == 0 {
		res.Message = "无 A/AAAA 记录"
	} else if res.Matched {
		res.Message = "解析命中本机网卡地址"
	} else {
		res.Message = "已解析，但未命中本机网卡（若经 NAT/负载均衡属正常，请确认公网入口指向本机）"
	}
	return res
}

func localIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out
}
