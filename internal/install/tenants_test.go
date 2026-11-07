package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fdt/internal/crypto"
	"fdt/internal/gateway"
)

func TestMaterializeTenants(t *testing.T) {
	src := t.TempDir()
	prefix := t.TempDir()
	lay := layout(prefix)

	a, _ := crypto.GenerateKeyPair()
	b, _ := crypto.GenerateKeyPair()
	adir := filepath.Join(src, "acme")
	bdir := filepath.Join(src, "beta")
	_ = os.MkdirAll(adir, 0o755)
	_ = os.MkdirAll(bdir, 0o755)
	_ = crypto.WritePublicPEM(filepath.Join(adir, "pk_internal.pem"), a.Pub)
	_ = crypto.WritePrivatePEM(filepath.Join(adir, "sk_gateway.pem"), a.Priv)
	_ = crypto.WritePublicPEM(filepath.Join(bdir, "pk_internal.pem"), b.Pub)
	_ = crypto.WritePrivatePEM(filepath.Join(bdir, "sk_gateway.pem"), b.Priv)

	cfgPath := filepath.Join(src, "tenants.json")
	raw := `{
  "tenants": [
    {"id":"acme","internalPub":"./acme/pk_internal.pem","gatewayKey":"./acme/sk_gateway.pem","feishu":{"mode":"live","appId":"x","appSecret":"y","approvalCode":"z"}},
    {"id":"beta","internalPub":"./beta/pk_internal.pem","gatewayKey":"./beta/sk_gateway.pem","feishu":{"mode":"live","appId":"x2","appSecret":"y2","approvalCode":"z2"}}
  ]
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	dest, ids, err := materializeTenants(cfgPath, lay, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids=%v", ids)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var tf gateway.TenantsFile
	if err := json.Unmarshal(data, &tf); err != nil {
		t.Fatal(err)
	}
	if tf.Tenants[0].Feishu.Mode != "mock" {
		t.Fatalf("forceMock: %+v", tf.Tenants[0].Feishu)
	}
	if _, err := gateway.LoadTenantsFile(dest); err != nil {
		t.Fatal(err)
	}
}
