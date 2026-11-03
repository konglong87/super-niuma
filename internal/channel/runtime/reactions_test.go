package runtime

import (
	"context"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type reactionRepoFake struct{ row mysqlstore.ChannelReaction }

func (f *reactionRepoFake) UpsertChannelReactionDesired(_ context.Context, input mysqlstore.ChannelReactionInput) error {
	if f.row.ID == 0 {
		f.row = mysqlstore.ChannelReaction{ID: 1, TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, ProviderMessageID: input.ProviderMessageID}
	}
	f.row.DesiredEmoji = input.DesiredEmoji
	f.row.Status = mysqlstore.ChannelReactionStatusPending
	return nil
}
func (f *reactionRepoFake) ClaimDueChannelReactions(context.Context, uint64, uint64, string, int, time.Time) ([]mysqlstore.ChannelReaction, error) {
	if f.row.ID == 0 || f.row.Status == mysqlstore.ChannelReactionStatusApplied {
		return nil, nil
	}
	return []mysqlstore.ChannelReaction{f.row}, nil
}
func (f *reactionRepoFake) MarkChannelReactionApplied(_ context.Context, _, _, _ uint64, _, emoji, reactionID string) error {
	if f.row.DesiredEmoji != emoji {
		return mysqlstore.ErrChannelReactionSuperseded
	}
	f.row.CurrentEmoji, f.row.ReactionID, f.row.Status = emoji, reactionID, mysqlstore.ChannelReactionStatusApplied
	return nil
}
func (f *reactionRepoFake) MarkChannelReactionDeleted(context.Context, uint64, uint64, uint64, string) error {
	f.row.CurrentEmoji, f.row.ReactionID, f.row.Status = "", "", mysqlstore.ChannelReactionStatusPending
	return nil
}
func (f *reactionRepoFake) MarkChannelReactionRetry(context.Context, uint64, uint64, uint64, string, time.Time, string, string) error {
	f.row.Status = mysqlstore.ChannelReactionStatusRetry
	return nil
}

type reactionAdapterFake struct {
	created  []channelcontract.ReactionEmoji
	deleted  []string
	onCreate func(channelcontract.ReactionEmoji)
}

func (f *reactionAdapterFake) CreateReaction(_ context.Context, _ string, emoji channelcontract.ReactionEmoji) (string, error) {
	f.created = append(f.created, emoji)
	if f.onCreate != nil {
		f.onCreate(emoji)
	}
	return "reaction-" + string(emoji), nil
}

func TestReactionReconcilerDoesNotOverwriteNewerDesiredState(t *testing.T) {
	repo := &reactionRepoFake{}
	adapter := &reactionAdapterFake{}
	reconciler := NewReactionReconciler(ReactionConfig{TenantID: 1, AccountID: 2, WorkerID: "worker", Repo: repo, Adapter: adapter})
	if err := reconciler.SetDesired(context.Background(), 3, "om-1", channelcontract.ReactionTyping); err != nil {
		t.Fatal(err)
	}
	adapter.onCreate = func(channelcontract.ReactionEmoji) {
		_ = reconciler.SetDesired(context.Background(), 3, "om-1", channelcontract.ReactionDone)
	}
	if err := reconciler.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.row.DesiredEmoji != string(channelcontract.ReactionDone) || repo.row.Status != mysqlstore.ChannelReactionStatusPending {
		t.Fatalf("new desired state was overwritten: %+v", repo.row)
	}
}
func (f *reactionAdapterFake) DeleteReaction(_ context.Context, _ string, reactionID string) error {
	f.deleted = append(f.deleted, reactionID)
	return nil
}

func TestReactionReconcilerConvergesTypingToDone(t *testing.T) {
	repo := &reactionRepoFake{}
	adapter := &reactionAdapterFake{}
	reconciler := NewReactionReconciler(ReactionConfig{TenantID: 1, AccountID: 2, WorkerID: "worker", Repo: repo, Adapter: adapter})
	if err := reconciler.SetDesired(context.Background(), 3, "om-1", channelcontract.ReactionTyping); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.row.CurrentEmoji != string(channelcontract.ReactionTyping) || repo.row.ReactionID == "" {
		t.Fatalf("typing state = %+v", repo.row)
	}
	if err := reconciler.SetDesired(context.Background(), 3, "om-1", channelcontract.ReactionDone); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(adapter.deleted) != 1 || repo.row.CurrentEmoji != "" {
		t.Fatalf("delete transition row=%+v deleted=%+v", repo.row, adapter.deleted)
	}
	if err := reconciler.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.row.CurrentEmoji != string(channelcontract.ReactionDone) || len(adapter.created) != 2 {
		t.Fatalf("done state=%+v created=%+v", repo.row, adapter.created)
	}
}
