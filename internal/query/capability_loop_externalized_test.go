package query

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/compact"
	"github.com/konglong87/super-niuma/internal/toolresult"
)

// TestExternalizedTaskResultKeepsFollowUpResolved is the end-to-end guard for
// TODO-094. A sub-agent resolves an earlier follow-up, but its result is large
// enough that toolresult externalizes it: the payload the parent had is replaced
// by a persisted-output stub, so the preserved summary becomes the only carrier
// of the supersede relation.
//
// Before the fix the summary rendered six fields and dropped resolved_follow_up
// and supersedes_evidence_id*, so the resolution was invisible and the follow-up
// the sub-agent had just closed reappeared as pending — the parent would be told
// it still could not finalize. Drop either half of the fix (toolresult rendering
// the fields, or capabilityloop reading them back and splitting the comma-joined
// id list) and this fails.
func TestExternalizedTaskResultKeepsFollowUpResolved(t *testing.T) {
	root := t.TempDir()
	session := toolresult.SessionRef{
		SessionID:      "session-externalized-resolution",
		TranscriptPath: filepath.Join(root, "session-externalized-resolution.jsonl"),
	}
	querySession := &Session{}

	pendingInput, err := json.Marshal(map[string]string{"description": "first pass audit"})
	if err != nil {
		t.Fatal(err)
	}
	querySession.rememberCapabilityLoopToolResult("Task", "toolu_pending", pendingInput,
		`{"status":"completed","capability_loop":{`+
			`"evidence":["PENDING_EVIDENCE: first pass found the gap."],`+
			`"next_action":"PENDING_NEXT_ACTION: close the gap before finalizing."}}`)

	gate := querySession.runtimeAgentEvidenceFollowUpReminderStatus()
	if !strings.Contains(gate, "follow_up_id: tool:toolu_pending") {
		t.Fatalf("first pass should be pending before the resolution arrives:\n%s", gate)
	}

	// The resolution result is too large to keep inline, and the prefix pushes the
	// payload past the 2000-byte preview window so nothing but the preserved
	// summary can carry it.
	resolutionPayload := strings.Repeat("unstructured-sub-agent-narration-", 90) + "\n" +
		"<capability_loop>\n" +
		`{"status":"completed","capability_loop":{` +
		`"evidence":["RESOLUTION_EVIDENCE: reran the audit and the gap is closed."],` +
		`"verification":["RESOLUTION_VERIFICATION: focused acceptance passed."],` +
		`"resolved_follow_up":"RESOLUTION_DONE: PENDING_NEXT_ACTION handled.",` +
		`"supersedes_evidence_ids":["tool:toolu_pending","tool:toolu_unrelated"]}}` +
		"\n</capability_loop>"

	externalized := toolresult.Process(resolutionPayload, toolresult.ProcessOptions{
		ToolName:  "Task",
		ToolUseID: "toolu_resolution",
		Limit:     1_000,
		Session:   session,
	})
	if !strings.Contains(externalized, "<persisted-output>") {
		t.Fatalf("result should have been externalized:\n%s", externalized)
	}
	if strings.Contains(externalized, "RESOLUTION_DONE: PENDING_NEXT_ACTION handled.") &&
		!strings.Contains(externalized, "Capability loop summary preserved from full output:") {
		t.Fatalf("resolution should only survive via the preserved summary:\n%s", externalized)
	}

	resolutionInput, err := json.Marshal(map[string]string{"description": "resolution pass"})
	if err != nil {
		t.Fatal(err)
	}
	querySession.rememberCapabilityLoopToolResult("Task", "toolu_resolution", resolutionInput, externalized)

	recent := querySession.runtimeRecentAgentEvidenceStatus()
	for _, want := range []string{
		"resolved_follow_up: RESOLUTION_DONE: PENDING_NEXT_ACTION handled.",
		"supersedes_evidence_id: tool:toolu_pending,tool:toolu_unrelated",
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("externalized resolution lost %q:\n%s", want, recent)
		}
	}

	gate = querySession.runtimeAgentEvidenceFollowUpReminderStatus()
	for _, notWant := range []string{
		"follow_up_id: tool:toolu_pending",
		"PENDING_NEXT_ACTION: close the gap before finalizing.",
	} {
		if strings.Contains(gate, notWant) {
			t.Fatalf("resolved follow-up came back as pending (%q):\n%s", notWant, gate)
		}
	}
}

// The same externalized stub is what compaction later reads, so the supersede
// relation has to survive that hop too, one id per fact rather than a single
// comma-joined string that matches no follow-up.
func TestExternalizedTaskResultKeepsSupersedeFactsForCompaction(t *testing.T) {
	root := t.TempDir()
	externalized := toolresult.Process(
		strings.Repeat("unstructured-sub-agent-narration-", 90)+"\n"+
			"<capability_loop>\n"+
			`{"status":"completed","capability_loop":{`+
			`"evidence":["COMPACT_RESOLUTION_EVIDENCE: gap closed."],`+
			`"follow_up_id":"tool:toolu_resolution",`+
			`"resolved_follow_up":"COMPACT_RESOLUTION_DONE",`+
			`"supersedes_evidence_ids":["tool:toolu_pending","tool:toolu_unrelated"]}}`+
			"\n</capability_loop>",
		toolresult.ProcessOptions{
			ToolName:  "Task",
			ToolUseID: "toolu_resolution",
			Limit:     1_000,
			Session: toolresult.SessionRef{
				SessionID:      "session-compact-resolution",
				TranscriptPath: filepath.Join(root, "session-compact-resolution.jsonl"),
			},
		})

	facts := compact.ExtractFacts([]anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_resolution",
			Content:   externalized,
		}},
	}})

	if len(facts.CapabilityResolved) != 1 || facts.CapabilityResolved[0] != "COMPACT_RESOLUTION_DONE" {
		t.Fatalf("resolved follow-up lost in compaction: %#v", facts.CapabilityResolved)
	}
	if len(facts.CapabilityFollowUpIDs) != 1 || facts.CapabilityFollowUpIDs[0] != "tool:toolu_resolution" {
		t.Fatalf("follow-up id lost in compaction: %#v", facts.CapabilityFollowUpIDs)
	}
	want := []string{"tool:toolu_pending", "tool:toolu_unrelated"}
	if len(facts.CapabilitySupersedes) != len(want) {
		t.Fatalf("supersedes = %#v, want %#v", facts.CapabilitySupersedes, want)
	}
	for i, id := range want {
		if facts.CapabilitySupersedes[i] != id {
			t.Fatalf("supersedes = %#v, want %#v", facts.CapabilitySupersedes, want)
		}
	}
}
