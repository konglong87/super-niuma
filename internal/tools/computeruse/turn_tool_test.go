package computeruse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type atomicTurnService struct {
	*serviceStub
	turn cu.ComputerTurnResult
	err  error
}

func (s *atomicTurnService) ExecuteTurn(_ context.Context, owner cu.SessionOwner, action cu.Action) (cu.ComputerTurnResult, error) {
	s.record(owner)
	s.order = append(s.order, "execute_turn")
	s.lastAction = action
	result := s.turn
	result.ActionID = action.ID
	result.SessionID = action.SessionID
	result.ActionKind = action.Kind
	result.Receipt.ActionID = action.ID
	result.Receipt.SessionID = action.SessionID
	result.Receipt.BeforeObservationID = action.ObservationID
	result.Receipt.RedactedActionSummary = action.RedactedSummary()
	return result, s.err
}

func atomicExecutedTurn(t *testing.T, service *atomicTurnService) {
	t.Helper()
	now := time.Now().Add(-time.Second)
	observation := service.observation
	observation.Capabilities = cu.Capabilities{
		ProtocolVersion: cu.ProtocolVersion, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady, InputReadiness: cu.ReadinessReady, PermissionState: cu.PermissionApproved,
		CoordinateSpace: cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels, Width: 10, Height: 10, ScaleFactor: 1},
	}
	observation.SessionID = testComputerSession
	observation.ObservedAt = now
	observation.ExpiresAt = now.Add(time.Minute)
	data := service.image
	media := cu.NewMediaRef(testFreshImageID, pngMediaType, data, observation.Width, observation.Height)
	observation.Screenshot = media
	action := cu.Action{ID: "placeholder", SessionID: testComputerSession, ObservationID: observation.ID, Kind: cu.ActionClick}
	service.turn = cu.ComputerTurnResult{
		ProtocolVersion: cu.ProtocolVersion,
		TurnID:          "turn-tool-1",
		SessionID:       action.SessionID,
		ActionID:        action.ID,
		ActionKind:      action.Kind,
		DispatchState:   cu.DispatchComplete,
		Outcome:         cu.OutcomeExecuted,
		Verification:    cu.VerificationNotChecked,
		Receipt: cu.ActionReceipt{
			ActionID: action.ID, SessionID: action.SessionID, BeforeObservationID: action.ObservationID,
			Outcome: cu.OutcomeExecuted, DispatchState: cu.DispatchComplete,
			Verification: cu.VerificationNotChecked, RedactedActionSummary: action.RedactedSummary(), CompletedAt: now,
		},
		Observation:      &observation,
		ObservationState: cu.ObservationReady,
		Screenshot:       &media,
		ScreenshotState:  cu.ScreenshotReady,
		RetryPolicy:      cu.RetryNever,
		Sequence:         1,
		StartedAt:        now,
		CompletedAt:      now,
		Duration:         time.Millisecond,
		ScreenshotData:   data,
	}
}

func TestToolUsesOneAtomicTurnResultAndDoesNotReadImageOrObserveAgain(t *testing.T) {
	base := screenshotService(t)
	service := &atomicTurnService{serviceStub: base}
	atomicExecutedTurn(t, service)

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`), testContext(service))
	if result.IsError {
		t.Fatalf("atomic turn failed: %s", result.Content)
	}
	if len(result.ContextMessages) != 1 || service.imageID != "" || len(service.order) != 1 || service.order[0] != "execute_turn" {
		t.Fatalf("tool stitched posthoc calls: messages=%d image=%q order=%v", len(result.ContextMessages), service.imageID, service.order)
	}
	payload := resultPayload(t, result)
	if _, ok := payload["turn_result"]; !ok {
		t.Fatalf("atomic turn result missing from payload: %s", result.Content)
	}
	if _, ok := payload["observation"]; !ok {
		t.Fatalf("next observation missing from payload: %s", result.Content)
	}
}

func TestToolUnknownAtomicTurnTellsModelDispatchStateAndNeverReplays(t *testing.T) {
	base := screenshotService(t)
	service := &atomicTurnService{serviceStub: base}
	now := time.Now()
	action := cu.Action{ID: "placeholder", SessionID: testComputerSession, ObservationID: "obs-1", Kind: cu.ActionClick}
	service.turn = cu.ComputerTurnResult{
		ProtocolVersion: cu.ProtocolVersion, TurnID: "turn-unknown", SessionID: action.SessionID, ActionID: action.ID, ActionKind: action.Kind,
		DispatchState: cu.DispatchUnknown, Outcome: cu.OutcomeUnknown, Verification: cu.VerificationUnknown,
		Receipt:          cu.ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, BeforeObservationID: action.ObservationID, Outcome: cu.OutcomeUnknown, DispatchState: cu.DispatchUnknown, Verification: cu.VerificationUnknown, CompletedAt: now},
		ObservationState: cu.ObservationInvalidated, ScreenshotState: cu.ScreenshotNotRequested, RetryPolicy: cu.RetryNever,
		Sequence: 1, StartedAt: now, CompletedAt: now,
	}

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"click","observation_id":"obs-1"}`), testContext(service))
	if !result.IsError || len(service.order) != 1 || service.order[0] != "execute_turn" {
		t.Fatalf("unknown turn was replayed or accepted: result=%+v order=%v", result, service.order)
	}
	if !strings.Contains(result.Content, `"dispatch_state":"unknown"`) || !strings.Contains(result.Content, "do not replay") {
		t.Fatalf("unknown dispatch guidance missing: %s", result.Content)
	}
}

type atomicLaunchService struct {
	*atomicTurnService
	legacyLaunchCalls     int
	sessionID             string
	forceEmptyObservation bool
}

func (s *atomicLaunchService) EnsureComputerSession(context.Context, cu.SessionOwner) (string, error) {
	return s.sessionID, nil
}

func (s *atomicLaunchService) CurrentComputerSession(context.Context, cu.SessionOwner) (string, error) {
	return s.sessionID, nil
}

func (s *atomicLaunchService) CurrentComputerObservation(context.Context, cu.SessionOwner) (string, error) {
	if s.forceEmptyObservation {
		return "", nil
	}
	return s.observation.ID, nil
}

func (s *atomicLaunchService) LaunchTarget(context.Context, cu.SessionOwner, string, cu.TargetID) (cu.LaunchReceipt, error) {
	s.legacyLaunchCalls++
	return cu.LaunchReceipt{Outcome: cu.OutcomeExecuted, ErrorCode: "legacy_launch_should_not_run", CompletedAt: time.Now()}, nil
}

func atomicExecutedLaunchTurn(t *testing.T, service *atomicLaunchService) {
	t.Helper()
	atomicExecutedTurn(t, service.atomicTurnService)
	service.turn.ActionKind = cu.ActionLaunchApp
	service.turn.Receipt.BeforeObservationID = ""
	service.turn.Receipt.RedactedActionSummary = string(cu.ActionLaunchApp)
	service.turn.LaunchReceipt = &cu.LaunchReceipt{
		TargetID:    cu.TargetID("workbuddy"),
		DisplayName: "WorkBuddy",
		BundleID:    cu.WorkBuddyBundleID,
		Window:      cu.WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID, IsVisible: true, IsFrontmost: true},
		Outcome:     cu.OutcomeExecuted,
		CompletedAt: time.Now(),
	}
}

func TestToolLaunchAppUsesOneAtomicTurnAndNeverCallsLegacyLaunch(t *testing.T) {
	base := screenshotService(t)
	service := &atomicLaunchService{atomicTurnService: &atomicTurnService{serviceStub: base}, sessionID: testComputerSession}
	atomicExecutedLaunchTurn(t, service)

	result := New().Run(context.Background(), json.RawMessage(`{"session_id":"computer-1","action":"launch_app","target_id":"workbuddy"}`), testContext(service))
	if result.IsError {
		t.Fatalf("atomic launch failed: %s", result.Content)
	}
	if service.legacyLaunchCalls != 0 || len(service.order) != 1 || service.order[0] != "execute_turn" {
		t.Fatalf("launch path stitched or used legacy RPC: legacy=%d order=%v", service.legacyLaunchCalls, service.order)
	}
	payload := resultPayload(t, result)
	if _, ok := payload["turn_result"]; !ok {
		t.Fatalf("atomic launch result missing turn_result: %s", result.Content)
	}
	if _, ok := payload["launch_receipt"]; !ok {
		t.Fatalf("atomic launch result missing launch_receipt: %s", result.Content)
	}
	if len(result.ContextMessages) != 1 {
		t.Fatalf("atomic launch result missing screenshot: %+v", result)
	}
}

func TestFastPathFirstObserveUsesAtomicLaunchTurnWithoutPreObserve(t *testing.T) {
	base := screenshotService(t)
	service := &atomicLaunchService{atomicTurnService: &atomicTurnService{serviceStub: base}, sessionID: testComputerSession}
	service.forceEmptyObservation = true
	atomicExecutedLaunchTurn(t, service)

	tc := testContext(service)
	tc.ComputerUseFastPath = true
	result := New().Run(context.Background(), json.RawMessage(`{"action":"observe","target_id":"workbuddy"}`), tc)
	if result.IsError {
		t.Fatalf("atomic first observe failed: %s", result.Content)
	}
	if service.legacyLaunchCalls != 0 || len(service.order) != 1 || service.order[0] != "execute_turn" {
		t.Fatalf("first observe used posthoc launch/observe: legacy=%d order=%v", service.legacyLaunchCalls, service.order)
	}
	if service.lastAction.Kind != cu.ActionLaunchApp || service.lastAction.TargetID != cu.TargetID("workbuddy") {
		t.Fatalf("atomic launch action = %+v", service.lastAction)
	}
}
