package session

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestRuntimeSpanSinkPersistsOnlySanitizedFinishedMetadata(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir()}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "91919191-9191-4191-8191-919191919191")
	if err != nil {
		t.Fatal(err)
	}
	sink := NewRuntimeSpanSink(recorder)
	ctx := telemetry.WithEmitter(context.Background(), telemetry.NewEmitter(sink))
	properties := map[string]any{
		"input_bytes":         12,
		"concurrency_class":   "read_only",
		"hook_event":          "PreToolUse",
		"gate_type":           "pre_tool",
		"entry_type":          "tool_result",
		"renderer":            "tool_result",
		"mode":                "automatic",
		"compacted":           true,
		"estimated_tokens":    42,
		"events":              3,
		"failure_kind":        "responses_sse_json_decode",
		"first_delta_gap_ms":  120,
		"first_delta_seen":    true,
		"first_event_gap_ms":  30,
		"first_event_seen":    true,
		"severity":            "allow",
		"rule_id":             "read-only",
		"provider_request_id": "req_success",
		"retryable":           true,
		"retry_attempt":       1,
		"retry_limit":         6,
		"retry_reason":        "reasoning_only_stream_decode",
		"retry_outcome":       "recovered",
		"http_status":         200,
		"response_mime":       "text/event-stream",
		"prompt":              "do not persist",
		"secret":              "top-secret",
		"reasoning":           "private chain of thought",
		"tool_arguments":      `{"file_path":"secret"}`,
		"authorization":       "Bearer private-token",
	}
	properties["last_failed_provider_request_id"] = "req_failed"
	ctx, span := telemetry.StartSpan(ctx, telemetry.Event{
		Name:       telemetry.EventToolExecution,
		Category:   telemetry.CategoryTool,
		ToolName:   "Bash",
		Properties: properties,
	})
	_ = ctx
	span.FinishEvent(telemetry.Event{
		Status: telemetry.StatusError,
		Error:  "tool output contains top-secret",
		Properties: map[string]any{
			"output_bytes":  27,
			"output":        "top-secret",
			"response_mime": "text/event-stream",
		},
	})
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	entries, format, err := LoadWithFormat(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if format != TranscriptFormatGolangCCV1 || len(entries) != 1 {
		t.Fatalf("format=%s entries=%+v", format, entries)
	}
	entry := entries[0]
	if entry.Type != EntryTypeRuntimeSpan || strings.Contains(entry.Content, "top-secret") || strings.Contains(entry.Content, "prompt") ||
		strings.Contains(entry.Content, "private chain of thought") || strings.Contains(entry.Content, "private-token") || strings.Contains(entry.Content, "secret\"") {
		t.Fatalf("runtime span was not sanitized: %+v", entry)
	}
	event, err := DecodeRuntimeSpanEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if event.Name != telemetry.EventToolExecution+telemetry.SpanFinishedSuffix || event.ToolName != "Bash" || event.Status != telemetry.StatusError {
		t.Fatalf("event = %+v", event)
	}
	if event.Error != "" || event.Properties["input_bytes"] != float64(12) || event.Properties["output_bytes"] != float64(27) {
		t.Fatalf("unsafe or missing metadata: %+v", event)
	}
	wantProperties := map[string]any{
		"concurrency_class":   "read_only",
		"hook_event":          "PreToolUse",
		"gate_type":           "pre_tool",
		"entry_type":          "tool_result",
		"renderer":            "tool_result",
		"mode":                "automatic",
		"compacted":           true,
		"estimated_tokens":    float64(42),
		"events":              float64(3),
		"failure_kind":        "responses_sse_json_decode",
		"first_delta_gap_ms":  float64(120),
		"first_delta_seen":    true,
		"first_event_gap_ms":  float64(30),
		"first_event_seen":    true,
		"severity":            "allow",
		"rule_id":             "read-only",
		"provider_request_id": "req_success",
		"retryable":           true,
		"retry_attempt":       float64(1),
		"retry_limit":         float64(6),
		"retry_reason":        "reasoning_only_stream_decode",
		"retry_outcome":       "recovered",
		"http_status":         float64(200),
		"response_mime":       "text/event-stream",
	}
	wantProperties["last_failed_provider_request_id"] = "req_failed"
	for key, want := range wantProperties {
		if got := event.Properties[key]; got != want {
			t.Fatalf("property %s = %#v, want %#v; properties=%+v", key, got, want, event.Properties)
		}
	}
}

func TestRuntimeSpanSinkSkipsStartedAndLegacyEvents(t *testing.T) {
	sink := &RuntimeSpanSink{}
	if err := sink.Emit(context.Background(), telemetry.Event{Name: "legacy.finished"}); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRuntimeSpanEntryRejectsMissingSchemaBeforeHydration(t *testing.T) {
	entry := Entry{
		Type:    EntryTypeRuntimeSpan,
		Content: `{"name":"tool.execution.finished","span_id":"orphan"}`,
	}
	if _, err := DecodeRuntimeSpanEntry(entry); err == nil {
		t.Fatal("runtime span without schema unexpectedly decoded")
	}
}

func TestRuntimeSpanDoesNotChangeV2ConversationGraph(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "95959595-9595-4595-8595-959595959595")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(Entry{ID: "user-1", Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := NewRuntimeSpanSink(recorder).Emit(context.Background(), telemetry.Event{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Name:          telemetry.EventModelRequest + telemetry.SpanFinishedSuffix,
		Status:        telemetry.StatusOK,
		SpanID:        "model-1",
		StartedAt:     now,
		OccurredAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	if got := recorder.Leaf(); got != "user-1" {
		t.Fatalf("runtime span changed live leaf to %q", got)
	}
	if err := recorder.Append(Entry{ID: "assistant-1", Type: "message", Role: "assistant", Content: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Type == EntryTypeRuntimeSpan && entry.ParentID != "" {
			t.Fatalf("runtime span joined conversation chain: %+v", entry)
		}
	}
	if got := ActiveLeaf(entries); got != "assistant-1" {
		t.Fatalf("active leaf = %q, want assistant-1", got)
	}
	leaves := Leaves(entries)
	if len(leaves) != 1 || leaves[0] != "assistant-1" {
		t.Fatalf("conversation leaves = %v", leaves)
	}
	chain := CurrentChain(entries)
	for _, entry := range chain {
		if entry.Type == EntryTypeRuntimeSpan {
			t.Fatalf("runtime span appeared in current conversation chain: %+v", chain)
		}
	}
}

func TestRecorderSerializesRuntimeAndConversationEntries(t *testing.T) {
	store := Store{TranscriptProjectsRoot: t.TempDir(), SchemaV2: true}
	recorder, err := store.NewRecorderWithID(t.TempDir(), "93939393-9393-4393-8393-939393939393")
	if err != nil {
		t.Fatal(err)
	}
	sink := NewRuntimeSpanSink(recorder)
	const pairs = 40
	var wg sync.WaitGroup
	for i := 0; i < pairs; i++ {
		wg.Add(2)
		go func(index int) {
			defer wg.Done()
			_ = recorder.Append(Entry{Type: "message", Role: "assistant", Content: fmt.Sprintf("message-%d", index)})
		}(i)
		go func(index int) {
			defer wg.Done()
			now := time.Now().UTC()
			_ = sink.Emit(context.Background(), telemetry.Event{
				SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
				Name:          telemetry.EventToolExecution + telemetry.SpanFinishedSuffix,
				Status:        telemetry.StatusOK,
				SpanID:        fmt.Sprintf("span-%d", index),
				StartedAt:     now,
				OccurredAt:    now,
			})
		}(i)
	}
	wg.Wait()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(recorder.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1+pairs*2 {
		t.Fatalf("entries = %d, want %d", len(entries), 1+pairs*2)
	}
}
