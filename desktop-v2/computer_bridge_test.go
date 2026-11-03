package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbridge"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func TestDesktopBridgeBindsApprovedConversationAndCleansSocket(t *testing.T) {
	// Keep the fixture below the repo, short enough for Darwin sun_path.
	relative, err := os.MkdirTemp("..", ".cb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(relative) })
	root, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := testComputerManager()
	ctx := context.Background()
	t.Cleanup(func() { _ = m.close(ctx) })
	listener, err := startDesktopComputerBridge(ctx, root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	client, err := computerbridge.NewClient(listener.Config())
	if err != nil {
		t.Fatal(err)
	}
	owner := cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 93}
	if _, err := client.Lookup(ctx, owner); err == nil {
		t.Fatal("implicit approval")
	}
	approved, err := m.startOwnedWithLifetime(ctx, ctx, ComputerSessionStartInput{Approved: true}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := client.Lookup(ctx, owner); err != nil || got != approved.ID {
		t.Fatal(got, err)
	}
	if _, err := m.control(ctx, approved.ID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lookup(ctx, owner); err == nil {
		t.Fatal("native Stop did not revoke model lookup")
	}
	if err := client.Stop(ctx, owner, approved.ID); err != nil {
		t.Fatal("query cleanup after native Stop must be idempotent", err)
	}
	path := listener.Config().SocketPath
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket not cleaned", err)
	}
	if _, err := client.Lookup(ctx, owner); err == nil {
		t.Fatal("closed bridge accepted request")
	}
}

func TestRemoteDesktopManagerUsesBridgeForLocalSession(t *testing.T) {
	relative, err := os.MkdirTemp("..", ".cb-remote-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(relative) })
	root, err := filepath.Abs(relative)
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	backendManager, _ := testComputerManager()
	ctx := context.Background()
	listener, err := startDesktopComputerBridge(ctx, root, backendManager)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := computerbridge.NewClient(listener.Config())
	if err != nil {
		t.Fatal(err)
	}
	remote := newRemoteComputerManager(client)
	started, err := remote.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if started.ID == "" || started.State != cu.SessionReady || !started.Capabilities.Ready() {
		t.Fatalf("remote start = %+v", started)
	}
	if got, err := remote.snapshot(ctx, started.ID); err != nil || got.ID != started.ID || got.State != cu.SessionReady {
		t.Fatalf("remote snapshot = %+v err=%v", got, err)
	}
	stopped, err := remote.control(ctx, started.ID, cu.ActionStop)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != cu.SessionStopped {
		t.Fatalf("remote stop = %+v", stopped)
	}
}

// A local-preview owner has no conversation ID. It must reach the trusted
// runtime controller only through the separately authenticated UI listener.
type bridgeLaunchBackend struct {
	*cu.FakeBackend
	launches int
}

func (b *bridgeLaunchBackend) LaunchApp(context.Context, string, string) (cu.LaunchReceipt, error) {
	b.launches++
	return cu.LaunchReceipt{BundleID: cu.WorkBuddyBundleID, Window: cu.WindowRef{ID: "fixture-window", OwnerPID: 42, BundleID: cu.WorkBuddyBundleID, IsVisible: true, IsFrontmost: true}, Outcome: cu.OutcomeExecuted, CompletedAt: time.Now()}, nil
}

func TestLocalUIAtomicTurnReachesRuntimeAndPreservesExecutedReceipt(t *testing.T) {
	manager, _ := testComputerManager()
	base, err := manager.factory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backend := &bridgeLaunchBackend{FakeBackend: base.(*cu.FakeBackend)}
	backend.ObserveError = errors.New("fixture observation unavailable after dispatch")
	manager.backend = backend
	service := computerAgentService{manager: manager}
	started, err := service.StartLocal(context.Background(), computerUIBridgeOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	result, turnErr := service.ExecuteTurn(context.Background(), computerUIBridgeOwner, cu.Action{
		ID: "ui-atomic-launch", SessionID: started.SessionID, TargetID: computerTargetWorkBuddy, Kind: cu.ActionLaunchApp,
	})
	if backend.launches != 1 || result.Outcome != cu.OutcomeExecuted || result.DispatchState != cu.DispatchComplete || result.Receipt.ActionID != "ui-atomic-launch" {
		t.Fatalf("UI turn was rejected before runtime dispatch: calls=%d outcome=%s dispatch=%s receipt=%s err=%v", backend.launches, result.Outcome, result.DispatchState, result.Receipt.ActionID, turnErr)
	}
}

func TestProviderEndpointCannotUseLocalUIOperations(t *testing.T) {
	manager, _ := testComputerManager()
	service := computerAgentService{manager: manager}
	if _, err := service.EnsureComputerSession(context.Background(), computerUIBridgeOwner); err == nil {
		t.Fatal("provider endpoint accepted local UI owner as a model conversation")
	}
	if _, err := service.Snapshot(context.Background(), computerUIBridgeOwner, "missing"); err == nil {
		t.Fatal("UI snapshot accepted a missing local session")
	}
}

func TestRuntimeOwnerStopRequiresSessionRevocation(t *testing.T) {
	manager, _ := testComputerManager()
	stopped := false
	service := computerAgentService{manager: manager, stopRuntime: func() { stopped = true }}
	started, err := service.StartLocal(context.Background(), computerUIBridgeOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StopRuntime(context.Background(), computerRuntimeSupervisionOwner); err == nil {
		t.Fatal("runtime owner stopped an active session")
	}
	if _, err := manager.control(context.Background(), started.SessionID, cu.ActionStop); err != nil {
		t.Fatal(err)
	}
	if err := service.StopRuntime(context.Background(), computerRuntimeSupervisionOwner); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("runtime stop callback was not notified")
	}
}

func TestRemoteDesktopManagerRuntimeStopFallsBackAfterBridgeDisconnect(t *testing.T) {
	client, err := computerbridge.NewClient(computerbridge.Config{
		SocketPath:     filepath.Join(t.TempDir(), "missing.sock"),
		Token:          "bridge-test-token",
		RequestTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	manager := newRemoteComputerManager(client, func(context.Context) error {
		called = true
		return nil
	})
	if err := manager.stopRuntime(context.Background()); err != nil {
		t.Fatalf("stopRuntime() error = %v", err)
	}
	if !called {
		t.Fatal("runtime stop did not use the authenticated process fallback")
	}
}

func TestGetComputerRuntimeStatusFailsClosedAfterBridgeDisconnect(t *testing.T) {
	client, err := computerbridge.NewClient(computerbridge.Config{
		SocketPath:     filepath.Join(t.TempDir(), "missing.sock"),
		Token:          "bridge-test-token",
		RequestTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := newRemoteComputerManager(client)
	manager.remoteSession = "session-after-disconnect"
	app := &app{computerManager: manager}
	status := app.GetComputerRuntimeStatus()
	if status.State != cu.RuntimeFaulted || status.Fault == nil || status.Fault.ErrorCode != cu.RuntimeFaultBridgeDisconnected {
		t.Fatalf("runtime status = %+v, want redacted bridge-disconnected fault", status)
	}
	if !status.ActiveSession || status.SessionID != manager.remoteSession || status.Available {
		t.Fatalf("runtime status lost fail-closed session authority: %+v", status)
	}
}

func TestStopComputerRuntimeUsesExplicitProcessFallback(t *testing.T) {
	client, err := computerbridge.NewClient(computerbridge.Config{
		SocketPath:     filepath.Join(t.TempDir(), "missing.sock"),
		Token:          "bridge-test-token",
		RequestTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	fallbackCalled := false
	manager := newRemoteComputerManager(client, func(context.Context) error {
		fallbackCalled = true
		return nil
	})
	status := (&app{computerManager: manager}).StopComputerRuntime()
	if !fallbackCalled || status.State != cu.RuntimeStopped || status.Available {
		t.Fatalf("stop runtime status = %+v fallback_called=%t", status, fallbackCalled)
	}
}
