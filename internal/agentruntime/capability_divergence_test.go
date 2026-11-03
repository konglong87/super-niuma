package agentruntime

import (
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/capabilityloop"
)

// TODO-100: a sub-agent that reported nothing at all reported nothing at all,
// whatever terminal status it died with.
//
// capabilityLoopFromContent injects a status-specific default next_action for
// five statuses, but defaultNextActions inside
// CapabilityLoopHasActionableEvidence only listed four of them — timeout was
// missing. So an entirely empty timeout fell through to "return true" on the
// strength of boilerplate the runtime had just written itself, while an
// identically empty completed sub-agent correctly returned false. The verdict
// was inverted for exactly one status.
//
// Both directions are asserted, because pinning only the timeout side would let
// a future fix that flipped completed to true pass while still disagreeing.
func TestEmptySubAgentHasNoActionableEvidenceForEveryTerminalStatus(t *testing.T) {
	statuses := []string{
		agenttasks.StatusCompleted,
		agenttasks.StatusFailed,
		agenttasks.StatusCancelled,
		agenttasks.StatusTimeout,
		"some_unrecognized_status",
	}
	for _, status := range statuses {
		// Empty content: every field below is runtime-injected boilerplate, so
		// there is no sub-agent signal anywhere in the loop.
		loop := capabilityLoopFromContent("", status)
		if CapabilityLoopHasActionableEvidence(&loop) {
			t.Errorf("status %q: an empty sub-agent must not count as actionable evidence; loop=%#v", status, loop)
		}
	}
}

// The other half of TODO-100: the fix must not make the verdict blind. A timeout
// that produced real partial content before dying is genuinely actionable, and
// that is the case the failure-path defaults exist to protect.
func TestTimeoutWithPartialContentStillHasActionableEvidence(t *testing.T) {
	loop := capabilityLoopFromContent("PARTIAL_TIMEOUT_CONTENT: migration reached step 3 of 7 before the deadline.", agenttasks.StatusTimeout)
	if !CapabilityLoopHasActionableEvidence(&loop) {
		t.Fatalf("a timeout carrying partial content is actionable evidence; loop=%#v", loop)
	}
}

// TODO-101 ②: agentruntime kept a private placeholder set that omitted the
// machine-injected "completed" default next_action, so that boilerplate counted
// as a real sub-agent signal here while capabilityloop and compact filtered it.
// Left unfiltered it alone could carry an otherwise contentless loop past the
// verdict and into the parent's decision context.
func TestAgentRuntimePlaceholderSetMatchesProtocol(t *testing.T) {
	placeholders := []string{
		"", "none", "None observed", "not observed.", "N/A", "na", "not applicable",
		"No explicit assumptions reported.", "No explicit unknowns reported.",
		"No explicit verification reported.",
		"Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing.",
	}
	for _, value := range placeholders {
		if !isCapabilityLoopPlaceholder(value) {
			t.Errorf("isCapabilityLoopPlaceholder(%q) = false, protocol says it is a placeholder", value)
		}
		if !capabilityloop.IsPlaceholder(value) {
			t.Errorf("capabilityloop.IsPlaceholder(%q) = false; test fixture drifted from the protocol", value)
		}
	}

	// The failure-path defaults are deliberately NOT placeholders: they tell the
	// parent it has a real failure to handle.
	signals := []string{
		"REAL_SIGNAL: the build failed on the changed path.",
		"Parent agent should inspect the failure, preserve any partial evidence, and decide whether to retry or answer with the limitation.",
		"Parent agent should preserve partial evidence, inspect transcript_path or output_file, then retry with a narrower scope or longer profile only if needed.",
	}
	for _, value := range signals {
		if isCapabilityLoopPlaceholder(value) {
			t.Errorf("isCapabilityLoopPlaceholder(%q) = true, but it carries real signal", value)
		}
	}
}

// The observable consequence of TODO-101 ②: the completed default alone must not
// be enough to attach a structured decision-context block to the parent's turn.
func TestCompletedDefaultBoilerplateAloneIsNotActionableEvidence(t *testing.T) {
	completedDefault := "Parent agent should synthesize the sub-agent result against the user's goal and verify any unproven claims before finalizing."
	loop := CapabilityLoop{
		Evidence:   []string{completedDefault},
		NextAction: completedDefault,
	}
	if CapabilityLoopHasActionableEvidence(&loop) {
		t.Fatalf("boilerplate echoed into evidence is still boilerplate; loop=%#v", loop)
	}
}
