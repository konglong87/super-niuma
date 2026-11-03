package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

func TestOperatorRepositoryDurableCrossScopeAndBounds(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	for i := 0; i < control.MaxRuns+1; i++ {
		scope := []string{"alice", "bob"}[i%2]
		r := controlFixture(fmt.Sprintf("operator-%03d", i), scope, "/work", false)
		r.Owner = scope
		r = createControlRun(t, s, r)
		next := nextControlRun(r)
		next.Status = control.Cancelled
		next.Jobs[0].Status = control.JobCancelled
		saveControlRun(t, s, next)
	}
	rows, more, err := s.RecentRunsForOperator(ctx, control.MaxRuns)
	if err != nil || !more || len(rows) != control.MaxRuns || rows[0].ID != "operator-100" || rows[99].ID != "operator-001" {
		t.Fatalf("rows=%d more=%v err=%v", len(rows), more, err)
	}
	if rows[0].Scope != "alice" || rows[1].Scope != "bob" {
		t.Fatal("scope filtering or ordering changed")
	}
	found, err := s.RunForOperator(ctx, "operator-000")
	if err != nil || found.Scope != "alice" || found.Owner != "alice" {
		t.Fatalf("old history unavailable %+v %v", found, err)
	}
	if _, err = s.RunForOperator(ctx, "missing"); !errors.Is(err, control.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.RunForOperator(ctx, "invalid/id"); !errors.Is(err, control.ErrInvalid) {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1, control.MaxRuns + 1} {
		if _, _, err = s.RecentRunsForOperator(ctx, limit); !errors.Is(err, control.ErrInvalid) {
			t.Fatal("invalid limit accepted")
		}
	}
	scoped, err := s.ListRuns(ctx, "bob")
	if err != nil || len(scoped) != 50 {
		t.Fatalf("scoped port changed %d %v", len(scoped), err)
	}
	for _, r := range scoped {
		if r.Scope != "bob" {
			t.Fatal("scoped listing leaked")
		}
	}
}

func TestOperatorRepositoryScopeCollisionFailsClosed(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	for _, scope := range []string{"alice", "bob"} {
		r := controlFixture("collision", scope, "/work", false)
		r.Owner = scope
		createControlRun(t, s, r)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.RunForOperator(ctx, "collision"); !errors.Is(err, control.ErrConflict) {
			t.Fatalf("lookup selected an arbitrary scope: %v", err)
		}
	}
	// limit=1 must still detect the second matching row outside the page.
	if rows, _, err := s.RecentRunsForOperator(ctx, 1); !errors.Is(err, control.ErrConflict) || rows != nil {
		t.Fatalf("collision hidden by page: %+v %v", rows, err)
	}
	for _, scope := range []string{"alice", "bob"} {
		r, err := s.GetRun(ctx, scope, "collision")
		if err != nil || r.Scope != scope {
			t.Fatalf("explicit scoped read changed %+v %v", r, err)
		}
	}
}

func TestOperatorRepositoryEmptyAndCancelledContext(t *testing.T) {
	s, _ := openControlTestStore(t)
	rows, more, err := s.RecentRunsForOperator(context.Background(), 100)
	if err != nil || more || rows == nil || len(rows) != 0 {
		t.Fatalf("empty=%+v more=%v err=%v", rows, more, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.RunForOperator(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err = s.RecentRunsForOperator(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOperatorCrossScopeAbandonStoreProofAndUnknownReservation(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	initial := controlFixture("abandon-alice", "alice", "/work", true)
	initial.Owner = "alice"
	r := createControlRun(t, s, initial)
	running := claimControlRun(r, "fixture-stopped-worker")
	running.Reservations = []control.Reservation{{ID: "uncertain-request", JobID: running.Jobs[0].Spec.ID, AttemptID: running.Jobs[0].Claim.Token, Input: 7, Output: 9}}
	r = saveControlRun(t, s, running)
	uncertain := nextControlRun(r)
	uncertain.Status = control.NeedsAttention
	uncertain.Jobs[0].Status = control.JobUnknown
	r = saveControlRun(t, s, uncertain)
	if controlCount(t, s, "super_resources") != 1 {
		t.Fatal("fixture did not hold real resource")
	}
	candidate := nextControlRun(r)
	candidate.Status = control.Cancelled
	candidate.SafeCode = "operator_abandoned_no_retry"
	candidate.Resolution = &control.Resolution{Kind: control.ResolutionAbandoned, Actor: control.LocalOperatorActor, PreviousVersion: r.Version, AtMS: candidate.UpdatedAtMS}
	candidate.Jobs[0].Status = control.JobCancelled
	candidate.Jobs[0].FinishedAtMS = candidate.UpdatedAtMS
	candidate.Jobs[0].SafeCode = "operator_stop_confirmed"
	candidate.Notices = append([]control.Notice(nil), r.Notices...)
	candidate.VerificationClaim = control.Claim{Epoch: r.VerificationClaim.Epoch}
	if !control.ValidStopResolution(r, candidate) {
		t.Fatal("canonical fixture invalid")
	}
	for _, change := range []func(*control.Run){
		func(n *control.Run) { n.Resolution.Actor = "alice" },
		func(n *control.Run) { n.Reservations[0].Settled = true },
		func(n *control.Run) { n.Jobs[0].Result.Text = "invented success" },
	} {
		forged := copyControlRun(candidate)
		change(&forged)
		if _, err := s.SaveRun(ctx, r.Version, forged); !errors.Is(err, control.ErrDenied) {
			t.Fatalf("store accepted forged release proof: %v", err)
		}
		if controlCount(t, s, "super_resources") != 1 {
			t.Fatal("forged proof released resources")
		}
	}
	e := &control.Engine{Store: s}
	if _, err := e.AbandonAfterInspection(ctx, control.Principal{Scope: "alice", Owner: "alice"}, r.ID, r.Version, control.StopConfirmation); !errors.Is(err, control.ErrDenied) {
		t.Fatal("nonadmin abandoned", err)
	}
	out, err := e.AbandonAfterInspection(ctx, control.Principal{Scope: r.Scope, Owner: r.Owner, LocalAdmin: true}, r.ID, r.Version, control.StopConfirmation)
	if err != nil || out.Resolution.Actor != control.LocalOperatorActor || !control.ValidStopResolution(r, out) || out.Scope != "alice" || out.Owner != "alice" {
		t.Fatalf("abandon=%+v err=%v", out.Resolution, err)
	}
	if control.ReservedTokens(out) != 16 || out.Reservations[0].Settled || out.Jobs[0].Claim != r.Jobs[0].Claim || controlCount(t, s, "super_resources") != 0 {
		t.Fatal("abandon lost provenance/refunded unknown/did not release")
	}
	saved, err := s.GetRun(ctx, "alice", r.ID)
	if err != nil || saved.Resolution.Actor != control.LocalOperatorActor || saved.Status != control.Cancelled {
		t.Fatal("resolution not durable", err)
	}
}

func TestOperatorCrossScopeEventsAndDeadLettersStayReadOnly(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	subscriber := "super.notices.v1"
	var events []eventbus.Event
	for _, scope := range []string{"local", "alice", "bob"} {
		events = append(events, mustAppendBus(t, s, busEvent("notice-"+scope, scope, "test.ready")))
		if err := s.RegisterSubscription(ctx, busSub(scope, subscriber, 1)); err != nil {
			t.Fatal(err)
		}
		d, found, err := s.ClaimEvent(ctx, scope, subscriber, "fixture-consumer", time.Second)
		if err != nil || !found {
			t.Fatalf("claim=%v err=%v", found, err)
		}
		if err = s.NackEvent(ctx, d.Token, eventbus.CodeConsumerFailed, 0); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.EventsForOperator(ctx, 0, 2)
	if err != nil || len(page) != 2 || page[0].Scope != "local" || page[1].Scope != "alice" {
		t.Fatalf("global events=%+v err=%v", page, err)
	}
	tail, err := s.EventsForOperator(ctx, page[1].Sequence, 2)
	if err != nil || len(tail) != 1 || tail[0].Scope != "bob" {
		t.Fatalf("global cursor=%+v err=%v", tail, err)
	}
	for i := 0; i < 2; i++ {
		dlq, err := s.DeadLettersForOperator(ctx, subscriber, 100)
		if err != nil || len(dlq) != 3 || dlq[0].Scope != "bob" || dlq[2].Scope != "local" {
			t.Fatalf("global DLQ=%+v err=%v", dlq, err)
		}
		for _, d := range dlq {
			if d.Scope != d.Event.Scope || d.Subscriber != subscriber || d.Code != eventbus.CodeConsumerFailed || d.Attempts != 1 {
				t.Fatal("DLQ evidence mismatch")
			}
		}
	}
	for _, scope := range []string{"local", "alice", "bob"} {
		scoped, err := s.ReadEvents(ctx, scope, 0, 100)
		if err != nil || len(scoped) != 1 || scoped[0].Scope != scope {
			t.Fatal("external scoped event port broadened", err)
		}
		dlq, err := s.ReadDeadLetters(ctx, scope, subscriber, 100)
		if err != nil || len(dlq) != 1 || dlq[0].Scope != scope {
			t.Fatal("external scoped DLQ port broadened", err)
		}
		if _, found, err := s.ClaimEvent(ctx, scope, subscriber, "fixture-consumer", time.Second); err != nil || found {
			t.Fatal("operator read replayed/retried", err)
		}
	}
	limited, err := s.DeadLettersForOperator(ctx, subscriber, 1)
	if err != nil || len(limited) != 1 || limited[0].Event.Sequence != events[2].Sequence {
		t.Fatal("DLQ not bounded", err)
	}
	for _, limit := range []int{0, -1, eventbus.MaxReadLimit + 1} {
		if _, err = s.EventsForOperator(ctx, 0, limit); !errors.Is(err, control.ErrInvalid) {
			t.Fatal("event limit accepted", err)
		}
		if _, err = s.DeadLettersForOperator(ctx, subscriber, limit); !errors.Is(err, control.ErrInvalid) {
			t.Fatal("DLQ limit accepted", err)
		}
	}
	if _, err = s.EventsForOperator(ctx, -1, 1); !errors.Is(err, control.ErrInvalid) {
		t.Fatal("negative cursor accepted", err)
	}
	if _, err = s.DeadLettersForOperator(ctx, "", 1); !errors.Is(err, control.ErrInvalid) {
		t.Fatal("empty subscriber accepted", err)
	}
}
