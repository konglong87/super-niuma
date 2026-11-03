// live_display.go 渲染 viewport 里仍在变化的活跃块。

package tui

import (
	"strings"
)

type liveDisplayBlockKind string

const (
	liveDisplayBlockMessage liveDisplayBlockKind = "message"
	liveDisplayBlockTools   liveDisplayBlockKind = "tools"
	liveDisplayBlockAgents  liveDisplayBlockKind = "agents"
	liveDisplayBlockMeta    liveDisplayBlockKind = "meta"
)

type liveDisplayBlock struct {
	kind         liveDisplayBlockKind
	messageIndex int
}

func (m Model) timelineLiveView() string {
	if !m.displayTimeline.hasSegments() {
		return ""
	}
	parts := []string{}
	hiddenSeq := m.liveHiddenSeq()
	seenAssistantText := false
	seenThinkingText := false
	for _, seg := range m.displayTimeline.segments {
		if seg.seq <= hiddenSeq {
			continue
		}
		if seg.kind == displaySegmentRecap && seg.seq <= m.recapSuppressedSeq {
			continue
		}
		continuation := false
		switch seg.kind {
		case displaySegmentAssistantText:
			continuation = seenAssistantText
			seenAssistantText = true
		case displaySegmentThinking:
			continuation = seenThinkingText
			seenThinkingText = true
		case displaySegmentUser, displaySegmentError, displaySegmentStatusKind, displaySegmentConfigNotice, displaySegmentBackground, displaySegmentLoop, displaySegmentRecap, displaySegmentTodo:
			seenAssistantText = false
			seenThinkingText = false
		}
		if rendered := strings.TrimRight(m.renderDisplaySegment(seg, false, continuation), "\n"); rendered != "" {
			parts = append(parts, rendered)
		}
	}
	return strings.Join(parts, "\n")
}

func (m Model) liveHiddenSeq() int64 {
	return maxInt64(m.transcriptPrintedSeq, maxInt64(m.transcriptCommittingSeq, m.transcriptPhaseCommittingSeq))
}

func (m Model) pendingTranscriptSeq() int64 {
	return maxInt64(m.transcriptPrintedSeq, maxInt64(m.transcriptCommittingSeq, m.transcriptPhaseCommittingSeq))
}

func (m Model) renderDisplaySegment(seg displaySegment, transcript bool, continuation bool) string {
	switch seg.kind {
	case displaySegmentTool:
		if strings.TrimSpace(seg.tool.ToolName) == "" && strings.TrimSpace(seg.tool.ToolID) == "" {
			return ""
		}
		if transcript {
			return m.toolCallTranscriptView([]toolActivityItem{seg.tool})
		}
		return m.toolCallInlineView([]toolActivityItem{seg.tool})
	case displaySegmentMeta:
		if strings.TrimSpace(seg.content) == "" {
			return ""
		}
		return statusStyle.Render(wrapPrefixedLine("* ", seg.content, m.contentWidth()))
	case displaySegmentTodo:
		if strings.TrimSpace(seg.content) == "" {
			return ""
		}
		return m.renderTodoCompletionSegment(seg.content)
	case displaySegmentAgent:
		if len(seg.agents) > 0 {
			return m.messageAgentProgressView(seg.agents)
		}
		return m.renderDisplayMessageSegment(seg, transcript)
	case displaySegmentAssistantText, displaySegmentThinking:
		if continuation {
			return m.renderDisplayTextContinuation(seg)
		}
		return m.renderDisplayMessageSegment(seg, transcript)
	default:
		return m.renderDisplayMessageSegment(seg, transcript)
	}
}

func (m Model) renderDisplayTextContinuation(seg displaySegment) string {
	content := strings.TrimSpace(seg.content)
	if content == "" {
		return ""
	}
	switch seg.kind {
	case displaySegmentAssistantText:
		if seg.status == displaySegmentStreaming {
			return m.renderStreamingMarkdown(content)
		}
		return m.renderMarkdown(content)
	case displaySegmentThinking:
		return m.renderThinkingSegment(seg, true)
	default:
		return wrapForViewport(content, m.contentWidth())
	}
}

func (m Model) renderDisplayMessageSegment(seg displaySegment, transcript bool) string {
	if seg.kind == displaySegmentThinking {
		return m.renderThinkingSegment(seg, false)
	}
	role := seg.role
	if role == "" {
		role = displayRoleForKind(seg.kind)
	}
	msg := message{role: role, content: seg.content, meta: seg.meta, agents: seg.agents}
	if transcript {
		return m.renderTranscriptMessage(msg)
	}
	if seg.kind == displaySegmentAssistantText && seg.status == displaySegmentStreaming {
		msg.content = stabilizeStreamingMarkdownTables(msg.content)
	}
	return m.renderMessage(msg)
}

func (m Model) renderTodoCompletionSegment(content string) string {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i, line := range lines {
		lines[i] = truncateDisplay(line, max(24, m.contentWidth()))
	}
	return statusStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) liveTranscriptView() string {
	if m.thinkingDetailActive() {
		return m.thinkingDetailView()
	}
	if m.initialTranscriptPending && !m.transcriptPrintedHeader {
		return ""
	}
	if m.shouldShowIdleWelcomeLive() {
		return m.fixedHeaderView()
	}
	return m.appendLiveWorkingLine(m.liveTranscriptBody())
}

// interactivePromptView 渲染权限 / AskUserQuestion 卡片。卡片属于 bottom chrome，
// 和 slash/resume/rewind picker 同层级：只占它需要的行数，viewport 继续显示本轮对话。
// 早期实现让它替换整个 live body，结果一弹卡片就把整轮输出从屏幕上抹掉。
func (m Model) interactivePromptView() string {
	if m.pendingPermission != nil {
		return permissionPromptView(*m.pendingPermission, m.contentWidth())
	}
	if m.pendingQuestion != nil {
		return questionPromptView(*m.pendingQuestion, m.contentWidth())
	}
	return ""
}

func (m Model) liveTranscriptBody() string {
	if live := m.timelineLiveView(); live != "" {
		return live
	}
	if live := m.liveDisplayBlocksView(); live != "" {
		return live
	}
	if msg, ok := m.lastLiveMessage(); ok {
		live := m.renderMessage(msg)
		if running := m.toolCallInlineView(m.toolActivity); running != "" {
			live = strings.TrimRight(live, "\n") + "\n" + running
		}
		if agents := m.agentProgressView(); agents != "" && len(msg.agents) == 0 {
			live = strings.TrimRight(live, "\n") + "\n" + agents
		}
		return live
	}
	if m.shouldShowLiveRecap() {
		msg, _ := m.latestRecapMessage()
		return m.renderMessage(msg)
	}
	if m.busy {
		var b strings.Builder
		if running := m.toolCallInlineView(m.toolActivity); running != "" {
			b.WriteString(running)
		}
		if agents := m.agentProgressView(); agents != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(agents)
		}
		return b.String()
	}
	return ""
}

// appendLiveWorkingLine pins an animated "still working" heartbeat to the bottom
// of the live body while the turn is busy but nothing else is animating — the
// long model round-trips between tool calls that otherwise leave the screen
// frozen. The status text that used to live only in the bottom chrome now shows
// in the transcript flow, where the eye already is.
func (m Model) appendLiveWorkingLine(body string) string {
	working := m.liveWorkingLine()
	if working == "" {
		return body
	}
	if strings.TrimSpace(body) == "" {
		return working
	}
	return strings.TrimRight(body, "\n") + "\n" + working
}

// liveWorkingLine returns the braille spinner + running status + turn elapsed
// heartbeat, or "" when idle or when another segment already animates on its
// own (a running tool/sub-agent, or streaming text).
func (m Model) liveWorkingLine() string {
	if !m.busy || m.hasActiveAnimatedSegment() {
		return ""
	}
	status := m.currentRunningStatus()
	if m.turnStopping {
		status = animateStatusEllipsis("Stopping...", m.spinnerFrame)
	}
	line := "  " + progressSpinnerFrame(m.now()) + " " + status
	if !m.turnStarted.IsZero() {
		line += "  " + runningDurationLabel(m.now().Sub(m.turnStarted))
	}
	return statusStyle.Render(line)
}

// hasActiveAnimatedSegment reports whether the live body already shows something
// that updates on its own: a running tool, a running sub-agent, or streaming
// assistant/thinking text. Used to suppress the heartbeat so there is never a
// redundant second spinner.
func (m Model) hasActiveAnimatedSegment() bool {
	for _, item := range m.toolActivity {
		if item.Status == "running" {
			return true
		}
	}
	for _, progress := range m.orderedAgentProgress() {
		if progressIsRunning(progress.status) {
			return true
		}
	}
	if n := len(m.displayTimeline.segments); n > 0 {
		last := m.displayTimeline.segments[n-1]
		if last.status == displaySegmentStreaming &&
			(last.kind == displaySegmentAssistantText || last.kind == displaySegmentThinking) {
			return true
		}
	}
	return false
}

func (m Model) shouldShowIdleWelcomeLive() bool {
	if !m.windowSizeKnown || !m.shouldShowWelcome() || m.busy || m.err != nil {
		return false
	}
	info := m.welcome.withDefaults(m.title)
	if strings.TrimSpace(info.Resume) != "" || strings.TrimSpace(info.SessionStatus) != "" {
		return false
	}
	if m.pendingPermission != nil || m.pendingQuestion != nil || m.resumePickerActive() || m.rewindPickerActive() || m.slashSuggestionsActive() {
		return false
	}
	return true
}

func (m *Model) resetLiveDisplayBlocks() {
	m.liveDisplayBlocks = nil
}

func (m *Model) ensureCurrentLiveMessageDisplayBlocks() {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := start; i < len(m.messages); i++ {
		if liveDisplayMessageEligible(m.messages[i]) {
			m.ensureLiveMessageDisplayBlock(i)
		}
	}
}

func (m *Model) ensureLiveMessageDisplayBlock(index int) {
	if index < 0 || index >= len(m.messages) || !liveDisplayMessageEligible(m.messages[index]) {
		return
	}
	for _, block := range m.liveDisplayBlocks {
		if block.kind == liveDisplayBlockMessage && block.messageIndex == index {
			return
		}
	}
	m.liveDisplayBlocks = append(m.liveDisplayBlocks, liveDisplayBlock{kind: liveDisplayBlockMessage, messageIndex: index})
}

func (m *Model) ensureLiveToolDisplayBlock() {
	for _, block := range m.liveDisplayBlocks {
		if block.kind == liveDisplayBlockTools {
			return
		}
	}
	m.liveDisplayBlocks = append(m.liveDisplayBlocks, liveDisplayBlock{kind: liveDisplayBlockTools})
}

func (m *Model) ensureLiveAgentDisplayBlock() {
	for _, block := range m.liveDisplayBlocks {
		if block.kind == liveDisplayBlockAgents {
			return
		}
	}
	m.liveDisplayBlocks = append(m.liveDisplayBlocks, liveDisplayBlock{kind: liveDisplayBlockAgents})
}

func liveDisplayMessageEligible(msg message) bool {
	if strings.TrimSpace(msg.content) == "" {
		return false
	}
	return msg.role != "recap"
}

func (m Model) liveDisplayBlocksView() string {
	if len(m.liveDisplayBlocks) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.liveDisplayBlocks))
	for _, block := range m.liveDisplayBlocks {
		switch block.kind {
		case liveDisplayBlockMessage:
			if block.messageIndex < 0 || block.messageIndex >= len(m.messages) {
				continue
			}
			msg := m.messages[block.messageIndex]
			if !liveDisplayMessageEligible(msg) {
				continue
			}
			if rendered := strings.TrimRight(m.renderLiveDisplayMessage(msg), "\n"); rendered != "" {
				parts = append(parts, rendered)
			}
		case liveDisplayBlockTools:
			if rendered := strings.TrimRight(m.toolCallInlineView(m.liveDisplayToolActivity()), "\n"); rendered != "" {
				parts = append(parts, rendered)
			}
		case liveDisplayBlockAgents:
			if rendered := strings.TrimRight(m.liveDisplayAgentProgressView(), "\n"); rendered != "" {
				parts = append(parts, rendered)
			}
		case liveDisplayBlockMeta:
			if rendered := strings.TrimRight(m.liveDisplayAssistantMetaView(), "\n"); rendered != "" {
				parts = append(parts, rendered)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (m Model) pendingLiveDisplayTranscriptBlocks(start int) []string {
	if len(m.liveDisplayBlocks) == 0 {
		return nil
	}
	blocks := make([]string, 0, len(m.liveDisplayBlocks))
	seenMessages := map[int]bool{}
	for _, block := range m.liveDisplayBlocks {
		switch block.kind {
		case liveDisplayBlockMessage:
			if block.messageIndex < start || block.messageIndex >= len(m.messages) {
				continue
			}
			msg := m.messages[block.messageIndex]
			if !m.messageReadyForTranscript(block.messageIndex, msg) {
				continue
			}
			seenMessages[block.messageIndex] = true
			if rendered := strings.TrimRight(m.renderLiveDisplayTranscriptMessage(msg), "\n"); rendered != "" {
				blocks = append(blocks, rendered)
			}
		case liveDisplayBlockTools:
			if rendered := strings.TrimRight(m.toolCallTranscriptView(m.liveDisplayToolActivity()), "\n"); rendered != "" {
				blocks = append(blocks, rendered)
			}
		case liveDisplayBlockAgents:
			if rendered := strings.TrimRight(m.liveDisplayAgentProgressTranscriptView(), "\n"); rendered != "" {
				blocks = append(blocks, rendered)
			}
		case liveDisplayBlockMeta:
			if rendered := strings.TrimRight(m.liveDisplayAssistantMetaView(), "\n"); rendered != "" {
				blocks = append(blocks, rendered)
			}
		}
	}
	for i := start; i < len(m.messages); i++ {
		if seenMessages[i] || !m.messageReadyForTranscript(i, m.messages[i]) {
			continue
		}
		rendered := strings.TrimRight(m.renderTranscriptMessage(m.messages[i]), "\n")
		if rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return blocks
}

func (m Model) renderLiveDisplayMessage(msg message) string {
	msg.tools = nil
	msg.agents = nil
	msg.meta = ""
	return m.renderMessage(msg)
}

func (m Model) renderLiveDisplayTranscriptMessage(msg message) string {
	msg.tools = nil
	msg.agents = nil
	msg.meta = ""
	return m.renderTranscriptMessage(msg)
}

func (m Model) liveDisplayToolActivity() []toolActivityItem {
	if len(m.toolActivity) > 0 {
		return m.toolActivity
	}
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		if len(m.messages[i].tools) > 0 {
			return m.messages[i].tools
		}
	}
	return nil
}

func (m Model) liveDisplayAgentProgressView() string {
	if len(m.agentProgress) > 0 {
		return m.agentProgressView()
	}
	return m.messageAgentProgressView(m.liveDisplayAgentProgress())
}

func (m Model) liveDisplayAgentProgressTranscriptView() string {
	return m.liveDisplayAgentProgressView()
}

func (m Model) liveDisplayAgentProgress() []agentProgress {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		if len(m.messages[i].agents) > 0 {
			return m.messages[i].agents
		}
	}
	return nil
}

func (m *Model) ensureLiveAssistantMetaDisplayBlock() {
	for _, block := range m.liveDisplayBlocks {
		if block.kind == liveDisplayBlockMeta {
			return
		}
	}
	m.liveDisplayBlocks = append(m.liveDisplayBlocks, liveDisplayBlock{kind: liveDisplayBlockMeta})
}

func (m Model) liveDisplayAssistantMetaView() string {
	meta := m.liveDisplayAssistantMeta()
	if meta == "" {
		return ""
	}
	return statusStyle.Render(wrapPrefixedLine("* ", meta, m.contentWidth()))
}

func (m Model) liveDisplayAssistantMeta() string {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		if m.messages[i].role == "assistant" && strings.TrimSpace(m.messages[i].meta) != "" {
			return m.messages[i].meta
		}
	}
	return ""
}

func (m Model) shouldShowLiveRecap() bool {
	if m.busy || m.err != nil || m.pendingPermission != nil || m.pendingQuestion != nil {
		return false
	}
	if m.latestRecapSegmentHidden() {
		return false
	}
	_, ok := m.latestLiveRecapMessage()
	return ok
}

func (m Model) latestRecapSegmentHidden() bool {
	if !m.displayTimeline.hasSegments() {
		return false
	}
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		seg := m.displayTimeline.segments[i]
		if seg.kind == displaySegmentRecap {
			return seg.seq <= m.recapSuppressedSeq
		}
	}
	return false
}

func (m Model) latestRecapMessage() (message, bool) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.role == "recap" && strings.TrimSpace(msg.content) != "" {
			return msg, true
		}
	}
	return message{}, false
}

func (m Model) latestLiveRecapMessage() (message, bool) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		msg := m.messages[i]
		if msg.role == "recap" && strings.TrimSpace(msg.content) != "" {
			if i < m.recapSuppressedCount {
				return message{}, false
			}
			return msg, true
		}
	}
	return message{}, false
}

func (m Model) lastLiveMessage() (message, bool) {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		msg := m.messages[i]
		if msg.role == "recap" {
			continue
		}
		if strings.TrimSpace(msg.content) != "" {
			return msg, true
		}
	}
	return message{}, false
}

func (m Model) hasVisibleAgentProgress() bool {
	if len(m.agentProgress) > 0 {
		return true
	}
	if msg, ok := m.lastLiveMessage(); ok && len(msg.agents) > 0 {
		return true
	}
	return false
}

func (m Model) hasArchivedToolActivity() bool {
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := len(m.messages) - 1; i >= start; i-- {
		if len(m.messages[i].tools) > 0 {
			return true
		}
	}
	return false
}
