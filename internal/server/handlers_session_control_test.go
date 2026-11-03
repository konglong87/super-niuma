package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

type sessionControlServiceFake struct {
	createRequest  sessioncontrol.CreateRequest
	listRequest    sessioncontrol.ListRequest
	getRequest     sessioncontrol.GetRequest
	sendRequest    sessioncontrol.SendRequest
	stopRequest    sessioncontrol.StopRequest
	attachRequest  sessioncontrol.AttachRequest
	monitorRequest sessioncontrol.MonitorRequest
	result         sessioncontrol.OperationResult
	snapshot       sessioncontrol.SessionSnapshot
	getSnapshots   []sessioncontrol.SessionSnapshot
	getCalls       int
	items          []sessioncontrol.SessionSnapshot
	err            error
	calls          []string
}

func TestNewSessionControlSessionDTOIncludesChannel(t *testing.T) {
	item := sessioncontrol.SessionSnapshot{
		ID:      41,
		Ref:     sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "feishu-session"},
		Title:   "Feishu conversation",
		Channel: &sessioncontrol.SessionChannel{Provider: "feishu", AccountKey: "feishu-primary"},
	}

	payload, err := json.Marshal(newSessionControlSessionDTO(item))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"channel":{"provider":"feishu","account_key":"feishu-primary"}`) {
		t.Fatalf("channel missing from session DTO: %s", payload)
	}
}

func (f *sessionControlServiceFake) Create(_ context.Context, request sessioncontrol.CreateRequest) (sessioncontrol.OperationResult, error) {
	f.calls = append(f.calls, "create")
	f.createRequest = request
	return f.result, f.err
}

func (f *sessionControlServiceFake) List(_ context.Context, request sessioncontrol.ListRequest) ([]sessioncontrol.SessionSnapshot, error) {
	f.calls = append(f.calls, "list")
	f.listRequest = request
	return f.items, f.err
}

func (f *sessionControlServiceFake) Get(_ context.Context, request sessioncontrol.GetRequest) (sessioncontrol.SessionSnapshot, error) {
	f.calls = append(f.calls, "get")
	f.getRequest = request
	if len(f.getSnapshots) > 0 {
		index := f.getCalls
		if index >= len(f.getSnapshots) {
			index = len(f.getSnapshots) - 1
		}
		f.getCalls++
		return f.getSnapshots[index], f.err
	}
	return f.snapshot, f.err
}

func (f *sessionControlServiceFake) Send(_ context.Context, request sessioncontrol.SendRequest) (sessioncontrol.OperationResult, error) {
	f.calls = append(f.calls, "send")
	f.sendRequest = request
	return f.result, f.err
}

func (f *sessionControlServiceFake) Stop(_ context.Context, request sessioncontrol.StopRequest) (sessioncontrol.OperationResult, error) {
	f.calls = append(f.calls, "stop")
	f.stopRequest = request
	return f.result, f.err
}

func (f *sessionControlServiceFake) Attach(_ context.Context, request sessioncontrol.AttachRequest) (sessioncontrol.OperationResult, error) {
	f.calls = append(f.calls, "attach")
	f.attachRequest = request
	return f.result, f.err
}

func (f *sessionControlServiceFake) Monitor(_ context.Context, request sessioncontrol.MonitorRequest) (sessioncontrol.OperationResult, error) {
	f.calls = append(f.calls, "monitor")
	f.monitorRequest = request
	return f.result, f.err
}

type sessionControlTenantService struct {
	*fakeTenantService
	resolveErr error
}

func (s *sessionControlTenantService) ResolveContext(ctx context.Context) (tenantservice.Context, error) {
	if s.resolveErr != nil {
		return tenantservice.Context{}, s.resolveErr
	}
	return s.fakeTenantService.ResolveContext(ctx)
}

func newSessionControlHandler(service SessionControlService) http.Handler {
	tenant := &sessionControlTenantService{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
	return NewHandler(Options{AuthToken: "token", TenantService: tenant, SessionControl: service, SessionControlCWDValidator: func(value string) (string, error) { return value, nil }}, nil)
}

func TestSessionControlRuntimeConfigWireContract(t *testing.T) {
	configJSON := `"model":"model-b","provider":"provider-b","permission_mode":"ask","effort":"high","prompt_mode":"chat"`
	want := sessioncontrol.RuntimeConfig{Model: "model-b", Provider: "provider-b", PermissionMode: "ask", Effort: "high", PromptMode: "chat"}
	for _, path := range []string{"/tenant/session-control/sessions", "/tenant/session-control/sessions/tenant/alpha/messages"} {
		service := &sessionControlServiceFake{result: sessioncontrol.OperationResult{Session: sessioncontrol.SessionSnapshot{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Model: want.Model, Provider: want.Provider, PermissionMode: want.PermissionMode, Effort: want.Effort, PromptMode: want.PromptMode}}}
		body := `{` + configJSON + `}`
		if strings.HasSuffix(path, "messages") {
			body = `{"content":"next",` + configJSON + `}`
		}
		request := sessionControlRequest(http.MethodPost, path, body)
		request.Header.Set("Idempotency-Key", "config-key")
		recorder := httptest.NewRecorder()
		newSessionControlHandler(service).ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		actual := service.createRequest.RuntimeConfig()
		if strings.HasSuffix(path, "messages") {
			actual = service.sendRequest.RuntimeConfig()
		}
		if actual != want {
			t.Fatalf("wire config=%+v", actual)
		}
		for _, field := range []string{`"model":"model-b"`, `"provider":"provider-b"`, `"permission_mode":"ask"`, `"effort":"high"`, `"prompt_mode":"chat"`} {
			if !strings.Contains(recorder.Body.String(), field) {
				t.Fatalf("snapshot missing %s: %s", field, recorder.Body.String())
			}
		}
	}
}

func sessionControlRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Tenant-Key", "tenant-a")
	req.Header.Set("X-User-Id", "user-a")
	req.Header.Set("X-Trace-Id", "trace-a")
	return req
}

func decodeSessionControlResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return payload
}

func TestSessionControlRoutesUseExactIsolatedMethods(t *testing.T) {
	routes := newRouter(Options{}, nil).Routes()
	want := map[string]string{
		"GET /tenant/session-control/sessions":                           "",
		"POST /tenant/session-control/sessions":                          "",
		"GET /tenant/session-control/sessions/:source/:id":               "",
		"POST /tenant/session-control/sessions/:source/:id/messages":     "",
		"POST /tenant/session-control/sessions/:source/:id/stop":         "",
		"POST /tenant/session-control/sessions/:source/:id/attachments":  "",
		"POST /tenant/session-control/sessions/:source/:id/monitors":     "",
		"GET /tenant/session-control/sessions/:source/:id/events/stream": "",
	}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = route.Handler
		}
	}
	for route, handler := range want {
		if handler == "" {
			t.Errorf("route %s is not registered", route)
		}
	}
}

func TestSessionControlHTTPUsesResolvedIdentityAndFixedDataEnvelope(t *testing.T) {
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	service := &sessionControlServiceFake{result: sessioncontrol.OperationResult{
		OperationID: "op-1",
		RunID:       41,
		Session: sessioncontrol.SessionSnapshot{
			ID: 9, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"},
			Title: "Managed", Status: sessioncontrol.StatusRunning, UpdatedAt: now,
		},
	}}
	handler := newSessionControlHandler(service)
	req := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/messages", `{"content":"continue"}`)
	req.Header.Set("Idempotency-Key", "send-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got := service.sendRequest
	if got.Context.TenantID != 7 || got.Context.UserID != 11 || got.Context.ActorUserID != 11 || got.Context.TraceID != "trace-a" {
		t.Fatalf("trusted context = %#v", got.Context)
	}
	if got.Ref.String() != "tenant:managed-1" || got.Content != "continue" || got.IdempotencyKey != "send-1" {
		t.Fatalf("send request = %#v", got)
	}
	payload := decodeSessionControlResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	if !ok || data["operation_id"] != "op-1" || data["run_id"] != float64(41) {
		t.Fatalf("response = %#v", payload)
	}
	session, ok := data["session"].(map[string]any)
	if !ok || session["ref"] != "tenant:managed-1" || session["source"] != "tenant" || session["short_id"] != "managed-1" || session["status"] != "running" {
		t.Fatalf("session response = %#v", data["session"])
	}
	if _, leaked := session["Ref"]; leaked {
		t.Fatalf("transport leaked internal Go field names: %#v", session)
	}
}

func TestSessionControlHTTPCarriesInitialTextSourcesAndAttachmentMetadata(t *testing.T) {
	service := &sessionControlServiceFake{result: sessioncontrol.OperationResult{OperationID: "op-1", Session: sessioncontrol.SessionSnapshot{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}}}}
	handler := newSessionControlHandler(service)

	create := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions", `{"title":"Managed","initial_text":"start here"}`)
	create.Header.Set("Idempotency-Key", "create-1")
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, create)
	if createRec.Code != http.StatusOK || service.createRequest.InitialText != "start here" {
		t.Fatalf("create status=%d body=%s request=%#v", createRec.Code, createRec.Body.String(), service.createRequest)
	}

	send := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/messages", `{"content":"continue","source_refs":["tenant:source-1","local:source-2"],"attachments":[{"attachment_id":"att-1","type":"image","media_type":"image/png","name":"screen.png","url":"https://files.example/screen.png","size_bytes":3,"sha256":"abc"}]}`)
	send.Header.Set("Idempotency-Key", "send-1")
	sendRec := httptest.NewRecorder()
	handler.ServeHTTP(sendRec, send)
	if sendRec.Code != http.StatusOK || len(service.sendRequest.SourceRefs) != 2 || len(service.sendRequest.Attachments) != 1 || service.sendRequest.Attachments[0].AttachmentID != "att-1" {
		t.Fatalf("send status=%d body=%s request=%#v", sendRec.Code, sendRec.Body.String(), service.sendRequest)
	}
}

func TestSessionControlHTTPBodyCannotOverrideIdentityOrBindAttachmentsThroughSend(t *testing.T) {
	service := &sessionControlServiceFake{}
	handler := newSessionControlHandler(service)
	for _, body := range []string{
		`{"content":"continue","tenant_id":999}`,
		`{"content":"continue","user_id":999}`,
		`{"content":"continue","actor_user_id":999}`,
		`{"content":"continue","trace_id":"forged"}`,
		`{"content":"continue","attachments":[{"id":"asset-1"}]}`,
	} {
		req := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/messages", body)
		req.Header.Set("Idempotency-Key", "send-1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
		payload := decodeSessionControlResponse(t, rec)
		if payload["code"] != "invalid_request" {
			t.Errorf("body=%s response=%#v", body, payload)
		}
	}
	if len(service.calls) != 0 {
		t.Fatalf("service calls = %v, want none", service.calls)
	}
}

func TestSessionControlHTTPValidatesNamespacedRefsAndIdempotency(t *testing.T) {
	service := &sessionControlServiceFake{}
	handler := newSessionControlHandler(service)
	tests := []struct {
		name   string
		path   string
		key    string
		status int
		code   string
	}{
		{name: "unknown source", path: "/tenant/session-control/sessions/remote/id/messages", key: "send-1", status: 400, code: "invalid_ref"},
		{name: "invalid id", path: "/tenant/session-control/sessions/tenant/bad:id/messages", key: "send-1", status: 400, code: "invalid_ref"},
		{name: "missing key", path: "/tenant/session-control/sessions/tenant/id/messages", status: 400, code: "idempotency_key_required"},
		{name: "too long key", path: "/tenant/session-control/sessions/tenant/id/messages", key: strings.Repeat("k", 129), status: 400, code: "idempotency_key_too_long"},
		{name: "local write", path: "/tenant/session-control/sessions/local/id/messages", key: "send-1", status: 403, code: "forbidden"},
		{name: "local stop", path: "/tenant/session-control/sessions/local/id/stop", key: "stop-1", status: 403, code: "forbidden"},
		{name: "local attach", path: "/tenant/session-control/sessions/local/id/attachments", key: "attach-1", status: 403, code: "forbidden"},
		{name: "local monitor", path: "/tenant/session-control/sessions/local/id/monitors", key: "monitor-1", status: 403, code: "forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := sessionControlRequest(http.MethodPost, tt.path, `{"content":"continue"}`)
			if tt.key != "" {
				req.Header.Set("Idempotency-Key", tt.key)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			payload := decodeSessionControlResponse(t, rec)
			if payload["code"] != tt.code || strings.TrimSpace(payload["error"].(string)) == "" {
				t.Fatalf("response=%#v", payload)
			}
		})
	}
	if len(service.calls) != 0 {
		t.Fatalf("service calls = %v, want none", service.calls)
	}
}

func TestSessionControlHTTPMapsStableServiceErrors(t *testing.T) {
	tests := []struct {
		code   sessioncontrol.ServiceErrorCode
		status int
	}{
		{sessioncontrol.CodeForbidden, http.StatusForbidden},
		{sessioncontrol.CodeNotFound, http.StatusNotFound},
		{sessioncontrol.CodeInvalidState, http.StatusConflict},
		{sessioncontrol.CodeIdempotencyConflict, http.StatusConflict},
		{sessioncontrol.CodeHandoffStale, http.StatusConflict},
		{sessioncontrol.CodeBudgetExceeded, http.StatusUnprocessableEntity},
		{sessioncontrol.CodeSchedulerUnavailable, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			service := &sessionControlServiceFake{err: &sessioncontrol.ServiceError{Code: tt.code, Message: "safe message", Cause: errors.New("private storage detail")}}
			handler := newSessionControlHandler(service)
			req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1", "")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			payload := decodeSessionControlResponse(t, rec)
			if payload["code"] != string(tt.code) || payload["error"] != "safe message" || strings.Contains(rec.Body.String(), "private storage detail") {
				t.Fatalf("response=%#v", payload)
			}
		})
	}
}

func TestSessionControlHTTPCoversAllOperations(t *testing.T) {
	service := &sessionControlServiceFake{
		items:    []sessioncontrol.SessionSnapshot{{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceLocal, Key: "local-1"}, ReadOnly: true}},
		snapshot: sessioncontrol.SessionSnapshot{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}},
	}
	handler := newSessionControlHandler(service)
	tests := []struct {
		method string
		path   string
		body   string
		key    string
		call   string
	}{
		{http.MethodGet, "/tenant/session-control/sessions?source=local&limit=5", "", "", "list"},
		{http.MethodPost, "/tenant/session-control/sessions", `{"session_key":"managed-1","title":"Managed","model":"model-a","cwd":"/repo"}`, "create-1", "create"},
		{http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1?include_links=true", "", "", "get"},
		{http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/messages", `{"content":"continue"}`, "send-1", "send"},
		{http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/stop", `{}`, "stop-1", "stop"},
		{http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/attachments", `{"target_task_id":41,"target_context_window_tokens":16384,"sources":["tenant:source-1","local:source-2"],"relation_type":"handoff"}`, "attach-1", "attach"},
		{http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/monitors", `{"sources":["tenant:source-1"],"interval_seconds":60,"channel":"feishu"}`, "monitor-1", "monitor"},
	}
	for _, tt := range tests {
		req := sessionControlRequest(tt.method, tt.path, tt.body)
		if tt.key != "" {
			req.Header.Set("Idempotency-Key", tt.key)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", tt.method, tt.path, rec.Code, rec.Body.String())
		}
	}
	if got, want := strings.Join(service.calls, ","), "list,create,get,send,stop,attach,monitor"; got != want {
		t.Fatalf("calls=%s want=%s", got, want)
	}
	if service.listRequest.Source != sessioncontrol.SourceLocal || service.listRequest.Limit != 5 {
		t.Fatalf("list request=%#v", service.listRequest)
	}
	if !service.getRequest.IncludeLinks || service.getRequest.Ref.String() != "tenant:managed-1" {
		t.Fatalf("get request=%#v", service.getRequest)
	}
	if len(service.attachRequest.Sources) != 2 || service.attachRequest.Sources[1].String() != "local:source-2" {
		t.Fatalf("attach request=%#v", service.attachRequest)
	}
	if service.monitorRequest.Channel != "feishu" || service.monitorRequest.IdempotencyKey != "monitor-1" {
		t.Fatalf("monitor request=%#v", service.monitorRequest)
	}
}

func TestSessionControlHTTPAuthAndDependencyFailuresUseStableEnvelope(t *testing.T) {
	service := &sessionControlServiceFake{}
	t.Run("bearer auth", func(t *testing.T) {
		handler := newSessionControlHandler(service)
		req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/id", "")
		req.Header.Del("Authorization")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || decodeSessionControlResponse(t, rec)["code"] != "unauthorized" {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("tenant identity", func(t *testing.T) {
		tenant := &sessionControlTenantService{fakeTenantService: &fakeTenantService{}, resolveErr: tenantservice.ErrMissingTenantKey}
		handler := NewHandler(Options{AuthToken: "token", TenantService: tenant, SessionControl: service}, nil)
		req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/id", "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || decodeSessionControlResponse(t, rec)["code"] != "invalid_request" {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
	t.Run("session control unavailable", func(t *testing.T) {
		handler := NewHandler(Options{AuthToken: "token", TenantService: &fakeTenantService{}}, nil)
		req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/id", "")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || decodeSessionControlResponse(t, rec)["code"] != "service_unavailable" {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestSessionControlHTTPReturnsIdenticalReplayEnvelope(t *testing.T) {
	service := &sessionControlServiceFake{result: sessioncontrol.OperationResult{
		OperationID: "op-1", Replayed: true,
		Session: sessioncontrol.SessionSnapshot{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}, Status: sessioncontrol.StatusRunning},
	}}
	handler := newSessionControlHandler(service)
	request := func() []byte {
		req := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/managed-1/messages", `{"content":"continue"}`)
		req.Header.Set("Idempotency-Key", "same-key")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		return bytes.TrimSpace(rec.Body.Bytes())
	}
	first, second := request(), request()
	if !bytes.Equal(first, second) {
		t.Fatalf("first=%s second=%s", first, second)
	}
}
