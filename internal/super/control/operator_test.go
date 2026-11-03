package control

import (
	"context"
	"errors"
	"testing"
)

func TestOperatorAbandonRequiresExplicitScopedStopConfirmation(t *testing.T) {
	e := testEngine()
	g := testGrant(t)
	p := Principal{Scope: LocalScope, Owner: LocalOwner, LocalAdmin: true}
	r, _, err := e.Start(context.Background(), p, "abandon", "fixture", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err = e.mutate(context.Background(), r.Scope, r.ID, func(n *Run) error {
		n.Status = NeedsAttention
		n.Jobs[0].Status = JobUnknown
		n.Jobs[0].Claim = Claim{Token: "old"}
		n.Reservations = []Reservation{{ID: "unknown-cost", JobID: "work", AttemptID: "old", Input: 100, Output: 20}}
		n.Notices = []Notice{{ID: "uncertain", Target: g.Targets[0], Status: DeliveryUnknown}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.AbandonAfterInspection(context.Background(), Principal{Scope: LocalScope, Owner: LocalOwner}, r.ID, r.Version, StopConfirmation); !errors.Is(err, ErrDenied) {
		t.Fatal("remote principal allowed")
	}
	if _, err = e.AbandonAfterInspection(context.Background(), p, r.ID, r.Version, "yes"); !errors.Is(err, ErrDenied) {
		t.Fatal("implicit confirmation allowed")
	}
	out, err := e.AbandonAfterInspection(context.Background(), p, r.ID, r.Version, StopConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != Cancelled || out.Jobs[0].Status != JobCancelled || out.Notices[0].Status != DeliveryUnknown || ReservedTokens(out) != 120 || !ValidStopResolution(r, out) {
		t.Fatal("abandon forged success or refunded unknown work")
	}
	if _, err = e.AbandonAfterInspection(context.Background(), p, r.ID, r.Version, StopConfirmation); !errors.Is(err, ErrConflict) {
		t.Fatal("replayed operator resolution")
	}
}

func TestOperatorAbandonExternalScopeRecordsLocalActor(t *testing.T) {
	e := testEngine()
	ctx := context.Background()
	g := testGrant(t)
	p := Principal{Scope: "alice", Owner: "alice", LocalAdmin: true}
	r, _, err := e.Start(ctx, p, "external-abandon", "fixture", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err = e.mutate(ctx, r.Scope, r.ID, func(n *Run) error { n.Status = NeedsAttention; return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Principal{{Scope: "alice", Owner: "alice"}, {Scope: "alice", Owner: "bob", LocalAdmin: true}, {Scope: "bob", Owner: "bob", LocalAdmin: true}} {
		if _, err = e.AbandonAfterInspection(ctx, bad, r.ID, r.Version, StopConfirmation); !errors.Is(err, ErrDenied) {
			t.Fatalf("untrusted principal accepted: %v", err)
		}
	}
	out, err := e.AbandonAfterInspection(ctx, p, r.ID, r.Version, StopConfirmation)
	if err != nil || out.Resolution == nil || out.Resolution.Actor != LocalOperatorActor || out.Resolution.Actor == r.Owner || !ValidStopResolution(r, out) {
		t.Fatalf("actor/proof %+v %v", out.Resolution, err)
	}
	if r.Jobs[0].Status != JobQueued {
		t.Fatal("transition mutated its input")
	}
}

func TestOperatorStopProofRejectsEvidenceAndIdentityTampering(t *testing.T) {
	old := Run{ID: "run", Scope: "alice", Owner: "alice", Status: NeedsAttention, Version: 4, UpdatedAtMS: 100,
		Jobs:         []Job{{Status: JobUnknown, Claim: Claim{Token: "old-claim", Epoch: 1}}},
		Reservations: []Reservation{{ID: "unknown", Input: 7, Output: 9}},
		Notices:      []Notice{{Status: DeliverySending, Claim: Claim{Token: "old-sender"}}}}
	valid := stoppedAfterInspection(old, 110)
	valid.Version = 5
	valid.UpdatedAtMS = 111
	if !ValidStopResolution(old, valid) {
		t.Fatal("canonical proof rejected")
	}
	for _, tc := range []struct {
		name   string
		change func(*Run)
	}{
		{"external-owner-actor", func(r *Run) { r.Resolution.Actor = "alice" }},
		{"other-scope", func(r *Run) { r.Scope = "bob" }},
		{"other-owner", func(r *Run) { r.Owner = "bob" }},
		{"other-id", func(r *Run) { r.ID = "another" }},
		{"old-version", func(r *Run) { r.Resolution.PreviousVersion-- }},
		{"unversioned", func(r *Run) { r.Version-- }},
		{"retroactive", func(r *Run) { r.Resolution.AtMS = 90 }},
		{"future-proof", func(r *Run) { r.Resolution.AtMS = 112 }},
		{"refund", func(r *Run) { r.Reservations[0].Settled = true }},
		{"erase-budget", func(r *Run) { r.Reservations = nil }},
		{"rewrite-claim", func(r *Run) { r.Jobs[0].Claim.Token = "new" }},
		{"fake-success", func(r *Run) { r.Jobs[0].Status = JobSucceeded }},
		{"fake-result", func(r *Run) { r.Jobs[0].Result.Text = "claimed success" }},
		{"fake-delivery", func(r *Run) { r.Notices[0].Status = DeliverySent }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := stoppedAfterInspection(old, 110)
			candidate.Version = 5
			candidate.UpdatedAtMS = 111
			candidate.Reservations = append([]Reservation(nil), old.Reservations...)
			tc.change(&candidate)
			if ValidStopResolution(old, candidate) {
				t.Fatal("forged proof accepted")
			}
		})
	}
}
