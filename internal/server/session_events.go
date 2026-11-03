package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/session"
	"github.com/konglong87/super-niuma/internal/sessioncontrol"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

// SessionEventStore is the high-frequency conversation event boundary. The
// SQLite implementation remains behind TenantService; the JSONL implementation
// writes directly to one transcript per logical session.
type SessionEventStore interface {
	AppendTaskEvent(context.Context, mysqlstore.AgentTask, agenttasks.EventInput) (uint64, error)
	ListTaskEvents(context.Context, mysqlstore.AgentTask, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
	ListSessionEvents(context.Context, uint64, uint64, uint64, string, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
}

type SessionConversationEventReader interface {
	ListSessionConversationEvents(context.Context, uint64, uint64, int) ([]mysqlstore.AgentTaskEvent, error)
}

// JSONLSessionEventStore maps a database control-plane session to a stable
// transcript UUID. The mapping is deterministic so a restart never creates a
// second transcript for the same tenant/user/session tuple.
type JSONLSessionEventStore struct {
	store    session.Store
	fallback SessionConversationEventReader
}

func NewJSONLSessionEventStore(store session.Store) *JSONLSessionEventStore {
	return NewJSONLSessionEventStoreWithFallback(store, nil)
}

func NewJSONLSessionEventStoreWithFallback(store session.Store, fallback SessionConversationEventReader) *JSONLSessionEventStore {
	store.SchemaV2 = true
	return &JSONLSessionEventStore{store: store, fallback: fallback}
}

func (s *JSONLSessionEventStore) AppendTaskEvent(_ context.Context, task mysqlstore.AgentTask, input agenttasks.EventInput) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("JSONL session event store is nil")
	}
	recorder, err := s.openRecorder(task)
	if err != nil {
		return 0, err
	}
	defer recorder.Close()
	payload := strings.TrimSpace(input.PayloadJSON)
	if payload == "" {
		payload = "{}"
	}
	sequence, err := recorder.AppendEvent(session.TranscriptEvent{
		TaskID:      input.TaskID,
		EventType:   input.EventType,
		PayloadJSON: payload,
		TraceID:     input.TraceID,
		CreatedAt:   time.Now().UTC(),
		Source:      sessionEventSource(task),
		Surface:     "wails",
		Channel:     sessionEventChannel(task),
		Scope:       "tenant",
		TenantKey:   taskTenantKey(task),
		UserKey:     taskUserKey(task),
	})
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

func (s *JSONLSessionEventStore) ListTaskEvents(_ context.Context, task mysqlstore.AgentTask, after uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	events, err := s.loadTaskEvents(task, after, limit)
	if err != nil {
		return nil, err
	}
	return projectTranscriptEvents(events), nil
}

func (s *JSONLSessionEventStore) ListSessionEvents(ctx context.Context, tenantID, userID, sessionID uint64, _ string, after uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("JSONL session event store is nil")
	}
	transcriptID := transcriptIDForSession(tenantID, userID, sessionID)
	summary, ok, err := s.store.Find(transcriptID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return s.readFallback(ctx, sessionID, after, limit)
	}
	events, err := session.LoadEvents(summary.Path, after, limit)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return s.readFallback(ctx, sessionID, after, limit)
	}
	return projectTranscriptEvents(events), nil
}

func (s *JSONLSessionEventStore) readFallback(ctx context.Context, sessionID, after uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	if s.fallback == nil {
		return nil, nil
	}
	return s.fallback.ListSessionConversationEvents(ctx, sessionID, after, limit)
}

func (s *JSONLSessionEventStore) openRecorder(task mysqlstore.AgentTask) (*session.Recorder, error) {
	cwd := taskCWD(task)
	recorder, _, err := s.store.OpenOrCreateRecorder(cwd, transcriptIDForSession(task.TenantID, task.UserID, taskSessionID(task)))
	return recorder, err
}

func (s *JSONLSessionEventStore) loadTaskEvents(task mysqlstore.AgentTask, after uint64, limit int) ([]session.TranscriptEvent, error) {
	if s == nil {
		return nil, fmt.Errorf("JSONL session event store is nil")
	}
	transcriptID := transcriptIDForSession(task.TenantID, task.UserID, taskSessionID(task))
	summary, ok, err := s.store.LocateInProject(taskCWD(task), transcriptID)
	if err == nil && !ok {
		summary, ok, err = s.store.Locate(transcriptID)
	}
	if err != nil || !ok {
		return nil, err
	}
	events, err := session.LoadEvents(summary.Path, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]session.TranscriptEvent, 0, len(events))
	for _, event := range events {
		if event.TaskID == task.ID {
			out = append(out, event)
		}
	}
	return out, nil
}

func projectTranscriptEvents(events []session.TranscriptEvent) []mysqlstore.AgentTaskEvent {
	out := make([]mysqlstore.AgentTaskEvent, 0, len(events))
	for _, event := range events {
		out = append(out, mysqlstore.AgentTaskEvent{
			ID:          event.Sequence,
			TaskID:      event.TaskID,
			EventType:   event.EventType,
			PayloadJSON: event.PayloadJSON,
			TraceID:     event.TraceID,
			Source:      event.Source,
			Surface:     event.Surface,
			Channel:     event.Channel,
			CreatedAt:   event.CreatedAt,
		})
	}
	return out
}

func transcriptIDForSession(tenantID, userID, sessionID uint64) string {
	name := fmt.Sprintf("niuma/desktop/session/%d/%d/%d", tenantID, userID, sessionID)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)).String()
}

func taskCWD(task mysqlstore.AgentTask) string {
	var metadata map[string]any
	if json.Unmarshal([]byte(task.MetadataJSON), &metadata) == nil {
		if cwd, ok := metadata["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			return strings.TrimSpace(cwd)
		}
	}
	return "."
}

func taskSessionID(task mysqlstore.AgentTask) uint64 {
	if task.ParentSessionID != 0 {
		return task.ParentSessionID
	}
	return task.ID
}

func taskTenantKey(task mysqlstore.AgentTask) string {
	metadata := parseTaskMetadata(task.MetadataJSON)
	if key, ok := metadata["tenant_key"].(string); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	return fmt.Sprintf("tenant-id:%d", task.TenantID)
}

func taskUserKey(task mysqlstore.AgentTask) string {
	metadata := parseTaskMetadata(task.MetadataJSON)
	if key, ok := metadata["user_key"].(string); ok && strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	return fmt.Sprintf("user-id:%d", task.UserID)
}

func parseTaskMetadata(raw string) map[string]any {
	var metadata map[string]any
	if json.Unmarshal([]byte(raw), &metadata) != nil || metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func sessionEventSource(task mysqlstore.AgentTask) string {
	var metadata map[string]any
	if json.Unmarshal([]byte(task.MetadataJSON), &metadata) == nil {
		if source, ok := metadata["source"].(string); ok && strings.TrimSpace(source) != "" {
			return strings.TrimSpace(source)
		}
	}
	return "desktop"
}

func sessionEventChannel(task mysqlstore.AgentTask) string {
	var metadata map[string]any
	if json.Unmarshal([]byte(task.MetadataJSON), &metadata) == nil {
		if channel, ok := metadata["channel"].(string); ok && strings.TrimSpace(channel) != "" {
			return strings.TrimSpace(channel)
		}
	}
	return "desktop"
}

func appendAgentTaskEvent(ctx context.Context, opts Options, task mysqlstore.AgentTask, input agenttasks.EventInput) (uint64, error) {
	if opts.SessionEvents != nil {
		if input.TenantID == 0 {
			input.TenantID = task.TenantID
		}
		if input.UserID == 0 {
			input.UserID = task.UserID
		}
		if input.TaskID == 0 {
			input.TaskID = task.ID
		}
		return opts.SessionEvents.AppendTaskEvent(ctx, task, input)
	}
	if opts.TenantService == nil {
		return 0, fmt.Errorf("tenant event store is unavailable")
	}
	return opts.TenantService.AppendAgentTaskEvent(ctx, input)
}

func listAgentTaskEvents(ctx context.Context, opts Options, task mysqlstore.AgentTask, after uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	if opts.SessionEvents != nil {
		return opts.SessionEvents.ListTaskEvents(ctx, task, after, limit)
	}
	if opts.TenantService == nil {
		return nil, fmt.Errorf("tenant event store is unavailable")
	}
	return opts.TenantService.ListAgentTaskEventsAfter(ctx, task.ID, after, limit)
}

func appendAgentTaskEventByID(ctx context.Context, opts Options, taskID uint64, input agenttasks.EventInput) (uint64, error) {
	if opts.SessionEvents == nil {
		if opts.TenantService == nil {
			return 0, fmt.Errorf("tenant event store is unavailable")
		}
		return opts.TenantService.AppendAgentTaskEvent(ctx, input)
	}
	if opts.TenantService == nil {
		return 0, fmt.Errorf("tenant task store is unavailable")
	}
	task, err := opts.TenantService.GetAgentTask(ctx, taskID)
	if err != nil {
		return 0, err
	}
	return appendAgentTaskEvent(ctx, opts, task, input)
}

func listAgentTaskEventsByID(ctx context.Context, opts Options, taskID, after uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	if opts.SessionEvents == nil {
		if opts.TenantService == nil {
			return nil, fmt.Errorf("tenant event store is unavailable")
		}
		return opts.TenantService.ListAgentTaskEventsAfter(ctx, taskID, after, limit)
	}
	if opts.TenantService == nil {
		return nil, fmt.Errorf("tenant task store is unavailable")
	}
	task, err := opts.TenantService.GetAgentTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return listAgentTaskEvents(ctx, opts, task, after, limit)
}

type routedAgentTaskEventStreamer struct {
	opts Options
}

type sessionControlManagedEventStore struct {
	opts Options
}

func (s sessionControlManagedEventStore) AppendAgentTaskEvent(ctx context.Context, scope sessioncontrol.RequestContext, input agenttasks.EventInput) (uint64, error) {
	if s.opts.TenantService == nil || s.opts.SessionEvents == nil {
		return 0, fmt.Errorf("session event projection is unavailable")
	}
	if input.TaskID == 0 {
		return 0, fmt.Errorf("session task id is required")
	}
	task, err := s.opts.TenantService.GetAgentTask(ctx, input.TaskID)
	if err != nil {
		return 0, err
	}
	if task.TenantID != scope.TenantID || task.UserID != scope.UserID {
		return 0, mysqlstore.ErrNotFound
	}
	return s.opts.SessionEvents.AppendTaskEvent(ctx, task, input)
}

func (s sessionControlManagedEventStore) ListAgentTaskEventsForTasksComplete(ctx context.Context, _ sessioncontrol.RequestContext, taskIDs []uint64) ([]mysqlstore.AgentTaskEvent, error) {
	if s.opts.TenantService == nil || s.opts.SessionEvents == nil {
		return nil, fmt.Errorf("session event projection is unavailable")
	}
	out := make([]mysqlstore.AgentTaskEvent, 0)
	for _, taskID := range taskIDs {
		if taskID == 0 {
			continue
		}
		task, err := s.opts.TenantService.GetAgentTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		events, err := s.opts.SessionEvents.ListTaskEvents(ctx, task, 0, 10000)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s sessionControlManagedEventStore) ListAgentTaskEventsForTaskRecords(ctx context.Context, scope sessioncontrol.RequestContext, tasks []mysqlstore.AgentTask) ([]mysqlstore.AgentTaskEvent, error) {
	if s.opts.TenantService == nil || s.opts.SessionEvents == nil {
		return nil, fmt.Errorf("session event projection is unavailable")
	}
	out := make([]mysqlstore.AgentTaskEvent, 0)
	for _, task := range tasks {
		if task.ID == 0 {
			continue
		}
		if task.TenantID != scope.TenantID || task.UserID != scope.UserID {
			return nil, mysqlstore.ErrNotFound
		}
		events, err := s.opts.SessionEvents.ListTaskEvents(ctx, task, 0, 10000)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ID == out[j].ID {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s sessionControlManagedEventStore) GetTaskEvent(ctx context.Context, _ sessioncontrol.RequestContext, sessionID, eventID uint64) (mysqlstore.AgentTaskEvent, error) {
	if s.opts.TenantService == nil || s.opts.SessionEvents == nil {
		return mysqlstore.AgentTaskEvent{}, fmt.Errorf("session event projection is unavailable")
	}
	if sessionID == 0 || eventID == 0 {
		return mysqlstore.AgentTaskEvent{}, mysqlstore.ErrNotFound
	}
	tasks, err := s.opts.TenantService.ListAgentTasks(ctx, 10000)
	if err != nil {
		return mysqlstore.AgentTaskEvent{}, err
	}
	for _, task := range tasks {
		if task.ParentSessionID != sessionID {
			continue
		}
		events, err := s.opts.SessionEvents.ListTaskEvents(ctx, task, eventID-1, 1)
		if err != nil {
			return mysqlstore.AgentTaskEvent{}, err
		}
		for _, event := range events {
			if event.ID == eventID {
				return event, nil
			}
		}
	}
	return mysqlstore.AgentTaskEvent{}, mysqlstore.ErrNotFound
}

func NewSessionControlEventReader(opts Options) SessionControlEventService {
	return routedAgentTaskEventStreamer{opts: opts}
}

func (s routedAgentTaskEventStreamer) ListAgentTaskEventsAfter(ctx context.Context, taskID, afterID uint64, limit int) ([]mysqlstore.AgentTaskEvent, error) {
	return listAgentTaskEventsByID(ctx, s.opts, taskID, afterID, limit)
}

func (s routedAgentTaskEventStreamer) GetAgentTask(ctx context.Context, taskID uint64) (mysqlstore.AgentTask, error) {
	return s.opts.TenantService.GetAgentTask(ctx, taskID)
}
