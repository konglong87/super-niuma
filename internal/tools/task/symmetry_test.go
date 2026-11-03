package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// failThenSucceedStreamer 前 failures 次调用返错，之后正常回话。
type failThenSucceedStreamer struct {
	failures int32
	calls    atomic.Int32
}

func (s *failThenSucceedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if s.calls.Add(1) <= s.failures {
		return nil, errors.New("transient provider failure")
	}
	text := req.Messages[0].Content[0].Text + " done"
	_ = cb.OnText(text)
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

// AUDIT-P1-20 的剩余项之一：batch 有重试，单 Task 一次不重试，而同一个
// retry_attempts 参数在两种形式下含义不同且描述里没写。
func TestSingleTaskRetriesLikeBatch(t *testing.T) {
	streamer := &failThenSucceedStreamer{failures: 2}
	input, err := json.Marshal(map[string]any{
		"description":      "one",
		"prompt":           "one",
		"retry_attempts":   3,
		"retry_backoff_ms": 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if res.IsError {
		t.Fatalf("single task failed despite retries being allowed: %s", res.Content)
	}
	if got := streamer.calls.Load(); got != 3 {
		t.Fatalf("streamer called %d times, want 3 (2 failures + 1 success)", got)
	}
}

// 反方向守卫：不配 retry_attempts 就不该偷偷重试。
func TestSingleTaskWithoutRetryAttemptsIsTriedOnce(t *testing.T) {
	streamer := &failThenSucceedStreamer{failures: 5}
	input, err := json.Marshal(map[string]any{"description": "one", "prompt": "one"})
	if err != nil {
		t.Fatal(err)
	}
	// 失败的单 Task 会以 partial evidence 的形式返回（IsError=false），所以这里
	// 断言的是尝试次数，不是 IsError。
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if !strings.Contains(res.Content, "transient provider failure") {
		t.Fatalf("failure was not surfaced: %q", res.Content)
	}
	if got := streamer.calls.Load(); got != 1 {
		t.Fatalf("streamer called %d times, want 1", got)
	}
}

// 单 Task 也必须和 batch 一样：取消之后不再重试。
func TestSingleTaskDoesNotRetryAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamer := &cancelCountingStreamer{cancel: cancel}
	input, err := json.Marshal(map[string]any{
		"description":      "one",
		"prompt":           "one",
		"retry_attempts":   3,
		"retry_backoff_ms": 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	New(streamer, "model").Run(ctx, input, tools.Context{})
	if got := streamer.callCount(); got != 1 {
		t.Fatalf("streamer called %d times after cancellation, want 1", got)
	}
}

// AUDIT-P1-19 的剩余项：schema 无顶层 required[]，所以 `{}` 和只给一半的输入都会被
// 接受，然后拿一个空 prompt 去起子代理。根级 anyOf 在各 provider 的校验里兼容性不一，
// 所以在代码里守，并给模型一句能照着改的错误。
func TestTaskRejectsInputThatIsNeitherFormCompletely(t *testing.T) {
	cases := map[string]map[string]any{
		"empty":      {},
		"no prompt":  {"description": "one"},
		"no descr":   {"prompt": "one"},
		"blank both": {"description": "  ", "prompt": "\t"},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			streamer := &failThenSucceedStreamer{}
			input, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
			if !res.IsError {
				t.Fatalf("accepted %v and returned %q", payload, res.Content)
			}
			if !strings.Contains(res.Content, "description and prompt") || !strings.Contains(res.Content, "tasks array") {
				t.Fatalf("error does not tell the model both valid forms: %q", res.Content)
			}
			if got := streamer.calls.Load(); got != 0 {
				t.Fatalf("a sub-agent was started anyway (%d calls)", got)
			}
		})
	}
}

// 两种形式同时给也是错的：以前顶层 description/prompt 被静默忽略。
func TestTaskRejectsBothFormsAtOnce(t *testing.T) {
	streamer := &failThenSucceedStreamer{}
	input, err := json.Marshal(map[string]any{
		"description": "single",
		"prompt":      "single",
		"tasks":       []map[string]string{{"description": "batch", "prompt": "batch"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := New(streamer, "model").Run(context.Background(), input, tools.Context{})
	if !res.IsError {
		t.Fatalf("both forms at once were accepted: %q", res.Content)
	}
	if !strings.Contains(res.Content, "not both") {
		t.Fatalf("error does not name the conflict: %q", res.Content)
	}
	if got := streamer.calls.Load(); got != 0 {
		t.Fatalf("a sub-agent was started anyway (%d calls)", got)
	}
}

// batch 共享一棵工作树这件事必须写在模型能看到的地方，否则它会照着 "isolated"
// 这个词把 8 个写同一批文件的任务扇出去（AUDIT-P1-20）。
func TestTaskDescribesThatBatchTasksShareOneWorkingTree(t *testing.T) {
	tool := New(&failThenSucceedStreamer{}, "model")
	description := tool.Description()
	schema := string(tool.InputSchema())
	if strings.Contains(description, "isolated sub-agent tasks") {
		t.Fatal("description still calls batch tasks isolated; they share the parent working tree")
	}
	for _, want := range []string{"same batch", "no file locking"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description does not warn about the shared tree (missing %q)", want)
		}
	}
	if !strings.Contains(schema, "SAME working tree") {
		t.Fatalf("tasks schema does not warn about the shared tree: %s", schema)
	}
}
