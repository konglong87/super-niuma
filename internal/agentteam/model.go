package agentteam

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agentruntime"
)

type Status string

const (
	StatusDraft      Status = "draft"
	StatusValidating Status = "validating"
	StatusPublished  Status = "published"
	StatusArchived   Status = "archived"
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusCompleted  Status = "completed"
	StatusPartial    Status = "partial"
	StatusCancelled  Status = "cancelled"
	StatusFailed     Status = "failed"
	StatusTimedOut   Status = "timed_out"
)

type OrchestrationMode string

const (
	ModeCoordinator    OrchestrationMode = "coordinator"
	ModeParallelReview OrchestrationMode = "parallel_review"
)

type Role string

const (
	RoleCoordinator Role = "coordinator"
	RoleResearcher  Role = "researcher"
	RoleWriter      Role = "writer"
	RoleCoder       Role = "coder"
	RoleReviewer    Role = "reviewer"
)

type TriggerPolicy string

const (
	TriggerMention      TriggerPolicy = "mention"
	TriggerCommand      TriggerPolicy = "command"
	TriggerInternalOnly TriggerPolicy = "internal_only"
)

type ProfileRef struct {
	Key     string
	Version uint
}

type TeamPolicy struct {
	Mode                   OrchestrationMode
	CoordinatorMember      string
	MaxRounds              uint
	MaxParallelMembers     uint
	MaxTotalTokens         uint
	RunTimeoutSeconds      uint
	RequireMention         bool
	Commands               []string
	AllowDirectMessage     bool
	RequireTenantMember    bool
	AllowedExternalUserIDs []string
	DestructiveActionMode  string
	PhaseUpdates           string
	PostMemberCards        bool
}

// UnmarshalJSON accepts the persisted flat V1 shape and the WebUI's nested
// orchestration/trigger/output document. Keeping the translation here gives
// API, storage replay, and channel runtime one policy contract.
func (p *TeamPolicy) UnmarshalJSON(data []byte) error {
	type nestedOrchestration struct {
		Mode               OrchestrationMode `json:"mode"`
		CoordinatorMember  string            `json:"coordinator_member"`
		MaxRounds          uint              `json:"max_rounds"`
		MaxParallelMembers uint              `json:"max_parallel_members"`
		RunTimeoutSeconds  uint              `json:"run_timeout_seconds"`
		MaxTotalTokens     uint              `json:"max_total_tokens"`
	}
	type nestedTrigger struct {
		RequireMention     bool     `json:"require_mention"`
		Commands           []string `json:"commands"`
		AllowDirectMessage bool     `json:"allow_direct_message"`
	}
	type nestedOutput struct {
		PhaseUpdates    string `json:"phase_updates"`
		PostMemberCards bool   `json:"post_member_cards"`
	}
	type nestedAuthorization struct {
		RequireTenantMember    bool     `json:"require_tenant_member"`
		AllowedExternalUserIDs []string `json:"allowed_external_user_ids"`
		DestructiveActionMode  string   `json:"destructive_action_mode"`
	}
	var wire struct {
		Mode                   OrchestrationMode    `json:"mode"`
		CoordinatorMember      string               `json:"coordinator_member"`
		MaxRounds              uint                 `json:"max_rounds"`
		MaxParallelMembers     uint                 `json:"max_parallel_members"`
		RunTimeoutSeconds      uint                 `json:"run_timeout_seconds"`
		MaxTotalTokens         uint                 `json:"max_total_tokens"`
		RequireMention         bool                 `json:"require_mention"`
		Commands               []string             `json:"commands"`
		AllowDirectMessage     bool                 `json:"allow_direct_message"`
		RequireTenantMember    bool                 `json:"require_tenant_member"`
		AllowedExternalUserIDs []string             `json:"allowed_external_user_ids"`
		DestructiveActionMode  string               `json:"destructive_action_mode"`
		PhaseUpdates           string               `json:"phase_updates"`
		PostMemberCards        bool                 `json:"post_member_cards"`
		Orchestration          *nestedOrchestration `json:"orchestration"`
		Trigger                *nestedTrigger       `json:"trigger"`
		Authorization          *nestedAuthorization `json:"authorization"`
		Output                 *nestedOutput        `json:"output"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*p = TeamPolicy{Mode: wire.Mode, CoordinatorMember: wire.CoordinatorMember, MaxRounds: wire.MaxRounds, MaxParallelMembers: wire.MaxParallelMembers, RunTimeoutSeconds: wire.RunTimeoutSeconds, MaxTotalTokens: wire.MaxTotalTokens, RequireMention: wire.RequireMention, Commands: wire.Commands, AllowDirectMessage: wire.AllowDirectMessage, RequireTenantMember: wire.RequireTenantMember, AllowedExternalUserIDs: wire.AllowedExternalUserIDs, DestructiveActionMode: wire.DestructiveActionMode, PhaseUpdates: wire.PhaseUpdates, PostMemberCards: wire.PostMemberCards}
	if wire.Orchestration != nil {
		p.Mode, p.CoordinatorMember, p.MaxRounds = wire.Orchestration.Mode, wire.Orchestration.CoordinatorMember, wire.Orchestration.MaxRounds
		p.MaxParallelMembers, p.RunTimeoutSeconds, p.MaxTotalTokens = wire.Orchestration.MaxParallelMembers, wire.Orchestration.RunTimeoutSeconds, wire.Orchestration.MaxTotalTokens
	}
	if wire.Trigger != nil {
		p.RequireMention, p.Commands, p.AllowDirectMessage = wire.Trigger.RequireMention, wire.Trigger.Commands, wire.Trigger.AllowDirectMessage
	}
	if wire.Authorization != nil {
		p.RequireTenantMember, p.AllowedExternalUserIDs, p.DestructiveActionMode = wire.Authorization.RequireTenantMember, wire.Authorization.AllowedExternalUserIDs, wire.Authorization.DestructiveActionMode
	}
	if wire.Output != nil {
		p.PhaseUpdates, p.PostMemberCards = wire.Output.PhaseUpdates, wire.Output.PostMemberCards
	}
	return nil
}

type Member struct {
	TenantID            uint64
	Key                 string
	ProfileKey          string
	ProfileVersion      uint
	Role                Role
	AccountKey          string
	ToolPolicyJSON      string
	WorkspacePolicyJSON string
	ExecutionOverride   agentruntime.AgentExecutionOverride
}

type Binding struct {
	TenantID         uint64
	TeamID           uint64
	TeamVersion      uint
	Provider         string
	AccountKey       string
	ExternalChatID   string
	ExternalThreadID string
	Trigger          TriggerPolicy
	Commands         []string
}

type InboundEvent struct {
	Provider               string
	AccountKey             string
	ExternalConversationID string
	ExternalThreadID       string
	ExternalUserID         string
	ChatType               string
	Text                   string
	MentionedBot           bool
}

type Team struct {
	TenantID      uint64
	ID            uint64
	Key           string
	Version       uint
	Status        Status
	EffectiveHash string
	Policy        TeamPolicy
	Members       []Member
	Bindings      []Binding
}

type RunOptions struct {
	InboxEventID    uint64
	SourceAccountID uint64
	ConversationID  uint64
}

type RunRecord struct {
	RunID              string
	TenantID           uint64
	TeamID             uint64
	InboxEventID       uint64
	SourceAccountID    uint64
	ConversationID     uint64
	CoordinatorMember  string
	Status             Status
	TeamEffectiveHash  string
	MemberCount        uint
	MaxRounds          uint
	MaxParallelMembers uint
	MaxTotalTokens     uint
	UsedTokens         uint
	UsedTurns          uint
	Result             string
	Error              string
	StartedAt          time.Time
	FinishedAt         time.Time
}

type RunRecorder interface {
	Start(context.Context, RunRecord) error
	Finish(context.Context, RunRecord) error
}

const (
	RunEventRunStarted         = "run_started"
	RunEventMemberStarted      = "member_started"
	RunEventMemberCompleted    = "member_completed"
	RunEventMemberFailed       = "member_failed"
	RunEventCoordinatorStarted = "coordinator_started"
	RunEventCoordinatorDelta   = "coordinator_delta"
	RunEventCoordinatorDone    = "coordinator_completed"
	RunEventMailboxMessage     = "mailbox_message"
	RunEventRunFinished        = "run_finished"
)

type RunEvent struct {
	TenantID    uint64
	RunID       string
	Sequence    uint64
	EventType   string
	MemberKey   string
	FromMember  string
	ToMember    string
	Status      Status
	Summary     string
	PayloadJSON string
	ArtifactRef string
	CreatedAt   time.Time
}

type RunEventRecorder interface {
	Append(context.Context, RunEvent) error
}

type ProfileAvailability struct {
	PublishedProfiles map[ProfileRef]bool
}

type ValidationContext = ProfileAvailability

type ValidationIssue struct {
	Code    string
	Field   string
	Message string
}

type ValidationReport struct {
	Valid  bool
	Issues []ValidationIssue
}

func (r ValidationReport) Has(code string) bool {
	for _, issue := range r.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

var (
	ErrMessageNotFound = errors.New("agent team mailbox message not found")
	ErrTeamNotFound    = errors.New("agent team not found")
)

type Message struct {
	TenantID       uint64
	RunID          string
	FromMember     string
	ToMember       string
	Kind           string
	Sequence       uint64
	IdempotencyKey string
	Content        string
	EvidenceRef    string
	Status         string
}

type Mailbox interface {
	Append(context.Context, Message) error
	List(context.Context, uint64, string) ([]Message, error)
	Consume(context.Context, uint64, string, string) error
}

type MemoryMailbox struct {
	mu       sync.Mutex
	messages map[string]Message
}

func NewMemoryMailbox() *MemoryMailbox {
	return &MemoryMailbox{messages: make(map[string]Message)}
}

func (m *MemoryMailbox) Append(_ context.Context, message Message) error {
	if m == nil || message.TenantID == 0 || message.RunID == "" || message.IdempotencyKey == "" {
		return ErrMessageNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.messages[mailboxKey(message.TenantID, message.RunID, message.IdempotencyKey)]; exists {
		return nil
	}
	if message.Status == "" {
		message.Status = "pending"
	}
	m.messages[mailboxKey(message.TenantID, message.RunID, message.IdempotencyKey)] = message
	return nil
}

func (m *MemoryMailbox) List(_ context.Context, tenantID uint64, runID string) ([]Message, error) {
	if m == nil || tenantID == 0 || runID == "" {
		return nil, ErrMessageNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Message, 0)
	for _, message := range m.messages {
		if message.TenantID == tenantID && message.RunID == runID {
			result = append(result, message)
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	return result, nil
}

func (m *MemoryMailbox) Consume(_ context.Context, tenantID uint64, runID, idempotencyKey string) error {
	if m == nil || tenantID == 0 || runID == "" || idempotencyKey == "" {
		return ErrMessageNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mailboxKey(tenantID, runID, idempotencyKey)
	message, exists := m.messages[key]
	if !exists {
		return ErrMessageNotFound
	}
	message.Status = "consumed"
	m.messages[key] = message
	return nil
}

func mailboxKey(tenantID uint64, runID, idempotencyKey string) string {
	return strconv.FormatUint(tenantID, 10) + "\x00" + runID + "\x00" + idempotencyKey
}
