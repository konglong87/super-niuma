package sessioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const (
	managedSessionLinkListLimit      = 100
	managedLifecycleIdle             = "idle"
	managedCancellationSource        = "session_control"
	managedDependencyFailure         = "managed session operation failed"
	managedEventWindowFailure        = "managed session event history is unavailable"
	managedResourceNotFound          = "managed session resource was not found"
	managedHandoffContextWindow      = 65_536
	managedPreparedRunCleanupTimeout = 2 * time.Second
)

// ManagedStore is the tenant-scoped persistence surface consumed by the
// managed session adapter. Implementations must apply scope to every method.
type ManagedStore interface {
	UpsertSession(context.Context, RequestContext, mysqlstore.SessionInput) (uint64, error)
	CreateSessionControlSession(context.Context, RequestContext, mysqlstore.SessionControlCreateInput) (mysqlstore.SessionControlCreateResult, error)
	ListSessions(context.Context, RequestContext, int) ([]mysqlstore.SessionControlSession, error)
	GetSessionControlSessionByKey(context.Context, RequestContext, string) (mysqlstore.SessionControlSession, error)
	GetAgentTask(context.Context, RequestContext, uint64) (mysqlstore.AgentTask, error)
	GetTaskEvent(context.Context, RequestContext, uint64, uint64) (mysqlstore.AgentTaskEvent, error)
	GetSessionLink(context.Context, RequestContext, uint64, SessionRef, string) (mysqlstore.SessionLink, error)
	ListLatestAgentTasksForSessions(context.Context, RequestContext, []uint64) ([]mysqlstore.AgentTask, error)
	CreateAgentTask(context.Context, RequestContext, agenttasks.TaskInput) (uint64, error)
	// CancelAgentTaskIfRunning conditionally persists cancellation only when the
	// task is still running. It returns false for a concurrent terminal change.
	CancelAgentTaskIfRunning(context.Context, RequestContext, uint64, string) (bool, error)
	CancelAgentTaskForSessionControl(context.Context, RequestContext, mysqlstore.SessionControlStopInput) (mysqlstore.SessionControlStopResult, error)
	// ListAgentTaskEventsForTasksComplete returns the complete ordered permission window
	// for each requested task, without a per-task truncation that could omit a
	// later permission_resolved event after a permission_request.
	ListAgentTaskEventsForTasksComplete(context.Context, RequestContext, []uint64) ([]mysqlstore.AgentTaskEvent, error)
	ListSessionLinks(context.Context, RequestContext, uint64, int) ([]mysqlstore.SessionLink, error)
}

type ManagedChannelStore interface {
	ListSessionChannels(context.Context, RequestContext, []uint64) (map[uint64]mysqlstore.SessionChannel, error)
}

// ManagedEventStore is an optional read projection for high-frequency session
// events. The control-plane ManagedStore remains the fallback for SQLite mode.
type ManagedEventStore interface {
	ListAgentTaskEventsForTasksComplete(context.Context, RequestContext, []uint64) ([]mysqlstore.AgentTaskEvent, error)
}

// ManagedTaskEventStore projects events for task rows already loaded in the
// current tenant scope, avoiding a second task lookup for each row.
type ManagedTaskEventStore interface {
	ListAgentTaskEventsForTaskRecords(context.Context, RequestContext, []mysqlstore.AgentTask) ([]mysqlstore.AgentTaskEvent, error)
}

// ManagedEventAppender is the write side of the optional high-frequency event
// projection. JSONL mode implements it by appending to the session transcript;
// SQLite mode intentionally leaves it unset and uses ManagedStore instead.
type ManagedEventAppender interface {
	AppendAgentTaskEvent(context.Context, RequestContext, agenttasks.EventInput) (uint64, error)
}

// ManagedRunDispatcher owns runtime actions. It deliberately does not expose
// HTTP handlers or command execution to the persistence adapter.
type ManagedRunDispatcher interface {
	QueueInput(context.Context, uint64, SendRequest) (uint64, error)
	StartRun(context.Context, uint64, agenttasks.TaskInput, SendRequest) (uint64, error)
	CancelRun(context.Context, uint64) (bool, error)
}

type ManagedPreparedRunDispatcher interface {
	PrepareRun(context.Context, uint64, agenttasks.TaskInput, SendRequest) (uint64, error)
	LaunchRun(context.Context, uint64, SendRequest) error
	FailPreparedRun(context.Context, uint64, PreparedRunFailure) (bool, error)
}

type ManagedPendingTriggerDispatcher interface {
	TriggerPending(context.Context, uint64)
}

type ManagedAdapter struct {
	store      ManagedStore
	eventStore ManagedEventStore
	dispatcher ManagedRunDispatcher
	handoff    HandoffPort
	pending    pendinginput.Queue
}

func (a *ManagedAdapter) SetHandoff(handoff HandoffPort)            { a.handoff = handoff }
func (a *ManagedAdapter) SetPendingInputs(queue pendinginput.Queue) { a.pending = queue }
func (a *ManagedAdapter) SetEventStore(store ManagedEventStore)     { a.eventStore = store }

func NewManagedAdapter(store ManagedStore, dispatcher ManagedRunDispatcher) *ManagedAdapter {
	return &ManagedAdapter{store: store, dispatcher: dispatcher}
}

func (a *ManagedAdapter) Create(ctx context.Context, request CreateRequest) (SessionSnapshot, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return SessionSnapshot{}, err
	}
	request.SessionKey = managedSessionKey(request.SessionKey, request.ReplayIdentity)
	ref, err := canonicalTenantKey(request.SessionKey)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if a.store == nil {
		return SessionSnapshot{}, unavailable("managed session store")
	}
	if strings.TrimSpace(request.InitialText) != "" && a.dispatcher == nil {
		return SessionSnapshot{}, unavailable("managed session dispatcher")
	}
	runtimeConfig, err := a.resolveRuntimeConfig(ctx, request.CWD, request.RuntimeConfig())
	if err != nil {
		return SessionSnapshot{}, err
	}
	metadataJSON, err := runtimeConfigMetadataJSON(request.CWD, runtimeConfig, request.ReplayIdentity)
	if err != nil {
		return SessionSnapshot{}, err
	}
	created, err := a.store.CreateSessionControlSession(ctx, request.Context, mysqlstore.SessionControlCreateInput{Session: mysqlstore.SessionInput{
		SessionKey: ref.Key, Title: request.Title, Status: managedLifecycleIdle,
		Model: runtimeConfig.Model, CWD: request.CWD, MetadataJSON: metadataJSON,
	}})
	if err != nil {
		return SessionSnapshot{}, normalizeManagedError(err)
	}
	if created.Session.ID == 0 {
		return SessionSnapshot{}, invalidState("managed session create readback is incomplete")
	}
	storedMetadata, err := operationMetadataFromJSON(created.Session.MetadataJSON)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if err := validateRecoveredMetadata(request.ReplayIdentity, storedMetadata); err != nil {
		return SessionSnapshot{}, err
	}
	if strings.TrimSpace(request.InitialText) != "" {
		runMetadataJSON, metadataErr := runtimeConfigMetadataJSON(request.CWD, runtimeConfig, request.ReplayIdentity)
		if metadataErr != nil {
			return SessionSnapshot{}, metadataErr
		}
		_, err := a.dispatcher.StartRun(ctx, created.Session.ID, agenttasks.TaskInput{
			TenantID: request.Context.TenantID, UserID: request.Context.UserID,
			ParentSessionID: created.Session.ID, SubagentSessionKey: ref.Key,
			AgentName: agenttasks.AgentNameWeb, Description: request.Title, Prompt: request.InitialText, Status: agenttasks.StatusReady,
			Model: runtimeConfig.Model, MetadataJSON: runMetadataJSON, TraceID: request.Context.TraceID,
			IdempotencyKey: request.ReplayIdentity.OperationID,
		}, SendRequest{Context: request.Context, Ref: ref, Content: request.InitialText, ReplayIdentity: request.ReplayIdentity})
		if err != nil {
			return SessionSnapshot{}, normalizeManagedError(err)
		}
	}
	return a.getByKey(ctx, request.Context, ref.Key, false)
}

func (a *ManagedAdapter) List(ctx context.Context, request ListRequest) ([]SessionSnapshot, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return nil, err
	}
	if request.Source != SourceTenant {
		return nil, &ServiceError{Code: CodeForbidden, Message: "managed adapter requires tenant sessions"}
	}
	if a.store == nil {
		return nil, unavailable("managed session store")
	}
	stored, err := a.store.ListSessions(ctx, request.Context, request.Limit)
	if err != nil {
		return nil, normalizeManagedError(err)
	}
	sessions := make([]mysqlstore.Session, 0, len(stored))
	sessionIDs := make([]uint64, 0, len(stored))
	for _, item := range stored {
		sessions = append(sessions, item.Session)
		sessionIDs = append(sessionIDs, item.ID)
	}
	channels, err := a.listSessionChannels(ctx, request.Context, sessionIDs)
	if err != nil {
		return nil, normalizeManagedError(err)
	}
	tasks, err := a.listLatestTasks(ctx, request.Context, sessions)
	if err != nil {
		return nil, err
	}
	latest := latestTasksBySession(tasks)
	events, err := a.listEvents(ctx, request.Context, activeSummaryTasks(tasks))
	if err != nil {
		return nil, err
	}
	eventsByTask := eventsByTaskID(events)
	pendingStates, err := a.pendingStates(ctx, request.Context, sessions)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSnapshot, 0, len(sessions))
	for _, item := range stored {
		snapshot, err := configuredManagedSnapshot(item, latest[item.ID], eventsByTask, pendingStates[strconv.FormatUint(item.ID, 10)].Count)
		if err != nil {
			return nil, err
		}
		applySessionChannel(&snapshot, channels[item.ID])
		out = append(out, snapshot)
	}
	return out, nil
}

func activeSummaryTasks(tasks []mysqlstore.AgentTask) []mysqlstore.AgentTask {
	active := make([]mysqlstore.AgentTask, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == agenttasks.StatusReady || task.Status == agenttasks.StatusRunning {
			active = append(active, task)
		}
	}
	return active
}

func (a *ManagedAdapter) Get(ctx context.Context, request GetRequest) (SessionSnapshot, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return SessionSnapshot{}, err
	}
	if err := validateManagedRef(request.Ref); err != nil {
		return SessionSnapshot{}, err
	}
	if a.store == nil {
		return SessionSnapshot{}, unavailable("managed session store")
	}
	return a.getByKey(ctx, request.Context, request.Ref.Key, request.IncludeLinks)
}

// ReadHandoffTarget verifies the explicitly selected task belongs to the
// target session. The status is returned for the handoff service to reject a
// non-ready target before it captures sources; persistence repeats the check.
func (a *ManagedAdapter) ReadHandoffTarget(ctx context.Context, requestContext RequestContext, target SessionRef, taskID uint64) (HandoffTarget, error) {
	if err := validateRequestContext(requestContext); err != nil {
		return HandoffTarget{}, err
	}
	if err := validateManagedRef(target); err != nil {
		return HandoffTarget{}, err
	}
	if taskID == 0 {
		return HandoffTarget{}, invalidState("handoff target task ID is required")
	}
	if a.store == nil {
		return HandoffTarget{}, unavailable("managed session store")
	}
	session, err := a.store.GetSessionControlSessionByKey(ctx, requestContext, target.Key)
	if err != nil {
		return HandoffTarget{}, normalizeManagedError(err)
	}
	task, err := a.store.GetAgentTask(ctx, requestContext, taskID)
	if err != nil {
		return HandoffTarget{}, normalizeManagedError(err)
	}
	if session.ID == 0 || task.ID != taskID || task.ParentSessionID != session.ID {
		return HandoffTarget{}, &ServiceError{Code: CodeNotFound, Message: "handoff target task was not found"}
	}
	return HandoffTarget{SessionID: session.ID, TaskID: task.ID, TaskStatus: task.Status}, nil
}

func (a *ManagedAdapter) ReadHandoffEvent(ctx context.Context, requestContext RequestContext, target SessionRef, taskID, eventID uint64) (HandoffEvent, error) {
	if eventID == 0 {
		return HandoffEvent{}, invalidState("handoff event ID is required")
	}
	resolvedTarget, err := a.ReadHandoffTarget(ctx, requestContext, target, taskID)
	if err != nil {
		return HandoffEvent{}, err
	}
	event, err := a.store.GetTaskEvent(ctx, requestContext, resolvedTarget.SessionID, eventID)
	if err != nil {
		return HandoffEvent{}, normalizeManagedError(err)
	}
	if event.ID != eventID || event.TaskID != taskID || event.EventType != agenttasks.EventSessionHandoff || event.PayloadJSON == "" {
		return HandoffEvent{}, &ServiceError{Code: CodeNotFound, Message: "handoff event was not found"}
	}
	return HandoffEvent{EventID: event.ID, TaskID: event.TaskID, PayloadJSON: event.PayloadJSON}, nil
}

func (a *ManagedAdapter) ReadHandoffLink(ctx context.Context, requestContext RequestContext, target, source SessionRef, linkID uint64) (SessionLink, error) {
	if err := validateManagedRef(target); err != nil {
		return SessionLink{}, err
	}
	session, err := a.store.GetSessionControlSessionByKey(ctx, requestContext, target.Key)
	if err != nil {
		return SessionLink{}, normalizeManagedError(err)
	}
	link, err := a.store.GetSessionLink(ctx, requestContext, session.ID, source, agenttasks.SessionHandoffRelationType)
	if err != nil {
		return SessionLink{}, normalizeManagedError(err)
	}
	if linkID != 0 && link.ID != linkID {
		return SessionLink{}, &ServiceError{Code: CodeNotFound, Message: "handoff link was not found"}
	}
	return SessionLink{ID: link.ID, Target: target, Source: source, RelationType: link.RelationType, CreatedAt: link.CreatedAt}, nil
}

func (a *ManagedAdapter) Send(ctx context.Context, request SendRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := validateManagedRef(request.Ref); err != nil {
		return errorResult(err), err
	}
	if a.store == nil || a.dispatcher == nil {
		err := unavailable("managed session dependencies")
		return errorResult(err), err
	}
	detail, err := a.loadManagedDetail(ctx, request.Context, request.Ref.Key, false)
	if err != nil {
		return errorResult(err), err
	}
	overrides, err := NormalizeRuntimeConfig(request.RuntimeConfig())
	if err != nil {
		return errorResult(err), err
	}
	current := detail.snapshot.RuntimeConfig()
	request.ExpectedRuntimeConfig = &current
	runtimeConfig := current.WithOverrides(overrides)
	// A prepared operation must resume its original Run after a pre-launch
	// failure; queueing its own retry would leave that ready Run stranded.
	resuming := detail.latest != nil && detail.latest.Status == agenttasks.StatusReady && detail.latest.IdempotencyKey != "" && detail.latest.IdempotencyKey == request.ReplayIdentity.KeyHash
	adopting := detail.pendingBaseTaskID == 0 && isPreparedSideChat(detail.latest)
	if adopting {
		request.AdoptTaskID = detail.latest.ID
	}
	if !resuming && !adopting && (detail.pendingBaseTaskID != 0 || detail.latest != nil && (detail.latest.Status == agenttasks.StatusRunning || detail.latest.Status == agenttasks.StatusReady)) {
		if runtimeConfig != current {
			err := invalidState("runtime configuration cannot be changed while a session run or pending input is active")
			return errorResult(err), err
		}
		if len(request.SourceRefs) > 0 {
			err := invalidState("source sessions can only be attached before a new run starts")
			return errorResult(err), err
		}
		baseTaskID := detail.pendingBaseTaskID
		if baseTaskID == 0 {
			baseTaskID = detail.latest.ID
		}
		runID, err := a.dispatcher.QueueInput(ctx, baseTaskID, request)
		if err != nil {
			err = normalizeManagedError(err)
			return errorResult(err), err
		}
		return OperationResult{Session: detail.snapshot, RunID: runID}, nil
	}
	runtimeConfig, err = a.resolveRuntimeConfig(ctx, detail.session.CWD, runtimeConfig)
	if err != nil {
		return errorResult(err), err
	}
	metadataJSON, err := runtimeConfigMetadataJSON(detail.session.CWD, runtimeConfig, request.ReplayIdentity)
	if err != nil {
		return errorResult(err), err
	}
	if adopting || resuming {
		metadataJSON, err = preserveSideChatMetadata(metadataJSON, detail.latest.MetadataJSON)
		if err != nil {
			return errorResult(err), err
		}
	}
	taskInput := agenttasks.TaskInput{
		TenantID:           request.Context.TenantID,
		UserID:             request.Context.UserID,
		ParentSessionID:    detail.session.ID,
		SubagentSessionKey: detail.session.SessionKey,
		AgentName:          agenttasks.AgentNameWeb,
		Description:        detail.session.Title,
		Prompt:             request.Content,
		Status:             agenttasks.StatusReady,
		Model:              runtimeConfig.Model,
		MetadataJSON:       metadataJSON,
		TraceID:            request.Context.TraceID,
		IdempotencyKey:     request.ReplayIdentity.KeyHash,
	}
	if len(request.SourceRefs) > 0 {
		prepared, ok := a.dispatcher.(ManagedPreparedRunDispatcher)
		if !ok || a.handoff == nil {
			err := unavailable("managed handoff runner")
			return errorResult(err), err
		}
		runID, err := prepared.PrepareRun(ctx, detail.session.ID, taskInput, request)
		if err != nil {
			err = normalizeManagedError(err)
			return errorResult(err), err
		}
		attachIdentity, err := managedSendAttachIdentity(request, runID)
		if err != nil {
			err = a.failPreparedRun(ctx, prepared, runID, request, err)
			return OperationResult{RunID: runID, ErrorCode: serviceErrorCode(err)}, err
		}
		handoffResult, err := a.handoff.Attach(ctx, AttachRequest{
			Context: request.Context, Target: request.Ref, TargetTaskID: runID,
			TargetContextWindowTokens: managedHandoffContextWindow,
			Sources:                   request.SourceRefs, RelationType: "handoff",
			IdempotencyKey:    request.ReplayIdentity.OperationID + ":handoff",
			OperationIdentity: attachIdentity.KeyHash, ReplayIdentity: attachIdentity,
		})
		if err != nil {
			err = a.failPreparedRun(ctx, prepared, runID, request, normalizeManagedError(err))
			return OperationResult{RunID: runID, Handoff: handoffResult, ErrorCode: serviceErrorCode(err)}, err
		}
		if err := prepared.LaunchRun(ctx, runID, request); err != nil {
			err = a.failPreparedRun(ctx, prepared, runID, request, normalizeManagedError(err))
			return OperationResult{RunID: runID, ErrorCode: serviceErrorCode(err)}, err
		}
		linkIDs := make([]uint64, 0, len(handoffResult.SourceResults))
		for _, source := range handoffResult.SourceResults {
			if source.LinkID != 0 {
				linkIDs = append(linkIDs, source.LinkID)
			}
		}
		return OperationResult{Session: detail.snapshot, RunID: runID, LinkIDs: linkIDs, Handoff: handoffResult}, nil
	}
	runID, err := a.dispatcher.StartRun(ctx, detail.session.ID, taskInput, request)
	if err != nil {
		err = normalizeManagedError(err)
		return errorResult(err), err
	}
	return OperationResult{Session: detail.snapshot, RunID: runID}, nil
}

func (a *ManagedAdapter) Compact(ctx context.Context, request CompactRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := validateManagedRef(request.Ref); err != nil {
		return errorResult(err), err
	}
	detail, err := a.loadManagedDetail(ctx, request.Context, request.Ref.Key, false)
	if err != nil {
		return errorResult(err), err
	}
	if detail.latest == nil {
		err := invalidState("session has no completed conversation to compact")
		return errorResult(err), err
	}
	if detail.latest.Status == agenttasks.StatusRunning || detail.latest.Status == agenttasks.StatusReady {
		err := invalidState("session must be idle before compacting")
		return errorResult(err), err
	}
	conversationStore, ok := a.store.(interface {
		ListSessionConversationTasks(context.Context, RequestContext, uint64, int) ([]mysqlstore.AgentTask, error)
	})
	if !ok {
		err := unavailable("managed conversation store")
		return errorResult(err), err
	}
	tasks, err := conversationStore.ListSessionConversationTasks(ctx, request.Context, detail.session.ID, 200)
	if err != nil {
		return errorResult(err), normalizeManagedError(err)
	}
	events, err := a.listEvents(ctx, request.Context, tasks)
	if err != nil {
		return errorResult(err), normalizeManagedError(err)
	}
	for _, event := range events {
		if event.EventType != agenttasks.EventCompactSummary {
			continue
		}
		var payload struct {
			OperationID string `json:"operation_id"`
		}
		if json.Unmarshal([]byte(event.PayloadJSON), &payload) == nil && payload.OperationID == request.ReplayIdentity.OperationID {
			return OperationResult{Session: detail.snapshot, Replayed: true}, nil
		}
	}
	entries := compactEntriesFromManagedConversation(tasks, events)
	if len(entries) == 0 {
		err := invalidState("session has no readable conversation to compact")
		return errorResult(err), err
	}
	summary := session.BuildCompactSummary(entries, 0)
	if strings.TrimSpace(summary) == "" {
		err := invalidState("compact summary is empty")
		return errorResult(err), err
	}
	payload, err := json.Marshal(map[string]any{
		"source": "session_control", "operation_id": request.ReplayIdentity.OperationID,
		"summary": summary, "manual": true,
	})
	if err != nil {
		return errorResult(err), err
	}
	eventInput := agenttasks.EventInput{
		TaskID: detail.latest.ID, EventType: agenttasks.EventCompactSummary,
		PayloadJSON: string(payload), TraceID: request.Context.TraceID,
	}
	if a.eventStore != nil {
		appender, ok := a.eventStore.(ManagedEventAppender)
		if !ok {
			err := unavailable("managed projected event appender")
			return errorResult(err), err
		}
		_, err = appender.AppendAgentTaskEvent(ctx, request.Context, eventInput)
	} else {
		appender, ok := a.store.(ManagedEventAppender)
		if !ok {
			err := unavailable("managed task event appender")
			return errorResult(err), err
		}
		_, err = appender.AppendAgentTaskEvent(ctx, request.Context, eventInput)
	}
	if err != nil {
		return errorResult(err), normalizeManagedError(err)
	}
	return OperationResult{Session: detail.snapshot}, nil
}

func compactEntriesFromManagedConversation(tasks []mysqlstore.AgentTask, events []mysqlstore.AgentTaskEvent) []session.Entry {
	eventsByTask := make(map[uint64][]mysqlstore.AgentTaskEvent, len(tasks))
	for _, event := range events {
		eventsByTask[event.TaskID] = append(eventsByTask[event.TaskID], event)
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].StartedAt.Equal(tasks[j].StartedAt) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].StartedAt.Before(tasks[j].StartedAt)
	})
	entries := make([]session.Entry, 0, len(tasks)*3)
	for _, task := range tasks {
		taskEvents := eventsByTask[task.ID]
		sort.SliceStable(taskEvents, func(i, j int) bool { return taskEvents[i].ID < taskEvents[j].ID })
		for _, event := range taskEvents {
			if event.EventType != agenttasks.EventMessage {
				continue
			}
			var message agenttasks.MessageInput
			if json.Unmarshal([]byte(event.PayloadJSON), &message) == nil && strings.TrimSpace(message.Content) != "" {
				entries = append(entries, session.Entry{Type: "message", Role: "user", Content: strings.TrimSpace(message.Content), Timestamp: event.CreatedAt})
			}
		}
		if response := managedTaskResponse(task); response != "" {
			entries = append(entries, session.Entry{Type: "message", Role: "assistant", Content: response, Timestamp: task.FinishedAt})
		}
	}
	return entries
}

func managedTaskResponse(task mysqlstore.AgentTask) string {
	var result map[string]any
	if json.Unmarshal([]byte(task.ResultJSON), &result) != nil {
		return ""
	}
	for _, key := range []string{"response", "content"} {
		if value, ok := result[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *ManagedAdapter) failPreparedRun(ctx context.Context, dispatcher ManagedPreparedRunDispatcher, taskID uint64, request SendRequest, operationErr error) error {
	failure := PreparedRunFailure{ErrorCode: serviceErrorCode(operationErr), TraceID: request.Context.TraceID}
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), managedPreparedRunCleanupTimeout)
	defer cancelCleanup()
	_, cleanupErr := dispatcher.FailPreparedRun(cleanupCtx, taskID, failure)
	if cleanupErr == nil {
		return operationErr
	}
	var serviceErr *ServiceError
	if errors.As(operationErr, &serviceErr) {
		combined := *serviceErr
		combined.Cause = errors.Join(serviceErr.Cause, cleanupErr)
		return &combined
	}
	return &ServiceError{Code: CodeInternal, Message: managedDependencyFailure, Cause: errors.Join(operationErr, cleanupErr)}
}

func managedSendAttachIdentity(request SendRequest, taskID uint64) (OperationIdentity, error) {
	sources := append([]SessionRef(nil), request.SourceRefs...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })
	fingerprint := operationFingerprint(OperationAttach, request.Context, struct {
		Target       SessionRef   `json:"target"`
		Sources      []SessionRef `json:"sources"`
		TargetTaskID uint64       `json:"target_task_id"`
	}{request.Ref, sources, taskID})
	return newOperationIdentity(OperationAttach, request.Context, request.ReplayIdentity.OperationID+":handoff", fingerprint)
}

func (a *ManagedAdapter) Stop(ctx context.Context, request StopRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := validateManagedRef(request.Ref); err != nil {
		return errorResult(err), err
	}
	if a.store == nil || a.dispatcher == nil {
		err := unavailable("managed session dependencies")
		return errorResult(err), err
	}
	detail, err := a.loadManagedDetail(ctx, request.Context, request.Ref.Key, false)
	if err != nil {
		return errorResult(err), err
	}
	if detail.latest == nil || detail.latest.Status != agenttasks.StatusRunning && detail.latest.Status != agenttasks.StatusReady {
		return OperationResult{Session: detail.snapshot}, nil
	}
	runtimeCancelled, err := a.dispatcher.CancelRun(ctx, detail.latest.ID)
	if err != nil {
		err = normalizeManagedError(err)
		return errorResult(err), err
	}
	cancelled := agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{Source: managedCancellationSource, TraceID: request.Context.TraceID})
	eventPayload, err := encodeStopEvent(request.ReplayIdentity)
	if err != nil {
		return errorResult(err), err
	}
	persisted, err := a.store.CancelAgentTaskForSessionControl(ctx, request.Context, mysqlstore.SessionControlStopInput{TaskID: detail.latest.ID, ResultJSON: cancelled, EventPayloadJSON: eventPayload, TraceID: request.Context.TraceID})
	if err != nil {
		err = normalizeManagedError(err)
		return errorResult(err), err
	}
	if persisted.Cancelled {
		if trigger, ok := a.dispatcher.(ManagedPendingTriggerDispatcher); ok {
			trigger.TriggerPending(ctx, detail.latest.ID)
		}
	}
	if !runtimeCancelled && !persisted.Cancelled {
		return OperationResult{Session: detail.snapshot}, nil
	}
	return OperationResult{Session: detail.snapshot, RunID: detail.latest.ID}, nil
}

type managedDetail struct {
	session           mysqlstore.Session
	snapshot          SessionSnapshot
	latest            *mysqlstore.AgentTask
	pendingBaseTaskID uint64
}

func (a *ManagedAdapter) getByKey(ctx context.Context, requestContext RequestContext, key string, includeLinks bool) (SessionSnapshot, error) {
	detail, err := a.loadManagedDetail(ctx, requestContext, key, includeLinks)
	if err != nil {
		return SessionSnapshot{}, err
	}
	return detail.snapshot, nil
}

func (a *ManagedAdapter) loadManagedDetail(ctx context.Context, requestContext RequestContext, key string, includeLinks bool) (managedDetail, error) {
	item, err := a.store.GetSessionControlSessionByKey(ctx, requestContext, key)
	if err != nil {
		return managedDetail{}, normalizeManagedError(err)
	}
	tasks, err := a.store.ListLatestAgentTasksForSessions(ctx, requestContext, []uint64{item.ID})
	if err != nil {
		return managedDetail{}, err
	}
	latest := latestTasksBySession(tasks)
	events, err := a.listEvents(ctx, requestContext, tasks)
	if err != nil {
		return managedDetail{}, err
	}
	eventsByTask := eventsByTaskID(events)
	var links []SessionLink
	if includeLinks {
		storedLinks, err := a.store.ListSessionLinks(ctx, requestContext, item.ID, managedSessionLinkListLimit)
		if err != nil {
			return managedDetail{}, normalizeManagedError(err)
		}
		links = managedLinks(storedLinks, item.Session)
	}
	selected := latest[item.ID]
	pendingCount, pendingBaseTaskID, err := a.pendingState(ctx, requestContext, item.ID)
	if err != nil {
		return managedDetail{}, err
	}
	snapshot, err := configuredManagedSnapshot(item, selected, eventsByTask, pendingCount)
	if err != nil {
		return managedDetail{}, err
	}
	channels, err := a.listSessionChannels(ctx, requestContext, []uint64{item.ID})
	if err != nil {
		return managedDetail{}, normalizeManagedError(err)
	}
	applySessionChannel(&snapshot, channels[item.ID])
	snapshot.Links = links
	return managedDetail{session: item.Session, snapshot: snapshot, latest: selected, pendingBaseTaskID: pendingBaseTaskID}, nil
}

func (a *ManagedAdapter) listSessionChannels(ctx context.Context, requestContext RequestContext, sessionIDs []uint64) (map[uint64]mysqlstore.SessionChannel, error) {
	store, ok := a.store.(ManagedChannelStore)
	if !ok {
		return map[uint64]mysqlstore.SessionChannel{}, nil
	}
	return store.ListSessionChannels(ctx, requestContext, sessionIDs)
}

func applySessionChannel(snapshot *SessionSnapshot, channel mysqlstore.SessionChannel) {
	if snapshot == nil || channel.Provider == "" {
		return
	}
	snapshot.Channel = &SessionChannel{Provider: channel.Provider, AccountKey: channel.AccountKey}
}

func (a *ManagedAdapter) pendingState(ctx context.Context, requestContext RequestContext, sessionID uint64) (int, uint64, error) {
	if a.pending == nil {
		return 0, 0, nil
	}
	items, err := a.pending.List(ctx, pendinginput.Scope{TenantID: requestContext.TenantID, UserID: requestContext.UserID, SessionID: strconv.FormatUint(sessionID, 10)})
	if err != nil {
		return 0, 0, normalizeManagedError(err)
	}
	baseTaskID := uint64(0)
	if len(items) > 0 {
		baseTaskID = items[0].BaseTaskID
	}
	return len(items), baseTaskID, nil
}

func (a *ManagedAdapter) pendingStates(ctx context.Context, requestContext RequestContext, sessions []mysqlstore.Session) (map[string]pendinginput.ActiveSessionState, error) {
	if a.pending == nil || len(sessions) == 0 {
		return map[string]pendinginput.ActiveSessionState{}, nil
	}
	lookup, ok := a.pending.(pendinginput.ActiveSessionStateLookup)
	if !ok {
		return nil, unavailable("pending input batch state lookup")
	}
	sessionIDs := make([]string, 0, len(sessions))
	for _, item := range sessions {
		sessionIDs = append(sessionIDs, strconv.FormatUint(item.ID, 10))
	}
	states, err := lookup.ListActiveSessionStates(ctx, requestContext.TenantID, requestContext.UserID, sessionIDs)
	return states, normalizeManagedError(err)
}

func (a *ManagedAdapter) listLatestTasks(ctx context.Context, requestContext RequestContext, sessions []mysqlstore.Session) ([]mysqlstore.AgentTask, error) {
	if len(sessions) == 0 {
		return nil, nil
	}
	sessionIDs := make([]uint64, 0, len(sessions))
	for _, item := range sessions {
		sessionIDs = append(sessionIDs, item.ID)
	}
	tasks, err := a.store.ListLatestAgentTasksForSessions(ctx, requestContext, sessionIDs)
	return tasks, normalizeManagedError(err)
}

func (a *ManagedAdapter) listEvents(ctx context.Context, requestContext RequestContext, tasks []mysqlstore.AgentTask) ([]mysqlstore.AgentTaskEvent, error) {
	if len(tasks) == 0 {
		return nil, nil
	}
	if reader, ok := a.eventStore.(ManagedTaskEventStore); ok {
		events, err := reader.ListAgentTaskEventsForTaskRecords(ctx, requestContext, tasks)
		return events, normalizeManagedError(err)
	}
	taskIDs := make([]uint64, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	if a.eventStore != nil {
		events, err := a.eventStore.ListAgentTaskEventsForTasksComplete(ctx, requestContext, taskIDs)
		return events, normalizeManagedError(err)
	}
	events, err := a.store.ListAgentTaskEventsForTasksComplete(ctx, requestContext, taskIDs)
	return events, normalizeManagedError(err)
}

func canonicalTenantKey(key string) (SessionRef, error) {
	ref, err := ParseRef(string(SourceTenant) + ":" + key)
	if err != nil || ref.Key != key {
		return SessionRef{}, invalidRef("managed session key must be non-empty and canonical")
	}
	return ref, nil
}

func validateManagedRef(ref SessionRef) error {
	if err := validateSessionRef(ref); err != nil {
		return err
	}
	if ref.Source != SourceTenant {
		return &ServiceError{Code: CodeForbidden, Message: "managed adapter requires a tenant ref"}
	}
	return nil
}

func normalizeManagedError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return &ServiceError{Code: CodeNotFound, Message: managedResourceNotFound, Cause: err}
	}
	if errors.Is(err, mysqlstore.ErrTooManyAgentTaskEvents) {
		return &ServiceError{Code: CodeInternal, Message: managedEventWindowFailure, Cause: err}
	}
	if errors.Is(err, mysqlstore.ErrIdempotencyConflict) {
		return &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request", Cause: err}
	}
	if errors.Is(err, mysqlstore.ErrInvalidInput) || errors.Is(err, mysqlstore.ErrInvalidState) {
		return &ServiceError{Code: CodeInvalidState, Message: "managed session state does not accept this operation", Cause: err}
	}
	return &ServiceError{Code: CodeInternal, Message: managedDependencyFailure, Cause: err}
}

func latestTasksBySession(tasks []mysqlstore.AgentTask) map[uint64]*mysqlstore.AgentTask {
	latest := make(map[uint64]*mysqlstore.AgentTask)
	for index := range tasks {
		task := &tasks[index]
		if task.ParentSessionID == 0 {
			continue
		}
		if current, ok := latest[task.ParentSessionID]; !ok || taskIsLater(*task, *current) {
			latest[task.ParentSessionID] = task
		}
	}
	return latest
}

func taskIsLater(candidate, current mysqlstore.AgentTask) bool {
	if candidate.StartedAt.Equal(current.StartedAt) {
		return candidate.ID > current.ID
	}
	return candidate.StartedAt.After(current.StartedAt)
}

func eventsByTaskID(events []mysqlstore.AgentTaskEvent) map[uint64][]mysqlstore.AgentTaskEvent {
	byTask := make(map[uint64][]mysqlstore.AgentTaskEvent)
	for _, event := range events {
		byTask[event.TaskID] = append(byTask[event.TaskID], event)
	}
	for taskID := range byTask {
		sort.SliceStable(byTask[taskID], func(i, j int) bool {
			left, right := byTask[taskID][i], byTask[taskID][j]
			if left.CreatedAt.Equal(right.CreatedAt) {
				return left.ID < right.ID
			}
			return left.CreatedAt.Before(right.CreatedAt)
		})
	}
	return byTask
}

func managedSnapshot(item mysqlstore.Session, latest *mysqlstore.AgentTask, eventsByTask map[uint64][]mysqlstore.AgentTaskEvent, pendingInputCount int) SessionSnapshot {
	snapshot := SessionSnapshot{
		ID:        item.ID,
		Ref:       SessionRef{Source: SourceTenant, Key: item.SessionKey},
		Source:    sessionSource("", latest),
		Title:     item.Title,
		Model:     item.Model,
		CWD:       item.CWD,
		StartedAt: item.StartedAt,
		UpdatedAt: item.LastMessageAt,
	}
	state := SessionStateInput{LifecycleStatus: item.Status, PendingInputCount: pendingInputCount}
	if latest != nil {
		state.ActiveRunStatus = latest.Status
		state.PermissionRequired = permissionIsOutstanding(eventsByTask[latest.ID])
		state.UserInputRequired = userInputIsOutstanding(eventsByTask[latest.ID])
		if latest.Status == agenttasks.StatusRunning || latest.Status == agenttasks.StatusReady {
			snapshot.ActiveRunID = latest.ID
		}
		snapshot.UpdatedAt = latestUpdate(snapshot.UpdatedAt, latest.StartedAt, latest.FinishedAt, latestEventTime(eventsByTask[latest.ID]))
	}
	snapshot.Status = ProjectStatus(state)
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = item.StartedAt
	}
	return snapshot
}

func sessionSource(sessionMetadata string, latest *mysqlstore.AgentTask) string {
	for _, raw := range []string{sessionMetadata, agentTaskMetadata(latest)} {
		var metadata map[string]any
		if json.Unmarshal([]byte(raw), &metadata) != nil || metadata == nil {
			continue
		}
		if value, ok := metadata["source"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "desktop"
}

func agentTaskMetadata(task *mysqlstore.AgentTask) string {
	if task == nil {
		return ""
	}
	return task.MetadataJSON
}

func userInputIsOutstanding(events []mysqlstore.AgentTaskEvent) bool {
	outstanding := make(map[string]struct{})
	for _, event := range events {
		if event.EventType != agenttasks.EventUserQuestionRequest && event.EventType != agenttasks.EventUserQuestionResolved {
			continue
		}
		var payload struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		}
		if json.Unmarshal([]byte(event.PayloadJSON), &payload) != nil {
			continue
		}
		requestID := strings.TrimSpace(payload.RequestID)
		if requestID == "" {
			continue
		}
		if event.EventType == agenttasks.EventUserQuestionRequest {
			outstanding[requestID] = struct{}{}
			continue
		}
		switch strings.TrimSpace(payload.Status) {
		case agenttasks.UserQuestionAnswered, agenttasks.UserQuestionCancelled, agenttasks.UserQuestionExpired:
			delete(outstanding, requestID)
		}
	}
	return len(outstanding) > 0
}

func permissionIsOutstanding(events []mysqlstore.AgentTaskEvent) bool {
	permissionRequired := false
	for _, event := range events {
		switch strings.TrimSpace(event.EventType) {
		case agenttasks.EventPermissionRequest:
			permissionRequired = true
		case agenttasks.EventPermissionResolved:
			permissionRequired = false
		}
	}
	return permissionRequired
}

func latestEventTime(events []mysqlstore.AgentTaskEvent) time.Time {
	var latest time.Time
	for _, event := range events {
		latest = latestUpdate(latest, event.CreatedAt)
	}
	return latest
}

func latestUpdate(values ...time.Time) time.Time {
	var latest time.Time
	for _, value := range values {
		if value.After(latest) {
			latest = value
		}
	}
	return latest
}

func managedLinks(links []mysqlstore.SessionLink, target mysqlstore.Session) []SessionLink {
	out := make([]SessionLink, 0, len(links))
	for _, link := range links {
		source := Source(strings.TrimSpace(link.SourceKind))
		if source != SourceTenant && source != SourceLocal {
			continue
		}
		out = append(out, SessionLink{
			ID:           link.ID,
			Target:       SessionRef{Source: SourceTenant, Key: target.SessionKey},
			Source:       SessionRef{Source: source, Key: link.SourceSessionKey},
			RelationType: link.RelationType,
			CreatedAt:    link.CreatedAt,
		})
	}
	return out
}
