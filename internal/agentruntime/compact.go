package agentruntime

import (
	"context"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/tools"
)

// newCompactor builds the sub-agent compactor. Config comes from Runtime.AutoCompact
// when set, otherwise from the settings resolved for cwd — the same cwd-scoped
// config pattern the runtime already uses for model tiers and pricing. Returning
// nil (no client, or compaction disabled) leaves the loop behaviour unchanged.
func (r Runtime) newCompactor(cwd, model string) *compact.Compactor {
	if r.Client == nil {
		return nil
	}
	cfg := compact.Config{}
	if r.AutoCompact != nil {
		cfg = *r.AutoCompact
	} else {
		cfg = compact.ConfigFromSettings(config.LoadForCWD(cwd).Settings, model)
	}
	if !cfg.WithDefaults().Enabled {
		return nil
	}
	return compact.New(cfg, r.Client, nil)
}

// defsForCompact returns the tool definitions used for token estimation. The
// registry is nil for tool-less sub-agents, and *tools.Registry panics on a nil
// receiver, so take the concrete type rather than an interface — a typed nil
// boxed in an interface compares non-nil.
func defsForCompact(registry *tools.Registry) []anthropic.ToolDefinition {
	if registry == nil {
		return nil
	}
	return registry.Definitions()
}

// maybeCompact applies proactive compaction to the sub-agent history. Failures
// are logged and the original history is returned: compaction is a mitigation,
// never a reason to abort a delegated task.
func (r Runtime) maybeCompact(ctx context.Context, compactor *compact.Compactor, model, system string, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam, turn int, emitEvent func(string, map[string]any)) []anthropic.MessageParam {
	if compactor == nil {
		return messages
	}
	result, err := compactor.MaybeCompact(ctx, model, system, nil, tools, messages)
	if err != nil {
		observability.Error(ctx, nil, "agent.autocompact.error", "agentruntime.Runtime.Run", "sub-agent auto compact failed",
			"turn", turn,
			"reason", result.SkippedReason,
			"estimated_tokens", result.EstimatedUsage,
			"error", err,
		)
		return messages
	}
	if !result.Compacted {
		return messages
	}
	emitCompactEvent(emitEvent, result, turn, false)
	observability.Info(ctx, nil, "agent.autocompact.success", "agentruntime.Runtime.Run", "sub-agent auto compact succeeded",
		"turn", turn,
		"trigger_tokens", result.Metadata.TriggerTokens,
		"token_after", result.Metadata.TokenAfter,
		"compacted_messages", result.Metadata.CompactedMessages,
		"preserved_messages", result.Metadata.PreservedMessages,
	)
	return result.Messages
}

// compactAfterOverflow forces a compaction when the provider rejected the
// sub-agent request as longer than the model's context window, reporting the
// replacement history and true when the turn should be retried. `attempted`
// bounds it to one recovery per run so a prompt that stays too long after
// compaction fails instead of consuming every remaining turn.
func (r Runtime) compactAfterOverflow(ctx context.Context, compactor *compact.Compactor, streamErr error, model, system string, tools []anthropic.ToolDefinition, messages []anthropic.MessageParam, turn int, attempted *bool, emitEvent func(string, map[string]any)) ([]anthropic.MessageParam, bool) {
	if compactor == nil || *attempted || ctx.Err() != nil || !compact.IsContextOverflowError(streamErr) {
		return nil, false
	}
	*attempted = true
	result, err := compactor.ForceCompact(ctx, model, system, nil, tools, messages)
	if err != nil || !result.Compacted {
		observability.Error(ctx, nil, "agent.autocompact.overflow_failed", "agentruntime.Runtime.Run", "sub-agent context overflow recovery could not compact",
			"turn", turn,
			"reason", result.SkippedReason,
			"overflow_error", streamErr,
			"error", err,
		)
		return nil, false
	}
	emitCompactEvent(emitEvent, result, turn, true)
	observability.Info(ctx, nil, "agent.autocompact.overflow_recovered", "agentruntime.Runtime.Run", "sub-agent context overflow triggered forced compaction; retrying turn",
		"turn", turn,
		"trigger_tokens", result.Metadata.TriggerTokens,
		"token_after", result.Metadata.TokenAfter,
		"compacted_messages", result.Metadata.CompactedMessages,
	)
	return result.Messages, true
}

// emitCompactEvent reports compaction as task progress. The payload carries only
// counters — never the summary text, which contains conversation content.
func emitCompactEvent(emitEvent func(string, map[string]any), result compact.Result, turn int, overflow bool) {
	if emitEvent == nil {
		return
	}
	emitEvent(agenttasks.EventCompactSummary, map[string]any{
		"turn":               turn,
		"overflow_recovery":  overflow,
		"trigger_tokens":     result.Metadata.TriggerTokens,
		"token_after":        result.Metadata.TokenAfter,
		"compacted_messages": result.Metadata.CompactedMessages,
		"preserved_messages": result.Metadata.PreservedMessages,
	})
}
