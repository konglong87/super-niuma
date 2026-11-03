package main

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestNewServerTokenIsHighEntropyAndUnique(t *testing.T) {
	first, err := newServerToken()
	if err != nil {
		t.Fatalf("newServerToken() error = %v", err)
	}
	second, err := newServerToken()
	if err != nil {
		t.Fatalf("newServerToken() second error = %v", err)
	}
	if len(first) != 64 || len(second) != 64 {
		t.Fatalf("token lengths = %d and %d, want 64", len(first), len(second))
	}
	if first == second {
		t.Fatal("newServerToken() returned the same token twice")
	}
}

func TestWindowStateBridgeContract(t *testing.T) {
	appType := reflect.TypeOf((*app)(nil))
	for _, name := range []string{
		"Maximize",
		"Unmaximize",
		"ToggleMaximize",
		"Fullscreen",
		"Unfullscreen",
		"ToggleFullscreen",
	} {
		method, ok := appType.MethodByName(name)
		if !ok {
			t.Fatalf("bridge method %s is missing", name)
		}
		if method.Type.NumIn() != 1 || method.Type.NumOut() != 0 {
			t.Fatalf("bridge method %s has unexpected signature %s", name, method.Type)
		}
	}
	getWindowState, ok := appType.MethodByName("GetWindowState")
	if !ok || getWindowState.Type.NumOut() != 1 || getWindowState.Type.Out(0) != reflect.TypeOf(DesktopWindowState{}) {
		t.Fatalf("GetWindowState has unexpected signature %s", getWindowState.Type)
	}
	restartService, ok := appType.MethodByName("RestartLocalService")
	if !ok || restartService.Type.NumOut() != 1 || restartService.Type.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("RestartLocalService has unexpected signature %s", restartService.Type)
	}
	selectWorkspace, ok := appType.MethodByName("SelectWorkspace")
	if !ok || selectWorkspace.Type.NumOut() != 2 || selectWorkspace.Type.Out(0) != reflect.TypeOf("") || selectWorkspace.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("SelectWorkspace has unexpected signature %s", selectWorkspace.Type)
	}
	getServiceStatus, ok := appType.MethodByName("GetLocalServiceStatus")
	if !ok || getServiceStatus.Type.NumOut() != 1 || getServiceStatus.Type.Out(0) != reflect.TypeOf(LocalServiceStatus{}) {
		t.Fatalf("GetLocalServiceStatus has unexpected signature %s", getServiceStatus.Type)
	}
	for _, name := range []string{"GetComputerCapabilities", "GetComputerRuntimeStatus", "StopComputerRuntime", "StartComputerSession", "GetComputerSession", "GetActiveComputerSession", "ObserveComputerSession", "PauseComputerSession", "ResumeComputerSession", "StopComputerSession", "GetComputerActionReceipt"} {
		if _, ok := appType.MethodByName(name); !ok {
			t.Fatalf("computer bridge method %s is missing", name)
		}
	}
	getSessionBackend, ok := appType.MethodByName("GetSessionBackend")
	if !ok || getSessionBackend.Type.NumOut() != 1 || getSessionBackend.Type.Out(0) != reflect.TypeOf("") {
		t.Fatalf("GetSessionBackend has unexpected signature %s", getSessionBackend.Type)
	}
	setSessionBackend, ok := appType.MethodByName("SetSessionBackend")
	if !ok || setSessionBackend.Type.NumOut() != 1 || setSessionBackend.Type.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Fatalf("SetSessionBackend has unexpected signature %s", setSessionBackend.Type)
	}
	if windowStateChangedEvent != "niuma:window-state-changed" {
		t.Fatalf("window state event = %q", windowStateChangedEvent)
	}
}

func TestDesktopWailsOptionsKeepMacZoomButtonEnabled(t *testing.T) {
	appOptions := desktopWailsOptions(&app{}, desktopConfig{}, &url.URL{})
	if appOptions.Mac == nil {
		t.Fatal("Mac options must be initialized so Wails keeps the native Zoom button enabled")
	}
	if appOptions.Mac.DisableZoom {
		t.Fatal("Mac DisableZoom = true, want false")
	}
	if appOptions.DisableResize {
		t.Fatal("DisableResize = true, want resizable window")
	}
}

func TestWindowStatePersistenceKeepsNormalGeometry(t *testing.T) {
	previous := windowState{
		Geometry: DesktopWindowGeometry{X: 40, Y: 50, Width: 1200, Height: 800},
	}
	maximized := persistedWindowState(previous, DesktopWindowState{
		Geometry:  DesktopWindowGeometry{X: 0, Y: 0, Width: 1920, Height: 1080},
		Maximized: true,
	})
	if maximized.Geometry != previous.Geometry {
		t.Fatalf("maximized geometry = %#v, want %#v", maximized.Geometry, previous.Geometry)
	}
	if !maximized.Maximized {
		t.Fatal("maximized state was not persisted")
	}

	normal := persistedWindowState(maximized, DesktopWindowState{
		Geometry: DesktopWindowGeometry{X: 80, Y: 90, Width: 1400, Height: 860},
	})
	if normal.Geometry != (DesktopWindowGeometry{X: 80, Y: 90, Width: 1400, Height: 860}) {
		t.Fatalf("normal geometry = %#v", normal.Geometry)
	}
	if normal.Maximized || normal.Fullscreen {
		t.Fatalf("normal state = %#v", normal)
	}
}

func TestWindowStartStatePrefersFullscreen(t *testing.T) {
	if got := windowStartState(desktopConfig{Window: windowState{Maximized: true}}); got != options.Maximised {
		t.Fatalf("maximized start state = %v", got)
	}
	if got := windowStartState(desktopConfig{Window: windowState{Maximized: true, Fullscreen: true}}); got != options.Fullscreen {
		t.Fatalf("fullscreen start state = %v", got)
	}
}

func TestDesktopWindowCloseDoesNotQuitRuntimeHost(t *testing.T) {
	appOptions := desktopWailsOptions(&app{}, desktopConfig{}, &url.URL{})
	if !appOptions.HideWindowOnClose {
		t.Fatal("closing the UI window still shuts down the Computer Use runtime host")
	}
	if appOptions.OnShutdown == nil {
		t.Fatal("explicit app/runtime shutdown must still drain sessions")
	}
}
