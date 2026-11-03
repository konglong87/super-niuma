package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/buildinfo"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestAnalyzeTraceSeparatesWorkWallCriticalAndDiagnostics(t *testing.T) {
	base := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	spans := finalizeTraceSpans([]traceSpan{
		{ID: "root", Type: "query", Name: "query.run", Start: base, End: base.Add(100 * time.Millisecond), DurationMS: 100},
		{ID: "read-1", ParentID: "root", Type: "tool", Name: "tool.execution", ToolName: "Read", ConcurrencyClass: "read_only", TurnIndex: 1, Start: base.Add(10 * time.Millisecond), End: base.Add(50 * time.Millisecond), DurationMS: 40},
		{ID: "grep-1", ParentID: "root", Type: "tool", Name: "tool.execution", ToolName: "Grep", ConcurrencyClass: "read_only", TurnIndex: 1, Start: base.Add(20 * time.Millisecond), End: base.Add(50 * time.Millisecond), DurationMS: 30},
		{ID: "model-1", ParentID: "root", Type: "model", Name: "model.request", TurnIndex: 1, Start: base.Add(55 * time.Millisecond), End: base.Add(95 * time.Millisecond), DurationMS: 40},
	})
	events := []traceEvent{
		{Type: "tool_call", ToolID: "a", ToolName: "Read", Input: `{"file_path":"/tmp/a"}`},
		{Type: "tool_result", ToolID: "a", ToolName: "Read", IsError: true},
		{Type: "tool_call", ToolID: "b", ToolName: "Read", Input: `{ "file_path": "/tmp/a" }`},
		{Type: "tool_result", ToolID: "b", ToolName: "Read", IsError: true},
	}
	analysis := analyzeTrace(events, spans)
	if analysis.Summary.ToolWorkMS != 70 || analysis.Summary.ToolWallMS != 40 {
		t.Fatalf("tool work/wall = %d/%d", analysis.Summary.ToolWorkMS, analysis.Summary.ToolWallMS)
	}
	if analysis.Summary.TaskWallMS != 100 || analysis.Summary.UnattributedMS != 20 {
		t.Fatalf("task/unattributed = %d/%d", analysis.Summary.TaskWallMS, analysis.Summary.UnattributedMS)
	}
	if analysis.Summary.CriticalPathMS != 100 {
		t.Fatalf("critical path = %d", analysis.Summary.CriticalPathMS)
	}
	if analysis.Summary.ParallelSavingsMS != 0 {
		t.Fatalf("parallel savings = %d", analysis.Summary.ParallelSavingsMS)
	}
	codes := map[string]bool{}
	for _, diagnostic := range analysis.Diagnostics {
		codes[diagnostic.Code] = true
	}
	for _, code := range []string{traceDiagnosticRepeatedCall, traceDiagnosticRepeatedFailure} {
		if !codes[code] {
			t.Fatalf("missing diagnostic %s: %+v", code, analysis.Diagnostics)
		}
	}
	if codes[traceDiagnosticParallelCandidate] {
		t.Fatalf("fully overlapping tools were reported as a future parallel opportunity: %+v", analysis.Diagnostics)
	}
}

func TestTraceParallelDiagnosticsMeasureRemainingWallOpportunity(t *testing.T) {
	base := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		second  time.Duration
		wantMS  int64
		wantHit bool
	}{
		{name: "sequential", second: 50 * time.Millisecond, wantMS: 40, wantHit: true},
		{name: "partial overlap", second: 30 * time.Millisecond, wantMS: 20, wantHit: true},
		{name: "full overlap", second: 10 * time.Millisecond, wantMS: 0, wantHit: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := []traceSpan{
				{ID: "read", Type: "tool", ToolName: "Read", ConcurrencyClass: "read_only", TurnIndex: 1, Start: base.Add(10 * time.Millisecond), End: base.Add(50 * time.Millisecond), DurationMS: 40},
				{ID: "grep", Type: "tool", ToolName: "Grep", ConcurrencyClass: "read_only", TurnIndex: 1, Start: base.Add(tt.second), End: base.Add(tt.second + 40*time.Millisecond), DurationMS: 40},
			}
			diagnostics := traceParallelDiagnostics(spans)
			if len(diagnostics) != 0 {
				if !tt.wantHit || diagnostics[0].EstimatedSavingsMS != tt.wantMS {
					t.Fatalf("diagnostics = %+v, want hit=%v savings=%d", diagnostics, tt.wantHit, tt.wantMS)
				}
				return
			}
			if tt.wantHit {
				t.Fatalf("missing diagnostic with savings %d", tt.wantMS)
			}
		})
	}
}

func TestTraceQualityRequiresCompletedPassingTestResults(t *testing.T) {
	missing := traceQualitySignals([]traceEvent{{Type: "tool_call", ToolID: "test-1", ToolName: "Bash", Input: `{"command":"go test ./..."}`}}, traceSummary{})
	if !missing.TestsRun || missing.TestsPassed {
		t.Fatalf("missing test result quality = %+v", missing)
	}
	passed := traceQualitySignals([]traceEvent{
		{Type: "tool_call", ToolID: "test-1", ToolName: "Bash", Input: `{"command":"go test ./..."}`},
		{Type: "tool_result", ToolID: "test-1"},
	}, traceSummary{})
	if !passed.TestsRun || !passed.TestsPassed {
		t.Fatalf("passing test quality = %+v", passed)
	}
}

func TestTraceQualityUsesLatestCompletedTestAsFinalVerification(t *testing.T) {
	quality := traceQualitySignals([]traceEvent{
		{Type: "tool_call", ToolID: "test-1", ToolName: "Bash", Input: `{"command":"go test ./..."}`},
		{Type: "tool_result", ToolID: "test-1", IsError: true},
		{Type: "tool_call", ToolID: "test-2", ToolName: "Bash", Input: `{"command":"go test ./..."}`},
		{Type: "tool_result", ToolID: "test-2"},
		{Type: "evidence_record"},
	}, traceSummary{})
	if !quality.TestsPassed {
		t.Fatalf("final successful test should pass quality: %+v", quality)
	}
	if quality.TestAttempts != 2 || quality.FailedTestAttempts != 1 || !quality.TestFailureRecovered {
		t.Fatalf("test attempt history = %+v", quality)
	}
	if quality.FinalVerificationPassed == nil || !*quality.FinalVerificationPassed {
		t.Fatalf("final verification = %+v", quality.FinalVerificationPassed)
	}
}

func TestTraceQualityDoesNotHideFinalTestFailure(t *testing.T) {
	quality := traceQualitySignals([]traceEvent{
		{Type: "tool_call", ToolID: "test-1", ToolName: "Bash", Input: `{"command":"go test ./..."}`},
		{Type: "tool_result", ToolID: "test-1"},
		{Type: "tool_call", ToolID: "test-2", ToolName: "Bash", Input: `{"command":"go test ./..."}`},
		{Type: "tool_result", ToolID: "test-2", IsError: true},
		{Type: "evidence_record"},
	}, traceSummary{})
	if quality.TestsPassed || quality.TestAttempts != 2 || quality.FailedTestAttempts != 1 || quality.TestFailureRecovered {
		t.Fatalf("final failed test quality = %+v", quality)
	}
	if quality.FinalVerificationPassed == nil || *quality.FinalVerificationPassed {
		t.Fatalf("final verification = %+v", quality.FinalVerificationPassed)
	}
}

func TestTraceQualityJSONKeepsZeroAttemptAndRecoveryValues(t *testing.T) {
	data, err := json.Marshal(traceQuality{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"test_attempts":0`, `"failed_test_attempts":0`, `"test_failure_recovered":false`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("trace quality JSON %s does not contain %s", data, want)
		}
	}
}

func TestTraceQualityRecoveryUsesToolCallIdentity(t *testing.T) {
	quality := traceQualitySignals([]traceEvent{
		{Type: "tool_call", ToolID: "read-1", ToolName: "Read"},
		{Type: "tool_result", ToolID: "read-1", IsError: true},
		{Type: "tool_call", ToolID: "read-2", ToolName: "Read"},
		{Type: "tool_result", ToolID: "read-2"},
	}, traceSummary{})
	if quality.Recoveries != 1 {
		t.Fatalf("recoveries = %d", quality.Recoveries)
	}
}

func TestWriteRuntimeTraceArtifactIsPrivateAndMetadataOnly(t *testing.T) {
	base := time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)
	detail := traceDetailResponse{
		SessionID: "session-1",
		Summary:   traceSummary{TaskWallMS: 42},
		Spans: []traceSpan{{
			ID: "root", Type: "query", Name: "query.run", TraceID: "trace-1", Start: base, DurationMS: 42,
		}},
		Events: []traceEvent{{Input: "Authorization: Bearer secret", Output: "private output", Content: "prompt secret"}},
	}
	identity := buildinfo.Info{SchemaVersion: buildinfo.SchemaVersion, Product: buildinfo.Product, Version: "v-test", GoToolchain: "go-test"}
	artifact := runtimeTraceArtifactFromDetail(detail, identity)
	path := filepath.Join(t.TempDir(), "artifact.json")
	if err := writeRuntimeTraceArtifact(path, artifact); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{[]byte("Authorization"), []byte("private output"), []byte("prompt secret")} {
		if bytes.Contains(data, secret) {
			t.Fatalf("artifact leaked %q: %s", secret, data)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode = %o", info.Mode().Perm())
	}
	var decoded RuntimeTraceArtifact
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 || decoded.Run.Agent != runtimeTraceAgentName || decoded.Run.DurationMS != 42 {
		t.Fatalf("artifact = %+v", decoded)
	}
	if decoded.Run.AgentVersion != "v-test" || decoded.Run.BuildInfo.Version != decoded.Run.AgentVersion {
		t.Fatalf("trace identity drifted: %+v", decoded.Run)
	}
}

func TestRuntimeTraceArtifactRequiresSuccessfulQueryRootForCompletedStatus(t *testing.T) {
	base := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	unknown := runtimeTraceArtifactFromDetail(traceDetailResponse{SessionID: "legacy", Spans: []traceSpan{{ID: "tool", Type: "tool", Status: telemetry.StatusOK, Start: base, DurationMS: 5}}}, buildinfo.Info{})
	if unknown.Run.Status != RuntimeTraceRunStatusUnknown {
		t.Fatalf("legacy status = %q", unknown.Run.Status)
	}
	completed := runtimeTraceArtifactFromDetail(traceDetailResponse{SessionID: "native", Spans: []traceSpan{{ID: "transport", Type: "api", Status: telemetry.StatusOK, Start: base, DurationMS: 10}, {ID: "query", ParentID: "transport", Type: "query", Status: telemetry.StatusOK, Start: base.Add(time.Millisecond), DurationMS: 5}}}, buildinfo.Info{})
	if completed.Run.Status != RuntimeTraceRunStatusCompleted {
		t.Fatalf("native status = %q", completed.Run.Status)
	}
}

func TestRuntimeTraceArtifactSelectsOneLatestQueryRun(t *testing.T) {
	base := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	detail := traceDetailResponse{
		SessionID: "resumed-session",
		Spans: finalizeTraceSpans([]traceSpan{
			{ID: "query-old", Type: "query", Status: telemetry.StatusOK, TraceID: "trace-old", Start: base, DurationMS: 100},
			{ID: "tool-old", ParentID: "query-old", Type: "tool", Status: telemetry.StatusOK, TraceID: "trace-old", Start: base.Add(10 * time.Millisecond), DurationMS: 20},
			{ID: "api-new", Type: "api", Status: telemetry.StatusOK, TraceID: "trace-new", Start: base.Add(time.Second), DurationMS: 80},
			{ID: "query-new", ParentID: "api-new", Type: "query", Status: telemetry.StatusError, TraceID: "trace-new", Start: base.Add(1010 * time.Millisecond), DurationMS: 40},
			{ID: "tool-new", ParentID: "query-new", Type: "tool", Status: telemetry.StatusError, TraceID: "trace-new", Start: base.Add(1020 * time.Millisecond), DurationMS: 10},
		}),
		Events: []traceEvent{
			{Type: "message", Role: "user", TraceID: "trace-old", Time: base.Add(time.Millisecond)},
			{Type: "message", Role: "user", TraceID: "trace-new", Time: base.Add(1011 * time.Millisecond)},
			{Type: "tool_result", ToolID: "new", ToolName: "Read", IsError: true, TraceID: "trace-new", Time: base.Add(1030 * time.Millisecond)},
		},
	}

	artifact := runtimeTraceArtifactFromDetail(detail, buildinfo.Info{})
	if artifact.Run.RunID != "trace-new" || artifact.Run.Status != RuntimeTraceRunStatusFailed || !artifact.Run.StartedAt.Equal(base.Add(1010*time.Millisecond)) || artifact.Run.DurationMS != 40 {
		t.Fatalf("run = %+v", artifact.Run)
	}
	if len(artifact.Spans) != 2 || artifact.Spans[0].ID != "query-new" || artifact.Spans[0].ParentID != "" || artifact.Spans[1].ID != "tool-new" {
		t.Fatalf("artifact mixed query runs: %+v", artifact.Spans)
	}
	if artifact.Summary.Turns != 1 || artifact.Quality.ToolErrors != 1 {
		t.Fatalf("run analysis = summary=%+v quality=%+v", artifact.Summary, artifact.Quality)
	}
}

func TestTraceAPIExportAuthenticatesValidatesSchemaAndExportsLocalArtifact(t *testing.T) {
	transcripts := t.TempDir()
	t.Setenv("NIUMA_TRANSCRIPT_PROJECTS_DIR", transcripts)
	store := session.DefaultStore()
	recorder, err := store.NewRecorderWithID(t.TempDir(), "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	sink := session.NewRuntimeSpanSink(recorder)
	if err := sink.Emit(t.Context(), telemetry.Event{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Name:          telemetry.EventQueryRun + telemetry.SpanFinishedSuffix,
		Status:        telemetry.StatusOK,
		TraceID:       "trace-export",
		SpanID:        "query-export",
		StartedAt:     base,
		OccurredAt:    base.Add(25 * time.Millisecond),
		DurationMS:    25,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(t.Context(), telemetry.Event{
		SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
		Name:          telemetry.EventToolExecution + telemetry.SpanFinishedSuffix,
		Status:        telemetry.StatusOK,
		TraceID:       "trace-export",
		SpanID:        "tool-export",
		ParentSpanID:  "query-export",
		TurnIndex:     1,
		ToolName:      "Read",
		ResourceType:  "tool_call",
		ResourceID:    "tool-use-export",
		StartedAt:     base.Add(5 * time.Millisecond),
		OccurredAt:    base.Add(15 * time.Millisecond),
		DurationMS:    10,
		Properties:    map[string]any{"concurrency_class": "read_only"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	handler := traceAPIExportHandler(Options{AuthToken: "test-token"})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/trace/api/sessions/11111111-1111-4111-8111-111111111111/export?source=local", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/11111111-1111-4111-8111-111111111111/export?source=local&schema=runtime-trace-v2", nil)
	invalidRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid schema status = %d body=%s", invalid.Code, invalid.Body.String())
	}

	exported := httptest.NewRecorder()
	exportRequest := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/11111111-1111-4111-8111-111111111111/export?source=local&schema=runtime-trace-v1", nil)
	exportRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(exported, exportRequest)
	if exported.Code != http.StatusOK || exported.Header().Get("Content-Disposition") == "" {
		t.Fatalf("export status=%d disposition=%q body=%s", exported.Code, exported.Header().Get("Content-Disposition"), exported.Body.String())
	}
	var artifact RuntimeTraceArtifact
	if err := json.Unmarshal(exported.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 || artifact.Run.SessionID != recorder.SessionID || artifact.Run.DurationMS != 25 {
		t.Fatalf("artifact = %+v", artifact)
	}
	if artifact.Run.AgentVersion == "" || artifact.Run.BuildInfo.Version != artifact.Run.AgentVersion || artifact.Run.BuildInfo.Product != buildinfo.Product {
		t.Fatalf("HTTP trace export lost build identity: %+v", artifact.Run)
	}
	if len(artifact.Spans) != 2 || artifact.Spans[1].ConcurrencyClass != "read_only" {
		t.Fatalf("artifact lost persisted concurrency metadata: %+v", artifact.Spans)
	}
}

func TestTraceAPIExportValidatesSourceAndPreservesTenantErrors(t *testing.T) {
	handler := traceAPIExportHandler(Options{AuthToken: "test-token"})

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/session/export?source=tenent", nil)
	invalidRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid source status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	unavailable := httptest.NewRecorder()
	unavailableRequest := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/5/export?source=tenant", nil)
	unavailableRequest.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(unavailable, unavailableRequest)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("tenant unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}

	notFoundHandler := traceAPIExportHandler(Options{AuthToken: "test-token", TenantService: &fakeTenantService{}})
	notFound := httptest.NewRecorder()
	notFoundRequest := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/5/export?source=tenant", nil)
	notFoundRequest.Header.Set("Authorization", "Bearer test-token")
	notFoundHandler.ServeHTTP(notFound, notFoundRequest)
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("tenant not-found status=%d body=%s", notFound.Code, notFound.Body.String())
	}
}

func TestTraceAPIExportSelectsNestedTenantQueryRun(t *testing.T) {
	base := time.Date(2026, 8, 11, 2, 30, 0, 0, time.UTC)
	fake := &fakeTenantService{
		sessions:    []mysqlstore.Session{{ID: 5, SessionKey: "tenant-session", Title: "Tenant Trace", Status: "active", LastMessageAt: base.Add(time.Second)}},
		messageRows: []mysqlstore.Message{{ID: 1, SessionID: 5, TurnIndex: 1, Role: "user", Content: "private prompt", TraceID: "tenant-trace", CreatedAt: base.Add(10 * time.Millisecond)}},
		telemetryEvents: []mysqlstore.TelemetryEvent{{Event: telemetry.Event{
			ID:            1,
			SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1,
			Name:          telemetry.EventQueryRun + telemetry.SpanFinishedSuffix,
			Category:      telemetry.CategorySession,
			Status:        telemetry.StatusOK,
			TraceID:       "tenant-trace",
			SpanID:        "tenant-query",
			ParentSpanID:  "tenant-api",
			SessionID:     5,
			StartedAt:     base,
			OccurredAt:    base.Add(25 * time.Millisecond),
			DurationMS:    25,
		}}},
	}
	handler := traceAPIExportHandler(Options{AuthToken: "test-token", TenantService: fake})
	request := httptest.NewRequest(http.MethodGet, "/trace/api/sessions/5/export?source=tenant", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("tenant export status=%d body=%s", response.Code, response.Body.String())
	}
	var artifact RuntimeTraceArtifact
	if err := json.Unmarshal(response.Body.Bytes(), &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Run.RunID != "tenant-trace" || artifact.Run.Status != RuntimeTraceRunStatusCompleted || len(artifact.Spans) != 1 || artifact.Spans[0].ParentID != "" {
		t.Fatalf("tenant artifact = %+v", artifact)
	}
	if strings.Contains(response.Body.String(), "private prompt") {
		t.Fatalf("tenant artifact leaked message content: %s", response.Body.String())
	}
}

func TestTenantTraceSpansPreferExplicitParent(t *testing.T) {
	base := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	events := []traceEvent{
		{Name: "query.run.started", Type: "telemetry", Status: telemetry.StatusStarted, Time: base, StartedAt: base, TraceID: "trace-1", SpanID: "root", TurnIndex: 1},
		{Name: "model.request.started", Type: "telemetry", Status: telemetry.StatusStarted, Time: base.Add(10 * time.Millisecond), StartedAt: base.Add(10 * time.Millisecond), TraceID: "trace-1", SpanID: "narrow", ParentSpanID: "root", TurnIndex: 1},
		{Name: "tool.execution.started", Type: "telemetry", Status: telemetry.StatusStarted, Time: base.Add(20 * time.Millisecond), StartedAt: base.Add(20 * time.Millisecond), TraceID: "trace-1", SpanID: "child", ParentSpanID: "root", TurnIndex: 1},
		{Name: "tool.execution.finished", Type: "telemetry", Status: telemetry.StatusOK, Time: base.Add(30 * time.Millisecond), StartedAt: base.Add(20 * time.Millisecond), DurationMS: 10, TraceID: "trace-1", SpanID: "child", ParentSpanID: "root", TurnIndex: 1},
		{Name: "model.request.finished", Type: "telemetry", Status: telemetry.StatusOK, Time: base.Add(90 * time.Millisecond), StartedAt: base.Add(10 * time.Millisecond), DurationMS: 80, TraceID: "trace-1", SpanID: "narrow", ParentSpanID: "root", TurnIndex: 1},
		{Name: "query.run.finished", Type: "telemetry", Status: telemetry.StatusOK, Time: base.Add(100 * time.Millisecond), StartedAt: base, DurationMS: 100, TraceID: "trace-1", SpanID: "root", TurnIndex: 1},
	}

	spans := finalizeTraceSpans(tenantTraceSpans(events))
	byID := make(map[string]traceSpan, len(spans))
	for _, span := range spans {
		byID[span.ID] = span
	}
	if byID["child"].ParentID != "root" {
		t.Fatalf("explicit parent = %q, want root; spans=%+v", byID["child"].ParentID, spans)
	}
	if byID["child"].TurnIndex != 1 || !byID["child"].Start.Equal(base.Add(20*time.Millisecond)) {
		t.Fatalf("child span = %+v", byID["child"])
	}
	if byID["narrow"].ParentID != "root" {
		t.Fatalf("narrow parent = %q, want root", byID["narrow"].ParentID)
	}
}

func TestTenantTraceSpansPairConcurrentSameNameBySpanID(t *testing.T) {
	base := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	events := []traceEvent{
		{Name: "tool.execution.started", Type: "telemetry", Status: telemetry.StatusStarted, Time: base, TraceID: "trace-1", SpanID: "first", ToolName: "Read"},
		{Name: "tool.execution.started", Type: "telemetry", Status: telemetry.StatusStarted, Time: base.Add(5 * time.Millisecond), TraceID: "trace-1", SpanID: "second", ToolName: "Read"},
		{Name: "tool.execution.finished", Type: "telemetry", Status: telemetry.StatusOK, Time: base.Add(15 * time.Millisecond), DurationMS: 10, TraceID: "trace-1", SpanID: "second", ToolName: "Read"},
		{Name: "tool.execution.finished", Type: "telemetry", Status: telemetry.StatusOK, Time: base.Add(30 * time.Millisecond), DurationMS: 30, TraceID: "trace-1", SpanID: "first", ToolName: "Read"},
	}

	spans := tenantTraceSpans(events)
	if len(spans) != 2 {
		t.Fatalf("spans = %+v", spans)
	}
	byID := map[string]traceSpan{spans[0].ID: spans[0], spans[1].ID: spans[1]}
	if !byID["first"].Start.Equal(base) || byID["first"].DurationMS != 30 {
		t.Fatalf("first = %+v", byID["first"])
	}
	if !byID["second"].Start.Equal(base.Add(5*time.Millisecond)) || byID["second"].DurationMS != 10 {
		t.Fatalf("second = %+v", byID["second"])
	}
}

func TestLocalTraceSpansUseNativeRuntimeParentsAndDeduplicateLegacyTool(t *testing.T) {
	base := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	entries := []session.Entry{
		runtimeSpanEntry(t, telemetry.Event{Name: "query.run.finished", SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1, Status: telemetry.StatusOK, TraceID: "trace-1", SpanID: "query-1", StartedAt: base, OccurredAt: base.Add(100 * time.Millisecond), DurationMS: 100}),
		runtimeSpanEntry(t, telemetry.Event{Name: "model.request.finished", SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1, Status: telemetry.StatusOK, TraceID: "trace-1", SpanID: "model-1", ParentSpanID: "query-1", TurnIndex: 1, StartedAt: base.Add(10 * time.Millisecond), OccurredAt: base.Add(50 * time.Millisecond), DurationMS: 40, Model: "test"}),
		{Type: "tool_call", ToolID: "toolu-1", ToolName: "Echo", Content: `{"text":"secret"}`, Timestamp: base.Add(55 * time.Millisecond)},
		runtimeSpanEntry(t, telemetry.Event{Name: "tool.execution.finished", SchemaVersion: telemetry.SchemaVersionRuntimeTraceV1, Status: telemetry.StatusOK, TraceID: "trace-1", SpanID: "tool-1", ParentSpanID: "query-1", TurnIndex: 1, StartedAt: base.Add(55 * time.Millisecond), OccurredAt: base.Add(75 * time.Millisecond), DurationMS: 20, ToolName: "Echo", ResourceType: "tool_call", ResourceID: "toolu-1"}),
		{Type: "tool_result", ToolID: "toolu-1", ToolName: "Echo", Content: "secret", Timestamp: base.Add(75 * time.Millisecond)},
	}

	spans := finalizeTraceSpans(localTraceSpans(localTraceEvents(entries)))
	if len(spans) != 3 {
		t.Fatalf("spans = %+v", spans)
	}
	byID := make(map[string]traceSpan, len(spans))
	for _, span := range spans {
		byID[span.ID] = span
	}
	if byID["model-1"].ParentID != "query-1" || byID["tool-1"].ParentID != "query-1" {
		t.Fatalf("native parents were not preserved: %+v", spans)
	}
	if byID["tool-1"].TurnIndex != 1 || byID["tool-1"].DurationMS != 20 {
		t.Fatalf("native tool span = %+v", byID["tool-1"])
	}
	if _, duplicated := byID["tool:toolu-1"]; duplicated {
		t.Fatalf("legacy tool span duplicated native tool: %+v", spans)
	}
}

func runtimeSpanEntry(t *testing.T, event telemetry.Event) session.Entry {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return session.Entry{Type: session.EntryTypeRuntimeSpan, Content: string(data), Timestamp: event.OccurredAt}
}

func TestFinalizeTraceSpansRepairsMalformedExplicitParents(t *testing.T) {
	base := time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC)
	spans := finalizeTraceSpans([]traceSpan{
		{ID: "missing", ParentID: "does-not-exist", Type: "tool", Name: "missing", TraceID: "trace-1", Start: base, DurationMS: 1},
		{ID: "self", ParentID: "self", Type: "tool", Name: "self", TraceID: "trace-1", Start: base.Add(time.Millisecond), DurationMS: 1},
		{ID: "cycle-a", ParentID: "cycle-b", Type: "tool", Name: "cycle-a", TraceID: "trace-1", Start: base.Add(2 * time.Millisecond), DurationMS: 1},
		{ID: "cycle-b", ParentID: "cycle-a", Type: "tool", Name: "cycle-b", TraceID: "trace-1", Start: base.Add(3 * time.Millisecond), DurationMS: 1},
		{ID: "cross", ParentID: "other", Type: "tool", Name: "cross", TraceID: "trace-1", Start: base.Add(4 * time.Millisecond), DurationMS: 1},
		{ID: "other", Type: "query", Name: "other", TraceID: "trace-2", Start: base, DurationMS: 10},
		{ID: "duplicate", Type: "query", Name: "first duplicate", TraceID: "trace-1", Start: base, DurationMS: 10},
		{ID: "duplicate", ParentID: "duplicate", Type: "tool", Name: "second duplicate", TraceID: "trace-1", Start: base.Add(5 * time.Millisecond), DurationMS: 1},
	})
	ids := map[string]bool{}
	for _, span := range spans {
		if ids[span.ID] {
			t.Fatalf("duplicate span id survived normalization: %+v", spans)
		}
		ids[span.ID] = true
		if span.ParentID == span.ID {
			t.Fatalf("self parent survived normalization: %+v", span)
		}
	}
	tree := buildTraceSpanTree(spans)
	if got := countTraceTreeNodes(tree); got != len(spans) {
		t.Fatalf("tree contains %d nodes, want %d; spans=%+v tree=%+v", got, len(spans), spans, tree)
	}
}

func countTraceTreeNodes(nodes []traceTree) int {
	total := 0
	for _, node := range nodes {
		total += 1 + countTraceTreeNodes(node.Children)
	}
	return total
}

func TestBuildLocalTraceDetailAddsTraceIDAndCWD(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(t.TempDir(), "my repo")
	store := session.Store{Root: root}
	recorder, err := store.NewRecorderWithID(project, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Timestamp: time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC), Type: "message", Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Timestamp: time.Date(2026, 6, 24, 1, 2, 4, 0, time.UTC), Type: "message", Role: "assistant", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	detail, err := buildLocalTraceDetail([]session.Store{store}, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SessionID != recorder.SessionID || detail.Summary.Messages != 2 || detail.Summary.Turns != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	for _, event := range detail.Events {
		if event.TraceID != "local:"+recorder.SessionID {
			t.Fatalf("event trace id = %q", event.TraceID)
		}
	}
	summaries, err := localTraceSessionSummaries([]session.Store{store}, 10, traceTimeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 || summaries[0].CWD != session.ProjectSlug(project) {
		t.Fatalf("summaries = %+v", summaries)
	}
}

func TestBuildLocalTraceDetailIncludesRecaps(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(t.TempDir(), "recap repo")
	store := session.Store{Root: root}
	recorder, err := store.NewRecorderWithID(project, "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC)
	if err := recorder.Append(session.Entry{ID: "user-1", Timestamp: base, Type: "message", Role: "user", Content: "build recap visibility"}); err != nil {
		t.Fatal(err)
	}
	recapMeta, err := json.Marshal(map[string]any{"version": 1, "source": "away", "status": "ok", "duration_ms": 15, "summarizes_entry_id": "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "recap-1", Timestamp: base.Add(time.Second), Type: "recap_summary", Role: "system", Content: "继续处理 WebUI recap 可观测展示。", Model: "recap-model", Metadata: recapMeta}); err != nil {
		t.Fatal(err)
	}
	invalidatedMeta, err := json.Marshal(map[string]any{"version": 1, "source": "rewind", "status": "invalidated", "message_id": "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{ID: "recap-2", Timestamp: base.Add(2 * time.Second), Type: "recap_summary", Role: "system", Content: "Recap invalidated by rewind.", Metadata: invalidatedMeta}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	detail, err := buildLocalTraceDetail([]session.Store{store}, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Recaps) != 2 {
		t.Fatalf("recaps = %+v", detail.Recaps)
	}
	if detail.LatestRecap == nil || detail.LatestRecap.ID != "recap-1" || detail.LatestRecap.Source != "away" || detail.LatestRecap.Model != "recap-model" {
		t.Fatalf("latest recap = %+v", detail.LatestRecap)
	}
	if detail.LatestRecap.DurationMS != 15 || detail.LatestRecap.SummarizesEntryID != "user-1" {
		t.Fatalf("latest recap metadata = %+v", detail.LatestRecap)
	}
	if detail.Recaps[1].Status != "invalidated" || detail.Recaps[1].SummarizesEntryID != "user-1" {
		t.Fatalf("invalidated recap = %+v", detail.Recaps[1])
	}
}

func TestBuildLocalTraceDetailIncludesClosureEvents(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(t.TempDir(), "closure repo")
	store := session.Store{Root: root}
	recorder, err := store.NewRecorderWithID(project, "44444444-4444-4444-8444-444444444444")
	if err != nil {
		t.Fatal(err)
	}
	taskContract, err := json.Marshal(map[string]any{
		"ClosureLevel": "L2_read_only_scoped",
		"Reasons": []map[string]any{{
			"rule_id": "read_only_scoped",
			"detail":  "read/summarize/explain/compare intent with local path",
		}},
		"Confidence": "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := json.Marshal(map[string]any{
		"severity":         "block_continue",
		"rule_id":          "read_scope",
		"missing_evidence": []string{"required read evidence: README.md"},
		"reason":           "<system-reminder>Completion is blocked by Read Scope Gate.</system-reminder>",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 10, 1, 2, 3, 0, time.UTC)
	if err := recorder.Append(session.Entry{Timestamp: base, Type: "task_contract", Content: string(taskContract)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Append(session.Entry{Timestamp: base.Add(time.Second), Type: "completion_gate", Content: string(gate)}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	detail, err := buildLocalTraceDetail([]session.Store{store}, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Events) != 2 {
		t.Fatalf("events = %+v, want task_contract and completion_gate", detail.Events)
	}
	if detail.Events[0].Name != "closure.task_contract" || stringProperty(detail.Events[0].Properties, "Confidence") != "high" {
		t.Fatalf("task contract event not normalized: %+v", detail.Events[0])
	}
	gateEvent := detail.Events[1]
	if gateEvent.Name != "closure.completion_gate" || gateEvent.Status != "block_continue" || !gateEvent.IsError {
		t.Fatalf("completion gate event not normalized: %+v", gateEvent)
	}
	if stringProperty(gateEvent.Properties, "rule_id") != "read_scope" {
		t.Fatalf("completion gate rule_id missing: %+v", gateEvent.Properties)
	}
	missing, ok := gateEvent.Properties["missing_evidence"].([]any)
	if !ok || len(missing) != 1 || missing[0] != "required read evidence: README.md" {
		t.Fatalf("completion gate missing_evidence = %#v", gateEvent.Properties["missing_evidence"])
	}
}

func TestNativeClaudeTraceEventsIncludeTraceIDAfterDetailBuild(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(t.TempDir(), "native")
	store := session.Store{Root: root}
	recorder, err := store.NewRecorderWithID(project, "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	line := `{"uuid":"abc","type":"assistant","timestamp":"2026-06-24T01:02:03Z","message":{"role":"assistant","content":[{"type":"text","text":"looking"},{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"README.md"}}],"model":"model-a","usage":{"input_tokens":3,"output_tokens":4}}}`
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recorder.Path, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	detail, err := buildLocalTraceDetail([]session.Store{store}, recorder.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Messages != 1 || detail.Summary.ToolCalls != 1 || detail.Summary.TotalTokens != 7 {
		t.Fatalf("summary = %+v", detail.Summary)
	}
	for _, event := range detail.Events {
		if event.TraceID != "local:"+recorder.SessionID {
			t.Fatalf("event = %+v", event)
		}
	}
}
