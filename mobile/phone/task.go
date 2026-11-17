package phone

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/super/execution"
)

// Route is the model endpoint for a task. Token is a credential for that
// endpoint; it is used for the task and never stored or reported.
type Route struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Token   string `json:"token"`
}

// TaskSpec is what the person asked for and allowed.
type TaskSpec struct {
	Goal       string   `json:"goal"`
	Apps       []string `json:"apps"`
	Expect     []string `json:"expect,omitempty"`
	Route      Route    `json:"route"`
	MaxMinutes int      `json:"max_minutes,omitempty"`
	MaxTokens  int      `json:"max_tokens,omitempty"`
}

// State is what the app's screens render.
type State struct {
	ScreenReader bool      `json:"screen_reader"`
	Task         *TaskView `json:"task,omitempty"`
}

var (
	ErrNoBridge    = errors.New("screen reader is off")
	ErrBusy        = errors.New("a task is already running")
	ErrInvalidSpec = errors.New("invalid task")
	ErrNotRunning  = errors.New("no task is running")
)

const (
	defaultMinutes   = 10
	maxMinutes       = 30
	defaultMaxTokens = 300_000
	maxGoalBytes     = 2000
	maxApps          = 10
	maxExpect        = 5
	approvalTimeout  = 60 * time.Second
	outputTokens     = 2048
)

var packageName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$`)

// Executor runs one execution attempt; tests replace it.
type Executor func(ctx context.Context, cfg config.Config, req execution.Request) (execution.Result, error)

func defaultExecutor(ctx context.Context, cfg config.Config, req execution.Request) (execution.Result, error) {
	return execution.New(func(context.Context) (config.Config, error) { return cfg, nil }, nil).Execute(ctx, req)
}

// Runner runs at most one task at a time on this phone.
type Runner struct {
	store     *Store
	own       string
	workspace string
	approvals *Approvals
	execute   Executor

	mu       sync.Mutex
	bridge   Bridge
	listener func(State)
	current  *running
}

type running struct {
	id      string
	cancel  context.CancelFunc
	stopped atomic.Bool
	done    chan struct{}

	mu    sync.Mutex
	retry *RetryView
}

func (run *running) retrying() *RetryView {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.retry == nil || time.Now().UnixMilli() > run.retry.UntilMS {
		return nil
	}
	r := *run.retry
	return &r
}

// NewRunner builds a runner. workspace is the attempt root execution needs
// (canonical, outside Super's state directory); own is the app's package.
func NewRunner(store *Store, workspace, own string, execute Executor) *Runner {
	if execute == nil {
		execute = defaultExecutor
	}
	r := &Runner{store: store, own: own, workspace: workspace, execute: execute}
	r.approvals = NewApprovals(approvalTimeout, r.notify)
	return r
}

// SetApprovalTimeout changes how long a step waits for the person (tests).
func (r *Runner) SetApprovalTimeout(d time.Duration) {
	r.approvals = NewApprovals(d, r.notify)
}

func (r *Runner) SetBridge(b Bridge) {
	r.mu.Lock()
	r.bridge = b
	r.mu.Unlock()
	r.notify()
}

func (r *Runner) SetListener(l func(State)) {
	r.mu.Lock()
	r.listener = l
	r.mu.Unlock()
}

func validSpec(s TaskSpec) error {
	if strings.TrimSpace(s.Goal) == "" || len(s.Goal) > maxGoalBytes || !utf8.ValidString(s.Goal) {
		return fmt.Errorf("%w: goal is required (at most %d bytes)", ErrInvalidSpec, maxGoalBytes)
	}
	if len(s.Apps) == 0 || len(s.Apps) > maxApps {
		return fmt.Errorf("%w: name 1 to %d apps the task may use", ErrInvalidSpec, maxApps)
	}
	for _, a := range s.Apps {
		if !packageName.MatchString(a) {
			return fmt.Errorf("%w: %q is not a package name", ErrInvalidSpec, a)
		}
	}
	if len(s.Expect) > maxExpect {
		return fmt.Errorf("%w: at most %d expected texts", ErrInvalidSpec, maxExpect)
	}
	for _, e := range s.Expect {
		if strings.TrimSpace(e) == "" || len(e) > 200 {
			return fmt.Errorf("%w: expected texts must be 1 to 200 bytes", ErrInvalidSpec)
		}
	}
	u, err := url.Parse(s.Route.BaseURL)
	if err != nil || u.Host == "" || strings.TrimSpace(s.Route.Model) == "" || s.Route.Token == "" {
		return fmt.Errorf("%w: the model route needs base_url, model and key", ErrInvalidSpec)
	}
	if ip := net.ParseIP(u.Hostname()); u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return fmt.Errorf("%w: the model route must use https (plain http only to this phone's loopback)", ErrInvalidSpec)
	}
	if s.MaxMinutes < 0 || s.MaxMinutes > maxMinutes || s.MaxTokens < 0 {
		return fmt.Errorf("%w: limits out of range", ErrInvalidSpec)
	}
	return nil
}

// Start begins a task in the background and returns its id.
func (r *Runner) Start(spec TaskSpec) (string, error) {
	if err := validSpec(spec); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bridge == nil {
		return "", ErrNoBridge
	}
	if r.current != nil {
		return "", ErrBusy
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	id := fmt.Sprintf("task-%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
	view := TaskView{ID: id, Goal: spec.Goal, Apps: spec.Apps, Expect: spec.Expect, Status: StatusRunning, StartedMS: time.Now().UnixMilli()}
	if err := r.store.Create(view); err != nil {
		return "", err
	}
	minutes := spec.MaxMinutes
	if minutes == 0 {
		minutes = defaultMinutes
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	run := &running{id: id, cancel: cancel, done: make(chan struct{})}
	r.current = run
	go r.run(ctx, run, spec, view, r.bridge)
	go r.notify()
	return id, nil
}

// Stop ends the running task: any step waiting for approval is denied and
// no further action reaches the phone.
func (r *Runner) Stop() error {
	r.mu.Lock()
	run := r.current
	r.mu.Unlock()
	if run == nil {
		return ErrNotRunning
	}
	run.stopped.Store(true)
	run.cancel()
	<-run.done
	return nil
}

// Decide answers the pending approval.
func (r *Runner) Decide(id string, d Decision) error { return r.approvals.Decide(id, d) }

// Wait blocks until the running task, if any, has finished (tests).
func (r *Runner) Wait() {
	r.mu.Lock()
	run := r.current
	r.mu.Unlock()
	if run != nil {
		<-run.done
	}
}

func (r *Runner) run(ctx context.Context, run *running, spec TaskSpec, view TaskView, bridge Bridge) {
	defer func() {
		run.cancel()
		r.mu.Lock()
		r.current = nil
		r.mu.Unlock()
		close(run.done)
		r.notify()
	}()
	gate := NewGate(bridge, spec.Apps, r.own, r.approvals, run.stopped.Load, func(st Step) {
		_ = r.store.AddStep(run.id, st)
		r.notify()
	})
	cfg := config.Config{
		Provider: "openai", ProviderProtocol: config.ProviderProtocolOpenAIChatCompletions,
		BaseURL: spec.Route.BaseURL, APIKey: spec.Route.Token,
		SelectedProvider: "phone", Settings: config.Settings{Model: spec.Route.Model},
	}
	maxTokens := spec.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	res, err := r.execute(ctx, cfg, execution.Request{
		RunID: run.id, JobID: "phone", AttemptID: run.id + "-1",
		Workspace: r.workspace, Objective: spec.Goal,
		Instruction:  instruction(spec),
		SystemPrompt: systemPrompt,
		Provider:     "phone", Model: spec.Route.Model,
		MaxTurns: execution.MaxDeviceTurns, MaxOutputTokens: outputTokens,
		CheckAuthority: func(ctx context.Context) error {
			if run.stopped.Load() {
				return execution.ErrDeviceStopped
			}
			return ctx.Err()
		},
		Budget: &tokenBudget{limit: maxTokens},
		Device: gate,
		Retry:  r.retryPolicy(run),
	})
	view.InputTokens, view.OutputTokens = res.Usage.InputTokens, res.Usage.OutputTokens
	view.EndedMS = time.Now().UnixMilli()
	switch {
	case run.stopped.Load():
		view.Status, view.Code = StatusStopped, "stopped_by_person"
	case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		view.Status, view.Code = StatusFailed, "time_limit"
	case err != nil:
		view.Status, view.Code = StatusFailed, execution.FailureCode(err)
	default:
		view.Result = strings.TrimSpace(res.Text)
		view.Status, view.Verified, view.Code = r.verify(spec, bridge)
		// Without expected texts nothing proves the goal was reached; a
		// refused or denied step makes that worth the person's look.
		if view.Status == StatusDone && view.Verified == nil && r.hadSetback(run.id) {
			view.Status, view.Code = StatusNeedsAttention, "steps_refused"
		}
	}
	_ = r.store.Finish(view)
}

// retryPolicy is execution's default policy, with each wait shown to the
// person so a paused task does not look stuck.
func (r *Runner) retryPolicy(run *running) *execution.RetryPolicy {
	p := execution.DefaultRetryPolicy
	p.OnRetry = func(n execution.RetryNotice) {
		run.mu.Lock()
		run.retry = &RetryView{Retry: n.Retry, Max: n.Max, WaitMS: n.Wait.Milliseconds(),
			UntilMS: time.Now().Add(n.Wait).UnixMilli(), Code: string(n.Code)}
		run.mu.Unlock()
		r.notify()
	}
	return &p
}

func (r *Runner) hadSetback(id string) bool {
	t, err := r.store.Get(id)
	if err != nil {
		return true
	}
	for _, st := range t.Steps {
		switch st.Outcome {
		case "denied", "refused", "failed", "uncertain":
			return true
		}
	}
	return false
}

// verify re-reads the screen, read-only, and checks the expected texts. The
// agent's own report never counts as success.
func (r *Runner) verify(spec TaskSpec, bridge Bridge) (string, *bool, string) {
	if len(spec.Expect) == 0 {
		return StatusDone, nil, "not_verified"
	}
	var screen Snapshot
	var err error
	for i := 0; i < 8; i++ {
		if screen, err = bridge.Snapshot(); !errors.Is(err, ErrNoWindow) {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	passed := err == nil
	for _, e := range spec.Expect {
		passed = passed && screen.Contains(e)
	}
	if !passed {
		return StatusNeedsAttention, &passed, "verification_failed"
	}
	return StatusDone, &passed, ""
}

// State reports the running task, or the latest one.
func (r *Runner) State() State {
	r.mu.Lock()
	s := State{ScreenReader: r.bridge != nil}
	run := r.current
	r.mu.Unlock()
	var t TaskView
	var err error
	if run != nil {
		t, err = r.store.Get(run.id)
	} else if recent, rerr := r.store.Recent(1); rerr == nil && len(recent) == 1 {
		t, err = r.store.Get(recent[0].ID)
	} else {
		return s
	}
	if err != nil {
		return s
	}
	if len(t.Steps) > 30 {
		t.Steps = t.Steps[len(t.Steps)-30:]
	}
	if run != nil {
		t.Pending = r.approvals.Pending()
		t.Retry = run.retrying()
	}
	s.Task = &t
	return s
}

func (r *Runner) notify() {
	r.mu.Lock()
	l := r.listener
	r.mu.Unlock()
	if l != nil {
		l(r.State())
	}
}

const systemPrompt = `You operate an Android phone for a person through the super_* tools.
Rules:
- Start with super_screen. Every action returns the new screen; act only on refs from the latest screen.
- Use only the apps this task allows. Go home or back if you end up elsewhere.
- Some steps (sending, paying, deleting, installing, calling, posting) wait for the person's approval. If a step is denied, do not repeat it; choose another way or finish and explain.
- Never type passwords or verification codes; finish and ask the person to do it.
- If a result says uncertain, read the screen before anything else and never repeat the action blindly.
- When the goal is reached, or cannot be reached, stop calling tools and reply with one short paragraph saying what you did and what the screen now shows.`

func instruction(spec TaskSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Apps this task may use: %s.\n", strings.Join(spec.Apps, ", "))
	if len(spec.Expect) > 0 {
		fmt.Fprintf(&b, "When you finish, leave the phone on a screen that shows: %s.\n", strings.Join(spec.Expect, "; "))
	}
	return b.String()
}

// tokenBudget refuses a model request that could take the task over limit.
type tokenBudget struct {
	mu       sync.Mutex
	limit    int
	reserved map[string]int
	spent    int
}

func (b *tokenBudget) Reserve(ctx context.Context, id string, input, output int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reserved == nil {
		b.reserved = map[string]int{}
	}
	if _, dup := b.reserved[id]; dup {
		return errors.New("reservation id already held")
	}
	held := 0
	for _, n := range b.reserved {
		held += n
	}
	if b.spent+held+input+output > b.limit {
		return errors.New("token budget exhausted")
	}
	b.reserved[id] = input + output
	return nil
}

func (b *tokenBudget) Settle(_ context.Context, id string, u execution.Usage, known bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	held, ok := b.reserved[id]
	if !ok {
		return errors.New("settling an unknown reservation")
	}
	delete(b.reserved, id)
	if known {
		b.spent += u.InputTokens + u.OutputTokens
	} else {
		b.spent += held
	}
	return nil
}
