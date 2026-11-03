package capabilityloop

import (
	"strings"
	"testing"
)

func TestAgentEvidenceFollowUpSkipsPlaceholderFields(t *testing.T) {
	loop := HintData{
		Evidence:     []string{"QUERY_PLACEHOLDER_EVIDENCE: concrete file evidence remains."},
		Assumptions:  []string{"None observed"},
		Unknowns:     []string{"None observed"},
		Verification: []string{"None observed"},
		Risks:        []string{"None observed"},
		NextAction:   "QUERY_PLACEHOLDER_NEXT_ACTION: continue from concrete evidence.",
	}
	hint := TaskHint(loop)
	for _, want := range []string{
		"capability_loop: evidence: QUERY_PLACEHOLDER_EVIDENCE",
		"next_action: QUERY_PLACEHOLDER_NEXT_ACTION",
	} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint missing %q:\n%s", want, hint)
		}
	}
	for _, notWant := range []string{"None observed", "assumptions:", "unknowns:", "verification:", "risks:"} {
		if strings.Contains(hint, notWant) {
			t.Fatalf("hint should skip placeholder %q:\n%s", notWant, hint)
		}
	}

	line := FollowUpLine(DecisionContext{
		ToolName:       "Task",
		Description:    "placeholder audit",
		Status:         "completed",
		EvidenceSource: "task_tool",
		Loop:           loop,
	})
	for _, want := range []string{
		"pending_follow_up: Task result completed: placeholder audit",
		"evidence_source: task_tool",
		"must_handle_next_action: QUERY_PLACEHOLDER_NEXT_ACTION",
		"source_action: Synchronous tool_result evidence is already in the parent turn",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q:\n%s", want, line)
		}
	}
	for _, notWant := range []string{"None observed", "verification_required:", "unknown_to_resolve_or_disclose:", "risk_to_account_for:"} {
		if strings.Contains(line, notWant) {
			t.Fatalf("line should skip placeholder %q:\n%s", notWant, line)
		}
	}
}

func TestAgentEvidenceFollowUpSkipsDefaultNextAction(t *testing.T) {
	loop := HintData{
		Evidence:   []string{"QUERY_DEFAULT_NEXT_ACTION_EVIDENCE: concrete evidence remains."},
		NextAction: "Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing.",
	}
	hint := TaskHint(loop)
	if strings.Contains(hint, "next_action:") {
		t.Fatalf("hint should skip default next_action:\n%s", hint)
	}
	line := FollowUpLine(DecisionContext{
		ToolName:       "Task",
		Description:    "default next action audit",
		Status:         "completed",
		EvidenceSource: "task_tool",
		Loop:           loop,
	})
	if line != "" {
		t.Fatalf("default next_action should not create follow-up gate:\n%s", line)
	}
}
