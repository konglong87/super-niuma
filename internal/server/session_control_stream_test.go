package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type sessionControlEventServiceFake struct {
	batches  [][]mysqlstore.AgentTaskEvent
	err      error
	calls    int
	taskID   uint64
	afterIDs []uint64
	limits   []int
}

func (f *sessionControlEventServiceFake) ListAgentTaskEventsAfter(_ context.Context, taskID, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	f.taskID = taskID
	f.afterIDs = append(f.afterIDs, afterID)
	f.limits = append(f.limits, limit)
	if f.err != nil {
		return nil, f.err
	}
	index := f.calls
	f.calls++
	if index >= len(f.batches) {
		return nil, nil
	}
	return f.batches[index], nil
}

func newSessionControlStreamHandler(service SessionControlService, events SessionControlEventService) http.Handler {
	tenant := &sessionControlTenantService{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}}
	return NewHandler(Options{AuthToken: "token", TenantService: tenant, SessionControl: service, SessionControlEvents: events}, nil)
}

func TestSessionControlStreamAuthenticatesAndReadsTargetBeforeHeaders(t *testing.T) {
	tests := []struct {
		name        string
		authorize   bool
		serviceErr  error
		wantStatus  int
		wantCode    string
		wantGetCall bool
	}{
		{name: "auth", authorize: false, wantStatus: 401, wantCode: "unauthorized"},
		{name: "ownership", authorize: true, serviceErr: &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "not owner"}, wantStatus: 403, wantCode: "forbidden", wantGetCall: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &sessionControlServiceFake{err: tt.serviceErr}
			handler := newSessionControlStreamHandler(service, &sessionControlEventServiceFake{})
			req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1/events/stream", "")
			if !tt.authorize {
				req.Header.Del("Authorization")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatalf("SSE headers committed before authorization/readback: %#v", rec.Header())
			}
			if decodeSessionControlResponse(t, rec)["code"] != tt.wantCode {
				t.Fatalf("body=%s", rec.Body.String())
			}
			if got := service.getCalls > 0 || service.getRequest.Ref.Key != ""; got != tt.wantGetCall {
				t.Fatalf("Get called=%v want=%v", got, tt.wantGetCall)
			}
		})
	}
}

func TestSessionControlStreamFailsClosedWithoutEventPortBeforeHeaders(t *testing.T) {
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}, Status: sessioncontrol.StatusRunning, ActiveRunID: 41}}
	handler := newSessionControlStreamHandler(service, nil)
	req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1/events/stream", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || decodeSessionControlResponse(t, rec)["code"] != "service_unavailable" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE headers committed without event port: %#v", rec.Header())
	}
}

func TestSessionControlStreamResumesCursorAndEmitsOnlyBoundedState(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{getSnapshots: []sessioncontrol.SessionSnapshot{
		{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41, UpdatedAt: now},
		{Ref: ref, Status: sessioncontrol.StatusCompleted, ActiveRunID: 41, UpdatedAt: now.Add(time.Second)},
	}}
	events := &sessionControlEventServiceFake{batches: [][]mysqlstore.AgentTaskEvent{{{
		ID: 8, TaskID: 41, EventType: agenttasks.EventCompleted, CreatedAt: now.Add(time.Second),
		PayloadJSON: `{"content":"PRIVATE","handoff":{"summary":"SECRET"},"attachments":[{"url":"https://private"}],"source_locator":"/private/path","api_key":"SECRET"}`,
	}}}}
	handler := newSessionControlStreamHandler(service, events)
	req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1/events/stream", "")
	req.Header.Set("Last-Event-ID", "7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d headers=%#v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}
	if len(events.afterIDs) == 0 || events.afterIDs[0] != 7 || events.taskID != 41 {
		t.Fatalf("event reads task=%d cursors=%v", events.taskID, events.afterIDs)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"id: 7", "event: session", "id: 8", "event: run",
		`"schema_version":"niuma.session-control-state.v1"`, `"cursor":"8"`,
		`"session_ref":"tenant:managed-1"`, `"run_id":41`, `"status":"completed"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"PRIVATE", "SECRET", "private/path", "attachments", "source_locator", "api_key", "payload_json", "trace_id"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body leaked %q:\n%s", forbidden, body)
		}
	}
}

func TestSessionControlStreamQueryCursorOverridesLastEventID(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{getSnapshots: []sessioncontrol.SessionSnapshot{
		{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41, UpdatedAt: now},
		{Ref: ref, Status: sessioncontrol.StatusCompleted, ActiveRunID: 41, UpdatedAt: now.Add(time.Second)},
	}}
	events := &sessionControlEventServiceFake{batches: [][]mysqlstore.AgentTaskEvent{{{ID: 10, TaskID: 41, EventType: agenttasks.EventCompleted, CreatedAt: now}}}}
	handler := newSessionControlStreamHandler(service, events)
	req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1/events/stream?cursor=9", "")
	req.Header.Set("Last-Event-ID", "7")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || len(events.afterIDs) == 0 || events.afterIDs[0] != 9 {
		t.Fatalf("status=%d cursors=%v body=%s", rec.Code, events.afterIDs, rec.Body.String())
	}
}

func TestSessionControlStreamRejectsMalformedCursorBeforeHeaders(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41}}
	handler := newSessionControlStreamHandler(service, &sessionControlEventServiceFake{})
	req := sessionControlRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/managed-1/events/stream?cursor=not-a-number", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || decodeSessionControlResponse(t, rec)["code"] != "invalid_request" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE headers committed for malformed cursor: %#v", rec.Header())
	}
}

func TestSessionControlStreamReadbackRepairsMissedStateAndDeduplicates(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{getSnapshots: []sessioncontrol.SessionSnapshot{
		{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41, UpdatedAt: now},
		{Ref: ref, Status: sessioncontrol.StatusWaitingPermission, ActiveRunID: 41, UpdatedAt: now.Add(time.Second)},
		{Ref: ref, Status: sessioncontrol.StatusWaitingPermission, ActiveRunID: 41, UpdatedAt: now.Add(time.Second)},
		{Ref: ref, Status: sessioncontrol.StatusCompleted, ActiveRunID: 41, UpdatedAt: now.Add(2 * time.Second)},
	}}
	events := &sessionControlEventServiceFake{}
	var emitted []sessionControlStateEvent
	stream := newSessionControlStateStream(service, events, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, ref, service.getSnapshots[0], 20)
	stream.write = func(event string, value sessionControlStateEvent) { emitted = append(emitted, value) }
	stream.heartbeat = func() {}
	stream.flush = func() {}
	stream.policy = sessionControlStreamPolicy{minInterval: time.Millisecond, maxInterval: 2 * time.Millisecond, wait: func(context.Context, time.Duration) bool { return true }}
	stream.run(context.Background())

	var statuses []sessioncontrol.SessionStatus
	for _, event := range emitted {
		statuses = append(statuses, event.Status)
	}
	if got, want := statuses, []sessioncontrol.SessionStatus{sessioncontrol.StatusRunning, sessioncontrol.StatusWaitingPermission, sessioncontrol.StatusCompleted}; !equalSessionControlStatuses(got, want) {
		t.Fatalf("statuses=%v want=%v", got, want)
	}
}

func TestSessionControlStreamDisconnectNeverStopsRun(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41}}
	stream := newSessionControlStateStream(service, &sessionControlEventServiceFake{}, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, ref, service.snapshot, 20)
	stream.write = func(string, sessionControlStateEvent) {}
	stream.heartbeat = func() {}
	stream.flush = func() {}
	stream.policy = sessionControlStreamPolicy{minInterval: time.Millisecond, maxInterval: time.Millisecond, wait: func(context.Context, time.Duration) bool { return false }}
	stream.run(context.Background())
	for _, call := range service.calls {
		if call == "stop" {
			t.Fatalf("disconnect invoked Stop: calls=%v", service.calls)
		}
	}
}

func TestSessionControlStreamEmitsHeartbeatWhenIdle(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41}}
	stream := newSessionControlStateStream(service, &sessionControlEventServiceFake{}, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, ref, service.snapshot, 20)
	heartbeats := 0
	stream.write = func(string, sessionControlStateEvent) {}
	stream.heartbeat = func() { heartbeats++ }
	stream.flush = func() {}
	stream.policy = sessionControlStreamPolicy{minInterval: time.Millisecond, maxInterval: time.Millisecond, wait: func(context.Context, time.Duration) bool { return false }}
	stream.run(context.Background())
	if heartbeats != 1 {
		t.Fatalf("heartbeats=%d want=1", heartbeats)
	}
}

func TestSessionControlStreamProjectsOperationEventsWithoutPayload(t *testing.T) {
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	operationID := strings.Repeat("a", 64)
	event := mysqlstore.AgentTaskEvent{ID: 12, TaskID: 41, EventType: agenttasks.EventSessionControlStop, CreatedAt: now, PayloadJSON: `{"operation":{"operation_id":"` + operationID + `","key_hash":"secret"},"content":"private"}`}
	projected := projectSessionControlTaskEvent(sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}, event)
	if projected.Type != sessionControlEventOperation || projected.OperationID != operationID || projected.Status != sessioncontrol.StatusStopped || projected.Cursor != "12" {
		t.Fatalf("projection=%#v", projected)
	}
}

func TestSessionControlStreamProjectsHandoffEventEvenWhenStatusIsUnchanged(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{getSnapshots: []sessioncontrol.SessionSnapshot{
		{Ref: ref, Status: sessioncontrol.StatusQueued, ActiveRunID: 41},
		{Ref: ref, Status: sessioncontrol.StatusCompleted, ActiveRunID: 41},
	}}
	events := &sessionControlEventServiceFake{batches: [][]mysqlstore.AgentTaskEvent{{{
		ID: 12, TaskID: 41, EventType: agenttasks.EventSessionHandoff, PayloadJSON: `{"summary":"private"}`,
	}}}}
	var types []string
	var statuses []sessioncontrol.SessionStatus
	stream := newSessionControlStateStream(service, events, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, ref, service.getSnapshots[0], 9999)
	stream.write = func(event string, value sessionControlStateEvent) {
		types = append(types, event)
		statuses = append(statuses, value.Status)
	}
	stream.heartbeat = func() {}
	stream.flush = func() {}
	stream.run(context.Background())
	if got, want := strings.Join(types, ","), "session,session,session"; got != want {
		t.Fatalf("event types=%s want=%s", got, want)
	}
	if statuses[1] != sessioncontrol.StatusQueued {
		t.Fatalf("handoff projection status=%s want=%s", statuses[1], sessioncontrol.StatusQueued)
	}
	if len(events.limits) == 0 || events.limits[0] != defaultSessionControlStreamLimit {
		t.Fatalf("event limits=%v", events.limits)
	}
}

func TestSessionControlStreamErrorsAreBounded(t *testing.T) {
	ref := sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "managed-1"}
	service := &sessionControlServiceFake{snapshot: sessioncontrol.SessionSnapshot{Ref: ref, Status: sessioncontrol.StatusRunning, ActiveRunID: 41}}
	events := &sessionControlEventServiceFake{err: errors.New("database password=SECRET content=PRIVATE")}
	var emitted []sessionControlStateEvent
	stream := newSessionControlStateStream(service, events, sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, ref, service.snapshot, 20)
	var eventTypes []string
	stream.write = func(eventType string, value sessionControlStateEvent) {
		eventTypes = append(eventTypes, eventType)
		emitted = append(emitted, value)
	}
	stream.heartbeat = func() {}
	stream.flush = func() {}
	stream.run(context.Background())
	if len(emitted) != 2 || emitted[1].Status != sessioncontrol.StatusRunning || eventTypes[1] != sessionControlEventStreamError {
		t.Fatalf("types=%v events=%#v", eventTypes, emitted)
	}
}

func equalSessionControlStatuses(a, b []sessioncontrol.SessionStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
