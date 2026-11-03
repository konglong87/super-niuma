package agentteam

import (
	"context"
	"errors"
	"testing"
)

func TestTeamPolicyUnmarshalNestedDocument(t *testing.T) {
	var policy TeamPolicy
	if err := policy.UnmarshalJSON([]byte(`{"orchestration":{"mode":"parallel_review","coordinator_member":"editor","max_rounds":4,"max_parallel_members":3,"max_total_tokens":24000},"trigger":{"require_mention":true,"commands":["/team"],"allow_direct_message":true},"output":{"phase_updates":"coordinator_only","post_member_cards":false}}`)); err != nil {
		t.Fatalf("unmarshal nested policy: %v", err)
	}
	if policy.Mode != ModeParallelReview || policy.CoordinatorMember != "editor" || policy.MaxRounds != 4 || policy.MaxParallelMembers != 3 || policy.MaxTotalTokens != 24000 {
		t.Fatalf("policy = %+v", policy)
	}
	if !policy.RequireMention || len(policy.Commands) != 1 || policy.Commands[0] != "/team" || policy.PhaseUpdates != "coordinator_only" || policy.PostMemberCards {
		t.Fatalf("nested trigger/output not mapped: %+v", policy)
	}
}

func TestValidateTeamRejectsInvalidMemberGraphAndBindings(t *testing.T) {
	team := Team{TenantID: 7, Key: "launch", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 2, MaxTotalTokens: 1000}, Members: []Member{
		{TenantID: 7, Key: "editor", ProfileKey: "copywriter", ProfileVersion: 1, Role: RoleCoordinator, AccountKey: "bot-a"},
		{TenantID: 7, Key: "editor", ProfileKey: "researcher", ProfileVersion: 1, Role: RoleResearcher, AccountKey: "bot-b"},
		{TenantID: 8, Key: "reviewer", ProfileKey: "coder", ProfileVersion: 1, Role: RoleReviewer, AccountKey: "bot-c"},
	}, Bindings: []Binding{{TenantID: 7, AccountKey: "bot-a", ExternalChatID: "oc-launch", Trigger: TriggerMention}, {TenantID: 7, AccountKey: "bot-a", ExternalChatID: "oc-launch", Trigger: TriggerMention}}}

	report := Validate(team, ValidationContext{PublishedProfiles: map[ProfileRef]bool{{Key: "copywriter", Version: 1}: true}})
	if report.Valid {
		t.Fatal("invalid team unexpectedly validated")
	}
	for _, code := range []string{IssueDuplicateMember, IssueTenantMismatch, IssueMissingProfile, IssueDuplicateBinding} {
		if !report.Has(code) {
			t.Fatalf("missing issue %q: %+v", code, report.Issues)
		}
	}
}

func TestValidateTeamRejectsMissingActorAuthorizationAndBotAccount(t *testing.T) {
	team := Team{TenantID: 7, Key: "launch", Version: 1, Status: StatusPublished, Policy: TeamPolicy{Mode: ModeCoordinator, CoordinatorMember: "editor", MaxRounds: 2, MaxParallelMembers: 1, MaxTotalTokens: 1000, PhaseUpdates: "coordinator_only", PostMemberCards: true}, Members: []Member{{TenantID: 7, Key: "editor", ProfileKey: "copywriter", ProfileVersion: 1, Role: RoleCoordinator, AccountKey: "0"}}}
	report := Validate(team, ValidationContext{PublishedProfiles: map[ProfileRef]bool{{Key: "copywriter", Version: 1}: true}})
	for _, code := range []string{IssueMissingAuthorization, IssueMissingBotAccount, IssueOutputConflict} {
		if !report.Has(code) {
			t.Fatalf("missing issue %q: %+v", code, report.Issues)
		}
	}
}

func TestTeamRouterAppliesTriggerAndBotLoopGuards(t *testing.T) {
	router := NewRouter([]Binding{
		{TenantID: 7, TeamID: 9, TeamVersion: 2, Provider: "feishu", AccountKey: "bot-a", ExternalChatID: "oc-launch", Trigger: TriggerMention},
		{TenantID: 7, TeamID: 9, TeamVersion: 2, Provider: "feishu", AccountKey: "bot-b", ExternalChatID: "oc-launch", Trigger: TriggerInternalOnly},
	}, map[string]struct{}{"bot-a": {}, "bot-b": {}})

	route, ok := router.Route(InboundEvent{Provider: "feishu", AccountKey: "bot-a", ExternalConversationID: "oc-launch", ChatType: "group", ExternalUserID: "ou-user", MentionedBot: true, Text: "请开始"})
	if !ok || route.TeamID != 9 || route.TeamVersion != 2 {
		t.Fatalf("mention route = %+v, ok=%v", route, ok)
	}
	if _, ok := router.Route(InboundEvent{Provider: "feishu", AccountKey: "bot-a", ExternalConversationID: "oc-launch", ChatType: "group", ExternalUserID: "bot-b", MentionedBot: true, Text: "bot reply"}); ok {
		t.Fatal("bot-originated message passed loop guard")
	}
	if _, ok := router.Route(InboundEvent{Provider: "feishu", AccountKey: "bot-b", ExternalConversationID: "oc-launch", ChatType: "group", ExternalUserID: "ou-user", MentionedBot: true, Text: "请开始"}); ok {
		t.Fatal("internal-only binding accepted external message")
	}
}

func TestInMemoryMailboxIsIdempotentAndOrdered(t *testing.T) {
	mailbox := NewMemoryMailbox()
	first := Message{TenantID: 7, RunID: "run-1", FromMember: "editor", ToMember: "researcher", Sequence: 1, IdempotencyKey: "m-1", Content: "research"}
	if err := mailbox.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Append(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	items, err := mailbox.List(context.Background(), 7, "run-1")
	if err != nil || len(items) != 1 || items[0].Sequence != 1 {
		t.Fatalf("mailbox = %+v err=%v", items, err)
	}
	if err := mailbox.Consume(context.Background(), 7, "run-1", "m-1"); err != nil {
		t.Fatal(err)
	}
	if err := mailbox.Consume(context.Background(), 7, "run-1", "missing"); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("missing consume error = %v", err)
	}
}
