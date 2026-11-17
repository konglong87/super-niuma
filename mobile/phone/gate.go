package phone

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/konglong87/super-niuma/internal/super/execution"
)

// Bridge is the phone as the app's accessibility service exposes it.
type Bridge interface {
	// Snapshot reads the active window; ErrNoWindow when none is readable.
	// Element indexes in a later Perform refer to the latest snapshot.
	Snapshot() (Snapshot, error)
	// Perform runs one action on the latest snapshot and waits for the
	// screen to settle.
	Perform(Action) (Result, error)
}

// Action is what the bridge performs. Index is an element of the latest
// snapshot, -1 for none.
type Action struct {
	Kind      string `json:"kind"`
	Index     int    `json:"index"`
	Text      string `json:"text,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
	Direction string `json:"direction,omitempty"`
	Key       string `json:"key,omitempty"`
	App       string `json:"app,omitempty"`
}

// Result is the bridge's account of an action. Uncertain means it may have
// happened (for example a tap whose effect could not be confirmed).
type Result struct {
	Status string `json:"status"` // done | failed | uncertain
	Error  string `json:"error,omitempty"`
}

// Step is one recorded action.
type Step struct {
	Seq      int    `json:"seq"`
	Kind     string `json:"kind"`
	Target   string `json:"target,omitempty"`
	App      string `json:"app,omitempty"`
	Intent   string `json:"intent,omitempty"`
	Risk     Risk   `json:"risk,omitempty"`
	Decision string `json:"decision,omitempty"` // approved | denied
	Outcome  string `json:"outcome"`            // done | failed | uncertain | refused | stale | denied
	Reason   string `json:"reason,omitempty"`
	AtMS     int64  `json:"at_ms"`
}

// Gate is the execution.Device for one task. It is the only path from the
// agent to the phone: it checks the task's apps, refuses credentials, holds
// risky steps for the person and confirms the target before acting.
type Gate struct {
	bridge    Bridge
	apps      map[string]bool
	appList   string
	own       string
	approvals *Approvals
	stopped   func() bool
	record    func(Step)
	retry     time.Duration

	mu   sync.Mutex
	last *Snapshot
	seq  int
}

// NewGate builds the gate for a task allowed to use apps. own is the app's
// package, whose screens the agent never sees or touches.
func NewGate(bridge Bridge, apps []string, own string, approvals *Approvals, stopped func() bool, record func(Step)) *Gate {
	allowed := map[string]bool{}
	for _, a := range apps {
		if a = strings.TrimSpace(a); a != "" {
			allowed[a] = true
		}
	}
	list := make([]string, 0, len(allowed))
	for a := range allowed {
		list = append(list, a)
	}
	sort.Strings(list)
	if record == nil {
		record = func(Step) {}
	}
	return &Gate{bridge: bridge, apps: allowed, appList: strings.Join(list, ", "), own: own, approvals: approvals,
		stopped: stopped, record: record, retry: 400 * time.Millisecond}
}

var _ execution.Device = (*Gate)(nil)

func refusal(reason string) error { return execution.DeviceRefusal{Reason: reason} }

// Act runs one model action through scope, credential, risk and approval
// checks, and returns the screen after it.
func (g *Gate) Act(ctx context.Context, a execution.DeviceAction) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped() {
		return "", execution.ErrDeviceStopped
	}
	g.seq++
	step := Step{Seq: g.seq, Kind: a.Kind, Intent: a.Intent, AtMS: time.Now().UnixMilli()}
	refuse := func(outcome, reason string) (string, error) {
		step.Outcome, step.Reason = outcome, reason
		g.record(step)
		return "", refusal(reason)
	}
	cur, err := g.snapshot(ctx)
	if errors.Is(err, ErrNoWindow) {
		return refuse("refused", "screen not readable yet; call super_screen again")
	}
	if err != nil {
		return "", err
	}
	step.App = cur.Package
	ownScreen := cur.Package == g.own

	act := Action{Kind: a.Kind, Index: -1, Text: a.Text, Submit: a.Submit, Direction: a.Direction, Key: a.Key, App: a.App}
	risk := RiskNone
	switch a.Kind {
	case "screen":
		g.last = &cur
		step.Outcome = "done"
		g.record(step)
		return g.view(cur), nil
	case "launch":
		step.Target = a.App
		if !g.apps[a.App] {
			return refuse("refused", fmt.Sprintf("scope_denied: %s is not one of this task's apps (%s)", a.App, g.appList))
		}
	case "key":
		step.Target = a.Key
		if a.Key == "enter" {
			if ownScreen || !g.inScope(cur) {
				return refuse("refused", g.scopeReason(cur))
			}
			risk = SubmitRisk(cur.Package)
		}
	case "tap", "type", "swipe":
		if ownScreen || !g.inScope(cur) {
			return refuse("refused", g.scopeReason(cur))
		}
		if a.Kind != "swipe" || a.Ref >= 0 {
			if g.last == nil {
				return refuse("refused", "read the screen with super_screen first")
			}
			idx := cur.Find(*g.last, a.Ref)
			if idx < 0 {
				return refuse("stale", fmt.Sprintf("stale: element [%d] is not on the screen any more; read the screen again", a.Ref))
			}
			act.Index = idx
			n := cur.Nodes[idx]
			step.Target = cur.Label(idx)
			switch {
			case a.Kind == "type" && n.Password:
				return refuse("refused", "credentials: the agent never types into password or code fields; finish and ask the person to do it")
			case a.Kind == "type" && !n.Editable:
				return refuse("refused", fmt.Sprintf("element [%d] is not editable", a.Ref))
			case a.Kind == "swipe" && !n.Scrolls:
				return refuse("refused", fmt.Sprintf("element [%d] does not scroll", a.Ref))
			case a.Kind == "tap":
				risk = LabelRisk(step.Target)
			case a.Kind == "type" && a.Submit:
				risk = SubmitRisk(cur.Package)
			}
		}
	default:
		return refuse("refused", "unknown action")
	}

	if risk != RiskNone {
		step.Risk = risk
		decision, err := g.approvals.Ask(ctx, Approval{Risk: risk, What: describe(a, step.Target), App: cur.Package, Intent: a.Intent})
		if g.stopped() {
			return "", execution.ErrDeviceStopped
		}
		if err != nil {
			return "", err
		}
		if decision != Allow {
			step.Decision = "denied"
			return refuse("denied", "approval_denied: the person did not allow this step; do not repeat it; choose another way or finish and explain")
		}
		step.Decision = "approved"
		// The person approved what they saw; act only if it is still there.
		again, err := g.snapshot(ctx)
		if err != nil && !errors.Is(err, ErrNoWindow) {
			return "", err
		}
		if act.Index >= 0 {
			idx := -1
			if err == nil {
				idx = again.Find(cur, act.Index)
			}
			if idx < 0 {
				return refuse("stale", "stale: the screen changed while waiting for approval; nothing was done; read the screen again")
			}
			act.Index = idx
		} else if err != nil || again.Package != cur.Package {
			return refuse("stale", "stale: the screen changed while waiting for approval; nothing was done; read the screen again")
		}
	}

	res, err := g.bridge.Perform(act)
	if err != nil {
		return "", err
	}
	switch res.Status {
	case "done", "uncertain":
	case "failed":
		return refuse("failed", "action failed: "+res.Error)
	default:
		return "", fmt.Errorf("bridge reported status %q", res.Status)
	}
	step.Outcome = res.Status
	after, err := g.snapshot(ctx)
	g.record(step)
	if errors.Is(err, ErrNoWindow) {
		g.last = nil
		return "result: " + res.Status + "\nscreen not readable yet; call super_screen", nil
	}
	if err != nil {
		return "", err
	}
	g.last = &after
	head := "result: " + res.Status + "\n"
	if res.Status == "uncertain" {
		head = "result: uncertain — the action may have happened; check the screen and never repeat it blindly\n"
	}
	return head + g.view(after), nil
}

func (g *Gate) inScope(s Snapshot) bool {
	return g.apps[s.Package] || (s.Home != "" && s.Package == s.Home)
}

func (g *Gate) scopeReason(s Snapshot) string {
	if s.Package == g.own {
		return "the foreground app is Super Niuma itself; launch one of this task's apps (" + g.appList + ")"
	}
	return fmt.Sprintf("scope_denied: %s is not one of this task's apps (%s); press back or home, or launch an allowed app", s.Package, g.appList)
}

func (g *Gate) view(s Snapshot) string {
	if s.Package == g.own {
		return "app: Super Niuma (its own screen is hidden)\nLaunch one of this task's apps: " + g.appList + "\n"
	}
	text := s.Render()
	if len(text) > execution.MaxDeviceResultBytes-256 {
		text = text[:execution.MaxDeviceResultBytes-256]
		text = text[:strings.LastIndexByte(text, '\n')+1] + "… screen truncated; scroll or narrow down\n"
	}
	return text
}

// snapshot retries briefly while a window is in transition.
func (g *Gate) snapshot(ctx context.Context) (Snapshot, error) {
	var err error
	for i := 0; i < 8; i++ {
		var s Snapshot
		s, err = g.bridge.Snapshot()
		if err == nil {
			return s, nil
		}
		if !errors.Is(err, ErrNoWindow) {
			return Snapshot{}, err
		}
		select {
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		case <-time.After(g.retry):
		}
	}
	return Snapshot{}, err
}

func describe(a execution.DeviceAction, target string) string {
	switch a.Kind {
	case "tap":
		return fmt.Sprintf("tap %q", target)
	case "type":
		return fmt.Sprintf("type %q into %q and press enter", truncate(a.Text, 60), target)
	case "key":
		return "press " + a.Key
	}
	return a.Kind
}
