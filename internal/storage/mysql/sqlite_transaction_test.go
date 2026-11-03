package mysql

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Interleave a competing writer after the audit transaction's first read.
// A deferred transaction lets that writer reserve the database, then fails its
// own INSERT immediately when it tries to upgrade the read lock. Reserving the
// writer at BEGIN prevents that interleaving, including with WAL enabled.
func TestSQLiteSessionControlAuditReservesWriterBeforeRead(t *testing.T) {
	for _, journalMode := range []string{"delete", "wal"} {
		t.Run(journalMode, func(t *testing.T) {
			ctx := context.Background()
			repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "audit.sqlite"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			if err := repo.db.Exec("PRAGMA journal_mode = " + journalMode).Error; err != nil {
				t.Fatal(err)
			}
			tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "audit-lock", Name: "Audit lock"})
			if err != nil {
				t.Fatal(err)
			}
			userID, err := repo.EnsureUser(ctx, tenantID, "audit-user")
			if err != nil {
				t.Fatal(err)
			}
			pool, err := repo.db.DB()
			if err != nil {
				t.Fatal(err)
			}
			// Pin a separate connection before starting the repository transaction.
			// Its zero timeout makes the competing reservation a synchronous probe,
			// not a sleep or a race against the scheduler.
			writer, err := pool.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if _, err := writer.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
				t.Fatal(err)
			}
			defer writer.ExecContext(ctx, "ROLLBACK")
			var probed, reserved bool
			const callbackName = "test:audit_competing_writer"
			if err := repo.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if probed || tx.Statement.Table != "tenants" || tx.Error != nil {
					return
				}
				probed = true
				_, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE")
				var sqliteErr sqlite3.Error
				reserved = errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrBusy
				if err != nil && !reserved {
					tx.AddError(err)
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer repo.db.Callback().Query().Remove(callbackName)

			input := SessionControlAuditInput{AuditLogInput: AuditLogInput{
				TenantID: tenantID, ActorUserID: userID, Action: "session_control.stop",
				ResourceType: "session", ResourceID: "stop-operation", MetadataJSON: `{}`,
			}}
			first, err := repo.InsertSessionControlAudit(ctx, input)
			if err != nil {
				t.Fatalf("audit after competing writer reservation (probed=%t reserved=%t): %v", probed, reserved, err)
			}
			if !probed || !reserved {
				t.Fatalf("audit must reserve writer before reading: probed=%t reserved=%t", probed, reserved)
			}
			if first.Replayed || first.Audit.ID == 0 {
				t.Fatalf("first audit = %+v", first)
			}
			replayed, err := repo.InsertSessionControlAudit(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if !replayed.Replayed || replayed.Audit.ID != first.Audit.ID {
				t.Fatalf("audit replay = %+v, original = %+v", replayed, first)
			}
			var count int64
			if err := repo.db.Table("tenant_audit_logs").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("audit rows = %d, want 1", count)
			}
		})
	}
}
