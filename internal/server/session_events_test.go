package server

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/session"
	mysqlstore "github.com/konglong87/super-niuma/internal/storage/mysql"
)

type fallbackSessionEventReader struct {
	calls int
}

func (r *fallbackSessionEventReader) ListSessionConversationEvents(_ context.Context, sessionID, afterID uint64, _ int) ([]mysqlstore.AgentTaskEvent, error) {
	r.calls++
	return []mysqlstore.AgentTaskEvent{{ID: afterID + 1, TaskID: sessionID, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"from sqlite"}`}}, nil
}

func TestJSONLSessionEventStoreFallsBackWhenTranscriptIsMissing(t *testing.T) {
	fallback := &fallbackSessionEventReader{}
	events := NewJSONLSessionEventStoreWithFallback(session.Store{TranscriptProjectsRoot: t.TempDir()}, fallback)

	got, err := events.ListSessionEvents(context.Background(), 7, 11, 99, "", 4, 20)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.calls != 1 || len(got) != 1 || got[0].ID != 5 || got[0].PayloadJSON != `{"content":"from sqlite"}` {
		t.Fatalf("fallback calls=%d events=%+v", fallback.calls, got)
	}
}

func TestJSONLSessionEventStoreAppendsAndReadsScopedEvents(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	events := NewJSONLSessionEventStore(store)
	task := mysqlstore.AgentTask{
		ID:              41,
		TenantID:        7,
		UserID:          11,
		ParentSessionID: 99,
		MetadataJSON:    fmt.Sprintf(`{"cwd":%q,"source":"feishu","channel":"feishu"}`, t.TempDir()),
	}

	first, err := events.AppendTaskEvent(context.Background(), task, agenttasks.EventInput{
		TaskID: task.ID, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"hello"}`, TraceID: "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := events.AppendTaskEvent(context.Background(), task, agenttasks.EventInput{
		TaskID: task.ID, EventType: agenttasks.EventTextDelta, PayloadJSON: `{"content":"world"}`, TraceID: "trace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 2 {
		t.Fatalf("sequences = %d, %d; want 1, 2", first, second)
	}

	got, err := events.ListTaskEvents(context.Background(), task, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("events = %+v", got)
	}
	if got[0].PayloadJSON != `{"content":"hello"}` || got[1].EventType != agenttasks.EventTextDelta {
		t.Fatalf("projected events = %+v", got)
	}
	if got[0].Source != "feishu" || got[0].Surface != "wails" || got[0].Channel != "feishu" {
		t.Fatalf("projected provenance = %+v", got[0])
	}

	sessionEvents, err := events.ListSessionEvents(context.Background(), task.TenantID, task.UserID, task.ParentSessionID, taskCWD(task), 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionEvents) != 1 || sessionEvents[0].ID != 2 {
		t.Fatalf("session events after cursor = %+v", sessionEvents)
	}
}

func TestJSONLSessionEventStoreFallsBackWhenTaskWorkspaceChanged(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	events := NewJSONLSessionEventStore(store)
	original := mysqlstore.AgentTask{
		ID: 41, TenantID: 7, UserID: 11, ParentSessionID: 99,
		MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, filepath.Join(t.TempDir(), "original")),
	}
	if _, err := events.AppendTaskEvent(context.Background(), original, agenttasks.EventInput{
		TaskID: original.ID, EventType: agenttasks.EventMessage, PayloadJSON: `{"content":"kept"}`,
	}); err != nil {
		t.Fatal(err)
	}

	latest := original
	latest.MetadataJSON = fmt.Sprintf(`{"cwd":%q}`, filepath.Join(t.TempDir(), "changed"))
	got, err := events.ListTaskEvents(context.Background(), latest, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TaskID != original.ID || got[0].PayloadJSON != `{"content":"kept"}` {
		t.Fatalf("events after workspace change = %+v", got)
	}
}

func TestJSONLSessionEventStoreConcurrentSessionsStayIsolated(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	events := NewJSONLSessionEventStore(store)
	tasks := []mysqlstore.AgentTask{
		{ID: 101, TenantID: 7, UserID: 11, ParentSessionID: 201, MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, t.TempDir())},
		{ID: 102, TenantID: 7, UserID: 11, ParentSessionID: 202, MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, t.TempDir())},
		{ID: 103, TenantID: 7, UserID: 11, ParentSessionID: 203, MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, t.TempDir())},
	}

	const eventsPerSession = 16
	var wg sync.WaitGroup
	errs := make(chan error, len(tasks)*eventsPerSession)
	for _, task := range tasks {
		task := task
		for index := 0; index < eventsPerSession; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := events.AppendTaskEvent(context.Background(), task, agenttasks.EventInput{
					TaskID:      task.ID,
					EventType:   agenttasks.EventTextDelta,
					PayloadJSON: fmt.Sprintf(`{"session":%d}`, task.ParentSessionID),
				})
				errs <- err
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, task := range tasks {
		got, err := events.ListTaskEvents(context.Background(), task, 0, eventsPerSession+1)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != eventsPerSession {
			t.Fatalf("session %d event count = %d, want %d", task.ParentSessionID, len(got), eventsPerSession)
		}
		for index, event := range got {
			if event.ID != uint64(index+1) || event.TaskID != task.ID {
				t.Fatalf("session %d event[%d] = %+v", task.ParentSessionID, index, event)
			}
		}
	}
}

func TestJSONLSessionEventStoreUsesDeterministicTranscript(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	events := NewJSONLSessionEventStore(store)
	cwd := filepath.Join(t.TempDir(), "workspace")
	task := mysqlstore.AgentTask{ID: 1, TenantID: 2, UserID: 3, ParentSessionID: 4, MetadataJSON: fmt.Sprintf(`{"cwd":%q}`, cwd)}

	if _, err := events.AppendTaskEvent(context.Background(), task, agenttasks.EventInput{
		TaskID: task.ID, EventType: agenttasks.EventStarted, PayloadJSON: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	summary, ok, err := store.Find(transcriptIDForSession(task.TenantID, task.UserID, task.ParentSessionID))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("deterministic transcript was not discoverable")
	}
	if summary.Path == "" || summary.SessionID != transcriptIDForSession(2, 3, 4) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestJSONLSessionEventStoreConcurrentAppendsHaveUniqueSequences(t *testing.T) {
	store := session.Store{TranscriptProjectsRoot: t.TempDir()}
	events := NewJSONLSessionEventStore(store)
	task := mysqlstore.AgentTask{
		ID:              41,
		TenantID:        7,
		UserID:          11,
		ParentSessionID: 99,
		MetadataJSON:    fmt.Sprintf(`{"cwd":%q}`, t.TempDir()),
	}

	const count = 24
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := events.AppendTaskEvent(context.Background(), task, agenttasks.EventInput{
				TaskID: task.ID, EventType: agenttasks.EventTextDelta,
				PayloadJSON: fmt.Sprintf(`{"index":%d}`, index),
			})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	got, err := events.ListTaskEvents(context.Background(), task, 0, count+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != count {
		t.Fatalf("event count = %d, want %d", len(got), count)
	}
	for index, event := range got {
		want := uint64(index + 1)
		if event.ID != want {
			t.Fatalf("event %d has id %d, want %d", index, event.ID, want)
		}
	}
}
