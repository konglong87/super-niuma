// transcript.go 负责把已完成的块冲进真实 scrollback（时间线的落盘侧）。

package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type transcriptFlushReason int

const (
	transcriptFlushInitial transcriptFlushReason = iota
	transcriptFlushUserSubmit
	transcriptFlushTurnComplete
	transcriptFlushSessionResume
	transcriptFlushBackground
	transcriptFlushRecap
	// transcriptFlushInteractivePrompt：弹权限 / AskUserQuestion 卡片前先把本轮已完成的
	// 块推进真实 scrollback。卡片会把 viewport 往上挤，被挤出去的内容如果没进滚动区就
	// 彻底找不回来了。
	transcriptFlushInteractivePrompt
	transcriptFlushPhaseBoundary
)

func (m Model) pendingTranscriptBlocks() []string {
	blocks := []string{}
	if !m.transcriptPrintedHeader && !m.transcriptCommittingHeader {
		if header := strings.TrimRight(m.transcriptHeaderView(), "\n"); header != "" {
			blocks = append(blocks, header)
		}
	}
	if m.displayTimeline.hasSegments() {
		blocks = append(blocks, m.pendingTimelineTranscriptBlocks()...)
		return blocks
	}
	start := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	if liveBlocks := m.pendingLiveDisplayTranscriptBlocks(start); len(liveBlocks) > 0 {
		blocks = append(blocks, liveBlocks...)
		return blocks
	}
	for i := start; i < len(m.messages); i++ {
		msg := m.messages[i]
		if !m.messageReadyForTranscript(i, msg) {
			continue
		}
		rendered := strings.TrimRight(m.renderTranscriptMessage(msg), "\n")
		blocks = append(blocks, rendered)
	}
	return blocks
}

func (m *Model) markTranscriptPrinted() {
	m.transcriptPrintedHeader = true
	m.transcriptPrintedCount = m.readyTranscriptCount()
	if m.displayTimeline.hasSegments() {
		m.transcriptPrintedSeq = m.displayTimeline.maxSeq()
	}
	m.transcriptCommittingSeq = 0
	m.initialTranscriptPending = false
	m.liveDisplayBlocks = m.unprintedLiveDisplayBlocks()
}

func (m *Model) prepareTranscriptFlushCmd(reason transcriptFlushReason) tea.Cmd {
	if reason != transcriptFlushPhaseBoundary && reason != transcriptFlushTurnComplete && m.phaseTranscriptFlushInFlight() {
		return nil
	}
	blocks := m.pendingTranscriptBlocks()
	if reason == transcriptFlushRecap {
		m.suppressLatestRecapLiveView()
	}
	if len(blocks) == 0 {
		return nil
	}
	msg := transcriptFlushCommitMsg{
		output:        formatTranscriptOutput(blocks, m.transcriptBottomGuardLinesFor(reason)),
		reason:        reason,
		printedHeader: true,
		printedCount:  m.readyTranscriptCount(),
		printedSeq:    m.transcriptPrintedSeq,
	}
	if m.displayTimeline.hasSegments() && reason != transcriptFlushPhaseBoundary {
		msg.printedSeq = m.latestReadyTimelineSeq()
	}
	if reason == transcriptFlushPhaseBoundary {
		msg.printedSeq = m.latestPhaseReadyTimelineSeq()
	}
	if msg.printedHeader {
		m.transcriptCommittingHeader = true
	}
	m.transcriptCommittingSeq = maxInt64(m.transcriptCommittingSeq, msg.printedSeq)
	if reason == transcriptFlushPhaseBoundary {
		m.transcriptPhaseCommittingSeq = maxInt64(m.transcriptPhaseCommittingSeq, msg.printedSeq)
	} else if reason != transcriptFlushUserSubmit {
		if msg.printedHeader {
			m.transcriptPrintedHeader = true
			m.transcriptCommittingHeader = false
		}
		m.transcriptPrintedCount = max(m.transcriptPrintedCount, msg.printedCount)
		m.transcriptPrintedSeq = maxInt64(m.transcriptPrintedSeq, msg.printedSeq)
	}
	m.initialTranscriptPending = false
	m.liveDisplayBlocks = m.unprintedLiveDisplayBlocks()
	return tea.Tick(transcriptFlushFrameDelay, func(time.Time) tea.Msg {
		return msg
	})
}

func (m Model) phaseTranscriptFlushInFlight() bool {
	return m.phaseBoundaryStreamCh != nil || m.transcriptPhaseCommittingSeq > m.transcriptPrintedSeq
}

func (m *Model) suppressLatestRecapLiveView() {
	if m.displayTimeline.hasSegments() {
		for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
			if m.displayTimeline.segments[i].kind == displaySegmentRecap {
				m.recapSuppressedSeq = maxInt64(m.recapSuppressedSeq, m.displayTimeline.segments[i].seq)
				break
			}
		}
	}
	m.recapSuppressedCount = max(m.recapSuppressedCount, len(m.messages))
	m.initialTranscriptPending = false
	m.liveDisplayBlocks = m.unprintedLiveDisplayBlocks()
}

func (m *Model) commitTranscriptFlushCmd(msg transcriptFlushCommitMsg) tea.Cmd {
	if strings.TrimSpace(msg.output) == "" {
		return nil
	}
	return tea.Sequence(tea.Println(msg.output), transcriptFlushPrintedCmd(msg))
}

func transcriptFlushPrintedCmd(msg transcriptFlushCommitMsg) tea.Cmd {
	return func() tea.Msg {
		return transcriptFlushPrintedMsg{
			printedHeader: msg.printedHeader,
			printedCount:  msg.printedCount,
			printedSeq:    msg.printedSeq,
		}
	}
}

func (m *Model) markTranscriptFlushPrinted(msg transcriptFlushPrintedMsg) {
	if msg.printedHeader {
		m.transcriptPrintedHeader = true
	}
	m.transcriptPrintedCount = max(m.transcriptPrintedCount, msg.printedCount)
	m.transcriptPrintedSeq = maxInt64(m.transcriptPrintedSeq, msg.printedSeq)
	if m.transcriptPhaseCommittingSeq <= m.transcriptPrintedSeq {
		m.transcriptPhaseCommittingSeq = 0
	}
	if m.transcriptCommittingSeq <= m.transcriptPrintedSeq {
		m.transcriptCommittingSeq = 0
	}
	if msg.printedHeader {
		m.transcriptCommittingHeader = false
	}
	m.initialTranscriptPending = false
	m.liveDisplayBlocks = m.unprintedLiveDisplayBlocks()
}

func (m Model) latestReadyTimelineSeq() int64 {
	pending := m.pendingTranscriptSeq()
	latest := pending
	for _, seg := range m.displayTimeline.segments {
		if seg.seq <= pending {
			continue
		}
		if !m.displaySegmentReadyForTranscript(seg) {
			if seg.kind == displaySegmentRecap {
				continue
			}
			break
		}
		latest = seg.seq
	}
	return latest
}

func (m Model) latestPhaseReadyTimelineSeq() int64 {
	pending := m.pendingTranscriptSeq()
	latest := pending
	for _, seg := range m.displayTimeline.segments {
		if seg.seq <= pending {
			continue
		}
		if seg.kind == displaySegmentRecap {
			continue
		}
		if seg.kind != displaySegmentThinking && seg.kind != displaySegmentAssistantText {
			break
		}
		if !m.displaySegmentReadyForTranscript(seg) {
			break
		}
		latest = seg.seq
	}
	return latest
}

func (m Model) unprintedLiveDisplayBlocks() []liveDisplayBlock {
	if len(m.liveDisplayBlocks) == 0 {
		return nil
	}
	printed := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	kept := make([]liveDisplayBlock, 0, len(m.liveDisplayBlocks))
	for _, block := range m.liveDisplayBlocks {
		switch block.kind {
		case liveDisplayBlockMessage:
			if block.messageIndex >= printed {
				kept = append(kept, block)
			}
		case liveDisplayBlockTools, liveDisplayBlockAgents, liveDisplayBlockMeta:
			if m.hasUnprintedAssistantMessage(printed) {
				kept = append(kept, block)
			}
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

func (m Model) hasUnprintedAssistantMessage(printed int) bool {
	for i := printed; i < len(m.messages); i++ {
		if m.messages[i].role == "assistant" && strings.TrimSpace(m.messages[i].content) != "" {
			return true
		}
	}
	return false
}

func (m Model) flushTranscriptCmdFor(reason transcriptFlushReason) tea.Cmd {
	blocks := m.pendingTranscriptBlocks()
	if len(blocks) == 0 {
		return nil
	}
	return tea.Println(formatTranscriptOutput(blocks, m.transcriptBottomGuardLinesFor(reason)))
}

func formatTranscriptOutput(blocks []string, bottomGuardLines int) string {
	output := formatTranscriptBlocks(blocks)
	if output == "" {
		return ""
	}
	if bottomGuardLines > 0 {
		output += strings.Repeat("\n", bottomGuardLines)
	}
	return output
}

func formatTranscriptBlocks(blocks []string) string {
	cleaned := make([]string, 0, len(blocks))
	for _, block := range blocks {
		block = strings.TrimRight(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		cleaned = append(cleaned, block)
	}
	if len(cleaned) == 0 {
		return ""
	}
	return strings.Join(cleaned, "\n\n") + "\n"
}

func (m Model) transcriptBottomGuardLinesFor(reason transcriptFlushReason) int {
	return transcriptBottomGuardMinSpacer
}

func (m Model) readyTranscriptCount() int {
	count := clamp(m.transcriptPrintedCount, 0, len(m.messages))
	for count < len(m.messages) {
		if !m.messageReadyForTranscript(count, m.messages[count]) && m.messages[count].role != "recap" {
			break
		}
		count++
	}
	return count
}

func (m Model) messageReadyForTranscript(index int, msg message) bool {
	if m.streamingActive && index == len(m.messages)-1 && (msg.role == "assistant" || msg.role == "thinking") {
		return false
	}
	switch msg.role {
	case "recap":
		return false
	case "user", "assistant", "error", "tool", "agent", "thinking", "status", messageRoleConfigNotice, "background", "loop":
		return strings.TrimSpace(msg.content) != ""
	default:
		return strings.TrimSpace(msg.content) != ""
	}
}

func (m Model) pendingTimelineTranscriptBlocks() []string {
	if !m.displayTimeline.hasSegments() {
		return nil
	}
	blocks := []string{}
	seenAssistantText := false
	seenThinkingText := false
	pendingSeq := m.pendingTranscriptSeq()
	for _, seg := range m.displayTimeline.segments {
		if seg.seq <= pendingSeq {
			continue
		}
		if !m.displaySegmentReadyForTranscript(seg) {
			if seg.kind != displaySegmentRecap {
				break
			}
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
		if rendered := strings.TrimRight(m.renderDisplaySegment(seg, true, continuation), "\n"); rendered != "" {
			blocks = append(blocks, rendered)
		}
	}
	return blocks
}

func (m Model) displaySegmentReadyForTranscript(seg displaySegment) bool {
	switch seg.kind {
	case displaySegmentAssistantText, displaySegmentThinking:
		return !m.streamingActive || seg.status != displaySegmentStreaming
	case displaySegmentTool:
		return !m.busy || seg.status != displaySegmentRunning
	case displaySegmentRecap:
		return false
	default:
		return true
	}
}
