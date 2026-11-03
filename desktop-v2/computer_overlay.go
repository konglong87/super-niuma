package main

import (
	"context"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

const (
	computerPanelPollInterval    = 60 * time.Millisecond
	computerPanelRefreshInterval = 500 * time.Millisecond
	computerPanelPreviewTimeout  = 2 * time.Second

	// nativeComputerPanelStartupEnabled keeps the native NSPanel opt-in while
	// the WebView DOM overlay remains the default desktop presentation.
	// Keep the native implementation compiled for a future explicit opt-in.
	nativeComputerPanelStartupEnabled = false
)

func (a *app) startComputerPanelIfEnabled(ctx context.Context) {
	if !nativeComputerPanelStartupEnabled {
		return
	}
	a.startComputerPanel(ctx)
}

func (a *app) startComputerPanel(ctx context.Context) {
	panel := newNativeComputerPanel()
	if panel == nil {
		return
	}
	done := make(chan struct{})
	a.mu.Lock()
	if a.computerPanel != nil {
		a.mu.Unlock()
		panel.Close()
		return
	}
	a.computerPanel, a.computerPanelDone = panel, done
	a.mu.Unlock()
	// Keep safety commands independent of image retrieval and hidden WebViews.
	go func() {
		ticker := time.NewTicker(computerPanelPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				a.handleComputerPanelCommand(ctx)
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(computerPanelRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				a.refreshComputerPanel(ctx)
			}
		}
	}()
}

func (a *app) stopComputerPanel() {
	a.mu.Lock()
	panel, done := a.computerPanel, a.computerPanelDone
	a.computerPanel, a.computerPanelDone = nil, nil
	a.computerOverlay = computerOverlayState{}
	a.mu.Unlock()
	if done != nil {
		close(done)
	}
	if panel != nil {
		panel.Close()
	}
}

func (a *app) IsComputerOverlayAvailable() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.computerPanel != nil
}

// Only updates presentation preferences. Session discovery, command state, and
// preview data are host-owned; an old WebView poll cannot undo Hide or Expand.
func (a *app) UpdateComputerOverlay(snapshot ComputerPanelSnapshot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.computerOverlay.configure(snapshot)
	a.renderComputerPanelLocked()
}

func (a *app) ShowComputerOverlay() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.computerOverlay.show()
	a.renderComputerPanelLocked()
}

func (a *app) renderComputerPanelLocked() {
	if a.computerPanel != nil {
		a.computerPanel.Update(a.computerOverlay.render())
	}
}

func (a *app) refreshComputerPanel(ctx context.Context) {
	session, err := a.GetActiveComputerSession()
	if err != nil {
		session = ComputerSessionDTO{}
	}
	var image ComputerObservationDTO
	if session.ID != "" && session.State != cu.SessionStopped {
		previewCtx, cancel := context.WithTimeout(ctx, computerPanelPreviewTimeout)
		image, _ = a.computer().preview(previewCtx, session.ID)
		cancel()
	}
	// Stop or replacement may have happened while an image read was in flight.
	current, currentErr := a.GetActiveComputerSession()
	if currentErr != nil || current.ID != session.ID || current.State != session.State {
		session, image = current, ComputerObservationDTO{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.computerPanel == nil {
		return
	}
	a.computerOverlay.update(computerOverlaySnapshot(session, a.computerOverlay.language, image))
	a.renderComputerPanelLocked()
}

func (a *app) handleComputerPanelCommand(ctx context.Context) {
	a.mu.Lock()
	panel := a.computerPanel
	a.mu.Unlock()
	if panel == nil {
		return
	}
	command := panel.Poll()
	if command == nil {
		return
	}
	// Validate the displayed session before any command, including presentation.
	session, err := a.GetActiveComputerSession()
	if err != nil || session.ID != command.SessionID || session.State == cu.SessionStopped {
		return
	}
	a.mu.Lock()
	handled := a.computerOverlay.command(*command)
	if handled {
		a.renderComputerPanelLocked()
	}
	a.mu.Unlock()
	if handled {
		return
	}
	allowed := computerOverlaySnapshot(session, "", ComputerObservationDTO{})
	switch command.Kind {
	case computerPanelCommandStop:
		if !allowed.CanStop {
			return
		}
	case computerPanelCommandPause:
		if !allowed.CanPause {
			return
		}
	case computerPanelCommandResume:
		if !allowed.CanResume {
			return
		}
	default:
		return
	}
	if kind, ok := mapComputerPanelCommand(command.Kind); ok {
		_, controlErr := a.computer().control(ctx, command.SessionID, kind)
		a.mu.Lock()
		if a.computerOverlay.latest.SessionID == command.SessionID {
			a.computerOverlay.controlFailed = controlErr != nil
		}
		a.mu.Unlock()
		a.refreshComputerPanel(ctx)
	}
}

func mapComputerPanelCommand(kind string) (cu.ActionKind, bool) {
	switch kind {
	case computerPanelCommandStop:
		return cu.ActionStop, true
	case computerPanelCommandPause:
		return cu.ActionPause, true
	case computerPanelCommandResume:
		return cu.ActionResume, true
	default:
		return "", false
	}
}
