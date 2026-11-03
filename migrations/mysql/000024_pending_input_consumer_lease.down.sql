ALTER TABLE tenant_agent_task_pending_input_settings
  DROP KEY idx_pending_input_settings_lease,
  DROP COLUMN lease_expires_at,
  DROP COLUMN lease_owner;
