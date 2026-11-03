package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	EntryTypeRuntimeSpan                      = "runtime_span"
	runtimeSpanLastFailedProviderRequestIDKey = "last_failed_provider_request_id"
)

var runtimeSpanPropertyAllowlist = map[string]struct{}{
	"active_skill":           {},
	"active_skill_fallback":  {},
	"active_skill_source":    {},
	"active_skill_version":   {},
	"compacted":              {},
	"concurrency_class":      {},
	"delta_type":             {},
	"entry_type":             {},
	"event_type":             {},
	"estimated_tokens":       {},
	"events":                 {},
	"failure_kind":           {},
	"first_delta_gap_ms":     {},
	"first_delta_seen":       {},
	"first_event_gap_ms":     {},
	"first_event_seen":       {},
	"gate_type":              {},
	"hook_event":             {},
	"inference_geo":          {},
	"input_bytes":            {},
	"messages":               {},
	"mode":                   {},
	"output_bytes":           {},
	"partial_stream_recover": {},
	"phase":                  {},
	"provider_request_id":    {},
	"provider":               {},
	"provider_kind":          {},
	"provider_name":          {},
	"provider_protocol":      {},
	"provider_role":          {},
	"runtime_profile":        {},
	"renderer":               {},
	"rule_id":                {},
	"service_tier":           {},
	"severity":               {},
	"speed":                  {},
	"stop_reason":            {},
	"tool_calls":             {},
	"text_deltas":            {},
	"tool_deltas":            {},
	"tools":                  {},
	"turns":                  {},
	"reasoning_deltas":       {},
	"retryable":              {},
	"retry_attempt":          {},
	"retry_limit":            {},
	"retry_outcome":          {},
	"retry_reason":           {},
	"http_status":            {},
	"response_mime":          {},
}

// RuntimeSpanSink persists only completed, metadata-only native spans. It is
// deliberately independent from model context reconstruction.
type RuntimeSpanSink struct {
	Recorder *Recorder
	mu       sync.Mutex
}

func NewRuntimeSpanSink(recorder *Recorder) *RuntimeSpanSink {
	return &RuntimeSpanSink{Recorder: recorder}
}

func (s *RuntimeSpanSink) Emit(_ context.Context, event telemetry.Event) error {
	if s == nil || s.Recorder == nil || event.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 ||
		event.SpanID == "" || !strings.HasSuffix(event.Name, telemetry.SpanFinishedSuffix) {
		return nil
	}
	clean := runtimeSpanMetadata(event)
	if err := telemetry.ValidateRuntimeTraceEvent(clean); err != nil {
		return fmt.Errorf("validate runtime span: %w", err)
	}
	data, err := json.Marshal(clean)
	if err != nil {
		return fmt.Errorf("marshal runtime span: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Recorder.Append(Entry{Type: EntryTypeRuntimeSpan, Content: string(data)})
}

func DecodeRuntimeSpanEntry(entry Entry) (telemetry.Event, error) {
	if entry.Type != EntryTypeRuntimeSpan {
		return telemetry.Event{}, fmt.Errorf("entry type %q is not %s", entry.Type, EntryTypeRuntimeSpan)
	}
	var event telemetry.Event
	if err := json.Unmarshal([]byte(entry.Content), &event); err != nil {
		return telemetry.Event{}, fmt.Errorf("decode runtime span: %w", err)
	}
	if err := telemetry.ValidateRuntimeTraceEvent(event); err != nil {
		return telemetry.Event{}, fmt.Errorf("validate runtime span: %w", err)
	}
	return telemetry.RestoreTraceContext(event), nil
}

func runtimeSpanMetadata(event telemetry.Event) telemetry.Event {
	return telemetry.Event{
		SchemaVersion:                       event.SchemaVersion,
		Name:                                event.Name,
		Category:                            event.Category,
		Source:                              event.Source,
		Status:                              event.Status,
		TraceID:                             event.TraceID,
		SpanID:                              event.SpanID,
		ParentSpanID:                        event.ParentSpanID,
		TurnIndex:                           event.TurnIndex,
		SessionID:                           event.SessionID,
		CLISessionID:                        event.CLISessionID,
		RequestPurpose:                      event.RequestPurpose,
		ResourceType:                        event.ResourceType,
		ResourceID:                          event.ResourceID,
		Model:                               event.Model,
		ToolName:                            event.ToolName,
		DurationMS:                          event.DurationMS,
		InputTokens:                         event.InputTokens,
		OutputTokens:                        event.OutputTokens,
		CacheCreationInputTokens:            event.CacheCreationInputTokens,
		CacheReadInputTokens:                event.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: event.CacheCreationEphemeral1hInputTokens,
		CacheCreationEphemeral5mInputTokens: event.CacheCreationEphemeral5mInputTokens,
		Properties:                          runtimeSpanProperties(event.Properties),
		StartedAt:                           event.StartedAt,
		OccurredAt:                          event.OccurredAt,
	}
}

func runtimeSpanProperties(properties map[string]any) map[string]any {
	properties = telemetry.SanitizeProperties(properties)
	if len(properties) == 0 {
		return nil
	}
	out := make(map[string]any)
	for key, value := range properties {
		if runtimeSpanPropertyAllowed(key) {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func runtimeSpanPropertyAllowed(key string) bool {
	_, ok := runtimeSpanPropertyAllowlist[key]
	return ok || key == runtimeSpanLastFailedProviderRequestIDKey
}
