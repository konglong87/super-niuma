-- Team V2 shares the legacy run identity. Channel FKs are retained, but desktop
-- workflows use SQL NULL rather than fabricated inbox/account identities.
ALTER TABLE agent_team_runs
  DROP FOREIGN KEY fk_agent_team_runs_inbox_tenant,
  DROP FOREIGN KEY fk_agent_team_runs_account_tenant,
  DROP CHECK chk_agent_team_runs_status,
  MODIFY inbox_event_id BIGINT UNSIGNED NULL,
  MODIFY source_account_id BIGINT UNSIGNED NULL,
  ADD COLUMN source_kind VARCHAR(32) NOT NULL DEFAULT 'channel',
  ADD COLUMN owner_user_id BIGINT UNSIGNED NULL,
  ADD COLUMN workflow_version INT UNSIGNED NOT NULL DEFAULT 1,
  ADD COLUMN workflow_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  ADD COLUMN workflow_state_json JSON NULL,
  ADD COLUMN idempotency_key_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD COLUMN workflow_fingerprint CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  ADD UNIQUE KEY uk_workflow_scoped_key (tenant_id, owner_user_id, idempotency_key_hash),
  ADD KEY idx_workflow_active (workflow_version, status),
  ADD KEY idx_workflow_scope_active (tenant_id, owner_user_id, workflow_version, status),
  ADD CONSTRAINT chk_agent_team_runs_status CHECK (status IN ('queued', 'running', 'waiting_input', 'completed', 'partial', 'cancelled', 'failed', 'timed_out', 'cancel_requested', 'interrupted')),
  ADD CONSTRAINT fk_agent_team_runs_inbox_tenant FOREIGN KEY (tenant_id, inbox_event_id) REFERENCES channel_inbox_events(tenant_id, id),
  ADD CONSTRAINT fk_agent_team_runs_account_tenant FOREIGN KEY (tenant_id, source_account_id) REFERENCES channel_accounts(tenant_id, id),
  ADD CONSTRAINT fk_workflow_run_owner FOREIGN KEY (owner_user_id) REFERENCES tenant_users(id),
  ADD CONSTRAINT chk_workflow_run_checkpoint CHECK (workflow_version <> 2 OR (source_kind = 'desktop' AND owner_user_id IS NOT NULL AND workflow_state_json IS NOT NULL AND idempotency_key_hash IS NOT NULL AND workflow_fingerprint IS NOT NULL));

-- Refuse ambiguous pre-existing timelines instead of silently renumbering V1.
ALTER TABLE agent_team_run_events
  ADD UNIQUE KEY uk_workflow_run_event_sequence (tenant_id, team_run_id, sequence_no);

CREATE TABLE agent_team_workflow_nodes (
  tenant_id BIGINT UNSIGNED NOT NULL,
  run_id CHAR(36) NOT NULL,
  node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status VARCHAR(32) NOT NULL,
  state_json JSON NOT NULL,
  PRIMARY KEY (tenant_id, run_id, node_id),
  CONSTRAINT fk_workflow_nodes_run FOREIGN KEY (tenant_id, run_id) REFERENCES agent_team_runs(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_team_workflow_attempts (
  tenant_id BIGINT UNSIGNED NOT NULL,
  run_id CHAR(36) NOT NULL,
  node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  attempt_no INT UNSIGNED NOT NULL,
  task_id BIGINT UNSIGNED NOT NULL,
  epoch BIGINT UNSIGNED NOT NULL,
  status VARCHAR(32) NOT NULL,
  state_json JSON NOT NULL,
  PRIMARY KEY (tenant_id, run_id, node_id, attempt_no),
  UNIQUE KEY uk_workflow_attempt_task (task_id),
  CONSTRAINT fk_workflow_attempts_run FOREIGN KEY (tenant_id, run_id) REFERENCES agent_team_runs(tenant_id, id),
  CONSTRAINT fk_workflow_attempts_task FOREIGN KEY (task_id) REFERENCES tenant_agent_tasks(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE agent_team_workflow_artifacts (
  id CHAR(36) NOT NULL,
  tenant_id BIGINT UNSIGNED NOT NULL,
  run_id CHAR(36) NOT NULL,
  node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  attempt_no INT UNSIGNED NOT NULL,
  kind VARCHAR(64) NOT NULL,
  content LONGTEXT NOT NULL,
  sha256 CHAR(64) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_workflow_artifacts_run (tenant_id, run_id),
  CONSTRAINT fk_workflow_artifacts_run FOREIGN KEY (tenant_id, run_id) REFERENCES agent_team_runs(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- This is the authoritative unique engine-task ownership lookup for legacy
-- Task-operation guards. It survives terminal runs, so old tasks stay owned.
CREATE TABLE agent_team_workflow_task_bindings (
  task_id BIGINT UNSIGNED NOT NULL,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  run_id CHAR(36) NOT NULL,
  node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  attempt_no INT UNSIGNED NOT NULL,
  PRIMARY KEY (task_id),
  KEY idx_workflow_task_bindings_run (tenant_id, run_id),
  CONSTRAINT fk_workflow_task_bindings_run FOREIGN KEY (tenant_id, run_id) REFERENCES agent_team_runs(tenant_id, id),
  CONSTRAINT fk_workflow_task_bindings_task FOREIGN KEY (task_id) REFERENCES tenant_agent_tasks(id),
  CONSTRAINT fk_workflow_task_bindings_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
