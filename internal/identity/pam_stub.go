//go:build !linux || !cgo

package identity

import (
	"context"
	"fmt"
)

const pamAvailable = false

// PAMProvider is a stub when libpam is unavailable (non-Linux or CGO disabled).
type PAMProvider struct{}

func NewPAMProvider(cfg PAMConfig) (*PAMProvider, error) {
	_ = cfg.withDefaults()
	return nil, fmt.Errorf("本机 PAM 仅支持 Linux，且需启用 CGO 链接 libpam（当前二进制未编译 PAM）")
}

func (p *PAMProvider) Authenticate(context.Context, string, string) (*User, error) {
	return nil, ErrNotConfigured
}

func (p *PAMProvider) Lookup(context.Context, string) (*User, error) {
	return nil, ErrNotConfigured
}

func (p *PAMProvider) Close() error { return nil }
