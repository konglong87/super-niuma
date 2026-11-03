package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/konglong87/super-niuma/internal/super/control"
)

const (
	controlRetentionLimit = 10000
	controlIngressLimit   = controlRetentionLimit * 30
	controlVerifyJob      = "super.verify"
	controlDeliveryPrefix = "super.delivery."
	controlResourceRead   = "read"
	controlResourceWrite  = "write"
	controlIngressEvent   = "event"
	controlIngressMessage = "message"
	controlIngressNonce   = "nonce"
)

// migrateControl runs inside the caller's version-2 -> version-3 transaction.
// It deliberately does not mutate application_id or user_version.
func migrateControl(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS super_runs (
 scope TEXT NOT NULL, id TEXT NOT NULL, owner TEXT NOT NULL,
 request_key TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision > 0),
 status TEXT NOT NULL, data TEXT NOT NULL,
 PRIMARY KEY(scope,id), UNIQUE(scope,request_key),
 CHECK(length(CAST(data AS BLOB)) BETWEEN 1 AND %d),
 CHECK(json_valid(data)),
 CHECK(json_extract(data,'$.scope') IS scope),
 CHECK(json_extract(data,'$.id') IS id),
 CHECK(json_extract(data,'$.owner') IS owner),
 CHECK(json_extract(data,'$.request_key') IS request_key),
 CHECK(json_extract(data,'$.version') IS revision),
 CHECK(json_extract(data,'$.status') IS status)
);
CREATE INDEX IF NOT EXISTS super_runs_active ON super_runs(status,scope,id);
CREATE TABLE IF NOT EXISTS super_ingress (
 account TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('%s','%s','%s')),
 identity TEXT NOT NULL, content_hash TEXT NOT NULL,
 scope TEXT NOT NULL, run_id TEXT NOT NULL,
 PRIMARY KEY(account,kind,identity),
 FOREIGN KEY(scope,run_id) REFERENCES super_runs(scope,id)
);
CREATE INDEX IF NOT EXISTS super_ingress_run ON super_ingress(scope,run_id);
CREATE TABLE IF NOT EXISTS super_resources (
 scope TEXT NOT NULL, run_id TEXT NOT NULL, job_id TEXT NOT NULL,
 workspace TEXT NOT NULL, mode TEXT NOT NULL CHECK(mode IN ('%s','%s')),
 owner TEXT NOT NULL, token TEXT NOT NULL, epoch INTEGER NOT NULL CHECK(epoch > 0),
 PRIMARY KEY(scope,run_id,job_id),
 FOREIGN KEY(scope,run_id) REFERENCES super_runs(scope,id)
);`, control.MaxRunBytes, controlIngressEvent, controlIngressMessage, controlIngressNonce, controlResourceRead, controlResourceWrite))
	return err
}
