package macos

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
)

type recordedMouseEvent struct {
	phase  string
	button cu.MouseButton
	x, y   float64
}

type fakeMouseDriver struct {
	mu                                        sync.Mutex
	permissionErr, prepareErr, dragErr, upErr error
	held                                      bool
	checks, prepares, closes                  int
	events                                    []recordedMouseEvent
	dragEntered, dragContinue                 chan struct{}
}

func (d *fakeMouseDriver) checkPermission() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.checks++
	return d.permissionErr
}
func (d *fakeMouseDriver) buttonsHeld() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.held
}
func (d *fakeMouseDriver) prepare(button cu.MouseButton, x, y float64) (mouseGesture, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.prepares++
	if d.prepareErr != nil {
		return nil, d.prepareErr
	}
	return &fakeMouseGesture{driver: d, button: button, x: x, y: y}, nil
}
func (d *fakeMouseDriver) snapshot() []recordedMouseEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]recordedMouseEvent(nil), d.events...)
}

type fakeMouseGesture struct {
	driver *fakeMouseDriver
	button cu.MouseButton
	x, y   float64
}

func (g *fakeMouseGesture) down() {
	g.driver.mu.Lock()
	defer g.driver.mu.Unlock()
	g.driver.events = append(g.driver.events, recordedMouseEvent{mousePhaseDown, g.button, g.x, g.y})
}
func (g *fakeMouseGesture) drag(x, y float64) error {
	d := g.driver
	if d.dragEntered != nil {
		close(d.dragEntered)
		<-d.dragContinue
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dragErr != nil {
		return d.dragErr
	}
	d.events = append(d.events, recordedMouseEvent{mousePhaseDrag, g.button, x, y})
	return nil
}
func (g *fakeMouseGesture) up(x, y float64) error {
	d := g.driver
	d.mu.Lock()
	defer d.mu.Unlock()
	// Record attempts, even when the synthetic driver rejects publication.
	d.events = append(d.events, recordedMouseEvent{mousePhaseUp, g.button, x, y})
	return d.upErr
}
func (g *fakeMouseGesture) close() { g.driver.mu.Lock(); g.driver.closes++; g.driver.mu.Unlock() }

func testMouseBroker(t *testing.T, driver *fakeMouseDriver) (*mouseBroker, []*os.File) {
	t.Helper()
	b, files, err := newMouseBroker(driver)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = b.close()
		for _, file := range files {
			file.Close()
		}
		select {
		case <-b.done:
		case <-time.After(time.Second):
			t.Error("broker worker leaked")
		}
	})
	return b, files
}
func authorizeMouse(t *testing.T, b *mouseBroker, button cu.MouseButton) string {
	t.Helper()
	token, err := b.authorize(context.Background(), cu.Action{SessionID: "session", ID: "drag", Button: string(button)}, 42, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func mouseRequest(token string, seq int64, phase string, button cu.MouseButton) mouseBrokerRequest {
	return mouseBrokerRequest{Token: token, Sequence: seq, Phase: phase, Button: button, X: -150.25 + float64(seq), Y: 200.5 + float64(seq)}
}
func assertMousePhases(t *testing.T, d *fakeMouseDriver, want ...string) {
	t.Helper()
	var got []string
	for _, event := range d.snapshot() {
		got = append(got, event.phase)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

func TestMouseBrokerLifecycleAndSingleConsumption(t *testing.T) {
	for _, button := range []cu.MouseButton{cu.MouseButtonLeft, cu.MouseButtonRight} {
		t.Run(string(button), func(t *testing.T) {
			d := &fakeMouseDriver{}
			b, _ := testMouseBroker(t, d)
			if d.checks != 0 || len(d.snapshot()) != 0 {
				t.Fatal("construction touched permissions or input")
			}
			token := authorizeMouse(t, b, button)
			if len(token) != mouseBrokerTokenBytes*2 || b.lease.epoch != 42 || b.lease.actionID != "drag" || b.lease.sessionID != "session" {
				t.Fatal("lease binding missing")
			}
			if _, err := b.authorize(context.Background(), cu.Action{}, 43, time.Second); err == nil {
				t.Fatal("overlapping lease accepted")
			}
			for i, phase := range []string{mousePhaseDown, mousePhaseDrag, mousePhaseUp} {
				if reply := b.handle(mouseRequest(token, int64(i+1), phase, button)); !reply.OK || reply.ErrorCode != "" {
					t.Fatalf("%s: %+v", phase, reply)
				}
			}
			if !b.handle(mouseRequest(token, 3, mousePhaseUp, button)).OK {
				t.Fatal("identical up ACK replay denied")
			}
			if !b.handle(mouseRequest(token, 4, mousePhaseUp, button)).OK {
				t.Fatal("consumed up denied")
			}
			if b.handle(mouseRequest(token, 5, mousePhaseDown, button)).OK {
				t.Fatal("second down accepted")
			}
			if result := b.finish(token); !result.complete || result.err != nil {
				t.Fatalf("finish %+v", result)
			}
			_ = b.close()
			_ = b.close()
			assertMousePhases(t, d, mousePhaseDown, mousePhaseDrag, mousePhaseUp)
			if d.closes != 1 {
				t.Fatal("gesture not disposed exactly once")
			}
			for _, event := range d.snapshot() {
				if event.button != button {
					t.Fatal("wrong button posted")
				}
			}
		})
	}
}

func TestMouseBrokerRevokeAllowsOnlyRelease(t *testing.T) {
	for _, beforeDown := range []bool{false, true} {
		t.Run(fmt.Sprint(beforeDown), func(t *testing.T) {
			d := &fakeMouseDriver{}
			b, _ := testMouseBroker(t, d)
			token := authorizeMouse(t, b, cu.MouseButtonLeft)
			seq := int64(1)
			if !beforeDown {
				if !b.handle(mouseRequest(token, seq, mousePhaseDown, cu.MouseButtonLeft)).OK {
					t.Fatal("down failed")
				}
				seq++
			}
			b.revoke()
			for _, phase := range []string{mousePhaseDown, mousePhaseDrag} {
				reply := b.handle(mouseRequest(token, seq, phase, cu.MouseButtonLeft))
				seq++
				if reply.OK || reply.ErrorCode != helperInactiveCode {
					t.Fatalf("revoke response %+v", reply)
				}
			}
			reply := b.handle(mouseRequest(token, seq, mousePhaseUp, cu.MouseButtonLeft))
			if reply.OK == beforeDown {
				t.Fatalf("release response %+v", reply)
			}
			_ = b.finish(token)
			if beforeDown {
				assertMousePhases(t, d)
			} else {
				assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
			}
		})
	}
}

func TestMouseBrokerAdmissionAndPressChecks(t *testing.T) {
	for _, kind := range []string{"permission-at-authorize", "held-at-authorize", "permission-at-press", "held-at-press", "prepare-failure", "cancelled", "expired"} {
		t.Run(kind, func(t *testing.T) {
			d := &fakeMouseDriver{}
			b, _ := testMouseBroker(t, d)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "permission-at-authorize" {
				d.permissionErr = errors.New("denied")
			}
			if kind == "held-at-authorize" {
				d.held = true
			}
			token, err := b.authorize(ctx, cu.Action{ID: "drag", SessionID: "session"}, 1, time.Second)
			if strings.HasSuffix(kind, "at-authorize") {
				if err == nil {
					t.Fatal("unsafe authorization accepted")
				}
				assertMousePhases(t, d)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "permission-at-press":
				d.permissionErr = errors.New("revoked")
			case "held-at-press":
				d.held = true
			case "prepare-failure":
				d.prepareErr = errors.New("allocation failed")
			case "cancelled":
				cancel()
			case "expired":
				b.lease.deadline = time.Now().Add(-time.Second)
			}
			if reply := b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft)); reply.OK || reply.ErrorCode != mouseBrokerInputUnavailable {
				t.Fatalf("unsafe press %+v", reply)
			}
			_ = b.finish(token)
			assertMousePhases(t, d)
		})
	}
}

func TestMouseBrokerBindingAndSequences(t *testing.T) {
	mutations := map[string]func(*mouseBrokerRequest){
		"token":            func(r *mouseBrokerRequest) { r.Token = "other-helper-or-action" },
		"button":           func(r *mouseBrokerRequest) { r.Button = cu.MouseButtonRight },
		"zero":             func(r *mouseBrokerRequest) { r.Sequence = 0 },
		"gap":              func(r *mouseBrokerRequest) { r.Sequence = 2 },
		"overflow":         func(r *mouseBrokerRequest) { r.Sequence = mouseBrokerMaxSequence + 1 },
		"nan":              func(r *mouseBrokerRequest) { r.X = math.NaN() },
		"infinity":         func(r *mouseBrokerRequest) { r.Y = math.Inf(1) },
		"phase":            func(r *mouseBrokerRequest) { r.Phase = "click" },
		"drag-before-down": func(r *mouseBrokerRequest) { r.Phase = mousePhaseDrag },
		"up-before-down":   func(r *mouseBrokerRequest) { r.Phase = mousePhaseUp },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			d := &fakeMouseDriver{}
			b, _ := testMouseBroker(t, d)
			token := authorizeMouse(t, b, cu.MouseButtonLeft)
			r := mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft)
			mutate(&r)
			if b.handle(r).OK {
				t.Fatal("invalid event accepted")
			}
			assertMousePhases(t, d)
		})
	}
	d := &fakeMouseDriver{}
	b, _ := testMouseBroker(t, d)
	old := authorizeMouse(t, b, cu.MouseButtonLeft)
	_ = b.finish(old)
	token := authorizeMouse(t, b, cu.MouseButtonLeft)
	if old == token {
		t.Fatal("token reused")
	}
	if b.handle(mouseRequest(old, 1, mousePhaseDown, cu.MouseButtonLeft)).OK {
		t.Fatal("old action token accepted")
	}
	if !b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft)).OK {
		t.Fatal("new action rejected")
	}
	if b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft)).OK {
		t.Fatal("down replay accepted")
	}
	_ = b.finish(token)
	if b.handle(mouseRequest(token, 2, mousePhaseDrag, cu.MouseButtonLeft)).OK {
		t.Fatal("late drag accepted")
	}
	assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
}

func TestMouseBrokerFinishRecoversButDoesNotCertifySuccess(t *testing.T) {
	d := &fakeMouseDriver{}
	b, _ := testMouseBroker(t, d)
	token := authorizeMouse(t, b, cu.MouseButtonRight)
	b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonRight))
	last := mouseRequest(token, 2, mousePhaseDrag, cu.MouseButtonRight)
	b.handle(last)
	result := b.finish(token)
	if !result.pressed || result.complete || result.err != nil {
		t.Fatalf("recovery %+v", result)
	}
	events := d.snapshot()
	if events[2].x != last.X || events[2].y != last.Y {
		t.Fatal("cleanup moved cursor")
	}
	_ = b.finish(token)
	_ = b.close()
	assertMousePhases(t, d, mousePhaseDown, mousePhaseDrag, mousePhaseUp)
}

func TestMouseBrokerFailureStillConsumesUpOnce(t *testing.T) {
	for _, failure := range []string{"drag", "up"} {
		t.Run(failure, func(t *testing.T) {
			d := &fakeMouseDriver{}
			b, _ := testMouseBroker(t, d)
			token := authorizeMouse(t, b, cu.MouseButtonLeft)
			b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
			if failure == "drag" {
				d.dragErr = errors.New("lost permission")
				b.handle(mouseRequest(token, 2, mousePhaseDrag, cu.MouseButtonLeft))
			} else {
				d.upErr = errors.New("lost permission")
			}
			result := b.finish(token)
			if result.err == nil || result.complete {
				t.Fatal("failed gesture certified")
			}
			b.finish(token)
			_ = b.close()
			_ = b.close()
			assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
			if d.closes != 1 {
				t.Fatal("release resource leaked")
			}
		})
	}
}

func TestMouseBrokerCloseSerializesWithInFlightDrag(t *testing.T) {
	d := &fakeMouseDriver{dragEntered: make(chan struct{}), dragContinue: make(chan struct{})}
	b, _ := testMouseBroker(t, d)
	token := authorizeMouse(t, b, cu.MouseButtonLeft)
	b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
	dragDone := make(chan struct{})
	go func() { b.handle(mouseRequest(token, 2, mousePhaseDrag, cu.MouseButtonLeft)); close(dragDone) }()
	<-d.dragEntered
	closeDone := make(chan struct{})
	go func() { _ = b.close(); close(closeDone) }()
	select {
	case <-closeDone:
		t.Fatal("close overtook an input publisher")
	default:
	}
	close(d.dragContinue)
	<-dragDone
	<-closeDone
	if b.handle(mouseRequest(token, 3, mousePhaseDrag, cu.MouseButtonLeft)).OK {
		t.Fatal("drag after cleanup")
	}
	assertMousePhases(t, d, mousePhaseDown, mousePhaseDrag, mousePhaseUp)
}

func TestMouseBrokerConcurrentTerminalPaths(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		d := &fakeMouseDriver{}
		b, _ := testMouseBroker(t, d)
		token := authorizeMouse(t, b, cu.MouseButtonLeft)
		b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
		var wg sync.WaitGroup
		for _, f := range []func(){func() { b.revoke() }, func() { _ = b.close() }, func() { _ = b.finish(token) }, func() { b.handle(mouseRequest(token, 2, mousePhaseUp, cu.MouseButtonLeft)) }} {
			wg.Add(1)
			go func(f func()) { defer wg.Done(); f() }(f)
		}
		wg.Wait()
		assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
	}
}

func TestMouseBrokerWireAndEOFRelease(t *testing.T) {
	d := &fakeMouseDriver{}
	b, files := testMouseBroker(t, d)
	token := authorizeMouse(t, b, cu.MouseButtonLeft)
	encoded, _ := json.Marshal(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
	if _, err := files[0].Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	var response mouseBrokerResponse
	if err := json.NewDecoder(files[1]).Decode(&response); err != nil || !response.OK {
		t.Fatalf("reply %+v, %v", response, err)
	}
	files[0].Close()
	select {
	case <-b.done:
	case <-time.After(time.Second):
		t.Fatal("EOF did not release")
	}
	assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
}

func TestMouseBrokerStrictWireSchema(t *testing.T) {
	valid := `{"token":"x","sequence":1,"phase":"down","button":"left","x":0,"y":1}`
	if _, err := decodeMouseBrokerRequest([]byte(valid + "\n")); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{}`, `[]`, valid + valid, valid + ` garbage`,
		strings.Replace(valid, `"x":0`, `"x":null`, 1),
		strings.Replace(valid, `"x":0,`, ``, 1),
		strings.Replace(valid, `"x":0`, `"x":0,"x":1`, 1),
		strings.Replace(valid, `"x":0`, `"other":0`, 1),
		strings.Replace(valid, `"sequence":1`, `"sequence":1.5`, 1),
		strings.Replace(valid, `"sequence":1`, `"sequence":9223372036854775808`, 1),
		strings.Replace(valid, `"y":1`, `"y":1e999`, 1),
	} {
		if _, err := decodeMouseBrokerRequest([]byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestMouseBrokerOversizeAndTruncatedChannel(t *testing.T) {
	for _, payload := range []string{strings.Repeat("x", mouseBrokerFrameLimit), `{"token":`} {
		d := &fakeMouseDriver{}
		b, files := testMouseBroker(t, d)
		token := authorizeMouse(t, b, cu.MouseButtonLeft)
		b.handle(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
		_, _ = files[0].Write([]byte(payload))
		files[0].Close()
		select {
		case <-b.done:
		case <-time.After(time.Second):
			t.Fatal("invalid channel left lease held")
		}
		assertMousePhases(t, d, mousePhaseDown, mousePhaseUp)
	}
}

func TestMouseBrokerFrameLimitIncludesNewline(t *testing.T) {
	for _, extra := range []int{0, 1} {
		d := &fakeMouseDriver{}
		b, files := testMouseBroker(t, d)
		token := authorizeMouse(t, b, cu.MouseButtonLeft)
		encoded, _ := json.Marshal(mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft))
		line := append(encoded, []byte(strings.Repeat(" ", mouseBrokerFrameLimit-len(encoded)-1+extra)+"\n")...)
		_, _ = files[0].Write(line)
		if extra == 0 {
			response, err := bufio.NewReader(files[1]).ReadBytes('\n')
			if err != nil || !strings.Contains(string(response), `"ok":true`) {
				t.Fatalf("boundary frame %s %v", response, err)
			}
		} else {
			select {
			case <-b.done:
			case <-time.After(time.Second):
				t.Fatal("oversize accepted")
			}
			assertMousePhases(t, d)
		}
	}
}

func TestMouseBrokerLostDragACKReleaseUsesHostPosition(t *testing.T) {
	d := &fakeMouseDriver{}
	b, _ := testMouseBroker(t, d)
	token := authorizeMouse(t, b, cu.MouseButtonLeft)
	down := mouseRequest(token, 1, mousePhaseDown, cu.MouseButtonLeft)
	if !b.handle(down).OK {
		t.Fatal("down failed")
	}
	drag := mouseRequest(token, 2, mousePhaseDrag, cu.MouseButtonLeft)
	drag.X, drag.Y = 450.5, -250.25
	_ = b.handle(drag) // deliberately lose the successful ACK
	up := mouseRequest(token, 3, mousePhaseUp, cu.MouseButtonLeft)
	up.X, up.Y = down.X, down.Y // Swift still remembers its last acknowledged point
	if !b.handle(up).OK {
		t.Fatal("cleanup up failed")
	}
	if !b.handle(up).OK {
		t.Fatal("consumed up not acknowledged")
	}
	events := d.snapshot()
	if len(events) != 3 || events[2].x != drag.X || events[2].y != drag.Y {
		t.Fatalf("release jumped back: %+v", events)
	}
}
