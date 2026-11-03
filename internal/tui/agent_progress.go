// agent_progress.go 维护子代理进度状态并渲染子代理面板。

package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type agentProgress struct {
	taskID      uint64
	agent       string
	model       string
	description string
	status      string
	detail      string
	turn        string
	messages    string
	toolCalls   string
	lastTool    string
	tokens      string
	cache       string
	messageFrom string
	durationMS  string
	startedAt   string
	sessionID   string
	transcript  string
	outputFile  string
	worktree    string
	branch      string
	capability  string
	updatedAt   time.Time
}

var progressSpinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m Model) agentProgressSummary() string {
	if len(m.agentProgress) == 0 {
		return ""
	}
	running := 0
	failed := 0
	cancelled := 0
	timeout := 0
	completed := 0
	other := 0
	for _, progress := range m.agentProgress {
		status := strings.ToLower(strings.TrimSpace(progress.status))
		switch {
		case progressIsRunning(status):
			running++
		case status == "failed":
			failed++
		case status == "cancelled":
			cancelled++
		case status == "timeout":
			timeout++
		case status == "completed":
			completed++
		default:
			other++
		}
	}
	var parts []string
	if running > 0 {
		parts = append(parts, fmt.Sprintf("run:%d", running))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("fail:%d", failed))
	}
	if cancelled > 0 {
		parts = append(parts, fmt.Sprintf("cancel:%d", cancelled))
	}
	if timeout > 0 {
		parts = append(parts, fmt.Sprintf("timeout:%d", timeout))
	}
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("done:%d", completed))
	}
	if other > 0 {
		parts = append(parts, fmt.Sprintf("other:%d", other))
	}
	return strings.Join(parts, "/")
}

func (m Model) agentProgressView() string {
	if len(m.agentProgress) == 0 {
		return ""
	}
	items := m.orderedAgentProgress()
	if len(items) == 0 {
		return ""
	}
	return m.agentProgressViewFor(items)
}

func (m Model) messageAgentProgressView(items []agentProgress) string {
	if len(items) == 0 {
		return ""
	}
	return m.agentProgressViewFor(items)
}

func (m Model) agentProgressViewFor(items []agentProgress) string {
	limit := m.agentProgressRenderLimit(len(items))
	visible := items
	hidden := 0
	if len(items) > limit {
		visible = items[:limit]
		hidden = len(items) - limit
	}
	var b strings.Builder
	header := agentProgressHeader(items, m.agentPanelExpanded)
	if m.agentPanelExpanded {
		header += "  ctrl+e collapse"
	} else if len(items) > limit {
		header += "  ctrl+e expand"
	}
	b.WriteString(statusStyle.Render(header))
	for _, progress := range visible {
		b.WriteString("\n")
		b.WriteString(renderAgentProgress(progress, m.agentPanelExpanded, m.now()))
	}
	if hidden > 0 {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("+%d older hidden", hidden)))
	}
	return b.String()
}

func (m Model) orderedAgentProgress() []agentProgress {
	items := make([]agentProgress, 0, len(m.agentOrder))
	seen := make(map[uint64]bool, len(m.agentOrder))
	for _, taskID := range m.agentOrder {
		progress, ok := m.agentProgress[taskID]
		if !ok {
			continue
		}
		items = append(items, progress)
		seen[taskID] = true
	}
	for taskID, progress := range m.agentProgress {
		if !seen[taskID] {
			items = append(items, progress)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftPriority := agentProgressStatusPriority(items[i].status)
		rightPriority := agentProgressStatusPriority(items[j].status)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		leftUpdated := items[i].updatedAt
		rightUpdated := items[j].updatedAt
		if !leftUpdated.Equal(rightUpdated) {
			return leftUpdated.After(rightUpdated)
		}
		return items[i].taskID > items[j].taskID
	})
	return items
}

func (m Model) agentProgressRenderLimit(total int) int {
	if m.agentPanelExpanded {
		return min(total, 10)
	}
	if m.height >= 32 {
		return min(total, 5)
	}
	return min(total, 3)
}

func (m *Model) archiveAgentProgressToAssistant() {
	if len(m.agentProgress) == 0 {
		return
	}
	archived := m.orderedAgentProgress()
	if i, ok := m.latestUnprintedAssistantIndex(); ok {
		m.messages[i].agents = archived
	}
	m.agentProgress = map[uint64]agentProgress{}
	m.agentOrder = nil
}

func (m *Model) updateAgentProgress(event StreamEvent) {
	progress, ok := m.agentProgress[event.TaskID]
	if !ok {
		progress = agentProgress{taskID: event.TaskID}
		m.agentOrder = append(m.agentOrder, event.TaskID)
	}
	progress.updatedAt = m.now()
	payload := parsePayload(event.Payload)
	progress.status = firstNonEmpty(event.Event, event.Text, event.Output, "progress")
	// Each event updates one slice of the task card; prior fields are retained
	// so the TUI can show a complete sub-agent snapshot instead of only last text.
	switch event.Event {
	case "started":
		progress.agent = payloadString(payload, "agent_name")
		progress.model = payloadString(payload, "model")
		progress.description = payloadString(payload, "description")
		progress.startedAt = payloadString(payload, "started_at")
		progress.status = "started"
		progress.detail = firstNonEmpty(payloadString(payload, "prompt_preview"), progress.detail)
	case "turn_start":
		progress.status = "running"
		progress.turn = payloadNumber(payload, "turn")
		progress.messages = payloadNumber(payload, "messages")
		progress.detail = formatProgressKV("turn", progress.turn, "messages", progress.messages)
	case "text_delta":
		progress.status = "running"
		progress.detail = truncate(oneLine(payloadString(payload, "text")), max(24, m.width-28))
	case "message":
		progress.status = "message"
		progress.messageFrom = firstNonEmpty(payloadString(payload, "from_agent"), "parent")
		progress.detail = "from " + progress.messageFrom
		if preview := payloadString(payload, "content_preview"); preview != "" {
			progress.detail += ": " + truncate(oneLine(preview), max(24, m.width-40))
		}
	case "tool_call":
		progress.status = "tool"
		progress.lastTool = firstNonEmpty(payloadString(payload, "tool_name"), payloadString(payload, "tool_id"))
		progress.detail = "tool: " + progress.lastTool
	case "tool_result":
		progress.status = "tool done"
		progress.lastTool = firstNonEmpty(payloadString(payload, "tool_name"), payloadString(payload, "tool_id"))
		progress.detail = "tool: " + progress.lastTool
		if payloadString(payload, "is_error") == "true" {
			progress.detail += " error"
		}
		if preview := payloadString(payload, "preview"); preview != "" {
			progress.detail += " - " + truncate(oneLine(preview), max(24, m.width-40))
		}
	case "usage":
		progress.status = "usage"
		inTokens := payloadNumber(payload, "input_tokens")
		outTokens := payloadNumber(payload, "output_tokens")
		progress.tokens = strings.Trim(strings.Join([]string{formatKV("in", inTokens), formatKV("out", outTokens)}, "/"), "/")
		cacheWrite := payloadNumber(payload, "cache_creation_input_tokens")
		cacheRead := payloadNumber(payload, "cache_read_input_tokens")
		progress.cache = strings.Trim(strings.Join([]string{cacheWrite, cacheRead}, "/"), "/")
		progress.detail = strings.Trim(strings.Join([]string{formatKV("tokens", progress.tokens), formatKV("cache", progress.cache)}, "  "), " ")
	case "cache_state":
		if payloadString(payload, "breaks_cache") == "true" {
			progress.detail = "cache signature changed"
		} else {
			progress.detail = "cache signature stable"
		}
	case "completed":
		progress.status = "completed"
		progress.turn = payloadNumber(payload, "turns")
		progress.toolCalls = payloadNumber(payload, "tool_calls")
		progress.durationMS = payloadNumber(payload, "duration_ms")
		progress.sessionID = payloadString(payload, "session_id")
		progress.detail = strings.Trim(strings.Join([]string{formatCount("turns", progress.turn), formatCount("tools", progress.toolCalls), formatDuration(progress.durationMS)}, ", "), ", ")
		if progress.sessionID != "" {
			progress.detail = strings.Trim(strings.Join([]string{progress.detail, "sess=" + progress.sessionID}, ", "), ", ")
		}
	case "failed":
		progress.status = "failed"
		progress.durationMS = payloadNumber(payload, "duration_ms")
		progress.detail = payloadString(payload, "error")
	case "cancelled":
		progress.status = "cancelled"
		progress.durationMS = payloadNumber(payload, "duration_ms")
		progress.detail = payloadString(payload, "source")
	case "timeout":
		progress.status = "timeout"
		progress.durationMS = payloadNumber(payload, "duration_ms")
		progress.detail = firstNonEmpty(payloadString(payload, "error"), payloadString(payload, "stop_reason"), "deadline exceeded")
	}
	if sessionID := payloadString(payload, "session_id"); sessionID != "" {
		progress.sessionID = sessionID
	}
	if transcript := payloadString(payload, "transcript_path"); transcript != "" {
		progress.transcript = transcript
	}
	if outputFile := payloadString(payload, "output_file"); outputFile != "" {
		progress.outputFile = outputFile
	}
	if worktree := payloadString(payload, "worktree_path"); worktree != "" {
		progress.worktree = worktree
	}
	if branch := payloadString(payload, "worktree_branch"); branch != "" {
		progress.branch = branch
	}
	if capability := capabilityLoopSummaryFromPayload(payload); capability != "" {
		progress.capability = capability
	}
	if progress.detail == "" {
		progress.detail = firstNonEmpty(event.Text, event.Output)
	}
	m.agentProgress[event.TaskID] = progress
}

func (m Model) subagentRunningStatus(taskID uint64) string {
	progress, ok := m.agentProgress[taskID]
	if !ok {
		return ""
	}
	label := fmt.Sprintf("Sub-agent #%d", progress.taskID)
	if progress.agent != "" {
		label += " " + progress.agent
	}
	parts := []string{label}
	if status := strings.TrimSpace(progress.status); status != "" {
		parts = append(parts, status)
	}
	if progress.lastTool != "" {
		parts = append(parts, "tool="+progress.lastTool)
	}
	if progress.turn != "" {
		parts = append(parts, "turn="+progress.turn)
	}
	if elapsed := agentProgressElapsed(progress, m.now()); elapsed != "" {
		parts = append(parts, "elapsed="+elapsed)
	}
	if progress.detail != "" && shouldShowCompactAgentDetail(progress.status) {
		parts = append(parts, truncate(oneLine(progress.detail), 48))
	}
	return truncate(strings.Join(parts, " · "), max(32, m.width-4))
}

func renderAgentProgress(progress agentProgress, expanded bool, now time.Time) string {
	label := fmt.Sprintf("#%d", progress.taskID)
	if progress.agent != "" {
		label += " " + progress.agent
	}
	if progress.model != "" {
		label += " (" + progress.model + ")"
	}
	if !expanded {
		return renderAgentProgressCompact(label, progress, now)
	}
	status := firstNonEmpty(progress.status, "progress")
	if progress.detail != "" {
		status += " - " + progress.detail
	}
	if progressIsRunning(progress.status) {
		status += " - stop #" + strconv.FormatUint(progress.taskID, 10)
	}
	metadata := strings.Trim(strings.Join([]string{
		formatKV("desc", progress.description),
		formatKV("from", progress.messageFrom),
		formatKV("turn", progress.turn),
		formatKV("messages", progress.messages),
		formatKV("tool", progress.lastTool),
		formatKV("tokens", progress.tokens),
		formatKV("c", progress.cache),
		formatKV("session", progress.sessionID),
		formatKV("out", agentProgressPath(progress.outputFile)),
		formatKV("transcript", agentProgressPath(progress.transcript)),
		formatKV("worktree", agentProgressPath(progress.worktree)),
		formatKV("branch", progress.branch),
		formatKV("elapsed", agentProgressElapsed(progress, now)),
	}, "  "), " ")
	lines := []string{agentProgressLineStyle(progress.status).Render(agentProgressIcon(progress, now) + " " + label + "  " + status)}
	if metadata != "" {
		lines = append(lines, "  "+statusStyle.Render(metadata))
	}
	if progress.capability != "" {
		lines = append(lines, renderAgentCapabilityLines(progress.capability)...)
	}
	return strings.Join(lines, "\n")
}

func renderAgentProgressCompact(label string, progress agentProgress, now time.Time) string {
	status := firstNonEmpty(progress.status, "progress")
	parts := []string{}
	if progressIsRunning(progress.status) {
		parts = append(parts, "stop #"+strconv.FormatUint(progress.taskID, 10))
	}
	if shouldShowCompactAgentDetail(progress.status) && progress.detail != "" {
		parts = append(parts, truncate(oneLine(progress.detail), 64))
	}
	if progress.turn != "" {
		parts = append(parts, formatKV("turn", progress.turn))
	}
	if progress.messages != "" {
		parts = append(parts, formatKV("messages", progress.messages))
	}
	if progress.toolCalls != "" {
		parts = append(parts, formatKV("tools", progress.toolCalls))
	}
	if progress.lastTool != "" {
		parts = append(parts, formatKV("tool", progress.lastTool))
	}
	if progress.tokens != "" {
		parts = append(parts, formatKV("tokens", progress.tokens))
	}
	if elapsed := agentProgressElapsed(progress, now); elapsed != "" {
		parts = append(parts, formatKV("elapsed", elapsed))
	}
	line := agentProgressIcon(progress, now) + " " + label + "  " + status
	if len(parts) > 0 {
		line += " - " + strings.Join(parts, ", ")
	}
	return agentProgressLineStyle(progress.status).Render(line)
}

func shouldShowCompactAgentDetail(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled", "cache_state", "message":
		return true
	default:
		return false
	}
}

func renderAgentCapabilityLines(capability string) []string {
	capability = oneLine(capability)
	if strings.TrimSpace(capability) == "" {
		return nil
	}
	parts := strings.Split(capability, " | ")
	lines := make([]string, 0, min(6, len(parts)))
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label := "evidence"
		if i > 0 {
			label = "detail"
		}
		lines = append(lines, "  "+statusStyle.Render(label+": "+truncate(part, 180)))
		if len(lines) == 6 {
			break
		}
	}
	return lines
}

func agentProgressPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return truncate(abbreviateHome(path), 96)
}

func agentProgressHeader(items []agentProgress, expanded bool) string {
	running := 0
	completed := 0
	failed := 0
	cancelled := 0
	timeout := 0
	interrupted := 0
	for _, item := range items {
		status := strings.ToLower(strings.TrimSpace(item.status))
		switch {
		case progressIsRunning(status):
			running++
		case status == "completed":
			completed++
		case status == "failed":
			failed++
		case status == "cancelled":
			cancelled++
		case status == "timeout":
			timeout++
		case status == "interrupted":
			interrupted++
		}
	}
	bad := failed + timeout
	parts := []string{
		fmt.Sprintf("%d running", running),
		fmt.Sprintf("%d done", completed),
		fmt.Sprintf("%d failed", bad),
	}
	if cancelled > 0 {
		parts = append(parts, fmt.Sprintf("%d cancelled", cancelled))
	}
	if interrupted > 0 {
		parts = append(parts, fmt.Sprintf("%d interrupted", interrupted))
	}
	header := "Sub-agents"
	if expanded {
		header += " expanded"
	}
	return header + "  " + strings.Join(parts, " / ")
}

func agentProgressIcon(progress agentProgress, now time.Time) string {
	status := strings.ToLower(strings.TrimSpace(progress.status))
	switch {
	case progressIsRunning(status):
		return progressSpinnerFrame(now)
	case status == "completed":
		return "✓"
	case status == "failed" || status == "timeout":
		return "✗"
	case status == "cancelled" || status == "interrupted":
		return "!"
	default:
		return "•"
	}
}

func agentProgressLineStyle(status string) lipgloss.Style {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case progressIsRunning(status):
		return agentRunningStyle
	case status == "completed":
		return agentCompletedStyle
	case status == "failed" || status == "timeout":
		return agentFailedStyle
	case status == "cancelled" || status == "interrupted":
		return agentCancelledStyle
	default:
		return statusStyle
	}
}

func agentProgressElapsed(progress agentProgress, now time.Time) string {
	if progressIsRunning(progress.status) {
		startedAt := strings.TrimSpace(progress.startedAt)
		if startedAt == "" {
			return ""
		}
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return ""
		}
		return runningDurationLabel(now.Sub(started))
	}
	return formatDuration(progress.durationMS)
}

func progressSpinnerFrame(now time.Time) string {
	if len(progressSpinnerFrames) == 0 {
		return "⋯"
	}
	if now.IsZero() {
		return progressSpinnerFrames[0]
	}
	index := int(now.UnixMilli()/120) % len(progressSpinnerFrames)
	if index < 0 {
		index = 0
	}
	return progressSpinnerFrames[index]
}

func progressIsRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "started", "running", "tool", "tool done", "message", "usage", "cache_state":
		return true
	default:
		return false
	}
}

func agentProgressStatusPriority(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "cancelled", "interrupted":
		return 0
	case "started", "running", "tool", "tool done", "message", "usage", "cache_state":
		return 1
	case "completed":
		return 2
	default:
		return 1
	}
}
