package bash

import (
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/product"
	"github.com/konglong87/super-niuma/internal/tools"
)

const allowDestructiveEnv = "NIUMA_ALLOW_DESTRUCTIVE"

// destructiveDecision is what the hard-refusal layer concluded about one command.
type destructiveDecision struct {
	// Reason is the refusal message; empty means the command may run.
	Reason string
	// SuppressedReason carries the refusal that NIUMA_ALLOW_DESTRUCTIVE=1
	// turned off. It is set only when the rule table actually objected, so the
	// caller can tell "the override is set" from "the override changed the
	// outcome" and audit only the latter.
	SuppressedReason string
}

// Overridden reports whether the override changed this command's outcome.
func (d destructiveDecision) Overridden() bool { return d.SuppressedReason != "" }

// dangerousCommandDecision decides whether the Bash tool must refuse a command.
//
// The rule table lives in internal/permissions so this hard-refusal layer and
// the permission-prompt layer read the same definitions. Keeping two regex sets
// in sync had already failed: `rm -r -f /` was destructive to the prompt layer
// but passed this one.
//
// NIUMA_ALLOW_DESTRUCTIVE=1 is the only switch that disables this layer, and
// this layer is the only one that survives --dangerously-skip-permissions — an
// explicit permissions.deny rule aside. So the classification now runs first and
// the override is applied to its result, rather than short-circuiting ahead of
// it: an override that leaves no trace means nobody can tell afterwards why a
// destructive command was allowed to run. The extra work when the override is set
// is one shell parse per Bash call.
func dangerousCommandDecision(command string) destructiveDecision {
	reason := permissions.HardDenyShellReason(command)
	if reason == "" {
		return destructiveDecision{}
	}
	if product.Getenv(allowDestructiveEnv) == "1" {
		return destructiveDecision{SuppressedReason: reason}
	}
	return destructiveDecision{Reason: reason + " (set " + allowDestructiveEnv + "=1 to override)"}
}

// destructiveOverrideRule is the audit rule id for an override that let a
// hard-denied command through. It is deliberately not a permissions rule pattern:
// no configured rule produced this decision, the environment did.
const destructiveOverrideRule = "destructive_command_override"

// reportDestructiveOverride records that the environment override let a command
// through that the rule table had refused. Allowed is true because the command is
// about to run; the suppressed refusal travels in Reason so an auditor sees which
// protection was waived, and Source names the switch responsible.
func reportDestructiveOverride(toolContext tools.Context, command, suppressedReason string) {
	if toolContext.PermissionAudit == nil {
		return
	}
	toolContext.PermissionAudit(tools.PermissionAudit{
		ToolName: "Bash",
		Allowed:  true,
		Reason:   "hard refusal waived by environment override: " + suppressedReason,
		Rule:     destructiveOverrideRule,
		Request:  command,
		Source:   allowDestructiveEnv,
	})
}
