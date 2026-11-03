package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

// migrateV1 is called only within initialize's transaction. Historical audit is
// not new admission: preserve it even above the ordinary event soft quota.
func migrateV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, busSchema()); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT work_order_id,kind,occurred_at FROM work_order_events ORDER BY sequence")
	if err != nil {
		return err
	}
	defer rows.Close()
	stmt, err := tx.PrepareContext(ctx, insertBusEventSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	count := 0
	for rows.Next() {
		var id, kind, at string
		if err = rows.Scan(&id, &kind, &at); err != nil {
			return err
		}
		occurred, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return errOpen
		}
		topic := ""
		switch workorder.Status(kind) {
		case workorder.StatusRecorded:
			topic = eventbus.TopicWorkOrderRecorded
		case workorder.StatusCancelled:
			topic = eventbus.TopicWorkOrderCancelled
		default:
			return errOpen
		}
		event, err := eventbus.NormalizeEvent(intakeBusEvent(id, topic, occurred))
		if err != nil {
			return errOpen
		}
		if _, err = stmt.ExecContext(ctx, event.ID, event.Scope, event.Topic, event.AggregateID, event.Version, string(event.Payload), event.OccurredAtMS); err != nil {
			return err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = validateV2Schema(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	slog.DebugContext(ctx, "Super audit backfill prepared for atomic migration", "component", "super.sqlitestore", "action", "migration.prepare", "version", schemaVersion, "event_count", count)
	return nil
}
