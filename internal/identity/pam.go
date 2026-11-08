package identity

import "strings"

// PAMConfig selects the PAM stack used for local OS authentication.
type PAMConfig struct {
	// Service is the name under /etc/pam.d/ (e.g. password-auth, login, fdt).
	Service string
}

func (c PAMConfig) withDefaults() PAMConfig {
	c.Service = strings.TrimSpace(c.Service)
	if c.Service == "" {
		c.Service = "password-auth"
	}
	return c
}

// PAMAvailable reports whether this binary can talk to libpam (Linux + CGO).
func PAMAvailable() bool { return pamAvailable }

// DefaultPAMService is the RHEL/CentOS authselect stack most sites use.
const DefaultPAMService = "password-auth"
