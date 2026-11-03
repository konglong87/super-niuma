package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SessionMonitorRelationObserved = "observed"
	sessionMonitorSchedulePrefix   = "session-monitor-"
)

type SessionMonitorConfigurationInput struct {
	SessionMonitorLinkInput
	ConfigurationKeyHash     string
	ConfigurationFingerprint string
}

type SessionMonitorConfigurationResult struct {
	Link     SessionLink
	Replayed bool
	Conflict bool
}

func (r *GormRepository) ResolveSessionMonitorTarget(ctx context.Context, tenantID, userID uint64, sessionKey, provider string) (SessionMonitorTarget, error) {
	if tenantID == 0 || userID == 0 || strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(provider) == "" {
		return SessionMonitorTarget{}, ErrInvalidInput
	}
	var rows []SessionMonitorTarget
	err := r.with(ctx).Table("tenant_sessions AS sessions").
		Select("sessions.id AS session_id, sessions.session_key, accounts.id AS account_id, accounts.account_key, conversations.id AS conversation_id, conversations.external_chat_id, conversations.external_thread_id").
		Joins("JOIN channel_conversations AS conversations ON conversations.tenant_id = sessions.tenant_id AND conversations.session_id = sessions.id AND conversations.archived_at IS NULL").
		Joins("JOIN channel_accounts AS accounts ON accounts.tenant_id = sessions.tenant_id AND accounts.id = conversations.account_id AND accounts.archived_at IS NULL").
		Where("sessions.tenant_id = ? AND sessions.user_id = ? AND sessions.session_key = ? AND sessions.archived_at IS NULL AND conversations.status = ? AND accounts.provider = ? AND accounts.enabled = ? AND accounts.status = ?", tenantID, userID, sessionKey, ChannelConversationStatusActive, provider, true, ChannelAccountStatusReady).
		Order("conversations.updated_at DESC, conversations.id DESC").Limit(2).Scan(&rows).Error
	if err != nil {
		return SessionMonitorTarget{}, err
	}
	if len(rows) == 0 {
		return SessionMonitorTarget{}, ErrNotFound
	}
	if len(rows) != 1 || rows[0].SessionID == 0 || rows[0].AccountID == 0 || rows[0].ConversationID == 0 || rows[0].ExternalChatID == "" {
		return SessionMonitorTarget{}, ErrInvalidState
	}
	return rows[0], nil
}

func (r *GormRepository) GetSessionMonitorLink(ctx context.Context, tenantID, userID, targetSessionID uint64, sourceKind, sourceSessionKey string) (SessionLink, error) {
	return r.GetSessionLink(ctx, tenantID, userID, targetSessionID, sourceKind, sourceSessionKey, SessionMonitorRelationObserved)
}

func (r *GormRepository) UpsertSessionMonitorLink(ctx context.Context, input SessionMonitorLinkInput) (SessionLink, error) {
	if input.TenantID == 0 || input.UserID == 0 || input.TargetSessionID == 0 || input.SourceKind == "" || input.SourceSessionKey == "" || input.RelationType != SessionMonitorRelationObserved || input.MetadataJSON == "" || !validJSONObject(input.MetadataJSON) {
		return SessionLink{}, ErrInvalidInput
	}
	id, err := upsertSessionLink(r.with(ctx), SessionLinkInput{
		TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID,
		SourceKind: input.SourceKind, SourceSessionKey: input.SourceSessionKey,
		RelationType: input.RelationType, Status: "active", MetadataJSON: input.MetadataJSON,
		CreatedByUserID: input.CreatedByUserID,
	})
	if err != nil {
		return SessionLink{}, err
	}
	var row gormSessionLink
	if err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, id).Take(&row).Error; err != nil {
		return SessionLink{}, err
	}
	return sessionLinkFromGORM(row), nil
}

// ApplySessionMonitorConfiguration serializes monitor configuration changes
// with observation commits by locking the target session and observed link.
func (r *GormRepository) ApplySessionMonitorConfiguration(ctx context.Context, input SessionMonitorConfigurationInput) (SessionMonitorConfigurationResult, error) {
	if input.TenantID == 0 || input.UserID == 0 || input.TargetSessionID == 0 || input.SourceKind == "" || input.SourceSessionKey == "" || input.RelationType != SessionMonitorRelationObserved || input.MetadataJSON == "" || !validJSONObject(input.MetadataJSON) || !validHandoffOperationIdentity(input.ConfigurationKeyHash) || !validHandoffOperationIdentity(input.ConfigurationFingerprint) {
		return SessionMonitorConfigurationResult{}, ErrInvalidInput
	}
	if _, err := sessionMonitorMetadataDocument(input.MetadataJSON, input.ConfigurationKeyHash, input.ConfigurationFingerprint); err != nil {
		return SessionMonitorConfigurationResult{}, ErrInvalidInput
	}
	var result SessionMonitorConfigurationResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var target struct {
			ID uint64 `gorm:"column:id"`
		}
		if err := tx.Table("tenant_sessions").Select("id").Where("tenant_id = ? AND user_id = ? AND id = ? AND archived_at IS NULL", input.TenantID, input.UserID, input.TargetSessionID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&target).Error; err != nil {
			return err
		}

		var existing gormSessionLink
		err := tx.Where("tenant_id = ? AND user_id = ? AND target_session_id = ? AND source_kind = ? AND source_session_key = ? AND relation_type = ?", input.TenantID, input.UserID, input.TargetSessionID, input.SourceKind, input.SourceSessionKey, SessionMonitorRelationObserved).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row := gormSessionLink{TenantID: input.TenantID, UserID: input.UserID, TargetSessionID: input.TargetSessionID, SourceKind: input.SourceKind, SourceSessionKey: input.SourceSessionKey, RelationType: SessionMonitorRelationObserved, Status: "active", MetadataJSON: nullableStringPtr(input.MetadataJSON), CreatedByUserID: input.CreatedByUserID}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			metadataJSON, metadataErr := sessionMonitorMetadataWithAuthority(input.MetadataJSON, nil, row.ID)
			if metadataErr != nil {
				return metadataErr
			}
			if err := tx.Table("tenant_session_links").Where("id = ?", row.ID).Updates(map[string]any{"metadata_json": metadataJSON, "updated_at": gorm.Expr("CURRENT_TIMESTAMP(6)")}).Error; err != nil {
				return err
			}
			row.MetadataJSON = nullableStringPtr(metadataJSON)
			result.Link = sessionLinkFromGORM(row)
			return nil
		}
		if err != nil {
			return err
		}

		existingMetadata := valueStringPtr(existing.MetadataJSON)
		storedKeyHash, storedFingerprint, metadataErr := sessionMonitorConfigurationFingerprint(existingMetadata)
		if metadataErr != nil {
			return metadataErr
		}
		if storedKeyHash == input.ConfigurationKeyHash {
			if storedFingerprint != input.ConfigurationFingerprint {
				result = SessionMonitorConfigurationResult{Link: sessionLinkFromGORM(existing), Conflict: true}
				return nil
			}
			storedMetadata, metadataErr := sessionMonitorMetadataDocument(existingMetadata, "", "")
			if metadataErr != nil {
				return metadataErr
			}
			wantScheduleID := sessionMonitorSchedulePrefix + strconv.FormatUint(existing.ID, 10)
			if storedMetadata.scheduleID != "" && storedMetadata.scheduleID != wantScheduleID {
				return ErrInvalidState
			}
			if storedMetadata.scheduleID == "" {
				metadataJSON, metadataErr := sessionMonitorMetadataWithAuthority(existingMetadata, &existingMetadata, existing.ID)
				if metadataErr != nil {
					return metadataErr
				}
				if err := tx.Table("tenant_session_links").Where("id = ?", existing.ID).Updates(map[string]any{"metadata_json": metadataJSON, "updated_at": gorm.Expr("CURRENT_TIMESTAMP(6)")}).Error; err != nil {
					return err
				}
				existing.MetadataJSON = nullableStringPtr(metadataJSON)
			}
			result = SessionMonitorConfigurationResult{Link: sessionLinkFromGORM(existing), Replayed: true}
			return nil
		}

		metadataJSON, metadataErr := sessionMonitorMetadataWithAuthority(input.MetadataJSON, &existingMetadata, existing.ID)
		if metadataErr != nil {
			return metadataErr
		}
		if err := tx.Table("tenant_session_links").Where("id = ?", existing.ID).Updates(map[string]any{"status": "active", "metadata_json": metadataJSON, "updated_at": gorm.Expr("CURRENT_TIMESTAMP(6)")}).Error; err != nil {
			return err
		}
		existing.Status = "active"
		existing.MetadataJSON = nullableStringPtr(metadataJSON)
		result.Link = sessionLinkFromGORM(existing)
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionMonitorConfigurationResult{}, ErrNotFound
	}
	return result, err
}

func sessionMonitorConfigurationFingerprint(metadataJSON string) (string, string, error) {
	metadata, err := sessionMonitorMetadataDocument(metadataJSON, "", "")
	if err != nil {
		return "", "", err
	}
	keyHash, fingerprint, ok := strings.Cut(metadata.configurationFingerprint, ".")
	if !ok || !validHandoffOperationIdentity(keyHash) || !validHandoffOperationIdentity(fingerprint) {
		return "", "", ErrInvalidState
	}
	return keyHash, fingerprint, nil
}

type sessionMonitorMetadata struct {
	configurationFingerprint string
	scheduleID               string
}

func sessionMonitorMetadataDocument(metadataJSON, wantKeyHash, wantFingerprint string) (sessionMonitorMetadata, error) {
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil || metadata == nil {
		return sessionMonitorMetadata{}, ErrInvalidState
	}
	var document sessionMonitorMetadata
	if err := json.Unmarshal(metadata["configuration_fingerprint"], &document.configurationFingerprint); err != nil || document.configurationFingerprint == "" {
		return sessionMonitorMetadata{}, ErrInvalidState
	}
	if rawScheduleID, ok := metadata["schedule_id"]; ok {
		if err := json.Unmarshal(rawScheduleID, &document.scheduleID); err != nil {
			return sessionMonitorMetadata{}, ErrInvalidState
		}
	}
	if wantKeyHash != "" && document.configurationFingerprint != wantKeyHash+"."+wantFingerprint {
		return sessionMonitorMetadata{}, ErrInvalidState
	}
	return document, nil
}

func sessionMonitorMetadataWithAuthority(inputJSON string, existingJSON *string, linkID uint64) (string, error) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inputJSON), &input); err != nil || input == nil {
		return "", ErrInvalidState
	}
	if existingJSON != nil {
		var existing map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*existingJSON), &existing); err != nil || existing == nil {
			return "", ErrInvalidState
		}
		for _, field := range []string{"schedule_id", "last_observed_cursor", "last_observed_status"} {
			if value, ok := existing[field]; ok {
				input[field] = value
			}
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	metadata, err := sessionMonitorMetadataDocument(string(encoded), "", "")
	if err != nil {
		return "", err
	}
	wantScheduleID := sessionMonitorSchedulePrefix + strconv.FormatUint(linkID, 10)
	if metadata.scheduleID == "" {
		scheduleJSON, err := json.Marshal(wantScheduleID)
		if err != nil {
			return "", err
		}
		input["schedule_id"] = scheduleJSON
	} else if metadata.scheduleID != wantScheduleID {
		return "", ErrInvalidState
	}
	encoded, err = json.Marshal(input)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (r *GormRepository) ResolveSessionMonitorByScheduleID(ctx context.Context, scheduleID string) (SessionMonitorRecord, error) {
	linkID, ok := sessionMonitorLinkIDFromScheduleID(scheduleID)
	if !ok {
		return SessionMonitorRecord{}, ErrInvalidInput
	}
	type monitorRecordRow struct {
		LinkID           uint64    `gorm:"column:link_id"`
		TenantID         uint64    `gorm:"column:tenant_id"`
		UserID           uint64    `gorm:"column:user_id"`
		TargetSessionID  uint64    `gorm:"column:target_session_id"`
		SourceKind       string    `gorm:"column:source_kind"`
		SourceSessionKey string    `gorm:"column:source_session_key"`
		SourceSessionID  uint64    `gorm:"column:source_session_id"`
		RelationType     string    `gorm:"column:relation_type"`
		LinkStatus       string    `gorm:"column:link_status"`
		MetadataJSON     string    `gorm:"column:metadata_json"`
		CreatedByUserID  uint64    `gorm:"column:created_by_user_id"`
		LinkCreatedAt    time.Time `gorm:"column:link_created_at"`
		LinkUpdatedAt    time.Time `gorm:"column:link_updated_at"`
		TenantKey        string    `gorm:"column:tenant_key"`
		UserKey          string    `gorm:"column:user_key"`
		TargetSessionKey string    `gorm:"column:target_session_key"`
		AccountID        uint64    `gorm:"column:account_id"`
		AccountKey       string    `gorm:"column:account_key"`
		ConversationID   uint64    `gorm:"column:conversation_id"`
		ExternalChatID   string    `gorm:"column:external_chat_id"`
		ExternalThreadID string    `gorm:"column:external_thread_id"`
	}
	var rows []monitorRecordRow
	err := r.with(ctx).Table("tenant_session_links AS links").
		Select("links.id AS link_id, links.tenant_id AS tenant_id, links.user_id AS user_id, links.target_session_id AS target_session_id, links.source_kind AS source_kind, links.source_session_key AS source_session_key, COALESCE(links.source_session_id, 0) AS source_session_id, links.relation_type AS relation_type, links.status AS link_status, COALESCE(links.metadata_json, '') AS metadata_json, links.created_by_user_id AS created_by_user_id, links.created_at AS link_created_at, links.updated_at AS link_updated_at, tenants.tenant_key, users.user_key, sessions.session_key AS target_session_key, accounts.id AS account_id, accounts.account_key, conversations.id AS conversation_id, conversations.external_chat_id, conversations.external_thread_id").
		Joins("JOIN tenants ON tenants.id = links.tenant_id AND tenants.deleted_at IS NULL").
		Joins("JOIN tenant_users AS users ON users.tenant_id = links.tenant_id AND users.id = links.user_id AND users.deleted_at IS NULL").
		Joins("JOIN tenant_sessions AS sessions ON sessions.tenant_id = links.tenant_id AND sessions.user_id = links.user_id AND sessions.id = links.target_session_id AND sessions.archived_at IS NULL").
		Joins("JOIN channel_conversations AS conversations ON conversations.tenant_id = links.tenant_id AND conversations.session_id = links.target_session_id AND conversations.status = ? AND conversations.archived_at IS NULL", ChannelConversationStatusActive).
		Joins("JOIN channel_accounts AS accounts ON accounts.tenant_id = links.tenant_id AND accounts.id = conversations.account_id AND accounts.provider = ? AND accounts.enabled = ? AND accounts.status = ? AND accounts.archived_at IS NULL", ChannelProviderFeishu, true, ChannelAccountStatusReady).
		Where("links.id = ? AND links.relation_type = ? AND links.status = ?", linkID, SessionMonitorRelationObserved, "active").
		Order("links.id ASC").Limit(2).Scan(&rows).Error
	if err != nil {
		return SessionMonitorRecord{}, err
	}
	if len(rows) == 0 {
		return SessionMonitorRecord{}, ErrNotFound
	}
	if len(rows) != 1 || rows[0].LinkID == 0 || rows[0].TenantKey == "" || rows[0].UserKey == "" {
		return SessionMonitorRecord{}, ErrInvalidState
	}
	row := rows[0]
	var metadata struct {
		ScheduleID string `json:"schedule_id"`
	}
	if err := json.Unmarshal([]byte(row.MetadataJSON), &metadata); err != nil || metadata.ScheduleID != scheduleID {
		return SessionMonitorRecord{}, ErrInvalidState
	}
	return SessionMonitorRecord{
		Link:      SessionLink{ID: row.LinkID, TenantID: row.TenantID, UserID: row.UserID, TargetSessionID: row.TargetSessionID, SourceKind: row.SourceKind, SourceSessionKey: row.SourceSessionKey, SourceSessionID: row.SourceSessionID, RelationType: row.RelationType, Status: row.LinkStatus, MetadataJSON: row.MetadataJSON, CreatedByUserID: row.CreatedByUserID, CreatedAt: row.LinkCreatedAt, UpdatedAt: row.LinkUpdatedAt},
		TenantKey: row.TenantKey, UserKey: row.UserKey, TargetSessionKey: row.TargetSessionKey,
		AccountID: row.AccountID, AccountKey: row.AccountKey, ConversationID: row.ConversationID,
		ExternalChatID: row.ExternalChatID, ExternalThreadID: row.ExternalThreadID,
	}, nil
}

func sessionMonitorLinkIDFromScheduleID(scheduleID string) (uint64, bool) {
	linkID := strings.TrimPrefix(scheduleID, sessionMonitorSchedulePrefix)
	if linkID == scheduleID || linkID == "" || linkID[0] == '0' {
		return 0, false
	}
	for _, character := range linkID {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(linkID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return parsed, true
}

// CommitSessionMonitorObservation makes the observation CAS and delivery
// materialization inseparable. Scheduler files are deliberately outside this
// transaction and remain a repairable projection of the observed link.
func (r *GormRepository) CommitSessionMonitorObservation(ctx context.Context, input SessionMonitorObservationInput) (SessionMonitorObservationResult, error) {
	if input.TenantID == 0 || input.UserID == 0 || input.LinkID == 0 || input.ExpectedMetadataJSON == "" || input.MetadataJSON == "" || !validJSONObject(input.MetadataJSON) || input.Deliver && (input.AccountID == 0 || input.ConversationID == 0 || input.IdempotencyKey == "" || !validJSONObject(input.PayloadJSON)) {
		return SessionMonitorObservationResult{}, ErrInvalidInput
	}
	var result SessionMonitorObservationResult
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		var link gormSessionLink
		if err := tx.Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.LinkID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&link).Error; err != nil {
			return err
		}
		current := valueStringPtr(link.MetadataJSON)
		if current != input.ExpectedMetadataJSON {
			if current == input.MetadataJSON {
				result = SessionMonitorObservationResult{Committed: true, Replayed: true}
				return nil
			}
			return ErrInvalidState
		}
		update := tx.Table("tenant_session_links").Where("tenant_id = ? AND user_id = ? AND id = ?", input.TenantID, input.UserID, input.LinkID).Updates(map[string]any{"metadata_json": input.MetadataJSON, "updated_at": gorm.Expr("CURRENT_TIMESTAMP(6)")})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrInvalidState
		}
		if !input.Deliver {
			result.Committed = true
			return nil
		}
		message := gormChannelMessage{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, Direction: ChannelMessageDirectionOutbound, Operation: ChannelMessageOperationCreate, IdempotencyKey: input.IdempotencyKey, ContentJSON: input.PayloadJSON, RenderVersion: 1, Status: ChannelMessageStatusPending}
		if err := tx.Create(&message).Error; err != nil {
			return err
		}
		outbox := gormChannelOutbox{TenantID: input.TenantID, AccountID: input.AccountID, ConversationID: input.ConversationID, MessageID: message.ID, Operation: ChannelMessageOperationCreate, SequenceNo: 1, ChunkCount: 1, IdempotencyKey: input.IdempotencyKey, PayloadJSON: input.PayloadJSON, Status: ChannelOutboxStatusPending, NextAttemptAt: time.Now().UTC()}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}
		result = SessionMonitorObservationResult{Committed: true, MessageID: message.ID, OutboxID: outbox.ID}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return SessionMonitorObservationResult{}, ErrNotFound
	}
	return result, err
}
