package control_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

func expiryEvents(t *testing.T, h *sqliteHarness) []eventbus.Event {
	t.Helper()
	events, err := h.store.ReadEvents(context.Background(), control.LocalScope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 100 {
		t.Fatal("fixture exceeded event page; cannot assert complete event history")
	}
	return events
}

// A real SQLite resource claim survives expiry and restart. Only the injected
// scheduler clock advances; no verifier, model, sender or application is run.
func TestSQLiteIntegrationVerificationExpirySettlesOnce(t *testing.T) {
	h := newSQLiteHarness(t)
	ctx := context.Background()
	r := h.start(t, "verification-expiry-once", h.grant())
	h.step(t, "fixture-worker")
	r = h.get(t, r.ID)
	if r.Status != control.Verifying {
		t.Fatalf("fixture did not reach verification: %s", r.Status)
	}
	claimed := expiryClone(t, r)
	claimed.Version++
	claimed.UpdatedAtMS = time.Now().UnixMilli()
	claim := control.Claim{Scope: r.Scope, RunID: r.ID, JobID: "super.verify", Owner: "crashed-verifier", Token: "verification-expiry-claim", Epoch: 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	claimed.VerificationClaim = claim
	claimed, err := h.store.SaveRun(ctx, r.Version, claimed)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents := expiryEvents(t, h)
	var clock atomic.Int64
	clock.Store(claim.UntilMS + 1)
	h.engine.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
	h.step(t, "expiry-recovery")
	attention := h.get(t, r.ID)
	if attention.Status != control.NeedsAttention || attention.Version != claimed.Version+1 || attention.VerificationClaim != claim {
		t.Fatalf("verification expiry lost claim or did not settle: %+v", attention)
	}
	if control.Digest(attention.Jobs) != control.Digest(claimed.Jobs) || control.Digest(attention.Reservations) != control.Digest(claimed.Reservations) || control.Digest(attention.Notices) != control.Digest(claimed.Notices) {
		t.Fatal("verification recovery changed receipts/results/usage")
	}
	afterEvents := expiryEvents(t, h)
	if len(afterEvents) != len(beforeEvents)+1 || afterEvents[len(afterEvents)-1].AggregateID != r.ID || afterEvents[len(afterEvents)-1].Topic != control.EventRunChanged {
		t.Fatal("first expiry must emit exactly one run change")
	}
	h.reopen(t)
	if err := h.store.CheckClaim(ctx, claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("quarantined verifier still authorized: %v", err)
	}
	if _, err := h.engine.Retry(ctx, h.principal, r.ID, attention.Version); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("quarantined verifier blindly retried: %v", err)
	}

	// A queued writer makes release of the verifier's read lock observable.
	var executions atomic.Int32
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, req execution.Request) (execution.Result, error) {
		executions.Add(1)
		return sqliteWork(ctx, req)
	})
	writer := h.start(t, "verification-expiry-writer", h.grant())
	stableEvents := control.Digest(expiryEvents(t, h))
	for i := 0; i < 64; i++ {
		clock.Add(100)
		worked, err := h.engine.Step(ctx, "repeated-recovery")
		if err != nil || worked {
			t.Fatalf("repeated recovery should be idle: worked=%v err=%v", worked, err)
		}
	}
	if control.Digest(h.get(t, r.ID)) != control.Digest(attention) || control.Digest(expiryEvents(t, h)) != stableEvents {
		t.Fatal("handled verification expiry changed revision/evidence or appended bus events")
	}
	if executions.Load() != 0 || h.get(t, writer.ID).Jobs[0].Attempts != 0 {
		t.Fatal("quarantined verification read lock released")
	}
	if _, err := h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, attention.Version, "not-confirmed"); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("unconfirmed stop released verifier: %v", err)
	}
	// The verifier was never launched in this crash fixture, so the operator's
	// stop confirmation is truthful; it is not a programmatic stop proof.
	abandoned, err := h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, attention.Version, control.StopConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if abandoned.Status != control.Cancelled || abandoned.VerificationClaim.Token != "" || abandoned.VerificationClaim.Epoch != claim.Epoch || abandoned.VerificationAttemptID != attention.VerificationAttemptID || !control.ValidStopResolution(attention, abandoned) {
		t.Fatal("manual stop did not preserve the existing resolution contract")
	}
	if control.Digest(abandoned.Jobs) != control.Digest(attention.Jobs) || control.Digest(abandoned.Reservations) != control.Digest(attention.Reservations) || len(abandoned.Notices) != len(attention.Notices) {
		t.Fatal("manual stop rewrote receipts or usage")
	}
	h.reopen(t)
	worked, err := h.engine.Step(ctx, "writer-after-stop-proof")
	if err != nil || !worked || executions.Load() != 1 || h.get(t, writer.ID).Jobs[0].Status != control.JobSucceeded || h.sender.count() != 0 {
		t.Fatalf("only confirmed stop should unblock writer: worked=%v executions=%d err=%v", worked, executions.Load(), err)
	}
}

func TestSQLiteIntegrationAttentionStillRecoversJobsAndNotices(t *testing.T) {
	for _, kind := range []string{"jobs", "notices"} {
		t.Run(kind, func(t *testing.T) {
			h := newSQLiteHarness(t)
			ctx := context.Background()
			g := h.grant()
			if kind == "jobs" {
				g = h.grant(sqliteNode("first-read", nil, []string{"src"}, nil), sqliteNode("second-read", nil, []string{"src"}, nil))
			} else {
				g.Targets = []control.Target{{ID: "first", Provider: control.ProviderDesktop}, {ID: "second", Provider: control.ProviderDesktop}}
			}
			r := h.start(t, "attention-expiry-"+kind, g)
			if kind == "notices" {
				h.step(t, "fixture-worker")
				h.step(t, "fixture-verifier")
				r = h.get(t, r.ID)
				if r.Status != control.DeliveryPending {
					t.Fatalf("delivery fixture: %s", r.Status)
				}
			}
			claimed := expiryClone(t, r)
			claimed.Version++
			claimed.UpdatedAtMS = time.Now().UnixMilli()
			firstUntil := time.Now().Add(time.Minute).UnixMilli()
			for i := 0; i < 2; i++ {
				id := "first"
				if i == 1 {
					id = "second"
				}
				claim := control.Claim{Scope: r.Scope, RunID: r.ID, Owner: "crashed-" + id, Token: "claim-" + id, Epoch: 1, UntilMS: firstUntil + int64(i)*1000}
				if kind == "jobs" {
					claim.JobID = claimed.Jobs[i].Spec.ID
					claimed.Status = control.Running
					claimed.Jobs[i].Status = control.JobRunning
					claimed.Jobs[i].Attempts = 1
					claimed.Jobs[i].Claim = claim
					claimed.Jobs[i].StartedAtMS = claimed.UpdatedAtMS
				} else {
					claim.JobID = "super.delivery." + claimed.Notices[i].Target.ID
					claimed.Notices[i].Status = control.DeliverySending
					claimed.Notices[i].Attempts = 1
					claimed.Notices[i].Claim = claim
				}
			}
			if _, err := h.store.SaveRun(ctx, r.Version, claimed); err != nil {
				t.Fatal(err)
			}
			var clock atomic.Int64
			clock.Store(firstUntil + 1)
			h.engine.Now = func() time.Time { return time.UnixMilli(clock.Load()) }
			h.step(t, "first-expiry")
			first := h.get(t, r.ID)
			if first.Status != control.NeedsAttention {
				t.Fatalf("first expiry: %s", first.Status)
			}
			if kind == "jobs" {
				if first.Jobs[0].Status != control.JobUnknown || first.Jobs[1].Status != control.JobRunning {
					t.Fatal("job expiry did not honor individual lease deadlines")
				}
			} else if first.Notices[0].Status != control.DeliveryUnknown || first.Notices[1].Status != control.DeliverySending {
				t.Fatal("notice expiry did not honor individual lease deadlines")
			}
			events := len(expiryEvents(t, h))
			clock.Store(firstUntil + 1001)
			h.step(t, "second-expiry-in-attention")
			second := h.get(t, r.ID)
			if second.Status != control.NeedsAttention || second.Version != first.Version+1 || len(expiryEvents(t, h)) != events+1 {
				t.Fatal("remaining in-flight claim was not settled in needs_attention")
			}
			if kind == "jobs" {
				if second.Jobs[1].Status != control.JobUnknown || second.Jobs[1].Claim != claimed.Jobs[1].Claim {
					t.Fatal("remaining job claim not quarantined intact")
				}
			} else if second.Notices[1].Status != control.DeliveryUnknown || second.Notices[1].Claim != claimed.Notices[1].Claim || second.Notices[1].Receipt != nil {
				t.Fatal("remaining notice lost uncertainty/provenance")
			}
			stableEvents := control.Digest(expiryEvents(t, h))
			for i := 0; i < 4; i++ {
				h.step(t, "settled-expiry")
			}
			if control.Digest(h.get(t, r.ID)) != control.Digest(second) || control.Digest(expiryEvents(t, h)) != stableEvents || h.sender.count() != 0 {
				t.Fatal("settled leases caused repeated mutation or sends")
			}
		})
	}
}
