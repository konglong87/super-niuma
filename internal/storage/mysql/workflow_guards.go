package mysql

import (
	"context"
	"errors"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Task IDs are globally unique. Never trust editable task metadata to decide
// ownership. The binding is created with its task, in the Run CAS transaction,
// and remains authoritative even after the task/run reaches a terminal state.
const workflowUnownedTaskPredicate = `NOT EXISTS (
 SELECT 1 FROM agent_team_workflow_task_bindings AS workflow_owner
 WHERE workflow_owner.task_id = tenant_agent_tasks.id
)`

func (r *GormRepository) workflowTaskWriteQuery(query *gorm.DB) *gorm.DB {
	if r.workflowTaskGuards.Load() {
		return query.Where(workflowUnownedTaskPredicate)
	}
	return query
}

// Scoped legacy methods must not reveal another owner's binding, including
// when a caller supplies an empty scope. Finish/Event retain their old globally
// addressed TaskID contract and use the explicit unscoped helper below.
func (r *GormRepository) checkWorkflowTaskOwnership(db *gorm.DB, taskID, tenantID, userID uint64) error {
	if !r.workflowTaskGuards.Load() {
		return nil
	}
	return workflowTaskBindingError(db.Model(&gormWorkflowTaskBinding{}).Where("task_id = ? AND tenant_id = ? AND user_id = ?", taskID, tenantID, userID))
}

func (r *GormRepository) checkAnyWorkflowTaskOwnership(db *gorm.DB, taskID uint64) error {
	if !r.workflowTaskGuards.Load() {
		return nil
	}
	return workflowTaskBindingError(db.Model(&gormWorkflowTaskBinding{}).Where("task_id = ?", taskID))
}

func workflowTaskBindingError(query *gorm.DB) error {
	var binding gormWorkflowTaskBinding
	err := query.Take(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return workflow.ErrOwnedTask
}

// A SELECT FOR UPDATE on the parent task protects the check and event append
// together. Store only creates bindings for NEW tasks in their creation
// transaction, never adopts an existing ordinary task. MySQL additionally
// protects parent identity through the binding FK; SQLite reserves its writer
// at BEGIN IMMEDIATE. Store's direct transaction projections do not use this
// legacy entry point and retain exclusive authority to append owned-task events.
func (r *GormRepository) appendWorkflowGuardedTaskEvent(ctx context.Context, row *gormAgentTaskEvent) error {
	return r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var task struct {
			ID uint64 `gorm:"column:id"`
		}
		if err := tx.Table("tenant_agent_tasks").Select("id").Where("id = ?", row.TaskID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&task).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := r.checkAnyWorkflowTaskOwnership(tx, row.TaskID); err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if isSQLite(tx) {
			return tx.Table(row.TableName()).Where("id = ?", row.ID).Update("created_at", time.Now().UTC()).Error
		}
		return nil
	})
}
