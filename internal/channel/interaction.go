package channel

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

const (
	InteractionKindUserQuestion = "user_question"
	InteractionAnswerButton     = "button"
	InteractionAnswerText       = "text"
	InteractionActionAnswer     = "user_question_answer"
)

type InteractionQuestion struct {
	ID               string
	Kind             string
	Question         string
	Choices          []string
	Token            string
	ExpiresAt        time.Time
	ToolUseID        string
	ToolName         string
	ToolInput        json.RawMessage
	AssistantMessage anthropic.MessageParam
}

type InteractionCreateInput struct {
	TenantID                  uint64
	AccountID                 uint64
	ConversationID            uint64
	RunID                     string
	SessionID                 uint64
	ExternalChatID            string
	ExternalThreadID          string
	ExternalUserID            string
	UserID                    uint64
	ScopeHash                 []byte
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	Question                  InteractionQuestion
	ExpiresAt                 time.Time
}

type InteractionAnswer struct {
	InteractionID  string
	Token          string
	ChoiceID       string
	Answer         string
	Source         string
	ExternalChatID string
	ExternalUserID string
	UserID         uint64
}

type InteractionResume struct {
	Interaction      InteractionQuestion
	RunID            string
	SessionID        uint64
	ConversationID   uint64
	ScopeHash        []byte
	ExternalThreadID string
	ExternalChatID   string
	ExternalUserID   string
	UserID           uint64
	Answer           string
	Resume           ResumeInput
}

func (a InteractionAnswer) Valid() bool {
	if a.ExternalChatID == "" || a.ExternalUserID == "" || a.UserID == 0 {
		return false
	}
	if a.Source == InteractionAnswerButton {
		return a.Token != "" && a.ChoiceID != ""
	}
	return a.Source == InteractionAnswerText && a.Answer != "" && (a.Token != "" || a.InteractionID != "")
}

type PendingInteraction struct {
	Question InteractionQuestion
}

func InteractionChoiceID(index int) string {
	return fmt.Sprintf("%d", index)
}
