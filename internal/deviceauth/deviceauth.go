// Package deviceauth authenticates an ATM to intranet services. When the
// request carries a client certificate signed by the FDT CA, the certificate
// fingerprint must match the registered device. Otherwise the dev bearer
// credential is checked. A presented certificate that does not match is
// rejected even if a bearer header is also set.
package deviceauth

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"strings"

	"fdt/internal/crypto"
	"fdt/internal/store"
)

var ErrUnauthenticated = errors.New("deviceauth: device not authenticated")

// Authenticate returns the registered device for r.
func Authenticate(r *http.Request, st *store.Store) (*store.Device, error) {
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		return fromCert(r, st)
	}
	return fromBearer(r, st)
}

func fromCert(r *http.Request, st *store.Store) (*store.Device, error) {
	cert := r.TLS.PeerCertificates[0]
	id, ok := crypto.DeviceIDFromCert(cert)
	if !ok {
		return nil, ErrUnauthenticated
	}
	dev, err := st.GetDevice(r.Context(), id)
	if err != nil || dev.Status != "active" {
		return nil, ErrUnauthenticated
	}
	fp := crypto.CertFingerprint(cert)
	if subtle.ConstantTimeCompare([]byte(dev.CertFingerprint), []byte(fp)) != 1 {
		return nil, ErrUnauthenticated
	}
	return dev, nil
}

func fromBearer(r *http.Request, st *store.Store) (*store.Device, error) {
	devID := r.Header.Get("X-Device-ID")
	authz := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if devID == "" || !strings.HasPrefix(authz, prefix) {
		return nil, ErrUnauthenticated
	}
	secret := strings.TrimPrefix(authz, prefix)
	dev, err := st.GetDevice(r.Context(), devID)
	if err != nil || dev.Status != "active" {
		return nil, ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare([]byte(dev.CertFingerprint), []byte(secret)) != 1 {
		return nil, ErrUnauthenticated
	}
	return dev, nil
}

// ClientTLSConfig is the ATM side of mTLS: present the device cert, trust the intranet CA.
func ClientTLSConfig(certPEM, keyPEM, caPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("deviceauth: no CA certificate")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ServerTLSConfig requires a client certificate signed by caPEM.
func ServerTLSConfig(caPEM []byte) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("deviceauth: no client CA certificate")
	}
	return &tls.Config{
		ClientCAs:  pool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// NewHTTPClient returns an HTTP client that presents the device certificate.
func NewHTTPClient(certFile, keyFile, caFile string) (*http.Client, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	cfg, err := ClientTLSConfig(certPEM, keyPEM, caPEM)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}, nil
}
