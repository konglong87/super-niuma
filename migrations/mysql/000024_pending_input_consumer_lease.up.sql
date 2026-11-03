ALTER TABLE tenant_agent_task_pending_input_settings
  ADD COLUMN lease_owner VARCHAR(128) NULL AFTER enabled,
  ADD COLUMN lease_expires_at TIMESTAMP(6) NULL AFTER lease_owner,
  ADD KEY idx_pending_input_settings_lease (lease_expires_at);
