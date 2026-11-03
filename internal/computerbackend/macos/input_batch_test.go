package macos

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type fakePreparedBatch struct {
	driver *fakeInputBatchDriver
	op     inputBatchOperation
	closed bool
}

func (p *fakePreparedBatch) commit() { p.driver.committed = append(p.driver.committed, p.op) }
func (p *fakePreparedBatch) close()  { p.closed = true }

type fakeInputBatchDriver struct {
	prepared   []inputBatchOperation
	committed  []inputBatchOperation
	prepareErr error
}

func (d *fakeInputBatchDriver) prepareBatch(op inputBatchOperation) (preparedInputBatch, error) {
	if d.prepareErr != nil {
		return nil, d.prepareErr
	}
	d.prepared = append(d.prepared, op)
	return &fakePreparedBatch{driver: d, op: op}, nil
}

func batchObservation() cu.Observation {
	return cu.Observation{
		ID: "obs", SessionID: "session", Width: 200, Height: 100,
		Capabilities: cu.Capabilities{CoordinateSpace: cu.CoordinateSpace{
			DisplayID: "1", Origin: cu.OriginTopLeft, Unit: cu.CoordinatePixels,
			Width: 200, Height: 100, ScaleFactor: 2,
			Bounds: &cu.WindowFrame{X: -100, Y: -20, Width: 100, Height: 50},
		}},
	}
}
func TestPlanInputBatchesUsesObservedGeometryAndBalancedUnits(t *testing.T) {
	obs := batchObservation()
	point := &cu.Point{X: 20, Y: 40}
	cases := []struct {
		name   string
		action cu.Action
		want   int
		check  func([]inputBatchOperation)
	}{
		{"click", cu.Action{Kind: cu.ActionClick, Point: point}, 1, func(g []inputBatchOperation) {
			if g[0].button != cu.MouseButtonLeft || g[0].x != -90 || g[0].y != 0 {
				t.Fatalf("click geometry %+v", g[0])
			}
		}},
		{"right", cu.Action{Kind: cu.ActionRightClick, Point: point}, 1, func(g []inputBatchOperation) {
			if g[0].button != cu.MouseButtonRight {
				t.Fatalf("right button %+v", g[0])
			}
		}},
		{"double", cu.Action{Kind: cu.ActionDoubleClick, Point: point}, 2, func(g []inputBatchOperation) {
			if g[0].clickCount != 1 || g[1].clickCount != 2 {
				t.Fatalf("double counts %+v", g)
			}
		}},
		{"unicode", cu.Action{Kind: cu.ActionType, Text: "A😀"}, 2, func(g []inputBatchOperation) {
			if g[0].scalar != 'A' || g[1].scalar != '😀' {
				t.Fatalf("scalars %+v", g)
			}
		}},
		{"key", cu.Action{Kind: cu.ActionKey, Key: "return"}, 1, func(g []inputBatchOperation) {
			if g[0].keyCode != 36 || g[0].flags != 0 {
				t.Fatalf("key %+v", g)
			}
		}},
		{"hotkey", cu.Action{Kind: cu.ActionHotkey, Keys: []string{"command", "shift", "k"}}, 1, func(g []inputBatchOperation) {
			if g[0].keyCode != 40 || g[0].flags != (batchFlagCommand|batchFlagShift) {
				t.Fatalf("hotkey %+v", g)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := planInputBatches(tc.action, obs)
			if err != nil || len(got) != tc.want {
				t.Fatalf("plan=%+v err=%v", got, err)
			}
			tc.check(got)
		})
	}
	if _, err := planInputBatches(cu.Action{Kind: cu.ActionClick, Point: &cu.Point{X: 200, Y: 0}}, obs); err == nil {
		t.Fatal("out-of-bounds point accepted")
	}
	if _, err := planInputBatches(cu.Action{Kind: cu.ActionType, Text: string([]byte{0xff})}, obs); err == nil {
		t.Fatal("invalid utf8 text accepted")
	}
}

func TestMouseBrokerInputBatchIsSingleUseAndAtomicPerOperation(t *testing.T) {
	d := &fakeInputBatchDriver{}
	b, files, err := newMouseBroker(&fakeMouseDriver{}, d)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = b.close()
		for _, f := range files {
			_ = f.Close()
		}
		<-b.done
	}()
	action := cu.Action{ID: "action", SessionID: "session", ObservationID: "obs", Kind: cu.ActionDoubleClick, Point: &cu.Point{X: 20, Y: 40}}
	token, count, err := b.authorizeBatch(context.Background(), action, batchObservation(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count=%d", count)
	}
	if reply := b.handleBatch(inputBatchRequest{Token: token, Sequence: 1, Phase: inputBatchPhase}); !reply.OK {
		t.Fatalf("first batch %+v", reply)
	}
	if len(d.committed) != 1 || d.committed[0].clickCount != 1 {
		t.Fatalf("first commit %+v", d.committed)
	}
	if reply := b.handleBatch(inputBatchRequest{Token: token, Sequence: 1, Phase: inputBatchPhase}); reply.OK {
		t.Fatal("replayed batch accepted")
	}
	if reply := b.handleBatch(inputBatchRequest{Token: token, Sequence: 2, Phase: inputBatchPhase}); !reply.OK {
		t.Fatalf("second batch %+v", reply)
	}
	result := b.finishBatch(token)
	if !result.started || !result.complete {
		t.Fatalf("finish=%+v", result)
	}
	if len(d.committed) != 2 || d.committed[1].clickCount != 2 {
		t.Fatalf("commits %+v", d.committed)
	}
	if reply := b.handleBatch(inputBatchRequest{Token: token, Sequence: 3, Phase: inputBatchPhase}); reply.OK {
		t.Fatal("post-finish batch accepted")
	}
}

func TestMouseBrokerInputBatchRevocationBeforeCommitPostsNothing(t *testing.T) {
	d := &fakeInputBatchDriver{}
	b, files, err := newMouseBroker(&fakeMouseDriver{}, d)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = b.close()
		for _, f := range files {
			_ = f.Close()
		}
		<-b.done
	}()
	action := cu.Action{ID: "action", SessionID: "session", ObservationID: "obs", Kind: cu.ActionClick, Point: &cu.Point{X: 1, Y: 1}}
	token, _, err := b.authorizeBatch(context.Background(), action, batchObservation(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b.revoke()
	reply := b.handleBatch(inputBatchRequest{Token: token, Sequence: 1, Phase: inputBatchPhase})
	if reply.OK || len(d.prepared) != 0 || len(d.committed) != 0 {
		t.Fatalf("revoked batch reply=%+v prepared=%v committed=%v", reply, d.prepared, d.committed)
	}
	result := b.finishBatch(token)
	if result.started || result.complete {
		t.Fatalf("revoked finish=%+v", result)
	}
}

func TestPlanInputBatchesRejectsDuplicateHotkeyModifiers(t *testing.T) {
	_, err := planInputBatches(cu.Action{Kind: cu.ActionHotkey, Keys: []string{"command", "cmd", "k"}}, batchObservation())
	if !errors.Is(err, errors.New("unused")) && err == nil {
		t.Fatal("duplicate hotkey accepted")
	}
	// A semantic check keeps this test independent of error identity.
	if _, err = planInputBatches(cu.Action{Kind: cu.ActionHotkey, Keys: []string{"command", "cmd", "k"}}, batchObservation()); err == nil {
		t.Fatal("duplicate hotkey accepted")
	}
}

func TestFakeBatchDriverRecordsExactPlan(t *testing.T) {
	d := &fakeInputBatchDriver{}
	op := inputBatchOperation{kind: cu.ActionType, scalar: '😀'}
	p, err := d.prepareBatch(op)
	if err != nil {
		t.Fatal(err)
	}
	p.commit()
	p.close()
	if !reflect.DeepEqual(d.committed, []inputBatchOperation{op}) {
		t.Fatalf("committed=%+v", d.committed)
	}
}
