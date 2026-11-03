package promptdump

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDumpProfileSupportsGoSummaryDump(t *testing.T) {
	path := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":1200,"system_block_count":2,"message_count":2,"tool_count":2,"tools_summary":[{"name":"Read"},{"name":"Grep"}],"messages_summary":[{"role":"user","block_count":1,"bytes":10,"blocks":[{"type":"text","text_bytes":10}]}],"runtime_status":{"present":false},"tool_result_stats":{"count":0,"total_bytes":0,"error_count":0},"compact_state":{}}`,
		`{"schema_version":1,"turn":2,"model":"claude-test","max_tokens":4096,"system_bytes":1200,"system_block_count":2,"message_count":4,"tool_count":0,"messages_summary":[{"role":"assistant","block_count":1,"bytes":10,"blocks":[{"type":"tool_use","tool_name":"Read","input_bytes":20}]},{"role":"user","block_count":1,"bytes":25,"blocks":[{"type":"tool_result","tool_name":"Read","content_bytes":25}]}],"runtime_status":{"present":true,"sections":["turn_budget"]},"tool_result_stats":{"count":1,"total_bytes":25,"error_count":0,"persisted_output":0,"raw_over_limit":0},"compact_state":{}}`,
	})

	profile, err := LoadDumpProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Format != DumpFormatGo {
		t.Fatalf("format = %q", profile.Format)
	}
	if profile.Init.ToolCount != 2 || strings.Join(profile.Init.ToolNames, ",") != "Grep,Read" {
		t.Fatalf("init tools = %+v", profile.Init)
	}
	if profile.Totals.Requests != 2 || profile.Totals.ToolResults != 1 || profile.Totals.ToolUses != 1 {
		t.Fatalf("totals = %+v", profile.Totals)
	}
	if got := strings.Join(profile.Turns[1].RuntimeSections, ","); got != "turn_budget" {
		t.Fatalf("runtime sections = %q", got)
	}
}

func TestLoadDumpProfileSupportsUpstreamDeltaDump(t *testing.T) {
	path := writeCompareDump(t, "upstream.jsonl", []string{
		`{"type":"init","timestamp":"2026-07-02T00:00:00Z","data":{"model":"claude-test","max_tokens":4096,"system":[{"type":"text","text":"system alpha"},{"type":"text","text":"system beta"}],"tools":[{"name":"Read","description":"read files","input_schema":{"type":"object"}},{"name":"Grep","description":"search","input_schema":{"type":"object"}}]}}`,
		`{"type":"message","timestamp":"2026-07-02T00:00:01Z","data":{"role":"user","content":[{"type":"text","text":"hello"},{"type":"tool_result","tool_use_id":"toolu_1","content":"tool output","is_error":false}]}}`,
		`{"type":"response","timestamp":"2026-07-02T00:00:02Z","data":{"stream":true,"chunks":[{"type":"message_start","message":{"role":"assistant"}},{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"Read","id":"toolu_2","input":{}}}]}}`,
	})

	profile, err := LoadDumpProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Format != DumpFormatUpstream {
		t.Fatalf("format = %q", profile.Format)
	}
	if profile.Init.Model != "claude-test" || profile.Init.ToolCount != 2 {
		t.Fatalf("init = %+v", profile.Init)
	}
	if profile.Totals.InitEntries != 1 || profile.Totals.MessageEntries != 1 || profile.Totals.ResponseEntries != 1 {
		t.Fatalf("totals = %+v", profile.Totals)
	}
	if profile.Totals.ToolResults != 1 || profile.Totals.ToolResultBytes != len("tool output") || profile.Totals.ToolUses != 1 {
		t.Fatalf("tool totals = %+v", profile.Totals)
	}
	if len(profile.Limitations) == 0 {
		t.Fatalf("expected upstream limitations")
	}
}

func TestLoadDumpProfileSupportsUpstreamRequestCapture(t *testing.T) {
	path := writeCompareDump(t, "upstream-request.jsonl", []string{
		`{"timestamp":"2026-07-02T00:00:00Z","url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":32000,"system":[{"type":"text","text":"system alpha"},{"type":"text","text":"system beta"}],"tools":[{"name":"Read","description":"read files","input_schema":{"type":"object","properties":{"file_path":{"type":"string"},"offset":{"type":"integer"}}}},{"name":"Grep","description":"search","input_schema":{"type":"object","properties":{"pattern":{"type":"string"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`,
		`{"timestamp":"2026-07-02T00:00:01Z","call":2,"url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":32000,"system":[{"type":"text","text":"system alpha"},{"type":"text","text":"system beta"}],"tools":[{"name":"Read"},{"name":"Grep"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_1","input":{"file_path":"a.go"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"tool output","is_error":false}]}]}}`,
	})

	profile, err := LoadDumpProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Format != DumpFormatUpstreamRequest {
		t.Fatalf("format = %q", profile.Format)
	}
	if profile.Init.Model != "claude-test" || profile.Init.MaxTokens != 32000 || profile.Init.ToolCount != 2 {
		t.Fatalf("init = %+v", profile.Init)
	}
	if got := strings.Join(profile.Init.ToolNames, ","); got != "Grep,Read" {
		t.Fatalf("tool names = %q", got)
	}
	if len(profile.Init.Tools) != 2 || strings.Join(profile.Init.Tools[1].InputSchemaFields, ",") != "file_path,offset" {
		t.Fatalf("tool profiles = %+v", profile.Init.Tools)
	}
	if profile.Totals.Requests != 2 || profile.Totals.UserMessages != 3 || profile.Totals.AssistantMessages != 1 || profile.Totals.ToolResults != 1 || profile.Totals.ToolResultBytes != len("tool output") || profile.Totals.ToolUses != 1 {
		t.Fatalf("totals = %+v", profile.Totals)
	}
	if profile.Turns[1].MessageCount != 3 || profile.Turns[1].ToolResultCount != 1 || profile.Turns[1].ToolUseCount != 1 {
		t.Fatalf("turn 2 = %+v", profile.Turns[1])
	}
	if len(profile.Limitations) == 0 {
		t.Fatalf("expected request capture limitations")
	}
}

func TestComparePromptDumpsReportsInitDifferences(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":8000,"system_block_count":1,"message_count":1,"tool_count":2,"tools_summary":[{"name":"Read"},{"name":"Grep"}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream.jsonl", []string{
		`{"type":"init","timestamp":"2026-07-02T00:00:00Z","data":{"model":"claude-test","max_tokens":2048,"system":"short system","tools":[{"name":"Read"},{"name":"Bash"}]}}`,
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("same model warnings should not fail report: %+v", report)
	}
	joined := compareDifferenceFields(report.Differences)
	for _, want := range []string{"max_tokens", "system_bytes", "tool_names.only_go", "tool_names.only_upstream"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
	if len(report.Limitations) == 0 {
		t.Fatalf("expected comparison limitations")
	}
}

func TestComparePromptDumpsAcceptsUpstreamRequestCapture(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":8000,"system_block_count":1,"message_count":1,"tool_count":2,"tools_summary":[{"name":"Read","description_bytes":10,"input_schema_hash":"same","input_schema_fields":["file_path"]},{"name":"Grep","description_bytes":10,"input_schema_hash":"same","input_schema_fields":["pattern"]}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-request.jsonl", []string{
		`{"timestamp":"2026-07-02T00:00:00Z","url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":4096,"system":"short system","tools":[{"name":"Read","description":"read files","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}}}},{"name":"Grep","description":"search","input_schema":{"type":"object","properties":{"pattern":{"type":"string"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`,
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("same model request capture should not fail report: %+v", report)
	}
	if report.Upstream.Format != DumpFormatUpstreamRequest {
		t.Fatalf("upstream format = %q", report.Upstream.Format)
	}
	if len(report.Limitations) == 0 || !strings.Contains(strings.Join(report.Limitations, "\n"), "fetch wrapper") {
		t.Fatalf("limitations = %+v", report.Limitations)
	}
}

func TestComparePromptDumpsReportsSameNamedToolSchemaDifferences(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":1000,"system_block_count":1,"message_count":1,"tool_count":1,"tools_summary":[{"name":"Read","description_bytes":100,"input_schema_hash":"go-hash","input_schema_fields":["file_path"]}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream-request.jsonl", []string{
		`{"timestamp":"2026-07-02T00:00:00Z","url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":4096,"system":"same enough","tools":[{"name":"Read","description":"read files with offset support and many more details that make this description intentionally much longer than the Go summary fixture. It explains line numbering, binary files, pdf pages, output limits, and safety reminders in enough detail to affect model planning and tool input selection.","input_schema":{"type":"object","properties":{"file_path":{"type":"string"},"offset":{"type":"integer"}}}}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`,
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("tool schema warnings should not fail report: %+v", report)
	}
	joined := compareDifferenceFields(report.Differences)
	for _, want := range []string{"tools.Read.description_bytes", "tools.Read.input_schema_hash", "tools.Read.input_schema_fields"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
}

func TestComparePromptDumpsReportsSystemBlockProfileDifferences(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":2100,"system_block_count":2,"system_blocks_summary":[{"index":0,"text_bytes":80,"text_hash":"go-core","kind":"core_prompt"},{"index":1,"text_bytes":2020,"text_hash":"go-skills","kind":"skills_catalog"}],"message_count":1,"tool_count":1,"tools_summary":[{"name":"Read"}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamCore := "Claude Code " + strings.Repeat("upstream core prompt ", 180)
	upstreamPath := writeCompareDump(t, "upstream-request.jsonl", []string{
		fmt.Sprintf(`{"timestamp":"2026-07-02T00:00:00Z","url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":4096,"system":[{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK."},{"type":"text","text":%q,"cache_control":{"type":"ephemeral","scope":"global"}}],"tools":[{"name":"Read"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`, upstreamCore),
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("system block warnings should not fail report: %+v", report)
	}
	if report.Go.Init.SystemBlockCount != 2 || report.Upstream.Init.SystemBlockCount != 2 {
		t.Fatalf("init block counts = go %+v upstream %+v", report.Go.Init, report.Upstream.Init)
	}
	joined := compareDifferenceFields(report.Differences)
	for _, want := range []string{"system_blocks.kinds", "system_blocks.kind.core_prompt.text_bytes", "system_blocks.kind.core_prompt.cache_control"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
}

func TestComparePromptDumpsAlignsSystemBlocksByKind(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-test","max_tokens":4096,"system_bytes":4200,"system_block_count":3,"system_blocks_summary":[{"index":0,"text_bytes":62,"text_hash":"go-attribution","kind":"attribution"},{"index":1,"text_bytes":2100,"text_hash":"go-core","kind":"core_prompt","has_cache_control":true,"cache_type":"ephemeral","cache_scope":"global"},{"index":2,"text_bytes":2038,"text_hash":"go-memory","kind":"auto_memory"}],"message_count":1,"tool_count":1,"tools_summary":[{"name":"Read"}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamCore := "Claude Code " + strings.Repeat("upstream core prompt ", 180)
	upstreamMemory := "# auto memory\n" + strings.Repeat("memory protocol ", 120)
	upstreamPath := writeCompareDump(t, "upstream-request.jsonl", []string{
		fmt.Sprintf(`{"timestamp":"2026-07-02T00:00:00Z","url":"https://api.anthropic.com/v1/messages","method":"POST","body":{"model":"claude-test","max_tokens":4096,"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.88;"},{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK."},{"type":"text","text":%q,"cache_control":{"type":"ephemeral","scope":"global"}},{"type":"text","text":%q}],"tools":[{"name":"Read"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}}`, upstreamCore, upstreamMemory),
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("system block warnings should not fail report: %+v", report)
	}
	joined := compareDifferenceFields(report.Differences)
	for _, want := range []string{"system_block_count", "system_blocks.kinds", "system_blocks.kind.core_prompt.text_bytes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diffs missing %q: %+v", want, report.Differences)
		}
	}
	for _, notWant := range []string{"system_blocks.1.text_bytes", "system_blocks.1.cache_control", "system_blocks.2.cache_control"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("diffs should not contain position-based mismatch %q: %+v", notWant, report.Differences)
		}
	}
}

func TestComparePromptDumpsFailsOnModelMismatch(t *testing.T) {
	goPath := writeCompareDump(t, "go.jsonl", []string{
		`{"schema_version":1,"turn":1,"model":"claude-go","max_tokens":4096,"system_bytes":1000,"message_count":1,"tool_count":1,"tools_summary":[{"name":"Read"}],"messages_summary":[],"runtime_status":{"present":false},"tool_result_stats":{},"compact_state":{}}`,
	})
	upstreamPath := writeCompareDump(t, "upstream.jsonl", []string{
		`{"type":"init","timestamp":"2026-07-02T00:00:00Z","data":{"model":"claude-upstream","max_tokens":4096,"system":"same enough","tools":[{"name":"Read"}]}}`,
	})

	report, err := ComparePromptDumps(goPath, upstreamPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("model mismatch should fail report: %+v", report)
	}
	joined := compareDifferenceFields(report.Differences)
	if !strings.Contains(joined, "model") {
		t.Fatalf("diffs missing model: %+v", report.Differences)
	}
}

func writeCompareDump(t *testing.T, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func compareDifferenceFields(diffs []CompareDifference) string {
	var fields []string
	for _, diff := range diffs {
		fields = append(fields, diff.Field)
	}
	return strings.Join(fields, "\n")
}
