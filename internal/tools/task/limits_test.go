package task

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// peakConcurrencyStreamer records the highest number of sub-agents that were in
// flight at once, which is the only thing that proves a clamp actually clamped.
type peakConcurrencyStreamer struct {
	active int32
	peak   int32
}

func (s *peakConcurrencyStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	current := atomic.AddInt32(&s.active, 1)
	for {
		peak := atomic.LoadInt32(&s.peak)
		if current <= peak || atomic.CompareAndSwapInt32(&s.peak, peak, current) {
			break
		}
	}
	// Long enough that every worker slot is genuinely occupied together.
	time.Sleep(30 * time.Millisecond)
	atomic.AddInt32(&s.active, -1)
	text := req.Messages[0].Content[0].Text + " done"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

func (s *peakConcurrencyStreamer) peakSeen() int32 { return atomic.LoadInt32(&s.peak) }

func batchInputWithConcurrency(t *testing.T, items int, maxConcurrency int) json.RawMessage {
	t.Helper()
	tasks := make([]map[string]string, 0, items)
	for i := 0; i < items; i++ {
		tasks = append(tasks, map[string]string{
			"description": "item",
			"prompt":      "item",
		})
	}
	input, err := json.Marshal(map[string]any{
		"tasks":           tasks,
		"max_concurrency": maxConcurrency,
	})
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestTaskToolClampsOversizedMaxConcurrency(t *testing.T) {
	t.Setenv(EnvMaxBatchConcurrency, "3")
	streamer := &peakConcurrencyStreamer{}
	// The audit's example: max_concurrency: 500 used to be honoured verbatim.
	res := New(streamer, "model").Run(context.Background(), batchInputWithConcurrency(t, 8, 500), tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.MaxConcurrency != 3 {
		t.Fatalf("summary max_concurrency = %d, want the 3-slot ceiling", decoded.Summary.MaxConcurrency)
	}
	if peak := streamer.peakSeen(); peak > 3 {
		t.Fatalf("%d sub-agents ran at once against a ceiling of 3; the clamp is not enforced", peak)
	}
	// Clamping silently would read as "your 500 workers ran".
	for _, want := range []string{"max_concurrency=500", "ceiling", EnvMaxBatchConcurrency} {
		if !strings.Contains(decoded.Summary.ConcurrencyClampNote, want) {
			t.Fatalf("clamp note %q missing %q", decoded.Summary.ConcurrencyClampNote, want)
		}
	}
	if decoded.Summary.Total != 8 || decoded.Summary.Completed != 8 {
		t.Fatalf("clamping dropped work: %+v", decoded.Summary)
	}
}

func TestTaskToolKeepsRequestedConcurrencyUnderCeiling(t *testing.T) {
	t.Setenv(EnvMaxBatchConcurrency, "8")
	streamer := &peakConcurrencyStreamer{}
	res := New(streamer, "model").Run(context.Background(), batchInputWithConcurrency(t, 4, 4), tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.MaxConcurrency != 4 {
		t.Fatalf("summary max_concurrency = %d, want 4 untouched", decoded.Summary.MaxConcurrency)
	}
	if decoded.Summary.ConcurrencyClampNote != "" {
		t.Fatalf("an in-range request was reported as clamped: %q", decoded.Summary.ConcurrencyClampNote)
	}
}

func TestMaxBatchConcurrencyEnvOverrideRejectsNonsense(t *testing.T) {
	t.Setenv(EnvMaxBatchConcurrency, "32")
	if got := maxBatchConcurrency(); got != 32 {
		t.Fatalf("maxBatchConcurrency() = %d, want 32", got)
	}
	for _, raw := range []string{"0", "-4", "many", ""} {
		t.Setenv(EnvMaxBatchConcurrency, raw)
		if got := maxBatchConcurrency(); got != defaultMaxBatchConcurrency {
			t.Fatalf("maxBatchConcurrency() with %q = %d, want the %d default", raw, got, defaultMaxBatchConcurrency)
		}
	}
}

// cancelCountingStreamer fails with the context error and counts how many times
// it was asked, so a retry after cancellation is visible.
type cancelCountingStreamer struct {
	calls  int32
	cancel context.CancelFunc
}

func (s *cancelCountingStreamer) StreamMessages(ctx context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	atomic.AddInt32(&s.calls, 1)
	if cb.OnText != nil {
		// Partial evidence the parent should still get to see.
		_ = cb.OnText("partial work before cancellation")
	}
	s.cancel()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *cancelCountingStreamer) callCount() int32 { return atomic.LoadInt32(&s.calls) }

func TestTaskToolBatchDoesNotRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamer := &cancelCountingStreamer{cancel: cancel}
	input, err := json.Marshal(map[string]any{
		"tasks":            []map[string]string{{"description": "one", "prompt": "one"}},
		"retry_attempts":   3,
		"retry_backoff_ms": 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := New(streamer, "model").Run(ctx, input, tools.Context{})
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatalf("unmarshal %q: %v", res.Content, err)
	}
	if len(decoded.Tasks) != 1 {
		t.Fatalf("tasks = %+v", decoded.Tasks)
	}
	// One attempt, not four: the parent context is gone, so every retry would
	// fail identically while overwriting the partial evidence (AUDIT-P1-20).
	if got := streamer.callCount(); got != 1 {
		t.Fatalf("streamer called %d times after cancellation, want 1", got)
	}
	if decoded.Tasks[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", decoded.Tasks[0].Attempts)
	}
	if !strings.Contains(decoded.Tasks[0].Content, "partial work before cancellation") {
		t.Fatalf("partial evidence was dropped: %+v", decoded.Tasks[0])
	}
}

// deadlineOnceStreamer fails the first attempt with a deadline, then succeeds.
type deadlineOnceStreamer struct {
	calls int32
}

func (s *deadlineOnceStreamer) StreamMessages(ctx context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if atomic.AddInt32(&s.calls, 1) == 1 {
		return nil, context.DeadlineExceeded
	}
	text := req.Messages[0].Content[0].Text + " ok"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

// TestTaskToolBatchStillRetriesDeadlines is the counterweight to the test above:
// skipping retries on cancellation must not turn into skipping them on timeouts,
// which say nothing about the next attempt.
func TestTaskToolBatchStillRetriesDeadlines(t *testing.T) {
	streamer := &deadlineOnceStreamer{}
	input, err := json.Marshal(map[string]any{
		"tasks":          []map[string]string{{"description": "one", "prompt": "one"}},
		"retry_attempts": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	var decoded batchResponse
	if err := json.Unmarshal([]byte(res.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Tasks) != 1 || decoded.Tasks[0].Attempts != 2 || decoded.Tasks[0].IsError {
		t.Fatalf("tasks = %+v, want a successful second attempt", decoded.Tasks)
	}
}
