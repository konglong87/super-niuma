package sqlitestore

import (
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

func validateControlNotices(a, b control.Run, now int64) error {
	if b.Status == control.DeliveryPending || b.Status == control.Delivered {
		if !b.Approved || b.Verification == nil || !b.Verification.Passed || b.Verification.PolicyHash != verification.PolicyHash(b.Grant.Verification) || len(b.Notices) == 0 {
			return control.ErrDenied
		}
		for _, j := range b.Jobs {
			if j.Status != control.JobSucceeded {
				return control.ErrDenied
			}
		}
	}
	seen, targets := map[string]bool{}, map[string]bool{}
	old := map[string]control.Notice{}
	for _, n := range a.Notices {
		old[n.ID] = n
	}
	for _, n := range b.Notices {
		if !control.ValidID(n.ID) || seen[n.ID] || targets[n.Target.ID] || n.Attempts < 0 || n.Attempts > control.MaxAttempts {
			return control.ErrInvalid
		}
		seen[n.ID] = true
		targets[n.Target.ID] = true
		allowed := false
		for _, target := range b.Grant.Targets {
			if control.Digest(target) == control.Digest(n.Target) {
				allowed = true
			}
		}
		if !allowed {
			return control.ErrDenied
		}
		switch n.Status {
		case control.DeliveryQueued, control.DeliverySending, control.DeliverySent, control.DeliveryRetry, control.DeliveryUnknown, control.DeliveryDead:
		default:
			return control.ErrInvalid
		}
		prior, exists := old[n.ID]
		if !exists {
			if b.Status != control.DeliveryPending || n.Status != control.DeliveryQueued || n.Attempts != 0 || n.Claim.Token != "" {
				return control.ErrDenied
			}
		} else {
			if control.Digest(prior.Target) != control.Digest(n.Target) || !controlNoticeTransition(prior.Status, n.Status) {
				return control.ErrConflict
			}
			if err := validateControlNoticeClaim(prior, n, b, now); err != nil {
				return err
			}
		}
		if n.Status == control.DeliverySent && (n.Receipt == nil || n.Receipt.MessageID == "" || n.Receipt.DeliveredAtMS <= 0) {
			return control.ErrInvalid
		}
		if b.Status == control.Delivered && n.Status != control.DeliverySent {
			return control.ErrDenied
		}
	}
	for id := range old {
		if !seen[id] {
			return control.ErrConflict
		}
	}
	return nil
}
func controlNoticeTransition(a, b control.DeliveryStatus) bool {
	if a == b {
		return true
	}
	switch a {
	case control.DeliveryQueued, control.DeliveryRetry:
		return b == control.DeliverySending || b == control.DeliveryDead
	case control.DeliverySending:
		return b == control.DeliverySent || b == control.DeliveryRetry || b == control.DeliveryUnknown || b == control.DeliveryDead
	default:
		return false
	}
}
func validateControlNoticeClaim(old, n control.Notice, r control.Run, now int64) error {
	starts := old.Status != control.DeliverySending && n.Status == control.DeliverySending
	if starts {
		if n.Attempts != old.Attempts+1 || n.Claim.Epoch != old.Claim.Epoch+1 || !validControlClaim(n.Claim, r, controlDeliveryPrefix+n.Target.ID) || n.Claim.UntilMS <= now || !controlLiveRoot(r, now) {
			return control.ErrDenied
		}
	} else if n.Attempts != old.Attempts {
		return control.ErrConflict
	}
	if old.Status != control.DeliverySending {
		return nil
	}
	if n.Status == control.DeliverySending {
		if !sameControlClaim(old.Claim, n.Claim) {
			return control.ErrDenied
		}
		if n.Claim.UntilMS != old.Claim.UntilMS && (old.Claim.UntilMS <= now || n.Claim.UntilMS <= now || !controlLiveRoot(r, now)) {
			return control.ErrDenied
		}
		return nil
	}
	// The aggregate CAS binds settlement to the currently saved claim. The
	// engine may clear that claim after the transport has returned; an expired
	// claim can only record an unknown outcome, never a successful receipt.
	if n.Claim != (control.Claim{}) && !sameControlClaim(old.Claim, n.Claim) {
		return control.ErrDenied
	}
	if old.Claim.UntilMS <= now && n.Status != control.DeliveryUnknown {
		return control.ErrDenied
	}
	return nil
}
