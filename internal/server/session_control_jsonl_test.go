package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func TestSessionControlJSONLCompositionPersistsConversationOutsideSQLiteEvents(t *testing.T) {
	ctx := observability.WithRequestValues(context.Background(), "trace-jsonl-compose", "webui-local-user", "webui-local")
	repo, err := mysqlstore.OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "desktop.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	tenantID, err := repo.UpsertTenant(ctx, mysqlstore.TenantInput{TenantKey: "webui-local", Name: "Local Desktop"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "webui-local-user")
	if err != nil {
		t.Fatal(err)
	}
	tenantSvc := tenantservice.NewService(repo, nil)
	countingTenantSvc := &countingAgentTaskTenantService{Service: tenantSvc}
	transcriptStore := session.Store{TranscriptProjectsRoot: t.TempDir()}
	opts := Options{
		AuthToken:           "test-token",
		Workspace:           t.TempDir(),
		TenantService:       countingTenantSvc,
		AgentTaskStore:      tenantSvc,
		PendingInputQueue:   repo,
		AgentTaskController: agenttasks.NewController(),
		SessionEvents:       NewJSONLSessionEventStore(transcriptStore),
		SessionControlCWDValidator: func(cwd string) (string, error) {
			return cwd, nil
		},
	}
	opts.SessionControlEvents = NewSessionControlEventReader(opts)
	service, err := NewSessionControlService(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	opts.SessionControl = service

	scope := sessioncontrol.RequestContext{TenantID: tenantID, UserID: userID, ActorUserID: userID}
	created, err := service.Create(ctx, sessioncontrol.CreateRequest{
		Context: scope, SessionKey: "jsonl-compose", Title: "JSONL composition", CWD: opts.Workspace,
		IdempotencyKey: "jsonl-compose-create",
	})
	if err != nil {
		t.Fatal(err)
	}

	taskID, err := tenantSvc.CreateAgentTask(ctx, agenttasks.TaskInput{
		TenantID:        tenantID,
		UserID:          userID,
		ParentSessionID: created.Session.ID,
		AgentName:       agenttasks.AgentNameWeb,
		Status:          agenttasks.StatusRunning,
		Model:           "test-model",
		MetadataJSON:    `{"cwd":"` + opts.Workspace + `","source":"desktop","channel":"desktop"}`,
		TraceID:         "trace-jsonl-task",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := tenantSvc.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opts.SessionEvents.AppendTaskEvent(ctx, task, agenttasks.EventInput{
		TenantID: tenantID, UserID: userID, TaskID: taskID, EventType: agenttasks.EventMessage,
		PayloadJSON: `{"content":"persisted in transcript"}`, TraceID: task.TraceID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := opts.SessionEvents.AppendTaskEvent(ctx, task, agenttasks.EventInput{
		TenantID: tenantID, UserID: userID, TaskID: taskID, EventType: agenttasks.EventPermissionRequest,
		PayloadJSON: `{}`, TraceID: task.TraceID,
	}); err != nil {
		t.Fatal(err)
	}

	dbEvents, err := repo.ListAgentTaskEvents(ctx, tenantID, userID, taskID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbEvents) != 0 {
		t.Fatalf("SQLite event rows = %d, want 0 in JSONL mode", len(dbEvents))
	}

	listed, err := service.List(ctx, sessioncontrol.ListRequest{Context: scope, Source: sessioncontrol.SourceTenant})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Status != sessioncontrol.StatusWaitingPermission {
		t.Fatalf("session summaries = %+v", listed)
	}
	if countingTenantSvc.getAgentTaskCalls != 0 {
		t.Fatalf("session list performed %d per-task lookups, want 0", countingTenantSvc.getAgentTaskCalls)
	}

	handler := NewHandler(opts, nil)
	request := httptest.NewRequest(http.MethodGet, "/tenant/session-control/sessions/tenant/jsonl-compose/conversation", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("X-Tenant-Key", "webui-local")
	request.Header.Set("X-User-Id", "webui-local-user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("conversation status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data struct {
			Events []mysqlstore.AgentTaskEvent `json:"events"`
			Cursor string                      `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Events) != 2 || envelope.Data.Events[0].PayloadJSON != `{"content":"persisted in transcript"}` || envelope.Data.Events[1].EventType != agenttasks.EventPermissionRequest {
		t.Fatalf("conversation events = %+v", envelope.Data.Events)
	}
	event := envelope.Data.Events[0]
	if event.Source != "desktop" || event.Surface != "wails" || event.Channel != "desktop" {
		t.Fatalf("conversation provenance = %+v", event)
	}
	if envelope.Data.Cursor != "2" {
		t.Fatalf("conversation cursor = %q, want 2", envelope.Data.Cursor)
	}
}

type countingAgentTaskTenantService struct {
	*tenantservice.Service
	getAgentTaskCalls int
}

func (s *countingAgentTaskTenantService) GetAgentTask(ctx context.Context, taskID uint64) (mysqlstore.AgentTask, error) {
	s.getAgentTaskCalls++
	return s.Service.GetAgentTask(ctx, taskID)
}
