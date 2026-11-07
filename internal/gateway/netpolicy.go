package gateway

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
)

// ParseCIDRFile reads one CIDR or IP per line. Blank lines and # comments are ignored.
func ParseCIDRFile(path string) ([]*net.IPNet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var nets []*net.IPNet
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "/") {
			if ip := net.ParseIP(line); ip != nil {
				if ip.To4() != nil {
					line += "/32"
				} else {
					line += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(line)
		if err != nil {
			return nil, fmt.Errorf("gateway: %s:%d: %w", path, lineNo, err)
		}
		nets = append(nets, n)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(nets) == 0 {
		return nil, fmt.Errorf("gateway: %s contains no CIDRs", path)
	}
	return nets, nil
}

func ipAllowed(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteIP(hostport string) net.IP {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return net.ParseIP(hostport)
	}
	return net.ParseIP(host)
}
