package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type imageGeneratorStub struct {
	generated imagegen.Artifact
	err       error
	generate  imagegen.GenerateRequest
	edit      imagegen.EditRequest
}

func TestTenantImageCapabilitiesRedactsProviderCredentials(t *testing.T) {
	service := &fakeTenantService{tenantID: 7, userID: 11, sessions: []mysqlstore.Session{{ID: 13, Status: "active"}}}
	h := NewHandler(Options{AuthToken: "token", TenantService: service, ImageCatalog: []config.ResolvedImageModel{{Provider: "agnes", Model: "agnes-image-2.5-flash", ImageProtocol: "agnes-images", Capability: config.ImageModelCapability{Resolutions: []string{"1K", "2K"}, AspectRatios: []string{"16:9"}}}}}, nil)
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, imageRequest(t, http.MethodGet, "/tenant/images/capabilities", nil))
	if recorder.Code != http.StatusOK || bytes.Contains(recorder.Body.Bytes(), []byte("apiKey")) || bytes.Contains(recorder.Body.Bytes(), []byte("baseURL")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !bytes.Contains(recorder.Body.Bytes(), []byte("agnes-image-2.5-flash")) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func (s *imageGeneratorStub) Generate(_ context.Context, req imagegen.GenerateRequest) (imagegen.Artifact, error) {
	s.generate = req
	return s.generated, s.err
}
func (s *imageGeneratorStub) Edit(_ context.Context, req imagegen.EditRequest) (imagegen.Artifact, error) {
	s.edit = req
	return s.generated, s.err
}

type imageHistoryStub struct{ rows []imagegen.GenerationRecord }

func (s imageHistoryStub) ListImageGenerations(context.Context, uint64, uint64, uint64, int) ([]imagegen.GenerationRecord, error) {
	return s.rows, nil
}

func imageServerHandler(t *testing.T, gen imagegen.Generator, mediaStore media.Store, blobs imagegen.BlobStore, history ImageGenerationHistoryStore) http.Handler {
	t.Helper()
	service := &fakeTenantService{tenantID: 7, userID: 11, sessions: []mysqlstore.Session{{ID: 13, Status: "active"}}}
	return NewHandler(Options{AuthToken: "token", TenantService: service, ImageGenerator: gen, MediaAssetStore: mediaStore, ImageBlobStore: blobs, ImageGenerationStore: history}, nil)
}

func imageRequest(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer token")
	return req
}

func TestTenantImageGenerateRequiresAuthAndSessionOwnership(t *testing.T) {
	gen := &imageGeneratorStub{generated: imagegen.Artifact{AssetID: "a1", URL: "/tenant/media/assets/a1"}}
	h := imageServerHandler(t, gen, nil, nil, nil)
	unauth := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodPost, "/tenant/sessions/13/images/generations", bytes.NewBufferString(`{"prompt":"cat"}`))
	h.ServeHTTP(unauth, unauthReq)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauth.Code)
	}
	bad := httptest.NewRecorder()
	badReq := imageRequest(t, http.MethodPost, "/tenant/sessions/999/images/generations", bytes.NewBufferString(`{"prompt":"cat"}`))
	h.ServeHTTP(bad, badReq)
	if bad.Code != http.StatusNotFound {
		t.Fatalf("cross-session status = %d body=%s", bad.Code, bad.Body.String())
	}
}

func TestTenantImageGenerateAndEdit(t *testing.T) {
	gen := &imageGeneratorStub{generated: imagegen.Artifact{AssetID: "a1", GenerationID: "g1", URL: "/tenant/media/assets/a1", MediaType: "image/png"}}
	h := imageServerHandler(t, gen, nil, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, imageRequest(t, http.MethodPost, "/tenant/sessions/13/images/generations", bytes.NewBufferString(`{"prompt":"cat","model":"gpt-image-2","idempotency_key":"body-key"}`)))
	if rec.Code != http.StatusOK || gen.generate.TenantID != 7 || gen.generate.UserID != 11 || gen.generate.SessionID != 13 || gen.generate.IdempotencyKey != "body-key" {
		t.Fatalf("generate status=%d req=%+v body=%s", rec.Code, gen.generate, rec.Body.String())
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("prompt", "redraw")
	_ = mw.WriteField("idempotency_key", "edit-body-key")
	part, _ := mw.CreateFormFile("image", "source.png")
	_, _ = part.Write([]byte("source"))
	_ = mw.Close()
	req := imageRequest(t, http.MethodPost, "/tenant/sessions/13/images/edits", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gen.edit.SessionID != 13 || gen.edit.SourceName != "source.png" || gen.edit.IdempotencyKey != "edit-body-key" {
		t.Fatalf("edit status=%d req=%+v body=%s", rec.Code, gen.edit, rec.Body.String())
	}
}

func TestTenantImageEditRejectsInvalidMultipartAndMapsProviderErrors(t *testing.T) {
	gen := &imageGeneratorStub{err: imagegen.ErrProviderTimeout}
	h := imageServerHandler(t, gen, nil, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, imageRequest(t, http.MethodPost, "/tenant/sessions/13/images/edits", bytes.NewBufferString("not multipart")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid multipart status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("prompt", "redraw")
	part, _ := mw.CreateFormFile("image", "source.png")
	_, _ = part.Write([]byte("source"))
	contentType := mw.FormDataContentType()
	_ = mw.Close()
	req := imageRequest(t, http.MethodPost, "/tenant/sessions/13/images/edits", &body)
	req.Header.Set("Content-Type", contentType)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("provider timeout status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTenantImageHistoryAndAuthorizedAssetRead(t *testing.T) {
	mediaStore := media.NewMemoryStore()
	blobs := imagegen.NewMemoryBlobStore()
	policy := media.AccessPolicy{TenantID: 7, UserID: 11, SessionID: 13}
	key := "tenant-7/user-11/session-13/asset-a1.png"
	blob, err := blobs.Put(context.Background(), imagegen.PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Name: "a1.png", Data: bytes.NewReader([]byte("png"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := mediaStore.Put(context.Background(), media.Asset{AssetID: "a1", Kind: media.KindImage, MediaType: "image/png", Name: "a1.png", SizeBytes: blob.SizeBytes, SessionID: 13, TenantID: 7, UserID: 11, State: media.StateReady, Original: media.Variant{Path: key, MediaType: "image/png"}, Access: policy}); err != nil {
		t.Fatal(err)
	}
	history := imageHistoryStub{rows: []imagegen.GenerationRecord{
		{GenerationID: "failed", TenantID: 7, UserID: 11, SessionID: 13, Status: imagegen.GenerationStatusFailed, Operation: imagegen.OperationGenerate},
		{GenerationID: "g1", TenantID: 7, UserID: 11, SessionID: 13, AssetID: "a1", Status: imagegen.GenerationStatusCompleted, Operation: imagegen.OperationGenerate},
	}}
	h := imageServerHandler(t, &imageGeneratorStub{}, mediaStore, blobs, history)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, imageRequest(t, http.MethodGet, "/tenant/sessions/13/images", nil))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"asset_id":"a1"`)) {
		t.Fatalf("history status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, imageRequest(t, http.MethodGet, "/tenant/media/assets/a1", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("asset status=%d headers=%v body=%q", rec.Code, rec.Header(), rec.Body.String())
	}
	var decoded map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
}
