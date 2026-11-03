package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

func busSchema() string {
	return fmt.Sprintf(`
CREATE TABLE super_events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 event_id TEXT NOT NULL,
 scope TEXT NOT NULL,
 topic TEXT NOT NULL,
 aggregate_id TEXT NOT NULL,
 version INTEGER NOT NULL CHECK(version > 0),
 payload TEXT NOT NULL,
 occurred_at_ms INTEGER NOT NULL CHECK(occurred_at_ms > 0),
 UNIQUE(scope,event_id)
);
CREATE INDEX super_events_scope_sequence ON super_events(scope,sequence);
CREATE INDEX super_events_scope_topic_sequence ON super_events(scope,topic,sequence);
CREATE TABLE super_subscriptions (
 scope TEXT NOT NULL,
 name TEXT NOT NULL,
 topics_json TEXT NOT NULL,
 start_position TEXT NOT NULL CHECK(start_position IN ('beginning','now')),
 max_attempts INTEGER NOT NULL CHECK(max_attempts BETWEEN 1 AND %d),
 cursor INTEGER NOT NULL DEFAULT 0 CHECK(cursor >= 0),
 current_sequence INTEGER NOT NULL DEFAULT 0 CHECK(current_sequence >= 0),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
 retry_at_ms INTEGER NOT NULL DEFAULT 0,
 owner TEXT NOT NULL DEFAULT '',
 lease_token TEXT NOT NULL DEFAULT '',
 epoch INTEGER NOT NULL DEFAULT 0 CHECK(epoch >= 0),
 lease_until_ms INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(scope,name)
);
CREATE TABLE super_dead_letters (
 scope TEXT NOT NULL,
 subscriber TEXT NOT NULL,
 event_sequence INTEGER NOT NULL REFERENCES super_events(sequence),
 attempts INTEGER NOT NULL CHECK(attempts > 0),
 code TEXT NOT NULL,
 created_at_ms INTEGER NOT NULL,
 PRIMARY KEY(scope,subscriber,event_sequence),
 FOREIGN KEY(scope,subscriber) REFERENCES super_subscriptions(scope,name)
);
`, eventbus.MaxAttempts)
}

func validateV2Schema(ctx context.Context, tx *sql.Tx) error {
	for _, query := range []string{
		"SELECT " + orderColumns + " FROM work_orders LIMIT 0",
		"SELECT sequence,work_order_id,kind,occurred_at FROM work_order_events LIMIT 0",
		"SELECT " + busEventColumns + " FROM super_events LIMIT 0",
		"SELECT " + subscriptionColumns + " FROM super_subscriptions LIMIT 0",
		"SELECT scope,subscriber,event_sequence,attempts,code,created_at_ms FROM super_dead_letters LIMIT 0",
	} {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}
