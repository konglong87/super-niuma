package mysql

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
)

func workflowPersistedState(t *testing.T, repo *GormRepository) map[string][]map[string]any {
	t.Helper()
	state := map[string][]map[string]any{}
	models := []interface{ TableName() string }{gormWorkflowRun{}, gormWorkflowNode{}, gormWorkflowAttempt{}, gormWorkflowArtifact{}, gormWorkflowTaskBinding{}, gormAgentTeamRunEvent{}, gormAgentTask{}, gormAgentTaskEvent{}}
	for _, model := range models {
		var rows []map[string]any
		if err := repo.db.Table(model.TableName()).Order("rowid").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		state[model.TableName()] = rows
	}
	return state
}

func assertWorkflowSnapshotRejected(t *testing.T, repo *GormRepository, prior, candidate workflow.Run) {
	t.Helper()
	ctx := context.Background()
	before := workflowPersistedState(t, repo)
	_, err := NewWorkflowStore(repo).Save(ctx, prior.Revision, candidate, []workflow.Event{{Type: workflow.EventCompleted, Text: "must not be committed"}})
	if !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("invalid snapshot error=%v want ErrConflict", err)
	}
	if after := workflowPersistedState(t, repo); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected snapshot changed checkpoint/projections/tasks/events")
	}
	got, err := NewWorkflowStore(repo).Get(ctx, prior.Scope, prior.ID)
	if err != nil || !reflect.DeepEqual(got, prior) {
		t.Fatalf("rejected snapshot changed Run: %v", err)
	}
}

func TestWorkflowSaveRejectsTerminalRunStatusChanges(t *testing.T) {
	ctx := context.Background()
	terminal := []workflow.Status{workflow.Completed, workflow.Failed, workflow.Cancelled, workflow.TimedOut}
	next := []workflow.Status{workflow.Queued, workflow.Running, workflow.CancelRequested, workflow.Interrupted, workflow.Completed, workflow.Failed, workflow.Cancelled, workflow.TimedOut}
	for _, status := range terminal {
		t.Run(string(status), func(t *testing.T) {
			repo, _, input := workflowTestRepo(t)
			store := NewWorkflowStore(repo)
			run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
			run.Status = status
			run.Nodes[0].Status = status
			run.Nodes[0].Attempts[0].Status = status
			now := time.Now().UTC()
			run.FinishedAt = &now
			prior, err := store.Save(ctx, run.Revision, run, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, status := range next {
				if status == prior.Status {
					continue
				}
				t.Run("to_"+string(status), func(t *testing.T) {
					candidate, err := cloneWorkflow(prior)
					if err != nil {
						t.Fatal(err)
					}
					candidate.Status = status
					assertWorkflowSnapshotRejected(t, repo, prior, candidate)
				})
			}
			// Existing Cancel/no-op callers may still save the same terminal snapshot.
			saved, err := store.Save(ctx, prior.Revision, prior, nil)
			if err != nil || saved.Status != prior.Status || saved.Revision != prior.Revision+1 {
				t.Fatalf("terminal no-op=%+v %v", saved, err)
			}
		})
	}
}

func TestWorkflowSaveRejectsEpochRegression(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	input.Epoch = 7
	prior := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	for _, epoch := range []uint64{0, prior.Epoch - 1} {
		candidate, err := cloneWorkflow(prior)
		if err != nil {
			t.Fatal(err)
		}
		candidate.Epoch = epoch
		assertWorkflowSnapshotRejected(t, repo, prior, candidate)
	}
	same, err := store.Save(ctx, prior.Revision, prior, nil)
	if err != nil || same.Epoch != prior.Epoch {
		t.Fatalf("same epoch rejected %v", err)
	}
	// A real interruption/recovery advances the epoch and remains legal.
	same.Status = workflow.Interrupted
	same.Nodes[0].Status = workflow.Interrupted
	same.Nodes[0].Attempts[0].Status = workflow.Interrupted
	interrupted, err := store.Save(ctx, same.Revision, same, nil)
	if err != nil {
		t.Fatal(err)
	}
	interrupted.Status = workflow.Queued
	interrupted.Epoch++
	interrupted.Nodes[0].Status = workflow.Pending
	recovered, err := store.Save(ctx, interrupted.Revision, interrupted, nil)
	if err != nil || recovered.Epoch != prior.Epoch+1 || recovered.Status != workflow.Queued {
		t.Fatalf("legitimate recovery rejected: %+v %v", recovered, err)
	}
}

func TestWorkflowSaveRejectsCompletedNodeMutation(t *testing.T) {
	repo, _, input := workflowTestRepo(t)
	ctx := context.Background()
	store := NewWorkflowStore(repo)
	input.Plan.Definition.Nodes = append(input.Plan.Definition.Nodes, workflow.Node{ID: "n2", AgentRef: "agent1", DependsOn: []string{"n1"}})
	run := workflowTestStart(t, repo, workflowTestCreate(t, repo, input))
	now := time.Now().UTC()
	run.Nodes[0].Status = workflow.Completed
	run.Nodes[0].Attempts[0].Status = workflow.Completed
	run.Nodes[0].Output = "sealed output"
	run.Nodes[0].ArtifactID = uuid.NewString()
	run.Artifacts = []workflow.Artifact{{ID: run.Nodes[0].ArtifactID, NodeID: "n1", AttemptNo: 1, Kind: "text", Content: run.Nodes[0].Output, SHA256: strings.Repeat("a", 64), CreatedAt: now}}
	prior, err := store.Save(ctx, run.Revision, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name   string
		mutate func(*workflow.Run)
	}{
		{"status_running", func(r *workflow.Run) { r.Nodes[0].Status = workflow.Running }},
		{"status_cancelled", func(r *workflow.Run) { r.Nodes[0].Status = workflow.Cancelled }},
		{"status_interrupted", func(r *workflow.Run) { r.Nodes[0].Status = workflow.Interrupted }},
		{"output", func(r *workflow.Run) { r.Nodes[0].Output = "rewritten" }},
		{"artifact_id", func(r *workflow.Run) { r.Nodes[0].ArtifactID = uuid.NewString() }},
		{"artifact_cleared", func(r *workflow.Run) { r.Nodes[0].ArtifactID = "" }},
		{"artifact_content", func(r *workflow.Run) { r.Artifacts[0].Content = "rewritten" }},
		{"new_artifact", func(r *workflow.Run) {
			extra := r.Artifacts[0]
			extra.ID = uuid.NewString()
			r.Artifacts = append(r.Artifacts, extra)
		}},
		{"new_attempt", func(r *workflow.Run) {
			r.Nodes[0].Attempts = append(r.Nodes[0].Attempts, workflow.Attempt{Number: 2, Epoch: r.Epoch, Status: workflow.Running})
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			candidate, err := cloneWorkflow(prior)
			if err != nil {
				t.Fatal(err)
			}
			check.mutate(&candidate)
			assertWorkflowSnapshotRejected(t, repo, prior, candidate)
		})
	}
	// Cancel/recovery may change the remaining node but cannot rewrite completed
	// dependencies. Preserve the sealed result throughout both legal operations.
	prior.Status = workflow.CancelRequested
	cancelled, err := store.Save(ctx, prior.Revision, prior, nil)
	if err != nil {
		t.Fatalf("cancel request rejected: %v", err)
	}
	cancelled.Status = workflow.Interrupted
	cancelled.Nodes[1].Status = workflow.Interrupted
	interrupted, err := store.Save(ctx, cancelled.Revision, cancelled, nil)
	if err != nil {
		t.Fatalf("interruption rejected: %v", err)
	}
	interrupted.Status = workflow.Queued
	interrupted.Epoch++
	interrupted.Nodes[1].Status = workflow.Pending
	recovered, err := store.Save(ctx, interrupted.Revision, interrupted, nil)
	if err != nil || !reflect.DeepEqual(recovered.Nodes[0], prior.Nodes[0]) {
		t.Fatalf("recovery changed completed node or was rejected: %v", err)
	}
}
