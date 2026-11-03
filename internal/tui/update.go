// update.go 只放 Update 事件分派：键盘/鼠标/流事件到各功能模块的入口。

package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.applyWindowSize(msg.Width, msg.Height)
		if !m.transcriptPrintedHeader && m.initialTranscriptPending {
			cmd := m.prepareTranscriptFlushCmd(transcriptFlushInitial)
			m.refreshViewport()
			return m, cmd
		}
		return m, nil
	case welcomeDirectFrameResetMsg:
		m.forceDirectWelcomeFrame = false
		return m, nil
	case tea.KeyMsg:
		m.noteActivity()
		if msg.String() == "shift+tab" && m.switchPermission != nil {
			return m.switchPermissionMode()
		}
		if m.pendingPermission != nil {
			return m.handlePermissionKey(msg)
		}
		if m.pendingQuestion != nil {
			return m.handleQuestionKey(msg)
		}
		if m.resumePickerActive() {
			return m.handleResumePickerKey(msg)
		}
		if m.rewindPickerActive() {
			return m.handleRewindPickerKey(msg)
		}
		if m.thinkingDetailActive() && msg.String() == "esc" {
			m.closeThinkingDetail()
			m.refreshViewportAtBottom()
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "esc":
			if len(m.slashSuggestions) > 0 || m.slashErr != nil {
				m.clearSlashSuggestions()
				return m, nil
			}
			if msg.String() == "esc" && m.nextStepsActive() {
				m.clearNextSteps()
				m.refreshViewport()
				return m, nil
			}
			if msg.String() == "ctrl+c" && m.busy && !m.turnStopping {
				if m.turnCancel != nil {
					m.turnCancel()
				}
				m.turnStopping = true
				m.err = errors.New("stopping current response; press Ctrl+C again to exit")
				m.refreshViewport()
				return m, nil
			}
			return m, tea.Quit
		case "up", "ctrl+p":
			if m.slashSuggestionsActive() {
				m.moveSlashSelection(-1)
				return m, nil
			}
		case "down", "ctrl+n":
			if m.slashSuggestionsActive() {
				m.moveSlashSelection(1)
				return m, nil
			}
		case "ctrl+down":
			if m.selectNextPendingInput() {
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+up":
			if m.moveSelectedPendingInputUp() {
				m.refreshViewport()
				return m, nil
			}
		case "tab":
			if m.slashSuggestionsActive() {
				m.applySlashSuggestion()
				return m, nil
			}
		case "1", "2", "3", "4", "5":
			if m.nextStepsActive() && m.applyNextStepSuggestion(int(msg.String()[0]-'1')) {
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+o":
			return m.toggleMouseTracking()
		case "ctrl+e":
			if m.hasVisibleAgentProgress() {
				m.agentPanelExpanded = !m.agentPanelExpanded
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+t":
			if len(m.toolActivity) > 0 || m.hasArchivedToolActivity() {
				m.toolPanelExpanded = !m.toolPanelExpanded
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+y":
			if len(m.todos) > 0 {
				m.todoPanelExpanded = !m.todoPanelExpanded
				m.refreshViewport()
				return m, nil
			}
		case "ctrl+u", "cmd+delete", "command+delete":
			if m.busy {
				return m, nil
			}
			if m.clearInputBuffer() {
				return m, nil
			}
		case "ctrl+d":
			if m.busy {
				return m, nil
			}
			if m.removeLastAttachment() {
				return m, nil
			}
		case "shift+enter", "alt+enter", "ctrl+j":
			if m.busy {
				return m, nil
			}
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if m.resizeTextareaToContent() {
				m.refreshViewport()
			}
			m.updateSlashSuggestions()
			m.saveDraft()
			return m, cmd
		case "ctrl+v":
			if m.importClipboard == nil {
				break
			}
			attachment, ok, err := m.importClipboard(m.ctx)
			if err != nil {
				m.err = err
				return m, nil
			}
			if ok {
				attachment.ID = m.nextAttachmentID
				m.nextAttachmentID++
				m.attachments = append(m.attachments, attachment)
				m.insertTextAtCursor(formatImageRef(attachment.ID))
				m.clipboardHasImage = false
				m.err = nil
				m.saveDraft()
				return m, nil
			}
		case "enter":
			prompt := strings.TrimSpace(m.textarea.Value())
			if prompt == "" && len(m.attachments) == 0 {
				return m, nil
			}
			runPrompt := normalizeBareInteractiveCommand(prompt)
			if m.handlePendingInputCommand(runPrompt) {
				m.resetTextarea()
				m.clearDraft()
				m.refreshViewport()
				if !m.busy && shouldResumePendingInputCommand(runPrompt) {
					if next, nextCmd := m.startNextPendingInput(); nextCmd != nil {
						return next, nextCmd
					}
				}
				return m, nil
			}
			if m.busy {
				if m.queuePendingInput() {
					return m, nil
				}
				return m, nil
			}
			if m.shouldCompleteSlashOnEnter() {
				m.applySlashSuggestion()
				return m, nil
			}
			if runPrompt == "/exit" || runPrompt == "/quit" {
				return m, tea.Quit
			}
			if m.handleThinkingCommand(runPrompt) {
				m.resetTextarea()
				m.attachments = nil
				m.clearDraft()
				m.clearSlashSuggestions()
				m.refreshViewport()
				if m.thinkingDetailActive() {
					m.viewport.GotoTop()
				}
				return m, nil
			}
			if isBareResumePrompt(runPrompt) && m.resumeSessions != nil {
				return m.openResumePicker()
			}
			if isBareRewindPrompt(runPrompt) && m.rewindCandidates != nil {
				return m.openRewindPicker(runPrompt)
			}
			attachments := referencedAttachments(prompt, m.attachments)
			runPromptForRun := promptWithAttachments(runPrompt, attachments)
			m.resetTextarea()
			m.attachments = nil
			m.clearDraft()
			m.clearSlashSuggestions()
			m.resetLiveDisplayBlocks()
			m.currentTurn++
			m.appendDisplayMessage("user", prompt, attachmentMeta(attachments))
			m.lastTurnPrompt = runPrompt
			m.busy = true
			m.streamingActive = m.runStreamWithAttachments != nil || m.runStream != nil
			m.pendingStreamResult = QueryResult{}
			m.hasPendingStreamResult = false
			m.turnStopping = false
			m.err = nil
			m.runningStatus = "niuma is preparing context..."
			turnCtx, cancel := context.WithCancel(m.ctx)
			m.turnCancel = cancel
			m.turnStarted = time.Now()
			printCmd := m.prepareTranscriptFlushCmd(transcriptFlushUserSubmit)
			m.refreshViewport()
			if m.runStreamWithAttachments != nil {
				events := make(chan StreamEvent, 32)
				return m, tea.Sequence(printCmd, tea.Batch(m.runPromptStreamWithAttachments(turnCtx, runPrompt, attachments, events), waitForStreamEvent(events)))
			}
			if m.runStream != nil {
				events := make(chan StreamEvent, 32)
				return m, tea.Sequence(printCmd, tea.Batch(m.runPromptStream(turnCtx, runPromptForRun, events), waitForStreamEvent(events)))
			}
			if m.runWithAttachments != nil {
				return m, tea.Sequence(printCmd, m.runPrompt(turnCtx, runPrompt, attachments))
			}
			return m, tea.Sequence(printCmd, m.runPrompt(turnCtx, runPromptForRun, attachments))
		}
		if m.isViewportScrollKey(msg) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}
	case tea.MouseMsg:
		m.noteActivity()
		if m.resumePickerActive() {
			return m.handleResumePickerMouse(msg)
		}
		if m.rewindPickerActive() {
			return m.handleRewindPickerMouse(msg)
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case streamEventMsg:
		if msg.event.Type == StreamPermissionRequest && msg.event.Permission != nil && msg.event.Reply != nil {
			m.pendingPermission = &pendingPermission{request: *msg.event.Permission, reply: msg.event.Reply, ch: msg.ch}
			// 先 flush 再 refreshViewport：卡片会把 viewport 往上挤，已完成的块必须先落进
			// 真实 scrollback，refreshViewport 之后 viewport 才不会重复显示它们。
			printCmd := m.prepareTranscriptFlushCmd(transcriptFlushInteractivePrompt)
			m.refreshViewport()
			return m, printCmd
		}
		if msg.event.Type == StreamUserQuestion && msg.event.Question != nil && msg.event.QuestionReply != nil {
			m.pendingQuestion = &pendingUserQuestion{
				request: *msg.event.Question,
				reply:   msg.event.QuestionReply,
				ch:      msg.ch,
			}
			printCmd := m.prepareTranscriptFlushCmd(transcriptFlushInteractivePrompt)
			m.refreshViewport()
			return m, printCmd
		}
		if msg.event.Type == StreamFinished {
			// 先取 result：applyStreamFinishedEvent 会清掉 pending 状态。
			result := m.pendingStreamResult
			if msg.event.Result != nil {
				result = *msg.event.Result
			}
			prompt := m.lastTurnPrompt
			cmd := m.applyStreamFinishedEvent(msg.event)
			m.finishActivePendingInput(msg.event.Err)
			m.refreshViewport()
			// 只有正常结束才请求候选/回顾；失败/取消的一轮没有可引导的下一步。
			if msg.event.Err == nil {
				cmds := []tea.Cmd{cmd}
				if next := m.runNextStepsCmd(prompt, result); next != nil {
					cmds = append(cmds, next)
				}
				// 回顾额外要求本轮真的产出过结果：slash 命令（/help、/model 等）
				// 也会走到这个分支且 err == nil，但从不经过 StreamUsage、也就不会
				// 产出 assistant 回复——不该为它们再触发一次 provider 调用加一条
				// 会话回顾（regression：StreamFinished 现在覆盖了 slash 命令，
				// 旧实现里 slash 命令在到达回顾调用点之前就已经返回）。
				if strings.TrimSpace(result.Response) != "" {
					if recapCmd := m.runPostTurnRecapCmd(); recapCmd != nil {
						cmds = append(cmds, recapCmd)
					}
				}
				if next, nextCmd := m.startNextPendingInput(); nextCmd != nil {
					return next, tea.Sequence(tea.Batch(cmds...), nextCmd)
				}
				if len(cmds) > 1 {
					return m, tea.Batch(cmds...)
				}
			}
			return m, cmd
		}
		effects := m.applyStreamEvent(msg.event)
		if msg.event.Type == StreamSessionResume {
			printCmd := m.prepareTranscriptFlushCmd(transcriptFlushSessionResume)
			m.refreshViewport()
			return m, tea.Sequence(printCmd, waitForStreamEvent(msg.ch))
		}
		if effects.phaseBoundary {
			printCmd := m.prepareTranscriptFlushCmd(transcriptFlushPhaseBoundary)
			if printCmd == nil {
				m.lastStreamRenderAt = time.Time{}
				m.refreshViewport()
				return m, waitForStreamEvent(msg.ch)
			}
			m.phaseBoundaryStreamCh = msg.ch
			m.lastStreamRenderAt = time.Time{}
			m.refreshViewport()
			return m, printCmd
		}
		// Text/thinking deltas are high-frequency and purely additive: some
		// providers stream ~1 delta per token, so re-rendering the full markdown
		// buffer on every delta is quadratic in response length. Throttle these
		// to at most one render per interval — content is already accumulated in
		// the segment, and the turn-completion refresh (plus the 1s busy ticker)
		// guarantees the final content renders even if the last delta is skipped.
		// All other event types refresh immediately so tool/permission/etc. stay
		// responsive.
		if msg.event.Type == StreamText || msg.event.Type == StreamThinking {
			m.refreshViewportThrottled()
		} else {
			// A non-text/thinking event is a phase change (tool start/result,
			// etc.). Reset the throttle window so the first text/thinking delta
			// after it renders immediately — the running-status line and content
			// must update promptly on a phase transition. Throttling then applies
			// only within a continuous text/thinking burst.
			m.lastStreamRenderAt = time.Time{}
			m.refreshViewport()
		}
		return m, waitForStreamEvent(msg.ch)
	case streamClosedMsg:
		m.streamingActive = false
		m.refreshViewport()
		return m, nil
	case streamProducerDoneMsg:
		return m, nil
	case tickMsg:
		if m.busy {
			m.refreshViewportThrottled()
		}
		cmds := []tea.Cmd{tickEverySecond(), m.checkClipboardImage(), m.maybeStartAwayRecap(time.Time(msg))}
		if c := m.startMascotTick(); c != nil {
			cmds = append(cmds, c)
		}
		if c := m.startSpinnerTick(); c != nil {
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	case spinnerTickMsg:
		if m.busy {
			m.spinnerFrame++
			// Throttled: during a long streamed reply a full render can cost
			// hundreds of ms; an unthrottled render every 120ms tick saturates
			// the event loop and starves stream deltas and Ctrl+C.
			m.refreshViewportThrottled()
			return m, spinnerTickCmd()
		}
		m.spinnerTicking = false
		return m, nil
	case welcomeMascotTickMsg:
		if m.shouldAnimateMascot() {
			m.refreshViewport()
			return m, welcomeMascotTickCmd()
		}
		m.mascotTicking = false
		return m, nil
	case backgroundPollMsg:
		if msg.err != nil {
			m.err = msg.err
		} else if len(msg.updates) > 0 {
			appended := false
			for _, update := range msg.updates {
				if m.applyBackgroundUpdate(update) {
					appended = true
				}
			}
			m.err = nil
			// Silent / scheduler-events updates append no visible message.
			// Flushing anyway would commit the welcome header into scrollback
			// while the live welcome is still shown, duplicating the header.
			if !appended {
				return m, m.scheduleBackgroundPoll()
			}
			cmd := m.prepareTranscriptFlushCmd(transcriptFlushBackground)
			m.refreshViewport()
			return m, tea.Batch(cmd, m.scheduleBackgroundPoll())
		}
		return m, m.scheduleBackgroundPoll()
	case clipboardImageHintMsg:
		if msg.err == nil {
			m.clipboardHasImage = msg.ok
		}
		return m, nil
	case awayRecapMsg:
		if msg.generation != m.recapGeneration {
			return m, nil
		}
		m.awayRecapRunning = false
		m.finishRecapJob(msg.generation)
		if msg.err != nil {
			m.err = msg.err
		} else if strings.TrimSpace(msg.text) != "" {
			m.applyRecap(StreamEvent{Type: StreamRecap, Text: msg.text})
			m.err = nil
		}
		m.refreshViewport()
		return m, nil
	case nextStepsMsg:
		// 失败静默：一条错误的引导比没有引导更糟，不设 m.err 打扰用户。
		if msg.err == nil {
			m.applyNextSteps(msg.event)
			m.refreshViewport()
		}
		return m, nil
	case postTurnRecapMsg:
		if msg.generation != m.recapGeneration {
			return m, nil
		}
		m.finishRecapJob(msg.generation)
		// 与 awayRecapMsg 不同，这里不把 msg.err 写进 m.err：away recap 只在用户
		// 离开时触发一次，失败值得在其回来时提示；post-turn recap 每轮结束都可能
		// 触发，把偶发的生成失败弹成错误行会比 nextStepsMsg 的静默失败更打扰人，
		// 而且这本来就是原实现的既有行为（原代码里生成失败只是 return，不上报）。
		if msg.err == nil && strings.TrimSpace(msg.text) != "" {
			m.applyRecap(StreamEvent{Type: StreamRecap, Text: msg.text})
			m.refreshViewport()
		}
		return m, nil
	case responseMsg:
		m.busy = false
		m.streamingActive = false
		m.pendingStreamResult = QueryResult{}
		m.hasPendingStreamResult = false
		m.pendingPermission = nil
		m.turnCancel = nil
		m.turnStopping = false
		m.runningStatus = ""
		m.noteActivity()
		m.err = msg.err
		if msg.err != nil {
			m.appendDisplayMessage("error", msg.err.Error(), "")
			m.setTextareaValue(m.lastTurnPrompt)
			m.saveDraft()
		} else if strings.TrimSpace(msg.result.Response) != "" && m.runStream == nil {
			m.appendDisplayMessage("assistant", msg.result.Response, m.responseMeta(msg.result))
			m.clearDraft()
		} else if strings.TrimSpace(msg.result.Response) != "" {
			m.syncFinalAssistantResponse(msg.result.Response)
			m.clearDraft()
		} else {
			m.clearDraft()
		}
		m.applyQueryResult(msg.result)
		m.markDisplayTurnDone()
		m.finishActivePendingInput(msg.err)
		if msg.err == nil {
			m.archiveToolActivityToAssistant()
			m.archiveAgentProgressToAssistant()
			m.armAwayRecap()
		}
		cmd := m.prepareTranscriptFlushCmd(transcriptFlushTurnComplete)
		m.resetLiveDisplayBlocks()
		m.refreshViewport()
		if msg.err == nil {
			if next, nextCmd := m.startNextPendingInput(); nextCmd != nil {
				return next, tea.Sequence(cmd, nextCmd)
			}
		}
		return m, cmd
	case transcriptFlushCommitMsg:
		cmd := m.commitTranscriptFlushCmd(msg)
		m.refreshViewport()
		if msg.reason == transcriptFlushPhaseBoundary {
			ch := m.phaseBoundaryStreamCh
			m.phaseBoundaryStreamCh = nil
			if ch != nil {
				return m, tea.Sequence(cmd, waitForStreamEvent(ch))
			}
		}
		return m, cmd
	case transcriptFlushPrintedMsg:
		m.markTranscriptFlushPrinted(msg)
		m.refreshViewport()
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	if m.resizeTextareaToContent() {
		m.refreshViewport()
	}
	m.updateSlashSuggestions()
	m.saveDraft()
	return m, cmd
}

func shouldResumePendingInputCommand(prompt string) bool {
	parts := strings.Fields(strings.TrimSpace(prompt))
	return len(parts) == 2 && parts[0] == "/queue" && parts[1] == "on" ||
		len(parts) == 3 && parts[0] == "/queue" && parts[1] == "retry"
}

func (m Model) isViewportScrollKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "pgup", "pgdown", "home", "end", "ctrl+u", "ctrl+d":
		return true
	case "up", "down", "ctrl+p", "ctrl+n":
		return !m.textarea.Focused() || m.textarea.Value() == ""
	default:
		return false
	}
}
