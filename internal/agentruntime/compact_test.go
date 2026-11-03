package agentruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/tools"
)

// subagentCompactStreamer answers compaction summary requests with a valid
// summary and everything else with either an error or a final text turn.
type subagentCompactStreamer struct {
	requests  []anthropic.MessagesRequest
	summaries int

	mainError     error
	maxMainErrors int
	mainCalls     int
	mainErrors    int
}

func (s *subagentCompactStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if subagentIsCompactSummaryRequest(req) {
		s.summaries++
		return subagentStreamText(cb, subagentCompactSummaryFixture())
	}
	s.mainCalls++
	if s.mainError != nil && s.mainErrors < s.maxMainErrors {
		s.mainErrors++
		return nil, s.mainError
	}
	return subagentStreamText(cb, "done")
}

func subagentIsCompactSummaryRequest(req anthropic.MessagesRequest) bool {
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "Summarize the conversation history below") {
				return true
			}
		}
	}
	return false
}

func subagentStreamText(cb anthropic.StreamCallbacks, text string) (*anthropic.StreamResult, error) {
	if cb.OnText != nil {
		if err := cb.OnText(text); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
	}, nil
}

func subagentCompactSummaryFixture() string {
	return strings.Join([]string{
		"## Current Goal",
		"Continue the delegated task.",
		"## User Preferences / Constraints",
		"None.",
		"## Decisions Made",
		"Use auto compact.",
		"## Files / Code Changed",
		"internal/agentruntime/runtime.go",
		"## Commands / Test Results",
		"go test ./internal/agentruntime",
		"## Open Tasks",
		"Finish.",
		"## Known Issues / Risks",
		"None.",
		"## Important Raw Facts",
		"internal/agentruntime/runtime.go and go test ./internal/agentruntime matter.",
	}, "\n")
}

// longSubagentHistory is long enough to satisfy the compactor's message-count
// gate and large enough to cross a low token threshold.
func longSubagentHistory() []anthropic.MessageParam {
	return []anthropic.MessageParam{
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Earlier context about internal/agentruntime/runtime.go and go test ./internal/agentruntime."}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: strings.Repeat("prior analysis detail ", 200)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Keep going."}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "Understood."}}},
	}
}

func compactingSubagentRuntime(streamer MessageStreamer, cfg compact.Config) Runtime {
	return Runtime{
		Client:      streamer,
		Registry:    tools.NewRegistry(echoTool{}),
		Model:       "base-model",
		MaxTurns:    3,
		AutoCompact: &cfg,
	}
}

func lowThresholdCompactConfig() compact.Config {
	return compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.5,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"base-model": 2_000},
	}
}

// AUDIT-P0-08: sub-agents had no compactor at all, so a long delegated task
// simply died on a context-overflow API error. Proactive compaction must run in
// the sub-agent loop.
func TestSubagentRunCompactsLongHistory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &subagentCompactStreamer{}
	runtime := compactingSubagentRuntime(streamer, lowThresholdCompactConfig())
	result, err := runtime.Run(context.Background(), Request{
		Prompt:          "Continue.",
		CWD:             t.TempDir(),
		InitialMessages: longSubagentHistory(),
	}, tools.Context{})
	if err != nil {
		t.Fatal(err)
	}
	if streamer.summaries != 1 {
		t.Fatalf("sub-agent compaction summary requests = %d, want 1: sub-agent path is not compacting", streamer.summaries)
	}
	main := streamer.requests[len(streamer.requests)-1]
	if !subagentMessagesContain(main.Messages, "Conversation summary so far:") {
		t.Fatal("sub-agent request was not sent with the compacted history")
	}
	if !strings.Contains(result.Content, "done") {
		t.Fatalf("result content = %q", result.Content)
	}
}

// AUDIT-P0-08: the reactive overflow path must work for sub-agents too.
func TestSubagentRunForcesCompactionAfterContextOverflow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &subagentCompactStreamer{
		mainError:     errors.New(`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`),
		maxMainErrors: 1,
	}
	// Threshold high enough that proactive compaction never fires.
	runtime := compactingSubagentRuntime(streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"base-model": 10_000_000},
	})
	result, err := runtime.Run(context.Background(), Request{
		Prompt:          "Continue.",
		CWD:             t.TempDir(),
		InitialMessages: longSubagentHistory(),
	}, tools.Context{})
	if err != nil {
		t.Fatalf("sub-agent failed instead of recovering from context overflow: %v", err)
	}
	if streamer.mainErrors != 1 {
		t.Fatalf("overflow responses served = %d, want 1", streamer.mainErrors)
	}
	if streamer.summaries != 1 {
		t.Fatalf("forced compaction summary requests = %d, want 1", streamer.summaries)
	}
	if !strings.Contains(result.Content, "done") {
		t.Fatalf("result content = %q, want the retried turn's output", result.Content)
	}
}

// Recovery must be bounded to one attempt per run.
func TestSubagentOverflowRecoveryIsAttemptedOnlyOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &subagentCompactStreamer{
		mainError:     errors.New("context_length_exceeded"),
		maxMainErrors: 99,
	}
	runtime := compactingSubagentRuntime(streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"base-model": 10_000_000},
	})
	if _, err := runtime.Run(context.Background(), Request{
		Prompt:          "Continue.",
		CWD:             t.TempDir(),
		InitialMessages: longSubagentHistory(),
	}, tools.Context{}); err == nil {
		t.Fatal("expected the sub-agent to fail once compaction could not shrink the prompt")
	}
	if streamer.summaries != 1 {
		t.Fatalf("forced compaction attempts = %d, want exactly 1", streamer.summaries)
	}
	if streamer.mainErrors != 2 {
		t.Fatalf("rejected main requests = %d, want 2 (original + one bounded retry)", streamer.mainErrors)
	}
}

// A non-overflow error must not cost the sub-agent its history.
func TestSubagentDoesNotCompactOnNonOverflowErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	streamer := &subagentCompactStreamer{
		mainError:     errors.New("rate_limit_error: too many requests"),
		maxMainErrors: 99,
	}
	runtime := compactingSubagentRuntime(streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"base-model": 10_000_000},
	})
	if _, err := runtime.Run(context.Background(), Request{
		Prompt:          "Continue.",
		CWD:             t.TempDir(),
		InitialMessages: longSubagentHistory(),
	}, tools.Context{}); err == nil {
		t.Fatal("expected the rate-limit error to surface")
	}
	if streamer.summaries != 0 {
		t.Fatalf("compaction ran for a rate-limit error (%d summary requests)", streamer.summaries)
	}
}

// AUDIT-P0-08 wiring: with no explicit Runtime.AutoCompact, the sub-agent must
// still get a compactor from the settings resolved for its cwd. Nothing else
// asserts newCompactor is called from Run, so this locks the wiring in place.
func TestSubagentCompactorDefaultsFromSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := Runtime{Client: &subagentCompactStreamer{}, Registry: tools.NewRegistry(echoTool{}), Model: "base-model"}
	if runtime.newCompactor(t.TempDir(), "base-model") == nil {
		t.Fatal("sub-agent has no compactor with default settings; delegated long tasks have no overflow protection")
	}
	disabled := compact.Config{Enabled: false}
	runtime.AutoCompact = &disabled
	if runtime.newCompactor(t.TempDir(), "base-model") != nil {
		t.Fatal("explicitly disabled AutoCompact was ignored")
	}
	// No client means no compaction is possible at all.
	runtime.Client = nil
	runtime.AutoCompact = nil
	if runtime.newCompactor(t.TempDir(), "base-model") != nil {
		t.Fatal("compactor built without a client")
	}
}

// defsForCompact must survive the tool-less sub-agent case: *tools.Registry
// panics on a nil receiver, and a typed nil boxed in an interface is not nil.
func TestDefsForCompactHandlesNilRegistry(t *testing.T) {
	if defs := defsForCompact(nil); defs != nil {
		t.Fatalf("defs = %v, want nil", defs)
	}
	var registry *tools.Registry
	if defs := defsForCompact(registry); defs != nil {
		t.Fatalf("defs = %v, want nil for a typed-nil registry", defs)
	}
}

func subagentMessagesContain(messages []anthropic.MessageParam, needle string) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, needle) {
				return true
			}
		}
	}
	return false
}
