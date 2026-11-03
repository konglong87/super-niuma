package promptdump

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyCodeModeRecoveryDumpPasses(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"messages_summary":[{"role":"assistant","blocks":[{"type":"tool_use","tool_name":"Read","tool_use_id":"toolu_read"},{"type":"tool_use","tool_name":"Grep","tool_use_id":"toolu_grep"}]},{"role":"user","blocks":[{"type":"tool_result","tool_name":"Read","tool_use_id":"toolu_read","content_bytes":80000,"raw_over_default_limit":true},{"type":"tool_result","tool_name":"Grep","tool_use_id":"toolu_grep","content_bytes":2200,"persisted_output":true}]}],"tool_result_stats":{"count":2,"error_count":0,"actual_truncated":0,"persisted_output":1,"raw_over_default_limit":1,"raw_over_limit":0},"compact_state":{"auto_compact_enabled":true,"compact_summaries":1}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolNoRawOverLimit = []string{"Grep"}
	options.RequireToolNoPersistedOutput = []string{"Read"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Final == nil || report.Final.ToolCount != 0 || report.Final.PersistedOutput != 1 || report.Final.RawOverDefault != 1 {
		t.Fatalf("final = %+v", report.Final)
	}
	if got := report.Final.ToolResultsByTool["Read"].RawOverDefaultLimit; got != 1 {
		t.Fatalf("Read tool stats = %+v", report.Final.ToolResultsByTool["Read"])
	}
	if got := report.Final.ToolResultsByTool["Grep"].PersistedOutput; got != 1 {
		t.Fatalf("Grep tool stats = %+v", report.Final.ToolResultsByTool["Grep"])
	}
}

func TestVerifyCodeModeRecoveryDumpFailsOnMissingFinalBudgetAndTools(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":4,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":2,"error_count":1,"actual_truncated":1,"raw_over_limit":1},"compact_state":{}}`,
	})

	report, err := VerifyCodeModeRecoveryDump(path, DefaultVerifyOptions())
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	for _, want := range []string{
		"tool_result errors",
		"actually truncated",
		"raw tool_results over configured limit",
		"exposes 3 tools",
		"missing runtime_status section turn_budget",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures missing %q: %v", want, report.Failures)
		}
	}
}

func TestVerifyCodeModeRecoveryDumpFailsToolSpecificGates(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"messages_summary":[{"role":"assistant","blocks":[{"type":"tool_use","tool_name":"Read","tool_use_id":"toolu_read"},{"type":"tool_use","tool_name":"Grep","tool_use_id":"toolu_grep"}]},{"role":"user","blocks":[{"type":"tool_result","tool_name":"Read","tool_use_id":"toolu_read","content_bytes":2400,"persisted_output":true},{"type":"tool_result","tool_name":"Grep","tool_use_id":"toolu_grep","content_bytes":210000,"raw_over_limit":true}]}],"tool_result_stats":{"count":2,"error_count":0,"actual_truncated":0,"persisted_output":1,"raw_over_limit":1},"compact_state":{}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireNoRawOverLimit = false
	options.RequireToolNoRawOverLimit = []string{"Grep"}
	options.RequireToolNoPersistedOutput = []string{"Read"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	for _, want := range []string{
		"turn 2 tool Grep has 1 raw tool_results over configured limit",
		"turn 2 tool Read has 1 persisted tool_results, want 0",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures missing %q: %v", want, report.Failures)
		}
	}
}

func TestVerifyCodeModeRecoveryDumpRequiresToolPersistedOutput(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"messages_summary":[{"role":"assistant","blocks":[{"type":"tool_use","tool_name":"Grep","tool_use_id":"toolu_grep"}]},{"role":"user","blocks":[{"type":"tool_result","tool_name":"Grep","tool_use_id":"toolu_grep","content_bytes":2200,"persisted_output":true}]}],"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"persisted_output":1,"raw_over_limit":0},"compact_state":{}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolPersistedOutput = []string{"Grep"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}

	options.RequireToolPersistedOutput = []string{"Read"}
	report, err = VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	if !strings.Contains(joined, "dump has no persisted tool_result for required tool Read") {
		t.Fatalf("failures missing persisted output failure: %v", report.Failures)
	}
}

func TestVerifyCodeModeRecoveryDumpFailsToolMaxBytesGate(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"messages_summary":[{"role":"assistant","blocks":[{"type":"tool_use","tool_name":"Grep","tool_use_id":"toolu_grep"}]},{"role":"user","blocks":[{"type":"tool_result","tool_name":"Grep","tool_use_id":"toolu_grep","content_bytes":21000}]}],"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolMaxBytes = []string{"Grep=20000"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	want := "turn 2 tool Grep has max visible tool_result bytes 21000 over required max 20000"
	if !strings.Contains(joined, want) {
		t.Fatalf("failures missing %q: %v", want, report.Failures)
	}
}

func TestVerifyCodeModeRecoveryDumpFailsInvalidToolMaxBytesSpec(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"prompt_mode":"code","message_count":2,"tool_count":3,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolMaxBytes = []string{"Grep:not-a-spec", "Bash=-1"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	for _, want := range []string{
		`invalid require_tool_max_bytes spec "Grep:not-a-spec", want Tool=Bytes`,
		`invalid require_tool_max_bytes spec "Bash=-1", bytes must be a non-negative integer`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures missing %q: %v", want, report.Failures)
		}
	}
}

func TestVerifyCodeModeRecoveryDumpPassesSubagentTurnBudgetToolsDisabled(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":4,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"scope":"subagent","query_source":"agentruntime","prompt_mode":"subagent","agent_name":"general-purpose","agent_mode":"default","message_count":6,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":160},"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":3,"scope":"main","prompt_mode":"code","message_count":8,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	report, err := VerifyCodeModeRecoveryDump(path, DefaultVerifyOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Records) != 3 {
		t.Fatalf("records = %d, want 3", len(report.Records))
	}
	subagent := report.Records[1]
	if subagent.Scope != "subagent" || subagent.AgentName != "general-purpose" || subagent.AgentMode != "default" || subagent.ToolCount != 0 {
		t.Fatalf("subagent brief = %+v", subagent)
	}
}

func TestVerifyCodeModeRecoveryDumpFailsSubagentTurnBudgetWithTools(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":4,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":5,"scope":"subagent","query_source":"agentruntime","prompt_mode":"subagent","agent_name":"reviewer","message_count":7,"tool_count":3,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":160},"tool_result_stats":{"count":2,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"scope":"main","prompt_mode":"code","message_count":8,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	report, err := VerifyCodeModeRecoveryDump(path, DefaultVerifyOptions())
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	want := "subagent reviewer turn 5 has turn_budget but exposes 3 tools, want 0"
	if !strings.Contains(joined, want) {
		t.Fatalf("failures missing %q: %v", want, report.Failures)
	}
}

func TestVerifyCodeModeRecoveryDumpFailsWhenSubagentRecordRequired(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":4,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"scope":"main","prompt_mode":"code","message_count":8,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireSubagentRecord = true
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	if !strings.Contains(joined, "dump has no subagent records") {
		t.Fatalf("failures missing subagent record failure: %v", report.Failures)
	}
}

func TestVerifyCodeModeRecoveryDumpRequiresToolResultAndRequestText(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":2,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"scope":"main","prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"messages_summary":[{"role":"assistant","blocks":[{"type":"tool_use","tool_name":"Skill","tool_use_id":"toolu_skill"}]},{"role":"user","blocks":[{"type":"tool_result","tool_name":"Skill","tool_use_id":"toolu_skill","content_bytes":82}]}],"tool_result_stats":{"count":1,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_skill","name":"Skill","input":{"name":"matrix-skill","mode":"strict"}}]},{"role":"user","content":[{"type":"text","text":"Skill matrix-skill instructions are now active. MATRIX_SKILL_ACTIVE"}]}]}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolResult = []string{"Skill"}
	options.RequireToolUseInputText = []string{"Skill=matrix-skill", "Skill=strict"}
	options.RequireRequestText = []string{"Skill matrix-skill instructions are now active", "MATRIX_SKILL_ACTIVE"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyCodeModeRecoveryDumpRequiresDecodedRequestText(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":1,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"Prefer scoped Grep with output_mode:\"content\" before broad reads."}]}]}}`,
	})

	options := DefaultVerifyOptions()
	options.MinTurns = 1
	options.RequireRequestText = []string{`output_mode:"content"`}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyCodeModeRecoveryDumpForbidsExposedTools(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":1,"tool_count":2,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"source contains \"name\":\"Bash\" but Bash is not exposed"}]}],"tools":[{"name":"Read","description":"read files","input_schema":{}},{"name":"Write","description":"write files","input_schema":{}}]}}`,
	})

	options := DefaultVerifyOptions()
	options.MinTurns = 1
	options.RequireFinalToolsDisabled = false
	options.ForbidTool = []string{"Bash"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}

	options.ForbidTool = []string{"Write"}
	report, err = VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	if !strings.Contains(joined, "turn 1 exposes forbidden tool Write") {
		t.Fatalf("failures missing forbidden tool failure: %v", report.Failures)
	}
}

func TestVerifyCodeModeRecoveryDumpFailsMissingToolUseInputText(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"printf ok"}}]}]}}`,
	})

	options := DefaultVerifyOptions()
	options.MinTurns = 1
	options.RequireToolUseInputText = []string{"Bash=run_in_background", "bad-spec"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	for _, want := range []string{
		`invalid require_tool_use_input_text spec "bad-spec", want Tool=snippet`,
		`dump has no tool_use input for required tool Bash containing "run_in_background"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures missing %q: %v", want, report.Failures)
		}
	}
}

func TestVerifyCodeModeRecoveryDumpFailsMissingToolResultAndRequestText(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":2,"runtime_status":{"present":false},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"scope":"main","prompt_mode":"code","message_count":5,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"],"text_bytes":120},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"plain request"}]}]}}`,
	})

	options := DefaultVerifyOptions()
	options.RequireToolResult = []string{"Skill"}
	options.RequireRequestText = []string{"Skill matrix-skill instructions are now active"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	for _, want := range []string{
		"dump has no tool_result for required tool Skill",
		`dump full request text missing "Skill matrix-skill instructions are now active"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failures missing %q: %v", want, report.Failures)
		}
	}
}

func TestVerifyCodeModeRecoveryDumpForbidsRequestText(t *testing.T) {
	path := writeVerifyDump(t, []string{
		`{"schema_version":1,"turn":1,"scope":"main","prompt_mode":"code","message_count":2,"tool_count":0,"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":0,"error_count":0,"actual_truncated":0,"raw_over_limit":0},"compact_state":{},"request":{"model":"test","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"<persisted-output> resume acceptance preview </persisted-output>"}]}]}}`,
	})

	options := DefaultVerifyOptions()
	options.MinTurns = 1
	options.RequireRequestText = []string{"<persisted-output>"}
	options.ForbidRequestText = []string{"RAW_RESUME_ACCEPTANCE_MARKER"}
	report, err := VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Failures) != 0 {
		t.Fatalf("report = %+v", report)
	}

	options.ForbidRequestText = []string{"resume acceptance preview"}
	report, err = VerifyCodeModeRecoveryDump(path, options)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("report unexpectedly ok: %+v", report)
	}
	joined := strings.Join(report.Failures, "\n")
	if !strings.Contains(joined, `dump full request text unexpectedly contains "resume acceptance preview"`) {
		t.Fatalf("failures missing forbidden text failure: %v", report.Failures)
	}
}

func writeVerifyDump(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dump.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
