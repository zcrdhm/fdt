package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"fdt/internal/feishu"
	"fdt/internal/model"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gw.db")
	ss, err := OpenSessionStore("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	sess := &session{
		Tenant: "acme", InstanceID: "inst-1", ManifestText: "m",
		Status: feishu.StatusApproved, Approver: "李四",
		QR2Frame: "FDT1.acme.xxx", QR2Exp: time.Now().Add(time.Minute),
		ReqToken: &model.RequestToken{ReqID: "req-1", Tenant: "acme"},
	}
	if err := ss.Save(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	svc := New(time.Minute)
	svc.Sessions = ss
	if err := svc.RestoreSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := svc.PollQR2(context.Background(), "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.QR2Frame != "FDT1.acme.xxx" || got.Tenant != "acme" {
		t.Fatalf("%+v", got)
	}
}
