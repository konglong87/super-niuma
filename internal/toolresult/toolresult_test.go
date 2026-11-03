package toolresult

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

func TestProcessNormalizesEmptyResult(t *testing.T) {
	got := Process(" \n\t", ProcessOptions{ToolName: "Bash", Limit: 10})
	if got != "(Bash completed with no output)" {
		t.Fatalf("result = %q", got)
	}
}

func TestProcessFallsBackToTruncateWithoutSession(t *testing.T) {
	got := Process("abcdefghijklmnopqrstuvwxyz", ProcessOptions{ToolName: "Read", ToolUseID: "toolu_1", Limit: 10})
	if !strings.Contains(got, "abcdefghij") || !strings.Contains(got, "truncated") {
		t.Fatalf("result = %q", got)
	}
}

func TestProcessPersistsLargeResultWithPreview(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "session-1.jsonl")
	content := strings.Repeat("0123456789", 300)
	got := Process(content, ProcessOptions{
		ToolName:  "Read",
		ToolUseID: "../toolu/read:1",
		Limit:     10,
		Session: SessionRef{
			SessionID:      "session-1",
			TranscriptPath: transcript,
		},
	})
	if !strings.Contains(got, "<persisted-output>") || !strings.Contains(got, "Full output saved to:") || !strings.Contains(got, "Preview (first 2000 bytes):") {
		t.Fatalf("result = %q", got)
	}
	if strings.Contains(got, content) {
		t.Fatalf("persisted message contains full content")
	}
	path := filepath.Join(root, "session-1", "tool-results", "_toolu_read_1.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("persisted content mismatch")
	}
}

func TestProcessSurfacesShellTruncationBeforePreview(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "session-shell-truncated.jsonl")
	content := strings.Repeat("x", PreviewSizeBytes+100) + "\n[output truncated after 204800 bytes]"
	got := Process(content, ProcessOptions{
		ToolName:  "Bash",
		ToolUseID: "toolu_shell",
		Limit:     100,
		Session: SessionRef{
			SessionID:      "session-shell-truncated",
			TranscriptPath: transcript,
		},
	})
	for _, want := range []string{
		"<persisted-output>",
		"already truncated by the shell runner before persistence",
		"saved file also omits the tail",
		"rerun the command with narrower filters",
		"Read offset/limit",
		"Preview (first 2000 bytes):",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "already truncated by the shell runner") > strings.Index(got, "Preview (first 2000 bytes):") {
		t.Fatalf("shell truncation warning should be visible before preview:\n%s", got)
	}
}

func TestProcessPreservesCapabilityLoopSummaryOutsidePreview(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "session-capability-loop.jsonl")
	content := strings.Repeat("prefix-without-structured-evidence-", 90) + strings.Join([]string{
		"",
		"<capability_loop>",
		`{"status":"failed","capability_loop":{"evidence":["CAPABILITY_PERSIST_EVIDENCE: long Task output kept tail evidence."],"assumptions":["Parent may only see persisted-output preview."],"unknowns":["Whether user will inspect saved file."],"verification":["go test ./internal/toolresult -run TestProcessPreservesCapabilityLoopSummaryOutsidePreview -count=1"],"risks":["Preview-only context could lose the action plan."],"next_action":"CAPABILITY_PERSIST_NEXT_ACTION: continue from preserved summary before finalizing."}}`,
		"</capability_loop>",
	}, "\n")

	got := Process(content, ProcessOptions{
		ToolName:  "Task",
		ToolUseID: "toolu_capability_loop",
		Limit:     100,
		Session: SessionRef{
			SessionID:      "session-capability-loop",
			TranscriptPath: transcript,
		},
	})

	for _, want := range []string{
		"<persisted-output>",
		"Capability loop summary preserved from full output:",
		"status: failed",
		"evidence: CAPABILITY_PERSIST_EVIDENCE: long Task output kept tail evidence.",
		"unknowns: Whether user will inspect saved file.",
		"verification: go test ./internal/toolresult -run TestProcessPreservesCapabilityLoopSummaryOutsidePreview -count=1",
		"risks: Preview-only context could lose the action plan.",
		"next_action: CAPABILITY_PERSIST_NEXT_ACTION: continue from preserved summary before finalizing.",
		"Preview (first 2000 bytes):",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "Capability loop summary preserved") > strings.Index(got, "Preview (first 2000 bytes):") {
		t.Fatalf("capability loop summary should appear before preview:\n%s", got)
	}
	data, err := os.ReadFile(filepath.Join(root, "session-capability-loop", "tool-results", "toolu_capability_loop.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("persisted content mismatch")
	}
}

func TestApplyMessageBudgetPersistsLargestResultsUntilUnderLimit(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-2", TranscriptPath: filepath.Join(root, "session-2.jsonl")}
	large := strings.Repeat("a", 9000)
	medium := strings.Repeat("b", 6000)
	small := strings.Repeat("c", 2000)
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_large", Content: large},
			{Type: "tool_result", ToolUseID: "toolu_medium", Content: medium},
			{Type: "tool_result", ToolUseID: "toolu_small", Content: small},
		},
	}}
	got := ApplyMessageBudget(messages, BudgetOptions{Limit: 12000, Session: session})
	blocks := got[0].Content
	if !strings.Contains(blocks[0].Content, "<persisted-output>") {
		t.Fatalf("largest result was not persisted: %+v", blocks[0])
	}
	if blocks[1].Content != medium || blocks[2].Content != small {
		t.Fatalf("unexpected replacements: %+v", blocks)
	}
	data, err := os.ReadFile(filepath.Join(root, "session-2", "tool-results", "toolu_large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != large {
		t.Fatalf("persisted content mismatch")
	}
}

func TestApplyMessageBudgetSkipsAlreadyPersistedOutput(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-3", TranscriptPath: filepath.Join(root, "session-3.jsonl")}
	persisted := "<persisted-output>\nOutput too large. Full output saved to: /tmp/x\n</persisted-output>"
	full := strings.Repeat("z", 80)
	messages := []anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_done", Content: persisted},
			{Type: "tool_result", ToolUseID: "toolu_full", Content: full},
		},
	}}
	got := ApplyMessageBudget(messages, BudgetOptions{Limit: 100, Session: session})
	if got[0].Content[0].Content != persisted {
		t.Fatalf("already persisted result changed: %q", got[0].Content[0].Content)
	}
	if !strings.Contains(got[0].Content[1].Content, "<persisted-output>") {
		t.Fatalf("full result was not persisted: %q", got[0].Content[1].Content)
	}
}

func TestApplyMessageBudgetSkipsConfiguredToolNames(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-read-skip", TranscriptPath: filepath.Join(root, "session-read-skip.jsonl")}
	readOutput := strings.Repeat("R", 30000)
	bashOutput := strings.Repeat("B", 12000)
	messages := []anthropic.MessageParam{
		{Role: "assistant", Content: []anthropic.ContentBlock{
			{Type: "tool_use", ID: "toolu_read", Name: "Read"},
			{Type: "tool_use", ID: "toolu_bash", Name: "Bash"},
		}},
		{Role: "user", Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_read", Content: readOutput},
			{Type: "tool_result", ToolUseID: "toolu_bash", Content: bashOutput},
		}},
	}

	got := ApplyMessageBudget(messages, BudgetOptions{
		Limit:         10000,
		Session:       session,
		SkipToolNames: map[string]bool{"Read": true},
	})

	if got[1].Content[0].Content != readOutput {
		t.Fatalf("Read result should not be persisted by aggregate budget")
	}
	if !strings.Contains(got[1].Content[1].Content, "<persisted-output>") {
		t.Fatalf("eligible Bash result was not persisted: %q", got[1].Content[1].Content)
	}
	if _, err := os.Stat(filepath.Join(root, "session-read-skip", "tool-results", "toolu_read.txt")); !os.IsNotExist(err) {
		t.Fatalf("Read output should not be persisted, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "session-read-skip", "tool-results", "toolu_bash.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != bashOutput {
		t.Fatalf("persisted Bash content mismatch")
	}
}

func TestApplyMessageBudgetReappliesKnownReplacement(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-known-replacement", TranscriptPath: filepath.Join(root, "session-known-replacement.jsonl")}
	original := strings.Repeat("O", 9000)
	replacement := "<persisted-output>\nprevious preview\n</persisted-output>"
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_known", Content: original}},
	}}

	got := ApplyMessageBudget(messages, BudgetOptions{
		Limit:        20_000,
		Session:      session,
		Replacements: map[string]string{"toolu_known": replacement},
	})

	if got[0].Content[0].Content != replacement {
		t.Fatalf("known replacement was not reapplied: %.120q", got[0].Content[0].Content)
	}
	if _, err := os.Stat(filepath.Join(root, "session-known-replacement", "tool-results", "toolu_known.txt")); !os.IsNotExist(err) {
		t.Fatalf("known replacement should not rewrite persisted file, stat err=%v", err)
	}
}

func TestApplyMessageBudgetFreezesKnownUnreplacedResult(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-frozen-message", TranscriptPath: filepath.Join(root, "session-frozen-message.jsonl")}
	original := strings.Repeat("F", 9000)
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_frozen", Content: original}},
	}}

	got := ApplyMessageBudget(messages, BudgetOptions{
		Limit:          100,
		Session:        session,
		SeenToolUseIDs: map[string]bool{"toolu_frozen": true},
	})

	if got[0].Content[0].Content != original {
		t.Fatalf("frozen result changed: %.120q", got[0].Content[0].Content)
	}
	if _, err := os.Stat(filepath.Join(root, "session-frozen-message", "tool-results", "toolu_frozen.txt")); !os.IsNotExist(err) {
		t.Fatalf("frozen result should not be persisted, stat err=%v", err)
	}
}

func TestApplyHistoryBudgetPersistsOlderResultsAndKeepsLatest(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-4", TranscriptPath: filepath.Join(root, "session-4.jsonl")}
	oldLarge := strings.Repeat("L", 9000)
	oldMedium := strings.Repeat("M", 4000)
	latest := strings.Repeat("N", 7000)
	messages := []anthropic.MessageParam{
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "text", Text: "start"}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_old_large", Name: "Echo"}}},
		{Role: "user", Content: []anthropic.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_old_large", Content: oldLarge},
			{Type: "tool_result", ToolUseID: "toolu_old_medium", Content: oldMedium},
		}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_latest", Name: "Echo"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_latest", Content: latest}}},
	}

	got := ApplyHistoryBudget(messages, BudgetOptions{Limit: 12000, Session: session})
	oldBlocks := got[2].Content
	if !strings.Contains(oldBlocks[0].Content, "<persisted-output>") || !strings.Contains(oldBlocks[1].Content, "<persisted-output>") {
		t.Fatalf("older results were not persisted: %+v", oldBlocks)
	}
	if got[4].Content[0].Content != latest {
		t.Fatalf("latest result changed")
	}
	for name, want := range map[string]string{
		"toolu_old_large.txt":  oldLarge,
		"toolu_old_medium.txt": oldMedium,
	} {
		data, err := os.ReadFile(filepath.Join(root, "session-4", "tool-results", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s content mismatch", name)
		}
	}
}

func TestApplyHistoryBudgetFreezesKnownUnreplacedResult(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-history-frozen", TranscriptPath: filepath.Join(root, "session-history-frozen.jsonl")}
	old := strings.Repeat("O", 9000)
	latest := strings.Repeat("N", 7000)
	messages := []anthropic.MessageParam{
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_old", Name: "Echo"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_old", Content: old}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_latest", Name: "Echo"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_latest", Content: latest}}},
	}

	got := ApplyHistoryBudget(messages, BudgetOptions{
		Limit:          100,
		Session:        session,
		SeenToolUseIDs: map[string]bool{"toolu_old": true},
	})

	if got[1].Content[0].Content != old {
		t.Fatalf("frozen old result changed: %.120q", got[1].Content[0].Content)
	}
	if got[3].Content[0].Content != latest {
		t.Fatalf("latest result changed")
	}
	if _, err := os.Stat(filepath.Join(root, "session-history-frozen", "tool-results", "toolu_old.txt")); !os.IsNotExist(err) {
		t.Fatalf("frozen old result should not be persisted, stat err=%v", err)
	}
}

func TestApplyHistoryBudgetKeepsLiveSeenUnreplacedResult(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-history-live-seen", TranscriptPath: filepath.Join(root, "session-history-live-seen.jsonl")}
	seen := map[string]bool{}
	old := strings.Repeat("O", 9000)
	latest := strings.Repeat("N", 7000)
	firstTurn := []anthropic.MessageParam{
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_old", Name: "Echo"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_old", Content: old}}},
	}

	got := ApplyMessageBudget(firstTurn, BudgetOptions{
		Limit:          20_000,
		Session:        session,
		SeenToolUseIDs: seen,
	})
	if got[1].Content[0].Content != old {
		t.Fatalf("first turn old result changed: %.120q", got[1].Content[0].Content)
	}
	if !seen["toolu_old"] {
		t.Fatalf("old result was not marked seen")
	}

	secondTurn := append([]anthropic.MessageParam(nil), firstTurn...)
	secondTurn = append(secondTurn,
		anthropic.MessageParam{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_latest", Name: "Echo"}}},
		anthropic.MessageParam{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_latest", Content: latest}}},
	)
	got = ApplyHistoryBudget(secondTurn, BudgetOptions{
		Limit:          100,
		Session:        session,
		SeenToolUseIDs: seen,
	})

	if got[1].Content[0].Content != old {
		t.Fatalf("live seen old result changed: %.120q", got[1].Content[0].Content)
	}
	if got[3].Content[0].Content != latest {
		t.Fatalf("latest result changed")
	}
	if _, err := os.Stat(filepath.Join(root, "session-history-live-seen", "tool-results", "toolu_old.txt")); !os.IsNotExist(err) {
		t.Fatalf("live seen old result should not be persisted, stat err=%v", err)
	}
}

func TestApplyHistoryBudgetSkipsConfiguredToolNames(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-history-read-skip", TranscriptPath: filepath.Join(root, "session-history-read-skip.jsonl")}
	readOutput := strings.Repeat("R", 30000)
	bashOutput := strings.Repeat("B", 12000)
	latest := strings.Repeat("N", 1000)
	messages := []anthropic.MessageParam{
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_read", Name: "Read"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_read", Content: readOutput}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_bash", Name: "Bash"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_bash", Content: bashOutput}}},
		{Role: "assistant", Content: []anthropic.ContentBlock{{Type: "tool_use", ID: "toolu_latest", Name: "Bash"}}},
		{Role: "user", Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_latest", Content: latest}}},
	}

	got := ApplyHistoryBudget(messages, BudgetOptions{
		Limit:         10000,
		Session:       session,
		SkipToolNames: map[string]bool{"Read": true},
	})

	if got[1].Content[0].Content != readOutput {
		t.Fatalf("Read result should not be persisted by history budget")
	}
	if !strings.Contains(got[3].Content[0].Content, "<persisted-output>") {
		t.Fatalf("older eligible Bash result was not persisted: %q", got[3].Content[0].Content)
	}
	if got[5].Content[0].Content != latest {
		t.Fatalf("latest result changed")
	}
	if _, err := os.Stat(filepath.Join(root, "session-history-read-skip", "tool-results", "toolu_read.txt")); !os.IsNotExist(err) {
		t.Fatalf("Read output should not be persisted, stat err=%v", err)
	}
}

func TestApplyHistoryBudgetKeepsLatestWhenNoOlderCandidates(t *testing.T) {
	root := t.TempDir()
	session := SessionRef{SessionID: "session-5", TranscriptPath: filepath.Join(root, "session-5.jsonl")}
	latest := strings.Repeat("N", 9000)
	messages := []anthropic.MessageParam{{
		Role:    "user",
		Content: []anthropic.ContentBlock{{Type: "tool_result", ToolUseID: "toolu_latest", Content: latest}},
	}}

	got := ApplyHistoryBudget(messages, BudgetOptions{Limit: 100, Session: session})
	if got[0].Content[0].Content != latest {
		t.Fatalf("latest-only result should not be collapsed")
	}
}
