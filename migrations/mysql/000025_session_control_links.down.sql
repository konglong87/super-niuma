DROP TABLE IF EXISTS tenant_session_links;

ALTER TABLE tenant_agent_tasks
  DROP INDEX uk_agent_tasks_user_idempotency,
  DROP COLUMN idempotency_key;
