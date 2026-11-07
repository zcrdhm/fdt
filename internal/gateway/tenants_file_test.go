package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"fdt/internal/crypto"
)

func TestLoadTenantsFile(t *testing.T) {
	dir := t.TempDir()
	a := mustKP(t)
	b := mustKP(t)
	apub := filepath.Join(dir, "a_pub.pem")
	ask := filepath.Join(dir, "a_sk.pem")
	bpub := filepath.Join(dir, "b_pub.pem")
	bsk := filepath.Join(dir, "b_sk.pem")
	mustWritePub(t, apub, a)
	mustWritePriv(t, ask, a)
	mustWritePub(t, bpub, b)
	mustWritePriv(t, bsk, b)

	cfg := filepath.Join(dir, "tenants.json")
	body := `{
  "releaseTTL": "5m",
  "tenants": [
    {"id":"acme","internalPub":"` + apub + `","gatewayKey":"` + ask + `","feishu":{"mode":"mock","autoApprove":true}},
    {"id":"beta","internalPub":"` + bpub + `","gatewayKey":"` + bsk + `","feishu":{"mode":"mock"}}
  ]
}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := LoadTenantsFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ids := res.Service.Tenants()
	if len(ids) != 2 || ids[0] != "acme" || ids[1] != "beta" {
		t.Fatalf("tenants=%v", ids)
	}
	if res.JSAPI == nil {
		t.Fatal("jsapi nil")
	}
}

func mustKP(t *testing.T) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return kp
}

func mustWritePub(t *testing.T, path string, kp *crypto.KeyPair) {
	t.Helper()
	if err := crypto.WritePublicPEM(path, kp.Pub); err != nil {
		t.Fatal(err)
	}
}

func mustWritePriv(t *testing.T, path string, kp *crypto.KeyPair) {
	t.Helper()
	if err := crypto.WritePrivatePEM(path, kp.Priv); err != nil {
		t.Fatal(err)
	}
}
