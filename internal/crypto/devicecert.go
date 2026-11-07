package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/url"
	"os"
	"time"
)

const deviceURNPrefix = "fdt:device:"

// CertFingerprint is the hex SHA-256 of the certificate DER. It is what the
// device table stores and what mTLS authentication compares.
func CertFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// CertFingerprintPEM parses the first CERTIFICATE block and returns its fingerprint.
func CertFingerprintPEM(pemBytes []byte) (string, error) {
	cert, err := ParseCertificatePEM(pemBytes)
	if err != nil {
		return "", err
	}
	return CertFingerprint(cert), nil
}

// CertFingerprintFile reads a PEM certificate and returns its fingerprint.
func CertFingerprintFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return CertFingerprintPEM(b)
}

// ParseCertificatePEM returns the first certificate in pemBytes.
func ParseCertificatePEM(pemBytes []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(pemBytes)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return nil, ErrBadPEM
	}
	return x509.ParseCertificate(blk.Bytes)
}

// DeviceIDFromCert reads the urn:fdt:device:<id> URI SAN.
func DeviceIDFromCert(cert *x509.Certificate) (string, bool) {
	for _, u := range cert.URIs {
		if u.Scheme == "urn" && len(u.Opaque) > len(deviceURNPrefix) && u.Opaque[:len(deviceURNPrefix)] == deviceURNPrefix {
			return u.Opaque[len(deviceURNPrefix):], true
		}
	}
	return "", false
}

// CA holds an Ed25519 certificate authority used to issue ATM client certs and
// the intranet server cert.
type CA struct {
	Cert    *x509.Certificate
	Key     ed25519.PrivateKey
	CertPEM []byte
	KeyPEM  []byte
}

// GenerateCA creates a new CA valid for ten years.
func GenerateCA(commonName string) (*CA, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"FDT"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return &CA{Cert: cert, Key: priv, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// IssueDevice signs a one-year client certificate whose URI SAN is
// urn:fdt:device:<deviceID>. The returned fingerprint matches CertFingerprint.
func (ca *CA) IssueDevice(deviceID string) (certPEM, keyPEM []byte, fingerprint string, err error) {
	if deviceID == "" {
		return nil, nil, "", errors.New("crypto: empty device id")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, "", err
	}
	uri := &url.URL{Scheme: "urn", Opaque: deviceURNPrefix + deviceID}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: deviceID, Organization: []string{"FDT"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{uri},
	}
	return ca.sign(tmpl, pub, priv)
}

// IssueServer signs a one-year server certificate for the intranet listeners.
func (ca *CA) IssueServer(commonName string, dns []string, ips []net.IP) (certPEM, keyPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{"FDT"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	certPEM, keyPEM, _, err = ca.sign(tmpl, pub, priv)
	return certPEM, keyPEM, err
}

func (ca *CA) sign(tmpl *x509.Certificate, pub ed25519.PublicKey, priv ed25519.PrivateKey) (certPEM, keyPEM []byte, fingerprint string, err error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, pub, ca.Key)
	if err != nil {
		return nil, nil, "", err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, "", err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, nil, "", err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, CertFingerprint(cert), nil
}

// LoadCA reads a CA certificate and PKCS#8 private key.
func LoadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	cert, err := ParseCertificatePEM(certPEM)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(keyPEM)
	if blk == nil {
		return nil, ErrBadPEM
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := keyAny.(ed25519.PrivateKey)
	if !ok {
		return nil, ErrBadKeySize
	}
	return &CA{Cert: cert, Key: priv, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

// WriteCertPEM writes a certificate (0644) and a private key (0600).
func WriteCertPEM(certPath string, certPEM []byte, keyPath string, keyPEM []byte) error {
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
