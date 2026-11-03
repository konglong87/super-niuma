package handoff

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/sessioncontrol"
)

const packageBudgetContextWindow = 13_654

// SourceReader exposes only a bounded deterministic snapshot; it has no
// generic transcript accessor.
type SourceReader interface {
	Capture(context.Context, sessioncontrol.RequestContext, sessioncontrol.SessionRef) (SourceSnapshot, error)
}

type PersistencePort interface {
	Persist(context.Context, PersistRequest) (PersistResult, error)
	PersistBatch(context.Context, PersistBatchRequest) (PersistBatchResult, error)
	RecoverBatch(context.Context, RecoverBatchRequest) (RecoverBatchResult, error)
}

// Observation is deliberately limited to redacted operation metadata.
type Observation struct {
	Operation             string
	TraceID               string
	TenantID              uint64
	UserID                uint64
	SourceCount           int
	PackageIDPrefixes     []string
	PackageSHA256Prefixes []string
	CursorPrefixes        []string
	EstimatedTokens       int
	EvidenceCount         int
	CompressorUsed        bool
	Stale                 bool
	ErrorCode             string
	DurationMS            int64
}

type Observer interface {
	Observe(context.Context, Observation)
}

type Dependencies struct {
	Sources     map[sessioncontrol.Source]SourceReader
	Target      sessioncontrol.HandoffTargetPort
	Persistence PersistencePort
	Compressor  Compressor
	Observer    Observer
}

// Service captures all sources before any durable write. Session Control owns
// authorization, idempotency recovery, target session readback, and audit.
type Service struct{ deps Dependencies }

func NewService(deps Dependencies) *Service { return &Service{deps: deps} }

func (s *Service) Attach(ctx context.Context, request sessioncontrol.AttachRequest) (result sessioncontrol.HandoffResult, err error) {
	startedAt := time.Now()
	packages := make([]Package, 0, len(request.Sources))
	defer func() { s.observeAttach(ctx, request, result, packages, err, startedAt) }()
	if err := validateAttachRequest(request); err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	if s == nil || s.deps.Target == nil || s.deps.Persistence == nil {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInternal, "handoff dependencies are required", nil)
	}
	operationMetadataJSON, err := handoffOperationMetadataJSON(request.OperationIdentity, request.ReplayIdentity)
	if err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	sources := append([]sessioncontrol.SessionRef(nil), request.Sources...)
	sort.SliceStable(sources, func(i, j int) bool { return sources[i].String() < sources[j].String() })
	target, err := s.deps.Target.ReadHandoffTarget(ctx, request.Context, request.Target, request.TargetTaskID)
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	if target.SessionID == 0 || target.TaskID != request.TargetTaskID {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeNotFound, "handoff target task was not found", nil)
	}
	recovered, err := s.deps.Persistence.RecoverBatch(ctx, RecoverBatchRequest{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, OperationIdentity: request.OperationIdentity, Sources: sources})
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	if recovered.Found {
		return recoveredResult(request.TargetTaskID, recovered)
	}
	if target.TaskStatus != "ready" {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff target task is not ready", nil)
	}

	result = sessioncontrol.HandoffResult{TargetTaskID: request.TargetTaskID, SourceResults: make([]sessioncontrol.HandoffSourceResult, 0, len(sources))}
	for _, source := range sources {
		item, pkg, captureErr := s.capturePackage(ctx, request, source)
		result.SourceResults = append(result.SourceResults, item)
		if captureErr != nil {
			return result, captureErr
		}
		packages = append(packages, pkg)
		result.EstimatedTokens += item.EstimatedTokens
	}
	packages, _, err = PlanAggregateBudget(packages, request.TargetContextWindowTokens)
	result.EstimatedTokens = 0
	for index, pkg := range packages {
		result.SourceResults[index].PackageSHA256Prefix = shortPrefix(pkg.PackageSHA256)
		result.SourceResults[index].EstimatedTokens = pkg.Budget.EstimatedTokens
		result.EstimatedTokens += pkg.Budget.EstimatedTokens
	}
	if err != nil {
		var exceeded *BudgetExceededError
		if errors.As(err, &exceeded) {
			bySource := make(map[string]int, len(result.SourceResults))
			for index, item := range result.SourceResults {
				bySource[item.Source.String()] = index
				result.SourceResults[index].ErrorCode = string(sessioncontrol.CodeBudgetExceeded)
			}
			for _, candidate := range exceeded.Candidates {
				if index, ok := bySource[candidate.SourceRef]; ok {
					result.SourceResults[index].CandidateRemovals = append(result.SourceResults[index].CandidateRemovals, sessioncontrol.HandoffCandidateRemoval{Category: string(candidate.Category), EstimatedTokens: candidate.Tokens})
				}
			}
		}
		return result, normalizeError(err)
	}
	items := make([]PersistRequest, 0, len(packages))
	for _, pkg := range packages {
		items = append(items, PersistRequest{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, Package: pkg})
	}
	persisted, persistErr := s.deps.Persistence.PersistBatch(ctx, PersistBatchRequest{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, OperationIdentity: request.OperationIdentity, OperationMetadataJSON: operationMetadataJSON, Items: items})
	if persistErr != nil {
		return result, normalizeError(persistErr)
	}
	if len(persisted.Items) != len(result.SourceResults) {
		return result, serviceError(sessioncontrol.CodeInternal, "handoff batch persistence result is incomplete", nil)
	}
	for index, stored := range persisted.Items {
		result.SourceResults[index].Success = true
		result.SourceResults[index].LinkID = stored.LinkID
		result.SourceResults[index].EventID = stored.EventID
	}
	return result, nil
}

func recoveredResult(targetTaskID uint64, recovered RecoverBatchResult) (sessioncontrol.HandoffResult, error) {
	result := sessioncontrol.HandoffResult{TargetTaskID: targetTaskID, Replayed: true, SourceResults: make([]sessioncontrol.HandoffSourceResult, 0, len(recovered.Items))}
	for _, stored := range recovered.Items {
		source, err := sessioncontrol.ParseRef(stored.SourceRef)
		if err != nil || stored.LinkID == 0 || stored.EventID == 0 || stored.PackageID == "" || stored.PackageSHA256 == "" {
			return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "recovered handoff batch is incomplete", err)
		}
		result.SourceResults = append(result.SourceResults, sessioncontrol.HandoffSourceResult{Source: source, Success: true, LinkID: stored.LinkID, EventID: stored.EventID, PackageID: stored.PackageID, PackageSHA256Prefix: shortPrefix(stored.PackageSHA256), CursorPrefix: shortPrefix(stored.SourceCursor), EstimatedTokens: stored.EstimatedTokens})
		result.EstimatedTokens += stored.EstimatedTokens
	}
	return result, nil
}

// Read derives staleness from a new deterministic fingerprint only. It never
// updates the immutable event, link, or task state.
func (s *Service) Read(ctx context.Context, request sessioncontrol.HandoffReadRequest) (sessioncontrol.HandoffResult, error) {
	event, err := s.readEvent(ctx, request.Context, request.Target, request.TargetTaskID, request.EventID)
	if err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	source, err := sessioncontrol.ParseRef(event.Package.Source.Ref)
	if err != nil {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff event source is invalid", err)
	}
	reader := s.deps.Sources[source.Source]
	if reader == nil {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff source reader is unavailable", nil)
	}
	snapshot, err := reader.Capture(ctx, request.Context, source)
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	snapshot.Target = event.Package.Target
	snapshot.Budget = event.Package.Budget
	current, err := Extract(snapshot)
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	stale := current.Source.Cursor != event.Package.Source.Cursor || current.Source.ContentSHA256 != event.Package.Source.ContentSHA256
	return sessioncontrol.HandoffResult{TargetTaskID: request.TargetTaskID, Stale: stale, SourceResults: []sessioncontrol.HandoffSourceResult{{
		Source: source, Success: true, PackageID: event.PackageID, PackageSHA256Prefix: shortPrefix(event.PackageSHA256),
		CursorPrefix: shortPrefix(event.Package.Source.Cursor), EstimatedTokens: event.Package.Budget.EstimatedTokens, EventID: request.EventID,
	}}}, nil
}

// Refresh only appends a new event after the caller explicitly names a stale
// prior event and a ready target. An unchanged source returns prior identity.
func (s *Service) Refresh(ctx context.Context, request sessioncontrol.RefreshHandoffRequest) (sessioncontrol.HandoffResult, error) {
	if s == nil || s.deps.Target == nil || s.deps.Persistence == nil {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInternal, "handoff dependencies are required", nil)
	}
	operationMetadataJSON, err := handoffOperationMetadataJSON(request.OperationIdentity, request.ReplayIdentity)
	if err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	target, err := s.deps.Target.ReadHandoffTarget(ctx, request.Context, request.Target, request.TargetTaskID)
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	if target.SessionID == 0 || target.TaskID != request.TargetTaskID {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeNotFound, "handoff refresh target task was not found", nil)
	}
	recovered, err := s.deps.Persistence.RecoverBatch(ctx, RecoverBatchRequest{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, OperationIdentity: request.OperationIdentity, Sources: []sessioncontrol.SessionRef{request.Source}})
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	if recovered.Found {
		return recoveredResult(request.TargetTaskID, recovered)
	}
	priorTarget, priorTaskID := request.Target, request.TargetTaskID
	if request.PreviousTarget != (sessioncontrol.SessionRef{}) {
		priorTarget, priorTaskID = request.PreviousTarget, request.PreviousTargetTaskID
	}
	prior, err := s.readEvent(ctx, request.Context, priorTarget, priorTaskID, request.PreviousEventID)
	if err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	if prior.PackageID != request.PreviousPackageID {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "refresh package does not match the prior event", nil)
	}
	if request.Source.String() != prior.Package.Source.Ref {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeForbidden, "refresh source does not match the prior package", nil)
	}
	attach := sessioncontrol.AttachRequest{Context: request.Context, Target: request.Target, TargetTaskID: request.TargetTaskID, TargetContextWindowTokens: request.TargetContextWindowTokens, Sources: []sessioncontrol.SessionRef{request.Source}}
	if err := validateAttachRequest(attach); err != nil {
		return sessioncontrol.HandoffResult{}, err
	}
	if target.TaskStatus != "ready" {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff refresh target task is not ready", nil)
	}
	item, pkg, err := s.capturePackage(ctx, attach, request.Source)
	if err != nil {
		return sessioncontrol.HandoffResult{SourceResults: []sessioncontrol.HandoffSourceResult{item}}, err
	}
	planned, _, err := PlanAggregateBudget([]Package{pkg}, request.TargetContextWindowTokens)
	if err != nil {
		return sessioncontrol.HandoffResult{SourceResults: []sessioncontrol.HandoffSourceResult{item}}, normalizeError(err)
	}
	pkg = planned[0]
	item.PackageSHA256Prefix, item.EstimatedTokens = shortPrefix(pkg.PackageSHA256), pkg.Budget.EstimatedTokens
	sameTarget := request.Target == priorTarget && request.TargetTaskID == priorTaskID
	sourceChanged := pkg.Source.Cursor != prior.Package.Source.Cursor || pkg.Source.ContentSHA256 != prior.Package.Source.ContentSHA256
	if sameTarget && !sourceChanged {
		link, linkErr := s.deps.Target.ReadHandoffLink(ctx, request.Context, priorTarget, request.Source, 0)
		if linkErr != nil {
			return sessioncontrol.HandoffResult{}, normalizeError(linkErr)
		}
		item.PackageID, item.PackageSHA256Prefix, item.CursorPrefix, item.EventID = prior.PackageID, shortPrefix(prior.PackageSHA256), shortPrefix(prior.Package.Source.Cursor), request.PreviousEventID
		item.LinkID = link.ID
		item.Success = true
		return sessioncontrol.HandoffResult{TargetTaskID: request.TargetTaskID, Replayed: true, SourceResults: []sessioncontrol.HandoffSourceResult{item}, EstimatedTokens: prior.Package.Budget.EstimatedTokens}, nil
	}
	persistedBatch, err := s.deps.Persistence.PersistBatch(ctx, PersistBatchRequest{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, OperationIdentity: request.OperationIdentity, OperationMetadataJSON: operationMetadataJSON, Items: []PersistRequest{{Context: request.Context, TargetSessionID: target.SessionID, TargetTaskID: target.TaskID, Package: pkg, RefreshOfPackageID: prior.PackageID, RefreshOfEventID: request.PreviousEventID}}})
	if err != nil {
		return sessioncontrol.HandoffResult{SourceResults: []sessioncontrol.HandoffSourceResult{item}}, normalizeError(err)
	}
	if len(persistedBatch.Items) != 1 {
		return sessioncontrol.HandoffResult{SourceResults: []sessioncontrol.HandoffSourceResult{item}}, serviceError(sessioncontrol.CodeInternal, "handoff refresh persistence result is incomplete", nil)
	}
	item.Success = true
	item.LinkID, item.EventID = persistedBatch.Items[0].LinkID, persistedBatch.Items[0].EventID
	return sessioncontrol.HandoffResult{TargetTaskID: request.TargetTaskID, Stale: sourceChanged, SourceResults: []sessioncontrol.HandoffSourceResult{item}, EstimatedTokens: item.EstimatedTokens}, nil
}

func (s *Service) Readback(ctx context.Context, request sessioncontrol.HandoffReadbackRequest) (sessioncontrol.HandoffResult, error) {
	if s == nil || s.deps.Target == nil || request.Expected.TargetTaskID == 0 || len(request.Expected.SourceResults) == 0 {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff readback request is incomplete", nil)
	}
	target, err := s.deps.Target.ReadHandoffTarget(ctx, request.Context, request.Target, request.Expected.TargetTaskID)
	if err != nil {
		return sessioncontrol.HandoffResult{}, normalizeError(err)
	}
	if target.SessionID == 0 || target.TaskID != request.Expected.TargetTaskID {
		return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeNotFound, "handoff readback target task was not found", nil)
	}
	verified := request.Expected
	for index, item := range verified.SourceResults {
		if item.LinkID == 0 || item.EventID == 0 || item.PackageID == "" || item.PackageSHA256Prefix == "" {
			return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff readback identity is incomplete", nil)
		}
		if _, err := s.deps.Target.ReadHandoffLink(ctx, request.Context, request.Target, item.Source, item.LinkID); err != nil {
			return sessioncontrol.HandoffResult{}, normalizeError(err)
		}
		event, err := s.readEvent(ctx, request.Context, request.Target, request.Expected.TargetTaskID, item.EventID)
		if err != nil {
			return sessioncontrol.HandoffResult{}, err
		}
		if event.PackageID != item.PackageID || !strings.HasPrefix(event.PackageSHA256, item.PackageSHA256Prefix) || event.Package.Source.Ref != item.Source.String() {
			return sessioncontrol.HandoffResult{}, serviceError(sessioncontrol.CodeInvalidState, "handoff readback package identity changed", nil)
		}
		verified.SourceResults[index].Success = true
	}
	return verified, nil
}

func (s *Service) readEvent(ctx context.Context, requestContext sessioncontrol.RequestContext, target sessioncontrol.SessionRef, taskID, eventID uint64) (SessionHandoffEvent, error) {
	if s == nil || s.deps.Target == nil || eventID == 0 || taskID == 0 {
		return SessionHandoffEvent{}, serviceError(sessioncontrol.CodeInvalidState, "handoff event request is incomplete", nil)
	}
	stored, err := s.deps.Target.ReadHandoffEvent(ctx, requestContext, target, taskID, eventID)
	if err != nil {
		return SessionHandoffEvent{}, normalizeError(err)
	}
	if stored.EventID != eventID || stored.TaskID != taskID {
		return SessionHandoffEvent{}, serviceError(sessioncontrol.CodeNotFound, "handoff event was not found", nil)
	}
	event, err := DecodeSessionHandoffEvent(stored.PayloadJSON)
	if err != nil {
		return SessionHandoffEvent{}, serviceError(sessioncontrol.CodeInvalidState, "handoff event payload is invalid", err)
	}
	if event.TargetTaskID != taskID || event.Package.Target.Ref != target.String() {
		return SessionHandoffEvent{}, serviceError(sessioncontrol.CodeNotFound, "handoff event target does not match", nil)
	}
	return event, nil
}

func (s *Service) capturePackage(ctx context.Context, request sessioncontrol.AttachRequest, source sessioncontrol.SessionRef) (sessioncontrol.HandoffSourceResult, Package, error) {
	item := sessioncontrol.HandoffSourceResult{Source: source}
	reader := s.deps.Sources[source.Source]
	if reader == nil {
		err := serviceError(sessioncontrol.CodeInvalidState, "handoff source reader is unavailable", nil)
		item.ErrorCode = string(sessioncontrol.CodeInvalidState)
		return item, Package{}, err
	}
	snapshot, err := reader.Capture(ctx, request.Context, source)
	if err != nil {
		err = normalizeError(err)
		item.ErrorCode = serviceErrorCode(err)
		return item, Package{}, err
	}
	if snapshot.Source.Ref != source.String() {
		err := serviceError(sessioncontrol.CodeInvalidState, "handoff source reader returned a mismatched snapshot", nil)
		item.ErrorCode = string(sessioncontrol.CodeInvalidState)
		return item, Package{}, err
	}
	snapshot.Target = Target{Ref: request.Target.String()}
	if snapshot.Budget.LimitTokens == 0 {
		snapshot.Budget.LimitTokens = DefaultPackageTokenLimit
	}
	pkg, err := Extract(snapshot)
	if err != nil {
		err = normalizeError(err)
		item.ErrorCode = serviceErrorCode(err)
		return item, Package{}, err
	}
	compressed, err := CompressPackage(ctx, pkg, s.deps.Compressor, packageBudgetContextWindow)
	if err != nil {
		err = normalizeError(err)
		item.ErrorCode = serviceErrorCode(err)
		return item, Package{}, err
	}
	item.Success = false
	item.PackageID = compressed.Package.PackageID
	item.PackageSHA256Prefix = shortPrefix(compressed.Package.PackageSHA256)
	item.CursorPrefix = shortPrefix(compressed.Package.Source.Cursor)
	item.EstimatedTokens = compressed.Budget.EstimatedTokens
	return item, compressed.Package, nil
}

func (s *Service) observeAttach(ctx context.Context, request sessioncontrol.AttachRequest, result sessioncontrol.HandoffResult, packages []Package, err error, startedAt time.Time) {
	if s == nil || s.deps.Observer == nil {
		return
	}
	observation := Observation{
		Operation: "attach", TraceID: request.Context.TraceID, TenantID: request.Context.TenantID, UserID: request.Context.UserID,
		SourceCount: len(request.Sources), EstimatedTokens: result.EstimatedTokens, CompressorUsed: s.deps.Compressor != nil, ErrorCode: serviceErrorCode(err), DurationMS: time.Since(startedAt).Milliseconds(),
	}
	if err == nil {
		observation.ErrorCode = ""
	}
	for _, pkg := range packages {
		observation.PackageIDPrefixes = append(observation.PackageIDPrefixes, shortPrefix(pkg.PackageID))
		observation.PackageSHA256Prefixes = append(observation.PackageSHA256Prefixes, shortPrefix(pkg.PackageSHA256))
		observation.CursorPrefixes = append(observation.CursorPrefixes, shortPrefix(pkg.Source.Cursor))
		observation.EvidenceCount += len(pkg.Evidence)
	}
	s.deps.Observer.Observe(ctx, observation)
}

func validateAttachRequest(request sessioncontrol.AttachRequest) error {
	if request.Context.TenantID == 0 || request.Context.UserID == 0 || request.Context.ActorUserID == 0 || request.TargetTaskID == 0 || request.TargetContextWindowTokens <= 0 {
		return serviceError(sessioncontrol.CodeInvalidState, "handoff context and target task are required", nil)
	}
	if request.Target.Source != sessioncontrol.SourceTenant || len(request.Sources) == 0 {
		return serviceError(sessioncontrol.CodeInvalidState, "handoff target and sources are required", nil)
	}
	seen := make(map[string]struct{}, len(request.Sources))
	for _, source := range request.Sources {
		parsed, err := sessioncontrol.ParseRef(source.String())
		if err != nil || parsed != source {
			return serviceError(sessioncontrol.CodeInvalidState, "handoff source ref must be canonical", err)
		}
		if _, exists := seen[source.String()]; exists {
			return serviceError(sessioncontrol.CodeInvalidState, "handoff source refs must be unique", nil)
		}
		seen[source.String()] = struct{}{}
	}
	return nil
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var serviceErr *sessioncontrol.ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr
	}
	var budgetErr *BudgetExceededError
	if errors.As(err, &budgetErr) {
		return serviceError(sessioncontrol.CodeBudgetExceeded, "handoff package exceeds its context budget", err)
	}
	var handoffErr *Error
	if errors.As(err, &handoffErr) {
		if handoffErr.Code == CodeSourceChanged {
			return serviceError(sessioncontrol.CodeHandoffStale, "handoff source changed during capture", err)
		}
		return serviceError(sessioncontrol.CodeInvalidState, "handoff package is invalid", err)
	}
	return serviceError(sessioncontrol.CodeInternal, "handoff operation failed", err)
}

func serviceError(code sessioncontrol.ServiceErrorCode, message string, cause error) error {
	return &sessioncontrol.ServiceError{Code: code, Message: message, Cause: cause}
}

func serviceErrorCode(err error) string {
	var typed *sessioncontrol.ServiceError
	if errors.As(err, &typed) {
		return string(typed.Code)
	}
	return string(sessioncontrol.CodeInternal)
}

func shortPrefix(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 16 {
		return value
	}
	return value[:16]
}

func handoffOperationMetadataJSON(operationIdentity string, identity sessioncontrol.OperationIdentity) (string, error) {
	if identity.OperationID == "" {
		return "", nil
	}
	if identity.KeyHash != operationIdentity {
		return "", serviceError(sessioncontrol.CodeInvalidState, "handoff operation identity does not match replay metadata", nil)
	}
	payload, err := sessioncontrol.EncodeOperationMetadataJSON(identity)
	if err != nil {
		return "", normalizeError(err)
	}
	return payload, nil
}
