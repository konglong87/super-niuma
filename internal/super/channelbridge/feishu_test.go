package channelbridge

import (
	"context"
	"encoding/json"
	"errors"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/router"
	"testing"
)

type fakeAdapter struct {
	inbound                       ch.InboundHandler
	bot                           string
	resolved                      ch.ThreadResolution
	threadErr                     error
	start, stop, resolve, deliver int
	operation                     ch.OutboundOperation
}

func (*fakeAdapter) Provider() ch.Provider { return ch.ProviderFeishu }
func (*fakeAdapter) AccountID() string     { return "feishu" }
func (a *fakeAdapter) Start(_ context.Context, h ch.InboundHandler) error {
	a.inbound = h
	a.start++
	return nil
}
func (a *fakeAdapter) Stop(context.Context) error { a.stop++; return nil }
func (a *fakeAdapter) Deliver(_ context.Context, op ch.OutboundOperation) (ch.DeliveryReceipt, error) {
	a.deliver++
	a.operation = op
	return ch.DeliveryReceipt{MessageID: "provider-message", ChatID: op.Message.ExternalChatID}, nil
}
func (a *fakeAdapter) ResolveThread(context.Context, ch.InboundMessage) (ch.ThreadResolution, error) {
	a.resolve++
	return a.resolved, a.threadErr
}
func (*fakeAdapter) Capabilities() ch.Capabilities      { return ch.Capabilities{Reply: true} }
func (*fakeAdapter) Health(context.Context) ch.Health   { return ch.Health{Status: ch.HealthReady} }
func (a *fakeAdapter) BotOpenID(context.Context) string { return a.bot }
func incoming() ch.InboundMessage {
	return ch.InboundMessage{Provider: ch.ProviderFeishu, AccountID: "feishu", EventID: "event", ExternalMessageID: "message", ExternalConversationID: "chat", ExternalUserID: "human", ChatType: ch.ChatTypeP2P, Text: "/run safe", RawMetadata: json.RawMessage(`{"event":{"sender":{"sender_type":"user"}}}`)}
}
func feishuBridge(t *testing.T, adapter *fakeAdapter, handler InboundHandler) *Bridge {
	t.Helper()
	b, err := New(Options{Accounts: func() []Account { return []Account{{ID: "feishu", Provider: control.ProviderFeishu, Enabled: true}} }, Handler: handler, AdapterFactory: func(Account) (ch.Adapter, error) { return adapter, nil }, Getenv: func(string) string { return "" }, AuthorizeTarget: allowedTarget})
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}
func TestFeishuUsesExistingAdapterButRequiresBotAndProvenThread(t *testing.T) {
	calls := 0
	a := &fakeAdapter{}
	b := feishuBridge(t, a, handleFunc(func(_ context.Context, e router.Envelope) (router.Decision, error) {
		calls++
		if !e.Authenticated || !e.ThreadKnown || e.Account != "feishu" {
			t.Fatal("untrusted mapping")
		}
		return router.Decision{Action: router.ActionRun, Code: router.CodeAccepted}, nil
	}))
	m := incoming()
	if d := a.inbound(context.Background(), m); !d.Retryable || d.ErrorCode != "bot_identity_unknown" || a.resolve != 0 || calls != 0 {
		t.Fatalf("unknown bot %+v", d)
	}
	a.bot = "bot"
	if d := a.inbound(context.Background(), m); !d.Retryable || d.ErrorCode != "thread_unknown" || calls != 0 {
		t.Fatalf("unknown root %+v", d)
	}
	a.resolved = ch.ThreadResolution{Source: ch.ThreadSourceRawMessageLookup}
	if d := a.inbound(context.Background(), m); !d.Ack || calls != 1 {
		t.Fatalf("proven root %+v", d)
	}
	if err := b.Start(context.Background()); err != nil || a.start != 1 {
		t.Fatal("duplicate transport start")
	}
}
func TestFeishuSelfOtherBotsCallbacksAndThreadFailureNeverRoute(t *testing.T) {
	a := &fakeAdapter{bot: "bot", resolved: ch.ThreadResolution{Source: ch.ThreadSourceRawMessageLookup}}
	feishuBridge(t, a, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) {
		t.Fatal("unsafe ingress reached router")
		return router.Decision{}, nil
	}))
	for _, kind := range []string{"self", "app", "callback", "metadata", "thread", "event mismatch", "account"} {
		m := incoming()
		a.threadErr = nil
		a.resolved = ch.ThreadResolution{Source: ch.ThreadSourceRawMessageLookup}
		switch kind {
		case "self":
			m.ExternalUserID = "bot"
		case "app":
			m.RawMetadata = json.RawMessage(`{"event":{"sender":{"sender_type":"app"}}}`)
		case "callback":
			m.CardAction = &ch.CardAction{Action: "approve"}
		case "metadata":
			m.RawMetadata = nil
		case "thread":
			a.threadErr = errors.New("provider-secret-error")
		case "event mismatch":
			a.resolved = ch.ThreadResolution{Source: ch.ThreadSourceEvent, ThreadID: "different"}
		case "account":
			m.AccountID = "other"
		}
		d := a.inbound(context.Background(), m)
		if err := d.Validate(); err != nil {
			t.Fatalf("%s invalid disposition %+v", kind, d)
		}
	}
}
func TestFeishuSenderAndClosedLifecycle(t *testing.T) {
	a := &fakeAdapter{bot: "bot"}
	b := feishuBridge(t, a, handleFunc(func(context.Context, router.Envelope) (router.Decision, error) { return router.Decision{}, nil }))
	target := control.Target{ID: "target", Provider: control.ProviderFeishu, Account: "feishu", Chat: "chat", Thread: "thread", ReplyTo: "original"}
	receipt, err := b.Send(context.Background(), target, "notice", "safe summary")
	if err != nil || receipt.MessageID != "provider-message" || a.operation.Message.IdempotencyKey != "notice" || a.operation.Message.ExternalThreadID != "thread" || a.operation.Message.ReplyToMessageID != "original" {
		t.Fatalf("%+v %v", receipt, err)
	}
	if err = b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = b.Close(context.Background()); err != nil || a.stop != 1 {
		t.Fatal("non-idempotent close")
	}
	if err = b.Start(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal("closed bridge restarted")
	}
}
