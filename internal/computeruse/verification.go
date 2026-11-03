package computeruse

import (
	"context"
	"errors"
	"time"
)

// VerificationEvidence is the redacted, host-owned evidence available after
// an operation. Raw pixels stay behind ImageReader; verifiers receive only the
// media reference and trusted metadata.
type VerificationEvidence struct {
	Before   Observation
	After    Observation
	Receipt  ActionReceipt
	Binding  TargetBinding
	Now      time.Time
	Deadline time.Time
}

type VerificationResult struct {
	Status  VerificationStatus
	Reason  string
	Checked time.Time
}

// ApplicationVerifier is an optional fixture/application plug-in. The generic
// runner never knows labels such as "send" or "reply"; an application plugin
// may interpret fresh observations for a task-specific assertion.
type ApplicationVerifier interface {
	Verify(context.Context, VerificationEvidence) VerificationResult
}

var (
	ErrEvidenceMissing   = errors.New("computer use: verification evidence missing")
	ErrEvidenceStale     = errors.New("computer use: verification evidence is stale")
	ErrBindingMismatch   = errors.New("computer use: verification target binding mismatch")
	ErrReceiptUnexecuted = errors.New("computer use: action was not executed")
)

// VerifyCore checks evidence that is meaningful for every desktop target. It
// deliberately does not infer application semantics from pixels or text.
func VerifyCore(e VerificationEvidence) VerificationResult {
	now := e.Now
	if now.IsZero() {
		now = time.Now()
	}
	result := VerificationResult{Status: VerificationUnknown, Checked: now}
	if e.Receipt.ActionID == "" || e.Receipt.SessionID == "" || e.Receipt.CompletedAt.IsZero() {
		result.Status, result.Reason = VerificationFailed, ErrEvidenceMissing.Error()
		return result
	}
	if e.Receipt.Outcome != OutcomeExecuted || e.Receipt.After == nil || e.Receipt.AfterObservationID == "" {
		result.Status, result.Reason = VerificationFailed, ErrReceiptUnexecuted.Error()
		return result
	}
	if e.After.ID == "" || e.After.ID != e.Receipt.AfterObservationID || e.After.SessionID != e.Receipt.SessionID {
		result.Status, result.Reason = VerificationFailed, ErrEvidenceMissing.Error()
		return result
	}
	if !e.After.ObservedAt.IsZero() && e.After.ObservedAt.Before(e.Before.ObservedAt) {
		result.Status, result.Reason = VerificationFailed, ErrEvidenceStale.Error()
		return result
	}
	if e.Deadline.IsZero() == false && now.After(e.Deadline) {
		result.Status, result.Reason = VerificationFailed, ErrEvidenceStale.Error()
		return result
	}
	if e.Binding.Valid() && !e.Binding.Matches(e.After.ActiveWindow) {
		result.Status, result.Reason = VerificationFailed, ErrBindingMismatch.Error()
		return result
	}
	result.Status = VerificationPassed
	return result
}
