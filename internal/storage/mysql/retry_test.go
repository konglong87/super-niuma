package mysql

import (
	"errors"
	"testing"

	driver "github.com/go-sql-driver/mysql"
)

func TestIsRetryableMySQLTransactionError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadlock", err: &driver.MySQLError{Number: 1213}, want: true},
		{name: "lock wait timeout", err: &driver.MySQLError{Number: 1205}, want: true},
		{name: "duplicate key", err: &driver.MySQLError{Number: 1062}, want: false},
		{name: "wrapped deadlock", err: errors.Join(errors.New("rollback failed"), &driver.MySQLError{Number: 1213}), want: true},
		{name: "generic", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableMySQLTransactionError(tc.err); got != tc.want {
				t.Fatalf("retryable = %v want %v", got, tc.want)
			}
		})
	}
}
