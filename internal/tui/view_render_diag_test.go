package tui

import (
	"context"
	"testing"
	"time"
)

// TestViewDoesNotReRenderMarkdownEveryFrame asserts that once a streaming
// segment has been rendered into the viewport, calling View() (which Bubble Tea
// does on every frame) does NOT re-run the full markdown render. Before the fix
// View() called liveTranscriptView() unconditionally — a full glamour render —
// only to test non-emptiness, then discarded it and displayed the cached
// viewport content. That made render count scale with frame/delta count (the
// residual that kept long gpt-5.5 replies slow even with refreshViewport
// throttled).
func TestViewDoesNotReRenderMarkdownEveryFrame(t *testing.T) {
	events := make(chan StreamEvent, 8)
	model := NewModel(context.Background(), Options{
		RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil },
	})
	model.busy = true
	model.streamingActive = true
	fixed := time.Unix(1_000, 0)
	model.now = func() time.Time { return fixed }

	// Stream one markdown delta so the viewport holds rendered live content.
	updated, _ := model.Update(streamEventMsg{
		event: StreamEvent{Type: StreamText, Text: "## 标题\n**加粗** 正文内容"},
		ch:    events,
	})
	model = updated.(Model)

	// Now count markdown renders triggered purely by View() calls.
	renders := 0
	markdownRenderHook = func() { renders++ }
	t.Cleanup(func() { markdownRenderHook = nil })

	const frames = 5
	for i := 0; i < frames; i++ {
		_ = model.View()
	}
	if renders != 0 {
		t.Fatalf("View() must reuse the cached viewport content, not re-render markdown per frame; got %d renders over %d frames", renders, frames)
	}
}
