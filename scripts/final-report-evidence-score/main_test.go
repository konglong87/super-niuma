package main

import "testing"

func TestScoreRunLogAcceptsAnchoredFinalReport(t *testing.T) {
	text := `
P0 Evidence: internal/query/query.go:3769 runtimeStatusText builds runtime status and internal/tools/task/task.go:79 Tool.Description defines Task delegation.
P1 Evidence: internal/tools/agent/agent.go:490 AgentGet describes progress retrieval and internal/agentruntime/runtime.go:1475 defines sub-agent evidence-first output.
Unknowns: production transcript quality was not sampled; this is bounded by /tmp/final-report.jsonl.
Verification: go test ./internal/query -count=1 and scripts/final-report-quality-prompt-acceptance.sh passed.
Risks: internal/tools/task/task.go:176 runSingle synchronous behavior can still differ under real provider latency.
Next action: rerun scripts/final-report-quality-prompt-acceptance.sh after prompt/runtime edits.
runtimeStatusText runtimeTodoStatus runtimePlanStatus runtimeAgentTaskStatus subagentToolPlanningStatus
Tool.Run runSingle AgentCreate AgentGet
`
	rep := scoreRunLog("run.log", text, 10, splitCSV("internal/query/query.go,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go"), splitCSV("runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,subagentToolPlanningStatus,Tool.Description,Tool.Run,runSingle,AgentCreate,AgentGet"), nil)
	if !rep.OK {
		t.Fatalf("expected score report ok, missing=%v report=%+v", rep.Missing, rep)
	}
	if rep.Score < rep.RequiredScore {
		t.Fatalf("score = %d, required %d", rep.Score, rep.RequiredScore)
	}
}

func TestScoreRunLogRejectsKeywordOnlyFinalReport(t *testing.T) {
	text := `
Evidence: trust me.
Unknowns: something.
Verification: checked.
Risks: maybe.
Next action: continue.
runtimeStatusText runtimeTodoStatus runtimePlanStatus runtimeAgentTaskStatus subagentToolPlanningStatus
Tool.Description Tool.Run runSingle AgentCreate AgentGet
`
	rep := scoreRunLog("run.log", text, 10, splitCSV("internal/query/query.go,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go"), splitCSV("runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,subagentToolPlanningStatus,Tool.Description,Tool.Run,runSingle,AgentCreate,AgentGet"), nil)
	if rep.OK {
		t.Fatalf("keyword-only score report should fail: %+v", rep)
	}
	if rep.Checks["section:evidence"] {
		t.Fatalf("keyword-only evidence should not be anchored: %+v", rep)
	}
	if rep.Checks["verification_has_command_or_dump"] {
		t.Fatalf("keyword-only verification should not be anchored: %+v", rep)
	}
}

func TestScoreRunLogKeepsNestedEvidenceHeadings(t *testing.T) {
	text := "\n" +
		"## Evidence:\n\n" +
		"### `internal/query/query.go` - runtime status context\n\n" +
		"- `internal/query/query.go:3491-3503` withRuntimeStatusMessages appends runtimeStatusText output into request messages.\n" +
		"- `internal/query/query.go:3505-3528` runtimeStatusText calls runtimeTodoStatus, runtimePlanStatus, and runtimeAgentTaskStatus.\n\n" +
		"### `internal/tools/task/task.go` - Task lifecycle\n\n" +
		"- `internal/tools/task/task.go:79-118` Tool.Description documents Task delegation.\n" +
		"- `internal/tools/task/task.go:176-230` runSingle executes the child task and returns structured content.\n\n" +
		"### `internal/tools/agent/agent.go` - Agent lifecycle\n\n" +
		"- `internal/tools/agent/agent.go:490-529` AgentGet retrieves background progress and results after AgentCreate.\n\n" +
		"### `internal/agentruntime/runtime.go` - subagent planning\n\n" +
		"- `internal/agentruntime/runtime.go:987-999` subagentToolPlanningStatus summarizes child tool-use state.\n\n" +
		"## Unknowns:\n\n" +
		"- Production transcript quality still needs sampling from /tmp/final-report.jsonl.\n\n" +
		"## Verification:\n\n" +
		"- go test ./scripts/final-report-evidence-score -count=1 and scripts/final-report-quality-prompt-acceptance.sh --verify-only passed.\n\n" +
		"## Risks:\n\n" +
		"- Provider variance may still cause missing Read coverage in future live runs.\n\n" +
		"## Next action:\n\n" +
		"- Rerun scripts/final-report-quality-prompt-acceptance.sh after prompt hardening.\n\n" +
		"runtimeStatusText runtimeTodoStatus runtimePlanStatus runtimeAgentTaskStatus subagentToolPlanningStatus\n" +
		"Tool.Run AgentCreate AgentGet\n"
	rep := scoreRunLog("run.log", text, 20, splitCSV("internal/query/query.go,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go"), splitCSV("runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,subagentToolPlanningStatus,Tool.Description,Tool.Run,runSingle,AgentCreate,AgentGet"), nil)
	if !rep.OK {
		t.Fatalf("nested evidence headings should score ok, missing=%v report=%+v", rep.Missing, rep)
	}
	if !rep.Checks["section:evidence"] {
		t.Fatalf("nested evidence block should count as anchored evidence: %+v", rep)
	}
	if len(rep.Anchors) < 4 {
		t.Fatalf("expected at least four file anchors, got %v", rep.Anchors)
	}
}

func TestScoreRunLogScoresVisibleOutputBeforeTelemetry(t *testing.T) {
	text := `
Evidence: trust me.
Unknowns: something.
Verification: checked.
Risks: maybe.
Next action: continue.
{"level":"info","msg":"telemetry","properties":{"request":"internal/query/query.go internal/tools/task/task.go internal/tools/agent/agent.go internal/agentruntime/runtime.go runtimeStatusText runtimeTodoStatus runtimePlanStatus runtimeAgentTaskStatus subagentToolPlanningStatus Tool.Description Tool.Run runSingle AgentCreate AgentGet"}}
`
	rep := scoreRunLog("run.log", text, 10, splitCSV("internal/query/query.go,internal/tools/task/task.go,internal/tools/agent/agent.go,internal/agentruntime/runtime.go"), splitCSV("runtimeStatusText,runtimeTodoStatus,runtimePlanStatus,runtimeAgentTaskStatus,subagentToolPlanningStatus,Tool.Description,Tool.Run,runSingle,AgentCreate,AgentGet"), nil)
	if rep.OK {
		t.Fatalf("telemetry-only anchors should not satisfy visible final report score: %+v", rep)
	}
	if rep.ScoredScope != "visible_output_before_json_logs" {
		t.Fatalf("unexpected scored scope: %s", rep.ScoredScope)
	}
	if rep.Checks["term:AgentGet"] {
		t.Fatalf("telemetry terms should not be counted: %+v", rep)
	}
}
