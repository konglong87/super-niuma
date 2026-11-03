package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

const (
	defaultTenantTimelineLimit      = 200
	defaultTenantTimelineTraceLimit = 100
	defaultTenantTimelineTaskLimit  = 500
)

type tenantSessionTimelineResponse struct {
	Session         mysqlstore.Session          `json:"session"`
	TraceIDs        []string                    `json:"trace_ids"`
	Messages        []mysqlstore.Message        `json:"messages"`
	AuditLogs       []mysqlstore.AuditLog       `json:"audit_logs"`
	TelemetryEvents []mysqlstore.TelemetryEvent `json:"telemetry_events"`
	AgentTasks      []mysqlstore.AgentTask      `json:"agent_tasks"`
	AgentTaskEvents []mysqlstore.AgentTaskEvent `json:"agent_task_events"`
	Timeline        []tenantTimelineItem        `json:"timeline"`
}

type tenantTimelineItem struct {
	Type           string                     `json:"type"`
	Time           time.Time                  `json:"time"`
	TraceID        string                     `json:"trace_id,omitempty"`
	Message        *mysqlstore.Message        `json:"message,omitempty"`
	AuditLog       *mysqlstore.AuditLog       `json:"audit_log,omitempty"`
	TelemetryEvent *mysqlstore.TelemetryEvent `json:"telemetry_event,omitempty"`
	AgentTask      *mysqlstore.AgentTask      `json:"agent_task,omitempty"`
	AgentTaskEvent *mysqlstore.AgentTaskEvent `json:"agent_task_event,omitempty"`
}

func tenantSessionTimelineHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_session_timeline", "server.tenantSessionTimelineHandler", "handle tenant session timeline")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if opts.TenantService == nil {
			writeTenantError(w, http.StatusServiceUnavailable, "tenant storage is not configured")
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !requireTenantRole(w, r, opts, "owner", "admin") {
			return
		}
		sessionID, ok := tenantSessionTimelineIDFromPath(r.URL.Path)
		if !ok || sessionID == 0 {
			writeTenantError(w, http.StatusBadRequest, "session id is required")
			return
		}
		resp, err := buildTenantSessionTimeline(r.Context(), opts.TenantService, sessionID, tenantTimelineOptionsFromRequest(r))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, resp)
	}
}

type tenantTimelineOptions struct {
	Limit      int
	TraceLimit int
	TaskLimit  int
}

func tenantTimelineOptionsFromRequest(r *http.Request) tenantTimelineOptions {
	query := r.URL.Query()
	limit := parseLimit(query.Get("limit"))
	if limit <= 0 {
		limit = defaultTenantTimelineLimit
	}
	traceLimit := parseLimit(query.Get("trace_limit"))
	if traceLimit <= 0 {
		traceLimit = defaultTenantTimelineTraceLimit
	}
	taskLimit := parseLimit(query.Get("task_limit"))
	if taskLimit <= 0 {
		taskLimit = defaultTenantTimelineTaskLimit
	}
	return tenantTimelineOptions{Limit: limit, TraceLimit: traceLimit, TaskLimit: taskLimit}
}

func buildTenantSessionTimeline(ctx context.Context, svc TenantService, sessionID uint64, opts tenantTimelineOptions) (tenantSessionTimelineResponse, error) {
	session, err := svc.GetSession(ctx, sessionID)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}
	messages, err := svc.ListMessages(ctx, sessionID, opts.Limit)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}

	traceSet := map[string]struct{}{}
	for _, message := range messages {
		addTraceID(traceSet, message.TraceID)
	}
	addTraceID(traceSet, observability.TraceID(ctx))

	// Sub-agent work uses parent_session_id plus trace_id as the durable join keys for a session chain.
	taskRows, err := svc.ListAgentTasks(ctx, opts.TaskLimit)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}
	tasks := make([]mysqlstore.AgentTask, 0, len(taskRows))
	for _, task := range taskRows {
		if task.ParentSessionID == sessionID || hasTraceID(traceSet, task.TraceID) {
			tasks = append(tasks, task)
			addTraceID(traceSet, task.TraceID)
		}
	}

	taskIDs := make([]uint64, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	// 一次 IN 查询覆盖全部任务，取代过去每个任务一条查询的 N+1(AUDIT-P1-26)。
	taskEvents, err := svc.ListAgentTaskEventsForTasks(ctx, taskIDs, opts.Limit)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}
	for _, event := range taskEvents {
		addTraceID(traceSet, event.TraceID)
	}

	traceIDs := sortedTraceIDs(traceSet)
	auditLogs, err := tenantTimelineAuditLogs(ctx, svc, sessionID, traceIDs, opts.TraceLimit)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}
	telemetryEvents, err := tenantTimelineTelemetryEvents(ctx, svc, sessionID, traceIDs, opts.TraceLimit)
	if err != nil {
		return tenantSessionTimelineResponse{}, err
	}

	timeline := tenantTimelineItems(messages, auditLogs, telemetryEvents, tasks, taskEvents)
	sort.SliceStable(timeline, func(i, j int) bool {
		return tenantTimelineLess(timeline[i], timeline[j])
	})

	return tenantSessionTimelineResponse{
		Session:         session,
		TraceIDs:        traceIDs,
		Messages:        messages,
		AuditLogs:       auditLogs,
		TelemetryEvents: telemetryEvents,
		AgentTasks:      tasks,
		AgentTaskEvents: taskEvents,
		Timeline:        timeline,
	}, nil
}

func tenantTimelineAuditLogs(ctx context.Context, svc TenantService, sessionID uint64, traceIDs []string, limit int) ([]mysqlstore.AuditLog, error) {
	byID := map[uint64]mysqlstore.AuditLog{}
	// 一次 IN 查询覆盖全部 trace，取代过去每个 trace 一条 `%x%` 全表扫(AUDIT-P1-26)。
	if qualifier := mysqlstore.SearchQualifier("trace_id", traceIDs...); qualifier != "" {
		page, err := svc.ListAuditLogsPage(ctx, tenantservice.ListOptions{Limit: traceScopedLimit(limit, len(traceIDs)), Search: qualifier})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			byID[item.ID] = item
		}
	}

	// Session lifecycle audits may not carry the same trace_id as model/tool execution.
	// 两个限定符要么一起成立要么整条查询不发 —— 只留 resource_type 会捞回全部会话审计。
	if qualifier := mysqlstore.SearchQualifier("resource_id", strconv.FormatUint(sessionID, 10)); qualifier != "" {
		page, err := svc.ListAuditLogsPage(ctx, tenantservice.ListOptions{Limit: limit, Search: "resource_type:session " + qualifier})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			byID[item.ID] = item
		}
	}

	items := make([]mysqlstore.AuditLog, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return itemTime(items[i].CreatedAt, items[i].ID).Before(itemTime(items[j].CreatedAt, items[j].ID))
	})
	return items, nil
}

func tenantTimelineTelemetryEvents(ctx context.Context, svc TenantService, sessionID uint64, traceIDs []string, limit int) ([]mysqlstore.TelemetryEvent, error) {
	byID := map[uint64]mysqlstore.TelemetryEvent{}
	if qualifier := mysqlstore.SearchQualifier("trace_id", traceIDs...); qualifier != "" {
		page, err := svc.ListTelemetryEventsPage(ctx, tenantservice.ListOptions{Limit: traceScopedLimit(limit, len(traceIDs)), Search: qualifier})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			if item.ID != 0 {
				byID[item.ID] = item
			}
		}
	}
	if qualifier := mysqlstore.SearchQualifier("session_id", strconv.FormatUint(sessionID, 10)); qualifier != "" {
		page, err := svc.ListTelemetryEventsPage(ctx, tenantservice.ListOptions{Limit: limit, Search: qualifier})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			if item.ID != 0 {
				byID[item.ID] = item
			}
		}
	}
	items := make([]mysqlstore.TelemetryEvent, 0, len(byID))
	for _, item := range byID {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return tenantTelemetryTime(items[i]).Before(tenantTelemetryTime(items[j]))
	})
	return items, nil
}

func tenantTimelineItems(messages []mysqlstore.Message, auditLogs []mysqlstore.AuditLog, telemetryEvents []mysqlstore.TelemetryEvent, tasks []mysqlstore.AgentTask, taskEvents []mysqlstore.AgentTaskEvent) []tenantTimelineItem {
	items := make([]tenantTimelineItem, 0, len(messages)+len(auditLogs)+len(telemetryEvents)+len(tasks)+len(taskEvents))
	for i := range messages {
		message := messages[i]
		items = append(items, tenantTimelineItem{Type: "message", Time: message.CreatedAt, TraceID: message.TraceID, Message: &message})
	}
	for i := range auditLogs {
		audit := auditLogs[i]
		items = append(items, tenantTimelineItem{Type: "audit", Time: audit.CreatedAt, TraceID: audit.TraceID, AuditLog: &audit})
	}
	for i := range telemetryEvents {
		event := telemetryEvents[i]
		items = append(items, tenantTimelineItem{Type: "telemetry", Time: tenantTelemetryTime(event), TraceID: event.TraceID, TelemetryEvent: &event})
	}
	for i := range tasks {
		task := tasks[i]
		items = append(items, tenantTimelineItem{Type: "agent_task", Time: task.StartedAt, TraceID: task.TraceID, AgentTask: &task})
	}
	for i := range taskEvents {
		event := taskEvents[i]
		items = append(items, tenantTimelineItem{Type: "agent_task_event", Time: event.CreatedAt, TraceID: event.TraceID, AgentTaskEvent: &event})
	}
	return items
}

func tenantSessionTimelineIDFromPath(path string) (uint64, bool) {
	raw := strings.TrimPrefix(path, "/tenant/sessions/")
	if raw == "" || raw == path {
		return 0, false
	}
	if !strings.HasSuffix(raw, "/timeline") {
		return 0, false
	}
	raw = strings.TrimSuffix(raw, "/timeline")
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	return id, err == nil
}

// traceScopedLimit 让「一条 IN 查询覆盖 N 个 trace」的额度和过去「每个 trace 一条
// 查询」相当。仓储层本来就把 limit 收在 500，这里同步收口，免得 ?trace_limit= 传个
// 大数再乘出溢出。
func traceScopedLimit(limit, traces int) int {
	const maxLimit = 500
	if limit >= maxLimit || traces <= 1 {
		return limit
	}
	if scaled := limit * traces; scaled < maxLimit {
		return scaled
	}
	return maxLimit
}

func addTraceID(traceSet map[string]struct{}, traceID string) {
	traceID = strings.TrimSpace(traceID)
	if traceID != "" {
		traceSet[traceID] = struct{}{}
	}
}

func hasTraceID(traceSet map[string]struct{}, traceID string) bool {
	_, ok := traceSet[strings.TrimSpace(traceID)]
	return ok
}

func sortedTraceIDs(traceSet map[string]struct{}) []string {
	traceIDs := make([]string, 0, len(traceSet))
	for traceID := range traceSet {
		traceIDs = append(traceIDs, traceID)
	}
	sort.Strings(traceIDs)
	return traceIDs
}

func tenantTelemetryTime(event mysqlstore.TelemetryEvent) time.Time {
	if !event.OccurredAt.IsZero() {
		return event.OccurredAt
	}
	return event.CreatedAt
}

func tenantTimelineLess(left, right tenantTimelineItem) bool {
	leftTime := left.Time
	rightTime := right.Time
	if leftTime.IsZero() && !rightTime.IsZero() {
		return false
	}
	if rightTime.IsZero() && !leftTime.IsZero() {
		return true
	}
	if !leftTime.Equal(rightTime) {
		return leftTime.Before(rightTime)
	}
	return left.Type < right.Type
}

func itemTime(t time.Time, id uint64) time.Time {
	if !t.IsZero() {
		return t
	}
	return time.Unix(0, int64(id))
}
