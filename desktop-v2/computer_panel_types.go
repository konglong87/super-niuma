package main

const (
	computerPanelCommandStop     = "stop"
	computerPanelCommandPause    = "pause"
	computerPanelCommandResume   = "resume"
	computerPanelCommandExpand   = "expand"
	computerPanelCommandCollapse = "collapse"
	computerPanelCommandDismiss  = "dismiss"
)

type ComputerPanelSnapshot struct {
	DisplayMode   string `json:"display_mode,omitempty"`
	PreviewDetail string `json:"preview_detail,omitempty"`
	Visible       bool   `json:"visible"`
	Expanded      bool   `json:"expanded"`
	SessionID     string `json:"session_id"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	ImageData     string `json:"image_data"`
	CanStop       bool   `json:"can_stop"`
	CanPause      bool   `json:"can_pause"`
	CanResume     bool   `json:"can_resume"`
	Language      string `json:"language"`
}

type computerPanelCommand struct {
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
}

type computerPanel interface {
	Update(ComputerPanelSnapshot)
	Poll() *computerPanelCommand
	Close()
}
