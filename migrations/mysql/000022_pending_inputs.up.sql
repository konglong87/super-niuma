CREATE TABLE IF NOT EXISTS tenant_agent_task_pending_inputs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id VARCHAR(128) NOT NULL,
  base_task_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
  client_input_id VARCHAR(128) NOT NULL,
  content MEDIUMTEXT NOT NULL,
  direction MEDIUMTEXT NULL,
  attachments_json JSON NULL,
  attempt INT NOT NULL DEFAULT 0,
  sequence_no BIGINT NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'queued',
  dispatched_task_id BIGINT UNSIGNED NULL,
  error_code VARCHAR(64) NULL,
  error_message VARCHAR(255) NULL,
  claimed_at TIMESTAMP(6) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_pending_input_client (tenant_id, user_id, session_id, client_input_id),
  KEY idx_pending_input_queue (tenant_id, user_id, session_id, status, sequence_no, id),
  KEY idx_pending_input_claimed (status, claimed_at),
  CONSTRAINT fk_pending_input_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_pending_input_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_pending_input_task FOREIGN KEY (dispatched_task_id) REFERENCES tenant_agent_tasks(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_agent_task_pending_input_settings (
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id VARCHAR(128) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (tenant_id, user_id, session_id),
  CONSTRAINT fk_pending_input_settings_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_pending_input_settings_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
