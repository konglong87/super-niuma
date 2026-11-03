package sessioncontrol

import (
	"fmt"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

type SessionStatus string

const (
	StatusIdle              SessionStatus = "idle"
	StatusQueued            SessionStatus = "queued"
	StatusRunning           SessionStatus = "running"
	StatusWaitingPermission SessionStatus = "waiting_permission"
	StatusWaitingInput      SessionStatus = "waiting_input"
	StatusBlocked           SessionStatus = "blocked"
	StatusCompleted         SessionStatus = "completed"
	StatusFailed            SessionStatus = "failed"
	StatusStopped           SessionStatus = "stopped"
	StatusArchived          SessionStatus = "archived"
)

// SessionStateInput contains the independently observed signals used to render
// a session status. It deliberately has no storage or runtime dependencies.
type SessionStateInput struct {
	LifecycleStatus    string
	ActiveRunStatus    string
	PendingInputCount  int
	PermissionRequired bool
	UserInputRequired  bool
	Blocked            bool
}

// RequestContext is the tenant-scoped actor identity carried by every session
// control request. Transport adapters are responsible for deriving it from
// their authentication mechanism before calling the service.
type RequestContext struct {
	TenantID    uint64 `json:"tenant_id"`
	UserID      uint64 `json:"user_id"`
	ActorUserID uint64 `json:"actor_user_id"`
	TraceID     string `json:"trace_id,omitempty"`
}

type CreateRequest struct {
	Context        RequestContext    `json:"context"`
	SessionKey     string            `json:"session_key,omitempty"`
	Title          string            `json:"title,omitempty"`
	Model          string            `json:"model,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	PermissionMode string            `json:"permission_mode,omitempty"`
	Effort         string            `json:"effort,omitempty"`
	PromptMode     string            `json:"prompt_mode,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	InitialText    string            `json:"initial_text,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
	ReplayIdentity OperationIdentity `json:"-"`
}

type ListRequest struct {
	Context RequestContext `json:"context"`
	Source  Source         `json:"source"`
	Limit   int            `json:"limit,omitempty"`
}

type GetRequest struct {
	Context      RequestContext `json:"context"`
	Ref          SessionRef     `json:"ref"`
	IncludeLinks bool           `json:"include_links,omitempty"`
}

type SendRequest struct {
	AdoptTaskID           uint64                  `json:"-"`
	ExpectedRuntimeConfig *RuntimeConfig          `json:"-"`
	Context               RequestContext          `json:"context"`
	Ref                   SessionRef              `json:"ref"`
	Content               string                  `json:"content"`
	Provider              string                  `json:"provider,omitempty"`
	Model                 string                  `json:"model,omitempty"`
	PermissionMode        string                  `json:"permission_mode,omitempty"`
	Effort                string                  `json:"effort,omitempty"`
	PromptMode            string                  `json:"prompt_mode,omitempty"`
	Attachments           []agenttasks.Attachment `json:"attachments,omitempty"`
	SourceRefs            []SessionRef            `json:"source_refs,omitempty"`
	IdempotencyKey        string                  `json:"idempotency_key"`
	ReplayIdentity        OperationIdentity       `json:"-"`
}

type CompactRequest struct {
	Context        RequestContext    `json:"context"`
	Ref            SessionRef        `json:"ref"`
	IdempotencyKey string            `json:"idempotency_key"`
	ReplayIdentity OperationIdentity `json:"-"`
}

const PreparedRunFailureSource = "session_control_prelaunch"

// PreparedRunFailure is the stable, content-free failure record written when
// a ready Run cannot cross the handoff/launch boundary.
type PreparedRunFailure struct {
	ErrorCode string `json:"error_code"`
	TraceID   string `json:"trace_id,omitempty"`
}

type StopRequest struct {
	Context        RequestContext    `json:"context"`
	Ref            SessionRef        `json:"ref"`
	IdempotencyKey string            `json:"idempotency_key"`
	ReplayIdentity OperationIdentity `json:"-"`
}

type AttachRequest struct {
	Context                   RequestContext    `json:"context"`
	Target                    SessionRef        `json:"target"`
	TargetTaskID              uint64            `json:"target_task_id"`
	TargetContextWindowTokens int               `json:"target_context_window_tokens"`
	Sources                   []SessionRef      `json:"sources"`
	RelationType              string            `json:"relation_type"`
	IdempotencyKey            string            `json:"idempotency_key"`
	OperationIdentity         string            `json:"-"`
	ReplayIdentity            OperationIdentity `json:"-"`
}

type HandoffSourceResult struct {
	Source              SessionRef                `json:"source"`
	Success             bool                      `json:"success"`
	ErrorCode           string                    `json:"error_code,omitempty"`
	PackageID           string                    `json:"package_id,omitempty"`
	PackageSHA256Prefix string                    `json:"package_sha256_prefix,omitempty"`
	CursorPrefix        string                    `json:"cursor_prefix,omitempty"`
	EstimatedTokens     int                       `json:"estimated_tokens,omitempty"`
	LinkID              uint64                    `json:"link_id,omitempty"`
	EventID             uint64                    `json:"event_id,omitempty"`
	CandidateRemovals   []HandoffCandidateRemoval `json:"candidate_removals,omitempty"`
}

type HandoffCandidateRemoval struct {
	Category        string `json:"category"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// HandoffResult intentionally carries metadata only; bounded package prose and
// evidence live in the immutable target task event, never a transport result.
type HandoffResult struct {
	SourceResults   []HandoffSourceResult `json:"source_results,omitempty"`
	EstimatedTokens int                   `json:"estimated_tokens,omitempty"`
	TargetTaskID    uint64                `json:"target_task_id,omitempty"`
	Stale           bool                  `json:"stale"`
	Replayed        bool                  `json:"replayed"`
}

type HandoffReadbackRequest struct {
	Context  RequestContext
	Target   SessionRef
	Expected HandoffResult
}

// HandoffTarget is the scoped target-task readback used before source capture.
// Durable persistence repeats this ready-state check transactionally.
type HandoffTarget struct {
	SessionID  uint64
	TaskID     uint64
	TaskStatus string
}

type HandoffEvent struct {
	EventID     uint64
	TaskID      uint64
	PayloadJSON string
}

type HandoffReadRequest struct {
	Context      RequestContext `json:"context"`
	Target       SessionRef     `json:"target"`
	TargetTaskID uint64         `json:"target_task_id"`
	EventID      uint64         `json:"event_id"`
}

type RefreshHandoffRequest struct {
	Context                   RequestContext    `json:"context"`
	Target                    SessionRef        `json:"target"`
	TargetTaskID              uint64            `json:"target_task_id"`
	TargetContextWindowTokens int               `json:"target_context_window_tokens"`
	Source                    SessionRef        `json:"source"`
	PreviousPackageID         string            `json:"previous_package_id"`
	PreviousEventID           uint64            `json:"previous_event_id"`
	PreviousTarget            SessionRef        `json:"previous_target,omitempty"`
	PreviousTargetTaskID      uint64            `json:"previous_target_task_id,omitempty"`
	IdempotencyKey            string            `json:"idempotency_key"`
	OperationIdentity         string            `json:"-"`
	ReplayIdentity            OperationIdentity `json:"-"`
}

type MonitorRequest struct {
	Context         RequestContext    `json:"context"`
	Target          SessionRef        `json:"target"`
	Sources         []SessionRef      `json:"sources"`
	IntervalSeconds int               `json:"interval_seconds"`
	Channel         string            `json:"channel"`
	IdempotencyKey  string            `json:"idempotency_key"`
	ReplayIdentity  OperationIdentity `json:"-"`
}

// SessionSnapshot deliberately aliases the Local adapter snapshot. Managed
// adapters must populate the shared representation instead of introducing a
// near-identical transport payload.
type SessionSnapshot = LocalSnapshot

type SessionChannel struct {
	Provider   string `json:"provider"`
	AccountKey string `json:"account_key,omitempty"`
}

type OperationResult struct {
	OperationID string          `json:"operation_id,omitempty"`
	Replayed    bool            `json:"replayed"`
	Session     SessionSnapshot `json:"session"`
	RunID       uint64          `json:"run_id,omitempty"`
	ScheduleID  string          `json:"schedule_id,omitempty"`
	LinkIDs     []uint64        `json:"link_ids,omitempty"`
	Handoff     HandoffResult   `json:"handoff,omitempty"`
	AuditID     uint64          `json:"audit_id,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
}

type ServiceErrorCode string

const (
	CodeInvalidState         ServiceErrorCode = "invalid_state"
	CodeNotFound             ServiceErrorCode = "not_found"
	CodeForbidden            ServiceErrorCode = "forbidden"
	CodeIdempotencyConflict  ServiceErrorCode = "idempotency_conflict"
	CodeHandoffStale         ServiceErrorCode = "handoff_stale"
	CodeBudgetExceeded       ServiceErrorCode = "budget_exceeded"
	CodeSchedulerUnavailable ServiceErrorCode = "scheduler_unavailable"
	CodeInternal             ServiceErrorCode = "internal_error"
)

// ServiceError is a stable, transport-neutral error. Its code may be exposed
// by HTTP, CLI, and tool adapters without coupling callers to storage errors.
type ServiceError struct {
	Code    ServiceErrorCode
	Message string
	Cause   error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ServiceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Clock makes timestamps injectable for deterministic operation and audit
// tests. Implementations should return UTC values.
type Clock interface {
	Now() time.Time
}
