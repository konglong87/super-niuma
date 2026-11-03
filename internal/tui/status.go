// status.go 生成状态行文案：响应元信息、耗时标签与省略号/spinner 动画。

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) responseMeta(result QueryResult) string {
	duration := "Responded"
	if !m.turnStarted.IsZero() {
		duration = responseDurationLabel(m.now().Sub(m.turnStarted))
	}
	parts := []string{duration}
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	parts = append(parts, "time="+formatChineseResponseTime(now()))
	if result.Model != "" {
		parts = append(parts, "model="+result.Model)
	} else if m.usage.Model != "" {
		parts = append(parts, "model="+m.usage.Model)
	}
	if result.Turns > 0 {
		turns := strconv.Itoa(result.Turns)
		if result.Context.MaxTurns > 0 {
			turns += "/" + strconv.Itoa(result.Context.MaxTurns)
		}
		parts = append(parts, "turns="+turns)
	}
	if result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0 {
		parts = append(parts, fmt.Sprintf("tokens in/out=%d/%d", result.Usage.InputTokens, result.Usage.OutputTokens))
	}
	cacheCreate, cacheRead := usageCacheTokens(result.Usage)
	if cacheCreate != 0 || cacheRead != 0 {
		parts = append(parts, formatCacheUsage(result.Usage.InputTokens, cacheCreate, cacheRead, "turn_hit"))
	}
	parts = append(parts, "工具 "+strconv.Itoa(len(result.ToolCalls))+" 次")
	if shouldShowUsageStopReason(result.StopReason) {
		parts = append(parts, "stop="+result.StopReason)
	}
	return strings.Join(parts, " · ")
}

func formatChineseResponseTime(t time.Time) string {
	t = t.Local()
	hour := t.Hour()
	period := "上午"
	displayHour := hour
	switch {
	case hour == 0:
		displayHour = 12
	case hour == 12:
		period = "下午"
	case hour > 12:
		period = "下午"
		displayHour = hour - 12
	}
	return fmt.Sprintf("%d年%d月%d号 %s%d点%d分", t.Year(), int(t.Month()), t.Day(), period, displayHour, t.Minute())
}

func responseDurationLabel(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed < time.Second {
		return "Responded in " + strconv.FormatInt(elapsed.Milliseconds(), 10) + "ms"
	}
	return "Responded in " + strconv.Itoa(int(elapsed.Round(time.Second)/time.Second)) + "s"
}

func runningDurationLabel(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	total := int(elapsed.Round(time.Second) / time.Second)
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func runningStatusElapsedPart(status string, elapsed time.Duration) string {
	label := "elapsed"
	if strings.Contains(status, "elapsed=") {
		label = "turn_elapsed"
	}
	return label + "=" + runningDurationLabel(elapsed)
}

func friendlyElapsedDuration(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > 0 && elapsed < time.Second {
		return "<1s"
	}
	return runningDurationLabel(elapsed)
}

func tickEverySecond() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) currentRunningStatus() string {
	status := m.runningStatus
	if strings.TrimSpace(status) == "" {
		status = "niuma is thinking..."
	}
	return animateStatusEllipsis(status, m.spinnerFrame)
}

// runningEllipsisBlank fills the unlit ellipsis slots. U+2800 (braille blank)
// renders as an empty cell, is width-1, and — unlike a space — survives the
// TrimSpace in wrapModeHintParts, so the trailing status stays a constant
// three cells wide and the fields after it never shift.
const runningEllipsisBlank = "⠀"

// runningEllipsis animates a trailing "..." as dots lighting up one at a time
// (".", "..", "...") at a constant three-cell width. Frame 0 renders the full
// "..." so any non-animating render (tests, the first pre-tick paint) shows the
// plain ellipsis.
func runningEllipsis(frame int) string {
	if frame <= 0 {
		return "..."
	}
	switch (frame / 3) % 3 { // advance a step every ~360ms (3 × 120ms ticks)
	case 0:
		return "." + runningEllipsisBlank + runningEllipsisBlank
	case 1:
		return ".." + runningEllipsisBlank
	default:
		return "..."
	}
}

func animateStatusEllipsis(status string, frame int) string {
	if !strings.HasSuffix(status, "...") {
		return status
	}
	return strings.TrimSuffix(status, "...") + runningEllipsis(frame)
}

// startSpinnerTick drives fast (~120ms) repaints while busy so the running
// tool/todo/agent spinners and the status ellipsis animate smoothly. It stops
// itself once idle.
func (m *Model) startSpinnerTick() tea.Cmd {
	if m.spinnerTicking || !m.busy {
		return nil
	}
	m.spinnerTicking = true
	m.spinnerFrame = 0
	return spinnerTickCmd()
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}
