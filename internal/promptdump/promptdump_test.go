package promptdump

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

func TestSummarizeToolResultsDistinguishesTruncationLiteralFromActualMarker(t *testing.T) {
	sourceLiteral := `func Truncate(content string, limit int) string {
	return content[:limit] + fmt.Sprintf("\n\n[Tool output truncated: %d bytes omitted]", omitted)
}`
	actualTruncated := strings.Repeat("x", 20) + "\n\n[Tool output truncated: 42 bytes omitted]"
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "source", Content: sourceLiteral},
			{Type: "tool_result", ToolUseID: "actual", Content: actualTruncated},
		},
	}}

	summary := SummarizeMessages(messages)
	stats := SummarizeToolResults(summary)

	if !summary[0].Blocks[0].SuspectedTruncated || summary[0].Blocks[0].ActualTruncated {
		t.Fatalf("source literal summary = %+v", summary[0].Blocks[0])
	}
	if !summary[0].Blocks[1].SuspectedTruncated || !summary[0].Blocks[1].ActualTruncated {
		t.Fatalf("actual truncation summary = %+v", summary[0].Blocks[1])
	}
	if stats.SuspectedTruncated != 2 || stats.ActualTruncated != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestSummarizeToolsCanonicalizesInputSchemaHash(t *testing.T) {
	tools := []anthropic.ToolDefinition{
		{
			Name:        "Read",
			Description: "read files",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer"},"file_path":{"type":"string"}}}`),
		},
		{
			Name:        "Read",
			Description: "read files",
			InputSchema: json.RawMessage(`{"properties":{"file_path":{"type":"string"},"offset":{"type":"integer"}},"type":"object"}`),
		},
	}

	summaries := SummarizeTools(tools)

	if summaries[0].InputSchemaHash == "" || summaries[0].InputSchemaHash != summaries[1].InputSchemaHash {
		t.Fatalf("schema hashes should be canonicalized: %+v", summaries)
	}
	if got := strings.Join(summaries[0].InputSchemaFields, ","); got != "file_path,offset" {
		t.Fatalf("schema fields = %q", got)
	}
}

func TestBuildSummarizesSystemBlocksWithoutFullText(t *testing.T) {
	req := anthropic.MessagesRequest{
		Model: "test",
		SystemBlocks: []anthropic.SystemBlock{
			{
				Text: "You are a Claude agent, built on Anthropic's Claude Agent SDK.",
				CacheControl: &anthropic.CacheControl{
					Type:  "ephemeral",
					Scope: "global",
				},
			},
			{
				Text:   "# Available Skills\nSkill names: inspect, test",
				Source: "skills_catalog",
			},
		},
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}

	record := Build(Metadata{Turn: 1, PromptMode: "code", PromptProfile: "claude-compatible"}, req, false)

	if record.PromptMode != "code" || record.PromptProfile != "claude-compatible" {
		t.Fatalf("prompt metadata = mode %q profile %q", record.PromptMode, record.PromptProfile)
	}
	if record.SystemBlockCount != 2 || len(record.SystemBlocks) != 2 {
		t.Fatalf("system blocks = count %d summary %+v", record.SystemBlockCount, record.SystemBlocks)
	}
	if got := record.SystemBlocks[0]; got.Kind != "attribution" || !got.HasCacheControl || got.CacheType != "ephemeral" || got.CacheScope != "global" {
		t.Fatalf("block 0 summary = %+v", got)
	}
	if got := record.SystemBlocks[1]; got.Kind != "skills_catalog" || got.Source != "skills_catalog" || got.TextHash == "" || got.TextBytes != len(req.SystemBlocks[1].Text) {
		t.Fatalf("block 1 summary = %+v", got)
	}
	if record.Request != nil || record.RequestRedaction.RawRequestIncluded {
		t.Fatalf("summary dump should not include full request")
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Skill names: inspect") || strings.Contains(string(encoded), "Claude Agent SDK") {
		t.Fatalf("summary dump leaked system block text: %s", encoded)
	}
}

func TestAnalyzeCacheDiagnosticsFindsStableUncachedSkillsAndSignatureDeltas(t *testing.T) {
	systemBlocks := []anthropic.SystemBlock{
		{
			Text:         strings.Repeat("c", 100),
			Source:       "static_prompt",
			CacheControl: &anthropic.CacheControl{Type: "ephemeral", Scope: "global"},
		},
		{Text: strings.Repeat("d", 30), Source: "dynamic_prompt"},
		{Text: "# Available Skills\n" + strings.Repeat("s", 200), Source: "skills_catalog"},
	}
	first := Build(Metadata{SessionID: "session-cache", Turn: 1}, anthropic.MessagesRequest{
		Model:        "claude-test",
		SystemBlocks: systemBlocks,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}},
		}},
		Tools: []anthropic.ToolDefinition{{Name: "Read", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}, false)
	second := Build(Metadata{SessionID: "session-cache", Turn: 2}, anthropic.MessagesRequest{
		Model:        "claude-test",
		SystemBlocks: systemBlocks,
		Messages: []anthropic.MessageParam{{
			Role:    "user",
			Content: []anthropic.ContentBlock{{Type: "text", Text: "hello with changed prefix"}},
		}},
		Tools: []anthropic.ToolDefinition{{Name: "Read", Description: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}, false)

	report := AnalyzeCacheDiagnostics([]Record{first, second})

	if len(report.Sessions) != 1 || len(report.Records) != 2 {
		t.Fatalf("report = %+v", report)
	}
	session := report.Sessions[0]
	if !session.SystemHashStable || !session.CacheControlHashStable || !session.ToolsHashStable || !session.ThinkingHashStable {
		t.Fatalf("stable flags = %+v", session)
	}
	if session.MessagePrefixHashStable || strings.Join(session.RequestSignatureChangedFields, ",") != "message_prefix" {
		t.Fatalf("message prefix diagnostics = %+v", session)
	}
	top := session.LargestUncachedSystemBlocks[0]
	if top.Source != "skills_catalog" || top.TextBytes != len(systemBlocks[2].Text) || !top.Stable || top.SeenCount != 2 {
		t.Fatalf("top uncached block = %+v", top)
	}
	secondDiag := report.Records[1]
	if secondDiag.CacheableSystemBytes != 100 || secondDiag.UncachedSystemBytes != len(systemBlocks[1].Text)+len(systemBlocks[2].Text) {
		t.Fatalf("byte diagnostics = %+v", secondDiag)
	}
	if secondDiag.SkillsCatalogPosition == nil || *secondDiag.SkillsCatalogPosition != 2 || secondDiag.SkillsCatalogHash != top.TextHash || secondDiag.SkillsCatalogBytes != len(systemBlocks[2].Text) {
		t.Fatalf("skills catalog diagnostics = %+v top=%+v", secondDiag, top)
	}
	if secondDiag.PrefixBeforeSkillsHash == "" || secondDiag.PrefixThroughSkillsHash == "" || secondDiag.PrefixBeforeSkillsHash == secondDiag.PrefixThroughSkillsHash {
		t.Fatalf("skills prefix hashes = before %q through %q", secondDiag.PrefixBeforeSkillsHash, secondDiag.PrefixThroughSkillsHash)
	}
	if !secondDiag.RequestSignatureDeltaFromPrevious.MessagePrefixChanged || secondDiag.RequestSignatureDeltaFromPrevious.ChangedFields[0] != "message_prefix" {
		t.Fatalf("signature delta = %+v", secondDiag.RequestSignatureDeltaFromPrevious)
	}
}

func TestAnalyzeCacheDiagnosticsShowsSkillsCatalogReorderPrefixChange(t *testing.T) {
	currentLayout := []anthropic.SystemBlock{
		{Text: strings.Repeat("c", 100), Source: "static_prompt", CacheControl: &anthropic.CacheControl{Type: "ephemeral", Scope: "global"}},
		{Text: strings.Repeat("d", 30), Source: "dynamic_prompt"},
		{Text: "# Available Skills\n" + strings.Repeat("s", 200), Source: "skills_catalog"},
	}
	reorderedLayout := []anthropic.SystemBlock{
		currentLayout[0],
		currentLayout[2],
		currentLayout[1],
	}
	current := Build(Metadata{SessionID: "session-cache", Turn: 1}, anthropic.MessagesRequest{
		Model:        "claude-test",
		SystemBlocks: currentLayout,
		Messages:     []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}}}},
	}, false)
	reordered := Build(Metadata{SessionID: "session-cache", Turn: 2}, anthropic.MessagesRequest{
		Model:        "claude-test",
		SystemBlocks: reorderedLayout,
		Messages:     []anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "hello"}}}},
	}, false)

	report := AnalyzeCacheDiagnostics([]Record{current, reordered})
	currentDiag := report.Records[0]
	reorderedDiag := report.Records[1]

	if currentDiag.SkillsCatalogPosition == nil || reorderedDiag.SkillsCatalogPosition == nil {
		t.Fatalf("missing skills diagnostics: current=%+v reordered=%+v", currentDiag, reorderedDiag)
	}
	if *currentDiag.SkillsCatalogPosition != 2 || *reorderedDiag.SkillsCatalogPosition != 1 {
		t.Fatalf("skills positions = %d/%d, want 2/1", *currentDiag.SkillsCatalogPosition, *reorderedDiag.SkillsCatalogPosition)
	}
	if currentDiag.SkillsCatalogHash == "" || currentDiag.SkillsCatalogHash != reorderedDiag.SkillsCatalogHash {
		t.Fatalf("skills hashes should remain stable across reorder: %q vs %q", currentDiag.SkillsCatalogHash, reorderedDiag.SkillsCatalogHash)
	}
	if currentDiag.PrefixBeforeSkillsHash == reorderedDiag.PrefixBeforeSkillsHash {
		t.Fatalf("prefix before skills should expose reorder: %q", currentDiag.PrefixBeforeSkillsHash)
	}
	if !reorderedDiag.RequestSignatureDeltaFromPrevious.SystemChanged {
		t.Fatalf("system delta should reflect reorder: %+v", reorderedDiag.RequestSignatureDeltaFromPrevious)
	}
}

func TestSummarizeSystemBlocksClassifiesAutoMemoryBeforeEnvironment(t *testing.T) {
	blocks := SummarizeSystemBlocks("", []anthropic.SystemBlock{{
		Text:   "# Environment\nCurrent date: 2026-07-02\n\n# auto memory\nDurable notes",
		Source: "dynamic_prompt+auto_memory",
	}})

	if len(blocks) != 1 || blocks[0].Kind != "auto_memory" || blocks[0].Source != "dynamic_prompt+auto_memory" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestSummarizeToolResultsCountsPersistedOutputReferences(t *testing.T) {
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{
				Type:      "tool_result",
				ToolUseID: "actual",
				Content:   "<persisted-output>\nOutput too large. Full output saved to: /tmp/out.txt\n</persisted-output>",
			},
			{
				Type:      "tool_result",
				ToolUseID: "source",
				Content:   `return "<persisted-output>\n...\n</persisted-output>"`,
			},
		},
	}}

	summary := SummarizeMessages(messages)
	stats := SummarizeToolResults(summary)

	if !summary[0].Blocks[0].PersistedOutput {
		t.Fatalf("persisted output not detected: %+v", summary[0].Blocks[0])
	}
	if summary[0].Blocks[1].PersistedOutput {
		t.Fatalf("source literal detected as persisted output: %+v", summary[0].Blocks[1])
	}
	if stats.PersistedOutput != 1 || stats.ActualTruncated != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestSummarizeToolResultsCountsRawOverDefaultLimit(t *testing.T) {
	largeRaw := strings.Repeat("x", 50_001)
	largePersisted := "<persisted-output>\n" + strings.Repeat("x", 50_001) + "\n</persisted-output>"
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "raw", Content: largeRaw},
			{Type: "tool_result", ToolUseID: "persisted", Content: largePersisted},
		},
	}}

	summary := SummarizeMessages(messages)
	stats := SummarizeToolResults(summary)

	if !summary[0].Blocks[0].RawOverDefaultLimit {
		t.Fatalf("large raw result was not detected: %+v", summary[0].Blocks[0])
	}
	if summary[0].Blocks[1].RawOverDefaultLimit {
		t.Fatalf("persisted output should not count as raw over-limit: %+v", summary[0].Blocks[1])
	}
	if stats.RawOverDefaultLimit != 1 || stats.MaxBytes != len(largePersisted) {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestSummarizeToolResultsUsesConfiguredLimit(t *testing.T) {
	raw := strings.Repeat("x", 55_000)
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "raw", Content: raw},
		},
	}}

	record := Build(Metadata{
		Turn:            1,
		ToolResultLimit: 200_000,
		MessageBudget:   210_000,
		HistoryBudget:   220_000,
	}, anthropic.MessagesRequest{Model: "test", Messages: messages}, false)

	block := record.MessagesSummary[0].Blocks[0]
	if !block.RawOverDefaultLimit {
		t.Fatalf("raw result should exceed the built-in default: %+v", block)
	}
	if block.RawOverLimit {
		t.Fatalf("raw result should not exceed configured limit: %+v", block)
	}
	if record.ToolResultStats.Limit != 200_000 || record.ToolResultStats.MessageBudget != 210_000 || record.ToolResultStats.HistoryBudget != 220_000 {
		t.Fatalf("stats limits = %+v", record.ToolResultStats)
	}
	if record.ToolResultStats.RawOverDefaultLimit != 1 || record.ToolResultStats.RawOverLimit != 0 {
		t.Fatalf("stats = %+v", record.ToolResultStats)
	}
}

func TestBuildSummarizesRuntimeStatusSections(t *testing.T) {
	runtimeStatus := `<system-reminder>
Current runtime status for this coding session. Use it to keep planning and background work consistent; do not mention it unless relevant.

## Tool planning reminder
- You have already gathered tool results.

## Permission context
- mode: deny

## Active todos
- [in_progress/high] verify prompt dump

## Plan mode
- active: true

## Background agent tasks
- Treat this list as status only.

## Recent agent evidence decision context
- #1 reviewer completed; capability_loop: evidence: file.go:10 | next_action: synthesize

## Agent capability follow-up gate
- pending_follow_up: #1 reviewer completed; must_handle_next_action: synthesize
</system-reminder>`
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: runtimeStatus}},
	}}

	record := Build(Metadata{Turn: 1}, anthropic.MessagesRequest{Model: "test", Messages: messages}, false)

	if !record.RuntimeStatus.Present || record.RuntimeStatus.TextBytes != len(runtimeStatus) {
		t.Fatalf("runtime status = %+v", record.RuntimeStatus)
	}
	want := []string{"tool_planning", "permission_context", "active_todos", "plan_mode", "background_agent_tasks", "agent_evidence_decision_context", "agent_capability_follow_up_gate"}
	if strings.Join(record.RuntimeStatus.Sections, ",") != strings.Join(want, ",") {
		t.Fatalf("sections = %+v", record.RuntimeStatus.Sections)
	}
	if record.MessagesSummary[0].Blocks[0].TextBytes != len(runtimeStatus) {
		t.Fatalf("message summary = %+v", record.MessagesSummary[0].Blocks[0])
	}
}

func TestBuildSummarizesSubagentRuntimeStatusSections(t *testing.T) {
	runtimeStatus := `<system-reminder>
Current sub-agent runtime status. Use it to finish the delegated task efficiently; do not mention it unless relevant.

## Sub-agent turn budget reminder
- This is the last turn.

## Sub-agent tool planning reminder
- You have already gathered tool results.
</system-reminder>`
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: runtimeStatus}},
	}}

	record := Build(Metadata{Scope: "subagent", Turn: 5}, anthropic.MessagesRequest{Model: "test", Messages: messages}, false)

	if !record.RuntimeStatus.Present || record.RuntimeStatus.TextBytes != len(runtimeStatus) {
		t.Fatalf("runtime status = %+v", record.RuntimeStatus)
	}
	want := []string{"turn_budget", "tool_planning"}
	if strings.Join(record.RuntimeStatus.Sections, ",") != strings.Join(want, ",") {
		t.Fatalf("sections = %+v", record.RuntimeStatus.Sections)
	}
}

func TestBuildDoesNotReportPlainRuntimeWordsAsRuntimeStatus(t *testing.T) {
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "text", Text: "Plan mode and todos are mentioned in ordinary prose."}},
	}}

	record := Build(Metadata{Turn: 1}, anthropic.MessagesRequest{Model: "test", Messages: messages}, false)

	if record.RuntimeStatus.Present || len(record.RuntimeStatus.Sections) != 0 || record.RuntimeStatus.TextBytes != 0 {
		t.Fatalf("runtime status = %+v", record.RuntimeStatus)
	}
}
