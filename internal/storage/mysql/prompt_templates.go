package mysql

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/prompttemplate"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type gormPromptTemplate struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	TenantID  uint64    `gorm:"column:tenant_id;not null"`
	UserID    uint64    `gorm:"column:user_id;not null"`
	Title     string    `gorm:"column:title;not null"`
	Content   string    `gorm:"column:content;not null"`
	Category  string    `gorm:"column:category;not null"`
	Pinned    bool      `gorm:"column:pinned;not null"`
	SortOrder int       `gorm:"column:sort_order;not null"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (gormPromptTemplate) TableName() string { return "tenant_prompt_templates" }

func (r *GormRepository) ListPromptTemplates(ctx context.Context, opts prompttemplate.ListOptions) ([]prompttemplate.PromptTemplate, error) {
	if opts.TenantID == 0 || opts.UserID == 0 {
		return nil, ErrInvalidInput
	}
	q := r.with(ctx).Where("tenant_id = ? AND user_id = ?", opts.TenantID, opts.UserID)
	if search := strings.TrimSpace(opts.Search); search != "" {
		like := "%" + search + "%"
		q = q.Where("title LIKE ? OR content LIKE ? OR category LIKE ?", like, like, like)
	}
	if category := strings.TrimSpace(opts.Category); category != "" {
		q = q.Where("category = ?", category)
	}
	var rows []gormPromptTemplate
	if err := q.Order("pinned DESC, sort_order ASC, title ASC").Limit(opts.Limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]prompttemplate.PromptTemplate, len(rows))
	for i, row := range rows {
		out[i] = prompttemplate.PromptTemplate{ID: row.ID, TenantID: row.TenantID, UserID: row.UserID, Title: row.Title, Content: row.Content, Category: row.Category, Pinned: row.Pinned, SortOrder: row.SortOrder, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}
	return out, nil
}

func (r *GormRepository) UpsertPromptTemplate(ctx context.Context, input prompttemplate.Input) (prompttemplate.PromptTemplate, error) {
	if input.TenantID == 0 || input.UserID == 0 || input.Title == "" || input.Content == "" {
		return prompttemplate.PromptTemplate{}, ErrInvalidInput
	}
	row := gormPromptTemplate{
		TenantID: input.TenantID, UserID: input.UserID, Title: input.Title,
		Content: input.Content, Category: input.Category, Pinned: input.Pinned, SortOrder: input.SortOrder,
	}
	err := r.with(ctx).Transaction(func(tx *gorm.DB) error {
		scope := tx.Where("tenant_id = ? AND user_id = ?", input.TenantID, input.UserID)
		if input.ID > 0 {
			// Explicit IDs are update-only: a missing or foreign record must not
			// become a new template. Maps also persist false/zero field values.
			if err := scope.Model(&gormPromptTemplate{}).Where("id = ?", input.ID).Updates(map[string]any{
				"title": input.Title, "content": input.Content, "category": input.Category,
				"pinned": input.Pinned, "sort_order": input.SortOrder,
			}).Error; err != nil {
				return err
			}
			return scope.Where("id = ?", input.ID).Take(&row).Error
		}
		// The database's owner/title unique key serializes concurrent creates.
		// Read back by that key instead of relying on dialect-specific insert IDs.
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "title"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"content", "category", "pinned", "sort_order", "updated_at",
			}),
		}).Create(&row).Error; err != nil {
			return err
		}
		row = gormPromptTemplate{}
		return scope.Where("title = ?", input.Title).Take(&row).Error
	})
	if err != nil {
		return prompttemplate.PromptTemplate{}, r.promptTemplateError(err)
	}
	return prompttemplate.PromptTemplate{ID: row.ID, TenantID: row.TenantID, UserID: row.UserID, Title: row.Title, Content: row.Content, Category: row.Category, Pinned: row.Pinned, SortOrder: row.SortOrder, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

func (r *GormRepository) DeletePromptTemplate(ctx context.Context, tenantID, userID, id uint64) error {
	result := r.with(ctx).Where("tenant_id = ? AND user_id = ? AND id = ?", tenantID, userID, id).Delete(&gormPromptTemplate{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return prompttemplate.ErrNotFound
	}
	return nil
}

func (r *GormRepository) promptTemplateError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return prompttemplate.ErrNotFound
	}
	if translator, ok := r.db.Dialector.(gorm.ErrorTranslator); ok {
		err = translator.Translate(err)
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return prompttemplate.ErrConflict
	}
	return err
}
