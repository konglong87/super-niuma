package router

import (
	"context"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
)

func (s *Service) command(ctx context.Context, p control.Principal, in control.Ingress, action, id string) (Decision, error) {
	receipts, ok := s.engine.Store.(CommandReceipts)
	if !ok {
		return rejected(CodeUnavailable), control.ErrUnavailable
	}
	run, err := s.engine.Get(ctx, p, id)
	if err != nil {
		return rejected(errorCode(err)), err
	}
	receipt, err := receipts.BeginCommand(ctx, CommandRequest{Principal: p, Ingress: in, Action: action, RunID: id, ExpectedVersion: run.Version})
	if err != nil {
		return rejected(errorCode(err)), err
	}
	if receipt.Decision != nil {
		return *receipt.Decision, nil
	}
	decision := Decision{Action: action, RunID: id}
	if action == ActionStatus {
		decision.Code = "status_" + string(receipt.Status)
	} else {
		decision, err = s.cancel(ctx, p, receipt)
		if err != nil {
			return rejected(errorCode(err)), err
		}
	}
	if err = receipts.FinishCommand(ctx, receipt, decision); err != nil {
		return rejected(CodeUnavailable), err
	}
	return decision, nil
}
func (s *Service) cancel(ctx context.Context, p control.Principal, r CommandReceipt) (Decision, error) {
	d := Decision{Action: ActionCancel, RunID: r.Request.RunID, Code: CodeCancelled}
	if r.Status == control.Cancelled || r.Status == control.Cancelling {
		return d, nil
	}
	_, err := s.engine.Cancel(ctx, p, r.Request.RunID, r.Version)
	if err == nil {
		return d, nil
	}
	if !errors.Is(err, control.ErrConflict) {
		return d, err
	}
	// Resolve a lost receipt or concurrent replay without reapplying cancellation
	// to a different revision. Control remains the sole business-state writer.
	current, getErr := s.engine.Get(ctx, p, r.Request.RunID)
	if getErr != nil {
		return d, getErr
	}
	if (current.Status == control.Cancelled || current.Status == control.Cancelling) && current.Version >= r.Version+1 {
		return d, nil
	}
	d.Code = CodeConflict
	return d, nil
}
