// Package crypto wraps Ed25519 signing/verification and key persistence for
// the FDT trust chain. Two key pairs exist in production:
//
//	SK_内网 / PK_内网  — signs QR#1 request tokens (verified by the gateway)
//	SK_网关 / PK_网关  — signs QR#2 release tokens (verified offline by the ATM)
//
// Private keys live in an HSM in production (see internal/hsm); this package
// provides the software signer used for development and the reference verify
// path used by every component.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
)

const (
	pemTypePriv = "FDT ED25519 PRIVATE KEY"
	pemTypePub  = "FDT ED25519 PUBLIC KEY"
)

var (
	ErrBadKeySize = errors.New("crypto: invalid ed25519 key size")
	ErrVerify     = errors.New("crypto: signature verification failed")
	ErrBadPEM     = errors.New("crypto: malformed PEM block")
)

// KeyPair holds an Ed25519 signing key and its public counterpart.
type KeyPair struct {
	Priv ed25519.PrivateKey
	Pub  ed25519.PublicKey
}

// GenerateKeyPair creates a fresh Ed25519 key pair from crypto/rand.
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Priv: priv, Pub: pub}, nil
}

// Sign signs msg with the private key.
func (k *KeyPair) Sign(msg []byte) []byte {
	return ed25519.Sign(k.Priv, msg)
}

// Verify reports whether sig is a valid signature of msg under public key pub.
func Verify(pub ed25519.PublicKey, msg, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return ErrBadKeySize
	}
	if !ed25519.Verify(pub, msg, sig) {
		return ErrVerify
	}
	return nil
}

// WritePrivatePEM saves the private key to path with 0600 permissions.
func WritePrivatePEM(path string, priv ed25519.PrivateKey) error {
	blk := pem.EncodeToMemory(&pem.Block{Type: pemTypePriv, Bytes: priv})
	return os.WriteFile(path, blk, 0o600)
}

// WritePublicPEM saves the public key (hex-of-raw-bytes inside PEM) to path.
func WritePublicPEM(path string, pub ed25519.PublicKey) error {
	raw, err := hex.DecodeString(hex.EncodeToString(pub))
	if err != nil {
		return err
	}
	blk := pem.EncodeToMemory(&pem.Block{Type: pemTypePub, Bytes: raw})
	return os.WriteFile(path, blk, 0o644)
}

// ReadPrivatePEM loads a private key from a PEM file.
func ReadPrivatePEM(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil || blk.Type != pemTypePriv {
		return nil, ErrBadPEM
	}
	if len(blk.Bytes) != ed25519.PrivateKeySize {
		return nil, ErrBadKeySize
	}
	return ed25519.PrivateKey(blk.Bytes), nil
}

// ReadPublicPEM loads a public key from a PEM file.
func ReadPublicPEM(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(data)
	if blk == nil || blk.Type != pemTypePub {
		return nil, ErrBadPEM
	}
	if len(blk.Bytes) != ed25519.PublicKeySize {
		return nil, ErrBadKeySize
	}
	return ed25519.PublicKey(blk.Bytes), nil
}
