package goal

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDeterministicReachabilityAnalyzerMarksWritableCWDAvailable(t *testing.T) {
	cwd := t.TempDir()
	result, err := DeterministicReachabilityAnalyzer{}.Analyze(context.Background(), Goal{
		ID:          "goal_test",
		Objective:   "ship feature",
		CWD:         cwd,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, GoalPlan{GoalID: "goal_test", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocked {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Dependencies) != 1 || result.Dependencies[0].Status != DependencyStatusAvailable {
		t.Fatalf("dependencies = %+v", result.Dependencies)
	}
}

func TestDeterministicReachabilityAnalyzerBlocksMissingCWD(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	result, err := DeterministicReachabilityAnalyzer{}.Analyze(context.Background(), Goal{
		ID:          "goal_test",
		Objective:   "ship feature",
		CWD:         missing,
		TurnBudget:  3,
		TokenBudget: 1000,
	}, GoalPlan{GoalID: "goal_test", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked || result.BlockerKey != "dep_cwd_writable" {
		t.Fatalf("result = %+v", result)
	}
}

func TestDeterministicReachabilityAnalyzerClassifiesObjectiveRisks(t *testing.T) {
	result, err := DeterministicReachabilityAnalyzer{}.Analyze(context.Background(), Goal{
		ID:          "goal_test",
		Objective:   "deploy to production with api key and then git reset --hard",
		CWD:         t.TempDir(),
		TurnBudget:  1,
		TokenBudget: 1000,
	}, GoalPlan{GoalID: "goal_test", Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked {
		t.Fatalf("expected blocked result: %+v", result)
	}
	want := map[string]bool{
		"risk_missing_credential":        false,
		"risk_external_dependency":       false,
		"risk_unsafe_destructive_action": false,
		"risk_turn_budget_low":           false,
	}
	for _, risk := range result.Risks {
		if _, ok := want[risk.ID]; ok {
			want[risk.ID] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Fatalf("risk %s not found in %+v", id, result.Risks)
		}
	}
}

func TestApplyReachabilityMergesPlanDependenciesAndRisks(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	plan := GoalPlan{
		GoalID:  "goal_test",
		Version: 1,
		Dependencies: []GoalDependency{{
			ID:          "dep_cwd_writable",
			Description: "old",
			Required:    true,
			Status:      DependencyStatusUnknown,
		}},
		Risks: []GoalRisk{{
			ID:          "risk_external_dependency",
			Description: "old",
			Severity:    RiskSeverityLow,
			Status:      RiskStatusAccepted,
		}},
		Steps: []GoalStep{{
			ID:     "step_execute",
			Title:  "Execute",
			Status: StepStatusActive,
		}},
		CurrentStepID: "step_execute",
	}
	updated := ApplyReachability(plan, ReachabilityResult{
		Dependencies: []GoalDependency{{
			ID:          "dep_cwd_writable",
			Description: "new",
			Required:    true,
			Status:      DependencyStatusAvailable,
		}},
		Risks: []GoalRisk{{
			ID:          "risk_external_dependency",
			Description: "new",
			Severity:    RiskSeverityMedium,
			Status:      RiskStatusOpen,
		}},
	}, now)
	if updated.Dependencies[0].Description != "new" || updated.Risks[0].Description != "new" || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("updated = %+v", updated)
	}
}
