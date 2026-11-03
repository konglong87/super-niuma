package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/konglong87/super-niuma/internal/agentprofile"
	"github.com/konglong87/super-niuma/internal/runtimeprofile"
)

func profileOverridesFromMap(values map[string]any) (agentprofile.ProfileOverrides, error) {
	var result agentprofile.ProfileOverrides
	for key, raw := range values {
		switch key {
		case "language":
			value, ok := raw.(string)
			if !ok {
				return result, fmt.Errorf("profile_overrides.language must be a string")
			}
			result.Language = value
		case "output_style":
			value, ok := raw.(string)
			if !ok {
				return result, fmt.Errorf("profile_overrides.output_style must be a string")
			}
			result.OutputStyle = value
		case "effort":
			value, ok := raw.(string)
			if !ok {
				return result, fmt.Errorf("profile_overrides.effort must be a string")
			}
			result.Effort = value
		case "max_turns":
			value, err := overrideInt(raw)
			if err != nil {
				return result, fmt.Errorf("profile_overrides.max_turns: %w", err)
			}
			result.MaxTurns = &value
		case "max_tokens":
			value, err := overrideInt(raw)
			if err != nil {
				return result, fmt.Errorf("profile_overrides.max_tokens: %w", err)
			}
			result.MaxTokens = &value
		default:
			return result, fmt.Errorf("profile_overrides.%s is not allowed", key)
		}
	}
	return result, nil
}

func overrideInt(raw any) (int, error) {
	switch value := raw.(type) {
	case float64:
		if value < 1 || value != float64(int(value)) {
			return 0, fmt.Errorf("must be a positive integer")
		}
		return int(value), nil
	case int:
		if value < 1 {
			return 0, fmt.Errorf("must be a positive integer")
		}
		return value, nil
	case string:
		value = strings.TrimSpace(value)
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return 0, fmt.Errorf("must be a positive integer")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("must be a positive integer")
	}
}

func applyAgentProfileToOptions(opts *options, effective agentprofile.EffectiveProfile) error {
	if opts == nil {
		return fmt.Errorf("agent profile options are nil")
	}
	if effective.Config.SchemaVersion == agentprofile.SchemaVersionV2 && effective.Config.Execution.Kind != "" && effective.Config.Execution.Kind != agentprofile.ExecutionKindText {
		return fmt.Errorf("Agent execution kind %q cannot run through the text session route", effective.Config.Execution.Kind)
	}
	input := agentprofile.Apply(effective)
	opts.promptMode = input.PromptMode
	if input.RuntimeProfile != "" {
		opts.runtimeProfile = runtimeprofile.Profile(input.RuntimeProfile)
	}
	if input.Model != "" {
		opts.model = input.Model
	}
	if input.Provider != "" {
		opts.providerName = input.Provider
	}
	if input.Effort != "" {
		opts.effort = input.Effort
	}
	if input.OutputStyle != "" {
		opts.outputStyle = input.OutputStyle
	}
	if input.Language != "" {
		opts.language = input.Language
	}
	if input.MaxTurns > 0 {
		opts.maxTurns = input.MaxTurns
	}
	if input.MaxTokens > 0 {
		opts.maxTokens = input.MaxTokens
	}
	if input.MaxParallelReadOnlyTools != 0 {
		opts.maxParallelReadOnlyTools = input.MaxParallelReadOnlyTools
	}
	opts.allowedTools = append([]string(nil), input.AllowedTools...)
	opts.deniedTools = append([]string(nil), input.DeniedTools...)
	opts.enabledTools = append([]string(nil), input.AllowedTools...)
	opts.toolsSpecified = true
	opts.inlineTenantSkills = mergeSkillKeys(opts.inlineTenantSkills, effective.Config.Normalize().Capabilities.Skills)
	if addendum := strings.TrimSpace(input.SystemAddendum); addendum != "" {
		opts.appendSystem = appendWithBlankLine(opts.appendSystem, addendum)
	}
	opts.agentProfileKey = effective.ProfileKey
	opts.agentProfileVersion = effective.ProfileVersion
	opts.agentProfileSource = effective.Source
	opts.agentProfileRequestedHash = effective.RequestedHash
	opts.agentProfileEffectiveHash = effective.EffectiveHash
	opts.agentProfileBlockedOverrides = len(effective.BlockedOverrides)
	// Tool names alone do not grant the managed Session capability. This marker
	// comes from the immutable builtin profile identity and is checked again at
	// registration with trusted tenant context plus an injected service.
	opts.sessionControlProfile = agentprofile.IsWebUIV2Orchestrator(effective.Profile)
	return nil
}

func mergeSkillKeys(existing, additional []string) []string {
	result := make([]string, 0, len(existing)+len(additional))
	seen := make(map[string]struct{}, len(existing)+len(additional))
	for _, values := range [][]string{existing, additional} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
