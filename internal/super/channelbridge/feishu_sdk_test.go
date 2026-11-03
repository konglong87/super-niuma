package channelbridge

import (
	"context"
	"encoding/json"
	ch "github.com/konglong87/super-niuma/internal/channel"
	"github.com/konglong87/super-niuma/internal/channel/feishu"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	"io"
	"net/http"
	"strings"
	"testing"
)

func offlineFeishu(t *testing.T, handle transportFunc) *strictFeishu {
	t.Helper()
	client := lark.NewClient("fixture-app", "fixture-secret", lark.WithEnableTokenCache(false), lark.WithHttpClient(&http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			return response(http.StatusOK, `{"code":0,"tenant_access_token":"fixture-token","expire":7200}`), nil
		}
		return handle(r)
	})}))
	return &strictFeishu{Adapter: feishu.NewAdapter(feishu.Config{AccountID: "feishu", AppID: "fixture-app", AppSecret: "fixture-secret", Client: client}), client: client}
}
func TestStrictFeishuLookupNeverTurnsProviderFailureIntoRoot(t *testing.T) {
	for _, body := range []string{`{"code":999,"msg":"fixture failure"}`, `{"code":0,"data":{"items":[]}}`, `{"code":0,"data":{"items":[{"message_id":"message","chat_id":"other"}]}}`, `{"code":0,"data":{"items":[{"message_id":"message","chat_id":"chat","deleted":true}]}}`} {
		adapter := offlineFeishu(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet {
				t.Fatal("unexpected effect")
			}
			return response(http.StatusOK, body), nil
		})
		if _, err := adapter.ResolveThread(context.Background(), incoming()); err == nil {
			t.Fatal("unproven root accepted")
		}
	}
	adapter := offlineFeishu(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"code":0,"data":{"items":[{"message_id":"message","chat_id":"chat"}]}}`), nil
	})
	resolved, err := adapter.ResolveThread(context.Background(), incoming())
	if err != nil || resolved.ThreadID != "" || resolved.Source != ch.ThreadSourceRawMessageLookup {
		t.Fatalf("proven root %+v %v", resolved, err)
	}
}
func TestStrictFeishuSendUsesStableUUIDAndExplicitThreadReply(t *testing.T) {
	var seenUUID string
	posts := 0
	adapter := offlineFeishu(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return response(http.StatusOK, `{"code":0,"data":{"items":[{"message_id":"original","chat_id":"chat","thread_id":"thread"}]}}`), nil
		}
		posts++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if json.Unmarshal(data, &body) != nil {
			t.Fatal("bad SDK payload")
		}
		id, _ := body["uuid"].(string)
		if id == "" || seenUUID != "" && seenUUID != id {
			t.Fatal("unstable provider idempotency key")
		}
		seenUUID = id
		if body["reply_in_thread"] != true || !strings.HasSuffix(r.URL.Path, "/original/reply") {
			t.Fatal("thread fallback")
		}
		return response(http.StatusOK, `{"code":0,"data":{"message_id":"sent","chat_id":"chat","thread_id":"thread"}}`), nil
	})
	op := ch.OutboundOperation{Operation: ch.DeliveryCreate, Message: ch.OutboundMessage{Provider: ch.ProviderFeishu, AccountID: "feishu", ExternalChatID: "chat", ExternalThreadID: "thread", ReplyToMessageID: "original", Kind: ch.MessageKindText, Text: "status", IdempotencyKey: "notice"}}
	for i := 0; i < 2; i++ {
		receipt, err := adapter.Deliver(context.Background(), op)
		if err != nil || receipt.MessageID != "sent" {
			t.Fatalf("%+v %v", receipt, err)
		}
	}
	if posts != 2 {
		t.Fatal("offline transport not exercised")
	}
	op.Message.ExternalThreadID = "other"
	if _, err := adapter.Deliver(context.Background(), op); err == nil || posts != 2 {
		t.Fatal("cross-thread reply sent")
	}
}
