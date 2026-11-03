package computeruse

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionAlwaysRequiresApproval(t *testing.T) {
	s, owner, now := newTestSession(t, true)
	if err := s.SetObservation(readyObservation(s.ID(), now)); err == nil {
		t.Fatal("unapproved observation accepted")
	}
	if err := s.Approve(SessionOwner{TenantID: owner.TenantID, UserID: owner.UserID, SessionID: 99}); err == nil {
		t.Fatal("cross-conversation approval")
	}
	if s.Approved() {
		t.Fatal("approval was implicit")
	}
}
func TestStopWinsLateUnknownReceipt(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	_ = s.SetObservation(readyObservation(s.ID(), now))
	a := Action{ID: "a", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionType, Text: "test"}
	if err := s.BeginAction(a); err != nil {
		t.Fatal(err)
	}
	_ = s.Stop(owner)
	if err := s.RecordReceipt(ActionReceipt{ActionID: a.ID, SessionID: s.ID(), Outcome: OutcomeUnknown}); err != nil {
		t.Fatal(err)
	}
	if s.State() != SessionStopped || s.Approved() {
		t.Fatal("late receipt resurrected session")
	}
	o := readyObservation(s.ID(), now)
	o.ID = "new"
	if s.SetObservation(o) == nil {
		t.Fatal("stopped session accepted observation")
	}
}
func TestUnknownCannotReplayAfterFreshObservation(t *testing.T) {
	s, _, now := newTestSession(t, false)
	_ = s.SetObservation(readyObservation(s.ID(), now))
	a := Action{ID: "a", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionType, Text: "test"}
	_ = s.BeginAction(a)
	_ = s.RecordReceipt(ActionReceipt{ActionID: a.ID, SessionID: s.ID(), Outcome: OutcomeUnknown})
	if s.SetObservation(readyObservation(s.ID(), now)) == nil {
		t.Fatal("old observation reused")
	}
	o := readyObservation(s.ID(), now)
	o.ID = "fresh"
	if err := s.SetObservation(o); err == nil {
		t.Fatal("unknown input regained authority")
	}
	a.ObservationID = o.ID
	if s.BeginAction(a) == nil {
		t.Fatal("unknown action replayed")
	}
	a.ID = "new"
	if err := s.BeginAction(a); err == nil {
		t.Fatal("unknown input replayed under a different ID")
	}
}
func TestSessionSnapshotDoesNotMutateAuthority(t *testing.T) {
	s, _, now := newTestSession(t, false)
	_ = s.SetObservation(readyObservation(s.ID(), now))
	caps := s.Capabilities()
	caps.Actions[0] = ActionStop
	obs, _ := s.CurrentObservation()
	obs.Capabilities.Actions[0] = ActionStop
	if !s.Capabilities().Supports(ActionClick) {
		t.Fatal("snapshot mutated capabilities")
	}
}
func TestActionBoundsAndUnknownKind(t *testing.T) {
	now := time.Now()
	o := readyObservation("s", now)
	for _, a := range []Action{
		{Kind: ActionKind("script")}, {Kind: ActionWait, DurationMS: -1}, {Kind: ActionDrag, StartPoint: &Point{X: 1, Y: 1}}, {Kind: ActionWait, DurationMS: MaxActionDurationMS + 1},
		{Kind: ActionType, Text: "x", DisplayID: "other"}, {Kind: ActionHotkey, Keys: []string{""}}, {Kind: ActionScroll, DeltaY: MaxScrollDelta + 1},
	} {
		a.ID = "a"
		a.SessionID = "s"
		a.ObservationID = o.ID
		if a.Validate(now, o) == nil {
			t.Fatalf("invalid action accepted %s", a.Kind)
		}
	}
}
func TestActionValidateAcceptsExplicitLeftButtonForClick(t *testing.T) {
	now := time.Now()
	o := readyObservation("s", now)
	a := Action{ID: "a", SessionID: "s", ObservationID: o.ID, Kind: ActionClick, Button: string(MouseButtonLeft), Point: &Point{X: 10, Y: 10}}
	if err := a.Validate(now, o); err != nil {
		t.Fatalf("explicit left click button rejected: %v", err)
	}
}

func TestEventCannotSerializeActionText(t *testing.T) {
	a := Action{Kind: ActionType, Text: "private"}
	b, err := json.Marshal(Event{Kind: EventActionRequested, ActionSummary: a.RedactedSummary()})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(b, &fields)
	if _, ok := fields["action"]; ok {
		t.Fatal("event carries raw action")
	}
}

type blockingBackend struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *blockingBackend) Capabilities(context.Context) (Capabilities, error) {
	return readyCapabilities(), nil
}
func (b *blockingBackend) Observe(context.Context, ObserveRequest) (Observation, error) {
	return Observation{}, errors.New("capture error")
}
func (b *blockingBackend) Execute(ctx context.Context, a Action) (ActionReceipt, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	close(b.entered)
	select {
	case <-ctx.Done():
	case <-b.release:
	}
	return ActionReceipt{}, errors.New("sensitive helper error")
}
func (*blockingBackend) Pause(context.Context) error  { return nil }
func (*blockingBackend) Resume(context.Context) error { return nil }
func (*blockingBackend) Stop(context.Context) error   { return nil }
func (*blockingBackend) Close(context.Context) error  { return nil }
func TestControllerStopCancelsBlockedActionAndRejectsQueuedAction(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	_ = s.SetObservation(readyObservation(s.ID(), now))
	b := &blockingBackend{entered: make(chan struct{}), release: make(chan struct{})}
	c, _ := NewController(s, b)
	a := Action{ID: "a", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionType, Text: "test"}
	done := make(chan ActionReceipt, 1)
	go func() { r, _ := c.Execute(context.Background(), owner, a); done <- r }()
	<-b.entered
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop(context.Background(), owner, s.ID()) }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked by Execute")
	}
	r := <-done
	if r.Outcome != OutcomeUnknown || r.ErrorMessage != "" {
		t.Fatalf("unsafe receipt %+v", r)
	}
	a.ID = "b"
	if _, err := c.Execute(context.Background(), owner, a); err == nil {
		t.Fatal("queued input accepted after Stop")
	}
	if b.calls != 1 || s.State() != SessionStopped {
		t.Fatal("Stop lost priority")
	}
}
func TestCaptureFailurePauses(t *testing.T) {
	s, owner, _ := newTestSession(t, false)
	c, _ := NewController(s, &blockingBackend{})
	if _, err := c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err == nil {
		t.Fatal("expected failure")
	}
	if s.State() != SessionPaused {
		t.Fatal("capture failure did not pause")
	}
}

// interruptibleBackend models a helper whose action RPC must finish normally
// after a control acknowledgement. Cancelling that RPC poisons the helper.
type interruptibleBackend struct {
	*FakeBackend
	entered     chan struct{}
	interrupted chan struct{}
	finish      chan struct{}
	returned    chan struct{}
	pauseSeen   chan struct{}
	stopSeen    chan struct{}
	resumeSeen  chan struct{}
	resumeReply chan struct{}
	mu          sync.Mutex
	actionCtx   context.Context
	unavailable bool
	stopped     bool
	calls       int
	pauseHook   func(context.Context) error
	stopHook    func(context.Context) error
}

func newInterruptibleBackend(s *ComputerSession, now time.Time) *interruptibleBackend {
	observation := readyObservation(s.ID(), now)
	observation.ID = "fresh-after-pause"
	return &interruptibleBackend{
		FakeBackend: &FakeBackend{ObservationValue: observation},
		entered:     make(chan struct{}), interrupted: make(chan struct{}), finish: make(chan struct{}),
		returned: make(chan struct{}), pauseSeen: make(chan struct{}), stopSeen: make(chan struct{}),
		resumeSeen: make(chan struct{}),
	}
}

func (b *interruptibleBackend) Execute(ctx context.Context, a Action) (ActionReceipt, error) {
	b.mu.Lock()
	b.actionCtx = ctx
	b.calls++
	b.mu.Unlock()
	close(b.entered)
	defer close(b.returned)
	select {
	case <-b.pauseSeen:
	case <-b.stopSeen:
	case <-ctx.Done():
	}
	close(b.interrupted)
	select {
	case <-b.finish:
	case <-ctx.Done():
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		b.unavailable = true
		return ActionReceipt{}, ctx.Err()
	}
	return ActionReceipt{ActionID: a.ID, SessionID: a.SessionID, Outcome: OutcomeRejected}, nil
}

func (b *interruptibleBackend) controlAvailable() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unavailable {
		return errors.New("helper unavailable")
	}
	select {
	case <-b.returned: // Normal controller cleanup cancels an already-finished context.
		return nil
	default:
	}
	if b.actionCtx != nil && b.actionCtx.Err() != nil {
		return errors.New("helper unavailable")
	}
	return nil
}

func (b *interruptibleBackend) Pause(ctx context.Context) error {
	if err := b.controlAvailable(); err != nil {
		return err
	}
	close(b.pauseSeen)
	if b.pauseHook != nil {
		return b.pauseHook(ctx)
	}
	return nil
}

func (b *interruptibleBackend) Resume(ctx context.Context) error {
	b.mu.Lock()
	unavailable := b.unavailable
	b.mu.Unlock()
	if unavailable {
		return errors.New("helper unavailable")
	}
	close(b.resumeSeen)
	if b.resumeReply != nil {
		select {
		case <-b.resumeReply:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (b *interruptibleBackend) Stop(ctx context.Context) error {
	b.mu.Lock()
	alreadyStopped := b.stopped
	b.stopped = true
	b.mu.Unlock()
	if alreadyStopped { // Native Stop also acknowledges repeat requests locally.
		return nil
	}
	if err := b.controlAvailable(); err != nil {
		return err
	}
	close(b.stopSeen)
	if b.stopHook != nil {
		return b.stopHook(ctx)
	}
	return nil
}

const controllerTestTimeout = 3 * time.Second

func awaitControl[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(controllerTestTimeout):
		t.Fatal("controller operation did not finish within test deadline")
		var zero T
		return zero
	}
}

func startWaitingController(t *testing.T) (*Controller, *interruptibleBackend, Action, <-chan ActionReceipt) {
	t.Helper()
	s, owner, now := newTestSession(t, false)
	if err := s.SetObservation(readyObservation(s.ID(), now)); err != nil {
		t.Fatal(err)
	}
	b := newInterruptibleBackend(s, now)
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	a := Action{ID: "waiting", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionWait, DurationMS: 5000}
	done := make(chan ActionReceipt, 1)
	go func() { receipt, _ := c.Execute(context.Background(), owner, a); done <- receipt }()
	awaitControl(t, b.entered)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c, b, a, done
}

func TestControllerCooperativePauseDrainsReceiptAndResumesWithoutReplay(t *testing.T) {
	c, b, action, done := startWaitingController(t)
	s, owner := c.Session(), c.Session().Owner()
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(context.Background(), owner, s.ID()) }()
	awaitControl(t, b.interrupted)
	if s.State() != SessionPaused {
		t.Fatal("session was not revoked before backend control")
	}
	select {
	case err := <-paused:
		t.Fatalf("Pause returned before the interrupted action drained: %v", err)
	default:
	}
	// Hold receipt processing, not just the backend RPC. Acknowledgement alone
	// must not let Pause return or Resume restore authority ahead of this work.
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		close(b.finish)
		awaitControl(t, b.returned)
		select {
		case err := <-paused:
			t.Fatalf("Pause returned before receipt processing: %v", err)
		default:
		}
	}()
	if err := awaitControl(t, paused); err != nil {
		t.Fatal(err)
	}
	if receipt, ok := s.LastReceipt(); !ok || receipt.Outcome != OutcomeRejected {
		t.Fatalf("Pause did not retain the cooperative rejection: %+v, %v", receipt, ok)
	}
	if receipt := awaitControl(t, done); receipt.Outcome != OutcomeRejected {
		t.Fatalf("cancellation poisoned healthy action: %+v", receipt)
	}
	if err := c.Resume(context.Background(), owner, s.ID()); err != nil {
		t.Fatalf("healthy helper could not resume: %v", err)
	}
	if s.State() != SessionNeedsObservation {
		t.Fatal("resume did not require a fresh observation")
	}
	observation, err := c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()})
	if err != nil || s.State() != SessionReady {
		t.Fatalf("fresh observation after resume failed: %v", err)
	}
	action.ObservationID = observation.ID
	if _, err := c.Execute(context.Background(), owner, action); err == nil {
		t.Fatal("consumed action was replayed")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.unavailable || b.calls != 1 {
		t.Fatalf("helper poisoned or action replayed: unavailable=%v calls=%d", b.unavailable, b.calls)
	}
}

func TestControllerStopSendsControlBeforeCancellation(t *testing.T) {
	c, b, _, done := startWaitingController(t)
	if err := c.Stop(context.Background(), c.Session().Owner(), c.Session().ID()); err != nil {
		t.Fatalf("Stop cancelled helper before control acknowledgement: %v", err)
	}
	awaitControl(t, b.stopSeen)
	if receipt := awaitControl(t, done); receipt.Outcome != OutcomeUnknown {
		t.Fatalf("cancelled outstanding action needs an unknown receipt: %+v", receipt)
	}
	if c.Session().State() != SessionStopped || c.Session().Approved() {
		t.Fatal("Stop did not permanently revoke authority")
	}
}

func TestControllerCloseSendsStopBeforeTransportCancellation(t *testing.T) {
	c, b, _, done := startWaitingController(t)
	if err := c.Close(context.Background()); err != nil {
		t.Fatalf("Close canceled helper before cooperative Stop: %v", err)
	}
	awaitControl(t, b.stopSeen)
	awaitControl(t, done)
	if c.Session().State() != SessionStopped || c.Session().Approved() {
		t.Fatal("Close did not revoke authority")
	}
}

func TestControllerPauseFallbackIsBoundedAndFailsClosed(t *testing.T) {
	pauseFailure := errors.New("pause rejected")
	for _, test := range []struct {
		name    string
		timeout time.Duration
		hook    func(context.Context) error
		want    error
	}{
		{name: "acknowledged but action does not drain", want: context.DeadlineExceeded},
		{name: "short caller deadline while draining", timeout: 40 * time.Millisecond, want: context.DeadlineExceeded},
		{name: "pause error", hook: func(context.Context) error { return pauseFailure }, want: pauseFailure},
		{name: "backend control deadline", timeout: 40 * time.Millisecond, hook: func(ctx context.Context) error {
			<-ctx.Done()
			return errors.New("backend hid context error")
		}, want: context.DeadlineExceeded},
		{name: "already cancelled", timeout: -1, want: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, b, _, done := startWaitingController(t)
			b.pauseHook = test.hook
			ctx := context.Background()
			if test.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.timeout)
				defer cancel()
			} else if test.timeout < 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			started := time.Now()
			err := c.Pause(ctx, c.Session().Owner(), c.Session().ID())
			if !errors.Is(err, test.want) {
				t.Fatalf("Pause error = %v, want %v", err, test.want)
			}
			limit := controllerControlGrace + 500*time.Millisecond
			if test.timeout != 0 {
				limit = 500 * time.Millisecond
			}
			if elapsed := time.Since(started); elapsed > limit {
				t.Fatalf("fallback took %s, limit %s", elapsed, limit)
			}
			if receipt := awaitControl(t, done); receipt.Outcome != OutcomeUnknown {
				t.Fatalf("cancelled RPC lost its unknown receipt: %+v", receipt)
			}
			if c.Session().State() != SessionPaused {
				t.Fatal("fallback restored input authority")
			}
			if err := c.Resume(context.Background(), c.Session().Owner(), c.Session().ID()); err == nil {
				t.Fatal("unavailable helper was recreated or resumed")
			}
		})
	}
}

func TestControllerStopBypassesPauseAndRejectsActuallyQueuedInput(t *testing.T) {
	c, b, action, done := startWaitingController(t)
	s, owner := c.Session(), c.Session().Owner()
	reply := make(chan struct{})
	var releaseOnce sync.Once
	releasePause := func() { releaseOnce.Do(func() { close(reply) }) }
	defer releasePause()
	b.pauseHook = func(ctx context.Context) error {
		select {
		case <-reply:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(context.Background(), owner, s.ID()) }()
	awaitControl(t, b.pauseSeen)
	queued := make(chan error, 1)
	action.ID = "queued-after-wait"
	go func() { _, err := c.Execute(context.Background(), owner, action); queued <- err }()
	stopped := make(chan error, 1)
	go func() { stopped <- c.Stop(context.Background(), owner, s.ID()) }()
	if err := awaitControl(t, stopped); err != nil {
		t.Fatalf("Stop lost priority to pending Pause: %v", err)
	}
	select {
	case err := <-paused:
		t.Fatalf("Pause completed before its blocked acknowledgement: %v", err)
	default:
	}
	awaitControl(t, done)
	if err := awaitControl(t, queued); err == nil {
		t.Fatal("queued input dispatched after revocation")
	}
	releasePause()
	awaitControl(t, paused)
	if err := c.Resume(context.Background(), owner, s.ID()); err == nil {
		t.Fatal("stopped session resumed")
	}
	if s.State() != SessionStopped || s.Approved() {
		t.Fatal("control overlap resurrected the stopped session")
	}
}

func TestControllerLateResumeCannotUndoTimedOutPause(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	b := newInterruptibleBackend(s, now)
	b.resumeReply = make(chan struct{})
	c, _ := NewController(s, b)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	resumed := make(chan error, 1)
	go func() { resumed <- c.Resume(context.Background(), owner, s.ID()) }()
	awaitControl(t, b.resumeSeen)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := c.Pause(ctx, owner, s.ID()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued pause error = %v, want deadline", err)
	}
	close(b.resumeReply)
	if err := awaitControl(t, resumed); err == nil {
		t.Fatal("late Resume acknowledgement undid a timed-out pause")
	}
	if s.State() != SessionPaused {
		t.Fatal("timed-out pause did not remain fail-closed")
	}
}

func TestControllerResumeWaitsForPauseAndReceiptDrain(t *testing.T) {
	c, b, _, done := startWaitingController(t)
	s, owner := c.Session(), c.Session().Owner()
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(context.Background(), owner, s.ID()) }()
	awaitControl(t, b.interrupted)
	resumed := make(chan error, 1)
	go func() { resumed <- c.Resume(context.Background(), owner, s.ID()) }()
	select {
	case <-b.resumeSeen:
		t.Fatal("Resume overtook pending Pause/action receipt")
	default:
	}
	close(b.finish)
	if err := awaitControl(t, paused); err != nil {
		t.Fatal(err)
	}
	if err := awaitControl(t, resumed); err != nil {
		t.Fatal(err)
	}
	awaitControl(t, done)
	if s.State() != SessionNeedsObservation {
		t.Fatal("late receipt processing undid Resume")
	}
}

func TestControllerStopDeadlineStillCancelsAndRevokes(t *testing.T) {
	c, b, _, done := startWaitingController(t)
	b.stopHook = func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("backend hid deadline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if err := c.Stop(ctx, c.Session().Owner(), c.Session().ID()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want caller deadline", err)
	}
	if receipt := awaitControl(t, done); receipt.Outcome != OutcomeUnknown {
		t.Fatalf("Stop timeout did not cancel action: %+v", receipt)
	}
	if c.Session().State() != SessionStopped || c.Session().Approved() {
		t.Fatal("Stop timeout restored authority")
	}
}

func TestControllerStopBypassesResumeAndRejectsLateAcknowledgement(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	b := newInterruptibleBackend(s, now)
	b.resumeReply = make(chan struct{})
	c, _ := NewController(s, b)
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	resumed := make(chan error, 1)
	go func() { resumed <- c.Resume(context.Background(), owner, s.ID()) }()
	awaitControl(t, b.resumeSeen)
	if err := c.Stop(context.Background(), owner, s.ID()); err != nil {
		t.Fatal(err)
	}
	close(b.resumeReply)
	if err := awaitControl(t, resumed); err == nil {
		t.Fatal("late Resume acknowledgement undid Stop")
	}
	if s.State() != SessionStopped || s.Approved() {
		t.Fatal("Stop lost priority to Resume")
	}
}

type cancellationIgnoringBackend struct {
	*FakeBackend
	entered chan context.Context
	release chan struct{}
}

func (b *cancellationIgnoringBackend) Execute(ctx context.Context, _ Action) (ActionReceipt, error) {
	b.entered <- ctx
	<-b.release
	return ActionReceipt{}, errors.New("uncooperative action ended")
}

func TestControllerPauseDoesNotWaitForeverForCancellationIgnoringAction(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	if err := s.SetObservation(readyObservation(s.ID(), now)); err != nil {
		t.Fatal(err)
	}
	b := &cancellationIgnoringBackend{FakeBackend: &FakeBackend{}, entered: make(chan context.Context, 1), release: make(chan struct{})}
	c, _ := NewController(s, b)
	done := make(chan error, 1)
	go func() {
		_, err := c.Execute(context.Background(), owner, Action{ID: "stuck", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionWait, DurationMS: 5000})
		done <- err
	}()
	t.Cleanup(func() {
		close(b.release)
		awaitControl(t, done)
		_ = c.Close(context.Background())
	})
	actionCtx := awaitControl(t, b.entered)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	paused := make(chan error, 1)
	go func() { paused <- c.Pause(ctx, owner, s.ID()) }()
	if err := awaitControl(t, paused); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Pause must time out independently of action completion: %v", err)
	}
	if !errors.Is(actionCtx.Err(), context.Canceled) || s.State() != SessionPaused {
		t.Fatal("uncooperative action was not cancelled with authority revoked")
	}
	ctx, cancelResume := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelResume()
	if err := c.Resume(ctx, owner, s.ID()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resume bypassed the outstanding operation: %v", err)
	}
	b.FakeBackend.mu.Lock()
	defer b.FakeBackend.mu.Unlock()
	if !b.FakeBackend.Paused {
		t.Fatal("backend was resumed before the old action drained")
	}
}

func TestControllerRepeatedStopDoesNotCancelPendingStopAcknowledgement(t *testing.T) {
	c, b, _, done := startWaitingController(t)
	reply := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(reply) }) }
	defer release()
	b.stopHook = func(ctx context.Context) error {
		select {
		case <-reply:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- c.Stop(context.Background(), c.Session().Owner(), c.Session().ID()) }()
	awaitControl(t, b.stopSeen)
	go func() { second <- c.Stop(context.Background(), c.Session().Owner(), c.Session().ID()) }()
	select {
	case err := <-second:
		t.Fatalf("repeat Stop bypassed the outstanding acknowledgement: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	b.mu.Lock()
	cancelled := b.actionCtx.Err()
	b.mu.Unlock()
	if cancelled != nil {
		t.Fatal("repeat Stop cancelled the action before the first Stop acknowledgement")
	}
	release()
	for _, stopped := range []<-chan error{first, second} {
		if err := awaitControl(t, stopped); err != nil {
			t.Fatal(err)
		}
	}
	awaitControl(t, done)
}

// transientFocusBackend returns a focus_changed failure on the first Execute,
// then succeeds on Resume + a fresh Observe + Execute cycle.
type transientFocusBackend struct {
	*FakeBackend
	failures int
}

func (b *transientFocusBackend) Execute(ctx context.Context, a Action) (ActionReceipt, error) {
	b.mu.Lock()
	b.failures++
	if b.failures == 1 {
		b.mu.Unlock()
		return ActionReceipt{ActionID: a.ID, SessionID: a.SessionID, Outcome: OutcomeUnknown, ErrorCode: ErrorCodeFocusChanged},
			errors.New("focus changed during input")
	}
	b.mu.Unlock()
	return b.FakeBackend.Execute(ctx, a)
}

// preDispatchRecoveryBackend models a native rejection that is known to happen
// before any input reaches the OS. The next fresh observation is allowed to
// continue the run, but the rejected action itself must never be replayed.
type preDispatchRecoveryBackend struct {
	*FakeBackend
	code  string
	calls int
	mu    sync.Mutex
}

func (b *preDispatchRecoveryBackend) Execute(ctx context.Context, action Action) (ActionReceipt, error) {
	b.mu.Lock()
	b.calls++
	first := b.calls == 1
	b.mu.Unlock()
	if first {
		return ActionReceipt{
			ActionID: action.ID, SessionID: action.SessionID,
			Outcome: OutcomeRejected, DispatchState: DispatchNotStarted,
			ErrorCode: b.code,
		}, &codedError{code: b.code}
	}
	return b.FakeBackend.Execute(ctx, action)
}

func TestPreDispatchWindowGeometryRejectionReopensObservation(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &preDispatchRecoveryBackend{
		FakeBackend: &FakeBackend{ObservationValue: obs},
		code:        ErrorCodeUnsupportedDisplay,
	}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err != nil {
		t.Fatal(err)
	}

	a := Action{ID: "geometry-moved", SessionID: s.ID(), ObservationID: obs.ID, Kind: ActionWait}
	receipt, err := c.Execute(context.Background(), owner, a)
	if err == nil {
		t.Fatal("expected pre-dispatch rejection")
	}
	if receipt.Outcome != OutcomeRejected || receipt.DispatchState != DispatchNotStarted || receipt.ErrorCode != ErrorCodeUnsupportedDisplay {
		t.Fatalf("receipt = %+v, want rejected/not_started/unsupported_display", receipt)
	}
	if s.State() != SessionPaused || c.RunSnapshot().State != RunStateObserved {
		t.Fatalf("recovery state = session=%s run=%s, want paused/observed", s.State(), c.RunSnapshot().State)
	}
	if len(b.Executed) != 0 {
		t.Fatalf("pre-dispatch rejection recorded execution: %+v", b.Executed)
	}

	b.FakeBackend.mu.Lock()
	b.FakeBackend.ObservationValue.ID = "obs-after-recovery"
	b.FakeBackend.mu.Unlock()
	if _, err = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err != nil {
		t.Fatalf("fresh observation did not recover: %v", err)
	}
	if c.RunSnapshot().State != RunStateObserved {
		t.Fatalf("run state after fresh observation = %s, want observed", c.RunSnapshot().State)
	}
}

func TestUnknownUnsupportedDisplayStillFailsRun(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &FakeBackend{
		ObservationValue: obs,
		ReceiptValue: ActionReceipt{
			Outcome: OutcomeUnknown, DispatchState: DispatchUnknown,
			ErrorCode: ErrorCodeUnsupportedDisplay,
		},
		ExecuteError: errors.New("native dispatch outcome unknown"),
	}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err != nil {
		t.Fatal(err)
	}
	a := Action{ID: "unknown-geometry", SessionID: s.ID(), ObservationID: obs.ID, Kind: ActionWait}
	if _, err = c.Execute(context.Background(), owner, a); err == nil {
		t.Fatal("expected unknown dispatch error")
	}
	if c.RunSnapshot().State != RunStateFailed || s.State() != SessionFailed {
		t.Fatalf("unknown outcome was recovered: session=%s run=%s", s.State(), c.RunSnapshot().State)
	}
}

// TestFocusChangedExecuteDoesNotFailRun verifies that a transient focus change
// during input pauses the session and reopens the observation phase instead of
// permanently failing the run. The run must stay non-terminal so a fresh
// Observe can resume.
func TestUnknownFocusChangedExecuteFailsRun(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &transientFocusBackend{FakeBackend: &FakeBackend{ObservationValue: obs, ReceiptValue: ActionReceipt{Outcome: OutcomeExecuted, AfterObservationID: "after-1"}}}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()})
	a := Action{ID: "a1", SessionID: s.ID(), ObservationID: obs.ID, Kind: ActionWait}
	receipt, err := c.Execute(context.Background(), owner, a)
	if err == nil {
		t.Fatal("expected execute error on focus failure")
	}
	if receipt.Outcome != OutcomeUnknown || receipt.ErrorCode != ErrorCodeFocusChanged {
		t.Fatalf("receipt = %+v, want unknown/focus_changed", receipt)
	}
	if c.RunSnapshot().State != RunStateFailed || s.State() != SessionFailed {
		t.Fatal("focus error alone incorrectly made unknown input recoverable")
	}
}

// TestFatalUnknownExecuteFailsRun verifies that a non-transient unknown outcome
// (no focus_changed code) still permanently fails the run. The recovery path is
// only for transient failures; genuine helper corruption must stay terminal.
func TestFatalUnknownExecuteFailsRun(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &FakeBackend{ObservationValue: obs, ReceiptValue: ActionReceipt{Outcome: OutcomeUnknown}, ExecuteError: errors.New("helper crashed")}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()})
	a := Action{ID: "a1", SessionID: s.ID(), ObservationID: obs.ID, Kind: ActionWait}
	_, err = c.Execute(context.Background(), owner, a)
	if err == nil {
		t.Fatal("expected execute error on fatal failure")
	}
	if c.RunSnapshot().State != RunStateFailed {
		t.Fatalf("run state = %s, want %s for fatal unknown", c.RunSnapshot().State, RunStateFailed)
	}
}

// codedError lets tests inject a backend error carrying a Code() string without
// depending on the macos package's unexported rejection type.
type codedError struct{ code string }

func (e *codedError) Error() string { return "computer failure: " + e.code }
func (e *codedError) Code() string  { return e.code }

// TestFocusChangedObserveDoesNotFailRun verifies that a transient focus change
// during Observe (not Execute) pauses and reopens the observation phase instead
// of permanently failing the run.
func TestFocusChangedObserveDoesNotFailRun(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &FakeBackend{ObservationValue: obs, ObserveError: &codedError{code: ErrorCodeFocusChanged}}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err == nil {
		t.Fatal("expected observe error on focus failure")
	}
	if c.RunSnapshot().State == RunStateFailed {
		t.Fatal("transient focus observe failure permanently failed the run")
	}
	if c.RunSnapshot().State != RunStateObserved {
		t.Fatalf("run state = %s, want %s (observation phase reopened)", c.RunSnapshot().State, RunStateObserved)
	}
	if s.State() != SessionPaused {
		t.Fatalf("session state = %s, want paused", s.State())
	}
}

// TestNewControllerWithBudget verifies that an explicit budget is applied to the
// runner, and that a zero-value budget falls back to DefaultRunBudget.
func TestNewControllerWithBudget(t *testing.T) {
	s, _, _ := newTestSession(t, false)
	b := &FakeBackend{}
	custom := RunBudget{TotalDuration: 42 * time.Second, MaxInputActions: 7, MaxModelTurns: 3}
	c, err := NewControllerWithBudget(s, b, nil, custom)
	if err != nil {
		t.Fatal(err)
	}
	snap := c.RunSnapshot()
	if snap.Budget.TotalDuration != 42*time.Second || snap.Budget.MaxInputActions != 7 || snap.Budget.MaxModelTurns != 3 {
		t.Fatalf("custom budget not applied: %+v", snap.Budget)
	}

	defaultCtrl, err := NewControllerWithBudget(s, &FakeBackend{}, nil, RunBudget{})
	if err != nil {
		t.Fatal(err)
	}
	defSnap := defaultCtrl.RunSnapshot()
	if defSnap.Budget != DefaultRunBudget() {
		t.Fatalf("zero budget did not fall back to default: %+v", defSnap.Budget)
	}
}

func TestCompleteDispatchWithoutEvidenceKeepsObservationRecoveryOpen(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	obs := readyObservation(s.ID(), now)
	b := &FakeBackend{ObservationValue: obs, ReceiptValue: ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete, ErrorCode: ErrorCodeScreenshotFailed}}
	c, err := NewController(s, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err != nil {
		t.Fatal(err)
	}
	a := Action{ID: "complete", SessionID: s.ID(), ObservationID: obs.ID, Kind: ActionWait}
	r, err := c.Execute(context.Background(), owner, a)
	if err != nil || r.Outcome != OutcomeExecuted || c.RunSnapshot().State == RunStateFailed {
		t.Fatalf("receipt=%+v err=%v state=%v", r, err, c.RunSnapshot().State)
	}
	b.ObservationValue.ID = "fresh-complete"
	if _, err = c.Observe(context.Background(), owner, ObserveRequest{SessionID: s.ID()}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Execute(context.Background(), owner, a); err == nil {
		t.Fatal("acknowledged action replayed")
	}
}

func TestUnknownPausedSessionCannotResumeOrReplayUnderNewID(t *testing.T) {
	s, owner, now := newTestSession(t, false)
	_ = s.SetObservation(readyObservation(s.ID(), now))
	a := Action{ID: "uncertain", SessionID: s.ID(), ObservationID: "obs-1", Kind: ActionType, Text: "test"}
	if err := s.BeginAction(a); err != nil {
		t.Fatal(err)
	}
	_ = s.Pause()
	_ = s.RecordReceipt(ActionReceipt{ActionID: a.ID, SessionID: s.ID(), Outcome: OutcomeUnknown})
	if err := s.Resume(owner); err == nil {
		t.Fatal("unknown input restored by resume")
	}
	a.ID = "changed-id"
	if err := s.BeginAction(a); err == nil {
		t.Fatal("unknown replayed under new id")
	}
}

func TestPermissionRevokeBeforeDispatchPreservesPermissionErrorAndBlocksReplay(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	backend := &FakeBackend{CapabilitiesValue: readyCapabilities(), ObservationValue: readyObservation(session.ID(), now), ReceiptValue: ActionReceipt{Outcome: OutcomeExecuted, DispatchState: DispatchComplete}}
	controller, err := NewController(session, backend)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := controller.Observe(context.Background(), owner, ObserveRequest{SessionID: session.ID()})
	if err != nil {
		t.Fatal(err)
	}

	backend.mu.Lock()
	backend.CapabilitiesValue.CaptureReadiness = ReadinessPermissionRequired
	backend.CapabilitiesValue.InputReadiness = ReadinessPermissionRequired
	backend.CapabilitiesValue.PermissionState = PermissionRequired
	backend.mu.Unlock()
	if _, err := controller.Capabilities(context.Background(), owner, session.ID()); err != nil {
		t.Fatal(err)
	}
	if session.State() != SessionPaused {
		t.Fatalf("session state = %s, want paused after permission revoke", session.State())
	}

	action := Action{ID: "permission-revoked", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}
	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil {
		t.Fatal("permission-revoked turn unexpectedly succeeded")
	}
	if result.Receipt.Outcome != OutcomeRejected || result.DispatchState != DispatchNotStarted || result.ErrorCode != ErrorCodePermissionRequired {
		t.Fatalf("permission-revoked result = %+v, want rejected/not_started/permission_required", result)
	}
	if len(backend.Executed) != 0 {
		t.Fatalf("revoked permission dispatched input: %+v", backend.Executed)
	}
	if result.RetryPolicy != RetryObserveOnly {
		t.Fatalf("retry policy = %s, want observe_only", result.RetryPolicy)
	}
	if _, err := controller.ExecuteTurn(context.Background(), owner, Action{ID: "permission-replayed", SessionID: session.ID(), ObservationID: initial.ID, Kind: ActionWait}); err == nil {
		t.Fatal("permission-revoked action was replayed under a new action ID")
	}

	backend.mu.Lock()
	backend.CapabilitiesValue = readyCapabilities()
	backend.ObservationValue.ID = "permission-restored-observation"
	backend.mu.Unlock()
	if _, err := controller.Capabilities(context.Background(), owner, session.ID()); err != nil {
		t.Fatal(err)
	}
	if err := controller.Resume(context.Background(), owner, session.ID()); err != nil {
		t.Fatalf("permission restore did not require explicit resume: %v", err)
	}
	if _, err := controller.ExecuteTurn(context.Background(), owner, Action{ID: "before-fresh-observe", SessionID: session.ID(), ObservationID: "permission-restored-observation", Kind: ActionWait}); err == nil {
		t.Fatal("action was allowed before fresh observe after permission restore")
	}
	restored, err := controller.Observe(context.Background(), owner, ObserveRequest{SessionID: session.ID()})
	if err != nil {
		t.Fatalf("fresh observe after permission restore failed: %v", err)
	}
	backend.mu.Lock()
	backend.ObservationValue.ID = "permission-post-action-observation"
	backend.mu.Unlock()
	result, err = controller.ExecuteTurn(context.Background(), owner, Action{ID: "after-permission-restore", SessionID: session.ID(), ObservationID: restored.ID, Kind: ActionWait})
	if err == nil || result.Outcome != OutcomeExecuted || result.DispatchState != DispatchComplete {
		t.Fatalf("new action after explicit recovery was not dispatched: result=%+v err=%v", result, err)
	}
}

func TestPermissionRevokeAfterDispatchIsUnknownAndTerminal(t *testing.T) {
	session, owner, now := newTestSession(t, false)
	backend := &FakeBackend{
		CapabilitiesValue: readyCapabilities(),
		ObservationValue:  readyObservation(session.ID(), now),
		ReceiptValue: ActionReceipt{
			Outcome:       OutcomeUnknown,
			DispatchState: DispatchUnknown,
			Verification:  VerificationUnknown,
			ErrorCode:     ErrorCodePermissionRequired,
		},
		ExecuteError: &codedError{code: ErrorCodePermissionRequired},
	}
	controller, err := NewController(session, backend)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := controller.Observe(context.Background(), owner, ObserveRequest{SessionID: session.ID()})
	if err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "permission-race", SessionID: session.ID(), ObservationID: observation.ID, Kind: ActionWait}
	result, err := controller.ExecuteTurn(context.Background(), owner, action)
	if err == nil {
		t.Fatal("permission race unexpectedly returned nil error")
	}
	if result.Outcome != OutcomeUnknown || result.DispatchState != DispatchUnknown || result.RetryPolicy != RetryNever || result.Receipt.ActionID == "" {
		t.Fatalf("permission race result = %+v", result)
	}
	if session.State() != SessionFailed || !session.InputUncertain() {
		t.Fatalf("permission race state = %s uncertain=%t, want failed/true", session.State(), session.InputUncertain())
	}
	if err := controller.Resume(context.Background(), owner, session.ID()); err == nil {
		t.Fatal("unknown permission race was resumable")
	}
	if _, err := controller.ExecuteTurn(context.Background(), owner, Action{ID: "permission-race-replay", SessionID: session.ID(), ObservationID: observation.ID, Kind: ActionWait}); err == nil {
		t.Fatal("unknown permission race was replayed under a new action ID")
	}
}

func TestHelperObserveFaultIsRecoverableWithoutInputReplay(t *testing.T) {
	session, owner, _ := newTestSession(t, false)
	backend := &FakeBackend{
		CapabilitiesValue: readyCapabilities(),
		ObserveError:      &codedError{code: ErrorCodeHelperUnavailable},
	}
	controller, err := NewController(session, backend)
	if err != nil {
		t.Fatal(err)
	}
	_, err = controller.Observe(context.Background(), owner, ObserveRequest{SessionID: session.ID()})
	if err == nil {
		t.Fatal("helper observe fault unexpectedly succeeded")
	}
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != ErrorCodeHelperUnavailable {
		t.Fatalf("observe fault = %v, code=%q", err, coded.Code())
	}
	if session.State() != SessionPaused || controller.RunSnapshot().State == RunStateFailed {
		t.Fatalf("helper fault lifecycle = session=%s run=%s, want paused/non-terminal", session.State(), controller.RunSnapshot().State)
	}
}

func TestUnavailableCapabilitiesUseHelperFaultCodeNotPermissionRevoke(t *testing.T) {
	session, owner, _ := newTestSession(t, true)
	caps := session.Capabilities()
	caps.CaptureReadiness = ReadinessUnavailable
	caps.InputReadiness = ReadinessUnavailable
	caps.PermissionState = PermissionApproved
	if err := session.UpdateCapabilities(caps); err != nil {
		t.Fatal(err)
	}
	if err := session.Approve(owner); err == nil {
		t.Fatal("unavailable helper was approved")
	} else {
		var coded interface{ Code() string }
		if !errors.As(err, &coded) || coded.Code() != ErrorCodeHelperUnavailable {
			t.Fatalf("approve error = %v, code=%q, want helper_unavailable", err, coded.Code())
		}
	}
}
