package mysql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func workflowTestRepo(t *testing.T) (*GormRepository, string, workflow.Run) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.sqlite")
	repo, err := OpenSQLiteGormRepository(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	scope, teamID := workflowTestOwner(t, repo, "one")
	return repo, path, workflowTestRun(scope, teamID)
}
func workflowTestOwner(t *testing.T, repo *GormRepository, key string) (workflow.Scope, uint64) {
	t.Helper()
	ctx := context.Background()
	tenant, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: key, Name: key})
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.EnsureUser(ctx, tenant, key)
	if err != nil {
		t.Fatal(err)
	}
	team, err := repo.CreateAgentTeam(ctx, AgentTeamInput{TenantID: tenant, OwnerUserID: user, TeamKey: key, TeamVersion: 1, OwnerKey: key, CreatedByUserID: user, UpdatedByUserID: user, SchemaVersion: workflow.SchemaVersion, PolicyJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	return workflow.Scope{TenantID: tenant, UserID: user}, team.ID
}
func workflowTestRun(scope workflow.Scope, teamID uint64) workflow.Run {
	return workflow.Run{ID: uuid.NewString(), Scope: scope, TeamID: teamID, TeamKey: "test", TeamVersion: 1, Input: "objective", Status: workflow.Queued, KeyHash: strings.Repeat("a", 64), Fingerprint: strings.Repeat("b", 64), Plan: workflow.Plan{Hash: strings.Repeat("c", 64), Definition: workflow.Definition{SchemaVersion: workflow.SchemaVersion, Nodes: []workflow.Node{{ID: "n1", AgentRef: "agent1", Instruction: "do work"}}, Limits: workflow.Limits{MaxParallel: 2, MaxTotalTokens: 1000}}, Agents: []workflow.Agent{{Ref: "agent1", Name: "Agent", Model: "test-model"}}}}
}
func workflowTestCreate(t *testing.T, repo *GormRepository, run workflow.Run) workflow.Run {
	t.Helper()
	got, created, err := NewWorkflowStore(repo).Create(context.Background(), run, []workflow.Event{{Type: workflow.EventQueued, Status: workflow.Queued}})
	if err != nil || !created {
		t.Fatalf("create: created=%v err=%v", created, err)
	}
	return got
}
func workflowTestStart(t *testing.T, repo *GormRepository, run workflow.Run) workflow.Run {
	t.Helper()
	run.Status = workflow.Running
	run.Epoch++
	run.Nodes[0].Status = workflow.Running
	run.Nodes[0].Attempts = append(run.Nodes[0].Attempts, workflow.Attempt{Number: len(run.Nodes[0].Attempts) + 1, Epoch: run.Epoch, Status: workflow.Running})
	got, err := NewWorkflowStore(repo).Save(context.Background(), run.Revision, run, []workflow.Event{{Type: workflow.EventAgentStarted, NodeID: "n1", Status: workflow.Running}})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func workflowCount(t *testing.T, repo *GormRepository, table string) int64 {
	t.Helper()
	var count int64
	if err := repo.db.Table(table).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestWorkflowReopenCheckpointAndTaskProjection(t *testing.T) {
	repo, path, input := workflowTestRepo(t)
	ctx := context.Background()
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	lease := time.Now().UTC().Add(time.Minute)
	run.LeaseOwner = "owner-instance"
	run.LeaseUntil = &lease
	run, err := NewWorkflowStore(repo).Save(ctx, run.Revision, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Revision != 3 || run.LastSequence != 2 || run.Nodes[0].Attempts[0].TaskID == 0 {
		t.Fatalf("checkpoint: %+v", run)
	}
	taskID := run.Nodes[0].Attempts[0].TaskID
	task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
	if err != nil || task.Status != agenttasks.StatusRunning || task.ParentSessionID != 0 {
		t.Fatalf("prepared task=%+v err=%v", task, err)
	}
	if task.StartedAt.IsZero() {
		t.Fatal("task started_at missing")
	}
	owner, owned, err := repo.WorkflowTaskOwner(ctx, taskID)
	if err != nil || !owned || owner != run.ID {
		t.Fatalf("ownership %s %v %v", owner, owned, err)
	}
	if _, owned, err := repo.WorkflowTaskOwner(ctx, taskID+1); err != nil || owned {
		t.Fatalf("unowned %v %v", owned, err)
	}
	var raw string
	if err := repo.db.Table("agent_team_runs").Select("workflow_state_json").Where("id = ?", run.ID).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, run.KeyHash) || strings.Contains(raw, run.Fingerprint) {
		t.Fatal("private idempotency metadata leaked to JSON")
	}
	var nullSources int64
	repo.db.Table("agent_team_runs").Where("id = ? AND inbox_event_id IS NULL AND source_account_id IS NULL AND source_kind = ?", run.ID, workflowSourceDesktop).Count(&nullSources)
	if nullSources != 1 {
		t.Fatal("desktop synthesized channel identities")
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := NewWorkflowStore(reopened).Get(ctx, run.Scope, run.ID)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("reopen mismatch: got=%+v err=%v", got, err)
	}
	events, err := NewWorkflowStore(reopened).Events(ctx, run.Scope, run.ID, 1, 50)
	if err != nil || len(events) != 1 || events[0].Sequence != 2 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	for _, table := range []string{"agent_team_workflow_nodes", "agent_team_workflow_attempts", "agent_team_workflow_task_bindings"} {
		if n := workflowCount(t, reopened, table); n != 1 {
			t.Fatalf("%s count=%d", table, n)
		}
	}
}

func TestWorkflowIdempotencyAndScopeIsolation(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	first := workflowTestCreate(t, repo, input)
	input.ID = uuid.NewString()
	repeat, created, err := store.Create(ctx, input, []workflow.Event{{Type: workflow.EventStarted}})
	if err != nil || created || repeat.ID != first.ID || repeat.LastSequence != 1 {
		t.Fatalf("repeat=%+v created=%v err=%v", repeat, created, err)
	}
	input.Fingerprint = strings.Repeat("d", 64)
	if _, _, err := store.Create(ctx, input, nil); !errors.Is(err, workflow.ErrIdempotencyConflict) {
		t.Fatalf("fingerprint collision: %v", err)
	}
	otherScope, team := workflowTestOwner(t, repo, "two")
	other := workflowTestRun(otherScope, team)
	_ = workflowTestCreate(t, repo, other) // Identical scoped key in a different tenant.
	otherUser, err := repo.EnsureUser(ctx, first.Scope.TenantID, "second-user")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := repo.CreateAgentTeam(ctx, AgentTeamInput{TenantID: first.Scope.TenantID, TeamKey: "shared", TeamVersion: 1, OwnerKey: "tenant", CreatedByUserID: first.Scope.UserID, UpdatedByUserID: first.Scope.UserID, PolicyJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	sameTenant := workflowTestRun(workflow.Scope{TenantID: first.Scope.TenantID, UserID: otherUser}, shared.ID)
	_ = workflowTestCreate(t, repo, sameTenant) // Same key and tenant, distinct owner.
	for _, scope := range []workflow.Scope{otherScope, sameTenant.Scope, {TenantID: first.Scope.TenantID, UserID: 99999}, {}} {
		if _, err := store.Get(ctx, scope, first.ID); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("Get leaked %v: %v", scope, err)
		}
		if _, err := store.Events(ctx, scope, first.ID, 999, 1); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("Events leaked: %v", err)
		}
		forged := first
		forged.Scope = scope
		if _, err := store.Save(ctx, first.Revision, forged, nil); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("Save leaked: %v", err)
		}
	}
	listed, err := store.List(ctx, first.Scope, 100)
	if err != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("List leak: %+v %v", listed, err)
	}
	for _, forged := range []workflow.Run{workflowTestRun(otherScope, first.TeamID), workflowTestRun(sameTenant.Scope, first.TeamID), workflowTestRun(workflow.Scope{TenantID: first.Scope.TenantID, UserID: otherScope.UserID}, shared.ID)} {
		forged.KeyHash = uuid.NewString()
		if _, _, err := store.Create(ctx, forged, nil); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("Create accepted foreign owner/team: %v", err)
		}
	}
}

func TestWorkflowCASAndCallerIsolation(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	run := workflowTestCreate(t, repo, input)
	run.Nodes[0].Status = workflow.Running
	run.Nodes[0].Attempts = []workflow.Attempt{{Number: 1, Epoch: 1, Status: workflow.Running}}
	saved, err := store.Save(ctx, run.Revision, run, []workflow.Event{{Type: workflow.EventAgentStarted}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Nodes[0].Attempts[0].TaskID != 0 {
		t.Fatal("Save mutated caller slices")
	}
	if _, err := store.Save(ctx, run.Revision, run, nil); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	got, err := store.Get(ctx, run.Scope, run.ID)
	if err != nil || got.Revision != saved.Revision || got.LastSequence != 2 {
		t.Fatalf("CAS checkpoint %v %v", got, err)
	}
	if count := workflowCount(t, repo, "tenant_agent_tasks"); count != 1 {
		t.Fatalf("CAS created %d tasks", count)
	}
}

func TestWorkflowAttemptTerminalTaskAtomicity(t *testing.T) {
	cases := []struct {
		status      workflow.Status
		task, event string
	}{{workflow.Completed, agenttasks.StatusCompleted, agenttasks.EventCompleted}, {workflow.Failed, agenttasks.StatusFailed, agenttasks.EventFailed}, {workflow.Cancelled, agenttasks.StatusCancelled, agenttasks.EventCancelled}, {workflow.TimedOut, agenttasks.StatusTimeout, agenttasks.EventTimeout}, {workflow.Interrupted, agenttasks.StatusFailed, agenttasks.EventFailed}}
	for _, tt := range cases {
		t.Run(string(tt.status), func(t *testing.T) {
			repo, _, input := workflowTestRepo(t)
			ctx := context.Background()
			store := NewWorkflowStore(repo)
			run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
			taskID := run.Nodes[0].Attempts[0].TaskID
			run.Status = tt.status
			run.Nodes[0].Status = tt.status
			run.Nodes[0].Attempts[0].Status = tt.status
			run.Nodes[0].Output = "result"
			run.Nodes[0].Attempts[0].Usage = workflow.Usage{InputTokens: 2, OutputTokens: 3}
			run.Artifacts = []workflow.Artifact{{ID: uuid.NewString(), NodeID: "n1", AttemptNo: 1, Kind: "text", Content: "result", SHA256: strings.Repeat("a", 64), CreatedAt: time.Now().UTC()}}
			saved, err := store.Save(ctx, run.Revision, run, []workflow.Event{{Type: workflow.EventAgentCompleted, NodeID: "n1", Status: tt.status}})
			if err != nil {
				t.Fatal(err)
			}
			task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, taskID)
			if err != nil || task.Status != tt.task || task.FinishedAt.IsZero() || !strings.Contains(task.ResultJSON, "result") {
				t.Fatalf("task=%+v err=%v", task, err)
			}
			if saved.Nodes[0].Attempts[0].FinishedAt == nil {
				t.Fatal("attempt missing finished_at")
			}
			events, err := repo.ListAgentTaskEvents(ctx, run.Scope.TenantID, run.Scope.UserID, taskID, 20)
			if err != nil || len(events) != 2 {
				t.Fatalf("task events=%+v %v", events, err)
			}
			found := false
			for _, event := range events {
				if event.EventType == tt.event {
					found = true
				}
			}
			if !found {
				t.Fatal("terminal task event missing")
			}
			if n := workflowCount(t, repo, "agent_team_workflow_artifacts"); n != 1 {
				t.Fatalf("artifacts=%d", n)
			}
			// Heartbeat/idempotent unchanged terminal attempt must not duplicate events.
			if _, err := store.Save(ctx, saved.Revision, saved, nil); err != nil {
				t.Fatal(err)
			}
			if n := workflowCount(t, repo, "tenant_agent_task_events"); n != 2 {
				t.Fatalf("duplicate terminal events=%d", n)
			}
		})
	}
}

func TestWorkflowTransactionRollbackAtEveryWriteBoundary(t *testing.T) {
	for _, stage := range []string{"create", "start", "terminal"} {
		t.Run(stage, func(t *testing.T) {
			repo, path, input := workflowTestRepo(t)
			ctx := context.Background()
			store := NewWorkflowStore(repo)
			var prior workflow.Run
			if stage != "create" {
				prior = workflowTestCreate(t, repo, input)
			}
			if stage == "terminal" {
				prior = workflowTestStart(t, repo, prior)
			}
			// Fail the very last event write, after tasks, bindings, node/attempt and
			// artifact projections have already been written in the transaction.
			if err := repo.db.Exec(`CREATE TRIGGER reject_workflow_event BEFORE INSERT ON agent_team_run_events BEGIN SELECT RAISE(ABORT, 'injected workflow failure'); END`).Error; err != nil {
				t.Fatal(err)
			}
			switch stage {
			case "create":
				if _, _, err := store.Create(ctx, input, []workflow.Event{{Type: workflow.EventQueued}}); err == nil {
					t.Fatal("expected injected create failure")
				}
			case "start":
				proposed, _ := cloneWorkflow(prior)
				proposed.Nodes[0].Attempts = []workflow.Attempt{{Number: 1, Status: workflow.Running}}
				if _, err := store.Save(ctx, prior.Revision, proposed, []workflow.Event{{Type: workflow.EventAgentStarted}}); err == nil {
					t.Fatal("expected injected start failure")
				}
				if proposed.Nodes[0].Attempts[0].TaskID != 0 {
					t.Fatal("failed save mutated caller")
				}
			case "terminal":
				proposed, _ := cloneWorkflow(prior)
				proposed.Nodes[0].Attempts[0].Status = workflow.Completed
				proposed.Nodes[0].Output = "done"
				proposed.Nodes[0].Status = workflow.Completed
				proposed.Artifacts = []workflow.Artifact{{ID: uuid.NewString(), NodeID: "n1", AttemptNo: 1, Kind: "text", Content: "done", CreatedAt: time.Now().UTC()}}
				if _, err := store.Save(ctx, prior.Revision, proposed, []workflow.Event{{Type: workflow.EventCompleted}}); err == nil {
					t.Fatal("expected injected terminal failure")
				}
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenSQLiteGormRepository(ctx, path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			got, err := NewWorkflowStore(reopened).Get(ctx, input.Scope, input.ID)
			if stage == "create" {
				if !errors.Is(err, workflow.ErrNotFound) {
					t.Fatalf("rolled-back run exists: %v", err)
				}
			} else if err != nil || !reflect.DeepEqual(got, prior) {
				t.Fatalf("rollback checkpoint mismatch: %+v %v", got, err)
			}
			tasks, taskEvents, attempts := int64(0), int64(0), int64(0)
			if stage == "terminal" {
				tasks = 1
				taskEvents = 1
				attempts = 1
				task, err := reopened.GetAgentTask(ctx, prior.Scope.TenantID, prior.Scope.UserID, prior.Nodes[0].Attempts[0].TaskID)
				if err != nil || task.Status != agenttasks.StatusRunning {
					t.Fatalf("terminal rollback task=%+v %v", task, err)
				}
			}
			expected := map[string]int64{"tenant_agent_tasks": tasks, "tenant_agent_task_events": taskEvents, "agent_team_workflow_task_bindings": tasks, "agent_team_workflow_attempts": attempts, "agent_team_workflow_artifacts": 0}
			for table, want := range expected {
				if n := workflowCount(t, reopened, table); n != want {
					t.Fatalf("rollback %s=%d want %d", table, n, want)
				}
			}
		})
	}
}

func TestWorkflowTaskEventFailureRollsBackTerminalCheckpoint(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	prior := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	if err := repo.db.Exec(`CREATE TRIGGER reject_task_event BEFORE INSERT ON tenant_agent_task_events WHEN NEW.event_type = 'completed' BEGIN SELECT RAISE(ABORT, 'injected task-event failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	proposed, _ := cloneWorkflow(prior)
	proposed.Nodes[0].Attempts[0].Status = workflow.Completed
	if _, err := store.Save(ctx, prior.Revision, proposed, nil); err == nil {
		t.Fatal("expected task-event failure")
	}
	task, err := repo.GetAgentTask(ctx, prior.Scope.TenantID, prior.Scope.UserID, prior.Nodes[0].Attempts[0].TaskID)
	if err != nil || task.Status != agenttasks.StatusRunning {
		t.Fatalf("task not rolled back: %+v %v", task, err)
	}
	got, err := store.Get(ctx, prior.Scope, prior.ID)
	if err != nil || !reflect.DeepEqual(got, prior) {
		t.Fatalf("checkpoint changed: %+v %v", got, err)
	}
}

func TestWorkflowEventsCursorAndLimit(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	events := make([]workflow.Event, workflow.EventPageLimit+5)
	for i := range events {
		events[i] = workflow.Event{Sequence: 999, Type: workflow.EventDelta, Text: fmt.Sprint(i)}
	}
	run, created, err := store.Create(ctx, input, events)
	if err != nil || !created {
		t.Fatalf("create events %v %v", created, err)
	}
	if run.LastSequence != uint64(len(events)) {
		t.Fatalf("tail=%d", run.LastSequence)
	}
	for _, limit := range []int{0, -1, 1000, workflow.EventPageLimit} {
		page, err := store.Events(ctx, run.Scope, run.ID, 0, limit)
		if err != nil || len(page) != workflow.EventPageLimit {
			t.Fatalf("limit %d length=%d err=%v", limit, len(page), err)
		}
		for i, event := range page {
			if event.Sequence != uint64(i+1) || event.Text != fmt.Sprint(i) {
				t.Fatalf("out of order event %+v", event)
			}
		}
	}
	tail, err := store.Events(ctx, run.Scope, run.ID, workflow.EventPageLimit, 2)
	if err != nil || len(tail) != 2 || tail[0].Sequence != workflow.EventPageLimit+1 {
		t.Fatalf("tail=%+v %v", tail, err)
	}
	empty, err := store.Events(ctx, run.Scope, run.ID, 9999, 1)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%+v %v", empty, err)
	}
	if err := repo.db.Create(&gormAgentTeamRunEvent{TenantID: run.Scope.TenantID, TeamRunID: run.ID, SequenceNo: 1, EventType: string(workflow.EventDelta)}).Error; err == nil {
		t.Fatal("sequence unique index missing")
	}
	if events[0].Sequence != 999 {
		t.Fatal("Create mutated caller events")
	}
}

func TestWorkflowActiveOnlyV2AndLeasePersistence(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	statuses := []workflow.Status{workflow.Queued, workflow.Running, workflow.CancelRequested, workflow.Interrupted, workflow.Completed, workflow.Failed, workflow.TimedOut, workflow.Cancelled}
	for i, status := range statuses {
		run := input
		run.ID = uuid.NewString()
		run.KeyHash = fmt.Sprintf("%064d", i)
		run.Status = status
		until := time.Now().UTC().Add(time.Hour)
		run.LeaseOwner = fmt.Sprintf("owner-%d", i)
		run.LeaseUntil = &until
		_ = workflowTestCreate(t, repo, run)
	}
	// Legacy queued run must not be swept up by the V2 recovery scanner.
	if err := repo.db.Create(&gormAgentTeamRun{ID: uuid.NewString(), TenantID: input.Scope.TenantID, TeamID: input.TeamID, Status: string(workflow.Queued), InboxEventID: 10, SourceAccountID: 20, CoordinatorMemberKey: "legacy"}).Error; err != nil {
		t.Fatal(err)
	}
	active, err := store.Active(ctx)
	if err != nil || len(active) != 4 {
		t.Fatalf("active=%+v %v", active, err)
	}
	for _, run := range active {
		if run.LeaseOwner == "" || run.LeaseUntil == nil {
			t.Fatal("lease lost in checkpoint")
		}
		if workflow.Terminal(run.Status) {
			t.Fatalf("terminal in Active: %s", run.Status)
		}
	}
	listed, err := store.List(ctx, input.Scope, 2)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list limit: %d %v", len(listed), err)
	}
}

func TestWorkflowConcurrentCreateAndCAS(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	const writers = 6
	type creation struct {
		run     workflow.Run
		created bool
		err     error
	}
	results := make(chan creation, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate := input
			candidate.ID = uuid.NewString()
			run, created, err := store.Create(ctx, candidate, []workflow.Event{{Type: workflow.EventQueued}})
			results <- creation{run, created, err}
		}()
	}
	wg.Wait()
	close(results)
	var first workflow.Run
	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if first.ID == "" {
			first = result.run
		}
		if first.ID != result.run.ID {
			t.Fatal("concurrent create produced multiple run IDs")
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 || workflowCount(t, repo, "agent_team_runs") != 1 {
		t.Fatalf("created count=%d", createdCount)
	}
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidate, _ := cloneWorkflow(first)
			candidate.Nodes[0].Attempts = []workflow.Attempt{{Number: 1, Status: workflow.Running}}
			_, err := store.Save(ctx, first.Revision, candidate, []workflow.Event{{Type: workflow.EventAgentStarted}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("unexpected concurrent CAS: %v", err)
		}
	}
	if wins != 1 || workflowCount(t, repo, "tenant_agent_tasks") != 1 || workflowCount(t, repo, "agent_team_run_events") != 2 {
		t.Fatalf("CAS winners=%d", wins)
	}
}

func TestWorkflowRejectsTaskRebindingAndProjectionRemoval(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	for _, mutate := range []func(*workflow.Run){
		func(r *workflow.Run) { r.Nodes[0].Attempts[0].TaskID++ },
		func(r *workflow.Run) { r.Nodes[0].Attempts[0].Epoch++ },
		func(r *workflow.Run) { r.Nodes[0].Attempts = nil },
		func(r *workflow.Run) { r.Nodes = nil },
		func(r *workflow.Run) { r.Plan.Hash = "changed" },
		func(r *workflow.Run) {
			r.Nodes[0].Attempts = append(r.Nodes[0].Attempts, workflow.Attempt{Number: 2, TaskID: r.Nodes[0].Attempts[0].TaskID, Status: workflow.Running})
		},
	} {
		candidate, _ := cloneWorkflow(run)
		mutate(&candidate)
		if _, err := store.Save(ctx, run.Revision, candidate, nil); err == nil {
			t.Fatal("accepted mutation of stable projection identity")
		}
	}
	binding := gormWorkflowTaskBinding{TaskID: run.Nodes[0].Attempts[0].TaskID, TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, RunID: uuid.NewString(), NodeID: "other", AttemptNo: 1}
	if err := repo.db.Create(&binding).Error; err == nil {
		t.Fatal("task ownership unique constraint missing")
	}
	got, err := store.Get(ctx, run.Scope, run.ID)
	if err != nil || !reflect.DeepEqual(got, run) {
		t.Fatalf("invalid snapshot changed state %v", err)
	}
}

func TestWorkflowSQLiteUpgradePreservesLegacyRuns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// The pre-V2 desktop schema omitted the run-events table altogether.
	if err := legacy.AutoMigrate(&gormAgentTeamRun{}); err != nil {
		t.Fatal(err)
	}
	original := gormAgentTeamRun{ID: uuid.NewString(), TenantID: 1, TeamID: 1, InboxEventID: 7, SourceAccountID: 8, CoordinatorMemberKey: "legacy-coordinator", Status: "running", TeamEffectiveHash: strings.Repeat("e", 64)}
	if err := legacy.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	got, err := repo.GetAgentTeamRun(ctx, 1, original.ID)
	if err != nil || got.InboxEventID != 7 || got.SourceAccountID != 8 || got.Status != "running" {
		t.Fatalf("legacy changed: %+v %v", got, err)
	}
	var row gormWorkflowRun
	if err := repo.db.Where("id = ?", original.ID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.WorkflowVersion != 1 || row.SourceKind != "channel" || row.WorkflowStateJSON != nil || row.OwnerUserID != nil {
		t.Fatalf("V1 defaults: %+v", row)
	}
	event, err := repo.AppendAgentTeamRunEvent(ctx, AgentTeamRunEventInput{TenantID: 1, TeamRunID: original.ID, SequenceNo: 1, EventType: "started"})
	if err != nil || event.ID == 0 {
		t.Fatalf("new run-events table unavailable: %+v %v", event, err)
	}
	if _, err := NewWorkflowStore(repo).Get(ctx, workflow.Scope{TenantID: 1, UserID: 1}, original.ID); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatalf("V1 exposed as V2: %v", err)
	}
	scope, team := workflowTestOwner(t, repo, "upgraded")
	_ = workflowTestStart(t, repo, workflowTestCreate(t, repo, workflowTestRun(scope, team)))
	// Legacy repository may still create channel runs after migration.
	if _, err := repo.CreateAgentTeamRun(ctx, AgentTeamRunInput{ID: uuid.NewString(), TenantID: scope.TenantID, TeamID: team, InboxEventID: 70, SourceAccountID: 80, CoordinatorMemberKey: "legacy-coordinator", TeamEffectiveHash: strings.Repeat("f", 64)}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRetryGetsDistinctTaskAndPreservesOwnership(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	task1 := run.Nodes[0].Attempts[0].TaskID
	run.Nodes[0].Attempts[0].Status = workflow.Interrupted
	run.Nodes[0].Status = workflow.Interrupted
	run.Status = workflow.Interrupted
	var err error
	run, err = store.Save(ctx, run.Revision, run, []workflow.Event{{Type: workflow.EventInterrupted}})
	if err != nil {
		t.Fatal(err)
	}
	run = workflowTestStart(t, repo, run)
	task2 := run.Nodes[0].Attempts[1].TaskID
	if task1 == task2 || task2 == 0 {
		t.Fatalf("retry reused task %d %d", task1, task2)
	}
	for _, taskID := range []uint64{task1, task2} {
		owner, owned, err := repo.WorkflowTaskOwner(ctx, taskID)
		if err != nil || !owned || owner != run.ID {
			t.Fatalf("retry ownership %d: %v", taskID, err)
		}
	}
	task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, task1)
	if err != nil || task.Status != agenttasks.StatusFailed {
		t.Fatalf("old interrupted task=%+v %v", task, err)
	}
	if n := workflowCount(t, repo, "agent_team_workflow_attempts"); n != 2 {
		t.Fatalf("attempts=%d", n)
	}
}

func TestWorkflowIDCollisionCannotOverwriteOtherOwner(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	prior := workflowTestCreate(t, repo, input)
	scope, team := workflowTestOwner(t, repo, "collision")
	forged := workflowTestRun(scope, team)
	forged.ID = prior.ID
	if _, created, err := store.Create(ctx, forged, nil); created || !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("ID collision: %v %v", created, err)
	}
	got, err := store.Get(ctx, prior.Scope, prior.ID)
	if err != nil || !reflect.DeepEqual(got, prior) {
		t.Fatalf("ID collision overwrote checkpoint: %v", err)
	}
}

func TestWorkflowEngineSQLiteTwoInstancesOneLease(t *testing.T) {
	repo, path, input := workflowTestRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input.Plan.Agents[0].ProfileKey = "agent"
	input.Plan.Agents[0].ProfileVersion = 1
	input.Plan.Definition.Limits.MaxTotalTokens = 10000
	started := make(chan workflow.Input, 2)
	release := make(chan struct{})
	executor := workflow.ExecutorFunc(func(ctx context.Context, in workflow.Input, sink workflow.Sink) (workflow.Result, error) {
		started <- in
		select {
		case <-release:
		case <-ctx.Done():
			return workflow.Result{}, ctx.Err()
		}
		sink(workflow.Event{Type: workflow.EventDelta, NodeID: in.NodeID, Text: "working"})
		return workflow.Result{Status: workflow.Completed, Output: "persisted engine result", Usage: workflow.Usage{InputTokens: 2, OutputTokens: 3}}, nil
	})
	first := workflow.NewEngine(NewWorkflowStore(repo), executor, workflow.EngineOptions{PollInterval: 5 * time.Millisecond})
	run, created, err := first.Create(ctx, input)
	if err != nil || !created {
		t.Fatalf("engine create %v %v", created, err)
	}
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = first.Close(closeCtx)
	}()
	var call workflow.Input
	select {
	case call = <-started:
	case <-ctx.Done():
		t.Fatal("engine did not start")
	}
	if call.TaskID == 0 {
		t.Fatal("executor received an unprepared engine task")
	}
	secondRepo, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer secondRepo.Close()
	second := workflow.NewEngine(NewWorkflowStore(secondRepo), executor, workflow.EngineOptions{PollInterval: 5 * time.Millisecond})
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = second.Close(closeCtx)
	}()
	// Give the second scanner several passes over a live persisted lease.
	select {
	case duplicate := <-started:
		t.Fatalf("second instance stole live lease: %+v", duplicate)
	case <-time.After(80 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	leased, err := NewWorkflowStore(secondRepo).Get(ctx, run.Scope, run.ID)
	if err != nil || leased.LeaseOwner == "" || leased.LeaseUntil == nil || leased.Status != workflow.Running {
		t.Fatalf("live lease=%+v %v", leased, err)
	}
	close(release)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		persisted, err := NewWorkflowStore(secondRepo).Get(ctx, run.Scope, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Status == workflow.Completed {
			if len(persisted.Artifacts) != 1 || persisted.Artifacts[0].Content != "persisted engine result" {
				t.Fatalf("engine artifacts %+v", persisted.Artifacts)
			}
			task, err := repo.GetAgentTask(ctx, run.Scope.TenantID, run.Scope.UserID, call.TaskID)
			if err != nil || task.Status != agenttasks.StatusCompleted {
				t.Fatalf("engine task terminal=%+v %v", task, err)
			}
			if n := workflowCount(t, repo, "tenant_agent_tasks"); n != 1 {
				t.Fatalf("multiple engine tasks=%d", n)
			}
			break
		}
		if workflow.Terminal(persisted.Status) {
			t.Fatalf("engine failed %+v", persisted.Failure)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("engine did not finish, status=%s failure=%+v", persisted.Status, persisted.Failure)
		}
	}
}
