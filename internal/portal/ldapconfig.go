package portal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fdt/internal/identity"
)

// LDAPFile is the on-disk LDAP settings edited from the Portal settings UI.
type LDAPFile struct {
	Enabled            bool   `json:"enabled"`
	URL                string `json:"url"`
	StartTLS           bool   `json:"startTLS"`
	CACertFile         string `json:"caCertFile,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`
	ServiceBindDN      string `json:"serviceBindDN"`
	ServicePassword    string `json:"servicePassword"`
	SearchBase         string `json:"searchBase"`
	SearchFilter       string `json:"searchFilter,omitempty"`
	AttrName           string `json:"attrName,omitempty"`
	AttrID             string `json:"attrID,omitempty"`
	AttrDept           string `json:"attrDept,omitempty"`
	AttrMail           string `json:"attrMail,omitempty"`
	AttrTitle          string `json:"attrTitle,omitempty"`
	AttrAccount        string `json:"attrAccount,omitempty"`
}

func LoadLDAPFile(path string) (LDAPFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return LDAPFile{}, nil
		}
		return LDAPFile{}, err
	}
	var f LDAPFile
	if err := json.Unmarshal(b, &f); err != nil {
		return LDAPFile{}, err
	}
	return f, nil
}

func SaveLDAPFile(path string, f LDAPFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LDAPFromEnv builds LDAPFile from fdt.env style keys.
func LDAPFromEnv(env map[string]string) LDAPFile {
	on := env["IDENTITY"] == "ldap" || env["LDAP"] == "1" || env["LDAP"] == "true"
	return LDAPFile{
		Enabled:            on,
		URL:                strings.TrimSpace(env["LDAP_URL"]),
		StartTLS:           env["LDAP_STARTTLS"] == "1" || env["LDAP_STARTTLS"] == "true",
		CACertFile:         strings.TrimSpace(env["LDAP_CA"]),
		InsecureSkipVerify: env["LDAP_INSECURE"] == "1" || env["LDAP_INSECURE"] == "true",
		ServiceBindDN:      strings.TrimSpace(env["LDAP_BIND_DN"]),
		ServicePassword:    env["LDAP_BIND_PASSWORD"],
		SearchBase:         strings.TrimSpace(env["LDAP_BASE"]),
		SearchFilter:       strings.TrimSpace(env["LDAP_FILTER"]),
		AttrName:           strings.TrimSpace(env["LDAP_ATTR_NAME"]),
		AttrID:             strings.TrimSpace(env["LDAP_ATTR_ID"]),
		AttrDept:           strings.TrimSpace(env["LDAP_ATTR_DEPT"]),
		AttrMail:           strings.TrimSpace(env["LDAP_ATTR_MAIL"]),
		AttrTitle:          strings.TrimSpace(env["LDAP_ATTR_TITLE"]),
		AttrAccount:        strings.TrimSpace(env["LDAP_ATTR_ACCOUNT"]),
	}
}

func (f LDAPFile) ToIdentity() identity.LDAPConfig {
	return identity.LDAPConfig{
		URL: f.URL, StartTLS: f.StartTLS, CACertFile: f.CACertFile,
		InsecureSkipVerify: f.InsecureSkipVerify,
		ServiceBindDN: f.ServiceBindDN, ServicePassword: f.ServicePassword,
		SearchBase: f.SearchBase, SearchFilter: f.SearchFilter,
		AttrName: f.AttrName, AttrID: f.AttrID, AttrDept: f.AttrDept,
		AttrMail: f.AttrMail, AttrTitle: f.AttrTitle, AttrAccount: f.AttrAccount,
	}
}

// PublicLDAP strips the bind password for UI display.
func (f LDAPFile) Public() map[string]any {
	return map[string]any{
		"enabled": f.Enabled, "url": f.URL, "startTLS": f.StartTLS,
		"caCertFile": f.CACertFile, "insecureSkipVerify": f.InsecureSkipVerify,
		"serviceBindDN": f.ServiceBindDN,
		"hasServicePassword": f.ServicePassword != "",
		"searchBase": f.SearchBase, "searchFilter": f.SearchFilter,
		"attrName": f.AttrName, "attrID": f.AttrID, "attrDept": f.AttrDept,
		"attrMail": f.AttrMail, "attrTitle": f.AttrTitle, "attrAccount": f.AttrAccount,
	}
}

// BuildIdentityProvider selects mock / none / ldap / pam from mode + optional ldap file.
func BuildIdentityProvider(mode string, ldapPath string, env map[string]string) (identity.Provider, string, error) {
	fileCfg, _ := LoadLDAPFile(ldapPath)
	envCfg := LDAPFromEnv(env)
	cfg := envCfg
	if fileCfg.URL != "" || fileCfg.SearchBase != "" || fileCfg.Enabled {
		cfg = fileCfg
		if cfg.ServicePassword == "" && envCfg.ServicePassword != "" {
			cfg.ServicePassword = envCfg.ServicePassword
		}
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if cfg.Enabled && mode != "pam" {
		mode = "ldap"
	}
	switch mode {
	case "none", "":
		return nil, "none", nil
	case "pam":
		svc := strings.TrimSpace(env["PAM_SERVICE"])
		p, err := identity.NewPAMProvider(identity.PAMConfig{Service: svc})
		if err != nil {
			return nil, "pam", err
		}
		return p, "pam", nil
	case "ldap":
		if cfg.URL == "" || cfg.SearchBase == "" {
			return nil, "ldap", fmt.Errorf("LDAP 未配置完整（需要 url + searchBase）")
		}
		p, err := identity.NewLDAPProvider(cfg.ToIdentity())
		if err != nil {
			return nil, "ldap", err
		}
		return p, "ldap", nil
	default: // mock
		return identity.NewMockProvider(), "mock", nil
	}
}
