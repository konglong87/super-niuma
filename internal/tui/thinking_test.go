package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestThinkingModeSummaryRendersMetadataWithoutBody(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary}})
	model.currentTurn = 2
	model.appendDisplayTextSegment("thinking", "private reasoning line one\nprivate reasoning line two")
	model.markDisplayTurnDone()

	seg := model.displayTimeline.segments[len(model.displayTimeline.segments)-1]
	rendered := stripANSI(model.renderDisplaySegment(seg, true, false))
	for _, want := range []string{"Thought", "turn 2", "phase 1", "2 lines"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("summary missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "private reasoning") {
		t.Fatalf("summary leaked thinking body:\n%s", rendered)
	}
	if seg.content != "private reasoning line one\nprivate reasoning line two" {
		t.Fatalf("summary mode mutated segment content: %q", seg.content)
	}
}

func TestThinkingModeFullKeepsExistingThinkingRail(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeFull}})
	model.currentTurn = 1
	model.appendDisplayTextSegment("thinking", "inspect the repository")
	model.markDisplayTurnDone()

	seg := model.displayTimeline.segments[len(model.displayTimeline.segments)-1]
	rendered := stripANSI(model.renderDisplaySegment(seg, true, false))
	if !strings.Contains(rendered, "Thought") || !strings.Contains(rendered, "inspect the repository") {
		t.Fatalf("full thinking rail missing content:\n%s", rendered)
	}
}

func TestThinkingModeHiddenRetainsSegmentWithoutRendering(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeHidden}})
	model.currentTurn = 1
	model.appendDisplayTextSegment("thinking", "retained but hidden")
	model.markDisplayTurnDone()

	seg := model.displayTimeline.segments[len(model.displayTimeline.segments)-1]
	if rendered := model.renderDisplaySegment(seg, true, false); rendered != "" {
		t.Fatalf("hidden mode rendered thinking: %q", stripANSI(rendered))
	}
	if seg.content != "retained but hidden" {
		t.Fatalf("hidden mode discarded content: %q", seg.content)
	}
}

func TestThinkingSegmentsCarryStableTurnAndPhase(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary}})
	model.now = func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) }
	model.currentTurn = 3
	model.appendDisplayTextSegment("thinking", "phase one")
	model.closeDisplayTextAppendWindow()
	model.appendDisplayTextSegment("assistant", "intermediate")
	model.closeDisplayTextAppendWindow()
	model.appendDisplayTextSegment("thinking", "phase two")

	var got []displaySegment
	for _, seg := range model.displayTimeline.segments {
		if seg.kind == displaySegmentThinking {
			got = append(got, seg)
		}
	}
	if len(got) != 2 || got[0].turn != 3 || got[0].phase != 1 || got[1].turn != 3 || got[1].phase != 2 {
		t.Fatalf("thinking anchors = %+v", got)
	}
}

func TestThinkingCommandChangesModeWithoutRunningModel(t *testing.T) {
	runs := 0
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{ThinkingMode: thinkingModeFull},
		Run: func(context.Context, string) (string, error) {
			runs++
			return "unexpected", nil
		},
	})
	beforeMouse := model.mouseTracking
	model.textarea.SetValue("/thinking summary")
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(Model)
	if cmd != nil || runs != 0 {
		t.Fatalf("local command called model: cmd=%v runs=%d", cmd, runs)
	}
	if model.thinkingMode != thinkingModeSummary || !model.thinkingModeExplicit {
		t.Fatalf("thinking mode = %q explicit=%v", model.thinkingMode, model.thinkingModeExplicit)
	}
	if model.mouseTracking != beforeMouse || model.textarea.Value() != "" {
		t.Fatalf("local command changed mouse/input: mouse=%v input=%q", model.mouseTracking, model.textarea.Value())
	}
}

func TestThinkingModeCommandsAllStayLocal(t *testing.T) {
	runs := 0
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{ThinkingMode: thinkingModeFull},
		Run: func(context.Context, string) (string, error) {
			runs++
			return "unexpected", nil
		},
	})
	for _, test := range []struct {
		command string
		want    thinkingMode
	}{
		{command: "/thinking full", want: thinkingModeFull},
		{command: "/thinking summary", want: thinkingModeSummary},
		{command: "/thinking hide", want: thinkingModeHidden},
	} {
		model = submitThinkingCommand(t, model, test.command)
		if model.thinkingMode != test.want {
			t.Fatalf("%s mode = %q, want %q", test.command, model.thinkingMode, test.want)
		}
	}
	if runs != 0 {
		t.Fatalf("thinking mode commands called model %d times", runs)
	}
}

func TestThinkingModeConfigReloadRespectsSessionOverride(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeFull}})
	model.applyConfigReload(StreamEvent{Welcome: &WelcomeInfo{ThinkingMode: thinkingModeHidden}})
	if model.thinkingMode != thinkingModeHidden {
		t.Fatalf("config reload mode = %q, want hidden", model.thinkingMode)
	}
	model = submitThinkingCommand(t, model, "/thinking summary")
	model.applyConfigReload(StreamEvent{Welcome: &WelcomeInfo{ThinkingMode: thinkingModeFull}})
	if model.thinkingMode != thinkingModeSummary {
		t.Fatalf("session override was replaced by config reload: %q", model.thinkingMode)
	}
	model.thinkingDetailTurn = 7
	model.applySessionResume(StreamEvent{Welcome: &WelcomeInfo{ThinkingMode: thinkingModeFull}})
	if model.thinkingMode != thinkingModeSummary || model.thinkingDetailActive() {
		t.Fatalf("resume state = mode:%q detail:%v", model.thinkingMode, model.thinkingDetailActive())
	}
}

func TestThinkingCommandOpensAndClosesTurnDetail(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary}})
	model.currentTurn = 2
	model.appendDisplayTextSegment("thinking", "TURN_TWO_PRIVATE_REASONING")
	model.markDisplayTurnDone()

	model = submitThinkingCommand(t, model, "/thinking show 2")
	if !model.thinkingDetailActive() || model.thinkingDetailTurn != 2 {
		t.Fatalf("thinking detail state = active:%v turn:%d", model.thinkingDetailActive(), model.thinkingDetailTurn)
	}
	if detail := stripANSI(model.liveTranscriptView()); !strings.Contains(detail, "TURN_TWO_PRIVATE_REASONING") || !strings.Contains(detail, "Thinking detail · turn 2") {
		t.Fatalf("thinking detail missing body/header:\n%s", detail)
	}

	model = submitThinkingCommand(t, model, "/thinking summary 2")
	if model.thinkingDetailActive() || model.thinkingMode != thinkingModeSummary {
		t.Fatalf("thinking detail did not collapse: active=%v mode=%q", model.thinkingDetailActive(), model.thinkingMode)
	}
}

func TestThinkingDetailEscapeClosesBeforeQuit(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary}})
	model.currentTurn = 1
	model.appendDisplayTextSegment("thinking", "detail")
	model.markDisplayTurnDone()
	model = submitThinkingCommand(t, model, "/thinking show latest")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = updated.(Model)
	if cmd != nil || model.thinkingDetailActive() {
		t.Fatalf("escape should close detail without quitting: cmd=%v active=%v", cmd, model.thinkingDetailActive())
	}
}

func TestThinkingCommandRejectsUnknownModeAndTurn(t *testing.T) {
	model := NewModel(context.Background(), Options{Welcome: WelcomeInfo{ThinkingMode: thinkingModeFull}})
	model = submitThinkingCommand(t, model, "/thinking sideways")
	if model.err == nil || !strings.Contains(model.err.Error(), "full, summary, hidden, or show") {
		t.Fatalf("unknown mode error = %v", model.err)
	}
	model.err = nil
	model = submitThinkingCommand(t, model, "/thinking show 99")
	if model.err == nil || !strings.Contains(model.err.Error(), "turn 99") {
		t.Fatalf("unknown turn error = %v", model.err)
	}
}

func TestThinkingResumeHistoryRendersSummaryAndOpensFullDetail(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary},
		InitialMessages: []InitialMessage{
			{Role: "user", Content: "resumed prompt", Turn: 8},
			{Role: "thinking", Content: "RESUMED_PRIVATE_REASONING", Turn: 8, Phase: 1, CreatedAt: time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)},
			{Role: "assistant", Content: "resumed answer", Turn: 8},
		},
	})
	if model.currentTurn != 8 {
		t.Fatalf("current turn = %d, want 8", model.currentTurn)
	}
	transcript := stripANSI(strings.Join(model.pendingTranscriptBlocks(), "\n"))
	if !strings.Contains(transcript, "Thought · turn 8 · phase 1") || strings.Contains(transcript, "RESUMED_PRIVATE_REASONING") {
		t.Fatalf("resumed summary is wrong:\n%s", transcript)
	}
	model = submitThinkingCommand(t, model, "/thinking show 8")
	if detail := stripANSI(model.liveTranscriptView()); !strings.Contains(detail, "RESUMED_PRIVATE_REASONING") {
		t.Fatalf("resumed detail missing full body:\n%s", detail)
	}
}

func TestThinkingInitialArchiveOpensOlderTurnWithoutRenderingIt(t *testing.T) {
	model := NewModel(context.Background(), Options{
		Welcome: WelcomeInfo{ThinkingMode: thinkingModeSummary},
		InitialThinking: []ThinkingDetail{
			{Turn: 1, Phase: 1, Content: "OLD_PRIVATE_REASONING", CreatedAt: time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)},
		},
		InitialMessages: []InitialMessage{
			{Role: "user", Content: "recent prompt", Turn: 8},
			{Role: "assistant", Content: "recent answer", Turn: 8},
		},
	})
	if transcript := stripANSI(strings.Join(model.pendingTranscriptBlocks(), "\n")); strings.Contains(transcript, "OLD_PRIVATE_REASONING") || strings.Contains(transcript, "turn 1") {
		t.Fatalf("older Thinking archive rendered without explicit show:\n%s", transcript)
	}
	model = submitThinkingCommand(t, model, "/thinking show 1")
	if detail := stripANSI(model.liveTranscriptView()); !strings.Contains(detail, "OLD_PRIVATE_REASONING") {
		t.Fatalf("older Thinking detail unavailable:\n%s", detail)
	}
}

func submitThinkingCommand(t *testing.T, model Model, command string) Model {
	t.Helper()
	model.textarea.SetValue(command)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("thinking command %q returned async command", command)
	}
	return updated.(Model)
}
