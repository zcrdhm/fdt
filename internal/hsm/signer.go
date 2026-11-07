// Package hsm abstracts the signing key so the gateway can sign QR#2 with a
// hardware-backed key in production while development uses a software signer.
// Every signer satisfies the structural interface expected by internal/token.
package hsm

import (
	"crypto/ed25519"
	"errors"
	"sync"

	"fdt/internal/crypto"
)

// Signer signs arbitrary payloads and exposes its key id and public key.
type Signer interface {
	Sign(msg []byte) ([]byte, error)
	KID() string
	Public() ed25519.PublicKey
}

// SoftwareSigner is an in-process Ed25519 signer backed by a PEM key file.
// Used for development and as the reference implementation. In production the
// gateway uses PKCS11Signer (below) so SK_网关 never leaves the HSM.
type SoftwareSigner struct {
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func NewSoftwareSigner(kid string, priv ed25519.PrivateKey) *SoftwareSigner {
	return &SoftwareSigner{kid: kid, priv: priv, pub: priv.Public().(ed25519.PublicKey)}
}

func NewSoftwareSignerFromFile(kid, path string) (*SoftwareSigner, error) {
	priv, err := crypto.ReadPrivatePEM(path)
	if err != nil {
		return nil, err
	}
	return NewSoftwareSigner(kid, priv), nil
}

func (s *SoftwareSigner) Sign(msg []byte) ([]byte, error) {
	return ed25519.Sign(s.priv, msg), nil
}
func (s *SoftwareSigner) KID() string               { return s.kid }
func (s *SoftwareSigner) Public() ed25519.PublicKey { return s.pub }

// ErrPKCS11Unavailable is returned when no PKCS#11 module is configured, or when
// this binary was built without cgo and cannot load a vendor module.
var ErrPKCS11Unavailable = errors.New("hsm: PKCS#11 provider not configured")

// PKCS11Signer signs with an Ed25519 key that stays inside a PKCS#11 token.
// The process holds the PIN and a key label, never the private key bytes.
type PKCS11Signer struct {
	mu         sync.Mutex
	kid        string
	pub        ed25519.PublicKey
	modulePath string
	slot       uint
	slotSet    bool
	keyLabel   string
	pin        string
}

type PKCS11Config struct {
	KID        string
	ModulePath string // path to the vendor PKCS#11 library (.so)
	Slot       uint   // token slot; ignored unless SlotSet
	SlotSet    bool
	KeyLabel   string // CKA_LABEL of the Ed25519 private key
	PIN        string
	PublicKey  ed25519.PublicKey // matching public key, distributed to ATMs
}

func NewPKCS11Signer(cfg PKCS11Config) *PKCS11Signer {
	return &PKCS11Signer{
		kid: cfg.KID, modulePath: cfg.ModulePath, slot: cfg.Slot, slotSet: cfg.SlotSet,
		keyLabel: cfg.KeyLabel, pin: cfg.PIN, pub: cfg.PublicKey,
	}
}

func (s *PKCS11Signer) KID() string               { return s.kid }
func (s *PKCS11Signer) Public() ed25519.PublicKey { return s.pub }
