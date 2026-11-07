package codec

import "github.com/fxamacker/cbor/v2"

// canon is RFC 8949 core deterministic CBOR: sorted map keys, definite
// lengths, no floating-point ambiguity. Token signatures cover these bytes,
// so two encoders of the same value produce the same signature input.
var canon cbor.EncMode
var decod cbor.DecMode

func init() {
	opts := cbor.CoreDetEncOptions()
	opts.Time = cbor.TimeUnix
	var err error
	canon, err = opts.EncMode()
	if err != nil {
		panic("codec: cbor enc mode: " + err.Error())
	}
	decod, err = cbor.DecOptions{}.DecMode()
	if err != nil {
		panic("codec: cbor dec mode: " + err.Error())
	}
}

// MarshalCanonical encodes v as core-deterministic CBOR.
func MarshalCanonical(v any) ([]byte, error) { return canon.Marshal(v) }

// UnmarshalCanonical decodes a single CBOR data item into v.
func UnmarshalCanonical(b []byte, v any) error { return decod.Unmarshal(b, v) }
