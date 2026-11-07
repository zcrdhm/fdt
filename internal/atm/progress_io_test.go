package atm

import (
	"bytes"
	"io"
	"testing"
)

func TestProgressReaderEmits(t *testing.T) {
	src := bytes.Repeat([]byte("x"), 600*1024)
	var last WriteProgress
	n := 0
	r := newProgressReader(bytes.NewReader(src), int64(len(src)), "uploading", func(p WriteProgress) {
		n++
		last = p
	})
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(src) {
		t.Fatalf("read %d", len(got))
	}
	if n == 0 {
		t.Fatal("expected progress emits")
	}
	if last.Phase != "uploading" || last.Total != int64(len(src)) {
		t.Fatalf("last = %#v", last)
	}
	if last.Written < int64(len(src)) {
		t.Fatalf("written %d < %d", last.Written, len(src))
	}
}
