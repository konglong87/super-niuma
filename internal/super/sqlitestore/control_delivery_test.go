package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

func controlDeliveryCandidate(r control.Run) control.Run {
	n := nextControlRun(r)
	n.VerificationClaim = control.Claim{}
	n.Verification = &verification.Report{Passed: true, PolicyHash: verification.PolicyHash(r.Grant.Verification), VerifiedAtMS: time.Now().UnixMilli()}
	n.Status = control.DeliveryPending
	n.Notices = []control.Notice{{ID: "notice.desktop", Target: r.Grant.Targets[0], Status: control.DeliveryQueued}}
	return n
}
func controlTopicCount(t *testing.T, s *Store, topic string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM super_events WHERE topic=?", topic).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestControlDeliveryReadyCommitsWithStateAndChangeEvent(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := verifiedControlRun(t, s, "delivery", "tenant", "/work")
	r = saveControlRun(t, s, claimControlVerification(r))
	before := controlEventCount(t, s)
	n := controlDeliveryCandidate(r)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_delivery_ready BEFORE INSERT ON super_events WHEN NEW.topic='super.delivery.ready' BEGIN SELECT RAISE(ABORT,'SECRET'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRun(ctx, r.Version, n); !errors.Is(err, control.ErrUnavailable) {
		t.Fatalf("delivery event failure: %v", err)
	}
	got, err := s.GetRun(ctx, r.Scope, r.ID)
	if err != nil || got.Version != r.Version || got.Status != control.Verifying || controlCount(t, s, "super_resources") != 1 {
		t.Fatalf("partial delivery transition: %v", err)
	}
	if controlEventCount(t, s) != before || controlTopicCount(t, s, control.EventDeliveryReady) != 0 {
		t.Fatal("partial dual-event transaction")
	}
	if _, err = s.db.Exec("DROP TRIGGER fail_delivery_ready"); err != nil {
		t.Fatal(err)
	}
	r = saveControlRun(t, s, n)
	events, err := s.ReadEvents(ctx, r.Scope, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatal("missing events")
	}
	changed, ready := events[len(events)-2], events[len(events)-1]
	if changed.Topic != control.EventRunChanged || ready.Topic != control.EventDeliveryReady || changed.ID == ready.ID || ready.ID != controlRunEvent(r, control.EventDeliveryReady).ID || string(ready.Payload) != `{"run_id":"delivery"}` {
		t.Fatalf("wrong delivery signal: %#v %#v", changed, ready)
	}
	if _, err = s.SaveRun(ctx, r.Version-1, r); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("CAS retry accepted: %v", err)
	}
	n = nextControlRun(r)
	n.SafeCode = "metadata_only"
	r = saveControlRun(t, s, n)
	if controlTopicCount(t, s, control.EventDeliveryReady) != 1 {
		t.Fatal("metadata/CAS retry duplicated delivery signal")
	}
	// A real dispatch claim is an outbox status transition, not a new delivery
	// obligation; its event cannot retrigger materialization of the same notice.
	n = nextControlRun(r)
	n.Notices[0].Status = control.DeliverySending
	n.Notices[0].Attempts = 1
	n.Notices[0].Claim = control.Claim{Scope: r.Scope, RunID: r.ID, JobID: controlDeliveryPrefix + "desktop", Owner: "sender", Token: "send-token", Epoch: 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	r = saveControlRun(t, s, n)
	n = nextControlRun(r)
	n.Notices[0].Status = control.DeliverySent
	n.Notices[0].Receipt = &control.Receipt{MessageID: "receipt", DeliveredAtMS: time.Now().UnixMilli()}
	n.Notices[0].Claim = control.Claim{}
	n.Status = control.Delivered
	saveControlRun(t, s, n)
	if controlTopicCount(t, s, control.EventDeliveryReady) != 1 {
		t.Fatal("dispatch/settlement emitted another ready signal")
	}
}
func TestControlNewNoticeEmitsOneAdditionalDeliveryReady(t *testing.T) {
	s, _ := openControlTestStore(t)
	base := controlFixture("additional", "tenant", "/work", true)
	base.Grant.Targets = append(base.Grant.Targets, control.Target{ID: "other-desktop", Provider: control.ProviderDesktop})
	base.GrantHash = control.Digest(base.Grant)
	r := createControlRun(t, s, base)
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	n := nextControlRun(r)
	n.Jobs[0].Status = control.JobSucceeded
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	n.Status = control.Verifying
	r = saveControlRun(t, s, n)
	r = saveControlRun(t, s, claimControlVerification(r))
	r = saveControlRun(t, s, controlDeliveryCandidate(r))
	n = nextControlRun(r)
	n.Notices = append(n.Notices, control.Notice{ID: "notice.second", Target: r.Grant.Targets[1], Status: control.DeliveryQueued})
	r = saveControlRun(t, s, n)
	if controlTopicCount(t, s, control.EventDeliveryReady) != 2 {
		t.Fatal("new notice did not publish durable readiness")
	}
	n = nextControlRun(r)
	n.Notices[0].SafeCode = "metadata"
	saveControlRun(t, s, n)
	if controlTopicCount(t, s, control.EventDeliveryReady) != 2 {
		t.Fatal("unchanged notices amplified readiness")
	}
}
