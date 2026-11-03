package goal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ReachabilityAnalyzer interface {
	Analyze(ctx context.Context, goal Goal, plan GoalPlan) (ReachabilityResult, error)
}

type ReachabilityResult struct {
	Dependencies []GoalDependency
	Risks        []GoalRisk
	Blocked      bool
	BlockerKey   string
	Reason       string
}

type DeterministicReachabilityAnalyzer struct{}

func (a DeterministicReachabilityAnalyzer) Analyze(ctx context.Context, item Goal, plan GoalPlan) (ReachabilityResult, error) {
	if err := ctx.Err(); err != nil {
		return ReachabilityResult{}, err
	}
	var result ReachabilityResult
	result.Dependencies = append(result.Dependencies, cwdDependency(item.CWD))
	result.Risks = append(result.Risks, objectiveRisks(item.Objective, item)...)
	result.Blocked, result.BlockerKey, result.Reason = reachabilityBlocker(result.Dependencies, result.Risks)
	return result, nil
}

func ApplyReachability(plan GoalPlan, result ReachabilityResult, now time.Time) GoalPlan {
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	plan.Dependencies = mergeDependencies(plan.Dependencies, result.Dependencies)
	plan.Risks = mergeRisks(plan.Risks, result.Risks)
	plan.UpdatedAt = now
	return plan
}

func cwdDependency(cwd string) GoalDependency {
	cwd = strings.TrimSpace(cwd)
	dep := GoalDependency{
		ID:          "dep_cwd_writable",
		Type:        "local_workspace",
		Description: "Working directory exists and is writable",
		Required:    true,
		Status:      DependencyStatusAvailable,
	}
	if cwd == "" {
		dep.Status = DependencyStatusMissing
		dep.Description = "Working directory is empty"
		return dep
	}
	info, err := os.Stat(cwd)
	if err != nil {
		dep.Status = DependencyStatusMissing
		dep.Description = fmt.Sprintf("Working directory is unavailable: %s", err)
		return dep
	}
	if !info.IsDir() {
		dep.Status = DependencyStatusMissing
		dep.Description = "Working directory is not a directory: " + cwd
		return dep
	}
	tmp, err := os.CreateTemp(cwd, ".goal-reachability-*")
	if err != nil {
		dep.Status = DependencyStatusBlocked
		dep.Description = fmt.Sprintf("Working directory is not writable: %s", err)
		return dep
	}
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
	dep.Description = "Working directory is writable: " + filepath.Clean(cwd)
	return dep
}

func objectiveRisks(objective string, item Goal) []GoalRisk {
	text := strings.ToLower(objective)
	var risks []GoalRisk
	if containsAny(text, "api key", "token", "credential", "secret", "oauth", "登录", "凭证", "密钥") {
		risks = append(risks, GoalRisk{
			ID:          "risk_missing_credential",
			Type:        "missing_credential",
			Description: "Objective appears to require credentials or secret material.",
			Severity:    RiskSeverityHigh,
			Mitigation:  "Use existing configured credentials or ask the user for a safe access path before continuing.",
			Status:      RiskStatusOpen,
		})
	}
	if containsAny(text, "deploy", "production", "external service", "third-party", "第三方", "外部服务", "生产环境", "部署") {
		risks = append(risks, GoalRisk{
			ID:          "risk_external_dependency",
			Type:        "external_dependency",
			Description: "Objective may depend on an external or production system.",
			Severity:    RiskSeverityMedium,
			Mitigation:  "Verify the external access path before spending turns on implementation.",
			Status:      RiskStatusOpen,
		})
	}
	if containsAny(text, "ask user", "need user", "confirm", "clarify", "需要用户", "确认", "澄清") {
		risks = append(risks, GoalRisk{
			ID:          "risk_needs_user_input",
			Type:        "needs_user_input",
			Description: "Objective indicates user input or confirmation may be required.",
			Severity:    RiskSeverityMedium,
			Mitigation:  "Ask a focused question only when local evidence cannot resolve the ambiguity.",
			Status:      RiskStatusOpen,
		})
	}
	if containsAny(text, "rm -rf", "git reset --hard", "git clean", "force push", "--force", "删除所有", "强制推送") {
		risks = append(risks, GoalRisk{
			ID:          "risk_unsafe_destructive_action",
			Type:        "unsafe_destructive_action",
			Description: "Objective mentions a destructive operation that must not run without explicit approval.",
			Severity:    RiskSeverityCritical,
			Mitigation:  "Stop before destructive execution and require explicit user approval.",
			Status:      RiskStatusOpen,
		})
	}
	if item.TurnBudget > 0 && item.TurnBudget-item.TurnsUsed <= 1 {
		risks = append(risks, GoalRisk{
			ID:          "risk_turn_budget_low",
			Type:        "budget",
			Description: "Only one turn remains in the goal budget.",
			Severity:    RiskSeverityMedium,
			Mitigation:  "Prefer verification, summary, or blocked over expanding scope.",
			Status:      RiskStatusOpen,
		})
	}
	return risks
}

func reachabilityBlocker(dependencies []GoalDependency, risks []GoalRisk) (bool, string, string) {
	for _, dep := range dependencies {
		if dep.Required && (dep.Status == DependencyStatusMissing || dep.Status == DependencyStatusBlocked) {
			return true, dep.ID, dep.Description
		}
	}
	for _, risk := range risks {
		if risk.Status != RiskStatusOpen {
			continue
		}
		switch risk.Severity {
		case RiskSeverityHigh, RiskSeverityCritical:
			return true, risk.ID, risk.Description
		}
	}
	return false, "", ""
}

func mergeDependencies(existing, incoming []GoalDependency) []GoalDependency {
	out := append([]GoalDependency(nil), existing...)
	index := make(map[string]int, len(out))
	for i, item := range out {
		index[item.ID] = i
	}
	for _, item := range incoming {
		if pos, ok := index[item.ID]; ok {
			out[pos] = item
			continue
		}
		index[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeRisks(existing, incoming []GoalRisk) []GoalRisk {
	out := append([]GoalRisk(nil), existing...)
	index := make(map[string]int, len(out))
	for i, item := range out {
		index[item.ID] = i
	}
	for _, item := range incoming {
		if pos, ok := index[item.ID]; ok {
			out[pos] = item
			continue
		}
		index[item.ID] = len(out)
		out = append(out, item)
	}
	return out
}
