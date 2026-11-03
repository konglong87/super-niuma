package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/permissions"
)

type guardedTool struct {
	inner  Tool
	policy permissions.Policy
}

func Guard(tool Tool, policy permissions.Policy) Tool {
	return guardedTool{inner: tool, policy: policy}
}

func GuardAll(policy permissions.Policy, items ...Tool) []Tool {
	out := make([]Tool, 0, len(items))
	for _, item := range items {
		out = append(out, Guard(item, policy))
	}
	return out
}

func (g guardedTool) Name() string                 { return g.inner.Name() }
func (g guardedTool) Description() string          { return g.inner.Description() }
func (g guardedTool) InputSchema() json.RawMessage { return g.inner.InputSchema() }
func (g guardedTool) ExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicyFor(g.inner)
}
func (g guardedTool) ParallelSafe(input json.RawMessage, toolContext Context) bool {
	if ExecutionPolicyFor(g.inner).Concurrency != ConcurrencyReadOnly {
		return false
	}
	if toolContext.ActiveSkill != nil && len(toolContext.ActiveSkill.AllowedTools) > 0 && !permissions.MatchAnyRule(toolContext.ActiveSkill.AllowedTools, g.inner.Name(), input) {
		return false
	}
	if toolContext.AgentPolicy != nil {
		if len(toolContext.AgentPolicy.AllowedTools) > 0 && !permissions.MatchAnyRule(toolContext.AgentPolicy.AllowedTools, g.inner.Name(), input) {
			return false
		}
		if len(toolContext.AgentPolicy.DeniedTools) > 0 && permissions.MatchAnyRule(toolContext.AgentPolicy.DeniedTools, g.inner.Name(), input) {
			return false
		}
	}
	return g.effectivePolicy(toolContext).CheckRequest(g.inner.Name(), input).Allowed
}
func (g guardedTool) MaxResultSizeChars() int {
	if limiter, ok := g.inner.(ResultSizeLimiter); ok {
		return limiter.MaxResultSizeChars()
	}
	return 0
}
func (g guardedTool) SkipToolResultBudget() bool {
	if skipper, ok := g.inner.(ResultBudgetSkipper); ok {
		return skipper.SkipToolResultBudget()
	}
	return false
}
func (g guardedTool) Run(ctx context.Context, input json.RawMessage, toolContext Context) Result {
	toolContext = redactComputerPermissionContext(g.inner.Name(), toolContext)
	if result, cancelled := cancelledContextResult(ctx); cancelled {
		return result
	}
	if toolContext.ActiveSkill != nil && len(toolContext.ActiveSkill.AllowedTools) > 0 {
		if !permissions.MatchAnyRule(toolContext.ActiveSkill.AllowedTools, g.inner.Name(), input) {
			return Result{Content: "tool " + g.inner.Name() + " is not allowed by active skill " + toolContext.ActiveSkill.Name, IsError: true}
		}
	}
	if toolContext.AgentPolicy != nil {
		agent := strings.TrimSpace(toolContext.AgentPolicy.Name)
		if len(toolContext.AgentPolicy.AllowedTools) > 0 && !permissions.MatchAnyRule(toolContext.AgentPolicy.AllowedTools, g.inner.Name(), input) {
			return Result{Content: "tool " + g.inner.Name() + " is not allowed by agent " + agent, IsError: true}
		}
		if len(toolContext.AgentPolicy.DeniedTools) > 0 && permissions.MatchAnyRule(toolContext.AgentPolicy.DeniedTools, g.inner.Name(), input) {
			return Result{Content: "tool " + g.inner.Name() + " is denied by agent " + agent, IsError: true}
		}
	}
	policy := g.effectivePolicy(toolContext)
	if result, cancelled := cancelledContextResult(ctx); cancelled {
		return result
	}
	decision := policy.CheckRequest(g.inner.Name(), input)
	if toolContext.PermissionAudit != nil {
		if result, cancelled := cancelledContextResult(ctx); cancelled {
			return result
		}
		toolContext.PermissionAudit(PermissionAudit{
			ToolName: g.inner.Name(),
			Allowed:  decision.Allowed,
			Reason:   decision.Reason,
			Rule:     decision.Rule,
			Request:  decision.Request,
			Source:   decision.Source,
		})
		if result, cancelled := cancelledContextResult(ctx); cancelled {
			return result
		}
	}
	if !decision.Allowed {
		if toolContext.PermissionPrompt != nil && requiresPermissionPrompt(decision.Reason) {
			if result, cancelled := cancelledContextResult(ctx); cancelled {
				return result
			}
			response := toolContext.PermissionPrompt(ctx, PermissionPromptRequest{
				ToolName: g.inner.Name(),
				Input:    input,
				Reason:   decision.Reason,
				Rule:     decision.Rule,
				Request:  decision.Request,
				Source:   decision.Source,
			})
			if result, cancelled := cancelledContextResult(ctx); cancelled {
				return result
			}
			if response.Allowed {
				update := PermissionUpdate{
					ToolName:    g.inner.Name(),
					Input:       input,
					Request:     decision.Request,
					Rule:        firstNonEmpty(response.Rule, permissionRule(g.inner.Name(), decision.Request)),
					Decision:    firstNonEmpty(response.Decision, "allow"),
					Destination: strings.ToLower(strings.TrimSpace(response.Destination)),
					Reason:      response.Reason,
				}
				if update.Destination != "" && update.Destination != "once" && toolContext.PermissionUpdate != nil {
					if result, cancelled := cancelledContextResult(ctx); cancelled {
						return result
					}
					if err := toolContext.PermissionUpdate(update); err != nil {
						return Result{Content: err.Error(), IsError: true}
					}
					if result, cancelled := cancelledContextResult(ctx); cancelled {
						return result
					}
				}
				if toolContext.PermissionAudit != nil {
					if result, cancelled := cancelledContextResult(ctx); cancelled {
						return result
					}
					toolContext.PermissionAudit(PermissionAudit{
						ToolName: g.inner.Name(),
						Allowed:  true,
						Reason:   firstNonEmpty(response.Reason, "approved by permission prompt tool"),
						Rule:     firstNonEmpty(update.Rule, "permission-prompt-tool"),
						Request:  decision.Request,
						Source:   decision.Source,
					})
					if result, cancelled := cancelledContextResult(ctx); cancelled {
						return result
					}
				}
				if result, cancelled := cancelledContextResult(ctx); cancelled {
					return result
				}
				return g.inner.Run(ctx, input, toolContext)
			}
			update := PermissionUpdate{
				ToolName:    g.inner.Name(),
				Input:       input,
				Request:     decision.Request,
				Rule:        firstNonEmpty(response.Rule, permissionRule(g.inner.Name(), decision.Request)),
				Decision:    firstNonEmpty(response.Decision, "deny"),
				Destination: strings.ToLower(strings.TrimSpace(response.Destination)),
				Reason:      response.Reason,
			}
			if update.Destination != "" && update.Destination != "once" && toolContext.PermissionUpdate != nil {
				if result, cancelled := cancelledContextResult(ctx); cancelled {
					return result
				}
				if err := toolContext.PermissionUpdate(update); err != nil {
					return Result{Content: err.Error(), IsError: true}
				}
				if result, cancelled := cancelledContextResult(ctx); cancelled {
					return result
				}
			}
			if response.Reason != "" {
				return Result{Content: response.Reason, IsError: true}
			}
		}
		return Result{Content: decision.Reason, IsError: true}
	}
	if result, cancelled := cancelledContextResult(ctx); cancelled {
		return result
	}
	return g.inner.Run(ctx, input, toolContext)
}

func cancelledContextResult(ctx context.Context) (Result, bool) {
	if err := ctx.Err(); err != nil {
		return Result{Content: err.Error(), IsError: true}, true
	}
	return Result{}, false
}

func (g guardedTool) effectivePolicy(toolContext Context) permissions.Policy {
	policy := g.policy
	if g.policy.RuleSources != nil {
		policy.RuleSources = make(map[string]string, len(g.policy.RuleSources))
		for key, source := range g.policy.RuleSources {
			policy.RuleSources[key] = source
		}
	}
	if toolContext.RuntimePermissionMode != nil {
		rawMode := strings.TrimSpace(toolContext.RuntimePermissionMode())
		mode := rawMode
		if normalized, ok := permissions.NormalizeMode(mode); ok {
			mode = normalized
		}
		if mode != "" {
			policy.DefaultMode = mode
			// Only an explicit bypassPermissions mode grants the full bypass. A
			// session-level bypass (--dangerously-skip-permissions, TUI bypass
			// toggle) survives while the runtime mode stays permissive and is
			// revoked as soon as the user narrows it — acceptEdits included.
			switch {
			case permissions.ModeGrantsBypass(rawMode):
				policy.Bypass = true
			case mode != permissions.ModeAllow:
				policy.Bypass = false
			}
			policy.Source = firstNonEmpty(policy.Source, "runtime")
			if policy.Bypass {
				policy.Allow = appendUnique(policy.Allow, "*")
			}
			if policy.RuleSources == nil {
				policy.RuleSources = map[string]string{}
			}
			policy.RuleSources["defaultMode"] = "runtime"
			policy.RuleSources["allow:*"] = "runtime"
		}
	}
	if toolContext.AgentPolicy != nil && strings.TrimSpace(toolContext.AgentPolicy.PermissionMode) != "" {
		mode := strings.TrimSpace(toolContext.AgentPolicy.PermissionMode)
		if normalized, ok := permissions.NormalizeMode(mode); ok {
			mode = normalized
		}
		policy.DefaultMode = mode
		policy.Source = firstNonEmpty(policy.Source, "agent:"+strings.TrimSpace(toolContext.AgentPolicy.Name))
		policy.AutoMode = strings.EqualFold(mode, "auto")
		if policy.RuleSources == nil {
			policy.RuleSources = map[string]string{}
		}
		policy.RuleSources["defaultMode"] = "agent:" + strings.TrimSpace(toolContext.AgentPolicy.Name)
	}
	policy.Allow = append(append([]string(nil), policy.Allow...), toolContext.SessionAllow...)
	policy.Deny = append(append([]string(nil), policy.Deny...), toolContext.SessionDeny...)
	if len(toolContext.SessionAllow) > 0 || len(toolContext.SessionDeny) > 0 {
		if policy.RuleSources == nil {
			policy.RuleSources = map[string]string{}
		}
		for _, rule := range toolContext.SessionAllow {
			policy.RuleSources["allow:"+strings.TrimSpace(rule)] = "session"
		}
		for _, rule := range toolContext.SessionDeny {
			policy.RuleSources["deny:"+strings.TrimSpace(rule)] = "session"
		}
	}
	return policy
}

func requiresPermissionPrompt(reason string) bool {
	return strings.Contains(reason, "requires permission approval")
}

func permissionRule(toolName, request string) string {
	if strings.TrimSpace(request) == "" {
		return toolName
	}
	return toolName + ":" + request
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}
