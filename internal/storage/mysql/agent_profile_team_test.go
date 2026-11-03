package mysql

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAgentProfileTeamMigrationsDeclareTenantScopedContracts(t *testing.T) {
	root := filepath.Join("..", "..", "..", "migrations", "mysql")
	profileUp := readMigrationFile(t, filepath.Join(root, "000015_agent_profiles.up.sql"))
	profileDown := readMigrationFile(t, filepath.Join(root, "000015_agent_profiles.down.sql"))
	teamUp := readMigrationFile(t, filepath.Join(root, "000016_agent_teams.up.sql"))
	teamDown := readMigrationFile(t, filepath.Join(root, "000016_agent_teams.down.sql"))
	eventsUp := readMigrationFile(t, filepath.Join(root, "000028_agent_team_run_events.up.sql"))
	eventsDown := readMigrationFile(t, filepath.Join(root, "000028_agent_team_run_events.down.sql"))

	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS agent_profiles",
		"CREATE TABLE IF NOT EXISTS agent_profile_assignments",
		"CREATE TABLE IF NOT EXISTS agent_profile_channel_bindings",
		"UNIQUE KEY uk_agent_profiles_version_owner",
		"UNIQUE KEY uk_agent_profile_assignments_surface",
		"FOREIGN KEY (tenant_id, profile_id)",
		"FOREIGN KEY (tenant_id, account_id)",
		"CHECK (status IN ('draft', 'validating', 'published', 'archived'))",
	} {
		if !strings.Contains(profileUp, statement) {
			t.Errorf("profile migration missing %q", statement)
		}
	}
	for _, statement := range []string{
		"DROP TABLE IF EXISTS agent_profile_channel_bindings",
		"DROP TABLE IF EXISTS agent_profile_assignments",
		"DROP TABLE IF EXISTS agent_profiles",
	} {
		if !strings.Contains(profileDown, statement) {
			t.Errorf("profile down migration missing %q", statement)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS agent_teams",
		"CREATE TABLE IF NOT EXISTS agent_team_members",
		"CREATE TABLE IF NOT EXISTS agent_team_bindings",
		"CREATE TABLE IF NOT EXISTS agent_team_runs",
		"CREATE TABLE IF NOT EXISTS agent_team_mailbox",
		"UNIQUE KEY uk_agent_teams_version",
		"UNIQUE KEY uk_agent_team_runs_tenant_id",
		"UNIQUE KEY uk_agent_team_runs_inbox",
		"UNIQUE KEY uk_agent_team_mailbox_idempotency",
		"FOREIGN KEY (tenant_id, team_id)",
		"FOREIGN KEY (tenant_id, inbox_event_id)",
	} {
		if !strings.Contains(teamUp, statement) {
			t.Errorf("team migration missing %q", statement)
		}
	}
	for _, statement := range []string{
		"DROP TABLE IF EXISTS agent_team_mailbox",
		"DROP TABLE IF EXISTS agent_team_runs",
		"DROP TABLE IF EXISTS agent_team_bindings",
		"DROP TABLE IF EXISTS agent_team_members",
		"DROP TABLE IF EXISTS agent_teams",
	} {
		if !strings.Contains(teamDown, statement) {
			t.Errorf("team down migration missing %q", statement)
		}
	}
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS agent_team_run_events",
		"idx_agent_team_run_events_timeline",
		"FOREIGN KEY (tenant_id, team_run_id)",
	} {
		if !strings.Contains(eventsUp, statement) {
			t.Errorf("event migration missing %q", statement)
		}
	}
	if !strings.Contains(eventsDown, "DROP TABLE IF EXISTS agent_team_run_events") {
		t.Errorf("event down migration missing drop statement")
	}
}

func TestAgentProfileRepositoryRejectsInvalidInput(t *testing.T) {
	repo := NewGormRepository(nil, nil)
	if _, err := repo.CreateAgentProfile(context.Background(), AgentProfileInput{ProfileKey: "missing-tenant"}); err != ErrInvalidInput {
		t.Fatalf("CreateAgentProfile error = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.GetAgentProfile(context.Background(), 0, 1); err != ErrInvalidInput {
		t.Fatalf("GetAgentProfile error = %v, want ErrInvalidInput", err)
	}
	if err := repo.PublishAgentProfile(context.Background(), 1, 2, 0, "", ""); err != ErrInvalidInput {
		t.Fatalf("PublishAgentProfile error = %v, want ErrInvalidInput", err)
	}
}

func TestAgentTeamRepositoryRejectsInvalidInput(t *testing.T) {
	repo := NewGormRepository(nil, nil)
	if _, err := repo.CreateAgentTeam(context.Background(), AgentTeamInput{TeamKey: "missing-tenant"}); err != ErrInvalidInput {
		t.Fatalf("CreateAgentTeam error = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.CreateAgentTeamRun(context.Background(), AgentTeamRunInput{TeamID: 1}); err != ErrInvalidInput {
		t.Fatalf("CreateAgentTeamRun error = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.AppendAgentTeamMailbox(context.Background(), AgentTeamMailboxInput{TeamRunID: "run"}); err != ErrInvalidInput {
		t.Fatalf("AppendAgentTeamMailbox error = %v, want ErrInvalidInput", err)
	}
	if _, err := repo.AppendAgentTeamRunEvent(context.Background(), AgentTeamRunEventInput{TeamRunID: "run"}); err != ErrInvalidInput {
		t.Fatalf("AppendAgentTeamRunEvent error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateAgentTeamRunPersistsFinishedAt(t *testing.T) {
	repo, mock, closeDB := newMockGormRepository(t)
	defer closeDB()
	finishedAt := time.Now().UTC()
	mock.ExpectExec("UPDATE `agent_team_runs` SET .*finished_at.*").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.UpdateAgentTeamRun(context.Background(), AgentTeamRunUpdate{
		TenantID:     1,
		RunID:        "team-run-1",
		Status:       "failed",
		FinishedAt:   &finishedAt,
		ErrorMessage: "provider request failed",
	}); err != nil {
		t.Fatalf("UpdateAgentTeamRun error = %v", err)
	}
	assertExpectations(t, mock)
}

func readMigrationFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}
	return string(data)
}
