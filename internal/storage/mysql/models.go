package mysql

// 共享模型与输入 DTO，由 GormRepository 及 tenant/server 等包共同使用。

import (
	"errors"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/quota"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	DefaultUserRole      = "member"
	DesktopLocalUserRole = "owner"
)

var (
	ErrNotFound                 = errors.New("mysql storage: not found")
	ErrInvalidInput             = errors.New("mysql storage: invalid input")
	ErrInvalidState             = errors.New("mysql storage: invalid state")
	ErrIdempotencyConflict      = errors.New("mysql storage: idempotency conflict")
	ErrOperationLockUnavailable = errors.New("mysql storage: session control operation lock unavailable")
	// ErrTooManyMessages 表示一个会话的消息条数超过了调用方要求的完整读取上限。
	// 存在的意义是「宁可失败也不返回被静默截断的列表」，见 ListAllMessages。
	ErrTooManyMessages = errors.New("mysql storage: too many messages")
	// ErrTooManyAgentTaskEvents 表示会话控制所需的完整任务事件窗口超过读取上限。
	// 调用方必须显式处理，不能把缺失 permission_resolved 的半截事件当作完整状态。
	ErrTooManyAgentTaskEvents = errors.New("mysql storage: too many agent task events")
)

type Tenant struct {
	ID           uint64 `json:"id"`
	TenantKey    string `json:"tenant_key"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	SettingsJSON string `json:"settings_json,omitempty"`
}

type TenantInput struct {
	TenantKey    string
	Name         string
	Status       string
	SettingsJSON string
}

type UserInput struct {
	TenantID     uint64
	UserKey      string
	Email        string
	DisplayName  string
	Role         string
	Status       string
	UserInfoJSON string
	MetadataJSON string
}

type User struct {
	ID           uint64    `json:"id"`
	TenantID     uint64    `json:"tenant_id"`
	UserKey      string    `json:"user_key"`
	Email        string    `json:"email,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	UserInfoJSON string    `json:"user_info_json,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type MemoryInput struct {
	TenantID     uint64
	UserID       uint64
	MemoryKey    string
	Category     string
	Content      string
	MetadataJSON string
	Importance   int
	EmbeddingRef string
	Source       string
}

type Memory struct {
	ID           uint64    `json:"id"`
	MemoryKey    string    `json:"memory_key"`
	Category     string    `json:"category"`
	Content      string    `json:"content"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	Importance   int       `json:"importance"`
	Source       string    `json:"source,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SkillInput struct {
	TenantID        uint64
	SkillKey        string
	Name            string
	Description     string
	ContentMD       string
	ConfigJSON      string
	PackageRef      string
	PackageSHA256   string
	ManifestJSON    string
	RuntimeRef      string
	Version         uint
	Enabled         bool
	CreatedByUserID uint64
}

type SkillRollbackInput struct {
	TenantID        uint64
	SkillKey        string
	FromVersion     uint
	TargetVersion   uint
	CreatedByUserID uint64
}

type Skill struct {
	ID            uint64    `json:"id"`
	SkillKey      string    `json:"skill_key"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	ContentMD     string    `json:"content_md,omitempty"`
	ConfigJSON    string    `json:"config_json,omitempty"`
	PackageRef    string    `json:"package_ref,omitempty"`
	PackageSHA256 string    `json:"package_sha256,omitempty"`
	ManifestJSON  string    `json:"manifest_json,omitempty"`
	RuntimeRef    string    `json:"runtime_ref,omitempty"`
	Version       uint      `json:"version"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type SkillOverrideInput struct {
	TenantID   uint64
	UserID     uint64
	SkillID    uint64
	Enabled    bool
	ConfigJSON string
}

type SkillOverride struct {
	ID         uint64    `json:"id"`
	SkillID    uint64    `json:"skill_id"`
	SkillKey   string    `json:"skill_key"`
	Name       string    `json:"name"`
	Version    uint      `json:"version"`
	Enabled    bool      `json:"enabled"`
	ConfigJSON string    `json:"config_json,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type EffectiveSkill struct {
	Skill
	OverrideID uint64 `json:"override_id,omitempty"`
}

type DocumentInput struct {
	TenantID    uint64
	UserID      uint64
	DocType     string
	Title       string
	ContentMD   string
	ContentJSON string
	Version     uint
	Active      bool
}

type Document struct {
	ID          uint64 `json:"id"`
	DocType     string `json:"doc_type"`
	Title       string `json:"title,omitempty"`
	ContentMD   string `json:"content_md,omitempty"`
	ContentJSON string `json:"content_json,omitempty"`
	Version     uint   `json:"version"`
	Active      bool   `json:"active"`
}

type KnowledgeDocumentInput struct {
	TenantID     uint64
	UserID       uint64
	Title        string
	SourceType   string
	Content      string
	MetadataJSON string
	Status       string
}

type KnowledgeDocument struct {
	ID           uint64    `json:"id"`
	TenantID     uint64    `json:"tenant_id,omitempty"`
	UserID       uint64    `json:"user_id,omitempty"`
	Title        string    `json:"title"`
	SourceType   string    `json:"source_type"`
	Content      string    `json:"content,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type KnowledgeChunkInput struct {
	TenantID     uint64
	DocumentID   uint64
	ChunkIndex   uint
	Content      string
	MetadataJSON string
	EmbeddingRef string
}

type KnowledgeSearchOptions struct {
	Query  string
	Limit  int
	UserID uint64
}

type KnowledgeChunk struct {
	ID           uint64  `json:"id"`
	DocumentID   uint64  `json:"document_id"`
	Title        string  `json:"title,omitempty"`
	SourceType   string  `json:"source_type,omitempty"`
	ChunkIndex   uint    `json:"chunk_index"`
	Content      string  `json:"content"`
	MetadataJSON string  `json:"metadata_json,omitempty"`
	EmbeddingRef string  `json:"embedding_ref,omitempty"`
	Score        float64 `json:"score"`
	SearchMode   string  `json:"search_mode,omitempty"`
}

type ProfileInput struct {
	TenantID               uint64
	UserID                 uint64
	ProfileVersion         uint
	Summary                string
	ProfileJSON            string
	GeneratedFromSessionID uint64
}

type Profile struct {
	ID                     uint64    `json:"id"`
	ProfileVersion         uint      `json:"profile_version"`
	Summary                string    `json:"summary,omitempty"`
	ProfileJSON            string    `json:"profile_json"`
	GeneratedFromSessionID uint64    `json:"generated_from_session_id,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
}

type SessionInput struct {
	ID           uint64
	TenantID     uint64
	UserID       uint64
	SessionKey   string
	Title        string
	Status       string
	Model        string
	CWD          string
	MetadataJSON string
}

type Session struct {
	ID            uint64    `json:"id"`
	SessionKey    string    `json:"session_key"`
	Title         string    `json:"title,omitempty"`
	Status        string    `json:"status"`
	Model         string    `json:"model,omitempty"`
	CWD           string    `json:"cwd,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	LastMessageAt time.Time `json:"last_message_at,omitempty"`
}

// SessionControlSession includes the private metadata needed for durable
// operation recovery. Public session projections continue to omit it.
type SessionControlSession struct {
	Session
	MetadataJSON string `json:"-"`
}

type SessionChannel struct {
	Provider   string
	AccountKey string
}

type SessionControlCreateInput struct {
	Session SessionInput
}

type SessionControlCreateResult struct {
	Session SessionControlSession
	Created bool
}

type MessageInput struct {
	TenantID    uint64
	UserID      uint64
	SessionID   uint64
	TurnIndex   uint
	Role        string
	Content     string
	ContentJSON string
	ToolID      string
	ToolName    string
	IsError     bool
	Model       string
	InputTokens uint
	OutputToken uint
	TraceID     string
}

type Message struct {
	ID           uint64    `json:"id"`
	SessionID    uint64    `json:"session_id"`
	TurnIndex    uint      `json:"turn_index"`
	Role         string    `json:"role"`
	Content      string    `json:"content,omitempty"`
	ContentJSON  string    `json:"content_json,omitempty"`
	ToolID       string    `json:"tool_id,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	IsError      bool      `json:"is_error"`
	Model        string    `json:"model,omitempty"`
	InputTokens  uint      `json:"input_tokens,omitempty"`
	OutputTokens uint      `json:"output_tokens,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type AgentTask struct {
	ID                 uint64    `json:"id"`
	TenantID           uint64    `json:"tenant_id"`
	UserID             uint64    `json:"user_id"`
	ParentSessionID    uint64    `json:"parent_session_id,omitempty"`
	SubagentSessionKey string    `json:"subagent_session_key,omitempty"`
	AgentName          string    `json:"agent_name,omitempty"`
	Description        string    `json:"description,omitempty"`
	Status             string    `json:"status"`
	Model              string    `json:"model,omitempty"`
	ResultJSON         string    `json:"result_json,omitempty"`
	MetadataJSON       string    `json:"metadata_json,omitempty"`
	TraceID            string    `json:"trace_id,omitempty"`
	IdempotencyKey     string    `json:"idempotency_key,omitempty"`
	StartedAt          time.Time `json:"started_at"`
	FinishedAt         time.Time `json:"finished_at,omitempty"`
}

type SessionLinkInput struct {
	TenantID         uint64
	UserID           uint64
	TargetSessionID  uint64
	SourceKind       string
	SourceSessionKey string
	SourceSessionID  uint64
	RelationType     string
	Status           string
	MetadataJSON     string
	CreatedByUserID  uint64
}

type SessionLink struct {
	ID               uint64    `json:"id"`
	TenantID         uint64    `json:"tenant_id"`
	UserID           uint64    `json:"user_id"`
	TargetSessionID  uint64    `json:"target_session_id"`
	SourceKind       string    `json:"source_kind"`
	SourceSessionKey string    `json:"source_session_key"`
	SourceSessionID  uint64    `json:"source_session_id,omitempty"`
	RelationType     string    `json:"relation_type"`
	Status           string    `json:"status"`
	MetadataJSON     string    `json:"metadata_json,omitempty"`
	CreatedByUserID  uint64    `json:"created_by_user_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// SessionMonitorTarget is the bounded delivery binding resolved before a
// monitor writes either its authoritative link or file-backed projection.
type SessionMonitorTarget struct {
	SessionID        uint64
	SessionKey       string
	AccountID        uint64
	AccountKey       string
	ConversationID   uint64
	ExternalChatID   string
	ExternalThreadID string
}

type SessionMonitorLinkInput struct {
	TenantID         uint64
	UserID           uint64
	TargetSessionID  uint64
	SourceKind       string
	SourceSessionKey string
	RelationType     string
	MetadataJSON     string
	CreatedByUserID  uint64
}

// SessionMonitorRecord contains only trusted identity, routing and bounded
// monitor metadata recovered from MySQL by a scheduler child.
type SessionMonitorRecord struct {
	Link             SessionLink
	TenantKey        string
	UserKey          string
	TargetSessionKey string
	AccountID        uint64
	AccountKey       string
	ConversationID   uint64
	ExternalChatID   string
	ExternalThreadID string
}

type SessionMonitorObservationInput struct {
	TenantID             uint64
	UserID               uint64
	LinkID               uint64
	ExpectedMetadataJSON string
	MetadataJSON         string
	AccountID            uint64
	ConversationID       uint64
	IdempotencyKey       string
	PayloadJSON          string
	Deliver              bool
}

type SessionMonitorObservationResult struct {
	Committed bool
	Replayed  bool
	MessageID uint64
	OutboxID  uint64
}

type AgentTaskEvent struct {
	ID          uint64    `json:"id"`
	TaskID      uint64    `json:"task_id"`
	EventType   string    `json:"event_type"`
	PayloadJSON string    `json:"payload_json,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	Source      string    `json:"source,omitempty"`
	Surface     string    `json:"surface,omitempty"`
	Channel     string    `json:"channel,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type goalRow struct {
	NumericID uint64
	Goal      goal.Goal
}

type goalPlanRow struct {
	GoalID   string
	PlanJSON string
}

type AuditLogInput struct {
	TenantID     uint64
	ActorUserID  uint64
	Action       string
	ResourceType string
	ResourceID   string
	MetadataJSON string
	TraceID      string
}

type AuditLog struct {
	ID           uint64    `json:"id"`
	TenantID     uint64    `json:"tenant_id"`
	ActorUserID  uint64    `json:"actor_user_id,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id,omitempty"`
	MetadataJSON string    `json:"metadata_json,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type SessionControlAuditInput struct {
	AuditLogInput
}

type SessionControlAuditResult struct {
	Audit    AuditLog
	Replayed bool
}

type SessionControlStopInput struct {
	TenantID         uint64
	UserID           uint64
	TaskID           uint64
	ResultJSON       string
	EventPayloadJSON string
	TraceID          string
}

type SessionControlStopResult struct {
	Cancelled bool
	EventID   uint64
}

type SessionControlStopRecovery struct {
	Found            bool
	TaskID           uint64
	EventID          uint64
	EventPayloadJSON string
}

type SessionControlRunInput struct {
	AdoptTaskID               uint64
	Task                      agenttasks.TaskInput
	Message                   agenttasks.MessageInput
	ExpectedRuntimeConfigJSON string
	RuntimeConfigChanged      bool
	// SkipMessageEvent keeps the task/control-plane transaction free of chat
	// content when the selected session event backend owns the transcript.
	SkipMessageEvent bool
}

type SessionControlRunResult struct {
	Task    AgentTask
	EventID uint64
	Created bool
	Busy    bool
}

type SessionControlRunStartResult struct {
	Task    AgentTask
	Started bool
}

type SessionControlRunFailureInput struct {
	TenantID         uint64
	UserID           uint64
	TaskID           uint64
	ResultJSON       string
	EventPayloadJSON string
	TraceID          string
}

type SessionControlRunFailureResult struct {
	Failed  bool
	EventID uint64
}

type TelemetryEventInput struct {
	TenantID uint64
	UserID   uint64
	Event    telemetry.Event
}

type TelemetryEvent struct {
	telemetry.Event
	CreatedAt time.Time `json:"created_at"`
}

const (
	ChannelProviderDingTalk = "dingtalk"
	ChannelProviderFeishu   = "feishu"

	ChannelAccountModeStream  = "stream"
	ChannelAccountModeWebhook = "webhook"

	ChannelAccountStatusDisabled = "disabled"
	ChannelAccountStatusStarting = "starting"
	ChannelAccountStatusReady    = "ready"
	ChannelAccountStatusDegraded = "degraded"
	ChannelAccountStatusFailed   = "failed"

	ChannelInboxStatusReceived   = "received"
	ChannelInboxStatusQueued     = "queued"
	ChannelInboxStatusProcessing = "processing"
	ChannelInboxStatusProcessed  = "processed"
	ChannelInboxStatusIgnored    = "ignored"
	ChannelInboxStatusRetry      = "retry"
	ChannelInboxStatusFailed     = "failed"

	ChannelOutboxStatusPending         = "pending"
	ChannelOutboxStatusSending         = "sending"
	ChannelOutboxStatusSent            = "sent"
	ChannelOutboxStatusRetry           = "retry"
	ChannelOutboxStatusDeliveryUnknown = "delivery_unknown"
	ChannelOutboxStatusDead            = "dead"

	ChannelRunStatusQueued          = "queued"
	ChannelRunStatusRunning         = "running"
	ChannelRunStatusCompleted       = "completed"
	ChannelRunStatusFailed          = "failed"
	ChannelRunStatusCancelRequested = "cancel_requested"
	ChannelRunStatusCancelled       = "cancelled"
	ChannelRunStatusInterrupted     = "interrupted"
	ChannelRunStatusWaitingInput    = "waiting_input"

	ChannelInteractionKindUserQuestion = "user_question"
	ChannelInteractionStatusPending    = "pending"
	ChannelInteractionStatusAnswered   = "answered"
	ChannelInteractionStatusExpired    = "expired"
	ChannelInteractionStatusCancelled  = "cancelled"

	ChannelCallbackStatusPending  = "pending"
	ChannelCallbackStatusConsumed = "consumed"
	ChannelCallbackStatusExpired  = "expired"
	ChannelCallbackStatusRevoked  = "revoked"

	ChannelConversationStatusActive   = "active"
	ChannelConversationStatusArchived = "archived"
	ChannelConversationStatusBlocked  = "blocked"
	ChannelMessageStatusPending       = "pending"
	ChannelMessageStatusSent          = "sent"
	ChannelMessageStatusFailed        = "failed"
	ChannelMessageStatusRecalled      = "recalled"
	ChannelMessageOperationCreate     = "create"
	ChannelMessageOperationUpdate     = "update"
	ChannelMessageOperationRecall     = "recall"
	ChannelMessageKindText            = "text"
	ChannelMessageKindMarkdown        = "markdown"
	ChannelMessageKindCard            = "card"
	ChannelChatTypeP2P                = "p2p"
	ChannelChatTypeGroup              = "group"
	ChannelMessageDirectionInbound    = "inbound"
	ChannelMessageDirectionOutbound   = "outbound"
	ChannelThreadIDSourceEvent        = "event"
	ChannelThreadIDSourceRawLookup    = "raw_message_lookup"
	ChannelThreadIDSourceNone         = "none"
	ChannelReactionStatusPending      = "pending"
	ChannelReactionStatusReconciling  = "reconciling"
	ChannelReactionStatusApplied      = "applied"
	ChannelReactionStatusRetry        = "retry"
	ChannelPermissionModeAsk          = "ask"
	ChannelPermissionModeAllow        = "allow"
	ChannelPermissionModeDeny         = "deny"
)

type ChannelAccountInput struct {
	ID               uint64
	TenantID         uint64
	Provider         string
	AccountKey       string
	AppID            string
	CredentialRef    string
	Mode             string
	Enabled          bool
	PolicyJSON       string
	Status           string
	LastConnectedAt  *time.Time
	LastErrorCode    string
	LastErrorMessage string
	ArchivedAt       *time.Time
}

type ChannelAccount struct {
	ID               uint64    `json:"id"`
	TenantID         uint64    `json:"tenant_id"`
	Provider         string    `json:"provider"`
	AccountKey       string    `json:"account_key"`
	AppID            string    `json:"app_id"`
	CredentialRef    string    `json:"credential_ref,omitempty"`
	Mode             string    `json:"mode"`
	Enabled          bool      `json:"enabled"`
	PolicyJSON       string    `json:"policy_json"`
	Status           string    `json:"status"`
	LastConnectedAt  time.Time `json:"last_connected_at,omitempty"`
	LastErrorCode    string    `json:"last_error_code,omitempty"`
	LastErrorMessage string    `json:"last_error_message,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ArchivedAt       time.Time `json:"archived_at,omitempty"`
}

type ChannelIdentityInput struct {
	TenantID        uint64
	AccountID       uint64
	ExternalUserID  string
	ExternalUnionID string
	UserID          uint64
	DisplayName     string
	MetadataJSON    string
	ArchivedAt      *time.Time
}

type ChannelIdentity struct {
	ID              uint64    `json:"id"`
	TenantID        uint64    `json:"tenant_id"`
	AccountID       uint64    `json:"account_id"`
	ExternalUserID  string    `json:"external_user_id"`
	ExternalUnionID string    `json:"external_union_id,omitempty"`
	UserID          uint64    `json:"user_id,omitempty"`
	DisplayName     string    `json:"display_name,omitempty"`
	MetadataJSON    string    `json:"metadata_json,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ArchivedAt      time.Time `json:"archived_at,omitempty"`
}

type ChannelConversationInput struct {
	TenantID                  uint64
	AccountID                 uint64
	ExternalChatID            string
	ExternalThreadID          string
	ThreadIDSource            string
	ScopeKey                  string
	ScopeHash                 []byte
	ExternalUserID            string
	ChatType                  string
	SessionID                 uint64
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	WorkspaceRealpath         string
	PermissionMode            string
	Status                    string
	LastInboundAt             *time.Time
	LastOutboundAt            *time.Time
	MetadataJSON              string
	ArchivedAt                *time.Time
}

type ChannelConversation struct {
	ID                        uint64    `json:"id"`
	TenantID                  uint64    `json:"tenant_id"`
	AccountID                 uint64    `json:"account_id"`
	ExternalChatID            string    `json:"external_chat_id"`
	ExternalThreadID          string    `json:"external_thread_id"`
	ThreadIDSource            string    `json:"thread_id_source"`
	ScopeKey                  string    `json:"scope_key"`
	ScopeHash                 []byte    `json:"scope_hash"`
	ExternalUserID            string    `json:"external_user_id,omitempty"`
	ChatType                  string    `json:"chat_type"`
	SessionID                 uint64    `json:"session_id,omitempty"`
	RuntimeFingerprint        string    `json:"runtime_fingerprint"`
	RuntimeFingerprintVersion int       `json:"runtime_fingerprint_version"`
	WorkspaceRealpath         string    `json:"workspace_realpath,omitempty"`
	PermissionMode            string    `json:"permission_mode"`
	Status                    string    `json:"status"`
	LastInboundAt             time.Time `json:"last_inbound_at,omitempty"`
	LastOutboundAt            time.Time `json:"last_outbound_at,omitempty"`
	MetadataJSON              string    `json:"metadata_json,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
	ArchivedAt                time.Time `json:"archived_at,omitempty"`
}

type ChannelConversationControlsInput struct {
	TenantID          uint64
	AccountID         uint64
	ConversationID    uint64
	WorkspaceRealpath string
	PermissionMode    string
}

type ChannelSessionRotationInput struct {
	TenantID       uint64
	AccountID      uint64
	ConversationID uint64
	UserID         uint64
	InboxEventID   uint64
	Model          string
}

type ChannelInboxEventInput struct {
	TenantID          uint64
	AccountID         uint64
	ConversationID    uint64
	ProviderEventID   string
	ProviderMessageID string
	ScopeHash         []byte
	PayloadSHA256     []byte
	PayloadCiphertext []byte
	PayloadKeyVersion string
	PayloadRef        string
	Authorized        bool
	Status            string
	Attempts          uint
	AvailableAt       *time.Time
	LeaseOwner        string
	LeaseUntil        *time.Time
	AckedAt           *time.Time
	ReceivedAt        *time.Time
	ProcessedAt       *time.Time
	ErrorCode         string
	ErrorMessage      string
}

type ChannelInboxEvent struct {
	ID                uint64    `json:"id"`
	TenantID          uint64    `json:"tenant_id"`
	AccountID         uint64    `json:"account_id"`
	ConversationID    uint64    `json:"conversation_id,omitempty"`
	ProviderEventID   string    `json:"provider_event_id"`
	ProviderMessageID string    `json:"provider_message_id,omitempty"`
	ScopeHash         []byte    `json:"scope_hash,omitempty"`
	PayloadSHA256     []byte    `json:"payload_sha256,omitempty"`
	PayloadCiphertext []byte    `json:"-"`
	PayloadKeyVersion string    `json:"payload_key_version,omitempty"`
	PayloadRef        string    `json:"payload_ref,omitempty"`
	PayloadPurgedAt   time.Time `json:"payload_purged_at,omitempty"`
	Authorized        bool      `json:"authorized"`
	Status            string    `json:"status"`
	Attempts          uint      `json:"attempts"`
	AvailableAt       time.Time `json:"available_at"`
	LeaseOwner        string    `json:"lease_owner,omitempty"`
	LeaseUntil        time.Time `json:"lease_until,omitempty"`
	AckedAt           time.Time `json:"acked_at,omitempty"`
	ReceivedAt        time.Time `json:"received_at"`
	ProcessedAt       time.Time `json:"processed_at,omitempty"`
	ErrorCode         string    `json:"error_code,omitempty"`
	ErrorMessage      string    `json:"error_message,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ChannelMessageInput struct {
	TenantID       uint64
	AccountID      uint64
	ConversationID uint64
	RunID          string
	Direction      string
	Operation      string
	// Empty values are mapped to SQL NULL so unknown outbound receipts do not
	// collapse into one unique-key value.
	ExternalMessageID string
	InternalMessageID uint64
	IdempotencyKey    string
	ContentJSON       string
	RenderVersion     uint64
	LastRenderSHA256  []byte
	Status            string
	SentAt            *time.Time
}

type ChannelMessage struct {
	ID             uint64 `json:"id"`
	TenantID       uint64 `json:"tenant_id"`
	AccountID      uint64 `json:"account_id"`
	ConversationID uint64 `json:"conversation_id"`
	RunID          string `json:"run_id,omitempty"`
	Direction      string `json:"direction"`
	Operation      string `json:"operation"`
	// A NULL external id is represented as the zero string at the storage DTO boundary.
	ExternalMessageID string    `json:"external_message_id,omitempty"`
	InternalMessageID uint64    `json:"internal_message_id,omitempty"`
	IdempotencyKey    string    `json:"idempotency_key"`
	ContentJSON       string    `json:"content_json"`
	RenderVersion     uint64    `json:"render_version"`
	LastRenderSHA256  []byte    `json:"last_render_sha256,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	SentAt            time.Time `json:"sent_at,omitempty"`
}

type ChannelOutboxInput struct {
	TenantID         uint64
	AccountID        uint64
	ConversationID   uint64
	RunID            string
	MessageID        uint64
	Operation        string
	SequenceNo       uint
	ChunkCount       uint
	IdempotencyKey   string
	PayloadJSON      string
	RenderSHA256     []byte
	Status           string
	Attempts         uint
	NextAttemptAt    *time.Time
	RetryAfterMS     uint
	LeaseOwner       string
	LeaseUntil       *time.Time
	LastErrorCode    string
	LastErrorMessage string
	SentAt           *time.Time
}

type ChannelOutbox struct {
	ID               uint64    `json:"id"`
	TenantID         uint64    `json:"tenant_id"`
	AccountID        uint64    `json:"account_id"`
	ConversationID   uint64    `json:"conversation_id"`
	RunID            string    `json:"run_id,omitempty"`
	MessageID        uint64    `json:"message_id"`
	Operation        string    `json:"operation"`
	SequenceNo       uint      `json:"sequence_no"`
	ChunkCount       uint      `json:"chunk_count"`
	IdempotencyKey   string    `json:"idempotency_key"`
	PayloadJSON      string    `json:"payload_json"`
	RenderSHA256     []byte    `json:"render_sha256,omitempty"`
	Status           string    `json:"status"`
	Attempts         uint      `json:"attempts"`
	NextAttemptAt    time.Time `json:"next_attempt_at"`
	RetryAfterMS     uint      `json:"retry_after_ms,omitempty"`
	LeaseOwner       string    `json:"lease_owner,omitempty"`
	LeaseUntil       time.Time `json:"lease_until,omitempty"`
	LastErrorCode    string    `json:"last_error_code,omitempty"`
	LastErrorMessage string    `json:"last_error_message,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	SentAt           time.Time `json:"sent_at,omitempty"`
}

type ChannelRunInput struct {
	ID                        string
	TenantID                  uint64
	AccountID                 uint64
	ConversationID            uint64
	ScopeHash                 []byte
	SessionID                 uint64
	RuntimeTurnID             string
	WorkerID                  string
	Status                    string
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	HeartbeatAt               *time.Time
	CancelRequestedAt         *time.Time
	LastEventSeq              uint64
	StartedAt                 *time.Time
	FinishedAt                *time.Time
	ErrorCode                 string
	ErrorMessage              string
}

type ChannelReactionInput struct {
	TenantID          uint64
	AccountID         uint64
	ConversationID    uint64
	ProviderMessageID string
	DesiredEmoji      string
}

type ChannelReaction struct {
	ID                uint64    `json:"id"`
	TenantID          uint64    `json:"tenant_id"`
	AccountID         uint64    `json:"account_id"`
	ConversationID    uint64    `json:"conversation_id"`
	ProviderMessageID string    `json:"provider_message_id"`
	DesiredEmoji      string    `json:"desired_emoji"`
	CurrentEmoji      string    `json:"current_emoji,omitempty"`
	ReactionID        string    `json:"reaction_id,omitempty"`
	Status            string    `json:"status"`
	Attempts          uint      `json:"attempts"`
	NextAttemptAt     time.Time `json:"next_attempt_at"`
	LeaseOwner        string    `json:"lease_owner,omitempty"`
	LeaseUntil        time.Time `json:"lease_until,omitempty"`
	LastErrorCode     string    `json:"last_error_code,omitempty"`
	LastErrorMessage  string    `json:"last_error_message,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ChannelRun struct {
	ID                        string    `json:"id"`
	TenantID                  uint64    `json:"tenant_id"`
	AccountID                 uint64    `json:"account_id"`
	ConversationID            uint64    `json:"conversation_id"`
	ScopeHash                 []byte    `json:"scope_hash"`
	SessionID                 uint64    `json:"session_id,omitempty"`
	RuntimeTurnID             string    `json:"runtime_turn_id,omitempty"`
	WorkerID                  string    `json:"worker_id,omitempty"`
	Status                    string    `json:"status"`
	RuntimeFingerprint        string    `json:"runtime_fingerprint"`
	RuntimeFingerprintVersion int       `json:"runtime_fingerprint_version"`
	HeartbeatAt               time.Time `json:"heartbeat_at,omitempty"`
	CancelRequestedAt         time.Time `json:"cancel_requested_at,omitempty"`
	LastEventSeq              uint64    `json:"last_event_seq"`
	StartedAt                 time.Time `json:"started_at,omitempty"`
	FinishedAt                time.Time `json:"finished_at,omitempty"`
	ErrorCode                 string    `json:"error_code,omitempty"`
	ErrorMessage              string    `json:"error_message,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type ChannelInteractionInput struct {
	ID                         string
	TenantID                   uint64
	AccountID                  uint64
	ConversationID             uint64
	RunID                      string
	SessionID                  uint64
	Kind                       string
	Status                     string
	ExternalChatID             string
	ExternalThreadID           string
	ExternalUserID             string
	ScopeHash                  []byte
	AllowedUserID              uint64
	QuestionCiphertext         []byte
	ResumeCheckpointCiphertext []byte
	AnswerCiphertext           []byte
	NonceHash                  []byte
	RuntimeFingerprint         string
	RuntimeFingerprintVersion  int
	ExpiresAt                  time.Time
	AnsweredAt                 *time.Time
	ConsumedAt                 *time.Time
}

type ChannelInteraction struct {
	ID                         string    `json:"id"`
	TenantID                   uint64    `json:"tenant_id"`
	AccountID                  uint64    `json:"account_id"`
	ConversationID             uint64    `json:"conversation_id"`
	RunID                      string    `json:"run_id"`
	SessionID                  uint64    `json:"session_id,omitempty"`
	Kind                       string    `json:"kind"`
	Status                     string    `json:"status"`
	ExternalChatID             string    `json:"external_chat_id"`
	ExternalThreadID           string    `json:"external_thread_id,omitempty"`
	ExternalUserID             string    `json:"external_user_id"`
	ScopeHash                  []byte    `json:"scope_hash"`
	AllowedUserID              uint64    `json:"allowed_user_id"`
	QuestionCiphertext         []byte    `json:"question_ciphertext"`
	ResumeCheckpointCiphertext []byte    `json:"resume_checkpoint_ciphertext"`
	AnswerCiphertext           []byte    `json:"answer_ciphertext,omitempty"`
	NonceHash                  []byte    `json:"nonce_hash"`
	RuntimeFingerprint         string    `json:"runtime_fingerprint"`
	RuntimeFingerprintVersion  int       `json:"runtime_fingerprint_version"`
	ExpiresAt                  time.Time `json:"expires_at"`
	AnsweredAt                 time.Time `json:"answered_at,omitempty"`
	ConsumedAt                 time.Time `json:"consumed_at,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

type ChannelRunInputEvent struct {
	TenantID     uint64
	RunID        string
	InboxEventID uint64
	SequenceNo   uint
}

type ChannelRunInputEventRecord struct {
	ID           uint64    `json:"id"`
	TenantID     uint64    `json:"tenant_id"`
	RunID        string    `json:"run_id"`
	InboxEventID uint64    `json:"inbox_event_id"`
	SequenceNo   uint      `json:"sequence_no"`
	CreatedAt    time.Time `json:"created_at"`
}

type ChannelCallbackInput struct {
	TenantID                  uint64
	AccountID                 uint64
	ConversationID            uint64
	RunID                     string
	PlatformMessageID         string
	ActionID                  string
	NonceHash                 []byte
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	AllowedUserID             uint64
	Status                    string
	ExpiresAt                 time.Time
}

type ChannelCallback struct {
	ID                        uint64    `json:"id"`
	TenantID                  uint64    `json:"tenant_id"`
	AccountID                 uint64    `json:"account_id"`
	ConversationID            uint64    `json:"conversation_id"`
	RunID                     string    `json:"run_id"`
	PlatformMessageID         string    `json:"platform_message_id"`
	ActionID                  string    `json:"action_id"`
	NonceHash                 []byte    `json:"nonce_hash"`
	RuntimeFingerprint        string    `json:"runtime_fingerprint"`
	RuntimeFingerprintVersion int       `json:"runtime_fingerprint_version"`
	AllowedUserID             uint64    `json:"allowed_user_id,omitempty"`
	Status                    string    `json:"status"`
	ExpiresAt                 time.Time `json:"expires_at"`
	ConsumedAt                time.Time `json:"consumed_at,omitempty"`
	CreatedAt                 time.Time `json:"created_at"`
}

type QuotaConfigInput = quota.ConfigInput
type QuotaConfig = quota.Config
type UsageLedgerInput = quota.LedgerInput
type UsageLedger = quota.Ledger
type UsageDailyDelta = quota.UsageDailyDelta
type UsageDaily = quota.UsageDaily
type QuotaEvent = quota.Event

type ListOptions struct {
	Limit  int
	Cursor uint64
	Search string
}
