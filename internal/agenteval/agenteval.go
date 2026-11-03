package agenteval

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/hooks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/tools"
	agenttool "github.com/konglong87/super-niuma/internal/tools/agent"
	"github.com/konglong87/super-niuma/internal/tools/bash"
	"github.com/konglong87/super-niuma/internal/tools/fileread"
	"github.com/konglong87/super-niuma/internal/tools/skill"
	"github.com/konglong87/super-niuma/internal/tools/task"
)

type Options struct {
	CWD       string
	Dataset   string
	Output    string
	Format    string
	FailFast  bool
	Profile   string
	Suite     string
	FixedTime time.Time
	// Runs / Provider 仅 golden-probe profile 使用：真实模型探针的重复次数
	// 与 settings.json fallbackProviders 里的 provider 名。
	Runs     int
	Provider string
}

type Suite struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
	Cases       []Case `json:"cases"`
}

type Case struct {
	ID              string   `json:"id"`
	Description     string   `json:"description,omitempty"`
	Type            string   `json:"type"`
	Prompt          string   `json:"prompt,omitempty"`
	ExpectContains  []string `json:"expect_contains,omitempty"`
	ExpectToolCalls []string `json:"expect_tool_calls,omitempty"`
	ExpectMaxTurns  int      `json:"expect_max_turns,omitempty"`
	// ExpectMaxGatePreflights 为 gate 摩擦预算：nil 表示不断言，指向 0 表示断言零次 gate preflight。
	ExpectMaxGatePreflights *int `json:"expect_max_gate_preflights,omitempty"`
}

type Report struct {
	SuiteID     string       `json:"suite_id"`
	Status      string       `json:"status"`
	StartedAt   string       `json:"started_at"`
	FinishedAt  string       `json:"finished_at"`
	DurationMS  int64        `json:"duration_ms"`
	Total       int          `json:"total"`
	Passed      int          `json:"passed"`
	Failed      int          `json:"failed"`
	Skipped     int          `json:"skipped,omitempty"`
	Cases       []CaseResult `json:"cases"`
	Environment Environment  `json:"environment"`
}

type Environment struct {
	CWD     string `json:"cwd"`
	Suite   string `json:"suite,omitempty"`
	Dataset string `json:"dataset,omitempty"`
	Mode    string `json:"mode"`
}

type CaseResult struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Status     string         `json:"status"`
	DurationMS int64          `json:"duration_ms"`
	Checks     []CheckResult  `json:"checks"`
	Evidence   map[string]any `json:"evidence,omitempty"`
	Error      string         `json:"error,omitempty"`
	Metrics    *CaseMetrics   `json:"metrics,omitempty"`
}

type CheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func Run(ctx context.Context, opts Options) (Report, error) {
	started := time.Now()
	if !opts.FixedTime.IsZero() {
		started = opts.FixedTime
	}
	if strings.TrimSpace(opts.CWD) == "" {
		cwd, _ := os.Getwd()
		opts.CWD = cwd
	}
	profile, err := normalizeProfile(opts.Profile)
	if err != nil {
		return Report{}, err
	}
	opts.Profile = profile
	if profile == "live" {
		return runLiveProfile(ctx, opts, started)
	}
	if profile == "live-agent-api" {
		return runLiveAgentAPIProfile(ctx, opts, started)
	}
	if profile == "anthropic-thinking" {
		return runAnthropicThinkingProfile(ctx, opts, started)
	}
	if profile == "golden-probe" {
		return runGoldenProbeProfile(ctx, opts, started)
	}
	suite, err := selectSuite(opts)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		SuiteID:     suite.ID,
		Status:      "passed",
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
		Total:       len(suite.Cases),
		Cases:       make([]CaseResult, 0, len(suite.Cases)),
		Environment: Environment{CWD: opts.CWD, Suite: opts.Suite, Dataset: opts.Dataset, Mode: "deterministic-local"},
	}
	for _, testCase := range suite.Cases {
		result := runCase(ctx, opts, testCase)
		report.Cases = append(report.Cases, result)
		if result.Status == "passed" {
			report.Passed++
		} else {
			report.Failed++
			report.Status = "failed"
			if opts.FailFast {
				break
			}
		}
	}
	finished := time.Now()
	if !opts.FixedTime.IsZero() {
		finished = opts.FixedTime.Add(time.Duration(len(report.Cases)) * time.Millisecond)
	}
	report.FinishedAt = finished.UTC().Format(time.RFC3339Nano)
	report.DurationMS = finished.Sub(started).Milliseconds()
	if opts.Output != "" {
		if err := WriteReport(report, opts.Output, opts.Format); err != nil {
			return report, err
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("agent eval failed: %d/%d cases failed", report.Failed, report.Total)
	}
	return report, nil
}

func WriteReport(report Report, path, format string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var data []byte
	var err error
	switch normalizeFormat(format, path) {
	case "markdown":
		data = []byte(Markdown(report))
	default:
		data, err = json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
	}
	return os.WriteFile(path, data, 0644)
}

func Markdown(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent Eval Report\n\n")
	fmt.Fprintf(&b, "- Suite: `%s`\n", report.SuiteID)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&b, "- Cases: %d total, %d passed, %d failed, %d skipped\n", report.Total, report.Passed, report.Failed, report.Skipped)
	fmt.Fprintf(&b, "- Duration: %dms\n\n", report.DurationMS)
	fmt.Fprintln(&b, "| Case | Type | Status | Turns | Gates | Checks |")
	fmt.Fprintln(&b, "| --- | --- | --- | --- | --- | --- |")
	for _, item := range report.Cases {
		var checks []string
		for _, check := range item.Checks {
			checks = append(checks, check.Name+":"+check.Status)
		}
		turns, gates := "-", "-"
		if item.Metrics != nil {
			turns = strconv.Itoa(item.Metrics.Turns)
			gates = strconv.Itoa(item.Metrics.GatePreflights)
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %s | %s | %s |\n", item.ID, item.Type, item.Status, turns, gates, strings.Join(checks, ", "))
	}
	return b.String()
}

func runLiveProfile(ctx context.Context, opts Options, started time.Time) (Report, error) {
	baseURL := strings.TrimSpace(firstEnv("NIUMA_EVAL_LIVE_API_BASE"))
	report := Report{
		SuiteID:     "niuma-agent-eval-live-p1",
		Status:      "passed",
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
		Environment: Environment{CWD: opts.CWD, Suite: opts.Suite, Dataset: opts.Dataset, Mode: "live-api-server"},
	}
	if baseURL == "" {
		report.Status = "skipped"
		report.Total = 1
		report.Skipped = 1
		report.Cases = []CaseResult{{
			ID:     "live_environment",
			Type:   "live",
			Status: "skipped",
			Checks: []CheckResult{{
				Name:   "env.api_base",
				Status: "skipped",
				Detail: "set NIUMA_EVAL_LIVE_API_BASE to run live API Server eval",
			}},
			Evidence: map[string]any{
				"required_env": []string{"NIUMA_EVAL_LIVE_API_BASE"},
			},
		}}
		return finishReport(report, started, opts)
	}

	endpoints := []liveEndpoint{
		{ID: "health", Path: "/health", Type: "server", WantStatus: http.StatusOK},
		{ID: "tenant_sessions", Path: "/tenant/sessions?limit=1", Type: "tenant", WantStatus: http.StatusOK, TenantHeaders: true, APIToken: true},
		{ID: "mobile_sessions", Path: "/mobile/chat/sessions?limit=1", Type: "mobile", WantStatus: http.StatusOK, TenantHeaders: true, APIToken: true},
		{ID: "trace_tenant_sessions", Path: "/trace/api/sessions?source=tenant&limit=1", Type: "trace", WantStatus: http.StatusOK, TenantHeaders: true, APIToken: true},
	}
	report.Total = len(endpoints)
	report.Cases = make([]CaseResult, 0, len(endpoints))
	client := &http.Client{Timeout: 10 * time.Second}
	for _, endpoint := range endpoints {
		result := runLiveEndpoint(ctx, client, baseURL, endpoint)
		report.Cases = append(report.Cases, result)
		switch result.Status {
		case "passed":
			report.Passed++
		case "skipped":
			report.Skipped++
			if report.Status == "passed" {
				report.Status = "skipped"
			}
		default:
			report.Failed++
			report.Status = "failed"
			if opts.FailFast {
				return finishReport(report, started, opts)
			}
		}
	}
	if report.Failed == 0 && report.Passed > 0 {
		report.Status = "passed"
	}
	return finishReport(report, started, opts)
}

func runAnthropicThinkingProfile(ctx context.Context, opts Options, started time.Time) (Report, error) {
	report := Report{
		SuiteID:     "niuma-agent-eval-anthropic-thinking",
		Status:      "passed",
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
		Total:       1,
		Environment: Environment{CWD: opts.CWD, Suite: opts.Suite, Dataset: opts.Dataset, Mode: "live-anthropic-thinking"},
	}
	cfg := config.LoadForCWD(opts.CWD)
	if cfg.ValidateProviderRoute() != nil || config.ResolveModel(opts.CWD, "") == "" || !config.ProviderKindAnthropic(cfg.Provider) {
		report.Status = "skipped"
		report.Skipped = 1
		report.Cases = []CaseResult{{
			ID:     "anthropic_thinking_signature",
			Type:   "anthropic_thinking",
			Status: "skipped",
			Checks: []CheckResult{{
				Name:   "settings.provider",
				Status: "skipped",
				Detail: "configure a Messages-compatible provider, endpoint, credentials and model in ~/.niuma/settings.json",
			}},
			Evidence: map[string]any{
				"required_settings": []string{"provider", "baseURL", "apiKey or authToken", "model"},
			},
		}}
		return finishReport(report, started, opts)
	}
	result := runAnthropicThinkingCase(ctx, opts)
	report.Cases = []CaseResult{result}
	switch result.Status {
	case "passed":
		report.Passed = 1
	case "skipped":
		report.Skipped = 1
		report.Status = "skipped"
	default:
		report.Failed = 1
		report.Status = "failed"
	}
	return finishReport(report, started, opts)
}

func runLiveAgentAPIProfile(ctx context.Context, opts Options, started time.Time) (Report, error) {
	baseURL := strings.TrimSpace(firstEnv("NIUMA_EVAL_LIVE_API_BASE"))
	report := Report{
		SuiteID:     "niuma-agent-eval-live-agent-api",
		Status:      "passed",
		StartedAt:   started.UTC().Format(time.RFC3339Nano),
		Total:       1,
		Environment: Environment{CWD: opts.CWD, Suite: opts.Suite, Dataset: opts.Dataset, Mode: "live-agent-api-server"},
	}
	if baseURL == "" {
		report.Status = "skipped"
		report.Skipped = 1
		report.Cases = []CaseResult{{
			ID:     "live_agent_task_api",
			Type:   "live_agent_api",
			Status: "skipped",
			Checks: []CheckResult{{
				Name:   "env.api_base",
				Status: "skipped",
				Detail: "set NIUMA_EVAL_LIVE_API_BASE to run live agent task API eval",
			}},
			Evidence: map[string]any{
				"required_env": []string{"NIUMA_EVAL_LIVE_API_BASE"},
			},
		}}
		return finishReport(report, started, opts)
	}

	result := runLiveAgentAPICase(ctx, &http.Client{Timeout: 15 * time.Second}, baseURL)
	report.Cases = []CaseResult{result}
	switch result.Status {
	case "passed":
		report.Passed = 1
	case "skipped":
		report.Skipped = 1
		report.Status = "skipped"
	default:
		report.Failed = 1
		report.Status = "failed"
	}
	return finishReport(report, started, opts)
}

func runLiveAgentAPICase(ctx context.Context, client *http.Client, baseURL string) CaseResult {
	started := time.Now()
	traceID := "eval-live-agent-" + strconv.FormatInt(started.UnixNano(), 36)
	result := CaseResult{
		ID:       "live_agent_task_api",
		Type:     "live_agent_api",
		Status:   "passed",
		Evidence: map[string]any{"trace_id": traceID, "base_url": strings.TrimRight(baseURL, "/")},
	}

	createBody := map[string]any{
		"agent_name":           "live-e2e",
		"description":          "live mysql multi-agent e2e",
		"prompt":               "verify live agent task persistence",
		"model":                "eval-model",
		"subagent_session_key": "eval-" + traceID,
		"metadata_json":        map[string]any{"source": "agent_eval_live", "profile": "live-agent-api"},
		"trace_id":             traceID,
	}
	var created struct {
		ID uint64 `json:"id"`
	}
	createStatus, createPreview, err := liveAgentJSON(ctx, client, http.MethodPost, baseURL, "/tenant/agent-tasks", createBody, &created)
	addCheck(&result, "agent_task.create", err == nil && createStatus == http.StatusOK && created.ID > 0, fmt.Sprintf("status=%d id=%d err=%s", createStatus, created.ID, errorDetail(err)))
	result.Evidence["create_status"] = createStatus
	result.Evidence["create_body_preview"] = createPreview
	if err != nil || createStatus != http.StatusOK || created.ID == 0 {
		return failLiveAgentResult(result, started, err)
	}
	result.Evidence["task_id"] = created.ID

	var messageResp struct {
		ID     uint64 `json:"id"`
		TaskID uint64 `json:"task_id"`
	}
	messageStatus, messagePreview, err := liveAgentJSON(ctx, client, http.MethodPost, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d/message", created.ID), map[string]any{
		"from_agent": "coordinator",
		"content":    "please continue",
		"trace_id":   traceID,
	}, &messageResp)
	addCheck(&result, "agent_task.message", err == nil && messageStatus == http.StatusOK && messageResp.ID > 0 && messageResp.TaskID == created.ID, fmt.Sprintf("status=%d event_id=%d task_id=%d err=%s", messageStatus, messageResp.ID, messageResp.TaskID, errorDetail(err)))
	result.Evidence["message_status"] = messageStatus
	result.Evidence["message_body_preview"] = messagePreview
	if err != nil || messageStatus != http.StatusOK || messageResp.ID == 0 || messageResp.TaskID != created.ID {
		return failLiveAgentResult(result, started, err)
	}

	var beforeCancel mysqlstore.AgentTask
	getStatus, getPreview, err := liveAgentJSON(ctx, client, http.MethodGet, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d", created.ID), nil, &beforeCancel)
	addCheck(&result, "agent_task.get.running", err == nil && getStatus == http.StatusOK && beforeCancel.ID == created.ID && beforeCancel.Status == agenttasks.StatusRunning, fmt.Sprintf("status=%d id=%d state=%s err=%s", getStatus, beforeCancel.ID, beforeCancel.Status, errorDetail(err)))
	result.Evidence["get_running_status"] = getStatus
	result.Evidence["get_running_body_preview"] = getPreview
	if err != nil || getStatus != http.StatusOK || beforeCancel.ID != created.ID || beforeCancel.Status != agenttasks.StatusRunning {
		return failLiveAgentResult(result, started, err)
	}

	var eventsBefore liveAgentTaskEventsResponse
	eventsStatus, eventsPreview, err := liveAgentJSON(ctx, client, http.MethodGet, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d/events?limit=20", created.ID), nil, &eventsBefore)
	eventTypesBefore := liveAgentEventTypes(eventsBefore.Data)
	addCheck(&result, "agent_task.events.started", err == nil && eventsStatus == http.StatusOK && contains(eventTypesBefore, agenttasks.EventStarted), fmt.Sprintf("status=%d events=%v err=%s", eventsStatus, eventTypesBefore, errorDetail(err)))
	addCheck(&result, "agent_task.events.message", err == nil && eventsStatus == http.StatusOK && contains(eventTypesBefore, agenttasks.EventMessage), fmt.Sprintf("status=%d events=%v err=%s", eventsStatus, eventTypesBefore, errorDetail(err)))
	result.Evidence["events_before_cancel_status"] = eventsStatus
	result.Evidence["events_before_cancel"] = eventTypesBefore
	result.Evidence["events_before_cancel_body_preview"] = eventsPreview
	if err != nil || eventsStatus != http.StatusOK || !contains(eventTypesBefore, agenttasks.EventStarted) || !contains(eventTypesBefore, agenttasks.EventMessage) {
		return failLiveAgentResult(result, started, err)
	}

	var cancelResp struct {
		ID        uint64 `json:"id"`
		Cancelled bool   `json:"cancelled"`
		InProcess bool   `json:"in_process"`
	}
	cancelStatus, cancelPreview, err := liveAgentJSON(ctx, client, http.MethodPost, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d/cancel", created.ID), nil, &cancelResp)
	addCheck(&result, "agent_task.cancel", err == nil && cancelStatus == http.StatusOK && cancelResp.ID == created.ID && cancelResp.Cancelled, fmt.Sprintf("status=%d id=%d cancelled=%v err=%s", cancelStatus, cancelResp.ID, cancelResp.Cancelled, errorDetail(err)))
	result.Evidence["cancel_status"] = cancelStatus
	result.Evidence["cancel_in_process"] = cancelResp.InProcess
	result.Evidence["cancel_body_preview"] = cancelPreview
	if err != nil || cancelStatus != http.StatusOK || cancelResp.ID != created.ID || !cancelResp.Cancelled {
		return failLiveAgentResult(result, started, err)
	}

	var afterCancel mysqlstore.AgentTask
	getCancelledStatus, getCancelledPreview, err := liveAgentJSON(ctx, client, http.MethodGet, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d", created.ID), nil, &afterCancel)
	addCheck(&result, "agent_task.get.cancelled", err == nil && getCancelledStatus == http.StatusOK && afterCancel.ID == created.ID && afterCancel.Status == agenttasks.StatusCancelled, fmt.Sprintf("status=%d id=%d state=%s err=%s", getCancelledStatus, afterCancel.ID, afterCancel.Status, errorDetail(err)))
	result.Evidence["get_cancelled_status"] = getCancelledStatus
	result.Evidence["get_cancelled_body_preview"] = getCancelledPreview
	if err != nil || getCancelledStatus != http.StatusOK || afterCancel.ID != created.ID || afterCancel.Status != agenttasks.StatusCancelled {
		return failLiveAgentResult(result, started, err)
	}

	var eventsAfter liveAgentTaskEventsResponse
	eventsAfterStatus, eventsAfterPreview, err := liveAgentJSON(ctx, client, http.MethodGet, baseURL, fmt.Sprintf("/tenant/agent-tasks/%d/events?limit=20", created.ID), nil, &eventsAfter)
	eventTypesAfter := liveAgentEventTypes(eventsAfter.Data)
	addCheck(&result, "agent_task.events.cancelled", err == nil && eventsAfterStatus == http.StatusOK && contains(eventTypesAfter, agenttasks.EventCancelled), fmt.Sprintf("status=%d events=%v err=%s", eventsAfterStatus, eventTypesAfter, errorDetail(err)))
	result.Evidence["events_after_cancel_status"] = eventsAfterStatus
	result.Evidence["events_after_cancel"] = eventTypesAfter
	result.Evidence["events_after_cancel_body_preview"] = eventsAfterPreview
	if err != nil || eventsAfterStatus != http.StatusOK || !contains(eventTypesAfter, agenttasks.EventCancelled) {
		return failLiveAgentResult(result, started, err)
	}

	var tasks liveAgentTasksResponse
	listStatus, listPreview, err := liveAgentJSON(ctx, client, http.MethodGet, baseURL, "/tenant/agent-tasks?limit=20", nil, &tasks)
	listContainsTask := liveAgentTasksContain(tasks.Data, created.ID, agenttasks.StatusCancelled)
	addCheck(&result, "agent_task.list", err == nil && listStatus == http.StatusOK && listContainsTask, fmt.Sprintf("status=%d found=%v err=%s", listStatus, listContainsTask, errorDetail(err)))
	result.Evidence["list_status"] = listStatus
	result.Evidence["list_count"] = len(tasks.Data)
	result.Evidence["list_body_preview"] = listPreview
	if err != nil || listStatus != http.StatusOK || !listContainsTask {
		return failLiveAgentResult(result, started, err)
	}

	verifyLiveAgentMySQL(ctx, &result, created.ID, traceID)
	result.DurationMS = time.Since(started).Milliseconds()
	for _, check := range result.Checks {
		if check.Status == "failed" {
			result.Status = "failed"
			break
		}
	}
	sortChecks(result.Checks)
	return result
}

type liveAgentTasksResponse struct {
	Data []mysqlstore.AgentTask `json:"data"`
}

type liveAgentTaskEventsResponse struct {
	Data []mysqlstore.AgentTaskEvent `json:"data"`
}

func liveAgentJSON(ctx context.Context, client *http.Client, method, baseURL, path string, body any, out any) (int, string, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, liveEndpointURL(baseURL, path), reqBody)
	if err != nil {
		return 0, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	applyLiveTenantHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	preview := strings.TrimSpace(string(data))
	if len(preview) > 2048 {
		preview = preview[:2048]
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, preview, fmt.Errorf("unexpected http status %d: %s", resp.StatusCode, preview)
	}
	if out != nil && len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, preview, err
		}
	}
	return resp.StatusCode, preview, nil
}

func applyLiveTenantHeaders(req *http.Request) {
	if token := strings.TrimSpace(firstEnv("NIUMA_EVAL_LIVE_AUTH_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-Tenant-Key", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_TENANT_KEY"), "yutang"))
	req.Header.Set("X-User-Id", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_USER_KEY"), "eval-user"))
	req.Header.Set("X-Device-Id", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_DEVICE_ID"), "agent-eval-live"))
	req.Header.Set("X-Trace-Id", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_TRACE_ID"), "agent-eval-live"))
}

func failLiveAgentResult(result CaseResult, started time.Time, err error) CaseResult {
	result.Status = "failed"
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
	}
	sortChecks(result.Checks)
	return result
}

func liveAgentEventTypes(events []mysqlstore.AgentTaskEvent) []string {
	out := make([]string, 0, len(events))
	for _, event := range events {
		out = append(out, event.EventType)
	}
	return out
}

func liveAgentTasksContain(tasks []mysqlstore.AgentTask, taskID uint64, status string) bool {
	for _, task := range tasks {
		if task.ID == taskID && task.Status == status {
			return true
		}
	}
	return false
}

func verifyLiveAgentMySQL(ctx context.Context, result *CaseResult, taskID uint64, traceID string) {
	dsn := strings.TrimSpace(firstEnv("NIUMA_EVAL_LIVE_MYSQL_DSN", "NIUMA_MYSQL_DSN", "MYSQL_DSN"))
	if dsn == "" {
		result.Evidence["mysql_verification"] = "skipped: set NIUMA_EVAL_LIVE_MYSQL_DSN or NIUMA_MYSQL_DSN for direct persistence checks"
		return
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		addCheck(result, "mysql.open", false, err.Error())
		return
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		addCheck(result, "mysql.ping", false, err.Error())
		return
	}
	var status, agentName, storedTraceID string
	err = db.QueryRowContext(ctx, `SELECT status, COALESCE(agent_name, ''), COALESCE(trace_id, '') FROM tenant_agent_tasks WHERE id = ? LIMIT 1`, taskID).Scan(&status, &agentName, &storedTraceID)
	addCheck(result, "mysql.task_row", err == nil && status == agenttasks.StatusCancelled && agentName == "live-e2e" && storedTraceID == traceID, fmt.Sprintf("status=%s agent=%s trace=%s err=%s", status, agentName, storedTraceID, errorDetail(err)))
	if err != nil {
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT event_type FROM tenant_agent_task_events WHERE task_id = ? ORDER BY created_at ASC LIMIT 20`, taskID)
	if err != nil {
		addCheck(result, "mysql.event_rows", false, err.Error())
		return
	}
	defer rows.Close()
	var eventTypes []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			addCheck(result, "mysql.event_rows", false, err.Error())
			return
		}
		eventTypes = append(eventTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		addCheck(result, "mysql.event_rows", false, err.Error())
		return
	}
	result.Evidence["mysql_events"] = eventTypes
	addCheck(result, "mysql.event_rows", contains(eventTypes, agenttasks.EventStarted) && contains(eventTypes, agenttasks.EventMessage) && contains(eventTypes, agenttasks.EventCancelled), fmt.Sprintf("events=%v", eventTypes))
}

func runAnthropicThinkingCase(ctx context.Context, opts Options) CaseResult {
	started := time.Now()
	result := CaseResult{ID: "anthropic_thinking_signature", Type: "anthropic_thinking", Status: "passed", Evidence: map[string]any{}}
	cfg := config.LoadForCWD(opts.CWD)
	model := config.ResolveModel(opts.CWD, "")
	client := anthropic.NewClient(cfg)
	var sawThinking, sawSignature bool
	request := anthropic.MessagesRequest{
		Model:     model,
		MaxTokens: 2048,
		System:    "Return a very short answer. This is a live eval for thinking block compatibility.",
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "Reply with exactly: thinking live ok"}},
		}},
		Thinking: anthropic.ThinkingConfigFromEffort("low", 2048),
	}
	result.Evidence["model"] = model
	result.Evidence["base_url"] = cfg.BaseURL
	result.Evidence["thinking"] = request.Thinking
	res, err := client.StreamMessages(ctx, request, anthropic.StreamCallbacks{
		OnThinking: func(text string) error {
			if strings.TrimSpace(text) != "" {
				sawThinking = true
			}
			return nil
		},
		OnSignature: func(signature string) error {
			if strings.TrimSpace(signature) != "" {
				sawSignature = true
			}
			return nil
		},
	})
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		addCheck(&result, "anthropic.request", false, err.Error())
		return result
	}
	if res == nil {
		result.Status = "failed"
		result.Error = "empty Anthropic response"
		addCheck(&result, "anthropic.response", false, result.Error)
		return result
	}
	for _, block := range res.Message.Content {
		if block.Type == "thinking" && strings.TrimSpace(block.Thinking) != "" {
			sawThinking = true
		}
		if block.Type == "thinking" && strings.TrimSpace(block.Signature) != "" {
			sawSignature = true
		}
	}
	result.Evidence["stop_reason"] = res.StopReason
	result.Evidence["usage"] = res.Usage
	result.Evidence["content_blocks"] = contentBlockTypes(res.Message.Content)
	addCheck(&result, "thinking.config", request.Thinking != nil && request.Thinking.BudgetTokens >= 1024, "effort low maps to enabled thinking config")
	addCheck(&result, "thinking.delta_or_block", sawThinking, "Anthropic returned thinking content for enabled thinking request")
	addCheck(&result, "thinking.signature", sawSignature, "Anthropic returned proprietary thinking signature")
	addCheck(&result, "anthropic.response", len(res.Message.Content) > 0, "Anthropic returned content blocks")
	return result
}

type liveEndpoint struct {
	ID            string
	Path          string
	Type          string
	WantStatus    int
	TenantHeaders bool
	APIToken      bool
}

func runLiveEndpoint(ctx context.Context, client *http.Client, baseURL string, endpoint liveEndpoint) CaseResult {
	started := time.Now()
	result := CaseResult{ID: endpoint.ID, Type: endpoint.Type, Status: "passed", Evidence: map[string]any{}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, liveEndpointURL(baseURL, endpoint.Path), nil)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		addCheck(&result, "request.build", false, err.Error())
		return result
	}
	if endpoint.APIToken {
		if token := strings.TrimSpace(firstEnv("NIUMA_EVAL_LIVE_AUTH_TOKEN")); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	if endpoint.TenantHeaders {
		req.Header.Set("X-Tenant-Key", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_TENANT_KEY"), "yutang"))
		req.Header.Set("X-User-Id", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_USER_KEY"), "eval-user"))
		req.Header.Set("X-Device-Id", firstNonEmpty(firstEnv("NIUMA_EVAL_LIVE_DEVICE_ID"), "agent-eval-live"))
	}
	resp, err := client.Do(req)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.DurationMS = time.Since(started).Milliseconds()
		addCheck(&result, "http.request", false, err.Error())
		return result
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result.DurationMS = time.Since(started).Milliseconds()
	result.Evidence["url"] = req.URL.String()
	result.Evidence["status_code"] = resp.StatusCode
	result.Evidence["body_preview"] = strings.TrimSpace(string(body))
	addCheck(&result, "http.status", resp.StatusCode == endpoint.WantStatus, fmt.Sprintf("got %d want %d", resp.StatusCode, endpoint.WantStatus))
	return result
}

func finishReport(report Report, started time.Time, opts Options) (Report, error) {
	finished := time.Now()
	if !opts.FixedTime.IsZero() {
		finished = opts.FixedTime.Add(time.Duration(len(report.Cases)) * time.Millisecond)
	}
	report.FinishedAt = finished.UTC().Format(time.RFC3339Nano)
	report.DurationMS = finished.Sub(started).Milliseconds()
	if opts.Output != "" {
		if err := WriteReport(report, opts.Output, opts.Format); err != nil {
			return report, err
		}
	}
	if report.Failed > 0 {
		return report, fmt.Errorf("agent eval failed: %d/%d cases failed", report.Failed, report.Total)
	}
	return report, nil
}

func liveEndpointURL(baseURL, endpointPath string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return baseURL + endpointPath
	}
	return baseURL + endpointPath
}

func contentBlockTypes(blocks []anthropic.ContentBlock) []string {
	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, block.Type)
	}
	return out
}

func DefaultSuite() Suite {
	return Suite{
		ID:          "niuma-agent-eval-p1",
		Description: "Deterministic P1 agent regression suite for query, tools, skills, sub-agents, compaction, permissions, and trace evidence.",
		Cases: []Case{
			{ID: "chat_basic", Type: "query", Prompt: "Say hello from eval.", ExpectContains: []string{"hello"}},
			{ID: "tool_read", Type: "tool", Prompt: "Read eval_target.txt.", ExpectContains: []string{"EVAL_TARGET"}, ExpectToolCalls: []string{"Read"}},
			{ID: "skill_load", Type: "skill", Prompt: "Load the eval-skill skill.", ExpectContains: []string{"Eval skill instructions"}, ExpectToolCalls: []string{"Skill"}},
			{ID: "subagent_task", Type: "subagent", Prompt: "Delegate a small review task.", ExpectContains: []string{"subagent complete"}, ExpectToolCalls: []string{"Task"}},
			{ID: "auto_compact", Type: "compact", Prompt: "Continue after compact.", ExpectContains: []string{"compact ok"}},
			{ID: "permission_boundary", Type: "permission", Prompt: "Try a denied shell command.", ExpectToolCalls: []string{"Bash"}},
			{ID: "trace_observability", Type: "trace", Prompt: "Produce trace evidence.", ExpectContains: []string{"trace ok"}},
			{ID: "workflow_closure", Type: "workflow_closure", Prompt: "Update docs/schema/index.md after adding a field.", ExpectContains: []string{"workflow closure ok"}},
			{ID: "subagent_multiagent_parity", Type: "subagent_parity", Prompt: "Run subagent parity checks."},
			{ID: "multiagent_e2e", Type: "multiagent_e2e", Prompt: "Coordinate a background teammate end to end.", ExpectContains: []string{"multiagent e2e ok"}, ExpectToolCalls: []string{"AgentCreate", "AgentMessage", "AgentGet", "AgentStop"}},
		},
	}
}

func loadSuite(path string) (Suite, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultSuite(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		return Suite{}, err
	}
	if strings.TrimSpace(suite.ID) == "" {
		return Suite{}, fmt.Errorf("eval dataset id is required")
	}
	if len(suite.Cases) == 0 {
		return Suite{}, fmt.Errorf("eval dataset must contain at least one case")
	}
	return suite, nil
}

func runCase(ctx context.Context, opts Options, testCase Case) CaseResult {
	started := time.Now()
	result := CaseResult{ID: testCase.ID, Type: testCase.Type, Status: "passed", Evidence: map[string]any{}}
	caseCtx := observability.WithRequestValues(ctx, "eval-"+testCase.ID, "eval-user", "eval-tenant")
	workspace, cleanup, err := prepareWorkspace(opts.CWD, testCase.ID)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer cleanup()
	capture := &telemetryCapture{}
	restoreTelemetry := withTelemetryCapture(capture)
	defer restoreTelemetry()

	switch testCase.Type {
	case "subagent_parity":
		result = runSubagentParityCase(caseCtx, workspace, testCase, result)
	case "permission":
		result = runPermissionCase(caseCtx, workspace, testCase, result)
	case "compact":
		result = runCompactCase(caseCtx, workspace, testCase, result)
	case "git_commit_push", "git_conflict_recovery":
		result = runGitWorkflowCase(caseCtx, workspace, testCase, result)
	default:
		result = runQueryCase(caseCtx, workspace, testCase, result)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	result.Evidence["telemetry_events"] = capture.names()
	result.Evidence["trace_id"] = "eval-" + testCase.ID
	if testCase.Type != "subagent_parity" {
		addCheck(&result, "trace_id", hasTelemetryTrace(capture.events, "eval-"+testCase.ID), "telemetry carries eval trace id")
	}
	if result.Status == "failed" {
		sortChecks(result.Checks)
		return result
	}
	for _, check := range result.Checks {
		if check.Status == "failed" {
			result.Status = "failed"
			break
		}
	}
	sortChecks(result.Checks)
	return result
}

func runQueryCase(ctx context.Context, workspace string, testCase Case, result CaseResult) CaseResult {
	if testCase.Type == "multiagent_e2e" {
		if err := writeEvalAgent(workspace, "e2e", "background: true\nmaxTurns: 2\n", "Handle e2e work."); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			return result
		}
	}
	if testCase.Type == "workflow_closure" {
		if err := writeWorkflowClosureFixture(workspace); err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			return result
		}
	}
	restoreEnv := func() {}
	if testCase.Type == "workflow_closure" {
		restoreEnv = setEnvForCase("NIUMA_FEATURE_ENHANCED_SYSTEM_PROMPT", "true")
	}
	defer restoreEnv()
	streamer := newScriptedStreamer(testCase.Type)
	store := &memoryTaskStore{}
	controller := agenttasks.NewController()
	registry := evalRegistry(streamer, store, controller)
	recorder, err := session.DefaultStore().NewRecorder(workspace)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	defer recorder.Close()
	maxTurns := 4
	if testCase.Type == "multiagent_e2e" {
		maxTurns = 5
	}
	querySession := query.New(streamer, registry, query.Options{
		Model:           "eval-model",
		MaxTurns:        maxTurns,
		CWD:             workspace,
		Recorder:        recorder,
		TaskStore:       store,
		TaskController:  controller,
		TraceID:         observability.TraceID(ctx),
		ToolResultLimit: 32 * 1024,
	})
	response, err := querySession.Run(ctx, firstNonEmpty(testCase.Prompt, "run eval"), io.Discard)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	result.Evidence["response"] = response.Response
	result.Evidence["tool_calls"] = toolNames(response.ToolCalls)
	result.Evidence["session_id"] = response.SessionID
	result.Evidence["transcript_path"] = response.TranscriptPath
	result.Evidence["usage"] = response.Usage
	result.Evidence["agent_tasks"] = len(store.tasks)
	metrics := collectCaseMetrics(response)
	result.Metrics = &metrics
	addBudgetChecks(&result, metrics, testCase)
	addContainsChecks(&result, response.Response, testCase.ExpectContains)
	addToolChecks(&result, response.ToolCalls, testCase.ExpectToolCalls)
	addCheck(&result, "query.completed", err == nil, errorDetail(err))
	addCheck(&result, "session.transcript", response.SessionID != "" && response.TranscriptPath != "", "recorder created session transcript")
	if testCase.Type == "subagent" {
		addCheck(&result, "agent.task.created", len(store.tasks) > 0, "sub-agent task store recorded a task")
		addCheck(&result, "agent.task.events", len(store.events) > 0, "sub-agent task store recorded lifecycle events")
	}
	if testCase.Type == "multiagent_e2e" {
		messageEvent := store.hasEvent(agenttasks.EventMessage)
		messageTerminalError := hasToolErrorContaining(response.ToolCalls, "AgentMessage", "only send to running")
		addCheck(&result, "agent.message.truthful", messageEvent || messageTerminalError, "AgentMessage either reached a running task or reported terminal task state truthfully")
		addCheck(&result, "agent.cancelled", store.hasStatus(agenttasks.StatusCancelled), "AgentStop persisted cancellation")
		result.Evidence["agent_tasks"] = len(store.listTasks())
		result.Evidence["agent_events"] = len(store.listEvents())
	}
	if testCase.Type == "trace" {
		addCheck(&result, "telemetry.model", true, "checked after telemetry capture")
	}
	if testCase.Type == "workflow_closure" {
		addCheck(&result, "system.workflow_closure", streamer.sawAll([]string{"# Workflow Closure", "source of truth", "git diff --name-only"}), "system prompt included workflow closure guidance")
		addCheck(&result, "context.workflow_contract", streamer.sawAll([]string{"Workflow Contract", "Source Of Truth", "Derived Files"}), "workflow contract loaded into prompt context")
	}
	return result
}

func runCompactCase(ctx context.Context, workspace string, testCase Case, result CaseResult) CaseResult {
	streamer := newScriptedStreamer(testCase.Type)
	store := &memoryTaskStore{}
	querySession := query.New(streamer, evalRegistry(streamer, store, nil), query.Options{
		Model:    "eval-model",
		MaxTurns: 2,
		CWD:      workspace,
		TraceID:  observability.TraceID(ctx),
		InitialMessages: []anthropic.MessageParam{
			textMessage("user", strings.Repeat("old context ", 40)),
			textMessage("assistant", "old answer"),
			textMessage("user", strings.Repeat("more context ", 40)),
			textMessage("assistant", "more answer"),
		},
		AutoCompact: compact.Config{
			Enabled:               true,
			DefaultThresholdRatio: 0.05,
			PreserveRecentRounds:  1,
			ModelContext:          map[string]int{"eval-model": 200},
			MaxSummaryTokens:      512,
		},
	})
	response, err := querySession.Run(ctx, firstNonEmpty(testCase.Prompt, "compact"), io.Discard)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	result.Evidence["response"] = response.Response
	result.Evidence["stream_requests"] = streamer.calls
	result.Evidence["summary_requests"] = streamer.summaryCalls
	addContainsChecks(&result, response.Response, testCase.ExpectContains)
	addCheck(&result, "compact.triggered", streamer.summaryCalls > 0, "summary model was invoked before final response")
	addCheck(&result, "query.completed", err == nil, errorDetail(err))
	return result
}

func runPermissionCase(ctx context.Context, workspace string, testCase Case, result CaseResult) CaseResult {
	streamer := newScriptedStreamer(testCase.Type)
	policy := permissions.Policy{Deny: []string{"Bash"}, DefaultMode: "allow"}
	registry := tools.NewRegistry(tools.Guard(bash.New(), policy))
	querySession := query.New(streamer, registry, query.Options{
		Model:    "eval-model",
		MaxTurns: 2,
		CWD:      workspace,
		TraceID:  observability.TraceID(ctx),
	})
	response, err := querySession.Run(ctx, firstNonEmpty(testCase.Prompt, "permission"), io.Discard)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	result.Evidence["response"] = response.Response
	result.Evidence["tool_calls"] = toolNames(response.ToolCalls)
	addToolChecks(&result, response.ToolCalls, testCase.ExpectToolCalls)
	addCheck(&result, "permission.denied", len(response.ToolCalls) > 0 && response.ToolCalls[0].IsError, "Bash was denied by policy")
	addCheck(&result, "query.completed", err == nil, errorDetail(err))
	return result
}

func runSubagentParityCase(ctx context.Context, workspace string, testCase Case, result CaseResult) CaseResult {
	_ = testCase
	if err := os.MkdirAll(filepath.Join(workspace, ".claude", "agents"), 0755); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	agentBody := `---
name: parity
tools: [Read]
skills: [eval-skill]
memory: project
background: true
permissionMode: ask
maxTurns: 2
---
Use all parity context.`
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "agents", "parity.md"), []byte(agentBody), 0644); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".claude", "agent-memory", "parity"), 0755); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	if err := os.WriteFile(filepath.Join(workspace, ".claude", "agent-memory", "parity", "MEMORY.md"), []byte("parity agent memory"), 0644); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	streamer := newScriptedStreamer("subagent_parity")
	store := &memoryTaskStore{}
	controller := agenttasks.NewController()
	hookRunner := hooks.New(map[string][]config.HookCommand{
		hooks.SubagentStart: {{Command: `cat > ` + strconv.Quote(filepath.Join(workspace, "parity-start.json"))}},
		hooks.SubagentStop:  {{Command: `cat > ` + strconv.Quote(filepath.Join(workspace, "parity-stop.json"))}},
	})
	traceID := observability.TraceID(ctx)
	registry := tools.NewRegistry(fileread.New())
	taskTool := task.New(streamer, "eval-model", task.WithRegistry(registry), task.WithTaskStore(store), task.WithController(controller), task.WithHooks(hookRunner))
	toolContext := tools.Context{CWD: workspace, TaskStore: store, TaskController: controller, TraceID: traceID}
	taskResult := taskTool.Run(ctx, json.RawMessage(`{"description":"parity","prompt":"check parity","subagent_type":"parity"}`), toolContext)
	result.Evidence["task_result"] = taskResult.Content
	addCheck(&result, "background.task_started", !taskResult.IsError && strings.Contains(taskResult.Content, `"background":true`), "Task returned background handle")
	waitStoreFinished(store)
	addCheck(&result, "background.task_finished", store.finishedCount() > 0, "background sub-agent finished through task store")
	addCheck(&result, "hooks.start", waitFileContains(filepath.Join(workspace, "parity-start.json"), `"event":"SubagentStart"`), "SubagentStart hook emitted")
	addCheck(&result, "hooks.stop", waitFileContains(filepath.Join(workspace, "parity-stop.json"), `"last_assistant_message":"parity subagent complete"`), "SubagentStop hook carried last assistant message")
	addCheck(&result, "context.skills_memory", streamer.sawAll([]string{"Eval skill instructions", "parity agent memory"}), "sub-agent system included skill and agent memory")

	agentRegistry := tools.NewRegistry()
	createRes := agenttool.NewCreate(streamer, "eval-model", agentRegistry).Run(ctx, json.RawMessage(`{"description":"agent create","prompt":"check parity","subagent_type":"parity"}`), toolContext)
	addCheck(&result, "agent.create", !createRes.IsError && strings.Contains(createRes.Content, `"task_id"`), "AgentCreate returned task handle")
	waitStoreFinishedCount(store, 2)
	listRes := agenttool.NewList().Run(ctx, json.RawMessage(`{"limit":10}`), toolContext)
	addCheck(&result, "agent.list", !listRes.IsError && strings.Contains(listRes.Content, `"agent_name": "parity"`), "AgentList returned parity task")
	getRes := agenttool.NewGet().Run(ctx, json.RawMessage(`{"task_id":1}`), toolContext)
	addCheck(&result, "agent.get", !getRes.IsError && strings.Contains(getRes.Content, `"id": 1`), "AgentGet returned first task")
	stopRes := agenttool.NewStop().Run(ctx, json.RawMessage(`{"task_id":1,"reason":"eval"}`), toolContext)
	addCheck(&result, "agent.stop", !stopRes.IsError && strings.Contains(stopRes.Content, `"cancelled": true`), "AgentStop cancelled or marked task")
	addCheck(&result, "trace_id", eventsHaveTrace(store.listEvents(), traceID), "agent task events carry eval trace id")
	result.Evidence["agent_create"] = createRes.Content
	result.Evidence["agent_list"] = listRes.Content
	result.Evidence["agent_stop"] = stopRes.Content
	result.Evidence["agent_tasks"] = len(store.listTasks())
	result.Evidence["agent_events"] = len(store.listEvents())
	return result
}

func evalRegistry(streamer *scriptedStreamer, store agenttasks.Store, controller *agenttasks.Controller) *tools.Registry {
	registry := tools.NewRegistry(
		fileread.New(),
		skill.New(streamer, "eval-model"),
	)
	taskOptions := []task.Option{task.WithRegistry(registry), task.WithTaskStore(store)}
	if controller != nil {
		taskOptions = append(taskOptions, task.WithController(controller))
	}
	registry.Register(task.New(streamer, "eval-model", taskOptions...))
	registry.Register(agenttool.NewCreate(streamer, "eval-model", registry))
	registry.Register(agenttool.NewList())
	registry.Register(agenttool.NewGet())
	registry.Register(agenttool.NewStop())
	registry.Register(agenttool.NewMessage())
	return registry
}

func prepareWorkspace(root, caseID string) (string, func(), error) {
	base, err := os.MkdirTemp("", "niuma-agent-eval-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(base) }
	project := filepath.Join(base, sanitizeName(caseID))
	if err := os.MkdirAll(filepath.Join(project, ".claude", "skills", "eval-skill"), 0755); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(project, "eval_target.txt"), []byte("EVAL_TARGET: deterministic fixture\n"), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	skillBody := "---\nname: eval-skill\ndescription: Eval skill\n---\n# Eval Skill\n\nEval skill instructions.\n"
	if err := os.WriteFile(filepath.Join(project, ".claude", "skills", "eval-skill", "SKILL.md"), []byte(skillBody), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return project, cleanup, nil
}

func writeEvalAgent(workspace, name, frontmatter, prompt string) error {
	agentsDir := filepath.Join(workspace, ".claude", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		return err
	}
	body := "---\nname: " + name + "\n" + frontmatter + "---\n" + prompt + "\n"
	return os.WriteFile(filepath.Join(agentsDir, name+".md"), []byte(body), 0644)
}

func writeWorkflowClosureFixture(workspace string) error {
	files := map[string]string{
		filepath.Join(workspace, "WORKFLOW.md"):                         "# Workflow Contract\n\n## Source Of Truth\n- references/source/*.md owns field definitions.\n\n## Derived Files\n- references/generated/*.md mirrors source definitions.\n\n## Validation\n- Search siblings and inspect git diff --name-only before completion.\n",
		filepath.Join(workspace, "references", "source", "fields.md"):   "field_a: int\nfield_b: int\n",
		filepath.Join(workspace, "references", "generated", "index.md"): "field_a\nfield_b\n",
		filepath.Join(workspace, "docs", "schema", "index.md"):          "field_a\nfield_b\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

type scriptedStreamer struct {
	mu           sync.Mutex
	caseType     string
	calls        int
	mainCalls    int
	summaryCalls int
	systems      []string
	messages     []string
	gitStep      int
}

func newScriptedStreamer(caseType string) *scriptedStreamer {
	return &scriptedStreamer{caseType: caseType}
}

func (s *scriptedStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.systems = append(s.systems, req.System)
	s.messages = append(s.messages, requestMessageText(req.Messages))
	if strings.Contains(req.System, "Conversation summary") || req.MaxTokens <= 512 {
		s.summaryCalls++
		s.mu.Unlock()
		text := compactSummaryText(req.Messages)
		_ = emitText(cb, text)
		return streamText(text, anthropic.Usage{InputTokens: 12, OutputTokens: 6}), nil
	}
	caseType := s.caseType
	if caseType == "multiagent_e2e" && isSubagentRequest(req.System) {
		s.mu.Unlock()
		return streamTextWithCallback(cb, "e2e subagent ready"), nil
	}
	s.mainCalls++
	mainCall := s.mainCalls
	s.mu.Unlock()
	switch caseType {
	case "git_commit_push", "git_conflict_recovery":
		return s.streamGitWorkflowStep(cb, req)
	case "tool":
		if call == 1 {
			return streamTool("toolu_read", "Read", `{"file_path":"eval_target.txt"}`), nil
		}
		return streamTextWithCallback(cb, "EVAL_TARGET read ok"), nil
	case "skill":
		if call == 1 {
			return streamTool("toolu_skill", "Skill", `{"name":"eval-skill"}`), nil
		}
		return streamTextWithCallback(cb, "Eval skill instructions loaded"), nil
	case "subagent":
		if isSubagentRequest(req.System) {
			return streamTextWithCallback(cb, "subagent complete"), nil
		}
		if call == 1 {
			return streamTool("toolu_task", "Task", `{"description":"eval subagent","prompt":"review fixture"}`), nil
		}
		return streamTextWithCallback(cb, "subagent complete"), nil
	case "subagent_parity":
		return streamTextWithCallback(cb, "parity subagent complete"), nil
	case "permission":
		if call == 1 {
			return streamTool("toolu_bash", "Bash", `{"command":"echo denied"}`), nil
		}
		return streamTextWithCallback(cb, "permission boundary ok"), nil
	case "compact":
		return streamTextWithCallback(cb, "compact ok"), nil
	case "trace":
		return streamTextWithCallback(cb, "trace ok"), nil
	case "workflow_closure":
		return streamTextWithCallback(cb, "workflow closure ok"), nil
	case "multiagent_e2e":
		switch mainCall {
		case 1:
			return streamTool("toolu_create", "AgentCreate", `{"description":"e2e","prompt":"start e2e","subagent_type":"e2e"}`), nil
		case 2:
			return streamTool("toolu_message", "AgentMessage", `{"task_id":1,"content":"please continue","from_agent":"coordinator"}`), nil
		case 3:
			return streamTool("toolu_get", "AgentGet", `{"task_id":1}`), nil
		case 4:
			return streamTool("toolu_stop", "AgentStop", `{"task_id":1,"reason":"eval"}`), nil
		default:
			return streamTextWithCallback(cb, "multiagent e2e ok"), nil
		}
	default:
		return streamTextWithCallback(cb, "hello from eval"), nil
	}
}

func (s *scriptedStreamer) sawAll(values []string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	haystack := strings.Join(append(append([]string{}, s.systems...), s.messages...), "\n")
	for _, value := range values {
		if !strings.Contains(haystack, value) {
			return false
		}
	}
	return true
}

func requestMessageText(messages []anthropic.MessageParam) string {
	var parts []string
	for _, message := range messages {
		for _, block := range message.Content {
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func setEnvForCase(key, value string) func() {
	oldValue, hadOld := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	return func() {
		if hadOld {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	}
}

func isSubagentRequest(system string) bool {
	return strings.Contains(system, "sub-agent")
}

func streamTool(id, name, input string) *anthropic.StreamResult {
	return &anthropic.StreamResult{
		Message: anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{
			Type:  "tool_use",
			ID:    id,
			Name:  name,
			Input: json.RawMessage(input),
		}}},
		StopReason: "tool_use",
		Usage:      anthropic.Usage{InputTokens: 10, OutputTokens: 3},
	}
}

func streamTextWithCallback(cb anthropic.StreamCallbacks, text string) *anthropic.StreamResult {
	_ = emitText(cb, text)
	return streamText(text, anthropic.Usage{InputTokens: 8, OutputTokens: 4})
}

func streamText(text string, usage anthropic.Usage) *anthropic.StreamResult {
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: text}}},
		StopReason: "end_turn",
		Usage:      usage,
	}
}

func emitText(cb anthropic.StreamCallbacks, text string) error {
	if cb.OnText == nil {
		return nil
	}
	return cb.OnText(text)
}

func textMessage(role, text string) anthropic.MessageParam {
	return anthropic.MessageParam{Role: role, Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}
}

func compactSummaryText(messages []anthropic.MessageParam) string {
	facts := compactSummaryFacts(messages)
	return strings.Join([]string{
		"## Current Goal",
		"Run deterministic eval compact scenario.",
		"## User Preferences / Constraints",
		"Use local deterministic evidence.",
		"## Decisions Made",
		"Triggered compact summary.",
		"## Files / Code Changed",
		"eval_target.txt",
		"## Commands / Test Results",
		"agent eval",
		"## Open Tasks",
		"None.",
		"## Known Issues / Risks",
		"None.",
		"## Important Raw Facts",
		"eval_target.txt and agent eval were discussed.",
		facts,
	}, "\n")
}

func compactSummaryFacts(messages []anthropic.MessageParam) string {
	var prompt string
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == "text" {
				prompt += "\n" + block.Text
			}
		}
	}
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	start := strings.Index(prompt, "Hard facts extracted by the runtime. Include all relevant items in the summary:")
	if start < 0 {
		return ""
	}
	prompt = prompt[start:]
	end := strings.Index(prompt, "\n\nConversation to compact:")
	if end >= 0 {
		prompt = prompt[:end]
	}
	var facts []string
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			facts = append(facts, line)
		}
	}
	return strings.Join(facts, "\n")
}

type telemetryCapture struct {
	mu     sync.Mutex
	events []telemetry.Event
}

func (c *telemetryCapture) Emit(_ context.Context, event telemetry.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *telemetryCapture) names() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.events))
	for _, event := range c.events {
		out = append(out, event.Name)
	}
	return out
}

func withTelemetryCapture(capture *telemetryCapture) func() {
	return telemetry.SetDefaultEmitter(telemetry.NewEmitter(capture))
}

func hasTelemetryTrace(events []telemetry.Event, traceID string) bool {
	for _, event := range events {
		if event.TraceID == traceID {
			return true
		}
	}
	return false
}

func eventsHaveTrace(events []agenttasks.EventInput, traceID string) bool {
	for _, event := range events {
		if event.TraceID == traceID {
			return true
		}
	}
	return false
}

type memoryTaskStore struct {
	mu       sync.Mutex
	nextID   uint64
	tasks    []agenttasks.TaskInput
	taskRows []mysqlstore.AgentTask
	events   []agenttasks.EventInput
}

func (s *memoryTaskStore) CreateAgentTask(_ context.Context, input agenttasks.TaskInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	if s.nextID == 0 {
		s.nextID = 1
	}
	s.tasks = append(s.tasks, input)
	s.taskRows = append(s.taskRows, mysqlstore.AgentTask{
		ID:                 s.nextID,
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		ParentSessionID:    input.ParentSessionID,
		SubagentSessionKey: input.SubagentSessionKey,
		AgentName:          input.AgentName,
		Description:        input.Description,
		Status:             agenttasks.StatusRunning,
		Model:              input.Model,
		ResultJSON:         input.ResultJSON,
		MetadataJSON:       input.MetadataJSON,
		TraceID:            input.TraceID,
		StartedAt:          time.Now().UTC(),
	})
	return s.nextID, nil
}

func (s *memoryTaskStore) FinishAgentTask(_ context.Context, taskID uint64, status string, resultJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.taskRows {
		if s.taskRows[i].ID == taskID {
			s.taskRows[i].Status = status
			s.taskRows[i].ResultJSON = resultJSON
			s.taskRows[i].FinishedAt = time.Now().UTC()
		}
	}
	return nil
}

func (s *memoryTaskStore) AppendAgentTaskEvent(_ context.Context, input agenttasks.EventInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, input)
	return uint64(len(s.events)), nil
}

func (s *memoryTaskStore) ListAgentTasks(context.Context, int) ([]mysqlstore.AgentTask, error) {
	return s.listTasks(), nil
}

func (s *memoryTaskStore) ListAgentTaskEvents(_ context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mysqlstore.AgentTaskEvent, 0, len(s.events))
	for i, event := range s.events {
		if taskID != 0 && event.TaskID != taskID {
			continue
		}
		out = append(out, mysqlstore.AgentTaskEvent{
			ID:          uint64(i + 1),
			TaskID:      event.TaskID,
			EventType:   event.EventType,
			PayloadJSON: event.PayloadJSON,
			TraceID:     event.TraceID,
			CreatedAt:   time.Now().UTC(),
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *memoryTaskStore) CancelAgentTask(_ context.Context, taskID uint64, resultJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.taskRows {
		if s.taskRows[i].ID == taskID {
			s.taskRows[i].Status = agenttasks.StatusCancelled
			s.taskRows[i].ResultJSON = resultJSON
			s.taskRows[i].FinishedAt = time.Now().UTC()
		}
	}
	return nil
}

func (s *memoryTaskStore) IsAgentTaskCancelled(_ context.Context, taskID uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.taskRows {
		if task.ID == taskID {
			return task.Status == agenttasks.StatusCancelled, nil
		}
	}
	return false, nil
}

func (s *memoryTaskStore) listTasks() []mysqlstore.AgentTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mysqlstore.AgentTask(nil), s.taskRows...)
}

func (s *memoryTaskStore) listEvents() []agenttasks.EventInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agenttasks.EventInput(nil), s.events...)
}

func (s *memoryTaskStore) hasEvent(eventType string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func (s *memoryTaskStore) hasStatus(status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.taskRows {
		if task.Status == status {
			return true
		}
	}
	return false
}

func (s *memoryTaskStore) finishedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, task := range s.taskRows {
		if task.Status == agenttasks.StatusCompleted || task.Status == agenttasks.StatusFailed || task.Status == agenttasks.StatusCancelled {
			count++
		}
	}
	return count
}

func addContainsChecks(result *CaseResult, text string, wants []string) {
	for _, want := range wants {
		addCheck(result, "contains."+want, strings.Contains(text, want), "response contains expected text")
	}
}

func addToolChecks(result *CaseResult, calls []query.ToolTrace, wants []string) {
	names := toolNames(calls)
	for _, want := range wants {
		addCheck(result, "tool."+want, contains(names, want), "expected tool call was observed")
	}
}

func addCheck(result *CaseResult, name string, ok bool, detail string) {
	status := "passed"
	if !ok {
		status = "failed"
		result.Status = "failed"
	}
	result.Checks = append(result.Checks, CheckResult{Name: name, Status: status, Detail: detail})
}

func sortChecks(checks []CheckResult) {
	sort.SliceStable(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
}

func toolNames(calls []query.ToolTrace) []string {
	out := make([]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Name)
	}
	return out
}

func hasToolErrorContaining(calls []query.ToolTrace, toolName, text string) bool {
	for _, call := range calls {
		if call.Name == toolName && call.IsError && strings.Contains(call.Output, text) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitStoreFinished(store *memoryTaskStore) {
	waitStoreFinishedCount(store, 1)
}

func waitStoreFinishedCount(store *memoryTaskStore, want int) {
	deadline := time.After(500 * time.Millisecond)
	for store.finishedCount() < want {
		select {
		case <-deadline:
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func waitFileContains(path, want string) bool {
	deadline := time.After(500 * time.Millisecond)
	for {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return true
		}
		select {
		case <-deadline:
			return false
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func errorDetail(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func normalizeFormat(format, path string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format != "" {
		return format
	}
	if strings.HasSuffix(strings.ToLower(path), ".md") || strings.HasSuffix(strings.ToLower(path), ".markdown") {
		return "markdown"
	}
	return "json"
}

func normalizeProfile(profile string) (string, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		return "local", nil
	}
	switch profile {
	case "local", "deterministic", "deterministic-local":
		return "local", nil
	case "live", "live-api", "api":
		return "live", nil
	case "live-agent-api", "live-agent", "agent-api", "live-mysql-agent":
		return "live-agent-api", nil
	case "anthropic-thinking", "thinking", "live-anthropic-thinking":
		return "anthropic-thinking", nil
	case "golden-probe", "probe":
		return "golden-probe", nil
	default:
		return "", fmt.Errorf("unknown eval profile: %s", profile)
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "case"
	}
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}
