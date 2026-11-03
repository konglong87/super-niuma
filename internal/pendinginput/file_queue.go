package pendinginput

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileQueue struct {
	mu     sync.Mutex
	path   string
	memory *MemoryQueue
}

type fileQueueState struct {
	Items    []PendingInput  `json:"items"`
	Settings map[string]bool `json:"settings,omitempty"`
	Sequence int64           `json:"sequence"`
	ID       uint64          `json:"id"`
}

func NewFileQueue(path string) (*FileQueue, error) {
	path = filepath.Clean(path)
	if path == "." || path == "" {
		return nil, errors.New("pending input queue path is required")
	}
	queue := &FileQueue{path: path, memory: NewMemoryQueue()}
	if err := queue.load(); err != nil {
		return nil, err
	}
	return queue, nil
}

func (q *FileQueue) Add(ctx context.Context, input NewInput) (PendingInput, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, err := q.memory.Add(ctx, input)
	if err == nil {
		err = q.saveLocked()
	}
	return item, err
}

func (q *FileQueue) List(ctx context.Context, scope Scope) ([]PendingInput, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.List(ctx, scope)
}

func (q *FileQueue) FindByClientInputID(ctx context.Context, tenantID, userID uint64, clientInputID string) (PendingInput, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.FindByClientInputID(ctx, tenantID, userID, clientInputID)
}

func (q *FileQueue) ListActiveSessionStates(ctx context.Context, tenantID, userID uint64, sessionIDs []string) (map[string]ActiveSessionState, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.ListActiveSessionStates(ctx, tenantID, userID, sessionIDs)
}

func (q *FileQueue) Update(ctx context.Context, id string, patch UpdateInput) (PendingInput, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, err := q.memory.Update(ctx, id, patch)
	if err == nil {
		err = q.saveLocked()
	}
	return item, err
}

func (q *FileQueue) Cancel(ctx context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.memory.Cancel(ctx, id)
	if err == nil {
		err = q.saveLocked()
	}
	return err
}

func (q *FileQueue) MoveUp(ctx context.Context, id string) (PendingInput, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, err := q.memory.MoveUp(ctx, id)
	if err == nil {
		err = q.saveLocked()
	}
	return item, err
}

func (q *FileQueue) ClaimNext(ctx context.Context, scope Scope) (PendingInput, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, ok, err := q.memory.ClaimNext(ctx, scope)
	if err == nil && ok {
		err = q.saveLocked()
	}
	return item, ok, err
}

func (q *FileQueue) MarkSent(ctx context.Context, id string, taskID uint64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.memory.MarkSent(ctx, id, taskID)
	if err == nil {
		err = q.saveLocked()
	}
	return err
}

func (q *FileQueue) MarkFailed(ctx context.Context, id string, code string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.memory.MarkFailed(ctx, id, code)
	if err == nil {
		err = q.saveLocked()
	}
	return err
}

func (q *FileQueue) Retry(ctx context.Context, id string) (PendingInput, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	item, err := q.memory.Retry(ctx, id)
	if err == nil {
		err = q.saveLocked()
	}
	return item, err
}

func (q *FileQueue) QueueEnabled(ctx context.Context, scope Scope) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.QueueEnabled(ctx, scope)
}

func (q *FileQueue) SetQueueEnabled(ctx context.Context, scope Scope, enabled bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.memory.SetQueueEnabled(ctx, scope, enabled)
	if err == nil {
		err = q.saveLocked()
	}
	return err
}

func (q *FileQueue) AcquireConsumerLease(ctx context.Context, scope Scope, owner string, ttl time.Duration) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.AcquireConsumerLease(ctx, scope, owner, ttl)
}

func (q *FileQueue) ReleaseConsumerLease(ctx context.Context, scope Scope, owner string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.memory.ReleaseConsumerLease(ctx, scope, owner)
}

func (q *FileQueue) load() error {
	data, err := os.ReadFile(q.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state fileQueueState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	q.memory.mu.Lock()
	recovered := false
	q.memory.items = make(map[string]PendingInput, len(state.Items))
	for _, item := range state.Items {
		if item.Status == StatusRunning {
			item.Status = StatusFailed
			item.ErrorCode = "consumer_restarted"
			item.UpdatedAt = time.Now().UTC()
			recovered = true
		}
		q.memory.items[item.ID] = cloneInput(item)
	}
	q.memory.settings = state.Settings
	if q.memory.settings == nil {
		q.memory.settings = make(map[string]bool)
	}
	q.memory.sequence = state.Sequence
	q.memory.id = state.ID
	q.memory.mu.Unlock()
	if recovered {
		return q.saveLocked()
	}
	return nil
}

func (q *FileQueue) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(q.path), 0o700); err != nil {
		return err
	}
	q.memory.mu.Lock()
	state := fileQueueState{Settings: make(map[string]bool, len(q.memory.settings)), Sequence: q.memory.sequence, ID: q.memory.id}
	for _, item := range q.memory.items {
		state.Items = append(state.Items, cloneInput(item))
	}
	for key, enabled := range q.memory.settings {
		state.Settings[key] = enabled
	}
	q.memory.mu.Unlock()
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(q.path), ".pending-inputs-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, q.path)
}
