package tui

import (
	"context"
	"strings"
	"testing"
)

func TestThinkingPhaseCommitsBeforeAssistantText(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true
	model.width = 100
	model.height = 32

	events := make(chan StreamEvent, 4)
	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamThinking, Text: "THINKING_START\nTHINKING_TAIL"}, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "ANSWER_START\nANSWER_TAIL"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("phase boundary must schedule transcript flush and stream wait commands")
	}

	if model.transcriptPhaseCommittingSeq == 0 {
		t.Fatal("thinking phase should be marked as committing before assistant text is rendered")
	}
	if got := stripANSI(model.timelineLiveView()); strings.Contains(got, "THINKING_START") || strings.Contains(got, "THINKING_TAIL") {
		t.Fatalf("committed thinking remained in live view:\n%s", got)
	}
	if got := stripANSI(model.timelineLiveView()); !strings.Contains(got, "ANSWER_START") || !strings.Contains(got, "ANSWER_TAIL") {
		t.Fatalf("assistant phase missing from live view:\n%s", got)
	}

	if model.transcriptPrintedSeq != 0 {
		t.Fatalf("phase sequence should not be acknowledged before tea.Println: %d", model.transcriptPrintedSeq)
	}
	var thinkingTranscript string
	for _, seg := range model.displayTimeline.segments {
		if seg.kind == displaySegmentThinking {
			thinkingTranscript = stripANSI(model.renderDisplaySegment(seg, true, false))
		}
	}
	if strings.Contains(thinkingTranscript, "ANSWER_START") || strings.Contains(thinkingTranscript, "ANSWER_TAIL") || !strings.Contains(thinkingTranscript, "THINKING_START") {
		t.Fatalf("thinking phase transcript is incorrect:\n%s", thinkingTranscript)
	}
}

func TestAssistantPhaseCommitsBeforeToolStart(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }})
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true
	model.width = 100
	model.height = 32
	events := make(chan StreamEvent, 4)

	updated, _ := model.Update(streamEventMsg{event: StreamEvent{Type: StreamText, Text: "ANSWER_BEFORE_TOOL"}, ch: events})
	model = updated.(Model)
	updated, cmd := model.Update(streamEventMsg{event: StreamEvent{Type: StreamToolStart, ToolName: "Read", ToolID: "tool-1"}, ch: events})
	model = updated.(Model)
	if cmd == nil {
		t.Fatal("tool boundary must schedule transcript flush and stream wait commands")
	}

	if model.transcriptPhaseCommittingSeq == 0 {
		t.Fatal("assistant phase should be marked as committing before tool rendering")
	}
	live := stripANSI(model.timelineLiveView())
	if strings.Contains(live, "ANSWER_BEFORE_TOOL") {
		t.Fatalf("completed assistant phase remained live:\n%s", live)
	}
	if !strings.Contains(live, "查看文件") {
		t.Fatalf("tool phase missing from live view:\n%s", live)
	}
	var assistantTranscript string
	for _, seg := range model.displayTimeline.segments {
		if seg.kind == displaySegmentAssistantText {
			assistantTranscript = stripANSI(model.renderDisplaySegment(seg, true, false))
		}
	}
	if !strings.Contains(assistantTranscript, "ANSWER_BEFORE_TOOL") {
		t.Fatalf("assistant phase missing from transcript segment:\n%s", assistantTranscript)
	}
}

func TestPhaseCommitAcknowledgementAdvancesPrintedSequence(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.transcriptPrintedHeader = true
	model.transcriptPhaseCommittingSeq = 4
	model.transcriptCommittingSeq = 4
	model.displayTimeline.nextSeq = 4

	model.markTranscriptFlushPrinted(transcriptFlushPrintedMsg{printedHeader: true, printedCount: 2, printedSeq: 4})

	if model.transcriptPrintedSeq != 4 || model.transcriptPhaseCommittingSeq != 0 || model.transcriptCommittingSeq != 0 {
		t.Fatalf("acknowledgement state = printed=%d phase=%d commit=%d", model.transcriptPrintedSeq, model.transcriptPhaseCommittingSeq, model.transcriptCommittingSeq)
	}
}

func TestHiddenThinkingDoesNotCommitOrSplitAssistantPhase(t *testing.T) {
	showThinking := false
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ShowThinking: &showThinking}})
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true

	model.applyStreamEvent(StreamEvent{Type: StreamText, Text: "before hidden reasoning"})
	effects := model.applyStreamEvent(StreamEvent{Type: StreamThinking, Text: "private reasoning"})
	if effects.phaseBoundary {
		t.Fatal("hidden thinking must not schedule a phase boundary commit")
	}

	model.applyStreamEvent(StreamEvent{Type: StreamText, Text: " after hidden reasoning"})
	if len(model.displayTimeline.segments) != 1 {
		t.Fatalf("hidden thinking split the assistant timeline: %#v", model.displayTimeline.segments)
	}
	if got := model.displayTimeline.segments[0].content; got != "before hidden reasoning after hidden reasoning" {
		t.Fatalf("assistant content = %q, want contiguous hidden-thinking stream", got)
	}
}

func TestBackgroundFlushDoesNotSkipStreamingAssistantDuringPhaseCommit(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.busy = true
	model.streamingActive = true
	model.transcriptPrintedHeader = true

	model.applyStreamEvent(StreamEvent{Type: StreamThinking, Text: "completed thinking"})
	model.applyStreamEvent(StreamEvent{Type: StreamText, Text: "streaming assistant"})
	thinkingSeq := model.displayTimeline.segments[0].seq
	model.transcriptPhaseCommittingSeq = thinkingSeq
	model.phaseBoundaryStreamCh = make(chan StreamEvent)

	updated, _ := model.Update(backgroundPollMsg{updates: []BackgroundUpdate{{
		ID:      "background-1",
		Prompt:  "background job",
		LogTail: "background output",
		Status:  "completed",
	}}})
	model = updated.(Model)

	if model.transcriptPrintedSeq != 0 {
		t.Fatalf("background flush advanced printed sequence across streaming assistant: %d", model.transcriptPrintedSeq)
	}
	if got := model.timelineLiveView(); !strings.Contains(got, "streaming assistant") {
		t.Fatalf("streaming assistant disappeared from live view:\n%s", got)
	}
}
