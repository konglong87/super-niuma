package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/platform"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/verification"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func TestSuperPlatformUnconfiguredKeepsIntakeAndNoExecution(t *testing.T) {
	dir, err := os.MkdirTemp(".", ".super-platform-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	abs, _ := filepath.Abs(dir)
	t.Setenv(super.StateDirectoryEnv, filepath.Join(abs, "super"))
	a := &app{}
	defer a.superWorkOrders.close()
	snap, err := a.GetSuperPlatform()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Configured || snap.Running || len(snap.Runs) != 0 || snap.SafeCode != "configuration_required" {
		t.Fatalf("unexpected snapshot %+v", snap)
	}
	order, err := a.CreateSuperWorkOrder("intake", workorder.CreateInput{Title: "fixture", Objective: "no execution", AcceptanceCriteria: []string{"no model call"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.StartSuperRun(order.ID, "default", "start"); !errors.Is(err, control.ErrUnavailable) {
		t.Fatalf("unconfigured start %v", err)
	}
	events, err := a.GetSuperEvents(0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events %d %v", len(events), err)
	}
	var config map[string]any
	if err = json.Unmarshal([]byte(snap.ConfigJSON), &config); err != nil {
		t.Fatal(err)
	}
	config["api_key"] = "never-store-this"
	bad, _ := json.Marshal(config)
	if _, err = a.SaveSuperPlatformConfig(string(bad)); !errors.Is(err, control.ErrInvalid) {
		t.Fatalf("raw credentials accepted %v", err)
	}
	snap, err = a.GetSuperPlatform()
	if err != nil || snap.Configured || len(snap.Runs) != 0 {
		t.Fatalf("phantom control state %v", err)
	}
	a.superWorkOrders.close()
	if _, err = a.GetSuperPlatform(); !errors.Is(err, workorder.ErrUnavailable) {
		t.Fatalf("closed bridge reopened %v", err)
	}
}

type desktopOperatorAuthority struct {
	seen    []control.Principal
	revoked bool
}

func (f *desktopOperatorAuthority) Current(_ context.Context, p control.Principal, _ control.Grant) error {
	f.seen = append(f.seen, p)
	if f.revoked || p.Owner != p.Scope {
		return control.ErrDenied
	}
	return nil
}
func desktopOperatorFixture(t *testing.T) (*app, *platform.Service, *sqlitestore.Store, *desktopOperatorAuthority, control.Grant) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(super.StateDirectoryEnv, filepath.Join(root, "super"))
	a := &app{}
	t.Cleanup(a.superWorkOrders.close)
	if _, err = a.GetSuperPlatform(); err != nil {
		t.Fatal(err)
	}
	s := a.superWorkOrders.platform
	store, ok := a.superWorkOrders.store.(*sqlitestore.Store)
	if !ok {
		t.Fatal("requires real SQLite")
	}
	authority := &desktopOperatorAuthority{}
	s.Engine.Authority = authority
	g := control.Grant{ProfileID: "fixture", Revision: "v1", Workspace: root, Provider: "fixture", Model: "fixture-model", MaxTokens: 10000, TimeoutMS: 10000, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Nodes: []control.Node{{ID: "job", Instruction: "Do not execute", ReadPaths: []string{"inputs"}, MaxTurns: 1, MaxOutputTokens: 64}}, Verification: verification.Policy{Version: "v1", Rules: []verification.Rule{{Path: "artifacts/result.txt", Kind: verification.KindTextContains, Expected: "done"}}}, Targets: []control.Target{{ID: "desktop", Provider: control.ProviderDesktop}}}
	return a, s, store, authority, g
}

func TestSuperPlatformDesktopOperatorScopedApproveCancel(t *testing.T) {
	for _, scope := range []string{"alice", control.LocalScope} {
		t.Run(scope, func(t *testing.T) {
			a, s, store, authority, g := desktopOperatorFixture(t)
			ctx := context.Background()
			intakeID := ""
			if scope == control.LocalScope {
				order, err := a.CreateSuperWorkOrder("local-intake", workorder.CreateInput{Title: "local", Objective: "offline fixture", AcceptanceCriteria: []string{"no model"}})
				if err != nil {
					t.Fatal(err)
				}
				intakeID = order.ID
			}
			r, created, err := s.Engine.Start(ctx, control.Principal{Scope: scope, Owner: scope}, "run", "offline task", intakeID, g, nil)
			if err != nil || !created {
				t.Fatalf("create %v %v", created, err)
			}
			snap, err := a.GetSuperPlatform()
			if err != nil || len(snap.Runs) != 1 || snap.Runs[0].ID != r.ID || snap.Runs[0].Status != control.AwaitingApproval {
				t.Fatalf("snapshot=%+v err=%v", snap, err)
			}
			if _, err = a.ApproveSuperRun(r.ID, r.Version+1, r.GrantHash); !errors.Is(err, control.ErrConflict) {
				t.Fatal("stale approval accepted", err)
			}
			approved, err := a.ApproveSuperRun(r.ID, r.Version, r.GrantHash)
			if err != nil || approved.Scope != scope || approved.Owner != scope || approved.Status != control.Queued {
				t.Fatalf("approval=%+v err=%v", approved, err)
			}
			principal := authority.seen[len(authority.seen)-1]
			if !principal.LocalAdmin || principal.Scope != scope || principal.Owner != scope {
				t.Fatalf("wrong principal %+v", principal)
			}
			// No current profile/binding exists; revoked execution authority is irrelevant
			// to finding and cancelling already-persisted work.
			authority.revoked = true
			// Desktop cancellation remains best-effort even if the snapshot version is stale.
			cancelled, err := a.CancelSuperRun(r.ID, r.Version)
			if err != nil || cancelled.Status != control.Cancelled {
				t.Fatalf("cancel=%+v err=%v", cancelled, err)
			}
			saved, err := store.GetRun(ctx, scope, r.ID)
			if err != nil || saved.Status != control.Cancelled {
				t.Fatal("cancel not durable", err)
			}
		})
	}
}

func TestSuperPlatformDesktopOperatorCollisionFailsClosed(t *testing.T) {
	a, s, store, _, g := desktopOperatorFixture(t)
	ctx := context.Background()
	r, _, err := s.Engine.Start(ctx, control.Principal{Scope: "alice", Owner: "alice"}, "same-id", "offline collision", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	twin := r
	twin.Scope = "bob"
	twin.Owner = "bob"
	if _, created, err := store.CreateRun(ctx, twin, nil); err != nil || !created {
		t.Fatalf("collision fixture %v %v", created, err)
	}
	if _, err = a.GetSuperPlatform(); !errors.Is(err, control.ErrConflict) {
		t.Fatal("ambiguous snapshot accepted", err)
	}
	for _, op := range []func() (control.Run, error){
		func() (control.Run, error) { return a.ApproveSuperRun(r.ID, r.Version, r.GrantHash) },
		func() (control.Run, error) { return a.CancelSuperRun(r.ID, r.Version) },
		func() (control.Run, error) { return a.RetrySuperRun(r.ID, r.Version) },
		func() (control.Run, error) { return a.AbandonSuperRun(r.ID, r.Version, control.StopConfirmation) },
	} {
		if _, err := op(); !errors.Is(err, control.ErrConflict) {
			t.Fatal("ambiguous mutation accepted", err)
		}
	}
	for _, scope := range []string{"alice", "bob"} {
		saved, err := store.GetRun(ctx, scope, r.ID)
		if err != nil || saved.Version != r.Version || saved.Status != control.AwaitingApproval {
			t.Fatal("collision mutated run", err)
		}
	}
}

func TestSuperPlatformDesktopOperatorRetryKeepsGrantAndScope(t *testing.T) {
	a, s, store, authority, g := desktopOperatorFixture(t)
	ctx := context.Background()
	r, _, err := s.Engine.Start(ctx, control.Principal{Scope: "alice", Owner: "alice"}, "retry", "offline retry", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err = a.ApproveSuperRun(r.ID, r.Version, r.GrantHash)
	if err != nil {
		t.Fatal(err)
	}
	previous := r.Version
	r.Version++
	r.Status = control.Failed
	r.SafeCode = "fixture_preflight_failed"
	r.UpdatedAtMS = time.Now().UnixMilli()
	r, err = store.SaveRun(ctx, previous, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.RetrySuperRun(r.ID, r.Version-1); !errors.Is(err, control.ErrConflict) {
		t.Fatal("stale retry accepted", err)
	}
	authority.revoked = true
	if _, err = a.RetrySuperRun(r.ID, r.Version); !errors.Is(err, control.ErrDenied) {
		t.Fatal("retry bypassed grant", err)
	}
	authority.revoked = false
	retried, err := a.RetrySuperRun(r.ID, r.Version)
	if err != nil || retried.Status != control.Queued || retried.Scope != "alice" || retried.GrantHash != r.GrantHash {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	p := authority.seen[len(authority.seen)-1]
	if !p.LocalAdmin || p.Scope != "alice" || p.Owner != "alice" {
		t.Fatal("wrong retry principal")
	}
}

func TestSuperPlatformDesktopExternalAbandonRecordsLocalOperator(t *testing.T) {
	a, s, store, _, g := desktopOperatorFixture(t)
	ctx := context.Background()
	r, _, err := s.Engine.Start(ctx, control.Principal{Scope: "alice", Owner: "alice"}, "abandon", "offline stopped fixture", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err = a.ApproveSuperRun(r.ID, r.Version, r.GrantHash)
	if err != nil {
		t.Fatal(err)
	}
	previous := r.Version
	r.Version++
	r.Status = control.NeedsAttention
	r.UpdatedAtMS = time.Now().UnixMilli()
	r, err = store.SaveRun(ctx, previous, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Engine.AbandonAfterInspection(ctx, control.Principal{Scope: "alice", Owner: "alice"}, r.ID, r.Version, control.StopConfirmation); !errors.Is(err, control.ErrDenied) {
		t.Fatal("nonadmin abandoned", err)
	}
	if _, err = a.AbandonSuperRun(r.ID, r.Version, "yes"); !errors.Is(err, control.ErrDenied) {
		t.Fatal("implicit confirmation accepted", err)
	}
	if _, err = a.AbandonSuperRun(r.ID, r.Version-1, control.StopConfirmation); !errors.Is(err, control.ErrConflict) {
		t.Fatal("stale inspection accepted", err)
	}
	out, err := a.AbandonSuperRun(r.ID, r.Version, control.StopConfirmation)
	if err != nil || out.Status != control.Cancelled || out.Resolution == nil || out.Resolution.Actor != control.LocalOperatorActor || out.Owner != "alice" || out.Scope != "alice" || !control.ValidStopResolution(r, out) {
		t.Fatalf("abandon=%+v err=%v", out, err)
	}
	saved, err := store.GetRun(ctx, "alice", r.ID)
	if err != nil || saved.Resolution == nil || saved.Resolution.Actor != control.LocalOperatorActor {
		t.Fatal("local operator proof not durable", err)
	}
}

func TestSuperPlatformDesktopCrossScopeNotificationEvidence(t *testing.T) {
	a, s, store, _, _ := desktopOperatorFixture(t)
	ctx := context.Background()
	const subscriber = "super.notices.v1"
	for _, scope := range []string{control.LocalScope, "alice", "bob"} {
		_, created, err := store.AppendEvent(ctx, eventbus.Event{ID: "notice-" + scope, Scope: scope, Topic: control.EventDeliveryReady, AggregateID: "run-" + scope, Version: eventbus.EventVersion, Payload: json.RawMessage(`{"run_id":"offline-fixture"}`), OccurredAtMS: time.Now().UnixMilli()})
		if err != nil || !created {
			t.Fatalf("event fixture %v %v", created, err)
		}
		err = store.RegisterSubscription(ctx, eventbus.Subscription{Scope: scope, Name: subscriber, Topics: []string{control.EventDeliveryReady}, Start: eventbus.StartBeginning, MaxAttempts: 1})
		if err != nil {
			t.Fatal(err)
		}
		d, found, err := store.ClaimEvent(ctx, scope, subscriber, "stopped-fixture", time.Minute)
		if err != nil || !found {
			t.Fatalf("claim %v %v", found, err)
		}
		if err = store.NackEvent(ctx, d.Token, eventbus.CodeConsumerFailed, 0); err != nil {
			t.Fatal(err)
		}
	}
	// There are no configured profiles/bindings; durable evidence remains visible.
	for i := 0; i < 2; i++ {
		snap, err := a.GetSuperPlatform()
		if err != nil || snap.Configured || len(snap.DeadLetters) != 3 || snap.DeadLetters[0].Scope != "bob" || snap.DeadLetters[2].Scope != control.LocalScope {
			t.Fatalf("global snapshot evidence=%+v err=%v", snap.DeadLetters, err)
		}
	}
	events, err := a.GetSuperEvents(0)
	if err != nil || len(events) != 3 || events[0].Scope != control.LocalScope || events[1].Scope != "alice" || events[2].Scope != "bob" {
		t.Fatalf("global events=%+v err=%v", events, err)
	}
	tail, err := a.GetSuperEvents(events[0].Sequence)
	if err != nil || len(tail) != 2 || tail[0].Scope != "alice" {
		t.Fatal("global event cursor incorrect", err)
	}
	if _, err = a.GetSuperEvents(-1); !errors.Is(err, control.ErrInvalid) {
		t.Fatal("negative event cursor accepted", err)
	}
	scoped, err := store.ReadEvents(ctx, control.LocalScope, 0, eventbus.MaxReadLimit)
	if err != nil || len(scoped) != 1 {
		t.Fatal("scoped event port broadened", err)
	}
	platformEvents, err := s.Events(ctx, events[1].Sequence)
	if err != nil || len(platformEvents) != 1 || platformEvents[0].Scope != "bob" {
		t.Fatal("platform operator events failed", err)
	}
	for _, scope := range []string{control.LocalScope, "alice", "bob"} {
		if _, found, err := store.ClaimEvent(ctx, scope, subscriber, "stopped-fixture", time.Minute); err != nil || found {
			t.Fatal("evidence read replayed notification", err)
		}
	}
}
