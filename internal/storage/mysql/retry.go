package mysql

import (
	"context"
	"errors"
	"time"

	driver "github.com/go-sql-driver/mysql"
)

const retryableTransactionAttempts = 3

func withRetryableTransaction(ctx context.Context, fn func() error) error {
	var err error
	for attempt := 0; attempt < retryableTransactionAttempts; attempt++ {
		err = fn()
		if !isRetryableMySQLTransactionError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
	return err
}

func isRetryableMySQLTransactionError(err error) bool {
	var mysqlErr *driver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1205 || mysqlErr.Number == 1213
}
