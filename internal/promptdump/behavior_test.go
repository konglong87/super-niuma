package promptdump

import (
	"strings"
	"testing"
)

func TestLoadBehaviorProfileUsesGoFullRequestHistory(t *testing.T) {
	path := writeCompareDump(t, "go-behavior.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":1,"tools_summary":[{"name":"Read"}],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"tools":[{"name":"Read"}],"messages":[{"role":"user","content":[{"type":"text","text":"read file"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_read","name":"Read","input":{"file_path":"README.md"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_read","content":"file body","is_error":false}]}]}}`,
	})

	profile, err := LoadBehaviorProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Format != DumpFormatGo || profile.Model != "claude-test" || profile.RequestCount != 1 {
		t.Fatalf("profile header = %+v", profile)
	}
	if profile.Final == nil {
		t.Fatalf("missing final profile")
	}
	if got := strings.Join(profile.Final.ToolUseSequence, ","); got != "Read" {
		t.Fatalf("tool use sequence = %q", got)
	}
	if got := strings.Join(profile.Final.ToolResultSequence, ","); got != "Read" {
		t.Fatalf("tool result sequence = %q", got)
	}
	if profile.Final.ToolResultBytes != len("file body") || profile.Final.UserTextBlocks != 1 {
		t.Fatalf("final = %+v", profile.Final)
	}
}

func TestCompareBehaviorDumpsReportsBehaviorDifferencesWithoutFailingWarnings(t *testing.T) {
	goPath := writeCompareDump(t, "go-behavior.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":2,"tools_summary":[{"name":"AgentCreate"},{"name":"AgentGet"}],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"tools":[{"name":"AgentCreate"},{"name":"AgentGet"}],"messages":[{"role":"user","content":[{"type":"text","text":"delegate"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_create","name":"AgentCreate","input":{"prompt":"inspect"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_create","content":"{\"task_id\":1}","is_error":false}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_get","name":"AgentGet","input":{"task_id":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_get","content":"done","is_error":false}]}]}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-behavior.jsonl", []string{
		`{"timestamp":"2026-07-03T00:00:00Z","call":1,"body":{"model":"claude-test","max_tokens":32000,"tools":[{"name":"Agent"}],"messages":[{"role":"user","content":[{"type":"text","text":"delegate"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent","name":"Agent","input":{"prompt":"inspect"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent","content":[{"type":"text","text":"done"}],"is_error":false}]}]}}`,
	})

	report, err := CompareBehaviorDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("warnings/info should not fail behavior report: %+v", report)
	}
	joined := behaviorDiffFields(report.Differences)
	for _, want := range []string{"max_tokens", "initial_tool_names", "final.tool_use_sequence", "final.tool_result_sequence", "final.tool_count"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
	if report.Go.Final.ToolResultBytes == 0 || report.Upstream.Final.ToolResultBytes == 0 {
		t.Fatalf("expected tool result bytes in both profiles: go=%+v upstream=%+v", report.Go.Final, report.Upstream.Final)
	}
}

func TestBehaviorProfileSeparatesMainAndSubagentTurns(t *testing.T) {
	path := writeCompareDump(t, "go-subagent-behavior.jsonl", []string{
		`{"schema_version":1,"turn":1,"scope":"main","model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":1,"tools_summary":[{"name":"Task"}],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"tools":[{"name":"Task"}],"messages":[{"role":"user","content":[{"type":"text","text":"delegate"}]}]}}`,
		`{"schema_version":1,"turn":1,"scope":"subagent","model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":0,"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"work"}]}]}}`,
		`{"schema_version":1,"turn":2,"scope":"main","model":"claude-test","max_tokens":4096,"message_count":3,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"delegate"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_task","name":"Task","input":{"prompt":"work"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_task","content":"done"}]}]}}`,
	})

	profile, err := LoadBehaviorProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.RequestCount != 3 || profile.MainRequestCount != 2 {
		t.Fatalf("counts = request %d main %d", profile.RequestCount, profile.MainRequestCount)
	}
	if profile.ScopeCounts["main"] != 2 || profile.ScopeCounts["subagent"] != 1 {
		t.Fatalf("scope counts = %+v", profile.ScopeCounts)
	}
	if profile.FinalMain == nil || strings.Join(profile.FinalMain.ToolResultSequence, ",") != "Task" {
		t.Fatalf("final main = %+v", profile.FinalMain)
	}
}

func TestCompareBehaviorDumpsReportsFinalMessageAndTextVolumeDifferences(t *testing.T) {
	goPath := writeCompareDump(t, "go-final-volume.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"message_count":4,"tool_count":0,"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]},{"role":"user","content":[{"type":"text","text":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]},{"role":"assistant","content":[{"type":"text","text":"done"}]},{"role":"user","content":[{"type":"text","text":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}]}]}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-final-volume.jsonl", []string{
		`{"timestamp":"2026-07-03T00:00:00Z","call":1,"body":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"short"}]},{"role":"assistant","content":[{"type":"text","text":"done"}]},{"role":"user","content":[{"type":"text","text":"short"}]}]}}`,
	})

	report, err := CompareBehaviorDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("info diffs should not fail report: %+v", report)
	}
	joined := behaviorDiffFields(report.Differences)
	for _, want := range []string{"final.message_count", "final.user_text_bytes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
}

func TestBehaviorProfileCapturesCapabilitySignals(t *testing.T) {
	path := writeCompareDump(t, "go-capability-signals.jsonl", []string{
		`{"schema_version":1,"turn":1,"scope":"main","model":"claude-test","max_tokens":4096,"message_count":3,"tool_count":0,"runtime_status":{"present":true,"sections":["background_agent_tasks"]},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"## Background agent tasks\ncapability_loop: evidence: file.go:10 | unknowns: live transcript absent | verification: go test ./internal/query | risks: context noise | next_action: inspect final request"}]},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_agent_get","name":"AgentGet","input":{"task_id":1}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_agent_get","content":"{\"capability_loop\":{\"evidence\":[\"file.go:10\"],\"assumptions\":[\"parent checks result\"],\"unknowns\":[\"none\"],\"verification\":[\"go test\"],\"risks\":[\"preview\"],\"next_action\":\"answer\"}}"}]}]}}`,
	})

	profile, err := LoadBehaviorProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	signals := profile.CapabilitySignals
	if signals.Score != 7 || !signals.CapabilityLoop || !signals.Evidence || !signals.Assumptions || !signals.Unknowns || !signals.Verification || !signals.Risks || !signals.NextAction {
		t.Fatalf("signals = %+v", signals)
	}
	if signals.AnchoredScore != 7 || !signals.EvidenceAnchored || !signals.VerificationAnchored || len(signals.Anchors) == 0 {
		t.Fatalf("anchored signals = %+v", signals)
	}
	if profile.FinalMain == nil || profile.FinalMain.CapabilitySignals.AnchoredScore != 7 {
		t.Fatalf("final main signals = %+v", profile.FinalMain)
	}
}

func TestBehaviorProfileDoesNotAnchorKeywordOnlyCapabilitySignals(t *testing.T) {
	path := writeCompareDump(t, "go-keyword-only-capability-signals.jsonl", []string{
		`{"schema_version":1,"turn":1,"scope":"main","model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":0,"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"capability_loop evidence: trust me unknowns: something verification: checked risks: vague next_action: continue"}]}]}}`,
	})

	profile, err := LoadBehaviorProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	signals := profile.CapabilitySignals
	if signals.Score == 0 || !signals.Evidence || !signals.Verification {
		t.Fatalf("expected raw keyword markers for diagnostic visibility: %+v", signals)
	}
	if signals.AnchoredScore != 0 || signals.EvidenceAnchored || signals.VerificationAnchored || len(signals.AnchoredMarkers) != 0 {
		t.Fatalf("keyword-only signals should not be evidence anchored: %+v", signals)
	}
}

func TestCompareBehaviorDumpsReportsCapabilitySignalDifference(t *testing.T) {
	goPath := writeCompareDump(t, "go-capability-diff.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"message_count":1,"tool_count":0,"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"claude-test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"capability_loop evidence: file.go unknowns: absent verification: go test risks: preview next_action: answer"}]}]}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-capability-diff.jsonl", []string{
		`{"timestamp":"2026-07-03T00:00:00Z","call":1,"body":{"model":"claude-test","max_tokens":4096,"tools":[],"messages":[{"role":"user","content":[{"type":"text","text":"plain final request"}]}]}}`,
	})

	report, err := CompareBehaviorDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("capability signal gain should not fail report: %+v", report)
	}
	joined := behaviorDiffFields(report.Differences)
	for _, want := range []string{"capability_signals", "final.capability_signals"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
	if report.Go.CapabilitySignals.AnchoredScore <= report.Upstream.CapabilitySignals.AnchoredScore {
		t.Fatalf("expected go capability score > upstream: go=%+v upstream=%+v", report.Go.CapabilitySignals, report.Upstream.CapabilitySignals)
	}
}

func TestCompareBehaviorDumpsFailsOnModelMismatch(t *testing.T) {
	goPath := writeCompareDump(t, "go-behavior.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"go-model","max_tokens":4096,"message_count":1,"tool_count":0,"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{},"request":{"model":"go-model","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-behavior.jsonl", []string{
		`{"timestamp":"2026-07-03T00:00:00Z","call":1,"body":{"model":"upstream-model","max_tokens":4096,"tools":[],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`,
	})

	report, err := CompareBehaviorDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("model mismatch should fail: %+v", report)
	}
	joined := behaviorDiffFields(report.Differences)
	if !strings.Contains(joined, "model") {
		t.Fatalf("diffs missing model: %+v", report.Differences)
	}
}

func behaviorDiffFields(diffs []BehaviorDifference) string {
	fields := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		fields = append(fields, diff.Field)
	}
	return strings.Join(fields, "\n")
}
