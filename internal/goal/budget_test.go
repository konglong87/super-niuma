package goal

import (
	"strings"
	"testing"
)

func TestAnalyzeBudgetBlocksWhenTurnsExhausted(t *testing.T) {
	policy := AnalyzeBudget(Goal{TurnBudget: 2, TurnsUsed: 2, TokenBudget: 100, InputTokens: 10})
	if !policy.Exhausted || policy.BlockerKey != "turn_budget_exhausted" {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestAnalyzeBudgetMarksClosingWhenOnlyOneTurnRemains(t *testing.T) {
	policy := AnalyzeBudget(Goal{TurnBudget: 3, TurnsUsed: 2, TokenBudget: 1000, InputTokens: 100})
	if !policy.Closing || policy.Exhausted {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestBuildTurnPromptIncludesClosingPolicy(t *testing.T) {
	prompt := BuildTurnPromptWithContext(Goal{ID: "goal_budget", Objective: "ship", Status: StatusActive, TurnBudget: 2, TurnsUsed: 1, TokenBudget: 1000}, TurnPromptContext{
		Budget: AnalyzeBudget(Goal{TurnBudget: 2, TurnsUsed: 1, TokenBudget: 1000, InputTokens: 100}),
	})
	if !strings.Contains(prompt, "Budget closing policy:") || !strings.Contains(prompt, "Do not expand scope") {
		t.Fatalf("prompt = %s", prompt)
	}
}
