package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func (s *Store) CreateRun(ctx context.Context, r control.Run, in *control.Ingress) (control.Run, bool, error) {
	if err := validateControlRecord(r); err != nil {
		return control.Run{}, false, err
	}
	if err := validateControlInitial(r); err != nil {
		return control.Run{}, false, err
	}
	if r.Scope == control.LocalScope && r.IntakeID == "" && (in == nil || r.Grant.SourceBindingID == "" || r.Grant.SourceBindingHash == "") {
		return control.Run{}, false, control.ErrDenied
	}
	if in != nil {
		if err := validateControlIngress(*in); err != nil {
			return control.Run{}, false, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	defer tx.Rollback()
	existing, found, err := controlReplay(ctx, tx, r, in)
	if err != nil {
		return control.Run{}, false, err
	}
	if found {
		if in != nil {
			if err = controlBindIngress(ctx, tx, existing, *in); err != nil {
				return control.Run{}, false, err
			}
		}
		if err = tx.Commit(); err != nil {
			return control.Run{}, false, controlStorageError(ctx, err)
		}
		controlLog(ctx, "replayed")
		return existing, false, nil
	}
	var foreignOwners int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_runs WHERE scope=? AND owner<>?", r.Scope, r.Owner).Scan(&foreignOwners); err != nil {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	if foreignOwners != 0 {
		return control.Run{}, false, control.ErrDenied
	}
	if err = controlCapacity(ctx, tx); err != nil {
		return control.Run{}, false, err
	}
	if err = controlIntake(ctx, tx, r); err != nil {
		return control.Run{}, false, err
	}
	data, err := controlJSON(r)
	if err != nil {
		return control.Run{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO super_runs(scope,id,owner,request_key,revision,status,data) VALUES(?,?,?,?,?,?,?)`, r.Scope, r.ID, r.Owner, r.RequestKey, r.Version, r.Status, data); err != nil {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	if in != nil {
		if err = controlBindIngress(ctx, tx, r, *in); err != nil {
			return control.Run{}, false, err
		}
	}
	if err = controlAppendChanged(ctx, tx, r); err != nil {
		return control.Run{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	controlLog(ctx, "created")
	return r, true, nil
}
func validateControlInitial(r control.Run) error {
	if r.Version != 1 || (r.Status != control.Queued && r.Status != control.AwaitingApproval) || len(r.Reservations) != 0 || len(r.Notices) != 0 || r.Verification != nil || r.VerificationClaim.Token != "" {
		return control.ErrInvalid
	}
	if r.Status == control.Queued && !r.Approved {
		return control.ErrDenied
	}
	for _, j := range r.Jobs {
		if j.Status != control.JobQueued || j.Attempts != 0 || j.Claim.Token != "" {
			return control.ErrInvalid
		}
	}
	return nil
}
func controlIntake(ctx context.Context, tx *sql.Tx, r control.Run) error {
	if r.IntakeID == "" {
		if r.Scope == control.LocalScope && r.Grant.SourceBindingID == "" {
			return control.ErrDenied
		}
		return nil
	}
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM work_orders WHERE id=?", r.IntakeID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return control.ErrDenied
		}
		return controlStorageError(ctx, err)
	}
	if status != string(workorder.StatusRecorded) {
		return control.ErrDenied
	}
	return nil
}
func controlChangedEvent(r control.Run) eventbus.Event {
	return controlRunEvent(r, control.EventRunChanged)
}
func controlRunEvent(r control.Run, topic string) eventbus.Event {
	payload, _ := json.Marshal(struct {
		RunID string `json:"run_id"`
	}{r.ID})
	return eventbus.Event{ID: "run." + control.Digest([]string{r.Scope, r.ID, topic})[:40] + "." + strconv.FormatUint(r.Version, 10), Scope: r.Scope, Topic: topic, AggregateID: r.ID, Version: eventbus.EventVersion, Payload: payload, OccurredAtMS: r.UpdatedAtMS}
}
func controlAppendChanged(ctx context.Context, tx *sql.Tx, r control.Run) error {
	return controlAppendRunEvent(ctx, tx, r, control.EventRunChanged)
}
func controlAppendRunEvent(ctx context.Context, tx *sql.Tx, r control.Run, topic string) error {
	if _, _, err := appendEventTx(ctx, tx, controlRunEvent(r, topic), true); err != nil {
		return controlStorageError(ctx, err)
	}
	return nil
}

// validateRunControlEvent is the narrow A1 integration seam: bus control
// admission may dispatch here only for EventRunChanged/EventDeliveryReady. The immutable
// envelope must exactly match the aggregate revision in this transaction.
func validateRunControlEvent(ctx context.Context, tx *sql.Tx, e eventbus.Event) error {
	if e.Topic != control.EventRunChanged && e.Topic != control.EventDeliveryReady {
		return eventbus.ErrInvalidInput
	}
	r, err := controlRunTx(ctx, tx, e.Scope, e.AggregateID)
	if err != nil {
		return eventbus.ErrInvalidInput
	}
	if e.Topic == control.EventDeliveryReady && (r.Status != control.DeliveryPending || len(r.Notices) == 0) {
		return eventbus.ErrInvalidInput
	}
	expected := controlRunEvent(r, e.Topic)
	if e.ID != expected.ID || e.Version != expected.Version || e.OccurredAtMS != expected.OccurredAtMS || string(e.Payload) != string(expected.Payload) {
		return eventbus.ErrInvalidInput
	}
	return nil
}
