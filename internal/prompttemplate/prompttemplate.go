package prompttemplate

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidInput = errors.New("prompt template: invalid input")
	ErrNotFound     = errors.New("prompt template: not found")
	ErrConflict     = errors.New("prompt template: title already exists")
)

type PromptTemplate struct {
	ID        uint64    `json:"id"`
	TenantID  uint64    `json:"tenant_id,omitempty"`
	UserID    uint64    `json:"user_id,omitempty"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Category  string    `json:"category"`
	Pinned    bool      `json:"pinned"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Input struct {
	ID        uint64
	TenantID  uint64
	UserID    uint64
	Title     string
	Content   string
	Category  string
	Pinned    bool
	SortOrder int
}

type ListOptions struct {
	TenantID uint64
	UserID   uint64
	Search   string
	Category string
	Limit    int
}

type Store interface {
	ListPromptTemplates(context.Context, ListOptions) ([]PromptTemplate, error)
	UpsertPromptTemplate(context.Context, Input) (PromptTemplate, error)
	DeletePromptTemplate(context.Context, uint64, uint64, uint64) error
}

type Service struct{ store Store }

func NewService(store Store) *Service { return &Service{store: store} }

func (s *Service) List(ctx context.Context, opts ListOptions) ([]PromptTemplate, error) {
	if s == nil || s.store == nil || opts.TenantID == 0 || opts.UserID == 0 {
		return nil, ErrInvalidInput
	}
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 100
	}
	items, err := s.store.ListPromptTemplates(ctx, opts)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		if items[i].SortOrder != items[j].SortOrder {
			return items[i].SortOrder < items[j].SortOrder
		}
		return strings.ToLower(items[i].Title) < strings.ToLower(items[j].Title)
	})
	return items, nil
}

func (s *Service) Save(ctx context.Context, input Input) (PromptTemplate, error) {
	input.Title, input.Content, input.Category = strings.TrimSpace(input.Title), strings.TrimSpace(input.Content), strings.TrimSpace(input.Category)
	if s == nil || s.store == nil || input.TenantID == 0 || input.UserID == 0 || input.Title == "" || input.Content == "" {
		return PromptTemplate{}, ErrInvalidInput
	}
	if len(input.Title) > 200 || len(input.Content) > 100000 || len(input.Category) > 100 {
		return PromptTemplate{}, ErrInvalidInput
	}
	return s.store.UpsertPromptTemplate(ctx, input)
}

func (s *Service) Delete(ctx context.Context, tenantID, userID, id uint64) error {
	if s == nil || s.store == nil || tenantID == 0 || userID == 0 || id == 0 {
		return ErrInvalidInput
	}
	return s.store.DeletePromptTemplate(ctx, tenantID, userID, id)
}
