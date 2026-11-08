package apprun

import (
	"fmt"
	"os"
	"strings"
)

// LoadEnv reads KEY=VAL lines into a map (shared by slim + fat binaries).
func LoadEnv(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// LoadEnvFile loads the first existing path among candidates.
func LoadEnvFile(explicit string, fallbacks ...string) (map[string]string, string, error) {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	candidates = append(candidates, fallbacks...)
	var last error
	for _, p := range candidates {
		if p == "" {
			continue
		}
		env, err := LoadEnv(p)
		if err == nil {
			return env, p, nil
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("no env file found")
	}
	return nil, "", last
}

// RequireRole returns an error when ROLE is set and does not match want.
func RequireRole(env map[string]string, want ...string) error {
	role := env["ROLE"]
	if role == "" {
		return nil
	}
	for _, w := range want {
		if role == w {
			return nil
		}
	}
	return fmt.Errorf("fdt.env ROLE=%q 与本二进制不匹配（期望 %v）", role, want)
}

// DefaultEnvCandidates returns common env paths for a role binary.
func DefaultEnvCandidates() []string {
	out := []string{"/etc/fdt/fdt.env", "fdt.env"}
	if v := os.Getenv("FDT_ENV"); v != "" {
		out = append([]string{v}, out...)
	}
	return out
}
