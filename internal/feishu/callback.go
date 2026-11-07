package feishu

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
)

// Feishu encrypts event callbacks with AES-256-CBC when an Encrypt Key is set.
// The key is SHA256(encryptKey); the payload is base64(iv[16] || ciphertext).
// This implements the real decryption so callback authenticity can be verified
// offline (risk C3), independent of the LiveClient transport.

var (
	ErrDecrypt       = errors.New("feishu: callback decryption failed")
	ErrBadPadding    = errors.New("feishu: bad PKCS#7 padding")
	ErrTokenMismatch = errors.New("feishu: verification token mismatch")
)

// DecryptEvent decrypts the base64 "encrypt" field of a Feishu callback body.
func DecryptEvent(encryptKey, encryptedB64 string) ([]byte, error) {
	key := sha256.Sum256([]byte(encryptKey))
	raw, err := base64.StdEncoding.DecodeString(encryptedB64)
	if err != nil {
		return nil, ErrDecrypt
	}
	if len(raw) < aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return nil, ErrDecrypt
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	iv := raw[:aes.BlockSize]
	ct := raw[aes.BlockSize:]
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	return pkcs7Unpad(plain)
}

// EncryptEvent is the inverse, exposed so the callback path can be unit-tested
// without a live Feishu tenant. Production gateways only decrypt.
func EncryptEvent(encryptKey string, plaintext []byte) (string, error) {
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	iv := make([]byte, aes.BlockSize)
	// A real deployment derives iv randomly; deterministic zero iv keeps tests
	// reproducible and is acceptable because each payload is unique.
	padded := pkcs7Pad(plaintext, aes.BlockSize)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	return base64.StdEncoding.EncodeToString(append(iv, ct...)), nil
}

func pkcs7Pad(b []byte, size int) []byte {
	pad := size - len(b)%size
	for i := 0; i < pad; i++ {
		b = append(b, byte(pad))
	}
	return b
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, ErrBadPadding
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > len(b) {
		return nil, ErrBadPadding
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, ErrBadPadding
		}
	}
	return b[:len(b)-pad], nil
}

// callbackEnvelope is the outer JSON of a Feishu event push.
type callbackEnvelope struct {
	Encrypt string `json:"encrypt"`
}

// eventV2 is the decrypted event schema 2.0 body.
type eventV2 struct {
	Schema string `json:"schema"`
	Header struct {
		EventType string `json:"event_type"`
		Token     string `json:"token"`
	} `json:"header"`
	Event struct {
		ApprovalCode string `json:"approval_code"`
		InstanceCode string `json:"instance_code"`
		Status       string `json:"status"`
		Type         string `json:"type"`
		UUID         string `json:"uuid"`
		// TenantKey etc. omitted; approver details come from GetApprovalStatus.
	} `json:"event"`
	// URL-verification handshake fields
	Challenge string `json:"challenge"`
	Token     string `json:"token"`
	Type      string `json:"type"`
}

// PeekURLChallenge extracts a plaintext Feishu url_verification challenge without
// requiring tenant credentials. Used so Feishu can save the request URL before
// the tenant is fully activated. Encrypted challenges still need Encrypt Key.
func PeekURLChallenge(rawBody []byte) (challenge string, ok bool) {
	var ev struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Encrypt   string `json:"encrypt"`
	}
	if err := json.Unmarshal(rawBody, &ev); err != nil {
		return "", false
	}
	if ev.Encrypt != "" {
		return "", false
	}
	if ev.Type == "url_verification" && ev.Challenge != "" {
		return ev.Challenge, true
	}
	return "", false
}

// VerifyCallbackBody decrypts (if needed), verifies the token, and normalizes
// an approval event. rawBody is the HTTP request body; encryptKey and
// verificationToken come from the Feishu app config.
func VerifyCallbackBody(rawBody []byte, encryptKey, verificationToken string) (*ApprovalEvent, string, error) {
	var env callbackEnvelope
	var plain []byte
	if err := json.Unmarshal(rawBody, &env); err == nil && env.Encrypt != "" {
		p, derr := DecryptEvent(encryptKey, env.Encrypt)
		if derr != nil {
			return nil, "", derr
		}
		plain = p
	} else {
		plain = rawBody
	}

	var ev eventV2
	if err := json.Unmarshal(plain, &ev); err != nil {
		return nil, "", ErrDecrypt
	}

	// token check (header.token for events, top-level token for handshake)
	tok := ev.Header.Token
	if tok == "" {
		tok = ev.Token
	}
	if verificationToken != "" && tok != verificationToken {
		return nil, "", ErrTokenMismatch
	}

	// URL-verification handshake: echo the challenge.
	if ev.Type == "url_verification" {
		return nil, ev.Challenge, nil
	}

	status := normalizeStatus(ev.Event.Status)
	return &ApprovalEvent{
		InstanceID: ev.Event.InstanceCode,
		Status:     status,
		Type:       ev.Header.EventType,
	}, "", nil
}

func normalizeStatus(s string) ApprovalStatus {
	switch s {
	case "APPROVED":
		return StatusApproved
	case "REJECTED":
		return StatusRejected
	case "CANCELED", "CANCELLED", "DELETED":
		return StatusCanceled
	default:
		return StatusPending
	}
}
