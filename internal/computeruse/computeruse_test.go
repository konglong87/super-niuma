package computeruse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func readyCapabilities() Capabilities {
	return Capabilities{
		ProtocolVersion:  ProtocolVersion,
		Platform:         PlatformMacOS,
		Backend:          BackendNativeHost,
		CaptureReadiness: ReadinessReady,
		InputReadiness:   ReadinessReady,
		FocusState:       FocusFocused,
		PermissionState:  PermissionApproved,
		CoordinateSpace:  CoordinateSpace{Origin: OriginTopLeft, Unit: CoordinatePixels, Width: 800, Height: 600, ScaleFactor: 2},
		Actions:          []ActionKind{ActionClick, ActionType, ActionWait},
		ImageSupported:   true,
		SupportsPause:    true,
		SupportsStop:     true,
	}
}

func readyObservation(sessionID string, now time.Time) Observation {
	return Observation{
		ID: "obs-1", SessionID: sessionID, Width: 800, Height: 600,
		Capabilities: readyCapabilities(), ObservedAt: now, ExpiresAt: now.Add(time.Minute),
	}
}

func newTestSession(t *testing.T, requireApproval bool) (*ComputerSession, SessionOwner, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	owner := SessionOwner{TenantID: 7, UserID: 11}
	session, err := NewComputerSession(SessionOptions{ID: "computer-1", Owner: owner, Capabilities: readyCapabilities(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if !requireApproval {
		if err := session.Approve(owner); err != nil {
			t.Fatal(err)
		}
	}
	return session, owner, now
}

func TestActionRejectsStaleObservation(t *testing.T) {
	session, _, now := newTestSession(t, false)
	if err := session.SetObservation(readyObservation(session.ID(), now)); err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "action-1", SessionID: session.ID(), ObservationID: "old", Kind: ActionClick, Point: &Point{X: 10, Y: 20}}
	if err := session.ValidateAction(action); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("ValidateAction error = %v, want stale observation error", err)
	}
}

func TestUnknownReceiptRequiresFreshObservationAndNeverReplays(t *testing.T) {
	session, _, now := newTestSession(t, false)
	if err := session.SetObservation(readyObservation(session.ID(), now)); err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "action-1", SessionID: session.ID(), ObservationID: "obs-1", Kind: ActionType, Text: "secret"}
	if err := session.ValidateAction(action); err != nil {
		t.Fatal(err)
	}
	if err := session.BeginAction(action); err != nil {
		t.Fatal(err)
	}
	if err := session.RecordReceipt(ActionReceipt{ActionID: action.ID, SessionID: session.ID(), Outcome: OutcomeUnknown}); err != nil {
		t.Fatal(err)
	}
	if session.State() != SessionFailed {
		t.Fatalf("state = %s, want %s", session.State(), SessionFailed)
	}
	if err := session.ValidateAction(action); err == nil {
		t.Fatal("unknown outcome allowed input replay")
	}
	fresh := readyObservation(session.ID(), now)
	fresh.ID = "obs-2"
	if err := session.SetObservation(fresh); err == nil {
		t.Fatal("unknown input restored authority through a new observation")
	}
	action.ID = "action-2"
	if action.ObservationID = fresh.ID; session.ValidateAction(action) == nil {
		t.Fatal("unknown input replayed under a new ID")
	}
}

func TestSessionApprovalOwnershipPauseAndStop(t *testing.T) {
	session, owner, _ := newTestSession(t, true)
	if session.State() != SessionPendingApproval {
		t.Fatalf("state = %s, want pending approval", session.State())
	}
	if err := session.Approve(SessionOwner{TenantID: 7, UserID: 99}); err == nil {
		t.Fatal("wrong owner approved session")
	}
	if err := session.Approve(owner); err != nil {
		t.Fatal(err)
	}
	if err := session.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := session.ValidateAction(Action{ID: "a", SessionID: session.ID(), Kind: ActionObserve}); err == nil {
		t.Fatal("paused session accepted action")
	}
	if err := session.Resume(owner); err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(owner); err != nil {
		t.Fatal(err)
	}
	if session.Approved() || session.State() != SessionStopped {
		t.Fatalf("stopped session = state=%s approved=%v", session.State(), session.Approved())
	}
	if err := session.Resume(owner); err == nil {
		t.Fatal("stopped session resumed")
	}
}

func TestSensitiveActionSummaryDoesNotContainText(t *testing.T) {
	action := Action{Kind: ActionType, Text: "password-123"}
	if got := action.RedactedSummary(); strings.Contains(got, action.Text) || !strings.Contains(got, "text_length") {
		t.Fatalf("redacted summary = %q", got)
	}
	encoded, err := json.Marshal(ActionReceipt{RedactedActionSummary: action.RedactedSummary()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), action.Text) {
		t.Fatalf("receipt leaked sensitive input: %s", encoded)
	}
}

func TestCapabilitiesAreCrossPlatformNeutral(t *testing.T) {
	caps := readyCapabilities()
	encoded, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"CGEvent", "Quartz", "ScreenCaptureKit", "AXUIElement", "Win32", "SendInput"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("capabilities leaked platform implementation detail %q: %s", forbidden, encoded)
		}
	}
}

type deadlineWaitBackend struct {
	Backend
	remaining time.Duration
}

func (b *deadlineWaitBackend) Execute(ctx context.Context, a Action) (ActionReceipt, error) {
	deadline, _ := ctx.Deadline()
	b.remaining = time.Until(deadline)
	return ActionReceipt{ActionID: a.ID, SessionID: a.SessionID, Outcome: OutcomeExecuted, DispatchState: DispatchNotStarted}, nil
}
func TestControllerMaximumWaitReservesEvidenceTime(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	if err := session.SetObservation(readyObservation(session.ID(), now)); err != nil {
		t.Fatal(err)
	}
	backend := &deadlineWaitBackend{}
	controller, _ := NewController(session, backend)
	action := Action{ID: "wait-deadline", SessionID: session.ID(), ObservationID: "obs-1", Kind: ActionWait, DurationMS: MaxActionDurationMS}
	if _, err := controller.Execute(context.Background(), owner, action); err != nil {
		t.Fatal(err)
	}
	if backend.remaining < 11*time.Second || backend.remaining > 12*time.Second {
		t.Fatalf("maximum wait deadline=%s; needs bounded 2s evidence grace", backend.remaining)
	}
}

func TestActionExecutionTimeoutIsBoundedAndKindScoped(t *testing.T) {
	baseline := time.Duration(MaxActionDurationMS) * time.Millisecond
	for _, kind := range []ActionKind{ActionWait, ActionDrag} {
		if timeout := ActionExecutionTimeout(Action{Kind: kind, DurationMS: MaxActionDurationMS}, baseline); timeout != baseline+ActionEvidenceGrace {
			t.Fatalf("%s timeout=%s", kind, timeout)
		}
	}
	for _, action := range []Action{{Kind: ActionType, DurationMS: MaxActionDurationMS}, {Kind: ActionWait, DurationMS: MaxActionDurationMS + 1}, {Kind: ActionWait, DurationMS: -1}} {
		if ActionExecutionTimeout(action, baseline) != baseline {
			t.Fatalf("unvalidated duration expanded deadline: %+v", action)
		}
	}
}
func TestControllerEvidenceGraceHonorsEarlierCallerDeadline(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	_ = session.SetObservation(readyObservation(session.ID(), now))
	backend := &deadlineWaitBackend{}
	controller, _ := NewController(session, backend)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := controller.Execute(ctx, owner, Action{ID: "wait-parent", SessionID: session.ID(), ObservationID: "obs-1", Kind: ActionWait, DurationMS: MaxActionDurationMS})
	if err != nil || backend.remaining > 100*time.Millisecond {
		t.Fatalf("caller deadline lost: remaining=%s err=%v", backend.remaining, err)
	}
}
