package imagegen

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/konglong87/super-niuma/internal/media"
)

const (
	DefaultImageStartupTimeout      = 10 * time.Second
	DefaultImageOrphanBlobRetention = 24 * time.Hour
	DefaultImageOrphanBlobGrace     = 15 * time.Minute

	ErrorClassLegacyOrphaned = "legacy_orphaned"
)

var (
	ErrLegacyImageReconciliationMismatch = errors.New("legacy image reconciliation readback mismatch")
	ErrImageRecoveryUnsupported          = errors.New("image recovery capability unsupported")
)

// LegacyImageGenerationRepository keeps the pre-async running rows isolated
// from normal claim and retry paths. Both methods must apply identical guards.
type LegacyImageGenerationRepository interface {
	CountLegacyOrphanedImageGenerations(context.Context, uint64, time.Time) (int, error)
	FailLegacyOrphanedImageGenerations(context.Context, uint64, time.Time) (int, error)
}

// TenantMediaBlobReferenceRepository lists live asset paths for one tenant.
// It deliberately contains no cross-tenant enumeration API.
type TenantMediaBlobReferenceRepository interface {
	ListTenantMediaBlobPaths(context.Context, uint64) ([]string, error)
}

type LegacyImageReconciliationResult struct {
	TenantID   uint64
	Cutoff     time.Time
	Candidates int
	Updated    int
	Remaining  int
}

type OrphanBlobGCResult struct {
	TenantID   uint64
	Cutoff     time.Time
	Scanned    int
	Referenced int
	Eligible   int
	Deleted    int
	Failed     int
}

type ImageWorkerStartupRecoveryResult struct {
	Legacy LegacyImageReconciliationResult
	BlobGC OrphanBlobGCResult
}

// RunStartupRecovery performs only durable repair and storage cleanup. It
// intentionally never claims a generation or invokes an image provider.
func (w *ImageWorker) RunStartupRecovery(ctx context.Context) (ImageWorkerStartupRecoveryResult, error) {
	var result ImageWorkerStartupRecoveryResult
	if err := w.validateRecoveryConfig(); err != nil {
		return result, err
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if _, ok := w.cfg.Repository.(LegacyImageGenerationRepository); ok {
		legacy, err := w.ReconcileLegacy(ctx, false)
		result.Legacy = legacy
		if err != nil {
			return result, err
		}
	}
	if w.cfg.BlobStore != nil {
		if _, hasInventory := w.cfg.BlobStore.(BlobInventory); hasInventory {
			if _, hasReferences := w.cfg.Repository.(TenantMediaBlobReferenceRepository); hasReferences {
				gc, err := w.CollectOrphanBlobs(ctx)
				result.BlobGC = gc
				if err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

// ReconcileLegacy transitions only the known pre-async orphan shape. The
// detached deadline lets a shutdown finish this small state repair without
// resurrecting jobs or depending on the worker's lifecycle context.
func (w *ImageWorker) ReconcileLegacy(_ context.Context, dryRun bool) (LegacyImageReconciliationResult, error) {
	var result LegacyImageReconciliationResult
	if err := w.validateRecoveryConfig(); err != nil {
		return result, err
	}
	repository, ok := w.cfg.Repository.(LegacyImageGenerationRepository)
	if !ok {
		return result, ErrImageRecoveryUnsupported
	}
	result.TenantID = w.cfg.TenantID
	result.Cutoff = w.cfg.Now().UTC().Add(-(w.cfg.AttemptTimeout + w.cfg.LegacyReconciliationGrace))
	persistenceCtx, cancel := context.WithTimeout(context.Background(), w.cfg.StartupTimeout)
	defer cancel()
	candidates, err := repository.CountLegacyOrphanedImageGenerations(persistenceCtx, result.TenantID, result.Cutoff)
	if err != nil {
		return result, err
	}
	result.Candidates = candidates
	result.Remaining = candidates
	if dryRun || candidates == 0 {
		return result, nil
	}
	updated, err := repository.FailLegacyOrphanedImageGenerations(persistenceCtx, result.TenantID, result.Cutoff)
	if err != nil {
		return result, err
	}
	result.Updated = updated
	remaining, err := repository.CountLegacyOrphanedImageGenerations(persistenceCtx, result.TenantID, result.Cutoff)
	if err != nil {
		return result, err
	}
	result.Remaining = remaining
	if result.Remaining != 0 {
		return result, fmt.Errorf("%w: candidates=%d updated=%d remaining=%d", ErrLegacyImageReconciliationMismatch, result.Candidates, result.Updated, result.Remaining)
	}
	return result, nil
}

// CollectOrphanBlobs removes only old, unreferenced blobs owned by this
// worker's tenant. A failed delete does not prevent later eligible blobs from
// being collected, while lifecycle cancellation stops further destructive work.
func (w *ImageWorker) CollectOrphanBlobs(ctx context.Context) (OrphanBlobGCResult, error) {
	var result OrphanBlobGCResult
	if err := w.validateRecoveryConfig(); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	inventory, ok := w.cfg.BlobStore.(BlobInventory)
	if !ok {
		return result, ErrImageRecoveryUnsupported
	}
	references, ok := w.cfg.Repository.(TenantMediaBlobReferenceRepository)
	if !ok {
		return result, ErrImageRecoveryUnsupported
	}
	result.TenantID = w.cfg.TenantID
	result.Cutoff = w.cfg.Now().UTC().Add(-(w.cfg.OrphanBlobRetention + w.cfg.OrphanBlobGrace))
	paths, err := references.ListTenantMediaBlobPaths(ctx, result.TenantID)
	if err != nil {
		return result, err
	}
	referenced := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if validateTenantKey(result.TenantID, path) == nil {
			referenced[path] = struct{}{}
		}
	}
	blobs, err := inventory.ListTenantBlobs(ctx, result.TenantID)
	if err != nil {
		return result, err
	}
	sort.Slice(blobs, func(i, j int) bool { return blobs[i].Key < blobs[j].Key })
	deleteErrors := make([]error, 0)
	for _, blob := range blobs {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(deleteErrors, err)...)
		}
		if validateTenantKey(result.TenantID, blob.Key) != nil {
			continue
		}
		result.Scanned++
		if _, exists := referenced[blob.Key]; exists {
			result.Referenced++
			continue
		}
		if blob.CreatedAt.IsZero() || !blob.CreatedAt.Before(result.Cutoff) {
			continue
		}
		policy, err := imageBlobAccessPolicy(blob.Key)
		if err != nil {
			continue
		}
		result.Eligible++
		if err := w.cfg.BlobStore.Delete(ctx, policy, blob.Key); err != nil {
			result.Failed++
			deleteErrors = append(deleteErrors, fmt.Errorf("delete image blob: %w", err))
			continue
		}
		result.Deleted++
	}
	return result, errors.Join(deleteErrors...)
}

func (w *ImageWorker) validateRecoveryConfig() error {
	if w == nil || w.cfg.Repository == nil || w.cfg.TenantID == 0 || w.cfg.Now == nil || w.cfg.AttemptTimeout <= 0 || w.cfg.LegacyReconciliationGrace <= 0 || w.cfg.StartupTimeout <= 0 || w.cfg.OrphanBlobRetention <= 0 || w.cfg.OrphanBlobGrace <= 0 {
		return ErrInvalidImageWorkerConfig
	}
	return nil
}

func imageBlobAccessPolicy(key string) (media.AccessPolicy, error) {
	if err := validateBlobKey(key); err != nil {
		return media.AccessPolicy{}, err
	}
	matches := scopeKeyPattern.FindStringSubmatch(key)
	if len(matches) != 4 {
		return media.AccessPolicy{}, ErrInvalidBlob
	}
	tenantID, err := strconv.ParseUint(matches[1], 10, 64)
	if err != nil {
		return media.AccessPolicy{}, ErrInvalidBlob
	}
	userID, err := strconv.ParseUint(matches[2], 10, 64)
	if err != nil {
		return media.AccessPolicy{}, ErrInvalidBlob
	}
	sessionID, err := strconv.ParseUint(matches[3], 10, 64)
	if err != nil {
		return media.AccessPolicy{}, ErrInvalidBlob
	}
	return media.AccessPolicy{TenantID: tenantID, UserID: userID, SessionID: sessionID}, nil
}
