CREATE INDEX idx_agent_task_events_tenant_user_task_id
ON tenant_agent_task_events (tenant_id, user_id, task_id, id);
