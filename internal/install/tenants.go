package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"fdt/internal/gateway"
)

// materializeTenants copies each tenant's key files under lay.Etc/tenants/<id>/
// and writes a rewritten tenants.json with absolute paths for the gateway to load.
func materializeTenants(srcPath string, lay Layout, forceMock bool) (destConfig string, ids []string, err error) {
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return "", nil, err
	}
	var tf gateway.TenantsFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return "", nil, fmt.Errorf("parse tenants: %w", err)
	}
	baseDir := filepath.Dir(srcPath)
	tenantsRoot := filepath.Join(lay.Etc, "tenants")
	if err := os.MkdirAll(tenantsRoot, 0o700); err != nil {
		return "", nil, err
	}

	out := gateway.TenantsFile{
		ReleaseTTL:     tf.ReleaseTTL,
		JSAPIAppID:     tf.JSAPIAppID,
		JSAPIAppSecret: tf.JSAPIAppSecret,
	}
	// Empty tenants is valid: operator creates tenants later via /admin.
	if len(tf.Tenants) == 0 {
		if out.ReleaseTTL == "" {
			out.ReleaseTTL = "72h"
		}
		destConfig = filepath.Join(lay.Etc, "tenants.json")
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(destConfig, append(b, '\n'), 0o600); err != nil {
			return "", nil, err
		}
		return destConfig, nil, nil
	}
	for _, t := range tf.Tenants {
		if t.ID == "" {
			return "", nil, fmt.Errorf("tenant entry missing id")
		}
		dir := filepath.Join(tenantsRoot, t.ID)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, err
		}
		pubSrc := resolvePath(baseDir, t.InternalPub)
		keySrc := resolvePath(baseDir, t.GatewayKey)
		pubDst := filepath.Join(dir, "pk_internal.pem")
		keyDst := filepath.Join(dir, "sk_gateway.pem")
		if err := copyFile(pubSrc, pubDst); err != nil {
			return "", nil, fmt.Errorf("tenant %s internalPub: %w", t.ID, err)
		}
		if err := copyFile(keySrc, keyDst); err != nil {
			return "", nil, fmt.Errorf("tenant %s gatewayKey: %w", t.ID, err)
		}
		// Optional companion public key if present next to the private key.
		if pk := filepath.Join(filepath.Dir(keySrc), "pk_gateway.pem"); fileExists(pk) {
			_ = copyFile(pk, filepath.Join(dir, "pk_gateway.pem"))
		}
		feishu := t.Feishu
		if forceMock {
			feishu = gateway.TenantFeishuFile{Mode: "mock", AutoApprove: true, Approver: "mock-approver"}
		}
		out.Tenants = append(out.Tenants, gateway.TenantFileEntry{
			ID: t.ID, InternalPub: pubDst, GatewayKey: keyDst, Feishu: feishu,
		})
		ids = append(ids, t.ID)
	}
	destConfig = filepath.Join(lay.Etc, "tenants.json")
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(destConfig, append(b, '\n'), 0o600); err != nil {
		return "", nil, err
	}
	return destConfig, ids, nil
}

func resolvePath(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
