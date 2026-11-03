package query

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/konglong87/super-niuma/internal/agentbudget"
	"github.com/konglong87/super-niuma/internal/tools"
)

// budgetProbeTool captures the AgentBudget handed down through tools.Context.
type budgetProbeTool struct {
	budgets []*agentbudget.Budget
}

func (*budgetProbeTool) Name() string                 { return "Task" }
func (*budgetProbeTool) Description() string          { return "capture the session sub-agent budget" }
func (*budgetProbeTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *budgetProbeTool) Run(_ context.Context, _ json.RawMessage, toolContext tools.Context) tools.Result {
	t.budgets = append(t.budgets, toolContext.AgentBudget)
	return tools.Result{Content: "probed"}
}

// TestSessionHandsSubagentBudgetToTools pins the wiring itself. Session creates
// the budget in one line and passes it in one more; both are easy to lose in a
// refactor and nothing else in the suite would notice, because a nil budget just
// means "unlimited" and every other test keeps passing (AUDIT-P0-14).
func TestSessionHandsSubagentBudgetToTools(t *testing.T) {
	probe := &budgetProbeTool{}
	session := New(&taskProgressStreamer{}, tools.NewRegistry(probe), Options{
		Model:    "test",
		MaxTurns: 3,
		CWD:      t.TempDir(),
	})
	if _, err := session.Run(context.Background(), "probe the budget", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(probe.budgets) == 0 {
		t.Fatal("the probe tool never ran")
	}
	if probe.budgets[0] == nil {
		t.Fatal("tools.Context.AgentBudget was nil; Session must hand its sub-agent budget to every tool or the cumulative ceiling silently becomes unlimited")
	}
	if probe.budgets[0] != session.agentBudget {
		t.Fatal("tools got a different budget than the session holds; nested sub-agents must share one counter")
	}
	if limits := probe.budgets[0].Snapshot().Limits; limits.MaxTotalTokens != agentbudget.DefaultMaxTotalTokens {
		t.Fatalf("session budget limits = %+v, want the %d-token default", limits, agentbudget.DefaultMaxTotalTokens)
	}
}
