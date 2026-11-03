package compact

import (
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/anthropic"
)

// TODO-093 decision ①: the machine-injected "completed" default next_action is a
// placeholder on every path, not just in the runtime-status one. Keeping it here
// let compaction resurrect a next_action the live gate had already suppressed,
// and — because it alone satisfies HasHint — admit a phantom decision context
// into the parent's three-slot evidence ring.
func TestExtractFactsDropsDefaultCompletedNextAction(t *testing.T) {
	facts := ExtractFacts([]anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_default_next_action",
			Content: `{"capability_loop":{"evidence":["DEFAULT_NEXT_ACTION_EVIDENCE: real evidence stays."],` +
				`"next_action":"Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."}}`,
		}},
	}})

	if len(facts.CapabilityEvidence) == 0 {
		t.Fatalf("real evidence should survive: %#v", facts)
	}
	if len(facts.CapabilityNextActions) != 0 {
		t.Fatalf("default completed next_action should be dropped, got %#v", facts.CapabilityNextActions)
	}
}

// The failure-path defaults are deliberately NOT placeholders: they tell the
// parent it must handle a failure, and the follow-up gate is tested to surface
// them. Unifying the placeholder set must not swallow those too.
func TestExtractFactsKeepsFailurePathDefaultNextAction(t *testing.T) {
	facts := ExtractFacts([]anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_failed_next_action",
			Content: `{"capability_loop":{"evidence":["FAILED_NEXT_ACTION_EVIDENCE: partial evidence."],` +
				`"next_action":"Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation."}}`,
		}},
	}})

	if len(facts.CapabilityNextActions) != 1 ||
		!strings.Contains(facts.CapabilityNextActions[0], "inspect the failure") {
		t.Fatalf("failure-path default next_action should survive, got %#v", facts.CapabilityNextActions)
	}
}

// TODO-092 decisions (a) and (b) reach compaction too: fact extraction shares the
// one candidate extractor, so a capitalized tag and a malformed leading pair no
// longer hide the payload from the compacted hard facts.
func TestExtractFactsUsesSharedCandidateExtractor(t *testing.T) {
	facts := ExtractFacts([]anthropic.MessageParam{{
		Role: "user",
		Content: []anthropic.ContentBlock{{
			Type:      "tool_result",
			ToolUseID: "toolu_shared_candidates",
			Content: strings.Join([]string{
				"<capability_loop>",
				`{"capability_loop":{"evidence":[`,
				"</capability_loop>",
				"<Capability_Loop>",
				`{"capability_loop":{"evidence":["SHARED_CANDIDATE_EVIDENCE: later capitalized pair parsed."]}}`,
				"</CAPABILITY_LOOP>",
			}, "\n"),
		}},
	}})

	if len(facts.CapabilityEvidence) != 1 ||
		!strings.Contains(facts.CapabilityEvidence[0], "SHARED_CANDIDATE_EVIDENCE") {
		t.Fatalf("shared extractor should recover the later pair, got %#v", facts.CapabilityEvidence)
	}
}
