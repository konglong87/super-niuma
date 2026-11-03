package tools

import (
	"context"
	"encoding/json"
	"os"

	"github.com/konglong87/super-niuma/internal/agentbudget"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/files"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/repair"
	"github.com/konglong87/super-niuma/internal/skills"
	"github.com/konglong87/super-niuma/internal/toolresult"
)

type Context struct {
	CWD                   string
	WritableRoots         []string
	Sandbox               SandboxConfig
	PermissionAudit       func(PermissionAudit)
	PermissionPrompt      func(context.Context, PermissionPromptRequest) PermissionPromptResponse
	PermissionUpdate      func(PermissionUpdate) error
	RuntimePermissionMode func() string
	UserQuestion          func(context.Context, UserQuestionRequest) UserQuestionResponse
	SessionAllow          []string
	SessionDeny           []string
	FileChange            func(FileChange)
	TaskProgress          func(agenttasks.EventInput)
	TaskController        *agenttasks.Controller
	AgentMessages         func(uint64) []agenttasks.MessageInput
	ActiveSkill           *SkillRuntime
	TaskStore             agenttasks.Store
	TenantID              uint64
	UserID                uint64
	SessionID             uint64
	TraceID               string
	// Invocation is immutable metadata owned by the query runtime for this one
	// tool call. Tools must not derive it from model-controlled input.
	Invocation    Invocation
	AgentPolicy   *AgentPolicy
	SkillProvider SkillProvider
	// SubagentDepth is how many sub-agent frames deep the current tool call is.
	// The parent/main conversation runs at 0; each sub-agent run increments it
	// for the tools it executes. agentruntime enforces the ceiling — see
	// AUDIT-P0-14: without this counter a sub-agent that still holds Task /
	// AgentCreate can nest forever.
	SubagentDepth int
	// AgentBudget is the cumulative token/cost ceiling shared by every
	// sub-agent reachable from this context. nil means unlimited.
	AgentBudget *agentbudget.Budget
	// SharedStateAuthorization is derived from the current parent user turn.
	// Delegated prompts inherit it but cannot expand it.
	SharedStateAuthorization gitpolicy.Authorization
	// ImageGenerator is the tenant-scoped image service exposed to image tools.
	// The runtime owns construction and credentials; tools only receive this
	// capability and the tenant/user/session context above.
	ImageGenerator            imagegen.Generator
	ComputerUse               computeruse.Service
	ComputerUseImageSupported bool
	// ComputerUseFastPath skips redundant receipt image reads after successful
	// input; a fresh authoritative observation and screenshot are still required.
	ComputerUseFastPath bool
}

type Invocation struct {
	RunID     string
	ToolUseID string
	BatchID   string
}

type SkillProvider interface {
	ListTenantSkills(ctx context.Context, prompt string, limit int) ([]skills.Skill, error)
	GetTenantSkill(ctx context.Context, name string) (skills.Skill, bool, error)
}

type AgentPolicy struct {
	Name           string
	AllowedTools   []string
	DeniedTools    []string
	PermissionMode string
}

type SandboxConfig struct {
	Enabled                   bool
	FailIfUnavailable         bool
	AllowUnsandboxedCommands  bool
	AutoAllowBashIfSandboxed  bool
	EnabledPlatforms          []string
	ExcludedCommands          []string
	FilesystemAllowRead       []string
	FilesystemDenyRead        []string
	FilesystemAllowWrite      []string
	FilesystemDenyWrite       []string
	NetworkDisabled           bool
	NetworkAllowDomains       []string
	NetworkDenyDomains        []string
	NetworkProxyURL           string
	NetworkProxyMode          string
	NetworkProxyRequired      bool
	NetworkMITMCAFile         string
	NetworkMITMRequired       bool
	UnixSocketDeny            []string
	SeccompEnabled            bool
	SeccompMode               string
	AllowPty                  bool
	EnableWeakerNestedSandbox bool
}

type Result struct {
	Content         string
	IsError         bool
	Interaction     *InteractionRequest
	ContextMessages []anthropic.MessageParam
	Verification    *repair.VerificationResult
}

type InteractionRequest struct {
	ID               string
	Kind             string
	Question         string
	Choices          []string
	ToolUseID        string
	ToolName         string
	ToolInput        json.RawMessage
	AssistantMessage anthropic.MessageParam
}

type UserQuestionRequest struct {
	Question  string
	Choices   []string
	ToolUseID string
}

type UserQuestionResponse struct {
	Answered      bool
	Answer        string
	Error         string
	Pending       bool
	InteractionID string
}

type PermissionAudit struct {
	ToolName string
	Allowed  bool
	Reason   string
	Rule     string
	Request  string
	Source   string
}

type PermissionPromptRequest struct {
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"input,omitempty"`
	Reason   string          `json:"reason,omitempty"`
	Request  string          `json:"request,omitempty"`
	Rule     string          `json:"rule,omitempty"`
	Source   string          `json:"source,omitempty"`
	OneShot  bool            `json:"one_shot,omitempty"`
}

type PermissionPromptResponse struct {
	Allowed     bool            `json:"allowed"`
	Reason      string          `json:"reason,omitempty"`
	Destination string          `json:"destination,omitempty"`
	Rule        string          `json:"rule,omitempty"`
	Decision    string          `json:"decision,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type PermissionUpdate struct {
	ToolName    string          `json:"tool_name"`
	Input       json.RawMessage `json:"input,omitempty"`
	Request     string          `json:"request,omitempty"`
	Rule        string          `json:"rule,omitempty"`
	Decision    string          `json:"decision"`
	Destination string          `json:"destination"`
	Reason      string          `json:"reason,omitempty"`
}

type SkillRuntime struct {
	Name             string                          `json:"name"`
	Source           string                          `json:"source,omitempty"`
	Version          string                          `json:"version,omitempty"`
	Fallback         bool                            `json:"fallback,omitempty"`
	AllowedTools     []string                        `json:"allowed_tools,omitempty"`
	Model            string                          `json:"model,omitempty"`
	Context          string                          `json:"context,omitempty"`
	Paths            []string                        `json:"paths,omitempty"`
	Arguments        []string                        `json:"arguments,omitempty"`
	ArgumentHint     string                          `json:"argument_hint,omitempty"`
	UserInvocable    bool                            `json:"user_invocable"`
	Agent            string                          `json:"agent,omitempty"`
	Effort           string                          `json:"effort,omitempty"`
	Hooks            map[string][]config.HookCommand `json:"hooks,omitempty"`
	Directory        string                          `json:"directory,omitempty"`
	FilesystemBacked bool                            `json:"filesystem_backed,omitempty"`
}

type FileChange struct {
	Path               string `json:"path"`
	Before             string `json:"before"`
	BeforeExists       bool   `json:"before_exists"`
	After              string `json:"after"`
	AfterExists        bool   `json:"after_exists"`
	BeforeSnapshotPath string `json:"before_snapshot_path,omitempty"`
	AfterSnapshotPath  string `json:"after_snapshot_path,omitempty"`
	// BeforeSHA256 / AfterSHA256 record content hashes so a content-free
	// transcript (file bodies externalized to the snapshot store) stays
	// auditable: you can still tell what changed and compare versions without
	// the transcript carrying the file body. Empty for empty/inline content.
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256,omitempty"`
	// The mode fields carry swaggertype:"integer" because swag --parseDependency
	// otherwise expands os.FileMode into an enum built from the Go stdlib's own
	// doc comments, and docs/ then drifts against the annotations whenever the
	// toolchain rewords them. Kept off the fields themselves so swag does not
	// copy this note into the published spec.

	BeforeMode      os.FileMode `json:"before_mode,omitempty" swaggertype:"integer"`
	AfterMode       os.FileMode `json:"after_mode,omitempty" swaggertype:"integer"`
	BeforeModeKnown bool        `json:"before_mode_known,omitempty"`
	AfterModeKnown  bool        `json:"after_mode_known,omitempty"`
	ModeChanged     bool        `json:"mode_changed,omitempty"`
	// Object-type fields describe symlinks so rewind can restore the link
	// itself rather than following it. Regular files leave these empty.
	BeforeIsSymlink  bool   `json:"before_is_symlink,omitempty"`
	AfterIsSymlink   bool   `json:"after_is_symlink,omitempty"`
	BeforeLinkTarget string `json:"before_link_target,omitempty"`
	AfterLinkTarget  string `json:"after_link_target,omitempty"`
	// Directory object type. Restore recreates/removes directories rather than
	// treating them as regular files.
	BeforeIsDir bool `json:"before_is_dir,omitempty"`
	AfterIsDir  bool `json:"after_is_dir,omitempty"`
	// BeforeMetadata carries capability-aware filesystem metadata (mtime, owner,
	// xattr) restored best-effort after content. A nil pointer means no metadata
	// was captured; individual capabilities carry their own "known" flags.
	BeforeMetadata *files.Metadata `json:"before_metadata,omitempty"`
	// Source records which tool boundary captured the change (edit/write/bash)
	// for observability; it never affects restore.
	Source string `json:"source,omitempty"`
	// MessageID keys the change to the message/turn that produced it in a v2
	// message-graph transcript (aligning with Claude Code's messageId->backup
	// model). Empty for v1 linear transcripts. It is observability/interop only;
	// rewind derives its restore set from the message-graph chain structure.
	MessageID string `json:"message_id,omitempty"`
	// BeforeSuperseded marks a turn-level lite entry: an earlier file_change in
	// the same turn already holds the recoverable turn-start state for this path,
	// so this entry keeps only path + hashes for observability and carries no
	// restorable body. Rewind restores the turn-start state, so intermediate
	// versions need no snapshot.
	BeforeSuperseded bool `json:"before_superseded,omitempty"`
}

type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Run(ctx context.Context, input json.RawMessage, toolContext Context) Result
}

type ConcurrencyClass string

const (
	ConcurrencySerial   ConcurrencyClass = "serial"
	ConcurrencyReadOnly ConcurrencyClass = "read_only"
)

type ExecutionPolicy struct {
	Concurrency ConcurrencyClass
}

// ExecutionPolicyProvider is intentionally opt-in. Tools without an explicit
// policy remain serial, including Bash, Git, Skill and dynamically loaded MCP tools.
type ExecutionPolicyProvider interface {
	ExecutionPolicy() ExecutionPolicy
}

type ParallelSafetyChecker interface {
	ParallelSafe(input json.RawMessage, toolContext Context) bool
}

func ExecutionPolicyFor(tool Tool) ExecutionPolicy {
	if provider, ok := tool.(ExecutionPolicyProvider); ok {
		policy := provider.ExecutionPolicy()
		if policy.Concurrency == ConcurrencyReadOnly {
			return policy
		}
	}
	return ExecutionPolicy{Concurrency: ConcurrencySerial}
}

func (r *Registry) ParallelSafe(name string, input json.RawMessage, toolContext Context) bool {
	if r == nil {
		return false
	}
	tool, ok := r.Get(name)
	if !ok || ExecutionPolicyFor(tool).Concurrency != ConcurrencyReadOnly {
		return false
	}
	if checker, ok := tool.(ParallelSafetyChecker); ok {
		return checker.ParallelSafe(input, toolContext)
	}
	return true
}

type ResultSizeLimiter interface {
	MaxResultSizeChars() int
}

type ResultBudgetSkipper interface {
	SkipToolResultBudget() bool
}

// ClaudeCodeDefaultDeclaredMaxResultSizeChars mirrors the common
// maxResultSizeChars value used by Claude Code tools. EffectiveResultLimit
// still caps this at toolresult.DefaultLimit to match Claude Code's
// getPersistenceThreshold behavior.
const ClaudeCodeDefaultDeclaredMaxResultSizeChars = 100_000

func EffectiveResultLimit(tool Tool, configuredLimit int) int {
	if skipper, ok := tool.(ResultBudgetSkipper); ok && skipper.SkipToolResultBudget() {
		return 0
	}
	limit := configuredLimit
	if limiter, ok := tool.(ResultSizeLimiter); ok {
		toolLimit := limiter.MaxResultSizeChars()
		if toolLimit > toolresult.DefaultLimit {
			toolLimit = toolresult.DefaultLimit
		}
		if toolLimit > 0 && (limit <= 0 || toolLimit < limit) {
			limit = toolLimit
		}
	}
	return limit
}

type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry(items ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, item := range items {
		r.Register(item)
	}
	return r
}

func (r *Registry) Clone() *Registry {
	cloned := NewRegistry()
	if r == nil {
		return cloned
	}
	for _, tool := range r.List() {
		cloned.Register(tool)
	}
	return cloned
}

func (r *Registry) Register(tool Tool) {
	if _, exists := r.tools[tool.Name()]; !exists {
		r.order = append(r.order, tool.Name())
	}
	r.tools[tool.Name()] = tool
}

func (r *Registry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *Registry) List() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

func (r *Registry) ToolResultBudgetSkipNames() map[string]bool {
	if r == nil {
		return nil
	}
	out := map[string]bool{}
	for _, tool := range r.List() {
		if skipper, ok := tool.(ResultBudgetSkipper); ok && skipper.SkipToolResultBudget() {
			out[tool.Name()] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r *Registry) Filter(allowed []string) *Registry {
	if len(allowed) == 0 {
		return r
	}
	filtered := NewRegistry()
	for _, tool := range r.List() {
		if toolAllowed(allowed, tool.Name()) {
			filtered.Register(tool)
		}
	}
	return filtered
}

func (r *Registry) FilterPolicy(allowed, denied []string) *Registry {
	filtered := NewRegistry()
	for _, tool := range r.List() {
		name := tool.Name()
		if len(allowed) > 0 && !toolAllowed(allowed, name) {
			continue
		}
		if len(denied) > 0 && toolAllowed(denied, name) {
			continue
		}
		filtered.Register(tool)
	}
	return filtered
}

func (r *Registry) Definitions() []anthropic.ToolDefinition {
	defs := make([]anthropic.ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		defs = append(defs, anthropic.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.InputSchema(),
		})
	}
	return defs
}

func toolAllowed(allowed []string, name string) bool {
	return permissions.MatchAnyRule(allowed, name, json.RawMessage(`{}`))
}
