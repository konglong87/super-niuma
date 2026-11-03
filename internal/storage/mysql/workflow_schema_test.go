package mysql

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type workflowSchemaObject struct{ Type, Name, SQL string }

func workflowSchemaSnapshot(t *testing.T, repo *GormRepository) []workflowSchemaObject {
	t.Helper()
	var objects []workflowSchemaObject
	if err := repo.db.Raw("SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name").Scan(&objects).Error; err != nil {
		t.Fatal(err)
	}
	return objects
}
func workflowReadOnlyProbe(t *testing.T, repo *GormRepository, want bool) {
	t.Helper()
	pool, err := repo.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	before := workflowSchemaSnapshot(t, repo)
	guardBefore := repo.workflowTaskGuards.Load()
	// Keep every probe query on this connection and make accidental DDL/DML a
	// real SQLite error, not just an assertion about generated SQL strings.
	if err := repo.db.Exec("PRAGMA query_only = ON").Error; err != nil {
		t.Fatal(err)
	}
	ready, probeErr := repo.WorkflowSchemaReady(context.Background())
	if err := repo.db.Exec("PRAGMA query_only = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if probeErr != nil || ready != want {
		t.Fatalf("read-only readiness=%v want=%v error=%v", ready, want, probeErr)
	}
	if repo.workflowTaskGuards.Load() != guardBefore {
		t.Fatal("readiness probe changed the Task guard")
	}
	if after := workflowSchemaSnapshot(t, repo); !reflect.DeepEqual(before, after) {
		t.Fatal("readiness probe changed the schema")
	}
}

func TestWorkflowSchemaReadyLegacySQLiteAndUpgrade(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// A real pre-V2 desktop database: ordinary tasks and the legacy Run model,
	// without workflow_version, run-events, or any V2 projection/binding table.
	if err := db.AutoMigrate(&gormAgentTask{}, &gormAgentTaskEvent{}, &gormAgentTeamRun{}); err != nil {
		t.Fatal(err)
	}
	legacy := NewGormRepository(db, nil)
	t.Cleanup(func() { _ = legacy.Close() })
	taskID, err := legacy.CreateAgentTask(ctx, agenttasks.TaskInput{TenantID: 1, UserID: 1, Status: agenttasks.StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	workflowReadOnlyProbe(t, legacy, false)
	if legacy.db.Migrator().HasColumn(&gormWorkflowRun{}, "workflow_version") || legacy.db.Migrator().HasTable(&gormWorkflowTaskBinding{}) {
		t.Fatal("readiness probe auto-migrated the legacy schema")
	}
	if err := legacy.FinishAgentTask(ctx, taskID, "", `{}`); err != nil {
		t.Fatalf("probe broke ordinary legacy finish: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	// Only the explicit desktop open/migration may upgrade the schema.
	upgraded, err := OpenSQLiteGormRepository(ctx, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	workflowReadOnlyProbe(t, upgraded, true)
	if upgraded.workflowTaskGuards.Load() {
		t.Fatal("ready schema silently enabled Task guards")
	}
	taskID, err = upgraded.CreateAgentTask(ctx, agenttasks.TaskInput{TenantID: 1, UserID: 1, Status: agenttasks.StatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := upgraded.FinishAgentTask(ctx, taskID, "", `{}`); err != nil {
		t.Fatalf("ready probe broke ordinary task finish: %v", err)
	}
}

func TestWorkflowSchemaReadyRejectsPartialTablesAndColumns(t *testing.T) {
	for _, model := range []interface{ TableName() string }{gormAgentTeamRunEvent{}, gormWorkflowNode{}, gormWorkflowAttempt{}, gormWorkflowArtifact{}, gormWorkflowTaskBinding{}} {
		t.Run(model.TableName(), func(t *testing.T) {
			repo, _, _ := workflowTestRepo(t)
			if err := repo.db.Migrator().DropTable(model); err != nil {
				t.Fatal(err)
			}
			workflowReadOnlyProbe(t, repo, false)
		})
	}
	t.Run("missing_checkpoint_column", func(t *testing.T) {
		repo, _, _ := workflowTestRepo(t)
		if err := repo.db.Migrator().DropColumn(&gormWorkflowRun{}, "workflow_state_json"); err != nil {
			t.Fatal(err)
		}
		workflowReadOnlyProbe(t, repo, false)
	})
	t.Run("version_column_without_binding_shape", func(t *testing.T) {
		repo, _, _ := workflowTestRepo(t)
		if err := repo.db.Migrator().DropColumn(&gormWorkflowTaskBinding{}, "node_id"); err != nil {
			t.Fatal(err)
		}
		workflowReadOnlyProbe(t, repo, false)
	})
}

func TestWorkflowSchemaReadyRequiresUniqueConstraints(t *testing.T) {
	cases := []struct {
		name  string
		model any
	}{
		{"uk_workflow_scoped_key", &gormWorkflowRun{}},
		{"uk_workflow_run_event_sequence", &gormAgentTeamRunEvent{}},
		{"uk_workflow_attempt_task", &gormWorkflowAttempt{}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo, _, _ := workflowTestRepo(t)
			if err := repo.db.Migrator().DropIndex(tt.model, tt.name); err != nil {
				t.Fatal(err)
			}
			workflowReadOnlyProbe(t, repo, false)
		})
	}
	t.Run("nonunique_same_name", func(t *testing.T) {
		repo, _, _ := workflowTestRepo(t)
		if err := repo.db.Exec("DROP INDEX uk_workflow_scoped_key").Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.db.Exec("CREATE INDEX uk_workflow_scoped_key ON agent_team_runs (tenant_id, owner_user_id, idempotency_key_hash)").Error; err != nil {
			t.Fatal(err)
		}
		workflowReadOnlyProbe(t, repo, false)
	})
}

func TestWorkflowSchemaReadyPreservesEnabledGuard(t *testing.T) {
	repo, _, _ := workflowTestRepo(t)
	_ = NewWorkflowStore(repo)
	workflowReadOnlyProbe(t, repo, true)
	if err := repo.db.Migrator().DropTable(&gormWorkflowTaskBinding{}); err != nil {
		t.Fatal(err)
	}
	workflowReadOnlyProbe(t, repo, false)
	if !repo.workflowTaskGuards.Load() {
		t.Fatal("missing schema probe disabled an existing ownership defense")
	}
}

func TestWorkflowSchemaReadyPropagatesDatabaseAndContextErrors(t *testing.T) {
	repo, _, _ := workflowTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ready, err := repo.WorkflowSchemaReady(ctx); ready || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe ready=%v error=%v", ready, err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if ready, err := repo.WorkflowSchemaReady(context.Background()); ready || err == nil {
		t.Fatalf("closed connection silently treated as missing schema: %v %v", ready, err)
	}
	if repo.workflowTaskGuards.Load() {
		t.Fatal("failed probe enabled guards")
	}
	var missing *GormRepository
	if ready, err := missing.WorkflowSchemaReady(context.Background()); ready || err == nil {
		t.Fatalf("nil repository probe: %v %v", ready, err)
	}
}
