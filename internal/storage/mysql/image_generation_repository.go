package mysql

import (
	"context"
	"errors"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/konglong87/super-niuma/internal/imagegen"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrImageGenerationLeaseLost = imagegen.ErrImageGenerationLeaseLost

var _ imagegen.SchedulerRepository = (*GormRepository)(nil)

type ImageJobRepository interface {
	EnqueueImageGeneration(context.Context, ImageGenerationInput) (ImageGenerationRecord, bool, error)
	AdmitImageGeneration(context.Context, imagegen.GenerationRecord, imagegen.QueueLimits) (imagegen.GenerationRecord, bool, error)
	ClaimDueImageGenerations(context.Context, uint64, string, int, time.Time) ([]imagegen.GenerationRecord, error)
	CountLegacyOrphanedImageGenerations(context.Context, uint64, time.Time) (int, error)
	FailLegacyOrphanedImageGenerations(context.Context, uint64, time.Time) (int, error)
	FinalizeExhaustedImageGenerations(context.Context, uint64, time.Time) (int, error)
	RenewImageGenerationLease(context.Context, uint64, string, string, time.Time) (bool, error)
	TransitionClaimedImageAttempt(context.Context, imagegen.ImageGenerationAttemptTransition) (string, error)
	CompleteClaimedImageAttempt(context.Context, imagegen.CompleteClaimedImageAttemptRequest) (string, error)
	ScheduleImageGenerationRetry(context.Context, uint64, string, string, time.Time, string, string) error
	FinalizeImageGeneration(context.Context, uint64, string, string, string, string, string, string) error
	RequestImageGenerationCancel(context.Context, uint64, uint64, uint64, string) (bool, error)
	RetryImageGeneration(context.Context, imagegen.ManualRetryRequest) (imagegen.GenerationRecord, bool, error)
	FinishImageGenerationAttempt(context.Context, imagegen.ImageGenerationAttemptFinish) error
	CompleteImageGeneration(context.Context, imagegen.CompleteImageGenerationRequest) error
}

type ImageCompletionOutboxRepository interface {
	ClaimDueImageCompletionEvents(context.Context, uint64, string, int, time.Time) ([]ImageCompletionOutboxRecord, error)
	MarkImageCompletionEventRetry(context.Context, uint64, uint64, string, time.Time, string, string) error
	MarkImageCompletionEventSent(context.Context, uint64, uint64, string) error
	MarkImageCompletionEventDead(context.Context, uint64, uint64, string, string, string) error
}

type ImageGenerationInput struct {
	ID                  uint64
	GenerationID        string
	TenantID            uint64
	UserID              uint64
	SessionID           uint64
	AssetID             string
	SourceAssetID       string
	Operation           string
	Status              string
	Prompt              string
	Provider            string
	Model               string
	RequestJSON         string
	ErrorCode           string
	ErrorMessage        string
	TraceID             string
	IdempotencyKey      string
	BatchID             string
	OriginType          string
	OriginRefJSON       string
	ToolUseID           string
	Attempts            uint
	MaxAttempts         uint
	NextAttemptAt       *time.Time
	LeaseOwner          string
	LeaseUntil          *time.Time
	HeartbeatAt         *time.Time
	StartedAt           *time.Time
	CancelRequestedAt   *time.Time
	ProviderRequestID   string
	RetryOfGenerationID string
	ErrorClass          string
	UpdatedAt           time.Time
	CreatedAt           time.Time
	FinishedAt          *time.Time
}

type ImageGenerationRecord struct {
	ID                  uint64     `json:"id"`
	GenerationID        string     `json:"generation_id"`
	TenantID            uint64     `json:"tenant_id"`
	UserID              uint64     `json:"user_id"`
	SessionID           uint64     `json:"session_id"`
	AssetID             string     `json:"asset_id,omitempty"`
	SourceAssetID       string     `json:"source_asset_id,omitempty"`
	Operation           string     `json:"operation"`
	Status              string     `json:"status"`
	Prompt              string     `json:"prompt"`
	Provider            string     `json:"provider"`
	Model               string     `json:"model"`
	RequestJSON         string     `json:"request_json,omitempty"`
	ErrorCode           string     `json:"error_code,omitempty"`
	ErrorMessage        string     `json:"error_message,omitempty"`
	TraceID             string     `json:"trace_id,omitempty"`
	IdempotencyKey      string     `json:"idempotency_key,omitempty"`
	BatchID             string     `json:"batch_id,omitempty"`
	OriginType          string     `json:"origin_type,omitempty"`
	OriginRefJSON       string     `json:"origin_ref_json,omitempty"`
	ToolUseID           string     `json:"tool_use_id,omitempty"`
	Attempts            uint       `json:"attempts,omitempty"`
	MaxAttempts         uint       `json:"max_attempts,omitempty"`
	NextAttemptAt       *time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner          string     `json:"lease_owner,omitempty"`
	LeaseUntil          *time.Time `json:"lease_until,omitempty"`
	HeartbeatAt         *time.Time `json:"heartbeat_at,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CancelRequestedAt   *time.Time `json:"cancel_requested_at,omitempty"`
	ProviderRequestID   string     `json:"provider_request_id,omitempty"`
	RetryOfGenerationID string     `json:"retry_of_generation_id,omitempty"`
	ErrorClass          string     `json:"error_class,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	FinishedAt          *time.Time `json:"finished_at,omitempty"`
}

type gormImageGeneration struct {
	ID                  uint64     `gorm:"column:id;primaryKey"`
	GenerationID        string     `gorm:"column:generation_id"`
	TenantID            uint64     `gorm:"column:tenant_id"`
	UserID              uint64     `gorm:"column:user_id"`
	SessionID           uint64     `gorm:"column:session_id"`
	AssetID             *string    `gorm:"column:asset_id"`
	SourceAssetID       *string    `gorm:"column:source_asset_id"`
	Operation           string     `gorm:"column:operation"`
	Status              string     `gorm:"column:status"`
	Prompt              string     `gorm:"column:prompt"`
	Provider            string     `gorm:"column:provider"`
	Model               string     `gorm:"column:model"`
	RequestJSON         *string    `gorm:"column:request_json"`
	ErrorCode           *string    `gorm:"column:error_code"`
	ErrorMessage        *string    `gorm:"column:error_message"`
	TraceID             *string    `gorm:"column:trace_id"`
	IdempotencyKey      *string    `gorm:"column:idempotency_key"`
	BatchID             *string    `gorm:"column:batch_id"`
	OriginType          string     `gorm:"column:origin_type"`
	OriginRefJSON       *string    `gorm:"column:origin_ref_json"`
	ToolUseID           *string    `gorm:"column:tool_use_id"`
	Attempts            uint       `gorm:"column:attempts"`
	MaxAttempts         uint       `gorm:"column:max_attempts"`
	NextAttemptAt       *time.Time `gorm:"column:next_attempt_at"`
	LeaseOwner          *string    `gorm:"column:lease_owner"`
	LeaseUntil          *time.Time `gorm:"column:lease_until"`
	HeartbeatAt         *time.Time `gorm:"column:heartbeat_at"`
	StartedAt           *time.Time `gorm:"column:started_at"`
	CancelRequestedAt   *time.Time `gorm:"column:cancel_requested_at"`
	ProviderRequestID   *string    `gorm:"column:provider_request_id"`
	RetryOfGenerationID *string    `gorm:"column:retry_of_generation_id"`
	ErrorClass          *string    `gorm:"column:error_class"`
	UpdatedAt           time.Time  `gorm:"column:updated_at"`
	CreatedAt           time.Time  `gorm:"column:created_at"`
	FinishedAt          *time.Time `gorm:"column:finished_at"`
}

func (gormImageGeneration) TableName() string { return "image_generations" }

type ImageGenerationAttemptFinishInput = imagegen.ImageGenerationAttemptFinish

type gormImageGenerationAttempt struct {
	ID                uint64     `gorm:"column:id;primaryKey"`
	GenerationID      string     `gorm:"column:generation_id"`
	TenantID          uint64     `gorm:"column:tenant_id"`
	AttemptNo         uint       `gorm:"column:attempt_no"`
	WorkerID          string     `gorm:"column:worker_id"`
	Provider          string     `gorm:"column:provider"`
	Model             string     `gorm:"column:model"`
	StartedAt         time.Time  `gorm:"column:started_at"`
	FinishedAt        *time.Time `gorm:"column:finished_at"`
	DurationMS        *uint64    `gorm:"column:duration_ms"`
	ProviderRequestID *string    `gorm:"column:provider_request_id"`
	Outcome           *string    `gorm:"column:outcome"`
	ErrorClass        *string    `gorm:"column:error_class"`
	ErrorMessage      *string    `gorm:"column:error_message"`
	CreatedAt         time.Time  `gorm:"column:created_at"`
}

func (gormImageGenerationAttempt) TableName() string { return "image_generation_attempts" }

type CompleteImageGenerationInput = imagegen.CompleteImageGenerationRequest

type ImageCompletionOutboxRecord = imagegen.CompletionEvent

type ImageGenerationQueueStats struct {
	QueueDepth     int64
	Running        int64
	OldestQueuedAt *time.Time
}

type gormImageCompletionOutbox struct {
	ID               uint64     `gorm:"column:id;primaryKey"`
	TenantID         uint64     `gorm:"column:tenant_id"`
	GenerationID     string     `gorm:"column:generation_id"`
	EventType        string     `gorm:"column:event_type"`
	OriginType       string     `gorm:"column:origin_type"`
	OriginRefJSON    *string    `gorm:"column:origin_ref_json"`
	IdempotencyKey   string     `gorm:"column:idempotency_key"`
	Status           string     `gorm:"column:status"`
	Attempts         uint       `gorm:"column:attempts"`
	NextAttemptAt    *time.Time `gorm:"column:next_attempt_at"`
	LeaseOwner       *string    `gorm:"column:lease_owner"`
	LeaseUntil       *time.Time `gorm:"column:lease_until"`
	LastErrorCode    *string    `gorm:"column:last_error_code"`
	LastErrorMessage *string    `gorm:"column:last_error_message"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	SentAt           *time.Time `gorm:"column:sent_at"`
}

func (gormImageCompletionOutbox) TableName() string { return "image_completion_outbox" }

func (r *GormRepository) CreateImageGeneration(ctx context.Context, input ImageGenerationInput) (ImageGenerationRecord, error) {
	if err := validateImageGenerationInput(input); err != nil {
		return ImageGenerationRecord{}, err
	}
	row := imageGenerationRowFromInput(input)
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	if err := r.with(ctx).Create(&row).Error; err != nil {
		return ImageGenerationRecord{}, err
	}
	return imageGenerationRecordFromRow(row), nil
}

// EnqueueImageGeneration creates an asynchronous job once per caller-owned
// idempotency key. A duplicate receipt is returned without changing its state.
func (r *GormRepository) EnqueueImageGeneration(ctx context.Context, input ImageGenerationInput) (ImageGenerationRecord, bool, error) {
	if err := validateAsyncImageGenerationInput(input); err != nil {
		return ImageGenerationRecord{}, false, err
	}
	if existing, err := r.FindImageGenerationByIdempotency(ctx, input.TenantID, input.UserID, input.SessionID, input.IdempotencyKey); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return ImageGenerationRecord{}, false, err
	}
	row := imageGenerationRowFromInput(input)
	row.Status = imagegen.GenerationStatusQueued
	row.IdempotencyKey = nullableStringPtr(input.IdempotencyKey)
	row.OriginType = firstImageString(input.OriginType, imagegen.OriginTypeDirect)
	row.MaxAttempts = input.MaxAttempts
	if row.MaxAttempts == 0 {
		row.MaxAttempts = 3
	}
	if row.NextAttemptAt == nil {
		now := time.Now().UTC()
		row.NextAttemptAt = &now
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	result := r.with(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "session_id"}, {Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		if isMySQLDuplicateKey(result.Error) {
			existing, err := r.FindImageGenerationByIdempotency(ctx, input.TenantID, input.UserID, input.SessionID, input.IdempotencyKey)
			if err != nil {
				return ImageGenerationRecord{}, false, err
			}
			return existing, false, nil
		}
		return ImageGenerationRecord{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return imageGenerationRecordFromRow(row), true, nil
	}
	existing, err := r.FindImageGenerationByIdempotency(ctx, input.TenantID, input.UserID, input.SessionID, input.IdempotencyKey)
	if err != nil {
		return ImageGenerationRecord{}, false, err
	}
	return existing, false, nil
}

// AdmitImageGeneration serializes all tenant queue admissions on the durable
// tenant row. The idempotency read, active queue limits and insert therefore
// share one short transaction across scheduler processes.
func (r *GormRepository) AdmitImageGeneration(ctx context.Context, record imagegen.GenerationRecord, limits imagegen.QueueLimits) (imagegen.GenerationRecord, bool, error) {
	input := imageGenerationInputFromDomain(record)
	if err := validateAsyncImageGenerationInput(input); err != nil {
		return imagegen.GenerationRecord{}, false, err
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return imagegen.GenerationRecord{}, false, tx.Error
	}
	rollback := func(err error) (imagegen.GenerationRecord, bool, error) {
		return imagegen.GenerationRecord{}, false, rollbackImageTransaction(tx, err)
	}
	var tenant gormTenant
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", input.TenantID).Take(&tenant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rollback(ErrNotFound)
		}
		return rollback(err)
	}
	var existing gormImageGeneration
	err := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND idempotency_key = ?", input.TenantID, input.UserID, input.SessionID, strings.TrimSpace(input.IdempotencyKey)).Take(&existing).Error
	if err == nil {
		if err := tx.Commit().Error; err != nil {
			return imagegen.GenerationRecord{}, false, err
		}
		return imageGenerationDomainRecord(imageGenerationRecordFromRow(existing)), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(err)
	}
	if limits.MaxQueuedPerTenant > 0 {
		count, err := countActiveImageGenerations(tx, input.TenantID, 0)
		if err != nil {
			return rollback(err)
		}
		if count >= int64(limits.MaxQueuedPerTenant) {
			if err := tx.Commit().Error; err != nil {
				return imagegen.GenerationRecord{}, false, err
			}
			return imagegen.GenerationRecord{}, false, imagegen.ErrImageQueueFull
		}
	}
	if limits.MaxQueuedPerUser > 0 {
		count, err := countActiveImageGenerations(tx, input.TenantID, input.UserID)
		if err != nil {
			return rollback(err)
		}
		if count >= int64(limits.MaxQueuedPerUser) {
			if err := tx.Commit().Error; err != nil {
				return imagegen.GenerationRecord{}, false, err
			}
			return imagegen.GenerationRecord{}, false, imagegen.ErrImageQueueFull
		}
	}
	row := imageGenerationRowFromInput(input)
	row.Status = imagegen.GenerationStatusQueued
	row.IdempotencyKey = nullableStringPtr(input.IdempotencyKey)
	row.OriginType = firstImageString(input.OriginType, imagegen.OriginTypeDirect)
	if row.MaxAttempts == 0 {
		row.MaxAttempts = 3
	}
	if row.NextAttemptAt == nil {
		now := time.Now().UTC()
		row.NextAttemptAt = &now
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "session_id"}, {Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return rollback(result.Error)
	}
	if result.RowsAffected == 1 {
		if err := tx.Commit().Error; err != nil {
			return imagegen.GenerationRecord{}, false, err
		}
		return imageGenerationDomainRecord(imageGenerationRecordFromRow(row)), true, nil
	}
	if err := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND idempotency_key = ?", input.TenantID, input.UserID, input.SessionID, strings.TrimSpace(input.IdempotencyKey)).Take(&existing).Error; err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return imagegen.GenerationRecord{}, false, err
	}
	return imageGenerationDomainRecord(imageGenerationRecordFromRow(existing)), false, nil
}

func countActiveImageGenerations(tx *gorm.DB, tenantID, userID uint64) (int64, error) {
	query := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND status IN (?, ?)", tenantID, imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry)
	if userID != 0 {
		query = query.Where("user_id = ?", userID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (r *GormRepository) ImageGenerationQueueStats(ctx context.Context, tenantID uint64) (ImageGenerationQueueStats, error) {
	if tenantID == 0 {
		return ImageGenerationQueueStats{}, ErrInvalidInput
	}
	var stats ImageGenerationQueueStats
	err := r.with(ctx).Model(&gormImageGeneration{}).
		Select("COALESCE(SUM(CASE WHEN status IN (?, ?) THEN 1 ELSE 0 END), 0) AS queue_depth, COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS running, MIN(CASE WHEN status IN (?, ?) THEN created_at ELSE NULL END) AS oldest_queued_at", imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry, imagegen.GenerationStatusRunning, imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry).
		Where("tenant_id = ?", tenantID).
		Scan(&stats).Error
	return stats, err
}

func (r *GormRepository) GetImageGeneration(ctx context.Context, tenantID, userID, sessionID uint64, generationID string) (ImageGenerationRecord, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 || strings.TrimSpace(generationID) == "" {
		return ImageGenerationRecord{}, ErrInvalidInput
	}
	var row gormImageGeneration
	err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ?", tenantID, userID, sessionID, strings.TrimSpace(generationID)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ImageGenerationRecord{}, ErrNotFound
	}
	if err != nil {
		return ImageGenerationRecord{}, err
	}
	return imageGenerationRecordFromRow(row), nil
}

func (r *GormRepository) FindImageGenerationByIdempotency(ctx context.Context, tenantID, userID, sessionID uint64, key string) (ImageGenerationRecord, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 || strings.TrimSpace(key) == "" {
		return ImageGenerationRecord{}, ErrInvalidInput
	}
	var row gormImageGeneration
	err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND idempotency_key = ?", tenantID, userID, sessionID, strings.TrimSpace(key)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ImageGenerationRecord{}, ErrNotFound
	}
	if err != nil {
		return ImageGenerationRecord{}, err
	}
	return imageGenerationRecordFromRow(row), nil
}

func (r *GormRepository) ListImageGenerations(ctx context.Context, tenantID, userID, sessionID uint64, limit int) ([]ImageGenerationRecord, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 {
		return nil, ErrInvalidInput
	}
	if limit <= 0 {
		limit = 100
	}
	var rows []gormImageGeneration
	if err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ?", tenantID, userID, sessionID).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]ImageGenerationRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, imageGenerationRecordFromRow(row))
	}
	return result, nil
}

func (r *GormRepository) UpdateImageGenerationStatus(ctx context.Context, tenantID, userID, sessionID uint64, generationID, status, errorCode, errorMessage string, finishedAt *time.Time) error {
	if tenantID == 0 || userID == 0 || sessionID == 0 || strings.TrimSpace(generationID) == "" || strings.TrimSpace(status) == "" {
		return ErrInvalidInput
	}
	updates := map[string]any{"status": strings.TrimSpace(status), "error_code": nullableStringPtr(errorCode), "error_message": nullableStringPtr(safeImageErrorMessage(errorMessage)), "finished_at": finishedAt}
	result := r.with(ctx).Model(&gormImageGeneration{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ?", tenantID, userID, sessionID, strings.TrimSpace(generationID)).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *GormRepository) SetImageGenerationAsset(ctx context.Context, tenantID, userID, sessionID uint64, generationID, assetID string) error {
	if tenantID == 0 || userID == 0 || sessionID == 0 || strings.TrimSpace(generationID) == "" || strings.TrimSpace(assetID) == "" {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormImageGeneration{}).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ?", tenantID, userID, sessionID, strings.TrimSpace(generationID)).
		Update("asset_id", strings.TrimSpace(assetID))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimDueImageGenerations holds a row lock only while ownership and the
// attempt ledger are persisted. Provider work must begin after this commits.
func (r *GormRepository) ClaimDueImageGenerations(ctx context.Context, tenantID uint64, workerID string, limit int, leaseUntil time.Time) ([]imagegen.GenerationRecord, error) {
	if tenantID == 0 || strings.TrimSpace(workerID) == "" || limit <= 0 || leaseUntil.IsZero() {
		return nil, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	rollback := func(err error) ([]imagegen.GenerationRecord, error) {
		return nil, rollbackImageTransaction(tx, err)
	}
	now := time.Now().UTC()
	var rows []gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND attempts < max_attempts AND ((status IN (?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND (lease_until IS NULL OR lease_until < ?)) OR (status = ? AND lease_until IS NOT NULL AND lease_until < ?))", tenantID, imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry, now, now, imagegen.GenerationStatusRunning, now).
		Order("next_attempt_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return rollback(err)
	}
	if len(rows) == 0 {
		if err := tx.Commit().Error; err != nil {
			return nil, err
		}
		return []imagegen.GenerationRecord{}, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	update := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND id IN ?", tenantID, ids).Updates(map[string]any{
		"status":       imagegen.GenerationStatusRunning,
		"lease_owner":  workerID,
		"lease_until":  leaseUntil,
		"heartbeat_at": now,
		"started_at":   gorm.Expr("COALESCE(started_at, ?)", now),
		"attempts":     gorm.Expr("attempts + 1"),
	})
	if update.Error != nil {
		return rollback(update.Error)
	}
	if update.RowsAffected != int64(len(rows)) {
		return rollback(ErrImageGenerationLeaseLost)
	}
	for i := range rows {
		rows[i].Status = imagegen.GenerationStatusRunning
		rows[i].LeaseOwner = nullableStringPtr(workerID)
		rows[i].LeaseUntil = &leaseUntil
		rows[i].HeartbeatAt = &now
		if rows[i].StartedAt == nil {
			rows[i].StartedAt = &now
		}
		rows[i].Attempts++
		attempt := gormImageGenerationAttempt{
			GenerationID: rows[i].GenerationID,
			TenantID:     tenantID,
			AttemptNo:    rows[i].Attempts,
			WorkerID:     workerID,
			Provider:     rows[i].Provider,
			Model:        rows[i].Model,
			StartedAt:    now,
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return rollback(err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	result := make([]imagegen.GenerationRecord, 0, len(rows))
	for _, row := range rows {
		result = append(result, imageGenerationDomainRecord(imageGenerationRecordFromRow(row)))
	}
	return result, nil
}

// CountLegacyOrphanedImageGenerations identifies only pre-async running rows
// that have never been leased by an image worker. It must stay in lockstep
// with FailLegacyOrphanedImageGenerations for reconciliation readback.
func (r *GormRepository) CountLegacyOrphanedImageGenerations(ctx context.Context, tenantID uint64, cutoff time.Time) (int, error) {
	if tenantID == 0 || cutoff.IsZero() {
		return 0, ErrInvalidInput
	}
	var count int64
	err := legacyOrphanedImageGenerationQuery(r.with(ctx), tenantID, cutoff).Count(&count).Error
	return int(count), err
}

// FailLegacyOrphanedImageGenerations makes legacy rows terminal without
// claiming, retrying, or calling a provider. The repeated predicate is the
// race guard between startup dry-run/count and this durable transition.
func (r *GormRepository) FailLegacyOrphanedImageGenerations(ctx context.Context, tenantID uint64, cutoff time.Time) (int, error) {
	if tenantID == 0 || cutoff.IsZero() {
		return 0, ErrInvalidInput
	}
	now := time.Now().UTC()
	result := legacyOrphanedImageGenerationQuery(r.with(ctx), tenantID, cutoff).Updates(map[string]any{
		"status":              imagegen.GenerationStatusFailed,
		"error_class":         imagegen.ErrorClassLegacyOrphaned,
		"error_code":          nil,
		"error_message":       "legacy image generation was not leased by an async worker",
		"finished_at":         now,
		"lease_owner":         nil,
		"lease_until":         nil,
		"heartbeat_at":        nil,
		"next_attempt_at":     nil,
		"provider_request_id": nil,
	})
	return int(result.RowsAffected), result.Error
}

func legacyOrphanedImageGenerationQuery(db *gorm.DB, tenantID uint64, cutoff time.Time) *gorm.DB {
	return db.Model(&gormImageGeneration{}).Where("tenant_id = ? AND status = ? AND lease_owner IS NULL AND lease_until IS NULL AND created_at < ?", tenantID, imagegen.GenerationStatusRunning, cutoff)
}

func (r *GormRepository) RenewImageGenerationLease(ctx context.Context, tenantID uint64, generationID, workerID string, leaseUntil time.Time) (bool, error) {
	if tenantID == 0 || strings.TrimSpace(generationID) == "" || strings.TrimSpace(workerID) == "" || leaseUntil.IsZero() {
		return false, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return false, tx.Error
	}
	rollback := func(err error) (bool, error) {
		return false, rollbackImageTransaction(tx, err)
	}
	var row gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("cancel_requested_at").Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", tenantID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning, strings.TrimSpace(workerID)).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrImageGenerationLeaseLost)
	}
	if err != nil {
		return rollback(err)
	}
	if row.CancelRequestedAt != nil {
		if err := tx.Commit().Error; err != nil {
			return false, err
		}
		return true, nil
	}
	now := time.Now().UTC()
	result := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", tenantID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning, strings.TrimSpace(workerID)).Updates(map[string]any{"lease_until": leaseUntil, "heartbeat_at": now})
	if err := imageGenerationLeaseResult(result); err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return false, err
	}
	return false, nil
}

func (r *GormRepository) TransitionClaimedImageAttempt(ctx context.Context, input imagegen.ImageGenerationAttemptTransition) (string, error) {
	if err := validateImageAttemptFinish(input.Attempt); err != nil || !validImageAttemptTransition(input.Status, input.NextAttemptAt) {
		return "", ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return "", tx.Error
	}
	rollback := func(err error) (string, error) { return "", rollbackImageTransaction(tx, err) }
	var generation gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("generation_id, tenant_id, origin_type, origin_ref_json, idempotency_key, cancel_requested_at").Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.Attempt.TenantID, strings.TrimSpace(input.Attempt.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(input.Attempt.WorkerID)).Take(&generation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrImageGenerationLeaseLost)
	}
	if err != nil {
		return rollback(err)
	}
	actualStatus := strings.TrimSpace(input.Status)
	if generation.CancelRequestedAt != nil {
		actualStatus = imagegen.GenerationStatusCancelled
	} else if actualStatus == imagegen.GenerationStatusCancelled {
		return rollback(ErrInvalidInput)
	}
	attempt := input.Attempt
	attempt.Outcome = actualStatus
	if actualStatus == imagegen.GenerationStatusCancelled {
		attempt.ErrorClass = imagegen.ErrorClassCallerCancelled
		attempt.ErrorMessage = "image generation cancellation requested"
	}
	if err := finishClaimedImageAttempt(tx, attempt); err != nil {
		return rollback(err)
	}
	updates := imageGenerationTransitionUpdates(actualStatus, attempt, input.NextAttemptAt)
	query := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", attempt.TenantID, strings.TrimSpace(attempt.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(attempt.WorkerID))
	if actualStatus == imagegen.GenerationStatusCancelled {
		query = query.Where("cancel_requested_at IS NOT NULL")
	} else {
		query = query.Where("cancel_requested_at IS NULL")
	}
	if err := imageGenerationLeaseResult(query.Updates(updates)); err != nil {
		return rollback(err)
	}
	if isImageGenerationFailureTerminalStatus(actualStatus) {
		if err := insertChannelImageTerminalEvent(tx, generation, attempt.FinishedAt); err != nil {
			return rollback(err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return "", err
	}
	return actualStatus, nil
}

func insertChannelImageTerminalEvent(tx *gorm.DB, generation gormImageGeneration, terminalAt time.Time) error {
	if generation.OriginType != imagegen.OriginTypeChannel {
		return nil
	}
	if generation.IdempotencyKey == nil || strings.TrimSpace(*generation.IdempotencyKey) == "" || generation.OriginRefJSON == nil || strings.TrimSpace(*generation.OriginRefJSON) == "" || terminalAt.IsZero() {
		return ErrInvalidInput
	}
	event := gormImageCompletionOutbox{
		TenantID: generation.TenantID, GenerationID: strings.TrimSpace(generation.GenerationID), EventType: imagegen.CompletionEventImageTerminal,
		OriginType: generation.OriginType, OriginRefJSON: generation.OriginRefJSON, IdempotencyKey: strings.TrimSpace(*generation.IdempotencyKey),
		Status: imagegen.CompletionDeliveryStatusPending, NextAttemptAt: &terminalAt,
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tenant_id"}, {Name: "generation_id"}, {Name: "event_type"}, {Name: "idempotency_key"}},
		DoUpdates: clause.Assignments(map[string]any{"id": gorm.Expr("id")}),
	}).Create(&event).Error
}

func (r *GormRepository) CompleteClaimedImageAttempt(ctx context.Context, input imagegen.CompleteClaimedImageAttemptRequest) (string, error) {
	if err := validateImageAttemptFinish(input.Attempt); err != nil || strings.TrimSpace(input.AssetID) == "" {
		return "", ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return "", tx.Error
	}
	rollback := func(err error) (string, error) { return "", rollbackImageTransaction(tx, err) }
	var generation gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.Attempt.TenantID, strings.TrimSpace(input.Attempt.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(input.Attempt.WorkerID)).Take(&generation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrImageGenerationLeaseLost)
	}
	if err != nil {
		return rollback(err)
	}
	if strings.TrimSpace(generation.OriginType) == "" || generation.IdempotencyKey == nil || strings.TrimSpace(*generation.IdempotencyKey) == "" {
		return rollback(ErrInvalidInput)
	}
	var asset gormMediaAsset
	if err := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND asset_id = ?", input.Attempt.TenantID, generation.UserID, generation.SessionID, strings.TrimSpace(input.AssetID)).Take(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rollback(ErrNotFound)
		}
		return rollback(err)
	}
	actualStatus := imagegen.GenerationStatusCompleted
	attempt := input.Attempt
	if generation.CancelRequestedAt != nil {
		actualStatus = imagegen.GenerationStatusCancelled
		attempt.Outcome = actualStatus
		attempt.ErrorClass = imagegen.ErrorClassCallerCancelled
		attempt.ErrorMessage = "image generation cancellation requested"
	} else {
		attempt.Outcome = imagegen.GenerationStatusCompleted
	}
	if err := finishClaimedImageAttempt(tx, attempt); err != nil {
		return rollback(err)
	}
	query := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", attempt.TenantID, strings.TrimSpace(attempt.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(attempt.WorkerID))
	if actualStatus == imagegen.GenerationStatusCancelled {
		query = query.Where("cancel_requested_at IS NOT NULL")
	} else {
		query = query.Where("cancel_requested_at IS NULL")
	}
	updates := imageGenerationTransitionUpdates(actualStatus, attempt, time.Time{})
	if actualStatus == imagegen.GenerationStatusCompleted {
		updates["asset_id"] = strings.TrimSpace(input.AssetID)
		updates["error_class"] = nil
		updates["error_code"] = nil
		updates["error_message"] = nil
	}
	if err := imageGenerationLeaseResult(query.Updates(updates)); err != nil {
		return rollback(err)
	}
	if actualStatus == imagegen.GenerationStatusCancelled {
		result := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND asset_id = ?", attempt.TenantID, generation.UserID, generation.SessionID, strings.TrimSpace(input.AssetID)).Delete(&gormMediaAsset{})
		if result.Error != nil {
			return rollback(result.Error)
		}
		if result.RowsAffected != 1 {
			return rollback(ErrNotFound)
		}
	}
	if err := insertChannelImageTerminalEvent(tx, generation, attempt.FinishedAt); err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return "", err
	}
	return actualStatus, nil
}

func (r *GormRepository) FinalizeExhaustedImageGenerations(ctx context.Context, tenantID uint64, now time.Time) (int, error) {
	if tenantID == 0 || now.IsZero() {
		return 0, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	rollback := func(err error) (int, error) { return 0, rollbackImageTransaction(tx, err) }
	var rows []gormImageGeneration
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND status = ? AND attempts >= max_attempts AND lease_until IS NOT NULL AND lease_until < ?", tenantID, imagegen.GenerationStatusRunning, now).Limit(100).Find(&rows).Error; err != nil {
		return rollback(err)
	}
	for _, row := range rows {
		status := imagegen.GenerationStatusDead
		class := imagegen.ErrorClassWorkerLeaseExpired
		message := "image generation lease expired after maximum attempts"
		if row.CancelRequestedAt != nil {
			status = imagegen.GenerationStatusCancelled
			class = imagegen.ErrorClassCallerCancelled
			message = "image generation cancellation requested"
		}
		attempt := imagegen.ImageGenerationAttemptFinish{TenantID: tenantID, GenerationID: row.GenerationID, AttemptNo: row.Attempts, WorkerID: valueStringPtr(row.LeaseOwner), FinishedAt: now, Outcome: status, ErrorClass: class, ErrorMessage: message}
		if err := finishClaimedImageAttempt(tx, attempt); err != nil {
			return rollback(err)
		}
		query := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ? AND lease_until < ?", tenantID, row.GenerationID, imagegen.GenerationStatusRunning, attempt.WorkerID, now)
		if status == imagegen.GenerationStatusCancelled {
			query = query.Where("cancel_requested_at IS NOT NULL")
		} else {
			query = query.Where("cancel_requested_at IS NULL")
		}
		if err := imageGenerationLeaseResult(query.Updates(imageGenerationTransitionUpdates(status, attempt, time.Time{}))); err != nil {
			return rollback(err)
		}
		if err := insertChannelImageTerminalEvent(tx, row, now); err != nil {
			return rollback(err)
		}
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(rows), nil
}

func validateImageAttemptFinish(input imagegen.ImageGenerationAttemptFinish) error {
	if input.TenantID == 0 || strings.TrimSpace(input.GenerationID) == "" || input.AttemptNo == 0 || strings.TrimSpace(input.WorkerID) == "" || input.FinishedAt.IsZero() || strings.TrimSpace(input.Outcome) == "" {
		return ErrInvalidInput
	}
	return nil
}

func validImageAttemptTransition(status string, next time.Time) bool {
	switch strings.TrimSpace(status) {
	case imagegen.GenerationStatusRetry:
		return !next.IsZero()
	case imagegen.GenerationStatusFailed, imagegen.GenerationStatusDead, imagegen.GenerationStatusOutcomeUnknown, imagegen.GenerationStatusCancelled:
		return next.IsZero()
	default:
		return false
	}
}

func finishClaimedImageAttempt(tx *gorm.DB, input imagegen.ImageGenerationAttemptFinish) error {
	updates := map[string]any{"finished_at": input.FinishedAt, "duration_ms": input.DurationMS, "provider_request_id": nullableStringPtr(input.ProviderRequestID), "outcome": strings.TrimSpace(input.Outcome), "error_class": nullableStringPtr(input.ErrorClass), "error_message": nullableStringPtr(truncateSafeImageError(input.ErrorMessage))}
	result := tx.Model(&gormImageGenerationAttempt{}).Where("tenant_id = ? AND generation_id = ? AND attempt_no = ? AND worker_id = ? AND finished_at IS NULL", input.TenantID, strings.TrimSpace(input.GenerationID), input.AttemptNo, strings.TrimSpace(input.WorkerID)).Updates(updates)
	return imageGenerationLeaseResult(result)
}

func imageGenerationTransitionUpdates(status string, attempt imagegen.ImageGenerationAttemptFinish, next time.Time) map[string]any {
	updates := map[string]any{"status": status, "error_class": nullableStringPtr(attempt.ErrorClass), "error_message": nullableStringPtr(truncateSafeImageError(attempt.ErrorMessage)), "provider_request_id": nullableStringPtr(attempt.ProviderRequestID), "lease_owner": nil, "lease_until": nil, "heartbeat_at": nil}
	if status == imagegen.GenerationStatusRetry {
		updates["next_attempt_at"] = next
		updates["finished_at"] = nil
	} else {
		updates["next_attempt_at"] = nil
		updates["finished_at"] = attempt.FinishedAt
	}
	return updates
}

func (r *GormRepository) ScheduleImageGenerationRetry(ctx context.Context, tenantID uint64, generationID, workerID string, nextAttemptAt time.Time, errorClass, errorMessage string) error {
	if tenantID == 0 || strings.TrimSpace(generationID) == "" || strings.TrimSpace(workerID) == "" || nextAttemptAt.IsZero() {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", tenantID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning, strings.TrimSpace(workerID)).Updates(map[string]any{
		"status":          imagegen.GenerationStatusRetry,
		"next_attempt_at": nextAttemptAt,
		"error_class":     nullableStringPtr(errorClass),
		"error_message":   nullableStringPtr(safeImageErrorMessage(errorMessage)),
		"lease_owner":     nil,
		"lease_until":     nil,
	})
	return imageGenerationLeaseResult(result)
}

// FinalizeImageGeneration is reserved for terminal outcomes that do not have
// an asset. Successful jobs use CompleteImageGeneration so the asset and
// completion event cannot diverge.
func (r *GormRepository) FinalizeImageGeneration(ctx context.Context, tenantID uint64, generationID, workerID, status, errorClass, errorMessage, providerRequestID string) error {
	if tenantID == 0 || strings.TrimSpace(generationID) == "" || strings.TrimSpace(workerID) == "" || !isImageGenerationFailureTerminalStatus(status) {
		return ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	rollback := func(err error) error { return rollbackImageTransaction(tx, err) }
	var generation gormImageGeneration
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", tenantID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning, strings.TrimSpace(workerID)).Take(&generation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rollback(ErrImageGenerationLeaseLost)
		}
		return rollback(err)
	}
	now := time.Now().UTC()
	result := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", tenantID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning, strings.TrimSpace(workerID)).Updates(map[string]any{
		"status":              strings.TrimSpace(status),
		"error_class":         nullableStringPtr(errorClass),
		"error_message":       nullableStringPtr(truncateSafeImageError(errorMessage)),
		"provider_request_id": nullableStringPtr(providerRequestID),
		"finished_at":         now,
		"lease_owner":         nil,
		"lease_until":         nil,
	})
	if err := imageGenerationLeaseResult(result); err != nil {
		return rollback(err)
	}
	if err := insertChannelImageTerminalEvent(tx, generation, now); err != nil {
		return rollback(err)
	}
	return tx.Commit().Error
}

func (r *GormRepository) FinishImageGenerationAttempt(ctx context.Context, input imagegen.ImageGenerationAttemptFinish) error {
	if input.TenantID == 0 || strings.TrimSpace(input.GenerationID) == "" || input.AttemptNo == 0 || strings.TrimSpace(input.WorkerID) == "" || input.FinishedAt.IsZero() || strings.TrimSpace(input.Outcome) == "" {
		return ErrInvalidInput
	}
	updates := map[string]any{
		"finished_at":         input.FinishedAt,
		"duration_ms":         input.DurationMS,
		"provider_request_id": nullableStringPtr(input.ProviderRequestID),
		"outcome":             strings.TrimSpace(input.Outcome),
		"error_class":         nullableStringPtr(input.ErrorClass),
		"error_message":       nullableStringPtr(truncateSafeImageError(input.ErrorMessage)),
	}
	result := r.with(ctx).Model(&gormImageGenerationAttempt{}).Where("tenant_id = ? AND generation_id = ? AND attempt_no = ? AND worker_id = ?", input.TenantID, strings.TrimSpace(input.GenerationID), input.AttemptNo, strings.TrimSpace(input.WorkerID)).Updates(updates)
	return imageGenerationLeaseResult(result)
}

// RequestImageGenerationCancel returns true only when an unclaimed queued or
// retry job reached cancelled. A running job records a cooperative request.
func (r *GormRepository) RequestImageGenerationCancel(ctx context.Context, tenantID, userID, sessionID uint64, generationID string) (bool, error) {
	if tenantID == 0 || userID == 0 || sessionID == 0 || strings.TrimSpace(generationID) == "" {
		return false, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return false, tx.Error
	}
	rollback := func(err error) (bool, error) { return false, rollbackImageTransaction(tx, err) }
	var generation gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ? AND status IN (?, ?, ?)", tenantID, userID, sessionID, strings.TrimSpace(generationID), imagegen.GenerationStatusQueued, imagegen.GenerationStatusRetry, imagegen.GenerationStatusRunning).Take(&generation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrNotFound)
	}
	if err != nil {
		return rollback(err)
	}
	now := time.Now().UTC()
	if generation.Status == imagegen.GenerationStatusRunning {
		result := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ? AND status = ?", tenantID, userID, sessionID, strings.TrimSpace(generationID), imagegen.GenerationStatusRunning).Updates(map[string]any{"cancel_requested_at": now})
		if err := imageGenerationLeaseResult(result); err != nil {
			return rollback(err)
		}
		if err := tx.Commit().Error; err != nil {
			return false, err
		}
		return false, nil
	}
	result := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ? AND status = ?", tenantID, userID, sessionID, strings.TrimSpace(generationID), generation.Status).Updates(map[string]any{"status": imagegen.GenerationStatusCancelled, "finished_at": now, "lease_owner": nil, "lease_until": nil})
	if err := imageGenerationLeaseResult(result); err != nil {
		return rollback(err)
	}
	if err := insertChannelImageTerminalEvent(tx, generation, now); err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return false, err
	}
	return true, nil
}

// RetryImageGeneration locks the exact caller-owned source before cloning its
// immutable request snapshot. The source terminal record is never rewritten.
func (r *GormRepository) RetryImageGeneration(ctx context.Context, input imagegen.ManualRetryRequest) (imagegen.GenerationRecord, bool, error) {
	if !validManualImageRetryRequest(input) {
		return imagegen.GenerationRecord{}, false, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return imagegen.GenerationRecord{}, false, tx.Error
	}
	rollback := func(err error) (imagegen.GenerationRecord, bool, error) {
		return imagegen.GenerationRecord{}, false, rollbackImageTransaction(tx, err)
	}

	var source gormImageGeneration
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND generation_id = ?", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID, strings.TrimSpace(input.SourceGenerationID)).
		Take(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(ErrNotFound)
	}
	if err != nil {
		return rollback(err)
	}
	if !manualImageRetryStatus(source.Status) {
		return rollback(imagegen.ErrImageManualRetryNotAllowed)
	}

	var existing gormImageGeneration
	err = tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND idempotency_key = ?", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID, strings.TrimSpace(input.IdempotencyKey)).Take(&existing).Error
	if err == nil {
		if valueStringPtr(existing.RetryOfGenerationID) != source.GenerationID {
			return rollback(ErrInvalidInput)
		}
		if err := tx.Commit().Error; err != nil {
			return imagegen.GenerationRecord{}, false, err
		}
		return imageGenerationDomainRecord(imageGenerationRecordFromRow(existing)), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return rollback(err)
	}

	now := input.CreatedAt.UTC()
	row := gormImageGeneration{
		GenerationID:        strings.TrimSpace(input.GenerationID),
		TenantID:            input.Scope.TenantID,
		UserID:              input.Scope.UserID,
		SessionID:           input.Scope.SessionID,
		SourceAssetID:       source.SourceAssetID,
		Operation:           source.Operation,
		Status:              imagegen.GenerationStatusQueued,
		Prompt:              source.Prompt,
		Provider:            source.Provider,
		Model:               source.Model,
		RequestJSON:         source.RequestJSON,
		TraceID:             nullableStringPtr(input.TraceID),
		IdempotencyKey:      nullableStringPtr(input.IdempotencyKey),
		BatchID:             nullableStringPtr(input.BatchID),
		OriginType:          input.Origin.Type,
		OriginRefJSON:       nullableStringPtr(input.Origin.RefJSON),
		ToolUseID:           nullableStringPtr(input.ToolUseID),
		MaxAttempts:         source.MaxAttempts,
		NextAttemptAt:       &now,
		RetryOfGenerationID: nullableStringPtr(source.GenerationID),
		UpdatedAt:           now,
		CreatedAt:           now,
	}
	if row.MaxAttempts == 0 {
		row.MaxAttempts = 3
	}
	if err := tx.Create(&row).Error; err != nil {
		return rollback(err)
	}
	if err := tx.Commit().Error; err != nil {
		return imagegen.GenerationRecord{}, false, err
	}
	return imageGenerationDomainRecord(imageGenerationRecordFromRow(row)), true, nil
}

func validManualImageRetryRequest(input imagegen.ManualRetryRequest) bool {
	return input.Scope.TenantID != 0 && input.Scope.UserID != 0 && input.Scope.SessionID != 0 &&
		strings.TrimSpace(input.SourceGenerationID) != "" && strings.TrimSpace(input.GenerationID) != "" &&
		strings.TrimSpace(input.IdempotencyKey) != "" && input.Origin.Type == imagegen.OriginTypeChannel &&
		strings.TrimSpace(input.Origin.RefJSON) != "" && input.CreatedAt.IsZero() == false
}

func manualImageRetryStatus(status string) bool {
	switch status {
	case imagegen.GenerationStatusFailed, imagegen.GenerationStatusDead, imagegen.GenerationStatusOutcomeUnknown:
		return true
	default:
		return false
	}
}

// CompleteImageGeneration makes asset linkage, terminal state and the
// completion event one durable fact. A lost lease rolls back all three.
func (r *GormRepository) CompleteImageGeneration(ctx context.Context, input imagegen.CompleteImageGenerationRequest) error {
	if input.TenantID == 0 || strings.TrimSpace(input.GenerationID) == "" || strings.TrimSpace(input.WorkerID) == "" || strings.TrimSpace(input.AssetID) == "" {
		return ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	rollback := func(err error) error {
		return rollbackImageTransaction(tx, err)
	}
	var generation gormImageGeneration
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.TenantID, strings.TrimSpace(input.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(input.WorkerID)).Take(&generation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rollback(ErrImageGenerationLeaseLost)
		}
		return rollback(err)
	}
	if strings.TrimSpace(generation.OriginType) == "" || generation.IdempotencyKey == nil || strings.TrimSpace(*generation.IdempotencyKey) == "" {
		return rollback(ErrInvalidInput)
	}
	var asset gormMediaAsset
	if err := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND asset_id = ?", input.TenantID, generation.UserID, generation.SessionID, strings.TrimSpace(input.AssetID)).Take(&asset).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return rollback(ErrNotFound)
		}
		return rollback(err)
	}
	now := time.Now().UTC()
	result := tx.Model(&gormImageGeneration{}).Where("tenant_id = ? AND generation_id = ? AND status = ? AND lease_owner = ?", input.TenantID, strings.TrimSpace(input.GenerationID), imagegen.GenerationStatusRunning, strings.TrimSpace(input.WorkerID)).Updates(map[string]any{
		"asset_id":      strings.TrimSpace(input.AssetID),
		"status":        imagegen.GenerationStatusCompleted,
		"finished_at":   now,
		"lease_owner":   nil,
		"lease_until":   nil,
		"error_class":   nil,
		"error_code":    nil,
		"error_message": nil,
	})
	if result.Error != nil {
		return rollback(result.Error)
	}
	if result.RowsAffected != 1 {
		return rollback(ErrImageGenerationLeaseLost)
	}
	if err := insertChannelImageTerminalEvent(tx, generation, now); err != nil {
		return rollback(err)
	}
	return tx.Commit().Error
}

func (r *GormRepository) ClaimDueImageCompletionEvents(ctx context.Context, tenantID uint64, workerID string, limit int, leaseUntil time.Time) ([]ImageCompletionOutboxRecord, error) {
	if tenantID == 0 || strings.TrimSpace(workerID) == "" || limit <= 0 || leaseUntil.IsZero() {
		return nil, ErrInvalidInput
	}
	tx := r.with(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	rollback := func(err error) ([]ImageCompletionOutboxRecord, error) {
		return nil, rollbackImageTransaction(tx, err)
	}
	now := time.Now().UTC()
	var rows []gormImageCompletionOutbox
	eligible := "((status IN (?, ?) AND (next_attempt_at IS NULL OR next_attempt_at <= ?) AND (lease_until IS NULL OR lease_until < ?)) OR (status = ? AND lease_until IS NOT NULL AND lease_until < ?))"
	eligibleArgs := []any{imagegen.CompletionDeliveryStatusPending, imagegen.CompletionDeliveryStatusRetry, now, now, imagegen.CompletionDeliveryStatusSending, now}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND "+eligible, append([]any{tenantID}, eligibleArgs...)...).Order("next_attempt_at ASC, id ASC").Limit(limit).Find(&rows).Error
	if err != nil {
		return rollback(err)
	}
	if len(rows) == 0 {
		if err := tx.Commit().Error; err != nil {
			return nil, err
		}
		return []ImageCompletionOutboxRecord{}, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	claimArgs := []any{tenantID, ids}
	claimArgs = append(claimArgs, eligibleArgs...)
	result := tx.Model(&gormImageCompletionOutbox{}).Where("tenant_id = ? AND id IN ? AND "+eligible, claimArgs...).Updates(map[string]any{"status": imagegen.CompletionDeliveryStatusSending, "lease_owner": strings.TrimSpace(workerID), "lease_until": leaseUntil, "attempts": gorm.Expr("attempts + 1")})
	if result.Error != nil {
		return rollback(result.Error)
	}
	if result.RowsAffected != int64(len(rows)) {
		return rollback(ErrImageGenerationLeaseLost)
	}
	output := make([]ImageCompletionOutboxRecord, 0, len(rows))
	for i := range rows {
		rows[i].Status = imagegen.CompletionDeliveryStatusSending
		rows[i].LeaseOwner = nullableStringPtr(workerID)
		rows[i].LeaseUntil = &leaseUntil
		rows[i].Attempts++
		output = append(output, imageCompletionOutboxRecordFromRow(rows[i]))
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	return output, nil
}

func (r *GormRepository) MarkImageCompletionEventRetry(ctx context.Context, tenantID, id uint64, workerID string, nextAttemptAt time.Time, code, message string) error {
	if nextAttemptAt.IsZero() {
		return ErrInvalidInput
	}
	return r.updateClaimedImageCompletionEvent(ctx, tenantID, id, workerID, map[string]any{"status": imagegen.CompletionDeliveryStatusRetry, "next_attempt_at": nextAttemptAt, "lease_owner": nil, "lease_until": nil, "last_error_code": nullableStringPtr(code), "last_error_message": nullableStringPtr(truncateSafeImageError(message))})
}

func (r *GormRepository) MarkImageCompletionEventSent(ctx context.Context, tenantID, id uint64, workerID string) error {
	return r.updateClaimedImageCompletionEvent(ctx, tenantID, id, workerID, map[string]any{"status": imagegen.CompletionDeliveryStatusSent, "sent_at": time.Now().UTC(), "lease_owner": nil, "lease_until": nil, "last_error_code": nil, "last_error_message": nil})
}

func (r *GormRepository) MarkImageCompletionEventDead(ctx context.Context, tenantID, id uint64, workerID, code, message string) error {
	return r.updateClaimedImageCompletionEvent(ctx, tenantID, id, workerID, map[string]any{"status": imagegen.CompletionDeliveryStatusDead, "lease_owner": nil, "lease_until": nil, "last_error_code": nullableStringPtr(code), "last_error_message": nullableStringPtr(truncateSafeImageError(message))})
}

func (r *GormRepository) updateClaimedImageCompletionEvent(ctx context.Context, tenantID, id uint64, workerID string, updates map[string]any) error {
	if tenantID == 0 || id == 0 || strings.TrimSpace(workerID) == "" {
		return ErrInvalidInput
	}
	result := r.with(ctx).Model(&gormImageCompletionOutbox{}).Where("tenant_id = ? AND id = ? AND status = ? AND lease_owner = ?", tenantID, id, imagegen.CompletionDeliveryStatusSending, strings.TrimSpace(workerID)).Updates(updates)
	return imageGenerationLeaseResult(result)
}

func validateImageGenerationInput(input ImageGenerationInput) error {
	if input.TenantID == 0 || input.UserID == 0 || input.SessionID == 0 || strings.TrimSpace(input.GenerationID) == "" || strings.TrimSpace(input.Operation) == "" || strings.TrimSpace(input.Status) == "" || strings.TrimSpace(input.Prompt) == "" || strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.Model) == "" {
		return ErrInvalidInput
	}
	return nil
}

func validateAsyncImageGenerationInput(input ImageGenerationInput) error {
	if err := validateImageGenerationInput(input); err != nil {
		return err
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return ErrInvalidInput
	}
	return nil
}

func imageGenerationRowFromInput(input ImageGenerationInput) gormImageGeneration {
	maxAttempts := input.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	return gormImageGeneration{
		ID: input.ID, GenerationID: strings.TrimSpace(input.GenerationID), TenantID: input.TenantID, UserID: input.UserID, SessionID: input.SessionID,
		AssetID: nullableStringPtr(input.AssetID), SourceAssetID: nullableStringPtr(input.SourceAssetID), Operation: strings.TrimSpace(input.Operation), Status: strings.TrimSpace(input.Status),
		Prompt: input.Prompt, Provider: strings.TrimSpace(input.Provider), Model: strings.TrimSpace(input.Model), RequestJSON: nullableStringPtr(input.RequestJSON),
		ErrorCode: nullableStringPtr(input.ErrorCode), ErrorMessage: nullableStringPtr(safeImageErrorMessage(input.ErrorMessage)), TraceID: nullableStringPtr(input.TraceID), IdempotencyKey: nullableStringPtr(input.IdempotencyKey),
		BatchID: nullableStringPtr(input.BatchID), OriginType: firstImageString(input.OriginType, imagegen.OriginTypeDirect), OriginRefJSON: nullableStringPtr(input.OriginRefJSON), ToolUseID: nullableStringPtr(input.ToolUseID),
		Attempts: input.Attempts, MaxAttempts: maxAttempts, NextAttemptAt: input.NextAttemptAt, LeaseOwner: nullableStringPtr(input.LeaseOwner), LeaseUntil: input.LeaseUntil, HeartbeatAt: input.HeartbeatAt, StartedAt: input.StartedAt,
		CancelRequestedAt: input.CancelRequestedAt, ProviderRequestID: nullableStringPtr(input.ProviderRequestID), RetryOfGenerationID: nullableStringPtr(input.RetryOfGenerationID), ErrorClass: nullableStringPtr(input.ErrorClass), UpdatedAt: input.UpdatedAt,
		CreatedAt: input.CreatedAt, FinishedAt: input.FinishedAt,
	}
}

func imageGenerationLeaseResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrImageGenerationLeaseLost
	}
	return nil
}

func imageCompletionOutboxRecordFromRow(row gormImageCompletionOutbox) ImageCompletionOutboxRecord {
	return ImageCompletionOutboxRecord{ID: row.ID, TenantID: row.TenantID, GenerationID: row.GenerationID, EventType: row.EventType, OriginType: row.OriginType, OriginRefJSON: valueStringPtr(row.OriginRefJSON), IdempotencyKey: row.IdempotencyKey, Status: row.Status, Attempts: row.Attempts, NextAttemptAt: row.NextAttemptAt, LeaseOwner: valueStringPtr(row.LeaseOwner), LeaseUntil: row.LeaseUntil, LastErrorCode: valueStringPtr(row.LastErrorCode), LastErrorMessage: valueStringPtr(row.LastErrorMessage), CreatedAt: row.CreatedAt, SentAt: row.SentAt}
}

func firstImageString(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

func truncateSafeImageError(message string) string {
	return imagegen.SanitizeErrorMessage(message)
}

func safeImageErrorMessage(message string) string { return imagegen.SanitizeErrorMessage(message) }

func rollbackImageTransaction(tx *gorm.DB, cause error) error {
	if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
		return errors.Join(cause, rollbackErr)
	}
	return cause
}

func isMySQLDuplicateKey(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func isImageGenerationFailureTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case imagegen.GenerationStatusFailed, imagegen.GenerationStatusOutcomeUnknown, imagegen.GenerationStatusCancelled, imagegen.GenerationStatusDead:
		return true
	default:
		return false
	}
}

func imageGenerationRecordFromRow(row gormImageGeneration) ImageGenerationRecord {
	return ImageGenerationRecord{ID: row.ID, GenerationID: row.GenerationID, TenantID: row.TenantID, UserID: row.UserID, SessionID: row.SessionID, AssetID: valueStringPtr(row.AssetID), SourceAssetID: valueStringPtr(row.SourceAssetID), Operation: row.Operation, Status: row.Status, Prompt: row.Prompt, Provider: row.Provider, Model: row.Model, RequestJSON: valueStringPtr(row.RequestJSON), ErrorCode: valueStringPtr(row.ErrorCode), ErrorMessage: valueStringPtr(row.ErrorMessage), TraceID: valueStringPtr(row.TraceID), IdempotencyKey: valueStringPtr(row.IdempotencyKey), BatchID: valueStringPtr(row.BatchID), OriginType: row.OriginType, OriginRefJSON: valueStringPtr(row.OriginRefJSON), ToolUseID: valueStringPtr(row.ToolUseID), Attempts: row.Attempts, MaxAttempts: row.MaxAttempts, NextAttemptAt: row.NextAttemptAt, LeaseOwner: valueStringPtr(row.LeaseOwner), LeaseUntil: row.LeaseUntil, HeartbeatAt: row.HeartbeatAt, StartedAt: row.StartedAt, CancelRequestedAt: row.CancelRequestedAt, ProviderRequestID: valueStringPtr(row.ProviderRequestID), RetryOfGenerationID: valueStringPtr(row.RetryOfGenerationID), ErrorClass: valueStringPtr(row.ErrorClass), UpdatedAt: row.UpdatedAt, CreatedAt: row.CreatedAt, FinishedAt: row.FinishedAt}
}

func imageGenerationInputFromDomain(record imagegen.GenerationRecord) ImageGenerationInput {
	return ImageGenerationInput{ID: record.ID, GenerationID: record.GenerationID, TenantID: record.TenantID, UserID: record.UserID, SessionID: record.SessionID, AssetID: record.AssetID, SourceAssetID: record.SourceAssetID, Operation: record.Operation, Status: record.Status, Prompt: record.Prompt, Provider: record.Provider, Model: record.Model, RequestJSON: record.RequestJSON, ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage, TraceID: record.TraceID, IdempotencyKey: record.IdempotencyKey, BatchID: record.BatchID, OriginType: record.OriginType, OriginRefJSON: record.OriginRefJSON, ToolUseID: record.ToolUseID, Attempts: record.Attempts, MaxAttempts: record.MaxAttempts, NextAttemptAt: record.NextAttemptAt, LeaseOwner: record.LeaseOwner, LeaseUntil: record.LeaseUntil, HeartbeatAt: record.HeartbeatAt, StartedAt: record.StartedAt, CancelRequestedAt: record.CancelRequestedAt, ProviderRequestID: record.ProviderRequestID, RetryOfGenerationID: record.RetryOfGenerationID, ErrorClass: record.ErrorClass, UpdatedAt: record.UpdatedAt, CreatedAt: record.CreatedAt, FinishedAt: record.FinishedAt}
}

func imageGenerationDomainRecord(record ImageGenerationRecord) imagegen.GenerationRecord {
	return imagegen.GenerationRecord{ID: record.ID, GenerationID: record.GenerationID, TenantID: record.TenantID, UserID: record.UserID, SessionID: record.SessionID, AssetID: record.AssetID, SourceAssetID: record.SourceAssetID, Operation: record.Operation, Status: record.Status, Prompt: record.Prompt, Provider: record.Provider, Model: record.Model, RequestJSON: record.RequestJSON, ErrorCode: record.ErrorCode, ErrorMessage: record.ErrorMessage, TraceID: record.TraceID, IdempotencyKey: record.IdempotencyKey, BatchID: record.BatchID, OriginType: record.OriginType, OriginRefJSON: record.OriginRefJSON, ToolUseID: record.ToolUseID, Attempts: record.Attempts, MaxAttempts: record.MaxAttempts, NextAttemptAt: record.NextAttemptAt, LeaseOwner: record.LeaseOwner, LeaseUntil: record.LeaseUntil, HeartbeatAt: record.HeartbeatAt, StartedAt: record.StartedAt, CancelRequestedAt: record.CancelRequestedAt, ProviderRequestID: record.ProviderRequestID, RetryOfGenerationID: record.RetryOfGenerationID, ErrorClass: record.ErrorClass, UpdatedAt: record.UpdatedAt, CreatedAt: record.CreatedAt, FinishedAt: record.FinishedAt}
}
