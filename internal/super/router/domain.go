// Package router admits authenticated, exactly bound external commands without
// model classification or legacy Session execution.
package router

import (
	"context"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
)

const (
	ActionRun           = "run"
	ActionStatus        = "status"
	ActionCancel        = "cancel"
	ActionClarification = "clarification"
	ActionRejected      = "rejected"
	ActionIgnored       = "ignored"
	CodeAccepted        = "accepted"
	CodeCancelled       = "cancelled"
	CodeClarification   = "clarification_required"
	CodeInvalid         = "invalid_request"
	CodeUnauthenticated = "unauthenticated"
	CodeUnbound         = "binding_denied"
	CodeAmbiguous       = "binding_ambiguous"
	CodeBot             = "bot_ignored"
	CodeThreadUnknown   = "thread_unknown"
	CodeMention         = "mention_required"
	CodeCommand         = "command_unsupported"
	CodeDenied          = "authority_denied"
	CodeUnavailable     = "unavailable"
	CodeConflict        = "conflict"
	MaxTextBytes        = 8192
	MaxBindings         = 1024
)

type Binding struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	Account        string `json:"account"`
	Chat           string `json:"chat"`
	Thread         string `json:"thread"`
	Sender         string `json:"sender"`
	Scope          string `json:"scope"`
	Owner          string `json:"owner"`
	ProfileID      string `json:"profile_id"`
	Enabled        bool   `json:"enabled"`
	RequireMention bool   `json:"require_mention"`
}

// Envelope is an in-process trust boundary. JSON cannot manufacture its flags.
type Envelope struct {
	Provider, Account, EventID, MessageID, Chat, Thread, Sender, Text, Nonce string
	Bot, Self, Mentioned, ThreadKnown, Authenticated                         bool
}

func (*Envelope) UnmarshalJSON([]byte) error {
	return errors.New("trusted envelope cannot be decoded from JSON")
}

type Decision struct {
	Code    string `json:"code"`
	RunID   string `json:"run_id,omitempty"`
	Action  string `json:"action"`
	UsedLLM bool   `json:"used_llm"`
}
type GrantResolver func(context.Context, control.Principal, string) (control.Grant, error)
type CommandRequest struct {
	Principal       control.Principal
	Ingress         control.Ingress
	RunID, Action   string
	ExpectedVersion uint64
}
type CommandReceipt struct {
	Request  CommandRequest
	Version  uint64
	Status   control.Status
	Decision *Decision
}

// BeginCommand atomically binds the shared ingress identities and persists the
// original target revision. FinishCommand records a safe, replayable response.
// A pending cancel resumes with the original revision, never a fresh mutation.
type CommandReceipts interface {
	BeginCommand(context.Context, CommandRequest) (CommandReceipt, error)
	FinishCommand(context.Context, CommandReceipt, Decision) error
}

func ValidDecision(d Decision) bool {
	if d.UsedLLM || !control.ValidID(d.RunID) {
		return false
	}
	switch d.Action {
	case ActionStatus:
		return validStatusCode(d.Code)
	case ActionCancel:
		return d.Code == CodeCancelled || d.Code == CodeConflict || d.Code == CodeDenied
	default:
		return false
	}
}
func validStatusCode(code string) bool {
	for _, status := range []control.Status{control.Queued, control.Running, control.Verifying, control.AwaitingApproval, control.DeliveryPending, control.Delivered, control.Cancelling, control.Cancelled, control.Failed, control.NeedsAttention} {
		if code == "status_"+string(status) {
			return true
		}
	}
	return false
}
