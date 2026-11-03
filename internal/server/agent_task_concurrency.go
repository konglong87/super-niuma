package server

import (
	"fmt"
	"sync"
)

// defaultAgentTaskMaxConcurrentRuns 是 detached agent runner 的默认并发上限。
// 每个 run 都是一条完整的 LLM 会话（最长 agentTaskRunTimeout，默认 20 分钟）外加
// 工具执行，所以默认取一个保守值：宁可显式拒绝，也不要静默排队到 OOM。
const defaultAgentTaskMaxConcurrentRuns = 16

// agentTaskRunLimiter 给 detached runner 记数。超限时 acquire 立刻返回 false，
// 调用方据此返回明确错误——这里不排队，排队等于把压力转成内存占用。
type agentTaskRunLimiter struct {
	mu     sync.Mutex
	active int
	max    int
}

func newAgentTaskRunLimiter(max int) *agentTaskRunLimiter {
	if max <= 0 {
		max = defaultAgentTaskMaxConcurrentRuns
	}
	return &agentTaskRunLimiter{max: max}
}

// acquire 占用一个槽位，返回的 release 可重复调用（只生效一次）。
func (l *agentTaskRunLimiter) acquire() (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.max {
		return func() {}, false
	}
	l.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.active > 0 {
				l.active--
			}
		})
	}, true
}

func (l *agentTaskRunLimiter) limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.max
}

func (l *agentTaskRunLimiter) inFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// atCapacityMessage 返回给客户端的拒绝原因，带上具体上限方便运维调参。
func (l *agentTaskRunLimiter) atCapacityMessage() string {
	return fmt.Sprintf("agent task runner is at capacity (%d concurrent runs); retry later", l.limit())
}
