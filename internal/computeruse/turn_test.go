package computeruse

import (
	"testing"
	"time"
)

func validTurnResultForTest() ComputerTurnResult {
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	receipt := ActionReceipt{
		ActionID:              "action-1",
		SessionID:             "session-1",
		Outcome:               OutcomeExecuted,
		DispatchState:         DispatchComplete,
		Verification:          VerificationNotChecked,
		RedactedActionSummary: "click",
		CompletedAt:           started.Add(25 * time.Millisecond),
	}
	return ComputerTurnResult{
		ProtocolVersion:  ProtocolVersion,
		TurnID:           "turn-1",
		SessionID:        "session-1",
		ActionID:         "action-1",
		ActionKind:       ActionClick,
		DispatchState:    DispatchComplete,
		Outcome:          OutcomeExecuted,
		Verification:     VerificationNotChecked,
		Receipt:          receipt,
		ObservationState: ObservationNotRequested,
		ScreenshotState:  ScreenshotUnavailable,
		ErrorCode:        ErrorCodeScreenshotFailed,
		RetryPolicy:      RetryObserveOnly,
		Sequence:         1,
		StartedAt:        started,
		CompletedAt:      started.Add(25 * time.Millisecond),
		Duration:         25 * time.Millisecond,
	}
}

func TestComputerTurnResultValidateRequiresAuthoritativeReceipt(t *testing.T) {
	result := validTurnResultForTest()
	result.Receipt = ActionReceipt{}
	if err := result.Validate(); err == nil {
		t.Fatal("zero receipt was accepted as an authoritative turn result")
	}

	result = validTurnResultForTest()
	result.Receipt.ActionID = "other-action"
	if err := result.Validate(); err == nil {
		t.Fatal("receipt with a mismatched action ID was accepted")
	}
}

func TestComputerTurnResultValidateUnknownAndPartialAreNeverReplayable(t *testing.T) {
	cases := []struct {
		name          string
		dispatchState DispatchState
	}{
		{name: "unknown", dispatchState: DispatchUnknown},
		{name: "partial", dispatchState: DispatchPartial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := validTurnResultForTest()
			result.DispatchState = tc.dispatchState
			result.Outcome = OutcomeUnknown
			result.Verification = VerificationUnknown
			result.RetryPolicy = RetryObserveOnly
			result.Receipt.DispatchState = tc.dispatchState
			result.Receipt.Outcome = OutcomeUnknown
			result.Receipt.Verification = VerificationUnknown
			if err := result.Validate(); err == nil {
				t.Fatal("unknown or partial dispatch was allowed to be replayable")
			}
		})
	}
}

func TestComputerTurnResultValidateAllowsExecutedReceiptWhenScreenshotIsUnavailable(t *testing.T) {
	result := validTurnResultForTest()
	if err := result.Validate(); err != nil {
		t.Fatalf("executed receipt with unavailable screenshot rejected: %v", err)
	}
	if result.Receipt.ActionID == "" || result.Receipt.SessionID == "" {
		t.Fatal("screenshot failure dropped the authoritative receipt identity")
	}
}

func TestComputerSessionStoresTurnSequenceAndLastCommittedResult(t *testing.T) {
	session, _, _ := newTestSession(t, false)
	result := validTurnResultForTest()
	result.SessionID = session.ID()
	result.Receipt.SessionID = session.ID()
	result.Sequence = session.NextTurnSequence()
	if err := session.RecordTurnResult(result); err != nil {
		t.Fatalf("RecordTurnResult() error = %v", err)
	}

	current, ok := session.CurrentTurnResult()
	if !ok {
		t.Fatal("CurrentTurnResult() did not return the committed result")
	}
	if current.TurnID != result.TurnID || current.Sequence != result.Sequence {
		t.Fatalf("current turn = %+v, want %+v", current, result)
	}
	last, ok := session.LastCommittedReceipt()
	if !ok || last.ActionID != result.ActionID || last.SessionID != session.ID() {
		t.Fatalf("last committed receipt = %+v, ok=%v", last, ok)
	}
	if current.ScreenshotData != nil {
		t.Fatal("session turn snapshot retained raw screenshot bytes")
	}
}

func TestComputerTurnResultFailureMatrix(t *testing.T) {
	started := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name          string
		dispatchState DispatchState
		outcome       Outcome
		verification  VerificationStatus
		retry         RetryPolicy
		screenshot    ScreenshotState
		observation   ObservationState
		errorCode     string
	}{
		{
			name:          "pre-dispatch rejection",
			dispatchState: DispatchNotStarted,
			outcome:       OutcomeRejected,
			verification:  VerificationNotChecked,
			retry:         RetryObserveOnly,
			screenshot:    ScreenshotNotRequested,
			observation:   ObservationNotRequested,
			errorCode:     ErrorCodeInvalidAction,
		},
		{
			name:          "complete action with screenshot failure",
			dispatchState: DispatchComplete,
			outcome:       OutcomeExecuted,
			verification:  VerificationNotChecked,
			retry:         RetryObserveOnly,
			screenshot:    ScreenshotUnavailable,
			observation:   ObservationNotRequested,
			errorCode:     ErrorCodeScreenshotFailed,
		},
		{
			name:          "native partial dispatch",
			dispatchState: DispatchPartial,
			outcome:       OutcomeUnknown,
			verification:  VerificationUnknown,
			retry:         RetryNever,
			screenshot:    ScreenshotUnavailable,
			observation:   ObservationInvalidated,
			errorCode:     ErrorCodeInputUncertain,
		},
		{
			name:          "bridge response ambiguity",
			dispatchState: DispatchUnknown,
			outcome:       OutcomeUnknown,
			verification:  VerificationUnknown,
			retry:         RetryNever,
			screenshot:    ScreenshotUnavailable,
			observation:   ObservationInvalidated,
			errorCode:     ErrorCodeActionFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receipt := ActionReceipt{
				ActionID:              "action-1",
				SessionID:             "session-1",
				Outcome:               tc.outcome,
				DispatchState:         tc.dispatchState,
				Verification:          tc.verification,
				ErrorCode:             tc.errorCode,
				RedactedActionSummary: "click",
				CompletedAt:           started.Add(time.Millisecond),
			}
			result := ComputerTurnResult{
				ProtocolVersion:  ProtocolVersion,
				TurnID:           "turn-1",
				SessionID:        "session-1",
				ActionID:         "action-1",
				ActionKind:       ActionClick,
				DispatchState:    tc.dispatchState,
				Outcome:          tc.outcome,
				Verification:     tc.verification,
				Receipt:          receipt,
				ObservationState: tc.observation,
				ScreenshotState:  tc.screenshot,
				ErrorCode:        tc.errorCode,
				RetryPolicy:      tc.retry,
				Sequence:         1,
				StartedAt:        started,
				CompletedAt:      started.Add(time.Millisecond),
				Duration:         time.Millisecond,
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("failure matrix result rejected: %v", err)
			}
			if result.Receipt.ActionID == "" || result.Receipt.SessionID == "" {
				t.Fatal("failure matrix returned a zero receipt identity")
			}
			if tc.outcome == OutcomeUnknown && result.RetryPolicy != RetryNever {
				t.Fatal("unknown result was marked replayable")
			}
		})
	}
}
