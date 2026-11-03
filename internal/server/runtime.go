package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/konglong87/super-niuma/internal/background"
	"github.com/konglong87/super-niuma/internal/scheduler"
)

type RuntimeBackgroundJob struct {
	ID              string     `json:"id"`
	ScheduleID      string     `json:"schedule_id,omitempty"`
	Prompt          string     `json:"prompt"`
	CWD             string     `json:"cwd"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	PID             int        `json:"pid,omitempty"`
	IntervalSeconds int        `json:"interval_seconds,omitempty"`
	Spec            string     `json:"spec,omitempty"`
	Enabled         bool       `json:"enabled"`
	RunCount        int        `json:"run_count,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	LogPath         string     `json:"log_path,omitempty"`
	LogTail         string     `json:"log_tail,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type RuntimeBackgroundListResponse struct {
	Data []RuntimeBackgroundJob `json:"data"`
}

type RuntimeBackgroundLogsResponse struct {
	ID      string `json:"id"`
	Logs    string `json:"logs"`
	LogTail string `json:"log_tail"`
}

type RuntimeBackgroundStopResponse struct {
	ID         string `json:"id"`
	ScheduleID string `json:"schedule_id,omitempty"`
	Stopped    bool   `json:"stopped"`
	Disabled   bool   `json:"disabled"`
}

type RuntimeLoopRequest struct {
	Prompt          string `json:"prompt"`
	CWD             string `json:"cwd,omitempty"`
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Interval        string `json:"interval,omitempty"`
	Model           string `json:"model,omitempty"`
	MaxTurns        int    `json:"max_turns,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
}

type RuntimeBackgroundEventsResponse struct {
	Data       []scheduler.Event `json:"data"`
	NextOffset int64             `json:"next_offset"`
}

type RuntimeBackgroundRunsResponse struct {
	Data []scheduler.RunRecord `json:"data"`
}

func registerRuntimeRoutes(router *gin.Engine, opts Options) {
	router.Any("/runtime/background", runtimeBackgroundListGin(opts))
	router.Any("/runtime/background/events", runtimeBackgroundEventsGin(opts))
	router.Any("/runtime/background/:id", runtimeBackgroundItemGin(opts))
	router.Any("/runtime/background/:id/logs", runtimeBackgroundLogsGin(opts))
	router.Any("/runtime/background/:id/run", runtimeBackgroundRunGin(opts))
	router.Any("/runtime/background/:id/stop", runtimeBackgroundStopGin(opts))
	router.Any("/runtime/background/:id/runs", runtimeBackgroundRunsGin(opts))
}

func runtimeBackgroundItemGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundItemGin", "update background job")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodPatch && c.Request.Method != http.MethodPut {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		job, err := runtimeUpdateLoop(c.Request.Context(), c.Param("id"), c.Request)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errRuntimeNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, errRuntimeBadRequest) {
				status = http.StatusBadRequest
			}
			http.Error(c.Writer, err.Error(), status)
			return
		}
		writeJSON(c.Writer, job)
	}
}

func runtimeBackgroundListGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundListGin", "list background jobs")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		switch c.Request.Method {
		case http.MethodGet:
			jobs, err := runtimeBackgroundJobs(c.Query("kind"), queryInt(c.Query("tail"), 1200), queryInt(c.Query("limit"), 100))
			if err != nil {
				http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(c.Writer, RuntimeBackgroundListResponse{Data: jobs})
		case http.MethodPost:
			job, err := runtimeCreateLoop(c.Request.Context(), c.Request, opts)
			if err != nil {
				status := http.StatusBadRequest
				if !errors.Is(err, errRuntimeBadRequest) {
					status = http.StatusInternalServerError
				}
				http.Error(c.Writer, err.Error(), status)
				return
			}
			writeJSON(c.Writer, job)
		default:
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}
}

func runtimeBackgroundLogsGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundLogsGin", "read background logs")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodGet {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSpace(c.Param("id"))
		logs, ok, err := background.DefaultStore().Logs(id)
		if err != nil {
			http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			http.Error(c.Writer, "background session not found: "+id, http.StatusNotFound)
			return
		}
		tail := tailText(logs, queryInt(c.Query("tail"), 8000))
		writeJSON(c.Writer, RuntimeBackgroundLogsResponse{ID: id, Logs: logs, LogTail: tail})
	}
}

func runtimeBackgroundStopGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundStopGin", "stop background job")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodPost {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSpace(c.Param("id"))
		scheduleStore := scheduler.DefaultStore()
		schedule, disabled, err := scheduleStore.Disable(id)
		if err != nil {
			http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
			return
		}
		killID := id
		if schedule.BackgroundID != "" {
			killID = schedule.BackgroundID
		}
		stopped, killErr := background.DefaultStore().Kill(killID)
		if killErr != nil {
			http.Error(c.Writer, killErr.Error(), http.StatusInternalServerError)
			return
		}
		if !stopped && !disabled {
			http.Error(c.Writer, "background session not found: "+id, http.StatusNotFound)
			return
		}
		writeJSON(c.Writer, RuntimeBackgroundStopResponse{ID: killID, ScheduleID: schedule.ID, Stopped: stopped, Disabled: disabled})
	}
}

func runtimeBackgroundRunGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundRunGin", "run background job")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodPost {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		job, err := runtimeRunLoopNow(c.Request.Context(), strings.TrimSpace(c.Param("id")))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errRuntimeNotFound) {
				status = http.StatusNotFound
			}
			http.Error(c.Writer, err.Error(), status)
			return
		}
		writeJSON(c.Writer, job)
	}
}

func runtimeBackgroundRunsGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundRunsGin", "list background runs")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodGet {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		store := scheduler.DefaultStore()
		runs, err := store.ListRuns(strings.TrimSpace(c.Param("id")), queryInt(c.Query("limit"), 100))
		if err != nil {
			http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(c.Writer, RuntimeBackgroundRunsResponse{Data: runs})
	}
}

func runtimeBackgroundEventsGin(opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		logService(c.Request.Context(), "runtime.background", "server.runtimeBackgroundEventsGin", "list background events")
		if !authorize(c.Writer, c.Request, opts.AuthToken) {
			return
		}
		if c.Request.Method != http.MethodGet {
			http.Error(c.Writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		store := scheduler.DefaultStore()
		events, next, err := store.ReadEventsAfter(queryInt64(c.Query("offset"), 0), queryInt(c.Query("limit"), 100))
		if err != nil {
			http.Error(c.Writer, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(c.Writer, RuntimeBackgroundEventsResponse{Data: events, NextOffset: next})
	}
}

var (
	errRuntimeBadRequest = errors.New("bad runtime request")
	errRuntimeNotFound   = errors.New("runtime background not found")
)

func runtimeCreateLoop(ctx context.Context, r *http.Request, opts Options) (RuntimeBackgroundJob, error) {
	var req RuntimeLoopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return RuntimeBackgroundJob{}, errRuntimeBadRequest
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return RuntimeBackgroundJob{}, errRuntimeBadRequest
	}
	intervalSeconds, err := runtimeLoopIntervalSeconds(req.IntervalSeconds, req.Interval)
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	cwd := strings.TrimSpace(req.CWD)
	if cwd == "" {
		cwd = opts.Workspace
	}
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			cwd = current
		}
	}
	bgStore := background.DefaultStore()
	job, err := bgStore.CreateWithOptions(background.Options{
		Prompt:          prompt,
		CWD:             cwd,
		Kind:            "loop",
		IntervalSeconds: intervalSeconds,
		Model:           req.Model,
		OutputFormat:    "text",
		MaxTurns:        req.MaxTurns,
		MaxTokens:       req.MaxTokens,
	})
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	scheduleStore := scheduler.DefaultStore()
	schedule, err := scheduleStore.Create(scheduler.Options{
		Prompt:          prompt,
		CWD:             cwd,
		Kind:            "loop",
		Spec:            runtimeLoopSpec(intervalSeconds),
		IntervalSeconds: intervalSeconds,
		Model:           req.Model,
		OutputFormat:    "text",
		MaxTurns:        req.MaxTurns,
		MaxTokens:       req.MaxTokens,
	}, job)
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	return runtimeBackgroundJob(job, schedule, "", 0), nil
}

func runtimeUpdateLoop(ctx context.Context, id string, r *http.Request) (RuntimeBackgroundJob, error) {
	_ = ctx
	var req RuntimeLoopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return RuntimeBackgroundJob{}, errRuntimeBadRequest
	}
	intervalSeconds, err := runtimeLoopIntervalSeconds(req.IntervalSeconds, req.Interval)
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	scheduleStore := scheduler.DefaultStore()
	schedule, ok, err := scheduleStore.Find(id)
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	if !ok {
		return RuntimeBackgroundJob{}, errRuntimeNotFound
	}
	if req.IntervalSeconds <= 0 && strings.TrimSpace(req.Interval) == "" && schedule.IntervalSeconds > 0 {
		intervalSeconds = schedule.IntervalSeconds
	}
	prompt := firstRuntimeString(req.Prompt, schedule.Prompt)
	cwd := firstRuntimeString(req.CWD, schedule.CWD)
	updated, ok, err := scheduleStore.UpdateLoop(id, scheduler.Options{
		Prompt:          prompt,
		CWD:             cwd,
		Kind:            "loop",
		Spec:            runtimeLoopSpec(intervalSeconds),
		IntervalSeconds: intervalSeconds,
		Model:           req.Model,
		MaxTurns:        req.MaxTurns,
		MaxTokens:       req.MaxTokens,
		OutputFormat:    "text",
	})
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	if !ok {
		return RuntimeBackgroundJob{}, errRuntimeNotFound
	}
	bgStore := background.DefaultStore()
	bg, _, _ := bgStore.Find(updated.BackgroundID)
	if bg.ID != "" {
		_, _, _ = bgStore.UpdateLoopOptions(bg.ID, background.Options{Prompt: prompt, CWD: cwd, Kind: "loop", IntervalSeconds: intervalSeconds, Model: req.Model, MaxTurns: req.MaxTurns, MaxTokens: req.MaxTokens, OutputFormat: "text"})
		bg, _, _ = bgStore.Find(updated.BackgroundID)
	}
	return runtimeBackgroundJob(bg, updated, "", 0), nil
}

func runtimeRunLoopNow(ctx context.Context, id string) (RuntimeBackgroundJob, error) {
	scheduleStore := scheduler.DefaultStore()
	schedule, ok, err := scheduleStore.Find(id)
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	if !ok {
		return RuntimeBackgroundJob{}, errRuntimeNotFound
	}
	executable, err := os.Executable()
	if err != nil {
		return RuntimeBackgroundJob{}, err
	}
	started := time.Now().UTC()
	_ = scheduleStore.AppendEvent(scheduler.Event{Type: "run_started", ScheduleID: schedule.ID, BackgroundID: schedule.BackgroundID, Prompt: schedule.Prompt, CWD: schedule.CWD, Status: "running", RunCount: schedule.RunCount})
	runErr := (scheduler.ChildProcessExecutor{Executable: executable}).RunSchedule(ctx, schedule)
	finished := time.Now().UTC()
	errText := ""
	if runErr != nil {
		errText = runErr.Error()
	}
	status := "completed"
	if errText != "" {
		status = "failed"
	}
	_ = scheduleStore.AppendRun(scheduler.RunRecord{ScheduleID: schedule.ID, BackgroundID: schedule.BackgroundID, Prompt: schedule.Prompt, CWD: schedule.CWD, Status: status, Error: errText, StartedAt: started, FinishedAt: &finished})
	nextSchedule, _, _ := scheduleStore.RecordRun(schedule.ID, schedule.NextRunAt, errText)
	bgStore := background.DefaultStore()
	bg, _, _ := bgStore.Find(schedule.BackgroundID)
	if runErr != nil {
		return runtimeBackgroundJob(bg, nextSchedule, "", 0), runErr
	}
	return runtimeBackgroundJob(bg, nextSchedule, "", 0), nil
}

func runtimeBackgroundJobs(kind string, tailBytes, limit int) ([]RuntimeBackgroundJob, error) {
	bgJobs, err := background.DefaultStore().List()
	if err != nil {
		return nil, err
	}
	scheduleStore := scheduler.DefaultStore()
	schedules, err := scheduleStore.List()
	if err != nil {
		return nil, err
	}
	scheduleByBackground := map[string]scheduler.Schedule{}
	for _, item := range schedules {
		if item.BackgroundID != "" {
			scheduleByBackground[item.BackgroundID] = item
		}
	}
	kind = strings.TrimSpace(kind)
	out := make([]RuntimeBackgroundJob, 0, len(bgJobs))
	for _, job := range bgJobs {
		if kind != "" && job.Kind != kind {
			continue
		}
		schedule := scheduleByBackground[job.ID]
		logs, _, _ := background.DefaultStore().Logs(job.ID)
		out = append(out, runtimeBackgroundJob(job, schedule, logs, tailBytes))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func runtimeBackgroundJob(job background.Job, schedule scheduler.Schedule, logs string, tailBytes int) RuntimeBackgroundJob {
	return RuntimeBackgroundJob{
		ID:              firstRuntimeString(job.ID, schedule.BackgroundID),
		ScheduleID:      schedule.ID,
		Prompt:          firstRuntimeString(job.Prompt, schedule.Prompt),
		CWD:             firstRuntimeString(job.CWD, schedule.CWD),
		Kind:            firstRuntimeString(job.Kind, schedule.Kind, "background"),
		Status:          firstRuntimeString(job.Status, "queued"),
		PID:             job.PID,
		IntervalSeconds: firstRuntimeInt(job.IntervalSeconds, schedule.IntervalSeconds),
		Spec:            schedule.Spec,
		Enabled:         schedule.ID == "" || schedule.Enabled,
		RunCount:        maxRuntimeInt(job.RunCount, schedule.RunCount),
		LastRunAt:       firstRuntimeTime(job.LastRunAt, schedule.LastRunAt),
		NextRunAt:       firstRuntimeTime(job.NextRunAt, schedule.NextRunAt),
		LastError:       schedule.LastError,
		LogPath:         job.LogPath,
		LogTail:         tailText(logs, tailBytes),
		CreatedAt:       firstRuntimeTimeValue(job.CreatedAt, schedule.CreatedAt),
		UpdatedAt:       firstRuntimeTimeValue(job.UpdatedAt, schedule.UpdatedAt),
	}
}

func runtimeLoopIntervalSeconds(seconds int, interval string) (int, error) {
	if seconds > 0 {
		return seconds, nil
	}
	interval = strings.TrimSpace(interval)
	if interval == "" {
		return 600, nil
	}
	duration, err := time.ParseDuration(interval)
	if err != nil || duration <= 0 {
		return 0, errRuntimeBadRequest
	}
	if duration < time.Minute {
		duration = time.Minute
	}
	return int(duration.Seconds()), nil
}

func runtimeLoopSpec(intervalSeconds int) string {
	if intervalSeconds <= 0 {
		intervalSeconds = 600
	}
	return "@every " + (time.Duration(intervalSeconds) * time.Second).String()
}

func queryInt(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func queryInt64(value string, fallback int64) int64 {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func tailText(text string, maxBytes int) string {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	if idx := strings.IndexByte(text[start:], '\n'); idx >= 0 {
		start += idx + 1
	}
	return text[start:]
}

func firstRuntimeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstRuntimeInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func maxRuntimeInt(values ...int) int {
	maxValue := 0
	for _, value := range values {
		if value > maxValue {
			maxValue = value
		}
	}
	return maxValue
}

func firstRuntimeTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstRuntimeTimeValue(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
