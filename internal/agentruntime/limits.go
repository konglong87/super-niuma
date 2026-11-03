package agentruntime

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	// EnvMaxSubagentDepth overrides DefaultMaxSubagentDepth.
	EnvMaxSubagentDepth = "NIUMA_MAX_SUBAGENT_DEPTH"
	// EnvMaxBackgroundAgents overrides defaultMaxBackgroundAgents.
	EnvMaxBackgroundAgents = "NIUMA_MAX_BACKGROUND_AGENTS"

	// DefaultMaxSubagentDepth allows a sub-agent to delegate one further level
	// and stops there. The parent conversation is depth 0, the sub-agent it
	// spawns is depth 1, and that sub-agent's own sub-agent is depth 2 — the
	// last one accepted. Anything deeper is a fan-out multiplier, not useful
	// division of labour, and general-purpose keeps Tools:["*"] so nothing else
	// bounds it (AUDIT-P0-14).
	DefaultMaxSubagentDepth = 2

	// defaultMaxBackgroundAgents caps detached sub-agent goroutines. Each one is
	// a full LLM conversation plus tool execution running on a context detached
	// from the caller, so the conservative value matches the server-side
	// agentTaskRunLimiter default.
	defaultMaxBackgroundAgents = 16
)

// MaxSubagentDepth resolves the recursion ceiling for sub-agent runs.
func MaxSubagentDepth() int {
	return envInt(EnvMaxSubagentDepth, DefaultMaxSubagentDepth, 1)
}

// depthLimitError explains a rejected nesting attempt in terms the model can
// act on: it names the depth reached, the ceiling, and the override.
func depthLimitError(depth, max int) error {
	return fmt.Errorf("sub-agent recursion depth limit reached: this would be sub-agent depth %d but the limit is %d; do the remaining work directly instead of delegating again (raise %s only if deeper nesting is genuinely required)",
		depth, max, EnvMaxSubagentDepth)
}

// backgroundAgentLimiter counts detached sub-agent runs process-wide. Detached
// runs outlive the request that started them, so a per-call limiter would not
// see them; the counter has to be shared.
//
// Over the limit acquire fails immediately rather than queueing — queueing just
// converts pressure into memory and hides the overload from the caller, the same
// reasoning as internal/server/agent_task_concurrency.go.
type backgroundAgentLimiter struct {
	mu     sync.Mutex
	active int
	max    int
}

var backgroundAgents = &backgroundAgentLimiter{}

func (l *backgroundAgentLimiter) limit() int {
	if l.max > 0 {
		return l.max
	}
	return envInt(EnvMaxBackgroundAgents, defaultMaxBackgroundAgents, 1)
}

// acquire takes a slot. The returned release is idempotent.
func (l *backgroundAgentLimiter) acquire() (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active >= l.limit() {
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

func (l *backgroundAgentLimiter) inFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

func (l *backgroundAgentLimiter) atCapacityError() error {
	return fmt.Errorf("background sub-agent limit reached: %d background sub-agents are already running; wait for one to finish (AgentGet / AgentList) or raise %s",
		l.limit(), EnvMaxBackgroundAgents)
}

func envInt(key string, fallback, min int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return fallback
	}
	return value
}
