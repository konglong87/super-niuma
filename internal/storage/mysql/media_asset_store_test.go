package mysql

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/konglong87/super-niuma/internal/media"
)

func TestGormRepositoryImplementsMediaStore(t *testing.T) {
	var _ media.Store = (*GormRepository)(nil)
}

func TestMediaAssetStorePutRejectsMissingScope(t *testing.T) {
	repo := &GormRepository{}
	err := repo.Put(context.Background(), media.Asset{AssetID: "asset-1", Kind: media.KindImage, State: media.StateReady})
	if err == nil {
		t.Fatal("expected missing scope error")
	}
}

func TestListTenantMediaBlobPathsReturnsOnlyLiveTenantReferences(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT original_json, derivatives_json FROM `media_assets` WHERE tenant_id = ? AND state = ? AND (expires_at IS NULL OR expires_at > ?)")).
		WithArgs(uint64(7), string(media.StateReady), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"original_json", "derivatives_json"}).AddRow(`{"path":"tenant-7/asset-1.png"}`, `{"thumb":{"path":"tenant-7/asset-1-thumb.png"},"web":{"path":"tenant-7/asset-1.webp"}}`).AddRow(`{"url":"https://example.test/asset-2.png"}`, nil))
	paths, err := repo.ListTenantMediaBlobPaths(testContext(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 || paths[0] != "tenant-7/asset-1.png" || paths[1] != "tenant-7/asset-1-thumb.png" || paths[2] != "tenant-7/asset-1.webp" {
		t.Fatalf("paths=%v", paths)
	}
	assertExpectations(t, mock)
}

func TestListTenantMediaBlobPathsRejectsMissingTenant(t *testing.T) {
	repo := &GormRepository{}
	_, err := repo.ListTenantMediaBlobPaths(context.Background(), 0)
	if err != ErrInvalidInput {
		t.Fatalf("err=%v", err)
	}
}

func TestListTenantMediaBlobPathsIncludesDerivativeOnlyAsset(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT original_json, derivatives_json FROM `media_assets` WHERE tenant_id = ? AND state = ? AND (expires_at IS NULL OR expires_at > ?)")).
		WithArgs(uint64(7), string(media.StateReady), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"original_json", "derivatives_json"}).AddRow(nil, `{"preview":{"path":"tenant-7/derivative-only.webp"}}`))
	paths, err := repo.ListTenantMediaBlobPaths(testContext(), 7)
	if err != nil || len(paths) != 1 || paths[0] != "tenant-7/derivative-only.webp" {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
	assertExpectations(t, mock)
}
