package prompttemplate

import (
	"context"
	"testing"
)

type fakeStore struct{ items []PromptTemplate }

func (f *fakeStore) ListPromptTemplates(_ context.Context, opts ListOptions) ([]PromptTemplate, error) {
	var out []PromptTemplate
	for _, item := range f.items {
		if item.TenantID == opts.TenantID && item.UserID == opts.UserID {
			out = append(out, item)
		}
	}
	return out, nil
}
func (f *fakeStore) UpsertPromptTemplate(_ context.Context, input Input) (PromptTemplate, error) {
	item := PromptTemplate{ID: input.ID, TenantID: input.TenantID, UserID: input.UserID, Title: input.Title, Content: input.Content, Pinned: input.Pinned, SortOrder: input.SortOrder}
	f.items = append(f.items, item)
	return item, nil
}
func (f *fakeStore) DeletePromptTemplate(_ context.Context, tenantID, userID, id uint64) error {
	for i, item := range f.items {
		if item.TenantID == tenantID && item.UserID == userID && item.ID == id {
			f.items = append(f.items[:i], f.items[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func TestServiceScopesAndValidatesTemplates(t *testing.T) {
	store := &fakeStore{items: []PromptTemplate{{ID: 1, TenantID: 1, UserID: 2, Title: "Pinned", Pinned: true}, {ID: 2, TenantID: 1, UserID: 3, Title: "Other"}}}
	svc := NewService(store)
	items, err := svc.List(context.Background(), ListOptions{TenantID: 1, UserID: 2})
	if err != nil || len(items) != 1 || items[0].ID != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if _, err := svc.Save(context.Background(), Input{TenantID: 1, UserID: 2, Title: " ", Content: "x"}); err != ErrInvalidInput {
		t.Fatalf("err=%v", err)
	}
}
