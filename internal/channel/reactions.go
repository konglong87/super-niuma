package channel

import "context"

type ReactionEmoji string

const (
	ReactionTyping ReactionEmoji = "Typing"
	ReactionDone   ReactionEmoji = "DONE"
	ReactionError  ReactionEmoji = "ERROR"
)

func (e ReactionEmoji) Valid() bool {
	switch e {
	case ReactionTyping, ReactionDone, ReactionError:
		return true
	default:
		return false
	}
}

// ReactionAdapter is optional. Providers without reaction APIs keep the core
// message/card delivery contract and simply do not enable reconciliation.
type ReactionAdapter interface {
	CreateReaction(context.Context, string, ReactionEmoji) (reactionID string, err error)
	DeleteReaction(context.Context, string, string) error
}
