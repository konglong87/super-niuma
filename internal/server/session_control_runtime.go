package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/handoff"
	"github.com/konglong87/super-niuma/internal/sessioncontrol/runtimecompose"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// NewSessionControlService builds the single production service used by HTTP
// and by the Orchestrator tool surface. The caller owns the tenant repository
// and durable pending-input queue lifetimes.
func NewSessionControlService(opts Options, queryFn QueryFunc) (SessionControlService, error) {
	tenantRuntime, ok := opts.TenantService.(runtimecompose.TenantService)
	if !ok || tenantRuntime == nil {
		return nil, fmt.Errorf("session control tenant runtime is unavailable")
	}
	if opts.PendingInputQueue == nil || opts.AgentTaskController == nil {
		return nil, fmt.Errorf("session control execution runtime is unavailable")
	}
	dispatcher := &sessionControlRunDispatcher{
		opts: opts, queryFn: queryFn,
		limiter: newAgentTaskRunLimiter(opts.AgentTaskMaxConcurrentRuns), cancels: make(map[uint64]context.CancelFunc),
	}
	if opts.pendingInputCoordinator != nil {
		dispatcher.pendingTrigger = opts.pendingInputCoordinator.trigger
	}
	var eventStore sessioncontrol.ManagedEventStore
	var handoffEventStore handoff.EventStore
	if opts.SessionEvents != nil {
		projection := sessionControlManagedEventStore{opts: opts}
		eventStore = projection
		handoffEventStore = projection
	}
	service, err := runtimecompose.NewService(runtimecompose.Dependencies{
		Tenant: tenantRuntime, EventStore: eventStore, HandoffEventStore: handoffEventStore,
		Dispatcher: dispatcher, PendingInputs: opts.PendingInputQueue,
		Monitor: opts.SessionMonitor, EnableLocalRead: opts.SessionControlEnableLocalRead,
	})
	if err != nil {
		return nil, err
	}
	return &sessionControlRuntimeService{Service: service, dispatcher: dispatcher}, nil
}

type sessionControlRuntimeService struct {
	*sessioncontrol.Service
	dispatcher *sessionControlRunDispatcher
}

type SessionControlDrainer interface {
	Drain(context.Context) error
}

func (s *sessionControlRuntimeService) Drain(ctx context.Context) error {
	if s == nil || s.dispatcher == nil {
		return nil
	}
	return s.dispatcher.wait(ctx)
}

type sessionControlRunDispatcher struct {
	opts           Options
	queryFn        QueryFunc
	limiter        *agentTaskRunLimiter
	runs           sync.WaitGroup
	mu             sync.Mutex
	cancels        map[uint64]context.CancelFunc
	pendingTrigger func(context.Context, pendinginput.Scope)
}

var errSessionControlRunBusy = errors.New("session control run is busy")

func (d *sessionControlRunDispatcher) ResolveRuntimeConfig(_ context.Context, cwd string, requested sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error) {
	resolved, err := sessioncontrol.NormalizeRuntimeConfig(requested)
	if err == nil && d.opts.SessionControlConfigResolver != nil {
		resolved, err = d.opts.SessionControlConfigResolver(cwd, resolved)
	} else if err == nil && d.opts.SessionControlRouteResolver != nil {
		resolved.Provider, resolved.Model, err = d.opts.SessionControlRouteResolver(cwd, resolved.Provider, resolved.Model)
	}
	if err != nil {
		return sessioncontrol.RuntimeConfig{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "runtime configuration is invalid"}
	}
	return sessioncontrol.NormalizeRuntimeConfig(resolved)
}

func (d *sessionControlRunDispatcher) QueueInput(ctx context.Context, taskID uint64, request sessioncontrol.SendRequest) (uint64, error) {
	if d == nil || d.opts.PendingInputQueue == nil || request.ReplayIdentity.KeyHash == "" {
		return 0, fmt.Errorf("session control pending-input runtime is unavailable")
	}
	if len(request.SourceRefs) > 0 {
		return 0, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "source sessions cannot be queued behind an active run"}
	}
	task, err := d.opts.TenantService.GetAgentTask(ctx, taskID)
	if err != nil || task.ParentSessionID == 0 {
		return 0, fmt.Errorf("session control pending-input task is unavailable")
	}
	var config sessioncontrol.RuntimeConfig
	if task.MetadataJSON != "" {
		if err := json.Unmarshal([]byte(task.MetadataJSON), &config); err != nil {
			return 0, err
		}
	}
	if config.Model == "" {
		config.Model = task.Model
	}
	overrides, err := sessioncontrol.NormalizeRuntimeConfig(request.RuntimeConfig())
	if err != nil {
		return 0, err
	}
	if config.WithOverrides(overrides) != config {
		return 0, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "runtime configuration cannot be changed while a session run or pending input is active"}
	}
	scope := pendingInputScopeForTask(task)
	scope.TenantID, scope.UserID = request.Context.TenantID, request.Context.UserID
	item, err := d.opts.PendingInputQueue.Add(ctx, pendinginput.NewInput{
		Scope:               scope,
		ClientInputID:       request.ReplayIdentity.KeyHash,
		Content:             request.Content,
		Attachments:         request.Attachments,
		GlobalClientInputID: true,
	})
	if err != nil {
		// Queue settings reject new input without turning an expected state conflict into a server failure.
		if errors.Is(err, pendinginput.ErrQueueDisabled) {
			return 0, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "pending input queue is disabled", Cause: err}
		}
		if errors.Is(err, pendinginput.ErrConflict) {
			return 0, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeIdempotencyConflict, Message: "idempotency key resolves to conflicting pending inputs", Cause: err}
		}
		return 0, err
	}
	if item.SessionID != scope.SessionID || item.Content != request.Content || !reflect.DeepEqual(item.Attachments, request.Attachments) {
		return 0, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
	}
	appendPendingInputEventContext(ctx, d.opts.TenantService, taskID, agenttasks.EventInputQueued, item)
	if d.pendingTrigger != nil {
		d.pendingTrigger(ctx, scope)
	}
	runID := item.DispatchedTaskID
	if runID == 0 {
		runID = item.BaseTaskID
	}
	return runID, nil
}

func (d *sessionControlRunDispatcher) StartRun(ctx context.Context, _ uint64, input agenttasks.TaskInput, request sessioncontrol.SendRequest) (uint64, error) {
	taskID, err := d.PrepareRun(ctx, input.ParentSessionID, input, request)
	if errors.Is(err, errSessionControlRunBusy) {
		return d.QueueInput(ctx, taskID, request)
	}
	if err != nil {
		return 0, err
	}
	if err := d.LaunchRun(ctx, taskID, request); err != nil {
		_, cleanupErr := d.FailPreparedRun(context.WithoutCancel(ctx), taskID, sessioncontrol.PreparedRunFailure{ErrorCode: string(sessioncontrol.CodeInternal), TraceID: request.Context.TraceID})
		if cleanupErr != nil {
			return taskID, errors.Join(err, cleanupErr)
		}
		return taskID, err
	}
	return taskID, nil
}

func (d *sessionControlRunDispatcher) FailPreparedRun(ctx context.Context, taskID uint64, failure sessioncontrol.PreparedRunFailure) (bool, error) {
	if d == nil || d.opts.TenantService == nil || taskID == 0 {
		return false, fmt.Errorf("session control prepared-run failure runtime is unavailable")
	}
	runtime, ok := d.opts.TenantService.(interface {
		FailSessionControlRun(context.Context, mysqlstore.SessionControlRunFailureInput) (mysqlstore.SessionControlRunFailureResult, error)
	})
	if !ok {
		return false, fmt.Errorf("session control prepared-run failure persistence is unavailable")
	}
	errorCode := stablePreparedRunErrorCode(failure.ErrorCode)
	payload := agentTaskEventPayload(map[string]any{
		"source":     sessioncontrol.PreparedRunFailureSource,
		"error_code": errorCode,
		"trace_id":   failure.TraceID,
	})
	result, err := runtime.FailSessionControlRun(ctx, mysqlstore.SessionControlRunFailureInput{
		TaskID: taskID, ResultJSON: payload, EventPayloadJSON: payload, TraceID: failure.TraceID,
	})
	if err == nil && result.Failed {
		d.TriggerPending(ctx, taskID)
	}
	return result.Failed, err
}

func stablePreparedRunErrorCode(value string) string {
	code := sessioncontrol.ServiceErrorCode(strings.TrimSpace(value))
	switch code {
	case sessioncontrol.CodeInvalidState, sessioncontrol.CodeNotFound, sessioncontrol.CodeForbidden, sessioncontrol.CodeIdempotencyConflict, sessioncontrol.CodeHandoffStale, sessioncontrol.CodeBudgetExceeded, sessioncontrol.CodeSchedulerUnavailable, sessioncontrol.CodeInternal:
		return string(code)
	default:
		return string(sessioncontrol.CodeInternal)
	}
}

func (d *sessionControlRunDispatcher) PrepareRun(ctx context.Context, _ uint64, input agenttasks.TaskInput, request sessioncontrol.SendRequest) (uint64, error) {
	if d == nil || d.opts.TenantService == nil {
		return 0, fmt.Errorf("session control agent runner is unavailable")
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(input.MetadataJSON), &metadata); err != nil {
		return 0, fmt.Errorf("session control run metadata is invalid")
	}
	if cwd, _ := metadata["cwd"].(string); strings.TrimSpace(cwd) != "" {
		if d.opts.SessionControlCWDValidator == nil {
			return 0, fmt.Errorf("session control run cwd is not allowed")
		}
		resolved, err := d.opts.SessionControlCWDValidator(cwd)
		if err != nil || resolved != cwd {
			return 0, fmt.Errorf("session control run cwd is not allowed")
		}
	}
	var runtimeConfig sessioncontrol.RuntimeConfig
	if err := json.Unmarshal([]byte(input.MetadataJSON), &runtimeConfig); err != nil {
		return 0, err
	}
	runtimeConfig.Model = input.Model
	cwd, _ := metadata["cwd"].(string)
	resolved, err := d.ResolveRuntimeConfig(ctx, cwd, runtimeConfig)
	if err != nil {
		return 0, err
	}
	input.Model = resolved.Model
	resolvedJSON, _ := json.Marshal(resolved)
	var fields map[string]any
	_ = json.Unmarshal(resolvedJSON, &fields)
	for key, value := range fields {
		metadata[key] = value
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return 0, err
	}
	input.MetadataJSON = string(encoded)
	runtime, ok := d.opts.TenantService.(interface {
		CreateSessionControlRun(context.Context, mysqlstore.SessionControlRunInput) (mysqlstore.SessionControlRunResult, error)
	})
	if !ok {
		return 0, fmt.Errorf("session control task recovery is unavailable")
	}
	message := agenttasks.MessageInput{Content: request.Content, Attachments: request.Attachments, TraceID: request.Context.TraceID}
	expectedJSON := ""
	configChanged := false
	if request.ExpectedRuntimeConfig != nil {
		encoded, err := json.Marshal(request.ExpectedRuntimeConfig)
		if err != nil {
			return 0, err
		}
		expectedJSON = string(encoded)
		configChanged = resolved != *request.ExpectedRuntimeConfig
	}
	result, err := runtime.CreateSessionControlRun(ctx, mysqlstore.SessionControlRunInput{
		Task: input, Message: message, AdoptTaskID: request.AdoptTaskID,
		ExpectedRuntimeConfigJSON: expectedJSON, RuntimeConfigChanged: configChanged,
		SkipMessageEvent: d.opts.SessionEvents != nil,
	})
	if err != nil {
		return 0, err
	}
	if result.Task.ID == 0 || d.opts.SessionEvents == nil && result.EventID == 0 {
		if result.Busy && result.Task.ID != 0 {
			return result.Task.ID, errSessionControlRunBusy
		}
		return 0, fmt.Errorf("session control prepared run readback is incomplete")
	}
	if d.opts.SessionEvents != nil && result.Created {
		message.TaskID = result.Task.ID
		payload, err := json.Marshal(message)
		if err != nil {
			return 0, err
		}
		if _, err := appendAgentTaskEvent(ctx, d.opts, result.Task, agenttasks.EventInput{
			TaskID:      result.Task.ID,
			EventType:   agenttasks.EventMessage,
			PayloadJSON: string(payload),
			TraceID:     message.TraceID,
		}); err != nil {
			return 0, err
		}
	}
	return result.Task.ID, nil
}

func (d *sessionControlRunDispatcher) LaunchRun(ctx context.Context, taskID uint64, request sessioncontrol.SendRequest) error {
	if d == nil || d.opts.TenantService == nil || d.limiter == nil || d.opts.AgentTaskController == nil || d.opts.StreamQueryFunc == nil && d.queryFn == nil {
		return fmt.Errorf("session control agent runner is unavailable")
	}
	release, admitted := d.limiter.acquire()
	if !admitted {
		return fmt.Errorf("session control agent runner is at capacity")
	}
	handedOff := false
	defer func() {
		if !handedOff {
			release()
		}
	}()
	runtime, ok := d.opts.TenantService.(interface {
		StartSessionControlRun(context.Context, uint64) (mysqlstore.SessionControlRunStartResult, error)
	})
	if !ok {
		return fmt.Errorf("session control run claim is unavailable")
	}
	message := agenttasks.MessageInput{TaskID: taskID, Content: request.Content, Attachments: request.Attachments, TraceID: request.Context.TraceID}
	runCtx := observability.WithTraceID(context.WithoutCancel(ctx), message.TraceID)
	runCtx, runCancel := context.WithTimeout(runCtx, agentTaskRunTimeout(d.opts))
	runCtx, controllerCancel := context.WithCancel(runCtx)
	d.mu.Lock()
	if _, exists := d.cancels[taskID]; exists {
		d.mu.Unlock()
		controllerCancel()
		runCancel()
		return nil
	}
	d.opts.AgentTaskController.Register(taskID, controllerCancel)
	d.cancels[taskID] = controllerCancel
	d.mu.Unlock()
	cleanupRegistration := func() {
		controllerCancel()
		runCancel()
		d.opts.AgentTaskController.Unregister(taskID)
		d.mu.Lock()
		delete(d.cancels, taskID)
		d.mu.Unlock()
	}
	claim, err := runtime.StartSessionControlRun(ctx, taskID)
	if err != nil {
		cleanupRegistration()
		return err
	}
	if !claim.Started {
		cleanupRegistration()
		return nil
	}
	task := claim.Task
	if runCtx.Err() != nil {
		cleanupRegistration()
		return nil
	}
	run := func() error {
		defer d.runs.Done()
		defer cleanupRegistration()
		defer release()
		defer recoverAgentTaskRun(runCtx, d.opts, task, message.TraceID)
		_, runErr := runAgentTaskMessage(runCtx, d.opts, d.queryFn, task, message)
		return runErr
	}
	d.runs.Add(1)
	handedOff = true
	if d.opts.SessionControlRunDetached {
		go func() { _ = run() }()
		return nil
	}
	if err := run(); err != nil {
		return err
	}
	return nil
}

func (d *sessionControlRunDispatcher) TriggerPending(ctx context.Context, taskID uint64) {
	if d == nil || d.pendingTrigger == nil || d.opts.TenantService == nil || taskID == 0 {
		return
	}
	task, err := d.opts.TenantService.GetAgentTask(ctx, taskID)
	if err != nil {
		return
	}
	d.pendingTrigger(ctx, pendingInputScopeForTask(task))
}

func (d *sessionControlRunDispatcher) wait(ctx context.Context) error {
	d.mu.Lock()
	for _, cancel := range d.cancels {
		cancel()
	}
	d.mu.Unlock()
	done := make(chan struct{})
	go func() {
		d.runs.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (d *sessionControlRunDispatcher) CancelRun(_ context.Context, taskID uint64) (bool, error) {
	if d == nil || d.opts.AgentTaskController == nil {
		return false, fmt.Errorf("session control task controller is unavailable")
	}
	return d.opts.AgentTaskController.Cancel(taskID), nil
}
