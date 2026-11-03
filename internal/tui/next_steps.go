// next_steps.go 展示「turn 结束后的下一步提示词候选」。候选是异步到达的，
// 所以可见性是一个纯展示条件（输入框为空且没有其它底部卡片在抢位），而不是
// 靠事件去销毁状态——用户清空输入框后候选会自然回来。

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) applyNextSteps(event StreamEvent) {
	m.nextSteps = nil
	if len(event.Payload) == 0 {
		return
	}
	var suggestions []string
	if err := json.Unmarshal(event.Payload, &suggestions); err != nil {
		return
	}
	for _, item := range suggestions {
		if item = strings.TrimSpace(item); item != "" {
			m.nextSteps = append(m.nextSteps, item)
		}
	}
}

func (m *Model) clearNextSteps() {
	m.nextSteps = nil
}

// nextStepsActive 同时驱动渲染与键位拦截：面板不可见时数字键必须是普通字符。
func (m Model) nextStepsActive() bool {
	if len(m.nextSteps) == 0 || m.busy {
		return false
	}
	if m.textarea.Value() != "" {
		return false
	}
	if m.pendingPermission != nil || m.pendingQuestion != nil {
		return false
	}
	if m.slashSuggestionsActive() || m.slashErr != nil {
		return false
	}
	return !m.resumePickerActive() && !m.rewindPickerActive()
}

// applyNextStepSuggestion 把第 idx 条填进输入框，返回是否命中。
func (m *Model) applyNextStepSuggestion(idx int) bool {
	if idx < 0 || idx >= len(m.nextSteps) {
		return false
	}
	suggestion := m.nextSteps[idx]
	m.clearNextSteps()
	m.setTextareaValue(suggestion)
	return true
}

// runNextStepsCmd 在一轮结束后请求下一步候选。它**自己持有** channel：主 stream
// channel 在 StreamFinished 之后既不再被读取（update.go 不再续读），也已被生产者
// 的 defer close 关闭，异步结果送不进去，往已关闭的 channel 发送还会 panic。
// away recap（background.go:137）用的就是这个自持 channel 的模式。
func (m Model) runNextStepsCmd(prompt string, result QueryResult) tea.Cmd {
	if m.runNextSteps == nil {
		return nil
	}
	return func() tea.Msg {
		events := make(chan StreamEvent, 4)
		err := m.runNextSteps(context.Background(), prompt, result, events)
		close(events)
		var latest StreamEvent
		for event := range events {
			if event.Type == StreamNextSteps {
				latest = event
			}
		}
		return nextStepsMsg{event: latest, err: err}
	}
}

func (m Model) nextStepsView() string {
	if !m.nextStepsActive() {
		return ""
	}
	var b strings.Builder
	b.WriteString(statusStyle.Render("Next steps  press 1-" + fmt.Sprint(len(m.nextSteps)) + " to use  esc to dismiss"))
	for i, suggestion := range m.nextSteps {
		b.WriteString("\n")
		line := fmt.Sprintf("  %d. %s", i+1, truncate(oneLine(suggestion), max(20, m.width-8)))
		b.WriteString(statusStyle.Render(line))
	}
	return b.String()
}
