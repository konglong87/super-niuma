package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type thinkingMode string

const (
	thinkingModeFull    = "full"
	thinkingModeSummary = "summary"
	thinkingModeHidden  = "hidden"
)

func resolveThinkingMode(info WelcomeInfo) thinkingMode {
	switch thinkingMode(strings.ToLower(strings.TrimSpace(info.ThinkingMode))) {
	case thinkingModeFull:
		return thinkingModeFull
	case thinkingModeSummary:
		return thinkingModeSummary
	case thinkingModeHidden:
		return thinkingModeHidden
	}
	if info.ShowThinking != nil && !*info.ShowThinking {
		return thinkingModeHidden
	}
	return thinkingModeFull
}

func (m Model) renderThinkingSegment(seg displaySegment, continuation bool) string {
	switch m.thinkingMode {
	case thinkingModeHidden:
		return ""
	case thinkingModeSummary:
		return m.renderThinkingSummary(seg)
	default:
		return m.renderThinking(seg.content, seg.status, continuation)
	}
}

func (m Model) renderThinkingSummary(seg displaySegment) string {
	content := strings.TrimSpace(seg.content)
	if content == "" {
		return ""
	}
	lines := len(strings.Split(content, "\n"))
	parts := []string{"◇ Thought"}
	if seg.status == displaySegmentStreaming {
		parts[0] = "◌ Thinking"
	}
	if seg.turn > 0 {
		parts = append(parts, "turn "+strconv.Itoa(seg.turn))
	}
	if seg.phase > 0 {
		parts = append(parts, "phase "+strconv.Itoa(seg.phase))
	}
	parts = append(parts, fmt.Sprintf("%d lines", lines))
	if !seg.createdAt.IsZero() && seg.updatedAt.After(seg.createdAt) {
		parts = append(parts, runningDurationLabel(seg.updatedAt.Sub(seg.createdAt)))
	}
	parts = append(parts, "tokens unavailable", "/thinking show "+thinkingTurnLabel(seg.turn))
	return thinkingHeaderStyle.Render(strings.Join(parts, " · "))
}

func thinkingTurnLabel(turn int) string {
	if turn > 0 {
		return strconv.Itoa(turn)
	}
	return "latest"
}

func (m Model) nextThinkingPhase(turn int) int {
	phase := 0
	for _, seg := range m.displayTimeline.segments {
		if seg.kind == displaySegmentThinking && seg.turn == turn && seg.phase > phase {
			phase = seg.phase
		}
	}
	for _, seg := range m.thinkingArchive {
		if seg.turn == turn && seg.phase > phase {
			phase = seg.phase
		}
	}
	return phase + 1
}

func (m *Model) appendHiddenThinking(text string) {
	if text == "" {
		return
	}
	last := len(m.thinkingArchive) - 1
	if last >= 0 {
		seg := &m.thinkingArchive[last]
		if seg.turn == m.currentTurn && seg.status == displaySegmentStreaming {
			seg.content = appendStreamingText(seg.content, text)
			seg.updatedAt = m.now()
			return
		}
	}
	now := m.now()
	m.thinkingArchive = append(m.thinkingArchive, displaySegment{
		id:        fmt.Sprintf("hidden-thinking-%d-%d", m.currentTurn, len(m.thinkingArchive)+1),
		kind:      displaySegmentThinking,
		role:      "thinking",
		status:    displaySegmentStreaming,
		content:   text,
		turn:      m.currentTurn,
		phase:     m.nextThinkingPhase(m.currentTurn),
		createdAt: now,
		updatedAt: now,
	})
}

func (m *Model) closeHiddenThinkingArchive() {
	if last := len(m.thinkingArchive) - 1; last >= 0 && m.thinkingArchive[last].status == displaySegmentStreaming {
		m.thinkingArchive[last].status = displaySegmentDone
		m.thinkingArchive[last].updatedAt = m.now()
	}
}

func (m *Model) loadThinkingArchive(details []ThinkingDetail) {
	for _, detail := range details {
		content := strings.TrimSpace(detail.Content)
		if detail.Turn < 1 || detail.Phase < 1 || content == "" {
			continue
		}
		m.thinkingArchive = append(m.thinkingArchive, displaySegment{
			id:        fmt.Sprintf("transcript-thinking-%d-%d", detail.Turn, detail.Phase),
			kind:      displaySegmentThinking,
			role:      "thinking",
			status:    displaySegmentDone,
			content:   content,
			turn:      detail.Turn,
			phase:     detail.Phase,
			createdAt: detail.CreatedAt,
			updatedAt: detail.CreatedAt,
		})
		if detail.Turn > m.currentTurn {
			m.currentTurn = detail.Turn
		}
	}
}

func isThinkingCommand(input string) bool {
	fields := strings.Fields(strings.TrimSpace(input))
	return len(fields) > 0 && strings.EqualFold(fields[0], "/thinking")
}

func (m *Model) handleThinkingCommand(input string) bool {
	if !isThinkingCommand(input) {
		return false
	}
	fields := strings.Fields(strings.TrimSpace(input))
	if len(fields) == 1 {
		m.err = nil
		m.appendDisplayMessage("status", "Thinking mode: "+string(m.thinkingMode)+". Use /thinking full|summary|hide or /thinking show <turn>.", "")
		return true
	}
	action := strings.ToLower(fields[1])
	switch action {
	case thinkingModeFull, thinkingModeSummary, "hide", thinkingModeHidden:
		mode := thinkingMode(action)
		if action == "hide" {
			mode = thinkingModeHidden
		}
		m.thinkingMode = mode
		m.thinkingModeExplicit = true
		m.closeThinkingDetail()
		m.err = nil
		m.appendDisplayMessage("status", "Thinking mode: "+string(mode)+". Already printed terminal history is unchanged.", "")
		return true
	case "show":
		if len(fields) > 3 {
			m.err = errors.New("usage: /thinking show <turn|latest>")
			return true
		}
		target := "latest"
		if len(fields) == 3 {
			target = fields[2]
		}
		turn, err := m.resolveThinkingTurn(target)
		if err != nil {
			m.err = err
			return true
		}
		m.thinkingDetailTurn = turn
		m.stickToBottom = false
		m.err = nil
		return true
	default:
		m.err = errors.New("thinking mode must be full, summary, hidden, or show")
		return true
	}
}

func (m Model) resolveThinkingTurn(value string) (int, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	turn := 0
	if value == "" || value == "latest" {
		turn = m.latestThinkingTurn()
	} else {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			return 0, fmt.Errorf("invalid thinking turn %q", value)
		}
		turn = parsed
	}
	if len(m.thinkingSegmentsForTurn(turn)) == 0 {
		return 0, fmt.Errorf("no Thinking detail was captured for turn %d", turn)
	}
	return turn, nil
}

func (m Model) latestThinkingTurn() int {
	turn := 0
	for _, seg := range m.displayTimeline.segments {
		if seg.kind == displaySegmentThinking && strings.TrimSpace(seg.content) != "" && seg.turn > turn {
			turn = seg.turn
		}
	}
	for _, seg := range m.thinkingArchive {
		if strings.TrimSpace(seg.content) != "" && seg.turn > turn {
			turn = seg.turn
		}
	}
	return turn
}

func (m Model) thinkingSegmentsForTurn(turn int) []displaySegment {
	var out []displaySegment
	seen := map[string]bool{}
	for _, seg := range m.displayTimeline.segments {
		if seg.kind == displaySegmentThinking && seg.turn == turn && strings.TrimSpace(seg.content) != "" {
			out = append(out, seg)
			seen[thinkingSegmentKey(seg)] = true
		}
	}
	for _, seg := range m.thinkingArchive {
		if seg.turn == turn && strings.TrimSpace(seg.content) != "" && !seen[thinkingSegmentKey(seg)] {
			out = append(out, seg)
		}
	}
	return out
}

func thinkingSegmentKey(seg displaySegment) string {
	return fmt.Sprintf("%d:%d:%s", seg.turn, seg.phase, seg.content)
}

func (m Model) thinkingDetailActive() bool {
	return m.thinkingDetailTurn > 0
}

func (m *Model) closeThinkingDetail() {
	m.thinkingDetailTurn = 0
	m.stickToBottom = true
}

func (m Model) thinkingDetailView() string {
	if !m.thinkingDetailActive() {
		return ""
	}
	segments := m.thinkingSegmentsForTurn(m.thinkingDetailTurn)
	if len(segments) == 0 {
		return statusStyle.Render(fmt.Sprintf("Thinking detail · turn %d\nNo captured Thinking content is available.", m.thinkingDetailTurn))
	}
	parts := []string{thinkingHeaderStyle.Render(fmt.Sprintf("Thinking detail · turn %d · %d phase(s) · Esc close", m.thinkingDetailTurn, len(segments)))}
	for _, seg := range segments {
		parts = append(parts, m.renderThinking(seg.content, displaySegmentDone, false))
	}
	return strings.Join(parts, "\n")
}
