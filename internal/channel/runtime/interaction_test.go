package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type interactionRepoFake struct {
	row      mysqlstore.ChannelInteraction
	answered bool
}

func (f *interactionRepoFake) CreateChannelInteraction(_ context.Context, input mysqlstore.ChannelInteractionInput) (mysqlstore.ChannelInteraction, error) {
	f.row = mysqlstore.ChannelInteraction{ID: input.ID, TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: input.RunID, SessionID: input.SessionID, Kind: input.Kind, Status: input.Status, ExternalChatID: input.ExternalChatID, ScopeHash: input.ScopeHash, AllowedUserID: input.AllowedUserID, QuestionCiphertext: input.QuestionCiphertext, ResumeCheckpointCiphertext: input.ResumeCheckpointCiphertext, NonceHash: input.NonceHash, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, ExpiresAt: input.ExpiresAt}
	return f.row, nil
}

func (f *interactionRepoFake) GetChannelInteraction(context.Context, uint64, uint64, string) (mysqlstore.ChannelInteraction, error) {
	if f.row.ID == "" {
		return mysqlstore.ChannelInteraction{}, mysqlstore.ErrNotFound
	}
	return f.row, nil
}

func (f *interactionRepoFake) FindChannelInteractionByNonce(context.Context, uint64, uint64, []byte) (mysqlstore.ChannelInteraction, error) {
	if f.row.ID == "" || f.row.Status != mysqlstore.ChannelInteractionStatusPending {
		return mysqlstore.ChannelInteraction{}, mysqlstore.ErrNotFound
	}
	return f.row, nil
}

func (f *interactionRepoFake) FindPendingChannelInteraction(context.Context, uint64, uint64, uint64, uint64, string, time.Time) (mysqlstore.ChannelInteraction, error) {
	if f.row.ID == "" || f.row.Status != mysqlstore.ChannelInteractionStatusPending {
		return mysqlstore.ChannelInteraction{}, mysqlstore.ErrNotFound
	}
	return f.row, nil
}

func (f *interactionRepoFake) AnswerChannelInteraction(_ context.Context, tenantID, accountID uint64, interactionID string, nonceHash []byte, allowedUserID uint64, fingerprint string, version int, answer []byte, now time.Time) error {
	if f.answered || tenantID != f.row.TenantID || accountID != f.row.AccountID || interactionID != f.row.ID || allowedUserID != f.row.AllowedUserID || fingerprint != f.row.RuntimeFingerprint || version != f.row.RuntimeFingerprintVersion || string(nonceHash) != string(f.row.NonceHash) || now.After(f.row.ExpiresAt) {
		return mysqlstore.ErrNotFound
	}
	f.answered = true
	f.row.Status = mysqlstore.ChannelInteractionStatusAnswered
	f.row.AnswerCiphertext = answer
	return nil
}

func TestQuestionBrokerCreatesOpaquePendingInteractionAndAnswersChoice(t *testing.T) {
	repo := &interactionRepoFake{}
	now := time.Now().UTC()
	broker := NewQuestionBroker(QuestionBrokerConfig{TenantID: 9, AccountID: 3, RuntimeFingerprint: "fp", RuntimeFingerprintVersion: 1, Repo: repo, Now: func() time.Time { return now }})
	question, err := broker.Create(context.Background(), channelcontract.InteractionCreateInput{
		TenantID: 9, AccountID: 3, ConversationID: 7, RunID: "run-1", SessionID: 11,
		ExternalChatID: "oc-1", ExternalUserID: "ou-1", UserID: 42, ScopeHash: []byte("scope"),
		Question: channelcontract.InteractionQuestion{Question: "Which path?", Choices: []string{"A", "B"}},
	})
	if err != nil || question.ID == "" || question.Token == "" || question.Token == string(repo.row.NonceHash) {
		t.Fatalf("question=%+v row=%+v err=%v", question, repo.row, err)
	}
	answer, err := broker.Answer(context.Background(), channelcontract.InteractionAnswer{InteractionID: question.ID, Token: question.Token, ChoiceID: "1", Answer: "ignored", Source: channelcontract.InteractionAnswerButton, ExternalChatID: "oc-1", ExternalUserID: "ou-1", UserID: 42})
	if err != nil || answer.Interaction.Question != "Which path?" || answer.Interaction.Token != "" || answer.Answer != "B" {
		t.Fatalf("answer=%+v err=%v", answer, err)
	}
	if _, err := broker.Answer(context.Background(), channelcontract.InteractionAnswer{InteractionID: question.ID, Token: question.Token, ChoiceID: "1", Answer: "ignored", Source: channelcontract.InteractionAnswerButton, ExternalChatID: "oc-1", ExternalUserID: "ou-1", UserID: 42}); !errors.Is(err, ErrInteractionRejected) {
		t.Fatalf("duplicate answer err=%v", err)
	}
}
