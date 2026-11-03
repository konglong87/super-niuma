package goal

import "fmt"

const closingTokenBudgetDivisor = 10

type BudgetPolicy struct {
	TurnsRemaining  int
	TokensRemaining int
	Exhausted       bool
	Closing         bool
	Reason          string
	NextAction      string
	BlockerKey      string
}

func AnalyzeBudget(goal Goal) BudgetPolicy {
	usedTokens := goal.InputTokens + goal.OutputTokens
	policy := BudgetPolicy{
		TurnsRemaining:  goal.TurnBudget - goal.TurnsUsed,
		TokensRemaining: goal.TokenBudget - usedTokens,
	}
	if goal.TurnBudget > 0 && policy.TurnsRemaining <= 0 {
		policy.Exhausted = true
		policy.Reason = "turn budget exhausted before running another goal turn"
		policy.NextAction = "increase the turn budget or refine the remaining objective"
		policy.BlockerKey = "turn_budget_exhausted"
		return policy
	}
	if goal.TokenBudget > 0 && policy.TokensRemaining <= 0 {
		policy.Exhausted = true
		policy.Reason = "token budget exhausted before running another goal turn"
		policy.NextAction = "increase the token budget or reduce the remaining objective"
		policy.BlockerKey = "token_budget_exhausted"
		return policy
	}
	if goal.TurnBudget > 0 && policy.TurnsRemaining <= 1 {
		policy.Closing = true
	}
	if goal.TokenBudget > 0 && policy.TokensRemaining <= closingTokenThreshold(goal.TokenBudget) {
		policy.Closing = true
	}
	if policy.Closing {
		policy.Reason = fmt.Sprintf("goal budget is low: %d turns and %d tokens remain", policy.TurnsRemaining, policy.TokensRemaining)
		policy.NextAction = "use the remaining budget for verification, concise summary, completion, or a clear blocker"
	}
	return policy
}

func closingTokenThreshold(tokenBudget int) int {
	if tokenBudget <= 0 {
		return 0
	}
	threshold := tokenBudget / closingTokenBudgetDivisor
	if threshold < 1 {
		return 1
	}
	return threshold
}
