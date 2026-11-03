package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const (
	applicationID = 0x53555052 // SUPR, not the legacy Session database.
	schemaVersion = 1
)

func initialize(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var appID, version, tables int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&appID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return err
	}
	switch {
	case appID == applicationID && version == schemaVersion:
		// Do not run schema migrations on an existing or foreign application.
		rows, err := tx.QueryContext(ctx, "SELECT "+orderColumns+" FROM work_orders LIMIT 0")
		if err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	case appID == 0 && version == 0 && tables == 0:
		if _, err := tx.ExecContext(ctx, schema()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA application_id = %d; PRAGMA user_version = %d", applicationID, schemaVersion)); err != nil {
			return err
		}
	default:
		return errOpen
	}
	return tx.Commit()
}

func schema() string {
	return fmt.Sprintf(`
CREATE TABLE work_orders (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 idempotency_key_hash TEXT NOT NULL UNIQUE,
 content_hash TEXT NOT NULL,
 title TEXT NOT NULL,
 objective TEXT NOT NULL,
 acceptance_criteria TEXT NOT NULL,
 workspace TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('%[1]s','%[2]s')),
 execution_status TEXT NOT NULL CHECK(execution_status = '%[3]s'),
 verification_status TEXT NOT NULL CHECK(verification_status = '%[3]s'),
 delivery_status TEXT NOT NULL CHECK(delivery_status = '%[3]s'),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE work_order_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 work_order_id TEXT NOT NULL REFERENCES work_orders(id),
 kind TEXT NOT NULL CHECK(kind IN ('%[1]s','%[2]s')),
 occurred_at TEXT NOT NULL,
 UNIQUE(work_order_id, kind)
);`, workorder.StatusRecorded, workorder.StatusCancelled, workorder.NotStarted)
}
