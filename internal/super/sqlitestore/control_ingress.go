package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/super/control"
)

type controlIngressKey struct{ kind, id string }

func ingressKeys(in control.Ingress) []controlIngressKey {
	keys := []controlIngressKey{{controlIngressEvent, in.EventID}, {controlIngressMessage, in.MessageID}}
	if in.Nonce != "" {
		keys = append(keys, controlIngressKey{controlIngressNonce, in.Nonce})
	}
	return keys
}
func validateControlIngress(in control.Ingress) error {
	if !control.ValidID(in.Account) || len(in.ContentHash) != 64 {
		return control.ErrInvalid
	}
	if _, err := hex.DecodeString(in.ContentHash); err != nil {
		return control.ErrInvalid
	}
	for _, k := range ingressKeys(in) {
		if k.id == "" || len(k.id) > 128 || !utf8.ValidString(k.id) || strings.ContainsAny(k.id, "\x00\n\r") {
			return control.ErrInvalid
		}
	}
	return nil
}
func controlReplay(ctx context.Context, tx *sql.Tx, r control.Run, in *control.Ingress) (control.Run, bool, error) {
	var found *control.Run
	if in != nil {
		for _, k := range ingressKeys(*in) {
			var scope, id, hash string
			err := tx.QueryRowContext(ctx, `SELECT scope,run_id,content_hash FROM super_ingress WHERE account=? AND kind=? AND identity=?`, in.Account, k.kind, k.id).Scan(&scope, &id, &hash)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return control.Run{}, false, controlStorageError(ctx, err)
			}
			if scope != r.Scope {
				return control.Run{}, false, control.ErrDenied
			}
			if hash != in.ContentHash {
				return control.Run{}, false, control.ErrConflict
			}
			old, err := controlRunTx(ctx, tx, scope, id)
			if err != nil {
				return control.Run{}, false, err
			}
			if old.Owner != r.Owner {
				return control.Run{}, false, control.ErrDenied
			}
			if found != nil && found.ID != old.ID {
				return control.Run{}, false, control.ErrConflict
			}
			found = &old
		}
	}
	old, err := scanControlRun(tx.QueryRowContext(ctx, "SELECT "+controlColumns+" FROM super_runs WHERE scope=? AND request_key=?", r.Scope, r.RequestKey))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	if err == nil {
		if old.Owner != r.Owner {
			return control.Run{}, false, control.ErrDenied
		}
		if !control.SameIntent(old, r) || (found != nil && old.ID != found.ID) {
			return control.Run{}, false, control.ErrConflict
		}
		found = &old
	}
	if found != nil {
		return *found, true, nil
	}
	// A caller may not reuse an aggregate ID under a different request key.
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM super_runs WHERE scope=? AND id=?", r.Scope, r.ID).Scan(&count); err != nil {
		return control.Run{}, false, controlStorageError(ctx, err)
	}
	if count != 0 {
		return control.Run{}, false, control.ErrConflict
	}
	return control.Run{}, false, nil
}
func controlBindIngress(ctx context.Context, tx *sql.Tx, r control.Run, in control.Ingress) error {
	missing := make([]controlIngressKey, 0, 3)
	for _, k := range ingressKeys(in) {
		var scope, id, hash string
		err := tx.QueryRowContext(ctx, `SELECT scope,run_id,content_hash FROM super_ingress WHERE account=? AND kind=? AND identity=?`, in.Account, k.kind, k.id).Scan(&scope, &id, &hash)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, k)
			continue
		}
		if err != nil {
			return controlStorageError(ctx, err)
		}
		if scope != r.Scope || id != r.ID || hash != in.ContentHash {
			return control.ErrConflict
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM super_ingress").Scan(&count); err != nil {
		return controlStorageError(ctx, err)
	}
	if count+len(missing) > controlIngressLimit {
		return control.ErrCapacity
	}
	for _, k := range missing {
		if _, err := tx.ExecContext(ctx, `INSERT INTO super_ingress(account,kind,identity,content_hash,scope,run_id) VALUES(?,?,?,?,?,?)`, in.Account, k.kind, k.id, in.ContentHash, r.Scope, r.ID); err != nil {
			return controlStorageError(ctx, err)
		}
	}
	return nil
}
