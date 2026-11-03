package agentprofile

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type RuntimePolicy struct {
	AllowedTools       StringSet
	DeniedTools        StringSet
	AllowedSkills      StringSet
	AllowedMCPServers  StringSet
	MaxTurns           int
	MaxTokens          int
	MaxParallelWorkers int
	AllowWorkspace     bool
	AllowGit           bool
}

type ProfileOverrides struct {
	Language    string
	OutputStyle string
	Effort      string
	MaxTurns    *int
	MaxTokens   *int
}

type ResolveRequest struct {
	TenantID       uint64
	UserID         uint64
	Surface        string
	ProfileKey     string
	ProfileVersion uint
	Overrides      ProfileOverrides
}

type BlockedOverride struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type EffectiveProfile struct {
	Profile          Profile           `json:"profile"`
	Requested        ProfileDocument   `json:"requested"`
	Config           ProfileDocument   `json:"config"`
	Source           string            `json:"source"`
	ProfileKey       string            `json:"profile_key"`
	ProfileVersion   uint              `json:"profile_version"`
	RequestedHash    string            `json:"requested_hash"`
	EffectiveHash    string            `json:"effective_hash"`
	BlockedOverrides []BlockedOverride `json:"blocked_overrides,omitempty"`
}

func (e EffectiveProfile) HasBlocked(field string) bool {
	for _, blocked := range e.BlockedOverrides {
		if blocked.Field == field {
			return true
		}
	}
	return false
}

type Resolver struct {
	catalog Catalog
	policy  RuntimePolicy
}

func NewResolver(catalog Catalog, policy RuntimePolicy) Resolver {
	return Resolver{catalog: catalog, policy: policy}
}

func (r Resolver) Resolve(ctx context.Context, request ResolveRequest) (EffectiveProfile, error) {
	profile, source, err := r.catalog.Find(ctx, request.TenantID, request.UserID, request.Surface, request.ProfileKey, request.ProfileVersion)
	if err != nil {
		return EffectiveProfile{}, err
	}
	report := Validate(profile.Config, ValidationContext{})
	if !report.Valid {
		return EffectiveProfile{}, fmt.Errorf("%w: %s", ErrInvalidProfile, report.Issues[0].Message)
	}

	requested := profile.Config.Normalize()
	requested = applyOverrides(requested, request.Overrides)
	requestedHash, err := requested.CanonicalHash()
	if err != nil {
		return EffectiveProfile{}, fmt.Errorf("hash requested profile: %w", err)
	}
	effective := requested
	blocked := make([]BlockedOverride, 0)
	blocked = append(blocked, intersectTools(&effective, r.policy)...)
	blocked = append(blocked, intersectSkills(&effective, r.policy)...)
	blocked = append(blocked, intersectMCP(&effective, r.policy)...)
	blocked = append(blocked, restrictContext(&effective, r.policy)...)
	blocked = append(blocked, restrictBudgets(&effective, r.policy)...)

	if report := Validate(effective, ValidationContext{}); !report.Valid {
		return EffectiveProfile{}, fmt.Errorf("%w after policy application: %s", ErrInvalidProfile, report.Issues[0].Message)
	}
	effectiveHash, err := effective.CanonicalHash()
	if err != nil {
		return EffectiveProfile{}, fmt.Errorf("hash effective profile: %w", err)
	}
	return EffectiveProfile{
		Profile:          profile,
		Requested:        requested,
		Config:           effective,
		Source:           source,
		ProfileKey:       profile.Key,
		ProfileVersion:   profile.Version,
		RequestedHash:    requestedHash,
		EffectiveHash:    effectiveHash,
		BlockedOverrides: blocked,
	}, nil
}

func applyOverrides(document ProfileDocument, overrides ProfileOverrides) ProfileDocument {
	if value := strings.TrimSpace(overrides.Language); value != "" {
		document.Prompt.Language = value
	}
	if value := strings.TrimSpace(overrides.OutputStyle); value != "" {
		document.Prompt.OutputStyle = value
	}
	if value := strings.TrimSpace(overrides.Effort); value != "" {
		document.Execution.Effort = value
	}
	if overrides.MaxTurns != nil && *overrides.MaxTurns > 0 {
		document.Execution.MaxTurns = *overrides.MaxTurns
	}
	if overrides.MaxTokens != nil && *overrides.MaxTokens > 0 {
		document.Execution.MaxTokens = *overrides.MaxTokens
	}
	return document.Normalize()
}

func intersectTools(document *ProfileDocument, policy RuntimePolicy) []BlockedOverride {
	if policy.AllowedTools == nil && policy.DeniedTools == nil {
		return nil
	}
	allowed := make([]string, 0, len(document.Capabilities.Tools.Allow))
	blocked := make([]BlockedOverride, 0)
	for _, tool := range document.Capabilities.Tools.Allow {
		if policy.AllowedTools != nil && !contains(policy.AllowedTools, tool) {
			blocked = append(blocked, BlockedOverride{Field: "capabilities.tools.allow." + tool, Reason: "server_or_tenant_allowlist"})
			continue
		}
		if policy.DeniedTools != nil && contains(policy.DeniedTools, tool) {
			blocked = append(blocked, BlockedOverride{Field: "capabilities.tools.allow." + tool, Reason: "server_or_tenant_denylist"})
			continue
		}
		allowed = append(allowed, tool)
	}
	document.Capabilities.Tools.Allow = sortedUnique(allowed)
	deny := append([]string(nil), document.Capabilities.Tools.Deny...)
	for tool := range policy.DeniedTools {
		deny = append(deny, tool)
	}
	document.Capabilities.Tools.Deny = sortedUnique(deny)
	return blocked
}

func intersectSkills(document *ProfileDocument, policy RuntimePolicy) []BlockedOverride {
	if policy.AllowedSkills == nil {
		return nil
	}
	result := make([]string, 0, len(document.Capabilities.Skills))
	var blocked []BlockedOverride
	for _, skill := range document.Capabilities.Skills {
		if !contains(policy.AllowedSkills, skill) {
			blocked = append(blocked, BlockedOverride{Field: "capabilities.skills." + skill, Reason: "server_or_tenant_allowlist"})
			continue
		}
		result = append(result, skill)
	}
	document.Capabilities.Skills = sortedUnique(result)
	return blocked
}

func intersectMCP(document *ProfileDocument, policy RuntimePolicy) []BlockedOverride {
	if policy.AllowedMCPServers == nil {
		return nil
	}
	result := make([]string, 0, len(document.Capabilities.MCPServers))
	var blocked []BlockedOverride
	for _, server := range document.Capabilities.MCPServers {
		if !contains(policy.AllowedMCPServers, server) {
			blocked = append(blocked, BlockedOverride{Field: "capabilities.mcp_servers." + server, Reason: "server_or_tenant_allowlist"})
			continue
		}
		result = append(result, server)
	}
	document.Capabilities.MCPServers = sortedUnique(result)
	return blocked
}

func restrictContext(document *ProfileDocument, policy RuntimePolicy) []BlockedOverride {
	var blocked []BlockedOverride
	if document.Context.Workspace && !policy.AllowWorkspace {
		document.Context.Workspace = false
		blocked = append(blocked, BlockedOverride{Field: "context.workspace", Reason: "workspace_context_disabled"})
	}
	if document.Context.Git && !policy.AllowGit {
		document.Context.Git = false
		blocked = append(blocked, BlockedOverride{Field: "context.git", Reason: "git_context_disabled"})
	}
	return blocked
}

func restrictBudgets(document *ProfileDocument, policy RuntimePolicy) []BlockedOverride {
	var blocked []BlockedOverride
	if policy.MaxTurns > 0 && document.Execution.MaxTurns > policy.MaxTurns {
		document.Execution.MaxTurns = policy.MaxTurns
		blocked = append(blocked, BlockedOverride{Field: "execution.max_turns", Reason: "ceiling"})
	}
	if policy.MaxTokens > 0 && document.Execution.MaxTokens > policy.MaxTokens {
		document.Execution.MaxTokens = policy.MaxTokens
		blocked = append(blocked, BlockedOverride{Field: "execution.max_tokens", Reason: "ceiling"})
	}
	if policy.MaxParallelWorkers > 0 && document.Execution.MaxParallelReadOnlyTools > policy.MaxParallelWorkers {
		document.Execution.MaxParallelReadOnlyTools = policy.MaxParallelWorkers
		blocked = append(blocked, BlockedOverride{Field: "execution.max_parallel_read_only_tools", Reason: "ceiling"})
	}
	return blocked
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok || strings.TrimSpace(value) == "" {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
