package sqlitestore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
)

func TestControlWorkspaceCompetitionAcrossScopesAndStores(t *testing.T) {
	first, path := openControlTestStore(t)
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	a := createControlRun(t, first, controlFixture("a", "scope-a", "/work/tree", true))
	b := createControlRun(t, second, controlFixture("b", "scope-b", "/work/tree/child", true))
	claims := []control.Run{claimControlRun(a, "worker-a"), claimControlRun(b, "worker-b")}
	stores := []*Store{first, second}
	errs := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range claims {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := stores[i].SaveRun(context.Background(), 1, claims[i])
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	wins, busy := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, control.ErrResourceBusy):
			busy++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || busy != 1 || controlCount(t, first, "super_resources") != 1 {
		t.Fatalf("writers=%d busy=%d", wins, busy)
	}
	if controlEventCount(t, first) != 3 {
		t.Fatal("rejected claim produced event")
	}
}
func TestControlWorkspaceReadAndWriteCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, a, b   string
		wa, wb, busy bool
	}{
		{"parallel-read", "/work", "/work/child", false, false, false},
		{"writer-reader", "/work", "/work/child", true, false, true},
		{"reader-writer", "/work/child", "/work", false, true, true},
		{"separate-roots", "/work/a", "/work/b", true, true, false},
		{"prefix-boundary", "/work/a", "/work/ab", true, true, false},
		{"filesystem-root", "/", "/work", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openControlTestStore(t)
			a := createControlRun(t, s, controlFixture("a", "scope-a", tc.a, tc.wa))
			b := createControlRun(t, s, controlFixture("b", "scope-b", tc.b, tc.wb))
			saveControlRun(t, s, claimControlRun(a, "a"))
			_, err := s.SaveRun(context.Background(), 1, claimControlRun(b, "b"))
			if tc.busy && !errors.Is(err, control.ErrResourceBusy) {
				t.Fatalf("expected busy: %v", err)
			}
			if !tc.busy && err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestControlUnknownAndExpiredWriterKeepResource(t *testing.T) {
	s, path := openControlTestStore(t)
	ctx := context.Background()
	a := createControlRun(t, s, controlFixture("a", "scope-a", "/work", true))
	b := createControlRun(t, s, controlFixture("b", "scope-b", "/work/sub", true))
	claim := claimControlRun(a, "writer")
	claim.Jobs[0].Claim.UntilMS = time.Now().Add(120 * time.Millisecond).UnixMilli()
	a = saveControlRun(t, s, claim)
	time.Sleep(time.Until(time.UnixMilli(a.Jobs[0].Claim.UntilMS)) + 5*time.Millisecond)
	if err := s.CheckClaim(ctx, a.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("expired claim accepted: %v", err)
	}
	if _, err := s.SaveRun(ctx, b.Version, claimControlRun(b, "replacement")); !errors.Is(err, control.ErrResourceBusy) {
		t.Fatalf("expired writer replaced: %v", err)
	}
	n := nextControlRun(a)
	n.Status = control.NeedsAttention
	n.Jobs[0].Status = control.JobUnknown
	a = saveControlRun(t, s, n)
	active, err := s.ActiveRuns(ctx)
	if err != nil || len(active) != 2 {
		t.Fatalf("terminal unknown disappeared: %d %v", len(active), err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.SaveRun(ctx, b.Version, claimControlRun(b, "replacement")); !errors.Is(err, control.ErrResourceBusy) {
		t.Fatalf("restart lost unknown lock: %v", err)
	}
	n = nextControlRun(a)
	n.Jobs[0].Status = control.JobFailed
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	if _, err = s.SaveRun(ctx, a.Version, n); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("no stop proof released unknown: %v", err)
	}
	n = nextControlRun(a)
	n.Jobs[0].Claim.Token = "replacement-token"
	n.Jobs[0].Claim.Epoch++
	if _, err = s.SaveRun(ctx, a.Version, n); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("unknown fence replaced: %v", err)
	}
	if controlCount(t, s, "super_resources") != 1 {
		t.Fatal("lock disappeared")
	}
}
func TestControlTerminalParentRetainsLiveWriterUntilOwnerFinishes(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	a := createControlRun(t, s, controlFixture("a", "scope-a", "/work", true))
	b := createControlRun(t, s, controlFixture("b", "scope-b", "/work", true))
	a = saveControlRun(t, s, claimControlRun(a, "writer"))
	n := nextControlRun(a)
	n.Status = control.NeedsAttention
	a = saveControlRun(t, s, n)
	if _, err := s.SaveRun(ctx, b.Version, claimControlRun(b, "other")); !errors.Is(err, control.ErrResourceBusy) {
		t.Fatalf("terminal released writer: %v", err)
	}
	if err := s.CheckClaim(ctx, a.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("terminal tool authority retained: %v", err)
	}
	n = nextControlRun(a)
	n.Jobs[0].Status = control.JobFailed
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	a = saveControlRun(t, s, n)
	if controlCount(t, s, "super_resources") != 0 {
		t.Fatal("finished owner lock not released")
	}
	saveControlRun(t, s, claimControlRun(b, "other"))
}
func TestControlResourceReleaseFailureIsAtomic(t *testing.T) {
	s, _ := openControlTestStore(t)
	r := createControlRun(t, s, controlFixture("a", "tenant", "/work", true))
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	if _, err := s.db.Exec(`CREATE TRIGGER fail_control_release BEFORE INSERT ON super_events WHEN NEW.topic='super.run.changed' BEGIN SELECT RAISE(ABORT,'failure'); END`); err != nil {
		t.Fatal(err)
	}
	n := nextControlRun(r)
	n.Jobs[0].Status = control.JobSucceeded
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	n.Status = control.Verifying
	if _, err := s.SaveRun(context.Background(), r.Version, n); !errors.Is(err, control.ErrUnavailable) {
		t.Fatal(err)
	}
	if controlCount(t, s, "super_resources") != 1 {
		t.Fatal("failed settlement released lock")
	}
	if err := s.CheckClaim(context.Background(), r.Jobs[0].Claim); err != nil {
		t.Fatal(err)
	}
}
func TestControlResourceProjectionCorruptionFailsClosed(t *testing.T) {
	s, _ := openControlTestStore(t)
	r := createControlRun(t, s, controlFixture("a", "tenant", "/work", true))
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	if _, err := s.db.Exec("DELETE FROM super_resources"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckClaim(context.Background(), r.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.SaveRun(context.Background(), r.Version, nextControlRun(r)); !errors.Is(err, control.ErrUnavailable) {
		t.Fatalf("silently recreated authority: %v", err)
	}
}

func TestControlClaimRejectsCorruptResourceScope(t *testing.T) {
	s, _ := openControlTestStore(t)
	r := createControlRun(t, s, controlFixture("corrupt-resource", "tenant", "/work", true))
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	if _, err := s.db.Exec("UPDATE super_resources SET workspace='/different-workspace'"); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckClaim(context.Background(), r.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("resource mismatch allowed side effect: %v", err)
	}
}
