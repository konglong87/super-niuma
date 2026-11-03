package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
)

func validRouterRequest(q router.CommandRequest) bool {
	return !q.Principal.LocalAdmin && control.ValidID(q.Principal.Scope) && control.ValidID(q.Principal.Owner) && control.ValidID(q.RunID) && q.ExpectedVersion > 0 && validateControlIngress(q.Ingress) == nil && (q.Action == router.ActionStatus || q.Action == router.ActionCancel)
}
func (s *Store) BeginCommand(ctx context.Context, q router.CommandRequest) (router.CommandReceipt, error) {
	if !validRouterRequest(q) {
		return router.CommandReceipt{}, control.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return router.CommandReceipt{}, controlStorageError(ctx, err)
	}
	defer tx.Rollback()
	run, err := controlRunTx(ctx, tx, q.Principal.Scope, q.RunID)
	if err != nil {
		return router.CommandReceipt{}, err
	}
	if run.Owner != q.Principal.Owner {
		return router.CommandReceipt{}, control.ErrDenied
	}
	receipt, found, err := readRouterReceipt(ctx, tx, q)
	if err != nil {
		return router.CommandReceipt{}, err
	}
	if !found {
		if run.Version != q.ExpectedVersion {
			return router.CommandReceipt{}, control.ErrConflict
		}
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_router_receipts").Scan(&count); err != nil {
			return router.CommandReceipt{}, controlStorageError(ctx, err)
		}
		if count >= routerReceiptLimit {
			return router.CommandReceipt{}, control.ErrCapacity
		}
		receipt = router.CommandReceipt{Request: q, Version: run.Version, Status: run.Status}
		_, err = tx.ExecContext(ctx, `INSERT INTO super_router_receipts(account,message_id,scope,owner,run_id,content_hash,action,run_version,snapshot_status) VALUES(?,?,?,?,?,?,?,?,?)`, q.Ingress.Account, q.Ingress.MessageID, q.Principal.Scope, q.Principal.Owner, q.RunID, q.Ingress.ContentHash, q.Action, run.Version, run.Status)
		if err != nil {
			return router.CommandReceipt{}, controlStorageError(ctx, err)
		}
	}
	// Reuse the same event/message/nonce index as CreateRun, including newly
	// observed retry aliases. There is no independent in-memory dedup authority.
	if err = controlBindIngress(ctx, tx, run, q.Ingress); err != nil {
		return router.CommandReceipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return router.CommandReceipt{}, controlStorageError(ctx, err)
	}
	return receipt, nil
}
func readRouterReceipt(ctx context.Context, tx *sql.Tx, q router.CommandRequest) (router.CommandReceipt, bool, error) {
	var scope, owner, runID, hash, action string
	var version uint64
	var status control.Status
	var raw sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT scope,owner,run_id,content_hash,action,run_version,snapshot_status,decision_json FROM super_router_receipts WHERE account=? AND message_id=?`, q.Ingress.Account, q.Ingress.MessageID).Scan(&scope, &owner, &runID, &hash, &action, &version, &status, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return router.CommandReceipt{}, false, nil
	}
	if err != nil {
		return router.CommandReceipt{}, false, controlStorageError(ctx, err)
	}
	if scope != q.Principal.Scope || owner != q.Principal.Owner {
		return router.CommandReceipt{}, false, control.ErrDenied
	}
	if runID != q.RunID || hash != q.Ingress.ContentHash || action != q.Action {
		return router.CommandReceipt{}, false, control.ErrConflict
	}
	q.ExpectedVersion = version
	receipt := router.CommandReceipt{Request: q, Version: version, Status: status}
	if raw.Valid {
		var d router.Decision
		if len(raw.String) > 1024 || json.Unmarshal([]byte(raw.String), &d) != nil || !router.ValidDecision(d) || d.RunID != runID || d.Action != action {
			return router.CommandReceipt{}, false, control.ErrUnavailable
		}
		receipt.Decision = &d
	}
	return receipt, true, nil
}
func (s *Store) FinishCommand(ctx context.Context, r router.CommandReceipt, d router.Decision) error {
	q := r.Request
	if !validRouterRequest(q) || !router.ValidDecision(d) || d.RunID != q.RunID || d.Action != q.Action || r.Version != q.ExpectedVersion {
		return control.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlStorageError(ctx, err)
	}
	defer tx.Rollback()
	stored, found, err := readRouterReceipt(ctx, tx, q)
	if err != nil {
		return err
	}
	if !found || stored.Version != r.Version || stored.Status != r.Status {
		return control.ErrConflict
	}
	if stored.Decision != nil {
		if *stored.Decision != d {
			return control.ErrConflict
		}
		return nil
	}
	data, _ := json.Marshal(d)
	result, err := tx.ExecContext(ctx, `UPDATE super_router_receipts SET decision_json=? WHERE account=? AND message_id=? AND scope=? AND owner=? AND run_version=? AND decision_json IS NULL`, string(data), q.Ingress.Account, q.Ingress.MessageID, q.Principal.Scope, q.Principal.Owner, r.Version)
	if err != nil {
		return controlStorageError(ctx, err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return control.ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return controlStorageError(ctx, err)
	}
	return nil
}
