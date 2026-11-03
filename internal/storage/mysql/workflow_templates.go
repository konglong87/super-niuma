package mysql

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"gorm.io/gorm"
)

const (
	workflowPrivateScope       = "user_private"
	workflowPublishedStatus    = "published"
	workflowActiveMemberStatus = "active"
)

// CreateWorkflowTeam persists an already compiled, fixed-version private Team
// and its complete membership atomically. Compilation and version selection
// belong to the caller; the existing unique version key arbitrates races.
func (r *GormRepository) CreateWorkflowTeam(ctx context.Context, input AgentTeamInput, members []AgentTeamMemberInput) (AgentTeam, error) {
	if err := normalizeWorkflowTeam(&input, members); err != nil {
		return AgentTeam{}, err
	}
	var team AgentTeam
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user gormTenantUser
		if err := tx.Select("id").Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", input.TenantID, input.OwnerUserID).Take(&user).Error; err != nil {
			return workflowReadError(err)
		}
		// Use existing row constructors on the transaction, not new SQL models.
		scoped := &GormRepository{db: tx, logger: r.logger}
		created, err := scoped.CreateAgentTeam(ctx, input)
		if err != nil {
			if isWorkflowDuplicate(err) {
				return ErrInvalidInput
			}
			return err
		}
		for _, member := range members {
			var profile gormAgentProfile
			if err := tx.Select("id").Where("tenant_id = ? AND id = ? AND status = ? AND (scope IN ? OR owner_user_id = ?)", input.TenantID, member.ProfileID, workflowPublishedStatus, []string{"builtin", "tenant_shared"}, input.OwnerUserID).Take(&profile).Error; err != nil {
				return workflowReadError(err)
			}
			member.TenantID = input.TenantID
			member.TeamID = created.ID
			member.Status = workflowActiveMemberStatus
			if _, err := scoped.CreateAgentTeamMember(ctx, member); err != nil {
				if isWorkflowDuplicate(err) {
					return ErrInvalidInput
				}
				return err
			}
		}
		team = created
		return nil
	})
	if err != nil {
		return AgentTeam{}, err
	}
	return team, nil
}

func normalizeWorkflowTeam(input *AgentTeamInput, members []AgentTeamMemberInput) error {
	input.TeamKey = strings.TrimSpace(input.TeamKey)
	input.OwnerKey = strings.TrimSpace(input.OwnerKey)
	if input.TenantID == 0 || input.OwnerUserID == 0 || input.TeamKey == "" || input.OwnerKey == "" || input.TeamVersion == 0 || strings.TrimSpace(input.EffectiveHash) == "" || !json.Valid([]byte(input.PolicyJSON)) || len(members) == 0 || len(members) > workflow.MaxNodes {
		return ErrInvalidInput
	}
	if input.Scope != "" && input.Scope != workflowPrivateScope {
		return ErrInvalidInput
	}
	if input.SchemaVersion != 0 && input.SchemaVersion != workflow.SchemaVersion {
		return ErrInvalidInput
	}
	if input.CreatedByUserID != 0 && input.CreatedByUserID != input.OwnerUserID {
		return ErrInvalidInput
	}
	if input.UpdatedByUserID != 0 && input.UpdatedByUserID != input.OwnerUserID {
		return ErrInvalidInput
	}
	input.Scope = workflowPrivateScope
	input.Status = workflowPublishedStatus
	input.SchemaVersion = workflow.SchemaVersion
	input.CreatedByUserID = input.OwnerUserID
	input.UpdatedByUserID = input.OwnerUserID
	if input.RequestedHash == "" {
		input.RequestedHash = input.EffectiveHash
	}
	now := time.Now().UTC()
	input.PublishedAt = &now
	seen := map[string]bool{}
	for _, member := range members {
		key := strings.TrimSpace(member.MemberKey)
		if key == "" || seen[key] || member.ProfileID == 0 || strings.TrimSpace(member.Role) == "" || member.AccountID != 0 || member.TeamID != 0 || (member.TenantID != 0 && member.TenantID != input.TenantID) {
			return ErrInvalidInput
		}
		seen[key] = true
	}
	return nil
}
