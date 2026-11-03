package main

import (
	"strings"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	computerOverlayModeAuto     = "auto"
	computerOverlayModeCompact  = "compact"
	computerOverlayModeExpanded = "expanded"
)

// Presentation state belongs to the host, not to transient WebView snapshots.
// Commands survive polling but never survive a session/mode change.
// All access is protected by app.mu.
type computerOverlayState struct {
	mode          string
	language      string
	latest        ComputerPanelSnapshot
	expanded      *bool
	hidden        bool
	controlFailed bool
}

func (s *computerOverlayState) configure(in ComputerPanelSnapshot) {
	mode := in.DisplayMode
	switch mode {
	case computerOverlayModeCompact, computerOverlayModeExpanded:
	default:
		mode = computerOverlayModeAuto
	}
	if s.mode != mode {
		s.expanded, s.hidden = nil, false
	}
	s.mode = mode
	s.language = in.Language
}

func (s *computerOverlayState) update(in ComputerPanelSnapshot) {
	if in.SessionID != s.latest.SessionID {
		s.expanded, s.hidden = nil, false
		s.controlFailed = false
	}
	s.latest = in
}

func (s *computerOverlayState) show() {
	s.hidden = false
	expanded := true
	s.expanded = &expanded
}

func (s *computerOverlayState) command(command computerPanelCommand) bool {
	if command.SessionID == "" || command.SessionID != s.latest.SessionID || !s.latest.Visible {
		return false
	}
	switch command.Kind {
	case computerPanelCommandExpand, computerPanelCommandCollapse:
		expanded := command.Kind == computerPanelCommandExpand
		s.expanded = &expanded
	case computerPanelCommandDismiss:
		s.hidden = true
	default:
		return false
	}
	return true
}

func (s *computerOverlayState) render() ComputerPanelSnapshot {
	out := s.latest
	out.Visible = out.Visible && !s.hidden
	out.Expanded = s.mode != computerOverlayModeCompact
	if s.expanded != nil {
		out.Expanded = *s.expanded
	}
	if s.controlFailed {
		out.Detail = "Control failed; check the workspace before retrying"
		if strings.HasPrefix(s.language, "zh") {
			out.Detail = "控制未成功，请在工作区检查后重试"
		}
	}
	if !out.Visible {
		out.ImageData = ""
	}
	return out
}

func computerOverlaySnapshot(session ComputerSessionDTO, language string, image ComputerObservationDTO) ComputerPanelSnapshot {
	zh := strings.HasPrefix(language, "zh")
	text := func(en, cn string) string {
		if zh {
			return cn
		}
		return en
	}
	out := ComputerPanelSnapshot{
		SessionID: session.ID, Language: language,
		Visible:       session.ID != "" && session.State != cu.SessionStopped,
		Title:         text("Computer Use", "电脑操作"),
		PreviewDetail: text("No operation image is available yet.", "暂无可显示的操作画面，截图后将自动更新。"),
	}
	switch session.State {
	case cu.SessionReady:
		out.Detail = text("Ready for the next action", "已就绪，等待下一步操作")
	case cu.SessionNeedsObservation:
		out.Detail = text("Waiting for the next observation", "等待下一次画面更新")
	case cu.SessionPaused:
		out.Detail = text("Paused", "操作已暂停")
	case cu.SessionPendingApproval:
		out.Detail = text("Waiting for approval", "等待授权")
	case cu.SessionFailed:
		out.Detail = text("Operation interrupted — check the workspace", "操作中断，请查看工作区")
	case cu.SessionStopped:
		out.Detail = text("Stopped", "操作已停止")
	default:
		out.Detail = text("Working", "正在操作")
	}
	out.CanStop = out.Visible && session.Capabilities.SupportsStop
	local := session.OwnerKind == "local_preview"
	out.CanPause = local && session.Capabilities.SupportsPause && (session.State == cu.SessionReady || session.State == cu.SessionNeedsObservation)
	out.CanResume = local && session.Capabilities.SupportsPause && session.State == cu.SessionPaused
	if image.ImageData != "" && out.Visible {
		out.ImageData = image.ImageData
		target := image.Observation.ActiveWindow.Title
		if target == "" {
			target = text("Desktop", "桌面")
		}
		out.PreviewDetail = text("Latest screenshot · ", "最近操作画面 · ") + target
	}
	return out
}
