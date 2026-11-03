package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/konglong87/super-niuma/internal/computerbridge"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func agentControllerForTest(t *testing.T, approved bool) (computerAgentService, *cu.FakeBackend, cu.SessionOwner, string) {
	t.Helper()
	owner := cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 13}
	caps := cu.Capabilities{ProtocolVersion: cu.ProtocolVersion, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady, InputReadiness: cu.ReadinessReady, PermissionState: cu.PermissionApproved,
		CoordinateSpace: cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels, Width: 100, Height: 100, ScaleFactor: 1},
		Actions:         []cu.ActionKind{cu.ActionClick}, ImageSupported: true, SupportsPause: true, SupportsStop: true}
	session, err := cu.NewComputerSession(cu.SessionOptions{Owner: owner, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	b := &cu.FakeBackend{CapabilitiesValue: caps, ObservationValue: cu.Observation{ID: "obs", Width: 100, Height: 100, ScaleFactor: 1, Capabilities: caps, ObservedAt: time.Now()},
		ReceiptValue: cu.ActionReceipt{Outcome: cu.OutcomeExecuted, Verification: cu.VerificationNotChecked, AfterObservationID: "after", After: &cu.MediaRef{ID: "after"}}}
	c, err := cu.NewController(session, b)
	if err != nil {
		t.Fatal(err)
	}
	if approved {
		if err := session.Approve(owner); err != nil {
			t.Fatal(err)
		}
	}
	m := &computerManager{owner: owner, backend: b, controller: c}
	t.Cleanup(func() { _ = m.close(context.Background()) })
	return computerAgentService{manager: m}, b, owner, session.ID()
}
func TestAgentServiceLazilyApprovesManagedConversation(t *testing.T) {
	pending, _, owner, id := agentControllerForTest(t, false)
	got, err := pending.EnsureComputerSession(context.Background(), owner)
	if err != nil || got != id {
		t.Fatalf("EnsureComputerSession() = %q, %v", got, err)
	}
	if !pending.manager.controller.Session().Approved() {
		t.Fatal("managed session was not approved")
	}
	if _, err := pending.EnsureComputerSession(context.Background(), cu.SessionOwner{TenantID: 7, UserID: 11, SessionID: 99}); err == nil {
		t.Fatal("foreign owner received a session")
	}
}

func TestAgentServiceNeverMintsOrExposesLocalApproval(t *testing.T) {
	ctx := context.Background()
	m, _ := testComputerManager()
	preview, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	s := computerAgentService{manager: m}
	for _, owner := range []cu.SessionOwner{m.owner, {TenantID: 1, UserID: 1, SessionID: 1}} {
		if _, err := s.Lookup(ctx, owner); !errors.Is(err, errComputerAgentUnauthorized) {
			t.Fatal("local approval exposed", err)
		}
		if _, err := s.Capabilities(ctx, owner, preview.ID); err == nil {
			t.Fatal("local controller exposed")
		}
	}
	pending, _, owner, _ := agentControllerForTest(t, false)
	if _, err := pending.Lookup(ctx, owner); err == nil {
		t.Fatal("lookup approved pending session")
	}
	if pending.manager.controller.Session().Approved() {
		t.Fatal("lookup minted authority")
	}
}
func TestAgentServiceChecksEveryOwnerComponentAndSession(t *testing.T) {
	s, b, owner, id := agentControllerForTest(t, true)
	ctx := context.Background()
	for _, bad := range []cu.SessionOwner{{TenantID: 8, UserID: 11, SessionID: 13}, {TenantID: 7, UserID: 12, SessionID: 13}, {TenantID: 7, UserID: 11, SessionID: 14}, {TenantID: 7, UserID: 11}} {
		if _, err := s.Lookup(ctx, bad); err == nil {
			t.Fatal("foreign lookup")
		}
		if _, err := s.Observe(ctx, bad, cu.ObserveRequest{SessionID: id}); err == nil {
			t.Fatal("foreign observe")
		}
		if _, err := s.Execute(ctx, bad, cu.Action{ID: "a", SessionID: id, Kind: cu.ActionClick}); err == nil {
			t.Fatal("foreign execute")
		}
		if _, _, err := s.ObservationImage(ctx, bad, id, "obs"); err == nil {
			t.Fatal("foreign image")
		}
		if s.Stop(ctx, bad, id) == nil || s.Pause(ctx, bad, id) == nil || s.Resume(ctx, bad, id) == nil {
			t.Fatal("foreign control")
		}
	}
	if _, err := s.Capabilities(ctx, owner, "other"); err == nil {
		t.Fatal("wrong session")
	}
	if len(b.Executed) != 0 || b.Stopped || b.Paused {
		t.Fatal("unauthorized request reached backend")
	}
}
func TestAgentServiceSharesControllerAndRevokesLookupAfterStop(t *testing.T) {
	s, b, owner, id := agentControllerForTest(t, true)
	ctx := context.Background()
	if got, err := s.Lookup(ctx, owner); err != nil || got != id {
		t.Fatal(got, err)
	}
	obs, err := s.Observe(ctx, owner, cu.ObserveRequest{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	a := cu.Action{ID: "action", SessionID: id, ObservationID: obs.ID, Kind: cu.ActionClick, Point: &cu.Point{X: 2, Y: 2}}
	if r, err := s.Execute(ctx, owner, a); err != nil || r.Outcome != cu.OutcomeExecuted {
		t.Fatal(r, err)
	}
	if len(b.Executed) != 1 {
		t.Fatal("did not reach actual shared backend")
	}
	if _, err := s.Execute(ctx, owner, a); err == nil {
		t.Fatal("replayed action")
	}
	if err := s.Stop(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, owner); err == nil {
		t.Fatal("stopped lease discoverable")
	}
	if _, err := s.Observe(ctx, owner, cu.ObserveRequest{SessionID: id}); err == nil {
		t.Fatal("stopped lease accepted")
	}
	if len(b.Executed) != 1 || !b.Stopped {
		t.Fatal("stop not shared")
	}
}
func TestAgentServiceRejectsCancelledLookup(t *testing.T) {
	s, _, owner, _ := agentControllerForTest(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Lookup(ctx, owner); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// Exercise the actual wire format and authority adapter together. The backend
// is fake: this is IPC integration evidence, not real macOS input acceptance.
func TestAgentBridgeUnixControllerIntegration(t *testing.T) {
	s, b, owner, id := agentControllerForTest(t, true)
	// Keep the Unix path short on macOS, where t.TempDir can exceed sun_path.
	dir, err := os.MkdirTemp("", "ge-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 64)
	handler, err := computerbridge.NewHandler(token, s)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan struct{})
	go func() { defer close(done); _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close(); <-done })
	client, err := computerbridge.NewClient(computerbridge.Config{SocketPath: path, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got, err := client.EnsureComputerSession(ctx, owner); err != nil || got != id {
		t.Fatalf("ensure over bridge got=%q err=%v", got, err)
	}
	attemptID := "cu-attempt-wire-1"
	if got, err := client.EnsureComputerSessionAttempt(ctx, owner, attemptID); err != nil || got != id {
		t.Fatalf("attempt ensure over bridge got=%q err=%v", got, err)
	}
	if got, err := client.ResolveComputerSessionStart(ctx, owner, attemptID); err != nil || got != id {
		t.Fatalf("attempt resolve over bridge got=%q err=%v", got, err)
	}
	if got, err := client.Lookup(ctx, owner); err != nil || got != id {
		t.Fatal(got, err)
	}
	foreign := owner
	foreign.SessionID++
	if _, err := client.Lookup(ctx, foreign); err == nil {
		t.Fatal("foreign conversation discovered session")
	}
	obs, err := client.Observe(ctx, owner, cu.ObserveRequest{SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	action := cu.Action{ID: "wire-action", SessionID: id, ObservationID: obs.ID, Kind: cu.ActionClick, Point: &cu.Point{X: 2, Y: 2}}
	receipt, err := client.Execute(ctx, owner, action)
	if err != nil || receipt.Outcome != cu.OutcomeExecuted || len(b.Executed) != 1 {
		t.Fatal(receipt, err)
	}
	if _, err := client.Execute(ctx, owner, action); err == nil || len(b.Executed) != 1 {
		t.Fatal("wire replay reached backend")
	}
	if err := client.Stop(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Lookup(ctx, owner); err == nil {
		t.Fatal("stopped session discoverable")
	}
	if !b.Stopped {
		t.Fatal("wire stop missed shared controller")
	}
}

func TestAgentServiceRepeatedStopDoesNotRestoreOrReassignAuthority(t *testing.T) {
	s, _, owner, id := agentControllerForTest(t, true)
	ctx := context.Background()
	if err := s.Stop(ctx, owner, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx, owner, id); err != nil {
		t.Fatal("repeat stop", err)
	}
	if _, err := s.Lookup(ctx, owner); err == nil {
		t.Fatal("repeat stop restored lookup")
	}
	pending, _, pendingOwner, pendingID := agentControllerForTest(t, false)
	if err := pending.Stop(ctx, pendingOwner, pendingID); err == nil {
		t.Fatal("unapproved session exposed")
	}
	replacement, replacementBackend, nextOwner, nextID := agentControllerForTest(t, true)
	s.manager.mu.Lock()
	s.manager.controller = replacement.manager.controller
	s.manager.mu.Unlock()
	if nextID == id {
		t.Fatal("test sessions collided")
	}
	if err := s.Stop(ctx, owner, id); err == nil {
		t.Fatal("old cleanup targeted replacement")
	}
	if replacementBackend.Stopped {
		t.Fatal("replacement was stopped")
	}
	if got, err := s.Lookup(ctx, nextOwner); err != nil || got != nextID {
		t.Fatal(got, err)
	}
}
