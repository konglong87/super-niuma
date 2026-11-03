// message_render.go 渲染单条消息（用户/助手/recap）及其附带的工具视图。

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	thinkingStreamingHeader = "◌ Thinking"
	thinkingDoneHeader      = "◇ Thought"
	thinkingRail            = "│"
	configNoticePrefix      = "↻ Config" + configNoticeSeparator
)

func (m Model) renderMessage(msg message) string {
	return m.renderMessageWithToolView(msg, m.toolCallInlineView)
}

func (m Model) renderTranscriptMessage(msg message) string {
	return m.renderMessageWithToolView(msg, m.toolCallTranscriptView)
}

func (m Model) renderMessageWithToolView(msg message, toolView func([]toolActivityItem) string) string {
	content := strings.TrimSpace(msg.content)
	switch msg.role {
	case "user":
		rendered := m.renderUserMessage(content)
		if msg.meta != "" {
			rendered += "\n" + statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth()))
		}
		return rendered
	case "assistant":
		var b strings.Builder
		b.WriteString(m.renderAssistantMarker())
		if tools := toolView(msg.tools); tools != "" {
			b.WriteString("\n")
			b.WriteString(tools)
		}
		if content != "" {
			b.WriteString("\n")
			b.WriteString(m.renderMarkdown(content))
		}
		if msg.meta != "" {
			b.WriteString("\n")
			b.WriteString(statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth())))
		}
		if agents := m.messageAgentProgressView(msg.agents); agents != "" {
			b.WriteString("\n")
			b.WriteString(agents)
		}
		return b.String()
	case "error":
		return errorStyle.Render("错误") + "\n" + wrapForViewport(content, m.contentWidth())
	case "tool":
		return statusStyle.Render("Tool") + "\n" + wrapForViewport(content, m.contentWidth())
	case "agent":
		return statusStyle.Render("Sub-agent") + "\n" + wrapForViewport(content, m.contentWidth())
	case "thinking":
		return m.renderThinkingSegment(displaySegment{kind: displaySegmentThinking, role: "thinking", status: displaySegmentDone, content: content, turn: m.currentTurn, phase: 1}, false)
	case messageRoleConfigNotice:
		return renderConfigNoticeForWidth(content, m.contentWidth())
	case "status":
		return statusStyle.Render("Status") + "\n" + wrapForViewport(content, m.contentWidth())
	case "background":
		var b strings.Builder
		b.WriteString(statusStyle.Render("Background（后台任务）"))
		if msg.meta != "" {
			b.WriteString("\n")
			b.WriteString(statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth())))
		}
		if content != "" {
			b.WriteString("\n")
			b.WriteString(wrapForViewport(content, m.contentWidth()))
		}
		return b.String()
	case "loop":
		var b strings.Builder
		b.WriteString(statusStyle.Render("Loop"))
		if msg.meta != "" {
			b.WriteString("\n")
			b.WriteString(statusStyle.Render(wrapPrefixedLine("* ", msg.meta, m.contentWidth())))
		}
		if content != "" {
			b.WriteString("\n")
			b.WriteString(wrapForViewport(content, m.contentWidth()))
		}
		return b.String()
	case "recap":
		if content == "" {
			return ""
		}
		return renderRecapForWidth(content, m.contentWidth())
	default:
		return fmt.Sprintf("%s\n%s", msg.role, wrapForViewport(content, m.contentWidth()))
	}
}

func renderConfigNoticeForWidth(content string, width int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = configNoticeDefaultText
	}
	return statusStyle.Render(strings.Join(wrapLabeledDetailLines(configNoticePrefix, content, width), "\n"))
}

func (m Model) renderThinking(content string, state displaySegmentState, continuation bool) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	bodyWidth := max(1, m.contentWidth()-2)
	body := wrapForViewport(content, bodyWidth)
	if looksLikeMarkdown(content) {
		body = renderMarkdownForWidthWithStyle(content, bodyWidth, thinkingMarkdownStyle)
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = thinkingRailStyle.Render(thinkingRail) + " " + thinkingBodyStyle.Render(line)
	}
	if continuation {
		return strings.Join(lines, "\n")
	}
	header := thinkingDoneHeader
	if state == displaySegmentStreaming {
		header = thinkingStreamingHeader
	}
	return thinkingHeaderStyle.Render(header) + "\n" + strings.Join(lines, "\n")
}

func (m Model) renderUserMessage(content string) string {
	width := max(16, m.contentWidth())
	bodyWidth := max(1, width-2)
	wrapped := lipgloss.NewStyle().Width(bodyWidth).Render(strings.TrimSpace(content))
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		prefix := "  "
		if i == 0 {
			prefix = "› "
		}
		lines[i] = userStyle.Render(padDisplayLine(prefix+line, width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderAssistantMarker() string {
	return assistantMarkerStyle.Render("●") + " " + secondaryStyle.Render("niuma")
}

func renderRecapForWidth(content string, width int) string {
	content = compactRecapText(content)
	if content == "" {
		return ""
	}
	label := "※recap:"
	bodyWidth := max(20, width-len([]rune(label))-1)
	wrapped := wrapForViewport(content, bodyWidth)
	lines := strings.Split(wrapped, "\n")
	for i, line := range lines {
		if i == 0 {
			lines[i] = recapLabelStyle.Render(label) + " " + recapTextStyle.Render(line)
			continue
		}
		lines[i] = strings.Repeat(" ", len([]rune(label))+1) + recapTextStyle.Render(line)
	}
	return strings.Join(lines, "\n")
}

func compactRecapText(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, "  ")
}
