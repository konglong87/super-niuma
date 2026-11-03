ALTER TABLE tenant_usage_ledger
  DROP KEY idx_usage_ledger_turn,
  DROP COLUMN cache_creation_ephemeral_5m_input_tokens,
  DROP COLUMN cache_creation_ephemeral_1h_input_tokens,
  DROP COLUMN usage_source,
  DROP COLUMN turn_index,
  DROP COLUMN provider;
