package sqlitestore

import (
	"context"
	"database/sql"
	"github.com/konglong87/super-niuma/internal/super/router"
)

const routerReceiptLimit = 10000

var _ router.CommandReceipts = (*Store)(nil)

// MigrateRouter must run in the coordinator's schema migration transaction,
// after migrateControl. It never changes application_id/user_version and is
// never called lazily by a command handler.
func MigrateRouter(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS super_router_receipts (
 account TEXT NOT NULL, message_id TEXT NOT NULL,
 scope TEXT NOT NULL, owner TEXT NOT NULL, run_id TEXT NOT NULL,
 content_hash TEXT NOT NULL, action TEXT NOT NULL CHECK(action IN ('status','cancel')),
 run_version INTEGER NOT NULL CHECK(run_version>0), snapshot_status TEXT NOT NULL,
 decision_json TEXT,
 PRIMARY KEY(account,message_id),
 FOREIGN KEY(scope,run_id) REFERENCES super_runs(scope,id)
 );`)
	return err
}
