package platform

import (
	"context"
	"errors"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

const (
	operatorApprove = "platform.operator.approve"
	operatorCancel  = "platform.operator.cancel"
	operatorRetry   = "platform.operator.retry"
	operatorAbandon = "platform.operator.abandon"
)

func (s *Service) operatorRepository() (control.OperatorRepository, error) {
	if s == nil || s.Engine == nil {
		return nil, control.ErrUnavailable
	}
	repo, ok := s.Engine.Store.(control.OperatorRepository)
	if !ok {
		return nil, control.ErrUnavailable
	}
	return repo, nil
}

// operate is reachable only from the trusted local Desktop management surface.
// It never accepts an owner/scope from the frontend and never changes Router
// principals. The ordinary Engine continues enforcing CAS, consent and grants.
func (s *Service) operate(ctx context.Context, action, id string, fn func(control.Principal) (control.Run, error)) (out control.Run, err error) {
	defer func() {
		if s != nil && s.logger != nil {
			s.logger.InfoContext(ctx, "Super operator action", "action", action, "code", operatorCode(err))
		}
	}()
	repo, err := s.operatorRepository()
	if err != nil {
		return control.Run{}, err
	}
	persisted, err := repo.RunForOperator(ctx, id)
	if err != nil {
		return control.Run{}, err
	}
	if persisted.ID != id || !control.ValidID(persisted.Scope) || !control.ValidID(persisted.Owner) {
		return control.Run{}, control.ErrUnavailable
	}
	return fn(control.Principal{Scope: persisted.Scope, Owner: persisted.Owner, LocalAdmin: true})
}
func operatorCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, control.ErrConflict):
		return "revision_conflict"
	case errors.Is(err, control.ErrNotFound):
		return "run_not_found"
	case errors.Is(err, control.ErrDenied):
		return "authority_denied"
	case errors.Is(err, control.ErrInvalid):
		return "invalid_request"
	default:
		return "runtime_unavailable"
	}
}
func (s *Service) ApproveRun(ctx context.Context, id string, version uint64, grantHash string) (control.Run, error) {
	return s.operate(ctx, operatorApprove, id, func(p control.Principal) (control.Run, error) {
		return s.Engine.Approve(ctx, p, id, version, grantHash)
	})
}

// CancelRun is the trusted operator's idempotent emergency stop. The legacy
// version argument is retained for bridge compatibility but heartbeats must not
// defeat cancellation. External Router commands still use Engine.Cancel's CAS.
func (s *Service) CancelRun(ctx context.Context, id string, version uint64) (control.Run, error) {
	return s.operate(ctx, operatorCancel, id, func(p control.Principal) (control.Run, error) {
		current, err := s.Engine.Get(ctx, p, id)
		if err != nil {
			return control.Run{}, err
		}
		if current.Status == control.Cancelled || current.Status == control.Cancelling {
			return current, nil
		}
		out, err := s.Engine.Cancel(ctx, p, id, 0)
		if errors.Is(err, control.ErrConflict) {
			// A concurrent stop may already have committed. Never treat another
			// terminal outcome (e.g. delivered/failed) as a successful stop.
			current, readErr := s.Engine.Get(ctx, p, id)
			if readErr == nil && (current.Status == control.Cancelled || current.Status == control.Cancelling) {
				return current, nil
			}
		}
		return out, err
	})
}
func (s *Service) RetryRun(ctx context.Context, id string, version uint64) (control.Run, error) {
	return s.operate(ctx, operatorRetry, id, func(p control.Principal) (control.Run, error) { return s.Engine.Retry(ctx, p, id, version) })
}
func (s *Service) AbandonRun(ctx context.Context, id string, version uint64, confirmation string) (control.Run, error) {
	return s.operate(ctx, operatorAbandon, id, func(p control.Principal) (control.Run, error) {
		return s.Engine.AbandonAfterInspection(ctx, p, id, version, confirmation)
	})
}

// Events exposes only a bounded cross-scope read to the trusted Desktop bridge.
func (s *Service) Events(ctx context.Context, after int64) ([]eventbus.Event, error) {
	repo, err := s.operatorRepository()
	if err != nil {
		return nil, err
	}
	return repo.EventsForOperator(ctx, after, eventbus.MaxReadLimit)
}
