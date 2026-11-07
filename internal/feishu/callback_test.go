package feishu

import (
	"encoding/json"
	"testing"
)

func TestCallbackDecryptVerifyAndParse(t *testing.T) {
	encryptKey := "test-encrypt-key"
	verifyToken := "test-verify-token"

	event := map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_type": "approval_instance", "token": verifyToken},
		"event": map[string]any{
			"approval_code": "APPROVAL-X",
			"instance_code": "fs-inst-req-1",
			"status":        "APPROVED",
			"type":          "approval_instance",
		},
	}
	plain, _ := json.Marshal(event)
	enc, err := EncryptEvent(encryptKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"encrypt": enc})

	ev, challenge, err := VerifyCallbackBody(body, encryptKey, verifyToken)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if challenge != "" {
		t.Fatal("unexpected challenge")
	}
	if ev.InstanceID != "fs-inst-req-1" || ev.Status != StatusApproved {
		t.Fatalf("bad parsed event: %+v", ev)
	}
}

func TestCallbackRejectsWrongToken(t *testing.T) {
	encryptKey := "k"
	event := map[string]any{
		"schema": "2.0",
		"header": map[string]any{"event_type": "approval_instance", "token": "good-token"},
		"event":  map[string]any{"instance_code": "x", "status": "APPROVED"},
	}
	plain, _ := json.Marshal(event)
	enc, _ := EncryptEvent(encryptKey, plain)
	body, _ := json.Marshal(map[string]string{"encrypt": enc})

	if _, _, err := VerifyCallbackBody(body, encryptKey, "wrong-token"); err != ErrTokenMismatch {
		t.Fatalf("want ErrTokenMismatch, got %v", err)
	}
}

func TestCallbackRejectsWrongEncryptKey(t *testing.T) {
	event := map[string]any{"schema": "2.0", "header": map[string]any{"token": "t"}}
	plain, _ := json.Marshal(event)
	enc, _ := EncryptEvent("right-key", plain)
	body, _ := json.Marshal(map[string]string{"encrypt": enc})
	if _, _, err := VerifyCallbackBody(body, "wrong-key", "t"); err == nil {
		t.Fatal("decryption with wrong key must fail")
	}
}

func TestURLVerificationHandshake(t *testing.T) {
	handshake := map[string]any{"type": "url_verification", "challenge": "abc123", "token": "t"}
	plain, _ := json.Marshal(handshake)
	// unencrypted body path
	ev, challenge, err := VerifyCallbackBody(plain, "", "t")
	if err != nil {
		t.Fatal(err)
	}
	if challenge != "abc123" || ev != nil {
		t.Fatalf("handshake not echoed: challenge=%q ev=%+v", challenge, ev)
	}
}

func TestMockClientAutoApprove(t *testing.T) {
	m := NewMockClient(true, "李四")
	inst, err := m.CreateApprovalInstance(nil, CreateInstanceRequest{ReqID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != StatusApproved || inst.Approver != "李四" {
		t.Fatalf("mock did not auto-approve: %+v", inst)
	}
	got, _ := m.GetApprovalStatus(nil, inst.ID)
	if got.Status != StatusApproved {
		t.Fatal("status lookup failed")
	}
	if _, err := m.GetApprovalStatus(nil, "nope"); err != ErrUnknownInst {
		t.Fatalf("want ErrUnknownInst, got %v", err)
	}
}
