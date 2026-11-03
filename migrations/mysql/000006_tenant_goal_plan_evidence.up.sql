SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS tenant_goal_plans (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  goal_id BIGINT UNSIGNED NOT NULL,
  version INT NOT NULL DEFAULT 1,
  summary TEXT NULL,
  current_step_id VARCHAR(128) NULL,
  plan_json LONGTEXT NOT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_goal_plans_goal (goal_id),
  KEY idx_tenant_goal_plans_user_recent (tenant_id, user_id, updated_at),
  CONSTRAINT fk_tenant_goal_plans_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_tenant_goal_plans_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_tenant_goal_plans_goal FOREIGN KEY (goal_id) REFERENCES tenant_goals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_goal_evidence (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  goal_id BIGINT UNSIGNED NOT NULL,
  evidence_key VARCHAR(64) NOT NULL,
  evidence_type VARCHAR(64) NOT NULL,
  summary TEXT NOT NULL,
  command TEXT NULL,
  exit_code INT NULL,
  passed BOOLEAN NOT NULL DEFAULT FALSE,
  payload_json LONGTEXT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_goal_evidence_key (goal_id, evidence_key),
  KEY idx_tenant_goal_evidence_goal (goal_id, created_at),
  KEY idx_tenant_goal_evidence_user_recent (tenant_id, user_id, created_at),
  CONSTRAINT fk_tenant_goal_evidence_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_tenant_goal_evidence_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_tenant_goal_evidence_goal FOREIGN KEY (goal_id) REFERENCES tenant_goals(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
