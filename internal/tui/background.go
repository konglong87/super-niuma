// background.go 处理后台任务轮询与离开期间的 away recap。

package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) pollBackground() tea.Cmd {
	if m.watchBackground == nil {
		return nil
	}
	return func() tea.Msg {
		updates, err := m.watchBackground(m.ctx, cloneBackgroundSnapshots(m.backgroundSnapshots))
		return backgroundPollMsg{updates: updates, err: err}
	}
}

func (m Model) scheduleBackgroundPoll() tea.Cmd {
	if m.watchBackground == nil {
		return nil
	}
	interval := m.backgroundPollInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return m.pollBackground()()
	})
}

func cloneBackgroundSnapshots(in map[string]BackgroundSnapshot) map[string]BackgroundSnapshot {
	out := make(map[string]BackgroundSnapshot, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// applyBackgroundUpdate applies a single background update and reports whether
// it appended a visible transcript message. Silent updates and the
// __scheduler_events__ bookkeeping entry only refresh internal snapshots and
// return false, so callers can avoid flushing an empty transcript.
func (m *Model) applyBackgroundUpdate(update BackgroundUpdate) bool {
	if update.ID == "" {
		return false
	}
	if update.ID == "__scheduler_events__" {
		m.backgroundSnapshots[update.ID] = BackgroundSnapshot{EventOffset: int64(update.LogSize)}
		return false
	}
	m.backgroundSnapshots[update.ID] = BackgroundSnapshot{
		RunCount: update.RunCount,
		LogSize:  update.LogSize,
		Status:   update.Status,
	}
	if update.Silent {
		return false
	}
	title := "Background command updated"
	if update.Kind == "loop" || update.ScheduleID != "" {
		title = "Scheduled task fired"
	}
	if update.LastRunAt != nil && (update.Kind == "loop" || update.ScheduleID != "") {
		title = "Scheduled task fired at " + update.LastRunAt.Local().Format("15:04:05")
	} else if update.Status == "completed" || update.Status == "failed" || update.Status == "killed" {
		title = "Background command " + update.Status
	}
	meta := strings.TrimSpace(update.Prompt)
	if meta == "" {
		meta = update.ID
	}
	if update.ScheduleID != "" {
		meta = meta + " · " + update.ScheduleID
	}
	text := strings.TrimSpace(update.LogTail)
	if text == "" {
		text = "No output yet. Use /logs " + update.ID + " for full background logs."
	}
	role := "background"
	if update.Kind == "loop" || update.ScheduleID != "" {
		role = "loop"
	}
	m.appendDisplayMessage(role, title+"\n"+text, meta)
	return true
}

func (m *Model) noteActivity() {
	if m.now != nil {
		m.lastActivity = m.now()
	} else {
		m.lastActivity = time.Now()
	}
	m.awayRecapArmed = false
	m.awayRecapArmedAt = time.Time{}
	m.invalidateRecapJob()
}

func (m *Model) invalidateRecapJob() {
	if m.recapCancel != nil {
		m.recapCancel()
		m.recapCancel = nil
	}
	// Increment even when the command has already returned: its tea.Msg may still
	// be queued behind the user event and must not mutate the next turn.
	m.recapGeneration++
	m.awayRecapRunning = false
}

func (m *Model) beginRecapJob() (context.Context, uint64) {
	if m.recapCancel != nil {
		m.recapCancel()
	}
	m.recapGeneration++
	ctx, cancel := context.WithCancel(m.ctx)
	m.recapCancel = cancel
	return ctx, m.recapGeneration
}

func (m *Model) finishRecapJob(generation uint64) {
	if generation != m.recapGeneration {
		return
	}
	if m.recapCancel != nil {
		m.recapCancel()
		m.recapCancel = nil
	}
}

func (m *Model) armAwayRecap() {
	if m.now != nil {
		m.awayRecapArmedAt = m.now()
	} else {
		m.awayRecapArmedAt = time.Now()
	}
	m.awayRecapArmed = true
}

func (m *Model) maybeStartAwayRecap(now time.Time) tea.Cmd {
	if m.runAwayRecap == nil || m.awayRecapDelay <= 0 || !m.awayRecapArmed || m.awayRecapRunning {
		return nil
	}
	if m.busy || m.pendingPermission != nil || m.pendingQuestion != nil || m.resumePickerActive() || m.rewindPickerActive() {
		return nil
	}
	if strings.TrimSpace(m.textarea.Value()) != "" || len(m.slashSuggestions) > 0 || m.slashErr != nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	startedAt := m.awayRecapArmedAt
	if startedAt.IsZero() {
		startedAt = m.lastActivity
	}
	if startedAt.IsZero() || now.Sub(startedAt) < m.awayRecapDelay {
		return nil
	}
	m.awayRecapArmed = false
	m.awayRecapArmedAt = time.Time{}
	m.awayRecapRunning = true
	ctx, generation := m.beginRecapJob()
	return m.runAwayRecapCmd(ctx, generation)
}

func (m Model) runAwayRecapCmd(ctx context.Context, generation uint64) tea.Cmd {
	return func() tea.Msg {
		text, err := collectRecap(ctx, m.runAwayRecap)
		return awayRecapMsg{generation: generation, text: text, err: err}
	}
}

// runPostTurnRecapCmd 在一轮结束后生成会话回顾。channel 由它调用的
// collectRecap 持有（不变式见 collectRecap 的注释）：主 stream channel 在
// StreamFinished 之后既已被生产者的 defer close 关闭，也不再被读取，往里发送
// 还会 panic，所以不能复用。
func (m *Model) runPostTurnRecapCmd() tea.Cmd {
	if m.runPostTurnRecap == nil {
		return nil
	}
	ctx, generation := m.beginRecapJob()
	return func() tea.Msg {
		text, err := collectRecap(ctx, m.runPostTurnRecap)
		return postTurnRecapMsg{generation: generation, text: text, err: err}
	}
}

// collectRecap 是 runAwayRecapCmd 与 runPostTurnRecapCmd 共用的 channel 归属
// 不变式：自己建 channel、同步调用 run、close、drain 出最后一条 StreamRecap
// 文本。两个调用方各自的 nil-guard 保持不变——这里不加、也不删。
func collectRecap(ctx context.Context, run AwayRecapFunc) (string, error) {
	events := make(chan StreamEvent, 8)
	err := run(ctx, events)
	close(events)
	var text string
	for event := range events {
		if event.Type == StreamRecap {
			text = event.Text
		}
	}
	return text, err
}
