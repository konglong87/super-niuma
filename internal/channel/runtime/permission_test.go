package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type permissionRepoFake struct {
	callback mysqlstore.ChannelCallback
	consumed bool
}

func (f *permissionRepoFake) CreateCallback(_ context.Context, input mysqlstore.ChannelCallbackInput) (mysqlstore.ChannelCallback, error) {
	f.callback = mysqlstore.ChannelCallback{ID: 1, TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: input.RunID, PlatformMessageID: input.PlatformMessageID, ActionID: input.ActionID, NonceHash: input.NonceHash, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, AllowedUserID: input.AllowedUserID, Status: mysqlstore.ChannelCallbackStatusPending, ExpiresAt: input.ExpiresAt}
	return f.callback, nil
}
func (f *permissionRepoFake) ConsumeCallback(_ context.Context, tenantID, accountID, conversationID uint64, runID, platformMessageID, actionID string, nonceHash []byte, allowedUserID uint64, fingerprint string, version int, now time.Time) (mysqlstore.ChannelCallback, error) {
	if f.consumed || tenantID != f.callback.TenantID || accountID != f.callback.AccountID || conversationID != f.callback.ConversationID || runID != f.callback.RunID || platformMessageID != f.callback.PlatformMessageID || actionID != f.callback.ActionID || allowedUserID != f.callback.AllowedUserID || fingerprint != f.callback.RuntimeFingerprint || version != f.callback.RuntimeFingerprintVersion || now.After(f.callback.ExpiresAt) || string(nonceHash) != string(f.callback.NonceHash) {
		return mysqlstore.ChannelCallback{}, errors.New("not found")
	}
	f.consumed = true
	f.callback.Status = mysqlstore.ChannelCallbackStatusConsumed
	return f.callback, nil
}

type permissionSenderFake struct {
	token string
	sent  chan struct{}
}

func (f *permissionSenderFake) SendPermissionCard(_ context.Context, _ channelcontract.PermissionRequest, token string) (string, error) {
	f.token = token
	close(f.sent)
	return "om-permission", nil
}

func TestPermissionBrokerAcceptsOnlyBoundUserAndConsumesNonceOnce(t *testing.T) {
	repo := &permissionRepoFake{}
	sender := &permissionSenderFake{sent: make(chan struct{})}
	broker := NewPermissionBroker(PermissionBrokerConfig{TenantID: 1, AccountID: 2, RuntimeFingerprint: "fp", RuntimeFingerprintVersion: 1, Repo: repo, Sender: sender, Timeout: time.Second, AdminOpenIDs: []string{"ou-owner"}})
	request := channelcontract.PermissionRequest{ConversationID: 3, RunID: "run-1", UserID: 4, ExternalUserID: "ou-owner", ExternalChatID: "oc-1", ChatType: channelcontract.ChatTypeP2P, ToolName: "Bash", Request: "git status"}
	decisionCh := make(chan channelcontract.PermissionDecision, 1)
	go func() { decisionCh <- broker.Request(context.Background(), request) }()
	select {
	case <-sender.sent:
	case <-time.After(time.Second):
		t.Fatal("permission card was not sent")
	}
	if broker.Resolve(context.Background(), sender.token, channelcontract.PermissionActionApproveOnce, "ou-other", "oc-1", "om-permission", 4) {
		t.Fatal("different external user approved request")
	}
	if !broker.Resolve(context.Background(), sender.token, channelcontract.PermissionActionApproveOnce, "ou-owner", "oc-1", "om-permission", 4) {
		t.Fatal("bound user approval was rejected")
	}
	decision := <-decisionCh
	if !decision.Allowed || decision.Destination != "once" || decision.Decision != "allow" {
		t.Fatalf("decision = %+v", decision)
	}
	if broker.Resolve(context.Background(), sender.token, channelcontract.PermissionActionApproveOnce, "ou-owner", "oc-1", "om-permission", 4) {
		t.Fatal("consumed nonce was accepted twice")
	}
}
