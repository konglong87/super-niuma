package mysql

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteOperationLockCancellationAllowsRetry(t *testing.T) {
	const waitTimeout = 20 * time.Millisecond
	const retryTimeout = time.Second
	repo, err := OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "locks.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name())))
	release, err := repo.AcquireSessionControlOperationLock(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer release(ctx)
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()
	if _, err := repo.AcquireSessionControlOperationLock(waitCtx, key); !errors.Is(err, ErrOperationLockUnavailable) {
		t.Fatalf("contending wait = %v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(waitTimeout)
	retryCtx, retryCancel := context.WithTimeout(ctx, retryTimeout)
	defer retryCancel()
	releaseAgain, err := repo.AcquireSessionControlOperationLock(retryCtx, key)
	if err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
	defer releaseAgain(ctx)
}

func TestSQLiteOperationLockRejectsCanceledContextAndIsIdempotent(t *testing.T) {
	repo, err := OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "locks.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	ctx := context.Background()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name())))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.AcquireSessionControlOperationLock(canceled, key); !errors.Is(err, ErrOperationLockUnavailable) {
		t.Fatalf("canceled context = %v", err)
	}
	ready, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	release, err := repo.AcquireSessionControlOperationLock(ready, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
}
