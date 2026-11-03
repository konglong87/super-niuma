package agentprofile

func BuiltinProfiles() map[string]Profile {
	return map[string]Profile{
		ProfileChatAssistant:       builtinChatAssistant(),
		ProfileCopywriter:          builtinCopywriter(),
		ProfileCoder:               builtinCoder(),
		ProfileWebUIV2Orchestrator: builtinWebUIV2Orchestrator(),
	}
}

func builtinWebUIV2Orchestrator() Profile {
	return Profile{
		Key: ProfileWebUIV2Orchestrator, Scope: ScopeBuiltin, DisplayName: "WebUI v2 Orchestrator",
		Description: "A dedicated WebUI v2 orchestrator with bounded managed Session Control tools.", Version: 1, Status: StatusPublished,
		Config: ProfileDocument{
			SchemaVersion: SchemaVersionV1,
			Prompt:        PromptPolicy{Mode: PromptModeChat, Language: "zh-CN"},
			Capabilities:  CapabilityPolicy{Tools: ToolPolicy{Allow: []string{"SessionCreate", "SessionList", "SessionGet", "SessionSend", "SessionStop", "SessionAttach", "SessionMonitor"}}},
			Execution:     ExecutionPolicy{Effort: "medium", MaxTurns: 6, MaxTokens: 4096},
			Context:       ContextPolicy{TenantMemory: true, UserMemory: true, SessionHistory: true},
			Safety:        SafetyPolicy{PermissionMode: PermissionModeAsk, Sandbox: SandboxRequired},
		},
	}
}

// IsWebUIV2Orchestrator deliberately checks the immutable builtin identity,
// not its mutable tool allow-list. Arbitrary tenant profiles cannot opt in by
// merely naming a Session tool.
func IsWebUIV2Orchestrator(profile Profile) bool {
	return profile.Key == ProfileWebUIV2Orchestrator && profile.Scope == ScopeBuiltin && profile.Status == StatusPublished
}

func builtinChatAssistant() Profile {
	return Profile{
		Key:         ProfileChatAssistant,
		Scope:       ScopeBuiltin,
		DisplayName: "Chat Assistant",
		Description: "A general multi-tenant conversation assistant.",
		Version:     1,
		Status:      StatusPublished,
		Config: ProfileDocument{
			SchemaVersion: SchemaVersionV1,
			Prompt: PromptPolicy{
				Mode:     PromptModeChat,
				Language: "zh-CN",
			},
			Capabilities: CapabilityPolicy{AllowAttachments: true},
			Execution:    ExecutionPolicy{Effort: "medium", MaxTurns: 3, MaxTokens: 2048},
			Context: ContextPolicy{
				TenantMemory:   true,
				UserMemory:     true,
				KnowledgeBase:  true,
				SessionHistory: true,
			},
			Safety: SafetyPolicy{PermissionMode: PermissionModeAsk, Sandbox: SandboxRequired},
		},
	}
}

func builtinCopywriter() Profile {
	return Profile{
		Key:         ProfileCopywriter,
		Scope:       ScopeBuiltin,
		DisplayName: "Copywriter",
		Description: "A writing assistant for audience-aware marketing content.",
		Version:     1,
		Status:      StatusPublished,
		Config: ProfileDocument{
			SchemaVersion: SchemaVersionV1,
			Prompt: PromptPolicy{
				Mode:        PromptModeChat,
				Persona:     "You are a careful copywriter who clarifies audience, channel, goals, and constraints before drafting.",
				OutputStyle: "marketing",
				Language:    "zh-CN",
			},
			Capabilities: CapabilityPolicy{
				Tools:            ToolPolicy{Allow: []string{"Read", "WebSearch"}},
				Skills:           []string{"copywriting"},
				AllowAttachments: true,
			},
			Execution: ExecutionPolicy{Effort: "medium", MaxTurns: 3, MaxTokens: 2048},
			Context: ContextPolicy{
				TenantMemory:   true,
				UserMemory:     true,
				KnowledgeBase:  true,
				SessionHistory: true,
			},
			Safety: SafetyPolicy{PermissionMode: PermissionModeAsk, Sandbox: SandboxRequired},
		},
	}
}

func builtinCoder() Profile {
	return Profile{
		Key:         ProfileCoder,
		Scope:       ScopeBuiltin,
		DisplayName: "Coder",
		Description: "A code agent with explicit workspace context and guarded tools.",
		Version:     1,
		Status:      StatusPublished,
		Config: ProfileDocument{
			SchemaVersion: SchemaVersionV1,
			Prompt:        PromptPolicy{Mode: PromptModeCode, Language: "zh-CN"},
			Capabilities: CapabilityPolicy{
				Tools:       ToolPolicy{Allow: []string{"Bash", "Edit", "Glob", "Grep", "Read"}},
				AllowAgents: true,
			},
			Execution: ExecutionPolicy{
				RuntimeProfile:           "default",
				Effort:                   "high",
				MaxTurns:                 20,
				MaxTokens:                8192,
				MaxParallelReadOnlyTools: 4,
				AutoCompact:              true,
			},
			Context: ContextPolicy{
				Workspace:      true,
				Git:            true,
				TenantMemory:   true,
				UserMemory:     true,
				SessionHistory: true,
			},
			Safety: SafetyPolicy{PermissionMode: PermissionModeAsk, Sandbox: SandboxRequired},
		},
	}
}
