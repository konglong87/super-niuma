package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/quota"
)

// AUDIT-P1-27: 租户配额只在调用方带了非默认 tenant/user header 时生效
// （reserveQueryQuota → tenantPersistenceRequested）。也就是说，**不带
// X-Tenant-Key 就绕过全部配额**，而 /query 与 /v1/chat/completions 此前没有任何
// IP 或全局兜底 —— 匿名调用者可以无限打模型。
//
// 这里补的是那条路径上的地板：按客户端 IP 的每分钟固定窗口。它不替代租户配额
// （带 tenant 上下文的请求仍然走 ReserveTenantQuota），只保证「没有租户上下文」
// 不等于「没有限制」。

// defaultQueryRateLimitPerMinute 是兜底限流的默认值。
//
// 选 120 而不是更小：这条路径覆盖的正是本机自用的 /query 调用（本机不带
// tenant header），限得太紧会打断仓库主人每天在跑的工作流。120/min 对一个人类
// 用户绰绰有余，对刷接口的脚本则是实打实的天花板。
const defaultQueryRateLimitPerMinute = 120

// clientRateLimiter 是按 key（客户端 IP）的分钟级固定窗口计数器。
//
// 固定窗口而非滑动窗口：这是兜底地板不是精细计费，窗口边界上最多放行 2 倍额度，
// 换来的是常数内存和零依赖。真正需要精确配额的调用方应该带 tenant 上下文，走
// Redis 后端的 quota.CounterStore。
type clientRateLimiter struct {
	limit int
	now   func() time.Time

	mu      sync.Mutex
	window  time.Time
	counter map[string]int
}

func newClientRateLimiter(limitPerMinute int, now func() time.Time) *clientRateLimiter {
	if limitPerMinute < 0 {
		return nil
	}
	if limitPerMinute == 0 {
		limitPerMinute = defaultQueryRateLimitPerMinute
	}
	if now == nil {
		now = time.Now
	}
	return &clientRateLimiter{limit: limitPerMinute, now: now, counter: map[string]int{}}
}

// allow 记一次调用并返回是否超限。整张表按分钟整体丢弃，所以不需要单独的清理
// goroutine，也不会因为 IP 基数大而无限增长。
func (l *clientRateLimiter) allow(key string) error {
	if l == nil {
		return nil
	}
	if key == "" {
		// 拿不到客户端 IP 时归到同一个桶。宁可把这类请求算作一个共享调用方
		// 一起限，也不要因为 key 为空就整体放行。
		key = "unknown"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	window := l.now().UTC().Truncate(time.Minute)
	if !window.Equal(l.window) {
		l.window = window
		l.counter = make(map[string]int, len(l.counter))
	}
	l.counter[key]++
	if l.counter[key] > l.limit {
		return rateLimitError{limit: l.limit}
	}
	return nil
}

// rateLimitError 让兜底限流的文案自洽。直接 wrap quota.ErrRateLimited 会得到
// 「tenant quota qps limit exceeded」开头 —— 而这条路径恰恰是**没有** tenant 的，
// 那句话只会把排查的人引向租户配置。Unwrap 仍然指向 quota.ErrRateLimited，
// 于是 writeQuotaHTTPError 照常给 429、isQuotaLimitError 照常判定 fail-closed。
type rateLimitError struct{ limit int }

func (e rateLimitError) Error() string {
	return fmt.Sprintf("request rate limit exceeded: %d requests/minute per client; send X-Tenant-Key and X-User-Id to use tenant quota instead", e.limit)
}

func (e rateLimitError) Unwrap() error { return quota.ErrRateLimited }

// rateLimitKeyFromRequest 取 TCP 对端地址，**刻意不看 X-Forwarded-For**。
//
// gin 的 c.ClientIP() 默认信任所有代理，如果拿它当限流 key，攻击者只要每次换一个
// 伪造的 X-Forwarded-For 就能拿到无限额度 —— 那样这道兜底等于不存在。
//
// 代价是：真的架在反向代理后面时，所有匿名请求会落进同一个桶。这是可以接受的，
// 甚至是想要的 —— 多用户部署本来就该带 tenant header，那条路径走的是真正的
// 租户配额，根本不经过这里。
func rateLimitKeyFromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

type clientIPContextKey struct{}

// withClientIP 把客户端 IP 放进 ctx。reserveQueryQuota 拿不到 *http.Request
// （它同时服务 /query、/v1/chat/completions 和 mobile 路径），而兜底限流又必须
// 按调用方区分，所以经由 context 传递。
func withClientIP(ctx context.Context, ip string) context.Context {
	if ip == "" {
		return ctx
	}
	return context.WithValue(ctx, clientIPContextKey{}, ip)
}

func clientIPFromContext(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPContextKey{}).(string)
	return ip
}
