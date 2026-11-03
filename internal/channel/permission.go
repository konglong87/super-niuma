package channel

import "context"

const (
	PermissionActionApproveOnce    = "approve_once"
	PermissionActionApproveSession = "approve_session"
	PermissionActionDeny           = "deny"
)

type PermissionRequest struct {
	ConversationID   uint64
	RunID            string
	UserID           uint64
	ExternalUserID   string
	ExternalChatID   string
	ReplyToMessageID string
	ChatType         ChatType
	ToolName         string
	Reason           string
	Request          string
}

type PermissionDecision struct {
	Allowed     bool
	Destination string
	Decision    string
	Reason      string
}

type PermissionCardSender interface {
	SendPermissionCard(context.Context, PermissionRequest, string) (messageID string, err error)
}
