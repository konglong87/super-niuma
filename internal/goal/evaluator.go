package goal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type DecisionStatus string

const (
	DecisionContinue DecisionStatus = "continue"
	DecisionComplete DecisionStatus = "complete"
	DecisionBlocked  DecisionStatus = "blocked"
	DecisionFailed   DecisionStatus = "failed"
)

type TurnResult struct {
	Response          string
	StopReason        string
	Turns             int
	InputTokens       int
	OutputTokens      int
	ToolErrors        int
	Evidence          []GoalEvidence
	CheckpointName    string
	CheckpointCreated bool
	Error             error
	ContextDone       bool
	PermissionDenied  bool
}

type Decision struct {
	Status     DecisionStatus `json:"status"`
	Reason     string         `json:"reason,omitempty"`
	NextAction string         `json:"next_action,omitempty"`
	BlockerKey string         `json:"blocker_key,omitempty"`
	Confidence float64        `json:"confidence,omitempty"`
}

type Evaluator interface {
	Evaluate(ctx context.Context, goal Goal, result TurnResult, recent []Event) Decision
}

type Classifier interface {
	ClassifyGoal(ctx context.Context, goal Goal, prompt string) (string, error)
}

type DeterministicEvaluator struct {
	BlockedThreshold int
}

func (e DeterministicEvaluator) Evaluate(ctx context.Context, goal Goal, result TurnResult, recent []Event) Decision {
	_ = recent
	threshold := e.BlockedThreshold
	if threshold <= 0 {
		threshold = DefaultBlockedThreshold
	}
	if result.Error != nil {
		if errors.Is(result.Error, context.Canceled) || result.ContextDone {
			return Decision{Status: DecisionContinue, Reason: "turn was cancelled; goal remains active", NextAction: "resume goal when ready"}
		}
		blocker := blockerKey(result.Error.Error())
		if containsAny(blocker, "internal runner error", "corrupt goal state", "unrecoverable") {
			return Decision{Status: DecisionFailed, Reason: result.Error.Error(), BlockerKey: blocker}
		}
		if result.PermissionDenied || hardBlocker(blocker) || goal.RepeatedBlockerCount+1 >= threshold {
			return Decision{Status: DecisionBlocked, Reason: result.Error.Error(), BlockerKey: blocker}
		}
		return Decision{Status: DecisionContinue, Reason: result.Error.Error(), NextAction: "retry or inspect the failed turn", BlockerKey: blocker}
	}
	status, blocker := parseGoalStatus(result.Response)
	switch status {
	case "complete":
		return Decision{Status: DecisionComplete, Reason: "runner reported objective complete", Confidence: 0.8}
	case "blocked":
		return Decision{Status: DecisionBlocked, Reason: "runner reported blocker", BlockerKey: firstNonEmpty(blocker, "model_reported_blocker"), Confidence: 0.7}
	}
	if goal.TurnBudget > 0 && goal.TurnsUsed >= goal.TurnBudget {
		return Decision{Status: DecisionBlocked, Reason: "turn budget exhausted", BlockerKey: "turn_budget_exhausted"}
	}
	if goal.TokenBudget > 0 && goal.InputTokens+goal.OutputTokens >= goal.TokenBudget {
		return Decision{Status: DecisionBlocked, Reason: "token budget exhausted", BlockerKey: "token_budget_exhausted"}
	}
	return Decision{Status: DecisionContinue, Reason: "goal still active", NextAction: "continue next goal turn", Confidence: 0.5}
}

type ModelClassifiedEvaluator struct {
	Base       DeterministicEvaluator
	Classifier Classifier
}

type EvidenceEvaluator struct {
	Base     Evaluator
	Plan     GoalPlan
	Evidence []GoalEvidence
}

func (e EvidenceEvaluator) Evaluate(ctx context.Context, goal Goal, result TurnResult, recent []Event) Decision {
	base := e.Base
	if base == nil {
		base = DeterministicEvaluator{}
	}
	allEvidence := append([]GoalEvidence(nil), e.Evidence...)
	allEvidence = append(allEvidence, result.Evidence...)
	evidenceDecision, ok := e.evaluateEvidence(result)
	if ok {
		if evidenceDecision.Status == DecisionComplete {
			if pending, pendingOK := pendingCapabilityFollowUpDecision(allEvidence); pendingOK {
				return pending
			}
		}
		return evidenceDecision
	}
	deterministic := base.Evaluate(ctx, goal, result, recent)
	if deterministic.Status == DecisionComplete {
		if pending, ok := pendingCapabilityFollowUpDecision(allEvidence); ok {
			return pending
		}
	}
	if deterministic.Status == DecisionComplete && requiredCriteriaPending(e.Plan) {
		return Decision{
			Status:     DecisionContinue,
			Reason:     "required acceptance criteria are not passed",
			NextAction: "collect evidence for pending acceptance criteria",
			Confidence: 0.9,
		}
	}
	return deterministic
}

func (e EvidenceEvaluator) evaluateEvidence(result TurnResult) (Decision, bool) {
	if strings.TrimSpace(e.Plan.GoalID) == "" || len(e.Plan.AcceptanceCriteria) == 0 {
		return Decision{}, false
	}
	if requiredCriteriaPending(e.Plan) {
		return Decision{}, false
	}
	if result.ToolErrors > 0 || hasFailedEvidence(e.Evidence) || hasFailedEvidence(result.Evidence) {
		return Decision{
			Status:     DecisionContinue,
			Reason:     "required criteria are satisfied but recent evidence contains failures",
			NextAction: "fix failing evidence before completing the goal",
			Confidence: 0.85,
		}, true
	}
	return Decision{
		Status:     DecisionComplete,
		Reason:     "all required acceptance criteria are satisfied by evidence",
		Confidence: 0.95,
	}, true
}

func requiredCriteriaPending(plan GoalPlan) bool {
	for _, criterion := range plan.AcceptanceCriteria {
		if !criterion.Required {
			continue
		}
		switch criterion.Status {
		case CriterionStatusPassed, CriterionStatusWaived:
			continue
		default:
			return true
		}
	}
	return false
}

func hasFailedEvidence(evidence []GoalEvidence) bool {
	for _, item := range evidence {
		if !item.Passed {
			return true
		}
	}
	return false
}

func pendingCapabilityFollowUpDecision(evidence []GoalEvidence) (Decision, bool) {
	resolved := resolvedCapabilityFollowUps(evidence)
	for i := len(evidence) - 1; i >= 0; i-- {
		item := evidence[i]
		payload, ok := goalCapabilityEvidencePayloadFrom(item)
		if !ok {
			continue
		}
		if resolvedIndex, ok := resolved[item.ID]; ok && resolvedIndex > i {
			continue
		}
		if next := capabilityPendingFollowUpText(payload); next != "" {
			return Decision{
				Status:     DecisionContinue,
				Reason:     "pending capability follow-up evidence must be handled before completion",
				NextAction: next,
				Confidence: 0.9,
			}, true
		}
	}
	return Decision{}, false
}

func resolvedCapabilityFollowUps(evidence []GoalEvidence) map[string]int {
	resolved := make(map[string]int)
	for i, item := range evidence {
		if !item.Passed {
			continue
		}
		payload, ok := goalCapabilityEvidencePayloadFrom(item)
		if !ok || !payload.hasResolutionProof() {
			continue
		}
		for _, id := range payload.supersededEvidenceIDs() {
			resolved[id] = i
		}
	}
	return resolved
}

func firstNonEmptyCapabilityFollowUp(nextAction string, groups ...[]string) string {
	if text := actionableCapabilityText(nextAction); text != "" {
		return text
	}
	for _, group := range groups {
		for _, value := range group {
			if text := actionableCapabilityText(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func (e ModelClassifiedEvaluator) Evaluate(ctx context.Context, goal Goal, result TurnResult, recent []Event) Decision {
	base := e.Base
	deterministic := base.Evaluate(ctx, goal, result, recent)
	if !shouldClassifyGoalDecision(goal, result, deterministic) || e.Classifier == nil {
		return deterministic
	}
	output, err := e.Classifier.ClassifyGoal(ctx, goal, BuildClassificationPrompt(goal, result, recent, deterministic))
	if err != nil {
		return deterministic
	}
	decision, err := ParseClassificationDecision(output)
	if err != nil {
		return deterministic
	}
	return decision
}

func shouldClassifyGoalDecision(goal Goal, result TurnResult, decision Decision) bool {
	if decision.Status != DecisionContinue || result.Error != nil || result.ContextDone {
		return false
	}
	if goal.TurnBudget > 0 && goal.TurnsUsed >= goal.TurnBudget {
		return false
	}
	if goal.TokenBudget > 0 && goal.InputTokens+goal.OutputTokens >= goal.TokenBudget {
		return false
	}
	if AnalyzeBudget(goal).Closing {
		return false
	}
	status, _ := parseGoalStatus(result.Response)
	return status == ""
}

func BuildClassificationPrompt(goal Goal, result TurnResult, recent []Event, deterministic Decision) string {
	var b strings.Builder
	b.WriteString("Classify the latest Goal Mode turn.\n")
	b.WriteString("Return exactly one JSON object with this schema and no markdown:\n")
	b.WriteString(`{"status":"continue|complete|blocked|failed","reason":"short reason","next_action":"short next action","blocker_key":"stable key or empty","confidence":0.0}`)
	b.WriteString("\n\nGoal:\n")
	fmt.Fprintf(&b, "- id: %s\n", goal.ID)
	fmt.Fprintf(&b, "- objective: %s\n", goal.Objective)
	fmt.Fprintf(&b, "- current_status: %s\n", goal.Status)
	fmt.Fprintf(&b, "- turns_used: %d\n", goal.TurnsUsed)
	fmt.Fprintf(&b, "- turn_budget: %d\n", goal.TurnBudget)
	fmt.Fprintf(&b, "- tokens_used: %d\n", goal.InputTokens+goal.OutputTokens)
	fmt.Fprintf(&b, "- token_budget: %d\n", goal.TokenBudget)
	if goal.LastReason != "" {
		fmt.Fprintf(&b, "- last_reason: %s\n", goal.LastReason)
	}
	if goal.LastNextAction != "" {
		fmt.Fprintf(&b, "- last_next_action: %s\n", goal.LastNextAction)
	}
	if goal.LastBlocker != "" {
		fmt.Fprintf(&b, "- last_blocker: %s\n", goal.LastBlocker)
	}
	b.WriteString("\nDeterministic fallback decision:\n")
	writeDecisionFields(&b, deterministic)
	if len(recent) > 0 {
		b.WriteString("\nRecent events:\n")
		for _, event := range recent {
			fmt.Fprintf(&b, "- type=%s turn=%d status=%s reason=%s next_action=%s blocker=%s error=%s\n", event.Type, event.Turn, event.Status, event.Reason, event.NextAction, event.BlockerKey, event.Error)
		}
	}
	b.WriteString("\nLatest turn result:\n")
	fmt.Fprintf(&b, "- stop_reason: %s\n", result.StopReason)
	fmt.Fprintf(&b, "- turns: %d\n", result.Turns)
	fmt.Fprintf(&b, "- input_tokens: %d\n", result.InputTokens)
	fmt.Fprintf(&b, "- output_tokens: %d\n", result.OutputTokens)
	fmt.Fprintf(&b, "- tool_errors: %d\n", result.ToolErrors)
	b.WriteString("- response:\n")
	b.WriteString(result.Response)
	b.WriteString("\n\nDecision rules:\n")
	b.WriteString("- complete only when the objective is genuinely satisfied and no required work remains.\n")
	b.WriteString("- blocked only for a real blocker that prevents meaningful next progress.\n")
	b.WriteString("- failed only for unrecoverable internal or corrupt state.\n")
	b.WriteString("- otherwise continue with a concrete next_action.\n")
	return b.String()
}

func writeDecisionFields(b *strings.Builder, decision Decision) {
	fmt.Fprintf(b, "- status: %s\n", decision.Status)
	fmt.Fprintf(b, "- reason: %s\n", decision.Reason)
	fmt.Fprintf(b, "- next_action: %s\n", decision.NextAction)
	fmt.Fprintf(b, "- blocker_key: %s\n", decision.BlockerKey)
	fmt.Fprintf(b, "- confidence: %.2f\n", decision.Confidence)
}

func ParseClassificationDecision(output string) (Decision, error) {
	output = strings.TrimSpace(output)
	output = strings.TrimPrefix(output, "```json")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)
	var decision Decision
	if err := json.Unmarshal([]byte(output), &decision); err != nil {
		return Decision{}, err
	}
	decision.Status = DecisionStatus(strings.ToLower(strings.TrimSpace(string(decision.Status))))
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.NextAction = strings.TrimSpace(decision.NextAction)
	decision.BlockerKey = blockerKey(decision.BlockerKey)
	if decision.Confidence < 0 {
		decision.Confidence = 0
	}
	if decision.Confidence > 1 {
		decision.Confidence = 1
	}
	switch decision.Status {
	case DecisionContinue:
		if decision.Reason == "" {
			decision.Reason = "model classified goal as active"
		}
		if decision.NextAction == "" {
			decision.NextAction = "continue next goal turn"
		}
	case DecisionComplete:
		if decision.Reason == "" {
			decision.Reason = "model classified objective complete"
		}
		decision.BlockerKey = ""
	case DecisionBlocked:
		if decision.Reason == "" {
			decision.Reason = "model classified goal as blocked"
		}
		if decision.BlockerKey == "" {
			decision.BlockerKey = "model_classified_blocker"
		}
	case DecisionFailed:
		if decision.Reason == "" {
			decision.Reason = "model classified goal as failed"
		}
	default:
		return Decision{}, fmt.Errorf("invalid goal classification status: %s", decision.Status)
	}
	return decision, nil
}

func ApplyDecision(goal Goal, decision Decision) Goal {
	decision.BlockerKey = strings.TrimSpace(decision.BlockerKey)
	goal.LastReason = strings.TrimSpace(decision.Reason)
	goal.LastNextAction = strings.TrimSpace(decision.NextAction)
	switch decision.Status {
	case DecisionComplete:
		goal.Status = StatusComplete
		goal.LastBlocker = ""
		goal.RepeatedBlockerCount = 0
	case DecisionBlocked:
		goal.Status = StatusBlocked
		if decision.BlockerKey != "" {
			if decision.BlockerKey == goal.LastBlocker {
				goal.RepeatedBlockerCount++
			} else {
				goal.LastBlocker = decision.BlockerKey
				goal.RepeatedBlockerCount = 1
			}
		}
	case DecisionFailed:
		goal.Status = StatusFailed
	default:
		goal.Status = StatusActive
		if decision.BlockerKey != "" {
			if decision.BlockerKey == goal.LastBlocker {
				goal.RepeatedBlockerCount++
			} else {
				goal.LastBlocker = decision.BlockerKey
				goal.RepeatedBlockerCount = 1
			}
		} else {
			goal.LastBlocker = ""
			goal.RepeatedBlockerCount = 0
		}
	}
	return goal
}

func blockerKey(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 120 {
		text = text[:120]
	}
	return text
}

func hardBlocker(key string) bool {
	return containsAny(key, "permission denied", "authentication", "api key", "quota", "account", "external dependency")
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func parseGoalStatus(response string) (string, string) {
	var status string
	var blocker string
	for _, line := range strings.Split(response, "\n") {
		key, value, ok := parseProtocolLine(line)
		if !ok {
			continue
		}
		switch key {
		case "goal_status":
			normalizedStatus := normalizeGoalStatusValue(value)
			switch normalizedStatus {
			case "complete", "blocked":
				status = normalizedStatus
			}
		case "blocker_key", "blocker":
			blocker = blockerKey(value)
		}
	}
	return status, blocker
}

func parseProtocolLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", false
	}
	line = trimProtocolLinePrefix(line)
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key := strings.ToLower(trimProtocolToken(line[:idx]))
	value := strings.ToLower(trimProtocolToken(line[idx+1:]))
	if value == "" {
		return "", "", false
	}
	return key, value, true
}

func trimProtocolLinePrefix(line string) string {
	line = strings.TrimSpace(line)
	for {
		next := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(next, ">"):
			line = strings.TrimSpace(strings.TrimPrefix(next, ">"))
		case strings.HasPrefix(next, "- "):
			line = strings.TrimSpace(strings.TrimPrefix(next, "- "))
		case strings.HasPrefix(next, "* "):
			line = strings.TrimSpace(strings.TrimPrefix(next, "* "))
		default:
			return next
		}
	}
}

func trimProtocolToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "`*_")
	return strings.TrimSpace(value)
}

func normalizeGoalStatusValue(value string) string {
	value = trimProtocolToken(value)
	if value == "" {
		return ""
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	head := strings.Trim(fields[0], "`*_.,;:!?-—–")
	return strings.ToLower(strings.TrimSpace(head))
}
