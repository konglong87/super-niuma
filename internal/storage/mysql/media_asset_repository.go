package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
	"gorm.io/gorm"
)

type MediaAssetInput struct {
	AssetID         string
	TenantID        uint64
	UserID          uint64
	SessionID       uint64
	Kind            string
	MediaType       string
	Name            string
	SizeBytes       int64
	SHA256          string
	State           string
	OriginalJSON    string
	DerivativesJSON string
	AccessJSON      string
	ErrorMessage    string
	ExpiresAt       *time.Time
}

type MediaAssetRecord struct {
	AssetID         string    `json:"asset_id"`
	TenantID        uint64    `json:"tenant_id"`
	UserID          uint64    `json:"user_id"`
	SessionID       uint64    `json:"session_id,omitempty"`
	Kind            string    `json:"kind"`
	MediaType       string    `json:"media_type"`
	Name            string    `json:"name,omitempty"`
	SizeBytes       int64     `json:"size_bytes,omitempty"`
	SHA256          string    `json:"sha256,omitempty"`
	State           string    `json:"state"`
	OriginalJSON    string    `json:"original_json,omitempty"`
	DerivativesJSON string    `json:"derivatives_json,omitempty"`
	AccessJSON      string    `json:"access_json"`
	ErrorMessage    string    `json:"error,omitempty"`
	ExpiresAt       time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type gormMediaAsset struct {
	AssetID         string     `gorm:"column:asset_id;primaryKey"`
	TenantID        uint64     `gorm:"column:tenant_id"`
	UserID          uint64     `gorm:"column:user_id"`
	SessionID       *uint64    `gorm:"column:session_id"`
	Kind            string     `gorm:"column:kind"`
	MediaType       string     `gorm:"column:media_type"`
	Name            *string    `gorm:"column:name"`
	SizeBytes       int64      `gorm:"column:size_bytes"`
	SHA256          *string    `gorm:"column:sha256"`
	State           string     `gorm:"column:state"`
	OriginalJSON    *string    `gorm:"column:original_json"`
	DerivativesJSON *string    `gorm:"column:derivatives_json"`
	AccessJSON      string     `gorm:"column:access_json"`
	ErrorMessage    *string    `gorm:"column:error_message"`
	ExpiresAt       *time.Time `gorm:"column:expires_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (gormMediaAsset) TableName() string { return "media_assets" }

func (r *GormRepository) UpsertMediaAsset(ctx context.Context, input MediaAssetInput) error {
	if strings.TrimSpace(input.AssetID) == "" || input.TenantID == 0 || input.UserID == 0 || strings.TrimSpace(input.State) == "" || strings.TrimSpace(input.AccessJSON) == "" {
		return ErrInvalidInput
	}
	row := gormMediaAsset{AssetID: strings.TrimSpace(input.AssetID), TenantID: input.TenantID, UserID: input.UserID, SessionID: nullableUint64Ptr(input.SessionID), Kind: strings.TrimSpace(input.Kind), MediaType: strings.TrimSpace(input.MediaType), Name: nullableStringPtr(input.Name), SizeBytes: input.SizeBytes, SHA256: nullableStringPtr(input.SHA256), State: strings.TrimSpace(input.State), OriginalJSON: nullableStringPtr(input.OriginalJSON), DerivativesJSON: nullableStringPtr(input.DerivativesJSON), AccessJSON: input.AccessJSON, ErrorMessage: nullableStringPtr(input.ErrorMessage), ExpiresAt: input.ExpiresAt}
	return r.with(ctx).Save(&row).Error
}

// Put implements media.Store while keeping the repository's existing typed
// input API available to upload callers.
func (r *GormRepository) Put(ctx context.Context, asset media.Asset) error {
	if asset.AssetID == "" || asset.TenantID == 0 || asset.UserID == 0 || asset.State == "" {
		return ErrInvalidInput
	}
	original, err := json.Marshal(asset.Original)
	if err != nil {
		return err
	}
	derivatives, err := json.Marshal(asset.Derivatives)
	if err != nil {
		return err
	}
	access, err := json.Marshal(asset.Access)
	if err != nil {
		return err
	}
	return r.UpsertMediaAsset(ctx, MediaAssetInput{
		AssetID:         asset.AssetID,
		TenantID:        asset.TenantID,
		UserID:          asset.UserID,
		SessionID:       asset.SessionID,
		Kind:            asset.Kind,
		MediaType:       asset.MediaType,
		Name:            asset.Name,
		SizeBytes:       asset.SizeBytes,
		SHA256:          asset.SHA256,
		State:           string(asset.State),
		OriginalJSON:    string(original),
		DerivativesJSON: string(derivatives),
		AccessJSON:      string(access),
		ErrorMessage:    asset.Error,
		ExpiresAt:       nullableTimePtr(asset.ExpiresAt),
	})
}

// Get implements media.Store and enforces the same tenant/user/session scope
// as the repository's explicit GetMediaAsset method.
func (r *GormRepository) Get(ctx context.Context, policy media.AccessPolicy, assetID string) (media.Asset, error) {
	if policy.TenantID == 0 || policy.UserID == 0 || strings.TrimSpace(assetID) == "" {
		return media.Asset{}, ErrInvalidInput
	}
	record, err := r.GetMediaAsset(ctx, policy.TenantID, policy.UserID, policy.SessionID, assetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return media.Asset{}, media.ErrNotFound
		}
		return media.Asset{}, err
	}
	var original media.Variant
	if record.OriginalJSON != "" {
		if err := json.Unmarshal([]byte(record.OriginalJSON), &original); err != nil {
			return media.Asset{}, err
		}
	}
	derivatives := map[string]media.Variant{}
	if record.DerivativesJSON != "" {
		if err := json.Unmarshal([]byte(record.DerivativesJSON), &derivatives); err != nil {
			return media.Asset{}, err
		}
	}
	var access media.AccessPolicy
	if record.AccessJSON != "" {
		if err := json.Unmarshal([]byte(record.AccessJSON), &access); err != nil {
			return media.Asset{}, err
		}
	}
	if access.TenantID == 0 {
		access = media.AccessPolicy{TenantID: record.TenantID, UserID: record.UserID, SessionID: record.SessionID}
	}
	return media.Asset{
		AssetID: record.AssetID, Kind: record.Kind, MediaType: record.MediaType, Name: record.Name,
		SizeBytes: record.SizeBytes, SHA256: record.SHA256, State: media.State(record.State),
		TenantID: record.TenantID, UserID: record.UserID, SessionID: record.SessionID,
		Original: original, Derivatives: derivatives, Access: access, Error: record.ErrorMessage,
		ExpiresAt: record.ExpiresAt,
	}, nil
}

// DeleteExpired implements media.Store. The repository method returns the
// exact count as int64; the provider-neutral contract intentionally uses int.
func (r *GormRepository) DeleteExpired(ctx context.Context, now time.Time) int {
	count, err := r.DeleteExpiredMediaAssets(ctx, now, 1000)
	if err != nil {
		return 0
	}
	return int(count)
}

func nullableTimePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (r *GormRepository) GetMediaAsset(ctx context.Context, tenantID, userID, sessionID uint64, assetID string) (MediaAssetRecord, error) {
	if tenantID == 0 || userID == 0 || strings.TrimSpace(assetID) == "" {
		return MediaAssetRecord{}, ErrInvalidInput
	}
	var row gormMediaAsset
	query := r.with(ctx).Where("asset_id = ? AND tenant_id = ? AND user_id = ?", strings.TrimSpace(assetID), tenantID, userID)
	if sessionID != 0 {
		query = query.Where("(session_id IS NULL OR session_id = ?)", sessionID)
	}
	if err := query.Where("(expires_at IS NULL OR expires_at > ?)", time.Now().UTC()).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return MediaAssetRecord{}, ErrNotFound
		}
		return MediaAssetRecord{}, err
	}
	return mediaAssetRecordFromRow(row), nil
}

func (r *GormRepository) DeleteExpiredMediaAssets(ctx context.Context, now time.Time, limit int) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 {
		limit = 1000
	}
	result := r.with(ctx).Exec("DELETE FROM media_assets WHERE expires_at IS NOT NULL AND expires_at <= ? LIMIT ?", now, limit)
	return result.RowsAffected, result.Error
}

// ListTenantMediaBlobPaths returns only live, ready original blob paths for
// one tenant. Orphan cleanup must never infer references from another tenant.
func (r *GormRepository) ListTenantMediaBlobPaths(ctx context.Context, tenantID uint64) ([]string, error) {
	if tenantID == 0 {
		return nil, ErrInvalidInput
	}
	var rows []gormMediaAsset
	if err := r.with(ctx).Model(&gormMediaAsset{}).Select("original_json, derivatives_json").Where("tenant_id = ? AND state = ? AND (expires_at IS NULL OR expires_at > ?)", tenantID, string(media.StateReady), time.Now().UTC()).Find(&rows).Error; err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.OriginalJSON != nil && strings.TrimSpace(*row.OriginalJSON) != "" {
			var original media.Variant
			if err := json.Unmarshal([]byte(*row.OriginalJSON), &original); err != nil {
				return nil, err
			}
			if path := strings.TrimSpace(original.Path); path != "" {
				paths = append(paths, path)
			}
		}
		if row.DerivativesJSON == nil || strings.TrimSpace(*row.DerivativesJSON) == "" {
			continue
		}
		derivatives := map[string]media.Variant{}
		if err := json.Unmarshal([]byte(*row.DerivativesJSON), &derivatives); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(derivatives))
		for name := range derivatives {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if path := strings.TrimSpace(derivatives[name].Path); path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

func mediaAssetRecordFromRow(row gormMediaAsset) MediaAssetRecord {
	return MediaAssetRecord{AssetID: row.AssetID, TenantID: row.TenantID, UserID: row.UserID, SessionID: valueUint64Ptr(row.SessionID), Kind: row.Kind, MediaType: row.MediaType, Name: valueStringPtr(row.Name), SizeBytes: row.SizeBytes, SHA256: valueStringPtr(row.SHA256), State: row.State, OriginalJSON: valueStringPtr(row.OriginalJSON), DerivativesJSON: valueStringPtr(row.DerivativesJSON), AccessJSON: row.AccessJSON, ErrorMessage: valueStringPtr(row.ErrorMessage), ExpiresAt: valueTimePtr(row.ExpiresAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
