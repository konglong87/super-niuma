package computeruse

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ObservationState describes whether a turn published a new observation that
// is authoritative for the next input action. Receipt.After remains
// after-action evidence and never becomes input authority by itself.
type ObservationState string

const (
	ObservationNotRequested ObservationState = "not_requested"
	ObservationReady        ObservationState = "ready"
	ObservationUnavailable  ObservationState = "unavailable"
	ObservationInvalidated  ObservationState = "invalidated"
)

func (s ObservationState) valid() bool {
	switch s {
	case ObservationNotRequested, ObservationReady, ObservationUnavailable, ObservationInvalidated:
		return true
	default:
		return false
	}
}

// ScreenshotState describes the lifecycle of the screenshot attached to a
// turn. A missing screenshot never erases or downgrades the action receipt.
type ScreenshotState string

const (
	ScreenshotNotRequested ScreenshotState = "not_requested"
	ScreenshotReady        ScreenshotState = "ready"
	ScreenshotUnavailable  ScreenshotState = "unavailable"
	ScreenshotExpired      ScreenshotState = "expired"
)

func (s ScreenshotState) valid() bool {
	switch s {
	case ScreenshotNotRequested, ScreenshotReady, ScreenshotUnavailable, ScreenshotExpired:
		return true
	default:
		return false
	}
}

// RetryPolicy is a declarative instruction to the provider/client. It never
// authorizes replaying an input whose dispatch state is partial or unknown.
type RetryPolicy string

const (
	RetryNever       RetryPolicy = "never"
	RetryObserveOnly RetryPolicy = "observe_only"
	RetryResumeOnly  RetryPolicy = "resume_only"
)

func (p RetryPolicy) valid() bool {
	switch p {
	case RetryNever, RetryObserveOnly, RetryResumeOnly:
		return true
	default:
		return false
	}
}

// ComputerTurnResult is the single authoritative result of one provider
// Computer Use turn. It binds dispatch, outcome, receipt, observation, media,
// verification and retry semantics to the same turn/session/action IDs.
type ComputerTurnResult struct {
	ProtocolVersion string             `json:"protocol_version"`
	TurnID          string             `json:"turn_id"`
	SessionID       string             `json:"session_id"`
	ActionID        string             `json:"action_id"`
	ActionKind      ActionKind         `json:"action_kind"`
	DispatchState   DispatchState      `json:"dispatch_state"`
	Outcome         Outcome            `json:"outcome"`
	Verification    VerificationStatus `json:"verification"`

	Receipt ActionReceipt `json:"receipt"`
	// LaunchReceipt carries the trusted target/window binding produced by an
	// atomic launch turn. Receipt remains the common authoritative action fact.
	LaunchReceipt    *LaunchReceipt   `json:"launch_receipt,omitempty"`
	Observation      *Observation     `json:"observation,omitempty"`
	ObservationState ObservationState `json:"observation_state"`
	Screenshot       *MediaRef        `json:"screenshot,omitempty"`
	ScreenshotState  ScreenshotState  `json:"screenshot_state"`
	// ScreenshotData is an in-memory provider transfer only. It is intentionally
	// excluded from JSON and session snapshots; MediaRef remains the persisted
	// authority while the tool receives the same-turn PNG without a second RPC.
	ScreenshotData []byte `json:"-"`

	ErrorCode   string        `json:"error_code,omitempty"`
	RetryPolicy RetryPolicy   `json:"retry_policy"`
	Sequence    uint64        `json:"sequence"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at"`
	Duration    time.Duration `json:"duration"`
}

var errInvalidComputerTurnResult = errors.New("invalid computer turn result")

// Validate enforces the cross-field invariants that keep a receipt and its
// attached evidence in one authoritative turn. It is intentionally
// platform-neutral so bridge, tool and runtime layers can all apply the same
// checks without parsing native errors.
func (r ComputerTurnResult) Validate() error {
	if r.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %q", errInvalidComputerTurnResult, r.ProtocolVersion)
	}
	for name, value := range map[string]string{
		"turn_id": r.TurnID, "session_id": r.SessionID, "action_id": r.ActionID,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s is required", errInvalidComputerTurnResult, name)
		}
	}
	if r.ActionKind == "" {
		return fmt.Errorf("%w: action_kind is required", errInvalidComputerTurnResult)
	}
	if !validDispatchState(r.DispatchState) {
		return fmt.Errorf("%w: invalid dispatch_state %q", errInvalidComputerTurnResult, r.DispatchState)
	}
	if !validOutcome(r.Outcome) {
		return fmt.Errorf("%w: invalid outcome %q", errInvalidComputerTurnResult, r.Outcome)
	}
	if !validVerificationStatus(r.Verification) {
		return fmt.Errorf("%w: invalid verification %q", errInvalidComputerTurnResult, r.Verification)
	}
	if !r.ObservationState.valid() {
		return fmt.Errorf("%w: invalid observation_state %q", errInvalidComputerTurnResult, r.ObservationState)
	}
	if !r.ScreenshotState.valid() {
		return fmt.Errorf("%w: invalid screenshot_state %q", errInvalidComputerTurnResult, r.ScreenshotState)
	}
	if !r.RetryPolicy.valid() {
		return fmt.Errorf("%w: invalid retry_policy %q", errInvalidComputerTurnResult, r.RetryPolicy)
	}
	if r.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be positive", errInvalidComputerTurnResult)
	}
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() || r.CompletedAt.Before(r.StartedAt) {
		return fmt.Errorf("%w: invalid turn timestamps", errInvalidComputerTurnResult)
	}
	if r.Duration < 0 {
		return fmt.Errorf("%w: duration must not be negative", errInvalidComputerTurnResult)
	}
	if r.Receipt.ActionID != r.ActionID || r.Receipt.SessionID != r.SessionID {
		return fmt.Errorf("%w: receipt identity does not match turn", errInvalidComputerTurnResult)
	}
	if !r.Receipt.IsTerminal() {
		return fmt.Errorf("%w: receipt outcome is not terminal", errInvalidComputerTurnResult)
	}
	if r.Receipt.CompletedAt.IsZero() {
		return fmt.Errorf("%w: receipt completion timestamp is required", errInvalidComputerTurnResult)
	}
	if r.Receipt.Outcome != r.Outcome || r.Receipt.DispatchState != r.DispatchState {
		return fmt.Errorf("%w: receipt state does not match turn", errInvalidComputerTurnResult)
	}
	if r.Receipt.Verification != r.Verification {
		return fmt.Errorf("%w: receipt verification does not match turn", errInvalidComputerTurnResult)
	}
	if r.ActionKind == ActionLaunchApp {
		if r.LaunchReceipt == nil || !r.LaunchReceipt.TargetID.valid() {
			return fmt.Errorf("%w: launch receipt binding is required for launch turn", errInvalidComputerTurnResult)
		}
		if r.LaunchReceipt.Outcome != r.Outcome {
			return fmt.Errorf("%w: launch receipt outcome does not match turn", errInvalidComputerTurnResult)
		}
	}
	if err := validateTurnState(r); err != nil {
		return err
	}
	if err := validateObservationState(r); err != nil {
		return err
	}
	if err := validateScreenshotState(r); err != nil {
		return err
	}
	return nil
}

func validDispatchState(state DispatchState) bool {
	switch state {
	case DispatchNotStarted, DispatchComplete, DispatchPartial, DispatchUnknown:
		return true
	default:
		return false
	}
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeNotStarted, OutcomeExecuted, OutcomeRejected, OutcomeFailed, OutcomeUnknown:
		return true
	default:
		return false
	}
}

func validVerificationStatus(status VerificationStatus) bool {
	switch status {
	case VerificationNotChecked, VerificationPassed, VerificationFailed, VerificationUnknown:
		return true
	default:
		return false
	}
}

func validateTurnState(r ComputerTurnResult) error {
	if r.Outcome == OutcomeUnknown || r.DispatchState == DispatchPartial || r.DispatchState == DispatchUnknown {
		if r.Outcome != OutcomeUnknown {
			return fmt.Errorf("%w: partial or unknown dispatch must have unknown outcome", errInvalidComputerTurnResult)
		}
		if r.Verification != VerificationUnknown || r.RetryPolicy != RetryNever {
			return fmt.Errorf("%w: unknown dispatch is never replayable", errInvalidComputerTurnResult)
		}
	}
	if r.Outcome == OutcomeExecuted && r.DispatchState != DispatchComplete {
		return fmt.Errorf("%w: executed outcome requires complete dispatch", errInvalidComputerTurnResult)
	}
	if (r.Outcome == OutcomeRejected || r.Outcome == OutcomeNotStarted) && r.DispatchState != DispatchNotStarted {
		return fmt.Errorf("%w: rejected outcome requires not_started dispatch", errInvalidComputerTurnResult)
	}
	if r.DispatchState == DispatchComplete && r.Outcome == OutcomeUnknown {
		return fmt.Errorf("%w: complete dispatch cannot be unknown outcome", errInvalidComputerTurnResult)
	}
	return nil
}

func validateObservationState(r ComputerTurnResult) error {
	if r.ObservationState == ObservationReady {
		if r.Observation == nil {
			return fmt.Errorf("%w: ready observation is missing", errInvalidComputerTurnResult)
		}
		o := r.Observation
		if o.ID == "" || o.SessionID != r.SessionID || o.Width <= 0 || o.Height <= 0 || o.ObservedAt.IsZero() {
			return fmt.Errorf("%w: ready observation metadata is invalid", errInvalidComputerTurnResult)
		}
		if o.ExpiresAt.IsZero() || !o.ExpiresAt.After(o.ObservedAt) {
			return fmt.Errorf("%w: ready observation TTL is invalid", errInvalidComputerTurnResult)
		}
		if err := o.Capabilities.Validate(); err != nil {
			return fmt.Errorf("%w: ready observation capabilities: %v", errInvalidComputerTurnResult, err)
		}
		return nil
	}
	if r.Observation != nil {
		return fmt.Errorf("%w: non-ready observation state must not publish observation authority", errInvalidComputerTurnResult)
	}
	return nil
}

func validateScreenshotState(r ComputerTurnResult) error {
	if r.ScreenshotState == ScreenshotReady {
		if r.Screenshot == nil {
			return fmt.Errorf("%w: ready screenshot is missing", errInvalidComputerTurnResult)
		}
		media := r.Screenshot
		if media.ID == "" || media.MediaType != "image/png" || media.SHA256 == "" || len(media.SHA256) != 64 || media.Width <= 0 || media.Height <= 0 || media.SizeBytes <= 0 {
			return fmt.Errorf("%w: ready screenshot metadata is invalid", errInvalidComputerTurnResult)
		}
		return nil
	}
	if r.ScreenshotState == ScreenshotNotRequested || r.ScreenshotState == ScreenshotUnavailable {
		if r.Screenshot != nil {
			return fmt.Errorf("%w: screenshot metadata present while screenshot is unavailable", errInvalidComputerTurnResult)
		}
	}
	return nil
}
