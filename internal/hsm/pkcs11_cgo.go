//go:build cgo

package hsm

import (
	"crypto/ed25519"
	"fmt"

	"github.com/miekg/pkcs11"
)

// CKM_EDDSA is PKCS#11 3.0 EdDSA (PureEdDSA for Ed25519 when the key is Edwards).
const ckmEDDSA = 0x00001057

// Sign finds the labeled private key and asks the token to sign. The private
// key is not returned to this process.
func (s *PKCS11Signer) Sign(msg []byte) ([]byte, error) {
	if s.modulePath == "" || s.keyLabel == "" {
		return nil, ErrPKCS11Unavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p := pkcs11.New(s.modulePath)
	if err := p.Initialize(); err != nil {
		return nil, fmt.Errorf("hsm: pkcs11 initialize: %w", err)
	}
	defer p.Destroy()
	defer p.Finalize()

	slot := s.slot
	if !s.slotSet {
		slots, err := p.GetSlotList(true)
		if err != nil {
			return nil, fmt.Errorf("hsm: slot list: %w", err)
		}
		if len(slots) == 0 {
			return nil, fmt.Errorf("hsm: no PKCS#11 token present")
		}
		slot = slots[0]
	}
	session, err := p.OpenSession(slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		return nil, fmt.Errorf("hsm: open session: %w", err)
	}
	defer p.CloseSession(session)
	if s.pin != "" {
		if err := p.Login(session, pkcs11.CKU_USER, s.pin); err != nil {
			return nil, fmt.Errorf("hsm: login: %w", err)
		}
		defer p.Logout(session)
	}

	tmpl := []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_LABEL, s.keyLabel),
	}
	if err := p.FindObjectsInit(session, tmpl); err != nil {
		return nil, fmt.Errorf("hsm: find init: %w", err)
	}
	objs, _, err := p.FindObjects(session, 1)
	p.FindObjectsFinal(session)
	if err != nil {
		return nil, fmt.Errorf("hsm: find key: %w", err)
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("hsm: private key %q not found in token", s.keyLabel)
	}

	mech := []*pkcs11.Mechanism{pkcs11.NewMechanism(ckmEDDSA, nil)}
	if err := p.SignInit(session, mech, objs[0]); err != nil {
		return nil, fmt.Errorf("hsm: sign init (CKM_EDDSA): %w", err)
	}
	sig, err := p.Sign(session, msg)
	if err != nil {
		return nil, fmt.Errorf("hsm: sign: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("hsm: expected %d-byte Ed25519 signature, got %d", ed25519.SignatureSize, len(sig))
	}
	return sig, nil
}
