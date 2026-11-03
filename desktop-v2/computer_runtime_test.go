package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func TestComputerRuntimeOutlivesWindowContext(t *testing.T) {
	windowCtx, cancelWindow := context.WithCancel(context.Background())
	runtime := newComputerRuntime(context.Background())
	defer func() { _ = runtime.Stop(context.Background()) }()

	cancelWindow()
	select {
	case <-runtime.Context().Done():
		t.Fatal("computer runtime was coupled to the window context")
	default:
	}
	if windowCtx.Err() == nil {
		t.Fatal("window context did not cancel")
	}
	if runtime.Manager() == nil || runtime.Manager().runtimeContext() != runtime.Context() {
		t.Fatal("manager is not bound to computerRuntimeCtx")
	}
}

func TestComputerHelperLifetimeDoesNotComeFromUIClient(t *testing.T) {
	for _, surface := range []string{"capabilities-first", "conversation-approval"} {
		t.Run(surface, func(t *testing.T) {
			windowCtx, cancelWindow := context.WithCancel(context.Background())
			defer cancelWindow()
			runtime := newComputerRuntime(nil)
			defer func() { _ = runtime.Stop(context.Background()) }()
			m, _ := testComputerManager()
			m.runtimeCtx = runtime.Context()
			runtime.manager = m
			var helperLifetime context.Context
			factory := m.factory
			m.factory = func(ctx context.Context) (cu.Backend, error) {
				helperLifetime = ctx
				return factory(ctx)
			}
			a := &app{windowCtx: windowCtx, computerRuntime: runtime, computerManager: m, token: "synthetic-test-only"}
			if surface == "capabilities-first" {
				if capabilities := a.GetComputerCapabilities(); !capabilities.Available {
					t.Fatal("UI capability probe failed")
				}
			} else {
				a.port = computerOwnerTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"data":{"tenant_id":7,"user_id":11,"session_id":93}}`)
				})
				if _, err := a.StartComputerSession(ComputerSessionStartInput{Approved: true, ConversationRef: "tenant:alpha"}); err != nil {
					t.Fatal(err)
				}
			}
			cancelWindow()
			if helperLifetime == nil || helperLifetime != runtime.Context() || helperLifetime.Err() != nil {
				t.Fatal("UI client cancellation owns the long-lived helper context")
			}
			if err := runtime.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			if helperLifetime.Err() == nil {
				t.Fatal("explicit runtime Stop did not end helper lifetime")
			}
		})
	}
}

type runtimeDrainBackend struct {
	*cu.FakeBackend
	lifetime         context.Context
	closedWhileAlive bool
}

func (b *runtimeDrainBackend) Close(ctx context.Context) error {
	b.closedWhileAlive = b.lifetime.Err() == nil && ctx.Err() == nil
	return b.FakeBackend.Close(ctx)
}

func TestComputerRuntimeStopDrainsBeforeCancellingHelperLifetime(t *testing.T) {
	runtime := newComputerRuntime(nil)
	defer func() { _ = runtime.Stop(context.Background()) }()
	m, _ := testComputerManager()
	m.runtimeCtx = runtime.Context()
	runtime.manager = m
	factory := m.factory
	var backend *runtimeDrainBackend
	m.factory = func(ctx context.Context) (cu.Backend, error) {
		base, err := factory(ctx)
		if err != nil {
			return nil, err
		}
		backend = &runtimeDrainBackend{FakeBackend: base.(*cu.FakeBackend), lifetime: ctx}
		return backend, nil
	}
	if _, err := m.start(context.Background(), ComputerSessionStartInput{Approved: true}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !backend.closedWhileAlive || runtime.Context().Err() == nil {
		t.Fatal("runtime canceled helper before bounded Stop/drain completed")
	}
}

func TestActiveComputerRuntimePreventsWailsHostClose(t *testing.T) {
	manager, _ := testComputerManager()
	ready, err := manager.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	app := &app{computerManager: manager}
	if !app.shouldPreventWindowClose() {
		t.Fatal("active computer session allowed Wails host close")
	}
	if _, err := manager.control(context.Background(), ready.ID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if app.shouldPreventWindowClose() {
		t.Fatal("terminal computer session still blocked explicit host close")
	}
}

func TestProviderRuntimeActiveSessionPreventsWailsHostClose(t *testing.T) {
	manager, _ := testComputerManager()
	ready, err := manager.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	provider := newComputerRuntime(nil)
	provider.manager = manager
	app := &app{providerRuntime: provider}
	if !app.shouldPreventWindowClose() {
		t.Fatal("active provider runtime session allowed Wails host close")
	}
	if _, err := manager.control(context.Background(), ready.ID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if app.shouldPreventWindowClose() {
		t.Fatal("stopped provider runtime still blocked explicit host close")
	}
}

func TestParseComputerRuntimeProcessArgsRequiresPrivateAbsoluteDirectory(t *testing.T) {
	if _, found, err := parseComputerRuntimeProcessArgs(nil); found || err != nil {
		t.Fatalf("no runtime args = found=%t err=%v", found, err)
	}
	if _, found, err := parseComputerRuntimeProcessArgs([]string{computerRuntimeProcessFlag}); !found || err == nil {
		t.Fatal("runtime process without data directory was accepted")
	}
	path, found, err := parseComputerRuntimeProcessArgs([]string{computerRuntimeProcessFlag, computerRuntimeDataDirFlag, "/private/runtime"})
	if err != nil || !found || path != "/private/runtime" {
		t.Fatalf("runtime args = path=%q found=%t err=%v", path, found, err)
	}
	if _, found, err := parseComputerRuntimeProcessArgs([]string{computerRuntimeProcessFlag, computerRuntimeDataDirFlag, "relative"}); !found || err == nil {
		t.Fatal("relative runtime directory was accepted")
	}
}

func TestAppendRuntimeProcessEnvRemovesInheritedHandshakeFD(t *testing.T) {
	env := appendRuntimeProcessEnv([]string{"A=1", "NIUMA_COMPUTER_RUNTIME_CONFIG_FD=99", "B=2"})
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "NIUMA_COMPUTER_RUNTIME_CONFIG_FD=") || !strings.Contains(joined, "A=1") || !strings.Contains(joined, "B=2") {
		t.Fatalf("sanitized runtime environment = %q", joined)
	}
}

func TestStandaloneRuntimeAcceptsExistingDesktopDataParent(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "computer-runtime")
	if err := ensurePrivateDirectory(child); err != nil {
		t.Fatalf("private runtime child rejected under existing data parent: %v", err)
	}
	info, err := os.Stat(child)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime child mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestRuntimeSupervisorMarksUnexpectedWorkerExitAsFaulted(t *testing.T) {
	process := &computerRuntimeProcess{done: make(chan error, 1)}
	runtime := newProcessComputerRuntime(process)
	process.done <- errors.New("worker exited unexpectedly")
	defer func() { _ = runtime.Stop(context.Background()) }()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.State() == cu.RuntimeFaulted {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if runtime.State() != cu.RuntimeFaulted {
		t.Fatalf("runtime state = %q, want faulted", runtime.State())
	}
	fault, ok := runtime.Fault()
	if !ok || fault.ErrorCode != cu.RuntimeFaultWorkerExited || fault.Recoverable {
		t.Fatalf("runtime fault = %+v, ok=%t", fault, ok)
	}
	if runtime.HasActiveSession() {
		t.Fatal("faulted runtime still reported an active session")
	}
}

func TestComputerManagerRuntimeStatusSeparatesRecoverableAndTerminalFaults(t *testing.T) {
	manager, _ := testComputerManager()
	started, err := manager.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	session := manager.controller.Session()
	caps := session.Capabilities()
	caps.CaptureReadiness = cu.ReadinessPermissionRequired
	caps.InputReadiness = cu.ReadinessPermissionRequired
	caps.PermissionState = cu.PermissionRequired
	if err := session.UpdateCapabilities(caps); err != nil {
		t.Fatal(err)
	}
	status := manager.runtimeStatus()
	if status.State != cu.RuntimeDegraded || status.Fault == nil || status.Fault.ErrorCode != cu.RuntimeFaultPermissionRevoked || !status.Fault.Recoverable || !status.ActiveSession || status.SessionID != started.ID {
		t.Fatalf("permission runtime status = %+v", status)
	}

	if err := session.Stop(session.Owner()); err != nil {
		t.Fatal(err)
	}
	status = manager.runtimeStatus()
	if status.State != cu.RuntimeReady || status.Fault != nil || status.ActiveSession || status.SessionID != "" {
		t.Fatalf("stopped runtime status = %+v", status)
	}
}

func TestComputerManagerRuntimeStatusDoesNotMislabelUserPauseAsHelperFault(t *testing.T) {
	manager, _ := testComputerManager()
	started, err := manager.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.control(context.Background(), started.ID, cu.ActionPause); err != nil {
		t.Fatal(err)
	}
	status := manager.runtimeStatus()
	if status.State != cu.RuntimeDegraded || status.Fault != nil || !status.ActiveSession {
		t.Fatalf("user pause runtime status = %+v, want degraded without fault", status)
	}
}

func TestNewProcessComputerRuntimeNilFailsClosed(t *testing.T) {
	runtime := newProcessComputerRuntime(nil)
	if runtime.State() != cu.RuntimeFaulted {
		t.Fatalf("nil process state = %s, want faulted", runtime.State())
	}
	fault, ok := runtime.Fault()
	if !ok || fault.ErrorCode != cu.RuntimeFaultHelperUnavailable || fault.Recoverable {
		t.Fatalf("nil process fault = %+v ok=%t", fault, ok)
	}
}

func TestRuntimeStopFixture(t *testing.T) {
	if os.Getenv("NIUMA_RUNTIME_STOP_FIXTURE") != "1" {
		return
	}
	signal.Ignore(os.Interrupt)
	select {}
}

func TestComputerRuntimeProcessStopEscalatesAfterInterruptTimeout(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestRuntimeStopFixture$", "-test.v")
	cmd.Env = append(os.Environ(), "NIUMA_RUNTIME_STOP_FIXTURE=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &computerRuntimeProcess{
		cmd:      cmd,
		done:     make(chan error, 1),
		exitDone: make(chan struct{}),
	}
	go func() { process.done <- cmd.Wait() }()
	process.startWaitMonitor()
	defer func() { _ = process.Stop(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := process.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v, want bounded escalation to converge", err)
	}
	if !process.exited() {
		t.Fatal("runtime process remained alive after Stop escalation")
	}
}

type runtimeFaultReportingBackend struct {
	*cu.FakeBackend
	code string
}

func (b *runtimeFaultReportingBackend) RuntimeFaultCode() string { return b.code }

func TestComputerManagerRuntimeStatusReportsQuarantinedHelperFault(t *testing.T) {
	manager, _ := testComputerManager()
	backend := &runtimeFaultReportingBackend{FakeBackend: &cu.FakeBackend{CapabilitiesValue: cu.Capabilities{
		ProtocolVersion: cu.ProtocolVersion, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady, InputReadiness: cu.ReadinessReady,
		PermissionState: cu.PermissionApproved,
		CoordinateSpace: cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels},
	}}, code: cu.ErrorCodeHelperUnavailable}
	manager.factory = func(context.Context) (cu.Backend, error) { return backend, nil }
	started, err := manager.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.controller.Session().Pause(); err != nil {
		t.Fatal(err)
	}
	status := manager.runtimeStatus()
	if status.State != cu.RuntimeDegraded || status.Fault == nil || status.Fault.ErrorCode != cu.RuntimeFaultHelperUnavailable || !status.Fault.Recoverable || status.SessionID != started.ID {
		t.Fatalf("quarantined helper status = %+v", status)
	}
}
