package sqlitestore

import (
	"context"
	"encoding/json"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

// ReadDeadLetters returns newest first, bounded to MaxReadLimit. It never retries
// events or resets a subscription; an authorized replay is a separate operation.
func (b *Bus) ReadDeadLetters(ctx context.Context, scope, name string, limit int) (letters []eventbus.DeadLetter, err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "dead_letter.read", err) }()
	if err = validatePage(scope, 0, limit); err != nil {
		return nil, err
	}
	if !eventbus.ValidKey(name) {
		return nil, eventbus.ErrInvalidInput
	}
	rows, err := b.store.db.QueryContext(ctx, `SELECT d.scope,d.subscriber,d.attempts,d.code,d.created_at_ms,e.sequence,e.event_id,e.scope,e.topic,e.aggregate_id,e.version,e.payload,e.occurred_at_ms FROM super_dead_letters d JOIN super_events e ON e.sequence=d.event_sequence AND e.scope=d.scope WHERE d.scope=? AND d.subscriber=? ORDER BY d.event_sequence DESC LIMIT ?`, scope, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	letters = []eventbus.DeadLetter{}
	for rows.Next() {
		var d eventbus.DeadLetter
		var payload string
		e := &d.Event
		if err = rows.Scan(&d.Scope, &d.Subscriber, &d.Attempts, &d.Code, &d.CreatedAtMS, &e.Sequence, &e.ID, &e.Scope, &e.Topic, &e.AggregateID, &e.Version, &payload, &e.OccurredAtMS); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		letters = append(letters, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return letters, nil
}
