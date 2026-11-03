package mysql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

func TestDriverDSNAddsUTCTimeDefaults(t *testing.T) {
	dsn := DriverDSN("mysql://root@tcp(127.0.0.1:3306)/claude?multiStatements=true")
	for _, want := range []string{"multiStatements=true", "parseTime=true", "loc=UTC", "time_zone=%27%2B00%3A00%27"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("DriverDSN missing %q in %q", want, dsn)
		}
	}
	if strings.Contains(dsn, "mysql://") {
		t.Fatalf("DriverDSN did not strip scheme: %q", dsn)
	}

	custom := DriverDSN("user@tcp(localhost:3306)/claude?parseTime=false&loc=Local&time_zone=SYSTEM")
	if strings.Contains(custom, "parseTime=true") || strings.Contains(custom, "loc=UTC") || strings.Contains(custom, "time_zone=%27%2B00%3A00%27") {
		t.Fatalf("DriverDSN overwrote explicit time params: %q", custom)
	}
}

func TestScanUserNormalizesNullRole(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("SELECT user").WillReturnRows(sqlmock.NewRows([]string{
		"id", "tenant_id", "user_key", "email", "display_name", "role", "status", "user_info_json", "metadata_json", "updated_at",
	}).AddRow(7, 3, "operator", nil, nil, nil, "active", nil, nil, nil))

	rows, err := db.QueryContext(testContext(), "SELECT user")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing user row")
	}
	user, err := scanUser(rows)
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != DefaultUserRole {
		t.Fatalf("role = %q, want %q", user.Role, DefaultUserRole)
	}
	if user.Status != "active" {
		t.Fatalf("status = %q", user.Status)
	}
	if !user.UpdatedAt.IsZero() {
		t.Fatalf("null updated_at = %v", user.UpdatedAt)
	}
	assertExpectations(t, mock)
}

func TestScanUsageLedgerRowsAllowsNullFinishedAt(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT ledger").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "request_id", "tenant_id", "user_id", "session_id", "trace_id", "source", "route", "model", "status", "estimated",
			"reserved_input_tokens", "reserved_output_tokens", "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "total_tokens",
			"error_code", "error_message", "started_at", "finished_at", "created_at", "updated_at",
		}).
			AddRow(1, "req-null", 2, 3, 4, "trace-null", "openai", "/v1/chat/completions", "gpt", "started", false, 0, 0, 10, 2, 0, 0, 12, "", "", now, nil, now, now).
			AddRow(2, "req-finished", 2, 3, 4, "trace-finished", "openai", "/v1/chat/completions", "gpt", "ok", false, 0, 0, 11, 3, 0, 0, 14, "", "", now, now.Add(time.Second), now, now))
	rows, err := db.QueryContext(testContext(), "SELECT ledger")
	if err != nil {
		t.Fatal(err)
	}
	items, err := scanUsageLedgerRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if !items[0].FinishedAt.IsZero() {
		t.Fatalf("null finished_at = %v", items[0].FinishedAt)
	}
	if items[1].FinishedAt.IsZero() {
		t.Fatalf("finished_at was not scanned: %+v", items[1])
	}
	assertExpectations(t, mock)
}

func TestScanTelemetryEventRestoresTraceContextFromProperties(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	startedAt := time.Date(2026, 8, 10, 1, 2, 3, 4, time.UTC)
	properties := `{"schema_version":"runtime-trace-v1","span_id":"span-1","parent_span_id":"parent-1","turn_index":4,"started_at":"` + startedAt.Format(time.RFC3339Nano) + `","legacy_input_tokens":7}`
	mock.ExpectQuery("SELECT telemetry").WillReturnRows(sqlmock.NewRows([]string{
		"id", "tenant_id", "user_id", "event_name", "category", "source", "status", "trace_id", "session_id", "resource_type", "resource_id", "model", "tool_name", "duration_ms", "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "cache_creation_ephemeral_1h_input_tokens", "cache_creation_ephemeral_5m_input_tokens", "error_message", "properties_json", "occurred_at", "created_at",
	}).AddRow(1, 2, 3, "model.request.finished", "model", "test", "ok", "trace-1", 5, "", "", "model-a", "", 50, 10, 2, 0, 0, 0, 0, "", properties, startedAt.Add(50*time.Millisecond), startedAt.Add(time.Second)))
	rows, err := db.QueryContext(testContext(), "SELECT telemetry")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing telemetry row")
	}
	event, err := scanTelemetryEvent(rows)
	if err != nil {
		t.Fatal(err)
	}
	if event.SchemaVersion != telemetry.SchemaVersionRuntimeTraceV1 || event.SpanID != "span-1" || event.ParentSpanID != "parent-1" || event.TurnIndex != 4 || !event.StartedAt.Equal(startedAt) {
		t.Fatalf("event = %+v", event)
	}
	if event.Properties["legacy_input_tokens"] != float64(7) {
		t.Fatalf("legacy properties were altered: %+v", event.Properties)
	}
	assertExpectations(t, mock)
}

func goalRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "goal_key", "objective", "status", "session_id", "cwd", "model", "turn_budget", "token_budget", "turns_used", "input_tokens", "output_tokens", "last_blocker", "repeated_blocker_count", "last_checkpoint", "last_reason", "last_next_action", "error_message", "created_at", "updated_at"})
}

func goalEventRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"event_key", "event_type", "message", "session_id", "turn_index", "input_tokens", "output_tokens", "duration_ms", "checkpoint", "status", "reason", "next_action", "blocker_key", "error_message", "created_at"})
}

func goalEvidenceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"evidence_key", "evidence_type", "summary", "command", "exit_code", "passed", "payload_json", "created_at"})
}

func testContext() context.Context {
	return observability.WithRequestValues(context.Background(), "trace-1", "user-1", "yutang")
}

func assertExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
