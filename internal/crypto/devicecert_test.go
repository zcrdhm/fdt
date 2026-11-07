package crypto

import (
	"crypto/x509"
	"testing"
)

func TestDeviceCertChainAndFingerprint(t *testing.T) {
	ca, err := GenerateCA("fdt-test")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, fp, err := ca.IssueDevice("atm-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(keyPEM) == 0 || fp == "" {
		t.Fatal("empty key or fingerprint")
	}
	cert, err := ParseCertificatePEM(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if got := CertFingerprint(cert); got != fp {
		t.Fatalf("fingerprint %s != %s", got, fp)
	}
	id, ok := DeviceIDFromCert(cert)
	if !ok || id != "atm-01" {
		t.Fatalf("device id %q ok=%v", id, ok)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatal(err)
	}
}
