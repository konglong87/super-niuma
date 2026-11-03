package channel

import (
	"encoding/json"
	"time"
)

type Provider string

const (
	ProviderDingTalk Provider = "dingtalk"
	ProviderFeishu   Provider = "feishu"
)

func (p Provider) Valid() bool {
	switch p {
	case ProviderDingTalk, ProviderFeishu:
		return true
	default:
		return false
	}
}

type ChatType string

const (
	ChatTypeP2P   ChatType = "p2p"
	ChatTypeGroup ChatType = "group"
)

func (c ChatType) Valid() bool {
	switch c {
	case ChatTypeP2P, ChatTypeGroup:
		return true
	default:
		return false
	}
}

type MessageKind string

const (
	MessageKindText     MessageKind = "text"
	MessageKindMarkdown MessageKind = "markdown"
	MessageKindCard     MessageKind = "card"
	MessageKindImage    MessageKind = "image"
)

func (k MessageKind) Valid() bool {
	switch k {
	case MessageKindText, MessageKindMarkdown, MessageKindCard, MessageKindImage:
		return true
	default:
		return false
	}
}

type DeliveryOperation string

const (
	DeliveryCreate DeliveryOperation = "create"
	DeliveryUpdate DeliveryOperation = "update"
	DeliveryRecall DeliveryOperation = "recall"
)

func (o DeliveryOperation) Valid() bool {
	switch o {
	case DeliveryCreate, DeliveryUpdate, DeliveryRecall:
		return true
	default:
		return false
	}
}

type Mention struct {
	Key        string `json:"key,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Name       string `json:"name,omitempty"`
	IsBot      bool   `json:"is_bot,omitempty"`
}

type Attachment struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	MediaType string `json:"media_type,omitempty"`
	Name      string `json:"name,omitempty"`
	URL       string `json:"url,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	TenantID  uint64 `json:"tenant_id,omitempty"`
	UserID    uint64 `json:"user_id,omitempty"`
	SessionID uint64 `json:"session_id,omitempty"`
}

type CardAction struct {
	Token    string `json:"token"`
	Action   string `json:"action"`
	ChoiceID string `json:"choice_id,omitempty"`
}

type InboundMessage struct {
	Provider               Provider        `json:"provider"`
	AccountID              string          `json:"account_id"`
	EventID                string          `json:"event_id"`
	ExternalMessageID      string          `json:"external_message_id"`
	ExternalConversationID string          `json:"external_conversation_id"`
	ExternalThreadID       string          `json:"external_thread_id,omitempty"`
	ExternalUserID         string          `json:"external_user_id"`
	ChatType               ChatType        `json:"chat_type"`
	Text                   string          `json:"text,omitempty"`
	Mentions               []Mention       `json:"mentions,omitempty"`
	Attachments            []Attachment    `json:"attachments,omitempty"`
	ReplyToMessageID       string          `json:"reply_to_message_id,omitempty"`
	MentionedBot           bool            `json:"mentioned_bot,omitempty"`
	CreatedAt              time.Time       `json:"created_at"`
	RawMetadata            json.RawMessage `json:"raw_metadata,omitempty"`
	CardAction             *CardAction     `json:"card_action,omitempty"`
}

type OutboundMessage struct {
	Provider         Provider        `json:"provider"`
	AccountID        string          `json:"account_id"`
	ConversationID   string          `json:"conversation_id"`
	ExternalChatID   string          `json:"external_chat_id"`
	ExternalThreadID string          `json:"external_thread_id,omitempty"`
	ReplyToMessageID string          `json:"reply_to_message_id,omitempty"`
	Kind             MessageKind     `json:"kind"`
	Text             string          `json:"text,omitempty"`
	Card             json.RawMessage `json:"card,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key"`
	RenderVersion    uint64          `json:"render_version,omitempty"`
	CorrelationID    string          `json:"correlation_id,omitempty"`
}

// Capabilities describes provider features and payload limits.
// A false feature flag means the capability is unsupported; a max value <= 0
// leaves the provider or runtime default limit unspecified.
type Capabilities struct {
	Markdown        bool `json:"markdown"`
	Reply           bool `json:"reply"`
	FinalCard       bool `json:"final_card"`
	StreamingCard   bool `json:"streaming_card"`
	CardActions     bool `json:"card_actions"`
	MessageUpdate   bool `json:"message_update"`
	MessageRecall   bool `json:"message_recall"`
	Reactions       bool `json:"reactions"`
	MaxMessageBytes int  `json:"max_message_bytes"`
	MaxCardBytes    int  `json:"max_card_bytes"`
	MaxCardElements int  `json:"max_card_elements"`
}

// SupportsStreamingCard requires final-card rendering, streaming-card support,
// and message updates; it intentionally does not gate on other capabilities.
func (c Capabilities) SupportsStreamingCard() bool {
	return c.FinalCard && c.StreamingCard && c.MessageUpdate
}
