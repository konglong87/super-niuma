package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

var (
	ErrInteractionRejected = errors.New("channel interaction rejected")
	ErrInteractionChoice   = errors.New("channel interaction choice is invalid")
)

type InteractionRepository interface {
	CreateChannelInteraction(context.Context, mysqlstore.ChannelInteractionInput) (mysqlstore.ChannelInteraction, error)
	GetChannelInteraction(context.Context, uint64, uint64, string) (mysqlstore.ChannelInteraction, error)
	FindChannelInteractionByNonce(context.Context, uint64, uint64, []byte) (mysqlstore.ChannelInteraction, error)
	FindPendingChannelInteraction(context.Context, uint64, uint64, uint64, uint64, string, time.Time) (mysqlstore.ChannelInteraction, error)
	AnswerChannelInteraction(context.Context, uint64, uint64, string, []byte, uint64, string, int, []byte, time.Time) error
}

type InteractionRecoveryRepository interface {
	ListAnsweredChannelInteractions(context.Context, uint64, uint64, int) ([]mysqlstore.ChannelInteraction, error)
}

type InteractionExpiryRepository interface {
	ExpirePendingChannelInteractions(context.Context, uint64, uint64, time.Time) ([]mysqlstore.ChannelInteraction, error)
}

type interactionQuestionPayload struct {
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Choices  []string `json:"choices,omitempty"`
}

type interactionResumePayload struct {
	ToolUseID        string                 `json:"tool_use_id"`
	ToolName         string                 `json:"tool_name"`
	ToolInput        json.RawMessage        `json:"tool_input"`
	AssistantMessage anthropic.MessageParam `json:"assistant_message"`
}

type interactionAnswerPayload struct {
	Answer string `json:"answer"`
	Source string `json:"source"`
}

type QuestionBrokerConfig struct {
	TenantID                  uint64
	AccountID                 uint64
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	Repo                      InteractionRepository
	PayloadCodec              PayloadCodec
	Timeout                   time.Duration
	Now                       func() time.Time
}

type QuestionBroker struct {
	cfg QuestionBrokerConfig
}

func NewQuestionBroker(cfg QuestionBrokerConfig) *QuestionBroker {
	if cfg.PayloadCodec == nil {
		cfg.PayloadCodec = JSONCodec{}
	}
	if cfg.RuntimeFingerprint == "" {
		cfg.RuntimeFingerprint = DefaultFingerprint
	}
	if cfg.RuntimeFingerprintVersion == 0 {
		cfg.RuntimeFingerprintVersion = DefaultRuntimeVersion
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &QuestionBroker{cfg: cfg}
}

func (b *QuestionBroker) Create(ctx context.Context, input channelcontract.InteractionCreateInput) (channelcontract.InteractionQuestion, error) {
	if b == nil || b.cfg.Repo == nil || input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.RunID == "" || input.SessionID == 0 || input.ExternalChatID == "" || input.ExternalUserID == "" || input.UserID == 0 || len(input.ScopeHash) == 0 || input.Question.Question == "" {
		return channelcontract.InteractionQuestion{}, mysqlstore.ErrInvalidInput
	}
	question := input.Question
	if question.ID == "" {
		question.ID = newID()
	}
	if question.Kind == "" {
		question.Kind = channelcontract.InteractionKindUserQuestion
	}
	if input.ExpiresAt.IsZero() {
		input.ExpiresAt = b.cfg.Now().Add(b.cfg.Timeout)
	}
	token, err := newInteractionToken()
	if err != nil {
		return channelcontract.InteractionQuestion{}, err
	}
	question.Token = token
	question.ExpiresAt = input.ExpiresAt
	questionPayload, err := b.cfg.PayloadCodec.Encode(interactionQuestionPayload{Kind: question.Kind, Question: question.Question, Choices: question.Choices})
	if err != nil {
		return channelcontract.InteractionQuestion{}, err
	}
	resumePayload, err := b.cfg.PayloadCodec.Encode(interactionResumePayload{ToolUseID: question.ToolUseID, ToolName: question.ToolName, ToolInput: question.ToolInput, AssistantMessage: question.AssistantMessage})
	if err != nil {
		return channelcontract.InteractionQuestion{}, err
	}
	hash := sha256.Sum256([]byte(token))
	_, err = b.cfg.Repo.CreateChannelInteraction(ctx, mysqlstore.ChannelInteractionInput{
		ID:                         question.ID,
		TenantID:                   input.TenantID,
		AccountID:                  input.AccountID,
		ConversationID:             input.ConversationID,
		RunID:                      input.RunID,
		SessionID:                  input.SessionID,
		Kind:                       question.Kind,
		Status:                     mysqlstore.ChannelInteractionStatusPending,
		ExternalChatID:             input.ExternalChatID,
		ExternalThreadID:           input.ExternalThreadID,
		ExternalUserID:             input.ExternalUserID,
		ScopeHash:                  append([]byte(nil), input.ScopeHash...),
		AllowedUserID:              input.UserID,
		QuestionCiphertext:         questionPayload,
		ResumeCheckpointCiphertext: resumePayload,
		NonceHash:                  hash[:],
		RuntimeFingerprint:         firstNonEmpty(input.RuntimeFingerprint, b.cfg.RuntimeFingerprint),
		RuntimeFingerprintVersion:  firstInt(input.RuntimeFingerprintVersion, b.cfg.RuntimeFingerprintVersion),
		ExpiresAt:                  input.ExpiresAt,
	})
	if err != nil {
		return channelcontract.InteractionQuestion{}, err
	}
	return question, nil
}

func (b *QuestionBroker) FindPending(ctx context.Context, conversationID, userID uint64, externalChatID string, now time.Time) (mysqlstore.ChannelInteraction, error) {
	if b == nil || b.cfg.Repo == nil {
		return mysqlstore.ChannelInteraction{}, ErrInteractionRejected
	}
	return b.cfg.Repo.FindPendingChannelInteraction(ctx, b.cfg.TenantID, b.cfg.AccountID, conversationID, userID, externalChatID, now)
}

func (b *QuestionBroker) ResumeStored(ctx context.Context, interaction mysqlstore.ChannelInteraction) (channelcontract.InteractionResume, error) {
	if b == nil || b.cfg.PayloadCodec == nil || interaction.ID == "" || interaction.Status != mysqlstore.ChannelInteractionStatusAnswered {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	question, err := b.decodeQuestion(interaction.QuestionCiphertext)
	if err != nil {
		return channelcontract.InteractionResume{}, err
	}
	var resume interactionResumePayload
	if err := b.cfg.PayloadCodec.Decode(interaction.ResumeCheckpointCiphertext, &resume); err != nil {
		return channelcontract.InteractionResume{}, err
	}
	var answer interactionAnswerPayload
	if err := b.cfg.PayloadCodec.Decode(interaction.AnswerCiphertext, &answer); err != nil {
		return channelcontract.InteractionResume{}, err
	}
	question.ID = interaction.ID
	return channelcontract.InteractionResume{Interaction: question, RunID: interaction.RunID, SessionID: interaction.SessionID, ConversationID: interaction.ConversationID, ScopeHash: append([]byte(nil), interaction.ScopeHash...), ExternalThreadID: interaction.ExternalThreadID, ExternalChatID: interaction.ExternalChatID, ExternalUserID: interaction.ExternalUserID, UserID: interaction.AllowedUserID, Answer: answer.Answer, Resume: channelcontract.ResumeInput{AssistantMessage: resume.AssistantMessage, ToolResult: anthropic.ContentBlock{Type: "tool_result", ToolUseID: resume.ToolUseID, Content: answer.Answer}}}, nil
}

func (b *QuestionBroker) Answer(ctx context.Context, input channelcontract.InteractionAnswer) (channelcontract.InteractionResume, error) {
	if b == nil || b.cfg.Repo == nil || !input.Valid() {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	var interaction mysqlstore.ChannelInteraction
	var err error
	var nonceHash []byte
	if input.Token != "" {
		hash := sha256.Sum256([]byte(input.Token))
		nonceHash = hash[:]
		interaction, err = b.cfg.Repo.FindChannelInteractionByNonce(ctx, b.cfg.TenantID, b.cfg.AccountID, nonceHash)
	} else if input.InteractionID != "" {
		interaction, err = b.cfg.Repo.GetChannelInteraction(ctx, b.cfg.TenantID, b.cfg.AccountID, input.InteractionID)
	} else {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	if err != nil {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	if interaction.Status != mysqlstore.ChannelInteractionStatusPending || interaction.ExternalChatID != input.ExternalChatID || interaction.AllowedUserID != input.UserID || interaction.RuntimeFingerprint != b.cfg.RuntimeFingerprint || interaction.RuntimeFingerprintVersion != b.cfg.RuntimeFingerprintVersion || b.cfg.Now().After(interaction.ExpiresAt) {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	question, err := b.decodeQuestion(interaction.QuestionCiphertext)
	if err != nil {
		return channelcontract.InteractionResume{}, err
	}
	answer := strings.TrimSpace(input.Answer)
	if input.Source == channelcontract.InteractionAnswerButton {
		index, parseErr := strconv.Atoi(input.ChoiceID)
		if parseErr != nil || index < 0 || index >= len(question.Choices) {
			return channelcontract.InteractionResume{}, ErrInteractionChoice
		}
		answer = question.Choices[index]
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	answerPayload, err := b.cfg.PayloadCodec.Encode(interactionAnswerPayload{Answer: answer, Source: input.Source})
	if err != nil {
		return channelcontract.InteractionResume{}, err
	}
	if len(nonceHash) == 0 {
		nonceHash = interaction.NonceHash
	}
	if !bytes.Equal(nonceHash, interaction.NonceHash) {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	if err := b.cfg.Repo.AnswerChannelInteraction(ctx, b.cfg.TenantID, b.cfg.AccountID, interaction.ID, nonceHash, input.UserID, b.cfg.RuntimeFingerprint, b.cfg.RuntimeFingerprintVersion, answerPayload, b.cfg.Now()); err != nil {
		return channelcontract.InteractionResume{}, ErrInteractionRejected
	}
	var resume interactionResumePayload
	if err := b.cfg.PayloadCodec.Decode(interaction.ResumeCheckpointCiphertext, &resume); err != nil {
		return channelcontract.InteractionResume{}, err
	}
	question.Token = ""
	question.ID = interaction.ID
	return channelcontract.InteractionResume{Interaction: question, RunID: interaction.RunID, SessionID: interaction.SessionID, ConversationID: interaction.ConversationID, ScopeHash: append([]byte(nil), interaction.ScopeHash...), ExternalThreadID: interaction.ExternalThreadID, ExternalChatID: interaction.ExternalChatID, ExternalUserID: interaction.ExternalUserID, UserID: interaction.AllowedUserID, Answer: answer, Resume: channelcontract.ResumeInput{AssistantMessage: resume.AssistantMessage, ToolResult: anthropic.ContentBlock{Type: "tool_result", ToolUseID: resume.ToolUseID, Content: answer}}}, nil
}

func (b *QuestionBroker) decodeQuestion(payload []byte) (channelcontract.InteractionQuestion, error) {
	var decoded interactionQuestionPayload
	if err := b.cfg.PayloadCodec.Decode(payload, &decoded); err != nil {
		return channelcontract.InteractionQuestion{}, err
	}
	return channelcontract.InteractionQuestion{Kind: decoded.Kind, Question: decoded.Question, Choices: append([]string(nil), decoded.Choices...)}, nil
}

func newInteractionToken() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func firstInt(value, fallback int) int {
	if value != 0 {
		return value
	}
	return fallback
}
