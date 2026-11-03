package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkchannel "github.com/larksuite/oapi-sdk-go/v3/channel"
	larknormalize "github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkdispatcher "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const (
	maxMessageBytes = 30000
	maxCardBytes    = 30000
	maxCardElements = 50
	MaxMessageBytes = maxMessageBytes
	MaxCardBytes    = maxCardBytes
	MaxCardElements = maxCardElements
)

var (
	errRetryableInbound      = errors.New("feishu inbound retryable")
	errUnsupportedRecall     = errors.New("feishu message recall unsupported")
	errAdapterNotInitialized = errors.New("feishu adapter is not initialized")
)

// Config contains one Feishu app's credentials and optional SDK injection points.
// Injection points keep lifecycle and payload tests independent from Feishu network access.
type Config struct {
	AccountID         string
	AppID             string
	AppSecret         string
	ClientOptions     []lark.ClientOptionFunc
	WSOptions         []larkws.ClientOption
	ChannelOptions    []larktypes.ChannelOption
	ResolveAttachment func(context.Context, channelcontract.Attachment) ([]byte, error)
	Client            *lark.Client
	WSClient          *larkws.Client
	Channel           larktypes.Channel
}

// Adapter implements channel.Adapter for Feishu's official Go SDK.
type Adapter struct {
	accountID         string
	appID             string
	client            *lark.Client
	wsClient          *larkws.Client
	channel           larktypes.Channel
	botOpenID         string
	resolveAttachment func(context.Context, channelcontract.Attachment) ([]byte, error)

	mu         sync.Mutex
	started    bool
	stopped    bool
	handler    channelcontract.InboundHandler
	health     channelcontract.Health
	hooksBound bool

	fetchRawMessage   func(context.Context, string) (string, error)
	createReactionAPI func(context.Context, string, string) (string, error)
	deleteReactionAPI func(context.Context, string, string) error
}

// FeishuAdapter is kept as an explicit provider-facing name for callers that
// prefer naming concrete adapters in registries.
type FeishuAdapter = Adapter

var _ channelcontract.Adapter = (*Adapter)(nil)
var _ channelcontract.StreamingAdapter = (*Adapter)(nil)

func NewAdapter(cfg Config) *Adapter {
	client := cfg.Client
	if client == nil {
		client = lark.NewClient(cfg.AppID, cfg.AppSecret, cfg.ClientOptions...)
	}
	wsClient := cfg.WSClient
	if wsClient == nil && cfg.Channel == nil {
		wsOptions := append([]larkws.ClientOption(nil), cfg.WSOptions...)
		// The SDK leaves EventHandler nil unless callers explicitly inject one.
		// A nil dispatcher panics on the first real WebSocket event, after the
		// connection has already reported ready, so always provide one here.
		wsOptions = append(wsOptions, larkws.WithEventHandler(larkdispatcher.NewEventDispatcher("", "")))
		wsClient = larkws.NewClient(cfg.AppID, cfg.AppSecret, wsOptions...)
	}
	ch := cfg.Channel
	if ch == nil {
		ch = larkchannel.NewChannel(client, wsClient, cfg.ChannelOptions...)
	}
	a := &Adapter{
		accountID:         cfg.AccountID,
		appID:             cfg.AppID,
		client:            client,
		channel:           ch,
		wsClient:          wsClient,
		resolveAttachment: cfg.ResolveAttachment,
		health:            channelcontract.Health{Status: channelcontract.HealthStarting},
	}
	a.fetchRawMessage = a.fetchThreadFromAPI
	a.createReactionAPI = a.createReaction
	a.deleteReactionAPI = a.deleteReaction
	return a
}

// New is a concise constructor alias for registry wiring.
func New(cfg Config) *Adapter { return NewAdapter(cfg) }

// NewAdapterWithChannel is intended for deterministic tests and local SDK adapters.
func NewAdapterWithChannel(cfg Config, ch larktypes.Channel) *Adapter {
	a := &Adapter{
		accountID:         cfg.AccountID,
		appID:             cfg.AppID,
		client:            cfg.Client,
		channel:           ch,
		wsClient:          cfg.WSClient,
		resolveAttachment: cfg.ResolveAttachment,
		health:            channelcontract.Health{Status: channelcontract.HealthStarting},
	}
	a.fetchRawMessage = a.fetchThreadFromAPI
	a.createReactionAPI = a.createReaction
	a.deleteReactionAPI = a.deleteReaction
	return a
}

func (a *Adapter) Provider() channelcontract.Provider { return channelcontract.ProviderFeishu }
func (a *Adapter) AccountID() string                  { return a.accountID }

// BotOpenID returns the provider-assigned identity used by the loop guard.
// The SDK resolves it lazily during Start; callers may use the empty value
// before the adapter has connected.
func (a *Adapter) BotOpenID(_ context.Context) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.botOpenID
}

func (a *Adapter) Capabilities() channelcontract.Capabilities {
	return channelcontract.Capabilities{
		Markdown:        true,
		Reply:           true,
		FinalCard:       true,
		StreamingCard:   a.client != nil && a.channel != nil,
		CardActions:     a.channel != nil,
		MessageUpdate:   a.client != nil && a.channel != nil,
		MessageRecall:   a.client != nil && a.channel != nil,
		Reactions:       a.client != nil,
		MaxMessageBytes: maxMessageBytes,
		MaxCardBytes:    maxCardBytes,
		MaxCardElements: maxCardElements,
	}
}

func (a *Adapter) CreateReaction(ctx context.Context, messageID string, emoji channelcontract.ReactionEmoji) (string, error) {
	if messageID == "" || !emoji.Valid() {
		return "", errors.New("feishu reaction create requires message id and valid emoji")
	}
	if a == nil || a.createReactionAPI == nil {
		return "", errAdapterNotInitialized
	}
	return a.createReactionAPI(ctx, messageID, string(emoji))
}

func (a *Adapter) DeleteReaction(ctx context.Context, messageID, reactionID string) error {
	if messageID == "" || reactionID == "" {
		return errors.New("feishu reaction delete requires message id and reaction id")
	}
	if a == nil || a.deleteReactionAPI == nil {
		return errAdapterNotInitialized
	}
	return a.deleteReactionAPI(ctx, messageID, reactionID)
}

func (a *Adapter) createReaction(ctx context.Context, messageID, emoji string) (string, error) {
	if a.client == nil {
		return "", errAdapterNotInitialized
	}
	body := larkim.NewCreateMessageReactionReqBodyBuilder().ReactionType(larkim.NewEmojiBuilder().EmojiType(emoji).Build()).Build()
	req := larkim.NewCreateMessageReactionReqBuilder().MessageId(messageID).Body(body).Build()
	resp, err := a.client.Im.V1.MessageReaction.Create(ctx, req)
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || resp.Data.ReactionId == nil || strings.TrimSpace(*resp.Data.ReactionId) == "" {
		return "", errors.New("feishu reaction create failed")
	}
	return *resp.Data.ReactionId, nil
}

func (a *Adapter) deleteReaction(ctx context.Context, messageID, reactionID string) error {
	if a.client == nil {
		return errAdapterNotInitialized
	}
	req := larkim.NewDeleteMessageReactionReqBuilder().MessageId(messageID).ReactionId(reactionID).Build()
	resp, err := a.client.Im.V1.MessageReaction.Delete(ctx, req)
	if err != nil || resp == nil || !resp.Success() {
		return errors.New("feishu reaction delete failed")
	}
	return nil
}

func (a *Adapter) Start(ctx context.Context, handler channelcontract.InboundHandler) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return nil
	}
	if handler == nil {
		a.mu.Unlock()
		return errors.New("feishu adapter: inbound handler is required")
	}
	if a.channel == nil {
		a.mu.Unlock()
		return errAdapterNotInitialized
	}
	a.started = true
	a.stopped = false
	a.handler = handler
	a.bindHooksLocked()
	a.mu.Unlock()
	// The official WebSocket client owns a blocking receive loop. Running it
	// synchronously would prevent the independent channel worker from polling
	// Inbox/Outbox after the connection becomes ready.
	go func() {
		if err := a.channel.Start(ctx); err != nil {
			a.mu.Lock()
			a.started = false
			a.health.Status = channelcontract.HealthFailed
			a.health.LastError = "start_failed"
			a.mu.Unlock()
		}
	}()
	runtime.Gosched()
	return nil
}

func (a *Adapter) bindHooksLocked() {
	if a.hooksBound {
		return
	}
	a.hooksBound = true
	messageHandler := func(ctx context.Context, msg *larktypes.NormalizedMessage) error {
		inbound, err := normalizeMessageWithIdentity(a.accountID, a.appID, a.resolveBotOpenID(ctx), msg)
		if err != nil {
			return errors.New("feishu inbound normalization failed")
		}
		disposition := a.handler(ctx, inbound)
		return dispositionError(disposition)
	}
	if a.wsClient != nil && a.wsClient.EventHandler() != nil {
		a.wsClient.EventHandler().OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return messageHandler(ctx, larknormalize.ParseMessage(event))
		})
	} else {
		a.channel.OnMessage(messageHandler)
	}
	// Reaction events are subscribed by the Feishu channel SDK when the app
	// has reaction scopes. They are outbound lifecycle noise for this adapter,
	// but must still be acknowledged so the SDK dispatcher does not emit a
	// misleading "not found handler" error for every bot reaction.
	a.channel.OnReaction(func(context.Context, *larktypes.ReactionEvent) error { return nil })
	a.channel.OnCardAction(func(ctx context.Context, event *larktypes.CardActionEvent) error {
		inbound := normalizeCardAction(a.accountID, event)
		if inbound.ExternalUserID == "" {
			return errors.New("feishu card action missing operator")
		}
		disposition := a.handler(ctx, inbound)
		return dispositionError(disposition)
	})
	a.channel.OnReady(func() { a.setHealth(channelcontract.HealthReady, "") })
	a.channel.OnError(func(error) { a.setHealth(channelcontract.HealthDegraded, "sdk_error") })
	a.channel.OnReconnecting(func() { a.setHealth(channelcontract.HealthDegraded, "reconnecting") })
	a.channel.OnReconnected(func() { a.setHealth(channelcontract.HealthReady, "") })
	a.channel.OnDisconnected(func() { a.setHealth(channelcontract.HealthDegraded, "disconnected") })
}

func (a *Adapter) setHealth(status channelcontract.HealthStatus, lastError string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return
	}
	a.health.Status = status
	a.health.LastError = lastError
	if status == channelcontract.HealthReady && a.health.ConnectedAt.IsZero() {
		a.health.ConnectedAt = time.Now()
	}
}

func (a *Adapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil
	}
	ch := a.channel
	a.mu.Unlock()
	if ch == nil {
		return nil
	}
	if err := ch.Stop(ctx); err != nil {
		return fmt.Errorf("feishu adapter stop: %w", err)
	}
	a.mu.Lock()
	a.stopped = true
	a.started = false
	a.health.Status = channelcontract.HealthDegraded
	a.health.LastError = "stopped"
	a.mu.Unlock()
	return nil
}

func (a *Adapter) Deliver(ctx context.Context, op channelcontract.OutboundOperation) (channelcontract.DeliveryReceipt, error) {
	if op.Message.Provider != "" && op.Message.Provider != channelcontract.ProviderFeishu {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery: provider mismatch")
	}
	if op.Message.AccountID != "" && op.Message.AccountID != a.accountID {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery: account mismatch")
	}
	switch op.Operation {
	case channelcontract.DeliveryCreate:
		return a.create(ctx, op.Message)
	case channelcontract.DeliveryUpdate:
		return a.update(ctx, op.Message, op.MessageID)
	case channelcontract.DeliveryRecall:
		return a.recall(ctx, op.Message, op.MessageID)
	default:
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery: unsupported operation")
	}
}

func (a *Adapter) OpenCardStream(ctx context.Context, msg channelcontract.OutboundMessage) (channelcontract.CardStreamReceipt, channelcontract.CardStream, error) {
	if a.channel == nil || a.client == nil {
		return channelcontract.CardStreamReceipt{}, nil, errAdapterNotInitialized
	}
	if msg.Provider != "" && msg.Provider != channelcontract.ProviderFeishu {
		return channelcontract.CardStreamReceipt{}, nil, errors.New("feishu stream: provider mismatch")
	}
	if msg.AccountID != "" && msg.AccountID != a.accountID {
		return channelcontract.CardStreamReceipt{}, nil, errors.New("feishu stream: account mismatch")
	}
	if msg.ExternalChatID == "" || !validateCard(msg.Card) {
		return channelcontract.CardStreamReceipt{}, nil, errors.New("feishu stream: valid external chat id and card are required")
	}
	res, err := a.channel.Send(ctx, &larktypes.SendInput{ChatID: msg.ExternalChatID, ReplyMessageID: msg.ReplyToMessageID, Card: string(msg.Card), MsgType: "interactive"})
	if err != nil || res == nil || res.MessageID == "" {
		return channelcontract.CardStreamReceipt{}, nil, errors.New("feishu stream: initial card failed")
	}
	return channelcontract.CardStreamReceipt{MessageID: res.MessageID, ChatID: res.ChatID}, &cardStream{adapter: a, messageID: res.MessageID}, nil
}

type cardStream struct {
	adapter   *Adapter
	messageID string
}

func (s *cardStream) Update(ctx context.Context, card json.RawMessage) error {
	if s == nil || s.adapter == nil {
		return errors.New("feishu stream: nil controller")
	}
	_, err := s.adapter.update(ctx, channelcontract.OutboundMessage{Kind: channelcontract.MessageKindCard, Card: card}, s.messageID)
	return err
}

func (*cardStream) Close(context.Context) error { return nil }

func (a *Adapter) create(ctx context.Context, msg channelcontract.OutboundMessage) (channelcontract.DeliveryReceipt, error) {
	if a.channel == nil {
		return channelcontract.DeliveryReceipt{}, errAdapterNotInitialized
	}
	if msg.ExternalChatID == "" {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery create requires external chat id")
	}
	input := &larktypes.SendInput{ChatID: msg.ExternalChatID, ReplyMessageID: msg.ReplyToMessageID}
	if msg.Kind == channelcontract.MessageKindImage {
		if len(msg.Attachments) != 1 || strings.TrimSpace(msg.Attachments[0].ID) == "" {
			return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery image requires one asset attachment")
		}
		if a.resolveAttachment == nil {
			return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery image resolver is not configured")
		}
		attachment := msg.Attachments[0]
		data, err := a.resolveAttachment(ctx, attachment)
		if err != nil {
			return channelcontract.DeliveryReceipt{}, fmt.Errorf("feishu delivery image resolver failed: %w", err)
		}
		if len(data) == 0 {
			return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery image resolver returned empty bytes")
		}
		input.MsgType = "image"
		input.Media = &larktypes.UploadInput{Kind: larktypes.MediaKindImage, SourceBytes: data, FileName: safeAttachmentFilename(attachment.Name)}
	} else {
		switch msg.Kind {
		case channelcontract.MessageKindText:
			input.Text = msg.Text
		case channelcontract.MessageKindMarkdown:
			input.Markdown = msg.Text
		case channelcontract.MessageKindCard:
			if !validateCard(msg.Card) {
				return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery: invalid card JSON")
			}
			input.Card = string(msg.Card)
		default:
			return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery: unsupported message kind")
		}
	}
	res, err := a.channel.Send(ctx, input)
	if err != nil {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery create failed")
	}
	if res == nil || res.MessageID == "" {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery create returned no message id")
	}
	return channelcontract.DeliveryReceipt{MessageID: res.MessageID, ChatID: res.ChatID, DeliveredAt: time.Now()}, nil
}

func safeAttachmentFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "image.png"
	}
	name = path.Base(name)
	if name == "." || name == "/" {
		return "image.png"
	}
	return strings.ReplaceAll(name, "\"", "")
}

func (a *Adapter) update(ctx context.Context, msg channelcontract.OutboundMessage, messageID string) (channelcontract.DeliveryReceipt, error) {
	if a.client == nil {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu message update unavailable")
	}
	if messageID == "" {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery update requires message id")
	}
	if msg.Kind != channelcontract.MessageKindCard || !validateCard(msg.Card) {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery update requires valid card JSON")
	}
	req := larkim.NewPatchMessageReqBuilder().MessageId(messageID).Body(larkim.NewPatchMessageReqBodyBuilder().Content(string(msg.Card)).Build()).Build()
	resp, err := a.client.Im.V1.Message.Patch(ctx, req)
	if err != nil || resp == nil || !resp.Success() {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery update failed")
	}
	return channelcontract.DeliveryReceipt{MessageID: messageID, ChatID: msg.ExternalChatID, DeliveredAt: time.Now(), RequestID: resp.RequestId()}, nil
}

func (a *Adapter) recall(ctx context.Context, msg channelcontract.OutboundMessage, messageID string) (channelcontract.DeliveryReceipt, error) {
	if a.client == nil {
		return channelcontract.DeliveryReceipt{}, errUnsupportedRecall
	}
	if messageID == "" || msg.ExternalChatID == "" || msg.AccountID != a.accountID {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery recall requires message id, external chat id, and account ownership")
	}
	resp, err := a.client.Im.V1.Message.Delete(ctx, larkim.NewDeleteMessageReqBuilder().MessageId(messageID).Build())
	if err != nil || resp == nil || !resp.Success() {
		return channelcontract.DeliveryReceipt{}, errors.New("feishu delivery recall failed")
	}
	return channelcontract.DeliveryReceipt{MessageID: messageID, DeliveredAt: time.Now(), RequestID: resp.RequestId()}, nil
}

func (a *Adapter) ResolveThread(ctx context.Context, msg channelcontract.InboundMessage) (channelcontract.ThreadResolution, error) {
	if msg.ExternalThreadID != "" {
		return channelcontract.ThreadResolution{ThreadID: msg.ExternalThreadID, Source: channelcontract.ThreadSourceEvent}, nil
	}
	if msg.ExternalMessageID == "" || a.fetchRawMessage == nil {
		return channelcontract.ThreadResolution{Source: channelcontract.ThreadSourceNone}, nil
	}
	threadID, err := a.fetchRawMessage(ctx, msg.ExternalMessageID)
	if err != nil {
		return channelcontract.ThreadResolution{}, errors.New("feishu thread lookup failed")
	}
	if threadID == "" {
		return channelcontract.ThreadResolution{Source: channelcontract.ThreadSourceNone}, nil
	}
	return channelcontract.ThreadResolution{ThreadID: threadID, Source: channelcontract.ThreadSourceRawMessageLookup}, nil
}

func (a *Adapter) Health(context.Context) channelcontract.Health {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.health
}

func dispositionError(d channelcontract.InboundDisposition) error {
	if err := d.Validate(); err != nil {
		return errors.New("feishu inbound disposition invalid")
	}
	if d.Ack {
		return nil
	}
	return fmt.Errorf("%w: %s", errRetryableInbound, d.ErrorCode)
}

func normalizeMessage(accountID string, msg *larktypes.NormalizedMessage) (channelcontract.InboundMessage, error) {
	return normalizeMessageWithIdentity(accountID, "", "", msg)
}

func normalizeMessageWithAppID(accountID, appID string, msg *larktypes.NormalizedMessage) (channelcontract.InboundMessage, error) {
	return normalizeMessageWithIdentity(accountID, appID, "", msg)
}

func normalizeMessageWithIdentity(accountID, appID, botOpenID string, msg *larktypes.NormalizedMessage) (channelcontract.InboundMessage, error) {
	if msg == nil {
		return channelcontract.InboundMessage{}, errors.New("nil normalized message")
	}
	chatType := channelcontract.ChatTypeP2P
	if msg.ChatType == string(channelcontract.ChatTypeGroup) {
		chatType = channelcontract.ChatTypeGroup
	}
	mentions := make([]channelcontract.Mention, 0, len(msg.Mentions))
	for _, mention := range msg.Mentions {
		externalID := mention.OpenID
		if externalID == "" {
			externalID = mention.UserID
		}
		mentions = append(mentions, channelcontract.Mention{Key: mention.Key, ExternalID: externalID, Name: mention.Name, IsBot: mention.IsBot || (botOpenID != "" && externalID == botOpenID)})
	}
	raw, _ := json.Marshal(msg.RawEvent)
	mentionedBot := msg.MentionedBot || normalizedMentionsContainOpenID(msg.Mentions, botOpenID) || rawEventMentionsAppID(msg.RawEvent, appID)
	createdAt := time.Time{}
	if msg.CreateTimeMs > 0 {
		createdAt = time.UnixMilli(msg.CreateTimeMs)
	}
	threadID := extractThreadID(msg.RawEvent)
	return channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: accountID, EventID: msg.EventID, ExternalMessageID: msg.MessageID, ExternalConversationID: msg.ChatID, ExternalThreadID: threadID, ExternalUserID: msg.UserID, ChatType: chatType, Text: normalizedMessageText(msg), Mentions: mentions, MentionedBot: mentionedBot, CreatedAt: createdAt, RawMetadata: raw}, nil
}

const richTextMessagePlaceholder = "[rich text message]"

func normalizedMessageText(msg *larktypes.NormalizedMessage) string {
	if msg == nil {
		return ""
	}
	if strings.TrimSpace(msg.Content) != richTextMessagePlaceholder {
		return msg.Content
	}
	raw, ok := msg.RawEvent.(*larkim.P2MessageReceiveV1)
	if !ok || raw == nil || raw.Event == nil || raw.Event.Message == nil || raw.Event.Message.Content == nil || raw.Event.Message.MessageType == nil {
		return msg.Content
	}
	content, _ := larknormalize.ParseContent(*raw.Event.Message.MessageType, *raw.Event.Message.Content)
	if strings.TrimSpace(content) != richTextMessagePlaceholder && strings.TrimSpace(content) != "" {
		return content
	}
	return extractRichTextContent(*raw.Event.Message.Content)
}

func extractRichTextContent(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return ""
	}
	parts := make([]string, 0, 4)
	var visit func(any)
	visit = func(node any) {
		switch current := node.(type) {
		case []any:
			for _, child := range current {
				visit(child)
			}
		case map[string]any:
			for _, key := range []string{"title", "text"} {
				if text, ok := current[key].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			keys := make([]string, 0, len(current))
			for key := range current {
				if key != "title" && key != "text" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				visit(current[key])
			}
		}
	}
	visit(value)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func (a *Adapter) resolveBotOpenID(ctx context.Context) string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	openID := a.botOpenID
	a.mu.Unlock()
	if openID != "" || a.channel == nil {
		return openID
	}
	identity := a.channel.GetBotIdentity(ctx)
	if identity == nil || strings.TrimSpace(identity.OpenID) == "" {
		return ""
	}
	a.mu.Lock()
	if a.botOpenID == "" {
		a.botOpenID = identity.OpenID
	}
	openID = a.botOpenID
	a.mu.Unlock()
	return openID
}

func normalizedMentionsContainOpenID(mentions []larktypes.Mention, botOpenID string) bool {
	botOpenID = strings.TrimSpace(botOpenID)
	if botOpenID == "" {
		return false
	}
	for _, mention := range mentions {
		if mention.OpenID == botOpenID || mention.UserID == botOpenID {
			return true
		}
	}
	return false
}

func rawEventMentionsAppID(event interface{}, appID string) bool {
	appID = strings.TrimSpace(appID)
	if appID == "" || event == nil {
		return false
	}
	if raw, ok := event.(*larkim.P2MessageReceiveV1); ok && raw != nil && raw.EventReq != nil {
		var payload map[string]interface{}
		if json.Unmarshal(raw.Body, &payload) == nil {
			if eventPayload, ok := payload["event"].(map[string]interface{}); ok {
				if messagePayload, ok := eventPayload["message"].(map[string]interface{}); ok {
					if mentions, ok := messagePayload["mentions"].([]interface{}); ok {
						return rawMentionsContainAppID(mentions, appID)
					}
				}
			}
		}
	}
	return false
}

func rawMentionsContainAppID(mentions []interface{}, appID string) bool {
	for _, value := range mentions {
		mention, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		idType, _ := mention["id_type"].(string)
		if idType != "app_id" {
			continue
		}
		if id, ok := mention["id"].(string); ok && id == appID {
			return true
		}
		if id, ok := mention["app_id"].(string); ok && id == appID {
			return true
		}
		if id, ok := mention["id"].(map[string]interface{}); ok {
			if app, ok := id["app_id"].(string); ok && app == appID {
				return true
			}
		}
	}
	return false
}

func normalizeCardAction(accountID string, event *larktypes.CardActionEvent) channelcontract.InboundMessage {
	if event == nil {
		return channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: accountID}
	}
	raw, _ := json.Marshal(event)
	text := event.Action.Name
	if text == "" {
		text = event.Action.Tag
	}
	chatID := event.ChatID
	if chatID == "" {
		chatID = event.Context.OpenChatID
	}
	userID := event.Operator.OpenID
	if userID == "" {
		userID = event.Operator.UserID
	}
	chatType := channelcontract.ChatTypeP2P
	callback := &channelcontract.CardAction{}
	if event.Action.Value != nil {
		callback.Token, _ = event.Action.Value["token"].(string)
		callback.Action, _ = event.Action.Value["action"].(string)
		callback.ChoiceID, _ = event.Action.Value["choice_id"].(string)
	}
	if callback.Token == "" && callback.Action == "" {
		callback = nil
	}
	var rawMap map[string]interface{}
	if json.Unmarshal(raw, &rawMap) == nil {
		if value := findStringJSON(rawMap, "chat_type"); value == string(channelcontract.ChatTypeGroup) {
			chatType = channelcontract.ChatTypeGroup
		}
		if value := findStringJSON(rawMap, "host"); strings.Contains(strings.ToLower(value), "group") {
			chatType = channelcontract.ChatTypeGroup
		}
	}
	return channelcontract.InboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: accountID, EventID: event.EventID, ExternalMessageID: event.MessageID, ExternalConversationID: chatID, ExternalUserID: userID, ChatType: chatType, Text: text, MentionedBot: true, RawMetadata: raw, CreatedAt: time.Now(), CardAction: callback}
}

func findStringJSON(value interface{}, wanted string) string {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if strings.EqualFold(key, wanted) {
				if s, ok := child.(string); ok {
					return s
				}
			}
			if found := findStringJSON(child, wanted); found != "" {
				return found
			}
		}
	case []interface{}:
		for _, child := range v {
			if found := findStringJSON(child, wanted); found != "" {
				return found
			}
		}
	}
	return ""
}

func extractThreadID(raw interface{}) string {
	if raw == nil {
		return ""
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return extractThreadReflect(reflect.ValueOf(raw))
	}
	var value interface{}
	if json.Unmarshal(b, &value) != nil {
		return ""
	}
	return findThreadJSON(value)
}

func findThreadJSON(value interface{}) string {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, child := range v {
			if strings.EqualFold(strings.ReplaceAll(key, "_", ""), "threadid") {
				if s, ok := child.(string); ok {
					return s
				}
			}
			if found := findThreadJSON(child); found != "" {
				return found
			}
		}
	case []interface{}:
		for _, child := range v {
			if found := findThreadJSON(child); found != "" {
				return found
			}
		}
	}
	return ""
}

func extractThreadReflect(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		return extractThreadReflect(v.Elem())
	}
	if v.Kind() == reflect.Struct {
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if strings.EqualFold(strings.ReplaceAll(field.Name, "_", ""), "threadid") && v.Field(i).Kind() == reflect.String {
				return v.Field(i).String()
			}
			if found := extractThreadReflect(v.Field(i)); found != "" {
				return found
			}
		}
	}
	return ""
}

func (a *Adapter) fetchThreadFromAPI(ctx context.Context, messageID string) (string, error) {
	if a.client == nil {
		return "", errAdapterNotInitialized
	}
	resp, err := a.client.Im.V1.Message.Get(ctx, larkim.NewGetMessageReqBuilder().MessageId(messageID).Build())
	if err != nil || resp == nil || !resp.Success() || resp.Data == nil || len(resp.Data.Items) == 0 || resp.Data.Items[0] == nil || resp.Data.Items[0].ThreadId == nil {
		return "", err
	}
	return *resp.Data.Items[0].ThreadId, nil
}

func validateCard(card []byte) bool {
	if len(card) == 0 || len(card) > maxCardBytes || !json.Valid(card) {
		return false
	}
	var payload map[string]interface{}
	if json.Unmarshal(card, &payload) != nil || payload["schema"] != "2.0" {
		return false
	}
	_, hasHeader := payload["header"]
	body, hasBody := payload["body"]
	if !hasHeader || !hasBody {
		return false
	}
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		return false
	}
	elements, ok := bodyMap["elements"].([]interface{})
	return ok && len(elements) > 0 && len(elements) <= maxCardElements
}
