// Package control owns Super work, not interactive conversations. All mutations
// are durable CAS transitions; executors and transports are replaceable ports.
package control

import (
	"context"
	"errors"

	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

type Status string
type JobStatus string
type DeliveryStatus string

const (
	MaxObjectiveBytes                 = 32768
	LocalScope                        = "local"
	LocalOwner                        = "local"
	Queued             Status         = "queued"
	Running            Status         = "running"
	Verifying          Status         = "verifying"
	AwaitingApproval   Status         = "awaiting_approval"
	DeliveryPending    Status         = "delivery_pending"
	Delivered          Status         = "delivered"
	Cancelling         Status         = "cancelling"
	Cancelled          Status         = "cancelled"
	Failed             Status         = "failed"
	NeedsAttention     Status         = "needs_attention"
	JobQueued          JobStatus      = "queued"
	JobRunning         JobStatus      = "running"
	JobSucceeded       JobStatus      = "succeeded"
	JobFailed          JobStatus      = "failed"
	JobCancelled       JobStatus      = "cancelled"
	JobUnknown         JobStatus      = "unknown"
	DeliveryQueued     DeliveryStatus = "pending"
	DeliverySending    DeliveryStatus = "sending"
	DeliverySent       DeliveryStatus = "sent"
	DeliveryRetry      DeliveryStatus = "retry"
	DeliveryUnknown    DeliveryStatus = "unknown"
	DeliveryDead       DeliveryStatus = "dead"
	ProviderDesktop                   = "desktop"
	ProviderWebhook                   = "webhook"
	ProviderFeishu                    = "feishu"
	MaxNodes                          = 32
	MaxRuns                           = 100
	MaxRunBytes                       = 1024 * 1024
	MaxAttempts                       = 3
	MaxReservations                   = MaxNodes * execution.MaxTurns * MaxAttempts
	MaxTargets                        = 8
	DefaultWorkers                    = 2
	MaxWorkers                        = 8
	DefaultLeaseMS     int64          = 30000
	DefaultTimeoutMS   int64          = 120000
	MaxTimeoutMS       int64          = 600000
	MaxBudgetTokens                   = 2000000
	EventRunChanged                   = "super.run.changed"
	EventDeliveryReady                = "super.delivery.ready"
)

var (
	ErrInvalid      = errors.New("invalid Super control request")
	ErrNotFound     = errors.New("Super run not found")
	ErrConflict     = errors.New("Super state revision conflict")
	ErrDenied       = errors.New("Super authority denied")
	ErrCapacity     = errors.New("Super capacity exhausted")
	ErrResourceBusy = errors.New("Super workspace occupied")
	ErrUnavailable  = errors.New("Super runtime not configured")
)

type Principal struct {
	Scope      string `json:"scope"`
	Owner      string `json:"owner"`
	LocalAdmin bool   `json:"-"`
}

type Node struct {
	ID              string   `json:"id"`
	ParentID        string   `json:"parent_id,omitempty"`
	Instruction     string   `json:"instruction"`
	DependsOn       []string `json:"depends_on,omitempty"`
	ReadPaths       []string `json:"read_paths,omitempty"`
	WritePaths      []string `json:"write_paths,omitempty"`
	MaxTurns        int      `json:"max_turns"`
	MaxOutputTokens int      `json:"max_output_tokens"`
}

type Target struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Account   string `json:"account,omitempty"`
	Chat      string `json:"chat,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	ReplyTo   string `json:"reply_to,omitempty"`
}

type Grant struct {
	ChannelPolicyHash string              `json:"channel_policy_hash,omitempty"`
	SourceBindingID   string              `json:"source_binding_id,omitempty"`
	SourceBindingHash string              `json:"source_binding_hash,omitempty"`
	RouteHash         string              `json:"route_hash"`
	ProfileID         string              `json:"profile_id"`
	Revision          string              `json:"revision"`
	Workspace         string              `json:"workspace"`
	Provider          string              `json:"provider"`
	Model             string              `json:"model"`
	SystemPrompt      string              `json:"system_prompt"`
	MaxTokens         int                 `json:"max_tokens"`
	TimeoutMS         int64               `json:"timeout_ms"`
	ExpiresAtMS       int64               `json:"expires_at_ms"`
	Nodes             []Node              `json:"nodes"`
	Verification      verification.Policy `json:"verification"`
	Targets           []Target            `json:"targets"`
	AutoApprove       bool                `json:"auto_approve"`
}

type Claim struct {
	Scope   string `json:"scope"`
	RunID   string `json:"run_id"`
	JobID   string `json:"job_id"`
	Owner   string `json:"owner"`
	Token   string `json:"token"`
	Epoch   uint64 `json:"epoch"`
	UntilMS int64  `json:"until_ms"`
}

type Job struct {
	Spec         Node             `json:"spec"`
	Status       JobStatus        `json:"status"`
	Claim        Claim            `json:"claim"`
	Attempts     int              `json:"attempts"`
	StartedAtMS  int64            `json:"started_at_ms,omitempty"`
	FinishedAtMS int64            `json:"finished_at_ms,omitempty"`
	Result       execution.Result `json:"result"`
	SafeCode     string           `json:"safe_code,omitempty"`
}

type Reservation struct {
	ID        string          `json:"id"`
	JobID     string          `json:"job_id"`
	AttemptID string          `json:"attempt_id"`
	Input     int             `json:"input"`
	Output    int             `json:"output"`
	Actual    execution.Usage `json:"actual"`
	Settled   bool            `json:"settled"`
}

type Receipt struct {
	MessageID     string `json:"message_id"`
	RequestID     string `json:"request_id,omitempty"`
	Chat          string `json:"chat,omitempty"`
	DeliveredAtMS int64  `json:"delivered_at_ms"`
}

type Notice struct {
	ID        string         `json:"id"`
	Target    Target         `json:"target"`
	Status    DeliveryStatus `json:"status"`
	Attempts  int            `json:"attempts"`
	Claim     Claim          `json:"claim"`
	SafeCode  string         `json:"safe_code,omitempty"`
	RetryAtMS int64          `json:"retry_at_ms,omitempty"`
	Receipt   *Receipt       `json:"receipt,omitempty"`
}

type Run struct {
	Resolution            *Resolution          `json:"resolution,omitempty"`
	ID                    string               `json:"id"`
	Scope                 string               `json:"scope"`
	Owner                 string               `json:"owner"`
	IntakeID              string               `json:"intake_id,omitempty"`
	RequestKey            string               `json:"request_key"`
	Objective             string               `json:"objective"`
	Grant                 Grant                `json:"grant"`
	GrantHash             string               `json:"grant_hash"`
	Version               uint64               `json:"version"`
	Status                Status               `json:"status"`
	Jobs                  []Job                `json:"jobs"`
	Reservations          []Reservation        `json:"reservations"`
	VerificationAttemptID string               `json:"verification_attempt_id,omitempty"`
	VerificationClaim     Claim                `json:"verification_claim"`
	Verification          *verification.Report `json:"verification,omitempty"`
	Notices               []Notice             `json:"notices"`
	SafeCode              string               `json:"safe_code,omitempty"`
	Approved              bool                 `json:"approved"`
	CreatedAtMS           int64                `json:"created_at_ms"`
	UpdatedAtMS           int64                `json:"updated_at_ms"`
}

// Ingress belongs to the server-authenticated Principal, never request-body
// owner/scope fields. Message and event identities have separate dedup domains.
type Ingress struct {
	Account     string `json:"account"`
	EventID     string `json:"event_id"`
	MessageID   string `json:"message_id"`
	Nonce       string `json:"nonce,omitempty"`
	ContentHash string `json:"content_hash"`
}

type Repository interface {
	CreateRun(context.Context, Run, *Ingress) (Run, bool, error)
	GetRun(context.Context, string, string) (Run, error)
	ListRuns(context.Context, string) ([]Run, error)
	ActiveRuns(context.Context) ([]Run, error)
	SaveRun(context.Context, uint64, Run) (Run, error)
	CheckClaim(context.Context, Claim) error
}

type Authority interface {
	Current(context.Context, Principal, Grant) error
}

type Sender interface {
	Send(context.Context, Target, string, string) (Receipt, error)
}

// Send errors must explicitly prove "not accepted" to be retryable. All
// untyped failures, timeouts and lost receipts are unknown, never blind retries.
type SendError struct {
	Code                  string
	Retryable             bool
	DefinitelyNotAccepted bool
}

func (e *SendError) Error() string { return "Super notice delivery failed" }

type Verifier interface {
	Verify(context.Context, string, verification.Policy) (verification.Report, error)
}
