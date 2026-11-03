// mode_hint.go 渲染输入框与其下方的模式提示行（含 full/compact/minimal 三档降级）。

package tui

import (
	"strconv"
	"strings"
)

func (m Model) inputBoxView(status string) string {
	return m.inputBoxViewForBudget(status, m.renderBudget(), chromeFull)
}

func (m Model) inputBoxViewForBudget(status string, budget renderBudget, mode chromeMode) string {
	line := dividerLine(budget.SafeWidth)
	return strings.Join([]string{
		statusStyle.Render(line),
		m.textarea.View(),
		statusStyle.Render(line),
		m.modeHintViewForBudget(status, budget, mode),
	}, "\n")
}

func (m Model) modeHintView(status string) string {
	return m.modeHintViewForBudget(status, m.renderBudget(), chromeFull)
}

func (m Model) modeHintViewForBudget(status string, budget renderBudget, mode chromeMode) string {
	if mode == chromeMinimal {
		return m.compactModeHintView(status, budget, true)
	}
	if mode == chromeCompact {
		return m.compactModeHintView(status, budget, false)
	}
	if m.shouldShowWelcome() && !m.busy && m.err == nil && len(m.toolActivity) == 0 && len(m.agentProgress) == 0 && strings.TrimSpace(m.recentAgentEvidence) == "" {
		return m.welcomeModeHintViewForBudget(status, budget)
	}
	if m.shouldUseQuietConversationHint() {
		return m.quietConversationModeHintViewForBudget(status, budget)
	}
	info := m.welcome.withDefaults(m.title)
	statusParts := []string{"status", status}
	if m.busy && !m.turnStarted.IsZero() {
		statusParts = append(statusParts, runningStatusElapsedPart(status, m.now().Sub(m.turnStarted)))
		statusParts = append(statusParts, m.runningTokenLabel())
	}
	runtimeParts := []string{"runtime", "ui=tui"}
	if info.Model != "" {
		runtimeParts = append(runtimeParts, "model="+info.Model)
	}
	if info.Provider != "" {
		runtimeParts = append(runtimeParts, "provider="+info.Provider)
	}
	if info.PromptMode != "" {
		runtimeParts = append(runtimeParts, "mode="+info.PromptMode)
	}
	if info.ContextLength > 0 {
		runtimeParts = append(runtimeParts, "ctx="+formatContextLength(info.ContextLength))
	}
	if info.ToolSummary != "" {
		runtimeParts = append(runtimeParts, "tools="+info.ToolSummary)
	}
	if info.MCPServers > 0 {
		runtimeParts = append(runtimeParts, "mcp="+strconv.Itoa(info.MCPServers))
	}
	if cwd := firstNonEmpty(m.usage.CWD, info.CWD); cwd != "" {
		runtimeParts = append(runtimeParts, "cwd="+truncate(abbreviateHome(cwd), 72))
	}
	var safetyParts []string
	if strings.EqualFold(info.PermissionMode, "bypass") || strings.EqualFold(info.PermissionMode, "allow") {
		safetyParts = append(safetyParts, errorStyle.Render("permissions="+info.PermissionMode+" active"))
	} else if info.PermissionMode != "" {
		safetyParts = append(safetyParts, "permissions="+info.PermissionMode)
	}
	if info.Sandbox != "" {
		safetyParts = append(safetyParts, "sandbox="+info.Sandbox)
	}
	controlParts := m.modeHintControlParts(false)
	width := budget.ChromeWidth
	lines := []string{}
	lines = append(lines, wrapModeHintParts(statusParts, width)...)
	lines = append(lines, wrapModeHintParts(runtimeParts, width)...)
	if len(safetyParts) > 0 {
		lines = append(lines, wrapModeHintParts(append([]string{"safety"}, safetyParts...), width)...)
	}
	for _, line := range m.runtimeContextPanelLines(info) {
		lines = append(lines, wrapModeHintLine(line, width)...)
	}
	lines = append(lines, wrapModeHintParts(controlParts, width)...)
	return statusStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) welcomeModeHintView(status string) string {
	return m.welcomeModeHintViewForBudget(status, m.renderBudget())
}

func (m Model) welcomeModeHintViewForBudget(status string, budget renderBudget) string {
	width := budget.ChromeWidth
	statusParts := []string{"status", status}
	controlParts := m.modeHintControlParts(true)
	lines := []string{}
	lines = append(lines, wrapModeHintParts(statusParts, width)...)
	lines = append(lines, wrapModeHintParts(controlParts, width)...)
	return statusStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) quietConversationModeHintView(status string) string {
	return m.quietConversationModeHintViewForBudget(status, m.renderBudget())
}

func (m Model) quietConversationModeHintViewForBudget(status string, budget renderBudget) string {
	width := budget.ChromeWidth
	statusParts := []string{"status", status}
	if m.busy && !m.turnStarted.IsZero() {
		statusParts = append(statusParts, runningStatusElapsedPart(status, m.now().Sub(m.turnStarted)))
		statusParts = append(statusParts, m.runningTokenLabel())
	}
	lines := []string{}
	lines = append(lines, wrapModeHintParts(statusParts, width)...)
	lines = append(lines, wrapModeHintParts(m.modeHintControlParts(false), width)...)
	return statusStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) compactModeHintView(status string, budget renderBudget, minimal bool) string {
	info := m.welcome.withDefaults(m.title)
	statusParts := []string{"status", status}
	if minimal {
		if toolSummary := compactToolSummary(m.usage); toolSummary != "" {
			statusParts = append(statusParts, toolSummary)
		}
	} else if info.Model != "" {
		statusParts = append(statusParts, "model="+info.Model)
	}
	if !minimal {
		if goal := strings.TrimSpace(info.Goal); goal != "" {
			statusParts = append(statusParts, "goal="+truncate(oneLine(goal), 18))
		}
	}
	if !minimal && m.usage.LastInput > 0 && m.usage.ContextWindow > 0 {
		statusParts = append(statusParts, formatContextWindowUsage(m.usage.LastInput, m.usage.ContextWindow))
	}
	if !minimal {
		if toolSummary := compactToolSummary(m.usage); toolSummary != "" {
			statusParts = append(statusParts, toolSummary)
		} else if info.ToolSummary != "" {
			statusParts = append(statusParts, "tools="+info.ToolSummary)
		}
	} else if info.ToolSummary != "" && compactToolSummary(m.usage) == "" {
		statusParts = append(statusParts, "tools="+info.ToolSummary)
	}
	controlParts := []string{"controls"}
	if !minimal {
		if hint := m.permissionModeShortcutHint(); hint != "" {
			controlParts = append(controlParts, hint)
		}
		controlParts = append(controlParts, "ctrl+o details", "enter send", "ctrl+j newline", "ctrl+c stop/exit", "ctrl+u clear", "/exit to quit")
	} else {
		controlParts = append(controlParts, "enter send", "ctrl+c stop/exit")
	}
	if m.importClipboard != nil {
		controlParts = append(controlParts, "ctrl+v paste image")
	}
	lines := []string{}
	lines = append(lines, wrapModeHintParts(statusParts, budget.ChromeWidth)...)
	lines = append(lines, wrapModeHintParts(controlParts, budget.ChromeWidth)...)
	return statusStyle.Render(strings.Join(lines, "\n"))
}

func (m Model) shouldUseQuietConversationHint() bool {
	if (m.shouldShowWelcome() && !m.busy) || m.err != nil || m.pendingPermission != nil || m.pendingQuestion != nil {
		return false
	}
	if m.hasIncompleteTodos() {
		return false
	}
	info := m.welcome.withDefaults(m.title)
	return strings.TrimSpace(info.Resume) == "" && strings.TrimSpace(info.SessionStatus) == ""
}

func (m Model) modeHintControlParts(welcome bool) []string {
	controlParts := []string{"controls"}
	if hint := m.permissionModeShortcutHint(); hint != "" {
		controlParts = append(controlParts, hint)
	}
	if welcome {
		controlParts = append(controlParts, "mouse=copy", "/ commands", "enter send", "ctrl+j newline", "ctrl+c exit", "ctrl+u clear")
	} else {
		if m.mouseTracking {
			controlParts = append(controlParts, "mouse=scroll")
		} else {
			controlParts = append(controlParts, "mouse=copy")
		}
		if len(m.agentProgress) > 0 {
			if agents := m.agentProgressSummary(); agents != "" {
				controlParts = append(controlParts, "agents="+agents)
			}
			controlParts = append(controlParts, "ctrl+e sub-agents")
		}
		if len(m.toolActivity) > 0 || m.hasArchivedToolActivity() {
			controlParts = append(controlParts, "ctrl+t tools")
		}
		controlParts = append(controlParts, "ctrl+o mouse", "enter send", "ctrl+j newline", "ctrl+c stop/exit", "ctrl+u clear")
		if len(m.attachments) > 0 {
			controlParts = append(controlParts, "ctrl+d remove attachment")
		}
		controlParts = append(controlParts, "/exit to quit")
	}
	if m.importClipboard != nil {
		if m.clipboardHasImage {
			controlParts = append(controlParts, "Image in clipboard · ctrl+v to paste")
		} else {
			controlParts = append(controlParts, "ctrl+v paste image")
		}
	}
	return controlParts
}

func (m Model) permissionModeShortcutHint() string {
	if m.switchPermission == nil {
		return ""
	}
	mode := strings.TrimSpace(m.welcome.withDefaults(m.title).PermissionMode)
	if mode == "" {
		mode = "ask"
	}
	return "shift+tab perm=" + mode
}

func (m Model) runtimeContextPanelLines(info WelcomeInfo) []string {
	var lines []string
	var sessionParts []string
	if resume := strings.TrimSpace(info.Resume); resume != "" {
		sessionParts = append(sessionParts, "resume="+truncate(oneLine(resume), 72))
	}
	if status := strings.TrimSpace(info.SessionStatus); status != "" {
		sessionParts = append(sessionParts, "state="+truncate(oneLine(status), 96))
	}
	if len(sessionParts) > 0 {
		lines = append(lines, "session "+strings.Join(sessionParts, "  "))
	}
	var goalParts []string
	if goal := strings.TrimSpace(info.Goal); goal != "" {
		goalParts = append(goalParts, "id="+truncate(oneLine(goal), 72))
	}
	if step := strings.TrimSpace(info.GoalStep); step != "" {
		goalParts = append(goalParts, "step="+truncate(oneLine(step), 48))
	}
	if criteria := strings.TrimSpace(info.GoalCriteria); criteria != "" {
		goalParts = append(goalParts, "criteria="+truncate(oneLine(criteria), 48))
	}
	if len(goalParts) > 0 {
		lines = append(lines, "goal "+strings.Join(goalParts, "  "))
	}
	var evidenceParts []string
	if evidence := strings.TrimSpace(info.GoalEvidence); evidence != "" {
		evidenceParts = append(evidenceParts, "evidence="+truncate(oneLine(evidence), 72))
	}
	if nextAction := strings.TrimSpace(info.GoalNextAction); nextAction != "" {
		evidenceParts = append(evidenceParts, "next="+truncate(oneLine(nextAction), 72))
	}
	if len(evidenceParts) > 0 {
		lines = append(lines, "evidence "+strings.Join(evidenceParts, "  "))
	}
	if evidence := strings.TrimSpace(m.recentAgentEvidence); evidence != "" {
		evidence = strings.ReplaceAll(oneLine(evidence), " | ", "  ")
		lines = append(lines, "evidence agent="+truncate(evidence, 520))
	}
	return lines
}
