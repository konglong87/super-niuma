package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestConfigNoticeRendersAsOneWeakLine(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyStreamEvent(StreamEvent{
		Type:       StreamConfigReload,
		Text:       "permissions ask → allow",
		NoticeKind: StreamNoticeConfigReload,
	})

	got := strings.TrimSpace(stripANSI(model.timelineLiveView()))
	if got != "↻ Config · permissions ask → allow" {
		t.Fatalf("config notice = %q", got)
	}
	if strings.Contains(got, "Status") || strings.Contains(got, "Config reloaded") {
		t.Fatalf("config notice retained redundant labels: %q", got)
	}
}

func TestConfigNoticeCoalescesConsecutiveUnprintedChanges(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	for _, detail := range []string{"model gpt-5.5 → glm-5.1", "permissions ask → allow"} {
		model.applyStreamEvent(StreamEvent{
			Type:       StreamConfigReload,
			Text:       detail,
			NoticeKind: StreamNoticeConfigReload,
		})
	}

	want := "model gpt-5.5 → glm-5.1 · permissions ask → allow"
	if len(model.messages) != 1 || model.messages[0].role != messageRoleConfigNotice || model.messages[0].content != want {
		t.Fatalf("messages = %#v, want one merged config notice", model.messages)
	}
	if len(model.displayTimeline.segments) != 1 || model.displayTimeline.segments[0].kind != displaySegmentConfigNotice || model.displayTimeline.segments[0].content != want {
		t.Fatalf("timeline = %#v, want one merged config notice", model.displayTimeline.segments)
	}
}

func TestConfigNoticeDoesNotRewriteCommittedNotice(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyStreamEvent(StreamEvent{
		Type:       StreamConfigReload,
		Text:       "model gpt-5.5 → glm-5.1",
		NoticeKind: StreamNoticeConfigReload,
	})
	model.transcriptPrintedCount = len(model.messages)
	model.transcriptPrintedSeq = model.displayTimeline.maxSeq()

	model.applyStreamEvent(StreamEvent{
		Type:       StreamConfigReload,
		Text:       "permissions ask → allow",
		NoticeKind: StreamNoticeConfigReload,
	})

	if len(model.messages) != 2 || len(model.displayTimeline.segments) != 2 {
		t.Fatalf("committed notice was rewritten: messages=%#v timeline=%#v", model.messages, model.displayTimeline.segments)
	}
	if model.messages[0].content != "model gpt-5.5 → glm-5.1" || model.messages[1].content != "permissions ask → allow" {
		t.Fatalf("config notice order changed: %#v", model.messages)
	}
}

func TestConfigNoticeWrapsWithHangingIndent(t *testing.T) {
	const width = 36
	got := stripANSI(renderConfigNoticeForWidth(
		"model gpt-5.5 → claude-sonnet-4-6 · permissions ask → allow",
		width,
	))
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("config notice should wrap at width %d: %q", width, got)
	}
	wantIndent := strings.Repeat(" ", runewidth.StringWidth(configNoticePrefix))
	for i, line := range lines {
		if runewidth.StringWidth(line) > width {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, runewidth.StringWidth(line), width, line)
		}
		if i > 0 && !strings.HasPrefix(line, wantIndent) {
			t.Fatalf("continuation line has no hanging indent: %q", line)
		}
	}
}

func TestOrdinaryStatusKeepsExistingRenderer(t *testing.T) {
	model := NewModel(context.Background(), Options{})
	model.applyStreamEvent(StreamEvent{Type: StreamConfigReload, Text: "Goal status updated."})
	if len(model.displayTimeline.segments) != 1 || model.displayTimeline.segments[0].kind != displaySegmentStatusKind {
		t.Fatalf("ordinary status was classified as config notice: %#v", model.displayTimeline.segments)
	}
	got := strings.TrimRight(stripANSI(model.timelineLiveView()), " ")
	if got != "Status\nGoal status updated." {
		t.Fatalf("ordinary status changed: %q", got)
	}
}
