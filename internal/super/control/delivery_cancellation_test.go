package control_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
)

const (
	deliveryRegressionWait  = 10 * time.Second
	deliveryRegressionLease = time.Minute
	deliveryRejectedCode    = "fixture_not_accepted"
)

// Only the transport is controlled: scheduling, claims, cancellation,
// verification and receipt persistence all use the existing SQLite harness.
// Each call remains inside Send until its own reply is explicitly released.
type pausedDeliverySender struct {
	started chan pausedDeliveryCall
	calls   atomic.Int64
}

type pausedDeliveryCall struct {
	id     string
	target control.Target
	reply  chan pausedDeliveryResult
}

type pausedDeliveryResult struct {
	receipt control.Receipt
	err     error
}

func (s *pausedDeliverySender) Send(ctx context.Context, target control.Target, id, _ string) (control.Receipt, error) {
	call := pausedDeliveryCall{id: id, target: target, reply: make(chan pausedDeliveryResult, 1)}
	s.calls.Add(1)
	select {
	case s.started <- call:
	case <-ctx.Done():
		return control.Receipt{}, ctx.Err()
	}
	select {
	case result := <-call.reply:
		return result.receipt, result.err
	case <-ctx.Done():
		return control.Receipt{}, ctx.Err()
	}
}

type pausedDeliveryStepResult struct {
	worked bool
	err    error
}

type inFlightDelivery struct {
	call pausedDeliveryCall
	done <-chan pausedDeliveryStepResult
}

func startInFlightDelivery(t *testing.T, h *sqliteHarness, sender *pausedDeliverySender, owner string) inFlightDelivery {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan pausedDeliveryStepResult, 1)
	exited := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(deliveryRegressionWait):
			t.Errorf("delivery worker %s did not exit", owner)
		}
	})
	go func() {
		defer close(exited)
		worked, err := h.engine.Step(ctx, owner)
		done <- pausedDeliveryStepResult{worked: worked, err: err}
	}()
	select {
	case call := <-sender.started:
		return inFlightDelivery{call: call, done: done}
	case result := <-done:
		t.Fatalf("worker %s returned before entering Send: worked=%v err=%v", owner, result.worked, result.err)
	case <-time.After(deliveryRegressionWait):
		t.Fatalf("worker %s never entered Send", owner)
	}
	return inFlightDelivery{}
}

func (d inFlightDelivery) settle(t *testing.T, result pausedDeliveryResult, wantErr error) {
	t.Helper()
	d.call.reply <- result
	select {
	case got := <-d.done:
		if !got.worked || !errors.Is(got.err, wantErr) {
			t.Fatalf("settle %s: worked=%v err=%v; want worked=true err=%v", d.call.id, got.worked, got.err, wantErr)
		}
	case <-time.After(deliveryRegressionWait):
		t.Fatalf("delivery %s did not settle", d.call.id)
	}
}

func (d inFlightDelivery) accepted() pausedDeliveryResult {
	return pausedDeliveryResult{receipt: control.Receipt{
		MessageID: "accepted." + d.call.id, RequestID: "request." + d.call.id,
		Chat: d.call.target.Chat, DeliveredAtMS: time.Now().UnixMilli(),
	}}
}

func twoInFlightDeliveries(t *testing.T) (*sqliteHarness, *pausedDeliverySender, control.Run, [2]inFlightDelivery) {
	t.Helper()
	h := newSQLiteHarness(t)
	sender := &pausedDeliverySender{started: make(chan pausedDeliveryCall, 2)}
	h.engine.Sender = sender
	// Keep real-time deadlines out of scheduling assertions. Recovery advances
	// Engine.Now to the saved lease boundary instead of sleeping.
	h.engine.Lease = deliveryRegressionLease
	grant := h.grant()
	grant.Targets = append(grant.Targets, control.Target{ID: "desktop.backup", Provider: control.ProviderDesktop})
	r := h.start(t, "two-in-flight-deliveries", grant)
	h.step(t, "fixture-worker")
	h.step(t, "fixture-verifier")
	r = h.get(t, r.ID)
	if r.Status != control.DeliveryPending || len(r.Notices) != 2 || r.VerificationClaim.Token != "" {
		t.Fatalf("delivery fixture: status=%s notices=%d verification_claim=%+v", r.Status, len(r.Notices), r.VerificationClaim)
	}
	for _, job := range r.Jobs {
		if job.Status != control.JobSucceeded {
			t.Fatalf("fixture still has a non-successful job: %+v", job)
		}
	}
	// Starting the second worker only after the first entered Send forces it
	// to claim the other notice, while the first transport is still blocked.
	pending := [2]inFlightDelivery{
		startInFlightDelivery(t, h, sender, "sender.first"),
		startInFlightDelivery(t, h, sender, "sender.second"),
	}
	r = h.get(t, r.ID)
	for i, notice := range r.Notices {
		if notice.ID != pending[i].call.id || notice.Status != control.DeliverySending || notice.Attempts != 1 || notice.Claim.Token == "" || notice.Receipt != nil {
			t.Fatalf("notice %d is not independently in flight: %+v", i, notice)
		}
	}
	if sender.calls.Load() != 2 || r.Notices[0].Claim.Token == r.Notices[1].Claim.Token {
		t.Fatalf("expected two concurrent, separately claimed sends; calls=%d", sender.calls.Load())
	}
	return h, sender, r, pending
}

func assertDeliverySnapshot(t *testing.T, got control.Run, status control.Status, notices []control.Notice) {
	t.Helper()
	if got.Status != status || !reflect.DeepEqual(got.Notices, notices) {
		t.Fatalf("delivery snapshot: status=%s want=%s\nnotices=%+v\nwant=%+v", got.Status, status, got.Notices, notices)
	}
}

func TestSQLiteDeliveryCancellationWaitsForEveryInFlightNotice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rejected int
	}{
		{name: "both_accepted", rejected: -1},
		{name: "first_definitely_not_accepted", rejected: 0},
		{name: "last_definitely_not_accepted", rejected: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, sender, sending, pending := twoInFlightDeliveries(t)
			results := [2]pausedDeliveryResult{pending[0].accepted(), pending[1].accepted()}
			if tc.rejected >= 0 {
				// Even a normally retryable rejection must finish cancellation,
				// not become retry/unknown/needs_attention or trigger a resend.
				results[tc.rejected] = pausedDeliveryResult{err: &control.SendError{
					Code: deliveryRejectedCode, Retryable: true, DefinitelyNotAccepted: true,
				}}
			}
			want := append([]control.Notice(nil), sending.Notices...)
			for i := range want {
				if results[i].err == nil {
					want[i].Status = control.DeliverySent
					want[i].Receipt = &results[i].receipt
				} else {
					want[i].Status = control.DeliveryDead
					want[i].SafeCode = deliveryRejectedCode
				}
			}

			ctx := context.Background()
			cancelled, err := h.engine.Cancel(ctx, h.principal, sending.ID, sending.Version)
			if err != nil {
				t.Fatal(err)
			}
			assertDeliverySnapshot(t, cancelled, control.Cancelling, sending.Notices)
			pending[0].settle(t, results[0], nil)
			assertDeliverySnapshot(t, h.get(t, sending.ID), control.Cancelling, []control.Notice{want[0], sending.Notices[1]})

			pending[1].settle(t, results[1], nil)
			assertDeliverySnapshot(t, h.get(t, sending.ID), control.Cancelled, want)
			h.reopen(t)
			assertDeliverySnapshot(t, h.get(t, sending.ID), control.Cancelled, want)
			if worked, err := h.engine.Step(ctx, "after-cancel"); err != nil || worked {
				t.Fatalf("cancelled run scheduled work: worked=%v err=%v", worked, err)
			}
			if terminal, err := h.engine.DeliverRun(ctx, sending.Scope, sending.ID, "after-cancel"); err != nil || !terminal {
				t.Fatalf("cancelled delivery is not terminal: terminal=%v err=%v", terminal, err)
			}
			assertDeliverySnapshot(t, h.get(t, sending.ID), control.Cancelled, want)
			if sender.calls.Load() != 2 {
				t.Fatalf("cancelled notices resent: calls=%d", sender.calls.Load())
			}
		})
	}
}

func TestSQLiteDeliveryNeedsAttentionRecoversInFlightNoticeAfterRestart(t *testing.T) {
	h, sender, sending, pending := twoInFlightDeliveries(t)
	ctx := context.Background()
	// One transport loses its response while the other is still in flight.
	// The root is terminal to new work, but still owes recovery for that send.
	pending[0].settle(t, pausedDeliveryResult{err: context.DeadlineExceeded}, nil)
	want := append([]control.Notice(nil), sending.Notices...)
	want[0].Status = control.DeliveryUnknown
	want[0].SafeCode = "delivery_outcome_unknown"
	assertDeliverySnapshot(t, h.get(t, sending.ID), control.NeedsAttention, want)

	// The remaining Sender is parked on its reply channel, so no database
	// operation is in progress while the durable store is closed/reopened.
	h.reopen(t)
	persisted := h.get(t, sending.ID)
	assertDeliverySnapshot(t, persisted, control.NeedsAttention, want)
	active, err := h.store.ActiveRuns(ctx)
	if err != nil || len(active) != 1 || active[0].ID != sending.ID {
		t.Fatalf("restart hid a terminal root with an in-flight notice: active=%+v err=%v", active, err)
	}
	assertDeliverySnapshot(t, active[0], control.NeedsAttention, want)

	recovery := *h.engine
	// A separate nonblocking spy makes any accidental replacement send fail
	// the assertion, rather than hanging behind the original transport gate.
	recovery.Sender = h.sender
	leaseEnd := sending.Notices[1].Claim.UntilMS
	recovery.Now = func() time.Time { return time.UnixMilli(leaseEnd - 1) }
	if worked, err := recovery.Step(ctx, "recovery-before-expiry"); err != nil || worked {
		t.Fatalf("unexpired terminal-root send scheduled: worked=%v err=%v", worked, err)
	}
	assertDeliverySnapshot(t, h.get(t, sending.ID), control.NeedsAttention, want)
	if h.get(t, sending.ID).Version != persisted.Version {
		t.Fatal("recovery changed an unexpired in-flight notice")
	}

	recovery.Now = func() time.Time { return time.UnixMilli(leaseEnd) }
	if worked, err := recovery.Step(ctx, "recovery-at-expiry"); err != nil || worked {
		t.Fatalf("expired terminal-root recovery: worked=%v err=%v", worked, err)
	}
	want[1].Status = control.DeliveryUnknown
	want[1].SafeCode = "delivery_outcome_unknown"
	expired := h.get(t, sending.ID)
	assertDeliverySnapshot(t, expired, control.NeedsAttention, want)
	if expired.SafeCode != "lease_lost" || expired.Version != persisted.Version+1 {
		t.Fatalf("lease recovery was not persisted exactly once: code=%s version=%d previous=%d", expired.SafeCode, expired.Version, persisted.Version)
	}
	if err := h.store.CheckClaim(ctx, sending.Notices[1].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("expired sender retained authority: %v", err)
	}

	// A late accepted response cannot overwrite the fenced unknown outcome,
	// manufacture a receipt or resurrect the needs_attention root.
	pending[1].settle(t, pending[1].accepted(), control.ErrConflict)
	h.reopen(t)
	recovery.Store = h.store
	assertDeliverySnapshot(t, h.get(t, sending.ID), control.NeedsAttention, want)
	for i := 0; i < 2; i++ {
		if worked, err := recovery.Step(ctx, "no-blind-retry"); err != nil || worked {
			t.Fatalf("unknown delivery scheduled: worked=%v err=%v", worked, err)
		}
		if terminal, err := recovery.DeliverRun(ctx, sending.Scope, sending.ID, "no-blind-resend"); err != nil || !terminal {
			t.Fatalf("unknown delivery resumed: terminal=%v err=%v", terminal, err)
		}
	}
	assertDeliverySnapshot(t, h.get(t, sending.ID), control.NeedsAttention, want)
	if h.get(t, sending.ID).Version != expired.Version || sender.calls.Load() != 2 || h.sender.count() != 0 {
		t.Fatalf("recovery retried or rewrote delivery: original_calls=%d recovery_calls=%d", sender.calls.Load(), h.sender.count())
	}
	if active, err := h.store.ActiveRuns(ctx); err != nil || len(active) != 0 {
		t.Fatalf("settled unknown notices should not remain schedulable: active=%+v err=%v", active, err)
	}
}
