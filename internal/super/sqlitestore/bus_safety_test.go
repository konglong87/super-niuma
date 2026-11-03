package sqlitestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

func TestBusEveryTokenFieldIsFenced(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	c := newBusClock()
	b := s.Bus(eventbus.Options{Now: c.Now})
	mustAppendBus(t, s, busEvent("one", "scope", "test.ready"))
	if err := b.RegisterSubscription(ctx, busSub("scope", "consumer", 2)); err != nil {
		t.Fatal(err)
	}
	d := mustClaimBus(t, b, "scope", "consumer")
	mutations := []func(*eventbus.DeliveryToken){func(v *eventbus.DeliveryToken) { v.Scope = "other" }, func(v *eventbus.DeliveryToken) { v.Name = "other" }, func(v *eventbus.DeliveryToken) { v.Owner = "other" }, func(v *eventbus.DeliveryToken) { v.Sequence++ }, func(v *eventbus.DeliveryToken) { v.Epoch++ }, func(v *eventbus.DeliveryToken) { v.Token = strings.Repeat("0", 64) }, func(v *eventbus.DeliveryToken) { v.LeaseUntilMS++ }}
	for i, mutate := range mutations {
		bad := d.Token
		mutate(&bad)
		if err := b.AckEvent(ctx, bad); !errors.Is(err, eventbus.ErrLeaseLost) {
			t.Fatalf("ACK field %d: %v", i, err)
		}
		if err := b.NackEvent(ctx, bad, eventbus.CodeConsumerFailed, 0); !errors.Is(err, eventbus.ErrLeaseLost) {
			t.Fatalf("NACK field %d: %v", i, err)
		}
	}
	if err := b.AckEvent(ctx, d.Token); err != nil {
		t.Fatal("rejections changed valid ownership", err)
	}
	if err := b.AckEvent(ctx, d.Token); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatal("settled capability was reusable")
	}
}
func TestBusFailedClaimDoesNotPublishOrConsumeLease(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	c := newBusClock()
	b := s.Bus(eventbus.Options{Now: c.Now})
	mustAppendBus(t, s, busEvent("one", "s", "test.ready"))
	if err := b.RegisterSubscription(ctx, busSub("s", "consumer", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_claim BEFORE UPDATE OF lease_token ON super_subscriptions WHEN NEW.lease_token <> '' BEGIN SELECT RAISE(ABORT,'PRIVATE_SQL'); END`); err != nil {
		t.Fatal(err)
	}
	d, ok, err := b.ClaimEvent(ctx, "s", "consumer", "worker", time.Second)
	if !errors.Is(err, eventbus.ErrUnavailable) || ok || d.Token.Token != "" {
		t.Fatalf("uncommitted claim escaped: %#v %v %v", d, ok, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_claim"); err != nil {
		t.Fatal(err)
	}
	d = mustClaimBus(t, b, "s", "consumer")
	if d.Attempt != 1 || d.Token.Epoch != 1 {
		t.Fatal("rolled back claim consumed state")
	}
}
func TestBusNowWatermarkIsAtomicWithConcurrentAppend(t *testing.T) {
	ctx := context.Background()
	s, path := openTestStore(t)
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = s.db.Exec(`CREATE TRIGGER assert_now BEFORE INSERT ON super_subscriptions WHEN NEW.start_position='now' AND NEW.cursor <> (SELECT coalesce(max(sequence),0) FROM super_events WHERE scope=NEW.scope) BEGIN SELECT RAISE(ABORT,'NON_ATOMIC_WATERMARK'); END`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		scope := fmt.Sprintf("scope-%d", i)
		sub := busSub(scope, "consumer", 2)
		sub.Start = eventbus.StartNow
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; errs <- s.RegisterSubscription(ctx, sub) }()
		go func() {
			defer wg.Done()
			<-start
			_, _, err := other.AppendEvent(ctx, busEvent("racing", scope, "test.ready"))
			errs <- err
		}()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var cursor int64
		if err = s.db.QueryRow("SELECT cursor FROM super_subscriptions WHERE scope=? AND name=?", scope, sub.Name).Scan(&cursor); err != nil {
			t.Fatal(err)
		}
		events, err := s.ReadEvents(ctx, scope, 0, 100)
		if err != nil || len(events) != 1 {
			t.Fatal("lost racing event", err)
		}
		d, found, err := s.ClaimEvent(ctx, scope, sub.Name, "worker", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if (cursor < events[0].Sequence) != found {
			t.Fatalf("cursor=%d event=%d found=%v", cursor, events[0].Sequence, found)
		}
		if found && d.Event.Sequence != events[0].Sequence {
			t.Fatal("wrong event after now registration")
		}
	}
}
func TestBusOnlyUnmatchedTopicsCanAdvanceWithoutAck(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t)
	skip := mustAppendBus(t, s, busEvent("skip", "scope", "test.other"))
	mustAppendBus(t, s, busEvent("foreign", "foreign", "test.ready"))
	if err := s.RegisterSubscription(ctx, busSub("scope", "consumer", 2)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimEvent(ctx, "scope", "consumer", "worker", time.Second); err != nil || ok {
		t.Fatal("unmatched topic delivered", err)
	}
	var cursor int64
	if err := s.db.QueryRow("SELECT cursor FROM super_subscriptions WHERE scope='scope'").Scan(&cursor); err != nil || cursor != skip.Sequence {
		t.Fatalf("scope/topic scan cursor %d %v", cursor, err)
	}
	match := mustAppendBus(t, s, busEvent("match", "scope", "test.ready"))
	last := mustAppendBus(t, s, busEvent("last", "scope", "test.other"))
	d, ok, err := s.ClaimEvent(ctx, "scope", "consumer", "worker", time.Minute)
	if err != nil || !ok || d.Event.Sequence != match.Sequence {
		t.Fatal("matching event lost", err)
	}
	if _, ok, err = s.ClaimEvent(ctx, "scope", "consumer", "other", time.Second); err != nil || ok {
		t.Fatal("live claim replaced", err)
	}
	if err = s.db.QueryRow("SELECT cursor FROM super_subscriptions WHERE scope='scope'").Scan(&cursor); err != nil || cursor >= match.Sequence {
		t.Fatal("cursor passed outstanding matching event", cursor, err)
	}
	if err = s.AckEvent(ctx, d.Token); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = s.ClaimEvent(ctx, "scope", "consumer", "worker", time.Second); err != nil || ok {
		t.Fatal(err)
	}
	if err = s.db.QueryRow("SELECT cursor FROM super_subscriptions WHERE scope='scope'").Scan(&cursor); err != nil || cursor != last.Sequence {
		t.Fatal("did not skip final unrelated event", err)
	}
}
func TestBusCancelledContextAndDurationValidation(t *testing.T) {
	s, _ := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.AppendEvent(ctx, busEvent("cancelled", "s", "test.ready")); !errors.Is(err, context.Canceled) {
		t.Fatalf("context: %v", err)
	}
	for _, duration := range []time.Duration{0, -time.Second, eventbus.MaxLease + time.Nanosecond} {
		if _, _, err := s.ClaimEvent(context.Background(), "s", "name", "owner", duration); !errors.Is(err, eventbus.ErrInvalidInput) {
			t.Fatalf("duration %v: %v", duration, err)
		}
	}
	if _, err := s.ReadEvents(context.Background(), "s", -1, 10); !errors.Is(err, eventbus.ErrInvalidInput) {
		t.Fatal("negative cursor accepted")
	}
	s.Close()
	if _, err := s.ReadEvents(context.Background(), "s", 0, 10); !errors.Is(err, eventbus.ErrUnavailable) {
		t.Fatalf("closed store: %v", err)
	}
}

const busCrashPathEnv = "SUPER_A1_BUS_CRASH_PATH"

func TestBusLeaseSurvivesAbruptProcessExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "bus.db")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBusAbruptExitChild$")
	cmd.Env = append(os.Environ(), busCrashPathEnv+"="+path)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("test child: %v", err)
	}
	var original eventbus.Delivery
	if err = json.Unmarshal(output, &original); err != nil {
		t.Fatalf("child result: %v %q", err, output)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	clock := newBusClock()
	clock.Advance(time.Second)
	b := s.Bus(eventbus.Options{Now: clock.Now})
	if err = b.AckEvent(ctx, original.Token); !errors.Is(err, eventbus.ErrLeaseLost) {
		t.Fatalf("expired process lease: %v", err)
	}
	recovered := mustClaimBus(t, b, "scope", "consumer")
	if recovered.Event.ID != original.Event.ID || recovered.Attempt != 2 || recovered.Token.Token == original.Token.Token {
		t.Fatal("restart lost attempt/fence")
	}
	if err = b.AckEvent(ctx, recovered.Token); err != nil {
		t.Fatal(err)
	}
}
func TestBusAbruptExitChild(t *testing.T) {
	path := os.Getenv(busCrashPathEnv)
	if path == "" {
		return
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	clock := newBusClock()
	b := s.Bus(eventbus.Options{Now: clock.Now})
	mustAppendBus(t, s, busEvent("crash", "scope", "test.ready"))
	if err = b.RegisterSubscription(context.Background(), busSub("scope", "consumer", 2)); err != nil {
		t.Fatal(err)
	}
	d := mustClaimBus(t, b, "scope", "consumer")
	if err = json.NewEncoder(os.Stdout).Encode(d); err != nil {
		t.Fatal(err)
	}
	os.Exit(0) // Deliberately bypass Close: exercise committed WAL recovery, not an app.
}
