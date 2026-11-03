package main

import (
	"context"
	"errors"
	"strings"

	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/platform"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func localSuperPrincipal() control.Principal {
	return control.Principal{Scope: control.LocalScope, Owner: control.LocalOwner, LocalAdmin: true}
}
func (b *superWorkOrderBridge) platformService() (*platform.Service, error) {
	if b.platform != nil {
		return b.platform, nil
	}
	store, ok := b.store.(control.Repository)
	if !ok {
		return nil, control.ErrUnavailable
	}
	directory, err := super.StateDirectory()
	if err != nil {
		return nil, control.ErrUnavailable
	}
	b.platform = platform.New(store, directory, b.logger)
	return b.platform, nil
}
func (a *app) callSuperPlatform(action string, fn func(context.Context, *platform.Service, *workorder.Service) error) error {
	return a.superWorkOrders.call(action, func(ctx context.Context, intake *workorder.Service) error {
		s, err := a.superWorkOrders.platformService()
		if err != nil {
			return err
		}
		return fn(ctx, s, intake)
	})
}
func (a *app) GetSuperPlatform() (out platform.Snapshot, err error) {
	err = a.callSuperPlatform("platform.snapshot", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		out, err = s.Snapshot(ctx)
		return err
	})
	return
}
func (a *app) SaveSuperPlatformConfig(text string) (out platform.Snapshot, err error) {
	err = a.callSuperPlatform("platform.configure", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		out, err = s.Save(ctx, text)
		return err
	})
	return
}
func (a *app) StartSuperRun(intakeID, profileID, key string) (out control.Run, err error) {
	err = a.callSuperPlatform("platform.start", func(ctx context.Context, s *platform.Service, intake *workorder.Service) error {
		order, e := intake.Get(ctx, intakeID)
		if e != nil {
			return e
		}
		if order.Status != workorder.StatusRecorded {
			return control.ErrDenied
		}
		p := localSuperPrincipal()
		grant, e := s.Grant(ctx, p, profileID)
		if e != nil {
			return e
		}
		if order.Workspace != "" && order.Workspace != grant.Workspace {
			return control.ErrDenied
		}
		objective := order.Objective + "\n\n用户验收要求（仅已配置结构化规则可自动判定；其余不得自报通过）：\n- " + strings.Join(order.AcceptanceCriteria, "\n- ")
		out, _, e = s.Engine.Start(ctx, p, key, objective, order.ID, grant, nil)
		return e
	})
	return
}
func (a *app) ApproveSuperRun(id string, version uint64, grantHash string) (out control.Run, err error) {
	err = a.callSuperPlatform("platform.approve", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		out, err = s.ApproveRun(ctx, id, version, grantHash)
		return err
	})
	return
}
func (a *app) CancelSuperRun(id string, version uint64) (out control.Run, err error) {
	err = a.callSuperPlatform("platform.cancel", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		// Preserve the existing best-effort stop semantics; only principal resolution changes.
		out, err = s.CancelRun(ctx, id, 0)
		return err
	})
	return
}
func (a *app) RetrySuperRun(id string, version uint64) (out control.Run, err error) {
	err = a.callSuperPlatform("platform.retry", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		out, err = s.RetryRun(ctx, id, version)
		return err
	})
	return
}
func (a *app) GetSuperEvents(after int64) (out []eventbus.Event, err error) {
	err = a.superWorkOrders.call("platform.events", func(ctx context.Context, _ *workorder.Service) error {
		reader, ok := a.superWorkOrders.store.(control.OperatorRepository)
		if !ok {
			return control.ErrUnavailable
		}
		out, err = reader.EventsForOperator(ctx, after, eventbus.MaxReadLimit)
		return err
	})
	return
}
func safeSuperPlatformError(err error) (error, string, bool) {
	for _, v := range []struct {
		err  error
		code string
	}{{control.ErrInvalid, "invalid_configuration"}, {control.ErrNotFound, "run_not_found"}, {control.ErrConflict, "revision_conflict"}, {control.ErrDenied, "authority_denied"}, {control.ErrCapacity, "capacity_exhausted"}, {control.ErrResourceBusy, "workspace_busy"}, {control.ErrUnavailable, "runtime_unconfigured"}} {
		if errors.Is(err, v.err) {
			return v.err, v.code, true
		}
	}
	return nil, "", false
}

func (a *app) AbandonSuperRun(id string, version uint64, confirmation string) (out control.Run, err error) {
	err = a.callSuperPlatform("platform.abandon_after_inspection", func(ctx context.Context, s *platform.Service, _ *workorder.Service) error {
		out, err = s.AbandonRun(ctx, id, version, confirmation)
		return err
	})
	return
}
