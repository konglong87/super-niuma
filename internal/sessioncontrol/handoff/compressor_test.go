package handoff

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type compressorFunc func(context.Context, FactSkeleton) (CompressedFields, error)

func (f compressorFunc) Compress(ctx context.Context, skeleton FactSkeleton) (CompressedFields, error) {
	return f(ctx, skeleton)
}

func TestHandoffCompressorFallsBackForNilTimeoutAndError(t *testing.T) {
	pkg := budgetFixture(t)
	for _, compressor := range []Compressor{
		nil,
		compressorFunc(func(context.Context, FactSkeleton) (CompressedFields, error) {
			return CompressedFields{}, context.DeadlineExceeded
		}),
		compressorFunc(func(context.Context, FactSkeleton) (CompressedFields, error) {
			return CompressedFields{}, errors.New("provider unavailable")
		}),
	} {
		result, err := CompressPackage(context.Background(), pkg, compressor, 100_000)
		if err != nil {
			t.Fatalf("CompressPackage() error = %v", err)
		}
		if !result.Fallback || result.Package.Objective != pkg.Objective || len(result.Package.Evidence) != len(pkg.Evidence) {
			t.Fatalf("fallback = %+v, want unchanged deterministic package", result)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := CompressPackage(canceled, pkg, compressorFunc(func(context.Context, FactSkeleton) (CompressedFields, error) {
		t.Fatal("compressor was called after timeout")
		return CompressedFields{}, nil
	}), 100_000)
	if err != nil || !result.Fallback {
		t.Fatalf("canceled context result = %+v, err = %v", result, err)
	}
}

func TestHandoffCompressorAcceptsShorterOptionalProse(t *testing.T) {
	pkg := budgetFixture(t)
	pkg.StageSummary = "Long stage summary with implementation detail."
	pkg = rebuildBudgetFixture(t, pkg)
	result, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
		fields := skeleton.Fields()
		fields.StageSummary = "Long stage"
		return fields, nil
	}), 100_000)
	if err != nil {
		t.Fatalf("CompressPackage() error = %v", err)
	}
	if result.Fallback || !result.Compressed || result.Package.StageSummary != "Long stage" {
		t.Fatalf("compression result = %+v", result)
	}
	if result.Package.Source.ContentSHA256 != pkg.Source.ContentSHA256 || result.Package.PackageID != pkg.PackageID {
		t.Fatalf("compression changed deterministic source identity: before=%s/%s after=%s/%s", pkg.Source.ContentSHA256, pkg.PackageID, result.Package.Source.ContentSHA256, result.Package.PackageID)
	}
	if result.Package.PackageSHA256 == pkg.PackageSHA256 {
		t.Fatal("compressed payload did not receive a new package SHA")
	}
}

func TestHandoffCompressorTreatsEqualFieldsAsFallback(t *testing.T) {
	pkg := budgetFixture(t)
	result, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
		return skeleton.Fields(), nil
	}), 100_000)
	if err != nil {
		t.Fatalf("CompressPackage() error = %v", err)
	}
	if !result.Fallback || result.Compressed {
		t.Fatalf("no-op compression result = %+v, want fallback", result)
	}
}

func TestHandoffCompressorRejectsEvidenceMutation(t *testing.T) {
	pkg := budgetFixture(t)
	result, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
		fields := skeleton.Fields()
		fields.Evidence = append(fields.Evidence, Evidence{Ref: "tenant:source-session#message:1"})
		return fields, nil
	}), 100_000)
	if err != nil {
		t.Fatalf("CompressPackage() error = %v", err)
	}
	if !result.Fallback {
		t.Fatalf("evidence addition was accepted: %+v", result.Package.Evidence)
	}
}

func TestHandoffCompressorRejectsEvidenceIdentityAndVerificationMutation(t *testing.T) {
	pkg := budgetFixture(t)
	for _, mutate := range []func(*CompressedFields){
		func(fields *CompressedFields) { fields.Evidence[0].Ref = "tenant:source-session#message:1" },
		func(fields *CompressedFields) { fields.Evidence[0].SHA256 = strings.Repeat("a", 64) },
		func(fields *CompressedFields) { fields.Evidence[0].Verification = VerificationVerified },
	} {
		result, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
			fields := skeleton.Fields()
			mutate(&fields)
			return fields, nil
		}), 100_000)
		if err != nil {
			t.Fatalf("CompressPackage() error = %v", err)
		}
		if !result.Fallback {
			t.Fatal("mutated evidence was accepted")
		}
	}
}

func TestHandoffCompressorFallsBackWhenCompressedResultExceedsBudget(t *testing.T) {
	pkg := budgetFixture(t)
	pkg.StageSummary = strings.Repeat("stage ", 100)
	pkg.Completed = make([]string, 24)
	for i := range pkg.Completed {
		pkg.Completed[i] = strings.Repeat(string(rune('a'+i)), MaxCandidateBytes)
	}
	pkg = rebuildBudgetFixture(t, pkg)
	originalPlan, err := PlanBudget(pkg, 100_000)
	if err != nil {
		t.Fatalf("PlanBudget(original) error = %v", err)
	}
	if len(originalPlan.Pruned) == 0 {
		t.Fatal("test fixture does not exceed the package budget")
	}
	compressorSawOriginal := false
	compressorReturnedStrictShortening := false
	result, err := CompressPackage(context.Background(), pkg, compressorFunc(func(_ context.Context, skeleton FactSkeleton) (CompressedFields, error) {
		fields := skeleton.Fields()
		compressorSawOriginal = len(fields.Completed) == len(pkg.Completed)
		fields.StageSummary = fields.StageSummary[:len(fields.StageSummary)-1]
		compressorReturnedStrictShortening = len(fields.StageSummary) == len(pkg.StageSummary)-1
		return fields, nil
	}), 100_000)
	if err != nil {
		t.Fatalf("CompressPackage() error = %v", err)
	}
	if !compressorSawOriginal {
		t.Fatal("compressor received the already-pruned package instead of the original skeleton")
	}
	if !compressorReturnedStrictShortening {
		t.Fatal("test compressor did not produce a strict shortening")
	}
	if !result.Fallback || result.Compressed || result.Package.PackageSHA256 != originalPlan.Package.PackageSHA256 {
		t.Fatalf("over-budget compressed result did not fall back: %+v", result)
	}
}
