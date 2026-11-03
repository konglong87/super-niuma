package feishu

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

func TestRenderPermissionCardContainsOpaqueActionsWithoutRawRequest(t *testing.T) {
	card, err := RenderPermissionCard(channelcontract.PermissionRequest{ToolName: "Bash", Reason: "需要读取状态", Request: "secret-token-must-not-render"}, "opaque-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"opaque-token", channelcontract.PermissionActionApproveOnce, channelcontract.PermissionActionApproveSession, channelcontract.PermissionActionDeny, "Bash"} {
		if !strings.Contains(string(card), want) {
			t.Fatalf("card missing %q: %s", want, card)
		}
	}
	if strings.Contains(string(card), "secret-token-must-not-render") {
		t.Fatalf("card leaked raw request: %s", card)
	}
	if strings.Contains(string(card), `"tag":"action"`) {
		t.Fatalf("card schema 2.0 must use standalone button elements: %s", card)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(card, &decoded); err != nil || decoded["schema"] != "2.0" {
		t.Fatalf("invalid card: %s err=%v", card, err)
	}
}

func TestSendPermissionCardRepliesInTargetChat(t *testing.T) {
	fake := &fakeChannel{}
	adapter := NewAdapterWithChannel(Config{AccountID: "acct"}, fake)
	messageID, err := adapter.SendPermissionCard(context.Background(), channelcontract.PermissionRequest{ExternalChatID: "oc-1", ReplyToMessageID: "om-source", ToolName: "Write"}, "opaque-token")
	if err != nil || messageID != "om_out" || fake.lastInput == nil || fake.lastInput.ChatID != "oc-1" || fake.lastInput.ReplyMessageID != "om-source" || fake.lastInput.Card == "" {
		t.Fatalf("message=%q input=%+v err=%v", messageID, fake.lastInput, err)
	}
}
