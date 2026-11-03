SET NAMES utf8mb4;

ALTER TABLE channel_conversations
  ADD COLUMN permission_mode VARCHAR(32) NOT NULL DEFAULT 'ask' AFTER workspace_realpath;

CREATE TABLE IF NOT EXISTS channel_reactions (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  account_id BIGINT UNSIGNED NOT NULL,
  conversation_id BIGINT UNSIGNED NOT NULL,
  provider_message_id VARCHAR(255) NOT NULL,
  desired_emoji VARCHAR(64) NOT NULL,
  current_emoji VARCHAR(64) NULL,
  reaction_id VARCHAR(255) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'pending',
  attempts INT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  lease_owner VARCHAR(255) NULL,
  lease_until DATETIME(3) NULL,
  last_error_code VARCHAR(128) NULL,
  last_error_message VARCHAR(1024) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_channel_reactions_tenant_account_message (tenant_id, account_id, provider_message_id),
  KEY idx_channel_reactions_reconcile (status, next_attempt_at, lease_until),
  CONSTRAINT fk_channel_reactions_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_channel_reactions_account_tenant FOREIGN KEY (tenant_id, account_id) REFERENCES channel_accounts(tenant_id, id),
  CONSTRAINT fk_channel_reactions_conversation_tenant FOREIGN KEY (tenant_id, conversation_id) REFERENCES channel_conversations(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
