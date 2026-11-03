package handoff

import (
	"fmt"

	"github.com/konglong87/super-niuma/internal/compact"
)

const (
	maxAggregateBudgetTokens = 6144
	targetContextBudgetRatio = 15

	CodeBudgetExceeded ErrorCode = "budget_exceeded"
)

type BudgetCategory string

const (
	BudgetCategoryObjective    BudgetCategory = "objective"
	BudgetCategoryConstraints  BudgetCategory = "constraints"
	BudgetCategoryOpenItems    BudgetCategory = "open_items"
	BudgetCategoryEvidence     BudgetCategory = "evidence"
	BudgetCategoryDuplicate    BudgetCategory = "duplicate"
	BudgetCategoryCompleted    BudgetCategory = "completed"
	BudgetCategoryStageSummary BudgetCategory = "stage_summary"
	BudgetCategoryRisks        BudgetCategory = "risks"
	BudgetCategoryNextActions  BudgetCategory = "next_actions"
)

// BudgetItem identifies a structured field considered by the planner. It
// never stores source records or transcript fallbacks.
type BudgetItem struct {
	Category  BudgetCategory
	Value     string
	Tokens    int
	SourceRef string
}

type BudgetPlan struct {
	Package              Package
	EstimatedTokens      int
	PackageLimitTokens   int
	AggregateLimitTokens int
	EffectiveLimitTokens int
	Retained             []BudgetItem
	Pruned               []BudgetItem
	CandidateRemovals    []BudgetItem
}

// BudgetExceededError is a structured terminal result. Callers can expose its
// stable code and suggested removals without exposing a transcript.
type BudgetExceededError struct {
	Code            ErrorCode
	EstimatedTokens int
	LimitTokens     int
	Candidates      []BudgetItem
}

func (e *BudgetExceededError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: estimated %d tokens exceeds limit %d", e.Code, e.EstimatedTokens, e.LimitTokens)
}

func PackageBudgetLimit() int { return DefaultPackageTokenLimit }

// AggregateBudgetLimit reserves at most 15 percent of the target context for
// all handoff packages, capped at 6,144 tokens. The caller owns the actual
// target context because no model-specific window is safe to hard-code here.
func AggregateBudgetLimit(targetContextTokens int) int {
	if targetContextTokens <= 0 {
		return 0
	}
	limit := targetContextTokens * targetContextBudgetRatio / 100
	if limit > maxAggregateBudgetTokens {
		return maxAggregateBudgetTokens
	}
	return limit
}

// PlanAggregateBudget prunes optional prose across an ordered package set.
// Source order is stable and later packages yield optional history first;
// protected objective, constraints, open items, and evidence are never dropped.
func PlanAggregateBudget(packages []Package, targetContextTokens int) ([]Package, []BudgetItem, error) {
	limit := AggregateBudgetLimit(targetContextTokens)
	planned := append([]Package(nil), packages...)
	pruned := []BudgetItem{}
	total, err := measurePackages(planned)
	if err != nil {
		return nil, nil, err
	}
	for total > limit {
		changed := false
		for _, category := range []BudgetCategory{BudgetCategoryDuplicate, BudgetCategoryCompleted, BudgetCategoryStageSummary, BudgetCategoryRisks, BudgetCategoryNextActions} {
			for index := len(planned) - 1; index >= 0; index-- {
				next, item, ok := prunePackageCategory(planned[index], category)
				if !ok {
					continue
				}
				next, _, err = rebuildWithMeasuredBudget(next, DefaultPackageTokenLimit)
				if err != nil {
					return nil, nil, err
				}
				planned[index] = next
				pruned = append(pruned, item)
				total, err = measurePackages(planned)
				if err != nil {
					return nil, nil, err
				}
				changed = true
				break
			}
			if changed {
				break
			}
		}
		if !changed {
			candidates := []BudgetItem{}
			for _, pkg := range planned {
				candidates = append(candidates, protectedItems(pkg)...)
			}
			return planned, pruned, &BudgetExceededError{Code: CodeBudgetExceeded, EstimatedTokens: total, LimitTokens: limit, Candidates: candidates}
		}
	}
	return planned, pruned, nil
}

func measurePackages(packages []Package) (int, error) {
	total := 0
	for index := range packages {
		measured, estimate, err := rebuildWithMeasuredBudget(packages[index], DefaultPackageTokenLimit)
		if err != nil {
			return 0, err
		}
		packages[index] = measured
		total += estimate
	}
	return total, nil
}

func prunePackageCategory(pkg Package, category BudgetCategory) (Package, BudgetItem, bool) {
	switch category {
	case BudgetCategoryDuplicate:
		return pruneDuplicate(pkg)
	case BudgetCategoryCompleted:
		if len(pkg.Completed) > 0 {
			value := pkg.Completed[len(pkg.Completed)-1]
			pkg.Completed = pkg.Completed[:len(pkg.Completed)-1]
			return pkg, budgetItem(category, value), true
		}
	case BudgetCategoryStageSummary:
		if pkg.StageSummary != "" {
			value := pkg.StageSummary
			pkg.StageSummary = ""
			return pkg, budgetItem(category, value), true
		}
	case BudgetCategoryRisks:
		if len(pkg.Risks) > 0 {
			value := pkg.Risks[len(pkg.Risks)-1]
			pkg.Risks = pkg.Risks[:len(pkg.Risks)-1]
			return pkg, budgetItem(category, value), true
		}
	case BudgetCategoryNextActions:
		if len(pkg.NextActions) > 0 {
			value := pkg.NextActions[len(pkg.NextActions)-1]
			pkg.NextActions = pkg.NextActions[:len(pkg.NextActions)-1]
			return pkg, budgetItem(category, value), true
		}
	}
	return Package{}, BudgetItem{}, false
}

// PlanBudget retains protected objective, constraints, open items, and
// evidence. It removes optional data in a stable order before reporting a
// typed error when protected fields alone cannot fit.
func PlanBudget(pkg Package, targetContextTokens int) (BudgetPlan, error) {
	if err := pkg.Validate(); err != nil {
		return BudgetPlan{}, err
	}
	aggregateLimit := AggregateBudgetLimit(targetContextTokens)
	effectiveLimit := PackageBudgetLimit()
	if aggregateLimit < effectiveLimit {
		effectiveLimit = aggregateLimit
	}
	if effectiveLimit <= 0 {
		_, estimatedTokens, err := rebuildWithMeasuredBudget(pkg, PackageBudgetLimit())
		if err != nil {
			return BudgetPlan{}, err
		}
		return BudgetPlan{}, &BudgetExceededError{Code: CodeBudgetExceeded, EstimatedTokens: estimatedTokens, LimitTokens: effectiveLimit, Candidates: protectedItems(pkg)}
	}

	current := pkg
	plan := BudgetPlan{PackageLimitTokens: PackageBudgetLimit(), AggregateLimitTokens: aggregateLimit, EffectiveLimitTokens: effectiveLimit}
	for {
		var err error
		current, plan.EstimatedTokens, err = rebuildWithMeasuredBudget(current, effectiveLimit)
		if err != nil {
			return BudgetPlan{}, err
		}
		if plan.EstimatedTokens <= effectiveLimit {
			plan.Package = current
			plan.Retained = retainedItems(current)
			return plan, nil
		}

		next, item, pruned := pruneOne(current)
		if !pruned {
			plan.Package = current
			plan.Retained = retainedItems(current)
			plan.CandidateRemovals = protectedItems(current)
			return plan, &BudgetExceededError{Code: CodeBudgetExceeded, EstimatedTokens: plan.EstimatedTokens, LimitTokens: effectiveLimit, Candidates: plan.CandidateRemovals}
		}
		plan.Pruned = append(plan.Pruned, item)
		current = next
	}
}

func rebuildWithMeasuredBudget(pkg Package, limit int) (Package, int, error) {
	estimate := pkg.Budget.EstimatedTokens
	for i := 0; i < 3; i++ {
		rebuilt, err := rebuildPackage(pkg, Budget{EstimatedTokens: estimate, LimitTokens: DefaultPackageTokenLimit})
		if err != nil {
			return Package{}, 0, err
		}
		payload, err := rebuilt.CanonicalPayload()
		if err != nil {
			return Package{}, 0, err
		}
		next := compact.EstimateTextTokens(string(payload))
		if next == estimate {
			return rebuilt, next, nil
		}
		estimate = next
		pkg = rebuilt
	}
	rebuilt, err := rebuildPackage(pkg, Budget{EstimatedTokens: estimate, LimitTokens: DefaultPackageTokenLimit})
	return rebuilt, estimate, err
}

func rebuildPackage(pkg Package, budget Budget) (Package, error) {
	rebuilt, err := BuildPackage(PackageInput{
		Source: pkg.Source, Target: pkg.Target, Objective: pkg.Objective, Constraints: pkg.Constraints,
		StageSummary: pkg.StageSummary, Completed: pkg.Completed, OpenItems: pkg.OpenItems,
		Risks: pkg.Risks, NextActions: pkg.NextActions, Evidence: pkg.Evidence, Budget: budget,
	})
	if err != nil || pkg.PackageID == "" {
		return rebuilt, err
	}
	// Pruning and compression change package presentation, not its fixed source
	// snapshot identity.
	rebuilt.Source.ContentSHA256 = pkg.Source.ContentSHA256
	rebuilt.PackageID = pkg.PackageID
	payload, err := rebuilt.CanonicalPayload()
	if err != nil {
		return Package{}, err
	}
	rebuilt.PackageSHA256 = hashPayload(payload)
	return rebuilt, rebuilt.Validate()
}

func pruneOne(pkg Package) (Package, BudgetItem, bool) {
	if pruned, item, ok := pruneDuplicate(pkg); ok {
		return pruned, item, true
	}
	if len(pkg.Completed) > 0 {
		value := pkg.Completed[len(pkg.Completed)-1]
		pkg.Completed = pkg.Completed[:len(pkg.Completed)-1]
		return pkg, budgetItem(BudgetCategoryCompleted, value), true
	}
	if pkg.StageSummary != "" {
		value := pkg.StageSummary
		pkg.StageSummary = ""
		return pkg, budgetItem(BudgetCategoryStageSummary, value), true
	}
	if len(pkg.Risks) > 0 {
		value := pkg.Risks[len(pkg.Risks)-1]
		pkg.Risks = pkg.Risks[:len(pkg.Risks)-1]
		return pkg, budgetItem(BudgetCategoryRisks, value), true
	}
	if len(pkg.NextActions) > 0 {
		value := pkg.NextActions[len(pkg.NextActions)-1]
		pkg.NextActions = pkg.NextActions[:len(pkg.NextActions)-1]
		return pkg, budgetItem(BudgetCategoryNextActions, value), true
	}
	return Package{}, BudgetItem{}, false
}

func pruneDuplicate(pkg Package) (Package, BudgetItem, bool) {
	seen := make(map[string]struct{}, 1+len(pkg.Constraints)+len(pkg.OpenItems)+len(pkg.Evidence))
	seen[pkg.Objective] = struct{}{}
	for _, value := range pkg.Constraints {
		seen[value] = struct{}{}
	}
	for _, value := range pkg.OpenItems {
		seen[value] = struct{}{}
	}
	for _, evidence := range pkg.Evidence {
		seen[evidence.Claim] = struct{}{}
	}
	if _, exists := seen[pkg.StageSummary]; exists && pkg.StageSummary != "" {
		value := pkg.StageSummary
		pkg.StageSummary = ""
		return pkg, budgetItem(BudgetCategoryDuplicate, value), true
	}
	if pkg.StageSummary != "" {
		seen[pkg.StageSummary] = struct{}{}
	}
	for index := len(pkg.Completed) - 1; index >= 0; index-- {
		if _, exists := seen[pkg.Completed[index]]; exists {
			value := pkg.Completed[index]
			pkg.Completed = append(pkg.Completed[:index:index], pkg.Completed[index+1:]...)
			return pkg, budgetItem(BudgetCategoryDuplicate, value), true
		}
		seen[pkg.Completed[index]] = struct{}{}
	}
	for index := len(pkg.Risks) - 1; index >= 0; index-- {
		if _, exists := seen[pkg.Risks[index]]; exists {
			value := pkg.Risks[index]
			pkg.Risks = append(pkg.Risks[:index:index], pkg.Risks[index+1:]...)
			return pkg, budgetItem(BudgetCategoryDuplicate, value), true
		}
		seen[pkg.Risks[index]] = struct{}{}
	}
	for index := len(pkg.NextActions) - 1; index >= 0; index-- {
		if _, exists := seen[pkg.NextActions[index]]; exists {
			value := pkg.NextActions[index]
			pkg.NextActions = append(pkg.NextActions[:index:index], pkg.NextActions[index+1:]...)
			return pkg, budgetItem(BudgetCategoryDuplicate, value), true
		}
		seen[pkg.NextActions[index]] = struct{}{}
	}
	return Package{}, BudgetItem{}, false
}

func retainedItems(pkg Package) []BudgetItem {
	items := protectedItems(pkg)
	for _, value := range pkg.Completed {
		items = append(items, budgetItem(BudgetCategoryCompleted, value))
	}
	if pkg.StageSummary != "" {
		items = append(items, budgetItem(BudgetCategoryStageSummary, pkg.StageSummary))
	}
	for _, value := range pkg.Risks {
		items = append(items, budgetItem(BudgetCategoryRisks, value))
	}
	for _, value := range pkg.NextActions {
		items = append(items, budgetItem(BudgetCategoryNextActions, value))
	}
	return items
}

func protectedItems(pkg Package) []BudgetItem {
	items := make([]BudgetItem, 0, 1+len(pkg.Constraints)+len(pkg.OpenItems)+len(pkg.Evidence))
	items = append(items, budgetItem(BudgetCategoryObjective, pkg.Objective))
	for _, value := range pkg.Constraints {
		items = append(items, budgetItem(BudgetCategoryConstraints, value))
	}
	for _, value := range pkg.OpenItems {
		items = append(items, budgetItem(BudgetCategoryOpenItems, value))
	}
	for _, evidence := range pkg.Evidence {
		items = append(items, budgetItem(BudgetCategoryEvidence, evidence.Ref))
	}
	for index := range items {
		items[index].SourceRef = pkg.Source.Ref
	}
	return items
}

func budgetItem(category BudgetCategory, value string) BudgetItem {
	return BudgetItem{Category: category, Value: value, Tokens: compact.EstimateTextTokens(value)}
}
