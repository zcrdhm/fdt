package atm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"fdt/internal/model"
)

// Manifest is the datasvc manifest response: the expected Merkle root and the
// physical file list the ATM will write.
type Manifest struct {
	ReqID      string            `json:"reqId"`
	MerkleRoot string            `json:"merkleRoot"`
	Count      int               `json:"count"`
	Files      []model.FileEntry `json:"files"`
}

// DataFetcher pulls approved data from the intranet data service.
type DataFetcher interface {
	FetchManifest(ctx context.Context, reqID string) (*Manifest, error)
	FetchTar(ctx context.Context, reqID string) (io.ReadCloser, error)
}

// HTTPFetcher is the real fetcher: it authenticates with the ATM's device
// credential (prod: mTLS client cert) and streams a tar of the staged data.
type HTTPFetcher struct {
	BaseURL  string
	DeviceID string
	Secret   string // dev bearer = device cert fingerprint; prod uses mTLS
	Client   *http.Client
}

func (f *HTTPFetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 0} // streaming: no global timeout
}

func (f *HTTPFetcher) newReq(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Device-ID", f.DeviceID)
	req.Header.Set("Authorization", "Bearer "+f.Secret)
	return req, nil
}

func (f *HTTPFetcher) FetchManifest(ctx context.Context, reqID string) (*Manifest, error) {
	req, err := f.newReq(ctx, http.MethodGet, fmt.Sprintf("%s/requests/%s/manifest", f.BaseURL, reqID))
	if err != nil {
		return nil, err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("atm: manifest fetch %d: %s", resp.StatusCode, string(body))
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (f *HTTPFetcher) FetchTar(ctx context.Context, reqID string) (io.ReadCloser, error) {
	req, err := f.newReq(ctx, http.MethodGet, fmt.Sprintf("%s/requests/%s/stream", f.BaseURL, reqID))
	if err != nil {
		return nil, err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("atm: stream fetch %d: %s", resp.StatusCode, string(body))
	}
	return resp.Body, nil
}

var _ DataFetcher = (*HTTPFetcher)(nil)
