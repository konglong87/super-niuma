package mysql

import (
	"context"
	"sync/atomic"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"gorm.io/gorm"
)

func TestWorkflowTransactionRetryRebuildsRolledBackTaskProjection(t *testing.T) {
	for _, stage := range []string{"create", "save"} {
		t.Run(stage, func(t *testing.T) {
			repo, _, input := workflowTestRepo(t)
			ctx := context.Background()
			store := NewWorkflowStore(repo)
			if stage == "save" {
				input = workflowTestCreate(t, repo, input)
			}
			input.Status = workflow.Running
			input.Nodes = []workflow.NodeRun{{NodeID: "n1", Status: workflow.Running, Attempts: []workflow.Attempt{{Number: 1, Status: workflow.Running}}}}
			var injected atomic.Bool
			const hook = "test_workflow_deadlock_after_projections"
			// Execute the real writes against SQLite, then emulate MySQL's deadlock
			// error at the last event insert. The outer transaction must roll everything
			// back, reset its private TaskIDs, and retry from the caller's original Run.
			if err := repo.db.Callback().Create().After("gorm:create").Register(hook, func(tx *gorm.DB) {
				if tx.Statement.Table == "agent_team_run_events" && injected.CompareAndSwap(false, true) {
					tx.AddError(&drivermysql.MySQLError{Number: 1213, Message: "injected deadlock after task projection"})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { repo.db.Callback().Create().Remove(hook) })
			var saved workflow.Run
			var err error
			if stage == "create" {
				var created bool
				saved, created, err = store.Create(ctx, input, []workflow.Event{{Type: workflow.EventQueued}})
				if !created {
					t.Errorf("retry did not create run")
				}
			} else {
				saved, err = store.Save(ctx, input.Revision, input, []workflow.Event{{Type: workflow.EventAgentStarted}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if !injected.Load() || saved.Nodes[0].Attempts[0].TaskID == 0 {
				t.Fatal("retry did not prepare a durable task")
			}
			if input.Nodes[0].Attempts[0].TaskID != 0 {
				t.Fatal("rollback/retry mutated caller TaskID")
			}
			for _, table := range []string{"agent_team_runs", "tenant_agent_tasks", "tenant_agent_task_events", "agent_team_workflow_nodes", "agent_team_workflow_attempts", "agent_team_workflow_task_bindings"} {
				if count := workflowCount(t, repo, table); count != 1 {
					t.Fatalf("retry leaked %s rows: %d", table, count)
				}
			}
		})
	}
}
