package handoff

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/compact"
)

func TestHandoffBudgetPackageLimitBoundary(t *testing.T) {
	for _, tt := range []struct {
		estimated int
		wantError bool
	}{
		{estimated: 2047},
		{estimated: 2048},
		{estimated: 2049, wantError: true},
	} {
		t.Run(fmt.Sprintf("%d_tokens", tt.estimated), func(t *testing.T) {
			pkg := protectedPackageAtEstimate(t, tt.estimated)
			plan, err := PlanBudget(pkg, 100_000)
			if tt.wantError {
				var exceeded *BudgetExceededError
				if !errors.As(err, &exceeded) {
					t.Fatalf("PlanBudget() error = %v, want BudgetExceededError", err)
				}
				if exceeded.EstimatedTokens != 2049 || exceeded.LimitTokens != 2048 {
					t.Fatalf("budget error = %+v, want estimate 2049 and limit 2048", exceeded)
				}
				return
			}
			if err != nil {
				t.Fatalf("PlanBudget() error = %v", err)
			}
			if plan.EstimatedTokens != tt.estimated || len(plan.Pruned) != 0 {
				t.Fatalf("plan estimate/pruned = %d/%+v, want %d/no pruning", plan.EstimatedTokens, plan.Pruned, tt.estimated)
			}
		})
	}
}

func TestHandoffBudgetInvalidTargetContextReportsActualEstimate(t *testing.T) {
	pkg := budgetFixture(t)
	want := measuredPackageTokens(t, pkg, PackageBudgetLimit())

	_, err := PlanBudget(pkg, 0)
	var exceeded *BudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("PlanBudget() error = %v, want BudgetExceededError", err)
	}
	if exceeded.EstimatedTokens != want || exceeded.LimitTokens != 0 {
		t.Fatalf("budget error = %+v, want estimate %d and limit 0", exceeded, want)
	}
}

func TestHandoffBudgetAggregateLimitBoundary(t *testing.T) {
	for _, contextTokens := range []int{40959, 40960, 40967} {
		got := AggregateBudgetLimit(contextTokens)
		want := contextTokens * 15 / 100
		if want > 6144 {
			want = 6144
		}
		if got != want {
			t.Fatalf("AggregateBudgetLimit(%d) = %d, want %d", contextTokens, got, want)
		}
	}
	if got := AggregateBudgetLimit(10_000); got != 1500 {
		t.Fatalf("AggregateBudgetLimit(10000) = %d, want 1500", got)
	}
}

func TestHandoffAggregateBudgetPrunesOptionalProseAcrossPackages(t *testing.T) {
	packages := make([]Package, 0, 2)
	for _, sourceRef := range []string{"tenant:source-a", "local:source-b"} {
		cursor := "message:1"
		if strings.HasPrefix(sourceRef, "local:") {
			cursor = "entry:one"
		}
		pkg, err := BuildPackage(PackageInput{Source: Source{Ref: sourceRef, Cursor: cursor}, Target: Target{Ref: "tenant:target"}, Objective: "protected objective", OpenItems: []string{"protected open item"}, Completed: []string{strings.Repeat("optional history ", 30), strings.Repeat("older result ", 30)}, StageSummary: strings.Repeat("optional stage ", 30), Budget: Budget{LimitTokens: DefaultPackageTokenLimit}})
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, pkg)
	}
	originalIDs := []string{packages[0].PackageID, packages[1].PackageID}
	planned, pruned, err := PlanAggregateBudget(packages, 4_000)
	if err != nil {
		t.Fatalf("PlanAggregateBudget() error = %v", err)
	}
	if len(pruned) == 0 || planned[0].Budget.EstimatedTokens+planned[1].Budget.EstimatedTokens > AggregateBudgetLimit(4_000) {
		t.Fatalf("aggregate plan = estimates %d/%d pruned=%v", planned[0].Budget.EstimatedTokens, planned[1].Budget.EstimatedTokens, pruned)
	}
	for index := range planned {
		if planned[index].PackageID != originalIDs[index] || planned[index].Objective != "protected objective" || len(planned[index].OpenItems) != 1 {
			t.Fatalf("aggregate pruning changed protected identity/content: %#v", planned[index])
		}
	}
}

func TestHandoffPruneRemovesOptionalContentInDeterministicOrder(t *testing.T) {
	pkg := budgetFixture(t)
	pkg.Completed = []string{"same", strings.Repeat("historical completed fact ", 40)}
	pkg.StageSummary = "same"
	pkg.Risks = []string{strings.Repeat("long excerpt ", 40)}
	pkg.NextActions = []string{strings.Repeat("next action ", 40)}
	pkg = rebuildBudgetFixture(t, pkg)

	plan, err := PlanBudget(pkg, 2_000)
	if err != nil {
		t.Fatalf("PlanBudget() error = %v", err)
	}
	if len(plan.Pruned) < 3 {
		t.Fatalf("pruned = %+v, want duplicate, history, and optional prose removals", plan.Pruned)
	}
	got := []BudgetCategory{plan.Pruned[0].Category, plan.Pruned[1].Category, plan.Pruned[2].Category}
	want := []BudgetCategory{BudgetCategoryDuplicate, BudgetCategoryCompleted, BudgetCategoryStageSummary}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pruning order = %v, want %v", got, want)
		}
	}
	if plan.Package.Objective != pkg.Objective || len(plan.Package.Constraints) != len(pkg.Constraints) || len(plan.Package.OpenItems) != len(pkg.OpenItems) || len(plan.Package.Evidence) != len(pkg.Evidence) {
		t.Fatalf("protected data changed: %+v", plan.Package)
	}
}

func TestHandoffPruneReturnsStructuredBudgetExceededForProtectedOverflow(t *testing.T) {
	pkg := budgetFixture(t)
	pkg.Objective = strings.Repeat("objective ", 400)
	pkg.Constraints = []string{strings.Repeat("constraint ", 400)}
	pkg.OpenItems = []string{strings.Repeat("open ", 400)}
	pkg = rebuildBudgetFixture(t, pkg)

	_, err := PlanBudget(pkg, 1)
	if err == nil {
		t.Fatal("PlanBudget() error = nil, want budget_exceeded")
	}
	var exceeded *BudgetExceededError
	if !errors.As(err, &exceeded) {
		t.Fatalf("PlanBudget() error = %T, want *BudgetExceededError", err)
	}
	if exceeded.Code != CodeBudgetExceeded || len(exceeded.Candidates) == 0 {
		t.Fatalf("budget error = %+v, want typed code and candidates", exceeded)
	}
	for _, candidate := range exceeded.Candidates {
		if candidate.Category == "transcript" {
			t.Fatalf("budget candidates include transcript fallback: %+v", exceeded.Candidates)
		}
	}
}

func budgetFixture(t *testing.T) Package {
	t.Helper()
	pkg := validPackage(t)
	pkg.Objective = "objective"
	pkg.Constraints = []string{"constraint"}
	pkg.OpenItems = []string{"open item"}
	pkg.Evidence[0].Claim = "evidence"
	return rebuildBudgetFixture(t, pkg)
}

func rebuildBudgetFixture(t *testing.T, pkg Package) Package {
	t.Helper()
	rebuilt, err := BuildPackage(PackageInput{
		Source: pkg.Source, Target: pkg.Target, Objective: pkg.Objective, Constraints: pkg.Constraints,
		StageSummary: pkg.StageSummary, Completed: pkg.Completed, OpenItems: pkg.OpenItems,
		Risks: pkg.Risks, NextActions: pkg.NextActions, Evidence: pkg.Evidence,
		Budget: Budget{LimitTokens: DefaultPackageTokenLimit},
	})
	if err != nil {
		t.Fatalf("BuildPackage() error = %v", err)
	}
	return rebuilt
}

func protectedPackageAtEstimate(t *testing.T, want int) Package {
	t.Helper()
	for fixedCount := 1; fixedCount <= 24; fixedCount++ {
		fixed := make([]string, 0, fixedCount+1)
		for i := 0; i < fixedCount; i++ {
			fixed = append(fixed, fmt.Sprintf("constraint-%02d:%s", i, strings.Repeat("x", MaxCandidateBytes)))
		}
		for tailRunes := 1; tailRunes <= MaxCandidateBytes/3; tailRunes++ {
			pkg := budgetFixture(t)
			pkg.StageSummary = ""
			pkg.Completed = nil
			pkg.Risks = nil
			pkg.NextActions = nil
			pkg.Constraints = append(append([]string(nil), fixed...), "tail:"+strings.Repeat("界", tailRunes))
			pkg = rebuildBudgetFixture(t, pkg)
			if measuredPackageTokens(t, pkg, DefaultPackageTokenLimit) == want {
				return pkg
			}
		}
	}
	t.Fatalf("could not construct protected package with %d estimated tokens", want)
	return Package{}
}

func measuredPackageTokens(t *testing.T, pkg Package, limit int) int {
	t.Helper()
	estimate := 0
	for i := 0; i < 5; i++ {
		rebuilt, err := rebuildPackage(pkg, Budget{EstimatedTokens: estimate, LimitTokens: limit})
		if err != nil {
			t.Fatalf("rebuildPackage() error = %v", err)
		}
		payload, err := rebuilt.CanonicalPayload()
		if err != nil {
			t.Fatalf("CanonicalPayload() error = %v", err)
		}
		next := compact.EstimateTextTokens(string(payload))
		if next == estimate {
			return next
		}
		estimate = next
		pkg = rebuilt
	}
	return estimate
}
