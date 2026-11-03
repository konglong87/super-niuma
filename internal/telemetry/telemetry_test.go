package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

func TestEmitterNormalizesAndSanitizesEvents(t *testing.T) {
	sink := NewMemorySink()
	emitter := NewEmitter(sink)
	ctx := observability.WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1")

	emitter.Emit(ctx, Event{
		Name:     "api.request.finished",
		Category: CategoryAPI,
		Status:   StatusOK,
		Properties: map[string]any{
			"status":        200,
			"authorization": "Bearer secret",
			"prompt":        "do not store",
			"safe":          "value",
		},
	})

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if event.TraceID != "trace-1" || event.UserKey != "user-1" || event.TenantKey != "tenant-1" {
		t.Fatalf("context fields = %+v", event)
	}
	if _, ok := event.Properties["authorization"]; ok {
		t.Fatalf("authorization leaked: %+v", event.Properties)
	}
	if _, ok := event.Properties["prompt"]; ok {
		t.Fatalf("prompt leaked: %+v", event.Properties)
	}
	if event.Properties["safe"] != "value" || event.Properties["status"] != 200 {
		t.Fatalf("properties = %+v", event.Properties)
	}
}

func TestEmitterAddsCLISessionAndRequestPurposeFromContext(t *testing.T) {
	sink := NewMemorySink()
	emitter := NewEmitter(sink)
	ctx := observability.WithCLISessionID(context.Background(), "session-uuid")
	ctx = observability.WithRequestPurpose(ctx, "main")
	emitter.Emit(ctx, Event{Name: "model.request.started", SessionID: 42})
	event := sink.Events()[0]
	if event.CLISessionID != "session-uuid" || event.RequestPurpose != "main" || event.SessionID != 42 {
		t.Fatalf("event = %+v", event)
	}

	emitter.Emit(ctx, Event{Name: "model.request.started", CLISessionID: "explicit", RequestPurpose: "compact"})
	event = sink.Events()[1]
	if event.CLISessionID != "explicit" || event.RequestPurpose != "compact" {
		t.Fatalf("explicit event = %+v", event)
	}
}

func TestStartSpanPropagatesIdentityParentAndTurn(t *testing.T) {
	sink := NewMemorySink()
	ctx := WithEmitter(context.Background(), NewEmitter(sink))

	rootCtx, root := StartSpan(ctx, Event{Name: "query.run", TurnIndex: 2, Properties: map[string]any{"safe": "root"}})
	childCtx, child := StartSpan(rootCtx, Event{Name: "model.request"})
	child.Finish(StatusOK, nil, map[string]any{"result": "done", "authorization": "secret"})
	root.Finish(StatusOK, nil, nil)

	events := sink.Events()
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	rootStart, childStart, childFinish, rootFinish := events[0], events[1], events[2], events[3]
	if rootStart.Name != "query.run.started" || rootFinish.Name != "query.run.finished" {
		t.Fatalf("root event names = %q, %q", rootStart.Name, rootFinish.Name)
	}
	if childStart.Name != "model.request.started" || childFinish.Name != "model.request.finished" {
		t.Fatalf("child event names = %q, %q", childStart.Name, childFinish.Name)
	}
	if rootStart.SpanID == "" || rootStart.SpanID != rootFinish.SpanID {
		t.Fatalf("root span identity = %+v / %+v", rootStart, rootFinish)
	}
	if childStart.SpanID == "" || childStart.SpanID != childFinish.SpanID || childStart.SpanID == rootStart.SpanID {
		t.Fatalf("child span identity = %+v / %+v", childStart, childFinish)
	}
	if childStart.ParentSpanID != rootStart.SpanID || SpanIDFromContext(childCtx) != childStart.SpanID {
		t.Fatalf("child parent/context = %+v context=%q", childStart, SpanIDFromContext(childCtx))
	}
	if rootStart.TurnIndex != 2 || childStart.TurnIndex != 2 || TurnIndexFromContext(childCtx) != 2 {
		t.Fatalf("turn propagation = root:%d child:%d context:%d", rootStart.TurnIndex, childStart.TurnIndex, TurnIndexFromContext(childCtx))
	}
	if childFinish.SchemaVersion != SchemaVersionRuntimeTraceV1 || childFinish.StartedAt.IsZero() {
		t.Fatalf("trace schema/start = %+v", childFinish)
	}
	if childFinish.Properties[PropertySpanID] != childFinish.SpanID || childFinish.Properties[PropertyParentSpanID] != rootStart.SpanID {
		t.Fatalf("persisted trace properties = %+v", childFinish.Properties)
	}
	if childFinish.Properties["result"] != "done" {
		t.Fatalf("finish properties = %+v", childFinish.Properties)
	}
	if _, ok := childFinish.Properties["authorization"]; ok {
		t.Fatalf("sensitive property leaked: %+v", childFinish.Properties)
	}
}

func TestStartPreservesLegacyNameAndStatusContract(t *testing.T) {
	sink := NewMemorySink()
	ctx := WithEmitter(context.Background(), NewEmitter(sink))
	span := Start(ctx, Event{Name: "legacy.operation", Status: StatusDenied, Properties: map[string]any{"start": true}})
	span.Finish(StatusOK, nil, map[string]any{"finish": true})

	events := sink.Events()
	if len(events) != 2 || events[0].Name != "legacy.operation" || events[1].Name != "legacy.operation" {
		t.Fatalf("legacy event names = %+v", events)
	}
	if events[0].Status != StatusDenied || events[1].Status != StatusOK {
		t.Fatalf("legacy statuses = %+v", events)
	}
	if events[0].SchemaVersion != "" || events[0].SpanID != "" || events[1].SpanID != "" {
		t.Fatalf("legacy event unexpectedly became runtime trace: %+v", events)
	}
	if events[1].Properties["finish"] != true || events[1].Properties["start"] != nil {
		t.Fatalf("legacy finish properties = %+v", events[1].Properties)
	}
}

func TestStartSpanSameNameConcurrencyKeepsPairsUnique(t *testing.T) {
	const spanCount = 100
	sink := NewMemorySink()
	ctx := WithEmitter(context.Background(), NewEmitter(sink))
	var wg sync.WaitGroup
	for range spanCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, span := StartSpan(ctx, Event{Name: "tool.execution", ToolName: "Read"})
			span.Finish(StatusOK, nil, nil)
		}()
	}
	wg.Wait()

	counts := make(map[string]int, spanCount)
	for _, event := range sink.Events() {
		if event.SpanID == "" {
			t.Fatal("span id is empty")
		}
		counts[event.SpanID]++
	}
	if len(counts) != spanCount {
		t.Fatalf("unique span ids = %d, want %d", len(counts), spanCount)
	}
	for spanID, count := range counts {
		if count != 2 {
			t.Fatalf("span %s emitted %d events, want 2", spanID, count)
		}
	}
}

func TestSpanFinishReportsErrorOnce(t *testing.T) {
	sink := NewMemorySink()
	ctx := WithEmitter(context.Background(), NewEmitter(sink))
	_, span := StartSpan(ctx, Event{Name: "tool.execution", Properties: map[string]any{"source": "test"}})
	span.Finish(StatusDenied, errors.New("boom"), map[string]any{"attempt": 1})
	span.Finish(StatusOK, nil, nil)

	events := sink.Events()
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	finish := events[1]
	if finish.Status != StatusError || finish.Error != "boom" || finish.Properties["source"] != "test" || finish.Properties["attempt"] != 1 {
		t.Fatalf("finish = %+v", finish)
	}
}

func TestNormalizeHydratesTraceFieldsFromProperties(t *testing.T) {
	startedAt := time.Date(2026, 8, 10, 9, 8, 7, 123, time.UTC)
	event := Normalize(context.Background(), Event{Properties: map[string]any{
		PropertySchemaVersion: SchemaVersionRuntimeTraceV1,
		PropertySpanID:        "span-1",
		PropertyParentSpanID:  "parent-1",
		PropertyTurnIndex:     float64(3),
		PropertyStartedAt:     startedAt.Format(time.RFC3339Nano),
	}})
	if event.SchemaVersion != SchemaVersionRuntimeTraceV1 || event.SpanID != "span-1" || event.ParentSpanID != "parent-1" || event.TurnIndex != 3 || !event.StartedAt.Equal(startedAt) {
		t.Fatalf("event = %+v", event)
	}
}

func TestNormalizeDoesNotHydrateUnversionedOrFutureTraceProperties(t *testing.T) {
	for _, schema := range []string{"", "runtime-trace-v2"} {
		event := RestoreTraceContext(Event{Properties: map[string]any{
			PropertySchemaVersion: schema,
			PropertySpanID:        "legacy-span",
			PropertyParentSpanID:  "legacy-parent",
			PropertyTurnIndex:     7,
			PropertyStartedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		}})
		if event.SpanID != "" || event.ParentSpanID != "" || event.TurnIndex != 0 || !event.StartedAt.IsZero() {
			t.Fatalf("schema %q was interpreted as runtime trace: %+v", schema, event)
		}
	}
}

func TestValidateRuntimeTraceEventRejectsMissingUnknownAndInvalidIdentity(t *testing.T) {
	tests := []Event{
		{SpanID: "span-without-schema"},
		{SchemaVersion: "runtime-trace-v2", SpanID: "span-1"},
		{SchemaVersion: SchemaVersionRuntimeTraceV1},
		{SchemaVersion: SchemaVersionRuntimeTraceV1, SpanID: "span with spaces"},
		{SchemaVersion: SchemaVersionRuntimeTraceV1, SpanID: "same", ParentSpanID: "same"},
		{SchemaVersion: SchemaVersionRuntimeTraceV1, SpanID: "span-1", TurnIndex: -1},
	}
	for _, event := range tests {
		if err := ValidateRuntimeTraceEvent(event); err == nil {
			t.Fatalf("event unexpectedly validated: %+v", event)
		}
	}
	if err := ValidateRuntimeTraceEvent(Event{SchemaVersion: SchemaVersionRuntimeTraceV1, SpanID: "span-1", ParentSpanID: "root:1", TurnIndex: 1}); err != nil {
		t.Fatalf("valid runtime trace rejected: %v", err)
	}
}

func TestMarshalPropertiesRetainsTraceIdentityWhenAttributeCannotMarshal(t *testing.T) {
	got := MarshalProperties(map[string]any{
		PropertySchemaVersion: SchemaVersionRuntimeTraceV1,
		PropertySpanID:        "span-1",
		"bad":                 func() {},
	})
	if !strings.Contains(got, `"span_id":"span-1"`) || strings.Contains(got, `"bad"`) {
		t.Fatalf("properties = %s", got)
	}
}

func TestLegacyEventJSONOmitsEmptyTraceFields(t *testing.T) {
	data, err := json.Marshal(Event{Name: "legacy.event"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schema_version", "span_id", "parent_span_id", "turn_index", "started_at"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("legacy JSON unexpectedly contains %q: %s", field, data)
		}
	}
}

func TestRecorderSinkSkipsAnonymousDefaultTenant(t *testing.T) {
	recorder := &fakeRecorder{}
	sink := RecorderSink{Recorder: recorder}

	if err := sink.Emit(context.Background(), Normalize(context.Background(), Event{Name: "api.request.finished"})); err != nil {
		t.Fatal(err)
	}
	if recorder.count != 0 {
		t.Fatalf("count = %d", recorder.count)
	}

	ctx := observability.WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1")
	if err := sink.Emit(ctx, Normalize(ctx, Event{Name: "api.request.finished"})); err != nil {
		t.Fatal(err)
	}
	if recorder.count != 1 || recorder.last.Name != "api.request.finished" {
		t.Fatalf("recorder = %+v count=%d", recorder.last, recorder.count)
	}
}

func TestEmitterSwallowsSinkErrors(t *testing.T) {
	ok := NewMemorySink()
	emitter := NewEmitter(SinkFunc(func(context.Context, Event) error { return errors.New("boom") }), ok)
	emitter.Emit(context.Background(), Event{Name: "event"})
	if len(ok.Events()) != 1 {
		t.Fatalf("events = %+v", ok.Events())
	}
}

func TestMetricsSinkExportsPrometheusCounters(t *testing.T) {
	sink := NewMetricsSink()
	emitter := NewEmitter(sink)
	emitter.Emit(context.Background(), Event{Name: `model.request.finished`, Category: CategoryModel, Status: StatusOK, DurationMS: 25, InputTokens: 3, OutputTokens: 5})
	emitter.Emit(context.Background(), Event{Name: `model.request.finished`, Category: CategoryModel, Status: StatusOK, DurationMS: 75, InputTokens: 7, OutputTokens: 11})

	out := string(sink.Prometheus())
	for _, want := range []string{
		`# HELP golang_cc_telemetry_events_total`,
		`golang_cc_telemetry_events_total{event_name="model.request.finished",category="model",status="ok"} 2`,
		`# HELP golang_claude_code_telemetry_events_total`,
		`golang_claude_code_telemetry_events_total{event_name="model.request.finished",category="model",status="ok"} 2`,
		`golang_claude_code_telemetry_duration_ms_total{event_name="model.request.finished",category="model",status="ok"} 100`,
		`golang_claude_code_telemetry_input_tokens_total{event_name="model.request.finished",category="model",status="ok"} 10`,
		`golang_claude_code_telemetry_output_tokens_total{event_name="model.request.finished",category="model",status="ok"} 16`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHTTPSinkExportsNormalizedEvent(t *testing.T) {
	var got Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer export-token" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sink := NewHTTPSink(server.URL, map[string]string{"Authorization": "Bearer export-token"})
	emitter := NewEmitter(sink)
	ctx := observability.WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1")
	emitter.Emit(ctx, Event{
		Name:     "model.request.finished",
		Category: CategoryModel,
		Status:   StatusOK,
		Properties: map[string]any{
			"content": "redacted",
			"safe":    "kept",
		},
	})
	if got.Name != "model.request.finished" || got.TraceID != "trace-1" || got.Properties["safe"] != "kept" {
		t.Fatalf("event = %+v", got)
	}
	if _, ok := got.Properties["content"]; ok {
		t.Fatalf("sensitive content leaked: %+v", got.Properties)
	}
}

func TestVendorHTTPSinkExportsOTelLogEnvelope(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sink := NewVendorHTTPSink(server.URL, "otel", "niuma-api", nil)
	emitter := NewEmitter(sink)
	startedAt := time.Date(2026, 8, 10, 2, 3, 4, 5, time.UTC)
	emitter.Emit(context.Background(), Event{Name: "model.request.finished", SchemaVersion: SchemaVersionRuntimeTraceV1, Category: CategoryModel, Status: StatusOK, DurationMS: 42, SpanID: "span-1", ParentSpanID: "root-1", TurnIndex: 2, StartedAt: startedAt})

	resourceLogs, ok := got["resourceLogs"].([]any)
	if !ok || len(resourceLogs) != 1 {
		t.Fatalf("resourceLogs = %#v", got["resourceLogs"])
	}
	data, _ := json.Marshal(got)
	body := string(data)
	for _, want := range []string{`"service.name"`, `"niuma-api"`, `"model.request.finished"`, `"duration_ms"`, `"schema_version"`, `"runtime-trace-v1"`, `"span_id"`, `"parent_span_id"`, `"turn_index"`, `"stringValue":"2026-08-10T02:03:04.000000005Z"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
}

func TestVendorHTTPSinkExportsDatadogEnvelope(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	sink := NewVendorHTTPSink(server.URL, "datadog", "niuma-api", nil)
	emitter := NewEmitter(sink)
	emitter.Emit(context.Background(), Event{Name: "tool.execution.finished", SchemaVersion: SchemaVersionRuntimeTraceV1, Category: CategoryTool, Status: StatusError, Error: "failed", SpanID: "span-2", ParentSpanID: "root-1", TurnIndex: 3, StartedAt: time.Date(2026, 8, 10, 2, 3, 4, 5, time.UTC)})

	if got["ddsource"] != "niuma" || got["service"] != "niuma-api" || got["status"] != "error" || got["message"] != "tool.execution.finished" {
		t.Fatalf("datadog payload = %#v", got)
	}
	attributes, ok := got["attributes"].(map[string]any)
	if !ok || attributes["schema_version"] != SchemaVersionRuntimeTraceV1 || attributes["span_id"] != "span-2" || attributes["parent_span_id"] != "root-1" || attributes["turn_index"] != float64(3) || attributes["started_at"] != "2026-08-10T02:03:04.000000005Z" {
		t.Fatalf("datadog attributes = %#v", got["attributes"])
	}
}

type fakeRecorder struct {
	count int
	last  Event
}

func (f *fakeRecorder) RecordTelemetry(ctx context.Context, event Event) (uint64, error) {
	f.count++
	f.last = event
	return uint64(f.count), nil
}
