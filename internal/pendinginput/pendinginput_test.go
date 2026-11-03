package pendinginput

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
)

func TestMemoryQueueAddsInOrderAndMovesCandidateUp(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session-1", BaseTaskID: 10}

	first, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	if err != nil {
		t.Fatalf("add first: %v", err)
	}
	second, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if first.Sequence >= second.Sequence {
		t.Fatalf("sequence = %d, %d; want increasing", first.Sequence, second.Sequence)
	}

	if _, err := q.MoveUp(ctx, second.ID); err != nil {
		t.Fatalf("move up: %v", err)
	}
	items, err := q.List(ctx, scope)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 || items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("order = %+v; want B,A", items)
	}
	if _, err := q.MoveUp(ctx, second.ID); err != nil {
		t.Fatalf("move top: %v", err)
	}
}

func TestMemoryQueueRetryIncrementsAttemptWithoutDuplicatingContent(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session-1", BaseTaskID: 10}
	item, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "a", Content: "keep me", Direction: "focus"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	claimed, ok, err := q.ClaimNext(ctx, scope)
	if err != nil || !ok || claimed.ID != item.ID {
		t.Fatalf("claim = %+v, %v, %v", claimed, ok, err)
	}
	if err := q.MarkFailed(ctx, item.ID, "provider_error"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	retried, err := q.Retry(ctx, item.ID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Attempt != 1 || retried.Status != StatusQueued || retried.Content != "keep me" || retried.Direction != "focus" {
		t.Fatalf("retried = %+v", retried)
	}
}

func TestMemoryQueueRejectsInvalidInputAndDuplicateClientID(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session-1", BaseTaskID: 10}
	if _, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "blank", Content: "  "}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank error = %v; want ErrInvalid", err)
	}
	item, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "same", Content: "A"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	duplicate, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "same", Content: "B"})
	if err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	if duplicate.ID != item.ID || duplicate.Content != "A" {
		t.Fatalf("duplicate = %+v; want original item", duplicate)
	}
}

func TestMemoryQueueGlobalClientIDFindsTerminalItemAcrossScopes(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	firstScope := Scope{TenantID: 1, UserID: 2, SessionID: "session-1", BaseTaskID: 10}
	item, err := q.Add(ctx, NewInput{Scope: firstScope, ClientInputID: "global-key", Content: "A", GlobalClientInputID: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.ClaimNext(ctx, firstScope); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := q.MarkSent(ctx, item.ID, 77); err != nil {
		t.Fatal(err)
	}
	found, ok, err := q.FindByClientInputID(ctx, 1, 2, "global-key")
	if err != nil || !ok || found.Status != StatusSent || found.DispatchedTaskID != 77 {
		t.Fatalf("found=%+v ok=%v err=%v", found, ok, err)
	}
	duplicate, err := q.Add(ctx, NewInput{Scope: Scope{TenantID: 1, UserID: 2, SessionID: "session-2", BaseTaskID: 20}, ClientInputID: "global-key", Content: "B", GlobalClientInputID: true})
	if err != nil || duplicate.ID != item.ID || duplicate.SessionID != firstScope.SessionID {
		t.Fatalf("duplicate=%+v err=%v", duplicate, err)
	}
}

func TestMemoryQueueListsActiveSessionStatesInOneBatch(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	for _, input := range []NewInput{
		{Scope: Scope{TenantID: 1, UserID: 2, SessionID: "41", BaseTaskID: 51}, ClientInputID: "a", Content: "A"},
		{Scope: Scope{TenantID: 1, UserID: 2, SessionID: "42", BaseTaskID: 61}, ClientInputID: "b", Content: "B"},
		{Scope: Scope{TenantID: 1, UserID: 3, SessionID: "41", BaseTaskID: 71}, ClientInputID: "foreign", Content: "X"},
	} {
		if _, err := q.Add(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	states, err := q.ListActiveSessionStates(ctx, 1, 2, []string{"41", "42"})
	if err != nil || len(states) != 2 || states["41"].Count != 1 || states["41"].BaseTaskID != 51 || states["42"].BaseTaskID != 61 {
		t.Fatalf("states=%+v err=%v", states, err)
	}
}

func TestMemoryQueueConsumerLeaseSerializesAndRecoversRunningInput(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session", BaseTaskID: 3}
	item, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if acquired, err := q.AcquireConsumerLease(ctx, scope, "owner-a", time.Minute); err != nil || !acquired {
		t.Fatalf("first lease acquired=%v err=%v", acquired, err)
	}
	if acquired, err := q.AcquireConsumerLease(ctx, scope, "owner-b", time.Minute); err != nil || acquired {
		t.Fatalf("second lease acquired=%v err=%v", acquired, err)
	}
	if _, ok, err := q.ClaimNext(ctx, scope); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := q.ReleaseConsumerLease(ctx, scope, "owner-a"); err != nil {
		t.Fatal(err)
	}
	if acquired, err := q.AcquireConsumerLease(ctx, scope, "owner-b", time.Minute); err != nil || !acquired {
		t.Fatalf("takeover acquired=%v err=%v", acquired, err)
	}
	items, err := q.List(ctx, scope)
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Status != StatusFailed || items[0].ErrorCode != "consumer_lease_expired" {
		t.Fatalf("recovered items=%+v err=%v", items, err)
	}
}

func TestMemoryQueueClaimChecksDisabledSettingAtomically(t *testing.T) {
	ctx := context.Background()
	q := NewMemoryQueue()
	scope := Scope{TenantID: 1, UserID: 2, SessionID: "session", BaseTaskID: 3}
	if _, err := q.Add(ctx, NewInput{Scope: scope, ClientInputID: "image", Attachments: []agenttasks.Attachment{{Type: "image", URL: "https://example.test/image.png"}}}); err != nil {
		t.Fatalf("attachment-only add: %v", err)
	}
	if err := q.SetQueueEnabled(ctx, scope, false); err != nil {
		t.Fatal(err)
	}
	if item, ok, err := q.ClaimNext(ctx, scope); err != nil || ok {
		t.Fatalf("disabled claim item=%+v ok=%v err=%v", item, ok, err)
	}
}
