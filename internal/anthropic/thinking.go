package anthropic

import (
	"strconv"
	"strings"
)

func ThinkingConfigFromEffort(effort string, maxTokens int) *ThinkingConfig {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || effort == "inherit" || effort == "none" || effort == "off" || effort == "disabled" {
		return nil
	}
	budget := thinkingBudgetTokens(effort)
	if budget <= 0 {
		return nil
	}
	if maxTokens > 0 && budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < 1024 {
		return nil
	}
	return &ThinkingConfig{
		Type:         "enabled",
		Effort:       effort,
		BudgetTokens: budget,
		Display:      "summarized",
	}
}

func thinkingBudgetTokens(effort string) int {
	switch effort {
	case "low":
		return 1024
	case "medium", "normal", "default":
		return 2048
	case "high":
		return 4096
	case "max", "maximum":
		return 8192
	default:
		n, err := strconv.Atoi(effort)
		if err != nil || n <= 0 {
			return 0
		}
		if n < 1024 {
			return 1024
		}
		return n
	}
}
