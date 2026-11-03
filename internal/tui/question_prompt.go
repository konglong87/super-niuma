// question_prompt.go 是 AskUserQuestion 弹窗（带常驻输入框的自由作答）。

package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type pendingUserQuestion struct {
	request  UserQuestionRequest
	reply    chan UserQuestionAnswer
	ch       <-chan StreamEvent
	selected int
	textBuf  string
}

// questionInputRow is the index of the always-visible free-text input row,
// rendered after the last choice.
func (p pendingUserQuestion) questionInputRow() int {
	return len(p.request.Choices)
}

func (m Model) handleQuestionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	inputRow := pending.questionInputRow()
	rowCount := inputRow + 1
	onInput := pending.selected == inputRow
	switch msg.Type {
	case tea.KeyBackspace:
		if onInput {
			if runes := []rune(pending.textBuf); len(runes) > 0 {
				pending.textBuf = string(runes[:len(runes)-1])
			}
			m.pendingQuestion = pending
			m.refreshViewport()
		}
		return m, nil
	case tea.KeySpace:
		if onInput {
			pending.textBuf += " "
			m.pendingQuestion = pending
			m.refreshViewport()
		}
		return m, nil
	case tea.KeyRunes:
		if !onInput {
			key := strings.ToLower(msg.String())
			if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
				if idx := int(key[0] - '1'); idx < rowCount {
					return m.confirmQuestionSelection(idx)
				}
				return m, nil
			}
			pending.selected = inputRow
		}
		pending.textBuf += string(msg.Runes)
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	}
	switch strings.ToLower(msg.String()) {
	case "up", "ctrl+p", "shift+tab":
		pending.selected = wrapIndex(pending.selected-1, rowCount)
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	case "down", "ctrl+n", "tab":
		pending.selected = wrapIndex(pending.selected+1, rowCount)
		m.pendingQuestion = pending
		m.refreshViewport()
		return m, nil
	case "esc":
		if onInput && pending.textBuf != "" {
			pending.textBuf = ""
			m.pendingQuestion = pending
			m.refreshViewport()
			return m, nil
		}
		return m.resolveQuestion(UserQuestionAnswer{})
	case "enter":
		return m.confirmQuestionSelection(pending.selected)
	}
	return m, nil
}

func (m Model) confirmQuestionSelection(idx int) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	if idx < len(pending.request.Choices) {
		return m.resolveQuestion(UserQuestionAnswer{Answered: true, Answer: pending.request.Choices[idx]})
	}
	if answer := strings.TrimSpace(pending.textBuf); answer != "" {
		return m.resolveQuestion(UserQuestionAnswer{Answered: true, Answer: answer})
	}
	pending.selected = idx
	m.pendingQuestion = pending
	m.refreshViewport()
	return m, nil
}

func (m Model) resolveQuestion(answer UserQuestionAnswer) (tea.Model, tea.Cmd) {
	pending := m.pendingQuestion
	if pending == nil {
		return m, nil
	}
	pending.reply <- answer
	m.pendingQuestion = nil
	m.refreshViewport()
	return m, waitForStreamEvent(pending.ch)
}

func questionPromptView(p pendingUserQuestion, width int) string {
	var b strings.Builder
	width = max(24, width)
	b.WriteString(titleStyle.Render("需要你的回答"))
	b.WriteString(statusStyle.Render(truncateDisplay(" · AskUserQuestion", max(4, width-runewidth.StringWidth("需要你的回答")))))
	b.WriteString("\n")
	for _, line := range labeledDetailLines("问题", p.request.Question, width) {
		b.WriteString("\n")
		b.WriteString(line)
	}
	b.WriteString("\n")
	for i, choice := range p.request.Choices {
		b.WriteString("\n")
		if i == p.selected {
			b.WriteString(permissionSelectedStyle.Render(truncateDisplay(fmt.Sprintf("[ ▶ %d. %s ]", i+1, choice), width)))
		} else {
			b.WriteString(truncateDisplay(fmt.Sprintf("  %d. %s", i+1, choice), width))
		}
	}
	inputRow := p.questionInputRow()
	b.WriteString("\n")
	if p.selected == inputRow {
		b.WriteString(permissionSelectedStyle.Render(truncateDisplay(fmt.Sprintf("[ ▶ %d. 输入: %s▌ ]", inputRow+1, p.textBuf), width)))
	} else {
		b.WriteString(truncateDisplay(fmt.Sprintf("  %d. 输入: %s", inputRow+1, p.textBuf), width))
	}
	b.WriteString("\n\n")
	hint := "↑/↓ 选择 · 1-9 快选 · 打字即输入 · enter 确认 · esc 取消"
	if len(p.request.Choices) == 0 {
		hint = "打字输入 · enter 确认 · esc 取消"
	}
	b.WriteString(statusStyle.Render(hint))
	return b.String()
}
