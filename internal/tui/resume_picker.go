// resume_picker.go 是 /resume 的会话选择弹窗。

package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func isBareResumePrompt(prompt string) bool {
	fields := strings.Fields(strings.TrimSpace(prompt))
	return len(fields) == 1 && strings.EqualFold(fields[0], "/resume")
}

func (m Model) openResumePicker() (tea.Model, tea.Cmd) {
	if m.resumeSessions == nil {
		return m, nil
	}
	items, err := m.resumeSessions(m.ctx)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.clearSlashSuggestions()
	m.saveDraft()
	m.attachments = nil
	m.resumePicker = items
	m.resumeSelected = 0
	m.resumeOffset = 0
	m.resumeLastClickIndex = -1
	m.resumeLastClickAt = time.Time{}
	m.resetTextarea()
	if len(items) == 0 {
		m.err = errors.New("no sessions found")
		m.resumePicker = nil
		return m, nil
	}
	m.err = nil
	if !m.mouseTracking {
		m.mouseTracking = true
		m.resumePickerEnabledMouse = true
		return m, tea.EnableMouseCellMotion
	}
	return m, nil
}

func (m Model) resumePickerActive() bool {
	return len(m.resumePicker) > 0
}

func (m Model) handleResumePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.closeResumePicker()
	case "up", "ctrl+p":
		m.moveResumeSelection(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveResumeSelection(1)
		return m, nil
	case "pgup":
		m.moveResumeSelection(-m.resumePickerVisibleLimit())
		return m, nil
	case "pgdown":
		m.moveResumeSelection(m.resumePickerVisibleLimit())
		return m, nil
	case "home":
		m.resumeSelected = 0
		m.ensureResumeSelectionVisible()
		return m, nil
	case "end":
		m.resumeSelected = len(m.resumePicker) - 1
		m.ensureResumeSelectionVisible()
		return m, nil
	case "enter":
		return m.submitSelectedResumeSession()
	default:
		return m, nil
	}
}

func (m Model) handleResumePickerMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.MouseWheelUp:
		m.moveResumeSelection(-1)
		return m, nil
	case tea.MouseWheelDown:
		m.moveResumeSelection(1)
		return m, nil
	case tea.MouseLeft:
		row, ok := m.resumePickerRowIndex(msg.Y)
		if !ok {
			return m, nil
		}
		m.resumeSelected = row
		m.ensureResumeSelectionVisible()
		now := m.now()
		if m.resumeLastClickIndex == row && !m.resumeLastClickAt.IsZero() && now.Sub(m.resumeLastClickAt) <= 500*time.Millisecond {
			return m.submitSelectedResumeSession()
		}
		m.resumeLastClickIndex = row
		m.resumeLastClickAt = now
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) moveResumeSelection(delta int) {
	if len(m.resumePicker) == 0 {
		return
	}
	m.resumeSelected = clamp(m.resumeSelected+delta, 0, len(m.resumePicker)-1)
	m.ensureResumeSelectionVisible()
}

func (m *Model) ensureResumeSelectionVisible() {
	limit := m.resumePickerVisibleLimit()
	if m.resumeSelected < m.resumeOffset {
		m.resumeOffset = m.resumeSelected
	}
	if m.resumeSelected >= m.resumeOffset+limit {
		m.resumeOffset = m.resumeSelected - limit + 1
	}
	maxOffset := max(0, len(m.resumePicker)-limit)
	m.resumeOffset = clamp(m.resumeOffset, 0, maxOffset)
}

func (m Model) resumePickerVisibleLimit() int {
	return max(1, min(8, len(m.resumePicker)))
}

func (m Model) resumePickerRowIndex(y int) (int, bool) {
	start := m.resumePickerStartY()
	if start < 0 || y < start {
		return 0, false
	}
	relative := y - start
	if relative%2 != 0 {
		return 0, false
	}
	index := m.resumeOffset + relative/2
	if index < 0 || index >= len(m.resumePicker) || index >= m.resumeOffset+m.resumePickerVisibleLimit() {
		return 0, false
	}
	return index, true
}

func (m Model) resumePickerStartY() int {
	before := []string{}
	if live := m.liveTranscriptView(); live != "" {
		before = append(before, live)
	}
	if todos := m.todoProgressView(); todos != "" {
		before = append(before, todos)
	}
	if usage := m.usageView(); usage != "" {
		before = append(before, usage)
	}
	if suggestions := m.slashSuggestionView(); suggestions != "" {
		before = append(before, suggestions)
	}
	return visualLineCount(strings.Join(before, "\n"), m.contentWidth()) + 1
}

func (m Model) submitSelectedResumeSession() (tea.Model, tea.Cmd) {
	if len(m.resumePicker) == 0 || m.resumeSelected < 0 || m.resumeSelected >= len(m.resumePicker) {
		return m, nil
	}
	selected := m.resumePicker[m.resumeSelected]
	prompt := "/resume " + selected.ID
	m.resumePicker = nil
	m.resumeSelected = 0
	m.resumeOffset = 0
	m.resumeLastClickIndex = -1
	m.resumeLastClickAt = time.Time{}
	m.setTextareaValue(prompt)
	if m.resumePickerEnabledMouse {
		m.resumePickerEnabledMouse = false
		m.mouseTracking = false
		updated, submitCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return updated, tea.Batch(tea.DisableMouse, submitCmd)
	}
	return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func (m Model) closeResumePicker() (tea.Model, tea.Cmd) {
	m.resumePicker = nil
	m.resumeSelected = 0
	m.resumeOffset = 0
	m.resumeLastClickIndex = -1
	m.resumeLastClickAt = time.Time{}
	if m.resumePickerEnabledMouse {
		m.resumePickerEnabledMouse = false
		m.mouseTracking = false
		return m, tea.DisableMouse
	}
	return m, nil
}

func (m Model) resumePickerView() string {
	if len(m.resumePicker) == 0 {
		return ""
	}
	m.ensureResumeSelectionVisible()
	limit := m.resumePickerVisibleLimit()
	end := min(len(m.resumePicker), m.resumeOffset+limit)
	var b strings.Builder
	header := fmt.Sprintf("Resume sessions  %d/%d  up/down select  enter open  wheel scroll  double-click open", m.resumeSelected+1, len(m.resumePicker))
	b.WriteString(statusStyle.Render(header))
	for i := m.resumeOffset; i < end; i++ {
		item := m.resumePicker[i]
		b.WriteString("\n")
		prefix := "  "
		if i == m.resumeSelected {
			prefix = "> "
		}
		title := firstNonEmpty(strings.TrimSpace(item.Title), strings.TrimSpace(item.Preview), item.ID)
		updated := ""
		if !item.Updated.IsZero() {
			updated = item.Updated.Format("2006-01-02 15:04")
		}
		line := strings.TrimSpace(strings.Join([]string{
			truncate(oneLine(title), max(20, m.width-32)),
			statusStyle.Render(updated),
		}, "  "))
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
		b.WriteString("    ")
		meta := item.ID
		if strings.TrimSpace(item.CWD) != "" {
			meta += "  " + abbreviateHome(item.CWD)
		}
		if strings.TrimSpace(item.Preview) != "" && !strings.EqualFold(strings.TrimSpace(item.Preview), strings.TrimSpace(title)) {
			meta += "  " + truncate(oneLine(item.Preview), max(20, m.width-12-len([]rune(item.ID))))
		}
		b.WriteString(statusStyle.Render(truncate(meta, max(20, m.width-4))))
	}
	if end < len(m.resumePicker) {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ... %d more", len(m.resumePicker)-end)))
	}
	return b.String()
}
