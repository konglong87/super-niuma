package mysql

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/goal"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

const (
	upsertQuotaConfigSQLiteSQL = `INSERT INTO tenant_quota_configs (
		tenant_id, quota_enabled, qps_limit, daily_token_limit, daily_message_limit,
		max_concurrent_requests, timezone, reserve_output_tokens, status, updated_by_user_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id) DO UPDATE SET
		quota_enabled = excluded.quota_enabled,
		qps_limit = excluded.qps_limit,
		daily_token_limit = excluded.daily_token_limit,
		daily_message_limit = excluded.daily_message_limit,
		max_concurrent_requests = excluded.max_concurrent_requests,
		timezone = excluded.timezone,
		reserve_output_tokens = excluded.reserve_output_tokens,
		status = excluded.status,
		updated_by_user_id = excluded.updated_by_user_id,
		updated_at = CURRENT_TIMESTAMP`
	upsertUsageDailySQLiteSQL = `INSERT INTO tenant_usage_daily (
		tenant_id, usage_date, source, model, request_count, message_count,
		input_tokens, output_tokens, cache_read_input_tokens,
		cache_creation_input_tokens, total_tokens, rejected_count
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, usage_date, source, model) DO UPDATE SET
		request_count = tenant_usage_daily.request_count + excluded.request_count,
		message_count = tenant_usage_daily.message_count + excluded.message_count,
		input_tokens = tenant_usage_daily.input_tokens + excluded.input_tokens,
		output_tokens = tenant_usage_daily.output_tokens + excluded.output_tokens,
		cache_read_input_tokens = tenant_usage_daily.cache_read_input_tokens + excluded.cache_read_input_tokens,
		cache_creation_input_tokens = tenant_usage_daily.cache_creation_input_tokens + excluded.cache_creation_input_tokens,
		total_tokens = tenant_usage_daily.total_tokens + excluded.total_tokens,
		rejected_count = tenant_usage_daily.rejected_count + excluded.rejected_count,
		updated_at = CURRENT_TIMESTAMP`
	selectQuotaConfigSQL = `SELECT tenant_id, quota_enabled, qps_limit, daily_token_limit, daily_message_limit, max_concurrent_requests, timezone, reserve_output_tokens, status, COALESCE(updated_by_user_id, 0), created_at, updated_at FROM tenant_quota_configs WHERE tenant_id = ? LIMIT 1`
	upsertQuotaConfigSQL = `INSERT INTO tenant_quota_configs (tenant_id, quota_enabled, qps_limit, daily_token_limit, daily_message_limit, max_concurrent_requests, timezone, reserve_output_tokens, status, updated_by_user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE quota_enabled = VALUES(quota_enabled), qps_limit = VALUES(qps_limit), daily_token_limit = VALUES(daily_token_limit), daily_message_limit = VALUES(daily_message_limit), max_concurrent_requests = VALUES(max_concurrent_requests), timezone = VALUES(timezone), reserve_output_tokens = VALUES(reserve_output_tokens), status = VALUES(status), updated_by_user_id = VALUES(updated_by_user_id), updated_at = CURRENT_TIMESTAMP(6)`
	insertUsageLedgerSQL = `INSERT INTO tenant_usage_ledger (request_id, tenant_id, user_id, session_id, trace_id, source, route, model, provider, turn_index, usage_source, status, estimated, reserved_input_tokens, reserved_output_tokens, input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens, cache_creation_ephemeral_1h_input_tokens, cache_creation_ephemeral_5m_input_tokens, total_tokens, error_code, error_message, started_at, finished_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	updateUsageLedgerSQL = `UPDATE tenant_usage_ledger SET provider = ?, turn_index = ?, usage_source = ?, status = ?, estimated = ?, input_tokens = ?, output_tokens = ?, cache_read_input_tokens = ?, cache_creation_input_tokens = ?, cache_creation_ephemeral_1h_input_tokens = ?, cache_creation_ephemeral_5m_input_tokens = ?, total_tokens = ?, error_code = ?, error_message = ?, finished_at = ? WHERE request_id = ?`
	upsertUsageDailySQL  = `INSERT INTO tenant_usage_daily (tenant_id, usage_date, source, model, request_count, message_count, input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens, total_tokens, rejected_count) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE request_count = request_count + VALUES(request_count), message_count = message_count + VALUES(message_count), input_tokens = input_tokens + VALUES(input_tokens), output_tokens = output_tokens + VALUES(output_tokens), cache_read_input_tokens = cache_read_input_tokens + VALUES(cache_read_input_tokens), cache_creation_input_tokens = cache_creation_input_tokens + VALUES(cache_creation_input_tokens), total_tokens = total_tokens + VALUES(total_tokens), rejected_count = rejected_count + VALUES(rejected_count), updated_at = CURRENT_TIMESTAMP(6)`
	listUsageDailySQL    = `SELECT id, tenant_id, usage_date, source, model, request_count, message_count, input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens, total_tokens, rejected_count, updated_at FROM tenant_usage_daily WHERE tenant_id = ? AND (? IS NULL OR usage_date >= ?) AND (? IS NULL OR usage_date <= ?) AND (? = '' OR source LIKE ? OR model LIKE ?) ORDER BY usage_date DESC, source ASC, model ASC LIMIT ? OFFSET ?`
	listUsageLedgerSQL   = `SELECT id, request_id, tenant_id, COALESCE(user_id, 0), COALESCE(session_id, 0), COALESCE(trace_id, ''), source, COALESCE(route, ''), COALESCE(model, ''), COALESCE(provider, ''), COALESCE(turn_index, 0), COALESCE(usage_source, ''), status, estimated, reserved_input_tokens, reserved_output_tokens, input_tokens, output_tokens, cache_read_input_tokens, cache_creation_input_tokens, COALESCE(cache_creation_ephemeral_1h_input_tokens, 0), COALESCE(cache_creation_ephemeral_5m_input_tokens, 0), total_tokens, COALESCE(error_code, ''), COALESCE(error_message, ''), started_at, finished_at, created_at, updated_at FROM tenant_usage_ledger WHERE tenant_id = ? AND (? IS NULL OR started_at >= ?) AND (? IS NULL OR started_at <= ?) AND (? = '' OR source = ?) AND (? = '' OR model = ?) AND (? = '' OR request_id LIKE ? OR trace_id LIKE ? OR route LIKE ? OR status LIKE ?) ORDER BY started_at DESC, id DESC LIMIT ? OFFSET ?`
	insertQuotaEventSQL  = `INSERT INTO tenant_quota_events (tenant_id, user_id, request_id, event_type, limit_type, limit_value, current_value, source, route, model, trace_id, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	listQuotaEventsSQL   = `SELECT id, tenant_id, COALESCE(user_id, 0), COALESCE(request_id, ''), event_type, COALESCE(limit_type, ''), COALESCE(limit_value, 0), COALESCE(current_value, 0), COALESCE(source, ''), COALESCE(route, ''), COALESCE(model, ''), COALESCE(trace_id, ''), COALESCE(metadata_json, ''), created_at FROM tenant_quota_events WHERE tenant_id = ? AND (? = '' OR event_type LIKE ? OR limit_type LIKE ? OR source LIKE ? OR route LIKE ? OR model LIKE ? OR trace_id LIKE ? OR request_id LIKE ?) ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
)

func DriverDSN(dsn string) string {
	dsn = strings.TrimPrefix(strings.TrimSpace(dsn), "mysql://")
	return withDefaultTimeParams(dsn)
}

func withDefaultTimeParams(dsn string) string {
	if dsn == "" {
		return ""
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	params := make([]string, 0, 3)
	if !dsnHasQueryParam(dsn, "parseTime") {
		params = append(params, "parseTime=true")
	}
	if !dsnHasQueryParam(dsn, "loc") {
		params = append(params, "loc=UTC")
	}
	if !dsnHasQueryParam(dsn, "time_zone") {
		params = append(params, "time_zone=%27%2B00%3A00%27")
	}
	if len(params) == 0 {
		return dsn
	}
	return dsn + separator + strings.Join(params, "&")
}

func dsnHasQueryParam(dsn, key string) bool {
	queryStart := strings.IndexByte(dsn, '?')
	if queryStart < 0 || queryStart == len(dsn)-1 {
		return false
	}
	query := dsn[queryStart+1:]
	for _, part := range strings.Split(query, "&") {
		name, _, _ := strings.Cut(part, "=")
		if name == key {
			return true
		}
	}
	return false
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func nullableUint64(value uint64) any {
	if value == 0 {
		return nil
	}
	return value
}

func decodeGoalPlan(goalID, planJSON string) (goal.GoalPlan, error) {
	var plan goal.GoalPlan
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return goal.GoalPlan{}, err
	}
	if plan.GoalID == "" {
		plan.GoalID = goalID
	}
	if err := plan.Validate(); err != nil {
		return goal.GoalPlan{}, err
	}
	return plan, nil
}

type skillScanner interface {
	Scan(dest ...any) error
}

func scanUser(scanner skillScanner) (User, error) {
	var user User
	var email, displayName, role, status, userInfoJSON, metadataJSON sql.NullString
	var updatedAt sql.NullTime
	if err := scanner.Scan(
		&user.ID,
		&user.TenantID,
		&user.UserKey,
		&email,
		&displayName,
		&role,
		&status,
		&userInfoJSON,
		&metadataJSON,
		&updatedAt,
	); err != nil {
		return User{}, err
	}
	user.Email = email.String
	user.DisplayName = displayName.String
	user.Role = role.String
	if user.Role == "" {
		user.Role = DefaultUserRole
	}
	user.Status = status.String
	user.UserInfoJSON = userInfoJSON.String
	user.MetadataJSON = metadataJSON.String
	user.UpdatedAt = updatedAt.Time
	return user, nil
}

func scanUsers(rows *sql.Rows) ([]User, error) {
	var out []User
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, user)
	}
	return out, rows.Err()
}

func scanSkill(scanner skillScanner) (Skill, error) {
	var skill Skill
	var description, configJSON, packageRef, packageSHA256, manifestJSON, runtimeRef sql.NullString
	if err := scanner.Scan(
		&skill.ID,
		&skill.SkillKey,
		&skill.Name,
		&description,
		&skill.ContentMD,
		&configJSON,
		&packageRef,
		&packageSHA256,
		&manifestJSON,
		&runtimeRef,
		&skill.Version,
		&skill.Enabled,
		&skill.UpdatedAt,
	); err != nil {
		return Skill{}, err
	}
	skill.Description = description.String
	skill.ConfigJSON = configJSON.String
	skill.PackageRef = packageRef.String
	skill.PackageSHA256 = packageSHA256.String
	skill.ManifestJSON = manifestJSON.String
	skill.RuntimeRef = runtimeRef.String
	return skill, nil
}

func scanSkills(rows *sql.Rows) ([]Skill, error) {
	var out []Skill
	for rows.Next() {
		skill, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func scanSkillOverride(scanner skillScanner) (SkillOverride, error) {
	var override SkillOverride
	var configJSON sql.NullString
	if err := scanner.Scan(
		&override.ID,
		&override.SkillID,
		&override.SkillKey,
		&override.Name,
		&override.Version,
		&override.Enabled,
		&configJSON,
		&override.UpdatedAt,
	); err != nil {
		return SkillOverride{}, err
	}
	override.ConfigJSON = configJSON.String
	return override, nil
}

func scanSkillOverrides(rows *sql.Rows) ([]SkillOverride, error) {
	var out []SkillOverride
	for rows.Next() {
		override, err := scanSkillOverride(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, override)
	}
	return out, rows.Err()
}

func scanEffectiveSkill(scanner skillScanner) (EffectiveSkill, error) {
	var skill EffectiveSkill
	var description, configJSON, packageRef, packageSHA256, manifestJSON, runtimeRef sql.NullString
	if err := scanner.Scan(
		&skill.ID,
		&skill.SkillKey,
		&skill.Name,
		&description,
		&skill.ContentMD,
		&configJSON,
		&packageRef,
		&packageSHA256,
		&manifestJSON,
		&runtimeRef,
		&skill.Version,
		&skill.Enabled,
		&skill.UpdatedAt,
		&skill.OverrideID,
	); err != nil {
		return EffectiveSkill{}, err
	}
	skill.Description = description.String
	skill.ConfigJSON = configJSON.String
	skill.PackageRef = packageRef.String
	skill.PackageSHA256 = packageSHA256.String
	skill.ManifestJSON = manifestJSON.String
	skill.RuntimeRef = runtimeRef.String
	return skill, nil
}

func scanEffectiveSkills(rows *sql.Rows) ([]EffectiveSkill, error) {
	var out []EffectiveSkill
	for rows.Next() {
		skill, err := scanEffectiveSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, skill)
	}
	return out, rows.Err()
}

func scanDocument(scanner skillScanner) (Document, error) {
	var doc Document
	var title, contentMD, contentJSON sql.NullString
	if err := scanner.Scan(
		&doc.ID,
		&doc.DocType,
		&title,
		&contentMD,
		&contentJSON,
		&doc.Version,
		&doc.Active,
	); err != nil {
		return Document{}, err
	}
	doc.Title = title.String
	doc.ContentMD = contentMD.String
	doc.ContentJSON = contentJSON.String
	return doc, nil
}

func scanDocuments(rows *sql.Rows) ([]Document, error) {
	var out []Document
	for rows.Next() {
		doc, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func scanKnowledgeDocument(scanner skillScanner) (KnowledgeDocument, error) {
	var doc KnowledgeDocument
	var metadata sql.NullString
	if err := scanner.Scan(
		&doc.ID,
		&doc.TenantID,
		&doc.UserID,
		&doc.Title,
		&doc.SourceType,
		&doc.Content,
		&metadata,
		&doc.Status,
		&doc.UpdatedAt,
	); err != nil {
		return KnowledgeDocument{}, err
	}
	doc.MetadataJSON = metadata.String
	return doc, nil
}

func scanKnowledgeDocuments(rows *sql.Rows) ([]KnowledgeDocument, error) {
	var out []KnowledgeDocument
	for rows.Next() {
		doc, err := scanKnowledgeDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func scanKnowledgeChunk(scanner skillScanner) (KnowledgeChunk, error) {
	var chunk KnowledgeChunk
	var metadata, embeddingRef, searchMode sql.NullString
	if err := scanner.Scan(
		&chunk.ID,
		&chunk.DocumentID,
		&chunk.Title,
		&chunk.SourceType,
		&chunk.ChunkIndex,
		&chunk.Content,
		&metadata,
		&embeddingRef,
		&chunk.Score,
		&searchMode,
	); err != nil {
		return KnowledgeChunk{}, err
	}
	chunk.MetadataJSON = metadata.String
	chunk.EmbeddingRef = embeddingRef.String
	chunk.SearchMode = searchMode.String
	return chunk, nil
}

func scanKnowledgeChunks(rows *sql.Rows) ([]KnowledgeChunk, error) {
	var out []KnowledgeChunk
	for rows.Next() {
		chunk, err := scanKnowledgeChunk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, chunk)
	}
	return out, rows.Err()
}

func scanProfile(scanner skillScanner) (Profile, error) {
	var profile Profile
	var summary sql.NullString
	if err := scanner.Scan(
		&profile.ID,
		&profile.ProfileVersion,
		&summary,
		&profile.ProfileJSON,
		&profile.GeneratedFromSessionID,
		&profile.CreatedAt,
	); err != nil {
		return Profile{}, err
	}
	profile.Summary = summary.String
	return profile, nil
}

func scanSession(scanner skillScanner) (Session, error) {
	var session Session
	var title, model, cwd sql.NullString
	var lastMessageAt sql.NullTime
	if err := scanner.Scan(
		&session.ID,
		&session.SessionKey,
		&title,
		&session.Status,
		&model,
		&cwd,
		&session.StartedAt,
		&lastMessageAt,
	); err != nil {
		return Session{}, err
	}
	session.Title = title.String
	session.Model = model.String
	session.CWD = cwd.String
	if lastMessageAt.Valid {
		session.LastMessageAt = lastMessageAt.Time
	}
	return session, nil
}

func scanSessions(rows *sql.Rows) ([]Session, error) {
	var out []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func scanMessage(scanner skillScanner) (Message, error) {
	var message Message
	var content, contentJSON, toolID, toolName, model, traceID sql.NullString
	if err := scanner.Scan(
		&message.ID,
		&message.SessionID,
		&message.TurnIndex,
		&message.Role,
		&content,
		&contentJSON,
		&toolID,
		&toolName,
		&message.IsError,
		&model,
		&message.InputTokens,
		&message.OutputTokens,
		&traceID,
		&message.CreatedAt,
	); err != nil {
		return Message{}, err
	}
	message.Content = content.String
	message.ContentJSON = contentJSON.String
	message.ToolID = toolID.String
	message.ToolName = toolName.String
	message.Model = model.String
	message.TraceID = traceID.String
	return message, nil
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var out []Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, message)
	}
	return out, rows.Err()
}

func scanAgentTask(scanner skillScanner) (AgentTask, error) {
	var item AgentTask
	var startedAt sql.NullTime
	var finishedAt sql.NullTime
	if err := scanner.Scan(
		&item.ID,
		&item.TenantID,
		&item.UserID,
		&item.ParentSessionID,
		&item.SubagentSessionKey,
		&item.AgentName,
		&item.Description,
		&item.Status,
		&item.Model,
		&item.ResultJSON,
		&item.MetadataJSON,
		&item.TraceID,
		&startedAt,
		&finishedAt,
	); err != nil {
		return AgentTask{}, err
	}
	if startedAt.Valid {
		item.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		item.FinishedAt = finishedAt.Time
	}
	return item, nil
}

func scanAgentTasks(rows *sql.Rows) ([]AgentTask, error) {
	var out []AgentTask
	for rows.Next() {
		item, err := scanAgentTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanAgentTaskEvent(scanner skillScanner) (AgentTaskEvent, error) {
	var item AgentTaskEvent
	// SQLite desktop rows written before created_at was populated stay NULL;
	// MySQL fills the column DEFAULT. Tolerate NULL instead of failing the scan.
	var createdAt sql.NullTime
	if err := scanner.Scan(
		&item.ID,
		&item.TaskID,
		&item.EventType,
		&item.PayloadJSON,
		&item.TraceID,
		&createdAt,
	); err != nil {
		return AgentTaskEvent{}, err
	}
	if createdAt.Valid {
		item.CreatedAt = createdAt.Time
	}
	return item, nil
}

func scanAgentTaskEvents(rows *sql.Rows) ([]AgentTaskEvent, error) {
	var out []AgentTaskEvent
	for rows.Next() {
		item, err := scanAgentTaskEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanGoal(scanner skillScanner) (goalRow, error) {
	var row goalRow
	var status string
	if err := scanner.Scan(
		&row.NumericID,
		&row.Goal.ID,
		&row.Goal.Objective,
		&status,
		&row.Goal.SessionID,
		&row.Goal.CWD,
		&row.Goal.Model,
		&row.Goal.TurnBudget,
		&row.Goal.TokenBudget,
		&row.Goal.TurnsUsed,
		&row.Goal.InputTokens,
		&row.Goal.OutputTokens,
		&row.Goal.LastBlocker,
		&row.Goal.RepeatedBlockerCount,
		&row.Goal.LastCheckpoint,
		&row.Goal.LastReason,
		&row.Goal.LastNextAction,
		&row.Goal.Error,
		&row.Goal.CreatedAt,
		&row.Goal.UpdatedAt,
	); err != nil {
		return goalRow{}, err
	}
	row.Goal.Status = goal.Status(status)
	return row, nil
}

func scanGoals(rows *sql.Rows) ([]goal.Goal, error) {
	var out []goal.Goal
	for rows.Next() {
		item, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item.Goal)
	}
	return out, rows.Err()
}

func scanGoalEvent(scanner skillScanner, goalID string) (goal.Event, error) {
	var item goal.Event
	var eventType, status string
	if err := scanner.Scan(
		&item.ID,
		&eventType,
		&item.Message,
		&item.SessionID,
		&item.Turn,
		&item.InputTokens,
		&item.OutputTokens,
		&item.DurationMS,
		&item.Checkpoint,
		&status,
		&item.Reason,
		&item.NextAction,
		&item.BlockerKey,
		&item.Error,
		&item.CreatedAt,
	); err != nil {
		return goal.Event{}, err
	}
	item.GoalID = goalID
	item.Type = goal.EventType(eventType)
	item.Status = goal.Status(status)
	return item, nil
}

func scanGoalEvents(rows *sql.Rows, goalID string) ([]goal.Event, error) {
	var out []goal.Event
	for rows.Next() {
		item, err := scanGoalEvent(rows, goalID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanGoalEvidence(rows *sql.Rows, goalID string) ([]goal.GoalEvidence, error) {
	var out []goal.GoalEvidence
	for rows.Next() {
		var item goal.GoalEvidence
		var evidenceType string
		var exitCode sql.NullInt64
		var payload sql.NullString
		if err := rows.Scan(
			&item.ID,
			&evidenceType,
			&item.Summary,
			&item.Command,
			&exitCode,
			&item.Passed,
			&payload,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		item.GoalID = goalID
		item.Type = goal.EvidenceType(evidenceType)
		if exitCode.Valid {
			code := int(exitCode.Int64)
			item.ExitCode = &code
		}
		if payload.Valid && strings.TrimSpace(payload.String) != "" {
			item.Payload = json.RawMessage(payload.String)
		}
		if err := item.Validate(); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func goalListStatus(filter goal.ListFilter) string {
	if filter.Active {
		return string(goal.StatusActive)
	}
	if filter.Status != "" {
		return string(filter.Status)
	}
	return ""
}

func isTerminalAgentTaskStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case agenttasks.StatusCompleted, agenttasks.StatusFailed, agenttasks.StatusCancelled, agenttasks.StatusTimeout:
		return true
	default:
		return false
	}
}

func scanAuditLog(scanner skillScanner) (AuditLog, error) {
	var item AuditLog
	// SQLite desktop rows written before created_at existed stay NULL; MySQL
	// fills the column DEFAULT. Tolerate NULL instead of failing the scan.
	var createdAt sql.NullTime
	if err := scanner.Scan(
		&item.ID,
		&item.TenantID,
		&item.ActorUserID,
		&item.Action,
		&item.ResourceType,
		&item.ResourceID,
		&item.MetadataJSON,
		&item.TraceID,
		&createdAt,
	); err != nil {
		return AuditLog{}, err
	}
	if createdAt.Valid {
		item.CreatedAt = createdAt.Time
	}
	return item, nil
}

func scanAuditLogs(rows *sql.Rows) ([]AuditLog, error) {
	var out []AuditLog
	for rows.Next() {
		item, err := scanAuditLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanTelemetryEvent(scanner skillScanner) (TelemetryEvent, error) {
	var item TelemetryEvent
	var propertiesJSON string
	if err := scanner.Scan(
		&item.ID,
		&item.TenantID,
		&item.UserID,
		&item.Name,
		&item.Category,
		&item.Source,
		&item.Status,
		&item.TraceID,
		&item.SessionID,
		&item.ResourceType,
		&item.ResourceID,
		&item.Model,
		&item.ToolName,
		&item.DurationMS,
		&item.InputTokens,
		&item.OutputTokens,
		&item.CacheCreationInputTokens,
		&item.CacheReadInputTokens,
		&item.CacheCreationEphemeral1hInputTokens,
		&item.CacheCreationEphemeral5mInputTokens,
		&item.Error,
		&propertiesJSON,
		&item.OccurredAt,
		&item.CreatedAt,
	); err != nil {
		return TelemetryEvent{}, err
	}
	if strings.TrimSpace(propertiesJSON) != "" {
		var properties map[string]any
		if err := json.Unmarshal([]byte(propertiesJSON), &properties); err == nil {
			item.Properties = properties
		}
	}
	item.Event = telemetry.RestoreTraceContext(item.Event)
	return item, nil
}

func scanTelemetryEvents(rows *sql.Rows) ([]TelemetryEvent, error) {
	var out []TelemetryEvent
	for rows.Next() {
		item, err := scanTelemetryEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanQuotaConfig(scanner skillScanner) (QuotaConfig, error) {
	var cfg QuotaConfig
	var qps, dailyTokens, dailyMessages, concurrent sql.NullInt64
	if err := scanner.Scan(
		&cfg.TenantID,
		&cfg.QuotaEnabled,
		&qps,
		&dailyTokens,
		&dailyMessages,
		&concurrent,
		&cfg.Timezone,
		&cfg.ReserveOutputTokens,
		&cfg.Status,
		&cfg.UpdatedByUserID,
		&cfg.CreatedAt,
		&cfg.UpdatedAt,
	); err != nil {
		return QuotaConfig{}, err
	}
	cfg.QPSLimit = nullInt64ToUint64Ptr(qps)
	cfg.DailyTokenLimit = nullInt64ToUint64Ptr(dailyTokens)
	cfg.DailyMessageLimit = nullInt64ToUint64Ptr(dailyMessages)
	cfg.MaxConcurrentRequests = nullInt64ToUint64Ptr(concurrent)
	return cfg, nil
}

func scanUsageDailyRows(rows *sql.Rows) ([]UsageDaily, error) {
	var out []UsageDaily
	for rows.Next() {
		var item UsageDaily
		if err := rows.Scan(
			&item.ID,
			&item.TenantID,
			&item.UsageDate,
			&item.Source,
			&item.Model,
			&item.RequestCount,
			&item.MessageCount,
			&item.InputTokens,
			&item.OutputTokens,
			&item.CacheReadInputTokens,
			&item.CacheCreationInputTokens,
			&item.TotalTokens,
			&item.RejectedCount,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanUsageLedgerRows(rows *sql.Rows) ([]UsageLedger, error) {
	var out []UsageLedger
	columns, _ := rows.Columns()
	for rows.Next() {
		var item UsageLedger
		var finishedAt sql.NullTime
		var err error
		if len(columns) >= 29 {
			err = rows.Scan(
				&item.ID,
				&item.RequestID,
				&item.TenantID,
				&item.UserID,
				&item.SessionID,
				&item.TraceID,
				&item.Source,
				&item.Route,
				&item.Model,
				&item.Provider,
				&item.Turn,
				&item.UsageSource,
				&item.Status,
				&item.Estimated,
				&item.ReservedInputTokens,
				&item.ReservedOutputTokens,
				&item.InputTokens,
				&item.OutputTokens,
				&item.CacheReadInputTokens,
				&item.CacheCreationInputTokens,
				&item.CacheCreationEphemeral1h,
				&item.CacheCreationEphemeral5m,
				&item.TotalTokens,
				&item.ErrorCode,
				&item.ErrorMessage,
				&item.StartedAt,
				&finishedAt,
				&item.CreatedAt,
				&item.UpdatedAt,
			)
		} else {
			err = rows.Scan(
				&item.ID, &item.RequestID, &item.TenantID, &item.UserID, &item.SessionID, &item.TraceID,
				&item.Source, &item.Route, &item.Model, &item.Status, &item.Estimated,
				&item.ReservedInputTokens, &item.ReservedOutputTokens, &item.InputTokens, &item.OutputTokens,
				&item.CacheReadInputTokens, &item.CacheCreationInputTokens, &item.TotalTokens,
				&item.ErrorCode, &item.ErrorMessage, &item.StartedAt, &finishedAt, &item.CreatedAt, &item.UpdatedAt,
			)
		}
		if err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			item.FinishedAt = finishedAt.Time
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanQuotaEventRows(rows *sql.Rows) ([]QuotaEvent, error) {
	var out []QuotaEvent
	for rows.Next() {
		var item QuotaEvent
		if err := rows.Scan(
			&item.ID,
			&item.TenantID,
			&item.UserID,
			&item.RequestID,
			&item.EventType,
			&item.LimitType,
			&item.LimitValue,
			&item.CurrentValue,
			&item.Source,
			&item.Route,
			&item.Model,
			&item.TraceID,
			&item.MetadataJSON,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func nullableUint64PtrValue(value *uint64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt64ToUint64Ptr(value sql.NullInt64) *uint64 {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	out := uint64(value.Int64)
	return &out
}

func usageDimension(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "*"
	}
	return value
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func normalizeSearch(search string) (string, string) {
	search = strings.TrimSpace(search)
	if search == "" {
		return "", ""
	}
	return search, "%" + strings.ReplaceAll(search, "%", "\\%") + "%"
}
