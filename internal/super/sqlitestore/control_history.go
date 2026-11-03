package sqlitestore

import (
	"context"

	"github.com/konglong87/super-niuma/internal/super/control"
)

// RecentRuns is a bounded presentation projection, not the active scheduler
// inventory. The explicit more flag prevents a growing terminal history from
// either taking down the Desktop or pretending the first page is exhaustive.
func (s *Store) RecentRuns(ctx context.Context, scope string, limit int) ([]control.Run, bool, error) {
	if !control.ValidID(scope) || limit < 1 || limit > control.MaxRuns {
		return nil, false, control.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+controlColumns+" FROM super_runs WHERE scope=? ORDER BY rowid DESC LIMIT ?", scope, limit+1)
	if err != nil {
		return nil, false, controlStorageError(ctx, err)
	}
	defer rows.Close()
	out := []control.Run{}
	for rows.Next() {
		r, err := scanControlRun(rows)
		if err != nil {
			return nil, false, controlStorageError(ctx, err)
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
