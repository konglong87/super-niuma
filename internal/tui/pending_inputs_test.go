package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/konglong87/super-niuma/internal/pendinginput"
)

func TestModelQueuesEnterWhileBusy(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }, PendingInputQueue: pendinginput.NewMemoryQueue(), Welcome: WelcomeInfo{SessionID: "session-1"}})
	model.busy = true
	model.textarea.SetValue("queued prompt")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("busy enter returned command %v", cmd)
	}
	m := updated.(Model)
	items, err := m.pendingInputQueue.List(context.Background(), m.pendingInputScope())
	if err != nil {
		t.Fatalf("list pending inputs: %v", err)
	}
	if len(items) != 1 || items[0].Content != "queued prompt" {
		t.Fatalf("pending inputs = %+v", items)
	}
	if m.textarea.Value() != "" {
		t.Fatalf("textarea = %q; want empty after enqueue", m.textarea.Value())
	}
}

func TestModelPendingInputsViewIncludesSequenceAndDirection(t *testing.T) {
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }, PendingInputQueue: pendinginput.NewMemoryQueue(), Welcome: WelcomeInfo{SessionID: "session-1"}})
	_, err := model.pendingInputQueue.Add(context.Background(), pendinginput.NewInput{Scope: model.pendingInputScope(), ClientInputID: "1", Content: "prompt", Direction: "focus"})
	if err != nil {
		t.Fatalf("add pending input: %v", err)
	}
	view := model.pendingInputsView()
	if !strings.Contains(view, "1") || !strings.Contains(view, "prompt") || !strings.Contains(view, "focus") {
		t.Fatalf("pending view = %q", view)
	}
}

func TestModelHandlesQueueCommandWhileBusy(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }, PendingInputQueue: queue, Welcome: WelcomeInfo{SessionID: "session-1"}})
	model.busy = true
	model.textarea.SetValue("/queue off")
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(Model)
	enabled, err := queue.QueueEnabled(context.Background(), m.pendingInputScope())
	if err != nil {
		t.Fatalf("queue enabled: %v", err)
	}
	if enabled {
		t.Fatal("/queue off was queued as text instead of disabling the queue")
	}
	items, _ := queue.List(context.Background(), m.pendingInputScope())
	if len(items) != 0 {
		t.Fatalf("queue command became pending input: %+v", items)
	}
}

func TestModelSelectsAndMovesPendingInputWithControlArrows(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }, PendingInputQueue: queue, Welcome: WelcomeInfo{SessionID: "session-1"}})
	scope := model.pendingInputScope()
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
	m := updated.(Model)
	if m.pendingInputSelected != 1 {
		t.Fatalf("selected=%d want 1", m.pendingInputSelected)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	m = updated.(Model)
	items, _ := queue.List(context.Background(), scope)
	if len(items) != 2 || items[0].Content != "B" || items[1].Content != "A" || m.pendingInputSelected != 0 {
		t.Fatalf("items=%+v selected=%d", items, m.pendingInputSelected)
	}
}

func TestModelQueuesAndRestoresAttachmentsWhileBusy(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	model := NewModel(context.Background(), Options{RunStreamWithAttachments: func(context.Context, string, []Attachment, chan<- StreamEvent) error { return nil }, PendingInputQueue: queue, Welcome: WelcomeInfo{SessionID: "session-1"}})
	model.busy = true
	model.attachments = []Attachment{{ID: 7, Type: "image", MediaType: "image/png", Name: "queued.png", Path: "/tmp/queued.png", SizeBytes: 12}}
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m := updated.(Model)
	items, _ := queue.List(context.Background(), m.pendingInputScope())
	if len(items) != 1 || len(items[0].Attachments) != 1 {
		t.Fatalf("queued attachments=%+v", items)
	}
	m.busy = false
	next, cmd := m.startNextPendingInput()
	if cmd == nil || next.activePendingInputID != items[0].ID || len(next.attachments) != 0 {
		t.Fatalf("started model active=%q attachments=%+v cmd=%v", next.activePendingInputID, next.attachments, cmd)
	}
	restored := tuiAttachmentsFromPending(items[0].Attachments)
	if len(restored) != 1 || restored[0].Path != "/tmp/queued.png" {
		t.Fatalf("restored attachments=%+v", restored)
	}
}

func TestModelPausesPendingQueueAfterFailedRun(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	model := NewModel(context.Background(), Options{RunStream: func(context.Context, string, chan<- StreamEvent) error { return nil }, PendingInputQueue: queue, Welcome: WelcomeInfo{SessionID: "session-1"}})
	scope := model.pendingInputScope()
	first, _ := queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "a", Content: "A"})
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: scope, ClientInputID: "b", Content: "B"})
	started, _ := model.startNextPendingInput()
	updated, _ := started.Update(streamEventMsg{event: StreamEvent{Type: StreamFinished, Err: errors.New("boom")}})
	m := updated.(Model)
	items, _ := queue.List(context.Background(), scope)
	if len(items) != 2 || items[0].ID != first.ID || items[0].Status != pendinginput.StatusFailed || items[1].Status != pendinginput.StatusQueued || m.activePendingInputID != "" {
		t.Fatalf("failed queue items=%+v active=%q", items, m.activePendingInputID)
	}
}

func TestModelNonStreamCompletionStartsNextPendingInput(t *testing.T) {
	queue := pendinginput.NewMemoryQueue()
	model := NewModel(context.Background(), Options{RunResult: func(context.Context, string) (QueryResult, error) { return QueryResult{Response: "ok"}, nil }, PendingInputQueue: queue, Welcome: WelcomeInfo{SessionID: "session-1"}})
	_, _ = queue.Add(context.Background(), pendinginput.NewInput{Scope: model.pendingInputScope(), ClientInputID: "a", Content: "A"})
	updated, cmd := model.Update(responseMsg{result: QueryResult{Response: "initial done"}})
	m := updated.(Model)
	if cmd == nil || !m.busy || m.activePendingInputID == "" {
		t.Fatalf("non-stream completion did not start queued input: busy=%v active=%q cmd=%v", m.busy, m.activePendingInputID, cmd)
	}
}
