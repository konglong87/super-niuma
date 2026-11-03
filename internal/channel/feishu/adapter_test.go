package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	larktypes "github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

const (
	expectedToolDetailsPanelElementID   = "tool_details_panel"
	expectedToolDetailsContentElementID = "tool_details_content"
)

type fakeChannel struct {
	startCount      atomic.Int32
	stopCount       atomic.Int32
	lastInput       *larktypes.SendInput
	reactionHandler func(context.Context, *larktypes.ReactionEvent) error
	botIdentity     *larktypes.BotIdentity
}

func TestDeliverImageResolvesAndUploadsAuthorizedAsset(t *testing.T) {
	fake := &fakeChannel{}
	adapter := NewAdapterWithChannel(Config{
		AccountID: "acct",
		ResolveAttachment: func(context.Context, channelcontract.Attachment) ([]byte, error) {
			return []byte("png-bytes"), nil
		},
	}, fake)
	_, err := adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryCreate, Message: channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc_1", Kind: channelcontract.MessageKindImage,
		Attachments: []channelcontract.Attachment{{ID: "asset-1", Type: "image", MediaType: "image/png", Name: "asset.png", TenantID: 7, UserID: 11, SessionID: 13}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if fake.lastInput == nil || fake.lastInput.Media == nil || fake.lastInput.Media.Kind != larktypes.MediaKindImage || string(fake.lastInput.Media.SourceBytes) != "png-bytes" || fake.lastInput.Media.FileName != "asset.png" {
		t.Fatalf("image send input = %+v", fake.lastInput)
	}
}

func TestDeliverImageRejectsMissingResolver(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	_, err := adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryCreate, Message: channelcontract.OutboundMessage{
		Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc_1", Kind: channelcontract.MessageKindImage,
		Attachments: []channelcontract.Attachment{{ID: "asset-1", Type: "image"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "resolver") {
		t.Fatalf("error = %v, want resolver error", err)
	}
}

func (f *fakeChannel) Send(_ context.Context, input *larktypes.SendInput) (*larktypes.SendResult, error) {
	f.lastInput = input
	return &larktypes.SendResult{MessageID: "om_out", ChatID: "oc_1"}, nil
}
func (f *fakeChannel) OnMessage(func(context.Context, *larktypes.NormalizedMessage) error) {}
func (f *fakeChannel) OnReaction(handler func(context.Context, *larktypes.ReactionEvent) error) {
	f.reactionHandler = handler
}
func (f *fakeChannel) OnComment(func(context.Context, *larktypes.CommentEvent) error)       {}
func (f *fakeChannel) OnBotAdded(func(context.Context, *larktypes.BotAddedEvent) error)     {}
func (f *fakeChannel) OnCardAction(func(context.Context, *larktypes.CardActionEvent) error) {}
func (f *fakeChannel) OnReject(func(context.Context, *larktypes.RejectEvent) error)         {}
func (f *fakeChannel) DownloadFile(context.Context, string, string) ([]byte, error)         { return nil, nil }
func (f *fakeChannel) OnReady(func())                                                       {}
func (f *fakeChannel) OnError(func(error))                                                  {}
func (f *fakeChannel) OnReconnecting(func())                                                {}
func (f *fakeChannel) OnReconnected(func())                                                 {}
func (f *fakeChannel) OnDisconnected(func())                                                {}
func (f *fakeChannel) Start(context.Context) error                                          { f.startCount.Add(1); return nil }
func (f *fakeChannel) Stream(context.Context, *larktypes.SendInput) (larktypes.StreamController, error) {
	return nil, nil
}
func (f *fakeChannel) UpdatePolicy(larktypes.PolicyConfig)                   {}
func (f *fakeChannel) GetPolicy() larktypes.PolicyConfig                     { return larktypes.PolicyConfig{} }
func (f *fakeChannel) GetBotIdentity(context.Context) *larktypes.BotIdentity { return f.botIdentity }
func (f *fakeChannel) Stop(context.Context) error                            { f.stopCount.Add(1); return nil }

func TestNormalizeMessagePreservesProviderFields(t *testing.T) {
	timeMs := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC).UnixMilli()
	normalized := &larktypes.NormalizedMessage{
		EventID: "evt_1", MessageID: "om_1", ChatID: "oc_1", ChatType: "group", UserID: "ou_1",
		Content: "hello", RawContentType: "text", MentionedBot: true,
		Mentions:     []larktypes.Mention{{Key: "@_user_1", OpenID: "ou_2", Name: "Ada", IsBot: false}},
		CreateTimeMs: timeMs,
	}
	got, err := normalizeMessage("acct", normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != channelcontract.ProviderFeishu || got.AccountID != "acct" || got.ExternalMessageID != "om_1" || got.ExternalConversationID != "oc_1" {
		t.Fatalf("unexpected identity: %+v", got)
	}
	if got.ChatType != channelcontract.ChatTypeGroup || got.Text != "hello" || !got.MentionedBot || len(got.Mentions) != 1 || got.Mentions[0].ExternalID != "ou_2" {
		t.Fatalf("unexpected normalized message: %+v", got)
	}
	if !got.CreatedAt.Equal(time.UnixMilli(timeMs)) {
		t.Fatalf("unexpected created_at: %v", got.CreatedAt)
	}
}

func TestNormalizeMessageRecoversRichTextFromRawFeishuEvent(t *testing.T) {
	messageType := "post"
	content := `{"zh_cn":{"title":"桌面验收","content":[[{"tag":"text","text":"桌面飞书闭环验收 2026-09-22"}]]}}`
	raw := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{MessageType: &messageType, Content: &content},
		},
	}
	got, err := normalizeMessage("acct", &larktypes.NormalizedMessage{
		EventID: "evt_rich", MessageID: "om_rich", ChatID: "oc_rich", ChatType: "p2p",
		UserID: "ou_rich", Content: richTextMessagePlaceholder, RawContentType: messageType, RawEvent: raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "**桌面验收**\n\n桌面飞书闭环验收 2026-09-22" {
		t.Fatalf("recovered text = %q", got.Text)
	}
}

func TestNormalizeMessageRecognizesAppIDMentionFromRawEvent(t *testing.T) {
	raw := &larkim.P2MessageReceiveV1{EventReq: &larkevent.EventReq{Body: []byte(`{"event":{"message":{"chat_type":"group","mentions":[{"key":"@_user_1","id":{"app_id":"cli_target"},"id_type":"app_id","name":"niuma-e2e-002"}]}}}`)}}
	normalized := &larktypes.NormalizedMessage{EventID: "evt_group", MessageID: "om_group", ChatID: "oc_group", ChatType: "group", UserID: "ou_user", Content: "hello", RawEvent: raw}
	got, err := normalizeMessageWithAppID("acct", "cli_target", normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChatType != channelcontract.ChatTypeGroup || !got.MentionedBot {
		t.Fatalf("expected app-id mention to pass group gate: %+v", got)
	}
	withoutMention := &larkim.P2MessageReceiveV1{EventReq: &larkevent.EventReq{Body: []byte(`{"event":{"sender":{"sender_id":{"app_id":"cli_target"}},"message":{"chat_type":"group","mentions":[]}}}`)}}
	normalized.RawEvent = withoutMention
	got, err = normalizeMessageWithAppID("acct", "cli_target", normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got.MentionedBot {
		t.Fatal("sender app_id must not satisfy the group mention gate")
	}
}

func TestNormalizeMessageRecognizesBotOpenIDMention(t *testing.T) {
	normalized := &larktypes.NormalizedMessage{
		EventID: "evt_group_open_id", MessageID: "om_group_open_id", ChatID: "oc_group", ChatType: "group", UserID: "ou_user", Content: "hello",
		Mentions: []larktypes.Mention{{Key: "@_user_1", OpenID: "ou_bot", Name: "niuma-e2e-002"}},
	}
	got, err := normalizeMessageWithIdentity("acct", "cli_target", "ou_bot", normalized)
	if err != nil {
		t.Fatal(err)
	}
	if got.ChatType != channelcontract.ChatTypeGroup || !got.MentionedBot || !got.Mentions[0].IsBot {
		t.Fatalf("expected bot open_id mention to pass group gate: %+v", got)
	}
}

func TestCapabilitiesAdvertiseFeishuCardFeatures(t *testing.T) {
	adapter := NewAdapter(Config{AccountID: "acct", AppID: "app", AppSecret: "secret"})
	cap := adapter.Capabilities()
	if !cap.FinalCard || !cap.CardActions || !cap.MessageUpdate || !cap.StreamingCard || !cap.Markdown {
		t.Fatalf("unexpected capabilities: %+v", cap)
	}
	if !cap.SupportsStreamingCard() || cap.MaxMessageBytes <= 0 || cap.MaxCardBytes <= 0 {
		t.Fatalf("expected bounded streaming capabilities: %+v", cap)
	}
}

func TestRenderCardV2ContainsSafeControls(t *testing.T) {
	card, err := RenderCard(channelcontract.CardState{
		RunID: "run_1", Status: channelcontract.CardRunning, Text: "working", RenderVersion: 2,
		Tools: []channelcontract.ToolProgress{{Name: "Read", Status: "complete"}},
	}, CardOptions{CallbackToken: "opaque-token"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(card, &got); err != nil {
		t.Fatal(err)
	}
	if got["schema"] != "2.0" {
		t.Fatalf("expected card schema 2.0: %s", card)
	}
	header := got["header"].(map[string]interface{})
	if header["title"].(map[string]interface{})["tag"] != "plain_text" {
		t.Fatalf("header title must use plain_text: %s", card)
	}
	if !strings.Contains(string(card), "opaque-token") {
		t.Fatalf("callback token missing: %s", card)
	}
}

func TestRenderCardIncludesToolDetailsAndSupportsOffMode(t *testing.T) {
	state := channelcontract.CardState{
		RunID: "run_details", Status: channelcontract.CardFailed, Text: "failed",
		Tools: []channelcontract.ToolProgress{{ID: "tool-1", Name: "Bash", Status: "failed", Command: "docker compose up", OutputPreview: "no configuration file provided", IsError: true}},
	}
	card, err := RenderCard(state, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	panel := findCardElementByID(t, card, expectedToolDetailsPanelElementID)
	if panel["tag"] != "collapsible_panel" || panel["expanded"] != false {
		t.Fatalf("tool details must use a collapsed native panel: %+v", panel)
	}
	header := panel["header"].(map[string]interface{})
	if got := header["title"].(map[string]interface{})["content"]; got != "执行失败 · 1 个工具 · 1 个失败" {
		t.Fatalf("tool summary = %q", got)
	}
	icon := header["icon"].(map[string]interface{})
	if icon["tag"] != "standard_icon" || icon["token"] != "down-small-ccm_outlined" || header["icon_position"] != "right" || header["icon_expanded_angle"] != float64(-180) {
		t.Fatalf("tool panel disclosure icon = %+v", header)
	}
	children := panel["elements"].([]interface{})
	if len(children) != 1 || children[0].(map[string]interface{})["tag"] != cardTagMarkdown {
		t.Fatalf("tool panel must contain one bounded markdown element: %+v", children)
	}
	if !strings.Contains(string(card), "docker compose up") || !strings.Contains(string(card), "no configuration file provided") || !strings.Contains(string(card), "错误") {
		t.Fatalf("tool details missing from card: %s", card)
	}
	offCard, err := RenderCard(state, CardOptions{ToolDetailsMode: channelcontract.ToolDetailsOff})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(offCard), "docker compose up") || strings.Contains(string(offCard), "no configuration file provided") {
		t.Fatalf("off mode rendered tool details: %s", offCard)
	}
	if hasCardElementByID(t, offCard, expectedToolDetailsPanelElementID) {
		t.Fatalf("off mode rendered tool panel: %s", offCard)
	}
}

func TestRenderCardToolSummaryCoversRuntimeStates(t *testing.T) {
	tests := []struct {
		name    string
		status  channelcontract.CardStatus
		tools   []channelcontract.ToolProgress
		summary string
	}{
		{name: "running", status: channelcontract.CardRunning, tools: []channelcontract.ToolProgress{{Name: "Read", Status: channelcontract.ToolStatusComplete}, {Name: "Bash\nunsafe", Status: channelcontract.ToolStatusRunning}}, summary: "处理中 · 2 个工具 · 0 个失败 · 当前 Bash unsafe"},
		{name: "completed", status: channelcontract.CardCompleted, tools: []channelcontract.ToolProgress{{Name: "Read", Status: channelcontract.ToolStatusComplete}}, summary: "已完成 · 1 个工具 · 0 个失败"},
		{name: "failed", status: channelcontract.CardFailed, tools: []channelcontract.ToolProgress{{Name: "Bash", Status: channelcontract.ToolStatusFailed}}, summary: "执行失败 · 1 个工具 · 1 个失败"},
		{name: "waiting input", status: channelcontract.CardWaitingInput, tools: []channelcontract.ToolProgress{{Name: "AskUserQuestion", Status: channelcontract.ToolStatusRunning}}, summary: "等待回答 · 1 个工具 · 0 个失败"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card, err := RenderCard(channelcontract.CardState{Status: tt.status, Text: "answer", Tools: tt.tools}, CardOptions{})
			if err != nil {
				t.Fatal(err)
			}
			panel := findCardElementByID(t, card, expectedToolDetailsPanelElementID)
			header := panel["header"].(map[string]interface{})
			if got := header["title"].(map[string]interface{})["content"]; got != tt.summary {
				t.Fatalf("summary = %q, want %q", got, tt.summary)
			}
		})
	}
}

func TestRenderStreamingAndFinalCardsKeepStableCollapsedToolPanel(t *testing.T) {
	running := channelcontract.CardState{Status: channelcontract.CardRunning, Text: "working", Tools: []channelcontract.ToolProgress{{ID: "tool-1", Name: "Bash", Status: channelcontract.ToolStatusRunning}}}
	completed := channelcontract.CardState{Status: channelcontract.CardCompleted, Text: "done", Tools: []channelcontract.ToolProgress{{ID: "tool-1", Name: "Bash", Status: channelcontract.ToolStatusComplete}}}
	streamingCard, err := RenderStreamingCard(running, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	finalCard, err := RenderFinalCard(completed, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for name, card := range map[string]json.RawMessage{"streaming": streamingCard, "final": finalCard} {
		panel := findCardElementByID(t, card, expectedToolDetailsPanelElementID)
		if panel["expanded"] != false {
			t.Fatalf("%s panel expanded = %v", name, panel["expanded"])
		}
		children := panel["elements"].([]interface{})
		if children[0].(map[string]interface{})["element_id"] != expectedToolDetailsContentElementID {
			t.Fatalf("%s tool content element id = %+v", name, children[0])
		}
	}
}

func TestRenderCardToolDetailsRemainBoundedAndEscaped(t *testing.T) {
	tools := make([]channelcontract.ToolProgress, maxVisibleToolDetails+2)
	for index := range tools {
		tools[index] = channelcontract.ToolProgress{Name: "Bash`", Status: channelcontract.ToolStatusComplete, OutputPreview: strings.Repeat("界", maxToolDetailsRunes)}
	}
	card, err := RenderCard(channelcontract.CardState{Status: channelcontract.CardCompleted, Text: "done", Tools: tools}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	panel := findCardElementByID(t, card, expectedToolDetailsPanelElementID)
	content := panel["elements"].([]interface{})[0].(map[string]interface{})["content"].(string)
	if utf8.RuneCountInString(content) > maxToolDetailsRunes || !strings.Contains(content, "输出已截断") {
		t.Fatalf("tool markdown was not rune-bounded: runes=%d content=%q", utf8.RuneCountInString(content), content)
	}
	if strings.Contains(content, "`Bash``") {
		t.Fatalf("inline markdown was not escaped: %q", content)
	}
}

func TestRenderCardDowngradesToolPanelWhenEscapedJSONExceedsLimit(t *testing.T) {
	card, err := RenderCard(channelcontract.CardState{
		Status: channelcontract.CardCompleted,
		Text:   "answer survives",
		Tools:  []channelcontract.ToolProgress{{Name: "Bash", Status: channelcontract.ToolStatusComplete, OutputPreview: strings.Repeat("<", maxToolDetailsRunes)}},
	}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(card) > maxCardBytes || !strings.Contains(string(card), "answer survives") {
		t.Fatalf("invalid downgraded card: bytes=%d card=%s", len(card), card)
	}
	if hasCardElementByID(t, card, expectedToolDetailsPanelElementID) {
		t.Fatalf("oversized tool panel was not downgraded: %s", card)
	}
}

func TestRenderCardWaitingQuestionUsesStandaloneChoiceButtons(t *testing.T) {
	card, err := RenderCard(channelcontract.CardState{Status: channelcontract.CardWaitingInput, Question: &channelcontract.InteractionQuestion{ID: "interaction-1", Token: "opaque-token", Question: "Which path?", Choices: []string{"A", "B"}}}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(card)
	for _, want := range []string{"等待回答", "Which path?", "opaque-token", channelcontract.InteractionActionAnswer, "choice_id", "0", "1", "A", "B"} {
		if !strings.Contains(text, want) {
			t.Fatalf("waiting card missing %q: %s", want, card)
		}
	}
	if strings.Contains(text, `"tag":"action"`) {
		t.Fatalf("waiting card used unsupported action wrapper: %s", card)
	}
}

func TestRenderCardEscapesOutputCodeFence(t *testing.T) {
	card, err := RenderCard(channelcontract.CardState{
		Status: channelcontract.CardCompleted,
		Text:   "done",
		Tools:  []channelcontract.ToolProgress{{Name: "Bash", Status: channelcontract.ToolStatusComplete, OutputPreview: "```injected```"}},
	}, CardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(card), "```injected```") {
		t.Fatalf("output fence was not escaped: %s", card)
	}
}

func findCardElementByID(t *testing.T, card json.RawMessage, elementID string) map[string]interface{} {
	t.Helper()
	var decoded struct {
		Body struct {
			Elements []map[string]interface{} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, element := range decoded.Body.Elements {
		if element["element_id"] == elementID {
			return element
		}
	}
	t.Fatalf("element %q missing from card: %s", elementID, card)
	return nil
}

func hasCardElementByID(t *testing.T, card json.RawMessage, elementID string) bool {
	t.Helper()
	var decoded struct {
		Body struct {
			Elements []map[string]interface{} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal(card, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, element := range decoded.Body.Elements {
		if element["element_id"] == elementID {
			return true
		}
	}
	return false
}

func TestResolveThreadUsesRawLookupWhenEventHasNoThread(t *testing.T) {
	adapter := NewAdapter(Config{AccountID: "acct", AppID: "app", AppSecret: "secret"})
	adapter.fetchRawMessage = func(context.Context, string) (string, error) { return "thread_1", nil }
	got, err := adapter.ResolveThread(context.Background(), channelcontract.InboundMessage{ExternalMessageID: "om_1"})
	if err != nil || got.ThreadID != "thread_1" || got.Source != channelcontract.ThreadSourceRawMessageLookup {
		t.Fatalf("unexpected resolution: %+v, %v", got, err)
	}
}

func TestResolveThreadReturnsNoneWithoutMessageID(t *testing.T) {
	adapter := NewAdapter(Config{AccountID: "acct"})
	got, err := adapter.ResolveThread(context.Background(), channelcontract.InboundMessage{})
	if err != nil || got.Source != channelcontract.ThreadSourceNone {
		t.Fatalf("unexpected resolution: %+v, %v", got, err)
	}
}

func TestStartStopAreIdempotent(t *testing.T) {
	fake := &fakeChannel{}
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, fake)
	if err := adapter.Start(context.Background(), func(context.Context, channelcontract.InboundMessage) channelcontract.InboundDisposition {
		return channelcontract.AcceptInbound()
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.startCount.Load() != 1 || fake.stopCount.Load() != 1 {
		t.Fatalf("expected idempotent lifecycle, got starts=%d stops=%d", fake.startCount.Load(), fake.stopCount.Load())
	}
	if got := adapter.Health(context.Background()); got.LastError != "stopped" {
		t.Fatalf("expected stopped health, got %+v", got)
	}
}

func TestStartRegistersReactionNoopHandler(t *testing.T) {
	fake := &fakeChannel{}
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, fake)
	if err := adapter.Start(context.Background(), func(context.Context, channelcontract.InboundMessage) channelcontract.InboundDisposition {
		return channelcontract.AcceptInbound()
	}); err != nil {
		t.Fatal(err)
	}
	if fake.reactionHandler == nil {
		t.Fatal("reaction events must be acknowledged by the channel adapter")
	}
	if err := fake.reactionHandler(context.Background(), &larktypes.ReactionEvent{}); err != nil {
		t.Fatalf("reaction no-op handler: %v", err)
	}
}

func TestRetryDispositionMapsToSafeError(t *testing.T) {
	err := dispositionError(channelcontract.RetryInbound("temporary_failure"))
	if err == nil || errors.Is(err, errRetryableInbound) == false {
		t.Fatalf("expected retryable error, got %v", err)
	}
	if got := err.Error(); got != "feishu inbound retryable: temporary_failure" {
		t.Fatalf("unexpected safe error: %s", got)
	}
}

func TestRecallWithoutClientReturnsDeterministicUnsupportedError(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	_, err := adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryRecall, MessageID: "om_1"})
	if !errors.Is(err, errUnsupportedRecall) {
		t.Fatalf("expected unsupported recall, got %v", err)
	}
}

func TestDeliverCreateBuildsTextAndCardInputs(t *testing.T) {
	fake := &fakeChannel{}
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, fake)
	_, err := adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryCreate, Message: channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc_1", Kind: channelcontract.MessageKindText, Text: "hello"}})
	if err != nil || fake.lastInput == nil || fake.lastInput.Text != "hello" || fake.lastInput.ChatID != "oc_1" {
		t.Fatalf("unexpected text input: %+v, %v", fake.lastInput, err)
	}
	card := json.RawMessage(`{"schema":"2.0","header":{},"body":{"elements":[{"tag":"markdown","content":"ok"}]}}`)
	_, err = adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryCreate, Message: channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc_1", Kind: channelcontract.MessageKindCard, Card: card}})
	if err != nil || fake.lastInput == nil || fake.lastInput.Card != string(card) || fake.lastInput.MsgType != "" {
		t.Fatalf("unexpected card input: %+v, %v", fake.lastInput, err)
	}
}

func TestDeliverCreateRejectsMissingExternalChatID(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	_, err := adapter.Deliver(context.Background(), channelcontract.OutboundOperation{Operation: channelcontract.DeliveryCreate, Message: channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", ConversationID: "internal", Kind: channelcontract.MessageKindText, Text: "hello"}})
	if err == nil || !strings.Contains(err.Error(), "external chat id") {
		t.Fatalf("expected fail-closed chat id validation, got %v", err)
	}
}

func TestStartRegistersRawDispatcherAndPropagatesRetry(t *testing.T) {
	d := dispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient("app", "secret", larkws.WithEventHandler(d), larkws.WithAutoReconnect(false))
	adapter := NewAdapterWithChannel(Config{AccountID: "acct", WSClient: wsClient}, &fakeChannel{})
	if err := adapter.Start(context.Background(), func(context.Context, channelcontract.InboundMessage) channelcontract.InboundDisposition {
		return channelcontract.RetryInbound("temporary_failure")
	}); err != nil {
		t.Fatal(err)
	}
	req := &larkevent.EventReq{Body: []byte(`{"schema":"2.0","header":{"event_id":"evt","event_type":"im.message.receive_v1","create_time":"1720000000000"},"event":{"message":{"message_id":"om","chat_id":"oc","chat_type":"p2p","message_type":"text","content":"{\"text\":\"hi\"}"},"sender":{"sender_id":{"open_id":"ou"}}}}`)}
	resp := d.Handle(context.Background(), req)
	if resp == nil || resp.StatusCode < 400 {
		t.Fatalf("expected dispatcher error response for retryable disposition: %+v", resp)
	}
}

func TestStartDispatchesGroupAppMentionToHandler(t *testing.T) {
	d := dispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient("app", "secret", larkws.WithEventHandler(d), larkws.WithAutoReconnect(false))
	adapter := NewAdapterWithChannel(Config{AccountID: "acct", AppID: "cli_target", WSClient: wsClient}, &fakeChannel{})
	var got channelcontract.InboundMessage
	if err := adapter.Start(context.Background(), func(_ context.Context, msg channelcontract.InboundMessage) channelcontract.InboundDisposition {
		got = msg
		return channelcontract.AcceptInbound()
	}); err != nil {
		t.Fatal(err)
	}
	req := &larkevent.EventReq{Body: []byte(`{"schema":"2.0","header":{"event_id":"evt-group","event_type":"im.message.receive_v1","create_time":"1720000000000"},"event":{"sender":{"sender_id":{"open_id":"ou-user"}},"message":{"message_id":"om-group","chat_id":"oc-group","chat_type":"group","message_type":"text","content":"{\"text\":\"hello\"}","mentions":[{"key":"@_user_1","id":{"app_id":"cli_target"},"id_type":"app_id","name":"niuma-e2e-002"}]}}}`)}
	resp := d.Handle(context.Background(), req)
	if resp == nil || resp.StatusCode >= 400 {
		t.Fatalf("dispatcher response=%+v", resp)
	}
	if got.ChatType != channelcontract.ChatTypeGroup || !got.MentionedBot || got.ExternalConversationID != "oc-group" {
		t.Fatalf("group mention was not normalized for runtime: %+v", got)
	}
}

func TestStartDispatchesGroupOpenIDMentionToHandler(t *testing.T) {
	d := dispatcher.NewEventDispatcher("", "")
	wsClient := larkws.NewClient("app", "secret", larkws.WithEventHandler(d), larkws.WithAutoReconnect(false))
	fake := &fakeChannel{botIdentity: &larktypes.BotIdentity{OpenID: "ou_bot"}}
	adapter := NewAdapterWithChannel(Config{AccountID: "acct", AppID: "cli_target", WSClient: wsClient}, fake)
	var got channelcontract.InboundMessage
	if err := adapter.Start(context.Background(), func(_ context.Context, msg channelcontract.InboundMessage) channelcontract.InboundDisposition {
		got = msg
		return channelcontract.AcceptInbound()
	}); err != nil {
		t.Fatal(err)
	}
	req := &larkevent.EventReq{Body: []byte(`{"schema":"2.0","header":{"event_id":"evt-group-open-id","event_type":"im.message.receive_v1","create_time":"1720000000000"},"event":{"sender":{"sender_id":{"open_id":"ou-user"}},"message":{"message_id":"om-group-open-id","chat_id":"oc-group","chat_type":"group","message_type":"text","content":"{\"text\":\"hello\"}","mentions":[{"key":"@_user_1","id":{"open_id":"ou_bot"},"id_type":"open_id","name":"niuma-e2e-002"}]}}}`)}
	resp := d.Handle(context.Background(), req)
	if resp == nil || resp.StatusCode >= 400 {
		t.Fatalf("dispatcher response=%+v", resp)
	}
	if got.ChatType != channelcontract.ChatTypeGroup || !got.MentionedBot || got.ExternalConversationID != "oc-group" {
		t.Fatalf("open-id group mention was not normalized for runtime: %+v", got)
	}
}

func TestNewAdapterInitializesWebSocketEventDispatcher(t *testing.T) {
	a := NewAdapter(Config{AccountID: "acct", AppID: "cli_test", AppSecret: "secret"})
	if a.wsClient == nil || a.wsClient.EventHandler() == nil {
		t.Fatal("production WebSocket client must have an event dispatcher")
	}
}

func TestOpenCardStreamFailsClosedWithoutRESTClient(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	_, _, err := adapter.OpenCardStream(context.Background(), channelcontract.OutboundMessage{Provider: channelcontract.ProviderFeishu, AccountID: "acct", ExternalChatID: "oc_1", Kind: channelcontract.MessageKindCard, Card: json.RawMessage(`{"schema":"2.0","header":{},"body":{"elements":[]}}`)})
	if err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("expected fail-closed stream error, got %v", err)
	}
}

func TestReactionAdapterCreatesAndDeletesByProviderMessageID(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	var createdMessage, createdEmoji, deletedMessage, deletedReaction string
	adapter.createReactionAPI = func(_ context.Context, messageID, emoji string) (string, error) {
		createdMessage, createdEmoji = messageID, emoji
		return "reaction-1", nil
	}
	adapter.deleteReactionAPI = func(_ context.Context, messageID, reactionID string) error {
		deletedMessage, deletedReaction = messageID, reactionID
		return nil
	}
	got, err := adapter.CreateReaction(context.Background(), "om-1", channelcontract.ReactionTyping)
	if err != nil || got != "reaction-1" || createdMessage != "om-1" || createdEmoji != "Typing" {
		t.Fatalf("reaction=%q created=(%q,%q) err=%v", got, createdMessage, createdEmoji, err)
	}
	if err := adapter.DeleteReaction(context.Background(), "om-1", got); err != nil || deletedMessage != "om-1" || deletedReaction != got {
		t.Fatalf("deleted=(%q,%q) err=%v", deletedMessage, deletedReaction, err)
	}
}

func TestReactionAdapterRejectsInvalidInputs(t *testing.T) {
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, &fakeChannel{})
	if _, err := adapter.CreateReaction(context.Background(), "", channelcontract.ReactionDone); err == nil {
		t.Fatal("empty message id accepted")
	}
	if _, err := adapter.CreateReaction(context.Background(), "om-1", channelcontract.ReactionEmoji("unknown")); err == nil {
		t.Fatal("unknown emoji accepted")
	}
	if err := adapter.DeleteReaction(context.Background(), "om-1", ""); err == nil {
		t.Fatal("empty reaction id accepted")
	}
}

func TestNormalizeCardActionPreservesOpaquePermissionDecision(t *testing.T) {
	got := normalizeCardAction("acct", &larktypes.CardActionEvent{
		EventID: "evt-action", MessageID: "om-card", ChatID: "oc-1",
		Operator: larktypes.CardActionOperator{OpenID: "ou-admin"},
		Action:   larktypes.CardActionPayload{Value: map[string]interface{}{"token": "opaque-token", "action": "approve_once"}},
	})
	if got.CardAction == nil || got.CardAction.Token != "opaque-token" || got.CardAction.Action != "approve_once" || got.ExternalUserID != "ou-admin" {
		t.Fatalf("card action = %+v", got)
	}
}

func TestNormalizeCardActionPreservesQuestionChoiceID(t *testing.T) {
	got := normalizeCardAction("acct", &larktypes.CardActionEvent{
		EventID:   "evt-question",
		MessageID: "om-question",
		ChatID:    "oc-question",
		Operator:  larktypes.CardActionOperator{OpenID: "ou-user"},
		Action: larktypes.CardActionPayload{Value: map[string]interface{}{
			"token": "opaque-token", "action": channelcontract.InteractionActionAnswer, "choice_id": "1",
		}},
	})
	if got.CardAction == nil || got.CardAction.ChoiceID != "1" || got.CardAction.Action != channelcontract.InteractionActionAnswer {
		t.Fatalf("card action = %+v", got.CardAction)
	}
}
