package goal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

// completedDefaultNextAction is the next_action internal/agentruntime injects
// when a *completed* sub-agent reported none of its own. It is machine-generated
// boilerplate that restates what the parent already knows, so neither driver may
// present it to the parent as an obligation to discharge.
const completedDefaultNextAction = "Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."

// TODO-101, the cross-driver half: one sub-agent output, both drivers, same
// conclusion.
//
// The same sub-agent result reaches the parent through two different drivers —
// internal/goal drives it through goalCapabilityFollowUpLine, internal/query
// through capabilityloop.FollowUpLine — and both render a follow-up gate line
// from it. Before the fix the two disagreed about exactly one string: goal's
// private placeholder set omitted the machine-injected "completed" default, so
// goal published it as must_handle_next_action while query suppressed it. The
// parent was then told to discharge an obligation that does not exist, but only
// when it happened to be running under the goal driver.
//
// Asserting each side against a hard-coded expectation would not catch that:
// the defect is the *disagreement*, so the assertion has to compare the two
// drivers against each other on one shared input.
func TestCapabilityFollowUpGateAgreesAcrossGoalAndQueryDrivers(t *testing.T) {
	// Real evidence, so both drivers admit the payload and the only thing left
	// to disagree about is the injected next_action.
	loop := agentCapabilityLoop{
		Evidence:   []string{"CROSS_DRIVER_EVIDENCE: focused acceptance passed on the changed path."},
		NextAction: completedDefaultNextAction,
	}

	payload, err := json.Marshal(goalCapabilityEvidencePayload{
		EvidenceSource: "agent_get",
		AgentStatus:    "completed",
		CapabilityLoop: loop,
	})
	if err != nil {
		t.Fatalf("marshal goal payload: %v", err)
	}
	goalLine := goalCapabilityFollowUpLine(GoalEvidence{
		ID:      "ev_cross_driver",
		Type:    EvidenceTypeArtifact,
		Summary: "sub-agent completed with no next_action of its own",
		Passed:  true,
		Payload: payload,
	})

	queryLine := capabilityloop.FollowUpLine(capabilityloop.DecisionContext{
		EvidenceSource: "agent_get",
		Status:         "completed",
		Loop: capabilityloop.HintData{
			Evidence:   loop.Evidence,
			NextAction: loop.NextAction,
		},
	})

	goalClaims := strings.Contains(goalLine, capabilityloop.FollowUpFieldNextAction)
	queryClaims := strings.Contains(queryLine, capabilityloop.FollowUpFieldNextAction)

	if goalClaims != queryClaims {
		t.Fatalf("drivers disagree on %s for the same sub-agent output:\n  goal  (claims=%v): %s\n  query (claims=%v): %s",
			capabilityloop.FollowUpFieldNextAction, goalClaims, goalLine, queryClaims, queryLine)
	}
	if goalClaims {
		t.Fatalf("neither driver should present the machine-injected completed default as an obligation, both did:\n  goal:  %s\n  query: %s", goalLine, queryLine)
	}
}

// TODO-101, the set-identity half. The cross-driver test above pins the one
// string that actually diverged; this pins the whole set, so a future entry
// added on one side alone fails here instead of silently reopening the
// divergence on a value no test happens to exercise.
//
// actionableCapabilityText is goal's own filter, so this compares the driver's
// observable behavior against the protocol rather than two spellings of the same
// set.
func TestGoalActionableTextFiltersExactlyTheProtocolPlaceholders(t *testing.T) {
	values := []string{
		"", "none", "None observed", "not observed.", "N/A", "na", "not applicable",
		"No explicit assumptions reported.", "No explicit unknowns reported.",
		"No explicit verification reported.", completedDefaultNextAction,
		// Real signals, to prove goal does not over-filter. The failure-path
		// defaults in particular must survive: they tell the parent it has a real
		// failure to handle.
		"REAL_SIGNAL: the build failed on the changed path.",
		"Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
		"Parent agent should preserve partial evidence, inspect transcript_path or output_file, then retry with a narrower scope or longer profile only if needed.",
	}
	for _, value := range values {
		filtered := actionableCapabilityText(value) == ""
		if want := capabilityloop.IsPlaceholder(value); filtered != want {
			t.Errorf("actionableCapabilityText(%q) filtered=%v, protocol placeholder=%v", value, filtered, want)
		}
	}
}
