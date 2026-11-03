package agentprofile

import "time"

const (
	SchemaVersionV1         = 1
	SchemaVersionV2         = 2
	MaxDisplayNameBytes     = 128
	MaxDescriptionBytes     = 4096
	MaxPersonaBytes         = 16384
	MaxSystemAddendumBytes  = 16384
	MaxProfileJSONBytes     = 256 * 1024
	MaxProfileTokensDefault = 128000
	MaxProfileTurnsDefault  = 100
	MaxParallelWorkersLimit = 32
)

type Scope string

const (
	ScopeBuiltin      Scope = "builtin"
	ScopeTenantShared Scope = "tenant_shared"
	ScopeUserPrivate  Scope = "user_private"
)

type Status string

const (
	StatusDraft      Status = "draft"
	StatusValidating Status = "validating"
	StatusPublished  Status = "published"
	StatusArchived   Status = "archived"
)

type PromptMode string

const (
	PromptModeCode PromptMode = "code"
	PromptModeChat PromptMode = "chat"
)

type PermissionMode string

const (
	PermissionModeAsk    PermissionMode = "ask"
	PermissionModeDeny   PermissionMode = "deny"
	PermissionModeAllow  PermissionMode = "allow"
	PermissionModeBypass PermissionMode = "bypassPermissions"
)

type SandboxMode string

const (
	SandboxRequired SandboxMode = "required"
	SandboxOptional SandboxMode = "optional"
	SandboxDisabled SandboxMode = "disabled"
)

const (
	ProfileChatAssistant = "chat-assistant"
	ProfileCopywriter    = "copywriter"
	ProfileCoder         = "coder"
	// ProfileWebUIV2Orchestrator is a non-default capability boundary. Only
	// this builtin profile may receive the seven managed Session tools.
	ProfileWebUIV2Orchestrator = "webui-v2-orchestrator"
)

type Profile struct {
	ID            uint64          `json:"id,omitempty"`
	TenantID      uint64          `json:"tenant_id,omitempty"`
	OwnerUserID   uint64          `json:"owner_user_id,omitempty"`
	Key           string          `json:"profile_key"`
	Scope         Scope           `json:"scope"`
	DisplayName   string          `json:"display_name"`
	Description   string          `json:"description,omitempty"`
	Version       uint            `json:"profile_version"`
	Status        Status          `json:"status"`
	Config        ProfileDocument `json:"config"`
	RequestedHash string          `json:"requested_hash,omitempty"`
	EffectiveHash string          `json:"effective_hash,omitempty"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
}

type ProfileDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Identity      IdentityPolicy   `json:"identity"`
	Prompt        PromptPolicy     `json:"prompt"`
	Capabilities  CapabilityPolicy `json:"capabilities"`
	Execution     ExecutionPolicy  `json:"execution"`
	Context       ContextPolicy    `json:"context"`
	Safety        SafetyPolicy     `json:"safety"`
}

type IdentityPolicy struct {
	RoleType    string `json:"role_type,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type PromptPolicy struct {
	Mode           PromptMode `json:"mode"`
	Persona        string     `json:"persona,omitempty"`
	OutputStyle    string     `json:"output_style,omitempty"`
	Language       string     `json:"language,omitempty"`
	SystemAddendum string     `json:"system_addendum,omitempty"`
}

type CapabilityPolicy struct {
	Tools            ToolPolicy `json:"tools"`
	Skills           []string   `json:"skills,omitempty"`
	MCPServers       []string   `json:"mcp_servers,omitempty"`
	AllowAgents      bool       `json:"allow_agents,omitempty"`
	AllowAttachments bool       `json:"allow_attachments,omitempty"`
}

type ToolPolicy struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

type ExecutionPolicy struct {
	Kind                     ExecutionKind `json:"kind,omitempty"`
	Model                    string        `json:"model,omitempty"`
	Provider                 string        `json:"provider,omitempty"`
	RuntimeProfile           string        `json:"runtime_profile,omitempty"`
	Effort                   string        `json:"effort,omitempty"`
	MaxTurns                 int           `json:"max_turns,omitempty"`
	MaxTokens                int           `json:"max_tokens,omitempty"`
	MaxParallelReadOnlyTools int           `json:"max_parallel_read_only_tools,omitempty"`
	AutoCompact              bool          `json:"auto_compact,omitempty"`
}

type ContextPolicy struct {
	Workspace      bool `json:"workspace,omitempty"`
	Git            bool `json:"git,omitempty"`
	TenantMemory   bool `json:"tenant_memory,omitempty"`
	UserMemory     bool `json:"user_memory,omitempty"`
	KnowledgeBase  bool `json:"knowledge_base,omitempty"`
	SessionHistory bool `json:"session_history,omitempty"`
}

type SafetyPolicy struct {
	PermissionMode           PermissionMode `json:"permission_mode"`
	Sandbox                  SandboxMode    `json:"sandbox"`
	AllowUnsandboxedCommands bool           `json:"allow_unsandboxed_commands,omitempty"`
}

type StringSet map[string]struct{}

type ValidationContext struct {
	AllowedTools       StringSet
	AllowedSkills      StringSet
	AllowedMCPServers  StringSet
	MaxTurns           int
	MaxTokens          int
	MaxParallelWorkers int
}

type ValidationIssue struct {
	Code     string `json:"code"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

func (r ValidationReport) Has(code string) bool {
	for _, issue := range r.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
