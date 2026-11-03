package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/tools"
)

// subagentLoopStreamer always asks for the identical tool call, reproducing the
// degenerate repetition a weak model falls into. A sub-agent used to have no
// early stop for this at all: only MaxTurns, and the token budget cannot help
// because a text-cheap spin barely spends anything.
type subagentLoopStreamer struct {
	mu       sync.Mutex
	requests []anthropic.MessagesRequest
}

func (s *subagentLoopStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, _ anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	n := len(s.requests)
	s.mu.Unlock()
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    fmt.Sprintf("toolu_%d", n), // fresh id per turn, like a real provider
			Name:  "Echo",
			Input: json.RawMessage(`{"text":"same"}`),
		}}},
		StopReason: "tool_use",
	}, nil
}

func (s *subagentLoopStreamer) snapshot() []anthropic.MessagesRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]anthropic.MessagesRequest(nil), s.requests...)
}

func subagentMessageText(messages []anthropic.MessageParam) string {
	var b strings.Builder
	for _, message := range messages {
		for _, block := range message.Content {
			b.WriteString(block.Text)
			b.WriteString("\n")
			b.WriteString(block.Content)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestSubagentLoopGuardAbortsIdenticalToolCallLoop(t *testing.T) {
	project := t.TempDir()
	streamer := &subagentLoopStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(echoTool{}), Model: "test", MaxTurns: 30}

	result, err := runtime.Run(context.Background(), Request{Prompt: "loop please", CWD: project}, tools.Context{CWD: project})
	if err == nil {
		t.Fatalf("expected a loop guard abort, got nil (result=%+v)", result)
	}
	if !strings.Contains(err.Error(), "loop guard") {
		t.Fatalf("error %q does not look like a loop guard abort; the sub-agent ran to its max-turns bound", err.Error())
	}
	requests := streamer.snapshot()
	// Same escalation contract as the main loop: hard abort on the 6th
	// no-progress turn instead of burning all 30 turns.
	if len(requests) != 6 {
		t.Fatalf("model calls = %d, want 6 (hard limit), MaxTurns=30", len(requests))
	}
	// The streak reaches 2 on turn 2, so turn 3's request must already warn the
	// sub-agent — an abort with no prior signal gives it no chance to recover.
	if !strings.Contains(subagentMessageText(requests[2].Messages), "Loop check") {
		t.Fatalf("loop-awareness section missing from turn 3 request:\n%s", subagentMessageText(requests[2].Messages))
	}
}

// subagentPollTool returns a different result on every call with the same input,
// emulating a legitimate poll on external state that is making progress.
type subagentPollTool struct {
	mu    sync.Mutex
	calls int
}

func (*subagentPollTool) Name() string        { return "Poll" }
func (*subagentPollTool) Description() string { return "poll changing status" }
func (*subagentPollTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)
}
func (t *subagentPollTool) Run(context.Context, json.RawMessage, tools.Context) tools.Result {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls++
	return tools.Result{Content: fmt.Sprintf("status: attempt %d, still running", t.calls)}
}

// subagentPollStreamer issues the identical Poll call for several turns, then
// answers. The result changes every turn, so this is progress, not a loop.
type subagentPollStreamer struct {
	mu       sync.Mutex
	requests int
}

func (s *subagentPollStreamer) StreamMessages(_ context.Context, _ anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.mu.Lock()
	s.requests++
	n := s.requests
	s.mu.Unlock()
	if n <= 8 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    fmt.Sprintf("toolu_%d", n),
				Name:  "Poll",
				Input: json.RawMessage(`{"id":"job-1"}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	_ = cb.OnText("done polling")
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "done polling"}}},
		StopReason: "end_turn",
	}, nil
}

func (s *subagentPollStreamer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func TestSubagentLoopGuardIgnoresPollingWithChangingResults(t *testing.T) {
	project := t.TempDir()
	streamer := &subagentPollStreamer{}
	runtime := Runtime{Client: streamer, Registry: tools.NewRegistry(&subagentPollTool{}), Model: "test", MaxTurns: 30}

	result, err := runtime.Run(context.Background(), Request{Prompt: "poll job-1 until done", CWD: project}, tools.Context{CWD: project})
	if err != nil {
		t.Fatalf("polling with changing results must not be aborted: %v", err)
	}
	if got := streamer.count(); got != 9 {
		t.Fatalf("model calls = %d, want 9 (8 polls + final); the guard falsely aborted a progressing poll", got)
	}
	if !strings.Contains(result.Content, "done polling") {
		t.Fatalf("final answer missing: %q", result.Content)
	}
}
