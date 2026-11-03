package imagegen

import (
	"errors"
	"strings"
	"time"
)

const (
	OperationGenerate = "generate"
	OperationEdit     = "edit"

	GenerationStatusQueued         = "queued"
	GenerationStatusRunning        = "running"
	GenerationStatusRetry          = "retry"
	GenerationStatusCompleted      = "completed"
	GenerationStatusFailed         = "failed"
	GenerationStatusOutcomeUnknown = "outcome_unknown"
	GenerationStatusCancelled      = "cancelled"
	GenerationStatusDead           = "dead"

	OriginTypeDirect    = "direct"
	OriginTypeChannel   = "channel"
	OriginTypeAgentTask = "agent_task"

	CompletionEventImageTerminal  = "image_terminal"
	CompletionEventImageCompleted = CompletionEventImageTerminal

	CompletionDeliveryStatusPending = "pending"
	CompletionDeliveryStatusSending = "sending"
	CompletionDeliveryStatusRetry   = "retry"
	CompletionDeliveryStatusSent    = "sent"
	CompletionDeliveryStatusDead    = "dead"

	ErrorClassCallerCancelled        = "caller_cancelled"
	ErrorClassCallerDeadlineExceeded = "caller_deadline_exceeded"
	ErrorClassProviderTimeout        = "provider_timeout"
	ErrorClassProviderRateLimited    = "provider_rate_limited"
	ErrorClassProviderUnavailable    = "provider_unavailable"
	ErrorClassProviderRejected       = "provider_rejected"
	ErrorClassProviderOutcomeUnknown = "provider_outcome_unknown"
)

type Artifact struct {
	AssetID       string    `json:"asset_id"`
	GenerationID  string    `json:"generation_id"`
	TenantID      uint64    `json:"tenant_id"`
	UserID        uint64    `json:"user_id"`
	SessionID     uint64    `json:"session_id"`
	Operation     string    `json:"operation"`
	SourceAssetID string    `json:"source_asset_id,omitempty"`
	MediaType     string    `json:"media_type"`
	Name          string    `json:"name"`
	Width         int       `json:"width,omitempty"`
	Height        int       `json:"height,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`
	SHA256        string    `json:"sha256"`
	URL           string    `json:"url"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	CreatedAt     time.Time `json:"created_at"`
}

// InlineData intentionally always returns an empty string: image bytes stay in BlobStore.
func (a Artifact) InlineData() string { return "" }

type GenerationRecord struct {
	ID                  uint64     `json:"id"`
	GenerationID        string     `json:"generation_id"`
	TenantID            uint64     `json:"tenant_id"`
	UserID              uint64     `json:"user_id"`
	SessionID           uint64     `json:"session_id"`
	AssetID             string     `json:"asset_id,omitempty"`
	SourceAssetID       string     `json:"source_asset_id,omitempty"`
	Operation           string     `json:"operation"`
	Status              string     `json:"status"`
	Prompt              string     `json:"prompt"`
	Provider            string     `json:"provider"`
	Model               string     `json:"model"`
	RequestJSON         string     `json:"request_json,omitempty"`
	ErrorCode           string     `json:"error_code,omitempty"`
	ErrorMessage        string     `json:"error_message,omitempty"`
	TraceID             string     `json:"trace_id,omitempty"`
	IdempotencyKey      string     `json:"idempotency_key,omitempty"`
	BatchID             string     `json:"batch_id,omitempty"`
	OriginType          string     `json:"origin_type,omitempty"`
	OriginRefJSON       string     `json:"origin_ref_json,omitempty"`
	ToolUseID           string     `json:"tool_use_id,omitempty"`
	Attempts            uint       `json:"attempts,omitempty"`
	MaxAttempts         uint       `json:"max_attempts,omitempty"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner          string     `json:"lease_owner,omitempty"`
	LeaseUntil          *time.Time `json:"lease_until,omitempty"`
	HeartbeatAt         *time.Time `json:"heartbeat_at,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CancelRequestedAt   *time.Time `json:"cancel_requested_at,omitempty"`
	ProviderRequestID   string     `json:"provider_request_id,omitempty"`
	RetryOfGenerationID string     `json:"retry_of_generation_id,omitempty"`
	ErrorClass          string     `json:"error_class,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
}

// JobScope names the tenant-owned context in which a generation is created
// and later accessed. Every scheduler operation requires the full scope.
type JobScope struct {
	TenantID  uint64
	UserID    uint64
	SessionID uint64
}

// QueueLimits are evaluated atomically with durable job admission. A zero
// value disables the corresponding cap.
type QueueLimits struct {
	MaxQueuedPerTenant int
	MaxQueuedPerUser   int
}

// RuntimeInvocation is supplied by the trusted runtime, not model arguments.
// Its pair produces the durable idempotency key for an asynchronous job.
type RuntimeInvocation struct {
	RunID     string
	ToolUseID string
}

// OriginMetadata captures only durable routing identifiers. RefJSON is
// versioned by the caller and must never contain credentials or message body.
type OriginMetadata struct {
	Type    string
	RefJSON string
}

// ManualRetryRequest contains only runtime-owned identity and routing fields.
// The repository reads the original prompt and provider snapshot atomically.
type ManualRetryRequest struct {
	Scope              JobScope
	SourceGenerationID string
	GenerationID       string
	IdempotencyKey     string
	BatchID            string
	ToolUseID          string
	Origin             OriginMetadata
	TraceID            string
	CreatedAt          time.Time
}

type JobStatus string

type JobReceipt struct {
	GenerationID string    `json:"generation_id"`
	BatchID      string    `json:"batch_id,omitempty"`
	Status       JobStatus `json:"status"`
	AcceptedAt   time.Time `json:"accepted_at"`
}

func (r GenerationRecord) Validate() error {
	if r.TenantID == 0 || r.UserID == 0 || r.SessionID == 0 || strings.TrimSpace(r.GenerationID) == "" || strings.TrimSpace(r.Prompt) == "" || strings.TrimSpace(r.Provider) == "" || strings.TrimSpace(r.Model) == "" {
		return errors.New("generation tenant, user, session, id, prompt, provider and model are required")
	}
	if r.Operation != OperationGenerate && r.Operation != OperationEdit {
		return errors.New("generation operation must be generate or edit")
	}
	switch r.Status {
	case GenerationStatusQueued, GenerationStatusRunning, GenerationStatusRetry, GenerationStatusCompleted, GenerationStatusFailed, GenerationStatusOutcomeUnknown, GenerationStatusCancelled, GenerationStatusDead:
	default:
		return errors.New("invalid generation status")
	}
	return nil
}
