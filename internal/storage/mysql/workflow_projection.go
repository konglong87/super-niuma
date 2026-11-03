package mysql

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func persistWorkflow(tx *gorm.DB, prior workflow.Run, run *workflow.Run, events []workflow.Event) error {
	oldNodes := map[string]workflow.NodeRun{}
	for _, n := range prior.Nodes {
		oldNodes[n.NodeID] = n
	}
	for i := range run.Nodes {
		n := &run.Nodes[i]
		old := oldNodes[n.NodeID]
		for j := range n.Attempts {
			a := &n.Attempts[j]
			if j >= len(old.Attempts) {
				if err := prepareWorkflowTask(tx, run, n, a); err != nil {
					return err
				}
			} else if !workflowAttemptTerminal(old.Attempts[j].Status) && workflowAttemptTerminal(a.Status) {
				if err := finishWorkflowTask(tx, run, n, a); err != nil {
					return err
				}
			}
			if j < len(old.Attempts) && reflect.DeepEqual(old.Attempts[j], *a) {
				continue
			}
			state, err := json.Marshal(a)
			if err != nil {
				return err
			}
			row := gormWorkflowAttempt{TenantID: run.Scope.TenantID, RunID: run.ID, NodeID: n.NodeID, AttemptNo: a.Number, TaskID: a.TaskID, Epoch: a.Epoch, Status: string(a.Status), StateJSON: string(state)}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		// Lease-only saves and output deltas need no redundant projection writes.
		if reflect.DeepEqual(old, *n) {
			continue
		}
		state, err := json.Marshal(n)
		if err != nil {
			return err
		}
		row := gormWorkflowNode{TenantID: run.Scope.TenantID, RunID: run.ID, NodeID: n.NodeID, Status: string(n.Status), StateJSON: string(state)}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	oldArtifacts := map[string]bool{}
	for _, a := range prior.Artifacts {
		oldArtifacts[a.ID] = true
	}
	for _, a := range run.Artifacts {
		if oldArtifacts[a.ID] {
			continue
		}
		row := gormWorkflowArtifact{ID: a.ID, TenantID: run.Scope.TenantID, RunID: run.ID, NodeID: a.NodeID, AttemptNo: a.AttemptNo, Kind: a.Kind, Content: a.Content, SHA256: a.SHA256, CreatedAt: a.CreatedAt}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	for _, event := range events {
		run.LastSequence++
		event.Sequence = run.LastSequence
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now().UTC()
		}
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		// Large deltas remain in payload, not the V1 summary's VARCHAR(1024).
		summary := workflowSummary(event.Text, 1024)
		row := gormAgentTeamRunEvent{TenantID: run.Scope.TenantID, TeamRunID: run.ID, SequenceNo: event.Sequence, EventType: string(event.Type), MemberKey: event.NodeID, Status: string(event.Status), Summary: summary, PayloadJSON: nullableStringPtr(string(payload)), CreatedAt: event.CreatedAt}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	state, err := json.Marshal(run)
	if err != nil {
		return err
	}
	values := map[string]any{"workflow_state_json": string(state), "status": string(run.Status), "workflow_revision": run.Revision, "started_at": run.StartedAt, "finished_at": run.FinishedAt, "cancel_requested_at": run.CancelRequestedAt, "used_tokens": run.Usage.Total(), "updated_at": time.Now().UTC()}
	return workflowQuery(tx, run.Scope).Where("id = ?", run.ID).Updates(values).Error
}

func prepareWorkflowTask(tx *gorm.DB, run *workflow.Run, node *workflow.NodeRun, attempt *workflow.Attempt) error {
	if attempt.TaskID != 0 || attempt.Status != workflow.Running || attempt.Number <= 0 {
		return ErrInvalidInput
	}
	var definition workflow.Node
	for _, n := range run.Plan.Definition.Nodes {
		if n.ID == node.NodeID {
			definition = n
			break
		}
	}
	var agent workflow.Agent
	for _, a := range run.Plan.Agents {
		if a.Ref == definition.AgentRef {
			agent = a
			break
		}
	}
	metadata, err := json.Marshal(struct {
		RunID     string `json:"workflow_run_id"`
		NodeID    string `json:"workflow_node_id"`
		AttemptNo int    `json:"workflow_attempt_no"`
		Epoch     uint64 `json:"workflow_epoch"`
	}{run.ID, node.NodeID, attempt.Number, attempt.Epoch})
	if err != nil {
		return err
	}
	// No channel/session is synthesized for desktop runs. The engine task is
	// prepared, already running, and may only be driven by its workflow owner.
	task := newGormAgentTask(agenttasks.TaskInput{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, AgentName: agent.Name, Description: workflowSummary(definition.Instruction, 255), Prompt: run.Input, Model: agent.Model, Status: agenttasks.StatusRunning, MetadataJSON: string(metadata)})
	if err := tx.Create(&task).Error; err != nil {
		return err
	}
	attempt.TaskID = task.ID
	if attempt.StartedAt.IsZero() {
		attempt.StartedAt = time.Now().UTC()
	}
	if err := tx.Table(task.TableName()).Where("id = ?", task.ID).Update("started_at", attempt.StartedAt).Error; err != nil {
		return err
	}
	binding := gormWorkflowTaskBinding{TaskID: task.ID, TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, RunID: run.ID, NodeID: node.NodeID, AttemptNo: attempt.Number}
	if err := tx.Create(&binding).Error; err != nil {
		return err
	}
	return appendWorkflowTaskEvent(tx, run, attempt, agenttasks.EventStarted, metadata, attempt.StartedAt)
}

func finishWorkflowTask(tx *gorm.DB, run *workflow.Run, node *workflow.NodeRun, attempt *workflow.Attempt) error {
	var binding gormWorkflowTaskBinding
	if err := tx.Where("task_id = ? AND tenant_id = ? AND user_id = ? AND run_id = ? AND node_id = ? AND attempt_no = ?", attempt.TaskID, run.Scope.TenantID, run.Scope.UserID, run.ID, node.NodeID, attempt.Number).Take(&binding).Error; err != nil {
		return workflowReadError(err)
	}
	status, eventType, err := workflowTaskTerminal(attempt.Status)
	if err != nil {
		return err
	}
	if attempt.FinishedAt == nil {
		now := time.Now().UTC()
		attempt.FinishedAt = &now
	}
	result, err := json.Marshal(struct {
		Status  workflow.Status   `json:"status"`
		Output  string            `json:"output"`
		Usage   workflow.Usage    `json:"usage"`
		Failure *workflow.Failure `json:"failure,omitempty"`
	}{attempt.Status, node.Output, attempt.Usage, attempt.Failure})
	if err != nil {
		return err
	}
	updated := tx.Model(&gormAgentTask{}).Where("id = ? AND tenant_id = ? AND user_id = ? AND status = ?", attempt.TaskID, run.Scope.TenantID, run.Scope.UserID, agenttasks.StatusRunning).Updates(map[string]any{"status": status, "result_json": string(result), "finished_at": attempt.FinishedAt})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return workflow.ErrConflict
	}
	return appendWorkflowTaskEvent(tx, run, attempt, eventType, result, *attempt.FinishedAt)
}

func workflowTaskTerminal(status workflow.Status) (string, string, error) {
	switch status {
	case workflow.Completed:
		return agenttasks.StatusCompleted, agenttasks.EventCompleted, nil
	case workflow.Failed, workflow.Interrupted:
		return agenttasks.StatusFailed, agenttasks.EventFailed, nil
	case workflow.TimedOut:
		return agenttasks.StatusTimeout, agenttasks.EventTimeout, nil
	case workflow.Cancelled:
		return agenttasks.StatusCancelled, agenttasks.EventCancelled, nil
	default:
		return "", "", fmt.Errorf("unsupported workflow task terminal status %q", status)
	}
}
func appendWorkflowTaskEvent(tx *gorm.DB, run *workflow.Run, attempt *workflow.Attempt, eventType string, payload []byte, created time.Time) error {
	event := gormAgentTaskEvent{TenantID: run.Scope.TenantID, UserID: run.Scope.UserID, TaskID: attempt.TaskID, EventType: eventType, PayloadJSON: nullableStringPtr(string(payload))}
	if err := tx.Create(&event).Error; err != nil {
		return err
	}
	return tx.Table(event.TableName()).Where("id = ?", event.ID).Update("created_at", created).Error
}

func workflowSummary(text string, limit int) string {
	runes := []rune(text)
	if len(runes) > limit {
		runes = runes[:limit]
	}
	return string(runes)
}

func workflowAttemptTerminal(status workflow.Status) bool {
	return workflow.Terminal(status) || status == workflow.Interrupted
}
