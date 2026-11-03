package computeruse

import "testing"

func aliasedCapabilities() Capabilities {
	c := readyCapabilities()
	frame := &WindowFrame{X: -100, Y: -20, Width: 400, Height: 300}
	c.CoordinateSpace.Bounds = frame
	c.Displays = []CoordinateSpace{c.CoordinateSpace}
	c.Windows = []WindowRef{{ID: "window", Frame: frame}}
	c.TargetWindow = c.Windows[0]
	return c
}
func assertDetachedCapabilities(t *testing.T, c Capabilities) {
	t.Helper()
	for _, frame := range []*WindowFrame{c.CoordinateSpace.Bounds, c.Displays[0].Bounds, c.Windows[0].Frame, c.TargetWindow.Frame} {
		if frame == nil || frame.X != -100 {
			t.Fatalf("geometry changed through alias: %+v", frame)
		}
	}
}
func mutateCapabilities(c Capabilities) {
	c.CoordinateSpace.Bounds.X = 999
	c.Displays[0].Bounds.X = 999
	c.Windows[0].Frame.X = 999
	c.TargetWindow.Frame.X = 999
}
func TestCloneCapabilitiesDetachesSharedGeometry(t *testing.T) {
	original := aliasedCapabilities()
	copy := original.Clone()
	original.CoordinateSpace.Bounds.X = 10
	assertDetachedCapabilities(t, copy)
	copy.CoordinateSpace.Bounds.X = 20
	if copy.Displays[0].Bounds.X != -100 {
		t.Fatal("top-level and display bounds remain aliased")
	}
	if cloneCapabilities(Capabilities{}).CoordinateSpace.Bounds != nil {
		t.Fatal("nil bounds not preserved")
	}
}
func TestSessionGeometrySnapshotsAreDetached(t *testing.T) {
	original := aliasedCapabilities()
	s, err := NewComputerSession(SessionOptions{Owner: SessionOwner{TenantID: 1, UserID: 1}, Capabilities: original})
	if err != nil {
		t.Fatal(err)
	}
	mutateCapabilities(original)
	assertDetachedCapabilities(t, s.Capabilities())
	updated := aliasedCapabilities()
	if err = s.UpdateCapabilities(updated); err != nil {
		t.Fatal(err)
	}
	mutateCapabilities(updated)
	snapshot := s.Capabilities()
	assertDetachedCapabilities(t, snapshot)
	mutateCapabilities(snapshot)
	assertDetachedCapabilities(t, s.Capabilities())
}
func TestObservationAndReceiptWindowFramesAreDetached(t *testing.T) {
	s, _, now := newTestSession(t, false)
	o := readyObservation(s.ID(), now)
	o.Capabilities = aliasedCapabilities()
	o.ActiveWindow = o.Capabilities.TargetWindow
	if err := s.SetObservation(o); err != nil {
		t.Fatal(err)
	}
	mutateCapabilities(o.Capabilities)
	o.ActiveWindow.Frame.X = 999
	snapshot, ok := s.CurrentObservation()
	if !ok {
		t.Fatal("observation missing")
	}
	assertDetachedCapabilities(t, snapshot.Capabilities)
	if snapshot.ActiveWindow.Frame.X != -100 {
		t.Fatal("input active window aliases stored observation")
	}
	snapshot.ActiveWindow.Frame.X = 999
	mutateCapabilities(snapshot.Capabilities)
	snapshot, _ = s.CurrentObservation()
	if snapshot.ActiveWindow.Frame.X != -100 {
		t.Fatal("output active window aliases stored observation")
	}
	assertDetachedCapabilities(t, snapshot.Capabilities)
	assertDetachedCapabilities(t, s.Capabilities())
	action := Action{ID: "clone-action", SessionID: s.ID(), ObservationID: snapshot.ID, Kind: ActionWait}
	if err := s.BeginAction(action); err != nil {
		t.Fatal(err)
	}
	receipt := ActionReceipt{ActionID: action.ID, SessionID: s.ID(), Outcome: OutcomeExecuted, ActiveWindowAfter: WindowRef{ID: "window", Frame: &WindowFrame{X: -100}}}
	if err := s.RecordReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	receipt.ActiveWindowAfter.Frame.X = 999
	stored, ok := s.Receipt(action.ID)
	if !ok || stored.ActiveWindowAfter.Frame.X != -100 {
		t.Fatal("receipt input alias")
	}
	stored.ActiveWindowAfter.Frame.X = 999
	last, ok := s.LastReceipt()
	if !ok || last.ActiveWindowAfter.Frame.X != -100 {
		t.Fatal("receipt output alias")
	}
	last.ActiveWindowAfter.Frame.X = 999
	stored, _ = s.Receipt(action.ID)
	if stored.ActiveWindowAfter.Frame.X != -100 {
		t.Fatal("last receipt output alias")
	}
}
