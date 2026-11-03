package compact

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

type fakeSummaryStreamer struct {
	requests []anthropic.MessagesRequest
	summary  string
}

func (f *fakeSummaryStreamer) StreamMessages(_ context.Context, req anthropic.MessagesRequest, cb anthropic.StreamCallbacks) (*anthropic.StreamResult, error) {
	f.requests = append(f.requests, req)
	if cb.OnText != nil {
		if err := cb.OnText(f.summary); err != nil {
			return nil, err
		}
	}
	return &anthropic.StreamResult{
		Message:    anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: f.summary}}},
		StopReason: "end_turn",
	}, nil
}

func TestThresholdUsesModelRatioAndContext(t *testing.T) {
	cfg := Config{
		DefaultThresholdRatio: 0.75,
		ModelContext:          map[string]int{"gpt-5.5": 200_000},
		ModelThresholdRatio:   map[string]float64{"gpt-5.5": 0.5},
	}
	if got := cfg.ThresholdTokens("gpt-5.5"); got != 100_000 {
		t.Fatalf("threshold = %d, want 100000", got)
	}
}

func TestMaybeCompactReplacesOlderRoundsWithSummary(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.05,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}, streamer, RoughTokenCounter{})
	messages := []anthropic.MessageParam{
		textMessage("user", "Please inspect internal/query/query.go and run go test ./internal/query."),
		textMessage("assistant", "I will inspect it."),
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_read", Name: "Read", Input: json.RawMessage(`{"file_path":"internal/query/query.go"}`)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_read", Content: "func run() {}", IsError: false}}},
		textMessage("assistant", "The query loop builds request messages."),
		textMessage("user", "Now continue with the implementation."),
	}
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "system", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected compaction, got skip=%s usage=%d", result.SkippedReason, result.EstimatedUsage)
	}
	if len(streamer.requests) != 1 {
		t.Fatalf("summary requests = %d, want 1", len(streamer.requests))
	}
	if len(result.Messages) != 2 {
		t.Fatalf("messages = %+v", result.Messages)
	}
	if !strings.Contains(result.Messages[0].Content[0].Text, "Conversation summary so far:") {
		t.Fatalf("summary message = %+v", result.Messages[0])
	}
	if result.PersistedSummary == "" || !strings.Contains(result.PersistedSummary, "## Runtime Extracted Facts") {
		t.Fatalf("persisted summary missing runtime facts:\n%s", result.PersistedSummary)
	}
	if result.Messages[0].Content[0].Text != "Conversation summary so far:\n"+result.PersistedSummary {
		t.Fatalf("persisted summary diverged from request summary:\nmessage=%q\npersisted=%q", result.Messages[0].Content[0].Text, result.PersistedSummary)
	}
	if result.Messages[1].Content[0].Text != "Now continue with the implementation." {
		t.Fatalf("preserved latest message = %+v", result.Messages[1])
	}
	if result.Metadata.ContextTokens != 1000 || result.Metadata.ThresholdTokens != 50 {
		t.Fatalf("metadata = %+v", result.Metadata)
	}
	assertCompactGolden(t, "auto_compact_messages.json", marshalCompactGolden(t, result.Messages))
}

func TestMaybeCompactKeepsToolUseWithToolResult(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.05,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}, streamer, RoughTokenCounter{})
	messages := []anthropic.MessageParam{
		textMessage("user", "Please inspect README.md."),
		textMessage("assistant", "Calling read."),
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_read", Name: "Read", Input: json.RawMessage(`{"file_path":"README.md"}`)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_read", Content: "# Project"}}},
		textMessage("assistant", "README.md starts with # Project."),
		textMessage("user", "Continue."),
	}
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected compacted")
	}
	for _, message := range result.Messages {
		if message.Content[0].Type == "tool_result" {
			t.Fatalf("tool_result was preserved without its triggering tool_use: %+v", result.Messages)
		}
	}
}

func TestMaybeCompactPersistsCapabilityLoopFacts(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.05,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}, streamer, RoughTokenCounter{})
	agentGetResult := `{"task":{"id":7,"status":"completed"},"result":{"content":"done","capability_loop":{"evidence":["internal/query/query.go:3912 preserves capability_loop"],"verification":["go test ./internal/query -count=1"],"unknowns":["No live transcript"],"risks":["Preview may be incomplete"],"next_action":"Parent should cite AgentGet evidence before final synthesis"}}}`
	messages := []anthropic.MessageParam{
		textMessage("user", "Start."),
		textMessage("assistant", "I will check the agent result."),
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_agent_get", Name: "AgentGet", Input: json.RawMessage(`{"task_id":7}`)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_agent_get", Content: agentGetResult}}},
		textMessage("assistant", "I saw the agent evidence."),
		textMessage("user", "Continue after compaction."),
	}
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "system", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected compaction, skip=%s usage=%d", result.SkippedReason, result.EstimatedUsage)
	}
	for _, want := range []string{
		"Capability Loop Evidence",
		"internal/query/query.go:3912 preserves capability_loop",
		"Capability Loop Verification",
		"go test ./internal/query -count=1",
		"Capability Loop Unknowns",
		"No live transcript",
		"Capability Loop Next Actions",
		"Parent should cite AgentGet evidence before final synthesis",
	} {
		if !strings.Contains(result.PersistedSummary, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, result.PersistedSummary)
		}
	}
}

func TestMaybeCompactPersistsToolResultCapabilityLoopWrapper(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: strings.Join([]string{
		"## Current Goal",
		"Continue after synchronous Task capability context.",
		"## User Preferences / Constraints",
		"Preserve structured evidence.",
		"## Decisions Made",
		"Synchronous Task returned parent decision context.",
		"## Files / Code Changed",
		"internal/tools/task.go",
		"internal/tools/task/task.go",
		"./internal/tools/task",
		"## Commands / Test Results",
		"go test ./internal/tools/task -count=1",
		"## Open Tasks",
		"Continue compact fidelity work.",
		"## Known Issues / Risks",
		"Model may ignore returned fields.",
		"## Important Raw Facts",
		"internal/tools/task/task.go returned TASK_SYNC_EVIDENCE and go test ./internal/tools/task -count=1 was the verification command.",
	}, "\n")}
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.05,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}, streamer, RoughTokenCounter{})
	taskResult := strings.Join([]string{
		"Summary: checked structured context.",
		"",
		"<capability_loop>",
		`{"capability_loop":{"evidence":["TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context."],"assumptions":["Parent sees synchronous Task output directly."],"unknowns":["No browser flow sampled."],"verification":["go test ./internal/tools/task -count=1"],"risks":["Model may ignore returned fields."],"next_action":"TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer."}}`,
		"</capability_loop>",
		"These structured fields are parent decision context.",
	}, "\n")
	messages := []anthropic.MessageParam{
		textMessage("user", "Start."),
		textMessage("assistant", "I will run a Task."),
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_task", Name: "Task", Input: json.RawMessage(`{"prompt":"inspect"}`)}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_task", Content: taskResult}}},
		textMessage("assistant", "I saw the Task capability context."),
		textMessage("user", "Continue after compaction."),
	}
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", "system", nil, nil, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected compaction, skip=%s usage=%d", result.SkippedReason, result.EstimatedUsage)
	}
	for _, want := range []string{
		"Capability Loop Evidence",
		"TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context.",
		"Capability Loop Assumptions",
		"Parent sees synchronous Task output directly.",
		"Capability Loop Unknowns",
		"No browser flow sampled.",
		"Capability Loop Verification",
		"go test ./internal/tools/task -count=1",
		"Capability Loop Risks",
		"Model may ignore returned fields.",
		"Capability Loop Next Actions",
		"TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer.",
	} {
		if !strings.Contains(result.PersistedSummary, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, result.PersistedSummary)
		}
	}
}

func TestMaybeCompactCountsSystemAndToolDefinitions(t *testing.T) {
	streamer := &fakeSummaryStreamer{summary: compactSummaryFixture()}
	compactor := New(Config{
		Enabled:               true,
		DefaultThresholdRatio: 0.1,
		PreserveRecentRounds:  1,
		ModelContext:          map[string]int{"gpt-5.5": 1000},
	}, streamer, RoughTokenCounter{})
	messages := []anthropic.MessageParam{
		textMessage("user", "Start with README.md."),
		textMessage("assistant", "Started."),
		textMessage("user", "Please inspect internal/query/query.go."),
		textMessage("assistant", "I will inspect it."),
		textMessage("user", "Continue."),
	}
	toolSchema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"` + strings.Repeat("large schema ", 80) + `"}}}`)
	result, err := compactor.MaybeCompact(context.Background(), "gpt-5.5", strings.Repeat("system ", 160), nil, []anthropic.ToolDefinition{{
		Name:        "Read",
		Description: strings.Repeat("tool description ", 80),
		InputSchema: toolSchema,
	}}, messages)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted {
		t.Fatalf("expected system/tool tokens to trigger compact, skip=%s usage=%d", result.SkippedReason, result.EstimatedUsage)
	}
}

func TestValidateSummaryRequiresExtractedFacts(t *testing.T) {
	err := validateSummary("## Current Goal\nx\n## Open Tasks\nx\n## Important Raw Facts\nmissing", Facts{
		Files:    []string{"internal/query/query.go"},
		Commands: []string{"go test ./internal/query"},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestExtractFactsCapturesCapabilityLoopRuntimeStatus(t *testing.T) {
	facts := ExtractFacts([]anthropic.MessageParam{textMessage("user", strings.Join([]string{
		"## Background agent tasks",
		"- #7 reviewer completed: inspect query; result preview: done; capability_loop: evidence: internal/query/query.go:3912 preserves capability loop | assumptions: parent has task store | unknowns: no live transcript | verification: go test ./internal/query -count=1 | risks: preview may be incomplete | next_action: call AgentGet before final synthesis; artifacts: session_id: sub-session | transcript_path: /tmp/subagent.jsonl | output_file: /tmp/subagent.output | worktree_path: /tmp/subagent-worktree | worktree_branch: agent/audit",
	}, "\n"))})
	if got := facts.CapabilityEvidence; len(got) != 1 || got[0] != "internal/query/query.go:3912 preserves capability loop" {
		t.Fatalf("capability evidence = %#v", got)
	}
	if got := facts.CapabilityUnknowns; len(got) != 1 || got[0] != "no live transcript" {
		t.Fatalf("capability unknowns = %#v", got)
	}
	if got := facts.CapabilityVerification; len(got) != 1 || got[0] != "go test ./internal/query -count=1" {
		t.Fatalf("capability verification = %#v", got)
	}
	if got := facts.CapabilityNextActions; len(got) != 1 || got[0] != "call AgentGet before final synthesis" {
		t.Fatalf("capability next actions = %#v", got)
	}
	for _, want := range []string{
		"session_id: sub-session",
		"transcript_path: /tmp/subagent.jsonl",
		"output_file: /tmp/subagent.output",
		"worktree_path: /tmp/subagent-worktree",
		"worktree_branch: agent/audit",
	} {
		if !containsString(facts.CapabilityArtifacts, want) {
			t.Fatalf("capability artifacts missing %q: %#v", want, facts.CapabilityArtifacts)
		}
	}
	if markdown := facts.Markdown(); !strings.Contains(markdown, "Capability Loop Evidence") || !strings.Contains(markdown, "Capability Loop Next Actions") || !strings.Contains(markdown, "Capability Loop Artifacts") {
		t.Fatalf("facts markdown missing capability sections:\n%s", markdown)
	}
}

func TestExtractFactsCapturesCapabilityLoopMarkdownSections(t *testing.T) {
	summary := strings.Join([]string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"- COMPACT_RESUME_GATE_EVIDENCE",
		"Capability Loop Assumptions:",
		"- COMPACT_RESUME_GATE_ASSUMPTION",
		"Capability Loop Unknowns:",
		"- COMPACT_RESUME_GATE_UNKNOWN",
		"Capability Loop Verification:",
		"- COMPACT_RESUME_GATE_VERIFICATION",
		"Capability Loop Risks:",
		"- COMPACT_RESUME_GATE_RISK",
		"Capability Loop Next Actions:",
		"- COMPACT_RESUME_GATE_NEXT_ACTION",
		"Capability Loop Follow Up IDs:",
		"- tool:compact-task",
		"Capability Loop Resolved Follow Ups:",
		"- COMPACT_RESUME_RESOLVED_FOLLOW_UP",
		"Capability Loop Supersedes:",
		"- tool:compact-task",
		"Capability Loop Artifacts:",
		"- session_id: compact-session",
		"- transcript_path: /tmp/compact.jsonl",
		"- output_file: /tmp/compact.output",
		"- worktree_path: /tmp/compact-worktree",
		"- worktree_branch: agent/compact",
	}, "\n")
	facts := ExtractFacts([]anthropic.MessageParam{textMessage("user", summary)})
	if got := facts.CapabilityEvidence; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_EVIDENCE" {
		t.Fatalf("capability evidence = %#v", got)
	}
	if got := facts.CapabilityAssumptions; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_ASSUMPTION" {
		t.Fatalf("capability assumptions = %#v", got)
	}
	if got := facts.CapabilityUnknowns; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_UNKNOWN" {
		t.Fatalf("capability unknowns = %#v", got)
	}
	if got := facts.CapabilityVerification; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_VERIFICATION" {
		t.Fatalf("capability verification = %#v", got)
	}
	if got := facts.CapabilityRisks; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_RISK" {
		t.Fatalf("capability risks = %#v", got)
	}
	if got := facts.CapabilityNextActions; len(got) != 1 || got[0] != "COMPACT_RESUME_GATE_NEXT_ACTION" {
		t.Fatalf("capability next actions = %#v", got)
	}
	if got := facts.CapabilityFollowUpIDs; len(got) != 1 || got[0] != "tool:compact-task" {
		t.Fatalf("capability follow-up ids = %#v", got)
	}
	if got := facts.CapabilityResolved; len(got) != 1 || got[0] != "COMPACT_RESUME_RESOLVED_FOLLOW_UP" {
		t.Fatalf("capability resolved follow-ups = %#v", got)
	}
	if got := facts.CapabilitySupersedes; len(got) != 1 || got[0] != "tool:compact-task" {
		t.Fatalf("capability supersedes = %#v", got)
	}
	for _, want := range []string{
		"session_id: compact-session",
		"transcript_path: /tmp/compact.jsonl",
		"output_file: /tmp/compact.output",
		"worktree_path: /tmp/compact-worktree",
		"worktree_branch: agent/compact",
	} {
		if !containsString(facts.CapabilityArtifacts, want) {
			t.Fatalf("capability artifacts missing %q: %#v", want, facts.CapabilityArtifacts)
		}
	}
}

func TestExtractFactsCapturesNestedCapabilityLoopJSON(t *testing.T) {
	agentGetResult := `{"task":{"id":7,"status":"completed"},"result":{"content":"done","session_id":"sub-session","transcript_path":"/tmp/subagent.jsonl","output_file":"/tmp/subagent.output","worktree_path":"/tmp/subagent-worktree","worktree_branch":"agent/audit","capability_loop":{"evidence":["internal/tools/agent/agent.go:578 returns capability_loop"],"assumptions":["AgentGet result is available"],"unknowns":["No browser validation"],"verification":["go test ./internal/tools/agent -count=1"],"risks":["JSON may be truncated"],"next_action":"synthesize with cited evidence","follow_up_id":"task:7","resolved_follow_up":"AgentGet follow-up was handled","supersedes_evidence_id":"task:7"}}}`
	facts := ExtractFacts([]anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{
		Type:      "tool_result",
		ToolUseID: "toolu_agent_get",
		Content:   agentGetResult,
	}}}})
	if got := facts.CapabilityEvidence; len(got) != 1 || got[0] != "internal/tools/agent/agent.go:578 returns capability_loop" {
		t.Fatalf("capability evidence = %#v", got)
	}
	if got := facts.CapabilityAssumptions; len(got) != 1 || got[0] != "AgentGet result is available" {
		t.Fatalf("capability assumptions = %#v", got)
	}
	if got := facts.CapabilityRisks; len(got) != 1 || got[0] != "JSON may be truncated" {
		t.Fatalf("capability risks = %#v", got)
	}
	if got := facts.CapabilityFollowUpIDs; len(got) != 1 || got[0] != "task:7" {
		t.Fatalf("capability follow-up ids = %#v", got)
	}
	if got := facts.CapabilityResolved; len(got) != 1 || got[0] != "AgentGet follow-up was handled" {
		t.Fatalf("capability resolved follow-ups = %#v", got)
	}
	if got := facts.CapabilitySupersedes; len(got) != 1 || got[0] != "task:7" {
		t.Fatalf("capability supersedes = %#v", got)
	}
	for _, want := range []string{
		"session_id: sub-session",
		"transcript_path: /tmp/subagent.jsonl",
		"output_file: /tmp/subagent.output",
		"worktree_path: /tmp/subagent-worktree",
		"worktree_branch: agent/audit",
	} {
		if !containsString(facts.CapabilityArtifacts, want) {
			t.Fatalf("capability artifacts missing %q: %#v", want, facts.CapabilityArtifacts)
		}
	}
}

func TestExtractFactsCapturesCapabilityLoopWrapper(t *testing.T) {
	content := strings.Join([]string{
		"Summary: checked structured context.",
		"",
		"<capability_loop>",
		`{"session_id":"sync-task-session","transcript_path":"/tmp/sync-task.jsonl","output_file":"/tmp/sync-task.output","worktree_path":"/tmp/sync-task-worktree","worktree_branch":"agent/sync-task","capability_loop":{"evidence":["TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context."],"assumptions":["Parent sees synchronous Task output directly."],"unknowns":["No browser flow sampled."],"verification":["go test ./internal/tools/task -count=1"],"risks":["Model may ignore returned fields."],"next_action":"TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer."}}`,
		"</capability_loop>",
		"These structured fields are parent decision context.",
	}, "\n")
	facts := ExtractFacts([]anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{
		Type:      "tool_result",
		ToolUseID: "toolu_task",
		Content:   content,
	}}}})
	if got := facts.CapabilityEvidence; len(got) != 1 || got[0] != "TASK_SYNC_EVIDENCE: internal/tools/task/task.go returns structured context." {
		t.Fatalf("capability evidence = %#v", got)
	}
	if got := facts.CapabilityAssumptions; len(got) != 1 || got[0] != "Parent sees synchronous Task output directly." {
		t.Fatalf("capability assumptions = %#v", got)
	}
	if got := facts.CapabilityUnknowns; len(got) != 1 || got[0] != "No browser flow sampled." {
		t.Fatalf("capability unknowns = %#v", got)
	}
	if got := facts.CapabilityVerification; len(got) != 1 || got[0] != "go test ./internal/tools/task -count=1" {
		t.Fatalf("capability verification = %#v", got)
	}
	if got := facts.CapabilityRisks; len(got) != 1 || got[0] != "Model may ignore returned fields." {
		t.Fatalf("capability risks = %#v", got)
	}
	if got := facts.CapabilityNextActions; len(got) != 1 || got[0] != "TASK_SYNC_NEXT_ACTION: parent should use the evidence before final answer." {
		t.Fatalf("capability next actions = %#v", got)
	}
	for _, want := range []string{
		"session_id: sync-task-session",
		"transcript_path: /tmp/sync-task.jsonl",
		"output_file: /tmp/sync-task.output",
		"worktree_path: /tmp/sync-task-worktree",
		"worktree_branch: agent/sync-task",
	} {
		if !containsString(facts.CapabilityArtifacts, want) {
			t.Fatalf("capability artifacts missing %q: %#v", want, facts.CapabilityArtifacts)
		}
	}
}

func TestExtractFactsCapturesPersistedOutputCapabilityLoopSummary(t *testing.T) {
	content := strings.Join([]string{
		"<persisted-output>",
		"Output too large (4096 bytes). Full output saved to: /tmp/session/tool-results/toolu_task.txt",
		"",
		"Capability loop summary preserved from full output:",
		"- status: failed",
		"- evidence: PERSISTED_COMPACT_EVIDENCE: tail evidence survived persisted-output.",
		"- assumptions: Parent only sees persisted summary unless it reads the file.",
		"- unknowns: Whether full tool output was inspected.",
		"- verification: go test ./internal/compact -run TestExtractFactsCapturesPersistedOutputCapabilityLoopSummary -count=1",
		"- risks: Compact could otherwise drop next_action.",
		"- next_action: PERSISTED_COMPACT_NEXT_ACTION: continue from preserved facts.",
		"",
		"Preview (first 2000 bytes):",
		"large prefix without structured evidence",
		"</persisted-output>",
	}, "\n")
	facts := ExtractFacts([]anthropic.MessageParam{{Role: "user", Content: []anthropic.ContentBlock{{
		Type:      "tool_result",
		ToolUseID: "toolu_task",
		Content:   content,
	}}}})
	if got := facts.CapabilityEvidence; len(got) != 1 || got[0] != "PERSISTED_COMPACT_EVIDENCE: tail evidence survived persisted-output." {
		t.Fatalf("capability evidence = %#v", got)
	}
	if got := facts.CapabilityAssumptions; len(got) != 1 || got[0] != "Parent only sees persisted summary unless it reads the file." {
		t.Fatalf("capability assumptions = %#v", got)
	}
	if got := facts.CapabilityUnknowns; len(got) != 1 || got[0] != "Whether full tool output was inspected." {
		t.Fatalf("capability unknowns = %#v", got)
	}
	if got := facts.CapabilityVerification; len(got) != 1 || got[0] != "go test ./internal/compact -run TestExtractFactsCapturesPersistedOutputCapabilityLoopSummary -count=1" {
		t.Fatalf("capability verification = %#v", got)
	}
	if got := facts.CapabilityRisks; len(got) != 1 || got[0] != "Compact could otherwise drop next_action." {
		t.Fatalf("capability risks = %#v", got)
	}
	if got := facts.CapabilityNextActions; len(got) != 1 || got[0] != "PERSISTED_COMPACT_NEXT_ACTION: continue from preserved facts." {
		t.Fatalf("capability next actions = %#v", got)
	}
	if markdown := facts.Markdown(); !strings.Contains(markdown, "Capability Loop Evidence") || !strings.Contains(markdown, "PERSISTED_COMPACT_NEXT_ACTION") {
		t.Fatalf("facts markdown missing persisted capability sections:\n%s", markdown)
	}
}

func TestExtractFactsCapturesCapabilityFollowUpGateResolution(t *testing.T) {
	text := strings.Join([]string{
		"## Agent capability follow-up gate",
		"- pending_follow_up: Task result completed: compact task; evidence_source: task_tool | follow_up_id: tool:compact-task | must_handle_next_action: COMPACT_GATE_NEXT_ACTION | verification_required: COMPACT_GATE_VERIFICATION | unknown_to_resolve_or_disclose: COMPACT_GATE_UNKNOWN | risk_to_account_for: COMPACT_GATE_RISK",
		"capability_loop: evidence: COMPACT_GATE_RESOLUTION_EVIDENCE | verification: COMPACT_GATE_RESOLUTION_VERIFICATION | resolved_follow_up: COMPACT_GATE_RESOLVED | supersedes_evidence_id: tool:compact-task",
	}, "\n")
	facts := ExtractFacts([]anthropic.MessageParam{textMessage("user", text)})
	for _, want := range []string{"tool:compact-task"} {
		if !containsString(facts.CapabilityFollowUpIDs, want) {
			t.Fatalf("capability follow-up ids missing %q: %#v", want, facts.CapabilityFollowUpIDs)
		}
		if !containsString(facts.CapabilitySupersedes, want) {
			t.Fatalf("capability supersedes missing %q: %#v", want, facts.CapabilitySupersedes)
		}
	}
	for _, want := range []string{"COMPACT_GATE_NEXT_ACTION"} {
		if !containsString(facts.CapabilityNextActions, want) {
			t.Fatalf("capability next actions missing %q: %#v", want, facts.CapabilityNextActions)
		}
	}
	for _, want := range []string{"COMPACT_GATE_RESOLVED"} {
		if !containsString(facts.CapabilityResolved, want) {
			t.Fatalf("capability resolved missing %q: %#v", want, facts.CapabilityResolved)
		}
	}
	markdown := facts.Markdown()
	for _, want := range []string{"Capability Loop Follow Up IDs", "Capability Loop Resolved Follow Ups", "Capability Loop Supersedes"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("facts markdown missing %q:\n%s", want, markdown)
		}
	}
}

func TestExtractFactsSkipsCapabilityLoopPlaceholders(t *testing.T) {
	jsonLoop := `{"capability_loop":{"evidence":["COMPACT_PLACEHOLDER_EVIDENCE"],"assumptions":["None observed"],"unknowns":["None observed"],"verification":["None observed"],"risks":["None observed"],"next_action":"COMPACT_PLACEHOLDER_NEXT_ACTION"}}`
	inlineLoop := "capability_loop: evidence: COMPACT_INLINE_EVIDENCE | assumptions: None observed | unknowns: N/A | verification: not applicable | risks: none | next_action: COMPACT_INLINE_NEXT_ACTION"
	markdownLoop := strings.Join([]string{
		"Conversation summary so far:",
		"## Runtime Extracted Facts",
		"Capability Loop Evidence:",
		"- COMPACT_MARKDOWN_EVIDENCE",
		"Capability Loop Assumptions:",
		"- None observed",
		"Capability Loop Unknowns:",
		"- None observed",
		"Capability Loop Verification:",
		"- None observed",
		"Capability Loop Risks:",
		"- None observed",
		"Capability Loop Next Actions:",
		"- COMPACT_MARKDOWN_NEXT_ACTION",
	}, "\n")
	facts := ExtractFacts([]anthropic.MessageParam{
		textMessage("user", jsonLoop),
		textMessage("assistant", inlineLoop),
		textMessage("user", markdownLoop),
	})
	for _, want := range []string{"COMPACT_PLACEHOLDER_EVIDENCE", "COMPACT_INLINE_EVIDENCE", "COMPACT_MARKDOWN_EVIDENCE"} {
		if !containsString(facts.CapabilityEvidence, want) {
			t.Fatalf("capability evidence missing %q: %#v", want, facts.CapabilityEvidence)
		}
	}
	for _, want := range []string{"COMPACT_PLACEHOLDER_NEXT_ACTION", "COMPACT_INLINE_NEXT_ACTION", "COMPACT_MARKDOWN_NEXT_ACTION"} {
		if !containsString(facts.CapabilityNextActions, want) {
			t.Fatalf("capability next action missing %q: %#v", want, facts.CapabilityNextActions)
		}
	}
	if len(facts.CapabilityAssumptions) != 0 || len(facts.CapabilityUnknowns) != 0 || len(facts.CapabilityVerification) != 0 || len(facts.CapabilityRisks) != 0 {
		t.Fatalf("placeholder capability facts should be filtered: assumptions=%#v unknowns=%#v verification=%#v risks=%#v", facts.CapabilityAssumptions, facts.CapabilityUnknowns, facts.CapabilityVerification, facts.CapabilityRisks)
	}
	markdown := facts.Markdown()
	for _, notWant := range []string{"None observed", "not applicable", "Capability Loop Unknowns", "Capability Loop Verification", "Capability Loop Risks"} {
		if strings.Contains(markdown, notWant) {
			t.Fatalf("facts markdown should skip placeholder %q:\n%s", notWant, markdown)
		}
	}
}

func textMessage(role, text string) anthropic.MessageParam {
	return anthropic.MessageParam{Role: role, Content: []anthropic.ContentBlock{{Type: "text", Text: text}}}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func compactSummaryFixture() string {
	return strings.Join([]string{
		"## Current Goal",
		"Continue implementing automatic compaction.",
		"## User Preferences / Constraints",
		"Use structured validation.",
		"## Decisions Made",
		"Preserve recent rounds.",
		"## Files / Code Changed",
		"internal/query/query.go",
		"README.md",
		"## Commands / Test Results",
		"go test ./internal/query",
		"## Open Tasks",
		"Finish tests.",
		"## Known Issues / Risks",
		"None.",
		"## Important Raw Facts",
		"internal/query/query.go and README.md were discussed; go test ./internal/query was requested.",
	}, "\n")
}

func marshalCompactGolden(t *testing.T, value any) string {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func assertCompactGolden(t *testing.T, name string, got string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(file), "testdata", "golden", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	want := strings.TrimRight(string(data), "\n")
	got = strings.TrimRight(got, "\n")
	if got != want {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
