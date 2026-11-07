package gwadmin

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fdt/internal/crypto"
	"fdt/internal/gateway"
)

func TestRegisterThenConsoleBind(t *testing.T) {
	dir := t.TempDir()
	svc := gateway.New(0)
	st := &Store{
		Dir: filepath.Join(dir, "tenants"),
		Index: filepath.Join(dir, "tenants.json"),
		Service: svc,
	}
	_ = st.EnsureLayout()

	v, err := st.CreateTenant("acme", "Acme", time.Time{})
	if err != nil || v.Status != StatusPending || v.RegisterURL == "" {
		t.Fatalf("create: %+v %v", v, err)
	}
	tok := v.RegisterURL[len("/register/#"):]

	reg, err := st.RegisterWithInvite(tok, "ops", "password123", "张三", "a@b.c")
	if err != nil || reg.Status != StatusRegistered {
		t.Fatalf("register: %+v %v", reg, err)
	}
	if _, err := st.LookupInvite(tok); err == nil {
		t.Fatal("invite should be cleared")
	}

	id, err := st.AuthenticateTenant("ops", "password123")
	if err != nil || id != "acme" {
		t.Fatalf("auth %q %v", id, err)
	}

	ik, _ := crypto.GenerateKeyPair()
	pubPath := filepath.Join(dir, "pk_internal.pem")
	_ = crypto.WritePublicPEM(pubPath, ik.Pub)
	pem, _ := os.ReadFile(pubPath)
	if _, err := st.UploadInternalPub("acme", pem); err != nil {
		t.Fatal(err)
	}
	v2, err := st.UpdateFeishuFromConsole("acme", gateway.TenantFeishuFile{Mode: "mock", AutoApprove: true})
	if err != nil || v2.Status != StatusActive {
		t.Fatalf("bind: %+v %v", v2, err)
	}
	if !svc.HasTenant("acme") {
		t.Fatal("not hot-loaded")
	}
}

func TestAuth(t *testing.T) {
	a := NewAuth("admin", "secret")
	tok, err := a.Login("admin", "secret")
	if err != nil || !a.Valid(tok) {
		t.Fatal(err)
	}
}
