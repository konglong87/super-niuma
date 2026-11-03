package sqlitestore

import (
	"context"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
)

func (s *Store) CheckClaim(ctx context.Context, c control.Claim) error {
	if !control.ValidID(c.Scope) || !control.ValidID(c.RunID) || !control.ValidID(c.JobID) || !control.ValidID(c.Owner) || !control.ValidID(c.Token) || c.Epoch == 0 || c.UntilMS <= 0 {
		return control.ErrDenied
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlStorageError(ctx, err)
	}
	defer tx.Rollback()
	r, err := controlRunTx(ctx, tx, c.Scope, c.RunID)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if !controlLiveRoot(r, now) {
		return control.ErrDenied
	}
	if err = controlIntake(ctx, tx, r); err != nil {
		return err
	}
	current, resource, ok := controlClaimFor(r, c.JobID)
	if !ok || !sameControlClaim(current, c) || current.UntilMS <= now {
		return control.ErrDenied
	}
	// The caller may hold the original deadline after a successful renewal.
	// The durable deadline is authoritative; owner/token/epoch must still match.
	if resource {
		mode := controlResourceRead
		for _, j := range r.Jobs {
			if j.Spec.ID == c.JobID && len(j.Spec.WritePaths) > 0 {
				mode = controlResourceWrite
			}
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM super_resources WHERE scope=? AND run_id=? AND job_id=? AND owner=? AND token=? AND epoch=? AND workspace=? AND mode=?`, c.Scope, c.RunID, c.JobID, c.Owner, c.Token, c.Epoch, r.Grant.Workspace, mode).Scan(&count); err != nil {
			return controlStorageError(ctx, err)
		}
		if count != 1 {
			return control.ErrDenied
		}
	}
	if err = tx.Commit(); err != nil {
		return controlStorageError(ctx, err)
	}
	controlLog(ctx, "claim_checked")
	return nil
}
func controlClaimFor(r control.Run, job string) (control.Claim, bool, bool) {
	if job == controlVerifyJob {
		return r.VerificationClaim, true, r.Status == control.Verifying && r.VerificationClaim.Token != ""
	}
	if strings.HasPrefix(job, controlDeliveryPrefix) {
		if r.Status != control.DeliveryPending || r.Verification == nil || !r.Verification.Passed {
			return control.Claim{}, false, false
		}
		target := strings.TrimPrefix(job, controlDeliveryPrefix)
		for _, n := range r.Notices {
			if n.Target.ID == target && n.Status == control.DeliverySending {
				return n.Claim, false, true
			}
		}
		return control.Claim{}, false, false
	}
	for _, j := range r.Jobs {
		if j.Spec.ID == job && j.Status == control.JobRunning {
			return j.Claim, true, true
		}
	}
	return control.Claim{}, false, false
}
