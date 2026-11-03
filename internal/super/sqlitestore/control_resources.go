package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/super/control"
)

type controlResource struct {
	scope, run, job, workspace, mode string
	claim                            control.Claim
}

func sameControlClaim(a, b control.Claim) bool {
	return a.Scope == b.Scope && a.RunID == b.RunID && a.JobID == b.JobID && a.Owner == b.Owner && a.Token != "" && a.Token == b.Token && a.Epoch == b.Epoch
}
func validControlClaim(c control.Claim, r control.Run, job string) bool {
	return c.Scope == r.Scope && c.RunID == r.ID && c.JobID == job && control.ValidID(c.Owner) && control.ValidID(c.Token) && c.Epoch > 0 && c.Epoch <= uint64(^uint64(0)>>1) && c.UntilMS > 0
}
func controlLiveRoot(r control.Run, now int64) bool {
	return r.Approved && r.Grant.ExpiresAtMS > now && r.Status != control.Cancelling && !control.IsTerminal(r.Status) && r.Status != control.AwaitingApproval
}
func controlResources(r control.Run) ([]controlResource, error) {
	out := make([]controlResource, 0, len(r.Jobs)+1)
	for _, j := range r.Jobs {
		if j.Status != control.JobRunning && j.Status != control.JobUnknown {
			continue
		}
		if !validControlClaim(j.Claim, r, j.Spec.ID) {
			return nil, control.ErrDenied
		}
		mode := controlResourceRead
		if len(j.Spec.WritePaths) > 0 {
			mode = controlResourceWrite
		}
		out = append(out, controlResource{r.Scope, r.ID, j.Spec.ID, r.Grant.Workspace, mode, j.Claim})
	}
	if c := r.VerificationClaim; c.Token != "" {
		if !validControlClaim(c, r, controlVerifyJob) {
			return nil, control.ErrDenied
		}
		out = append(out, controlResource{r.Scope, r.ID, controlVerifyJob, r.Grant.Workspace, controlResourceRead, c})
	}
	return out, nil
}

// Paths are canonical authorized roots, not arbitrary tool paths. Rel handles
// volume/root boundaries correctly (including "/"), unlike prefix matching.
func controlPathsOverlap(a, b string) bool {
	contains := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return contains(a, b) || contains(b, a)
}
func conflictingControlResources(a, b controlResource) bool {
	return (a.mode == controlResourceWrite || b.mode == controlResourceWrite) && controlPathsOverlap(a.workspace, b.workspace)
}
func validateControlResources(old, next control.Run, now int64) error {
	for i, j := range old.Jobs {
		n := next.Jobs[i]
		held := j.Status == control.JobRunning || j.Status == control.JobUnknown
		holding := n.Status == control.JobRunning || n.Status == control.JobUnknown
		if !held && holding {
			if n.Status != control.JobRunning || n.Claim.UntilMS <= now || !controlLiveRoot(next, now) {
				return control.ErrDenied
			}
		}
		if held {
			if !sameControlClaim(j.Claim, n.Claim) {
				return control.ErrDenied
			}
			if holding {
				if j.Claim.UntilMS != n.Claim.UntilMS && (j.Claim.UntilMS <= now || n.Claim.UntilMS <= now || !controlLiveRoot(next, now)) {
					return control.ErrDenied
				}
			} else if !control.ValidStopResolution(old, next) && (j.Status == control.JobUnknown || j.Claim.UntilMS <= now || n.FinishedAtMS <= 0) {
				return control.ErrDenied
			}
		}
	}
	a, b := old.VerificationClaim, next.VerificationClaim
	if a.Token != "" {
		if b.Token != "" {
			if !sameControlClaim(a, b) {
				return control.ErrDenied
			}
			if a.UntilMS != b.UntilMS && (a.UntilMS <= now || b.UntilMS <= now || !controlLiveRoot(next, now)) {
				return control.ErrDenied
			}
		} else if a.UntilMS <= now && !control.ValidStopResolution(old, next) {
			return control.ErrDenied
		}
	} else if b.Token != "" {
		if b.UntilMS <= now || !controlLiveRoot(next, now) || next.Status != control.Verifying {
			return control.ErrDenied
		}
		for _, j := range next.Jobs {
			if j.Status != control.JobSucceeded {
				return control.ErrDenied
			}
		}
	}
	return nil
}
func controlSyncResources(ctx context.Context, tx *sql.Tx, old, next control.Run, now int64) error {
	if err := validateControlResources(old, next, now); err != nil {
		return err
	}
	wanted, err := controlResources(next)
	if err != nil {
		return err
	}
	prior, err := controlResources(old)
	if err != nil {
		return err
	}
	// Existing ownership must be present: never silently recreate a lost fence.
	rows, err := tx.QueryContext(ctx, `SELECT job_id,workspace,mode,owner,token,epoch FROM super_resources WHERE scope=? AND run_id=?`, old.Scope, old.ID)
	if err != nil {
		return controlStorageError(ctx, err)
	}
	held := map[string]controlResource{}
	for rows.Next() {
		var h controlResource
		h.scope = old.Scope
		h.run = old.ID
		if err = rows.Scan(&h.job, &h.workspace, &h.mode, &h.claim.Owner, &h.claim.Token, &h.claim.Epoch); err != nil {
			rows.Close()
			return controlStorageError(ctx, err)
		}
		held[h.job] = h
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return controlStorageError(ctx, err)
	}
	if len(held) != len(prior) {
		return control.ErrUnavailable
	}
	for _, p := range prior {
		h, ok := held[p.job]
		if !ok || h.workspace != p.workspace || h.mode != p.mode || h.claim.Owner != p.claim.Owner || h.claim.Token != p.claim.Token || h.claim.Epoch != p.claim.Epoch {
			return control.ErrUnavailable
		}
	}
	for i, a := range wanted {
		for _, b := range wanted[:i] {
			if conflictingControlResources(a, b) {
				return control.ErrResourceBusy
			}
		}
	}
	// Budget accounting and lease renewals do not change resource ownership.
	// Avoid a global scan and delete/reinsert of the same locks on every turn.
	if sameControlResourceOwnership(prior, wanted) {
		return nil
	}
	rows, err = tx.QueryContext(ctx, `SELECT workspace,mode FROM super_resources WHERE NOT(scope=? AND run_id=?)`, next.Scope, next.ID)
	if err != nil {
		return controlStorageError(ctx, err)
	}
	for rows.Next() {
		var other controlResource
		if err = rows.Scan(&other.workspace, &other.mode); err != nil {
			rows.Close()
			return controlStorageError(ctx, err)
		}
		for _, w := range wanted {
			if conflictingControlResources(w, other) {
				rows.Close()
				return control.ErrResourceBusy
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return controlStorageError(ctx, err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM super_resources WHERE scope=? AND run_id=?", next.Scope, next.ID); err != nil {
		return controlStorageError(ctx, err)
	}
	for _, w := range wanted {
		if _, err = tx.ExecContext(ctx, `INSERT INTO super_resources(scope,run_id,job_id,workspace,mode,owner,token,epoch) VALUES(?,?,?,?,?,?,?,?)`, w.scope, w.run, w.job, w.workspace, w.mode, w.claim.Owner, w.claim.Token, w.claim.Epoch); err != nil {
			return controlStorageError(ctx, err)
		}
	}
	return nil
}

func sameControlResourceOwnership(a, b []controlResource) bool {
	if len(a) != len(b) {
		return false
	}
	for i, p := range a {
		n := b[i]
		if p.scope != n.scope || p.run != n.run || p.job != n.job || p.workspace != n.workspace || p.mode != n.mode || !sameControlClaim(p.claim, n.claim) {
			return false
		}
	}
	return true
}
