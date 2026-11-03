-- GUARDED DOWNGRADE: stop all writers and take a consistent backup of runs,
-- workflow projection/binding tables, run events, engine tasks and task events.
-- Export/archive V2 runs with their events/projections/bindings and verify the
-- backup before removing them manually in FK order. This migration NEVER
-- deletes V2 history or unbinds engine tasks on the operator's behalf.
-- CREATE ROUTINE privilege is required. A failed guard leaves this routine
-- installed; retry is safe. MySQL DDL is not transactionally rolled back.
DROP PROCEDURE IF EXISTS niuma_guard_workflow_v2_downgrade;
CREATE PROCEDURE niuma_guard_workflow_v2_downgrade()
BEGIN
  IF EXISTS (SELECT 1 FROM agent_team_runs WHERE workflow_version <> 1 OR workflow_state_json IS NOT NULL OR owner_user_id IS NOT NULL OR idempotency_key_hash IS NOT NULL OR workflow_fingerprint IS NOT NULL OR source_kind <> 'channel' OR inbox_event_id IS NULL OR source_account_id IS NULL OR status IN ('cancel_requested', 'interrupted'))
     OR EXISTS (SELECT 1 FROM agent_team_workflow_nodes)
     OR EXISTS (SELECT 1 FROM agent_team_workflow_attempts)
     OR EXISTS (SELECT 1 FROM agent_team_workflow_artifacts)
     OR EXISTS (SELECT 1 FROM agent_team_workflow_task_bindings)
  THEN
    SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'Workflow V2 downgrade refused: stop writers, backup and archive V2 data before retry';
  END IF;
END;
CALL niuma_guard_workflow_v2_downgrade();
DROP PROCEDURE niuma_guard_workflow_v2_downgrade;

DROP TABLE agent_team_workflow_task_bindings;
DROP TABLE agent_team_workflow_artifacts;
DROP TABLE agent_team_workflow_attempts;
DROP TABLE agent_team_workflow_nodes;
ALTER TABLE agent_team_run_events DROP INDEX uk_workflow_run_event_sequence;
ALTER TABLE agent_team_runs
  DROP FOREIGN KEY fk_workflow_run_owner,
  DROP FOREIGN KEY fk_agent_team_runs_inbox_tenant,
  DROP FOREIGN KEY fk_agent_team_runs_account_tenant,
  DROP CHECK chk_workflow_run_checkpoint,
  DROP CHECK chk_agent_team_runs_status,
  DROP INDEX uk_workflow_scoped_key,
  DROP INDEX idx_workflow_active,
  DROP INDEX idx_workflow_scope_active,
  DROP COLUMN source_kind,
  DROP COLUMN owner_user_id,
  DROP COLUMN workflow_version,
  DROP COLUMN workflow_revision,
  DROP COLUMN workflow_state_json,
  DROP COLUMN idempotency_key_hash,
  DROP COLUMN workflow_fingerprint,
  MODIFY inbox_event_id BIGINT UNSIGNED NOT NULL,
  MODIFY source_account_id BIGINT UNSIGNED NOT NULL,
  ADD CONSTRAINT chk_agent_team_runs_status CHECK (status IN ('queued', 'running', 'waiting_input', 'completed', 'partial', 'cancelled', 'failed', 'timed_out')),
  ADD CONSTRAINT fk_agent_team_runs_inbox_tenant FOREIGN KEY (tenant_id, inbox_event_id) REFERENCES channel_inbox_events(tenant_id, id),
  ADD CONSTRAINT fk_agent_team_runs_account_tenant FOREIGN KEY (tenant_id, source_account_id) REFERENCES channel_accounts(tenant_id, id);
