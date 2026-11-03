package goal

import (
	"fmt"
	"strings"
	"time"
)

type Planner interface {
	BuildInitialPlan(goal Goal, now time.Time) (GoalPlan, error)
}

type DeterministicPlanner struct{}

func (p DeterministicPlanner) BuildInitialPlan(item Goal, now time.Time) (GoalPlan, error) {
	if item.ID == "" {
		return GoalPlan{}, fmt.Errorf("goal id is required")
	}
	objective := strings.TrimSpace(item.Objective)
	if objective == "" {
		return GoalPlan{}, fmt.Errorf("goal objective is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	criteria := parseObjectiveCriteria(objective)
	if len(criteria) == 0 {
		criteria = []GoalCriterion{{
			ID:          "crit_objective_satisfied",
			Description: "Objective is satisfied: " + objective,
			Required:    true,
			Status:      CriterionStatusPending,
		}}
	}
	plan := GoalPlan{
		GoalID:             item.ID,
		Version:            1,
		Summary:            objective,
		CurrentStepID:      "step_execute",
		AcceptanceCriteria: criteria,
		Steps: []GoalStep{{
			ID:        "step_plan",
			Title:     "Clarify objective and constraints",
			Status:    StepStatusPending,
			Rationale: "Confirm what must be true before execution is considered done.",
		}, {
			ID:        "step_execute",
			Title:     "Execute the next useful change",
			Status:    StepStatusActive,
			DependsOn: []string{"step_plan"},
			Rationale: "Make concrete progress against the objective with the available tools.",
		}, {
			ID:        "step_verify",
			Title:     "Verify acceptance criteria",
			Status:    StepStatusPending,
			DependsOn: []string{"step_execute"},
			Rationale: "Collect evidence before allowing completion.",
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := plan.Validate(); err != nil {
		return GoalPlan{}, err
	}
	return plan, nil
}

func parseObjectiveCriteria(objective string) []GoalCriterion {
	lines := strings.Split(objective, "\n")
	var criteria []GoalCriterion
	capturing := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		normalized := strings.ToLower(strings.TrimRight(line, ":："))
		if isAcceptanceHeading(normalized) {
			capturing = true
			continue
		}
		if !capturing {
			continue
		}
		if isLikelyNewSection(normalized) {
			break
		}
		description := cleanCriterionLine(line)
		if description == "" {
			continue
		}
		criteria = append(criteria, GoalCriterion{
			ID:          fmt.Sprintf("crit_%02d", len(criteria)+1),
			Description: description,
			Required:    true,
			Status:      CriterionStatusPending,
		})
	}
	return criteria
}

func isAcceptanceHeading(line string) bool {
	switch line {
	case "acceptance criteria", "acceptance", "验收标准", "验收条件", "完成标准", "完成条件":
		return true
	default:
		return false
	}
}

func isLikelyNewSection(line string) bool {
	switch line {
	case "notes", "note", "constraints", "constraint", "限制", "约束", "背景", "说明":
		return true
	default:
		return strings.HasSuffix(line, ":") || strings.HasSuffix(line, "：")
	}
}

func cleanCriterionLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-*• \t")
	if len(line) >= 2 && line[0] >= '0' && line[0] <= '9' {
		rest := line[1:]
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			rest = rest[1:]
		}
		rest = strings.TrimLeft(rest, ".、)） \t")
		if rest != "" {
			line = rest
		}
	}
	return strings.TrimSpace(line)
}
