// permission_prompt.go 是权限审批弹窗：选项、按键、落盘规则与 --permission-mode 切换。

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/konglong87/super-niuma/internal/permissions"
	"github.com/mattn/go-runewidth"
)

type pendingPermission struct {
	request  PermissionRequest
	reply    chan PermissionDecision
	ch       <-chan StreamEvent
	selected int
}

func (m Model) switchPermissionMode() (tea.Model, tea.Cmd) {
	if m.switchPermission == nil {
		return m, nil
	}
	next, err := m.switchPermission(m.welcome.PermissionMode)
	if err != nil {
		m.err = err
		return m, nil
	}
	m.welcome.PermissionMode = next
	m.err = nil
	if m.pendingPermission != nil && permissionModeAllowsWithoutPrompt(next) {
		pending := m.pendingPermission
		decision := PermissionDecision{
			Allowed:     true,
			Destination: "once",
			Reason:      "approved by runtime permission mode",
			Rule:        firstNonEmpty(pending.request.Rule, persistentPermissionRule(pending.request)),
		}
		pending.reply <- decision
		m.pendingPermission = nil
		m.forceDirectWelcomeFrame = true
		m.refreshViewport()
		return m, tea.Batch(waitForStreamEvent(pending.ch), resetWelcomeDirectFrameCmd())
	}
	m.forceDirectWelcomeFrame = true
	m.refreshViewport()
	return m, tea.Batch(resetWelcomeDirectFrameCmd(), m.startMascotTick())
}

func permissionModeAllowsWithoutPrompt(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	return mode == "allow" || mode == "bypass"
}

func (m Model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	pending := m.pendingPermission
	if pending == nil {
		return m, nil
	}
	optionCount := len(permissionOptions())
	if pending.request.OneShot {
		optionCount = 2
		if pending.selected >= optionCount {
			pending.selected = optionCount - 1
		}
	}
	key := strings.ToLower(msg.String())
	switch key {
	case "up", "ctrl+p", "shift+tab":
		pending.selected = wrapIndex(pending.selected-1, optionCount)
		m.pendingPermission = pending
		m.refreshViewport()
		return m, nil
	case "down", "ctrl+n", "tab":
		pending.selected = wrapIndex(pending.selected+1, optionCount)
		m.pendingPermission = pending
		m.refreshViewport()
		return m, nil
	}
	var decision PermissionDecision
	switch key {
	case "enter":
		if pending.request.OneShot {
			if pending.selected == 0 {
				decision = PermissionDecision{Allowed: true, Destination: "once", Reason: "approved in TUI"}
			} else {
				decision = PermissionDecision{Allowed: false, Destination: "once", Reason: "denied in TUI"}
			}
		} else {
			decision = permissionOptions()[pending.selected].decision(pending.request)
		}
	case "y":
		decision = PermissionDecision{Allowed: true, Destination: "once", Reason: "approved in TUI"}
	case "s":
		if pending.request.OneShot {
			return m, nil
		}
		decision = PermissionDecision{Allowed: true, Destination: "session", Reason: "approved in TUI for session", Rule: persistentPermissionRule(pending.request)}
	case "p":
		if pending.request.OneShot {
			return m, nil
		}
		decision = PermissionDecision{Allowed: true, Destination: "project", Reason: "approved in TUI for project", Rule: persistentPermissionRule(pending.request)}
	case "g":
		if pending.request.OneShot {
			return m, nil
		}
		decision = PermissionDecision{Allowed: true, Destination: "global", Reason: "approved in TUI globally", Rule: persistentPermissionRule(pending.request)}
	case "n", "d", "esc":
		decision = PermissionDecision{Allowed: false, Destination: "once", Reason: "denied in TUI"}
	default:
		return m, nil
	}
	if decision.Rule == "" {
		decision.Rule = pending.request.Rule
	}
	pending.reply <- decision
	m.pendingPermission = nil
	m.refreshViewport()
	return m, waitForStreamEvent(pending.ch)
}

func permissionPromptView(p pendingPermission, width int) string {
	var b strings.Builder
	width = max(24, width)
	tool := firstNonEmpty(strings.TrimSpace(p.request.ToolName), "tool")
	b.WriteString(titleStyle.Render("Permission request"))
	b.WriteString(statusStyle.Render(truncateDisplay(" · "+tool, max(4, width-runewidth.StringWidth("Permission request")))))
	b.WriteString("\n")
	if len(p.request.Input) > 0 {
		rawInput := strings.TrimSpace(string(p.request.Input))
		if summary := permissionInputSummary(p.request.ToolName, rawInput); summary != "" {
			for _, line := range labeledDetailLines("Summary", summary, width) {
				b.WriteString("\n")
				b.WriteString(line)
			}
		}
	}
	if risk := permissionRiskSummary(p.request.ToolName); risk != "" {
		for _, line := range labeledDetailLines("Risk", risk, width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	if p.request.Request != "" {
		for _, line := range labeledDetailLines("Request", p.request.Request, width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	if p.request.Reason != "" {
		for _, line := range labeledDetailLines("Reason", p.request.Reason, width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	if p.request.Source != "" {
		for _, line := range labeledDetailLines("Source", p.request.Source, width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	if rule := persistentPermissionRule(p.request); rule != "" {
		for _, line := range labeledDetailLines("Rule", rule, width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	if len(p.request.Input) > 0 {
		rawInput := strings.TrimSpace(string(p.request.Input))
		for _, line := range labeledDetailLines("Raw input", truncateDisplay(rawInput, 240), width) {
			b.WriteString("\n")
			b.WriteString(line)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(permissionDecisionLine(p.selected, width, p.request.OneShot))
	scopeText := "Scopes: once=current request · session=this run · project=workspace · global=all workspaces"
	if p.request.OneShot {
		scopeText = "Scope: once=current exact Git effect (persistent approval disabled)"
	}
	for _, line := range wrapDisplayChunks(scopeText, width) {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(line))
	}
	return b.String()
}

func permissionDecisionLine(selected int, width int, oneShot bool) string {
	width = max(24, width)
	labels := []string{"Once y", "Session s", "Project p", "Global g", "Deny n/esc"}
	if oneShot {
		labels = []string{"Once y", "Deny n/esc"}
		if selected > 0 {
			selected = 1
		}
	}
	if selected < 0 {
		selected = 0
	}
	parts := make([]string, 0, len(labels)+1)
	for i, label := range labels {
		part := label
		if i == selected {
			part = permissionSelectedStyle.Render("[ ▶ " + label + " ]")
		}
		parts = append(parts, part)
	}
	parts = append(parts, statusStyle.Render("enter confirm"))
	lines := []string{}
	line := statusStyle.Render("Decision:")
	for _, part := range parts {
		candidate := line + "  " + part
		if lipgloss.Width(candidate) <= width {
			line = candidate
			continue
		}
		lines = append(lines, line)
		line = "  " + part
	}
	if strings.TrimSpace(terminalANSIEscapeRE.ReplaceAllString(line, "")) != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func permissionInputSummary(toolName, rawInput string) string {
	if summary := toolInputSummary(toolName, rawInput); summary != "" {
		return summary
	}
	if detail := compactToolDetail(rawInput); detail != "" {
		return truncate(detail, 120)
	}
	return ""
}

func permissionRiskSummary(toolName string) string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "bash", "powershell":
		return "executes a shell command; review cwd and side effects"
	case "edit", "multiedit", "write":
		return "modifies workspace files; persistent approval affects future writes"
	case "todowrite":
		return "updates task progress; usually safe within this session"
	case "read", "ls", "glob", "grep":
		return "reads workspace data; persistent approval affects future reads"
	case "task", "agent", "agentcreate", "agentmessage", "agentget", "agentstop":
		return "delegates work to an agent; review task and workspace"
	default:
		return "grants this tool request according to the selected scope"
	}
}

type permissionOption struct {
	label    string
	help     string
	decision func(PermissionRequest) PermissionDecision
}

func permissionOptions() []permissionOption {
	return []permissionOption{
		{label: "Allow once", help: "y", decision: func(PermissionRequest) PermissionDecision {
			return PermissionDecision{Allowed: true, Destination: "once", Reason: "approved in TUI"}
		}},
		{label: "Allow for session", help: "s", decision: func(req PermissionRequest) PermissionDecision {
			return PermissionDecision{Allowed: true, Destination: "session", Reason: "approved in TUI for session", Rule: persistentPermissionRule(req)}
		}},
		{label: "Allow for project", help: "p", decision: func(req PermissionRequest) PermissionDecision {
			return PermissionDecision{Allowed: true, Destination: "project", Reason: "approved in TUI for project", Rule: persistentPermissionRule(req)}
		}},
		{label: "Allow globally", help: "g", decision: func(req PermissionRequest) PermissionDecision {
			return PermissionDecision{Allowed: true, Destination: "global", Reason: "approved in TUI globally", Rule: persistentPermissionRule(req)}
		}},
		{label: "Deny", help: "n/esc", decision: func(PermissionRequest) PermissionDecision {
			return PermissionDecision{Allowed: false, Destination: "once", Reason: "denied in TUI"}
		}},
	}
}

func persistentPermissionRule(req PermissionRequest) string {
	tool := strings.TrimSpace(req.ToolName)
	if tool == "TodoWrite" {
		return tool
	}
	rule := strings.TrimSpace(req.Rule)
	request := strings.TrimSpace(req.Request)
	if rule != "" && !isBroadPermissionRule(rule, tool) {
		return rule
	}
	if tool != "" && request != "" {
		return permissions.FormatRule(tool, request)
	}
	if tool != "" {
		return tool
	}
	return request
}

func isBroadPermissionRule(rule, tool string) bool {
	rule = strings.TrimSpace(rule)
	tool = strings.TrimSpace(tool)
	if rule == "" {
		return false
	}
	if rule == "*" || rule == tool || strings.EqualFold(rule, tool+":*") || strings.EqualFold(rule, tool+"(*)") {
		return true
	}
	return false
}

func wrapIndex(index, length int) int {
	if length <= 0 {
		return 0
	}
	index %= length
	if index < 0 {
		index += length
	}
	return index
}
