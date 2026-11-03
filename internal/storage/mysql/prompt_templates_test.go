package mysql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/prompttemplate"
)

func TestPromptTemplatesSQLitePersistence(t *testing.T) {
	repo, err := OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "templates.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	testPromptTemplatePersistence(t, repo)
}

func TestPromptTemplatesMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("NIUMA_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires an isolated, migrated NIUMA_TEST_MYSQL_DSN")
	}
	repo, err := OpenGormRepository(context.Background(), dsn, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	testPromptTemplatePersistence(t, repo)
}

func testPromptTemplatePersistence(t *testing.T, repo *GormRepository) {
	t.Helper()
	ctx := context.Background()
	tenantID, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "templates-" + uuid.NewString(), Name: "Template tests"})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := repo.EnsureUser(ctx, tenantID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	service := prompttemplate.NewService(repo)
	input := prompttemplate.Input{
		TenantID: tenantID, UserID: userID, Title: "Original", Content: "before",
		Category: "review", Pinned: true, SortOrder: 7,
	}
	created, err := service.Save(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("update and rename retain identity and zero values", func(t *testing.T) {
		update := input
		update.ID, update.Content, update.Pinned, update.SortOrder = created.ID, "after", false, 0
		stored, err := service.Save(ctx, update)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ID != created.ID || stored.Content != "after" || stored.Pinned || stored.SortOrder != 0 {
			t.Fatalf("update = %+v", stored)
		}
		update.Title = "Renamed"
		stored, err = service.Save(ctx, update)
		if err != nil || stored.ID != created.ID || stored.Title != update.Title {
			t.Fatalf("rename = %+v, err = %v", stored, err)
		}
		items, err := service.List(ctx, prompttemplate.ListOptions{TenantID: tenantID, UserID: userID})
		if err != nil || len(items) != 1 || items[0].ID != created.ID {
			t.Fatalf("readback = %+v, err = %v", items, err)
		}
	})

	t.Run("missing and foreign IDs never insert or update", func(t *testing.T) {
		otherUser, err := repo.EnsureUser(ctx, tenantID, "other")
		if err != nil {
			t.Fatal(err)
		}
		otherTenant, err := repo.UpsertTenant(ctx, TenantInput{TenantKey: "templates-foreign-" + uuid.NewString(), Name: "Other"})
		if err != nil {
			t.Fatal(err)
		}
		foreignUser, err := repo.EnsureUser(ctx, otherTenant, "foreign")
		if err != nil {
			t.Fatal(err)
		}
		for _, scope := range [][3]uint64{
			{tenantID, userID, created.ID + 1000},
			{tenantID, otherUser, created.ID},
			{otherTenant, foreignUser, created.ID},
		} {
			_, err := service.Save(ctx, prompttemplate.Input{
				TenantID: scope[0], UserID: scope[1], ID: scope[2], Title: "Must not exist", Content: "foreign",
			})
			if !errors.Is(err, prompttemplate.ErrNotFound) {
				t.Fatalf("scope %v: error = %v, want not found", scope, err)
			}
		}
	})

	t.Run("conflicting rename preserves both records", func(t *testing.T) {
		second := input
		second.Title = "Second"
		stored, err := service.Save(ctx, second)
		if err != nil {
			t.Fatal(err)
		}
		second.ID, second.Title, second.Content = stored.ID, "Renamed", "must not overwrite"
		if _, err := service.Save(ctx, second); !errors.Is(err, prompttemplate.ErrConflict) {
			t.Fatalf("conflicting rename = %v, want conflict", err)
		}
		items, err := service.List(ctx, prompttemplate.ListOptions{TenantID: tenantID, UserID: userID})
		if err != nil || len(items) != 2 {
			t.Fatalf("readback = %+v, err = %v", items, err)
		}
		for _, item := range items {
			if item.Content == second.Content {
				t.Fatal("conflicting rename changed stored content")
			}
		}
	})

	t.Run("concurrent title upserts converge on one ID", func(t *testing.T) {
		const writers = 8
		var wg sync.WaitGroup
		results := make(chan prompttemplate.PromptTemplate, writers)
		errs := make(chan error, writers)
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				item, err := service.Save(ctx, prompttemplate.Input{
					TenantID: tenantID, UserID: userID, Title: "Concurrent", Content: "same payload",
				})
				results <- item
				errs <- err
			}()
		}
		wg.Wait()
		close(results)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var id uint64
		for item := range results {
			if id == 0 {
				id = item.ID
			}
			if item.ID != id {
				t.Fatalf("concurrent IDs differ: %d != %d", item.ID, id)
			}
		}
		items, err := service.List(ctx, prompttemplate.ListOptions{TenantID: tenantID, UserID: userID, Search: "Concurrent"})
		if err != nil || len(items) != 1 || items[0].ID != id {
			t.Fatalf("concurrent readback = %+v, err = %v", items, err)
		}
		if err := service.Delete(ctx, tenantID, userID, id); err != nil {
			t.Fatal(err)
		}
		if err := service.Delete(ctx, tenantID, userID, id); !errors.Is(err, prompttemplate.ErrNotFound) {
			t.Fatal(fmt.Errorf("second delete: %w", err))
		}
	})
}
