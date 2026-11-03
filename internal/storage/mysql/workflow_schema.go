package mysql

import (
	"context"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

type workflowSchemaRequirement struct {
	model           any
	uniqueColumns   [][]string
	nullableColumns []string
}

// WorkflowSchemaReady is a read-only, uncached composition-root probe. It must
// run BEFORE NewWorkflowStore: a legacy database is not an instruction to run
// production migrations or enable Task guards. Missing/partial schema returns
// false; connection, permission and canceled-context errors are not swallowed.
func (r *GormRepository) WorkflowSchemaReady(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r == nil || r.db == nil {
		return false, fmt.Errorf("workflow schema probe requires an initialized repository")
	}
	db := r.db.WithContext(ctx)
	migrator := db.Migrator()
	// HasTable/HasColumn return only bool and discard metadata-query errors.
	// Use error-returning Migrator APIs so a failed connection is distinguishable
	// from an ordinary installation which has not applied migration 000029.
	tables, err := migrator.GetTables()
	if err != nil {
		return false, fmt.Errorf("inspect workflow schema tables: %w", err)
	}
	present := make(map[string]bool, len(tables))
	for _, table := range tables {
		present[table] = true
	}
	requirements := []workflowSchemaRequirement{
		{model: &gormWorkflowRun{}, uniqueColumns: [][]string{{"tenant_id", "owner_user_id", "idempotency_key_hash"}}, nullableColumns: []string{"inbox_event_id", "source_account_id"}},
		{model: &gormAgentTeamRunEvent{}, uniqueColumns: [][]string{{"tenant_id", "team_run_id", "sequence_no"}}},
		{model: &gormWorkflowNode{}},
		{model: &gormWorkflowAttempt{}, uniqueColumns: [][]string{{"task_id"}}},
		{model: &gormWorkflowArtifact{}},
		{model: &gormWorkflowTaskBinding{}},
	}
	for _, requirement := range requirements {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(requirement.model); err != nil {
			return false, fmt.Errorf("inspect workflow schema model: %w", err)
		}
		if !present[statement.Table] {
			return false, nil
		}
		ready, err := workflowSchemaTableReady(migrator, statement, requirement)
		if err != nil {
			return false, fmt.Errorf("inspect workflow schema %s: %w", statement.Table, err)
		}
		if !ready {
			return false, nil
		}
	}
	return true, nil
}

func workflowSchemaTableReady(migrator gorm.Migrator, statement *gorm.Statement, requirement workflowSchemaRequirement) (bool, error) {
	columns, err := migrator.ColumnTypes(requirement.model)
	if err != nil {
		return false, err
	}
	byName := make(map[string]gorm.ColumnType, len(columns))
	primaryCount := 0
	for _, column := range columns {
		byName[column.Name()] = column
		if primary, known := column.PrimaryKey(); known && primary {
			primaryCount++
		}
	}
	// Derive required columns/primary keys from the actual write models rather
	// than maintaining a second list that could silently miss a new V2 field.
	for _, name := range statement.Schema.DBNames {
		if _, ok := byName[name]; !ok {
			return false, nil
		}
	}
	if primaryCount != len(statement.Schema.PrimaryFields) {
		return false, nil
	}
	for _, field := range statement.Schema.PrimaryFields {
		primary, known := byName[field.DBName].PrimaryKey()
		if !known || !primary {
			return false, nil
		}
	}
	for _, name := range requirement.nullableColumns {
		nullable, known := byName[name].Nullable()
		if !known || !nullable {
			return false, nil
		}
	}
	if len(requirement.uniqueColumns) == 0 {
		return true, nil
	}
	indexes, err := migrator.GetIndexes(requirement.model)
	if err != nil {
		return false, err
	}
	for _, required := range requirement.uniqueColumns {
		found := false
		for _, index := range indexes {
			unique, known := index.Unique()
			if known && unique && reflect.DeepEqual(index.Columns(), required) {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}
