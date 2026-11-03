package quota

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/product"
	"github.com/redis/go-redis/v9"
)

const defaultRedisKeyPrefix = product.TenantQuotaRedisPrefix

const redisReserveScript = `
local qps_count = tonumber(redis.call("GET", KEYS[1]) or "0")
local day_messages = tonumber(redis.call("GET", KEYS[2]) or "0")
local day_tokens = tonumber(redis.call("GET", KEYS[3]) or "0")
local concurrent = tonumber(redis.call("GET", KEYS[4]) or "0")
local qps_limit = tonumber(ARGV[1])
local message_limit = tonumber(ARGV[2])
local token_limit = tonumber(ARGV[3])
local concurrent_limit = tonumber(ARGV[4])
local reserved_tokens = tonumber(ARGV[5])
if qps_limit > 0 and qps_count >= qps_limit then
  return 1
end
if message_limit > 0 and day_messages + 1 > message_limit then
  return 2
end
if token_limit > 0 and day_tokens + reserved_tokens > token_limit then
  return 3
end
if concurrent_limit > 0 and concurrent >= concurrent_limit then
  return 4
end
redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[6]))
redis.call("INCR", KEYS[2])
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[7]))
if reserved_tokens > 0 then
  redis.call("INCRBY", KEYS[3], reserved_tokens)
end
redis.call("EXPIRE", KEYS[3], tonumber(ARGV[7]))
redis.call("INCR", KEYS[4])
redis.call("EXPIRE", KEYS[4], tonumber(ARGV[8]))
return 0
`

const redisSettleScript = `
local reserved_tokens = tonumber(ARGV[1])
local actual_tokens = tonumber(ARGV[2])
local day_ttl = tonumber(ARGV[3])
local concurrent_ttl = tonumber(ARGV[4])
local concurrent = tonumber(redis.call("GET", KEYS[2]) or "0")
if concurrent > 0 then
  redis.call("DECR", KEYS[2])
end
if actual_tokens > reserved_tokens then
  redis.call("INCRBY", KEYS[1], actual_tokens - reserved_tokens)
elseif reserved_tokens > actual_tokens then
  local current_tokens = tonumber(redis.call("GET", KEYS[1]) or "0")
  local delta = reserved_tokens - actual_tokens
  if current_tokens > delta then
    redis.call("DECRBY", KEYS[1], delta)
  else
    redis.call("SET", KEYS[1], 0)
  end
end
redis.call("EXPIRE", KEYS[1], day_ttl)
redis.call("EXPIRE", KEYS[2], concurrent_ttl)
return 0
`

type RedisStore struct {
	client    redis.Cmdable
	keyPrefix string
	now       func() time.Time
}

func NewRedisStore(client redis.Cmdable, keyPrefix string, now func() time.Time) *RedisStore {
	if now == nil {
		now = time.Now
	}
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultRedisKeyPrefix
	}
	return &RedisStore{client: client, keyPrefix: keyPrefix, now: now}
}

func NewRedisStoreFromAddr(addr, password string, db int, keyPrefix string, now func() time.Time) *RedisStore {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	return NewRedisStore(client, keyPrefix, now)
}

// Ping 探活。go-redis 的 NewClient 是懒连接，配错地址也照样构造成功，于是配额
// 后端不可用要等到第一次真实请求才暴露 —— 而那时 Reserve 会走 fail-open/fail-closed
// 分支，两种都不是运维想要的「启动就报错」。启动探活与 /readyz 都用这个方法
// （AUDIT-P1-22）。
func (s *RedisStore) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) Reserve(ctx context.Context, cfg Config, req ReserveRequest, usageDate time.Time) error {
	if s == nil || s.client == nil || !cfg.QuotaEnabled {
		return nil
	}
	now := s.now()
	result, err := s.client.Eval(ctx, redisReserveScript, s.keys(cfg.TenantID, usageDate, now),
		limitValue(cfg.QPSLimit),
		limitValue(cfg.DailyMessageLimit),
		limitValue(cfg.DailyTokenLimit),
		limitValue(cfg.MaxConcurrentRequests),
		req.EstimatedInputTokens+req.ReservedOutputTokens,
		secondTTL(now),
		dayTTL(now, usageDate),
		concurrentTTL(),
	).Int()
	if err != nil {
		return err
	}
	switch result {
	case 1:
		return ErrRateLimited
	case 2:
		return ErrDailyMessageLimitExceeded
	case 3:
		return ErrDailyTokenLimitExceeded
	case 4:
		return ErrConcurrentLimitExceeded
	default:
		return nil
	}
}

func (s *RedisStore) Settle(ctx context.Context, reservation Reservation, usage Usage) error {
	if s == nil || s.client == nil || !reservation.QuotaEnabled {
		return nil
	}
	_, err := s.client.Eval(ctx, redisSettleScript, s.settleKeys(reservation.TenantID, reservation.UsageDate),
		reservation.ReservedInputTokens+reservation.ReservedOutputTokens,
		usage.TotalTokens(),
		dayTTL(s.now(), reservation.UsageDate),
		concurrentTTL(),
	).Result()
	return err
}

func (s *RedisStore) keys(tenantID uint64, usageDate time.Time, now time.Time) []string {
	qpsKey, messageKey, tokenKey, concurrentKey := s.keyParts(tenantID, usageDate, now)
	return []string{qpsKey, messageKey, tokenKey, concurrentKey}
}

func (s *RedisStore) settleKeys(tenantID uint64, usageDate time.Time) []string {
	_, _, tokenKey, concurrentKey := s.keyParts(tenantID, usageDate, s.now())
	return []string{tokenKey, concurrentKey}
}

func (s *RedisStore) keyParts(tenantID uint64, usageDate time.Time, now time.Time) (string, string, string, string) {
	tenant := strconv.FormatUint(tenantID, 10)
	second := strconv.FormatInt(now.Unix(), 10)
	day := usageDate.Format("20060102")
	base := s.keyPrefix + ":tenant:" + tenant
	return base + ":qps:" + second, base + ":messages:" + day, base + ":tokens:" + day, base + ":concurrent"
}

func limitValue(limit *uint64) uint64 {
	if limit == nil {
		return 0
	}
	return *limit
}

func secondTTL(now time.Time) int {
	ttl := int(now.Truncate(time.Second).Add(2 * time.Second).Sub(now).Seconds())
	if ttl < 1 {
		return 1
	}
	return ttl
}

func dayTTL(now time.Time, usageDate time.Time) int {
	nextDay := time.Date(usageDate.Year(), usageDate.Month(), usageDate.Day()+1, 0, 0, 1, 0, usageDate.Location())
	ttl := int(nextDay.Sub(now.In(usageDate.Location())).Seconds())
	if ttl < 1 {
		return 1
	}
	return ttl
}

func concurrentTTL() int {
	return int((24 * time.Hour).Seconds())
}
