package memstore

import (
	"context"
	"errors"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func TestStoreTracksAgentTaskLifecycle(t *testing.T) {
	store := New()
	ctx := context.Background()
	firstID, err := store.CreateAgentTask(ctx, agenttasks.TaskInput{
		AgentName:   "reviewer",
		Description: "Inspect files",
		Status:      agenttasks.StatusRunning,
		Model:       "model-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := store.CreateAgentTask(ctx, agenttasks.TaskInput{
		AgentName:   "builder",
		Description: "Implement fix",
		Status:      agenttasks.StatusRunning,
		Model:       "model-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID || firstID == 0 || secondID == 0 {
		t.Fatalf("ids first=%d second=%d", firstID, secondID)
	}
	if _, err := store.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TaskID: firstID, EventType: agenttasks.EventTurnStart, PayloadJSON: `{"turn":1}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendAgentTaskEvent(ctx, agenttasks.EventInput{TaskID: firstID, EventType: agenttasks.EventCompleted, PayloadJSON: `{"turns":1}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishAgentTask(ctx, firstID, agenttasks.StatusCompleted, `{"content":"done"}`); err != nil {
		t.Fatal(err)
	}

	tasks, err := store.ListAgentTasks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != secondID || tasks[1].ID != firstID {
		t.Fatalf("tasks = %+v", tasks)
	}
	if tasks[1].Status != agenttasks.StatusCompleted || tasks[1].ResultJSON == "" || tasks[1].FinishedAt.IsZero() {
		t.Fatalf("finished task = %+v", tasks[1])
	}
	events, err := store.ListAgentTaskEvents(ctx, firstID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].EventType != agenttasks.EventTurnStart || events[1].EventType != agenttasks.EventCompleted {
		t.Fatalf("events = %+v", events)
	}
}

func TestStoreCancelsAgentTask(t *testing.T) {
	store := New()
	ctx := context.Background()
	taskID, err := store.CreateAgentTask(ctx, agenttasks.TaskInput{Status: agenttasks.StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CancelAgentTask(ctx, taskID, `{"cancelled":true}`); err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.IsAgentTaskCancelled(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("task should be cancelled")
	}
	if err := store.FinishAgentTask(ctx, taskID, agenttasks.StatusCompleted, `{"content":"late"}`); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListAgentTasks(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != agenttasks.StatusCancelled {
		t.Fatalf("tasks = %+v", tasks)
	}
	if err := store.CancelAgentTask(ctx, 999, "{}"); !errors.Is(err, mysqlstore.ErrNotFound) {
		t.Fatalf("cancel missing err = %v", err)
	}
}
