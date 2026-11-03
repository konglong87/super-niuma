// tool_activity.go 维护工具调用列表的生命周期并渲染工具卡片。

package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

func (m *Model) startToolActivity(event StreamEvent) {
	item := toolActivityItem{
		ToolName: firstNonEmpty(event.ToolName, event.ToolID, "tool"),
		ToolID:   event.ToolID,
		Status:   "running",
		Detail:   toolInputSummary(event.ToolName, event.Output),
		RawInput: strings.TrimSpace(event.Output),
		Started:  m.now(),
	}
	if index := m.findToolActivityIndex(event); index >= 0 {
		existing := m.toolActivity[index]
		if item.ToolID == "" {
			item.ToolID = existing.ToolID
		}
		if item.ToolName == "" {
			item.ToolName = existing.ToolName
		}
		if !existing.Started.IsZero() {
			item.Started = existing.Started
		}
		m.toolActivity[index] = item
		return
	}
	m.toolActivity = append(m.toolActivity, item)
	m.trimToolActivity()
}

func (m *Model) finishToolActivity(event StreamEvent) {
	now := m.now()
	index := m.findToolActivityIndex(event)
	if index < 0 {
		m.toolActivity = append(m.toolActivity, toolActivityItem{
			ToolName: firstNonEmpty(event.ToolName, event.ToolID, "tool"),
			ToolID:   event.ToolID,
			Started:  now,
		})
		index = len(m.toolActivity) - 1
	}
	item := m.toolActivity[index]
	item.ToolName = firstNonEmpty(event.ToolName, item.ToolName, event.ToolID, "tool")
	item.ToolID = firstNonEmpty(event.ToolID, item.ToolID)
	if input := strings.TrimSpace(event.Input); input != "" {
		item.RawInput = input
		if detail := toolInputSummary(item.ToolName, input); detail != "" {
			item.Detail = detail
		}
	}
	item.RawOutput = strings.TrimSpace(event.Output)
	item.IsError = event.IsError
	if event.IsError {
		if gate, ok := classifyToolGateBlock(event.Output); ok {
			item.Status = "blocked"
			item.GateRule = gate.Rule
			item.GateTitle = gate.Title
			item.GateReason = gate.Reason
			item.GateNextAction = gate.NextAction
			item.Result = gate.Summary
		} else {
			item.Status = "error"
			item.Result = toolResultSummaryForStatus(item.ToolName, event.Output, event.IsError)
		}
	} else {
		item.Status = "done"
		item.Result = toolResultSummaryForStatus(item.ToolName, event.Output, event.IsError)
	}
	if item.Started.IsZero() {
		item.Started = now
	}
	item.Elapsed = now.Sub(item.Started)
	if item.Result == "" {
		item.Result = toolResultSummaryForStatus(item.ToolName, event.Output, event.IsError)
	}
	if summary := recentAgentEvidenceSummary(item.ToolName, event.Output); summary != "" {
		m.recentAgentEvidence = summary
	}
	if item.Detail == "" {
		detail := toolInputSummary(item.ToolName, event.Input)
		if detail == "" {
			if strings.EqualFold(item.Status, "blocked") {
				detail = "命令未执行"
			} else {
				detail = strings.TrimSpace(event.Output)
			}
			if detail == "" {
				detail = "completed"
			}
			detail = truncate(oneLine(detail), max(24, m.width-44))
		}
		item.Detail = detail
	}
	m.toolActivity[index] = item
	m.trimToolActivity()
}

func (m Model) findToolActivityIndex(event StreamEvent) int {
	if event.ToolID != "" {
		for i := len(m.toolActivity) - 1; i >= 0; i-- {
			if m.toolActivity[i].ToolID == event.ToolID {
				return i
			}
		}
	}
	name := strings.TrimSpace(event.ToolName)
	if name == "" {
		return -1
	}
	for i := len(m.toolActivity) - 1; i >= 0; i-- {
		item := m.toolActivity[i]
		if strings.EqualFold(item.ToolName, name) && item.Status == "running" {
			return i
		}
	}
	return -1
}

func (m *Model) trimToolActivity() {
	if len(m.toolActivity) <= toolActivityStoreLimit {
		return
	}
	m.toolActivity = append([]toolActivityItem(nil), m.toolActivity[len(m.toolActivity)-toolActivityStoreLimit:]...)
}

func (m *Model) archiveToolActivityToAssistant() {
	if len(m.toolActivity) == 0 {
		return
	}
	archived := append([]toolActivityItem(nil), m.toolActivity...)
	if i, ok := m.latestUnprintedAssistantIndex(); ok {
		m.messages[i].tools = archived
		m.toolActivity = nil
		return
	}
	m.toolActivity = nil
}

func (m Model) latestUnprintedAssistantIndex() (int, bool) {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		if m.messages[i].role == "assistant" {
			return i, true
		}
	}
	return -1, false
}

func (m Model) toolCallInlineView(items []toolActivityItem) string {
	return m.toolCallView(items, toolCallViewOptions{
		expanded:    m.toolPanelExpanded,
		showDetails: m.toolPanelExpanded,
		interactive: true,
	})
}

func (m Model) toolCallTranscriptView(items []toolActivityItem) string {
	return m.toolCallView(items, toolCallViewOptions{
		expanded:    true,
		showDetails: false,
		interactive: false,
	})
}

type toolCallViewOptions struct {
	expanded    bool
	showDetails bool
	interactive bool
}

func (m Model) toolCallView(items []toolActivityItem, opts toolCallViewOptions) string {
	if len(items) == 0 {
		return ""
	}
	limit := 3
	if opts.expanded {
		limit = len(items)
	}
	start := max(0, len(items)-limit)
	visible := items[start:]

	var b strings.Builder
	width := max(24, m.width-8)

	for i, item := range visible {
		name := toolNameStyle(item.ToolName).Render(toolDisplayName(item.ToolName))
		icon := toolStatusIcon(item)
		if item.Status == "running" {
			icon = progressSpinnerFrame(m.now())
		}
		detail := toolDisplayDetail(item)
		statusLabel := toolStatusLabel(item)
		var meta []string
		if item.Status != "running" && item.Result != "" {
			meta = append(meta, toolResultPrefix(item)+friendlyToolResult(item.ToolName, item.Result))
		} else if statusLabel != "" {
			meta = append(meta, statusLabel)
		}
		if item.Status != "running" && item.Elapsed > 0 {
			meta = append(meta, "用时 "+friendlyElapsedDuration(item.Elapsed))
		}
		line := fmt.Sprintf("  %s %s", icon, name)
		if detail != "" {
			line += "  " + detail
		}
		if len(meta) > 0 {
			line += "  " + strings.Join(meta, "  ")
		}
		b.WriteString(statusStyle.Render(truncateDisplay(line, width)))
		if opts.showDetails {
			for _, detailLine := range toolActivityDetailLines(item, width-4) {
				b.WriteString("\n")
				b.WriteString(statusStyle.Render("    " + detailLine))
			}
		}
		if toolResultDisplayModeFor(item) == toolResultDisplayKeyOutput && item.Status != "running" {
			for _, outputLine := range toolKeyOutputLines(item, width-4, opts.interactive && opts.expanded) {
				b.WriteString("\n")
				b.WriteString(outputLine)
			}
		}
		if i < len(visible)-1 {
			b.WriteString("\n")
		}
	}

	hidden := len(items) - len(visible)
	if opts.interactive && hidden > 0 && !opts.expanded {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  ... 还有 %d 次工具操作  ctrl+t 展开", hidden)))
	} else if opts.interactive && opts.expanded && len(items) > 3 {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render("  ctrl+t 收起"))
	}

	return b.String()
}

func toolKeyOutputLines(item toolActivityItem, width int, expanded bool) []string {
	raw := strings.TrimSpace(item.RawOutput)
	if raw == "" {
		return nil
	}
	maxLines, maxChars := toolKeyOutputMaxLines, toolKeyOutputMaxChars
	if expanded {
		maxLines, maxChars = toolKeyOutputFullLines, toolKeyOutputFullChars
	}
	truncated := false
	if len(raw) > maxChars {
		raw = string([]rune(raw)[:min(maxChars, len([]rune(raw)))])
		truncated = true
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}
	out := []string{toolOutputLabelStyle.Render("    输出：")}
	for _, line := range lines {
		line = truncateDisplay(line, max(24, width-6))
		styled := toolOutputBodyStyle.Render("      " + line)
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			styled = toolOutputAddedStyle.Render("      " + line)
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			styled = toolOutputRemovedStyle.Render("      " + line)
		} else if strings.HasPrefix(line, "@@") {
			styled = toolOutputHunkStyle.Render("      " + line)
		}
		out = append(out, styled)
	}
	if truncated {
		out = append(out, toolOutputTruncatedStyle.Render("      … 输出较长，按 Ctrl+T 展开"))
	}
	return out
}

func toolActivityDetailLines(item toolActivityItem, width int) []string {
	width = max(24, width)
	var lines []string
	if strings.EqualFold(item.Status, "blocked") {
		if item.GateTitle != "" {
			lines = append(lines, friendlyLabeledDetailLines("拦截器", item.GateTitle, width)...)
		}
		if item.GateReason != "" {
			lines = append(lines, friendlyLabeledDetailLines("拦截原因", item.GateReason, width)...)
		}
		if item.GateNextAction != "" {
			lines = append(lines, friendlyLabeledDetailLines("下一步", item.GateNextAction, width)...)
		}
	} else if strings.EqualFold(item.Status, "interrupted") {
		lines = append(lines, friendlyLabeledDetailLines("中断原因", friendlyToolResult(item.ToolName, firstNonEmpty(item.Result, "本轮已中断")), width)...)
	} else if item.IsError {
		lines = append(lines, friendlyLabeledDetailLines("失败原因", friendlyToolResult(item.ToolName, firstNonEmpty(item.Result, errorResultSummary(item.RawOutput))), width)...)
	}
	if input := compactToolDetail(item.RawInput); input != "" {
		lines = append(lines, friendlyLabeledDetailLines("输入", input, width)...)
	}
	if item.Status != "running" && !strings.EqualFold(item.Status, "blocked") && toolResultDisplayModeFor(item) != toolResultDisplayKeyOutput {
		if output := compactToolDetail(item.RawOutput); output != "" {
			lines = append(lines, friendlyLabeledDetailLines("输出", output, width)...)
		}
	}
	return lines
}

func friendlyLabeledDetailLines(label, value string, width int) []string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	width = max(24, width)
	prefix := label + "："
	if prefixWidth := runewidth.StringWidth(prefix); prefixWidth < 8 {
		prefix += strings.Repeat(" ", 8-prefixWidth)
	}
	return wrapLabeledDetailLines(prefix, value, width)
}

func labeledDetailLines(label, value string, width int) []string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	width = max(24, width)
	prefix := fmt.Sprintf("%-7s ", label+":")
	return wrapLabeledDetailLines(prefix, value, width)
}

func wrapLabeledDetailLines(prefix, value string, width int) []string {
	available := max(8, width-runewidth.StringWidth(prefix))
	chunks := wrapDisplayChunks(value, available)
	lines := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i == 0 {
			lines = append(lines, prefix+chunk)
			continue
		}
		lines = append(lines, strings.Repeat(" ", runewidth.StringWidth(prefix))+chunk)
	}
	return lines
}

func wrapDisplayChunks(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	width = max(8, width)
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	var lines []string
	line := ""
	for _, word := range words {
		if runewidth.StringWidth(word) > width {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, splitDisplayChunk(word, width)...)
			continue
		}
		candidate := word
		if line != "" {
			candidate = line + " " + word
		}
		if runewidth.StringWidth(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func splitDisplayChunk(text string, width int) []string {
	var lines []string
	var b strings.Builder
	current := 0
	for _, r := range text {
		rw := runewidth.RuneWidth(r)
		if current > 0 && current+rw > width {
			lines = append(lines, b.String())
			b.Reset()
			current = 0
		}
		b.WriteRune(r)
		current += rw
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}

func compactToolDetail(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if summary, ok := gatePreflightSummary(raw); ok {
		return summary + "；原命令未执行，请根据检查结果决定是否继续"
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err == nil {
		return compactJSONValue(value)
	}
	return oneLine(raw)
}

func compactJSONValue(value any) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, min(4, len(keys)))
		for _, key := range keys {
			parts = append(parts, key+"="+compactJSONScalar(v[key]))
			if len(parts) == 4 {
				break
			}
		}
		if len(v) > len(parts) {
			parts = append(parts, fmt.Sprintf("+%d fields", len(v)-len(parts)))
		}
		return strings.Join(parts, " ")
	case []any:
		return fmt.Sprintf("%d item(s)", len(v))
	default:
		return compactJSONScalar(v)
	}
}

func compactJSONScalar(value any) string {
	switch v := value.(type) {
	case string:
		return truncate(oneLine(v), 80)
	case float64:
		if math.Trunc(v) == v {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		return fmt.Sprintf("%d item(s)", len(v))
	case map[string]any:
		return fmt.Sprintf("%d field(s)", len(v))
	case nil:
		return "null"
	default:
		return truncate(oneLine(fmt.Sprint(v)), 80)
	}
}
