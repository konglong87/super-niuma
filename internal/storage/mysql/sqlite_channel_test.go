package mysql

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	channelcontract "github.com/konglong87/super-niuma/internal/channel"
)

func TestSQLiteDesktopRepositorySupportsChannelRuntimePersistence(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "channel.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "desktop-channel", Name: "Desktop Channel"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "desktop-user")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := repo.EnsureChannelAccount(ctx, tenantID, ChannelProviderFeishu, "code", "cli_test", "code")
	if err != nil {
		t.Fatal(err)
	}
	if accountID == 0 {
		t.Fatal("channel account id is zero")
	}
	sameAccountID, err := repo.EnsureChannelAccount(ctx, tenantID, ChannelProviderFeishu, "code", "cli_test_updated", "code")
	if err != nil {
		t.Fatal(err)
	}
	if sameAccountID != accountID {
		t.Fatalf("reconciled account id = %d, want %d", sameAccountID, accountID)
	}
	var accountCount int64
	if err := repo.db.Table("channel_accounts").Where("tenant_id = ? AND provider = ? AND account_key = ?", tenantID, ChannelProviderFeishu, "code").Count(&accountCount).Error; err != nil {
		t.Fatal(err)
	}
	if accountCount != 1 {
		t.Fatalf("channel account count = %d, want 1", accountCount)
	}

	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, "code", "chat-1", "")
	if err != nil {
		t.Fatal(err)
	}
	event, created, err := repo.ClaimInboxEvent(ctx, ChannelInboxEventInput{
		TenantID: tenantID, AccountID: accountID, ProviderEventID: "event-1",
		ProviderMessageID: "message-1", ScopeHash: scope.Hash[:],
		PayloadSHA256: []byte("hash"), PayloadCiphertext: []byte("{}"),
		Authorized: true, Status: ChannelInboxStatusReceived, ReceivedAt: ptrTimeForSQLiteTest(time.Now().UTC()),
	})
	if err != nil || !created {
		t.Fatalf("claim inbox event = %+v, created=%v, err=%v", event, created, err)
	}
	if err := repo.MarkInboxQueued(ctx, tenantID, event.ID, 1, scope.Hash[:]); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkInboxProcessed(ctx, tenantID, event.ID); err != nil {
		t.Fatal(err)
	}

	conversation, err := repo.GetOrCreateChannelConversation(ctx, ChannelConversationInput{
		TenantID: tenantID, AccountID: accountID, ExternalChatID: "chat-1",
		ScopeKey: scope.Key, ScopeHash: scope.Hash[:], ExternalUserID: "user-1",
		ChatType: ChannelChatTypeP2P, SessionID: userID,
		RuntimeFingerprint:        channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, Status: ChannelConversationStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := repo.CreateChannelRun(ctx, ChannelRunInput{
		ID: "run-1", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, ScopeHash: scope.Hash[:],
		SessionID: conversation.SessionID, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AttachRunInputs(ctx, tenantID, run.ID, []ChannelRunInputEvent{{TenantID: tenantID, InboxEventID: event.ID, SequenceNo: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RequeueStrandedChannelRuns(ctx, tenantID, accountID); err != nil {
		t.Fatal(err)
	}
	if err := repo.FinishChannelRun(ctx, tenantID, accountID, run.ID, ChannelRunStatusCompleted, "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteDesktopRepositorySupportsChannelClaims(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "channel-claims.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "claims-tenant", Name: "Claims"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "claims-user")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := repo.EnsureChannelAccount(ctx, tenantID, ChannelProviderFeishu, "claims", "app", "credential")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, "claims", "claims-chat", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := repo.GetOrCreateChannelConversation(ctx, ChannelConversationInput{
		TenantID: tenantID, AccountID: accountID, ExternalChatID: "claims-chat",
		ScopeKey: scope.Key, ScopeHash: scope.Hash[:], ExternalUserID: "claims-user",
		ChatType: ChannelChatTypeP2P, SessionID: userID, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, Status: ChannelConversationStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(-time.Minute)
	leaseUntil := now.Add(time.Hour)
	event, created, err := repo.ClaimInboxEvent(ctx, ChannelInboxEventInput{
		TenantID: tenantID, AccountID: accountID, ConversationID: conversation.ID,
		ProviderEventID: "claims-event", ProviderMessageID: "claims-message",
		ScopeHash: scope.Hash[:], PayloadSHA256: []byte("hash"), PayloadCiphertext: []byte("{}"),
		Authorized: true, Status: ChannelInboxStatusQueued, AvailableAt: &now,
		ReceivedAt: &now,
	})
	if err != nil || !created {
		t.Fatalf("claim inbox event = %+v created=%v err=%v", event, created, err)
	}
	inbox, err := repo.ClaimDueInbox(ctx, tenantID, accountID, "worker-inbox", 10, leaseUntil)
	if err != nil || len(inbox) != 1 || inbox[0].Status != ChannelInboxStatusProcessing {
		t.Fatalf("claim due inbox = %+v err=%v", inbox, err)
	}
	if err := repo.MarkInboxQueued(ctx, tenantID, inbox[0].ID, conversation.ID, scope.Hash[:]); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimDueInbox(ctx, tenantID, accountID, "worker-inbox-reclaimed", 10, leaseUntil)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Status != ChannelInboxStatusProcessing {
		t.Fatalf("reclaim queued inbox = %+v err=%v", reclaimed, err)
	}
	if err := repo.MarkInboxQueued(ctx, tenantID, reclaimed[0].ID, conversation.ID, scope.Hash[:]); err != nil {
		t.Fatal(err)
	}
	localExpiredLease := time.Now().Add(-time.Minute)
	if _, err := repo.ClaimDueInbox(ctx, tenantID, accountID, "worker-local-time", 10, localExpiredLease); err != nil {
		t.Fatal(err)
	}
	localReclaimed, err := repo.ClaimDueInbox(ctx, tenantID, accountID, "worker-local-time-reclaimed", 10, time.Now().UTC().Add(time.Minute))
	if err != nil || len(localReclaimed) != 1 || localReclaimed[0].Status != ChannelInboxStatusProcessing {
		t.Fatalf("reclaim local-time lease = %+v err=%v", localReclaimed, err)
	}

	message, err := repo.CreateChannelMessage(ctx, ChannelMessageInput{
		TenantID: tenantID, AccountID: accountID, ConversationID: conversation.ID,
		RunID: "claims-run", Direction: ChannelMessageDirectionOutbound,
		Operation: ChannelMessageOperationCreate, IdempotencyKey: "claims-message",
		ContentJSON: `{}`, Status: ChannelMessageStatusPending,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateOutboxMessage(ctx, ChannelOutboxInput{
		TenantID: tenantID, AccountID: accountID, ConversationID: conversation.ID,
		RunID: "claims-run", MessageID: message.ID, Operation: ChannelMessageOperationCreate,
		SequenceNo: 1, ChunkCount: 1, IdempotencyKey: "claims-outbox", PayloadJSON: `{}`,
		Status: ChannelOutboxStatusPending, NextAttemptAt: &now,
	}); err != nil {
		t.Fatal(err)
	}
	outbox, err := repo.ClaimDueOutbox(ctx, tenantID, accountID, "worker-outbox", 10, leaseUntil)
	if err != nil || len(outbox) != 1 || outbox[0].Status != ChannelOutboxStatusSending {
		t.Fatalf("claim due outbox = %+v err=%v", outbox, err)
	}

	if err := repo.UpsertChannelReactionDesired(ctx, ChannelReactionInput{
		TenantID: tenantID, AccountID: accountID, ConversationID: conversation.ID,
		ProviderMessageID: "claims-message", DesiredEmoji: "Typing",
	}); err != nil {
		t.Fatal(err)
	}
	reactions, err := repo.ClaimDueChannelReactions(ctx, tenantID, accountID, "worker-reaction", 10, leaseUntil)
	if err != nil || len(reactions) != 1 || reactions[0].Status != ChannelReactionStatusReconciling {
		t.Fatalf("claim due reactions = %+v err=%v", reactions, err)
	}
	if err := repo.MarkChannelReactionDeleted(ctx, tenantID, accountID, reactions[0].ID, "worker-reaction"); err != nil {
		t.Fatalf("mark reaction deleted: %v", err)
	}

	run, err := repo.CreateChannelRun(ctx, ChannelRunInput{
		ID: "claims-run", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, ScopeHash: scope.Hash[:], SessionID: userID,
		RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest, RuntimeFingerprintVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimedRun, err := repo.ClaimConversationRun(ctx, tenantID, accountID, conversation.ID, "worker-run")
	if err != nil || claimedRun.ID != run.ID || claimedRun.Status != ChannelRunStatusRunning {
		t.Fatalf("claim conversation run = %+v err=%v", claimedRun, err)
	}
}

func TestSQLiteDesktopRepositoryExpiresPendingInteractionsWithoutLocking(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "channel-interactions.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "interaction-tenant", Name: "Interactions"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "interaction-user")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := repo.EnsureChannelAccount(ctx, tenantID, ChannelProviderFeishu, "interaction-account", "app", "credential")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, "interaction-account", "interaction-chat", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := repo.GetOrCreateChannelConversation(ctx, ChannelConversationInput{
		TenantID: tenantID, AccountID: accountID, ExternalChatID: "interaction-chat",
		ScopeKey: scope.Key, ScopeHash: scope.Hash[:], ExternalUserID: "interaction-user",
		ChatType: ChannelChatTypeP2P, SessionID: userID, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, Status: ChannelConversationStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := repo.CreateChannelInteraction(ctx, ChannelInteractionInput{
		ID: "expired-interaction", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, RunID: "interaction-run", SessionID: userID,
		Kind: "question", Status: ChannelInteractionStatusPending,
		ExternalChatID: "interaction-chat", ExternalUserID: "interaction-user",
		ScopeHash: scope.Hash[:], AllowedUserID: userID,
		QuestionCiphertext: []byte("question"), ResumeCheckpointCiphertext: []byte("resume"),
		NonceHash: []byte("nonce"), RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, ExpiresAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	expired, err := repo.ExpirePendingChannelInteractions(ctx, tenantID, accountID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].ID != "expired-interaction" || expired[0].Status != ChannelInteractionStatusExpired {
		t.Fatalf("expired interactions = %+v", expired)
	}
}

func TestSQLiteDesktopRepositoryDoesNotBlockOnExpiredWaitingInputRun(t *testing.T) {
	ctx := context.Background()
	repo, err := OpenSQLiteGormRepository(ctx, filepath.Join(t.TempDir(), "expired-waiting-run.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "expired-waiting-tenant", Name: "Expired waiting"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "expired-waiting-user")
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := repo.EnsureChannelAccount(ctx, tenantID, ChannelProviderFeishu, "expired-waiting-account", "app", "credential")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := channelcontract.NewScope(channelcontract.ProviderFeishu, "expired-waiting-account", "expired-waiting-chat", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := repo.GetOrCreateChannelConversation(ctx, ChannelConversationInput{
		TenantID: tenantID, AccountID: accountID, ExternalChatID: "expired-waiting-chat",
		ScopeKey: scope.Key, ScopeHash: scope.Hash[:], ExternalUserID: "expired-waiting-user",
		ChatType: ChannelChatTypeP2P, SessionID: userID, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, Status: ChannelConversationStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateChannelRun(ctx, ChannelRunInput{
		ID: "expired-waiting-run", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, ScopeHash: scope.Hash[:], SessionID: userID,
		Status: ChannelRunStatusWaitingInput, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateChannelInteraction(ctx, ChannelInteractionInput{
		ID: "expired-waiting-interaction", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, RunID: "expired-waiting-run", SessionID: userID,
		Kind: "question", Status: ChannelInteractionStatusExpired,
		ExternalChatID: "expired-waiting-chat", ExternalUserID: "expired-waiting-user",
		ScopeHash: scope.Hash[:], AllowedUserID: userID,
		QuestionCiphertext: []byte("question"), ResumeCheckpointCiphertext: []byte("resume"),
		NonceHash: []byte("nonce"), RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
		RuntimeFingerprintVersion: 1, ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateChannelRun(ctx, ChannelRunInput{
		ID: "new-queued-run", TenantID: tenantID, AccountID: accountID,
		ConversationID: conversation.ID, ScopeHash: scope.Hash[:], SessionID: userID,
		Status: ChannelRunStatusQueued, RuntimeFingerprint: channelruntimeFingerprintForSQLiteTest,
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := repo.ClaimConversationRun(ctx, tenantID, accountID, conversation.ID, "worker-expired")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != "new-queued-run" || claimed.Status != ChannelRunStatusRunning {
		t.Fatalf("claimed run = %+v", claimed)
	}
}

const channelruntimeFingerprintForSQLiteTest = "sqlite-test-runtime"

func ptrTimeForSQLiteTest(value time.Time) *time.Time { return &value }
