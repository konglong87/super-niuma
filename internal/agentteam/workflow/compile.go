package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var identifier = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string  { return e.Field + ": " + e.Message }
func invalid(field, message string) error { return &ValidationError{field, message} }

// Compile normalizes a static DAG and produces a canonical, immutable plan.
// It never loads configuration, executes prompts or infers authority from roles.
func Compile(def Definition, agents []Agent) (Plan, error) {
	if def.SchemaVersion != SchemaVersion {
		return Plan{}, invalid("schema_version", "only Team workflow schema 2 is supported")
	}
	if len(def.Nodes) == 0 || len(def.Nodes) > MaxNodes {
		return Plan{}, invalid("nodes", fmt.Sprintf("choose 1 to %d steps", MaxNodes))
	}
	if len(agents) == 0 || len(agents) > MaxNodes {
		return Plan{}, invalid("agents", "choose at least one Agent and no more than 16")
	}
	def.Limits = normalizedLimits(def.Limits)
	if def.Limits.MaxParallel < 1 || def.Limits.MaxParallel > MaxParallel {
		return Plan{}, invalid("limits.max_parallel", "parallel steps must be between 1 and 4")
	}
	if def.Limits.RunTimeoutSeconds < 1 || def.Limits.RunTimeoutSeconds > MaxRunSeconds {
		return Plan{}, invalid("limits.run_timeout_seconds", "run timeout must be between 1 and 1800 seconds")
	}
	if def.Limits.MaxTotalTokens < 1 || def.Limits.MaxTotalTokens > MaxTotalTokens {
		return Plan{}, invalid("limits.max_total_tokens", "token budget must be between 1 and 128000")
	}
	copied := append([]Agent(nil), agents...)
	byAgent := map[string]Agent{}
	for i, a := range copied {
		a.Ref = strings.TrimSpace(a.Ref)
		if !identifier.MatchString(a.Ref) {
			return Plan{}, invalid("agents.ref", "use a unique letter-led key of at most 64 characters")
		}
		if _, ok := byAgent[a.Ref]; ok {
			return Plan{}, invalid("agents.ref", "duplicate Agent reference")
		}
		if a.ProfileKey == "" || a.ProfileVersion == 0 {
			return Plan{}, invalid("agents.profile", "select a fixed published Agent version")
		}
		if a.Kind == "" {
			a.Kind = "text"
		}
		if a.Kind != "text" {
			return Plan{}, invalid("agents.kind", "this workflow supports text Agents only; desktop and image authority cannot be delegated")
		}
		if len(a.Tools) > 0 {
			return Plan{}, invalid("agents.tools", "this release supports explicit no-tool workflows only; no shell, desktop, MCP or Agent delegation")
		}
		if len(a.SystemPrompt) > MaxInputBytes/2 {
			return Plan{}, invalid("agents.system_prompt", "Agent instructions exceed the context budget")
		}
		if a.MaxOutputTokens == 0 {
			a.MaxOutputTokens = 2048
		}
		if a.MaxOutputTokens < 1 || a.MaxOutputTokens > 8192 {
			return Plan{}, invalid("agents.max_output_tokens", "choose an output limit between 1 and 8192 tokens")
		}
		a.Tools = []string{}
		copied[i] = a
		byAgent[a.Ref] = a
	}
	def.Nodes = append([]Node(nil), def.Nodes...)
	byNode := map[string]int{}
	for i, n := range def.Nodes {
		n.ID = strings.TrimSpace(n.ID)
		n.AgentRef = strings.TrimSpace(n.AgentRef)
		n.Instruction = strings.TrimSpace(n.Instruction)
		if !identifier.MatchString(n.ID) {
			return Plan{}, invalid("nodes.id", "step keys must start with a letter, using letters, digits, _ or -")
		}
		if _, ok := byNode[n.ID]; ok {
			return Plan{}, invalid("nodes.id", "duplicate step key")
		}
		if _, ok := byAgent[n.AgentRef]; !ok {
			return Plan{}, invalid("nodes.agent_ref", "step references an unavailable Agent")
		}
		if len(n.Instruction) > 8192 {
			return Plan{}, invalid("nodes.instruction", "step instructions are too long")
		}
		if n.TimeoutSeconds == 0 {
			n.TimeoutSeconds = DefaultNodeSeconds
		}
		if n.TimeoutSeconds < 1 || n.TimeoutSeconds > MaxNodeSeconds {
			return Plan{}, invalid("nodes.timeout_seconds", "step timeout must be between 1 and 300 seconds")
		}
		if n.Retry.MaxAttempts == 0 {
			n.Retry.MaxAttempts = DefaultAttempts
		}
		if n.Retry.MaxAttempts < 1 || n.Retry.MaxAttempts > MaxAttempts {
			return Plan{}, invalid("nodes.retry.max_attempts", "attempts include the first execution and must be between 1 and 3")
		}
		if n.Retry.BackoffMS == 0 {
			n.Retry.BackoffMS = 1000
		}
		if n.Retry.BackoffMS < 1 || n.Retry.BackoffMS > 10000 {
			return Plan{}, invalid("nodes.retry.backoff_ms", "retry delay must be between 1 and 10000 milliseconds")
		}
		n.DependsOn = append([]string{}, n.DependsOn...)
		sort.Strings(n.DependsOn)
		def.Nodes[i] = n
		byNode[n.ID] = i
	}
	for _, n := range def.Nodes {
		seen := map[string]bool{}
		for _, dep := range n.DependsOn {
			if _, ok := byNode[dep]; !ok || dep == n.ID {
				return Plan{}, invalid("nodes.depends_on", "dependencies must reference another existing step")
			}
			if seen[dep] {
				return Plan{}, invalid("nodes.depends_on", "duplicate dependency")
			}
			seen[dep] = true
		}
	}
	// Stable Kahn ordering makes editing, snapshots and retry replay deterministic.
	ordered := make([]Node, 0, len(def.Nodes))
	done := map[string]bool{}
	for len(ordered) < len(def.Nodes) {
		found := false
		for _, n := range def.Nodes {
			if !done[n.ID] && dependenciesDone(n.DependsOn, done) {
				ordered = append(ordered, n)
				done[n.ID] = true
				found = true
			}
		}
		if !found {
			return Plan{}, invalid("nodes.depends_on", "dependencies contain a cycle; a step cannot wait on itself indirectly")
		}
	}
	def.Nodes = ordered
	sort.Slice(copied, func(i, j int) bool { return copied[i].Ref < copied[j].Ref })
	plan := Plan{Definition: def, Agents: copied}
	raw, _ := json.Marshal(plan)
	sum := sha256.Sum256(raw)
	plan.Hash = hex.EncodeToString(sum[:])
	return plan, nil
}
func normalizedLimits(l Limits) Limits {
	if l.MaxParallel == 0 {
		l.MaxParallel = DefaultParallel
	}
	if l.RunTimeoutSeconds == 0 {
		l.RunTimeoutSeconds = DefaultRunSeconds
	}
	if l.MaxTotalTokens == 0 {
		l.MaxTotalTokens = DefaultMaxTokens
	}
	return l
}
func dependenciesDone(deps []string, done map[string]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}
func (p Plan) Agent(ref string) (Agent, bool) {
	for _, a := range p.Agents {
		if a.Ref == ref {
			return a, true
		}
	}
	return Agent{}, false
}
func (p Plan) Node(id string) (Node, bool) {
	for _, n := range p.Definition.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}
func (r Run) Node(id string) (NodeRun, bool) {
	for _, n := range r.Nodes {
		if n.NodeID == id {
			return n, true
		}
	}
	return NodeRun{}, false
}
