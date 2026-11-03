package anthropic

import (
	"sync"
	"time"
)

// providerCooldown 是一个 provider 在可 fallback 的失败之后被跳过的时长。
//
// 没有它，`providers` 列表就没有失败记忆：primary 挂掉之后每一轮都从 primary 重新开始，
// 每轮都先赔上一整个建流超时才切到 fallback（AUDIT-P1-08）。做成变量是为了让测试压到
// 毫秒级 —— 与 providerTimeouts、openAIStreamCreateBackoff 同一个理由。置 0 即关闭。
var providerCooldown = 60 * time.Second

// providerBreaker 记住哪些 provider 刚刚失败过。
//
// 状态挂在 Client 上而不是包级全局：CLI 的会话 client 是长命的（一个会话一个，跨轮共享），
// 正好是需要失败记忆的那个作用域；recap / agenteval 那几个调用点每次新建 client 然后丢掉，
// 一次性调用本来就无从记忆，全局状态只会让测试互相污染。
//
// 必须加锁：同一个 Client 会被 batch 子代理并发使用（Task 默认 4 路、最多 16 路）。
type providerBreaker struct {
	mu    sync.Mutex
	now   func() time.Time
	until map[string]time.Time
}

func (b *providerBreaker) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// trip 让 name 进入冷却。
func (b *providerBreaker) trip(name string) {
	if providerCooldown <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.until == nil {
		b.until = make(map[string]time.Time)
	}
	b.until[name] = b.clock().Add(providerCooldown)
}

// reset 在一次成功之后清掉 name 的冷却，让恢复了的 provider 立刻回到队首。
func (b *providerBreaker) reset(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.until, name)
}

// cooling 返回 providers 里仍在冷却中的那些，值是剩余时长（只用于错误文案）。
//
// 全员都在冷却时返回 nil：宁可试一个可能已经恢复的 provider，也不要一次都不试就报错 ——
// 那会把一次短暂的全域抖动变成整段会话不可用。
func (b *providerBreaker) cooling(providers []providerClient) map[string]time.Duration {
	if providerCooldown <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.until) == 0 {
		return nil
	}
	now := b.clock()
	out := make(map[string]time.Duration, len(b.until))
	for _, provider := range providers {
		until, ok := b.until[provider.name]
		if !ok {
			continue
		}
		if remaining := until.Sub(now); remaining > 0 {
			out[provider.name] = remaining
			continue
		}
		// 过期了就顺手清掉，免得 map 随 provider 名字无限长大。
		delete(b.until, provider.name)
	}
	if len(out) == 0 || len(out) == len(providers) {
		return nil
	}
	return out
}
