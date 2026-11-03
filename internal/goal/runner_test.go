package goal

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeQueryRunner struct {
	prompt     string
	checkpoint string
	calls      int
	result     TurnResult
	err        error
	block      chan struct{}
	started    chan struct{}
}

func (f *fakeQueryRunner) RunGoalTurn(ctx context.Context, goal Goal, prompt string, checkpointName string) (TurnResult, error) {
	_ = ctx
	_ = goal
	f.calls++
	f.prompt = prompt
	f.checkpoint = checkpointName
	if f.started != nil {
		close(f.started)
	}
	if f.block != nil {
		<-f.block
	}
	return f.result, f.err
}

func TestRunnerRunOnceCreatesEventsAndUpdatesUsage(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "finish feature", SessionID: "session-1", CWD: t.TempDir(), TurnBudget: 5, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "progress", InputTokens: 10, OutputTokens: 5, CheckpointCreated: true}}
	runner := Runner{Store: store, Query: fake, Now: func() time.Time { return time.Now().UTC() }}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusActive || result.Goal.TurnsUsed != 1 || result.Goal.InputTokens != 10 || result.Goal.OutputTokens != 5 {
		t.Fatalf("goal = %+v", result.Goal)
	}
	if fake.checkpoint != "goal:"+created.ID+":turn:1" {
		t.Fatalf("checkpoint = %q", fake.checkpoint)
	}
	if !strings.Contains(fake.prompt, "Objective:") || !strings.Contains(fake.prompt, "finish feature") {
		t.Fatalf("prompt = %s", fake.prompt)
	}
	plan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if plan.GoalID != created.ID || plan.CurrentStepID != "step_execute" || len(plan.AcceptanceCriteria) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	events, err := store.ListEvents(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].Type != EventTurnStarted || events[2].Type != EventTurnFinished {
		t.Fatalf("events = %+v", events)
	}
}

func TestRunnerRunOnceDoesNotOverwriteExistingPlan(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "keep custom plan", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	custom := GoalPlan{
		GoalID:        created.ID,
		Version:       7,
		Summary:       "custom",
		CurrentStepID: "step_custom",
		Steps: []GoalStep{{
			ID:     "step_custom",
			Title:  "Custom step",
			Status: StepStatusActive,
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_custom",
			Description: "custom criterion",
			Required:    true,
			Status:      CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, custom); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "progress"}}
	runner := Runner{Store: store, Query: fake}
	if _, err := runner.RunOnce(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	plan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if plan.Version != 7 || plan.CurrentStepID != "step_custom" || plan.AcceptanceCriteria[0].ID != "crit_custom" {
		t.Fatalf("plan overwritten: %+v", plan)
	}
}

func TestRunnerRunOnceCompletesDefaultPlanFromMarkdownProtocolLine(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "read marker and finish", CWD: t.TempDir(), TurnBudget: 3, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "marker verified\n**GOAL_STATUS: complete**\n**GOAL_SMOKE_DONE**"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusComplete || result.Decision.Status != DecisionComplete {
		t.Fatalf("goal=%+v decision=%+v", result.Goal, result.Decision)
	}
	plan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if len(plan.AcceptanceCriteria) != 1 || plan.AcceptanceCriteria[0].Status != CriterionStatusPassed || len(plan.AcceptanceCriteria[0].EvidenceIDs) != 1 {
		t.Fatalf("criteria = %+v", plan.AcceptanceCriteria)
	}
	if plan.CurrentStepID != "" {
		t.Fatalf("completed goal should clear current step, plan = %+v", plan)
	}
	for _, step := range plan.Steps {
		if step.Status != StepStatusDone {
			t.Fatalf("completed default plan should mark steps done, step = %+v plan = %+v", step, plan)
		}
	}
	evidence, err := store.ListEvidence(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].Type != EvidenceTypeManual || !evidence[0].Passed || evidence[0].ID != plan.AcceptanceCriteria[0].EvidenceIDs[0] {
		t.Fatalf("evidence=%+v criteria=%+v", evidence, plan.AcceptanceCriteria)
	}
}

func TestBuildTurnPromptWithContextIncludesPlanEvidenceAndNextAction(t *testing.T) {
	exitCode := 0
	prompt := BuildTurnPromptWithContext(Goal{
		ID:             "goal_test",
		Objective:      "ship prompt context",
		Status:         StatusActive,
		TurnBudget:     3,
		TokenBudget:    1000,
		LastNextAction: "run focused tests",
	}, TurnPromptContext{
		Plan: GoalPlan{
			GoalID:        "goal_test",
			Version:       1,
			CurrentStepID: "step_verify",
			Steps: []GoalStep{{
				ID:        "step_verify",
				Title:     "Verify implementation",
				Status:    StepStatusActive,
				Rationale: "Evidence gates completion",
			}},
			AcceptanceCriteria: []GoalCriterion{{
				ID:          "crit_tests",
				Description: "Tests pass",
				Required:    true,
				Status:      CriterionStatusPending,
			}, {
				ID:          "crit_docs",
				Description: "Docs updated",
				Required:    true,
				Status:      CriterionStatusPassed,
			}},
		},
		Evidence: []GoalEvidence{{
			ID:       "ev_test",
			GoalID:   "goal_test",
			Type:     EvidenceTypeTest,
			Summary:  "go test ./internal/goal passed",
			ExitCode: &exitCode,
			Passed:   true,
		}},
		RecentEvents: []Event{{
			Type:       EventTurnFinished,
			Turn:       1,
			Status:     StatusActive,
			Reason:     "continue",
			NextAction: "run focused tests",
			BlockerKey: "temporary",
			Error:      "none",
		}},
	})
	for _, want := range []string{
		"last_next_action: run focused tests",
		"Current plan step:",
		"step_verify",
		"Unpassed acceptance criteria:",
		"crit_tests",
		"Recent evidence:",
		"ev_test",
		"next_action=run focused tests",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "crit_docs") {
		t.Fatalf("passed criterion should not be listed:\n%s", prompt)
	}
}

func TestBuildTurnPromptWithContextExpandsCapabilityLoopEvidence(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"evidence_source":  "terminal_agent_task_store",
		"agent_task_id":    42,
		"agent_status":     "failed",
		"partial_evidence": true,
		"capability_loop": map[string]any{
			"evidence":     []string{"sub-agent found root cause in provider fallback"},
			"assumptions":  []string{"fallback provider is configured"},
			"unknowns":     []string{"whether retry reset preserves request context"},
			"verification": []string{"rerun focused provider fallback acceptance"},
			"risks":        []string{"parent may otherwise continue with primary provider failure"},
			"next_action":  "patch fallback selection and rerun acceptance",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := BuildTurnPromptWithContext(Goal{
		ID:          "goal_agent",
		Objective:   "use sub-agent evidence",
		Status:      StatusActive,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, TurnPromptContext{
		Evidence: []GoalEvidence{{
			ID:      "ev_agent_task_42",
			GoalID:  "goal_agent",
			Type:    EvidenceTypeManual,
			Summary: "AgentGet failed partial evidence",
			Passed:  false,
			Payload: payload,
		}},
	})
	for _, want := range []string{
		"Recent evidence:",
		"ev_agent_task_42",
		"capability_loop: evidence_source=terminal_agent_task_store agent_task_id=42 agent_status=failed partial_evidence=true",
		"evidence: sub-agent found root cause in provider fallback",
		"assumptions: fallback provider is configured",
		"unknowns: whether retry reset preserves request context",
		"verification: rerun focused provider fallback acceptance",
		"risks: parent may otherwise continue with primary provider failure",
		"next_action: patch fallback selection and rerun acceptance",
		"## Goal capability follow-up gate",
		"pending_goal_follow_up: ev_agent_task_42 passed=false agent_status=failed partial_evidence=true agent_task_id=42",
		"evidence_source: terminal_agent_task_store",
		"source_action: This is fallback terminal task-store evidence",
		"must_handle_next_action: patch fallback selection and rerun acceptance",
		"verification_required: rerun focused provider fallback acceptance",
		"unknown_to_resolve_or_disclose: whether retry reset preserves request context",
		"risk_to_account_for: parent may otherwise continue with primary provider failure",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildTurnPromptWithContextSkipsCapabilityLoopPlaceholders(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"evidence_source":  "agent_get",
		"agent_task_id":    43,
		"agent_status":     "completed",
		"partial_evidence": false,
		"capability_loop": map[string]any{
			"evidence":     []string{"None observed", "Goal prompt includes only concrete evidence"},
			"assumptions":  []string{"None observed"},
			"unknowns":     []string{"None observed"},
			"verification": []string{"No explicit verification reported."},
			"risks":        []string{"N/A"},
			"next_action":  "rerun goal evidence verifier",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := BuildTurnPromptWithContext(Goal{
		ID:          "goal_placeholders",
		Objective:   "avoid fake follow-up actions",
		Status:      StatusActive,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, TurnPromptContext{
		Evidence: []GoalEvidence{{
			ID:      "ev_agent_task_43",
			GoalID:  "goal_placeholders",
			Type:    EvidenceTypeManual,
			Summary: "AgentGet capability evidence",
			Passed:  true,
			Payload: payload,
		}},
	})
	for _, want := range []string{
		"capability_loop: evidence_source=agent_get agent_task_id=43 agent_status=completed partial_evidence=false",
		"evidence: Goal prompt includes only concrete evidence",
		"next_action: rerun goal evidence verifier",
		"## Goal capability follow-up gate",
		"pending_goal_follow_up: ev_agent_task_43 passed=true agent_status=completed partial_evidence=false agent_task_id=43",
		"evidence_source: agent_get",
		"source_action: AgentGet result was explicitly retrieved",
		"must_handle_next_action: rerun goal evidence verifier",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, notWant := range []string{"None observed", "assumptions:", "unknowns:", "verification:", "risks:", "unknown_to_resolve_or_disclose:", "verification_required:", "risk_to_account_for:"} {
		if strings.Contains(prompt, notWant) {
			t.Fatalf("prompt contains placeholder %q:\n%s", notWant, prompt)
		}
	}
}

func TestBuildTurnPromptWithContextShowsCapabilityFollowUpResolution(t *testing.T) {
	prompt := BuildTurnPromptWithContext(Goal{
		ID:          "goal_resolution",
		Objective:   "resolve pending follow-up",
		Status:      StatusActive,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, TurnPromptContext{
		Evidence: []GoalEvidence{capabilityFollowUpResolutionEvidence(t, "goal_resolution", "ev_capability_follow_up", true)},
	})
	for _, want := range []string{
		"Recent evidence:",
		"ev_capability_follow_up_resolution",
		"capability_loop: evidence_source=manual_resolution",
		"evidence: GOAL_EVAL_RESOLUTION_EVIDENCE: inspected child output",
		"verification: GOAL_EVAL_RESOLUTION_VERIFICATION: focused acceptance passed",
		"capability_follow_up_resolution: resolved_follow_up=handled pending capability follow-up supersedes_evidence_id=ev_capability_follow_up",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "pending_goal_follow_up: ev_capability_follow_up_resolution") {
		t.Fatalf("resolution evidence should not create a pending follow-up gate:\n%s", prompt)
	}
}

func TestBuildTurnPromptWithContextScansPastResolvedFollowUps(t *testing.T) {
	resolved := capabilityFollowUpEvidence(t, "goal_resolution_scan", map[string]any{
		"next_action": "RESOLVED_NEXT_ACTION: old handled action",
	})
	resolved.ID = "ev_resolved_old"
	unresolved := capabilityFollowUpEvidence(t, "goal_resolution_scan", map[string]any{
		"next_action": "UNRESOLVED_NEXT_ACTION: still pending action",
	})
	unresolved.ID = "ev_unresolved_old"
	resolution := capabilityFollowUpResolutionEvidence(t, "goal_resolution_scan", "ev_resolved_old", true)
	prompt := BuildTurnPromptWithContext(Goal{
		ID:          "goal_resolution_scan",
		Objective:   "scan past resolved follow-up",
		Status:      StatusActive,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, TurnPromptContext{
		Evidence: []GoalEvidence{unresolved, resolved, resolution},
	})
	if strings.Contains(prompt, "pending_goal_follow_up: ev_resolved_old") {
		t.Fatalf("prompt should skip resolved follow-up:\n%s", prompt)
	}
	if !strings.Contains(prompt, "pending_goal_follow_up: ev_unresolved_old") ||
		!strings.Contains(prompt, "UNRESOLVED_NEXT_ACTION: still pending action") {
		t.Fatalf("prompt should scan past resolution and retain unresolved follow-up:\n%s", prompt)
	}
}

func TestRunnerRunOncePromptUsesExistingPlanAndEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "use plan context", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	created.LastNextAction = "inspect evidence"
	if err := store.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	plan := GoalPlan{
		GoalID:        created.ID,
		Version:       1,
		CurrentStepID: "step_context",
		Steps: []GoalStep{{
			ID:     "step_context",
			Title:  "Use context",
			Status: StepStatusActive,
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_context",
			Description: "Prompt includes context",
			Required:    true,
			Status:      CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, GoalEvidence{
		ID:        "ev_context",
		GoalID:    created.ID,
		Type:      EvidenceTypeManual,
		Summary:   "context evidence",
		Passed:    true,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "progress"}}
	runner := Runner{Store: store, Query: fake}
	if _, err := runner.RunOnce(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"step_context", "crit_context", "ev_context", "last_next_action: inspect evidence"} {
		if !strings.Contains(fake.prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, fake.prompt)
		}
	}
}

func TestRunnerRunOnceAppendsTurnEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "append evidence", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{
		Response: "progress",
		Evidence: []GoalEvidence{{
			ID:      "ev_turn",
			GoalID:  created.ID,
			Type:    EvidenceTypeTest,
			Summary: "go test passed",
			Command: "go test ./internal/goal -count=1",
			Passed:  true,
		}},
	}}
	runner := Runner{Store: store, Query: fake}
	if _, err := runner.RunOnce(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.ListEvidence(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].ID != "ev_turn" || evidence[0].CreatedAt.IsZero() {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestRunnerRunOnceUsesEvidenceFirstCompletionGate(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "gate completion", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan := GoalPlan{
		GoalID:        created.ID,
		Version:       1,
		CurrentStepID: "step_verify",
		Steps: []GoalStep{{
			ID:     "step_verify",
			Title:  "Verify",
			Status: StepStatusActive,
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_tests",
			Description: "tests pass",
			Required:    true,
			Status:      CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "GOAL_STATUS: complete"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusActive || !strings.Contains(result.Decision.Reason, "required acceptance criteria") {
		t.Fatalf("goal=%+v decision=%+v", result.Goal, result.Decision)
	}
	updatedPlan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if updatedPlan.CurrentStepID != "step_verify" || updatedPlan.Steps[0].Status != StepStatusActive {
		t.Fatalf("incomplete goal should leave active plan unchanged: %+v", updatedPlan)
	}
}

func TestRunnerRunOnceCompletesCustomPlanClearsCurrentStep(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "complete custom plan", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan := GoalPlan{
		GoalID:        created.ID,
		Version:       3,
		CurrentStepID: "step_active",
		Steps: []GoalStep{{
			ID:     "step_pending",
			Title:  "Pending",
			Status: StepStatusPending,
		}, {
			ID:     "step_active",
			Title:  "Active",
			Status: StepStatusActive,
		}, {
			ID:     "step_blocked",
			Title:  "Blocked",
			Status: StepStatusBlocked,
		}, {
			ID:     "step_skipped",
			Title:  "Skipped",
			Status: StepStatusSkipped,
		}, {
			ID:     "step_done",
			Title:  "Done",
			Status: StepStatusDone,
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_done",
			Description: "work verified",
			Required:    true,
			Status:      CriterionStatusPassed,
			EvidenceIDs: []string{"ev_done"},
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, GoalEvidence{
		ID:        "ev_done",
		GoalID:    created.ID,
		Type:      EvidenceTypeManual,
		Summary:   "verified",
		Passed:    true,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "GOAL_STATUS: complete"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusComplete || result.Decision.Status != DecisionComplete {
		t.Fatalf("goal=%+v decision=%+v", result.Goal, result.Decision)
	}
	updatedPlan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if updatedPlan.CurrentStepID != "" {
		t.Fatalf("completed custom plan should clear current step: %+v", updatedPlan)
	}
	statusByID := map[string]StepStatus{}
	for _, step := range updatedPlan.Steps {
		statusByID[step.ID] = step.Status
	}
	for _, id := range []string{"step_pending", "step_active", "step_blocked", "step_done"} {
		if statusByID[id] != StepStatusDone {
			t.Fatalf("step %s status = %s, plan = %+v", id, statusByID[id], updatedPlan)
		}
	}
	if statusByID["step_skipped"] != StepStatusSkipped {
		t.Fatalf("skipped step should stay skipped, plan = %+v", updatedPlan)
	}
}

func TestRunnerRunOnceRejectsCompleteWithPendingCapabilityFollowUp(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "handle capability follow-up", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, capabilityFollowUpEvidence(t, created.ID, map[string]any{
		"evidence":    []string{"child found incomplete verification"},
		"unknowns":    []string{"RUNNER_GATE_UNKNOWN: pending request proof"},
		"risks":       []string{"RUNNER_GATE_RISK: premature completion"},
		"next_action": "RUNNER_GATE_NEXT_ACTION: inspect child evidence before completing",
	})); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "GOAL_STATUS: complete"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusActive ||
		!strings.Contains(result.Decision.Reason, "pending capability follow-up") ||
		result.Decision.NextAction != "RUNNER_GATE_NEXT_ACTION: inspect child evidence before completing" {
		t.Fatalf("goal=%+v decision=%+v prompt=%s", result.Goal, result.Decision, fake.prompt)
	}
	if !strings.Contains(fake.prompt, "## Goal capability follow-up gate") {
		t.Fatalf("prompt missing follow-up gate:\n%s", fake.prompt)
	}
}

func TestRunnerRunOnceCompletesAfterCapabilityFollowUpResolution(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "complete after capability follow-up resolution", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan := GoalPlan{
		GoalID:        created.ID,
		Version:       1,
		CurrentStepID: "step_done",
		Steps: []GoalStep{{
			ID:     "step_done",
			Title:  "Done",
			Status: StepStatusDone,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, capabilityFollowUpEvidence(t, created.ID, map[string]any{
		"verification": []string{"RUNNER_GATE_VERIFICATION: focused acceptance required"},
		"next_action":  "RUNNER_GATE_NEXT_ACTION: inspect child evidence before completing",
	})); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, capabilityFollowUpResolutionEvidence(t, created.ID, "ev_capability_follow_up", true)); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "GOAL_STATUS: complete"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusComplete || result.Decision.Status != DecisionComplete {
		t.Fatalf("goal=%+v decision=%+v prompt=%s", result.Goal, result.Decision, fake.prompt)
	}
	if !strings.Contains(fake.prompt, "capability_follow_up_resolution:") {
		t.Fatalf("prompt missing follow-up resolution evidence:\n%s", fake.prompt)
	}
}

func TestRunnerRunOnceBlocksBeforeQueryWhenBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "budget exhausted", CWD: t.TempDir(), TurnBudget: 1, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	created.TurnsUsed = 1
	if err := store.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "should not run"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("query should not run, calls=%d", fake.calls)
	}
	if result.Goal.Status != StatusBlocked || result.Decision.BlockerKey != "turn_budget_exhausted" || result.Event.Type != EventStatusChanged {
		t.Fatalf("goal=%+v decision=%+v event=%+v", result.Goal, result.Decision, result.Event)
	}
}

func TestRunnerRunOnceAddsClosingBudgetPrompt(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "final turn", CWD: t.TempDir(), TurnBudget: 2, TokenBudget: 1000})
	if err != nil {
		t.Fatal(err)
	}
	created.TurnsUsed = 1
	if err := store.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "GOAL_STATUS: complete"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.prompt, "Budget closing policy:") || !strings.Contains(fake.prompt, "Do not expand scope") {
		t.Fatalf("prompt = %s", fake.prompt)
	}
	if result.Goal.Status != StatusComplete || result.Decision.Status != DecisionComplete {
		t.Fatalf("goal=%+v decision=%+v", result.Goal, result.Decision)
	}
}

func TestRunnerRunOnceBlocksUnreachableGoalBeforeQuery(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	missingCWD := filepath.Join(t.TempDir(), "missing")
	created, err := store.Create(ctx, CreateInput{Objective: "needs missing cwd", CWD: missingCWD})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "should not run"}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Fatalf("query should not run, calls=%d", fake.calls)
	}
	if result.Goal.Status != StatusBlocked || result.Decision.BlockerKey != "dep_cwd_writable" {
		t.Fatalf("result = %+v decision=%+v", result.Goal, result.Decision)
	}
	plan, ok, err := store.GetPlan(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if len(plan.Dependencies) != 1 || plan.Dependencies[0].Status != DependencyStatusMissing {
		t.Fatalf("dependencies = %+v", plan.Dependencies)
	}
	events, err := store.ListEvents(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[len(events)-1].Type != EventStatusChanged {
		t.Fatalf("events = %+v", events)
	}
}

func TestRunnerRunOnceDoesNotRecordMissingCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "checkpoint truth", SessionID: "session-1", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{result: TurnResult{Response: "progress", InputTokens: 1, OutputTokens: 1}}
	runner := Runner{Store: store, Query: fake}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.LastCheckpoint != "" || result.Event.Checkpoint != "" {
		t.Fatalf("checkpoint should not be recorded without CheckpointCreated: goal=%+v event=%+v", result.Goal, result.Event)
	}
}

func TestRunnerRunOnceBlocksAfterRepeatedFailure(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "blocked feature", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	created.LastBlocker = "temporary outage"
	created.RepeatedBlockerCount = 2
	if err := store.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueryRunner{err: errors.New("temporary outage")}
	runner := Runner{Store: store, Query: fake, Evaluator: DeterministicEvaluator{BlockedThreshold: 3}}
	result, err := runner.RunOnce(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Goal.Status != StatusBlocked || result.Goal.RepeatedBlockerCount != 3 {
		t.Fatalf("goal = %+v decision=%+v", result.Goal, result.Decision)
	}
}

func TestRunnerRunOnceRejectsConcurrentSameGoal(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	created, err := store.Create(ctx, CreateInput{Objective: "single runner", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	started := make(chan struct{})
	fake := &fakeQueryRunner{block: block, started: started}
	runner := Runner{Store: store, Query: fake}
	var wg sync.WaitGroup
	wg.Add(1)
	var firstErr error
	go func() {
		defer wg.Done()
		_, firstErr = runner.RunOnce(ctx, created.ID)
	}()
	<-started
	if _, err := runner.RunOnce(ctx, created.ID); !errors.Is(err, ErrGoalLocked) {
		close(block)
		wg.Wait()
		t.Fatalf("expected ErrGoalLocked, got %v", err)
	}
	close(block)
	wg.Wait()
	if firstErr != nil {
		t.Fatalf("first run err = %v", firstErr)
	}
	updated, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.TurnsUsed != 1 {
		t.Fatalf("turns used = %d", updated.TurnsUsed)
	}
}
