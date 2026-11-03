package goal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGoalPlanValidateCoversStructuredFields(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	exitCode := 0
	plan := GoalPlan{
		GoalID:        "goal_123",
		Version:       1,
		Summary:       "Ship structured goal mode",
		CurrentStepID: "step_verify",
		Steps: []GoalStep{{
			ID:          "step_verify",
			Title:       "Run verification",
			Status:      StepStatusActive,
			Rationale:   "Evidence gates completion",
			DependsOn:   []string{"step_impl"},
			EvidenceIDs: []string{"ev_test"},
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_tests",
			Description: "Goal package tests pass",
			Required:    true,
			Status:      CriterionStatusPending,
			EvidenceIDs: []string{"ev_test"},
		}},
		Dependencies: []GoalDependency{{
			ID:          "dep_repo",
			Type:        "local_repo",
			Description: "Repository checkout is writable",
			Required:    true,
			Status:      DependencyStatusAvailable,
		}},
		Risks: []GoalRisk{{
			ID:          "risk_budget",
			Type:        "budget",
			Description: "Long goals can waste turns",
			Severity:    RiskSeverityMedium,
			Mitigation:  "Use closing policy",
			Status:      RiskStatusOpen,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence := GoalEvidence{
		ID:        "ev_test",
		GoalID:    "goal_123",
		Type:      EvidenceTypeTest,
		Summary:   "go test ./internal/goal -count=1 passed",
		Command:   "go test ./internal/goal -count=1",
		ExitCode:  &exitCode,
		Passed:    true,
		Payload:   json.RawMessage(`{"package":"internal/goal"}`),
		CreatedAt: now,
	}
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"exit_code":0`) {
		t.Fatalf("successful exit code should remain in JSON: %s", data)
	}
}

func TestGoalPlanValidateRejectsInvalidReferencesAndStatuses(t *testing.T) {
	plan := GoalPlan{
		GoalID:        "goal_123",
		Version:       1,
		CurrentStepID: "missing",
		Steps: []GoalStep{{
			ID:     "step_1",
			Title:  "Implement",
			Status: StepStatusPending,
		}},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "current step id not found") {
		t.Fatalf("expected current step validation error, got %v", err)
	}
	plan.CurrentStepID = "step_1"
	plan.AcceptanceCriteria = []GoalCriterion{{
		ID:          "crit_1",
		Description: "Pass tests",
		Status:      CriterionStatus("unknown"),
	}}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "invalid goal criterion status") {
		t.Fatalf("expected criterion status validation error, got %v", err)
	}
}

func TestGoalEvidenceValidateRejectsInvalidPayload(t *testing.T) {
	evidence := GoalEvidence{
		ID:      "ev_bad",
		GoalID:  "goal_123",
		Type:    EvidenceTypeCommand,
		Summary: "bad json",
		Payload: json.RawMessage(`{"unterminated"`),
	}
	if err := evidence.Validate(); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("expected payload validation error, got %v", err)
	}
}
