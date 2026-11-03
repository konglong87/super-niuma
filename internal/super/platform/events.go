package platform

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

const noticeSubscriber = "super.notices.v1"

func (s *Service) consumeNotices(ctx context.Context) {
	owner := uuid.NewString()
	for ctx.Err() == nil {
		// Scopes are obtained from authorized, server-owned profiles, not a caller's
		// event payload. Each gets an independent durable subscriber cursor.
		cfg, err := s.config.load()
		if err != nil {
			if !pause(ctx) {
				return
			}
			continue
		}
		scopes := map[string]bool{control.LocalScope: true}
		for _, p := range cfg.Profiles {
			for _, scope := range p.Scopes {
				scopes[scope] = true
			}
		}
		worked := false
		for scope := range scopes {
			sub := eventbus.Subscription{Scope: scope, Name: noticeSubscriber, Topics: []string{control.EventDeliveryReady}, Start: eventbus.StartBeginning, MaxAttempts: eventbus.MaxAttempts}
			if err = s.bus.RegisterSubscription(ctx, sub); err != nil {
				s.logger.Warn("Super notice subscription unavailable", "action", "bus.subscribe", "code", "subscription_failed")
				continue
			}
			delivery, found, err := s.bus.ClaimEvent(ctx, scope, noticeSubscriber, owner, time.Minute)
			if err != nil || !found {
				continue
			}
			worked = true
			settled, runErr := s.Engine.DeliverRun(ctx, scope, delivery.Event.AggregateID, owner)
			if runErr == nil && settled {
				err = s.bus.AckEvent(ctx, delivery.Token)
			} else {
				err = s.bus.NackEvent(ctx, delivery.Token, "notice_pending", 10*time.Second)
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("Super notice cursor settlement failed", "action", "bus.settle", "code", "settlement_failed")
			}
		}
		if !worked && !pause(ctx) {
			return
		}
	}
}
func pause(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(250 * time.Millisecond):
		return true
	}
}
