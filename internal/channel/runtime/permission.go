package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

const permissionCallbackActionID = "tool_permission"

type PermissionCallbackRepository interface {
	CreateCallback(context.Context, mysqlstore.ChannelCallbackInput) (mysqlstore.ChannelCallback, error)
	ConsumeCallback(context.Context, uint64, uint64, uint64, string, string, string, []byte, uint64, string, int, time.Time) (mysqlstore.ChannelCallback, error)
}

type PermissionBrokerConfig struct {
	TenantID                  uint64
	AccountID                 uint64
	RuntimeFingerprint        string
	RuntimeFingerprintVersion int
	Repo                      PermissionCallbackRepository
	Sender                    channelcontract.PermissionCardSender
	Timeout                   time.Duration
	Now                       func() time.Time
	AdminOpenIDs              []string
}

type permissionWaiter struct {
	request   channelcontract.PermissionRequest
	decision  chan channelcontract.PermissionDecision
	ready     chan struct{}
	persisted bool
	messageID string
}

type PermissionBroker struct {
	cfg     PermissionBrokerConfig
	mu      sync.Mutex
	waiters map[string]*permissionWaiter
	admins  map[string]struct{}
}

func NewPermissionBroker(cfg PermissionBrokerConfig) *PermissionBroker {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	admins := make(map[string]struct{}, len(cfg.AdminOpenIDs))
	for _, id := range cfg.AdminOpenIDs {
		if id != "" {
			admins[id] = struct{}{}
		}
	}
	return &PermissionBroker{cfg: cfg, waiters: map[string]*permissionWaiter{}, admins: admins}
}

func (b *PermissionBroker) Request(ctx context.Context, request channelcontract.PermissionRequest) channelcontract.PermissionDecision {
	if b == nil || b.cfg.Repo == nil || b.cfg.Sender == nil || request.ConversationID == 0 || request.RunID == "" || request.UserID == 0 || request.ExternalUserID == "" || request.ExternalChatID == "" || request.ChatType != channelcontract.ChatTypeP2P {
		return deniedPermission("permission broker is not configured")
	}
	if _, allowed := b.admins[request.ExternalUserID]; !allowed {
		return deniedPermission("permission approval requires an administrator DM")
	}
	token := newID()
	waiter := &permissionWaiter{request: request, decision: make(chan channelcontract.PermissionDecision, 1), ready: make(chan struct{})}
	b.mu.Lock()
	b.waiters[token] = waiter
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiters, token)
		b.mu.Unlock()
	}()
	messageID, err := b.cfg.Sender.SendPermissionCard(ctx, request, token)
	if err != nil || messageID == "" {
		close(waiter.ready)
		return deniedPermission("permission card delivery failed")
	}
	hash := sha256.Sum256([]byte(token))
	expiresAt := b.cfg.Now().Add(b.cfg.Timeout)
	_, err = b.cfg.Repo.CreateCallback(ctx, mysqlstore.ChannelCallbackInput{TenantID: b.cfg.TenantID, AccountID: b.cfg.AccountID, ConversationID: request.ConversationID, RunID: request.RunID, PlatformMessageID: messageID, ActionID: permissionCallbackActionID, NonceHash: hash[:], RuntimeFingerprint: b.cfg.RuntimeFingerprint, RuntimeFingerprintVersion: b.cfg.RuntimeFingerprintVersion, AllowedUserID: request.UserID, Status: mysqlstore.ChannelCallbackStatusPending, ExpiresAt: expiresAt})
	waiter.messageID = messageID
	waiter.persisted = err == nil
	close(waiter.ready)
	if err != nil {
		return deniedPermission("permission callback persistence failed")
	}
	waitCtx, cancel := context.WithDeadline(ctx, expiresAt)
	defer cancel()
	select {
	case decision := <-waiter.decision:
		return decision
	case <-waitCtx.Done():
		return deniedPermission("permission request expired")
	}
}

func (b *PermissionBroker) Resolve(ctx context.Context, token, action, externalUserID, externalChatID, platformMessageID string, userID uint64) bool {
	if b == nil || token == "" || userID == 0 {
		return false
	}
	decision, ok := permissionDecisionForAction(action)
	if !ok {
		return false
	}
	b.mu.Lock()
	waiter := b.waiters[token]
	b.mu.Unlock()
	if waiter == nil || waiter.request.ExternalUserID != externalUserID || waiter.request.UserID != userID {
		return false
	}
	select {
	case <-waiter.ready:
	case <-ctx.Done():
		return false
	}
	if !waiter.persisted || waiter.request.ExternalChatID != externalChatID || waiter.messageID != platformMessageID {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	if _, err := b.cfg.Repo.ConsumeCallback(ctx, b.cfg.TenantID, b.cfg.AccountID, waiter.request.ConversationID, waiter.request.RunID, platformMessageID, permissionCallbackActionID, hash[:], userID, b.cfg.RuntimeFingerprint, b.cfg.RuntimeFingerprintVersion, b.cfg.Now()); err != nil {
		return false
	}
	select {
	case waiter.decision <- decision:
		return true
	default:
		return false
	}
}

func permissionDecisionForAction(action string) (channelcontract.PermissionDecision, bool) {
	switch action {
	case channelcontract.PermissionActionApproveOnce:
		return channelcontract.PermissionDecision{Allowed: true, Destination: "once", Decision: "allow", Reason: "approved in Feishu"}, true
	case channelcontract.PermissionActionApproveSession:
		return channelcontract.PermissionDecision{Allowed: true, Destination: "session", Decision: "allow", Reason: "approved for session in Feishu"}, true
	case channelcontract.PermissionActionDeny:
		return deniedPermission("denied in Feishu"), true
	default:
		return channelcontract.PermissionDecision{}, false
	}
}

func deniedPermission(reason string) channelcontract.PermissionDecision {
	if reason == "" {
		reason = errors.New("permission denied").Error()
	}
	return channelcontract.PermissionDecision{Allowed: false, Destination: "once", Decision: "deny", Reason: reason}
}
