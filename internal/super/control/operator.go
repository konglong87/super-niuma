package control

import (
	"context"
	"errors"
	"reflect"
)

const StopConfirmation = "ALL_WORKERS_AND_SENDERS_CONFIRMED_STOPPED"
const ResolutionAbandoned = "abandoned_after_stop_confirmation"

// LocalOperatorActor identifies the trusted local administration surface, not
// the external owner whose persisted scope/principal selected the target.
const LocalOperatorActor = LocalOwner

type Resolution struct {
	Kind            string `json:"kind"`
	Actor           string `json:"actor"`
	PreviousVersion uint64 `json:"previous_version"`
	AtMS            int64  `json:"at_ms"`
}

// AbandonAfterInspection releases isolation only after a trusted local operator
// explicitly attests every prior executor/sender stopped. The platform derives
// p.Scope/Owner from the unique persisted run; LocalAdmin is never JSON input.
func (e *Engine) AbandonAfterInspection(ctx context.Context, p Principal, id string, version uint64, confirmation string) (Run, error) {
	if !p.LocalAdmin || !ValidID(p.Scope) || !ValidID(p.Owner) || confirmation != StopConfirmation {
		return Run{}, ErrDenied
	}
	out, err := e.mutate(ctx, p.Scope, id, func(r *Run) error {
		if !CanRead(p, *r) {
			return ErrDenied
		}
		if r.Status != NeedsAttention || r.Version != version || r.Resolution != nil {
			return ErrConflict
		}
		*r = stoppedAfterInspection(*r, e.now())
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Run{}, ErrDenied
	}
	return out, err
}

// The command and Store proof share one exact, side-effect-free transition.
// Preserve receipts, claims, results and reservations; never manufacture success
// or refund unknown usage. Clone only the slices whose entries are changed.
func stoppedAfterInspection(old Run, atMS int64) Run {
	next := old
	next.Jobs = append([]Job(nil), old.Jobs...)
	next.Notices = append([]Notice(nil), old.Notices...)
	next.Resolution = &Resolution{Kind: ResolutionAbandoned, Actor: LocalOperatorActor, PreviousVersion: old.Version, AtMS: atMS}
	next.Status = Cancelled
	next.SafeCode = "operator_abandoned_no_retry"
	for i := range next.Jobs {
		j := &next.Jobs[i]
		if j.Status == JobQueued || j.Status == JobRunning || j.Status == JobUnknown {
			j.Status = JobCancelled
			j.FinishedAtMS = atMS
			j.SafeCode = "operator_stop_confirmed"
		}
	}
	next.VerificationClaim = Claim{Epoch: old.VerificationClaim.Epoch}
	for i := range next.Notices {
		n := &next.Notices[i]
		if n.Status == DeliverySending {
			n.Status = DeliveryUnknown
			n.SafeCode = "outcome_still_unknown"
		} else if n.Status == DeliveryQueued || n.Status == DeliveryRetry {
			n.Status = DeliveryDead
			n.SafeCode = "operator_abandoned"
		}
	}
	return next
}
func ValidStopResolution(old, next Run) bool {
	if old.Version == 0 || next.Version == 0 || old.Resolution != nil || old.Status != NeedsAttention || next.Resolution == nil || next.Version != old.Version+1 || next.Resolution.AtMS <= 0 || next.Resolution.AtMS < old.UpdatedAtMS || next.UpdatedAtMS < next.Resolution.AtMS {
		return false
	}
	expected := stoppedAfterInspection(old, next.Resolution.AtMS)
	expected.Version = old.Version + 1
	expected.UpdatedAtMS = next.UpdatedAtMS
	// Exact structural equality also rejects tampering with evidence/unknown
	// reservations. A plausible actor/version alone is not a valid release proof.
	return reflect.DeepEqual(expected, next)
}
