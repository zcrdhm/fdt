// Package merkle builds a SHA-256 Merkle tree over per-file hashes. The root is
// a fixed 32 bytes regardless of file count, which is what lets a single-frame
// QR token cryptographically bind an arbitrarily large dataset.
package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

var (
	ErrEmptyLeaves  = errors.New("merkle: no leaves")
	ErrBadProof     = errors.New("merkle: invalid inclusion proof")
	ErrLeafMismatch = errors.New("merkle: leaf hash size must be 32 bytes")
)

// HashFileContent returns the SHA-256 of raw content (a Merkle leaf preimage).
func HashFileContent(content []byte) [32]byte {
	return sha256.Sum256(content)
}

// pairHash computes SHA256(left || right).
func pairHash(left, right []byte) []byte {
	h := sha256.New()
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// Root computes the Merkle root over leafHashes (each 32 bytes). Leaves must be
// supplied in canonical order (the manifest pipeline sorts by path). An odd node
// at any level is promoted by hashing it with itself. Zero leaves => 32 zero bytes.
func Root(leafHashes [][]byte) ([]byte, error) {
	for _, l := range leafHashes {
		if len(l) != sha256.Size {
			return nil, ErrLeafMismatch
		}
	}
	if len(leafHashes) == 0 {
		return make([]byte, sha256.Size), nil
	}
	level := make([][]byte, len(leafHashes))
	copy(level, leafHashes)
	for len(level) > 1 {
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, pairHash(level[i], level[i+1]))
			} else {
				next = append(next, pairHash(level[i], level[i]))
			}
		}
		level = next
	}
	return level[0], nil
}

// RootHex computes the root and returns it as lower-case hex.
func RootHex(leafHashes [][]byte) (string, error) {
	r, err := Root(leafHashes)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(r), nil
}

// ProofStep is one sibling in an inclusion proof.
type ProofStep struct {
	Hash  []byte `json:"h"`
	Right bool   `json:"r"` // true if sibling is on the right
}

// Prove returns an inclusion proof for the leaf at index.
func Prove(leafHashes [][]byte, index int) ([]ProofStep, error) {
	if index < 0 || index >= len(leafHashes) {
		return nil, ErrEmptyLeaves
	}
	if len(leafHashes) == 1 {
		return nil, nil
	}
	level := make([][]byte, len(leafHashes))
	copy(level, leafHashes)
	var proof []ProofStep
	idx := index
	for len(level) > 1 {
		var sibling []byte
		right := false
		if idx%2 == 0 {
			if idx+1 < len(level) {
				sibling = level[idx+1]
				right = true
			} else {
				sibling = level[idx]
				right = true
			}
		} else {
			sibling = level[idx-1]
			right = false
		}
		proof = append(proof, ProofStep{Hash: sibling, Right: right})
		next := make([][]byte, 0, (len(level)+1)/2)
		for i := 0; i < len(level); i += 2 {
			if i+1 < len(level) {
				next = append(next, pairHash(level[i], level[i+1]))
			} else {
				next = append(next, pairHash(level[i], level[i]))
			}
		}
		level = next
		idx /= 2
	}
	return proof, nil
}

// VerifyProof checks an inclusion proof against a known root.
func VerifyProof(leafHash, root []byte, index int, proof []ProofStep) error {
	cur := leafHash
	idx := index
	for _, step := range proof {
		if step.Right {
			cur = pairHash(cur, step.Hash)
		} else {
			cur = pairHash(step.Hash, cur)
		}
		idx /= 2
	}
	if hex.EncodeToString(cur) != hex.EncodeToString(root) {
		return ErrBadProof
	}
	return nil
}
