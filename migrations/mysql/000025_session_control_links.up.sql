ALTER TABLE tenant_agent_tasks
  ADD COLUMN idempotency_key VARCHAR(128) NULL AFTER trace_id,
  ADD UNIQUE KEY uk_agent_tasks_user_idempotency (tenant_id, user_id, idempotency_key);

CREATE TABLE IF NOT EXISTS tenant_session_links (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  target_session_id BIGINT UNSIGNED NOT NULL,
  source_kind VARCHAR(16) NOT NULL,
  source_session_key VARCHAR(128) NOT NULL,
  source_session_id BIGINT UNSIGNED NULL,
  relation_type VARCHAR(32) NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  metadata_json JSON NULL,
  created_by_user_id BIGINT UNSIGNED NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_session_link (tenant_id, user_id, target_session_id, source_kind, source_session_key, relation_type),
  KEY idx_session_links_source (tenant_id, user_id, source_kind, source_session_key),
  KEY idx_session_links_target (tenant_id, user_id, target_session_id),
  CONSTRAINT fk_session_links_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_session_links_user_tenant FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id),
  CONSTRAINT fk_session_links_target_tenant FOREIGN KEY (tenant_id, target_session_id) REFERENCES tenant_sessions(tenant_id, id),
  CONSTRAINT fk_session_links_created_by_tenant FOREIGN KEY (tenant_id, created_by_user_id) REFERENCES tenant_users(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
