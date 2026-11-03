SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS tenants (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_key VARCHAR(64) NOT NULL,
  name VARCHAR(128) NOT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  settings_json JSON NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenants_tenant_key (tenant_key),
  KEY idx_tenants_status_deleted (status, deleted_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_key VARCHAR(128) NOT NULL,
  email VARCHAR(255) NULL,
  display_name VARCHAR(255) NULL,
  role VARCHAR(64) NOT NULL DEFAULT 'member',
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  user_info_json JSON NULL,
  metadata_json JSON NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_users_user_key (tenant_id, user_key),
  UNIQUE KEY uk_tenant_users_email (tenant_id, email),
  KEY idx_tenant_users_status (tenant_id, status, deleted_at),
  CONSTRAINT fk_tenant_users_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_skills (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  skill_key VARCHAR(128) NOT NULL,
  name VARCHAR(255) NOT NULL,
  description TEXT NULL,
  content_md LONGTEXT NOT NULL,
  config_json JSON NULL,
  version INT UNSIGNED NOT NULL DEFAULT 1,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  created_by_user_id BIGINT UNSIGNED NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_skills_key_version (tenant_id, skill_key, version),
  KEY idx_tenant_skills_enabled (tenant_id, enabled, deleted_at),
  CONSTRAINT fk_tenant_skills_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_tenant_skills_created_by FOREIGN KEY (created_by_user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_user_skill_overrides (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  skill_id BIGINT UNSIGNED NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  config_json JSON NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_skill_override (tenant_id, user_id, skill_id),
  KEY idx_user_skill_override_user (tenant_id, user_id, enabled),
  CONSTRAINT fk_user_skill_override_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_user_skill_override_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_user_skill_override_skill FOREIGN KEY (skill_id) REFERENCES tenant_skills(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_user_memories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  memory_key VARCHAR(191) NOT NULL,
  category VARCHAR(64) NOT NULL DEFAULT 'general',
  content TEXT NOT NULL,
  metadata_json JSON NULL,
  importance INT NOT NULL DEFAULT 0,
  embedding_ref VARCHAR(255) NULL,
  source VARCHAR(128) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_memories_key (tenant_id, user_id, memory_key),
  KEY idx_user_memories_category (tenant_id, user_id, category, deleted_at),
  KEY idx_user_memories_importance (tenant_id, user_id, importance, updated_at),
  CONSTRAINT fk_user_memories_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_user_memories_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_user_profiles (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  profile_version INT UNSIGNED NOT NULL DEFAULT 1,
  summary TEXT NULL,
  profile_json JSON NOT NULL,
  generated_from_session_id BIGINT UNSIGNED NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_profile_version (tenant_id, user_id, profile_version),
  KEY idx_user_profiles_latest (tenant_id, user_id, created_at),
  CONSTRAINT fk_user_profiles_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_user_profiles_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_user_documents (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  doc_type VARCHAR(64) NOT NULL,
  title VARCHAR(255) NULL,
  content_md LONGTEXT NULL,
  content_json JSON NULL,
  version INT UNSIGNED NOT NULL DEFAULT 1,
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_documents_version (tenant_id, user_id, doc_type, version),
  KEY idx_user_documents_active (tenant_id, user_id, doc_type, is_active),
  CONSTRAINT fk_user_documents_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_user_documents_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_sessions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_key VARCHAR(128) NOT NULL,
  title VARCHAR(255) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  model VARCHAR(128) NULL,
  cwd VARCHAR(1024) NULL,
  metadata_json JSON NULL,
  started_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  last_message_at TIMESTAMP(6) NULL,
  archived_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_tenant_sessions_key (tenant_id, user_id, session_key),
  KEY idx_tenant_sessions_recent (tenant_id, user_id, last_message_at),
  KEY idx_tenant_sessions_status (tenant_id, status, archived_at),
  CONSTRAINT fk_tenant_sessions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_tenant_sessions_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_session_messages (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id BIGINT UNSIGNED NOT NULL,
  turn_index INT UNSIGNED NOT NULL,
  role VARCHAR(32) NOT NULL,
  content LONGTEXT NULL,
  content_json JSON NULL,
  tool_id VARCHAR(128) NULL,
  tool_name VARCHAR(128) NULL,
  is_error TINYINT(1) NOT NULL DEFAULT 0,
  model VARCHAR(128) NULL,
  input_tokens INT UNSIGNED NOT NULL DEFAULT 0,
  output_tokens INT UNSIGNED NOT NULL DEFAULT 0,
  trace_id VARCHAR(128) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_session_messages_turn (session_id, turn_index),
  KEY idx_session_messages_user_recent (tenant_id, user_id, created_at),
  KEY idx_session_messages_trace (tenant_id, trace_id),
  CONSTRAINT fk_session_messages_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_session_messages_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_session_messages_session FOREIGN KEY (session_id) REFERENCES tenant_sessions(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_agent_tasks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  parent_session_id BIGINT UNSIGNED NULL,
  subagent_session_key VARCHAR(128) NULL,
  agent_name VARCHAR(128) NULL,
  description VARCHAR(255) NULL,
  prompt LONGTEXT NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'running',
  model VARCHAR(128) NULL,
  result_json JSON NULL,
  metadata_json JSON NULL,
  trace_id VARCHAR(128) NULL,
  started_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  finished_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  KEY idx_agent_tasks_user_recent (tenant_id, user_id, started_at),
  KEY idx_agent_tasks_parent_session (tenant_id, user_id, parent_session_id),
  KEY idx_agent_tasks_status (tenant_id, status, started_at),
  KEY idx_agent_tasks_trace (tenant_id, trace_id),
  CONSTRAINT fk_agent_tasks_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_agent_tasks_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_agent_tasks_parent_session FOREIGN KEY (parent_session_id) REFERENCES tenant_sessions(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_agent_task_events (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  task_id BIGINT UNSIGNED NOT NULL,
  event_type VARCHAR(64) NOT NULL,
  payload_json JSON NULL,
  trace_id VARCHAR(128) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_agent_task_events_task (task_id, created_at),
  KEY idx_agent_task_events_user_recent (tenant_id, user_id, created_at),
  KEY idx_agent_task_events_trace (tenant_id, trace_id),
  CONSTRAINT fk_agent_task_events_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_agent_task_events_user FOREIGN KEY (user_id) REFERENCES tenant_users(id),
  CONSTRAINT fk_agent_task_events_task FOREIGN KEY (task_id) REFERENCES tenant_agent_tasks(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_audit_logs (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  actor_user_id BIGINT UNSIGNED NULL,
  action VARCHAR(128) NOT NULL,
  resource_type VARCHAR(64) NOT NULL,
  resource_id VARCHAR(128) NULL,
  metadata_json JSON NULL,
  trace_id VARCHAR(128) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  KEY idx_tenant_audit_recent (tenant_id, created_at),
  KEY idx_tenant_audit_actor (tenant_id, actor_user_id, created_at),
  KEY idx_tenant_audit_action (tenant_id, action, created_at),
  KEY idx_tenant_audit_trace (tenant_id, trace_id),
  CONSTRAINT fk_tenant_audit_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_tenant_audit_actor FOREIGN KEY (actor_user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO tenants (tenant_key, name, status)
VALUES
  ('yutang', 'Yutang', 'active'),
  ('aihe', 'Aihe', 'active'),
  ('gongtong', 'Gongtong', 'active')
ON DUPLICATE KEY UPDATE
  name = VALUES(name),
  status = VALUES(status),
  updated_at = CURRENT_TIMESTAMP(6);
