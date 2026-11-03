package quota

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func memNow() time.Time       { return time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC) }
func memUsageDate() time.Time { return time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC) }

func TestMemoryStoreEnforcesConcurrentLimit(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, MaxConcurrentRequests: uint64Ptr(2)}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); !errors.Is(err, ErrConcurrentLimitExceeded) {
		t.Fatalf("third reserve should exceed concurrent limit, got %v", err)
	}
}

func TestMemoryStoreSettleReleasesConcurrent(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, MaxConcurrentRequests: uint64Ptr(1)}
	ctx := context.Background()

	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); !errors.Is(err, ErrConcurrentLimitExceeded) {
		t.Fatalf("second reserve should be blocked, got %v", err)
	}
	if err := store.Settle(ctx, Reservation{TenantID: 7, QuotaEnabled: true, UsageDate: memUsageDate()}, Usage{}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("reserve after settle should succeed, got %v", err)
	}
}

func TestMemoryStoreEnforcesDailyTokenLimit(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, DailyTokenLimit: uint64Ptr(10)}
	ctx := context.Background()

	if err := store.Reserve(ctx, cfg, ReserveRequest{EstimatedInputTokens: 6, ReservedOutputTokens: 3}, memUsageDate()); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{EstimatedInputTokens: 5}, memUsageDate()); !errors.Is(err, ErrDailyTokenLimitExceeded) {
		t.Fatalf("token limit should trigger, got %v", err)
	}
}

func TestMemoryStoreEnforcesDailyMessageLimit(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, DailyMessageLimit: uint64Ptr(1)}
	ctx := context.Background()

	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); !errors.Is(err, ErrDailyMessageLimitExceeded) {
		t.Fatalf("message limit should trigger, got %v", err)
	}
}

func TestMemoryStoreEnforcesQPSLimitPerSecondWindow(t *testing.T) {
	now := memNow()
	store := NewMemoryStore(func() time.Time { return now })
	cfg := Config{TenantID: 7, QuotaEnabled: true, QPSLimit: uint64Ptr(1)}
	ctx := context.Background()

	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("qps limit should trigger in same second, got %v", err)
	}
	now = now.Add(time.Second)
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("reserve in next second window should succeed, got %v", err)
	}
}

func TestMemoryStoreSettleReconcilesOverReservedTokens(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, DailyTokenLimit: uint64Ptr(20)}
	ctx := context.Background()

	// 预扣 15,实际只用 5,Settle 应回补 10
	if err := store.Reserve(ctx, cfg, ReserveRequest{EstimatedInputTokens: 10, ReservedOutputTokens: 5}, memUsageDate()); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := store.Settle(ctx, Reservation{TenantID: 7, QuotaEnabled: true, UsageDate: memUsageDate(), ReservedInputTokens: 10, ReservedOutputTokens: 5}, Usage{InputTokens: 3, OutputTokens: 2}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// 回补后已用 5,余额 15,应允许再预扣 15
	if err := store.Reserve(ctx, cfg, ReserveRequest{EstimatedInputTokens: 15}, memUsageDate()); err != nil {
		t.Fatalf("reserve after reconciliation should fit remaining budget, got %v", err)
	}
}

func TestMemoryStoreConcurrentReserveDoesNotOversell(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, MaxConcurrentRequests: uint64Ptr(50)}
	ctx := context.Background()

	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 50 {
		t.Fatalf("granted=%d, want exactly 50 (no oversell under concurrency)", granted)
	}
}

func TestMemoryStoreDisabledQuotaAllowsAll(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: false, MaxConcurrentRequests: uint64Ptr(1)}
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
			t.Fatalf("disabled quota should allow all, reserve %d: %v", i, err)
		}
	}
}

func TestMemoryStoreResetsCountersOnNewDay(t *testing.T) {
	store := NewMemoryStore(memNow)
	cfg := Config{TenantID: 7, QuotaEnabled: true, DailyMessageLimit: uint64Ptr(1)}
	ctx := context.Background()

	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); err != nil {
		t.Fatalf("day 1 reserve: %v", err)
	}
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, memUsageDate()); !errors.Is(err, ErrDailyMessageLimitExceeded) {
		t.Fatalf("day 1 second reserve should be blocked, got %v", err)
	}
	nextDay := memUsageDate().AddDate(0, 0, 1)
	if err := store.Reserve(ctx, cfg, ReserveRequest{}, nextDay); err != nil {
		t.Fatalf("day 2 reserve should reset daily counters, got %v", err)
	}
}
