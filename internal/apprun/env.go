// Package apprun holds role entrypoints shared by the fat fdt binary and
// slim fdt-gw / fdt-intranet / fdt-atm builds.
package apprun

import "strings"

// EnvOr returns m[k] or def when empty.
func EnvOr(m map[string]string, k, def string) string {
	if v := m[k]; v != "" {
		return v
	}
	return def
}

// EnvTruthy reports 1/true/yes/on.
func EnvTruthy(m map[string]string, k string) bool {
	v := strings.ToLower(strings.TrimSpace(m[k]))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// FirstNonEmpty returns the first non-blank string.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
