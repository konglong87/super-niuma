package sessioncontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

const (
	managedGeneratedSessionPrefix = "sc-"
	stopEventSchema               = "niuma.session-control-stop.v1"
)

type RuntimeStore interface {
	ResolveContext(context.Context) (tenant.Context, error)
	GetSessionControlSessionByKey(context.Context, string) (mysqlstore.SessionControlSession, error)
	GetAgentTaskByIdempotencyKey(context.Context, string) (mysqlstore.AgentTask, error)
	RecoverSessionControlStop(context.Context, uint64, string) (mysqlstore.SessionControlStopRecovery, error)
	ListLatestAgentTasksForSessions(context.Context, []uint64) ([]mysqlstore.AgentTask, error)
	ListAgentTaskEventsForTasksComplete(context.Context, []uint64) ([]mysqlstore.AgentTaskEvent, error)
	RecoverHandoffBatch(context.Context, agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error)
	RecordSessionControlAudit(context.Context, tenant.SessionControlAuditRequest) (mysqlstore.SessionControlAuditResult, error)
	GetSessionControlAuditByKeyHash(context.Context, string, string) (mysqlstore.AuditLog, error)
}

type RuntimeDependencies struct {
	Store         RuntimeStore
	PendingInputs pendinginput.Queue
}

// Runtime adapts authenticated tenant state into the operation-specific
// recovery and audit ports used by Service.
type Runtime struct {
	store         RuntimeStore
	pendingInputs pendinginput.Queue
}

func NewRuntime(deps RuntimeDependencies) *Runtime {
	return &Runtime{store: deps.Store, pendingInputs: deps.PendingInputs}
}

func (r *Runtime) Authorize(ctx context.Context, requestContext RequestContext, _ Operation) error {
	if r == nil || r.store == nil {
		return unavailable("session control runtime store")
	}
	resolved, err := r.store.ResolveContext(ctx)
	if err != nil {
		return normalizeRuntimeError(err)
	}
	if resolved.TenantID != requestContext.TenantID || resolved.UserID != requestContext.UserID || requestContext.ActorUserID != resolved.UserID {
		return &ServiceError{Code: CodeForbidden, Message: "request scope does not match authenticated tenant context"}
	}
	return nil
}

func (r *Runtime) AcquireOperationLock(ctx context.Context, keyHash string) (OperationUnlock, error) {
	if err := r.requireStore(); err != nil {
		return nil, err
	}
	locker, ok := r.store.(interface {
		AcquireSessionControlOperationLock(context.Context, string) (func(context.Context) error, error)
	})
	if !ok {
		return nil, unavailable("session control operation lock")
	}
	unlock, err := locker.AcquireSessionControlOperationLock(ctx, keyHash)
	if err != nil {
		return nil, &ServiceError{Code: CodeInternal, Message: "session control operation lock is unavailable", Cause: err}
	}
	if unlock == nil {
		return nil, unavailable("session control operation lock")
	}
	return OperationUnlock(unlock), nil
}

func (r *Runtime) CheckOperationKey(ctx context.Context, identity OperationIdentity) error {
	if err := r.requireStore(); err != nil {
		return err
	}
	audit, err := r.store.GetSessionControlAuditByKeyHash(ctx, "session_control."+string(identity.Operation), identity.KeyHash)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return normalizeRuntimeError(err)
	}
	metadata, err := operationMetadataFromJSON(audit.MetadataJSON)
	if err != nil {
		return err
	}
	return validateRecoveredMetadata(identity, metadata)
}

func (r *Runtime) RecoverCreate(ctx context.Context, request CreateRequest, identity OperationIdentity) (RecoveredOperation, error) {
	if err := r.requireStore(); err != nil {
		return RecoveredOperation{}, err
	}
	key := managedSessionKey(request.SessionKey, identity)
	item, err := r.store.GetSessionControlSessionByKey(ctx, key)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return RecoveredOperation{}, nil
	}
	if err != nil {
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	target := SessionRef{Source: SourceTenant, Key: item.SessionKey}
	recovered, err := recoveredFromMetadata(identity, item.MetadataJSON, target, OperationResult{})
	if err != nil || strings.TrimSpace(request.InitialText) == "" {
		return recovered, err
	}
	task, err := r.store.GetAgentTaskByIdempotencyKey(ctx, identity.OperationID)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return RecoveredOperation{}, nil
	}
	if err != nil {
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	if task.ParentSessionID != item.ID {
		return RecoveredOperation{}, invalidState("initial managed run does not match its create operation")
	}
	metadata, err := operationMetadataFromJSON(task.MetadataJSON)
	if err != nil {
		return RecoveredOperation{}, err
	}
	if err := validateRecoveredMetadata(identity, metadata); err != nil {
		return RecoveredOperation{}, err
	}
	if task.Status == agenttasks.StatusReady {
		return RecoveredOperation{}, nil
	}
	if task.Status == agenttasks.StatusFailed {
		failureErr, readErr := r.preparedRunFailure(ctx, task.ID)
		if readErr != nil {
			return RecoveredOperation{}, readErr
		}
		if failureErr != nil {
			return RecoveredOperation{}, failureErr
		}
	}
	return recovered, nil
}

func (r *Runtime) RecoverSend(ctx context.Context, request SendRequest, identity OperationIdentity) (RecoveredOperation, error) {
	if err := r.requireStore(); err != nil {
		return RecoveredOperation{}, err
	}
	item, err := r.requiredSession(ctx, request.Ref)
	if err != nil {
		return RecoveredOperation{}, err
	}
	task, err := r.store.GetAgentTaskByIdempotencyKey(ctx, identity.KeyHash)
	if err == nil {
		recovered, recoverErr := recoveredFromMetadata(identity, task.MetadataJSON, request.Ref, OperationResult{RunID: task.ID})
		if recoverErr != nil {
			return RecoveredOperation{}, recoverErr
		}
		if task.ParentSessionID != item.ID || task.IdempotencyKey != identity.KeyHash {
			return RecoveredOperation{}, &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
		}
		if task.Status == agenttasks.StatusReady {
			return RecoveredOperation{}, nil
		}
		if task.Status == agenttasks.StatusFailed {
			failureErr, readErr := r.preparedRunFailure(ctx, task.ID)
			if readErr != nil {
				return RecoveredOperation{}, readErr
			}
			if failureErr != nil {
				return RecoveredOperation{}, failureErr
			}
		}
		return recovered, nil
	}
	if !errors.Is(err, mysqlstore.ErrNotFound) {
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	if r.pendingInputs == nil {
		return RecoveredOperation{}, nil
	}
	lookup, ok := r.pendingInputs.(pendinginput.ClientInputLookup)
	if !ok {
		return RecoveredOperation{}, unavailable("pending input replay lookup")
	}
	pending, found, err := lookup.FindByClientInputID(ctx, request.Context.TenantID, request.Context.UserID, identity.KeyHash)
	if err != nil {
		if errors.Is(err, pendinginput.ErrConflict) {
			return RecoveredOperation{}, &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key resolves to conflicting pending inputs", Cause: err}
		}
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	if found {
		storedRequest := request
		storedRequest.Content, storedRequest.Attachments, storedRequest.SourceRefs = pending.Content, pending.Attachments, nil
		storedFingerprint := sendRequestFingerprint(storedRequest)
		storedSessionID := strings.TrimSpace(pending.SessionID)
		if storedFingerprint != identity.Fingerprint || storedSessionID != strconv.FormatUint(item.ID, 10) && storedSessionID != request.Ref.Key {
			return RecoveredOperation{}, &ServiceError{Code: CodeIdempotencyConflict, Message: "idempotency key was used for a different request"}
		}
		if request.RuntimeConfig() != (RuntimeConfig{}) {
			// Pending rows retain message data, while the scoped audit is the
			// durable authority for the original explicit configuration fields.
			audit, err := r.store.GetSessionControlAuditByKeyHash(ctx, "session_control."+string(OperationSend), identity.KeyHash)
			if errors.Is(err, mysqlstore.ErrNotFound) {
				return RecoveredOperation{}, invalidState("queued configuration replay requires its operation audit")
			}
			if err != nil {
				return RecoveredOperation{}, normalizeRuntimeError(err)
			}
			metadata, err := operationMetadataFromJSON(audit.MetadataJSON)
			if err != nil {
				return RecoveredOperation{}, err
			}
			if err := validateRecoveredMetadata(identity, metadata); err != nil {
				return RecoveredOperation{}, err
			}
		}
		runID := pending.DispatchedTaskID
		if runID == 0 {
			runID = pending.BaseTaskID
		}
		return RecoveredOperation{Found: true, Metadata: identity.Metadata(), Target: request.Ref, Result: OperationResult{RunID: runID}}, nil
	}
	return RecoveredOperation{}, nil
}

func (r *Runtime) preparedRunFailure(ctx context.Context, taskID uint64) (error, error) {
	events, err := r.store.ListAgentTaskEventsForTasksComplete(ctx, []uint64{taskID})
	if err != nil {
		return nil, normalizeRuntimeError(err)
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.TaskID != taskID || event.EventType != agenttasks.EventFailed {
			continue
		}
		var failure struct {
			Source    string `json:"source"`
			ErrorCode string `json:"error_code"`
		}
		if json.Unmarshal([]byte(event.PayloadJSON), &failure) != nil || failure.Source != PreparedRunFailureSource {
			continue
		}
		code := ServiceErrorCode(strings.TrimSpace(failure.ErrorCode))
		switch code {
		case CodeInvalidState, CodeNotFound, CodeForbidden, CodeIdempotencyConflict, CodeHandoffStale, CodeBudgetExceeded, CodeSchedulerUnavailable, CodeInternal:
		default:
			code = CodeInternal
		}
		return &ServiceError{Code: code, Message: "managed session run failed before launch"}, nil
	}
	return nil, nil
}

func (r *Runtime) RecoverStop(ctx context.Context, request StopRequest, identity OperationIdentity) (RecoveredOperation, error) {
	if err := r.requireStore(); err != nil {
		return RecoveredOperation{}, err
	}
	item, err := r.requiredSession(ctx, request.Ref)
	if err != nil {
		return RecoveredOperation{}, err
	}
	stored, err := r.store.RecoverSessionControlStop(ctx, item.ID, identity.KeyHash)
	if err != nil {
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	if !stored.Found {
		audit, auditErr := r.store.GetSessionControlAuditByKeyHash(ctx, "session_control."+string(OperationStop), identity.KeyHash)
		if errors.Is(auditErr, mysqlstore.ErrNotFound) {
			return RecoveredOperation{}, nil
		}
		if auditErr != nil {
			return RecoveredOperation{}, normalizeRuntimeError(auditErr)
		}
		metadata, metadataErr := operationMetadataFromJSON(audit.MetadataJSON)
		if metadataErr != nil {
			return RecoveredOperation{}, metadataErr
		}
		if metadataErr := validateRecoveredMetadata(identity, metadata); metadataErr != nil {
			return RecoveredOperation{}, metadataErr
		}
		return RecoveredOperation{Found: true, Metadata: metadata, Target: request.Ref}, nil
	}
	metadata, err := decodeStopEvent(stored.EventPayloadJSON)
	if err != nil {
		return RecoveredOperation{}, err
	}
	if err := validateRecoveredMetadata(identity, metadata); err != nil {
		return RecoveredOperation{}, err
	}
	return RecoveredOperation{Found: true, Metadata: metadata, Target: request.Ref, Result: OperationResult{RunID: stored.TaskID}}, nil
}

func (r *Runtime) RecoverAttach(ctx context.Context, request AttachRequest, identity OperationIdentity) (RecoveredOperation, error) {
	if err := r.requireStore(); err != nil {
		return RecoveredOperation{}, err
	}
	return r.recoverHandoff(ctx, request.Context, request.Target, request.TargetTaskID, request.Sources, identity)
}

func (r *Runtime) RecoverRefresh(ctx context.Context, request RefreshHandoffRequest, identity OperationIdentity) (RecoveredOperation, error) {
	if err := r.requireStore(); err != nil {
		return RecoveredOperation{}, err
	}
	return r.recoverHandoff(ctx, request.Context, request.Target, request.TargetTaskID, []SessionRef{request.Source}, identity)
}

func (r *Runtime) RecoverMonitor(context.Context, MonitorRequest, OperationIdentity) (RecoveredOperation, error) {
	return RecoveredOperation{}, nil
}

func (r *Runtime) recoverHandoff(ctx context.Context, requestContext RequestContext, target SessionRef, taskID uint64, sources []SessionRef, identity OperationIdentity) (RecoveredOperation, error) {
	item, err := r.requiredSession(ctx, target)
	if err != nil {
		return RecoveredOperation{}, err
	}
	expectedSources := make([]string, 0, len(sources))
	for _, source := range sources {
		expectedSources = append(expectedSources, source.String())
	}
	sort.Strings(expectedSources)
	stored, err := r.store.RecoverHandoffBatch(ctx, agenttasks.HandoffRecoveryInput{TenantID: requestContext.TenantID, UserID: requestContext.UserID, TargetSessionID: item.ID, TargetTaskID: taskID, OperationIdentity: identity.KeyHash, ExpectedSources: expectedSources})
	if err != nil {
		return RecoveredOperation{}, normalizeRuntimeError(err)
	}
	if !stored.Found {
		return RecoveredOperation{}, nil
	}
	metadata, err := decodeOperationMetadata(stored.OperationMetadataJSON)
	if err != nil {
		return RecoveredOperation{}, err
	}
	if err := validateRecoveredMetadata(identity, metadata); err != nil {
		return RecoveredOperation{}, err
	}
	result := OperationResult{Handoff: HandoffResult{TargetTaskID: taskID, Replayed: true}}
	for _, storedItem := range stored.Items {
		source, parseErr := ParseRef(storedItem.SourceRef)
		if parseErr != nil {
			return RecoveredOperation{}, invalidState("stored handoff source is invalid")
		}
		result.LinkIDs = append(result.LinkIDs, storedItem.LinkID)
		result.Handoff.EstimatedTokens += storedItem.EstimatedTokens
		result.Handoff.SourceResults = append(result.Handoff.SourceResults, HandoffSourceResult{Source: source, Success: true, LinkID: storedItem.LinkID, EventID: storedItem.EventID, PackageID: storedItem.PackageID, PackageSHA256Prefix: shortRuntimePrefix(storedItem.PackageSHA256), CursorPrefix: shortRuntimePrefix(storedItem.SourceCursor), EstimatedTokens: storedItem.EstimatedTokens})
	}
	return RecoveredOperation{Found: true, Metadata: metadata, Target: target, Result: result}, nil
}

func (r *Runtime) Record(ctx context.Context, record AuditRecord) (uint64, error) {
	if err := r.requireStore(); err != nil {
		return 0, err
	}
	identity := OperationIdentity{Schema: operationMetadataSchema, Operation: record.Operation, OperationID: record.OperationID, KeyHash: record.KeyHash, Fingerprint: record.Fingerprint}
	if err := validateOperationIdentity(identity); err != nil {
		return 0, err
	}
	metadataBase, err := json.Marshal(struct {
		Target     string    `json:"target,omitempty"`
		Replayed   bool      `json:"replayed"`
		Outcome    string    `json:"outcome"`
		DurationMS int64     `json:"duration_ms"`
		CreatedAt  time.Time `json:"created_at"`
	}{record.Target.String(), record.Replayed, record.Outcome, record.DurationMS, record.CreatedAt.UTC()})
	if err != nil {
		return 0, err
	}
	metadata, err := mergeOperationMetadataJSON(string(metadataBase), identity)
	if err != nil {
		return 0, err
	}
	result, err := r.store.RecordSessionControlAudit(ctx, tenant.SessionControlAuditRequest{Action: "session_control." + string(record.Operation), ResourceType: "session", ResourceID: record.OperationID, MetadataJSON: metadata, TraceID: record.Context.TraceID})
	if err != nil {
		return 0, normalizeRuntimeError(err)
	}
	if result.Audit.ID == 0 {
		return 0, invalidState("session control audit readback is incomplete")
	}
	return result.Audit.ID, nil
}

func (r *Runtime) requiredSession(ctx context.Context, ref SessionRef) (mysqlstore.SessionControlSession, error) {
	if err := r.requireStore(); err != nil {
		return mysqlstore.SessionControlSession{}, err
	}
	item, err := r.store.GetSessionControlSessionByKey(ctx, ref.Key)
	if errors.Is(err, mysqlstore.ErrNotFound) {
		return mysqlstore.SessionControlSession{}, &ServiceError{Code: CodeNotFound, Message: "managed session was not found", Cause: err}
	}
	if err != nil {
		return mysqlstore.SessionControlSession{}, normalizeRuntimeError(err)
	}
	return item, nil
}

func (r *Runtime) requireStore() error {
	if r == nil || r.store == nil {
		return unavailable("session control runtime store")
	}
	return nil
}

func recoveredFromMetadata(identity OperationIdentity, payload string, target SessionRef, result OperationResult) (RecoveredOperation, error) {
	metadata, err := operationMetadataFromJSON(payload)
	if err != nil {
		return RecoveredOperation{}, err
	}
	if err := validateRecoveredMetadata(identity, metadata); err != nil {
		return RecoveredOperation{}, err
	}
	return RecoveredOperation{Found: true, Metadata: metadata, Target: target, Result: result}, nil
}

type stopEventEnvelope struct {
	Schema    string            `json:"schema"`
	Operation OperationMetadata `json:"operation"`
}

func encodeStopEvent(identity OperationIdentity) (string, error) {
	if err := validateOperationIdentity(identity); err != nil {
		return "", err
	}
	payload, err := json.Marshal(stopEventEnvelope{Schema: stopEventSchema, Operation: identity.Metadata()})
	if err != nil {
		return "", fmt.Errorf("encode stop event: %w", err)
	}
	return string(payload), nil
}

func decodeStopEvent(payload string) (OperationMetadata, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(payload))
	decoder.DisallowUnknownFields()
	var event stopEventEnvelope
	if err := decoder.Decode(&event); err != nil || event.Schema != stopEventSchema {
		return OperationMetadata{}, invalidState("session control stop event is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return OperationMetadata{}, invalidState("session control stop event contains trailing data")
	}
	if err := validateOperationMetadata(event.Operation); err != nil {
		return OperationMetadata{}, err
	}
	return event.Operation, nil
}

func managedSessionKey(requested string, identity OperationIdentity) string {
	if strings.TrimSpace(requested) != "" {
		return requested
	}
	return managedGeneratedSessionPrefix + identity.KeyHash
}

func normalizeRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	if errors.Is(err, mysqlstore.ErrInvalidInput) || errors.Is(err, mysqlstore.ErrInvalidState) {
		return &ServiceError{Code: CodeInvalidState, Message: "session control runtime state is invalid", Cause: err}
	}
	return &ServiceError{Code: CodeInternal, Message: "session control runtime dependency failed", Cause: err}
}

func shortRuntimePrefix(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
