package mysql

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

// ListWebAgentConversationTasks preserves the legacy detail's task membership,
// including subagents and older metadata-only session links, within one owner.
func (r *GormRepository) ListWebAgentConversationTasks(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]AgentTask, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 {
		return nil, ErrInvalidInput
	}
	r.log(ctx, "web_agent_conversation.tasks", "mysql.GormRepository.ListWebAgentConversationTasks", "list session conversation tasks", "session_id", sessionID)
	legacySessionPredicate := "JSON_UNQUOTE(JSON_EXTRACT(metadata_json, '$.web_agent_session_id')) = ?"
	if isSQLite(r.db) {
		// SQLite stores legacy metadata from different clients as either a JSON
		// number or a JSON string. Comparing the normalized text keeps both
		// formats compatible with the MySQL behavior.
		legacySessionPredicate = "CAST(json_extract(metadata_json, '$.web_agent_session_id') AS TEXT) = ?"
	}
	rows, err := r.with(ctx).Table("tenant_agent_tasks").
		Select("id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at").
		Where("tenant_id = ? AND user_id = ?", tenantID, userID).
		Where("parent_session_id = ? OR ((parent_session_id IS NULL OR parent_session_id = 0) AND "+legacySessionPredicate+")", sessionID, strconv.FormatUint(sessionID, 10)).
		Order("started_at DESC, id DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTasks(rows)
}

func (r *GormRepository) ListSessionConversationTasks(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]AgentTask, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 {
		return nil, ErrInvalidInput
	}
	rows, err := r.with(ctx).Table("tenant_agent_tasks").
		Select("id, tenant_id, user_id, COALESCE(parent_session_id, 0), COALESCE(subagent_session_key, ''), COALESCE(agent_name, ''), COALESCE(description, ''), status, COALESCE(model, ''), COALESCE(result_json, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), started_at, finished_at").
		Where("tenant_id = ? AND user_id = ? AND parent_session_id = ? AND agent_name = ?", tenantID, userID, sessionID, agenttasks.AgentNameWeb).
		Order("started_at DESC, id DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAgentTasks(rows)
}

// ListSessionConversationEvents reads the durable main-run event log directly
// by session, so unrelated tasks cannot evict conversation history.
func (r *GormRepository) ListSessionConversationEvents(ctx context.Context, tenantID, userID, sessionID, afterID uint64, limit int) ([]AgentTaskEvent, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 {
		return nil, ErrInvalidInput
	}
	rows, err := r.with(ctx).Table("tenant_agent_task_events AS e").
		Select("e.id, e.task_id, e.event_type, COALESCE(e.payload_json, ''), COALESCE(e.trace_id, ''), e.created_at").
		Joins("JOIN tenant_agent_tasks AS t ON t.id = e.task_id AND t.tenant_id = e.tenant_id AND t.user_id = e.user_id").
		Joins("JOIN tenant_sessions AS s ON s.id = t.parent_session_id AND s.tenant_id = t.tenant_id AND s.user_id = t.user_id").
		Where("e.tenant_id = ? AND e.user_id = ? AND s.id = ? AND s.archived_at IS NULL AND t.agent_name = ? AND e.id > ?", tenantID, userID, sessionID, agenttasks.AgentNameWeb, afterID).
		Order("e.id ASC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events, err := scanAgentTaskEvents(rows)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 || !isSQLite(r.db) {
		return events, nil
	}

	// Channel workers persist the durable user/assistant projection in
	// tenant_session_messages, while they do not create Web Agent task events.
	// Project those messages only when the task event stream is empty so the
	// existing WebUI event contract can render Feishu conversations too.
	messageLimit := (normalizeLimit(limit) + 1) / 2
	messageRows, err := r.with(ctx).Table("tenant_session_messages").
		Select(messageSelectColumns).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND (id * 2) > ?", tenantID, userID, sessionID, afterID).
		Order("turn_index ASC, id ASC").
		Limit(messageLimit).
		Rows()
	if err != nil {
		return nil, err
	}
	defer messageRows.Close()
	messages, err := scanMessages(messageRows)
	if err != nil {
		return nil, err
	}
	return projectSessionMessages(messages), nil
}

func projectSessionMessages(messages []Message) []AgentTaskEvent {
	events := make([]AgentTaskEvent, 0, len(messages)*2)
	for _, message := range messages {
		if message.ID == 0 {
			continue
		}
		payload, err := json.Marshal(map[string]any{
			"content": message.Content,
			"role":    message.Role,
		})
		if err != nil {
			continue
		}
		taskID := message.ID
		firstID := message.ID*2 - 1
		events = append(events, AgentTaskEvent{
			ID: firstID, TaskID: taskID, EventType: agenttasks.EventMessage,
			PayloadJSON: string(payload), TraceID: message.TraceID, CreatedAt: message.CreatedAt,
		})
		if message.Role == "assistant" {
			events[len(events)-1].EventType = agenttasks.EventTextDelta
		}
		terminalType := agenttasks.EventCompleted
		terminalPayload := `{"response":""}`
		if message.IsError {
			terminalType = agenttasks.EventFailed
			terminalPayload = `{"error":"channel message failed"}`
		}
		events = append(events, AgentTaskEvent{
			ID: firstID + 1, TaskID: taskID, EventType: terminalType,
			PayloadJSON: terminalPayload, TraceID: message.TraceID, CreatedAt: message.CreatedAt,
		})
	}
	return events
}
