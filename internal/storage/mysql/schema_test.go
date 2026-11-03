package mysql

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMultiTenantMigrationUpSchema(t *testing.T) {
	sql := readMigration(t, "000001_multi_tenant.up.sql") + "\n" + readMigration(t, "000002_telemetry_events.up.sql") + "\n" + readMigration(t, "000004_tenant_goals.up.sql") + "\n" + readMigration(t, "000006_tenant_goal_plan_evidence.up.sql")
	for _, table := range []string{
		"tenants",
		"tenant_users",
		"tenant_skills",
		"tenant_user_skill_overrides",
		"tenant_user_memories",
		"tenant_user_profiles",
		"tenant_user_documents",
		"tenant_sessions",
		"tenant_session_messages",
		"tenant_agent_tasks",
		"tenant_agent_task_events",
		"tenant_goals",
		"tenant_goal_events",
		"tenant_goal_plans",
		"tenant_goal_evidence",
		"tenant_audit_logs",
		"tenant_telemetry_events",
	} {
		assertContains(t, sql, "CREATE TABLE IF NOT EXISTS "+table)
	}

	for _, key := range []string{
		"UNIQUE KEY uk_tenants_tenant_key (tenant_key)",
		"UNIQUE KEY uk_tenant_users_user_key (tenant_id, user_key)",
		"UNIQUE KEY uk_tenant_skills_key_version (tenant_id, skill_key, version)",
		"UNIQUE KEY uk_user_skill_override (tenant_id, user_id, skill_id)",
		"UNIQUE KEY uk_user_memories_key (tenant_id, user_id, memory_key)",
		"UNIQUE KEY uk_user_profile_version (tenant_id, user_id, profile_version)",
		"UNIQUE KEY uk_user_documents_version (tenant_id, user_id, doc_type, version)",
		"UNIQUE KEY uk_tenant_sessions_key (tenant_id, user_id, session_key)",
		"UNIQUE KEY uk_session_messages_turn (session_id, turn_index)",
		"KEY idx_agent_tasks_parent_session (tenant_id, user_id, parent_session_id)",
		"KEY idx_agent_task_events_task (task_id, created_at)",
		"UNIQUE KEY uk_tenant_goals_key (tenant_id, user_id, goal_key)",
		"KEY idx_tenant_goals_status (tenant_id, user_id, status, updated_at)",
		"UNIQUE KEY uk_tenant_goal_events_key (goal_id, event_key)",
		"KEY idx_tenant_goal_events_goal (goal_id, created_at)",
		"UNIQUE KEY uk_tenant_goal_plans_goal (goal_id)",
		"UNIQUE KEY uk_tenant_goal_evidence_key (goal_id, evidence_key)",
		"KEY idx_tenant_goal_evidence_goal (goal_id, created_at)",
		"KEY idx_session_messages_trace (tenant_id, trace_id)",
		"KEY idx_tenant_audit_trace (tenant_id, trace_id)",
		"KEY idx_telemetry_trace (tenant_id, trace_id, occurred_at)",
		"KEY idx_telemetry_event (tenant_id, event_name, occurred_at)",
	} {
		assertContains(t, sql, key)
	}

	for _, seed := range []string{"('yutang'", "('aihe'", "('gongtong'"} {
		assertContains(t, sql, seed)
	}
	assertContains(t, sql, "trace_id VARCHAR(128) NULL")
	assertContains(t, sql, "ON DUPLICATE KEY UPDATE")
}

func TestMultiTenantMigrationDownDropsInDependencyOrder(t *testing.T) {
	sql := readMigration(t, "000006_tenant_goal_plan_evidence.down.sql") + "\n" + readMigration(t, "000004_tenant_goals.down.sql") + "\n" + readMigration(t, "000002_telemetry_events.down.sql") + "\n" + readMigration(t, "000001_multi_tenant.down.sql")
	order := []string{
		"DROP TABLE IF EXISTS tenant_goal_evidence",
		"DROP TABLE IF EXISTS tenant_goal_plans",
		"DROP TABLE IF EXISTS tenant_goal_events",
		"DROP TABLE IF EXISTS tenant_goals",
		"DROP TABLE IF EXISTS tenant_telemetry_events",
		"DROP TABLE IF EXISTS tenant_session_messages",
		"DROP TABLE IF EXISTS tenant_agent_task_events",
		"DROP TABLE IF EXISTS tenant_agent_tasks",
		"DROP TABLE IF EXISTS tenant_audit_logs",
		"DROP TABLE IF EXISTS tenant_sessions",
		"DROP TABLE IF EXISTS tenant_user_documents",
		"DROP TABLE IF EXISTS tenant_user_profiles",
		"DROP TABLE IF EXISTS tenant_user_memories",
		"DROP TABLE IF EXISTS tenant_user_skill_overrides",
		"DROP TABLE IF EXISTS tenant_skills",
		"DROP TABLE IF EXISTS tenant_users",
		"DROP TABLE IF EXISTS tenants",
	}
	last := -1
	for _, statement := range order {
		idx := strings.Index(sql, statement)
		if idx == -1 {
			t.Fatalf("missing statement %q in down migration", statement)
		}
		if idx <= last {
			t.Fatalf("statement %q is out of dependency order", statement)
		}
		last = idx
	}
}

// TODO-118：message_key 的定点查询靠 000010 的生成列 + 前缀索引。
//
// 这里校验的只是 migration 的**文本**：子句在不在、down 撤得干净不干净、撤的顺序对
// 不对。本机没有 MySQL 也没有 docker，这个 migration 一行都没有真跑过 —— 所以它
// 证明不了 MySQL 接受这段 DDL（尤其是「stored 生成列上建前缀索引」这一条），
// 也证明不了回填在存量数据上不会失败。
func TestSessionMessageMobileKeyMigration(t *testing.T) {
	up := readMigration(t, "000010_session_message_mobile_key.up.sql")
	for _, clause := range []string{
		"ALTER TABLE tenant_session_messages",
		"ADD COLUMN mobile_message_key LONGTEXT",
		"JSON_UNQUOTE(JSON_EXTRACT(content_json, '$.mobile.message_key'))",
		"STORED",
		"CREATE INDEX idx_session_messages_mobile_key",
		"(session_id, role, mobile_message_key(191))",
	} {
		assertContains(t, up, clause)
	}

	down := readMigration(t, "000010_session_message_mobile_key.down.sql")
	assertContains(t, down, "DROP INDEX idx_session_messages_mobile_key ON tenant_session_messages")
	assertContains(t, down, "ALTER TABLE tenant_session_messages DROP COLUMN mobile_message_key")
	// 先撤索引再撤列：反过来虽然 MySQL 也会连带删掉索引，但依赖那个连带行为
	// 就等于让回滚脚本读起来不像它做的事。
	if strings.Index(down, "DROP INDEX") > strings.Index(down, "DROP COLUMN") {
		t.Fatal("down migration drops the column before the index")
	}
}

func TestImageGenerationMigrationSchema(t *testing.T) {
	up := readMigration(t, "000021_image_generations.up.sql")
	for _, clause := range []string{
		"CREATE TABLE IF NOT EXISTS image_generations",
		"generation_id VARCHAR(64) NOT NULL",
		"tenant_id BIGINT UNSIGNED NOT NULL",
		"user_id BIGINT UNSIGNED NOT NULL",
		"session_id BIGINT UNSIGNED NOT NULL",
		"UNIQUE KEY uk_image_generations_generation_id",
		"UNIQUE KEY uk_image_generations_idempotency",
		"KEY idx_image_generations_session",
		"KEY idx_image_generations_asset",
		"KEY idx_image_generations_source_asset",
		"CONSTRAINT fk_image_generations_session",
	} {
		assertContains(t, up, clause)
	}
	down := readMigration(t, "000021_image_generations.down.sql")
	assertContains(t, down, "DROP TABLE IF EXISTS image_generations")
}

func TestAsyncImageGenerationJobsMigrationSchema(t *testing.T) {
	up := readMigration(t, "000023_async_image_generation_jobs.up.sql")
	for _, clause := range []string{
		"ALTER TABLE image_generations",
		"ADD COLUMN batch_id VARCHAR(64) NULL",
		"ADD COLUMN origin_type VARCHAR(32) NOT NULL DEFAULT 'direct'",
		"ADD COLUMN origin_ref_json JSON NULL",
		"ADD COLUMN tool_use_id VARCHAR(255) NULL",
		"ADD COLUMN attempts INT UNSIGNED NOT NULL DEFAULT 0",
		"ADD COLUMN max_attempts INT UNSIGNED NOT NULL DEFAULT 3",
		"ADD COLUMN next_attempt_at DATETIME(3) NULL",
		"ADD COLUMN lease_owner VARCHAR(255) NULL",
		"ADD COLUMN lease_until DATETIME(3) NULL",
		"ADD COLUMN heartbeat_at DATETIME(3) NULL",
		"ADD COLUMN started_at DATETIME(3) NULL",
		"ADD COLUMN cancel_requested_at DATETIME(3) NULL",
		"ADD COLUMN provider_request_id VARCHAR(255) NULL",
		"ADD COLUMN retry_of_generation_id VARCHAR(64) NULL",
		"ADD COLUMN error_class VARCHAR(64) NULL",
		"ADD COLUMN updated_at DATETIME(3)",
		"KEY idx_image_generations_claim (status, next_attempt_at, lease_until, id)",
		"KEY idx_image_generations_origin (tenant_id, origin_type, batch_id)",
		"CREATE TABLE IF NOT EXISTS image_generation_attempts",
		"UNIQUE KEY uk_image_generation_attempts_number (tenant_id, generation_id, attempt_no)",
		"CREATE TABLE IF NOT EXISTS image_completion_outbox",
		"UNIQUE KEY uk_image_completion_outbox_idempotency (tenant_id, generation_id, event_type, idempotency_key)",
		"KEY idx_image_completion_outbox_claim (tenant_id, status, next_attempt_at, lease_until, id)",
	} {
		assertContains(t, up, clause)
	}

	down := readMigration(t, "000023_async_image_generation_jobs.down.sql")
	for _, clause := range []string{
		"DROP TABLE IF EXISTS image_completion_outbox",
		"DROP TABLE IF EXISTS image_generation_attempts",
		"DROP INDEX idx_image_generations_claim ON image_generations",
		"DROP COLUMN updated_at",
	} {
		assertContains(t, down, clause)
	}
}

func TestPendingInputConsumerLeaseMigrationSchema(t *testing.T) {
	up := readMigration(t, "000024_pending_input_consumer_lease.up.sql")
	for _, clause := range []string{
		"ALTER TABLE tenant_agent_task_pending_input_settings",
		"ADD COLUMN lease_owner VARCHAR(128) NULL",
		"ADD COLUMN lease_expires_at TIMESTAMP(6) NULL",
		"ADD KEY idx_pending_input_settings_lease (lease_expires_at)",
	} {
		assertContains(t, up, clause)
	}
	down := readMigration(t, "000024_pending_input_consumer_lease.down.sql")
	for _, clause := range []string{
		"DROP KEY idx_pending_input_settings_lease",
		"DROP COLUMN lease_expires_at",
		"DROP COLUMN lease_owner",
	} {
		assertContains(t, down, clause)
	}
}

func TestChannelIntegrationMigrationSchema(t *testing.T) {
	up := readMigration(t, "000011_channel_integration.up.sql")
	for _, table := range []string{
		"channel_accounts",
		"channel_identities",
		"channel_conversations",
		"channel_inbox_events",
		"channel_messages",
		"channel_outbox",
		"channel_runs",
		"channel_run_inputs",
		"channel_callbacks",
	} {
		assertContains(t, up, "CREATE TABLE IF NOT EXISTS "+table)
	}

	for _, clause := range []string{
		"UNIQUE KEY uk_channel_accounts_tenant_provider_key (tenant_id, provider, account_key)",
		"UNIQUE KEY uk_channel_identities_account_external_user (account_id, external_user_id)",
		"UNIQUE KEY uk_channel_conversations_account_scope (account_id, scope_hash)",
		"UNIQUE KEY uk_channel_conversations_account_chat_thread (account_id, external_chat_id, external_thread_id)",
		"UNIQUE KEY uk_channel_inbox_events_account_event (account_id, provider_event_id)",
		"UNIQUE KEY uk_channel_inbox_events_account_message (account_id, provider_message_id)",
		"UNIQUE KEY uk_channel_messages_conversation_direction_external (conversation_id, direction, external_message_id)",
		"UNIQUE KEY uk_channel_outbox_delivery (account_id, idempotency_key, sequence_no, operation)",
		"UNIQUE KEY uk_channel_run_inputs_inbox (inbox_event_id)",
		"UNIQUE KEY uk_channel_run_inputs_run_event (run_id, inbox_event_id)",
		"UNIQUE KEY uk_channel_run_inputs_run_sequence (run_id, sequence_no)",
		"UNIQUE KEY uk_channel_callbacks_account_nonce (account_id, nonce_hash)",
		"scope_hash BINARY(32)",
		"external_thread_id VARCHAR(255) NOT NULL DEFAULT ''",
		"runtime_fingerprint_version INT NOT NULL",
		"payload_ciphertext MEDIUMBLOB",
		"delivery_unknown",
		"heartbeat_at DATETIME(3)",
		"cancel_requested_at DATETIME(3)",
		"CONSTRAINT fk_channel_accounts_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id)",
		"CONSTRAINT fk_channel_identities_account FOREIGN KEY (account_id) REFERENCES channel_accounts(id)",
		"CONSTRAINT fk_channel_conversations_session FOREIGN KEY (session_id) REFERENCES tenant_sessions(id)",
		"CONSTRAINT fk_channel_inbox_events_conversation FOREIGN KEY (conversation_id) REFERENCES channel_conversations(id)",
		"CONSTRAINT fk_channel_messages_run FOREIGN KEY (run_id) REFERENCES channel_runs(id)",
		"CONSTRAINT fk_channel_outbox_message FOREIGN KEY (message_id) REFERENCES channel_messages(id)",
		"CONSTRAINT fk_channel_run_inputs_run FOREIGN KEY (run_id) REFERENCES channel_runs(id)",
		"CONSTRAINT fk_channel_callbacks_run FOREIGN KEY (run_id) REFERENCES channel_runs(id)",
	} {
		assertContains(t, up, clause)
	}
}

func TestChannelIntegrationMigrationDownDropsInDependencyOrder(t *testing.T) {
	down := readMigration(t, "000011_channel_integration.down.sql")
	order := []string{
		"DROP TABLE IF EXISTS channel_callbacks",
		"DROP TABLE IF EXISTS channel_run_inputs",
		"DROP TABLE IF EXISTS channel_outbox",
		"DROP TABLE IF EXISTS channel_messages",
		"DROP TABLE IF EXISTS channel_runs",
		"DROP TABLE IF EXISTS channel_inbox_events",
		"DROP TABLE IF EXISTS channel_conversations",
		"DROP TABLE IF EXISTS channel_identities",
		"DROP TABLE IF EXISTS channel_accounts",
	}
	last := -1
	for _, statement := range order {
		idx := strings.Index(down, statement)
		if idx == -1 {
			t.Fatalf("missing statement %q in channel down migration", statement)
		}
		if idx <= last {
			t.Fatalf("statement %q is out of dependency order", statement)
		}
		last = idx
	}
}

func TestChannelInteractionsMigrationSchema(t *testing.T) {
	up := readMigration(t, "000014_channel_interactions.up.sql")
	for _, clause := range []string{
		"CREATE TABLE IF NOT EXISTS channel_interactions",
		"status VARCHAR(32) NOT NULL DEFAULT 'pending'",
		"question_ciphertext MEDIUMBLOB NOT NULL",
		"resume_checkpoint_ciphertext MEDIUMBLOB NOT NULL",
		"answer_ciphertext MEDIUMBLOB NULL",
		"nonce_hash BINARY(32) NOT NULL",
		"UNIQUE KEY uk_channel_interactions_account_nonce (account_id, nonce_hash)",
		"KEY idx_channel_interactions_conversation_status (tenant_id, account_id, conversation_id, status)",
		"CONSTRAINT fk_channel_interactions_run_tenant FOREIGN KEY (tenant_id, run_id)",
	} {
		assertContains(t, up, clause)
	}
	down := readMigration(t, "000014_channel_interactions.down.sql")
	assertContains(t, down, "DROP TABLE IF EXISTS channel_interactions")
}

func TestChannelTenantInvariantsMigrationSchema(t *testing.T) {
	up := readMigration(t, "000012_channel_integration_tenant_invariants.up.sql")
	for _, clause := range []string{
		"information_schema.statistics",
		"information_schema.referential_constraints",
		"IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints",
		"uk_channel_v12_accounts_tenant_id",
		"uk_channel_v12_identities_tenant_id",
		"uk_channel_v12_conversations_tenant_id",
		"uk_channel_v12_inbox_events_tenant_id",
		"uk_channel_v12_runs_tenant_id",
		"uk_channel_v12_messages_tenant_id",
		"uk_channel_v12_tenant_users_tenant_id",
		"uk_channel_v12_tenant_sessions_tenant_id",
		"uk_channel_v12_tenant_session_messages_tenant_id",
		"fk_channel_v12_identities_account_tenant",
		"fk_channel_v12_identities_user_tenant",
		"fk_channel_v12_conversations_account_tenant",
		"fk_channel_v12_conversations_session_tenant",
		"fk_channel_v12_inbox_events_account_tenant",
		"fk_channel_v12_inbox_events_conversation_tenant",
		"fk_channel_v12_runs_account_tenant",
		"fk_channel_v12_runs_conversation_tenant",
		"fk_channel_v12_runs_session_tenant",
		"fk_channel_v12_messages_conversation_tenant",
		"fk_channel_v12_messages_run_tenant",
		"fk_channel_v12_messages_internal_tenant",
		"fk_channel_v12_outbox_account_tenant",
		"fk_channel_v12_outbox_conversation_tenant",
		"fk_channel_v12_outbox_run_tenant",
		"fk_channel_v12_outbox_message_tenant",
		"fk_channel_v12_run_inputs_run_tenant",
		"fk_channel_v12_run_inputs_inbox_tenant",
		"fk_channel_v12_callbacks_account_tenant",
		"fk_channel_v12_callbacks_conversation_tenant",
		"fk_channel_v12_callbacks_run_tenant",
		"fk_channel_v12_callbacks_user_tenant",
		"CONSTRAINT chk_channel_v12_inbox_payload_lifecycle CHECK",
		"authorized = 0 AND payload_ciphertext IS NULL AND payload_ref IS NULL",
		"authorized = 1 AND (payload_ciphertext IS NOT NULL OR payload_ref IS NOT NULL OR payload_purged_at IS NOT NULL)",
		"MODIFY external_message_id VARCHAR(255) NULL DEFAULT NULL",
		"MODIFY runtime_fingerprint VARCHAR(64) NOT NULL",
		"MODIFY runtime_fingerprint_version INT NOT NULL",
		"ADD COLUMN tenant_id BIGINT UNSIGNED",
	} {
		assertContains(t, up, clause)
	}

	down := readMigration(t, "000012_channel_integration_tenant_invariants.down.sql")
	for _, clause := range []string{
		"information_schema.statistics",
		"information_schema.referential_constraints",
		"uk_channel_v12_accounts_tenant_id",
		"fk_channel_v12_identities_account_tenant",
		"chk_channel_v12_inbox_payload_lifecycle",
		"DROP COLUMN tenant_id",
		"Preserve NULL outbound receipt ids on rollback",
		"ADD CONSTRAINT fk_channel_identities_account FOREIGN KEY (account_id) REFERENCES channel_accounts(id)",
		"ADD CONSTRAINT fk_channel_callbacks_user FOREIGN KEY (allowed_user_id) REFERENCES tenant_users(id)",
	} {
		assertContains(t, down, clause)
	}
}

func TestMigrationURLHelpers(t *testing.T) {
	sourceURL, err := SourceURL("migrations/mysql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sourceURL, "file:///") || !strings.HasSuffix(sourceURL, "/migrations/mysql") {
		t.Fatalf("sourceURL = %q", sourceURL)
	}
	databaseURL, err := DatabaseURL("user:pass@tcp(127.0.0.1:3306)/claude?multiStatements=true")
	if err != nil {
		t.Fatal(err)
	}
	if databaseURL != "mysql://user:pass@tcp(127.0.0.1:3306)/claude?multiStatements=true" {
		t.Fatalf("databaseURL = %q", databaseURL)
	}
	if got, err := DatabaseURL("mysql://user:pass@tcp(localhost:3306)/claude"); err != nil || got != "mysql://user:pass@tcp(localhost:3306)/claude" {
		t.Fatalf("databaseURL existing scheme = %q err=%v", got, err)
	}
}

func TestTenantUserRoleIntegrityMigration(t *testing.T) {
	up := readMigration(t, "000027_tenant_user_role_integrity.up.sql")
	for _, clause := range []string{
		"UPDATE tenant_users",
		"SET role = 'member'",
		"WHERE role IS NULL",
		"MODIFY COLUMN role VARCHAR(64) NOT NULL DEFAULT 'member'",
	} {
		assertContains(t, up, clause)
	}

	down := readMigration(t, "000027_tenant_user_role_integrity.down.sql")
	assertContains(t, down, "MODIFY COLUMN role VARCHAR(64) NULL DEFAULT NULL")
}

func readMigration(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "mysql", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	return string(data)
}

func assertContains(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("migration is missing %q", want)
	}
}
