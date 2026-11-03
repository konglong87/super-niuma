package sqlitestore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

func (b *Bus) ClaimEvent(ctx context.Context, scope, name, owner string, lease time.Duration) (delivery eventbus.Delivery, found bool, err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "delivery.claim", err) }()
	if !eventbus.ValidKey(scope) || !eventbus.ValidKey(name) || !eventbus.ValidKey(owner) || lease <= 0 || lease > eventbus.MaxLease {
		return eventbus.Delivery{}, false, eventbus.ErrInvalidInput
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return eventbus.Delivery{}, false, err
	}
	defer tx.Rollback()
	state, err := loadSubscription(ctx, tx, scope, name)
	if err != nil {
		return eventbus.Delivery{}, false, err
	}
	delivery, found, err = b.claimTx(ctx, tx, state, owner, lease)
	if err != nil {
		return eventbus.Delivery{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return eventbus.Delivery{}, false, err
	}
	return delivery, found, nil
}
func (b *Bus) claimTx(ctx context.Context, tx *sql.Tx, state subscriptionState, owner string, lease time.Duration) (eventbus.Delivery, bool, error) {
	now := b.now().UTC().UnixMilli()
	until, err := futureMillis(now, lease)
	if err != nil {
		return eventbus.Delivery{}, false, err
	}
	// At most one expired current event is retired; its successor is a fresh
	// attempt. Existing valid leases always win, including the final attempt.
	for {
		if state.leaseUntil > now || state.retryAt > now {
			return eventbus.Delivery{}, false, nil
		}
		prior := state
		if state.current != 0 && state.attempts >= state.maxAttempts {
			if err := deadLetterTx(ctx, tx, state, eventbus.CodeLeaseExpired, now); err != nil {
				return eventbus.Delivery{}, false, err
			}
			state = clearedDelivery(state)
			if err := writeSubscription(ctx, tx, prior, state); err != nil {
				return eventbus.Delivery{}, false, err
			}
			continue
		}
		event, exists, err := nextSubscriptionEvent(ctx, tx, state)
		if err != nil {
			return eventbus.Delivery{}, false, err
		}
		if !exists {
			// All matching events in this transaction's snapshot have been handled;
			// skip unrelated topics without leaving an ever-growing scan backlog.
			if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0) FROM super_events WHERE scope=?", state.scope).Scan(&state.cursor); err != nil {
				return eventbus.Delivery{}, false, err
			}
			if state.cursor < prior.cursor {
				return eventbus.Delivery{}, false, eventbus.ErrUnavailable
			}
			if state.cursor != prior.cursor {
				err = writeSubscription(ctx, tx, prior, state)
			}
			return eventbus.Delivery{}, false, err
		}
		if state.epoch == int64(^uint64(0)>>1) {
			return eventbus.Delivery{}, false, eventbus.ErrUnavailable
		}
		bytes := make([]byte, 32)
		if _, err = rand.Read(bytes); err != nil {
			return eventbus.Delivery{}, false, eventbus.ErrUnavailable
		}
		state.current = event.Sequence
		state.attempts++
		state.epoch++
		state.owner = owner
		state.token = hex.EncodeToString(bytes)
		state.leaseUntil = until
		state.retryAt = 0
		if err = writeSubscription(ctx, tx, prior, state); err != nil {
			return eventbus.Delivery{}, false, err
		}
		token := eventbus.DeliveryToken{Scope: state.scope, Name: state.name, Sequence: state.current, Owner: owner, Token: state.token, Epoch: state.epoch, LeaseUntilMS: until}
		return eventbus.Delivery{Event: event, Token: token, Attempt: state.attempts}, true, nil
	}
}
func nextSubscriptionEvent(ctx context.Context, tx *sql.Tx, s subscriptionState) (eventbus.Event, bool, error) {
	if s.current != 0 {
		if s.current <= s.cursor || s.attempts < 1 {
			return eventbus.Event{}, false, eventbus.ErrUnavailable
		}
		e, err := scanBusEvent(tx.QueryRowContext(ctx, "SELECT "+busEventColumns+" FROM super_events WHERE scope=? AND sequence=?", s.scope, s.current))
		if errors.Is(err, sql.ErrNoRows) {
			return eventbus.Event{}, false, eventbus.ErrUnavailable
		}
		return e, err == nil, err
	}
	var topics []string
	if err := json.Unmarshal([]byte(s.topicsJSON), &topics); err != nil || len(topics) == 0 || len(topics) > eventbus.MaxTopics {
		return eventbus.Event{}, false, eventbus.ErrUnavailable
	}
	args := []any{s.scope, s.cursor}
	for _, topic := range topics {
		args = append(args, topic)
	}
	query := "SELECT " + busEventColumns + " FROM super_events WHERE scope=? AND sequence>? AND topic IN (" + strings.TrimSuffix(strings.Repeat("?,", len(topics)), ",") + ") ORDER BY sequence LIMIT 1"
	e, err := scanBusEvent(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return eventbus.Event{}, false, nil
	}
	return e, err == nil, err
}

// All mutations run under an IMMEDIATE SQLite transaction. The predicate also
// makes stale ownership explicit rather than relying on a process-local mutex.
func writeSubscription(ctx context.Context, tx *sql.Tx, prior, next subscriptionState) error {
	result, err := tx.ExecContext(ctx, `UPDATE super_subscriptions SET cursor=?,current_sequence=?,attempts=?,retry_at_ms=?,owner=?,lease_token=?,epoch=?,lease_until_ms=? WHERE scope=? AND name=? AND cursor=? AND current_sequence=? AND epoch=? AND lease_token=?`, next.cursor, next.current, next.attempts, next.retryAt, next.owner, next.token, next.epoch, next.leaseUntil, prior.scope, prior.name, prior.cursor, prior.current, prior.epoch, prior.token)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return eventbus.ErrLeaseLost
	}
	return nil
}
func clearedDelivery(s subscriptionState) subscriptionState {
	s.cursor = s.current
	s.current = 0
	s.attempts = 0
	s.retryAt = 0
	s.owner = ""
	s.token = ""
	s.leaseUntil = 0
	return s
}
func deadLetterTx(ctx context.Context, tx *sql.Tx, s subscriptionState, code string, now int64) error {
	if s.current <= s.cursor || s.attempts < 1 {
		return eventbus.ErrUnavailable
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO super_dead_letters(scope,subscriber,event_sequence,attempts,code,created_at_ms) VALUES(?,?,?,?,?,?)`, s.scope, s.name, s.current, s.attempts, code, now)
	return err
}
func liveDelivery(ctx context.Context, tx *sql.Tx, t eventbus.DeliveryToken, now int64) (subscriptionState, error) {
	s, err := loadSubscription(ctx, tx, t.Scope, t.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return s, eventbus.ErrLeaseLost
	}
	if err != nil {
		return s, err
	}
	if now <= 0 || s.current != t.Sequence || s.owner != t.Owner || s.token != t.Token || s.epoch != t.Epoch || s.leaseUntil != t.LeaseUntilMS || s.leaseUntil <= now {
		return s, eventbus.ErrLeaseLost
	}
	return s, nil
}
func (b *Bus) AckEvent(ctx context.Context, token eventbus.DeliveryToken) (err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "delivery.ack", err) }()
	if err = eventbus.ValidateToken(token); err != nil {
		return err
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := liveDelivery(ctx, tx, token, b.now().UTC().UnixMilli())
	if err != nil {
		return err
	}
	if err = writeSubscription(ctx, tx, state, clearedDelivery(state)); err != nil {
		return err
	}
	return tx.Commit()
}
func (b *Bus) NackEvent(ctx context.Context, token eventbus.DeliveryToken, code string, retry time.Duration) (err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "delivery.nack", err) }()
	if err = eventbus.ValidateToken(token); err != nil {
		return err
	}
	if !eventbus.ValidCode(code) || retry < 0 || retry > eventbus.MaxRetryDelay {
		return eventbus.ErrInvalidInput
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := b.now().UTC().UnixMilli()
	state, err := liveDelivery(ctx, tx, token, now)
	if err != nil {
		return err
	}
	next := state
	if state.attempts >= state.maxAttempts {
		if err = deadLetterTx(ctx, tx, state, code, now); err != nil {
			return err
		}
		next = clearedDelivery(state)
	} else {
		next.owner = ""
		next.token = ""
		next.leaseUntil = 0
		next.retryAt, err = futureMillis(now, retry)
		if err != nil {
			return err
		}
	}
	if err = writeSubscription(ctx, tx, state, next); err != nil {
		return err
	}
	return tx.Commit()
}
