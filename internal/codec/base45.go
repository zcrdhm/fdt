// Package codec provides compact, deterministic binary-to-text encoding used
// to pack signed tokens into a single QR code frame.
package codec

import (
	"errors"
	"strings"
)

// base45 alphabet per RFC 9285. Chosen because it maps to the QR alphanumeric
// mode densely and survives camera capture better than raw bytes.
const base45Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

var base45Reverse = func() [256]int8 {
	var rev [256]int8
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(base45Alphabet); i++ {
		rev[base45Alphabet[i]] = int8(i)
	}
	return rev
}()

// ErrInvalidBase45 is returned when input contains characters outside the alphabet
// or has a dangling single-character group.
var ErrInvalidBase45 = errors.New("codec: invalid base45 input")

// Base45Encode encodes arbitrary bytes to a base45 string (RFC 9285).
func Base45Encode(data []byte) string {
	var sb strings.Builder
	sb.Grow(len(data) * 2)
	for i := 0; i < len(data); {
		rem := len(data) - i
		if rem >= 2 {
			n := int(data[i])<<8 | int(data[i+1])
			a := n / (45 * 45)
			n -= a * 45 * 45
			b := n / 45
			c := n - b*45
			sb.WriteByte(base45Alphabet[a])
			sb.WriteByte(base45Alphabet[b])
			sb.WriteByte(base45Alphabet[c])
			i += 2
		} else {
			n := int(data[i])
			a := n / 45
			b := n - a*45
			sb.WriteByte(base45Alphabet[a])
			sb.WriteByte(base45Alphabet[b])
			i++
		}
	}
	return sb.String()
}

// Base45Decode decodes a base45 string back to bytes.
func Base45Decode(s string) ([]byte, error) {
	if len(s)%3 == 1 {
		return nil, ErrInvalidBase45
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		rem := len(s) - i
		if rem >= 3 {
			a, b, c := base45Reverse[s[i]], base45Reverse[s[i+1]], base45Reverse[s[i+2]]
			if a < 0 || b < 0 || c < 0 {
				return nil, ErrInvalidBase45
			}
			n := int(a)*45*45 + int(b)*45 + int(c)
			if n > 0xFFFF {
				return nil, ErrInvalidBase45
			}
			out = append(out, byte(n>>8), byte(n))
			i += 3
		} else {
			a, b := base45Reverse[s[i]], base45Reverse[s[i+1]]
			if a < 0 || b < 0 {
				return nil, ErrInvalidBase45
			}
			n := int(a)*45 + int(b)
			if n > 0xFF {
				return nil, ErrInvalidBase45
			}
			out = append(out, byte(n))
			i += 2
		}
	}
	return out, nil
}
