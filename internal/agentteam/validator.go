package agentteam

import (
	"fmt"
	"strings"
)

const (
	IssueDuplicateMember      = "duplicate_member"
	IssueTenantMismatch       = "tenant_mismatch"
	IssueMissingProfile       = "profile_not_published"
	IssueDuplicateBinding     = "duplicate_binding"
	IssueMissingCoordinator   = "missing_coordinator"
	IssueMultipleCoordinators = "multiple_coordinators"
	IssueInvalidPolicy        = "invalid_policy"
	IssueBindingMismatch      = "binding_mismatch"
	IssueMissingBotAccount    = "missing_bot_account"
	IssueMissingAuthorization = "missing_actor_authorization"
	IssueOutputConflict       = "output_policy_conflict"
)

func Validate(team Team, context ValidationContext) ValidationReport {
	report := ValidationReport{Valid: true}
	add := func(code, field, message string) {
		report.Valid = false
		report.Issues = append(report.Issues, ValidationIssue{Code: code, Field: field, Message: message})
	}
	if team.TenantID == 0 || strings.TrimSpace(team.Key) == "" || team.Version == 0 || team.Status != StatusPublished {
		add(IssueInvalidPolicy, "team", "published team requires tenant, key, version and published status")
	}
	if team.Policy.Mode != ModeCoordinator && team.Policy.Mode != ModeParallelReview {
		add(IssueInvalidPolicy, "policy.mode", "mode must be coordinator or parallel_review")
	}
	if team.Policy.MaxRounds == 0 || team.Policy.MaxParallelMembers == 0 || team.Policy.MaxTotalTokens == 0 {
		add(IssueInvalidPolicy, "policy.budget", "rounds, parallel members and total tokens must be positive")
	}
	if !team.Policy.RequireTenantMember {
		add(IssueMissingAuthorization, "policy.authorization", "team requires tenant membership or explicit external actor authorization")
	}
	if team.Policy.PhaseUpdates == "coordinator_only" && team.Policy.PostMemberCards {
		add(IssueOutputConflict, "policy.output", "post_member_cards conflicts with coordinator-only phase updates")
	}
	seenMembers := make(map[string]struct{}, len(team.Members))
	coordinators := 0
	for _, member := range team.Members {
		if member.TenantID != 0 && member.TenantID != team.TenantID {
			add(IssueTenantMismatch, "members."+member.Key, "member tenant does not match team tenant")
		}
		if _, exists := seenMembers[member.Key]; exists || strings.TrimSpace(member.Key) == "" {
			add(IssueDuplicateMember, "members."+member.Key, "member key must be unique")
		}
		seenMembers[member.Key] = struct{}{}
		if member.Role == RoleCoordinator {
			coordinators++
		}
		if strings.TrimSpace(member.AccountKey) == "" || strings.TrimSpace(member.AccountKey) == "0" {
			add(IssueMissingBotAccount, "members."+member.Key, "Feishu team members require a bound bot account")
		}
		if !context.PublishedProfiles[ProfileRef{Key: member.ProfileKey, Version: member.ProfileVersion}] {
			add(IssueMissingProfile, "members."+member.Key, fmt.Sprintf("profile %s@%d is not published", member.ProfileKey, member.ProfileVersion))
		}
	}
	if coordinators == 0 || team.Policy.CoordinatorMember == "" {
		add(IssueMissingCoordinator, "policy.coordinator_member", "team needs one coordinator member")
	}
	if coordinators > 1 {
		add(IssueMultipleCoordinators, "members", "team can have only one coordinator")
	}
	seenBindings := make(map[string]struct{}, len(team.Bindings))
	for _, binding := range team.Bindings {
		if binding.TenantID != 0 && binding.TenantID != team.TenantID {
			add(IssueTenantMismatch, "bindings."+binding.ExternalChatID, "binding tenant does not match team tenant")
		}
		if binding.TeamID != 0 && binding.TeamID != team.ID {
			add(IssueBindingMismatch, "bindings."+binding.ExternalChatID, "binding team does not match team id")
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", binding.Provider, binding.AccountKey, binding.ExternalChatID, binding.ExternalThreadID)
		if _, exists := seenBindings[key]; exists {
			add(IssueDuplicateBinding, "bindings."+binding.ExternalChatID, "binding must be unique")
		}
		seenBindings[key] = struct{}{}
	}
	return report
}
