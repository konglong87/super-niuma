// todo.go 维护 TodoWrite 的待办状态并渲染待办进度。

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/konglong87/super-niuma/internal/identity"
)

type todoItem struct {
	ID         string
	Content    string
	ActiveForm string
	Status     string
	Priority   string
}

func (m Model) todoProgressView() string {
	if len(m.todos) == 0 {
		return ""
	}
	completed := 0
	pending := 0
	inProgress := 0
	for _, item := range m.todos {
		switch item.Status {
		case "completed":
			completed++
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		}
	}
	if m.todosLoadedFromDisk && completed == len(m.todos) {
		return ""
	}
	if completed == len(m.todos) {
		return ""
	}
	if m.shouldShowWelcome() && !m.busy && completed == len(m.todos) {
		return ""
	}
	budget := m.renderBudget()
	if !m.todoPanelExpanded && budget.MaxChrome <= 5 {
		return ""
	}
	title := fmt.Sprintf("Tasks %d/%d", completed, len(m.todos))
	if m.todoPanelExpanded {
		title += " expanded  ctrl+y collapse"
	} else if len(m.todos) > todoCollapsedDefaultLimit {
		title += "  ctrl+y expand"
	}
	title += fmt.Sprintf("  active=%d pending=%d", inProgress, pending)
	if !m.todoPanelExpanded && budget.MaxChrome <= 7 {
		return statusStyle.Render(truncateDisplay(title, budget.ChromeWidth))
	}
	var b strings.Builder
	b.WriteString(statusStyle.Render(truncateDisplay(title, budget.ChromeWidth)))
	visible, hidden := m.visibleTodos()
	for _, item := range visible {
		b.WriteString("\n")
		b.WriteString(m.renderTodoItem(item))
	}
	if !m.todoPanelExpanded && hidden > 0 {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  还有 %d 个，ctrl+y 展开", hidden)))
	}
	return b.String()
}

func (m Model) visibleTodos() ([]todoItem, int) {
	if m.todoPanelExpanded {
		return append([]todoItem(nil), m.todos...), 0
	}
	if len(m.todos) <= todoCollapsedDefaultLimit {
		return append([]todoItem(nil), m.todos...), 0
	}
	limit := min(todoCollapsedDefaultLimit, len(m.todos))
	current := 0
	for i, item := range m.todos {
		if item.Status == "in_progress" {
			current = i
			break
		}
	}
	start := current - limit/2
	if start < 0 {
		start = 0
	}
	if maxStart := len(m.todos) - limit; start > maxStart {
		start = maxStart
	}
	end := min(len(m.todos), start+limit)
	out := make([]todoItem, 0, end-start)
	for _, item := range m.todos[start:end] {
		out = append(out, item)
	}
	return out, len(m.todos) - len(out)
}

func (m Model) renderTodoItem(item todoItem) string {
	icon := m.todoStatusIcon(item.Status)
	content := truncate(oneLine(todoDisplayText(item)), max(24, m.width-14))
	line := "  " + icon + " " + content
	if item.Priority != "" && item.Priority != "medium" {
		line += "  " + item.Priority
	}
	if item.Status == "in_progress" {
		if !m.busy && m.err != nil {
			return errorStyle.Render(line)
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Render(line)
	}
	return statusStyle.Render(line)
}

func (m Model) todoStatusIcon(status string) string {
	switch status {
	case "pending":
		return "☐"
	case "in_progress":
		if m.busy {
			return progressSpinnerFrame(m.now())
		}
		if m.err != nil {
			return "!"
		}
		return "›"
	case "completed":
		return "✓"
	default:
		return "•"
	}
}

func (m *Model) syncTodosFromToolEvent(event StreamEvent) {
	if event.IsError {
		return
	}
	switch event.ToolName {
	case "TodoWrite":
		m.todosLoadedFromDisk = false
		if m.updateTodosFromJSON(event.Input) {
			return
		}
	case "TodoRead":
		m.todosLoadedFromDisk = false
		if m.updateTodosFromJSON(event.Output) {
		}
	}
}

func (m *Model) refreshRunningStatusFromCurrentTodo() {
	if current := m.currentTodo(); current != nil {
		m.runningStatus = "Task: " + truncate(oneLine(todoDisplayText(*current)), max(24, m.width-18))
	}
}

func (m *Model) updateTodosFromJSON(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	var wrapped struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapped); err == nil && wrapped.Todos != nil {
		m.setTodos(wrapped.Todos)
		return true
	}
	var list []todoItem
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		m.setTodos(list)
		return true
	}
	return false
}

func (m *Model) setTodos(items []todoItem) {
	previousAllCompleted := len(m.todos) > 0 && allTodosCompleted(m.todos)
	out := make([]todoItem, 0, len(items))
	for i, item := range items {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			item.ID = fmt.Sprintf("todo-%d", i+1)
		}
		item.Content = strings.TrimSpace(item.Content)
		item.ActiveForm = strings.TrimSpace(item.ActiveForm)
		item.Status = normalizeTodoStatus(item.Status)
		item.Priority = normalizeTodoPriority(item.Priority)
		if item.Content == "" {
			continue
		}
		out = append(out, item)
	}
	m.todos = out
	if len(out) == 0 || !allTodosCompleted(out) {
		m.todoCompletionArchiveKey = ""
		return
	}
	if m.todosLoadedFromDisk {
		return
	}
	key := todoCompletionKey(out)
	if previousAllCompleted && m.todoCompletionArchiveKey == key {
		return
	}
	if m.todoCompletionArchiveKey == key {
		return
	}
	m.todoCompletionArchiveKey = key
	m.appendTodoCompletionDisplaySegment(out)
}

func allTodosCompleted(items []todoItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if normalizeTodoStatus(item.Status) != "completed" {
			return false
		}
	}
	return true
}

func todoCompletionKey(items []todoItem) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(strings.TrimSpace(item.ID))
		b.WriteByte('\x00')
		b.WriteString(strings.TrimSpace(item.Content))
		b.WriteByte('\x00')
	}
	return b.String()
}

func (m *Model) appendTodoCompletionDisplaySegment(items []todoItem) {
	content := todoCompletionTranscriptText(items)
	if strings.TrimSpace(content) == "" {
		return
	}
	m.closeDisplayTextAppendWindow()
	m.appendDisplaySegment(displaySegment{
		kind:    displaySegmentTodo,
		role:    "status",
		status:  displaySegmentDone,
		content: content,
	})
}

func todoCompletionTranscriptText(items []todoItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✓ 本轮任务完成：%d 项全部完成", len(items))
	limit := min(len(items), 8)
	for i := 0; i < limit; i++ {
		text := strings.TrimSpace(todoDisplayText(items[i]))
		if text == "" {
			continue
		}
		b.WriteString("\n  ✓ ")
		b.WriteString(text)
	}
	if hidden := len(items) - limit; hidden > 0 {
		fmt.Fprintf(&b, "\n  还有 %d 项已完成", hidden)
	}
	return b.String()
}

func todoDisplayText(item todoItem) string {
	if normalizeTodoStatus(item.Status) == "in_progress" && strings.TrimSpace(item.ActiveForm) != "" {
		return item.ActiveForm
	}
	return item.Content
}

func normalizeTodoStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "in_progress", "completed":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "pending"
	}
}

func normalizeTodoPriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(priority))
	default:
		return ""
	}
}

func (m Model) currentTodo() *todoItem {
	for i := range m.todos {
		if m.todos[i].Status == "in_progress" {
			return &m.todos[i]
		}
	}
	return nil
}

func (m Model) hasIncompleteTodos() bool {
	for _, item := range m.todos {
		if normalizeTodoStatus(item.Status) != "completed" {
			return true
		}
	}
	return false
}

func (m *Model) loadTodosFromDisk() {
	cwd := strings.TrimSpace(m.welcome.CWD)
	if cwd == "" {
		return
	}
	data, err := os.ReadFile(identity.Default().ProjectStatePath(cwd, "todos.json"))
	if err != nil {
		return
	}
	m.todosLoadedFromDisk = true
	if !m.updateTodosFromJSON(string(data)) {
		m.todosLoadedFromDisk = false
	}
}
