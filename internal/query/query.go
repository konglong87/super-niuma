package query

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/agentbudget"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/defaults"
	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/gitcontext"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/hooks"
	imagegensvc "github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/loopguard"
	"github.com/konglong87/super-niuma/internal/media"
	"github.com/konglong87/super-niuma/internal/memory"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/outputstyle"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/promptcache"
	"github.com/konglong87/super-niuma/internal/promptmode"
	"github.com/konglong87/super-niuma/internal/repair"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/skills"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/toolpolicy"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
	"github.com/konglong87/super-niuma/internal/tools/planmode"
	"github.com/konglong87/super-niuma/internal/tools/todowrite"
)

const (
	computerUseFastPathMaxTurns  = 16
	computerUseFastPathMaxTokens = 2048
)

type Options struct {
	Model     string
	MaxTurns  int
	MaxTokens int
	// Effort enables extended thinking for this session's main loop; same
	// vocabulary as skill effort ("low"/"medium"/"high"/"max"/token count,
	// "off"/"none" disabled). Empty values use defaults.Effort. Skills with
	// their own effort override it.
	Effort                        string
	ToolResultLimit               int
	ToolResultMessageBudget       int
	ToolResultHistoryBudget       int
	CWD                           string
	WritableRoots                 []string
	Recorder                      *session.Recorder
	InitialMessages               []anthropic.MessageParam
	InitialToolResultReplacements []toolresult.ReplacementRecord
	InitialAcknowledgedAgentTasks []uint64
	InitialActiveSkillMessages    []anthropic.MessageParam
	SystemPrompt                  string
	OverrideSystemPrompt          string
	CoordinatorPrompt             string
	MainThreadAgentPrompt         string
	InitialPrompt                 string
	SystemAddendum                string
	OutputStyle                   string
	Language                      string
	Hooks                         hooks.Runner
	PermissionPromptTool          string
	PermissionPrompt              func(context.Context, tools.PermissionPromptRequest) tools.PermissionPromptResponse
	UserQuestionPrompt            func(context.Context, tools.UserQuestionRequest) tools.UserQuestionResponse
	RuntimePermissionMode         func() string
	Sandbox                       tools.SandboxConfig
	TaskStore                     agenttasks.Store
	TaskController                *agenttasks.Controller
	TenantID                      uint64
	UserID                        uint64
	TenantSessionID               uint64
	TraceID                       string
	// RunID identifies one runtime-owned query execution. Channel runtimes set
	// it to the durable channel run ID; other runtimes use the session fallback.
	RunID string
	// ImageGenerator is the optional tenant-scoped image service. Keeping it
	// injectable preserves the disabled/bare runtime behavior by default.
	ImageGenerator            imagegensvc.Generator
	ImageBlobStore            imagegensvc.BlobStore
	MediaAssetStore           media.Store
	ComputerUse               computeruse.Service
	ComputerUseImageSupported bool
	// ComputerUseFastPath bounds the entire run, not each model request or tool call.
	// It is opt-in so ordinary queries retain their configured limits and context.
	ComputerUseFastPath          bool
	QuerySource                  string
	PromptMode                   string
	AgentProfileKey              string
	AgentProfileVersion          uint
	AgentProfileSource           string
	AgentProfileRequestedHash    string
	AgentProfileEffectiveHash    string
	AgentProfileBlockedOverrides int
	IncludeHookEvents            bool
	IncludePartialMessages       bool
	IncludeStreamEvents          bool
	NestedAgentProgress          func(agenttasks.EventInput)
	Attachments                  []Attachment
	AutoCompact                  compact.Config
	TenantContextManifest        TenantContextManifest
	SkillProvider                tools.SkillProvider
	DisableTools                 bool
	InlineTenantSkills           []string
	InlineTenantSkillSource      string
	ResponseFormat               *anthropic.ResponseFormat
	RuntimeProfile               runtimeprofile.Profile
	ExplicitContextRoots         []string
	// MaxParallelReadOnlyTools bounds opt-in read-only tool execution. Zero uses
	// the local-runtime default; a negative value disables parallel execution.
	MaxParallelReadOnlyTools int
}

const DefaultMaxParallelReadOnlyTools = 4

const executionRunIDBytes = 16

func EffectiveMaxParallelReadOnlyTools(configured int) (workers int, enabled bool) {
	if configured < 0 {
		return 1, false
	}
	if configured == 0 {
		return DefaultMaxParallelReadOnlyTools, true
	}
	return configured, true
}

type Session struct {
	client                  MessageStreamer
	registry                *tools.Registry
	options                 Options
	activeSkill             *tools.SkillRuntime
	sessionAllow            []string
	sessionDeny             []string
	cacheTracker            promptcache.Tracker
	requestCacheTracker     promptcache.RequestTracker
	sectionCache            map[string]*string
	agentMessagesMu         sync.Mutex
	toolStateMu             sync.Mutex
	transientComputerImages map[[sha256.Size]byte]struct{}
	deliveredAgentMessages  map[uint64]map[uint64]bool
	compactor               *compact.Compactor
	toolResultReplacements  map[string]string
	toolResultSeenIDs       map[string]bool
	activeSkillMessages     []anthropic.MessageParam
	acknowledgedAgentTasks  map[uint64]bool
	recentAgentEvidence     []capabilityloop.DecisionContext
	// turnFileChangeSeen tracks which paths already have a recoverable
	// file_change in the current turn, so later edits of the same file within the
	// turn are recorded as lite (non-recoverable) entries. Reset at each turn start.
	turnFileChangeSeen map[string]bool
	// currentTurnMessageID is the user message id that opened the current turn.
	// v2 message-graph file_change entries record it as message_id (Claude Code's
	// messageId->backup model). Empty for v1 transcripts.
	currentTurnMessageID string
	// agentBudget is the session-wide ceiling on cumulative sub-agent token and
	// cost usage. It is handed to every tool through tools.Context so nested
	// sub-agents share one counter instead of each getting a fresh allowance
	// (AUDIT-P0-14). The parent loop itself is not metered against it.
	agentBudget *agentbudget.Budget
	resumeInput *ResumeInput
}

type MessageStreamer interface {
	StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error)
}

type ToolResultReplacementRecord = toolresult.ReplacementRecord

type agentTaskEventLister interface {
	ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
}

type agentTaskLister interface {
	ListAgentTasks(ctx context.Context, limit int) ([]mysqlstore.AgentTask, error)
}

type Result struct {
	Response           string                    `json:"response"`
	Model              string                    `json:"model"`
	StopReason         string                    `json:"stop_reason,omitempty"`
	PendingInteraction *tools.InteractionRequest `json:"-"`
	Turns              int                       `json:"turns"`
	SessionID          string                    `json:"session_id,omitempty"`
	TranscriptPath     string                    `json:"transcript_path,omitempty"`
	Usage              Usage                     `json:"usage,omitempty"`
	ToolCalls          []ToolTrace               `json:"tool_calls,omitempty"`
	TenantRuntime      *TenantRuntimeManifest    `json:"tenant_runtime,omitempty"`
}

type ResumeInput struct {
	AssistantMessage anthropic.MessageParam
	ToolResult       anthropic.ContentBlock
}

type ContextManifest struct {
	Mode                         string                    `json:"mode"`
	Profile                      string                    `json:"profile"`
	RuntimeProfile               string                    `json:"runtime_profile,omitempty"`
	AgentProfileKey              string                    `json:"agent_profile_key,omitempty"`
	AgentProfileVersion          uint                      `json:"agent_profile_version,omitempty"`
	AgentProfileSource           string                    `json:"agent_profile_source,omitempty"`
	AgentProfileRequestedHash    string                    `json:"agent_profile_requested_hash,omitempty"`
	AgentProfileEffectiveHash    string                    `json:"agent_profile_effective_hash,omitempty"`
	AgentProfileBlockedOverrides int                       `json:"agent_profile_blocked_override_count,omitempty"`
	PromptProfile                string                    `json:"prompt_profile,omitempty"`
	QuerySource                  string                    `json:"query_source,omitempty"`
	SystemBlocks                 int                       `json:"system_blocks"`
	UserMessages                 int                       `json:"user_messages"`
	CodeContext                  CodeContextManifest       `json:"code_context,omitempty"`
	SkillsCatalog                SkillsCatalogManifest     `json:"skills_catalog,omitempty"`
	TenantRuntime                TenantRuntimeManifest     `json:"tenant_runtime,omitempty"`
	TenantSkillInline            TenantSkillInlineManifest `json:"tenant_skill_inline,omitempty"`
	TenantContext                TenantContextManifest     `json:"tenant_context,omitempty"`
	Attachments                  int                       `json:"attachments,omitempty"`
	InitialMessages              int                       `json:"initial_messages,omitempty"`
	CustomSystem                 bool                      `json:"custom_system,omitempty"`
	OverrideSystem               bool                      `json:"override_system,omitempty"`
	CoordinatorSystem            bool                      `json:"coordinator_system,omitempty"`
	MainThreadSystem             bool                      `json:"main_thread_system,omitempty"`
	SystemAddendum               bool                      `json:"system_addendum,omitempty"`
	Sources                      []ContextManifestSource   `json:"sources,omitempty"`
}

type ContextManifestSource struct {
	Type   string `json:"type"`
	Count  int    `json:"count"`
	Active bool   `json:"active"`
}

type CodeContextManifest struct {
	Active          bool                          `json:"active"`
	Documents       int                           `json:"documents,omitempty"`
	DocumentBytes   int                           `json:"document_bytes,omitempty"`
	PromptBytes     int                           `json:"prompt_bytes,omitempty"`
	ByType          map[string]int                `json:"by_type,omitempty"`
	ByTypeBytes     map[string]int                `json:"by_type_bytes,omitempty"`
	DocumentSummary []CodeContextDocumentManifest `json:"document_summary,omitempty"`
	WorkflowRules   int                           `json:"workflow_rules,omitempty"`
	WorkflowBytes   int                           `json:"workflow_bytes,omitempty"`
	BudgetedDocs    int                           `json:"budgeted_docs,omitempty"`
	WorkflowBudget  int                           `json:"workflow_budget,omitempty"`
	Includes        int                           `json:"includes,omitempty"`
	PathScoped      int                           `json:"path_scoped,omitempty"`
	ExcludeScoped   int                           `json:"exclude_scoped,omitempty"`
	GitContext      bool                          `json:"git_context"`
	MCPInstructions bool                          `json:"mcp_instructions"`
	Environment     bool                          `json:"environment"`
	OutputStyle     bool                          `json:"output_style"`
	Language        bool                          `json:"language"`
	Scratchpad      bool                          `json:"scratchpad"`
	AutoCompact     bool                          `json:"auto_compact,omitempty"`
	PromptCache     bool                          `json:"prompt_cache,omitempty"`
	FeatureSections []string                      `json:"feature_sections,omitempty"`
}

type CodeContextDocumentManifest struct {
	Path          string `json:"path,omitempty"`
	Type          string `json:"type,omitempty"`
	Bytes         int    `json:"bytes,omitempty"`
	PromptBytes   int    `json:"prompt_bytes,omitempty"`
	Budgeted      bool   `json:"budgeted,omitempty"`
	Parent        string `json:"parent,omitempty"`
	Workflow      bool   `json:"workflow,omitempty"`
	PathScoped    bool   `json:"path_scoped,omitempty"`
	ExcludeScoped bool   `json:"exclude_scoped,omitempty"`
}

type SkillsCatalogManifest struct {
	Active bool `json:"active"`
	Bytes  int  `json:"bytes,omitempty"`
}

type TenantRuntimeManifest struct {
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

func (m TenantRuntimeManifest) HasMetadata() bool {
	return m.Active || m.Resolved || len(m.LoadedKeys) > 0 || len(m.SkillKeys) > 0 ||
		len(m.Versions) > 0 || len(m.PackageSHA256) > 0 || len(m.PackageRefs) > 0 ||
		len(m.RuntimeRefs) > 0 || m.Bytes > 0 || m.TenantAddendum
}

type TenantSkillInlineManifest struct {
	Active        bool     `json:"active"`
	Selector      string   `json:"selector,omitempty"`
	SkillKeys     []string `json:"skill_keys,omitempty"`
	Loaded        bool     `json:"loaded,omitempty"`
	LoadedKeys    []string `json:"loaded_keys,omitempty"`
	Versions      []string `json:"versions,omitempty"`
	PackageSHA256 []string `json:"package_sha256,omitempty"`
	PackageRefs   []string `json:"package_refs,omitempty"`
	RuntimeRefs   []string `json:"runtime_refs,omitempty"`
	Bytes         int      `json:"bytes,omitempty"`
	MissingKeys   []string `json:"missing_keys,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type TenantContextManifest struct {
	Active          bool `json:"active"`
	Resolved        bool `json:"resolved,omitempty"`
	Addendum        bool `json:"addendum,omitempty"`
	KnowledgeChunks int  `json:"knowledge_chunks,omitempty"`
	MemoryItems     int  `json:"memory_items,omitempty"`
	Documents       int  `json:"documents,omitempty"`
	Profile         bool `json:"profile,omitempty"`
	ManagedMemory   bool `json:"managed_memory,omitempty"`
	TeamMemory      bool `json:"team_memory,omitempty"`
	AutoMemory      bool `json:"auto_memory,omitempty"`
}

type Usage struct {
	InputTokens                         int    `json:"input_tokens,omitempty"`
	OutputTokens                        int    `json:"output_tokens,omitempty"`
	LastInputTokens                     int    `json:"-"`
	CacheCreationInputTokens            int    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	ServiceTier                         string `json:"service_tier,omitempty"`
	InferenceGeo                        string `json:"inference_geo,omitempty"`
	Speed                               string `json:"speed,omitempty"`
}

type contextAssembly struct {
	userMessages     []anthropic.MessageParam
	systemMemory     string
	codeDocs         []memory.Document
	codePromptReport memory.PromptDocumentsReport
}

type ComputerObservationReference struct {
	ObservationID string `json:"observation_id"`
	AssetID       string `json:"asset_id"`
	MediaType     string `json:"media_type"`
	Name          string `json:"name"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
}

type ToolTrace struct {
	ID                  string                        `json:"id"`
	Name                string                        `json:"name"`
	Input               string                        `json:"input,omitempty"`
	Output              string                        `json:"output"`
	IsError             bool                          `json:"is_error,omitempty"`
	Interaction         *tools.InteractionRequest     `json:"-"`
	FileChanges         []tools.FileChange            `json:"file_changes,omitempty"`
	ComputerObservation *ComputerObservationReference `json:"computer_observation,omitempty"`
	// Verification stays internal; transcript evidence uses dedicated closure
	// events so existing ToolTrace API responses remain unchanged.
	verification    *repair.VerificationResult
	contextMessages []anthropic.MessageParam
}

type Attachment struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type"`
	MediaType  string `json:"media_type,omitempty"`
	Name       string `json:"name,omitempty"`
	Path       string `json:"path,omitempty"`
	URL        string `json:"url,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
	InlineData string `json:"-"`
}

type streamEvent struct {
	Type                     string              `json:"type"`
	Text                     string              `json:"text,omitempty"`
	Name                     string              `json:"name,omitempty"`
	ID                       string              `json:"id,omitempty"`
	Turn                     int                 `json:"turn,omitempty"`
	Index                    *int                `json:"index,omitempty"`
	Input                    json.RawMessage     `json:"input,omitempty"`
	Output                   string              `json:"output,omitempty"`
	IsError                  bool                `json:"is_error,omitempty"`
	Event                    any                 `json:"event,omitempty"`
	TaskID                   uint64              `json:"task_id,omitempty"`
	Payload                  json.RawMessage     `json:"payload,omitempty"`
	Usage                    *Usage              `json:"usage,omitempty"`
	SessionID                string              `json:"session_id,omitempty"`
	ParentToolUseID          json.RawMessage     `json:"parent_tool_use_id,omitempty"`
	UUID                     string              `json:"uuid,omitempty"`
	TTFTMs                   *int                `json:"ttftMs,omitempty"`
	Skill                    string              `json:"skill,omitempty"`
	SkillSource              string              `json:"skill_source,omitempty"`
	SkillVersion             string              `json:"skill_version,omitempty"`
	SkillFallback            bool                `json:"skill_fallback,omitempty"`
	Model                    string              `json:"model,omitempty"`
	Context                  string              `json:"context,omitempty"`
	PermissionDecision       string              `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string              `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             json.RawMessage     `json:"updatedInput,omitempty"`
	Message                  *streamMessage      `json:"message,omitempty"`
	ContentBlock             *streamContentBlock `json:"content_block,omitempty"`
	Delta                    *streamDelta        `json:"delta,omitempty"`
	StopReason               string              `json:"stop_reason,omitempty"`
	Error                    *streamError        `json:"error,omitempty"`
}

type streamMessage struct {
	ID           string               `json:"id,omitempty"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Model        string               `json:"model,omitempty"`
	Content      []streamContentBlock `json:"content"`
	StopReason   *string              `json:"stop_reason"`
	StopSequence *string              `json:"stop_sequence"`
	Usage        streamMessageUsage   `json:"usage"`
}

type streamMessageUsage struct {
	InputTokens                         int    `json:"input_tokens"`
	OutputTokens                        int    `json:"output_tokens"`
	CacheCreationInputTokens            int    `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens                int    `json:"cache_read_input_tokens,omitempty"`
	CacheCreationEphemeral1hInputTokens int    `json:"cache_creation_ephemeral_1h_input_tokens,omitempty"`
	CacheCreationEphemeral5mInputTokens int    `json:"cache_creation_ephemeral_5m_input_tokens,omitempty"`
	ServiceTier                         string `json:"service_tier,omitempty"`
	InferenceGeo                        string `json:"inference_geo,omitempty"`
	Speed                               string `json:"speed,omitempty"`
}

type streamContentBlock struct {
	Type          string            `json:"type"`
	Text          string            `json:"text,omitempty"`
	Thinking      string            `json:"thinking,omitempty"`
	Signature     string            `json:"signature,omitempty"`
	ConnectorText string            `json:"connector_text,omitempty"`
	Citations     []json.RawMessage `json:"citations,omitempty"`
	Name          string            `json:"name,omitempty"`
	ID            string            `json:"id,omitempty"`
	Input         json.RawMessage   `json:"input,omitempty"`
}

type streamDelta struct {
	Type          string          `json:"type"`
	Text          string          `json:"text,omitempty"`
	Thinking      string          `json:"thinking,omitempty"`
	PartialJSON   string          `json:"partial_json,omitempty"`
	Signature     string          `json:"signature,omitempty"`
	Citation      json.RawMessage `json:"citation,omitempty"`
	ConnectorText string          `json:"connector_text,omitempty"`
	StopReason    string          `json:"stop_reason,omitempty"`
	StopSequence  string          `json:"stop_sequence,omitempty"`
}

type streamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func New(client MessageStreamer, registry *tools.Registry, options Options) *Session {
	if strings.TrimSpace(options.Effort) == "" {
		options.Effort = defaults.Effort
	}
	if _, err := runtimeprofile.Normalize(options.RuntimeProfile); err != nil {
		options.DisableTools = true
	}
	if resolveRuntimePolicy(options.RuntimeProfile).DiscoverWorkspaceContext {
		_ = config.EnsureProjectSettingsMaterialized(options.CWD)
	}
	if options.MaxTurns <= 0 {
		options.MaxTurns = defaults.MaxTurns
	}
	if options.ComputerUseFastPath && options.MaxTurns > computerUseFastPathMaxTurns {
		options.MaxTurns = computerUseFastPathMaxTurns
	}
	if options.MaxTokens <= 0 {
		options.MaxTokens = defaultMaxTokens(options)
	}
	if options.ComputerUseFastPath && options.MaxTokens > computerUseFastPathMaxTokens {
		options.MaxTokens = computerUseFastPathMaxTokens
	}
	if options.ToolResultLimit <= 0 {
		options.ToolResultLimit = toolresult.DefaultLimit
	}
	if options.ToolResultMessageBudget <= 0 {
		options.ToolResultMessageBudget = toolresult.DefaultMessageBudget
	}
	if options.ToolResultHistoryBudget <= 0 {
		options.ToolResultHistoryBudget = toolresult.DefaultHistoryBudget
	}
	options.WritableRoots = codeModeWritableRoots(options)
	session := &Session{
		client:                 client,
		registry:               registry,
		options:                options,
		sectionCache:           map[string]*string{},
		toolResultReplacements: toolResultReplacementMap(options.InitialToolResultReplacements),
		toolResultSeenIDs:      toolresult.ToolResultIDs(options.InitialMessages),
		acknowledgedAgentTasks: acknowledgedAgentTaskMap(options.InitialAcknowledgedAgentTasks),
		agentBudget:            agentbudget.NewDefault(),
	}
	session.rememberActiveSkillMessages(options.InitialActiveSkillMessages)
	session.rememberAgentEvidenceFromMessages(options.InitialMessages)
	session.rememberAgentEvidenceFromCompactSummary(options.InitialMessages)
	if options.AutoCompact.Enabled {
		session.compactor = compact.New(options.AutoCompact, client, nil)
	}
	return session
}

func (s *Session) withRuntimeSpanRecorder(ctx context.Context) context.Context {
	if s == nil || s.options.Recorder == nil {
		return ctx
	}
	emitter := telemetry.FromContext(ctx)
	sinks := emitter.Sinks()
	sinks = append(sinks, session.NewRuntimeSpanSink(s.options.Recorder))
	return telemetry.WithEmitter(ctx, telemetry.NewEmitter(sinks...))
}

// AutoCompactEnabled reports whether this session has a compactor wired in.
// Exported so callers that construct sessions (CLI, server, goal, scheduler)
// can assert the wiring instead of silently losing it in a refactor.
func (s *Session) AutoCompactEnabled() bool {
	return s != nil && s.compactor != nil
}

func defaultMaxTokens(options Options) int {
	mode := promptmode.Parse(firstNonEmpty(options.PromptMode, getenv("NIUMA_PROMPT_MODE")), promptmode.Code)
	if mode.IsChat() {
		return defaults.ChatMaxTokens
	}
	return defaults.CodeMaxTokens
}

func acknowledgedAgentTaskMap(ids []uint64) map[uint64]bool {
	if len(ids) == 0 {
		return nil
	}
	out := map[uint64]bool{}
	for _, id := range ids {
		if id != 0 {
			out[id] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func codeModeWritableRoots(options Options) []string {
	roots := append([]string(nil), options.WritableRoots...)
	mode := promptmode.Parse(firstNonEmpty(options.PromptMode, getenv("NIUMA_PROMPT_MODE")), promptmode.Code)
	if !mode.IsCode() || !resolveRuntimePolicy(options.RuntimeProfile).AllowAutoMemoryRoot {
		return roots
	}
	return appendPathUnique(roots, memory.ClaudeCodeProjectMemoryDir(options.CWD))
}

func appendPathUnique(paths []string, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return paths
	}
	for _, existing := range paths {
		if strings.TrimSpace(existing) == path {
			return paths
		}
	}
	return append(paths, path)
}

func (s *Session) ToolDefinitions() []anthropic.ToolDefinition {
	return s.effectiveRegistry().Definitions()
}

func (s *Session) RunText(ctx context.Context, prompt string, stdout io.Writer) error {
	_, err := s.run(ctx, prompt, runCallbacks{
		onText: func(text string) error {
			_, err := io.WriteString(stdout, text)
			return err
		},
	})
	if err == nil {
		_, _ = io.WriteString(stdout, "\n")
	}
	return err
}

func (s *Session) Run(ctx context.Context, prompt string, textSink io.Writer) (Result, error) {
	return s.run(ctx, prompt, runCallbacks{
		onText: func(text string) error {
			_, err := io.WriteString(textSink, text)
			return err
		},
	})
}

func (s *Session) RunWithResume(ctx context.Context, resume ResumeInput, textSink io.Writer, cb RunCallbacks) (Result, error) {
	previous := s.resumeInput
	s.resumeInput = &resume
	defer func() { s.resumeInput = previous }()
	return s.run(ctx, "", runCallbacks{
		onText: func(text string) error {
			_, err := io.WriteString(textSink, text)
			return err
		},
		onThinking:    cb.OnThinking,
		onCompact:     cb.OnCompact,
		onTextAmended: cb.OnTextAmended,
		onToolCall: func(block anthropic.ContentBlock) error {
			if cb.OnToolCall == nil {
				return nil
			}
			return cb.OnToolCall(ToolCallEvent{ID: block.ID, Name: block.Name, Input: block.Input})
		},
		onToolResult: cb.OnToolResult,
		onMessageStop: func(turn int, stopReason string, usage Usage) error {
			if cb.OnUsage != nil {
				if err := cb.OnUsage(turn, usage); err != nil {
					return err
				}
			}
			if cb.OnMessageStop != nil {
				return cb.OnMessageStop(turn, stopReason, usage)
			}
			return nil
		},
	})
}

// RunWithCallbacks is used by richer interactive frontends that need the same
// query loop as Run plus non-text progress such as thinking and tool events.
func (s *Session) RunWithCallbacks(ctx context.Context, prompt string, textSink io.Writer, cb RunCallbacks) (Result, error) {
	return s.run(ctx, prompt, runCallbacks{
		onText: func(text string) error {
			_, err := io.WriteString(textSink, text)
			return err
		},
		onThinking:    cb.OnThinking,
		onCompact:     cb.OnCompact,
		onTextAmended: cb.OnTextAmended,
		onToolCall: func(block anthropic.ContentBlock) error {
			if cb.OnToolCall == nil {
				return nil
			}
			return cb.OnToolCall(ToolCallEvent{ID: block.ID, Name: block.Name, Input: block.Input})
		},
		onToolResult: cb.OnToolResult,
		onMessageStop: func(turn int, stopReason string, usage Usage) error {
			if cb.OnUsage != nil {
				if err := cb.OnUsage(turn, usage); err != nil {
					return err
				}
			}
			if cb.OnMessageStop != nil {
				return cb.OnMessageStop(turn, stopReason, usage)
			}
			return nil
		},
	})
}

type RunCallbacks struct {
	OnThinking    func(text string) error
	OnToolCall    func(event ToolCallEvent) error
	OnToolResult  func(trace ToolTrace) error
	OnUsage       func(turn int, usage Usage) error
	OnMessageStop func(turn int, stopReason string, usage Usage) error
	OnCompact     func(result compact.Result) error
	// OnTextAmended asks the frontend to replace the live-streamed tail
	// `streamed` with `final` ("" retracts it) — see runCallbacks.onTextAmended.
	OnTextAmended func(streamed, final string) error
}

type ToolCallEvent struct {
	ID    string
	Name  string
	Input json.RawMessage
}

func (s *Session) RunStreamJSON(ctx context.Context, prompt string, stdout io.Writer) error {
	enc := json.NewEncoder(stdout)
	started := time.Now()
	sessionID := s.streamSessionID()
	emit := func(event streamEvent) error {
		return enc.Encode(event)
	}
	emitAny := func(event any) error {
		return enc.Encode(event)
	}
	emitStreamEvent := func(turn int, event map[string]any) error {
		if !s.options.IncludeStreamEvents {
			return nil
		}
		// The SDK stream_event envelope always carries session identity and a
		// nullable parent tool id; consumers use those keys for remote replay.
		out := streamEvent{
			Type:            "stream_event",
			Event:           event,
			SessionID:       sessionID,
			ParentToolUseID: json.RawMessage("null"),
			UUID:            fmt.Sprintf("turn_%d", turn),
		}
		if event["type"] == streamEventMessageStart {
			zero := 0
			out.TTFTMs = &zero
		}
		return enc.Encode(out)
	}
	textBlockOpen := false
	textBlockIndex := 0
	partialText := ""
	thinkingIndex := 0
	thinkingBlockOpen := false
	connectorIndex := 0
	connectorBlockOpen := false
	currentTurn := 0
	result, err := s.run(ctx, prompt, runCallbacks{
		onTurnStart: func(turn int) error {
			currentTurn = turn
			textBlockOpen = false
			textBlockIndex = 0
			partialText = ""
			thinkingIndex = 0
			thinkingBlockOpen = false
			connectorIndex = 0
			connectorBlockOpen = false
			message := &streamMessage{
				ID:         fmt.Sprintf("turn_%d", turn),
				Type:       "message",
				Role:       "assistant",
				Model:      s.currentModel(),
				Content:    []streamContentBlock{},
				StopReason: nil,
				Usage:      streamMessageUsage{},
			}
			if err := emit(streamEvent{Type: "turn_start", Turn: turn}); err != nil {
				return err
			}
			if err := emit(streamEvent{Type: streamEventMessageStart, Turn: turn, Message: message}); err != nil {
				return err
			}
			return emitStreamEvent(turn, map[string]any{"type": streamEventMessageStart, "message": message})
		},
		onText: func(text string) error {
			if thinkingBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(thinkingIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": thinkingIndex}); err != nil {
					return err
				}
				thinkingBlockOpen = false
				textBlockIndex = thinkingIndex + 1
			}
			if connectorBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(connectorIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": connectorIndex}); err != nil {
					return err
				}
				connectorBlockOpen = false
				textBlockIndex = connectorIndex + 1
			}
			if !textBlockOpen {
				contentBlock := &streamContentBlock{Type: blockTypeText, Text: ""}
				if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(textBlockIndex), Name: "text", ContentBlock: contentBlock}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": textBlockIndex, "content_block": contentBlock}); err != nil {
					return err
				}
				textBlockOpen = true
			}
			partialText += text
			delta := &streamDelta{Type: "text_delta", Text: text}
			if err := emit(streamEvent{Type: "text_delta", Text: text, Delta: delta}); err != nil {
				return err
			}
			if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": textBlockIndex, "delta": delta}); err != nil {
				return err
			}
			if !s.options.IncludePartialMessages {
				return nil
			}
			return emit(streamEvent{Type: "partial_message", Text: partialText})
		},
		onThinking: func(text string) error {
			if textBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(textBlockIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": textBlockIndex}); err != nil {
					return err
				}
				textBlockOpen = false
				thinkingIndex = textBlockIndex + 1
			}
			if connectorBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(connectorIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": connectorIndex}); err != nil {
					return err
				}
				connectorBlockOpen = false
				thinkingIndex = connectorIndex + 1
			}
			if !thinkingBlockOpen {
				contentBlock := &streamContentBlock{Type: blockTypeThinking}
				if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(thinkingIndex), Name: "thinking", ContentBlock: contentBlock}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": thinkingIndex, "content_block": contentBlock}); err != nil {
					return err
				}
				thinkingBlockOpen = true
			}
			delta := &streamDelta{Type: "thinking_delta", Thinking: text}
			if err := emit(streamEvent{Type: "thinking_delta", Text: text, Delta: delta}); err != nil {
				return err
			}
			return emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": thinkingIndex, "delta": delta})
		},
		onSignature: func(signature string) error {
			index := thinkingIndex
			if connectorBlockOpen {
				index = connectorIndex
			} else if !thinkingBlockOpen {
				contentBlock := &streamContentBlock{Type: blockTypeThinking, Signature: ""}
				if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(index), Name: "thinking", ContentBlock: contentBlock}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": index, "content_block": contentBlock}); err != nil {
					return err
				}
				thinkingBlockOpen = true
			}
			delta := &streamDelta{Type: "signature_delta", Signature: signature}
			if err := emit(streamEvent{Type: "signature_delta", Index: intPtr(index), Delta: delta}); err != nil {
				return err
			}
			return emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": index, "delta": delta})
		},
		onCitation: func(citation json.RawMessage) error {
			if !textBlockOpen {
				contentBlock := &streamContentBlock{Type: blockTypeText, Text: ""}
				if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(textBlockIndex), Name: "text", ContentBlock: contentBlock}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": textBlockIndex, "content_block": contentBlock}); err != nil {
					return err
				}
				textBlockOpen = true
			}
			delta := &streamDelta{Type: "citations_delta", Citation: citation}
			if err := emit(streamEvent{Type: "citations_delta", Index: intPtr(textBlockIndex), Delta: delta}); err != nil {
				return err
			}
			return emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": textBlockIndex, "delta": delta})
		},
		onConnectorText: func(text string) error {
			if textBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(textBlockIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": textBlockIndex}); err != nil {
					return err
				}
				textBlockOpen = false
				connectorIndex = textBlockIndex + 1
			}
			if thinkingBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(thinkingIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": thinkingIndex}); err != nil {
					return err
				}
				thinkingBlockOpen = false
				connectorIndex = thinkingIndex + 1
			}
			if !connectorBlockOpen {
				contentBlock := &streamContentBlock{Type: "connector_text", ConnectorText: ""}
				if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(connectorIndex), Name: "connector_text", ContentBlock: contentBlock}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": connectorIndex, "content_block": contentBlock}); err != nil {
					return err
				}
				connectorBlockOpen = true
			}
			delta := &streamDelta{Type: "connector_text_delta", ConnectorText: text}
			if err := emit(streamEvent{Type: "connector_text_delta", Text: text, Delta: delta}); err != nil {
				return err
			}
			return emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": connectorIndex, "delta": delta})
		},
		onToolCall: func(block anthropic.ContentBlock) error {
			index := 0
			if textBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(textBlockIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": textBlockIndex}); err != nil {
					return err
				}
				textBlockOpen = false
				index = textBlockIndex + 1
			}
			if thinkingBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(thinkingIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": thinkingIndex}); err != nil {
					return err
				}
				thinkingBlockOpen = false
				index = thinkingIndex + 1
			}
			if connectorBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(connectorIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": connectorIndex}); err != nil {
					return err
				}
				connectorBlockOpen = false
				index = connectorIndex + 1
			}
			publicBlock := redactComputerToolCall(block)
			contentBlock := &streamContentBlock{Type: blockTypeToolUse, Name: publicBlock.Name, ID: publicBlock.ID, Input: publicBlock.Input}
			if err := emit(streamEvent{Type: streamEventContentBlockStart, Index: intPtr(index), Name: publicBlock.Name, ID: publicBlock.ID, Input: publicBlock.Input, ContentBlock: contentBlock}); err != nil {
				return err
			}
			if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStart, "index": index, "content_block": contentBlock}); err != nil {
				return err
			}
			if len(publicBlock.Input) > 0 {
				for _, chunk := range splitJSONDelta(string(publicBlock.Input)) {
					delta := &streamDelta{Type: "input_json_delta", PartialJSON: chunk}
					if err := emit(streamEvent{Type: "input_json_delta", Index: intPtr(index), ID: block.ID, Delta: delta}); err != nil {
						return err
					}
					if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockDelta, "index": index, "delta": delta}); err != nil {
						return err
					}
				}
			}
			if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(index)}); err != nil {
				return err
			}
			if err := emitStreamEvent(currentTurn, map[string]any{"type": streamEventContentBlockStop, "index": index}); err != nil {
				return err
			}
			return emit(streamEvent{Type: "tool_call", Name: publicBlock.Name, ID: publicBlock.ID, Input: publicBlock.Input})
		},
		onToolResult: func(trace ToolTrace) error {
			if err := emit(streamEvent{Type: blockTypeToolResult, ID: trace.ID, Name: trace.Name, IsError: trace.IsError, Output: trace.Output}); err != nil {
				return err
			}
			if trace.Name == "Task" {
				return emit(streamEvent{Type: "nested_agent_progress", ID: trace.ID, Name: trace.Name, IsError: trace.IsError, Output: trace.Output, Event: "completed"})
			}
			return nil
		},
		onHookStart: func(event, toolName string) error {
			if !s.options.IncludeHookEvents {
				return nil
			}
			return emit(streamEvent{Type: "hook_start", Event: event, Name: toolName})
		},
		onHookResult: func(event, toolName string, result hooks.Result, err error) error {
			if !s.options.IncludeHookEvents {
				return nil
			}
			out := result.Message
			isError := err != nil
			if err != nil {
				out = err.Error()
			}
			return emit(streamEvent{
				Type:                     "hook_result",
				Event:                    event,
				Name:                     toolName,
				IsError:                  isError,
				Output:                   out,
				PermissionDecision:       result.PermissionDecision,
				PermissionDecisionReason: result.PermissionDecisionReason,
				UpdatedInput:             result.UpdatedInput,
			})
		},
		onSkillActivated: func(runtime *tools.SkillRuntime) error {
			if runtime == nil {
				return nil
			}
			return emit(streamEvent{
				Type:          "skill_activated",
				Skill:         runtime.Name,
				SkillSource:   runtime.Source,
				SkillVersion:  runtime.Version,
				SkillFallback: runtime.Fallback,
				Model:         runtime.Model,
				Context:       runtime.Context,
			})
		},
		onNestedAgentProgress: func(event agenttasks.EventInput) error {
			var payload json.RawMessage
			if strings.TrimSpace(event.PayloadJSON) != "" {
				payload = json.RawMessage(event.PayloadJSON)
			}
			return emit(streamEvent{Type: "nested_agent_progress", Event: event.EventType, TaskID: event.TaskID, Payload: payload})
		},
		onMessageStop: func(turn int, stopReason string, usage Usage) error {
			if textBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(textBlockIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(turn, map[string]any{"type": streamEventContentBlockStop, "index": textBlockIndex}); err != nil {
					return err
				}
				textBlockOpen = false
			}
			if thinkingBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(thinkingIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(turn, map[string]any{"type": streamEventContentBlockStop, "index": thinkingIndex}); err != nil {
					return err
				}
				thinkingBlockOpen = false
			}
			if connectorBlockOpen {
				if err := emit(streamEvent{Type: streamEventContentBlockStop, Index: intPtr(connectorIndex)}); err != nil {
					return err
				}
				if err := emitStreamEvent(turn, map[string]any{"type": streamEventContentBlockStop, "index": connectorIndex}); err != nil {
					return err
				}
				connectorBlockOpen = false
			}
			if usage.InputTokens != 0 || usage.OutputTokens != 0 {
				if err := emit(streamEvent{Type: "usage_delta", Turn: turn, Usage: &usage}); err != nil {
					return err
				}
			}
			delta := &streamDelta{Type: streamEventMessageDelta, StopReason: stopReason}
			if err := emit(streamEvent{Type: streamEventMessageDelta, Turn: turn, Delta: delta, Usage: &usage}); err != nil {
				return err
			}
			if err := emitStreamEvent(turn, map[string]any{"type": streamEventMessageDelta, "delta": delta, "usage": usage}); err != nil {
				return err
			}
			if err := emit(streamEvent{Type: streamEventMessageStop, Turn: turn, StopReason: stopReason}); err != nil {
				return err
			}
			return emitStreamEvent(turn, map[string]any{"type": streamEventMessageStop})
		},
	})
	if err != nil {
		_ = emit(streamEvent{Type: "error", Error: &streamError{Type: "runtime_error", Message: err.Error()}})
		_ = emitAny(streamResultEnvelope(result, err, sessionID, time.Since(started)))
		_ = emit(streamEvent{Type: "done"})
		return err
	}
	if err := emitAny(streamResultEnvelope(result, nil, sessionID, time.Since(started))); err != nil {
		return err
	}
	return emit(streamEvent{Type: "done"})
}

func (s *Session) streamSessionID() string {
	if s.options.Recorder != nil && strings.TrimSpace(s.options.Recorder.SessionID) != "" {
		return s.options.Recorder.SessionID
	}
	if strings.TrimSpace(s.options.TraceID) != "" {
		return strings.TrimSpace(s.options.TraceID)
	}
	return "local"
}

func (s *Session) recorderSessionID() string {
	if s.options.Recorder == nil {
		return ""
	}
	return strings.TrimSpace(s.options.Recorder.SessionID)
}

func (s *Session) recorderTranscriptPath() string {
	if s.options.Recorder == nil {
		return ""
	}
	return strings.TrimSpace(s.options.Recorder.Path)
}

func streamResultEnvelope(result Result, runErr error, sessionID string, duration time.Duration) map[string]any {
	durationMS := duration.Milliseconds()
	if isEnvTruthy("NIUMA_DETERMINISTIC_STREAM_RESULT") {
		durationMS = 0
	}
	base := map[string]any{
		"type":               "result",
		"duration_ms":        durationMS,
		"duration_api_ms":    durationMS,
		"num_turns":          result.Turns,
		"stop_reason":        nullableString(result.StopReason),
		"total_cost_usd":     0,
		"usage":              result.Usage,
		"modelUsage":         streamModelUsage(result),
		"permission_denials": []any{},
		"uuid":               "result",
		"session_id":         sessionID,
	}
	if runErr != nil {
		base["subtype"] = "error_during_execution"
		base["is_error"] = true
		base["errors"] = []string{runErr.Error()}
		return base
	}
	base["subtype"] = "success"
	base["is_error"] = false
	base["result"] = result.Response
	return base
}

func streamModelUsage(result Result) map[string]Usage {
	model := strings.TrimSpace(result.Model)
	if model == "" {
		model = "unknown"
	}
	return map[string]Usage{model: result.Usage}
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

type runCallbacks struct {
	onTurnStart           func(turn int) error
	onText                func(text string) error
	onThinking            func(text string) error
	onSignature           func(signature string) error
	onCitation            func(citation json.RawMessage) error
	onConnectorText       func(text string) error
	onToolCall            func(block anthropic.ContentBlock) error
	onToolResult          func(trace ToolTrace) error
	onCompact             func(result compact.Result) error
	onHookStart           func(event, toolName string) error
	onHookResult          func(event, toolName string, result hooks.Result, err error) error
	onSkillActivated      func(runtime *tools.SkillRuntime) error
	onNestedAgentProgress func(event agenttasks.EventInput) error
	onMessageStop         func(turn int, stopReason string, usage Usage) error
	// onTextAmended fires when the accepted turn text diverges from what was
	// live-streamed (output guards, provider failover) or when a completion
	// gate retracts a turn (final == ""). Sinks replace the live tail
	// `streamed` with `final`.
	onTextAmended func(streamed, final string) error
}

type turnStreamCallbackKind int

const (
	turnStreamCallbackText turnStreamCallbackKind = iota
	turnStreamCallbackThinking
	turnStreamCallbackSignature
	turnStreamCallbackCitation
	turnStreamCallbackConnectorText
)

type turnStreamCallbackEvent struct {
	kind turnStreamCallbackKind
	text string
	raw  json.RawMessage
}

// turnStreamCallbackBuffer preserves the buffered contract for sinks that
// cannot amend already-shown text (stdout pipes, RunStreamJSON, server
// writers): stream events are held during generation and replayed in arrival
// order only once the turn's message is accepted, so gate-retracted drafts
// never reach output that cannot be unprinted. Amend-capable frontends (the
// TUI) bypass this entirely and stream live.
type turnStreamCallbackBuffer struct {
	events []turnStreamCallbackEvent
	text   strings.Builder
}

func (b *turnStreamCallbackBuffer) AppendText(text string) {
	b.events = append(b.events, turnStreamCallbackEvent{kind: turnStreamCallbackText, text: text})
	b.text.WriteString(text)
}

func (b *turnStreamCallbackBuffer) AppendThinking(text string) {
	b.events = append(b.events, turnStreamCallbackEvent{kind: turnStreamCallbackThinking, text: text})
}

func (b *turnStreamCallbackBuffer) AppendSignature(signature string) {
	b.events = append(b.events, turnStreamCallbackEvent{kind: turnStreamCallbackSignature, text: signature})
}

func (b *turnStreamCallbackBuffer) AppendCitation(citation json.RawMessage) {
	raw := append(json.RawMessage(nil), citation...)
	b.events = append(b.events, turnStreamCallbackEvent{kind: turnStreamCallbackCitation, raw: raw})
}

func (b *turnStreamCallbackBuffer) AppendConnectorText(text string) {
	b.events = append(b.events, turnStreamCallbackEvent{kind: turnStreamCallbackConnectorText, text: text})
}

func (b *turnStreamCallbackBuffer) Replay(cb runCallbacks, accepted *anthropic.MessageParam) error {
	if len(b.events) == 0 {
		if accepted == nil {
			return nil
		}
		text := assistantText(accepted.Content)
		if text == "" || cb.onText == nil {
			return nil
		}
		return cb.onText(text)
	}
	if accepted != nil {
		if acceptedText := assistantText(accepted.Content); acceptedText != b.text.String() {
			if acceptedText == "" || cb.onText == nil {
				return nil
			}
			return cb.onText(acceptedText)
		}
	}
	for _, event := range b.events {
		switch event.kind {
		case turnStreamCallbackText:
			if cb.onText != nil {
				if err := cb.onText(event.text); err != nil {
					return err
				}
			}
		case turnStreamCallbackThinking:
			if cb.onThinking != nil {
				if err := cb.onThinking(event.text); err != nil {
					return err
				}
			}
		case turnStreamCallbackSignature:
			if cb.onSignature != nil {
				if err := cb.onSignature(event.text); err != nil {
					return err
				}
			}
		case turnStreamCallbackCitation:
			if cb.onCitation != nil {
				if err := cb.onCitation(event.raw); err != nil {
					return err
				}
			}
		case turnStreamCallbackConnectorText:
			if cb.onConnectorText != nil {
				if err := cb.onConnectorText(event.text); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// reconcileStreamedText runs after a turn's message is accepted. Stream deltas
// were already delivered live to the callbacks during generation (the
// typewriter), so normally there is nothing to do. Two exceptions:
//   - no deltas were streamed (non-streaming provider path): emit the accepted
//     text now, once;
//   - the accepted text diverged from what was streamed (output guards,
//     provider failover mid-stream): ask the sink to amend the live tail.
//
// Sinks without an amend callback keep the streamed text as-is; interactive
// frontends reconcile the final response downstream from the run result.
func reconcileStreamedText(cb runCallbacks, streamed string, accepted *anthropic.MessageParam) error {
	if accepted == nil {
		return nil
	}
	acceptedText := assistantText(accepted.Content)
	if acceptedText == streamed {
		return nil
	}
	if streamed == "" {
		if acceptedText == "" || cb.onText == nil {
			return nil
		}
		return cb.onText(acceptedText)
	}
	// Accepted text extends what streamed (e.g. exact-output completion
	// appending a required literal): emit only the missing tail — this keeps
	// plain writer sinks correct without any amend capability.
	if tail, ok := strings.CutPrefix(acceptedText, streamed); ok && tail != "" {
		if cb.onText == nil {
			return nil
		}
		return cb.onText(tail)
	}
	if cb.onTextAmended != nil {
		return cb.onTextAmended(streamed, acceptedText)
	}
	return nil
}

func (s *Session) run(ctx context.Context, prompt string, cb runCallbacks) (result Result, runErr error) {
	if !s.options.ComputerUseFastPath && s.options.ComputerUse != nil && s.options.ComputerUseImageSupported && isSimpleComputerUsePrompt(prompt) {
		s.options.ComputerUseFastPath = true
		// A simple desktop task does not need repository, skills, memory, or hook
		// context. Keeping the normal code profile here made every subsequent
		// screenshot/model turn slower as the visual transcript grew.
		s.options.RuntimeProfile = runtimeprofile.ProfileBare
		if s.options.MaxTurns > computerUseFastPathMaxTurns {
			s.options.MaxTurns = computerUseFastPathMaxTurns
		}
		if s.options.MaxTokens > computerUseFastPathMaxTokens {
			s.options.MaxTokens = computerUseFastPathMaxTokens
		}
	}
	// Fast path narrows the tool/context surface for simple GUI work, but it
	// does not impose a product wall-clock deadline. Callers own cancellation
	// (the acceptance wrapper is one such caller); ordinary Computer Use stays
	// alive until the user or parent request stops it.
	executionRunID, err := newExecutionRunID(s.options.RunID)
	if err != nil {
		return result, err
	}
	resuming := s.resumeInput != nil
	ctx = observability.WithCLISessionID(ctx, s.streamSessionID())
	if observability.RequestPurpose(ctx) == "" {
		ctx = observability.WithRequestPurpose(ctx, "main")
	}
	ctx = s.withRuntimeSpanRecorder(ctx)
	if !resuming {
		prompt = s.effectiveUserPrompt(prompt)
	}
	runStart := time.Now()
	observability.Info(ctx, nil, "query.run.start", "query.Session.run", "query run start",
		"model", s.options.Model,
		"cwd", s.options.CWD,
		"max_turns", s.options.MaxTurns,
		"max_tokens", s.options.MaxTokens,
		"prompt_bytes", len(prompt),
	)
	ctx, runSpan := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventQueryRun,
		Category:  telemetry.CategorySession,
		Source:    "query.Session.run",
		Model:     s.options.Model,
		SessionID: s.options.TenantSessionID,
		Properties: map[string]any{
			"cwd":             s.options.CWD,
			"max_turns":       s.options.MaxTurns,
			"max_tokens":      s.options.MaxTokens,
			"prompt_bytes":    len(prompt),
			"input_bytes":     len(prompt),
			"query_source":    s.querySource(),
			"runtime_profile": s.runtimeProfile().String(),
		},
	})
	defer func() {
		status := telemetry.StatusOK
		if runErr != nil {
			status = telemetry.StatusError
		}
		event := telemetry.Event{
			Name:                                telemetry.EventQueryRun,
			Category:                            telemetry.CategorySession,
			Source:                              "query.Session.run",
			Status:                              status,
			Model:                               result.Model,
			SessionID:                           s.options.TenantSessionID,
			DurationMS:                          time.Since(runStart).Milliseconds(),
			InputTokens:                         result.Usage.InputTokens,
			OutputTokens:                        result.Usage.OutputTokens,
			CacheCreationInputTokens:            result.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:                result.Usage.CacheReadInputTokens,
			CacheCreationEphemeral1hInputTokens: result.Usage.CacheCreationEphemeral1hInputTokens,
			CacheCreationEphemeral5mInputTokens: result.Usage.CacheCreationEphemeral5mInputTokens,
			Properties: map[string]any{
				"turns":           result.Turns,
				"stop_reason":     result.StopReason,
				"tool_calls":      len(result.ToolCalls),
				"runtime_profile": s.runtimeProfile().String(),
			},
		}
		if runErr != nil {
			event.Error = runErr.Error()
		}
		runSpan.FinishEvent(event)
		_ = s.runStopHook(ctx, result, runErr, runStart, cb)
	}()
	if !resuming {
		prompt, runErr = s.runUserPromptSubmitHook(ctx, prompt, cb)
		if runErr != nil {
			return result, runErr
		}
	}
	directSharedStateAuthorization := gitpolicy.ParseAuthorization(prompt)
	forbiddenLiterals := extractExplicitForbiddenOutputLiterals(prompt)
	outputGuard := newForbiddenOutputGuard(forbiddenLiterals, nil)
	exactOutputGuard := newExactOutputGuard(prompt, forbiddenLiterals, nil)
	continuation := resolveContinuationIntent(prompt, s.options.InitialMessages)
	continuationAuthorization := newContinuationAuthorizationGrant(continuation.SharedStateEffects)
	messages := append([]anthropic.MessageParam(nil), s.options.InitialMessages...)
	for i := range messages {
		messages[i] = redactComputerAssistant(messages[i])
	}
	messages = computerModelHistory(messages)
	assembly := s.assembleContextMessages(ctx, prompt)
	userRecordContent := ""
	if resuming {
		messages = append(messages, redactComputerAssistant(s.resumeInput.AssistantMessage))
		messages = append(messages, anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{s.resumeInput.ToolResult}})
		messages = computerModelHistory(messages)
	} else {
		userMessage, recordContent := userMessageWithAttachments(prompt, s.options.Attachments)
		userRecordContent = recordContent
		if packed, ok := packCodeContextWithUserPrompt(s.options.InitialMessages, assembly.userMessages, userMessage); ok {
			userMessage = packed
		} else {
			messages = append(messages, assembly.userMessages...)
		}
		messages = append(messages, userMessage)
	}
	if continuation.Active {
		messages = append(messages, anthropic.MessageParam{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: continuationReminder(continuation)}},
		})
	}
	result = Result{Model: s.options.Model}
	requiredVerifications := requiredCompletionVerifications(prompt)
	contract := continuationTaskContract(buildTaskContract(prompt), continuation)
	// Turn boundary: reset per-turn file-change dedup so the first edit of each
	// file this turn records a recoverable snapshot and later edits go lite.
	s.turnFileChangeSeen = map[string]bool{}
	s.currentTurnMessageID = ""
	var messageID string
	if s.options.Recorder != nil {
		// Turn boundary (systemic leaf alignment): realign the recorder's active
		// leaf with the transcript on disk before this turn's first append. Any
		// branch_head appended out-of-band since the last turn — /rewind, /redo,
		// by-id compact, or a CLI op in another terminal — is thereby honored, so
		// the new turn chains from the current leaf and forks correctly instead of
		// linearly extending a stale tip. No-op for v1 recorders.
		_ = s.options.Recorder.SyncLeafFromDisk()
		result.SessionID = s.options.Recorder.SessionID
		result.TranscriptPath = s.options.Recorder.Path
		messageID = session.NewEntryID()
		s.currentTurnMessageID = messageID
		_, _ = s.options.Recorder.Checkpoint("auto-"+messageID, messageID)
		_ = s.options.Recorder.Append(session.Entry{ID: messageID, Type: "message", Role: "user", Content: userRecordContent})
		// v2 message-graph: anchor this turn at its opening user message. No-op for
		// v1 recorders. The active leaf still advances with the turn's later entries.
		_ = s.options.Recorder.MarkTurn(messageID)
		if continuation.Active {
			s.recordClosureEvent("continuation_intent", continuation)
		}
		s.recordClosureEvent("task_contract", contract)
	}
	systemBlocks := s.effectiveSystemBlocks()
	system := joinSystemBlocks(systemBlocks)
	var skillsManifest SkillsCatalogManifest
	sentSkillCatalog := map[string]bool{}
	var skillCatalogEvidence strings.Builder
	skillCatalogEvidence.WriteString(prompt)
	inline, tenantSkillInlineManifest := s.inlineTenantSkills(ctx)
	if strings.TrimSpace(inline) != "" {
		system = appendSystemText(system, inline)
		systemBlocks = appendSystemBlockWithSource(systemBlocks, inline, "tenant_skill_inline")
		skillsManifest = SkillsCatalogManifest{Active: true, Bytes: len(inline)}
	} else if catalog, err := s.tenantSkillsCatalog(ctx, prompt); err != nil {
		observability.Error(ctx, nil, "tenant.skills.catalog.error", "query.Session.run", "load tenant skill catalog failed", "error", err)
	} else if strings.TrimSpace(catalog) != "" {
		catalog = strings.TrimSpace(catalog)
		system = appendSystemText(system, catalog)
		systemBlocks = appendSystemBlockWithSource(systemBlocks, catalog, "tenant_skills_catalog")
		skillsManifest = SkillsCatalogManifest{Active: true, Bytes: len(catalog)}
	}
	if s.promptProfile().IsCode() && s.runtimePolicy().DiscoverSkills {
		if catalog, names, err := skills.CatalogPromptForPromptWithNames(s.options.CWD, prompt); err != nil {
			observability.Error(ctx, nil, "skills.catalog.error", "query.Session.run", "load skill catalog failed", "error", err)
		} else if strings.TrimSpace(catalog) != "" && !s.isClaudeCompatibleProfile() {
			catalog = strings.TrimSpace(catalog)
			system = appendSystemText(system, catalog)
			systemBlocks = appendSystemBlockWithSource(systemBlocks, catalog, "skills_catalog")
			systemBlocks = maybeMoveSkillsCatalogIntoStablePrefix(systemBlocks)
			system = joinSystemBlocks(systemBlocks)
			skillsManifest.Active = true
			skillsManifest.Bytes += len(catalog)
			markSkillCatalogSent(sentSkillCatalog, names)
		}
	}
	if strings.TrimSpace(assembly.systemMemory) != "" && !systemBlocksContainText(systemBlocks, "# auto memory") {
		system = appendSystemText(system, assembly.systemMemory)
		systemBlocks = appendCompatibleMemoryToSystemBlocks(systemBlocks, assembly.systemMemory)
	}
	manifest := s.contextManifest(systemBlocks, assembly, skillsManifest, tenantSkillInlineManifest)
	if manifest.TenantRuntime.HasMetadata() {
		runtime := manifest.TenantRuntime
		result.TenantRuntime = &runtime
	}
	s.recordContextManifest(ctx, messageID, manifest)
	s.observePromptCache(ctx, systemBlocks)
	registry := s.effectiveRegistry()

	// Loop guard state: track consecutive turns that make no progress, i.e. whose
	// fingerprint recurs within a recent window so no new information is entering
	// the context. A degenerate model (common on weak providers) can otherwise
	// repeat the same call — or cycle through the same few calls, or keep emitting
	// the same rejected closing text — until MaxTurns, wasting requests and
	// flooding the UI. For tool turns the fingerprint includes the results, so a
	// legitimate poll/wait (same input, changing result) is never mistaken for a
	// stuck loop.
	var loopTracker loopguard.Tracker
	sharedStateGateRetries := newSharedStateGateRetryTracker()
	// applyLoopGuard folds one turn into the tracker and returns a non-nil error
	// when the run must abort. Both the tool-execution path and the
	// completion-gated text path go through it so they escalate identically: a
	// text-only loop is just as costly as a tool loop and used to be invisible to
	// the guard entirely.
	applyLoopGuard := func(fingerprint, label, auditSignature, recurring string) error {
		streak := loopTracker.Observe(fingerprint, label)
		if loopTracker.Tripped() {
			result.StopReason = "loop_guard_abort"
			s.recordClosureEvent("loop_guard", map[string]any{
				"severity":   "abort",
				"signature":  compactOneLine(auditSignature, 200),
				"repeat":     streak,
				"hard_limit": loopguard.HardLimit,
			})
			err := fmt.Errorf("loop guard: no progress for %d turns (%s kept recurring)", streak, recurring)
			observability.Error(ctx, nil, "query.loop_guard_abort", "query.Session.run", "query aborted by loop guard",
				"turns", result.Turns,
				"repeat", streak,
				"error", err,
			)
			return err
		}
		// The soft signal is delivered continuously via the loop-awareness
		// runtime-status section (loopguard.Awareness); here we only record a
		// one-time audit event when the streak first crosses the soft limit.
		if loopTracker.TakeWarning() {
			s.recordClosureEvent("loop_guard", map[string]any{
				"severity":   "warn",
				"signature":  compactOneLine(auditSignature, 200),
				"repeat":     streak,
				"soft_limit": loopguard.SoftLimit,
			})
		}
		return nil
	}
	// Overflow recovery is allowed once per session: if the prompt is still too
	// long after a forced compaction, retrying again would just burn turns.
	overflowCompacted := false
	// pendingGateNudge carries a completion-gate reminder into the NEXT request
	// only. It is deliberately NOT appended to `messages`: doing that stacked one
	// rejected draft plus one identical <system-reminder> per gated turn, so a
	// model that never satisfies the gate dragged a copy for every turn so far
	// through every later request.
	pendingGateNudge := ""

	for turn := 1; turn <= s.options.MaxTurns; turn++ {
		result.Turns = turn
		turnStartedAt := time.Now()
		turnCtx := telemetry.WithTurnIndex(ctx, turn)
		if cb.onTurnStart != nil {
			if err := cb.onTurnStart(turn); err != nil {
				return result, err
			}
		}
		observability.Debug(ctx, nil, "query.turn.start", "query.Session.run", "query turn start",
			"turn", turn,
			"messages", len(messages),
			"tools", len(registry.Definitions()),
		)
		if turn > 1 && s.promptProfile().IsCode() && !s.isClaudeCompatibleProfile() && s.runtimePolicy().DiscoverSkills {
			if msg := s.dynamicSkillCatalogMessage(ctx, skillCatalogEvidence.String(), sentSkillCatalog); msg != nil {
				messages = append(messages, *msg)
			}
		}
		requestBuildStartedAt := time.Now()
		// Tool-result externalization runs BEFORE the compaction check, not after.
		// Both shrink the context, but externalization is free (large tool results
		// move to disk, leaving a path plus a preview) while compaction costs a
		// full LLM summarization round and permanently discards detail. Checking
		// the threshold first meant a context inflated purely by one large tool
		// result paid for an LLM compaction that externalization would have made
		// unnecessary.
		messages = toolresult.ApplyMessageBudget(messages, toolresult.BudgetOptions{
			Limit:          s.options.ToolResultMessageBudget,
			Session:        s.toolResultSessionRef(),
			SkipToolNames:  registry.ToolResultBudgetSkipNames(),
			Replacements:   s.toolResultReplacements,
			SeenToolUseIDs: s.toolResultSeenIDs,
			OnReplacement:  s.recordContentReplacement,
		})
		messages = toolresult.ApplyHistoryBudget(messages, toolresult.BudgetOptions{
			Limit:          s.options.ToolResultHistoryBudget,
			Session:        s.toolResultSessionRef(),
			SkipToolNames:  registry.ToolResultBudgetSkipNames(),
			Replacements:   s.toolResultReplacements,
			SeenToolUseIDs: s.toolResultSeenIDs,
			OnReplacement:  s.recordContentReplacement,
		})
		if s.compactor != nil {
			compactCtx, compactSpan := telemetry.StartSpan(turnCtx, telemetry.Event{Name: telemetry.EventCompact, Category: telemetry.CategorySystem, Source: "query.Session.run", SessionID: s.options.TenantSessionID, Properties: map[string]any{"mode": "automatic", "turn": turn}})
			compactResult, err := s.compactor.MaybeCompact(compactCtx, s.currentModel(), system, systemBlocks, registry.Definitions(), s.computerAuditHistory(messages))
			compactStatus := telemetry.StatusOK
			if err != nil {
				compactStatus = telemetry.StatusError
			}
			compactSpan.FinishEvent(telemetry.Event{Name: telemetry.EventCompact, Category: telemetry.CategorySystem, Source: "query.Session.run", Status: compactStatus, SessionID: s.options.TenantSessionID, Properties: map[string]any{"mode": "automatic", "turn": turn, "compacted": compactResult.Compacted, "estimated_tokens": compactResult.EstimatedUsage}})
			if err != nil {
				observability.Error(ctx, nil, "query.autocompact.error", "query.Session.run", "auto compact failed",
					"turn", turn,
					"reason", compactResult.SkippedReason,
					"estimated_tokens", compactResult.EstimatedUsage,
					"error", err,
				)
				_ = s.runNotificationHook(ctx, "auto compact failed", hooks.Payload{
					Message:   "auto compact failed",
					SessionID: s.sessionIDForHook(),
					Error:     err.Error(),
				}, cb)
			} else if compactResult.Compacted {
				messages = compactResult.Messages
				s.recordPersistenceSpan(turnCtx, "compact", func() { s.recordCompact(compactResult) })
				if cb.onCompact != nil {
					if err := cb.onCompact(compactResult); err != nil {
						return result, err
					}
				}
				observability.Info(ctx, nil, "query.autocompact.success", "query.Session.run", "auto compact succeeded",
					"turn", turn,
					"trigger_tokens", compactResult.Metadata.TriggerTokens,
					"token_after", compactResult.Metadata.TokenAfter,
					"compacted_messages", compactResult.Metadata.CompactedMessages,
					"preserved_messages", compactResult.Metadata.PreservedMessages,
				)
				_ = s.runNotificationHook(ctx, "auto compact succeeded", hooks.Payload{
					Message:   "auto compact succeeded",
					SessionID: s.sessionIDForHook(),
				}, cb)
			}
		}
		messages = s.limitComputerImageHistory(messages)
		requestBaseMessages := s.withRuntimeStatusMessages(ctx, messages, runtimeStatusRequest{
			Turn:       turn,
			MaxTurns:   s.options.MaxTurns,
			UserPrompt: prompt,
			LoopStreak: loopTracker.Streak(),
			LoopCall:   loopTracker.Label(),
		})
		requestBaseMessages = s.withActiveSkillContextMessages(requestBaseMessages)
		requestBaseMessages = withPendingGateNudge(requestBaseMessages, pendingGateNudge)
		requestMessages := addMessageCacheBreakpoint(requestBaseMessages, promptCachingEnabled(s.currentModel()), false, s.querySource())
		currentModel := s.currentModel()
		toolDefinitions := registry.Definitions()
		if s.shouldDisableToolsForFinalTurn(turn, messages) {
			toolDefinitions = nil
		}
		thinkingConfig := s.currentThinkingConfig()
		request := anthropic.MessagesRequest{
			Model:           currentModel,
			MaxTokens:       s.options.MaxTokens,
			System:          system,
			SystemBlocks:    systemBlocks,
			Messages:        requestMessages,
			Tools:           toolDefinitions,
			Thinking:        thinkingConfig,
			ResponseFormat:  s.options.ResponseFormat,
			TenantSessionID: s.options.TenantSessionID,
		}
		if err := s.dumpPromptRequest(turn, request, manifest); err != nil {
			return result, err
		}
		s.observeRequestCache(ctx, request, thinkingConfig)
		requestBuildDuration := time.Since(requestBuildStartedAt)
		computerImageCount, computerImageBytes := s.transientComputerImageStats(requestMessages)
		modelCtx, modelSpan := telemetry.StartSpan(turnCtx, telemetry.Event{
			Name:      telemetry.EventModelRequest,
			Category:  telemetry.CategoryModel,
			Source:    "query.Session.run",
			Model:     currentModel,
			SessionID: s.options.TenantSessionID,
			Properties: map[string]any{
				"turn":                      turn,
				"messages":                  len(requestMessages),
				"tools":                     len(toolDefinitions),
				"computer_image_count":      computerImageCount,
				"computer_image_bytes":      computerImageBytes,
				"computer_use_fast_path":    s.options.ComputerUseFastPath,
				"request_build_duration_ms": requestBuildDuration.Milliseconds(),
			},
		})
		modelRequestStartedAt := time.Now()
		var recoveredStreamError string
		// Text deltas pass through to the callbacks LIVE during generation —
		// this is what makes the interactive typewriter real instead of a replay
		// after completion — but ONLY for frontends that can amend already-shown
		// text (onTextAmended != nil, e.g. the TUI). Sinks that cannot unprint
		// (stdout pipes, server writers) keep the buffered contract: text is
		// emitted once, at acceptance, so gate-retracted drafts never leak.
		liveStreaming := cb.onTextAmended != nil
		// Live mode: streamedTurnText records what was emitted so
		// reconcileStreamedText / the gate retraction can amend the live tail.
		var streamedTurnText strings.Builder
		emitLiveText := func(text string) error {
			streamedTurnText.WriteString(text)
			if cb.onText != nil {
				return cb.onText(text)
			}
			return nil
		}
		streamLiveText := emitLiveText
		// Forbidden output literals must be redacted from the live stream too,
		// not only from the accepted message. Fresh guard per turn: it holds
		// back a tail of runes while filtering, and that buffer must not leak
		// across turns.
		var turnTextGuard *forbiddenOutputGuard
		if liveStreaming && outputGuard != nil {
			turnTextGuard = newForbiddenOutputGuard(forbiddenLiterals, emitLiveText)
			streamLiveText = turnTextGuard.OnText
		}
		streamEvents := turnStreamCallbackBuffer{}
		// emitAcceptedTurnText runs once the turn's message is accepted: live
		// sinks only need divergence reconciliation; buffered sinks get the
		// full ordered replay now.
		emitAcceptedTurnText := func(accepted *anthropic.MessageParam) error {
			return s.renderSpan(turnCtx, "accepted_text", func() error {
				if liveStreaming {
					return reconcileStreamedText(cb, streamedTurnText.String(), accepted)
				}
				return streamEvents.Replay(cb, accepted)
			})
		}
		stream, err := s.client.StreamMessages(modelCtx, request, anthropic.StreamCallbacks{
			OnText: func(text string) error {
				if liveStreaming {
					return streamLiveText(text)
				}
				streamEvents.AppendText(text)
				return nil
			},
			OnThinking: func(text string) error {
				if liveStreaming {
					if cb.onThinking != nil {
						return cb.onThinking(text)
					}
					return nil
				}
				streamEvents.AppendThinking(text)
				return nil
			},
			OnSignature: func(signature string) error {
				if liveStreaming {
					if cb.onSignature != nil {
						return cb.onSignature(signature)
					}
					return nil
				}
				streamEvents.AppendSignature(signature)
				return nil
			},
			OnCitation: func(citation json.RawMessage) error {
				if liveStreaming {
					if cb.onCitation != nil {
						return cb.onCitation(citation)
					}
					return nil
				}
				streamEvents.AppendCitation(citation)
				return nil
			},
			OnConnectorText: func(text string) error {
				if liveStreaming {
					if cb.onConnectorText != nil {
						return cb.onConnectorText(text)
					}
					return nil
				}
				streamEvents.AppendConnectorText(text)
				return nil
			},
		})
		modelRequestDuration := time.Since(modelRequestStartedAt)
		if err != nil {
			if partial, ok := recoverPartialTextStreamResult(err); ok {
				recoveredStreamError = err.Error()
				stream = partial
				observability.Error(ctx, nil, "query.stream.partial_recovered", "query.Session.run", "model stream failed after partial text; using partial assistant response",
					"turn", turn,
					"error", err,
				)
			} else {
				modelSpan.FinishEvent(telemetry.Event{
					Name:      telemetry.EventModelRequest,
					Status:    telemetry.StatusError,
					Model:     currentModel,
					SessionID: s.options.TenantSessionID,
					Error:     err.Error(),
					Properties: map[string]any{
						"turn":                      turn,
						"computer_image_count":      computerImageCount,
						"computer_image_bytes":      computerImageBytes,
						"computer_use_fast_path":    s.options.ComputerUseFastPath,
						"request_build_duration_ms": requestBuildDuration.Milliseconds(),
						"model_request_duration_ms": modelRequestDuration.Milliseconds(),
					},
				})
				if turnTextGuard != nil {
					_ = turnTextGuard.Flush()
				}
				// Reactive overflow fallback. The local estimator can undershoot
				// (image blocks, CJK, provider-specific tokenizers), so the
				// proactive threshold check above is not a guarantee and the
				// provider can still reject the prompt as too long. Force one
				// compaction and retry the turn instead of ending the session.
				//
				// This does not race provider fallback: context overflow is a
				// deterministic 400, which canFallbackAfterError declines, so the
				// client has already exhausted its own options by the time we
				// get here.
				if next, ok := s.compactAfterOverflow(ctx, cb, err, turn, system, systemBlocks, registry.Definitions(), messages, &overflowCompacted); ok {
					messages = next
					continue
				}
				// Buffered sinks haven't seen this turn's events yet; replay the
				// partial output before returning the error. Live sinks already
				// received everything as it streamed.
				if !liveStreaming {
					_ = streamEvents.Replay(cb, nil)
				}
				observability.Error(ctx, nil, "query.stream.error", "query.Session.run", "model stream failed", "turn", turn, "error", err)
				// 落进 transcript：否则这一轮在会话文件里只剩一个 prompt_context 后面
				// 接空白，事后分不清是 provider 失败还是用户打断。
				s.recordProviderError(currentModel, err)
				return result, err
			}
		}
		modelUsage := usageFromAnthropic(stream.Usage)
		modelSpan.FinishEvent(telemetry.Event{
			Name:                                telemetry.EventModelRequest,
			Status:                              telemetry.StatusOK,
			Model:                               currentModel,
			SessionID:                           s.options.TenantSessionID,
			InputTokens:                         modelUsage.InputTokens,
			OutputTokens:                        modelUsage.OutputTokens,
			CacheCreationInputTokens:            modelUsage.CacheCreationInputTokens,
			CacheReadInputTokens:                modelUsage.CacheReadInputTokens,
			CacheCreationEphemeral1hInputTokens: modelUsage.CacheCreationEphemeral1hInputTokens,
			CacheCreationEphemeral5mInputTokens: modelUsage.CacheCreationEphemeral5mInputTokens,
			Properties: map[string]any{
				"turn":                      turn,
				"stop_reason":               stream.StopReason,
				"computer_image_count":      computerImageCount,
				"computer_image_bytes":      computerImageBytes,
				"computer_use_fast_path":    s.options.ComputerUseFastPath,
				"request_build_duration_ms": requestBuildDuration.Milliseconds(),
				"model_request_duration_ms": modelRequestDuration.Milliseconds(),
				"service_tier":              stream.Usage.ServiceTier,
				"inference_geo":             stream.Usage.InferenceGeo,
				"speed":                     stream.Usage.Speed,
				"partial_stream_recover":    recoveredStreamError != "",
			},
		})
		if turnTextGuard != nil {
			if err := turnTextGuard.Flush(); err != nil {
				return result, err
			}
		}
		if outputGuard != nil {
			stream.Message = outputGuard.SanitizeMessage(stream.Message)
		}
		if exactOutputGuard != nil && stream.StopReason != "tool_use" {
			message, err := exactOutputGuard.Complete(stream.Message)
			if err != nil {
				return result, err
			}
			stream.Message = message
		}
		result.StopReason = stream.StopReason
		turnUsage := modelUsage
		result.Usage.InputTokens += turnUsage.InputTokens
		result.Usage.OutputTokens += stream.Usage.OutputTokens
		result.Usage.LastInputTokens = turnUsage.InputTokens
		// Feed the provider's exact prompt size back into the compactor so its
		// estimator self-corrects instead of relying on a static heuristic.
		// turnUsage.InputTokens already folds in cache-read and cache-creation
		// tokens, i.e. it is the size of the whole prompt that was billed.
		if s.compactor != nil {
			s.compactor.ObservePromptTokens(turnUsage.InputTokens)
		}
		result.Usage.CacheCreationInputTokens += stream.Usage.CacheCreationInputTokens
		result.Usage.CacheReadInputTokens += stream.Usage.CacheReadInputTokens
		result.Usage.CacheCreationEphemeral1hInputTokens += turnUsage.CacheCreationEphemeral1hInputTokens
		result.Usage.CacheCreationEphemeral5mInputTokens += turnUsage.CacheCreationEphemeral5mInputTokens
		result.Usage.ServiceTier = firstNonEmpty(turnUsage.ServiceTier, result.Usage.ServiceTier)
		result.Usage.InferenceGeo = firstNonEmpty(turnUsage.InferenceGeo, result.Usage.InferenceGeo)
		result.Usage.Speed = firstNonEmpty(turnUsage.Speed, result.Usage.Speed)
		s.recordUsage(stream.Usage, turn)
		// draftIndex marks where this turn's assistant message starts, so the
		// completion gate below can retract the whole turn if it rejects the draft.
		draftIndex := len(messages)
		messages = append(messages, stream.Message)

		turnResponse := assistantText(stream.Message.Content)
		for _, block := range stream.Message.Content {
			if block.Type == blockTypeText {
				result.Response += block.Text
			}
		}

		toolUses := collectToolUses(stream.Message.Content)
		observability.Debug(ctx, nil, "query.turn.finish", "query.Session.run", "query turn finish",
			"turn", turn,
			"stop_reason", stream.StopReason,
			"input_tokens", turnUsage.InputTokens,
			"output_tokens", stream.Usage.OutputTokens,
			"tool_uses", len(toolUses),
			"turn_duration_ms", time.Since(turnStartedAt).Milliseconds(),
			"request_build_duration_ms", requestBuildDuration.Milliseconds(),
			"model_request_duration_ms", modelRequestDuration.Milliseconds(),
			"computer_image_count", computerImageCount,
			"computer_image_bytes", computerImageBytes,
		)
		if len(toolUses) == 0 {
			if turn < s.options.MaxTurns {
				nudge := completionVerificationNudge(requiredVerifications, result.ToolCalls, turnResponse)
				gateRuleID := ""
				var gateMissingEvidence []string
				if nudge == "" {
					if gate := continuationCompletionGate(continuation, result.ToolCalls, turnResponse); gate.Severity == gateSeverityBlockContinue {
						nudge = gate.Reminder
						gateRuleID = gate.RuleID
						gateMissingEvidence = gate.MissingEvidence
					}
				}
				if nudge == "" {
					gate := s.evaluateGateSpan(turnCtx, "completion", "", func() completionGateResult {
						return completionGate(contract, result.ToolCalls, turnResponse)
					})
					if gate.Severity == gateSeverityBlockContinue {
						nudge = gate.Reminder
						gateRuleID = gate.RuleID
						gateMissingEvidence = gate.MissingEvidence
					} else if gate.Severity == gateSeverityWarn {
						s.recordClosureEvent("completion_gate", map[string]any{
							"severity":         string(gate.Severity),
							"rule_id":          gate.RuleID,
							"reason":           gate.Reminder,
							"missing_evidence": gate.MissingEvidence,
						})
					}
				}
				if nudge != "" {
					result.Response = strings.TrimSuffix(result.Response, turnResponse)
					// The gated turn's text already streamed live to the UI;
					// retract it so the retry doesn't leave a discarded draft
					// on screen (empty final text = retract).
					if cb.onTextAmended != nil {
						if streamed := streamedTurnText.String(); streamed != "" {
							if err := cb.onTextAmended(streamed, ""); err != nil {
								return result, err
							}
						}
					}
					event := map[string]any{
						"severity": "block_continue",
						"reason":   nudge,
					}
					if gateRuleID != "" {
						event["rule_id"] = gateRuleID
					}
					if len(gateMissingEvidence) > 0 {
						event["missing_evidence"] = gateMissingEvidence
					}
					s.recordClosureEvent("completion_gate", event)
					// Fully retract the rejected draft: it is already gone from the
					// UI, from result.Response and from the transcript, and the only
					// copy left was the one appended to `messages` at draftIndex.
					// Dropping it keeps the request in step with everything else,
					// keeps user and assistant turns alternating now that the reminder
					// no longer sits between two drafts, and lets the loop guard below
					// recognise a re-generated identical draft as a repeat.
					messages = messages[:draftIndex]
					// The gate nudge is a request-only runtime reminder (like the
					// continuation reminder above): it drives the retry but is neither
					// persisted to the transcript (it would leak a <system-reminder>
					// into resume/inspect and the /rewind picker as if it were a real
					// user turn) nor accumulated in `messages`. The audit trail is the
					// completion_gate closure event recorded just above.
					pendingGateNudge = nudge
					observability.Info(ctx, nil, "query.completion_verification_nudge", "query.Session.run", "completion verification nudge inserted",
						"turn", turn,
						"required", len(requiredVerifications),
					)
					// Loop guard for text-only turns. The tool-execution guard below
					// is unreachable from here, so a model that keeps closing with the
					// same gate-rejected text used to burn every remaining turn with
					// no streak counting and no escalating reminder. Fingerprinting the
					// rejected draft plus the reason it was rejected feeds the very
					// same window/streak machinery: distinct retries still count as
					// progress, identical ones escalate and finally abort.
					if err := applyLoopGuard(
						loopguard.TextFingerprint(firstNonEmpty(gateRuleID, nudge), turnResponse),
						"the same final answer, which the completion gate rejected: "+compactOneLine(turnResponse, 120),
						turnResponse,
						"the same blocked final answer",
					); err != nil {
						return result, err
					}
					continue
				}
			}
			if err := emitAcceptedTurnText(&stream.Message); err != nil {
				return result, err
			}
			if cb.onMessageStop != nil {
				if err := s.renderSpan(turnCtx, "message_stop", func() error { return cb.onMessageStop(turn, stream.StopReason, usageFromAnthropic(stream.Usage)) }); err != nil {
					return result, err
				}
			}
			s.recordPersistenceSpan(turnCtx, "assistant", func() { s.recordAssistant(stream.Message.Content) })
			observability.Info(ctx, nil, "query.run.finish", "query.Session.run", "query run finish",
				"turns", result.Turns,
				"stop_reason", result.StopReason,
				"input_tokens", result.Usage.InputTokens,
				"output_tokens", result.Usage.OutputTokens,
				"tool_calls", len(result.ToolCalls),
			)
			return result, nil
		}
		if err := emitAcceptedTurnText(&stream.Message); err != nil {
			return result, err
		}
		if cb.onMessageStop != nil {
			if err := s.renderSpan(turnCtx, "message_stop", func() error { return cb.onMessageStop(turn, stream.StopReason, usageFromAnthropic(stream.Usage)) }); err != nil {
				return result, err
			}
		}
		s.recordPersistenceSpan(turnCtx, "assistant", func() { s.recordAssistant(stream.Message.Content) })
		// The model took an action instead of trying to close, so the gate reminder
		// has done its job and must not follow the conversation any further.
		pendingGateNudge = ""

		toolResults := make([]anthropic.ContentBlock, 0, len(toolUses))
		var toolContextMessages []anthropic.MessageParam
		appendCancelled := func(skipped []anthropic.ContentBlock, announced int, cause error) error {
			var callbackErr error
			for index, block := range skipped {
				trace := cancelledToolTrace(block, cause)
				result.ToolCalls = append(result.ToolCalls, trace)
				s.recordClosureTrace(trace)
				s.recordPersistenceSpan(turnCtx, "tool_result", func() { s.recordTool(trace) })
				toolResults = append(toolResults, anthropic.ContentBlock{Type: blockTypeToolResult, ToolUseID: block.ID, Content: trace.Output, IsError: true})
				if index < announced && cb.onToolResult != nil {
					if err := s.renderSpan(turnCtx, "tool_result", func() error { return cb.onToolResult(trace) }); err != nil && callbackErr == nil {
						callbackErr = err
					}
				}
			}
			s.recordRepairEvidence(result.ToolCalls)
			messages = append(messages, anthropic.MessageParam{Role: "user", Content: toolResults})
			if callbackErr != nil {
				return callbackErr
			}
			return cause
		}
		invocation := tools.Invocation{RunID: executionRunID, BatchID: toolInvocationBatchID(executionRunID, turn)}
		parallelTraces, parallelReadOnly, parallelAnnounced, parallelErr := s.runParallelReadOnlyToolTurn(turnCtx, registry, toolUses, cb, contract, prompt, result.ToolCalls, directSharedStateAuthorization, continuationAuthorization, invocation)
		if parallelErr != nil {
			return result, parallelErr
		}
		for blockIndex, block := range toolUses {
			trace := ToolTrace{}
			var contextErrAfterTrace error
			if parallelReadOnly {
				trace = parallelTraces[blockIndex]
			} else {
				if contextErr := turnCtx.Err(); contextErr != nil {
					return result, appendCancelled(toolUses[blockIndex:], 0, contextErr)
				}
				if cb.onToolCall != nil {
					if err := cb.onToolCall(redactComputerToolCall(block)); err != nil {
						return result, err
					}
				}
				if contextErr := turnCtx.Err(); contextErr != nil {
					return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
				}
				gitAnalysis := gitpolicy.Analysis{}
				if strings.EqualFold(block.Name, "Bash") {
					gitAnalysis = gitpolicy.Analyze(bashCommandFromInput(string(block.Input)))
				}
				toolAuthorization, continuationConsumeKeys := continuationAuthorization.authorizationFor(directSharedStateAuthorization, gitAnalysis)
				if contextErr := turnCtx.Err(); contextErr != nil {
					return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
				}
				gate := s.evaluateGateSpan(turnCtx, "pre_tool", block.Name, func() completionGateResult {
					return preToolClosureGateWithPolicy(contract, prompt, result.ToolCalls, block.Name, string(block.Input), s.options.CWD, toolAuthorization)
				})
				if contextErr := turnCtx.Err(); contextErr != nil {
					return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
				}
				approved, approvedOnce := s.approveSharedStateInDangerousMode(turnCtx, block, gate, gitAnalysis)
				if contextErr := turnCtx.Err(); contextErr != nil {
					return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
				}
				if approvedOnce {
					continuationAuthorization.addEffects(gitAnalysis.Effects)
					toolAuthorization, continuationConsumeKeys = continuationAuthorization.authorizationFor(directSharedStateAuthorization, gitAnalysis)
					toolAuthorization = gitpolicy.MergeAuthorizations(toolAuthorization, approved)
					if contextErr := turnCtx.Err(); contextErr != nil {
						return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
					}
					gate = s.evaluateGateSpan(turnCtx, "pre_tool", block.Name, func() completionGateResult {
						return preToolClosureGateWithPolicy(contract, prompt, result.ToolCalls, block.Name, string(block.Input), s.options.CWD, toolAuthorization)
					})
					if contextErr := turnCtx.Err(); contextErr != nil {
						return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
					}
				}
				if gate.Severity == gateSeverityBlockTool {
					if count, effectFingerprint, abort := sharedStateGateRetries.observe(gate.RuleID, gitAnalysis); abort {
						result.StopReason = "shared_state_gate_retry_abort"
						trace = blockedToolTrace(block, gate.Reminder)
						result.ToolCalls = append(result.ToolCalls, trace)
						s.recordClosureTrace(trace)
						s.recordPersistenceSpan(turnCtx, "tool_result", func() { s.recordTool(trace) })
						if cb.onToolResult != nil {
							if callbackErr := s.renderSpan(turnCtx, "tool_result", func() error { return cb.onToolResult(trace) }); callbackErr != nil {
								return result, callbackErr
							}
						}
						s.recordClosureEvent("shared_state_gate_retry", map[string]any{
							"severity":           "abort",
							"rule_id":            gate.RuleID,
							"repeat":             count,
							"hard_limit":         sharedStateGateRetryLimit,
							"effect_fingerprint": effectFingerprint,
						})
						err := fmt.Errorf("shared-state gate retry: %s blocked the same Git effect %d times", gate.RuleID, count)
						observability.Error(ctx, nil, "query.shared_state_gate_retry_abort", "query.Session.run", "query aborted by repeated shared-state gate block",
							"rule_id", gate.RuleID,
							"repeat", count,
							"effect_fingerprint", effectFingerprint,
							"error", err,
						)
						return result, err
					}
					event := map[string]any{
						"severity":  string(gate.Severity),
						"tool_name": block.Name,
						"tool_id":   block.ID,
						"reason":    gate.Reminder,
					}
					if gate.RuleID != "" {
						event["rule_id"] = gate.RuleID
					}
					if len(gate.MissingEvidence) > 0 {
						event["missing_evidence"] = gate.MissingEvidence
					}
					if gate.PreflightCommand != "" {
						if contextErr := turnCtx.Err(); contextErr != nil {
							return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
						}
						originalCommand := bashCommandFromInput(string(block.Input))
						preflightBlock := preflightToolBlock(block, gate.PreflightCommand)
						// A generated preflight must not carry an unconsumed continuation
						// capability. Otherwise a hook could rewrite the read-only preflight
						// into the confirmed Git effect and execute it without consumption.
						preflightInvocation := invocation
						preflightInvocation.ToolUseID = preflightBlock.ID
						trace = s.runToolWithInvocation(turnCtx, registry, preflightBlock, cb, directSharedStateAuthorization, preflightInvocation)
						trace.Output = gatePreflightToolOutput(gate, originalCommand, trace.Output, trace.IsError)
						event["action"] = "auto_preflight"
						event["original_command"] = originalCommand
						event["preflight_command"] = gate.PreflightCommand
						event["preflight_error"] = trace.IsError
					} else {
						trace = blockedToolTrace(block, gate.Reminder)
						event["action"] = "blocked"
					}
					s.recordClosureEvent("completion_gate", event)
				} else {
					if gate.Severity == gateSeverityWarn {
						s.recordClosureEvent("completion_gate", map[string]any{
							"severity":         string(gate.Severity),
							"tool_name":        block.Name,
							"tool_id":          block.ID,
							"rule_id":          gate.RuleID,
							"reason":           gate.Reminder,
							"missing_evidence": gate.MissingEvidence,
						})
					}
					// Consume recovered confirmation at dispatch. A hook denial, tool error,
					// or final-input rewrite cannot replay the same one-time Git capability.
					if contextErr := turnCtx.Err(); contextErr != nil {
						return result, appendCancelled(toolUses[blockIndex:], 1, contextErr)
					}
					continuationAuthorization.consume(continuationConsumeKeys)
					toolInvocation := invocation
					toolInvocation.ToolUseID = block.ID
					trace = s.runToolWithInvocation(turnCtx, registry, block, cb, toolAuthorization, toolInvocation)
				}
				contextErrAfterTrace = turnCtx.Err()
			}
			skillCatalogEvidence.WriteString("\n\n")
			skillCatalogEvidence.WriteString(block.Name)
			skillCatalogEvidence.WriteString(" input:\n")
			if trace.Input != "" {
				skillCatalogEvidence.WriteString(trace.Input)
			} else {
				skillCatalogEvidence.Write(block.Input)
			}
			skillCatalogEvidence.WriteString("\n")
			skillCatalogEvidence.WriteString(block.Name)
			skillCatalogEvidence.WriteString(" result:\n")
			skillCatalogEvidence.WriteString(trace.Output)
			bindRepairVerificationEpoch(&trace, result.ToolCalls)
			result.ToolCalls = append(result.ToolCalls, trace)
			if trace.Interaction != nil {
				if cb.onToolResult != nil {
					if err := s.renderSpan(turnCtx, "tool_result_pending", func() error { return cb.onToolResult(trace) }); err != nil {
						return result, err
					}
				}
				pending := *trace.Interaction
				pending.ToolUseID = block.ID
				pending.ToolName = block.Name
				pending.ToolInput = append([]byte(nil), block.Input...)
				pending.AssistantMessage = redactComputerAssistant(stream.Message)
				result.PendingInteraction = &pending
				result.StopReason = "waiting_input"
				return result, nil
			}
			s.recordClosureTrace(trace)
			s.recordRepairEvidence(result.ToolCalls)
			s.recordPersistenceSpan(turnCtx, "tool_result", func() { s.recordTool(trace) })
			shouldNotifyResult := !parallelReadOnly || blockIndex < parallelAnnounced
			if shouldNotifyResult && cb.onToolResult != nil {
				if err := s.renderSpan(turnCtx, "tool_result", func() error { return cb.onToolResult(trace) }); err != nil {
					return result, err
				}
			}
			toolResults = append(toolResults, anthropic.ContentBlock{
				Type:      blockTypeToolResult,
				ToolUseID: block.ID,
				Content:   trace.Output,
				IsError:   trace.IsError,
			})
			if (!trace.IsError || tools.IsComputerUseTool(trace.Name)) && len(trace.contextMessages) > 0 {
				if tools.IsComputerUseTool(trace.Name) {
					s.rememberTransientComputerImages(trace.contextMessages)
				}
				toolContextMessages = append(toolContextMessages, trace.contextMessages...)
			}
			if contextErrAfterTrace != nil {
				if blockIndex+1 < len(toolUses) {
					return result, appendCancelled(toolUses[blockIndex+1:], 0, contextErrAfterTrace)
				}
				return result, contextErrAfterTrace
			}
		}
		if parallelReadOnly {
			if contextErr := turnCtx.Err(); contextErr != nil {
				return result, contextErr
			}
		}
		messages = append(messages, anthropic.MessageParam{
			Role:    "user",
			Content: toolResults,
		})
		for _, message := range toolContextMessages {
			messages = append(messages, message)
			s.recordMessage(message)
		}
		// Loop guard, evaluated after execution once results are known: a turn
		// makes "no progress" when its (tool call + result) fingerprint already
		// appeared within the recent window, i.e. no new information entered the
		// context. Trade-offs: a degenerate loop whose output carries noise
		// (timestamps, changing ids) or whose period exceeds the window is not
		// caught here and falls back to the MaxTurns bound; a genuinely stalled
		// wait whose output never changes is still aborted, but with a readable
		// error to retry. See docs/loop_guard.md.
		if err := applyLoopGuard(
			loopguard.TurnFingerprint(toolUses, toolResults),
			loopguard.Describe(toolUses),
			loopguard.CallSignature(toolUses),
			"the same tool calls and results",
		); err != nil {
			return result, err
		}
	}
	maxTurnsErr := fmt.Errorf("max turns reached (%d)", s.options.MaxTurns)
	observability.Error(ctx, nil, "query.max_turns", "query.Session.run", "query max turns reached",
		"turns", result.Turns,
		"max_turns", s.options.MaxTurns,
		"error", maxTurnsErr,
	)
	return result, maxTurnsErr
}

func (s *Session) effectiveUserPrompt(prompt string) string {
	initial := strings.TrimSpace(s.options.InitialPrompt)
	switch {
	case initial == "":
		return prompt
	case strings.TrimSpace(prompt) == "":
		return initial
	default:
		return initial + "\n\n" + prompt
	}
}

func userMessageWithAttachments(prompt string, attachments []Attachment) (anthropic.MessageParam, string) {
	blocks := []anthropic.ContentBlock{{Type: blockTypeText, Text: prompt}}
	recordContent := prompt
	if len(attachments) == 0 {
		return anthropic.MessageParam{Role: "user", Content: blocks}, recordContent
	}
	recordLines := []string{strings.TrimSpace(prompt), "", "Attached content:"}
	for _, attachment := range attachments {
		label := attachmentRecordLabel(attachment)
		if label != "" {
			recordLines = append(recordLines, "- "+label)
		}
		if !strings.EqualFold(attachment.Type, "image") {
			continue
		}
		block, ok := imageAttachmentBlock(attachment)
		if ok {
			blocks = append(blocks, block)
		}
	}
	return anthropic.MessageParam{Role: "user", Content: blocks}, strings.TrimSpace(strings.Join(recordLines, "\n"))
}

var (
	explicitForbiddenOutputRE  = regexp.MustCompile(`(?i)\bdo\s+not\s+(?:print|output|reveal|repeat|include|emit|say)\s+(?:the\s+)?(?:exact\s+)?([A-Za-z0-9][A-Za-z0-9_.:/-]{5,})`)
	explicitExactOutputCueRE   = regexp.MustCompile(`(?i)\b(?:print|output|emit|say)\s+(?:the\s+)?exact(?:ly)?(?:\s+(?:marker|string|text|tokens?))?\s+([^\n]*)`)
	explicitIncludeOutputCueRE = regexp.MustCompile(`(?i)\b(?:final\s+answer\s+must\s+include|(?:structured\s+files?\s+and\s+)?final\s+answer\s+must\s+include|must\s+include|must\s+contain)\s+([^\n]*)`)
	explicitLiteralTokenRE     = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_.:/-]{5,}`)
)

type forbiddenOutputGuard struct {
	forbidden []string
	onText    func(string) error
	buffer    string
	holdRunes int
}

func newForbiddenOutputGuard(forbidden []string, onText func(string) error) *forbiddenOutputGuard {
	if len(forbidden) == 0 {
		return nil
	}
	holdRunes := 0
	for _, value := range forbidden {
		if n := len([]rune(value)) - 1; n > holdRunes {
			holdRunes = n
		}
	}
	return &forbiddenOutputGuard{forbidden: forbidden, onText: onText, holdRunes: holdRunes}
}

type exactOutputGuard struct {
	required []string
	onText   func(string) error
}

func newExactOutputGuard(prompt string, forbidden []string, onText func(string) error) *exactOutputGuard {
	required := extractExplicitExactOutputLiterals(prompt, forbidden)
	if len(required) == 0 {
		return nil
	}
	return &exactOutputGuard{required: required, onText: onText}
}

func extractExplicitForbiddenOutputLiterals(prompt string) []string {
	matches := explicitForbiddenOutputRE.FindAllStringSubmatch(prompt, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		value := strings.Trim(match[1], "`'\".,;:!?)]}")
		if !looksLikeExplicitLiteral(value) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func looksLikeExplicitLiteral(value string) bool {
	value = strings.TrimSpace(value)
	if len([]rune(value)) < 6 {
		return false
	}
	if strings.ContainsAny(value, "_0123456789") {
		return true
	}
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "sk-") || strings.Contains(value, ".") || strings.Contains(value, "/")
}

func extractExplicitExactOutputLiterals(prompt string, forbidden []string) []string {
	matches := append([][]string{}, explicitExactOutputCueRE.FindAllStringSubmatch(prompt, -1)...)
	matches = append(matches, explicitIncludeOutputCueRE.FindAllStringSubmatch(prompt, -1)...)
	if len(matches) == 0 {
		return nil
	}
	forbiddenSet := map[string]bool{}
	for _, value := range forbidden {
		forbiddenSet[value] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		for _, token := range explicitLiteralTokenRE.FindAllString(match[1], -1) {
			value := strings.Trim(token, "`'\".,;:!?)]}")
			if !looksLikeExplicitLiteral(value) || forbiddenSet[value] || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func (g *forbiddenOutputGuard) OnText(text string) error {
	if g == nil {
		return nil
	}
	g.buffer += text
	g.buffer = g.SanitizeText(g.buffer)
	runes := []rune(g.buffer)
	if len(runes) <= g.holdRunes {
		return nil
	}
	flushLen := len(runes) - g.holdRunes
	out := string(runes[:flushLen])
	g.buffer = string(runes[flushLen:])
	if out == "" || g.onText == nil {
		return nil
	}
	return g.onText(out)
}

func (g *forbiddenOutputGuard) Flush() error {
	if g == nil {
		return nil
	}
	out := g.SanitizeText(g.buffer)
	g.buffer = ""
	if out == "" || g.onText == nil {
		return nil
	}
	return g.onText(out)
}

func (g *forbiddenOutputGuard) SanitizeMessage(message anthropic.MessageParam) anthropic.MessageParam {
	if g == nil {
		return message
	}
	message.Content = append([]anthropic.ContentBlock(nil), message.Content...)
	for i := range message.Content {
		if message.Content[i].Type == blockTypeText {
			message.Content[i].Text = g.SanitizeText(message.Content[i].Text)
		}
	}
	return message
}

func (g *forbiddenOutputGuard) SanitizeText(text string) string {
	for _, value := range g.forbidden {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

func (g *exactOutputGuard) Complete(message anthropic.MessageParam) (anthropic.MessageParam, error) {
	if g == nil || len(g.required) == 0 {
		return message, nil
	}
	text := messageVisibleText(message)
	var missing []string
	for _, value := range g.required {
		if !strings.Contains(text, value) {
			missing = append(missing, value)
		}
	}
	if len(missing) == 0 {
		return message, nil
	}
	suffix := "\n" + strings.Join(missing, "\n")
	if g.onText != nil {
		if err := g.onText(suffix); err != nil {
			return message, err
		}
	}
	message.Content = append([]anthropic.ContentBlock(nil), message.Content...)
	for i := len(message.Content) - 1; i >= 0; i-- {
		if message.Content[i].Type == blockTypeText {
			message.Content[i].Text += suffix
			return message, nil
		}
	}
	message.Content = append(message.Content, anthropic.ContentBlock{Type: blockTypeText, Text: strings.TrimPrefix(suffix, "\n")})
	return message, nil
}

func messageVisibleText(message anthropic.MessageParam) string {
	var b strings.Builder
	for _, block := range message.Content {
		switch block.Type {
		case blockTypeText:
			b.WriteString(block.Text)
		case "connector_text":
			b.WriteString(block.ConnectorText)
		default:
			b.WriteString(block.Text)
			b.WriteString(block.Content)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func packCodeContextWithUserPrompt(initialMessages []anthropic.MessageParam, contextMessages []anthropic.MessageParam, userMessage anthropic.MessageParam) (anthropic.MessageParam, bool) {
	if len(initialMessages) != 0 || len(contextMessages) == 0 || userMessage.Role != "user" {
		return anthropic.MessageParam{}, false
	}
	var contextBlocks []anthropic.ContentBlock
	for _, message := range contextMessages {
		if message.Role != "user" || len(message.Content) == 0 {
			return anthropic.MessageParam{}, false
		}
		for _, block := range message.Content {
			if block.Type != blockTypeText {
				return anthropic.MessageParam{}, false
			}
			contextBlocks = append(contextBlocks, block)
		}
	}
	packed := userMessage
	packed.Content = append(append([]anthropic.ContentBlock(nil), contextBlocks...), userMessage.Content...)
	return packed, true
}

func imageAttachmentBlock(attachment Attachment) (anthropic.ContentBlock, bool) {
	mediaType := firstNonEmpty(strings.TrimSpace(attachment.MediaType), "image/png")
	if data := strings.TrimSpace(attachment.InlineData); data != "" {
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			return anthropic.ContentBlock{}, false
		}
		return anthropic.ContentBlock{
			Type:   "image",
			Source: &anthropic.ContentSource{Type: "base64", MediaType: mediaType, Data: data},
		}, true
	}
	if url := strings.TrimSpace(attachment.URL); url != "" {
		return anthropic.ContentBlock{
			Type: "image",
			Source: &anthropic.ContentSource{
				Type:      "url",
				URL:       url,
				MediaType: mediaType,
			},
		}, true
	}
	if strings.TrimSpace(attachment.Path) == "" {
		return anthropic.ContentBlock{}, false
	}
	data, err := os.ReadFile(attachment.Path)
	if err != nil || len(data) == 0 {
		return anthropic.ContentBlock{}, false
	}
	return anthropic.ContentBlock{
		Type: "image",
		Source: &anthropic.ContentSource{
			Type:      "base64",
			MediaType: mediaType,
			Data:      base64.StdEncoding.EncodeToString(data),
		},
	}, true
}

func attachmentRecordLabel(attachment Attachment) string {
	parts := []string{firstNonEmpty(attachment.Type, "file")}
	if attachment.Name != "" {
		parts = append(parts, attachment.Name)
	}
	if attachment.MediaType != "" {
		parts = append(parts, attachment.MediaType)
	}
	if attachment.Path != "" {
		parts = append(parts, attachment.Path)
	} else if attachment.URL != "" {
		parts = append(parts, attachment.URL)
	}
	return strings.Join(parts, " · ")
}

func (s *Session) recordPersistenceSpan(ctx context.Context, entryType string, record func()) {
	if record == nil {
		return
	}
	if s.options.Recorder == nil {
		record()
		return
	}
	_, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventPersistence,
		Category:  telemetry.CategorySession,
		Source:    "query.Session.recordPersistenceSpan",
		SessionID: s.options.TenantSessionID,
		Properties: map[string]any{
			"entry_type": entryType,
		},
	})
	record()
	span.FinishEvent(telemetry.Event{Name: telemetry.EventPersistence, Category: telemetry.CategorySession, Source: "query.Session.recordPersistenceSpan", Status: telemetry.StatusOK, SessionID: s.options.TenantSessionID, Properties: map[string]any{"entry_type": entryType}})
}

func (s *Session) renderSpan(ctx context.Context, renderer string, render func() error) error {
	if render == nil {
		return nil
	}
	_, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventRenderer,
		Category:  telemetry.CategorySystem,
		Source:    "query.Session.renderSpan",
		SessionID: s.options.TenantSessionID,
		Properties: map[string]any{
			"renderer": renderer,
		},
	})
	err := render()
	status := telemetry.StatusOK
	if err != nil {
		status = telemetry.StatusError
	}
	span.FinishEvent(telemetry.Event{Name: telemetry.EventRenderer, Category: telemetry.CategorySystem, Source: "query.Session.renderSpan", Status: status, SessionID: s.options.TenantSessionID, Properties: map[string]any{"renderer": renderer}})
	return err
}

func (s *Session) recordAssistant(blocks []anthropic.ContentBlock) {
	if s.options.Recorder == nil {
		return
	}
	for _, block := range blocks {
		switch block.Type {
		case blockTypeText:
			if block.Text != "" {
				_ = s.options.Recorder.Append(session.Entry{Type: "message", Role: "assistant", Content: block.Text})
			}
		case blockTypeThinking, blockTypeRedactedThinking:
			if block.Thinking != "" || block.Signature != "" {
				_ = s.options.Recorder.Append(session.Entry{Type: block.Type, Role: "assistant", Content: block.Thinking, Signature: block.Signature})
			}
		case blockTypeProviderContinuation:
			if block.Continuation != nil {
				_ = session.AppendProviderContinuation(s.options.Recorder, *block.Continuation)
			}
		case blockTypeToolUse:
			_ = s.options.Recorder.Append(session.Entry{Type: "tool_call", ToolID: block.ID, ToolName: block.Name, Content: string(tools.RedactToolInput(block.Name, block.Input))})
		}
	}
}

func (s *Session) recordTool(trace ToolTrace) {
	if s.options.Recorder == nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:     blockTypeToolResult,
		ToolID:   trace.ID,
		ToolName: trace.Name,
		Content:  trace.Output,
		IsError:  trace.IsError,
	})
}

// recordProviderError 把 provider 侧的硬失败写进 transcript。纯诊断记录：resume 时
// 会被跳过，不参与模型上下文，也不参与 tool_call/tool_result 配对修复。
func (s *Session) recordProviderError(model string, err error) {
	if s.options.Recorder == nil || err == nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:    "provider_error",
		Model:   model,
		Content: err.Error(),
		IsError: true,
	})
}

func (s *Session) recordClosureEvent(eventType string, value any) {
	if s.options.Recorder == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:    eventType,
		Content: string(data),
	})
}

func (s *Session) recordClosureTrace(trace ToolTrace) {
	s.recordClosureEvent("action_record", closureActionRecord(trace))
	for _, evidence := range closureEvidenceRecords(trace) {
		s.recordClosureEvent("evidence_record", evidence)
	}
	if trace.verification != nil {
		s.recordClosureEvent("repair_verification", trace.verification)
	}
	verified := false
	if len(trace.FileChanges) == 0 && strings.EqualFold(trace.Name, "Bash") {
		command := strings.ToLower(bashCommandFromTrace(trace))
		verified = strings.Contains(command, "git status --short") ||
			strings.Contains(command, "git diff --name-status") ||
			strings.Contains(command, "git diff --summary") ||
			isSemanticCheckCommand(command)
	}
	for _, delta := range closureDeltaRecords(trace, verified) {
		s.recordClosureEvent("delta_record", delta)
	}
}

func bindRepairVerificationEpoch(trace *ToolTrace, prior []ToolTrace) {
	if trace == nil || trace.verification == nil {
		return
	}
	calls := make([]ToolTrace, 0, len(prior)+1)
	calls = append(calls, prior...)
	calls = append(calls, *trace)
	ledger := repairEvidenceLedger(calls)
	if len(ledger.Evidence) > 0 {
		trace.verification.ContentEpoch = ledger.Evidence[len(ledger.Evidence)-1].ContentEpoch
	}
}

func (s *Session) recordRepairEvidence(calls []ToolTrace) {
	if s.options.Recorder == nil || !repair.CurrentEnforcementMode().RecordsEvidence() || len(calls) == 0 {
		return
	}
	last := calls[len(calls)-1]
	if !strings.EqualFold(last.Name, "Bash") {
		return
	}
	command := bashCommandFromTrace(last)
	if last.verification == nil && !isSemanticCheckCommand(command) {
		return
	}
	ledger := repairEvidenceLedger(calls)
	if len(ledger.Evidence) == 0 {
		return
	}
	s.recordClosureEvent("repair_evidence", ledger.Evidence[len(ledger.Evidence)-1])
}

func (s *Session) recordContentReplacement(record toolresult.ReplacementRecord) {
	if strings.TrimSpace(record.Kind) != "tool-result" || strings.TrimSpace(record.ToolUseID) == "" || record.Replacement == "" {
		return
	}
	if s.toolResultReplacements == nil {
		s.toolResultReplacements = map[string]string{}
	}
	if existing, ok := s.toolResultReplacements[record.ToolUseID]; ok && existing == record.Replacement {
		return
	}
	s.toolResultReplacements[record.ToolUseID] = record.Replacement
	if s.options.Recorder == nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type: "content_replacement",
		Replacements: []session.ReplacementRecord{{
			Kind:        record.Kind,
			ToolUseID:   record.ToolUseID,
			Replacement: record.Replacement,
		}},
	})
}

func (s *Session) recordMessage(message anthropic.MessageParam) {
	if s.options.Recorder == nil || message.Role != "user" {
		return
	}
	for _, block := range message.Content {
		s.recordUserContentBlock(block)
	}
}

// recordUserContentBlock 把一条 user 消息里的单个内容块落到 transcript。
// recordMessage 与 recordCompactMessage 共用它，两条路径不能对「什么会被持久化」
// 有不同答案 —— 图片就是在其中一条上漏掉才有了 TODO-080。
func (s *Session) recordUserContentBlock(block anthropic.ContentBlock) {
	switch block.Type {
	case blockTypeText:
		if block.Text != "" {
			_ = s.options.Recorder.Append(session.Entry{Type: "message", Role: "user", Content: block.Text})
		}
	case blockTypeToolResult:
		_ = s.options.Recorder.Append(session.Entry{
			Type:    blockTypeToolResult,
			ToolID:  block.ToolUseID,
			Content: block.Content,
			IsError: block.IsError,
		})
	case blockTypeImage:
		s.recordImageBlock(block)
	}
}

// recordImageBlock 把 MCP 送来的图片外化到 transcript 旁边的侧车文件，日志里只留
// 一条引用（TODO-080）。base64 直接内联会让 .jsonl 按张膨胀到 MB 级，而 transcript
// 是每次读会话都要整文件扫回来的。
//
// 落盘失败不算致命：本轮图片已经在消息序列里送给模型了，这里只影响 resume。
// 退化成记下解释性占位文本，即 TODO-061 之前的行为，而不是丢掉整块。
func (s *Session) recordImageBlock(block anthropic.ContentBlock) {
	if s.isTransientComputerImage(block) {
		return
	}
	if block.Source == nil || block.Source.Data == "" {
		if block.Text != "" {
			_ = s.options.Recorder.Append(session.Entry{Type: "message", Role: "user", Content: block.Text})
		}
		return
	}
	ref, err := session.PersistMedia(s.options.Recorder.SessionID, s.options.Recorder.Path, block.Source.MediaType, block.Source.Data)
	if err != nil {
		observability.Error(context.Background(), nil, "query.record.image_persist_failed", "query.Session.recordImageBlock",
			"could not persist image payload next to the transcript; it will not survive resume",
			"media_type", block.Source.MediaType,
			"base64_bytes", len(block.Source.Data),
			"error", err,
		)
		if block.Text != "" {
			_ = s.options.Recorder.Append(session.Entry{Type: "message", Role: "user", Content: block.Text})
		}
		return
	}
	metadata, err := session.MarshalMediaRef(ref)
	if err != nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:     blockTypeImage,
		Role:     "user",
		Content:  block.Text,
		Metadata: metadata,
	})
}

func (s *Session) recordUsage(usage anthropic.Usage, turns ...int) {
	if s.options.Recorder == nil {
		return
	}
	input := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if input == 0 && usage.OutputTokens == 0 {
		return
	}
	turn := 0
	if len(turns) > 0 {
		turn = turns[0]
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:                                "usage",
		Turn:                                turn,
		UsageSource:                         "provider",
		Model:                               s.options.Model,
		InputTokens:                         usage.InputTokens,
		CacheCreationInputTokens:            usage.CacheCreationInputTokens,
		CacheReadInputTokens:                usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: firstNonZero(usage.CacheCreationEphemeral1hInputTokens, usage.CacheCreation.Ephemeral1hInputTokens),
		CacheCreationEphemeral5mInputTokens: firstNonZero(usage.CacheCreationEphemeral5mInputTokens, usage.CacheCreation.Ephemeral5mInputTokens),
		ServiceTier:                         usage.ServiceTier,
		InferenceGeo:                        usage.InferenceGeo,
		Speed:                               usage.Speed,
		OutputTokens:                        usage.OutputTokens,
		ReasoningOutputTokens:               usage.ReasoningOutputTokens,
	})
}

func (s *Session) recordCompact(result compact.Result) {
	if s.options.Recorder == nil || !result.Compacted {
		return
	}
	metadataValue := result.Metadata
	if entries, err := session.LoadConversation(s.options.Recorder.Path); err == nil {
		provenance := session.NewSummaryProvenance(entries, nil)
		metadataValue.SourceEntryStartID = provenance.SourceStartID
		metadataValue.SourceEntryEndID = provenance.SourceEndID
		metadataValue.SourceEntryCount = provenance.SourceEntryCount
		metadataValue.SourceEntryDigest = provenance.SourceDigest
	}
	metadata := compact.MetadataJSON(metadataValue)
	_ = s.options.Recorder.Append(session.Entry{
		Type:            "compact_summary",
		Content:         result.PersistedSummary,
		CompactMetadata: json.RawMessage(metadata),
	})
	for _, message := range result.Messages[1:] {
		s.recordCompactMessage(message)
	}
}

// compactAfterOverflow forces a compaction when the provider rejected the
// request as longer than the model's context window. It reports the replacement
// message list and true when the caller should retry the turn. `attempted`
// guards against retry loops when compaction cannot shrink the prompt enough.
func (s *Session) compactAfterOverflow(ctx context.Context, cb runCallbacks, streamErr error, turn int, system string, systemBlocks []anthropic.SystemBlock, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam, attempted *bool) ([]anthropic.MessageParam, bool) {
	if s.compactor == nil || *attempted || !compact.IsContextOverflowError(streamErr) {
		return nil, false
	}
	*attempted = true
	compactCtx, compactSpan := telemetry.StartSpan(ctx, telemetry.Event{Name: telemetry.EventCompact, Category: telemetry.CategorySystem, Source: "query.Session.compactAfterOverflow", SessionID: s.options.TenantSessionID, Properties: map[string]any{"mode": "overflow", "turn": turn}})
	compactResult, err := s.compactor.ForceCompact(compactCtx, s.currentModel(), system, systemBlocks, tools, s.computerAuditHistory(messages))
	compactStatus := telemetry.StatusOK
	if err != nil || !compactResult.Compacted {
		compactStatus = telemetry.StatusError
	}
	compactSpan.FinishEvent(telemetry.Event{Name: telemetry.EventCompact, Category: telemetry.CategorySystem, Source: "query.Session.compactAfterOverflow", Status: compactStatus, SessionID: s.options.TenantSessionID, Properties: map[string]any{"mode": "overflow", "turn": turn, "compacted": compactResult.Compacted, "estimated_tokens": compactResult.EstimatedUsage}})
	if err != nil || !compactResult.Compacted {
		reason := compactResult.SkippedReason
		observability.Error(ctx, nil, "query.autocompact.overflow_failed", "query.Session.run", "context overflow recovery could not compact",
			"turn", turn,
			"reason", reason,
			"overflow_error", streamErr,
			"error", err,
		)
		return nil, false
	}
	s.recordPersistenceSpan(ctx, "compact", func() { s.recordCompact(compactResult) })
	if cb.onCompact != nil {
		// A callback error here would mask the real overflow error, so surface
		// the overflow instead of the callback failure and let the caller fail.
		if cbErr := cb.onCompact(compactResult); cbErr != nil {
			observability.Error(ctx, nil, "query.autocompact.overflow_callback_failed", "query.Session.run", "compact callback failed during overflow recovery",
				"turn", turn,
				"error", cbErr,
			)
			return nil, false
		}
	}
	observability.Info(ctx, nil, "query.autocompact.overflow_recovered", "query.Session.run", "context overflow triggered forced compaction; retrying turn",
		"turn", turn,
		"trigger_tokens", compactResult.Metadata.TriggerTokens,
		"token_after", compactResult.Metadata.TokenAfter,
		"compacted_messages", compactResult.Metadata.CompactedMessages,
		"preserved_messages", compactResult.Metadata.PreservedMessages,
	)
	telemetry.Emit(ctx, telemetry.Event{
		Name:      "query.autocompact.overflow_recovered",
		Category:  telemetry.CategoryModel,
		Source:    "query.Session.run",
		Status:    telemetry.StatusOK,
		Model:     s.currentModel(),
		SessionID: s.options.TenantSessionID,
		Properties: map[string]any{
			"turn":               turn,
			"trigger_tokens":     compactResult.Metadata.TriggerTokens,
			"token_after":        compactResult.Metadata.TokenAfter,
			"compacted_messages": compactResult.Metadata.CompactedMessages,
		},
	})
	_ = s.runNotificationHook(ctx, "auto compact recovered context overflow", hooks.Payload{
		Message:   "auto compact recovered context overflow",
		SessionID: s.sessionIDForHook(),
	}, cb)
	return compactResult.Messages, true
}

func (s *Session) recordCompactMessage(message anthropic.MessageParam) {
	if s.options.Recorder == nil {
		return
	}
	switch message.Role {
	case "user":
		for _, block := range message.Content {
			s.recordUserContentBlock(block)
		}
	case "assistant":
		s.recordAssistant(message.Content)
	}
}

// usageFromAnthropic sums every input tier into InputTokens on purpose: that is
// the full prompt size, which is what the context-window estimator and
// compaction thresholds are calibrated against. Do not "fix" it to exclude cache
// tiers — cost is priced from the tiers kept alongside it, via
// session.ReportedUsage.Tiers (AUDIT-P0-15).
func usageFromAnthropic(usage anthropic.Usage) Usage {
	inputTokens := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if usage.InputTokensIncludeCacheRead {
		inputTokens = usage.InputTokens + usage.CacheCreationInputTokens
	}
	return Usage{
		InputTokens:                         inputTokens,
		OutputTokens:                        usage.OutputTokens,
		CacheCreationInputTokens:            usage.CacheCreationInputTokens,
		CacheReadInputTokens:                usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: firstNonZero(usage.CacheCreationEphemeral1hInputTokens, usage.CacheCreation.Ephemeral1hInputTokens),
		CacheCreationEphemeral5mInputTokens: firstNonZero(usage.CacheCreationEphemeral5mInputTokens, usage.CacheCreation.Ephemeral5mInputTokens),
		ServiceTier:                         usage.ServiceTier,
		InferenceGeo:                        usage.InferenceGeo,
		Speed:                               usage.Speed,
	}
}

func addMessageCacheBreakpoint(messages []anthropic.MessageParam, enablePromptCaching bool, skipCacheWrite bool, querySource string) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].Content = append([]anthropic.ContentBlock(nil), out[i].Content...)
	}
	if !enablePromptCaching || len(out) == 0 {
		return out
	}
	markerIndex := len(out) - 1
	if skipCacheWrite {
		markerIndex = len(out) - 2
	}
	if markerIndex < 0 || markerIndex >= len(out) {
		return out
	}
	for i := range out {
		for j := range out[i].Content {
			out[i].Content[j].CacheControl = nil
		}
	}
	content := out[markerIndex].Content
	for i := len(content) - 1; i >= 0; i-- {
		switch content[i].Type {
		case blockTypeText, blockTypeToolResult, blockTypeToolUse, "":
			content[i].CacheControl = cacheControlForScope("", querySource)
			out[markerIndex].Content = content
			return out
		}
	}
	return out
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func (s *Session) evaluateGateSpan(ctx context.Context, gateType, toolName string, evaluate func() completionGateResult) completionGateResult {
	_, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventGateEvaluation,
		Category:  telemetry.CategorySystem,
		Source:    "query.Session.evaluateGateSpan",
		SessionID: s.options.TenantSessionID,
		ToolName:  toolName,
		Properties: map[string]any{
			"gate_type": gateType,
		},
	})
	result := evaluate()
	status := telemetry.StatusOK
	if result.Severity == gateSeverityBlockTool || result.Severity == gateSeverityBlockContinue {
		status = telemetry.StatusBlocked
	}
	span.FinishEvent(telemetry.Event{
		Name:      telemetry.EventGateEvaluation,
		Category:  telemetry.CategorySystem,
		Source:    "query.Session.evaluateGateSpan",
		Status:    status,
		SessionID: s.options.TenantSessionID,
		ToolName:  toolName,
		Properties: map[string]any{
			"gate_type": gateType,
			"severity":  result.Severity,
			"rule_id":   result.RuleID,
		},
	})
	return result
}

func (s *Session) runParallelReadOnlyToolTurn(
	ctx context.Context,
	registry *tools.Registry,
	blocks []anthropic.ContentBlock,
	cb runCallbacks,
	contract taskContract,
	prompt string,
	priorCalls []ToolTrace,
	directAuthorization gitpolicy.Authorization,
	continuation *continuationAuthorizationGrant,
	invocation tools.Invocation,
) ([]ToolTrace, bool, int, error) {
	workers, parallelEnabled := EffectiveMaxParallelReadOnlyTools(s.options.MaxParallelReadOnlyTools)
	if len(blocks) < 2 || !parallelEnabled {
		return nil, false, 0, nil
	}
	// Hook protocol events share their renderer with the rest of the stream.
	// Keep the entire turn serial when they are requested so callbacks retain
	// model order and never write to a JSON encoder concurrently.
	if s.options.IncludeHookEvents {
		return nil, false, 0, nil
	}
	if s.hasHookEvent(hooks.PreToolUse) || s.hasHookEvent(hooks.PostToolUse) || s.hasHookEvent(hooks.PostToolUseFailure) {
		return nil, false, 0, nil
	}
	parallelContext := tools.Context{
		CWD:                   s.options.CWD,
		WritableRoots:         s.options.WritableRoots,
		SessionAllow:          append([]string(nil), s.sessionAllow...),
		SessionDeny:           append([]string(nil), s.sessionDeny...),
		ActiveSkill:           s.activeSkill,
		RuntimePermissionMode: s.options.RuntimePermissionMode,
	}
	authorizations := make([]gitpolicy.Authorization, len(blocks))
	consumeKeys := make([][]string, len(blocks))
	for i, block := range blocks {
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, 0, nil
		}
		parallelContext.Invocation = invocation
		parallelContext.Invocation.ToolUseID = block.ID
		if !registry.ParallelSafe(block.Name, block.Input, parallelContext) {
			if contextErr := ctx.Err(); contextErr != nil {
				return cancelledToolTraces(blocks, contextErr), true, 0, nil
			}
			return nil, false, 0, nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, 0, nil
		}
		authorizations[i], consumeKeys[i] = continuation.authorizationFor(directAuthorization, gitpolicy.Analysis{})
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, 0, nil
		}
		gate := preToolClosureGateWithPolicy(contract, prompt, priorCalls, block.Name, string(block.Input), s.options.CWD, authorizations[i])
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, 0, nil
		}
		if gate.Severity != gateSeverityAllow {
			return nil, false, 0, nil
		}
	}
	announced := 0
	for i, block := range blocks {
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, announced, nil
		}
		if cb.onToolCall != nil {
			if err := cb.onToolCall(redactComputerToolCall(block)); err != nil {
				announced++
				if contextErr := ctx.Err(); contextErr != nil {
					return cancelledToolTraces(blocks, contextErr), true, announced, nil
				}
				return nil, false, announced, err
			}
		}
		announced++
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, announced, nil
		}
		gate := s.evaluateGateSpan(ctx, "pre_tool", block.Name, func() completionGateResult {
			return preToolClosureGateWithPolicy(contract, prompt, priorCalls, block.Name, string(block.Input), s.options.CWD, authorizations[i])
		})
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, announced, nil
		}
		if gate.Severity != gateSeverityAllow {
			return nil, false, announced, errors.New("parallel read-only gate changed during dispatch")
		}
	}
	for _, keys := range consumeKeys {
		if contextErr := ctx.Err(); contextErr != nil {
			return cancelledToolTraces(blocks, contextErr), true, announced, nil
		}
		continuation.consume(keys)
	}
	if workers > len(blocks) {
		workers = len(blocks)
	}
	semaphore := make(chan struct{}, workers)
	traces := make([]ToolTrace, len(blocks))
	var wait sync.WaitGroup
	for i := range blocks {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				traces[index] = ToolTrace{ID: blocks[index].ID, Name: blocks[index].Name, Input: string(tools.RedactToolInput(blocks[index].Name, blocks[index].Input)), Output: ctx.Err().Error(), IsError: true}
				return
			}
			if contextErr := ctx.Err(); contextErr != nil {
				traces[index] = cancelledToolTrace(blocks[index], contextErr)
				return
			}
			toolInvocation := invocation
			toolInvocation.ToolUseID = blocks[index].ID
			traces[index] = s.runToolWithInvocation(ctx, registry, blocks[index], cb, authorizations[index], toolInvocation)
		}(i)
	}
	wait.Wait()
	return traces, true, announced, nil
}

func (s *Session) runTool(ctx context.Context, registry *tools.Registry, block anthropic.ContentBlock, cb runCallbacks) ToolTrace {
	return s.runToolWithAuthorization(ctx, registry, block, cb, gitpolicy.Authorization{})
}

func (s *Session) runToolWithAuthorization(ctx context.Context, registry *tools.Registry, block anthropic.ContentBlock, cb runCallbacks, sharedStateAuthorization gitpolicy.Authorization) ToolTrace {
	runID, err := newExecutionRunID(s.options.RunID)
	if err != nil {
		return ToolTrace{ID: block.ID, Name: block.Name, Input: string(tools.RedactToolInput(block.Name, block.Input)), Output: err.Error(), IsError: true}
	}
	return s.runToolWithInvocation(ctx, registry, block, cb, sharedStateAuthorization, tools.Invocation{RunID: runID, ToolUseID: block.ID})
}

func (s *Session) runToolWithInvocation(ctx context.Context, registry *tools.Registry, block anthropic.ContentBlock, cb runCallbacks, sharedStateAuthorization gitpolicy.Authorization, invocation tools.Invocation) ToolTrace {
	trace := ToolTrace{
		ID:    block.ID,
		Name:  block.Name,
		Input: string(tools.RedactToolInput(block.Name, block.Input)),
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	tool, toolFound := registry.Get(block.Name)
	executionPolicy := tools.ExecutionPolicyFor(tool)
	startSkillProperties := s.activeSkillTelemetryProperties()
	ctx, toolSpan := telemetry.StartSpan(ctx, telemetry.Event{
		Name:         telemetry.EventToolExecution,
		Category:     telemetry.CategoryTool,
		Source:       "query.Session.runTool",
		SessionID:    s.options.TenantSessionID,
		ResourceType: "tool_call",
		ResourceID:   block.ID,
		ToolName:     block.Name,
		Properties: map[string]any{
			"input_bytes":           len(block.Input),
			"concurrency_class":     executionPolicy.Concurrency,
			"active_skill":          startSkillProperties["active_skill"],
			"active_skill_source":   startSkillProperties["active_skill_source"],
			"active_skill_version":  startSkillProperties["active_skill_version"],
			"active_skill_fallback": startSkillProperties["active_skill_fallback"],
		},
	})
	defer func() {
		status := telemetry.StatusOK
		if trace.IsError {
			status = telemetry.StatusError
		}
		finishSkillProperties := s.activeSkillTelemetryProperties()
		event := telemetry.Event{
			Name:         telemetry.EventToolExecution,
			Category:     telemetry.CategoryTool,
			Source:       "query.Session.runTool",
			Status:       status,
			SessionID:    s.options.TenantSessionID,
			ResourceType: "tool_call",
			ResourceID:   block.ID,
			ToolName:     block.Name,
			Properties: map[string]any{
				"input_bytes":           len(block.Input),
				"output_bytes":          len(trace.Output),
				"concurrency_class":     executionPolicy.Concurrency,
				"active_skill":          finishSkillProperties["active_skill"],
				"active_skill_source":   finishSkillProperties["active_skill_source"],
				"active_skill_version":  finishSkillProperties["active_skill_version"],
				"active_skill_fallback": finishSkillProperties["active_skill_fallback"],
			},
		}
		if trace.IsError {
			event.Error = trace.Output
		}
		toolSpan.FinishEvent(event)
	}()
	observability.Info(ctx, nil, "tool.run.start", "query.Session.runTool", "tool run start",
		"tool_id", block.ID,
		"tool_name", block.Name,
		"input_bytes", len(block.Input),
	)
	if !toolFound {
		trace.IsError = true
		trace.Output = "unknown tool: " + block.Name
		observability.Error(ctx, nil, "tool.run.error", "query.Session.runTool", "unknown tool",
			"tool_id", block.ID,
			"tool_name", block.Name,
			"error", trace.Output,
		)
		return trace
	}
	input := block.Input
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	if cb.onHookStart != nil {
		if err := cb.onHookStart(hooks.PreToolUse, block.Name); err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return cancelledToolTrace(block, contextErr)
			}
			return ToolTrace{ID: block.ID, Name: block.Name, Input: string(tools.RedactToolInput(block.Name, block.Input)), Output: err.Error(), IsError: true}
		}
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	hookResult, err := s.runHookSpan(ctx, hooks.PreToolUse, hooks.Payload{ToolName: block.Name, Input: block.Input})
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	if cb.onHookResult != nil {
		if callbackErr := cb.onHookResult(hooks.PreToolUse, block.Name, hookResult, err); callbackErr != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return cancelledToolTrace(block, contextErr)
			}
			return ToolTrace{ID: block.ID, Name: block.Name, Input: string(tools.RedactToolInput(block.Name, block.Input)), Output: callbackErr.Error(), IsError: true}
		}
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	if err != nil {
		trace.IsError = true
		trace.Output = err.Error()
		observability.Error(ctx, nil, "tool.hook.pre_error", "query.Session.runTool", "pre tool hook blocked tool",
			"tool_id", block.ID,
			"tool_name", block.Name,
			"error", err,
		)
		return trace
	}
	if strings.EqualFold(hookResult.PermissionDecision, "deny") {
		trace.IsError = true
		trace.Output = firstNonEmpty(hookResult.PermissionDecisionReason, hookResult.Message, "tool denied by PreToolUse hook")
		return trace
	}
	if len(hookResult.UpdatedInput) > 0 && !tools.IsComputerUseTool(block.Name) {
		input = hookResult.UpdatedInput
		trace.Input = string(input)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	if decision := toolpolicy.Check(block.Name, input, s.options.CWD, sharedStateAuthorization); decision != nil {
		trace.IsError = true
		trace.Output = decision.Message
		return trace
	}
	var fileChanges []tools.FileChange
	if contextErr := ctx.Err(); contextErr != nil {
		return cancelledToolTrace(block, contextErr)
	}
	res := tool.Run(ctx, input, tools.Context{
		CWD:           s.options.CWD,
		WritableRoots: s.options.WritableRoots,
		Sandbox:       s.options.Sandbox,
		SessionAllow:  append([]string(nil), s.sessionAllow...),
		SessionDeny:   append([]string(nil), s.sessionDeny...),
		ActiveSkill:   s.activeSkill,
		TaskStore:     s.options.TaskStore,
		AgentMessages: func(taskID uint64) []agenttasks.MessageInput {
			return s.pendingAgentMessages(context.WithoutCancel(ctx), taskID)
		},
		TenantID:                  s.options.TenantID,
		UserID:                    s.options.UserID,
		SessionID:                 s.options.TenantSessionID,
		TraceID:                   s.options.TraceID,
		Invocation:                invocation,
		ImageGenerator:            s.options.ImageGenerator,
		ComputerUse:               s.options.ComputerUse,
		ComputerUseImageSupported: s.options.ComputerUseImageSupported,
		ComputerUseFastPath:       s.options.ComputerUseFastPath,
		AgentBudget:               s.agentBudget,
		SharedStateAuthorization:  sharedStateAuthorization,
		PermissionPrompt: func(promptCtx context.Context, req tools.PermissionPromptRequest) tools.PermissionPromptResponse {
			if contextErr := promptCtx.Err(); contextErr != nil {
				return tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
			}
			response := s.runPermissionPrompt(promptCtx, req)
			if contextErr := promptCtx.Err(); contextErr != nil {
				return tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
			}
			return response
		},
		PermissionAudit: func(audit tools.PermissionAudit) {
			s.recordPermission(ctx, audit)
		},
		PermissionUpdate: func(update tools.PermissionUpdate) error {
			return s.applyPermissionUpdate(update)
		},
		RuntimePermissionMode: s.options.RuntimePermissionMode,
		UserQuestion:          s.options.UserQuestionPrompt,
		FileChange: func(change tools.FileChange) {
			fileChanges = append(fileChanges, change)
			s.recordFileChange(change)
		},
		TaskController: s.options.TaskController,
		SkillProvider:  s.options.SkillProvider,
		TaskProgress: func(event agenttasks.EventInput) {
			if s.options.NestedAgentProgress != nil {
				s.options.NestedAgentProgress(event)
			}
			if cb.onNestedAgentProgress != nil {
				_ = cb.onNestedAgentProgress(event)
			}
		},
	})
	if tools.IsComputerUseTool(block.Name) && res.Interaction != nil {
		res.Interaction = nil
		res.IsError = true
		res.Content = "ComputerUse cannot create a resumable interaction"
	}
	if contextErr := ctx.Err(); contextErr != nil {
		bindToolResult(&trace, res, fileChanges)
		if res.IsError {
			trace.Output = contextErr.Error()
			trace.IsError = true
		}
		return trace
	}
	event := hooks.PostToolUse
	if res.IsError {
		event = hooks.PostToolUseFailure
	}
	if cb.onHookStart != nil {
		if err := cb.onHookStart(event, block.Name); err != nil {
			res = toolHookFailure(block.Name, res, err)
		}
	}
	if contextErr := ctx.Err(); contextErr != nil {
		bindToolResult(&trace, res, fileChanges)
		return trace
	}
	postResult, postErr := s.runHookSpan(ctx, event, hooks.Payload{ToolName: block.Name, Input: input, Result: res.Content, IsError: res.IsError})
	if contextErr := ctx.Err(); contextErr != nil {
		bindToolResult(&trace, res, fileChanges)
		return trace
	}
	if cb.onHookResult != nil {
		if callbackErr := cb.onHookResult(event, block.Name, postResult, postErr); callbackErr != nil && !res.IsError {
			postErr = callbackErr
		}
	}
	if contextErr := ctx.Err(); contextErr != nil {
		bindToolResult(&trace, res, fileChanges)
		return trace
	}
	if postErr != nil && !res.IsError {
		res = toolHookFailure(block.Name, res, postErr)
		observability.Error(ctx, nil, "tool.hook.post_error", "query.Session.runTool", "post tool hook failed",
			"tool_id", block.ID,
			"tool_name", block.Name,
			"error", postErr,
		)
	}
	res.Content = toolresult.Process(res.Content, toolresult.ProcessOptions{
		ToolName:  block.Name,
		ToolUseID: block.ID,
		Limit:     tools.EffectiveResultLimit(tool, s.options.ToolResultLimit),
		Session:   s.toolResultSessionRef(),
	})
	if !res.IsError && tools.IsComputerUseTool(block.Name) {
		trace.ComputerObservation = s.persistComputerObservation(ctx, res.Content, res.ContextMessages)
	}
	if res.IsError && (res.Verification == nil || !res.Verification.MatchedExpectation) {
		res.Content = appendVerificationFailureRecoveryReminder(block.Name, input, res.Content)
	} else {
		res.Content = appendPostEditVerificationReminder(block.Name, input, res.Content, fileChanges)
	}
	bindToolResult(&trace, res, fileChanges)
	s.toolStateMu.Lock()
	if !trace.IsError && block.Name == "Skill" && len(trace.contextMessages) > 0 {
		s.rememberActiveSkillMessages(trace.contextMessages)
	}
	if !trace.IsError && strings.EqualFold(block.Name, "AgentGet") {
		s.acknowledgeAgentGetResult(trace.Output)
	}
	if !strings.EqualFold(block.Name, "AgentGet") {
		s.rememberCapabilityLoopToolResult(block.Name, block.ID, input, trace.Output)
	}
	activated := s.applySkillRuntime(ctx, block.Name, input)
	s.toolStateMu.Unlock()
	if activated != nil && cb.onSkillActivated != nil {
		if err := cb.onSkillActivated(activated); err != nil {
			trace.IsError = true
			trace.Output = err.Error()
		}
	}
	if trace.IsError {
		observability.Error(ctx, nil, "tool.run.error", "query.Session.runTool", "tool run failed",
			"tool_id", block.ID,
			"tool_name", block.Name,
			"output_bytes", len(trace.Output),
		)
		return trace
	}
	observability.Info(ctx, nil, "tool.run.finish", "query.Session.runTool", "tool run finish",
		"tool_id", block.ID,
		"tool_name", block.Name,
		"output_bytes", len(trace.Output),
	)
	return trace
}

func (s *Session) persistComputerObservation(ctx context.Context, content string, messages []anthropic.MessageParam) *ComputerObservationReference {
	if s.options.ImageBlobStore == nil || s.options.MediaAssetStore == nil || s.options.TenantID == 0 || s.options.UserID == 0 || s.options.TenantSessionID == 0 {
		return nil
	}
	var payload struct {
		Observation struct {
			ID string `json:"id"`
		} `json:"observation"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil || strings.TrimSpace(payload.Observation.ID) == "" {
		return nil
	}
	var source *anthropic.ContentSource
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == blockTypeImage && block.Source != nil && strings.TrimSpace(block.Source.Data) != "" {
				source = block.Source
				break
			}
		}
		if source != nil {
			break
		}
	}
	if source == nil || !strings.EqualFold(source.Type, "base64") || !strings.EqualFold(source.MediaType, "image/png") {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(source.Data)
	if err != nil || len(data) == 0 {
		return nil
	}
	contentHash := sha256.Sum256(data)
	contentDigest := hex.EncodeToString(contentHash[:])
	scopeHash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s", s.options.TenantID, s.options.UserID, s.options.TenantSessionID, contentDigest)))
	assetID := "cu-image-" + hex.EncodeToString(scopeHash[:])
	name := assetID + ".png"
	policy := media.AccessPolicy{TenantID: s.options.TenantID, UserID: s.options.UserID, SessionID: s.options.TenantSessionID}
	if existing, err := s.options.MediaAssetStore.Get(ctx, policy, assetID); err == nil {
		return &ComputerObservationReference{ObservationID: payload.Observation.ID, AssetID: existing.AssetID, MediaType: existing.MediaType, Name: existing.Name, SizeBytes: existing.SizeBytes, SHA256: existing.SHA256}
	}
	key := fmt.Sprintf("tenant-%d/user-%d/session-%d/%s.png", policy.TenantID, policy.UserID, policy.SessionID, assetID)
	blob, err := s.options.ImageBlobStore.Put(ctx, imagegensvc.PutBlobRequest{Policy: policy, Key: key, MediaType: "image/png", Name: name, Data: bytes.NewReader(data)})
	if err != nil {
		return nil
	}
	asset := media.Asset{
		AssetID: assetID, Kind: media.KindImage, MediaType: "image/png", Name: name,
		SizeBytes: int64(len(data)), SHA256: contentDigest, State: media.StateReady,
		TenantID: policy.TenantID, UserID: policy.UserID, SessionID: policy.SessionID,
		Original: media.Variant{Path: blob.Key, MediaType: "image/png", SizeBytes: blob.SizeBytes, SHA256: blob.SHA256},
		Access:   policy,
	}
	if err := s.options.MediaAssetStore.Put(ctx, asset); err != nil {
		_ = s.options.ImageBlobStore.Delete(ctx, policy, blob.Key)
		return nil
	}
	return &ComputerObservationReference{
		ObservationID: payload.Observation.ID, AssetID: assetID, MediaType: "image/png",
		Name: name, SizeBytes: int64(len(data)), SHA256: contentDigest,
	}
}

func bindToolResult(trace *ToolTrace, result tools.Result, fileChanges []tools.FileChange) {
	trace.Output = result.Content
	trace.IsError = result.IsError
	trace.Interaction = result.Interaction
	trace.FileChanges = append([]tools.FileChange(nil), fileChanges...)
	trace.verification = result.Verification
	trace.contextMessages = append([]anthropic.MessageParam(nil), result.ContextMessages...)
}

func newExecutionRunID(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	var value [executionRunIDBytes]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate query execution run id: %w", err)
	}
	return "query-" + hex.EncodeToString(value[:]), nil
}

func toolInvocationBatchID(runID string, turn int) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(runID) + "\x00" + strconv.Itoa(turn)))
	return fmt.Sprintf("%x", sum[:])
}

func (s *Session) activeSkillTelemetryProperties() map[string]any {
	if s.activeSkill == nil {
		return map[string]any{
			"active_skill":          "",
			"active_skill_source":   "",
			"active_skill_version":  "",
			"active_skill_fallback": false,
		}
	}
	return map[string]any{
		"active_skill":          s.activeSkill.Name,
		"active_skill_source":   s.activeSkill.Source,
		"active_skill_version":  s.activeSkill.Version,
		"active_skill_fallback": s.activeSkill.Fallback,
	}
}

func (s *Session) rememberActiveSkillMessages(messages []anthropic.MessageParam) {
	for _, message := range messages {
		if !messageHasText(message) {
			continue
		}
		if containsMessageText(s.activeSkillMessages, message) {
			continue
		}
		s.activeSkillMessages = append(s.activeSkillMessages, cloneMessageParam(message))
		if len(s.activeSkillMessages) > 8 {
			s.activeSkillMessages = s.activeSkillMessages[len(s.activeSkillMessages)-8:]
		}
	}
}

func (s *Session) withActiveSkillContextMessages(messages []anthropic.MessageParam) []anthropic.MessageParam {
	if len(s.activeSkillMessages) == 0 {
		return messages
	}
	out := messages
	copied := false
	for _, message := range s.activeSkillMessages {
		if containsMessageText(out, message) {
			continue
		}
		if !copied {
			out = append([]anthropic.MessageParam(nil), messages...)
			copied = true
		}
		out = append(out, cloneMessageParam(message))
	}
	return out
}

func containsMessageText(messages []anthropic.MessageParam, target anthropic.MessageParam) bool {
	targetText := messageTextForMatch(target)
	if targetText == "" {
		return false
	}
	for _, message := range messages {
		if messageTextForMatch(message) == targetText {
			return true
		}
	}
	return false
}

func messageHasText(message anthropic.MessageParam) bool {
	return messageTextForMatch(message) != ""
}

func messageTextForMatch(message anthropic.MessageParam) string {
	var parts []string
	for _, block := range message.Content {
		if block.Type == blockTypeText {
			text := strings.TrimSpace(block.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func cloneMessageParam(message anthropic.MessageParam) anthropic.MessageParam {
	cloned := message
	if len(message.Content) > 0 {
		cloned.Content = append([]anthropic.ContentBlock(nil), message.Content...)
	}
	return cloned
}

func (s *Session) effectiveRegistry() *tools.Registry {
	if s.options.DisableTools || s.registry == nil {
		return tools.NewRegistry()
	}
	if s.options.ComputerUseFastPath {
		// A bounded GUI run must not spend model context on unrelated coding,
		// shell, file, or agent tools. ComputerUse remains the same authority-
		// checked tool; this only narrows the advertised surface for the run.
		// Keep injected test-only registries intact when they do not contain the
		// production ComputerUse tool.
		if _, ok := s.registry.Get("ComputerUse"); ok {
			return s.registry.Filter([]string{"ComputerUse"})
		}
	}
	if !s.coordinatorMode() {
		return s.registry
	}
	return s.registry.Filter(coordinatorToolAllowlist())
}

func (s *Session) inlineTenantSkills(ctx context.Context) (string, TenantSkillInlineManifest) {
	if s.options.SkillProvider == nil || len(s.options.InlineTenantSkills) == 0 {
		return "", TenantSkillInlineManifest{}
	}
	manifest := TenantSkillInlineManifest{
		Active:    true,
		Selector:  strings.TrimSpace(s.options.InlineTenantSkillSource),
		SkillKeys: normalizeInlineSkillKeys(s.options.InlineTenantSkills),
	}
	var parts []string
	seen := map[string]bool{}
	for _, name := range s.options.InlineTenantSkills {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		skill, ok, err := s.options.SkillProvider.GetTenantSkill(ctx, name)
		if err != nil {
			observability.Error(ctx, nil, "tenant.skill.inline.error", "query.Session.inlineTenantSkills", "load tenant skill failed", "skill", name, "error", err)
			manifest.MissingKeys = append(manifest.MissingKeys, name)
			if manifest.Error == "" {
				manifest.Error = "load_error"
			}
			continue
		}
		if !ok {
			manifest.MissingKeys = append(manifest.MissingKeys, name)
			if manifest.Error == "" {
				manifest.Error = "not_found_or_disabled"
			}
			continue
		}
		content := strings.TrimSpace(skill.Content)
		if content == "" {
			content = strings.TrimSpace(skill.Description)
		}
		if content == "" {
			manifest.MissingKeys = append(manifest.MissingKeys, name)
			if manifest.Error == "" {
				manifest.Error = "empty_content"
			}
			continue
		}
		header := fmt.Sprintf("# Tenant Skill: %s", skill.Name)
		if strings.TrimSpace(skill.Version) != "" {
			header += " v" + strings.TrimSpace(skill.Version)
		}
		parts = append(parts, header+"\n"+content)
		manifest.Loaded = true
		manifest.LoadedKeys = append(manifest.LoadedKeys, skill.Name)
		manifest.Versions = append(manifest.Versions, strings.TrimSpace(skill.Version))
		if value := strings.TrimSpace(skill.PackageSHA256); value != "" {
			manifest.PackageSHA256 = append(manifest.PackageSHA256, value)
		}
		if value := strings.TrimSpace(skill.PackageRef); value != "" {
			manifest.PackageRefs = append(manifest.PackageRefs, value)
		}
		if value := strings.TrimSpace(skill.RuntimeRef); value != "" {
			manifest.RuntimeRefs = append(manifest.RuntimeRefs, value)
		}
	}
	if len(parts) == 0 {
		return "", manifest
	}
	inline := "Tenant skill instructions are inlined for this structured single-call request. Follow them without calling tools.\n\n" + strings.Join(parts, "\n\n")
	manifest.Bytes = len(inline)
	return inline, manifest
}

func normalizeInlineSkillKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		lower := strings.ToLower(key)
		if seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, key)
	}
	return out
}

func (s *Session) coordinatorMode() bool {
	return strings.TrimSpace(s.options.CoordinatorPrompt) != "" && strings.TrimSpace(s.options.MainThreadAgentPrompt) == "" && strings.TrimSpace(s.options.OverrideSystemPrompt) == ""
}

func coordinatorToolAllowlist() []string {
	return []string{
		"Task",
		"TaskOutput",
		"AgentCreate",
		"AgentList",
		"AgentGet",
		"AgentStop",
		"AgentMessage",
		"AskUserQuestion",
		"TodoRead",
		"TodoWrite",
	}
}

func (s *Session) pendingAgentMessages(ctx context.Context, taskID uint64) []agenttasks.MessageInput {
	if taskID == 0 || s.options.TaskStore == nil {
		return nil
	}
	store, ok := s.options.TaskStore.(agentTaskEventLister)
	if !ok {
		return nil
	}
	events, err := store.ListAgentTaskEvents(ctx, taskID, 200)
	if err != nil {
		observability.Error(ctx, nil, "agent.message.list_error", "query.Session.pendingAgentMessages", "list pending agent messages failed",
			"task_id", taskID,
			"error", err,
		)
		return nil
	}
	out := make([]agenttasks.MessageInput, 0)
	for _, event := range events {
		if event.EventType != agenttasks.EventMessage || s.agentMessageDelivered(taskID, event.ID) {
			continue
		}
		var msg agenttasks.MessageInput
		if err := json.Unmarshal([]byte(event.PayloadJSON), &msg); err != nil {
			observability.Error(ctx, nil, "agent.message.payload_error", "query.Session.pendingAgentMessages", "parse pending agent message failed",
				"task_id", taskID,
				"event_id", event.ID,
				"error", err,
			)
			s.markAgentMessageDelivered(taskID, event.ID)
			continue
		}
		msg.Content = strings.TrimSpace(msg.Content)
		if msg.Content == "" {
			s.markAgentMessageDelivered(taskID, event.ID)
			continue
		}
		if msg.TaskID == 0 {
			msg.TaskID = taskID
		}
		if msg.TraceID == "" {
			msg.TraceID = event.TraceID
		}
		out = append(out, msg)
		s.markAgentMessageDelivered(taskID, event.ID)
	}
	return out
}

func (s *Session) agentMessageDelivered(taskID, eventID uint64) bool {
	s.agentMessagesMu.Lock()
	defer s.agentMessagesMu.Unlock()
	return s.deliveredAgentMessages != nil && s.deliveredAgentMessages[taskID] != nil && s.deliveredAgentMessages[taskID][eventID]
}

func (s *Session) markAgentMessageDelivered(taskID, eventID uint64) {
	s.agentMessagesMu.Lock()
	defer s.agentMessagesMu.Unlock()
	if s.deliveredAgentMessages == nil {
		s.deliveredAgentMessages = map[uint64]map[uint64]bool{}
	}
	if s.deliveredAgentMessages[taskID] == nil {
		s.deliveredAgentMessages[taskID] = map[uint64]bool{}
	}
	s.deliveredAgentMessages[taskID][eventID] = true
}

func (s *Session) hookRunner() hooks.Runner {
	if s.activeSkill == nil || len(s.activeSkill.Hooks) == 0 {
		return s.options.Hooks
	}
	return s.options.Hooks.WithAdditional(s.activeSkill.Hooks)
}

func (s *Session) runHookWithCallbacks(ctx context.Context, event, name string, payload hooks.Payload, cb runCallbacks) (hooks.Result, error) {
	if !s.hasHookEvent(event) {
		return hooks.Result{}, nil
	}
	if cb.onHookStart != nil {
		if err := cb.onHookStart(event, name); err != nil {
			return hooks.Result{}, err
		}
	}
	result, err := s.runHookSpan(ctx, event, payload)
	if cb.onHookResult != nil {
		if callbackErr := cb.onHookResult(event, name, result, err); callbackErr != nil {
			if err != nil {
				return result, err
			}
			return result, callbackErr
		}
	}
	return result, err
}

func (s *Session) runHookSpan(ctx context.Context, event string, payload hooks.Payload) (hooks.Result, error) {
	hookCtx, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventHookExecution,
		Category:  telemetry.CategorySystem,
		Source:    "query.Session.runHookSpan",
		SessionID: s.options.TenantSessionID,
		ToolName:  payload.ToolName,
		Properties: map[string]any{
			"hook_event":  event,
			"input_bytes": len(payload.Input),
		},
	})
	var result hooks.Result
	err := hookCtx.Err()
	if err == nil {
		if tools.IsComputerUseTool(payload.ToolName) {
			result, err = runComputerHooks(hookCtx, s.hookRunner(), event, s.options.CWD, payload)
		} else {
			result, err = s.hookRunner().RunWithPayload(hookCtx, event, s.options.CWD, payload)
		}
	}
	status := telemetry.StatusOK
	if err != nil {
		status = telemetry.StatusError
	}
	span.FinishEvent(telemetry.Event{
		Name:      telemetry.EventHookExecution,
		Category:  telemetry.CategorySystem,
		Source:    "query.Session.runHookSpan",
		Status:    status,
		SessionID: s.options.TenantSessionID,
		ToolName:  payload.ToolName,
		Properties: map[string]any{
			"hook_event": event,
		},
	})
	return result, err
}

func (s *Session) hasHookEvent(event string) bool {
	if !s.runtimePolicy().RunHooks {
		return false
	}
	return len(s.hookRunner().Hooks[event]) > 0
}

func (s *Session) runUserPromptSubmitHook(ctx context.Context, prompt string, cb runCallbacks) (string, error) {
	result, err := s.runHookWithCallbacks(ctx, hooks.UserPromptSubmit, hooks.UserPromptSubmit, hooks.Payload{
		Prompt:    prompt,
		SessionID: s.sessionIDForHook(),
	}, cb)
	if err != nil {
		return prompt, err
	}
	if strings.EqualFold(result.PermissionDecision, "deny") {
		return prompt, fmt.Errorf("prompt denied by UserPromptSubmit hook: %s", firstNonEmpty(result.PermissionDecisionReason, result.Message, "denied"))
	}
	updated := promptFromHookUpdatedInput(result.UpdatedInput)
	if updated == "" {
		return prompt, nil
	}
	return updated, nil
}

func (s *Session) runNotificationHook(ctx context.Context, name string, payload hooks.Payload, cb runCallbacks) error {
	_, err := s.runHookWithCallbacks(ctx, hooks.Notification, name, payload, cb)
	if err != nil {
		observability.Error(ctx, nil, "hook.notification.error", "query.Session.runNotificationHook", "notification hook failed",
			"message", name,
			"error", err,
		)
	}
	return err
}

func (s *Session) runStopHook(ctx context.Context, result Result, runErr error, started time.Time, cb runCallbacks) error {
	payload := hooks.Payload{
		Result:     result.Response,
		IsError:    runErr != nil,
		StopReason: result.StopReason,
		SessionID:  firstNonEmpty(result.SessionID, s.sessionIDForHook()),
		DurationMS: time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		payload.Error = runErr.Error()
	}
	_, err := s.runHookWithCallbacks(ctx, hooks.Stop, hooks.Stop, payload, cb)
	if err != nil {
		observability.Error(ctx, nil, "hook.stop.error", "query.Session.runStopHook", "stop hook failed", "error", err)
	}
	return err
}

func (s *Session) sessionIDForHook() string {
	if s.options.Recorder != nil && strings.TrimSpace(s.options.Recorder.SessionID) != "" {
		return strings.TrimSpace(s.options.Recorder.SessionID)
	}
	if strings.TrimSpace(s.options.TraceID) != "" {
		return strings.TrimSpace(s.options.TraceID)
	}
	if s.options.TenantSessionID != 0 {
		return fmt.Sprintf("%d", s.options.TenantSessionID)
	}
	return ""
}

func promptFromHookUpdatedInput(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var obj struct {
		Prompt string `json:"prompt"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return firstNonEmpty(obj.Prompt, obj.Text)
	}
	return ""
}

func (s *Session) currentModel() string {
	if s.activeSkill != nil && strings.TrimSpace(s.activeSkill.Model) != "" && s.activeSkill.Model != "inherit" {
		return s.activeSkill.Model
	}
	return s.options.Model
}

func (s *Session) currentThinkingConfig() *anthropic.ThinkingConfig {
	if s.activeSkill != nil {
		effort := strings.ToLower(strings.TrimSpace(s.activeSkill.Effort))
		if effort != "" && effort != "inherit" {
			return anthropic.ThinkingConfigFromEffort(s.activeSkill.Effort, s.options.MaxTokens)
		}
	}
	return anthropic.ThinkingConfigFromEffort(s.options.Effort, s.options.MaxTokens)
}

func (s *Session) applySkillRuntime(ctx context.Context, toolName string, input json.RawMessage) *tools.SkillRuntime {
	if toolName != "Skill" {
		return nil
	}
	var params struct {
		Name  string `json:"name"`
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return nil
	}
	name := firstNonEmpty(params.Skill, params.Name)
	if strings.TrimSpace(name) == "" {
		return nil
	}
	skill, fallback, ok, err := s.loadSkillRuntime(ctx, name)
	if err != nil || !ok {
		return nil
	}
	directory, filesystemBacked := filesystemSkillDirectory(skill)
	s.activeSkill = &tools.SkillRuntime{
		Name:             skill.Name,
		Source:           string(skill.Source),
		Version:          skill.Version,
		Fallback:         fallback,
		AllowedTools:     append([]string(nil), skill.AllowedTools...),
		Model:            skill.Model,
		Context:          skill.ExecutionContext,
		Paths:            append([]string(nil), skill.Paths...),
		Arguments:        append([]string(nil), skill.Arguments...),
		ArgumentHint:     skill.ArgumentHint,
		UserInvocable:    skill.UserInvocable,
		Agent:            skill.Agent,
		Effort:           skill.Effort,
		Hooks:            skill.Hooks,
		Directory:        directory,
		FilesystemBacked: filesystemBacked,
	}
	return s.activeSkill
}

func filesystemSkillDirectory(skill skills.Skill) (string, bool) {
	if skill.Source == skills.SourceTenant {
		return "", false
	}
	path := strings.TrimSpace(skill.Path)
	if path == "" {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		return "", false
	}
	directory := filepath.Clean(filepath.Dir(absPath))
	if root := strings.TrimSpace(skill.Root); root != "" {
		absRoot, err := filepath.Abs(root)
		if err != nil || filepath.Clean(absRoot) != directory {
			return "", false
		}
	}
	return directory, true
}

func (s *Session) tenantSkillsCatalog(ctx context.Context, prompt string) (string, error) {
	if s.options.SkillProvider == nil {
		return "", nil
	}
	items, err := s.options.SkillProvider.ListTenantSkills(ctx, prompt, 100)
	if err != nil {
		return "", err
	}
	return skills.CatalogPromptFromSkills(items, prompt), nil
}

func (s *Session) dynamicSkillCatalogMessage(ctx context.Context, evidence string, seen map[string]bool) *anthropic.MessageParam {
	catalog, names, err := skills.CatalogPromptForPromptExcluding(s.options.CWD, evidence, seen)
	if err != nil {
		observability.Error(ctx, nil, "skills.catalog.delta.error", "query.Session.dynamicSkillCatalogMessage", "load dynamic skill catalog failed", "error", err)
		return nil
	}
	catalog = strings.TrimSpace(catalog)
	if catalog == "" {
		return nil
	}
	markSkillCatalogSent(seen, names)
	text := "<system-reminder>\nAdditional skills became relevant based on files, paths, or tool results seen so far. If a listed skill matches the next task-specific step, call the Skill tool with its exact name before continuing that work.\n\n" + catalog + "\n</system-reminder>"
	return &anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: text}},
	}
}

func markSkillCatalogSent(seen map[string]bool, names []string) {
	if seen == nil {
		return
	}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		seen[strings.ToLower(name)] = true
	}
}

func (s *Session) loadSkillRuntime(ctx context.Context, name string) (skills.Skill, bool, bool, error) {
	if s.options.SkillProvider != nil {
		skill, ok, err := s.options.SkillProvider.GetTenantSkill(ctx, name)
		if err != nil || ok {
			return skill, false, ok, err
		}
	}
	skill, ok, err := skills.Load(s.options.CWD, name)
	return skill, s.options.SkillProvider != nil && ok, ok, err
}

func (s *Session) runPermissionPrompt(ctx context.Context, req tools.PermissionPromptRequest) (response tools.PermissionPromptResponse) {
	req = tools.RedactPermissionPromptRequest(req)
	defer func() { response = tools.RedactPermissionPromptResponse(req.ToolName, response) }()
	permissionCtx, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:      telemetry.EventPermissionWait,
		Category:  telemetry.CategoryPermission,
		Source:    "query.Session.runPermissionPrompt",
		SessionID: s.options.TenantSessionID,
		ToolName:  req.ToolName,
	})
	defer func() {
		status := telemetry.StatusDenied
		if response.Allowed {
			status = telemetry.StatusOK
		}
		span.FinishEvent(telemetry.Event{Name: telemetry.EventPermissionWait, Category: telemetry.CategoryPermission, Source: "query.Session.runPermissionPrompt", Status: status, SessionID: s.options.TenantSessionID, ToolName: req.ToolName})
	}()
	if contextErr := permissionCtx.Err(); contextErr != nil {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
		return response
	}
	if s.options.PermissionPrompt != nil {
		response = s.options.PermissionPrompt(permissionCtx, req)
		if contextErr := permissionCtx.Err(); contextErr != nil {
			response = tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
		}
		return response
	}
	name := strings.TrimSpace(s.options.PermissionPromptTool)
	if name == "" {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: req.Reason}
		return response
	}
	promptTool, ok := s.registry.Get(name)
	if !ok {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: "permission prompt tool not found: " + name}
		return response
	}
	payload, _ := json.Marshal(req)
	if contextErr := permissionCtx.Err(); contextErr != nil {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
		return response
	}
	res := promptTool.Run(permissionCtx, payload, tools.Context{CWD: s.options.CWD, WritableRoots: s.options.WritableRoots})
	if contextErr := permissionCtx.Err(); contextErr != nil {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: contextErr.Error()}
		return response
	}
	if res.IsError {
		response = tools.PermissionPromptResponse{Allowed: false, Reason: res.Content}
		return response
	}
	response = parsePermissionPromptResponse(res.Content)
	return response
}

func parsePermissionPromptResponse(content string) tools.PermissionPromptResponse {
	var decoded struct {
		Behavior    string          `json:"behavior"`
		Decision    string          `json:"decision"`
		Destination string          `json:"destination"`
		Scope       string          `json:"scope"`
		Rule        string          `json:"rule"`
		Allowed     *bool           `json:"allowed"`
		Allow       *bool           `json:"allow"`
		Reason      string          `json:"reason"`
		Payload     json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &decoded); err == nil {
		destination := firstNonEmpty(decoded.Destination, decoded.Scope)
		decision := strings.ToLower(firstNonEmpty(decoded.Decision, decoded.Behavior))
		if strings.Contains(decision, "session") {
			destination = firstNonEmpty(destination, "session")
		}
		if strings.Contains(decision, "global") || strings.Contains(decision, "permanent") || strings.Contains(decision, "persist") {
			destination = firstNonEmpty(destination, "global")
		}
		if strings.Contains(decision, "project") {
			destination = firstNonEmpty(destination, "project")
		}
		if strings.Contains(decision, "local") {
			destination = firstNonEmpty(destination, "local")
		}
		if strings.Contains(decision, "once") {
			destination = firstNonEmpty(destination, "once")
		}
		if decoded.Allowed != nil {
			return tools.PermissionPromptResponse{Allowed: *decoded.Allowed, Reason: decoded.Reason, Destination: destination, Rule: decoded.Rule, Decision: allowDeny(*decoded.Allowed), Payload: decoded.Payload}
		}
		if decoded.Allow != nil {
			return tools.PermissionPromptResponse{Allowed: *decoded.Allow, Reason: decoded.Reason, Destination: destination, Rule: decoded.Rule, Decision: allowDeny(*decoded.Allow), Payload: decoded.Payload}
		}
		switch decision {
		case "allow", "allowed", "approve", "approved":
			return tools.PermissionPromptResponse{Allowed: true, Reason: decoded.Reason, Destination: destination, Rule: decoded.Rule, Decision: "allow", Payload: decoded.Payload}
		case "allow_once", "allow-once", "approve_once", "approve-once":
			return tools.PermissionPromptResponse{Allowed: true, Reason: decoded.Reason, Destination: "once", Rule: decoded.Rule, Decision: "allow", Payload: decoded.Payload}
		case "allow_session", "allow-session", "approve_session", "approve-session", "allow_for_session", "allow-for-session":
			return tools.PermissionPromptResponse{Allowed: true, Reason: decoded.Reason, Destination: "session", Rule: decoded.Rule, Decision: "allow", Payload: decoded.Payload}
		case "allow_global", "allow-global", "allow_permanently", "allow-permanently", "approve_global", "approve-global":
			return tools.PermissionPromptResponse{Allowed: true, Reason: decoded.Reason, Destination: "global", Rule: decoded.Rule, Decision: "allow", Payload: decoded.Payload}
		case "allow_project", "allow-project", "approve_project", "approve-project":
			return tools.PermissionPromptResponse{Allowed: true, Reason: decoded.Reason, Destination: "project", Rule: decoded.Rule, Decision: "allow", Payload: decoded.Payload}
		case "deny", "denied", "reject", "rejected":
			return tools.PermissionPromptResponse{Allowed: false, Reason: decoded.Reason, Destination: destination, Rule: decoded.Rule, Decision: "deny", Payload: decoded.Payload}
		case "deny_session", "deny-session":
			return tools.PermissionPromptResponse{Allowed: false, Reason: decoded.Reason, Destination: "session", Rule: decoded.Rule, Decision: "deny", Payload: decoded.Payload}
		case "deny_global", "deny-global":
			return tools.PermissionPromptResponse{Allowed: false, Reason: decoded.Reason, Destination: "global", Rule: decoded.Rule, Decision: "deny", Payload: decoded.Payload}
		case "deny_project", "deny-project":
			return tools.PermissionPromptResponse{Allowed: false, Reason: decoded.Reason, Destination: "project", Rule: decoded.Rule, Decision: "deny", Payload: decoded.Payload}
		}
	}
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "allow", "allowed", "approve", "approved", "yes", "true":
		return tools.PermissionPromptResponse{Allowed: true}
	default:
		return tools.PermissionPromptResponse{Allowed: false, Reason: content}
	}
}

func allowDeny(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func intPtr(value int) *int {
	return &value
}

func splitJSONDelta(value string) []string {
	const chunkSize = 64
	if len(value) <= chunkSize {
		return []string{value}
	}
	chunks := make([]string, 0, (len(value)+chunkSize-1)/chunkSize)
	for len(value) > chunkSize {
		chunks = append(chunks, value[:chunkSize])
		value = value[chunkSize:]
	}
	if value != "" {
		chunks = append(chunks, value)
	}
	return chunks
}

func (s *Session) applyPermissionUpdate(update tools.PermissionUpdate) error {
	update = tools.RedactPermissionUpdate(update)
	rule := strings.TrimSpace(update.Rule)
	if rule == "" {
		rule = permissionRule(update.ToolName, update.Request)
	}
	rule = normalizePermissionUpdateRule(update.ToolName, rule)
	decision := strings.ToLower(strings.TrimSpace(update.Decision))
	if decision == "" {
		decision = "allow"
	}
	destination := strings.ToLower(strings.TrimSpace(update.Destination))
	switch destination {
	case "", "once":
		return nil
	case "session":
		if decision == "deny" {
			s.sessionDeny = appendUnique(s.sessionDeny, rule)
		} else {
			s.sessionAllow = appendUnique(s.sessionAllow, rule)
		}
		return nil
	case "global", "user", "settings":
		settings := config.LoadGlobalSettings()
		addPermissionRule(&settings.Permissions, decision, rule)
		if err := config.SaveGlobalSettings(settings); err != nil {
			return err
		}
		s.applySessionPermissionRule(decision, rule)
		return nil
	case "project":
		if err := config.EnsureProjectSettingsMaterialized(s.options.CWD); err != nil {
			return err
		}
		path := config.ProjectSettingsPath(s.options.CWD, false)
		settings := config.LoadSettingsFile(path)
		addPermissionRule(&settings.Permissions, decision, rule)
		if err := config.SaveSettingsFile(path, settings); err != nil {
			return err
		}
		s.applySessionPermissionRule(decision, rule)
		return nil
	case "local":
		if err := config.EnsureProjectSettingsMaterialized(s.options.CWD); err != nil {
			return err
		}
		path := config.ProjectSettingsPath(s.options.CWD, true)
		settings := config.LoadSettingsFile(path)
		addPermissionRule(&settings.Permissions, decision, rule)
		if err := config.SaveSettingsFile(path, settings); err != nil {
			return err
		}
		s.applySessionPermissionRule(decision, rule)
		return nil
	default:
		return fmt.Errorf("unsupported permission update destination: %s", update.Destination)
	}
}

func normalizePermissionUpdateRule(toolName, rule string) string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "TodoWrite" {
		return toolName
	}
	return strings.TrimSpace(rule)
}

func (s *Session) applySessionPermissionRule(decision, rule string) {
	if strings.EqualFold(decision, "deny") {
		s.sessionDeny = appendUnique(s.sessionDeny, rule)
		return
	}
	s.sessionAllow = appendUnique(s.sessionAllow, rule)
}

func addPermissionRule(settings *config.PermissionSettings, decision, rule string) {
	if strings.EqualFold(decision, "deny") {
		settings.Deny = appendUnique(settings.Deny, rule)
		return
	}
	settings.Allow = appendUnique(settings.Allow, rule)
}

func permissionRule(toolName, request string) string {
	return permissions.FormatRule(toolName, request)
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Session) recordPermission(ctx context.Context, audit tools.PermissionAudit) {
	audit = tools.RedactPermissionAudit(audit)
	status := telemetry.StatusOK
	if !audit.Allowed {
		status = telemetry.StatusDenied
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:      "permission.decision",
		Category:  telemetry.CategoryPermission,
		Source:    "query.Session.recordPermission",
		Status:    status,
		SessionID: s.options.TenantSessionID,
		ToolName:  audit.ToolName,
		Properties: map[string]any{
			"allowed": audit.Allowed,
			"reason":  audit.Reason,
			"rule":    audit.Rule,
			"request": audit.Request,
			"source":  audit.Source,
		},
	})
	if s.options.Recorder == nil {
		return
	}
	data, err := json.Marshal(map[string]any{
		"allowed": audit.Allowed,
		"reason":  audit.Reason,
		"rule":    audit.Rule,
		"request": audit.Request,
		"source":  audit.Source,
	})
	content := ""
	if err == nil {
		content = string(data)
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:     "permission",
		ToolName: audit.ToolName,
		Content:  content,
		IsError:  !audit.Allowed,
	})
}

func (s *Session) recordFileChange(change tools.FileChange) {
	if s.options.Recorder == nil {
		return
	}
	if s.turnFileChangeSeen == nil {
		s.turnFileChangeSeen = map[string]bool{}
	}
	// Turn-level dedup: the first change to a path in a turn anchors the
	// recoverable turn-start state; later changes to the same path this turn are
	// intermediate versions with no rewind value (rewind restores the turn-start
	// state), so they are recorded as lite, non-recoverable entries.
	if path := change.Path; path != "" && s.turnFileChangeSeen[path] {
		supersedeFileChangeForTranscript(&change)
	} else {
		externalizeFileChangeForTranscript(&change)
		if path != "" {
			s.turnFileChangeSeen[path] = true
		}
	}
	// v2 message-graph: key the change to the turn's message so file history is
	// message-keyed. Left empty for v1 so linear transcripts are byte-unchanged.
	if s.options.Recorder.IsV2() {
		change.MessageID = s.currentTurnMessageID
	}
	data, err := json.Marshal(change)
	if err != nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:    "file_change",
		Content: string(data),
	})
}

// supersedeFileChangeForTranscript strips a file_change down to a lite,
// non-recoverable entry: it keeps path, mode, existence, and content hashes for
// observability but drops all bodies and blob references, since an earlier
// file_change in the same turn already holds the recoverable turn-start state.
func supersedeFileChangeForTranscript(change *tools.FileChange) {
	if change.BeforeSHA256 == "" {
		change.BeforeSHA256 = auditHash(change.Before, change.BeforeSnapshotPath)
	}
	if change.AfterSHA256 == "" {
		change.AfterSHA256 = auditHash(change.After, change.AfterSnapshotPath)
	}
	change.Before = ""
	change.After = ""
	change.BeforeSnapshotPath = ""
	change.AfterSnapshotPath = ""
	change.BeforeSuperseded = true
}

// auditHash returns a content hash for observability without storing a blob. It
// prefers the hash embedded in an existing content-addressed blob path, else
// hashes inline content. The large-file capture placeholder is a marker, not a
// body, so it yields no hash.
func auditHash(inline, snapshotPath string) string {
	if h := files.SnapshotHashFromPath(snapshotPath); h != "" {
		return h
	}
	if inline != "" && inline != files.CaptureSnapshotPlaceholder {
		return files.SHA256Hex(inline)
	}
	return ""
}

// externalizeFileChangeForTranscript moves any inline before/after file body into
// the content-addressed snapshot store so the transcript never persists the file
// body itself; only a blob reference (path) and a content hash remain. This is
// the single choke point for every producer (Edit/Write/MultiEdit/Bash), so the
// transcript is content-free regardless of which tool captured the change.
//
// Rules:
//   - Empty content stays inline (no body, no leak, no blob).
//   - Symlink/directory changes carry no file body and are left untouched.
//   - The large-file capture placeholder is a marker, not a body, so it is left
//     inline rather than stored as a blob.
//   - Already-externalized content keeps its blob path; only its audit hash is
//     backfilled from the content-addressed blob name.
//   - On any store failure the body is left inline so the before-state stays
//     recoverable — rewind availability wins over transcript hygiene.
func externalizeFileChangeForTranscript(change *tools.FileChange) {
	if change.BeforeExists && !change.BeforeIsSymlink && !change.BeforeIsDir {
		if change.BeforeSnapshotPath != "" {
			if change.BeforeSHA256 == "" {
				change.BeforeSHA256 = files.SnapshotHashFromPath(change.BeforeSnapshotPath)
			}
		} else if change.Before != "" {
			if path, hexsum, err := files.ExternalizeContent("", change.Before); err == nil && path != "" {
				change.BeforeSnapshotPath = path
				change.BeforeSHA256 = hexsum
				change.Before = ""
			}
		}
	}
	if change.AfterExists && !change.AfterIsSymlink && !change.AfterIsDir {
		if change.AfterSnapshotPath != "" {
			if change.AfterSHA256 == "" {
				change.AfterSHA256 = files.SnapshotHashFromPath(change.AfterSnapshotPath)
			}
		} else if change.After != "" && change.After != files.CaptureSnapshotPlaceholder {
			if path, hexsum, err := files.ExternalizeContent("", change.After); err == nil && path != "" {
				change.AfterSnapshotPath = path
				change.AfterSHA256 = hexsum
				change.After = ""
			}
		}
	}
}

func truncateToolResult(content string, limit int) string {
	return toolresult.Truncate(content, limit)
}

func (s *Session) toolResultSessionRef() toolresult.SessionRef {
	if s.options.Recorder == nil {
		return toolresult.SessionRef{}
	}
	return toolresult.SessionRef{
		SessionID:      s.options.Recorder.SessionID,
		TranscriptPath: s.options.Recorder.Path,
	}
}

func collectToolUses(blocks []anthropic.ContentBlock) []anthropic.ContentBlock {
	var toolUses []anthropic.ContentBlock
	for _, block := range blocks {
		if block.Type == blockTypeToolUse {
			toolUses = append(toolUses, block)
		}
	}
	return toolUses
}

func blockedToolTrace(block anthropic.ContentBlock, reason string) ToolTrace {
	return ToolTrace{
		ID:      block.ID,
		Name:    block.Name,
		Input:   string(tools.RedactToolInput(block.Name, block.Input)),
		Output:  reason,
		IsError: true,
	}
}

func cancelledToolTrace(block anthropic.ContentBlock, err error) ToolTrace {
	reason := context.Canceled.Error()
	if err != nil {
		reason = err.Error()
	}
	return ToolTrace{ID: block.ID, Name: block.Name, Input: string(tools.RedactToolInput(block.Name, block.Input)), Output: reason, IsError: true}
}

func cancelledToolTraces(blocks []anthropic.ContentBlock, err error) []ToolTrace {
	traces := make([]ToolTrace, len(blocks))
	for index, block := range blocks {
		traces[index] = cancelledToolTrace(block, err)
	}
	return traces
}

func preflightToolBlock(block anthropic.ContentBlock, command string) anthropic.ContentBlock {
	block.Input = json.RawMessage(fmt.Sprintf(`{"command":%q}`, command))
	return block
}

// GatePreflightMarker is the fixed phrase emitted in closure-gate auto-preflight
// tool output when the guarded command was not executed. External packages
// (e.g. agenteval) match on it to detect gate friction; keep this as the single
// source of truth rather than duplicating the literal.
const GatePreflightMarker = "Original command was not executed"

func gatePreflightToolOutput(gate completionGateResult, originalCommand, output string, isError bool) string {
	status := "completed"
	if isError {
		status = "failed"
	}
	title := firstNonEmpty(gate.PreflightTitle, "Gate preflight")
	summary := firstNonEmpty(gate.PreflightSummary, "Required read-only preflight completed; original command was not executed")
	return strings.Join([]string{
		fmt.Sprintf(`<gate-preflight rule_id=%q status=%q title=%q>`, gate.RuleID, status, title),
		"<system-reminder>" + summary + ". " + GatePreflightMarker + ": " + originalCommand + ". Inspect the preflight output above. Only if it matches the user's request and reveals no problem (for example the remote has not diverged), re-send your ENTIRE original command shown above verbatim — do not drop or restructure any step (keep git add/commit/push/tag together). If the output reveals a problem, resolve it first (for example git pull --rebase for a diverged remote) and never blindly re-run a push past a divergence.</system-reminder>",
		"<preflight-command>",
		gate.PreflightCommand,
		"</preflight-command>",
		"<preflight-output>",
		strings.TrimSpace(output),
		"</preflight-output>",
		"</gate-preflight>",
	}, "\n")
}

func assistantText(blocks []anthropic.ContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == blockTypeText && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "")
}

const (
	systemPromptDynamicBoundary = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"
	defaultCLISystemPrompt      = "You are niuma, a Go-developed universal agent super assistant."
	agentSDKClaudeCodePrefix    = "You are niuma, a Go-developed universal agent super assistant, running within the Claude Agent SDK."
	agentSDKPrefix              = "You are a Claude agent, built on Anthropic's Claude Agent SDK."
	// Legacy prefixes are recognized only when splitting old transcript/cache
	// blocks; new requests use defaultCLISystemPrompt above.
	legacyClaudeCodePrefix    = "You are Claude Code, Anthropic's official CLI for Claude."
	legacyClaudeCodeSDKPrefix = "You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK."
)

const (
	promptProfileDefault                = "default"
	promptProfileClaudeCompatible       = "claude-compatible"
	promptProfileClaudeCompatibleStrict = "claude-compatible-strict"
	promptProfileEnv                    = "NIUMA_PROMPT_PROFILE"
)

var cliSystemPromptPrefixes = map[string]bool{
	defaultCLISystemPrompt:    true,
	agentSDKClaudeCodePrefix:  true,
	agentSDKPrefix:            true,
	legacyClaudeCodePrefix:    true,
	legacyClaudeCodeSDKPrefix: true,
}

type splitSystemBlock struct {
	text       string
	cacheScope *string
	source     string
}

func systemPrompt(cwd string) string {
	return joinSystemBlocks(systemPromptBlocks(cwd, "", ""))
}

func (s *Session) observePromptCache(ctx context.Context, blocks []anthropic.SystemBlock) {
	if len(blocks) == 0 {
		return
	}
	report := s.cacheTracker.Observe(blocks)
	if report.BreaksCache {
		observability.Info(ctx, nil, "prompt.cache.break", "query.Session.observePromptCache", "system prompt cache signature changed",
			"text_hash", report.TextHash,
			"cache_control_hash", report.CacheControlHash,
			"text_changed", report.TextChanged,
			"cache_control_changed", report.CacheControlChanged,
		)
		return
	}
	observability.Debug(ctx, nil, "prompt.cache.stable", "query.Session.observePromptCache", "system prompt cache signature stable",
		"text_hash", report.TextHash,
		"cache_control_hash", report.CacheControlHash,
		"initialized", report.Initialized,
	)
}

func (s *Session) observeRequestCache(ctx context.Context, req anthropic.MessagesRequest, thinkingConfig any) {
	report := s.requestCacheTracker.Observe(req, thinkingConfig)
	if report.BreaksCache {
		observability.Info(ctx, nil, "prompt.cache.request_break", "query.Session.observeRequestCache", "request cache-safe signature changed",
			"signature_hash", report.SignatureHash,
			"model_hash", report.ModelHash,
			"system_hash", report.SystemHash,
			"cache_control_hash", report.CacheControlHash,
			"tools_hash", report.ToolsHash,
			"message_prefix_hash", report.MessagePrefixHash,
			"thinking_hash", report.ThinkingHash,
			"model_changed", report.ModelChanged,
			"system_changed", report.SystemChanged,
			"cache_changed", report.CacheChanged,
			"tools_changed", report.ToolsChanged,
			"message_changed", report.MessageChanged,
			"thinking_changed", report.ThinkingChanged,
		)
		return
	}
	observability.Debug(ctx, nil, "prompt.cache.request_stable", "query.Session.observeRequestCache", "request cache-safe signature stable",
		"signature_hash", report.SignatureHash,
		"initialized", report.Initialized,
	)
}

func (s *Session) effectiveSystemBlocks() []anthropic.SystemBlock {
	if text := strings.TrimSpace(s.options.OverrideSystemPrompt); text != "" {
		return []anthropic.SystemBlock{{Type: blockTypeText, Text: text, Source: "override_system_prompt"}}
	}
	var parts []string
	switch {
	case strings.TrimSpace(s.options.CoordinatorPrompt) != "" && strings.TrimSpace(s.options.MainThreadAgentPrompt) == "":
		parts = []string{strings.TrimSpace(s.options.CoordinatorPrompt)}
	case strings.TrimSpace(s.options.MainThreadAgentPrompt) != "":
		parts = []string{strings.TrimSpace(s.options.MainThreadAgentPrompt)}
	case strings.TrimSpace(s.options.SystemPrompt) != "":
		parts = []string{strings.TrimSpace(s.options.SystemPrompt)}
	default:
		parts = s.defaultSystemPromptParts()
	}
	if text := strings.TrimSpace(s.options.SystemAddendum); text != "" {
		parts = append(parts, text)
	}
	return buildSystemPromptBlocks(parts, promptCachingEnabled(s.currentModel()), s.querySource())
}

func systemPromptBlocks(cwd, selectedOutputStyle, language string) []anthropic.SystemBlock {
	return buildSystemPromptBlocks(defaultSystemPromptParts(cwd, selectedOutputStyle, language), promptCachingEnabled(""), defaultQuerySource)
}

func defaultSystemPromptParts(cwd, selectedOutputStyle, language string) []string {
	session := New(nil, tools.NewRegistry(), Options{CWD: cwd, OutputStyle: selectedOutputStyle, Language: language})
	return session.defaultSystemPromptParts()
}

type systemPromptSection struct {
	name       string
	cacheBreak bool
	compute    func() string
}

const (
	featureEnhancedSystemPrompt        = "ENHANCED_SYSTEM_PROMPT"
	featureEnhancedBehaviorConstraints = "ENHANCED_BEHAVIOR_CONSTRAINTS"
)

func (s *Session) defaultSystemPromptParts() []string {
	if s.runtimePolicy().ToolSet == runtimeprofile.ToolSetMinimal {
		return []string{fmt.Sprintf("%s\n\nCWD: %s\nDate: %s", defaultCLISystemPrompt, strings.TrimSpace(s.options.CWD), time.Now().Format("2006-01-02"))}
	}
	if isEnvTruthy("CLAUDE_CODE_SIMPLE") || isEnvTruthy("NIUMA_SIMPLE") {
		return []string{fmt.Sprintf("%s\n\nCWD: %s\nDate: %s", defaultCLISystemPrompt, strings.TrimSpace(s.options.CWD), time.Now().Format("2006-01-02"))}
	}
	if s.promptProfile().IsChat() {
		return s.chatSystemPromptParts()
	}
	cwd := s.options.CWD
	selectedOutputStyle := s.options.OutputStyle
	language := s.options.Language
	settings := config.LoadSettings(cwd).Settings
	if strings.TrimSpace(selectedOutputStyle) == "" {
		selectedOutputStyle = firstNonEmpty(
			getenv("NIUMA_OUTPUT_STYLE"),
			getenv("CLAUDE_CODE_OUTPUT_STYLE"),
			settings.OutputStyle,
		)
	}
	if strings.TrimSpace(language) == "" {
		language = firstNonEmpty(
			getenv("NIUMA_LANGUAGE"),
			getenv("CLAUDE_CODE_LANGUAGE"),
			settings.Language,
		)
	}
	if (s.featureEnabled("PROACTIVE") || s.featureEnabled("KAIROS")) && (isEnvTruthy("NIUMA_PROACTIVE_ACTIVE") || isEnvTruthy("CLAUDE_CODE_PROACTIVE_ACTIVE")) {
		return compactPromptParts([]string{
			proactiveIntroSection(),
			systemRemindersSection(),
			envInfoSection(cwd, s.currentModel(), s.recorderSessionID(), s.recorderTranscriptPath()),
			gitcontext.Snapshot(context.Background(), cwd),
			languageSection(language),
			mcpInstructionsSection(cwd),
			scratchpadSection(),
			functionResultClearingSection(s.currentModel(), s.featureEnabled("CACHED_MICROCOMPACT")),
			summarizeToolResultsSection(),
			recoverySection(),
			sessionDiagnosticsSection(),
			proactiveSection(),
		})
	}
	style, _ := outputstyle.Resolve(cwd, selectedOutputStyle)
	identityPrompt := defaultCLISystemPrompt
	introPrompt := introSection(style)
	systemSection := simpleSystemSection()
	if s.isClaudeCompatibleProfile() {
		identityPrompt = agentSDKPrefix
		introPrompt = claudeCompatibleIntroSection(style)
		systemSection = claudeCompatibleSystemSection()
	}
	parts := []string{
		identityPrompt,
		introPrompt,
		systemSection,
	}
	keepCodingInstructions := style == nil || style.KeepCodingInstructions
	if keepCodingInstructions {
		parts = append(parts, doingTasksSection())
	}
	if keepCodingInstructions && s.featureEnabled(featureEnhancedSystemPrompt) {
		parts = append(parts,
			planningSection(),
			verificationSection(),
			recoverySection(),
			workflowClosureSection(),
		)
	}
	if keepCodingInstructions {
		parts = append(parts,
			informationPrioritySection(),
		)
	}
	if keepCodingInstructions && s.featureEnabled(featureEnhancedBehaviorConstraints) {
		parts = append(parts,
			precisionSection(),
		)
	}
	parts = append(parts, sessionDiagnosticsSection(), usingToolsSection(), actionsSection(), toneAndStyleSection(), outputEfficiencySection(), systemPromptDynamicBoundary)
	dynamicSections := []systemPromptSection{
		{name: "session_guidance", compute: func() string {
			if s.isClaudeCompatibleProfile() {
				return compatibleSessionGuidanceSection(s.enabledToolNames())
			}
			return sessionGuidanceSection(s.enabledToolNames())
		}},
	}
	if s.isClaudeCompatibleProfile() {
		dynamicSections = append(dynamicSections, systemPromptSection{name: "auto_memory", compute: func() string {
			return compatibleMemorySystemBlock(memory.ClaudeCodeProjectMemoryDir(cwd))
		}})
		dynamicSections = append(dynamicSections,
			systemPromptSection{name: "ant_model_override", compute: antModelOverrideSection},
			systemPromptSection{name: "env_info_simple", compute: func() string {
				return envInfoSection(cwd, s.currentModel(), s.recorderSessionID(), s.recorderTranscriptPath())
			}},
			systemPromptSection{name: "language", compute: func() string { return languageSection(language) }},
			systemPromptSection{name: "output_style", compute: func() string { return outputStyleSection(style) }},
			systemPromptSection{name: "mcp_instructions", cacheBreak: true, compute: func() string { return mcpInstructionsSection(cwd) }},
			systemPromptSection{name: "scratchpad", compute: scratchpadSection},
			systemPromptSection{name: "frc", compute: func() string {
				return functionResultClearingSection(s.currentModel(), s.featureEnabled("CACHED_MICROCOMPACT"))
			}},
			systemPromptSection{name: "summarize_tool_results", compute: summarizeToolResultsSection},
			systemPromptSection{name: "git_context", compute: func() string { return compatibleGitStatusSystemContext(cwd) }},
		)
	} else {
		dynamicSections = append(dynamicSections,
			systemPromptSection{name: "git_context", compute: func() string { return gitcontext.Snapshot(context.Background(), cwd) }},
			systemPromptSection{name: "ant_model_override", compute: antModelOverrideSection},
			systemPromptSection{name: "env_info_simple", compute: func() string {
				return envInfoSection(cwd, s.currentModel(), s.recorderSessionID(), s.recorderTranscriptPath())
			}},
			systemPromptSection{name: "language", compute: func() string { return languageSection(language) }},
			systemPromptSection{name: "output_style", compute: func() string { return outputStyleSection(style) }},
			systemPromptSection{name: "mcp_instructions", cacheBreak: true, compute: func() string { return mcpInstructionsSection(cwd) }},
			systemPromptSection{name: "scratchpad", compute: scratchpadSection},
			systemPromptSection{name: "frc", compute: func() string {
				return functionResultClearingSection(s.currentModel(), s.featureEnabled("CACHED_MICROCOMPACT"))
			}},
			systemPromptSection{name: "summarize_tool_results", compute: summarizeToolResultsSection},
		)
	}
	if strings.EqualFold(getenv("USER_TYPE"), "ant") {
		dynamicSections = append(dynamicSections, systemPromptSection{name: "numeric_length_anchors", compute: numericLengthAnchorsSection})
	}
	if s.featureEnabled("TOKEN_BUDGET") {
		dynamicSections = append(dynamicSections, systemPromptSection{name: "token_budget", compute: tokenBudgetSection})
	}
	if s.featureEnabled("KAIROS") || s.featureEnabled("KAIROS_BRIEF") {
		dynamicSections = append(dynamicSections, systemPromptSection{name: "brief", compute: briefSection})
	}
	parts = append(parts, s.resolveSystemPromptSections(dynamicSections)...)
	return compactPromptParts(parts)
}

func (s *Session) promptProfile() promptmode.Mode {
	return promptmode.Parse(firstNonEmpty(s.options.PromptMode, getenv("NIUMA_PROMPT_MODE")), promptmode.Code)
}

func (s *Session) runtimeProfile() runtimeprofile.Profile {
	return s.runtimePolicy().Profile
}

func (s *Session) runtimePolicy() runtimeprofile.Policy {
	policy := resolveRuntimePolicy(s.options.RuntimeProfile)
	if s.options.ComputerUseFastPath && s.options.ComputerUse != nil && s.options.ComputerUseImageSupported && !s.options.DisableTools {
		// Bare desktop context removes discovery/enrichment, not explicitly
		// supplied authorization hooks. Optimization must never bypass a deny.
		policy.RunHooks = true
	}
	return policy
}

func resolveRuntimePolicy(profile runtimeprofile.Profile) runtimeprofile.Policy {
	policy, err := runtimeprofile.Resolve(profile)
	if err == nil {
		return policy
	}
	policy, _ = runtimeprofile.Resolve(runtimeprofile.ProfileBare)
	policy.Profile = profile
	return policy
}

func (s *Session) promptProfileVariant() string {
	if !s.promptProfile().IsCode() {
		return promptProfileDefault
	}
	switch strings.ToLower(strings.TrimSpace(getenv(promptProfileEnv))) {
	case promptProfileClaudeCompatible:
		return promptProfileClaudeCompatible
	case promptProfileClaudeCompatibleStrict:
		return promptProfileClaudeCompatibleStrict
	default:
		return promptProfileDefault
	}
}

func (s *Session) isClaudeCompatibleProfile() bool {
	switch s.promptProfileVariant() {
	case promptProfileClaudeCompatible, promptProfileClaudeCompatibleStrict:
		return true
	default:
		return false
	}
}

func (s *Session) chatSystemPromptParts() []string {
	selectedOutputStyle := s.options.OutputStyle
	language := s.options.Language
	if strings.TrimSpace(selectedOutputStyle) == "" {
		selectedOutputStyle = firstNonEmpty(getenv("NIUMA_OUTPUT_STYLE"), getenv("CLAUDE_CODE_OUTPUT_STYLE"))
	}
	if strings.TrimSpace(language) == "" {
		language = firstNonEmpty(getenv("NIUMA_LANGUAGE"), getenv("CLAUDE_CODE_LANGUAGE"))
	}
	style, _ := outputstyle.Resolve("", selectedOutputStyle)
	parts := []string{
		"You are niuma, a helpful multi-tenant chat assistant.",
		"# System\n- Help the user with their request using only the conversation, explicitly provided context, and enabled tenant/chat tools.\n- Do not assume access to the server's local source code repository, git state, CLAUDE.md files, local skills, or developer machine configuration.\n- Do not reveal or rely on server working directory, git branch, git status, local files, or project instructions unless the user explicitly provided that information in the chat.\n" + toolResultVisibilityLine,
		outputStyleSection(style),
		languageSection(language),
		"# Context Boundary\nThis is a tenant chat session. Local code-development context is intentionally not loaded.",
		systemPromptDynamicBoundary,
		summarizeToolResultsSection(),
	}
	return compactPromptParts(parts)
}

func (s *Session) assembleContextMessages(ctx context.Context, prompt string) contextAssembly {
	if s.promptProfile().IsChat() {
		return contextAssembly{}
	}
	loadOptions := memory.LoadCodeOptions{
		DisableProjectAgentsFallback: s.promptProfileVariant() == promptProfileClaudeCompatibleStrict,
	}
	if !s.runtimePolicy().DiscoverWorkspaceContext {
		loadOptions.DiscoveryMode = memory.DiscoveryExplicit
		loadOptions.ExplicitRoots = append([]string(nil), s.options.ExplicitContextRoots...)
	}
	docs, err := memory.LoadCodeWithOptions(s.options.CWD, prompt, loadOptions)
	if err != nil {
		observability.Error(ctx, nil, "memory.load.error", "query.Session.assembleContextMessages", "load code memory failed", "error", err)
		return contextAssembly{}
	}
	promptDocs, report := memory.PreparePromptDocuments(docs)
	addendum := memory.SystemAddendumFromPreparedDocuments(promptDocs)
	var parts []string
	if strings.TrimSpace(addendum) != "" {
		parts = append(parts, addendum)
	}
	if s.isClaudeCompatibleProfile() {
		if len(parts) == 0 {
			return contextAssembly{
				codeDocs:         docs,
				codePromptReport: report,
				systemMemory:     compatibleMemorySystemBlock(memory.ClaudeCodeProjectMemoryDir(s.options.CWD)),
			}
		}
		userContext := compatibleWorkspaceGuidanceUserMessage(addendum)
		var userMessages []anthropic.MessageParam
		if len(userContext.Content) > 0 {
			userMessages = []anthropic.MessageParam{userContext}
		}
		return contextAssembly{
			codeDocs:         docs,
			userMessages:     userMessages,
			codePromptReport: report,
			systemMemory:     compatibleMemorySystemBlock(memory.ClaudeCodeProjectMemoryDir(s.options.CWD)),
		}
	}
	if len(parts) == 0 {
		return contextAssembly{codeDocs: docs, codePromptReport: report}
	}
	text := "<system-reminder>\nAs you answer the user's questions, you can use the following context. It may or may not be relevant to the current task; do not mention it unless it is useful.\n\n" + strings.Join(parts, "\n\n") + "\n</system-reminder>"
	return contextAssembly{codeDocs: docs, userMessages: []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: text}},
	}}, codePromptReport: report}
}

func compatibleWorkspaceGuidanceUserMessage(addendum string) anthropic.MessageParam {
	addendum = strings.TrimSpace(addendum)
	if addendum == "" {
		return anthropic.MessageParam{}
	}
	guidance := compatibleWorkspaceGuidanceText(addendum)
	text := `<system-reminder>
As you answer the user's questions, you can use the following context:
# claudeMd
Codebase and user instructions are shown below. Be sure to adhere to these instructions. IMPORTANT: These instructions OVERRIDE any default behavior and you MUST follow them exactly as written. If two of these instructions conflict with each other or with the user's current request, surface the conflict explicitly and state which one you are following instead of silently choosing.

` + guidance + `
# currentDate
Today's date is ` + time.Now().Format("2006-01-02") + `.

      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.
</system-reminder>
`
	return anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: text}},
	}
}

func compatibleWorkspaceGuidanceText(addendum string) string {
	text := strings.TrimSpace(addendum)
	text = strings.TrimPrefix(text, "# Memory")
	text = strings.TrimSpace(text)
	prefix := "The following guidance is available for this workspace. Follow it as durable user/project guidance unless it conflicts with higher-priority instructions in the current conversation."
	text = strings.TrimPrefix(text, prefix)
	return strings.TrimSpace(text)
}

func compatibleGitStatusSystemContext(cwd string) string {
	snapshot := strings.TrimSpace(gitcontext.Snapshot(context.Background(), cwd))
	if snapshot == "" {
		return ""
	}
	snapshot = strings.TrimSpace(strings.TrimPrefix(snapshot, "# Git Snapshot"))
	if snapshot == "" {
		return ""
	}
	return "gitStatus: " + snapshot
}

func compatibleMemorySystemBlock(memoryDir string) string {
	dirLine := "You have a persistent, file-based memory system for this coding workspace."
	if strings.TrimSpace(memoryDir) != "" {
		dirLine = "You have a persistent, file-based memory system at `" + strings.TrimSpace(memoryDir) + "`. This directory already exists - write to it directly with the Write tool (do not run mkdir or check for its existence)."
	}
	lines := []string{
		"# auto memory",
		"",
		dirLine,
		"",
		"You should build up this memory system over time so that future conversations can have a complete picture of who the user is, how they would like to collaborate with you, what behaviors to avoid or repeat, and the context behind the work the user gives you.",
		"",
		"If the user explicitly asks you to remember something, save it immediately as whichever type fits best. If they ask you to forget something, find and remove the relevant entry. For explicitly read-only tasks, do not write memory unless the user directly asks you to remember or forget something.",
		"",
		"## Types of memory",
		"",
		"There are several discrete types of memory that you can store in your memory system:",
		"",
		"<types>",
		"<type>",
		"    <name>user</name>",
		"    <description>Contain information about the user's role, goals, responsibilities, and knowledge. Great user memories help you tailor future behavior to the user's preferences and perspective. Your goal in reading and writing these memories is to build up an understanding of who the user is and how you can be most helpful to them specifically. For example, you should collaborate with a senior software engineer differently than a student who is coding for the very first time. Keep in mind that the aim here is to be helpful to the user. Avoid writing memories about the user that could be viewed as a negative judgement or that are not relevant to the work you're trying to accomplish together.</description>",
		"    <when_to_save>When you learn any stable details about the user's role, preferences, responsibilities, or knowledge.</when_to_save>",
		"    <how_to_use>When your work should be informed by the user's profile or perspective. For example, if the user is asking you to explain a part of the code, answer in a way that is tailored to the details they will find most valuable.</how_to_use>",
		"    <examples>",
		"    user: I'm a data scientist investigating what logging we have in place",
		"    assistant: [saves user memory: user is a data scientist, currently focused on observability/logging]",
		"    </examples>",
		"</type>",
		"<type>",
		"    <name>feedback</name>",
		"    <description>Guidance the user has given you about how to approach work - both what to avoid and what to keep doing. These are a very important type of memory because they help you remain coherent and responsive to the way you should approach work in the project. Record from failure and success: if you only save corrections, you will avoid past mistakes but drift away from approaches the user has already validated.</description>",
		"    <when_to_save>Any time the user corrects your approach or confirms a non-obvious approach worked. Corrections are easy to notice; confirmations are quieter. Include why so future decisions can judge edge cases.</when_to_save>",
		"    <how_to_use>Let these memories guide your behavior so the user does not need to offer the same guidance twice.</how_to_use>",
		"    <body_structure>Lead with the rule itself, then a Why line and a How to apply line.</body_structure>",
		"</type>",
		"<type>",
		"    <name>project</name>",
		"    <description>Information about ongoing work, goals, initiatives, bugs, incidents, deadlines, or rationale within the project that is not otherwise derivable from the code or git history. Project memories help you understand the broader context and motivation behind the work the user is doing within this working directory.</description>",
		"    <when_to_save>When you learn who is doing what, why, or by when. Convert relative dates to absolute dates before saving.</when_to_save>",
		"    <how_to_use>Use this to understand the broader context and motivation behind project work.</how_to_use>",
		"    <body_structure>Lead with the fact or decision, then a Why line and a How to apply line.</body_structure>",
		"</type>",
		"<type>",
		"    <name>reference</name>",
		"    <description>Stores pointers to where information can be found in external systems, such as issue trackers, dashboards, documents, channels, or operational runbooks. These memories help you remember where to look for up-to-date information outside of the project directory.</description>",
		"    <when_to_save>When you learn about resources in external systems and their purpose.</when_to_save>",
		"    <how_to_use>Use this when the user references external systems or information that may live outside the project directory.</how_to_use>",
		"</type>",
		"</types>",
		"",
		"## What NOT to save in memory",
		"",
		"- Code patterns, conventions, architecture, file paths, or project structure - these can be derived by reading the current project state.",
		"- Git history, recent changes, or who-changed-what - git log and git blame are authoritative.",
		"- Debugging solutions or fix recipes - the fix is in the code; the commit message has the context.",
		"- Anything already documented in CLAUDE.md files.",
		"- Ephemeral task details: in-progress work, temporary state, current conversation context.",
		"",
		"These exclusions apply even when the user explicitly asks you to save. If they ask to save a PR list or activity summary, ask what was surprising or non-obvious about it - that is the part worth keeping.",
		"",
		"## How to save memories",
		"",
		"Saving a memory is a two-step process:",
		"",
		"**Step 1** - write the memory to its own file, for example `user_role.md` or `feedback_testing.md`, using this frontmatter format:",
		"",
		"```markdown",
		"---",
		"name: {{memory name}}",
		"description: {{one-line description used to decide relevance in future conversations}}",
		"metadata:",
		"  type: {{user, feedback, project, reference}}",
		"---",
		"",
		"{{memory content - for feedback/project types, structure as: rule/fact, then Why and How to apply lines}}",
		"```",
		"",
		"**Step 2** - add a pointer to that file in `MEMORY.md`. `MEMORY.md` is an index, not a memory. Each entry should be one line under about 150 characters, such as `- [Title](file.md) - one-line hook`. It has no frontmatter. Never write memory content directly into `MEMORY.md`.",
		"",
		"- `MEMORY.md` is always loaded into your conversation context; lines after 200 will be truncated, so keep the index concise.",
		"- Keep the name, description, and type fields in memory files up-to-date with the content.",
		"- Organize memory semantically by topic, not chronologically.",
		"- Update or remove memories that turn out to be wrong or outdated.",
		"- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.",
		"",
		"## When to access memories",
		"",
		"- When memories seem relevant, or the user references prior-conversation work.",
		"- You MUST access memory when the user explicitly asks you to check, recall, or remember.",
		"- If the user says to ignore or not use memory, proceed as if MEMORY.md were empty. Do not apply remembered facts, cite, compare against, or mention memory content.",
		"- Memory records can become stale over time. Use memory as context for what was true at a given point in time. Before answering the user or building assumptions based solely on information in memory records, verify that the memory is still correct and up-to-date by reading the current state of the files or resources. If a recalled memory conflicts with current information, trust what you observe now rather than acting on the stale memory.",
		"",
		"## Before recommending from memory",
		"",
		"A memory that names a specific function, file, or flag is a claim that it existed when the memory was written. It may have been renamed, removed, or never merged. Before recommending it:",
		"",
		"- If the memory names a file path: check the file exists.",
		"- If the memory names a function or flag: grep for it.",
		"- If the user is about to act on your recommendation, not just asking about history, verify first.",
		"",
		"\"The memory says X exists\" is not the same as \"X exists now.\"",
		"",
		"A memory that summarizes repo state, activity logs, or architecture snapshots is frozen in time. If the user asks about recent or current state, prefer git log or reading the code over recalling the snapshot.",
		"",
		"## Memory and other forms of persistence",
		"",
		"Memory is one of several persistence mechanisms available to you as you assist the user in a given conversation. The distinction is often that memory can be recalled in future conversations and should not be used for persisting information that is only useful within the scope of the current conversation.",
		"- When to use or update a plan instead of memory: If you are about to start a non-trivial implementation task and would like to reach alignment with the user on your approach, use a plan rather than saving this information to memory. Similarly, if you already have a plan within the conversation and you have changed your approach, persist that change by updating the plan rather than saving a memory.",
		"- When to use or update tasks instead of memory: When you need to break your work in the current conversation into discrete steps or keep track of your progress, use tasks or progress notes instead of saving to memory. Tasks are useful for current conversation work, but memory should be reserved for information that will be useful in future conversations.",
		"",
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type runtimeStatusRequest struct {
	Turn       int
	MaxTurns   int
	UserPrompt string
	LoopStreak int
	LoopCall   string
}

func (s *Session) withRuntimeStatusMessages(ctx context.Context, messages []anthropic.MessageParam, request runtimeStatusRequest) []anthropic.MessageParam {
	text := s.runtimeStatusText(ctx, messages, request)
	if strings.TrimSpace(text) == "" {
		return messages
	}
	out := make([]anthropic.MessageParam, 0, len(messages)+1)
	out = append(out, messages...)
	out = append(out, anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: text}},
	})
	return out
}

// withPendingGateNudge appends a completion-gate reminder to the request only.
// Kept out of `messages` so repeated gate failures cannot stack one copy per
// turn; the reminder always reflects the most recent rejection.
func withPendingGateNudge(messages []anthropic.MessageParam, nudge string) []anthropic.MessageParam {
	if strings.TrimSpace(nudge) == "" {
		return messages
	}
	out := make([]anthropic.MessageParam, 0, len(messages)+1)
	out = append(out, messages...)
	out = append(out, anthropic.MessageParam{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: blockTypeText, Text: nudge}},
	})
	return out
}

func (s *Session) runtimeStatusText(ctx context.Context, messages []anthropic.MessageParam, request runtimeStatusRequest) string {
	if !s.promptProfile().IsCode() {
		return ""
	}
	var sections []string
	if section := s.runtimeTurnStatus(request, messages); section != "" {
		sections = append(sections, section)
	}
	if section := loopguard.Awareness(request.LoopStreak, request.LoopCall); section != "" {
		sections = append(sections, section)
	}
	if section := runtimeTaskStrategySection(request.UserPrompt); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimeToolPlanningStatus(messages); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimePermissionStatus(); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimeTodoStatus(ctx); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimePlanStatus(ctx); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimeAgentTaskStatus(ctx); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimeRecentAgentEvidenceStatus(); section != "" {
		sections = append(sections, section)
	}
	if section := s.runtimeAgentEvidenceFollowUpReminderStatus(); section != "" {
		sections = append(sections, section)
	}
	if len(sections) == 0 {
		return ""
	}
	return "<system-reminder>\nCurrent runtime status for this coding session. Use it to keep planning and background work consistent; do not mention it unless relevant.\n\n" + strings.Join(sections, "\n\n") + "\n</system-reminder>"
}

type runtimeTaskType string

const (
	runtimeTaskUnknown               runtimeTaskType = ""
	runtimeTaskRepoHealthAudit       runtimeTaskType = "repo_health_audit"
	runtimeTaskReleaseReadinessAudit runtimeTaskType = "release_readiness_audit"
	runtimeTaskEntrypointAudit       runtimeTaskType = "entrypoint_audit"
	runtimeTaskDocReview             runtimeTaskType = "doc_review"
	runtimeTaskCodeReview            runtimeTaskType = "code_review"
	runtimeTaskCodeChange            runtimeTaskType = "code_change"
	runtimeTaskTargetedRepair        runtimeTaskType = "targeted_repair"
	runtimeTaskReleaseRepair         runtimeTaskType = "release_repair"
	runtimeTaskEntrypointRepair      runtimeTaskType = "entrypoint_repair"
	runtimeTaskMetadataRepair        runtimeTaskType = "metadata_repair"
)

type runtimeTaskClassification struct {
	Type runtimeTaskType
}

func runtimeTaskStrategySection(prompt string) string {
	switch classifyRuntimeTaskPrompt(prompt).Type {
	case runtimeTaskRepoHealthAudit:
		return repoHealthAuditStrategySection()
	case runtimeTaskReleaseReadinessAudit:
		return releaseReadinessAuditStrategySection()
	case runtimeTaskEntrypointAudit:
		return entrypointAuditStrategySection()
	case runtimeTaskTargetedRepair, runtimeTaskReleaseRepair, runtimeTaskEntrypointRepair, runtimeTaskMetadataRepair:
		return repairSafetyStrategySection(classifyRuntimeTaskPrompt(prompt).Type)
	default:
		return ""
	}
}

func isRepoHealthAuditPrompt(prompt string) bool {
	return classifyRuntimeTaskPrompt(prompt).Type == runtimeTaskRepoHealthAudit
}

func classifyRuntimeTaskPrompt(prompt string) runtimeTaskClassification {
	text := normalizeRuntimeTaskPrompt(prompt)
	if text == "" {
		return runtimeTaskClassification{}
	}
	if containsAny(text, []string{
		"不要审计整个项目",
		"不要审计项目",
		"不要做项目审计",
		"not audit the whole project",
		"not a repo audit",
		"not a repository audit",
	}) {
		return runtimeTaskClassification{}
	}
	semanticText := stripRuntimeTaskURLs(text)
	if looksLikeCodeReviewPrompt(semanticText) && !looksLikeExplicitRepairRequest(semanticText) {
		return runtimeTaskClassification{Type: runtimeTaskCodeReview}
	}
	if looksLikeReadOnlyRepairAnalysisPrompt(semanticText) {
		return runtimeTaskClassification{}
	}
	if repairType := classifyRepairRuntimeTask(semanticText); repairType != runtimeTaskUnknown {
		return runtimeTaskClassification{Type: repairType}
	}
	if looksLikeFocusedCodeChangePrompt(semanticText) {
		return runtimeTaskClassification{Type: runtimeTaskCodeChange}
	}
	if looksLikeDocReviewPrompt(text) {
		return runtimeTaskClassification{Type: runtimeTaskDocReview}
	}
	if isReleaseReadinessAuditPrompt(text) {
		return runtimeTaskClassification{Type: runtimeTaskReleaseReadinessAudit}
	}
	if isEntrypointAuditPrompt(text) {
		return runtimeTaskClassification{Type: runtimeTaskEntrypointAudit}
	}
	if isGeneralRepoHealthAuditPrompt(text) {
		return runtimeTaskClassification{Type: runtimeTaskRepoHealthAudit}
	}
	return runtimeTaskClassification{}
}

func classifyRepairRuntimeTask(text string) runtimeTaskType {
	if looksLikeReadOnlyRepairAnalysisPrompt(text) {
		return runtimeTaskUnknown
	}
	actionLike := looksLikeExplicitRepairRequest(text) || looksLikeExplicitLinkRepairRequest(text) || containsAny(text, []string{
		"修复",
		"修正",
		"修改",
		"更改",
		"改成",
		"改为",
		"替换",
		"统一",
		"创建",
		"推送",
	})
	if !actionLike {
		return runtimeTaskUnknown
	}
	if containsAny(text, []string{
		"release",
		"publish",
		"version",
		"tag",
		"install",
		"upgrade",
		"engine",
		"engines",
		"版本",
		"发版",
		"发布",
		"安装",
		"升级",
	}) {
		return runtimeTaskReleaseRepair
	}
	if containsAny(text, []string{
		"entrypoint",
		"entry point",
		"slash command",
		"hook",
		"script",
		"cli",
		"command",
		"first-run",
		"first run",
		"入口",
		"命令",
		"脚本",
		"钩子",
		"首次使用",
	}) || containsExplicitSlashCommand(text) {
		return runtimeTaskEntrypointRepair
	}
	if containsAny(text, []string{
		"package",
		"readme",
		"index",
		"count",
		"manifest",
		"metadata",
		"badge",
		"package.json",
		"go.mod",
		"pyproject.toml",
		"cargo.toml",
		"元数据",
		"清单",
		"计数",
	}) {
		return runtimeTaskMetadataRepair
	}
	if containsAny(text, []string{
		"引用",
		"配置",
		"source of truth",
		"生成文件",
		"generated",
	}) {
		return runtimeTaskTargetedRepair
	}
	return runtimeTaskUnknown
}

func looksLikeReadOnlyRepairAnalysisPrompt(text string) bool {
	return containsAny(text, []string{
		"先不改",
		"不要修改",
		"不修改",
		"只分析",
		"先分析",
		"只设计",
		"先设计",
		"read-only",
		"review only",
		"do not edit",
		"don't edit",
		"do not change",
		"no changes",
	})
}

func normalizeRuntimeTaskPrompt(prompt string) string {
	return strings.ToLower(strings.Join(strings.Fields(prompt), " "))
}

var runtimeTaskURLRE = regexp.MustCompile(`(?i)\b(?:https?|ssh|git)://\S+|\bgit@[\w.-]+:[^\s]+`)

func stripRuntimeTaskURLs(text string) string {
	return strings.TrimSpace(runtimeTaskURLRE.ReplaceAllString(text, " "))
}

func looksLikeCodeReviewPrompt(text string) bool {
	if containsEnglishTaskWord(text, []string{"review"}) && containsAny(text, []string{
		"branch", "pull request", " pr ", "pr #", "diff", "change", "commit",
		"分支", "改动", "代码", "提交",
	}) {
		return true
	}
	return containsAny(text, []string{
		"code review",
		"review this branch",
		"review the branch",
		"review this pr",
		"review the pr",
		"review这个分支",
		"review 这个分支",
		"review这个pr",
		"review 这个 pr",
		"审查这个分支",
		"审查这个 pr",
		"审查代码",
		"代码审查",
		"评审代码",
	})
}

func looksLikeExplicitRepairRequest(text string) bool {
	return containsEnglishTaskWord(text, []string{
		"fix",
		"repair",
		"replace",
		"align",
		"correct",
		"create",
		"push",
	}) || containsAny(text, []string{
		"修复",
		"修正",
		"修改",
		"更改",
		"改成",
		"改为",
		"替换",
		"统一",
		"创建",
		"推送",
	})
}

func looksLikeExplicitLinkRepairRequest(text string) bool {
	trimmed := strings.TrimSpace(text)
	if containsAny(trimmed, []string{"how do i", "how can i", "怎么添加", "如何添加", "怎么链接", "如何链接"}) {
		return false
	}
	if strings.HasPrefix(trimmed, "add ") || strings.HasPrefix(trimmed, "link ") ||
		strings.HasPrefix(trimmed, "please add ") || strings.HasPrefix(trimmed, "please link ") {
		return true
	}
	return strings.HasPrefix(trimmed, "添加") || strings.HasPrefix(trimmed, "链接") ||
		(strings.Contains(trimmed, "把") || strings.Contains(trimmed, "将")) &&
			(strings.Contains(trimmed, "链接到") || strings.Contains(trimmed, "添加到"))
}

func containsEnglishTaskWord(text string, words []string) bool {
	for _, word := range words {
		matched, err := regexp.MatchString(`(?:^|[^a-z0-9_])`+regexp.QuoteMeta(word)+`(?:$|[^a-z0-9_])`, text)
		if err == nil && matched {
			return true
		}
	}
	return false
}

var explicitSlashCommandRE = regexp.MustCompile(`(?:^|\s)/[a-z0-9][a-z0-9_-]*(?:\s|$)`)

func containsExplicitSlashCommand(text string) bool {
	return explicitSlashCommandRE.MatchString(text)
}

func isGeneralRepoHealthAuditPrompt(text string) bool {
	if containsAny(text, []string{
		"repo health",
		"repository health",
		"project audit",
		"repository audit",
		"release readiness",
		"项目健康",
		"项目审计",
		"项目体检",
		"发版准备",
		"发布准备",
		"熟悉当前项目",
		"项目是否有问题",
	}) {
		return true
	}
	projectLike := containsAny(text, []string{
		"project",
		"repository",
		"codebase",
		"项目",
		"仓库",
		"代码库",
	}) || strings.Contains(" "+text+" ", " repo ")
	if !projectLike {
		return false
	}
	return containsAny(text, []string{
		"audit",
		"health",
		"readiness",
		"optimiz",
		"improv",
		"issue",
		"problem",
		"gap",
		"risk",
		"客观分析",
		"优化",
		"问题",
		"缺口",
		"风险",
	})
}

func isReleaseReadinessAuditPrompt(text string) bool {
	if containsAny(text, []string{
		"release readiness",
		"发版准备",
		"发布准备",
	}) {
		return true
	}
	releaseLike := containsAny(text, []string{
		"release",
		"publish",
		"version",
		"tag",
		"install",
		"upgrade",
		"发版",
		"发布",
		"版本",
		"安装",
		"升级",
	})
	return releaseLike && containsAny(text, []string{
		"audit",
		"check",
		"readiness",
		"ready",
		"准备",
		"检查",
		"审计",
		"风险",
		"问题",
	})
}

func isEntrypointAuditPrompt(text string) bool {
	entrypointLike := containsAny(text, []string{
		"entrypoint",
		"entry point",
		"first-run",
		"first run",
		"missing command",
		"slash command",
		"hook",
		"cli command",
		"user command",
		"入口",
		"首次使用",
		"命令缺失",
		"斜杠命令",
		"钩子",
	})
	return entrypointLike && containsAny(text, []string{
		"audit",
		"check",
		"risk",
		"missing",
		"problem",
		"issue",
		"审计",
		"检查",
		"风险",
		"问题",
		"缺失",
	})
}

func looksLikeFocusedCodeChangePrompt(text string) bool {
	return containsAny(text, []string{
		"fix ",
		"fix:",
		"fix bug",
		"bug",
		"panic",
		"implement",
		"add feature",
		"refactor",
		"修复",
		"实现",
		"改代码",
		"重构",
	})
}

func looksLikeDocReviewPrompt(text string) bool {
	return containsAny(text, []string{
		"readme summary",
		"summarize readme",
		"summarise readme",
		"polish readme",
		"rewrite readme",
		"translate readme",
		"读取 readme",
		"总结项目做什么",
		"润色 readme",
		"翻译 readme",
		"文案",
	})
}

func containsAny(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func (s *Session) runtimePermissionStatus() string {
	var lines []string
	if s.options.RuntimePermissionMode != nil {
		mode := strings.TrimSpace(s.options.RuntimePermissionMode())
		if normalized, ok := permissions.NormalizeMode(mode); ok {
			mode = normalized
		}
		if mode != "" {
			lines = append(lines, "- mode: "+mode)
			lines = append(lines, "- Tools are still enforced by runtime policy. If a tool call is denied, do not retry the exact same call; explain or choose a permitted alternative.")
		}
	}
	if roots := s.additionalWritableRootsForPrompt(); len(roots) > 0 {
		lines = append(lines, "- additional writable directories:")
		for _, root := range firstNStrings(roots, 5) {
			lines = append(lines, "  - "+root)
		}
		if hidden := len(roots) - 5; hidden > 0 {
			lines = append(lines, fmt.Sprintf("  - %d more hidden", hidden))
		}
		lines = append(lines, "- These directories are writable roots in addition to the current working directory; use absolute paths when operating there.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "## Permission context\n" + strings.Join(lines, "\n")
}

func (s *Session) additionalWritableRootsForPrompt() []string {
	memoryRoot := cleanPathForPrompt(memory.ClaudeCodeProjectMemoryDir(s.options.CWD))
	seen := map[string]bool{}
	var out []string
	for _, root := range s.options.WritableRoots {
		root = cleanPathForPrompt(root)
		if root == "" || root == memoryRoot || seen[root] {
			continue
		}
		seen[root] = true
		out = append(out, root)
	}
	return out
}

func cleanPathForPrompt(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func firstNStrings(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func (s *Session) shouldDisableToolsForFinalTurn(turn int, messages []anthropic.MessageParam) bool {
	if !s.promptProfile().IsCode() || turn <= 0 || s.options.MaxTurns <= 0 || turn < s.options.MaxTurns {
		return false
	}
	toolUses, toolResults := countToolBlocks(messages)
	return toolUses > 0 || toolResults > 0
}

func recoverPartialTextStreamResult(err error) (*anthropic.StreamResult, bool) {
	var partialErr *anthropic.PartialStreamError
	if !errors.As(err, &partialErr) || partialErr.Partial == nil {
		return nil, false
	}
	if !streamResultHasText(partialErr.Partial) || streamResultHasToolUse(partialErr.Partial) {
		return nil, false
	}
	recovered := *partialErr.Partial
	if strings.TrimSpace(recovered.StopReason) == "" {
		recovered.StopReason = "partial_stream_error"
	}
	return &recovered, true
}

func streamResultHasText(result *anthropic.StreamResult) bool {
	if result == nil {
		return false
	}
	for _, block := range result.Message.Content {
		if block.Type == blockTypeText && strings.TrimSpace(block.Text) != "" {
			return true
		}
	}
	return false
}

func streamResultHasToolUse(result *anthropic.StreamResult) bool {
	if result == nil {
		return false
	}
	for _, block := range result.Message.Content {
		if block.Type == blockTypeToolUse {
			return true
		}
	}
	return false
}

func (s *Session) runtimeTurnStatus(request runtimeStatusRequest, messages []anthropic.MessageParam) string {
	if request.Turn <= 0 || request.MaxTurns <= 0 || request.Turn < request.MaxTurns {
		return ""
	}
	toolUses, toolResults := countToolBlocks(messages)
	if toolResults == 0 && toolUses == 0 {
		return ""
	}
	return fmt.Sprintf(`## Turn budget reminder
- This is model turn %d of %d, the last turn allowed by the current max-turns budget.
- If you already have enough file/function/line evidence to answer the user, produce the final answer now.
- Tools are disabled for this final request when prior tool evidence already exists; synthesize the best answer from the available context.`, request.Turn, request.MaxTurns)
}

func (s *Session) runtimeToolPlanningStatus(messages []anthropic.MessageParam) string {
	toolUses, toolResults := countToolBlocks(messages)
	if toolResults < 8 && toolUses < 10 {
		return ""
	}
	return fmt.Sprintf(`## Tool planning reminder
- You have already gathered %d tool results from %d tool calls in this request context.
- If you have enough file/function/line evidence to answer, stop exploring and synthesize the answer now.
- Before calling Read, use paths returned by LS, Glob, or Grep; do not invent file paths.
- Prefer scoped Grep with output_mode:"files_with_matches" or Glob before broad Read loops, then read only the few files or line ranges needed.`, toolResults, toolUses)
}

func countToolBlocks(messages []anthropic.MessageParam) (int, int) {
	toolUses := 0
	toolResults := 0
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case blockTypeToolUse:
				toolUses++
			case blockTypeToolResult:
				toolResults++
			}
		}
	}
	return toolUses, toolResults
}

func (s *Session) runtimeTodoStatus(ctx context.Context) string {
	todos, err := todowrite.Load(s.options.CWD)
	if err != nil {
		observability.Error(ctx, nil, "runtime_status.todos.error", "query.Session.runtimeTodoStatus", "load todo state failed", "error", err)
		return ""
	}
	if len(todos) == 0 {
		return ""
	}
	lines := []string{"## Active todos"}
	count := 0
	completed := 0
	for _, todo := range todos {
		status := strings.TrimSpace(todo.Status)
		if status == "completed" {
			completed++
			continue
		}
		content := trimRuntimeStatusText(todoRuntimeContent(todo), 160)
		if content == "" {
			continue
		}
		priority := strings.TrimSpace(todo.Priority)
		label := status
		if priority != "" {
			label += "/" + priority
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", label, content))
		count++
		if count >= 8 {
			break
		}
	}
	if count == 0 {
		return ""
	}
	if completed > 0 {
		lines = append(lines, fmt.Sprintf("- completed: %d hidden", completed))
	}
	return strings.Join(lines, "\n")
}

func todoRuntimeContent(todo todowrite.Todo) string {
	if strings.TrimSpace(todo.Status) == "in_progress" && strings.TrimSpace(todo.ActiveForm) != "" {
		return todo.ActiveForm
	}
	return todo.Content
}

func (s *Session) runtimePlanStatus(ctx context.Context) string {
	state, err := planmode.Load(s.options.CWD)
	if err != nil {
		observability.Error(ctx, nil, "runtime_status.plan.error", "query.Session.runtimePlanStatus", "load plan mode state failed", "error", err)
		return ""
	}
	if !state.Active {
		return ""
	}
	plan := trimRuntimeStatusText(state.Plan, 900)
	if plan == "" {
		return "## Plan mode\n- active: true"
	}
	return "## Plan mode\n- active: true\n- plan: " + plan
}

func (s *Session) runtimeAgentTaskStatus(ctx context.Context) string {
	if s.options.TaskStore == nil {
		return ""
	}
	store, ok := s.options.TaskStore.(agentTaskLister)
	if !ok {
		return ""
	}
	tasks, err := store.ListAgentTasks(ctx, 5)
	if err != nil {
		observability.Error(ctx, nil, "runtime_status.agent_tasks.error", "query.Session.runtimeAgentTaskStatus", "list agent tasks failed", "error", err)
		return ""
	}
	if len(tasks) == 0 {
		return ""
	}
	agentGetAvailable := s.agentGetAvailable()
	lines := []string{
		"## Background agent tasks",
		"- Treat running rows as status only; terminal rows with result preview or capability_loop are parent decision inputs.",
		"- Use terminal task capability_loop fields to carry forward evidence, assumptions, unknowns, verification, risks, and next_action before finalizing or choosing recovery.",
		"- Failed, cancelled, or timeout terminal tasks may still contain partial evidence; preserve it as partial evidence and surface remaining unknowns/risks instead of discarding the task.",
	}
	if agentGetAvailable {
		lines = append(lines,
			"- If a task is running, do not infer its result; either wait, report that it is still running, or use AgentGet for progress when relevant.",
			"- Completed, failed, cancelled, or timeout tasks are notifications that final task state is available; call AgentGet with the task id before using or summarizing findings.",
		)
	} else {
		lines = append(lines,
			"- If a task is running, do not infer its result; either wait or report that it is still running.",
			"- Completed, failed, cancelled, or timeout tasks are notifications that final task state is available; use the status, result preview, transcript_path, and output_file below because AgentGet is not available in this request.",
		)
	}
	headerLineCount := len(lines)
	for _, task := range tasks {
		status := strings.TrimSpace(task.Status)
		if status == "" {
			status = "unknown"
		}
		if s.agentTaskNotificationAcknowledged(task.ID, status) {
			continue
		}
		name := trimRuntimeStatusText(task.AgentName, 48)
		if name == "" {
			name = "agent"
		}
		description := trimRuntimeStatusText(task.Description, 160)
		if description == "" {
			description = "no description"
		}
		action := capabilityloop.TaskStatusAction(status, agentGetAvailable)
		resultHint := capabilityloop.TaskResultHint(status, task.ResultJSON)
		if !agentGetAvailable {
			stored := capabilityloop.StoredTask{
				ID:          task.ID,
				AgentName:   task.AgentName,
				Description: task.Description,
				Status:      task.Status,
				ResultJSON:  task.ResultJSON,
			}
			if evidence, ok := capabilityloop.DecisionContextFromStoredTask(stored); ok {
				s.rememberRecentAgentEvidence(evidence)
			}
		}
		var suffixes []string
		if action != "" {
			suffixes = append(suffixes, action)
		}
		if attachment := s.agentTaskProcessAttachment(task.ID, status); attachment != "" {
			suffixes = append(suffixes, attachment)
		}
		if resultHint != "" {
			suffixes = append(suffixes, resultHint)
		}
		if len(suffixes) > 0 {
			lines = append(lines, fmt.Sprintf("- #%d %s %s: %s; %s", task.ID, name, status, description, strings.Join(suffixes, "; ")))
			continue
		}
		lines = append(lines, fmt.Sprintf("- #%d %s %s: %s", task.ID, name, status, description))
	}
	if len(lines) == headerLineCount {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (s *Session) agentTaskProcessAttachment(taskID uint64, status string) string {
	if strings.TrimSpace(strings.ToLower(status)) != agenttasks.StatusRunning || s.options.TaskController == nil {
		return ""
	}
	if s.options.TaskController.Active(taskID) {
		return "process_attachment: attached_to_current_process"
	}
	return "process_attachment: not_attached_to_current_process; the task may be running in another process or interrupted; do not wait indefinitely without checking progress"
}

func (s *Session) agentGetAvailable() bool {
	registry := s.effectiveRegistry()
	if registry == nil {
		return false
	}
	_, ok := registry.Get("AgentGet")
	return ok
}

func (s *Session) acknowledgeAgentGetResult(content string) {
	var decoded struct {
		Task struct {
			ID          uint64 `json:"id"`
			Status      string `json:"status"`
			AgentName   string `json:"agent_name"`
			Description string `json:"description"`
		} `json:"task"`
		Result struct {
			Status         string                  `json:"status"`
			AgentName      string                  `json:"agent_name"`
			SessionID      string                  `json:"session_id"`
			TranscriptPath string                  `json:"transcript_path"`
			OutputFile     string                  `json:"output_file"`
			WorktreePath   string                  `json:"worktree_path"`
			WorktreeBranch string                  `json:"worktree_branch"`
			CapabilityLoop capabilityloop.HintData `json:"capability_loop"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil || decoded.Task.ID == 0 {
		return
	}
	if !capabilityloop.TaskStatusIsTerminal(decoded.Task.Status) {
		return
	}
	if s.acknowledgedAgentTasks == nil {
		s.acknowledgedAgentTasks = map[uint64]bool{}
	}
	s.acknowledgedAgentTasks[decoded.Task.ID] = true
	if capabilityloop.HasHint(decoded.Result.CapabilityLoop) {
		s.rememberRecentAgentEvidence(capabilityloop.DecisionContext{
			TaskID:         decoded.Task.ID,
			EvidenceSource: "agent_get",
			AgentName:      firstNonEmpty(decoded.Result.AgentName, decoded.Task.AgentName),
			Description:    decoded.Task.Description,
			Status:         firstNonEmpty(decoded.Result.Status, decoded.Task.Status),
			SessionID:      decoded.Result.SessionID,
			TranscriptPath: decoded.Result.TranscriptPath,
			OutputFile:     decoded.Result.OutputFile,
			WorktreePath:   decoded.Result.WorktreePath,
			WorktreeBranch: decoded.Result.WorktreeBranch,
			Loop:           decoded.Result.CapabilityLoop,
		})
	}
}

func (s *Session) rememberAgentEvidenceFromMessages(messages []anthropic.MessageParam) {
	if len(messages) == 0 {
		return
	}
	type toolInfo struct {
		Name  string
		Input json.RawMessage
	}
	toolsByID := map[string]toolInfo{}
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case blockTypeToolUse:
				if strings.TrimSpace(block.ID) != "" && strings.TrimSpace(block.Name) != "" {
					toolsByID[block.ID] = toolInfo{Name: block.Name, Input: block.Input}
				}
			case blockTypeToolResult:
				toolUseID := firstNonEmpty(block.ToolUseID, block.ID)
				info := toolsByID[toolUseID]
				toolName := firstNonEmpty(block.Name, info.Name)
				if strings.EqualFold(toolName, "AgentGet") && strings.TrimSpace(block.Content) != "" {
					s.acknowledgeAgentGetResult(block.Content)
					continue
				}
				s.rememberCapabilityLoopToolResult(toolName, toolUseID, info.Input, block.Content)
			}
		}
	}
}

func (s *Session) rememberAgentEvidenceFromCompactSummary(messages []anthropic.MessageParam) {
	if len(messages) == 0 {
		return
	}
	filtered := make([]anthropic.MessageParam, 0, len(messages))
	for _, message := range messages {
		var blocks []anthropic.ContentBlock
		for _, block := range message.Content {
			text := strings.TrimSpace(capabilityloop.BlockTextForCompactFacts(block))
			if text == "" {
				continue
			}
			if !strings.Contains(text, "Conversation summary so far:") && !strings.Contains(text, "## Runtime Extracted Facts") {
				continue
			}
			if !strings.Contains(text, "Capability Loop") {
				continue
			}
			blocks = append(blocks, block)
		}
		if len(blocks) > 0 {
			filtered = append(filtered, anthropic.MessageParam{Role: message.Role, Content: blocks})
		}
	}
	if len(filtered) == 0 {
		return
	}
	facts := compact.ExtractFacts(filtered)
	loop := capabilityloop.HintData{
		Evidence:     facts.CapabilityEvidence,
		Assumptions:  facts.CapabilityAssumptions,
		Unknowns:     facts.CapabilityUnknowns,
		Verification: facts.CapabilityVerification,
		Risks:        facts.CapabilityRisks,
	}
	if len(facts.CapabilityNextActions) > 0 {
		loop.NextAction = facts.CapabilityNextActions[0]
	}
	if len(facts.CapabilityResolved) > 0 {
		loop.ResolvedFollowUp = facts.CapabilityResolved[0]
	}
	if len(facts.CapabilitySupersedes) > 0 {
		loop.SupersedesEvidenceID = facts.CapabilitySupersedes[0]
		if len(facts.CapabilitySupersedes) > 1 {
			loop.SupersedesEvidenceIDs = facts.CapabilitySupersedes[1:]
		}
	}
	if !capabilityloop.HasHint(loop) {
		return
	}
	artifacts := capabilityloop.ArtifactHintsFromCompactFacts(facts.CapabilityArtifacts)
	if len(facts.CapabilityFollowUpIDs) > 0 && len(facts.CapabilityResolved) > 0 && len(facts.CapabilitySupersedes) > 0 {
		pendingLoop := capabilityloop.HintData{
			Unknowns:     facts.CapabilityUnknowns,
			Verification: facts.CapabilityVerification,
			Risks:        facts.CapabilityRisks,
		}
		if len(facts.CapabilityNextActions) > 0 {
			pendingLoop.NextAction = facts.CapabilityNextActions[0]
		}
		if capabilityloop.HasHint(pendingLoop) {
			pending := capabilityloop.CompactSummaryDecisionContext(artifacts, pendingLoop)
			pending.ToolUseID = capabilityloop.ToolUseIDFromFollowUpID(facts.CapabilityFollowUpIDs[0])
			pending.Description = "Recovered pending capability follow-up from compact summary"
			s.rememberRecentAgentEvidence(pending)
		}
		resolutionLoop := capabilityloop.HintData{
			Evidence:             facts.CapabilityEvidence,
			Verification:         facts.CapabilityVerification,
			ResolvedFollowUp:     facts.CapabilityResolved[0],
			SupersedesEvidenceID: facts.CapabilitySupersedes[0],
		}
		if len(facts.CapabilitySupersedes) > 1 {
			resolutionLoop.SupersedesEvidenceIDs = facts.CapabilitySupersedes[1:]
		}
		resolution := capabilityloop.CompactSummaryDecisionContext(artifacts, resolutionLoop)
		resolution.ToolUseID = "compact-summary-capability-resolution"
		resolution.Description = "Recovered capability follow-up resolution from compact summary"
		s.rememberRecentAgentEvidence(resolution)
		return
	}
	s.rememberRecentAgentEvidence(capabilityloop.CompactSummaryDecisionContext(artifacts, loop))
}

func (s *Session) rememberCapabilityLoopToolResult(toolName, toolUseID string, input json.RawMessage, content string) {
	toolName = strings.TrimSpace(toolName)
	if !capabilityloop.IsSubAgentResultTool(toolName) || strings.TrimSpace(content) == "" {
		return
	}
	loop, artifacts, ok := capabilityloop.FromToolResult(content)
	if !ok {
		return
	}
	s.rememberRecentAgentEvidence(capabilityloop.DecisionContext{
		ToolUseID:      strings.TrimSpace(toolUseID),
		ToolName:       toolName,
		EvidenceSource: capabilityloop.SubAgentToolEvidenceSource(toolName),
		AgentName:      toolName,
		Description:    capabilityloop.DescriptionFromToolInput(input),
		Status:         firstNonEmpty(capabilityloop.StatusFromToolResult(content), "completed"),
		SessionID:      artifacts.SessionID,
		TranscriptPath: artifacts.TranscriptPath,
		OutputFile:     artifacts.OutputFile,
		WorktreePath:   artifacts.WorktreePath,
		WorktreeBranch: artifacts.WorktreeBranch,
		Loop:           loop,
	})
}

func (s *Session) rememberRecentAgentEvidence(item capabilityloop.DecisionContext) {
	if (item.TaskID == 0 && strings.TrimSpace(item.ToolUseID) == "") || !capabilityloop.HasHint(item.Loop) {
		return
	}
	for i, existing := range s.recentAgentEvidence {
		if capabilityloop.SameDecisionContext(existing, item) {
			s.recentAgentEvidence[i] = item
			return
		}
	}
	s.recentAgentEvidence = append(s.recentAgentEvidence, item)
	if len(s.recentAgentEvidence) > 3 {
		s.recentAgentEvidence = append([]capabilityloop.DecisionContext(nil), s.recentAgentEvidence[len(s.recentAgentEvidence)-3:]...)
	}
}

func (s *Session) agentTaskNotificationAcknowledged(taskID uint64, status string) bool {
	if taskID == 0 || !capabilityloop.TaskStatusIsTerminal(status) {
		return false
	}
	return s.acknowledgedAgentTasks[taskID]
}

func (s *Session) runtimeRecentAgentEvidenceStatus() string {
	if len(s.recentAgentEvidence) == 0 {
		return ""
	}
	lines := []string{
		"## Recent agent evidence decision context",
		"- These Task, Agent, or AgentGet capability_loop fields are request-only parent decision inputs after sub-agent results are observed.",
		"- Cite evidence, resolve or disclose unknowns, run or mention verification, account for risks, and follow next_action unless user intent or newer evidence overrides it.",
	}
	start := len(s.recentAgentEvidence) - 1
	for i := start; i >= 0 && start-i < 3; i-- {
		item := s.recentAgentEvidence[i]
		loop := capabilityloop.TaskHint(item.Loop)
		if loop == "" {
			continue
		}
		name := trimRuntimeStatusText(item.AgentName, 48)
		if name == "" {
			name = "agent"
		}
		status := trimRuntimeStatusText(item.Status, 32)
		if status == "" {
			status = "unknown"
		}
		description := trimRuntimeStatusText(item.Description, 120)
		var prefix string
		if item.TaskID > 0 {
			prefix = fmt.Sprintf("- #%d %s %s", item.TaskID, name, status)
		} else {
			toolName := trimRuntimeStatusText(firstNonEmpty(item.ToolName, "sub-agent"), 48)
			prefix = fmt.Sprintf("- %s result %s", toolName, status)
		}
		if description != "" {
			prefix += ": " + description
		}
		var contextParts []string
		if source := capabilityloop.SourceHint(item); source != "" {
			contextParts = append(contextParts, source)
		}
		contextParts = append(contextParts, loop)
		if artifacts := capabilityloop.ArtifactHint(item); artifacts != "" {
			contextParts = append(contextParts, artifacts)
		}
		lines = append(lines, prefix+"; "+strings.Join(contextParts, "; "))
	}
	if len(lines) == 3 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// runtimeAgentEvidenceFollowUpReminderStatus renders a runtime-status section, not
// a gate. It was named ...GateStatus, which reads as enforcement it does not
// provide: the text asks the model not to finalize until each sub-agent
// next_action is resolved, but nothing checks that it did. The closure gate is
// unaware of sub-agents entirely — internal/query/closure_gate.go does not import
// capabilityloop, and the evidence collectors whitelist Read/Grep/Glob/LS/Bash, so
// a Task trace counts as no evidence at all.
//
// Two consequences, and the plan for both, are written up in
// docs/architecture/subagent_closure_gate_design.md.
func (s *Session) runtimeAgentEvidenceFollowUpReminderStatus() string {
	if len(s.recentAgentEvidence) == 0 {
		return ""
	}
	lines := []string{
		"## Agent capability follow-up gate",
		"- Do not finalize until each listed sub-agent next_action is executed, superseded by newer evidence or user intent, or explicitly carried into residual risk.",
		"- If verification is listed, either run it, cite an equivalent verification already run, or say why it remains unverified before the final answer.",
	}
	resolved := capabilityloop.ResolvedFollowUps(s.recentAgentEvidence)
	pendingLines := 0
	for i := len(s.recentAgentEvidence) - 1; i >= 0 && pendingLines < 3; i-- {
		item := s.recentAgentEvidence[i]
		if resolvedIndex, ok := resolved[capabilityloop.FollowUpID(item)]; ok && resolvedIndex > i {
			continue
		}
		line := capabilityloop.FollowUpLine(item)
		if line == "" {
			continue
		}
		lines = append(lines, line)
		pendingLines++
	}
	if len(lines) == 3 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func trimRuntimeStatusText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func (s *Session) contextManifest(systemBlocks []anthropic.SystemBlock, assembly contextAssembly, skillsManifest SkillsCatalogManifest, tenantSkillInlineManifest TenantSkillInlineManifest) ContextManifest {
	profile := s.promptProfile()
	manifest := ContextManifest{
		Mode:                         s.promptMode(),
		Profile:                      profile.String(),
		RuntimeProfile:               s.runtimeProfile().String(),
		AgentProfileKey:              s.options.AgentProfileKey,
		AgentProfileVersion:          s.options.AgentProfileVersion,
		AgentProfileSource:           s.options.AgentProfileSource,
		AgentProfileRequestedHash:    s.options.AgentProfileRequestedHash,
		AgentProfileEffectiveHash:    s.options.AgentProfileEffectiveHash,
		AgentProfileBlockedOverrides: s.options.AgentProfileBlockedOverrides,
		PromptProfile:                s.promptProfileVariant(),
		QuerySource:                  s.querySource(),
		SystemBlocks:                 len(systemBlocks),
		UserMessages:                 len(assembly.userMessages),
		InitialMessages:              len(s.options.InitialMessages),
		Attachments:                  len(s.options.Attachments),
		CustomSystem:                 strings.TrimSpace(s.options.SystemPrompt) != "",
		OverrideSystem:               strings.TrimSpace(s.options.OverrideSystemPrompt) != "",
		CoordinatorSystem:            strings.TrimSpace(s.options.CoordinatorPrompt) != "",
		MainThreadSystem:             strings.TrimSpace(s.options.MainThreadAgentPrompt) != "",
		SystemAddendum:               strings.TrimSpace(s.options.SystemAddendum) != "",
		SkillsCatalog:                skillsManifest,
		TenantSkillInline:            tenantSkillInlineManifest,
		TenantContext:                s.tenantContextManifest(profile),
	}
	manifest.TenantRuntime = tenantRuntimeManifest(manifest.TenantSkillInline, manifest.TenantContext)
	if profile.IsCode() {
		manifest.CodeContext = s.codeContextManifest(assembly.codeDocs, assembly.codePromptReport, systemBlocks)
	}
	manifest.Sources = contextManifestSources(manifest)
	return manifest
}

func tenantRuntimeManifest(inline TenantSkillInlineManifest, tenantContext TenantContextManifest) TenantRuntimeManifest {
	runtime := TenantRuntimeManifest{
		Active:         inline.Active || tenantContext.Resolved,
		Resolved:       inline.Active || tenantContext.Resolved,
		Source:         inline.Selector,
		SkillKeys:      append([]string(nil), inline.SkillKeys...),
		LoadedKeys:     append([]string(nil), inline.LoadedKeys...),
		Versions:       append([]string(nil), inline.Versions...),
		PackageSHA256:  append([]string(nil), inline.PackageSHA256...),
		PackageRefs:    append([]string(nil), inline.PackageRefs...),
		RuntimeRefs:    append([]string(nil), inline.RuntimeRefs...),
		Bytes:          inline.Bytes,
		TenantAddendum: tenantContext.Active,
	}
	return runtime
}

func (s *Session) tenantContextManifest(profile promptmode.Mode) TenantContextManifest {
	manifest := s.options.TenantContextManifest
	if strings.TrimSpace(s.options.SystemAddendum) != "" {
		manifest.Active = true
		manifest.Addendum = true
	}
	if !profile.IsChat() && !manifest.ManagedMemory && !manifest.TeamMemory {
		return manifest
	}
	return manifest
}

func (s *Session) codeContextManifest(docs []memory.Document, report memory.PromptDocumentsReport, systemBlocks []anthropic.SystemBlock) CodeContextManifest {
	byType := make(map[string]int)
	byTypeBytes := make(map[string]int)
	documentBytes := 0
	workflowRules := 0
	workflowBytes := 0
	includes := 0
	pathScoped := 0
	excludeScoped := 0
	documentSummary := make([]CodeContextDocumentManifest, 0, len(docs))
	for _, doc := range docs {
		typ := strings.TrimSpace(doc.Type)
		if typ == "" {
			typ = "Unknown"
		}
		contentBytes := len(strings.TrimSpace(doc.Content))
		documentBytes += contentBytes
		byType[typ]++
		byTypeBytes[typ] += contentBytes
		isWorkflow := memory.IsWorkflowDocument(doc)
		if isWorkflow {
			workflowRules++
			workflowBytes += contentBytes
		}
		if strings.TrimSpace(doc.Parent) != "" {
			includes++
		}
		if len(doc.Paths) > 0 {
			pathScoped++
		}
		if len(doc.Excludes) > 0 {
			excludeScoped++
		}
		documentSummary = append(documentSummary, CodeContextDocumentManifest{
			Path:          doc.Path,
			Type:          typ,
			Bytes:         contentBytes,
			PromptBytes:   promptBytesForDocument(report, doc),
			Budgeted:      budgetedDocument(report, doc),
			Parent:        doc.Parent,
			Workflow:      isWorkflow,
			PathScoped:    len(doc.Paths) > 0,
			ExcludeScoped: len(doc.Excludes) > 0,
		})
	}
	if len(byType) == 0 {
		byType = nil
	}
	if len(byTypeBytes) == 0 {
		byTypeBytes = nil
	}
	if len(documentSummary) == 0 {
		documentSummary = nil
	}
	sections := systemBlockTextSet(systemBlocks)
	features := s.activeFeatureSectionNames()
	return CodeContextManifest{
		Active:          true,
		Documents:       len(docs),
		DocumentBytes:   documentBytes,
		PromptBytes:     report.PromptBytes,
		ByType:          byType,
		ByTypeBytes:     byTypeBytes,
		DocumentSummary: documentSummary,
		WorkflowRules:   workflowRules,
		WorkflowBytes:   workflowBytes,
		BudgetedDocs:    report.BudgetedDocuments,
		WorkflowBudget:  report.WorkflowBudgetBytes,
		Includes:        includes,
		PathScoped:      pathScoped,
		ExcludeScoped:   excludeScoped,
		GitContext:      hasSystemSection(sections, "Current branch:") || hasSystemSection(sections, "Git Snapshot"),
		MCPInstructions: hasSystemSection(sections, "Configured MCP servers:"),
		Environment:     hasSystemSection(sections, "# Environment"),
		OutputStyle:     hasSystemSection(sections, "# Output Style:"),
		Language:        hasSystemSection(sections, "# Language"),
		Scratchpad:      hasSystemSection(sections, "# Scratchpad Directory"),
		AutoCompact:     s.AutoCompactEnabled(),
		PromptCache:     promptCachingEnabled(s.currentModel()),
		FeatureSections: features,
	}
}

func promptBytesForDocument(report memory.PromptDocumentsReport, doc memory.Document) int {
	for _, summary := range report.Documents {
		if summary.Path == doc.Path && strings.EqualFold(summary.Type, strings.TrimSpace(doc.Type)) {
			return summary.PromptBytes
		}
	}
	return len(strings.TrimSpace(doc.Content))
}

func budgetedDocument(report memory.PromptDocumentsReport, doc memory.Document) bool {
	for _, summary := range report.Documents {
		if summary.Path == doc.Path && strings.EqualFold(summary.Type, strings.TrimSpace(doc.Type)) {
			return summary.Budgeted
		}
	}
	return false
}

func (s *Session) activeFeatureSectionNames() []string {
	var out []string
	for _, item := range []struct {
		name    string
		enabled bool
	}{
		{name: "proactive", enabled: (s.featureEnabled("PROACTIVE") || s.featureEnabled("KAIROS")) && (isEnvTruthy("NIUMA_PROACTIVE_ACTIVE") || isEnvTruthy("CLAUDE_CODE_PROACTIVE_ACTIVE"))},
		{name: "enhanced_behavior_constraints", enabled: s.promptProfile().IsCode() && s.featureEnabled(featureEnhancedBehaviorConstraints)},
		{name: "enhanced_system_prompt", enabled: s.promptProfile().IsCode() && s.featureEnabled(featureEnhancedSystemPrompt)},
		{name: "workflow_closure", enabled: s.promptProfile().IsCode() && s.featureEnabled(featureEnhancedSystemPrompt)},
		{name: "numeric_length_anchors", enabled: strings.EqualFold(getenv("USER_TYPE"), "ant")},
		{name: "token_budget", enabled: s.featureEnabled("TOKEN_BUDGET")},
		{name: "brief", enabled: s.featureEnabled("KAIROS") || s.featureEnabled("KAIROS_BRIEF")},
	} {
		if item.enabled {
			out = append(out, item.name)
		}
	}
	sort.Strings(out)
	return out
}

func systemBlockTextSet(blocks []anthropic.SystemBlock) []string {
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func hasSystemSection(blocks []string, marker string) bool {
	for _, block := range blocks {
		if strings.Contains(block, marker) {
			return true
		}
	}
	return false
}

func contextManifestSources(manifest ContextManifest) []ContextManifestSource {
	sources := []ContextManifestSource{
		{Type: "system_blocks", Count: manifest.SystemBlocks, Active: manifest.SystemBlocks > 0},
		{Type: "initial_messages", Count: manifest.InitialMessages, Active: manifest.InitialMessages > 0},
		{Type: "user_context_messages", Count: manifest.UserMessages, Active: manifest.UserMessages > 0},
		{Type: "attachments", Count: manifest.Attachments, Active: manifest.Attachments > 0},
	}
	if manifest.CodeContext.Active {
		sources = append(sources,
			ContextManifestSource{Type: "code_memory", Count: manifest.CodeContext.Documents, Active: manifest.CodeContext.Documents > 0},
			ContextManifestSource{Type: "workflow_rules", Count: manifest.CodeContext.WorkflowRules, Active: manifest.CodeContext.WorkflowRules > 0},
			ContextManifestSource{Type: "git_context", Count: boolCount(manifest.CodeContext.GitContext), Active: manifest.CodeContext.GitContext},
			ContextManifestSource{Type: "mcp_instructions", Count: boolCount(manifest.CodeContext.MCPInstructions), Active: manifest.CodeContext.MCPInstructions},
		)
	}
	if manifest.SkillsCatalog.Active {
		sources = append(sources, ContextManifestSource{Type: "skills_catalog", Count: 1, Active: true})
	}
	if manifest.TenantSkillInline.Active {
		count := len(manifest.TenantSkillInline.LoadedKeys)
		if count == 0 {
			count = len(manifest.TenantSkillInline.SkillKeys)
		}
		sources = append(sources, ContextManifestSource{Type: "tenant_skill_inline", Count: count, Active: true})
	}
	if manifest.TenantRuntime.Active {
		count := len(manifest.TenantRuntime.LoadedKeys)
		if count == 0 {
			count = len(manifest.TenantRuntime.SkillKeys)
		}
		sources = append(sources, ContextManifestSource{Type: "tenant_runtime", Count: count, Active: true})
	}
	if manifest.TenantContext.Active {
		count := manifest.TenantContext.MemoryItems + manifest.TenantContext.Documents + manifest.TenantContext.KnowledgeChunks
		if manifest.TenantContext.Profile {
			count++
		}
		if count == 0 && manifest.TenantContext.Addendum {
			count = 1
		}
		sources = append(sources, ContextManifestSource{Type: "tenant_context", Count: count, Active: true})
	}
	return sources
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Session) recordContextManifest(ctx context.Context, messageID string, manifest ContextManifest) {
	telemetryManifest := redactedContextManifestForTelemetry(manifest)
	properties := map[string]any{
		"context_manifest": telemetryManifest,
		"mode":             telemetryManifest.Mode,
		"profile":          telemetryManifest.Profile,
		"prompt_profile":   telemetryManifest.PromptProfile,
		"query_source":     telemetryManifest.QuerySource,
		"system_blocks":    telemetryManifest.SystemBlocks,
		"user_messages":    telemetryManifest.UserMessages,
		"sources":          telemetryManifest.Sources,
	}
	if telemetryManifest.TenantSkillInline.Active {
		properties["tenant_skill_inline.active"] = true
		properties["tenant_skill_inline.selector"] = telemetryManifest.TenantSkillInline.Selector
		properties["tenant_skill_inline.skill_keys"] = strings.Join(telemetryManifest.TenantSkillInline.SkillKeys, ",")
		properties["tenant_skill_inline.loaded"] = telemetryManifest.TenantSkillInline.Loaded
		properties["tenant_skill_inline.loaded_keys"] = strings.Join(telemetryManifest.TenantSkillInline.LoadedKeys, ",")
		properties["tenant_skill_inline.versions"] = strings.Join(telemetryManifest.TenantSkillInline.Versions, ",")
		properties["tenant_skill_inline.package_sha256"] = strings.Join(telemetryManifest.TenantSkillInline.PackageSHA256, ",")
		properties["tenant_skill_inline.package_refs"] = strings.Join(telemetryManifest.TenantSkillInline.PackageRefs, ",")
		properties["tenant_skill_inline.runtime_refs"] = strings.Join(telemetryManifest.TenantSkillInline.RuntimeRefs, ",")
		properties["tenant_skill_inline.bytes"] = telemetryManifest.TenantSkillInline.Bytes
		if telemetryManifest.TenantSkillInline.Error != "" {
			properties["tenant_skill_inline.error"] = telemetryManifest.TenantSkillInline.Error
		}
	}
	if telemetryManifest.TenantRuntime.Active {
		properties["tenant_runtime.active"] = true
		properties["tenant_runtime.resolved"] = telemetryManifest.TenantRuntime.Resolved
		properties["tenant_runtime.source"] = telemetryManifest.TenantRuntime.Source
		properties["tenant_runtime.skill_keys"] = strings.Join(telemetryManifest.TenantRuntime.SkillKeys, ",")
		properties["tenant_runtime.loaded_keys"] = strings.Join(telemetryManifest.TenantRuntime.LoadedKeys, ",")
		properties["tenant_runtime.versions"] = strings.Join(telemetryManifest.TenantRuntime.Versions, ",")
		properties["tenant_runtime.package_sha256"] = strings.Join(telemetryManifest.TenantRuntime.PackageSHA256, ",")
		properties["tenant_runtime.package_refs"] = strings.Join(telemetryManifest.TenantRuntime.PackageRefs, ",")
		properties["tenant_runtime.runtime_refs"] = strings.Join(telemetryManifest.TenantRuntime.RuntimeRefs, ",")
		properties["tenant_runtime.bytes"] = telemetryManifest.TenantRuntime.Bytes
		properties["tenant_runtime.tenant_addendum"] = telemetryManifest.TenantRuntime.TenantAddendum
	}
	if telemetryManifest.TenantContext.Resolved {
		properties["tenant_context.resolved"] = true
	}
	if telemetryManifest.CodeContext.Active {
		properties["code_context.workflow_rules"] = telemetryManifest.CodeContext.WorkflowRules
		properties["code_context.feature_sections"] = strings.Join(telemetryManifest.CodeContext.FeatureSections, ",")
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:       "query.prompt_context",
		Category:   telemetry.CategorySession,
		Source:     "query.Session.run",
		Status:     telemetry.StatusOK,
		Model:      s.options.Model,
		SessionID:  s.options.TenantSessionID,
		Properties: properties,
	})
	if s.options.Recorder == nil {
		return
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return
	}
	_ = s.options.Recorder.Append(session.Entry{
		Type:     "prompt_context",
		Role:     "system",
		Content:  "Prompt context manifest",
		ToolID:   messageID,
		Metadata: data,
	})
}

func redactedContextManifestForTelemetry(manifest ContextManifest) ContextManifest {
	redacted := manifest
	if len(manifest.CodeContext.DocumentSummary) > 0 {
		redacted.CodeContext.DocumentSummary = make([]CodeContextDocumentManifest, 0, len(manifest.CodeContext.DocumentSummary))
		for _, doc := range manifest.CodeContext.DocumentSummary {
			doc.Path = ""
			doc.Parent = ""
			redacted.CodeContext.DocumentSummary = append(redacted.CodeContext.DocumentSummary, doc)
		}
	}
	return redacted
}

func (s *Session) resolveSystemPromptSections(sections []systemPromptSection) []string {
	if s.sectionCache == nil {
		s.sectionCache = map[string]*string{}
	}
	out := make([]string, 0, len(sections))
	for _, section := range sections {
		if section.name == "" || section.compute == nil {
			continue
		}
		if !section.cacheBreak {
			if cached, ok := s.sectionCache[section.name]; ok {
				if cached != nil && strings.TrimSpace(*cached) != "" {
					out = append(out, *cached)
				}
				continue
			}
		}
		value := strings.TrimSpace(section.compute())
		if value == "" {
			s.sectionCache[section.name] = nil
			continue
		}
		cachedValue := value
		s.sectionCache[section.name] = &cachedValue
		out = append(out, value)
	}
	return out
}

func (s *Session) enabledToolNames() []string {
	registry := s.effectiveRegistry()
	if registry == nil {
		return nil
	}
	defs := registry.Definitions()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.Name) != "" {
			names = append(names, strings.TrimSpace(def.Name))
		}
	}
	sort.Strings(names)
	return names
}

func (s *Session) featureEnabled(name string) bool {
	// Prompt sections are selected per session, so GrowthBook-style targeting must
	// see the same query source, mode, and model that will be sent to the API.
	return config.FeatureEnabled(s.options.CWD, name, config.FeatureContext{
		UserType:    getenv("USER_TYPE"),
		QuerySource: s.querySource(),
		Mode:        s.promptMode(),
		Model:       s.currentModel(),
	})
}

func (s *Session) promptMode() string {
	switch {
	case isEnvTruthy("CLAUDE_CODE_SIMPLE") || isEnvTruthy("NIUMA_SIMPLE"):
		return "simple"
	case s.promptProfile().IsChat():
		return "chat"
	case strings.TrimSpace(s.options.OverrideSystemPrompt) != "":
		return "override"
	case strings.TrimSpace(s.options.CoordinatorPrompt) != "" && strings.TrimSpace(s.options.MainThreadAgentPrompt) == "":
		return "coordinator"
	case strings.TrimSpace(s.options.MainThreadAgentPrompt) != "":
		return "main_thread_agent"
	case strings.TrimSpace(s.options.SystemPrompt) != "":
		return "custom"
	default:
		return defaultQuerySource
	}
}

func compactPromptParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == systemPromptDynamicBoundary {
			out = append(out, part)
			continue
		}
		if text := strings.TrimSpace(part); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func buildSystemPromptBlocks(parts []string, enablePromptCaching bool, querySource string) []anthropic.SystemBlock {
	split := splitSystemPromptPrefix(parts, false)
	blocks := make([]anthropic.SystemBlock, 0, len(split))
	for _, block := range split {
		text := strings.TrimSpace(block.text)
		if text == "" {
			continue
		}
		out := anthropic.SystemBlock{Type: blockTypeText, Text: text, Source: block.source}
		if enablePromptCaching && block.cacheScope != nil {
			out.CacheControl = cacheControlForScope(*block.cacheScope, querySource)
		}
		blocks = append(blocks, out)
	}
	return blocks
}

func splitSystemPromptPrefix(parts []string, skipGlobalCacheForSystemPrompt bool) []splitSystemBlock {
	useGlobalCache := shouldUseGlobalCacheScope()
	if useGlobalCache && skipGlobalCacheForSystemPrompt {
		return splitSystemPromptWithoutBoundary(parts, "org", true)
	}
	if useGlobalCache {
		boundary := -1
		for i, part := range parts {
			if part == systemPromptDynamicBoundary {
				boundary = i
				break
			}
		}
		if boundary >= 0 {
			var attributionHeader, systemPromptPrefix string
			var staticBlocks, dynamicBlocks []string
			for i, part := range parts {
				text := strings.TrimSpace(part)
				if text == "" || text == systemPromptDynamicBoundary {
					continue
				}
				switch {
				case strings.HasPrefix(text, "x-anthropic-billing-header"):
					attributionHeader = text
				case cliSystemPromptPrefixes[text]:
					systemPromptPrefix = text
				case i < boundary:
					staticBlocks = append(staticBlocks, text)
				default:
					dynamicBlocks = append(dynamicBlocks, text)
				}
			}
			var out []splitSystemBlock
			if attributionHeader != "" {
				out = append(out, splitSystemBlock{text: attributionHeader, source: "attribution"})
			}
			if systemPromptPrefix != "" {
				out = append(out, splitSystemBlock{text: systemPromptPrefix, source: "identity"})
			}
			if joined := strings.Join(staticBlocks, "\n\n"); joined != "" {
				scope := "global"
				out = append(out, splitSystemBlock{text: joined, cacheScope: &scope, source: "static_prompt"})
			}
			if joined := strings.Join(dynamicBlocks, "\n\n"); joined != "" {
				out = append(out, splitSystemBlock{text: joined, source: "dynamic_prompt"})
			}
			return out
		}
	}
	return splitSystemPromptWithoutBoundary(parts, "org", false)
}

func splitSystemPromptWithoutBoundary(parts []string, defaultScope string, skipBoundary bool) []splitSystemBlock {
	var attributionHeader, systemPromptPrefix string
	var rest []string
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" || (skipBoundary && text == systemPromptDynamicBoundary) {
			continue
		}
		if text == systemPromptDynamicBoundary {
			continue
		}
		switch {
		case strings.HasPrefix(text, "x-anthropic-billing-header"):
			attributionHeader = text
		case cliSystemPromptPrefixes[text]:
			systemPromptPrefix = text
		default:
			rest = append(rest, text)
		}
	}
	var out []splitSystemBlock
	if attributionHeader != "" {
		out = append(out, splitSystemBlock{text: attributionHeader, source: "attribution"})
	}
	if systemPromptPrefix != "" {
		scope := defaultScope
		out = append(out, splitSystemBlock{text: systemPromptPrefix, cacheScope: &scope, source: "identity"})
	}
	if joined := strings.Join(rest, "\n\n"); joined != "" {
		scope := defaultScope
		out = append(out, splitSystemBlock{text: joined, cacheScope: &scope, source: "static_prompt"})
	}
	return out
}

func cacheControlForScope(scope, querySource string) *anthropic.CacheControl {
	cache := &anthropic.CacheControl{Type: "ephemeral"}
	if shouldUsePromptCache1hTTL(querySource) {
		cache.TTL = "1h"
	}
	if scope == "global" {
		cache.Scope = "global"
	}
	return cache
}

const defaultQuerySource = "repl_main_thread"

// ComputerUse prompts include a trusted safety/targeting preamble before the
// user's short GUI request. Keep the classifier limit in user-visible Unicode
// characters and leave room for that bounded preamble.
const computerUseSimplePromptRuneLimit = 2000

func isSimpleComputerUsePrompt(prompt string) bool {
	prompt = strings.ToLower(strings.TrimSpace(prompt))
	// The classifier budget is expressed in user-visible characters, not UTF-8
	// bytes, so multilingual GUI requests receive the same fast-path budget.
	if prompt == "" || utf8.RuneCountInString(prompt) > computerUseSimplePromptRuneLimit || strings.Contains(prompt, "don't") {
		return false
	}
	normalized := strings.NewReplacer(
		",", " ", ".", " ", "!", " ", "?", " ", ":", " ", ";", " ",
		"/", " ", "_", " ", "-", " ", "(", " ", ")", " ", "`", " ",
	).Replace(prompt)
	words := make(map[string]struct{}, len(strings.Fields(normalized)))
	for _, word := range strings.Fields(normalized) {
		words[word] = struct{}{}
	}
	guiHints := []string{"computer use", "computeruse", "gui", "desktop", "应用", "窗口", "界面", "电脑", "workbuddy"}
	guiMentioned := false
	for _, hint := range guiHints {
		if strings.Contains(prompt, hint) {
			guiMentioned = true
			break
		}
	}
	if !guiMentioned {
		return false
	}
	for _, blocked := range []string{"code", "coding", "repo", "repository", "file", "function", "git", "bash", "shell", "implement", "debug"} {
		if _, ok := words[blocked]; ok && hasPositiveComputerUseBlockedTerm(prompt, blocked) {
			return false
		}
	}
	for _, blockedPhrase := range []string{"do not", "never", "without"} {
		if strings.Contains(normalized, blockedPhrase) {
			return false
		}
	}
	for _, action := range []string{"click", "type", "send", "launch", "open", "submit", "navigate", "message", "chat"} {
		if _, ok := words[action]; ok {
			return true
		}
	}
	for _, action := range []string{"输入", "点击", "发送", "打开", "新建", "启动", "查询", "询问", "消息", "会话"} {
		if strings.Contains(prompt, action) {
			return true
		}
	}
	return false
}

// hasPositiveComputerUseBlockedTerm keeps the fast-path classifier from
// rejecting a GUI request merely because its safety instructions mention a
// forbidden shell/code tool (for example, "不要使用 Bash"). A positive
// occurrence still blocks the fast path; the classifier is only an optimization
// and must remain conservative for actual coding requests.
func hasPositiveComputerUseBlockedTerm(prompt, term string) bool {
	for offset := 0; offset < len(prompt); {
		index := strings.Index(prompt[offset:], term)
		if index < 0 {
			return false
		}
		index += offset
		prefix := prompt[:index]
		lastBoundary := -1
		for _, boundary := range []string{".", "!", "?", ";", "。", "！", "？", "；", "\n"} {
			if boundaryIndex := strings.LastIndex(prefix, boundary); boundaryIndex > lastBoundary {
				lastBoundary = boundaryIndex
			}
		}
		clause := prefix[lastBoundary+1:]
		lastNegative := -1
		for _, marker := range []string{"do not", "don't", "never", "without", "禁止", "不要", "不得", "不能", "不可以", "请勿", "不使用", "不操作"} {
			if markerIndex := strings.LastIndex(clause, marker); markerIndex > lastNegative {
				lastNegative = markerIndex
			}
		}
		lastPositiveConnector := -1
		for _, connector := range []string{"but", "however", "except", "但是", "不过", "而是"} {
			if connectorIndex := strings.LastIndex(clause, connector); connectorIndex > lastPositiveConnector {
				lastPositiveConnector = connectorIndex
			}
		}
		if lastNegative < 0 || lastNegative < lastPositiveConnector {
			return true
		}
		offset = index + len(term)
	}
	return false
}

func (s *Session) querySource() string {
	return firstNonEmpty(s.options.QuerySource, defaultQuerySource)
}

func shouldUsePromptCache1hTTL(querySource string) bool {
	querySource = firstNonEmpty(querySource, defaultQuerySource)
	// 这里原来还有一条 ENABLE_PROMPT_CACHING_1H_BEDROCK + ANTHROPIC_PROVIDER=bedrock 的
	// 短路分支。niuma 只支持 anthropic / openai-compatible 两类 provider，
	// providerKindSupported 会直接拒掉 bedrock，ANTHROPIC_PROVIDER 也没有别的读取点，
	// 所以那是一条指向不存在 provider 的死分支，已删除（AUDIT-P1-09）。
	if !promptCache1hUserEligible() {
		return false
	}
	allowlist := promptCache1hAllowlist()
	if len(allowlist) == 0 {
		return false
	}
	for _, pattern := range allowlist {
		pattern = strings.TrimSpace(pattern)
		switch {
		case pattern == "":
			continue
		case pattern == "*":
			return true
		case strings.HasSuffix(pattern, "*"):
			if strings.HasPrefix(querySource, strings.TrimSuffix(pattern, "*")) {
				return true
			}
		case querySource == pattern:
			return true
		}
	}
	return false
}

func promptCache1hUserEligible() bool {
	if isEnvTruthy("NIUMA_PROMPT_CACHE_1H") || isEnvTruthy("CLAUDE_CODE_PROMPT_CACHE_1H") {
		return true
	}
	if strings.EqualFold(getenv("USER_TYPE"), "ant") {
		return true
	}
	if isEnvTruthy("CLAUDE_AI_SUBSCRIBER") || isEnvTruthy("NIUMA_SUBSCRIBER") {
		return !isEnvTruthy("CLAUDE_AI_OVERAGE") && !isEnvTruthy("NIUMA_OVERAGE")
	}
	return false
}

func promptCache1hAllowlist() []string {
	raw := firstNonEmpty(
		getenv("NIUMA_PROMPT_CACHE_1H_ALLOWLIST"),
		getenv("CLAUDE_CODE_PROMPT_CACHE_1H_ALLOWLIST"),
		getenv("TENGU_PROMPT_CACHE_1H_ALLOWLIST"),
	)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []string
	if strings.HasPrefix(strings.TrimSpace(raw), "[") {
		if err := json.Unmarshal([]byte(raw), &values); err == nil {
			return values
		}
	}
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\t'
	})
}

func shouldUseGlobalCacheScope() bool {
	return !isEnvTruthy("NIUMA_DISABLE_GLOBAL_CACHE_SCOPE") && !isEnvTruthy("CLAUDE_CODE_DISABLE_GLOBAL_CACHE_SCOPE")
}

func promptCachingEnabled(model string) bool {
	if isEnvTruthy("DISABLE_PROMPT_CACHING") || isEnvTruthy("NIUMA_DISABLE_PROMPT_CACHING") {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower != "" && isEnvTruthy("DISABLE_PROMPT_CACHING_HAIKU") && strings.Contains(lower, "haiku") {
		return false
	}
	if lower != "" && isEnvTruthy("DISABLE_PROMPT_CACHING_SONNET") && strings.Contains(lower, "sonnet") {
		return false
	}
	if lower != "" && isEnvTruthy("DISABLE_PROMPT_CACHING_OPUS") && strings.Contains(lower, "opus") {
		return false
	}
	return true
}

func isEnvTruthy(key string) bool {
	switch strings.ToLower(strings.TrimSpace(product.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func applyCacheControl(block *anthropic.SystemBlock, ttl string) {
	block.Type = blockTypeText
	block.CacheControl = &anthropic.CacheControl{Type: "ephemeral", TTL: ttl}
}

func joinSystemBlocks(blocks []anthropic.SystemBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func appendSystemText(system, text string) string {
	if strings.TrimSpace(text) == "" {
		return system
	}
	if strings.TrimSpace(system) == "" {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(system) + "\n\n" + strings.TrimSpace(text)
}

func appendSystemBlock(blocks []anthropic.SystemBlock, text string) []anthropic.SystemBlock {
	return appendSystemBlockWithSource(blocks, text, "")
}

func appendSystemBlockWithSource(blocks []anthropic.SystemBlock, text, source string) []anthropic.SystemBlock {
	if len(blocks) == 0 || strings.TrimSpace(text) == "" {
		return blocks
	}
	blocks = append(blocks, anthropic.SystemBlock{Type: blockTypeText, Text: strings.TrimSpace(text), Source: source})
	return blocks
}

func maybeMoveSkillsCatalogIntoStablePrefix(blocks []anthropic.SystemBlock) []anthropic.SystemBlock {
	if !isEnvTruthy("NIUMA_STABLE_PREFIX_SKILLS") {
		return blocks
	}
	skillsIndex := -1
	dynamicIndex := -1
	for i, block := range blocks {
		source := strings.TrimSpace(block.Source)
		if source == "dynamic_prompt" && dynamicIndex < 0 {
			dynamicIndex = i
		}
		if source == "skills_catalog" {
			skillsIndex = i
		}
	}
	if skillsIndex < 0 || dynamicIndex < 0 || skillsIndex < dynamicIndex {
		return blocks
	}
	out := append([]anthropic.SystemBlock(nil), blocks...)
	skillBlock := out[skillsIndex]
	out = append(out[:skillsIndex], out[skillsIndex+1:]...)
	out = append(out[:dynamicIndex], append([]anthropic.SystemBlock{skillBlock}, out[dynamicIndex:]...)...)
	return out
}

func appendCompatibleMemoryToSystemBlocks(blocks []anthropic.SystemBlock, text string) []anthropic.SystemBlock {
	text = strings.TrimSpace(text)
	if text == "" {
		return blocks
	}
	if len(blocks) == 0 {
		return []anthropic.SystemBlock{{Type: blockTypeText, Text: text, Source: "auto_memory"}}
	}
	out := append([]anthropic.SystemBlock(nil), blocks...)
	last := &out[len(out)-1]
	last.Text = appendSystemText(last.Text, text)
	if strings.TrimSpace(last.Source) == "" {
		last.Source = "auto_memory"
	} else if !strings.Contains(last.Source, "auto_memory") {
		last.Source += "+auto_memory"
	}
	return out
}

func systemBlocksContainText(blocks []anthropic.SystemBlock, text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, block := range blocks {
		if strings.Contains(block.Text, text) {
			return true
		}
	}
	return false
}

func getenv(key string) string {
	return strings.TrimSpace(product.Getenv(key))
}
