package mysql

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSessionControlOperationLockAcquiresAndReleasesOnHeldConnection(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	lockDB, lockMock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer lockDB.Close()
	lockDB.SetMaxOpenConns(1)
	repo.lockDB = lockDB
	sqlDB, err := repo.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	keyHash := strings.Repeat("a", 64)
	lockMock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, ?)")).WithArgs(keyHash, sessionControlOperationLockTimeoutSeconds).WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT 1")).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(1))
	lockMock.ExpectQuery(regexp.QuoteMeta("SELECT RELEASE_LOCK(?)")).WithArgs(keyHash).WillReturnRows(sqlmock.NewRows([]string{"RELEASE_LOCK"}).AddRow(1))
	release, err := repo.AcquireSessionControlOperationLock(context.Background(), keyHash)
	if err != nil {
		t.Fatal(err)
	}
	var nested int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT 1").Scan(&nested); err != nil || nested != 1 {
		t.Fatalf("nested query value=%d err=%v", nested, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
	if err := lockMock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionControlOperationLockFailsClosedOnTimeoutNullAndReleaseFailure(t *testing.T) {
	for name, value := range map[string]any{"timeout": 0, "null": nil} {
		t.Run(name, func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			lockDB, lockMock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer lockDB.Close()
			repo.lockDB = lockDB
			keyHash := strings.Repeat("b", 64)
			lockMock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, ?)")).WithArgs(keyHash, sessionControlOperationLockTimeoutSeconds).WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK"}).AddRow(value))
			if _, err := repo.AcquireSessionControlOperationLock(context.Background(), keyHash); !errors.Is(err, ErrOperationLockUnavailable) {
				t.Fatalf("error=%v", err)
			}
			assertExpectations(t, mock)
			if err := lockMock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("release", func(t *testing.T) {
		repo, mock, closeDB := newMockGormRepository(t)
		defer closeDB()
		lockDB, lockMock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer lockDB.Close()
		repo.lockDB = lockDB
		keyHash := strings.Repeat("c", 64)
		lockMock.ExpectQuery(regexp.QuoteMeta("SELECT GET_LOCK(?, ?)")).WithArgs(keyHash, sessionControlOperationLockTimeoutSeconds).WillReturnRows(sqlmock.NewRows([]string{"GET_LOCK"}).AddRow(1))
		lockMock.ExpectQuery(regexp.QuoteMeta("SELECT RELEASE_LOCK(?)")).WithArgs(keyHash).WillReturnRows(sqlmock.NewRows([]string{"RELEASE_LOCK"}).AddRow(0))
		release, err := repo.AcquireSessionControlOperationLock(context.Background(), keyHash)
		if err != nil {
			t.Fatal(err)
		}
		if err := release(context.Background()); !errors.Is(err, ErrOperationLockUnavailable) {
			t.Fatalf("release error=%v", err)
		}
		assertExpectations(t, mock)
		if err := lockMock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
