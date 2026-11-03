package computeruse

import (
	"context"
	"errors"
	"testing"
	"time"
)

type turnImageBackend struct {
	*FakeBackend
	imageData   []byte
	mediaType   string
	imageErr    error
	imageCalls  int
	lastImageID string
}

func (b *turnImageBackend) ObservationImage(_ context.Context, observationID string) ([]byte, string, error) {
	b.imageCalls++
	b.lastImageID = observationID
	if b.imageErr != nil {
		return nil, "", b.imageErr
	}
	return b.imageData, b.mediaType, nil
}

type postObserveFailureBackend struct {
	*FakeBackend
	observeCalls int
	observeErr   error
}

func (b *postObserveFailureBackend) Observe(context.Context, ObserveRequest) (Observation, error) {
	b.observeCalls++
	return Observation{}, b.observeErr
}

func prepareTurnController(t *testing.T, backend Backend) (*Controller, *ComputerSession, SessionOwner, Observation) {
	t.Helper()
	session, owner, now := newTestSession(t, false)
	initial := readyObservation(session.ID(), now)
	initial.ID = "obs-before"
	if err := session.SetObservation(initial); err != nil {
		t.Fatal(err)
	}
	controller, err := NewController(session, backend)
	if err != nil {
		t.Fatal(err)
	}
	return controller, session, owner, initial
}

func TestControllerExecuteTurnReturnsReceiptObservationAndScreenshotFromOneTurn(t *testing.T) {
	backend := &turnImageBackend{
		FakeBackend: &FakeBackend{
			ObservationValue: readyObservation("", initialTestTime()),
			ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
		},
		imageData: []byte("png-bytes"),
		mediaType: "image/png",
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	action := Action{ID: "action-turn", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if result.Receipt.ActionID != action.ID || result.Receipt.SessionID != session.ID() {
		t.Fatalf("receipt = %+v, want action/session binding", result.Receipt)
	}
	if result.Outcome != OutcomeExecuted || result.DispatchState != DispatchComplete {
		t.Fatalf("turn state = outcome=%s dispatch=%s", result.Outcome, result.DispatchState)
	}
	if result.ObservationState != ObservationReady || result.Observation == nil || result.Observation.ID != "obs-after" {
		t.Fatalf("observation = state=%s value=%+v", result.ObservationState, result.Observation)
	}
	if result.ScreenshotState != ScreenshotReady || result.Screenshot == nil {
		t.Fatalf("screenshot = state=%s value=%+v", result.ScreenshotState, result.Screenshot)
	}
	if result.Screenshot.MediaType != "image/png" || result.Screenshot.SHA256 == "" {
		t.Fatalf("screenshot metadata = %+v", result.Screenshot)
	}
	if backend.imageCalls != 1 || backend.lastImageID != "obs-after" {
		t.Fatalf("image calls = %d id=%q, want one read for next observation", backend.imageCalls, backend.lastImageID)
	}
	stored, ok := session.CurrentTurnResult()
	if !ok || stored.TurnID != result.TurnID {
		t.Fatalf("stored turn = %+v, ok=%v", stored, ok)
	}
}

func TestControllerExecuteTurnScreenshotFailureKeepsExecutedReceipt(t *testing.T) {
	backend := &turnImageBackend{
		FakeBackend: &FakeBackend{
			ObservationValue: readyObservation("", initialTestTime()),
			ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
		},
		imageErr: errors.New("decode failed"),
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	action := Action{ID: "action-image-fail", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil {
		t.Fatal("ExecuteTurn() error = nil, want screenshot failure")
	}
	if result.Outcome != OutcomeExecuted || result.DispatchState != DispatchComplete {
		t.Fatalf("receipt was downgraded after screenshot failure: %+v", result)
	}
	if result.Receipt.ActionID == "" || result.Receipt.SessionID == "" || result.Receipt.Outcome != OutcomeExecuted {
		t.Fatalf("screenshot failure lost receipt: %+v", result.Receipt)
	}
	if result.ScreenshotState != ScreenshotUnavailable || result.RetryPolicy != RetryObserveOnly {
		t.Fatalf("screenshot failure policy = state=%s retry=%s", result.ScreenshotState, result.RetryPolicy)
	}
	if result.ObservationState != ObservationReady || result.Observation == nil {
		t.Fatalf("observation authority was lost with screenshot: state=%s value=%+v", result.ObservationState, result.Observation)
	}
}

func TestControllerExecuteTurnPostObserveFailureDoesNotRejectExecutedAction(t *testing.T) {
	backend := &postObserveFailureBackend{
		FakeBackend: &FakeBackend{
			ObservationValue: readyObservation("", initialTestTime()),
			ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
		},
		observeErr: errors.New("capture unavailable"),
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	action := Action{ID: "action-observe-fail", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil {
		t.Fatal("ExecuteTurn() error = nil, want observation failure")
	}
	if result.Outcome != OutcomeExecuted || result.Receipt.Outcome != OutcomeExecuted {
		t.Fatalf("post-action observe failure rejected executed action: %+v", result)
	}
	if result.ObservationState != ObservationUnavailable || result.RetryPolicy != RetryObserveOnly {
		t.Fatalf("observation failure policy = state=%s retry=%s", result.ObservationState, result.RetryPolicy)
	}
	if result.ScreenshotState != ScreenshotUnavailable {
		t.Fatalf("observation failure screenshot state = %s", result.ScreenshotState)
	}
	if session.State() == SessionFailed {
		t.Fatal("post-action observation failure permanently failed the session")
	}
}

func TestControllerExecuteTurnUnknownDispatchNeverReplays(t *testing.T) {
	backend := &FakeBackend{
		ObservationValue: readyObservation("", initialTestTime()),
		ReceiptValue:     ActionReceipt{Outcome: OutcomeUnknown, DispatchState: DispatchUnknown},
		ExecuteError:     errors.New("bridge response lost"),
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	action := Action{ID: "action-unknown", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil {
		t.Fatal("ExecuteTurn() error = nil, want unknown dispatch error")
	}
	if result.Outcome != OutcomeUnknown || result.DispatchState != DispatchUnknown || result.RetryPolicy != RetryNever {
		t.Fatalf("unknown result = %+v", result)
	}
	if result.Receipt.ActionID != action.ID || result.Receipt.SessionID != session.ID() || result.Receipt.Outcome != OutcomeUnknown {
		t.Fatalf("unknown result lost non-zero receipt: %+v", result.Receipt)
	}
	if len(backend.Executed) != 1 {
		t.Fatalf("backend execution count = %d, want one dispatch", len(backend.Executed))
	}
}

func initialTestTime() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }

type recordingTurnPublisher struct {
	results chan ComputerTurnResult
}

func (p *recordingTurnPublisher) PublishTurn(result ComputerTurnResult) {
	p.results <- result
}

func TestControllerExecuteTurnPublishesCommittedResultWithoutReconstruction(t *testing.T) {
	backend := &turnImageBackend{
		FakeBackend: &FakeBackend{
			ObservationValue: readyObservation("", initialTestTime()),
			ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
		},
		imageData: []byte("png-bytes"),
		mediaType: "image/png",
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	publisher := &recordingTurnPublisher{results: make(chan ComputerTurnResult, 1)}
	controller.SetTurnPublisher(publisher)
	action := Action{ID: "action-published", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	published := <-publisher.results
	if published.TurnID != result.TurnID || published.ActionID != result.ActionID || published.Receipt.ActionID != result.Receipt.ActionID {
		t.Fatalf("published = %+v, returned = %+v", published, result)
	}
	stored, ok := session.CurrentTurnResult()
	if !ok || stored.TurnID != published.TurnID {
		t.Fatalf("published result was not committed before event: stored=%+v ok=%v", stored, ok)
	}
}

type atomicLaunchBackend struct {
	*turnImageBackend
	launchReceipt LaunchReceipt
	launchErr     error
	launchCalls   int
	executeCalls  int
}

func (b *atomicLaunchBackend) LaunchApp(_ context.Context, sessionID, providerKey string) (LaunchReceipt, error) {
	b.launchCalls++
	if sessionID == "" || providerKey == "" {
		return LaunchReceipt{Outcome: OutcomeUnknown, ErrorCode: ErrorCodeInputUncertain, CompletedAt: time.Now()}, errors.New("invalid launch binding")
	}
	return b.launchReceipt, b.launchErr
}

func (b *atomicLaunchBackend) Execute(ctx context.Context, action Action) (ActionReceipt, error) {
	b.executeCalls++
	return b.FakeBackend.Execute(ctx, action)
}

func TestControllerExecuteTurnLaunchIsAtomicAndBindsFirstObservation(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID: "workbuddy", DisplayName: "WorkBuddy",
		Launch: LaunchPolicy{ProviderKey: "trusted-workbuddy"},
		Window: WindowPolicy{BundleID: WorkBuddyBundleID, RequireVisible: true, RequireFrontmost: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &atomicLaunchBackend{
		turnImageBackend: &turnImageBackend{
			FakeBackend: &FakeBackend{
				ObservationValue: readyObservation(session.ID(), now),
			},
			imageData: []byte("png-bytes"), mediaType: "image/png",
		},
		launchReceipt: LaunchReceipt{
			BundleID: WorkBuddyBundleID,
			Window:   WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: WorkBuddyBundleID, IsVisible: true, IsFrontmost: true},
			Outcome:  OutcomeExecuted, CompletedAt: now.Add(time.Millisecond),
		},
	}
	backend.ObservationValue.ID = "obs-after-launch"
	controller, err := NewControllerWithRegistry(session, backend, registry)
	if err != nil {
		t.Fatal(err)
	}

	action := Action{ID: "launch-turn-1", SessionID: session.ID(), TargetID: TargetID("workbuddy"), Kind: ActionLaunchApp}
	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if backend.launchCalls != 1 || backend.executeCalls != 0 {
		t.Fatalf("launch dispatch was not atomic: launch=%d execute=%d", backend.launchCalls, backend.executeCalls)
	}
	if result.Outcome != OutcomeExecuted || result.DispatchState != DispatchComplete || result.LaunchReceipt == nil {
		t.Fatalf("launch turn result = %+v", result)
	}
	if result.LaunchReceipt.Window.ID != "workbuddy-window" || result.ObservationState != ObservationReady || result.ScreenshotState != ScreenshotReady {
		t.Fatalf("launch evidence = launch=%+v observation=%s screenshot=%s", result.LaunchReceipt, result.ObservationState, result.ScreenshotState)
	}
	stored, ok := session.CurrentTurnResult()
	if !ok || stored.TurnID != result.TurnID || stored.LaunchReceipt == nil {
		t.Fatalf("authoritative launch turn not committed: stored=%+v ok=%v", stored, ok)
	}
}

func TestControllerExecuteTurnExecutedLaunchDoesNotInventFailureCode(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID: "workbuddy", DisplayName: "WorkBuddy",
		Launch: LaunchPolicy{ProviderKey: "trusted-workbuddy"},
		Window: WindowPolicy{BundleID: WorkBuddyBundleID, RequireVisible: true, RequireFrontmost: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &atomicLaunchBackend{
		turnImageBackend: &turnImageBackend{
			FakeBackend: &FakeBackend{ObservationValue: readyObservation(session.ID(), now)},
			imageData:   []byte("png-bytes"), mediaType: "image/png",
		},
		launchReceipt: LaunchReceipt{
			BundleID: WorkBuddyBundleID,
			Window:   WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: WorkBuddyBundleID, IsVisible: true, IsFrontmost: true},
			Outcome:  OutcomeExecuted, CompletedAt: now.Add(time.Millisecond),
		},
		launchErr: errors.New("post-dispatch diagnostic"),
	}
	backend.ObservationValue.ID = "obs-after-launch-error"
	controller, err := NewControllerWithRegistry(session, backend, registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTurn(context.Background(), owner, Action{ID: "launch-error-1", SessionID: session.ID(), TargetID: "workbuddy", Kind: ActionLaunchApp})
	if err != nil {
		t.Fatalf("executed launch returned an error: %v", err)
	}
	if result.Outcome != OutcomeExecuted || result.Receipt.Outcome != OutcomeExecuted || result.ErrorCode != "" || result.Receipt.ErrorCode != "" {
		t.Fatalf("executed launch invented failure metadata: result=%+v receipt=%+v", result, result.Receipt)
	}
}

func TestControllerExecuteTurnLaunchReopensObservedRunnerPhase(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	registry, err := NewStaticTargetRegistry(ApplicationTarget{
		ID: "workbuddy", DisplayName: "WorkBuddy",
		Launch: LaunchPolicy{ProviderKey: "trusted-workbuddy"},
		Window: WindowPolicy{BundleID: WorkBuddyBundleID, RequireVisible: true, RequireFrontmost: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend := &atomicLaunchBackend{
		turnImageBackend: &turnImageBackend{
			FakeBackend: &FakeBackend{ObservationValue: readyObservation(session.ID(), now)},
			imageData:   []byte("png-bytes"), mediaType: "image/png",
		},
		launchReceipt: LaunchReceipt{
			BundleID: WorkBuddyBundleID,
			Window:   WindowRef{ID: "workbuddy-window", OwnerPID: 84, BundleID: WorkBuddyBundleID, IsVisible: true, IsFrontmost: true},
			Outcome:  OutcomeExecuted, CompletedAt: now.Add(time.Millisecond),
		},
	}
	backend.ObservationValue.ID = "obs-after-launch-runner"
	controller, err := NewControllerWithRegistry(session, backend, registry)
	if err != nil {
		t.Fatal(err)
	}
	result, err := controller.ExecuteTurn(context.Background(), owner, Action{ID: "launch-runner-1", SessionID: session.ID(), TargetID: "workbuddy", Kind: ActionLaunchApp})
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	if result.ObservationState != ObservationReady {
		t.Fatalf("launch observation state = %s", result.ObservationState)
	}
	if got := controller.RunSnapshot().State; got != RunStateObserved {
		t.Fatalf("runner state after committed launch = %s, want %s", got, RunStateObserved)
	}
}

func TestDuplicateTurnKeepsOriginalDispatchFact(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeExecuted, OutcomeUnknown} {
		t.Run(string(outcome), func(t *testing.T) {
			dispatch := DispatchComplete
			var backendErr error
			if outcome == OutcomeUnknown {
				dispatch, backendErr = DispatchUnknown, errors.New("lost acknowledgement")
			}
			backend := &turnImageBackend{FakeBackend: &FakeBackend{
				ObservationValue: readyObservation("", initialTestTime()),
				ReceiptValue:     ActionReceipt{Outcome: outcome, DispatchState: dispatch}, ExecuteError: backendErr,
			}, imageData: []byte("png"), mediaType: "image/png"}
			backend.ObservationValue.ID = "duplicate-next"
			controller, session, owner, initial := prepareTurnController(t, backend)
			action := Action{ID: "duplicate-turn", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}
			first, _ := controller.ExecuteTurn(context.Background(), owner, action)
			duplicate, err := controller.ExecuteTurn(context.Background(), owner, action)
			if err == nil || duplicate.Validate() != nil || duplicate.Outcome != first.Outcome || duplicate.DispatchState != first.DispatchState || duplicate.Receipt.CompletedAt != first.Receipt.CompletedAt {
				t.Fatalf("duplicate rewrote original dispatch: first=%+v duplicate=%+v err=%v", first.Receipt, duplicate.Receipt, err)
			}
			stored, _ := session.Receipt(action.ID)
			current, _ := session.CurrentTurnResult()
			if stored.Outcome != first.Outcome || stored.DispatchState != first.DispatchState || current.TurnID != first.TurnID || len(backend.Executed) != 1 {
				t.Fatal("duplicate poisoned committed receipt/result or re-dispatched")
			}
		})
	}
}

func TestDuplicateOlderTurnKeepsReceiptWithoutRetainingPNGHistory(t *testing.T) {
	backend := &turnImageBackend{FakeBackend: &FakeBackend{
		ObservationValue: readyObservation("", initialTestTime()),
		ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
	}, imageData: []byte("png"), mediaType: "image/png"}
	backend.ObservationValue.ID = "after-first"
	controller, session, owner, initial := prepareTurnController(t, backend)
	firstAction := Action{ID: "first", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}
	first, err := controller.ExecuteTurn(context.Background(), owner, firstAction)
	if err != nil {
		t.Fatal(err)
	}
	backend.ObservationValue.ID = "after-second"
	secondAction := Action{ID: "second", SessionID: session.ID(), ObservationID: first.Observation.ID, Kind: ActionWait}
	second, err := controller.ExecuteTurn(context.Background(), owner, secondAction)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := controller.ExecuteTurn(context.Background(), owner, firstAction)
	if err == nil || duplicate.TurnID != first.TurnID || duplicate.DispatchState != DispatchComplete || len(backend.Executed) != 2 {
		t.Fatal("older action was replayed or its authoritative turn was replaced")
	}
	current, _ := session.CurrentTurnResult()
	if current.TurnID != second.TurnID {
		t.Fatal("duplicate replaced current authority")
	}
	if len(session.committedTurns) != 2 {
		t.Fatal("consumed action metadata was not retained")
	}
	for _, metadata := range session.committedTurns {
		if len(metadata.ScreenshotData) != 0 {
			t.Fatal("receipt recovery retained private PNG history")
		}
	}
}

type inflightTurnBackend struct {
	*turnImageBackend
	entered chan struct{}
	release chan struct{}
}

func (b *inflightTurnBackend) Execute(ctx context.Context, action Action) (ActionReceipt, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.FakeBackend.Execute(ctx, action)
	case <-ctx.Done():
		return ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: OutcomeUnknown, DispatchState: DispatchUnknown}, ctx.Err()
	}
}

func TestCancelledDuplicateOfInflightTurnNeverReturnsZeroReceipt(t *testing.T) {
	backend := &inflightTurnBackend{turnImageBackend: &turnImageBackend{FakeBackend: &FakeBackend{
		ObservationValue: readyObservation("", initialTestTime()),
		ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
	}, imageData: []byte("png"), mediaType: "image/png"}, entered: make(chan struct{}), release: make(chan struct{})}
	backend.ObservationValue.ID = "inflight-next"
	controller, session, owner, initial := prepareTurnController(t, backend)
	originalCtx, cancelOriginal := context.WithCancel(context.Background())
	defer cancelOriginal()
	action := Action{ID: "inflight", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}
	originalDone := make(chan ComputerTurnResult, 1)
	go func() { result, _ := controller.ExecuteTurn(originalCtx, owner, action); originalDone <- result }()
	<-backend.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	duplicate, err := controller.ExecuteTurn(ctx, owner, action)
	close(backend.release)
	original := <-originalDone
	if err == nil || duplicate.Validate() != nil || duplicate.Receipt.ActionID != action.ID || duplicate.DispatchState != DispatchUnknown || duplicate.RetryPolicy != RetryNever {
		t.Fatal("in-flight dispatch ambiguity returned zero/not_started receipt")
	}
	if original.Outcome != OutcomeExecuted || len(backend.Executed) != 1 {
		t.Fatal("duplicate canceled or replayed original turn")
	}
}
