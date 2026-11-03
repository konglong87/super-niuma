package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/konglong87/super-niuma/internal/agentteam/workflow"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type workflowStore struct{ repo *GormRepository }

var _ workflow.Store = (*workflowStore)(nil)

// Schema migrations must finish before wiring a workflow Store. Enabling the
// guards only here keeps repositories without V2 tables (including old SQLmock
// fixtures) on their unchanged legacy SQL path.
func NewWorkflowStore(repo *GormRepository) workflow.Store {
	if repo != nil {
		repo.workflowTaskGuards.Store(true)
	}
	return &workflowStore{repo: repo}
}

func workflowQuery(db *gorm.DB, scope workflow.Scope) *gorm.DB {
	return db.Model(&gormWorkflowRun{}).Where("tenant_id = ? AND owner_user_id = ? AND workflow_version = ?", scope.TenantID, scope.UserID, workflow.SchemaVersion)
}

func decodeWorkflow(row gormWorkflowRun) (workflow.Run, error) {
	var run workflow.Run
	if row.WorkflowStateJSON == nil {
		return run, fmt.Errorf("workflow checkpoint missing: %s", row.Legacy.ID)
	}
	if err := json.Unmarshal([]byte(*row.WorkflowStateJSON), &run); err != nil {
		return run, fmt.Errorf("workflow checkpoint %s: %w", row.Legacy.ID, err)
	}
	if run.ID != row.Legacy.ID || run.Scope.TenantID != row.Legacy.TenantID || run.TeamID != row.Legacy.TeamID || run.Status != workflow.Status(row.Legacy.Status) || row.OwnerUserID == nil || run.Scope.UserID != *row.OwnerUserID || run.Revision != row.WorkflowRevision {
		return workflow.Run{}, fmt.Errorf("workflow checkpoint identity mismatch: %s", row.Legacy.ID)
	}
	run.KeyHash = valueStringPtr(row.IdempotencyKeyHash)
	run.Fingerprint = valueStringPtr(row.Fingerprint)
	return run, nil
}

func (s *workflowStore) Get(ctx context.Context, scope workflow.Scope, id string) (workflow.Run, error) {
	var row gormWorkflowRun
	if err := workflowQuery(s.repo.db.WithContext(ctx), scope).Where("id = ?", id).Take(&row).Error; err != nil {
		return workflow.Run{}, workflowReadError(err)
	}
	return decodeWorkflow(row)
}
func workflowReadError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return workflow.ErrNotFound
	}
	return err
}

func (s *workflowStore) List(ctx context.Context, scope workflow.Scope, limit int) ([]workflow.Run, error) {
	if limit <= 0 || limit > workflow.EventPageLimit {
		limit = workflow.EventPageLimit
	}
	var rows []gormWorkflowRun
	if err := workflowQuery(s.repo.db.WithContext(ctx), scope).Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	return decodeWorkflows(rows)
}
func decodeWorkflows(rows []gormWorkflowRun) ([]workflow.Run, error) {
	out := make([]workflow.Run, 0, len(rows))
	for _, row := range rows {
		run, err := decodeWorkflow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}
func (s *workflowStore) Active(ctx context.Context) ([]workflow.Run, error) {
	var rows []gormWorkflowRun
	err := s.repo.db.WithContext(ctx).Where("workflow_version = ? AND status IN ?", workflow.SchemaVersion, workflowRecoverableStatuses()).Order("created_at, id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return decodeWorkflows(rows)
}

func (s *workflowStore) Create(ctx context.Context, input workflow.Run, events []workflow.Event) (workflow.Run, bool, error) {
	// Clone before assigning task IDs; failed transactions never mutate caller slices.
	run, err := cloneWorkflow(input)
	if err != nil {
		return workflow.Run{}, false, err
	}
	if run.ID == "" || run.Scope.TenantID == 0 || run.Scope.UserID == 0 || run.TeamID == 0 || run.KeyHash == "" || run.Fingerprint == "" || len(run.Plan.Definition.Nodes) == 0 {
		return workflow.Run{}, false, ErrInvalidInput
	}
	created := false
	err = withRetryableTransaction(ctx, func() error {
		// A retry must not reuse task IDs or other projections assigned by a
		// transaction that MySQL rolled back while resolving a deadlock.
		var cloneErr error
		run, cloneErr = cloneWorkflow(input)
		if cloneErr != nil {
			return cloneErr
		}
		created = false
		return s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var prior gormWorkflowRun
			err := workflowQuery(tx, run.Scope).Where("idempotency_key_hash = ?", run.KeyHash).Take(&prior).Error
			if err == nil {
				var e error
				run, e = matchingWorkflow(prior, input.Fingerprint)
				return e
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := validateWorkflowOwner(tx, run); err != nil {
				return err
			}
			// The authenticated User row serializes admission for this scope. Both
			// reads below must be locking/current reads: the initial idempotency
			// lookup may have established an older MySQL REPEATABLE READ snapshot
			// before waiting for another Create's User lock.
			err = workflowQuery(tx, run.Scope).Where("idempotency_key_hash = ?", run.KeyHash).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&prior).Error
			if err == nil {
				var e error
				run, e = matchingWorkflow(prior, input.Fingerprint)
				return e
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := enforceWorkflowCapacity(tx, run.Scope); err != nil {
				return err
			}
			run.Revision = 1
			run.LastSequence = 0
			if run.CreatedAt.IsZero() {
				run.CreatedAt = time.Now().UTC()
			}
			if run.Status == "" {
				run.Status = workflow.Queued
			}
			if len(run.Nodes) == 0 {
				for _, n := range run.Plan.Definition.Nodes {
					run.Nodes = append(run.Nodes, workflow.NodeRun{NodeID: n.ID, Status: workflow.Pending})
				}
			}
			if err := validateWorkflowSnapshot(workflow.Run{}, run); err != nil {
				return err
			}
			values, err := workflowRunValues(run)
			if err != nil {
				return err
			}
			inserted := tx.Table("agent_team_runs").Create(values)
			if inserted.Error != nil {
				if !isWorkflowDuplicate(inserted.Error) {
					return inserted.Error
				}
				// A concurrent scoped-key insert (or ID collision) won. Read the
				// committed winner, not a MySQL REPEATABLE READ snapshot. Do not
				// infer creation from affected rows (clientFoundRows changes it).
				if err := workflowQuery(tx, run.Scope).Clauses(clause.Locking{Strength: "UPDATE"}).Where("idempotency_key_hash = ?", run.KeyHash).Take(&prior).Error; err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						return workflow.ErrConflict
					}
					return err
				}
				var e error
				run, e = matchingWorkflow(prior, input.Fingerprint)
				return e
			}
			if err := persistWorkflow(tx, workflow.Run{}, &run, events); err != nil {
				return err
			}
			created = true
			return nil
		})
	})
	if err != nil {
		return workflow.Run{}, false, err
	}
	return run, created, nil
}
func matchingWorkflow(row gormWorkflowRun, fingerprint string) (workflow.Run, error) {
	if valueStringPtr(row.Fingerprint) != fingerprint {
		return workflow.Run{}, workflow.ErrIdempotencyConflict
	}
	return decodeWorkflow(row)
}
func cloneWorkflow(run workflow.Run) (workflow.Run, error) {
	data, err := json.Marshal(run)
	if err != nil {
		return workflow.Run{}, err
	}
	var copy workflow.Run
	err = json.Unmarshal(data, &copy)
	copy.KeyHash = run.KeyHash
	copy.Fingerprint = run.Fingerprint
	return copy, err
}

func (s *workflowStore) Save(ctx context.Context, expected uint64, input workflow.Run, events []workflow.Event) (workflow.Run, error) {
	run, err := cloneWorkflow(input)
	if err != nil {
		return workflow.Run{}, err
	}
	err = withRetryableTransaction(ctx, func() error {
		// A retry must not reuse task IDs or other projections assigned by a
		// transaction that MySQL rolled back while resolving a deadlock.
		var cloneErr error
		run, cloneErr = cloneWorkflow(input)
		if cloneErr != nil {
			return cloneErr
		}
		return s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row gormWorkflowRun
			if err := workflowQuery(tx, run.Scope).Where("id = ?", run.ID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row).Error; err != nil {
				return workflowReadError(err)
			}
			if row.WorkflowRevision != expected {
				return workflow.ErrConflict
			}
			prior, err := decodeWorkflow(row)
			if err != nil {
				return err
			}
			if err := validateWorkflowSnapshot(prior, run); err != nil {
				return err
			}
			// Reserve CAS before projection writes, including on SQLite (no FOR UPDATE).
			updated := workflowQuery(tx, run.Scope).Where("id = ? AND workflow_revision = ?", run.ID, expected).Update("workflow_revision", expected+1)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return workflow.ErrConflict
			}
			run.Revision = expected + 1
			run.LastSequence = prior.LastSequence
			run.KeyHash = prior.KeyHash
			run.Fingerprint = prior.Fingerprint
			return persistWorkflow(tx, prior, &run, events)
		})
	})
	if err != nil {
		return workflow.Run{}, err
	}
	return run, nil
}

func (s *workflowStore) Events(ctx context.Context, scope workflow.Scope, id string, after uint64, limit int) ([]workflow.Event, error) {
	// Check ownership even when there are no events or the cursor is beyond the tail.
	if _, err := s.Get(ctx, scope, id); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > workflow.EventPageLimit {
		limit = workflow.EventPageLimit
	}
	var rows []gormAgentTeamRunEvent
	if err := s.repo.db.WithContext(ctx).Where("tenant_id = ? AND team_run_id = ? AND sequence_no > ?", scope.TenantID, id, after).Order("sequence_no").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]workflow.Event, 0, len(rows))
	for _, row := range rows {
		var e workflow.Event
		if row.PayloadJSON == nil {
			return nil, fmt.Errorf("workflow event payload missing")
		}
		if err := json.Unmarshal([]byte(*row.PayloadJSON), &e); err != nil {
			return nil, err
		}
		e.Sequence = row.SequenceNo
		out = append(out, e)
	}
	return out, nil
}

func (r *GormRepository) WorkflowTaskOwner(ctx context.Context, taskID uint64) (string, bool, error) {
	var row gormWorkflowTaskBinding
	err := r.db.WithContext(ctx).Where("task_id = ?", taskID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.RunID, true, nil
}

func validateWorkflowSnapshot(prior, run workflow.Run) error {
	// A terminal Run cannot be reopened or rewritten into another outcome.
	// Interrupted is intentionally recoverable and is not workflow.Terminal.
	if prior.ID != "" && (run.Epoch < prior.Epoch || (workflow.Terminal(prior.Status) && run.Status != prior.Status)) {
		return workflow.ErrConflict
	}
	if prior.ID != "" && (run.TeamID != prior.TeamID || !reflect.DeepEqual(run.Plan, prior.Plan) || run.TeamKey != prior.TeamKey || run.TeamVersion != prior.TeamVersion || run.Input != prior.Input || !run.CreatedAt.Equal(prior.CreatedAt)) {
		return workflow.ErrConflict
	}
	definitions := make(map[string]bool, len(run.Plan.Definition.Nodes))
	for _, n := range run.Plan.Definition.Nodes {
		if n.ID == "" || definitions[n.ID] {
			return ErrInvalidInput
		}
		definitions[n.ID] = true
	}
	nodes := make(map[string]workflow.NodeRun, len(run.Nodes))
	for _, n := range run.Nodes {
		if !definitions[n.NodeID] {
			return ErrInvalidInput
		}
		if _, ok := nodes[n.NodeID]; ok {
			return ErrInvalidInput
		}
		nodes[n.NodeID] = n
		for i, a := range n.Attempts {
			if a.Number != i+1 || (i > 0 && !workflowAttemptTerminal(n.Attempts[i-1].Status)) {
				return ErrInvalidInput
			}
		}
	}
	if len(nodes) != len(definitions) {
		return ErrInvalidInput
	}
	completedNodes := make(map[string]bool)
	for _, old := range prior.Nodes {
		n, ok := nodes[old.NodeID]
		if !ok || len(n.Attempts) < len(old.Attempts) {
			return workflow.ErrConflict
		}
		if old.Status == workflow.Completed {
			completedNodes[old.NodeID] = true
			if !reflect.DeepEqual(old, n) {
				return workflow.ErrConflict
			}
		}
		for i, a := range old.Attempts {
			b := n.Attempts[i]
			if b.TaskID != a.TaskID || b.Epoch != a.Epoch || !b.StartedAt.Equal(a.StartedAt) || (workflowAttemptTerminal(a.Status) && !reflect.DeepEqual(a, b)) {
				return workflow.ErrConflict
			}
		}
	}
	priorArtifactIDs := make(map[string]bool, len(prior.Artifacts))
	for _, a := range prior.Artifacts {
		priorArtifactIDs[a.ID] = true
	}
	artifacts := map[string]workflow.Artifact{}
	for _, a := range run.Artifacts {
		// Completing a node seals its artifact set, not just its ArtifactID.
		if completedNodes[a.NodeID] && !priorArtifactIDs[a.ID] {
			return workflow.ErrConflict
		}
		n, ok := nodes[a.NodeID]
		if a.ID == "" || !ok || a.AttemptNo <= 0 || a.AttemptNo > len(n.Attempts) {
			return ErrInvalidInput
		}
		if _, ok := artifacts[a.ID]; ok {
			return ErrInvalidInput
		}
		artifacts[a.ID] = a
	}
	for _, a := range prior.Artifacts {
		if !reflect.DeepEqual(artifacts[a.ID], a) {
			return workflow.ErrConflict
		}
	}
	return nil
}

// The scope must identify a real tenant member and an accessible team, not only
// matching IDs on the new run row. Shared teams have no private owner.
func validateWorkflowOwner(tx *gorm.DB, run workflow.Run) error {
	var user gormTenantUser
	if err := tx.Select("id").Where("tenant_id = ? AND id = ? AND deleted_at IS NULL", run.Scope.TenantID, run.Scope.UserID).Clauses(clause.Locking{Strength: "UPDATE"}).Take(&user).Error; err != nil {
		return workflowReadError(err)
	}
	var team gormAgentTeam
	return workflowReadError(tx.Select("id").Where("tenant_id = ? AND id = ? AND (owner_user_id IS NULL OR owner_user_id = ?)", run.Scope.TenantID, run.TeamID, run.Scope.UserID).Take(&team).Error)
}

func isWorkflowDuplicate(err error) bool {
	if isMySQLDuplicateKey(err) || errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && (sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique || sqliteErr.ExtendedCode == sqlite3.ErrConstraintPrimaryKey)
}

func workflowRunValues(run workflow.Run) (map[string]any, error) {
	checkpoint, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":                     run.ID,
		"tenant_id":              run.Scope.TenantID,
		"owner_user_id":          run.Scope.UserID,
		"team_id":                run.TeamID,
		"source_kind":            workflowSourceDesktop,
		"workflow_version":       workflow.SchemaVersion,
		"workflow_revision":      run.Revision,
		"workflow_state_json":    string(checkpoint),
		"idempotency_key_hash":   run.KeyHash,
		"workflow_fingerprint":   run.Fingerprint,
		"inbox_event_id":         nil,
		"source_account_id":      nil,
		"coordinator_member_key": run.Plan.Definition.Nodes[0].AgentRef,
		"status":                 string(run.Status),
		"team_effective_hash":    run.Plan.Hash,
		"member_count":           len(run.Plan.Agents),
		"max_parallel_members":   run.Plan.Definition.Limits.MaxParallel,
		"max_total_tokens":       run.Plan.Definition.Limits.MaxTotalTokens,
		"created_at":             run.CreatedAt,
		"updated_at":             run.CreatedAt,
	}, nil
}

// ByKey is an optional indexed lookup; callers may type-assert this method
// without expanding the stable workflow.Store contract.
func (s *workflowStore) ByKey(ctx context.Context, scope workflow.Scope, keyHash string) (workflow.Run, error) {
	var row gormWorkflowRun
	if err := workflowQuery(s.repo.db.WithContext(ctx), scope).Where("idempotency_key_hash = ?", keyHash).Take(&row).Error; err != nil {
		return workflow.Run{}, workflowReadError(err)
	}
	return decodeWorkflow(row)
}

func workflowRecoverableStatuses() []workflow.Status {
	return []workflow.Status{workflow.Queued, workflow.Running, workflow.CancelRequested, workflow.Interrupted}
}

func enforceWorkflowCapacity(tx *gorm.DB, scope workflow.Scope) error {
	// A bounded current-read count, not SELECT COUNT from an old snapshot. This
	// also avoids loading aggregate JSON checkpoints during queue admission.
	var activeIDs []string
	if err := workflowQuery(tx, scope).Where("status IN ?", workflowRecoverableStatuses()).Clauses(clause.Locking{Strength: "UPDATE"}).Limit(workflow.MaxQueuedRuns).Pluck("id", &activeIDs).Error; err != nil {
		return err
	}
	if len(activeIDs) >= workflow.MaxQueuedRuns {
		return workflow.ErrCapacity
	}
	return nil
}
