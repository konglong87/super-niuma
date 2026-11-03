package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

func TestTaskToolGoldenBatchPriorityRetry(t *testing.T) {
	streamer := &goldenBatchStreamer{failOncePrompt: "high"}
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "low priority", "prompt": "low", "priority": 1},
			{"description": "high priority", "prompt": "high", "priority": 10},
			{"description": "mid priority", "prompt": "mid", "priority": 5},
		},
		"max_concurrency":   1,
		"schedule_strategy": "priority",
		"retry_attempts":    1,
	})
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(streamer.prompts(), ","); got != "high,high,mid,low" {
		t.Fatalf("execution order = %s", got)
	}
	assertTaskGolden(t, "batch_priority_retry.json", normalizeBatchGolden(t, res.Content))
}

func TestTaskToolGoldenBatchTimeout(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "slow task", "prompt": "slow", "timeout_ms": 5},
		},
		"max_concurrency": 1,
	})
	res := New(blockingStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("expected timeout result = %+v", res)
	}
	assertTaskGolden(t, "batch_timeout.json", normalizeBatchGolden(t, res.Content))
}

func TestTaskToolGoldenBatchInvalidShortTimeout(t *testing.T) {
	input, _ := json.Marshal(map[string]any{
		"tasks": []map[string]any{
			{"description": "count breathe files", "prompt": "Count Go files in service/breathe."},
			{"description": "count mobile files", "prompt": "Count Go files in service/breathe_mobile."},
		},
		"max_concurrency": 2,
		"timeout_ms":      15000,
	})
	res := New(&goldenBatchStreamer{}, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("expected invalid timeout result = %+v", res)
	}
	assertTaskGolden(t, "batch_invalid_short_timeout.json", normalizeBatchGolden(t, res.Content))
}

type goldenBatchStreamer struct {
	mu             sync.Mutex
	order          []string
	failOncePrompt string
	failed         bool
}

func (s *goldenBatchStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	prompt := req.Messages[0].Content[0].Text
	s.mu.Lock()
	s.order = append(s.order, prompt)
	shouldFail := prompt == s.failOncePrompt && !s.failed
	if shouldFail {
		s.failed = true
	}
	s.mu.Unlock()
	if shouldFail {
		return nil, errors.New("temporary failure")
	}
	text := prompt + " done"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

func (s *goldenBatchStreamer) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.order...)
}

func normalizeBatchGolden(t *testing.T, raw string) string {
	t.Helper()
	var decoded batchResponse
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	decoded.Summary.StartedAt = "<timestamp>"
	decoded.Summary.FinishedAt = "<timestamp>"
	decoded.Summary.DurationMS = 0
	for i := range decoded.Tasks {
		if decoded.Tasks[i].StartedAt != "" {
			decoded.Tasks[i].StartedAt = "<timestamp>"
		}
		if decoded.Tasks[i].FinishedAt != "" {
			decoded.Tasks[i].FinishedAt = "<timestamp>"
		}
		decoded.Tasks[i].DurationMS = 0
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(decoded); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func assertTaskGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\n--- got ---\n%s", name, err, got)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
