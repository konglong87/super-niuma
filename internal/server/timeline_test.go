package server

import (
	"context"
	"strings"
	"testing"
	"time"

	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func timelineFixture() *fakeTenantService {
	now := time.Now()
	fake := &fakeTenantService{}
	fake.sessions = []mysqlstore.Session{{ID: 5, SessionKey: "session-1", Status: "active"}}
	fake.messageRows = []mysqlstore.Message{
		{ID: 90, SessionID: 5, Role: "user", Content: "hi", TraceID: "trace-a", CreatedAt: now},
		{ID: 91, SessionID: 5, Role: "assistant", Content: "hello", TraceID: "trace-b", CreatedAt: now},
		{ID: 92, SessionID: 5, Role: "assistant", Content: "again", TraceID: "trace-c", CreatedAt: now},
	}
	fake.agentTasks = []mysqlstore.AgentTask{
		{ID: 41, ParentSessionID: 5, AgentName: "reviewer", Status: "completed", TraceID: "trace-a", StartedAt: now},
		{ID: 43, ParentSessionID: 5, AgentName: "tester", Status: "completed", TraceID: "trace-c", StartedAt: now},
	}
	fake.agentTaskEvents = []mysqlstore.AgentTaskEvent{
		{ID: 12, TaskID: 41, EventType: "started", TraceID: "trace-a", CreatedAt: now},
		{ID: 14, TaskID: 43, EventType: "started", TraceID: "trace-c", CreatedAt: now},
	}
	return fake
}

// 过去每个 trace 一条 `%x%` 全表扫，每个任务一条事件查询。三个 trace 加两个任务
// 就是 3+3+2 = 8 条；现在收敛成固定的 2+2+1 = 5 条，且都走等值/IN(AUDIT-P1-26)。
func TestTenantSessionTimelineCollapsesPerTraceQueries(t *testing.T) {
	fake := timelineFixture()
	opts := tenantTimelineOptions{Limit: 10, TraceLimit: 10, TaskLimit: 10}
	if _, err := buildTenantSessionTimeline(context.Background(), fake, 5, opts); err != nil {
		t.Fatalf("buildTenantSessionTimeline: %v", err)
	}

	if len(fake.auditPageOpts) != 2 {
		t.Fatalf("audit queries = %d (%v), want 2 regardless of trace count", len(fake.auditPageOpts), fake.auditPageOpts)
	}
	if len(fake.telemetryPageOpts) != 2 {
		t.Fatalf("telemetry queries = %d (%v), want 2 regardless of trace count", len(fake.telemetryPageOpts), fake.telemetryPageOpts)
	}
	if fake.agentTaskBatchCallCount() != 1 {
		t.Fatalf("agent task event queries = %d, want 1 regardless of task count", fake.agentTaskBatchCallCount())
	}
}

// 手里攥着精确的 trace_id / session_id 就不该退化成前导通配 LIKE。
func TestTenantSessionTimelineSendsExactKeyQualifiers(t *testing.T) {
	fake := timelineFixture()
	opts := tenantTimelineOptions{Limit: 10, TraceLimit: 10, TaskLimit: 10}
	if _, err := buildTenantSessionTimeline(context.Background(), fake, 5, opts); err != nil {
		t.Fatalf("buildTenantSessionTimeline: %v", err)
	}

	traceSearch := fake.auditPageOpts[0].Search
	for _, traceID := range []string{"trace-a", "trace-b", "trace-c"} {
		if !strings.Contains(traceSearch, traceID) {
			t.Fatalf("audit trace search = %q, want it to cover %s", traceSearch, traceID)
		}
	}
	if !strings.HasPrefix(traceSearch, "trace_id:") {
		t.Fatalf("audit trace search = %q, want a trace_id qualifier", traceSearch)
	}

	if got := fake.auditPageOpts[1].Search; got != "resource_type:session resource_id:5" {
		t.Fatalf("audit session search = %q", got)
	}
	if got := fake.telemetryPageOpts[1].Search; got != "session_id:5" {
		t.Fatalf("telemetry session search = %q", got)
	}
}

func TestTraceScopedLimitStaysBounded(t *testing.T) {
	if got := traceScopedLimit(100, 3); got != 300 {
		t.Fatalf("traceScopedLimit(100, 3) = %d, want the per-trace budget preserved", got)
	}
	if got := traceScopedLimit(100, 50); got != 500 {
		t.Fatalf("traceScopedLimit(100, 50) = %d, want it capped at the repository ceiling", got)
	}
	if got := traceScopedLimit(1<<40, 4); got != 1<<40 {
		t.Fatalf("traceScopedLimit(huge, 4) = %d, want no overflow multiplication", got)
	}
}
