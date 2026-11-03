package agentprofile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	IssueSchemaVersion        = "schema_version_invalid"
	IssueRoleTypeInvalid      = "role_type_invalid"
	IssueExecutionKindInvalid = "execution_kind_invalid"
	IssueDisplayNameTooLong   = "display_name_too_large"
	IssueDescriptionTooLong   = "description_too_large"
	IssuePersonaTooLarge      = "persona_too_large"
	IssueAddendumTooLarge     = "system_addendum_too_large"
	IssueDocumentTooLarge     = "profile_json_too_large"
	IssueModeInvalid          = "prompt_mode_invalid"
	IssueChatWorkspace        = "chat_workspace_forbidden"
	IssueChatGit              = "chat_git_forbidden"
	IssueBypassPermission     = "bypass_permission_forbidden"
	IssueUnsandboxedCommands  = "unsandboxed_commands_forbidden"
	IssueToolNotAllowed       = "tool_not_allowed"
	IssueSkillNotAllowed      = "skill_not_allowed"
	IssueMCPNotAllowed        = "mcp_server_not_allowed"
	IssueMaxTurns             = "max_turns_exceeded"
	IssueMaxTokens            = "max_tokens_exceeded"
	IssueMaxParallel          = "max_parallel_workers_exceeded"
)

func DecodeProfileDocument(raw []byte) (ProfileDocument, error) {
	if len(raw) > MaxProfileJSONBytes {
		return ProfileDocument{}, fmt.Errorf("profile JSON exceeds %d bytes", MaxProfileJSONBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ProfileDocument
	if err := decoder.Decode(&document); err != nil {
		return ProfileDocument{}, fmt.Errorf("decode profile document: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return ProfileDocument{}, fmt.Errorf("decode profile document: multiple JSON values")
	} else if document.SchemaVersion == SchemaVersionV2 && err != io.EOF {
		return ProfileDocument{}, fmt.Errorf("decode profile document trailing data: %w", err)
	}
	// V1 used to reject these fields as unknown, including explicit empty/null
	// values. Adding V2 fields to the shared model must not relax that contract.
	if document.SchemaVersion == SchemaVersionV1 {
		var fields struct {
			Identity  map[string]json.RawMessage `json:"identity"`
			Execution map[string]json.RawMessage `json:"execution"`
		}
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&fields); err != nil {
			return ProfileDocument{}, fmt.Errorf("decode profile document: %w", err)
		}
		for key := range fields.Identity {
			if strings.EqualFold(key, "role_type") {
				return ProfileDocument{}, fmt.Errorf("decode profile document: unknown field %q for V1", key)
			}
		}
		for key := range fields.Execution {
			if strings.EqualFold(key, "kind") {
				return ProfileDocument{}, fmt.Errorf("decode profile document: unknown field %q for V1", key)
			}
		}
	}
	return document, nil
}

func (d ProfileDocument) Normalize() ProfileDocument {
	if d.SchemaVersion == SchemaVersionV2 {
		d.Identity.RoleType = strings.ToLower(strings.TrimSpace(d.Identity.RoleType))
		d.Execution.Kind = ExecutionKind(strings.ToLower(strings.TrimSpace(string(d.Execution.Kind))))
	}
	d.Identity.DisplayName = strings.TrimSpace(d.Identity.DisplayName)
	d.Identity.Description = strings.TrimSpace(d.Identity.Description)
	d.Prompt.Mode = PromptMode(strings.ToLower(strings.TrimSpace(string(d.Prompt.Mode))))
	d.Prompt.Persona = strings.TrimSpace(d.Prompt.Persona)
	d.Prompt.OutputStyle = strings.TrimSpace(d.Prompt.OutputStyle)
	d.Prompt.Language = strings.TrimSpace(d.Prompt.Language)
	d.Prompt.SystemAddendum = strings.TrimSpace(d.Prompt.SystemAddendum)
	d.Capabilities.Tools.Allow = normalizeSet(d.Capabilities.Tools.Allow)
	d.Capabilities.Tools.Deny = normalizeSet(d.Capabilities.Tools.Deny)
	d.Capabilities.Skills = normalizeSet(d.Capabilities.Skills)
	d.Capabilities.MCPServers = normalizeSet(d.Capabilities.MCPServers)
	d.Execution.Model = strings.TrimSpace(d.Execution.Model)
	d.Execution.Provider = strings.TrimSpace(d.Execution.Provider)
	d.Execution.RuntimeProfile = strings.TrimSpace(d.Execution.RuntimeProfile)
	d.Execution.Effort = strings.TrimSpace(d.Execution.Effort)
	d.Safety.PermissionMode = PermissionMode(strings.TrimSpace(string(d.Safety.PermissionMode)))
	d.Safety.Sandbox = SandboxMode(strings.TrimSpace(string(d.Safety.Sandbox)))
	return d
}

func normalizeSet(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (d ProfileDocument) CanonicalJSON() ([]byte, error) {
	normalized := d.Normalize()
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical profile: %w", err)
	}
	if len(encoded) > MaxProfileJSONBytes {
		return nil, fmt.Errorf("profile JSON exceeds %d bytes", MaxProfileJSONBytes)
	}
	return encoded, nil
}

func (d ProfileDocument) CanonicalHash() (string, error) {
	canonical, err := d.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func Validate(document ProfileDocument, context ValidationContext) ValidationReport {
	document = document.Normalize()
	report := ValidationReport{Valid: true}
	add := func(code, field, message string) {
		report.Valid = false
		report.Issues = append(report.Issues, ValidationIssue{Code: code, Field: field, Message: message, Severity: "error"})
	}

	switch document.SchemaVersion {
	case SchemaVersionV1:
		if document.Identity.RoleType != "" {
			add(IssueRoleTypeInvalid, "identity.role_type", "role_type requires schema_version 2")
		}
		if document.Execution.Kind != "" {
			add(IssueExecutionKindInvalid, "execution.kind", "execution.kind requires schema_version 2")
		}
	case SchemaVersionV2:
		if _, known := LookupRoleType(document.Identity.RoleType); !known {
			add(IssueRoleTypeInvalid, "identity.role_type", fmt.Sprintf("unknown role_type %q", document.Identity.RoleType))
		}
		if _, known := LookupExecutionKind(document.Execution.Kind); !known {
			add(IssueExecutionKindInvalid, "execution.kind", fmt.Sprintf("unknown execution.kind %q", document.Execution.Kind))
		}
	default:
		add(IssueSchemaVersion, "schema_version", "schema_version must be 1 or 2")
	}
	if len([]byte(document.Identity.DisplayName)) > MaxDisplayNameBytes {
		add(IssueDisplayNameTooLong, "identity.display_name", "display_name exceeds the maximum size")
	}
	if len([]byte(document.Identity.Description)) > MaxDescriptionBytes {
		add(IssueDescriptionTooLong, "identity.description", "description exceeds the maximum size")
	}
	if len([]byte(document.Prompt.Persona)) > MaxPersonaBytes {
		add(IssuePersonaTooLarge, "prompt.persona", "persona exceeds the maximum size")
	}
	if len([]byte(document.Prompt.SystemAddendum)) > MaxSystemAddendumBytes {
		add(IssueAddendumTooLarge, "prompt.system_addendum", "system_addendum exceeds the maximum size")
	}
	if mode := document.Prompt.Mode; mode != PromptModeCode && mode != PromptModeChat {
		add(IssueModeInvalid, "prompt.mode", "prompt.mode must be code or chat")
	}
	if document.Prompt.Mode == PromptModeChat && document.Context.Workspace {
		add(IssueChatWorkspace, "context.workspace", "chat profiles cannot discover local workspace context")
	}
	if document.Prompt.Mode == PromptModeChat && document.Context.Git {
		add(IssueChatGit, "context.git", "chat profiles cannot load local git context")
	}
	if document.Safety.PermissionMode == PermissionModeBypass {
		add(IssueBypassPermission, "safety.permission_mode", "bypassPermissions is not configurable in a profile")
	}
	if document.Safety.AllowUnsandboxedCommands {
		add(IssueUnsandboxedCommands, "safety.allow_unsandboxed_commands", "profiles cannot enable unsandboxed commands")
	}

	for _, tool := range document.Capabilities.Tools.Allow {
		if context.AllowedTools != nil && !contains(context.AllowedTools, tool) {
			add(IssueToolNotAllowed, "capabilities.tools.allow", fmt.Sprintf("tool %q is not allowed by the catalog", tool))
		}
	}
	for _, skill := range document.Capabilities.Skills {
		if context.AllowedSkills != nil && !contains(context.AllowedSkills, skill) {
			add(IssueSkillNotAllowed, "capabilities.skills", fmt.Sprintf("skill %q is not allowed by the catalog", skill))
		}
	}
	for _, server := range document.Capabilities.MCPServers {
		if context.AllowedMCPServers != nil && !contains(context.AllowedMCPServers, server) {
			add(IssueMCPNotAllowed, "capabilities.mcp_servers", fmt.Sprintf("MCP server %q is not allowed by the catalog", server))
		}
	}
	if context.MaxTurns > 0 && document.Execution.MaxTurns > context.MaxTurns {
		add(IssueMaxTurns, "execution.max_turns", "max_turns exceeds the configured ceiling")
	}
	if context.MaxTokens > 0 && document.Execution.MaxTokens > context.MaxTokens {
		add(IssueMaxTokens, "execution.max_tokens", "max_tokens exceeds the configured ceiling")
	}
	if context.MaxParallelWorkers > 0 && document.Execution.MaxParallelReadOnlyTools > context.MaxParallelWorkers {
		add(IssueMaxParallel, "execution.max_parallel_read_only_tools", "parallel worker count exceeds the configured ceiling")
	}
	return report
}

func contains(set StringSet, value string) bool {
	_, ok := set[value]
	return ok
}
