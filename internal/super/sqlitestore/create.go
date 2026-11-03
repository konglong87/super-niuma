package sqlitestore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func (s *Store) Create(ctx context.Context, key string, order workorder.WorkOrder) (workorder.WorkOrder, bool, error) {
	if order.Status != workorder.StatusRecorded || order.ExecutionStatus != workorder.NotStarted || order.VerificationStatus != workorder.NotStarted || order.DeliveryStatus != workorder.NotStarted || order.CreatedAt.IsZero() || !order.UpdatedAt.Equal(order.CreatedAt) {
		return workorder.WorkOrder{}, false, workorder.ErrInvalidInput
	}
	fingerprint, err := contentHash(order)
	if err != nil {
		return workorder.WorkOrder{}, false, workorder.ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	defer tx.Rollback()
	existing, err := findDuplicate(ctx, tx, hash([]byte(key)), fingerprint)
	if err == nil {
		return existing, false, nil
	}
	if errors.Is(err, workorder.ErrIdempotencyConflict) {
		return workorder.WorkOrder{}, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	if err := insertOrder(ctx, tx, key, fingerprint, order); err != nil {
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	if err := insertEvent(ctx, tx, order.ID, workorder.StatusRecorded, order.CreatedAt); err != nil {
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	if _, _, err := appendEventTx(ctx, tx, intakeBusEvent(order.ID, eventbus.TopicWorkOrderRecorded, order.CreatedAt), false); err != nil {
		traceBus(ctx, "intake.create", err)
		if errors.Is(err, eventbus.ErrCapacity) {
			return workorder.WorkOrder{}, false, err
		}
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return workorder.WorkOrder{}, false, storageError(ctx, err)
	}
	traceBus(ctx, "intake.create", nil)
	return order, true, nil
}

func findDuplicate(ctx context.Context, tx *sql.Tx, keyHash, fingerprint string) (workorder.WorkOrder, error) {
	var id, storedHash string
	if err := tx.QueryRowContext(ctx, "SELECT id, content_hash FROM work_orders WHERE idempotency_key_hash = ?", keyHash).Scan(&id, &storedHash); err != nil {
		return workorder.WorkOrder{}, err
	}
	if storedHash != fingerprint {
		return workorder.WorkOrder{}, workorder.ErrIdempotencyConflict
	}
	return scanOrder(tx.QueryRowContext(ctx, "SELECT "+orderColumns+" FROM work_orders WHERE id = ?", id))
}

func insertOrder(ctx context.Context, tx *sql.Tx, key, fingerprint string, order workorder.WorkOrder) error {
	criteria, err := json.Marshal(order.AcceptanceCriteria)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO work_orders (`+orderColumns+`, idempotency_key_hash, content_hash)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, order.ID, order.Title, order.Objective, string(criteria), order.Workspace, order.Status,
		order.ExecutionStatus, order.VerificationStatus, order.DeliveryStatus, order.CreatedAt.Format(time.RFC3339Nano), order.UpdatedAt.Format(time.RFC3339Nano), hash([]byte(key)), fingerprint)
	return err
}

func contentHash(order workorder.WorkOrder) (string, error) {
	content, err := json.Marshal(workorder.CreateInput{Title: order.Title, Objective: order.Objective, AcceptanceCriteria: order.AcceptanceCriteria, Workspace: order.Workspace})
	if err != nil {
		return "", err
	}
	return hash(content), nil
}
func hash(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
func insertEvent(ctx context.Context, tx *sql.Tx, id string, kind workorder.Status, at time.Time) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO work_order_events (work_order_id, kind, occurred_at) VALUES (?, ?, ?)", id, kind, at.Format(time.RFC3339Nano))
	return err
}
