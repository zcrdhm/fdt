// Package token builds, signs, encodes and verifies the single-frame QR tokens
// that are the only thing crossing the air gap (technical design §4.4).
//
// Wire format:
//
//	payload = canonical CBOR(token)
//	sig     = ed25519(SK, payload)          // raw 64 bytes
//	QR      = FDT1.<tenant>.base45(deflate(payload || sig))
//
// The signature covers the exact CBOR bytes, so verification never re-encodes.
package token

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"fdt/internal/codec"
	"fdt/internal/crypto"
	"fdt/internal/model"
)

// Signer abstracts the signing key so a software key or an HSM both work.
type Signer interface {
	Sign(msg []byte) ([]byte, error)
	KID() string
}

var (
	ErrDecode         = errors.New("token: malformed envelope")
	ErrExpired        = errors.New("token: expired")
	ErrNotYet         = errors.New("token: not yet valid")
	ErrBadVersion     = errors.New("token: unsupported version")
	ErrTenantMismatch = errors.New("token: tenant segment does not match signed payload")
	ErrBadSigLen      = errors.New("token: signature is not 64 bytes")
)

const Version = 1

func sign(signer Signer, tenant string, payload any) (string, error) {
	body, err := codec.MarshalCanonical(payload)
	if err != nil {
		return "", err
	}
	sig, err := signer.Sign(body)
	if err != nil {
		return "", err
	}
	if len(sig) != ed25519.SignatureSize {
		return "", ErrBadSigLen
	}
	blob := make([]byte, 0, len(body)+len(sig))
	blob = append(blob, body...)
	blob = append(blob, sig...)
	return codec.EncodeFrame(tenant, blob)
}

func open(frame string, pub ed25519.PublicKey, out any) (string, error) {
	tenant, raw, err := codec.DecodeFrame(frame)
	if err != nil {
		return "", err
	}
	if len(raw) <= ed25519.SignatureSize {
		return "", ErrDecode
	}
	body := raw[:len(raw)-ed25519.SignatureSize]
	sig := raw[len(raw)-ed25519.SignatureSize:]
	if err := crypto.Verify(pub, body, sig); err != nil {
		return "", err
	}
	if err := codec.UnmarshalCanonical(body, out); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecode, err)
	}
	return tenant, nil
}

// PeekTenant returns the plaintext tenant segment of a frame so a multi-tenant
// gateway can select the right verification key before opening it.
func PeekTenant(frame string) (string, error) { return codec.PeekTenant(frame) }

// SignRequest produces the QR#1 frame for a request token.
func SignRequest(signer Signer, t *model.RequestToken) (string, error) {
	t.V = Version
	if t.Tenant == "" {
		t.Tenant = codec.DefaultTenant
	}
	if t.KID == "" {
		t.KID = signer.KID()
	}
	return sign(signer, t.Tenant, t)
}

// SignRelease produces the QR#2 frame for a release token.
func SignRelease(signer Signer, t *model.ReleaseToken) (string, error) {
	t.V = Version
	if t.Tenant == "" {
		t.Tenant = codec.DefaultTenant
	}
	if t.KID == "" {
		t.KID = signer.KID()
	}
	return sign(signer, t.Tenant, t)
}

// OpenRequest decodes and verifies a QR#1 frame against PK_内网.
func OpenRequest(frame string, pub ed25519.PublicKey) (*model.RequestToken, error) {
	var t model.RequestToken
	tenant, err := open(frame, pub, &t)
	if err != nil {
		return nil, err
	}
	if t.V != Version {
		return nil, ErrBadVersion
	}
	if t.Tenant != "" && t.Tenant != tenant {
		return nil, ErrTenantMismatch
	}
	t.Tenant = tenant
	return &t, nil
}

// OpenRelease decodes and verifies a QR#2 frame against PK_网关.
func OpenRelease(frame string, pub ed25519.PublicKey) (*model.ReleaseToken, error) {
	var t model.ReleaseToken
	tenant, err := open(frame, pub, &t)
	if err != nil {
		return nil, err
	}
	if t.V != Version {
		return nil, ErrBadVersion
	}
	if t.Tenant != "" && t.Tenant != tenant {
		return nil, ErrTenantMismatch
	}
	t.Tenant = tenant
	return &t, nil
}

// CheckValidity enforces the time window. now is passed in for testability.
func CheckValidity(iat, exp int64, now time.Time) error {
	un := now.Unix()
	if exp != 0 && un > exp {
		return ErrExpired
	}
	if iat != 0 && un+1 < iat { // +1s clock skew tolerance
		return ErrNotYet
	}
	return nil
}
