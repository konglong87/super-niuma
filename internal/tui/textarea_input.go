// textarea_input.go 管理输入框本身：样式、取值、随内容伸缩的高度。

package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func configureTextareaStyle(input *textarea.Model) {
	visibleText := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	placeholder := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	endOfBuffer := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	input.FocusedStyle = textarea.Style{
		Base:        lipgloss.NewStyle(),
		CursorLine:  visibleText,
		EndOfBuffer: endOfBuffer,
		Placeholder: placeholder,
		Prompt:      prompt,
		Text:        visibleText,
	}
	input.BlurredStyle = input.FocusedStyle
	input.Cursor.TextStyle = visibleText
}

func (m *Model) clearInputBuffer() bool {
	if strings.TrimSpace(m.textarea.Value()) == "" && len(m.attachments) == 0 {
		return false
	}
	m.resetTextarea()
	m.attachments = nil
	m.clearDraft()
	m.clearSlashSuggestions()
	m.err = nil
	return true
}

func (m *Model) resetTextarea() {
	m.textarea.Reset()
	if m.resizeTextareaToContent() {
		m.refreshViewport()
	}
}

func (m *Model) setTextareaValue(value string) {
	m.textarea.SetValue(value)
	if m.resizeTextareaToContent() {
		m.refreshViewport()
	}
}

func (m *Model) resizeTextareaToContent() bool {
	oldHeight := m.textarea.Height()
	nextHeight := m.textareaContentHeight()
	m.textarea.SetHeight(nextHeight)
	if nextHeight > oldHeight {
		value := m.textarea.Value()
		m.textarea.SetValue(value)
		m.textarea.SetHeight(nextHeight)
	}
	return m.textarea.Height() != oldHeight
}

func (m Model) textareaContentHeight() int {
	value := m.textarea.Value()
	if value == "" {
		return 1
	}
	width := max(1, m.textareaContentWidth())
	height := 0
	for _, line := range strings.Split(value, "\n") {
		lineWidth := runewidth.StringWidth(line)
		if lineWidth == 0 {
			height++
			continue
		}
		height += max(1, (lineWidth+width-1)/width)
	}
	if current := m.textarea.LineInfo().Height; current > 0 {
		height = max(height, current)
	}
	return clamp(height, 1, m.maxTextareaHeight())
}

func (m Model) textareaContentWidth() int {
	width := m.textarea.Width() - runewidth.StringWidth(m.textarea.Prompt)
	if m.textarea.ShowLineNumbers {
		width -= 4
	}
	return max(1, width)
}

func (m Model) maxTextareaHeight() int {
	if m.height <= 0 {
		return 10000
	}
	// Keep at least one row for history and one row for the mode hint while
	// allowing long prompts to expand through the available terminal space.
	return max(1, m.height-4)
}
