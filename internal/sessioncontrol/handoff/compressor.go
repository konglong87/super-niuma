package handoff

import (
	"context"
	"reflect"
	"strings"
)

// Compressor is provider-neutral. Implementations receive no source transcript
// and may only return shortened display prose from the immutable skeleton.
type Compressor interface {
	Compress(context.Context, FactSkeleton) (CompressedFields, error)
}

// FactSkeleton is the complete, immutable structured source for a compression
// request. The package is copied before it crosses the port boundary.
type FactSkeleton struct{ pkg Package }

func (s FactSkeleton) Fields() CompressedFields {
	pkg := s.pkg
	return CompressedFields{
		Objective: pkg.Objective, Constraints: append([]string(nil), pkg.Constraints...), StageSummary: pkg.StageSummary,
		Completed: append([]string(nil), pkg.Completed...), OpenItems: append([]string(nil), pkg.OpenItems...),
		Risks: append([]string(nil), pkg.Risks...), NextActions: append([]string(nil), pkg.NextActions...),
		Evidence: append([]Evidence(nil), pkg.Evidence...),
	}
}

type CompressedFields struct {
	Objective    string
	Constraints  []string
	StageSummary string
	Completed    []string
	OpenItems    []string
	Risks        []string
	NextActions  []string
	Evidence     []Evidence
}

type CompressionResult struct {
	Package    Package
	Compressed bool
	Fallback   bool
	Budget     BudgetPlan
}

// CompressPackage accepts only a strictly shorter optional-prose result. Any
// compressor failure, mutation, or budget failure falls back to the validated
// deterministic package and never changes evidence or verification.
func CompressPackage(ctx context.Context, pkg Package, compressor Compressor, targetContextTokens int) (CompressionResult, error) {
	if err := pkg.Validate(); err != nil {
		return CompressionResult{}, err
	}
	if compressor == nil {
		return fallbackCompression(pkg, targetContextTokens)
	}
	if ctx.Err() != nil {
		return fallbackCompression(pkg, targetContextTokens)
	}
	fields, err := compressor.Compress(ctx, FactSkeleton{pkg: pkg})
	if err != nil {
		return fallbackCompression(pkg, targetContextTokens)
	}
	compressed, ok := compressedPackage(pkg, fields)
	if !ok {
		return fallbackCompression(pkg, targetContextTokens)
	}
	plan, err := PlanBudget(compressed, targetContextTokens)
	if err != nil || len(plan.Pruned) != 0 {
		return fallbackCompression(pkg, targetContextTokens)
	}
	return CompressionResult{Package: plan.Package, Compressed: true, Budget: plan}, nil
}

func fallbackCompression(pkg Package, targetContextTokens int) (CompressionResult, error) {
	plan, err := PlanBudget(pkg, targetContextTokens)
	if err != nil {
		return CompressionResult{}, err
	}
	return CompressionResult{Package: plan.Package, Fallback: true, Budget: plan}, nil
}

func compressedPackage(original Package, fields CompressedFields) (Package, bool) {
	if fields.Objective != original.Objective || !reflect.DeepEqual(fields.Constraints, original.Constraints) || !reflect.DeepEqual(fields.OpenItems, original.OpenItems) || !reflect.DeepEqual(fields.Evidence, original.Evidence) {
		return Package{}, false
	}
	if !shortens(fields.StageSummary, original.StageSummary) || !shortensAll(fields.Completed, original.Completed) || !shortensAll(fields.Risks, original.Risks) || !shortensAll(fields.NextActions, original.NextActions) {
		return Package{}, false
	}
	if fields.StageSummary == original.StageSummary && reflect.DeepEqual(fields.Completed, original.Completed) && reflect.DeepEqual(fields.Risks, original.Risks) && reflect.DeepEqual(fields.NextActions, original.NextActions) {
		return Package{}, false
	}
	pkg := original
	pkg.Objective, pkg.Constraints, pkg.StageSummary = fields.Objective, fields.Constraints, fields.StageSummary
	pkg.Completed, pkg.OpenItems, pkg.Risks, pkg.NextActions, pkg.Evidence = fields.Completed, fields.OpenItems, fields.Risks, fields.NextActions, fields.Evidence
	pkg, err := rebuildPackage(pkg, original.Budget)
	if err != nil || pkg.Validate() != nil {
		return Package{}, false
	}
	return pkg, true
}

func shortens(value, original string) bool {
	return len(value) <= len(original) && strings.Contains(original, value)
}

func shortensAll(values, original []string) bool {
	if len(values) != len(original) {
		return false
	}
	for i := range values {
		if !shortens(values[i], original[i]) {
			return false
		}
	}
	return true
}
