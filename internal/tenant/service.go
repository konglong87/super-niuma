package tenant

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/quota"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/tenantpkg"
)

var (
	ErrMissingTenantKey = errors.New("tenant key is required")
	ErrMissingUserID    = errors.New("user id is required")
	ErrForbidden        = errors.New("tenant access forbidden")
	ErrInvalidRequest   = errors.New("tenant request invalid")
)

type Repository interface {
	GetTenantByKey(ctx context.Context, tenantKey string) (mysqlstore.Tenant, error)
	UpsertTenant(ctx context.Context, input mysqlstore.TenantInput) (uint64, error)
	ArchiveTenant(ctx context.Context, tenantKey string) error
	ListTenants(ctx context.Context, limit int) ([]mysqlstore.Tenant, error)
	ListTenantsFiltered(ctx context.Context, opts mysqlstore.ListOptions) ([]mysqlstore.Tenant, error)
	UpsertUser(ctx context.Context, input mysqlstore.UserInput) (uint64, error)
	EnsureUser(ctx context.Context, tenantID uint64, userKey string) (uint64, error)
	ArchiveUser(ctx context.Context, tenantID uint64, userKey string) error
	GetUser(ctx context.Context, tenantID, userID uint64) (mysqlstore.User, error)
	ListUsers(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.User, error)
	ListUsersFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.User, error)
	UpsertMemory(ctx context.Context, input mysqlstore.MemoryInput) (uint64, error)
	ListMemories(ctx context.Context, tenantID, userID uint64, category string, limit int) ([]mysqlstore.Memory, error)
	UpsertSkill(ctx context.Context, input mysqlstore.SkillInput) (uint64, error)
	RollbackSkillVersion(ctx context.Context, input mysqlstore.SkillRollbackInput) (mysqlstore.Skill, error)
	ListSkills(ctx context.Context, tenantID uint64, enabledOnly bool, limit int) ([]mysqlstore.Skill, error)
	GetSkill(ctx context.Context, tenantID uint64, skillKey string, version uint) (mysqlstore.Skill, error)
	UpsertSkillOverride(ctx context.Context, input mysqlstore.SkillOverrideInput) (uint64, error)
	ListSkillOverrides(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.SkillOverride, error)
	GetSkillOverride(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (mysqlstore.SkillOverride, error)
	ListEffectiveSkills(ctx context.Context, tenantID, userID uint64, enabledOnly bool, limit int) ([]mysqlstore.EffectiveSkill, error)
	GetEffectiveSkill(ctx context.Context, tenantID, userID uint64, skillKey string, version uint) (mysqlstore.EffectiveSkill, error)
	SaveDocument(ctx context.Context, input mysqlstore.DocumentInput) (uint64, error)
	GetActiveDocument(ctx context.Context, tenantID, userID uint64, docType string) (mysqlstore.Document, error)
	ListDocuments(ctx context.Context, tenantID, userID uint64, docType string, limit int) ([]mysqlstore.Document, error)
	SaveKnowledgeDocument(ctx context.Context, input mysqlstore.KnowledgeDocumentInput, chunks []mysqlstore.KnowledgeChunkInput) (uint64, error)
	ListKnowledgeDocuments(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.KnowledgeDocument, error)
	SearchKnowledgeChunks(ctx context.Context, tenantID uint64, opts mysqlstore.KnowledgeSearchOptions) ([]mysqlstore.KnowledgeChunk, error)
	SaveProfile(ctx context.Context, input mysqlstore.ProfileInput) (uint64, error)
	GetProfile(ctx context.Context, tenantID, userID uint64, version uint) (mysqlstore.Profile, error)
	UpsertSession(ctx context.Context, input mysqlstore.SessionInput) (uint64, error)
	CreateSessionControlSession(ctx context.Context, input mysqlstore.SessionControlCreateInput) (mysqlstore.SessionControlCreateResult, error)
	GetSessionControlSessionByKey(ctx context.Context, tenantID, userID uint64, sessionKey string) (mysqlstore.SessionControlSession, error)
	ListSessionControlSessions(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.SessionControlSession, error)
	ListSessions(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.Session, error)
	GetSession(ctx context.Context, tenantID, userID, sessionID uint64) (mysqlstore.Session, error)
	GetSessionByKey(ctx context.Context, tenantID, userID uint64, sessionKey string) (mysqlstore.Session, error)
	UpdateSession(ctx context.Context, input mysqlstore.SessionInput) error
	ArchiveSession(ctx context.Context, tenantID, userID, sessionID uint64) error
	UpsertMessage(ctx context.Context, input mysqlstore.MessageInput) (uint64, error)
	SaveQueryTurn(ctx context.Context, input mysqlstore.QueryTurnInput) (mysqlstore.QueryTurnResult, error)
	ForkSession(ctx context.Context, input mysqlstore.ForkSessionInput) (mysqlstore.ForkSessionResult, error)
	ListMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]mysqlstore.Message, error)
	ListAllMessages(ctx context.Context, tenantID, userID, sessionID uint64, maxRows int) ([]mysqlstore.Message, error)
	MaxMessageTurn(ctx context.Context, tenantID, userID, sessionID uint64) (uint, error)
	GetMessage(ctx context.Context, tenantID, userID, sessionID, messageID uint64) (mysqlstore.Message, error)
	PreviousUserMessage(ctx context.Context, tenantID, userID, sessionID uint64, beforeTurn uint) (mysqlstore.Message, error)
	MessageByKey(ctx context.Context, tenantID, userID, sessionID uint64, role, messageKey string) (mysqlstore.Message, bool, error)
	ListRecentMessages(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]mysqlstore.Message, error)
	CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error)
	CreateSessionControlRun(context.Context, mysqlstore.SessionControlRunInput) (mysqlstore.SessionControlRunResult, error)
	StartSessionControlRun(context.Context, uint64, uint64, uint64) (mysqlstore.SessionControlRunStartResult, error)
	FailSessionControlRun(context.Context, mysqlstore.SessionControlRunFailureInput) (mysqlstore.SessionControlRunFailureResult, error)
	GetAgentTaskByIdempotencyKey(ctx context.Context, tenantID, userID uint64, idempotencyKey string) (mysqlstore.AgentTask, error)
	UpsertSessionLink(ctx context.Context, input mysqlstore.SessionLinkInput) (uint64, error)
	GetSessionLink(ctx context.Context, tenantID, userID, targetSessionID uint64, sourceKind, sourceSessionKey, relationType string) (mysqlstore.SessionLink, error)
	ListSessionLinks(ctx context.Context, tenantID, userID, targetSessionID uint64, limit int) ([]mysqlstore.SessionLink, error)
	FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error
	CancelAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error
	CancelAgentTaskIfRunning(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) (bool, error)
	CancelAgentTaskForSessionControl(ctx context.Context, input mysqlstore.SessionControlStopInput) (mysqlstore.SessionControlStopResult, error)
	RecoverSessionControlStop(ctx context.Context, tenantID, userID, sessionID uint64, keyHash string) (mysqlstore.SessionControlStopRecovery, error)
	GetAgentTaskStatus(ctx context.Context, tenantID, userID, taskID uint64) (string, error)
	GetAgentTask(ctx context.Context, tenantID, userID, taskID uint64) (mysqlstore.AgentTask, error)
	UpdateAgentTask(ctx context.Context, taskID uint64, input agenttasks.TaskUpdate) error
	ListAgentTasks(ctx context.Context, tenantID, userID uint64, limit int) ([]mysqlstore.AgentTask, error)
	ListLatestAgentTasksForSessions(ctx context.Context, tenantID, userID uint64, sessionIDs []uint64) ([]mysqlstore.AgentTask, error)
	AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error)
	CreateHandoffLinkAndEvent(ctx context.Context, input agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error)
	CreateHandoffBatch(ctx context.Context, input agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error)
	RecoverHandoffBatch(ctx context.Context, input agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error)
	GetAgentTaskEvent(ctx context.Context, tenantID, userID, eventID uint64) (mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEvents(ctx context.Context, tenantID, userID, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEventsAfter(ctx context.Context, tenantID, userID, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEventsForTasks(ctx context.Context, tenantID, userID uint64, taskIDs []uint64, limitPerTask int) ([]mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEventsForTasksComplete(ctx context.Context, tenantID, userID uint64, taskIDs []uint64) ([]mysqlstore.AgentTaskEvent, error)
	CreateGoal(ctx context.Context, tenantID, userID uint64, input goal.CreateInput) (goal.Goal, error)
	GetGoal(ctx context.Context, tenantID, userID uint64, goalID string) (goal.Goal, error)
	ListGoals(ctx context.Context, tenantID, userID uint64, filter goal.ListFilter, limit int) ([]goal.Goal, error)
	UpdateGoal(ctx context.Context, tenantID, userID uint64, item goal.Goal) error
	AppendGoalEvent(ctx context.Context, tenantID, userID uint64, event goal.Event) error
	ListGoalEvents(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.Event, error)
	SaveGoalPlan(ctx context.Context, tenantID, userID uint64, plan goal.GoalPlan) error
	GetGoalPlan(ctx context.Context, tenantID, userID uint64, goalID string) (goal.GoalPlan, bool, error)
	AppendGoalEvidence(ctx context.Context, tenantID, userID uint64, evidence goal.GoalEvidence) error
	ListGoalEvidence(ctx context.Context, tenantID, userID uint64, goalID string, limit int) ([]goal.GoalEvidence, error)
	InsertAuditLog(ctx context.Context, input mysqlstore.AuditLogInput) (uint64, error)
	InsertSessionControlAudit(ctx context.Context, input mysqlstore.SessionControlAuditInput) (mysqlstore.SessionControlAuditResult, error)
	GetSessionControlAuditByKeyHash(context.Context, uint64, uint64, string, string) (mysqlstore.AuditLog, error)
	ListAuditLogs(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.AuditLog, error)
	ListAuditLogsFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.AuditLog, error)
	InsertTelemetryEvent(ctx context.Context, input mysqlstore.TelemetryEventInput) (uint64, error)
	ListTelemetryEvents(ctx context.Context, tenantID uint64, limit int) ([]mysqlstore.TelemetryEvent, error)
	ListTelemetryEventsFiltered(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.TelemetryEvent, error)
	GetTenantQuotaConfig(ctx context.Context, tenantID uint64) (mysqlstore.QuotaConfig, error)
	UpsertTenantQuotaConfig(ctx context.Context, input mysqlstore.QuotaConfigInput) error
	InsertUsageLedger(ctx context.Context, input mysqlstore.UsageLedgerInput) (uint64, error)
	UpdateUsageLedgerSettlement(ctx context.Context, requestID string, input mysqlstore.UsageLedgerInput) error
	UpsertUsageDailyDelta(ctx context.Context, delta mysqlstore.UsageDailyDelta) error
	ListTenantUsageDaily(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.UsageDaily, error)
	ListTenantUsageLedger(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.UsageLedger, error)
	InsertQuotaEvent(ctx context.Context, event mysqlstore.QuotaEvent) (uint64, error)
	ListQuotaEvents(ctx context.Context, tenantID uint64, opts mysqlstore.ListOptions) ([]mysqlstore.QuotaEvent, error)
}

type Service struct {
	repo   Repository
	logger *slog.Logger
}

type resolvedContextKey struct{}

type resolvedContextBinding struct {
	service  *Service
	resolved Context
}

var _ agenttasks.Store = (*Service)(nil)
var _ goal.Store = (*Service)(nil)
var _ goal.PlanStore = (*Service)(nil)

type Context struct {
	Tenant   mysqlstore.Tenant `json:"tenant"`
	UserID   uint64            `json:"user_id"`
	UserKey  string            `json:"user_key"`
	TenantID uint64            `json:"tenant_id"`
}

type ListOptions struct {
	Limit  int
	Cursor uint64
	Search string
}

type TenantListResult struct {
	Data       []mysqlstore.Tenant `json:"data"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

type UserListResult struct {
	Data       []mysqlstore.User `json:"data"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

type AuditListResult struct {
	Data       []mysqlstore.AuditLog `json:"data"`
	NextCursor string                `json:"next_cursor,omitempty"`
	HasMore    bool                  `json:"has_more"`
}

type TelemetryListResult struct {
	Data       []mysqlstore.TelemetryEvent `json:"data"`
	NextCursor string                      `json:"next_cursor,omitempty"`
	HasMore    bool                        `json:"has_more"`
}

type UsageDailyListResult struct {
	Data       []mysqlstore.UsageDaily `json:"data"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

type UsageLedgerListResult struct {
	Data       []mysqlstore.UsageLedger `json:"data"`
	NextCursor string                   `json:"next_cursor,omitempty"`
	HasMore    bool                     `json:"has_more"`
}

type QuotaEventListResult struct {
	Data       []mysqlstore.QuotaEvent `json:"data"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	HasMore    bool                    `json:"has_more"`
}

type QuotaConfigRequest struct {
	QuotaEnabled          bool    `json:"quota_enabled"`
	QPSLimit              *uint64 `json:"qps_limit"`
	DailyTokenLimit       *uint64 `json:"daily_token_limit"`
	DailyMessageLimit     *uint64 `json:"daily_message_limit"`
	MaxConcurrentRequests *uint64 `json:"max_concurrent_requests"`
	Timezone              string  `json:"timezone,omitempty"`
	ReserveOutputTokens   uint64  `json:"reserve_output_tokens,omitempty"`
	Status                string  `json:"status,omitempty"`
}

type UserRequest struct {
	UserKey      string `json:"user_key,omitempty"`
	Email        string `json:"email,omitempty"`
	DisplayName  string `json:"display_name,omitempty"`
	Role         string `json:"role,omitempty"`
	Status       string `json:"status,omitempty"`
	UserInfoJSON string `json:"user_info_json,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

type TenantRequest struct {
	TenantKey    string `json:"tenant_key"`
	Name         string `json:"name,omitempty"`
	Status       string `json:"status,omitempty"`
	SettingsJSON string `json:"settings_json,omitempty"`
}

type MemoryRequest struct {
	MemoryKey    string `json:"memory_key"`
	Category     string `json:"category,omitempty"`
	Content      string `json:"content"`
	MetadataJSON string `json:"metadata_json,omitempty"`
	Importance   int    `json:"importance,omitempty"`
	EmbeddingRef string `json:"embedding_ref,omitempty"`
	Source       string `json:"source,omitempty"`
}

type MemoryReviewRequest struct {
	MemoryKey string `json:"memory_key"`
	Action    string `json:"action"`
}

type AutoMemoryReviewRequest = MemoryReviewRequest

type MemoryReviewListOptions struct {
	Limit           int
	CandidateType   string
	RiskStatus      string
	SourceSessionID uint64
}

type SkillRequest struct {
	SkillKey      string `json:"skill_key"`
	Name          string `json:"name"`
	Description   string `json:"description,omitempty"`
	ContentMD     string `json:"content_md"`
	ConfigJSON    string `json:"config_json,omitempty"`
	PackageRef    string `json:"package_ref,omitempty"`
	PackageSHA256 string `json:"package_sha256,omitempty"`
	ManifestJSON  string `json:"manifest_json,omitempty"`
	RuntimeRef    string `json:"runtime_ref,omitempty"`
	Version       uint   `json:"version,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

type SkillRollbackRequest struct {
	SkillKey      string `json:"skill_key"`
	Version       uint   `json:"version"`
	TargetVersion uint   `json:"target_version,omitempty"`
}

type SkillRollbackResult struct {
	ID          uint64 `json:"id"`
	SkillKey    string `json:"skill_key"`
	FromVersion uint   `json:"from_version"`
	Version     uint   `json:"version"`
}

type SkillPackageRequest struct {
	SkillKey      string `json:"skill_key"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Version       uint   `json:"version,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
	StoreRoot     string `json:"store_root,omitempty"`
}

type SkillPackageResult struct {
	ID            uint64             `json:"id,omitempty"`
	SkillKey      string             `json:"skill_key"`
	Name          string             `json:"name,omitempty"`
	Description   string             `json:"description,omitempty"`
	Version       uint               `json:"version,omitempty"`
	Enabled       bool               `json:"enabled,omitempty"`
	PackageSHA256 string             `json:"package_sha256"`
	PackageRef    string             `json:"package_ref,omitempty"`
	RuntimeRef    string             `json:"runtime_ref,omitempty"`
	ManifestRef   string             `json:"manifest_ref,omitempty"`
	RuntimeMD     string             `json:"runtime_md,omitempty"`
	Manifest      tenantpkg.Manifest `json:"manifest"`
}

type SkillOverrideRequest struct {
	SkillKey   string `json:"skill_key"`
	Version    uint   `json:"version,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	ConfigJSON string `json:"config_json,omitempty"`
}

type DocumentRequest struct {
	DocType     string `json:"doc_type"`
	Title       string `json:"title,omitempty"`
	ContentMD   string `json:"content_md,omitempty"`
	ContentJSON string `json:"content_json,omitempty"`
	Version     uint   `json:"version,omitempty"`
	Active      bool   `json:"active"`
}

type KnowledgeDocumentRequest struct {
	Title        string `json:"title"`
	SourceType   string `json:"source_type,omitempty"`
	Content      string `json:"content"`
	MetadataJSON string `json:"metadata_json,omitempty"`
	Status       string `json:"status,omitempty"`
}

type KnowledgeSearchRequest struct {
	Query string `json:"query"`
	Limit int    `json:"limit,omitempty"`
}

type ProfileRequest struct {
	ProfileVersion         uint   `json:"profile_version,omitempty"`
	Summary                string `json:"summary,omitempty"`
	ProfileJSON            string `json:"profile_json"`
	GeneratedFromSessionID uint64 `json:"generated_from_session_id,omitempty"`
}

type SessionRequest struct {
	SessionKey   string `json:"session_key"`
	Title        string `json:"title,omitempty"`
	Status       string `json:"status,omitempty"`
	Model        string `json:"model,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

type SessionControlSessionRequest = SessionRequest

type SessionControlStopRequest struct {
	TaskID           uint64
	ResultJSON       string
	EventPayloadJSON string
	TraceID          string
}

type MessageRequest struct {
	SessionID   uint64 `json:"session_id"`
	TurnIndex   uint   `json:"turn_index"`
	Role        string `json:"role"`
	Content     string `json:"content,omitempty"`
	ContentJSON string `json:"content_json,omitempty"`
	ToolID      string `json:"tool_id,omitempty"`
	ToolName    string `json:"tool_name,omitempty"`
	IsError     bool   `json:"is_error,omitempty"`
	Model       string `json:"model,omitempty"`
	InputTokens uint   `json:"input_tokens,omitempty"`
	OutputToken uint   `json:"output_tokens,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
}

// QueryTurnRequest 是一次查询要原子落库的整批行。User/Assistant 的
// SessionID 由存储层填成同一事务里刚写的那个会话，调用方不填。
type QueryTurnRequest struct {
	Session   SessionRequest
	User      MessageRequest
	Assistant MessageRequest
}

type QueryTurnResult struct {
	SessionID          uint64
	UserMessageID      uint64
	AssistantMessageID uint64
}

// ForkSessionRequest 是一次会话 fork 要原子落库的整批行：新建的分支会话 +
// 逐条复制的消息。消息的 SessionID 由存储层填成同一事务里刚写的分支会话，调用方不填。
type ForkSessionRequest struct {
	Session  SessionRequest
	Messages []MessageRequest
}

type ForkSessionResult struct {
	SessionID      uint64
	CopiedMessages int
}

type AuditRequest struct {
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id,omitempty"`
	MetadataJSON string `json:"metadata_json,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
}

type SessionControlAuditRequest = AuditRequest

func NewService(repo Repository, logger *slog.Logger) *Service {
	return &Service{repo: repo, logger: logger}
}

func (s *Service) ResolveContext(ctx context.Context) (Context, error) {
	if binding, ok := ctx.Value(resolvedContextKey{}).(resolvedContextBinding); ok && binding.service == s {
		return binding.resolved, nil
	}
	s.log(ctx, "tenant.resolve", "tenant.Service.ResolveContext", "resolve tenant user context")
	tenantKey, userKey, err := requireTenantUser(ctx)
	if err != nil {
		return Context{}, err
	}
	tenant, err := s.repo.GetTenantByKey(ctx, tenantKey)
	if err != nil {
		return Context{}, fmt.Errorf("get tenant %s: %w", tenantKey, err)
	}
	userID, err := s.repo.EnsureUser(ctx, tenant.ID, userKey)
	if err != nil {
		return Context{}, fmt.Errorf("ensure tenant user %s: %w", userKey, err)
	}
	return Context{Tenant: tenant, TenantID: tenant.ID, UserID: userID, UserKey: userKey}, nil
}

// ResolveContextOnce authenticates tenant/user identity and returns a context
// bound to this Service instance. Only tenant can construct the private marker;
// another Service instance deliberately ignores it and resolves independently.
func (s *Service) ResolveContextOnce(ctx context.Context) (context.Context, Context, error) {
	if binding, ok := ctx.Value(resolvedContextKey{}).(resolvedContextBinding); ok && binding.service == s {
		return ctx, binding.resolved, nil
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return ctx, Context{}, err
	}
	bound := context.WithValue(ctx, resolvedContextKey{}, resolvedContextBinding{service: s, resolved: resolved})
	return bound, resolved, nil
}

func (s *Service) SaveTenant(ctx context.Context, req TenantRequest) (uint64, error) {
	s.log(ctx, "tenant.admin_save", "tenant.Service.SaveTenant", "admin save tenant")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return 0, err
	}
	if req.TenantKey == "" {
		return 0, ErrMissingTenantKey
	}
	return s.repo.UpsertTenant(ctx, mysqlstore.TenantInput{
		TenantKey:    req.TenantKey,
		Name:         req.Name,
		Status:       req.Status,
		SettingsJSON: req.SettingsJSON,
	})
}

func (s *Service) ArchiveTenant(ctx context.Context, tenantKey string) error {
	s.log(ctx, "tenant.archive", "tenant.Service.ArchiveTenant", "archive tenant")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return err
	}
	if tenantKey == "" {
		return ErrMissingTenantKey
	}
	return s.repo.ArchiveTenant(ctx, tenantKey)
}

func (s *Service) ListTenants(ctx context.Context, limit int) ([]mysqlstore.Tenant, error) {
	s.log(ctx, "tenant.list", "tenant.Service.ListTenants", "list tenants")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return nil, err
	}
	return s.repo.ListTenants(ctx, limit)
}

func (s *Service) ListTenantsPage(ctx context.Context, opts ListOptions) (TenantListResult, error) {
	s.log(ctx, "tenant.list_page", "tenant.Service.ListTenantsPage", "list tenants page")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return TenantListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListTenantsFiltered(ctx, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return TenantListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return TenantListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) GetCurrentUser(ctx context.Context) (mysqlstore.User, error) {
	s.log(ctx, "tenant.user.get", "tenant.Service.GetCurrentUser", "get tenant user info")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.User{}, err
	}
	return s.repo.GetUser(ctx, resolved.TenantID, resolved.UserID)
}

func (s *Service) SaveCurrentUser(ctx context.Context, req UserRequest) (uint64, error) {
	s.log(ctx, "tenant.user.save", "tenant.Service.SaveCurrentUser", "save tenant user info")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	current, err := s.repo.GetUser(ctx, resolved.TenantID, resolved.UserID)
	if err != nil && !errors.Is(err, mysqlstore.ErrNotFound) {
		return 0, err
	}
	return s.repo.UpsertUser(ctx, mysqlstore.UserInput{
		TenantID:     resolved.TenantID,
		UserKey:      resolved.UserKey,
		Email:        preferNonEmpty(req.Email, current.Email),
		DisplayName:  preferNonEmpty(req.DisplayName, current.DisplayName),
		Role:         preferNonEmpty(req.Role, current.Role),
		Status:       preferNonEmpty(req.Status, current.Status),
		UserInfoJSON: preferNonEmpty(req.UserInfoJSON, current.UserInfoJSON),
		MetadataJSON: preferNonEmpty(req.MetadataJSON, current.MetadataJSON),
	})
}

func (s *Service) SaveTenantUser(ctx context.Context, req UserRequest) (uint64, error) {
	s.log(ctx, "tenant.user.admin_save", "tenant.Service.SaveTenantUser", "admin save tenant user")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return 0, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	userKey := req.UserKey
	if userKey == "" {
		return 0, ErrMissingUserID
	}
	return s.repo.UpsertUser(ctx, mysqlstore.UserInput{
		TenantID:     resolved.TenantID,
		UserKey:      userKey,
		Email:        req.Email,
		DisplayName:  req.DisplayName,
		Role:         req.Role,
		Status:       req.Status,
		UserInfoJSON: req.UserInfoJSON,
		MetadataJSON: req.MetadataJSON,
	})
}

func (s *Service) ArchiveTenantUser(ctx context.Context, userKey string) error {
	s.log(ctx, "tenant.user.archive", "tenant.Service.ArchiveTenantUser", "archive tenant user")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	if userKey == "" {
		return ErrMissingUserID
	}
	return s.repo.ArchiveUser(ctx, resolved.TenantID, userKey)
}

func (s *Service) ListTenantUsers(ctx context.Context, limit int) ([]mysqlstore.User, error) {
	s.log(ctx, "tenant.user.list", "tenant.Service.ListTenantUsers", "list tenant users")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListUsers(ctx, resolved.TenantID, limit)
}

func (s *Service) ListTenantUsersPage(ctx context.Context, opts ListOptions) (UserListResult, error) {
	s.log(ctx, "tenant.user.list_page", "tenant.Service.ListTenantUsersPage", "list tenant users page")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return UserListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return UserListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListUsersFiltered(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return UserListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return UserListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) UpsertMemory(ctx context.Context, req MemoryRequest) (uint64, error) {
	s.log(ctx, "tenant.memory.upsert", "tenant.Service.UpsertMemory", "upsert tenant user memory")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.UpsertMemory(ctx, mysqlstore.MemoryInput{
		TenantID:     resolved.TenantID,
		UserID:       resolved.UserID,
		MemoryKey:    req.MemoryKey,
		Category:     req.Category,
		Content:      req.Content,
		MetadataJSON: req.MetadataJSON,
		Importance:   req.Importance,
		EmbeddingRef: req.EmbeddingRef,
		Source:       req.Source,
	})
}

func (s *Service) ListMemories(ctx context.Context, category string, limit int) ([]mysqlstore.Memory, error) {
	s.log(ctx, "tenant.memory.list", "tenant.Service.ListMemories", "list tenant user memories")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListMemories(ctx, resolved.TenantID, resolved.UserID, category, limit)
}

func (s *Service) ListAutoMemoryCandidates(ctx context.Context, limit int) ([]mysqlstore.Memory, error) {
	s.log(ctx, "tenant.automem.list", "tenant.Service.ListAutoMemoryCandidates", "list pending auto memory candidates")
	return s.ListMemories(ctx, "automem_pending", limit)
}

func (s *Service) ListMemoryReviewCandidates(ctx context.Context, limit int) ([]mysqlstore.Memory, error) {
	return s.ListMemoryReviewCandidatesFiltered(ctx, MemoryReviewListOptions{Limit: limit})
}

func (s *Service) ListMemoryReviewCandidatesFiltered(ctx context.Context, opts MemoryReviewListOptions) ([]mysqlstore.Memory, error) {
	s.log(ctx, "tenant.memory_review.list", "tenant.Service.ListMemoryReviewCandidates", "list pending memory candidates")
	limit := normalizeReviewLimit(opts.Limit)
	poolLimit := limit
	if hasMemoryReviewFilters(opts) {
		poolLimit = 200
	}
	var items []mysqlstore.Memory
	if memoryReviewTypeMatches(opts.CandidateType, "automem") {
		automem, err := s.ListMemories(ctx, "automem_pending", poolLimit)
		if err != nil {
			return nil, err
		}
		items = append(items, automem...)
	}
	if memoryReviewTypeMatches(opts.CandidateType, "explicit-remember") {
		explicit, err := s.ListMemories(ctx, "explicit_pending", poolLimit)
		if err != nil {
			return nil, err
		}
		items = append(items, explicit...)
	}
	items = filterMemoryReviewCandidates(items, opts)
	sortMemoryReviewCandidates(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func hasMemoryReviewFilters(opts MemoryReviewListOptions) bool {
	return strings.TrimSpace(opts.CandidateType) != "" || strings.TrimSpace(opts.RiskStatus) != "" || opts.SourceSessionID != 0
}

func memoryReviewTypeMatches(filter string, kind string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" || filter == "all" {
		return true
	}
	switch filter {
	case "explicit_remember", "explicit-remember":
		return kind == "explicit-remember"
	case "automem_candidate", "automem":
		return kind == "automem"
	default:
		return false
	}
}

func filterMemoryReviewCandidates(items []mysqlstore.Memory, opts MemoryReviewListOptions) []mysqlstore.Memory {
	if !hasMemoryReviewFilters(opts) {
		return items
	}
	out := make([]mysqlstore.Memory, 0, len(items))
	for _, item := range items {
		if !memoryReviewTypeMatches(opts.CandidateType, memoryReviewKind(item)) {
			continue
		}
		metadata := parseMemoryReviewCandidateMetadata(item)
		if risk := strings.TrimSpace(opts.RiskStatus); risk != "" && risk != "all" && !strings.EqualFold(memoryReviewRiskStatus(metadata), risk) {
			continue
		}
		if opts.SourceSessionID != 0 && memoryReviewSourceSessionID(metadata) != opts.SourceSessionID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func sortMemoryReviewCandidates(items []mysqlstore.Memory) {
	sort.SliceStable(items, func(i, j int) bool {
		leftExplicit := memoryReviewKind(items[i]) == "explicit-remember"
		rightExplicit := memoryReviewKind(items[j]) == "explicit-remember"
		if leftExplicit != rightExplicit {
			return leftExplicit
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}

func parseMemoryReviewCandidateMetadata(candidate mysqlstore.Memory) map[string]any {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(candidate.MetadataJSON), &metadata); err != nil || metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func memoryReviewRiskStatus(metadata map[string]any) string {
	if value, ok := metadata["risk_status"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return "needs_review"
}

func memoryReviewSourceSessionID(metadata map[string]any) uint64 {
	value, ok := metadata["source_session_id"]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return uint64(typed)
		}
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}

func (s *Service) ReviewMemoryCandidate(ctx context.Context, req MemoryReviewRequest) (uint64, error) {
	s.log(ctx, "tenant.memory_review.review", "tenant.Service.ReviewMemoryCandidate", "review memory candidate")
	key := strings.TrimSpace(req.MemoryKey)
	if key == "" {
		return 0, fmt.Errorf("memory_key is required")
	}
	candidate, err := s.findMemoryReviewCandidate(ctx, key)
	if err != nil {
		return 0, err
	}
	reviewKind := memoryReviewKind(candidate)
	targetCategory := memoryReviewTargetCategory(candidate)
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "approve", "approved":
		targetSource := reviewKind + "-approved"
		if reviewKind == "explicit-remember" {
			targetSource = "explicit-user-remember-approved"
		}
		id, err := s.UpsertMemory(ctx, MemoryRequest{
			MemoryKey:    memoryReviewApprovedKey(candidate),
			Category:     targetCategory,
			Content:      candidate.Content,
			MetadataJSON: memoryReviewMetadata(candidate, "approved"),
			Importance:   candidate.Importance,
			Source:       targetSource,
		})
		if err != nil {
			return 0, err
		}
		if _, err := s.UpsertMemory(ctx, MemoryRequest{
			MemoryKey:    candidate.MemoryKey,
			Category:     memoryReviewMarkerCategory(candidate, "approved"),
			Content:      candidate.Content,
			MetadataJSON: memoryReviewMetadata(candidate, "approved"),
			Importance:   candidate.Importance,
			Source:       reviewKind + "-approved",
		}); err != nil {
			return 0, err
		}
		s.emitMemoryReviewTelemetry(ctx, candidate, "approved", id)
		return id, nil
	case "reject", "rejected":
		id, err := s.UpsertMemory(ctx, MemoryRequest{
			MemoryKey:    candidate.MemoryKey,
			Category:     memoryReviewMarkerCategory(candidate, "rejected"),
			Content:      candidate.Content,
			MetadataJSON: memoryReviewMetadata(candidate, "rejected"),
			Importance:   candidate.Importance,
			Source:       reviewKind + "-rejected",
		})
		if err == nil {
			s.emitMemoryReviewTelemetry(ctx, candidate, "rejected", id)
		}
		return id, err
	case "archive", "archived":
		id, err := s.UpsertMemory(ctx, MemoryRequest{
			MemoryKey:    candidate.MemoryKey,
			Category:     memoryReviewMarkerCategory(candidate, "archived"),
			Content:      candidate.Content,
			MetadataJSON: memoryReviewMetadata(candidate, "archived"),
			Importance:   candidate.Importance,
			Source:       reviewKind + "-archived",
		})
		if err == nil {
			s.emitMemoryReviewTelemetry(ctx, candidate, "archived", id)
		}
		return id, err
	default:
		return 0, fmt.Errorf("action must be approve, reject, or archive")
	}
}

func (s *Service) ReviewAutoMemory(ctx context.Context, req AutoMemoryReviewRequest) (uint64, error) {
	s.log(ctx, "tenant.automem.review", "tenant.Service.ReviewAutoMemory", "review auto memory candidate")
	return s.ReviewMemoryCandidate(ctx, req)
}

func (s *Service) findMemoryReviewCandidate(ctx context.Context, key string) (mysqlstore.Memory, error) {
	for _, category := range []string{"automem_pending", "explicit_pending"} {
		candidates, err := s.ListMemories(ctx, category, 100)
		if err != nil {
			return mysqlstore.Memory{}, err
		}
		for _, item := range candidates {
			if item.MemoryKey == key {
				return item, nil
			}
		}
	}
	return mysqlstore.Memory{}, mysqlstore.ErrNotFound
}

func (s *Service) UpsertSkill(ctx context.Context, req SkillRequest) (uint64, error) {
	s.log(ctx, "tenant.skill.upsert", "tenant.Service.UpsertSkill", "upsert tenant skill")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return 0, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return s.repo.UpsertSkill(ctx, mysqlstore.SkillInput{
		TenantID:        resolved.TenantID,
		SkillKey:        req.SkillKey,
		Name:            req.Name,
		Description:     req.Description,
		ContentMD:       req.ContentMD,
		ConfigJSON:      req.ConfigJSON,
		PackageRef:      req.PackageRef,
		PackageSHA256:   req.PackageSHA256,
		ManifestJSON:    req.ManifestJSON,
		RuntimeRef:      req.RuntimeRef,
		Version:         req.Version,
		Enabled:         enabled,
		CreatedByUserID: resolved.UserID,
	})
}

func (s *Service) RollbackSkill(ctx context.Context, req SkillRollbackRequest) (SkillRollbackResult, error) {
	s.log(ctx, "tenant.skill.rollback", "tenant.Service.RollbackSkill", "rollback tenant skill to historical version")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return SkillRollbackResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return SkillRollbackResult{}, err
	}
	skillKey := strings.TrimSpace(req.SkillKey)
	if skillKey == "" || req.Version == 0 {
		return SkillRollbackResult{}, fmt.Errorf("%w: skill_key and version are required", ErrInvalidRequest)
	}
	latest, err := s.repo.GetSkill(ctx, resolved.TenantID, skillKey, 0)
	if err != nil {
		return SkillRollbackResult{}, err
	}
	if req.TargetVersion > 0 && req.TargetVersion <= latest.Version {
		return SkillRollbackResult{}, fmt.Errorf("%w: target_version must be greater than latest version %d", ErrInvalidRequest, latest.Version)
	}
	created, err := s.repo.RollbackSkillVersion(ctx, mysqlstore.SkillRollbackInput{
		TenantID:        resolved.TenantID,
		SkillKey:        skillKey,
		FromVersion:     req.Version,
		TargetVersion:   req.TargetVersion,
		CreatedByUserID: resolved.UserID,
	})
	if err != nil {
		if errors.Is(err, mysqlstore.ErrInvalidInput) {
			return SkillRollbackResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
		}
		return SkillRollbackResult{}, err
	}
	return SkillRollbackResult{
		ID:          created.ID,
		SkillKey:    created.SkillKey,
		FromVersion: req.Version,
		Version:     created.Version,
	}, nil
}

func (s *Service) RenderSkillPackage(ctx context.Context, req SkillPackageRequest) (SkillPackageResult, error) {
	s.log(ctx, "tenant.skill_package.render", "tenant.Service.RenderSkillPackage", "render tenant skill package")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return SkillPackageResult{}, err
	}
	pkg, cleanup, err := importSkillPackage(req)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return SkillPackageResult{}, err
	}
	return skillPackageResult(pkg, tenantpkg.StoreResult{}, req, 0, false), nil
}

func (s *Service) PublishSkillPackage(ctx context.Context, req SkillPackageRequest) (SkillPackageResult, error) {
	s.log(ctx, "tenant.skill_package.publish", "tenant.Service.PublishSkillPackage", "publish tenant skill package")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return SkillPackageResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return SkillPackageResult{}, err
	}
	pkg, cleanup, err := importSkillPackage(req)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return SkillPackageResult{}, err
	}
	skillKey := strings.TrimSpace(req.SkillKey)
	if skillKey == "" {
		skillKey = pkg.Manifest.SkillKey
	}
	if skillKey == "" {
		return SkillPackageResult{}, fmt.Errorf("%w: skill_key is required", ErrInvalidRequest)
	}
	version := req.Version
	if version == 0 {
		if latest, err := s.repo.GetSkill(ctx, resolved.TenantID, skillKey, 0); err == nil {
			version = latest.Version + 1
		} else if errors.Is(err, mysqlstore.ErrNotFound) {
			version = 1
		} else {
			return SkillPackageResult{}, err
		}
	}
	store := tenantpkg.Store{Root: strings.TrimSpace(req.StoreRoot)}
	result, err := store.Save(resolved.Tenant.TenantKey, skillKey, pkg)
	if err != nil {
		return SkillPackageResult{}, err
	}
	manifestJSON, err := json.Marshal(pkg.Manifest)
	if err != nil {
		return SkillPackageResult{}, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = skillKey
	}
	id, err := s.repo.UpsertSkill(ctx, mysqlstore.SkillInput{
		TenantID:        resolved.TenantID,
		SkillKey:        skillKey,
		Name:            name,
		Description:     req.Description,
		ContentMD:       pkg.RuntimeMD,
		ConfigJSON:      packageConfigJSON(result, pkg),
		PackageRef:      result.PackageRef,
		PackageSHA256:   pkg.PackageSHA256,
		ManifestJSON:    string(manifestJSON),
		RuntimeRef:      result.RuntimeRef,
		Version:         version,
		Enabled:         enabled,
		CreatedByUserID: resolved.UserID,
	})
	if err != nil {
		return SkillPackageResult{}, err
	}
	out := skillPackageResult(pkg, result, req, version, enabled)
	out.ID = id
	out.SkillKey = skillKey
	out.Name = name
	out.Description = req.Description
	return out, nil
}

func (s *Service) ListSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.Skill, error) {
	s.log(ctx, "tenant.skill.list", "tenant.Service.ListSkills", "list tenant skills")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSkills(ctx, resolved.TenantID, enabledOnly, limit)
}

func (s *Service) GetSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.Skill, error) {
	s.log(ctx, "tenant.skill.get", "tenant.Service.GetSkill", "get tenant skill")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Skill{}, err
	}
	return s.repo.GetSkill(ctx, resolved.TenantID, skillKey, version)
}

func importSkillPackage(req SkillPackageRequest) (tenantpkg.Package, func(), error) {
	sourcePath := strings.TrimSpace(req.SourcePath)
	if sourcePath != "" {
		pkg, err := tenantpkg.ImportPath(sourcePath, tenantpkg.ImportOptions{SkillKey: req.SkillKey})
		return pkg, nil, err
	}
	content := strings.TrimSpace(req.ContentBase64)
	if content == "" {
		return tenantpkg.Package{}, nil, fmt.Errorf("%w: source_path or content_base64 is required", ErrInvalidRequest)
	}
	raw, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return tenantpkg.Package{}, nil, fmt.Errorf("%w: invalid content_base64", ErrInvalidRequest)
	}
	dir, err := os.MkdirTemp("", "tenant-skill-package-*")
	if err != nil {
		return tenantpkg.Package{}, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	zipPath := filepath.Join(dir, "package.skill.zip")
	if err := os.WriteFile(zipPath, raw, 0o600); err != nil {
		cleanup()
		return tenantpkg.Package{}, nil, err
	}
	pkg, err := tenantpkg.ImportZip(zipPath, tenantpkg.ImportOptions{SkillKey: req.SkillKey})
	if err != nil {
		cleanup()
		return tenantpkg.Package{}, nil, err
	}
	return pkg, cleanup, nil
}

func packageConfigJSON(store tenantpkg.StoreResult, pkg tenantpkg.Package) string {
	raw, err := json.Marshal(map[string]any{
		"source":         "tenant_skill_package",
		"schema_version": tenantpkg.SchemaVersion,
		"package_sha256": pkg.PackageSHA256,
		"package_ref":    store.PackageRef,
		"manifest_ref":   store.ManifestRef,
		"runtime_ref":    store.RuntimeRef,
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

func skillPackageResult(pkg tenantpkg.Package, store tenantpkg.StoreResult, req SkillPackageRequest, version uint, enabled bool) SkillPackageResult {
	skillKey := strings.TrimSpace(req.SkillKey)
	if skillKey == "" {
		skillKey = pkg.Manifest.SkillKey
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = skillKey
	}
	return SkillPackageResult{
		SkillKey:      skillKey,
		Name:          name,
		Description:   req.Description,
		Version:       version,
		Enabled:       enabled,
		PackageSHA256: pkg.PackageSHA256,
		PackageRef:    store.PackageRef,
		RuntimeRef:    store.RuntimeRef,
		ManifestRef:   store.ManifestRef,
		RuntimeMD:     pkg.RuntimeMD,
		Manifest:      pkg.Manifest,
	}
}

func (s *Service) UpsertSkillOverride(ctx context.Context, req SkillOverrideRequest) (uint64, error) {
	s.log(ctx, "tenant.skill_override.upsert", "tenant.Service.UpsertSkillOverride", "upsert tenant user skill override")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	skill, err := s.repo.GetSkill(ctx, resolved.TenantID, req.SkillKey, req.Version)
	if err != nil {
		return 0, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return s.repo.UpsertSkillOverride(ctx, mysqlstore.SkillOverrideInput{
		TenantID:   resolved.TenantID,
		UserID:     resolved.UserID,
		SkillID:    skill.ID,
		Enabled:    enabled,
		ConfigJSON: req.ConfigJSON,
	})
}

func (s *Service) ListSkillOverrides(ctx context.Context, limit int) ([]mysqlstore.SkillOverride, error) {
	s.log(ctx, "tenant.skill_override.list", "tenant.Service.ListSkillOverrides", "list tenant user skill overrides")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSkillOverrides(ctx, resolved.TenantID, resolved.UserID, limit)
}

func (s *Service) GetSkillOverride(ctx context.Context, skillKey string, version uint) (mysqlstore.SkillOverride, error) {
	s.log(ctx, "tenant.skill_override.get", "tenant.Service.GetSkillOverride", "get tenant user skill override")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SkillOverride{}, err
	}
	return s.repo.GetSkillOverride(ctx, resolved.TenantID, resolved.UserID, skillKey, version)
}

func (s *Service) ListEffectiveSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.EffectiveSkill, error) {
	s.log(ctx, "tenant.skill.effective_list", "tenant.Service.ListEffectiveSkills", "list effective tenant user skills")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListEffectiveSkills(ctx, resolved.TenantID, resolved.UserID, enabledOnly, limit)
}

func (s *Service) GetEffectiveSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.EffectiveSkill, error) {
	s.log(ctx, "tenant.skill.effective_get", "tenant.Service.GetEffectiveSkill", "get effective tenant user skill")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.EffectiveSkill{}, err
	}
	return s.repo.GetEffectiveSkill(ctx, resolved.TenantID, resolved.UserID, skillKey, version)
}

func (s *Service) SaveDocument(ctx context.Context, req DocumentRequest) (uint64, error) {
	s.log(ctx, "tenant.document.save", "tenant.Service.SaveDocument", "save tenant user document")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.SaveDocument(ctx, mysqlstore.DocumentInput{
		TenantID:    resolved.TenantID,
		UserID:      resolved.UserID,
		DocType:     req.DocType,
		Title:       req.Title,
		ContentMD:   req.ContentMD,
		ContentJSON: req.ContentJSON,
		Version:     req.Version,
		Active:      req.Active,
	})
}

func (s *Service) GetActiveDocument(ctx context.Context, docType string) (mysqlstore.Document, error) {
	s.log(ctx, "tenant.document.get_active", "tenant.Service.GetActiveDocument", "get active tenant user document")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Document{}, err
	}
	return s.repo.GetActiveDocument(ctx, resolved.TenantID, resolved.UserID, docType)
}

func (s *Service) ListDocuments(ctx context.Context, docType string, limit int) ([]mysqlstore.Document, error) {
	s.log(ctx, "tenant.document.list", "tenant.Service.ListDocuments", "list tenant user documents")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListDocuments(ctx, resolved.TenantID, resolved.UserID, docType, limit)
}

func (s *Service) SaveKnowledgeDocument(ctx context.Context, req KnowledgeDocumentRequest) (uint64, error) {
	s.log(ctx, "tenant.knowledge.save", "tenant.Service.SaveKnowledgeDocument", "save tenant knowledge document")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	title := strings.TrimSpace(req.Title)
	content := strings.TrimSpace(req.Content)
	if title == "" {
		title = "Untitled"
	}
	if content == "" {
		return 0, fmt.Errorf("knowledge content is required")
	}
	input := mysqlstore.KnowledgeDocumentInput{
		TenantID:     resolved.TenantID,
		UserID:       resolved.UserID,
		Title:        title,
		SourceType:   firstNonEmpty(req.SourceType, "manual"),
		Content:      content,
		MetadataJSON: req.MetadataJSON,
		Status:       firstNonEmpty(req.Status, "active"),
	}
	return s.repo.SaveKnowledgeDocument(ctx, input, buildKnowledgeChunks(input))
}

func (s *Service) ListKnowledgeDocuments(ctx context.Context, limit int) ([]mysqlstore.KnowledgeDocument, error) {
	s.log(ctx, "tenant.knowledge.list", "tenant.Service.ListKnowledgeDocuments", "list tenant knowledge documents")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListKnowledgeDocuments(ctx, resolved.TenantID, resolved.UserID, limit)
}

func (s *Service) SearchKnowledgeChunks(ctx context.Context, req KnowledgeSearchRequest) ([]mysqlstore.KnowledgeChunk, error) {
	s.log(ctx, "tenant.knowledge.search", "tenant.Service.SearchKnowledgeChunks", "search tenant knowledge chunks")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.SearchKnowledgeChunks(ctx, resolved.TenantID, mysqlstore.KnowledgeSearchOptions{Query: req.Query, Limit: req.Limit, UserID: resolved.UserID})
}

func (s *Service) SaveProfile(ctx context.Context, req ProfileRequest) (uint64, error) {
	s.log(ctx, "tenant.profile.save", "tenant.Service.SaveProfile", "save tenant user profile")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.SaveProfile(ctx, mysqlstore.ProfileInput{
		TenantID:               resolved.TenantID,
		UserID:                 resolved.UserID,
		ProfileVersion:         req.ProfileVersion,
		Summary:                req.Summary,
		ProfileJSON:            req.ProfileJSON,
		GeneratedFromSessionID: req.GeneratedFromSessionID,
	})
}

func (s *Service) GetProfile(ctx context.Context, version uint) (mysqlstore.Profile, error) {
	s.log(ctx, "tenant.profile.get", "tenant.Service.GetProfile", "get tenant user profile")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Profile{}, err
	}
	return s.repo.GetProfile(ctx, resolved.TenantID, resolved.UserID, version)
}

func (s *Service) UpsertSession(ctx context.Context, req SessionRequest) (uint64, error) {
	s.log(ctx, "tenant.session.upsert", "tenant.Service.UpsertSession", "upsert tenant session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.UpsertSession(ctx, mysqlstore.SessionInput{
		TenantID:     resolved.TenantID,
		UserID:       resolved.UserID,
		SessionKey:   req.SessionKey,
		Title:        req.Title,
		Status:       req.Status,
		Model:        req.Model,
		CWD:          req.CWD,
		MetadataJSON: req.MetadataJSON,
	})
}

func (s *Service) CreateSessionControlSession(ctx context.Context, req SessionControlSessionRequest) (mysqlstore.SessionControlCreateResult, error) {
	s.log(ctx, "tenant.session_control.create", "tenant.Service.CreateSessionControlSession", "create managed session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlCreateResult{}, err
	}
	return s.repo.CreateSessionControlSession(ctx, mysqlstore.SessionControlCreateInput{Session: mysqlstore.SessionInput{
		TenantID: resolved.TenantID, UserID: resolved.UserID, SessionKey: req.SessionKey,
		Title: req.Title, Status: req.Status, Model: req.Model, CWD: req.CWD, MetadataJSON: req.MetadataJSON,
	}})
}

func (s *Service) GetSessionControlSessionByKey(ctx context.Context, sessionKey string) (mysqlstore.SessionControlSession, error) {
	s.log(ctx, "tenant.session_control.get", "tenant.Service.GetSessionControlSessionByKey", "get managed session recovery metadata")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlSession{}, err
	}
	return s.repo.GetSessionControlSessionByKey(ctx, resolved.TenantID, resolved.UserID, sessionKey)
}

func (s *Service) ListSessionControlSessions(ctx context.Context, limit int) ([]mysqlstore.SessionControlSession, error) {
	s.log(ctx, "tenant.session_control.list", "tenant.Service.ListSessionControlSessions", "list managed session configuration")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSessionControlSessions(ctx, resolved.TenantID, resolved.UserID, limit)
}

func (s *Service) ListSessionChannels(ctx context.Context, tenantID, userID uint64, sessionIDs []uint64) (map[uint64]mysqlstore.SessionChannel, error) {
	if len(sessionIDs) == 0 {
		return map[uint64]mysqlstore.SessionChannel{}, nil
	}
	lister, ok := s.repo.(interface {
		ListSessionChannels(context.Context, uint64, uint64, []uint64) (map[uint64]mysqlstore.SessionChannel, error)
	})
	if !ok {
		return map[uint64]mysqlstore.SessionChannel{}, nil
	}
	return lister.ListSessionChannels(ctx, tenantID, userID, sessionIDs)
}

type sessionControlOperationLockRepository interface {
	AcquireSessionControlOperationLock(context.Context, string) (func(context.Context) error, error)
}

func (s *Service) AcquireSessionControlOperationLock(ctx context.Context, keyHash string) (func(context.Context) error, error) {
	if _, err := s.ResolveContext(ctx); err != nil {
		return nil, err
	}
	repo, ok := s.repo.(sessionControlOperationLockRepository)
	if !ok {
		return nil, mysqlstore.ErrOperationLockUnavailable
	}
	return repo.AcquireSessionControlOperationLock(ctx, keyHash)
}

type sessionMonitorRepository interface {
	ResolveSessionMonitorTarget(context.Context, uint64, uint64, string, string) (mysqlstore.SessionMonitorTarget, error)
	GetSessionMonitorLink(context.Context, uint64, uint64, uint64, string, string) (mysqlstore.SessionLink, error)
	UpsertSessionMonitorLink(context.Context, mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error)
	ApplySessionMonitorConfiguration(context.Context, mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error)
}

func (s *Service) ResolveSessionMonitorTarget(ctx context.Context, ref string, channel string) (mysqlstore.SessionMonitorTarget, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionMonitorTarget{}, err
	}
	repo, ok := s.repo.(sessionMonitorRepository)
	if !ok {
		return mysqlstore.SessionMonitorTarget{}, mysqlstore.ErrInvalidState
	}
	return repo.ResolveSessionMonitorTarget(ctx, resolved.TenantID, resolved.UserID, ref, channel)
}

func (s *Service) GetSessionMonitorLink(ctx context.Context, targetSessionID uint64, sourceKind, sourceSessionKey string) (mysqlstore.SessionLink, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionLink{}, err
	}
	repo, ok := s.repo.(sessionMonitorRepository)
	if !ok {
		return mysqlstore.SessionLink{}, mysqlstore.ErrInvalidState
	}
	return repo.GetSessionMonitorLink(ctx, resolved.TenantID, resolved.UserID, targetSessionID, sourceKind, sourceSessionKey)
}

func (s *Service) UpsertSessionMonitorLink(ctx context.Context, input mysqlstore.SessionMonitorLinkInput) (mysqlstore.SessionLink, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionLink{}, err
	}
	repo, ok := s.repo.(sessionMonitorRepository)
	if !ok {
		return mysqlstore.SessionLink{}, mysqlstore.ErrInvalidState
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = resolved.UserID
	}
	return repo.UpsertSessionMonitorLink(ctx, input)
}

func (s *Service) ApplySessionMonitorConfiguration(ctx context.Context, input mysqlstore.SessionMonitorConfigurationInput) (mysqlstore.SessionMonitorConfigurationResult, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionMonitorConfigurationResult{}, err
	}
	repo, ok := s.repo.(sessionMonitorRepository)
	if !ok {
		return mysqlstore.SessionMonitorConfigurationResult{}, mysqlstore.ErrInvalidState
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = resolved.UserID
	}
	return repo.ApplySessionMonitorConfiguration(ctx, input)
}

func (s *Service) ListSessions(ctx context.Context, limit int) ([]mysqlstore.Session, error) {
	s.log(ctx, "tenant.session.list", "tenant.Service.ListSessions", "list tenant user sessions")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSessions(ctx, resolved.TenantID, resolved.UserID, limit)
}

func (s *Service) GetSession(ctx context.Context, sessionID uint64) (mysqlstore.Session, error) {
	s.log(ctx, "tenant.session.get", "tenant.Service.GetSession", "get tenant session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Session{}, err
	}
	return s.repo.GetSession(ctx, resolved.TenantID, resolved.UserID, sessionID)
}

// GetSessionByKey resolves the request scope before using a session key as an
// external reference. It is intentionally separate from GetSession, whose id
// is an internal transport detail.
func (s *Service) GetSessionByKey(ctx context.Context, sessionKey string) (mysqlstore.Session, error) {
	s.log(ctx, "tenant.session.get_by_key", "tenant.Service.GetSessionByKey", "get tenant session by key")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Session{}, err
	}
	return s.repo.GetSessionByKey(ctx, resolved.TenantID, resolved.UserID, sessionKey)
}

func (s *Service) UpdateSession(ctx context.Context, sessionID uint64, req SessionRequest) error {
	s.log(ctx, "tenant.session.update", "tenant.Service.UpdateSession", "update tenant session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.UpdateSession(ctx, mysqlstore.SessionInput{
		ID:           sessionID,
		TenantID:     resolved.TenantID,
		UserID:       resolved.UserID,
		Title:        req.Title,
		Status:       req.Status,
		Model:        req.Model,
		CWD:          req.CWD,
		MetadataJSON: req.MetadataJSON,
	})
}

func (s *Service) ArchiveSession(ctx context.Context, sessionID uint64) error {
	s.log(ctx, "tenant.session.archive", "tenant.Service.ArchiveSession", "archive tenant session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.ArchiveSession(ctx, resolved.TenantID, resolved.UserID, sessionID)
}

func (s *Service) UpsertMessage(ctx context.Context, req MessageRequest) (uint64, error) {
	s.log(ctx, "tenant.message.upsert", "tenant.Service.UpsertMessage", "upsert tenant session message")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	if _, err := s.repo.GetSession(ctx, resolved.TenantID, resolved.UserID, req.SessionID); err != nil {
		return 0, err
	}
	input := messageInput(req)
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	return s.repo.UpsertMessage(ctx, input)
}

// SaveQueryTurn 把会话行与两条消息交给存储层一次写完，由存储层用一个事务保证
// 要么全成功要么全回滚 —— 服务端不再分三次写出半写会话。
func (s *Service) SaveQueryTurn(ctx context.Context, req QueryTurnRequest) (QueryTurnResult, error) {
	s.log(ctx, "tenant.session.turn.save", "tenant.Service.SaveQueryTurn", "save tenant query turn")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return QueryTurnResult{}, err
	}
	out, err := s.repo.SaveQueryTurn(ctx, mysqlstore.QueryTurnInput{
		Session: mysqlstore.SessionInput{
			TenantID:     resolved.TenantID,
			UserID:       resolved.UserID,
			SessionKey:   req.Session.SessionKey,
			Title:        req.Session.Title,
			Status:       req.Session.Status,
			Model:        req.Session.Model,
			CWD:          req.Session.CWD,
			MetadataJSON: req.Session.MetadataJSON,
		},
		User:      messageInput(req.User),
		Assistant: messageInput(req.Assistant),
	})
	if err != nil {
		return QueryTurnResult{}, err
	}
	return QueryTurnResult(out), nil
}

// ForkSession 把分支会话行与它的全部消息交给存储层一次写完，由存储层用一个事务
// 保证要么全成功要么全回滚 —— 服务端不再先建会话再逐条复制、失败时留下半条分支。
func (s *Service) ForkSession(ctx context.Context, req ForkSessionRequest) (ForkSessionResult, error) {
	s.log(ctx, "tenant.session.fork", "tenant.Service.ForkSession", "fork tenant session")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return ForkSessionResult{}, err
	}
	messages := make([]mysqlstore.MessageInput, 0, len(req.Messages))
	for _, message := range req.Messages {
		messages = append(messages, messageInput(message))
	}
	out, err := s.repo.ForkSession(ctx, mysqlstore.ForkSessionInput{
		Session: mysqlstore.SessionInput{
			TenantID:     resolved.TenantID,
			UserID:       resolved.UserID,
			SessionKey:   req.Session.SessionKey,
			Title:        req.Session.Title,
			Status:       req.Session.Status,
			Model:        req.Session.Model,
			CWD:          req.Session.CWD,
			MetadataJSON: req.Session.MetadataJSON,
		},
		Messages: messages,
	})
	if err != nil {
		return ForkSessionResult{}, err
	}
	return ForkSessionResult(out), nil
}

func messageInput(req MessageRequest) mysqlstore.MessageInput {
	return mysqlstore.MessageInput{
		SessionID:   req.SessionID,
		TurnIndex:   req.TurnIndex,
		Role:        req.Role,
		Content:     req.Content,
		ContentJSON: req.ContentJSON,
		ToolID:      req.ToolID,
		ToolName:    req.ToolName,
		IsError:     req.IsError,
		Model:       req.Model,
		InputTokens: req.InputTokens,
		OutputToken: req.OutputToken,
		TraceID:     req.TraceID,
	}
}

func (s *Service) ListMessages(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.Message, error) {
	s.log(ctx, "tenant.message.list", "tenant.Service.ListMessages", "list tenant session messages")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListMessages(ctx, resolved.TenantID, resolved.UserID, sessionID, limit)
}

// ListAllMessages 取一个会话的全部消息，超过 maxRows 时报 ErrTooManyMessages。
// 需要「完整的会话」而不是「一页会话」的调用方（fork）必须走这条，
// 因为 ListMessages 的 limit 会被静默夹取。
func (s *Service) ListAllMessages(ctx context.Context, sessionID uint64, maxRows int) ([]mysqlstore.Message, error) {
	s.log(ctx, "tenant.message.list_all", "tenant.Service.ListAllMessages", "list every tenant session message")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListAllMessages(ctx, resolved.TenantID, resolved.UserID, sessionID, maxRows)
}

// MaxMessageTurn 返回会话里最大的 turn_index，用来分配下一轮的轮次。
func (s *Service) MaxMessageTurn(ctx context.Context, sessionID uint64) (uint, error) {
	s.log(ctx, "tenant.message.max_turn", "tenant.Service.MaxMessageTurn", "read the highest tenant session message turn")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.MaxMessageTurn(ctx, resolved.TenantID, resolved.UserID, sessionID)
}

// GetMessage 按 id 取一条消息。只要一条消息的调用方（cancel、regenerate）走这条，
// 不要拿 ListMessages 的返回切片线性查找 —— 那个 limit 会被静默夹取。
func (s *Service) GetMessage(ctx context.Context, sessionID, messageID uint64) (mysqlstore.Message, error) {
	s.log(ctx, "tenant.message.get", "tenant.Service.GetMessage", "read one tenant session message by id")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Message{}, err
	}
	return s.repo.GetMessage(ctx, resolved.TenantID, resolved.UserID, sessionID, messageID)
}

// PreviousUserMessage 取 beforeTurn 之前最后一条 user 消息，供 regenerate 取提示用。
func (s *Service) PreviousUserMessage(ctx context.Context, sessionID uint64, beforeTurn uint) (mysqlstore.Message, error) {
	s.log(ctx, "tenant.message.previous_user", "tenant.Service.PreviousUserMessage", "read the user message before a turn")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Message{}, err
	}
	return s.repo.PreviousUserMessage(ctx, resolved.TenantID, resolved.UserID, sessionID, beforeTurn)
}

// MessageByKey 按 (role, message_key) 定点取一条消息，供 message_key 的幂等重放用。
// 「查不到」是这条路径的常态（第一次请求不是重放），所以用 found 而不是 ErrNotFound。
func (s *Service) MessageByKey(ctx context.Context, sessionID uint64, role, messageKey string) (mysqlstore.Message, bool, error) {
	s.log(ctx, "tenant.message.by_key", "tenant.Service.MessageByKey", "read one tenant session message by message key")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.Message{}, false, err
	}
	return s.repo.MessageByKey(ctx, resolved.TenantID, resolved.UserID, sessionID, role, messageKey)
}

// ListRecentMessages 取会话最近的 limit 条消息（按 turn_index 升序返回）。
// 要求「最新的什么」的调用方走这条：ListMessages 是升序 + 夹取，求出来的是
// 「最旧 500 条里的最新」。
func (s *Service) ListRecentMessages(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.Message, error) {
	s.log(ctx, "tenant.message.list_recent", "tenant.Service.ListRecentMessages", "list the most recent tenant session messages")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListRecentMessages(ctx, resolved.TenantID, resolved.UserID, sessionID, limit)
}

func (s *Service) CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error) {
	s.log(ctx, "tenant.agent_task.create", "tenant.Service.CreateAgentTask", "create tenant agent task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	if input.ParentSessionID != 0 {
		if _, err := s.repo.GetSession(ctx, resolved.TenantID, resolved.UserID, input.ParentSessionID); err != nil {
			return 0, err
		}
	}
	return s.repo.CreateAgentTask(ctx, input)
}

func (s *Service) CreateSessionControlRun(ctx context.Context, input mysqlstore.SessionControlRunInput) (mysqlstore.SessionControlRunResult, error) {
	s.log(ctx, "tenant.session_control.run.create", "tenant.Service.CreateSessionControlRun", "create managed session run")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlRunResult{}, err
	}
	input.Task.TenantID, input.Task.UserID = resolved.TenantID, resolved.UserID
	return s.repo.CreateSessionControlRun(ctx, input)
}

func (s *Service) StartSessionControlRun(ctx context.Context, taskID uint64) (mysqlstore.SessionControlRunStartResult, error) {
	s.log(ctx, "tenant.session_control.run.start", "tenant.Service.StartSessionControlRun", "claim managed session run")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlRunStartResult{}, err
	}
	return s.repo.StartSessionControlRun(ctx, resolved.TenantID, resolved.UserID, taskID)
}

func (s *Service) FailSessionControlRun(ctx context.Context, input mysqlstore.SessionControlRunFailureInput) (mysqlstore.SessionControlRunFailureResult, error) {
	s.log(ctx, "tenant.session_control.run.fail", "tenant.Service.FailSessionControlRun", "fail unlaunched managed session run")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlRunFailureResult{}, err
	}
	input.TenantID, input.UserID = resolved.TenantID, resolved.UserID
	return s.repo.FailSessionControlRun(ctx, input)
}

func (s *Service) GetAgentTaskByIdempotencyKey(ctx context.Context, idempotencyKey string) (mysqlstore.AgentTask, error) {
	s.log(ctx, "tenant.agent_task.get_by_idempotency", "tenant.Service.GetAgentTaskByIdempotencyKey", "get tenant agent task by idempotency key")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTask{}, err
	}
	return s.repo.GetAgentTaskByIdempotencyKey(ctx, resolved.TenantID, resolved.UserID, idempotencyKey)
}

func (s *Service) UpsertSessionLink(ctx context.Context, input mysqlstore.SessionLinkInput) (uint64, error) {
	s.log(ctx, "tenant.session_link.upsert", "tenant.Service.UpsertSessionLink", "upsert tenant session link")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	if input.CreatedByUserID == 0 {
		input.CreatedByUserID = resolved.UserID
	}
	return s.repo.UpsertSessionLink(ctx, input)
}

func (s *Service) GetSessionLink(ctx context.Context, targetSessionID uint64, sourceKind, sourceSessionKey, relationType string) (mysqlstore.SessionLink, error) {
	s.log(ctx, "tenant.session_link.get", "tenant.Service.GetSessionLink", "get tenant session link")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionLink{}, err
	}
	return s.repo.GetSessionLink(ctx, resolved.TenantID, resolved.UserID, targetSessionID, sourceKind, sourceSessionKey, relationType)
}

func (s *Service) ListSessionLinks(ctx context.Context, targetSessionID uint64, limit int) ([]mysqlstore.SessionLink, error) {
	s.log(ctx, "tenant.session_link.list", "tenant.Service.ListSessionLinks", "list tenant session links")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListSessionLinks(ctx, resolved.TenantID, resolved.UserID, targetSessionID, limit)
}

func (s *Service) FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error {
	s.log(ctx, "tenant.agent_task.finish", "tenant.Service.FinishAgentTask", "finish tenant agent task")
	if _, err := s.ResolveContext(ctx); err != nil {
		return err
	}
	return s.repo.FinishAgentTask(ctx, taskID, status, resultJSON)
}

func (s *Service) CancelAgentTask(ctx context.Context, taskID uint64, resultJSON string) error {
	s.log(ctx, "tenant.agent_task.cancel", "tenant.Service.CancelAgentTask", "cancel tenant agent task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	if resultJSON == "" {
		resultJSON = agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{Source: "tenant_service"})
	}
	if err := s.repo.CancelAgentTask(ctx, resolved.TenantID, resolved.UserID, taskID, resultJSON); err != nil {
		return err
	}
	_, err = s.repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{
		TenantID:    resolved.TenantID,
		UserID:      resolved.UserID,
		TaskID:      taskID,
		EventType:   agenttasks.EventCancelled,
		PayloadJSON: resultJSON,
		TraceID:     observability.TraceID(ctx),
	})
	return err
}

// CancelAgentTaskIfRunning preserves a concurrent runner's terminal result.
// Unlike CancelAgentTask, it deliberately has no event side effect: the
// session-control adapter owns the exact cancellation/audit ordering.
func (s *Service) CancelAgentTaskIfRunning(ctx context.Context, taskID uint64, resultJSON string) (bool, error) {
	s.log(ctx, "tenant.agent_task.cancel_if_running", "tenant.Service.CancelAgentTaskIfRunning", "cancel running tenant agent task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return false, err
	}
	return s.repo.CancelAgentTaskIfRunning(ctx, resolved.TenantID, resolved.UserID, taskID, resultJSON)
}

func (s *Service) CancelAgentTaskForSessionControl(ctx context.Context, req SessionControlStopRequest) (mysqlstore.SessionControlStopResult, error) {
	s.log(ctx, "tenant.session_control.stop", "tenant.Service.CancelAgentTaskForSessionControl", "cancel managed session task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlStopResult{}, err
	}
	return s.repo.CancelAgentTaskForSessionControl(ctx, mysqlstore.SessionControlStopInput{
		TenantID: resolved.TenantID, UserID: resolved.UserID, TaskID: req.TaskID,
		ResultJSON: req.ResultJSON, EventPayloadJSON: req.EventPayloadJSON, TraceID: req.TraceID,
	})
}

func (s *Service) RecoverSessionControlStop(ctx context.Context, sessionID uint64, keyHash string) (mysqlstore.SessionControlStopRecovery, error) {
	s.log(ctx, "tenant.session_control.stop.recover", "tenant.Service.RecoverSessionControlStop", "recover managed session stop")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlStopRecovery{}, err
	}
	return s.repo.RecoverSessionControlStop(ctx, resolved.TenantID, resolved.UserID, sessionID, keyHash)
}

func (s *Service) IsAgentTaskCancelled(ctx context.Context, taskID uint64) (bool, error) {
	s.log(ctx, "tenant.agent_task.cancel_check", "tenant.Service.IsAgentTaskCancelled", "check tenant agent task cancellation")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return false, err
	}
	status, err := s.repo.GetAgentTaskStatus(ctx, resolved.TenantID, resolved.UserID, taskID)
	if err != nil {
		return false, err
	}
	return status == agenttasks.StatusCancelled, nil
}

func (s *Service) GetAgentTask(ctx context.Context, taskID uint64) (mysqlstore.AgentTask, error) {
	s.log(ctx, "tenant.agent_task.get", "tenant.Service.GetAgentTask", "get tenant agent task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTask{}, err
	}
	return s.repo.GetAgentTask(ctx, resolved.TenantID, resolved.UserID, taskID)
}

func (s *Service) UpdateAgentTask(ctx context.Context, taskID uint64, input agenttasks.TaskUpdate) error {
	s.log(ctx, "tenant.agent_task.update", "tenant.Service.UpdateAgentTask", "update tenant agent task")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	return s.repo.UpdateAgentTask(ctx, taskID, input)
}

func (s *Service) ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error) {
	s.log(ctx, "tenant.agent_task.list", "tenant.Service.ListAgentTasks", "list tenant agent tasks")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListAgentTasks(ctx, resolved.TenantID, resolved.UserID, limit)
}

// ListLatestAgentTasksForSessions fetches the current task summary for each
// session through one scoped repository read.
func (s *Service) ListLatestAgentTasksForSessions(ctx context.Context, sessionIDs []uint64) ([]mysqlstore.AgentTask, error) {
	s.log(ctx, "tenant.agent_task.list_latest_for_sessions", "tenant.Service.ListLatestAgentTasksForSessions", "list latest tenant agent tasks for sessions")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListLatestAgentTasksForSessions(ctx, resolved.TenantID, resolved.UserID, sessionIDs)
}

func (s *Service) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	s.log(ctx, "tenant.agent_task_event.append", "tenant.Service.AppendAgentTaskEvent", "append tenant agent task event")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	return s.repo.AppendAgentTaskEvent(ctx, input)
}

func (s *Service) GetAgentTaskEvent(ctx context.Context, eventID uint64) (mysqlstore.AgentTaskEvent, error) {
	s.log(ctx, "tenant.agent_task_event.get", "tenant.Service.GetAgentTaskEvent", "get one tenant agent task event")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AgentTaskEvent{}, err
	}
	return s.repo.GetAgentTaskEvent(ctx, resolved.TenantID, resolved.UserID, eventID)
}

// CreateHandoffLinkAndEvent resolves caller identity once and forwards only
// the authenticated tenant/user scope to the repository transaction.
func (s *Service) CreateHandoffLinkAndEvent(ctx context.Context, input agenttasks.HandoffLinkAndEventInput) (agenttasks.HandoffLinkAndEventResult, error) {
	s.log(ctx, "tenant.session_handoff.persist", "tenant.Service.CreateHandoffLinkAndEvent", "persist tenant session handoff")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return agenttasks.HandoffLinkAndEventResult{}, err
	}
	input.TenantID = resolved.TenantID
	input.UserID = resolved.UserID
	input.CreatedByUserID = resolved.UserID
	return s.repo.CreateHandoffLinkAndEvent(ctx, input)
}

func (s *Service) CreateHandoffBatch(ctx context.Context, input agenttasks.HandoffBatchInput) (agenttasks.HandoffBatchResult, error) {
	s.log(ctx, "tenant.session_handoff.batch.persist", "tenant.Service.CreateHandoffBatch", "persist tenant session handoff batch")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return agenttasks.HandoffBatchResult{}, err
	}
	input.TenantID, input.UserID, input.CreatedByUserID = resolved.TenantID, resolved.UserID, resolved.UserID
	return s.repo.CreateHandoffBatch(ctx, input)
}

func (s *Service) RecoverHandoffBatch(ctx context.Context, input agenttasks.HandoffRecoveryInput) (agenttasks.HandoffRecoveryResult, error) {
	s.log(ctx, "tenant.session_handoff.batch.recover", "tenant.Service.RecoverHandoffBatch", "recover durable tenant session handoff batch")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return agenttasks.HandoffRecoveryResult{}, err
	}
	input.TenantID, input.UserID = resolved.TenantID, resolved.UserID
	return s.repo.RecoverHandoffBatch(ctx, input)
}

func (s *Service) ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return s.ListAgentTaskEventsAfter(ctx, taskID, 0, limit)
}

func (s *Service) ListAgentTaskEventsAfter(ctx context.Context, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.log(ctx, "tenant.agent_task_event.list", "tenant.Service.ListAgentTaskEvents", "list tenant agent task events")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListAgentTaskEventsAfter(ctx, resolved.TenantID, resolved.UserID, taskID, afterID, limit)
}

// ListAgentTaskEventsForTasks 批量取多个任务的事件，供 timeline / 会话详情用。
func (s *Service) ListAgentTaskEventsForTasks(ctx context.Context, taskIDs []uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.log(ctx, "tenant.agent_task_event.list_batch", "tenant.Service.ListAgentTaskEventsForTasks", "list tenant agent task events for tasks")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListAgentTaskEventsForTasks(ctx, resolved.TenantID, resolved.UserID, taskIDs, limit)
}

// ListAgentTaskEventsForTasksComplete returns either every event needed to
// resolve task permissions or an explicit repository safety-limit error.
func (s *Service) ListAgentTaskEventsForTasksComplete(ctx context.Context, taskIDs []uint64) ([]mysqlstore.AgentTaskEvent, error) {
	s.log(ctx, "tenant.agent_task_event.list_complete", "tenant.Service.ListAgentTaskEventsForTasksComplete", "list complete tenant agent task events")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListAgentTaskEventsForTasksComplete(ctx, resolved.TenantID, resolved.UserID, taskIDs)
}

func (s *Service) Create(ctx context.Context, input goal.CreateInput) (goal.Goal, error) {
	return s.CreateGoal(ctx, input)
}

func (s *Service) CreateGoal(ctx context.Context, input goal.CreateInput) (goal.Goal, error) {
	s.log(ctx, "tenant.goal.create", "tenant.Service.CreateGoal", "create tenant goal")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return goal.Goal{}, err
	}
	return s.repo.CreateGoal(ctx, resolved.TenantID, resolved.UserID, input)
}

func (s *Service) Get(ctx context.Context, goalID string) (goal.Goal, error) {
	return s.GetGoal(ctx, goalID)
}

func (s *Service) GetGoal(ctx context.Context, goalID string) (goal.Goal, error) {
	s.log(ctx, "tenant.goal.get", "tenant.Service.GetGoal", "get tenant goal")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return goal.Goal{}, err
	}
	return s.repo.GetGoal(ctx, resolved.TenantID, resolved.UserID, goalID)
}

func (s *Service) List(ctx context.Context, filter goal.ListFilter) ([]goal.Goal, error) {
	return s.ListGoals(ctx, filter, 0)
}

func (s *Service) ListGoals(ctx context.Context, filter goal.ListFilter, limit int) ([]goal.Goal, error) {
	s.log(ctx, "tenant.goal.list", "tenant.Service.ListGoals", "list tenant goals")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListGoals(ctx, resolved.TenantID, resolved.UserID, filter, limit)
}

func (s *Service) Update(ctx context.Context, item goal.Goal) error {
	return s.UpdateGoal(ctx, item)
}

func (s *Service) UpdateGoal(ctx context.Context, item goal.Goal) error {
	s.log(ctx, "tenant.goal.update", "tenant.Service.UpdateGoal", "update tenant goal")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.UpdateGoal(ctx, resolved.TenantID, resolved.UserID, item)
}

func (s *Service) AppendEvent(ctx context.Context, event goal.Event) error {
	return s.AppendGoalEvent(ctx, event)
}

func (s *Service) AppendGoalEvent(ctx context.Context, event goal.Event) error {
	s.log(ctx, "tenant.goal_event.append", "tenant.Service.AppendGoalEvent", "append tenant goal event")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.AppendGoalEvent(ctx, resolved.TenantID, resolved.UserID, event)
}

func (s *Service) ListEvents(ctx context.Context, goalID string, limit int) ([]goal.Event, error) {
	return s.ListGoalEvents(ctx, goalID, limit)
}

func (s *Service) ListGoalEvents(ctx context.Context, goalID string, limit int) ([]goal.Event, error) {
	s.log(ctx, "tenant.goal_event.list", "tenant.Service.ListGoalEvents", "list tenant goal events")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListGoalEvents(ctx, resolved.TenantID, resolved.UserID, goalID, limit)
}

func (s *Service) SavePlan(ctx context.Context, plan goal.GoalPlan) error {
	return s.SaveGoalPlan(ctx, plan)
}

func (s *Service) SaveGoalPlan(ctx context.Context, plan goal.GoalPlan) error {
	s.log(ctx, "tenant.goal_plan.save", "tenant.Service.SaveGoalPlan", "save tenant goal plan")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.SaveGoalPlan(ctx, resolved.TenantID, resolved.UserID, plan)
}

func (s *Service) GetPlan(ctx context.Context, goalID string) (goal.GoalPlan, bool, error) {
	return s.GetGoalPlan(ctx, goalID)
}

func (s *Service) GetGoalPlan(ctx context.Context, goalID string) (goal.GoalPlan, bool, error) {
	s.log(ctx, "tenant.goal_plan.get", "tenant.Service.GetGoalPlan", "get tenant goal plan")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return goal.GoalPlan{}, false, err
	}
	return s.repo.GetGoalPlan(ctx, resolved.TenantID, resolved.UserID, goalID)
}

func (s *Service) AppendEvidence(ctx context.Context, evidence goal.GoalEvidence) error {
	return s.AppendGoalEvidence(ctx, evidence)
}

func (s *Service) AppendGoalEvidence(ctx context.Context, evidence goal.GoalEvidence) error {
	s.log(ctx, "tenant.goal_evidence.append", "tenant.Service.AppendGoalEvidence", "append tenant goal evidence")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	return s.repo.AppendGoalEvidence(ctx, resolved.TenantID, resolved.UserID, evidence)
}

func (s *Service) ListEvidence(ctx context.Context, goalID string, limit int) ([]goal.GoalEvidence, error) {
	return s.ListGoalEvidence(ctx, goalID, limit)
}

func (s *Service) ListGoalEvidence(ctx context.Context, goalID string, limit int) ([]goal.GoalEvidence, error) {
	s.log(ctx, "tenant.goal_evidence.list", "tenant.Service.ListGoalEvidence", "list tenant goal evidence")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	return s.repo.ListGoalEvidence(ctx, resolved.TenantID, resolved.UserID, goalID, limit)
}

func (s *Service) RecordAudit(ctx context.Context, req AuditRequest) (uint64, error) {
	s.log(ctx, "tenant.audit.record", "tenant.Service.RecordAudit", "record tenant audit log")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	return s.repo.InsertAuditLog(ctx, mysqlstore.AuditLogInput{
		TenantID:     resolved.TenantID,
		ActorUserID:  resolved.UserID,
		Action:       req.Action,
		ResourceType: req.ResourceType,
		ResourceID:   req.ResourceID,
		MetadataJSON: req.MetadataJSON,
		TraceID:      preferNonEmpty(req.TraceID, observability.TraceID(ctx)),
	})
}

func (s *Service) RecordSessionControlAudit(ctx context.Context, req SessionControlAuditRequest) (mysqlstore.SessionControlAuditResult, error) {
	s.log(ctx, "tenant.session_control.audit", "tenant.Service.RecordSessionControlAudit", "record managed session audit")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.SessionControlAuditResult{}, err
	}
	return s.repo.InsertSessionControlAudit(ctx, mysqlstore.SessionControlAuditInput{AuditLogInput: mysqlstore.AuditLogInput{
		TenantID: resolved.TenantID, ActorUserID: resolved.UserID, Action: req.Action,
		ResourceType: req.ResourceType, ResourceID: req.ResourceID, MetadataJSON: req.MetadataJSON,
		TraceID: preferNonEmpty(req.TraceID, observability.TraceID(ctx)),
	}})
}

func (s *Service) GetSessionControlAuditByKeyHash(ctx context.Context, action, keyHash string) (mysqlstore.AuditLog, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.AuditLog{}, err
	}
	return s.repo.GetSessionControlAuditByKeyHash(ctx, resolved.TenantID, resolved.UserID, action, keyHash)
}

func (s *Service) ListAuditLogs(ctx context.Context, limit int) ([]mysqlstore.AuditLog, error) {
	s.log(ctx, "tenant.audit.list", "tenant.Service.ListAuditLogs", "list tenant audit logs")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return nil, err
	}
	return s.repo.ListAuditLogs(ctx, resolved.TenantID, limit)
}

func (s *Service) ListAuditLogsPage(ctx context.Context, opts ListOptions) (AuditListResult, error) {
	s.log(ctx, "tenant.audit.list_page", "tenant.Service.ListAuditLogsPage", "list tenant audit logs page")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return AuditListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return AuditListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListAuditLogsFiltered(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return AuditListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return AuditListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) RecordTelemetry(ctx context.Context, event telemetry.Event) (uint64, error) {
	s.log(ctx, "tenant.telemetry.record", "tenant.Service.RecordTelemetry", "record tenant telemetry event", "event_name", event.Name)
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return 0, err
	}
	event.TraceID = preferNonEmpty(event.TraceID, observability.TraceID(ctx))
	event.TenantID = resolved.TenantID
	event.UserID = resolved.UserID
	return s.repo.InsertTelemetryEvent(ctx, mysqlstore.TelemetryEventInput{
		TenantID: resolved.TenantID,
		UserID:   resolved.UserID,
		Event:    event,
	})
}

func (s *Service) ListTelemetryEvents(ctx context.Context, limit int) ([]mysqlstore.TelemetryEvent, error) {
	s.log(ctx, "tenant.telemetry.list", "tenant.Service.ListTelemetryEvents", "list tenant telemetry events")
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return nil, err
	}
	return s.repo.ListTelemetryEvents(ctx, resolved.TenantID, limit)
}

func (s *Service) ListTelemetryEventsPage(ctx context.Context, opts ListOptions) (TelemetryListResult, error) {
	s.log(ctx, "tenant.telemetry.list_page", "tenant.Service.ListTelemetryEventsPage", "list tenant telemetry events page")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return TelemetryListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return TelemetryListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListTelemetryEventsFiltered(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return TelemetryListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return TelemetryListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) GetQuotaConfig(ctx context.Context) (mysqlstore.QuotaConfig, error) {
	s.log(ctx, "tenant.quota.config.get", "tenant.Service.GetQuotaConfig", "get tenant quota config")
	if err := s.RequireRole(ctx, "owner", "admin", "member"); err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	return s.repo.GetTenantQuotaConfig(ctx, resolved.TenantID)
}

func (s *Service) SaveQuotaConfig(ctx context.Context, req QuotaConfigRequest) (mysqlstore.QuotaConfig, error) {
	s.log(ctx, "tenant.quota.config.save", "tenant.Service.SaveQuotaConfig", "save tenant quota config")
	if err := s.RequireRole(ctx, "owner", "admin"); err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	input := mysqlstore.QuotaConfigInput{
		TenantID:              resolved.TenantID,
		QuotaEnabled:          req.QuotaEnabled,
		QPSLimit:              req.QPSLimit,
		DailyTokenLimit:       req.DailyTokenLimit,
		DailyMessageLimit:     req.DailyMessageLimit,
		MaxConcurrentRequests: req.MaxConcurrentRequests,
		Timezone:              req.Timezone,
		ReserveOutputTokens:   req.ReserveOutputTokens,
		Status:                req.Status,
		UpdatedByUserID:       resolved.UserID,
	}
	if err := s.repo.UpsertTenantQuotaConfig(ctx, input); err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	cfg, err := s.repo.GetTenantQuotaConfig(ctx, resolved.TenantID)
	if err != nil {
		return mysqlstore.QuotaConfig{}, err
	}
	metadata, _ := json.Marshal(map[string]any{
		"quota_enabled":           cfg.QuotaEnabled,
		"qps_limit":               cfg.QPSLimit,
		"daily_token_limit":       cfg.DailyTokenLimit,
		"daily_message_limit":     cfg.DailyMessageLimit,
		"max_concurrent_requests": cfg.MaxConcurrentRequests,
	})
	_, _ = s.repo.InsertAuditLog(ctx, mysqlstore.AuditLogInput{
		TenantID:     resolved.TenantID,
		ActorUserID:  resolved.UserID,
		Action:       "quota.config.updated",
		ResourceType: "tenant_quota_config",
		ResourceID:   strconv.FormatUint(resolved.TenantID, 10),
		MetadataJSON: string(metadata),
		TraceID:      observability.TraceID(ctx),
	})
	_, _ = s.repo.InsertQuotaEvent(ctx, mysqlstore.QuotaEvent{
		TenantID:     resolved.TenantID,
		UserID:       resolved.UserID,
		EventType:    "quota.config.updated",
		LimitType:    "config",
		Source:       "tenant-api",
		TraceID:      observability.TraceID(ctx),
		MetadataJSON: string(metadata),
	})
	return cfg, nil
}

func (s *Service) InsertUsageLedger(ctx context.Context, input mysqlstore.UsageLedgerInput) (uint64, error) {
	return s.repo.InsertUsageLedger(ctx, input)
}

func (s *Service) UpdateUsageLedgerSettlement(ctx context.Context, requestID string, input mysqlstore.UsageLedgerInput) error {
	return s.repo.UpdateUsageLedgerSettlement(ctx, requestID, input)
}

func (s *Service) UpsertUsageDailyDelta(ctx context.Context, delta mysqlstore.UsageDailyDelta) error {
	return s.repo.UpsertUsageDailyDelta(ctx, delta)
}

func (s *Service) InsertQuotaEvent(ctx context.Context, event mysqlstore.QuotaEvent) (uint64, error) {
	return s.repo.InsertQuotaEvent(ctx, event)
}

func (s *Service) ListUsageDailyPage(ctx context.Context, opts ListOptions) (UsageDailyListResult, error) {
	s.log(ctx, "tenant.usage.daily.list", "tenant.Service.ListUsageDailyPage", "list tenant usage daily")
	if err := s.RequireRole(ctx, "owner", "admin", "member"); err != nil {
		return UsageDailyListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return UsageDailyListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListTenantUsageDaily(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return UsageDailyListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return UsageDailyListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) ListUsageLedgerPage(ctx context.Context, opts ListOptions) (UsageLedgerListResult, error) {
	s.log(ctx, "tenant.usage.ledger.list", "tenant.Service.ListUsageLedgerPage", "list tenant usage ledger")
	if err := s.RequireRole(ctx, "owner", "admin", "member"); err != nil {
		return UsageLedgerListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return UsageLedgerListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListTenantUsageLedger(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return UsageLedgerListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return UsageLedgerListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) ListQuotaEventsPage(ctx context.Context, opts ListOptions) (QuotaEventListResult, error) {
	s.log(ctx, "tenant.quota.events.list", "tenant.Service.ListQuotaEventsPage", "list tenant quota events")
	if err := s.RequireRole(ctx, "owner", "admin", "member"); err != nil {
		return QuotaEventListResult{}, err
	}
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return QuotaEventListResult{}, err
	}
	limit := normalizePageLimit(opts.Limit)
	items, err := s.repo.ListQuotaEvents(ctx, resolved.TenantID, mysqlstore.ListOptions{Limit: limit + 1, Cursor: opts.Cursor, Search: opts.Search})
	if err != nil {
		return QuotaEventListResult{}, err
	}
	data, next, hasMore := trimPage(items, limit, opts.Cursor)
	return QuotaEventListResult{Data: data, NextCursor: next, HasMore: hasMore}, nil
}

func (s *Service) ReserveTenantQuota(ctx context.Context, store quota.CounterStore, req quota.ReserveRequest) (quota.Reservation, error) {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return quota.Reservation{}, err
	}
	cfg, err := s.repo.GetTenantQuotaConfig(ctx, resolved.TenantID)
	if err != nil {
		return quota.Reservation{}, err
	}
	startedAt := req.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	usageDate, err := quota.UsageDate(startedAt, cfg.Timezone)
	if err != nil {
		return quota.Reservation{}, err
	}
	req.TenantID = resolved.TenantID
	req.UserID = resolved.UserID
	if req.TraceID == "" {
		req.TraceID = observability.TraceID(ctx)
	}
	if req.ReservedOutputTokens == 0 {
		req.ReservedOutputTokens = cfg.ReserveOutputTokens
	}
	if req.RequestID == "" {
		req.RequestID = "quota-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	if cfg.QuotaEnabled && store != nil {
		if err := store.Reserve(ctx, cfg, req, usageDate); err != nil {
			_, _ = s.repo.InsertQuotaEvent(ctx, mysqlstore.QuotaEvent{
				TenantID:  resolved.TenantID,
				UserID:    resolved.UserID,
				RequestID: req.RequestID,
				EventType: quota.RejectionEventType(err),
				LimitType: quota.RejectionLimitType(err),
				Source:    req.Source,
				Route:     req.Route,
				Model:     req.Model,
				TraceID:   req.TraceID,
			})
			_ = s.repo.UpsertUsageDailyDelta(ctx, mysqlstore.UsageDailyDelta{
				TenantID:      resolved.TenantID,
				UsageDate:     usageDate,
				Source:        req.Source,
				Model:         req.Model,
				RejectedCount: 1,
			})
			return quota.Reservation{}, err
		}
	}
	reservation := quota.Reservation{
		RequestID:            req.RequestID,
		TenantID:             resolved.TenantID,
		UserID:               resolved.UserID,
		Source:               req.Source,
		Route:                req.Route,
		Model:                req.Model,
		Provider:             req.Provider,
		Turn:                 req.Turn,
		UsageSource:          req.UsageSource,
		SessionID:            req.SessionID,
		TraceID:              req.TraceID,
		QuotaEnabled:         cfg.QuotaEnabled,
		UsageDate:            usageDate,
		ReservedInputTokens:  req.EstimatedInputTokens,
		ReservedOutputTokens: req.ReservedOutputTokens,
		StartedAt:            startedAt,
	}
	_, _ = s.repo.InsertUsageLedger(ctx, mysqlstore.UsageLedgerInput{
		RequestID:            reservation.RequestID,
		TenantID:             reservation.TenantID,
		UserID:               reservation.UserID,
		SessionID:            reservation.SessionID,
		TraceID:              reservation.TraceID,
		Source:               reservation.Source,
		Route:                reservation.Route,
		Model:                reservation.Model,
		Provider:             reservation.Provider,
		Turn:                 reservation.Turn,
		UsageSource:          reservation.UsageSource,
		Status:               quota.StatusRunning,
		Estimated:            true,
		ReservedInputTokens:  reservation.ReservedInputTokens,
		ReservedOutputTokens: reservation.ReservedOutputTokens,
		StartedAt:            reservation.StartedAt,
	})
	return reservation, nil
}

func (s *Service) SettleTenantQuota(ctx context.Context, store quota.CounterStore, reservation quota.Reservation, usage quota.Usage, status string, runErr error) error {
	if reservation.RequestID == "" {
		return nil
	}
	if status == "" {
		status = quota.StatusSucceeded
	}
	if runErr != nil && status == quota.StatusSucceeded {
		status = quota.StatusFailed
	}
	finishedAt := time.Now().UTC()
	if store != nil {
		_ = store.Settle(ctx, reservation, usage)
	}
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	input := mysqlstore.UsageLedgerInput{
		Status:                   status,
		Estimated:                usage.Estimated,
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheCreationEphemeral1h: usage.CacheCreationEphemeral1h,
		CacheCreationEphemeral5m: usage.CacheCreationEphemeral5m,
		TotalTokens:              usage.TotalTokens(),
		ErrorCode:                quotaErrorCode(runErr),
		ErrorMessage:             errorMessage,
		FinishedAt:               &finishedAt,
	}
	if err := s.repo.UpdateUsageLedgerSettlement(ctx, reservation.RequestID, input); err != nil {
		return err
	}
	return s.repo.UpsertUsageDailyDelta(ctx, mysqlstore.UsageDailyDelta{
		TenantID:                 reservation.TenantID,
		UsageDate:                reservation.UsageDate,
		Source:                   reservation.Source,
		Model:                    preferNonEmpty(reservation.Model, "*"),
		RequestCount:             1,
		MessageCount:             1,
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		TotalTokens:              usage.TotalTokens(),
	})
}

func (s *Service) RequireRole(ctx context.Context, roles ...string) error {
	resolved, err := s.ResolveContext(ctx)
	if err != nil {
		return err
	}
	user, err := s.repo.GetUser(ctx, resolved.TenantID, resolved.UserID)
	if err != nil {
		return err
	}
	if user.Status != "" && user.Status != "active" {
		return ErrForbidden
	}
	for _, role := range roles {
		if user.Role == role {
			return nil
		}
	}
	return ErrForbidden
}

func quotaErrorCode(err error) string {
	if err == nil {
		return ""
	}
	return "request_error"
}

func (s *Service) log(ctx context.Context, action, function, message string, attrs ...any) {
	observability.Info(ctx, s.logger, action, function, message, attrs...)
}

func requireTenantUser(ctx context.Context) (string, string, error) {
	tenantKey := observability.TenantKey(ctx)
	if tenantKey == "" || tenantKey == observability.DefaultTenantKey {
		return "", "", ErrMissingTenantKey
	}
	userKey := observability.UserID(ctx)
	if userKey == "" || userKey == observability.DefaultUserID {
		return "", "", ErrMissingUserID
	}
	return tenantKey, userKey, nil
}

func preferNonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func buildKnowledgeChunks(input mysqlstore.KnowledgeDocumentInput) []mysqlstore.KnowledgeChunkInput {
	const maxChunkChars = 1200
	var chunks []mysqlstore.KnowledgeChunkInput
	var current strings.Builder
	currentChars := 0
	flush := func() {
		text := strings.TrimSpace(current.String())
		current.Reset()
		currentChars = 0
		if text == "" {
			return
		}
		chunks = append(chunks, mysqlstore.KnowledgeChunkInput{
			TenantID:   input.TenantID,
			ChunkIndex: uint(len(chunks)),
			Content:    text,
		})
	}
	for _, paragraph := range strings.Split(input.Content, "\n\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		chars := utf8.RuneCountInString(paragraph)
		if currentChars > 0 && currentChars+chars+2 > maxChunkChars {
			flush()
		}
		for chars > maxChunkChars {
			head, rest := splitAtRune(paragraph, maxChunkChars)
			chunks = append(chunks, mysqlstore.KnowledgeChunkInput{
				TenantID:   input.TenantID,
				ChunkIndex: uint(len(chunks)),
				Content:    strings.TrimSpace(head),
			})
			paragraph = strings.TrimSpace(rest)
			chars = utf8.RuneCountInString(paragraph)
		}
		if paragraph == "" {
			continue
		}
		if currentChars > 0 {
			current.WriteString("\n\n")
			currentChars += 2
		}
		current.WriteString(paragraph)
		currentChars += chars
	}
	flush()
	if len(chunks) == 0 && strings.TrimSpace(input.Content) != "" {
		chunks = append(chunks, mysqlstore.KnowledgeChunkInput{TenantID: input.TenantID, ChunkIndex: 0, Content: strings.TrimSpace(input.Content)})
	}
	return chunks
}

// splitAtRune cuts text after the first n characters. Slicing by byte offset
// instead splits multi-byte runes, which is how CJK documents used to end up
// chunked into replacement characters.
func splitAtRune(text string, n int) (string, string) {
	if n <= 0 {
		return "", text
	}
	count := 0
	for offset := range text {
		if count == n {
			return text[:offset], text[offset:]
		}
		count++
	}
	return text, ""
}

func memoryReviewTargetCategory(candidate mysqlstore.Memory) string {
	type metadata struct {
		Category string `json:"category"`
	}
	var data metadata
	if err := json.Unmarshal([]byte(candidate.MetadataJSON), &data); err == nil {
		if category := strings.TrimSpace(data.Category); category != "" {
			return category
		}
	}
	return "general"
}

func memoryReviewApprovedKey(candidate mysqlstore.Memory) string {
	key := strings.TrimSpace(candidate.MemoryKey)
	switch memoryReviewKind(candidate) {
	case "automem":
		return strings.TrimPrefix(key, "auto.pending.")
	case "explicit-remember":
		return strings.TrimPrefix(key, "explicit.pending.")
	default:
		return strings.TrimPrefix(strings.TrimPrefix(key, "auto.pending."), "explicit.pending.")
	}
}

func memoryReviewMarkerCategory(candidate mysqlstore.Memory, decision string) string {
	prefix := "memory"
	switch memoryReviewKind(candidate) {
	case "automem":
		prefix = "automem"
	case "explicit-remember":
		prefix = "explicit"
	}
	return prefix + "_" + decision
}

func memoryReviewKind(candidate mysqlstore.Memory) string {
	switch strings.ToLower(strings.TrimSpace(candidate.Category)) {
	case "automem_pending", "automem_approved", "automem_rejected", "automem_archived":
		return "automem"
	case "explicit_pending", "explicit_approved", "explicit_rejected", "explicit_archived":
		return "explicit-remember"
	default:
		if strings.HasPrefix(candidate.MemoryKey, "explicit.pending.") {
			return "explicit-remember"
		}
		if strings.HasPrefix(candidate.MemoryKey, "auto.pending.") {
			return "automem"
		}
		return "memory"
	}
}

func memoryReviewMetadata(candidate mysqlstore.Memory, decision string) string {
	data := map[string]any{
		"review_status":      decision,
		"reviewed_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"source_memory_key":  candidate.MemoryKey,
		"source_category":    candidate.Category,
		"source_updated_at":  candidate.UpdatedAt,
		"source_importance":  candidate.Importance,
		"candidate_type":     memoryReviewKind(candidate),
		"source_description": "Memory candidate reviewed by tenant user",
	}
	var original map[string]any
	if err := json.Unmarshal([]byte(candidate.MetadataJSON), &original); err == nil && len(original) > 0 {
		data["candidate_metadata"] = original
	}
	out, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(out)
}

func normalizeReviewLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}

func (s *Service) emitMemoryReviewTelemetry(ctx context.Context, candidate mysqlstore.Memory, decision string, memoryID uint64) {
	name := "memory.remember." + decision
	if memoryReviewKind(candidate) == "automem" {
		name = "memory.automem." + decision
	}
	status := telemetry.StatusOK
	if decision == "rejected" {
		status = telemetry.StatusBlocked
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:       name,
		Category:   telemetry.CategorySession,
		Source:     "tenant.memory_review",
		Status:     status,
		ResourceID: fmt.Sprint(memoryID),
		Properties: map[string]any{
			"memory_key":       candidate.MemoryKey,
			"candidate_type":   memoryReviewKind(candidate),
			"source_category":  candidate.Category,
			"source_memory_id": candidate.ID,
		},
	})
}

func normalizePageLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func trimPage[T any](items []T, limit int, cursor uint64) ([]T, string, bool) {
	if len(items) <= limit {
		return items, "", false
	}
	return items[:limit], fmt.Sprintf("%d", cursor+uint64(limit)), true
}
