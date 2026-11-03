package mysql

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
)

func workflowTestPublishedProfile(t *testing.T, repo *GormRepository, scope workflow.Scope, key string) AgentProfile {
	t.Helper()
	ctx := context.Background()
	profile, err := repo.CreateAgentProfile(ctx, AgentProfileInput{TenantID: scope.TenantID, OwnerUserID: scope.UserID, OwnerKey: fmt.Sprintf("user:%d", scope.UserID), ProfileKey: key, ProfileVersion: 1, Scope: workflowPrivateScope, DisplayName: key, Status: "draft", ConfigJSON: `{}`, RequestedHash: strings.Repeat("a", 64), EffectiveHash: strings.Repeat("b", 64), CreatedByUserID: scope.UserID, UpdatedByUserID: scope.UserID})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(-time.Second)
	if err := repo.PublishAgentProfile(ctx, scope.TenantID, profile.ID, scope.UserID, `{"valid":true}`, strings.Repeat("c", 64)); err != nil {
		t.Fatalf("SQLite publish Profile: %v", err)
	}
	profile, err = repo.GetAgentProfile(ctx, scope.TenantID, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Status != workflowPublishedStatus || profile.PublishedAt.Before(before) || profile.PublishedAt.After(time.Now().UTC()) || profile.ValidationJSON != `{"valid":true}` {
		t.Fatalf("published profile=%+v", profile)
	}
	return profile
}
func workflowTestTeamInput(scope workflow.Scope) AgentTeamInput {
	return AgentTeamInput{TenantID: scope.TenantID, OwnerUserID: scope.UserID, OwnerKey: fmt.Sprintf("user:%d", scope.UserID), TeamKey: "compiled-workflow", TeamVersion: 1, DisplayName: "Workflow", SchemaVersion: workflow.SchemaVersion, PolicyJSON: `{"schema_version":2,"nodes":[]}`, EffectiveHash: strings.Repeat("d", 64)}
}

func TestWorkflowSQLitePublishProfileAndLegacyTeam(t *testing.T) {
	repo, _, run := workflowTestRepo(t)
	ctx := context.Background()
	profile := workflowTestPublishedProfile(t, repo, run.Scope, "publish-test")
	if err := repo.PublishAgentProfile(ctx, run.Scope.TenantID, profile.ID, run.Scope.UserID, `{}`, strings.Repeat("d", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("republish should keep published version immutable: %v", err)
	}
	before := time.Now().UTC().Add(-time.Second)
	if err := repo.PublishAgentTeam(ctx, run.Scope.TenantID, run.TeamID, run.Scope.UserID, `{"valid":true}`, strings.Repeat("e", 64)); err != nil {
		t.Fatalf("SQLite publish Team: %v", err)
	}
	team, err := repo.GetAgentTeam(ctx, run.Scope.TenantID, run.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if team.Status != workflowPublishedStatus || team.PublishedAt.Before(before) || team.PublishedAt.After(time.Now().UTC()) {
		t.Fatalf("published Team=%+v", team)
	}
}

func TestWorkflowCreateTeamAtomicPublication(t *testing.T) {
	repo, _, run := workflowTestRepo(t)
	ctx := context.Background()
	profile := workflowTestPublishedProfile(t, repo, run.Scope, "team-agent")
	input := workflowTestTeamInput(run.Scope)
	members := []AgentTeamMemberInput{{MemberKey: "agent", ProfileID: profile.ID, Role: "worker"}}
	team, err := repo.CreateWorkflowTeam(ctx, input, members)
	if err != nil {
		t.Fatal(err)
	}
	if team.Status != workflowPublishedStatus || team.SchemaVersion != workflow.SchemaVersion || team.Scope != workflowPrivateScope || team.OwnerUserID != run.Scope.UserID || team.TeamVersion != input.TeamVersion || team.PublishedAt.IsZero() {
		t.Fatalf("compiled Team=%+v", team)
	}
	listed, err := repo.ListAgentTeamMembers(ctx, run.Scope.TenantID, team.ID, 20)
	if err != nil || len(listed) != 1 || listed[0].ProfileID != profile.ID || listed[0].AccountID != 0 {
		t.Fatalf("members=%+v %v", listed, err)
	}
	if members[0].TenantID != 0 || members[0].TeamID != 0 {
		t.Fatal("CreateWorkflowTeam mutated caller members")
	}
	if _, err := repo.CreateWorkflowTeam(ctx, input, members); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate fixed version: %v", err)
	}
	input.TeamVersion++
	next, err := repo.CreateWorkflowTeam(ctx, input, members)
	if err != nil || next.ID == team.ID || next.TeamVersion != 2 {
		t.Fatalf("new version=%+v %v", next, err)
	}
	if n := workflowCount(t, repo, "agent_team_members"); n != 2 {
		t.Fatalf("members count=%d", n)
	}
}

func TestWorkflowCreateTeamRollbackAndProfileScope(t *testing.T) {
	repo, _, run := workflowTestRepo(t)
	ctx := context.Background()
	profile := workflowTestPublishedProfile(t, repo, run.Scope, "team-agent")
	foreignScope, _ := workflowTestOwner(t, repo, "foreign-team")
	foreign := workflowTestPublishedProfile(t, repo, foreignScope, "foreign-agent")
	privateUser, err := repo.EnsureUser(ctx, run.Scope.TenantID, "private-owner")
	if err != nil {
		t.Fatal(err)
	}
	private := workflowTestPublishedProfile(t, repo, workflow.Scope{TenantID: run.Scope.TenantID, UserID: privateUser}, "private-agent")
	before := workflowCount(t, repo, "agent_teams")
	for _, badID := range []uint64{foreign.ID, private.ID, 999999} {
		input := workflowTestTeamInput(run.Scope)
		// The first member write succeeds; the second must roll back both the
		// published team row and the already-created first member.
		_, err := repo.CreateWorkflowTeam(ctx, input, []AgentTeamMemberInput{{MemberKey: "one", ProfileID: profile.ID, Role: "worker"}, {MemberKey: "two", ProfileID: badID, Role: "worker"}})
		if !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("foreign/missing profile error %v", err)
		}
		if n := workflowCount(t, repo, "agent_teams"); n != before {
			t.Fatal("failed membership left a published team")
		}
		if n := workflowCount(t, repo, "agent_team_members"); n != 0 {
			t.Fatal("failed membership left partial members")
		}
	}
	if err := repo.db.Exec(`CREATE TRIGGER reject_team_member BEFORE INSERT ON agent_team_members BEGIN SELECT RAISE(ABORT, 'injected member failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateWorkflowTeam(ctx, workflowTestTeamInput(run.Scope), []AgentTeamMemberInput{{MemberKey: "agent", ProfileID: profile.ID, Role: "worker"}})
	if err == nil {
		t.Fatal("expected injected member write failure")
	}
	if n := workflowCount(t, repo, "agent_teams"); n != before {
		t.Fatal("injected failure left published empty team")
	}
}

func TestWorkflowCreateTeamVersionRace(t *testing.T) {
	repo, _, run := workflowTestRepo(t)
	ctx := context.Background()
	profile := workflowTestPublishedProfile(t, repo, run.Scope, "race-agent")
	input := workflowTestTeamInput(run.Scope)
	members := []AgentTeamMemberInput{{MemberKey: "agent", ProfileID: profile.ID, Role: "worker"}}
	const writers = 4
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := repo.CreateWorkflowTeam(ctx, input, members); results <- err }()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("version race unexpected error=%v", err)
		}
	}
	if winners != 1 || workflowCount(t, repo, "agent_team_members") != 1 {
		t.Fatalf("version race winners=%d", winners)
	}
}
