package sessioncontrol

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestManagedRejectsNonTenantRefBeforeStore(t *testing.T) {
	store := &managedStoreFake{}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	_, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: localRef("local-1")})
	assertServiceErrorCode(t, err, CodeForbidden)
	if store.getSessionByKeyCalls != 0 {
		t.Fatalf("GetSessionByKey was called %d times", store.getSessionByKeyCalls)
	}
}

func TestManagedCreateUpsertsAndReturnsScopedReadback(t *testing.T) {
	store := &managedStoreFake{}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	request := CreateRequest{Context: managedRequestContext(), SessionKey: "created-1", Title: "Created", Model: "model-a", CWD: "/repo", ReplayIdentity: managedOperationIdentity(t, OperationCreate, "create-1")}
	snapshot, err := adapter.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if store.upserted.SessionKey != request.SessionKey || store.upserted.Title != request.Title || store.upserted.MetadataJSON == "" || store.upsertScope != request.Context {
		t.Fatalf("upsert = %+v scope=%+v", store.upserted, store.upsertScope)
	}
	if snapshot.Ref != tenantRef("created-1") || snapshot.ID == 0 || snapshot.Model != "model-a" || snapshot.CWD != "/repo" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if store.getKey != "created-1" {
		t.Fatalf("Create did not read back the exact stored session: %q", store.getKey)
	}
}

func TestManagedCreateStartsInitialInstructionWithDerivedIdentity(t *testing.T) {
	store := &managedStoreFake{}
	dispatcher := &managedDispatcherFake{startedID: 77}
	adapter := NewManagedAdapter(store, dispatcher)
	identity := managedOperationIdentity(t, OperationCreate, "create-initial")
	_, err := adapter.Create(context.Background(), CreateRequest{
		Context: managedRequestContext(), SessionKey: "created-1", Title: "Created",
		InitialText: "start here", ReplayIdentity: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dispatcher.startedTask.Prompt != "start here" || dispatcher.startedTask.IdempotencyKey != identity.OperationID || dispatcher.startedTask.MetadataJSON == "" {
		t.Fatalf("started task = %#v", dispatcher.startedTask)
	}
}

func TestManagedListAndGetMapLatestTaskPermissionAndLinks(t *testing.T) {
	now := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "alpha", Title: "Alpha", Status: managedLifecycleIdle, Model: "model-a", CWD: "/repo", StartedAt: now}},
		tasks: []mysqlstore.AgentTask{
			{ID: 11, ParentSessionID: 7, Status: agenttasks.StatusCompleted, StartedAt: now.Add(time.Minute)},
			{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusRunning, StartedAt: now.Add(2 * time.Minute)},
		},
		events: []mysqlstore.AgentTaskEvent{{ID: 1, TaskID: 12, EventType: agenttasks.EventPermissionRequest, CreatedAt: now.Add(3 * time.Minute)}},
		links:  []mysqlstore.SessionLink{{ID: 31, TargetSessionID: 7, SourceKind: string(SourceLocal), SourceSessionKey: "local-1", RelationType: "attached", CreatedAt: now.Add(4 * time.Minute)}},
	}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	items, err := adapter.List(context.Background(), ListRequest{Context: managedRequestContext(), Source: SourceTenant})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != StatusWaitingPermission || items[0].ActiveRunID != 12 || items[0].ID != 7 {
		t.Fatalf("list = %+v", items)
	}
	if store.listLatestTasksCalls != 1 || store.listEventsCalls != 1 || !reflect.DeepEqual(store.latestTaskSessionIDs, []uint64{7}) {
		t.Fatalf("List calls latest_tasks=%d events=%d sessions=%v, want one exact batch", store.listLatestTasksCalls, store.listEventsCalls, store.latestTaskSessionIDs)
	}
	assertManagedReadScopes(t, store, managedRequestContext())
	detail, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), IncludeLinks: true})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != StatusWaitingPermission || len(detail.Links) != 1 || detail.Links[0].Source != localRef("local-1") {
		t.Fatalf("detail = %+v", detail)
	}
	if store.linkScope != managedRequestContext() {
		t.Fatalf("link scope = %+v", store.linkScope)
	}
}

func TestManagedPermissionWindowIncludesLaterResolution(t *testing.T) {
	now := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "alpha", Status: managedLifecycleIdle, StartedAt: now}},
		tasks:    []mysqlstore.AgentTask{{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusRunning, StartedAt: now}},
		events: []mysqlstore.AgentTaskEvent{
			{ID: 2, TaskID: 12, EventType: agenttasks.EventPermissionResolved, CreatedAt: now.Add(2 * time.Minute)},
			{ID: 1, TaskID: 12, EventType: agenttasks.EventPermissionRequest, CreatedAt: now.Add(time.Minute)},
		},
	}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	snapshot, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusRunning {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusRunning)
	}
	assertManagedReadScopes(t, store, managedRequestContext())
}

func TestManagedSendQueuesRunningTask(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusRunning)
	dispatcher := &managedDispatcherFake{queuedID: 88}
	adapter := NewManagedAdapter(store, dispatcher)
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "private input", IdempotencyKey: "send-1", ReplayIdentity: managedOperationIdentity(t, OperationSend, "send-1")}
	result, err := adapter.Send(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 88 || dispatcher.queuedTaskID != 12 || dispatcher.startedTask.ParentSessionID != 0 {
		t.Fatalf("result=%+v dispatcher=%+v", result, dispatcher)
	}
}

func TestManagedTerminalSessionWithPendingInputStaysQueuedAndPreservesFIFOBase(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "7", BaseTaskID: 12}
	if _, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "older", Content: "older"}); err != nil {
		t.Fatal(err)
	}
	dispatcher := &managedDispatcherFake{queuedID: 12}
	adapter := NewManagedAdapter(store, dispatcher)
	adapter.SetPendingInputs(queue)
	snapshot, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	if err != nil || snapshot.Status != StatusQueued {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "newer", ReplayIdentity: managedOperationIdentity(t, OperationSend, "newer")}
	result, err := adapter.Send(context.Background(), request)
	if err != nil || result.RunID != 12 || dispatcher.queuedTaskID != 12 || dispatcher.startedTask.ParentSessionID != 0 {
		t.Fatalf("result=%+v dispatcher=%+v err=%v", result, dispatcher, err)
	}
}

func TestManagedListBatchesPendingStateAcrossSessions(t *testing.T) {
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "alpha"}, {ID: 8, SessionKey: "beta"}},
		tasks:    []mysqlstore.AgentTask{{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusCompleted}, {ID: 13, ParentSessionID: 8, Status: agenttasks.StatusFailed}},
	}
	queue := &countingPendingQueue{MemoryQueue: pendinginput.NewMemoryQueue()}
	if _, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "7", BaseTaskID: 12}, ClientInputID: "queued", Content: "queued"}); err != nil {
		t.Fatal(err)
	}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	adapter.SetPendingInputs(queue)
	items, err := adapter.List(context.Background(), ListRequest{Context: managedRequestContext(), Source: SourceTenant})
	if err != nil || len(items) != 2 || items[0].Status != StatusQueued || queue.batchCalls != 1 || queue.listCalls != 0 {
		t.Fatalf("items=%+v batch_calls=%d list_calls=%d err=%v", items, queue.batchCalls, queue.listCalls, err)
	}
}

func TestManagedListSkipsEventHistoryForTerminalTaskSummaries(t *testing.T) {
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "alpha", Status: managedLifecycleIdle}},
		tasks:    []mysqlstore.AgentTask{{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusCompleted}},
	}
	items, err := NewManagedAdapter(store, &managedDispatcherFake{}).List(context.Background(), ListRequest{
		Context: managedRequestContext(), Source: SourceTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != StatusCompleted {
		t.Fatalf("summaries = %+v, want one completed session", items)
	}
	if store.listEventsCalls != 0 {
		t.Fatalf("terminal summary loaded event history %d times, want 0", store.listEventsCalls)
	}
}

func TestManagedSnapshotProjectsOutstandingUserQuestionsByRequestID(t *testing.T) {
	task := &mysqlstore.AgentTask{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusRunning}
	events := map[uint64][]mysqlstore.AgentTaskEvent{12: {
		{ID: 1, TaskID: 12, EventType: agenttasks.EventUserQuestionRequest, PayloadJSON: `{"request_id":"question-1"}`},
		{ID: 2, TaskID: 12, EventType: agenttasks.EventUserQuestionRequest, PayloadJSON: `{"request_id":"question-2"}`},
		{ID: 3, TaskID: 12, EventType: agenttasks.EventUserQuestionResolved, PayloadJSON: `{"request_id":"question-1","status":"answered"}`},
	}}
	snapshot := managedSnapshot(mysqlstore.Session{ID: 7, SessionKey: "alpha"}, task, events, 0)
	if snapshot.Status != StatusWaitingInput {
		t.Fatalf("status=%q, want %q", snapshot.Status, StatusWaitingInput)
	}

	for index, status := range []string{"cancelled", "expired"} {
		t.Run(status, func(t *testing.T) {
			resolved := append([]mysqlstore.AgentTaskEvent(nil), events[12]...)
			resolved = append(resolved, mysqlstore.AgentTaskEvent{ID: uint64(4 + index), TaskID: 12, EventType: agenttasks.EventUserQuestionResolved, PayloadJSON: `{"request_id":"question-2","status":"` + status + `"}`})
			got := managedSnapshot(mysqlstore.Session{ID: 7, SessionKey: "alpha"}, task, map[uint64][]mysqlstore.AgentTaskEvent{12: resolved}, 0)
			if got.Status != StatusRunning {
				t.Fatalf("status=%q, want %q", got.Status, StatusRunning)
			}
		})
	}

	terminal := *task
	terminal.Status = agenttasks.StatusCompleted
	snapshot = managedSnapshot(mysqlstore.Session{ID: 7, SessionKey: "alpha"}, &terminal, events, 0)
	if snapshot.Status != StatusCompleted {
		t.Fatalf("terminal status=%q, want %q", snapshot.Status, StatusCompleted)
	}
}

type countingPendingQueue struct {
	*pendinginput.MemoryQueue
	batchCalls int
	listCalls  int
}

func (q *countingPendingQueue) List(ctx context.Context, scope pendinginput.Scope) ([]pendinginput.PendingInput, error) {
	q.listCalls++
	return q.MemoryQueue.List(ctx, scope)
}

func (q *countingPendingQueue) ListActiveSessionStates(ctx context.Context, tenantID, userID uint64, sessionIDs []string) (map[string]pendinginput.ActiveSessionState, error) {
	q.batchCalls++
	return q.MemoryQueue.ListActiveSessionStates(ctx, tenantID, userID, sessionIDs)
}

func TestManagedStopCancelsRegisteredReadyRunBeforePersistence(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusReady)
	store.cancelled = true
	dispatcher := &managedDispatcherFake{cancelled: true}
	_, err := NewManagedAdapter(store, dispatcher).Stop(context.Background(), StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), ReplayIdentity: managedOperationIdentity(t, OperationStop, "stop-ready")})
	if err != nil {
		t.Fatal(err)
	}
	if dispatcher.cancelledTaskID != 12 || dispatcher.triggeredTaskID != 12 || store.cancelledTaskID != 12 {
		t.Fatalf("dispatcher=%+v store=%+v", dispatcher, store)
	}
}

func TestManagedSendStartsIdleTaskWithIdempotencyKey(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	dispatcher := &managedDispatcherFake{startedID: 99}
	adapter := NewManagedAdapter(store, dispatcher)
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "start this", IdempotencyKey: "send-1", ReplayIdentity: managedOperationIdentity(t, OperationSend, "send-1")}
	result, err := adapter.Send(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 99 || dispatcher.queuedTaskID != 0 || dispatcher.startedTask.TenantID != 7 || dispatcher.startedTask.UserID != 11 || dispatcher.startedTask.ParentSessionID != 7 || dispatcher.startedTask.IdempotencyKey != request.ReplayIdentity.KeyHash || !strings.Contains(dispatcher.startedTask.MetadataJSON, `"cwd":"/repo"`) || dispatcher.startedTask.Prompt != "start this" {
		t.Fatalf("result=%+v started=%+v", result, dispatcher.startedTask)
	}
}

func TestManagedSendAttachesSourcesBeforeLaunchingPreparedRun(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	dispatcher := &managedDispatcherFake{startedID: 99}
	handoff := &managedHandoffFake{}
	adapter := NewManagedAdapter(store, dispatcher)
	adapter.SetHandoff(handoff)
	request := SendRequest{
		Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue",
		SourceRefs:     []SessionRef{tenantRef("beta"), localRef("local-1")},
		IdempotencyKey: "send-with-context", ReplayIdentity: managedOperationIdentity(t, OperationSend, "send-with-context"),
	}
	result, err := adapter.Send(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 99 || dispatcher.preparedID != 99 || dispatcher.launchedTaskID != 99 || handoff.request.TargetTaskID != 99 || len(handoff.request.Sources) != 2 {
		t.Fatalf("result=%#v dispatcher=%#v handoff=%#v", result, dispatcher, handoff)
	}
	if got := strings.Join(append(append([]string{}, dispatcher.calls...), handoff.calls...), ","); !strings.Contains(got, "prepare") || !strings.Contains(got, "launch") {
		t.Fatalf("calls=%q", got)
	}
}

func TestManagedSendTerminalizesPreparedRunWhenAttachFails(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	dispatcher := &managedDispatcherFake{startedID: 99, onFail: func() {
		store.tasks = []mysqlstore.AgentTask{{ID: 99, ParentSessionID: 7, Status: agenttasks.StatusFailed, StartedAt: time.Now()}}
	}}
	handoffErr := &ServiceError{Code: CodeBudgetExceeded, Message: "handoff package exceeds its context budget"}
	handoff := &managedHandoffFake{err: handoffErr}
	adapter := NewManagedAdapter(store, dispatcher)
	adapter.SetHandoff(handoff)
	request := SendRequest{
		Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue",
		SourceRefs: []SessionRef{tenantRef("beta")}, ReplayIdentity: managedOperationIdentity(t, OperationSend, "attach-failure"),
	}

	result, err := adapter.Send(context.Background(), request)
	if !errors.Is(err, handoffErr) || result.RunID != 99 || result.ErrorCode != string(CodeBudgetExceeded) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if dispatcher.failedTaskID != 99 || dispatcher.failure.ErrorCode != string(CodeBudgetExceeded) || dispatcher.failure.TraceID != request.Context.TraceID || dispatcher.launchedTaskID != 0 || !dispatcher.failureHasDeadline {
		t.Fatalf("prepared failure was not terminalized: %+v", dispatcher)
	}
	dispatcher.startedID = 100
	next := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "try without context", ReplayIdentity: managedOperationIdentity(t, OperationSend, "next-send")}
	result, err = adapter.Send(context.Background(), next)
	if err != nil || result.RunID != 100 || dispatcher.startedTask.Prompt != next.Content {
		t.Fatalf("next send remained blocked: result=%+v dispatcher=%+v err=%v", result, dispatcher, err)
	}
}

func TestManagedSendPreservesAttachErrorWhenPreparedCleanupFails(t *testing.T) {
	cleanupErr := errors.New("prepared cleanup unavailable")
	dispatcher := &managedDispatcherFake{startedID: 99, failErr: cleanupErr}
	handoffErr := &ServiceError{Code: CodeBudgetExceeded, Message: "handoff package exceeds its context budget"}
	adapter := NewManagedAdapter(managedStoreWithTask(agenttasks.StatusCompleted), dispatcher)
	adapter.SetHandoff(&managedHandoffFake{err: handoffErr})
	request := SendRequest{
		Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue",
		SourceRefs: []SessionRef{tenantRef("beta")}, ReplayIdentity: managedOperationIdentity(t, OperationSend, "cleanup-failure"),
	}

	result, err := adapter.Send(context.Background(), request)
	assertServiceErrorCode(t, err, CodeBudgetExceeded)
	if result.RunID != 99 || result.ErrorCode != string(CodeBudgetExceeded) || !errors.Is(err, cleanupErr) || !dispatcher.failureHasDeadline {
		t.Fatalf("result=%+v err=%v dispatcher=%+v", result, err, dispatcher)
	}
}

func TestManagedSendTerminalizesPreparedRunWhenLaunchFails(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	launchErr := errors.New("launch unavailable")
	dispatcher := &managedDispatcherFake{startedID: 99, launchErr: launchErr}
	adapter := NewManagedAdapter(store, dispatcher)
	adapter.SetHandoff(&managedHandoffFake{})
	request := SendRequest{
		Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue",
		SourceRefs: []SessionRef{tenantRef("beta")}, ReplayIdentity: managedOperationIdentity(t, OperationSend, "launch-failure"),
	}

	result, err := adapter.Send(context.Background(), request)
	if err == nil || result.RunID != 99 || result.ErrorCode != string(CodeInternal) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if dispatcher.failedTaskID != 99 || dispatcher.failure.ErrorCode != string(CodeInternal) {
		t.Fatalf("launch failure was not terminalized: %+v", dispatcher)
	}
}

func TestManagedStopCancelsRunningTaskInTenantScope(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusRunning)
	store.cancelled = true
	dispatcher := &managedDispatcherFake{cancelled: true}
	adapter := NewManagedAdapter(store, dispatcher)
	result, err := adapter.Stop(context.Background(), StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), ReplayIdentity: managedOperationIdentity(t, OperationStop, "stop-1")})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 12 || dispatcher.cancelledTaskID != 12 || store.cancelledTaskID != 12 || store.cancelScope != managedRequestContext() {
		t.Fatalf("result=%+v dispatcher=%+v store=%+v", result, dispatcher, store)
	}
}

func TestManagedStopTerminalTaskIsNoOp(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusCompleted)
	dispatcher := &managedDispatcherFake{}
	adapter := NewManagedAdapter(store, dispatcher)
	result, err := adapter.Stop(context.Background(), StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), ReplayIdentity: managedOperationIdentity(t, OperationStop, "stop-1")})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 0 || dispatcher.cancelledTaskID != 0 || store.cancelledTaskID != 0 || result.Session.Status != StatusCompleted {
		t.Fatalf("result=%+v dispatcher=%+v store=%+v", result, dispatcher, store)
	}
}

func TestManagedGetReturnsTenantScopedNotFound(t *testing.T) {
	store := &managedStoreFake{getErr: mysqlstore.ErrNotFound}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	_, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("missing")})
	assertServiceErrorCode(t, err, CodeNotFound)
	if store.getScope != managedRequestContext() {
		t.Fatalf("scope = %+v", store.getScope)
	}
}

func TestManagedGetUsesExactKeyWithoutSessionListLimit(t *testing.T) {
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 1, SessionKey: "old"}},
		byKey:    map[string]mysqlstore.Session{"target": {ID: 999, SessionKey: "target", Status: managedLifecycleIdle}},
	}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	snapshot, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("target")})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != 999 || store.getKey != "target" || store.listSessionsCalls != 0 {
		t.Fatalf("snapshot=%+v key=%q list_calls=%d", snapshot, store.getKey, store.listSessionsCalls)
	}
}

func TestManagedReadHandoffTargetUsesExactScopedReadyTask(t *testing.T) {
	store := &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "target", Status: managedLifecycleIdle}},
		tasks:    []mysqlstore.AgentTask{{ID: 12, ParentSessionID: 7, Status: agenttasks.StatusReady}},
	}
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})
	target, err := adapter.ReadHandoffTarget(context.Background(), managedRequestContext(), tenantRef("target"), 12)
	if err != nil {
		t.Fatalf("ReadHandoffTarget() error = %v", err)
	}
	if target.SessionID != 7 || target.TaskID != 12 || target.TaskStatus != agenttasks.StatusReady || store.getScope != managedRequestContext() {
		t.Fatalf("target = %+v scope=%+v", target, store.getScope)
	}
	for name, task := range map[string]mysqlstore.AgentTask{
		"foreign session": {ID: 12, ParentSessionID: 8, Status: agenttasks.StatusReady},
		"running":         {ID: 12, ParentSessionID: 7, Status: agenttasks.StatusRunning},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := &managedStoreFake{sessions: []mysqlstore.Session{{ID: 7, SessionKey: "target"}}, tasks: []mysqlstore.AgentTask{task}}
			_, readErr := NewManagedAdapter(fixture, &managedDispatcherFake{}).ReadHandoffTarget(context.Background(), managedRequestContext(), tenantRef("target"), 12)
			if name == "foreign session" {
				assertServiceErrorCode(t, readErr, CodeNotFound)
				return
			}
			if readErr != nil {
				t.Fatalf("ReadHandoffTarget() error = %v", readErr)
			}
		})
	}
}

func TestManagedStopCancellationRaceDoesNotOverwriteTerminalTask(t *testing.T) {
	for name, fixture := range map[string]struct {
		runtimeCancelled bool
		storeCancelled   bool
		wantRunID        uint64
	}{
		"runtime cancelled before terminal persistence": {runtimeCancelled: true, storeCancelled: false, wantRunID: 12},
		"runtime false and terminal store":              {runtimeCancelled: false, storeCancelled: false, wantRunID: 0},
		"runtime false and store still running":         {runtimeCancelled: false, storeCancelled: true, wantRunID: 12},
	} {
		t.Run(name, func(t *testing.T) {
			store := managedStoreWithTask(agenttasks.StatusRunning)
			store.cancelled = fixture.storeCancelled
			dispatcher := &managedDispatcherFake{cancelled: fixture.runtimeCancelled}
			adapter := NewManagedAdapter(store, dispatcher)
			result, err := adapter.Stop(context.Background(), StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), ReplayIdentity: managedOperationIdentity(t, OperationStop, "stop-race")})
			if err != nil {
				t.Fatal(err)
			}
			if result.RunID != fixture.wantRunID || store.cancelledTaskID != 12 {
				t.Fatalf("result=%+v persisted_task=%d", result, store.cancelledTaskID)
			}
		})
	}
}

func TestManagedSnapshotsDoNotExposeMessageContent(t *testing.T) {
	if _, found := reflect.TypeOf(SessionSnapshot{}).FieldByName("Content"); found {
		t.Fatal("session snapshot must not expose message content")
	}
}

func TestManagedCompleteEventOverflowIsTypedWithoutStorageDetails(t *testing.T) {
	store := managedStoreWithTask(agenttasks.StatusRunning)
	store.eventsErr = mysqlstore.ErrTooManyAgentTaskEvents
	adapter := NewManagedAdapter(store, &managedDispatcherFake{})

	_, err := adapter.Get(context.Background(), GetRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	assertServiceErrorCode(t, err, CodeInternal)
	if !errors.Is(err, mysqlstore.ErrTooManyAgentTaskEvents) {
		t.Fatalf("error cause = %v, want ErrTooManyAgentTaskEvents", err)
	}
	if strings.Contains(err.Error(), mysqlstore.ErrTooManyAgentTaskEvents.Error()) {
		t.Fatalf("error leaked storage detail: %v", err)
	}
	if code := serviceErrorCode(err); code != string(CodeInternal) {
		t.Fatalf("serviceErrorCode = %q, want %q", code, CodeInternal)
	}
}

func TestManagedRawDependencyErrorsAreTypedAndStable(t *testing.T) {
	storageErr := errors.New("storage credentials leaked")
	dispatchErr := errors.New("dispatcher credentials leaked")
	tests := []struct {
		name      string
		invoke    func() error
		cause     error
		forbidden string
	}{
		{
			name: "store",
			invoke: func() error {
				store := &managedStoreFake{listSessionsErr: storageErr}
				_, err := NewManagedAdapter(store, &managedDispatcherFake{}).List(context.Background(), ListRequest{Context: managedRequestContext(), Source: SourceTenant})
				return err
			},
			cause:     storageErr,
			forbidden: "credentials leaked",
		},
		{
			name: "dispatcher",
			invoke: func() error {
				store := managedStoreWithTask(agenttasks.StatusRunning)
				dispatcher := &managedDispatcherFake{queueErr: dispatchErr}
				_, err := NewManagedAdapter(store, dispatcher).Send(context.Background(), SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
				return err
			},
			cause:     dispatchErr,
			forbidden: "credentials leaked",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.invoke()
			assertServiceErrorCode(t, err, CodeInternal)
			if !errors.Is(err, test.cause) {
				t.Fatalf("error cause = %v, want %v", err, test.cause)
			}
			if strings.Contains(err.Error(), test.forbidden) {
				t.Fatalf("error leaked dependency detail: %v", err)
			}
			if code := serviceErrorCode(err); code != string(CodeInternal) {
				t.Fatalf("serviceErrorCode = %q, want %q", code, CodeInternal)
			}
		})
	}
}

func TestManagedSendMissingStoreReturnsTypedError(t *testing.T) {
	_, err := NewManagedAdapter(nil, &managedDispatcherFake{}).Send(context.Background(), SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha")})
	assertServiceErrorCode(t, err, CodeInternal)
	if code := serviceErrorCode(err); code != string(CodeInternal) {
		t.Fatalf("serviceErrorCode = %q, want %q", code, CodeInternal)
	}
}

func managedRequestContext() RequestContext {
	return RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11, TraceID: "trace-1"}
}

func managedOperationIdentity(t *testing.T, operation Operation, key string) OperationIdentity {
	t.Helper()
	identity, err := newOperationIdentity(operation, managedRequestContext(), key, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func managedStoreWithTask(status string) *managedStoreFake {
	now := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)
	return &managedStoreFake{
		sessions: []mysqlstore.Session{{ID: 7, SessionKey: "alpha", Status: managedLifecycleIdle, Model: "model-a", CWD: "/repo", StartedAt: now}},
		tasks:    []mysqlstore.AgentTask{{ID: 12, ParentSessionID: 7, Status: status, StartedAt: now.Add(time.Minute)}},
	}
}

type managedStoreFake struct {
	sessions        []mysqlstore.Session
	tasks           []mysqlstore.AgentTask
	events          []mysqlstore.AgentTaskEvent
	eventsErr       error
	links           []mysqlstore.SessionLink
	byKey           map[string]mysqlstore.Session
	getErr          error
	listSessionsErr error

	upserted             mysqlstore.SessionInput
	createdMetadata      string
	upsertScope          RequestContext
	listScope            RequestContext
	getScope             RequestContext
	getKey               string
	getSessionByKeyCalls int
	linkScope            RequestContext
	cancelScope          RequestContext
	cancelledTaskID      uint64
	cancelled            bool
	stopInput            mysqlstore.SessionControlStopInput
	listSessionsCalls    int
	listLatestTasksCalls int
	latestTaskSessionIDs []uint64
	latestTaskScope      RequestContext
	listEventsCalls      int
	eventScope           RequestContext
}

func (f *managedStoreFake) UpsertSession(_ context.Context, scope RequestContext, input mysqlstore.SessionInput) (uint64, error) {
	f.upsertScope, f.upserted = scope, input
	for _, item := range f.sessions {
		if item.SessionKey == input.SessionKey {
			return item.ID, nil
		}
	}
	id := uint64(len(f.sessions) + 1)
	f.sessions = append(f.sessions, mysqlstore.Session{ID: id, SessionKey: input.SessionKey, Title: input.Title, Status: input.Status, Model: input.Model, CWD: input.CWD, StartedAt: time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)})
	return id, nil
}

func (f *managedStoreFake) CreateSessionControlSession(_ context.Context, scope RequestContext, input mysqlstore.SessionControlCreateInput) (mysqlstore.SessionControlCreateResult, error) {
	f.upsertScope, f.upserted = scope, input.Session
	for _, item := range f.sessions {
		if item.SessionKey == input.Session.SessionKey {
			return mysqlstore.SessionControlCreateResult{Session: mysqlstore.SessionControlSession{Session: item, MetadataJSON: f.createdMetadata}}, nil
		}
	}
	id := uint64(len(f.sessions) + 1)
	item := mysqlstore.Session{ID: id, SessionKey: input.Session.SessionKey, Title: input.Session.Title, Status: input.Session.Status, Model: input.Session.Model, CWD: input.Session.CWD, StartedAt: time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)}
	f.sessions = append(f.sessions, item)
	f.createdMetadata = input.Session.MetadataJSON
	return mysqlstore.SessionControlCreateResult{Session: mysqlstore.SessionControlSession{Session: item, MetadataJSON: f.createdMetadata}, Created: true}, nil
}

func (f *managedStoreFake) ListSessions(_ context.Context, scope RequestContext, _ int) ([]mysqlstore.SessionControlSession, error) {
	f.listScope, f.listSessionsCalls = scope, f.listSessionsCalls+1
	if f.listSessionsErr != nil {
		return nil, f.listSessionsErr
	}
	items := make([]mysqlstore.SessionControlSession, 0, len(f.sessions))
	for _, item := range f.sessions {
		items = append(items, mysqlstore.SessionControlSession{Session: item, MetadataJSON: f.createdMetadata})
	}
	return items, nil
}

func (f *managedStoreFake) GetSessionControlSessionByKey(_ context.Context, scope RequestContext, key string) (mysqlstore.SessionControlSession, error) {
	f.getSessionByKeyCalls++
	f.getScope, f.getKey = scope, key
	if f.getErr != nil {
		return mysqlstore.SessionControlSession{}, f.getErr
	}
	if item, ok := f.byKey[key]; ok {
		return mysqlstore.SessionControlSession{Session: item, MetadataJSON: f.createdMetadata}, nil
	}
	for _, item := range f.sessions {
		if item.SessionKey == key {
			return mysqlstore.SessionControlSession{Session: item, MetadataJSON: f.createdMetadata}, nil
		}
	}
	return mysqlstore.SessionControlSession{}, mysqlstore.ErrNotFound
}

func (f *managedStoreFake) ListLatestAgentTasksForSessions(_ context.Context, scope RequestContext, sessionIDs []uint64) ([]mysqlstore.AgentTask, error) {
	f.listLatestTasksCalls++
	f.latestTaskSessionIDs = append([]uint64(nil), sessionIDs...)
	f.latestTaskScope = scope
	return append([]mysqlstore.AgentTask(nil), f.tasks...), nil
}

func (f *managedStoreFake) GetAgentTask(_ context.Context, scope RequestContext, taskID uint64) (mysqlstore.AgentTask, error) {
	f.latestTaskScope = scope
	for _, task := range f.tasks {
		if task.ID == taskID {
			return task, nil
		}
	}
	return mysqlstore.AgentTask{}, mysqlstore.ErrNotFound
}

func (f *managedStoreFake) GetTaskEvent(_ context.Context, scope RequestContext, _ uint64, eventID uint64) (mysqlstore.AgentTaskEvent, error) {
	f.eventScope = scope
	for _, event := range f.events {
		if event.ID == eventID {
			return event, nil
		}
	}
	return mysqlstore.AgentTaskEvent{}, mysqlstore.ErrNotFound
}

func (f *managedStoreFake) CreateAgentTask(context.Context, RequestContext, agenttasks.TaskInput) (uint64, error) {
	return 0, nil
}

func (f *managedStoreFake) CancelAgentTaskIfRunning(_ context.Context, scope RequestContext, taskID uint64, _ string) (bool, error) {
	f.cancelScope, f.cancelledTaskID = scope, taskID
	return f.cancelled, nil
}

func (f *managedStoreFake) CancelAgentTaskForSessionControl(_ context.Context, scope RequestContext, input mysqlstore.SessionControlStopInput) (mysqlstore.SessionControlStopResult, error) {
	f.cancelScope, f.cancelledTaskID, f.stopInput = scope, input.TaskID, input
	return mysqlstore.SessionControlStopResult{Cancelled: f.cancelled, EventID: 1}, nil
}

func (f *managedStoreFake) ListAgentTaskEventsForTasksComplete(_ context.Context, scope RequestContext, _ []uint64) ([]mysqlstore.AgentTaskEvent, error) {
	f.listEventsCalls++
	f.eventScope = scope
	if f.eventsErr != nil {
		return nil, f.eventsErr
	}
	return append([]mysqlstore.AgentTaskEvent(nil), f.events...), nil
}

func assertManagedReadScopes(t *testing.T, store *managedStoreFake, want RequestContext) {
	t.Helper()
	if store.latestTaskScope != want || store.eventScope != want {
		t.Fatalf("latest task scope=%+v event scope=%+v, want=%+v", store.latestTaskScope, store.eventScope, want)
	}
}

func (f *managedStoreFake) ListSessionLinks(_ context.Context, scope RequestContext, _ uint64, _ int) ([]mysqlstore.SessionLink, error) {
	f.linkScope = scope
	return append([]mysqlstore.SessionLink(nil), f.links...), nil
}

func (f *managedStoreFake) GetSessionLink(_ context.Context, scope RequestContext, targetSessionID uint64, source SessionRef, relationType string) (mysqlstore.SessionLink, error) {
	f.linkScope = scope
	for _, link := range f.links {
		if link.TargetSessionID == targetSessionID && link.SourceKind == string(source.Source) && link.SourceSessionKey == source.Key && link.RelationType == relationType {
			return link, nil
		}
	}
	return mysqlstore.SessionLink{}, mysqlstore.ErrNotFound
}

type managedDispatcherFake struct {
	queuedID           uint64
	startedID          uint64
	queuedTaskID       uint64
	startedTask        agenttasks.TaskInput
	startedRequest     SendRequest
	cancelledTaskID    uint64
	cancelled          bool
	queueErr           error
	preparedID         uint64
	launchedTaskID     uint64
	triggeredTaskID    uint64
	failedTaskID       uint64
	failure            PreparedRunFailure
	launchErr          error
	failErr            error
	failureHasDeadline bool
	onFail             func()
	calls              []string
}

func (f *managedDispatcherFake) QueueInput(_ context.Context, taskID uint64, _ SendRequest) (uint64, error) {
	f.queuedTaskID = taskID
	if f.queueErr != nil {
		return 0, f.queueErr
	}
	return f.queuedID, nil
}

func (f *managedDispatcherFake) StartRun(_ context.Context, _ uint64, input agenttasks.TaskInput, request SendRequest) (uint64, error) {
	f.startedTask = input
	f.startedRequest = request
	return f.startedID, nil
}

func (f *managedDispatcherFake) PrepareRun(_ context.Context, _ uint64, input agenttasks.TaskInput, _ SendRequest) (uint64, error) {
	f.calls = append(f.calls, "prepare")
	f.startedTask = input
	f.preparedID = f.startedID
	return f.startedID, nil
}

func (f *managedDispatcherFake) LaunchRun(_ context.Context, taskID uint64, _ SendRequest) error {
	f.calls = append(f.calls, "launch")
	f.launchedTaskID = taskID
	return f.launchErr
}

func (f *managedDispatcherFake) FailPreparedRun(ctx context.Context, taskID uint64, failure PreparedRunFailure) (bool, error) {
	f.calls = append(f.calls, "fail")
	f.failedTaskID = taskID
	f.failure = failure
	_, f.failureHasDeadline = ctx.Deadline()
	if f.onFail != nil {
		f.onFail()
	}
	return true, f.failErr
}

func (f *managedDispatcherFake) CancelRun(_ context.Context, taskID uint64) (bool, error) {
	f.cancelledTaskID = taskID
	return f.cancelled, nil
}

func (f *managedDispatcherFake) TriggerPending(_ context.Context, taskID uint64) {
	f.triggeredTaskID = taskID
}

type managedHandoffFake struct {
	request AttachRequest
	calls   []string
	err     error
}

func (f *managedHandoffFake) Attach(_ context.Context, request AttachRequest) (HandoffResult, error) {
	f.calls = append(f.calls, "attach")
	f.request = request
	return HandoffResult{TargetTaskID: request.TargetTaskID}, f.err
}
func (*managedHandoffFake) Read(context.Context, HandoffReadRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (*managedHandoffFake) Refresh(context.Context, RefreshHandoffRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
func (*managedHandoffFake) Readback(context.Context, HandoffReadbackRequest) (HandoffResult, error) {
	return HandoffResult{}, nil
}
