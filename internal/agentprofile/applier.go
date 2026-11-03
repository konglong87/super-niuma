package agentprofile

import "strings"

type RuntimeInput struct {
	PromptMode               string
	RuntimeProfile           string
	SystemAddendum           string
	OutputStyle              string
	Language                 string
	Model                    string
	Provider                 string
	Effort                   string
	MaxTurns                 int
	MaxTokens                int
	MaxParallelReadOnlyTools int
	AutoCompact              bool
	AllowedTools             []string
	DeniedTools              []string
	WorkspaceContext         bool
	GitContext               bool
	TenantMemory             bool
	UserMemory               bool
	KnowledgeBase            bool
	SessionHistory           bool
}

func Apply(effective EffectiveProfile) RuntimeInput {
	config := effective.Config.Normalize()
	runtimeProfile := config.Execution.RuntimeProfile
	if runtimeProfile == "" {
		runtimeProfile = "default"
	}
	return RuntimeInput{
		PromptMode:               string(config.Prompt.Mode),
		RuntimeProfile:           runtimeProfile,
		SystemAddendum:           joinPromptAddendum(config.Prompt.Persona, config.Prompt.SystemAddendum),
		OutputStyle:              config.Prompt.OutputStyle,
		Language:                 config.Prompt.Language,
		Model:                    config.Execution.Model,
		Provider:                 config.Execution.Provider,
		Effort:                   config.Execution.Effort,
		MaxTurns:                 config.Execution.MaxTurns,
		MaxTokens:                config.Execution.MaxTokens,
		MaxParallelReadOnlyTools: config.Execution.MaxParallelReadOnlyTools,
		AutoCompact:              config.Execution.AutoCompact,
		AllowedTools:             append([]string(nil), config.Capabilities.Tools.Allow...),
		DeniedTools:              append([]string(nil), config.Capabilities.Tools.Deny...),
		WorkspaceContext:         config.Context.Workspace,
		GitContext:               config.Context.Git,
		TenantMemory:             config.Context.TenantMemory,
		UserMemory:               config.Context.UserMemory,
		KnowledgeBase:            config.Context.KnowledgeBase,
		SessionHistory:           config.Context.SessionHistory,
	}
}

func joinPromptAddendum(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(filtered, "\n\n")
}
