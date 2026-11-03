package sessioncontrol

import (
	"context"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

func TestRuntimeAuthorizationUsesAuthenticatedTenantScope(t *testing.T) {
	runtime := NewRuntime(RuntimeDependencies{Store: &runtimeStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}}})
	if err := runtime.Authorize(context.Background(), RequestContext{TenantID: 7, UserID: 11, ActorUserID: 11}, OperationSend); err != nil {
		t.Fatal(err)
	}
	for name, scope := range map[string]RequestContext{
		"tenant": {TenantID: 8, UserID: 11, ActorUserID: 11},
		"user":   {TenantID: 7, UserID: 12, ActorUserID: 12},
		"actor":  {TenantID: 7, UserID: 11, ActorUserID: 12},
	} {
		t.Run(name, func(t *testing.T) {
			assertServiceErrorCode(t, runtime.Authorize(context.Background(), scope, OperationSend), CodeForbidden)
		})
	}
}

func TestRuntimeMissingStoreFailsClosed(t *testing.T) {
	runtime := NewRuntime(RuntimeDependencies{})
	identity := managedOperationIdentity(t, OperationCreate, "missing-store")
	_, err := runtime.RecoverCreate(context.Background(), CreateRequest{Context: managedRequestContext(), SessionKey: "managed-1"}, identity)
	assertServiceErrorCode(t, err, CodeInternal)
	_, err = runtime.Record(context.Background(), AuditRecord{Operation: OperationCreate, Context: managedRequestContext(), OperationID: identity.OperationID, KeyHash: identity.KeyHash, Fingerprint: identity.Fingerprint})
	assertServiceErrorCode(t, err, CodeInternal)
}

func TestRuntimeRecoverCreateValidatesStoredOperationMetadata(t *testing.T) {
	request := CreateRequest{Context: managedRequestContext(), SessionKey: "managed-1", Title: "Managed", IdempotencyKey: "create-key"}
	fingerprint := operationFingerprint(OperationCreate, request.Context, struct {
		SessionKey string `json:"session_key"`
		Title      string `json:"title"`
		Model      string `json:"model"`
		CWD        string `json:"cwd"`
	}{request.SessionKey, request.Title, request.Model, request.CWD})
	identity, err := newOperationIdentity(OperationCreate, request.Context, request.IdempotencyKey, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := mergeOperationMetadataJSON("", identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}, session: mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "managed-1"}, MetadataJSON: metadata}}
	runtime := NewRuntime(RuntimeDependencies{Store: store})

	recovered, err := runtime.RecoverCreate(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Target != tenantRef("managed-1") {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
	changed, err := newOperationIdentity(OperationCreate, request.Context, request.IdempotencyKey, operationFingerprint(OperationCreate, request.Context, struct{ Title string }{Title: "Changed"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = runtime.RecoverCreate(context.Background(), request, changed)
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)
	if store.sessionOwner != [2]uint64{7, 11} {
		t.Fatalf("session lookup owner = %v", store.sessionOwner)
	}
}

func TestRuntimeRecoverCreateReplaysPreparedFailureCode(t *testing.T) {
	request := CreateRequest{Context: managedRequestContext(), SessionKey: "managed-1", InitialText: "start", IdempotencyKey: "create-key"}
	identity := managedOperationIdentity(t, OperationCreate, request.IdempotencyKey)
	metadata, err := mergeOperationMetadataJSON(`{"cwd":"/repo"}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		session: mysqlstore.SessionControlSession{
			Session:      mysqlstore.Session{ID: 41, SessionKey: request.SessionKey},
			MetadataJSON: metadata,
		},
		task: mysqlstore.AgentTask{ID: 51, ParentSessionID: 41, IdempotencyKey: identity.OperationID, MetadataJSON: metadata, Status: agenttasks.StatusFailed},
		events: []mysqlstore.AgentTaskEvent{{
			ID: 61, TaskID: 51, EventType: agenttasks.EventFailed,
			PayloadJSON: `{"source":"session_control_prelaunch","error_code":"budget_exceeded"}`,
		}},
	}

	_, err = NewRuntime(RuntimeDependencies{Store: store}).RecoverCreate(context.Background(), request, identity)
	assertServiceErrorCode(t, err, CodeBudgetExceeded)

	store.events[0].PayloadJSON = `{"source":"runner","error_code":"provider_error"}`
	recovered, err := NewRuntime(RuntimeDependencies{Store: store}).RecoverCreate(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Target != tenantRef(request.SessionKey) {
		t.Fatalf("normal post-launch create failure replay changed: recovered=%+v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverSendUsesTaskOrPendingInputAuthority(t *testing.T) {
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue", IdempotencyKey: "send-key"}
	fingerprint := operationFingerprint(OperationSend, request.Context, struct {
		Ref     SessionRef `json:"ref"`
		Content string     `json:"content"`
	}{request.Ref, request.Content})
	identity, err := newOperationIdentity(OperationSend, request.Context, request.IdempotencyKey, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := mergeOperationMetadataJSON(`{"cwd":"/repo"}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		session:  mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}},
		task:     mysqlstore.AgentTask{ID: 51, ParentSessionID: 41, IdempotencyKey: identity.KeyHash, MetadataJSON: metadata},
	}
	runtime := NewRuntime(RuntimeDependencies{Store: store})
	recovered, err := runtime.RecoverSend(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.RunID != 51 {
		t.Fatalf("task recovery=%#v err=%v", recovered, err)
	}

	queue := pendinginput.NewMemoryQueue()
	store.task = mysqlstore.AgentTask{}
	store.taskErr = mysqlstore.ErrNotFound
	store.latest = []mysqlstore.AgentTask{{ID: 61, ParentSessionID: 41, Status: agenttasks.StatusRunning}}
	if _, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "alpha", BaseTaskID: 61}, ClientInputID: identity.KeyHash, Content: request.Content}); err != nil {
		t.Fatal(err)
	}
	runtime = NewRuntime(RuntimeDependencies{Store: store, PendingInputs: queue})
	recovered, err = runtime.RecoverSend(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.RunID != 61 {
		t.Fatalf("pending recovery=%#v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverSendReplaysPreparedFailureCode(t *testing.T) {
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue", SourceRefs: []SessionRef{tenantRef("source")}, IdempotencyKey: "send-key"}
	identity := sendOperationIdentityForTest(t, request)
	metadata, err := mergeOperationMetadataJSON(`{"cwd":"/repo"}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		session:  mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}},
		task:     mysqlstore.AgentTask{ID: 51, ParentSessionID: 41, IdempotencyKey: identity.KeyHash, MetadataJSON: metadata, Status: agenttasks.StatusFailed},
		events: []mysqlstore.AgentTaskEvent{{
			ID: 61, TaskID: 51, EventType: agenttasks.EventFailed,
			PayloadJSON: `{"source":"session_control_prelaunch","error_code":"budget_exceeded"}`,
		}},
	}

	_, err = NewRuntime(RuntimeDependencies{Store: store}).RecoverSend(context.Background(), request, identity)
	assertServiceErrorCode(t, err, CodeBudgetExceeded)

	store.events[0].PayloadJSON = `{"source":"runner","error_code":"provider_error"}`
	recovered, err := NewRuntime(RuntimeDependencies{Store: store}).RecoverSend(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.RunID != 51 {
		t.Fatalf("normal post-launch failure replay changed: recovered=%+v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverSendFindsTerminalPendingInputGlobally(t *testing.T) {
	request := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue", IdempotencyKey: "send-terminal"}
	request.ReplayIdentity = sendOperationIdentityForTest(t, request)
	queue := pendinginput.NewMemoryQueue()
	scope := pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "41", BaseTaskID: 61}
	item, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: request.ReplayIdentity.KeyHash, Content: request.Content, GlobalClientInputID: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := queue.ClaimNext(context.Background(), scope); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := queue.MarkSent(context.Background(), item.ID, 77); err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}, session: mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}}, taskErr: mysqlstore.ErrNotFound, latest: []mysqlstore.AgentTask{{ID: 61, ParentSessionID: 41, Status: agenttasks.StatusCompleted}}}
	recovered, err := NewRuntime(RuntimeDependencies{Store: store, PendingInputs: queue}).RecoverSend(context.Background(), request, request.ReplayIdentity)
	if err != nil || !recovered.Found || recovered.Result.RunID != 77 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverSendSameKeyDifferentTargetConflictsGlobally(t *testing.T) {
	stored := SendRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), Content: "continue", IdempotencyKey: "same-key"}
	stored.ReplayIdentity = sendOperationIdentityForTest(t, stored)
	request := stored
	request.Ref = tenantRef("beta")
	request.ReplayIdentity = sendOperationIdentityForTest(t, request)
	queue := pendinginput.NewMemoryQueue()
	_, err := queue.Add(context.Background(), pendinginput.NewInput{Scope: pendinginput.Scope{TenantID: 7, UserID: 11, SessionID: "41", BaseTaskID: 61}, ClientInputID: stored.ReplayIdentity.KeyHash, Content: stored.Content, GlobalClientInputID: true})
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}, session: mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 42, SessionKey: "beta"}}, taskErr: mysqlstore.ErrNotFound}
	_, err = NewRuntime(RuntimeDependencies{Store: store, PendingInputs: queue}).RecoverSend(context.Background(), request, request.ReplayIdentity)
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)
}

func sendOperationIdentityForTest(t *testing.T, request SendRequest) OperationIdentity {
	t.Helper()
	fingerprint := operationFingerprint(OperationSend, request.Context, struct {
		Ref         SessionRef              `json:"ref"`
		Content     string                  `json:"content"`
		Attachments []agenttasks.Attachment `json:"attachments,omitempty"`
		SourceRefs  []SessionRef            `json:"source_refs,omitempty"`
	}{request.Ref, request.Content, request.Attachments, request.SourceRefs})
	identity, err := newOperationIdentity(OperationSend, request.Context, request.IdempotencyKey, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestRuntimeRecoverAttachValidatesHandoffMetadata(t *testing.T) {
	request := AttachRequest{Context: managedRequestContext(), Target: tenantRef("target"), TargetTaskID: 71, TargetContextWindowTokens: 16_384, Sources: []SessionRef{tenantRef("source")}, IdempotencyKey: "attach-key"}
	fingerprint := operationFingerprint(OperationAttach, request.Context, struct {
		Target                    SessionRef   `json:"target"`
		Sources                   []SessionRef `json:"sources"`
		RelationType              string       `json:"relation_type"`
		TargetTaskID              uint64       `json:"target_task_id"`
		TargetContextWindowTokens int          `json:"target_context_window_tokens"`
	}{request.Target, request.Sources, request.RelationType, request.TargetTaskID, request.TargetContextWindowTokens})
	identity, err := newOperationIdentity(OperationAttach, request.Context, request.IdempotencyKey, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := encodeOperationMetadata(identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		session:  mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "target"}},
		handoff:  agenttasks.HandoffRecoveryResult{Found: true, OperationMetadataJSON: metadata, Items: []agenttasks.HandoffRecoveredItem{{LinkID: 81, EventID: 91, SourceRef: "tenant:source", PackageID: "pkg", PackageSHA256: "abcdef", SourceCursor: "message:1", EstimatedTokens: 10}}},
	}
	runtime := NewRuntime(RuntimeDependencies{Store: store})
	recovered, err := runtime.RecoverAttach(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.LinkIDs[0] != 81 || recovered.Result.Handoff.SourceResults[0].EventID != 91 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverStopFindsOriginalRunAfterNewerRun(t *testing.T) {
	request := StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), IdempotencyKey: "stop-key"}
	identity := managedOperationIdentity(t, OperationStop, request.IdempotencyKey)
	payload, err := encodeStopEvent(identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved: tenant.Context{TenantID: 7, UserID: 11},
		session:  mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}},
		latest:   []mysqlstore.AgentTask{{ID: 62, ParentSessionID: 41, Status: agenttasks.StatusRunning}},
		events:   []mysqlstore.AgentTaskEvent{{ID: 91, TaskID: 61, EventType: agenttasks.EventSessionControlStop, PayloadJSON: payload}},
	}
	runtime := NewRuntime(RuntimeDependencies{Store: store})
	recovered, err := runtime.RecoverStop(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Result.RunID != 61 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestRuntimeRecoverStopUsesAuditForNoActiveRun(t *testing.T) {
	request := StopRequest{Context: managedRequestContext(), Ref: tenantRef("alpha"), IdempotencyKey: "stop-idle"}
	identity := managedOperationIdentity(t, OperationStop, request.IdempotencyKey)
	metadata, err := mergeOperationMetadataJSON(`{"target":"tenant:alpha","outcome":"succeeded"}`, identity)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{
		resolved:   tenant.Context{TenantID: 7, UserID: 11},
		session:    mysqlstore.SessionControlSession{Session: mysqlstore.Session{ID: 41, SessionKey: "alpha"}},
		auditByKey: mysqlstore.AuditLog{ID: 99, MetadataJSON: metadata},
	}
	recovered, err := NewRuntime(RuntimeDependencies{Store: store}).RecoverStop(context.Background(), request, identity)
	if err != nil || !recovered.Found || recovered.Target != request.Ref {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestRuntimeOperationKeyGuardUsesGlobalCompletedAudit(t *testing.T) {
	request := CreateRequest{Context: managedRequestContext(), SessionKey: "second", IdempotencyKey: "same-key"}
	identity, err := newOperationIdentity(OperationCreate, request.Context, request.IdempotencyKey, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	first, err := newOperationIdentity(OperationCreate, request.Context, request.IdempotencyKey, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := mergeOperationMetadataJSON(`{"target":"tenant:first"}`, first)
	if err != nil {
		t.Fatal(err)
	}
	store := &runtimeStoreFake{auditByKey: mysqlstore.AuditLog{ID: 99, MetadataJSON: metadata}}
	err = NewRuntime(RuntimeDependencies{Store: store}).CheckOperationKey(context.Background(), identity)
	assertServiceErrorCode(t, err, CodeIdempotencyConflict)
	if store.auditLookupAction != "session_control.create" || store.auditLookupKeyHash != identity.KeyHash {
		t.Fatalf("audit lookup action=%q key=%q", store.auditLookupAction, store.auditLookupKeyHash)
	}
}

func TestRuntimeAuditIsOperationIdempotentAndRedacted(t *testing.T) {
	store := &runtimeStoreFake{resolved: tenant.Context{TenantID: 7, UserID: 11}, audit: mysqlstore.SessionControlAuditResult{Audit: mysqlstore.AuditLog{ID: 99}, Replayed: true}}
	runtime := NewRuntime(RuntimeDependencies{Store: store})
	identity, err := newOperationIdentity(OperationSend, managedRequestContext(), "private-key", strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	record := AuditRecord{Operation: OperationSend, Context: managedRequestContext(), Target: tenantRef("alpha"), OperationID: identity.OperationID, KeyHash: identity.KeyHash, Fingerprint: identity.Fingerprint}
	id, err := runtime.Record(context.Background(), record)
	if err != nil || id != 99 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	if store.auditInput.ResourceID != record.OperationID || store.auditInput.Action != "session_control.send" || store.auditInput.MetadataJSON == "" {
		t.Fatalf("audit input = %#v", store.auditInput)
	}
	if containsAny(store.auditInput.MetadataJSON, "private", "content", "idempotency_key") {
		t.Fatalf("audit metadata leaked content: %s", store.auditInput.MetadataJSON)
	}
}

type runtimeStoreFake struct {
	resolved           tenant.Context
	session            mysqlstore.SessionControlSession
	sessionErr         error
	sessionOwner       [2]uint64
	task               mysqlstore.AgentTask
	taskErr            error
	latest             []mysqlstore.AgentTask
	events             []mysqlstore.AgentTaskEvent
	stopRecovery       mysqlstore.SessionControlStopRecovery
	handoff            agenttasks.HandoffRecoveryResult
	audit              mysqlstore.SessionControlAuditResult
	auditInput         mysqlstore.AuditLogInput
	auditByKey         mysqlstore.AuditLog
	auditLookupAction  string
	auditLookupKeyHash string
}

func (f *runtimeStoreFake) ResolveContext(context.Context) (tenant.Context, error) {
	return f.resolved, nil
}
func (f *runtimeStoreFake) GetSessionControlSessionByKey(_ context.Context, key string) (mysqlstore.SessionControlSession, error) {
	f.sessionOwner = [2]uint64{f.resolved.TenantID, f.resolved.UserID}
	if f.sessionErr != nil {
		return mysqlstore.SessionControlSession{}, f.sessionErr
	}
	if f.session.ID == 0 || f.session.SessionKey != key {
		return mysqlstore.SessionControlSession{}, mysqlstore.ErrNotFound
	}
	return f.session, nil
}
func (f *runtimeStoreFake) GetAgentTaskByIdempotencyKey(context.Context, string) (mysqlstore.AgentTask, error) {
	if f.taskErr != nil {
		return mysqlstore.AgentTask{}, f.taskErr
	}
	if f.task.ID == 0 {
		return mysqlstore.AgentTask{}, mysqlstore.ErrNotFound
	}
	return f.task, nil
}
func (f *runtimeStoreFake) RecoverSessionControlStop(context.Context, uint64, string) (mysqlstore.SessionControlStopRecovery, error) {
	if f.stopRecovery.Found {
		return f.stopRecovery, nil
	}
	for _, event := range f.events {
		if event.EventType == agenttasks.EventSessionControlStop {
			return mysqlstore.SessionControlStopRecovery{Found: true, TaskID: event.TaskID, EventID: event.ID, EventPayloadJSON: event.PayloadJSON}, nil
		}
	}
	return mysqlstore.SessionControlStopRecovery{}, nil
}
func (f *runtimeStoreFake) ListLatestAgentTasksForSessions(context.Context, []uint64) ([]mysqlstore.AgentTask, error) {
	return f.latest, nil
}
func (f *runtimeStoreFake) ListAgentTaskEventsForTasksComplete(context.Context, []uint64) ([]mysqlstore.AgentTaskEvent, error) {
	return f.events, nil
}
func (f *runtimeStoreFake) RecoverHandoffBatch(context.Context, agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error) {
	return f.handoff, nil
}
func (f *runtimeStoreFake) RecordSessionControlAudit(_ context.Context, input tenant.SessionControlAuditRequest) (mysqlstore.SessionControlAuditResult, error) {
	f.auditInput = mysqlstore.AuditLogInput{Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID, MetadataJSON: input.MetadataJSON, TraceID: input.TraceID}
	return f.audit, nil
}
func (f *runtimeStoreFake) GetSessionControlAuditByKeyHash(_ context.Context, action, keyHash string) (mysqlstore.AuditLog, error) {
	f.auditLookupAction, f.auditLookupKeyHash = action, keyHash
	if f.auditByKey.ID == 0 {
		return mysqlstore.AuditLog{}, mysqlstore.ErrNotFound
	}
	return f.auditByKey, nil
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
