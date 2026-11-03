package computeruse

import (
	"context"
	"testing"
	"time"
)

type fixtureVerifier struct{}

func (fixtureVerifier) Verify(context.Context, VerificationEvidence) VerificationResult {
	return VerificationResult{Status: VerificationPassed}
}

func TestVerifyCoreRequiresFreshExecutedEvidence(t *testing.T) {
	beforeAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	afterAt := beforeAt.Add(time.Second)
	before := Observation{ID: "before", SessionID: "session", ObservedAt: beforeAt}
	after := Observation{ID: "after", SessionID: "session", ObservedAt: afterAt}
	receipt := ActionReceipt{ActionID: "action", SessionID: "session", Outcome: OutcomeExecuted, CompletedAt: afterAt, AfterObservationID: "after", After: &MediaRef{ID: "after", Width: 10, Height: 10, MediaType: "image/png"}}
	result := VerifyCore(VerificationEvidence{Before: before, After: after, Receipt: receipt, Now: afterAt})
	if result.Status != VerificationPassed {
		t.Fatalf("result=%+v", result)
	}
	receipt.Outcome = OutcomeUnknown
	if result := VerifyCore(VerificationEvidence{Before: before, After: after, Receipt: receipt, Now: afterAt}); result.Status != VerificationFailed {
		t.Fatalf("unknown receipt verified: %+v", result)
	}
}

func TestApplicationVerifierIsOptionalAndProviderNeutral(t *testing.T) {
	var verifier ApplicationVerifier = fixtureVerifier{}
	if result := verifier.Verify(context.Background(), VerificationEvidence{}); result.Status != VerificationPassed {
		t.Fatalf("plugin result=%+v", result)
	}
}
