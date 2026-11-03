package channel

import (
	"context"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type RunInput struct {
	TenantID           uint64
	AccountID          uint64
	UserID             uint64
	TenantSessionID    uint64
	ConversationID     uint64
	RunID              string
	Scope              Scope
	RuntimeFingerprint string
	Prompt             string
	Model              string
	WorkspaceRealpath  string
	PermissionMode     string
	ExternalUserID     string
	ExternalChatID     string
	ReplyToMessageID   string
	ChatType           ChatType
	// ModelProvider identifies the model backend; it is distinct from channel.Provider.
	ModelProvider string
	Resume        *ResumeInput
}

type ResumeInput struct {
	AssistantMessage anthropic.MessageParam
	ToolResult       anthropic.ContentBlock
}

type DeltaKind string

const (
	DeltaText      DeltaKind = "text"
	DeltaTextAmend DeltaKind = "text_amend"
	DeltaTool      DeltaKind = "tool"
	DeltaStatus    DeltaKind = "status"
)

func (k DeltaKind) Valid() bool {
	switch k {
	case DeltaText, DeltaTextAmend, DeltaTool, DeltaStatus:
		return true
	default:
		return false
	}
}

type Delta struct {
	Kind          DeltaKind
	Text          string
	PreviousText  string
	ToolID        string
	ToolName      string
	ToolStatus    string
	ToolCommand   string
	ToolOutput    string
	ToolIsError   bool
	ToolTruncated bool
}

type DeltaSink interface {
	OnDelta(context.Context, Delta) error
}

type RunResult struct {
	FinalText          string
	TurnID             string
	Tools              []ToolProgress
	Timeline           []TimelineEntry
	Attachments        []Attachment
	PendingInteraction *InteractionQuestion
}

type ConversationRunner interface {
	Run(context.Context, RunInput) (RunResult, error)
	// RunStream may return a provider capability error when streaming is disabled.
	RunStream(context.Context, RunInput, DeltaSink) (RunResult, error)
}
