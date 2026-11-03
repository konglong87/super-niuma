package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

func testComputerManager() (*computerManager, *int) {
	count := new(int)
	m := &computerManager{owner: cu.SessionOwner{TenantID: 1, UserID: 1}, factory: func(context.Context) (cu.Backend, error) {
		*count++
		return &cu.FakeBackend{CapabilitiesValue: cu.Capabilities{ProtocolVersion: cu.ProtocolVersion, Platform: cu.PlatformMacOS, Backend: cu.BackendNativeHost, CaptureReadiness: cu.ReadinessReady, InputReadiness: cu.ReadinessReady, PermissionState: cu.PermissionApproved, CoordinateSpace: cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels}}}, nil
	}}
	return m, count
}
func TestComputerStartApprovalAndSnapshotContract(t *testing.T) {
	m, _ := testComputerManager()
	ctx := context.Background()
	pending, err := m.start(ctx, ComputerSessionStartInput{})
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != cu.SessionPendingApproval {
		t.Fatal("implicit approval")
	}
	ready, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if ready.ID != pending.ID || ready.State != cu.SessionReady {
		t.Fatal("pending session could not be approved")
	}
	raw, _ := json.Marshal(ready)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["session_id"] != ready.ID {
		t.Fatalf("bridge contract mismatch %s", raw)
	}
	if _, exists := out["id"]; exists {
		t.Fatal("legacy wrong ID field retained")
	}
}
func TestComputerPermissionLossPausesAndRequiresExplicitRecovery(t *testing.T) {
	m, _ := testComputerManager()
	ctx := context.Background()
	ready, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	backend := m.backend.(*cu.FakeBackend)
	backend.CapabilitiesValue.CaptureReadiness = cu.ReadinessPermissionRequired
	backend.CapabilitiesValue.InputReadiness = cu.ReadinessPermissionRequired
	backend.CapabilitiesValue.PermissionState = cu.PermissionRequired
	if _, err := m.capabilities(ctx); err != nil {
		t.Fatal("permission refresh failed:", err)
	}
	paused := m.controller.Session()
	if paused.State() != cu.SessionPaused || !paused.Approved() {
		t.Fatalf("permission loss did not pause approved session: state=%q approved=%t", paused.State(), paused.Approved())
	}
	if _, err := m.control(ctx, ready.ID, cu.ActionResume); err == nil {
		t.Fatal("resume succeeded while permissions were unavailable")
	}
	backend.CapabilitiesValue.CaptureReadiness = cu.ReadinessReady
	backend.CapabilitiesValue.InputReadiness = cu.ReadinessReady
	backend.CapabilitiesValue.PermissionState = cu.PermissionApproved
	if _, err := m.capabilities(ctx); err != nil {
		t.Fatal("permission restore refresh failed:", err)
	}
	if paused.State() != cu.SessionPaused {
		t.Fatalf("permission restore auto-resumed session: state=%q", paused.State())
	}
	recovered, err := m.control(ctx, ready.ID, cu.ActionResume)
	if err != nil || recovered.State != cu.SessionNeedsObservation {
		t.Fatalf("explicit recovery failed: snapshot=%+v err=%v", recovered, err)
	}
}

func TestComputerControlsReturnStateAndNewSessionReplacesStoppedBackend(t *testing.T) {
	m, count := testComputerManager()
	ctx := context.Background()
	ready, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := m.control(ctx, ready.ID, cu.ActionPause)
	if err != nil || paused.State != cu.SessionPaused {
		t.Fatalf("pause %v %v", paused, err)
	}
	resumed, err := m.control(ctx, ready.ID, cu.ActionResume)
	if err != nil || resumed.State != cu.SessionNeedsObservation {
		t.Fatalf("resume %v %v", resumed, err)
	}
	stopped, err := m.control(ctx, ready.ID, cu.ActionStop)
	if err != nil || stopped.State != cu.SessionStopped {
		t.Fatalf("stop %v %v", stopped, err)
	}
	if _, err = m.control(ctx, ready.ID, cu.ActionResume); err == nil {
		t.Fatal("resume bypassed stop")
	}
	next, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == ready.ID || *count != 2 {
		t.Fatal("stopped helper reused")
	}
	if _, err = m.control(ctx, ready.ID, cu.ActionStop); err == nil {
		t.Fatal("old session accepted")
	}
}

// Any capture/image read through the status endpoint is a regression: model
// screenshots and observation freshness belong exclusively to the controller.
type snapshotOnlyComputerBackend struct {
	cu.FakeBackend
	t *testing.T
}

func (b *snapshotOnlyComputerBackend) Observe(context.Context, cu.ObserveRequest) (cu.Observation, error) {
	b.t.Fatal("snapshot captured the desktop")
	return cu.Observation{}, nil
}
func (b *snapshotOnlyComputerBackend) ObservationImage(context.Context, string) ([]byte, string, error) {
	b.t.Fatal("snapshot accessed model screenshot bytes")
	return nil, "", nil
}

func TestGetComputerSessionReadOnlyModelState(t *testing.T) {
	ctx := context.Background()
	m, _ := testComputerManager()
	backend, err := m.factory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b := &snapshotOnlyComputerBackend{FakeBackend: cu.FakeBackend{
		CapabilitiesValue: backend.(*cu.FakeBackend).CapabilitiesValue,
		ReceiptValue:      cu.ActionReceipt{AfterObservationID: "model-after", After: &cu.MediaRef{ID: "model-after-image"}},
	}, t: t}
	b.CapabilitiesValue.Actions = []cu.ActionKind{cu.ActionWait}
	m.backend = b
	ready, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	a := &app{computerManager: m}
	c := m.controller
	s := c.Session()
	o := cu.Observation{ID: "model-observation", SessionID: ready.ID, Width: 100, Height: 100,
		Capabilities: ready.Capabilities, ObservedAt: time.Now(), Screenshot: cu.MediaRef{ID: "model-image"}}
	if err := s.SetObservation(o); err != nil {
		t.Fatal(err)
	}
	before, ok := s.CurrentObservation()
	if !ok {
		t.Fatal("missing model observation")
	}
	for range 3 {
		got, err := a.GetComputerSession(ready.ID)
		if err != nil || got.State != cu.SessionReady || got.Observation == nil || !reflect.DeepEqual(*got.Observation, before) {
			t.Fatalf("snapshot changed model observation: %+v, %v", got, err)
		}
	}
	after, ok := s.CurrentObservation()
	if !ok || !reflect.DeepEqual(before, after) || m.controller != c || !s.Approved() {
		t.Fatal("read changed controller, grant, observation, or freshness")
	}
	action := cu.Action{ID: "model-action", SessionID: ready.ID, ObservationID: o.ID, Kind: cu.ActionWait}
	r, err := c.Execute(ctx, s.Owner(), action)
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.GetComputerSession(ready.ID)
	if err != nil || got.State != cu.SessionNeedsObservation || got.LastReceipt == nil || !reflect.DeepEqual(*got.LastReceipt, r) {
		t.Fatalf("model receipt missing: %+v, %v", got, err)
	}
	if _, ok := s.CurrentObservation(); ok {
		t.Fatal("status read restored an observation consumed by the model")
	}
	// Simulate model/runtime cleanup through the shared controller, not UI Stop.
	if err := c.Stop(ctx, s.Owner(), ready.ID); err != nil {
		t.Fatal(err)
	}
	got, err = a.GetComputerSession(ready.ID)
	if err != nil || got.State != cu.SessionStopped || got.LastReceipt == nil || s.Approved() || !b.Stopped {
		t.Fatalf("model Stop not reflected: %+v, %v", got, err)
	}
	if _, err := a.GetComputerSession("unknown-session"); err == nil {
		t.Fatal("accepted a different session ID")
	}
	next, err := m.start(ctx, ComputerSessionStartInput{Approved: true})
	if err != nil || next.ID == ready.ID {
		t.Fatalf("replacement start: %+v, %v", next, err)
	}
	if _, err := a.GetComputerSession(ready.ID); err == nil {
		t.Fatal("accepted replaced session")
	}
}

func TestGetComputerSessionDoesNotInitializeBackend(t *testing.T) {
	m, count := testComputerManager()
	a := &app{computerManager: m}
	if _, err := a.GetComputerSession("missing"); err == nil {
		t.Fatal("missing session accepted")
	}
	if *count != 0 || m.controller != nil || m.backend != nil {
		t.Fatal("status lookup initialized backend or session")
	}
}

type failingCapabilitiesBackend struct {
	*cu.FakeBackend
	err error
}

func (b *failingCapabilitiesBackend) Capabilities(context.Context) (cu.Capabilities, error) {
	return cu.Capabilities{}, b.err
}

func TestComputerManagerRestartsFailedHelperBeforeCreatingSession(t *testing.T) {
	caps := cu.Capabilities{
		ProtocolVersion:  cu.ProtocolVersion,
		Platform:         cu.PlatformMacOS,
		Backend:          cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady,
		InputReadiness:   cu.ReadinessReady,
		PermissionState:  cu.PermissionApproved,
		CoordinateSpace:  cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels},
	}
	first := &failingCapabilitiesBackend{FakeBackend: &cu.FakeBackend{CapabilitiesValue: caps}, err: context.DeadlineExceeded}
	second := &cu.FakeBackend{CapabilitiesValue: caps}
	created := 0
	m := &computerManager{owner: cu.SessionOwner{TenantID: 1, UserID: 1}, factory: func(context.Context) (cu.Backend, error) {
		created++
		if created == 1 {
			return first, nil
		}
		return second, nil
	}}
	started, err := m.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if started.State != cu.SessionReady || created != 2 || m.backend != second {
		t.Fatalf("state=%q created=%d backend=%T", started.State, created, m.backend)
	}
	if m.controller == nil || m.controller.Session().Capabilities().PermissionState != cu.PermissionApproved {
		t.Fatal("replacement helper did not bind a ready session")
	}
}

type observeFaultBackend struct {
	*cu.FakeBackend
	observeErr      error
	capabilitiesErr error
	faulted         bool
}

func (b *observeFaultBackend) Observe(context.Context, cu.ObserveRequest) (cu.Observation, error) {
	if b.observeErr != nil {
		b.faulted = true
		return cu.Observation{}, b.observeErr
	}
	return b.FakeBackend.Observe(context.Background(), cu.ObserveRequest{})
}

func (b *observeFaultBackend) Capabilities(ctx context.Context) (cu.Capabilities, error) {
	if b.faulted && b.capabilitiesErr != nil {
		return cu.Capabilities{}, b.capabilitiesErr
	}
	return b.FakeBackend.Capabilities(ctx)
}

func (b *observeFaultBackend) ObservationImage(context.Context, string) ([]byte, string, error) {
	return []byte("png"), "image/png", nil
}

type managerImageBackend struct {
	*cu.FakeBackend
}

func (b *managerImageBackend) ObservationImage(context.Context, string) ([]byte, string, error) {
	return []byte("png"), "image/png", nil
}

func TestComputerManagerRecoversObserveHelperFaultWithoutReusingObservation(t *testing.T) {
	caps := cu.Capabilities{
		ProtocolVersion:  cu.ProtocolVersion,
		Platform:         cu.PlatformMacOS,
		Backend:          cu.BackendNativeHost,
		CaptureReadiness: cu.ReadinessReady,
		InputReadiness:   cu.ReadinessReady,
		PermissionState:  cu.PermissionApproved,
		CoordinateSpace:  cu.CoordinateSpace{Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels},
	}
	first := &observeFaultBackend{
		FakeBackend:     &cu.FakeBackend{CapabilitiesValue: caps, ObservationValue: cu.Observation{Capabilities: caps, ID: "stale-observation"}},
		observeErr:      errors.New("helper pipe closed"),
		capabilitiesErr: errors.New("helper exited"),
	}
	second := &managerImageBackend{FakeBackend: &cu.FakeBackend{CapabilitiesValue: caps, ObservationValue: cu.Observation{Capabilities: caps, ID: "fresh-observation", Width: 100, Height: 100, ScaleFactor: 1, ObservedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}}}
	created := 0
	m := &computerManager{owner: cu.SessionOwner{TenantID: 1, UserID: 1}, factory: func(context.Context) (cu.Backend, error) {
		created++
		if created == 1 {
			return first, nil
		}
		return second, nil
	}}
	started, err := m.start(context.Background(), ComputerSessionStartInput{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.observe(context.Background(), started.ID); err == nil {
		t.Fatal("helper observe fault unexpectedly succeeded")
	}
	if m.controller.Session().State() != cu.SessionPaused {
		t.Fatalf("session state after helper observe fault = %s, want paused", m.controller.Session().State())
	}
	oldController := m.controller
	if _, err := m.capabilities(context.Background()); err != nil {
		t.Fatalf("capability refresh did not replace failed helper: %v", err)
	}
	if created != 2 || m.controller == oldController || m.controller.Session().ID() != started.ID || m.controller.Session().State() != cu.SessionPaused {
		t.Fatalf("replacement lifecycle created=%d controller_reused=%t old=%p new=%p backend=%T session=%+v", created, m.controller == oldController, oldController, m.controller, m.backend, computerSnapshot(m.controller.Session()))
	}
	if _, ok := m.controller.Session().CurrentObservation(); ok {
		t.Fatal("helper replacement restored stale observation authority")
	}
	if _, err := m.control(context.Background(), started.ID, cu.ActionResume); err != nil {
		t.Fatalf("explicit resume after helper replacement failed: %v", err)
	}
	if _, err := m.observe(context.Background(), started.ID); err != nil {
		t.Fatalf("fresh observe after helper replacement failed: %v", err)
	}
	if m.controller.Session().State() != cu.SessionReady {
		t.Fatalf("session state after fresh observe = %s, want ready", m.controller.Session().State())
	}
}
