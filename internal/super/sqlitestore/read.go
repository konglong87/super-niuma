package sqlitestore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

const orderColumns = `id, title, objective, acceptance_criteria, workspace, status,
 execution_status, verification_status, delivery_status, created_at, updated_at`

type rowScanner interface{ Scan(...any) error }

func scanOrder(row rowScanner) (workorder.WorkOrder, error) {
	var order workorder.WorkOrder
	var criteria, created, updated string
	err := row.Scan(&order.ID, &order.Title, &order.Objective, &criteria, &order.Workspace, &order.Status,
		&order.ExecutionStatus, &order.VerificationStatus, &order.DeliveryStatus, &created, &updated)
	if err != nil {
		return workorder.WorkOrder{}, err
	}
	if err := json.Unmarshal([]byte(criteria), &order.AcceptanceCriteria); err != nil {
		return workorder.WorkOrder{}, err
	}
	order.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return workorder.WorkOrder{}, err
	}
	order.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return workorder.WorkOrder{}, err
	}
	return order, nil
}

func (s *Store) Get(ctx context.Context, id string) (workorder.WorkOrder, error) {
	order, err := scanOrder(s.db.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM work_orders WHERE id = ?", id))
	if err != nil {
		return workorder.WorkOrder{}, storageError(ctx, err)
	}
	return order, nil
}

func (s *Store) List(ctx context.Context, limit int) ([]workorder.WorkOrder, error) {
	if limit < 1 || limit > workorder.ListLimit {
		return nil, workorder.ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+orderColumns+" FROM work_orders ORDER BY sequence DESC LIMIT ?", limit)
	if err != nil {
		return nil, storageError(ctx, err)
	}
	defer rows.Close()
	orders := make([]workorder.WorkOrder, 0)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, storageError(ctx, err)
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(ctx, err)
	}
	return orders, nil
}
