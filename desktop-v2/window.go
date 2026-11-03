package main

import (
	"context"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const windowStateChangedEvent = "niuma:window-state-changed"

type DesktopWindowState struct {
	Geometry   DesktopWindowGeometry `json:"geometry"`
	Maximized  bool                  `json:"maximized"`
	Fullscreen bool                  `json:"fullscreen"`
}

func (s DesktopWindowState) equal(other DesktopWindowState) bool {
	return s == other
}

func windowWidth(config desktopConfig) int {
	if config.Window.Geometry.valid() {
		return config.Window.Geometry.Width
	}
	return defaultWindowWidth
}

func windowHeight(config desktopConfig) int {
	if config.Window.Geometry.valid() {
		return config.Window.Geometry.Height
	}
	return defaultWindowHeight
}

func (a *app) windowContext() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.windowCtx
}

func (a *app) setWindowContext(ctx context.Context) {
	a.mu.Lock()
	a.windowCtx = ctx
	a.mu.Unlock()
}

func restoreWindowGeometry(ctx context.Context, config desktopConfig) {
	if !config.Window.Geometry.valid() {
		return
	}
	wailsruntime.WindowSetSize(ctx, config.Window.Geometry.Width, config.Window.Geometry.Height)
	wailsruntime.WindowSetPosition(ctx, config.Window.Geometry.X, config.Window.Geometry.Y)
}

func (a *app) Maximize() {
	if ctx := a.windowContext(); ctx != nil {
		wailsruntime.WindowMaximise(ctx)
		a.emitWindowState(ctx)
	}
}

func (a *app) Unmaximize() {
	if ctx := a.windowContext(); ctx != nil {
		wailsruntime.WindowUnmaximise(ctx)
		a.emitWindowState(ctx)
	}
}

func (a *app) ToggleMaximize() {
	if ctx := a.windowContext(); ctx != nil {
		wailsruntime.WindowToggleMaximise(ctx)
		a.emitWindowState(ctx)
	}
}

func (a *app) Fullscreen() {
	if ctx := a.windowContext(); ctx != nil {
		wailsruntime.WindowFullscreen(ctx)
		a.emitWindowState(ctx)
	}
}

func (a *app) Unfullscreen() {
	if ctx := a.windowContext(); ctx != nil {
		wailsruntime.WindowUnfullscreen(ctx)
		a.emitWindowState(ctx)
	}
}

func (a *app) ToggleFullscreen() {
	if ctx := a.windowContext(); ctx != nil {
		if wailsruntime.WindowIsFullscreen(ctx) {
			wailsruntime.WindowUnfullscreen(ctx)
		} else {
			wailsruntime.WindowFullscreen(ctx)
		}
		a.emitWindowState(ctx)
	}
}

func (a *app) GetWindowState() DesktopWindowState {
	ctx := a.windowContext()
	if ctx == nil {
		return DesktopWindowState{}
	}
	return a.readWindowState(ctx)
}

func (a *app) readWindowState(ctx context.Context) DesktopWindowState {
	width, height := wailsruntime.WindowGetSize(ctx)
	x, y := wailsruntime.WindowGetPosition(ctx)
	return DesktopWindowState{
		Geometry: DesktopWindowGeometry{
			X:      x,
			Y:      y,
			Width:  width,
			Height: height,
		},
		Maximized:  wailsruntime.WindowIsMaximised(ctx),
		Fullscreen: wailsruntime.WindowIsFullscreen(ctx),
	}
}

func (a *app) emitWindowState(ctx context.Context) {
	snapshot := a.readWindowState(ctx)
	a.rememberWindowState(snapshot)
	wailsruntime.EventsEmit(ctx, windowStateChangedEvent, snapshot)
}

func (a *app) rememberWindowState(snapshot DesktopWindowState) {
	a.mu.Lock()
	a.config.Window = persistedWindowState(a.config.Window, snapshot)
	a.mu.Unlock()
}

func (a *app) startWindowStateWatcher(ctx context.Context) {
	a.mu.Lock()
	if a.windowDone != nil {
		close(a.windowDone)
	}
	done := make(chan struct{})
	a.windowDone = done
	a.mu.Unlock()

	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		last := a.readWindowState(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				current := a.readWindowState(ctx)
				if !current.equal(last) {
					last = current
					a.rememberWindowState(current)
					wailsruntime.EventsEmit(ctx, windowStateChangedEvent, current)
				}
			}
		}
	}()
}

func (a *app) stopWindowStateWatcher() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.windowDone != nil {
		close(a.windowDone)
		a.windowDone = nil
	}
	a.windowCtx = nil
}

func (a *app) persistWindowState(ctx context.Context) error {
	snapshot := a.readWindowState(ctx)
	a.mu.Lock()
	config := a.config
	config.Window = persistedWindowState(config.Window, snapshot)
	a.config = config
	a.mu.Unlock()
	return saveDesktopConfig(config)
}

func persistedWindowState(previous windowState, snapshot DesktopWindowState) windowState {
	next := previous
	if snapshot.Geometry.valid() && !snapshot.Maximized && !snapshot.Fullscreen {
		next.Geometry = snapshot.Geometry
	}
	next.Maximized = snapshot.Maximized
	next.Fullscreen = snapshot.Fullscreen
	return next.normalized()
}

func windowStartState(config desktopConfig) options.WindowStartState {
	switch {
	case config.Window.Fullscreen:
		return options.Fullscreen
	case config.Window.Maximized:
		return options.Maximised
	default:
		return options.Normal
	}
}
