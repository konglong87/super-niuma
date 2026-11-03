package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/provisioning"
	"gorm.io/gorm"
)

type gormAgentProvisioning struct {
	ID               uint64    `gorm:"column:id;primaryKey"`
	TenantID         uint64    `gorm:"column:tenant_id"`
	ProfileKey       string    `gorm:"column:profile_key"`
	AccountKey       string    `gorm:"column:account_key"`
	CredentialRef    string    `gorm:"column:credential_ref"`
	Supervisor       string    `gorm:"column:supervisor"`
	Status           string    `gorm:"column:status"`
	WorkerSpecJSON   string    `gorm:"column:worker_spec_json"`
	WorkerStatusJSON *string   `gorm:"column:worker_status_json"`
	ChecksJSON       *string   `gorm:"column:checks_json"`
	LastErrorCode    *string   `gorm:"column:last_error_code"`
	LastErrorMessage *string   `gorm:"column:last_error_message"`
	CreatedByUserID  uint64    `gorm:"column:created_by_user_id"`
	UpdatedByUserID  uint64    `gorm:"column:updated_by_user_id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func (gormAgentProvisioning) TableName() string { return "agent_provisionings" }

// EnsureChannelAccount is the storage-side idempotent bridge between a
// provisioning record and the provider-neutral channel runtime account.
func (r *GormRepository) EnsureChannelAccount(ctx context.Context, tenantID uint64, provider, accountKey, appID, credentialRef string) (uint64, error) {
	provider, accountKey = strings.TrimSpace(provider), strings.TrimSpace(accountKey)
	if tenantID == 0 || provider == "" || accountKey == "" {
		return 0, ErrInvalidInput
	}
	var row gormChannelAccount
	lookup := r.with(ctx).Where("tenant_id = ? AND provider = ? AND account_key = ?", tenantID, provider, accountKey)
	err := lookup.Where("archived_at IS NULL").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Reuse an archived identity instead of colliding with the tenant-scoped
		// unique key. A worker restart should reactivate the same account row.
		if archivedErr := lookup.Take(&row).Error; archivedErr == nil {
			if err := r.with(ctx).Table("channel_accounts").Where("tenant_id = ? AND id = ?", tenantID, row.ID).Updates(channelAccountReadyUpdates(appID, credentialRef, true)).Error; err != nil {
				return 0, fmt.Errorf("restore channel account: %w", err)
			}
			return row.ID, nil
		}
		row = gormChannelAccount{
			TenantID: tenantID, Provider: provider, AccountKey: accountKey,
			AppID: appID, CredentialRef: credentialRef, Mode: ChannelAccountModeStream,
			Enabled: true, PolicyJSON: `{}`, Status: ChannelAccountStatusReady,
		}
		if err := r.with(ctx).Create(&row).Error; err != nil {
			// Another process may have won the create race. Re-read the
			// canonical row before surfacing a real database failure.
			if lookupErr := lookup.Where("archived_at IS NULL").Take(&row).Error; lookupErr == nil {
				if updateErr := r.with(ctx).Table("channel_accounts").Where("tenant_id = ? AND id = ?", tenantID, row.ID).Updates(channelAccountReadyUpdates(appID, credentialRef, false)).Error; updateErr != nil {
					return 0, fmt.Errorf("update channel account after create race: %w", updateErr)
				}
				return row.ID, nil
			}
			return 0, err
		}
		return row.ID, nil
	}
	if err != nil {
		return 0, err
	}
	if err := r.with(ctx).Table("channel_accounts").Where("tenant_id = ? AND id = ?", tenantID, row.ID).Updates(channelAccountReadyUpdates(appID, credentialRef, false)).Error; err != nil {
		return 0, fmt.Errorf("update channel account: %w", err)
	}
	return row.ID, nil
}

func channelAccountReadyUpdates(appID, credentialRef string, restore bool) map[string]any {
	updates := map[string]any{"enabled": true, "status": ChannelAccountStatusReady}
	if restore {
		updates["archived_at"] = nil
	}
	if strings.TrimSpace(appID) != "" {
		updates["app_id"] = strings.TrimSpace(appID)
	}
	if strings.TrimSpace(credentialRef) != "" {
		updates["credential_ref"] = strings.TrimSpace(credentialRef)
	}
	return updates
}

func (r *GormRepository) UpsertAgentProvisioning(ctx context.Context, input provisioning.RecordInput) (provisioning.Record, error) {
	if input.TenantID == 0 || input.ProfileKey == "" || input.AccountKey == "" {
		return provisioning.Record{}, ErrInvalidInput
	}
	spec, _ := json.Marshal(input.WorkerSpec)
	status, _ := json.Marshal(input.WorkerStatus)
	checks, _ := json.Marshal(input.Checks)
	row := gormAgentProvisioning{ID: input.ID, TenantID: input.TenantID, ProfileKey: input.ProfileKey, AccountKey: input.AccountKey, CredentialRef: input.CredentialRef, Supervisor: input.Supervisor, Status: input.Status, WorkerSpecJSON: string(spec), WorkerStatusJSON: provisioningStringPtr(string(status)), ChecksJSON: provisioningStringPtr(string(checks)), CreatedByUserID: input.UserID, UpdatedByUserID: input.UserID}
	if input.ID == 0 {
		if err := r.with(ctx).Create(&row).Error; err != nil {
			return provisioning.Record{}, err
		}
	} else if err := r.with(ctx).Model(&gormAgentProvisioning{}).Where("tenant_id = ? AND id = ?", input.TenantID, input.ID).Updates(map[string]any{"profile_key": input.ProfileKey, "account_key": input.AccountKey, "credential_ref": input.CredentialRef, "supervisor": input.Supervisor, "status": input.Status, "worker_spec_json": string(spec), "worker_status_json": provisioningStringPtr(string(status)), "checks_json": provisioningStringPtr(string(checks)), "last_error_code": errorCode(input.LastError), "last_error_message": errorMessage(input.LastError), "updated_by_user_id": input.UserID}).Error; err != nil {
		return provisioning.Record{}, err
	}
	return r.GetAgentProvisioning(ctx, input.TenantID, row.ID, input.AccountKey)
}

func provisioningStringPtr(value string) *string { return &value }
func (r *GormRepository) GetAgentProvisioning(ctx context.Context, tenantID, id uint64, accountKey string) (provisioning.Record, error) {
	var row gormAgentProvisioning
	q := r.with(ctx).Where("tenant_id = ?", tenantID)
	if id > 0 {
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("account_key = ?", accountKey)
	}
	if err := q.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return provisioning.Record{}, ErrNotFound
		}
		return provisioning.Record{}, err
	}
	return provisioningFromRow(row), nil
}
func (r *GormRepository) ListAgentProvisionings(ctx context.Context, tenantID uint64, limit int) ([]provisioning.Record, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows []gormAgentProvisioning
	if err := r.with(ctx).Where("tenant_id = ?", tenantID).Order("updated_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]provisioning.Record, 0, len(rows))
	for _, row := range rows {
		out = append(out, provisioningFromRow(row))
	}
	return out, nil
}
func provisioningFromRow(row gormAgentProvisioning) provisioning.Record {
	var spec provisioning.WorkerSpec
	var ws provisioning.WorkerStatus
	var checks []provisioning.HealthCheck
	_ = json.Unmarshal([]byte(row.WorkerSpecJSON), &spec)
	if spec.WorkerName == "" {
		spec.WorkerName = row.ProfileKey
	}
	if row.WorkerStatusJSON != nil {
		_ = json.Unmarshal([]byte(*row.WorkerStatusJSON), &ws)
	}
	if row.ChecksJSON != nil {
		_ = json.Unmarshal([]byte(*row.ChecksJSON), &checks)
	}
	var last *provisioning.ProvisionError
	if row.LastErrorCode != nil && *row.LastErrorCode != "" {
		last = &provisioning.ProvisionError{Code: provisioning.ErrorCode(*row.LastErrorCode), Message: valueString(row.LastErrorMessage)}
	}
	return provisioning.Record{ID: row.ID, TenantID: row.TenantID, ProfileKey: row.ProfileKey, AccountKey: row.AccountKey, CredentialRef: row.CredentialRef, Supervisor: row.Supervisor, Status: provisioning.Status(row.Status), WorkerSpec: spec, WorkerStatus: ws, Checks: checks, LastError: last, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
func errorCode(err *provisioning.ProvisionError) any {
	if err == nil {
		return nil
	}
	return string(err.Code)
}
func errorMessage(err *provisioning.ProvisionError) any {
	if err == nil {
		return nil
	}
	return err.Message
}
