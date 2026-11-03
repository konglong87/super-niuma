package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestStreamTextAmendedReplacesLiveTail verifies the UI side of live
// streaming's reconciliation: when the accepted turn text diverges from what
// was streamed (sanitizers, provider failover), StreamTextAmended replaces the
// live tail; an empty final text retracts a gated turn's text entirely.
func TestStreamTextAmendedReplacesLiveTail(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	model.streamingActive = true
	fixed := time.Unix(1_000, 0)
	model.now = func() time.Time { return fixed }

	send := func(ev StreamEvent) {
		updated, _ := model.Update(streamEventMsg{event: ev, ch: events})
		model = updated.(Model)
	}

	send(StreamEvent{Type: StreamText, Text: "stable "})
	send(StreamEvent{Type: StreamText, Text: "draft tail"})

	// Amend: replace the streamed tail with the accepted text.
	send(StreamEvent{Type: StreamTextAmended, PrevText: "draft tail", Text: "final tail"})
	view := model.View()
	if !strings.Contains(view, "stable final tail") || strings.Contains(view, "draft") {
		t.Fatalf("amended view should show replaced tail:\n%s", view)
	}

	// Retract: a gated turn's entire text disappears.
	send(StreamEvent{Type: StreamTextAmended, PrevText: "stable final tail", Text: ""})
	view = model.View()
	if strings.Contains(view, "stable") || strings.Contains(view, "final tail") {
		t.Fatalf("retracted text should no longer be visible:\n%s", view)
	}
}
