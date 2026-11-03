package query

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/toolresult"
	"github.com/konglong87/super-niuma/internal/tools"
)

// bigToolResultBlob must stay under toolresult.DefaultLimit: tools.EffectiveResultLimit
// clamps any per-tool limit to that value, so a larger result would be
// externalized by toolresult.Process at tool-run time and never reach the
// message history — which is precisely the ordering these tests examine. At this
// size Process leaves it alone and only the message/history budget can shrink it.
const bigToolResultBlob = 45_000

// compactOrderStreamer scripts the shape these tests need: the first model turn
// calls a tool that returns an oversized result, and the turn after that is
// where externalization and the compaction threshold check compete. Compaction
// summary requests are answered with a valid summary so the compactor succeeds.
type compactOrderStreamer struct {
	requests []anthropic.MessagesRequest
	// mainError, when set, is returned for every main request after the tool
	// call, up to maxMainErrors times.
	mainError     error
	maxMainErrors int

	mainCalls  int
	mainErrors int
	summaries  int
}

func (s *compactOrderStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.requests = append(s.requests, req)
	if isCompactSummaryRequest(req) {
		s.summaries++
		return streamText(cb, compactSummaryFixtureText())
	}
	s.mainCalls++
	if s.mainCalls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_big",
				Name:  "LongTool",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
		}, nil
	}
	if s.mainError != nil && s.mainErrors < s.maxMainErrors {
		s.mainErrors++
		return nil, s.mainError
	}
	return streamText(cb, "ok")
}

// compactOrderSession wires a session whose only large context contributor is a
// single oversized tool result produced mid-run, with a small custom system
// prompt so token arithmetic in these tests stays predictable.
func compactOrderSession(t *testing.T, streamer MessageStreamer, autoCompact compact.Config) (*Session, *session.Recorder) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "55555555-5555-4555-8555-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	registry := tools.NewRegistry(largeResultTool{
		content:       strings.Repeat("X", bigToolResultBlob),
		maxResultSize: toolresult.DefaultLimit,
	})
	querySession := New(streamer, registry, Options{
		Model:        "gpt-5.5",
		MaxTurns:     4,
		CWD:          t.TempDir(),
		Recorder:     recorder,
		SystemPrompt: "You are a test agent.",
		// Keep the raw result intact through per-result truncation so the only
		// thing that can shrink it is the message-budget externalization under test.
		ToolResultLimit: toolresult.DefaultLimit,
		// Force externalization of the blob: it is far above this budget.
		ToolResultMessageBudget: 4_000,
		ToolResultHistoryBudget: 4_000,
		InitialMessages: []anthropic.MessageParam{
			{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Please inspect internal/query/query.go and run go test ./internal/query."}}},
			{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "I will read the file."}}},
		},
		AutoCompact: autoCompact,
	})
	return querySession, recorder
}

func compactSummaryEntries(t *testing.T, recorder *session.Recorder) int {
	t.Helper()
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Type == "compact_summary" {
			count++
		}
	}
	return count
}

// AUDIT-P1-01: the compaction threshold check must run AFTER tool-result
// externalization. The behavioural gold standard is the token count the
// compactor actually measured: it must reflect the externalized stub (a 2 KB
// preview plus a path), not the raw 200 KB tool result. Under the old order the
// recorded trigger_tokens was the ~50k-token raw blob, and no summary request
// ever saw the externalized form.
func TestSessionCompactsExternalizedToolResultsNotRawBlobs(t *testing.T) {
	streamer := &compactOrderStreamer{}
	// Threshold sits above the pre-tool-call context but below the externalized
	// context, so compaction fires exactly once, on the turn after the tool call.
	querySession, recorder := compactOrderSession(t, streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.5,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 800},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err != nil {
		t.Fatal(err)
	}
	if streamer.summaries == 0 {
		t.Fatalf("no compaction happened, so this test proves nothing about ordering: %v", requestShapes(streamer.requests))
	}
	metadata := recordedCompactMetadata(t, recorder)
	if len(metadata) != 1 {
		t.Fatalf("compact_summary entries = %d, want 1", len(metadata))
	}
	if metadata[0].SourceEntryCount == 0 ||
		metadata[0].SourceEntryStartID == "" ||
		metadata[0].SourceEntryEndID == "" ||
		metadata[0].SourceEntryDigest == "" {
		t.Fatalf("compact source provenance = %+v", metadata[0])
	}
	var compactSpan telemetry.Event
	for _, event := range loadRuntimeSpanEvents(t, recorder.Path) {
		if event.Name == telemetry.EventCompact+telemetry.SpanFinishedSuffix && event.Properties["compacted"] == true {
			compactSpan = event
			break
		}
	}
	if compactSpan.SpanID == "" || compactSpan.ParentSpanID == "" || compactSpan.Status != telemetry.StatusOK || compactSpan.Properties["mode"] != "automatic" {
		t.Fatalf("successful compact span = %+v", compactSpan)
	}
	if _, ok := compactSpan.Properties["estimated_tokens"].(float64); !ok {
		t.Fatalf("compact estimated_tokens was not persisted as safe numeric metadata: %+v", compactSpan.Properties)
	}
	rawBlobTokens := rawBlobTokenEstimate()
	if got := metadata[0].TriggerTokens; got > rawBlobTokens/2 {
		t.Fatalf("compaction measured %d tokens against a raw blob worth %d; externalization did not run first", got, rawBlobTokens)
	}
	// And no request the compactor sent may carry the raw blob.
	for i, req := range streamer.requests {
		for _, message := range req.Messages {
			for _, block := range message.Content {
				if strings.Contains(block.Content, strings.Repeat("X", 10_000)) || strings.Contains(block.Text, strings.Repeat("X", 10_000)) {
					t.Fatalf("request %d (%s) still carries the raw tool-result blob", i, requestShapes(streamer.requests)[i])
				}
			}
		}
	}
}

func recordedCompactMetadata(t *testing.T, recorder *session.Recorder) []compact.Metadata {
	t.Helper()
	entries, err := session.Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	var out []compact.Metadata
	for _, entry := range entries {
		if entry.Type != "compact_summary" {
			continue
		}
		var metadata compact.Metadata
		if err := json.Unmarshal(entry.CompactMetadata, &metadata); err != nil {
			t.Fatalf("unmarshal compact metadata: %v", err)
		}
		out = append(out, metadata)
	}
	return out
}

// AUDIT-P1-01: when externalization alone brings the context back under the
// threshold, no LLM compaction call may happen at all. That is the whole point
// of the reorder — the old code paid for a summarization round that the free
// externalization made unnecessary.
func TestSessionSkipsCompactionWhenExternalizationFreesEnoughContext(t *testing.T) {
	streamer := &compactOrderStreamer{}
	// 5k-token threshold: far above the externalized context (a 2 KB preview
	// plus a path) and far below the raw blob (~11k tokens).
	querySession, recorder := compactOrderSession(t, streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.5,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 10_000},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err != nil {
		t.Fatal(err)
	}
	if streamer.summaries != 0 {
		t.Fatalf("paid for %d LLM compaction call(s); externalization had already freed enough context: %v", streamer.summaries, requestShapes(streamer.requests))
	}
	if count := compactSummaryEntries(t, recorder); count != 0 {
		t.Fatalf("compact_summary entries = %d, want 0", count)
	}
	// Sanity: the raw blob really would have crossed the threshold, so this test
	// genuinely distinguishes the two orderings.
	if raw := rawBlobTokenEstimate(); raw <= 5_000 {
		t.Fatalf("raw blob estimates at %d tokens, below the 5000 threshold; this test no longer distinguishes the two orderings", raw)
	}
}

// rawBlobTokenEstimate is what the compactor would measure if it ran before
// externalization.
func rawBlobTokenEstimate() int {
	return compact.RoughTokenCounter{}.EstimateMessages("gpt-5.5", []anthropic.MessageParam{
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_big", Content: strings.Repeat("X", bigToolResultBlob)}}},
	})
}

// AUDIT-P0-08: when the estimator undershoots and the provider rejects the
// prompt as too long, the session must force a compaction and retry instead of
// surfacing a hard failure. Without this path the whole session dies on a 400.
func TestSessionForcesCompactionAfterContextOverflowAndRetries(t *testing.T) {
	streamer := &compactOrderStreamer{
		mainError:     errors.New(`{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`),
		maxMainErrors: 1,
	}
	// Threshold high enough that proactive compaction never fires: the only way
	// this session can succeed is the reactive overflow path.
	querySession, recorder := compactOrderSession(t, streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 10_000_000},
	})
	var out strings.Builder
	result, err := querySession.Run(context.Background(), "Continue.", &out)
	if err != nil {
		t.Fatalf("session failed instead of recovering from context overflow: %v", err)
	}
	if streamer.mainErrors != 1 {
		t.Fatalf("overflow responses served = %d, want 1", streamer.mainErrors)
	}
	if streamer.summaries != 1 {
		t.Fatalf("forced compaction summary requests = %d, want 1: %v", streamer.summaries, requestShapes(streamer.requests))
	}
	rejected, retry := streamer.requests[1], streamer.requests[len(streamer.requests)-1]
	if !messagesContainText(retry.Messages, "Conversation summary so far:") {
		t.Fatalf("retry request was not sent with the compacted history: %v", requestShapes(streamer.requests))
	}
	if len(retry.Messages) >= len(rejected.Messages) {
		t.Fatalf("retry history (%d messages) did not shrink versus the rejected request (%d messages)", len(retry.Messages), len(rejected.Messages))
	}
	if count := compactSummaryEntries(t, recorder); count != 1 {
		t.Fatalf("compact_summary entries = %d, want 1", count)
	}
	if !strings.Contains(result.Response, "ok") {
		t.Fatalf("response = %q, want the retried turn's output", result.Response)
	}
}

// Overflow recovery must be bounded: a prompt that is still too long after one
// forced compaction has to fail, not retry until MaxTurns is exhausted.
func TestSessionOverflowRecoveryIsAttemptedOnlyOnce(t *testing.T) {
	streamer := &compactOrderStreamer{
		mainError:     errors.New("context_length_exceeded"),
		maxMainErrors: 99,
	}
	querySession, _ := compactOrderSession(t, streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 10_000_000},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err == nil {
		t.Fatal("expected the session to fail once compaction could not shrink the prompt")
	}
	if streamer.summaries != 1 {
		t.Fatalf("forced compaction attempts = %d, want exactly 1: %v", streamer.summaries, requestShapes(streamer.requests))
	}
	if streamer.mainErrors != 2 {
		t.Fatalf("rejected main requests = %d, want 2 (original + one bounded retry): %v", streamer.mainErrors, requestShapes(streamer.requests))
	}
}

// A non-overflow provider error must not trigger compaction: throwing away
// history for an unrelated failure loses context for no benefit.
func TestSessionDoesNotCompactOnNonOverflowErrors(t *testing.T) {
	streamer := &compactOrderStreamer{
		mainError:     errors.New("rate_limit_error: too many requests"),
		maxMainErrors: 99,
	}
	querySession, recorder := compactOrderSession(t, streamer, compact.Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.9,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 10_000_000},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err == nil {
		t.Fatal("expected the rate-limit error to surface")
	}
	if streamer.summaries != 0 {
		t.Fatalf("compaction ran for a rate-limit error: %v", requestShapes(streamer.requests))
	}
	if count := compactSummaryEntries(t, recorder); count != 0 {
		t.Fatalf("compact_summary entries = %d, want 0", count)
	}
}

// usageReportingStreamer reports a provider prompt size that is a fixed
// multiple of what the local estimator would produce, and keeps the loop going
// for one extra turn so the calibrated estimate gets a chance to act.
type usageReportingStreamer struct {
	reportedInputTokens int
	summaries           int
	mainCalls           int
}

func (s *usageReportingStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	if isCompactSummaryRequest(req) {
		s.summaries++
		return streamText(cb, compactSummaryFixtureText())
	}
	s.mainCalls++
	if s.mainCalls == 1 {
		return &anthropic.StreamResult{
			Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
				Type:  "tool_use",
				ID:    "toolu_noop",
				Name:  "LongTool",
				Input: json.RawMessage(`{}`),
			}}},
			StopReason: "tool_use",
			Usage:      anthropic.Usage{InputTokens: s.reportedInputTokens},
		}, nil
	}
	result, err := streamText(cb, "ok")
	if err != nil {
		return nil, err
	}
	result.Usage = anthropic.Usage{InputTokens: s.reportedInputTokens}
	return result, nil
}

// AUDIT-P1-05 wiring: the query loop must feed the provider's reported prompt
// size back into the compactor. Without that feedback the compactor never learns
// that its estimate undershoots, and a session whose real prompt is already over
// the threshold keeps reporting itself as safely below it.
func TestSessionFeedsProviderUsageBackIntoCompactor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := session.Store{Root: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "66666666-6666-4666-8666-666666666666")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	// ~7.5k estimated tokens of history: above the calibration floor, below the
	// 20k threshold. The provider reports 4x that, which must cross it.
	history := []anthropic.MessageParam{
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Work on internal/query/query.go and run go test ./internal/query."}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: strings.Repeat("analysis detail ", 2_000)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "Keep going."}}},
	}
	estimated := compact.RoughTokenCounter{}.EstimateMessages("gpt-5.5", history)
	if estimated < 1_000 || estimated > 20_000 {
		t.Fatalf("history estimates at %d tokens; the test needs it between the calibration floor and the threshold", estimated)
	}
	streamer := &usageReportingStreamer{reportedInputTokens: estimated * 4}
	querySession := New(streamer, tools.NewRegistry(largeResultTool{content: "small", maxResultSize: 1_000}), Options{
		Model:           "gpt-5.5",
		MaxTurns:        3,
		CWD:             t.TempDir(),
		Recorder:        recorder,
		SystemPrompt:    "You are a test agent.",
		InitialMessages: history,
		AutoCompact: compact.Config{
			Enabled:               true,
			DefaultThresholdRatio: 0.5,
			PreserveRecentRounds:  1,
			ModelContext:          map[string]int{"gpt-5.5": 40_000},
		},
	})
	var out strings.Builder
	if _, err := querySession.Run(context.Background(), "Continue.", &out); err != nil {
		t.Fatal(err)
	}
	if streamer.summaries == 0 {
		t.Fatalf("no compaction happened: the provider reported %d prompt tokens against a 20000 threshold, but the estimator (%d) was never corrected", streamer.reportedInputTokens, estimated)
	}
}

func isCompactSummaryRequest(req anthropic.MessagesRequest) bool {
	for _, message := range req.Messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, "Summarize the conversation history below") {
				return true
			}
		}
	}
	return false
}

func streamText(cb anthropic.StreamCallbacks, text string) (*anthropic.StreamResult, error) {
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

func compactSummaryFixtureText() string {
	return strings.Join([]string{
		"## Current Goal",
		"Continue implementation.",
		"## User Preferences / Constraints",
		"Keep tests updated.",
		"## Decisions Made",
		"Use auto compact.",
		"## Files / Code Changed",
		"internal/query/query.go",
		"## Commands / Test Results",
		"go test ./internal/query",
		"## Open Tasks",
		"Run tests.",
		"## Known Issues / Risks",
		"None.",
		"## Important Raw Facts",
		"internal/query/query.go and go test ./internal/query are important.",
	}, "\n")
}

func requestShapes(requests []anthropic.MessagesRequest) []string {
	out := make([]string, 0, len(requests))
	for _, req := range requests {
		kind := "main"
		if isCompactSummaryRequest(req) {
			kind = "summary"
		}
		out = append(out, kind)
	}
	return out
}

func messagesContainText(messages []anthropic.MessageParam, needle string) bool {
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.Contains(block.Text, needle) {
				return true
			}
		}
	}
	return false
}
