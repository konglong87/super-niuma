package mysql

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readSessionControlMigration(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "mysql", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(contents)
}

func TestSessionControlMigrationUsesTenantScopedForeignKeys(t *testing.T) {
	up := readSessionControlMigration(t, "000025_session_control_links.up.sql")
	for _, expected := range []string{
		"UNIQUE KEY uk_agent_tasks_user_idempotency (tenant_id, user_id, idempotency_key)",
		"UNIQUE KEY uk_session_link (tenant_id, user_id, target_session_id, source_kind, source_session_key, relation_type)",
		"FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id)",
		"FOREIGN KEY (tenant_id, target_session_id) REFERENCES tenant_sessions(tenant_id, id)",
		"FOREIGN KEY (tenant_id, created_by_user_id) REFERENCES tenant_users(tenant_id, id)",
	} {
		if !strings.Contains(up, expected) {
			t.Errorf("up migration missing %q", expected)
		}
	}
}

func TestSessionControlMigrationDownDropsOnlyAddedObjects(t *testing.T) {
	down := readSessionControlMigration(t, "000025_session_control_links.down.sql")
	for _, expected := range []string{"DROP TABLE IF EXISTS tenant_session_links", "DROP INDEX uk_agent_tasks_user_idempotency", "DROP COLUMN idempotency_key"} {
		if !strings.Contains(down, expected) {
			t.Errorf("down migration missing %q", expected)
		}
	}
	dropTable := strings.Index(down, "DROP TABLE IF EXISTS tenant_session_links")
	rollbackAlter := strings.Index(down, "ALTER TABLE tenant_agent_tasks")
	if dropTable < 0 || rollbackAlter < 0 || dropTable > rollbackAlter {
		t.Fatalf("link table must be dropped before task ALTER rollback: drop=%d alter=%d", dropTable, rollbackAlter)
	}
	upper := strings.ToUpper(down)
	if strings.Contains(upper, "DELETE") || strings.Contains(upper, "TRUNCATE") || strings.Contains(upper, "DROP TABLE TENANT_AGENT_TASKS") || strings.Contains(upper, "DROP TABLE IF EXISTS TENANT_AGENT_TASKS") {
		t.Fatal("down migration must preserve tenant_agent_tasks rows")
	}
}
