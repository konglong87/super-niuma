// display_segments.go 把流事件写成 display timeline 的 segment（时间线的写入侧）。

package tui

import (
	"fmt"
	"strings"
)

func (m *Model) appendDisplayMessage(role, content, meta string) {
	msg := message{role: role, content: content, meta: meta}
	m.messages = append(m.messages, msg)
	m.appendDisplaySegment(displaySegment{
		kind:    displayKindForRole(role),
		role:    role,
		status:  displaySegmentDone,
		content: content,
		meta:    meta,
	})
}

func (m *Model) appendInitialDisplayMessage(initial InitialMessage) {
	role := strings.TrimSpace(initial.Role)
	if role == "" {
		role = "status"
	}
	content := strings.TrimSpace(initial.Content)
	if content == "" {
		return
	}
	turn := initial.Turn
	if turn <= 0 && role == "user" {
		m.currentTurn++
		turn = m.currentTurn
	}
	if turn > m.currentTurn {
		m.currentTurn = turn
	}
	m.messages = append(m.messages, message{role: role, content: content})
	m.appendDisplaySegment(displaySegment{
		kind:      displayKindForRole(role),
		role:      role,
		status:    displaySegmentDone,
		content:   content,
		turn:      turn,
		phase:     initial.Phase,
		createdAt: initial.CreatedAt,
		updatedAt: initial.CreatedAt,
	})
}

func (m *Model) appendConfigNotice(content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = configNoticeDefaultText
	}
	lastMessage := len(m.messages) - 1
	lastSegment := len(m.displayTimeline.segments) - 1
	if lastMessage >= m.transcriptPrintedCount && lastSegment >= 0 {
		msg := &m.messages[lastMessage]
		seg := &m.displayTimeline.segments[lastSegment]
		if msg.role == messageRoleConfigNotice && seg.kind == displaySegmentConfigNotice && seg.seq > m.pendingTranscriptSeq() {
			if msg.content != content {
				msg.content += configNoticeSeparator + content
				seg.content = msg.content
				seg.updatedAt = m.now()
			}
			return
		}
	}
	m.appendDisplayMessage(messageRoleConfigNotice, content, "")
}

func (m *Model) appendDisplayTextSegment(role, text string) {
	if text == "" {
		return
	}
	kind := displayKindForRole(role)
	if kind != displaySegmentAssistantText && kind != displaySegmentThinking {
		m.appendDisplaySegment(displaySegment{
			kind:    kind,
			role:    role,
			status:  displaySegmentDone,
			content: text,
		})
		return
	}
	last := len(m.displayTimeline.segments) - 1
	if last >= 0 {
		seg := &m.displayTimeline.segments[last]
		if seg.kind == kind && seg.status == displaySegmentStreaming {
			seg.content = appendStreamingText(seg.content, text)
			seg.updatedAt = m.now()
			return
		}
	}
	m.appendDisplaySegment(displaySegment{
		kind:    kind,
		role:    role,
		status:  displaySegmentStreaming,
		content: text,
	})
}

// amendLiveAssistantText replaces the live-streamed tail `prev` with `final`
// (empty final retracts it). It fires when the query layer's accepted turn
// text diverges from what streamed live — output guards, provider failover,
// or a completion-gate retry discarding the turn. Streamed deltas are appended
// verbatim to both the message content and the timeline segment, so the tail
// is an exact suffix; if it isn't (already reconciled elsewhere), do nothing —
// the final response sync at turn completion is the safety net.
func (m *Model) amendLiveAssistantText(prev, final string) {
	if prev == "" && final == "" {
		return
	}
	if n := len(m.messages) - 1; n >= 0 && m.messages[n].role == "assistant" && strings.HasSuffix(m.messages[n].content, prev) {
		content := strings.TrimSuffix(m.messages[n].content, prev) + final
		if strings.TrimSpace(content) == "" {
			m.messages = m.messages[:n]
			blocks := m.liveDisplayBlocks[:0]
			for _, block := range m.liveDisplayBlocks {
				if block.kind == liveDisplayBlockMessage && block.messageIndex == n {
					continue
				}
				blocks = append(blocks, block)
			}
			m.liveDisplayBlocks = blocks
		} else {
			m.messages[n].content = content
			m.ensureLiveMessageDisplayBlock(n)
		}
	}
	if last := len(m.displayTimeline.segments) - 1; last >= 0 {
		seg := &m.displayTimeline.segments[last]
		if seg.kind == displaySegmentAssistantText && strings.HasSuffix(seg.content, prev) {
			content := strings.TrimSuffix(seg.content, prev) + final
			if strings.TrimSpace(content) == "" {
				m.displayTimeline.segments = m.displayTimeline.segments[:last]
			} else {
				seg.content = content
			}
		}
	}
}

func appendStreamingText(existing, next string) string {
	if existing == "" {
		return next
	}
	if next == "" {
		return existing
	}
	if strings.HasSuffix(existing, "\n") || strings.HasPrefix(next, "\n") {
		return existing + next
	}
	return existing + next
}

func (m *Model) appendDisplayToolFromEvent(event StreamEvent) {
	m.closeDisplayTextAppendWindow()
	if item, ok := m.latestToolActivityForEvent(event); ok {
		m.upsertDisplayTool(item)
	}
}

func (m *Model) updateDisplayToolFromEvent(event StreamEvent) {
	if item, ok := m.latestToolActivityForEvent(event); ok {
		m.upsertDisplayTool(item)
	}
}

func (m Model) latestToolActivityForEvent(event StreamEvent) (toolActivityItem, bool) {
	if event.ToolID != "" {
		for i := len(m.toolActivity) - 1; i >= 0; i-- {
			if m.toolActivity[i].ToolID == event.ToolID {
				return m.toolActivity[i], true
			}
		}
	}
	name := strings.TrimSpace(firstNonEmpty(event.ToolName, event.ToolID))
	if name != "" {
		for i := len(m.toolActivity) - 1; i >= 0; i-- {
			if m.toolActivity[i].ToolName == name {
				return m.toolActivity[i], true
			}
		}
	}
	return toolActivityItem{}, false
}

func (m *Model) upsertDisplayTool(item toolActivityItem) {
	if strings.TrimSpace(item.ToolName) == "" && strings.TrimSpace(item.ToolID) == "" {
		return
	}
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		seg := &m.displayTimeline.segments[i]
		if seg.kind != displaySegmentTool {
			continue
		}
		if item.ToolID != "" && seg.toolID == item.ToolID {
			seg.tool = item
			seg.status = displayStatusForTool(item)
			seg.updatedAt = m.now()
			return
		}
	}
	m.appendDisplaySegment(displaySegment{
		kind:   displaySegmentTool,
		role:   "tool",
		status: displayStatusForTool(item),
		toolID: item.ToolID,
		tool:   item,
	})
}

func displayStatusForTool(item toolActivityItem) displaySegmentState {
	switch strings.ToLower(strings.TrimSpace(item.Status)) {
	case "running", "":
		return displaySegmentRunning
	case "error", "blocked", "interrupted":
		return displaySegmentErrorDone
	default:
		return displaySegmentDone
	}
}

func (m *Model) appendOrUpdateDisplayMeta(meta string) {
	meta = strings.TrimSpace(meta)
	if meta == "" {
		return
	}
	m.closeDisplayTextAppendWindow()
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		seg := &m.displayTimeline.segments[i]
		if seg.kind == displaySegmentMeta && seg.seq > m.transcriptPrintedSeq {
			seg.content = meta
			seg.updatedAt = m.now()
			return
		}
		if seg.kind == displaySegmentAssistantText {
			break
		}
	}
	m.appendDisplaySegment(displaySegment{
		kind:    displaySegmentMeta,
		role:    "status",
		status:  displaySegmentDone,
		content: meta,
	})
}

func (m *Model) appendDisplaySegment(seg displaySegment) {
	if strings.TrimSpace(seg.content) == "" && seg.kind != displaySegmentTool && len(seg.agents) == 0 {
		return
	}
	m.displayTimeline.nextSeq++
	seg.seq = m.displayTimeline.nextSeq
	seg.id = fmt.Sprintf("seg-%d", seg.seq)
	if seg.turn <= 0 {
		seg.turn = m.currentTurn
	}
	if seg.kind == displaySegmentThinking && seg.phase <= 0 {
		seg.phase = m.nextThinkingPhase(seg.turn)
	}
	if seg.role == "" {
		seg.role = displayRoleForKind(seg.kind)
	}
	if seg.status == "" {
		seg.status = displaySegmentDone
	}
	now := m.now()
	if seg.createdAt.IsZero() {
		seg.createdAt = now
	}
	if seg.updatedAt.IsZero() {
		seg.updatedAt = seg.createdAt
	}
	m.displayTimeline.segments = append(m.displayTimeline.segments, seg)
}

func (m *Model) ensureDisplayTimelineForUnprintedMessages() {
	if m.displayTimeline.hasSegments() {
		return
	}
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for i := start; i < len(m.messages); i++ {
		msg := m.messages[i]
		if strings.TrimSpace(msg.content) == "" {
			continue
		}
		status := displaySegmentDone
		if m.streamingActive && i == len(m.messages)-1 && (msg.role == "assistant" || msg.role == "thinking") {
			status = displaySegmentStreaming
		}
		m.appendDisplaySegment(displaySegment{
			kind:    displayKindForRole(msg.role),
			role:    msg.role,
			status:  status,
			content: msg.content,
			meta:    msg.meta,
			agents:  append([]agentProgress(nil), msg.agents...),
		})
	}
}

func (m *Model) upsertDisplayAgentProgress() {
	agents := m.orderedAgentProgress()
	if len(agents) == 0 {
		return
	}
	m.closeDisplayTextAppendWindow()
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		seg := &m.displayTimeline.segments[i]
		if seg.kind == displaySegmentAgent && seg.seq > m.transcriptPrintedSeq {
			seg.agents = append([]agentProgress(nil), agents...)
			seg.status = displaySegmentDone
			seg.updatedAt = m.now()
			return
		}
	}
	m.appendDisplaySegment(displaySegment{
		kind:   displaySegmentAgent,
		role:   "agent",
		status: displaySegmentDone,
		agents: append([]agentProgress(nil), agents...),
	})
}

func (m *Model) closeDisplayTextAppendWindow() {
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		seg := &m.displayTimeline.segments[i]
		if seg.kind == displaySegmentAssistantText || seg.kind == displaySegmentThinking {
			if seg.status == displaySegmentStreaming {
				seg.status = displaySegmentDone
				seg.updatedAt = m.now()
			}
			return
		}
		if seg.kind != displaySegmentMeta {
			return
		}
	}
}

func (m *Model) markDisplayTurnDone() {
	for i := range m.displayTimeline.segments {
		switch m.displayTimeline.segments[i].status {
		case displaySegmentStreaming, displaySegmentRunning:
			m.displayTimeline.segments[i].status = displaySegmentDone
			m.displayTimeline.segments[i].updatedAt = m.now()
		}
	}
}
