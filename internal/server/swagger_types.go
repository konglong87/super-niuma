package server

import (
	"encoding/json"
	"time"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/promptdump"
	"github.com/konglong87/super-niuma/internal/prompttemplate"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/skills"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

type SwaggerError struct {
	Error string `json:"error"`
}

type SwaggerHealthResponse struct {
	OK        bool   `json:"ok"`
	Workspace string `json:"workspace"`
}

type SwaggerLivenessResponse struct {
	Status string `json:"status" example:"ok"`
}

type SwaggerReadinessResponse struct {
	Status string `json:"status" example:"ok"`
	// Checks 是依赖名到 "ok"/"unavailable" 的映射，键取决于服务端配置了哪些依赖。
	Checks map[string]string `json:"checks"`
}

type SwaggerIDResponse struct {
	ID uint64 `json:"id"`
}

type SwaggerPromptTemplateRequest = promptTemplateRequest
type SwaggerPromptTemplate = prompttemplate.PromptTemplate

type SwaggerSkillRollbackRequest = tenantservice.SkillRollbackRequest
type SwaggerSkillRollbackResponse = tenantservice.SkillRollbackResult

type SwaggerArchiveResponse struct {
	ID       uint64 `json:"id"`
	Archived bool   `json:"archived"`
}

type SwaggerSessionIDResponse struct {
	ID         uint64 `json:"id"`
	SessionKey string `json:"session_key"`
}

type SwaggerSessionControlError struct {
	Error string `json:"error" example:"session is not available in this state"`
	Code  string `json:"code" example:"invalid_state"`
}

type SwaggerSessionControlCreateRequest struct {
	PermissionMode string `json:"permission_mode,omitempty" example:"ask"`
	Effort         string `json:"effort,omitempty" example:"high"`
	PromptMode     string `json:"prompt_mode,omitempty" enums:"code,chat"`
	Provider       string `json:"provider,omitempty"`
	SessionKey     string `json:"session_key,omitempty" example:"support-case-42"`
	Title          string `json:"title,omitempty" example:"Support case 42"`
	Model          string `json:"model,omitempty" example:"claude-sonnet-4-5"`
	CWD            string `json:"cwd,omitempty" example:"/workspace/project"`
	InitialText    string `json:"initial_text,omitempty" example:"Start by reviewing the rollout plan."`
}

type SwaggerSessionControlSendRequest struct {
	Model          string                            `json:"model,omitempty" example:"claude-sonnet-4-5"`
	PermissionMode string                            `json:"permission_mode,omitempty" example:"ask"`
	Effort         string                            `json:"effort,omitempty" example:"high"`
	PromptMode     string                            `json:"prompt_mode,omitempty" enums:"code,chat"`
	Provider       string                            `json:"provider,omitempty"`
	Content        string                            `json:"content" example:"Continue with the verified plan."`
	SourceRefs     []string                          `json:"source_refs,omitempty" example:"tenant:research"`
	Attachments    []SwaggerSessionControlAttachment `json:"attachments,omitempty"`
}

type SwaggerSessionControlAttachment struct {
	InlineData   string `json:"inline_data,omitempty" example:"iVBORw0KGgo..."`
	AttachmentID string `json:"attachment_id,omitempty" example:"att_123"`
	Type         string `json:"type" example:"image"`
	MediaType    string `json:"media_type,omitempty" example:"image/png"`
	Name         string `json:"name,omitempty" example:"screenshot.png"`
	URL          string `json:"url,omitempty" example:"https://cdn.example.test/screenshot.png"`
	SizeBytes    int64  `json:"size_bytes,omitempty" example:"1024"`
	SHA256       string `json:"sha256,omitempty" example:"abc123"`
}

type SwaggerSessionControlStopRequest struct{}

type SwaggerSessionControlAttachRequest struct {
	TargetTaskID              uint64   `json:"target_task_id" example:"41"`
	TargetContextWindowTokens int      `json:"target_context_window_tokens" example:"16384"`
	Sources                   []string `json:"sources" example:"tenant:research,local:review"`
	RelationType              string   `json:"relation_type" example:"handoff"`
}

type SwaggerSessionControlMonitorRequest struct {
	Sources         []string `json:"sources" example:"tenant:research" validate:"required,min=1" minItems:"1"`
	IntervalSeconds int      `json:"interval_seconds" example:"300" validate:"required,min=10,max=2592000" minimum:"10" maximum:"2592000"`
	Channel         string   `json:"channel" example:"feishu" validate:"required,oneof=feishu" enums:"feishu"`
}

type SwaggerSessionControlLink struct {
	ID           uint64    `json:"id"`
	Target       string    `json:"target" example:"tenant:target"`
	Source       string    `json:"source" example:"local:source"`
	RelationType string    `json:"relation_type" example:"handoff"`
	CreatedAt    time.Time `json:"created_at"`
}

type SwaggerSessionControlSession struct {
	PermissionMode string                       `json:"permission_mode,omitempty"`
	Effort         string                       `json:"effort,omitempty"`
	PromptMode     string                       `json:"prompt_mode,omitempty" enums:"code,chat"`
	ID             uint64                       `json:"id,omitempty"`
	Provider       string                       `json:"provider,omitempty"`
	Ref            string                       `json:"ref" example:"tenant:support-case-42"`
	Source         sessioncontrol.Source        `json:"source" example:"tenant" enums:"tenant,local"`
	Title          string                       `json:"title"`
	Status         sessioncontrol.SessionStatus `json:"status" example:"running" enums:"idle,queued,running,waiting_permission,waiting_input,blocked,completed,failed,stopped,archived"`
	UpdatedAt      time.Time                    `json:"updated_at"`
	ShortID        string                       `json:"short_id" example:"support-case"`
	Model          string                       `json:"model,omitempty"`
	CWD            string                       `json:"cwd,omitempty"`
	ActiveRunID    uint64                       `json:"active_run_id,omitempty"`
	ReadOnly       bool                         `json:"read_only,omitempty"`
	Links          []SwaggerSessionControlLink  `json:"links,omitempty"`
}

type SwaggerSessionConversationResponse struct {
	Data sessionConversationPage `json:"data"`
}
type SwaggerSessionConversationSubscribe = conversationSubscribeRequest

type SwaggerSessionControlHandoffSource struct {
	Source              string                                   `json:"source"`
	Success             bool                                     `json:"success"`
	ErrorCode           string                                   `json:"error_code,omitempty"`
	PackageID           string                                   `json:"package_id,omitempty"`
	PackageSHA256Prefix string                                   `json:"package_sha256_prefix,omitempty"`
	CursorPrefix        string                                   `json:"cursor_prefix,omitempty"`
	EstimatedTokens     int                                      `json:"estimated_tokens,omitempty"`
	LinkID              uint64                                   `json:"link_id,omitempty"`
	EventID             uint64                                   `json:"event_id,omitempty"`
	CandidateRemovals   []sessioncontrol.HandoffCandidateRemoval `json:"candidate_removals,omitempty"`
}

type SwaggerSessionControlHandoff struct {
	SourceResults   []SwaggerSessionControlHandoffSource `json:"source_results,omitempty"`
	EstimatedTokens int                                  `json:"estimated_tokens,omitempty"`
	TargetTaskID    uint64                               `json:"target_task_id,omitempty"`
	Stale           bool                                 `json:"stale"`
	Replayed        bool                                 `json:"replayed"`
}

type SwaggerSessionControlOperation struct {
	OperationID string                        `json:"operation_id"`
	Replayed    bool                          `json:"replayed"`
	Session     SwaggerSessionControlSession  `json:"session"`
	RunID       uint64                        `json:"run_id,omitempty"`
	ScheduleID  string                        `json:"schedule_id,omitempty"`
	LinkIDs     []uint64                      `json:"link_ids,omitempty"`
	Handoff     *SwaggerSessionControlHandoff `json:"handoff,omitempty"`
	AuditID     uint64                        `json:"audit_id,omitempty"`
	ErrorCode   string                        `json:"error_code,omitempty"`
}

type SwaggerSessionControlSessionListResponse struct {
	Data []SwaggerSessionControlSession `json:"data"`
}

type SwaggerSessionControlSessionResponse struct {
	Data SwaggerSessionControlSession `json:"data"`
}

type SwaggerSessionControlOperationResponse struct {
	Data SwaggerSessionControlOperation `json:"data"`
}

type SwaggerSessionControlStateEvent struct {
	SchemaVersion string                       `json:"schema_version" example:"niuma.session-control-state.v1"`
	Cursor        string                       `json:"cursor" example:"81"`
	SessionRef    string                       `json:"session_ref" example:"tenant:support-case-42"`
	OperationID   string                       `json:"operation_id,omitempty"`
	RunID         uint64                       `json:"run_id,omitempty"`
	Status        sessioncontrol.SessionStatus `json:"status" example:"running"`
	UpdatedAt     time.Time                    `json:"updated_at"`
}

type SwaggerRuntimeBackgroundListResponse struct {
	Data []RuntimeBackgroundJob `json:"data"`
}

type SwaggerPromptDumpRecordsResponse struct {
	Path        string                            `json:"path"`
	Exists      bool                              `json:"exists"`
	SizeBytes   int64                             `json:"size_bytes,omitempty"`
	GeneratedAt time.Time                         `json:"generated_at"`
	Filter      SwaggerPromptDumpFilter           `json:"filter"`
	Sessions    []SwaggerPromptDumpSessionSummary `json:"sessions"`
	Records     []SwaggerPromptDumpRecordSummary  `json:"records"`
	Warning     string                            `json:"warning,omitempty"`
}

type SwaggerPromptDumpFilter struct {
	SessionID      string `json:"session_id,omitempty"`
	Limit          int    `json:"limit"`
	IncludeRequest bool   `json:"include_request"`
}

type SwaggerPromptDumpSessionSummary struct {
	SessionID           string                             `json:"session_id"`
	Records             int                                `json:"records"`
	FirstTimestamp      string                             `json:"first_timestamp,omitempty"`
	LastTimestamp       string                             `json:"last_timestamp,omitempty"`
	MaxTurn             int                                `json:"max_turn,omitempty"`
	Models              []string                           `json:"models,omitempty"`
	Scopes              []string                           `json:"scopes,omitempty"`
	PromptModes         []string                           `json:"prompt_modes,omitempty"`
	SystemHashes        []string                           `json:"system_hashes,omitempty"`
	SystemBytesMax      int                                `json:"system_bytes_max,omitempty"`
	ToolCountMax        int                                `json:"tool_count_max,omitempty"`
	CacheControlBlocks  int                                `json:"cache_control_blocks,omitempty"`
	RawRequestRecords   int                                `json:"raw_request_records,omitempty"`
	TraceUsageAvailable bool                               `json:"trace_usage_available"`
	TraceSummary        traceSummary                       `json:"trace_summary,omitempty"`
	CacheDiagnostics    promptdump.CacheSessionDiagnostics `json:"cache_diagnostics"`
}

type SwaggerPromptDumpRecordSummary struct {
	LineNumber         int                               `json:"line_number"`
	Timestamp          string                            `json:"timestamp,omitempty"`
	SessionID          string                            `json:"session_id,omitempty"`
	Turn               int                               `json:"turn"`
	Scope              string                            `json:"scope,omitempty"`
	PromptMode         string                            `json:"prompt_mode,omitempty"`
	Model              string                            `json:"model"`
	MaxTokens          int                               `json:"max_tokens"`
	SystemBytes        int                               `json:"system_bytes"`
	SystemHash         string                            `json:"system_hash,omitempty"`
	SystemBlockCount   int                               `json:"system_block_count"`
	MessageCount       int                               `json:"message_count"`
	ToolCount          int                               `json:"tool_count"`
	CacheControlBlocks int                               `json:"cache_control_blocks"`
	CacheControlBytes  int                               `json:"cache_control_bytes"`
	RequestRedaction   promptdump.RequestRedaction       `json:"request_redaction"`
	SystemBlocks       []promptdump.SystemBlockSummary   `json:"system_blocks_summary,omitempty"`
	MessagesSummary    []promptdump.MessageSummary       `json:"messages_summary,omitempty"`
	ToolsSummary       []promptdump.ToolSummary          `json:"tools_summary,omitempty"`
	ContextManifest    json.RawMessage                   `json:"context_manifest,omitempty"`
	CacheDiagnostics   promptdump.CacheRecordDiagnostics `json:"cache_diagnostics"`
	Request            json.RawMessage                   `json:"request,omitempty"`
}

type SwaggerRuntimeLoopRequest = RuntimeLoopRequest
type SwaggerRuntimeBackgroundLogsResponse = RuntimeBackgroundLogsResponse
type SwaggerRuntimeBackgroundStopResponse = RuntimeBackgroundStopResponse
type SwaggerRuntimeBackgroundEventsResponse = RuntimeBackgroundEventsResponse
type SwaggerRuntimeBackgroundRunsResponse = RuntimeBackgroundRunsResponse
type SwaggerSchedulerEvent = scheduler.Event
type SwaggerSchedulerRunRecord = scheduler.RunRecord
type SwaggerBackgroundJob = background.Job

type SwaggerMobileMessageListResponse struct {
	Data       []mysqlstore.Message `json:"data"`
	NextCursor string               `json:"next_cursor"`
	HasMore    bool                 `json:"has_more"`
}

type SwaggerMobileCancelResponse struct {
	ID     uint64 `json:"id"`
	Status string `json:"status"`
	Active bool   `json:"active"`
}

type SwaggerMobileBranchResponse struct {
	ID              uint64 `json:"id"`
	SessionKey      string `json:"session_key"`
	CopiedMessages  int    `json:"copied_messages"`
	ParentSessionID uint64 `json:"parent_session_id"`
	UntilTurn       uint   `json:"until_turn"`
}

type SwaggerMobileLatestRecap struct {
	MessageID uint64    `json:"message_id,omitempty"`
	Content   string    `json:"content"`
	Model     string    `json:"model,omitempty"`
	TurnIndex uint      `json:"turn_index,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type SwaggerMobileSessionDetailResponse struct {
	mysqlstore.Session
	LatestRecap *SwaggerMobileLatestRecap `json:"latest_recap,omitempty"`
}

type SwaggerMobileAttachmentPresignRequest struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type SwaggerMobileAttachmentPresignResponse struct {
	AttachmentID string                  `json:"attachment_id"`
	ObjectKey    string                  `json:"object_key"`
	UploadURL    string                  `json:"upload_url"`
	ExpiresAt    string                  `json:"expires_at"`
	MaxSizeBytes int64                   `json:"max_size_bytes"`
	Headers      map[string]string       `json:"headers"`
	Attachment   SwaggerMobileAttachment `json:"attachment"`
}

type SwaggerMobileAttachment struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Transcript   string `json:"transcript,omitempty"`
}

type SwaggerMobileSessionRequest struct {
	SessionKey   string `json:"session_key,omitempty"`
	Title        string `json:"title,omitempty"`
	Status       string `json:"status,omitempty"`
	Model        string `json:"model,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

type SwaggerMobileMessageStreamRequest struct {
	Content     string                    `json:"content"`
	Model       string                    `json:"model,omitempty"`
	MessageKey  string                    `json:"message_key,omitempty"`
	Attachments []SwaggerMobileAttachment `json:"attachments,omitempty"`
}

type SwaggerMobileRegenerateRequest struct {
	Model      string `json:"model,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
}

type SwaggerMobileBranchRequest struct {
	UntilTurn      uint   `json:"until_turn,omitempty"`
	UntilMessageID uint64 `json:"until_message_id,omitempty"`
	Title          string `json:"title,omitempty"`
	SessionKey     string `json:"session_key,omitempty"`
}

type SwaggerListSessionsResponse struct {
	Data []mysqlstore.Session `json:"data"`
}

type SwaggerWebAgentConversation struct {
	ID            string                 `json:"id"`
	SessionID     uint64                 `json:"session_id,omitempty"`
	SessionKey    string                 `json:"session_key,omitempty"`
	Title         string                 `json:"title"`
	CWD           string                 `json:"cwd,omitempty"`
	WorkspaceName string                 `json:"workspace_name,omitempty"`
	Status        string                 `json:"status"`
	UpdatedAt     string                 `json:"updated_at,omitempty"`
	Session       *mysqlstore.Session    `json:"session,omitempty"`
	LatestTask    mysqlstore.AgentTask   `json:"latest_task"`
	Tasks         []mysqlstore.AgentTask `json:"tasks"`
}

type SwaggerListWebAgentConversationsResponse struct {
	Data []SwaggerWebAgentConversation `json:"data"`
}

type SwaggerAgentSlashCommand struct {
	Name        string `json:"name" example:"review"`
	Description string `json:"description,omitempty" example:"Run a code review prompt"`
	Source      string `json:"source,omitempty" example:"builtin"`
}

type SwaggerListAgentSlashCommandsResponse struct {
	Data []SwaggerAgentSlashCommand `json:"data"`
}

type SwaggerWebAgentConversationDetail struct {
	SwaggerWebAgentConversation
	Events   []mysqlstore.AgentTaskEvent      `json:"events"`
	Messages []mysqlstore.Message             `json:"messages,omitempty"`
	Usage    SwaggerWebAgentConversationUsage `json:"usage"`
}

type SwaggerWebAgentConversationUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	TotalTokens     int `json:"total_tokens"`
	ContextLength   int `json:"context_length"`
	ContextPercent  int `json:"context_percent"`
	ToolCalls       int `json:"tool_calls"`
	CompletedRuns   int `json:"completed_runs"`
	FailedRuns      int `json:"failed_runs"`
	CancelledRuns   int `json:"cancelled_runs"`
	TimeoutRuns     int `json:"timeout_runs"`
	RunningRuns     int `json:"running_runs"`
	TotalRuns       int `json:"total_runs"`
	TotalDurationMS int `json:"total_duration_ms"`
}

type SwaggerListUsersResponse struct {
	Data       []mysqlstore.User `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type SwaggerListTenantsResponse struct {
	Data       []mysqlstore.Tenant `json:"data"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

type SwaggerListMemoriesResponse struct {
	Data []mysqlstore.Memory `json:"data"`
}

type SwaggerAutoMemoryReviewRequest = tenantservice.AutoMemoryReviewRequest
type SwaggerMemoryReviewRequest = tenantservice.MemoryReviewRequest

type SwaggerListSkillsResponse struct {
	Data []mysqlstore.Skill `json:"data"`
}

type SwaggerListLocalSkillsResponse struct {
	Workspace string         `json:"workspace"`
	Data      []skills.Skill `json:"data"`
}

type SwaggerListSkillOverridesResponse struct {
	Data []mysqlstore.SkillOverride `json:"data"`
}

type SwaggerListEffectiveSkillsResponse struct {
	Data []mysqlstore.EffectiveSkill `json:"data"`
}

type SwaggerListDocumentsResponse struct {
	Data []mysqlstore.Document `json:"data"`
}

type SwaggerListKnowledgeDocumentsResponse struct {
	Data []mysqlstore.KnowledgeDocument `json:"data"`
}

type SwaggerKnowledgeSearchResponse struct {
	Data []mysqlstore.KnowledgeChunk `json:"data"`
}

type SwaggerListMessagesResponse struct {
	Data []mysqlstore.Message `json:"data"`
}

type SwaggerListAgentTasksResponse struct {
	Data []mysqlstore.AgentTask `json:"data"`
}

type SwaggerAgentWorkspaceValidateRequest struct {
	CWD string `json:"cwd" example:"/Users/example/GolandProjects/niuma"`
}

type SwaggerAgentWorkspaceValidateResponse struct {
	CWD           string `json:"cwd"`
	WorkspaceName string `json:"workspace_name"`
	Exists        bool   `json:"exists"`
	IsDir         bool   `json:"is_dir"`
	GitRoot       string `json:"git_root,omitempty"`
	IsGitRepo     bool   `json:"is_git_repo"`
}

type SwaggerCreateAgentTaskRequest struct {
	ParentSessionID    uint64          `json:"parent_session_id,omitempty"`
	SubagentSessionKey string          `json:"subagent_session_key,omitempty"`
	AgentName          string          `json:"agent_name,omitempty"`
	Description        string          `json:"description,omitempty"`
	Prompt             string          `json:"prompt,omitempty"`
	Status             string          `json:"status,omitempty" example:"running"`
	Model              string          `json:"model,omitempty"`
	ResultJSON         json.RawMessage `json:"result_json,omitempty"`
	MetadataJSON       json.RawMessage `json:"metadata_json,omitempty"`
	TraceID            string          `json:"trace_id,omitempty"`
}

type SwaggerCreateAgentTaskResponse struct {
	ID uint64 `json:"id"`
}

type SwaggerAgentTaskResponse = mysqlstore.AgentTask

type SwaggerUpdateAgentTaskRequest struct {
	Status       string          `json:"status,omitempty" example:"completed"`
	ResultJSON   json.RawMessage `json:"result_json,omitempty"`
	MetadataJSON json.RawMessage `json:"metadata_json,omitempty"`
}

type SwaggerUpdateAgentTaskResponse struct {
	ID      uint64 `json:"id"`
	Updated bool   `json:"updated"`
}

type SwaggerAgentTaskMessageRequest struct {
	FromAgent   string                       `json:"from_agent,omitempty"`
	Content     string                       `json:"content"`
	TraceID     string                       `json:"trace_id,omitempty"`
	Attachments []SwaggerAgentTaskAttachment `json:"attachments,omitempty"`
}

type SwaggerAgentTaskAttachment struct {
	AttachmentID string `json:"attachment_id,omitempty" example:"att_123"`
	Type         string `json:"type" example:"image"`
	MediaType    string `json:"media_type" example:"image/png"`
	Name         string `json:"name,omitempty" example:"screenshot.png"`
	URL          string `json:"url,omitempty" example:"https://cdn.example.test/screenshot.png"`
	SizeBytes    int64  `json:"size_bytes" example:"1024"`
	SHA256       string `json:"sha256,omitempty" example:"abc123"`
	InlineData   string `json:"inline_data,omitempty" example:"iVBORw0KGgo..."`
}

type SwaggerAgentTaskMessageResponse struct {
	ID     uint64 `json:"id"`
	TaskID uint64 `json:"task_id"`
	Status string `json:"status" example:"running"`
}

type SwaggerPendingInput = pendinginput.PendingInput

type SwaggerPendingInputRequest struct {
	ClientInputID string                       `json:"client_input_id" example:"web-123"`
	Content       string                       `json:"content"`
	Direction     string                       `json:"direction,omitempty"`
	Attachments   []SwaggerAgentTaskAttachment `json:"attachments,omitempty"`
}

type SwaggerPendingInputPatchRequest struct {
	Content   *string `json:"content,omitempty"`
	Direction *string `json:"direction,omitempty"`
}

type SwaggerPendingInputSettingsRequest struct {
	Enabled bool `json:"enabled"`
}

type SwaggerPendingInputListResponse struct {
	Data []pendinginput.PendingInput `json:"data"`
}

type SwaggerPendingInputResponse struct {
	Data                 *pendinginput.PendingInput `json:"data,omitempty"`
	SessionID            uint64                     `json:"session_id,omitempty"`
	TaskID               uint64                     `json:"task_id,omitempty"`
	SourcePendingInputID string                     `json:"source_pending_input_id,omitempty"`
}

type SwaggerAgentTaskPermissionRequest struct {
	Allowed     bool   `json:"allowed"`
	Reason      string `json:"reason,omitempty"`
	Destination string `json:"destination,omitempty"`
	Rule        string `json:"rule,omitempty"`
}

type SwaggerAgentTaskQuestionAnswerRequest struct {
	Answer string `json:"answer" example:"Architecture diagram"`
}

type SwaggerAgentTaskQuestionAnswerResponse struct {
	ID        uint64 `json:"id" example:"44"`
	RequestID string `json:"request_id" example:"question-uuid"`
	Status    string `json:"status" example:"answered"`
	Answer    string `json:"answer" example:"Architecture diagram"`
}

type SwaggerAgentTaskPermissionResponse struct {
	ID        uint64 `json:"id"`
	RequestID string `json:"request_id"`
	Allowed   bool   `json:"allowed"`
}

type SwaggerListAgentTaskEventsResponse struct {
	Data []mysqlstore.AgentTaskEvent `json:"data"`
}

type SwaggerListGoalsResponse struct {
	Data []goal.Goal `json:"data"`
}

type SwaggerCreateGoalRequest struct {
	Objective   string `json:"objective"`
	SessionID   string `json:"session_id,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Model       string `json:"model,omitempty"`
	TurnBudget  int    `json:"turn_budget,omitempty"`
	TokenBudget int    `json:"token_budget,omitempty"`
}

type SwaggerGoalResponse = goal.Goal

type SwaggerUpdateGoalRequest struct {
	Objective            *string      `json:"objective,omitempty"`
	Status               *goal.Status `json:"status,omitempty" example:"blocked"`
	SessionID            *string      `json:"session_id,omitempty"`
	CWD                  *string      `json:"cwd,omitempty"`
	Model                *string      `json:"model,omitempty"`
	TurnBudget           *int         `json:"turn_budget,omitempty"`
	TokenBudget          *int         `json:"token_budget,omitempty"`
	TurnsUsed            *int         `json:"turns_used,omitempty"`
	InputTokens          *int         `json:"input_tokens,omitempty"`
	OutputTokens         *int         `json:"output_tokens,omitempty"`
	LastBlocker          *string      `json:"last_blocker,omitempty"`
	RepeatedBlockerCount *int         `json:"repeated_blocker_count,omitempty"`
	LastCheckpoint       *string      `json:"last_checkpoint,omitempty"`
	LastReason           *string      `json:"last_reason,omitempty"`
	LastNextAction       *string      `json:"last_next_action,omitempty"`
	Error                *string      `json:"error,omitempty"`
}

type SwaggerListGoalEventsResponse struct {
	Data []goal.Event `json:"data"`
}

type SwaggerGoalPlanResponse = goal.GoalPlan

type SwaggerListGoalEvidenceResponse struct {
	Data []goal.GoalEvidence `json:"data"`
}

type SwaggerRunGoalResponse = goal.RunResult

type SwaggerTenantSessionTimelineResponse struct {
	Session         mysqlstore.Session          `json:"session"`
	TraceIDs        []string                    `json:"trace_ids"`
	Messages        []mysqlstore.Message        `json:"messages"`
	AuditLogs       []mysqlstore.AuditLog       `json:"audit_logs"`
	TelemetryEvents []mysqlstore.TelemetryEvent `json:"telemetry_events"`
	AgentTasks      []mysqlstore.AgentTask      `json:"agent_tasks"`
	AgentTaskEvents []mysqlstore.AgentTaskEvent `json:"agent_task_events"`
	Timeline        []SwaggerTenantTimelineItem `json:"timeline"`
}

type SwaggerTenantTimelineItem struct {
	Type           string                     `json:"type"`
	Time           string                     `json:"time"`
	TraceID        string                     `json:"trace_id,omitempty"`
	Message        *mysqlstore.Message        `json:"message,omitempty"`
	AuditLog       *mysqlstore.AuditLog       `json:"audit_log,omitempty"`
	TelemetryEvent *mysqlstore.TelemetryEvent `json:"telemetry_event,omitempty"`
	AgentTask      *mysqlstore.AgentTask      `json:"agent_task,omitempty"`
	AgentTaskEvent *mysqlstore.AgentTaskEvent `json:"agent_task_event,omitempty"`
}

type SwaggerTraceSessionsResponse struct {
	Source string                `json:"source"`
	Data   []traceSessionSummary `json:"data"`
}

type SwaggerTraceDetailResponse = traceDetailResponse

type SwaggerCancelAgentTaskResponse struct {
	ID        uint64 `json:"id"`
	Cancelled bool   `json:"cancelled"`
	InProcess bool   `json:"in_process"`
}

type SwaggerListAuditLogsResponse struct {
	Data       []mysqlstore.AuditLog `json:"data"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

type SwaggerListTelemetryEventsResponse struct {
	Data       []mysqlstore.TelemetryEvent `json:"data"`
	NextCursor string                      `json:"next_cursor,omitempty"`
	HasMore    bool                        `json:"has_more"`
}

type SwaggerTenantQuotaConfig = mysqlstore.QuotaConfig
type SwaggerTenantQuotaConfigRequest = tenantservice.QuotaConfigRequest

type SwaggerListTenantUsageDailyResponse struct {
	Data       []mysqlstore.UsageDaily `json:"data"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

type SwaggerListTenantUsageLedgerResponse struct {
	Data       []mysqlstore.UsageLedger `json:"data"`
	NextCursor string                   `json:"next_cursor,omitempty"`
	HasMore    bool                     `json:"has_more"`
}

type SwaggerListTenantQuotaEventsResponse struct {
	Data       []mysqlstore.QuotaEvent `json:"data"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

type SwaggerTelemetryEventRequest struct {
	SchemaVersion string         `json:"schema_version,omitempty"`
	Name          string         `json:"name"`
	Category      string         `json:"category,omitempty"`
	Source        string         `json:"source,omitempty"`
	Status        string         `json:"status,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	TurnIndex     int            `json:"turn_index,omitempty"`
	StartedAt     time.Time      `json:"started_at,omitempty"`
	SessionID     uint64         `json:"session_id,omitempty"`
	ResourceType  string         `json:"resource_type,omitempty"`
	ResourceID    string         `json:"resource_id,omitempty"`
	Model         string         `json:"model,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	InputTokens   int            `json:"input_tokens,omitempty"`
	OutputTokens  int            `json:"output_tokens,omitempty"`
	Error         string         `json:"error,omitempty"`
	Properties    map[string]any `json:"properties,omitempty"`
}

type SwaggerOpenAIModelsResponse struct {
	Object string               `json:"object"`
	Data   []SwaggerOpenAIModel `json:"data"`
}

// SwaggerProviderListResponse documents GET /v1/providers.
type SwaggerProviderListResponse struct {
	Object string                  `json:"object"`
	Data   []SwaggerProviderOption `json:"data"`
}

type SwaggerProviderOption struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

type SwaggerOpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type SwaggerOpenAIChatResponse struct {
	ID            string                        `json:"id"`
	Object        string                        `json:"object"`
	Created       int64                         `json:"created"`
	Model         string                        `json:"model"`
	Choices       []SwaggerOpenAIChoice         `json:"choices"`
	Usage         SwaggerOpenAIUsage            `json:"usage,omitempty"`
	TenantRuntime *SwaggerTenantRuntimeMetadata `json:"tenant_runtime,omitempty"`
}

type SwaggerOpenAIChoice struct {
	Index        int                  `json:"index"`
	Message      SwaggerOpenAIMessage `json:"message"`
	FinishReason string               `json:"finish_reason"`
}

type SwaggerOpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type SwaggerOpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type SwaggerTenantRuntimeMetadata struct {
	Active         bool     `json:"active"`
	Resolved       bool     `json:"resolved,omitempty"`
	Source         string   `json:"source,omitempty"`
	SkillKeys      []string `json:"skill_keys,omitempty"`
	LoadedKeys     []string `json:"loaded_keys,omitempty"`
	Versions       []string `json:"versions,omitempty"`
	PackageSHA256  []string `json:"package_sha256,omitempty"`
	PackageRefs    []string `json:"package_refs,omitempty"`
	RuntimeRefs    []string `json:"runtime_refs,omitempty"`
	Bytes          int      `json:"bytes,omitempty"`
	TenantAddendum bool     `json:"tenant_addendum,omitempty"`
}

type SwaggerQueryResult struct {
	Response       string            `json:"response"`
	Model          string            `json:"model"`
	StopReason     string            `json:"stop_reason,omitempty"`
	Turns          int               `json:"turns"`
	SessionID      string            `json:"session_id,omitempty"`
	TranscriptPath string            `json:"transcript_path,omitempty"`
	Usage          query.Usage       `json:"usage,omitempty"`
	ToolCalls      []query.ToolTrace `json:"tool_calls,omitempty"`
}

type SwaggerSnapshotResponse struct {
	Data map[string]interface{} `json:"data,omitempty"`
}

type SwaggerTenantUser = mysqlstore.User
type SwaggerTenantProfile = mysqlstore.Profile
type SwaggerTenantSession = mysqlstore.Session
type SwaggerTenantContext = tenantservice.Context
type SwaggerTenantRequest = tenantservice.TenantRequest
type SwaggerTenantUserRequest = tenantservice.UserRequest
type SwaggerTenantMemoryRequest = tenantservice.MemoryRequest
type SwaggerTenantSkillRequest = tenantservice.SkillRequest
type SwaggerTenantSkillPackageRequest = tenantservice.SkillPackageRequest
type SwaggerTenantSkillPackageResult = tenantservice.SkillPackageResult
type SwaggerSkillPackageVerifyRequest = SkillPackageVerifyRequest
type SwaggerSkillPackageVerifyResponse = SkillPackageVerifyResponse
type SwaggerTenantSkillOverrideRequest = tenantservice.SkillOverrideRequest
type SwaggerTenantDocumentRequest = tenantservice.DocumentRequest
type SwaggerTenantKnowledgeDocumentRequest = tenantservice.KnowledgeDocumentRequest
type SwaggerTenantKnowledgeSearchRequest = tenantservice.KnowledgeSearchRequest
type SwaggerTenantProfileRequest = tenantservice.ProfileRequest
type SwaggerAgentProfile = mysqlstore.AgentProfile

type SwaggerAgentProfileConversationCatalog = mysqlstore.AgentProfileConversationCatalog
type SwaggerAgentProfileConversationSummary = mysqlstore.AgentProfileConversationSummary
type SwaggerAgentProfileTeamLink = mysqlstore.AgentProfileTeamLink
type SwaggerAgentProfileAssignment = mysqlstore.AgentProfileAssignment
type SwaggerAgentProfileChannelBinding = mysqlstore.AgentProfileChannelBinding
type SwaggerEffectiveAgentProfile = agentprofile.EffectiveProfile
type SwaggerAgentTeam = mysqlstore.AgentTeam
type SwaggerAgentTeamMember = mysqlstore.AgentTeamMember
type SwaggerAgentTeamBinding = mysqlstore.AgentTeamBinding
type SwaggerAgentTeamRun = mysqlstore.AgentTeamRun
type SwaggerChannelAccount = mysqlstore.ChannelAccount
type SwaggerTenantSessionRequest = tenantservice.SessionRequest
type SwaggerTenantMessageRequest = tenantservice.MessageRequest
type SwaggerTenantAuditRequest = tenantservice.AuditRequest
type SwaggerImageGenerateRequest = imageGenerateRequest
type SwaggerImageArtifact = imagegen.Artifact
type SwaggerImageArtifactResponse struct {
	Asset imagegen.Artifact `json:"asset"`
}
type SwaggerImageGenerationRecord = imagegen.GenerationRecord
type SwaggerImageGenerationListResponse struct {
	Data []imagegen.GenerationRecord `json:"data"`
}
type SwaggerImageCapabilityEntry struct {
	Provider      string                      `json:"provider"`
	Model         string                      `json:"model"`
	Label         string                      `json:"label,omitempty"`
	ImageProtocol string                      `json:"image_protocol,omitempty"`
	Capability    config.ImageModelCapability `json:"capability"`
}
type SwaggerImageCapabilitiesResponse struct {
	Data []SwaggerImageCapabilityEntry `json:"data"`
}

type SwaggerSSEEvent struct {
	Type       string `json:"type"`
	SessionID  uint64 `json:"session_id,omitempty"`
	MessageID  uint64 `json:"message_id,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
	Status     string `json:"status,omitempty"`
	Delta      string `json:"delta,omitempty"`
	Error      string `json:"error,omitempty"`
}

type SwaggerTimeResponse struct {
	Time time.Time `json:"time"`
}
