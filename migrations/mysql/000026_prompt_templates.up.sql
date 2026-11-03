CREATE TABLE tenant_prompt_templates (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  title VARCHAR(200) NOT NULL,
  content MEDIUMTEXT NOT NULL,
  category VARCHAR(100) NOT NULL DEFAULT '',
  pinned BOOLEAN NOT NULL DEFAULT FALSE,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (id),
  UNIQUE KEY uk_prompt_templates_owner_title (tenant_id, user_id, title),
  KEY idx_prompt_templates_owner_sort (tenant_id, user_id, pinned, sort_order, title),
  CONSTRAINT fk_prompt_templates_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_prompt_templates_user FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id)
);
