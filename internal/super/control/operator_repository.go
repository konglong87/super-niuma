package control

import (
	"context"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
)

// OperatorRepository is a trusted Desktop administration port. It is deliberately
// separate from Repository and must never be selected by an external Router.
// Records come from durable history, independent of current channel bindings.
// An ID shared by multiple scopes must return ErrConflict, never a first match.
type OperatorRepository interface {
	RecentRunsForOperator(context.Context, int) ([]Run, bool, error)
	RunForOperator(context.Context, string) (Run, error)
	EventsForOperator(context.Context, int64, int) ([]eventbus.Event, error)
	DeadLettersForOperator(context.Context, string, int) ([]eventbus.DeadLetter, error)
}
