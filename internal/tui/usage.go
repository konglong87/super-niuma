// usage.go 渲染 token / 成本 / 上下文用量面板，以及流错误的友好文案。

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

type usagePanel struct {
	Model         string
	StopReason    string
	Turns         int
	SessionID     string
	InputTokens   int
	OutputTokens  int
	LastInput     int
	CacheCreate   int
	CacheRead     int
	ToolCalls     int
	ToolErrors    int
	ToolBlocked   int
	LastTool      string
	CWD           string
	MaxTurns      int
	MaxTokens     int
	ToolCount     int
	ContextHint   string
	ContextWindow int
	ServiceTier   string
	InferenceGeo  string
	Speed         string
}

func (m Model) usageView() string {
	return m.usageViewForBudget(m.renderBudget(), chromeFull)
}

func (m Model) usageViewForBudget(budget renderBudget, mode chromeMode) string {
	panel := m.usage
	if panel.Model == "" && panel.InputTokens == 0 && panel.OutputTokens == 0 && panel.ToolCalls == 0 && panel.CWD == "" {
		return ""
	}
	if mode != chromeFull {
		return m.compactUsageView(panel, budget.ChromeWidth, mode == chromeMinimal)
	}
	if m.shouldUseQuietConversationHint() {
		return m.quietUsageViewForWidth(panel, budget.ChromeWidth)
	}
	parts := []string{"Usage"}
	// Session-level model/cwd already appear in the header and runtime line;
	// only repeat them here when the per-turn value actually differs
	// (BUG-2026-07-03-001: avoid redundant model=/cwd= in per-turn usage).
	headerInfo := m.welcome.withDefaults(m.title)
	if panel.Model != "" && !strings.EqualFold(panel.Model, headerInfo.Model) {
		parts = append(parts, "model="+panel.Model)
	}
	if panel.Turns > 0 {
		turns := strconv.Itoa(panel.Turns)
		if panel.MaxTurns > 0 {
			turns += "/" + strconv.Itoa(panel.MaxTurns)
		}
		parts = append(parts, "turns="+turns)
	}
	if panel.InputTokens != 0 || panel.OutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("tokens in/out=%d/%d", panel.InputTokens, panel.OutputTokens))
	}
	if panel.LastInput > 0 && panel.ContextWindow > 0 {
		parts = append(parts, formatContextWindowUsage(panel.LastInput, panel.ContextWindow))
	}
	if panel.CacheCreate != 0 || panel.CacheRead != 0 {
		parts = append(parts, formatCacheUsage(panel.InputTokens, panel.CacheCreate, panel.CacheRead, "session_hit"))
	}
	if panel.ToolCalls != 0 || panel.ToolCount != 0 {
		parts = append(parts, friendlyToolUsageSummary(panel))
	}
	if panel.MaxTokens > 0 {
		parts = append(parts, "max_tokens="+strconv.Itoa(panel.MaxTokens))
	}
	if shouldShowUsageStopReason(panel.StopReason) {
		parts = append(parts, "stop="+panel.StopReason)
	}
	if panel.Speed != "" {
		parts = append(parts, "speed="+panel.Speed)
	}
	if panel.ServiceTier != "" {
		parts = append(parts, "tier="+panel.ServiceTier)
	}
	if panel.InferenceGeo != "" {
		parts = append(parts, "geo="+panel.InferenceGeo)
	}
	if panel.ContextHint != "" {
		parts = append(parts, "context="+panel.ContextHint)
	} else if panel.CWD != "" && !strings.EqualFold(panel.CWD, headerInfo.CWD) {
		parts = append(parts, "cwd="+panel.CWD)
	}
	return statusStyle.Render(wrapUsageParts(parts, budget.ChromeWidth))
}

func (m Model) quietUsageView(panel usagePanel) string {
	return m.quietUsageViewForWidth(panel, m.renderBudget().ChromeWidth)
}

func (m Model) quietUsageViewForWidth(panel usagePanel, width int) string {
	parts := []string{"Usage"}
	if panel.Turns > 0 {
		turns := strconv.Itoa(panel.Turns)
		if panel.MaxTurns > 0 {
			turns += "/" + strconv.Itoa(panel.MaxTurns)
		}
		parts = append(parts, "turns="+turns)
	}
	if panel.InputTokens != 0 || panel.OutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("tokens in/out=%d/%d", panel.InputTokens, panel.OutputTokens))
	}
	if panel.LastInput > 0 && panel.ContextWindow > 0 {
		parts = append(parts, formatContextWindowUsage(panel.LastInput, panel.ContextWindow))
	}
	if panel.CacheCreate != 0 || panel.CacheRead != 0 {
		parts = append(parts, formatCacheUsage(panel.InputTokens, panel.CacheCreate, panel.CacheRead, "session_hit"))
	}
	if panel.ToolCalls != 0 || panel.ToolCount != 0 {
		parts = append(parts, friendlyToolUsageSummary(panel))
	}
	if shouldShowUsageStopReason(panel.StopReason) {
		parts = append(parts, "stop="+panel.StopReason)
	}
	if len(parts) == 1 {
		return ""
	}
	return statusStyle.Render(wrapUsageParts(parts, width))
}

func (m Model) compactUsageView(panel usagePanel, width int, minimal bool) string {
	if minimal {
		return ""
	}
	parts := []string{"Usage"}
	if panel.Turns > 0 {
		turns := strconv.Itoa(panel.Turns)
		if panel.MaxTurns > 0 {
			turns += "/" + strconv.Itoa(panel.MaxTurns)
		}
		parts = append(parts, "turns="+turns)
	}
	if panel.InputTokens != 0 || panel.OutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("tokens=%d/%d", panel.InputTokens, panel.OutputTokens))
	}
	if panel.LastInput > 0 && panel.ContextWindow > 0 {
		parts = append(parts, formatContextWindowUsage(panel.LastInput, panel.ContextWindow))
	}
	if panel.CacheCreate != 0 || panel.CacheRead != 0 {
		parts = append(parts, formatCacheUsage(panel.InputTokens, panel.CacheCreate, panel.CacheRead, "session_hit"))
	}
	if summary := compactToolSummary(panel); summary != "" {
		parts = append(parts, summary)
	}
	if len(parts) == 1 {
		return ""
	}
	return statusStyle.Render(wrapUsageParts(parts, width))
}

func compactToolSummary(panel usagePanel) string {
	if panel.ToolCalls == 0 {
		return ""
	}
	parts := []string{"操作 " + strconv.Itoa(panel.ToolCalls) + " 次"}
	if panel.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("未完成 %d", panel.ToolErrors))
	}
	if panel.ToolBlocked > 0 {
		parts = append(parts, fmt.Sprintf("安全保护 %d", panel.ToolBlocked))
	}
	return strings.Join(parts, " · ")
}

func friendlyToolUsageSummary(panel usagePanel) string {
	if panel.ToolCalls == 0 {
		return ""
	}
	parts := []string{"执行了 " + strconv.Itoa(panel.ToolCalls) + " 次操作"}
	if panel.ToolErrors > 0 {
		parts = append(parts, fmt.Sprintf("%d 次未完成", panel.ToolErrors))
	}
	if panel.ToolBlocked > 0 {
		parts = append(parts, fmt.Sprintf("%d 次安全保护", panel.ToolBlocked))
	}
	if panel.LastTool != "" {
		parts = append(parts, "最近使用 "+toolDisplayName(panel.LastTool))
	}
	return strings.Join(parts, " · ")
}

func shouldShowUsageStopReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	return reason != "" && !strings.EqualFold(reason, "end_turn")
}

func usageCacheTokens(usage Usage) (int, int) {
	return usage.CacheCreationInputTokens + usage.CacheCreationEphemeral1hInputTokens + usage.CacheCreationEphemeral5mInputTokens, usage.CacheReadInputTokens
}

func formatCacheUsage(inputTokens, cacheCreate, cacheRead int, hitLabel string) string {
	summary := fmt.Sprintf("cache create/read=%d/%d", cacheCreate, cacheRead)
	if inputTokens > 0 {
		if strings.TrimSpace(hitLabel) == "" {
			hitLabel = "hit"
		}
		summary += fmt.Sprintf(" %s=%.1f%%", hitLabel, float64(cacheRead)/float64(inputTokens)*100)
	}
	return summary
}

func formatContextWindowUsage(inputTokens, contextWindow int) string {
	return fmt.Sprintf("ctx=%.1f%% %d/%d", float64(inputTokens)/float64(contextWindow)*100, inputTokens, contextWindow)
}

func wrapUsageParts(parts []string, width int) string {
	if len(parts) == 0 {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	indent := strings.Repeat(" ", len(parts[0])+2)
	var lines []string
	line := parts[0]
	for _, part := range parts[1:] {
		if part == "" {
			continue
		}
		candidate := line + "  " + part
		if runewidth.StringWidth(candidate) <= width {
			line = candidate
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = ""
		for _, chunk := range wrapDisplayChunks(part, max(8, width-runewidth.StringWidth(indent))) {
			wrapped := indent + chunk
			if line != "" {
				lines = append(lines, line)
			}
			line = wrapped
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) runningTokenLabel() string {
	if m.usage.InputTokens != 0 || m.usage.OutputTokens != 0 {
		return fmt.Sprintf("session tokens in/out=%d/%d", m.usage.InputTokens, m.usage.OutputTokens)
	}
	return "tokens pending"
}

func friendlyStreamErrorStatus(err error) string {
	if err == nil {
		return ""
	}
	if isNetworkStreamError(err) {
		text := strings.ToLower(err.Error())
		if strings.Contains(text, "timed out") || strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded") {
			return "网络连接超时，本轮已停止"
		}
		return "网络连接中断，本轮已停止"
	}
	return truncate(oneLine(err.Error()), 96)
}

func friendlyStreamErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	raw := strings.TrimSpace(err.Error())
	if raw == "" {
		return "本轮已停止。"
	}
	if !isNetworkStreamError(err) {
		return raw
	}
	title := "网络连接中断，本轮已停止"
	if strings.Contains(strings.ToLower(raw), "timed out") || strings.Contains(strings.ToLower(raw), "timeout") {
		title = "网络连接超时，本轮已停止"
	}
	return strings.Join([]string{
		title,
		"模型流式响应中断，已保留当前进度。可以直接输入“继续”，让 niuma 从当前任务继续。",
		"详情：" + raw,
	}, "\n")
}

func isNetworkStreamError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"operation timed out",
		"connection reset by peer",
		"i/o timeout",
		"context deadline exceeded",
		"stream error",
		"read tcp",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
