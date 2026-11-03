package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

type LoggerSink struct{}

func (LoggerSink) Emit(ctx context.Context, event Event) error {
	attrs := []any{
		"event_name", event.Name,
		"category", event.Category,
		"source", event.Source,
		"status", event.Status,
		"schema_version", event.SchemaVersion,
		"span_id", event.SpanID,
		"parent_span_id", event.ParentSpanID,
		"turn_index", event.TurnIndex,
		"session_id", event.SessionID,
		"cli_session_id", event.CLISessionID,
		"request_purpose", event.RequestPurpose,
		"resource_type", event.ResourceType,
		"resource_id", event.ResourceID,
		"model", event.Model,
		"tool_name", event.ToolName,
		"duration_ms", event.DurationMS,
		"input_tokens", event.InputTokens,
		"output_tokens", event.OutputTokens,
	}
	if event.Error != "" {
		attrs = append(attrs, "error", event.Error)
	}
	if !event.StartedAt.IsZero() {
		attrs = append(attrs, "started_at", event.StartedAt)
	}
	if len(event.Properties) > 0 {
		attrs = append(attrs, "properties", event.Properties)
	}
	switch event.Status {
	case StatusError, StatusDenied, StatusBlocked:
		observability.Error(ctx, nil, event.Name, event.Source, "telemetry event", attrs...)
	default:
		observability.Info(ctx, nil, event.Name, event.Source, "telemetry event", attrs...)
	}
	return nil
}

type MemorySink struct {
	mu     sync.Mutex
	events []Event
}

func NewMemorySink() *MemorySink {
	return &MemorySink{}
}

func (s *MemorySink) Emit(_ context.Context, event Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *MemorySink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func (s *MemorySink) Reset() {
	s.mu.Lock()
	s.events = nil
	s.mu.Unlock()
}

type Recorder interface {
	RecordTelemetry(ctx context.Context, event Event) (uint64, error)
}

type RecorderSink struct {
	Recorder Recorder
}

func (s RecorderSink) Emit(ctx context.Context, event Event) error {
	if s.Recorder == nil || event.Name == "" {
		return nil
	}
	if shouldSkipRecorderEvent(event) {
		return nil
	}
	_, err := s.Recorder.RecordTelemetry(ctx, event)
	return err
}

func shouldSkipRecorderEvent(event Event) bool {
	return event.TenantKey == observability.DefaultTenantKey || event.UserKey == observability.DefaultUserID
}

type HTTPSink struct {
	Endpoint string
	Headers  map[string]string
	Format   string
	Service  string
	Client   *http.Client
}

func NewHTTPSink(endpoint string, headers map[string]string) *HTTPSink {
	return &HTTPSink{
		Endpoint: strings.TrimSpace(endpoint),
		Headers:  cloneHeaders(headers),
		Client:   &http.Client{Timeout: 2 * time.Second},
	}
}

func NewVendorHTTPSink(endpoint, format, service string, headers map[string]string) *HTTPSink {
	sink := NewHTTPSink(endpoint, headers)
	sink.Format = strings.ToLower(strings.TrimSpace(format))
	sink.Service = strings.TrimSpace(service)
	return sink
}

func (s *HTTPSink) Emit(ctx context.Context, event Event) error {
	if s == nil || s.Endpoint == "" || event.Name == "" {
		return nil
	}
	payload := s.payload(event)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range s.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telemetry http exporter status %d", resp.StatusCode)
	}
	return nil
}

func (s *HTTPSink) payload(event Event) any {
	switch strings.ToLower(strings.TrimSpace(s.Format)) {
	case "", "json", "niuma", "niuma-legacy":
		return event
	case "otel", "otlp", "otlp-log", "otlp-logs":
		return otelLogPayload(event, firstNonEmptyString(s.Service, "niuma"))
	case "datadog", "dd":
		return datadogLogPayload(event, firstNonEmptyString(s.Service, "niuma"))
	default:
		return map[string]any{
			"vendor_format": s.Format,
			"service":       firstNonEmptyString(s.Service, "niuma"),
			"event":         event,
		}
	}
}

func otelLogPayload(event Event, service string) map[string]any {
	attrs := map[string]any{
		"event.name":      event.Name,
		"schema_version":  event.SchemaVersion,
		"event.category":  event.Category,
		"event.status":    event.Status,
		"service.name":    service,
		"source":          event.Source,
		"trace_id":        event.TraceID,
		"span_id":         event.SpanID,
		"parent_span_id":  event.ParentSpanID,
		"turn_index":      event.TurnIndex,
		"tenant_key":      event.TenantKey,
		"user_key":        event.UserKey,
		"session_id":      event.SessionID,
		"cli_session_id":  event.CLISessionID,
		"request_purpose": event.RequestPurpose,
		"resource.type":   event.ResourceType,
		"resource.id":     event.ResourceID,
		"model":           event.Model,
		"tool.name":       event.ToolName,
		"duration_ms":     event.DurationMS,
		"started_at":      event.StartedAt,
		"input_tokens":    event.InputTokens,
		"output_tokens":   event.OutputTokens,
		"cache_read":      event.CacheReadInputTokens,
		"cache_creation":  event.CacheCreationInputTokens,
		"error.message":   event.Error,
		"properties_json": event.Properties,
	}
	return map[string]any{
		"resourceLogs": []any{
			map[string]any{
				"resource": map[string]any{
					"attributes": []any{stringAttribute("service.name", service)},
				},
				"scopeLogs": []any{
					map[string]any{
						"scope": map[string]any{"name": "niuma"},
						"logRecords": []any{
							map[string]any{
								"timeUnixNano": unixNanoString(event.OccurredAt),
								"severityText": severityText(event.Status),
								"body":         map[string]any{"stringValue": event.Name},
								"attributes":   otelAttributes(attrs),
							},
						},
					},
				},
			},
		},
	}
}

func datadogLogPayload(event Event, service string) map[string]any {
	return map[string]any{
		"ddsource": "niuma",
		"service":  service,
		"status":   datadogStatus(event.Status),
		"message":  event.Name,
		"hostname": event.TenantKey,
		"attributes": map[string]any{
			"category":        event.Category,
			"schema_version":  event.SchemaVersion,
			"source":          event.Source,
			"trace_id":        event.TraceID,
			"span_id":         event.SpanID,
			"parent_span_id":  event.ParentSpanID,
			"turn_index":      event.TurnIndex,
			"tenant_key":      event.TenantKey,
			"user_key":        event.UserKey,
			"session_id":      event.SessionID,
			"cli_session_id":  event.CLISessionID,
			"request_purpose": event.RequestPurpose,
			"resource_type":   event.ResourceType,
			"resource_id":     event.ResourceID,
			"model":           event.Model,
			"tool_name":       event.ToolName,
			"duration_ms":     event.DurationMS,
			"started_at":      event.StartedAt,
			"input_tokens":    event.InputTokens,
			"output_tokens":   event.OutputTokens,
			"error":           event.Error,
			"properties":      event.Properties,
			"occurred_at":     event.OccurredAt,
			"telemetry_name":  event.Name,
		},
	}
}

func otelAttributes(values map[string]any) []any {
	attrs := make([]any, 0, len(values))
	for key, value := range values {
		if isZeroAttribute(value) {
			continue
		}
		attrs = append(attrs, map[string]any{"key": key, "value": otelAnyValue(value)})
	}
	return attrs
}

func otelAnyValue(value any) map[string]any {
	switch v := value.(type) {
	case string:
		return map[string]any{"stringValue": v}
	case int:
		return map[string]any{"intValue": fmt.Sprintf("%d", v)}
	case int64:
		return map[string]any{"intValue": fmt.Sprintf("%d", v)}
	case uint64:
		return map[string]any{"intValue": fmt.Sprintf("%d", v)}
	case time.Time:
		return map[string]any{"stringValue": v.UTC().Format(time.RFC3339Nano)}
	case bool:
		return map[string]any{"boolValue": v}
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return map[string]any{"stringValue": fmt.Sprint(v)}
		}
		return map[string]any{"stringValue": string(data)}
	}
}

func stringAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func unixNanoString(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return fmt.Sprintf("%d", t.UnixNano())
}

func severityText(status string) string {
	switch status {
	case StatusError, StatusDenied, StatusBlocked:
		return "ERROR"
	case StatusStarted:
		return "INFO"
	default:
		return "INFO"
	}
}

func datadogStatus(status string) string {
	switch status {
	case StatusError, StatusDenied, StatusBlocked:
		return "error"
	case StatusStarted:
		return "info"
	default:
		return "info"
	}
}

func isZeroAttribute(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case int:
		return v == 0
	case int64:
		return v == 0
	case uint64:
		return v == 0
	case map[string]any:
		return len(v) == 0
	default:
		return false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}
