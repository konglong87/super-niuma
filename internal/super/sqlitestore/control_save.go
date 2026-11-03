package sqlitestore

import (
	"context"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
)

func (s *Store) SaveRun(ctx context.Context, expected uint64, r control.Run) (control.Run, error) {
	if err := validateControlRecord(r); err != nil {
		return control.Run{}, err
	}
	if expected == 0 || r.Version != expected+1 {
		return control.Run{}, control.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	defer tx.Rollback()
	old, err := controlRunTx(ctx, tx, r.Scope, r.ID)
	if err != nil {
		return control.Run{}, err
	}
	if old.Version != expected {
		return control.Run{}, control.ErrConflict
	}
	if old.Owner != r.Owner {
		return control.Run{}, control.ErrDenied
	}
	if !control.SameIntent(old, r) || old.CreatedAtMS != r.CreatedAtMS || r.UpdatedAtMS < old.UpdatedAtMS {
		return control.Run{}, control.ErrConflict
	}
	now := time.Now().UnixMilli()
	if err = validateControlTransition(old, r, now); err != nil {
		return control.Run{}, err
	}
	if !controlOccupiesCapacity(old) && controlOccupiesCapacity(r) {
		var active int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_runs WHERE "+controlActivePredicate, control.Delivered, control.Cancelled, control.Failed, control.NeedsAttention).Scan(&active); err != nil {
			return control.Run{}, controlStorageError(ctx, err)
		}
		if active >= control.MaxRuns {
			return control.Run{}, control.ErrCapacity
		}
	}
	if controlStartsOrRenewsClaim(old, r) {
		if err = controlIntake(ctx, tx, r); err != nil {
			return control.Run{}, err
		}
	}
	if err = controlSyncResources(ctx, tx, old, r, now); err != nil {
		return control.Run{}, err
	}
	data, err := controlJSON(r)
	if err != nil {
		return control.Run{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE super_runs SET revision=?,status=?,data=? WHERE scope=? AND id=? AND owner=? AND revision=?`, r.Version, r.Status, data, r.Scope, r.ID, r.Owner, expected)
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	if count != 1 {
		return control.Run{}, control.ErrConflict
	}
	if controlStateChanged(old, r) {
		if err = controlAppendChanged(ctx, tx, r); err != nil {
			return control.Run{}, err
		}
	}
	if controlDeliveryReady(old, r) {
		if err = controlAppendRunEvent(ctx, tx, r, control.EventDeliveryReady); err != nil {
			return control.Run{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	controlLog(ctx, "saved")
	return r, nil
}
func controlStateChanged(a, b control.Run) bool {
	if a.Status != b.Status || len(a.Jobs) != len(b.Jobs) || len(a.Notices) != len(b.Notices) {
		return true
	}
	for i, j := range a.Jobs {
		if j.Status != b.Jobs[i].Status {
			return true
		}
	}
	for i, n := range a.Notices {
		if n.Status != b.Notices[i].Status {
			return true
		}
	}
	return false
}
func controlStatusTransition(a, b control.Status) bool {
	if a == b {
		return true
	}
	switch a {
	case control.AwaitingApproval:
		return b == control.Queued || b == control.Cancelling || b == control.Cancelled
	case control.Queued:
		return b == control.Running || b == control.Cancelling || b == control.Cancelled || b == control.Failed || b == control.NeedsAttention
	case control.Running:
		return b == control.Verifying || b == control.Cancelling || b == control.Cancelled || b == control.Failed || b == control.NeedsAttention
	case control.Verifying:
		return b == control.Queued || b == control.DeliveryPending || b == control.Cancelling || b == control.Cancelled || b == control.Failed || b == control.NeedsAttention
	case control.DeliveryPending:
		return b == control.Delivered || b == control.Cancelling || b == control.Cancelled || b == control.Failed || b == control.NeedsAttention
	case control.Cancelling:
		return b == control.Cancelled || b == control.NeedsAttention
	case control.Failed:
		return b == control.Queued
	default:
		return false
	}
}

// controlExpiredFailureTransition permits only recovery of a failed root's
// expired running jobs. It neither authorizes another attempt nor releases their
// resource claims. All other evidence stays unchanged; a returning executor may
// add its typed diagnostic/usage without replacing receipt or result contents.
func controlExpiredFailureTransition(a, b control.Run, now int64) bool {
	if a.Status != control.Failed || b.Status != control.NeedsAttention || len(a.Jobs) != len(b.Jobs) {
		return false
	}
	expected := a
	expected.Status = control.NeedsAttention
	expected.SafeCode = "lease_lost"
	expected.Version = b.Version
	expected.UpdatedAtMS = b.UpdatedAtMS
	expected.Jobs = append([]control.Job(nil), a.Jobs...)
	expired := false
	for i, old := range a.Jobs {
		if old.Status != control.JobRunning || old.Claim.Token == "" || old.Claim.UntilMS <= 0 || old.Claim.UntilMS > now {
			continue
		}
		next := b.Jobs[i]
		// Recovery must not erase usage already observed before a crash.
		if (old.Result.UsageKnown && !next.Result.UsageKnown) || next.Result.Usage.InputTokens < old.Result.Usage.InputTokens || next.Result.Usage.OutputTokens < old.Result.Usage.OutputTokens {
			return false
		}
		expected.Jobs[i].Status = control.JobUnknown
		expected.Jobs[i].SafeCode = "lease_lost"
		expected.Jobs[i].Result.Diagnostic = next.Result.Diagnostic
		expected.Jobs[i].Result.Usage = next.Result.Usage
		expected.Jobs[i].Result.UsageKnown = next.Result.UsageKnown
		expired = true
	}
	return expired && control.Digest(expected) == control.Digest(b)
}

func validateControlTransition(a, b control.Run, now int64) error {
	resolved := control.ValidStopResolution(a, b)
	if control.Digest(a.Resolution) != control.Digest(b.Resolution) && !resolved {
		return control.ErrDenied
	}
	if !resolved && !controlStatusTransition(a.Status, b.Status) && !controlExpiredFailureTransition(a, b, now) {
		return control.ErrConflict
	}
	if a.Approved != b.Approved && !(a.Status == control.AwaitingApproval && b.Status == control.Queued && b.Approved) {
		return control.ErrDenied
	}
	for i, old := range a.Jobs {
		j := b.Jobs[i]
		if j.Attempts < old.Attempts || j.Attempts > old.Attempts+1 {
			return control.ErrConflict
		}
		if j.Status == old.Status {
			if j.Attempts != old.Attempts {
				return control.ErrConflict
			}
			continue
		}
		switch old.Status {
		case control.JobQueued:
			if j.Status != control.JobRunning && j.Status != control.JobCancelled {
				return control.ErrConflict
			}
			if j.Status == control.JobRunning {
				if j.Attempts != old.Attempts+1 || j.Claim.Epoch != old.Claim.Epoch+1 || !controlLiveRoot(b, now) {
					return control.ErrDenied
				}
				for _, dep := range j.Spec.DependsOn {
					for _, parent := range b.Jobs {
						if parent.Spec.ID == dep && parent.Status != control.JobSucceeded {
							return control.ErrDenied
						}
					}
				}
			}
		case control.JobRunning:
			if j.Status != control.JobSucceeded && j.Status != control.JobFailed && j.Status != control.JobCancelled && j.Status != control.JobUnknown {
				return control.ErrConflict
			}
			if j.Attempts != old.Attempts {
				return control.ErrConflict
			}
		case control.JobUnknown:
			if !resolved || j.Status != control.JobCancelled {
				return control.ErrDenied
			}
		case control.JobSucceeded, control.JobFailed, control.JobCancelled:
			if j.Status != control.JobQueued || (a.Status != control.Verifying && a.Status != control.Failed) || b.Status != control.Queued || j.Attempts != old.Attempts {
				return control.ErrConflict
			}
		default:
			return control.ErrInvalid
		}
	}
	return validateControlNotices(a, b, now)
}

func controlOccupiesCapacity(r control.Run) bool {
	if !control.IsTerminal(r.Status) || r.VerificationClaim.Token != "" {
		return true
	}
	for _, j := range r.Jobs {
		if j.Status == control.JobRunning || j.Status == control.JobUnknown {
			return true
		}
	}
	return false
}

func controlStartsOrRenewsClaim(a, b control.Run) bool {
	for i, j := range b.Jobs {
		if j.Status == control.JobRunning && (a.Jobs[i].Status != control.JobRunning || a.Jobs[i].Claim.UntilMS != j.Claim.UntilMS) {
			return true
		}
	}
	if b.VerificationClaim.Token != "" && (a.VerificationClaim.Token == "" || a.VerificationClaim.UntilMS != b.VerificationClaim.UntilMS) {
		return true
	}
	for _, n := range b.Notices {
		if n.Status != control.DeliverySending {
			continue
		}
		found := false
		for _, old := range a.Notices {
			if old.ID == n.ID && old.Status == control.DeliverySending && old.Claim.UntilMS == n.Claim.UntilMS {
				found = true
			}
		}
		if !found {
			return true
		}
	}
	return false
}

func controlDeliveryReady(a, b control.Run) bool {
	if b.Status != control.DeliveryPending || len(b.Notices) == 0 {
		return false
	}
	if a.Status != control.DeliveryPending {
		return true
	}
	old := map[string]bool{}
	for _, n := range a.Notices {
		old[n.ID] = true
	}
	for _, n := range b.Notices {
		if !old[n.ID] {
			return true
		}
	}
	return false
}
