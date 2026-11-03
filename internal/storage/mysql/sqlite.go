package mysql

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// OpenSQLiteGormRepository opens the desktop persistence database while
// retaining the existing tenant repository contract. The desktop schema
// includes the session-control and image runtime surfaces used by the local
// server.
func OpenSQLiteGormRepository(ctx context.Context, path string, logger *slog.Logger) (*GormRepository, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// SQLite omits SELECT FOR UPDATE. Reserve the writer at BEGIN instead of
	// upgrading a deferred read transaction after another connection has begun
	// writing (an immediate SQLITE_BUSY that the busy timeout cannot resolve).
	// Driver options apply to every pooled connection, including replacements.
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	options := url.Values{"_txlock": {"immediate"}}
	dsn.RawQuery = options.Encode()
	db, err := gorm.Open(gormsqlite.Open(dsn.String()), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	repo := &GormRepository{db: db, logger: logger}
	if err := migrateSQLiteDesktopSchema(db); err != nil {
		_ = repo.Close()
		return nil, err
	}
	return repo, nil
}

func migrateSQLiteDesktopSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&gormTenant{}, &gormTenantUser{}, &gormMemory{}, &gormSkill{}, &gormSkillOverride{}, &gormDocument{},
		&gormKnowledgeDocument{}, &gormKnowledgeChunk{}, &gormProfile{}, &gormSessionLink{},
		&gormGoal{}, &gormGoalEvent{}, &gormGoalPlan{}, &gormGoalEvidence{},
		&gormSession{}, &gormMessage{},
		&gormAgentTask{}, &gormAgentTaskEvent{}, &gormAuditLog{}, &gormTelemetryEvent{},
		&gormPendingInput{}, &gormPendingInputSetting{},
		&gormAgentProfile{}, &gormAgentProfileAssignment{}, &gormAgentProfileChannelBinding{},
		&gormAgentTeam{}, &gormAgentTeamMember{}, &gormAgentTeamBinding{},
		&gormWorkflowRun{}, &gormAgentTeamMailbox{}, &gormAgentTeamRunEvent{},
		&gormWorkflowNode{}, &gormWorkflowAttempt{}, &gormWorkflowArtifact{}, &gormWorkflowTaskBinding{},
		&gormPromptTemplate{},
		&gormAgentProvisioning{},
		&gormChannelAccount{}, &gormChannelIdentity{}, &gormChannelConversation{},
		&gormChannelInboxEvent{}, &gormChannelRun{}, &gormChannelRunInput{},
		&gormChannelInteraction{}, &gormChannelMessage{}, &gormChannelOutbox{},
		&gormChannelCallback{}, &gormChannelReaction{},
		&gormMediaAsset{}, &gormImageGeneration{},
		&gormImageGenerationAttempt{}, &gormImageCompletionOutbox{},
	); err != nil {
		return err
	}
	// These timestamps are part of the existing MySQL contract but are not
	// represented by the write models. Keep them explicit for SQLite reads.
	for _, statement := range []string{
		`ALTER TABLE tenants ADD COLUMN created_at DATETIME`,
		`ALTER TABLE tenants ADD COLUMN updated_at DATETIME`,
		`ALTER TABLE tenant_users ADD COLUMN created_at DATETIME`,
		`ALTER TABLE tenant_users ADD COLUMN updated_at DATETIME`,
		`ALTER TABLE tenant_sessions ADD COLUMN created_at DATETIME`,
		`ALTER TABLE tenant_sessions ADD COLUMN started_at DATETIME`,
		`ALTER TABLE tenant_sessions ADD COLUMN archived_at DATETIME`,
		`ALTER TABLE tenant_session_messages ADD COLUMN created_at DATETIME`,
		`ALTER TABLE tenant_agent_tasks ADD COLUMN started_at DATETIME`,
		`ALTER TABLE tenant_agent_tasks ADD COLUMN finished_at DATETIME`,
		`ALTER TABLE tenant_agent_task_events ADD COLUMN created_at DATETIME`,
		`ALTER TABLE tenant_audit_logs ADD COLUMN created_at DATETIME`,
	} {
		if err := addSQLiteColumnIfMissing(db, statement); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_teams_version ON agent_teams (tenant_id, team_key, team_version)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_team_members_key ON agent_team_members (team_id, member_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_workflow_scoped_key ON agent_team_runs (tenant_id, owner_user_id, idempotency_key_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_workflow_run_event_sequence ON agent_team_run_events (tenant_id, team_run_id, sequence_no)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_active ON agent_team_runs (workflow_version, status)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_scope_active ON agent_team_runs (tenant_id, owner_user_id, workflow_version, status)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenants_tenant_key ON tenants (tenant_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_users_user_key ON tenant_users (tenant_id, user_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_skills_key_version ON tenant_skills (tenant_id, skill_key, version)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_user_skill_override ON tenant_user_skill_overrides (tenant_id, user_id, skill_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_profiles_version_owner ON agent_profiles (tenant_id, profile_key, profile_version, owner_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_profile_assignments_surface ON agent_profile_assignments (tenant_id, user_id, surface)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_profile_channel_bindings_profile ON agent_profile_channel_bindings (tenant_id, profile_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_user_memories_key ON tenant_user_memories (tenant_id, user_id, memory_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_user_profile_version ON tenant_user_profiles (tenant_id, user_id, profile_version)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_session_link ON tenant_session_links (tenant_id, user_id, target_session_id, source_kind, source_session_key, relation_type)`,
		`CREATE INDEX IF NOT EXISTS idx_session_links_source ON tenant_session_links (tenant_id, user_id, source_kind, source_session_key)`,
		`CREATE INDEX IF NOT EXISTS idx_session_links_target ON tenant_session_links (tenant_id, user_id, target_session_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_knowledge_chunks_doc_index ON tenant_knowledge_chunks (document_id, chunk_index)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_goals_key ON tenant_goals (tenant_id, user_id, goal_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_goal_events_key ON tenant_goal_events (goal_id, event_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_goal_plans_goal ON tenant_goal_plans (goal_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_goal_evidence_key ON tenant_goal_evidence (goal_id, evidence_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_provisionings_tenant_account ON agent_provisionings (tenant_id, account_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_accounts_tenant_provider_key ON channel_accounts (tenant_id, provider, account_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_identities_account_user ON channel_identities (account_id, external_user_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_conversations_account_scope ON channel_conversations (account_id, scope_hash)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_inbox_account_event ON channel_inbox_events (account_id, provider_event_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_inbox_account_message ON channel_inbox_events (account_id, provider_message_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_run_inputs_inbox ON channel_run_inputs (inbox_event_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_run_inputs_run_sequence ON channel_run_inputs (run_id, sequence_no)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_messages_delivery ON channel_messages (conversation_id, direction, external_message_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_outbox_delivery ON channel_outbox (account_id, idempotency_key, sequence_no, operation)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_channel_reactions_account_message ON channel_reactions (tenant_id, account_id, provider_message_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_sessions_key ON tenant_sessions (tenant_id, user_id, session_key)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_session_messages_turn ON tenant_session_messages (session_id, turn_index)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_parent_session ON tenant_agent_tasks (tenant_id, user_id, parent_session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_task_events_task ON tenant_agent_task_events (task_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tenant_audit_recent ON tenant_audit_logs (tenant_id, created_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_prompt_templates_owner_title ON tenant_prompt_templates (tenant_id, user_id, title)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_image_generations_generation_id ON image_generations (generation_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_image_generations_idempotency ON image_generations (tenant_id, user_id, session_id, idempotency_key)`,
		`CREATE INDEX IF NOT EXISTS idx_image_generations_session ON image_generations (tenant_id, user_id, session_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_image_generations_asset ON image_generations (tenant_id, user_id, session_id, asset_id)`,
		`CREATE INDEX IF NOT EXISTS idx_image_generations_source_asset ON image_generations (tenant_id, user_id, session_id, source_asset_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_image_generation_attempts_number ON image_generation_attempts (tenant_id, generation_id, attempt_no)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uk_image_completion_outbox_idempotency ON image_completion_outbox (tenant_id, generation_id, event_type, idempotency_key)`,
		`CREATE INDEX IF NOT EXISTS idx_image_completion_outbox_claim ON image_completion_outbox (tenant_id, status, next_attempt_at, lease_until, id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS tenant_quota_configs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			quota_enabled INTEGER NOT NULL DEFAULT 0,
			qps_limit INTEGER NULL,
			daily_token_limit INTEGER NULL,
			daily_message_limit INTEGER NULL,
			max_concurrent_requests INTEGER NULL,
			timezone TEXT NOT NULL DEFAULT 'UTC',
			reserve_output_tokens INTEGER NOT NULL DEFAULT 4096,
			status TEXT NOT NULL DEFAULT 'active',
			updated_by_user_id INTEGER NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (tenant_id)
		)`,
		`CREATE TABLE IF NOT EXISTS tenant_usage_ledger (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			request_id TEXT NOT NULL UNIQUE,
			tenant_id INTEGER NOT NULL,
			user_id INTEGER NULL,
			session_id INTEGER NULL,
			trace_id TEXT NULL,
			source TEXT NOT NULL,
			route TEXT NULL,
			model TEXT NULL,
			provider TEXT NULL,
			turn_index INTEGER NOT NULL DEFAULT 0,
			usage_source TEXT NULL,
			status TEXT NOT NULL,
			estimated INTEGER NOT NULL DEFAULT 0,
			reserved_input_tokens INTEGER NOT NULL DEFAULT 0,
			reserved_output_tokens INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_ephemeral_1h_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_ephemeral_5m_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			error_code TEXT NULL,
			error_message TEXT NULL,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tenant_usage_daily (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			usage_date DATE NOT NULL,
			source TEXT NOT NULL DEFAULT '*',
			model TEXT NOT NULL DEFAULT '*',
			request_count INTEGER NOT NULL DEFAULT 0,
			message_count INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			rejected_count INTEGER NOT NULL DEFAULT 0,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (tenant_id, usage_date, source, model)
		)`,
		`CREATE TABLE IF NOT EXISTS tenant_quota_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			user_id INTEGER NULL,
			request_id TEXT NULL,
			event_type TEXT NOT NULL,
			limit_type TEXT NULL,
			limit_value INTEGER NULL,
			current_value INTEGER NULL,
			source TEXT NULL,
			route TEXT NULL,
			model TEXT NULL,
			trace_id TEXT NULL,
			metadata_json TEXT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_ledger_tenant_started ON tenant_usage_ledger (tenant_id, started_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_daily_date ON tenant_usage_daily (usage_date, tenant_id)`,
		`CREATE INDEX IF NOT EXISTS idx_quota_events_tenant_created ON tenant_quota_events (tenant_id, created_at, id)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`UPDATE tenants SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP), updated_at = COALESCE(updated_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_users SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP), updated_at = COALESCE(updated_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_sessions SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP), started_at = COALESCE(started_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_session_messages SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_agent_tasks SET started_at = COALESCE(started_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_agent_task_events SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP)`,
		`UPDATE tenant_audit_logs SET created_at = COALESCE(created_at, CURRENT_TIMESTAMP)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func addSQLiteColumnIfMissing(db *gorm.DB, statement string) error {
	var table, column string
	parts := strings.Fields(statement)
	if len(parts) < 6 {
		return fmt.Errorf("invalid sqlite migration statement %q", statement)
	}
	table, column = parts[2], parts[5]
	var count int64
	if err := db.Raw(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := db.Exec(statement).Error; err != nil {
		return fmt.Errorf("sqlite migration failed: %s: %w", statement, err)
	}
	return nil
}
