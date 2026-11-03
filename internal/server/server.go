package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/agentteam/workflowapp"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/prompttemplate"
	"github.com/konglong87/super-niuma/internal/provisioning"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/quota"
	"github.com/konglong87/super-niuma/internal/scheduler"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	"github.com/konglong87/super-niuma/internal/skills"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
	"github.com/konglong87/super-niuma/internal/tools"
)

type Options struct {
	WorkflowReadOnly  bool
	WorkflowService   *workflowapp.Service
	WorkflowTaskOwner func(context.Context, uint64) (string, bool, error)

	Host             string
	Port             int
	AuthToken        string
	Workspace        string
	WebUIDir         string
	StatusFunc       SnapshotFunc
	ToolsFunc        SnapshotFunc
	SessionsFunc     SnapshotFunc
	LocalSkillsFunc  func(context.Context) ([]skills.Skill, error)
	StreamQueryFunc  StreamQueryFunc
	SessionTitleFunc SessionTitleFunc
	NextStepsFunc    NextStepsFunc

	// ProcessSettingsSnapshot optionally describes configuration captured at startup.
	// The settings endpoint exposes only an allowlist of non-credential fields.
	ProcessSettingsSnapshot map[string]any

	// NextStepsWebEnabled 是 web 端下一步候选生成的开关，零值 false 即关。
	// next_steps 事件晚于 completed 落库，只有实现了「终态后等服务端排空再关流」
	// 契约的客户端收得到（见 appendAgentTaskNextSteps 的注释）；niuma server
	// 自带的 web UI 满足契约，所以 CLI 侧显式置 true。
	NextStepsWebEnabled bool
	MobileJWTSecret     string
	MobileDevAuth       bool
	MobilePolicy        MobilePolicy
	MobileLimiter       *MobileLimiter
	MobileUsageStore    MobileUsageStore
	QuotaStore          quota.CounterStore
	MobileStreams       *MobileStreamRegistry
	MobileUploadBaseURL string
	MobileUploadSigner  MobileUploadSigner
	MediaAssetStore     media.Store
	// ImageGenerator, ImageBlobStore and ImageGenerationStore are optional
	// provider-neutral dependencies for tenant image APIs.
	ImageGenerator        imagegen.Generator
	ImageBlobStore        imagegen.BlobStore
	ImageGenerationStore  ImageGenerationHistoryStore
	ImageCatalog          []config.ResolvedImageModel
	Models                []string
	Providers             []config.ProviderOption
	TelemetryMetrics      *telemetry.MetricsSink
	TelemetrySinks        []telemetry.Sink
	telemetryAsync        *telemetryPump
	TenantService         TenantService
	PromptTemplateService *prompttemplate.Service
	SettingsDatabase      string
	SettingsEnvironments  []SettingsEnvironment
	// SessionControl is the transport-neutral session control boundary. The
	// runtime composition is owned by the server caller, not the HTTP package.
	SessionControl SessionControlService
	// DesktopComputerOwnerLookup is enabled only for the authenticated desktop-local server.
	DesktopComputerOwnerLookup   bool
	DesktopComputerOwnerResolver func(context.Context) (context.Context, sessioncontrol.RequestContext, error)
	// SessionBackend is selected once when the desktop process starts. The
	// server keeps the field transport-neutral so storage implementations can be
	// swapped without changing the WebUI session-control contract.
	SessionBackend        sessioncontrol.SessionBackend
	SessionEvents         SessionEventStore
	SessionControlEvents  SessionControlEventService
	SessionMonitor        sessioncontrol.MonitorPort
	AgentTaskStore        agenttasks.Store
	AgentTaskController   *agenttasks.Controller
	AgentTaskPermissions  *AgentTaskPermissionRegistry
	AgentTaskQuestions    *AgentTaskQuestionRegistry
	AgentTaskRunTimeout   time.Duration
	AgentTaskIdleTimeout  time.Duration
	GoalEvaluator         string
	StructuredSkillRoutes []StructuredSkillRoute
	ProvisioningService   ProvisioningService
	FeishuOnboarding      FeishuOnboardingService

	// AgentTaskMaxConcurrentRuns 限制 detached agent runner 的并发数（<=0 用默认值）。
	AgentTaskMaxConcurrentRuns int
	// SessionControlRunDetached keeps HTTP/tool-started runs alive after the
	// request returns. Direct CLI composition leaves it false and waits so the
	// process cannot exit while its newly created run is still executing.
	SessionControlRunDetached bool
	// SessionControlEnableLocalRead is an explicit host-level opt-in. Tenant
	// authentication alone never grants access to the machine's local transcripts.
	SessionControlEnableLocalRead bool
	SessionControlCWDValidator    func(string) (string, error)
	SessionControlRouteResolver   func(cwd, provider, model string) (string, string, error)
	SessionControlConfigResolver  func(cwd string, requested sessioncontrol.RuntimeConfig) (sessioncontrol.RuntimeConfig, error)
	// PendingInputQueue stores user-confirmed inputs while a session is busy.
	// NewHandler defaults to a bounded in-memory queue; production callers may
	// inject a durable implementation backed by tenant storage.
	PendingInputQueue       pendinginput.Queue
	pendingInputCoordinator *pendingInputCoordinator
	// nextStepsDispatcher is owned by Run/serveHTTPLifecycle. NewHandler leaves
	// it nil unless a test or embedding caller explicitly injects one.
	nextStepsDispatcher *agentTaskNextStepsDispatcher
	// AgentTaskReaperStore 为空则不启动 stale task reaper。
	AgentTaskReaperStore StaleAgentTaskStore
	// MaxRequestBodyBytes 限制单个请求体大小；0 使用路由默认值（通常 10 MiB），负数关闭上限。
	MaxRequestBodyBytes int64
	// ShutdownTimeout 是优雅退出的总预算；0 取默认值 20s。
	ShutdownTimeout time.Duration
	// ReadinessProbes 是 /readyz 逐个执行的依赖探活（MySQL、Redis 等）。为空时
	// /readyz 只反映进程存活，与 /livez 等价。
	ReadinessProbes []ReadinessProbe
	// QueryRateLimitPerMinute 是租户配额之外、按客户端 IP 的兜底限流，只在租户
	// 配额没生效的请求上起作用；0 取默认值 120，负数关闭。见 rate_limit.go。
	QueryRateLimitPerMinute int

	// queryRateLimiter 由 newRouter 按 QueryRateLimitPerMinute 装配，调用方不填。
	queryRateLimiter *clientRateLimiter
}

type SessionControlService interface {
	Create(context.Context, sessioncontrol.CreateRequest) (sessioncontrol.OperationResult, error)
	List(context.Context, sessioncontrol.ListRequest) ([]sessioncontrol.SessionSnapshot, error)
	Get(context.Context, sessioncontrol.GetRequest) (sessioncontrol.SessionSnapshot, error)
	Send(context.Context, sessioncontrol.SendRequest) (sessioncontrol.OperationResult, error)
	Stop(context.Context, sessioncontrol.StopRequest) (sessioncontrol.OperationResult, error)
	Attach(context.Context, sessioncontrol.AttachRequest) (sessioncontrol.OperationResult, error)
	Monitor(context.Context, sessioncontrol.MonitorRequest) (sessioncontrol.OperationResult, error)
}

type SessionControlCompactService interface {
	Compact(context.Context, sessioncontrol.CompactRequest) (sessioncontrol.OperationResult, error)
}

// FeishuOnboardingService is the transport-neutral boundary for the desktop
// QR onboarding flow. The HTTP layer exposes only redacted session state.
type FeishuOnboardingService interface {
	Start(context.Context, uint64, uint64, FeishuOnboardingRequest) (FeishuOnboardingSession, error)
	Get(context.Context, uint64, string) (FeishuOnboardingSession, error)
	Cancel(context.Context, uint64, string) error
	CLIStatus(context.Context) (provisioning.CLIAvailability, error)
	InstallCLI(context.Context) (provisioning.CLIAvailability, error)
}

type SessionControlEventService interface {
	ListAgentTaskEventsAfter(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
}

type TenantService interface {
	ResolveContext(ctx context.Context) (tenantservice.Context, error)
	SaveTenant(ctx context.Context, req tenantservice.TenantRequest) (uint64, error)
	ArchiveTenant(ctx context.Context, tenantKey string) error
	ListTenants(ctx context.Context, limit int) ([]mysqlstore.Tenant, error)
	ListTenantsPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.TenantListResult, error)
	GetCurrentUser(ctx context.Context) (mysqlstore.User, error)
	SaveCurrentUser(ctx context.Context, req tenantservice.UserRequest) (uint64, error)
	SaveTenantUser(ctx context.Context, req tenantservice.UserRequest) (uint64, error)
	ArchiveTenantUser(ctx context.Context, userKey string) error
	ListTenantUsers(ctx context.Context, limit int) ([]mysqlstore.User, error)
	ListTenantUsersPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.UserListResult, error)
	UpsertMemory(ctx context.Context, req tenantservice.MemoryRequest) (uint64, error)
	ListMemories(ctx context.Context, category string, limit int) ([]mysqlstore.Memory, error)
	ListMemoryReviewCandidates(ctx context.Context, limit int) ([]mysqlstore.Memory, error)
	ListMemoryReviewCandidatesFiltered(ctx context.Context, opts tenantservice.MemoryReviewListOptions) ([]mysqlstore.Memory, error)
	ListAutoMemoryCandidates(ctx context.Context, limit int) ([]mysqlstore.Memory, error)
	ReviewMemoryCandidate(ctx context.Context, req tenantservice.MemoryReviewRequest) (uint64, error)
	ReviewAutoMemory(ctx context.Context, req tenantservice.MemoryReviewRequest) (uint64, error)
	UpsertSkill(ctx context.Context, req tenantservice.SkillRequest) (uint64, error)
	RollbackSkill(ctx context.Context, req tenantservice.SkillRollbackRequest) (tenantservice.SkillRollbackResult, error)
	RenderSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error)
	PublishSkillPackage(ctx context.Context, req tenantservice.SkillPackageRequest) (tenantservice.SkillPackageResult, error)
	ListSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.Skill, error)
	GetSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.Skill, error)
	UpsertSkillOverride(ctx context.Context, req tenantservice.SkillOverrideRequest) (uint64, error)
	ListSkillOverrides(ctx context.Context, limit int) ([]mysqlstore.SkillOverride, error)
	GetSkillOverride(ctx context.Context, skillKey string, version uint) (mysqlstore.SkillOverride, error)
	ListEffectiveSkills(ctx context.Context, enabledOnly bool, limit int) ([]mysqlstore.EffectiveSkill, error)
	GetEffectiveSkill(ctx context.Context, skillKey string, version uint) (mysqlstore.EffectiveSkill, error)
	SaveDocument(ctx context.Context, req tenantservice.DocumentRequest) (uint64, error)
	GetActiveDocument(ctx context.Context, docType string) (mysqlstore.Document, error)
	ListDocuments(ctx context.Context, docType string, limit int) ([]mysqlstore.Document, error)
	SaveKnowledgeDocument(ctx context.Context, req tenantservice.KnowledgeDocumentRequest) (uint64, error)
	ListKnowledgeDocuments(ctx context.Context, limit int) ([]mysqlstore.KnowledgeDocument, error)
	SearchKnowledgeChunks(ctx context.Context, req tenantservice.KnowledgeSearchRequest) ([]mysqlstore.KnowledgeChunk, error)
	SaveProfile(ctx context.Context, req tenantservice.ProfileRequest) (uint64, error)
	GetProfile(ctx context.Context, version uint) (mysqlstore.Profile, error)
	UpsertSession(ctx context.Context, req tenantservice.SessionRequest) (uint64, error)
	ListSessions(ctx context.Context, limit int) ([]mysqlstore.Session, error)
	GetSession(ctx context.Context, sessionID uint64) (mysqlstore.Session, error)
	UpdateSession(ctx context.Context, sessionID uint64, req tenantservice.SessionRequest) error
	ArchiveSession(ctx context.Context, sessionID uint64) error
	UpsertMessage(ctx context.Context, req tenantservice.MessageRequest) (uint64, error)
	SaveQueryTurn(ctx context.Context, req tenantservice.QueryTurnRequest) (tenantservice.QueryTurnResult, error)
	ForkSession(ctx context.Context, req tenantservice.ForkSessionRequest) (tenantservice.ForkSessionResult, error)
	ListMessages(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.Message, error)
	ListAllMessages(ctx context.Context, sessionID uint64, maxRows int) ([]mysqlstore.Message, error)
	MaxMessageTurn(ctx context.Context, sessionID uint64) (uint, error)
	GetMessage(ctx context.Context, sessionID, messageID uint64) (mysqlstore.Message, error)
	PreviousUserMessage(ctx context.Context, sessionID uint64, beforeTurn uint) (mysqlstore.Message, error)
	MessageByKey(ctx context.Context, sessionID uint64, role, messageKey string) (mysqlstore.Message, bool, error)
	ListRecentMessages(ctx context.Context, sessionID uint64, limit int) ([]mysqlstore.Message, error)
	CreateAgentTask(ctx context.Context, input agenttasks.TaskInput) (uint64, error)
	FinishAgentTask(ctx context.Context, taskID uint64, status string, resultJSON string) error
	CancelAgentTask(ctx context.Context, taskID uint64, resultJSON string) error
	GetAgentTask(ctx context.Context, taskID uint64) (mysqlstore.AgentTask, error)
	UpdateAgentTask(ctx context.Context, taskID uint64, input agenttasks.TaskUpdate) error
	ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error)
	AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error)
	ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEventsAfter(ctx context.Context, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	ListAgentTaskEventsForTasks(ctx context.Context, taskIDs []uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	CreateGoal(ctx context.Context, input goal.CreateInput) (goal.Goal, error)
	GetGoal(ctx context.Context, goalID string) (goal.Goal, error)
	ListGoals(ctx context.Context, filter goal.ListFilter, limit int) ([]goal.Goal, error)
	UpdateGoal(ctx context.Context, item goal.Goal) error
	AppendGoalEvent(ctx context.Context, event goal.Event) error
	ListGoalEvents(ctx context.Context, goalID string, limit int) ([]goal.Event, error)
	SavePlan(ctx context.Context, plan goal.GoalPlan) error
	GetPlan(ctx context.Context, goalID string) (goal.GoalPlan, bool, error)
	AppendEvidence(ctx context.Context, evidence goal.GoalEvidence) error
	ListEvidence(ctx context.Context, goalID string, limit int) ([]goal.GoalEvidence, error)
	RecordAudit(ctx context.Context, req tenantservice.AuditRequest) (uint64, error)
	ListAuditLogs(ctx context.Context, limit int) ([]mysqlstore.AuditLog, error)
	ListAuditLogsPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.AuditListResult, error)
	RecordTelemetry(ctx context.Context, event telemetry.Event) (uint64, error)
	ListTelemetryEvents(ctx context.Context, limit int) ([]mysqlstore.TelemetryEvent, error)
	ListTelemetryEventsPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.TelemetryListResult, error)
	GetQuotaConfig(ctx context.Context) (mysqlstore.QuotaConfig, error)
	SaveQuotaConfig(ctx context.Context, req tenantservice.QuotaConfigRequest) (mysqlstore.QuotaConfig, error)
	ListUsageDailyPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.UsageDailyListResult, error)
	ListUsageLedgerPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.UsageLedgerListResult, error)
	ListQuotaEventsPage(ctx context.Context, opts tenantservice.ListOptions) (tenantservice.QuotaEventListResult, error)
	ReserveTenantQuota(ctx context.Context, store quota.CounterStore, req quota.ReserveRequest) (quota.Reservation, error)
	SettleTenantQuota(ctx context.Context, store quota.CounterStore, reservation quota.Reservation, usage quota.Usage, status string, err error) error
	RequireRole(ctx context.Context, roles ...string) error
}

type QueryRequest struct {
	Prompt                  string            `json:"prompt"`
	Model                   string            `json:"model,omitempty"`
	Provider                string            `json:"provider,omitempty"`
	CWD                     string            `json:"cwd,omitempty"`
	SessionKey              string            `json:"session_key,omitempty"`
	SystemPrompt            string            `json:"system_prompt,omitempty"`
	PromptMode              string            `json:"prompt_mode,omitempty"`
	PermissionMode          string            `json:"-"`
	Effort                  string            `json:"-"`
	ProfileID               string            `json:"profile_id,omitempty"`
	ProfileVersion          uint              `json:"profile_version,omitempty"`
	ProfileOverrides        map[string]any    `json:"profile_overrides,omitempty"`
	ProfileSurface          string            `json:"profile_surface,omitempty"`
	ProfileSource           string            `json:"profile_source,omitempty"`
	ProfileRequestedHash    string            `json:"profile_requested_hash,omitempty"`
	ProfileEffectiveHash    string            `json:"profile_effective_hash,omitempty"`
	ProfileBlockedOverrides int               `json:"profile_blocked_override_count,omitempty"`
	MaxTokens               int               `json:"max_tokens,omitempty"`
	MaxTurns                int               `json:"-"`
	Attachments             []QueryAttachment `json:"attachments,omitempty"`

	TenantSessionID          uint64                   `json:"-"`
	TenantID                 uint64                   `json:"-"`
	UserID                   uint64                   `json:"-"`
	InitialMessages          []anthropic.MessageParam `json:"-"`
	TraceID                  string                   `json:"-"`
	DisableTenantPersistence bool                     `json:"-"`
	DisableTools             bool                     `json:"-"`
	SkipAutoTitle            bool                     `json:"-"`
	InlineTenantSkills       []string                 `json:"-"`
	InlineTenantSkillSource  string                   `json:"-"`
	ResponseFormat           *OpenAIResponseFormat    `json:"-"`
	StructuredRetryAttempt   int                      `json:"-"`
}

type QueryAttachment struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Transcript   string `json:"transcript,omitempty"`
	InlineData   string `json:"inline_data,omitempty"`
}

type SkillPackageVerifyRequest struct {
	SkillKey              string `json:"skill_key"`
	SchemaName            string `json:"schema_name"`
	ExpectedPackageSHA256 string `json:"expected_package_sha256,omitempty"`
	ExpectedVersion       uint   `json:"expected_version,omitempty"`
	Model                 string `json:"model,omitempty"`
	Prompt                string `json:"prompt,omitempty"`
}

type SkillPackageVerifyResponse struct {
	OK            bool                         `json:"ok"`
	TraceID       string                       `json:"trace_id,omitempty"`
	Error         string                       `json:"error,omitempty"`
	TenantRuntime *openAITenantRuntimeMetadata `json:"tenant_runtime,omitempty"`
}

type QueryFunc func(ctx context.Context, req QueryRequest) (query.Result, error)
type StreamQueryFunc func(ctx context.Context, req QueryRequest, textSink io.Writer) (query.Result, error)
type SessionTitleFunc func(ctx context.Context, prompt, response string) (string, error)

// NextStepsFunc generates follow-up prompt suggestions for a finished turn.
// It is injected rather than called directly so the server package keeps no
// provider dependency, mirroring SessionTitleFunc. toolNames carries the
// turn's tool names, matching the context the TUI path already forwards.
// provider must be forwarded to the meta-call's QueryRequest: without it the
// call routes to the default provider while still carrying another
// provider's model id.
type NextStepsFunc func(ctx context.Context, prompt, response string, toolNames []string, model, provider string, count int) ([]string, error)
type SnapshotFunc func(ctx context.Context) (any, error)

type AgentTaskEventSink interface {
	OnThinking(ctx context.Context, text string) error
	OnToolCall(ctx context.Context, event query.ToolCallEvent) error
	OnToolResult(ctx context.Context, trace query.ToolTrace) error
	OnUsage(ctx context.Context, turn int, usage query.Usage) error
	OnMessageStop(ctx context.Context, turn int, stopReason string, usage query.Usage) error
	OnCompact(ctx context.Context, result compact.Result) error
	OnPermissionRequest(ctx context.Context, req tools.PermissionPromptRequest) (tools.PermissionPromptResponse, error)
	OnNestedAgentProgress(ctx context.Context, event agenttasks.EventInput) error
}

// AgentTaskUserQuestionSink is optional so existing event consumers retain
// their contract while interactive Web runs can await an explicit answer.
type AgentTaskUserQuestionSink interface {
	OnUserQuestion(context.Context, tools.UserQuestionRequest) (tools.UserQuestionResponse, error)
}

func Run(ctx context.Context, opts Options, queryFn QueryFunc) error {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	// 绑定安全检查必须排在所有副作用之前：拒绝启动就不该已经拉起 scheduler daemon
	// 或占住端口。见 validateServerBind（AUDIT-P1-21）。
	if err := validateServerBind(opts); err != nil {
		return err
	}
	scheduleStore := scheduler.DefaultStore()
	if executable, err := os.Executable(); err == nil {
		if _, _, err := scheduleStore.EnsureDaemon(executable, os.Environ()); err != nil {
			observability.Error(ctx, nil, "scheduler.error", "server.Run", "start local scheduler daemon failed", "error", err)
		}
	} else {
		observability.Error(ctx, nil, "scheduler.error", "server.Run", "resolve executable for scheduler daemon failed", "error", err)
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if opts.nextStepsDispatcher == nil {
		opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcher(ctx, opts)
	}
	// reaper 的 stop 必须在 serveHTTPLifecycle 的每一条返回路径上都跑到，包括
	// Serve 因 listener 错误返回（父 ctx 仍然存活）那条。stop 自带 cancel，不依赖
	// 父 ctx，见 startAgentTaskReaper。
	defer startAgentTaskReaper(ctx, opts)()
	return serveHTTPLifecycle(ctx, listener, opts, queryFn)
}

func NewHandler(opts Options, queryFn QueryFunc) http.Handler {
	handler, _ := newHandlerWithStreams(opts, queryFn)
	return handler
}

// newHandlerWithStreams 额外交出 streamRegistry，让 shutdown 能主动终止 SSE /
// WebSocket 这类永远不会变 idle 的连接。
func newHandlerWithStreams(opts Options, queryFn QueryFunc) (http.Handler, *streamRegistry) {
	preparePendingInputRuntime(context.Background(), &opts, queryFn)
	router := newRouter(opts, queryFn)
	streams := newStreamRegistry()
	// The /api prefix compat shim follows the *resolved* build directory: an
	// auto-discovered web/dist serves a UI whose fetches all go through /api, so
	// gating the shim on the raw option would break exactly that case.
	return requestGuardHandler(webUIAPIPrefixHandler(router, resolveWebUIDir(opts.WebUIDir)), streams, opts), streams
}

// newRouter builds the route table. Kept separate from the guard/static wrapping
// above so the full set of registered routes stays enumerable — swagger coverage
// is asserted against it (AUDIT-P0-18).
func newRouter(opts Options, queryFn QueryFunc) *gin.Engine {
	preparePendingInputRuntime(context.Background(), &opts, queryFn)
	if opts.MobileUsageStore == nil && opts.MobileLimiter != nil {
		opts.MobileUsageStore = opts.MobileLimiter
	}
	if opts.MobileUsageStore == nil {
		opts.MobileLimiter = NewMobileLimiter(time.Now)
		opts.MobileUsageStore = opts.MobileLimiter
	}
	if opts.QuotaStore == nil {
		opts.QuotaStore = quota.NewMemoryStore(time.Now)
	}
	if opts.MobileStreams == nil {
		opts.MobileStreams = NewMobileStreamRegistry()
	}
	if opts.TelemetryMetrics == nil {
		opts.TelemetryMetrics = telemetry.NewMetricsSink()
	}
	if opts.AgentTaskPermissions == nil {
		opts.AgentTaskPermissions = NewAgentTaskPermissionRegistry()
	}
	if opts.AgentTaskQuestions == nil {
		opts.AgentTaskQuestions = NewAgentTaskQuestionRegistry()
	}
	// 无租户上下文请求的兜底限流（AUDIT-P1-27）。装在这里而不是 Options 上，是为了
	// 让每个 handler 闭包共享同一个计数器实例。
	opts.queryRateLimiter = newClientRateLimiter(opts.QueryRateLimitPerMinute, time.Now)
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.MaxMultipartMemory = defaultMaxMultipartMemory
	router.Use(gin.Recovery())
	router.Use(requestid.New(requestid.WithCustomHeaderStrKey("X-Trace-Id")))
	router.Use(requestLogMiddleware(opts))
	// /livez 与 /readyz 不鉴权，供 k8s probe 与 LB 使用；/health 保持原样（带 token、
	// 含 workspace）。见 health.go 的说明（AUDIT-P1-22）。
	router.Any("/livez", gin.WrapF(livenessHandler()))
	router.Any("/readyz", gin.WrapF(readinessHandler(opts)))
	router.Any("/health", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "health", "server.NewHandler.health", "check health")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "workspace": opts.Workspace})
	}))
	router.Any("/status", gin.WrapF(snapshotHandler(opts.AuthToken, opts.StatusFunc, map[string]any{"workspace": opts.Workspace})))
	router.Any("/metrics", gin.WrapF(metricsHandler(opts.AuthToken, opts.TelemetryMetrics)))
	router.Any("/tools", gin.WrapF(snapshotHandler(opts.AuthToken, opts.ToolsFunc, []any{})))
	router.Any("/sessions", gin.WrapF(snapshotHandler(opts.AuthToken, opts.SessionsFunc, []any{})))
	router.Any("/local/skills", gin.WrapF(localSkillsHandler(opts)))
	registerWebUIRoutes(router, opts)
	registerRuntimeRoutes(router, opts)
	registerSettingsRoutes(router, opts)
	router.GET("/trace", gin.WrapF(traceUIHandler(opts)))
	router.Any("/trace/api/sessions", gin.WrapF(traceAPISessionsHandler(opts)))
	router.Any("/trace/api/sessions/:id/export", gin.WrapF(traceAPIExportHandler(opts)))
	router.Any("/trace/api/sessions/:id", gin.WrapF(traceAPISessionHandler(opts)))
	router.GET("/prompt-dump", gin.WrapF(promptDumpUIHandler(opts)))
	router.Any("/prompt-dump/api/records", gin.WrapF(promptDumpAPIRecordsHandler(opts)))
	router.Any("/tenant/context", gin.WrapF(tenantContextHandler(opts)))
	router.GET("/tenant/session-control/sessions", gin.WrapF(tenantSessionControlSessionsHandler(opts)))
	router.POST("/tenant/session-control/sessions", gin.WrapF(tenantSessionControlSessionsHandler(opts)))
	router.GET("/tenant/session-control/sessions/:source/:id/computer-owner", gin.WrapF(tenantComputerOwnerHandler(opts)))
	router.GET("/tenant/session-control/sessions/:source/:id", gin.WrapF(tenantSessionControlSessionHandler(opts)))
	router.POST("/tenant/session-control/sessions/:source/:id/messages", gin.WrapF(tenantSessionControlMessageHandler(opts)))
	router.POST("/tenant/session-control/sessions/:source/:id/compact", gin.WrapF(tenantSessionControlCompactHandler(opts)))
	router.POST("/tenant/session-control/sessions/:source/:id/stop", gin.WrapF(tenantSessionControlStopHandler(opts)))
	router.POST("/tenant/session-control/sessions/:source/:id/attachments", gin.WrapF(tenantSessionControlAttachHandler(opts)))
	router.POST("/tenant/session-control/sessions/:source/:id/monitors", gin.WrapF(tenantSessionControlMonitorHandler(opts)))
	router.GET("/tenant/session-control/sessions/:source/:id/events/stream", gin.WrapF(tenantSessionControlStreamHandler(opts)))
	router.GET("/tenant/session-control/sessions/:source/:id/conversation", gin.WrapF(tenantSessionConversationHandler(opts)))
	router.POST("/tenant/session-control/conversations/stream", gin.WrapF(tenantSessionConversationsStreamHandler(opts)))
	router.Any("/tenant/tenants", gin.WrapF(tenantTenantsHandler(opts)))
	router.Any("/tenant/user", gin.WrapF(tenantUserHandler(opts)))
	router.Any("/tenant/users", gin.WrapF(tenantUsersHandler(opts)))
	router.Any("/tenant/memories", gin.WrapF(tenantMemoriesHandler(opts)))
	router.Any("/tenant/memory-review/candidates", gin.WrapF(tenantMemoryReviewCandidatesHandler(opts)))
	router.Any("/tenant/memory-review/review", gin.WrapF(tenantMemoryReviewHandler(opts)))
	router.Any("/tenant/automem/candidates", gin.WrapF(tenantAutoMemoryCandidatesHandler(opts)))
	router.Any("/tenant/automem/review", gin.WrapF(tenantAutoMemoryReviewHandler(opts)))
	router.Any("/tenant/team-memory", gin.WrapF(tenantScopedMemoryHandler(opts, "team")))
	router.Any("/tenant/managed-memory", gin.WrapF(tenantScopedMemoryHandler(opts, "managed")))
	router.Any("/tenant/skills", gin.WrapF(tenantSkillsHandler(opts)))
	router.Any("/tenant/skills/rollback", gin.WrapF(tenantSkillRollbackHandler(opts)))
	router.Any("/tenant/skill-packages/import", gin.WrapF(tenantSkillPackageRenderHandler(opts)))
	router.Any("/tenant/skill-packages/render", gin.WrapF(tenantSkillPackageRenderHandler(opts)))
	router.Any("/tenant/skill-packages/publish", gin.WrapF(tenantSkillPackagePublishHandler(opts)))
	router.Any("/tenant/skill-packages/verify-runtime", gin.WrapF(tenantSkillPackageVerifyRuntimeHandler(opts, queryFn)))
	router.Any("/tenant/skill-overrides", gin.WrapF(tenantSkillOverridesHandler(opts)))
	router.Any("/tenant/effective-skills", gin.WrapF(tenantEffectiveSkillsHandler(opts)))
	router.Any("/tenant/documents", gin.WrapF(tenantDocumentsHandler(opts)))
	router.Any("/tenant/knowledge/documents", gin.WrapF(tenantKnowledgeDocumentsHandler(opts)))
	router.Any("/tenant/knowledge/search", gin.WrapF(tenantKnowledgeSearchHandler(opts)))
	router.Any("/tenant/profile", gin.WrapF(tenantProfileHandler(opts)))
	router.Any("/tenant/prompt-templates", gin.WrapF(promptTemplatesHandler(opts)))
	router.Any("/tenant/prompt-templates/:id", gin.WrapF(promptTemplatesHandler(opts)))
	registerAgentProfileRoutes(router, opts)
	registerWorkflowRoutes(router, opts)
	registerSettingsEnvironmentRoutes(router, opts)
	router.Any("/tenant/agent-teams", gin.WrapF(tenantAgentTeamsHandler(opts)))
	router.Any("/tenant/agent-teams/:key", gin.WrapF(tenantAgentTeamsHandler(opts)))
	router.Any("/tenant/agent-teams/:key/validate", gin.WrapF(tenantAgentTeamValidateHandler(opts)))
	router.Any("/tenant/agent-teams/:key/publish", gin.WrapF(func(w http.ResponseWriter, r *http.Request) { tenantAgentTeamLifecycleHandler(opts, "publish")(w, r) }))
	router.Any("/tenant/agent-teams/:key/archive", gin.WrapF(func(w http.ResponseWriter, r *http.Request) { tenantAgentTeamLifecycleHandler(opts, "archive")(w, r) }))
	router.Any("/tenant/agent-teams/:key/rollback", gin.WrapF(tenantAgentTeamRollbackHandler(opts)))
	router.Any("/tenant/agent-teams/:key/members", gin.WrapF(tenantAgentTeamMembersHandler(opts)))
	router.Any("/tenant/agent-teams/:key/bindings", gin.WrapF(tenantAgentTeamBindingsHandler(opts)))
	router.Any("/tenant/agent-teams/:key/runs", gin.WrapF(tenantAgentTeamRunsHandler(opts)))
	router.Any("/tenant/agent-teams/:key/runs/:run_id/events", gin.WrapF(tenantAgentTeamRunTimelineHandler(opts)))
	router.Any("/tenant/agent-teams/:key/runs/:run_id", gin.WrapF(tenantAgentTeamRunHandler(opts)))
	router.Any("/tenant/agent-teams/:key/runs/:run_id/cancel", gin.WrapF(tenantAgentTeamRunHandler(opts)))
	router.Any("/tenant/channel-accounts", gin.WrapF(tenantChannelAccountsHandler(opts)))
	router.Any("/tenant/feishu/onboarding", gin.WrapF(tenantFeishuOnboardingHandler(opts)))
	router.Any("/tenant/feishu/onboarding/cli", gin.WrapF(tenantFeishuOnboardingCLISHandler(opts)))
	router.Any("/tenant/feishu/onboarding/cli/install", gin.WrapF(tenantFeishuOnboardingCLIInstallHandler(opts)))
	router.Any("/tenant/feishu/onboarding/:id", gin.WrapF(tenantFeishuOnboardingSessionHandler(opts)))
	router.Any("/tenant/feishu/onboarding/:id/cancel", gin.WrapF(tenantFeishuOnboardingSessionHandler(opts)))
	router.Any("/tenant/agent-provisionings", gin.WrapF(tenantAgentProvisioningsHandler(opts)))
	router.Any("/tenant/agent-provisionings/overview", gin.WrapF(tenantAgentProvisioningOverviewHandler(opts)))
	router.Any("/tenant/agent-provisionings/:id", gin.WrapF(tenantAgentProvisioningHandler(opts)))
	router.Any("/tenant/agent-provisionings/:id/preflight", gin.WrapF(tenantAgentProvisioningActionHandler(opts, "preflight")))
	router.Any("/tenant/agent-provisionings/:id/logs", gin.WrapF(tenantAgentProvisioningLogsHandler(opts)))
	router.Any("/tenant/agent-provisionings/:id/:action", gin.WrapF(tenantAgentProvisioningActionHandler(opts, "worker")))
	router.Any("/tenant/sessions", gin.WrapF(tenantSessionsHandler(opts)))
	router.Any("/tenant/sessions/:id/timeline", gin.WrapF(tenantSessionTimelineHandler(opts)))
	router.Any("/tenant/sessions/:id/images/generations", gin.WrapF(tenantImageGenerateHandler(opts)))
	router.Any("/tenant/sessions/:id/images/edits", gin.WrapF(tenantImageEditHandler(opts)))
	router.Any("/tenant/sessions/:id/images", gin.WrapF(tenantImageHistoryHandler(opts)))
	router.Any("/tenant/images/capabilities", gin.WrapF(tenantImageCapabilitiesHandler(opts)))
	router.Any("/tenant/sessions/:id", gin.WrapF(tenantSessionHandler(opts)))
	router.Any("/tenant/media/assets/:asset_id", gin.WrapF(tenantImageAssetHandler(opts)))
	router.Any("/tenant/messages", gin.WrapF(tenantMessagesHandler(opts)))
	router.Any("/agent/workspaces/validate", gin.WrapF(agentWorkspaceValidateHandler(opts)))
	router.Any("/agent/slash-commands", gin.WrapF(agentSlashCommandsHandler(opts)))
	router.Any("/tenant/web-agent/conversations", gin.WrapF(tenantWebAgentConversationsHandler(opts)))
	router.Any("/tenant/web-agent/conversations/:id", gin.WrapF(tenantWebAgentConversationHandler(opts)))
	router.Any("/tenant/agent-tasks", gin.WrapF(tenantAgentTasksHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/events/stream", gin.WrapF(tenantAgentTaskEventsStreamHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/events", gin.WrapF(tenantAgentTaskEventsHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/pending-inputs", gin.WrapF(tenantPendingInputsHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/pending-inputs/:input_id", gin.WrapF(tenantPendingInputsHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/pending-inputs/:input_id/:action", gin.WrapF(tenantPendingInputsHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/pending-input-settings", gin.WrapF(tenantPendingInputsHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/message", gin.WrapF(tenantAgentTaskMessageHandler(opts, queryFn)))
	router.Any("/tenant/agent-tasks/:id/cancel", gin.WrapF(tenantAgentTaskCancelHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/permissions/:request_id", gin.WrapF(tenantAgentTaskPermissionHandler(opts)))
	router.Any("/tenant/agent-tasks/:id/questions/:request_id", gin.WrapF(tenantAgentTaskQuestionHandler(opts)))
	router.Any("/tenant/agent-tasks/:id", gin.WrapF(tenantAgentTaskHandler(opts)))
	router.Any("/tenant/goals", gin.WrapF(tenantGoalsHandler(opts)))
	router.Any("/tenant/goals/:id/events", gin.WrapF(tenantGoalEventsHandler(opts)))
	router.Any("/tenant/goals/:id/plan", gin.WrapF(tenantGoalPlanHandler(opts)))
	router.Any("/tenant/goals/:id/evidence", gin.WrapF(tenantGoalEvidenceHandler(opts)))
	router.Any("/tenant/goals/:id/stop", gin.WrapF(tenantGoalStopHandler(opts)))
	router.Any("/tenant/goals/:id/resume", gin.WrapF(tenantGoalResumeHandler(opts)))
	router.Any("/tenant/goals/:id/run", gin.WrapF(tenantGoalRunHandler(opts, queryFn)))
	router.Any("/tenant/goals/:id", gin.WrapF(tenantGoalHandler(opts)))
	router.Any("/tenant/audit", gin.WrapF(tenantAuditHandler(opts)))
	router.Any("/tenant/telemetry", gin.WrapF(tenantTelemetryHandler(opts)))
	router.Any("/tenant/quota/config", gin.WrapF(tenantQuotaConfigHandler(opts)))
	router.Any("/tenant/usage/daily", gin.WrapF(tenantUsageDailyHandler(opts)))
	router.Any("/tenant/usage/ledger", gin.WrapF(tenantUsageLedgerHandler(opts)))
	router.Any("/tenant/quota/events", gin.WrapF(tenantQuotaEventsHandler(opts)))
	registerMobileRoutes(router, opts)
	router.Any("/v1/chat/completions", gin.WrapF(openAIChatHandler(opts, queryFn)))
	router.Any("/v1/models", gin.WrapF(openAIModelsHandler(opts)))
	router.Any("/v1/providers", gin.WrapF(agentProvidersHandler(opts)))
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	router.Any("/query", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "query", "server.NewHandler.query", "run query endpoint")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// 请求体上限由 requestGuardHandler 的 MaxBytesReader 兜住，这里只负责
			// 把它报成 413 而不是含义错误的 400。
			if isRequestBodyTooLarge(err) {
				writeRequestBodyTooLarge(w, maxRequestBodyBytes(opts))
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Prompt == "" {
			http.Error(w, "prompt is required", http.StatusBadRequest)
			return
		}
		if req.CWD == "" {
			req.CWD = opts.Workspace
		}
		reservation, reserved := reserveQueryQuota(r.Context(), opts, quota.SourceQuery, "/query", req)
		if reserved.err != nil {
			writeQuotaHTTPError(w, reserved.err)
			return
		}
		settler := newQuotaSettler(r.Context(), opts, reservation)
		defer settler.settleOnPanic()
		res, err := queryFn(r.Context(), req)
		settler.settle(res, err)
		if err != nil {
			observability.Error(r.Context(), nil, "query.error", "server.NewHandler.query", "query handler failed", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		persistTenantQuery(r.Context(), opts.TenantService, opts.SessionTitleFunc, req, res)
		writeJSON(w, res)
	}))
	return router
}

func preparePendingInputRuntime(ctx context.Context, opts *Options, queryFn QueryFunc) {
	if opts == nil {
		return
	}
	if opts.PendingInputQueue == nil {
		if provider, ok := opts.TenantService.(interface{ PendingInputQueue() pendinginput.Queue }); ok {
			opts.PendingInputQueue = provider.PendingInputQueue()
		}
		if opts.PendingInputQueue == nil {
			opts.PendingInputQueue = pendinginput.NewMemoryQueue()
		}
	}
	if opts.pendingInputCoordinator == nil && opts.TenantService != nil && (queryFn != nil || opts.StreamQueryFunc != nil) {
		opts.pendingInputCoordinator = newPendingInputCoordinator(ctx, *opts, queryFn)
	}
}

// PrepareSessionControlRuntime initializes the long-lived dependencies that a
// Session Control dispatcher captures by value. Call it before composing the
// service so pending inputs, permissions, and next-step lifecycle are shared
// with the HTTP server instead of being replaced in a later Options copy.
func PrepareSessionControlRuntime(ctx context.Context, opts *Options, queryFn QueryFunc) {
	if opts == nil {
		return
	}
	if opts.AgentTaskPermissions == nil {
		opts.AgentTaskPermissions = NewAgentTaskPermissionRegistry()
	}
	if opts.AgentTaskQuestions == nil {
		opts.AgentTaskQuestions = NewAgentTaskQuestionRegistry()
	}
	preparePendingInputRuntime(ctx, opts, queryFn)
	if opts.nextStepsDispatcher == nil {
		opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcher(ctx, *opts)
	}
}

func requestLogMiddleware(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := requestid.Get(c)
		userID := requestUserID(c.Request)
		tenantKey := requestTenantKey(c.Request)
		ctx := observability.WithRequestValues(c.Request.Context(), traceID, userID, tenantKey)
		ctx = telemetry.WithEmitter(ctx, requestTelemetryEmitter(opts, c.Request))
		c.Request = c.Request.WithContext(ctx)
		start := time.Now()
		startAttrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"remote_addr", c.ClientIP(),
		}
		observability.Debug(ctx, nil, c.Request.Method+" "+c.FullPath(), "server.requestLogMiddleware", "api request start", startAttrs...)
		telemetry.Emit(ctx, telemetry.Event{
			Name:       "api.request.started",
			Category:   telemetry.CategoryAPI,
			Source:     "server.requestLogMiddleware",
			Status:     telemetry.StatusStarted,
			ResourceID: c.Request.URL.Path,
			Properties: map[string]any{
				"method":      c.Request.Method,
				"route":       c.FullPath(),
				"remote_addr": c.ClientIP(),
			},
		})
		c.Next()
		status := telemetry.StatusOK
		if c.Writer.Status() >= http.StatusBadRequest {
			status = telemetry.StatusError
		}
		telemetry.Emit(ctx, telemetry.Event{
			Name:       "api.request.finished",
			Category:   telemetry.CategoryAPI,
			Source:     "server.requestLogMiddleware",
			Status:     status,
			ResourceID: c.Request.URL.Path,
			DurationMS: time.Since(start).Milliseconds(),
			Properties: map[string]any{
				"method":         c.Request.Method,
				"route":          c.FullPath(),
				"status":         c.Writer.Status(),
				"response_bytes": c.Writer.Size(),
				"aborted":        c.IsAborted(),
			},
		})
		finishAttrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		}
		if len(c.Errors) > 0 {
			finishAttrs = append(finishAttrs, "gin_errors", c.Errors.String())
		}
		if c.Writer.Status() >= http.StatusInternalServerError {
			observability.Error(ctx, nil, c.Request.Method+" "+c.FullPath(), "server.requestLogMiddleware", "api request finish", finishAttrs...)
			return
		}
		observability.Info(ctx, nil, c.Request.Method+" "+c.FullPath(), "server.requestLogMiddleware", "api request finish", finishAttrs...)
	}
}

func requestTelemetryEmitter(opts Options, r *http.Request) *telemetry.Emitter {
	sinks := []telemetry.Sink{telemetry.LoggerSink{}}
	if opts.TelemetryMetrics != nil {
		sinks = append(sinks, opts.TelemetryMetrics)
	}
	// 写库和外呼的 sink 走 telemetryPump 的异步缓冲，不再计入请求延迟。
	sinks = append(sinks, requestTelemetrySinks(opts, r)...)
	return telemetry.NewEmitter(sinks...)
}

func metricsHandler(authToken string, sink *telemetry.MetricsSink) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "metrics", "server.metricsHandler", "export telemetry metrics")
		// Metrics are operational data but can still reveal tenant traffic shape,
		// so the endpoint follows the same bearer-token gate as admin snapshots.
		if !authorize(w, r, authToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
		if sink != nil {
			_, _ = w.Write(sink.Prometheus())
		}
	}
}

func requestHasTelemetryAuth(opts Options, r *http.Request) bool {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if strings.TrimSpace(opts.AuthToken) != "" && token == opts.AuthToken {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/mobile/") && strings.TrimSpace(opts.MobileJWTSecret) != "" {
		_, err := parseMobileJWT(token, opts.MobileJWTSecret, time.Now())
		return err == nil
	}
	return strings.TrimSpace(opts.AuthToken) == "" && strings.TrimSpace(opts.MobileJWTSecret) == ""
}

func logService(ctx context.Context, action, function, message string) {
	observability.Info(ctx, nil, action, function, message)
}

func persistTenantQuery(ctx context.Context, svc TenantService, titleFunc SessionTitleFunc, req QueryRequest, res query.Result) {
	if svc == nil || !tenantPersistenceRequested(ctx) {
		return
	}
	sessionKey := strings.TrimSpace(req.SessionKey)
	if sessionKey == "" {
		sessionKey = observability.TraceID(ctx)
	}
	if sessionKey == "" {
		sessionKey = "query"
	}
	model := res.Model
	if model == "" {
		model = req.Model
	}
	title := titleFromPrompt(req.Prompt)
	if !req.SkipAutoTitle {
		if optsTitle := sessionTitle(ctx, req.Prompt, res.Response, titleFunc); optsTitle != "" {
			title = optsTitle
		}
	}
	toolID, toolName := firstToolCallMetadata(res.ToolCalls)
	// 会话行 + 用户消息 + 助手消息交给存储层一次写完：存储层用一个事务保证
	// 要么全成功要么全回滚，中途失败不会留下半写会话。
	turn, err := svc.SaveQueryTurn(ctx, tenantservice.QueryTurnRequest{
		Session: tenantservice.SessionRequest{
			SessionKey:   sessionKey,
			Title:        title,
			Model:        model,
			CWD:          req.CWD,
			MetadataJSON: queryProfileMetadataJSON(req),
		},
		User: tenantservice.MessageRequest{
			TurnIndex:   1,
			Role:        "user",
			Content:     req.Prompt,
			Model:       model,
			InputTokens: uint(res.Usage.InputTokens),
			TraceID:     observability.TraceID(ctx),
		},
		Assistant: tenantservice.MessageRequest{
			TurnIndex:   2,
			Role:        "assistant",
			Content:     res.Response,
			ContentJSON: toolCallsContentJSON(res),
			ToolID:      toolID,
			ToolName:    toolName,
			Model:       model,
			OutputToken: uint(res.Usage.OutputTokens),
			TraceID:     observability.TraceID(ctx),
		},
	})
	if err != nil {
		observability.Error(ctx, nil, "tenant.query.persist_error", "server.persistTenantQuery", "persist tenant query turn failed", "error", err)
		return
	}
	// 记忆写的是另一张表，不在这次查询的原子边界内；它依赖上面两个 ID，
	// 所以放在事务提交之后，失败也只影响记忆本身。
	maybeWriteExplicitRememberMemories(ctx, svc, req.Prompt, turn.SessionID, turn.UserMessageID)
	maybeWriteAutoMemories(ctx, svc, req.Prompt, turn.SessionID, turn.UserMessageID)
}

func queryProfileMetadataJSON(req QueryRequest) string {
	if strings.TrimSpace(req.ProfileID) == "" && req.ProfileVersion == 0 && strings.TrimSpace(req.ProfileSurface) == "" {
		return ""
	}
	metadata := map[string]any{
		"profile_key":                    strings.TrimSpace(req.ProfileID),
		"profile_version":                req.ProfileVersion,
		"profile_surface":                strings.TrimSpace(req.ProfileSurface),
		"profile_source":                 strings.TrimSpace(req.ProfileSource),
		"profile_requested_hash":         strings.TrimSpace(req.ProfileRequestedHash),
		"profile_effective_hash":         strings.TrimSpace(req.ProfileEffectiveHash),
		"profile_blocked_override_count": req.ProfileBlockedOverrides,
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func firstToolCallMetadata(calls []query.ToolTrace) (string, string) {
	if len(calls) == 0 {
		return "", ""
	}
	return calls[0].ID, calls[0].Name
}

func toolCallsContentJSON(res query.Result) string {
	if len(res.ToolCalls) == 0 {
		return ""
	}
	data, err := json.Marshal(map[string]any{
		"content":     res.Response,
		"tool_calls":  openAIToolCalls(res.ToolCalls),
		"tool_traces": res.ToolCalls,
	})
	if err != nil {
		return ""
	}
	return string(data)
}

type autoMemoryCandidate struct {
	Category   string
	Content    string
	Confidence float64
}

type explicitRememberCandidate struct {
	Category string
	Content  string
	Risk     string
}

func maybeWriteAutoMemories(ctx context.Context, svc TenantService, prompt string, sessionID, messageID uint64) {
	if svc == nil || !autoMemoryWritebackEnabled() {
		return
	}
	candidates := extractAutoMemoryCandidates(prompt)
	for _, candidate := range candidates {
		metadata, err := json.Marshal(map[string]any{
			"source_session_id": sessionID,
			"source_message_id": messageID,
			"trace_id":          observability.TraceID(ctx),
			"confidence":        candidate.Confidence,
			"category":          candidate.Category,
			"review_status":     "pending",
			"extractor":         "heuristic-v1",
		})
		if err != nil {
			continue
		}
		key := autoMemoryKey(candidate.Category, candidate.Content)
		_, err = svc.UpsertMemory(ctx, tenantservice.MemoryRequest{
			MemoryKey:    "auto.pending." + key,
			Category:     "automem_pending",
			Content:      candidate.Content,
			MetadataJSON: string(metadata),
			Importance:   5,
			Source:       "automem-pending",
		})
		if err != nil {
			observability.Error(ctx, nil, "tenant.automem.persist_error", "server.maybeWriteAutoMemories", "persist auto memory failed", "error", err)
		}
	}
}

func maybeWriteExplicitRememberMemories(ctx context.Context, svc TenantService, prompt string, sessionID, messageID uint64) {
	if svc == nil {
		return
	}
	candidates, rejected := extractExplicitRememberCandidates(prompt)
	for _, item := range rejected {
		emitMemoryTelemetry(ctx, "memory.remember.detected", telemetry.StatusOK, sessionID, messageID, map[string]any{
			"risk_status": item.Risk,
		})
		emitMemoryTelemetry(ctx, "memory.remember.rejected", telemetry.StatusBlocked, sessionID, messageID, map[string]any{
			"reason":       item.Risk,
			"content_hash": contentHash(item.Content),
			"content_len":  len([]rune(item.Content)),
		})
	}
	for _, candidate := range candidates {
		emitMemoryTelemetry(ctx, "memory.remember.detected", telemetry.StatusOK, sessionID, messageID, map[string]any{
			"category":    candidate.Category,
			"risk_status": candidate.Risk,
		})
		metadata, err := json.Marshal(map[string]any{
			"source_session_id": sessionID,
			"source_message_id": messageID,
			"trace_id":          observability.TraceID(ctx),
			"category":          candidate.Category,
			"review_status":     "pending",
			"extractor":         "explicit-remember-heuristic-v1",
			"candidate_type":    "explicit_remember",
			"risk_status":       candidate.Risk,
		})
		if err != nil {
			continue
		}
		key := explicitRememberMemoryKey(candidate.Category, candidate.Content)
		id, err := svc.UpsertMemory(ctx, tenantservice.MemoryRequest{
			MemoryKey:    "explicit.pending." + key,
			Category:     "explicit_pending",
			Content:      candidate.Content,
			MetadataJSON: string(metadata),
			Importance:   explicitRememberImportance(candidate.Category),
			Source:       "explicit-user-remember-pending",
		})
		if err != nil {
			observability.Error(ctx, nil, "tenant.remember.persist_error", "server.maybeWriteExplicitRememberMemories", "persist explicit remember memory failed", "error", err)
			continue
		}
		emitMemoryTelemetry(ctx, "memory.remember.candidate", telemetry.StatusOK, sessionID, messageID, map[string]any{
			"memory_id":   id,
			"memory_key":  "explicit.pending." + key,
			"category":    candidate.Category,
			"risk_status": candidate.Risk,
		})
	}
}

func emitMemoryTelemetry(ctx context.Context, name string, status string, sessionID, messageID uint64, properties map[string]any) {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["source_session_id"] = sessionID
	properties["source_message_id"] = messageID
	telemetry.Emit(ctx, telemetry.Event{
		Name:       name,
		Category:   telemetry.CategorySession,
		Source:     "server.memory",
		Status:     status,
		SessionID:  sessionID,
		ResourceID: strconv.FormatUint(messageID, 10),
		Properties: properties,
	})
}

func autoMemoryWritebackEnabled() bool {
	return truthyEnv("NIUMA_AUTOMEM_WRITEBACK") || truthyEnv("CLAUDE_CODE_AUTOMEM_WRITEBACK")
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func extractAutoMemoryCandidates(prompt string) []autoMemoryCandidate {
	if unsafeAutoMemoryText(prompt) {
		return nil
	}
	var out []autoMemoryCandidate
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "-* \t"))
		if line == "" || unsafeAutoMemoryText(line) {
			continue
		}
		if category, content, ok := classifyAutoMemoryLine(line); ok {
			out = append(out, autoMemoryCandidate{Category: category, Content: content, Confidence: 0.72})
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func extractExplicitRememberCandidates(prompt string) ([]explicitRememberCandidate, []explicitRememberCandidate) {
	var pending []explicitRememberCandidate
	var rejected []explicitRememberCandidate
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(strings.Trim(line, "-* \t"))
		if line == "" {
			continue
		}
		content, ok := explicitRememberContent(line)
		if !ok {
			continue
		}
		content = cleanAutoMemoryContent(content)
		if content == "" {
			continue
		}
		if risk := unsafeMemoryReason(content); risk != "" {
			rejected = append(rejected, explicitRememberCandidate{Content: content, Risk: risk})
			continue
		}
		category := classifyExplicitRememberContent(content)
		pending = append(pending, explicitRememberCandidate{Category: category, Content: content, Risk: "low_risk"})
		if len(pending) >= 5 {
			break
		}
	}
	return pending, rejected
}

func explicitRememberContent(line string) (string, bool) {
	cleaned := strings.TrimSpace(line)
	lower := strings.ToLower(cleaned)
	prefixes := []string{
		"请帮我记住", "帮我记住", "请记住", "记住", "以后记得", "以后请记得", "以后请记住",
		"please remember that", "please remember", "remember that", "remember:", "remember ",
		"keep in mind that", "keep in mind:", "keep in mind ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			content := strings.TrimSpace(string([]rune(cleaned)[len([]rune(prefix)):]))
			content = strings.TrimLeft(content, ":：,， ")
			return content, true
		}
	}
	return "", false
}

func classifyExplicitRememberContent(content string) string {
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(content, "喜欢") || strings.Contains(content, "偏好") || strings.Contains(content, "更喜欢") || strings.Contains(content, "希望") || strings.Contains(lower, "prefer") || strings.Contains(lower, "like"):
		return "preference"
	case strings.Contains(content, "项目使用") || strings.Contains(content, "项目采用") || strings.Contains(content, "技术栈") || strings.Contains(lower, "project uses") || strings.Contains(lower, "we use"):
		return "project_fact"
	case strings.Contains(content, "我是") || strings.Contains(content, "我的") || strings.Contains(lower, "i am") || strings.Contains(lower, "my role"):
		return "user_fact"
	case strings.Contains(content, "以后") || strings.Contains(content, "约定") || strings.Contains(lower, "from now on") || strings.Contains(lower, "convention"):
		return "convention"
	default:
		return "preference"
	}
}

func classifyAutoMemoryLine(line string) (string, string, bool) {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(line, "我喜欢") || strings.Contains(line, "我偏好") || strings.Contains(line, "我更喜欢") || strings.Contains(lower, "i prefer") || strings.Contains(lower, "i like"):
		return "preference", cleanAutoMemoryContent(line), true
	case strings.Contains(line, "请以后") || strings.Contains(line, "以后请") || strings.Contains(lower, "please remember") || strings.Contains(lower, "from now on"):
		return "convention", cleanAutoMemoryContent(line), true
	case strings.Contains(line, "项目使用") || strings.Contains(line, "项目采用") || strings.Contains(line, "技术栈") || strings.Contains(lower, "project uses") || strings.Contains(lower, "we use"):
		return "project_fact", cleanAutoMemoryContent(line), true
	case strings.Contains(line, "约定是") || strings.Contains(line, "团队约定") || strings.Contains(lower, "convention is"):
		return "convention", cleanAutoMemoryContent(line), true
	default:
		return "", "", false
	}
}

func cleanAutoMemoryContent(line string) string {
	line = strings.Join(strings.Fields(line), " ")
	if len([]rune(line)) > 300 {
		line = string([]rune(line)[:300])
	}
	return strings.TrimSpace(line)
}

func unsafeAutoMemoryText(text string) bool {
	return unsafeMemoryReason(text) != ""
}

func unsafeMemoryReason(text string) string {
	lower := strings.ToLower(text)
	if len([]rune(text)) > 1200 {
		return "too_long"
	}
	markers := map[string]string{
		"api_key":           "credential",
		"api key":           "credential",
		"apikey":            "credential",
		"access key":        "credential",
		"authorization:":    "credential",
		"bearer ":           "credential",
		"password":          "credential",
		"secret":            "credential",
		"token":             "credential",
		"jwt":               "credential",
		"private key":       "credential",
		"ignore previous":   "prompt_injection",
		"ignore above":      "prompt_injection",
		"system prompt":     "prompt_injection",
		"developer message": "prompt_injection",
		"jailbreak":         "prompt_injection",
		"越狱":                "prompt_injection",
		"忽略之前":              "prompt_injection",
		"忽略以上":              "prompt_injection",
		"系统提示词":             "prompt_injection",
		"bypass approval":   "permission_bypass",
		"不询问权限":             "permission_bypass",
		"忽略权限":              "permission_bypass",
		"tenant isolation":  "tenant_isolation",
		"跨租户":               "tenant_isolation",
		"管理员数据":             "tenant_isolation",
		"audit bypass":      "audit_bypass",
		"不记录审计":             "audit_bypass",
	}
	for marker, reason := range markers {
		if strings.Contains(lower, marker) {
			if reason != "" {
				return reason
			}
			return "high_risk"
		}
	}
	return ""
}

func autoMemoryKey(category, content string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + content))
	return "auto." + category + "." + hex.EncodeToString(sum[:])[:16]
}

func explicitRememberMemoryKey(category, content string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + content))
	return category + "." + hex.EncodeToString(sum[:])[:16]
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

func explicitRememberImportance(category string) int {
	switch category {
	case "preference":
		return 8
	case "convention":
		return 7
	default:
		return 6
	}
}

func tenantPersistenceRequested(ctx context.Context) bool {
	return observability.TenantKey(ctx) != observability.DefaultTenantKey && observability.UserID(ctx) != observability.DefaultUserID
}

func titleFromPrompt(prompt string) string {
	title := strings.Join(strings.Fields(prompt), " ")
	if len([]rune(title)) <= 80 {
		return title
	}
	return string([]rune(title)[:77]) + "..."
}

func sessionTitle(ctx context.Context, prompt, response string, titleFunc SessionTitleFunc) string {
	if titleFunc == nil {
		return ""
	}
	title, err := titleFunc(ctx, prompt, response)
	if err != nil {
		observability.Debug(ctx, nil, "tenant.session.title", "server.sessionTitle", "session title generation failed", "error", err)
		return ""
	}
	return cleanSessionTitle(title)
}

func cleanSessionTitle(title string) string {
	title = strings.Join(strings.Fields(strings.Trim(title, " \t\r\n\"'`")), " ")
	if title == "" {
		return ""
	}
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:77]) + "..."
	}
	return title
}

func requestUserID(r *http.Request) string {
	for _, header := range userIDHeaderAliases {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return "anonymous"
}

func requestTenantKey(r *http.Request) string {
	for _, header := range []string{"X-Tenant-Key", "X-Claude-Tenant-Key", "X-Workspace-Tenant"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return observability.DefaultTenantKey
}

func requestMobileClaims(r *http.Request) mobileClaims {
	return mobileClaims{
		TenantKey: requestTenantKey(r),
		UserID:    requestUserID(r),
		DeviceID:  strings.TrimSpace(r.Header.Get("X-Device-Id")),
	}
}

func requestSessionKey(r *http.Request) string {
	for _, header := range []string{"X-Session-Key", "X-Claude-Session-Id", "X-Session-Id"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
}

type quotaReserveResult struct {
	err error
}

// isQuotaLimitError 判断错误是否为配额超限(必须 fail-closed 拒绝),
// 以便与存储/基础设施故障(可 fail-open 放行)区分开。
func isQuotaLimitError(err error) bool {
	return errors.Is(err, quota.ErrRateLimited) ||
		errors.Is(err, quota.ErrDailyTokenLimitExceeded) ||
		errors.Is(err, quota.ErrDailyMessageLimitExceeded) ||
		errors.Is(err, quota.ErrConcurrentLimitExceeded)
}

// quotaFailOpen 表示配额存储不可用时是否降级放行。默认 false(fail-closed);
// 仅当运维显式设置 NIUMA_QUOTA_FAIL_OPEN=true 时放行,且必须记录告警。
func quotaFailOpen() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("NIUMA_QUOTA_FAIL_OPEN")), "true")
}

func reserveQueryQuota(ctx context.Context, opts Options, source, route string, req QueryRequest) (quota.Reservation, quotaReserveResult) {
	if opts.TenantService == nil || !tenantPersistenceRequested(ctx) {
		// 没有租户上下文 ⇒ 租户配额整段不适用。此前这里直接放行，于是任何不带
		// X-Tenant-Key 的调用者都不受任何限制（AUDIT-P1-27）。改为落到按客户端
		// 的兜底限流上：它不是配额，只是让「没有配额」不等于「没有上限」。
		if err := opts.queryRateLimiter.allow(clientIPFromContext(ctx)); err != nil {
			observability.Error(ctx, nil, "quota.fallback_rate_limited", "server.reserveQueryQuota", "tenant-less request rate limited", "source", source, "route", route)
			return quota.Reservation{}, quotaReserveResult{err: err}
		}
		return quota.Reservation{}, quotaReserveResult{}
	}
	reservation, err := opts.TenantService.ReserveTenantQuota(ctx, opts.QuotaStore, quota.ReserveRequest{
		RequestID:            "req-" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Source:               source,
		Route:                route,
		Model:                req.Model,
		Provider:             req.Provider,
		SessionID:            req.TenantSessionID,
		TraceID:              observability.TraceID(ctx),
		EstimatedInputTokens: uint64(mobileApproxTokens(req.Prompt) + queryAttachmentTokenEstimate(req.Attachments)),
		ReservedOutputTokens: uint64(maxInt(req.MaxTokens, 0)),
		StartedAt:            time.Now().UTC(),
	})
	if err != nil {
		// 配额超限必须 fail-closed;仅存储/基础设施故障且显式开启时才 fail-open 放行。
		if !isQuotaLimitError(err) && quotaFailOpen() {
			observability.Error(ctx, nil, "quota.fail_open", "server.reserveQueryQuota", "quota store unavailable, failing open", "error", err)
			return quota.Reservation{}, quotaReserveResult{}
		}
		return quota.Reservation{}, quotaReserveResult{err: err}
	}
	return reservation, quotaReserveResult{}
}

func settleQueryQuota(ctx context.Context, opts Options, reservation quota.Reservation, result query.Result, runErr error) {
	if opts.TenantService == nil || reservation.RequestID == "" {
		return
	}
	status := quota.StatusSucceeded
	if errors.Is(runErr, context.Canceled) {
		status = quota.StatusCancelled
	} else if runErr != nil {
		status = quota.StatusFailed
	}
	usage := quotaUsageFromQuery(result.Usage)
	if strings.TrimSpace(result.Model) != "" {
		reservation.Model = result.Model
	}
	reservation.Turn = result.Turns
	reservation.UsageSource = "provider"
	if usage.TotalTokens() == 0 {
		usage.Estimated = true
	}
	if err := opts.TenantService.SettleTenantQuota(ctx, opts.QuotaStore, reservation, usage, status, runErr); err != nil {
		observability.Error(ctx, nil, "quota.settle_error", "server.settleQueryQuota", "settle tenant quota failed", "error", err)
	}
}

// quotaSettler 保证一次 reserve 恰好结算一次,并在 panic 时兜底释放并发计数。
// 没有它时,查询执行 panic 会跳过 Settle,导致 MemoryStore 的 concurrent 计数
// 只增不减、租户被永久锁死(需重启进程才能恢复)。
type quotaSettler struct {
	ctx         context.Context
	opts        Options
	reservation quota.Reservation
	done        bool
}

func newQuotaSettler(ctx context.Context, opts Options, reservation quota.Reservation) *quotaSettler {
	return &quotaSettler{ctx: ctx, opts: opts, reservation: reservation}
}

// settle 正常路径调用,once 语义保证与 settleOnPanic 不重复结算。
func (s *quotaSettler) settle(result query.Result, runErr error) {
	if s == nil || s.done {
		return
	}
	s.done = true
	settleQueryQuota(s.ctx, s.opts, s.reservation, result, runErr)
}

// settleOnPanic 必须以 defer 调用:panic 时先释放配额并发计数,再重新 panic
// 交给上层 gin.Recovery 处理,避免吞掉 panic。
func (s *quotaSettler) settleOnPanic() {
	if rec := recover(); rec != nil {
		s.settle(query.Result{}, fmt.Errorf("panic during query: %v", rec))
		panic(rec)
	}
}

func quotaUsageFromQuery(usage query.Usage) quota.Usage {
	turn := session.NewTurnUsage("", 0, "", "", session.ReportedUsage{
		InputTokens:              usage.InputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreation1hTokens:    usage.CacheCreationEphemeral1hInputTokens,
		CacheCreation5mTokens:    usage.CacheCreationEphemeral5mInputTokens,
	}, false, "provider")
	return quota.UsageFromTurnUsage(turn)
}

func queryAttachmentTokenEstimate(attachments []QueryAttachment) int {
	total := 0
	for _, attachment := range attachments {
		total += mobileApproxTokens(attachment.Transcript)
		if attachment.SizeBytes > 0 {
			total += int(minInt64(attachment.SizeBytes/4096, 2048))
		}
	}
	return total
}

func writeQuotaHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusTooManyRequests
	if errors.Is(err, quota.ErrDailyTokenLimitExceeded) || errors.Is(err, quota.ErrDailyMessageLimitExceeded) {
		status = http.StatusPaymentRequired
	}
	writeTenantError(w, status, err.Error())
}

func writeOpenAIQuotaError(w http.ResponseWriter, err error) {
	status := http.StatusTooManyRequests
	if errors.Is(err, quota.ErrDailyTokenLimitExceeded) || errors.Is(err, quota.ErrDailyMessageLimitExceeded) {
		status = http.StatusPaymentRequired
	}
	writeOpenAIError(w, status, "quota_exceeded", err.Error())
}

func maxInt(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func snapshotHandler(authToken string, fn SnapshotFunc, fallback any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorize(w, r, authToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if fn == nil {
			writeJSON(w, fallback)
			return
		}
		value, err := fn(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, value)
	}
}

func requireTenantRole(w http.ResponseWriter, r *http.Request, opts Options, roles ...string) bool {
	if opts.TenantService == nil {
		writeTenantError(w, http.StatusServiceUnavailable, errMsgTenantStorageNotConfig)
		return false
	}
	if err := opts.TenantService.RequireRole(r.Context(), roles...); err != nil {
		writeTenantServiceError(w, err)
		return false
	}
	return true
}

func recordTenantAudit(ctx context.Context, svc TenantService, action, resourceType string, resourceID any) {
	if svc == nil {
		return
	}
	_, err := svc.RecordAudit(ctx, tenantservice.AuditRequest{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   fmt.Sprint(resourceID),
		TraceID:      observability.TraceID(ctx),
	})
	if err != nil {
		observability.Error(ctx, nil, "tenant.audit.persist_error", "server.recordTenantAudit", "persist tenant audit failed", "error", err)
	}
}

func writeTenantServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, workflow.ErrOwnedTask):
		writeTenantError(w, http.StatusConflict, "This task belongs to a workflow. Control it from the Team run page.")
	case errors.Is(err, tenantservice.ErrMissingTenantKey), errors.Is(err, tenantservice.ErrMissingUserID), errors.Is(err, tenantservice.ErrInvalidRequest):
		writeTenantError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, tenantservice.ErrForbidden):
		writeTenantError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, mysqlstore.ErrNotFound):
		writeTenantError(w, http.StatusNotFound, err.Error())
	default:
		writeTenantError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeTenantError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"error": message})
}

func parseLimit(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return limit
}

func parseUintQuery(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func tenantListOptions(r *http.Request) tenantservice.ListOptions {
	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("search"))
	if search == "" {
		search = strings.TrimSpace(query.Get("q"))
	}
	return tenantservice.ListOptions{
		Limit:  parseLimit(query.Get(paramLimit)),
		Cursor: parseCursor(query.Get("cursor")),
		Search: search,
	}
}

func memoryReviewListOptions(r *http.Request) tenantservice.MemoryReviewListOptions {
	query := r.URL.Query()
	return tenantservice.MemoryReviewListOptions{
		Limit:           parseLimit(query.Get(paramLimit)),
		CandidateType:   strings.TrimSpace(query.Get("candidate_type")),
		RiskStatus:      strings.TrimSpace(query.Get("risk_status")),
		SourceSessionID: parseCursor(query.Get("source_session_id")),
	}
}

func parseCursor(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return cursor
}

func querySkillKey(r *http.Request) string {
	for _, key := range []string{"key", "skill_key"} {
		if value := strings.TrimSpace(r.URL.Query().Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func parseOptionalBool(value string) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, true
	}
	parsed, err := strconv.ParseBool(value)
	return parsed, err == nil
}

func parseOptionalUint(value string) (uint, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.ParseUint(value, 10, 0)
	return uint(parsed), err == nil
}

// authorize 是默认的端点守卫，只认 Authorization 头。
//
// 空 token 仍然放行，但这已经不再是 fail-open：validateServerBind 保证空 token
// 只可能出现在回环绑定上，也就是「本机自用、不想配 token」这一种情形
// （AUDIT-P1-21）。
func authorize(w http.ResponseWriter, r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if authorizeHeaderToken(r, token) {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func authorizeHeaderToken(r *http.Request, token string) bool {
	return secureTokenEqual(r.Header.Get("authorization"), "Bearer "+token)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, fmt.Sprintf("json encode error: %v", err), http.StatusInternalServerError)
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, fmt.Sprintf("json encode error: %v", err), http.StatusInternalServerError)
	}
}
