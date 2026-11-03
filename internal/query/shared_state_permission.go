package query

import (
	"context"
	"strings"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/gitpolicy"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/konglong87/super-niuma/internal/tools"
)

const sharedStatePermissionSource = "shared-state-gate"

func (s *Session) approveSharedStateInDangerousMode(ctx context.Context, block anthropic.ContentBlock, gate completionGateResult, analysis gitpolicy.Analysis) (gitpolicy.Authorization, bool) {
	if !s.canPromptForSharedStateInDangerousMode(gate, analysis) {
		return gitpolicy.Authorization{}, false
	}
	response := s.runPermissionPrompt(ctx, tools.PermissionPromptRequest{
		ToolName: block.Name,
		Input:    block.Input,
		Reason:   "Shared-State Authorization Gate: approve this exact non-destructive Git operation once",
		Request:  bashCommandFromInput(string(block.Input)),
		Rule:     gate.RuleID,
		Source:   sharedStatePermissionSource,
		OneShot:  true,
	})
	s.recordPermission(ctx, tools.PermissionAudit{
		ToolName: block.Name,
		Allowed:  response.Allowed,
		Reason:   firstNonEmpty(response.Reason, "shared-state permission prompt decision"),
		Rule:     gate.RuleID,
		Request:  bashCommandFromInput(string(block.Input)),
		Source:   sharedStatePermissionSource,
	})
	if !response.Allowed {
		return gitpolicy.Authorization{}, false
	}
	return gitpolicy.AuthorizationFromEffects(analysis.Effects), true
}

func (s *Session) canPromptForSharedStateInDangerousMode(gate completionGateResult, analysis gitpolicy.Analysis) bool {
	if gate.RuleID != string(gitpolicy.ViolationSharedState) || len(analysis.Effects) == 0 {
		return false
	}
	if s.options.PermissionPrompt == nil && strings.TrimSpace(s.options.PermissionPromptTool) == "" {
		return false
	}
	if !dangerousPermissionMode(s.options.RuntimePermissionMode) {
		return false
	}
	for _, effect := range analysis.Effects {
		if effect.Destructive || effect.Operation == gitpolicy.OperationForceAdd {
			return false
		}
	}
	return true
}

func dangerousPermissionMode(current func() string) bool {
	if current == nil {
		return false
	}
	mode := strings.TrimSpace(current())
	if normalized, ok := permissions.NormalizeMode(mode); ok {
		mode = normalized
	}
	return mode == permissions.ModeAllow || mode == permissions.ModeBypassPermissions
}
