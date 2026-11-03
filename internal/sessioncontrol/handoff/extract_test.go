package handoff

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestExtractionCanonicalPayloadAndIdentityAreDeterministic(t *testing.T) {
	first, err := Extract(unorderedSnapshot(false))
	if err != nil {
		t.Fatalf("first Extract() error = %v", err)
	}
	second, err := Extract(unorderedSnapshot(true))
	if err != nil {
		t.Fatalf("second Extract() error = %v", err)
	}

	firstPayload, err := first.CanonicalPayload()
	if err != nil {
		t.Fatalf("first CanonicalPayload() error = %v", err)
	}
	secondPayload, err := second.CanonicalPayload()
	if err != nil {
		t.Fatalf("second CanonicalPayload() error = %v", err)
	}
	if !bytes.Equal(firstPayload, secondPayload) {
		t.Fatalf("canonical payloads differ\nfirst:  %s\nsecond: %s", firstPayload, secondPayload)
	}
	if first.Source.ContentSHA256 != second.Source.ContentSHA256 {
		t.Fatalf("source hashes differ: %s != %s", first.Source.ContentSHA256, second.Source.ContentSHA256)
	}
	if first.PackageSHA256 != second.PackageSHA256 {
		t.Fatalf("package hashes differ: %s != %s", first.PackageSHA256, second.PackageSHA256)
	}
	if first.PackageID != second.PackageID {
		t.Fatalf("package IDs differ: %s != %s", first.PackageID, second.PackageID)
	}
}

func TestExtractionSelectedRecordMutationChangesSourceHashAndPackageID(t *testing.T) {
	original, err := Extract(unorderedSnapshot(false))
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	changed := unorderedSnapshot(false)
	changed.Facts[0].Text = "Tests are still pending."
	mutated, err := Extract(changed)
	if err != nil {
		t.Fatalf("Extract(mutated) error = %v", err)
	}
	if original.Source.ContentSHA256 == mutated.Source.ContentSHA256 {
		t.Fatal("source hash did not change after selected record mutation")
	}
	if original.PackageID == mutated.PackageID {
		t.Fatal("package ID did not change after selected record mutation")
	}
}

func TestExtractionEvidenceClaimChangesOnlyPackageHash(t *testing.T) {
	firstSnapshot := unorderedSnapshot(false)
	secondSnapshot := unorderedSnapshot(false)
	secondSnapshot.Evidence[0].Claim = "The display wording changed."

	first, err := Extract(firstSnapshot)
	if err != nil {
		t.Fatalf("first Extract() error = %v", err)
	}
	second, err := Extract(secondSnapshot)
	if err != nil {
		t.Fatalf("second Extract() error = %v", err)
	}
	if first.Source.ContentSHA256 != second.Source.ContentSHA256 {
		t.Fatalf("display claim changed source hash: %s != %s", first.Source.ContentSHA256, second.Source.ContentSHA256)
	}
	if first.PackageID != second.PackageID {
		t.Fatalf("display claim changed package ID: %s != %s", first.PackageID, second.PackageID)
	}
	if first.PackageSHA256 == second.PackageSHA256 {
		t.Fatal("display claim did not change package hash")
	}
}

func TestExtractionCapturedAtDoesNotChangeCanonicalIdentity(t *testing.T) {
	firstSnapshot := unorderedSnapshot(false)
	secondSnapshot := unorderedSnapshot(false)
	secondSnapshot.Source.CapturedAt = secondSnapshot.Source.CapturedAt.Add(24 * time.Hour)

	first, err := Extract(firstSnapshot)
	if err != nil {
		t.Fatalf("first Extract() error = %v", err)
	}
	second, err := Extract(secondSnapshot)
	if err != nil {
		t.Fatalf("second Extract() error = %v", err)
	}
	firstPayload, err := first.CanonicalPayload()
	if err != nil {
		t.Fatalf("first CanonicalPayload() error = %v", err)
	}
	secondPayload, err := second.CanonicalPayload()
	if err != nil {
		t.Fatalf("second CanonicalPayload() error = %v", err)
	}
	if !bytes.Equal(firstPayload, secondPayload) {
		t.Fatalf("canonical payload changed with captured_at\nfirst:  %s\nsecond: %s", firstPayload, secondPayload)
	}
	if first.Source.ContentSHA256 != second.Source.ContentSHA256 || first.PackageSHA256 != second.PackageSHA256 || first.PackageID != second.PackageID {
		t.Fatalf("captured_at changed canonical identity: first=%+v second=%+v", first, second)
	}
}

func TestExtractionPreservesEvidenceWhenClaimIsCapped(t *testing.T) {
	snapshot := unorderedSnapshot(false)
	snapshot.Evidence[0].Claim = strings.Repeat("x", MaxCandidateBytes+1)

	pkg, err := Extract(snapshot)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(pkg.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(pkg.Evidence))
	}
	for _, evidence := range pkg.Evidence {
		if evidence.Ref == snapshot.Evidence[0].Ref {
			if len(evidence.Claim) > MaxCandidateBytes {
				t.Fatalf("claim length = %d, cap = %d", len(evidence.Claim), MaxCandidateBytes)
			}
			return
		}
	}
	t.Fatalf("capped evidence %q was removed", snapshot.Evidence[0].Ref)
}

func unorderedSnapshot(reverse bool) SourceSnapshot {
	candidates := []Candidate{
		{Locator: "tenant:source-session#task:1", Text: "Ship the deterministic handoff contract.", Precedence: PrecedenceTaskDescription},
		{Locator: "tenant:source-session#message:8", Text: "An older instruction.", Precedence: PrecedenceUserInstruction},
	}
	facts := []FactCandidate{
		{Locator: "tenant:source-session#task_event:20", Text: "Tests pass.", Kind: FactCompleted},
		{Locator: "tenant:source-session#task_event:21", Text: "Review the canonical hash.", Kind: FactOpenItem},
		{Locator: "tenant:source-session#task_event:22", Text: "A changed source makes the package stale.", Kind: FactRisk},
		{Locator: "tenant:source-session#task_event:23", Text: "Attach only a validated package.", Kind: FactNextAction},
	}
	evidence := []Evidence{
		{Ref: "tenant:source-session#task_event:22", Claim: "Staleness is explicit.", Verification: VerificationReported, SHA256: testSHA256},
		{Ref: "tenant:source-session#task_event:20", Claim: "Tests passed.", Verification: VerificationVerified, SHA256: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", Verifier: Verifier{Kind: VerifierTest, Ref: "tenant:source-session#task_event:20", SHA256: testSHA256}},
	}
	if reverse {
		candidates[0], candidates[1] = candidates[1], candidates[0]
		facts[0], facts[3] = facts[3], facts[0]
		evidence[0], evidence[1] = evidence[1], evidence[0]
	}
	return SourceSnapshot{
		Source:     Source{Ref: "tenant:source-session", Cursor: "task_event:189", CapturedAt: time.Date(2026, time.September, 4, 8, 30, 0, 0, time.UTC)},
		Target:     Target{Ref: "tenant:target-session"},
		Objectives: candidates,
		Constraints: []Candidate{
			{Locator: "tenant:source-session#task_event:10", Text: "No transcript fields.", Precedence: PrecedenceHardConstraint},
		},
		Stages: []Candidate{
			{Locator: "tenant:source-session#task_event:11", Text: "Contract implementation.", Precedence: PrecedenceStructuredStatus},
		},
		Facts:    facts,
		Evidence: evidence,
		Budget:   Budget{EstimatedTokens: 25, LimitTokens: DefaultPackageTokenLimit},
	}
}
