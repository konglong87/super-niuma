package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/capabilityloop"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

type QueryRunner interface {
	RunGoalTurn(ctx context.Context, goal Goal, prompt string, checkpointName string) (TurnResult, error)
}

const defaultPromptEvidenceLimit = 5

type Runner struct {
	Store            Store
	Query            QueryRunner
	Evaluator        Evaluator
	Planner          Planner
	Reachability     ReachabilityAnalyzer
	RecentEventLimit int
	Now              func() time.Time
}

type RunResult struct {
	Goal     Goal
	Decision Decision
	Event    Event
}

type TurnPromptContext struct {
	Plan         GoalPlan
	Evidence     []GoalEvidence
	RecentEvents []Event
	Budget       BudgetPolicy
}

func (r *Runner) RunOnce(ctx context.Context, goalID string) (RunResult, error) {
	if r.Store == nil {
		return RunResult{}, errors.New("goal store is required")
	}
	if r.Query == nil {
		return RunResult{}, errors.New("goal query runner is required")
	}
	if locker, ok := r.Store.(GoalLocker); ok {
		unlock, err := locker.LockGoal(ctx, goalID)
		if err != nil {
			return RunResult{}, err
		}
		defer unlock()
	}
	evaluator := r.Evaluator
	if evaluator == nil {
		evaluator = DeterministicEvaluator{}
	}
	goal, err := r.Store.Get(ctx, goalID)
	if err != nil {
		return RunResult{}, err
	}
	if goal.Status != StatusActive {
		return RunResult{}, fmt.Errorf("goal %s is %s", goal.ID, goal.Status)
	}
	plan, hasPlanStore, err := r.ensurePlan(ctx, goal)
	if err != nil {
		return RunResult{}, err
	}
	if hasPlanStore {
		if result, blocked, err := r.applyReachability(ctx, goal, plan); err != nil || blocked {
			return result, err
		}
	}
	budget := AnalyzeBudget(goal)
	if budget.Exhausted {
		return r.blockBeforeTurn(ctx, goal, Decision{
			Status:     DecisionBlocked,
			Reason:     budget.Reason,
			NextAction: budget.NextAction,
			BlockerKey: budget.BlockerKey,
			Confidence: 1,
		}, "goal blocked by budget policy")
	}
	promptContext, err := r.turnPromptContext(ctx, goal.ID, plan, hasPlanStore)
	if err != nil {
		return RunResult{}, err
	}
	promptContext.Budget = budget
	turn := goal.TurnsUsed + 1
	checkpoint := fmt.Sprintf("goal:%s:turn:%d", goal.ID, turn)
	startedAt := r.now()
	started, err := NewEvent(EventInput{
		GoalID:     goal.ID,
		Type:       EventTurnStarted,
		Message:    "goal turn started",
		SessionID:  goal.SessionID,
		Turn:       turn,
		Checkpoint: checkpoint,
		Status:     goal.Status,
		Now:        startedAt,
	})
	if err != nil {
		return RunResult{}, err
	}
	if err := r.Store.AppendEvent(ctx, started); err != nil {
		return RunResult{}, err
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "goal.turn.started",
		Category:     telemetry.CategoryAgent,
		Source:       "goal.Runner.RunOnce",
		Status:       telemetry.StatusStarted,
		ResourceType: "goal",
		ResourceID:   goal.ID,
		Properties: map[string]any{
			"turn":       turn,
			"session_id": goal.SessionID,
			"cwd":        goal.CWD,
		},
	})
	recentBeforeTurn, _ := r.Store.ListEvents(ctx, goal.ID, r.recentEventLimit())
	promptContext.RecentEvents = recentBeforeTurn
	prompt := BuildTurnPromptWithContext(goal, promptContext)
	result, runErr := r.Query.RunGoalTurn(ctx, goal, prompt, checkpoint)
	if runErr != nil && result.Error == nil {
		result.Error = runErr
	}
	result.ContextDone = result.ContextDone || errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled)
	goal.TurnsUsed++
	goal.InputTokens += result.InputTokens
	goal.OutputTokens += result.OutputTokens
	actualCheckpoint := ""
	if result.CheckpointCreated {
		actualCheckpoint = firstNonEmpty(result.CheckpointName, checkpoint)
		goal.LastCheckpoint = actualCheckpoint
	}
	goal.Error = ""
	if runErr != nil && !result.ContextDone {
		goal.Error = runErr.Error()
	}
	if err := r.appendTurnEvidence(ctx, goal.ID, result.Evidence); err != nil {
		return RunResult{}, err
	}
	promptContext, err = r.applyDefaultCompletionEvidence(ctx, goal.ID, promptContext, result)
	if err != nil {
		return RunResult{}, err
	}
	current, currentErr := r.Store.Get(ctx, goal.ID)
	if currentErr == nil && current.Status == StatusStopped {
		current.TurnsUsed = goal.TurnsUsed
		current.InputTokens = goal.InputTokens
		current.OutputTokens = goal.OutputTokens
		current.LastCheckpoint = firstNonEmpty(goal.LastCheckpoint, current.LastCheckpoint)
		current.Error = goal.Error
		current.LastReason = firstNonEmpty(current.LastReason, "stopped by user")
		current.UpdatedAt = r.now()
		event, err := NewEvent(EventInput{
			GoalID:       current.ID,
			Type:         EventTurnFinished,
			Message:      "goal turn finished after stop request",
			SessionID:    current.SessionID,
			Turn:         turn,
			InputTokens:  result.InputTokens,
			OutputTokens: result.OutputTokens,
			DurationMS:   time.Since(startedAt).Milliseconds(),
			Checkpoint:   actualCheckpoint,
			Status:       current.Status,
			Reason:       current.LastReason,
			Error:        errorString(runErr),
			Now:          current.UpdatedAt,
		})
		if err != nil {
			return RunResult{}, err
		}
		if updateErr := r.Store.Update(ctx, current); updateErr != nil {
			return RunResult{}, updateErr
		}
		if appendErr := r.Store.AppendEvent(ctx, event); appendErr != nil {
			return RunResult{}, appendErr
		}
		return RunResult{Goal: current, Decision: Decision{Status: DecisionContinue, Reason: current.LastReason}, Event: event}, nil
	}
	recent, _ := r.Store.ListEvents(ctx, goal.ID, r.recentEventLimit())
	decision := r.evaluateTurn(ctx, evaluator, goal, result, recent, promptContext)
	goal = ApplyDecision(goal, decision)
	goal.UpdatedAt = r.now()
	if err := r.applyCompletedPlanStatus(ctx, promptContext, decision); err != nil {
		return RunResult{}, err
	}
	eventType := EventTurnFinished
	if runErr != nil && !result.ContextDone {
		eventType = EventTurnFailed
	}
	status := telemetry.StatusOK
	if goal.Status == StatusBlocked {
		status = telemetry.StatusBlocked
	}
	if goal.Status == StatusFailed || (runErr != nil && !result.ContextDone) {
		status = telemetry.StatusError
	}
	event, err := NewEvent(EventInput{
		GoalID:       goal.ID,
		Type:         eventType,
		Message:      eventMessage(decision, runErr),
		SessionID:    goal.SessionID,
		Turn:         turn,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		DurationMS:   time.Since(startedAt).Milliseconds(),
		Checkpoint:   actualCheckpoint,
		Status:       goal.Status,
		Reason:       decision.Reason,
		NextAction:   decision.NextAction,
		BlockerKey:   decision.BlockerKey,
		Error:        errorString(runErr),
		Now:          goal.UpdatedAt,
	})
	if err != nil {
		return RunResult{}, err
	}
	if updateErr := r.Store.Update(ctx, goal); updateErr != nil {
		return RunResult{}, updateErr
	}
	if appendErr := r.Store.AppendEvent(ctx, event); appendErr != nil {
		return RunResult{}, appendErr
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "goal.turn.finished",
		Category:     telemetry.CategoryAgent,
		Source:       "goal.Runner.RunOnce",
		Status:       status,
		ResourceType: "goal",
		ResourceID:   goal.ID,
		DurationMS:   event.DurationMS,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		Error:        errorString(runErr),
		Properties: map[string]any{
			"turn":        turn,
			"goal_status": string(goal.Status),
			"checkpoint":  actualCheckpoint,
			"session_id":  goal.SessionID,
		},
	})
	if runErr != nil && goal.Status == StatusFailed {
		observability.Error(ctx, nil, "goal.turn.failed", "goal.Runner.RunOnce", "goal turn failed", "goal_id", goal.ID, "error", runErr)
	}
	return RunResult{Goal: goal, Decision: decision, Event: event}, nil
}

func (r *Runner) RunUntilStop(ctx context.Context, goalID string) (Goal, error) {
	var last Goal
	for {
		result, err := r.RunOnce(ctx, goalID)
		if err != nil {
			return last, err
		}
		last = result.Goal
		if last.Status != StatusActive {
			return last, nil
		}
		if err := ctx.Err(); err != nil {
			return last, err
		}
	}
}

func BuildTurnPrompt(goal Goal, recent []Event) string {
	return BuildTurnPromptWithContext(goal, TurnPromptContext{RecentEvents: recent})
}

func BuildTurnPromptWithContext(goal Goal, context TurnPromptContext) string {
	var b bytes.Buffer
	b.WriteString("You are running in niuma Goal Mode.\n\n")
	b.WriteString("Objective:\n")
	b.WriteString(goal.Objective)
	b.WriteString("\n\nCurrent goal state:\n")
	fmt.Fprintf(&b, "- goal_id: %s\n", goal.ID)
	fmt.Fprintf(&b, "- status: %s\n", goal.Status)
	fmt.Fprintf(&b, "- turns: %d/%d\n", goal.TurnsUsed, goal.TurnBudget)
	fmt.Fprintf(&b, "- tokens: %d/%d\n", goal.InputTokens+goal.OutputTokens, goal.TokenBudget)
	if goal.LastCheckpoint != "" {
		fmt.Fprintf(&b, "- last_checkpoint: %s\n", goal.LastCheckpoint)
	}
	if goal.LastNextAction != "" {
		fmt.Fprintf(&b, "- last_next_action: %s\n", goal.LastNextAction)
	}
	writePlanPromptContext(&b, context.Plan)
	writeEvidencePromptContext(&b, context.Evidence)
	writeBudgetPromptContext(&b, context.Budget)
	if len(context.RecentEvents) > 0 {
		b.WriteString("\nRecent goal events:\n")
		for _, event := range context.RecentEvents {
			fmt.Fprintf(&b, "- turn=%d type=%s status=%s reason=%s next_action=%s blocker=%s error=%s\n", event.Turn, event.Type, event.Status, event.Reason, event.NextAction, event.BlockerKey, event.Error)
		}
	}
	b.WriteString("\nRules:\n")
	b.WriteString("- Execute exactly one useful agent turn toward the objective.\n")
	b.WriteString("- Use existing project tools, tests, checkpoints, and docs where appropriate.\n")
	b.WriteString("- Do not mark complete unless the objective is genuinely satisfied and no required work remains.\n")
	b.WriteString("- If blocked, state the blocker clearly and include `GOAL_STATUS: blocked` and `blocker_key: <stable-key>`.\n")
	b.WriteString("- If complete, include `GOAL_STATUS: complete` in the final response.\n")
	b.WriteString("- Otherwise continue making progress without asking for permission unless required by policy.\n")
	return b.String()
}

func writeBudgetPromptContext(b *bytes.Buffer, budget BudgetPolicy) {
	if !budget.Closing {
		return
	}
	b.WriteString("\nBudget closing policy:\n")
	fmt.Fprintf(b, "- turns_remaining: %d\n", budget.TurnsRemaining)
	fmt.Fprintf(b, "- tokens_remaining: %d\n", budget.TokensRemaining)
	b.WriteString("- Use this turn for verification, concise summary, completion, or a clear blocker.\n")
	b.WriteString("- Do not expand scope or start speculative work that cannot be closed inside the remaining budget.\n")
}

func writePlanPromptContext(b *bytes.Buffer, plan GoalPlan) {
	if strings.TrimSpace(plan.GoalID) == "" {
		return
	}
	if current, ok := currentPlanStep(plan); ok {
		b.WriteString("\nCurrent plan step:\n")
		fmt.Fprintf(b, "- id: %s\n", current.ID)
		fmt.Fprintf(b, "- title: %s\n", current.Title)
		fmt.Fprintf(b, "- status: %s\n", current.Status)
		if current.Rationale != "" {
			fmt.Fprintf(b, "- rationale: %s\n", current.Rationale)
		}
	}
	pending := pendingCriteria(plan)
	if len(pending) > 0 {
		b.WriteString("\nUnpassed acceptance criteria:\n")
		for _, criterion := range pending {
			required := "optional"
			if criterion.Required {
				required = "required"
			}
			fmt.Fprintf(b, "- %s [%s status=%s]: %s\n", criterion.ID, required, criterion.Status, criterion.Description)
		}
	}
}

func writeEvidencePromptContext(b *bytes.Buffer, evidence []GoalEvidence) {
	if len(evidence) == 0 {
		return
	}
	b.WriteString("\nRecent evidence:\n")
	for _, item := range evidence {
		exit := ""
		if item.ExitCode != nil {
			exit = fmt.Sprintf(" exit_code=%d", *item.ExitCode)
		}
		fmt.Fprintf(b, "- %s type=%s passed=%v%s summary=%s\n", item.ID, item.Type, item.Passed, exit, item.Summary)
		writeCapabilityLoopEvidencePromptContext(b, item)
	}
	writeGoalCapabilityFollowUpGatePromptContext(b, evidence)
}

type goalCapabilityEvidencePayload struct {
	EvidenceSource        string              `json:"evidence_source"`
	AgentTaskID           any                 `json:"agent_task_id"`
	AgentStatus           string              `json:"agent_status"`
	PartialEvidence       bool                `json:"partial_evidence"`
	CapabilityLoop        agentCapabilityLoop `json:"capability_loop"`
	ResolvedFollowUp      string              `json:"resolved_follow_up"`
	SupersedesEvidenceID  string              `json:"supersedes_evidence_id"`
	SupersedesEvidenceIDs []string            `json:"supersedes_evidence_ids"`
}

func goalCapabilityEvidencePayloadFrom(item GoalEvidence) (goalCapabilityEvidencePayload, bool) {
	if len(item.Payload) == 0 {
		return goalCapabilityEvidencePayload{}, false
	}
	var payload goalCapabilityEvidencePayload
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return goalCapabilityEvidencePayload{}, false
	}
	payload.CapabilityLoop = payload.CapabilityLoop.normalized()
	payload.ResolvedFollowUp = actionableCapabilityText(payload.ResolvedFollowUp)
	payload.SupersedesEvidenceID = strings.TrimSpace(payload.SupersedesEvidenceID)
	payload.SupersedesEvidenceIDs = normalizedEvidenceIDs(payload.SupersedesEvidenceIDs)
	if !payload.CapabilityLoop.hasSignal() && !payload.hasResolutionSignal() {
		return goalCapabilityEvidencePayload{}, false
	}
	return payload, true
}

func writeCapabilityLoopEvidencePromptContext(b *bytes.Buffer, item GoalEvidence) {
	payload, ok := goalCapabilityEvidencePayloadFrom(item)
	if !ok {
		return
	}
	loop := payload.CapabilityLoop
	source := strings.TrimSpace(payload.EvidenceSource)
	if source == "" {
		source = "unknown"
	}
	if loop.hasSignal() {
		fmt.Fprintf(b, "  capability_loop: evidence_source=%s agent_task_id=%v agent_status=%s partial_evidence=%v\n", source, payload.AgentTaskID, payload.AgentStatus, payload.PartialEvidence)
		writeCapabilityLoopPromptValues(b, "evidence", loop.Evidence)
		writeCapabilityLoopPromptValues(b, "assumptions", loop.Assumptions)
		writeCapabilityLoopPromptValues(b, "unknowns", loop.Unknowns)
		writeCapabilityLoopPromptValues(b, "verification", loop.Verification)
		writeCapabilityLoopPromptValues(b, "risks", loop.Risks)
		if next := actionableCapabilityText(loop.NextAction); next != "" {
			fmt.Fprintf(b, "  next_action: %s\n", trimEvidenceText(next, 240))
		}
	}
	if payload.hasResolutionSignal() {
		ids := payload.supersededEvidenceIDs()
		fmt.Fprintf(b, "  capability_follow_up_resolution: resolved_follow_up=%s supersedes_evidence_id=%s\n", trimEvidenceText(payload.ResolvedFollowUp, 240), strings.Join(ids, ","))
	}
}

func writeGoalCapabilityFollowUpGatePromptContext(b *bytes.Buffer, evidence []GoalEvidence) {
	lines := []string{
		"## Goal capability follow-up gate",
		"- Do not mark GOAL_STATUS: complete until each listed sub-agent next_action is executed, superseded by newer evidence or user intent, or carried into residual risk.",
		"- If verification is listed, either run it, cite an equivalent verification already run, or say why it remains unverified before the final response.",
	}
	resolved := resolvedCapabilityFollowUps(evidence)
	pendingLines := 0
	for i := len(evidence) - 1; i >= 0 && pendingLines < 3; i-- {
		if resolvedIndex, ok := resolved[evidence[i].ID]; ok && resolvedIndex > i {
			continue
		}
		if line := goalCapabilityFollowUpLine(evidence[i]); line != "" {
			lines = append(lines, line)
			pendingLines++
		}
	}
	if len(lines) == 3 {
		return
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
}

func goalCapabilityFollowUpLine(item GoalEvidence) string {
	payload, ok := goalCapabilityEvidencePayloadFrom(item)
	if !ok {
		return ""
	}
	loop := payload.CapabilityLoop
	if capabilityPendingFollowUpText(payload) == "" {
		return ""
	}
	var parts []string
	if source := trimEvidenceText(strings.TrimSpace(payload.EvidenceSource), 80); source != "" {
		parts = append(parts, "evidence_source: "+source)
	}
	if action := goalCapabilitySourceActionHint(payload.EvidenceSource); action != "" {
		parts = append(parts, action)
	}
	if next := trimCapabilityPromptSignal(loop.NextAction, 180); next != "" {
		parts = append(parts, capabilityloop.FollowUpFieldNextAction+": "+next)
	}
	if verification := firstCapabilityPromptSignal(loop.Verification, 160); verification != "" {
		parts = append(parts, capabilityloop.FollowUpFieldVerification+": "+verification)
	}
	if unknown := firstCapabilityPromptSignal(loop.Unknowns, 140); unknown != "" {
		parts = append(parts, capabilityloop.FollowUpFieldUnknown+": "+unknown)
	}
	if risk := firstCapabilityPromptSignal(loop.Risks, 140); risk != "" {
		parts = append(parts, capabilityloop.FollowUpFieldRisk+": "+risk)
	}
	if len(parts) == 0 {
		return ""
	}
	status := trimEvidenceText(strings.TrimSpace(payload.AgentStatus), 32)
	if status == "" {
		status = "unknown"
	}
	prefix := fmt.Sprintf("- pending_goal_follow_up: %s passed=%v agent_status=%s partial_evidence=%v", item.ID, item.Passed, status, payload.PartialEvidence)
	if payload.AgentTaskID != nil {
		prefix += fmt.Sprintf(" agent_task_id=%v", payload.AgentTaskID)
	}
	if summary := trimEvidenceText(item.Summary, 120); summary != "" {
		prefix += ": " + summary
	}
	return prefix + "; " + strings.Join(parts, " | ")
}

func (payload goalCapabilityEvidencePayload) hasResolutionSignal() bool {
	return payload.ResolvedFollowUp != "" && len(payload.supersededEvidenceIDs()) > 0
}

func (payload goalCapabilityEvidencePayload) hasResolutionProof() bool {
	if !payload.hasResolutionSignal() {
		return false
	}
	return firstActionableCapabilityText(payload.CapabilityLoop.Evidence...) != "" ||
		firstActionableCapabilityText(payload.CapabilityLoop.Verification...) != ""
}

func capabilityPendingFollowUpText(payload goalCapabilityEvidencePayload) string {
	loop := payload.CapabilityLoop
	if payload.hasResolutionSignal() {
		return firstNonEmptyCapabilityFollowUp(loop.NextAction, loop.Unknowns, loop.Risks)
	}
	return firstNonEmptyCapabilityFollowUp(loop.NextAction, loop.Verification, loop.Unknowns, loop.Risks)
}

func (payload goalCapabilityEvidencePayload) supersededEvidenceIDs() []string {
	ids := make([]string, 0, 1+len(payload.SupersedesEvidenceIDs))
	if id := strings.TrimSpace(payload.SupersedesEvidenceID); id != "" {
		ids = append(ids, id)
	}
	ids = append(ids, normalizedEvidenceIDs(payload.SupersedesEvidenceIDs)...)
	if len(ids) <= 1 {
		return ids
	}
	seen := make(map[string]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func normalizedEvidenceIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstCapabilityPromptSignal(values []string, limit int) string {
	for _, value := range values {
		if signal := trimCapabilityPromptSignal(value, limit); signal != "" {
			return signal
		}
	}
	return ""
}

func trimCapabilityPromptSignal(value string, limit int) string {
	if text := actionableCapabilityText(value); text != "" {
		return trimEvidenceText(text, limit)
	}
	return ""
}

func goalCapabilitySourceActionHint(source string) string {
	switch strings.TrimSpace(source) {
	case "agent_get":
		return "source_action: AgentGet result was explicitly retrieved; use it as the current sub-agent result, but still run or disclose listed verification and risks before completing the goal."
	case "terminal_agent_task_store":
		return "source_action: This is fallback terminal task-store evidence; inspect available artifacts or rerun verification before relying on it as complete."
	case "task_tool", "agent_tool":
		return "source_action: Synchronous tool_result evidence is already in the goal turn; carry it forward and handle next_action before completing the goal."
	case "compact_summary":
		return "source_action: This evidence was recovered from compact summary; treat it as condensed context and re-verify or disclose compaction limits for unresolved unknowns/risks."
	default:
		return ""
	}
}

func writeCapabilityLoopPromptValues(b *bytes.Buffer, label string, values []string) {
	for _, value := range values {
		if text := actionableCapabilityText(value); text != "" {
			fmt.Fprintf(b, "  %s: %s\n", label, trimEvidenceText(text, 240))
		}
	}
}

func currentPlanStep(plan GoalPlan) (GoalStep, bool) {
	if plan.CurrentStepID != "" {
		for _, step := range plan.Steps {
			if step.ID == plan.CurrentStepID {
				return step, true
			}
		}
	}
	for _, step := range plan.Steps {
		if step.Status == StepStatusActive {
			return step, true
		}
	}
	return GoalStep{}, false
}

func pendingCriteria(plan GoalPlan) []GoalCriterion {
	var out []GoalCriterion
	for _, criterion := range plan.AcceptanceCriteria {
		switch criterion.Status {
		case CriterionStatusPassed, CriterionStatusWaived:
			continue
		default:
			out = append(out, criterion)
		}
	}
	return out
}

func (r *Runner) recentEventLimit() int {
	if r.RecentEventLimit > 0 {
		return r.RecentEventLimit
	}
	return DefaultRecentEventLimit
}

func (r *Runner) ensurePlan(ctx context.Context, item Goal) (GoalPlan, bool, error) {
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return GoalPlan{}, false, nil
	}
	if plan, exists, err := planStore.GetPlan(ctx, item.ID); err != nil || exists {
		return plan, true, err
	}
	planner := r.Planner
	if planner == nil {
		planner = DeterministicPlanner{}
	}
	plan, err := planner.BuildInitialPlan(item, r.now())
	if err != nil {
		return GoalPlan{}, true, err
	}
	if err := planStore.SavePlan(ctx, plan); err != nil {
		return GoalPlan{}, true, err
	}
	return plan, true, nil
}

func (r *Runner) applyReachability(ctx context.Context, item Goal, plan GoalPlan) (RunResult, bool, error) {
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return RunResult{}, false, nil
	}
	analyzer := r.Reachability
	if analyzer == nil {
		analyzer = DeterministicReachabilityAnalyzer{}
	}
	result, err := analyzer.Analyze(ctx, item, plan)
	if err != nil {
		return RunResult{}, false, err
	}
	plan = ApplyReachability(plan, result, r.now())
	if err := planStore.SavePlan(ctx, plan); err != nil {
		return RunResult{}, false, err
	}
	if !result.Blocked {
		return RunResult{}, false, nil
	}
	blocked, err := r.blockBeforeTurn(ctx, item, Decision{
		Status:     DecisionBlocked,
		Reason:     result.Reason,
		NextAction: "resolve reachability blocker before running the next goal turn",
		BlockerKey: result.BlockerKey,
		Confidence: 1,
	}, "goal blocked by reachability analysis")
	return blocked, true, err
}

func (r *Runner) blockBeforeTurn(ctx context.Context, item Goal, decision Decision, message string) (RunResult, error) {
	item = ApplyDecision(item, decision)
	item.UpdatedAt = r.now()
	event, err := NewEvent(EventInput{
		GoalID:     item.ID,
		Type:       EventStatusChanged,
		Message:    message,
		SessionID:  item.SessionID,
		Status:     item.Status,
		Reason:     decision.Reason,
		NextAction: decision.NextAction,
		BlockerKey: decision.BlockerKey,
		Now:        item.UpdatedAt,
	})
	if err != nil {
		return RunResult{}, err
	}
	if err := r.Store.Update(ctx, item); err != nil {
		return RunResult{}, err
	}
	if err := r.Store.AppendEvent(ctx, event); err != nil {
		return RunResult{}, err
	}
	return RunResult{Goal: item, Decision: decision, Event: event}, nil
}

func (r *Runner) turnPromptContext(ctx context.Context, goalID string, plan GoalPlan, hasPlanStore bool) (TurnPromptContext, error) {
	context := TurnPromptContext{Plan: plan}
	if !hasPlanStore {
		return context, nil
	}
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return context, nil
	}
	evidence, err := planStore.ListEvidence(ctx, goalID, defaultPromptEvidenceLimit)
	if err != nil {
		return TurnPromptContext{}, err
	}
	context.Evidence = evidence
	return context, nil
}

func (r *Runner) appendTurnEvidence(ctx context.Context, goalID string, evidence []GoalEvidence) error {
	if len(evidence) == 0 {
		return nil
	}
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return nil
	}
	for _, item := range evidence {
		if strings.TrimSpace(item.GoalID) == "" {
			item.GoalID = goalID
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = r.now()
		}
		if err := planStore.AppendEvidence(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) applyDefaultCompletionEvidence(ctx context.Context, goalID string, promptContext TurnPromptContext, result TurnResult) (TurnPromptContext, error) {
	status, _ := parseGoalStatus(result.Response)
	if status != "complete" || !hasPendingDefaultObjectiveCriterion(promptContext.Plan) {
		return promptContext, nil
	}
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return promptContext, nil
	}
	evidenceID, err := newPrefixedID("ev", defaultEventIDByteLength)
	if err != nil {
		return TurnPromptContext{}, err
	}
	now := r.now()
	evidence := GoalEvidence{
		ID:        evidenceID,
		GoalID:    goalID,
		Type:      EvidenceTypeManual,
		Summary:   "Goal completion protocol reported objective complete",
		Passed:    true,
		CreatedAt: now,
	}
	if err := planStore.AppendEvidence(ctx, evidence); err != nil {
		return TurnPromptContext{}, err
	}
	plan := promptContext.Plan
	plan.AcceptanceCriteria[0].Status = CriterionStatusPassed
	plan.AcceptanceCriteria[0].EvidenceIDs = appendUniqueString(plan.AcceptanceCriteria[0].EvidenceIDs, evidenceID)
	plan.UpdatedAt = now
	if err := planStore.SavePlan(ctx, plan); err != nil {
		return TurnPromptContext{}, err
	}
	promptContext.Plan = plan
	promptContext.Evidence = append(promptContext.Evidence, evidence)
	return promptContext, nil
}

func hasPendingDefaultObjectiveCriterion(plan GoalPlan) bool {
	if len(plan.AcceptanceCriteria) != 1 {
		return false
	}
	criterion := plan.AcceptanceCriteria[0]
	return criterion.ID == "crit_objective_satisfied" && criterion.Required && criterion.Status == CriterionStatusPending
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func (r *Runner) applyCompletedPlanStatus(ctx context.Context, promptContext TurnPromptContext, decision Decision) error {
	if decision.Status != DecisionComplete || strings.TrimSpace(promptContext.Plan.GoalID) == "" {
		return nil
	}
	planStore, ok := r.Store.(PlanStore)
	if !ok {
		return nil
	}
	plan := promptContext.Plan
	changed := false
	for i := range plan.Steps {
		switch plan.Steps[i].Status {
		case StepStatusPending, StepStatusActive, StepStatusBlocked:
			plan.Steps[i].Status = StepStatusDone
			changed = true
		}
	}
	if plan.CurrentStepID != "" {
		plan.CurrentStepID = ""
		changed = true
	}
	if !changed {
		return nil
	}
	plan.UpdatedAt = r.now()
	return planStore.SavePlan(ctx, plan)
}

func (r *Runner) evaluateTurn(ctx context.Context, evaluator Evaluator, item Goal, result TurnResult, recent []Event, promptContext TurnPromptContext) Decision {
	evidence := append([]GoalEvidence(nil), promptContext.Evidence...)
	evidence = append(evidence, result.Evidence...)
	return EvidenceEvaluator{
		Base:     evaluator,
		Plan:     promptContext.Plan,
		Evidence: evidence,
	}.Evaluate(ctx, item, result, recent)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func eventMessage(decision Decision, err error) string {
	if err != nil {
		return strings.TrimSpace(err.Error())
	}
	return firstNonEmpty(decision.Reason, "goal turn finished")
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
