package feishu

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestJSAPISignatureFormula(t *testing.T) {
	// Documented Feishu example values from the H5 JSAPI guide.
	ticket := "617bf955832a4d4d80d9d8d85917a427"
	nonce := "Y7a8KkqX041bsSwT"
	ts := int64(1510045655000)
	url := "https://example.cn/test/1234/content.html"
	verify := fmt.Sprintf("jsapi_ticket=%s&noncestr=%s&timestamp=%d&url=%s", ticket, nonce, ts, url)
	sum := sha1.Sum([]byte(verify))
	got := hex.EncodeToString(sum[:])
	want := "40a68999ecf7e05907edba43b31a50fd1830c777"
	if got != want {
		t.Fatalf("signature=%s want %s\nverifyStr=%s", got, want, verify)
	}
}

func TestMockJSAPI(t *testing.T) {
	cfg, err := (MockJSAPI{}).SignJSAPI(nil, "https://x/app/")
	if err != nil || !cfg.Mock {
		t.Fatalf("mock jsapi: %+v %v", cfg, err)
	}
}
