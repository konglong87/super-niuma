package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

var _ control.Repository = (*Store)(nil)

const controlColumns = `scope,id,owner,request_key,revision,status,data`
const controlActivePredicate = `(status NOT IN (?,?,?,?) OR EXISTS (SELECT 1 FROM super_resources sr WHERE sr.scope=super_runs.scope AND sr.run_id=super_runs.id) OR EXISTS (SELECT 1 FROM json_each(data,'$.notices') AS notice WHERE json_extract(notice.value,'$.status')='sending'))`

func controlStorageError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return control.ErrNotFound
	}
	slog.WarnContext(ctx, "Super control storage operation failed", "component", "super.sqlitestore", "code", "storage_failed")
	return control.ErrUnavailable
}
func scanControlRun(row rowScanner) (control.Run, error) {
	var r control.Run
	var scope, id, owner, key, status string
	var revision uint64
	var data []byte
	if err := row.Scan(&scope, &id, &owner, &key, &revision, &status, &data); err != nil {
		return r, err
	}
	if len(data) == 0 || len(data) > control.MaxRunBytes {
		return r, control.ErrUnavailable
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return control.Run{}, control.ErrUnavailable
	}
	if r.Scope != scope || r.ID != id || r.Owner != owner || r.RequestKey != key || r.Version != revision || string(r.Status) != status || control.ValidateRun(r) != nil {
		return control.Run{}, control.ErrUnavailable
	}
	return r, nil
}
func controlRunTx(ctx context.Context, tx *sql.Tx, scope, id string) (control.Run, error) {
	r, err := scanControlRun(tx.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM super_runs WHERE scope=? AND id=?", scope, id))
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	return r, nil
}
func (s *Store) GetRun(ctx context.Context, scope, id string) (control.Run, error) {
	if !control.ValidID(scope) || !control.ValidID(id) {
		return control.Run{}, control.ErrInvalid
	}
	r, err := scanControlRun(s.db.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM super_runs WHERE scope=? AND id=?", scope, id))
	if err != nil {
		return control.Run{}, controlStorageError(ctx, err)
	}
	return r, nil
}
func (s *Store) ListRuns(ctx context.Context, scope string) ([]control.Run, error) {
	if !control.ValidID(scope) {
		return nil, control.ErrInvalid
	}
	return s.controlRuns(ctx, "scope=?", scope)
}

// ActiveRuns is a trusted scheduler port, not an end-user listing. Terminal
// roots with unresolved resource holders remain visible to recovery operators.
func (s *Store) ActiveRuns(ctx context.Context) ([]control.Run, error) {
	return s.controlRuns(ctx, controlActivePredicate, control.Delivered, control.Cancelled, control.Failed, control.NeedsAttention)
}
func (s *Store) controlRuns(ctx context.Context, predicate string, args ...any) ([]control.Run, error) {
	args = append(args, control.MaxRuns+1)
	rows, err := s.db.QueryContext(ctx, "SELECT "+controlColumns+" FROM super_runs WHERE "+predicate+" ORDER BY scope,id LIMIT ?", args...)
	if err != nil {
		return nil, controlStorageError(ctx, err)
	}
	defer rows.Close()
	result := make([]control.Run, 0)
	for rows.Next() {
		r, err := scanControlRun(rows)
		if err != nil {
			return nil, controlStorageError(ctx, err)
		}
		result = append(result, r)
		if len(result) > control.MaxRuns {
			return nil, control.ErrCapacity
		}
	}
	if err = rows.Err(); err != nil {
		return nil, controlStorageError(ctx, err)
	}
	return result, nil
}
func controlCapacity(ctx context.Context, tx *sql.Tx) error {
	var total, active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM super_runs").Scan(&total); err != nil {
		return controlStorageError(ctx, err)
	}
	if total >= controlRetentionLimit {
		return control.ErrCapacity
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM super_runs WHERE "+controlActivePredicate, control.Delivered, control.Cancelled, control.Failed, control.NeedsAttention).Scan(&active); err != nil {
		return controlStorageError(ctx, err)
	}
	if active >= control.MaxRuns {
		return control.ErrCapacity
	}
	// New roots are ordinary admission; only later state obligations bypass
	// the bus soft quota. Replays reach this function only when truly new.
	var events int64
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM super_events").Scan(&events); err != nil {
		return controlStorageError(ctx, err)
	}
	if events >= eventbus.MaxRetainedEvents {
		return control.ErrCapacity
	}
	return nil
}
func validateControlRecord(r control.Run) error {
	if err := control.ValidateRun(r); err != nil {
		return err
	}
	if r.Version > uint64(^uint64(0)>>1) || r.CreatedAtMS <= 0 || r.UpdatedAtMS < r.CreatedAtMS {
		return control.ErrInvalid
	}
	if r.IntakeID != "" && !control.ValidID(r.IntakeID) {
		return control.ErrInvalid
	}
	return nil
}
func controlJSON(r control.Run) (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", control.ErrInvalid
	}
	if len(data) > control.MaxRunBytes {
		return "", control.ErrCapacity
	}
	return string(data), nil
}
func controlLog(ctx context.Context, action string) {
	slog.DebugContext(ctx, "Super control transaction committed", "component", "super.sqlitestore", "action", fmt.Sprintf("control.%s", action))
}
