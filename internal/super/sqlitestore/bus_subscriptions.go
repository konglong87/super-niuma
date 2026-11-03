package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

const subscriptionColumns = "scope,name,topics_json,start_position,max_attempts,cursor,current_sequence,attempts,retry_at_ms,owner,lease_token,epoch,lease_until_ms"

type subscriptionState struct {
	scope, name, topicsJSON, start string
	maxAttempts                    int
	cursor, current                int64
	attempts                       int
	retryAt                        int64
	owner, token                   string
	epoch, leaseUntil              int64
}

func loadSubscription(ctx context.Context, tx *sql.Tx, scope, name string) (subscriptionState, error) {
	var s subscriptionState
	err := tx.QueryRowContext(ctx, "SELECT "+subscriptionColumns+" FROM super_subscriptions WHERE scope=? AND name=?", scope, name).Scan(&s.scope, &s.name, &s.topicsJSON, &s.start, &s.maxAttempts, &s.cursor, &s.current, &s.attempts, &s.retryAt, &s.owner, &s.token, &s.epoch, &s.leaseUntil)
	return s, err
}
func (b *Bus) RegisterSubscription(ctx context.Context, input eventbus.Subscription) (err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "subscription.register", err) }()
	sub, err := eventbus.NormalizeSubscription(input)
	if err != nil {
		return err
	}
	topics, _ := json.Marshal(sub.Topics)
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	prior, err := loadSubscription(ctx, tx, sub.Scope, sub.Name)
	if err == nil {
		if prior.topicsJSON != string(topics) || prior.start != string(sub.Start) || prior.maxAttempts != sub.MaxAttempts {
			return eventbus.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_subscriptions").Scan(&count); err != nil {
		return err
	}
	if count >= eventbus.MaxSubscriptions {
		return eventbus.ErrCapacity
	}
	cursor := int64(0)
	if sub.Start == eventbus.StartNow {
		if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0) FROM super_events WHERE scope=?", sub.Scope).Scan(&cursor); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO super_subscriptions(scope,name,topics_json,start_position,max_attempts,cursor) VALUES(?,?,?,?,?,?)`, sub.Scope, sub.Name, string(topics), sub.Start, sub.MaxAttempts, cursor)
	if err != nil {
		return err
	}
	return tx.Commit()
}
