package channelbridge

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/channel/feishu"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	im "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// Embed the existing transport, not its Session/runtime coordinator. Only the
// ambiguous thread lookup and durable notice send mapping are strengthened.
type strictFeishu struct {
	*feishu.Adapter
	client *lark.Client
}

func (b *Bridge) newFeishu(a Account) (ch.Adapter, error) {
	if a.AppIDEnv == "" || a.AppSecretEnv == "" {
		return nil, ErrConfiguration
	}
	id, secret := b.options.Getenv(a.AppIDEnv), b.options.Getenv(a.AppSecretEnv)
	if id == "" || secret == "" {
		return nil, ErrConfiguration
	}
	client := lark.NewClient(id, secret)
	return &strictFeishu{Adapter: feishu.NewAdapter(feishu.Config{AccountID: a.ID, AppID: id, AppSecret: secret, Client: client}), client: client}, nil
}
func (a *strictFeishu) lookup(ctx context.Context, id, chat string) (*im.Message, error) {
	response, err := a.client.Im.V1.Message.Get(ctx, im.NewGetMessageReqBuilder().MessageId(id).Build())
	if err != nil || response == nil || !response.Success() || response.Data == nil || len(response.Data.Items) != 1 {
		return nil, errors.New("thread lookup unavailable")
	}
	item := response.Data.Items[0]
	if item == nil || stringValue(item.MessageId) != id || stringValue(item.ChatId) != chat || item.Deleted != nil && *item.Deleted {
		return nil, errors.New("thread identity mismatch")
	}
	return item, nil
}
func (a *strictFeishu) ResolveThread(ctx context.Context, msg ch.InboundMessage) (ch.ThreadResolution, error) {
	if msg.ExternalThreadID != "" {
		return ch.ThreadResolution{ThreadID: msg.ExternalThreadID, Source: ch.ThreadSourceEvent}, nil
	}
	if msg.ExternalMessageID == "" {
		return ch.ThreadResolution{}, errors.New("thread identity missing")
	}
	item, err := a.lookup(ctx, msg.ExternalMessageID, msg.ExternalConversationID)
	if err != nil {
		return ch.ThreadResolution{}, err
	}
	// Empty ID is a proven root only after a successful, matching provider lookup.
	return ch.ThreadResolution{ThreadID: stringValue(item.ThreadId), Source: ch.ThreadSourceRawMessageLookup}, nil
}
func (b *Bridge) feishuInbound(account Account, adapter ch.Adapter) ch.InboundHandler {
	fingerprint := b.configurationFingerprint(account)
	return func(ctx context.Context, msg ch.InboundMessage) ch.InboundDisposition {
		current, err := b.account(account.ID, control.ProviderFeishu)
		if err != nil || current != account || b.configurationFingerprint(current) != fingerprint || msg.AccountID != account.ID || msg.Provider != ch.ProviderFeishu {
			return ch.IgnoreInbound("account_denied")
		}
		if msg.CardAction != nil {
			return ch.IgnoreInbound("external_approval_disabled")
		}
		botID := ""
		if account.BotIDEnv != "" {
			botID = b.options.Getenv(account.BotIDEnv)
		}
		if botID == "" {
			if identity, ok := adapter.(interface{ BotOpenID(context.Context) string }); ok {
				botID = identity.BotOpenID(ctx)
			}
		}
		if !safeToken(botID, 256) {
			b.options.Logger.WarnContext(ctx, "Super Feishu ingress degraded", "code", "bot_identity_unknown")
			b.setAdmissionCode(account.ID, "bot_identity_unknown")
			return ch.RetryInbound("bot_identity_unknown")
		}
		var metadata struct {
			Event struct {
				Sender struct {
					SenderType string `json:"sender_type"`
				} `json:"sender"`
			} `json:"event"`
		}
		if json.Unmarshal(msg.RawMetadata, &metadata) != nil {
			return ch.IgnoreInbound("sender_type_unknown")
		}
		senderType := metadata.Event.Sender.SenderType
		if msg.ExternalUserID == botID || senderType == "app" {
			return ch.IgnoreInbound("bot_ignored")
		}
		if senderType != "user" {
			return ch.IgnoreInbound("sender_type_unknown")
		}
		resolved, err := adapter.ResolveThread(ctx, msg)
		if err != nil || resolved.Source != ch.ThreadSourceRawMessageLookup && resolved.Source != ch.ThreadSourceEvent || resolved.Source == ch.ThreadSourceEvent && (resolved.ThreadID == "" || resolved.ThreadID != msg.ExternalThreadID) {
			b.setAdmissionCode(account.ID, "thread_unknown")
			return ch.RetryInbound("thread_unknown")
		}
		b.setAdmissionCode(account.ID, "")
		envelope := router.Envelope{Provider: account.Provider, Account: account.ID, EventID: msg.EventID, MessageID: msg.ExternalMessageID, Chat: msg.ExternalConversationID, Thread: resolved.ThreadID, Sender: msg.ExternalUserID, Text: msg.Text, Mentioned: msg.MentionedBot || msg.ChatType == ch.ChatTypeP2P, Authenticated: true, ThreadKnown: true}
		decision, err := b.options.Handler.Handle(ctx, envelope)
		b.options.Logger.DebugContext(ctx, "Super Feishu ingress routed", "code", decision.Code, "action", decision.Action)
		if err == nil {
			return ch.AcceptInbound()
		}
		if errors.Is(err, control.ErrDenied) || errors.Is(err, control.ErrInvalid) || errors.Is(err, control.ErrConflict) || errors.Is(err, control.ErrNotFound) {
			return ch.IgnoreInbound(decision.Code)
		}
		b.setAdmissionCode(account.ID, "admission_unavailable")
		return ch.RetryInbound("admission_unavailable")
	}
}
func (a *strictFeishu) Deliver(ctx context.Context, op ch.OutboundOperation) (ch.DeliveryReceipt, error) {
	m := op.Message
	if op.Operation != ch.DeliveryCreate || m.Kind != ch.MessageKindText || m.Provider != ch.ProviderFeishu || m.AccountID != a.AccountID() || m.ExternalChatID == "" || m.IdempotencyKey == "" {
		return ch.DeliveryReceipt{}, notAccepted("invalid_notice")
	}
	content, _ := json.Marshal(map[string]string{"text": m.Text})
	token := uuid.NewSHA1(uuid.NameSpaceOID, []byte(a.AccountID()+"\x00"+m.IdempotencyKey)).String()
	if m.ReplyToMessageID != "" {
		target, err := a.lookup(ctx, m.ReplyToMessageID, m.ExternalChatID)
		if err != nil || stringValue(target.ThreadId) != m.ExternalThreadID {
			return ch.DeliveryReceipt{}, notAccepted("reply_target_denied")
		}
		body := im.NewReplyMessageReqBodyBuilder().MsgType("text").Content(string(content)).ReplyInThread(m.ExternalThreadID != "").Uuid(token).Build()
		response, err := a.client.Im.V1.Message.Reply(ctx, im.NewReplyMessageReqBuilder().MessageId(m.ReplyToMessageID).Body(body).Build())
		if err != nil || response == nil || !response.Success() || response.Data == nil {
			return ch.DeliveryReceipt{}, unknown("provider_delivery_unknown")
		}
		if stringValue(response.Data.ChatId) != m.ExternalChatID || stringValue(response.Data.ThreadId) != m.ExternalThreadID || stringValue(response.Data.MessageId) == "" {
			return ch.DeliveryReceipt{}, unknown("receipt_identity_unknown")
		}
		return ch.DeliveryReceipt{MessageID: *response.Data.MessageId, ChatID: *response.Data.ChatId}, nil
	}
	if m.ExternalThreadID != "" {
		return ch.DeliveryReceipt{}, notAccepted("reply_target_required")
	}
	body := im.NewCreateMessageReqBodyBuilder().ReceiveId(m.ExternalChatID).MsgType("text").Content(string(content)).Uuid(token).Build()
	response, err := a.client.Im.V1.Message.Create(ctx, im.NewCreateMessageReqBuilder().ReceiveIdType("chat_id").Body(body).Build())
	if err != nil || response == nil || !response.Success() || response.Data == nil {
		return ch.DeliveryReceipt{}, unknown("provider_delivery_unknown")
	}
	if stringValue(response.Data.ChatId) != m.ExternalChatID || stringValue(response.Data.ThreadId) != "" || stringValue(response.Data.MessageId) == "" {
		return ch.DeliveryReceipt{}, unknown("receipt_identity_unknown")
	}
	return ch.DeliveryReceipt{MessageID: *response.Data.MessageId, ChatID: *response.Data.ChatId}, nil
}
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
