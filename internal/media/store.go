package media

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrNotFound  = errors.New("media asset not found")
	ErrForbidden = errors.New("media asset access forbidden")
)

// Store is the persistence boundary for media lifecycle records. Blob
// contents remain owned by the configured object store; this contract stores
// metadata, authorization and derived references only.
type Store interface {
	Put(context.Context, Asset) error
	Get(context.Context, AccessPolicy, string) (Asset, error)
	DeleteExpired(context.Context, time.Time) int
}

type MemoryStore struct {
	mu   sync.RWMutex
	now  func() time.Time
	data map[string]Asset
}

func NewMemoryStore() *MemoryStore { return NewMemoryStoreWithClock(time.Now) }

func NewMemoryStoreWithClock(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{now: now, data: make(map[string]Asset)}
}

func (s *MemoryStore) Put(ctx context.Context, asset Asset) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if asset.AssetID == "" || asset.TenantID == 0 || asset.UserID == 0 {
		return errors.New("media asset id, tenant and user are required")
	}
	if asset.Access.TenantID == 0 {
		asset.Access = AccessPolicy{TenantID: asset.TenantID, UserID: asset.UserID, SessionID: asset.SessionID}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.data[asset.AssetID]; ok && (existing.TenantID != asset.TenantID || existing.UserID != asset.UserID) {
		return ErrForbidden
	}
	s.data[asset.AssetID] = asset
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, policy AccessPolicy, assetID string) (Asset, error) {
	if err := ctx.Err(); err != nil {
		return Asset{}, err
	}
	s.mu.RLock()
	asset, ok := s.data[assetID]
	s.mu.RUnlock()
	if !ok {
		return Asset{}, ErrNotFound
	}
	if !asset.ExpiresAt.IsZero() && !asset.ExpiresAt.After(s.now()) {
		return Asset{}, ErrNotFound
	}
	if policy.TenantID == 0 || policy.TenantID != asset.Access.TenantID || (policy.UserID != 0 && policy.UserID != asset.Access.UserID) || (policy.SessionID != 0 && asset.Access.SessionID != 0 && policy.SessionID != asset.Access.SessionID) {
		return Asset{}, ErrForbidden
	}
	return asset, nil
}

func (s *MemoryStore) DeleteExpired(ctx context.Context, now time.Time) int {
	if ctx.Err() != nil {
		return 0
	}
	if now.IsZero() {
		now = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, asset := range s.data {
		if !asset.ExpiresAt.IsZero() && !asset.ExpiresAt.After(now) {
			delete(s.data, id)
			removed++
		}
	}
	return removed
}
