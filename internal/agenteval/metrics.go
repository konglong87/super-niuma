package agenteval

import (
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/query"
)

// gatePreflightMarker is the fixed phrase in closure gate auto-preflight output,
// used to count gate friction events and also to trigger retry in scripted git workflows.
// It is derived from query.GatePreflightMarker so the two packages never drift.
const gatePreflightMarker = query.GatePreflightMarker

type CaseMetrics struct {
	Turns          int `json:"turns,omitempty"`
	ToolCalls      int `json:"tool_calls,omitempty"`
	GatePreflights int `json:"gate_preflights,omitempty"`
	InputTokens    int `json:"input_tokens,omitempty"`
	OutputTokens   int `json:"output_tokens,omitempty"`
}

func collectCaseMetrics(response query.Result) CaseMetrics {
	gates := 0
	for _, call := range response.ToolCalls {
		if strings.Contains(call.Output, gatePreflightMarker) {
			gates++
		}
	}
	return CaseMetrics{
		Turns:          response.Turns,
		ToolCalls:      len(response.ToolCalls),
		GatePreflights: gates,
		InputTokens:    response.Usage.InputTokens,
		OutputTokens:   response.Usage.OutputTokens,
	}
}

func addBudgetChecks(result *CaseResult, metrics CaseMetrics, testCase Case) {
	if testCase.ExpectMaxTurns > 0 {
		addCheck(result, "budget.max_turns", metrics.Turns <= testCase.ExpectMaxTurns,
			fmt.Sprintf("turns %d within budget %d", metrics.Turns, testCase.ExpectMaxTurns))
	}
	if testCase.ExpectMaxGatePreflights != nil {
		addCheck(result, "budget.max_gate_preflights", metrics.GatePreflights <= *testCase.ExpectMaxGatePreflights,
			fmt.Sprintf("gate preflights %d within budget %d", metrics.GatePreflights, *testCase.ExpectMaxGatePreflights))
	}
}
