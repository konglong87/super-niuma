package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	supercontrol "github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const busEventColumns = "sequence,event_id,scope,topic,aggregate_id,version,payload,occurred_at_ms"
const insertBusEventSQL = `INSERT INTO super_events(event_id,scope,topic,aggregate_id,version,payload,occurred_at_ms) VALUES(?,?,?,?,?,?,?)`

func scanBusEvent(row interface{ Scan(...any) error }) (eventbus.Event, error) {
	var e eventbus.Event
	var payload string
	err := row.Scan(&e.Sequence, &e.ID, &e.Scope, &e.Topic, &e.AggregateID, &e.Version, &payload, &e.OccurredAtMS)
	e.Payload = json.RawMessage(payload)
	return e, err
}
func intakeBusEvent(id, topic string, at time.Time) eventbus.Event {
	payload, _ := json.Marshal(struct {
		ID string `json:"work_order_id"`
	}{id})
	return eventbus.Event{ID: topic + ":" + id, Scope: eventbus.LocalScope, Topic: topic, AggregateID: id, Version: eventbus.EventVersion, Payload: payload, OccurredAtMS: at.UnixMilli()}
}
func sameBusEvent(a, b eventbus.Event) bool {
	return a.ID == b.ID && a.Scope == b.Scope && a.Topic == b.Topic && a.AggregateID == b.AggregateID && a.Version == b.Version && a.OccurredAtMS == b.OccurredAtMS && string(a.Payload) == string(b.Payload)
}

// appendEventTx is the shared transactional seam for intake and future domains.
// control is PRIVATE: only exact persisted cancellation/run revisions may
// bypass ordinary admission. It is not available on the public AppendEvent API.
func appendEventTx(ctx context.Context, tx *sql.Tx, e eventbus.Event, control bool) (eventbus.Event, bool, error) {
	normalized, err := eventbus.NormalizeEvent(e)
	if err != nil {
		return eventbus.Event{}, false, err
	}
	e = normalized
	if control {
		if e.Topic == supercontrol.EventRunChanged || e.Topic == supercontrol.EventDeliveryReady {
			err = validateRunControlEvent(ctx, tx, e)
		} else {
			err = validateCancellationControl(ctx, tx, e)
		}
		if err != nil {
			return eventbus.Event{}, false, err
		}
	}
	prior, err := scanBusEvent(tx.QueryRowContext(ctx, "SELECT "+busEventColumns+" FROM super_events WHERE scope=? AND event_id=?", e.Scope, e.ID))
	if err == nil {
		if !sameBusEvent(prior, e) {
			return eventbus.Event{}, false, eventbus.ErrConflict
		}
		return prior, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return eventbus.Event{}, false, busError(ctx, err)
	}
	if !control {
		var count int64
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_events").Scan(&count); err != nil {
			return eventbus.Event{}, false, busError(ctx, err)
		}
		if count >= eventbus.MaxRetainedEvents {
			return eventbus.Event{}, false, eventbus.ErrCapacity
		}
	}
	result, err := tx.ExecContext(ctx, insertBusEventSQL, e.ID, e.Scope, e.Topic, e.AggregateID, e.Version, string(e.Payload), e.OccurredAtMS)
	if err != nil {
		return eventbus.Event{}, false, busError(ctx, err)
	}
	e.Sequence, err = result.LastInsertId()
	if err != nil {
		return eventbus.Event{}, false, busError(ctx, err)
	}
	return e, true, nil
}
func validateCancellationControl(ctx context.Context, tx *sql.Tx, e eventbus.Event) error {
	if e.Scope != eventbus.LocalScope || e.Topic != eventbus.TopicWorkOrderCancelled {
		return eventbus.ErrInvalidInput
	}
	var occurred string
	err := tx.QueryRowContext(ctx, `SELECT a.occurred_at FROM work_order_events a JOIN work_orders o ON o.id=a.work_order_id WHERE o.id=? AND o.status=? AND a.kind=?`, e.AggregateID, workorder.StatusCancelled, workorder.StatusCancelled).Scan(&occurred)
	if errors.Is(err, sql.ErrNoRows) {
		return eventbus.ErrInvalidInput
	}
	if err != nil {
		return busError(ctx, err)
	}
	at, err := time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return eventbus.ErrUnavailable
	}
	if !sameBusEvent(e, intakeBusEvent(e.AggregateID, eventbus.TopicWorkOrderCancelled, at)) {
		return eventbus.ErrInvalidInput
	}
	return nil
}
func (b *Bus) AppendEvent(ctx context.Context, e eventbus.Event) (saved eventbus.Event, created bool, err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "event.append", err) }()
	if _, err = eventbus.NormalizeEvent(e); err != nil {
		return eventbus.Event{}, false, err
	}
	tx, err := b.store.db.BeginTx(ctx, nil)
	if err != nil {
		return eventbus.Event{}, false, err
	}
	defer tx.Rollback()
	saved, created, err = appendEventTx(ctx, tx, e, false)
	if err != nil {
		return eventbus.Event{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return eventbus.Event{}, false, err
	}
	return saved, created, nil
}
func (b *Bus) ReadEvents(ctx context.Context, scope string, after int64, limit int) (events []eventbus.Event, err error) {
	defer func() { err = busError(ctx, err); traceBus(ctx, "event.read", err) }()
	if err = validatePage(scope, after, limit); err != nil {
		return nil, err
	}
	rows, err := b.store.db.QueryContext(ctx, "SELECT "+busEventColumns+" FROM super_events WHERE scope=? AND sequence>? ORDER BY sequence LIMIT ?", scope, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events = []eventbus.Event{}
	for rows.Next() {
		e, scanErr := scanBusEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}
