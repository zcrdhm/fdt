package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fdt/internal/feishu"
)

func TestWebAppRoutes(t *testing.T) {
	h := &Handler{Svc: &Service{}, JSAPI: feishu.MockJSAPI{}}
	srv := httptest.NewServer(h.Router())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/app/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), "数据摆渡审批") {
		t.Fatalf("GET /app/ status=%d body=%s", res.StatusCode, body)
	}

	res, err = http.Get(srv.URL + "/app/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	js, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(js), "/submit") {
		t.Fatalf("GET /app/app.js status=%d", res.StatusCode)
	}

	res, err = http.Get(srv.URL + "/jsapi/config?url=" + "https://example.com/app/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var cfg feishu.JSAPIConfig
	if err := json.NewDecoder(res.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Mock {
		t.Fatalf("expected mock jsapi: %+v", cfg)
	}
}
