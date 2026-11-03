package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type Store struct {
	mu          sync.Mutex
	nextTaskID  uint64
	nextEventID uint64
	tasks       []mysqlstore.AgentTask
	events      []mysqlstore.AgentTaskEvent
	cancelled   map[uint64]bool
	now         func() time.Time
}

func New() *Store {
	return &Store{
		nextTaskID:  1,
		nextEventID: 1,
		cancelled:   map[uint64]bool{},
		now:         time.Now,
	}
}

func (s *Store) CreateAgentTask(_ context.Context, input agenttasks.TaskInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	id := s.nextTaskID
	s.nextTaskID++
	started := s.now().UTC()
	s.tasks = append(s.tasks, mysqlstore.AgentTask{
		ID:                 id,
		TenantID:           input.TenantID,
		UserID:             input.UserID,
		ParentSessionID:    input.ParentSessionID,
		SubagentSessionKey: input.SubagentSessionKey,
		AgentName:          input.AgentName,
		Description:        input.Description,
		Status:             input.Status,
		Model:              input.Model,
		ResultJSON:         input.ResultJSON,
		MetadataJSON:       input.MetadataJSON,
		TraceID:            input.TraceID,
		StartedAt:          started,
	})
	return id, nil
}

func (s *Store) SeedAgentTask(task mysqlstore.AgentTask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	if task.ID == 0 {
		return
	}
	if task.StartedAt.IsZero() {
		task.StartedAt = s.now().UTC()
	}
	for i := range s.tasks {
		if s.tasks[i].ID != task.ID {
			continue
		}
		s.tasks[i] = mergeTask(s.tasks[i], task)
		if s.nextTaskID <= task.ID {
			s.nextTaskID = task.ID + 1
		}
		if s.tasks[i].Status == agenttasks.StatusCancelled {
			s.cancelled[task.ID] = true
		}
		return
	}
	s.tasks = append(s.tasks, task)
	if s.nextTaskID <= task.ID {
		s.nextTaskID = task.ID + 1
	}
	if task.Status == agenttasks.StatusCancelled {
		s.cancelled[task.ID] = true
	}
}

func mergeTask(existing mysqlstore.AgentTask, incoming mysqlstore.AgentTask) mysqlstore.AgentTask {
	if incoming.TenantID != 0 {
		existing.TenantID = incoming.TenantID
	}
	if incoming.UserID != 0 {
		existing.UserID = incoming.UserID
	}
	if incoming.ParentSessionID != 0 {
		existing.ParentSessionID = incoming.ParentSessionID
	}
	if incoming.SubagentSessionKey != "" {
		existing.SubagentSessionKey = incoming.SubagentSessionKey
	}
	if incoming.AgentName != "" {
		existing.AgentName = incoming.AgentName
	}
	if incoming.Description != "" {
		existing.Description = incoming.Description
	}
	if incoming.Status != "" {
		existing.Status = incoming.Status
	}
	if incoming.Model != "" {
		existing.Model = incoming.Model
	}
	if incoming.ResultJSON != "" {
		existing.ResultJSON = incoming.ResultJSON
	}
	if incoming.MetadataJSON != "" {
		existing.MetadataJSON = incoming.MetadataJSON
	}
	if incoming.TraceID != "" {
		existing.TraceID = incoming.TraceID
	}
	if !incoming.StartedAt.IsZero() {
		existing.StartedAt = incoming.StartedAt
	}
	if !incoming.FinishedAt.IsZero() {
		existing.FinishedAt = incoming.FinishedAt
	}
	return existing
}

func (s *Store) FinishAgentTask(_ context.Context, taskID uint64, status string, resultJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	for i := range s.tasks {
		if s.tasks[i].ID != taskID {
			continue
		}
		if s.tasks[i].Status == agenttasks.StatusCancelled && status != agenttasks.StatusCancelled {
			return nil
		}
		s.tasks[i].Status = status
		s.tasks[i].ResultJSON = resultJSON
		s.tasks[i].FinishedAt = s.now().UTC()
		if status == agenttasks.StatusCancelled {
			s.cancelled[taskID] = true
		}
		return nil
	}
	return mysqlstore.ErrNotFound
}

func (s *Store) AppendAgentTaskEvent(_ context.Context, input agenttasks.EventInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	id := s.nextEventID
	s.nextEventID++
	s.events = append(s.events, mysqlstore.AgentTaskEvent{
		ID:          id,
		TaskID:      input.TaskID,
		EventType:   input.EventType,
		PayloadJSON: input.PayloadJSON,
		TraceID:     input.TraceID,
		CreatedAt:   s.now().UTC(),
	})
	return id, nil
}

func (s *Store) ListAgentTasks(_ context.Context, limit int) ([]mysqlstore.AgentTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]mysqlstore.AgentTask(nil), s.tasks...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ListAgentTaskEvents(_ context.Context, taskID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return s.ListAgentTaskEventsAfter(context.Background(), taskID, 0, limit)
}

func (s *Store) ListAgentTaskEventsAfter(_ context.Context, taskID uint64, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mysqlstore.AgentTaskEvent, 0)
	for _, event := range s.events {
		if event.TaskID == taskID && event.ID > afterID {
			out = append(out, event)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) CancelAgentTask(_ context.Context, taskID uint64, resultJSON string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	for i := range s.tasks {
		if s.tasks[i].ID != taskID {
			continue
		}
		s.cancelled[taskID] = true
		s.tasks[i].Status = agenttasks.StatusCancelled
		s.tasks[i].ResultJSON = resultJSON
		s.tasks[i].FinishedAt = s.now().UTC()
		return nil
	}
	return mysqlstore.ErrNotFound
}

func (s *Store) IsAgentTaskCancelled(_ context.Context, taskID uint64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled[taskID], nil
}

func (s *Store) ensureLocked() {
	if s.nextTaskID == 0 {
		s.nextTaskID = 1
	}
	if s.nextEventID == 0 {
		s.nextEventID = 1
	}
	if s.cancelled == nil {
		s.cancelled = map[uint64]bool{}
	}
	if s.now == nil {
		s.now = time.Now
	}
}
