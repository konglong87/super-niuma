package imagegen

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type completionRepositoryStub struct {
	mu      sync.Mutex
	events  []CompletionEvent
	claims  int
	retries []completionTransition
	dead    []completionTransition
}

type completionTransition struct {
	eventID uint64
	owner   string
	next    time.Time
	code    string
}

func (s *completionRepositoryStub) ClaimDueImageCompletionEvents(context.Context, uint64, string, int, time.Time) ([]CompletionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	events := append([]CompletionEvent(nil), s.events...)
	s.events = nil
	return events, nil
}

func (s *completionRepositoryStub) MarkImageCompletionEventRetry(_ context.Context, _ uint64, eventID uint64, owner string, next time.Time, code, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retries = append(s.retries, completionTransition{eventID: eventID, owner: owner, next: next, code: code})
	return nil
}

func (s *completionRepositoryStub) MarkImageCompletionEventDead(_ context.Context, _ uint64, eventID uint64, owner, code, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dead = append(s.dead, completionTransition{eventID: eventID, owner: owner, code: code})
	return nil
}

type completionConsumerFunc func(context.Context, CompletionEvent) error

func (f completionConsumerFunc) ConsumeImageCompletion(ctx context.Context, event CompletionEvent) error {
	return f(ctx, event)
}

func TestCompletionDispatcherConsumesClaimedEvent(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	event := CompletionEvent{ID: 3, TenantID: 7, GenerationID: "gen-1", OriginType: OriginTypeChannel, Status: CompletionDeliveryStatusSending, Attempts: 1, LeaseOwner: "dispatcher-a"}
	repo := &completionRepositoryStub{events: []CompletionEvent{event}}
	var consumed []CompletionEvent
	dispatcher := NewCompletionDispatcher(CompletionDispatcherConfig{
		Repository: repo, Consumer: completionConsumerFunc(func(_ context.Context, got CompletionEvent) error {
			consumed = append(consumed, got)
			return nil
		}),
		TenantID: 7, WorkerID: "dispatcher-a", Now: func() time.Time { return now },
	})

	if err := dispatcher.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(consumed) != 1 || consumed[0].GenerationID != "gen-1" || len(repo.retries) != 0 || len(repo.dead) != 0 {
		t.Fatalf("consumed=%+v retries=%+v dead=%+v", consumed, repo.retries, repo.dead)
	}
}

func TestCompletionDispatcherRetriesOriginThatIsNotReady(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	event := CompletionEvent{ID: 3, TenantID: 7, GenerationID: "gen-1", OriginType: OriginTypeChannel, Status: CompletionDeliveryStatusSending, Attempts: 1, LeaseOwner: "dispatcher-a"}
	repo := &completionRepositoryStub{events: []CompletionEvent{event}}
	dispatcher := NewCompletionDispatcher(CompletionDispatcherConfig{
		Repository: repo, Consumer: completionConsumerFunc(func(context.Context, CompletionEvent) error { return ErrCompletionOriginNotReady }),
		TenantID: 7, WorkerID: "dispatcher-a", RetryDelay: 2 * time.Second, Now: func() time.Time { return now },
	})

	if err := dispatcher.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.retries) != 1 || repo.retries[0].eventID != 3 || repo.retries[0].owner != "dispatcher-a" || !repo.retries[0].next.Equal(now.Add(2*time.Second)) || repo.retries[0].code != CompletionErrorOriginNotReady {
		t.Fatalf("retries=%+v", repo.retries)
	}
}

func TestCompletionDispatcherDeadLettersPermanentOrExhaustedEvents(t *testing.T) {
	tests := []struct {
		name     string
		attempts uint
		err      error
		code     string
	}{
		{name: "permanent origin", attempts: 1, err: PermanentCompletionError("invalid_origin", errors.New("malformed origin")), code: "invalid_origin"},
		{name: "attempts exhausted", attempts: 3, err: errors.New("database unavailable"), code: CompletionErrorAttemptsExhausted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := CompletionEvent{ID: 4, TenantID: 7, GenerationID: "gen-2", OriginType: OriginTypeChannel, Status: CompletionDeliveryStatusSending, Attempts: test.attempts, LeaseOwner: "dispatcher-a"}
			repo := &completionRepositoryStub{events: []CompletionEvent{event}}
			dispatcher := NewCompletionDispatcher(CompletionDispatcherConfig{Repository: repo, Consumer: completionConsumerFunc(func(context.Context, CompletionEvent) error { return test.err }), TenantID: 7, WorkerID: "dispatcher-a", MaxAttempts: 3})
			if err := dispatcher.ProcessOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(repo.dead) != 1 || repo.dead[0].code != test.code || len(repo.retries) != 0 {
				t.Fatalf("dead=%+v retries=%+v", repo.dead, repo.retries)
			}
		})
	}
}

func TestCompletionDispatcherRunStopsWithItsOwnContext(t *testing.T) {
	repo := &completionRepositoryStub{}
	dispatcher := NewCompletionDispatcher(CompletionDispatcherConfig{Repository: repo, Consumer: completionConsumerFunc(func(context.Context, CompletionEvent) error { return nil }), TenantID: 7, WorkerID: "dispatcher-a", PollInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Run(ctx) }()
	deadline := time.Now().Add(time.Second)
	for {
		repo.mu.Lock()
		claims := repo.claims
		repo.mu.Unlock()
		if claims > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("dispatcher did not poll")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not stop")
	}
}
