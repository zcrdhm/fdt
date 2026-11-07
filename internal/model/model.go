// Package model holds the shared data structures crossing module boundaries:
// request tokens (QR#1), release tokens (QR#2), file manifests and Merkle
// summaries. Tokens are signed as canonical CBOR (RFC 8949 core deterministic
// encoding); json tags remain for the intranet API and the gateway session store.
package model

// Direction distinguishes export (intranet -> USB) from import (USB -> intranet).
type Direction string

const (
	DirExport Direction = "export"
	DirImport Direction = "import"
)

// Classification is the data sensitivity level driving approval routing.
type Classification string

const (
	ClassInternal     Classification = "internal"
	ClassConfidential Classification = "confidential"
	ClassSecret       Classification = "secret"
	ClassTopSecret    Classification = "topsecret"
)

// ManifestMode records whether the QR#1 token carried a full per-file list or a
// degraded summary (single-frame budget exceeded). See technical design §5.2.
type ManifestMode string

const (
	ModeFull    ManifestMode = "full"
	ModeSummary ManifestMode = "summary"
)

// Applicant is the person requesting the ferry.
type Applicant struct {
	ID   string `json:"id" cbor:"id"`
	Name string `json:"name" cbor:"name"`
	Dept string `json:"dept" cbor:"dept"`
}

// FileEntry is one file in the manifest. HashHex is truncated to 128 bits in
// the QR payload to save space; the full 256-bit hash is kept in the store.
type FileEntry struct {
	Path      string `json:"p" cbor:"p"`
	Size      int64  `json:"s" cbor:"s"`
	Type      string `json:"t" cbor:"t"`
	HashHex   string `json:"h" cbor:"h"`
	InArchive string `json:"a,omitempty" cbor:"a,omitempty"`
	Leaf      int    `json:"l,omitempty" cbor:"l,omitempty"`
}

// TreeNode is an aggregated directory in summary mode.
type TreeNode struct {
	Name        string      `json:"n" cbor:"n"`
	Count       int         `json:"c" cbor:"c"`
	Size        int64       `json:"s" cbor:"s"`
	MerkleChild string      `json:"mr" cbor:"mr"`
	Children    []*TreeNode `json:"ch,omitempty" cbor:"ch,omitempty"`
}

// DLPSummary is the internal DLP/AV classification result attached to the token.
type DLPSummary struct {
	Categories map[string]int `json:"categories,omitempty" cbor:"categories,omitempty"`
	AV         string         `json:"av" cbor:"av"` // clean | infected | not_scanned | pending | n/a
	Hits       []string       `json:"hits,omitempty" cbor:"hits,omitempty"`
}

// Dataset is the compact description of the ferried data that crosses the gap.
// The Merkle root binds the exact byte set regardless of file count.
type Dataset struct {
	Count      int          `json:"count" cbor:"count"`
	TotalSize  int64        `json:"totalSize" cbor:"totalSize"`
	MerkleRoot string       `json:"merkleRoot" cbor:"merkleRoot"` // 64-hex, fixed size
	Mode       ManifestMode `json:"mode" cbor:"mode"`
	DLP        DLPSummary   `json:"dlp" cbor:"dlp"`
	Entries    []FileEntry  `json:"entries,omitempty" cbor:"entries,omitempty"` // full mode only
	Tree       []*TreeNode  `json:"tree,omitempty" cbor:"tree,omitempty"`       // summary mode
}

// RequestToken is the QR#1 payload, signed with SK_内网.
type RequestToken struct {
	V         int            `json:"v" cbor:"v"`
	KID       string         `json:"kid" cbor:"kid"`
	Tenant    string         `json:"tenant" cbor:"tenant"`
	ReqID     string         `json:"reqId" cbor:"reqId"`
	Dir       Direction      `json:"dir" cbor:"dir"`
	Applicant Applicant      `json:"applicant" cbor:"applicant"`
	Reason    string            `json:"reason" cbor:"reason"`
	Cls       Classification    `json:"cls" cbor:"cls"`
	Dataset   Dataset           `json:"dataset" cbor:"dataset"`
	Extras    map[string]string `json:"extras,omitempty" cbor:"extras,omitempty"` // custom form → Feishu fields
	Nonce     string            `json:"nonce" cbor:"nonce"`
	Iat       int64             `json:"iat" cbor:"iat"`
	Exp       int64             `json:"exp" cbor:"exp"`
}

// ReleaseToken is the QR#2 payload, signed with SK_网关 after Feishu approval.
type ReleaseToken struct {
	V            int       `json:"v" cbor:"v"`
	KID          string    `json:"kid" cbor:"kid"`
	Tenant       string    `json:"tenant" cbor:"tenant"`
	ReqID        string    `json:"reqId" cbor:"reqId"`
	Dir          Direction `json:"dir" cbor:"dir"`
	MerkleRoot   string    `json:"merkleRoot" cbor:"merkleRoot"`
	FsApprovalID string    `json:"fsApprovalId" cbor:"fsApprovalId"`
	Approver     Applicant `json:"approver" cbor:"approver"`
	Nonce        string    `json:"nonce" cbor:"nonce"`
	Iat          int64     `json:"iat" cbor:"iat"`
	Exp          int64     `json:"exp" cbor:"exp"`
}
