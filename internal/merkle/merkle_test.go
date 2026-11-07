package merkle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func leavesFromStrings(ss ...string) [][]byte {
	out := make([][]byte, 0, len(ss))
	for _, s := range ss {
		h := sha256.Sum256([]byte(s))
		out = append(out, h[:])
	}
	return out
}

func TestRootStableAndOrderSensitive(t *testing.T) {
	l := leavesFromStrings("a", "b", "c", "d")
	r1, err := Root(l)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := Root(leavesFromStrings("a", "b", "c", "d"))
	if !bytes.Equal(r1, r2) {
		t.Fatal("root not deterministic")
	}
	r3, _ := Root(leavesFromStrings("a", "b", "c", "e"))
	if bytes.Equal(r1, r3) {
		t.Fatal("root should change when a leaf changes")
	}
	r4, _ := Root(leavesFromStrings("b", "a", "c", "d"))
	if bytes.Equal(r1, r4) {
		t.Fatal("root must be order-sensitive (canonical order matters)")
	}
}

func TestRootFixedSizeForAnyCount(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 17, 1000} {
		l := make([][]byte, n)
		for i := 0; i < n; i++ {
			h := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
			l[i] = h[:]
		}
		r, err := Root(l)
		if err != nil {
			t.Fatal(err)
		}
		if len(r) != 32 {
			t.Fatalf("root size %d != 32 for n=%d", len(r), n)
		}
	}
}

func TestOddLeafPromotion(t *testing.T) {
	l := leavesFromStrings("a", "b", "c")
	if _, err := Root(l); err != nil {
		t.Fatal(err)
	}
}

func TestProofVerify(t *testing.T) {
	l := leavesFromStrings("a", "b", "c", "d", "e")
	root, _ := Root(l)
	for i := range l {
		proof, err := Prove(l, i)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyProof(l[i], root, i, proof); err != nil {
			t.Fatalf("proof %d failed: %v", i, err)
		}
	}
}

func TestRootHex(t *testing.T) {
	l := leavesFromStrings("x")
	hx, err := RootHex(l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hex.DecodeString(hx); err != nil || len(hx) != 64 {
		t.Fatalf("bad hex root: %q", hx)
	}
}
