package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"golang.org/x/sync/semaphore"
)

const sessionControlOperationLockTimeoutSeconds = 5

var sqliteOperationLocks sync.Map

// AcquireSessionControlOperationLock uses a dedicated advisory-lock pool.
// The returned release closure always runs RELEASE_LOCK on the exact sql.Conn
// that acquired it, while nested repository work remains free to use db.
func (r *GormRepository) AcquireSessionControlOperationLock(ctx context.Context, keyHash string) (func(context.Context) error, error) {
	if r == nil || !validHandoffOperationIdentity(keyHash) {
		return nil, ErrOperationLockUnavailable
	}
	if isSQLite(r.db) {
		lock := semaphore.NewWeighted(1)
		actual, _ := sqliteOperationLocks.LoadOrStore(keyHash, lock)
		lock = actual.(*semaphore.Weighted)
		// Canceled waiters must leave the queue without acquiring an orphaned
		// lock later. Weighted.Acquire owns that cancellation race.
		if err := lock.Acquire(ctx, 1); err != nil {
			return nil, fmt.Errorf("%w: acquire: %w", ErrOperationLockUnavailable, err)
		}
		var once sync.Once
		return func(context.Context) error {
			once.Do(func() { lock.Release(1) })
			return nil
		}, nil
	}
	if r.lockDB == nil {
		return nil, ErrOperationLockUnavailable
	}
	conn, err := r.lockDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire connection", ErrOperationLockUnavailable)
	}
	var acquired sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", keyHash, sessionControlOperationLockTimeoutSeconds).Scan(&acquired); err != nil || !acquired.Valid || acquired.Int64 != 1 {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: acquire", ErrOperationLockUnavailable)
	}
	var once sync.Once
	var releaseErr error
	release := func(releaseCtx context.Context) error {
		once.Do(func() {
			defer conn.Close()
			var released sql.NullInt64
			if err := conn.QueryRowContext(releaseCtx, "SELECT RELEASE_LOCK(?)", keyHash).Scan(&released); err != nil || !released.Valid || released.Int64 != 1 {
				releaseErr = fmt.Errorf("%w: release", ErrOperationLockUnavailable)
			}
		})
		return releaseErr
	}
	return release, nil
}
