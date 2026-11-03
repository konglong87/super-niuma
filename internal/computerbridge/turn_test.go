package computerbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func testTurnResult(t *testing.T) (cu.ComputerTurnResult, []byte) {
	t.Helper()
	now := time.Now().Add(-time.Second)
	data := testPNG(t)
	observation := testObservation()
	observation.Width = 2
	observation.Height = 3
	observation.ObservedAt = now
	observation.ExpiresAt = now.Add(time.Minute)
	observation.Screenshot = cu.NewMediaRef("screenshot-1", pngMediaType, data, observation.Width, observation.Height)
	action := testAction()
	receipt := testReceipt()
	receipt.DispatchState = cu.DispatchComplete
	receipt.CompletedAt = now
	receipt.Duration = time.Millisecond
	return cu.ComputerTurnResult{
		ProtocolVersion:  cu.ProtocolVersion,
		TurnID:           "turn-1",
		SessionID:        action.SessionID,
		ActionID:         action.ID,
		ActionKind:       action.Kind,
		DispatchState:    cu.DispatchComplete,
		Outcome:          cu.OutcomeExecuted,
		Verification:     cu.VerificationNotChecked,
		Receipt:          receipt,
		Observation:      &observation,
		ObservationState: cu.ObservationReady,
		Screenshot:       &observation.Screenshot,
		ScreenshotState:  cu.ScreenshotReady,
		RetryPolicy:      cu.RetryNever,
		Sequence:         1,
		StartedAt:        now,
		CompletedAt:      now,
		Duration:         time.Millisecond,
	}, data
}

func TestClientExecuteTurnReturnsBoundResultAndValidatedPNG(t *testing.T) {
	result, data := testTurnResult(t)
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op != OpExecuteTurn || req.Action == nil || req.Action.ID != result.ActionID || req.SessionID != result.SessionID {
			t.Fatalf("execute_turn binding lost: %+v", req)
		}
		writeData(t, w, ExecuteTurnResponse{
			Result:              result,
			ScreenshotData:      base64.StdEncoding.EncodeToString(data),
			ScreenshotMediaType: pngMediaType,
			ScreenshotExpiresAt: result.Observation.ExpiresAt,
		})
	})

	actual, err := client.ExecuteTurn(context.Background(), testOwner(), testAction())
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if actual.TurnID != result.TurnID || actual.Receipt.ActionID != result.Receipt.ActionID || actual.ScreenshotState != cu.ScreenshotReady {
		t.Fatalf("actual turn = %+v", actual)
	}
}

func TestClientExecuteTurnRejectsScreenshotHashMismatchWithoutDroppingReceipt(t *testing.T) {
	result, data := testTurnResult(t)
	result.Screenshot.SHA256 = strings.Repeat("0", 64)
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		_ = readRequest(t, r)
		writeData(t, w, ExecuteTurnResponse{
			Result:              result,
			ScreenshotData:      base64.StdEncoding.EncodeToString(data),
			ScreenshotMediaType: pngMediaType,
			ScreenshotExpiresAt: result.Observation.ExpiresAt,
		})
	})

	actual, err := client.ExecuteTurn(context.Background(), testOwner(), testAction())
	if !errors.Is(err, ErrInvalidImage) || actual.Receipt.ActionID != result.ActionID || actual.Receipt.Outcome != cu.OutcomeExecuted {
		t.Fatalf("hash mismatch result=%+v err=%v", actual, err)
	}
}

func TestValidRequestExecuteTurnRequiresBoundAction(t *testing.T) {
	owner := testOwner()
	action := testAction()
	if !validRequest(Request{Op: OpExecuteTurn, Owner: owner, SessionID: action.SessionID, Action: &action}) {
		t.Fatal("valid execute_turn request was rejected")
	}
	if validRequest(Request{Op: OpExecuteTurn, Owner: owner, SessionID: action.SessionID}) {
		t.Fatal("execute_turn request without action was accepted")
	}
}

type turnHostSpy struct {
	serverSpy
	result     cu.ComputerTurnResult
	imageData  []byte
	imageMedia string
	turnCalls  int
	imageCalls int
}

func (s *turnHostSpy) ExecuteTurn(context.Context, cu.SessionOwner, cu.Action) (cu.ComputerTurnResult, error) {
	s.turnCalls++
	return s.result, nil
}

func (s *turnHostSpy) TurnScreenshot(context.Context, cu.SessionOwner, cu.ComputerTurnResult) ([]byte, string, error) {
	s.imageCalls++
	return s.imageData, s.imageMedia, nil
}

func TestHandlerExecuteTurnUsesAtomicServiceAndInlineScreenshot(t *testing.T) {
	result, data := testTurnResult(t)
	host := &turnHostSpy{result: result, imageData: data, imageMedia: pngMediaType}
	h, err := NewHandler(serverTestToken, host)
	if err != nil {
		t.Fatal(err)
	}
	action := testAction()
	body, err := json.Marshal(Request{Op: OpExecuteTurn, Owner: testOwner(), SessionID: action.SessionID, Action: &action})
	if err != nil {
		t.Fatal(err)
	}
	response := serverCall(h, string(body), serverTestToken, "")
	if response.Code != http.StatusOK || host.turnCalls != 1 || host.imageCalls != 1 {
		t.Fatalf("response=%d turn_calls=%d image_calls=%d body=%s", response.Code, host.turnCalls, host.imageCalls, response.Body.String())
	}
	var envelope Response
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var out ExecuteTurnResponse
	if err := json.Unmarshal(envelope.Data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Result.TurnID != result.TurnID || out.ScreenshotData == "" || out.ScreenshotMediaType != pngMediaType {
		t.Fatalf("execute_turn response = %+v", out)
	}
}

func TestHandlerExecuteTurnDoesNotFallbackToLegacyExecute(t *testing.T) {
	host := &serverSpy{}
	h, err := NewHandler(serverTestToken, host)
	if err != nil {
		t.Fatal(err)
	}
	action := testAction()
	body, err := json.Marshal(Request{Op: OpExecuteTurn, Owner: testOwner(), SessionID: action.SessionID, Action: &action})
	if err != nil {
		t.Fatal(err)
	}
	response := serverCall(h, string(body), serverTestToken, "")
	if response.Code != http.StatusOK || host.calls != 0 {
		t.Fatalf("execute_turn fell back to legacy service: code=%d calls=%d body=%s", response.Code, host.calls, response.Body.String())
	}
}

func TestClientExecuteTurnPreservesUnknownReceiptWhenBridgeReportsErrorWithData(t *testing.T) {
	now := time.Now()
	action := testAction()
	unknown := cu.ComputerTurnResult{
		ProtocolVersion: cu.ProtocolVersion, TurnID: "turn-ambiguous", SessionID: action.SessionID, ActionID: action.ID, ActionKind: action.Kind,
		DispatchState: cu.DispatchUnknown, Outcome: cu.OutcomeUnknown, Verification: cu.VerificationUnknown,
		Receipt:          cu.ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, BeforeObservationID: action.ObservationID, Outcome: cu.OutcomeUnknown, DispatchState: cu.DispatchUnknown, Verification: cu.VerificationUnknown, ErrorCode: cu.ErrorCodeInputUncertain, CompletedAt: now},
		ObservationState: cu.ObservationInvalidated, ScreenshotState: cu.ScreenshotNotRequested, ErrorCode: cu.ErrorCodeInputUncertain, RetryPolicy: cu.RetryNever,
		Sequence: 1, StartedAt: now, CompletedAt: now,
	}
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		_ = readRequest(t, r)
		data, _ := json.Marshal(ExecuteTurnResponse{Result: unknown})
		_ = json.NewEncoder(w).Encode(Response{Data: data, Error: "host operation failed"})
	})

	result, err := client.ExecuteTurn(context.Background(), testOwner(), action)
	if err == nil || result.Outcome != cu.OutcomeUnknown || result.Receipt.ActionID != action.ID || result.RetryPolicy != cu.RetryNever {
		t.Fatalf("ambiguous response lost receipt: result=%+v err=%v", result, err)
	}
}

func TestClientExecuteTurnRejectsExpiredScreenshotTTL(t *testing.T) {
	result, data := testTurnResult(t)
	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		_ = readRequest(t, r)
		writeData(t, w, ExecuteTurnResponse{Result: result, ScreenshotData: base64.StdEncoding.EncodeToString(data), ScreenshotMediaType: pngMediaType, ScreenshotExpiresAt: time.Now().Add(-time.Second)})
	})
	actual, err := client.ExecuteTurn(context.Background(), testOwner(), testAction())
	if !errors.Is(err, ErrInvalidImage) || actual.Receipt.ActionID != result.ActionID || actual.Receipt.Outcome != cu.OutcomeExecuted {
		t.Fatalf("expired TTL result=%+v err=%v", actual, err)
	}
}

func TestClientExecuteTurnLaunchUsesAtomicRPCAndTargetBinding(t *testing.T) {
	result, data := testTurnResult(t)
	result.ActionKind = cu.ActionLaunchApp
	result.ActionID = "launch-action"
	result.Receipt.ActionID = result.ActionID
	result.Receipt.BeforeObservationID = ""
	result.LaunchReceipt = &cu.LaunchReceipt{
		TargetID:    cu.TargetID("workbuddy"),
		DisplayName: "WorkBuddy",
		BundleID:    cu.WorkBuddyBundleID,
		Window:      cu.WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: cu.WorkBuddyBundleID, IsVisible: true, IsFrontmost: true},
		Outcome:     cu.OutcomeExecuted,
		CompletedAt: result.CompletedAt,
	}
	action := testAction()
	action.ID = result.ActionID
	action.ObservationID = ""
	action.Kind = cu.ActionLaunchApp
	action.TargetID = cu.TargetID("workbuddy")
	result.SessionID = action.SessionID
	result.Receipt.SessionID = action.SessionID
	result.ActionKind = action.Kind
	result.Receipt.Outcome = cu.OutcomeExecuted
	result.Receipt.DispatchState = cu.DispatchComplete

	client := newUnixClient(t, 0, func(w http.ResponseWriter, r *http.Request) {
		req := readRequest(t, r)
		if req.Op != OpExecuteTurn || req.Action == nil || req.Action.Kind != cu.ActionLaunchApp || req.Action.TargetID != action.TargetID || req.TargetID != "" {
			t.Fatalf("launch did not use atomic execute_turn: %+v", req)
		}
		writeData(t, w, ExecuteTurnResponse{
			Result:              result,
			ScreenshotData:      base64.StdEncoding.EncodeToString(data),
			ScreenshotMediaType: pngMediaType,
			ScreenshotExpiresAt: result.Observation.ExpiresAt,
		})
	})

	actual, err := client.ExecuteTurn(context.Background(), testOwner(), action)
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if actual.LaunchReceipt == nil || actual.LaunchReceipt.TargetID != action.TargetID || actual.Receipt.ActionID != action.ID {
		t.Fatalf("launch turn binding lost: %+v", actual)
	}
}

func TestValidRequestExecuteTurnAcceptsLaunchWithoutObservation(t *testing.T) {
	action := testAction()
	action.Kind = cu.ActionLaunchApp
	action.ObservationID = ""
	action.TargetID = cu.TargetID("workbuddy")
	if !validRequest(Request{Op: OpExecuteTurn, Owner: testOwner(), SessionID: action.SessionID, Action: &action}) {
		t.Fatal("atomic launch request was rejected")
	}
	action.TargetID = ""
	if validRequest(Request{Op: OpExecuteTurn, Owner: testOwner(), SessionID: action.SessionID, Action: &action}) {
		t.Fatal("launch request without target was accepted")
	}
}
