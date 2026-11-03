package mysql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
)

func TestWorkflowCapacityConcurrentScopeAdmissionAndFullQueueReplay(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	// Terminal V2 history and legacy rows do not occupy a recoverable slot.
	terminal := input
	terminal.ID = uuid.NewString()
	terminal.KeyHash = fmt.Sprintf("%064d", 999)
	terminal.Status = workflow.Completed
	_ = workflowTestCreate(t, repo, terminal)
	statuses := workflowRecoverableStatuses()
	var first workflow.Run
	for i := 0; i < workflow.MaxQueuedRuns-1; i++ {
		run := input
		run.ID = uuid.NewString()
		run.KeyHash = fmt.Sprintf("%064d", i)
		run.Status = statuses[i%len(statuses)]
		created := workflowTestCreate(t, repo, run)
		if i == 0 {
			first = created
		}
	}
	const writers = 8
	type outcome struct {
		created bool
		err     error
	}
	outcomes := make(chan outcome, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			candidate := input
			candidate.ID = uuid.NewString()
			candidate.KeyHash = fmt.Sprintf("%064d", 100+i)
			_, created, err := store.Create(ctx, candidate, []workflow.Event{{Type: workflow.EventQueued}})
			outcomes <- outcome{created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)
	winners := 0
	rejected := 0
	for result := range outcomes {
		switch {
		case result.err == nil && result.created:
			winners++
		case errors.Is(result.err, workflow.ErrCapacity) && !result.created:
			rejected++
		default:
			t.Fatalf("unexpected admission created=%v error=%v", result.created, result.err)
		}
	}
	if winners != 1 || rejected != writers-1 {
		t.Fatalf("TOCTOU capacity: winners=%d rejected=%d", winners, rejected)
	}
	var count int64
	if err := workflowQuery(repo.db, input.Scope).Where("status IN ?", workflowRecoverableStatuses()).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != workflow.MaxQueuedRuns {
		t.Fatalf("recoverable run count=%d", count)
	}
	if n := workflowCount(t, repo, "agent_team_runs"); n != workflow.MaxQueuedRuns+1 {
		t.Fatalf("rejected Create left extra Run rows: %d", n)
	}
	if n := workflowCount(t, repo, "agent_team_run_events"); n != workflow.MaxQueuedRuns+1 {
		t.Fatalf("rejected Create left extra events: %d", n)
	}
	// Replays are authoritative and must precede a full-queue capacity rejection.
	replay := input
	replay.ID = uuid.NewString()
	replay.KeyHash = first.KeyHash
	existing, created, err := store.Create(ctx, replay, []workflow.Event{{Type: workflow.EventQueued}})
	if err != nil || created || existing.ID != first.ID || existing.LastSequence != first.LastSequence {
		t.Fatalf("full queue replay=%+v created=%v error=%v", existing, created, err)
	}
	replay.Fingerprint = "different"
	if _, created, err := store.Create(ctx, replay, nil); created || !errors.Is(err, workflow.ErrIdempotencyConflict) {
		t.Fatalf("full queue fingerprint conflict created=%v err=%v", created, err)
	}
	// Exhaustion is per authenticated scope, not a global admission limit.
	otherScope, teamID := workflowTestOwner(t, repo, "capacity-other")
	_ = workflowTestCreate(t, repo, workflowTestRun(otherScope, teamID))
}

func TestWorkflowCapacityConcurrentSameKeyAtLastSlot(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	for i := 0; i < workflow.MaxQueuedRuns-1; i++ {
		run := input
		run.ID = uuid.NewString()
		run.KeyHash = fmt.Sprintf("%064d", i)
		_ = workflowTestCreate(t, repo, run)
	}
	const writers = 6
	type outcome struct {
		run     workflow.Run
		created bool
		err     error
	}
	outcomes := make(chan outcome, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			candidate := input
			candidate.ID = uuid.NewString()
			candidate.KeyHash = "shared-last-slot"
			run, created, err := store.Create(ctx, candidate, nil)
			outcomes <- outcome{run, created, err}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)
	winners := 0
	winnerID := ""
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("same-key admission/replay error=%v", result.err)
		}
		if result.created {
			winners++
		}
		if winnerID == "" {
			winnerID = result.run.ID
		}
		if result.run.ID != winnerID {
			t.Fatal("same key created multiple runs at capacity")
		}
	}
	if winners != 1 {
		t.Fatalf("same-key last slot winners=%d", winners)
	}
}
