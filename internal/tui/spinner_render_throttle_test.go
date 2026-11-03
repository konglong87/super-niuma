package tui

import (
	"context"
	"testing"
	"time"
)

// TestSpinnerTickRenderThrottledWhileBusy reproduces the real-session freeze
// (session 94a17fb1): during a long streamed reply the 120ms spinner tick
// called refreshViewport unconditionally — a full markdown render that bypassed
// the stream-delta throttle. Once a render costs more than the tick interval
// the event queue saturates and text deltas plus Ctrl+C starve ("fast at first,
// then grinds to a halt"). Spinner ticks must share the same render throttle.
func TestSpinnerTickRenderThrottledWhileBusy(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	model.streamingActive = true
	fixed := time.Unix(1_000, 0)
	model.now = func() time.Time { return fixed }

	// One delta renders and stamps the throttle window at the pinned clock.
	updated, _ := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamText, Text: "**alpha** body"},
		ch:    events,
	})
	model = updated.(Model)

	renders := 0
	refreshViewportHook = func() { renders++ }
	t.Cleanup(func() { refreshViewportHook = nil })

	// Spinner ticks inside the throttle window must not trigger renders.
	for i := 0; i < 5; i++ {
		updated, _ = model.Update(spinnerTickMsg{})
		model = updated.(Model)
	}
	if renders != 0 {
		t.Fatalf("spinner ticks within the throttle window must not re-render, got %d renders", renders)
	}

	// After the interval elapses one tick may render again.
	fixed = fixed.Add(streamRenderThrottleInterval + time.Millisecond)
	updated, _ = model.Update(spinnerTickMsg{})
	model = updated.(Model)
	if renders != 1 {
		t.Fatalf("spinner tick after the interval should render once, got %d", renders)
	}
}

// TestLiveRenderIntervalScalesWithCost verifies the adaptive throttle: the
// minimum gap between live renders grows with the measured cost of the last
// render (bounded above), so render work can never saturate the event loop no
// matter how large the accumulated markdown gets.
func TestLiveRenderIntervalScalesWithCost(t *testing.T) {
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})

	model.lastLiveRenderCost = 0
	if got := model.liveRenderInterval(); got != streamRenderThrottleInterval {
		t.Fatalf("zero-cost interval should be the base %v, got %v", streamRenderThrottleInterval, got)
	}

	model.lastLiveRenderCost = 200 * time.Millisecond
	if got := model.liveRenderInterval(); got != 600*time.Millisecond {
		t.Fatalf("interval should be 3x the last render cost, got %v", got)
	}

	model.lastLiveRenderCost = 10 * time.Second
	if got := model.liveRenderInterval(); got != streamRenderThrottleMaxInterval {
		t.Fatalf("interval should cap at %v, got %v", streamRenderThrottleMaxInterval, got)
	}
}
