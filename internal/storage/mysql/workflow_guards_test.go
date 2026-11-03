package mysql

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
)

func TestWorkflowGuardsRejectLegacyTaskMutationAndKeepStoreAuthority(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	taskID := run.Nodes[0].Attempts[0].TaskID
	// Even if editable metadata loses every workflow marker, ownership remains in
	// the authoritative binding table. Never treat JSON as the permission guard.
	if err := repo.db.Table("tenant_agent_tasks").Where("id = ?", taskID).Update("metadata_json", `{}`).Error; err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name string
		call func() error
		want error
	}{
		{"patch", func() error {
			return repo.UpdateAgentTask(ctx, taskID, agenttasks.TaskUpdate{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, Status: agenttasks.StatusCompleted, ResultJSON: `{"content":"forged"}`, MetadataJSON: `{"source":"ordinary"}`})
		}, workflow.ErrOwnedTask},
		{"metadata_only", func() error {
			return repo.UpdateAgentTask(ctx, taskID, agenttasks.TaskUpdate{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, MetadataJSON: `{"source":"ordinary"}`})
		}, workflow.ErrOwnedTask},
		{"empty_patch", func() error {
			return repo.UpdateAgentTask(ctx, taskID, agenttasks.TaskUpdate{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID})
		}, workflow.ErrOwnedTask},
		{"finish", func() error { return repo.FinishAgentTask(ctx, taskID, "", `{"content":"forged"}`) }, workflow.ErrOwnedTask},
		{"cancel", func() error { return repo.CancelAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID, `{}`) }, workflow.ErrOwnedTask},
		{"cancel_if_running", func() error {
			changed, err := repo.CancelAgentTaskIfRunning(ctx, run.Scope.TenantID, run.Scope.UserID, taskID, `{}`)
			if changed {
				t.Error("owned task cancelled")
			}
			return err
		}, workflow.ErrOwnedTask},
		{"event", func() error {
			id, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, TaskID: taskID, EventType: agenttasks.EventCompleted, PayloadJSON: `{"text":"forged"}`})
			if id != 0 {
				t.Error("owned task got legacy event")
			}
			return err
		}, workflow.ErrOwnedTask},
		{"session_stop", func() error {
			result, err := repo.CancelAgentTaskForSessionControl(ctx, SessionControlStopInput{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, TaskID: taskID, ResultJSON: `{}`, EventPayloadJSON: `{}`})
			if result.Cancelled || result.EventID != 0 {
				t.Error("owned task got a SessionControlStop")
			}
			return err
		}, workflow.ErrOwnedTask},
		{"reaper", func() error { return repo.FailStaleAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID, `{}`) }, ErrNotFound},
	}
	assertBlocked := func() {
		t.Helper()
		before, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		eventCount := workflowCount(t, repo, "tenant_agent_task_events")
		for _, check := range checks {
			t.Run(check.name, func(t *testing.T) {
				if err := check.call(); !errors.Is(err, check.want) {
					t.Fatalf("guard error=%v want=%v", err, check.want)
				}
				after, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
				if err != nil || !reflect.DeepEqual(after, before) {
					t.Fatalf("legacy write mutated task: %+v %v", after, err)
				}
				if n := workflowCount(t, repo, "tenant_agent_task_events"); n != eventCount {
					t.Fatalf("legacy write appended event: %d want=%d", n, eventCount)
				}
			})
		}
	}
	assertBlocked()
	persisted, err := NewWorkflowStore(repo).Get(ctx, run.Scope, run.ID)
	if err != nil || !reflect.DeepEqual(persisted, run) {
		t.Fatalf("legacy writes changed Run: %v", err)
	}
	// Only the Run transaction may finish the task and publish its real event.
	run.Nodes[0].Attempts[0].Status = workflow.Completed
	run.Nodes[0].Status = workflow.Completed
	run.Nodes[0].Output = "authorized"
	run.Status = workflow.Completed
	saved, err := NewWorkflowStore(repo).Save(ctx, run.Revision, run, []workflow.Event{{Type: workflow.EventCompleted, Status: workflow.Completed}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
	if err != nil || task.Status != agenttasks.StatusCompleted {
		t.Fatalf("Run store could not finish owned task %+v %v", task, err)
	}
	if saved.Nodes[0].Attempts[0].FinishedAt == nil || workflowCount(t, repo, "tenant_agent_task_events") != 2 {
		t.Fatal("Run store missed terminal task event/checkpoint")
	}
	// The binding is not a lease: terminal tasks cannot fall back to legacy writes.
	assertBlocked()
}

func workflowGuardOrdinaryTask(t *testing.T, repo *GormRepository, scope workflow.Scope) uint64 {
	t.Helper()
	id, err := repo.CreateAgentTask(context.Background(), agenttasks.TaskInput{TenantID: scope.TenantID, UserID: scope.UserID, Status: agenttasks.StatusRunning, MetadataJSON: `{"workflow_run_id":"fake","workflow_node_id":"fake"}`})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWorkflowGuardsPreserveOrdinaryTaskOperations(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	_ = NewWorkflowStore(repo)
	checks := []struct {
		name, want string
		call       func(uint64) error
	}{
		{"patch", agenttasks.StatusCompleted, func(id uint64) error {
			return repo.UpdateAgentTask(ctx, id, agenttasks.TaskUpdate{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, Status: agenttasks.StatusCompleted, ResultJSON: `{"content":"ordinary"}`})
		}},
		{"finish", agenttasks.StatusCompleted, func(id uint64) error { return repo.FinishAgentTask(ctx, id, "", `{}`) }},
		{"cancel", agenttasks.StatusCancelled, func(id uint64) error {
			return repo.CancelAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, id, `{}`)
		}},
		{"cancel_if_running", agenttasks.StatusCancelled, func(id uint64) error {
			changed, err := repo.CancelAgentTaskIfRunning(ctx, input.Scope.TenantID, input.Scope.UserID, id, `{}`)
			if !changed {
				t.Error("ordinary task not cancelled")
			}
			return err
		}},
		{"event", agenttasks.StatusRunning, func(id uint64) error {
			event, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, TaskID: id, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"text":"ordinary"}`})
			if event == 0 {
				t.Error("ordinary event missing")
			}
			return err
		}},
		{"session_stop", agenttasks.StatusCancelled, func(id uint64) error {
			result, err := repo.CancelAgentTaskForSessionControl(ctx, SessionControlStopInput{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, TaskID: id, ResultJSON: `{}`, EventPayloadJSON: `{}`})
			if !result.Cancelled || result.EventID == 0 {
				t.Error("ordinary SessionControlStop did not commit")
			}
			return err
		}},
		{"reaper", agenttasks.StatusFailed, func(id uint64) error {
			return repo.FailStaleAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, id, `{}`)
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			id := workflowGuardOrdinaryTask(t, repo, input.Scope)
			if err := check.call(id); err != nil {
				t.Fatal(err)
			}
			task, err := repo.GetAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, id)
			if err != nil || task.Status != check.want {
				t.Fatalf("ordinary task=%+v %v", task, err)
			}
		})
	}
}

func TestWorkflowGuardsReaperExcludesOwnedBeforeLimit(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	ownedID := run.Nodes[0].Attempts[0].TaskID
	ordinaryID := workflowGuardOrdinaryTask(t, repo, input.Scope)
	if err := repo.db.Table("tenant_agent_tasks").Where("id = ?", ownedID).Update("started_at", time.Now().UTC().Add(-3*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Table("tenant_agent_tasks").Where("id = ?", ordinaryID).Update("started_at", time.Now().UTC().Add(-2*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	tasks, err := repo.ListStaleRunningAgentTasks(ctx, time.Now().UTC().Add(-time.Hour), 1)
	if err != nil || len(tasks) != 1 || tasks[0].ID != ordinaryID {
		t.Fatalf("reaper list=%+v %v", tasks, err)
	}
	if err := repo.FailStaleAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, ownedID, `{}`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owned reaper returned %v", err)
	}
}

func TestWorkflowGuardsDoNotLeakScopedOwnership(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	taskID := run.Nodes[0].Attempts[0].TaskID
	for _, scope := range []workflow.Scope{{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID + 1}, {TenantID: run.Scope.TenantID + 1, UserID: run.Scope.UserID}, {}} {
		if err := repo.UpdateAgentTask(ctx, taskID, agenttasks.TaskUpdate{TenantID: scope.TenantID, UserID: scope.UserID, Status: agenttasks.StatusCompleted}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign PATCH leaked owner %v: %v", scope, err)
		}
		if err := repo.CancelAgentTask(ctx, scope.TenantID, scope.UserID, taskID, `{}`); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign cancel leaked owner %v: %v", scope, err)
		}
		changed, err := repo.CancelAgentTaskIfRunning(ctx, scope.TenantID, scope.UserID, taskID, `{}`)
		if changed || err != nil {
			t.Fatalf("foreign CAS cancel %v %v", changed, err)
		}
	}
}

func TestWorkflowGuardsActivationAndReopen(t *testing.T) {
	repo, path, input := workflowTestRepo(t)
	ctx := context.Background()
	if repo.workflowTaskGuards.Load() {
		t.Fatal("guard should default off for legacy repositories")
	}
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	if !repo.workflowTaskGuards.Load() {
		t.Fatal("NewWorkflowStore did not activate guard")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	_ = NewWorkflowStore(reopened)
	if err := reopened.FinishAgentTask(ctx, run.Nodes[0].Attempts[0].TaskID, "", `{}`); !errors.Is(err, workflow.ErrOwnedTask) {
		t.Fatalf("reopened owner bypass %v", err)
	}
	ordinaryID := workflowGuardOrdinaryTask(t, reopened, input.Scope)
	if err := reopened.FinishAgentTask(ctx, ordinaryID, "", `{}`); err != nil {
		t.Fatalf("reopened ordinary finish %v", err)
	}
}

func TestWorkflowGuardsConcurrentLegacyWritesCannotRaceRunCommit(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	taskID := run.Nodes[0].Attempts[0].TaskID
	const workers = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs <- repo.FinishAgentTask(ctx, taskID, agenttasks.StatusFailed, `{"forged":true}`)
			} else {
				_, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, TaskID: taskID, EventType: agenttasks.EventFailed, PayloadJSON: `{"forged":true}`})
				errs <- err
			}
		}(i)
	}
	proposed, _ := cloneWorkflow(run)
	proposed.Status = workflow.Completed
	proposed.Nodes[0].Status = workflow.Completed
	proposed.Nodes[0].Attempts[0].Status = workflow.Completed
	_, err := NewWorkflowStore(repo).Save(ctx, run.Revision, proposed, []workflow.Event{{Type: workflow.EventCompleted}})
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, workflow.ErrOwnedTask) {
			t.Fatalf("racing legacy write %v", err)
		}
	}
	task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
	if err != nil || task.Status != agenttasks.StatusCompleted {
		t.Fatalf("Run CAS lost to legacy %+v %v", task, err)
	}
	if n := workflowCount(t, repo, "tenant_agent_task_events"); n != 2 {
		t.Fatalf("forged task event committed: %d", n)
	}
}

func TestWorkflowOptionalByKeyPreservesScopeAndMetadata(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestCreate(t, repo, input)
	lookup, ok := NewWorkflowStore(repo).(interface {
		ByKey(context.Context, workflow.Scope, string) (workflow.Run, error)
	})
	if !ok {
		t.Fatal("optional ByKey missing")
	}
	got, err := lookup.ByKey(ctx, run.Scope, run.KeyHash)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("ByKey=%+v %v", got, err)
	}
	for _, scope := range []workflow.Scope{{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID + 1}, {TenantID: run.Scope.TenantID + 1, UserID: run.Scope.UserID}, {}} {
		if _, err := lookup.ByKey(ctx, scope, run.KeyHash); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("foreign ByKey scope=%v error=%v", scope, err)
		}
	}
	if _, err := lookup.ByKey(ctx, run.Scope, "missing"); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("unknown ByKey %v", err)
	}
}

func TestWorkflowGuardsFailClosedWithoutOwnershipTable(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	_ = NewWorkflowStore(repo)
	taskID := workflowGuardOrdinaryTask(t, repo, input.Scope)
	before, err := repo.GetAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an incorrectly migrated/restored database. Enabling the runtime
	// must never silently downgrade ownership checks when the table is missing.
	if err := repo.db.Exec("DROP TABLE agent_team_workflow_task_bindings").Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishAgentTask(ctx, taskID, "", `{}`); err == nil {
		t.Fatal("missing bindings allowed a legacy finish")
	}
	if _, err := repo.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, TaskID: taskID, EventType: agenttasks.EventCompleted}); err == nil {
		t.Fatal("missing bindings allowed an event")
	}
	if _, err := repo.CancelAgentTaskForSessionControl(ctx, SessionControlStopInput{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, TaskID: taskID, ResultJSON: `{}`, EventPayloadJSON: `{}`}); err == nil {
		t.Fatal("missing bindings allowed SessionControlStop")
	}
	after, err := repo.GetAgentTask(ctx, input.Scope.TenantID, input.Scope.UserID, taskID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("fail-closed path changed ordinary task: %v", err)
	}
	if n := workflowCount(t, repo, "tenant_agent_task_events"); n != 0 {
		t.Fatalf("fail-closed path left an event: %d", n)
	}
}
