package computeruse

import (
	"context"
	"errors"
	"testing"
	"time"
)

type recordingVisualCuePublisher struct {
	cues chan ActionVisualCue
}

func (p *recordingVisualCuePublisher) PublishVisualCue(cue ActionVisualCue) {
	p.cues <- cue
}

func TestNewActionVisualCueUsesObservationPixelAndGlobalPoint(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	before := readyObservation("session", now)
	before.DisplayID = "display-2"
	before.WindowID = "window-7"
	before.ActiveWindow = WindowRef{ID: "window-7", BundleID: "com.example.Target"}
	before.Capabilities.CoordinateSpace = CoordinateSpace{
		DisplayID:   "display-2",
		Origin:      OriginTopLeft,
		Unit:        CoordinatePixels,
		Width:       2000,
		Height:      1200,
		ScaleFactor: 2,
		Bounds:      &WindowFrame{X: -1440, Y: 32, Width: 1000, Height: 600},
	}
	action := Action{ID: "action-1", SessionID: "session", Kind: ActionClick, Point: &Point{X: 400, Y: 200}}
	receipt := ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: OutcomeExecuted, DispatchState: DispatchComplete}

	cue, ok := NewActionVisualCue("turn-1", 9, action, before, receipt, now)
	if !ok {
		t.Fatal("NewActionVisualCue() returned !ok")
	}
	if cue.Phase != VisualCueDispatched || cue.ImagePoint == nil || *cue.ImagePoint != *action.Point {
		t.Fatalf("cue = %+v, want dispatched image point", cue)
	}
	if cue.GlobalPoint == nil || cue.GlobalPoint.X != -1240 || cue.GlobalPoint.Y != 132 {
		t.Fatalf("global point = %+v, want (-1240,132)", cue.GlobalPoint)
	}
	if cue.AppBundleID != "com.example.Target" || cue.WindowID != "window-7" || cue.DisplayID != "display-2" {
		t.Fatalf("cue target binding = %+v", cue)
	}
}

func TestControllerPublishesVisualCueAfterReceiptBeforeObservation(t *testing.T) {
	backend := &turnImageBackend{
		FakeBackend: &FakeBackend{
			ObservationValue: readyObservation("", initialTestTime()),
			ReceiptValue:     ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete},
		},
		imageData: []byte("png-bytes"), mediaType: "image/png",
	}
	backend.ObservationValue.ID = "obs-after"
	controller, session, owner, initial := prepareTurnController(t, backend)
	initial.ID = "obs-cue-before"
	initial.DisplayID = "display-1"
	initial.WindowID = "window-1"
	initial.ActiveWindow = WindowRef{ID: "window-1", BundleID: "com.example.Target"}
	initial.Capabilities.CoordinateSpace = CoordinateSpace{
		DisplayID: "display-1", Origin: OriginTopLeft, Unit: CoordinatePixels,
		Width: 1000, Height: 800, ScaleFactor: 1,
		Bounds: &WindowFrame{X: 10, Y: 20, Width: 1000, Height: 800},
	}
	if err := session.SetObservation(initial); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingVisualCuePublisher{cues: make(chan ActionVisualCue, 2)}
	controller.SetVisualCuePublisher(publisher)
	action := Action{ID: "click-cue", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionClick, Point: &Point{X: 30, Y: 40}}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err != nil {
		t.Fatalf("ExecuteTurn() error = %v", err)
	}
	preparing := <-publisher.cues
	if preparing.Phase != VisualCuePreparing || preparing.GlobalPoint == nil {
		t.Fatalf("preparing cue = %+v", preparing)
	}
	cue := <-publisher.cues
	if _, ok := session.Receipt(action.ID); !ok {
		t.Fatal("visual cue was published before the authoritative receipt was recorded")
	}
	if cue.TurnID != result.TurnID || cue.Sequence != result.Sequence || cue.ActionID != action.ID {
		t.Fatalf("cue binding = %+v, result = %+v", cue, result)
	}
	if cue.Phase != VisualCueDispatched || cue.GlobalPoint == nil || cue.GlobalPoint.X != 40 || cue.GlobalPoint.Y != 60 {
		t.Fatalf("cue = %+v, want dispatched global point (40,60)", cue)
	}
}

func TestControllerPublishesUnknownVisualCueWithoutReplay(t *testing.T) {
	backend := &FakeBackend{
		ObservationValue: readyObservation("", initialTestTime()),
		ReceiptValue:     ActionReceipt{Outcome: OutcomeUnknown, DispatchState: DispatchUnknown},
		ExecuteError:     errors.New("bridge response lost"),
	}
	backend.ObservationValue.ID = "obs-before"
	controller, session, owner, initial := prepareTurnController(t, backend)
	publisher := &recordingVisualCuePublisher{cues: make(chan ActionVisualCue, 2)}
	controller.SetVisualCuePublisher(publisher)
	action := Action{ID: "unknown-cue", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionClick, Point: &Point{X: 10, Y: 20}}

	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil || result.Outcome != OutcomeUnknown || result.RetryPolicy != RetryNever {
		t.Fatalf("unknown turn = result=%+v err=%v", result, err)
	}
	preparing := <-publisher.cues
	if preparing.Phase != VisualCuePreparing {
		t.Fatalf("preparing cue = %+v", preparing)
	}
	cue := <-publisher.cues
	if cue.Phase != VisualCueUnknown || cue.DispatchState != DispatchUnknown {
		t.Fatalf("unknown cue = %+v", cue)
	}
	_, duplicateErr := controller.ExecuteTurn(context.Background(), owner, action)
	if duplicateErr == nil {
		t.Fatal("duplicate unknown action was accepted")
	}
	if len(backend.Executed) != 1 {
		t.Fatalf("backend executions = %d, want one dispatch", len(backend.Executed))
	}
	select {
	case extra := <-publisher.cues:
		t.Fatalf("duplicate action published an extra cue: %+v", extra)
	default:
	}
}

func TestVisualCueBusClonesAndClosesSlowSubscribers(t *testing.T) {
	bus := NewVisualCueBus()
	updates, cancel := bus.SubscribeVisualCues(1)
	defer cancel()
	now := time.Now()
	point := Point{X: 1, Y: 2}
	cue := ActionVisualCue{SessionID: "s", ActionID: "a", ActionKind: ActionClick, Phase: VisualCueDispatched, DispatchState: DispatchComplete, ImagePoint: &point, CreatedAt: now, ExpiresAt: now.Add(time.Second)}
	bus.PublishVisualCue(cue)
	point.X = 99
	bus.PublishVisualCue(cue)
	first := <-updates
	if first.ImagePoint == nil || first.ImagePoint.X != 1 {
		t.Fatalf("bus did not clone cue: %+v", first)
	}
	if _, ok := <-updates; ok {
		t.Fatal("slow visual cue subscriber remained open after overflow")
	}
}

func TestVisualCueBusPublishesHiddenCueWithoutAActionPoint(t *testing.T) {
	bus := NewVisualCueBus()
	updates, cancel := bus.SubscribeVisualCues(1)
	defer cancel()
	bus.HideVisualCue("session", "runtime", "")
	select {
	case cue := <-updates:
		if cue.Phase != VisualCueHidden || cue.ImagePoint != nil || cue.GlobalPoint != nil {
			t.Fatalf("hidden cue = %+v", cue)
		}
	case <-time.After(time.Second):
		t.Fatal("hidden cue was not published")
	}
}

func TestNewActionVisualCuePrefersNativeResolvedGlobalPoint(t *testing.T) {
	now := time.Now()
	before := readyObservation("session", now)
	before.Capabilities.CoordinateSpace = CoordinateSpace{
		Origin: OriginTopLeft, Unit: CoordinatePixels, Width: 800, Height: 600,
		ScaleFactor: 2, Bounds: &WindowFrame{X: 100, Y: 200, Width: 400, Height: 300},
	}
	action := Action{ID: "action-native-point", SessionID: "session", Kind: ActionClick, Point: &Point{X: 10, Y: 20}}
	receipt := ActionReceipt{ActionID: action.ID, SessionID: action.SessionID, Outcome: OutcomeExecuted, DispatchState: DispatchComplete,
		ActualGlobalPoint: &ScreenPoint{X: 777.5, Y: -12.25}}
	cue, ok := NewActionVisualCue("turn-native-point", 1, action, before, receipt, now)
	if !ok || cue.GlobalPoint == nil || *cue.GlobalPoint != *receipt.ActualGlobalPoint {
		t.Fatalf("cue global point = %+v, want native point %+v", cue.GlobalPoint, receipt.ActualGlobalPoint)
	}
}

func TestVisualCueBusLatestIsSessionBoundAndExpires(t *testing.T) {
	bus := NewVisualCueBus()
	now := time.Now()
	point := ScreenPoint{X: 10, Y: 20}
	cue := ActionVisualCue{SessionID: "session-1", ActionID: "action-1", ActionKind: ActionClick,
		Phase: VisualCueDispatched, DispatchState: DispatchComplete, WindowID: "window-1", GlobalPoint: &point,
		CreatedAt: now, ExpiresAt: now.Add(time.Second)}
	bus.PublishVisualCue(cue)
	if _, ok := bus.LatestVisualCue("other-session"); ok {
		t.Fatal("latest cue crossed session boundary")
	}
	got, ok := bus.LatestVisualCue("session-1")
	if !ok || got.ActionID != cue.ActionID || got.GlobalPoint == nil || *got.GlobalPoint != point {
		t.Fatalf("latest cue = %+v ok=%v", got, ok)
	}
	bus.HideVisualCue("session-1", "runtime", "")
	hidden, ok := bus.LatestVisualCue("session-1")
	if !ok || hidden.Phase != VisualCueHidden {
		t.Fatalf("hidden cue was not retained for bridge delivery: %+v ok=%v", hidden, ok)
	}
	if hidden.ExpiresAt.Sub(hidden.CreatedAt) < 100*time.Millisecond {
		t.Fatalf("hidden cue expires before the remote bridge can poll it: created=%s expires=%s", hidden.CreatedAt, hidden.ExpiresAt)
	}
}
