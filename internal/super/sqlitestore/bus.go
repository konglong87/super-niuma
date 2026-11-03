package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

// Bus is an immutable clock-configured view; all authority remains in Store.db.
// No process-local cursor, lease or global options registry is involved.
type Bus struct {
	store *Store
	now   func() time.Time
}

func (s *Store) Bus(options eventbus.Options) *Bus {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Bus{store: s, now: now}
}

var _ eventbus.Store = (*Store)(nil)
var _ eventbus.Store = (*Bus)(nil)

func (s *Store) AppendEvent(ctx context.Context, e eventbus.Event) (eventbus.Event, bool, error) {
	return s.Bus(eventbus.Options{}).AppendEvent(ctx, e)
}
func (s *Store) RegisterSubscription(ctx context.Context, v eventbus.Subscription) error {
	return s.Bus(eventbus.Options{}).RegisterSubscription(ctx, v)
}
func (s *Store) ClaimEvent(ctx context.Context, scope, name, owner string, lease time.Duration) (eventbus.Delivery, bool, error) {
	return s.Bus(eventbus.Options{}).ClaimEvent(ctx, scope, name, owner, lease)
}
func (s *Store) AckEvent(ctx context.Context, t eventbus.DeliveryToken) error {
	return s.Bus(eventbus.Options{}).AckEvent(ctx, t)
}
func (s *Store) NackEvent(ctx context.Context, t eventbus.DeliveryToken, code string, retry time.Duration) error {
	return s.Bus(eventbus.Options{}).NackEvent(ctx, t, code, retry)
}
func (s *Store) ReadEvents(ctx context.Context, scope string, after int64, limit int) ([]eventbus.Event, error) {
	return s.Bus(eventbus.Options{}).ReadEvents(ctx, scope, after, limit)
}
func (s *Store) ReadDeadLetters(ctx context.Context, scope, name string, limit int) ([]eventbus.DeadLetter, error) {
	return s.Bus(eventbus.Options{}).ReadDeadLetters(ctx, scope, name, limit)
}

func busError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, safe := range []error{eventbus.ErrInvalidInput, eventbus.ErrConflict, eventbus.ErrCapacity, eventbus.ErrNotFound, eventbus.ErrLeaseLost, eventbus.ErrUnavailable} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return eventbus.ErrNotFound
	}
	return eventbus.ErrUnavailable
}
func traceBus(ctx context.Context, action string, err error) {
	args := []any{"component", "super.eventbus", "action", action}
	if err != nil {
		args = append(args, "safe_code", busError(ctx, err).Error())
		slog.WarnContext(ctx, "durable bus operation rejected", args...)
		return
	}
	slog.DebugContext(ctx, "durable bus operation finished", args...)
}
func validatePage(scope string, after int64, limit int) error {
	if !eventbus.ValidKey(scope) || after < 0 || limit < 1 || limit > eventbus.MaxReadLimit {
		return eventbus.ErrInvalidInput
	}
	return nil
}

// Ceil to the persisted clock resolution; never silently shorten a lease.
func futureMillis(now int64, d time.Duration) (int64, error) {
	if now <= 0 || d < 0 {
		return 0, eventbus.ErrInvalidInput
	}
	ms := int64(d / time.Millisecond)
	if d%time.Millisecond != 0 {
		ms++
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	if ms > maxInt64-now {
		return 0, eventbus.ErrInvalidInput
	}
	return now + ms, nil
}
