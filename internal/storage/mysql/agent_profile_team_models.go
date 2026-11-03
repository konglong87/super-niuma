package mysql

import "time"

type AgentProfileInput struct {
	TenantID        uint64
	OwnerUserID     uint64
	OwnerKey        string
	ProfileKey      string
	Scope           string
	DisplayName     string
	Description     string
	ProfileVersion  uint
	Status          string
	ConfigJSON      string
	RequestedHash   string
	EffectiveHash   string
	ValidationJSON  string
	CreatedByUserID uint64
	UpdatedByUserID uint64
	PublishedAt     *time.Time
}

type AgentProfile struct {
	ID              uint64    `json:"id"`
	TenantID        uint64    `json:"tenant_id"`
	OwnerUserID     uint64    `json:"owner_user_id,omitempty"`
	OwnerKey        string    `json:"owner_key"`
	ProfileKey      string    `json:"profile_key"`
	Scope           string    `json:"scope"`
	DisplayName     string    `json:"display_name"`
	Description     string    `json:"description,omitempty"`
	ProfileVersion  uint      `json:"profile_version"`
	Status          string    `json:"status"`
	ConfigJSON      string    `json:"config_json"`
	RequestedHash   string    `json:"requested_hash"`
	EffectiveHash   string    `json:"effective_hash"`
	ValidationJSON  string    `json:"validation_json,omitempty"`
	CreatedByUserID uint64    `json:"created_by_user_id"`
	UpdatedByUserID uint64    `json:"updated_by_user_id"`
	SourceKind      string    `json:"source_kind,omitempty"`
	SourceRef       string    `json:"source_ref,omitempty"`
	SourcePath      string    `json:"source_path,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AgentProfileConversationSummary struct {
	ConversationID     uint64    `json:"conversation_id"`
	SessionID          uint64    `json:"session_id,omitempty"`
	AccountID          uint64    `json:"account_id"`
	AccountKey         string    `json:"account_key"`
	ExternalChatID     string    `json:"external_chat_id"`
	ExternalThreadID   string    `json:"external_thread_id,omitempty"`
	ChatType           string    `json:"chat_type"`
	ConversationStatus string    `json:"conversation_status"`
	Title              string    `json:"title,omitempty"`
	Model              string    `json:"model,omitempty"`
	LastMessageAt      time.Time `json:"last_message_at,omitempty"`
	LastInboundAt      time.Time `json:"last_inbound_at,omitempty"`
	LastOutboundAt     time.Time `json:"last_outbound_at,omitempty"`
	MessageCount       uint64    `json:"message_count"`
	RunCount           uint64    `json:"run_count"`
	LatestRunStatus    string    `json:"latest_run_status,omitempty"`
	LastMessageRole    string    `json:"last_message_role,omitempty"`
	LastMessagePreview string    `json:"last_message_preview,omitempty"`
}

type AgentProfileTeamLink struct {
	TeamID          uint64 `json:"team_id"`
	TeamKey         string `json:"team_key"`
	TeamVersion     uint   `json:"team_version"`
	TeamDisplayName string `json:"team_display_name"`
	MemberKey       string `json:"member_key"`
	Role            string `json:"role"`
	AccountID       uint64 `json:"account_id,omitempty"`
	AccountKey      string `json:"account_key,omitempty"`
	ExternalChatID  string `json:"external_chat_id,omitempty"`
	TriggerPolicy   string `json:"trigger_policy,omitempty"`
	Status          string `json:"status"`
}

type AgentProfileConversationCatalog struct {
	Profile       AgentProfile                      `json:"profile"`
	Conversations []AgentProfileConversationSummary `json:"conversations"`
	Teams         []AgentProfileTeamLink            `json:"teams"`
	MessageCount  uint64                            `json:"message_count"`
	RunCount      uint64                            `json:"run_count"`
}

type AgentProfileAssignmentInput struct {
	TenantID         uint64
	UserID           uint64
	Surface          string
	ProfileID        uint64
	AssignedByUserID uint64
}

type AgentProfileAssignment struct {
	ID               uint64    `json:"id"`
	TenantID         uint64    `json:"tenant_id"`
	UserID           uint64    `json:"user_id"`
	Surface          string    `json:"surface"`
	ProfileID        uint64    `json:"profile_id"`
	AssignedByUserID uint64    `json:"assigned_by_user_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AgentProfileChannelBindingInput struct {
	TenantID        uint64
	ProfileID       uint64
	AccountID       uint64
	Provider        string
	BindingKey      string
	Status          string
	CreatedByUserID uint64
}

type AgentProfileChannelBinding struct {
	ID              uint64    `json:"id"`
	TenantID        uint64    `json:"tenant_id"`
	ProfileID       uint64    `json:"profile_id"`
	AccountID       uint64    `json:"account_id"`
	Provider        string    `json:"provider"`
	BindingKey      string    `json:"binding_key"`
	Status          string    `json:"status"`
	CreatedByUserID uint64    `json:"created_by_user_id"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ArchivedAt      time.Time `json:"archived_at,omitempty"`
}

type AgentTeamInput struct {
	TenantID        uint64
	OwnerUserID     uint64
	OwnerKey        string
	TeamKey         string
	TeamVersion     uint
	Scope           string
	DisplayName     string
	Description     string
	Status          string
	SchemaVersion   uint
	PolicyJSON      string
	RequestedHash   string
	EffectiveHash   string
	ValidationJSON  string
	CreatedByUserID uint64
	UpdatedByUserID uint64
	PublishedAt     *time.Time
}

type AgentTeam struct {
	ID              uint64    `json:"id"`
	TenantID        uint64    `json:"tenant_id"`
	OwnerUserID     uint64    `json:"owner_user_id,omitempty"`
	OwnerKey        string    `json:"owner_key"`
	TeamKey         string    `json:"team_key"`
	TeamVersion     uint      `json:"team_version"`
	Scope           string    `json:"scope"`
	DisplayName     string    `json:"display_name"`
	Description     string    `json:"description,omitempty"`
	Status          string    `json:"status"`
	SchemaVersion   uint      `json:"schema_version"`
	PolicyJSON      string    `json:"policy_json"`
	RequestedHash   string    `json:"requested_hash"`
	EffectiveHash   string    `json:"effective_hash"`
	ValidationJSON  string    `json:"validation_json,omitempty"`
	CreatedByUserID uint64    `json:"created_by_user_id"`
	UpdatedByUserID uint64    `json:"updated_by_user_id"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AgentTeamMemberInput struct {
	TenantID              uint64
	TeamID                uint64
	MemberKey             string
	ProfileID             uint64
	Role                  string
	AccountID             uint64
	ToolPolicyJSON        string
	WorkspacePolicyJSON   string
	ExecutionOverrideJSON string
	Status                string
}

type AgentTeamMember struct {
	ID                    uint64    `json:"id"`
	TenantID              uint64    `json:"tenant_id"`
	TeamID                uint64    `json:"team_id"`
	MemberKey             string    `json:"member_key"`
	ProfileID             uint64    `json:"profile_id"`
	Role                  string    `json:"role"`
	AccountID             uint64    `json:"account_id,omitempty"`
	ToolPolicyJSON        string    `json:"tool_policy_json,omitempty"`
	WorkspacePolicyJSON   string    `json:"workspace_policy_json,omitempty"`
	ExecutionOverrideJSON string    `json:"execution_override_json,omitempty"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type AgentTeamBindingInput struct {
	TenantID         uint64
	TeamID           uint64
	Provider         string
	AccountID        uint64
	ExternalChatID   string
	ExternalThreadID string
	TriggerPolicy    string
	Status           string
}

type AgentTeamBinding struct {
	ID               uint64    `json:"id"`
	TenantID         uint64    `json:"tenant_id"`
	TeamID           uint64    `json:"team_id"`
	Provider         string    `json:"provider"`
	AccountID        uint64    `json:"account_id"`
	ExternalChatID   string    `json:"external_chat_id"`
	ExternalThreadID string    `json:"external_thread_id,omitempty"`
	TriggerPolicy    string    `json:"trigger_policy"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type AgentTeamRunInput struct {
	ID                   string
	TenantID             uint64
	TeamID               uint64
	InboxEventID         uint64
	SourceAccountID      uint64
	ConversationID       uint64
	CoordinatorMemberKey string
	Status               string
	TeamEffectiveHash    string
	MemberCount          uint
	MaxRounds            uint
	MaxParallelMembers   uint
	MaxTotalTokens       uint
	StartedAt            *time.Time
}

type AgentTeamRun struct {
	ID                   string    `json:"id"`
	TenantID             uint64    `json:"tenant_id"`
	TeamID               uint64    `json:"team_id"`
	InboxEventID         uint64    `json:"inbox_event_id"`
	SourceAccountID      uint64    `json:"source_account_id"`
	SourceKind           string    `json:"source_kind,omitempty"`
	ConversationID       uint64    `json:"conversation_id,omitempty"`
	CoordinatorMemberKey string    `json:"coordinator_member_key"`
	Status               string    `json:"status"`
	TeamEffectiveHash    string    `json:"team_effective_hash"`
	MemberCount          uint      `json:"member_count"`
	MaxRounds            uint      `json:"max_rounds"`
	MaxParallelMembers   uint      `json:"max_parallel_members"`
	MaxTotalTokens       uint      `json:"max_total_tokens"`
	UsedTokens           uint      `json:"used_tokens"`
	UsedTurns            uint      `json:"used_turns"`
	HeartbeatAt          time.Time `json:"heartbeat_at,omitempty"`
	CancelRequestedAt    time.Time `json:"cancel_requested_at,omitempty"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	FinishedAt           time.Time `json:"finished_at,omitempty"`
	ResultJSON           string    `json:"result_json,omitempty"`
	ErrorCode            string    `json:"error_code,omitempty"`
	ErrorMessage         string    `json:"error_message,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type AgentTeamRunUpdate struct {
	TenantID          uint64
	RunID             string
	Status            string
	UsedTokens        uint
	UsedTurns         uint
	HeartbeatAt       *time.Time
	CancelRequestedAt *time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
	ResultJSON        string
	ErrorCode         string
	ErrorMessage      string
}

type AgentTeamMailboxInput struct {
	TenantID          uint64
	TeamRunID         string
	FromMemberKey     string
	ToMemberKey       string
	MessageKind       string
	SequenceNo        uint64
	IdempotencyKey    string
	PayloadRef        string
	PayloadCiphertext []byte
	EvidenceRef       string
	Status            string
}

type AgentTeamRunEventInput struct {
	TenantID      uint64
	TeamRunID     string
	SequenceNo    uint64
	EventType     string
	MemberKey     string
	FromMemberKey string
	ToMemberKey   string
	Status        string
	Summary       string
	PayloadJSON   string
	ArtifactRef   string
	CreatedAt     time.Time
}

type AgentTeamRunEvent struct {
	ID            uint64    `json:"id"`
	TenantID      uint64    `json:"tenant_id"`
	TeamRunID     string    `json:"team_run_id"`
	SequenceNo    uint64    `json:"sequence_no"`
	EventType     string    `json:"event_type"`
	MemberKey     string    `json:"member_key,omitempty"`
	FromMemberKey string    `json:"from_member_key,omitempty"`
	ToMemberKey   string    `json:"to_member_key,omitempty"`
	Status        string    `json:"status,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	PayloadJSON   string    `json:"payload_json,omitempty"`
	ArtifactRef   string    `json:"artifact_ref,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type AgentTeamMailbox struct {
	ID                uint64    `json:"id"`
	TenantID          uint64    `json:"tenant_id"`
	TeamRunID         string    `json:"team_run_id"`
	FromMemberKey     string    `json:"from_member_key"`
	ToMemberKey       string    `json:"to_member_key"`
	MessageKind       string    `json:"message_kind"`
	SequenceNo        uint64    `json:"sequence_no"`
	IdempotencyKey    string    `json:"idempotency_key"`
	PayloadRef        string    `json:"payload_ref,omitempty"`
	PayloadCiphertext []byte    `json:"-"`
	EvidenceRef       string    `json:"evidence_ref,omitempty"`
	Status            string    `json:"status"`
	LeaseOwner        string    `json:"lease_owner,omitempty"`
	LeaseUntil        time.Time `json:"lease_until,omitempty"`
	ConsumedAt        time.Time `json:"consumed_at,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
