SET NAMES utf8mb4;

-- Add the tenant discriminator to run inputs before replacing its foreign keys.
SET @channel_v12_run_inputs_column_sql := IF(
  EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'channel_run_inputs'
      AND column_name = 'tenant_id'
  ),
  'SELECT 1',
  'ALTER TABLE channel_run_inputs ADD COLUMN tenant_id BIGINT UNSIGNED NULL AFTER id'
);
PREPARE channel_v12_run_inputs_column_stmt FROM @channel_v12_run_inputs_column_sql;
EXECUTE channel_v12_run_inputs_column_stmt;
DEALLOCATE PREPARE channel_v12_run_inputs_column_stmt;

UPDATE channel_run_inputs i
JOIN channel_runs r ON r.id = i.run_id
SET i.tenant_id = r.tenant_id
WHERE i.tenant_id IS NULL;
ALTER TABLE channel_run_inputs MODIFY tenant_id BIGINT UNSIGNED NOT NULL;

-- Composite parent keys are named exclusively for this migration. Each key is
-- added only when absent, so replaying v12 does not touch pre-existing indexes.
SET @channel_v12_accounts_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_accounts' AND index_name = 'uk_channel_v12_accounts_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_accounts ADD UNIQUE KEY uk_channel_v12_accounts_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_accounts_key_stmt FROM @channel_v12_accounts_key_sql;
EXECUTE channel_v12_accounts_key_stmt;
DEALLOCATE PREPARE channel_v12_accounts_key_stmt;

SET @channel_v12_identities_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_identities' AND index_name = 'uk_channel_v12_identities_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_identities ADD UNIQUE KEY uk_channel_v12_identities_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_identities_key_stmt FROM @channel_v12_identities_key_sql;
EXECUTE channel_v12_identities_key_stmt;
DEALLOCATE PREPARE channel_v12_identities_key_stmt;

SET @channel_v12_conversations_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_conversations' AND index_name = 'uk_channel_v12_conversations_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_conversations ADD UNIQUE KEY uk_channel_v12_conversations_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_conversations_key_stmt FROM @channel_v12_conversations_key_sql;
EXECUTE channel_v12_conversations_key_stmt;
DEALLOCATE PREPARE channel_v12_conversations_key_stmt;

SET @channel_v12_inbox_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_inbox_events' AND index_name = 'uk_channel_v12_inbox_events_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_inbox_events ADD UNIQUE KEY uk_channel_v12_inbox_events_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_inbox_key_stmt FROM @channel_v12_inbox_key_sql;
EXECUTE channel_v12_inbox_key_stmt;
DEALLOCATE PREPARE channel_v12_inbox_key_stmt;

SET @channel_v12_runs_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_runs' AND index_name = 'uk_channel_v12_runs_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_runs ADD UNIQUE KEY uk_channel_v12_runs_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_runs_key_stmt FROM @channel_v12_runs_key_sql;
EXECUTE channel_v12_runs_key_stmt;
DEALLOCATE PREPARE channel_v12_runs_key_stmt;

SET @channel_v12_messages_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'channel_messages' AND index_name = 'uk_channel_v12_messages_tenant_id'),
  'SELECT 1', 'ALTER TABLE channel_messages ADD UNIQUE KEY uk_channel_v12_messages_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_messages_key_stmt FROM @channel_v12_messages_key_sql;
EXECUTE channel_v12_messages_key_stmt;
DEALLOCATE PREPARE channel_v12_messages_key_stmt;

SET @channel_v12_users_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'tenant_users' AND index_name = 'uk_channel_v12_tenant_users_tenant_id'),
  'SELECT 1', 'ALTER TABLE tenant_users ADD UNIQUE KEY uk_channel_v12_tenant_users_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_users_key_stmt FROM @channel_v12_users_key_sql;
EXECUTE channel_v12_users_key_stmt;
DEALLOCATE PREPARE channel_v12_users_key_stmt;

SET @channel_v12_sessions_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'tenant_sessions' AND index_name = 'uk_channel_v12_tenant_sessions_tenant_id'),
  'SELECT 1', 'ALTER TABLE tenant_sessions ADD UNIQUE KEY uk_channel_v12_tenant_sessions_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_sessions_key_stmt FROM @channel_v12_sessions_key_sql;
EXECUTE channel_v12_sessions_key_stmt;
DEALLOCATE PREPARE channel_v12_sessions_key_stmt;

SET @channel_v12_session_messages_key_sql := IF(
  EXISTS (SELECT 1 FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'tenant_session_messages' AND index_name = 'uk_channel_v12_tenant_session_messages_tenant_id'),
  'SELECT 1', 'ALTER TABLE tenant_session_messages ADD UNIQUE KEY uk_channel_v12_tenant_session_messages_tenant_id (tenant_id, id)'
);
PREPARE channel_v12_session_messages_key_stmt FROM @channel_v12_session_messages_key_sql;
EXECUTE channel_v12_session_messages_key_stmt;
DEALLOCATE PREPARE channel_v12_session_messages_key_stmt;

ALTER TABLE channel_conversations
  MODIFY runtime_fingerprint VARCHAR(64) NOT NULL,
  MODIFY runtime_fingerprint_version INT NOT NULL;
ALTER TABLE channel_runs
  MODIFY runtime_fingerprint VARCHAR(64) NOT NULL,
  MODIFY runtime_fingerprint_version INT NOT NULL;
ALTER TABLE channel_messages
  MODIFY external_message_id VARCHAR(255) NULL DEFAULT NULL;

SET @channel_v12_check_sql := IF(
  EXISTS (
    SELECT 1 FROM information_schema.table_constraints
    WHERE constraint_schema = DATABASE() AND table_name = 'channel_inbox_events'
      AND constraint_name = 'chk_channel_v12_inbox_payload_lifecycle'
  ),
  'SELECT 1',
  'ALTER TABLE channel_inbox_events ADD CONSTRAINT chk_channel_v12_inbox_payload_lifecycle CHECK ((authorized = 0 AND payload_ciphertext IS NULL AND payload_ref IS NULL) OR (authorized = 1 AND (payload_ciphertext IS NOT NULL OR payload_ref IS NOT NULL OR payload_purged_at IS NOT NULL)))'
);
PREPARE channel_v12_check_stmt FROM @channel_v12_check_sql;
EXECUTE channel_v12_check_stmt;
DEALLOCATE PREPARE channel_v12_check_stmt;

-- Replace the v11 unscoped foreign keys. Every drop is conditional so a
-- partially retried migration does not fail on an already removed constraint.
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_identities' AND constraint_name = 'fk_channel_identities_account'), 'ALTER TABLE channel_identities DROP FOREIGN KEY fk_channel_identities_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;

-- Add tenant-scoped replacements using v12-specific constraint names.
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_identities' AND constraint_name = 'fk_channel_v12_identities_account_tenant'), 'ALTER TABLE channel_identities ADD CONSTRAINT fk_channel_v12_identities_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_identities' AND constraint_name = 'fk_channel_v12_identities_user_tenant'), 'ALTER TABLE channel_identities ADD CONSTRAINT fk_channel_v12_identities_user_tenant FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_conversations' AND constraint_name = 'fk_channel_v12_conversations_account_tenant'), 'ALTER TABLE channel_conversations ADD CONSTRAINT fk_channel_v12_conversations_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_conversations' AND constraint_name = 'fk_channel_v12_conversations_session_tenant'), 'ALTER TABLE channel_conversations ADD CONSTRAINT fk_channel_v12_conversations_session_tenant FOREIGN KEY (tenant_id, session_id) REFERENCES tenant_sessions(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_inbox_events' AND constraint_name = 'fk_channel_v12_inbox_events_account_tenant'), 'ALTER TABLE channel_inbox_events ADD CONSTRAINT fk_channel_v12_inbox_events_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_inbox_events' AND constraint_name = 'fk_channel_v12_inbox_events_conversation_tenant'), 'ALTER TABLE channel_inbox_events ADD CONSTRAINT fk_channel_v12_inbox_events_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_v12_runs_account_tenant'), 'ALTER TABLE channel_runs ADD CONSTRAINT fk_channel_v12_runs_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_v12_runs_conversation_tenant'), 'ALTER TABLE channel_runs ADD CONSTRAINT fk_channel_v12_runs_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_v12_runs_session_tenant'), 'ALTER TABLE channel_runs ADD CONSTRAINT fk_channel_v12_runs_session_tenant FOREIGN KEY (tenant_id, session_id) REFERENCES tenant_sessions(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_v12_messages_account_tenant'), 'ALTER TABLE channel_messages ADD CONSTRAINT fk_channel_v12_messages_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_v12_messages_conversation_tenant'), 'ALTER TABLE channel_messages ADD CONSTRAINT fk_channel_v12_messages_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_v12_messages_run_tenant'), 'ALTER TABLE channel_messages ADD CONSTRAINT fk_channel_v12_messages_run_tenant FOREIGN KEY (tenant_id, run_id) REFERENCES channel_runs(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_v12_messages_internal_tenant'), 'ALTER TABLE channel_messages ADD CONSTRAINT fk_channel_v12_messages_internal_tenant FOREIGN KEY (tenant_id, internal_message_id) REFERENCES tenant_session_messages(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_v12_outbox_account_tenant'), 'ALTER TABLE channel_outbox ADD CONSTRAINT fk_channel_v12_outbox_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_v12_outbox_conversation_tenant'), 'ALTER TABLE channel_outbox ADD CONSTRAINT fk_channel_v12_outbox_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_v12_outbox_run_tenant'), 'ALTER TABLE channel_outbox ADD CONSTRAINT fk_channel_v12_outbox_run_tenant FOREIGN KEY (tenant_id, run_id) REFERENCES channel_runs(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_v12_outbox_message_tenant'), 'ALTER TABLE channel_outbox ADD CONSTRAINT fk_channel_v12_outbox_message_tenant FOREIGN KEY (tenant_id, message_id) REFERENCES channel_messages(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_run_inputs' AND constraint_name = 'fk_channel_v12_run_inputs_run_tenant'), 'ALTER TABLE channel_run_inputs ADD CONSTRAINT fk_channel_v12_run_inputs_run_tenant FOREIGN KEY (tenant_id, run_id) REFERENCES channel_runs(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_run_inputs' AND constraint_name = 'fk_channel_v12_run_inputs_inbox_tenant'), 'ALTER TABLE channel_run_inputs ADD CONSTRAINT fk_channel_v12_run_inputs_inbox_tenant FOREIGN KEY (tenant_id, inbox_event_id) REFERENCES channel_inbox_events(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_v12_callbacks_account_tenant'), 'ALTER TABLE channel_callbacks ADD CONSTRAINT fk_channel_v12_callbacks_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_v12_callbacks_conversation_tenant'), 'ALTER TABLE channel_callbacks ADD CONSTRAINT fk_channel_v12_callbacks_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_v12_callbacks_run_tenant'), 'ALTER TABLE channel_callbacks ADD CONSTRAINT fk_channel_v12_callbacks_run_tenant FOREIGN KEY (tenant_id, run_id) REFERENCES channel_runs(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_add_sql := IF(NOT EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_v12_callbacks_user_tenant'), 'ALTER TABLE channel_callbacks ADD CONSTRAINT fk_channel_v12_callbacks_user_tenant FOREIGN KEY (tenant_id, allowed_user_id) REFERENCES tenant_users(tenant_id, id)', 'SELECT 1');
PREPARE channel_v12_add_stmt FROM @channel_v12_add_sql; EXECUTE channel_v12_add_stmt; DEALLOCATE PREPARE channel_v12_add_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_identities' AND constraint_name = 'fk_channel_identities_user'), 'ALTER TABLE channel_identities DROP FOREIGN KEY fk_channel_identities_user', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_conversations' AND constraint_name = 'fk_channel_conversations_account'), 'ALTER TABLE channel_conversations DROP FOREIGN KEY fk_channel_conversations_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_conversations' AND constraint_name = 'fk_channel_conversations_session'), 'ALTER TABLE channel_conversations DROP FOREIGN KEY fk_channel_conversations_session', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_inbox_events' AND constraint_name = 'fk_channel_inbox_events_account'), 'ALTER TABLE channel_inbox_events DROP FOREIGN KEY fk_channel_inbox_events_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_inbox_events' AND constraint_name = 'fk_channel_inbox_events_conversation'), 'ALTER TABLE channel_inbox_events DROP FOREIGN KEY fk_channel_inbox_events_conversation', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_runs_account'), 'ALTER TABLE channel_runs DROP FOREIGN KEY fk_channel_runs_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_runs_conversation'), 'ALTER TABLE channel_runs DROP FOREIGN KEY fk_channel_runs_conversation', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_runs' AND constraint_name = 'fk_channel_runs_session'), 'ALTER TABLE channel_runs DROP FOREIGN KEY fk_channel_runs_session', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_messages_account'), 'ALTER TABLE channel_messages DROP FOREIGN KEY fk_channel_messages_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_messages_conversation'), 'ALTER TABLE channel_messages DROP FOREIGN KEY fk_channel_messages_conversation', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_messages_run'), 'ALTER TABLE channel_messages DROP FOREIGN KEY fk_channel_messages_run', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_messages' AND constraint_name = 'fk_channel_messages_internal'), 'ALTER TABLE channel_messages DROP FOREIGN KEY fk_channel_messages_internal', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_outbox_account'), 'ALTER TABLE channel_outbox DROP FOREIGN KEY fk_channel_outbox_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_outbox_conversation'), 'ALTER TABLE channel_outbox DROP FOREIGN KEY fk_channel_outbox_conversation', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_outbox_run'), 'ALTER TABLE channel_outbox DROP FOREIGN KEY fk_channel_outbox_run', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_outbox' AND constraint_name = 'fk_channel_outbox_message'), 'ALTER TABLE channel_outbox DROP FOREIGN KEY fk_channel_outbox_message', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_run_inputs' AND constraint_name = 'fk_channel_run_inputs_run'), 'ALTER TABLE channel_run_inputs DROP FOREIGN KEY fk_channel_run_inputs_run', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_run_inputs' AND constraint_name = 'fk_channel_run_inputs_inbox'), 'ALTER TABLE channel_run_inputs DROP FOREIGN KEY fk_channel_run_inputs_inbox', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_callbacks_account'), 'ALTER TABLE channel_callbacks DROP FOREIGN KEY fk_channel_callbacks_account', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_callbacks_conversation'), 'ALTER TABLE channel_callbacks DROP FOREIGN KEY fk_channel_callbacks_conversation', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_callbacks_run'), 'ALTER TABLE channel_callbacks DROP FOREIGN KEY fk_channel_callbacks_run', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
SET @channel_v12_drop_sql := IF(EXISTS (SELECT 1 FROM information_schema.referential_constraints WHERE constraint_schema = DATABASE() AND table_name = 'channel_callbacks' AND constraint_name = 'fk_channel_callbacks_user'), 'ALTER TABLE channel_callbacks DROP FOREIGN KEY fk_channel_callbacks_user', 'SELECT 1');
PREPARE channel_v12_drop_stmt FROM @channel_v12_drop_sql; EXECUTE channel_v12_drop_stmt; DEALLOCATE PREPARE channel_v12_drop_stmt;
