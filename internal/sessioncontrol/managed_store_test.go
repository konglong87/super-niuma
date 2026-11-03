package sessioncontrol

import (
	"context"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

func TestTenantManagedStoreForwardsMatchingScope(t *testing.T) {
	service := &tenantManagedServiceFake{resolved: tenant.Context{TenantID: 7, UserID: 11}}
	store := NewTenantManagedStore(service)
	scope := RequestContext{TenantID: 7, UserID: 11, ActorUserID: 99}
	ctx := context.Background()

	if _, err := store.UpsertSession(ctx, scope, mysql.SessionInput{TenantID: 1, UserID: 2, SessionKey: "alpha", Title: "Alpha", Status: "idle", Model: "m", CWD: "/repo", MetadataJSON: `{"kind":"managed"}`}); err != nil {
		t.Fatalf("UpsertSession() error = %v", err)
	}
	if _, err := store.ListSessions(ctx, scope, 12); err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if _, err := store.GetSessionByKey(ctx, scope, "alpha"); err != nil {
		t.Fatalf("GetSessionByKey() error = %v", err)
	}
	if _, err := store.ListLatestAgentTasksForSessions(ctx, scope, []uint64{5, 6}); err != nil {
		t.Fatalf("ListLatestAgentTasksForSessions() error = %v", err)
	}
	if _, err := store.CreateAgentTask(ctx, scope, agenttasks.TaskInput{TenantID: 1, UserID: 2, ParentSessionID: 5, Description: "run"}); err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if _, err := store.CancelAgentTaskIfRunning(ctx, scope, 13, `{"status":"cancelled"}`); err != nil {
		t.Fatalf("CancelAgentTaskIfRunning() error = %v", err)
	}
	if _, err := store.ListAgentTaskEventsForTasksComplete(ctx, scope, []uint64{13}); err != nil {
		t.Fatalf("ListAgentTaskEventsForTasksComplete() error = %v", err)
	}
	if _, err := store.ListSessionLinks(ctx, scope, 5, 4); err != nil {
		t.Fatalf("ListSessionLinks() error = %v", err)
	}

	if got, want := service.session, (tenant.SessionRequest{SessionKey: "alpha", Title: "Alpha", Status: "idle", Model: "m", CWD: "/repo", MetadataJSON: `{"kind":"managed"}`}); got != want {
		t.Fatalf("session request = %+v, want %+v", got, want)
	}
	if service.sessionLimit != 12 || service.sessionKey != "alpha" || !equalIDs(service.latestSessionIDs, []uint64{5, 6}) {
		t.Fatalf("session forwarding = limit %d key %q ids %v", service.sessionLimit, service.sessionKey, service.latestSessionIDs)
	}
	if got := service.task; got.TenantID != 7 || got.UserID != 11 || got.ParentSessionID != 5 || got.Description != "run" {
		t.Fatalf("task forwarding = %+v", got)
	}
	if service.cancelTaskID != 13 || service.cancelResultJSON != `{"status":"cancelled"}` || !equalIDs(service.eventTaskIDs, []uint64{13}) || service.linkSessionID != 5 || service.linkLimit != 4 {
		t.Fatalf("task/link forwarding did not preserve arguments")
	}
}

func TestTenantManagedStoreRejectsTenantMismatchBeforeMutation(t *testing.T) {
	service := &tenantManagedServiceFake{resolved: tenant.Context{TenantID: 8, UserID: 11}}
	store := NewTenantManagedStore(service)
	scope := RequestContext{TenantID: 7, UserID: 11}

	for _, mutate := range []func() error{
		func() error {
			_, err := store.UpsertSession(context.Background(), scope, mysql.SessionInput{SessionKey: "alpha"})
			return err
		},
		func() error {
			_, err := store.CreateAgentTask(context.Background(), scope, agenttasks.TaskInput{})
			return err
		},
		func() error {
			_, err := store.CancelAgentTaskIfRunning(context.Background(), scope, 13, "cancelled")
			return err
		},
	} {
		assertServiceErrorCode(t, mutate(), CodeForbidden)
	}
	if service.upsertSessionCalls != 0 || service.createTaskCalls != 0 || service.cancelTaskCalls != 0 {
		t.Fatalf("mutation calls = upsert:%d create:%d cancel:%d, want all 0", service.upsertSessionCalls, service.createTaskCalls, service.cancelTaskCalls)
	}
}

func TestTenantManagedStoreRejectsUserMismatchBeforeMutation(t *testing.T) {
	service := &tenantManagedServiceFake{resolved: tenant.Context{TenantID: 7, UserID: 12}}
	store := NewTenantManagedStore(service)
	scope := RequestContext{TenantID: 7, UserID: 11}

	for _, mutate := range []func() error{
		func() error {
			_, err := store.UpsertSession(context.Background(), scope, mysql.SessionInput{SessionKey: "alpha"})
			return err
		},
		func() error {
			_, err := store.CreateAgentTask(context.Background(), scope, agenttasks.TaskInput{})
			return err
		},
		func() error {
			_, err := store.CancelAgentTaskIfRunning(context.Background(), scope, 13, "cancelled")
			return err
		},
	} {
		assertServiceErrorCode(t, mutate(), CodeForbidden)
	}
	if service.upsertSessionCalls != 0 || service.createTaskCalls != 0 || service.cancelTaskCalls != 0 {
		t.Fatalf("mutation calls = upsert:%d create:%d cancel:%d, want all 0", service.upsertSessionCalls, service.createTaskCalls, service.cancelTaskCalls)
	}
}

func TestTenantManagedStoreExactHandoffReadsStayInScope(t *testing.T) {
	service := &tenantManagedServiceFake{
		resolved:   tenant.Context{TenantID: 7, UserID: 11},
		messageRow: mysql.Message{ID: 90, SessionID: 5, Role: "user"},
		taskRow:    mysql.AgentTask{ID: 13, ParentSessionID: 5},
		eventRow:   mysql.AgentTaskEvent{ID: 17, TaskID: 13, EventType: agenttasks.EventToolResult},
	}
	store := NewTenantManagedStore(service)
	scope := RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}

	if _, err := store.GetMessage(context.Background(), scope, 5, 90); err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if _, err := store.GetTaskEvent(context.Background(), scope, 5, 17); err != nil {
		t.Fatalf("GetTaskEvent() error = %v", err)
	}
	if _, err := store.GetToolTraceRecord(context.Background(), scope, 5, 17, 0); err != nil {
		t.Fatalf("GetToolTraceRecord() error = %v", err)
	}
	if service.messageSessionID != 5 || service.messageID != 90 || service.eventID != 17 || service.taskID != 13 {
		t.Fatalf("exact reads = session/message %d/%d event/task %d/%d", service.messageSessionID, service.messageID, service.eventID, service.taskID)
	}
}

func equalIDs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type tenantManagedServiceFake struct {
	resolved tenant.Context

	session            tenant.SessionRequest
	sessionLimit       int
	sessionKey         string
	latestSessionIDs   []uint64
	task               agenttasks.TaskInput
	cancelTaskID       uint64
	cancelResultJSON   string
	eventTaskIDs       []uint64
	linkSessionID      uint64
	linkLimit          int
	upsertSessionCalls int
	createTaskCalls    int
	cancelTaskCalls    int
	messageRow         mysql.Message
	taskRow            mysql.AgentTask
	eventRow           mysql.AgentTaskEvent
	messageSessionID   uint64
	messageID          uint64
	taskID             uint64
	eventID            uint64
}

func (f *tenantManagedServiceFake) ResolveContext(context.Context) (tenant.Context, error) {
	return f.resolved, nil
}
func (f *tenantManagedServiceFake) ResolveContextOnce(ctx context.Context) (context.Context, tenant.Context, error) {
	return ctx, f.resolved, nil
}
func (f *tenantManagedServiceFake) UpsertSession(_ context.Context, input tenant.SessionRequest) (uint64, error) {
	f.upsertSessionCalls++
	f.session = input
	return 1, nil
}
func (f *tenantManagedServiceFake) CreateSessionControlSession(_ context.Context, input tenant.SessionControlSessionRequest) (mysql.SessionControlCreateResult, error) {
	f.session = input
	return mysql.SessionControlCreateResult{Session: mysql.SessionControlSession{Session: mysql.Session{ID: 1, SessionKey: input.SessionKey}, MetadataJSON: input.MetadataJSON}, Created: true}, nil
}
func (f *tenantManagedServiceFake) ListSessionControlSessions(_ context.Context, limit int) ([]mysql.SessionControlSession, error) {
	f.sessionLimit = limit
	return nil, nil
}
func (f *tenantManagedServiceFake) GetSessionControlSessionByKey(_ context.Context, key string) (mysql.SessionControlSession, error) {
	f.sessionKey = key
	return mysql.SessionControlSession{}, nil
}
func (f *tenantManagedServiceFake) RecoverSessionControlStop(context.Context, uint64, string) (mysql.SessionControlStopRecovery, error) {
	return mysql.SessionControlStopRecovery{}, nil
}
func (f *tenantManagedServiceFake) ListLatestAgentTasksForSessions(_ context.Context, ids []uint64) ([]mysql.AgentTask, error) {
	f.latestSessionIDs = append([]uint64(nil), ids...)
	return nil, nil
}
func (f *tenantManagedServiceFake) CreateAgentTask(_ context.Context, input agenttasks.TaskInput) (uint64, error) {
	f.createTaskCalls++
	f.task = input
	return 1, nil
}
func (f *tenantManagedServiceFake) CancelAgentTaskIfRunning(_ context.Context, taskID uint64, resultJSON string) (bool, error) {
	f.cancelTaskCalls++
	f.cancelTaskID, f.cancelResultJSON = taskID, resultJSON
	return true, nil
}
func (f *tenantManagedServiceFake) CancelAgentTaskForSessionControl(_ context.Context, input tenant.SessionControlStopRequest) (mysql.SessionControlStopResult, error) {
	f.cancelTaskCalls++
	f.cancelTaskID, f.cancelResultJSON = input.TaskID, input.ResultJSON
	return mysql.SessionControlStopResult{Cancelled: true, EventID: 1}, nil
}
func (f *tenantManagedServiceFake) ListAgentTaskEventsForTasksComplete(_ context.Context, ids []uint64) ([]mysql.AgentTaskEvent, error) {
	f.eventTaskIDs = append([]uint64(nil), ids...)
	return nil, nil
}
func (f *tenantManagedServiceFake) ListSessionLinks(_ context.Context, sessionID uint64, limit int) ([]mysql.SessionLink, error) {
	f.linkSessionID, f.linkLimit = sessionID, limit
	return nil, nil
}

func (f *tenantManagedServiceFake) GetSessionLink(context.Context, uint64, string, string, string) (mysql.SessionLink, error) {
	return mysql.SessionLink{}, nil
}

func (f *tenantManagedServiceFake) GetMessage(_ context.Context, sessionID, messageID uint64) (mysql.Message, error) {
	f.messageSessionID, f.messageID = sessionID, messageID
	if f.messageRow.ID != messageID || f.messageRow.SessionID != sessionID {
		return mysql.Message{}, mysql.ErrNotFound
	}
	return f.messageRow, nil
}

func (f *tenantManagedServiceFake) ListRecentMessages(context.Context, uint64, int) ([]mysql.Message, error) {
	return nil, nil
}

func (f *tenantManagedServiceFake) GetAgentTask(_ context.Context, taskID uint64) (mysql.AgentTask, error) {
	f.taskID = taskID
	if f.taskRow.ID != taskID {
		return mysql.AgentTask{}, mysql.ErrNotFound
	}
	return f.taskRow, nil
}

func (f *tenantManagedServiceFake) GetAgentTaskEvent(_ context.Context, eventID uint64) (mysql.AgentTaskEvent, error) {
	f.eventID = eventID
	if f.eventRow.ID != eventID {
		return mysql.AgentTaskEvent{}, mysql.ErrNotFound
	}
	return f.eventRow, nil
}

func (f *tenantManagedServiceFake) CreateHandoffBatch(context.Context, agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error) {
	return agenttasks.HandoffBatchResult{}, nil
}
