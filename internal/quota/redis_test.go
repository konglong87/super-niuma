package quota

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreEnforcesSharedLimits(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	storeA := NewRedisStore(client, "test:quota", func() time.Time { return now })
	storeB := NewRedisStore(client, "test:quota", func() time.Time { return now })
	one := uint64(1)
	cfg := Config{TenantID: 7, QuotaEnabled: true, QPSLimit: &one, DailyTokenLimit: uint64Ptr(10), DailyMessageLimit: uint64Ptr(2), MaxConcurrentRequests: &one}
	usageDate := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)

	if err := storeA.Reserve(context.Background(), cfg, ReserveRequest{EstimatedInputTokens: 2, ReservedOutputTokens: 3}, usageDate); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if err := storeB.Reserve(context.Background(), cfg, ReserveRequest{EstimatedInputTokens: 1}, usageDate); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("shared qps error = %v", err)
	}

	now = now.Add(2 * time.Second)
	if err := storeA.Settle(context.Background(), Reservation{TenantID: 7, QuotaEnabled: true, UsageDate: usageDate, ReservedInputTokens: 2, ReservedOutputTokens: 3}, Usage{InputTokens: 2, OutputTokens: 1}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if err := storeB.Reserve(context.Background(), cfg, ReserveRequest{EstimatedInputTokens: 8}, usageDate); !errors.Is(err, ErrDailyTokenLimitExceeded) {
		t.Fatalf("daily token error = %v", err)
	}
	if err := storeB.Reserve(context.Background(), cfg, ReserveRequest{EstimatedInputTokens: 1}, usageDate); err != nil {
		t.Fatalf("reserve after concurrent release: %v", err)
	}
}

func TestRedisStoreDefaultPrefixRemainsCompatible(t *testing.T) {
	store := NewRedisStore(nil, "", nil)
	if store.keyPrefix != defaultRedisKeyPrefix {
		t.Fatalf("default key prefix = %q, want %q", store.keyPrefix, defaultRedisKeyPrefix)
	}
}

func TestRedisE2EDefaultPrefixUsesCanonicalProductName(t *testing.T) {
	addr := os.Getenv("NIUMA_REDIS_E2E_ADDR")
	if addr == "" {
		t.Skip("set NIUMA_REDIS_E2E_ADDR to run the real Redis namespace check")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store := NewRedisStore(client, "", func() time.Time { return now })
	tenantID := uint64(now.UnixNano())
	keys := store.keys(tenantID, now, now)
	t.Cleanup(func() { _ = client.Del(ctx, keys...).Err() })
	one := uint64(1)
	cfg := Config{TenantID: tenantID, QuotaEnabled: true, QPSLimit: &one, DailyMessageLimit: &one, DailyTokenLimit: uint64Ptr(10), MaxConcurrentRequests: &one}
	if err := store.Reserve(ctx, cfg, ReserveRequest{EstimatedInputTokens: 1}, now); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "niuma:tenant_quota:") {
			t.Fatalf("Redis key = %q", key)
		}
		if exists, err := client.Exists(ctx, key).Result(); err != nil || exists != 1 {
			t.Fatalf("Redis key %q exists=%d err=%v", key, exists, err)
		}
	}
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}
