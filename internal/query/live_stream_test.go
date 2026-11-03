package query

import (
	"context"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// liveProbeStreamer emits a text delta and records whether the caller's sink
// had already received it BEFORE StreamMessages returned — i.e. whether text
// streams live during generation instead of being buffered until completion.
type liveProbeStreamer struct {
	sink    *strings.Builder
	sawLive bool
}

func (s *liveProbeStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if err := cb.OnText("hello"); err != nil {
		return nil, err
	}
	s.sawLive = s.sink.String() == "hello"
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}}},
		StopReason: "end_turn",
	}, nil
}

// TestStreamTextReachesSinkDuringGeneration is the heart of the live-streaming
// change: for frontends that can amend already-shown text (OnTextAmended set,
// e.g. the TUI), deltas must reach the sink while the model is still
// generating (the typewriter), and must NOT be re-delivered after acceptance.
func TestStreamTextReachesSinkDuringGeneration(t *testing.T) {
	var sink strings.Builder
	streamer := &liveProbeStreamer{sink: &sink}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{Model: "test", CWD: t.TempDir()})
	cb := RunCallbacks{OnTextAmended: func(string, string) error { return nil }}
	if _, err := session.RunWithCallbacks(context.Background(), "hi", &sink, cb); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !streamer.sawLive {
		t.Fatal("text delta should reach the sink during generation (live), not after completion")
	}
	if got := sink.String(); got != "hello" {
		t.Fatalf("sink should receive the text exactly once, got %q", got)
	}
}

// TestPlainSinkKeepsBufferedContract locks the counterpart: sinks without an
// amend capability (stdout pipes, -p mode) cannot unprint retracted drafts, so
// they must keep receiving text only at acceptance, never mid-generation.
func TestPlainSinkKeepsBufferedContract(t *testing.T) {
	var sink strings.Builder
	streamer := &liveProbeStreamer{sink: &sink}
	session := New(streamer, tools.NewRegistry(echoTool{}), Options{Model: "test", CWD: t.TempDir()})
	if _, err := session.Run(context.Background(), "hi", &sink); err != nil {
		t.Fatalf("run: %v", err)
	}
	if streamer.sawLive {
		t.Fatal("plain writer sinks must not receive text mid-generation (no retraction possible)")
	}
	if got := sink.String(); got != "hello" {
		t.Fatalf("sink should still receive the accepted text once, got %q", got)
	}
}

// TestReconcileStreamedText covers the post-acceptance reconciliation that
// replaces the old buffered Replay:
//   - accepted == streamed: nothing further is emitted (already live)
//   - no deltas were streamed: accepted text is emitted for compatibility with
//     non-streaming providers
//   - accepted != streamed: the amend callback rewrites the live tail
func TestReconcileStreamedText(t *testing.T) {
	msg := func(text string) *anthropic.MessageParam {
		return &anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}
	}

	var texts []string
	var amends [][2]string
	cb := runCallbacks{
		onText:        func(text string) error { texts = append(texts, text); return nil },
		onTextAmended: func(streamed, final string) error { amends = append(amends, [2]string{streamed, final}); return nil },
	}

	if err := reconcileStreamedText(cb, "same", msg("same")); err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(texts) != 0 || len(amends) != 0 {
		t.Fatalf("matching text must emit nothing, got texts=%v amends=%v", texts, amends)
	}

	if err := reconcileStreamedText(cb, "", msg("late")); err != nil {
		t.Fatalf("no-delta: %v", err)
	}
	if len(texts) != 1 || texts[0] != "late" || len(amends) != 0 {
		t.Fatalf("no-delta providers must emit accepted text once, got texts=%v amends=%v", texts, amends)
	}

	// Accepted text extends the streamed text (e.g. exact-output completion):
	// emit only the missing tail — works for every sink, no amend needed.
	if err := reconcileStreamedText(cb, "par", msg("partial")); err != nil {
		t.Fatalf("prefix extension: %v", err)
	}
	if len(texts) != 2 || texts[1] != "tial" || len(amends) != 0 {
		t.Fatalf("prefix extension must emit only the tail, got texts=%v amends=%v", texts, amends)
	}

	if err := reconcileStreamedText(cb, "draft", msg("final")); err != nil {
		t.Fatalf("diverged: %v", err)
	}
	if len(amends) != 1 || amends[0] != [2]string{"draft", "final"} {
		t.Fatalf("diverged text must amend streamed->final, got %v", amends)
	}

	// Stream error path: accepted == nil, live output already delivered — no-op.
	if err := reconcileStreamedText(cb, "partial", nil); err != nil {
		t.Fatalf("nil accepted: %v", err)
	}
	if len(texts) != 2 || len(amends) != 1 {
		t.Fatalf("nil accepted must emit nothing further, got texts=%v amends=%v", texts, amends)
	}
}
