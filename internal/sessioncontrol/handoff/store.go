package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/tenant"
)

const (
	SessionHandoffEventSchema = agenttasks.SessionHandoffEventSchema
	SessionHandoffRelation    = "handoff"
	SessionHandoffLinkStatus  = "active"
)

// SessionHandoffEvent is the only durable package event payload. It retains
// versioned lineage plus a validated bounded package; arbitrary JSON maps are
// rejected at this boundary.
type SessionHandoffEvent struct {
	Schema             string  `json:"schema"`
	PackageID          string  `json:"package_id"`
	PackageSHA256      string  `json:"package_sha256"`
	TargetTaskID       uint64  `json:"target_task_id"`
	RefreshOfPackageID string  `json:"refresh_of_package_id,omitempty"`
	RefreshOfEventID   uint64  `json:"refresh_of_event_id,omitempty"`
	OperationIdentity  string  `json:"operation_identity,omitempty"`
	Package            Package `json:"package"`
}

func EncodeSessionHandoffEvent(pkg Package, targetTaskID uint64) (string, error) {
	return EncodeSessionHandoffEventWithRefresh(pkg, targetTaskID, "", 0)
}

func EncodeSessionHandoffEventWithRefresh(pkg Package, targetTaskID uint64, refreshOfPackageID string, refreshOfEventID uint64) (string, error) {
	if err := pkg.Validate(); err != nil {
		return "", err
	}
	packageJSON, err := json.Marshal(pkg)
	if err != nil {
		return "", err
	}
	payload, err := agenttasks.EncodeSessionHandoffEventWithRefresh(targetTaskID, string(packageJSON), refreshOfPackageID, refreshOfEventID)
	if err != nil {
		return "", contractError(CodeInvalidPackage, "session handoff event does not satisfy the persistence contract")
	}
	return payload, nil
}

func DecodeSessionHandoffEvent(payload string) (SessionHandoffEvent, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event SessionHandoffEvent
	if err := decoder.Decode(&event); err != nil {
		return SessionHandoffEvent{}, contractError(CodeInvalidPackage, "invalid session handoff event payload")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SessionHandoffEvent{}, contractError(CodeInvalidPackage, "invalid session handoff event payload")
	}
	if err := event.Validate(); err != nil {
		return SessionHandoffEvent{}, err
	}
	if _, err := agenttasks.DecodeSessionHandoffEvent(payload); err != nil {
		return SessionHandoffEvent{}, contractError(CodeInvalidPackage, "session handoff event is not bounded")
	}
	return event, nil
}

func (event SessionHandoffEvent) MarshalJSON() ([]byte, error) {
	if err := event.Validate(); err != nil {
		return nil, err
	}
	type wire SessionHandoffEvent
	return json.Marshal(wire(event))
}

func (event SessionHandoffEvent) Validate() error {
	if event.Schema != SessionHandoffEventSchema {
		return contractError(CodeUnsupportedSchema, "unsupported session handoff event schema")
	}
	if event.TargetTaskID == 0 {
		return contractError(CodeInvalidPackage, "target_task_id is required")
	}
	if err := event.Package.Validate(); err != nil {
		return err
	}
	if event.PackageID != event.Package.PackageID || event.PackageSHA256 != event.Package.PackageSHA256 {
		return contractError(CodeInvalidHash, "handoff event lineage does not match package identity")
	}
	if event.OperationIdentity != "" && !isSHA256(event.OperationIdentity) {
		return contractError(CodeInvalidHash, "handoff operation identity must be lowercase SHA-256")
	}
	return nil
}

// Store exposes one compound command so orchestration cannot write a link and
// event as independent side effects.
type Store interface {
	ResolveContext(context.Context) (tenant.Context, error)
	CreateHandoffLinkAndEvent(context.Context, agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error)
}

type BatchStore interface {
	ResolveContext(context.Context) (tenant.Context, error)
	CreateHandoffBatch(context.Context, agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error)
	RecoverHandoffBatch(context.Context, agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error)
}

type Persistence struct{ store Store }

func NewStore(store Store) *Persistence { return &Persistence{store: store} }

type PersistRequest struct {
	Context            sessioncontrol.RequestContext
	TargetSessionID    uint64
	TargetTaskID       uint64
	SourceSessionID    uint64
	Package            Package
	RefreshOfPackageID string
	RefreshOfEventID   uint64
}

type PersistResult = agenttasks.HandoffLinkAndEventResult

type PersistBatchRequest struct {
	Context               sessioncontrol.RequestContext
	TargetSessionID       uint64
	TargetTaskID          uint64
	OperationIdentity     string
	OperationMetadataJSON string
	Items                 []PersistRequest
}

type PersistBatchResult = agenttasks.HandoffBatchResult

type RecoverBatchRequest struct {
	Context           sessioncontrol.RequestContext
	TargetSessionID   uint64
	TargetTaskID      uint64
	OperationIdentity string
	Sources           []sessioncontrol.SessionRef
}

type RecoverBatchResult = agenttasks.HandoffRecoveryResult

func (p *Persistence) RecoverBatch(ctx context.Context, request RecoverBatchRequest) (agenttasks.HandoffRecoveryResult, error) {
	if p == nil {
		return agenttasks.HandoffRecoveryResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff recovery request is incomplete"}
	}
	batchStore, ok := any(p.store).(BatchStore)
	if !ok || batchStore == nil || request.TargetSessionID == 0 || request.TargetTaskID == 0 || request.OperationIdentity == "" || len(request.Sources) == 0 {
		return agenttasks.HandoffRecoveryResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff recovery request is incomplete"}
	}
	identity, err := batchStore.ResolveContext(ctx)
	if err != nil {
		return agenttasks.HandoffRecoveryResult{}, normalizeStoreError(err)
	}
	if identity.TenantID != request.Context.TenantID || identity.UserID != request.Context.UserID {
		return agenttasks.HandoffRecoveryResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff recovery is outside the authenticated tenant scope"}
	}
	expected := make([]string, len(request.Sources))
	for index, source := range request.Sources {
		expected[index] = source.String()
	}
	result, err := batchStore.RecoverHandoffBatch(ctx, agenttasks.HandoffRecoveryInput{TargetSessionID: request.TargetSessionID, TargetTaskID: request.TargetTaskID, OperationIdentity: request.OperationIdentity, ExpectedSources: expected})
	if err != nil {
		return agenttasks.HandoffRecoveryResult{}, normalizeStoreError(err)
	}
	return result, nil
}

func (p *Persistence) PersistBatch(ctx context.Context, request PersistBatchRequest) (PersistBatchResult, error) {
	if p == nil {
		return PersistBatchResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff batch persistence request is incomplete"}
	}
	batchStore, ok := any(p.store).(BatchStore)
	if !ok || batchStore == nil || request.Context.TenantID == 0 || request.Context.UserID == 0 || request.Context.ActorUserID == 0 || request.TargetSessionID == 0 || request.TargetTaskID == 0 || request.OperationIdentity == "" || len(request.Items) == 0 {
		return PersistBatchResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff batch persistence request is incomplete"}
	}
	identity, err := batchStore.ResolveContext(ctx)
	if err != nil {
		return PersistBatchResult{}, normalizeStoreError(err)
	}
	if identity.TenantID != request.Context.TenantID || identity.UserID != request.Context.UserID {
		return PersistBatchResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff persistence is outside the authenticated tenant scope"}
	}
	input := agenttasks.HandoffBatchInput{TargetSessionID: request.TargetSessionID, TargetTaskID: request.TargetTaskID, OperationIdentity: request.OperationIdentity, CreatedByUserID: request.Context.ActorUserID, TraceID: request.Context.TraceID, Items: make([]agenttasks.HandoffLinkAndEventInput, 0, len(request.Items))}
	for _, item := range request.Items {
		if item.TargetSessionID != request.TargetSessionID || item.TargetTaskID != request.TargetTaskID {
			return PersistBatchResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff batch targets must match"}
		}
		source, parseErr := sessioncontrol.ParseRef(item.Package.Source.Ref)
		if parseErr != nil {
			return PersistBatchResult{}, parseErr
		}
		payload, encodeErr := encodeSessionHandoffEvent(item.Package, item.TargetTaskID, item.RefreshOfPackageID, item.RefreshOfEventID, request.OperationIdentity, request.OperationMetadataJSON)
		if encodeErr != nil {
			return PersistBatchResult{}, encodeErr
		}
		input.Items = append(input.Items, agenttasks.HandoffLinkAndEventInput{SourceKind: string(source.Source), SourceSessionKey: source.Key, SourceSessionID: item.SourceSessionID, RelationType: SessionHandoffRelation, LinkStatus: SessionHandoffLinkStatus, PayloadJSON: payload})
	}
	result, err := batchStore.CreateHandoffBatch(ctx, input)
	if err != nil {
		return PersistBatchResult{}, normalizeStoreError(err)
	}
	return result, nil
}

func (p *Persistence) Persist(ctx context.Context, request PersistRequest) (PersistResult, error) {
	if p == nil || p.store == nil {
		return PersistResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInternal, Message: "handoff persistence store is required"}
	}
	if request.Context.TenantID == 0 || request.Context.UserID == 0 || request.Context.ActorUserID == 0 || request.TargetSessionID == 0 || request.TargetTaskID == 0 {
		return PersistResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff persistence scope and target are required"}
	}
	if err := request.Package.Validate(); err != nil {
		return PersistResult{}, err
	}
	target, err := sessioncontrol.ParseRef(request.Package.Target.Ref)
	if err != nil || target.Source != sessioncontrol.SourceTenant {
		return PersistResult{}, contractError(CodeInvalidRef, "handoff target must be a tenant session")
	}
	source, err := sessioncontrol.ParseRef(request.Package.Source.Ref)
	if err != nil {
		return PersistResult{}, contractError(CodeInvalidRef, "handoff source session is invalid")
	}
	identity, err := p.store.ResolveContext(ctx)
	if err != nil {
		return PersistResult{}, normalizeStoreError(err)
	}
	if identity.TenantID != request.Context.TenantID || identity.UserID != request.Context.UserID {
		return PersistResult{}, &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff persistence is outside the authenticated tenant scope"}
	}
	payload, err := EncodeSessionHandoffEventWithRefresh(request.Package, request.TargetTaskID, request.RefreshOfPackageID, request.RefreshOfEventID)
	if err != nil {
		return PersistResult{}, err
	}
	result, err := p.store.CreateHandoffLinkAndEvent(ctx, agenttasks.HandoffLinkAndEventInput{
		TargetSessionID: request.TargetSessionID, TargetTaskID: request.TargetTaskID,
		SourceKind: string(source.Source), SourceSessionKey: source.Key, SourceSessionID: request.SourceSessionID,
		RelationType: SessionHandoffRelation, LinkStatus: SessionHandoffLinkStatus,
		CreatedByUserID: request.Context.ActorUserID, PayloadJSON: payload, TraceID: request.Context.TraceID,
	})
	if err != nil {
		return PersistResult{}, normalizeStoreError(err)
	}
	return result, nil
}

func encodeSessionHandoffEvent(pkg Package, targetTaskID uint64, refreshOfPackageID string, refreshOfEventID uint64, operationIdentity, operationMetadataJSON string) (string, error) {
	if err := pkg.Validate(); err != nil {
		return "", err
	}
	packageJSON, err := json.Marshal(pkg)
	if err != nil {
		return "", err
	}
	payload, err := agenttasks.EncodeSessionHandoffEventWithOperationMetadata(targetTaskID, string(packageJSON), refreshOfPackageID, refreshOfEventID, operationIdentity, operationMetadataJSON)
	if err != nil {
		return "", contractError(CodeInvalidPackage, "session handoff event does not satisfy the persistence contract")
	}
	return payload, nil
}

func normalizeStoreError(err error) error {
	if errors.Is(err, mysql.ErrNotFound) {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeNotFound, Message: "handoff target task was not found", Cause: err}
	}
	if errors.Is(err, mysql.ErrInvalidState) {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeInvalidState, Message: "handoff target task is not ready", Cause: err}
	}
	if errors.Is(err, tenant.ErrForbidden) {
		return &sessioncontrol.ServiceError{Code: sessioncontrol.CodeForbidden, Message: "handoff persistence is forbidden", Cause: err}
	}
	return err
}
