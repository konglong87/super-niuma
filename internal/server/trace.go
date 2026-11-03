package server

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/buildinfo"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	defaultTraceSessionLimit     = 100
	defaultTraceFilterScanLimit  = 1000
	traceTimeQueryLayoutFallback = "2006-01-02T15:04"
)

type traceTimeFilter struct {
	From time.Time
	To   time.Time
}

type traceSessionSummary struct {
	Source      string    `json:"source"`
	SessionID   string    `json:"session_id"`
	Title       string    `json:"title,omitempty"`
	Path        string    `json:"path,omitempty"`
	Status      string    `json:"status,omitempty"`
	Model       string    `json:"model,omitempty"`
	CWD         string    `json:"cwd,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	MessageHint string    `json:"message_hint,omitempty"`
}

type traceDetailResponse struct {
	Source      string            `json:"source"`
	SessionID   string            `json:"session_id"`
	Title       string            `json:"title,omitempty"`
	TraceIDs    []string          `json:"trace_ids,omitempty"`
	Summary     traceSummary      `json:"summary"`
	Quality     traceQuality      `json:"quality,omitempty"`
	Diagnostics []traceDiagnostic `json:"diagnostics,omitempty"`
	LatestRecap *traceRecap       `json:"latest_recap,omitempty"`
	Recaps      []traceRecap      `json:"recaps,omitempty"`
	Spans       []traceSpan       `json:"spans"`
	SpanTree    []traceTree       `json:"span_tree,omitempty"`
	Events      []traceEvent      `json:"events"`
	Rewind      traceRewind       `json:"rewind,omitempty"`
	Raw         any               `json:"raw,omitempty"`
	Generated   time.Time         `json:"generated_at"`
}

type traceSummary struct {
	DurationMS           int64    `json:"duration_ms,omitempty"`
	TaskWallMS           int64    `json:"task_wall_ms,omitempty"`
	ModelWallMS          int64    `json:"model_wall_ms,omitempty"`
	ToolWorkMS           int64    `json:"tool_work_ms,omitempty"`
	ToolWallMS           int64    `json:"tool_wall_ms,omitempty"`
	CriticalPathMS       int64    `json:"critical_path_ms,omitempty"`
	UnattributedMS       int64    `json:"unattributed_ms,omitempty"`
	ParallelSavingsMS    int64    `json:"parallel_savings_ms,omitempty"`
	Turns                int      `json:"turns,omitempty"`
	Messages             int      `json:"messages,omitempty"`
	ToolCalls            int      `json:"tool_calls,omitempty"`
	ToolErrors           int      `json:"tool_errors,omitempty"`
	PermissionDecisions  int      `json:"permission_decisions,omitempty"`
	FilesChanged         int      `json:"files_changed,omitempty"`
	AgentTasks           int      `json:"agent_tasks,omitempty"`
	InputTokens          int      `json:"input_tokens,omitempty"`
	OutputTokens         int      `json:"output_tokens,omitempty"`
	TotalTokens          int      `json:"total_tokens,omitempty"`
	CacheReadTokens      int      `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens  int      `json:"cache_creation_tokens,omitempty"`
	CacheHitRatio        float64  `json:"cache_hit_ratio,omitempty"`
	TokenSummary         string   `json:"token_summary,omitempty"`
	CacheSummary         string   `json:"cache_summary,omitempty"`
	TotalModelDurationMS int64    `json:"total_model_duration_ms,omitempty"`
	TotalToolDurationMS  int64    `json:"total_tool_duration_ms,omitempty"`
	Skills               []string `json:"skills,omitempty"`
	Errors               int      `json:"errors,omitempty"`
}

type traceQuality struct {
	TestsRun                bool  `json:"tests_run"`
	TestsPassed             bool  `json:"tests_passed"`
	TestAttempts            int   `json:"test_attempts"`
	FailedTestAttempts      int   `json:"failed_test_attempts"`
	TestFailureRecovered    bool  `json:"test_failure_recovered"`
	CompletionVerified      bool  `json:"completion_verified"`
	FinalVerificationPassed *bool `json:"final_verification_passed,omitempty"`
	ToolErrors              int   `json:"tool_errors"`
	Recoveries              int   `json:"recoveries"`
	GateBlocks              int   `json:"gate_blocks"`
	TodoWrites              int   `json:"todo_writes"`
}

type traceDiagnostic struct {
	Code               string   `json:"code"`
	Severity           string   `json:"severity"`
	Message            string   `json:"message"`
	TurnIndex          int      `json:"turn_index,omitempty"`
	ToolName           string   `json:"tool_name,omitempty"`
	Count              int      `json:"count,omitempty"`
	EstimatedSavingsMS int64    `json:"estimated_savings_ms,omitempty"`
	SpanIDs            []string `json:"span_ids,omitempty"`
	Fingerprint        string   `json:"fingerprint,omitempty"`
}

type traceSpan struct {
	ID               string    `json:"id"`
	ParentID         string    `json:"parent_id,omitempty"`
	Sequence         int       `json:"sequence,omitempty"`
	Depth            int       `json:"depth,omitempty"`
	Type             string    `json:"type"`
	Name             string    `json:"name"`
	Status           string    `json:"status,omitempty"`
	Start            time.Time `json:"start,omitempty"`
	End              time.Time `json:"end,omitempty"`
	DurationMS       int64     `json:"duration_ms,omitempty"`
	SelfDurationMS   int64     `json:"self_duration_ms,omitempty"`
	TraceID          string    `json:"trace_id,omitempty"`
	TurnIndex        int       `json:"turn_index,omitempty"`
	ToolName         string    `json:"tool_name,omitempty"`
	ConcurrencyClass string    `json:"concurrency_class,omitempty"`
	Model            string    `json:"model,omitempty"`
	Skill            string    `json:"skill,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type traceTree struct {
	Span     traceSpan   `json:"span"`
	Children []traceTree `json:"children,omitempty"`
}

type traceEvent struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Name         string         `json:"name,omitempty"`
	Time         time.Time      `json:"time,omitempty"`
	StartedAt    time.Time      `json:"started_at,omitempty,omitzero"`
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id,omitempty"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	TurnIndex    int            `json:"turn_index,omitempty"`
	Status       string         `json:"status,omitempty"`
	Role         string         `json:"role,omitempty"`
	Content      string         `json:"content,omitempty"`
	ToolID       string         `json:"tool_id,omitempty"`
	ToolName     string         `json:"tool_name,omitempty"`
	Model        string         `json:"model,omitempty"`
	DurationMS   int64          `json:"duration_ms,omitempty"`
	Input        string         `json:"input,omitempty"`
	Output       string         `json:"output,omitempty"`
	IsError      bool           `json:"is_error,omitempty"`
	Skill        string         `json:"skill,omitempty"`
	Properties   map[string]any `json:"properties,omitempty"`
	Raw          any            `json:"raw,omitempty"`
}

type traceRecap struct {
	ID                string         `json:"id,omitempty"`
	Content           string         `json:"content"`
	Model             string         `json:"model,omitempty"`
	Source            string         `json:"source,omitempty"`
	Status            string         `json:"status,omitempty"`
	Time              time.Time      `json:"time,omitempty"`
	DurationMS        int64          `json:"duration_ms,omitempty"`
	SummarizesEntryID string         `json:"summarizes_entry_id,omitempty"`
	Properties        map[string]any `json:"properties,omitempty"`
}

type traceRewind struct {
	Checkpoints []traceRewindCheckpoint `json:"checkpoints,omitempty"`
	Rewinds     []traceRewindEvent      `json:"rewinds,omitempty"`
}

type traceRewindCheckpoint struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	MessageID   string    `json:"message_id,omitempty"`
	Message     string    `json:"message,omitempty"`
	Time        time.Time `json:"time,omitempty"`
	Index       int       `json:"index"`
	NextIndex   int       `json:"next_index,omitempty"`
	EventsAfter int       `json:"events_after,omitempty"`
	FileChanges int       `json:"file_changes,omitempty"`
}

type traceRewindEvent struct {
	ID      string    `json:"id"`
	Name    string    `json:"name,omitempty"`
	Content string    `json:"content,omitempty"`
	Time    time.Time `json:"time,omitempty"`
	Index   int       `json:"index"`
}

func traceUIHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "trace_ui", "server.traceUIHandler", "serve trace ui")
		if !authorizeViewerShell(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(traceHTML))
	}
}

func traceAPISessionsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "trace_sessions", "server.traceAPISessionsHandler", "list trace sessions")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		source := traceSource(r)
		limit := parseLimit(r.URL.Query().Get("limit"))
		if limit <= 0 {
			limit = defaultTraceSessionLimit
		}
		timeFilter, err := traceTimeFilterFromRequest(r)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		switch source {
		case "tenant":
			if opts.TenantService == nil {
				writeTraceTenantSessionsUnavailable(w)
				return
			}
			items, err := tenantTraceSessionSummaries(r.Context(), opts, limit, timeFilter)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"source": "tenant", "data": items})
		default:
			items, err := localTraceSessionSummaries(localTraceStores(), limit, timeFilter)
			if err != nil {
				writeTenantError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, map[string]any{"source": "local", "data": items})
		}
	}
}

func traceAPISessionHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "trace_session_detail", "server.traceAPISessionHandler", "get trace session detail")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		sessionID := traceSessionIDFromPath(r.URL.Path)
		if sessionID == "" {
			writeTenantError(w, http.StatusBadRequest, "session id is required")
			return
		}
		switch traceSource(r) {
		case "tenant":
			if opts.TenantService == nil {
				writeTraceTenantUnavailable(w)
				return
			}
			id, err := strconv.ParseUint(sessionID, 10, 64)
			if err != nil || id == 0 {
				writeTenantError(w, http.StatusBadRequest, "tenant session id must be numeric")
				return
			}
			resp, err := buildTenantTraceDetail(r.Context(), opts, id, tenantTimelineOptionsFromRequest(r))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, resp)
		default:
			resp, err := buildLocalTraceDetail(localTraceStores(), sessionID)
			if err != nil {
				writeTenantError(w, http.StatusNotFound, err.Error())
				return
			}
			writeJSON(w, resp)
		}
	}
}

func traceAPIExportHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "trace_session_export", "server.traceAPIExportHandler", "export runtime trace artifact")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if schema := strings.TrimSpace(r.URL.Query().Get("schema")); schema != "" && schema != telemetry.SchemaVersionRuntimeTraceV1 {
			writeTenantError(w, http.StatusBadRequest, "unsupported trace schema: "+schema)
			return
		}
		rawID := strings.TrimPrefix(r.URL.Path, "/trace/api/sessions/")
		sessionID := strings.TrimSpace(strings.TrimSuffix(rawID, "/export"))
		if rawID == r.URL.Path || strings.Contains(sessionID, "/") {
			sessionID = ""
		}
		if sessionID == "" {
			writeTenantError(w, http.StatusBadRequest, "session id is required")
			return
		}
		source, sourceErr := traceExportSource(r)
		if sourceErr != nil {
			writeTenantError(w, http.StatusBadRequest, sourceErr.Error())
			return
		}
		var detail traceDetailResponse
		var err error
		if source == "tenant" {
			if opts.TenantService == nil {
				writeTraceTenantUnavailable(w)
				return
			}
			id, parseErr := strconv.ParseUint(sessionID, 10, 64)
			if parseErr != nil || id == 0 {
				writeTenantError(w, http.StatusBadRequest, "tenant session id must be numeric")
				return
			}
			detail, err = buildTenantTraceDetail(r.Context(), opts, id, tenantTimelineOptionsFromRequest(r))
		} else {
			detail, err = buildLocalTraceDetail(localTraceStores(), sessionID)
		}
		if err != nil {
			if source == "tenant" {
				writeTenantServiceError(w, err)
			} else {
				writeTenantError(w, http.StatusNotFound, err.Error())
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="runtime-trace-`+sessionID+`.json"`)
		writeJSON(w, runtimeTraceArtifactFromDetail(detail, buildinfo.Current()))
	}
}

func traceExportSource(r *http.Request) (string, error) {
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	switch source {
	case "", "local":
		return "local", nil
	case "tenant":
		return "tenant", nil
	default:
		return "", fmt.Errorf("unsupported trace source: %s", source)
	}
}

// authorizeViewerShell 守的是 /trace 与 /prompt-dump 这两个 HTML 外壳。
//
// 外壳本身不含任何会话内容 —— 它是一段静态 JS，拿到 token 后再用 Authorization
// 头去调 API。浏览器打开一个链接时没有别的地方能塞 header，所以这里保留
// `?token=`，但它换到的只有一个空页面。真正吐数据的 /trace/api/* 与
// /prompt-dump/api/* 走 authorize（只认 header）：`?token=` 会进 Nginx access
// log、浏览器历史和 Referer，而那些端点的 token 泄漏等于全量对话泄漏
// （AUDIT-P1-21）。
func authorizeViewerShell(w http.ResponseWriter, r *http.Request, token string) bool {
	if strings.TrimSpace(token) == "" {
		return true
	}
	if authorizeHeaderToken(r, token) || secureTokenEqual(strings.TrimSpace(r.URL.Query().Get("token")), token) {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

func traceSource(r *http.Request) string {
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	if source == "tenant" {
		return "tenant"
	}
	return "local"
}

func traceSessionIDFromPath(path string) string {
	raw := strings.TrimPrefix(path, "/trace/api/sessions/")
	if raw == "" || raw == path || strings.Contains(raw, "/") {
		return ""
	}
	return strings.TrimSpace(raw)
}

func writeTraceTenantUnavailable(w http.ResponseWriter) {
	writeTenantError(w, http.StatusServiceUnavailable, "tenant trace source is not configured")
}

func writeTraceTenantSessionsUnavailable(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"source":  "tenant",
		"data":    []traceSessionSummary{},
		"warning": "tenant trace source is not configured",
	})
}

func traceTimeFilterFromRequest(r *http.Request) (traceTimeFilter, error) {
	var filter traceTimeFilter
	from, err := parseTraceTimeQuery(r.URL.Query().Get("from"))
	if err != nil {
		return filter, err
	}
	to, err := parseTraceTimeQuery(r.URL.Query().Get("to"))
	if err != nil {
		return filter, err
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return filter, errors.New("from must be before to")
	}
	filter.From = from
	filter.To = to
	return filter, nil
}

func parseTraceTimeQuery(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, traceTimeQueryLayoutFallback} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("from/to must be RFC3339 timestamps")
}

func (f traceTimeFilter) Active() bool {
	return !f.From.IsZero() || !f.To.IsZero()
}

func (f traceTimeFilter) Match(value time.Time) bool {
	if value.IsZero() {
		return !f.Active()
	}
	if !f.From.IsZero() && value.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && value.After(f.To) {
		return false
	}
	return true
}

func traceSessionTime(item traceSessionSummary) time.Time {
	if !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.StartedAt
}

func localTraceStores() []session.Store {
	stores := []session.Store{session.DefaultStore()}
	if root := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); root != "" {
		stores = append(stores, session.Store{Root: root})
	}
	return stores
}

func localTraceSessionSummaries(stores []session.Store, limit int, filter traceTimeFilter) ([]traceSessionSummary, error) {
	out := make([]traceSessionSummary, 0)
	seen := map[string]bool{}
	for _, store := range stores {
		summaries, err := store.List()
		if err != nil {
			return nil, err
		}
		for _, item := range summaries {
			if seen[item.Path] {
				continue
			}
			seen[item.Path] = true
			summary := traceSessionSummary{
				Source:      "local",
				SessionID:   item.SessionID,
				Title:       item.Title,
				Path:        item.Path,
				CWD:         localTraceCWD(item.Path),
				UpdatedAt:   item.ModTime,
				MessageHint: item.Title,
			}
			if !filter.Match(traceSessionTime(summary)) {
				continue
			}
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func tenantTraceSessionSummaries(ctx context.Context, opts Options, limit int, filter traceTimeFilter) ([]traceSessionSummary, error) {
	if opts.TenantService == nil {
		return nil, tenantserviceUnavailable()
	}
	scanLimit := limit
	if filter.Active() && scanLimit < defaultTraceFilterScanLimit {
		scanLimit = defaultTraceFilterScanLimit
	}
	sessions, err := opts.TenantService.ListSessions(ctx, scanLimit)
	if err != nil {
		return nil, err
	}
	out := make([]traceSessionSummary, 0, len(sessions))
	for _, item := range sessions {
		summary := traceSessionSummary{
			Source:    "tenant",
			SessionID: strconv.FormatUint(item.ID, 10),
			Title:     firstNonEmptyTrace(item.Title, item.SessionKey),
			Status:    item.Status,
			Model:     item.Model,
			CWD:       item.CWD,
			StartedAt: item.StartedAt,
			UpdatedAt: item.LastMessageAt,
		}
		if !filter.Match(traceSessionTime(summary)) {
			continue
		}
		out = append(out, summary)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func buildLocalTraceDetail(stores []session.Store, sessionID string) (traceDetailResponse, error) {
	var summary session.Summary
	found := false
	for _, store := range stores {
		item, ok, err := store.Find(sessionID)
		if err != nil {
			return traceDetailResponse{}, err
		}
		if ok {
			summary = item
			found = true
			break
		}
	}
	if !found {
		return traceDetailResponse{}, errSessionNotFound(sessionID)
	}
	events, err := loadLocalTraceEvents(summary.Path)
	if err != nil {
		return traceDetailResponse{}, err
	}
	ensureLocalTraceIDs(events, summary.SessionID)
	spans := finalizeTraceSpans(localTraceSpans(events))
	analysis := analyzeTrace(events, spans)
	recaps, latestRecap := traceRecapsFromEvents(events)
	resp := traceDetailResponse{
		Source:      "local",
		SessionID:   sessionID,
		Title:       summary.Title,
		Summary:     analysis.Summary,
		Quality:     analysis.Quality,
		Diagnostics: analysis.Diagnostics,
		LatestRecap: latestRecap,
		Recaps:      nonNilTraceRecaps(recaps),
		Spans:       nonNilTraceSpans(spans),
		SpanTree:    buildTraceSpanTree(spans),
		Events:      nonNilTraceEvents(events),
		Rewind:      buildTraceRewind(events),
		Generated:   time.Now().UTC(),
	}
	return resp, nil
}

func traceRecapsFromEvents(events []traceEvent) ([]traceRecap, *traceRecap) {
	recaps := make([]traceRecap, 0)
	for _, event := range events {
		if event.Type != "recap_summary" && event.Name != "session.recap" {
			continue
		}
		content := strings.TrimSpace(event.Content)
		if content == "" {
			continue
		}
		props := event.Properties
		status := firstNonEmptyTrace(event.Status, stringProperty(props, "status"))
		recap := traceRecap{
			ID:                event.ID,
			Content:           content,
			Model:             event.Model,
			Source:            stringProperty(props, "source"),
			Status:            status,
			Time:              event.Time,
			DurationMS:        event.DurationMS,
			SummarizesEntryID: firstNonEmptyTrace(stringProperty(props, "summarizes_entry_id"), stringProperty(props, "message_id")),
			Properties:        props,
		}
		if recap.DurationMS == 0 {
			recap.DurationMS = int64Property(props, "duration_ms")
		}
		recaps = append(recaps, recap)
	}
	var latest *traceRecap
	for i := len(recaps) - 1; i >= 0; i-- {
		if strings.EqualFold(recaps[i].Status, "invalidated") {
			continue
		}
		item := recaps[i]
		latest = &item
		break
	}
	return recaps, latest
}

func localTraceCWD(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] != "projects" {
			continue
		}
		if i+1 >= len(parts) {
			return ""
		}
		return parts[i+1]
	}
	return ""
}

func ensureLocalTraceIDs(events []traceEvent, sessionID string) {
	traceID := "local:" + sessionID
	for i := range events {
		if events[i].TraceID == "" {
			events[i].TraceID = traceID
		}
	}
}

func loadLocalTraceEvents(path string) ([]traceEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	events := make([]traceEvent, 0)
	activeSkill := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for index := 0; scanner.Scan(); index++ {
		line := append([]byte(nil), scanner.Bytes()...)
		var entry session.Entry
		if err := json.Unmarshal(line, &entry); err == nil && isLocalSessionEntry(entry) {
			events = append(events, localTraceEventFromEntry(entry, index, &activeSkill))
			continue
		}
		events = append(events, nativeClaudeTraceEvents(line, index, &activeSkill)...)
	}
	return events, scanner.Err()
}

func localTraceEvents(entries []session.Entry) []traceEvent {
	events := make([]traceEvent, 0, len(entries))
	activeSkill := ""
	for index, entry := range entries {
		events = append(events, localTraceEventFromEntry(entry, index, &activeSkill))
	}
	return events
}

func isLocalSessionEntry(entry session.Entry) bool {
	switch entry.Type {
	case "session", "message", "tool_call", "tool_result", "usage", "permission", "file_change", "checkpoint", "rewind", "fork", "compact_summary", "recap_summary",
		"task_contract", "completion_gate", "action_record", "evidence_record", "delta_record",
		"repair_verification", "provider_error", session.EntryTypeRuntimeSpan:
		return true
	default:
		return false
	}
}

func localTraceEventFromEntry(entry session.Entry, index int, activeSkill *string) traceEvent {
	event := traceEvent{
		ID:       firstNonEmptyTrace(entry.ID, "entry-"+strconv.Itoa(index+1)),
		Type:     entry.Type,
		Time:     entry.Timestamp,
		Role:     entry.Role,
		Content:  trimTraceText(entry.Content, 65536),
		ToolID:   entry.ToolID,
		ToolName: entry.ToolName,
		Model:    entry.Model,
		IsError:  entry.IsError,
	}
	switch entry.Type {
	case session.EntryTypeRuntimeSpan:
		tel, err := session.DecodeRuntimeSpanEntry(entry)
		if err != nil {
			event.Name = session.EntryTypeRuntimeSpan
			event.IsError = true
			return event
		}
		event.ID = "runtime:" + tel.SpanID
		event.Name = tel.Name
		event.Time = tel.OccurredAt
		event.StartedAt = tel.StartedAt
		event.TraceID = tel.TraceID
		event.SpanID = tel.SpanID
		event.ParentSpanID = tel.ParentSpanID
		event.TurnIndex = tel.TurnIndex
		event.Status = tel.Status
		event.ToolName = tel.ToolName
		event.Model = tel.Model
		event.DurationMS = tel.DurationMS
		event.IsError = tel.Status == telemetry.StatusError || tel.Status == telemetry.StatusDenied || tel.Status == telemetry.StatusBlocked
		event.Skill = stringProperty(tel.Properties, "active_skill")
		event.Properties = traceTokenProperties(
			tel.InputTokens,
			tel.OutputTokens,
			tel.CacheCreationInputTokens,
			tel.CacheReadInputTokens,
			tel.CacheCreationEphemeral1hInputTokens,
			tel.CacheCreationEphemeral5mInputTokens,
			tel.Properties,
		)
		if event.Properties == nil {
			event.Properties = map[string]any{}
		}
		if tel.ResourceType != "" {
			event.Properties["resource_type"] = tel.ResourceType
		}
		if tel.ResourceID != "" {
			event.Properties["resource_id"] = tel.ResourceID
		}
		event.Raw = tel
	case "message":
		event.Name = strings.TrimSpace(entry.Role)
	case "tool_call":
		event.Name = "tool_call"
		event.Input = trimTraceText(entry.Content, 4096)
		event.Content = ""
		if skill := skillNameFromToolCall(entry.ToolName, entry.Content); skill != "" {
			*activeSkill = skill
			event.Skill = skill
		} else {
			event.Skill = *activeSkill
		}
	case "tool_result":
		event.Name = "tool_result"
		event.Output = trimTraceText(entry.Content, 4096)
		event.Content = ""
		event.Skill = *activeSkill
	case "usage":
		event.Name = "usage"
		event.Input = strconv.Itoa(entry.InputTokens)
		event.Output = strconv.Itoa(entry.OutputTokens)
		event.Properties = map[string]any{
			"input_tokens":                             entry.InputTokens,
			"raw_input_tokens":                         entry.InputTokens,
			"output_tokens":                            entry.OutputTokens,
			"cache_creation_input_tokens":              entry.CacheCreationInputTokens,
			"cache_read_input_tokens":                  entry.CacheReadInputTokens,
			"cache_creation_ephemeral_1h_tokens":       entry.CacheCreationEphemeral1hInputTokens,
			"cache_creation_ephemeral_5m_tokens":       entry.CacheCreationEphemeral5mInputTokens,
			"cache_creation_ephemeral_1h_input_tokens": entry.CacheCreationEphemeral1hInputTokens,
			"cache_creation_ephemeral_5m_input_tokens": entry.CacheCreationEphemeral5mInputTokens,
			"service_tier":                             entry.ServiceTier,
			"inference_geo":                            entry.InferenceGeo,
			"speed":                                    entry.Speed,
		}
	case "permission":
		event.Name = "permission.decision"
		event.Properties = parseJSONProperties(entry.Content)
		event.Skill = *activeSkill
	case "file_change":
		event.Name = "file_change"
		event.Properties = parseJSONProperties(entry.Content)
		event.Skill = *activeSkill
	case "recap_summary":
		event.Name = "session.recap"
		event.Properties = parseJSONProperties(string(entry.Metadata))
		event.Status = stringProperty(event.Properties, "status")
		if strings.EqualFold(event.Status, "invalidated") {
			event.IsError = true
		}
	case "task_contract", "completion_gate", "action_record", "evidence_record", "delta_record", "repair_verification":
		event.Name = "closure." + entry.Type
		event.Properties = parseJSONProperties(entry.Content)
		event.Status = firstNonEmptyTrace(stringProperty(event.Properties, "severity"), stringProperty(event.Properties, "status"))
		event.Skill = *activeSkill
		if strings.EqualFold(entry.Type, "completion_gate") && strings.HasPrefix(event.Status, "block") {
			event.IsError = true
		}
	default:
		event.Name = entry.Type
		event.Skill = *activeSkill
	}
	return event
}

type nativeClaudeLine struct {
	UUID       string               `json:"uuid"`
	Type       string               `json:"type"`
	Timestamp  time.Time            `json:"timestamp"`
	Message    *nativeClaudeMessage `json:"message"`
	Attachment *nativeClaudeAttach  `json:"attachment"`
}

type nativeClaudeMessage struct {
	ID      string              `json:"id"`
	Role    string              `json:"role"`
	Content nativeClaudeContent `json:"content"`
	Model   string              `json:"model"`
	Usage   nativeClaudeUsage   `json:"usage"`
}

type nativeClaudeContent []nativeClaudeBlock

func (c *nativeClaudeContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*c = []nativeClaudeBlock{{Type: "text", Text: text}}
		return nil
	}
	var blocks []nativeClaudeBlock
	if err := json.Unmarshal(data, &blocks); err != nil {
		return err
	}
	*c = blocks
	return nil
}

type nativeClaudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   nativeToolText  `json:"content"`
	IsError   bool            `json:"is_error"`
}

type nativeToolText string

func (t *nativeToolText) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		*t = nativeToolText(text)
		return nil
	}
	var blocks []nativeClaudeBlock
	if err := json.Unmarshal(data, &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			parts = append(parts, firstNonEmptyTrace(block.Text, string(block.Content)))
		}
		*t = nativeToolText(strings.TrimSpace(strings.Join(parts, "\n")))
		return nil
	}
	*t = nativeToolText(string(data))
	return nil
}

type nativeClaudeUsage struct {
	InputTokens                         int `json:"input_tokens"`
	OutputTokens                        int `json:"output_tokens"`
	CacheCreationInputTokens            int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens                int `json:"cache_read_input_tokens"`
	CacheCreationEphemeral1hInputTokens int `json:"cache_creation_ephemeral_1h_input_tokens"`
	CacheCreationEphemeral5mInputTokens int `json:"cache_creation_ephemeral_5m_input_tokens"`
	CacheCreation                       struct {
		Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
		Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
	ServiceTier  string `json:"service_tier"`
	InferenceGeo string `json:"inference_geo"`
	Speed        string `json:"speed"`
}

type nativeClaudeAttach struct {
	Type       string `json:"type"`
	HookName   string `json:"hookName"`
	ToolUseID  string `json:"toolUseID"`
	DurationMS int64  `json:"durationMs"`
	ExitCode   int    `json:"exitCode"`
}

func nativeClaudeTraceEvents(line []byte, index int, activeSkill *string) []traceEvent {
	var raw nativeClaudeLine
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil
	}
	eventID := firstNonEmptyTrace(raw.UUID, "native-"+strconv.Itoa(index+1))
	if raw.Message == nil {
		return nativeClaudeAttachmentEvent(raw, eventID, activeSkill)
	}
	msg := raw.Message
	events := make([]traceEvent, 0, 3)
	textContent := nativeMessageText(msg.Content)
	if textContent != "" {
		events = append(events, traceEvent{
			ID:      eventID,
			Type:    "message",
			Name:    strings.TrimSpace(msg.Role),
			Time:    raw.Timestamp,
			Role:    msg.Role,
			Model:   msg.Model,
			Content: trimTraceText(textContent, 65536),
			Skill:   *activeSkill,
			Raw:     json.RawMessage(line),
		})
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			input := string(block.Input)
			skill := skillNameFromToolCall(block.Name, input)
			if skill != "" {
				*activeSkill = skill
			}
			events = append(events, traceEvent{
				ID:       firstNonEmptyTrace(block.ID, eventID+":tool_call"),
				Type:     "tool_call",
				Name:     "tool_call",
				Time:     raw.Timestamp,
				ToolID:   block.ID,
				ToolName: block.Name,
				Model:    msg.Model,
				Input:    trimTraceText(input, 4096),
				Skill:    firstNonEmptyTrace(skill, *activeSkill),
				Raw:      json.RawMessage(line),
			})
		case "tool_result":
			events = append(events, traceEvent{
				ID:      firstNonEmptyTrace(block.ToolUseID, eventID+":tool_result"),
				Type:    "tool_result",
				Name:    "tool_result",
				Time:    raw.Timestamp,
				ToolID:  block.ToolUseID,
				Output:  trimTraceText(string(block.Content), 4096),
				IsError: block.IsError,
				Skill:   *activeSkill,
				Raw:     json.RawMessage(line),
			})
		}
	}
	if usageHasTokens(msg.Usage) {
		events = append(events, nativeUsageEvent(eventID+":usage", raw.Timestamp, msg.Model, msg.Usage))
	}
	return events
}

func nativeClaudeAttachmentEvent(raw nativeClaudeLine, eventID string, activeSkill *string) []traceEvent {
	if raw.Attachment == nil {
		return nil
	}
	attachment := raw.Attachment
	if attachment.Type == "" && attachment.HookName == "" {
		return nil
	}
	return []traceEvent{{
		ID:         eventID,
		Type:       "hook",
		Name:       firstNonEmptyTrace(attachment.HookName, attachment.Type),
		Time:       raw.Timestamp,
		ToolID:     attachment.ToolUseID,
		DurationMS: attachment.DurationMS,
		IsError:    attachment.ExitCode != 0,
		Skill:      *activeSkill,
		Raw:        raw,
	}}
}

func nativeMessageText(blocks []nativeClaudeBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func usageHasTokens(usage nativeClaudeUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CacheCreationInputTokens != 0 || usage.CacheReadInputTokens != 0
}

func nativeUsageEvent(id string, timestamp time.Time, model string, usage nativeClaudeUsage) traceEvent {
	ephemeral1h := firstNonZeroTrace(usage.CacheCreationEphemeral1hInputTokens, usage.CacheCreation.Ephemeral1hInputTokens)
	ephemeral5m := firstNonZeroTrace(usage.CacheCreationEphemeral5mInputTokens, usage.CacheCreation.Ephemeral5mInputTokens)
	return traceEvent{
		ID:     id,
		Type:   "usage",
		Name:   "usage",
		Time:   timestamp,
		Model:  model,
		Input:  strconv.Itoa(usage.InputTokens),
		Output: strconv.Itoa(usage.OutputTokens),
		Properties: map[string]any{
			"input_tokens":                             usage.InputTokens,
			"raw_input_tokens":                         usage.InputTokens,
			"output_tokens":                            usage.OutputTokens,
			"cache_creation_input_tokens":              usage.CacheCreationInputTokens,
			"cache_read_input_tokens":                  usage.CacheReadInputTokens,
			"cache_creation_ephemeral_1h_input_tokens": ephemeral1h,
			"cache_creation_ephemeral_5m_input_tokens": ephemeral5m,
			"service_tier":                             usage.ServiceTier,
			"inference_geo":                            usage.InferenceGeo,
			"speed":                                    usage.Speed,
		},
	}
}

func localTraceSpans(events []traceEvent) []traceSpan {
	toolCalls := map[string]traceEvent{}
	spans := make([]traceSpan, 0)
	nativeToolIDs := map[string]struct{}{}
	for _, event := range events {
		if event.Type != session.EntryTypeRuntimeSpan || event.SpanID == "" {
			continue
		}
		baseName, _ := splitTelemetryPhase(event.Name)
		spans = append(spans, traceSpan{
			ID:               event.SpanID,
			ParentID:         event.ParentSpanID,
			Type:             telemetrySpanType(baseName),
			Name:             baseName,
			Status:           event.Status,
			Start:            event.StartedAt,
			End:              event.Time,
			DurationMS:       event.DurationMS,
			TraceID:          event.TraceID,
			TurnIndex:        event.TurnIndex,
			ToolName:         event.ToolName,
			ConcurrencyClass: stringProperty(event.Properties, "concurrency_class"),
			Model:            event.Model,
			Skill:            event.Skill,
		})
		if baseName == telemetry.EventToolExecution {
			if toolID := stringProperty(event.Properties, "resource_id"); toolID != "" {
				nativeToolIDs[toolID] = struct{}{}
			}
		}
	}
	for _, event := range events {
		if event.Type == "tool_call" && event.ToolID != "" {
			if _, native := nativeToolIDs[event.ToolID]; native {
				continue
			}
			toolCalls[event.ToolID] = event
			continue
		}
		if event.Type != "tool_result" || event.ToolID == "" {
			continue
		}
		if _, native := nativeToolIDs[event.ToolID]; native {
			continue
		}
		call, ok := toolCalls[event.ToolID]
		if !ok {
			continue
		}
		duration := int64(0)
		if !call.Time.IsZero() && !event.Time.IsZero() && event.Time.After(call.Time) {
			duration = event.Time.Sub(call.Time).Milliseconds()
		}
		spans = append(spans, traceSpan{
			ID:         "tool:" + event.ToolID,
			Type:       "tool",
			Name:       firstNonEmptyTrace(event.ToolName, call.ToolName, "tool"),
			Status:     boolStatus(!event.IsError),
			Start:      call.Time,
			End:        event.Time,
			DurationMS: duration,
			ToolName:   firstNonEmptyTrace(event.ToolName, call.ToolName),
			Skill:      firstNonEmptyTrace(event.Skill, call.Skill),
			Error:      errorText(event.IsError, event.Output),
		})
	}
	return spans
}

func buildTenantTraceDetail(ctx context.Context, opts Options, sessionID uint64, timelineOpts tenantTimelineOptions) (traceDetailResponse, error) {
	if opts.TenantService == nil {
		return traceDetailResponse{}, tenantserviceUnavailable()
	}
	if timelineOpts.Limit <= 0 {
		timelineOpts.Limit = defaultTenantTimelineLimit
	}
	if timelineOpts.TraceLimit <= 0 {
		timelineOpts.TraceLimit = defaultTenantTimelineTraceLimit
	}
	if timelineOpts.TaskLimit <= 0 {
		timelineOpts.TaskLimit = defaultTenantTimelineTaskLimit
	}
	timeline, err := buildTenantSessionTimeline(ctx, opts.TenantService, sessionID, timelineOpts)
	if err != nil {
		return traceDetailResponse{}, err
	}
	events := tenantTraceEvents(timeline)
	spans := finalizeTraceSpans(tenantTraceSpans(events))
	analysis := analyzeTrace(events, spans)
	return traceDetailResponse{
		Source:      "tenant",
		SessionID:   strconv.FormatUint(sessionID, 10),
		Title:       firstNonEmptyTrace(timeline.Session.Title, timeline.Session.SessionKey),
		TraceIDs:    timeline.TraceIDs,
		Summary:     analysis.Summary,
		Quality:     analysis.Quality,
		Diagnostics: analysis.Diagnostics,
		Spans:       nonNilTraceSpans(spans),
		SpanTree:    buildTraceSpanTree(spans),
		Events:      nonNilTraceEvents(events),
		Rewind:      buildTraceRewind(events),
		Raw:         timeline,
		Generated:   time.Now().UTC(),
	}, nil
}

func nonNilTraceSpans(items []traceSpan) []traceSpan {
	if items == nil {
		return []traceSpan{}
	}
	return items
}

func nonNilTraceEvents(items []traceEvent) []traceEvent {
	if items == nil {
		return []traceEvent{}
	}
	return items
}

func nonNilTraceRecaps(items []traceRecap) []traceRecap {
	if items == nil {
		return []traceRecap{}
	}
	return items
}

func buildTraceRewind(events []traceEvent) traceRewind {
	if len(events) == 0 {
		return traceRewind{}
	}
	checkpoints := make([]traceRewindCheckpoint, 0)
	rewinds := make([]traceRewindEvent, 0)
	for i, event := range events {
		switch event.Type {
		case "checkpoint":
			messageID := strings.TrimSpace(firstNonEmptyTrace(event.Content, strings.TrimPrefix(event.Name, "auto-")))
			checkpoints = append(checkpoints, traceRewindCheckpoint{
				ID:        event.ID,
				Name:      event.Name,
				MessageID: messageID,
				Message:   traceRewindMessageForCheckpoint(events, i, messageID),
				Time:      event.Time,
				Index:     i,
			})
		case "rewind":
			rewinds = append(rewinds, traceRewindEvent{
				ID:      event.ID,
				Name:    event.Name,
				Content: event.Content,
				Time:    event.Time,
				Index:   i,
			})
		}
	}
	for i := range checkpoints {
		start := checkpoints[i].Index + 1
		end := len(events)
		for j := start; j < len(events); j++ {
			if events[j].Type == "checkpoint" || events[j].Type == "rewind" {
				end = j
				break
			}
		}
		checkpoints[i].NextIndex = end
		if end > start {
			checkpoints[i].EventsAfter = end - start
		}
		for _, event := range events[start:end] {
			if event.Type == "file_change" {
				checkpoints[i].FileChanges++
			}
		}
	}
	return traceRewind{Checkpoints: checkpoints, Rewinds: rewinds}
}

func traceRewindMessageForCheckpoint(events []traceEvent, checkpointIndex int, messageID string) string {
	for i := checkpointIndex + 1; i < len(events); i++ {
		event := events[i]
		if event.Type == "checkpoint" || event.Type == "rewind" {
			return ""
		}
		if event.Type != "message" || event.Role != "user" {
			continue
		}
		if messageID == "" || event.ID == messageID {
			return trimTraceText(strings.Join(strings.Fields(event.Content), " "), 240)
		}
	}
	return ""
}

func tenantTraceEvents(resp tenantSessionTimelineResponse) []traceEvent {
	events := make([]traceEvent, 0, len(resp.Timeline))
	for index, item := range resp.Timeline {
		event := traceEvent{
			ID:      "timeline-" + strconv.Itoa(index+1),
			Type:    item.Type,
			Time:    item.Time,
			TraceID: item.TraceID,
		}
		switch item.Type {
		case "message":
			if item.Message == nil {
				continue
			}
			msg := *item.Message
			event.ID = "message:" + strconv.FormatUint(msg.ID, 10)
			event.Name = msg.Role
			event.Role = msg.Role
			event.Content = trimTraceText(msg.Content, 65536)
			event.ToolID = msg.ToolID
			event.ToolName = msg.ToolName
			event.Model = msg.Model
			event.IsError = msg.IsError
			event.Properties = traceTokenProperties(int(msg.InputTokens), int(msg.OutputTokens), 0, 0, 0, 0, nil)
			event.Raw = msg
		case "telemetry":
			if item.TelemetryEvent == nil {
				continue
			}
			tel := telemetry.RestoreTraceContext(item.TelemetryEvent.Event)
			event.ID = "telemetry:" + strconv.FormatUint(tel.ID, 10)
			event.Name = tel.Name
			event.StartedAt = tel.StartedAt
			event.SpanID = tel.SpanID
			event.ParentSpanID = tel.ParentSpanID
			event.TurnIndex = tel.TurnIndex
			event.Status = tel.Status
			event.ToolName = tel.ToolName
			event.Model = tel.Model
			event.DurationMS = tel.DurationMS
			event.IsError = tel.Status == telemetry.StatusError || tel.Status == telemetry.StatusDenied || tel.Status == telemetry.StatusBlocked
			event.Skill = firstNonEmptyTrace(
				stringProperty(tel.Properties, "active_skill"),
				stringProperty(tel.Properties, "skill"),
				skillNameFromToolCall(tel.ToolName, stringProperty(tel.Properties, "input")),
			)
			event.Properties = traceTokenProperties(
				tel.InputTokens,
				tel.OutputTokens,
				tel.CacheCreationInputTokens,
				tel.CacheReadInputTokens,
				tel.CacheCreationEphemeral1hInputTokens,
				tel.CacheCreationEphemeral5mInputTokens,
				tel.Properties,
			)
			if event.Properties == nil {
				event.Properties = map[string]any{}
			}
			if tel.ResourceType != "" {
				event.Properties["resource_type"] = tel.ResourceType
			}
			if tel.ResourceID != "" {
				event.Properties["resource_id"] = tel.ResourceID
			}
			event.Raw = tel
		case "audit":
			if item.AuditLog == nil {
				continue
			}
			audit := *item.AuditLog
			event.ID = "audit:" + strconv.FormatUint(audit.ID, 10)
			event.Name = audit.Action
			event.Content = trimTraceText(audit.MetadataJSON, 4096)
			event.Raw = audit
		case "agent_task":
			if item.AgentTask == nil {
				continue
			}
			task := *item.AgentTask
			event.ID = "agent_task:" + strconv.FormatUint(task.ID, 10)
			event.Name = task.AgentName
			event.Status = task.Status
			event.Model = task.Model
			event.Content = trimTraceText(task.Description, 1024)
			event.Raw = task
		case "agent_task_event":
			if item.AgentTaskEvent == nil {
				continue
			}
			taskEvent := *item.AgentTaskEvent
			event.ID = "agent_task_event:" + strconv.FormatUint(taskEvent.ID, 10)
			event.Name = taskEvent.EventType
			event.Content = trimTraceText(taskEvent.PayloadJSON, 4096)
			event.Properties = parseJSONProperties(taskEvent.PayloadJSON)
			if event.Properties == nil {
				event.Properties = map[string]any{}
			}
			event.Properties["task_id"] = taskEvent.TaskID
			event.Model = stringProperty(event.Properties, "model")
			event.ToolName = stringProperty(event.Properties, "tool_name")
			event.IsError = boolProperty(event.Properties, "is_error")
			event.Raw = taskEvent
		}
		events = append(events, event)
	}
	return events
}

func tenantTraceSpans(events []traceEvent) []traceSpan {
	starts := map[string]traceEvent{}
	spans := make([]traceSpan, 0)
	for _, event := range events {
		if event.Type != "telemetry" {
			if event.Type == "agent_task" {
				spans = append(spans, traceSpan{
					ID:      event.ID,
					Type:    "agent_task",
					Name:    firstNonEmptyTrace(event.Name, "agent task"),
					Status:  event.Status,
					Start:   event.Time,
					TraceID: event.TraceID,
					Model:   event.Model,
				})
			}
			continue
		}
		baseName, phase := splitTelemetryPhase(event.Name)
		var key string
		if event.SpanID != "" {
			key = "span|" + event.SpanID
			if event.Status == telemetry.StatusStarted {
				phase = telemetry.SpanPhaseStarted
			} else if event.Status != "" {
				phase = telemetry.SpanPhaseFinished
			}
		} else {
			key = baseName + "|" + event.TraceID + "|" + event.ToolName + "|" + event.Model
		}
		if phase == telemetry.SpanPhaseStarted {
			starts[key] = event
			continue
		}
		if phase != telemetry.SpanPhaseFinished {
			continue
		}
		start := starts[key]
		startTime := event.StartedAt
		if startTime.IsZero() {
			startTime = start.StartedAt
		}
		if startTime.IsZero() {
			startTime = start.Time
		}
		if startTime.IsZero() {
			startTime = event.Time.Add(-time.Duration(event.DurationMS) * time.Millisecond)
		}
		spans = append(spans, traceSpan{
			ID:               firstNonEmptyTrace(event.SpanID, event.ID),
			ParentID:         firstNonEmptyTrace(event.ParentSpanID, start.ParentSpanID),
			Type:             telemetrySpanType(baseName),
			Name:             baseName,
			Status:           event.Status,
			Start:            startTime,
			End:              event.Time,
			DurationMS:       event.DurationMS,
			TraceID:          event.TraceID,
			TurnIndex:        max(event.TurnIndex, start.TurnIndex),
			ToolName:         event.ToolName,
			ConcurrencyClass: firstNonEmptyTrace(stringProperty(event.Properties, "concurrency_class"), stringProperty(start.Properties, "concurrency_class")),
			Model:            event.Model,
			Skill:            firstNonEmptyTrace(event.Skill, start.Skill),
			Error:            errorText(event.IsError, stringProperty(event.Properties, "error")),
		})
	}
	return spans
}

func finalizeTraceSpans(spans []traceSpan) []traceSpan {
	out := append([]traceSpan(nil), spans...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		if out[i].DurationMS != out[j].DurationMS {
			return out[i].DurationMS > out[j].DurationMS
		}
		return out[i].ID < out[j].ID
	})
	for i := range out {
		out[i].Sequence = i + 1
		out[i].ParentID = strings.TrimSpace(out[i].ParentID)
		if out[i].DurationMS == 0 && !out[i].Start.IsZero() && !out[i].End.IsZero() && out[i].End.After(out[i].Start) {
			out[i].DurationMS = out[i].End.Sub(out[i].Start).Milliseconds()
		}
	}
	normalizeExplicitTraceParents(out)
	annotateTraceSpanParents(out)
	annotateTraceSpanDepthAndSelf(out)
	return out
}

func normalizeExplicitTraceParents(spans []traceSpan) {
	seenIDs := make(map[string]int, len(spans))
	for i := range spans {
		id := strings.TrimSpace(spans[i].ID)
		if id == "" {
			id = "span-" + strconv.Itoa(i+1)
		}
		seenIDs[id]++
		if seenIDs[id] > 1 {
			spans[i].ID = id + "#" + strconv.Itoa(seenIDs[id])
		} else {
			spans[i].ID = id
		}
	}
	byID := make(map[string]int, len(spans))
	for i := range spans {
		byID[spans[i].ID] = i
	}
	for i := range spans {
		parentID := strings.TrimSpace(spans[i].ParentID)
		parentIndex, ok := byID[parentID]
		if parentID == "" || !ok || parentIndex == i ||
			(spans[i].TraceID != "" && spans[parentIndex].TraceID != "" && spans[i].TraceID != spans[parentIndex].TraceID) {
			spans[i].ParentID = ""
			continue
		}
		visited := map[string]struct{}{spans[i].ID: {}}
		for current := parentID; current != ""; {
			if _, exists := visited[current]; exists {
				spans[i].ParentID = ""
				break
			}
			visited[current] = struct{}{}
			nextIndex, exists := byID[current]
			if !exists {
				break
			}
			current = strings.TrimSpace(spans[nextIndex].ParentID)
		}
	}
}

func annotateTraceSpanParents(spans []traceSpan) {
	for i := range spans {
		if strings.TrimSpace(spans[i].ParentID) != "" {
			continue
		}
		parentIndex := -1
		for j := range spans {
			if i == j || !traceSpanCanParent(spans[j], spans[i]) {
				continue
			}
			if parentIndex < 0 || traceSpanParentScore(spans[j], spans[i]) < traceSpanParentScore(spans[parentIndex], spans[i]) {
				parentIndex = j
			}
		}
		if parentIndex >= 0 {
			spans[i].ParentID = spans[parentIndex].ID
		}
	}
}

func traceSpanCanParent(parent, child traceSpan) bool {
	if parent.ID == "" || child.ID == "" || parent.ID == child.ID {
		return false
	}
	if parent.TraceID != "" && child.TraceID != "" && parent.TraceID != child.TraceID {
		return false
	}
	if parent.DurationMS <= 0 || child.DurationMS < 0 {
		return false
	}
	if parent.Start.IsZero() || child.Start.IsZero() {
		return false
	}
	parentEnd := traceSpanEnd(parent)
	childEnd := traceSpanEnd(child)
	if parentEnd.IsZero() || childEnd.IsZero() {
		return false
	}
	if child.Start.Before(parent.Start) || childEnd.After(parentEnd) {
		return false
	}
	if traceSpanKindRank(parent) >= traceSpanKindRank(child) {
		return false
	}
	return traceSpanParentAllows(parent, child)
}

func traceSpanParentAllows(parent, child traceSpan) bool {
	parentName := strings.ToLower(parent.Name)
	childName := strings.ToLower(child.Name)
	parentType := strings.ToLower(parent.Type)
	childType := strings.ToLower(child.Type)
	switch {
	case parentType == "api":
		return childType != "api"
	case parentType == "mobile":
		return childType == "query" || childType == "model" || childType == "tool" || childType == "skill" || childType == "phase" || childType == "agent_task"
	case parentType == "query":
		return childType == "model" || childType == "tool" || childType == "skill" || childType == "phase" || childType == "agent_task"
	case parentType == "agent_task":
		return childType == "model" || childType == "tool" || childType == "phase"
	case parentType == "model":
		return childType == "phase" && strings.HasPrefix(childName, "model.phase.")
	case parentType == "tool":
		return childType == "phase" || childType == "skill"
	case strings.Contains(parentName, "stream.create"):
		return strings.Contains(childName, "http.") || strings.Contains(childName, "stream.")
	default:
		return false
	}
}

func traceSpanParentScore(parent, child traceSpan) int64 {
	parentDuration := parent.DurationMS
	childDuration := child.DurationMS
	if parentDuration <= 0 {
		parentDuration = traceSpanEnd(parent).Sub(parent.Start).Milliseconds()
	}
	if childDuration <= 0 {
		childDuration = traceSpanEnd(child).Sub(child.Start).Milliseconds()
	}
	return maxInt64(0, parentDuration-childDuration)*10 + int64(traceSpanKindRank(parent))
}

func annotateTraceSpanDepthAndSelf(spans []traceSpan) {
	byID := map[string]traceSpan{}
	for _, span := range spans {
		byID[span.ID] = span
	}
	for i := range spans {
		spans[i].Depth = traceSpanDepth(spans[i], byID)
		children := traceDirectChildren(spans[i].ID, spans)
		covered := coveredTraceSpanDuration(children)
		if spans[i].DurationMS > 0 {
			spans[i].SelfDurationMS = maxInt64(0, spans[i].DurationMS-covered)
		}
	}
}

func traceSpanDepth(span traceSpan, byID map[string]traceSpan) int {
	depth := 0
	current := strings.TrimSpace(span.ParentID)
	seen := map[string]struct{}{}
	for current != "" {
		if _, ok := seen[current]; ok {
			break
		}
		seen[current] = struct{}{}
		parent, ok := byID[current]
		if !ok {
			break
		}
		depth++
		current = strings.TrimSpace(parent.ParentID)
	}
	if depth > 8 {
		return 8
	}
	return depth
}

func traceDirectChildren(parentID string, spans []traceSpan) []traceSpan {
	children := make([]traceSpan, 0)
	for _, span := range spans {
		if span.ParentID == parentID {
			children = append(children, span)
		}
	}
	return children
}

func coveredTraceSpanDuration(spans []traceSpan) int64 {
	type interval struct {
		start time.Time
		end   time.Time
	}
	intervals := make([]interval, 0, len(spans))
	for _, span := range spans {
		end := traceSpanEnd(span)
		if span.Start.IsZero() || end.IsZero() || !end.After(span.Start) {
			continue
		}
		intervals = append(intervals, interval{start: span.Start, end: end})
	}
	sort.SliceStable(intervals, func(i, j int) bool {
		if !intervals[i].start.Equal(intervals[j].start) {
			return intervals[i].start.Before(intervals[j].start)
		}
		return intervals[i].end.Before(intervals[j].end)
	})
	var total int64
	var currentStart, currentEnd time.Time
	for _, item := range intervals {
		if currentStart.IsZero() {
			currentStart = item.start
			currentEnd = item.end
			continue
		}
		if item.start.After(currentEnd) {
			total += currentEnd.Sub(currentStart).Milliseconds()
			currentStart = item.start
			currentEnd = item.end
			continue
		}
		if item.end.After(currentEnd) {
			currentEnd = item.end
		}
	}
	if !currentStart.IsZero() {
		total += currentEnd.Sub(currentStart).Milliseconds()
	}
	return total
}

func buildTraceSpanTree(spans []traceSpan) []traceTree {
	children := map[string][]traceSpan{}
	roots := make([]traceSpan, 0)
	for _, span := range spans {
		if span.ParentID == "" {
			roots = append(roots, span)
			continue
		}
		children[span.ParentID] = append(children[span.ParentID], span)
	}
	for parentID := range children {
		sort.SliceStable(children[parentID], func(i, j int) bool {
			return children[parentID][i].Sequence < children[parentID][j].Sequence
		})
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].Sequence < roots[j].Sequence })
	out := make([]traceTree, 0, len(roots))
	for _, root := range roots {
		out = append(out, buildTraceTreeNode(root, children, map[string]struct{}{}))
	}
	return out
}

func buildTraceTreeNode(span traceSpan, children map[string][]traceSpan, path map[string]struct{}) traceTree {
	node := traceTree{Span: span}
	if _, seen := path[span.ID]; seen {
		return node
	}
	path[span.ID] = struct{}{}
	defer delete(path, span.ID)
	for _, child := range children[span.ID] {
		node.Children = append(node.Children, buildTraceTreeNode(child, children, path))
	}
	return node
}

func traceSpanEnd(span traceSpan) time.Time {
	if !span.End.IsZero() {
		return span.End
	}
	if !span.Start.IsZero() && span.DurationMS > 0 {
		return span.Start.Add(time.Duration(span.DurationMS) * time.Millisecond)
	}
	return time.Time{}
}

func traceSpanKindRank(span traceSpan) int {
	name := strings.ToLower(span.Name)
	switch strings.ToLower(span.Type) {
	case "api":
		return 10
	case "mobile":
		return 20
	case "query":
		return 30
	case "agent_task":
		return 35
	case "model":
		return 40
	case "tool", "skill":
		return 50
	case "phase":
		if strings.Contains(name, "http.") || strings.Contains(name, "stream.") {
			return 60
		}
		return 55
	default:
		return 70
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func summarizeTrace(events []traceEvent, spans []traceSpan) traceSummary {
	var summary traceSummary
	skillSet := map[string]struct{}{}
	usageGroups := map[string]traceUsageGroup{}
	var first, last time.Time
	for _, event := range events {
		if !event.Time.IsZero() {
			if first.IsZero() || event.Time.Before(first) {
				first = event.Time
			}
			if last.IsZero() || event.Time.After(last) {
				last = event.Time
			}
		}
		switch event.Type {
		case "message":
			summary.Messages++
			if event.Role == "user" {
				summary.Turns++
			}
		case "tool_call":
			summary.ToolCalls++
		case "tool_result":
			if event.IsError {
				summary.ToolErrors++
			}
		case "permission":
			summary.PermissionDecisions++
		case "file_change":
			summary.FilesChanged++
		case "agent_task":
			summary.AgentTasks++
		case "agent_task_event":
			if strings.EqualFold(event.Name, agenttasks.EventUsage) && event.Properties != nil {
				inputTokens, cacheCreationTokens, cacheReadTokens := traceUsageTokens(event.Properties)
				addTraceUsageGroup(usageGroups, event, inputTokens, intProperty(event.Properties, "output_tokens"), cacheCreationTokens, cacheReadTokens)
			}
		case "telemetry":
			if strings.EqualFold(event.Name, "permission.decision") {
				summary.PermissionDecisions++
			}
			if strings.EqualFold(event.Name, "tool.execution.finished") {
				summary.ToolCalls++
			}
			if event.IsError {
				summary.Errors++
			}
		case session.EntryTypeRuntimeSpan:
			if event.IsError {
				summary.Errors++
			}
		}
		if event.Skill != "" {
			skillSet[event.Skill] = struct{}{}
		}
		if event.Properties != nil {
			inputTokens, cacheCreationTokens, cacheReadTokens := traceUsageTokens(event.Properties)
			addTraceUsageGroup(usageGroups, event, inputTokens, intProperty(event.Properties, "output_tokens"), cacheCreationTokens, cacheReadTokens)
			if skill := stringProperty(event.Properties, "active_skill"); skill != "" {
				skillSet[skill] = struct{}{}
			}
		}
	}
	for _, usage := range usageGroups {
		summary.InputTokens += usage.InputTokens
		summary.OutputTokens += usage.OutputTokens
		summary.CacheReadTokens += usage.CacheReadTokens
		summary.CacheCreationTokens += usage.CacheCreationTokens
	}
	for _, span := range spans {
		switch span.Type {
		case "model":
			summary.TotalModelDurationMS += span.DurationMS
		case "tool":
			summary.TotalToolDurationMS += span.DurationMS
		}
		if span.Skill != "" {
			skillSet[span.Skill] = struct{}{}
		}
		if span.Error != "" {
			summary.Errors++
		}
	}
	if !first.IsZero() && !last.IsZero() && last.After(first) {
		summary.DurationMS = last.Sub(first).Milliseconds()
	}
	summary.Skills = sortedStringSet(skillSet)
	summary.TotalTokens = summary.InputTokens + summary.OutputTokens
	if summary.InputTokens > 0 && summary.CacheReadTokens > 0 {
		summary.CacheHitRatio = float64(summary.CacheReadTokens) / float64(summary.InputTokens)
	}
	summary.TokenSummary = traceTokenSummary(summary)
	summary.CacheSummary = traceCacheSummary(summary)
	return summary
}

func skillNameFromToolCall(toolName, input string) string {
	if !strings.EqualFold(strings.TrimSpace(toolName), "Skill") {
		return ""
	}
	var payload struct {
		Name  string `json:"name"`
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &payload); err != nil {
		return ""
	}
	return firstNonEmptyTrace(payload.Name, payload.Skill)
}

func traceTokenSummary(summary traceSummary) string {
	if summary.TotalTokens == 0 {
		return "No token usage was recorded for this session."
	}
	parts := []string{
		"Input " + strconv.Itoa(summary.InputTokens),
		"output " + strconv.Itoa(summary.OutputTokens),
		"total " + strconv.Itoa(summary.TotalTokens),
	}
	if summary.CacheReadTokens > 0 {
		parts = append(parts, "cache read "+strconv.Itoa(summary.CacheReadTokens))
	}
	if summary.CacheCreationTokens > 0 {
		parts = append(parts, "cache write "+strconv.Itoa(summary.CacheCreationTokens))
	}
	return strings.Join(parts, " · ")
}

func traceCacheSummary(summary traceSummary) string {
	if summary.InputTokens == 0 {
		return "Cache hit rate is unavailable until provider usage is recorded."
	}
	if summary.CacheReadTokens == 0 && summary.CacheCreationTokens == 0 {
		return "No prompt-cache read or write tokens were reported."
	}
	ratio := int(summary.CacheHitRatio*100 + 0.5)
	return "Cache hit " + strconv.Itoa(ratio) + "% · read " + strconv.Itoa(summary.CacheReadTokens) + " · write " + strconv.Itoa(summary.CacheCreationTokens)
}

func splitTelemetryPhase(name string) (string, string) {
	for _, suffix := range []string{telemetry.SpanStartedSuffix, telemetry.SpanFinishedSuffix} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix), strings.TrimPrefix(suffix, ".")
		}
	}
	return name, ""
}

func telemetrySpanType(name string) string {
	switch {
	case strings.HasPrefix(name, "agent.model."):
		return "model"
	case strings.HasPrefix(name, "agent.tool."):
		return "tool"
	case strings.HasPrefix(name, "agent."):
		return "agent_task"
	case strings.HasPrefix(name, "model.phase.") || strings.HasPrefix(name, "mobile.phase."):
		return "phase"
	case strings.HasPrefix(name, "model."):
		return "model"
	case strings.HasPrefix(name, "tool."):
		return "tool"
	case strings.HasPrefix(name, "api."):
		return "api"
	case strings.HasPrefix(name, "mobile."):
		return "mobile"
	case strings.HasPrefix(name, "query."):
		return "query"
	case strings.HasPrefix(name, "hook."):
		return "hook"
	case strings.HasPrefix(name, "permission."):
		return "permission"
	case strings.HasPrefix(name, "context.compact"):
		return "compact"
	case strings.HasPrefix(name, "gate."):
		return "gate"
	case strings.HasPrefix(name, "session.persistence"):
		return "persistence"
	case strings.HasPrefix(name, "output.renderer"):
		return "renderer"
	default:
		return "telemetry"
	}
}

func parseJSONProperties(content string) map[string]any {
	var props map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &props); err != nil {
		return nil
	}
	return telemetry.SanitizeProperties(props)
}

func traceTokenProperties(inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens, cacheCreation1hTokens, cacheCreation5mTokens int, props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	setTraceIntProperty(props, "input_tokens", inputTokens)
	setTraceIntProperty(props, "output_tokens", outputTokens)
	setTraceIntProperty(props, "cache_creation_input_tokens", cacheCreationTokens)
	setTraceIntProperty(props, "cache_read_input_tokens", cacheReadTokens)
	setTraceIntProperty(props, "cache_creation_ephemeral_1h_input_tokens", cacheCreation1hTokens)
	setTraceIntProperty(props, "cache_creation_ephemeral_5m_input_tokens", cacheCreation5mTokens)
	if len(props) == 0 {
		return nil
	}
	return props
}

func setTraceIntProperty(props map[string]any, key string, value int) {
	if value == 0 || props == nil {
		return
	}
	if intProperty(props, key) == 0 {
		props[key] = value
	}
}

func traceUsageTokens(props map[string]any) (inputTokens int, cacheCreationTokens int, cacheReadTokens int) {
	inputTokens = intProperty(props, "input_tokens")
	cacheCreationTokens = intProperty(props, "cache_creation_input_tokens")
	cacheReadTokens = intProperty(props, "cache_read_input_tokens")
	rawInputTokens := intProperty(props, "raw_input_tokens")
	if rawInputTokens > 0 {
		inputTokens = rawInputTokens + cacheCreationTokens + cacheReadTokens
	}
	return inputTokens, cacheCreationTokens, cacheReadTokens
}

type traceUsageGroup struct {
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	Rank                int
}

func addTraceUsageGroup(groups map[string]traceUsageGroup, event traceEvent, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int) {
	if inputTokens == 0 && outputTokens == 0 && cacheCreationTokens == 0 && cacheReadTokens == 0 {
		return
	}
	key := traceUsageGroupKey(event)
	rank := traceUsageRank(event)
	if current, ok := groups[key]; ok && current.Rank > rank {
		return
	}
	groups[key] = traceUsageGroup{
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		CacheCreationTokens: cacheCreationTokens,
		CacheReadTokens:     cacheReadTokens,
		Rank:                rank,
	}
}

func traceUsageGroupKey(event traceEvent) string {
	if event.Properties != nil {
		if resourceType := stringProperty(event.Properties, "resource_type"); resourceType == "agent_task" {
			if resourceID := stringProperty(event.Properties, "resource_id"); resourceID != "" {
				return "agent_task:" + resourceID + ":" + event.Model
			}
		}
		if taskID := stringProperty(event.Properties, "task_id"); taskID != "" {
			return "agent_task:" + taskID + ":" + event.Model
		}
		if taskID := intProperty(event.Properties, "task_id"); taskID > 0 {
			return "agent_task:" + strconv.Itoa(taskID) + ":" + event.Model
		}
	}
	if event.Type == "telemetry" && strings.HasPrefix(event.Name, "agent.") && event.Raw != nil {
		if raw, ok := event.Raw.(telemetry.Event); ok && raw.ResourceType == "agent_task" && raw.ResourceID != "" {
			return "agent_task:" + raw.ResourceID + ":" + event.Model
		}
	}
	if event.TraceID != "" && (traceUsageRank(event) > 1 || event.Model != "" || strings.EqualFold(event.Role, "assistant")) {
		return "trace:" + event.TraceID
	}
	if event.Model != "" {
		return "model:" + event.Model
	}
	if event.ID != "" {
		return "event:" + event.ID
	}
	return event.Type + ":" + event.Name
}

func traceUsageRank(event traceEvent) int {
	switch {
	case (event.Type == "telemetry" || event.Type == session.EntryTypeRuntimeSpan) && strings.EqualFold(event.Name, "query.run.finished"):
		return 4
	case (event.Type == "telemetry" || event.Type == session.EntryTypeRuntimeSpan) && strings.EqualFold(event.Name, "model.request.finished"):
		return 3
	case event.Type == "telemetry" && strings.EqualFold(event.Name, "mobile.chat.stream.finished"):
		return 2
	case event.Type == "usage":
		return 2
	default:
		return 1
	}
}

func intProperty(props map[string]any, key string) int {
	if props == nil {
		return 0
	}
	switch value := props[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func int64Property(props map[string]any, key string) int64 {
	return int64(intProperty(props, key))
}

func stringProperty(props map[string]any, key string) string {
	if props == nil {
		return ""
	}
	value, _ := props[key].(string)
	return strings.TrimSpace(value)
}

func boolProperty(props map[string]any, key string) bool {
	if props == nil {
		return false
	}
	value, _ := props[key].(bool)
	return value
}

func trimTraceText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func boolStatus(ok bool) string {
	if ok {
		return telemetry.StatusOK
	}
	return telemetry.StatusError
}

func errorText(isError bool, text string) string {
	if !isError {
		return ""
	}
	return trimTraceText(text, 1024)
}

func sortedStringSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyTrace(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonZeroTrace(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type traceError string

func (e traceError) Error() string { return string(e) }

func errSessionNotFound(sessionID string) error {
	return traceError("session not found: " + sessionID)
}

func tenantserviceUnavailable() error {
	return traceError("tenant storage is not configured")
}

//go:embed trace_viewer.html
var traceHTML string
