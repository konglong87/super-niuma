package channel

import (
	"testing"
	"time"
)

func TestStreamBufferFlushesByCharacterThresholdAndTerminalState(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-1", now, 500*time.Millisecond, 5)
	if changed := buffer.Apply(Delta{Kind: DeltaText, Text: "hello"}); !changed {
		t.Fatal("first visible delta should change the buffer")
	}
	state, ok := buffer.Flush(now, false)
	if !ok || state.Text != "hello" || state.RenderVersion != 1 {
		t.Fatalf("first flush = %+v, ok=%v", state, ok)
	}
	if changed := buffer.Apply(Delta{Kind: DeltaText, Text: "!"}); !changed {
		t.Fatal("second delta should change the buffer")
	}
	if _, ok := buffer.Flush(now.Add(100*time.Millisecond), false); ok {
		t.Fatal("flush should be throttled before interval and threshold")
	}
	state, ok = buffer.Flush(now.Add(500*time.Millisecond), true)
	if !ok || state.Text != "hello!" || state.RenderVersion != 2 {
		t.Fatalf("terminal flush = %+v, ok=%v", state, ok)
	}
	if changed := buffer.Apply(Delta{Kind: DeltaText, Text: "late"}); changed {
		t.Fatal("late delta must be rejected after terminal flush")
	}
}

func TestStreamBufferSkipsUnchangedFlush(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-2", now, 0, 1)
	buffer.Apply(Delta{Kind: DeltaText, Text: "same"})
	if _, ok := buffer.Flush(now, true); !ok {
		t.Fatal("expected first flush")
	}
	if _, ok := buffer.Flush(now.Add(time.Second), true); ok {
		t.Fatal("unchanged state should not flush")
	}
}

func TestStreamBufferKeepsRepeatedToolNamesAndDetails(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-tools", now, 0, 1)
	if !buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Bash", ToolStatus: "running", ToolCommand: "go test ./internal/channel"}) {
		t.Fatal("first tool delta should change the buffer")
	}
	if !buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-2", ToolName: "Bash", ToolStatus: "running", ToolCommand: "docker compose ps"}) {
		t.Fatal("second same-name tool should remain distinct")
	}
	if !buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Bash", ToolStatus: "complete", ToolOutput: "ok"}) {
		t.Fatal("tool result should update the existing record")
	}
	state, ok := buffer.Flush(now, true)
	if !ok || len(state.Tools) != 2 {
		t.Fatalf("terminal tools = %+v, ok=%v", state.Tools, ok)
	}
	if state.Tools[0].Command != "go test ./internal/channel" || state.Tools[0].OutputPreview != "ok" {
		t.Fatalf("first tool details = %+v", state.Tools[0])
	}
	if state.Tools[1].Command != "docker compose ps" || state.Tools[1].Status != "running" {
		t.Fatalf("second tool details = %+v", state.Tools[1])
	}
}

func TestStreamBufferTimelinePreservesOrderAndUpdatesToolInPlace(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-timeline", now, 0, 1)
	buffer.Apply(Delta{Kind: DeltaText, Text: "before"})
	buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Read", ToolStatus: ToolStatusRunning, ToolCommand: "README.md"})
	buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Read", ToolStatus: ToolStatusComplete, ToolOutput: "done"})
	buffer.Apply(Delta{Kind: DeltaText, Text: "after"})

	state, ok := buffer.Flush(now, true)
	if !ok {
		t.Fatal("timeline state did not flush")
	}
	if len(state.Timeline) != 3 {
		t.Fatalf("timeline = %+v, want three ordered entries", state.Timeline)
	}
	if state.Timeline[0].Kind != TimelineText || state.Timeline[0].Text != "before" {
		t.Fatalf("first timeline entry = %+v", state.Timeline[0])
	}
	if state.Timeline[1].Kind != TimelineTool || state.Timeline[1].Tool.ID != "tool-1" || state.Timeline[1].Tool.Status != ToolStatusComplete || state.Timeline[1].Tool.OutputPreview != "done" {
		t.Fatalf("tool timeline entry = %+v", state.Timeline[1])
	}
	if state.Timeline[2].Kind != TimelineText || state.Timeline[2].Text != "after" {
		t.Fatalf("last timeline entry = %+v", state.Timeline[2])
	}
	if state.Text != "beforeafter" || len(state.Tools) != 1 {
		t.Fatalf("legacy state changed: %+v", state)
	}
}

func TestStreamBufferTimelineAmendsAndRetractsLatestText(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-amend", now, 0, 1)
	buffer.Apply(Delta{Kind: DeltaText, Text: "accepted"})
	buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Read", ToolStatus: ToolStatusComplete})
	buffer.Apply(Delta{Kind: DeltaText, Text: "draft"})
	if !buffer.Apply(Delta{Kind: DeltaTextAmend, PreviousText: "draft", Text: "final"}) {
		t.Fatal("text replacement did not change timeline")
	}
	state, ok := buffer.Flush(now, false)
	if !ok || state.Text != "acceptedfinal" || len(state.Timeline) != 3 || state.Timeline[2].Text != "final" {
		t.Fatalf("amended state = %+v, ok=%v", state, ok)
	}
	if !buffer.Apply(Delta{Kind: DeltaTextAmend, PreviousText: "final", Text: ""}) {
		t.Fatal("text retraction did not change timeline")
	}
	state, ok = buffer.Flush(now.Add(time.Second), true)
	if !ok || state.Text != "accepted" || len(state.Timeline) != 2 || state.Timeline[1].Kind != TimelineTool {
		t.Fatalf("retracted state = %+v, ok=%v", state, ok)
	}
}

func TestStreamBufferFlushesToolChangesWithoutWaitingForTextThrottle(t *testing.T) {
	now := time.Date(2026, 9, 3, 18, 0, 0, 0, time.UTC)
	buffer := NewStreamBuffer("run-urgent-tool", now, time.Hour, 1000)
	buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-1", ToolName: "Read", ToolStatus: ToolStatusRunning})
	if _, ok := buffer.Flush(now, false); !ok {
		t.Fatal("first tool did not flush")
	}
	buffer.Apply(Delta{Kind: DeltaTool, ToolID: "tool-2", ToolName: "Read", ToolStatus: ToolStatusRunning})
	state, ok := buffer.Flush(now.Add(time.Millisecond), false)
	if !ok || len(state.Tools) != 2 {
		t.Fatalf("urgent tool flush = %+v, ok=%v", state, ok)
	}
}
