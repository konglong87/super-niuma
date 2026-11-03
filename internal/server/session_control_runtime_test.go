package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestNewSessionControlServiceFailsClosedWithoutTenantRuntime(t *testing.T) {
	if _, err := NewSessionControlService(Options{}, nil); err == nil {
		t.Fatal("NewSessionControlService() error = nil")
	}
}

func TestSessionControlQueueInputUsesContinuationBaseAndTriggersCoordinator(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	task := mysqlstore.AgentTask{ID: 52, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusRunning, MetadataJSON: `{"pending_input_base_task_id":51}`}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: task}
	triggered := make(chan pendinginput.Scope, 1)
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}, pendingTrigger: func(_ context.Context, scope pendinginput.Scope) { triggered <- scope }}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Content: "next", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "key", Fingerprint: "fingerprint"}}
	runID, err := dispatcher.QueueInput(context.Background(), task.ID, request)
	if err != nil || runID != 51 {
		t.Fatalf("runID=%d err=%v", runID, err)
	}
	select {
	case scope := <-triggered:
		if scope.BaseTaskID != 51 || scope.SessionID != "41" || scope.TenantID != 7 || scope.UserID != 11 {
			t.Fatalf("scope=%+v", scope)
		}
	case <-time.After(time.Second):
		t.Fatal("coordinator was not triggered")
	}
}

func TestSessionControlQueueInputDisabledReturnsConflictWithoutSideEffects(t *testing.T) {
	ctx := context.Background()
	queue := pendinginput.NewMemoryQueue()
	task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusRunning}
	scope := pendingInputScopeForTask(task)
	if err := queue.SetQueueEnabled(ctx, scope, false); err != nil {
		t.Fatal(err)
	}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: task}
	triggered := false
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}, pendingTrigger: func(context.Context, pendinginput.Scope) { triggered = true }}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Content: "next", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "key", Fingerprint: "fingerprint"}}
	runID, err := dispatcher.QueueInput(ctx, task.ID, request)
	var serviceErr *sessioncontrol.ServiceError
	if runID != 0 || !errors.As(err, &serviceErr) || serviceErr.Code != sessioncontrol.CodeInvalidState || !errors.Is(err, pendinginput.ErrQueueDisabled) {
		t.Fatalf("runID=%d err=%v", runID, err)
	}
	items, listErr := queue.List(ctx, scope)
	if listErr != nil || len(items) != 0 || triggered {
		t.Fatalf("items=%+v err=%v triggered=%v", items, listErr, triggered)
	}
	enabled, enabledErr := queue.QueueEnabled(ctx, scope)
	if enabledErr != nil || enabled {
		t.Fatalf("enabled=%v err=%v", enabled, enabledErr)
	}

	handler := newSessionControlHandler(&sessionControlServiceFake{err: err})
	httpRequest := sessionControlRequest(http.MethodPost, "/tenant/session-control/sessions/tenant/alpha/messages", `{"content":"next"}`)
	httpRequest.Header.Set("Idempotency-Key", "key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusConflict || decodeSessionControlResponse(t, recorder)["code"] != string(sessioncontrol.CodeInvalidState) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	if err := queue.SetQueueEnabled(ctx, scope, true); err != nil {
		t.Fatal(err)
	}
	if runID, err := dispatcher.QueueInput(ctx, task.ID, request); err != nil || runID != task.ID || !triggered {
		t.Fatalf("retry runID=%d err=%v triggered=%v", runID, err, triggered)
	}
	items, err = queue.List(ctx, scope)
	if err != nil || len(items) != 1 || items[0].Content != request.Content {
		t.Fatalf("retry items=%+v err=%v", items, err)
	}
}

func TestSessionControlQueueInputPreservesWrappedQueueErrorSemantics(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
		code  sessioncontrol.ServiceErrorCode
	}{
		{name: "disabled", cause: pendinginput.ErrQueueDisabled, code: sessioncontrol.CodeInvalidState},
		{name: "idempotency conflict", cause: pendinginput.ErrConflict, code: sessioncontrol.CodeIdempotencyConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusRunning}
			tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: task}
			queue := &sessionControlAddErrorQueue{Queue: pendinginput.NewMemoryQueue(), err: fmt.Errorf("queue operation: %w", test.cause)}
			dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}}
			request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Content: "next", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "key"}}
			_, err := dispatcher.QueueInput(context.Background(), task.ID, request)
			var serviceErr *sessioncontrol.ServiceError
			if !errors.As(err, &serviceErr) || serviceErr.Code != test.code || !errors.Is(err, test.cause) {
				t.Fatalf("err=%v, want code=%s cause=%v", err, test.code, test.cause)
			}
		})
	}
}

type sessionControlAddErrorQueue struct {
	pendinginput.Queue
	err error
}

func (q *sessionControlAddErrorQueue) Add(context.Context, pendinginput.NewInput) (pendinginput.PendingInput, error) {
	return pendinginput.PendingInput{}, q.err
}

func TestSessionControlStartRunQueuesBehindAtomicBusyWinner(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	winner := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: winner, createResult: mysqlstore.SessionControlRunResult{Task: winner, Busy: true}}
	triggered := make(chan pendinginput.Scope, 1)
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}, pendingTrigger: func(_ context.Context, scope pendinginput.Scope) { triggered <- scope }}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Content: "second", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "second-key", Fingerprint: "second-fingerprint"}}
	runID, err := dispatcher.StartRun(context.Background(), 41, agenttasks.TaskInput{ParentSessionID: 41, Status: agenttasks.StatusReady, MetadataJSON: `{}`}, request)
	if err != nil || runID != 51 {
		t.Fatalf("runID=%d err=%v", runID, err)
	}
	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("busy winner queue did not trigger coordinator")
	}
}

func TestSessionControlBusyFallbackRejectsSourceRefs(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	winner := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: winner, createResult: mysqlstore.SessionControlRunResult{Task: winner, Busy: true}}
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Content: "second", SourceRefs: []sessioncontrol.SessionRef{{Source: sessioncontrol.SourceTenant, Key: "source"}}, ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "second-key", Fingerprint: "second-fingerprint"}}
	if _, err := dispatcher.StartRun(context.Background(), 41, agenttasks.TaskInput{ParentSessionID: 41, Status: agenttasks.StatusReady, MetadataJSON: `{}`}, request); err == nil {
		t.Fatal("busy fallback with source refs error = nil")
	}
	states, err := queue.ListActiveSessionStates(context.Background(), 7, 11, []string{"41"})
	if err != nil || len(states) != 0 {
		t.Fatalf("states=%+v err=%v", states, err)
	}
}

func TestSessionControlQueueInputRejectsGlobalExistingFromDifferentTarget(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	firstTask := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusRunning}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: firstTask}
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}}
	first := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Ref: sessioncontrol.SessionRef{Source: sessioncontrol.SourceTenant, Key: "alpha"}, Content: "A", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "same-key", Fingerprint: "alpha-fingerprint"}}
	if _, err := dispatcher.QueueInput(context.Background(), 51, first); err != nil {
		t.Fatal(err)
	}
	tenant.task = mysqlstore.AgentTask{ID: 61, TenantID: 7, UserID: 11, ParentSessionID: 42, Status: agenttasks.StatusRunning}
	second := first
	second.Ref.Key = "beta"
	second.Content = "B"
	second.ReplayIdentity.Fingerprint = "beta-fingerprint"
	_, err := dispatcher.QueueInput(context.Background(), 61, second)
	var serviceErr *sessioncontrol.ServiceError
	if !errors.As(err, &serviceErr) || serviceErr.Code != sessioncontrol.CodeIdempotencyConflict {
		t.Fatalf("error=%v", err)
	}
}

func TestSessionControlLaunchRegistersBeforeCASAndStopPreventsGoroutine(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady}, startEntered: startEntered, releaseStart: releaseStart}
	controller := agenttasks.NewController()
	ran := make(chan struct{}, 1)
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, AgentTaskController: controller, SessionControlRunDetached: true}, queryFn: func(context.Context, QueryRequest) (query.Result, error) {
		ran <- struct{}{}
		return query.Result{}, nil
	}, limiter: newAgentTaskRunLimiter(1), cancels: make(map[uint64]context.CancelFunc)}
	done := make(chan error, 1)
	go func() { done <- dispatcher.LaunchRun(context.Background(), 51, sessioncontrol.SendRequest{}) }()
	<-startEntered
	if cancelled, err := dispatcher.CancelRun(context.Background(), 51); err != nil || !cancelled {
		t.Fatalf("cancelled=%v err=%v", cancelled, err)
	}
	close(releaseStart)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-ran:
		t.Fatal("run goroutine started after Stop won registration window")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSessionControlStartRunFailureTerminalizesPreparedTask(t *testing.T) {
	task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: task, createResult: mysqlstore.SessionControlRunResult{Task: task, EventID: 61, Created: true}, failResult: mysqlstore.SessionControlRunFailureResult{Failed: true, EventID: 62}}
	limiter := newAgentTaskRunLimiter(1)
	release, admitted := limiter.acquire()
	if !admitted {
		t.Fatal("failed to reserve limiter slot")
	}
	defer release()
	triggered := false
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, AgentTaskController: agenttasks.NewController()}, queryFn: func(context.Context, QueryRequest) (query.Result, error) { return query.Result{}, nil }, limiter: limiter, cancels: make(map[uint64]context.CancelFunc), pendingTrigger: func(context.Context, pendinginput.Scope) { triggered = true }}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11, TraceID: "trace-1"}, Content: "next"}

	runID, err := dispatcher.StartRun(context.Background(), 41, agenttasks.TaskInput{TenantID: 7, UserID: 11, ParentSessionID: 41, Model: "model", MetadataJSON: `{}`}, request)
	if err == nil || runID != 51 {
		t.Fatalf("run_id=%d err=%v", runID, err)
	}
	if tenant.failInput.TaskID != 51 || tenant.failInput.TraceID != "trace-1" || !strings.Contains(tenant.failInput.EventPayloadJSON, `"error_code":"internal_error"`) {
		t.Fatalf("prepared task was not terminalized: %+v", tenant.failInput)
	}
	if !triggered {
		t.Fatal("pending input coordinator was not triggered after prepared failure")
	}
}

func TestSessionControlProviderFailurePersistsFailedTaskState(t *testing.T) {
	fake := &fakeTenantService{}
	task := mysqlstore.AgentTask{
		ID:              51,
		TenantID:        7,
		UserID:          11,
		ParentSessionID: 41,
		AgentName:       agenttasks.AgentNameWeb,
		Model:           "gpt-6-sol",
		MetadataJSON:    `{"cwd":"` + t.TempDir() + `","provider":"jiuan-responses-gpt-5.6sol"}`,
	}
	result, err := runAgentTaskMessage(context.Background(), Options{TenantService: fake}, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{}, errors.New("provider unavailable")
	}, task, agenttasks.MessageInput{TaskID: task.ID, Content: "launch", TraceID: "trace-provider-failure"})
	if err != nil {
		t.Fatalf("runAgentTaskMessage error = %v, want persisted terminal failure", err)
	}
	if result.Status != agenttasks.StatusFailed {
		t.Fatalf("result status = %q, want %q", result.Status, agenttasks.StatusFailed)
	}
	status, payload := fake.lastFinishedAgentSnapshot()
	if status != agenttasks.StatusFailed {
		t.Fatalf("persisted task status = %q, want %q", status, agenttasks.StatusFailed)
	}
	if !strings.Contains(payload, `"trace_id":"trace-provider-failure"`) {
		t.Fatalf("failure payload missing trace id: %s", payload)
	}
	lastEvent := fake.lastAgentTaskEventInputSnapshot()
	if lastEvent.EventType != agenttasks.EventFailed {
		t.Fatalf("last event type = %q, want %q", lastEvent.EventType, agenttasks.EventFailed)
	}
}

func TestConcurrentSessionControlLaunchLoserCannotUnregisterWinner(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady}, startEntered: startEntered, releaseStart: releaseStart}
	controller := agenttasks.NewController()
	runStarted := make(chan struct{})
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, AgentTaskController: controller, SessionControlRunDetached: true}, queryFn: func(ctx context.Context, _ QueryRequest) (query.Result, error) {
		close(runStarted)
		<-ctx.Done()
		return query.Result{}, ctx.Err()
	}, limiter: newAgentTaskRunLimiter(2), cancels: make(map[uint64]context.CancelFunc)}
	firstDone := make(chan error, 1)
	go func() { firstDone <- dispatcher.LaunchRun(context.Background(), 51, sessioncontrol.SendRequest{}) }()
	<-startEntered
	secondDone := make(chan error, 1)
	go func() { secondDone <- dispatcher.LaunchRun(context.Background(), 51, sessioncontrol.SendRequest{}) }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second launcher did not resolve")
	}
	close(releaseStart)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("winning run did not start")
	}
	if cancelled, err := dispatcher.CancelRun(context.Background(), 51); err != nil || !cancelled {
		t.Fatalf("winner cancel=%v err=%v", cancelled, err)
	}
}

type sessionControlRuntimeTenantFake struct {
	*fakeTenantService
	mu           sync.Mutex
	task         mysqlstore.AgentTask
	createResult mysqlstore.SessionControlRunResult
	createInput  mysqlstore.SessionControlRunInput
	startEntered chan struct{}
	releaseStart chan struct{}
	startCalls   int
	failInput    mysqlstore.SessionControlRunFailureInput
	failResult   mysqlstore.SessionControlRunFailureResult
}

func (f *sessionControlRuntimeTenantFake) FailSessionControlRun(_ context.Context, input mysqlstore.SessionControlRunFailureInput) (mysqlstore.SessionControlRunFailureResult, error) {
	f.failInput = input
	return f.failResult, nil
}

func (f *sessionControlRuntimeTenantFake) GetAgentTask(context.Context, uint64) (mysqlstore.AgentTask, error) {
	return f.task, nil
}
func (f *sessionControlRuntimeTenantFake) CreateSessionControlRun(_ context.Context, input mysqlstore.SessionControlRunInput) (mysqlstore.SessionControlRunResult, error) {
	f.createInput = input
	return f.createResult, nil
}
func (f *sessionControlRuntimeTenantFake) StartSessionControlRun(context.Context, uint64) (mysqlstore.SessionControlRunStartResult, error) {
	f.mu.Lock()
	f.startCalls++
	call := f.startCalls
	f.mu.Unlock()
	if call == 1 && f.startEntered != nil {
		close(f.startEntered)
		<-f.releaseStart
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if call > 1 {
		return mysqlstore.SessionControlRunStartResult{Task: f.task}, nil
	}
	f.task.Status = agenttasks.StatusRunning
	return mysqlstore.SessionControlRunStartResult{Task: f.task, Started: true}, nil
}

func TestSessionControlRuntimeConfigPreflightAndActualQuery(t *testing.T) {
	config := sessioncontrol.RuntimeConfig{Model: "model-b", Provider: "provider-b", PermissionMode: "deny", Effort: "low", PromptMode: "chat"}
	encoded, _ := json.Marshal(config)
	metadata := string(encoded)
	task := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, AgentName: agenttasks.AgentNameWeb, Status: agenttasks.StatusReady, Model: config.Model, MetadataJSON: metadata}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{tenantID: 7, userID: 11}, task: task, createResult: mysqlstore.SessionControlRunResult{Task: task, Created: true, EventID: 61}}
	var actual QueryRequest
	dispatcher := &sessionControlRunDispatcher{
		opts: Options{TenantService: tenant, Workspace: t.TempDir(), AgentTaskController: agenttasks.NewController(), SessionControlConfigResolver: func(_ string, requested sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error) {
			if requested != config {
				t.Fatalf("preflight=%+v", requested)
			}
			return requested, nil
		}},
		limiter: newAgentTaskRunLimiter(1), cancels: make(map[uint64]context.CancelFunc),
		queryFn: func(_ context.Context, request QueryRequest) (query.Result, error) {
			actual = request
			return query.Result{Response: "done"}, nil
		},
	}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Content: "next"}
	if _, err := dispatcher.StartRun(context.Background(), 41, agenttasks.TaskInput{ParentSessionID: 41, Model: config.Model, MetadataJSON: metadata}, request); err != nil {
		t.Fatal(err)
	}
	if actual.Model != config.Model || actual.Provider != config.Provider || actual.PermissionMode != config.PermissionMode || actual.Effort != config.Effort || actual.PromptMode != config.PromptMode {
		t.Fatalf("actual query=%+v", actual)
	}
	var persisted sessioncontrol.RuntimeConfig
	if err := json.Unmarshal([]byte(tenant.createInput.Task.MetadataJSON), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != config {
		t.Fatalf("persisted=%+v", persisted)
	}
	tenant.createInput = mysqlstore.SessionControlRunInput{}
	dispatcher.opts.SessionControlConfigResolver = func(string, sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error) {
		return sessioncontrol.RuntimeConfig{}, errors.New("bad route")
	}
	if _, err := dispatcher.PrepareRun(context.Background(), 41, agenttasks.TaskInput{ParentSessionID: 41, Model: config.Model, MetadataJSON: metadata}, request); err == nil || tenant.createInput.Task.ParentSessionID != 0 {
		t.Fatal("invalid preflight reached persistence")
	}
}

func TestSessionControlBusyCASRejectsConfigChanges(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	winner := mysqlstore.AgentTask{ID: 51, TenantID: 7, UserID: 11, ParentSessionID: 41, Status: agenttasks.StatusReady, Model: "model-a", MetadataJSON: `{"model":"model-a","provider":"provider-a","permission_mode":"ask","effort":"high","prompt_mode":"code"}`}
	tenant := &sessionControlRuntimeTenantFake{fakeTenantService: &fakeTenantService{}, task: winner, createResult: mysqlstore.SessionControlRunResult{Task: winner, Busy: true}}
	dispatcher := &sessionControlRunDispatcher{opts: Options{TenantService: tenant, PendingInputQueue: queue}}
	request := sessioncontrol.SendRequest{Context: sessioncontrol.RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, Content: "next", Model: "model-b", ReplayIdentity: sessioncontrol.OperationIdentity{KeyHash: "key"}}
	if _, err := dispatcher.StartRun(context.Background(), 41, agenttasks.TaskInput{ParentSessionID: 41, Model: "model-b", MetadataJSON: `{}`}, request); err == nil {
		t.Fatal("concurrent configuration change queued")
	}
	items, err := queue.List(context.Background(), pendingInputScopeForTask(winner))
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}
