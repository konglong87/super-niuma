package sqlitestore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

func TestControlClaimFencesAndCancellation(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := createControlRun(t, s, controlFixture("claims", "tenant", "/work", true))
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	c := r.Jobs[0].Claim
	if err := s.CheckClaim(ctx, c); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*control.Claim){func(c *control.Claim) { c.Scope = "wrong" }, func(c *control.Claim) { c.RunID = "wrong" }, func(c *control.Claim) { c.JobID = "wrong" }, func(c *control.Claim) { c.Owner = "wrong" }, func(c *control.Claim) { c.Token = "wrong" }, func(c *control.Claim) { c.Epoch++ }, func(c *control.Claim) { c.Epoch = 0 }, func(c *control.Claim) { c.UntilMS = 0 }} {
		bad := c
		change(&bad)
		if err := s.CheckClaim(ctx, bad); err == nil {
			t.Fatalf("invalid claim accepted: %#v", bad)
		}
	}
	n := nextControlRun(r)
	n.Jobs[0].Claim.UntilMS += 1000
	r = saveControlRun(t, s, n)
	if err := s.CheckClaim(ctx, c); err != nil {
		t.Fatalf("renewed original token rejected: %v", err)
	}
	n = nextControlRun(r)
	n.Status = control.Cancelling
	r = saveControlRun(t, s, n)
	if err := s.CheckClaim(ctx, c); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("cancel allowed side effect: %v", err)
	}
	n = nextControlRun(r)
	n.Status = control.Cancelled
	n.Jobs[0].Status = control.JobCancelled
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	saveControlRun(t, s, n)
	if controlCount(t, s, "super_resources") != 0 {
		t.Fatal("cancelled valid owner did not release lock")
	}
}
func TestControlClaimGrantExpiry(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := controlFixture("expiry", "tenant", "/work", true)
	r.Grant.ExpiresAtMS = time.Now().Add(120 * time.Millisecond).UnixMilli()
	r.GrantHash = control.Digest(r.Grant)
	r = createControlRun(t, s, r)
	r = saveControlRun(t, s, claimControlRun(r, "worker"))
	time.Sleep(time.Until(time.UnixMilli(r.Grant.ExpiresAtMS)) + 5*time.Millisecond)
	if err := s.CheckClaim(ctx, r.Jobs[0].Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("expired grant accepted: %v", err)
	}
	n := nextControlRun(r)
	n.Jobs[0].Claim.UntilMS += 1000
	if _, err := s.SaveRun(ctx, r.Version, n); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("expired grant renewed: %v", err)
	}
}
func verifiedControlRun(t *testing.T, s *Store, id, scope, workspace string) control.Run {
	t.Helper()
	r := createControlRun(t, s, controlFixture(id, scope, workspace, true))
	r = saveControlRun(t, s, claimControlRun(r, "writer"))
	n := nextControlRun(r)
	n.Jobs[0].Status = control.JobSucceeded
	n.Jobs[0].FinishedAtMS = time.Now().UnixMilli()
	n.Status = control.Verifying
	return saveControlRun(t, s, n)
}
func claimControlVerification(r control.Run) control.Run {
	n := nextControlRun(r)
	n.VerificationClaim = control.Claim{Scope: r.Scope, RunID: r.ID, JobID: controlVerifyJob, Owner: "verifier", Token: "verification-token", Epoch: 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	return n
}
func TestControlVerificationReadLockAndSpecialClaims(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := verifiedControlRun(t, s, "verified", "tenant", "/work")
	r = saveControlRun(t, s, claimControlVerification(r))
	if err := s.CheckClaim(ctx, r.VerificationClaim); err != nil {
		t.Fatalf("verification lookup: %v", err)
	}
	writer := createControlRun(t, s, controlFixture("writer", "other-tenant", "/work/sub", true))
	if _, err := s.SaveRun(ctx, writer.Version, claimControlRun(writer, "other")); !errors.Is(err, control.ErrResourceBusy) {
		t.Fatalf("writer entered verification: %v", err)
	}
	reader := createControlRun(t, s, controlFixture("reader", "read-tenant", "/work", false))
	saveControlRun(t, s, claimControlRun(reader, "reader"))
	n := nextControlRun(r)
	n.VerificationClaim = control.Claim{}
	n.Verification = &verification.Report{Passed: true, PolicyHash: verification.PolicyHash(r.Grant.Verification), VerifiedAtMS: time.Now().UnixMilli()}
	n.Status = control.DeliveryPending
	n.Notices = []control.Notice{{ID: "notice", Target: r.Grant.Targets[0], Status: control.DeliveryQueued}}
	r = saveControlRun(t, s, n)
	n = nextControlRun(r)
	n.Notices[0].Status = control.DeliverySending
	n.Notices[0].Attempts = 1
	n.Notices[0].Claim = control.Claim{Scope: r.Scope, RunID: r.ID, JobID: controlDeliveryPrefix + r.Grant.Targets[0].ID, Owner: "sender", Token: "send-token", Epoch: 1, UntilMS: time.Now().Add(time.Minute).UnixMilli()}
	r = saveControlRun(t, s, n)
	if err := s.CheckClaim(ctx, r.Notices[0].Claim); err != nil {
		t.Fatalf("delivery target lookup: %v", err)
	}
	wrong := r.Notices[0].Claim
	wrong.JobID = controlDeliveryPrefix + r.Notices[0].ID
	if err := s.CheckClaim(ctx, wrong); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("notice ID confused with target ID: %v", err)
	}
	wrong = r.Notices[0].Claim
	wrong.Epoch++
	if err := s.CheckClaim(ctx, wrong); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("stale delivery epoch: %v", err)
	}
}
func TestControlVerifierCannotEnterLiveWriter(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	r := verifiedControlRun(t, s, "verified", "tenant", "/work")
	writer := createControlRun(t, s, controlFixture("writer", "other-tenant", "/work/sub", true))
	saveControlRun(t, s, claimControlRun(writer, "other"))
	if _, err := s.SaveRun(ctx, r.Version, claimControlVerification(r)); !errors.Is(err, control.ErrResourceBusy) {
		t.Fatalf("verification entered live writer: %v", err)
	}
}
