package mysql

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// recordingMatcher 接受任意查询，顺手把实际发出的 SQL 记下来 —— 这些断言关心的
// 正是生成的谓词长什么样。
type recordingMatcher struct {
	queries *[]string
}

func (m recordingMatcher) Match(expectedSQL, actualSQL string) error {
	*m.queries = append(*m.queries, actualSQL)
	if expectedSQL == ".*" || regexp.MustCompile(expectedSQL).MatchString(actualSQL) {
		return nil
	}
	return errors.New("sql does not match")
}

func recordQuery(t *testing.T, columns []string, run func(repo *GormRepository) error) string {
	t.Helper()
	var queries []string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(recordingMatcher{queries: &queries}))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gormDB, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows(columns))
	if err := run(NewGormRepository(gormDB, nil)); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("query: %v", err)
	}
	if len(queries) == 0 {
		t.Fatal("no SQL was captured")
	}
	return queries[0]
}

var (
	auditColumns     = []string{"id", "tenant_id", "actor_user_id", "action", "resource_type", "resource_id", "metadata_json", "trace_id", "created_at"}
	telemetryColumns = []string{"id"}
)

func recordAuditQuery(t *testing.T, search string) string {
	t.Helper()
	return recordQuery(t, auditColumns, func(repo *GormRepository) error {
		_, err := repo.ListAuditLogsFiltered(testContext(), 1, ListOptions{Limit: 10, Search: search})
		return err
	})
}

func recordTelemetryQuery(t *testing.T, search string) string {
	t.Helper()
	return recordQuery(t, telemetryColumns, func(repo *GormRepository) error {
		_, err := repo.ListTelemetryEventsFiltered(testContext(), 1, ListOptions{Limit: 10, Search: search})
		return err
	})
}

// 精确 trace_id 必须走等值，落到 idx_tenant_audit_trace 上，而不是退化成
// 前导通配的全表扫(AUDIT-P1-26)。
func TestAuditSearchQualifierUsesEquality(t *testing.T) {
	got := recordAuditQuery(t, "trace_id:abc123")
	if !strings.Contains(got, "trace_id = ?") {
		t.Fatalf("sql = %s, want an equality predicate on trace_id", got)
	}
	if strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want no LIKE at all", got)
	}
}

// 多个 trace 合成一条 IN，取代过去每个 trace 一条查询的 N+1。
func TestAuditSearchQualifierCollapsesTraceListIntoIn(t *testing.T) {
	got := recordAuditQuery(t, "trace_id:a,b,c")
	if !strings.Contains(got, "trace_id IN (?,?,?)") {
		t.Fatalf("sql = %s, want a single IN predicate", got)
	}
}

func TestAuditSearchQualifiersCombineWithAnd(t *testing.T) {
	got := recordAuditQuery(t, "resource_type:session resource_id:42")
	if !strings.Contains(got, "resource_type = ?") || !strings.Contains(got, "resource_id = ?") {
		t.Fatalf("sql = %s, want both qualifiers applied", got)
	}
	if strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want no LIKE at all", got)
	}
}

// 自由文本仍旧走原来的 LIKE，老用法不能被这次改动打断。
func TestAuditFreeTextStillUsesLike(t *testing.T) {
	got := recordAuditQuery(t, "archive")
	if !strings.Contains(got, "action LIKE ?") {
		t.Fatalf("sql = %s, want the fuzzy fallback", got)
	}
}

func TestTelemetrySessionQualifierUsesEquality(t *testing.T) {
	got := recordTelemetryQuery(t, "session_id:42")
	if !strings.Contains(got, "session_id = ?") {
		t.Fatalf("sql = %s, want an equality predicate on session_id", got)
	}
	if strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want no LIKE at all", got)
	}
}

func TestTelemetryTraceQualifierUsesEquality(t *testing.T) {
	got := recordTelemetryQuery(t, "trace_id:abc123")
	if !strings.Contains(got, "trace_id = ?") {
		t.Fatalf("sql = %s, want an equality predicate on trace_id", got)
	}
	if strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want no LIKE at all", got)
	}
}

func TestTelemetryFreeTextStillUsesLike(t *testing.T) {
	got := recordTelemetryQuery(t, "request")
	if !strings.Contains(got, "event_name LIKE ?") {
		t.Fatalf("sql = %s, want the fuzzy fallback", got)
	}
}

// 写错的数值限定符不该静默返回空结果，退回自由文本反而更容易发现。
func TestNumericQualifierFallsBackToFreeTextWhenUnparseable(t *testing.T) {
	got := recordTelemetryQuery(t, "session_id:abc")
	if strings.Contains(got, "session_id = ?") {
		t.Fatalf("sql = %s, want the token treated as free text", got)
	}
	if !strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want the fuzzy fallback", got)
	}
}

func TestUnknownQualifierIsTreatedAsFreeText(t *testing.T) {
	got := recordAuditQuery(t, "http://example.test/x")
	if !strings.Contains(got, "LIKE") {
		t.Fatalf("sql = %s, want the fuzzy fallback", got)
	}
}

func TestSearchQualifierRejectsUnsafeValues(t *testing.T) {
	if got := SearchQualifier("trace_id", "a b", "c,d", "e:f", "  "); got != "" {
		t.Fatalf("SearchQualifier = %q, want empty", got)
	}
	if got := SearchQualifier("trace_id", "ok", "a b"); got != "trace_id:ok" {
		t.Fatalf("SearchQualifier = %q", got)
	}
	if got := SearchQualifier("trace_id"); got != "" {
		t.Fatalf("SearchQualifier = %q, want empty", got)
	}
}
