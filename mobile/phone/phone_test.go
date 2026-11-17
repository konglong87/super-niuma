package phone

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

const own = "io.github.konglong87.superniuma.mobile"
const clock = "com.example.clock"

// fakePhone is a tiny clock app: a list with an alarm row and buttons.
type fakePhone struct {
	mu        sync.Mutex
	screen    Snapshot
	performed []Action
	result    Result
	onPerform func(*fakePhone, Action)
	noWindow  int // snapshots that fail with ErrNoWindow first
}

func clockScreen(alarm string) Snapshot {
	nodes := []Node{
		{Parent: -1, Class: "android.widget.FrameLayout", Bounds: [4]int{0, 0, 1080, 2400}},
		{Parent: 0, Class: "android.widget.ImageButton", Desc: "Add alarm", ViewID: "fab", Bounds: [4]int{440, 2000, 640, 2200}, Clickable: true},
		{Parent: 0, Class: "android.widget.EditText", Text: "", ViewID: "label", Bounds: [4]int{0, 300, 1080, 400}, Editable: true, Clickable: true},
		{Parent: 0, Class: "android.widget.EditText", ViewID: "pin", Bounds: [4]int{0, 400, 1080, 500}, Editable: true, Password: true},
	}
	if alarm != "" {
		nodes = append(nodes,
			Node{Parent: 0, Class: "android.widget.TextView", Text: alarm, ViewID: "time", Bounds: [4]int{0, 600, 540, 700}},
			Node{Parent: 0, Class: "android.widget.Button", ViewID: "delete", Bounds: [4]int{800, 600, 1000, 700}, Clickable: true},
			Node{Parent: 5, Class: "android.widget.TextView", Text: "Delete", Bounds: [4]int{820, 610, 980, 690}},
		)
	}
	return Snapshot{Package: clock, Home: "com.example.home", Nodes: nodes}
}

func (p *fakePhone) Snapshot() (Snapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.noWindow > 0 {
		p.noWindow--
		return Snapshot{}, ErrNoWindow
	}
	return p.screen, nil
}

func (p *fakePhone) Perform(a Action) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.performed = append(p.performed, a)
	if p.onPerform != nil {
		p.onPerform(p, a)
	}
	if p.result.Status == "" {
		return Result{Status: "done"}, nil
	}
	return p.result, nil
}

func (p *fakePhone) actions() []Action {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Action(nil), p.performed...)
}

func newGate(p *fakePhone, approvals *Approvals, stopped func() bool) (*Gate, *[]Step) {
	var steps []Step
	var mu sync.Mutex
	if stopped == nil {
		stopped = func() bool { return false }
	}
	g := NewGate(p, []string{clock}, own, approvals, stopped, func(s Step) { mu.Lock(); steps = append(steps, s); mu.Unlock() })
	g.retry = time.Millisecond
	return g, &steps
}

func act(t *testing.T, g *Gate, a execution.DeviceAction) (string, error) {
	t.Helper()
	return g.Act(context.Background(), a)
}

func refused(err error, want string) bool {
	var r execution.DeviceRefusal
	return errors.As(err, &r) && strings.Contains(r.Reason, want)
}

// --- screen ---

func TestRenderShowsUsableElementsWithChildLabels(t *testing.T) {
	text := clockScreen("7:00 AM").Render()
	for _, want := range []string{"app: com.example.clock", `[1] ImageButton "Add alarm" @540,2100 click`, `[3] EditText "(password field)"`, `[5] Button "Delete" @900,650 click`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "[0]") {
		t.Fatal("unlabelled container shown")
	}
}

func TestFindMatchesSameElementOnly(t *testing.T) {
	a, b := clockScreen("7:00 AM"), clockScreen("7:00 AM")
	if b.Find(a, 5) != 5 {
		t.Fatal("same element not found")
	}
	moved := clockScreen("7:00 AM")
	moved.Nodes[5].Bounds[1] += 10
	if moved.Find(a, 5) != -1 || clockScreen("").Find(a, 5) != -1 || b.Find(a, 99) != -1 {
		t.Fatal("changed or missing element matched")
	}
}

func TestParseSnapshotRejectsBrokenInput(t *testing.T) {
	for _, s := range []string{`{`, `{"package":"","nodes":[]}`, `{"package":"p","nodes":[{"parent":0,"cls":"x","b":[0,0,1,1]}]}`} {
		if _, err := ParseSnapshot(s); err == nil {
			t.Fatalf("%s accepted", s)
		}
	}
	if _, err := ParseSnapshot(`{"package":"","nodes":[]}`); !errors.Is(err, ErrNoWindow) {
		t.Fatal("empty window not reported as no window")
	}
}

// --- policy ---

func TestLabelRisk(t *testing.T) {
	cases := map[string]Risk{
		"Send": RiskSend, "发送": RiskSend, "Delete": RiskDelete, "删除闹钟": RiskDelete, "Pay $12.80": RiskPay,
		"确认支付": RiskPay, "Place order · $12": RiskPay, "Install": RiskInstall, "Call Sam": RiskCall,
		"Add alarm": RiskNone, "Sending options": RiskNone, "Recall": RiskNone, "OK": RiskNone, "": RiskNone,
	}
	for label, want := range cases {
		if got := LabelRisk(label); got != want {
			t.Errorf("%q: got %q want %q", label, got, want)
		}
	}
	if SubmitRisk("com.google.android.apps.messaging") != RiskSend || SubmitRisk(clock) != RiskNone {
		t.Fatal("submit risk wrong")
	}
	ids := map[string]Risk{
		"com.chat:id/send_button": RiskSend, "sendButton": RiskSend, "com.x:id/delete": RiskDelete,
		"btnPayNow": RiskPay, "com.x:id/sending_status": RiskNone, "com.x:id/fab": RiskNone, "": RiskNone,
	}
	for id, want := range ids {
		if got := IDRisk(id); got != want {
			t.Errorf("id %q: got %q want %q", id, got, want)
		}
	}
}

func TestGateAsksForIconOnlyRiskyButton(t *testing.T) {
	chat := "com.example.chat"
	p := &fakePhone{screen: Snapshot{Package: chat, Nodes: []Node{
		{Parent: -1, Class: "x"}, {Parent: 0, Class: "android.widget.ImageButton", ViewID: chat + ":id/send_button", Bounds: [4]int{900, 2000, 1000, 2100}, Clickable: true},
	}}}
	g := NewGate(p, []string{chat}, own, NewApprovals(5*time.Millisecond, nil), func() bool { return false }, nil)
	g.retry = time.Millisecond
	act(t, g, execution.DeviceAction{Kind: "screen"})
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 1}); !refused(err, "approval_denied") || len(p.actions()) != 0 {
		t.Fatalf("icon-only send ran without approval: %v", err)
	}
}

func TestApprovalLateDecisionIsRefused(t *testing.T) {
	a := NewApprovals(20*time.Millisecond, nil)
	ids := make(chan string, 1)
	go func() {
		for a.Pending() == nil {
			time.Sleep(time.Millisecond)
		}
		ids <- a.Pending().ID
	}()
	if d, _ := a.Ask(context.Background(), Approval{}); d != Deny {
		t.Fatal("timeout did not deny")
	}
	if err := a.Decide(<-ids, Allow); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("late allow accepted: %v", err)
	}
}

// --- approvals ---

func TestApprovalsAllowDenyTimeoutAndCancel(t *testing.T) {
	a := NewApprovals(time.Second, nil)
	answer := func(d Decision) {
		for a.Pending() == nil {
			time.Sleep(time.Millisecond)
		}
		if err := a.Decide("wrong", d); !errors.Is(err, ErrNoApproval) {
			t.Error("wrong id accepted")
		}
		if err := a.Decide(a.Pending().ID, d); err != nil {
			t.Error(err)
		}
	}
	for _, d := range []Decision{Allow, Deny} {
		go answer(d)
		got, err := a.Ask(context.Background(), Approval{Risk: RiskDelete})
		if err != nil || got != d || a.Pending() != nil {
			t.Fatalf("%s: got %s %v", d, got, err)
		}
	}
	short := NewApprovals(10*time.Millisecond, nil)
	if got, err := short.Ask(context.Background(), Approval{}); err != nil || got != Deny {
		t.Fatal("timeout did not deny")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := a.Ask(ctx, Approval{}); err == nil || got != Deny {
		t.Fatal("cancelled ask did not deny")
	}
	if err := a.Decide("x", "maybe"); !errors.Is(err, ErrBadDecision) {
		t.Fatal("bad decision accepted")
	}
}

// --- gate ---

func TestGateScreenLaunchAndScope(t *testing.T) {
	p := &fakePhone{screen: Snapshot{Package: own, Nodes: []Node{{Parent: -1, Class: "x", Text: "Start"}}}}
	g, steps := newGate(p, NewApprovals(time.Second, nil), nil)
	out, err := act(t, g, execution.DeviceAction{Kind: "screen"})
	if err != nil || !strings.Contains(out, "own screen is hidden") || strings.Contains(out, "Start") {
		t.Fatalf("own screen leaked: %q %v", out, err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 0}); !refused(err, "Super Niuma itself") {
		t.Fatalf("tap on own screen: %v", err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "launch", App: "com.android.settings"}); !refused(err, "scope_denied") {
		t.Fatalf("out-of-scope launch: %v", err)
	}
	p.onPerform = func(p *fakePhone, a Action) { p.screen = clockScreen("") }
	out, err = act(t, g, execution.DeviceAction{Kind: "launch", App: clock})
	if err != nil || !strings.Contains(out, "Add alarm") || len(p.actions()) != 1 {
		t.Fatalf("launch: %q %v", out, err)
	}
	p.screen = Snapshot{Package: "com.android.settings", Nodes: []Node{{Parent: -1, Class: "x", Text: "Wi-Fi", Clickable: true}}}
	if _, err := act(t, g, execution.DeviceAction{Kind: "screen"}); err != nil {
		t.Fatal(err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 0}); !refused(err, "scope_denied") {
		t.Fatalf("tap in another app: %v", err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "key", Key: "home"}); err != nil {
		t.Fatalf("home refused: %v", err)
	}
	if len(*steps) == 0 || (*steps)[0].Outcome != "done" {
		t.Fatal("steps not recorded")
	}
}

func TestGateHidesScreensOutsideTheTask(t *testing.T) {
	shade := Snapshot{Package: "com.android.systemui", Nodes: []Node{{Parent: -1, Class: "x"}, {Parent: 0, Class: "android.widget.Switch", Text: "Wi-Fi", Checked: true, Clickable: true}}}
	home := Snapshot{Package: "com.example.home", Home: "com.example.home", Nodes: []Node{{Parent: -1, Class: "x"}, {Parent: 0, Class: "android.widget.TextView", Text: "Clock", Clickable: true}}}
	p := &fakePhone{screen: home}
	g, _ := newGate(p, NewApprovals(time.Second, nil), nil)
	out, err := act(t, g, execution.DeviceAction{Kind: "screen"})
	if err != nil || !strings.Contains(out, `"Clock"`) {
		t.Fatalf("home screen hidden: %q %v", out, err)
	}
	// A swipe on the home screen pulls down the notification shade.
	p.onPerform = func(p *fakePhone, a Action) { p.screen = shade }
	out, err = act(t, g, execution.DeviceAction{Kind: "swipe", Ref: -1, Direction: "up"})
	if err != nil || strings.Contains(out, "Wi-Fi") || !strings.Contains(out, "content is hidden") {
		t.Fatalf("out-of-scope screen shown after an action: %q %v", out, err)
	}
	out, err = act(t, g, execution.DeviceAction{Kind: "screen"})
	if err != nil || strings.Contains(out, "Wi-Fi") || !strings.Contains(out, "com.android.systemui") {
		t.Fatalf("out-of-scope screen shown on read: %q %v", out, err)
	}
}

func TestGateRequiresFreshTargets(t *testing.T) {
	p := &fakePhone{screen: clockScreen("")}
	g, _ := newGate(p, NewApprovals(time.Second, nil), nil)
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 1}); !refused(err, "super_screen first") {
		t.Fatalf("tap before reading: %v", err)
	}
	act(t, g, execution.DeviceAction{Kind: "screen"})
	p.screen = clockScreen("7:00 AM")
	p.screen.Nodes[1].Bounds[0] = 10 // the button moved
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 1}); !refused(err, "stale") || len(p.actions()) != 0 {
		t.Fatalf("stale target acted on: %v", err)
	}
}

func TestGateRefusesCredentialsAndNonEditable(t *testing.T) {
	p := &fakePhone{screen: clockScreen("")}
	g, _ := newGate(p, NewApprovals(time.Second, nil), nil)
	act(t, g, execution.DeviceAction{Kind: "screen"})
	if _, err := act(t, g, execution.DeviceAction{Kind: "type", Ref: 3, Text: "1234"}); !refused(err, "credentials") {
		t.Fatalf("typed into a password field: %v", err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "type", Ref: 1, Text: "x"}); !refused(err, "not editable") {
		t.Fatalf("typed into a button: %v", err)
	}
	if len(p.actions()) != 0 {
		t.Fatal("refused action reached the phone")
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "type", Ref: 2, Text: "Wake up"}); err != nil {
		t.Fatal(err)
	}
	if a := p.actions(); len(a) != 1 || a[0].Index != 2 || a[0].Text != "Wake up" {
		t.Fatalf("performed %+v", a)
	}
}

func answerNext(t *testing.T, a *Approvals, d Decision, before func()) {
	t.Helper()
	go func() {
		for a.Pending() == nil {
			time.Sleep(time.Millisecond)
		}
		if before != nil {
			before()
		}
		if err := a.Decide(a.Pending().ID, d); err != nil {
			t.Error(err)
		}
	}()
}

func TestGateHoldsRiskyTapForApproval(t *testing.T) {
	p := &fakePhone{screen: clockScreen("7:00 AM")}
	approvals := NewApprovals(time.Second, nil)
	g, steps := newGate(p, approvals, nil)
	act(t, g, execution.DeviceAction{Kind: "screen"})

	answerNext(t, approvals, Deny, nil)
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 5, Intent: "delete the alarm"}); !refused(err, "approval_denied") || len(p.actions()) != 0 {
		t.Fatalf("denied tap ran: %v", err)
	}
	last := (*steps)[len(*steps)-1]
	if last.Risk != RiskDelete || last.Decision != "denied" || last.Target != "Delete" {
		t.Fatalf("denied step recorded as %+v", last)
	}

	p.onPerform = func(p *fakePhone, a Action) { p.screen = clockScreen("") }
	answerNext(t, approvals, Allow, nil)
	out, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 5, Intent: "delete the alarm"})
	if err != nil || len(p.actions()) != 1 || p.actions()[0].Index != 5 || strings.Contains(out, "7:00") {
		t.Fatalf("approved tap: %q %v %+v", out, err, p.actions())
	}
	if last := (*steps)[len(*steps)-1]; last.Decision != "approved" || last.Outcome != "done" {
		t.Fatalf("approved step recorded as %+v", last)
	}
}

func TestGateDoesNothingWhenScreenChangesDuringApproval(t *testing.T) {
	p := &fakePhone{screen: clockScreen("7:00 AM")}
	approvals := NewApprovals(time.Second, nil)
	g, _ := newGate(p, approvals, nil)
	act(t, g, execution.DeviceAction{Kind: "screen"})
	answerNext(t, approvals, Allow, func() {
		p.mu.Lock()
		p.screen = clockScreen("8:00 AM") // the row now says something else
		p.screen.Nodes[5].Bounds[1] = 900
		p.mu.Unlock()
	})
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 5}); !refused(err, "changed while waiting") || len(p.actions()) != 0 {
		t.Fatalf("acted on a changed screen: %v", err)
	}
}

func TestGateApprovalTimeoutDenies(t *testing.T) {
	p := &fakePhone{screen: clockScreen("7:00 AM")}
	g, _ := newGate(p, NewApprovals(5*time.Millisecond, nil), nil)
	act(t, g, execution.DeviceAction{Kind: "screen"})
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 5}); !refused(err, "approval_denied") || len(p.actions()) != 0 {
		t.Fatalf("unanswered approval ran: %v", err)
	}
}

func TestGateStopDuringApprovalEndsTask(t *testing.T) {
	p := &fakePhone{screen: clockScreen("7:00 AM")}
	approvals := NewApprovals(time.Minute, nil)
	var stopped bool
	var mu sync.Mutex
	g, _ := newGate(p, approvals, func() bool { mu.Lock(); defer mu.Unlock(); return stopped })
	act(t, g, execution.DeviceAction{Kind: "screen"})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for approvals.Pending() == nil {
			time.Sleep(time.Millisecond)
		}
		mu.Lock()
		stopped = true
		mu.Unlock()
		cancel()
	}()
	if _, err := g.Act(ctx, execution.DeviceAction{Kind: "tap", Ref: 5}); !errors.Is(err, execution.ErrDeviceStopped) || len(p.actions()) != 0 {
		t.Fatalf("stop: %v", err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "screen"}); !errors.Is(err, execution.ErrDeviceStopped) {
		t.Fatal("actions continue after stop")
	}
}

func TestGateSubmitInMessagingAsksAndUncertainIsReported(t *testing.T) {
	p := &fakePhone{screen: Snapshot{Package: "com.google.android.apps.messaging", Nodes: []Node{
		{Parent: -1, Class: "x"}, {Parent: 0, Class: "android.widget.EditText", ViewID: "compose", Bounds: [4]int{0, 2000, 900, 2100}, Editable: true},
	}}}
	approvals := NewApprovals(5*time.Millisecond, nil)
	var steps []Step
	g := NewGate(p, []string{"com.google.android.apps.messaging"}, own, approvals, func() bool { return false }, func(s Step) { steps = append(steps, s) })
	g.retry = time.Millisecond
	act(t, g, execution.DeviceAction{Kind: "screen"})
	if _, err := act(t, g, execution.DeviceAction{Kind: "type", Ref: 1, Text: "hi", Submit: true}); !refused(err, "approval_denied") {
		t.Fatalf("send without approval: %v", err)
	}
	if _, err := act(t, g, execution.DeviceAction{Kind: "key", Key: "enter"}); !refused(err, "approval_denied") || len(p.actions()) != 0 {
		t.Fatalf("enter without approval: %v", err)
	}
	p.result = Result{Status: "uncertain"}
	out, err := act(t, g, execution.DeviceAction{Kind: "type", Ref: 1, Text: "draft"})
	if err != nil || !strings.Contains(out, "never repeat it blindly") {
		t.Fatalf("uncertain not reported: %q %v", out, err)
	}
}

func TestGateWaitsForWindowAndFailsOnBridgeError(t *testing.T) {
	p := &fakePhone{screen: clockScreen(""), noWindow: 3}
	g, _ := newGate(p, NewApprovals(time.Second, nil), nil)
	if out, err := act(t, g, execution.DeviceAction{Kind: "screen"}); err != nil || !strings.Contains(out, "Add alarm") {
		t.Fatalf("transition not waited out: %v", err)
	}
	p.noWindow = 100
	if _, err := act(t, g, execution.DeviceAction{Kind: "screen"}); !refused(err, "not readable") {
		t.Fatalf("no window: %v", err)
	}
	p.noWindow = 0
	p.result = Result{Status: "failed", Error: "not clickable"}
	act(t, g, execution.DeviceAction{Kind: "screen"})
	if _, err := act(t, g, execution.DeviceAction{Kind: "tap", Ref: 1}); !refused(err, "not clickable") {
		t.Fatalf("failed action: %v", err)
	}
}

// --- store ---

func TestStoreMarksRunningTasksInterruptedOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phone.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(TaskView{ID: "t1", Goal: "set alarm", Apps: []string{clock}, StartedMS: 1}); err != nil {
		t.Fatal(err)
	}
	s.AddStep("t1", Step{Seq: 1, Kind: "screen", Outcome: "done"})
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get("t1")
	if err != nil || got.Status != StatusInterrupted || got.Code != "app_stopped" || len(got.Steps) != 1 || got.EndedMS == 0 {
		t.Fatalf("after reopen: %+v %v", got, err)
	}
	got.Status = StatusDone
	if s.Finish(got); func() bool { g, _ := s.Get("t1"); return g.Status != StatusInterrupted }() {
		t.Fatal("finished task overwritten")
	}
}

// --- runner ---

type scriptedAgent struct {
	steps []execution.DeviceAction
	text  string
}

func (a scriptedAgent) run(ctx context.Context, cfg config.Config, req execution.Request) (execution.Result, error) {
	if cfg.APIKey != "secret-token" || req.Device == nil || req.MaxTurns != execution.MaxDeviceTurns {
		return execution.Result{}, errors.New("bad request")
	}
	for _, s := range a.steps {
		if err := req.CheckAuthority(ctx); err != nil {
			return execution.Result{}, execution.ErrAuthority
		}
		_, err := req.Device.Act(ctx, s)
		if errors.Is(err, execution.ErrDeviceStopped) {
			return execution.Result{}, execution.ErrAuthority
		}
		var r execution.DeviceRefusal
		if err != nil && !errors.As(err, &r) {
			return execution.Result{}, err
		}
	}
	return execution.Result{Text: a.text, Usage: execution.Usage{InputTokens: 100, OutputTokens: 10}, UsageKnown: true}, nil
}

func newRunner(t *testing.T, agent scriptedAgent) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "phone.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewRunner(store, dir, own, agent.run), filepath.Join(dir, "phone.db")
}

func spec(expect ...string) TaskSpec {
	return TaskSpec{Goal: "Set an alarm for 7:00", Apps: []string{clock}, Expect: expect,
		Route: Route{BaseURL: "http://127.0.0.1:1/v1", Model: "m", Token: "secret-token"}}
}

func TestRunnerVerifiesTheFinalScreen(t *testing.T) {
	agent := scriptedAgent{steps: []execution.DeviceAction{{Kind: "screen"}, {Kind: "tap", Ref: 1}}, text: "Alarm set for 7:00."}
	for _, tc := range []struct {
		expect string
		status string
	}{{"7:00", StatusDone}, {"8:00", StatusNeedsAttention}} {
		r, dbPath := newRunner(t, agent)
		p := &fakePhone{screen: clockScreen(""), onPerform: func(p *fakePhone, a Action) { p.screen = clockScreen("7:00 AM") }}
		if _, err := r.Start(spec(tc.expect)); !errors.Is(err, ErrNoBridge) {
			t.Fatal("started without a screen reader")
		}
		r.SetBridge(p)
		if _, err := r.Start(spec(tc.expect)); err != nil {
			t.Fatal(err)
		}
		r.Wait()
		s := r.State()
		if s.Task == nil || s.Task.Status != tc.status || s.Task.Result != "Alarm set for 7:00." || len(s.Task.Steps) != 2 || s.Task.InputTokens != 100 {
			t.Fatalf("expect %s: %+v", tc.expect, s.Task)
		}
		data, _ := os.ReadFile(dbPath)
		wal, _ := os.ReadFile(dbPath + "-wal")
		if strings.Contains(string(data)+string(wal), "secret-token") {
			t.Fatal("model credential stored")
		}
	}
}

func TestRunnerStopDuringApproval(t *testing.T) {
	agent := scriptedAgent{steps: []execution.DeviceAction{{Kind: "screen"}, {Kind: "tap", Ref: 5}, {Kind: "screen"}}}
	r, _ := newRunner(t, agent)
	p := &fakePhone{screen: clockScreen("7:00 AM")}
	r.SetBridge(p)
	var mu sync.Mutex
	var sawPending bool
	r.SetListener(func(s State) {
		if s.Task != nil && s.Task.Pending != nil {
			mu.Lock()
			sawPending = true
			mu.Unlock()
		}
	})
	if _, err := r.Start(spec()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Start(spec()); !errors.Is(err, ErrBusy) {
		t.Fatal("second task started")
	}
	for r.approvals.Pending() == nil {
		time.Sleep(time.Millisecond)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	s := r.State()
	mu.Lock()
	defer mu.Unlock()
	if s.Task.Status != StatusStopped || len(p.actions()) != 0 || !sawPending || s.Task.Pending != nil {
		t.Fatalf("after stop: %+v actions=%d pending-seen=%v", s.Task, len(p.actions()), sawPending)
	}
	if err := r.Stop(); !errors.Is(err, ErrNotRunning) {
		t.Fatal("stop with nothing running")
	}
}

func TestRunnerFlagsUnverifiedTaskWithDeniedStep(t *testing.T) {
	agent := scriptedAgent{steps: []execution.DeviceAction{{Kind: "screen"}, {Kind: "tap", Ref: 5}}, text: "\n\n  Could not delete it.  \n"}
	r, _ := newRunner(t, agent)
	r.SetApprovalTimeout(5 * time.Millisecond)
	r.SetBridge(&fakePhone{screen: clockScreen("7:00 AM")})
	if _, err := r.Start(spec()); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	s := r.State()
	if s.Task.Status != StatusNeedsAttention || s.Task.Code != "steps_refused" || s.Task.Result != "Could not delete it." {
		t.Fatalf("task %+v", s.Task)
	}
}

func TestRunnerShowsRetryWaitsWhileTheyLast(t *testing.T) {
	var during, policy State
	var r *Runner
	run := func(ctx context.Context, cfg config.Config, req execution.Request) (execution.Result, error) {
		if req.Retry == nil || req.Retry.MaxRetries != execution.DefaultRetryPolicy.MaxRetries || req.Retry.OnRetry == nil {
			return execution.Result{}, errors.New("phone tasks must use the default retry policy with a notice")
		}
		policy = r.State()
		req.Retry.OnRetry(execution.RetryNotice{Retry: 1, Max: 3, Wait: time.Minute, Code: "execution_http_rate_limited"})
		during = r.State()
		return execution.Result{Text: "done", Retries: 1}, nil
	}
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "phone.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r = NewRunner(store, dir, own, run)
	r.SetBridge(&fakePhone{screen: clockScreen("")})
	if _, err := r.Start(spec()); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	if policy.Task == nil || policy.Task.Retry != nil {
		t.Fatalf("retry shown before any retry: %+v", policy.Task)
	}
	rv := during.Task.Retry
	if rv == nil || rv.Retry != 1 || rv.Max != 3 || rv.WaitMS != 60000 || rv.Code != "execution_http_rate_limited" || rv.UntilMS <= time.Now().UnixMilli() {
		t.Fatalf("retry view %+v", rv)
	}
	if after := r.State(); after.Task.Retry != nil {
		t.Fatal("retry shown after the task ended")
	}
}

func TestRunnerRejectsInvalidSpecs(t *testing.T) {
	r, _ := newRunner(t, scriptedAgent{})
	r.SetBridge(&fakePhone{screen: clockScreen("")})
	bad := []TaskSpec{
		{Goal: "", Apps: []string{clock}, Route: spec().Route},
		{Goal: "x", Route: spec().Route},
		{Goal: "x", Apps: []string{"not a package"}, Route: spec().Route},
		{Goal: "x", Apps: []string{clock}},
		{Goal: "x", Apps: []string{clock}, Route: Route{BaseURL: "ftp://x", Model: "m", Token: "t"}},
		{Goal: "x", Apps: []string{clock}, Route: spec().Route, MaxMinutes: 99},
		{Goal: "x", Apps: []string{clock}, Route: Route{BaseURL: "http://gateway.example/v1", Model: "m", Token: "t"}},
	}
	for _, s := range bad {
		if _, err := r.Start(s); !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("%+v accepted: %v", s, err)
		}
	}
}
