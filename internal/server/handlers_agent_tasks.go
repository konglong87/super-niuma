package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/nextsteps"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/slashcommands"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	"github.com/konglong87/super-niuma/internal/telemetry"
	"github.com/konglong87/super-niuma/internal/tools"
)

func tenantAgentTasksHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_tasks", "server.tenantAgentTasksHandler", "handle tenant agent tasks")
		switch r.Method {
		case http.MethodGet:
			items, err := opts.TenantService.ListAgentTasks(r.Context(), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req agentTaskCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			input, err := req.toTaskInput()
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			input.TraceID = firstNonEmptyString(input.TraceID, observability.TraceID(r.Context()), newWebAgentTraceID())
			input.MetadataJSON = ensureAgentTaskMetadataTrace(input.MetadataJSON, input.TraceID)
			id, err := opts.TenantService.CreateAgentTask(r.Context(), input)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			if input.Status == agenttasks.StatusRunning {
				if _, err := appendAgentTaskEventByID(r.Context(), opts, id, agenttasks.EventInput{
					TaskID:      id,
					EventType:   agenttasks.EventStarted,
					PayloadJSON: agentTaskEventPayload(map[string]any{"source": "api", "agent_name": input.AgentName, "status": input.Status}),
					TraceID:     input.TraceID,
				}); err != nil {
					writeTenantServiceError(w, err)
					return
				}
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_task.create", "agent_task", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func agentSlashCommandsHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "agent_slash_commands", "server.agentSlashCommandsHandler", "list agent slash commands")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cwd := strings.TrimSpace(r.URL.Query().Get("cwd"))
		if cwd == "" {
			cwd = opts.Workspace
		}
		if cwd == "" {
			if current, err := os.Getwd(); err == nil {
				cwd = current
			}
		}
		commands, err := slashcommands.List(cwd, r.URL.Query().Get("prefix"))
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		limit := parseLimit(r.URL.Query().Get(paramLimit))
		if limit <= 0 {
			limit = 8
		}
		if limit > 50 {
			limit = 50
		}
		if len(commands) > limit {
			commands = commands[:limit]
		}
		writeJSON(w, map[string]any{"data": commands})
	}
}

func agentWorkspaceValidateHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "agent_workspace_validate", "server.agentWorkspaceValidateHandler", "validate agent workspace")
		if !authorize(w, r, opts.AuthToken) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			CWD string `json:"cwd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		workspace, err := ValidateWorkspaceCWD(req.CWD)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, workspace)
	}
}

// ValidateWorkspaceCWD checks that value is an absolute, existing directory
// and returns the canonical workspace metadata used by the WebUI.
func ValidateWorkspaceCWD(value string) (map[string]any, error) {
	cwd, err := ValidateWorkspaceCWDPath(value)
	if err != nil {
		return nil, err
	}
	gitRoot := findGitRoot(cwd)
	return map[string]any{
		"cwd":            cwd,
		"workspace_name": filepath.Base(cwd),
		"exists":         true,
		"is_dir":         true,
		"git_root":       gitRoot,
		"is_git_repo":    gitRoot != "",
	}, nil
}

// ValidateWorkspaceCWDPath is the typed path-only form used by server
// composition code that does not need the HTTP metadata envelope.
func ValidateWorkspaceCWDPath(value string) (string, error) {
	cwd := strings.TrimSpace(value)
	if cwd == "" {
		return "", errors.New("cwd is required")
	}
	if !filepath.IsAbs(cwd) {
		return "", errors.New("cwd must be an absolute path")
	}
	cleaned := filepath.Clean(cwd)
	info, err := os.Stat(cleaned)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("cwd does not exist")
		}
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("cwd must be a directory")
	}
	return cleaned, nil
}

func findGitRoot(dir string) string {
	current := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func tenantAgentTaskHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task", "server.tenantAgentTaskHandler", "handle tenant agent task")
		taskID, ok := tenantAgentTaskIDFromPath(r.URL.Path, "")
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, errMsgAgentTaskIDRequired)
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := opts.TenantService.GetAgentTask(r.Context(), taskID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPatch:
			if opts.WorkflowTaskOwner != nil {
				if _, err := ensureTenantOwnsAgentTask(r.Context(), opts.TenantService, taskID); err != nil {
					writeTenantServiceError(w, err)
					return
				}
				if rejectWorkflowTaskMutation(w, r, opts, taskID) {
					return
				}
			}
			var req agentTaskUpdateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			update, eventType, err := req.toTaskUpdate()
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := opts.TenantService.UpdateAgentTask(r.Context(), taskID, update); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			if eventType != "" {
				if _, err := appendAgentTaskEventByID(r.Context(), opts, taskID, agenttasks.EventInput{
					TaskID:      taskID,
					EventType:   eventType,
					PayloadJSON: agentTaskEventPayload(map[string]any{"source": "api", "status": update.Status, "result_json": update.ResultJSON, "metadata_json": update.MetadataJSON}),
				}); err != nil {
					writeTenantServiceError(w, err)
					return
				}
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_task.update", "agent_task", taskID)
			writeJSON(w, map[string]any{"id": taskID, "updated": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantAgentTaskEventsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task_events", "server.tenantAgentTaskEventsHandler", "handle tenant agent task events")
		taskID, ok := tenantAgentTaskIDFromPath(r.URL.Path, "events")
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, errMsgAgentTaskIDRequired)
			return
		}
		switch r.Method {
		case http.MethodGet:
			query := r.URL.Query()
			items, err := listAgentTaskEventsByID(r.Context(), opts, taskID, parseUintQuery(query.Get("after_id")), parseLimit(query.Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantAgentTaskEventsStreamHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task_events_stream", "server.tenantAgentTaskEventsStreamHandler", "stream tenant agent task events")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		taskID, ok := tenantAgentTaskIDFromPath(r.URL.Path, "events/stream")
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, errMsgAgentTaskIDRequired)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeTenantError(w, http.StatusInternalServerError, "streaming is not supported")
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("connection", "keep-alive")
		writeAgentTaskSSE(w, "connected", map[string]any{"task_id": taskID})
		flusher.Flush()

		query := r.URL.Query()
		limit := parseLimit(query.Get(paramLimit))
		if limit == 0 {
			limit = 200
		}
		var streamSvc agentTaskEventStreamer = opts.TenantService
		if opts.SessionEvents != nil {
			streamSvc = routedAgentTaskEventStreamer{opts: opts}
		}
		stream := &agentTaskStream{
			svc:    streamSvc,
			write:  func(event string, value any) { writeAgentTaskSSE(w, event, value) },
			flush:  flusher.Flush,
			taskID: taskID,
			lastID: parseUintQuery(query.Get("after_id")),
			limit:  limit,
			policy: defaultAgentTaskStreamPolicy(),
		}
		stream.run(r.Context())
	})
}

const (
	// 有新事件时保持 250ms:runner 每个 chunk 实时落库,流式体验主要取决于这里的
	// 推送节奏;1s 会让 text_delta 一秒一批,前端打字机也救不回实时感。
	minAgentTaskPollInterval = 250 * time.Millisecond
	// 空转时退避到这里。原来固定 250ms 且每 tick 打两条查询，100 条并发流就是
	// 800 QPS 常驻(AUDIT-P0-12)。上限同时也是任务收尾被察觉的最坏延迟。
	maxAgentTaskPollInterval = 2 * time.Second
)

type agentTaskEventStreamer interface {
	ListAgentTaskEventsAfter(ctx context.Context, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error)
	GetAgentTask(ctx context.Context, taskID uint64) (mysqlstore.AgentTask, error)
}

type agentTaskStreamPolicy struct {
	minInterval time.Duration
	maxInterval time.Duration
	// wait 睡 d，返回 false 表示 ctx 已取消、该收流了。
	wait func(ctx context.Context, d time.Duration) bool
}

func defaultAgentTaskStreamPolicy() agentTaskStreamPolicy {
	return agentTaskStreamPolicy{
		minInterval: minAgentTaskPollInterval,
		maxInterval: maxAgentTaskPollInterval,
		wait:        waitAgentTaskPoll,
	}
}

func waitAgentTaskPoll(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (p agentTaskStreamPolicy) next(current time.Duration, sawEvents bool) time.Duration {
	if sawEvents || current < p.minInterval {
		return p.minInterval
	}
	if next := current * 2; next < p.maxInterval {
		return next
	}
	return p.maxInterval
}

type agentTaskStream struct {
	svc    agentTaskEventStreamer
	write  func(event string, value any)
	flush  func()
	taskID uint64
	lastID uint64
	limit  int
	policy agentTaskStreamPolicy
}

// run 轮询事件表并把新事件推给客户端。每轮只打一条查询；只有在这一轮没有新事件时
// 才多花一条去确认任务是否已经收尾 —— 事件还在流说明任务显然还活着。
func (s *agentTaskStream) run(ctx context.Context) {
	interval := s.policy.minInterval
	for {
		events, err := s.svc.ListAgentTaskEventsAfter(ctx, s.taskID, s.lastID, s.limit)
		if err != nil {
			s.fail(err)
			return
		}
		for _, event := range events {
			s.write("agent_task_event", event)
			if event.ID > s.lastID {
				s.lastID = event.ID
			}
		}
		s.flush()

		if len(events) == 0 {
			task, err := s.svc.GetAgentTask(ctx, s.taskID)
			if err != nil {
				s.fail(err)
				return
			}
			if task.Status != "" && task.Status != agenttasks.StatusRunning {
				return
			}
		}
		interval = s.policy.next(interval, len(events) > 0)
		if !s.policy.wait(ctx, interval) {
			return
		}
	}
}

func (s *agentTaskStream) fail(err error) {
	s.write("error", map[string]any{"error": err.Error()})
	s.flush()
}

func writeAgentTaskSSE(w io.Writer, event string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte(`{"error":"json encode error"}`)
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

func tenantAgentTaskMessageHandler(opts Options, queryFn QueryFunc) http.HandlerFunc {
	// 每个 handler 一个 limiter：detached runner 最长活 agentTaskRunTimeout，
	// 不设上限的话一串 POST 就能起满 goroutine 直到 OOM。
	runLimiter := newAgentTaskRunLimiter(opts.AgentTaskMaxConcurrentRuns)
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task_message", "server.tenantAgentTaskMessageHandler", "send tenant agent task message")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		taskID, ok := tenantAgentTaskIDFromPath(r.URL.Path, "message")
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, errMsgAgentTaskIDRequired)
			return
		}
		task, err := opts.TenantService.GetAgentTask(r.Context(), taskID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if rejectWorkflowTaskMutation(w, r, opts, taskID) {
			return
		}
		if isTerminalAgentTaskStatus(task.Status) {
			writeTenantError(w, http.StatusConflict, "agent task is not accepting messages")
			return
		}
		if task.Status == agenttasks.StatusRunning && opts.PendingInputQueue != nil && r.Header.Get("X-Pending-Input") == "true" {
			var req agentTaskMessageRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			input, err := req.toMessageInput(taskID)
			if err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			_, scope, err := pendingInputScope(r, opts, taskID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			item, err := opts.PendingInputQueue.Add(r.Context(), pendinginput.NewInput{Scope: scope, ClientInputID: firstNonEmptyString(input.TraceID, newWebAgentTraceID()), Content: input.Content, Attachments: input.Attachments})
			if err != nil {
				writePendingInputError(w, err)
				return
			}
			appendPendingInputEvent(r, opts.TenantService, taskID, agenttasks.EventInputQueued, item)
			// Preserve the legacy message endpoint's 200 response while exposing
			// the queued status; the dedicated pending-input endpoint uses 202.
			writeJSON(w, map[string]any{"id": item.ID, "task_id": taskID, "status": pendinginput.StatusQueued})
			return
		}
		// 容量检查必须在改 status 之前：拒绝时任务要原样留在 ready，
		// 客户端才能重试；否则就留下一个没人跑的 running 任务。
		release, admitted := runLimiter.acquire()
		if !admitted {
			emitAgentRunRejected(r.Context(), task, runLimiter.limit())
			w.Header().Set("Retry-After", "5")
			writeTenantError(w, http.StatusServiceUnavailable, runLimiter.atCapacityMessage())
			return
		}
		// 槽位交给 goroutine 前，任何提前返回都要把它还回去。
		handedOff := false
		defer func() {
			if !handedOff {
				release()
			}
		}()
		if task.Status != agenttasks.StatusRunning {
			if err := opts.TenantService.UpdateAgentTask(r.Context(), taskID, agenttasks.TaskUpdate{Status: agenttasks.StatusRunning}); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			task.Status = agenttasks.StatusRunning
		}
		var req agentTaskMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		input, err := req.toMessageInput(taskID)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		input.TraceID = agentTaskRunTraceID(r.Context(), task, input.TraceID)
		task.TraceID = input.TraceID
		payload, err := json.Marshal(input)
		if err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		eventID, err := appendAgentTaskEvent(r.Context(), opts, task, agenttasks.EventInput{
			TaskID:      taskID,
			EventType:   agenttasks.EventMessage,
			PayloadJSON: string(payload),
			TraceID:     input.TraceID,
		})
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if opts.StreamQueryFunc == nil && queryFn == nil {
			_, _ = appendAgentTaskEvent(r.Context(), opts, task, agenttasks.EventInput{
				TaskID:      taskID,
				EventType:   agenttasks.EventFailed,
				PayloadJSON: agentTaskEventPayload(map[string]any{"source": "webui-agent", "error": errMsgAgentRunnerNotConfig}),
				TraceID:     input.TraceID,
			})
			_ = opts.TenantService.FinishAgentTask(r.Context(), taskID, agenttasks.StatusFailed, agentTaskEventPayload(map[string]any{"source": "webui-agent", "error": errMsgAgentRunnerNotConfig, "trace_id": input.TraceID}))
			triggerPendingInputCoordinator(r.Context(), opts, task)
			writeTenantError(w, http.StatusServiceUnavailable, errMsgAgentRunnerNotConfig)
			return
		}
		runCtx := context.WithoutCancel(r.Context())
		runCtx = observability.WithTraceID(runCtx, input.TraceID)
		runCtx, runCancel := context.WithTimeout(runCtx, agentTaskRunTimeout(opts))
		if opts.AgentTaskController != nil {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithCancel(runCtx)
			opts.AgentTaskController.Register(taskID, cancel)
		}
		handedOff = true
		go func() {
			defer runCancel()
			if opts.AgentTaskController != nil {
				defer opts.AgentTaskController.Unregister(taskID)
			}
			defer release()
			// gin.Recovery 覆盖不到这条脱离请求的 goroutine：panic 必须在这里收住，
			// 否则一次 runner panic 打死整个进程。
			defer recoverAgentTaskRun(runCtx, opts, task, input.TraceID)
			_, _ = runAgentTaskMessage(runCtx, opts, queryFn, task, input)
		}()
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_task.message", "agent_task", taskID)
		writeJSON(w, map[string]any{"id": eventID, "task_id": taskID, "status": agenttasks.StatusRunning})
	})
}

type agentTaskRunResult struct {
	Status string
}

func runAgentTaskMessage(ctx context.Context, opts Options, queryFn QueryFunc, task mysqlstore.AgentTask, input agenttasks.MessageInput) (agentTaskRunResult, error) {
	if opts.TenantService == nil {
		return agentTaskRunResult{}, errors.New(errMsgTenantStorageNotConfig)
	}
	metadata := parseAgentTaskJSONMap(task.MetadataJSON)
	cwd := strings.TrimSpace(agentTaskStringValue(metadata["cwd"]))
	if cwd == "" {
		cwd = opts.Workspace
	}
	model := strings.TrimSpace(task.Model)
	if model == "" {
		model = strings.TrimSpace(agentTaskStringValue(metadata["model"]))
	}
	provider := strings.TrimSpace(agentTaskStringValue(metadata["provider"]))
	source := strings.TrimSpace(agentTaskStringValue(metadata["source"]))
	promptMode := normalizeAgentTaskPromptMode(agentTaskStringValue(metadata["prompt_mode"]))
	permissionMode := strings.TrimSpace(agentTaskStringValue(metadata["permission_mode"]))
	effort := strings.TrimSpace(agentTaskStringValue(metadata["effort"]))
	prompt := input.Content
	if resolved, ok, err := slashcommands.ResolvePrompt(ctx, cwd, input.Content); err != nil {
		return agentTaskRunResult{}, err
	} else if ok {
		prompt = resolved
	}
	queryReq := QueryRequest{
		Prompt:                   prompt,
		Model:                    model,
		Provider:                 provider,
		TenantID:                 task.TenantID,
		UserID:                   task.UserID,
		TenantSessionID:          task.ParentSessionID,
		CWD:                      cwd,
		SessionKey:               fmt.Sprintf("web-agent-task-%d", task.ID),
		PromptMode:               promptMode,
		PermissionMode:           permissionMode,
		Effort:                   effort,
		TraceID:                  input.TraceID,
		DisableTenantPersistence: true,
	}
	attachments, attachmentErr := agentTaskQueryAttachments(ctx, opts, task, input.Attachments)
	queryReq.Attachments = attachments
	queryReq.InitialMessages = webAgentConversationInitialMessagesWithOptions(ctx, opts, task)
	handoffMessage, handoffErr := agentTaskHandoffContextMessage(ctx, opts.TenantService, task, cwd, model)
	if handoffErr == nil && handoffMessage != nil {
		queryReq.InitialMessages = append(queryReq.InitialMessages, *handoffMessage)
	}
	if handoffErr == nil && source == agenttasks.SourcePendingInputSideChat {
		candidate, candidateErr := webAgentTaskMessageFromEventsWithReader(ctx, opts.TenantService, routedAgentTaskHistoryReader{opts: opts}, task.ID)
		attachmentErr = errors.Join(attachmentErr, candidateErr)
		if candidate.Content != strings.TrimSpace(prompt) || len(input.Attachments) == 0 {
			if candidate.Content != "" && candidate.Content != strings.TrimSpace(prompt) {
				queryReq.InitialMessages = append(queryReq.InitialMessages, anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: candidate.Content}}})
			}
			candidateAttachments, err := agentTaskQueryAttachments(ctx, opts, task, candidate.Attachments)
			attachmentErr = errors.Join(attachmentErr, err)
			queryReq.Attachments = append(candidateAttachments, queryReq.Attachments...)
		}
	}
	var text strings.Builder
	var eventSink *agentTaskTextSink
	var result query.Result
	err := errors.Join(attachmentErr, handoffErr)
	startedAt := time.Now().UTC()
	emitAgentRunStarted(ctx, task, input.TraceID, model, cwd, promptMode, permissionMode, effort)
	switch {
	case err != nil:
		// Handoff validation is a pre-dispatch gate: the model must not see a
		// partial, foreign, malformed, or over-budget package set.
	case opts.StreamQueryFunc != nil:
		idleCtx, idleCancel := context.WithCancelCause(ctx)
		defer idleCancel(nil)
		eventSink = &agentTaskTextSink{
			ctx:         idleCtx,
			opts:        opts,
			task:        task,
			permissions: opts.AgentTaskPermissions,
			questions:   opts.AgentTaskQuestions,
			taskID:      task.ID,
			traceID:     input.TraceID,
			text:        &text,
			idleTimeout: agentTaskIdleTimeout(opts),
			idleCancel:  idleCancel,
		}
		eventSink.startIdleWatchdog()
		result, err = opts.StreamQueryFunc(idleCtx, queryReq, eventSink)
		eventSink.stopIdleWatchdog()
		if cause := context.Cause(idleCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			err = cause
		} else if err == nil {
			err = context.Cause(idleCtx)
		}
	case queryFn != nil:
		result, err = queryFn(ctx, queryReq)
		if result.Response != "" {
			_, appendErr := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
				TaskID:      task.ID,
				EventType:   agenttasks.EventTextDelta,
				PayloadJSON: agentTaskEventPayload(map[string]any{"source": "runner", "content": result.Response}),
				TraceID:     input.TraceID,
			})
			if appendErr != nil {
				return agentTaskRunResult{}, appendErr
			}
		}
	default:
		err = errors.New(errMsgAgentRunnerNotConfig)
	}
	if result.Response == "" {
		result.Response = text.String()
	}
	totalTokens := queryUsageTotalTokens(result.Usage)
	contextLength := agentTaskContextLength(cwd, model)
	contextPercent := 0
	if contextLength > 0 && totalTokens > 0 {
		contextPercent = int(math.Ceil(float64(totalTokens) * 100 / float64(contextLength)))
		if contextPercent < 1 {
			contextPercent = 1
		}
		if contextPercent > 100 {
			contextPercent = 100
		}
	}
	persistCtx := context.WithoutCancel(ctx)
	if err != nil {
		if isAgentTaskTimeoutError(ctx, err) {
			durationMS := time.Since(startedAt).Milliseconds()
			payload := agentTaskEventPayload(map[string]any{
				"source":      "runner",
				"trace_id":    input.TraceID,
				"error":       err.Error(),
				"timeout":     true,
				"stop_reason": agentTaskTimeoutStopReason(ctx, err),
				"duration_ms": durationMS,
			})
			if _, appendErr := appendAgentTaskEvent(persistCtx, opts, task, agenttasks.EventInput{TaskID: task.ID, EventType: agenttasks.EventTimeout, PayloadJSON: payload, TraceID: input.TraceID}); appendErr != nil {
				return agentTaskRunResult{}, appendErr
			}
			if finishErr := opts.TenantService.FinishAgentTask(persistCtx, task.ID, agenttasks.StatusTimeout, payload); finishErr != nil {
				return agentTaskRunResult{}, finishErr
			}
			triggerPendingInputCoordinator(persistCtx, opts, task)
			emitAgentRunFinished(ctx, task, input.TraceID, model, agenttasks.StatusTimeout, durationMS, result, totalTokens, contextPercent, err, map[string]any{"timeout": true})
			return agentTaskRunResult{Status: agenttasks.StatusTimeout}, nil
		}
		if errors.Is(err, context.Canceled) {
			runResult, cancelErr := finishAgentTaskCancelled(persistCtx, opts, task, input.TraceID, startedAt, result.Response)
			if cancelErr == nil {
				triggerPendingInputCoordinator(persistCtx, opts, task)
			}
			emitAgentRunFinished(ctx, task, input.TraceID, model, agenttasks.StatusCancelled, time.Since(startedAt).Milliseconds(), result, totalTokens, contextPercent, err, map[string]any{"cancelled": true})
			return runResult, cancelErr
		}
		durationMS := time.Since(startedAt).Milliseconds()
		failureValues := map[string]any{
			"source":      "runner",
			"trace_id":    input.TraceID,
			"error":       err.Error(),
			"duration_ms": durationMS,
		}
		if errorCode := agentTaskHandoffErrorCode(err); errorCode != "" {
			failureValues["error_code"] = errorCode
		}
		payload := agentTaskEventPayload(failureValues)
		if _, appendErr := appendAgentTaskEvent(persistCtx, opts, task, agenttasks.EventInput{TaskID: task.ID, EventType: agenttasks.EventFailed, PayloadJSON: payload, TraceID: input.TraceID}); appendErr != nil {
			return agentTaskRunResult{}, appendErr
		}
		if finishErr := opts.TenantService.FinishAgentTask(persistCtx, task.ID, agenttasks.StatusFailed, payload); finishErr != nil {
			return agentTaskRunResult{}, finishErr
		}
		triggerPendingInputCoordinator(persistCtx, opts, task)
		emitAgentRunFinished(ctx, task, input.TraceID, model, agenttasks.StatusFailed, durationMS, result, totalTokens, contextPercent, err, nil)
		return agentTaskRunResult{Status: agenttasks.StatusFailed}, nil
	}
	durationMS := time.Since(startedAt).Milliseconds()
	var emittedToolIDs map[string]bool
	if eventSink != nil {
		emittedToolIDs = eventSink.toolIDs()
	}
	if appendErr := appendAgentTaskToolEventsWithOptions(persistCtx, opts, task, input.TraceID, result.ToolCalls, emittedToolIDs); appendErr != nil {
		return agentTaskRunResult{}, appendErr
	}
	toolNames := agentTaskNextStepsToolNames(result.ToolCalls)
	nextStepsCfg, nextStepsEligible := agentTaskNextStepsConfig(opts, task, cwd, source, result.Response, model)
	nextStepsReserved := false
	if nextStepsEligible && opts.nextStepsDispatcher != nil {
		if opts.nextStepsDispatcher.reserve != nil {
			nextStepsReserved = opts.nextStepsDispatcher.reserve()
			if nextStepsReserved && opts.nextStepsDispatcher.enqueueReserved == nil && opts.nextStepsDispatcher.enqueue == nil {
				if opts.nextStepsDispatcher.releaseReservation != nil {
					opts.nextStepsDispatcher.releaseReservation()
				}
				nextStepsReserved = false
			}
		} else {
			// Compatibility for small direct-test/embedding dispatchers that only
			// implement the original enqueue hook; such dispatchers are responsible
			// for accepting the immutable job without a bounded reservation.
			nextStepsReserved = opts.nextStepsDispatcher.enqueue != nil
		}
	}
	resultValues := map[string]any{
		"source":                      "runner",
		"trace_id":                    input.TraceID,
		"response":                    result.Response,
		"model":                       result.Model,
		"turns":                       result.Turns,
		"stop_reason":                 result.StopReason,
		"input_tokens":                result.Usage.InputTokens,
		"output_tokens":               result.Usage.OutputTokens,
		"cache_creation_input_tokens": result.Usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     result.Usage.CacheReadInputTokens,
		"cache_creation_ephemeral_1h_input_tokens": result.Usage.CacheCreationEphemeral1hInputTokens,
		"cache_creation_ephemeral_5m_input_tokens": result.Usage.CacheCreationEphemeral5mInputTokens,
		"total_tokens":     totalTokens,
		"context_length":   contextLength,
		"context_percent":  contextPercent,
		"initial_messages": len(queryReq.InitialMessages),
		"tool_calls":       len(result.ToolCalls),
		"transcript_path":  result.TranscriptPath,
		"duration_ms":      durationMS,
		"service_tier":     result.Usage.ServiceTier,
		"inference_geo":    result.Usage.InferenceGeo,
		"speed":            result.Usage.Speed,
	}
	if nextStepsReserved {
		resultValues["next_steps_status"] = "pending"
	}
	resultJSON := agentTaskEventPayload(resultValues)
	if _, appendErr := appendAgentTaskEvent(persistCtx, opts, task, agenttasks.EventInput{
		TaskID:      task.ID,
		EventType:   agenttasks.EventCompleted,
		PayloadJSON: resultJSON,
		TraceID:     input.TraceID,
	}); appendErr != nil {
		if nextStepsReserved && opts.nextStepsDispatcher.releaseReservation != nil {
			opts.nextStepsDispatcher.releaseReservation()
		}
		return agentTaskRunResult{}, appendErr
	}
	if finishErr := opts.TenantService.FinishAgentTask(persistCtx, task.ID, agenttasks.StatusCompleted, resultJSON); finishErr != nil {
		if nextStepsReserved && opts.nextStepsDispatcher.releaseReservation != nil {
			opts.nextStepsDispatcher.releaseReservation()
		}
		return agentTaskRunResult{}, finishErr
	}
	postFinishAgentTaskNextSteps(persistCtx, opts, task, cwd, source, input.TraceID, prompt, result.Response, model, provider, toolNames, nextStepsCfg, nextStepsEligible, nextStepsReserved)
	triggerPendingInputCoordinator(persistCtx, opts, task)
	emitAgentRunFinishedAfterCompletion(ctx, task, input.TraceID, model, durationMS, result, totalTokens, contextPercent)
	return agentTaskRunResult{Status: agenttasks.StatusCompleted}, nil
}

func triggerPendingInputCoordinator(ctx context.Context, opts Options, task mysqlstore.AgentTask) {
	metadata := parseAgentTaskJSONMap(task.MetadataJSON)
	if agentTaskStringValue(metadata["pending_input_id"]) != "" {
		return
	}
	if opts.pendingInputCoordinator != nil {
		opts.pendingInputCoordinator.trigger(ctx, pendingInputScopeForTask(task))
	}
}

func emitAgentRunFinishedAfterCompletion(ctx context.Context, task mysqlstore.AgentTask, traceID, model string, durationMS int64, result query.Result, totalTokens, contextPercent int) {
	defer func() {
		if recover() != nil {
			observability.Error(ctx, nil, "agent.run.finished_panic", "server.runAgentTaskMessage", "completion telemetry panicked", "task_id", task.ID, "error_class", "panic")
		}
	}()
	emitAgentRunFinished(ctx, task, traceID, model, agenttasks.StatusCompleted, durationMS, result, totalTokens, contextPercent, nil, nil)
}

// postFinishAgentTaskNextSteps owns all optional work after the main task is
// completed. A panic here must never re-enter recoverAgentTaskRun and rewrite
// the already persisted completed status.
func postFinishAgentTaskNextSteps(ctx context.Context, opts Options, task mysqlstore.AgentTask, cwd, source, traceID, prompt, response, model, provider string, toolNames []string, cfg nextsteps.Config, eligible, reserved bool) {
	reservationHeld := reserved
	defer func() {
		if rec := recover(); rec != nil {
			if reservationHeld && opts.nextStepsDispatcher != nil && opts.nextStepsDispatcher.releaseReservation != nil {
				func() {
					defer func() { _ = recover() }()
					opts.nextStepsDispatcher.releaseReservation()
				}()
			}
			observability.Error(ctx, nil, "agent.next_steps.post_finish_panic", "server.runAgentTaskMessage", "post-finish next-step action panicked", "task_id", task.ID, "error_class", "panic")
		}
	}()
	if reserved {
		job := agentTaskNextStepsJob{
			task: task, cwd: cwd, source: source, traceID: traceID,
			userID: observability.UserID(ctx), tenantKey: observability.TenantKey(ctx),
			emitter: telemetry.FromContext(ctx),
			prompt:  prompt, response: response, model: model, provider: provider,
			toolNames: toolNames, cfg: cfg,
		}
		accepted := false
		if opts.nextStepsDispatcher.enqueueReserved != nil {
			accepted = opts.nextStepsDispatcher.enqueueReserved(job)
		} else if opts.nextStepsDispatcher.enqueue != nil {
			accepted = opts.nextStepsDispatcher.enqueue(job)
		}
		reservationHeld = false
		if !accepted {
			observability.Info(ctx, nil, "agent.next_steps.rejected", "server.runAgentTaskMessage", "next-step job enqueue rejected after completion", "task_id", task.ID)
			appendAgentTaskNextStepsDropped(ctx, opts, task, traceID)
		}
		emitAgentTaskNextStepsQueued(ctx, task, traceID, accepted)
		return
	}
	if eligible && opts.nextStepsDispatcher != nil {
		emitAgentTaskNextStepsQueued(ctx, task, traceID, false)
		return
	}
	if eligible {
		// NewHandler is also used by direct embedders/tests without lifecycle ownership.
		// Keep that path deterministic and avoid creating an unowned goroutine/dispatcher.
		appendAgentTaskNextStepsWithConfig(ctx, opts, task, cwd, source, traceID, prompt, response, provider, toolNames, cfg)
	}
}

func queryAttachmentsFromAgentTask(attachments []agenttasks.Attachment) []QueryAttachment {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]QueryAttachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, QueryAttachment{
			AttachmentID: attachment.AttachmentID,
			Type:         attachment.Type,
			MediaType:    attachment.MediaType,
			Name:         attachment.Name,
			URL:          attachment.URL,
			SizeBytes:    attachment.SizeBytes,
			SHA256:       attachment.SHA256,
			InlineData:   attachment.InlineData,
		})
	}
	return out
}

func isAgentTaskTimeoutError(ctx context.Context, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return true
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "idle timeout")
}

func agentTaskTimeoutStopReason(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return "deadline_exceeded"
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "idle timeout") {
		return "idle_timeout"
	}
	return "timeout"
}

func appendAgentTaskToolEvents(ctx context.Context, svc TenantService, taskID uint64, traceID string, calls []query.ToolTrace, skip map[string]bool) error {
	return appendAgentTaskToolEventsWithOptions(ctx, Options{TenantService: svc}, mysqlstore.AgentTask{ID: taskID}, traceID, calls, skip)
}

func appendAgentTaskToolEventsWithOptions(ctx context.Context, opts Options, task mysqlstore.AgentTask, traceID string, calls []query.ToolTrace, skip map[string]bool) error {
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" && strings.TrimSpace(call.Name) == "" {
			continue
		}
		if skip != nil && skip[call.ID] {
			continue
		}
		payload := map[string]any{
			"source":    "runner",
			"tool_id":   call.ID,
			"tool_name": call.Name,
		}
		if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
			TaskID:      task.ID,
			EventType:   agenttasks.EventToolCall,
			PayloadJSON: agentTaskEventPayload(payload),
			TraceID:     traceID,
		}); err != nil {
			return err
		}
		resultPayload := map[string]any{
			"source":    "runner",
			"tool_id":   call.ID,
			"tool_name": call.Name,
			"is_error":  call.IsError,
			"input":     truncateAgentTaskEventText(call.Input, 600),
			"preview":   truncateAgentTaskEventText(call.Output, 160),
			"output":    truncateAgentTaskEventText(call.Output, 2000),
		}
		if call.Name == "ComputerUse" {
			for key, value := range computeruse.ResultMetadata(call.Output) {
				resultPayload[key] = value
			}
		}
		if call.ComputerObservation != nil {
			resultPayload["computer_observation"] = call.ComputerObservation
		}
		if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
			TaskID:      task.ID,
			EventType:   agenttasks.EventToolResult,
			PayloadJSON: agentTaskEventPayload(resultPayload),
			TraceID:     traceID,
		}); err != nil {
			return err
		}
		// Non-streaming runs rebuild their event log from result.ToolCalls, so the
		// file_change rows have to be derived here too or the Files tab is empty
		// for exactly the runs that did not stream.
		if err := appendAgentTaskFileChangeEventsWithOptions(ctx, opts, task, traceID, "runner", call); err != nil {
			return err
		}
	}
	return nil
}

const webAgentConversationHistoryLimit = 12

type agentTaskHistoryEventReader interface {
	ListAgentTaskEvents(context.Context, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
}

type routedAgentTaskHistoryReader struct {
	opts Options
}

func (r routedAgentTaskHistoryReader) ListAgentTaskEvents(ctx context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return listAgentTaskEventsByID(ctx, r.opts, taskID, 0, limit)
}

func webAgentConversationInitialMessages(ctx context.Context, svc TenantService, current mysqlstore.AgentTask) []anthropic.MessageParam {
	return webAgentConversationInitialMessagesWithReader(ctx, svc, svc, current)
}

func webAgentConversationInitialMessagesWithOptions(ctx context.Context, opts Options, current mysqlstore.AgentTask) []anthropic.MessageParam {
	return webAgentConversationInitialMessagesWithReader(ctx, opts.TenantService, routedAgentTaskHistoryReader{opts: opts}, current)
}

func webAgentConversationInitialMessagesWithReader(ctx context.Context, svc TenantService, events agentTaskHistoryEventReader, current mysqlstore.AgentTask) []anthropic.MessageParam {
	if svc == nil || current.ParentSessionID == 0 {
		return nil
	}
	var tasks []mysqlstore.AgentTask
	var err error
	if history, ok := svc.(interface {
		ListSessionConversationTasks(context.Context, uint64, int) ([]mysqlstore.AgentTask, error)
	}); ok {
		tasks, err = history.ListSessionConversationTasks(ctx, current.ParentSessionID, 200)
	} else {
		tasks, err = svc.ListAgentTasks(ctx, 200)
	}
	if err != nil {
		observability.Error(ctx, nil, "agent.conversation_context.list_error", "server.webAgentConversationInitialMessages", "list web agent history failed", "error", err)
		return nil
	}
	type webAgentHistoryTurn struct {
		task     mysqlstore.AgentTask
		prompt   string
		response string
		compact  string
	}
	history := make([]webAgentHistoryTurn, 0)
	for _, task := range tasks {
		if task.ID == current.ID || task.ParentSessionID != current.ParentSessionID || !isTerminalAgentTaskStatus(task.Status) {
			continue
		}
		prompt := webAgentTaskHistoryPromptFromReader(ctx, svc, events, task)
		compactSummary := webAgentTaskLatestCompactSummaryFromReader(ctx, events, task.ID)
		response := webAgentTaskHistoryResponse(task)
		if prompt == "" || response == "" {
			continue
		}
		history = append(history, webAgentHistoryTurn{task: task, prompt: prompt, response: response, compact: compactSummary})
	}
	sort.SliceStable(history, func(i, j int) bool {
		if history[i].task.StartedAt.Equal(history[j].task.StartedAt) {
			return history[i].task.ID < history[j].task.ID
		}
		return history[i].task.StartedAt.Before(history[j].task.StartedAt)
	})
	latestCompactIndex := -1
	for i, turn := range history {
		if strings.TrimSpace(turn.compact) != "" {
			latestCompactIndex = i
		}
	}
	var compactSummary string
	if latestCompactIndex >= 0 {
		compactSummary = strings.TrimSpace(history[latestCompactIndex].compact)
		history = history[latestCompactIndex+1:]
	}
	if len(history) > webAgentConversationHistoryLimit {
		history = history[len(history)-webAgentConversationHistoryLimit:]
	}
	messages := make([]anthropic.MessageParam, 0, len(history)*2+1)
	if compactSummary != "" {
		messages = append(messages, anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: compactSummary}}})
	}
	for _, turn := range history {
		messages = append(messages,
			anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: turn.prompt}}},
			anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: turn.response}}},
		)
	}
	return messages
}

func webAgentTaskHistoryPromptFromEvents(ctx context.Context, svc TenantService, task mysqlstore.AgentTask) string {
	return webAgentTaskHistoryPromptFromReader(ctx, svc, svc, task)
}

func webAgentTaskHistoryPromptFromReader(ctx context.Context, svc TenantService, events agentTaskHistoryEventReader, task mysqlstore.AgentTask) string {
	inputs, err := webAgentTaskMessagesFromEventsWithReader(ctx, events, task.ID)
	if err != nil {
		observability.Error(ctx, nil, "agent.conversation_context.events_error", "server.webAgentTaskHistoryPromptFromEvents", "list web agent task events failed", "task_id", task.ID, "error", err)
		return ""
	}
	metadata := parseAgentTaskJSONMap(task.MetadataJSON)
	includeFollowup := agentTaskStringValue(metadata["source"]) == agenttasks.SourcePendingInputSideChat
	var prompts []string
	for _, input := range inputs {
		if input.Content != "" {
			prompts = append(prompts, input.Content)
			if !includeFollowup {
				break
			}
		}
	}
	return strings.Join(prompts, "\n\n")
}

func webAgentTaskMessageFromEvents(ctx context.Context, svc TenantService, taskID uint64) (agenttasks.MessageInput, error) {
	return webAgentTaskMessageFromEventsWithReader(ctx, svc, svc, taskID)
}

func webAgentTaskMessageFromEventsWithReader(ctx context.Context, _ TenantService, events agentTaskHistoryEventReader, taskID uint64) (agenttasks.MessageInput, error) {
	inputs, err := webAgentTaskMessagesFromEventsWithReader(ctx, events, taskID)
	if err != nil || len(inputs) == 0 {
		return agenttasks.MessageInput{}, err
	}
	return inputs[0], nil
}

func webAgentTaskMessagesFromEvents(ctx context.Context, svc TenantService, taskID uint64) ([]agenttasks.MessageInput, error) {
	return webAgentTaskMessagesFromEventsWithReader(ctx, svc, taskID)
}

func webAgentTaskMessagesFromEventsWithReader(ctx context.Context, events agentTaskHistoryEventReader, taskID uint64) ([]agenttasks.MessageInput, error) {
	items, err := events.ListAgentTaskEvents(ctx, taskID, 50)
	if err != nil {
		return nil, err
	}
	var inputs []agenttasks.MessageInput
	for _, event := range items {
		if event.EventType != agenttasks.EventMessage {
			continue
		}
		var input agenttasks.MessageInput
		if err := json.Unmarshal([]byte(event.PayloadJSON), &input); err != nil {
			continue
		}
		input.Content = strings.TrimSpace(input.Content)
		if input.Content != "" || len(input.Attachments) != 0 {
			inputs = append(inputs, input)
		}
	}
	return inputs, nil
}

func webAgentTaskHistoryResponse(task mysqlstore.AgentTask) string {
	result := parseAgentTaskJSONMap(task.ResultJSON)
	if strings.EqualFold(strings.TrimSpace(task.Status), agenttasks.StatusCompleted) {
		if response := strings.TrimSpace(agentTaskStringValue(result["response"])); response != "" {
			return response
		}
		if content := strings.TrimSpace(agentTaskStringValue(result["content"])); content != "" {
			return content
		}
		return ""
	}
	loopText := webAgentCapabilityLoopSummary(result["capability_loop"])
	content := strings.TrimSpace(agentTaskStringValue(result["content"]))
	errorText := strings.TrimSpace(agentTaskStringValue(result["error"]))
	status := strings.TrimSpace(task.Status)
	if status == "" {
		status = agentTaskStringValue(result["status"])
	}
	if status == "" {
		status = "terminal"
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("Previous agent task #%d %s before completion.", task.ID, status))
	if loopText != "" {
		parts = append(parts, "capability_loop: "+loopText)
	}
	if content != "" && loopText == "" {
		parts = append(parts, "partial_content: "+truncateAgentTaskEventText(content, 400))
	}
	if errorText != "" {
		parts = append(parts, "error: "+truncateAgentTaskEventText(errorText, 400))
	}
	if loopText == "" && content == "" && errorText == "" {
		return ""
	}
	parts = append(parts, "Use this as partial context for recovery; preserve evidence, unknowns, risks, verification, and next action.")
	return strings.Join(parts, "\n")
}

func webAgentCapabilityLoopSummary(value any) string {
	raw, ok := value.(map[string]any)
	if !ok || len(raw) == 0 {
		return ""
	}
	sections := []struct {
		key   string
		label string
	}{
		{key: "evidence", label: "evidence"},
		{key: "assumptions", label: "assumptions"},
		{key: "unknowns", label: "unknowns"},
		{key: "verification", label: "verification"},
		{key: "risks", label: "risks"},
		{key: "next_action", label: "next_action"},
	}
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		text := firstAgentTaskCapabilityLoopValue(raw[section.key])
		if text != "" {
			parts = append(parts, section.label+": "+truncateAgentTaskEventText(text, 220))
		}
	}
	return strings.Join(parts, " | ")
}

func firstAgentTaskCapabilityLoopValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		for _, item := range typed {
			if text := agentTaskStringValue(item); text != "" {
				return text
			}
		}
	case []string:
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				return text
			}
		}
	}
	return ""
}

func webAgentTaskLatestCompactSummary(ctx context.Context, svc TenantService, taskID uint64) string {
	return webAgentTaskLatestCompactSummaryFromReader(ctx, svc, taskID)
}

func webAgentTaskLatestCompactSummaryFromReader(ctx context.Context, eventsReader agentTaskHistoryEventReader, taskID uint64) string {
	events, err := eventsReader.ListAgentTaskEvents(ctx, taskID, 200)
	if err != nil {
		observability.Error(ctx, nil, "agent.conversation_context.compact_events_error", "server.webAgentTaskLatestCompactSummary", "list web agent compact events failed", "task_id", taskID, "error", err)
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.EventType != agenttasks.EventCompactSummary {
			continue
		}
		payload := parseAgentTaskJSONMap(event.PayloadJSON)
		summary := strings.TrimSpace(agentTaskStringValue(payload["summary"]))
		if summary != "" {
			return summary
		}
	}
	return ""
}

func truncateAgentTaskEventText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "..."
}

func rawJSONPreview(value json.RawMessage, limit int) string {
	if len(value) == 0 {
		return ""
	}
	return truncateAgentTaskEventText(string(value), limit)
}

// recoverAgentTaskRun 以 defer 调用在 detached runner 内部：把 panic 收住、记成
// 遥测，并把任务显式置 failed —— panic 后任务同样会永久停在 running。
func recoverAgentTaskRun(ctx context.Context, opts Options, task mysqlstore.AgentTask, traceID string) {
	rec := recover()
	if rec == nil {
		return
	}
	reason, stack := describeBackgroundPanic(rec)
	observability.Error(ctx, nil, "agent.run.panic", "server.runAgentTaskMessage", "agent task runner panicked",
		"agent_task_id", task.ID, "panic", reason, "stack", stack)
	emitBackgroundPanic(ctx, "server.runAgentTaskMessage", reason, stack, map[string]any{
		"agent_task_id": task.ID,
		"agent_name":    task.AgentName,
		"trace_id":      traceID,
	})
	if opts.TenantService == nil {
		return
	}
	// runCtx 可能已经因超时/取消失效，落库要用不可取消的副本。
	persistCtx := context.WithoutCancel(ctx)
	payload := agentTaskEventPayload(map[string]any{
		"source":   "runner",
		"trace_id": traceID,
		"error":    reason,
		"panic":    true,
	})
	if _, err := appendAgentTaskEvent(persistCtx, opts, task, agenttasks.EventInput{
		TaskID:      task.ID,
		EventType:   agenttasks.EventFailed,
		PayloadJSON: payload,
		TraceID:     traceID,
	}); err != nil {
		observability.Error(persistCtx, nil, "agent.run.panic", "server.runAgentTaskMessage", "append panic event failed",
			"agent_task_id", task.ID, "error", err)
	}
	if err := opts.TenantService.FinishAgentTask(persistCtx, task.ID, agenttasks.StatusFailed, payload); err != nil {
		observability.Error(persistCtx, nil, "agent.run.panic", "server.runAgentTaskMessage", "finish panicked agent task failed",
			"agent_task_id", task.ID, "error", err)
	}
	triggerPendingInputCoordinator(persistCtx, opts, task)
}

// emitAgentRunRejected 记录一次因并发上限被拒的 run，便于运维判断该不该调大上限。
func emitAgentRunRejected(ctx context.Context, task mysqlstore.AgentTask, limit int) {
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.run.rejected",
		Category:     telemetry.CategoryAgent,
		Source:       "server.tenantAgentTaskMessageHandler",
		Status:       telemetry.StatusBlocked,
		TraceID:      task.TraceID,
		Model:        task.Model,
		ResourceType: "agent_task",
		ResourceID:   strconv.FormatUint(task.ID, 10),
		Properties: map[string]any{
			"agent_name":          task.AgentName,
			"reason":              "max_concurrent_runs",
			"max_concurrent_runs": limit,
		},
	})
}

func emitAgentRunStarted(ctx context.Context, task mysqlstore.AgentTask, traceID, model, cwd, promptMode, permissionMode, effort string) {
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.run.started",
		Category:     telemetry.CategoryAgent,
		Source:       "server.runAgentTaskMessage",
		Status:       telemetry.StatusStarted,
		TraceID:      traceID,
		Model:        model,
		ResourceType: "agent_task",
		ResourceID:   strconv.FormatUint(task.ID, 10),
		Properties: map[string]any{
			"agent_name":        task.AgentName,
			"description":       task.Description,
			"parent_session_id": task.ParentSessionID,
			"conversation_id":   webAgentConversationID("", task.ParentSessionID),
			"cwd":               cwd,
			"prompt_mode":       promptMode,
			"permission_mode":   permissionMode,
			"effort":            effort,
		},
	})
}

func emitAgentRunFinished(ctx context.Context, task mysqlstore.AgentTask, traceID, model, status string, durationMS int64, result query.Result, totalTokens, contextPercent int, runErr error, extra map[string]any) {
	telemetryStatus := telemetry.StatusOK
	if status == agenttasks.StatusCancelled {
		telemetryStatus = telemetry.StatusBlocked
	} else if runErr != nil || status == agenttasks.StatusFailed || status == agenttasks.StatusTimeout {
		telemetryStatus = telemetry.StatusError
	}
	properties := map[string]any{
		"agent_name":        task.AgentName,
		"description":       task.Description,
		"parent_session_id": task.ParentSessionID,
		"conversation_id":   webAgentConversationID("", task.ParentSessionID),
		"agent_status":      status,
		"turns":             result.Turns,
		"stop_reason":       result.StopReason,
		"tool_calls":        len(result.ToolCalls),
		"context_percent":   contextPercent,
		"service_tier":      result.Usage.ServiceTier,
		"inference_geo":     result.Usage.InferenceGeo,
		"speed":             result.Usage.Speed,
	}
	for key, value := range extra {
		properties[key] = value
	}
	event := telemetry.Event{
		Name:                                "agent.run.finished",
		Category:                            telemetry.CategoryAgent,
		Source:                              "server.runAgentTaskMessage",
		Status:                              telemetryStatus,
		TraceID:                             traceID,
		Model:                               firstNonEmptyString(result.Model, model),
		ResourceType:                        "agent_task",
		ResourceID:                          strconv.FormatUint(task.ID, 10),
		DurationMS:                          durationMS,
		InputTokens:                         result.Usage.InputTokens,
		OutputTokens:                        result.Usage.OutputTokens,
		CacheCreationInputTokens:            result.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:                result.Usage.CacheReadInputTokens,
		CacheCreationEphemeral1hInputTokens: result.Usage.CacheCreationEphemeral1hInputTokens,
		CacheCreationEphemeral5mInputTokens: result.Usage.CacheCreationEphemeral5mInputTokens,
		Properties:                          properties,
	}
	if runErr != nil {
		event.Error = runErr.Error()
	}
	telemetry.Emit(ctx, event)
}

func ensureAgentTaskMetadataTrace(metadataJSON, traceID string) string {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return metadataJSON
	}
	metadata := parseAgentTaskJSONMap(metadataJSON)
	if strings.TrimSpace(agentTaskStringValue(metadata["run_trace_id"])) == "" {
		metadata["run_trace_id"] = traceID
	}
	if strings.TrimSpace(agentTaskStringValue(metadata["trace_id"])) == "" {
		metadata["trace_id"] = traceID
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return metadataJSON
	}
	return string(data)
}

func newWebAgentTraceID() string {
	return "web-agent-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 36)
}

func agentTaskRunTraceID(ctx context.Context, task mysqlstore.AgentTask, requested string) string {
	metadata := parseAgentTaskJSONMap(task.MetadataJSON)
	return firstNonEmptyString(
		task.TraceID,
		agentTaskStringValue(metadata["run_trace_id"]),
		agentTaskStringValue(metadata["trace_id"]),
		requested,
		observability.TraceID(ctx),
	)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeAgentTaskPromptMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "code":
		return "code"
	case "":
		return "code"
	default:
		return "chat"
	}
}

var _ query.EventSink = (*agentTaskTextSink)(nil)

type agentTaskTextSink struct {
	ctx                    context.Context
	svc                    TenantService
	opts                   Options
	task                   mysqlstore.AgentTask
	permissions            *AgentTaskPermissionRegistry
	questions              *AgentTaskQuestionRegistry
	taskID                 uint64
	traceID                string
	text                   *strings.Builder
	idleTimeout            time.Duration
	idleCancel             context.CancelCauseFunc
	watchdogMu             sync.Mutex
	lastWrite              time.Time
	stopWatch              chan struct{}
	activeLongRunningTools map[string]struct{}
	waitingUserQuestions   int
	toolMu                 sync.Mutex
	toolSeen               map[string]bool
}

func (s *agentTaskTextSink) Write(p []byte) (int, error) {
	text := string(p)
	// Stream fragments may contain only a word separator or code indentation.
	// Preserve every nonempty fragment so replay matches the provider output.
	if text == "" {
		return len(p), nil
	}
	if s.text != nil {
		_, _ = s.text.WriteString(text)
	}
	s.markWrite()
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	opts, task := s.eventOptions()
	if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
		TaskID:      s.taskID,
		EventType:   agenttasks.EventTextDelta,
		PayloadJSON: agentTaskEventPayload(map[string]any{"source": "runner", "content": text}),
		TraceID:     s.traceID,
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *agentTaskTextSink) OnThinking(ctx context.Context, text string) error {
	// Whitespace belongs to the stream, including boundaries between fragments.
	if text == "" {
		return nil
	}
	s.markWrite()
	return s.appendEvent(ctx, agenttasks.EventThinking, map[string]any{"source": "runner", "content": text})
}

func (s *agentTaskTextSink) OnToolCall(ctx context.Context, event query.ToolCallEvent) error {
	if strings.TrimSpace(event.ID) == "" && strings.TrimSpace(event.Name) == "" {
		return nil
	}
	s.markTool(event.ID)
	s.beginLongRunningTool(event.ID, event.Name)
	return s.appendEvent(ctx, agenttasks.EventToolCall, map[string]any{
		"source":    "runner",
		"tool_id":   event.ID,
		"tool_name": event.Name,
		"input":     truncateAgentTaskEventText(string(event.Input), 600),
	})
}

func (s *agentTaskTextSink) OnToolResult(ctx context.Context, trace query.ToolTrace) error {
	if strings.TrimSpace(trace.ID) == "" && strings.TrimSpace(trace.Name) == "" {
		return nil
	}
	s.markTool(trace.ID)
	s.endLongRunningTool(trace.ID, trace.Name)
	resultPayload := map[string]any{
		"source":    "runner",
		"tool_id":   trace.ID,
		"tool_name": trace.Name,
		"is_error":  trace.IsError,
		"input":     truncateAgentTaskEventText(trace.Input, 600),
		"preview":   truncateAgentTaskEventText(trace.Output, 160),
		"output":    truncateAgentTaskEventText(trace.Output, 2000),
	}
	if trace.Name == "ComputerUse" {
		for key, value := range computeruse.ResultMetadata(trace.Output) {
			resultPayload[key] = value
		}
	}
	if trace.ComputerObservation != nil {
		resultPayload["computer_observation"] = trace.ComputerObservation
	}
	if err := s.appendEvent(ctx, agenttasks.EventToolResult, resultPayload); err != nil {
		return err
	}
	// trace.FileChanges is already populated by the tools that touched files, so
	// the Files tab reads structured rows instead of guessing at payload keys.
	opts, task := s.eventOptions()
	return appendAgentTaskFileChangeEventsWithOptions(ctx, opts, task, s.traceID, "runner", trace)
}

func (s *agentTaskTextSink) OnUsage(ctx context.Context, turn int, usage query.Usage) error {
	if queryUsageTotalTokens(usage) == 0 && usage.CacheCreationInputTokens == 0 && usage.CacheReadInputTokens == 0 {
		return nil
	}
	return s.appendEvent(ctx, agenttasks.EventUsage, map[string]any{
		"source":                      "runner",
		"turn":                        turn,
		"input_tokens":                usage.InputTokens,
		"output_tokens":               usage.OutputTokens,
		"total_tokens":                queryUsageTotalTokens(usage),
		"cache_creation_input_tokens": usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     usage.CacheReadInputTokens,
		"cache_creation_ephemeral_1h_input_tokens": usage.CacheCreationEphemeral1hInputTokens,
		"cache_creation_ephemeral_5m_input_tokens": usage.CacheCreationEphemeral5mInputTokens,
		"service_tier":  usage.ServiceTier,
		"inference_geo": usage.InferenceGeo,
		"speed":         usage.Speed,
	})
}

func (s *agentTaskTextSink) OnMessageStop(ctx context.Context, turn int, stopReason string, usage query.Usage) error {
	return s.appendEvent(ctx, agenttasks.EventMessageStop, map[string]any{
		"source":        "runner",
		"turn":          turn,
		"stop_reason":   stopReason,
		"input_tokens":  usage.InputTokens,
		"output_tokens": usage.OutputTokens,
	})
}

func (s *agentTaskTextSink) OnCompact(ctx context.Context, result compact.Result) error {
	if !result.Compacted {
		return nil
	}
	return s.appendEvent(ctx, agenttasks.EventCompactSummary, map[string]any{
		"source":             "runner",
		"summary":            result.PersistedSummary,
		"trigger_tokens":     result.Metadata.TriggerTokens,
		"token_after":        result.Metadata.TokenAfter,
		"compacted_messages": result.Metadata.CompactedMessages,
		"preserved_messages": result.Metadata.PreservedMessages,
		"model":              result.Metadata.Model,
		"estimated_tokens":   result.EstimatedUsage,
	})
}

func (s *agentTaskTextSink) OnPermissionRequest(ctx context.Context, req tools.PermissionPromptRequest) (tools.PermissionPromptResponse, error) {
	requestID := fmt.Sprintf("perm-%d-%d", s.taskID, time.Now().UTC().UnixNano())
	if s.permissions == nil {
		response := tools.PermissionPromptResponse{Allowed: false, Reason: "permission registry is not configured", Decision: "deny"}
		_ = s.appendEvent(ctx, agenttasks.EventPermissionResolved, map[string]any{"source": "runner", "request_id": requestID, "allowed": false, "decision": "deny", "reason": response.Reason})
		return response, nil
	}
	wait := s.permissions.Register(requestID)
	defer s.permissions.Remove(requestID)
	payload := map[string]any{
		"source":     "runner",
		"request_id": requestID,
		"tool_name":  req.ToolName,
		"reason":     req.Reason,
		"request":    req.Request,
		"rule":       req.Rule,
		"input":      rawJSONPreview(req.Input, 800),
	}
	if err := s.appendEvent(ctx, agenttasks.EventPermissionRequest, payload); err != nil {
		return tools.PermissionPromptResponse{}, err
	}
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	select {
	case response := <-wait:
		return response, nil
	case <-ctx.Done():
		return tools.PermissionPromptResponse{Allowed: false, Reason: ctx.Err().Error(), Decision: "deny"}, nil
	case <-timer.C:
		response := tools.PermissionPromptResponse{Allowed: false, Reason: "permission request timed out", Decision: "deny"}
		_ = s.appendEvent(ctx, agenttasks.EventPermissionResolved, map[string]any{"source": "runner", "request_id": requestID, "allowed": false, "decision": "deny", "reason": response.Reason})
		return response, nil
	}
}

// OnNestedAgentProgress 把 sub-agent(Task 工具子代理)的进度事件落成父任务的一条
// nested_agent_progress 事件,原始 EventType/TaskID/payload 内嵌在 payload 里,
// 避免污染父任务自身的 tool_call/tool_result 事件流。前端据此聚合出 sub-agent 卡片。
func (s *agentTaskTextSink) OnNestedAgentProgress(ctx context.Context, event agenttasks.EventInput) error {
	if event.EventType == agenttasks.EventImageArtifact {
		// Image artifacts are first-class persisted outputs. Keep the stable
		// event type so WebUI consumers can render them without unpacking nested
		// progress, while the payload remains metadata-only.
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.PayloadJSON), &payload); err != nil {
			return s.appendEvent(ctx, agenttasks.EventImageArtifact, map[string]any{"artifact": event.PayloadJSON})
		}
		return s.appendEvent(ctx, agenttasks.EventImageArtifact, payload)
	}
	payload := map[string]any{
		"source":         "runner",
		"sub_task_id":    event.TaskID,
		"sub_event_type": event.EventType,
	}
	if raw := strings.TrimSpace(event.PayloadJSON); raw != "" {
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			payload["sub_payload"] = parsed
		} else {
			payload["sub_payload_raw"] = raw
		}
	}
	return s.appendEvent(ctx, agenttasks.EventNestedProgress, payload)
}

func (s *agentTaskTextSink) appendEvent(ctx context.Context, eventType string, payload map[string]any) error {
	if ctx == nil {
		ctx = s.ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	opts, task := s.eventOptions()
	_, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
		TaskID:      s.taskID,
		EventType:   eventType,
		PayloadJSON: agentTaskEventPayload(payload),
		TraceID:     s.traceID,
	})
	return err
}

func (s *agentTaskTextSink) eventOptions() (Options, mysqlstore.AgentTask) {
	opts := s.opts
	if opts.TenantService == nil {
		opts.TenantService = s.svc
	}
	task := s.task
	if task.ID == 0 {
		task.ID = s.taskID
	}
	return opts, task
}

func (s *agentTaskTextSink) markTool(toolID string) {
	if strings.TrimSpace(toolID) == "" {
		return
	}
	s.toolMu.Lock()
	if s.toolSeen == nil {
		s.toolSeen = map[string]bool{}
	}
	s.toolSeen[toolID] = true
	s.toolMu.Unlock()
}

func (s *agentTaskTextSink) toolIDs() map[string]bool {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()
	if len(s.toolSeen) == 0 {
		return nil
	}
	out := make(map[string]bool, len(s.toolSeen))
	for key, value := range s.toolSeen {
		out[key] = value
	}
	return out
}

func (s *agentTaskTextSink) startIdleWatchdog() {
	if s == nil || s.idleTimeout <= 0 || s.idleCancel == nil {
		return
	}
	s.watchdogMu.Lock()
	s.lastWrite = time.Now()
	s.stopWatch = make(chan struct{})
	stop := s.stopWatch
	s.watchdogMu.Unlock()
	goSafe(s.ctx, "server.agentTaskTextSink.startIdleWatchdog", map[string]any{"agent_task_id": s.taskID}, func() {
		ticker := time.NewTicker(minAgentTaskWatchdogInterval(s.idleTimeout))
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.watchdogMu.Lock()
				if len(s.activeLongRunningTools) > 0 || s.waitingUserQuestions > 0 {
					s.lastWrite = time.Now()
					s.watchdogMu.Unlock()
					continue
				}
				idleFor := time.Since(s.lastWrite)
				s.watchdogMu.Unlock()
				if idleFor >= s.idleTimeout {
					s.idleCancel(fmt.Errorf("agent task stream idle timeout after %s", s.idleTimeout))
					return
				}
			}
		}
	})
}

func (s *agentTaskTextSink) stopIdleWatchdog() {
	if s == nil {
		return
	}
	s.watchdogMu.Lock()
	stop := s.stopWatch
	s.stopWatch = nil
	s.watchdogMu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (s *agentTaskTextSink) markWrite() {
	if s == nil || s.idleTimeout <= 0 {
		return
	}
	s.watchdogMu.Lock()
	s.lastWrite = time.Now()
	s.watchdogMu.Unlock()
}

const (
	generateImageToolName = "GenerateImage"
	editImageToolName     = "EditImage"
)

func isLongRunningImageTool(name string) bool {
	switch strings.TrimSpace(name) {
	case generateImageToolName, editImageToolName:
		return true
	default:
		return false
	}
}

func (s *agentTaskTextSink) beginLongRunningTool(toolID, toolName string) {
	if s == nil || !isLongRunningImageTool(toolName) {
		return
	}
	key := strings.TrimSpace(toolID)
	if key == "" {
		key = strings.TrimSpace(toolName)
	}
	s.watchdogMu.Lock()
	if s.activeLongRunningTools == nil {
		s.activeLongRunningTools = make(map[string]struct{})
	}
	s.activeLongRunningTools[key] = struct{}{}
	s.lastWrite = time.Now()
	s.watchdogMu.Unlock()
}

func (s *agentTaskTextSink) endLongRunningTool(toolID, toolName string) {
	if s == nil {
		return
	}
	key := strings.TrimSpace(toolID)
	if key == "" {
		key = strings.TrimSpace(toolName)
	}
	s.watchdogMu.Lock()
	if !isLongRunningImageTool(toolName) {
		if _, ok := s.activeLongRunningTools[key]; !ok {
			s.watchdogMu.Unlock()
			return
		}
	}
	delete(s.activeLongRunningTools, key)
	s.lastWrite = time.Now()
	s.watchdogMu.Unlock()
}

func finishAgentTaskCancelled(ctx context.Context, opts Options, task mysqlstore.AgentTask, traceID string, startedAt time.Time, content string) (agentTaskRunResult, error) {
	payload := agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{
		Source:     "runner",
		TraceID:    traceID,
		DurationMS: time.Since(startedAt).Milliseconds(),
		Content:    content,
	})
	if _, appendErr := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{TaskID: task.ID, EventType: agenttasks.EventCancelled, PayloadJSON: payload, TraceID: traceID}); appendErr != nil {
		return agentTaskRunResult{}, appendErr
	}
	if finishErr := opts.TenantService.FinishAgentTask(ctx, task.ID, agenttasks.StatusCancelled, payload); finishErr != nil {
		return agentTaskRunResult{}, finishErr
	}
	return agentTaskRunResult{Status: agenttasks.StatusCancelled}, nil
}

func isTerminalAgentTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

func parseAgentTaskJSONMap(value string) map[string]any {
	var out map[string]any
	if strings.TrimSpace(value) == "" {
		return map[string]any{}
	}
	if err := json.Unmarshal([]byte(value), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func agentTaskStringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func queryUsageTotalTokens(usage query.Usage) int {
	return usage.InputTokens + usage.OutputTokens
}

func agentTaskContextLength(cwd, model string) int {
	settings := config.LoadSettings(cwd).Settings
	return compact.ConfigFromSettings(settings, model).ContextTokens(model)
}

func agentTaskRunTimeout(opts Options) time.Duration {
	if opts.AgentTaskRunTimeout > 0 {
		return opts.AgentTaskRunTimeout
	}
	return 20 * time.Minute
}

func agentTaskIdleTimeout(opts Options) time.Duration {
	if opts.AgentTaskIdleTimeout > 0 {
		return opts.AgentTaskIdleTimeout
	}
	return 2 * time.Minute
}

func minAgentTaskWatchdogInterval(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return time.Second
	}
	interval := timeout / 4
	if interval < 250*time.Millisecond {
		return 250 * time.Millisecond
	}
	if interval > 5*time.Second {
		return 5 * time.Second
	}
	return interval
}

type AgentTaskPermissionRegistry struct {
	mu      sync.Mutex
	pending map[string]chan tools.PermissionPromptResponse
}

func NewAgentTaskPermissionRegistry() *AgentTaskPermissionRegistry {
	return &AgentTaskPermissionRegistry{pending: map[string]chan tools.PermissionPromptResponse{}}
}

func (r *AgentTaskPermissionRegistry) Register(requestID string) <-chan tools.PermissionPromptResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch := make(chan tools.PermissionPromptResponse, 1)
	r.pending[requestID] = ch
	return ch
}

func (r *AgentTaskPermissionRegistry) Resolve(requestID string, response tools.PermissionPromptResponse) bool {
	r.mu.Lock()
	ch := r.pending[requestID]
	if ch != nil {
		delete(r.pending, requestID)
	}
	r.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- response
	return true
}

func (r *AgentTaskPermissionRegistry) Remove(requestID string) {
	r.mu.Lock()
	delete(r.pending, requestID)
	r.mu.Unlock()
}

type agentTaskPermissionRequest struct {
	Allowed     bool   `json:"allowed"`
	Reason      string `json:"reason,omitempty"`
	Destination string `json:"destination,omitempty"`
	Rule        string `json:"rule,omitempty"`
}

func tenantAgentTaskPermissionHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task_permission", "server.tenantAgentTaskPermissionHandler", "resolve agent task permission")
		if r.Method != http.MethodPatch && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		taskID, requestID, ok := tenantAgentTaskPermissionPath(r.URL.Path)
		if !ok || taskID == 0 || requestID == "" {
			writeTenantError(w, http.StatusBadRequest, "agent task permission request id is required")
			return
		}
		task, err := ensureTenantOwnsAgentTask(r.Context(), opts.TenantService, taskID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if opts.AgentTaskPermissions == nil {
			writeTenantError(w, http.StatusConflict, "agent task permission registry is not configured")
			return
		}
		var req agentTaskPermissionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeTenantError(w, http.StatusBadRequest, err.Error())
			return
		}
		response := tools.PermissionPromptResponse{
			Allowed:     req.Allowed,
			Reason:      strings.TrimSpace(req.Reason),
			Destination: strings.TrimSpace(req.Destination),
			Rule:        strings.TrimSpace(req.Rule),
			Decision:    mapPermissionDecision(req.Allowed),
		}
		if !opts.AgentTaskPermissions.Resolve(requestID, response) {
			writeTenantError(w, http.StatusNotFound, "permission request is not pending")
			return
		}
		payload := agentTaskEventPayload(map[string]any{
			"source":      "webui",
			"request_id":  requestID,
			"allowed":     response.Allowed,
			"decision":    response.Decision,
			"reason":      response.Reason,
			"destination": response.Destination,
		})
		if _, err := appendAgentTaskEvent(r.Context(), opts, task, agenttasks.EventInput{
			TaskID:      task.ID,
			EventType:   agenttasks.EventPermissionResolved,
			PayloadJSON: payload,
			TraceID:     task.TraceID,
		}); err != nil {
			writeTenantServiceError(w, err)
			return
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_task.permission.resolve", "agent_task", taskID)
		writeJSON(w, map[string]any{"id": taskID, "request_id": requestID, "allowed": response.Allowed})
	})
}

func mapPermissionDecision(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}

func tenantAgentTaskCancelHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_agent_task_cancel", "server.tenantAgentTaskCancelHandler", "cancel tenant agent task")
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		taskID, ok := tenantAgentTaskIDFromPath(r.URL.Path, "cancel")
		if !ok || taskID == 0 {
			writeTenantError(w, http.StatusBadRequest, errMsgAgentTaskIDRequired)
			return
		}
		task, err := ensureTenantOwnsAgentTask(r.Context(), opts.TenantService, taskID)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		if rejectWorkflowTaskMutation(w, r, opts, taskID) {
			return
		}
		if task.Status != agenttasks.StatusRunning {
			writeTenantError(w, http.StatusConflict, "agent task is not running")
			return
		}
		cancelledInProcess := false
		if opts.AgentTaskController != nil {
			cancelledInProcess = opts.AgentTaskController.Cancel(taskID)
		}
		if !cancelledInProcess {
			resultJSON := agenttasks.CancelledResultJSON(agenttasks.CancelledResultOptions{Source: "api"})
			if err := opts.TenantService.CancelAgentTask(r.Context(), taskID, resultJSON); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			triggerPendingInputCoordinator(r.Context(), opts, task)
		}
		recordTenantAudit(r.Context(), opts.TenantService, "tenant.agent_task.cancel", "agent_task", taskID)
		writeJSON(w, map[string]any{"id": taskID, "cancelled": true, "in_process": cancelledInProcess})
	})
}

func ensureTenantOwnsAgentTask(ctx context.Context, svc TenantService, taskID uint64) (mysqlstore.AgentTask, error) {
	return svc.GetAgentTask(ctx, taskID)
}

func tenantAgentTaskIDFromPath(path, suffix string) (uint64, bool) {
	raw := strings.TrimPrefix(path, "/tenant/agent-tasks/")
	if raw == "" || raw == path {
		return 0, false
	}
	suffix = strings.Trim(suffix, "/")
	if suffix != "" {
		expectedSuffix := "/" + suffix
		if !strings.HasSuffix(raw, expectedSuffix) {
			return 0, false
		}
		raw = strings.TrimSuffix(raw, expectedSuffix)
	} else if strings.Contains(raw, "/") {
		return 0, false
	}
	if raw == "" || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	return id, err == nil
}

func tenantAgentTaskPermissionPath(path string) (uint64, string, bool) {
	raw := strings.TrimPrefix(path, "/tenant/agent-tasks/")
	if raw == "" || raw == path {
		return 0, "", false
	}
	parts := strings.Split(raw, "/")
	if len(parts) != 3 || parts[1] != "permissions" {
		return 0, "", false
	}
	id, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || strings.TrimSpace(parts[2]) == "" {
		return 0, "", false
	}
	return id, parts[2], true
}

type agentTaskCreateRequest struct {
	ParentSessionID    uint64          `json:"parent_session_id,omitempty"`
	SubagentSessionKey string          `json:"subagent_session_key,omitempty"`
	AgentName          string          `json:"agent_name,omitempty"`
	Description        string          `json:"description,omitempty"`
	Prompt             string          `json:"prompt,omitempty"`
	Status             string          `json:"status,omitempty"`
	Model              string          `json:"model,omitempty"`
	ResultJSON         json.RawMessage `json:"result_json,omitempty"`
	MetadataJSON       json.RawMessage `json:"metadata_json,omitempty"`
	TraceID            string          `json:"trace_id,omitempty"`
}

func (r agentTaskCreateRequest) toTaskInput() (agenttasks.TaskInput, error) {
	status := strings.TrimSpace(r.Status)
	if status == "" {
		status = agenttasks.StatusRunning
	}
	if !isValidAgentTaskStatus(status) {
		return agenttasks.TaskInput{}, fmt.Errorf("invalid status %q", status)
	}
	return agenttasks.TaskInput{
		ParentSessionID:    r.ParentSessionID,
		SubagentSessionKey: strings.TrimSpace(r.SubagentSessionKey),
		AgentName:          strings.TrimSpace(r.AgentName),
		Description:        strings.TrimSpace(r.Description),
		Prompt:             r.Prompt,
		Status:             status,
		Model:              strings.TrimSpace(r.Model),
		ResultJSON:         rawJSONToString(r.ResultJSON),
		MetadataJSON:       rawJSONToString(r.MetadataJSON),
		TraceID:            strings.TrimSpace(r.TraceID),
	}, nil
}

type agentTaskUpdateRequest struct {
	Status       string          `json:"status,omitempty"`
	ResultJSON   json.RawMessage `json:"result_json,omitempty"`
	MetadataJSON json.RawMessage `json:"metadata_json,omitempty"`
}

func (r agentTaskUpdateRequest) toTaskUpdate() (agenttasks.TaskUpdate, string, error) {
	status := strings.TrimSpace(r.Status)
	if status == "" && len(r.ResultJSON) == 0 && len(r.MetadataJSON) == 0 {
		return agenttasks.TaskUpdate{}, "", fmt.Errorf("status, result_json, or metadata_json is required")
	}
	if status != "" && !isValidAgentTaskStatus(status) {
		return agenttasks.TaskUpdate{}, "", fmt.Errorf("invalid status %q", status)
	}
	update := agenttasks.TaskUpdate{
		Status:       status,
		ResultJSON:   rawJSONToString(r.ResultJSON),
		MetadataJSON: rawJSONToString(r.MetadataJSON),
	}
	eventType := ""
	switch status {
	case agenttasks.StatusCompleted:
		eventType = agenttasks.EventCompleted
	case agenttasks.StatusFailed:
		eventType = agenttasks.EventFailed
	case agenttasks.StatusCancelled:
		eventType = agenttasks.EventCancelled
	case agenttasks.StatusTimeout:
		eventType = agenttasks.EventTimeout
	}
	return update, eventType, nil
}

type agentTaskMessageRequest struct {
	FromAgent   string                       `json:"from_agent,omitempty"`
	Content     string                       `json:"content"`
	TraceID     string                       `json:"trace_id,omitempty"`
	Attachments []agentTaskAttachmentRequest `json:"attachments,omitempty"`
}

type agentTaskAttachmentRequest struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	Type         string `json:"type"`
	MediaType    string `json:"media_type,omitempty"`
	Name         string `json:"name,omitempty"`
	URL          string `json:"url,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	InlineData   string `json:"inline_data,omitempty"`
}

func (r agentTaskMessageRequest) toMessageInput(taskID uint64) (agenttasks.MessageInput, error) {
	content := strings.TrimSpace(r.Content)
	if content == "" && len(r.Attachments) == 0 {
		return agenttasks.MessageInput{}, fmt.Errorf(errMsgContentRequired)
	}
	attachments := make([]agenttasks.Attachment, 0, len(r.Attachments))
	if len(r.Attachments) > maxAgentTaskAttachments {
		return agenttasks.MessageInput{}, fmt.Errorf("too many attachments: max %d", maxAgentTaskAttachments)
	}
	for _, attachment := range r.Attachments {
		item, err := normalizeAgentTaskAttachment(attachment)
		if err != nil {
			return agenttasks.MessageInput{}, err
		}
		attachments = append(attachments, item)
	}
	return agenttasks.MessageInput{
		TaskID:      taskID,
		FromAgent:   strings.TrimSpace(r.FromAgent),
		Content:     content,
		TraceID:     strings.TrimSpace(r.TraceID),
		Attachments: attachments,
	}, nil
}

const (
	maxAgentTaskAttachments           = 8
	maxAgentTaskAttachmentBytes int64 = 25 * 1024 * 1024
)

func normalizeAgentTaskAttachment(input agentTaskAttachmentRequest) (agenttasks.Attachment, error) {
	attachmentType := strings.ToLower(strings.TrimSpace(input.Type))
	if attachmentType != "image" {
		return agenttasks.Attachment{}, fmt.Errorf("attachment type %q is not supported; only image is allowed", input.Type)
	}
	mediaType := strings.ToLower(strings.TrimSpace(input.MediaType))
	if mediaType == "" || !strings.HasPrefix(mediaType, "image/") {
		return agenttasks.Attachment{}, errors.New("image attachment media_type must be image/*")
	}
	if input.SizeBytes <= 0 || input.SizeBytes > maxAgentTaskAttachmentBytes {
		return agenttasks.Attachment{}, fmt.Errorf("image attachment size must be between 1 and %d bytes", maxAgentTaskAttachmentBytes)
	}
	inlineData := strings.TrimSpace(input.InlineData)
	if inlineData != "" {
		decoded, err := base64.StdEncoding.DecodeString(inlineData)
		if err != nil {
			return agenttasks.Attachment{}, errors.New("image attachment inline_data is invalid base64")
		}
		if int64(len(decoded)) > maxAgentTaskAttachmentBytes {
			return agenttasks.Attachment{}, fmt.Errorf("image attachment inline_data exceeds %d bytes", maxAgentTaskAttachmentBytes)
		}
	}
	attachmentURL := strings.TrimSpace(input.URL)
	if inlineData == "" && attachmentURL == "" {
		return agenttasks.Attachment{}, errors.New("image attachment requires url or inline_data")
	}
	if attachmentURL != "" {
		parsed, err := url.Parse(attachmentURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return agenttasks.Attachment{}, errors.New("image attachment url must be an absolute http(s) URL")
		}
	}
	return agenttasks.Attachment{
		AttachmentID: strings.TrimSpace(input.AttachmentID),
		Type:         attachmentType,
		MediaType:    mediaType,
		Name:         strings.TrimSpace(input.Name),
		URL:          attachmentURL,
		SizeBytes:    input.SizeBytes,
		SHA256:       strings.TrimSpace(input.SHA256),
		InlineData:   inlineData,
	}, nil
}

func rawJSONToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}

func agentTaskEventPayload(values map[string]any) string {
	clean := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			// Content can be a standalone stream separator or indentation.
			if typed == "" || (key != "content" && strings.TrimSpace(typed) == "") {
				continue
			}
			clean[key] = typed
		case nil:
			continue
		default:
			clean[key] = typed
		}
	}
	payload, err := json.Marshal(clean)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func isValidAgentTaskStatus(status string) bool {
	switch status {
	case agenttasks.StatusReady, agenttasks.StatusRunning, agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

// agentTaskNextStepsTimeout 界定异步元调用的阻塞时长。建议事件允许在
// FinishAgentTask 之后追加；Web 客户端会在终态后短暂排空 SSE 尾部事件。
const agentTaskNextStepsTimeout = 6 * time.Second

// appendAgentTaskNextSteps 追加一轮的下一步候选。任何失败都静默返回：一条
// 错误的引导比没有引导更糟,更不该阻断 turn 收尾。这也包括 panic——
// NextStepsFunc 会一路调用到 provider 客户端和 nextsteps.Parse,任何一处
// panic 若不在这里收住,会被外层 recoverAgentTaskRun 接住,把一个已经
// EventCompleted 落库、且已经流给浏览器的 turn 错误地改判成 failed。
func appendAgentTaskNextSteps(ctx context.Context, opts Options, task mysqlstore.AgentTask, cwd, source, traceID, prompt, response, model, provider string, toolNames []string) {
	cfg, ok := agentTaskNextStepsConfig(opts, task, cwd, source, response, model)
	if !ok {
		return
	}
	appendAgentTaskNextStepsWithConfig(ctx, opts, task, cwd, source, traceID, prompt, response, provider, toolNames, cfg)
}

func agentTaskNextStepsConfig(opts Options, task mysqlstore.AgentTask, cwd, source, response, model string) (nextsteps.Config, bool) {
	if opts.NextStepsFunc == nil || opts.TenantService == nil || !opts.NextStepsWebEnabled {
		return nextsteps.Config{}, false
	}
	if strings.TrimSpace(source) != "webui-agent" || strings.TrimSpace(response) == "" {
		return nextsteps.Config{}, false
	}
	cfg := nextsteps.ConfigFromSettings(config.LoadSettings(cwd).Settings, model)
	if !cfg.Enabled {
		return nextsteps.Config{}, false
	}
	return cfg, true
}

func appendAgentTaskNextStepsWithConfig(ctx context.Context, opts Options, task mysqlstore.AgentTask, cwd, source, traceID, prompt, response, provider string, toolNames []string, cfg nextsteps.Config) {
	startedAt := time.Now()
	outcome := "unknown"
	errorClass := ""
	defer func() {
		if rec := recover(); rec != nil {
			outcome = "panic"
			errorClass = "panic"
			observability.Error(ctx, nil, "agent.next_steps.panic", "server.appendAgentTaskNextSteps", "next-step suggestion generation panicked", "task_id", task.ID, "error_class", "panic")
		}
		emitAgentTaskNextStepsFinished(ctx, task, traceID, time.Since(startedAt), outcome, errorClass)
	}()
	if opts.NextStepsFunc == nil || opts.TenantService == nil {
		outcome = "not_configured"
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, agentTaskNextStepsTimeout)
	defer cancel()
	suggestions, err := opts.NextStepsFunc(callCtx, prompt, response, toolNames, cfg.Model, provider, cfg.Count)
	if err != nil {
		errorClass = nextStepsErrorClass(err)
		outcome = errorClass
		observability.Error(ctx, nil, "agent.next_steps.failed", "server.appendAgentTaskNextSteps", "generate next-step suggestions failed", "task_id", task.ID, "error_class", nextStepsErrorClass(err))
		return
	}
	if len(suggestions) == 0 {
		outcome = "empty"
		return
	}
	if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
		TaskID:      task.ID,
		EventType:   agenttasks.EventNextSteps,
		PayloadJSON: agentTaskEventPayload(map[string]any{"source": "runner", "suggestions": suggestions}),
		TraceID:     traceID,
	}); err != nil {
		outcome = "append_error"
		errorClass = "storage_error"
		observability.Error(ctx, nil, "agent.next_steps.append_failed", "server.appendAgentTaskNextSteps", "append next_steps event failed", "task_id", task.ID, "error_class", "storage_error")
		return
	}
	outcome = "emitted"
}

func emitAgentTaskNextStepsQueued(ctx context.Context, task mysqlstore.AgentTask, traceID string, accepted bool) {
	outcome := "rejected"
	status := telemetry.StatusBlocked
	if accepted {
		outcome = "accepted"
		status = telemetry.StatusOK
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.next_steps.queued",
		Category:     telemetry.CategoryAgent,
		Source:       "server.runAgentTaskMessage",
		Status:       status,
		TraceID:      traceID,
		ResourceType: "agent_task",
		ResourceID:   strconv.FormatUint(task.ID, 10),
		Properties:   map[string]any{"outcome": outcome},
	})
}

func emitAgentTaskNextStepsFinished(ctx context.Context, task mysqlstore.AgentTask, traceID string, duration time.Duration, outcome, errorClass string) {
	status := telemetry.StatusOK
	if outcome == "panic" || outcome == "provider_error" || outcome == "timeout" || outcome == "canceled" || outcome == "append_error" {
		status = telemetry.StatusError
	}
	properties := map[string]any{"outcome": outcome}
	if errorClass != "" {
		properties["error_class"] = errorClass
	}
	telemetry.Emit(ctx, telemetry.Event{
		Name:         "agent.next_steps.finished",
		Category:     telemetry.CategoryAgent,
		Source:       "server.appendAgentTaskNextSteps",
		Status:       status,
		TraceID:      traceID,
		ResourceType: "agent_task",
		ResourceID:   strconv.FormatUint(task.ID, 10),
		DurationMS:   duration.Milliseconds(),
		Properties:   properties,
	})
}

func appendAgentTaskNextStepsDropped(ctx context.Context, opts Options, task mysqlstore.AgentTask, traceID string) {
	if opts.TenantService == nil {
		return
	}
	if _, err := appendAgentTaskEvent(ctx, opts, task, agenttasks.EventInput{
		TaskID:      task.ID,
		EventType:   agenttasks.EventNextSteps,
		PayloadJSON: agentTaskEventPayload(map[string]any{"source": "runner", "suggestions": []string{}, "status": "dropped"}),
		TraceID:     traceID,
	}); err != nil {
		observability.Error(ctx, nil, "agent.next_steps.drop_append_failed", "server.runAgentTaskMessage", "append dropped next_steps event failed", "task_id", task.ID, "error_class", "storage_error")
	}
}

func nextStepsErrorClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "provider_error"
	}
}

// agentTaskNextStepsToolNames 从这一轮的工具调用里提炼名字列表,口径和 TUI
// 侧 toolNamesFromResult 一致:去空白、跳过空名。
func agentTaskNextStepsToolNames(calls []query.ToolTrace) []string {
	var names []string
	for _, call := range calls {
		if name := strings.TrimSpace(call.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
