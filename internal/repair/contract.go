// Package repair models verification evidence for targeted repair work.
// It intentionally has no dependency on the query runtime so evidence policy
// can be tested and reused by other agent entrypoints.
package repair

import (
	"fmt"
	"os"
	"strings"
)

type State string

const (
	StateHypothesis State = "hypothesis"
	StateScoped     State = "scoped"
	StateReproduced State = "reproduced"
	StatePatched    State = "patched"
	StateVerified   State = "verified"
	StateCommitted  State = "committed"
)

type Trigger string

const (
	TriggerTargeted   Trigger = "targeted_repair"
	TriggerRelease    Trigger = "release_repair"
	TriggerEntrypoint Trigger = "entrypoint_repair"
	TriggerMetadata   Trigger = "metadata_repair"
)

type EvidenceKind string

const (
	EvidenceKindShellCommand EvidenceKind = "shell_command"
)

type EvidenceGrade string

const (
	EvidenceGradeUnknown        EvidenceGrade = "unknown"
	EvidenceGradeObservation    EvidenceGrade = "observation"
	EvidenceGradeAssertion      EvidenceGrade = "assertion"
	EvidenceGradeBehavioralTest EvidenceGrade = "behavioral_test"
	EvidenceGradePairedProbe    EvidenceGrade = "paired_probe"
)

type VerificationPhase string

const (
	VerificationPhaseBaseline   VerificationPhase = "baseline"
	VerificationPhasePostChange VerificationPhase = "post_change"
)

type ExitExpectation string

const (
	ExitExpectationZero    ExitExpectation = "zero"
	ExitExpectationNonzero ExitExpectation = "nonzero"
)

type EnforcementMode string

const (
	EnforcementModeOff     EnforcementMode = "off"
	EnforcementModeObserve EnforcementMode = "observe"
	EnforcementModeWarn    EnforcementMode = "warn"
	EnforcementModeEnforce EnforcementMode = "enforce"
)

const EnforcementModeEnv = "NIUMA_REPAIR_VERIFICATION_MODE"

type GateReason string

const (
	GateReasonMissingBaseline GateReason = "missing_baseline"
	GateReasonMissingPost     GateReason = "missing_post_change"
	GateReasonStaleEvidence   GateReason = "stale_evidence"
	GateReasonWeakEvidence    GateReason = "weak_evidence"
)

type WaiverReason string

const (
	WaiverReasonMissingDependency WaiverReason = "missing_dependency"
	WaiverReasonExternalService   WaiverReason = "external_service_unavailable"
	WaiverReasonEnvironment       WaiverReason = "environment_unavailable"
)

type VerificationRequest struct {
	ProbeID    string            `json:"probe_id"`
	Phase      VerificationPhase `json:"phase"`
	Purpose    string            `json:"purpose,omitempty"`
	Targets    []string          `json:"targets"`
	ExpectExit ExitExpectation   `json:"expect_exit"`
}

func (r VerificationRequest) Validate() error {
	if strings.TrimSpace(r.ProbeID) == "" {
		return fmt.Errorf("verification.probe_id is required")
	}
	switch r.Phase {
	case VerificationPhaseBaseline, VerificationPhasePostChange:
	default:
		return fmt.Errorf("verification.phase must be %q or %q", VerificationPhaseBaseline, VerificationPhasePostChange)
	}
	if len(normalizeTargets(r.Targets)) == 0 {
		return fmt.Errorf("verification.targets must contain at least one target")
	}
	switch r.ExpectExit {
	case ExitExpectationZero, ExitExpectationNonzero:
	default:
		return fmt.Errorf("verification.expect_exit must be %q or %q", ExitExpectationZero, ExitExpectationNonzero)
	}
	return nil
}

func NormalizeEnforcementMode(value string) EnforcementMode {
	switch EnforcementMode(strings.ToLower(strings.TrimSpace(value))) {
	case EnforcementModeOff:
		return EnforcementModeOff
	case EnforcementModeWarn:
		return EnforcementModeWarn
	case EnforcementModeEnforce:
		return EnforcementModeEnforce
	case EnforcementModeObserve:
		return EnforcementModeObserve
	default:
		return EnforcementModeObserve
	}
}

func CurrentEnforcementMode() EnforcementMode {
	return NormalizeEnforcementMode(os.Getenv(EnforcementModeEnv))
}

func (m EnforcementMode) RecordsEvidence() bool {
	return m != EnforcementModeOff
}

func (m EnforcementMode) Warns() bool {
	return m == EnforcementModeWarn || m == EnforcementModeEnforce
}

func (m EnforcementMode) Enforces() bool {
	return m == EnforcementModeEnforce
}
