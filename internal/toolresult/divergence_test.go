package toolresult

import (
	"path/filepath"
	"strings"
	"testing"
)

func persistLarge(t *testing.T, payload string) string {
	t.Helper()
	root := t.TempDir()
	// The prefix pushes the payload past the 2000-byte preview window, so the
	// preserved summary is the only place the capability_loop can survive.
	content := strings.Repeat("prefix-without-structured-evidence-", 90) + "\n" + payload
	return Process(content, ProcessOptions{
		ToolName:  "Task",
		ToolUseID: "toolu_divergence",
		Limit:     100,
		Session: SessionRef{
			SessionID:      "session-divergence",
			TranscriptPath: filepath.Join(root, "session-divergence.jsonl"),
		},
	})
}

// TODO-092 decision (a): tag matching is ASCII-case-insensitive. A sub-agent that
// capitalizes the tag previously lost its whole summary on this path.
func TestPersistedSummaryMatchesTagsCaseInsensitively(t *testing.T) {
	got := persistLarge(t, strings.Join([]string{
		"<Capability_Loop>",
		`{"capability_loop":{"evidence":["MIXED_CASE_SUMMARY_EVIDENCE: capitalized tag still summarized."]}}`,
		"</CAPABILITY_LOOP>",
	}, "\n"))

	for _, want := range []string{
		"Capability loop summary preserved from full output:",
		"evidence: MIXED_CASE_SUMMARY_EVIDENCE: capitalized tag still summarized.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, got)
		}
	}
}

// TODO-092 decision (b): every tag pair is a candidate, so a malformed leading
// pair no longer discards the valid payload behind it.
func TestPersistedSummaryTakesEveryTagPair(t *testing.T) {
	got := persistLarge(t, strings.Join([]string{
		"<capability_loop>",
		`{"capability_loop":{"evidence":[`,
		"</capability_loop>",
		"<capability_loop>",
		`{"capability_loop":{"evidence":["MULTI_TAG_SUMMARY_EVIDENCE: second pair summarized."]}}`,
		"</capability_loop>",
	}, "\n"))

	if !strings.Contains(got, "evidence: MULTI_TAG_SUMMARY_EVIDENCE: second pair summarized.") {
		t.Fatalf("persisted summary should use the second tag pair:\n%s", got)
	}
}

// TODO-094: the preserved summary must carry the follow-up identity and the
// supersede relation. Dropping them is what let an already-resolved follow-up
// return to pending after the result was externalized.
func TestPersistedSummaryPreservesFollowUpAndSupersedeFields(t *testing.T) {
	got := persistLarge(t, strings.Join([]string{
		"<capability_loop>",
		`{"status":"completed","capability_loop":{` +
			`"evidence":["RESOLUTION_SUMMARY_EVIDENCE: inspected the superseded result."],` +
			`"verification":["RESOLUTION_SUMMARY_VERIFICATION: focused acceptance passed."],` +
			`"follow_up_id":"tool:toolu_self",` +
			`"resolved_follow_up":"RESOLUTION_SUMMARY_RESOLVED: old next_action handled.",` +
			`"supersedes_evidence_id":"tool:toolu_first",` +
			`"supersedes_evidence_ids":["tool:toolu_second","tool:toolu_third"]}}`,
		"</capability_loop>",
	}, "\n"))

	for _, want := range []string{
		"resolved_follow_up: RESOLUTION_SUMMARY_RESOLVED: old next_action handled.",
		"supersedes_evidence_id: tool:toolu_first",
		"supersedes_evidence_ids: tool:toolu_second,tool:toolu_third",
		"follow_up_id: tool:toolu_self",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("persisted summary missing %q:\n%s", want, got)
		}
	}
}
