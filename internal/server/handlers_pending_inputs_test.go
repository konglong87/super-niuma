package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestPendingInputHandlerCreatesListsAndMovesUp(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusRunning}}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, nil)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs", bytes.NewBufferString(body))
		req.Header.Set("authorization", "Bearer token")
		req.Header.Set("X-User-Id", "user-test")
		req.Header.Set("X-Tenant-Key", "yutang")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"client_input_id":"a","content":"A"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("first status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"client_input_id":"b","content":"B"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("second status=%d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs/pi-2/move-up", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("move status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/tenant/agent-tasks/41/pending-inputs", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"B"`) {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPendingInputHandlerRejectsQueueWhenDisabled(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusRunning}}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, nil)
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	if err := queue.SetQueueEnabled(context.Background(), scope, false); err != nil {
		t.Fatalf("disable queue: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs", strings.NewReader(`{"client_input_id":"a","content":"A"}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPendingInputEventsExcludeUserContentAndAttachments(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusRunning}}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, nil)
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs", strings.NewReader(`{"client_input_id":"secret-client","content":"secret body","direction":"secret direction","attachments":[{"type":"image","media_type":"image/png","size_bytes":12,"url":"https://private.example/image.png"}]}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	payload := fake.lastAgentTaskEventInput.PayloadJSON
	for _, forbidden := range []string{"secret body", "secret direction", "secret-client", "private.example", "attachments", "content", "direction", "client_input_id"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("event payload leaked %q: %s", forbidden, payload)
		}
	}
	for _, required := range []string{`"input_id":"pi-1"`, `"status":"queued"`, `"sequence":1`} {
		if !strings.Contains(payload, required) {
			t.Fatalf("event payload missing %s: %s", required, payload)
		}
	}
}

func TestPendingInputHandlerReusesAgentAttachmentValidation(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, Status: agenttasks.StatusRunning}}}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, nil)
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs", strings.NewReader(`{"client_input_id":"invalid-attachment","content":"body","attachments":[{"type":"file","media_type":"text/plain","size_bytes":12,"url":"file:///tmp/private"}]}`))
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "only image is allowed") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	items, _ := queue.List(context.Background(), pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41})
	if len(items) != 0 {
		t.Fatalf("invalid attachment was persisted: %+v", items)
	}
}

func TestPendingInputSideChatCreatesSelectableTask(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	fake := &fakeTenantService{agentTasks: []mysqlstore.AgentTask{{ID: 41, ParentSessionID: 5, AgentName: "web-agent", Description: "main", Model: "model-a", Status: agenttasks.StatusRunning, MetadataJSON: `{"cwd":"/repo","provider":"provider-a"}`}}}
	scope := pendinginput.Scope{TenantID: 1, UserID: 2, SessionID: "5", BaseTaskID: 41}
	item, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "side", Content: "inspect separately", Direction: "focus"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	handler := NewHandler(Options{AuthToken: "token", TenantService: fake, PendingInputQueue: queue}, nil)
	req := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs/"+item.ID+"/side-chat", nil)
	req.Header.Set("authorization", "Bearer token")
	req.Header.Set("X-User-Id", "user-test")
	req.Header.Set("X-Tenant-Key", "yutang")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"task_id":`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreatedAgentTask.Status != agenttasks.StatusReady || fake.lastCreatedAgentTask.ParentSessionID == 0 || !strings.Contains(fake.lastCreatedAgentTask.MetadataJSON, `"source_pending_input_id":"`+item.ID+`"`) {
		t.Fatalf("side task = %+v", fake.lastCreatedAgentTask)
	}
	if fake.lastAgentTaskEventInput.EventType != agenttasks.EventMessage || !strings.Contains(fake.lastAgentTaskEventInput.PayloadJSON, `"from_agent":"webui"`) || !strings.Contains(fake.lastAgentTaskEventInput.PayloadJSON, "inspect separately") {
		t.Fatalf("side task message event = %+v", fake.lastAgentTaskEventInput)
	}
	var response struct {
		SessionID uint64 `json:"session_id"`
		TaskID    uint64 `json:"task_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	sideScope := pendingInputScopeForTask(mysqlstore.AgentTask{ID: response.TaskID, TenantID: 1, UserID: 2, ParentSessionID: response.SessionID, MetadataJSON: fake.lastCreatedAgentTask.MetadataJSON})
	if sideScope.SessionID != strconv.FormatUint(response.SessionID, 10) || sideScope.BaseTaskID != response.TaskID {
		t.Fatalf("side scope=%+v; side chat must have independent lineage", sideScope)
	}

	retryReq := httptest.NewRequest(http.MethodPost, "/tenant/agent-tasks/41/pending-inputs/"+item.ID+"/side-chat", nil)
	retryReq.Header = req.Header.Clone()
	retryRec := httptest.NewRecorder()
	handler.ServeHTTP(retryRec, retryReq)
	if retryRec.Code != http.StatusOK || retryRec.Body.String() != rec.Body.String() {
		t.Fatalf("idempotent retry status=%d body=%s first=%s", retryRec.Code, retryRec.Body.String(), rec.Body.String())
	}
	if len(fake.agentTasks) != 2 {
		t.Fatalf("side-chat retry created duplicate tasks: %+v", fake.agentTasks)
	}
	items, err := queue.List(context.Background(), scope)
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != pendinginput.StatusQueued {
		t.Fatalf("main pending candidate changed: items=%+v err=%v", items, err)
	}
}

func decodePendingInput(t *testing.T, rec *httptest.ResponseRecorder) pendinginput.PendingInput {
	t.Helper()
	var body struct {
		Data pendinginput.PendingInput `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}
