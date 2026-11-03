package agents

import (
	"strings"
	"testing"
)

// TestReadOnlyBuiltInsDenyDelegationTools pins the deny lists of the read-only
// built-ins. Their prompts promise "READ-ONLY ... STRICTLY PROHIBITED", but
// denying only "Agent" left Task / AgentCreate / AgentMessage reachable, so
// either agent could spawn a writable general-purpose sub-agent and nest without
// bound (AUDIT-P0-14).
func TestReadOnlyBuiltInsDenyDelegationTools(t *testing.T) {
	mustDeny := []string{"Agent", "AgentCreate", "AgentMessage", "SendMessage", "Task"}
	for _, name := range []string{"Explore", "Plan"} {
		agent, ok := BuiltIn(name)
		if !ok {
			t.Fatalf("BuiltIn(%q) not found", name)
		}
		denied := "," + join(agent.DisallowedTools) + ","
		for _, tool := range mustDeny {
			if !strings.Contains(denied, ","+tool+",") {
				t.Fatalf("%s does not deny %s (deny list: %s); a read-only agent must not be able to delegate", name, tool, denied)
			}
		}
		// The write tools it already denied must stay denied.
		for _, tool := range []string{"Edit", "Write", "NotebookEdit", "ExitPlanMode"} {
			if !strings.Contains(denied, ","+tool+",") {
				t.Fatalf("%s stopped denying %s (deny list: %s)", name, tool, denied)
			}
		}
	}
}

// TestGeneralPurposeKeepsFullToolAccess guards the other direction: the depth
// counter — not a deny list — is what bounds general-purpose, so narrowing its
// tools here would be a silent capability regression.
func TestGeneralPurposeKeepsFullToolAccess(t *testing.T) {
	agent, ok := BuiltIn("general-purpose")
	if !ok {
		t.Fatal("general-purpose built-in not found")
	}
	if join(agent.Tools) != "*" || len(agent.DisallowedTools) != 0 {
		t.Fatalf("general-purpose = tools %v denied %v, want Tools:[\"*\"] and no deny list", agent.Tools, agent.DisallowedTools)
	}
}
