package gwadmin

import (
	"fmt"
	"os"
	"strings"
)

// setEnvValue updates KEY=VAL in a dotenv-style file, preserving other lines.
func setEnvValue(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(path, []byte(fmt.Sprintf("%s=%s\n", key, value)), 0o600)
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		k, _, ok := strings.Cut(trim, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		lines[i] = key + "=" + value
		found = true
		break
	}
	if !found {
		if len(lines) == 0 || lines[len(lines)-1] != "" {
			lines = append(lines, key+"="+value)
		} else {
			lines = append(lines[:len(lines)-1], key+"="+value, "")
		}
	}
	out := strings.Join(lines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
