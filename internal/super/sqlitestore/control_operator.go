package sqlitestore

import (
	"context"
	"encoding/json"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

var _ control.OperatorRepository = (*Store)(nil)

// The count and row come from the same SQLite statement/snapshot. Window counts
// run before LIMIT, so collisions outside the displayed page cannot be hidden.
type operatorCountedRow struct {
	row     rowScanner
	matches *int
}

func (r operatorCountedRow) Scan(dest ...any) error { return r.row.Scan(append(dest, r.matches)...) }
func scanOperatorRun(row rowScanner) (control.Run, error) {
	var count int
	run, err := scanControlRun(operatorCountedRow{row, &count})
	if count > 1 {
		return control.Run{}, control.ErrConflict
	}
	return run, err
}

// RunForOperator resolves a scope-less Desktop ID only when globally unique.
// No current binding/profile is required to find persisted work.
func (s *Store) RunForOperator(ctx context.Context, id string) (control.Run, error) {
	if !control.ValidID(id) {
		return control.Run{}, control.ErrInvalid
	}
	run, err := scanOperatorRun(s.db.QueryRowContext(ctx, "SELECT "+controlColumns+",count(*) OVER () FROM super_runs WHERE id=? LIMIT 1", id))
	if err == control.ErrConflict {
		return control.Run{}, err
	}
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	return run, nil
}

// RecentRunsForOperator is bounded durable history, never a scope inventory
// derived from configuration. Keep the public scoped repository unchanged.
func (s *Store) RecentRunsForOperator(ctx context.Context, limit int) ([]control.Run, bool, error) {
	if limit < 1 || limit > control.MaxRuns {
		return nil, false, control.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+controlColumns+",count(*) OVER (PARTITION BY id) FROM super_runs ORDER BY rowid DESC LIMIT ?", limit+1)
	if err != nil {
		return nil, false, controlStorageError(ctx, err)
	}
	defer rows.Close()
	out := make([]control.Run, 0, limit)
	for rows.Next() {
		r, e := scanOperatorRun(rows)
		if e == control.ErrConflict {
			return nil, false, e
		}
		if e != nil {
			return nil, false, controlStorageError(ctx, e)
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, false, controlStorageError(ctx, err)
	}
	return out, false, nil
}

// EventsForOperator follows the global durable sequence across all scopes.
// This is a separate trusted read port; ReadEvents remains scope restricted.
func (s *Store) EventsForOperator(ctx context.Context, after int64, limit int) ([]eventbus.Event, error) {
	if after < 0 || limit < 1 || limit > eventbus.MaxReadLimit {
		return nil, control.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+busEventColumns+" FROM super_events WHERE sequence>? ORDER BY sequence LIMIT ?", after, limit)
	if err != nil {
		return nil, controlStorageError(ctx, err)
	}
	defer rows.Close()
	out := make([]eventbus.Event, 0)
	for rows.Next() {
		e, err := scanBusEvent(rows)
		if err != nil {
			return nil, controlStorageError(ctx, err)
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return nil, controlStorageError(ctx, err)
	}
	return out, nil
}

// DeadLettersForOperator reads notification evidence even after source bindings
// or profiles have been removed. It never acknowledges, replays or retries.
func (s *Store) DeadLettersForOperator(ctx context.Context, subscriber string, limit int) ([]eventbus.DeadLetter, error) {
	if !eventbus.ValidKey(subscriber) || limit < 1 || limit > eventbus.MaxReadLimit {
		return nil, control.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.scope,d.subscriber,d.attempts,d.code,d.created_at_ms,e.sequence,e.event_id,e.scope,e.topic,e.aggregate_id,e.version,e.payload,e.occurred_at_ms FROM super_dead_letters d JOIN super_events e ON e.sequence=d.event_sequence AND e.scope=d.scope WHERE d.subscriber=? ORDER BY d.event_sequence DESC LIMIT ?`, subscriber, limit)
	if err != nil {
		return nil, controlStorageError(ctx, err)
	}
	defer rows.Close()
	out := make([]eventbus.DeadLetter, 0)
	for rows.Next() {
		var d eventbus.DeadLetter
		var payload string
		e := &d.Event
		if err = rows.Scan(&d.Scope, &d.Subscriber, &d.Attempts, &d.Code, &d.CreatedAtMS, &e.Sequence, &e.ID, &e.Scope, &e.Topic, &e.AggregateID, &e.Version, &payload, &e.OccurredAtMS); err != nil {
			return nil, controlStorageError(ctx, err)
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, controlStorageError(ctx, err)
	}
	return out, nil
}
