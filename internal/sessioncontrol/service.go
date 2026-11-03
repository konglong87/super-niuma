package sessioncontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// Dependencies are the infrastructure boundaries used by Service. Production
// wiring is intentionally deferred; tests and future transports inject ports.
type Dependencies struct {
	Managed           ManagedSessionPort
	Local             LocalSessionPort
	Authorizer        AuthorizationPort
	CreateRecovery    CreateRecoveryPort
	SendRecovery      SendRecoveryPort
	StopRecovery      StopRecoveryPort
	AttachRecovery    AttachRecoveryPort
	RefreshRecovery   RefreshRecoveryPort
	MonitorRecovery   MonitorRecoveryPort
	Monitor           MonitorPort
	Links             LinkPort
	Handoff           HandoffPort
	Audit             AuditPort
	OperationLock     OperationLockPort
	OperationKeyGuard OperationKeyGuardPort
	Clock             Clock
}

type Service struct{ deps Dependencies }

func NewService(deps Dependencies) *Service { return &Service{deps: deps} }

func (s *Service) Create(ctx context.Context, request CreateRequest) (OperationResult, error) {
	fingerprint := operationFingerprint(OperationCreate, request.Context, struct {
		SessionKey     string `json:"session_key"`
		Title          string `json:"title"`
		Model          string `json:"model"`
		CWD            string `json:"cwd"`
		InitialText    string `json:"initial_text"`
		Provider       string `json:"provider,omitempty"`
		PermissionMode string `json:"permission_mode,omitempty"`
		Effort         string `json:"effort,omitempty"`
		PromptMode     string `json:"prompt_mode,omitempty"`
	}{request.SessionKey, request.Title, request.Model, request.CWD, request.InitialText, request.Provider, request.PermissionMode, request.Effort, request.PromptMode})
	return s.executeMutation(ctx, request.Context, OperationCreate, request.IdempotencyKey, fingerprint, SessionRef{}, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.CreateRecovery == nil {
			return RecoveredOperation{}, unavailable("create recovery port")
		}
		request.ReplayIdentity = identity
		return s.deps.CreateRecovery.RecoverCreate(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		request.ReplayIdentity = identity
		snapshot, err := s.deps.Managed.Create(ctx, request)
		if err != nil {
			return OperationResult{}, SessionRef{}, err
		}
		return OperationResult{Session: snapshot}, snapshot.Ref, nil
	})
}

func (s *Service) List(ctx context.Context, request ListRequest) ([]SessionSnapshot, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return nil, err
	}
	if s.deps.Authorizer == nil {
		return nil, unavailable("session control authorization")
	}
	if err := s.deps.Authorizer.Authorize(ctx, request.Context, OperationList); err != nil {
		return nil, err
	}
	switch request.Source {
	case SourceTenant:
		if s.deps.Managed == nil {
			return nil, unavailable("managed session port")
		}
		return s.deps.Managed.List(ctx, request)
	case SourceLocal:
		if s.deps.Local == nil {
			return nil, unavailable("local session port")
		}
		return s.deps.Local.List(ctx, request)
	default:
		return nil, invalidState("session source must be tenant or local")
	}
}

func (s *Service) Get(ctx context.Context, request GetRequest) (SessionSnapshot, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return SessionSnapshot{}, err
	}
	if err := validateSessionRef(request.Ref); err != nil {
		return SessionSnapshot{}, err
	}
	if s.deps.Authorizer == nil {
		return SessionSnapshot{}, unavailable("session control authorization")
	}
	if err := s.deps.Authorizer.Authorize(ctx, request.Context, OperationGet); err != nil {
		return SessionSnapshot{}, err
	}
	switch request.Ref.Source {
	case SourceTenant:
		if s.deps.Managed == nil {
			return SessionSnapshot{}, unavailable("managed session port")
		}
		return s.deps.Managed.Get(ctx, request)
	case SourceLocal:
		if s.deps.Local == nil {
			return SessionSnapshot{}, unavailable("local session port")
		}
		return s.deps.Local.Get(ctx, request)
	default:
		return SessionSnapshot{}, invalidState("session source must be tenant or local")
	}
}

func (s *Service) Send(ctx context.Context, request SendRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Ref); err != nil {
		return errorResult(err), err
	}
	for _, source := range request.SourceRefs {
		if err := validateSessionRef(source); err != nil {
			return errorResult(err), err
		}
	}
	fingerprint := sendRequestFingerprint(request)
	return s.executeMutation(ctx, request.Context, OperationSend, request.IdempotencyKey, fingerprint, request.Ref, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.SendRecovery == nil {
			return RecoveredOperation{}, unavailable("send recovery port")
		}
		request.ReplayIdentity = identity
		return s.deps.SendRecovery.RecoverSend(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		request.ReplayIdentity = identity
		result, err := s.deps.Managed.Send(ctx, request)
		return result, request.Ref, err
	})
}

func (s *Service) Compact(ctx context.Context, request CompactRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Ref); err != nil {
		return errorResult(err), err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		err := invalidState("idempotency key is required")
		return errorResult(err), err
	}
	if s.deps.Authorizer == nil {
		err := unavailable("session control authorization")
		return errorResult(err), err
	}
	if err := s.deps.Authorizer.Authorize(ctx, request.Context, OperationCompact); err != nil {
		return errorResult(err), err
	}
	compactor, ok := s.deps.Managed.(ManagedCompactionPort)
	if !ok {
		err := unavailable("managed compaction port")
		return errorResult(err), err
	}
	identity, err := newOperationIdentity(OperationCompact, request.Context, request.IdempotencyKey, operationFingerprint(OperationCompact, request.Context, struct {
		Ref SessionRef `json:"ref"`
	}{request.Ref}))
	if err != nil {
		return errorResult(err), err
	}
	request.ReplayIdentity = identity
	result, err := compactor.Compact(ctx, request)
	if err != nil {
		return resultWithError(identity.OperationID, result, err), err
	}
	result.OperationID = identity.OperationID
	result.Session.Ref = request.Ref
	return result, nil
}

func (s *Service) Stop(ctx context.Context, request StopRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Ref); err != nil {
		return errorResult(err), err
	}
	fingerprint := operationFingerprint(OperationStop, request.Context, struct {
		Ref SessionRef `json:"ref"`
	}{request.Ref})
	return s.executeMutation(ctx, request.Context, OperationStop, request.IdempotencyKey, fingerprint, request.Ref, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.StopRecovery == nil {
			return RecoveredOperation{}, unavailable("stop recovery port")
		}
		request.ReplayIdentity = identity
		return s.deps.StopRecovery.RecoverStop(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		request.ReplayIdentity = identity
		result, err := s.deps.Managed.Stop(ctx, request)
		return result, request.Ref, err
	})
}

func (s *Service) Attach(ctx context.Context, request AttachRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		err := invalidState("idempotency key is required")
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Target); err != nil {
		return errorResult(err), err
	}
	if request.TargetTaskID == 0 || request.TargetContextWindowTokens <= 0 {
		err := invalidState("target task ID and target context window are required")
		return errorResult(err), err
	}
	if len(request.Sources) == 0 {
		err := invalidState("at least one handoff source is required")
		return errorResult(err), err
	}
	for _, source := range request.Sources {
		if err := validateSessionRef(source); err != nil {
			return errorResult(err), err
		}
	}
	fingerprintSources := append([]SessionRef(nil), request.Sources...)
	sort.SliceStable(fingerprintSources, func(i, j int) bool { return fingerprintSources[i].String() < fingerprintSources[j].String() })
	fingerprint := operationFingerprint(OperationAttach, request.Context, struct {
		Target                    SessionRef   `json:"target"`
		Sources                   []SessionRef `json:"sources"`
		RelationType              string       `json:"relation_type"`
		TargetTaskID              uint64       `json:"target_task_id"`
		TargetContextWindowTokens int          `json:"target_context_window_tokens"`
	}{request.Target, fingerprintSources, request.RelationType, request.TargetTaskID, request.TargetContextWindowTokens})
	return s.executeMutation(ctx, request.Context, OperationAttach, request.IdempotencyKey, fingerprint, request.Target, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.AttachRecovery == nil {
			return RecoveredOperation{}, unavailable("attach recovery port")
		}
		request.ReplayIdentity = identity
		return s.deps.AttachRecovery.RecoverAttach(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		request.ReplayIdentity = identity
		request.OperationIdentity = identity.KeyHash
		if s.deps.Handoff == nil {
			return OperationResult{}, request.Target, unavailable("session handoff port")
		}
		handoffResult, err := s.deps.Handoff.Attach(ctx, request)
		if err != nil {
			return OperationResult{Handoff: handoffResult}, request.Target, err
		}
		linkIDs := make([]uint64, 0, len(handoffResult.SourceResults))
		for _, source := range handoffResult.SourceResults {
			if source.LinkID != 0 {
				linkIDs = append(linkIDs, source.LinkID)
			}
		}
		return OperationResult{Session: SessionSnapshot{Ref: request.Target}, LinkIDs: linkIDs, Handoff: handoffResult}, request.Target, nil
	})
}

func (s *Service) ReadHandoff(ctx context.Context, request HandoffReadRequest) (HandoffResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return HandoffResult{}, err
	}
	if err := rejectLocalTarget(request.Target); err != nil {
		return HandoffResult{}, err
	}
	if request.TargetTaskID == 0 || request.EventID == 0 || s.deps.Handoff == nil {
		return HandoffResult{}, invalidState("handoff read request is incomplete")
	}
	return s.deps.Handoff.Read(ctx, request)
}

func (s *Service) RefreshHandoff(ctx context.Context, request RefreshHandoffRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Target); err != nil {
		return errorResult(err), err
	}
	if err := validateSessionRef(request.Source); err != nil {
		return errorResult(err), err
	}
	if request.PreviousTarget != (SessionRef{}) {
		if err := rejectLocalTarget(request.PreviousTarget); err != nil {
			return errorResult(err), err
		}
		if request.PreviousTargetTaskID == 0 {
			err := invalidState("previous handoff target task ID is required")
			return errorResult(err), err
		}
	}
	if request.TargetTaskID == 0 || request.TargetContextWindowTokens <= 0 || request.PreviousEventID == 0 || strings.TrimSpace(request.PreviousPackageID) == "" {
		err := invalidState("handoff refresh request is incomplete")
		return errorResult(err), err
	}
	fingerprint := operationFingerprint(OperationHandoffRefresh, request.Context, struct {
		Target                    SessionRef `json:"target"`
		TargetTaskID              uint64     `json:"target_task_id"`
		TargetContextWindowTokens int        `json:"target_context_window_tokens"`
		Source                    SessionRef `json:"source"`
		PreviousPackageID         string     `json:"previous_package_id"`
		PreviousEventID           uint64     `json:"previous_event_id"`
		PreviousTarget            SessionRef `json:"previous_target"`
		PreviousTargetTaskID      uint64     `json:"previous_target_task_id"`
	}{request.Target, request.TargetTaskID, request.TargetContextWindowTokens, request.Source, request.PreviousPackageID, request.PreviousEventID, request.PreviousTarget, request.PreviousTargetTaskID})
	return s.executeMutation(ctx, request.Context, OperationHandoffRefresh, request.IdempotencyKey, fingerprint, request.Target, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.RefreshRecovery == nil {
			return RecoveredOperation{}, unavailable("handoff refresh recovery port")
		}
		request.ReplayIdentity = identity
		return s.deps.RefreshRecovery.RecoverRefresh(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		request.ReplayIdentity = identity
		request.OperationIdentity = identity.KeyHash
		if s.deps.Handoff == nil {
			return OperationResult{}, request.Target, unavailable("session handoff port")
		}
		handoffResult, err := s.deps.Handoff.Refresh(ctx, request)
		if err != nil {
			return OperationResult{Handoff: handoffResult}, request.Target, err
		}
		linkIDs := make([]uint64, 0, len(handoffResult.SourceResults))
		for _, source := range handoffResult.SourceResults {
			if source.LinkID != 0 {
				linkIDs = append(linkIDs, source.LinkID)
			}
		}
		return OperationResult{Session: SessionSnapshot{Ref: request.Target}, LinkIDs: linkIDs, Handoff: handoffResult}, request.Target, nil
	})
}

func (s *Service) Monitor(ctx context.Context, request MonitorRequest) (OperationResult, error) {
	if err := validateRequestContext(request.Context); err != nil {
		return errorResult(err), err
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		err := invalidState("idempotency key is required")
		return errorResult(err), err
	}
	if err := rejectLocalTarget(request.Target); err != nil {
		return errorResult(err), err
	}
	if len(request.Sources) == 0 {
		err := invalidState("at least one monitor source is required")
		return errorResult(err), err
	}
	for _, source := range request.Sources {
		if err := validateSessionRef(source); err != nil {
			return errorResult(err), err
		}
	}
	if request.IntervalSeconds < SessionMonitorMinIntervalSeconds || request.IntervalSeconds > SessionMonitorMaxIntervalSeconds {
		err := invalidState("monitor interval is outside the supported range")
		return errorResult(err), err
	}
	if strings.TrimSpace(request.Channel) == "" {
		err := invalidState("monitor channel is required")
		return errorResult(err), err
	}
	fingerprintSources := append([]SessionRef(nil), request.Sources...)
	sort.SliceStable(fingerprintSources, func(i, j int) bool { return fingerprintSources[i].String() < fingerprintSources[j].String() })
	fingerprint := operationFingerprint(OperationMonitor, request.Context, struct {
		Target          SessionRef   `json:"target"`
		Sources         []SessionRef `json:"sources"`
		IntervalSeconds int          `json:"interval_seconds"`
		Channel         string       `json:"channel"`
	}{request.Target, fingerprintSources, request.IntervalSeconds, strings.TrimSpace(request.Channel)})
	return s.executeMutation(ctx, request.Context, OperationMonitor, request.IdempotencyKey, fingerprint, request.Target, func(identity OperationIdentity) (RecoveredOperation, error) {
		if s.deps.MonitorRecovery == nil {
			return RecoveredOperation{}, &ServiceError{Code: CodeSchedulerUnavailable, Message: "session monitor scheduler is unavailable"}
		}
		request.ReplayIdentity = identity
		return s.deps.MonitorRecovery.RecoverMonitor(ctx, request, identity)
	}, func(identity OperationIdentity) (OperationResult, SessionRef, error) {
		if s.deps.Monitor == nil {
			err := &ServiceError{Code: CodeSchedulerUnavailable, Message: "session monitor scheduler is unavailable"}
			return errorResult(err), request.Target, err
		}
		request.ReplayIdentity = identity
		result, err := s.deps.Monitor.Monitor(ctx, request)
		return result, request.Target, err
	})
}

type recoverOperationFunc func(OperationIdentity) (RecoveredOperation, error)
type applyOperationFunc func(OperationIdentity) (OperationResult, SessionRef, error)

// executeMutation keeps orchestration transport-neutral while every adapter
// recovers from its own authoritative session/task/link/schedule record.
func (s *Service) executeMutation(ctx context.Context, requestContext RequestContext, operation Operation, idempotencyKey, fingerprint string, target SessionRef, recoverOperation recoverOperationFunc, apply applyOperationFunc) (OperationResult, error) {
	startedAt := s.now()
	if err := validateRequestContext(requestContext); err != nil {
		return errorResult(err), err
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		err := invalidState("idempotency key is required")
		return errorResult(err), err
	}
	if s.deps.Managed == nil || s.deps.Authorizer == nil || s.deps.Audit == nil || recoverOperation == nil || apply == nil {
		err := unavailable("session control mutation dependency")
		return errorResult(err), err
	}
	if err := s.deps.Authorizer.Authorize(ctx, requestContext, operation); err != nil {
		return errorResult(err), err
	}
	identity, err := newOperationIdentity(operation, requestContext, idempotencyKey, fingerprint)
	if err != nil {
		return errorResult(err), err
	}
	execute := func() (OperationResult, error) {
		if s.deps.OperationKeyGuard != nil {
			if err := s.deps.OperationKeyGuard.CheckOperationKey(ctx, identity); err != nil {
				return resultWithError(identity.OperationID, OperationResult{}, err), err
			}
		}
		return s.executeMutationLocked(ctx, requestContext, operation, identity, target, startedAt, recoverOperation, apply)
	}
	if s.deps.OperationLock == nil {
		return execute()
	}
	unlock, err := s.deps.OperationLock.AcquireOperationLock(ctx, identity.KeyHash)
	if err != nil {
		return resultWithError(identity.OperationID, OperationResult{}, err), err
	}
	if unlock == nil {
		err := unavailable("session control operation lock")
		return resultWithError(identity.OperationID, OperationResult{}, err), err
	}
	result, operationErr := execute()
	releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	releaseErr := unlock(releaseCtx)
	cancelRelease()
	if releaseErr != nil {
		err := &ServiceError{Code: CodeInternal, Message: "session control operation lock release failed", Cause: releaseErr}
		return resultWithError(identity.OperationID, result, err), err
	}
	return result, operationErr
}

func (s *Service) executeMutationLocked(ctx context.Context, requestContext RequestContext, operation Operation, identity OperationIdentity, target SessionRef, startedAt time.Time, recoverOperation recoverOperationFunc, apply applyOperationFunc) (OperationResult, error) {
	recovered, err := recoverOperation(identity)
	if err != nil {
		return resultWithError(identity.OperationID, OperationResult{}, err), err
	}
	if recovered.Found {
		if err := validateRecoveredMetadata(identity, recovered.Metadata); err != nil {
			return resultWithError(identity.OperationID, recovered.Result, err), err
		}
	}

	result := recovered.Result
	readbackRef := target
	if recovered.Found {
		readbackRef = recovered.Target
	} else {
		result, readbackRef, err = apply(identity)
		if err != nil {
			return resultWithError(identity.OperationID, result, err), err
		}
	}
	result.OperationID = identity.OperationID
	result.Replayed = recovered.Found
	if result.Session.Ref == (SessionRef{}) {
		result.Session.Ref = readbackRef
	}
	if operation == OperationAttach || operation == OperationHandoffRefresh {
		if s.deps.Handoff == nil {
			err := unavailable("session handoff readback port")
			return resultWithError(identity.OperationID, result, err), err
		}
		verified, readbackErr := s.deps.Handoff.Readback(ctx, HandoffReadbackRequest{Context: requestContext, Target: readbackRef, Expected: result.Handoff})
		if readbackErr != nil {
			return resultWithError(identity.OperationID, result, readbackErr), readbackErr
		}
		result.Handoff = verified
	}

	if err := validateSessionRef(readbackRef); err != nil {
		return resultWithError(identity.OperationID, result, err), err
	}
	readback, err := s.deps.Managed.Get(ctx, GetRequest{Context: requestContext, Ref: readbackRef})
	if err != nil {
		return resultWithError(identity.OperationID, result, err), err
	}
	result.Session = readback
	auditID, err := s.deps.Audit.Record(ctx, AuditRecord{Operation: operation, Context: requestContext, Target: readbackRef, OperationID: identity.OperationID, KeyHash: identity.KeyHash, Fingerprint: identity.Fingerprint, Replayed: recovered.Found, Outcome: "succeeded", DurationMS: s.now().Sub(startedAt).Milliseconds(), CreatedAt: s.now()})
	if err != nil {
		return resultWithError(identity.OperationID, result, err), err
	}
	result.AuditID = auditID
	return result, nil
}

func (s *Service) now() time.Time {
	if s.deps.Clock == nil {
		return time.Now().UTC()
	}
	return s.deps.Clock.Now().UTC()
}

func validateRequestContext(requestContext RequestContext) error {
	if requestContext.TenantID == 0 || requestContext.UserID == 0 || requestContext.ActorUserID == 0 {
		return invalidState("tenant, user, and actor user IDs are required")
	}
	return nil
}

func validateSessionRef(ref SessionRef) error {
	parsed, err := ParseRef(ref.String())
	if err != nil {
		return err
	}
	if parsed != ref {
		return invalidRef("session ref must use its canonical source and key")
	}
	return nil
}

func rejectLocalTarget(ref SessionRef) error {
	if err := validateSessionRef(ref); err != nil {
		return err
	}
	if ref.Source == SourceLocal {
		return &ServiceError{Code: CodeForbidden, Message: "local sessions are read-only"}
	}
	return nil
}

func operationFingerprint(operation Operation, requestContext RequestContext, request any) string {
	payload, _ := json.Marshal(struct {
		Operation   Operation `json:"operation"`
		TenantID    uint64    `json:"tenant_id"`
		UserID      uint64    `json:"user_id"`
		ActorUserID uint64    `json:"actor_user_id"`
		Request     any       `json:"request"`
	}{operation, requestContext.TenantID, requestContext.UserID, requestContext.ActorUserID, request})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func invalidState(message string) error {
	return &ServiceError{Code: CodeInvalidState, Message: message}
}

func unavailable(message string) error { return &ServiceError{Code: CodeInternal, Message: message} }

func errorResult(err error) OperationResult { return OperationResult{ErrorCode: serviceErrorCode(err)} }

func resultWithError(operationID string, result OperationResult, err error) OperationResult {
	result.OperationID = operationID
	result.ErrorCode = serviceErrorCode(err)
	return result
}

func serviceErrorCode(err error) string {
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return string(serviceErr.Code)
	}
	var refErr *RefError
	if errors.As(err, &refErr) {
		return string(refErr.Code)
	}
	return string(CodeInternal)
}
