package pendinginput

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileQueueRestoresQueuedInputs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending-inputs.json")
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session", BaseTaskID: 7}
	queue, err := NewFileQueue(path)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	if _, err := queue.Add(context.Background(), NewInput{Scope: scope, ClientInputID: "one", Content: "persist me"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	restored, err := NewFileQueue(path)
	if err != nil {
		t.Fatalf("restore queue: %v", err)
	}
	items, err := restored.List(context.Background(), scope)
	if err != nil || len(items) != 1 || items[0].Content != "persist me" {
		t.Fatalf("restored = %+v, err=%v", items, err)
	}
}

func TestFileQueueRecoversRunningCandidateAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "queue.json")
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session", BaseTaskID: 3}
	queue, err := NewFileQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	item, err := queue.Add(ctx, NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, ok, err := queue.ClaimNext(ctx, scope); err != nil || !ok || claimed.ID != item.ID {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	restored, err := NewFileQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	items, err := restored.List(ctx, scope)
	if err != nil || len(items) != 1 || items[0].Status != StatusFailed || items[0].ErrorCode != "consumer_restarted" {
		t.Fatalf("restored=%+v err=%v", items, err)
	}
	if _, err := restored.Retry(ctx, item.ID); err != nil {
		t.Fatalf("recovered candidate is not retryable: %v", err)
	}
}
