package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sessionControlStatusIdle     = "idle"
	sessionControlSessionColumns = "id, session_key, title, status, model, cwd, COALESCE(metadata_json, ''), started_at, last_message_at"
)

func (r *GormRepository) GetSessionControlSessionByKey(ctx context.Context, tenantID, userID uint64, sessionKey string) (SessionControlSession, error) {
	r.log(ctx, "session_control.session.get", "mysql.GormRepository.GetSessionControlSessionByKey", "get session control session")
	if tenantID == 0 || userID == 0 || strings.TrimSpace(sessionKey) == "" {
		return SessionControlSession{}, ErrInvalidInput
	}
	return getSessionControlSessionTx(r.with(ctx), tenantID, userID, sessionKey, false)
}

// ListSessionControlSessions carries private configuration in the same scoped
// list query. Public Session JSON continues to omit the metadata document.
func (r *GormRepository) ListSessionControlSessions(ctx context.Context, tenantID, userID uint64, limit int) ([]SessionControlSession, error) {
	r.log(ctx, "session_control.session.list", "mysql.GormRepository.ListSessionControlSessions", "list session control sessions")
	if tenantID == 0 || userID == 0 {
		return nil, ErrInvalidInput
	}
	rows, err := r.with(ctx).Table("tenant_sessions").
		Select(sessionControlSessionColumns).
		Where("tenant_id = ? AND user_id = ? AND archived_at IS NULL", tenantID, userID).
		Order("last_message_at DESC, started_at DESC").Limit(normalizeLimit(limit)).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SessionControlSession, 0)
	for rows.Next() {
		item, err := scanSessionControlSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *GormRepository) ListSessionChannels(ctx context.Context, tenantID, userID uint64, sessionIDs []uint64) (map[uint64]SessionChannel, error) {
	result := make(map[uint64]SessionChannel)
	if tenantID == 0 || userID == 0 || len(sessionIDs) == 0 {
		return result, nil
	}
	rows, err := r.with(ctx).Table("channel_conversations AS conversations").
		Select("conversations.session_id, accounts.provider, accounts.account_key").
		Joins("JOIN channel_accounts AS accounts ON accounts.tenant_id = conversations.tenant_id AND accounts.id = conversations.account_id AND accounts.archived_at IS NULL").
		Joins("JOIN tenant_sessions AS sessions ON sessions.id = conversations.session_id").
		Where("sessions.tenant_id = ? AND sessions.user_id = ? AND sessions.archived_at IS NULL AND conversations.tenant_id = ? AND conversations.session_id IN ? AND conversations.archived_at IS NULL", tenantID, userID, tenantID, sessionIDs).
		Order("conversations.updated_at DESC, conversations.id DESC").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID uint64
		var provider, accountKey sql.NullString
		if err := rows.Scan(&sessionID, &provider, &accountKey); err != nil {
			return nil, err
		}
		if _, exists := result[sessionID]; exists {
			continue
		}
		result[sessionID] = SessionChannel{Provider: provider.String, AccountKey: accountKey.String}
	}
	return result, rows.Err()
}

// CreateSessionControlRun serializes on the parent Session so only one ready
// or running Run can exist, and commits the task plus its initial message event
// together. The task idempotency key remains the replay anchor.
func (r *GormRepository) CreateSessionControlRun(ctx context.Context, input SessionControlRunInput) (SessionControlRunResult, error) {
	task := input.Task
	message := input.Message
	if task.TenantID == 0 || task.UserID == 0 || task.ParentSessionID == 0 || task.Status != agenttasks.StatusReady || strings.TrimSpace(task.IdempotencyKey) == "" || !validJSONObject(task.MetadataJSON) || strings.TrimSpace(message.Content) == "" && len(message.Attachments) == 0 {
		return SessionControlRunResult{}, ErrInvalidInput
	}
	var result SessionControlRunResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		if input.RuntimeConfigChanged {
			if err := lockEmptySessionControlQueue(tx, task); err != nil {
				return err
			}
		}
		var sessionID uint64
		if err := tx.Table("tenant_sessions").Select("id").Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", task.TenantID, task.UserID, task.ParentSessionID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sessionID).Error; err != nil {
			return err
		}
		active, err := sessionControlTaskByQuery(tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND parent_session_id = ? AND agent_name = ? AND status IN ?", task.TenantID, task.UserID, task.ParentSessionID, agenttasks.AgentNameWeb, []string{agenttasks.StatusReady, agenttasks.StatusRunning}).Clauses(clause.Locking{Strength: "UPDATE"}))
		if err == nil {
			if active.IdempotencyKey == task.IdempotencyKey {
				if !sessionControlJSONEqual(active.MetadataJSON, task.MetadataJSON) {
					return ErrIdempotencyConflict
				}
				return recoverSessionControlMessageEvent(tx, active, message, input.SkipMessageEvent, &result)
			}
			if input.AdoptTaskID == active.ID && active.Status == agenttasks.StatusReady && active.IdempotencyKey == "" && sessionControlSideChatMetadata(active.MetadataJSON) && sessionControlSideChatMetadata(task.MetadataJSON) {
				if err := validateSessionControlExpectedConfig(tx, input); err != nil {
					return err
				}
				// Claim only the unstarted legacy side-chat task. The candidate
				// event stays first; the operation message is appended atomically.
				updated := tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND id = ? AND status = ? AND (idempotency_key IS NULL OR idempotency_key = '')", task.TenantID, task.UserID, active.ID, agenttasks.StatusReady).
					Updates(map[string]any{"idempotency_key": task.IdempotencyKey, "metadata_json": task.MetadataJSON, "model": nullableStringPtr(task.Model)})
				if updated.Error != nil {
					return updated.Error
				}
				if updated.RowsAffected != 1 {
					return ErrInvalidState
				}
				active.IdempotencyKey, active.MetadataJSON, active.Model = task.IdempotencyKey, task.MetadataJSON, task.Model
				return appendSessionControlRunMessage(tx, active, message, input.SkipMessageEvent, &result)
			}
			result = SessionControlRunResult{Task: active, Busy: true}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		existing, err := sessionControlTaskByQuery(tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND idempotency_key = ?", task.TenantID, task.UserID, task.IdempotencyKey).Clauses(clause.Locking{Strength: "UPDATE"}))
		if err == nil {
			if existing.ParentSessionID != task.ParentSessionID || !sessionControlJSONEqual(existing.MetadataJSON, task.MetadataJSON) {
				return ErrIdempotencyConflict
			}
			return recoverSessionControlMessageEvent(tx, existing, message, input.SkipMessageEvent, &result)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if input.AdoptTaskID != 0 {
			return ErrInvalidState
		}
		if err := validateSessionControlExpectedConfig(tx, input); err != nil {
			return err
		}
		row := newGormAgentTask(task)
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return appendSessionControlRunMessage(tx, agentTaskFromGORM(row), message, input.SkipMessageEvent, &result)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = ErrNotFound
	}
	return result, err
}

func validateSessionControlExpectedConfig(tx *gorm.DB, input SessionControlRunInput) error {
	if input.ExpectedRuntimeConfigJSON == "" {
		return nil
	}
	task := input.Task
	var session gormSession
	if err := tx.Table("tenant_sessions").Select("model, metadata_json").Where("tenant_id = ? AND user_id = ? AND id = ?", task.TenantID, task.UserID, task.ParentSessionID).Take(&session).Error; err != nil {
		return err
	}
	if !sessionControlConfigMatches(valueStringPtr(session.Model), valueStringPtr(session.MetadataJSON), input.ExpectedRuntimeConfigJSON) {
		return ErrInvalidState
	}
	return nil
}

func appendSessionControlRunMessage(tx *gorm.DB, task AgentTask, message agenttasks.MessageInput, skipMessageEvent bool, result *SessionControlRunResult) error {
	if skipMessageEvent {
		*result = SessionControlRunResult{Task: task, Created: true}
		return nil
	}
	message.TaskID = task.ID
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	row := gormAgentTaskEvent{TenantID: task.TenantID, UserID: task.UserID, TaskID: task.ID, EventType: agenttasks.EventMessage, PayloadJSON: nullableStringPtr(string(payload)), TraceID: nullableStringPtr(message.TraceID)}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	if isSQLite(tx) {
		if err := tx.Exec(`UPDATE tenant_agent_task_events
			SET created_at = ?
			WHERE tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ? AND created_at IS NULL`,
			time.Now().UTC(), row.TenantID, row.UserID, row.TaskID, row.EventType).Error; err != nil {
			return err
		}
	}
	*result = SessionControlRunResult{Task: task, EventID: row.ID, Created: true}
	return nil
}

func sessionControlSideChatMetadata(metadataJSON string) bool {
	var metadata struct {
		Source string `json:"source"`
	}
	return json.Unmarshal([]byte(metadataJSON), &metadata) == nil && metadata.Source == agenttasks.SourcePendingInputSideChat
}

func sessionControlTaskByQuery(query *gorm.DB) (AgentTask, error) {
	var row gormAgentTask
	err := query.Select("id, tenant_id, user_id, parent_session_id, subagent_session_key, agent_name, description, status, model, result_json, metadata_json, trace_id, idempotency_key, started_at, finished_at").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTask{}, ErrNotFound
	}
	return agentTaskFromGORM(row), err
}

func recoverSessionControlMessageEvent(tx *gorm.DB, task AgentTask, message agenttasks.MessageInput, skipMessageEvent bool, result *SessionControlRunResult) error {
	if skipMessageEvent {
		*result = SessionControlRunResult{Task: task}
		return nil
	}
	var row gormAgentTaskEvent
	order := "id ASC"
	if sessionControlSideChatMetadata(task.MetadataJSON) {
		order = "id DESC"
	}
	err := tx.Table("tenant_agent_task_events").Where("tenant_id = ? AND user_id = ? AND task_id = ? AND event_type = ?", task.TenantID, task.UserID, task.ID, agenttasks.EventMessage).Order(order).Limit(1).Take(&row).Error
	if err != nil {
		return err
	}
	message.TaskID = task.ID
	payload, encodeErr := json.Marshal(message)
	if encodeErr != nil || !sessionControlJSONEqual(valueStringPtr(row.PayloadJSON), string(payload)) {
		return ErrInvalidState
	}
	*result = SessionControlRunResult{Task: task, EventID: row.ID}
	return nil
}

func sessionControlJSONEqual(left, right string) bool {
	if !json.Valid([]byte(left)) || !json.Valid([]byte(right)) {
		return false
	}
	decode := func(input string) (any, error) {
		decoder := json.NewDecoder(strings.NewReader(input))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}

// StartSessionControlRun is the single ready-to-running claim. A concurrent
// launcher observes Started=false and must not launch a second goroutine.
func (r *GormRepository) StartSessionControlRun(ctx context.Context, tenantID, userID, taskID uint64) (SessionControlRunStartResult, error) {
	if tenantID == 0 || userID == 0 || taskID == 0 {
		return SessionControlRunStartResult{}, ErrInvalidInput
	}
	var result SessionControlRunStartResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		// Follow the creation lock order so config persistence cannot deadlock
		// with a concurrent next-Run request holding the Session row.
		var sessionID uint64
		parent := tx.Table("tenant_agent_tasks").Select("parent_session_id").Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID)
		if err := tx.Table("tenant_sessions").Select("id").Where("tenant_id = ? AND user_id = ? AND id = (?) AND archived_at IS NULL", tenantID, userID, parent).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sessionID).Error; err != nil {
			return err
		}
		task, err := sessionControlTaskByQuery(tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, taskID).Clauses(clause.Locking{Strength: "UPDATE"}))
		if err != nil {
			return err
		}
		result.Task = task
		if task.Status != agenttasks.StatusReady {
			return nil
		}
		updated := tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND id = ? AND status = ?", tenantID, userID, taskID, agenttasks.StatusReady).Update("status", agenttasks.StatusRunning)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrInvalidState
		}
		if err := persistSessionControlRuntimeConfig(tx, task); err != nil {
			return err
		}
		result.Task.Status = agenttasks.StatusRunning
		result.Started = true
		return nil
	})
	return result, err
}

// FailSessionControlRun terminalizes only an unclaimed prepared Run. The CAS
// and event insert share a transaction, so a concurrent launcher either owns
// the running task or observes the complete failed record.
func (r *GormRepository) FailSessionControlRun(ctx context.Context, input SessionControlRunFailureInput) (SessionControlRunFailureResult, error) {
	r.log(ctx, "session_control.run.fail", "mysql.GormRepository.FailSessionControlRun", "fail unlaunched managed session run")
	if input.TenantID == 0 || input.UserID == 0 || input.TaskID == 0 || !validJSONObject(input.ResultJSON) || !validJSONObject(input.EventPayloadJSON) {
		return SessionControlRunFailureResult{}, ErrInvalidInput
	}
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	var result SessionControlRunFailureResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		updated := tx.Table("tenant_agent_tasks").Where("tenant_id = ? AND user_id = ? AND id = ? AND status = ?", input.TenantID, input.UserID, input.TaskID, agenttasks.StatusReady).Updates(map[string]any{
			"status": agenttasks.StatusFailed, "result_json": nullableStringPtr(input.ResultJSON), "finished_at": time.Now().UTC(),
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return nil
		}
		row := gormAgentTaskEvent{TenantID: input.TenantID, UserID: input.UserID, TaskID: input.TaskID, EventType: agenttasks.EventFailed, PayloadJSON: nullableStringPtr(input.EventPayloadJSON), TraceID: nullableStringPtr(input.TraceID)}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result.Failed, result.EventID = true, row.ID
		return nil
	})
	return result, err
}

var sessionControlConfigKeys = []string{"model", "provider", "permission_mode", "effort", "prompt_mode"}

func sessionControlConfigMatches(model, metadataJSON, expectedJSON string) bool {
	var stored, expected map[string]any
	if metadataJSON != "" && json.Unmarshal([]byte(metadataJSON), &stored) != nil {
		return false
	}
	if json.Unmarshal([]byte(expectedJSON), &expected) != nil {
		return false
	}
	if stored == nil {
		stored = make(map[string]any)
	}
	if model != "" {
		stored["model"] = model
	}
	for _, key := range sessionControlConfigKeys {
		left, _ := stored[key].(string)
		right, _ := expected[key].(string)
		if left != right {
			return false
		}
	}
	return true
}

func lockEmptySessionControlQueue(tx *gorm.DB, task agenttasks.TaskInput) error {
	sessionID := strconv.FormatUint(task.ParentSessionID, 10)
	seed := gormPendingInputSetting{TenantID: task.TenantID, UserID: task.UserID, SessionID: sessionID, Enabled: true, UpdatedAt: time.Now().UTC()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return err
	}
	var setting gormPendingInputSetting
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", task.TenantID, task.UserID, sessionID).First(&setting).Error; err != nil {
		return err
	}
	var count int64
	if err := tx.Model(&gormPendingInput{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status NOT IN (?, ?)", task.TenantID, task.UserID, sessionID, pendinginput.StatusSent, pendinginput.StatusCancelled).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrInvalidState
	}
	return nil
}

func persistSessionControlRuntimeConfig(tx *gorm.DB, task AgentTask) error {
	var metadata map[string]any
	if err := json.Unmarshal([]byte(task.MetadataJSON), &metadata); err != nil {
		return err
	}
	// Preserve Session creation replay metadata and unrelated settings. Only
	// the accepted Run's non-secret configuration becomes the next default.
	args := make([]any, 0, len(sessionControlConfigKeys)*2)
	placeholders := make([]string, 0, len(sessionControlConfigKeys))
	for _, key := range sessionControlConfigKeys {
		if value, ok := metadata[key].(string); ok && value != "" {
			args = append(args, "$."+key, value)
			placeholders = append(placeholders, "?, ?")
		}
	}
	if len(args) == 0 {
		return nil
	}
	if isSQLite(tx) {
		var existing struct {
			MetadataJSON *string `gorm:"column:metadata_json"`
		}
		if err := tx.Table("tenant_sessions").
			Select("metadata_json").
			Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", task.TenantID, task.UserID, task.ParentSessionID).
			Take(&existing).Error; err != nil {
			return err
		}
		metadata := map[string]any{}
		if existing.MetadataJSON != nil && strings.TrimSpace(*existing.MetadataJSON) != "" {
			if err := json.Unmarshal([]byte(*existing.MetadataJSON), &metadata); err != nil {
				return err
			}
		}
		var taskMetadata map[string]any
		if err := json.Unmarshal([]byte(task.MetadataJSON), &taskMetadata); err != nil {
			return err
		}
		for _, key := range sessionControlConfigKeys {
			if value, ok := taskMetadata[key].(string); ok && value != "" {
				metadata[key] = value
			}
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		updates := map[string]any{"metadata_json": string(encoded)}
		if task.Model != "" {
			updates["model"] = task.Model
		}
		return tx.Table("tenant_sessions").
			Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", task.TenantID, task.UserID, task.ParentSessionID).
			Updates(updates).Error
	}
	updates := map[string]any{"metadata_json": gorm.Expr("JSON_SET(COALESCE(metadata_json, JSON_OBJECT()), "+strings.Join(placeholders, ", ")+")", args...)}
	if task.Model != "" {
		updates["model"] = task.Model
	}
	result := tx.Table("tenant_sessions").Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", task.TenantID, task.UserID, task.ParentSessionID).Updates(updates)
	return result.Error
}

// CreateSessionControlSession inserts without an upsert update. A concurrent
// winner is read back, allowing the caller to compare immutable operation
// metadata without overwriting the existing Session.
func (r *GormRepository) CreateSessionControlSession(ctx context.Context, input SessionControlCreateInput) (SessionControlCreateResult, error) {
	r.log(ctx, "session_control.session.create", "mysql.GormRepository.CreateSessionControlSession", "create session control session")
	item := input.Session
	if item.TenantID == 0 || item.UserID == 0 || strings.TrimSpace(item.SessionKey) == "" || !validJSONObject(item.MetadataJSON) {
		return SessionControlCreateResult{}, ErrInvalidInput
	}
	if item.Status == "" {
		item.Status = sessionControlStatusIdle
	}
	var result SessionControlCreateResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		existing, err := getSessionControlSessionTx(tx, item.TenantID, item.UserID, item.SessionKey, true)
		if err == nil {
			result.Session = existing
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		now := time.Now().UTC()
		row := gormSession{
			TenantID: item.TenantID, UserID: item.UserID, SessionKey: item.SessionKey,
			Title: nullableStringPtr(item.Title), Status: item.Status, Model: nullableStringPtr(item.Model),
			CWD: nullableStringPtr(item.CWD), MetadataJSON: nullableStringPtr(item.MetadataJSON), LastMessageAt: &now,
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if isSQLite(tx) {
			if err := tx.Table("tenant_sessions").Where("id = ?", row.ID).
				Update("started_at", time.Now().UTC()).Error; err != nil {
				return err
			}
		}
		stored, err := getSessionControlSessionTx(tx, item.TenantID, item.UserID, item.SessionKey, true)
		if err != nil {
			return err
		}
		result.Session = stored
		result.Created = row.ID != 0 && stored.ID == row.ID
		return nil
	})
	return result, err
}

func getSessionControlSessionTx(db *gorm.DB, tenantID, userID uint64, sessionKey string, lock bool) (SessionControlSession, error) {
	query := db.Table("tenant_sessions").
		Select(sessionControlSessionColumns).
		Where("tenant_id = ? AND user_id = ? AND session_key = ? AND archived_at IS NULL", tenantID, userID, sessionKey).
		Limit(1)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	item, err := scanSessionControlSession(query.Row())
	if errors.Is(err, sql.ErrNoRows) {
		return SessionControlSession{}, ErrNotFound
	}
	return item, err
}

func scanSessionControlSession(scanner skillScanner) (SessionControlSession, error) {
	var item SessionControlSession
	var title, model, cwd, metadata sql.NullString
	var lastMessageAt sql.NullTime
	if err := scanner.Scan(&item.ID, &item.SessionKey, &title, &item.Status, &model, &cwd, &metadata, &item.StartedAt, &lastMessageAt); err != nil {
		return SessionControlSession{}, err
	}
	item.Title, item.Model, item.CWD, item.MetadataJSON = title.String, model.String, cwd.String, metadata.String
	if lastMessageAt.Valid {
		item.LastMessageAt = lastMessageAt.Time
	}
	return item, nil
}

// CancelAgentTaskForSessionControl commits the status transition and its
// operation-specific replay event together.
func (r *GormRepository) CancelAgentTaskForSessionControl(ctx context.Context, input SessionControlStopInput) (SessionControlStopResult, error) {
	r.log(ctx, "session_control.stop", "mysql.GormRepository.CancelAgentTaskForSessionControl", "cancel task for session control")
	if input.TenantID == 0 || input.UserID == 0 || input.TaskID == 0 || !validJSONObject(input.ResultJSON) || !validJSONObject(input.EventPayloadJSON) {
		return SessionControlStopResult{}, ErrInvalidInput
	}
	if input.TraceID == "" {
		input.TraceID = observability.TraceID(ctx)
	}
	var result SessionControlStopResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID     uint64 `gorm:"column:id"`
			Status string `gorm:"column:status"`
		}
		if err := tx.Table("tenant_agent_tasks").Select("id, status").Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.TaskID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := r.checkWorkflowTaskOwnership(tx, input.TaskID, input.TenantID, input.UserID); err != nil {
			return err
		}
		if task.Status != agenttasks.StatusRunning && task.Status != agenttasks.StatusReady {
			return nil
		}
		updated := r.workflowTaskWriteQuery(tx.Table("tenant_agent_tasks")).Where("tenant_id = ? AND user_id = ? AND id = ? AND status IN ?", input.TenantID, input.UserID, input.TaskID, []string{agenttasks.StatusReady, agenttasks.StatusRunning}).Updates(map[string]any{
			"status": agenttasks.StatusCancelled, "result_json": nullableStringPtr(input.ResultJSON), "finished_at": time.Now().UTC(),
		})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			return r.checkWorkflowTaskOwnership(tx, input.TaskID, input.TenantID, input.UserID)
		}
		row := gormAgentTaskEvent{TenantID: input.TenantID, UserID: input.UserID, TaskID: input.TaskID, EventType: agenttasks.EventSessionControlStop, PayloadJSON: nullableStringPtr(input.EventPayloadJSON), TraceID: nullableStringPtr(input.TraceID)}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result.Cancelled, result.EventID = true, row.ID
		return nil
	})
	return result, err
}

// RecoverSessionControlStop finds the original stopped run by operation key,
// even when the same managed Session has started newer runs meanwhile.
func (r *GormRepository) RecoverSessionControlStop(ctx context.Context, tenantID, userID, sessionID uint64, keyHash string) (SessionControlStopRecovery, error) {
	r.log(ctx, "session_control.stop.recover", "mysql.GormRepository.RecoverSessionControlStop", "recover managed session stop")
	if tenantID == 0 || userID == 0 || sessionID == 0 || !validHandoffOperationIdentity(keyHash) {
		return SessionControlStopRecovery{}, ErrInvalidInput
	}
	var row struct {
		EventID          uint64  `gorm:"column:event_id"`
		TaskID           uint64  `gorm:"column:task_id"`
		EventPayloadJSON *string `gorm:"column:payload_json"`
	}
	err := r.with(ctx).Table("tenant_agent_task_events AS events").
		Select("events.id AS event_id, events.task_id AS task_id, events.payload_json AS payload_json").
		Joins("JOIN tenant_agent_tasks AS tasks ON tasks.id = events.task_id AND tasks.tenant_id = events.tenant_id AND tasks.user_id = events.user_id").
		Where("events.tenant_id = ? AND events.user_id = ? AND tasks.parent_session_id = ? AND events.event_type = ? AND "+jsonTextEqualsPredicate(r.db, "events.payload_json", "$.operation.key_hash"), tenantID, userID, sessionID, agenttasks.EventSessionControlStop, keyHash).
		Order("events.id DESC").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionControlStopRecovery{}, nil
	}
	if err != nil {
		return SessionControlStopRecovery{}, err
	}
	if row.EventID == 0 || row.TaskID == 0 || row.EventPayloadJSON == nil {
		return SessionControlStopRecovery{}, ErrInvalidState
	}
	return SessionControlStopRecovery{Found: true, TaskID: row.TaskID, EventID: row.EventID, EventPayloadJSON: *row.EventPayloadJSON}, nil
}

// InsertSessionControlAudit serializes on the tenant row so equal operation
// IDs cannot create duplicate audit records without a dedicated command table.
func (r *GormRepository) InsertSessionControlAudit(ctx context.Context, input SessionControlAuditInput) (SessionControlAuditResult, error) {
	r.log(ctx, "session_control.audit", "mysql.GormRepository.InsertSessionControlAudit", "record session control audit")
	item := input.AuditLogInput
	if item.TenantID == 0 || item.ActorUserID == 0 || strings.TrimSpace(item.Action) == "" || strings.TrimSpace(item.ResourceType) == "" || strings.TrimSpace(item.ResourceID) == "" || !validJSONObject(item.MetadataJSON) {
		return SessionControlAuditResult{}, ErrInvalidInput
	}
	if item.TraceID == "" {
		item.TraceID = observability.TraceID(ctx)
	}
	var result SessionControlAuditResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var tenantID uint64
		if err := tx.Table("tenants").Select("id").Where("id = ?", item.TenantID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&tenantID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		existing, err := scanAuditLog(tx.Table("tenant_audit_logs").
			Select("id, tenant_id, COALESCE(actor_user_id, 0), action, resource_type, COALESCE(resource_id, ''), COALESCE(metadata_json, ''), COALESCE(trace_id, ''), created_at").
			Where("tenant_id = ? AND actor_user_id = ? AND action = ? AND resource_type = ? AND resource_id = ?", item.TenantID, item.ActorUserID, item.Action, item.ResourceType, item.ResourceID).
			Limit(1).Row())
		if err == nil {
			result.Audit, result.Replayed = existing, true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		row := gormAuditLog{TenantID: item.TenantID, ActorUserID: nullableUint64Ptr(item.ActorUserID), Action: item.Action, ResourceType: item.ResourceType, ResourceID: nullableStringPtr(item.ResourceID), MetadataJSON: nullableStringPtr(item.MetadataJSON), TraceID: nullableStringPtr(item.TraceID)}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if isSQLite(tx) {
			if err := tx.Table("tenant_audit_logs").Where("id = ?", row.ID).
				Update("created_at", time.Now().UTC()).Error; err != nil {
				return err
			}
		}
		result.Audit = AuditLog{ID: row.ID, TenantID: item.TenantID, ActorUserID: item.ActorUserID, Action: item.Action, ResourceType: item.ResourceType, ResourceID: item.ResourceID, MetadataJSON: item.MetadataJSON, TraceID: item.TraceID, CreatedAt: time.Now().UTC()}
		return nil
	})
	return result, err
}

func (r *GormRepository) GetSessionControlAuditByKeyHash(ctx context.Context, tenantID, userID uint64, action, keyHash string) (AuditLog, error) {
	if tenantID == 0 || userID == 0 || strings.TrimSpace(action) == "" || !validHandoffOperationIdentity(keyHash) {
		return AuditLog{}, ErrInvalidInput
	}
	var rows []gormAuditLog
	keyHashWhere := jsonTextEqualsPredicate(r.db, "metadata_json", "$.session_control_operation.key_hash")
	err := r.with(ctx).Table("tenant_audit_logs").
		Where("tenant_id = ? AND actor_user_id = ? AND action = ? AND "+keyHashWhere, tenantID, userID, action, keyHash).
		Order("id ASC").Limit(2).Find(&rows).Error
	if err != nil {
		return AuditLog{}, err
	}
	if len(rows) == 0 {
		return AuditLog{}, ErrNotFound
	}
	if len(rows) != 1 {
		return AuditLog{}, ErrInvalidState
	}
	row := rows[0]
	return AuditLog{ID: row.ID, TenantID: row.TenantID, ActorUserID: valueUint64Ptr(row.ActorUserID), Action: row.Action, ResourceType: row.ResourceType, ResourceID: valueStringPtr(row.ResourceID), MetadataJSON: valueStringPtr(row.MetadataJSON), TraceID: valueStringPtr(row.TraceID)}, nil
}

func validJSONObject(value string) bool {
	if strings.TrimSpace(value) == "" || !json.Valid([]byte(value)) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal([]byte(value), &object) == nil && object != nil
}
