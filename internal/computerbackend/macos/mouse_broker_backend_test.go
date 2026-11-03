package macos

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/computerbackend/native"
	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

// Runs only inside TestHelperProcess, never invokes real OS input APIs.
func runMouseBrokerHelper(t *testing.T, mode string, req native.Envelope, codec native.Codec) (cu.Outcome, string) {
	t.Helper()
	var payload struct {
		Kind       cu.ActionKind  `json:"kind"`
		Token      string         `json:"drag_token"`
		BatchToken string         `json:"input_batch_token"`
		BatchCount int            `json:"input_batch_count"`
		Button     cu.MouseButton `json:"button"`
	}
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(os.Args, " "), mouseBrokerArgument) {
		t.Fatal("missing broker argument")
	}
	if payload.BatchToken != "" {
		if payload.BatchCount <= 0 {
			t.Fatal("missing batch count")
		}
		requests, responses := os.NewFile(3, "broker requests"), os.NewFile(4, "broker responses")
		reader := bufio.NewReader(responses)
		for seq := 1; seq <= payload.BatchCount; seq++ {
			r := inputBatchRequest{Token: payload.BatchToken, Sequence: int64(seq), Phase: inputBatchPhase}
			body, _ := json.Marshal(r)
			if _, err := requests.Write(append(body, '\n')); err != nil {
				t.Fatal(err)
			}
			line, err := reader.ReadBytes('\n')
			if err != nil {
				t.Fatal(err)
			}
			var response mouseBrokerResponse
			if err := json.Unmarshal(line, &response); err != nil {
				t.Fatal(err)
			}
			if response.Sequence != int64(seq) || !response.OK || response.ErrorCode != "" {
				t.Fatalf("unexpected batch response %+v", response)
			}
			if mode == "broker-batch-sigkill" && seq == 1 {
				if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
					t.Fatal(err)
				}
				select {}
			}
		}
		return cu.OutcomeExecuted, ""
	}
	if payload.Kind != cu.ActionDrag {
		return cu.OutcomeExecuted, ""
	}
	if payload.Token == "" {
		t.Fatal("missing drag broker token")
	}
	if payload.Button == "" {
		payload.Button = cu.MouseButtonLeft
	}
	if mode == "broker-no-events" {
		return cu.OutcomeExecuted, ""
	}
	requests, responses := os.NewFile(3, "broker requests"), os.NewFile(4, "broker responses")
	reader := bufio.NewReader(responses)
	seq := int64(0)
	exchange := func(phase string, ok bool, code string) {
		seq++
		r := mouseRequest(payload.Token, seq, phase, payload.Button)
		body, _ := json.Marshal(r)
		if _, err := requests.Write(append(body, '\n')); err != nil {
			t.Fatal(err)
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response mouseBrokerResponse
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if response.Sequence != seq || response.OK != ok || response.ErrorCode != code {
			t.Fatalf("unexpected broker response %+v", response)
		}
	}
	if mode == "broker-lost-down-ack" {
		body, _ := json.Marshal(mouseRequest(payload.Token, 1, mousePhaseDown, payload.Button))
		if _, err := requests.Write(append(body, '\n')); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Hour) // never read the down ACK; host will kill this fixture
	}
	exchange(mousePhaseDown, true, "")
	switch mode {
	case "broker-exit":
		syscall.Exit(0)
	case "broker-sigkill":
		// A real uncatchable process death, not a cooperative helper return.
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		select {}
	case "broker-hang":
		time.Sleep(time.Hour)
	case "broker-reject-after-down":
		return cu.OutcomeRejected, mouseBrokerInputUnavailable
	case "broker-success-held":
		return cu.OutcomeExecuted, ""
	case "broker-channel-eof":
		requests.Close()
		return cu.OutcomeExecuted, ""
	case "broker-pause":
		control, err := codec.ReadFrame(os.Stdin)
		if err != nil || control.Command != commandPause {
			t.Fatalf("expected pause, got %+v %v", control, err)
		}
		exchange(mousePhaseDrag, false, helperInactiveCode)
		exchange(mousePhaseUp, true, "")
		control.Payload, _ = json.Marshal(map[string]any{"ok": true, "outcome": cu.OutcomeExecuted, "result": map[string]any{}})
		if err := codec.WriteFrame(os.Stdout, control); err != nil {
			t.Fatal(err)
		}
		return cu.OutcomeUnknown, helperInactiveCode
	}
	exchange(mousePhaseDrag, true, "")
	exchange(mousePhaseUp, true, "")
	exchange(mousePhaseUp, true, "")
	if mode == "broker-unknown" {
		return cu.OutcomeUnknown, ""
	}
	return cu.OutcomeExecuted, ""
}

func newBrokerBackend(t *testing.T, mode string, driver *fakeMouseDriver) *Backend {
	t.Helper()
	config := helperConfig(t, mode, time.Second, "")
	config.mouseDriver = driver
	b, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = b.Close(context.Background())
		select {
		case <-b.mouseBroker.done:
		case <-time.After(time.Second):
			t.Error("broker leaked")
		}
		select {
		case <-b.process.Exited():
		case <-time.After(time.Second):
			t.Error("helper not reaped")
		}
	})
	return b
}
func brokerDragAction(obs cu.Observation, button cu.MouseButton) cu.Action {
	return cu.Action{ID: "action-drag", SessionID: obs.SessionID, ObservationID: obs.ID, Kind: cu.ActionDrag, StartPoint: &cu.Point{X: 0, Y: 0}, Point: &cu.Point{X: 1, Y: 1}, Button: string(button), DurationMS: 50}
}
func awaitMouseDown(t *testing.T, driver *fakeMouseDriver) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(driver.snapshot()) > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fixture did not publish down")
}

type brokerExecuteResult struct {
	receipt cu.ActionReceipt
	err     error
}

func asyncBrokerExecute(b *Backend, ctx context.Context, action cu.Action) <-chan brokerExecuteResult {
	result := make(chan brokerExecuteResult, 1)
	go func() { receipt, err := b.Execute(ctx, action); result <- brokerExecuteResult{receipt, err} }()
	return result
}
func awaitBrokerExecute(t *testing.T, result <-chan brokerExecuteResult) brokerExecuteResult {
	t.Helper()
	select {
	case r := <-result:
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("execute did not finish")
		return brokerExecuteResult{}
	}
}

func TestBackendBrokerNormalAndUncertainReceipts(t *testing.T) {
	for _, button := range []cu.MouseButton{cu.MouseButtonLeft, cu.MouseButtonRight} {
		for _, mode := range []string{"broker-normal", "broker-exit", "broker-sigkill", "broker-reject-after-down", "broker-success-held", "broker-no-events", "broker-channel-eof", "broker-unknown"} {
			t.Run(string(button)+"/"+mode, func(t *testing.T) {
				d := &fakeMouseDriver{}
				b := newBrokerBackend(t, mode, d)
				if d.checks != 0 {
					t.Fatal("non-drag setup checked host permissions")
				}
				obs := observeTest(t, b)
				receipt, err := b.Execute(context.Background(), brokerDragAction(obs, button))
				if mode == "broker-normal" {
					if err != nil || receipt.Outcome != cu.OutcomeExecuted {
						t.Fatalf("normal: %+v %v", receipt, err)
					}
				} else if err == nil || receipt.Outcome != cu.OutcomeUnknown || receipt.Verification != cu.VerificationUnknown {
					t.Fatalf("unsafe receipt: %+v %v", receipt, err)
				}
				if mode == "broker-no-events" {
					assertMousePhases(t, d)
				} else if mode == "broker-normal" || mode == "broker-unknown" {
					assertMousePhases(t, d, mousePhaseDown, mousePhaseDrag, mousePhaseUp)
				} else {
					assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
				}
				if _, err := b.Execute(context.Background(), brokerDragAction(obs, button)); err == nil {
					t.Fatal("action replay accepted")
				}
			})
		}
	}
}

func batchClickAction(obs cu.Observation, kind cu.ActionKind) cu.Action {
	a := cu.Action{ID: "action-batch", SessionID: obs.SessionID, ObservationID: obs.ID, Kind: kind, Point: &cu.Point{X: 0, Y: 0}}
	if kind == cu.ActionRightClick {
		a.Button = string(cu.MouseButtonRight)
	}
	return a
}
func newBatchBrokerBackend(t *testing.T, mode string, driver *fakeInputBatchDriver) *Backend {
	t.Helper()
	config := helperConfig(t, mode, time.Second, "")
	config.batchDriver = driver
	config.mouseDriver = &fakeMouseDriver{}
	b, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = b.Close(context.Background())
		select {
		case <-b.mouseBroker.done:
		case <-time.After(time.Second):
			t.Error("batch broker leaked")
		}
		select {
		case <-b.process.Exited():
		case <-time.After(time.Second):
			t.Error("batch helper not reaped")
		}
	})
	return b
}

func TestBackendBrokerBatchNormalAndHelperCrash(t *testing.T) {
	cases := []struct {
		name   string
		action func(cu.Observation) cu.Action
		want   int
	}{
		{"click", func(o cu.Observation) cu.Action { return batchClickAction(o, cu.ActionClick) }, 1},
		{"double-click", func(o cu.Observation) cu.Action { return batchClickAction(o, cu.ActionDoubleClick) }, 2},
		{"right-click", func(o cu.Observation) cu.Action { return batchClickAction(o, cu.ActionRightClick) }, 1},
		{"type", func(o cu.Observation) cu.Action {
			return cu.Action{ID: "action-batch", SessionID: o.SessionID, ObservationID: o.ID, Kind: cu.ActionType, Text: "A😀"}
		}, 2},
		{"key", func(o cu.Observation) cu.Action {
			return cu.Action{ID: "action-batch", SessionID: o.SessionID, ObservationID: o.ID, Kind: cu.ActionKey, Key: "return"}
		}, 1},
		{"hotkey", func(o cu.Observation) cu.Action {
			return cu.Action{ID: "action-batch", SessionID: o.SessionID, ObservationID: o.ID, Kind: cu.ActionHotkey, Keys: []string{"command", "shift", "k"}}
		}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/normal", func(t *testing.T) {
			d := &fakeInputBatchDriver{}
			b := newBatchBrokerBackend(t, "broker-batch-normal", d)
			o := observeTest(t, b)
			receipt, err := b.Execute(context.Background(), tc.action(o))
			if err != nil || receipt.Outcome != cu.OutcomeExecuted {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
			if len(d.committed) != tc.want {
				t.Fatalf("committed=%d want=%d", len(d.committed), tc.want)
			}
		})
		t.Run(tc.name+"/helper-sigkill", func(t *testing.T) {
			d := &fakeInputBatchDriver{}
			b := newBatchBrokerBackend(t, "broker-batch-sigkill", d)
			o := observeTest(t, b)
			receipt, err := b.Execute(context.Background(), tc.action(o))
			if err == nil || receipt.Outcome != cu.OutcomeUnknown || receipt.Verification != cu.VerificationUnknown {
				t.Fatalf("unsafe receipt=%+v err=%v", receipt, err)
			}
			if len(d.committed) != 1 {
				t.Fatalf("helper crash committed=%d want=1", len(d.committed))
			}
		})
	}
}

func TestBackendBrokerRejectsPermissionAndPhysicalHeld(t *testing.T) {
	for _, held := range []bool{false, true} {
		d := &fakeMouseDriver{held: held}
		if !held {
			d.permissionErr = errors.New("TCC denied")
		}
		b := newBrokerBackend(t, "broker-normal", d)
		obs := observeTest(t, b)
		receipt, err := b.Execute(context.Background(), brokerDragAction(obs, cu.MouseButtonLeft))
		if err == nil || receipt.Outcome != cu.OutcomeRejected {
			t.Fatalf("unsafe authorization %+v %v", receipt, err)
		}
		assertMousePhases(t, d)
	}
}

func TestBackendBrokerAbortCancelCloseAndLostACK(t *testing.T) {
	for _, action := range []string{"abort", "cancel", "close", "lost-ack", "concurrent"} {
		t.Run(action, func(t *testing.T) {
			d := &fakeMouseDriver{}
			mode := "broker-hang"
			if action == "lost-ack" {
				mode = "broker-lost-down-ack"
			}
			b := newBrokerBackend(t, mode, d)
			obs := observeTest(t, b)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := asyncBrokerExecute(b, ctx, brokerDragAction(obs, cu.MouseButtonRight))
			awaitMouseDown(t, d)
			switch action {
			case "cancel":
				cancel()
			case "close":
				_ = b.Close(context.Background())
			case "concurrent":
				var wg sync.WaitGroup
				for _, f := range []func(){cancel, func() { b.process.Abort(errors.New("fault")) }, func() { _ = b.Close(context.Background()) }} {
					wg.Add(1)
					go func(f func()) { defer wg.Done(); f() }(f)
				}
				wg.Wait()
			default:
				b.process.Abort(errors.New("fault"))
			}
			r := awaitBrokerExecute(t, result)
			if r.err == nil || r.receipt.Outcome != cu.OutcomeUnknown {
				t.Fatalf("uncertain gesture misreported: %+v", r)
			}
			assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
		})
	}
}

func TestBackendBrokerPauseRemainsResumable(t *testing.T) {
	d := &fakeMouseDriver{}
	b := newBrokerBackend(t, "broker-pause", d)
	obs := observeTest(t, b)
	result := asyncBrokerExecute(b, context.Background(), brokerDragAction(obs, cu.MouseButtonLeft))
	awaitMouseDown(t, d)
	if err := b.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := awaitBrokerExecute(t, result)
	if r.err == nil || r.receipt.Outcome != cu.OutcomeUnknown {
		t.Fatalf("pause result %+v", r)
	}
	if b.failed || b.stopped {
		t.Fatal("cooperative inactive poisoned helper")
	}
	assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
	if err := b.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	fresh := observeTest(t, b)
	if receipt, err := b.Execute(context.Background(), waitAction(fresh)); err != nil || receipt.Outcome != cu.OutcomeExecuted {
		t.Fatalf("resume %+v %v", receipt, err)
	}
}

func TestBackendBrokerReleaseFailureNeverSuccess(t *testing.T) {
	d := &fakeMouseDriver{upErr: errors.New("permission revoked during drag")}
	b := newBrokerBackend(t, "broker-success-held", d)
	obs := observeTest(t, b)
	receipt, err := b.Execute(context.Background(), brokerDragAction(obs, cu.MouseButtonLeft))
	if err == nil || receipt.Outcome != cu.OutcomeUnknown {
		t.Fatalf("release failure %+v %v", receipt, err)
	}
	if err := b.Close(context.Background()); err == nil {
		t.Fatal("close concealed release failure")
	}
	assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
}
