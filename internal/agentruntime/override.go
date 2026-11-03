package agentruntime

import "strings"

// AgentExecutionOverride is the provider-neutral execution contract shared by
// Task, Agent, AgentCreate and team members. Zero values mean "inherit".
type AgentExecutionOverride struct {
	Provider        string   `json:"provider,omitempty"`
	Model           string   `json:"model,omitempty"`
	Effort          string   `json:"effort,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	MaxTurns        int      `json:"max_turns,omitempty"`
	TimeoutMS       int      `json:"timeout_ms,omitempty"`
	ReasonCodes     []string `json:"reason_codes,omitempty"`
}

const defaultOverrideEffort = "medium"

// NormalizeExecutionOverride removes invalid values before a run is created.
// Every clamp/default is observable through ReasonCodes; callers must not
// silently claim the requested value was applied.
func NormalizeExecutionOverride(input AgentExecutionOverride) AgentExecutionOverride {
	out := input
	out.Provider = strings.TrimSpace(out.Provider)
	out.Model = strings.TrimSpace(out.Model)
	out.Effort = strings.ToLower(strings.TrimSpace(out.Effort))
	if out.Effort != "" && out.Effort != "low" && out.Effort != "medium" && out.Effort != "high" && out.Effort != "max" {
		out.Effort = defaultOverrideEffort
		out.ReasonCodes = appendReason(out.ReasonCodes, "effort_invalid_defaulted")
	}
	if out.MaxOutputTokens < 0 {
		out.MaxOutputTokens = 0
		out.ReasonCodes = appendReason(out.ReasonCodes, "max_output_tokens_clamped")
	}
	if out.MaxTurns < 0 {
		out.MaxTurns = 0
		out.ReasonCodes = appendReason(out.ReasonCodes, "max_turns_clamped")
	}
	if out.TimeoutMS < 0 {
		out.TimeoutMS = 0
		out.ReasonCodes = appendReason(out.ReasonCodes, "timeout_clamped")
	}
	return out
}

// ResolveExecutionOverride applies explicit child values over inherited
// parent values and preserves reason codes from normalization.
func ResolveExecutionOverride(parent, child AgentExecutionOverride) AgentExecutionOverride {
	parent = NormalizeExecutionOverride(parent)
	child = NormalizeExecutionOverride(child)
	out := parent
	if child.Provider != "" {
		out.Provider = child.Provider
	}
	if child.Model != "" {
		out.Model = child.Model
	}
	if child.Effort != "" {
		out.Effort = child.Effort
	}
	if child.MaxOutputTokens > 0 {
		out.MaxOutputTokens = child.MaxOutputTokens
	}
	if child.MaxTurns > 0 {
		out.MaxTurns = child.MaxTurns
	}
	if child.TimeoutMS > 0 {
		out.TimeoutMS = child.TimeoutMS
	}
	out.ReasonCodes = appendReason(append([]string(nil), parent.ReasonCodes...), child.ReasonCodes...)
	return out
}

func appendReason(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		if value != "" {
			if _, ok := seen[value]; !ok {
				dst = append(dst, value)
				seen[value] = struct{}{}
			}
		}
	}
	return dst
}
