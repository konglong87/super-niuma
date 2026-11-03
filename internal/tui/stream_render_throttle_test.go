package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestStreamingRenderThrottledByInterval pins the clock and feeds several
// markdown text deltas within one throttle window. Without throttling every
// delta triggers a full viewport re-render (the O(N²) streaming-render bug that
// makes ~1-delta-per-token providers hang for minutes). With throttling, deltas
// inside one interval coalesce into a single render, and advancing the clock
// past the interval allows the next render — while the accumulated content is
// never dropped.
func TestStreamingRenderThrottledByInterval(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	model.streamingActive = true

	fixed := time.Unix(1_000, 0)
	model.now = func() time.Time { return fixed }

	renders := 0
	refreshViewportHook = func() { renders++ }
	t.Cleanup(func() { refreshViewportHook = nil })

	// Four markdown deltas, all within the same throttle window.
	for _, chunk := range []string{"**alpha** ", "beta ", "gamma ", "delta"} {
		updated, _ := model.Update(streamEventMsg{
			event: StreamEvent{Type: StreamText, Text: chunk},
			ch:    events,
		})
		model = updated.(Model)
	}
	if renders != 1 {
		t.Fatalf("text deltas within one throttle window should coalesce into 1 render, got %d", renders)
	}

	// Advancing past the interval permits one more render.
	fixed = fixed.Add(streamRenderThrottleInterval + time.Millisecond)
	updated, _ := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamText, Text: " epsilon"},
		ch:    events,
	})
	model = updated.(Model)
	if renders != 2 {
		t.Fatalf("a delta after the throttle interval elapses should render, got %d", renders)
	}

	// Throttling must never drop content: the final view has every delta.
	view := model.View()
	for _, want := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if !strings.Contains(view, want) {
			t.Fatalf("throttled streaming dropped content %q:\n%s", want, view)
		}
	}
}
