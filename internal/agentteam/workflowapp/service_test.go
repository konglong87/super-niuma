package workflowapp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

func appTest(t *testing.T, executor workflow.Executor) (*Service, *mysqlstore.GormRepository, workflow.Scope) {
	t.Helper()
	repo, err := mysqlstore.OpenSQLiteGormRepository(context.Background(), filepath.Join(t.TempDir(), "workflow.sqlite"), nil)
	if err != nil {
		t.Fatal(err)
	}
	tenant, err := repo.UpsertTenant(context.Background(), mysqlstore.TenantInput{TenantKey: "workflow-test", Name: "Workflow"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.EnsureUser(context.Background(), tenant, "owner")
	if err != nil {
		t.Fatal(err)
	}
	store := mysqlstore.NewWorkflowStore(repo)
	s := New(repo, store, executor, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = s.Close(ctx)
		_ = repo.Close()
	})
	return s, repo, workflow.Scope{TenantID: tenant, UserID: user}
}
func awaitApp(t *testing.T, s *Service, scope workflow.Scope, id string) workflow.Run {
	t.Helper()
	until := time.Now().Add(4 * time.Second)
	for time.Now().Before(until) {
		r, err := s.Run(context.Background(), scope, id)
		if err == nil && workflow.Terminal(r.Status) {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run never finished")
	return workflow.Run{}
}
func TestAppBuiltinRunSnapshotsVersionsAndIdempotency(t *testing.T) {
	s, repo, scope := appTest(t, workflow.ExecutorFunc(func(_ context.Context, in workflow.Input, _ workflow.Sink) (workflow.Result, error) {
		return workflow.Result{Status: workflow.Completed, Output: "Result from " + in.NodeID, Usage: workflow.Usage{InputTokens: 10, OutputTokens: 20}}, nil
	}))
	ctx := context.Background()
	item, err := s.CreateBuiltin(ctx, scope, TemplateParallel)
	if err != nil {
		t.Fatal(err)
	}
	if len(item.Definition.Nodes) != 4 || len(item.Agents) != 4 {
		t.Fatal(item)
	}
	run, created, err := s.StartRun(ctx, scope, item.Key, StartRequest{Version: item.Version, Input: "Analyze the supplied notes"}, "stable-request-key")
	if err != nil || !created {
		t.Fatal(run, created, err)
	}
	done := awaitApp(t, s, scope, run.ID)
	if done.Status != workflow.Completed || len(done.Artifacts) != 4 || done.Usage.Total() != 120 {
		t.Fatalf("%+v", done)
	}
	for _, node := range done.Nodes {
		if len(node.Attempts) != 1 || node.Attempts[0].TaskID == 0 {
			t.Fatal("no task binding")
		}
		task, err := repo.GetAgentTask(ctx, scope.TenantID, scope.UserID, node.Attempts[0].TaskID)
		if err != nil || task.Status != "completed" {
			t.Fatal(task, err)
		}
	}
	replay, created, err := s.StartRun(ctx, scope, item.Key, StartRequest{Version: item.Version, Input: "Analyze the supplied notes"}, "stable-request-key")
	if err != nil || created || replay.ID != run.ID {
		t.Fatal(replay, created, err)
	}
	_, _, err = s.StartRun(ctx, scope, item.Key, StartRequest{Version: item.Version, Input: "different"}, "stable-request-key")
	if !errors.Is(err, workflow.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	newer, err := s.Save(ctx, scope, SaveRequest{Key: item.Key, Name: "Updated Team", Definition: item.Definition, Agents: item.Agents})
	if err != nil || newer.Version != 2 {
		t.Fatal(newer, err)
	}
	stored, _ := s.Run(ctx, scope, run.ID)
	if stored.TeamVersion != 1 || stored.Name == newer.Name || stored.Plan.Hash != done.Plan.Hash {
		t.Fatal("run snapshot mutated")
	}
	events, err := s.Events(ctx, scope, run.ID, 0, 3)
	if err != nil || len(events) != 3 {
		t.Fatal(events, err)
	}
	more, err := s.Events(ctx, scope, run.ID, events[2].Sequence, 3)
	if err != nil || len(more) == 0 || more[0].Sequence != 4 {
		t.Fatal(more, err)
	}
}
func TestAppRejectsInvalidGraphAndForeignProfiles(t *testing.T) {
	s, repo, scope := appTest(t, workflow.ExecutorFunc(func(context.Context, workflow.Input, workflow.Sink) (workflow.Result, error) {
		return workflow.Result{}, nil
	}))
	ctx := context.Background()
	item, err := s.CreateBuiltin(ctx, scope, TemplateSequential)
	if err != nil {
		t.Fatal(err)
	}
	def := item.Definition
	def.Nodes[0].DependsOn = []string{"review"}
	if _, err := s.Save(ctx, scope, SaveRequest{Name: "cycle", Definition: def, Agents: item.Agents}); err == nil {
		t.Fatal("accepted cycle")
	}
	other, err := repo.EnsureUser(ctx, scope.TenantID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, workflow.Scope{TenantID: scope.TenantID, UserID: other}, item.Key, item.Version); !errors.Is(err, workflow.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, workflow.Scope{TenantID: scope.TenantID, UserID: other}, SaveRequest{Name: "foreign", Definition: item.Definition, Agents: item.Agents}); err == nil {
		t.Fatal("foreign Profile accepted")
	}
}
func TestAppFailedAgentDoesNotSucceedAndBlocksDependencies(t *testing.T) {
	calls := 0
	s, _, scope := appTest(t, workflow.ExecutorFunc(func(context.Context, workflow.Input, workflow.Sink) (workflow.Result, error) {
		calls++
		return workflow.Result{Status: workflow.Failed, Failure: &workflow.Failure{Code: "rejected", Message: "not approved"}}, nil
	}))
	item, err := s.CreateBuiltin(context.Background(), scope, TemplateSequential)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.StartRun(context.Background(), scope, item.Key, StartRequest{Version: item.Version, Input: "Do this"}, "failure-request-key")
	if err != nil {
		t.Fatal(err)
	}
	done := awaitApp(t, s, scope, r.ID)
	if done.Status != workflow.Failed || len(done.Artifacts) != 0 || done.Nodes[1].Status != workflow.Blocked || calls != 1 {
		t.Fatal(done)
	}
}
func TestAppSanitizesInputBeforeCheckpoint(t *testing.T) {
	s, _, scope := appTest(t, workflow.ExecutorFunc(func(context.Context, workflow.Input, workflow.Sink) (workflow.Result, error) {
		return workflow.Result{Status: workflow.Completed, Output: "done"}, nil
	}))
	item, err := s.CreateBuiltin(context.Background(), scope, TemplateSequential)
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := s.StartRun(context.Background(), scope, item.Key, StartRequest{Version: item.Version, Input: "Use api_key=not-a-real-secret-1234 for this analysis"}, "redaction-request-key")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Input, "not-a-real-secret") {
		t.Fatal("raw credential persisted")
	}
}
