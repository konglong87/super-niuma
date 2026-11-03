package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisLeaseRenewScript   = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`
	redisLeaseReleaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
)

type RedisLease struct {
	client redis.Cmdable
	prefix string
}

func NewRedisLease(client redis.Cmdable, prefix string) *RedisLease {
	if prefix == "" {
		prefix = "niuma:channel"
	}
	return &RedisLease{client: client, prefix: prefix}
}

func NewRedisLeaseFromAddr(addr, password string, db int, prefix string) *RedisLease {
	return NewRedisLease(redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db}), prefix)
}

func (l *RedisLease) Acquire(ctx context.Context, tenantID, accountID uint64, workerID string, ttl time.Duration) (func(), error) {
	if l == nil || l.client == nil {
		return nil, errors.New("redis lease is not configured")
	}
	if tenantID == 0 || accountID == 0 || workerID == "" || ttl <= 0 {
		return nil, errors.New("invalid channel lease input")
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := workerID + ":" + hex.EncodeToString(tokenBytes)
	key := fmt.Sprintf("%s:account:%d:%d", l.prefix, tenantID, accountID)
	ok, err := l.client.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("channel account lease is held")
	}
	leaseCtx, cancel := context.WithCancel(context.Background())
	interval := ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				_, _ = l.client.Eval(context.Background(), redisLeaseRenewScript, []string{key}, token, ttl.Milliseconds()).Result()
			}
		}
	}()
	return func() {
		cancel()
		_, _ = l.client.Eval(context.Background(), redisLeaseReleaseScript, []string{key}, token).Result()
	}, nil
}

func (l *RedisLease) Ping(ctx context.Context) error {
	if l == nil || l.client == nil {
		return errors.New("redis lease is not configured")
	}
	return l.client.Ping(ctx).Err()
}
