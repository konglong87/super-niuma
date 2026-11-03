package query

import (
	"context"
	"testing"

	"github.com/konglong87/super-niuma/internal/compact"
)

type recordingSink struct{ calls []string }

func (s *recordingSink) OnThinking(_ context.Context, text string) error {
	s.calls = append(s.calls, "thinking:"+text)
	return nil
}

func (s *recordingSink) OnToolCall(_ context.Context, e ToolCallEvent) error {
	s.calls = append(s.calls, "call:"+e.Name)
	return nil
}

func (s *recordingSink) OnToolResult(_ context.Context, t ToolTrace) error {
	s.calls = append(s.calls, "result:"+t.Name)
	return nil
}

func (s *recordingSink) OnUsage(_ context.Context, turn int, _ Usage) error {
	s.calls = append(s.calls, "usage")
	return nil
}

func (s *recordingSink) OnMessageStop(_ context.Context, _ int, reason string, _ Usage) error {
	s.calls = append(s.calls, "stop:"+reason)
	return nil
}

func (s *recordingSink) OnCompact(_ context.Context, _ compact.Result) error {
	s.calls = append(s.calls, "compact")
	return nil
}

func TestSinkCallbacksForwardsAll(t *testing.T) {
	sink := &recordingSink{}
	cb := SinkCallbacks(context.Background(), sink)
	_ = cb.OnThinking("hi")
	_ = cb.OnToolCall(ToolCallEvent{Name: "Bash"})
	_ = cb.OnToolResult(ToolTrace{Name: "Bash"})
	_ = cb.OnUsage(1, Usage{})
	_ = cb.OnMessageStop(1, "end_turn", Usage{})
	_ = cb.OnCompact(compact.Result{})
	want := []string{"thinking:hi", "call:Bash", "result:Bash", "usage", "stop:end_turn", "compact"}
	if len(sink.calls) != len(want) {
		t.Fatalf("got %v", sink.calls)
	}
	for i := range want {
		if sink.calls[i] != want[i] {
			t.Fatalf("call %d: got %q want %q", i, sink.calls[i], want[i])
		}
	}
}
