// rewind_picker.go 是 /rewind 与 /checkpoint 的检查点选择弹窗。

package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func isBareRewindPrompt(prompt string) bool {
	fields := strings.Fields(strings.TrimSpace(prompt))
	return len(fields) == 1 && (strings.EqualFold(fields[0], "/rewind") || strings.EqualFold(fields[0], "/checkpoint"))
}

func rewindCommandName(prompt string) string {
	fields := strings.Fields(strings.TrimSpace(prompt))
	if len(fields) == 0 {
		return "rewind"
	}
	name := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	if name == "checkpoint" {
		return "checkpoint"
	}
	return "rewind"
}

func (m Model) openRewindPicker(prompt string) (tea.Model, tea.Cmd) {
	if m.rewindCandidates == nil {
		return m, nil
	}
	items, err := m.rewindCandidates(m.ctx)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.clearSlashSuggestions()
	m.saveDraft()
	m.attachments = nil
	m.rewindPicker = items
	m.rewindSelected = 0
	m.rewindOffset = 0
	m.rewindLastClickIndex = -1
	m.rewindLastClickAt = time.Time{}
	m.rewindMode = rewindCommandName(prompt)
	m.resetTextarea()
	if len(items) == 0 {
		m.err = errors.New("nothing to rewind to yet")
		m.rewindPicker = nil
		return m, nil
	}
	m.err = nil
	return m, nil
}

func (m Model) rewindPickerActive() bool {
	return len(m.rewindPicker) > 0
}

func (m Model) handleRewindPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		return m.closeRewindPicker(), nil
	case "up", "ctrl+p":
		m.moveRewindSelection(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveRewindSelection(1)
		return m, nil
	case "pgup":
		m.moveRewindSelection(-m.rewindPickerVisibleLimit())
		return m, nil
	case "pgdown":
		m.moveRewindSelection(m.rewindPickerVisibleLimit())
		return m, nil
	case "home":
		m.rewindSelected = 0
		m.ensureRewindSelectionVisible()
		return m, nil
	case "end":
		m.rewindSelected = len(m.rewindPicker) - 1
		m.ensureRewindSelectionVisible()
		return m, nil
	case "enter":
		return m.submitSelectedRewindCandidate()
	default:
		return m, nil
	}
}

func (m Model) handleRewindPickerMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.MouseWheelUp:
		m.moveRewindSelection(-1)
		return m, nil
	case tea.MouseWheelDown:
		m.moveRewindSelection(1)
		return m, nil
	case tea.MouseLeft:
		row, ok := m.rewindPickerRowIndex(msg.Y)
		if !ok {
			return m, nil
		}
		m.rewindSelected = row
		m.ensureRewindSelectionVisible()
		now := m.now()
		if m.rewindLastClickIndex == row && !m.rewindLastClickAt.IsZero() && now.Sub(m.rewindLastClickAt) <= 500*time.Millisecond {
			return m.submitSelectedRewindCandidate()
		}
		m.rewindLastClickIndex = row
		m.rewindLastClickAt = now
		return m, nil
	default:
		return m, nil
	}
}

func (m *Model) moveRewindSelection(delta int) {
	if len(m.rewindPicker) == 0 {
		return
	}
	m.rewindSelected = clamp(m.rewindSelected+delta, 0, len(m.rewindPicker)-1)
	m.ensureRewindSelectionVisible()
}

func (m *Model) ensureRewindSelectionVisible() {
	limit := m.rewindPickerVisibleLimit()
	if m.rewindSelected < m.rewindOffset {
		m.rewindOffset = m.rewindSelected
	}
	if m.rewindSelected >= m.rewindOffset+limit {
		m.rewindOffset = m.rewindSelected - limit + 1
	}
	maxOffset := max(0, len(m.rewindPicker)-limit)
	m.rewindOffset = clamp(m.rewindOffset, 0, maxOffset)
}

func (m Model) rewindPickerVisibleLimit() int {
	return max(1, min(8, len(m.rewindPicker)))
}

func (m Model) rewindPickerRowIndex(y int) (int, bool) {
	start := m.rewindPickerStartY()
	if start < 0 || y < start {
		return 0, false
	}
	relative := y - start
	if relative%2 != 0 {
		return 0, false
	}
	index := m.rewindOffset + relative/2
	if index < 0 || index >= len(m.rewindPicker) || index >= m.rewindOffset+m.rewindPickerVisibleLimit() {
		return 0, false
	}
	return index, true
}

func (m Model) rewindPickerStartY() int {
	before := []string{}
	if live := m.liveTranscriptView(); live != "" {
		before = append(before, live)
	}
	if usage := m.usageView(); usage != "" {
		before = append(before, usage)
	}
	if suggestions := m.slashSuggestionView(); suggestions != "" {
		before = append(before, suggestions)
	}
	if picker := m.resumePickerView(); picker != "" {
		before = append(before, picker)
	}
	return visualLineCount(strings.Join(before, "\n"), m.contentWidth()) + 1
}

func (m Model) submitSelectedRewindCandidate() (tea.Model, tea.Cmd) {
	if len(m.rewindPicker) == 0 || m.rewindSelected < 0 || m.rewindSelected >= len(m.rewindPicker) {
		return m, nil
	}
	selected := m.rewindPicker[m.rewindSelected]
	command := firstNonEmpty(strings.TrimSpace(m.rewindMode), "rewind")
	prompt := "/" + command + " " + selected.ID
	m = m.closeRewindPicker()
	m.setTextareaValue(prompt)
	return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func (m Model) closeRewindPicker() Model {
	m.rewindPicker = nil
	m.rewindSelected = 0
	m.rewindOffset = 0
	m.rewindLastClickIndex = -1
	m.rewindLastClickAt = time.Time{}
	m.rewindMode = ""
	return m
}

func (m Model) rewindPickerView() string {
	if len(m.rewindPicker) == 0 {
		return ""
	}
	m.ensureRewindSelectionVisible()
	limit := m.rewindPickerVisibleLimit()
	end := min(len(m.rewindPicker), m.rewindOffset+limit)
	var b strings.Builder
	header := fmt.Sprintf("Rewind messages  %d/%d  up/down select  enter restore  esc cancel", m.rewindSelected+1, len(m.rewindPicker))
	b.WriteString(statusStyle.Render(header))
	for i := m.rewindOffset; i < end; i++ {
		item := m.rewindPicker[i]
		b.WriteString("\n")
		prefix := "  "
		if i == m.rewindSelected {
			prefix = "> "
		}
		b.WriteString(prefix)
		b.WriteString(truncate(oneLine(firstNonEmpty(strings.TrimSpace(item.Preview), item.ID)), max(20, m.width-4)))
		b.WriteString("\n")
		b.WriteString("    ")
		meta := item.ID
		if strings.TrimSpace(item.Mode) != "" {
			meta += "  " + strings.TrimSpace(item.Mode)
		}
		b.WriteString(statusStyle.Render(truncate(meta, max(20, m.width-4))))
	}
	if end < len(m.rewindPicker) {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ... %d more", len(m.rewindPicker)-end)))
	}
	return b.String()
}
