package runtimecompose

import (
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/konglong87/super-niuma/internal/anthropic"
)

// readUsage separates measured counters from certainty. Missing/zero counters
// are not proof of free execution: the scheduler retains the admission reserve
// when known is false. Never substitute that reserve into actual provider usage.
func readUsage(raw anthropic.Usage) (usage workflow.Usage, known bool, err *ExecutionError) {
	for _, count := range [...]int{raw.InputTokens, raw.OutputTokens, raw.CacheReadInputTokens, raw.CacheCreationInputTokens,
		raw.CacheCreationEphemeral1hInputTokens, raw.CacheCreationEphemeral5mInputTokens,
		raw.CacheCreation.Ephemeral1hInputTokens, raw.CacheCreation.Ephemeral5mInputTokens, raw.ReasoningOutputTokens} {
		if count < 0 {
			return workflow.Usage{}, false, invalidUsage()
		}
	}
	if raw.InputTokensIncludeCacheRead && raw.CacheReadInputTokens > raw.InputTokens {
		return workflow.Usage{}, false, invalidUsage()
	}
	usage = mapUsage(raw)
	if err := validateUsage(usage); err != nil {
		// Invalid counters are not billable evidence and cannot enter the Store's
		// arithmetic. An explicit failure + known=false retains the entire reserve.
		return workflow.Usage{}, false, err
	}
	input := usage.InputTokens + usage.CacheReadTokens + usage.CacheWriteTokens
	return usage, input > 0 && usage.OutputTokens > 0, nil
}

func mapUsage(raw anthropic.Usage) workflow.Usage {
	// query.Result.Usage.InputTokens includes cache tiers; workflow stores these
	// tiers separately. Convert cache-inclusive protocols without double counting.
	input := raw.InputTokens
	if raw.InputTokensIncludeCacheRead {
		input -= raw.CacheReadInputTokens
	}
	return workflow.Usage{InputTokens: input, OutputTokens: raw.OutputTokens, CacheReadTokens: raw.CacheReadInputTokens, CacheWriteTokens: raw.CacheCreationInputTokens}
}

func validateUsage(usage workflow.Usage) *ExecutionError {
	total := 0
	maxInt := int(^uint(0) >> 1)
	for _, count := range [...]int{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens} {
		if count < 0 || count > maxInt-total {
			return invalidUsage()
		}
		total += count
	}
	return nil
}

func invalidUsage() *ExecutionError {
	return failure(CodeUsage, "Provider returned invalid token usage.", false)
}
