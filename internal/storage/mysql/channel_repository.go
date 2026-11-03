package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/imagegen"
	"github.com/konglong87/super-niuma/internal/media"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	channelImageOriginVersionLegacy = 1
	channelImageOriginVersionStrict = 2
)

// Channel repository errors are intentionally stable so service code can
// distinguish a duplicate/ownership race from a transient database failure.
var (
	ErrChannelRuntimeFingerprintMismatch = errors.New("mysql storage: channel runtime fingerprint mismatch")
	ErrChannelRunBusy                    = errors.New("mysql storage: channel conversation run busy")
	ErrChannelReactionSuperseded         = errors.New("mysql storage: channel reaction desired state superseded")
	ErrChannelImageCompletionNotReady    = errors.New("mysql storage: channel image completion origin not ready")
	ErrChannelImageCompletionOwnership   = errors.New("mysql storage: channel image completion origin ownership mismatch")
)

type ChannelImageCompletionOrigin struct {
	Version        int    `json:"version"`
	TenantID       uint64 `json:"tenant_id,omitempty"`
	AccountID      uint64 `json:"account_id"`
	ConversationID uint64 `json:"conversation_id"`
	RunID          string `json:"run_id"`
	SessionID      uint64 `json:"session_id,omitempty"`
	UserID         uint64 `json:"user_id,omitempty"`
	ReplyMessageID string `json:"reply_message_id,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
}

type ResolveChannelImageCompletionInput struct {
	EventID      uint64
	TenantID     uint64
	GenerationID string
	WorkerID     string
	Origin       ChannelImageCompletionOrigin
}

type ImageGenerationBatchProgress struct {
	BatchID   string
	Total     int
	Completed int
	Failed    int
	Terminal  bool
}

type ChannelImageCompletionMaterial struct {
	EventID          uint64
	TenantID         uint64
	GenerationID     string
	WorkerID         string
	Origin           ChannelImageCompletionOrigin
	GenerationStatus string
	AccountKey       string
	ExternalChatID   string
	ExternalThreadID string
	UserID           uint64
	SessionID        uint64
	AssetID          string
	AssetMediaType   string
	AssetName        string
	AssetSizeBytes   int64
	AssetSHA256      string
	Batch            ImageGenerationBatchProgress
}

type MaterializeChannelImageCompletionInput struct {
	ResolveChannelImageCompletionInput
	GenerationStatus      string
	SessionID             uint64
	ImageIdempotencyKey   string
	ImagePayloadJSON      string
	SummaryIdempotencyKey string
	SummaryPayloadJSON    string
	ExpectedBatch         ImageGenerationBatchProgress
}

type gormChannelAccount struct {
	ID               uint64     `gorm:"column:id;primaryKey"`
	TenantID         uint64     `gorm:"column:tenant_id"`
	Provider         string     `gorm:"column:provider"`
	AccountKey       string     `gorm:"column:account_key"`
	AppID            string     `gorm:"column:app_id"`
	CredentialRef    string     `gorm:"column:credential_ref"`
	Mode             string     `gorm:"column:mode"`
	Enabled          bool       `gorm:"column:enabled"`
	PolicyJSON       string     `gorm:"column:policy_json"`
	Status           string     `gorm:"column:status"`
	LastConnectedAt  *time.Time `gorm:"column:last_connected_at"`
	LastErrorCode    *string    `gorm:"column:last_error_code"`
	LastErrorMessage *string    `gorm:"column:last_error_message"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
	ArchivedAt       *time.Time `gorm:"column:archived_at"`
}

type gormChannelIdentity struct {
	ID              uint64     `gorm:"column:id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	AccountID       uint64     `gorm:"column:account_id"`
	ExternalUserID  string     `gorm:"column:external_user_id"`
	ExternalUnionID *string    `gorm:"column:external_union_id"`
	UserID          *uint64    `gorm:"column:user_id"`
	DisplayName     *string    `gorm:"column:display_name"`
	MetadataJSON    *string    `gorm:"column:metadata_json"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	ArchivedAt      *time.Time `gorm:"column:archived_at"`
}

type gormChannelConversation struct {
	ID                        uint64     `gorm:"column:id;primaryKey"`
	TenantID                  uint64     `gorm:"column:tenant_id"`
	AccountID                 uint64     `gorm:"column:account_id"`
	ExternalChatID            string     `gorm:"column:external_chat_id"`
	ExternalThreadID          string     `gorm:"column:external_thread_id"`
	ThreadIDSource            string     `gorm:"column:thread_id_source"`
	ScopeKey                  string     `gorm:"column:scope_key"`
	ScopeHash                 []byte     `gorm:"column:scope_hash"`
	ExternalUserID            *string    `gorm:"column:external_user_id"`
	ChatType                  string     `gorm:"column:chat_type"`
	SessionID                 *uint64    `gorm:"column:session_id"`
	RuntimeFingerprint        string     `gorm:"column:runtime_fingerprint"`
	RuntimeFingerprintVersion int        `gorm:"column:runtime_fingerprint_version"`
	WorkspaceRealpath         *string    `gorm:"column:workspace_realpath"`
	PermissionMode            string     `gorm:"column:permission_mode"`
	Status                    string     `gorm:"column:status"`
	LastInboundAt             *time.Time `gorm:"column:last_inbound_at"`
	LastOutboundAt            *time.Time `gorm:"column:last_outbound_at"`
	MetadataJSON              *string    `gorm:"column:metadata_json"`
	CreatedAt                 time.Time  `gorm:"column:created_at"`
	UpdatedAt                 time.Time  `gorm:"column:updated_at"`
	ArchivedAt                *time.Time `gorm:"column:archived_at"`
}

type gormChannelInboxEvent struct {
	ID                uint64     `gorm:"column:id;primaryKey"`
	TenantID          uint64     `gorm:"column:tenant_id"`
	AccountID         uint64     `gorm:"column:account_id"`
	ConversationID    *uint64    `gorm:"column:conversation_id"`
	ProviderEventID   string     `gorm:"column:provider_event_id"`
	ProviderMessageID *string    `gorm:"column:provider_message_id"`
	ScopeHash         []byte     `gorm:"column:scope_hash"`
	PayloadSHA256     []byte     `gorm:"column:payload_sha256"`
	PayloadCiphertext []byte     `gorm:"column:payload_ciphertext"`
	PayloadKeyVersion *string    `gorm:"column:payload_key_version"`
	PayloadRef        *string    `gorm:"column:payload_ref"`
	PayloadPurgedAt   *time.Time `gorm:"column:payload_purged_at"`
	Authorized        bool       `gorm:"column:authorized"`
	Status            string     `gorm:"column:status"`
	Attempts          uint       `gorm:"column:attempts"`
	AvailableAt       time.Time  `gorm:"column:available_at"`
	LeaseOwner        *string    `gorm:"column:lease_owner"`
	LeaseUntil        *time.Time `gorm:"column:lease_until"`
	AckedAt           *time.Time `gorm:"column:acked_at"`
	ReceivedAt        time.Time  `gorm:"column:received_at"`
	ProcessedAt       *time.Time `gorm:"column:processed_at"`
	ErrorCode         *string    `gorm:"column:error_code"`
	ErrorMessage      *string    `gorm:"column:error_message"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

type gormChannelRun struct {
	ID                        string     `gorm:"column:id;primaryKey"`
	TenantID                  uint64     `gorm:"column:tenant_id"`
	AccountID                 uint64     `gorm:"column:account_id"`
	ConversationID            uint64     `gorm:"column:conversation_id"`
	ScopeHash                 []byte     `gorm:"column:scope_hash"`
	SessionID                 *uint64    `gorm:"column:session_id"`
	RuntimeTurnID             *string    `gorm:"column:runtime_turn_id"`
	WorkerID                  *string    `gorm:"column:worker_id"`
	Status                    string     `gorm:"column:status"`
	RuntimeFingerprint        string     `gorm:"column:runtime_fingerprint"`
	RuntimeFingerprintVersion int        `gorm:"column:runtime_fingerprint_version"`
	HeartbeatAt               *time.Time `gorm:"column:heartbeat_at"`
	CancelRequestedAt         *time.Time `gorm:"column:cancel_requested_at"`
	LastEventSeq              uint64     `gorm:"column:last_event_seq"`
	StartedAt                 *time.Time `gorm:"column:started_at"`
	FinishedAt                *time.Time `gorm:"column:finished_at"`
	ErrorCode                 *string    `gorm:"column:error_code"`
	ErrorMessage              *string    `gorm:"column:error_message"`
	CreatedAt                 time.Time  `gorm:"column:created_at"`
	UpdatedAt                 time.Time  `gorm:"column:updated_at"`
}

type gormChannelInteraction struct {
	ID                         string     `gorm:"column:id;primaryKey"`
	TenantID                   uint64     `gorm:"column:tenant_id"`
	AccountID                  uint64     `gorm:"column:account_id"`
	ConversationID             uint64     `gorm:"column:conversation_id"`
	RunID                      string     `gorm:"column:run_id"`
	SessionID                  *uint64    `gorm:"column:session_id"`
	Kind                       string     `gorm:"column:kind"`
	Status                     string     `gorm:"column:status"`
	ExternalChatID             string     `gorm:"column:external_chat_id"`
	ExternalThreadID           string     `gorm:"column:external_thread_id"`
	ExternalUserID             string     `gorm:"column:external_user_id"`
	ScopeHash                  []byte     `gorm:"column:scope_hash"`
	AllowedUserID              uint64     `gorm:"column:allowed_user_id"`
	QuestionCiphertext         []byte     `gorm:"column:question_ciphertext"`
	ResumeCheckpointCiphertext []byte     `gorm:"column:resume_checkpoint_ciphertext"`
	AnswerCiphertext           []byte     `gorm:"column:answer_ciphertext"`
	NonceHash                  []byte     `gorm:"column:nonce_hash"`
	RuntimeFingerprint         string     `gorm:"column:runtime_fingerprint"`
	RuntimeFingerprintVersion  int        `gorm:"column:runtime_fingerprint_version"`
	ExpiresAt                  time.Time  `gorm:"column:expires_at"`
	AnsweredAt                 *time.Time `gorm:"column:answered_at"`
	ConsumedAt                 *time.Time `gorm:"column:consumed_at"`
	CreatedAt                  time.Time  `gorm:"column:created_at"`
	UpdatedAt                  time.Time  `gorm:"column:updated_at"`
}

type gormChannelMessage struct {
	ID                uint64     `gorm:"column:id;primaryKey"`
	TenantID          uint64     `gorm:"column:tenant_id"`
	AccountID         uint64     `gorm:"column:account_id"`
	ConversationID    uint64     `gorm:"column:conversation_id"`
	RunID             *string    `gorm:"column:run_id"`
	Direction         string     `gorm:"column:direction"`
	Operation         string     `gorm:"column:operation"`
	ExternalMessageID *string    `gorm:"column:external_message_id"`
	InternalMessageID *uint64    `gorm:"column:internal_message_id"`
	IdempotencyKey    string     `gorm:"column:idempotency_key"`
	ContentJSON       string     `gorm:"column:content_json"`
	RenderVersion     uint64     `gorm:"column:render_version"`
	LastRenderSHA256  []byte     `gorm:"column:last_render_sha256"`
	Status            string     `gorm:"column:status"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	SentAt            *time.Time `gorm:"column:sent_at"`
}

type gormChannelOutbox struct {
	ID               uint64     `gorm:"column:id;primaryKey"`
	TenantID         uint64     `gorm:"column:tenant_id"`
	AccountID        uint64     `gorm:"column:account_id"`
	ConversationID   uint64     `gorm:"column:conversation_id"`
	RunID            *string    `gorm:"column:run_id"`
	MessageID        uint64     `gorm:"column:message_id"`
	Operation        string     `gorm:"column:operation"`
	SequenceNo       uint       `gorm:"column:sequence_no"`
	ChunkCount       uint       `gorm:"column:chunk_count"`
	IdempotencyKey   string     `gorm:"column:idempotency_key"`
	PayloadJSON      string     `gorm:"column:payload_json"`
	RenderSHA256     []byte     `gorm:"column:render_sha256"`
	Status           string     `gorm:"column:status"`
	Attempts         uint       `gorm:"column:attempts"`
	NextAttemptAt    time.Time  `gorm:"column:next_attempt_at"`
	RetryAfterMS     *uint      `gorm:"column:retry_after_ms"`
	LeaseOwner       *string    `gorm:"column:lease_owner"`
	LeaseUntil       *time.Time `gorm:"column:lease_until"`
	LastErrorCode    *string    `gorm:"column:last_error_code"`
	LastErrorMessage *string    `gorm:"column:last_error_message"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	SentAt           *time.Time `gorm:"column:sent_at"`
}

type gormChannelCallback struct {
	ID                        uint64     `gorm:"column:id;primaryKey"`
	TenantID                  uint64     `gorm:"column:tenant_id"`
	AccountID                 uint64     `gorm:"column:account_id"`
	ConversationID            uint64     `gorm:"column:conversation_id"`
	RunID                     string     `gorm:"column:run_id"`
	PlatformMessageID         string     `gorm:"column:platform_message_id"`
	ActionID                  string     `gorm:"column:action_id"`
	NonceHash                 []byte     `gorm:"column:nonce_hash"`
	RuntimeFingerprint        string     `gorm:"column:runtime_fingerprint"`
	RuntimeFingerprintVersion int        `gorm:"column:runtime_fingerprint_version"`
	AllowedUserID             *uint64    `gorm:"column:allowed_user_id"`
	Status                    string     `gorm:"column:status"`
	ExpiresAt                 time.Time  `gorm:"column:expires_at"`
	ConsumedAt                *time.Time `gorm:"column:consumed_at"`
	CreatedAt                 time.Time  `gorm:"column:created_at"`
}

type gormChannelReaction struct {
	ID                uint64     `gorm:"column:id;primaryKey"`
	TenantID          uint64     `gorm:"column:tenant_id"`
	AccountID         uint64     `gorm:"column:account_id"`
	ConversationID    uint64     `gorm:"column:conversation_id"`
	ProviderMessageID string     `gorm:"column:provider_message_id"`
	DesiredEmoji      string     `gorm:"column:desired_emoji"`
	CurrentEmoji      *string    `gorm:"column:current_emoji"`
	ReactionID        *string    `gorm:"column:reaction_id"`
	Status            string     `gorm:"column:status"`
	Attempts          uint       `gorm:"column:attempts"`
	NextAttemptAt     time.Time  `gorm:"column:next_attempt_at"`
	LeaseOwner        *string    `gorm:"column:lease_owner"`
	LeaseUntil        *time.Time `gorm:"column:lease_until"`
	LastErrorCode     *string    `gorm:"column:last_error_code"`
	LastErrorMessage  *string    `gorm:"column:last_error_message"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

type gormChannelRunInput struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	TenantID     uint64    `gorm:"column:tenant_id"`
	RunID        string    `gorm:"column:run_id"`
	InboxEventID uint64    `gorm:"column:inbox_event_id"`
	SequenceNo   uint      `gorm:"column:sequence_no"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

const (
	channelAccountSelect      = "id, tenant_id, provider, account_key, app_id, credential_ref, mode, enabled, policy_json, status, last_connected_at, last_error_code, last_error_message, created_at, updated_at, archived_at"
	channelIdentitySelect     = "id, tenant_id, account_id, external_user_id, external_union_id, user_id, display_name, metadata_json, created_at, updated_at, archived_at"
	channelConversationSelect = "id, tenant_id, account_id, external_chat_id, external_thread_id, thread_id_source, scope_key, scope_hash, external_user_id, chat_type, session_id, runtime_fingerprint, runtime_fingerprint_version, workspace_realpath, permission_mode, status, last_inbound_at, last_outbound_at, metadata_json, created_at, updated_at, archived_at"
	channelInboxSelect        = "id, tenant_id, account_id, conversation_id, provider_event_id, provider_message_id, scope_hash, payload_sha256, payload_ciphertext, payload_key_version, payload_ref, payload_purged_at, authorized, status, attempts, available_at, lease_owner, lease_until, acked_at, received_at, processed_at, error_code, error_message, created_at, updated_at"
	channelRunSelect          = "id, tenant_id, account_id, conversation_id, scope_hash, session_id, runtime_turn_id, worker_id, status, runtime_fingerprint, runtime_fingerprint_version, heartbeat_at, cancel_requested_at, last_event_seq, started_at, finished_at, error_code, error_message, created_at, updated_at"
	channelInteractionSelect  = "id, tenant_id, account_id, conversation_id, run_id, session_id, kind, status, external_chat_id, external_thread_id, external_user_id, scope_hash, allowed_user_id, question_ciphertext, resume_checkpoint_ciphertext, answer_ciphertext, nonce_hash, runtime_fingerprint, runtime_fingerprint_version, expires_at, answered_at, consumed_at, created_at, updated_at"
	channelMessageSelect      = "id, tenant_id, account_id, conversation_id, run_id, direction, operation, external_message_id, internal_message_id, idempotency_key, content_json, render_version, last_render_sha256, status, created_at, sent_at"
	channelOutboxSelect       = "id, tenant_id, account_id, conversation_id, run_id, message_id, operation, sequence_no, chunk_count, idempotency_key, payload_json, render_sha256, status, attempts, next_attempt_at, retry_after_ms, lease_owner, lease_until, last_error_code, last_error_message, created_at, sent_at"
	channelCallbackSelect     = "id, tenant_id, account_id, conversation_id, run_id, platform_message_id, action_id, nonce_hash, runtime_fingerprint, runtime_fingerprint_version, allowed_user_id, status, expires_at, consumed_at, created_at"
	channelReactionSelect     = "id, tenant_id, account_id, conversation_id, provider_message_id, desired_emoji, current_emoji, reaction_id, status, attempts, next_attempt_at, lease_owner, lease_until, last_error_code, last_error_message, created_at, updated_at"
)

func (gormChannelAccount) TableName() string      { return "channel_accounts" }
func (gormChannelIdentity) TableName() string     { return "channel_identities" }
func (gormChannelConversation) TableName() string { return "channel_conversations" }
func (gormChannelInboxEvent) TableName() string   { return "channel_inbox_events" }
func (gormChannelRun) TableName() string          { return "channel_runs" }
func (gormChannelInteraction) TableName() string  { return "channel_interactions" }
func (gormChannelMessage) TableName() string      { return "channel_messages" }
func (gormChannelOutbox) TableName() string       { return "channel_outbox" }
func (gormChannelCallback) TableName() string     { return "channel_callbacks" }
func (gormChannelReaction) TableName() string     { return "channel_reactions" }
func (gormChannelRunInput) TableName() string     { return "channel_run_inputs" }

func channelStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func channelUint64Ptr(v uint64) *uint64 {
	if v == 0 {
		return nil
	}
	return &v
}
func channelUintPtr(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}
func channelAccountFromRow(x gormChannelAccount) ChannelAccount {
	return ChannelAccount{ID: x.ID, TenantID: x.TenantID, Provider: x.Provider, AccountKey: x.AccountKey, AppID: x.AppID, CredentialRef: x.CredentialRef, Mode: x.Mode, Enabled: x.Enabled, PolicyJSON: x.PolicyJSON, Status: x.Status, LastConnectedAt: valueTime(x.LastConnectedAt), LastErrorCode: valueString(x.LastErrorCode), LastErrorMessage: valueString(x.LastErrorMessage), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, ArchivedAt: valueTime(x.ArchivedAt)}
}
func channelIdentityFromRow(x gormChannelIdentity) ChannelIdentity {
	return ChannelIdentity{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ExternalUserID: x.ExternalUserID, ExternalUnionID: valueString(x.ExternalUnionID), UserID: valueUint64(x.UserID), DisplayName: valueString(x.DisplayName), MetadataJSON: valueString(x.MetadataJSON), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, ArchivedAt: valueTime(x.ArchivedAt)}
}
func channelConversationFromRow(x gormChannelConversation) ChannelConversation {
	return ChannelConversation{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ExternalChatID: x.ExternalChatID, ExternalThreadID: x.ExternalThreadID, ThreadIDSource: x.ThreadIDSource, ScopeKey: x.ScopeKey, ScopeHash: x.ScopeHash, ExternalUserID: valueString(x.ExternalUserID), ChatType: x.ChatType, SessionID: valueUint64(x.SessionID), RuntimeFingerprint: x.RuntimeFingerprint, RuntimeFingerprintVersion: x.RuntimeFingerprintVersion, WorkspaceRealpath: valueString(x.WorkspaceRealpath), PermissionMode: x.PermissionMode, Status: x.Status, LastInboundAt: valueTime(x.LastInboundAt), LastOutboundAt: valueTime(x.LastOutboundAt), MetadataJSON: valueString(x.MetadataJSON), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, ArchivedAt: valueTime(x.ArchivedAt)}
}
func channelInboxFromRow(x gormChannelInboxEvent) ChannelInboxEvent {
	return ChannelInboxEvent{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: valueUint64(x.ConversationID), ProviderEventID: x.ProviderEventID, ProviderMessageID: valueString(x.ProviderMessageID), ScopeHash: x.ScopeHash, PayloadSHA256: x.PayloadSHA256, PayloadCiphertext: x.PayloadCiphertext, PayloadKeyVersion: valueString(x.PayloadKeyVersion), PayloadRef: valueString(x.PayloadRef), PayloadPurgedAt: valueTime(x.PayloadPurgedAt), Authorized: x.Authorized, Status: x.Status, Attempts: x.Attempts, AvailableAt: x.AvailableAt, LeaseOwner: valueString(x.LeaseOwner), LeaseUntil: valueTime(x.LeaseUntil), AckedAt: valueTime(x.AckedAt), ReceivedAt: x.ReceivedAt, ProcessedAt: valueTime(x.ProcessedAt), ErrorCode: valueString(x.ErrorCode), ErrorMessage: valueString(x.ErrorMessage), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func channelRunFromRow(x gormChannelRun) ChannelRun {
	return ChannelRun{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, ScopeHash: x.ScopeHash, SessionID: valueUint64(x.SessionID), RuntimeTurnID: valueString(x.RuntimeTurnID), WorkerID: valueString(x.WorkerID), Status: x.Status, RuntimeFingerprint: x.RuntimeFingerprint, RuntimeFingerprintVersion: x.RuntimeFingerprintVersion, HeartbeatAt: valueTime(x.HeartbeatAt), CancelRequestedAt: valueTime(x.CancelRequestedAt), LastEventSeq: x.LastEventSeq, StartedAt: valueTime(x.StartedAt), FinishedAt: valueTime(x.FinishedAt), ErrorCode: valueString(x.ErrorCode), ErrorMessage: valueString(x.ErrorMessage), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func channelInteractionFromRow(x gormChannelInteraction) ChannelInteraction {
	return ChannelInteraction{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, RunID: x.RunID, SessionID: valueUint64(x.SessionID), Kind: x.Kind, Status: x.Status, ExternalChatID: x.ExternalChatID, ExternalThreadID: x.ExternalThreadID, ExternalUserID: x.ExternalUserID, ScopeHash: x.ScopeHash, AllowedUserID: x.AllowedUserID, QuestionCiphertext: x.QuestionCiphertext, ResumeCheckpointCiphertext: x.ResumeCheckpointCiphertext, AnswerCiphertext: x.AnswerCiphertext, NonceHash: x.NonceHash, RuntimeFingerprint: x.RuntimeFingerprint, RuntimeFingerprintVersion: x.RuntimeFingerprintVersion, ExpiresAt: x.ExpiresAt, AnsweredAt: valueTime(x.AnsweredAt), ConsumedAt: valueTime(x.ConsumedAt), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func channelMessageFromRow(x gormChannelMessage) ChannelMessage {
	return ChannelMessage{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, RunID: valueString(x.RunID), Direction: x.Direction, Operation: x.Operation, ExternalMessageID: valueString(x.ExternalMessageID), InternalMessageID: valueUint64(x.InternalMessageID), IdempotencyKey: x.IdempotencyKey, ContentJSON: x.ContentJSON, RenderVersion: x.RenderVersion, LastRenderSHA256: x.LastRenderSHA256, Status: x.Status, CreatedAt: x.CreatedAt, SentAt: valueTime(x.SentAt)}
}
func channelOutboxFromRow(x gormChannelOutbox) ChannelOutbox {
	return ChannelOutbox{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, RunID: valueString(x.RunID), MessageID: x.MessageID, Operation: x.Operation, SequenceNo: x.SequenceNo, ChunkCount: x.ChunkCount, IdempotencyKey: x.IdempotencyKey, PayloadJSON: x.PayloadJSON, RenderSHA256: x.RenderSHA256, Status: x.Status, Attempts: x.Attempts, NextAttemptAt: x.NextAttemptAt, RetryAfterMS: valueUint(x.RetryAfterMS), LeaseOwner: valueString(x.LeaseOwner), LeaseUntil: valueTime(x.LeaseUntil), LastErrorCode: valueString(x.LastErrorCode), LastErrorMessage: valueString(x.LastErrorMessage), CreatedAt: x.CreatedAt, SentAt: valueTime(x.SentAt)}
}
func channelCallbackFromRow(x gormChannelCallback) ChannelCallback {
	return ChannelCallback{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, RunID: x.RunID, PlatformMessageID: x.PlatformMessageID, ActionID: x.ActionID, NonceHash: x.NonceHash, RuntimeFingerprint: x.RuntimeFingerprint, RuntimeFingerprintVersion: x.RuntimeFingerprintVersion, AllowedUserID: valueUint64(x.AllowedUserID), Status: x.Status, ExpiresAt: x.ExpiresAt, ConsumedAt: valueTime(x.ConsumedAt), CreatedAt: x.CreatedAt}
}
func channelReactionFromRow(x gormChannelReaction) ChannelReaction {
	return ChannelReaction{ID: x.ID, TenantID: x.TenantID, AccountID: x.AccountID, ConversationID: x.ConversationID, ProviderMessageID: x.ProviderMessageID, DesiredEmoji: x.DesiredEmoji, CurrentEmoji: valueString(x.CurrentEmoji), ReactionID: valueString(x.ReactionID), Status: x.Status, Attempts: x.Attempts, NextAttemptAt: x.NextAttemptAt, LeaseOwner: valueString(x.LeaseOwner), LeaseUntil: valueTime(x.LeaseUntil), LastErrorCode: valueString(x.LastErrorCode), LastErrorMessage: valueString(x.LastErrorMessage), CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
}
func valueString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func valueUint64(v *uint64) uint64 {
	if v == nil {
		return 0
	}
	return *v
}
func valueUint(v *uint) uint {
	if v == nil {
		return 0
	}
	return *v
}
func valueTime(v *time.Time) time.Time {
	if v == nil {
		return time.Time{}
	}
	return *v
}

func channelNow(db *gorm.DB) clause.Expr {
	if isSQLite(db) {
		return gorm.Expr("CURRENT_TIMESTAMP")
	}
	return gorm.Expr("CURRENT_TIMESTAMP(6)")
}

func channelForUpdate(db *gorm.DB) *gorm.DB {
	if isSQLite(db) {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

func channelForUpdateSkipLocked(db *gorm.DB) *gorm.DB {
	if isSQLite(db) {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
}

func (r *GormRepository) CreateChannelAccount(ctx context.Context, input ChannelAccountInput) (ChannelAccount, error) {
	if input.TenantID == 0 || input.Provider == "" || input.AccountKey == "" {
		return ChannelAccount{}, ErrInvalidInput
	}
	if input.PolicyJSON == "" {
		input.PolicyJSON = `{}`
	}
	if input.Status == "" {
		input.Status = ChannelAccountStatusDisabled
	}
	row := gormChannelAccount{TenantID: input.TenantID, Provider: input.Provider, AccountKey: input.AccountKey, AppID: input.AppID, CredentialRef: input.CredentialRef, Mode: input.Mode, Enabled: input.Enabled, PolicyJSON: input.PolicyJSON, Status: input.Status, LastConnectedAt: input.LastConnectedAt, LastErrorCode: channelStringPtr(input.LastErrorCode), LastErrorMessage: channelStringPtr(input.LastErrorMessage), ArchivedAt: input.ArchivedAt}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelAccount{}, err
	}
	return channelAccountFromRow(row), nil
}

func (r *GormRepository) GetChannelAccount(ctx context.Context, tenantID, accountID uint64) (ChannelAccount, error) {
	var row gormChannelAccount
	err := r.with(ctx).Table("channel_accounts").Select(channelAccountSelect).Where("tenant_id = ? AND id = ? AND archived_at IS NULL", tenantID, accountID).Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelAccount{}, ErrNotFound
	}
	if err != nil {
		return ChannelAccount{}, err
	}
	return channelAccountFromRow(row), nil
}

func (r *GormRepository) ListChannelAccounts(ctx context.Context, tenantID uint64, opts ListOptions) ([]ChannelAccount, error) {
	var rows []gormChannelAccount
	q := r.with(ctx).Table("channel_accounts").Select(channelAccountSelect).Where("tenant_id = ? AND archived_at IS NULL", tenantID).Order("id ASC").Limit(normalizeLimit(opts.Limit))
	if opts.Cursor != 0 {
		q = q.Where("id > ?", opts.Cursor)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ChannelAccount, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelAccountFromRow(row))
	}
	return out, nil
}

func (r *GormRepository) UpdateChannelAccount(ctx context.Context, input ChannelAccountInput) error {
	if input.TenantID == 0 || input.ID == 0 {
		return ErrInvalidInput
	}
	updates := map[string]any{"provider": input.Provider, "account_key": input.AccountKey, "app_id": input.AppID, "credential_ref": input.CredentialRef, "mode": input.Mode, "enabled": input.Enabled, "policy_json": input.PolicyJSON, "status": input.Status, "last_connected_at": input.LastConnectedAt, "last_error_code": channelStringPtr(input.LastErrorCode), "last_error_message": channelStringPtr(input.LastErrorMessage), "archived_at": input.ArchivedAt}
	res := r.with(ctx).Table("channel_accounts").Where("tenant_id = ? AND id = ?", input.TenantID, input.ID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ClaimInboxEvent(ctx context.Context, input ChannelInboxEventInput) (ChannelInboxEvent, bool, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ProviderEventID == "" {
		return ChannelInboxEvent{}, false, ErrInvalidInput
	}
	var row gormChannelInboxEvent
	err := r.with(ctx).Table("channel_inbox_events").Select(channelInboxSelect).Where("tenant_id = ? AND account_id = ? AND provider_event_id = ?", input.TenantID, input.AccountID, input.ProviderEventID).Limit(1).Take(&row).Error
	if err == nil {
		return channelInboxFromRow(row), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelInboxEvent{}, false, err
	}
	row = gormChannelInboxEvent{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: channelUint64Ptr(input.ConversationID), ProviderEventID: input.ProviderEventID, ProviderMessageID: channelStringPtr(input.ProviderMessageID), ScopeHash: input.ScopeHash, PayloadSHA256: input.PayloadSHA256, PayloadCiphertext: input.PayloadCiphertext, PayloadKeyVersion: channelStringPtr(input.PayloadKeyVersion), PayloadRef: channelStringPtr(input.PayloadRef), Authorized: input.Authorized, Status: input.Status, Attempts: input.Attempts, AvailableAt: time.Now().UTC(), LeaseOwner: channelStringPtr(input.LeaseOwner), LeaseUntil: input.LeaseUntil, AckedAt: input.AckedAt, ReceivedAt: valueTime(input.ReceivedAt), ProcessedAt: input.ProcessedAt, ErrorCode: channelStringPtr(input.ErrorCode), ErrorMessage: channelStringPtr(input.ErrorMessage)}
	if row.Status == "" {
		row.Status = ChannelInboxStatusReceived
	}
	if row.ReceivedAt.IsZero() {
		row.ReceivedAt = time.Now().UTC()
	}
	if input.AvailableAt != nil {
		row.AvailableAt = *input.AvailableAt
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		// A concurrent insert is an idempotent claim. Read back only within the
		// same tenant/account ownership boundary.
		if getErr := r.with(ctx).Table("channel_inbox_events").Select(channelInboxSelect).Where("tenant_id = ? AND account_id = ? AND provider_event_id = ?", input.TenantID, input.AccountID, input.ProviderEventID).Limit(1).Take(&row).Error; getErr == nil {
			return channelInboxFromRow(row), false, nil
		}
		if input.ProviderMessageID != "" {
			if getErr := r.with(ctx).Table("channel_inbox_events").Select(channelInboxSelect).Where("tenant_id = ? AND account_id = ? AND provider_message_id = ?", input.TenantID, input.AccountID, input.ProviderMessageID).Limit(1).Take(&row).Error; getErr == nil {
				return channelInboxFromRow(row), false, nil
			}
		}
		return ChannelInboxEvent{}, false, err
	}
	return channelInboxFromRow(row), true, nil
}

func (r *GormRepository) updateInbox(ctx context.Context, tenantID, eventID uint64, updates map[string]any) error {
	res := r.with(ctx).Table("channel_inbox_events").Where("tenant_id = ? AND id = ?", tenantID, eventID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) MarkInboxQueued(ctx context.Context, tenantID, eventID, conversationID uint64, scopeHash []byte) error {
	return r.updateInbox(ctx, tenantID, eventID, map[string]any{
		"conversation_id": conversationID,
		"scope_hash":      scopeHash,
		"status":          ChannelInboxStatusQueued,
		"acked_at":        gorm.Expr("COALESCE(acked_at, ?)", channelNow(r.with(ctx))),
		"lease_owner":     nil,
		"lease_until":     nil,
	})
}

func (r *GormRepository) MarkInboxProcessing(ctx context.Context, tenantID, eventID uint64, workerID string, leaseUntil time.Time) error {
	if tenantID == 0 || eventID == 0 || workerID == "" {
		return ErrInvalidInput
	}
	leaseColumn := channelTimeColumn(r.db, "lease_until")
	res := r.with(ctx).Table("channel_inbox_events").Where("tenant_id = ? AND id = ? AND status IN (?, ?, ?) AND (lease_until IS NULL OR "+leaseColumn+" < ?)", tenantID, eventID, ChannelInboxStatusQueued, ChannelInboxStatusRetry, ChannelInboxStatusProcessing, time.Now().UTC()).Updates(map[string]any{"status": ChannelInboxStatusProcessing, "lease_owner": workerID, "lease_until": leaseUntil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) MarkInboxProcessed(ctx context.Context, tenantID, eventID uint64) error {
	return r.updateInbox(ctx, tenantID, eventID, map[string]any{"status": ChannelInboxStatusProcessed, "processed_at": channelNow(r.with(ctx)), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkInboxRetry(ctx context.Context, tenantID, eventID uint64, availableAt time.Time, errorCode, errorMessage string) error {
	return r.updateInbox(ctx, tenantID, eventID, map[string]any{"status": ChannelInboxStatusRetry, "available_at": availableAt, "attempts": gorm.Expr("attempts + 1"), "error_code": channelStringPtr(errorCode), "error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkInboxFailed(ctx context.Context, tenantID, eventID uint64, errorCode, errorMessage string) error {
	return r.updateInbox(ctx, tenantID, eventID, map[string]any{"status": ChannelInboxStatusFailed, "attempts": gorm.Expr("attempts + 1"), "error_code": channelStringPtr(errorCode), "error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkInboxIgnored(ctx context.Context, tenantID, eventID uint64, errorCode, errorMessage string) error {
	return r.updateInbox(ctx, tenantID, eventID, map[string]any{"status": ChannelInboxStatusIgnored, "processed_at": channelNow(r.with(ctx)), "error_code": channelStringPtr(errorCode), "error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}

// ClaimDueInbox claims queued/retry inbox events for worker restart recovery.
// The row lock and lease make this safe for competing channel workers.
func (r *GormRepository) ClaimDueInbox(ctx context.Context, tenantID, accountID uint64, workerID string, limit int, leaseUntil time.Time) ([]ChannelInboxEvent, error) {
	if tenantID == 0 || accountID == 0 || workerID == "" || limit <= 0 {
		return nil, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	var rows []gormChannelInboxEvent
	now := time.Now().UTC()
	availableColumn := channelTimeColumn(tx, "available_at")
	leaseColumn := channelTimeColumn(tx, "lease_until")
	q := channelForUpdate(tx.Table("channel_inbox_events").Select(channelInboxSelect).
		Where("tenant_id = ? AND account_id = ? AND status IN (?, ?, ?) AND "+availableColumn+" <= ? AND (lease_until IS NULL OR "+leaseColumn+" < ?)", tenantID, accountID, ChannelInboxStatusQueued, ChannelInboxStatusRetry, ChannelInboxStatusProcessing, now, now).
		Order("available_at ASC, id ASC").Limit(limit)).Find(&rows)
	if q.Error != nil {
		_ = tx.Rollback()
		return nil, q.Error
	}
	if len(rows) == 0 {
		_ = tx.Rollback()
		return []ChannelInboxEvent{}, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if err := tx.Table("channel_inbox_events").Where("tenant_id = ? AND account_id = ? AND id IN ?", tenantID, accountID, ids).Updates(map[string]any{"status": ChannelInboxStatusProcessing, "lease_owner": workerID, "lease_until": leaseUntil}).Error; err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = ChannelInboxStatusProcessing
		rows[i].LeaseOwner = channelStringPtr(workerID)
		rows[i].LeaseUntil = &leaseUntil
	}
	out := make([]ChannelInboxEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelInboxFromRow(row))
	}
	return out, nil
}

func (r *GormRepository) GetOrCreateChannelIdentity(ctx context.Context, input ChannelIdentityInput) (ChannelIdentity, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ExternalUserID == "" {
		return ChannelIdentity{}, ErrInvalidInput
	}
	var row gormChannelIdentity
	q := r.with(ctx).Table("channel_identities").Select(channelIdentitySelect).Where("tenant_id = ? AND account_id = ? AND external_user_id = ?", input.TenantID, input.AccountID, input.ExternalUserID).Limit(1)
	err := q.Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = gormChannelIdentity{TenantID: input.TenantID, AccountID: input.AccountID, ExternalUserID: input.ExternalUserID, ExternalUnionID: channelStringPtr(input.ExternalUnionID), UserID: channelUint64Ptr(input.UserID), DisplayName: channelStringPtr(input.DisplayName), MetadataJSON: channelStringPtr(input.MetadataJSON), ArchivedAt: input.ArchivedAt}
		if err = r.with(ctx).Create(&row).Error; err == nil {
			return channelIdentityFromRow(row), nil
		}
		if err2 := q.Take(&row).Error; err2 != nil {
			return ChannelIdentity{}, err
		}
		return channelIdentityFromRow(row), nil
	}
	if err != nil {
		return ChannelIdentity{}, err
	}
	return channelIdentityFromRow(row), nil
}

func (r *GormRepository) GetOrCreateChannelConversation(ctx context.Context, input ChannelConversationInput) (ChannelConversation, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ExternalChatID == "" || len(input.ScopeHash) == 0 {
		return ChannelConversation{}, ErrInvalidInput
	}
	var row gormChannelConversation
	q := r.with(ctx).Table("channel_conversations").Select(channelConversationSelect).Where("tenant_id = ? AND account_id = ? AND scope_hash = ?", input.TenantID, input.AccountID, input.ScopeHash).Limit(1)
	err := q.Take(&row).Error
	if err == nil {
		if row.RuntimeFingerprint != "" && input.RuntimeFingerprint != "" && (row.RuntimeFingerprint != input.RuntimeFingerprint || row.RuntimeFingerprintVersion != input.RuntimeFingerprintVersion) {
			return ChannelConversation{}, fmt.Errorf("%w: conversation_id=%d", ErrChannelRuntimeFingerprintMismatch, row.ID)
		}
		return channelConversationFromRow(row), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelConversation{}, err
	}
	row = gormChannelConversation{TenantID: input.TenantID, AccountID: input.AccountID, ExternalChatID: input.ExternalChatID, ExternalThreadID: input.ExternalThreadID, ThreadIDSource: input.ThreadIDSource, ScopeKey: input.ScopeKey, ScopeHash: input.ScopeHash, ExternalUserID: channelStringPtr(input.ExternalUserID), ChatType: input.ChatType, SessionID: channelUint64Ptr(input.SessionID), RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, WorkspaceRealpath: channelStringPtr(input.WorkspaceRealpath), PermissionMode: input.PermissionMode, Status: input.Status, LastInboundAt: input.LastInboundAt, LastOutboundAt: input.LastOutboundAt, MetadataJSON: channelStringPtr(input.MetadataJSON), ArchivedAt: input.ArchivedAt}
	if row.ThreadIDSource == "" {
		row.ThreadIDSource = ChannelThreadIDSourceNone
	}
	if row.Status == "" {
		row.Status = ChannelConversationStatusActive
	}
	if row.RuntimeFingerprintVersion == 0 {
		row.RuntimeFingerprintVersion = 1
	}
	if row.PermissionMode == "" {
		row.PermissionMode = ChannelPermissionModeAsk
	} else if !validChannelPermissionMode(row.PermissionMode) {
		return ChannelConversation{}, ErrInvalidInput
	}
	if err = r.with(ctx).Create(&row).Error; err == nil {
		return channelConversationFromRow(row), nil
	}
	if err2 := q.Take(&row).Error; err2 != nil {
		return ChannelConversation{}, err
	}
	if row.RuntimeFingerprint != input.RuntimeFingerprint || row.RuntimeFingerprintVersion != input.RuntimeFingerprintVersion {
		return ChannelConversation{}, ErrChannelRuntimeFingerprintMismatch
	}
	return channelConversationFromRow(row), nil
}

func (r *GormRepository) GetChannelConversationByScope(ctx context.Context, tenantID, accountID uint64, scopeHash []byte) (ChannelConversation, error) {
	if tenantID == 0 || accountID == 0 || len(scopeHash) == 0 {
		return ChannelConversation{}, ErrInvalidInput
	}
	var row gormChannelConversation
	err := r.with(ctx).Table("channel_conversations").Select(channelConversationSelect).
		Where("tenant_id = ? AND account_id = ? AND scope_hash = ? AND archived_at IS NULL", tenantID, accountID, scopeHash).
		Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelConversation{}, ErrNotFound
	}
	if err != nil {
		return ChannelConversation{}, err
	}
	return channelConversationFromRow(row), nil
}

func (r *GormRepository) UpdateChannelConversationControls(ctx context.Context, input ChannelConversationControlsInput) error {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.WorkspaceRealpath == "" && input.PermissionMode == "" {
		return ErrInvalidInput
	}
	updates := map[string]any{}
	if input.WorkspaceRealpath != "" {
		updates["workspace_realpath"] = input.WorkspaceRealpath
	}
	if input.PermissionMode != "" {
		if !validChannelPermissionMode(input.PermissionMode) {
			return ErrInvalidInput
		}
		updates["permission_mode"] = input.PermissionMode
	}
	res := r.with(ctx).Table("channel_conversations").Where("tenant_id = ? AND account_id = ? AND id = ?", input.TenantID, input.AccountID, input.ConversationID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func validChannelPermissionMode(mode string) bool {
	return mode == ChannelPermissionModeAsk || mode == ChannelPermissionModeAllow || mode == ChannelPermissionModeDeny
}

func (r *GormRepository) RotateChannelConversationSession(ctx context.Context, input ChannelSessionRotationInput) (uint64, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.UserID == 0 || input.InboxEventID == 0 {
		return 0, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	rollback := func(err error) (uint64, error) {
		_ = tx.Rollback().Error
		return 0, err
	}
	var conversation gormChannelConversation
	err := channelForUpdate(
		tx.Table("channel_conversations").Select(channelConversationSelect).
			Where("tenant_id = ? AND account_id = ? AND id = ? AND archived_at IS NULL", input.TenantID, input.AccountID, input.ConversationID),
	).
		Limit(1).Take(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrNotFound)
	}
	if err != nil {
		return rollback(err)
	}
	sessionKey := fmt.Sprintf("channel:%x:new:%d", conversation.ScopeHash, input.InboxEventID)
	sessionID, err := upsertSessionTx(tx, SessionInput{
		TenantID:   input.TenantID,
		UserID:     input.UserID,
		SessionKey: sessionKey,
		Title:      "Feishu " + conversation.ExternalChatID,
		Status:     "active",
		Model:      input.Model,
		CWD:        valueString(conversation.WorkspaceRealpath),
	})
	if err != nil {
		return rollback(err)
	}
	res := tx.Table("channel_conversations").Where("tenant_id = ? AND account_id = ? AND id = ?", input.TenantID, input.AccountID, input.ConversationID).Update("session_id", sessionID)
	if res.Error != nil {
		return rollback(res.Error)
	}
	if res.RowsAffected == 0 {
		return rollback(ErrNotFound)
	}
	if err = tx.Commit().Error; err != nil {
		return 0, err
	}
	return sessionID, nil
}

func (r *GormRepository) CreateChannelRun(ctx context.Context, input ChannelRunInput) (ChannelRun, error) {
	if input.ID == "" || input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 {
		return ChannelRun{}, ErrInvalidInput
	}
	if input.Status == "" {
		input.Status = ChannelRunStatusQueued
	}
	if input.RuntimeFingerprintVersion == 0 {
		input.RuntimeFingerprintVersion = 1
	}
	row := gormChannelRun{ID: input.ID, TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, ScopeHash: input.ScopeHash, SessionID: channelUint64Ptr(input.SessionID), RuntimeTurnID: channelStringPtr(input.RuntimeTurnID), WorkerID: channelStringPtr(input.WorkerID), Status: input.Status, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, HeartbeatAt: input.HeartbeatAt, CancelRequestedAt: input.CancelRequestedAt, LastEventSeq: input.LastEventSeq, StartedAt: input.StartedAt, FinishedAt: input.FinishedAt, ErrorCode: channelStringPtr(input.ErrorCode), ErrorMessage: channelStringPtr(input.ErrorMessage)}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelRun{}, err
	}
	return channelRunFromRow(row), nil
}

func (r *GormRepository) CreateChannelInteraction(ctx context.Context, input ChannelInteractionInput) (ChannelInteraction, error) {
	if input.ID == "" || input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.RunID == "" || input.Kind == "" || input.ExternalChatID == "" || input.AllowedUserID == 0 || len(input.ScopeHash) == 0 || len(input.QuestionCiphertext) == 0 || len(input.ResumeCheckpointCiphertext) == 0 || len(input.NonceHash) == 0 || input.ExpiresAt.IsZero() {
		return ChannelInteraction{}, ErrInvalidInput
	}
	if input.Status == "" {
		input.Status = ChannelInteractionStatusPending
	}
	if input.RuntimeFingerprintVersion == 0 {
		input.RuntimeFingerprintVersion = 1
	}
	row := gormChannelInteraction{
		ID:                         input.ID,
		TenantID:                   input.TenantID,
		AccountID:                  input.AccountID,
		ConversationID:             input.ConversationID,
		RunID:                      input.RunID,
		SessionID:                  channelUint64Ptr(input.SessionID),
		Kind:                       input.Kind,
		Status:                     input.Status,
		ExternalChatID:             input.ExternalChatID,
		ExternalThreadID:           input.ExternalThreadID,
		ExternalUserID:             input.ExternalUserID,
		ScopeHash:                  append([]byte(nil), input.ScopeHash...),
		AllowedUserID:              input.AllowedUserID,
		QuestionCiphertext:         append([]byte(nil), input.QuestionCiphertext...),
		ResumeCheckpointCiphertext: append([]byte(nil), input.ResumeCheckpointCiphertext...),
		AnswerCiphertext:           append([]byte(nil), input.AnswerCiphertext...),
		NonceHash:                  append([]byte(nil), input.NonceHash...),
		RuntimeFingerprint:         input.RuntimeFingerprint,
		RuntimeFingerprintVersion:  input.RuntimeFingerprintVersion,
		ExpiresAt:                  input.ExpiresAt,
		AnsweredAt:                 input.AnsweredAt,
		ConsumedAt:                 input.ConsumedAt,
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelInteraction{}, err
	}
	return channelInteractionFromRow(row), nil
}

func (r *GormRepository) AnswerChannelInteraction(ctx context.Context, tenantID, accountID uint64, interactionID string, nonceHash []byte, allowedUserID uint64, runtimeFingerprint string, runtimeFingerprintVersion int, answerCiphertext []byte, now time.Time) error {
	if tenantID == 0 || accountID == 0 || interactionID == "" || len(nonceHash) == 0 || allowedUserID == 0 || runtimeFingerprint == "" || runtimeFingerprintVersion == 0 || len(answerCiphertext) == 0 || now.IsZero() {
		return ErrInvalidInput
	}
	expiresColumn := channelTimeColumn(r.db, "expires_at")
	res := r.with(ctx).Table("channel_interactions").Where("tenant_id = ? AND account_id = ? AND id = ? AND nonce_hash = ? AND allowed_user_id = ? AND runtime_fingerprint = ? AND runtime_fingerprint_version = ? AND status = ? AND "+expiresColumn+" > ?", tenantID, accountID, interactionID, nonceHash, allowedUserID, runtimeFingerprint, runtimeFingerprintVersion, ChannelInteractionStatusPending, now).Updates(map[string]any{
		"status":            ChannelInteractionStatusAnswered,
		"answer_ciphertext": answerCiphertext,
		"answered_at":       now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) GetChannelInteraction(ctx context.Context, tenantID, accountID uint64, interactionID string) (ChannelInteraction, error) {
	if tenantID == 0 || accountID == 0 || interactionID == "" {
		return ChannelInteraction{}, ErrInvalidInput
	}
	var row gormChannelInteraction
	err := r.with(ctx).Table("channel_interactions").Select(channelInteractionSelect).Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, interactionID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelInteraction{}, ErrNotFound
	}
	if err != nil {
		return ChannelInteraction{}, err
	}
	return channelInteractionFromRow(row), nil
}

func (r *GormRepository) FindChannelInteractionByNonce(ctx context.Context, tenantID, accountID uint64, nonceHash []byte) (ChannelInteraction, error) {
	if tenantID == 0 || accountID == 0 || len(nonceHash) == 0 {
		return ChannelInteraction{}, ErrInvalidInput
	}
	var row gormChannelInteraction
	err := r.with(ctx).Table("channel_interactions").Select(channelInteractionSelect).Where("tenant_id = ? AND account_id = ? AND nonce_hash = ?", tenantID, accountID, nonceHash).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelInteraction{}, ErrNotFound
	}
	if err != nil {
		return ChannelInteraction{}, err
	}
	return channelInteractionFromRow(row), nil
}

func (r *GormRepository) FindPendingChannelInteraction(ctx context.Context, tenantID, accountID, conversationID, allowedUserID uint64, externalChatID string, now time.Time) (ChannelInteraction, error) {
	if tenantID == 0 || accountID == 0 || conversationID == 0 || allowedUserID == 0 || externalChatID == "" || now.IsZero() {
		return ChannelInteraction{}, ErrInvalidInput
	}
	var row gormChannelInteraction
	expiresColumn := channelTimeColumn(r.db, "expires_at")
	err := r.with(ctx).Table("channel_interactions").Select(channelInteractionSelect).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND allowed_user_id = ? AND external_chat_id = ? AND status = ? AND "+expiresColumn+" > ?", tenantID, accountID, conversationID, allowedUserID, externalChatID, ChannelInteractionStatusPending, now).Order("created_at ASC, id ASC").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelInteraction{}, ErrNotFound
	}
	if err != nil {
		return ChannelInteraction{}, err
	}
	return channelInteractionFromRow(row), nil
}

func (r *GormRepository) ListAnsweredChannelInteractions(ctx context.Context, tenantID, accountID uint64, limit int) ([]ChannelInteraction, error) {
	if tenantID == 0 || accountID == 0 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := r.with(ctx).Table("channel_interactions").Select(channelInteractionSelect).Where("tenant_id = ? AND account_id = ? AND status = ?", tenantID, accountID, ChannelInteractionStatusAnswered).Order("answered_at ASC, id ASC").Limit(limit).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelInteraction
	for rows.Next() {
		var row gormChannelInteraction
		if err := r.with(ctx).ScanRows(rows, &row); err != nil {
			return nil, err
		}
		out = append(out, channelInteractionFromRow(row))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *GormRepository) ExpirePendingChannelInteractions(ctx context.Context, tenantID, accountID uint64, now time.Time) ([]ChannelInteraction, error) {
	if tenantID == 0 || accountID == 0 || now.IsZero() {
		return nil, ErrInvalidInput
	}
	expiresColumn := channelTimeColumn(r.db, "expires_at")
	rows, err := r.with(ctx).Table("channel_interactions").Select(channelInteractionSelect).Where("tenant_id = ? AND account_id = ? AND status = ? AND "+expiresColumn+" <= ?", tenantID, accountID, ChannelInteractionStatusPending, now).Order("expires_at ASC, id ASC").Limit(100).Rows()
	if err != nil {
		return nil, err
	}
	pending := make([]gormChannelInteraction, 0, 100)
	for rows.Next() {
		var row gormChannelInteraction
		if err := r.with(ctx).ScanRows(rows, &row); err != nil {
			_ = rows.Close()
			return nil, err
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	expired := make([]ChannelInteraction, 0, len(pending))
	for _, row := range pending {
		updated := r.with(ctx).Table("channel_interactions").Where("tenant_id = ? AND account_id = ? AND id = ? AND status = ?", tenantID, accountID, row.ID, ChannelInteractionStatusPending).Updates(map[string]any{"status": ChannelInteractionStatusExpired})
		if updated.Error != nil {
			return nil, updated.Error
		}
		if updated.RowsAffected == 1 {
			row.Status = ChannelInteractionStatusExpired
			expired = append(expired, channelInteractionFromRow(row))
		}
	}
	return expired, nil
}

func (r *GormRepository) AttachRunInputs(ctx context.Context, tenantID uint64, runID string, inputs []ChannelRunInputEvent) error {
	if tenantID == 0 || runID == "" {
		return ErrInvalidInput
	}
	for _, input := range inputs {
		if input.TenantID != 0 && input.TenantID != tenantID {
			return ErrInvalidInput
		}
		row := map[string]any{"tenant_id": tenantID, "run_id": runID, "inbox_event_id": input.InboxEventID, "sequence_no": input.SequenceNo}
		if err := r.with(ctx).Table("channel_run_inputs").Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *GormRepository) ClaimConversationRun(ctx context.Context, tenantID, accountID, conversationID uint64, workerID string) (ChannelRun, error) {
	if tenantID == 0 || accountID == 0 || conversationID == 0 || workerID == "" {
		return ChannelRun{}, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return ChannelRun{}, tx.Error
	}
	defer func() {
		if recover() != nil {
			tx.Rollback()
			panic("channel run claim panic")
		}
	}()
	now := time.Now().UTC()
	expiresColumn := channelTimeColumn(tx, "interactions.expires_at")
	var existing gormChannelRun
	err := channelForUpdate(
		tx.Table("channel_runs").Select(channelRunSelect).
			Where(`tenant_id = ? AND account_id = ? AND conversation_id = ? AND (
				status IN (?, ?) OR
				(status = ? AND EXISTS (
					SELECT 1 FROM channel_interactions interactions
					WHERE interactions.tenant_id = ?
						AND interactions.account_id = ?
						AND interactions.run_id = channel_runs.id
						AND (
							(interactions.status = ? AND `+expiresColumn+` > ?)
							OR interactions.status = ?
						)
				))
			)`,
				tenantID, accountID, conversationID,
				ChannelRunStatusRunning, ChannelRunStatusCancelRequested,
				ChannelRunStatusWaitingInput,
				tenantID, accountID,
				ChannelInteractionStatusPending, now,
				ChannelInteractionStatusAnswered,
			),
	).
		Limit(1).Take(&existing).Error
	if err == nil {
		tx.Rollback()
		return ChannelRun{}, ErrChannelRunBusy
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		tx.Rollback()
		return ChannelRun{}, err
	}
	var row gormChannelRun
	err = channelForUpdate(
		tx.Table("channel_runs").Select(channelRunSelect).
			Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND status = ?", tenantID, accountID, conversationID, ChannelRunStatusQueued),
	).
		Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		tx.Rollback()
		return ChannelRun{}, ErrNotFound
	}
	if err != nil {
		tx.Rollback()
		return ChannelRun{}, err
	}
	res := tx.Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND id = ? AND status = ?", tenantID, accountID, row.ID, ChannelRunStatusQueued).Updates(map[string]any{"status": ChannelRunStatusRunning, "worker_id": workerID, "heartbeat_at": now, "started_at": now})
	if res.Error != nil {
		tx.Rollback()
		return ChannelRun{}, res.Error
	}
	if res.RowsAffected != 1 {
		tx.Rollback()
		return ChannelRun{}, ErrChannelRunBusy
	}
	if err = tx.Commit().Error; err != nil {
		return ChannelRun{}, err
	}
	row.Status = ChannelRunStatusRunning
	row.WorkerID = &workerID
	row.HeartbeatAt = &now
	row.StartedAt = &now
	return channelRunFromRow(row), nil
}

func (r *GormRepository) GetQueuedChannelRun(ctx context.Context, tenantID, accountID, conversationID uint64) (ChannelRun, error) {
	var row gormChannelRun
	err := r.with(ctx).Table("channel_runs").Select(channelRunSelect).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND status = ?", tenantID, accountID, conversationID, ChannelRunStatusQueued).Order("created_at ASC, id ASC").Limit(1).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChannelRun{}, ErrNotFound
	}
	if err != nil {
		return ChannelRun{}, err
	}
	return channelRunFromRow(row), nil
}

func (r *GormRepository) RequeueStrandedChannelRuns(ctx context.Context, tenantID, accountID uint64) error {
	if tenantID == 0 || accountID == 0 {
		return ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	if err := tx.Exec("UPDATE channel_runs SET status = ?, worker_id = NULL, heartbeat_at = NULL, started_at = NULL WHERE tenant_id = ? AND account_id = ? AND status = ?", ChannelRunStatusQueued, tenantID, accountID, ChannelRunStatusRunning).Error; err != nil {
		_ = tx.Rollback().Error
		return err
	}
	var cancelInboxQuery string
	if isSQLite(tx) {
		cancelInboxQuery = `UPDATE channel_inbox_events
			SET status = ?, processed_at = CURRENT_TIMESTAMP, lease_owner = NULL, lease_until = NULL
			WHERE tenant_id = ? AND account_id = ? AND id IN (
				SELECT ri.inbox_event_id FROM channel_run_inputs ri
				JOIN channel_runs r ON r.id = ri.run_id AND r.tenant_id = ?
				WHERE r.status = ?
			)`
	} else {
		cancelInboxQuery = "UPDATE channel_inbox_events i JOIN channel_run_inputs ri ON ri.inbox_event_id = i.id JOIN channel_runs r ON r.id = ri.run_id AND r.tenant_id = i.tenant_id SET i.status = ?, i.processed_at = CURRENT_TIMESTAMP(6), i.lease_owner = NULL, i.lease_until = NULL WHERE i.tenant_id = ? AND i.account_id = ? AND r.status = ?"
	}
	var cancelInboxArgs []any
	if isSQLite(tx) {
		cancelInboxArgs = []any{ChannelInboxStatusProcessed, tenantID, accountID, tenantID, ChannelRunStatusCancelRequested}
	} else {
		cancelInboxArgs = []any{ChannelInboxStatusProcessed, tenantID, accountID, ChannelRunStatusCancelRequested}
	}
	if err := tx.Exec(cancelInboxQuery, cancelInboxArgs...).Error; err != nil {
		_ = tx.Rollback().Error
		return err
	}
	finishedAt := "CURRENT_TIMESTAMP(6)"
	if isSQLite(tx) {
		finishedAt = "CURRENT_TIMESTAMP"
	}
	if err := tx.Exec("UPDATE channel_runs SET status = ?, finished_at = "+finishedAt+", worker_id = NULL, heartbeat_at = NULL WHERE tenant_id = ? AND account_id = ? AND status = ?", ChannelRunStatusCancelled, tenantID, accountID, ChannelRunStatusCancelRequested).Error; err != nil {
		_ = tx.Rollback().Error
		return err
	}
	return tx.Commit().Error
}

func (r *GormRepository) updateRun(ctx context.Context, tenantID, accountID uint64, runID string, updates map[string]any) error {
	res := r.with(ctx).Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, runID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) HeartbeatChannelRun(ctx context.Context, tenantID, accountID uint64, runID, workerID string, lastEventSeq uint64) error {
	return r.updateRun(ctx, tenantID, accountID, runID, map[string]any{"heartbeat_at": time.Now().UTC(), "last_event_seq": lastEventSeq, "worker_id": workerID})
}
func (r *GormRepository) RequestCancelChannelRun(ctx context.Context, tenantID, accountID uint64, runID string) error {
	return r.updateRun(ctx, tenantID, accountID, runID, map[string]any{"status": ChannelRunStatusCancelRequested, "cancel_requested_at": channelNow(r.with(ctx))})
}

func (r *GormRepository) RequestCancelActiveChannelRun(ctx context.Context, tenantID, accountID, conversationID uint64) (bool, error) {
	if tenantID == 0 || accountID == 0 || conversationID == 0 {
		return false, ErrInvalidInput
	}
	var res *gorm.DB
	if isSQLite(r.db) {
		var row gormChannelRun
		err := r.with(ctx).Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND status IN (?, ?, ?)", tenantID, accountID, conversationID, ChannelRunStatusRunning, ChannelRunStatusQueued, ChannelRunStatusWaitingInput).Order("CASE WHEN status = '" + ChannelRunStatusRunning + "' THEN 0 WHEN status = '" + ChannelRunStatusWaitingInput + "' THEN 1 ELSE 2 END, created_at ASC, id ASC").Limit(1).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		res = r.with(ctx).Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND id = ? AND status IN (?, ?, ?)", tenantID, accountID, row.ID, ChannelRunStatusRunning, ChannelRunStatusQueued, ChannelRunStatusWaitingInput).Updates(map[string]any{"status": ChannelRunStatusCancelRequested, "cancel_requested_at": channelNow(r.with(ctx))})
	} else {
		res = r.with(ctx).Exec("UPDATE channel_runs SET status = ?, cancel_requested_at = CURRENT_TIMESTAMP(6) WHERE tenant_id = ? AND account_id = ? AND conversation_id = ? AND status IN (?, ?, ?) ORDER BY CASE WHEN status = ? THEN 0 WHEN status = ? THEN 1 ELSE 2 END, created_at ASC, id ASC LIMIT 1", ChannelRunStatusCancelRequested, tenantID, accountID, conversationID, ChannelRunStatusRunning, ChannelRunStatusQueued, ChannelRunStatusWaitingInput, ChannelRunStatusRunning, ChannelRunStatusWaitingInput)
	}
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *GormRepository) UpsertChannelReactionDesired(ctx context.Context, input ChannelReactionInput) error {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.ProviderMessageID == "" || input.DesiredEmoji == "" {
		return ErrInvalidInput
	}
	row := gormChannelReaction{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, ProviderMessageID: input.ProviderMessageID, DesiredEmoji: input.DesiredEmoji, Status: ChannelReactionStatusPending, NextAttemptAt: time.Now().UTC()}
	return r.with(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "account_id"}, {Name: "provider_message_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"desired_emoji":      input.DesiredEmoji,
			"status":             ChannelReactionStatusPending,
			"next_attempt_at":    channelNow(r.with(ctx)),
			"lease_owner":        nil,
			"lease_until":        nil,
			"last_error_code":    nil,
			"last_error_message": nil,
		}),
	}).Create(&row).Error
}

func (r *GormRepository) ClaimDueChannelReactions(ctx context.Context, tenantID, accountID uint64, workerID string, limit int, leaseUntil time.Time) ([]ChannelReaction, error) {
	if tenantID == 0 || accountID == 0 || workerID == "" {
		return nil, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	var rows []gormChannelReaction
	now := time.Now().UTC()
	query := channelForUpdateSkipLocked(tx.Table("channel_reactions").Select(channelReactionSelect).
		Where("tenant_id = ? AND account_id = ? AND ((status IN (?, ?) AND "+channelTimeColumn(r.db, "next_attempt_at")+" <= ? AND (lease_until IS NULL OR "+channelTimeColumn(r.db, "lease_until")+" < ?)) OR (status = ? AND "+channelTimeColumn(r.db, "lease_until")+" < ?))", tenantID, accountID, ChannelReactionStatusPending, ChannelReactionStatusRetry, now, now, ChannelReactionStatusReconciling, now).
		Order("next_attempt_at ASC, id ASC").Limit(normalizeLimit(limit)))
	err := query.Find(&rows).Error
	if err != nil {
		_ = tx.Rollback().Error
		return nil, err
	}
	if len(rows) == 0 {
		_ = tx.Rollback().Error
		return []ChannelReaction{}, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	err = tx.Table("channel_reactions").Where("tenant_id = ? AND account_id = ? AND id IN ?", tenantID, accountID, ids).Updates(map[string]any{"status": ChannelReactionStatusReconciling, "lease_owner": workerID, "lease_until": leaseUntil}).Error
	if err != nil {
		_ = tx.Rollback().Error
		return nil, err
	}
	if err = tx.Commit().Error; err != nil {
		return nil, err
	}
	out := make([]ChannelReaction, 0, len(rows))
	for _, row := range rows {
		row.Status, row.LeaseOwner, row.LeaseUntil = ChannelReactionStatusReconciling, &workerID, &leaseUntil
		out = append(out, channelReactionFromRow(row))
	}
	return out, nil
}

func (r *GormRepository) MarkChannelReactionApplied(ctx context.Context, tenantID, accountID, reactionRowID uint64, workerID, emoji, reactionID string) error {
	if tenantID == 0 || accountID == 0 || reactionRowID == 0 || workerID == "" || emoji == "" || reactionID == "" {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_reactions").Where("tenant_id = ? AND account_id = ? AND id = ? AND desired_emoji = ? AND status = ? AND lease_owner = ?", tenantID, accountID, reactionRowID, emoji, ChannelReactionStatusReconciling, workerID).Updates(map[string]any{"current_emoji": emoji, "reaction_id": reactionID, "status": ChannelReactionStatusApplied, "lease_owner": nil, "lease_until": nil, "last_error_code": nil, "last_error_message": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrChannelReactionSuperseded
	}
	return nil
}

func (r *GormRepository) MarkChannelReactionDeleted(ctx context.Context, tenantID, accountID, reactionRowID uint64, workerID string) error {
	return r.updateClaimedChannelReaction(ctx, tenantID, accountID, reactionRowID, workerID, map[string]any{"current_emoji": nil, "reaction_id": nil, "status": ChannelReactionStatusPending, "next_attempt_at": channelNow(r.with(ctx)), "lease_owner": nil, "lease_until": nil})
}

func (r *GormRepository) MarkChannelReactionRetry(ctx context.Context, tenantID, accountID, reactionRowID uint64, workerID string, nextAttempt time.Time, code, message string) error {
	return r.updateClaimedChannelReaction(ctx, tenantID, accountID, reactionRowID, workerID, map[string]any{"status": ChannelReactionStatusRetry, "attempts": gorm.Expr("attempts + 1"), "next_attempt_at": nextAttempt, "lease_owner": nil, "lease_until": nil, "last_error_code": channelStringPtr(code), "last_error_message": channelStringPtr(message)})
}

func (r *GormRepository) updateClaimedChannelReaction(ctx context.Context, tenantID, accountID, reactionRowID uint64, workerID string, updates map[string]any) error {
	if tenantID == 0 || accountID == 0 || reactionRowID == 0 || workerID == "" {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_reactions").Where("tenant_id = ? AND account_id = ? AND id = ? AND status = ? AND lease_owner = ?", tenantID, accountID, reactionRowID, ChannelReactionStatusReconciling, workerID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrChannelReactionSuperseded
	}
	return nil
}

func (r *GormRepository) updateChannelReaction(ctx context.Context, tenantID, accountID, reactionRowID uint64, updates map[string]any) error {
	if tenantID == 0 || accountID == 0 || reactionRowID == 0 {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_reactions").Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, reactionRowID).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) FinishChannelRun(ctx context.Context, tenantID, accountID uint64, runID, status, errorCode, errorMessage string) error {
	if status == "" {
		return ErrInvalidInput
	}
	return r.updateRun(ctx, tenantID, accountID, runID, map[string]any{"status": status, "finished_at": channelNow(r.with(ctx)), "error_code": channelStringPtr(errorCode), "error_message": channelStringPtr(errorMessage)})
}

func (r *GormRepository) MarkChannelRunWaitingInput(ctx context.Context, tenantID, accountID uint64, runID string) error {
	if tenantID == 0 || accountID == 0 || runID == "" {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND id = ? AND status = ?", tenantID, accountID, runID, ChannelRunStatusRunning).Updates(map[string]any{"status": ChannelRunStatusWaitingInput, "worker_id": nil, "heartbeat_at": nil, "finished_at": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) QueueWaitingChannelRun(ctx context.Context, tenantID, accountID uint64, runID string) error {
	if tenantID == 0 || accountID == 0 || runID == "" {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_runs").Where("tenant_id = ? AND account_id = ? AND id = ? AND status = ?", tenantID, accountID, runID, ChannelRunStatusWaitingInput).Updates(map[string]any{"status": ChannelRunStatusQueued, "worker_id": nil, "heartbeat_at": nil, "finished_at": nil, "cancel_requested_at": nil})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) CreateChannelMessage(ctx context.Context, input ChannelMessageInput) (ChannelMessage, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.IdempotencyKey == "" {
		return ChannelMessage{}, ErrInvalidInput
	}
	if input.ContentJSON == "" {
		input.ContentJSON = `{}`
	}
	if input.Status == "" {
		input.Status = ChannelMessageStatusPending
	}
	if input.Operation == "" {
		input.Operation = ChannelMessageOperationCreate
	}
	if input.RenderVersion == 0 {
		input.RenderVersion = 1
	}
	row := gormChannelMessage{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: channelStringPtr(input.RunID), Direction: input.Direction, Operation: input.Operation, ExternalMessageID: channelStringPtr(input.ExternalMessageID), InternalMessageID: channelUint64Ptr(input.InternalMessageID), IdempotencyKey: input.IdempotencyKey, ContentJSON: input.ContentJSON, RenderVersion: input.RenderVersion, LastRenderSHA256: input.LastRenderSHA256, Status: input.Status, SentAt: input.SentAt}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelMessage{}, err
	}
	return channelMessageFromRow(row), nil
}

// ResolveChannelImageCompletion returns only the durable metadata required to
// build a channel payload. MaterializeChannelImageCompletion repeats every
// ownership and readiness check under row locks before committing effects.
func (r *GormRepository) ResolveChannelImageCompletion(ctx context.Context, input ResolveChannelImageCompletionInput) (ChannelImageCompletionMaterial, error) {
	if err := validateChannelImageCompletionLookup(input); err != nil {
		return ChannelImageCompletionMaterial{}, err
	}
	material, err := resolveChannelImageCompletion(r.with(ctx), input, false)
	return material.ChannelImageCompletionMaterial, err
}

// MaterializeChannelImageCompletion makes the image message, channel outbox,
// optional terminal batch update and completion-event acknowledgement one
// atomic fact. The completion event lease is the serialization fence.
func (r *GormRepository) MaterializeChannelImageCompletion(ctx context.Context, input MaterializeChannelImageCompletionInput) error {
	if err := validateChannelImageCompletionMaterialization(input); err != nil {
		return err
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	rollback := func(err error) error { return rollbackImageTransaction(tx, err) }
	material, err := resolveChannelImageCompletion(tx, input.ResolveChannelImageCompletionInput, true)
	if err != nil {
		return rollback(err)
	}
	if material.Batch != input.ExpectedBatch {
		return rollback(ErrChannelImageCompletionNotReady)
	}
	if material.GenerationStatus != input.GenerationStatus || material.SessionID != input.SessionID {
		return rollback(ErrChannelImageCompletionNotReady)
	}

	if material.GenerationStatus == imagegen.GenerationStatusCompleted {
		message, err := insertOrLoadChannelImageMessage(tx, material, input)
		if err != nil {
			return rollback(err)
		}
		if _, err := insertOrLoadChannelImageOutbox(tx, material, message.ID, ChannelMessageOperationCreate, input.ImageIdempotencyKey, input.ImagePayloadJSON); err != nil {
			return rollback(err)
		}
	}
	if material.Batch.Terminal && material.Batch.BatchID != "" {
		if _, err := insertOrLoadChannelImageOutbox(tx, material, material.originMessageID, ChannelMessageOperationUpdate, input.SummaryIdempotencyKey, input.SummaryPayloadJSON); err != nil {
			return rollback(err)
		}
	}
	now := time.Now().UTC()
	result := tx.Model(&gormImageCompletionOutbox{}).
		Where("tenant_id = ? AND id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.TenantID, input.EventID, input.GenerationID, imagegen.CompletionDeliveryStatusSending, input.WorkerID).
		Updates(map[string]any{"status": imagegen.CompletionDeliveryStatusSent, "sent_at": now, "next_attempt_at": nil, "lease_owner": nil, "lease_until": nil, "last_error_code": nil, "last_error_message": nil})
	if result.Error != nil {
		return rollback(result.Error)
	}
	if result.RowsAffected != 1 {
		return rollback(ErrImageGenerationLeaseLost)
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	return nil
}

type resolvedChannelImageCompletion struct {
	ChannelImageCompletionMaterial
	originMessageID uint64
}

func resolveChannelImageCompletion(db *gorm.DB, input ResolveChannelImageCompletionInput, lock bool) (resolvedChannelImageCompletion, error) {
	query := func(value any) *gorm.DB {
		q := db
		if lock {
			q = channelForUpdate(q)
		}
		return q.Model(value)
	}

	var event gormImageCompletionOutbox
	if err := query(&event).Where("tenant_id = ? AND id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.TenantID, input.EventID, input.GenerationID, imagegen.CompletionDeliveryStatusSending, input.WorkerID).Take(&event).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if event.TenantID != input.TenantID || event.GenerationID != input.GenerationID || event.EventType != imagegen.CompletionEventImageTerminal || event.OriginType != imagegen.OriginTypeChannel || valueStringPtr(event.LeaseOwner) != input.WorkerID {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	persistedOrigin, err := decodeChannelImageCompletionOrigin(valueStringPtr(event.OriginRefJSON))
	if err != nil || persistedOrigin != input.Origin {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}

	var generation gormImageGeneration
	if err := query(&generation).Where("tenant_id = ? AND generation_id = ? AND status IN (?, ?, ?, ?, ?)", input.TenantID, input.GenerationID, imagegen.GenerationStatusCompleted, imagegen.GenerationStatusFailed, imagegen.GenerationStatusOutcomeUnknown, imagegen.GenerationStatusCancelled, imagegen.GenerationStatusDead).Take(&generation).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if generation.TenantID != input.TenantID || generation.GenerationID != input.GenerationID || !isImageGenerationTerminalStatus(generation.Status) || generation.OriginType != imagegen.OriginTypeChannel || generation.OriginRefJSON == nil || *generation.OriginRefJSON != *event.OriginRefJSON {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	if input.Origin.Version == channelImageOriginVersionStrict && (input.Origin.TenantID != generation.TenantID || input.Origin.UserID != generation.UserID || input.Origin.SessionID != generation.SessionID) {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	if generation.Status == imagegen.GenerationStatusCompleted && (generation.AssetID == nil || valueStringPtr(generation.AssetID) == "") {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}

	var session gormSession
	if err := query(&session).Where("tenant_id = ? AND id = ? AND user_id = ?", input.TenantID, generation.SessionID, generation.UserID).Take(&session).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if session.TenantID != input.TenantID || session.ID != generation.SessionID || session.UserID != generation.UserID {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}

	var account gormChannelAccount
	if err := query(&account).Where("tenant_id = ? AND id = ? AND provider = ? AND archived_at IS NULL", input.TenantID, input.Origin.AccountID, ChannelProviderFeishu).Take(&account).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if account.TenantID != input.TenantID || account.ID != input.Origin.AccountID || account.Provider != ChannelProviderFeishu || account.ArchivedAt != nil {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	var conversation gormChannelConversation
	if err := query(&conversation).Where("tenant_id = ? AND account_id = ? AND id = ? AND archived_at IS NULL", input.TenantID, input.Origin.AccountID, input.Origin.ConversationID).Take(&conversation).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if conversation.TenantID != input.TenantID || conversation.AccountID != input.Origin.AccountID || conversation.ID != input.Origin.ConversationID || conversation.SessionID == nil || *conversation.SessionID != generation.SessionID || conversation.Status != ChannelConversationStatusActive || conversation.ArchivedAt != nil {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	if input.Origin.Version == channelImageOriginVersionStrict && input.Origin.ThreadID != conversation.ExternalThreadID {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	var run gormChannelRun
	if err := query(&run).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND id = ?", input.TenantID, input.Origin.AccountID, input.Origin.ConversationID, input.Origin.RunID).Take(&run).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
	}
	if run.TenantID != input.TenantID || run.AccountID != input.Origin.AccountID || run.ConversationID != input.Origin.ConversationID || run.ID != input.Origin.RunID || run.SessionID == nil || *run.SessionID != generation.SessionID {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
	}
	if !terminalChannelRunStatus(run.Status) || run.FinishedAt == nil {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionNotReady
	}
	if input.Origin.Version == channelImageOriginVersionStrict {
		originInbox, err := loadChannelImageOriginInbox(db, input, lock)
		if err != nil {
			return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
		}
		if originInbox.TenantID != input.TenantID || originInbox.AccountID != input.Origin.AccountID || valueUint64(originInbox.ConversationID) != input.Origin.ConversationID || valueStringPtr(originInbox.ProviderMessageID) != input.Origin.ReplyMessageID {
			return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
		}
	}

	var asset gormMediaAsset
	if generation.Status == imagegen.GenerationStatusCompleted {
		if err := query(&asset).Where("asset_id = ? AND tenant_id = ? AND user_id = ?", *generation.AssetID, input.TenantID, generation.UserID).Take(&asset).Error; err != nil {
			return resolvedChannelImageCompletion{}, channelImageOwnershipError(err)
		}
		if asset.SessionID == nil || *asset.SessionID != generation.SessionID || asset.Kind != media.KindImage || asset.State != string(media.StateReady) || asset.ExpiresAt != nil && !asset.ExpiresAt.After(time.Now().UTC()) {
			return resolvedChannelImageCompletion{}, ErrChannelImageCompletionOwnership
		}
	}

	finalKey := "run:" + input.Origin.RunID + ":final"
	var originMessage gormChannelMessage
	if err := query(&originMessage).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND direction = ? AND operation = ? AND idempotency_key = ?", input.TenantID, input.Origin.AccountID, input.Origin.ConversationID, input.Origin.RunID, ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, finalKey).Take(&originMessage).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageReadinessError(err)
	}
	if originMessage.Status != ChannelMessageStatusSent || originMessage.ExternalMessageID == nil || valueString(originMessage.ExternalMessageID) == "" {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionNotReady
	}
	var originOutbox gormChannelOutbox
	if err := query(&originOutbox).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND message_id = ? AND operation = ? AND idempotency_key = ?", input.TenantID, input.Origin.AccountID, input.Origin.ConversationID, input.Origin.RunID, originMessage.ID, ChannelMessageOperationCreate, finalKey).Take(&originOutbox).Error; err != nil {
		return resolvedChannelImageCompletion{}, channelImageReadinessError(err)
	}
	if originOutbox.Status != ChannelOutboxStatusSent {
		return resolvedChannelImageCompletion{}, ErrChannelImageCompletionNotReady
	}

	batch, err := loadImageGenerationBatchProgress(query, input.TenantID, generation)
	if err != nil {
		return resolvedChannelImageCompletion{}, err
	}
	return resolvedChannelImageCompletion{ChannelImageCompletionMaterial: ChannelImageCompletionMaterial{
		EventID: input.EventID, TenantID: input.TenantID, GenerationID: input.GenerationID, WorkerID: input.WorkerID, Origin: input.Origin, GenerationStatus: generation.Status,
		AccountKey: account.AccountKey, ExternalChatID: conversation.ExternalChatID, ExternalThreadID: conversation.ExternalThreadID,
		UserID: generation.UserID, SessionID: generation.SessionID, AssetID: asset.AssetID, AssetMediaType: asset.MediaType,
		AssetName: valueStringPtr(asset.Name), AssetSizeBytes: asset.SizeBytes, AssetSHA256: valueStringPtr(asset.SHA256), Batch: batch,
	}, originMessageID: originMessage.ID}, nil
}

func loadChannelImageOriginInbox(db *gorm.DB, input ResolveChannelImageCompletionInput, lock bool) (gormChannelInboxEvent, error) {
	query := db.Table("channel_inbox_events AS inbox").
		Select("inbox.id, inbox.tenant_id, inbox.account_id, inbox.conversation_id, inbox.provider_message_id").
		Joins("JOIN channel_run_inputs AS run_input ON run_input.tenant_id = inbox.tenant_id AND run_input.inbox_event_id = inbox.id").
		Where("run_input.tenant_id = ? AND run_input.run_id = ? AND inbox.tenant_id = ? AND inbox.account_id = ? AND inbox.conversation_id = ? AND inbox.provider_message_id = ?", input.TenantID, input.Origin.RunID, input.TenantID, input.Origin.AccountID, input.Origin.ConversationID, input.Origin.ReplyMessageID).
		Order("run_input.sequence_no ASC")
	if lock {
		query = channelForUpdate(query)
	}
	var inbox gormChannelInboxEvent
	return inbox, query.Take(&inbox).Error
}

func decodeChannelImageCompletionOrigin(raw string) (ChannelImageCompletionOrigin, error) {
	if strings.TrimSpace(raw) == "" {
		return ChannelImageCompletionOrigin{}, ErrChannelImageCompletionOwnership
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var origin ChannelImageCompletionOrigin
	if err := decoder.Decode(&origin); err != nil {
		return ChannelImageCompletionOrigin{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return ChannelImageCompletionOrigin{}, ErrChannelImageCompletionOwnership
		}
		return ChannelImageCompletionOrigin{}, err
	}
	if !validChannelImageCompletionOrigin(origin) {
		return ChannelImageCompletionOrigin{}, ErrChannelImageCompletionOwnership
	}
	return origin, nil
}

func validChannelImageCompletionOrigin(origin ChannelImageCompletionOrigin) bool {
	if origin.AccountID == 0 || origin.ConversationID == 0 || strings.TrimSpace(origin.RunID) == "" || origin.RunID != strings.TrimSpace(origin.RunID) {
		return false
	}
	switch origin.Version {
	case channelImageOriginVersionLegacy:
		return origin.TenantID == 0 && origin.SessionID == 0 && origin.UserID == 0 && origin.ThreadID == ""
	case channelImageOriginVersionStrict:
		return origin.TenantID != 0 && origin.SessionID != 0 && origin.UserID != 0 && strings.TrimSpace(origin.ReplyMessageID) != "" && origin.ReplyMessageID == strings.TrimSpace(origin.ReplyMessageID) && origin.ThreadID == strings.TrimSpace(origin.ThreadID)
	default:
		return false
	}
}

func loadImageGenerationBatchProgress(query func(any) *gorm.DB, tenantID uint64, generation gormImageGeneration) (ImageGenerationBatchProgress, error) {
	batchID := valueStringPtr(generation.BatchID)
	if batchID == "" {
		return ImageGenerationBatchProgress{}, nil
	}
	var rows []gormImageGeneration
	if err := query(&gormImageGeneration{}).Select("id, status").Where("tenant_id = ? AND user_id = ? AND session_id = ? AND origin_type = ? AND batch_id = ?", tenantID, generation.UserID, generation.SessionID, imagegen.OriginTypeChannel, batchID).Order("id ASC").Find(&rows).Error; err != nil {
		return ImageGenerationBatchProgress{}, err
	}
	progress := ImageGenerationBatchProgress{BatchID: batchID, Total: len(rows)}
	for _, row := range rows {
		switch {
		case row.Status == imagegen.GenerationStatusCompleted:
			progress.Completed++
		case isImageGenerationFailureTerminalStatus(row.Status):
			progress.Failed++
		}
	}
	progress.Terminal = progress.Total > 0 && progress.Completed+progress.Failed == progress.Total
	return progress, nil
}

func insertOrLoadChannelImageMessage(tx *gorm.DB, material resolvedChannelImageCompletion, input MaterializeChannelImageCompletionInput) (gormChannelMessage, error) {
	var row gormChannelMessage
	query := channelForUpdate(tx).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND direction = ? AND operation = ? AND idempotency_key = ?", material.TenantID, material.Origin.AccountID, material.Origin.ConversationID, material.Origin.RunID, ChannelMessageDirectionOutbound, ChannelMessageOperationCreate, input.ImageIdempotencyKey)
	err := query.Take(&row).Error
	if err == nil {
		if !channelJSONEqual(row.ContentJSON, input.ImagePayloadJSON) {
			return gormChannelMessage{}, ErrChannelImageCompletionOwnership
		}
		return row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return gormChannelMessage{}, err
	}
	row = gormChannelMessage{TenantID: material.TenantID, AccountID: material.Origin.AccountID, ConversationID: material.Origin.ConversationID, RunID: channelStringPtr(material.Origin.RunID), Direction: ChannelMessageDirectionOutbound, Operation: ChannelMessageOperationCreate, IdempotencyKey: input.ImageIdempotencyKey, ContentJSON: input.ImagePayloadJSON, RenderVersion: 1, Status: ChannelMessageStatusPending}
	if err := tx.Create(&row).Error; err != nil {
		return gormChannelMessage{}, err
	}
	return row, nil
}

func insertOrLoadChannelImageOutbox(tx *gorm.DB, material resolvedChannelImageCompletion, messageID uint64, operation, idempotencyKey, payloadJSON string) (gormChannelOutbox, error) {
	var row gormChannelOutbox
	query := channelForUpdate(tx).Where("tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND idempotency_key = ? AND sequence_no = ? AND operation = ?", material.TenantID, material.Origin.AccountID, material.Origin.ConversationID, material.Origin.RunID, idempotencyKey, uint(1), operation)
	err := query.Take(&row).Error
	if err == nil {
		if row.MessageID != messageID || !channelJSONEqual(row.PayloadJSON, payloadJSON) {
			return gormChannelOutbox{}, ErrChannelImageCompletionOwnership
		}
		return row, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return gormChannelOutbox{}, err
	}
	now := time.Now().UTC()
	row = gormChannelOutbox{TenantID: material.TenantID, AccountID: material.Origin.AccountID, ConversationID: material.Origin.ConversationID, RunID: channelStringPtr(material.Origin.RunID), MessageID: messageID, Operation: operation, SequenceNo: 1, ChunkCount: 1, IdempotencyKey: idempotencyKey, PayloadJSON: payloadJSON, Status: ChannelOutboxStatusPending, NextAttemptAt: now}
	if err := tx.Create(&row).Error; err != nil {
		return gormChannelOutbox{}, err
	}
	return row, nil
}

func validateChannelImageCompletionLookup(input ResolveChannelImageCompletionInput) error {
	if input.EventID == 0 || input.TenantID == 0 || input.GenerationID == "" || input.WorkerID == "" || !validChannelImageCompletionOrigin(input.Origin) {
		return ErrInvalidInput
	}
	if input.Origin.Version == channelImageOriginVersionStrict && input.Origin.TenantID != input.TenantID {
		return ErrInvalidInput
	}
	return nil
}

func validateChannelImageCompletionMaterialization(input MaterializeChannelImageCompletionInput) error {
	if err := validateChannelImageCompletionLookup(input.ResolveChannelImageCompletionInput); err != nil {
		return err
	}
	if !isImageGenerationTerminalStatus(input.GenerationStatus) || input.SessionID == 0 {
		return ErrInvalidInput
	}
	if input.GenerationStatus == imagegen.GenerationStatusCompleted {
		if input.ImageIdempotencyKey != "generation:"+input.GenerationID+":channel:image" || input.ImagePayloadJSON == "" || !json.Valid([]byte(input.ImagePayloadJSON)) {
			return ErrInvalidInput
		}
	} else if input.ImageIdempotencyKey != "" || input.ImagePayloadJSON != "" {
		return ErrInvalidInput
	}
	hasSummary := input.SummaryIdempotencyKey != "" || input.SummaryPayloadJSON != ""
	wantsSummary := input.ExpectedBatch.Terminal && input.ExpectedBatch.BatchID != ""
	if hasSummary != wantsSummary {
		return ErrInvalidInput
	}
	if wantsSummary && (input.SummaryIdempotencyKey != channelImageBatchSummaryKey(input.Origin.RunID, input.SessionID, input.ExpectedBatch.BatchID) || !json.Valid([]byte(input.SummaryPayloadJSON))) {
		return ErrInvalidInput
	}
	return nil
}

func isImageGenerationTerminalStatus(status string) bool {
	return status == imagegen.GenerationStatusCompleted || isImageGenerationFailureTerminalStatus(status)
}

func channelImageBatchSummaryKey(runID string, sessionID uint64, batchID string) string {
	return fmt.Sprintf("run:%s:session:%d:batch:%s:channel:summary", runID, sessionID, batchID)
}

func terminalChannelRunStatus(status string) bool {
	switch status {
	case ChannelRunStatusCompleted, ChannelRunStatusFailed, ChannelRunStatusCancelled, ChannelRunStatusInterrupted:
		return true
	default:
		return false
	}
}

func channelImageOwnershipError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrChannelImageCompletionOwnership
	}
	return err
}

func channelImageReadinessError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrChannelImageCompletionNotReady
	}
	return err
}

func channelJSONEqual(left, right string) bool {
	var leftValue, rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func (r *GormRepository) MarkChannelMessageSent(ctx context.Context, tenantID, accountID, messageID uint64, externalMessageID string) error {
	if tenantID == 0 || accountID == 0 || messageID == 0 || externalMessageID == "" {
		return ErrInvalidInput
	}
	res := r.with(ctx).Table("channel_messages").Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, messageID).Updates(map[string]any{"external_message_id": externalMessageID, "status": ChannelMessageStatusSent, "sent_at": channelNow(r.with(ctx))})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) GetChannelMessageForDelivery(ctx context.Context, tenantID, accountID, messageID uint64) (ChannelMessage, error) {
	if tenantID == 0 || accountID == 0 || messageID == 0 {
		return ChannelMessage{}, ErrInvalidInput
	}
	var row gormChannelMessage
	if err := r.with(ctx).Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, messageID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ChannelMessage{}, ErrNotFound
		}
		return ChannelMessage{}, err
	}
	return channelMessageFromRow(row), nil
}

func (r *GormRepository) CreateOutboxMessage(ctx context.Context, input ChannelOutboxInput) (ChannelOutbox, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.MessageID == 0 || input.IdempotencyKey == "" {
		return ChannelOutbox{}, ErrInvalidInput
	}
	if input.PayloadJSON == "" {
		input.PayloadJSON = `{}`
	}
	if input.Status == "" {
		input.Status = ChannelOutboxStatusPending
	}
	if input.ChunkCount == 0 {
		input.ChunkCount = 1
	}
	if input.NextAttemptAt == nil {
		now := time.Now().UTC()
		input.NextAttemptAt = &now
	}
	row := gormChannelOutbox{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: channelStringPtr(input.RunID), MessageID: input.MessageID, Operation: input.Operation, SequenceNo: input.SequenceNo, ChunkCount: input.ChunkCount, IdempotencyKey: input.IdempotencyKey, PayloadJSON: input.PayloadJSON, RenderSHA256: input.RenderSHA256, Status: input.Status, Attempts: input.Attempts, NextAttemptAt: *input.NextAttemptAt, RetryAfterMS: channelUintPtr(input.RetryAfterMS), LeaseOwner: channelStringPtr(input.LeaseOwner), LeaseUntil: input.LeaseUntil, LastErrorCode: channelStringPtr(input.LastErrorCode), LastErrorMessage: channelStringPtr(input.LastErrorMessage), SentAt: input.SentAt}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelOutbox{}, err
	}
	return channelOutboxFromRow(row), nil
}

func (r *GormRepository) ClaimDueOutbox(ctx context.Context, tenantID, accountID uint64, workerID string, limit int, leaseUntil time.Time) ([]ChannelOutbox, error) {
	if tenantID == 0 || accountID == 0 || workerID == "" {
		return nil, ErrInvalidInput
	}
	limit = normalizeLimit(limit)
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	var rows []gormChannelOutbox
	now := time.Now().UTC()
	nextAttemptColumn := channelTimeColumn(tx, "next_attempt_at")
	leaseColumn := channelTimeColumn(tx, "lease_until")
	if err := channelForUpdate(tx.Table("channel_outbox").Select(channelOutboxSelect).Where("tenant_id = ? AND account_id = ? AND status IN (?, ?) AND "+nextAttemptColumn+" <= ? AND (lease_until IS NULL OR "+leaseColumn+" < ?)", tenantID, accountID, ChannelOutboxStatusPending, ChannelOutboxStatusRetry, now, now).Order("next_attempt_at ASC, id ASC").Limit(limit)).Find(&rows).Error; err != nil {
		tx.Rollback()
		return nil, err
	}
	if len(rows) == 0 {
		tx.Commit()
		return []ChannelOutbox{}, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	res := tx.Table("channel_outbox").Where("tenant_id = ? AND account_id = ? AND id IN ?", tenantID, accountID, ids).Updates(map[string]any{"status": ChannelOutboxStatusSending, "lease_owner": workerID, "lease_until": leaseUntil, "attempts": gorm.Expr("attempts + 1")})
	if res.Error != nil {
		tx.Rollback()
		return nil, res.Error
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = ChannelOutboxStatusSending
		rows[i].LeaseOwner = &workerID
		rows[i].LeaseUntil = &leaseUntil
		rows[i].Attempts++
	}
	out := make([]ChannelOutbox, 0, len(rows))
	for _, row := range rows {
		out = append(out, channelOutboxFromRow(row))
	}
	return out, nil
}

func (r *GormRepository) HasUnsentPriorChannelOutbox(ctx context.Context, tenantID, accountID, conversationID uint64, runID string, sequenceNo uint) (bool, error) {
	if tenantID == 0 || accountID == 0 || conversationID == 0 || strings.TrimSpace(runID) == "" || sequenceNo <= 1 {
		return false, ErrInvalidInput
	}
	var count int64
	err := r.with(ctx).Table("channel_outbox").Where(
		"tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND sequence_no < ? AND status NOT IN (?, ?)",
		tenantID, accountID, conversationID, runID, sequenceNo, ChannelOutboxStatusSent, ChannelOutboxStatusDead,
	).Count(&count).Error
	return count > 0, err
}

func (r *GormRepository) updateOutbox(ctx context.Context, tenantID, accountID, id uint64, updates map[string]any) error {
	res := r.with(ctx).Table("channel_outbox").Where("tenant_id = ? AND account_id = ? AND id = ?", tenantID, accountID, id).Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) MarkOutboxSent(ctx context.Context, tenantID, accountID, id uint64) error {
	return r.updateOutbox(ctx, tenantID, accountID, id, map[string]any{"status": ChannelOutboxStatusSent, "sent_at": channelNow(r.with(ctx)), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkOutboxRetry(ctx context.Context, tenantID, accountID, id uint64, nextAttemptAt time.Time, errorCode, errorMessage string) error {
	return r.updateOutbox(ctx, tenantID, accountID, id, map[string]any{"status": ChannelOutboxStatusRetry, "next_attempt_at": nextAttemptAt, "last_error_code": channelStringPtr(errorCode), "last_error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkOutboxDeliveryUnknown(ctx context.Context, tenantID, accountID, id uint64, errorCode, errorMessage string) error {
	return r.updateOutbox(ctx, tenantID, accountID, id, map[string]any{"status": ChannelOutboxStatusDeliveryUnknown, "last_error_code": channelStringPtr(errorCode), "last_error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}
func (r *GormRepository) MarkOutboxDead(ctx context.Context, tenantID, accountID, id uint64, errorCode, errorMessage string) error {
	return r.updateOutbox(ctx, tenantID, accountID, id, map[string]any{"status": ChannelOutboxStatusDead, "last_error_code": channelStringPtr(errorCode), "last_error_message": channelStringPtr(errorMessage), "lease_owner": nil, "lease_until": nil})
}

func (r *GormRepository) CreateCallback(ctx context.Context, input ChannelCallbackInput) (ChannelCallback, error) {
	if input.TenantID == 0 || input.AccountID == 0 || input.ConversationID == 0 || input.RunID == "" || len(input.NonceHash) == 0 || input.ExpiresAt.IsZero() {
		return ChannelCallback{}, ErrInvalidInput
	}
	if input.Status == "" {
		input.Status = ChannelCallbackStatusPending
	}
	row := gormChannelCallback{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, RunID: input.RunID, PlatformMessageID: input.PlatformMessageID, ActionID: input.ActionID, NonceHash: input.NonceHash, RuntimeFingerprint: input.RuntimeFingerprint, RuntimeFingerprintVersion: input.RuntimeFingerprintVersion, AllowedUserID: channelUint64Ptr(input.AllowedUserID), Status: input.Status, ExpiresAt: input.ExpiresAt}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ChannelCallback{}, err
	}
	return channelCallbackFromRow(row), nil
}

func (r *GormRepository) ConsumeCallback(ctx context.Context, tenantID, accountID, conversationID uint64, runID, platformMessageID, actionID string, nonceHash []byte, allowedUserID uint64, runtimeFingerprint string, runtimeFingerprintVersion int, now time.Time) (ChannelCallback, error) {
	if tenantID == 0 || accountID == 0 || conversationID == 0 || runID == "" || platformMessageID == "" || actionID == "" || len(nonceHash) == 0 || allowedUserID == 0 {
		return ChannelCallback{}, ErrInvalidInput
	}
	predicate := "tenant_id = ? AND account_id = ? AND conversation_id = ? AND run_id = ? AND platform_message_id = ? AND action_id = ? AND nonce_hash = ? AND status = ? AND expires_at > ? AND allowed_user_id = ? AND runtime_fingerprint = ? AND runtime_fingerprint_version = ?"
	args := []any{tenantID, accountID, conversationID, runID, platformMessageID, actionID, nonceHash, ChannelCallbackStatusPending, now, allowedUserID, runtimeFingerprint, runtimeFingerprintVersion}
	res := r.with(ctx).Table("channel_callbacks").Where(predicate, args...).Updates(map[string]any{"status": ChannelCallbackStatusConsumed, "consumed_at": now})
	if res.Error != nil {
		return ChannelCallback{}, res.Error
	}
	if res.RowsAffected != 1 {
		return ChannelCallback{}, ErrNotFound
	}
	var row gormChannelCallback
	if err := r.with(ctx).Table("channel_callbacks").Select(channelCallbackSelect).Where("tenant_id = ? AND account_id = ? AND nonce_hash = ? AND status = ?", tenantID, accountID, nonceHash, ChannelCallbackStatusConsumed).Limit(1).Take(&row).Error; err != nil {
		return ChannelCallback{}, err
	}
	row.Status = ChannelCallbackStatusConsumed
	row.ConsumedAt = &now
	return channelCallbackFromRow(row), nil
}

func (r *GormRepository) ExpireCallbacks(ctx context.Context, tenantID, accountID uint64, now time.Time) error {
	q := r.with(ctx).Table("channel_callbacks").Where("tenant_id = ? AND account_id = ? AND status = ? AND expires_at <= ?", tenantID, accountID, ChannelCallbackStatusPending, now).Updates(map[string]any{"status": ChannelCallbackStatusExpired})
	return q.Error
}

// Keep a compile-time assertion close to the implementation when a future
// channel service introduces its repository interface.
var _ interface {
	GetChannelAccount(context.Context, uint64, uint64) (ChannelAccount, error)
} = (*GormRepository)(nil)
