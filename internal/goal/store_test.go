package goal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalStoreCreateUpdateEvents(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	now := time.Date(2026, 6, 15, 1, 2, 3, 0, time.UTC)
	created, err := store.Create(ctx, CreateInput{
		Objective:   "Ship goal mode",
		SessionID:   "session-1",
		CWD:         t.TempDir(),
		TurnBudget:  3,
		TokenBudget: 1000,
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.ID, "goal_") || created.Status != StatusActive || created.TurnBudget != 3 {
		t.Fatalf("created = %+v", created)
	}
	events, err := store.ListEvents(ctx, created.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != EventGoalStarted || events[0].GoalID != created.ID {
		t.Fatalf("events = %+v", events)
	}
	created.Status = StatusStopped
	created.UpdatedAt = now.Add(time.Minute)
	if err := store.Update(ctx, created); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusStopped || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("got = %+v", got)
	}
	list, err := store.List(ctx, ListFilter{Status: StatusStopped})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
}

func TestDefaultStoreReadsLegacyGoalsAndWritesUpdatesToGolangCC(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NIUMA_CONFIG_DIR", "")
	t.Setenv("NIUMA_CONFIG_DIR", "")

	legacy := NewLocalStore(filepath.Join(home, ".niuma-legacy"))
	created, err := legacy.Create(ctx, CreateInput{Objective: "legacy goal", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	store := DefaultStore()
	got, err := store.Get(ctx, created.ID)
	if err != nil || got.Objective != "legacy goal" {
		t.Fatalf("legacy goal = %+v err=%v", got, err)
	}
	got.Status = StatusStopped
	got.UpdatedAt = time.Now().UTC()
	if err := store.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".niuma", "goals", "goals.jsonl")); err != nil {
		t.Fatalf("canonical goal store was not written: %v", err)
	}
}

func TestLocalStoreListEventsLimitReturnsTail(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	goal, err := store.Create(ctx, CreateInput{Objective: "limit events", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		event, err := NewEvent(EventInput{GoalID: goal.ID, Type: EventTurnFinished, Turn: i, Now: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := store.ListEvents(ctx, goal.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Turn != 2 || events[1].Turn != 3 {
		t.Fatalf("events = %+v", events)
	}
}

func TestLocalStorePlanAndEvidence(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	goal, err := store.Create(ctx, CreateInput{Objective: "structured store", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	plan := GoalPlan{
		GoalID:        goal.ID,
		Version:       1,
		CurrentStepID: "step_1",
		Steps: []GoalStep{{
			ID:     "step_1",
			Title:  "Design schema",
			Status: StepStatusActive,
		}},
		AcceptanceCriteria: []GoalCriterion{{
			ID:          "crit_1",
			Description: "Plan is stored",
			Required:    true,
			Status:      CriterionStatusPending,
		}},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.SavePlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetPlan(ctx, goal.ID)
	if err != nil || !ok {
		t.Fatalf("GetPlan ok=%v err=%v", ok, err)
	}
	if got.GoalID != goal.ID || got.CurrentStepID != "step_1" || len(got.AcceptanceCriteria) != 1 {
		t.Fatalf("plan = %+v", got)
	}
	exitCode := 0
	first := GoalEvidence{
		ID:        "ev_1",
		GoalID:    goal.ID,
		Type:      EvidenceTypeCommand,
		Summary:   "first command",
		Command:   "true",
		ExitCode:  &exitCode,
		Passed:    true,
		Payload:   json.RawMessage(`{"index":1}`),
		CreatedAt: time.Now().UTC(),
	}
	second := first
	second.ID = "ev_2"
	second.Summary = "second command"
	second.Payload = json.RawMessage(`{"index":2}`)
	if err := store.AppendEvidence(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvidence(ctx, second); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.ListEvidence(ctx, goal.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 1 || evidence[0].ID != "ev_2" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestLocalStoreGetPlanMissing(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	plan, ok, err := store.GetPlan(ctx, "goal_missing")
	if err != nil || ok || plan.GoalID != "" {
		t.Fatalf("GetPlan missing plan=%+v ok=%v err=%v", plan, ok, err)
	}
}

func TestLocalStoreLockGoalRecoversStaleLock(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir())
	goal, err := store.Create(ctx, CreateInput{Objective: "stale lock", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	path := store.lockPath(goal.ID)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-defaultLockStaleAfter - time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockGoal(ctx, goal.ID)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock path still exists or stat err unexpected: %v", err)
	}
}

func TestLocalStoreForceUnlockGoal(t *testing.T) {
	store := NewLocalStore(t.TempDir())
	id := "goal_force_unlock"
	path := store.lockPath(id)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "meta.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.ForceUnlockGoal(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock path still exists or stat err unexpected: %v", err)
	}
}
