package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/konglong87/super-niuma/docs"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
	tenantservice "github.com/konglong87/super-niuma/internal/tenant"
)

func tenantSessionsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_sessions", "server.tenantSessionsHandler", "handle tenant sessions")
		switch r.Method {
		case http.MethodGet:
			items, err := opts.TenantService.ListSessions(r.Context(), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req tenantservice.SessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if strings.TrimSpace(req.SessionKey) == "" {
				writeTenantError(w, http.StatusBadRequest, "session_key is required")
				return
			}
			id, err := opts.TenantService.UpsertSession(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.session.upsert", "session", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func tenantSessionHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_session", "server.tenantSessionHandler", "handle tenant session")
		sessionID, ok := tenantSessionIDFromPath(r.URL.Path)
		if !ok || sessionID == 0 {
			writeTenantError(w, http.StatusBadRequest, "session id is required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			item, err := opts.TenantService.GetSession(r.Context(), sessionID)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, item)
		case http.MethodPatch, http.MethodPut:
			var req tenantservice.SessionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := opts.TenantService.UpdateSession(r.Context(), sessionID, req); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.session.update", "session", sessionID)
			writeJSON(w, map[string]any{"id": sessionID})
		case http.MethodDelete:
			if err := opts.TenantService.ArchiveSession(r.Context(), sessionID); err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.session.archive", "session", sessionID)
			writeJSON(w, map[string]any{"id": sessionID, "archived": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

type webAgentConversation struct {
	ID            string                 `json:"id"`
	SessionID     uint64                 `json:"session_id,omitempty"`
	SessionKey    string                 `json:"session_key,omitempty"`
	Source        string                 `json:"source,omitempty"`
	Title         string                 `json:"title"`
	CWD           string                 `json:"cwd,omitempty"`
	WorkspaceName string                 `json:"workspace_name,omitempty"`
	Status        string                 `json:"status"`
	UpdatedAt     time.Time              `json:"updated_at,omitempty"`
	Session       *mysqlstore.Session    `json:"session,omitempty"`
	LatestTask    mysqlstore.AgentTask   `json:"latest_task"`
	Tasks         []mysqlstore.AgentTask `json:"tasks"`
}

type webAgentConversationDetail struct {
	webAgentConversation
	Events   []mysqlstore.AgentTaskEvent `json:"events"`
	Messages []mysqlstore.Message        `json:"messages,omitempty"`
	Usage    webAgentConversationUsage   `json:"usage"`
}

type webAgentConversationUsage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	TotalTokens     int `json:"total_tokens"`
	ContextLength   int `json:"context_length"`
	ContextPercent  int `json:"context_percent"`
	ToolCalls       int `json:"tool_calls"`
	CompletedRuns   int `json:"completed_runs"`
	FailedRuns      int `json:"failed_runs"`
	CancelledRuns   int `json:"cancelled_runs"`
	TimeoutRuns     int `json:"timeout_runs"`
	RunningRuns     int `json:"running_runs"`
	TotalRuns       int `json:"total_runs"`
	TotalDurationMS int `json:"total_duration_ms"`
}

func tenantWebAgentConversationsHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_web_agent_conversations", "server.tenantWebAgentConversationsHandler", "handle web agent conversations")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limit := parseLimit(r.URL.Query().Get(paramLimit))
		sessions, err := opts.TenantService.ListSessions(r.Context(), limit)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		tasks, err := opts.TenantService.ListAgentTasks(r.Context(), limit)
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, map[string]any{"data": buildWebAgentConversations(sessions, tasks)})
	})
}

func tenantWebAgentConversationHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_web_agent_conversation", "server.tenantWebAgentConversationHandler", "handle web agent conversation detail")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		conversationID, ok := webAgentConversationIDFromPath(r.URL.Path)
		if !ok {
			writeTenantError(w, http.StatusBadRequest, "web agent conversation id is required")
			return
		}
		selected, err := readWebAgentConversation(r.Context(), opts.TenantService, conversationID, parseLimit(r.URL.Query().Get(paramLimit)))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		detail, err := buildWebAgentConversationDetail(r.Context(), opts.TenantService, selected, parseLimit(r.URL.Query().Get("event_limit")))
		if err != nil {
			writeTenantServiceError(w, err)
			return
		}
		writeJSON(w, detail)
	})
}

func readWebAgentConversation(ctx context.Context, svc TenantService, conversationID string, limit int) (webAgentConversation, error) {
	sessions, tasks, err := webAgentConversationSource(ctx, svc, conversationID, limit)
	if err != nil {
		return webAgentConversation{}, err
	}
	for _, conversation := range buildWebAgentConversations(sessions, tasks) {
		if conversation.ID == conversationID {
			return conversation, nil
		}
	}
	return webAgentConversation{}, mysqlstore.ErrNotFound
}

func webAgentConversationSource(ctx context.Context, svc TenantService, conversationID string, limit int) ([]mysqlstore.Session, []mysqlstore.AgentTask, error) {
	if strings.HasPrefix(conversationID, "session:") {
		sessionID, err := strconv.ParseUint(strings.TrimPrefix(conversationID, "session:"), 10, 64)
		if err != nil || sessionID == 0 {
			return nil, nil, mysqlstore.ErrNotFound
		}
		// Authorize the numeric session before reading tasks; unrelated recent
		// activity must not determine whether this conversation can be opened.
		session, err := svc.GetSession(ctx, sessionID)
		if err != nil {
			return nil, nil, err
		}
		var tasks []mysqlstore.AgentTask
		if reader, ok := svc.(interface {
			ListWebAgentConversationTasks(context.Context, uint64, int) ([]mysqlstore.AgentTask, error)
		}); ok {
			tasks, err = reader.ListWebAgentConversationTasks(ctx, sessionID, limit)
		} else {
			// Preserve compatibility with services without the scoped read capability.
			tasks, err = svc.ListAgentTasks(ctx, limit)
		}
		return []mysqlstore.Session{session}, tasks, err
	}
	sessions, err := svc.ListSessions(ctx, limit)
	if err != nil {
		return nil, nil, err
	}
	tasks, err := svc.ListAgentTasks(ctx, limit)
	return sessions, tasks, err
}

func buildWebAgentConversations(sessions []mysqlstore.Session, tasks []mysqlstore.AgentTask) []webAgentConversation {
	sessionByID := make(map[uint64]mysqlstore.Session, len(sessions))
	for _, session := range sessions {
		sessionByID[session.ID] = session
	}
	parentByTaskID := make(map[uint64]uint64, len(tasks))
	for _, task := range tasks {
		if parentID := webAgentMetadataUint(task.MetadataJSON, "continuation_of_task_id"); parentID != 0 {
			parentByTaskID[task.ID] = parentID
		}
	}
	rootCache := make(map[uint64]uint64, len(tasks))
	rootTaskID := func(taskID uint64) uint64 {
		if cached := rootCache[taskID]; cached != 0 {
			return cached
		}
		seen := map[uint64]bool{}
		current := taskID
		for parentByTaskID[current] != 0 && !seen[current] {
			seen[current] = true
			current = parentByTaskID[current]
		}
		for id := range seen {
			rootCache[id] = current
		}
		rootCache[taskID] = current
		return current
	}

	grouped := make(map[string][]mysqlstore.AgentTask)
	for _, task := range tasks {
		parentSessionID := task.ParentSessionID
		if parentSessionID == 0 {
			parentSessionID = webAgentMetadataUint(task.MetadataJSON, "web_agent_session_id")
		}
		key := fmt.Sprintf("legacy:%d", rootTaskID(task.ID))
		if parentSessionID != 0 {
			key = fmt.Sprintf("session:%d", parentSessionID)
		}
		grouped[key] = append(grouped[key], task)
	}

	conversations := make([]webAgentConversation, 0, len(grouped))
	for key, items := range grouped {
		sortAgentTasksByStart(items)
		latest := latestAgentTask(items)
		parentSessionID := latest.ParentSessionID
		if parentSessionID == 0 {
			parentSessionID = webAgentMetadataUint(latest.MetadataJSON, "web_agent_session_id")
		}
		var sessionPtr *mysqlstore.Session
		if session, ok := sessionByID[parentSessionID]; ok {
			copied := session
			sessionPtr = &copied
		}
		conversations = append(conversations, webAgentConversation{
			ID:            webAgentConversationID(key, parentSessionID),
			SessionID:     parentSessionID,
			SessionKey:    webAgentSessionKey(sessionPtr, latest),
			Source:        sessionEventSource(latest),
			Title:         webAgentConversationTitle(sessionPtr, latest),
			CWD:           webAgentConversationCWD(sessionPtr, latest),
			WorkspaceName: webAgentWorkspaceName(sessionPtr, latest),
			Status:        webAgentConversationStatus(sessionPtr, latest),
			UpdatedAt:     webAgentConversationUpdatedAt(sessionPtr, latest),
			Session:       sessionPtr,
			LatestTask:    latest,
			Tasks:         items,
		})
	}
	sort.SliceStable(conversations, func(i, j int) bool {
		if !conversations[i].UpdatedAt.Equal(conversations[j].UpdatedAt) {
			return conversations[i].UpdatedAt.After(conversations[j].UpdatedAt)
		}
		return conversations[i].LatestTask.ID > conversations[j].LatestTask.ID
	})
	return conversations
}

func buildWebAgentConversationDetail(ctx context.Context, svc TenantService, conversation webAgentConversation, eventLimit int) (webAgentConversationDetail, error) {
	if eventLimit == 0 {
		eventLimit = 500
	}
	detail := webAgentConversationDetail{
		webAgentConversation: conversation,
		Events:               []mysqlstore.AgentTaskEvent{},
		Usage:                summarizeWebAgentConversationUsage(conversation.Tasks),
	}
	taskIDs := make([]uint64, 0, len(conversation.Tasks))
	for _, task := range conversation.Tasks {
		taskIDs = append(taskIDs, task.ID)
	}
	// 一次 IN 查询覆盖整段会话的任务，取代每个任务一条查询的 N+1(AUDIT-P1-26)。
	events, err := svc.ListAgentTaskEventsForTasks(ctx, taskIDs, eventLimit)
	if err != nil {
		return detail, err
	}
	detail.Events = append(detail.Events, events...)
	byTask := make(map[uint64][]mysqlstore.AgentTaskEvent, len(taskIDs))
	for _, event := range events {
		byTask[event.TaskID] = append(byTask[event.TaskID], event)
	}
	for _, task := range conversation.Tasks {
		if strings.TrimSpace(task.ResultJSON) == "" {
			detail.Usage = mergeWebAgentEventUsage(detail.Usage, byTask[task.ID])
		}
	}
	sort.SliceStable(detail.Events, func(i, j int) bool {
		if !detail.Events[i].CreatedAt.Equal(detail.Events[j].CreatedAt) {
			return detail.Events[i].CreatedAt.Before(detail.Events[j].CreatedAt)
		}
		return detail.Events[i].ID < detail.Events[j].ID
	})
	if conversation.SessionID != 0 {
		if messages, err := svc.ListMessages(ctx, conversation.SessionID, 200); err == nil {
			detail.Messages = messages
		} else if !errors.Is(err, mysqlstore.ErrNotFound) {
			return detail, err
		}
	}
	return detail, nil
}

func summarizeWebAgentConversationUsage(tasks []mysqlstore.AgentTask) webAgentConversationUsage {
	usage := webAgentConversationUsage{TotalRuns: len(tasks)}
	for _, task := range tasks {
		switch strings.ToLower(strings.TrimSpace(task.Status)) {
		case agenttasks.StatusCompleted:
			usage.CompletedRuns++
		case agenttasks.StatusFailed:
			usage.FailedRuns++
		case agenttasks.StatusCancelled:
			usage.CancelledRuns++
		case agenttasks.StatusTimeout:
			usage.TimeoutRuns++
		case agenttasks.StatusRunning:
			usage.RunningRuns++
		}
		usage = mergeWebAgentPayloadUsage(usage, parseAgentTaskJSONMap(task.ResultJSON))
	}
	return usage
}

func mergeWebAgentEventUsage(usage webAgentConversationUsage, events []mysqlstore.AgentTaskEvent) webAgentConversationUsage {
	for _, event := range events {
		switch event.EventType {
		case agenttasks.EventCompleted, agenttasks.EventFailed, agenttasks.EventTimeout, agenttasks.EventUsage:
			usage = mergeWebAgentPayloadUsage(usage, parseAgentTaskJSONMap(event.PayloadJSON))
		}
	}
	return usage
}

func mergeWebAgentPayloadUsage(usage webAgentConversationUsage, payload map[string]any) webAgentConversationUsage {
	inputTokens := webAgentPayloadInt(payload, "input_tokens")
	outputTokens := webAgentPayloadInt(payload, "output_tokens")
	totalTokens := webAgentPayloadInt(payload, "total_tokens")
	if totalTokens == 0 {
		totalTokens = inputTokens + outputTokens
	}
	usage.InputTokens += inputTokens
	usage.OutputTokens += outputTokens
	usage.TotalTokens += totalTokens
	usage.ToolCalls += webAgentPayloadInt(payload, "tool_calls")
	usage.TotalDurationMS += webAgentPayloadInt(payload, "duration_ms")
	if contextLength := webAgentPayloadInt(payload, "context_length"); contextLength > usage.ContextLength {
		usage.ContextLength = contextLength
	}
	if contextPercent := webAgentPayloadInt(payload, "context_percent"); contextPercent > usage.ContextPercent {
		usage.ContextPercent = contextPercent
	}
	return usage
}

func webAgentPayloadInt(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint64:
		return int(value)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}

func webAgentConversationIDFromPath(path string) (string, bool) {
	raw := strings.TrimPrefix(path, "/tenant/web-agent/conversations/")
	if raw == "" || raw == path || strings.Contains(raw, "/") {
		return "", false
	}
	value, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func sortAgentTasksByStart(tasks []mysqlstore.AgentTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].StartedAt.Equal(tasks[j].StartedAt) {
			return tasks[i].StartedAt.Before(tasks[j].StartedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
}

func latestAgentTask(tasks []mysqlstore.AgentTask) mysqlstore.AgentTask {
	latest := tasks[0]
	for _, task := range tasks[1:] {
		if task.StartedAt.After(latest.StartedAt) || (task.StartedAt.Equal(latest.StartedAt) && task.ID > latest.ID) {
			latest = task
		}
	}
	return latest
}

func webAgentConversationID(fallback string, sessionID uint64) string {
	if sessionID != 0 {
		return fmt.Sprintf("session:%d", sessionID)
	}
	return fallback
}

func webAgentSessionKey(session *mysqlstore.Session, task mysqlstore.AgentTask) string {
	if session != nil && strings.TrimSpace(session.SessionKey) != "" {
		return session.SessionKey
	}
	return webAgentMetadataString(task.MetadataJSON, "web_agent_session_key")
}

func webAgentConversationTitle(session *mysqlstore.Session, task mysqlstore.AgentTask) string {
	if session != nil && strings.TrimSpace(session.Title) != "" {
		return session.Title
	}
	if strings.TrimSpace(task.Description) != "" {
		return task.Description
	}
	if strings.TrimSpace(task.AgentName) != "" {
		return task.AgentName
	}
	return fmt.Sprintf("Session %d", task.ID)
}

func webAgentConversationCWD(session *mysqlstore.Session, task mysqlstore.AgentTask) string {
	if session != nil && strings.TrimSpace(session.CWD) != "" {
		return session.CWD
	}
	return webAgentMetadataString(task.MetadataJSON, "cwd")
}

func webAgentWorkspaceName(session *mysqlstore.Session, task mysqlstore.AgentTask) string {
	if value := webAgentMetadataString(task.MetadataJSON, "workspace_name"); value != "" {
		return value
	}
	cwd := webAgentConversationCWD(session, task)
	if cwd == "" {
		return ""
	}
	return filepath.Base(cwd)
}

func webAgentConversationStatus(session *mysqlstore.Session, task mysqlstore.AgentTask) string {
	if strings.TrimSpace(task.Status) != "" {
		return task.Status
	}
	if session != nil {
		return session.Status
	}
	return "unknown"
}

func webAgentConversationUpdatedAt(session *mysqlstore.Session, task mysqlstore.AgentTask) time.Time {
	if !task.FinishedAt.IsZero() {
		return task.FinishedAt
	}
	if !task.StartedAt.IsZero() {
		return task.StartedAt
	}
	if session != nil {
		if !session.LastMessageAt.IsZero() {
			return session.LastMessageAt
		}
		return session.StartedAt
	}
	return time.Time{}
}

func webAgentMetadataString(raw, key string) string {
	metadata := parseAgentTaskJSONMap(raw)
	return agentTaskStringValue(metadata[key])
}

func webAgentMetadataUint(raw, key string) uint64 {
	metadata := parseAgentTaskJSONMap(raw)
	switch value := metadata[key].(type) {
	case float64:
		if value > 0 {
			return uint64(value)
		}
	case int:
		if value > 0 {
			return uint64(value)
		}
	case uint64:
		return value
	case string:
		id, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		return id
	}
	return 0
}

func tenantSessionIDFromPath(path string) (uint64, bool) {
	raw := strings.TrimPrefix(path, "/tenant/sessions/")
	if raw == "" || raw == path || strings.Contains(raw, "/") {
		return 0, false
	}
	id, err := strconv.ParseUint(raw, 10, 64)
	return id, err == nil
}

func tenantMessagesHandler(opts Options) http.HandlerFunc {
	return tenantEndpoint(opts, nil, func(w http.ResponseWriter, r *http.Request) {
		logService(r.Context(), "tenant_messages", "server.tenantMessagesHandler", "handle tenant messages")
		switch r.Method {
		case http.MethodGet:
			sessionID, ok := parseOptionalUint(r.URL.Query().Get("session_id"))
			if !ok || sessionID == 0 {
				writeTenantError(w, http.StatusBadRequest, "session_id is required")
				return
			}
			items, err := opts.TenantService.ListMessages(r.Context(), uint64(sessionID), parseLimit(r.URL.Query().Get(paramLimit)))
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			writeJSON(w, map[string]any{"data": items})
		case http.MethodPost:
			var req tenantservice.MessageRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeTenantError(w, http.StatusBadRequest, err.Error())
				return
			}
			if req.SessionID == 0 || strings.TrimSpace(req.Role) == "" {
				writeTenantError(w, http.StatusBadRequest, "session_id and role are required")
				return
			}
			id, err := opts.TenantService.UpsertMessage(r.Context(), req)
			if err != nil {
				writeTenantServiceError(w, err)
				return
			}
			recordTenantAudit(r.Context(), opts.TenantService, "tenant.message.upsert", "message", id)
			writeJSON(w, map[string]any{"id": id})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
