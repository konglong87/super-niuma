package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormAgentProfile struct {
	ID              uint64     `gorm:"column:id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	OwnerUserID     *uint64    `gorm:"column:owner_user_id"`
	OwnerKey        string     `gorm:"column:owner_key"`
	ProfileKey      string     `gorm:"column:profile_key"`
	Scope           string     `gorm:"column:scope"`
	DisplayName     string     `gorm:"column:display_name"`
	Description     *string    `gorm:"column:description"`
	ProfileVersion  uint       `gorm:"column:profile_version"`
	Status          string     `gorm:"column:status"`
	ConfigJSON      string     `gorm:"column:config_json"`
	RequestedHash   string     `gorm:"column:requested_hash"`
	EffectiveHash   string     `gorm:"column:effective_hash"`
	ValidationJSON  *string    `gorm:"column:validation_json"`
	CreatedByUserID uint64     `gorm:"column:created_by_user_id"`
	UpdatedByUserID uint64     `gorm:"column:updated_by_user_id"`
	PublishedAt     *time.Time `gorm:"column:published_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (gormAgentProfile) TableName() string { return "agent_profiles" }

type gormAgentProfileAssignment struct {
	ID               uint64    `gorm:"column:id;primaryKey"`
	TenantID         uint64    `gorm:"column:tenant_id"`
	UserID           uint64    `gorm:"column:user_id"`
	Surface          string    `gorm:"column:surface"`
	ProfileID        uint64    `gorm:"column:profile_id"`
	AssignedByUserID uint64    `gorm:"column:assigned_by_user_id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (gormAgentProfileAssignment) TableName() string { return "agent_profile_assignments" }

type gormAgentProfileChannelBinding struct {
	ID              uint64     `gorm:"column:id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	ProfileID       uint64     `gorm:"column:profile_id"`
	AccountID       uint64     `gorm:"column:account_id"`
	Provider        string     `gorm:"column:provider"`
	BindingKey      string     `gorm:"column:binding_key"`
	Status          string     `gorm:"column:status"`
	CreatedByUserID uint64     `gorm:"column:created_by_user_id"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	ArchivedAt      *time.Time `gorm:"column:archived_at"`
}

func (gormAgentProfileChannelBinding) TableName() string { return "agent_profile_channel_bindings" }

type gormAgentTeam struct {
	ID              uint64     `gorm:"column:id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	OwnerUserID     *uint64    `gorm:"column:owner_user_id"`
	OwnerKey        string     `gorm:"column:owner_key"`
	TeamKey         string     `gorm:"column:team_key"`
	TeamVersion     uint       `gorm:"column:team_version"`
	Scope           string     `gorm:"column:scope"`
	DisplayName     string     `gorm:"column:display_name"`
	Description     *string    `gorm:"column:description"`
	Status          string     `gorm:"column:status"`
	SchemaVersion   uint       `gorm:"column:schema_version"`
	PolicyJSON      string     `gorm:"column:policy_json"`
	RequestedHash   string     `gorm:"column:requested_hash"`
	EffectiveHash   string     `gorm:"column:effective_hash"`
	ValidationJSON  *string    `gorm:"column:validation_json"`
	CreatedByUserID uint64     `gorm:"column:created_by_user_id"`
	UpdatedByUserID uint64     `gorm:"column:updated_by_user_id"`
	PublishedAt     *time.Time `gorm:"column:published_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (gormAgentTeam) TableName() string { return "agent_teams" }

type gormAgentTeamMember struct {
	ID                    uint64    `gorm:"column:id;primaryKey"`
	TenantID              uint64    `gorm:"column:tenant_id"`
	TeamID                uint64    `gorm:"column:team_id"`
	MemberKey             string    `gorm:"column:member_key"`
	ProfileID             uint64    `gorm:"column:profile_id"`
	Role                  string    `gorm:"column:role"`
	AccountID             *uint64   `gorm:"column:account_id"`
	ToolPolicyJSON        *string   `gorm:"column:tool_policy_json"`
	WorkspacePolicyJSON   *string   `gorm:"column:workspace_policy_json"`
	ExecutionOverrideJSON *string   `gorm:"column:execution_override_json"`
	Status                string    `gorm:"column:status"`
	CreatedAt             time.Time `gorm:"column:created_at"`
	UpdatedAt             time.Time `gorm:"column:updated_at"`
}

func (gormAgentTeamMember) TableName() string { return "agent_team_members" }

type gormAgentTeamBinding struct {
	ID               uint64    `gorm:"column:id;primaryKey"`
	TenantID         uint64    `gorm:"column:tenant_id"`
	TeamID           uint64    `gorm:"column:team_id"`
	Provider         string    `gorm:"column:provider"`
	AccountID        uint64    `gorm:"column:account_id"`
	ExternalChatID   string    `gorm:"column:external_chat_id"`
	ExternalThreadID string    `gorm:"column:external_thread_id"`
	TriggerPolicy    string    `gorm:"column:trigger_policy"`
	Status           string    `gorm:"column:status"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (gormAgentTeamBinding) TableName() string { return "agent_team_bindings" }

type gormAgentTeamRun struct {
	ID                   string     `gorm:"column:id;primaryKey"`
	TenantID             uint64     `gorm:"column:tenant_id"`
	TeamID               uint64     `gorm:"column:team_id"`
	InboxEventID         uint64     `gorm:"column:inbox_event_id"`
	SourceAccountID      uint64     `gorm:"column:source_account_id"`
	ConversationID       *uint64    `gorm:"column:conversation_id"`
	CoordinatorMemberKey string     `gorm:"column:coordinator_member_key"`
	Status               string     `gorm:"column:status"`
	TeamEffectiveHash    string     `gorm:"column:team_effective_hash"`
	MemberCount          uint       `gorm:"column:member_count"`
	MaxRounds            uint       `gorm:"column:max_rounds"`
	MaxParallelMembers   uint       `gorm:"column:max_parallel_members"`
	MaxTotalTokens       uint       `gorm:"column:max_total_tokens"`
	UsedTokens           uint       `gorm:"column:used_tokens"`
	UsedTurns            uint       `gorm:"column:used_turns"`
	HeartbeatAt          *time.Time `gorm:"column:heartbeat_at"`
	CancelRequestedAt    *time.Time `gorm:"column:cancel_requested_at"`
	StartedAt            *time.Time `gorm:"column:started_at"`
	FinishedAt           *time.Time `gorm:"column:finished_at"`
	ResultJSON           *string    `gorm:"column:result_json"`
	ErrorCode            *string    `gorm:"column:error_code"`
	ErrorMessage         *string    `gorm:"column:error_message"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
}

func (gormAgentTeamRun) TableName() string { return "agent_team_runs" }

type gormAgentTeamMailbox struct {
	ID                uint64     `gorm:"column:id;primaryKey"`
	TenantID          uint64     `gorm:"column:tenant_id"`
	TeamRunID         string     `gorm:"column:team_run_id"`
	FromMemberKey     string     `gorm:"column:from_member_key"`
	ToMemberKey       string     `gorm:"column:to_member_key"`
	MessageKind       string     `gorm:"column:message_kind"`
	SequenceNo        uint64     `gorm:"column:sequence_no"`
	IdempotencyKey    string     `gorm:"column:idempotency_key"`
	PayloadRef        *string    `gorm:"column:payload_ref"`
	PayloadCiphertext []byte     `gorm:"column:payload_ciphertext"`
	EvidenceRef       *string    `gorm:"column:evidence_ref"`
	Status            string     `gorm:"column:status"`
	LeaseOwner        *string    `gorm:"column:lease_owner"`
	LeaseUntil        *time.Time `gorm:"column:lease_until"`
	ConsumedAt        *time.Time `gorm:"column:consumed_at"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
	UpdatedAt         time.Time  `gorm:"column:updated_at"`
}

func (gormAgentTeamMailbox) TableName() string { return "agent_team_mailbox" }

type gormAgentTeamRunEvent struct {
	ID            uint64    `gorm:"column:id;primaryKey"`
	TenantID      uint64    `gorm:"column:tenant_id"`
	TeamRunID     string    `gorm:"column:team_run_id"`
	SequenceNo    uint64    `gorm:"column:sequence_no"`
	EventType     string    `gorm:"column:event_type"`
	MemberKey     string    `gorm:"column:member_key"`
	FromMemberKey string    `gorm:"column:from_member_key"`
	ToMemberKey   string    `gorm:"column:to_member_key"`
	Status        string    `gorm:"column:status"`
	Summary       string    `gorm:"column:summary"`
	PayloadJSON   *string   `gorm:"column:payload_json"`
	ArtifactRef   *string   `gorm:"column:artifact_ref"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (gormAgentTeamRunEvent) TableName() string { return "agent_team_run_events" }

func (r *GormRepository) CreateAgentProfile(ctx context.Context, input AgentProfileInput) (AgentProfile, error) {
	if input.TenantID == 0 || strings.TrimSpace(input.ProfileKey) == "" || input.ProfileVersion == 0 || strings.TrimSpace(input.OwnerKey) == "" || input.CreatedByUserID == 0 || input.UpdatedByUserID == 0 {
		return AgentProfile{}, ErrInvalidInput
	}
	row := gormAgentProfile{
		TenantID: input.TenantID, OwnerUserID: nullableUint64Ptr(input.OwnerUserID), OwnerKey: strings.TrimSpace(input.OwnerKey),
		ProfileKey: strings.TrimSpace(input.ProfileKey), Scope: strings.TrimSpace(input.Scope), DisplayName: strings.TrimSpace(input.DisplayName),
		Description: nullableStringPtr(input.Description), ProfileVersion: input.ProfileVersion, Status: firstNonEmpty(input.Status, "draft"),
		ConfigJSON: input.ConfigJSON, RequestedHash: input.RequestedHash, EffectiveHash: input.EffectiveHash,
		ValidationJSON: nullableStringPtr(input.ValidationJSON), CreatedByUserID: input.CreatedByUserID, UpdatedByUserID: input.UpdatedByUserID, PublishedAt: input.PublishedAt,
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return AgentProfile{}, err
	}
	return agentProfileFromRow(row), nil
}

func (r *GormRepository) GetAgentProfile(ctx context.Context, tenantID, profileID uint64) (AgentProfile, error) {
	if tenantID == 0 || profileID == 0 {
		return AgentProfile{}, ErrInvalidInput
	}
	var row gormAgentProfile
	err := r.with(ctx).Where("tenant_id = ? AND id = ?", tenantID, profileID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentProfile{}, ErrNotFound
	}
	if err != nil {
		return AgentProfile{}, err
	}
	return agentProfileFromRow(row), nil
}

func (r *GormRepository) GetAgentProfileVersion(ctx context.Context, tenantID uint64, profileKey, ownerKey string, version uint) (AgentProfile, error) {
	if tenantID == 0 || strings.TrimSpace(profileKey) == "" || strings.TrimSpace(ownerKey) == "" || version == 0 {
		return AgentProfile{}, ErrInvalidInput
	}
	var row gormAgentProfile
	err := r.with(ctx).Where("tenant_id = ? AND profile_key = ? AND owner_key = ? AND profile_version = ?", tenantID, strings.TrimSpace(profileKey), strings.TrimSpace(ownerKey), version).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentProfile{}, ErrNotFound
	}
	if err != nil {
		return AgentProfile{}, err
	}
	return agentProfileFromRow(row), nil
}

func (r *GormRepository) ListAgentProfiles(ctx context.Context, tenantID, ownerUserID uint64, status string, limit int) ([]AgentProfile, error) {
	if tenantID == 0 {
		return nil, ErrInvalidInput
	}
	query := r.with(ctx).Where("tenant_id = ?", tenantID)
	if ownerUserID > 0 {
		query = query.Where("scope = 'tenant_shared' OR owner_user_id = ?", ownerUserID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var rows []gormAgentProfile
	if err := query.Order("profile_key ASC, profile_version DESC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentProfile, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentProfileFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) PublishAgentProfile(ctx context.Context, tenantID, profileID, updatedByUserID uint64, validationJSON, effectiveHash string) error {
	if tenantID == 0 || profileID == 0 || updatedByUserID == 0 || strings.TrimSpace(effectiveHash) == "" {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentProfile{}).Where("tenant_id = ? AND id = ? AND status IN ('draft', 'validating')", tenantID, profileID).Updates(map[string]any{
		"status": "published", "validation_json": nullableStringPtr(validationJSON), "effective_hash": strings.TrimSpace(effectiveHash),
		"updated_by_user_id": updatedByUserID, "published_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ArchiveAgentProfile(ctx context.Context, tenantID, profileID, updatedByUserID uint64) error {
	if tenantID == 0 || profileID == 0 || updatedByUserID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentProfile{}).Where("tenant_id = ? AND id = ? AND status <> 'archived'", tenantID, profileID).Updates(map[string]any{"status": "archived", "updated_by_user_id": updatedByUserID})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) UpsertAgentProfileAssignment(ctx context.Context, input AgentProfileAssignmentInput) (AgentProfileAssignment, error) {
	if input.TenantID == 0 || input.UserID == 0 || strings.TrimSpace(input.Surface) == "" || input.ProfileID == 0 || input.AssignedByUserID == 0 {
		return AgentProfileAssignment{}, ErrInvalidInput
	}
	row := gormAgentProfileAssignment{TenantID: input.TenantID, UserID: input.UserID, Surface: strings.TrimSpace(input.Surface), ProfileID: input.ProfileID, AssignedByUserID: input.AssignedByUserID}
	err := r.with(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "surface"}}, DoUpdates: clause.AssignmentColumns([]string{"profile_id", "assigned_by_user_id", "updated_at"})}).Create(&row).Error
	if err != nil {
		return AgentProfileAssignment{}, err
	}
	if row.ID == 0 {
		return r.GetAgentProfileAssignment(ctx, input.TenantID, input.UserID, input.Surface)
	}
	return agentProfileAssignmentFromRow(row), nil
}

func (r *GormRepository) GetAgentProfileAssignment(ctx context.Context, tenantID, userID uint64, surface string) (AgentProfileAssignment, error) {
	if tenantID == 0 || userID == 0 || strings.TrimSpace(surface) == "" {
		return AgentProfileAssignment{}, ErrInvalidInput
	}
	var row gormAgentProfileAssignment
	err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND surface = ?", tenantID, userID, strings.TrimSpace(surface)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentProfileAssignment{}, ErrNotFound
	}
	if err != nil {
		return AgentProfileAssignment{}, err
	}
	return agentProfileAssignmentFromRow(row), nil
}

func (r *GormRepository) UpsertAgentProfileChannelBinding(ctx context.Context, input AgentProfileChannelBindingInput) (AgentProfileChannelBinding, error) {
	if input.TenantID == 0 || input.ProfileID == 0 || input.AccountID == 0 || strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.BindingKey) == "" || input.CreatedByUserID == 0 {
		return AgentProfileChannelBinding{}, ErrInvalidInput
	}
	row := gormAgentProfileChannelBinding{TenantID: input.TenantID, ProfileID: input.ProfileID, AccountID: input.AccountID, Provider: strings.TrimSpace(input.Provider), BindingKey: strings.TrimSpace(input.BindingKey), Status: firstNonEmpty(input.Status, "active"), CreatedByUserID: input.CreatedByUserID}
	err := r.with(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "profile_id"}}, DoUpdates: clause.AssignmentColumns([]string{"account_id", "provider", "binding_key", "status", "created_by_user_id", "archived_at", "updated_at"})}).Create(&row).Error
	if err != nil {
		return AgentProfileChannelBinding{}, err
	}
	if row.ID == 0 {
		return r.GetAgentProfileChannelBinding(ctx, input.TenantID, input.ProfileID)
	}
	return agentProfileChannelBindingFromRow(row), nil
}

func (r *GormRepository) GetAgentProfileChannelBinding(ctx context.Context, tenantID, profileID uint64) (AgentProfileChannelBinding, error) {
	if tenantID == 0 || profileID == 0 {
		return AgentProfileChannelBinding{}, ErrInvalidInput
	}
	var row gormAgentProfileChannelBinding
	err := r.with(ctx).Where("tenant_id = ? AND profile_id = ? AND status = 'active'", tenantID, profileID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentProfileChannelBinding{}, ErrNotFound
	}
	if err != nil {
		return AgentProfileChannelBinding{}, err
	}
	return agentProfileChannelBindingFromRow(row), nil
}

func (r *GormRepository) ArchiveAgentProfileChannelBinding(ctx context.Context, tenantID, profileID uint64) error {
	if tenantID == 0 || profileID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentProfileChannelBinding{}).Where("tenant_id = ? AND profile_id = ? AND status = 'active'", tenantID, profileID).Updates(map[string]any{"status": "archived", "archived_at": gorm.Expr("CURRENT_TIMESTAMP(3)")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) CreateAgentTeam(ctx context.Context, input AgentTeamInput) (AgentTeam, error) {
	if input.TenantID == 0 || strings.TrimSpace(input.TeamKey) == "" || input.TeamVersion == 0 || strings.TrimSpace(input.OwnerKey) == "" || input.CreatedByUserID == 0 || input.UpdatedByUserID == 0 {
		return AgentTeam{}, ErrInvalidInput
	}
	row := gormAgentTeam{TenantID: input.TenantID, OwnerUserID: nullableUint64Ptr(input.OwnerUserID), OwnerKey: strings.TrimSpace(input.OwnerKey), TeamKey: strings.TrimSpace(input.TeamKey), TeamVersion: input.TeamVersion, Scope: firstNonEmpty(input.Scope, "tenant_shared"), DisplayName: strings.TrimSpace(input.DisplayName), Description: nullableStringPtr(input.Description), Status: firstNonEmpty(input.Status, "draft"), SchemaVersion: input.SchemaVersion, PolicyJSON: input.PolicyJSON, RequestedHash: input.RequestedHash, EffectiveHash: input.EffectiveHash, ValidationJSON: nullableStringPtr(input.ValidationJSON), CreatedByUserID: input.CreatedByUserID, UpdatedByUserID: input.UpdatedByUserID, PublishedAt: input.PublishedAt}
	if row.SchemaVersion == 0 {
		row.SchemaVersion = 1
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return AgentTeam{}, err
	}
	return agentTeamFromRow(row), nil
}

func (r *GormRepository) GetAgentTeam(ctx context.Context, tenantID, teamID uint64) (AgentTeam, error) {
	if tenantID == 0 || teamID == 0 {
		return AgentTeam{}, ErrInvalidInput
	}
	var row gormAgentTeam
	err := r.with(ctx).Where("tenant_id = ? AND id = ?", tenantID, teamID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTeam{}, ErrNotFound
	}
	if err != nil {
		return AgentTeam{}, err
	}
	return agentTeamFromRow(row), nil
}

func (r *GormRepository) ListAgentTeams(ctx context.Context, tenantID, ownerUserID uint64, status string, limit int) ([]AgentTeam, error) {
	if tenantID == 0 {
		return nil, ErrInvalidInput
	}
	query := r.with(ctx).Where("tenant_id = ?", tenantID)
	if ownerUserID > 0 {
		query = query.Where("scope = 'tenant_shared' OR owner_user_id = ?", ownerUserID)
	}
	if strings.TrimSpace(status) != "" {
		query = query.Where("status = ?", strings.TrimSpace(status))
	}
	var rows []gormAgentTeam
	if err := query.Order("team_key ASC, team_version DESC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeam, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) PublishAgentTeam(ctx context.Context, tenantID, teamID, updatedByUserID uint64, validationJSON, effectiveHash string) error {
	if tenantID == 0 || teamID == 0 || updatedByUserID == 0 || strings.TrimSpace(effectiveHash) == "" {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentTeam{}).Where("tenant_id = ? AND id = ? AND status IN ('draft', 'validating')", tenantID, teamID).Updates(map[string]any{"status": "published", "validation_json": nullableStringPtr(validationJSON), "effective_hash": strings.TrimSpace(effectiveHash), "updated_by_user_id": updatedByUserID, "published_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ArchiveAgentTeam(ctx context.Context, tenantID, teamID, updatedByUserID uint64) error {
	if tenantID == 0 || teamID == 0 || updatedByUserID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentTeam{}).Where("tenant_id = ? AND id = ? AND status <> 'archived'", tenantID, teamID).Updates(map[string]any{"status": "archived", "updated_by_user_id": updatedByUserID})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) ListAgentTeamRuns(ctx context.Context, tenantID, teamID uint64, limit int) ([]AgentTeamRun, error) {
	if tenantID == 0 || teamID == 0 {
		return nil, ErrInvalidInput
	}
	var rows []gormAgentTeamRun
	if err := r.with(ctx).Where("tenant_id = ? AND team_id = ?", tenantID, teamID).Order("created_at DESC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeamRun, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamRunFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) CreateAgentTeamMember(ctx context.Context, input AgentTeamMemberInput) (AgentTeamMember, error) {
	if input.TenantID == 0 || input.TeamID == 0 || strings.TrimSpace(input.MemberKey) == "" || input.ProfileID == 0 || strings.TrimSpace(input.Role) == "" {
		return AgentTeamMember{}, ErrInvalidInput
	}
	row := gormAgentTeamMember{TenantID: input.TenantID, TeamID: input.TeamID, MemberKey: strings.TrimSpace(input.MemberKey), ProfileID: input.ProfileID, Role: strings.TrimSpace(input.Role), AccountID: nullableUint64Ptr(input.AccountID), ToolPolicyJSON: nullableStringPtr(input.ToolPolicyJSON), WorkspacePolicyJSON: nullableStringPtr(input.WorkspacePolicyJSON), ExecutionOverrideJSON: nullableStringPtr(input.ExecutionOverrideJSON), Status: firstNonEmpty(input.Status, "active")}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return AgentTeamMember{}, err
	}
	return agentTeamMemberFromRow(row), nil
}

func (r *GormRepository) ArchiveAgentTeamMembers(ctx context.Context, tenantID, teamID uint64) error {
	if tenantID == 0 || teamID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentTeamMember{}).Where("tenant_id = ? AND team_id = ? AND status = 'active'", tenantID, teamID).Updates(map[string]any{"status": "archived"})
	return result.Error
}

func (r *GormRepository) ListAgentTeamMembers(ctx context.Context, tenantID, teamID uint64, limit int) ([]AgentTeamMember, error) {
	if tenantID == 0 || teamID == 0 {
		return nil, ErrInvalidInput
	}
	var rows []gormAgentTeamMember
	if err := r.with(ctx).Where("tenant_id = ? AND team_id = ? AND status = 'active'", tenantID, teamID).Order("id ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeamMember, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamMemberFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) CreateAgentTeamBinding(ctx context.Context, input AgentTeamBindingInput) (AgentTeamBinding, error) {
	if input.TenantID == 0 || input.TeamID == 0 || strings.TrimSpace(input.Provider) == "" || input.AccountID == 0 || strings.TrimSpace(input.ExternalChatID) == "" || strings.TrimSpace(input.TriggerPolicy) == "" {
		return AgentTeamBinding{}, ErrInvalidInput
	}
	row := gormAgentTeamBinding{TenantID: input.TenantID, TeamID: input.TeamID, Provider: strings.TrimSpace(input.Provider), AccountID: input.AccountID, ExternalChatID: strings.TrimSpace(input.ExternalChatID), ExternalThreadID: input.ExternalThreadID, TriggerPolicy: strings.TrimSpace(input.TriggerPolicy), Status: firstNonEmpty(input.Status, "active")}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return AgentTeamBinding{}, err
	}
	return agentTeamBindingFromRow(row), nil
}

func (r *GormRepository) ArchiveAgentTeamBindings(ctx context.Context, tenantID, teamID uint64) error {
	if tenantID == 0 || teamID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentTeamBinding{}).Where("tenant_id = ? AND team_id = ? AND status = 'active'", tenantID, teamID).Updates(map[string]any{"status": "archived"})
	return result.Error
}

func (r *GormRepository) ListAgentTeamBindings(ctx context.Context, tenantID, teamID uint64, limit int) ([]AgentTeamBinding, error) {
	if tenantID == 0 || teamID == 0 {
		return nil, ErrInvalidInput
	}
	var rows []gormAgentTeamBinding
	if err := r.with(ctx).Where("tenant_id = ? AND team_id = ? AND status = 'active'", tenantID, teamID).Order("id ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeamBinding, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamBindingFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) CreateAgentTeamRun(ctx context.Context, input AgentTeamRunInput) (AgentTeamRun, error) {
	if input.TenantID == 0 || strings.TrimSpace(input.ID) == "" || input.TeamID == 0 || input.InboxEventID == 0 || input.SourceAccountID == 0 || strings.TrimSpace(input.CoordinatorMemberKey) == "" || strings.TrimSpace(input.TeamEffectiveHash) == "" {
		return AgentTeamRun{}, ErrInvalidInput
	}
	row := gormAgentTeamRun{ID: strings.TrimSpace(input.ID), TenantID: input.TenantID, TeamID: input.TeamID, InboxEventID: input.InboxEventID, SourceAccountID: input.SourceAccountID, ConversationID: nullableUint64Ptr(input.ConversationID), CoordinatorMemberKey: strings.TrimSpace(input.CoordinatorMemberKey), Status: firstNonEmpty(input.Status, "queued"), TeamEffectiveHash: strings.TrimSpace(input.TeamEffectiveHash), MemberCount: input.MemberCount, MaxRounds: input.MaxRounds, MaxParallelMembers: input.MaxParallelMembers, MaxTotalTokens: input.MaxTotalTokens, StartedAt: input.StartedAt}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		if existing, getErr := r.GetAgentTeamRun(ctx, input.TenantID, input.ID); getErr == nil {
			return existing, nil
		}
		return AgentTeamRun{}, err
	}
	return agentTeamRunFromRow(row), nil
}

func (r *GormRepository) GetAgentTeamRun(ctx context.Context, tenantID uint64, runID string) (AgentTeamRun, error) {
	if tenantID == 0 || strings.TrimSpace(runID) == "" {
		return AgentTeamRun{}, ErrInvalidInput
	}
	var row gormAgentTeamRun
	err := r.with(ctx).Where("tenant_id = ? AND id = ?", tenantID, strings.TrimSpace(runID)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentTeamRun{}, ErrNotFound
	}
	if err != nil {
		return AgentTeamRun{}, err
	}
	return agentTeamRunFromRow(row), nil
}

func (r *GormRepository) UpdateAgentTeamRun(ctx context.Context, input AgentTeamRunUpdate) error {
	if input.TenantID == 0 || strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.Status) == "" {
		return ErrInvalidInput
	}
	updates := map[string]any{
		"status": input.Status, "used_tokens": input.UsedTokens, "used_turns": input.UsedTurns,
		"heartbeat_at": input.HeartbeatAt, "cancel_requested_at": input.CancelRequestedAt,
		"started_at": input.StartedAt, "finished_at": input.FinishedAt,
		"result_json": nullableStringPtr(input.ResultJSON), "error_code": nullableStringPtr(input.ErrorCode), "error_message": nullableStringPtr(input.ErrorMessage),
	}
	result := r.with(ctx).Model(&gormAgentTeamRun{}).Where("tenant_id = ? AND id = ?", input.TenantID, strings.TrimSpace(input.RunID)).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) AppendAgentTeamMailbox(ctx context.Context, input AgentTeamMailboxInput) (AgentTeamMailbox, error) {
	if input.TenantID == 0 || strings.TrimSpace(input.TeamRunID) == "" || strings.TrimSpace(input.FromMemberKey) == "" || strings.TrimSpace(input.ToMemberKey) == "" || strings.TrimSpace(input.MessageKind) == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return AgentTeamMailbox{}, ErrInvalidInput
	}
	row := gormAgentTeamMailbox{TenantID: input.TenantID, TeamRunID: strings.TrimSpace(input.TeamRunID), FromMemberKey: strings.TrimSpace(input.FromMemberKey), ToMemberKey: strings.TrimSpace(input.ToMemberKey), MessageKind: strings.TrimSpace(input.MessageKind), SequenceNo: input.SequenceNo, IdempotencyKey: strings.TrimSpace(input.IdempotencyKey), PayloadRef: nullableStringPtr(input.PayloadRef), PayloadCiphertext: input.PayloadCiphertext, EvidenceRef: nullableStringPtr(input.EvidenceRef), Status: firstNonEmpty(input.Status, "pending")}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		var existing gormAgentTeamMailbox
		if getErr := r.with(ctx).Where("tenant_id = ? AND team_run_id = ? AND idempotency_key = ?", input.TenantID, input.TeamRunID, input.IdempotencyKey).Take(&existing).Error; getErr == nil {
			return agentTeamMailboxFromRow(existing), nil
		}
		return AgentTeamMailbox{}, err
	}
	return agentTeamMailboxFromRow(row), nil
}

func (r *GormRepository) ListAgentTeamMailbox(ctx context.Context, tenantID uint64, runID string, limit int) ([]AgentTeamMailbox, error) {
	if tenantID == 0 || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalidInput
	}
	var rows []gormAgentTeamMailbox
	if err := r.with(ctx).Where("tenant_id = ? AND team_run_id = ?", tenantID, strings.TrimSpace(runID)).Order("sequence_no ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeamMailbox, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamMailboxFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) AppendAgentTeamRunEvent(ctx context.Context, input AgentTeamRunEventInput) (AgentTeamRunEvent, error) {
	if input.TenantID == 0 || strings.TrimSpace(input.TeamRunID) == "" || input.SequenceNo == 0 || strings.TrimSpace(input.EventType) == "" {
		return AgentTeamRunEvent{}, ErrInvalidInput
	}
	row := gormAgentTeamRunEvent{
		TenantID: input.TenantID, TeamRunID: strings.TrimSpace(input.TeamRunID), SequenceNo: input.SequenceNo,
		EventType: strings.TrimSpace(input.EventType), MemberKey: strings.TrimSpace(input.MemberKey),
		FromMemberKey: strings.TrimSpace(input.FromMemberKey), ToMemberKey: strings.TrimSpace(input.ToMemberKey),
		Status: strings.TrimSpace(input.Status), Summary: input.Summary,
		PayloadJSON: nullableStringPtr(input.PayloadJSON), ArtifactRef: nullableStringPtr(input.ArtifactRef),
	}
	if !input.CreatedAt.IsZero() {
		row.CreatedAt = input.CreatedAt
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return AgentTeamRunEvent{}, err
	}
	return agentTeamRunEventFromRow(row), nil
}

func (r *GormRepository) ListAgentTeamRunEvents(ctx context.Context, tenantID uint64, runID string, limit int) ([]AgentTeamRunEvent, error) {
	if tenantID == 0 || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalidInput
	}
	var rows []gormAgentTeamRunEvent
	if err := r.with(ctx).Where("tenant_id = ? AND team_run_id = ?", tenantID, strings.TrimSpace(runID)).Order("sequence_no ASC, id ASC").Limit(normalizeLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]AgentTeamRunEvent, 0, len(rows))
	for _, row := range rows {
		result = append(result, agentTeamRunEventFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) MarkAgentTeamMailboxConsumed(ctx context.Context, tenantID, mailboxID uint64) error {
	if tenantID == 0 || mailboxID == 0 {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormAgentTeamMailbox{}).Where("tenant_id = ? AND id = ? AND status IN ('pending', 'leased')", tenantID, mailboxID).Updates(map[string]any{"status": "consumed", "consumed_at": gorm.Expr("CURRENT_TIMESTAMP(3)"), "lease_owner": nil, "lease_until": nil})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func agentProfileFromRow(row gormAgentProfile) AgentProfile {
	return AgentProfile{ID: row.ID, TenantID: row.TenantID, OwnerUserID: valueUint64Ptr(row.OwnerUserID), OwnerKey: row.OwnerKey, ProfileKey: row.ProfileKey, Scope: row.Scope, DisplayName: row.DisplayName, Description: valueStringPtr(row.Description), ProfileVersion: row.ProfileVersion, Status: row.Status, ConfigJSON: row.ConfigJSON, RequestedHash: row.RequestedHash, EffectiveHash: row.EffectiveHash, ValidationJSON: valueStringPtr(row.ValidationJSON), CreatedByUserID: row.CreatedByUserID, UpdatedByUserID: row.UpdatedByUserID, SourceKind: "database", SourceRef: fmt.Sprintf("agent_profiles/%d", row.ID), PublishedAt: valueTimePtr(row.PublishedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func (r *GormRepository) ListAgentProfileConversationSummaries(ctx context.Context, tenantID, profileID uint64, limit int) ([]AgentProfileConversationSummary, error) {
	if tenantID == 0 || profileID == 0 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []AgentProfileConversationSummary
	query := `SELECT c.id AS conversation_id, COALESCE(c.session_id, 0) AS session_id, c.account_id, a.account_key, c.external_chat_id, c.external_thread_id, c.chat_type, c.status AS conversation_status, COALESCE(s.title, '') AS title, COALESCE(s.model, '') AS model, COALESCE(s.last_message_at, c.last_outbound_at, c.last_inbound_at, c.updated_at) AS last_message_at, COALESCE(c.last_inbound_at, c.updated_at) AS last_inbound_at, COALESCE(c.last_outbound_at, c.updated_at) AS last_outbound_at, (SELECT COUNT(*) FROM tenant_session_messages sm WHERE sm.tenant_id = c.tenant_id AND sm.session_id = c.session_id) AS message_count, (SELECT COUNT(*) FROM channel_runs cr WHERE cr.tenant_id = c.tenant_id AND cr.conversation_id = c.id) AS run_count, COALESCE((SELECT cr.status FROM channel_runs cr WHERE cr.tenant_id = c.tenant_id AND cr.conversation_id = c.id ORDER BY cr.created_at DESC LIMIT 1), '') AS latest_run_status, COALESCE((SELECT sm.role FROM tenant_session_messages sm WHERE sm.tenant_id = c.tenant_id AND sm.session_id = c.session_id ORDER BY sm.turn_index DESC, sm.id DESC LIMIT 1), '') AS last_message_role, COALESCE((SELECT LEFT(COALESCE(sm.content, sm.content_json, ''), 240) FROM tenant_session_messages sm WHERE sm.tenant_id = c.tenant_id AND sm.session_id = c.session_id ORDER BY sm.turn_index DESC, sm.id DESC LIMIT 1), '') AS last_message_preview FROM agent_profile_channel_bindings b JOIN channel_accounts a ON a.tenant_id = b.tenant_id AND a.id = b.account_id AND a.archived_at IS NULL JOIN channel_conversations c ON c.tenant_id = b.tenant_id AND c.account_id = b.account_id AND c.archived_at IS NULL LEFT JOIN tenant_sessions s ON s.tenant_id = c.tenant_id AND s.id = c.session_id WHERE b.tenant_id = ? AND b.profile_id = ? AND b.status = 'active' AND b.archived_at IS NULL ORDER BY last_message_at DESC LIMIT ?`
	if err := r.with(ctx).Raw(query, tenantID, profileID, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *GormRepository) ListAgentProfileTeamLinks(ctx context.Context, tenantID, profileID uint64, limit int) ([]AgentProfileTeamLink, error) {
	if tenantID == 0 || profileID == 0 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []AgentProfileTeamLink
	query := `SELECT t.id AS team_id, t.team_key, t.team_version, t.display_name AS team_display_name, m.member_key, m.role, COALESCE(m.account_id, 0) AS account_id, COALESCE(a.account_key, '') AS account_key, COALESCE(b.external_chat_id, '') AS external_chat_id, COALESCE(b.trigger_policy, '') AS trigger_policy, m.status FROM agent_team_members m JOIN agent_teams t ON t.tenant_id = m.tenant_id AND t.id = m.team_id AND t.status <> 'archived' LEFT JOIN channel_accounts a ON a.tenant_id = m.tenant_id AND a.id = m.account_id AND a.archived_at IS NULL LEFT JOIN agent_team_bindings b ON b.tenant_id = m.tenant_id AND b.team_id = m.team_id AND b.account_id = m.account_id AND b.status = 'active' WHERE m.tenant_id = ? AND m.profile_id = ? AND m.status = 'active' ORDER BY t.team_key, m.member_key LIMIT ?`
	if err := r.with(ctx).Raw(query, tenantID, profileID, limit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func agentProfileAssignmentFromRow(row gormAgentProfileAssignment) AgentProfileAssignment {
	return AgentProfileAssignment{ID: row.ID, TenantID: row.TenantID, UserID: row.UserID, Surface: row.Surface, ProfileID: row.ProfileID, AssignedByUserID: row.AssignedByUserID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentProfileChannelBindingFromRow(row gormAgentProfileChannelBinding) AgentProfileChannelBinding {
	return AgentProfileChannelBinding{ID: row.ID, TenantID: row.TenantID, ProfileID: row.ProfileID, AccountID: row.AccountID, Provider: row.Provider, BindingKey: row.BindingKey, Status: row.Status, CreatedByUserID: row.CreatedByUserID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: valueTimePtr(row.ArchivedAt)}
}

func agentTeamFromRow(row gormAgentTeam) AgentTeam {
	return AgentTeam{ID: row.ID, TenantID: row.TenantID, OwnerUserID: valueUint64Ptr(row.OwnerUserID), OwnerKey: row.OwnerKey, TeamKey: row.TeamKey, TeamVersion: row.TeamVersion, Scope: row.Scope, DisplayName: row.DisplayName, Description: valueStringPtr(row.Description), Status: row.Status, SchemaVersion: row.SchemaVersion, PolicyJSON: row.PolicyJSON, RequestedHash: row.RequestedHash, EffectiveHash: row.EffectiveHash, ValidationJSON: valueStringPtr(row.ValidationJSON), CreatedByUserID: row.CreatedByUserID, UpdatedByUserID: row.UpdatedByUserID, PublishedAt: valueTimePtr(row.PublishedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentTeamMemberFromRow(row gormAgentTeamMember) AgentTeamMember {
	return AgentTeamMember{ID: row.ID, TenantID: row.TenantID, TeamID: row.TeamID, MemberKey: row.MemberKey, ProfileID: row.ProfileID, Role: row.Role, AccountID: valueUint64Ptr(row.AccountID), ToolPolicyJSON: valueStringPtr(row.ToolPolicyJSON), WorkspacePolicyJSON: valueStringPtr(row.WorkspacePolicyJSON), ExecutionOverrideJSON: valueStringPtr(row.ExecutionOverrideJSON), Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentTeamBindingFromRow(row gormAgentTeamBinding) AgentTeamBinding {
	return AgentTeamBinding{ID: row.ID, TenantID: row.TenantID, TeamID: row.TeamID, Provider: row.Provider, AccountID: row.AccountID, ExternalChatID: row.ExternalChatID, ExternalThreadID: row.ExternalThreadID, TriggerPolicy: row.TriggerPolicy, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentTeamRunFromRow(row gormAgentTeamRun) AgentTeamRun {
	sourceKind := "desktop"
	if row.SourceAccountID != 0 {
		sourceKind = "feishu"
	}
	return AgentTeamRun{ID: row.ID, TenantID: row.TenantID, TeamID: row.TeamID, InboxEventID: row.InboxEventID, SourceAccountID: row.SourceAccountID, SourceKind: sourceKind, ConversationID: valueUint64Ptr(row.ConversationID), CoordinatorMemberKey: row.CoordinatorMemberKey, Status: row.Status, TeamEffectiveHash: row.TeamEffectiveHash, MemberCount: row.MemberCount, MaxRounds: row.MaxRounds, MaxParallelMembers: row.MaxParallelMembers, MaxTotalTokens: row.MaxTotalTokens, UsedTokens: row.UsedTokens, UsedTurns: row.UsedTurns, HeartbeatAt: valueTimePtr(row.HeartbeatAt), CancelRequestedAt: valueTimePtr(row.CancelRequestedAt), StartedAt: valueTimePtr(row.StartedAt), FinishedAt: valueTimePtr(row.FinishedAt), ResultJSON: valueStringPtr(row.ResultJSON), ErrorCode: valueStringPtr(row.ErrorCode), ErrorMessage: valueStringPtr(row.ErrorMessage), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentTeamMailboxFromRow(row gormAgentTeamMailbox) AgentTeamMailbox {
	return AgentTeamMailbox{ID: row.ID, TenantID: row.TenantID, TeamRunID: row.TeamRunID, FromMemberKey: row.FromMemberKey, ToMemberKey: row.ToMemberKey, MessageKind: row.MessageKind, SequenceNo: row.SequenceNo, IdempotencyKey: row.IdempotencyKey, PayloadRef: valueStringPtr(row.PayloadRef), PayloadCiphertext: row.PayloadCiphertext, EvidenceRef: valueStringPtr(row.EvidenceRef), Status: row.Status, LeaseOwner: valueStringPtr(row.LeaseOwner), LeaseUntil: valueTimePtr(row.LeaseUntil), ConsumedAt: valueTimePtr(row.ConsumedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func agentTeamRunEventFromRow(row gormAgentTeamRunEvent) AgentTeamRunEvent {
	return AgentTeamRunEvent{ID: row.ID, TenantID: row.TenantID, TeamRunID: row.TeamRunID, SequenceNo: row.SequenceNo, EventType: row.EventType, MemberKey: row.MemberKey, FromMemberKey: row.FromMemberKey, ToMemberKey: row.ToMemberKey, Status: row.Status, Summary: row.Summary, PayloadJSON: valueStringPtr(row.PayloadJSON), ArtifactRef: valueStringPtr(row.ArtifactRef), CreatedAt: row.CreatedAt}
}

func valueStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func valueUint64Ptr(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func valueTimePtr(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
