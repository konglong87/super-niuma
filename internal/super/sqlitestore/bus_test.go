package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

type busClock struct{ ms atomic.Int64 }

func newBusClock() *busClock                { c := &busClock{}; c.ms.Store(1800000000000); return c }
func (c *busClock) Now() time.Time          { return time.UnixMilli(c.ms.Load()) }
func (c *busClock) Advance(d time.Duration) { c.ms.Add(d.Milliseconds()) }
func busEvent(id, scope, topic string) eventbus.Event {
	return eventbus.Event{ID: id, Scope: scope, Topic: topic, AggregateID: "aggregate", Version: eventbus.EventVersion, Payload: json.RawMessage(`{"reference":"safe"}`), OccurredAtMS: 1800000000000}
}
func busSub(scope, name string, max int) eventbus.Subscription {
	return eventbus.Subscription{Scope: scope, Name: name, Topics: []string{"test.ready"}, Start: eventbus.StartBeginning, MaxAttempts: max}
}
func mustAppendBus(t *testing.T, s *Store, e eventbus.Event) eventbus.Event {
	t.Helper()
	saved, created, err := s.AppendEvent(context.Background(), e)
	if err != nil || !created {
		t.Fatalf("append: created=%v err=%v", created, err)
	}
	return saved
}
func mustClaimBus(t *testing.T, b *Bus, scope, name string) eventbus.Delivery {
	t.Helper()
	d, found, err := b.ClaimEvent(context.Background(), scope, name, "worker", time.Second)
	if err != nil || !found {
		t.Fatalf("claim: found=%v err=%v", found, err)
	}
	return d
}

func TestBusAppendIdempotencyIsolationAndBounds(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	e := busEvent("same", "one", "test.ready")
	e.Payload = json.RawMessage(`{"b":2,"a":1}`)
	saved := mustAppendBus(t, s, e)
	e.Payload = json.RawMessage(`{ "a":1, "b":2 }`)
	again, created, err := s.AppendEvent(ctx, e)
	if err != nil || created || !reflect.DeepEqual(saved, again) {
		t.Fatalf("replay: %v %v", created, err)
	}
	e.Payload = json.RawMessage(`{"a":9}`)
	if _, _, err = s.AppendEvent(ctx, e); !errors.Is(err, eventbus.ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	other := mustAppendBus(t, s, busEvent("same", "two", "test.ready"))
	if other.Sequence <= saved.Sequence {
		t.Fatal("sequence not global")
	}
	page, err := s.ReadEvents(ctx, "one", 0, 100)
	if err != nil || len(page) != 1 || page[0].Scope != "one" {
		t.Fatalf("scope: %v %v", page, err)
	}
	for _, limit := range []int{0, -1, 101} {
		if _, err = s.ReadEvents(ctx, "one", 0, limit); !errors.Is(err, eventbus.ErrInvalidInput) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, payload := range []string{`{"a":1,"a":2}`, `{"x":{"a":1,"a":2}}`, `[]`, `{} {}`, `{"secret":"` + strings.Repeat("x", eventbus.MaxPayloadBytes) + `"}`} {
		invalid := busEvent("invalid", "one", "test.ready")
		invalid.Payload = json.RawMessage(payload)
		if _, _, err = s.AppendEvent(ctx, invalid); !errors.Is(err, eventbus.ErrInvalidInput) {
			t.Fatalf("invalid payload accepted: %v", err)
		}
	}
}

func TestBusIndependentSubscribersRestartAndFencing(t *testing.T) {
	s, path := openTestStore(t)
	ctx := context.Background()
	clock := newBusClock()
	b := s.Bus(eventbus.Options{Now: clock.Now})
	e := mustAppendBus(t, s, busEvent("one", "scope", "test.ready"))
	for _, name := range []string{"a", "b"} {
		if err := b.RegisterSubscription(ctx, busSub("scope", name, 3)); err != nil {
			t.Fatal(err)
		}
	}
	first := mustClaimBus(t, b, "scope", "a")
	parallel := mustClaimBus(t, b, "scope", "b")
	if first.Event.Sequence != e.Sequence || parallel.Event.Sequence != e.Sequence {
		t.Fatal("subscribers competed for one notification")
	}
	if _, ok, err := b.ClaimEvent(ctx, "scope", "a", "other", time.Second); err != nil || ok {
		t.Fatalf("live lease stolen: %v %v", ok, err)
	}
	wrong := first.Token
	wrong.Scope = "other"
	if err := b.AckEvent(ctx, wrong); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatalf("cross scope ACK: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	b = reopened.Bus(eventbus.Options{Now: clock.Now})
	clock.Advance(time.Second)
	if err = b.AckEvent(ctx, first.Token); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatalf("expired ACK: %v", err)
	}
	if err = b.NackEvent(ctx, first.Token, eventbus.CodeConsumerFailed, 0); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatalf("expired NACK: %v", err)
	}
	reclaimed := mustClaimBus(t, b, "scope", "a")
	if reclaimed.Attempt != 2 || reclaimed.Token.Epoch <= first.Token.Epoch || reclaimed.Token.Token == first.Token.Token {
		t.Fatal("crash did not consume attempt/rotate fence")
	}
	if err = b.AckEvent(ctx, first.Token); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatalf("stale ACK: %v", err)
	}
	if err = b.AckEvent(ctx, reclaimed.Token); err != nil {
		t.Fatal(err)
	}
	if err = b.RegisterSubscription(ctx, busSub("scope", "a", 3)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.ClaimEvent(ctx, "scope", "a", "worker", time.Second); err != nil || ok {
		t.Fatalf("registration reset cursor: %v %v", ok, err)
	}
	changed := busSub("scope", "a", 3)
	changed.Start = eventbus.StartNow
	if err = b.RegisterSubscription(ctx, changed); !errors.Is(err, eventbus.ErrConflict) {
		t.Fatalf("changed registration: %v", err)
	}
}

func TestBusCrashExhaustionAndNackBackoff(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	c := newBusClock()
	b := s.Bus(eventbus.Options{Now: c.Now})
	mustAppendBus(t, s, busEvent("first", "s", "test.ready"))
	second := mustAppendBus(t, s, busEvent("second", "s", "test.ready"))
	if err := b.RegisterSubscription(ctx, busSub("s", "crashes", 2)); err != nil {
		t.Fatal(err)
	}
	mustClaimBus(t, b, "s", "crashes")
	c.Advance(time.Second)
	last := mustClaimBus(t, b, "s", "crashes")
	if _, ok, err := b.ClaimEvent(ctx, "s", "crashes", "other", time.Second); err != nil || ok {
		t.Fatalf("live final attempt dead-lettered: %v %v", ok, err)
	}
	letters, err := b.ReadDeadLetters(ctx, "s", "crashes", 100)
	if err != nil || len(letters) != 0 {
		t.Fatal("premature DLQ")
	}
	c.Advance(time.Second)
	next := mustClaimBus(t, b, "s", "crashes")
	if next.Event.Sequence != second.Sequence || next.Attempt != 1 {
		t.Fatal("did not advance exhausted event")
	}
	letters, err = b.ReadDeadLetters(ctx, "s", "crashes", 100)
	if err != nil || len(letters) != 1 || letters[0].Attempts != 2 || letters[0].Code != eventbus.CodeLeaseExpired {
		t.Fatalf("crash DLQ: %#v %v", letters, err)
	}
	if err = b.AckEvent(ctx, last.Token); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatal("old claim survived DLQ")
	}
	if err = b.NackEvent(ctx, next.Token, eventbus.CodeConsumerFailed, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := b.ClaimEvent(ctx, "s", "crashes", "worker", time.Second); err != nil || ok {
		t.Fatal("backoff ignored")
	}
	c.Advance(2 * time.Second)
	next = mustClaimBus(t, b, "s", "crashes")
	if err = b.NackEvent(ctx, next.Token, eventbus.CodeConsumerFailed, 0); err != nil {
		t.Fatal(err)
	}
	letters, err = b.ReadDeadLetters(ctx, "s", "crashes", 100)
	if err != nil || len(letters) != 2 {
		t.Fatalf("NACK exhaustion: %v %v", letters, err)
	}
}

func TestBusDLQAndCursorRollbackTogether(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	c := newBusClock()
	b := s.Bus(eventbus.Options{Now: c.Now})
	e := mustAppendBus(t, s, busEvent("one", "s", "test.ready"))
	if err := b.RegisterSubscription(ctx, busSub("s", "consumer", 1)); err != nil {
		t.Fatal(err)
	}
	d := mustClaimBus(t, b, "s", "consumer")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_bus_cursor BEFORE UPDATE OF cursor ON super_subscriptions WHEN NEW.cursor > OLD.cursor BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL'); END`); err != nil {
		t.Fatal(err)
	}
	err := b.NackEvent(ctx, d.Token, eventbus.CodeConsumerFailed, 0)
	if !errors.Is(err, eventbus.ErrUnavailable) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("unsafe/incorrect failure: %v", err)
	}
	letters, err := b.ReadDeadLetters(ctx, "s", "consumer", 100)
	if err != nil || len(letters) != 0 {
		t.Fatal("orphaned DLQ after rollback")
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_bus_cursor"); err != nil {
		t.Fatal(err)
	}
	if err = b.AckEvent(ctx, d.Token); err != nil {
		t.Fatalf("rollback lost lease for %d: %v", e.Sequence, err)
	}
}

func TestBusNowTopicFilteringAndConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	s, path := openTestStore(t)
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	mustAppendBus(t, s, busEvent("history", "s", "test.ready"))
	sub := busSub("s", "now", 3)
	sub.Start = eventbus.StartNow
	if err = s.RegisterSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	mustAppendBus(t, s, busEvent("skip", "s", "test.other"))
	mustAppendBus(t, s, busEvent("foreign", "foreign", "test.ready"))
	want := mustAppendBus(t, s, busEvent("future", "s", "test.ready"))
	var wg sync.WaitGroup
	var winners atomic.Int64
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 != 0 {
				store = other
			}
			d, ok, err := store.ClaimEvent(ctx, "s", "now", fmt.Sprintf("worker-%d", i), time.Minute)
			if err != nil {
				errs <- err
				return
			}
			if ok {
				winners.Add(1)
				if d.Event.Sequence != want.Sequence {
					errs <- fmt.Errorf("wrong event %d", d.Event.Sequence)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if winners.Load() != 1 {
		t.Fatalf("claims=%d", winners.Load())
	}
}

func TestBusSubscriptionCapacityAndSafeErrors(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	for i := 0; i < eventbus.MaxSubscriptions; i++ {
		if err := s.RegisterSubscription(ctx, busSub("s", fmt.Sprintf("sub-%d", i), 2)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterSubscription(ctx, busSub("s", "over", 2)); !errors.Is(err, eventbus.ErrCapacity) {
		t.Fatalf("subscription capacity: %v", err)
	}
	if err := s.RegisterSubscription(ctx, busSub("s", "sub-0", 2)); err != nil {
		t.Fatal("re-registration blocked at capacity")
	}
	if _, _, err := s.ClaimEvent(ctx, "foreign", "sub-0", "worker", time.Second); !errors.Is(err, eventbus.ErrNotFound) {
		t.Fatalf("subscription isolation: %v", err)
	}
	mustAppendBus(t, s, busEvent("one", "s", "test.ready"))
	d, ok, err := s.ClaimEvent(ctx, "s", "sub-0", "worker", time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = s.NackEvent(ctx, d.Token, "secret_token_do_not_store", 0); !errors.Is(err, eventbus.ErrInvalidInput) {
		t.Fatalf("unsafe code: %v", err)
	}
	if err = s.AckEvent(ctx, d.Token); err != nil {
		t.Fatal(err)
	}
}

func TestBusIntakeRollbackAndQuotaCannotBlockCancel(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	svc := workorder.NewService(s, nil)
	order, _, err := svc.Create(ctx, "original", intake())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_bus_insert BEFORE INSERT ON super_events BEGIN SELECT RAISE(ABORT,'PRIVATE_BUS_SQL'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.Create(ctx, "rollback", intake()); !errors.Is(err, workorder.ErrUnavailable) {
		t.Fatalf("create rollback: %v", err)
	}
	if _, err = svc.Cancel(ctx, order.ID); !errors.Is(err, workorder.ErrUnavailable) {
		t.Fatalf("cancel rollback: %v", err)
	}
	got, err := svc.Get(ctx, order.ID)
	if err != nil || got.Status != workorder.StatusRecorded || eventCount(t, s) != 1 {
		t.Fatal("bus failure leaked business/audit writes")
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_bus_insert"); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < ?) INSERT INTO super_events(event_id,scope,topic,aggregate_id,version,payload,occurred_at_ms) SELECT 'quota-'||x,'quota','test.ready','aggregate',1,'{}',1800000000000 FROM n`, eventbus.MaxRetainedEvents-1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AppendEvent(ctx, busEvent("full", "s", "test.ready")); !errors.Is(err, eventbus.ErrCapacity) {
		t.Fatalf("event capacity: %v", err)
	}
	if _, _, err = svc.Create(ctx, "new-at-capacity", intake()); err == nil {
		t.Fatal("new intake admitted at capacity")
	}
	replay, created, err := svc.Create(ctx, "original", intake())
	if err != nil || created || replay.ID != order.ID {
		t.Fatal("idempotent intake blocked at capacity")
	}
	cancelled, err := svc.Cancel(ctx, order.ID)
	if err != nil || cancelled.Status != workorder.StatusCancelled {
		t.Fatalf("quota blocked cancel: %v", err)
	}
	if _, err = svc.Cancel(ctx, order.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.ReadEvents(ctx, eventbus.LocalScope, 0, 100)
	if err != nil || len(page) != 2 || page[1].Topic != eventbus.TopicWorkOrderCancelled {
		t.Fatalf("intake bus: %v %v", page, err)
	}
	if strings.Contains(string(page[0].Payload), order.Objective) {
		t.Fatal("objective leaked into bus")
	}
	if _, created, err = s.AppendEvent(ctx, pageWithoutSequence(page[1])); err != nil || created {
		t.Fatalf("exact event replay blocked at quota: %v", err)
	}
	fake := busEvent("fake-control", eventbus.LocalScope, eventbus.TopicWorkOrderCancelled)
	if _, _, err = s.AppendEvent(ctx, fake); !errors.Is(err, eventbus.ErrCapacity) {
		t.Fatalf("public cancellation bypassed quota: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, _, err = appendEventTx(ctx, tx, fake, true); !errors.Is(err, eventbus.ErrInvalidInput) {
		t.Fatalf("arbitrary internal control bypass: %v", err)
	}
}
func pageWithoutSequence(e eventbus.Event) eventbus.Event { e.Sequence = 0; return e }
