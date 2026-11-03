// Package workflow provides the transport- and provider-independent Team V2
// execution contract. It deliberately has no dependency on the parent agentteam
// package, SQL, CLI, or a model SDK.
package workflow

import (
	"context"
	"errors"
	"time"
)

const (
	MaxQueuedRuns      = 20
	SchemaVersion      = 2
	DefaultParallel    = 2
	MaxParallel        = 4
	MaxNodes           = 16
	MaxInputBytes      = 32 * 1024
	MaxOutputBytes     = 64 * 1024
	DefaultRunSeconds  = 600
	MaxRunSeconds      = 1800
	DefaultNodeSeconds = 120
	MaxNodeSeconds     = 300
	MaxAttempts        = 3
	DefaultAttempts    = 2
	DefaultMaxTokens   = 32000
	MaxTotalTokens     = 128000
	EventPageLimit     = 200
	ToolArtifactRead   = "ArtifactRead"
)

type Status string

const (
	Queued          Status = "queued"
	Pending         Status = "pending"
	Ready           Status = "ready"
	Running         Status = "running"
	RetryWait       Status = "retry_wait"
	Completed       Status = "completed"
	Failed          Status = "failed"
	TimedOut        Status = "timed_out"
	CancelRequested Status = "cancel_requested"
	Cancelled       Status = "cancelled"
	Interrupted     Status = "interrupted"
	Blocked         Status = "blocked"
)

func Terminal(s Status) bool { return s == Completed || s == Failed || s == TimedOut || s == Cancelled }

type EventType string

const (
	EventQueued          EventType = "run_queued"
	EventStarted         EventType = "run_started"
	EventTaskCreated     EventType = "task_created"
	EventWaiting         EventType = "task_waiting_dependency"
	EventAgentStarted    EventType = "agent_started"
	EventDelta           EventType = "agent_output_delta"
	EventPhase           EventType = "agent_phase"
	EventAgentCompleted  EventType = "agent_completed"
	EventAgentFailed     EventType = "agent_failed"
	EventRetry           EventType = "task_retry_scheduled"
	EventCancelRequested EventType = "run_cancel_requested"
	EventCancelled       EventType = "run_cancelled"
	EventTaskCancelled   EventType = "task_cancelled"
	EventCompleted       EventType = "run_completed"
	EventFailed          EventType = "run_failed"
	EventTimedOut        EventType = "run_timed_out"
	EventInterrupted     EventType = "run_interrupted"
	EventRecovered       EventType = "run_recovered"
)

type Scope struct {
	TenantID uint64 `json:"tenant_id"`
	UserID   uint64 `json:"user_id"`
}
type RetryPolicy struct {
	MaxAttempts int `json:"max_attempts"`
	BackoffMS   int `json:"backoff_ms"`
}
type Limits struct {
	MaxParallel       int `json:"max_parallel"`
	RunTimeoutSeconds int `json:"run_timeout_seconds"`
	MaxTotalTokens    int `json:"max_total_tokens"`
}
type Node struct {
	ID             string      `json:"id"`
	AgentRef       string      `json:"agent_ref"`
	Instruction    string      `json:"instruction"`
	DependsOn      []string    `json:"depends_on"`
	TimeoutSeconds int         `json:"timeout_seconds"`
	Retry          RetryPolicy `json:"retry"`
}
type Definition struct {
	SchemaVersion int    `json:"schema_version"`
	Nodes         []Node `json:"nodes"`
	Limits        Limits `json:"limits"`
}

// Agent is an immutable, secret-free effective Profile projection. Roles are
// descriptive; no scheduler branch grants authority based on a role name.
type Agent struct {
	RouteHash       string   `json:"route_hash,omitempty"`
	Ref             string   `json:"ref"`
	ProfileID       uint64   `json:"profile_id"`
	ProfileKey      string   `json:"profile_key"`
	ProfileVersion  uint     `json:"profile_version"`
	ProfileHash     string   `json:"profile_hash"`
	Name            string   `json:"name"`
	RoleType        string   `json:"role_type"`
	Kind            string   `json:"kind"`
	SystemPrompt    string   `json:"system_prompt"`
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	Effort          string   `json:"effort,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	Tools           []string `json:"tools"`
}
type Plan struct {
	Definition Definition `json:"definition"`
	Agents     []Agent    `json:"agents"`
	Hash       string     `json:"hash"`
}
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}
type Attempt struct {
	// BudgetReserved is the immutable admission reservation, not provider usage.
	BudgetReserved int        `json:"budget_reserved"`
	UsageKnown     bool       `json:"usage_known"`
	Number         int        `json:"number"`
	Epoch          uint64     `json:"epoch"`
	TaskID         uint64     `json:"task_id"`
	Status         Status     `json:"status"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	Usage          Usage      `json:"usage"`
	Failure        *Failure   `json:"failure,omitempty"`
	Provider       string     `json:"provider,omitempty"`
	Model          string     `json:"model,omitempty"`
}
type NodeRun struct {
	NodeID      string     `json:"node_id"`
	Status      Status     `json:"status"`
	Attempts    []Attempt  `json:"attempts"`
	Output      string     `json:"output,omitempty"`
	ArtifactID  string     `json:"artifact_id,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Failure     *Failure   `json:"failure,omitempty"`
}
type Artifact struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id"`
	AttemptNo int       `json:"attempt_no"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}
type Run struct {
	// UncertainTokens includes in-flight and unconfirmed attempt reservations.
	// It is not actual usage or a monetary amount.
	UncertainTokens   int        `json:"uncertain_tokens"`
	LeaseOwner        string     `json:"lease_owner,omitempty"`
	LeaseUntil        *time.Time `json:"lease_until,omitempty"`
	ID                string     `json:"id"`
	Scope             Scope      `json:"scope"`
	TeamID            uint64     `json:"team_id"`
	TeamKey           string     `json:"team_key"`
	TeamVersion       uint       `json:"team_version"`
	Name              string     `json:"name"`
	Input             string     `json:"input"`
	Plan              Plan       `json:"plan"`
	Status            Status     `json:"status"`
	Revision          uint64     `json:"revision"`
	Epoch             uint64     `json:"epoch"`
	LastSequence      uint64     `json:"last_sequence"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at,omitempty"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
	Deadline          time.Time  `json:"deadline"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	Nodes             []NodeRun  `json:"nodes"`
	Artifacts         []Artifact `json:"artifacts"`
	Failure           *Failure   `json:"failure,omitempty"`
	Usage             Usage      `json:"usage"`
	// Idempotency identities are private transport metadata, never credentials.
	KeyHash     string `json:"-"`
	Fingerprint string `json:"-"`
}
type Event struct {
	Sequence  uint64    `json:"sequence"`
	Type      EventType `json:"type"`
	NodeID    string    `json:"node_id,omitempty"`
	AttemptNo int       `json:"attempt_no,omitempty"`
	Status    Status    `json:"status,omitempty"`
	Text      string    `json:"text,omitempty"`
	Failure   *Failure  `json:"failure,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	ErrNotFound            = errors.New("workflow not found")
	ErrConflict            = errors.New("workflow revision conflict")
	ErrIdempotencyConflict = errors.New("workflow idempotency key reused with different input")
	ErrOwnedTask           = errors.New("workflow-owned task must be controlled through its workflow run")
	ErrCapacity            = errors.New("workflow execution capacity reached")
)

// Store owns atomic Run CAS, node/attempt/task projections, artifacts and event
// sequence allocation. Save must bind new attempts to prepared engine tasks and
// commit task terminal state in the same transaction as the attempt checkpoint.
type Store interface {
	Create(context.Context, Run, []Event) (Run, bool, error)
	Get(context.Context, Scope, string) (Run, error)
	List(context.Context, Scope, int) ([]Run, error)
	Save(context.Context, uint64, Run, []Event) (Run, error)
	Events(context.Context, Scope, string, uint64, int) ([]Event, error)
	Active(context.Context) ([]Run, error)
}
type Input struct {
	RunID        string
	NodeID       string
	AttemptNo    int
	TaskID       uint64
	Epoch        uint64
	Agent        Agent
	Objective    string
	Instruction  string
	Dependencies []Artifact
}
type Result struct {
	UsageKnown bool
	Status     Status
	Output     string
	Usage      Usage
	Provider   string
	Model      string
	Failure    *Failure
}
type Sink func(Event)
type Executor interface {
	Execute(context.Context, Input, Sink) (Result, error)
}
type ExecutorFunc func(context.Context, Input, Sink) (Result, error)

func (f ExecutorFunc) Execute(ctx context.Context, in Input, sink Sink) (Result, error) {
	return f(ctx, in, sink)
}
