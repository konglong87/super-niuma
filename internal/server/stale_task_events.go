package server

import (
	"context"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// routedStaleAgentTaskStore keeps the reaper's cross-tenant state transition
// on the control plane while routing its high-frequency event to the selected
// transcript backend. The task row is passed explicitly because the reaper
// scans across tenants and cannot use a request-scoped TenantService lookup.
type routedStaleAgentTaskStore struct {
	base   StaleAgentTaskStore
	events SessionEventStore
}

func (s routedStaleAgentTaskStore) ListStaleRunningAgentTasks(ctx context.Context, startedBefore time.Time, limit int) ([]mysqlstore.AgentTask, error) {
	return s.base.ListStaleRunningAgentTasks(ctx, startedBefore, limit)
}

func (s routedStaleAgentTaskStore) FailStaleAgentTask(ctx context.Context, tenantID, userID, taskID uint64, resultJSON string) error {
	return s.base.FailStaleAgentTask(ctx, tenantID, userID, taskID, resultJSON)
}

func (s routedStaleAgentTaskStore) AppendAgentTaskEvent(ctx context.Context, input agenttasks.EventInput) (uint64, error) {
	return s.base.AppendAgentTaskEvent(ctx, input)
}

func (s routedStaleAgentTaskStore) AppendAgentTaskEventForTask(ctx context.Context, task mysqlstore.AgentTask, input agenttasks.EventInput) (uint64, error) {
	return s.events.AppendTaskEvent(ctx, task, input)
}
