// Package sqlitestore persists only Super intake in its own identified database.
package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/konglong87/super-niuma/internal/super/workorder"
	_ "github.com/mattn/go-sqlite3"
)

const (
	busyTimeoutMilliseconds = 5000
	maxOpenConnections      = 1
)

type Store struct{ db *sql.DB }

var _ workorder.Repository = (*Store)(nil)

func Open(ctx context.Context, dbPath string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := preparePath(dbPath)
	if err != nil {
		return nil, errOpen
	}
	uri := url.URL{Scheme: "file", Path: path}
	query := url.Values{"_busy_timeout": {strconv.Itoa(busyTimeoutMilliseconds)}, "_foreign_keys": {"on"}, "_synchronous": {"FULL"}, "_txlock": {"immediate"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return nil, errOpen
	}
	db.SetMaxOpenConns(maxOpenConnections)
	db.SetMaxIdleConns(maxOpenConnections)
	if err := initialize(ctx, db); err != nil {
		db.Close()
		return nil, openError(ctx)
	}
	// Only change journal mode after proving the database is dedicated to Super.
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil || mode != "wal" {
		db.Close()
		return nil, openError(ctx)
	}
	if err := checkPrivateFile(path); err != nil {
		db.Close()
		return nil, errOpen
	}
	slog.DebugContext(ctx, "private work-order store opened", "component", "super.sqlitestore", "action", "store.open")
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return workorder.ErrUnavailable
	}
	slog.Debug("work-order store closed", "component", "super.sqlitestore", "action", "store.close")
	return nil
}

func openError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errOpen
}
func storageError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, sql.ErrNoRows) {
		return workorder.ErrNotFound
	}
	return workorder.ErrUnavailable
}
