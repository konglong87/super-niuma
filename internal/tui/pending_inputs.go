package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/konglong87/super-niuma/internal/agenttasks"
	"github.com/konglong87/super-niuma/internal/pendinginput"
)

func (m Model) pendingInputScope() pendinginput.Scope {
	sessionID := strings.TrimSpace(m.welcome.SessionID)
	if sessionID == "" {
		sessionID = "tui"
	}
	return pendinginput.Scope{TenantID: 1, UserID: 1, SessionID: sessionID, BaseTaskID: 1}
}

func defaultPendingInputQueue(sessionID, cwd string) pendinginput.Queue {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return pendinginput.NewMemoryQueue()
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(sessionID) + "\x00" + filepath.Clean(strings.TrimSpace(cwd))))
	path := filepath.Join(home, ".niuma", "pending-inputs", hex.EncodeToString(digest[:])+".json")
	queue, err := pendinginput.NewFileQueue(path)
	if err != nil {
		return pendinginput.NewMemoryQueue()
	}
	return queue
}

func (m *Model) queuePendingInput() bool {
	if m.pendingInputQueue == nil {
		return false
	}
	content := strings.TrimSpace(m.textarea.Value())
	if content == "" && len(m.attachments) == 0 {
		return false
	}
	attachments := referencedAttachments(m.textarea.Value(), m.attachments)
	if content == "" {
		attachments = append([]Attachment(nil), m.attachments...)
	}
	item, err := m.pendingInputQueue.Add(context.Background(), pendinginput.NewInput{
		Scope:         m.pendingInputScope(),
		ClientInputID: "tui-" + strings.ReplaceAll(strings.TrimSpace(m.textarea.Value()), " ", "-") + "-" + m.now().UTC().Format("20060102150405.000000000"),
		Content:       content,
		Attachments:   pendingAttachmentsFromTUI(attachments),
	})
	if err != nil {
		m.err = err
		return false
	}
	m.resetTextarea()
	m.attachments = nil
	m.clearDraft()
	m.err = nil
	m.runningStatus = "queued input " + item.ID
	if items, listErr := m.pendingInputQueue.List(context.Background(), m.pendingInputScope()); listErr == nil && len(items) > 0 {
		m.pendingInputSelected = len(items) - 1
	}
	m.refreshViewport()
	return true
}

func (m Model) pendingInputsView() string {
	if m.pendingInputQueue == nil {
		return ""
	}
	items, err := m.pendingInputQueue.List(context.Background(), m.pendingInputScope())
	if err != nil || len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, secondaryStyle.Render("Queued inputs"))
	for i, item := range items {
		line := truncateDisplay(strings.ReplaceAll(item.Content, "\n", " "), max(20, m.width-18))
		if strings.TrimSpace(line) == "" && len(item.Attachments) > 0 {
			line = itoa(len(item.Attachments)) + " attachment(s)"
		}
		if strings.TrimSpace(item.Direction) != "" {
			line += " · " + truncateDisplay(item.Direction, max(12, m.width/4))
		}
		marker := "  "
		if i == clamp(m.pendingInputSelected, 0, len(items)-1) {
			marker = "> "
		}
		lines = append(lines, statusStyle.Render(marker+itoa(i+1)+" ")+line+statusStyle.Render("  [Ctrl+Up move]"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) startNextPendingInput() (Model, tea.Cmd) {
	if m.pendingInputQueue == nil || m.busy {
		return m, nil
	}
	enabled, err := m.pendingInputQueue.QueueEnabled(context.Background(), m.pendingInputScope())
	if err != nil || !enabled {
		return m, nil
	}
	item, ok, err := m.pendingInputQueue.ClaimNext(context.Background(), m.pendingInputScope())
	if err != nil || !ok {
		return m, nil
	}
	m.setTextareaValue(composePendingInputForTUI(item))
	m.attachments = tuiAttachmentsFromPending(item.Attachments)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := updated.(Model)
	next.activePendingInputID = item.ID
	return next, cmd
}

func (m *Model) finishActivePendingInput(runErr error) {
	if m.activePendingInputID == "" || m.pendingInputQueue == nil {
		return
	}
	id := m.activePendingInputID
	m.activePendingInputID = ""
	if runErr != nil {
		_ = m.pendingInputQueue.MarkFailed(context.Background(), id, "run_failed")
		return
	}
	_ = m.pendingInputQueue.MarkSent(context.Background(), id, uint64(m.currentTurn))
}

func pendingAttachmentsFromTUI(items []Attachment) []agenttasks.Attachment {
	out := make([]agenttasks.Attachment, 0, len(items))
	for _, item := range items {
		url := item.URL
		if strings.TrimSpace(item.Path) != "" {
			url = "tui-file:" + item.Path
		}
		out = append(out, agenttasks.Attachment{AttachmentID: strconv.Itoa(item.ID), Type: item.Type, MediaType: item.MediaType, Name: item.Name, URL: url, SizeBytes: item.SizeBytes})
	}
	return out
}

func tuiAttachmentsFromPending(items []agenttasks.Attachment) []Attachment {
	out := make([]Attachment, 0, len(items))
	for _, item := range items {
		id, _ := strconv.Atoi(item.AttachmentID)
		attachment := Attachment{ID: id, Type: item.Type, MediaType: item.MediaType, Name: item.Name, URL: item.URL, SizeBytes: item.SizeBytes}
		if strings.HasPrefix(item.URL, "tui-file:") {
			attachment.Path = strings.TrimPrefix(item.URL, "tui-file:")
			attachment.URL = ""
		}
		out = append(out, attachment)
	}
	return out
}

func (m *Model) selectNextPendingInput() bool {
	items, err := m.pendingInputQueue.List(context.Background(), m.pendingInputScope())
	if err != nil || len(items) == 0 {
		return false
	}
	m.pendingInputSelected = min(len(items)-1, m.pendingInputSelected+1)
	return true
}

func (m *Model) moveSelectedPendingInputUp() bool {
	items, err := m.pendingInputQueue.List(context.Background(), m.pendingInputScope())
	if err != nil || len(items) == 0 {
		return false
	}
	m.pendingInputSelected = clamp(m.pendingInputSelected, 0, len(items)-1)
	if m.pendingInputSelected == 0 {
		return true
	}
	if _, err := m.pendingInputQueue.MoveUp(context.Background(), items[m.pendingInputSelected].ID); err != nil {
		m.err = err
		return true
	}
	m.pendingInputSelected--
	m.err = nil
	return true
}

func composePendingInputForTUI(item pendinginput.PendingInput) string {
	if strings.TrimSpace(item.Direction) == "" {
		return item.Content
	}
	return "[方向]\n" + item.Direction + "\n\n[原消息]\n" + item.Content
}

func (m *Model) handlePendingInputCommand(prompt string) bool {
	parts := strings.Fields(prompt)
	if len(parts) == 0 || parts[0] != "/queue" {
		return false
	}
	if m.pendingInputQueue == nil {
		m.err = pendinginput.ErrNotFound
		return true
	}
	ctx := context.Background()
	scope := m.pendingInputScope()
	if len(parts) >= 4 && parts[1] == "direction" {
		direction := strings.Join(parts[3:], " ")
		_, err := m.pendingInputQueue.Update(ctx, idOrEmpty(parts[2]), pendinginput.UpdateInput{Direction: &direction})
		m.err = err
		return true
	}
	switch len(parts) {
	case 2:
		switch parts[1] {
		case "on":
			m.err = m.pendingInputQueue.SetQueueEnabled(ctx, scope, true)
			return true
		case "off":
			m.err = m.pendingInputQueue.SetQueueEnabled(ctx, scope, false)
			return true
		case "list":
			m.err = nil
			return true
		}
	case 3:
		id := parts[2]
		switch parts[1] {
		case "up":
			_, err := m.pendingInputQueue.MoveUp(ctx, id)
			m.err = err
			return true
		case "retry":
			_, err := m.pendingInputQueue.Retry(ctx, id)
			m.err = err
			return true
		case "delete":
			m.err = m.pendingInputQueue.Cancel(ctx, id)
			return true
		case "side-chat":
			m.err = errors.New("side chat is available in WebUI; use /resume for a TUI fork")
			return true
		}
	}
	m.err = errors.New("usage: /queue on|off|list|up <id>|retry <id>|delete <id>|direction <id> <text>")
	return true
}

func idOrEmpty(value string) string { return strings.TrimSpace(value) }

func itoa(value int) string {
	return strconv.Itoa(value)
}
