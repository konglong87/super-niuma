package server

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/product"
	"github.com/redis/go-redis/v9"
)

const defaultMobileRedisKeyPrefix = product.MobileUsageRedisPrefix

// Reserve runs as one Redis Lua script so multi-instance API servers cannot
// oversell a user's minute/day quota between separate GET and INCR commands.
const mobileRedisReserveScript = `
local minute_count = tonumber(redis.call("GET", KEYS[1]) or "0")
local day_messages = tonumber(redis.call("GET", KEYS[2]) or "0")
local day_tokens = tonumber(redis.call("GET", KEYS[3]) or "0")
local minute_limit = tonumber(ARGV[1])
local message_limit = tonumber(ARGV[2])
local token_limit = tonumber(ARGV[3])
local estimated_tokens = tonumber(ARGV[4])
if minute_limit > 0 and minute_count >= minute_limit then
  return 1
end
if message_limit > 0 and day_messages >= message_limit then
  return 2
end
if token_limit > 0 and day_tokens + estimated_tokens > token_limit then
  return 2
end
redis.call("INCR", KEYS[1])
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[5]))
redis.call("INCR", KEYS[2])
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[6]))
if estimated_tokens > 0 then
  redis.call("INCRBY", KEYS[3], estimated_tokens)
end
redis.call("EXPIRE", KEYS[3], tonumber(ARGV[6]))
return 0
`

const mobileRedisAddTokensScript = `
redis.call("INCRBY", KEYS[1], tonumber(ARGV[1]))
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
return 0
`

const mobileRedisReleaseScript = `
local minute_count = tonumber(redis.call("GET", KEYS[1]) or "0")
local day_messages = tonumber(redis.call("GET", KEYS[2]) or "0")
local day_tokens = tonumber(redis.call("GET", KEYS[3]) or "0")
local estimated_tokens = tonumber(ARGV[1])
if minute_count > 0 then
  redis.call("DECR", KEYS[1])
end
if day_messages > 0 then
  redis.call("DECR", KEYS[2])
end
if estimated_tokens > 0 then
  if day_tokens > estimated_tokens then
    redis.call("DECRBY", KEYS[3], estimated_tokens)
  else
    redis.call("SET", KEYS[3], 0)
  end
end
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[2]))
redis.call("EXPIRE", KEYS[2], tonumber(ARGV[3]))
redis.call("EXPIRE", KEYS[3], tonumber(ARGV[3]))
return 0
`

type RedisMobileUsageStore struct {
	client    redis.Cmdable
	keyPrefix string
	now       func() time.Time
}

func NewRedisMobileUsageStore(client redis.Cmdable, keyPrefix string, now func() time.Time) *RedisMobileUsageStore {
	if now == nil {
		now = time.Now
	}
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = defaultMobileRedisKeyPrefix
	}
	return &RedisMobileUsageStore{client: client, keyPrefix: keyPrefix, now: now}
}

func NewRedisMobileUsageStoreFromAddr(addr, password string, db int, keyPrefix string, now func() time.Time) *RedisMobileUsageStore {
	client := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	return NewRedisMobileUsageStore(client, keyPrefix, now)
}

// Ping 探活。理由同 quota.RedisStore.Ping：NewClient 懒连接，不主动探一下就要等到
// 第一条 mobile 消息才发现 Redis 根本连不上（AUDIT-P1-22）。
func (s *RedisMobileUsageStore) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Ping(ctx).Err()
}

func (s *RedisMobileUsageStore) Reserve(userKey string, policy MobileUsagePolicy, estimatedTokens int) error {
	if s == nil || s.client == nil {
		return nil
	}
	now := s.now()
	keys := s.keys(userKey, now)
	minuteTTL := int(now.Truncate(time.Minute).Add(time.Minute + time.Second).Sub(now).Seconds())
	dayTTL := int(time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, now.Location()).Sub(now).Seconds())
	result, err := s.client.Eval(context.Background(), mobileRedisReserveScript, keys,
		policy.RateLimitPerMinute,
		policy.DailyMessageQuota,
		policy.DailyTokenQuota,
		estimatedTokens,
		minuteTTL,
		dayTTL,
	).Int()
	if err != nil {
		return err
	}
	switch result {
	case 1:
		return errMobileRateLimited
	case 2:
		return errMobileQuotaExceeded
	default:
		return nil
	}
}

func (s *RedisMobileUsageStore) AddTokens(userKey string, tokens int) {
	if s == nil || s.client == nil || tokens <= 0 {
		return
	}
	now := s.now()
	_, _, tokenKey := s.keyParts(userKey, now)
	dayTTL := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, now.Location()).Sub(now)
	_, _ = s.client.Eval(context.Background(), mobileRedisAddTokensScript, []string{tokenKey}, tokens, int(dayTTL.Seconds())).Result()
}

func (s *RedisMobileUsageStore) Release(userKey string, estimatedTokens int) {
	if s == nil || s.client == nil {
		return
	}
	now := s.now()
	minuteTTL := int(now.Truncate(time.Minute).Add(time.Minute + time.Second).Sub(now).Seconds())
	dayTTL := int(time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 1, 0, now.Location()).Sub(now).Seconds())
	_, _ = s.client.Eval(context.Background(), mobileRedisReleaseScript, s.keys(userKey, now), estimatedTokens, minuteTTL, dayTTL).Result()
}

func (s *RedisMobileUsageStore) keys(userKey string, now time.Time) []string {
	minuteKey, messageKey, tokenKey := s.keyParts(userKey, now)
	return []string{minuteKey, messageKey, tokenKey}
}

func (s *RedisMobileUsageStore) keyParts(userKey string, now time.Time) (string, string, string) {
	userKey = strings.NewReplacer(" ", "_", "\n", "_", "\t", "_").Replace(strings.TrimSpace(userKey))
	if userKey == "" {
		userKey = "anonymous"
	}
	minute := strconv.FormatInt(now.Unix()/60, 10)
	day := now.Format("20060102")
	base := s.keyPrefix + ":" + userKey
	return base + ":minute:" + minute, base + ":day_messages:" + day, base + ":day_tokens:" + day
}
