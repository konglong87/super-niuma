package runtime

import (
	"context"
	"errors"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const defaultReactionBatchSize = 16

type ReactionRepository interface {
	UpsertChannelReactionDesired(context.Context, mysqlstore.ChannelReactionInput) error
	ClaimDueChannelReactions(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelReaction, error)
	MarkChannelReactionApplied(context.Context, uint64, uint64, uint64, string, string, string) error
	MarkChannelReactionDeleted(context.Context, uint64, uint64, uint64, string) error
	MarkChannelReactionRetry(context.Context, uint64, uint64, uint64, string, time.Time, string, string) error
}

type ReactionConfig struct {
	TenantID   uint64
	AccountID  uint64
	WorkerID   string
	Repo       ReactionRepository
	Adapter    channelcontract.ReactionAdapter
	BatchSize  int
	Lease      time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

type ReactionReconciler struct{ cfg ReactionConfig }

func NewReactionReconciler(cfg ReactionConfig) *ReactionReconciler {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultReactionBatchSize
	}
	if cfg.Lease <= 0 {
		cfg.Lease = DefaultOutboxLease
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = DefaultRetryDelay
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &ReactionReconciler{cfg: cfg}
}

func (r *ReactionReconciler) SetDesired(ctx context.Context, conversationID uint64, providerMessageID string, emoji channelcontract.ReactionEmoji) error {
	if r == nil || r.cfg.Repo == nil || conversationID == 0 || providerMessageID == "" || !emoji.Valid() {
		return errors.New("channel reaction: invalid desired state")
	}
	return r.cfg.Repo.UpsertChannelReactionDesired(ctx, mysqlstore.ChannelReactionInput{TenantID: r.cfg.TenantID, AccountID: r.cfg.AccountID, ConversationID: conversationID, ProviderMessageID: providerMessageID, DesiredEmoji: string(emoji)})
}

func (r *ReactionReconciler) ProcessOne(ctx context.Context) error {
	if r == nil || r.cfg.Repo == nil || r.cfg.Adapter == nil {
		return nil
	}
	rows, err := r.cfg.Repo.ClaimDueChannelReactions(ctx, r.cfg.TenantID, r.cfg.AccountID, r.cfg.WorkerID, r.cfg.BatchSize, r.cfg.Now().Add(r.cfg.Lease))
	if err != nil {
		return err
	}
	for _, row := range rows {
		r.reconcile(ctx, row)
	}
	return nil
}

func (r *ReactionReconciler) reconcile(ctx context.Context, row mysqlstore.ChannelReaction) {
	if row.CurrentEmoji != "" && row.CurrentEmoji != row.DesiredEmoji {
		if err := r.cfg.Adapter.DeleteReaction(ctx, row.ProviderMessageID, row.ReactionID); err != nil {
			r.retry(ctx, row, "reaction_delete_failed", err)
			return
		}
		if err := r.cfg.Repo.MarkChannelReactionDeleted(ctx, row.TenantID, row.AccountID, row.ID, r.cfg.WorkerID); err != nil {
			r.retry(ctx, row, "reaction_delete_persist_failed", err)
		}
		return
	}
	if row.CurrentEmoji == row.DesiredEmoji && row.ReactionID != "" {
		_ = r.cfg.Repo.MarkChannelReactionApplied(ctx, row.TenantID, row.AccountID, row.ID, r.cfg.WorkerID, row.CurrentEmoji, row.ReactionID)
		return
	}
	reactionID, err := r.cfg.Adapter.CreateReaction(ctx, row.ProviderMessageID, channelcontract.ReactionEmoji(row.DesiredEmoji))
	if err != nil || reactionID == "" {
		if err == nil {
			err = errors.New("provider returned empty reaction id")
		}
		r.retry(ctx, row, "reaction_create_failed", err)
		return
	}
	if err := r.cfg.Repo.MarkChannelReactionApplied(ctx, row.TenantID, row.AccountID, row.ID, r.cfg.WorkerID, row.DesiredEmoji, reactionID); err != nil && !errors.Is(err, mysqlstore.ErrChannelReactionSuperseded) {
		_ = r.cfg.Adapter.DeleteReaction(ctx, row.ProviderMessageID, reactionID)
		r.retry(ctx, row, "reaction_create_persist_failed", err)
	}
}

func (r *ReactionReconciler) retry(ctx context.Context, row mysqlstore.ChannelReaction, code string, err error) {
	_ = r.cfg.Repo.MarkChannelReactionRetry(ctx, row.TenantID, row.AccountID, row.ID, r.cfg.WorkerID, r.cfg.Now().Add(r.cfg.RetryDelay), code, safeError(err))
}
