package mysql

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
)

func TestChannelAccountLifecycleIsTenantScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `channel_accounts`").WillReturnResult(sqlmock.NewResult(17, 1))
	created, err := repo.CreateChannelAccount(testContext(), ChannelAccountInput{TenantID: 9, Provider: ChannelProviderFeishu, AccountKey: "primary", AppID: "app", CredentialRef: "vault/ref", Mode: ChannelAccountModeStream, PolicyJSON: `{}`})
	if err != nil || created.ID != 17 || created.TenantID != 9 {
		t.Fatalf("created = %+v, err = %v", created, err)
	}

	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, tenant_id, provider, account_key, app_id, credential_ref, mode, enabled, policy_json, status, last_connected_at, last_error_code, last_error_message, created_at, updated_at, archived_at FROM `channel_accounts` WHERE tenant_id = ? AND id = ? AND archived_at IS NULL LIMIT ?")).
		WithArgs(uint64(9), uint64(17), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "provider", "account_key", "app_id", "credential_ref", "mode", "enabled", "policy_json", "status", "last_connected_at", "last_error_code", "last_error_message", "created_at", "updated_at", "archived_at"}).AddRow(17, 9, ChannelProviderFeishu, "primary", "app", "vault/ref", ChannelAccountModeStream, true, `{}`, ChannelAccountStatusReady, now, nil, nil, now, now, nil))
	got, err := repo.GetChannelAccount(testContext(), 9, 17)
	if err != nil || got.ID != 17 || got.Status != ChannelAccountStatusReady {
		t.Fatalf("got = %+v, err = %v", got, err)
	}

	mock.ExpectExec("UPDATE `channel_accounts` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateChannelAccount(testContext(), ChannelAccountInput{ID: 17, TenantID: 9, Provider: ChannelProviderFeishu, AccountKey: "primary", AppID: "app", Mode: ChannelAccountModeStream, PolicyJSON: `{}`, Status: ChannelAccountStatusReady}); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestHasUnsentPriorChannelOutboxIsRunAndTenantScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `channel_outbox` WHERE tenant_id = \\? AND account_id = \\? AND conversation_id = \\? AND run_id = \\? AND sequence_no < \\? AND status NOT IN \\(\\?, \\?\\)").
		WithArgs(uint64(9), uint64(3), uint64(17), "run-pages", uint(2), ChannelOutboxStatusSent, ChannelOutboxStatusDead).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	blocked, err := repo.HasUnsentPriorChannelOutbox(testContext(), 9, 3, 17, "run-pages", 2)
	if err != nil || !blocked {
		t.Fatalf("blocked=%v err=%v", blocked, err)
	}
	if _, err := repo.HasUnsentPriorChannelOutbox(testContext(), 9, 3, 17, "run-pages", 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("sequence one error = %v", err)
	}
	assertExpectations(t, mock)
}

func TestClaimInboxEventIsIdempotentWithinAccount(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `channel_inbox_events` WHERE tenant_id = \\? AND account_id = \\? AND provider_event_id = \\? LIMIT \\?").
		WithArgs(uint64(3), uint64(7), "evt-1", 1).
		WillReturnError(sqlmock.ErrCancelled)
	// A database error is not treated as a duplicate: callers must retry the
	// durable claim rather than acknowledge an event that was not persisted.
	_, claimed, err := repo.ClaimInboxEvent(testContext(), ChannelInboxEventInput{TenantID: 3, AccountID: 7, ProviderEventID: "evt-1"})
	if claimed || err == nil {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	assertExpectations(t, mock)
}

func TestMarkInboxProcessingCanReclaimExpiredProcessingLease(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `channel_inbox_events` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.MarkInboxProcessing(testContext(), 3, 11, "worker-2", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestGetOrCreateConversationRejectsFingerprintChange(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	scopeHash := []byte("scope-hash")
	mock.ExpectQuery("SELECT .* FROM `channel_conversations` WHERE tenant_id = \\? AND account_id = \\? AND scope_hash = \\? LIMIT \\?").
		WithArgs(uint64(1), uint64(2), scopeHash, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "external_chat_id", "external_thread_id", "thread_id_source", "scope_key", "scope_hash", "external_user_id", "chat_type", "session_id", "runtime_fingerprint", "runtime_fingerprint_version", "workspace_realpath", "permission_mode", "status", "last_inbound_at", "last_outbound_at", "metadata_json", "created_at", "updated_at", "archived_at"}).AddRow(5, 1, 2, "chat", "", ChannelThreadIDSourceNone, "scope", scopeHash, nil, ChannelChatTypeP2P, nil, "old", 1, nil, "ask", ChannelConversationStatusActive, nil, nil, nil, time.Now(), time.Now(), nil))
	_, err := repo.GetOrCreateChannelConversation(testContext(), ChannelConversationInput{TenantID: 1, AccountID: 2, ExternalChatID: "chat", ScopeHash: scopeHash, RuntimeFingerprint: "new", RuntimeFingerprintVersion: 1})
	if err == nil || !errors.Is(err, ErrChannelRuntimeFingerprintMismatch) {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestGetChannelConversationByScopeRequiresTenantAndAccountOwnership(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	scopeHash := []byte("scope-hash")
	mock.ExpectQuery("SELECT .* FROM `channel_conversations` WHERE tenant_id = \\? AND account_id = \\? AND scope_hash = \\? AND archived_at IS NULL LIMIT \\?").
		WithArgs(uint64(3), uint64(7), scopeHash, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "external_chat_id", "external_thread_id", "thread_id_source", "scope_key", "scope_hash", "external_user_id", "chat_type", "session_id", "runtime_fingerprint", "runtime_fingerprint_version", "workspace_realpath", "permission_mode", "status", "last_inbound_at", "last_outbound_at", "metadata_json", "created_at", "updated_at", "archived_at"}).AddRow(9, 3, 7, "oc", "", ChannelThreadIDSourceNone, "scope", scopeHash, "ou", ChannelChatTypeP2P, 11, "fp", 1, "/workspace", "deny", ChannelConversationStatusActive, nil, nil, nil, time.Now(), time.Now(), nil))
	got, err := repo.GetChannelConversationByScope(testContext(), 3, 7, scopeHash)
	if err != nil || got.ID != 9 || got.SessionID != 11 || got.PermissionMode != "deny" {
		t.Fatalf("conversation = %+v, err = %v", got, err)
	}
	assertExpectations(t, mock)
}

func TestUpdateChannelConversationControlsRequiresTenantAndAccountOwnership(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `channel_conversations` SET .* WHERE tenant_id = \\? AND account_id = \\? AND id = \\?").
		WithArgs("allow", "/workspace", uint64(3), uint64(7), uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := repo.UpdateChannelConversationControls(testContext(), ChannelConversationControlsInput{TenantID: 3, AccountID: 7, ConversationID: 9, WorkspaceRealpath: "/workspace", PermissionMode: "allow"})
	if err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestRequestCancelActiveChannelRunIsConversationScoped(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE channel_runs SET status = \\?, cancel_requested_at = CURRENT_TIMESTAMP\\(6\\) WHERE tenant_id = \\? AND account_id = \\? AND conversation_id = \\? AND status IN \\(\\?, \\?, \\?\\) ORDER BY CASE WHEN status = \\? THEN 0 WHEN status = \\? THEN 1 ELSE 2 END, created_at ASC, id ASC LIMIT 1").
		WithArgs(ChannelRunStatusCancelRequested, uint64(3), uint64(7), uint64(9), ChannelRunStatusRunning, ChannelRunStatusQueued, ChannelRunStatusWaitingInput, ChannelRunStatusRunning, ChannelRunStatusWaitingInput).
		WillReturnResult(sqlmock.NewResult(0, 1))
	changed, err := repo.RequestCancelActiveChannelRun(testContext(), 3, 7, 9)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	assertExpectations(t, mock)
}

func TestCreateChannelInteractionPersistsEncryptedPayloadAndScope(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectExec("INSERT INTO `channel_interactions`").WillReturnResult(sqlmock.NewResult(0, 1))
	created, err := repo.CreateChannelInteraction(testContext(), ChannelInteractionInput{
		ID:                         "interaction-1",
		TenantID:                   3,
		AccountID:                  7,
		ConversationID:             9,
		RunID:                      "run-1",
		SessionID:                  11,
		Kind:                       ChannelInteractionKindUserQuestion,
		ExternalChatID:             "oc-1",
		ScopeHash:                  []byte("scope"),
		AllowedUserID:              4,
		QuestionCiphertext:         []byte("question"),
		ResumeCheckpointCiphertext: []byte("checkpoint"),
		NonceHash:                  []byte("nonce"),
		RuntimeFingerprint:         "fp",
		RuntimeFingerprintVersion:  1,
		Status:                     ChannelInteractionStatusPending,
		ExpiresAt:                  now.Add(time.Minute),
	})
	if err != nil || created.ID != "interaction-1" || created.Status != ChannelInteractionStatusPending {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	assertExpectations(t, mock)
}

func TestAnswerChannelInteractionIsCompareAndSet(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectExec("UPDATE `channel_interactions` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.AnswerChannelInteraction(testContext(), 3, 7, "interaction-1", []byte("nonce"), 4, "fp", 1, []byte("answer"), now); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE `channel_interactions` SET").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.AnswerChannelInteraction(testContext(), 3, 7, "interaction-1", []byte("nonce"), 4, "fp", 1, []byte("answer"), now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second answer err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestRotateChannelConversationSessionIsAtomicAndEventIdempotent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	scopeHash := []byte("scope-hash")
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `channel_conversations` WHERE tenant_id = \\? AND account_id = \\? AND id = \\? AND archived_at IS NULL LIMIT \\? FOR UPDATE").
		WithArgs(uint64(3), uint64(7), uint64(9), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "external_chat_id", "external_thread_id", "thread_id_source", "scope_key", "scope_hash", "external_user_id", "chat_type", "session_id", "runtime_fingerprint", "runtime_fingerprint_version", "workspace_realpath", "permission_mode", "status", "last_inbound_at", "last_outbound_at", "metadata_json", "created_at", "updated_at", "archived_at"}).AddRow(9, 3, 7, "oc", "", ChannelThreadIDSourceNone, "scope", scopeHash, "ou", ChannelChatTypeP2P, 11, "fp", 1, "/workspace", "ask", ChannelConversationStatusActive, nil, nil, nil, now, now, nil))
	mock.ExpectExec(insertSessionSQL).WillReturnResult(sqlmock.NewResult(55, 1))
	mock.ExpectExec("UPDATE `channel_conversations` SET .* WHERE tenant_id = \\? AND account_id = \\? AND id = \\?").
		WithArgs(uint64(55), uint64(3), uint64(7), uint64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	got, err := repo.RotateChannelConversationSession(testContext(), ChannelSessionRotationInput{TenantID: 3, AccountID: 7, ConversationID: 9, UserID: 4, InboxEventID: 101, Model: "model"})
	if err != nil || got != 55 {
		t.Fatalf("session=%d err=%v", got, err)
	}
	assertExpectations(t, mock)
}

func TestUpsertChannelReactionDesiredIsAccountMessageIdempotent(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("INSERT INTO `channel_reactions` .*ON DUPLICATE KEY UPDATE").
		WillReturnResult(sqlmock.NewResult(17, 1))
	err := repo.UpsertChannelReactionDesired(testContext(), ChannelReactionInput{TenantID: 3, AccountID: 7, ConversationID: 9, ProviderMessageID: "om-1", DesiredEmoji: "Typing"})
	if err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestMarkChannelReactionAppliedRequiresTenantAndAccountOwnership(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `channel_reactions` SET .* WHERE tenant_id = \\? AND account_id = \\? AND id = \\?").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.MarkChannelReactionApplied(testContext(), 3, 7, 17, "worker", "DONE", "reaction-1")
	if err != ErrChannelReactionSuperseded {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMarkOutboxSentRequiresTenantAndAccountOwnership(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectExec("UPDATE `channel_outbox` SET").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.MarkOutboxSent(testContext(), 11, 22, 33); err != ErrNotFound {
		t.Fatalf("err = %v", err)
	}
	assertExpectations(t, mock)
}

func TestGetChannelMessageForDeliveryRequiresTenantAndAccountOwnership(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery("SELECT .* FROM `channel_messages` WHERE tenant_id = \\? AND account_id = \\? AND id = \\? LIMIT \\?").
		WithArgs(uint64(7), uint64(3), uint64(41), 1).
		WillReturnRows(channelImageMessageRows().AddRow(41, 7, 3, 9, "run-1", ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, "om-final", nil, "run:run-1:final", `{}`, 1, nil, ChannelMessageStatusSent, time.Now().UTC(), time.Now().UTC()))
	message, err := repo.GetChannelMessageForDelivery(testContext(), 7, 3, 41)
	if err != nil || message.ExternalMessageID != "om-final" {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	assertExpectations(t, mock)
}

func TestResolveChannelImageCompletionWaitsForSentOriginFinalCard(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := channelImageCompletionLookup()
	expectChannelImageCompletionCoreReads(mock, input, false)
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*LIMIT \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "run_id", "idempotency_key", "status", "external_message_id"}).
			AddRow(41, 7, 3, 9, "run-1", "run:run-1:final", ChannelMessageStatusPending, nil))

	_, err := repo.ResolveChannelImageCompletion(testContext(), input)
	if !errors.Is(err, ErrChannelImageCompletionNotReady) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestResolveChannelImageCompletionAcceptsStrictV2Origin(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := channelImageCompletionLookupV2()
	expectChannelImageCompletionV2ReadyReads(mock, input, false, "")

	material, err := repo.ResolveChannelImageCompletion(testContext(), input)
	if err != nil || material.Origin != input.Origin || material.UserID != 11 || material.SessionID != 13 || material.ExternalThreadID != "thread-1" {
		t.Fatalf("material=%+v err=%v", material, err)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelImageCompletionAcceptsStrictV2Origin(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInputV2()
	mock.ExpectBegin()
	expectChannelImageCompletionV2ReadyReads(mock, input.ResolveChannelImageCompletionInput, true, "")
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageMessageRows())
	mock.ExpectExec("INSERT INTO `channel_messages`").WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(61, 1))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestResolveChannelImageCompletionRejectsMalformedV2Origin(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := channelImageCompletionLookupV2()
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox` .*LIMIT \\?").
		WillReturnRows(imageCompletionOutboxRows().AddRow(input.EventID, 7, "gen-1", imagegen.CompletionEventImageTerminal, imagegen.OriginTypeChannel, strings.TrimSuffix(channelImageV2OriginJSON(), "}")+`,"prompt":"secret"}`, "channel:source", imagegen.CompletionDeliveryStatusSending, 1, nil, "dispatcher-a", time.Now().UTC().Add(time.Minute), nil, nil, time.Now().UTC(), nil))

	_, err := repo.ResolveChannelImageCompletion(testContext(), input)
	if !errors.Is(err, ErrChannelImageCompletionOwnership) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelImageCompletionRejectsMismatchedV2LockedRows(t *testing.T) {
	for _, mismatch := range []string{"user", "thread", "reply"} {
		t.Run(mismatch, func(t *testing.T) {
			repo, mock, closeDB := newMockGormRepository(t)
			defer closeDB()
			input := materializeChannelImageCompletionInputV2()
			mock.ExpectBegin()
			expectChannelImageCompletionV2ReadyReads(mock, input.ResolveChannelImageCompletionInput, true, mismatch)
			mock.ExpectRollback()
			err := repo.MaterializeChannelImageCompletion(testContext(), input)
			if !errors.Is(err, ErrChannelImageCompletionOwnership) {
				t.Fatalf("err=%v", err)
			}
			assertExpectations(t, mock)
		})
	}
}

func TestMaterializeChannelImageCompletionRejectsSessionOwnedByAnotherUser(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInput()
	mock.ExpectBegin()
	expectChannelImageCompletionEventAndGeneration(mock, input.ResolveChannelImageCompletionInput, true, nil, imagegen.GenerationStatusCompleted)
	mock.ExpectQuery("SELECT .* FROM `tenant_sessions` WHERE tenant_id = \\? AND id = \\? AND user_id = \\? LIMIT \\? FOR UPDATE").
		WithArgs(uint64(7), uint64(13), uint64(11), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id"}))
	mock.ExpectRollback()
	err := repo.MaterializeChannelImageCompletion(testContext(), input)
	if !errors.Is(err, ErrChannelImageCompletionOwnership) {
		t.Fatalf("err=%v", err)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelImageCompletionRollsBackAndReplaysAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInput()

	mock.ExpectBegin()
	expectChannelImageCompletionReadyReads(mock, input.ResolveChannelImageCompletionInput, true)
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageMessageRows())
	mock.ExpectExec("INSERT INTO `channel_messages`").WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnError(errors.New("outbox insert failed"))
	mock.ExpectRollback()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err == nil {
		t.Fatal("materialization unexpectedly succeeded")
	}

	// Replay observes no partial message/outbox rows from the rolled-back
	// transaction and can commit the complete durable effect once.
	mock.ExpectBegin()
	expectChannelImageCompletionReadyReads(mock, input.ResolveChannelImageCompletionInput, true)
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageMessageRows())
	mock.ExpectExec("INSERT INTO `channel_messages`").WillReturnResult(sqlmock.NewResult(52, 1))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(61, 1))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelImageCompletionLoadsDeterministicRowsWithoutDuplicates(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInput()
	mock.ExpectBegin()
	expectChannelImageCompletionReadyReads(mock, input.ResolveChannelImageCompletionInput, true)
	persistedJSON := `{"idempotency_key": "generation:gen-1:channel:image", "kind": "image"}`
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*idempotency_key.*LIMIT \\? FOR UPDATE").
		WillReturnRows(channelImageMessageRows().AddRow(51, 7, 3, 9, "run-1", ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, nil, nil, input.ImageIdempotencyKey, persistedJSON, 1, nil, ChannelMessageStatusPending, time.Now().UTC(), nil))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").
		WillReturnRows(channelImageOutboxRows().AddRow(61, 7, 3, 9, "run-1", 51, ChannelMessageOperationCreate, 1, 1, input.ImageIdempotencyKey, persistedJSON, nil, ChannelOutboxStatusPending, 0, time.Now().UTC(), nil, nil, nil, nil, nil, time.Now().UTC(), nil))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestResolveChannelImageCompletionCalculatesTerminalBatchProgress(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := channelImageCompletionLookup()
	expectChannelImageCompletionReadyReadsWithBatch(mock, input, false, "batch-1")
	mock.ExpectQuery("SELECT id, status FROM `image_generations` .*batch_id.*ORDER BY id ASC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).
			AddRow(19, imagegen.GenerationStatusCompleted).
			AddRow(20, imagegen.GenerationStatusFailed).
			AddRow(21, imagegen.GenerationStatusDead).
			AddRow(22, imagegen.GenerationStatusCompleted))
	material, err := repo.ResolveChannelImageCompletion(testContext(), input)
	if err != nil {
		t.Fatal(err)
	}
	want := (ImageGenerationBatchProgress{BatchID: "batch-1", Total: 4, Completed: 2, Failed: 2, Terminal: true})
	if material.Batch != want {
		t.Fatalf("batch=%+v want=%+v", material.Batch, want)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelImageCompletionCreatesTerminalBatchUpdateInSameTransaction(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInput()
	input.ExpectedBatch = ImageGenerationBatchProgress{BatchID: "batch-1", Total: 2, Completed: 1, Failed: 1, Terminal: true}
	input.SummaryIdempotencyKey = "run:run-1:session:13:batch:batch-1:channel:summary"
	input.SummaryPayloadJSON = `{"kind":"card","idempotency_key":"run:run-1:session:13:batch:batch-1:channel:summary"}`
	mock.ExpectBegin()
	expectChannelImageCompletionReadyReadsWithBatch(mock, input.ResolveChannelImageCompletionInput, true, "batch-1")
	mock.ExpectQuery("SELECT id, status FROM `image_generations` .*batch_id.*ORDER BY id ASC FOR UPDATE").
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(19, imagegen.GenerationStatusCompleted).AddRow(20, imagegen.GenerationStatusFailed))
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageMessageRows())
	mock.ExpectExec("INSERT INTO `channel_messages`").WillReturnResult(sqlmock.NewResult(51, 1))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(61, 1))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(62, 1))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func TestMaterializeChannelFailureCompletionRollsBackAndReplaysSummaryAtomically(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	input := materializeChannelImageCompletionInput()
	input.GenerationStatus = imagegen.GenerationStatusFailed
	input.ImageIdempotencyKey = ""
	input.ImagePayloadJSON = ""
	input.ExpectedBatch = ImageGenerationBatchProgress{BatchID: "batch-1", Total: 2, Completed: 1, Failed: 1, Terminal: true}
	input.SummaryIdempotencyKey = "run:run-1:session:13:batch:batch-1:channel:summary"
	input.SummaryPayloadJSON = `{"kind":"card","idempotency_key":"run:run-1:session:13:batch:batch-1:channel:summary"}`

	mock.ExpectBegin()
	expectChannelImageCompletionReadyReadsWithBatchAndStatus(mock, input.ResolveChannelImageCompletionInput, true, "batch-1", imagegen.GenerationStatusFailed)
	mock.ExpectQuery("SELECT id, status FROM `image_generations` .*batch_id.*ORDER BY id ASC FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(19, imagegen.GenerationStatusFailed).AddRow(20, imagegen.GenerationStatusCompleted))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnError(errors.New("summary insert failed"))
	mock.ExpectRollback()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err == nil {
		t.Fatal("failure-event materialization unexpectedly succeeded")
	}

	mock.ExpectBegin()
	expectChannelImageCompletionReadyReadsWithBatchAndStatus(mock, input.ResolveChannelImageCompletionInput, true, "batch-1", imagegen.GenerationStatusFailed)
	mock.ExpectQuery("SELECT id, status FROM `image_generations` .*batch_id.*ORDER BY id ASC FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(19, imagegen.GenerationStatusFailed).AddRow(20, imagegen.GenerationStatusCompleted))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*idempotency_key.*LIMIT \\? FOR UPDATE").WillReturnRows(channelImageOutboxRows())
	mock.ExpectExec("INSERT INTO `channel_outbox`").WillReturnResult(sqlmock.NewResult(62, 1))
	mock.ExpectExec("UPDATE `image_completion_outbox` SET .* WHERE tenant_id = \\? AND id = \\? AND generation_id = \\? AND status = \\? AND lease_owner = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.MaterializeChannelImageCompletion(testContext(), input); err != nil {
		t.Fatal(err)
	}
	assertExpectations(t, mock)
}

func channelImageCompletionLookup() ResolveChannelImageCompletionInput {
	return ResolveChannelImageCompletionInput{EventID: 5, TenantID: 7, GenerationID: "gen-1", WorkerID: "dispatcher-a", Origin: ChannelImageCompletionOrigin{Version: 1, AccountID: 3, ConversationID: 9, RunID: "run-1", ReplyMessageID: "om-source"}}
}

func channelImageCompletionLookupV2() ResolveChannelImageCompletionInput {
	return ResolveChannelImageCompletionInput{EventID: 5, TenantID: 7, GenerationID: "gen-1", WorkerID: "dispatcher-a", Origin: ChannelImageCompletionOrigin{Version: 2, TenantID: 7, AccountID: 3, ConversationID: 9, RunID: "run-1", SessionID: 13, UserID: 11, ReplyMessageID: "om-source", ThreadID: "thread-1"}}
}

func materializeChannelImageCompletionInput() MaterializeChannelImageCompletionInput {
	lookup := channelImageCompletionLookup()
	return MaterializeChannelImageCompletionInput{ResolveChannelImageCompletionInput: lookup, GenerationStatus: imagegen.GenerationStatusCompleted, SessionID: 13, ImageIdempotencyKey: "generation:gen-1:channel:image", ImagePayloadJSON: `{"kind":"image","idempotency_key":"generation:gen-1:channel:image"}`}
}

func materializeChannelImageCompletionInputV2() MaterializeChannelImageCompletionInput {
	input := materializeChannelImageCompletionInput()
	input.ResolveChannelImageCompletionInput = channelImageCompletionLookupV2()
	return input
}

func channelImageV2OriginJSON() string {
	return `{"version":2,"tenant_id":7,"account_id":3,"conversation_id":9,"run_id":"run-1","session_id":13,"user_id":11,"reply_message_id":"om-source","thread_id":"thread-1"}`
}

func expectChannelImageCompletionV2ReadyReads(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, mismatch string) {
	lockSuffix := ""
	if lock {
		lockSuffix = " FOR UPDATE"
	}
	originJSON := channelImageV2OriginJSON()
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(imageCompletionOutboxRows().AddRow(input.EventID, 7, "gen-1", imagegen.CompletionEventImageTerminal, imagegen.OriginTypeChannel, originJSON, "channel:source", imagegen.CompletionDeliveryStatusSending, 1, nil, "dispatcher-a", time.Now().UTC().Add(time.Minute), nil, nil, time.Now().UTC(), nil))
	userID := uint64(11)
	if mismatch == "user" {
		userID = 12
	}
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "asset_id", "status", "origin_type", "origin_ref_json", "batch_id"}).AddRow(19, "gen-1", 7, userID, 13, "asset-1", imagegen.GenerationStatusCompleted, imagegen.OriginTypeChannel, originJSON, nil))
	if mismatch == "user" {
		return
	}
	mock.ExpectQuery("SELECT .* FROM `tenant_sessions` .*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id"}).AddRow(13, 7, 11))
	mock.ExpectQuery("SELECT .* FROM `channel_accounts` .*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "provider", "account_key", "archived_at"}).AddRow(3, 7, ChannelProviderFeishu, "art-bot", nil))
	threadID := "thread-1"
	if mismatch == "thread" {
		threadID = "thread-other"
	}
	mock.ExpectQuery("SELECT .* FROM `channel_conversations` .*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "external_chat_id", "external_thread_id", "session_id", "status", "archived_at"}).AddRow(9, 7, 3, "oc-1", threadID, 13, ChannelConversationStatusActive, nil))
	if mismatch == "thread" {
		return
	}
	mock.ExpectQuery("SELECT .* FROM `channel_runs` .*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "session_id", "status", "finished_at"}).AddRow("run-1", 7, 3, 9, 13, ChannelRunStatusCompleted, time.Now().UTC()))
	replyID := "om-source"
	if mismatch == "reply" {
		replyID = "om-other"
	}
	mock.ExpectQuery("SELECT .* FROM channel_inbox_events AS inbox .*channel_run_inputs AS run_input.*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "provider_message_id"}).AddRow(1, 7, 3, 9, replyID))
	if mismatch == "reply" {
		return
	}
	mock.ExpectQuery("SELECT .* FROM `media_assets` .*LIMIT \\?" + lockSuffix).WillReturnRows(sqlmock.NewRows([]string{"asset_id", "tenant_id", "user_id", "session_id", "kind", "media_type", "name", "size_bytes", "sha256", "state"}).AddRow("asset-1", 7, 11, 13, media.KindImage, "image/png", "result.png", 123, "abc", media.StateReady))
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*LIMIT \\?" + lockSuffix).WillReturnRows(channelImageMessageRows().AddRow(41, 7, 3, 9, "run-1", ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, "om-final", nil, "run:run-1:final", `{}`, 1, nil, ChannelMessageStatusSent, time.Now().UTC(), time.Now().UTC()))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*LIMIT \\?" + lockSuffix).WillReturnRows(channelImageOutboxRows().AddRow(42, 7, 3, 9, "run-1", 41, ChannelMessageOperationCreate, 1, 1, "run:run-1:final", `{}`, nil, ChannelOutboxStatusSent, 1, time.Now().UTC(), nil, nil, nil, nil, nil, time.Now().UTC(), time.Now().UTC()))
}

func expectChannelImageCompletionCoreReads(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool) {
	expectChannelImageCompletionCoreReadsWithBatch(mock, input, lock, nil)
}

func expectChannelImageCompletionCoreReadsWithBatch(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, batchID any) {
	expectChannelImageCompletionCoreReadsWithBatchAndStatus(mock, input, lock, batchID, imagegen.GenerationStatusCompleted)
}

func expectChannelImageCompletionCoreReadsWithBatchAndStatus(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, batchID any, status string) {
	expectChannelImageCompletionEventAndGeneration(mock, input, lock, batchID, status)
	lockSuffix := ""
	if lock {
		lockSuffix = " FOR UPDATE"
	}
	mock.ExpectQuery("SELECT .* FROM `tenant_sessions` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "user_id"}).AddRow(13, 7, 11))
	mock.ExpectQuery("SELECT .* FROM `channel_accounts` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "provider", "account_key", "archived_at"}).AddRow(3, 7, ChannelProviderFeishu, "art-bot", nil))
	mock.ExpectQuery("SELECT .* FROM `channel_conversations` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "external_chat_id", "external_thread_id", "session_id", "status", "archived_at"}).AddRow(9, 7, 3, "oc-1", "thread-1", 13, ChannelConversationStatusActive, nil))
	mock.ExpectQuery("SELECT .* FROM `channel_runs` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "session_id", "status", "finished_at"}).AddRow("run-1", 7, 3, 9, 13, ChannelRunStatusCompleted, time.Now().UTC()))
	if status == imagegen.GenerationStatusCompleted {
		mock.ExpectQuery("SELECT .* FROM `media_assets` .*LIMIT \\?" + lockSuffix).
			WillReturnRows(sqlmock.NewRows([]string{"asset_id", "tenant_id", "user_id", "session_id", "kind", "media_type", "name", "size_bytes", "sha256", "state"}).AddRow("asset-1", 7, 11, 13, media.KindImage, "image/png", "result.png", 123, "abc", media.StateReady))
	}
}

func expectChannelImageCompletionEventAndGeneration(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, batchID any, status string) {
	lockSuffix := ""
	if lock {
		lockSuffix = " FOR UPDATE"
	}
	originJSON := `{"version":1,"account_id":3,"conversation_id":9,"run_id":"run-1","reply_message_id":"om-source"}`
	mock.ExpectQuery("SELECT .* FROM `image_completion_outbox` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(imageCompletionOutboxRows().AddRow(input.EventID, 7, "gen-1", imagegen.CompletionEventImageCompleted, imagegen.OriginTypeChannel, originJSON, "channel:source", imagegen.CompletionDeliveryStatusSending, 1, nil, "dispatcher-a", time.Now().UTC().Add(time.Minute), nil, nil, time.Now().UTC(), nil))
	mock.ExpectQuery("SELECT .* FROM `image_generations` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(sqlmock.NewRows([]string{"id", "generation_id", "tenant_id", "user_id", "session_id", "asset_id", "status", "origin_type", "origin_ref_json", "batch_id"}).AddRow(19, "gen-1", 7, 11, 13, "asset-1", status, imagegen.OriginTypeChannel, originJSON, batchID))
}

func expectChannelImageCompletionReadyReads(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool) {
	expectChannelImageCompletionReadyReadsWithBatch(mock, input, lock, nil)
}

func expectChannelImageCompletionReadyReadsWithBatch(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, batchID any) {
	expectChannelImageCompletionReadyReadsWithBatchAndStatus(mock, input, lock, batchID, imagegen.GenerationStatusCompleted)
}

func expectChannelImageCompletionReadyReadsWithBatchAndStatus(mock sqlmock.Sqlmock, input ResolveChannelImageCompletionInput, lock bool, batchID any, status string) {
	expectChannelImageCompletionCoreReadsWithBatchAndStatus(mock, input, lock, batchID, status)
	lockSuffix := ""
	if lock {
		lockSuffix = " FOR UPDATE"
	}
	mock.ExpectQuery("SELECT .* FROM `channel_messages` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(channelImageMessageRows().AddRow(41, 7, 3, 9, "run-1", ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, "om-final", nil, "run:run-1:final", `{}`, 1, nil, ChannelMessageStatusSent, time.Now().UTC(), time.Now().UTC()))
	mock.ExpectQuery("SELECT .* FROM `channel_outbox` .*LIMIT \\?" + lockSuffix).
		WillReturnRows(channelImageOutboxRows().AddRow(42, 7, 3, 9, "run-1", 41, ChannelMessageOperationCreate, 1, 1, "run:run-1:final", `{}`, nil, ChannelOutboxStatusSent, 1, time.Now().UTC(), nil, nil, nil, nil, nil, time.Now().UTC(), time.Now().UTC()))
}

func channelImageMessageRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "run_id", "direction", "operation", "external_message_id", "internal_message_id", "idempotency_key", "content_json", "render_version", "last_render_sha256", "status", "created_at", "sent_at"})
}

func channelImageOutboxRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "conversation_id", "run_id", "message_id", "operation", "sequence_no", "chunk_count", "idempotency_key", "payload_json", "render_sha256", "status", "attempts", "next_attempt_at", "retry_after_ms", "lease_owner", "lease_until", "last_error_code", "last_error_message", "created_at", "sent_at"})
}
