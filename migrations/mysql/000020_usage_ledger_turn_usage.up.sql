SET NAMES utf8mb4;

ALTER TABLE tenant_usage_ledger
  ADD COLUMN provider VARCHAR(128) NULL AFTER model,
  ADD COLUMN turn_index INT UNSIGNED NOT NULL DEFAULT 0 AFTER provider,
  ADD COLUMN usage_source VARCHAR(32) NULL AFTER turn_index,
  ADD COLUMN cache_creation_ephemeral_1h_input_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER cache_creation_input_tokens,
  ADD COLUMN cache_creation_ephemeral_5m_input_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0 AFTER cache_creation_ephemeral_1h_input_tokens,
  ADD KEY idx_usage_ledger_turn (tenant_id, session_id, turn_index);
