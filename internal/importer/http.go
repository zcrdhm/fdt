package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"fdt/internal/deviceauth"
	"fdt/internal/store"
)

// Handler exposes the importer over HTTP on the intranet, guarded by the same
// device-credential auth as the data service (prod: mTLS client cert).
type Handler struct {
	Svc *Service
	// MaxUploadBytes bounds an import upload; 0 => 8 GiB.
	MaxUploadBytes int64
}

func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("POST /imports", h.auth(h.createImport))
	mux.Handle("POST /imports/{id}/stage", h.auth(h.stageContent))
	mux.Handle("POST /imports/{id}/release", h.auth(h.releaseStaged))
	mux.Handle("POST /imports/{id}/content", h.auth(h.uploadContent)) // legacy: stage+release
	return mux
}

func (h *Handler) auth(next func(http.ResponseWriter, *http.Request, *store.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, err := deviceauth.Authenticate(r, h.Svc.Store)
		if err != nil {
			writeErrJSON(w, http.StatusUnauthorized, errors.New("importer: device not authenticated"))
			return
		}
		next(w, r, dev)
	}
}

func (h *Handler) createImport(w http.ResponseWriter, r *http.Request, _ *store.Device) {
	var in ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErrJSON(w, http.StatusBadRequest, err)
		return
	}
	res, err := h.Svc.CreateImportRequest(r.Context(), in)
	if err != nil {
		writeErrJSON(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) stageContent(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	id := r.PathValue("id")
	limit := h.MaxUploadBytes
	if limit <= 0 {
		limit = 8 << 30
	}
	body := io.LimitReader(r.Body, limit)
	res, err := h.Svc.StageContent(r.Context(), id, body, dev.ID)
	if err != nil {
		writeErrJSON(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) releaseStaged(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	id := r.PathValue("id")
	frame := r.Header.Get("X-Release-Token")
	if frame == "" {
		var body struct {
			Frame string `json:"frame"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		frame = body.Frame
	}
	if frame == "" {
		writeErrJSON(w, http.StatusBadRequest, errors.New("importer: missing QR#2 frame"))
		return
	}
	res, err := h.Svc.ReleaseStaged(r.Context(), id, frame, dev.ID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, ErrBlocked):
		writeJSON(w, http.StatusUnprocessableEntity, res)
	default:
		writeErrJSON(w, http.StatusBadRequest, err)
	}
}

func (h *Handler) uploadContent(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	id := r.PathValue("id")
	frame := r.Header.Get("X-Release-Token")
	if frame == "" {
		writeErrJSON(w, http.StatusBadRequest, errors.New("importer: missing X-Release-Token (QR#2)"))
		return
	}
	limit := h.MaxUploadBytes
	if limit <= 0 {
		limit = 8 << 30
	}
	body := io.LimitReader(r.Body, limit)
	res, err := h.Svc.ReceiveAndRelease(r.Context(), id, frame, body, dev.ID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, ErrBlocked):
		writeJSON(w, http.StatusUnprocessableEntity, res) // 422 with findings
	default:
		writeErrJSON(w, http.StatusBadRequest, err)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErrJSON(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// Client is the ATM-facing importer client.
type Client interface {
	CreateImport(ctx context.Context, in ImportRequest) (*ImportCreateResult, error)
	// StageContent uploads USB bytes to quarantine (no QR#2).
	StageContent(ctx context.Context, reqID string, tarBody io.Reader) (*ImportResult, error)
	// ReleaseImport authorizes release of already-staged content with QR#2.
	ReleaseImport(ctx context.Context, reqID, qr2Frame string) (*ImportResult, error)
	// UploadAndRelease is legacy one-shot (stage+release). Prefer Stage then Release.
	UploadAndRelease(ctx context.Context, reqID, qr2Frame string, tarBody io.Reader) (*ImportResult, error)
}

// HTTPClient talks to the importer over the intranet with device credentials.
type HTTPClient struct {
	BaseURL  string
	DeviceID string
	Secret   string
	HTTP     *http.Client
}

func (c *HTTPClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *HTTPClient) CreateImport(ctx context.Context, in ImportRequest) (*ImportCreateResult, error) {
	buf, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/imports", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", c.DeviceID)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("importer: create %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	var out ImportCreateResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) StageContent(ctx context.Context, reqID string, tarBody io.Reader) (*ImportResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/imports/"+reqID+"/stage", tarBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	req.Header.Set("X-Device-ID", c.DeviceID)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("importer: stage %d: %s", resp.StatusCode, readBody(resp.Body))
	}
	var out ImportResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) ReleaseImport(ctx context.Context, reqID, qr2Frame string) (*ImportResult, error) {
	body, _ := json.Marshal(map[string]string{"frame": qr2Frame})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/imports/"+reqID+"/release", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-ID", c.DeviceID)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("X-Release-Token", qr2Frame)
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var out ImportResult
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, err
		}
		return &out, nil
	case http.StatusUnprocessableEntity:
		var out ImportResult
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return &out, ErrBlocked
	default:
		return nil, fmt.Errorf("importer: release %d: %s", resp.StatusCode, readBody(resp.Body))
	}
}

func (c *HTTPClient) UploadAndRelease(ctx context.Context, reqID, qr2Frame string, tarBody io.Reader) (*ImportResult, error) {
	if _, err := c.StageContent(ctx, reqID, tarBody); err != nil {
		return nil, err
	}
	return c.ReleaseImport(ctx, reqID, qr2Frame)
}

func readBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return string(b)
}

var _ Client = (*HTTPClient)(nil)
