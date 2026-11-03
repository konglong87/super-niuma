SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS agent_provisionings (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  profile_key VARCHAR(64) NOT NULL,
  account_key VARCHAR(128) NOT NULL,
  credential_ref VARCHAR(255) NOT NULL,
  supervisor VARCHAR(32) NOT NULL DEFAULT 'screen',
  status VARCHAR(32) NOT NULL DEFAULT 'draft',
  worker_spec_json JSON NOT NULL,
  worker_status_json JSON NULL,
  checks_json JSON NULL,
  last_error_code VARCHAR(128) NULL,
  last_error_message VARCHAR(1024) NULL,
  created_by_user_id BIGINT UNSIGNED NOT NULL,
  updated_by_user_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_agent_provisionings_tenant_account (tenant_id, account_key),
  KEY idx_agent_provisionings_profile (tenant_id, profile_key, status),
  CONSTRAINT fk_agent_provisionings_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_agent_provisionings_created_by FOREIGN KEY (tenant_id, created_by_user_id) REFERENCES tenant_users(tenant_id, id),
  CONSTRAINT fk_agent_provisionings_updated_by FOREIGN KEY (tenant_id, updated_by_user_id) REFERENCES tenant_users(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
