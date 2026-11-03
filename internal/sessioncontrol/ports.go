package sessioncontrol

import (
	"context"
	"time"
)

// ManagedSessionPort abstracts tenant-backed sessions. Implementations must
// enforce each mutation's idempotency key atomically with its side effect. The
// service marks an operation applied after the adapter returns, so an adapter
// may receive the same request again if that persistence step fails.
type ManagedSessionPort interface {
	Create(context.Context, CreateRequest) (SessionSnapshot, error)
	List(context.Context, ListRequest) ([]SessionSnapshot, error)
	Get(context.Context, GetRequest) (SessionSnapshot, error)
	Send(context.Context, SendRequest) (OperationResult, error)
	Stop(context.Context, StopRequest) (OperationResult, error)
}

// LocalSessionPort is read-only by design. Local transcript inspection must
// never become an implicit mutation path through Session Control.
type LocalSessionPort interface {
	List(context.Context, ListRequest) ([]SessionSnapshot, error)
	Get(context.Context, GetRequest) (SessionSnapshot, error)
}

type Operation string

const (
	OperationCreate         Operation = "create"
	OperationSend           Operation = "send"
	OperationCompact        Operation = "compact"
	OperationStop           Operation = "stop"
	OperationAttach         Operation = "attach"
	OperationHandoffRefresh Operation = "handoff_refresh"
	OperationMonitor        Operation = "monitor"
	OperationList           Operation = "list"
	OperationGet            Operation = "get"
)

type ManagedCompactionPort interface {
	Compact(context.Context, CompactRequest) (OperationResult, error)
}

// AuthorizationPort owns tenant/user/actor checks. The service invokes it
// before operation recovery or adapter mutations.
type AuthorizationPort interface {
	Authorize(context.Context, RequestContext, Operation) error
}

type OperationUnlock func(context.Context) error

// OperationLockPort serializes one raw idempotency key after it has been
// reduced to a tenant/user/operation-scoped hash. Implementations must never
// log or persist the raw key.
type OperationLockPort interface {
	AcquireOperationLock(context.Context, string) (OperationUnlock, error)
}

// OperationKeyGuardPort rejects a completed operation whose globally scoped
// key hash is reused with different semantics after the lock is acquired.
type OperationKeyGuardPort interface {
	CheckOperationKey(context.Context, OperationIdentity) error
}

// RecoveredOperation is a bounded projection of one authoritative persisted
// side effect. Each mutation owns a separate recovery port because its replay
// anchor is a different session/task/link/schedule record.
type RecoveredOperation struct {
	Found    bool
	Metadata OperationMetadata
	Target   SessionRef
	Result   OperationResult
}

type CreateRecoveryPort interface {
	RecoverCreate(context.Context, CreateRequest, OperationIdentity) (RecoveredOperation, error)
}

type SendRecoveryPort interface {
	RecoverSend(context.Context, SendRequest, OperationIdentity) (RecoveredOperation, error)
}

type StopRecoveryPort interface {
	RecoverStop(context.Context, StopRequest, OperationIdentity) (RecoveredOperation, error)
}

type AttachRecoveryPort interface {
	RecoverAttach(context.Context, AttachRequest, OperationIdentity) (RecoveredOperation, error)
}

type RefreshRecoveryPort interface {
	RecoverRefresh(context.Context, RefreshHandoffRequest, OperationIdentity) (RecoveredOperation, error)
}

type MonitorRecoveryPort interface {
	RecoverMonitor(context.Context, MonitorRequest, OperationIdentity) (RecoveredOperation, error)
}

type MonitorPort interface {
	Monitor(context.Context, MonitorRequest) (OperationResult, error)
}

type SessionLink struct {
	ID           uint64
	Target       SessionRef
	Source       SessionRef
	RelationType string
	CreatedAt    time.Time
}

// LinkPort is retained for link-only readers. SessionAttach mutations use the
// Handoff port so link and task event persistence remain one transaction.
type LinkPort interface {
	Attach(context.Context, RequestContext, AttachRequest) ([]SessionLink, error)
}

// HandoffPort owns bounded source capture and the atomic per-package
// link/event persistence operation. It must not expose raw source content.
type HandoffPort interface {
	Attach(context.Context, AttachRequest) (HandoffResult, error)
	Read(context.Context, HandoffReadRequest) (HandoffResult, error)
	Refresh(context.Context, RefreshHandoffRequest) (HandoffResult, error)
	Readback(context.Context, HandoffReadbackRequest) (HandoffResult, error)
}

type HandoffTargetPort interface {
	ReadHandoffTarget(context.Context, RequestContext, SessionRef, uint64) (HandoffTarget, error)
	ReadHandoffEvent(context.Context, RequestContext, SessionRef, uint64, uint64) (HandoffEvent, error)
	ReadHandoffLink(context.Context, RequestContext, SessionRef, SessionRef, uint64) (SessionLink, error)
}

type AuditRecord struct {
	Operation   Operation
	Context     RequestContext
	Target      SessionRef
	OperationID string
	KeyHash     string
	Fingerprint string
	Replayed    bool
	Outcome     string
	DurationMS  int64
	CreatedAt   time.Time
}

type AuditPort interface {
	// Record must return the existing record for an equal OperationID. This
	// makes audit failure recovery idempotent without a command table.
	Record(context.Context, AuditRecord) (uint64, error)
}
