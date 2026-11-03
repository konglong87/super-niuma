// welcome.go 渲染欢迎卡与头部（含沙箱告警与吉祥物动画）。

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

func resetWelcomeDirectFrameCmd() tea.Cmd {
	return tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg {
		return welcomeDirectFrameResetMsg{}
	})
}

func (m Model) shouldShowWelcome() bool {
	if len(m.messages) == 0 {
		return true
	}
	for _, msg := range m.messages {
		switch msg.role {
		case "user", "assistant", "tool", "agent", "thinking", "error", messageRoleConfigNotice:
			return false
		}
	}
	return true
}

func (m Model) welcomeView() string {
	return m.headerView(true)
}

func (m Model) headerView(includePrompt bool) string {
	info := m.welcome.withDefaults(m.title)
	if m.width > 0 && m.width < 96 {
		return m.compactWelcomeView(includePrompt)
	}
	return m.productWelcomeView(info, includePrompt)
}

func (m Model) productWelcomeView(info WelcomeInfo, includePrompt bool) string {
	width := m.headerContentWidth()
	card := m.productWelcomeCard(info, width, includePrompt)
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(card)
	return b.String()
}

func (m Model) productWelcomeCard(info WelcomeInfo, width int, includePrompt bool) string {
	cardWidth := max(60, width-2)
	title := productWelcomeCardTitle(info)
	innerWidth := cardWidth - 2
	danger := productWelcomeDangerMode(info.PermissionMode)
	bodyLines := m.productWelcomeCardBodyLines(info, innerWidth, includePrompt, danger)
	bodyLines = append(bodyLines, welcomeCardSeparator(cardWidth))
	bodyLines = append(bodyLines, welcomeCardLine(productWelcomePermissionStyle(danger).Render(productWelcomePermissionLine(info.PermissionMode, innerWidth)), innerWidth))
	for _, line := range welcomeSandboxWarningLines(info, innerWidth) {
		bodyLines = append(bodyLines, welcomeCardLine(errorStyle.Render(line), innerWidth))
	}
	lines := []string{welcomeCardTopLine(title, cardWidth)}
	lines = append(lines, bodyLines...)
	lines = append(lines, welcomeBorderStyle.Render("╰"+strings.Repeat("─", max(0, cardWidth-2))+"╯"))
	return strings.Join(lines, "\n")
}

func productWelcomeCardTitle(info WelcomeInfo) string {
	version := strings.TrimSpace(info.Version)
	if version == "" {
		return "niuma"
	}
	return "niuma " + version
}

func (m Model) productWelcomeCardBodyLines(info WelcomeInfo, width int, includePrompt bool, danger bool) []string {
	leftWidth := max(34, min(46, width/3+4))
	rightWidth := max(20, width-leftWidth-1)
	mascot := productWelcomeMascotLines(danger, m.productWelcomeThinkingFrame(), m.welcomeDangerBlink())

	leftLines := []string{""}
	leftLines = append(leftLines, mascot...)
	leftLines = append(leftLines, productWelcomeContextLines(info, leftWidth)...)

	rightLines := productWelcomeRightPanelLines(info, rightWidth)

	lineCount := max(len(leftLines), len(rightLines))
	rendered := make([]string, 0, lineCount)
	for i := 0; i < lineCount; i++ {
		left := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		if i >= 1 && i < 1+len(mascot) {
			left = welcomeMascotStyleForState(danger, m.busy).Render(left)
		}
		rendered = append(rendered, welcomeCardLine(welcomeTwoPanelLine(left, right, leftWidth, rightWidth), width))
	}
	return rendered
}

func productWelcomeFitText(text string, width int) string {
	text = strings.TrimSpace(text)
	if width <= 0 || runewidth.StringWidth(text) <= width {
		return text
	}
	if width <= 3 {
		return truncate(text, width)
	}
	var b strings.Builder
	for _, r := range text {
		candidate := b.String() + string(r) + "..."
		if runewidth.StringWidth(candidate) > width {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "..."
}

func welcomeCardTopLine(title string, width int) string {
	if title == "" {
		return welcomeBorderStyle.Render("╭" + strings.Repeat("─", max(0, width-2)) + "╮")
	}
	prefix := welcomeBorderStyle.Render("╭─ ") + titleStyle.Render(title) + welcomeBorderStyle.Render(" ")
	suffixWidth := max(0, width-lipgloss.Width(prefix)-1)
	return prefix + welcomeBorderStyle.Render(strings.Repeat("─", suffixWidth)+"╮")
}

func welcomeCardSeparator(width int) string {
	return welcomeBorderStyle.Render("├" + strings.Repeat("─", max(0, width-2)) + "┤")
}

func welcomeCardLine(content string, width int) string {
	return welcomeBorderStyle.Render("│") + lipgloss.NewStyle().Width(width).Render(content) + welcomeBorderStyle.Render("│")
}

func welcomeTwoPanelLine(left, right string, leftWidth, rightWidth int) string {
	leftCell := lipgloss.NewStyle().Width(leftWidth).Align(lipgloss.Center).Render(left)
	rightCell := lipgloss.NewStyle().Width(rightWidth).Render(right)
	return leftCell + welcomeBorderStyle.Render("│") + rightCell
}

func welcomePanelGroupLines(width int, groups ...[]string) []string {
	groupWidth := 0
	for _, group := range groups {
		for _, line := range group {
			groupWidth = max(groupWidth, runewidth.StringWidth(line))
		}
	}
	leftPad := max(0, (width-groupWidth)/2)
	var out []string
	for _, group := range groups {
		out = append(out, "")
		for _, line := range group {
			out = append(out, strings.Repeat(" ", leftPad)+line)
		}
	}
	return out
}

func productWelcomeRightPanelLines(info WelcomeInfo, width int) []string {
	quick := []string{"Quick start", "/init", "  初始化项目说明", "/help", "  查看命令"}
	session := []string{"Session", productWelcomeSessionSummary(info)}
	if id := strings.TrimSpace(info.SessionID); id != "" {
		session = append(session, "id "+id)
	}
	if width < 56 {
		lines := welcomePanelGroupLines(width, quick, session)
		return append([]string{""}, lines...)
	}
	gap := 4
	quickWidth := maxStringWidth(quick)
	blockWidth := max(0, width-2)
	sessionWidth := max(18, blockWidth-quickWidth-gap)
	totalWidth := quickWidth + gap + sessionWidth
	leftPad := max(0, (width-totalWidth)/2)
	rows := max(len(quick), len(session))
	out := []string{""}
	for i := 0; i < rows; i++ {
		left := ""
		if i < len(quick) {
			left = quick[i]
		}
		right := ""
		if i < len(session) {
			right = productWelcomeFitText(session[i], sessionWidth)
		}
		out = append(out, strings.Repeat(" ", leftPad)+padRightDisplay(left, quickWidth)+strings.Repeat(" ", gap)+padRightDisplay(right, sessionWidth))
	}
	return out
}

func maxStringWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, runewidth.StringWidth(line))
	}
	return width
}

func padRightDisplay(text string, width int) string {
	pad := max(0, width-runewidth.StringWidth(text))
	return text + strings.Repeat(" ", pad)
}

func welcomeMascotStyleForState(danger, busy bool) lipgloss.Style {
	if danger {
		return welcomeMascotDangerStyle
	}
	if busy {
		return welcomeMascotBusyStyle
	}
	return welcomeMascotStyle
}

// welcomeDangerBlink drives the danger-mode mascot flicker (◆ ⇄ ◇). It is a
// pure function of wall-clock time so no per-frame state is needed; the ~500ms
// period reads as an alert blink.
func (m Model) welcomeDangerBlink() bool {
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	return now.UnixMilli()/500%2 == 0
}

// shouldAnimateMascot reports whether the danger-mode blink should keep
// redrawing. Only the danger permission modes animate; the default welcome
// stays static so idle sessions never trigger a repaint loop.
func (m Model) shouldAnimateMascot() bool {
	if !m.shouldShowIdleWelcomeLive() {
		return false
	}
	return productWelcomeDangerMode(m.welcome.withDefaults(m.title).PermissionMode)
}

// startMascotTick schedules the blink loop if it should run and isn't already
// running. The mascotTicking guard prevents overlapping tick loops.
func (m *Model) startMascotTick() tea.Cmd {
	if m.mascotTicking || !m.shouldAnimateMascot() {
		return nil
	}
	m.mascotTicking = true
	return welcomeMascotTickCmd()
}

func welcomeMascotTickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg {
		return welcomeMascotTickMsg{}
	})
}

func (m Model) productWelcomeThinkingFrame() int {
	if !m.busy {
		return 0
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	if now.UnixMilli()/800%2 == 0 {
		return 1
	}
	return 2
}

func productWelcomeMascotLines(danger bool, thinkingFrame int, dangerBlink bool) []string {
	if danger {
		eye := "◆"
		if !dangerBlink {
			eye = "◇"
		}
		return normalizeWelcomeMascotLines([]string{
			"╭────────────╮",
			"│  " + eye + "      " + eye + "  │",
			"│     ━      │",
			"│  ╭──────╮  │",
			"│  │      │  │",
			"╰──╯      ╰──╯",
		})
	}
	if thinkingFrame == 2 {
		return normalizeWelcomeMascotLines([]string{
			"╭────────────╮",
			"│  ◌      ●  │",
			"│     ·      │",
			"│  ╭──────╮  │",
			"│  │      │  │",
			"╰──╯      ╰──╯",
		})
	}
	if thinkingFrame > 0 {
		return normalizeWelcomeMascotLines([]string{
			"╭────────────╮",
			"│  ●      ◌  │",
			"│     ·      │",
			"│  ╭──────╮  │",
			"│  │      │  │",
			"╰──╯      ╰──╯",
		})
	}
	return normalizeWelcomeMascotLines([]string{
		"╭────────────╮",
		"│  ●      ●  │",
		"│     ▿      │",
		"│  ╭──────╮  │",
		"│  │      │  │",
		"╰──╯      ╰──╯",
	})
}

func normalizeWelcomeMascotLines(lines []string) []string {
	width := maxStringWidth(lines)
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = padRightDisplay(line, width)
	}
	return out
}

func productWelcomeDangerMode(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), "bypass") || strings.EqualFold(strings.TrimSpace(mode), "allow")
}

func productWelcomePermissionLine(mode string, width int) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "ask"
	}
	right := "shift+tab 切换权限模式"
	var left string
	if productWelcomeDangerMode(mode) {
		left = "  >> 危险权限 " + mode
	} else {
		left = "  权限 " + mode
	}
	if width <= 0 {
		return left + "  " + right
	}
	line := left + "  " + right
	if runewidth.StringWidth(line) <= width {
		return line
	}
	return productWelcomeFitText(line, width)
}

func productWelcomePermissionStyle(danger bool) lipgloss.Style {
	if danger {
		return welcomeSafetyStyle
	}
	return welcomePermissionStyle
}

func productWelcomeModelLine(info WelcomeInfo, width int) string {
	model := strings.TrimSpace(info.Model)
	if model == "" {
		model = productWelcomeState(info)
	}
	return productWelcomeFitText("模型 "+model, width)
}

func productWelcomeWorkspaceLine(info WelcomeInfo, width int) string {
	cwd := strings.TrimSpace(info.CWD)
	if cwd == "" {
		return ""
	}
	return productWelcomeFitText("工作区 "+abbreviateHome(cwd), width)
}

func productWelcomeContextLines(info WelcomeInfo, width int) []string {
	contentWidth := max(0, width-4)
	lines := []string{
		productWelcomeWorkspaceLine(info, contentWidth),
		productWelcomeModelLine(info, contentWidth),
	}
	blockWidth := 0
	for _, line := range lines {
		blockWidth = max(blockWidth, runewidth.StringWidth(line))
	}
	leftPad := max(0, (width-blockWidth)/2)
	for i, line := range lines {
		lineWidth := runewidth.StringWidth(line)
		rightPad := max(0, width-leftPad-lineWidth)
		lines[i] = strings.Repeat(" ", leftPad) + line + strings.Repeat(" ", rightPad)
	}
	return lines
}

func productWelcomeSessionSummary(info WelcomeInfo) string {
	parts := []string{productWelcomeState(info)}
	if info.ToolSummary != "" {
		parts = append(parts, "tools "+info.ToolSummary)
	}
	if info.Sandbox != "" {
		parts = append(parts, "sandbox "+info.Sandbox)
	}
	if len(parts) == 0 {
		return "ready"
	}
	return strings.Join(parts, " · ")
}

func (m Model) compactWelcomeView(includePrompt bool) string {
	info := m.welcome.withDefaults(m.title)
	width := m.headerContentWidth()
	danger := productWelcomeDangerMode(info.PermissionMode)
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(titleStyle.Render(compactWelcomeTitle(info)))
	if state := productWelcomeState(info); state != "" {
		b.WriteString(statusStyle.Render(" · " + state))
	}
	if model := strings.TrimSpace(info.Model); model != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("模型", model, width)))
	}
	if cwd := strings.TrimSpace(info.CWD); cwd != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("工作区", abbreviateHome(cwd), width)))
	}
	if status := compactWelcomeStatusLine(info); status != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("状态", status, width)))
	}
	if session := strings.TrimSpace(info.SessionID); session != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("会话", session, width)))
	}
	if resume := strings.TrimSpace(info.Resume); resume != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("恢复", resume, width)))
	}
	if sessionStatus := strings.TrimSpace(info.SessionStatus); sessionStatus != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("状态", oneLine(sessionStatus), width)))
	}
	if includePrompt {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(compactWelcomeLine("命令", "/init 初始化项目说明 · /help 查看命令", width)))
	}
	b.WriteString("\n")
	b.WriteString(productWelcomePermissionStyle(danger).Render(productWelcomePermissionLine(info.PermissionMode, width)))
	// A narrow terminal is not a reason to hide that the sandbox is not enforced.
	for _, line := range welcomeSandboxWarningLines(info, width) {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(line))
	}
	return b.String()
}

// welcomeSandboxWarningLines spells out every configured sandbox option that is
// not actually enforced on this host (AUDIT-P1-35). It returns nothing when the
// sandbox is fully enforced, so a healthy welcome card is byte-identical.
//
// The prefix matters: the status label alone cannot carry "why" or "what to do",
// and a user who believes they are isolated will act on that belief.
func welcomeSandboxWarningLines(info WelcomeInfo, width int) []string {
	var lines []string
	for _, warning := range info.SandboxWarnings {
		warning = oneLine(warning)
		if warning == "" {
			continue
		}
		wrapped := wrapForViewport("  sandbox not enforced: "+warning, max(0, width-2))
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}
	return lines
}

func compactWelcomeTitle(info WelcomeInfo) string {
	version := strings.TrimSpace(info.Version)
	if version == "" {
		return "niuma"
	}
	return "niuma " + version
}

func compactWelcomeLine(label, value string, width int) string {
	line := fmt.Sprintf("%-6s %s", label, value)
	if runewidth.StringWidth(line) <= width {
		return line
	}
	return wrapForViewport(line, width)
}

func compactWelcomeStatusLine(info WelcomeInfo) string {
	var parts []string
	if info.ToolSummary != "" {
		parts = append(parts, "tools "+info.ToolSummary)
	}
	if info.Sandbox != "" {
		parts = append(parts, "sandbox "+info.Sandbox)
	}
	return strings.Join(parts, " · ")
}

func (m Model) headerContentWidth() int {
	if m.width > 0 {
		safeWidth := max(20, m.width-4)
		if m.width >= 140 {
			target := max(100, (m.width*70)/100)
			return min(safeWidth, target)
		}
		return safeWidth
	}
	return 96
}

func productWelcomeState(info WelcomeInfo) string {
	if resume := strings.TrimSpace(info.Resume); resume != "" {
		return "resume"
	}
	if status := strings.TrimSpace(info.SessionStatus); status != "" && !strings.EqualFold(status, "new") {
		return "active"
	}
	return "ready"
}

func (info WelcomeInfo) withDefaults(title string) WelcomeInfo {
	if info.Model == "" && info.Provider == "" && info.PromptMode == "" && info.CWD == "" && info.SessionID == "" && info.PermissionMode == "" && info.Sandbox == "" && info.ToolSummary == "" && info.MCPServers == 0 && info.Version == "" && info.ContextLength == 0 && info.Resume == "" && info.SessionStatus == "" && info.Goal == "" && info.GoalStep == "" && info.GoalCriteria == "" && info.GoalEvidence == "" && info.GoalNextAction == "" {
		info.Model = "default"
	}
	if title != "" && !strings.EqualFold(title, "niuma") && info.Model == "default" {
		info.Model = ""
	}
	return info
}

func (info WelcomeInfo) statusParts() []string {
	parts := []string{}
	if info.ToolSummary != "" {
		parts = append(parts, "tools="+info.ToolSummary)
	}
	parts = append(parts, fmt.Sprintf("mcp=%d", info.MCPServers))
	if info.Sandbox != "" {
		parts = append(parts, "sandbox="+info.Sandbox)
	}
	if info.PromptMode != "" {
		parts = append(parts, "mode="+info.PromptMode)
	}
	if info.PermissionMode != "" {
		parts = append(parts, "permissions="+info.PermissionMode)
	}
	if info.SessionStatus != "" {
		parts = append(parts, "session="+info.SessionStatus)
	}
	if len(parts) == 0 {
		return []string{"ready"}
	}
	return parts
}

func formatContextLength(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens >= 1000 && tokens%1000 == 0 {
		return strconv.Itoa(tokens/1000) + "k"
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return strconv.Itoa(tokens)
}
