package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const testHost = "127.0.0.1:7391"
const testCollection = "/api/super/v1/work-orders"
const testInput = `{"title":"Title","objective":"Objective","acceptance_criteria":["Criterion"],"workspace":"/metadata"}`

func testHandler(t *testing.T, logger *slog.Logger) (http.Handler, *workorder.Service) {
	t.Helper()
	store, err := sqlitestore.Open(context.Background(), filepath.Join(t.TempDir(), "private", "work-orders.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	service := workorder.NewService(store, logger)
	return NewHandler(Config{Service: service, AllowedHost: testHost, Logger: logger}), service
}
func request(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, "http://"+testHost+path, strings.NewReader(body))
	r.Header.Set("X-Super-Niuma-Request", "1")
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "test-key")
	}
	return r
}
func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func decodeOrder(t *testing.T, w *httptest.ResponseRecorder) workorder.WorkOrder {
	t.Helper()
	var order workorder.WorkOrder
	if err := json.Unmarshal(w.Body.Bytes(), &order); err != nil {
		t.Fatal(err)
	}
	return order
}
func requireError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != code || body.Error.Message == "" {
		t.Fatalf("error contract: %s", w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("permissive CORS header")
	}
}
func TestIntakeHTTPContract(t *testing.T) {
	h, _ := testHandler(t, nil)
	w := serve(h, request(http.MethodGet, "/api/super/v1/status", ""))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"name":"super-niuma","mode":"intake_only","execution_enabled":false,"llm_routing_enabled":false}` {
		t.Fatalf("status: %s", w.Body.String())
	}
	w = serve(h, request(http.MethodGet, testCollection, ""))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"items":[]}` {
		t.Fatalf("empty list: %s", w.Body.String())
	}
	w = serve(h, request(http.MethodPost, testCollection, testInput))
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	order := decodeOrder(t, w)
	if order.ID == "" || order.Status != workorder.StatusRecorded || order.ExecutionStatus != workorder.NotStarted || order.VerificationStatus != workorder.NotStarted || order.DeliveryStatus != workorder.NotStarted {
		t.Fatalf("unsafe aggregate: %#v", order)
	}
	w = serve(h, request(http.MethodGet, testCollection+"/"+order.ID, ""))
	if w.Code != http.StatusOK || decodeOrder(t, w).ID != order.ID {
		t.Fatal("get")
	}
	w = serve(h, request(http.MethodPost, testCollection, testInput))
	if w.Code != http.StatusOK || decodeOrder(t, w).ID != order.ID {
		t.Fatal("retry")
	}
	w = serve(h, request(http.MethodPost, testCollection, strings.Replace(testInput, "Title", "Changed", 1)))
	requireError(t, w, http.StatusConflict, "idempotency_conflict")
	cancelPath := testCollection + "/" + order.ID + "/cancel"
	w = serve(h, request(http.MethodPost, cancelPath, `{}`))
	if w.Code != http.StatusOK || decodeOrder(t, w).Status != workorder.StatusCancelled {
		t.Fatalf("cancel: %s", w.Body.String())
	}
	cancelled := w.Body.String()
	w = serve(h, request(http.MethodPost, cancelPath, `{}`))
	if w.Code != http.StatusOK || w.Body.String() != cancelled {
		t.Fatal("repeat cancel changed response")
	}
	w = serve(h, request(http.MethodPost, testCollection, testInput))
	if w.Code != http.StatusOK || w.Body.String() != cancelled {
		t.Fatal("retry resurrected cancelled order")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing response safety headers")
	}
}
func TestHostOriginAndMarker(t *testing.T) {
	h, _ := testHandler(t, nil)
	tests := []struct {
		name   string
		change func(*http.Request)
	}{
		{"missing marker", func(r *http.Request) { r.Header.Del("X-Super-Niuma-Request") }},
		{"wrong marker", func(r *http.Request) { r.Header.Set("X-Super-Niuma-Request", "2") }},
		{"duplicate marker", func(r *http.Request) { r.Header.Add("X-Super-Niuma-Request", "1") }},
		{"host attacker", func(r *http.Request) { r.Host = "evil.example:7391" }},
		{"localhost alias", func(r *http.Request) { r.Host = "localhost:7391" }},
		{"wrong port", func(r *http.Request) { r.Host = "127.0.0.1:7392" }},
		{"alternate loopback", func(r *http.Request) { r.Host = "127.0.0.2:7391" }},
		{"forwarded spoof", func(r *http.Request) { r.Host = "evil.example"; r.Header.Set("X-Forwarded-Host", testHost) }},
		{"foreign origin", func(r *http.Request) { r.Header.Set("Origin", "http://evil.example") }},
		{"same site other port", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:7392") }},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Origin", "https://"+testHost) }},
		{"null origin", func(r *http.Request) { r.Header.Set("Origin", "null") }},
		{"empty origin", func(r *http.Request) { r.Header["Origin"] = []string{""} }},
		{"origin slash", func(r *http.Request) { r.Header.Set("Origin", "http://"+testHost+"/") }},
		{"origin userinfo", func(r *http.Request) { r.Header.Set("Origin", "http://evil@"+testHost) }},
		{"duplicate origin", func(r *http.Request) { r.Header["Origin"] = []string{"http://" + testHost, "http://" + testHost} }},
		{"cross site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"same site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "same-site") }},
		{"duplicate fetch metadata", func(r *http.Request) { r.Header["Sec-Fetch-Site"] = []string{"same-origin", "cross-site"} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := request(http.MethodGet, "/api/super/v1/status", "")
			tt.change(r)
			requireError(t, serve(h, r), http.StatusForbidden, "forbidden")
		})
	}
	for _, fetch := range []string{"", "none", "same-origin"} {
		r := request(http.MethodGet, "/api/super/v1/status", "")
		r.Header.Set("Origin", "http://"+testHost)
		if fetch != "" {
			r.Header.Set("Sec-Fetch-Site", fetch)
		}
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Fatalf("same origin rejected: %d", w.Code)
		}
	}
}
func TestInvalidConfigurationFailsClosed(t *testing.T) {
	_, service := testHandler(t, nil)
	for _, host := range []string{"", "localhost:7391", "0.0.0.0:7391", "[::]:7391", "192.168.1.1:7391", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:99999"} {
		h := NewHandler(Config{Service: service, AllowedHost: host})
		requireError(t, serve(h, request(http.MethodGet, "/api/super/v1/status", "")), http.StatusServiceUnavailable, "service_unavailable")
	}
	h := NewHandler(Config{AllowedHost: testHost})
	requireError(t, serve(h, request(http.MethodGet, "/api/super/v1/status", "")), http.StatusServiceUnavailable, "service_unavailable")
	h = NewHandler(Config{Service: service, AllowedHost: "[::1]:7391"})
	r := request(http.MethodGet, "/api/super/v1/status", "")
	r.Host = "[::1]:7391"
	r.URL.Host = r.Host
	r.Header.Set("Origin", "http://[::1]:7391")
	if w := serve(h, r); w.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback rejected: %d", w.Code)
	}
}
func TestStrictJSONAndFields(t *testing.T) {
	h, service := testHandler(t, nil)
	for _, body := range []string{
		"", "null", "[]", `"text"`, `{`, `{}`, testInput + ` {}`,
		strings.TrimSuffix(testInput, "}") + `,"unknown":true}`,
		strings.TrimSuffix(testInput, "}") + `,"title":"Other"}`,
		strings.Replace(testInput, `"title"`, `"Title"`, 1),
		strings.Replace(testInput, `"Title"`, `null`, 1),
		strings.Replace(testInput, `["Criterion"]`, `null`, 1),
		strings.Replace(testInput, `["Criterion"]`, `[null]`, 1),
		strings.Replace(testInput, `"/metadata"`, `null`, 1),
		strings.Replace(testInput, `"Title"`, `12`, 1),
		strings.Replace(testInput, `"Title"`, `""`, 1),
		strings.Replace(testInput, `"/metadata"`, `"relative"`, 1),
		strings.Replace(testInput, `"Title"`, "\"bad"+string([]byte{0xff})+"\"", 1),
		strings.Replace(testInput, `"Title"`, `"`+strings.Repeat("a", workorder.MaxTitleBytes+1)+`"`, 1),
	} {
		w := serve(h, request(http.MethodPost, testCollection, body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid JSON/field accepted: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	items, err := service.List(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatal("invalid request persisted")
	}
	for _, key := range []string{"", strings.Repeat("a", workorder.MaxIdempotencyKeyBytes+1), "bad key"} {
		r := request(http.MethodPost, testCollection, testInput)
		r.Header.Set("Idempotency-Key", key)
		requireError(t, serve(h, r), http.StatusBadRequest, "invalid_input")
	}
	r := request(http.MethodPost, testCollection, testInput)
	r.Header.Add("Idempotency-Key", "other")
	requireError(t, serve(h, r), http.StatusBadRequest, "invalid_input")
	r = request(http.MethodPost, testCollection, testInput)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if w := serve(h, r); w.Code != http.StatusCreated {
		t.Fatalf("utf8 JSON rejected: %s", w.Body.String())
	}
}
func TestBodyAndMediaBounds(t *testing.T) {
	h, _ := testHandler(t, nil)
	for _, contentType := range []string{"", "text/plain", "application/jsonp", "application/json; charset=utf-16", "application/json, text/plain"} {
		r := request(http.MethodPost, testCollection, testInput)
		r.Header.Set("Content-Type", contentType)
		requireError(t, serve(h, r), http.StatusUnsupportedMediaType, "unsupported_media_type")
	}
	r := request(http.MethodPost, testCollection, testInput)
	r.Header.Add("Content-Type", "application/json")
	requireError(t, serve(h, r), http.StatusUnsupportedMediaType, "unsupported_media_type")
	r = request(http.MethodPost, testCollection, testInput)
	r.Header.Set("Content-Encoding", "gzip")
	requireError(t, serve(h, r), http.StatusUnsupportedMediaType, "unsupported_media_type")
	for _, length := range []int64{-1, MaxBodyBytes + 1} {
		r := request(http.MethodPost, testCollection, strings.Repeat(" ", int(MaxBodyBytes)+1))
		r.ContentLength = length
		requireError(t, serve(h, r), http.StatusRequestEntityTooLarge, "body_too_large")
	}
	r = request(http.MethodGet, "/api/super/v1/status", `{}`)
	requireError(t, serve(h, r), http.StatusBadRequest, "invalid_request")
}
func TestExactPathsAndMethods(t *testing.T) {
	h, _ := testHandler(t, nil)
	for _, path := range []string{"/", "/api/super/v1", "/api/super/v1/status/", testCollection + "/", testCollection + "//", testCollection + "/../status", testCollection + "/%2e%2e/status", testCollection + "/bad%2fid", testCollection + "/bad/extra", testCollection + "/bad/cancel/", testCollection + "x"} {
		requireError(t, serve(h, request(http.MethodGet, path, "")), http.StatusNotFound, "not_found")
	}
	for _, method := range []string{http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodPatch} {
		w := serve(h, request(method, testCollection, ""))
		requireError(t, w, http.StatusMethodNotAllowed, "method_not_allowed")
		if w.Header().Get("Allow") != "GET, POST" {
			t.Fatal("missing Allow")
		}
	}
	requireError(t, serve(h, request(http.MethodPost, "/api/super/v1/status", `{}`)), http.StatusMethodNotAllowed, "method_not_allowed")
	requireError(t, serve(h, request(http.MethodGet, testCollection+"/00000000-0000-0000-0000-000000000000/cancel", "")), http.StatusMethodNotAllowed, "method_not_allowed")
	requireError(t, serve(h, request(http.MethodPost, testCollection+"/00000000-0000-0000-0000-000000000000/cancel", `{}`)), http.StatusNotFound, "not_found")
	requireError(t, serve(h, request(http.MethodGet, testCollection+"/not-an-id", "")), http.StatusNotFound, "not_found")
	requireError(t, serve(h, request(http.MethodGet, testCollection+"?limit=100000", "")), http.StatusBadRequest, "invalid_request")
}
func TestCancellationBodyMustBeEmptyObject(t *testing.T) {
	h, service := testHandler(t, nil)
	order, _, err := service.Create(context.Background(), "create", workorder.CreateInput{Title: "Title", Objective: "Objective", AcceptanceCriteria: []string{"Criterion"}})
	if err != nil {
		t.Fatal(err)
	}
	path := testCollection + "/" + order.ID + "/cancel"
	for _, body := range []string{"", "null", "[]", `{"reason":"secret"}`, `{} {}`} {
		requireError(t, serve(h, request(http.MethodPost, path, body)), http.StatusBadRequest, "invalid_request")
	}
	loaded, err := service.Get(context.Background(), order.ID)
	if err != nil || loaded.Status != workorder.StatusRecorded {
		t.Fatal("invalid cancel mutated order")
	}
}
func TestAPIRealLoopbackServer(t *testing.T) {
	_, service := testHandler(t, nil)
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = NewHandler(Config{Service: service, AllowedHost: server.Listener.Addr().String()})
	server.Start()
	defer server.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL+testCollection, strings.NewReader(testInput))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Super-Niuma-Request", "1")
	req.Header.Set("Idempotency-Key", "socket-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", server.URL)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusCreated {
		t.Fatalf("real HTTP: %d %s %v", response.StatusCode, body, err)
	}
}
func TestHTTPLogsRedacted(t *testing.T) {
	var log bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h, _ := testHandler(t, logger)
	r := request(http.MethodPost, testCollection, strings.ReplaceAll(testInput, "Title", "SECRET_TITLE"))
	r.Header.Set("Idempotency-Key", "SECRET_KEY")
	serve(h, r)
	r = request(http.MethodGet, "/SECRET_PATH", "")
	r.Host = "SECRET_HOST"
	r.Header.Set("Origin", "http://SECRET_ORIGIN")
	serve(h, r)
	if strings.Contains(log.String(), "SECRET") {
		t.Fatalf("leaked request information: %s", log.String())
	}
}

type blockingRepository struct {
	entered chan struct{}
	release chan struct{}
}

func (r *blockingRepository) Create(context.Context, string, workorder.WorkOrder) (workorder.WorkOrder, bool, error) {
	panic("unused")
}
func (r *blockingRepository) Get(context.Context, string) (workorder.WorkOrder, error) {
	panic("unused")
}
func (r *blockingRepository) Cancel(context.Context, string, time.Time) (workorder.WorkOrder, error) {
	panic("unused")
}
func (r *blockingRepository) List(ctx context.Context, _ int) ([]workorder.WorkOrder, error) {
	r.entered <- struct{}{}
	select {
	case <-r.release:
		return []workorder.WorkOrder{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestHTTPConcurrencyIsBounded(t *testing.T) {
	repo := &blockingRepository{entered: make(chan struct{}, MaxConcurrentRequests), release: make(chan struct{})}
	h := NewHandler(Config{Service: workorder.NewService(repo, nil), AllowedHost: testHost})
	var wg sync.WaitGroup
	for i := 0; i < MaxConcurrentRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := serve(h, request(http.MethodGet, testCollection, ""))
			if w.Code != http.StatusOK {
				t.Errorf("admitted request: %d", w.Code)
			}
		}()
	}
	for i := 0; i < MaxConcurrentRequests; i++ {
		select {
		case <-repo.entered:
		case <-time.After(5 * time.Second):
			close(repo.release)
			wg.Wait()
			t.Fatal("admission timeout")
		}
	}
	requireError(t, serve(h, request(http.MethodGet, testCollection, "")), http.StatusServiceUnavailable, "service_unavailable")
	close(repo.release)
	wg.Wait()
	if w := serve(h, request(http.MethodGet, testCollection, "")); w.Code != http.StatusOK {
		t.Fatal("admission slot leaked")
	}
}
