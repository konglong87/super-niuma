package computerbridge

import (
	"math"
	"strings"
	"unicode/utf8"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const maxIDBytes = 1024

// Owner uses uint64 in the shared domain contract. Restrict to positive signed
// IDs as well, so negative IDs converted by an upstream adapter fail closed.
// All three components are forwarded unchanged; no fixed owner or 1:1 mapping.
func validOwner(owner cu.SessionOwner) bool {
	return owner.TenantID > 0 && owner.TenantID <= math.MaxInt64 &&
		owner.UserID > 0 && owner.UserID <= math.MaxInt64 &&
		owner.SessionID > 0 && owner.SessionID <= math.MaxInt64
}

func validID(id string) bool {
	if id == "" || len(id) > maxIDBytes || strings.TrimSpace(id) != id || !utf8.ValidString(id) {
		return false
	}
	for _, c := range id {
		if c < ' ' || c == 127 {
			return false
		}
	}
	return true
}
func optionalID(id string) bool { return id == "" || validID(id) }

func validRequest(r Request) bool {
	if !validOwner(r.Owner) {
		return false
	}
	if r.Op == OpCapabilities {
		return (r.SessionID == "" || validID(r.SessionID)) && r.StartupAttemptID == "" && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == "" && !r.Approved
	}
	switch r.Op {
	case OpLookup:
		return r.SessionID == "" && r.StartupAttemptID == "" && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	case OpEnsure:
		return r.SessionID == "" && optionalID(r.StartupAttemptID) && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	case OpResolveEnsure:
		return r.SessionID == "" && validID(r.StartupAttemptID) && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	case OpRuntimeStatus, OpRuntimeStop, OpStartLocal:
		return r.SessionID == "" && r.StartupAttemptID == "" && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	case OpVisualCue:
		return validID(r.SessionID) && r.StartupAttemptID == "" && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	}
	if !validID(r.SessionID) {
		return false
	}
	switch r.Op {
	case OpPause, OpResume, OpStop, OpSnapshot:
		return r.StartupAttemptID == "" && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == "" && !r.Approved
	case OpLaunchApp:
		validTargetLaunch := validID(r.TargetID) && r.Application == ""
		validLegacyLaunch := r.TargetID == "" && validID(r.Application)
		return r.StartupAttemptID == "" && (validTargetLaunch || validLegacyLaunch) && r.ObserveRequest == nil && r.Action == nil && r.ObservationID == ""
	case OpObserve:
		return r.StartupAttemptID == "" && r.ObserveRequest != nil && r.ObserveRequest.SessionID == r.SessionID &&
			optionalID(r.ObserveRequest.DisplayID) && optionalID(r.ObserveRequest.WindowID) && r.Action == nil && r.ObservationID == ""
	case OpExecute, OpExecuteTurn:
		return r.StartupAttemptID == "" && r.Action != nil && r.Action.SessionID == r.SessionID && validAction(*r.Action) && r.ObserveRequest == nil && r.ObservationID == ""
	case OpImage:
		return r.StartupAttemptID == "" && validID(r.ObservationID) && r.ObserveRequest == nil && r.Action == nil
	default:
		return false
	}
}

// The client can check shape and bounds but cannot validate freshness, approval,
// coordinates against the actual screenshot, or action budgets: the host must.
func validAction(a cu.Action) bool {
	if !validID(a.ID) || !validID(a.SessionID) || !optionalID(a.ObservationID) ||
		!optionalID(string(a.TargetID)) || !optionalID(a.DisplayID) || !optionalID(a.WindowID) {
		return false
	}
	if a.Kind == cu.ActionLaunchApp {
		return a.TargetID != "" && a.ObservationID == "" && validID(string(a.TargetID))
	}
	if !a.Kind.IsInput() && a.Kind != cu.ActionWait {
		return false
	}
	if a.ObservationID == "" {
		return false
	}
	if len(a.Text) > cu.MaxInputBytes || !utf8.ValidString(a.Text) || len(a.Key) > cu.MaxKeyBytes || !utf8.ValidString(a.Key) ||
		len(a.Keys) > cu.MaxHotkeyKeys || !optionalID(a.Button) || a.DurationMS < 0 || a.DurationMS > cu.MaxActionDurationMS ||
		a.DeltaX < -cu.MaxScrollDelta || a.DeltaX > cu.MaxScrollDelta || a.DeltaY < -cu.MaxScrollDelta || a.DeltaY > cu.MaxScrollDelta {
		return false
	}
	for _, key := range a.Keys {
		if strings.TrimSpace(key) == "" || len(key) > cu.MaxKeyBytes || !utf8.ValidString(key) {
			return false
		}
	}
	if a.Expected != nil && (!optionalID(a.Expected.WindowID) || !optionalID(a.Expected.Hash)) {
		return false
	}
	if a.Point != nil && (a.Point.X < 0 || a.Point.Y < 0) {
		return false
	}
	switch a.Kind {
	case cu.ActionClick, cu.ActionDoubleClick, cu.ActionRightClick, cu.ActionMove:
		return a.Point != nil
	case cu.ActionType:
		return a.Text != ""
	case cu.ActionKey:
		return strings.TrimSpace(a.Key) != ""
	case cu.ActionHotkey:
		return len(a.Keys) > 0
	case cu.ActionScroll:
		return a.DeltaX != 0 || a.DeltaY != 0
	default:
		return true
	}
}

func validVisualCueResponse(response VisualCueResponse, sessionID string) bool {
	if response.Cue == nil {
		return true
	}
	cue := response.Cue
	if cue.SessionID != sessionID || cue.ActionID == "" || cue.CreatedAt.IsZero() || cue.ExpiresAt.IsZero() || !cue.ExpiresAt.After(cue.CreatedAt) {
		return false
	}
	if cue.Phase == cu.VisualCueHidden {
		return true
	}
	return cue.WindowID != "" && cue.GlobalPoint != nil
}

func validObservation(o cu.Observation, r cu.ObserveRequest) bool {
	return validID(o.ID) && o.SessionID == r.SessionID &&
		(r.DisplayID == "" || r.DisplayID == o.DisplayID) && (r.WindowID == "" || r.WindowID == o.WindowID) &&
		o.Width > 0 && o.Height > 0 && o.Width <= MaxImagePixels/o.Height && o.ScaleFactor > 0 &&
		o.Capabilities.Validate() == nil && !o.ObservedAt.IsZero() &&
		(o.ExpiresAt.IsZero() || o.ExpiresAt.After(o.ObservedAt))
}

func validReceipt(r cu.ActionReceipt, a cu.Action) bool {
	if r.ActionID != a.ID || r.SessionID != a.SessionID || !r.IsTerminal() || r.Duration < 0 ||
		(r.BeforeObservationID != "" && r.BeforeObservationID != a.ObservationID) {
		return false
	}
	switch r.Verification {
	case cu.VerificationNotChecked, cu.VerificationPassed, cu.VerificationFailed, cu.VerificationUnknown:
	default:
		return false
	}
	if r.Outcome == cu.OutcomeUnknown && r.Verification != cu.VerificationUnknown {
		return false
	}
	return r.Outcome == cu.OutcomeExecuted || r.Verification != cu.VerificationPassed
}

func validLaunchReceipt(r cu.LaunchReceipt, sessionID, targetID, application string) bool {
	if !validID(sessionID) || !r.IsTerminal() || r.Duration < 0 || r.CompletedAt.IsZero() {
		return false
	}
	if targetID != "" {
		if r.TargetID != cu.TargetID(targetID) || r.DisplayName == "" {
			return false
		}
	} else if application == "" || string(r.Application) != application {
		return false
	}
	if r.Outcome != cu.OutcomeExecuted {
		return r.ErrorCode != ""
	}
	return r.Window.ID != "" && r.Window.OwnerPID > 0 && r.BundleID != "" && r.Window.BundleID == r.BundleID
}

func validTurnResultForAction(result cu.ComputerTurnResult, action cu.Action) bool {
	if result.Validate() != nil || result.SessionID != action.SessionID || result.ActionID != action.ID || result.ActionKind != action.Kind {
		return false
	}
	if action.Kind == cu.ActionLaunchApp && (result.LaunchReceipt == nil || result.LaunchReceipt.TargetID != action.TargetID) {
		return false
	}
	return result.Receipt.BeforeObservationID == "" || result.Receipt.BeforeObservationID == action.ObservationID
}

func validateSessionSnapshot(snapshot SessionSnapshotResponse, requestedID string) error {
	if !validID(snapshot.SessionID) || (requestedID != "" && snapshot.SessionID != requestedID) || snapshot.Owner.TenantID == 0 || snapshot.Owner.UserID == 0 || snapshot.Capabilities.Validate() != nil {
		return ErrInvalidResponse
	}
	switch snapshot.State {
	case cu.SessionPendingApproval, cu.SessionReady, cu.SessionPaused, cu.SessionNeedsObservation, cu.SessionStopped, cu.SessionFailed:
	default:
		return ErrInvalidResponse
	}
	if snapshot.Observation != nil && (snapshot.Observation.SessionID != snapshot.SessionID || snapshot.Observation.Capabilities.Validate() != nil) {
		return ErrInvalidResponse
	}
	if snapshot.LastReceipt != nil && snapshot.LastReceipt.SessionID != snapshot.SessionID {
		return ErrInvalidResponse
	}
	return nil
}
