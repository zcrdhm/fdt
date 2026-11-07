// Package gwacme integrates Let's Encrypt (ACME) certificate issuance for the
// DMZ gateway so operators need not place TLS files by hand.
package gwacme

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/acme/autocert"
)

// Config drives Let's Encrypt issuance.
type Config struct {
	Enabled bool     `json:"enabled"`
	Domain  string   `json:"domain"`
	Aliases []string `json:"aliases,omitempty"` // extra SANs / HostWhitelist
	Email   string   `json:"email"`
	Cache   string   `json:"cache"` // directory for certs + account key
	// HTTPAddr is where HTTP-01 challenges are served (default :80).
	HTTPAddr string `json:"httpAddr,omitempty"`
}

// Status is returned to the admin UI.
type Status struct {
	Enabled    bool     `json:"enabled"`
	Domain     string   `json:"domain"`
	Aliases    []string `json:"aliases,omitempty"`
	Email      string   `json:"email"`
	Cache      string   `json:"cache"`
	HTTPAddr   string   `json:"httpAddr"`
	HasCache   bool     `json:"hasCache"`
	Ready      bool     `json:"ready"` // manager constructed
	Message    string   `json:"message,omitempty"`
}

// Manager wraps autocert and optional :80 challenge listener.
type Manager struct {
	mu     sync.Mutex
	cfg    Config
	m      *autocert.Manager
	httpSrv *http.Server
}

// LoadFile reads JSON config; missing file returns zero Config.
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
	return c, nil
}

// SaveFile writes ACME config atomically.
func SaveFile(path string, c Config) error {
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

// FromEnv builds Config from fdt.env style map.
func FromEnv(env map[string]string, defaultCache string) Config {
	on := env["ACME"] == "1" || env["ACME"] == "true" || env["LETSENCRYPT"] == "1"
	cache := env["ACME_CACHE"]
	if cache == "" {
		cache = defaultCache
	}
	httpAddr := env["ACME_HTTP"]
	if httpAddr == "" {
		httpAddr = "0.0.0.0:80"
	}
	return Config{
		Enabled:  on,
		Domain:   strings.TrimSpace(env["ACME_DOMAIN"]),
		Email:    strings.TrimSpace(env["ACME_EMAIL"]),
		Cache:    cache,
		HTTPAddr: httpAddr,
	}
}

// Start builds the autocert manager and HTTP-01 listener. Caller uses TLSConfig().
func (g *Manager) Start(cfg Config) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.httpSrv != nil {
		_ = g.httpSrv.Close()
		g.httpSrv = nil
	}
	g.cfg = cfg
	g.m = nil
	if !cfg.Enabled {
		return nil
	}
	domain := strings.TrimSpace(strings.ToLower(cfg.Domain))
	if domain == "" {
		return fmt.Errorf("gwacme: ACME_DOMAIN required")
	}
	hosts := []string{domain}
	seen := map[string]bool{domain: true}
	for _, a := range cfg.Aliases {
		a = strings.TrimSpace(strings.ToLower(a))
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		hosts = append(hosts, a)
	}
	cache := cfg.Cache
	if cache == "" {
		cache = "/var/lib/fdt/acme"
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(hosts...),
		Cache:      autocert.DirCache(cache),
		Email:      cfg.Email,
	}
	g.m = m
	httpAddr := cfg.HTTPAddr
	if httpAddr == "" {
		httpAddr = "0.0.0.0:80"
	}
	hs := &http.Server{Addr: httpAddr, Handler: m.HTTPHandler(nil)}
	g.httpSrv = hs
	go func() {
		if err := hs.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Logged by caller via Message; keep serving if :80 busy.
			fmt.Fprintf(os.Stderr, "gwacme: HTTP-01 listener %s: %v\n", httpAddr, err)
		}
	}()
	return nil
}

// TLSConfig returns a TLS config that obtains certs via ACME, or nil if disabled.
func (g *Manager) TLSConfig() *tls.Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.m == nil {
		return nil
	}
	cfg := g.m.TLSConfig()
	cfg.MinVersion = tls.VersionTLS12
	return cfg
}

// Status reports current ACME state.
func (g *Manager) Status() Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := Status{
		Enabled: g.cfg.Enabled, Domain: g.cfg.Domain, Aliases: g.cfg.Aliases,
		Email: g.cfg.Email, Cache: g.cfg.Cache, HTTPAddr: g.cfg.HTTPAddr, Ready: g.m != nil,
	}
	if g.cfg.Cache != "" {
		if ents, err := os.ReadDir(g.cfg.Cache); err == nil && len(ents) > 0 {
			st.HasCache = true
		}
	}
	if !g.cfg.Enabled {
		st.Message = "ACME 未启用（手动证书或明文）"
	} else if g.m == nil {
		st.Message = "配置不完整或启动失败"
	} else {
		st.Message = "已启用：首次 HTTPS 访问时向 Let's Encrypt 申请并自动续期"
	}
	return st
}

// Config returns a copy of the active config.
func (g *Manager) Config() Config {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cfg
}

// Shutdown stops the HTTP-01 listener.
func (g *Manager) Shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.httpSrv != nil {
		_ = g.httpSrv.Close()
		g.httpSrv = nil
	}
}
