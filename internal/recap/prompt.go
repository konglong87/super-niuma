package recap

import (
	"fmt"
	"strings"

	"github.com/konglong87/super-niuma/internal/session"
)

func BuildPrompt(entries []session.Entry, cfg Config) string {
	cfg = cfg.WithDefaults("")
	contextEntries := ContextEntries(entries, cfg)
	if len(contextEntries) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("你要为一个 niuma TUI 会话生成底部 recap。\n\n")
	out.WriteString("要求：\n")
	out.WriteString("- 只输出 recap 正文，不要寒暄。\n")
	out.WriteString("- 使用用户主要语言；中英混合时优先中文。\n")
	out.WriteString("- 写成 Claude Code 风格的会话状态提示，不要写成报告、日报或三段式模板。\n")
	out.WriteString("- 默认 1 段自然语言；复杂任务最多 2 行。\n")
	out.WriteString("- 说明当前正在做什么、已经推进到哪里、下一步最应该接什么。\n")
	out.WriteString("- 如果当前有阻塞、失败测试、未提交、等待用户动作或需要恢复工作区，优先写清楚。\n")
	out.WriteString("- 不要使用固定字段名，例如“本次会话目标：”“已完成：”“下一步：”，除非用户原文就是这样写的。\n")
	out.WriteString("- 不要编造未发生的文件修改、测试结果、提交或外部状态。\n")
	out.WriteString("- 不要输出敏感信息、API key、JWT、完整私有 URL。\n")
	out.WriteString("- 不要重复长代码。\n\n")
	out.WriteString("上下文如下：\n")
	for i, entry := range contextEntries {
		out.WriteString(fmt.Sprintf("\n### Entry %d (%s", i+1, entry.Type))
		if entry.Role != "" {
			out.WriteString("/" + entry.Role)
		}
		if entry.ToolName != "" {
			out.WriteString(" " + entry.ToolName)
		}
		out.WriteString(")\n")
		out.WriteString(formatEntry(entry, cfg))
		out.WriteString("\n")
	}
	return out.String()
}

func ContextEntries(entries []session.Entry, cfg Config) []session.Entry {
	cfg = cfg.WithDefaults("")
	filtered := make([]session.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == EntryType {
			continue
		}
		switch entry.Type {
		case "message", "tool_call", "tool_result":
			filtered = append(filtered, entry)
		case "compact_summary":
			if cfg.IncludeCompactSummary {
				filtered = append(filtered, entry)
			}
		}
	}
	if cfg.RecentMessageWindow > 0 && len(filtered) > cfg.RecentMessageWindow {
		filtered = filtered[len(filtered)-cfg.RecentMessageWindow:]
	}
	return filtered
}

func formatEntry(entry session.Entry, cfg Config) string {
	text := strings.TrimSpace(entry.Content)
	switch entry.Type {
	case "tool_call":
		text = "TOOL CALL " + entry.ToolName + ": " + text
	case "tool_result":
		prefix := "TOOL RESULT "
		if entry.IsError {
			prefix = "TOOL RESULT ERROR "
		}
		text = prefix + entry.ToolName + ": " + text
	case "compact_summary":
		text = "COMPACT SUMMARY:\n" + text
	}
	text = redactSensitive(text)
	if entry.Type == "tool_result" {
		text = truncateMiddle(text, cfg.ToolResultLimit)
	} else {
		text = truncateMiddle(text, 2400)
	}
	return text
}

func truncateMiddle(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	head := limit / 2
	tail := limit - head - len("\n...[truncated]...\n")
	if tail < 0 {
		return string(runes[:limit])
	}
	return string(runes[:head]) + "\n...[truncated]...\n" + string(runes[len(runes)-tail:])
}

func redactSensitive(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "api_key") ||
			strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "authorization: bearer") ||
			strings.Contains(lower, "password") ||
			strings.Contains(lower, "private key") ||
			strings.Contains(lower, "jwt") ||
			strings.Contains(lower, "token") {
			lines[i] = "[sensitive content redacted]"
		}
	}
	return strings.Join(lines, "\n")
}
