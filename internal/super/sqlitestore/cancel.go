package sqlitestore

import (
	"context"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func (s *Store) Cancel(ctx context.Context, id string, at time.Time) (workorder.WorkOrder, error) {
	if at.IsZero() {
		return workorder.WorkOrder{}, workorder.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	defer tx.Rollback()
	order, err := scanOrder(tx.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM work_orders WHERE id = ?", id))
	if err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	if order.Status == workorder.StatusCancelled {
		return order, nil
	}
	// Clock adjustment must not make a work order older than its creation.
	if at.Before(order.UpdatedAt) {
		at = order.UpdatedAt
	}
	order.Status, order.UpdatedAt = workorder.StatusCancelled, at.UTC()
	_, err = tx.ExecContext(ctx, "UPDATE work_orders SET status = ?, updated_at = ? WHERE id = ?", order.Status, order.UpdatedAt.Format(time.RFC3339Nano), id)
	if err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	if err := insertEvent(ctx, tx, id, workorder.StatusCancelled, order.UpdatedAt); err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	if _, _, err := appendEventTx(ctx, tx, intakeBusEvent(id, eventbus.TopicWorkOrderCancelled, order.UpdatedAt), true); err != nil {
		traceBus(ctx, "intake.cancel", err)
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	traceBus(ctx, "intake.cancel", nil)
	return order, nil
}
