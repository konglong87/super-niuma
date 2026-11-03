package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormPendingInput struct {
	ID               uint64     `gorm:"column:id;primaryKey"`
	TenantID         uint64     `gorm:"column:tenant_id"`
	UserID           uint64     `gorm:"column:user_id"`
	SessionID        string     `gorm:"column:session_id"`
	BaseTaskID       uint64     `gorm:"column:base_task_id"`
	ClientInputID    string     `gorm:"column:client_input_id"`
	Content          string     `gorm:"column:content"`
	Direction        *string    `gorm:"column:direction"`
	AttachmentsJSON  *string    `gorm:"column:attachments_json"`
	Attempt          int        `gorm:"column:attempt"`
	Sequence         int64      `gorm:"column:sequence_no"`
	Status           string     `gorm:"column:status"`
	DispatchedTaskID *uint64    `gorm:"column:dispatched_task_id"`
	ErrorCode        *string    `gorm:"column:error_code"`
	ErrorMessage     *string    `gorm:"column:error_message"`
	ClaimedAt        *time.Time `gorm:"column:claimed_at"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
}

func (gormPendingInput) TableName() string { return "tenant_agent_task_pending_inputs" }

type gormPendingInputSetting struct {
	TenantID       uint64     `gorm:"column:tenant_id;primaryKey"`
	UserID         uint64     `gorm:"column:user_id;primaryKey"`
	SessionID      string     `gorm:"column:session_id;primaryKey"`
	Enabled        bool       `gorm:"column:enabled"`
	LeaseOwner     *string    `gorm:"column:lease_owner"`
	LeaseExpiresAt *time.Time `gorm:"column:lease_expires_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
}

func (gormPendingInputSetting) TableName() string {
	return "tenant_agent_task_pending_input_settings"
}

var _ pendinginput.Queue = (*GormRepository)(nil)
var _ pendinginput.ClientInputLookup = (*GormRepository)(nil)
var _ pendinginput.ActiveSessionStateLookup = (*GormRepository)(nil)

func (r *GormRepository) Add(ctx context.Context, input pendinginput.NewInput) (pendinginput.PendingInput, error) {
	input.Scope.SessionID = strings.TrimSpace(input.Scope.SessionID)
	input.ClientInputID = strings.TrimSpace(input.ClientInputID)
	if input.Scope.TenantID == 0 || input.Scope.UserID == 0 || strings.TrimSpace(input.Scope.SessionID) == "" || strings.TrimSpace(input.ClientInputID) == "" || (strings.TrimSpace(input.Content) == "" && len(input.Attachments) == 0) {
		return pendinginput.PendingInput{}, pendinginput.ErrInvalid
	}
	if len([]byte(input.Content)) > pendinginput.DefaultMaxContentSize || len([]byte(input.Direction)) > pendinginput.DefaultMaxContentSize {
		return pendinginput.PendingInput{}, pendinginput.ErrInvalid
	}
	attachments, err := json.Marshal(input.Attachments)
	if err != nil {
		return pendinginput.PendingInput{}, err
	}
	var result pendinginput.PendingInput
	err = r.pendingInputTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if input.GlobalClientInputID {
			var userID uint64
			if err := tx.Table("tenant_users").Select("id").Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", input.Scope.TenantID, input.Scope.UserID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&userID).Error; err != nil {
				return err
			}
			var existing []gormPendingInput
			if err := tx.Where("tenant_id = ? AND user_id = ? AND client_input_id = ?", input.Scope.TenantID, input.Scope.UserID, input.ClientInputID).Order("id ASC").Limit(2).Find(&existing).Error; err != nil {
				return err
			}
			if len(existing) > 1 {
				return pendinginput.ErrConflict
			}
			if len(existing) == 1 {
				result = pendingInputFromRow(existing[0])
				return nil
			}
		}
		seed := gormPendingInputSetting{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, SessionID: input.Scope.SessionID, Enabled: true, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var setting gormPendingInputSetting
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID).First(&setting).Error; err != nil {
			return err
		}
		if !setting.Enabled {
			return pendinginput.ErrQueueDisabled
		}
		var existing gormPendingInput
		query := tx.Where("tenant_id = ? AND user_id = ? AND session_id = ? AND client_input_id = ?", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID, input.ClientInputID).First(&existing)
		if query.Error == nil {
			result = pendingInputFromRow(existing)
			return nil
		}
		if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return query.Error
		}
		var active int64
		if err := tx.Model(&gormPendingInput{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status NOT IN (?, ?)", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID, pendinginput.StatusSent, pendinginput.StatusCancelled).Count(&active).Error; err != nil {
			return err
		}
		if active >= pendinginput.DefaultMaxItems {
			return pendinginput.ErrQueueFull
		}
		var maxSequence int64
		if err := tx.Model(&gormPendingInput{}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", input.Scope.TenantID, input.Scope.UserID, input.Scope.SessionID).Select("COALESCE(MAX(sequence_no), 0)").Scan(&maxSequence).Error; err != nil {
			return err
		}
		row := gormPendingInput{TenantID: input.Scope.TenantID, UserID: input.Scope.UserID, SessionID: input.Scope.SessionID, BaseTaskID: input.Scope.BaseTaskID, ClientInputID: input.ClientInputID, Content: input.Content, Direction: nullableStringPtr(input.Direction), AttachmentsJSON: nullableStringPtr(string(attachments)), Sequence: maxSequence + 1, Status: string(pendinginput.StatusQueued), CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		result = pendingInputFromRow(row)
		return nil
	})
	return result, err
}

func (r *GormRepository) FindByClientInputID(ctx context.Context, tenantID, userID uint64, clientInputID string) (pendinginput.PendingInput, bool, error) {
	clientInputID = strings.TrimSpace(clientInputID)
	if tenantID == 0 || userID == 0 || clientInputID == "" {
		return pendinginput.PendingInput{}, false, pendinginput.ErrInvalid
	}
	var rows []gormPendingInput
	if err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND client_input_id = ?", tenantID, userID, clientInputID).Order("id ASC").Limit(2).Find(&rows).Error; err != nil {
		return pendinginput.PendingInput{}, false, err
	}
	if len(rows) == 0 {
		return pendinginput.PendingInput{}, false, nil
	}
	if len(rows) != 1 {
		return pendinginput.PendingInput{}, false, pendinginput.ErrConflict
	}
	return pendingInputFromRow(rows[0]), true, nil
}

func (r *GormRepository) ListActiveSessionStates(ctx context.Context, tenantID, userID uint64, sessionIDs []string) (map[string]pendinginput.ActiveSessionState, error) {
	if tenantID == 0 || userID == 0 || len(sessionIDs) == 0 {
		return nil, pendinginput.ErrInvalid
	}
	unique := make(map[string]struct{}, len(sessionIDs))
	clean := make([]string, 0, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			return nil, pendinginput.ErrInvalid
		}
		if _, ok := unique[sessionID]; ok {
			continue
		}
		unique[sessionID] = struct{}{}
		clean = append(clean, sessionID)
	}
	sort.Strings(clean)
	type stateRow struct {
		SessionID  string `gorm:"column:session_id"`
		BaseTaskID uint64 `gorm:"column:base_task_id"`
		BaseCount  int    `gorm:"column:base_count"`
		Count      int    `gorm:"column:count"`
	}
	var rows []stateRow
	err := r.with(ctx).Model(&gormPendingInput{}).
		Select("session_id, MIN(base_task_id) AS base_task_id, COUNT(DISTINCT base_task_id) AS base_count, COUNT(*) AS count").
		Where("tenant_id = ? AND user_id = ? AND session_id IN ? AND status NOT IN (?, ?)", tenantID, userID, clean, pendinginput.StatusSent, pendinginput.StatusCancelled).
		Group("session_id").Order("session_id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	states := make(map[string]pendinginput.ActiveSessionState, len(rows))
	for _, row := range rows {
		if row.SessionID == "" || row.BaseTaskID == 0 || row.BaseCount != 1 || row.Count <= 0 {
			return nil, pendinginput.ErrConflict
		}
		states[row.SessionID] = pendinginput.ActiveSessionState{Count: row.Count, BaseTaskID: row.BaseTaskID}
	}
	return states, nil
}

func (r *GormRepository) List(ctx context.Context, scope pendinginput.Scope) ([]pendinginput.PendingInput, error) {
	if !scopeValid(scope) {
		return nil, pendinginput.ErrInvalid
	}
	var rows []gormPendingInput
	query := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status NOT IN (?, ?)", scope.TenantID, scope.UserID, scope.SessionID, pendinginput.StatusSent, pendinginput.StatusCancelled)
	if scope.BaseTaskID > 0 {
		query = query.Where("base_task_id = ?", scope.BaseTaskID)
	}
	query = query.Order("sequence_no ASC, id ASC").Limit(pendinginput.DefaultMaxItems).Find(&rows)
	if query.Error != nil {
		return nil, query.Error
	}
	out := make([]pendinginput.PendingInput, 0, len(rows))
	for _, row := range rows {
		out = append(out, pendingInputFromRow(row))
	}
	return out, nil
}

func (r *GormRepository) Update(ctx context.Context, id string, patch pendinginput.UpdateInput) (pendinginput.PendingInput, error) {
	row, err := r.getPendingInput(ctx, id)
	if err != nil {
		return pendinginput.PendingInput{}, err
	}
	if row.Status != string(pendinginput.StatusQueued) && row.Status != string(pendinginput.StatusFailed) {
		return pendinginput.PendingInput{}, pendinginput.ErrConflict
	}
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if patch.Content != nil {
		if strings.TrimSpace(*patch.Content) == "" || len([]byte(*patch.Content)) > pendinginput.DefaultMaxContentSize {
			return pendinginput.PendingInput{}, pendinginput.ErrInvalid
		}
		updates["content"] = *patch.Content
	}
	if patch.Direction != nil {
		if len([]byte(*patch.Direction)) > pendinginput.DefaultMaxContentSize {
			return pendinginput.PendingInput{}, pendinginput.ErrInvalid
		}
		updates["direction"] = *patch.Direction
	}
	if err := r.with(ctx).Model(&gormPendingInput{}).Where("id = ? AND tenant_id = ? AND user_id = ? AND status IN (?, ?)", row.ID, row.TenantID, row.UserID, pendinginput.StatusQueued, pendinginput.StatusFailed).Updates(updates).Error; err != nil {
		return pendinginput.PendingInput{}, err
	}
	row, err = r.getPendingInput(ctx, id)
	if err != nil {
		return pendinginput.PendingInput{}, err
	}
	return pendingInputFromRow(row), nil
}

func (r *GormRepository) Cancel(ctx context.Context, id string) error {
	row, err := r.getPendingInput(ctx, id)
	if err != nil {
		return err
	}
	result := r.with(ctx).Model(&gormPendingInput{}).Where("id = ? AND status IN (?, ?)", row.ID, pendinginput.StatusQueued, pendinginput.StatusFailed).Updates(map[string]any{"status": pendinginput.StatusCancelled, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return pendinginput.ErrConflict
	}
	return nil
}

func (r *GormRepository) MoveUp(ctx context.Context, id string) (pendinginput.PendingInput, error) {
	numericID, err := parsePendingInputID(id)
	if err != nil {
		return pendinginput.PendingInput{}, pendinginput.ErrInvalid
	}
	var result pendinginput.PendingInput
	err = r.pendingInputTransaction(ctx, func(tx *gorm.DB) error {
		var current, previous gormPendingInput
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", numericID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return pendinginput.ErrNotFound
			}
			return err
		}
		if current.Status != string(pendinginput.StatusQueued) {
			return pendinginput.ErrConflict
		}
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status = ? AND sequence_no < ?", current.TenantID, current.UserID, current.SessionID, pendinginput.StatusQueued, current.Sequence).Order("sequence_no DESC, id DESC").First(&previous)
		if current.BaseTaskID > 0 {
			query = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND base_task_id = ? AND status = ? AND sequence_no < ?", current.TenantID, current.UserID, current.SessionID, current.BaseTaskID, pendinginput.StatusQueued, current.Sequence).Order("sequence_no DESC, id DESC").First(&previous)
		}
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			result = pendingInputFromRow(current)
			return nil
		}
		if query.Error != nil {
			return query.Error
		}
		current.Sequence, previous.Sequence = previous.Sequence, current.Sequence
		now := time.Now().UTC()
		if err := tx.Model(&gormPendingInput{}).Where("id = ?", current.ID).Updates(map[string]any{"sequence_no": current.Sequence, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&gormPendingInput{}).Where("id = ?", previous.ID).Updates(map[string]any{"sequence_no": previous.Sequence, "updated_at": now}).Error; err != nil {
			return err
		}
		result = pendingInputFromRow(current)
		return nil
	})
	return result, err
}

func (r *GormRepository) ClaimNext(ctx context.Context, scope pendinginput.Scope) (pendinginput.PendingInput, bool, error) {
	if !scopeValid(scope) {
		return pendinginput.PendingInput{}, false, pendinginput.ErrInvalid
	}
	var result pendinginput.PendingInput
	claimed := false
	err := r.pendingInputTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		seed := gormPendingInputSetting{TenantID: scope.TenantID, UserID: scope.UserID, SessionID: scope.SessionID, Enabled: true, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var setting gormPendingInputSetting
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", scope.TenantID, scope.UserID, scope.SessionID).First(&setting).Error; err != nil {
			return err
		}
		if !setting.Enabled {
			return nil
		}
		var row gormPendingInput
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status = ?", scope.TenantID, scope.UserID, scope.SessionID, pendinginput.StatusQueued)
		if scope.BaseTaskID > 0 {
			query = query.Where("base_task_id = ?", scope.BaseTaskID)
		}
		query = query.Order("sequence_no ASC, id ASC").First(&row)
		if errors.Is(query.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if query.Error != nil {
			return query.Error
		}
		if err := tx.Model(&gormPendingInput{}).Where("id = ? AND status = ?", row.ID, pendinginput.StatusQueued).Updates(map[string]any{"status": pendinginput.StatusRunning, "claimed_at": now, "updated_at": now}).Error; err != nil {
			return err
		}
		row.Status, row.ClaimedAt, row.UpdatedAt = string(pendinginput.StatusRunning), &now, now
		result, claimed = pendingInputFromRow(row), true
		return nil
	})
	return result, claimed, err
}

func (r *GormRepository) MarkSent(ctx context.Context, id string, taskID uint64) error {
	numericID, err := parsePendingInputID(id)
	if err != nil {
		return pendinginput.ErrInvalid
	}
	result := r.with(ctx).Model(&gormPendingInput{}).Where("id = ? AND status = ?", numericID, pendinginput.StatusRunning).Updates(map[string]any{"status": pendinginput.StatusSent, "dispatched_task_id": taskID, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return pendinginput.ErrConflict
	}
	return nil
}

func (r *GormRepository) MarkFailed(ctx context.Context, id string, code string) error {
	numericID, err := parsePendingInputID(id)
	if err != nil {
		return pendinginput.ErrInvalid
	}
	result := r.with(ctx).Model(&gormPendingInput{}).Where("id = ? AND status = ?", numericID, pendinginput.StatusRunning).Updates(map[string]any{"status": pendinginput.StatusFailed, "error_code": strings.TrimSpace(code), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return pendinginput.ErrConflict
	}
	return nil
}

func (r *GormRepository) Retry(ctx context.Context, id string) (pendinginput.PendingInput, error) {
	numericID, err := parsePendingInputID(id)
	if err != nil {
		return pendinginput.PendingInput{}, pendinginput.ErrInvalid
	}
	var result pendinginput.PendingInput
	err = r.pendingInputTransaction(ctx, func(tx *gorm.DB) error {
		var row gormPendingInput
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", numericID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return pendinginput.ErrNotFound
			}
			return err
		}
		if row.Status != string(pendinginput.StatusFailed) {
			return pendinginput.ErrConflict
		}
		var maxSequence int64
		if err := tx.Model(&gormPendingInput{}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", row.TenantID, row.UserID, row.SessionID).Select("COALESCE(MAX(sequence_no), 0)").Scan(&maxSequence).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&gormPendingInput{}).Where("id = ? AND status = ?", row.ID, pendinginput.StatusFailed).Updates(map[string]any{"status": pendinginput.StatusQueued, "attempt": row.Attempt + 1, "sequence_no": maxSequence + 1, "error_code": nil, "error_message": nil, "updated_at": now}).Error; err != nil {
			return err
		}
		row.Status, row.Attempt, row.Sequence, row.UpdatedAt = string(pendinginput.StatusQueued), row.Attempt+1, maxSequence+1, now
		result = pendingInputFromRow(row)
		return nil
	})
	return result, err
}

func (r *GormRepository) QueueEnabled(ctx context.Context, scope pendinginput.Scope) (bool, error) {
	if !scopeValid(scope) {
		return false, pendinginput.ErrInvalid
	}
	var setting gormPendingInputSetting
	err := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ?", scope.TenantID, scope.UserID, scope.SessionID).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil
	}
	return setting.Enabled, err
}

func (r *GormRepository) SetQueueEnabled(ctx context.Context, scope pendinginput.Scope, enabled bool) error {
	if !scopeValid(scope) {
		return pendinginput.ErrInvalid
	}
	row := gormPendingInputSetting{TenantID: scope.TenantID, UserID: scope.UserID, SessionID: scope.SessionID, Enabled: enabled, UpdatedAt: time.Now().UTC()}
	return r.with(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "session_id"}}, DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"})}).Create(&row).Error
}

func (r *GormRepository) AcquireConsumerLease(ctx context.Context, scope pendinginput.Scope, owner string, ttl time.Duration) (bool, error) {
	owner = strings.TrimSpace(owner)
	if !scopeValid(scope) || owner == "" || ttl <= 0 {
		return false, pendinginput.ErrInvalid
	}
	acquired := false
	err := r.pendingInputTransaction(ctx, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		seed := gormPendingInputSetting{TenantID: scope.TenantID, UserID: scope.UserID, SessionID: scope.SessionID, Enabled: true, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var row gormPendingInputSetting
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", scope.TenantID, scope.UserID, scope.SessionID).First(&row).Error; err != nil {
			return err
		}
		if !row.Enabled {
			return nil
		}
		currentOwner := stringValue(row.LeaseOwner)
		if currentOwner != "" && currentOwner != owner && row.LeaseExpiresAt != nil && row.LeaseExpiresAt.After(now) {
			return nil
		}
		if currentOwner != owner {
			updates := map[string]any{"status": pendinginput.StatusFailed, "error_code": "consumer_lease_expired", "updated_at": now}
			query := tx.Model(&gormPendingInput{}).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status = ?", scope.TenantID, scope.UserID, scope.SessionID, pendinginput.StatusRunning)
			if scope.BaseTaskID > 0 {
				query = query.Where("base_task_id = ?", scope.BaseTaskID)
			}
			if err := query.Updates(updates).Error; err != nil {
				return err
			}
		}
		expiresAt := now.Add(ttl)
		if err := tx.Model(&gormPendingInputSetting{}).Where("tenant_id = ? AND user_id = ? AND session_id = ?", scope.TenantID, scope.UserID, scope.SessionID).Updates(map[string]any{"lease_owner": owner, "lease_expires_at": expiresAt, "updated_at": now}).Error; err != nil {
			return err
		}
		acquired = true
		return nil
	})
	return acquired, err
}

func (r *GormRepository) ReleaseConsumerLease(ctx context.Context, scope pendinginput.Scope, owner string) error {
	owner = strings.TrimSpace(owner)
	if !scopeValid(scope) || owner == "" {
		return pendinginput.ErrInvalid
	}
	return r.with(ctx).Model(&gormPendingInputSetting{}).
		Where("tenant_id = ? AND user_id = ? AND session_id = ? AND lease_owner = ?", scope.TenantID, scope.UserID, scope.SessionID, owner).
		Updates(map[string]any{"lease_owner": nil, "lease_expires_at": nil, "updated_at": time.Now().UTC()}).Error
}

func (r *GormRepository) getPendingInput(ctx context.Context, id string) (gormPendingInput, error) {
	numericID, err := parsePendingInputID(id)
	if err != nil {
		return gormPendingInput{}, pendinginput.ErrInvalid
	}
	var row gormPendingInput
	err = r.with(ctx).Where("id = ?", numericID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, pendinginput.ErrNotFound
	}
	return row, err
}

func parsePendingInputID(value string) (uint64, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "pi-"))
	if value == "" {
		return 0, pendinginput.ErrInvalid
	}
	return strconv.ParseUint(value, 10, 64)
}

func (r *GormRepository) pendingInputTransaction(ctx context.Context, fn func(*gorm.DB) error) error {
	return withRetryableTransaction(ctx, func() error {
		return r.with(ctx).Transaction(fn)
	})
}

func pendingInputFromRow(row gormPendingInput) pendinginput.PendingInput {
	var attachments []agenttasks.Attachment
	if row.AttachmentsJSON != nil {
		_ = json.Unmarshal([]byte(*row.AttachmentsJSON), &attachments)
	}
	return pendinginput.PendingInput{ID: formatPendingInputID(row.ID), TenantID: row.TenantID, UserID: row.UserID, SessionID: row.SessionID, BaseTaskID: row.BaseTaskID, ClientInputID: row.ClientInputID, Content: row.Content, Direction: stringValue(row.Direction), Attempt: row.Attempt, Attachments: attachments, Sequence: row.Sequence, Status: pendinginput.Status(row.Status), DispatchedTaskID: uint64Value(row.DispatchedTaskID), ErrorCode: stringValue(row.ErrorCode), ErrorMessage: stringValue(row.ErrorMessage), ClaimedAt: timeValue(row.ClaimedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func formatPendingInputID(id uint64) string { return "pi-" + strconv.FormatUint(id, 10) }
func scopeValid(scope pendinginput.Scope) bool {
	return scope.TenantID > 0 && scope.UserID > 0 && strings.TrimSpace(scope.SessionID) != ""
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func uint64Value(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}
func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
