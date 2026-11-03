// layout.go 负责终端尺寸预算、底部 chrome 组装与 viewport 刷新节流。

package tui

import (
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type chromeMode int

const (
	chromeFull chromeMode = iota
	chromeCompact
	chromeMinimal
)

type renderBudget struct {
	ReportedWidth  int
	ReportedHeight int
	SafeWidth      int
	SafeHeight     int
	ContentWidth   int
	ChromeWidth    int
	InputWidth     int
	MinViewport    int
	MaxChrome      int
}

func (m Model) bottomChromeParts(status string, hasLive bool) []string {
	budget := m.renderBudget()
	mode := m.chromeModeForBudget(status, hasLive, budget)
	return m.bottomChromePartsForBudget(status, hasLive, budget, mode)
}

func (m Model) chromeModeForBudget(status string, hasLive bool, budget renderBudget) chromeMode {
	if m.pendingPermission != nil || m.pendingQuestion != nil || m.resumePickerActive() || m.rewindPickerActive() || m.slashSuggestionsActive() || m.nextStepsActive() {
		return chromeFull
	}
	if m.chromeHeight(m.bottomChromePartsForBudget(status, hasLive, budget, chromeFull), budget) <= budget.MaxChrome {
		return chromeFull
	}
	if m.chromeHeight(m.bottomChromePartsForBudget(status, hasLive, budget, chromeCompact), budget) <= budget.MaxChrome {
		return chromeCompact
	}
	return chromeMinimal
}

func (m Model) chromeHeight(parts []string, budget renderBudget) int {
	if len(parts) == 0 {
		return 0
	}
	return visualLineCount(strings.Join(parts, "\n"), budget.SafeWidth)
}

func (m Model) bottomChromePartsForBudget(status string, hasLive bool, budget renderBudget, mode chromeMode) []string {
	parts := make([]string, 0, 7)
	if todos := m.todoProgressView(); todos != "" {
		parts = append(parts, todos)
	}
	if usage := m.usageViewForBudget(budget, mode); usage != "" {
		if hasLive {
			parts = append(parts, "")
		}
		parts = append(parts, usage)
	}
	if suggestions := m.slashSuggestionView(); suggestions != "" {
		parts = append(parts, suggestions)
	}
	if nextSteps := m.nextStepsView(); nextSteps != "" {
		parts = append(parts, nextSteps)
	}
	if pendingInputs := m.pendingInputsView(); pendingInputs != "" {
		parts = append(parts, pendingInputs)
	}
	if picker := m.resumePickerView(); picker != "" {
		parts = append(parts, picker)
	}
	if picker := m.rewindPickerView(); picker != "" {
		parts = append(parts, picker)
	}
	if prompt := m.interactivePromptView(); prompt != "" {
		parts = append(parts, prompt)
	}
	if attachments := m.attachmentTrayView(); attachments != "" {
		parts = append(parts, attachments)
	}
	parts = append(parts, m.inputBoxViewForBudget(status, budget, mode))
	return parts
}

func (m Model) fixedHeaderView() string {
	return indentBlock(m.headerView(m.shouldShowWelcome()), 2)
}

func (m Model) transcriptHeaderView() string {
	return indentBlock(m.headerView(false), 2)
}

func (m Model) toggleMouseTracking() (tea.Model, tea.Cmd) {
	m.mouseTracking = !m.mouseTracking
	if m.mouseTracking {
		m.err = errors.New("mouse scroll mode on; use Shift+drag if your terminal requires it for copy")
		return m, tea.EnableMouseCellMotion
	}
	m.err = errors.New("mouse copy mode on; terminal drag selection is restored")
	return m, tea.DisableMouse
}

// refreshViewportHook, when non-nil, is invoked at the top of refreshViewport.
// Test-only seam for asserting how often the viewport is actually re-rendered:
// streaming throttling coalesces bursts of deltas into far fewer renders.
var refreshViewportHook func()

// streamRenderThrottleInterval bounds how often the busy-path callers (stream
// text/thinking deltas, the spinner tick, the 1s ticker) trigger a full
// viewport re-render. Deltas arriving within one interval coalesce into a
// single render, so total renders scale with wall-clock time, not delta count
// (some providers stream ~1 delta per token, which otherwise forces one full
// markdown re-render per token — quadratic in response length). The
// turn-completion refresh renders unthrottled, so the final content always
// lands even when the last delta was throttle-skipped.
const streamRenderThrottleInterval = 50 * time.Millisecond

// streamRenderThrottleMaxInterval caps the adaptive throttle interval so the
// live view never lags more than this behind the stream.
const streamRenderThrottleMaxInterval = 2 * time.Second

// liveRenderInterval is the adaptive minimum gap between live renders: at
// least the base interval, and at least 3x the measured cost of the last
// render. This bounds render work to ~1/3 of the event loop regardless of how
// large the accumulated markdown grows — a fixed interval cannot, because a
// single render's cost keeps growing with content length until it exceeds any
// fixed budget (the "fast at first, then freezes" failure: 120ms spinner ticks
// each demanding a 500ms render).
func (m Model) liveRenderInterval() time.Duration {
	interval := 3 * m.lastLiveRenderCost
	if interval < streamRenderThrottleInterval {
		interval = streamRenderThrottleInterval
	}
	if interval > streamRenderThrottleMaxInterval {
		interval = streamRenderThrottleMaxInterval
	}
	return interval
}

// refreshViewportThrottled renders the live view only when the adaptive
// interval has elapsed since the last throttled render. High-frequency busy
// callers (stream deltas, spinner tick, 1s ticker) MUST use this instead of
// refreshViewport so no single caller can saturate the event loop; low-rate
// and finalization paths call refreshViewport directly for freshness.
func (m *Model) refreshViewportThrottled() {
	if m.now().Sub(m.lastStreamRenderAt) < m.liveRenderInterval() {
		return
	}
	m.refreshViewport()
	m.lastStreamRenderAt = m.now()
}

func (m *Model) refreshViewport() {
	if refreshViewportHook != nil {
		refreshViewportHook()
	}
	renderStart := m.now()
	defer func() {
		m.lastLiveRenderCost = m.now().Sub(renderStart)
	}()
	budget := m.renderBudget()
	m.viewport.Width = budget.ContentWidth
	var b strings.Builder
	if live := m.liveTranscriptView(); live != "" {
		b.WriteString(live)
	}
	content := hardWrapRenderedText(b.String(), budget.ContentWidth)
	// Record whether there is live content so View() can decide to show the
	// viewport without re-rendering the markdown itself (see View()).
	m.hasLiveViewContent = strings.TrimSpace(content) != ""
	m.viewport.Height = m.viewportHeightForContent(content)
	m.viewport.SetContent(content)
	if m.stickToBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) normalViewportHeight() int {
	return max(0, m.height-m.fixedChromeHeight())
}

func (m Model) viewportHeightForContent(content string) int {
	normalHeight := m.normalViewportHeight()
	if normalHeight <= 0 {
		return 0
	}
	contentHeight := visualLineCount(content, m.contentWidth())
	if m.shouldShowWelcome() {
		return max(1, min(normalHeight, contentHeight))
	}
	// Short conversations should keep the prompt near the content. Long ones
	// expand to the available scrollback area under the fixed header.
	return max(min(3, normalHeight), min(normalHeight, contentHeight))
}

func (m Model) fixedChromeHeight() int {
	hasLive := m.liveTranscriptView() != ""
	budget := m.renderBudget()
	return m.chromeHeight(m.bottomChromeParts(m.viewStatus(), hasLive), budget)
}

func (m *Model) refreshViewportAtBottom() {
	m.stickToBottom = true
	m.refreshViewport()
}

func (m Model) contentWidth() int {
	return m.renderBudget().ContentWidth
}

func (m Model) terminalWidth() int {
	return m.renderBudget().SafeWidth
}

func (m Model) chromeWidth() int {
	return m.renderBudget().ChromeWidth
}

func (m Model) renderBudget() renderBudget {
	reportedWidth := m.width
	if reportedWidth <= 0 {
		reportedWidth = 80
	}
	reportedHeight := m.height
	safeWidth := reportedWidth
	if safeWidth > minRenderWidth+renderWidthSafetyMargin {
		safeWidth -= renderWidthSafetyMargin
	}
	safeWidth = max(minRenderWidth, safeWidth)
	contentWidth := max(20, safeWidth-4)
	chromeWidth := max(20, safeWidth-2)
	inputWidth := max(20, safeWidth-2)
	maxChrome := maxBottomChromeHeight
	if reportedHeight > 0 {
		maxChrome = min(maxBottomChromeHeight, max(5, reportedHeight-minViewportHeight))
	}
	return renderBudget{
		ReportedWidth:  reportedWidth,
		ReportedHeight: reportedHeight,
		SafeWidth:      safeWidth,
		SafeHeight:     max(0, reportedHeight),
		ContentWidth:   contentWidth,
		ChromeWidth:    chromeWidth,
		InputWidth:     inputWidth,
		MinViewport:    minViewportHeight,
		MaxChrome:      maxChrome,
	}
}
