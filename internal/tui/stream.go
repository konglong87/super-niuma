// stream.go 驱动一次 prompt 的执行并把流事件套用到 Model 上。

package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) runPrompt(ctx context.Context, prompt string, attachments []Attachment) tea.Cmd {
	return func() tea.Msg {
		if m.runWithAttachments != nil {
			result, err := m.runWithAttachments(ctx, prompt, attachments)
			return responseMsg{prompt: prompt, attachments: attachments, result: result, err: err}
		}
		if m.runResult != nil {
			result, err := m.runResult(ctx, prompt)
			return responseMsg{prompt: prompt, attachments: attachments, result: result, err: err}
		}
		if m.run == nil {
			return responseMsg{prompt: prompt, attachments: attachments, err: errors.New("query function is not configured")}
		}
		text, err := m.run(ctx, prompt)
		return responseMsg{prompt: prompt, attachments: attachments, result: QueryResult{Response: text}, err: err}
	}
}

func (m Model) runPromptStream(ctx context.Context, prompt string, events chan StreamEvent) tea.Cmd {
	return func() tea.Msg {
		defer close(events)
		if m.runStream == nil {
			events <- StreamEvent{Type: StreamFinished, Err: errors.New("stream query function is not configured")}
			return streamProducerDoneMsg{}
		}
		err := m.runStream(ctx, prompt, events)
		events <- StreamEvent{Type: StreamFinished, Err: err}
		return streamProducerDoneMsg{}
	}
}

func (m Model) runPromptStreamWithAttachments(ctx context.Context, prompt string, attachments []Attachment, events chan StreamEvent) tea.Cmd {
	return func() tea.Msg {
		defer close(events)
		if m.runStreamWithAttachments == nil {
			events <- StreamEvent{Type: StreamFinished, Err: errors.New("stream query function is not configured")}
			return streamProducerDoneMsg{}
		}
		err := m.runStreamWithAttachments(ctx, prompt, attachments, events)
		events <- StreamEvent{Type: StreamFinished, Err: err}
		return streamProducerDoneMsg{}
	}
}

func waitForStreamEvent(ch <-chan StreamEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return streamEventMsg{event: event, ch: ch}
	}
}

type streamEventEffects struct {
	phaseBoundary bool
}

func (m *Model) applyStreamEvent(event StreamEvent) streamEventEffects {
	effects := streamEventEffects{phaseBoundary: m.shouldCommitBefore(event.Type)}
	if effects.phaseBoundary {
		m.closeDisplayTextAppendWindow()
	}
	if event.Type != StreamThinking {
		m.closeHiddenThinkingArchive()
	}
	switch event.Type {
	case StreamUsage:
		m.runningStatus = ""
		if event.Result != nil {
			m.applyQueryResult(*event.Result)
			m.pendingStreamResult = *event.Result
			m.hasPendingStreamResult = true
		}
	case StreamConfigReload:
		m.applyConfigReload(event)
	case StreamSessionResume:
		m.applySessionResume(event)
	case StreamRecap:
		m.applyRecap(event)
	// StreamNextSteps 没有 case：唯一的生产者是 runNextStepsCmd 自建、自己
	// drain 的 channel（见 next_steps.go），从不写主 stream channel。主 channel
	// 在 StreamFinished 之后已被生产者 close，这里加一个「看着能收」的分支
	// 只会诱使以后有人真的往主 channel 塞 StreamNextSteps，从而复现刚修过的
	// 「写入已被生产者关闭的 channel」那个 panic。
	case StreamThinking:
		m.runningStatus = "niuma is reasoning..."
		text := event.Text
		if text == "" {
			return effects
		}
		if m.thinkingMode == thinkingModeHidden {
			m.appendHiddenThinking(text)
			return effects
		}
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "thinking" {
			if strings.TrimSpace(text) == "" {
				return effects
			}
			m.messages = append(m.messages, message{role: "thinking", content: text})
			m.appendDisplayTextSegment("thinking", text)
			m.ensureLiveMessageDisplayBlock(len(m.messages) - 1)
			return effects
		}
		m.messages[len(m.messages)-1].content += text
		m.appendDisplayTextSegment("thinking", text)
		m.ensureLiveMessageDisplayBlock(len(m.messages) - 1)
	case StreamText:
		m.runningStatus = "niuma is writing..."
		if event.Text == "" {
			return effects
		}
		if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "assistant" {
			m.messages = append(m.messages, message{role: "assistant", content: event.Text})
			m.appendDisplayTextSegment("assistant", event.Text)
			m.ensureLiveMessageDisplayBlock(len(m.messages) - 1)
			return effects
		}
		m.messages[len(m.messages)-1].content += event.Text
		m.appendDisplayTextSegment("assistant", event.Text)
		m.ensureLiveMessageDisplayBlock(len(m.messages) - 1)
	case StreamTextAmended:
		m.amendLiveAssistantText(event.PrevText, event.Text)
	case StreamToolStart:
		m.ensureDisplayTimelineForUnprintedMessages()
		m.ensureCurrentLiveMessageDisplayBlocks()
		m.ensureLiveToolDisplayBlock()
		if current := m.currentTodo(); current != nil {
			m.runningStatus = "Task: " + truncate(oneLine(todoDisplayText(*current)), max(24, m.width-18))
		} else {
			toolLabel := toolDisplayName(firstNonEmpty(event.ToolName, event.ToolID, ""))
			if summary := toolInputSummary(event.ToolName, event.Output); summary != "" {
				toolLabel += " " + summary
			}
			m.runningStatus = truncate("niuma is using "+toolLabel+"...", max(32, m.width-4))
		}
		m.startToolActivity(event)
		m.appendDisplayToolFromEvent(event)
	case StreamToolResult:
		m.ensureDisplayTimelineForUnprintedMessages()
		m.ensureCurrentLiveMessageDisplayBlocks()
		m.ensureLiveToolDisplayBlock()
		m.runningStatus = "niuma is processing tool results..."
		m.finishToolActivity(event)
		m.updateDisplayToolFromEvent(event)
		m.syncTodosFromToolEvent(event)
		m.refreshRunningStatusFromCurrentTodo()
	case StreamNestedProgress:
		m.runningStatus = "niuma is coordinating sub-agents..."
		if event.TaskID == 0 {
			if event.IsError && strings.TrimSpace(event.Output) != "" {
				m.runningStatus = "Sub-agent error: " + truncate(oneLine(event.Output), max(24, m.width-24))
			}
			return effects
		}
		m.ensureDisplayTimelineForUnprintedMessages()
		m.ensureCurrentLiveMessageDisplayBlocks()
		m.ensureLiveAgentDisplayBlock()
		m.updateAgentProgress(event)
		m.upsertDisplayAgentProgress()
		if status := m.subagentRunningStatus(event.TaskID); status != "" {
			m.runningStatus = status
		}
	}
	return effects
}

func (m Model) shouldCommitBefore(event StreamEventType) bool {
	last, ok := m.displayTimeline.lastUnprintedSegment(m.pendingTranscriptSeq())
	if !ok || last.status != displaySegmentStreaming {
		return false
	}
	switch event {
	case StreamText:
		return last.kind == displaySegmentThinking
	case StreamThinking:
		// Hidden Thinking stays in UI memory for an explicit detail request, but it
		// must not commit or split the visible assistant phase around it.
		if m.thinkingMode == thinkingModeHidden {
			return false
		}
		return last.kind == displaySegmentAssistantText
	case StreamToolStart, StreamNestedProgress:
		return last.kind == displaySegmentThinking || last.kind == displaySegmentAssistantText
	default:
		return false
	}
}

func (m *Model) applyStreamFinishedEvent(event StreamEvent) tea.Cmd {
	m.busy = false
	m.streamingActive = false
	m.pendingPermission = nil
	m.pendingQuestion = nil
	m.clearNextSteps()
	m.turnCancel = nil
	m.turnStopping = false
	m.runningStatus = ""
	m.noteActivity()
	m.err = event.Err
	if event.Result != nil {
		m.applyQueryResult(*event.Result)
		m.syncFinalAssistantResponse(event.Result.Response)
		m.pendingStreamResult = *event.Result
		m.hasPendingStreamResult = true
	}
	if event.Err != nil {
		m.markInterruptedProgress(event.Err)
		m.appendDisplayMessage("error", friendlyStreamErrorMessage(event.Err), "")
	} else if m.hasPendingStreamResult {
		m.applyAssistantMeta(m.responseMeta(m.pendingStreamResult))
	}
	m.markDisplayTurnDone()
	if event.Err == nil {
		m.archiveToolActivityToAssistant()
		m.archiveAgentProgressToAssistant()
		m.armAwayRecap()
	}
	cmd := m.prepareTranscriptFlushCmd(transcriptFlushTurnComplete)
	m.resetLiveDisplayBlocks()
	m.pendingStreamResult = QueryResult{}
	m.hasPendingStreamResult = false
	return cmd
}

func (m *Model) markInterruptedProgress(err error) {
	reason := friendlyStreamErrorStatus(err)
	if strings.TrimSpace(reason) == "" {
		reason = "本轮已中断"
	}
	now := m.now()
	for i := range m.toolActivity {
		if strings.EqualFold(strings.TrimSpace(m.toolActivity[i].Status), "running") {
			m.toolActivity[i].Status = "interrupted"
			m.toolActivity[i].IsError = true
			m.toolActivity[i].Result = reason
			if m.toolActivity[i].Detail == "" {
				m.toolActivity[i].Detail = "命令未完成"
			}
			if !m.toolActivity[i].Started.IsZero() {
				m.toolActivity[i].Elapsed = now.Sub(m.toolActivity[i].Started)
			}
			m.upsertDisplayTool(m.toolActivity[i])
		}
	}
	agentChanged := false
	for taskID, progress := range m.agentProgress {
		if progressIsRunning(progress.status) {
			progress.status = "interrupted"
			progress.detail = reason
			startedAt := strings.TrimSpace(progress.startedAt)
			if progress.durationMS == "" && startedAt != "" {
				if started, parseErr := time.Parse(time.RFC3339Nano, startedAt); parseErr == nil {
					progress.durationMS = strconv.FormatInt(now.Sub(started).Milliseconds(), 10)
				}
			}
			progress.updatedAt = now
			m.agentProgress[taskID] = progress
			agentChanged = true
		}
	}
	if agentChanged {
		m.upsertDisplayAgentProgress()
	}
}

func (m *Model) applySessionResume(event StreamEvent) {
	m.messages = nil
	m.toolActivity = nil
	m.recentAgentEvidence = ""
	m.agentProgress = map[uint64]agentProgress{}
	m.agentOrder = nil
	m.displayTimeline = displayTimeline{}
	m.usage = usagePanel{}
	m.runningStatus = ""
	m.transcriptPrintedHeader = false
	m.transcriptPrintedCount = 0
	m.transcriptPrintedSeq = 0
	m.transcriptCommittingSeq = 0
	m.transcriptPhaseCommittingSeq = 0
	m.phaseBoundaryStreamCh = nil
	m.recapSuppressedSeq = 0
	m.recapSuppressedCount = 0
	m.pendingStreamResult = QueryResult{}
	m.hasPendingStreamResult = false
	m.thinkingDetailTurn = 0
	m.thinkingArchive = nil
	m.currentTurn = 0
	if event.Welcome != nil {
		m.welcome = *event.Welcome
		if !m.thinkingModeExplicit {
			m.thinkingMode = resolveThinkingMode(m.welcome)
		}
	}
	if strings.TrimSpace(m.welcome.SessionStatus) == "" {
		m.welcome.SessionStatus = "resumed"
	}
	m.loadThinkingArchive(event.Thinking)
	m.loadTodosFromDisk()
	text := strings.TrimSpace(event.Text)
	if text != "" {
		m.appendDisplayMessage("status", text, "")
	}
	for _, history := range event.History {
		m.appendInitialDisplayMessage(history)
	}
}

func (m *Model) applyRecap(event StreamEvent) {
	text := strings.TrimSpace(event.Text)
	if text == "" {
		return
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "recap" {
			if i < m.transcriptPrintedCount {
				break
			}
			m.messages[i].content = text
			m.updateLatestDisplaySegmentContent(displaySegmentRecap, text)
			return
		}
	}
	m.appendDisplayMessage("recap", text, "")
}

func (m *Model) updateLatestDisplaySegmentContent(kind displaySegmentKind, content string) bool {
	for i := len(m.displayTimeline.segments) - 1; i >= 0; i-- {
		if m.displayTimeline.segments[i].kind == kind && m.displayTimeline.segments[i].seq > m.transcriptPrintedSeq {
			m.displayTimeline.segments[i].content = content
			m.displayTimeline.segments[i].updatedAt = m.now()
			return true
		}
	}
	return false
}

func (m *Model) syncFinalAssistantResponse(response string) {
	response = strings.TrimSpace(response)
	if response == "" {
		return
	}
	foundMessage := false
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" {
			m.messages[i].content = response
			foundMessage = true
			break
		}
	}
	if !foundMessage {
		m.messages = append(m.messages, message{role: "assistant", content: response})
	}
	target := -1
	count := 0
	for i := range m.displayTimeline.segments {
		seg := m.displayTimeline.segments[i]
		if seg.kind == displaySegmentAssistantText && seg.seq > m.transcriptPrintedSeq {
			target = i
			count++
		}
	}
	switch count {
	case 0:
		m.appendDisplaySegment(displaySegment{
			kind:    displaySegmentAssistantText,
			role:    "assistant",
			status:  displaySegmentDone,
			content: response,
		})
	case 1:
		m.displayTimeline.segments[target].content = response
		m.displayTimeline.segments[target].updatedAt = m.now()
	}
}

func (m *Model) applyConfigReload(event StreamEvent) {
	oldModel := strings.TrimSpace(m.welcome.Model)
	oldProvider := strings.TrimSpace(m.welcome.Provider)
	if event.Welcome != nil {
		m.welcome = *event.Welcome
		if !m.thinkingModeExplicit {
			m.thinkingMode = resolveThinkingMode(m.welcome)
		}
	}
	newModel := strings.TrimSpace(m.welcome.Model)
	newProvider := strings.TrimSpace(m.welcome.Provider)
	text := strings.TrimSpace(event.Text)
	if event.NoticeKind == StreamNoticeConfigReload {
		m.appendConfigNotice(text)
		return
	}
	if text == "" {
		changes := []string{}
		if oldModel != "" && newModel != "" && oldModel != newModel {
			changes = append(changes, "model "+oldModel+" → "+newModel)
		}
		if oldProvider != "" && newProvider != "" && oldProvider != newProvider {
			changes = append(changes, "provider "+oldProvider+" → "+newProvider)
		}
		if len(changes) == 0 {
			return
		}
		m.appendConfigNotice(strings.Join(changes, configNoticeSeparator))
		return
	}
	m.appendDisplayMessage("status", text, "")
}

func (m *Model) applyAssistantMeta(meta string) {
	if strings.TrimSpace(meta) == "" {
		return
	}
	if i, ok := m.latestUnprintedAssistantIndex(); ok {
		m.appendOrUpdateDisplayMeta(meta)
		m.messages[i].meta = meta
		m.ensureLiveMessageDisplayBlock(i)
		m.ensureLiveAssistantMetaDisplayBlock()
	}
}

func (m *Model) applyQueryResult(result QueryResult) {
	panel := m.usage
	if result.Model != "" {
		panel.Model = result.Model
	}
	if result.StopReason != "" {
		panel.StopReason = result.StopReason
	}
	if result.Turns > 0 {
		panel.Turns += result.Turns
	}
	if result.SessionID != "" {
		panel.SessionID = result.SessionID
	}
	cacheCreate, cacheRead := usageCacheTokens(result.Usage)
	if result.Usage.InputTokens != 0 || result.Usage.OutputTokens != 0 || cacheCreate != 0 || cacheRead != 0 {
		panel.InputTokens += result.Usage.InputTokens
		panel.OutputTokens += result.Usage.OutputTokens
		lastInput := result.Usage.LastInputTokens
		if lastInput == 0 {
			lastInput = result.Usage.InputTokens
		}
		if lastInput > 0 {
			panel.LastInput = lastInput
		}
		panel.CacheCreate += cacheCreate
		panel.CacheRead += cacheRead
		panel.ServiceTier = result.Usage.ServiceTier
		panel.InferenceGeo = result.Usage.InferenceGeo
		panel.Speed = result.Usage.Speed
	}
	panel.ToolCalls += len(result.ToolCalls)
	for _, call := range result.ToolCalls {
		if call.IsError && isToolGateBlocked(call.Output) {
			panel.ToolBlocked++
		} else if call.IsError {
			panel.ToolErrors++
		}
		if call.Name != "" {
			panel.LastTool = call.Name
		}
	}
	if result.Context.CWD != "" {
		panel.CWD = result.Context.CWD
	}
	if result.Context.MaxTurns > 0 {
		panel.MaxTurns = result.Context.MaxTurns
	}
	if result.Context.MaxTokens > 0 {
		panel.MaxTokens = result.Context.MaxTokens
	}
	if result.Context.ToolCount > 0 {
		panel.ToolCount = result.Context.ToolCount
	}
	if result.Context.ContextHint != "" {
		panel.ContextHint = result.Context.ContextHint
	}
	if result.Context.ContextWindow > 0 {
		panel.ContextWindow = result.Context.ContextWindow
	}
	m.usage = panel
}
