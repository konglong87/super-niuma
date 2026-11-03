// slash.go 处理 slash 命令的补全建议与裸命令归一化。

package tui

import (
	"fmt"
	"strings"
)

func (m *Model) updateSlashSuggestions() {
	if m.busy || m.pendingPermission != nil || m.pendingQuestion != nil || m.slashCommands == nil {
		m.clearSlashSuggestions()
		return
	}
	prefix, ok := slashCommandPrefix(m.textarea.Value())
	if !ok {
		m.clearSlashSuggestions()
		return
	}
	items, err := m.slashCommands(m.ctx, prefix)
	m.slashErr = err
	if err != nil {
		m.slashSuggestions = nil
		m.slashSelected = 0
		return
	}
	m.slashSuggestions = items
	if len(m.slashSuggestions) == 0 {
		m.slashSelected = 0
		return
	}
	if m.slashSelected >= len(m.slashSuggestions) {
		m.slashSelected = len(m.slashSuggestions) - 1
	}
	if m.slashSelected < 0 {
		m.slashSelected = 0
	}
}

func slashCommandPrefix(value string) (string, bool) {
	value = strings.TrimLeft(value, " \t")
	if !strings.HasPrefix(value, "/") {
		return "", false
	}
	value = strings.TrimPrefix(value, "/")
	if strings.ContainsAny(value, " \t\r\n") {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(value)), true
}

func normalizeBareInteractiveCommand(input string) string {
	trimmed := strings.TrimSpace(input)
	switch {
	case strings.EqualFold(trimmed, "exit"):
		return "/exit"
	case trimmed == "退出":
		return "/exit"
	case strings.EqualFold(trimmed, "quit"):
		return "/quit"
	case strings.EqualFold(trimmed, "compact"):
		return "/compact"
	case trimmed == "压缩":
		return "/compact"
	default:
		return trimmed
	}
}

func (m *Model) clearSlashSuggestions() {
	m.slashSuggestions = nil
	m.slashSelected = 0
	m.slashErr = nil
}

func (m Model) slashSuggestionsActive() bool {
	return len(m.slashSuggestions) > 0
}

func (m *Model) moveSlashSelection(delta int) {
	if len(m.slashSuggestions) == 0 {
		return
	}
	m.slashSelected = (m.slashSelected + delta + len(m.slashSuggestions)) % len(m.slashSuggestions)
}

func (m Model) shouldCompleteSlashOnEnter() bool {
	prefix, ok := slashCommandPrefix(m.textarea.Value())
	if !ok || len(m.slashSuggestions) == 0 {
		return false
	}
	selected := m.slashSuggestions[m.slashSelected].Name
	if selected == "" {
		return false
	}
	return prefix == "" || !strings.EqualFold(prefix, strings.TrimPrefix(selected, "/"))
}

func (m *Model) applySlashSuggestion() {
	if len(m.slashSuggestions) == 0 {
		return
	}
	name := strings.TrimSpace(m.slashSuggestions[m.slashSelected].Name)
	if name == "" {
		return
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	m.setTextareaValue(name + " ")
	m.clearSlashSuggestions()
}

func (m Model) slashSuggestionView() string {
	if m.slashErr != nil {
		return errorStyle.Render("Slash commands unavailable: " + m.slashErr.Error())
	}
	if len(m.slashSuggestions) == 0 {
		return ""
	}
	limit := min(8, len(m.slashSuggestions))
	start := 0
	if m.slashSelected >= limit {
		start = m.slashSelected - limit + 1
	}
	end := min(len(m.slashSuggestions), start+limit)
	var b strings.Builder
	b.WriteString(statusStyle.Render("Slash commands  up/down select  tab complete  enter run"))
	if start > 0 {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ... %d before", start)))
	}
	for i := start; i < end; i++ {
		item := m.slashSuggestions[i]
		b.WriteString("\n")
		name := strings.TrimSpace(item.Name)
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		line := "  " + name
		if item.Description != "" {
			line += "  " + truncate(oneLine(item.Description), max(20, m.width-24))
		}
		if item.Source != "" {
			line += "  [" + item.Source + "]"
		}
		if i == m.slashSelected {
			b.WriteString(slashSelectedStyle.Render("▶ " + line))
		} else {
			b.WriteString(statusStyle.Render(line))
		}
	}
	if end < len(m.slashSuggestions) {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ... %d more", len(m.slashSuggestions)-end)))
	}
	return b.String()
}
