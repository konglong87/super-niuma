package repair

import "testing"

func TestGradeShellCommand(t *testing.T) {
	tests := []struct {
		command string
		want    EvidenceGrade
	}{
		{"grep -n BUNDLE_ROOT SKILL.md", EvidenceGradeObservation},
		{"rg BUNDLE_ROOT SKILL.md", EvidenceGradeObservation},
		{"grep -q '^BUNDLE_ROOT=' SKILL.md", EvidenceGradeAssertion},
		{"test \"$total\" -gt 0", EvidenceGradeAssertion},
		{"bash -n scripts/check.sh", EvidenceGradeAssertion},
		{"go test ./internal/query -run TestX -count=1", EvidenceGradeBehavioralTest},
		{"npm test -- --runInBand", EvidenceGradeBehavioralTest},
	}
	for _, tc := range tests {
		if got := GradeShellCommand(tc.command); got != tc.want {
			t.Errorf("GradeShellCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

func TestBuildLedgerPairsBaselineAndPostChangeAndTracksFreshness(t *testing.T) {
	request := VerificationRequest{
		ProbeID: "nonempty-scan", Phase: VerificationPhaseBaseline,
		Targets: []string{"skills/check/SKILL.md"}, ExpectExit: ExitExpectationNonzero,
	}
	baseline := EvaluateShellCommand("test \"$total\" -gt 0", &request, 1)
	request.Phase = VerificationPhasePostChange
	request.ExpectExit = ExitExpectationZero
	post := EvaluateShellCommand("test \"$total\" -gt 0", &request, 0)
	ledger := BuildLedger([]Observation{
		{Verification: &baseline},
		{ContentChange: true},
		{Verification: &post},
	})
	if !ledger.HasCurrentPassingEvidence() {
		t.Fatalf("expected current evidence: %+v", ledger)
	}
	if got := ledger.Evidence[len(ledger.Evidence)-1].Grade; got != EvidenceGradePairedProbe {
		t.Fatalf("post grade = %q, want %q", got, EvidenceGradePairedProbe)
	}
	stale := BuildLedger([]Observation{
		{Verification: &baseline},
		{ContentChange: true},
		{Verification: &post},
		{ContentChange: true},
	})
	if stale.HasCurrentPassingEvidence() {
		t.Fatalf("content change must stale prior evidence: %+v", stale)
	}
}

func TestVerificationRequestValidation(t *testing.T) {
	valid := VerificationRequest{ProbeID: "probe", Phase: VerificationPhaseBaseline, Targets: []string{"file"}, ExpectExit: ExitExpectationNonzero}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	invalid := valid
	invalid.Targets = nil
	if err := invalid.Validate(); err == nil {
		t.Fatal("request without targets accepted")
	}
}

func TestShellFingerprintNormalizesFormattingAndTargets(t *testing.T) {
	left := shellFingerprint("test \"$total\" -gt 0", []string{"b", "a"})
	right := shellFingerprint("test   \"$total\"   -gt   0\n", []string{"a", "b", "a"})
	if left != right {
		t.Fatalf("format-only change altered fingerprint: %s != %s", left, right)
	}
	changed := shellFingerprint("test \"$total\" -ge 0", []string{"a", "b"})
	if left == changed {
		t.Fatal("semantic command change did not alter fingerprint")
	}
}
