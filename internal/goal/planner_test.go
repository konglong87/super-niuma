package goal

import (
	"strings"
	"testing"
	"time"
)

func TestDeterministicPlannerBuildsMinimalPlan(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	plan, err := DeterministicPlanner{}.BuildInitialPlan(Goal{
		ID:        "goal_test",
		Objective: "ship goal planner",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if plan.GoalID != "goal_test" || plan.Version != 1 || plan.CurrentStepID != "step_execute" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Steps) != 3 || plan.Steps[1].Status != StepStatusActive {
		t.Fatalf("steps = %+v", plan.Steps)
	}
	if len(plan.AcceptanceCriteria) != 1 || !strings.Contains(plan.AcceptanceCriteria[0].Description, "ship goal planner") {
		t.Fatalf("criteria = %+v", plan.AcceptanceCriteria)
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDeterministicPlannerPreservesExplicitAcceptanceCriteria(t *testing.T) {
	objective := strings.Join([]string{
		"Optimize goal mode",
		"",
		"验收标准:",
		"- planner tests pass",
		"2. runner creates a plan before first turn",
		"",
		"说明:",
		"- do not expand API scope",
	}, "\n")
	plan, err := DeterministicPlanner{}.BuildInitialPlan(Goal{ID: "goal_test", Objective: objective}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.AcceptanceCriteria) != 2 {
		t.Fatalf("criteria = %+v", plan.AcceptanceCriteria)
	}
	if plan.AcceptanceCriteria[0].Description != "planner tests pass" {
		t.Fatalf("first criterion = %+v", plan.AcceptanceCriteria[0])
	}
	if plan.AcceptanceCriteria[1].Description != "runner creates a plan before first turn" {
		t.Fatalf("second criterion = %+v", plan.AcceptanceCriteria[1])
	}
}

func TestDeterministicPlannerRejectsMissingGoalFields(t *testing.T) {
	if _, err := (DeterministicPlanner{}).BuildInitialPlan(Goal{Objective: "missing id"}, time.Time{}); err == nil || !strings.Contains(err.Error(), "goal id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
	if _, err := (DeterministicPlanner{}).BuildInitialPlan(Goal{ID: "goal_test"}, time.Time{}); err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected missing objective error, got %v", err)
	}
}
