//go:build !cgo

package hsm

import "fmt"

// Sign reports that this build cannot load a PKCS#11 module. Rebuild the
// gateway with CGO_ENABLED=1 and the vendor library present to sign inside the HSM.
func (s *PKCS11Signer) Sign(_ []byte) ([]byte, error) {
	if s.modulePath == "" || s.keyLabel == "" {
		return nil, ErrPKCS11Unavailable
	}
	return nil, fmt.Errorf("%w: binary built with CGO_ENABLED=0, cannot load %s", ErrPKCS11Unavailable, s.modulePath)
}
