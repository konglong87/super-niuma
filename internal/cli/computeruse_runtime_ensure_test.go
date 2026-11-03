package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	cu "github.com/konglong87/super-niuma/internal/computeruse"
	"github.com/konglong87/super-niuma/internal/tools"
	cutool "github.com/konglong87/super-niuma/internal/tools/computeruse"
)

// Like the host, Ensure reuses a live grant but creates a new one after Stop.
// All counters/grants are synchronized so the regressions also run under -race.
type queryComputerBridge struct {
	computerUseWiringService
	mu        sync.Mutex
	starts    int
	lookups   int
	stops     []string
	current   string
	grants    map[string]bool
	image     []byte
	ensureACK func(context.Context, string) (string, error)
}

// attemptQueryComputerBridge models a host that commits the grant but loses
// the Ensure response. The resolver can return only the exact attempt token.
type attemptQueryComputerBridge struct {
	*queryComputerBridge
	mu          sync.Mutex
	attempts    map[string]string
	resolveCall int
	dropACK     bool
}

func (b *attemptQueryComputerBridge) EnsureComputerSessionAttempt(ctx context.Context, owner cu.SessionOwner, attemptID string) (string, error) {
	id, err := b.queryComputerBridge.EnsureComputerSession(ctx, owner)
	if err != nil && id == "" {
		return "", err
	}
	b.mu.Lock()
	if b.attempts == nil {
		b.attempts = make(map[string]string)
	}
	b.attempts[attemptID] = id
	b.mu.Unlock()
	if b.dropACK {
		return "", errors.New("simulated lost startup acknowledgement")
	}
	return id, err
}

func (b *attemptQueryComputerBridge) ResolveComputerSessionStart(_ context.Context, _ cu.SessionOwner, attemptID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.resolveCall++
	id := b.attempts[attemptID]
	if id == "" {
		return "", errors.New("attempt not found")
	}
	return id, nil
}

func (b *queryComputerBridge) EnsureComputerSession(ctx context.Context, owner cu.SessionOwner) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if owner != computerRuntimeOwner {
		return "", errors.New("owner denied")
	}
	b.mu.Lock()
	b.starts++ // Count IPC attempts, not just grant creations.
	if !b.grants[b.current] {
		b.current = fmt.Sprintf("query-host-%d", b.starts)
		b.grants[b.current] = true
	}
	id := b.current
	b.mu.Unlock()
	if b.ensureACK != nil {
		return b.ensureACK(ctx, id)
	}
	return id, nil
}

func (b *queryComputerBridge) Lookup(context.Context, cu.SessionOwner) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lookups++
	return b.current, nil
}

func (b *queryComputerBridge) Observe(ctx context.Context, owner cu.SessionOwner, req cu.ObserveRequest) (cu.Observation, error) {
	if err := ctx.Err(); err != nil {
		return cu.Observation{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if owner != computerRuntimeOwner || !b.grants[req.SessionID] {
		return cu.Observation{}, errors.New("grant stopped or owner denied")
	}
	return cu.Observation{ID: computerRuntimeObservation, SessionID: req.SessionID}, nil
}

func (b *queryComputerBridge) ObservationImage(context.Context, cu.SessionOwner, string, string) ([]byte, string, error) {
	return b.image, "image/png", nil
}

func (b *queryComputerBridge) Stop(ctx context.Context, owner cu.SessionOwner, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner != computerRuntimeOwner {
		return errors.New("owner denied")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stops = append(b.stops, id)
	b.grants[id] = false
	return nil
}

func (b *queryComputerBridge) counts() (int, int, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starts, b.lookups, append([]string(nil), b.stops...)
}

func newQueryComputerBridge(t *testing.T) *queryComputerBridge {
	t.Helper()
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return &queryComputerBridge{grants: make(map[string]bool), image: imageData.Bytes()}
}

func newTrackedQueryComputer(t *testing.T, bridge desktopComputerBridge) (*trackedDesktopComputerService, func()) {
	t.Helper()
	opts := computerRuntimeOptions(bridge)
	_, cleanup := configureDesktopComputerUse(context.Background(), computerRuntimeConfig(), computerRuntimeModel, &opts)
	tracked, ok := opts.computerUseService.(*trackedDesktopComputerService)
	if !ok {
		t.Fatalf("unexpected service %T", opts.computerUseService)
	}
	t.Cleanup(cleanup)
	return tracked, cleanup
}

func TestDesktopComputerUseExactStartupAttemptRecoversLostAck(t *testing.T) {
	base := newQueryComputerBridge(t)
	bridge := &attemptQueryComputerBridge{queryComputerBridge: base, dropACK: true}
	tracked, _ := newTrackedQueryComputer(t, bridge)

	id, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil || id == "" {
		t.Fatalf("lost Ensure ACK was not recovered: id=%q err=%v", id, err)
	}
	bridge.mu.Lock()
	resolved := bridge.resolveCall
	bridge.mu.Unlock()
	starts, lookups, _ := base.counts()
	if resolved != 1 || starts != 1 || lookups != 0 {
		t.Fatalf("recovery resolved wrong scope: resolves=%d starts=%d lookups=%d", resolved, starts, lookups)
	}
}

func TestDesktopComputerUseQueryStopPreventsImplicitRestart(t *testing.T) {
	for _, hostStop := range []bool{false, true} {
		t.Run(fmt.Sprintf("user_stop=%t", hostStop), func(t *testing.T) {
			bridge := newQueryComputerBridge(t)
			tracked, _ := newTrackedQueryComputer(t, bridge)
			tc := tools.Context{ComputerUse: tracked, ComputerUseImageSupported: true,
				TenantID: computerRuntimeOwner.TenantID, UserID: computerRuntimeOwner.UserID, SessionID: computerRuntimeOwner.SessionID}
			tool := cutool.Tool{}
			observe := []byte(`{"action":"observe"}`)
			if result := tool.Run(context.Background(), observe, tc); result.IsError {
				t.Fatalf("first observe: %s", result.Content)
			}
			bound := tracked.session()
			if hostStop {
				// UI Stop is invisible to the query-local wrapper.
				if err := bridge.Stop(context.Background(), computerRuntimeOwner, bound); err != nil {
					t.Fatal(err)
				}
			} else if result := tool.Run(context.Background(), []byte(`{"action":"stop"}`), tc); result.IsError {
				t.Fatalf("stop: %s", result.Content)
			}
			if result := tool.Run(context.Background(), observe, tc); !result.IsError || !strings.Contains(result.Content, cu.ErrorCodeActionFailed) {
				t.Errorf("observe after Stop must reach the stopped grant and be denied: %+v", result)
			}
			if starts, _, _ := bridge.counts(); starts != 1 || tracked.session() != bound {
				t.Errorf("Stop bypassed: starts=%d binding=%q original=%q", starts, tracked.session(), bound)
			}
		})
	}
}

func TestDesktopComputerUseQueryNewServiceCanStart(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	first, cleanup := newTrackedQueryComputer(t, bridge)
	firstID, err := first.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	second, _ := newTrackedQueryComputer(t, bridge)
	secondID, err := second.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil || secondID == firstID || secondID == "" {
		t.Fatalf("new query: first=%q second=%q err=%v", firstID, secondID, err)
	}
	if starts, _, _ := bridge.counts(); starts != 2 {
		t.Fatalf("starts=%d, want 2", starts)
	}
}

func TestDesktopComputerUseQueryBoundLookupNeedsNoCoordinator(t *testing.T) {
	bridge := &runtimeComputerBridge{sessionID: computerRuntimeHostSession}
	tracked, _ := newTrackedQueryComputer(t, bridge)
	id, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil || id != computerRuntimeHostSession {
		t.Fatalf("bound lookup must be reused: id=%q err=%v", id, err)
	}
}

func TestDesktopComputerUseQueryEnsureConcurrent(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("error=%t", fail), func(t *testing.T) {
			bridge := newQueryComputerBridge(t)
			ackErr := errors.New("lost startup ACK")
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			bridge.ensureACK = func(_ context.Context, id string) (string, error) {
				once.Do(func() { close(entered) })
				<-release
				if fail {
					return "", ackErr
				}
				return id, nil
			}
			tracked, _ := newTrackedQueryComputer(t, bridge)
			const callers = 24
			results := make(chan error, callers)
			for range callers {
				go func() {
					id, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
					if fail && errors.Is(err, ackErr) || !fail && err == nil && id != "" {
						results <- nil
					} else {
						results <- fmt.Errorf("id=%q err=%v", id, err)
					}
				}()
			}
			<-entered
			// Reading the binding must not wait for the host IPC mutex.
			bindingRead := make(chan struct{})
			go func() { tracked.session(); close(bindingRead) }()
			select {
			case <-bindingRead:
			case <-time.After(time.Second):
				t.Error("session mutex held across Ensure IPC")
			}
			close(release)
			for range callers {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			if starts, _, _ := bridge.counts(); starts != 1 {
				t.Fatalf("Ensure dispatched %d times, want 1", starts)
			}
		})
	}
}

func TestDesktopComputerUseQueryEnsureErrorIsSticky(t *testing.T) {
	for _, tc := range []struct {
		name     string
		returnID bool
		ackErr   error
	}{
		{name: "lost ACK", ackErr: errors.New("transport failed")},
		{name: "known ID with error", returnID: true, ackErr: errors.New("start incomplete")},
		{name: "empty success ACK"},
		{name: "canceled startup", ackErr: context.Canceled},
		{name: "startup deadline", ackErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := newQueryComputerBridge(t)
			bridge.ensureACK = func(_ context.Context, id string) (string, error) {
				if tc.returnID {
					return id, tc.ackErr
				}
				return "", tc.ackErr
			}
			tracked, cleanup := newTrackedQueryComputer(t, bridge)
			for range 2 {
				id, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
				if id != "" || !errors.Is(err, errComputerSessionStartUnknown) || tc.ackErr != nil && !errors.Is(err, tc.ackErr) {
					t.Errorf("failed start must remain a failure: id=%q err=%v", id, err)
				}
			}
			if !tc.returnID {
				if _, err := tracked.CurrentComputerSession(context.Background(), computerRuntimeOwner); !errors.Is(err, errComputerSessionStartUnknown) {
					t.Errorf("unknown start was reported as never started: %v", err)
				}
			}
			cleanup()
			starts, lookups, stops := bridge.counts()
			if starts != 1 || lookups != 0 {
				t.Errorf("unsafe recovery: starts=%d lookups=%d", starts, lookups)
			}
			if tc.returnID && (len(stops) != 1 || stops[0] != tracked.session()) || !tc.returnID && len(stops) != 0 {
				t.Errorf("cleanup must stop only an acknowledged ID: %v", stops)
			}
		})
	}
}

func TestDesktopComputerUseQueryEnsureContextAndOwner(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	tracked, _ := newTrackedQueryComputer(t, bridge)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tracked.EnsureComputerSession(canceled, computerRuntimeOwner); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before start: %v", err)
	}
	if starts, _, _ := bridge.counts(); starts != 0 {
		t.Fatalf("canceled caller started host: %d", starts)
	}
	if _, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := tracked.EnsureComputerSession(canceled, computerRuntimeOwner); !errors.Is(err, context.Canceled) {
		t.Errorf("cached success bypassed cancellation: %v", err)
	}
	for _, owner := range []cu.SessionOwner{
		{}, {TenantID: math.MaxUint64, UserID: 11, SessionID: 13},
		{TenantID: 8, UserID: 11, SessionID: 13}, {TenantID: 7, UserID: 12, SessionID: 13}, {TenantID: 7, UserID: 11, SessionID: 14},
	} {
		if _, err := tracked.EnsureComputerSession(context.Background(), owner); err == nil {
			t.Errorf("cached Ensure bypassed owner: %+v", owner)
		}
		if _, err := tracked.CurrentComputerSession(context.Background(), owner); err == nil {
			t.Errorf("cached binding bypassed owner: %+v", owner)
		}
	}
	if starts, _, _ := bridge.counts(); starts != 1 {
		t.Errorf("invalid/cached callers must not start: %d", starts)
	}
}

type ensureWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *ensureWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDesktopComputerUseQueryEnsureCanceledWaiter(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	bridge.ensureACK = func(_ context.Context, id string) (string, error) {
		once.Do(func() { close(entered) })
		<-release
		return id, nil
	}
	tracked, _ := newTrackedQueryComputer(t, bridge)
	first := make(chan error, 1)
	go func() {
		_, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
		first <- err
	}()
	<-entered
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &ensureWaitContext{Context: base, waiting: make(chan struct{})}
	waiter := make(chan error, 1)
	go func() { _, err := tracked.EnsureComputerSession(ctx, computerRuntimeOwner); waiter <- err }()
	select {
	case <-ctx.waiting:
	case <-time.After(time.Second):
		t.Error("caller did not wait on the existing Ensure")
	}
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled waiter: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("canceled waiter waited for host ACK")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if _, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner); err != nil {
		t.Fatal(err)
	}
	if starts, _, _ := bridge.counts(); starts != 1 {
		t.Fatalf("waiter replayed start: %d", starts)
	}
}

func TestDesktopComputerUseQueryEnsureCancellationAfterACKKeepsCleanupBinding(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge.ensureACK = func(_ context.Context, id string) (string, error) { cancel(); return id, nil }
	tracked, cleanup := newTrackedQueryComputer(t, bridge)
	if id, err := tracked.EnsureComputerSession(ctx, computerRuntimeOwner); id != "" || !errors.Is(err, context.Canceled) {
		t.Errorf("cancellation racing ACK returned success: id=%q err=%v", id, err)
	}
	cleanup()
	if _, _, stops := bridge.counts(); len(stops) != 1 || stops[0] != tracked.session() {
		t.Fatalf("lost acknowledged startup during cancellation: stops=%v binding=%q", stops, tracked.session())
	}
}

func TestDesktopComputerUseQueryCleanupDuringEnsure(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	entered, release := make(chan struct{}), make(chan struct{})
	bridge.ensureACK = func(_ context.Context, id string) (string, error) { close(entered); <-release; return id, nil }
	tracked, cleanup := newTrackedQueryComputer(t, bridge)
	result := make(chan error, 1)
	go func() {
		_, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner)
		result <- err
	}()
	<-entered
	cleanup()
	close(release)
	if err := <-result; err == nil {
		t.Error("closed query returned startup success")
	}
	cleanup()
	if starts, lookups, stops := bridge.counts(); starts != 1 || lookups != 0 || len(stops) != 1 || stops[0] != tracked.session() {
		t.Fatalf("late ACK cleanup: starts=%d lookups=%d stops=%v bound=%q", starts, lookups, stops, tracked.session())
	}
}

func TestDesktopComputerUseQueryCleanupBeforeEnsure(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	tracked, cleanup := newTrackedQueryComputer(t, bridge)
	cleanup()
	if _, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner); err == nil {
		t.Error("closed query started a session")
	}
	if starts, lookups, stops := bridge.counts(); starts != 0 || lookups != 0 || len(stops) != 0 {
		t.Fatalf("unused query acquired authority: starts=%d lookups=%d stops=%v", starts, lookups, stops)
	}
}

func TestDesktopComputerUseQueryBindingPreservesObservation(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	tracked, _ := newTrackedQueryComputer(t, bridge)
	ctx := context.Background()
	id, err := tracked.EnsureComputerSession(ctx, computerRuntimeOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracked.Observe(ctx, computerRuntimeOwner, cu.ObserveRequest{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	if cached, err := tracked.EnsureComputerSession(ctx, computerRuntimeOwner); err != nil || cached != id {
		t.Fatalf("cached binding: id=%q err=%v", cached, err)
	}
	if observation, err := tracked.CurrentComputerObservation(ctx, computerRuntimeOwner); err != nil || observation != computerRuntimeObservation {
		t.Fatalf("Ensure cleared observation: id=%q err=%v", observation, err)
	}
	if _, err := tracked.CurrentComputerObservation(ctx, cu.SessionOwner{}); err == nil {
		t.Error("observation cache bypassed owner check")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tracked.CurrentComputerObservation(canceled, computerRuntimeOwner); !errors.Is(err, context.Canceled) {
		t.Errorf("observation cache bypassed cancellation: %v", err)
	}
	if _, err := tracked.CurrentComputerSession(canceled, computerRuntimeOwner); !errors.Is(err, context.Canceled) {
		t.Errorf("session cache bypassed cancellation: %v", err)
	}
	const replacement = "another-query-session"
	bridge.grants[replacement] = true
	if _, err := tracked.Observe(ctx, computerRuntimeOwner, cu.ObserveRequest{SessionID: replacement}); err == nil {
		t.Error("explicit observe rebound query")
	}
	if tracked.session() != id {
		t.Fatalf("stable binding replaced: %q", tracked.session())
	}
}

func TestDesktopComputerUseQueryInvalidCallerDoesNotConsumeStart(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	tracked, _ := newTrackedQueryComputer(t, bridge)
	if _, err := tracked.EnsureComputerSession(context.Background(), cu.SessionOwner{}); err == nil {
		t.Fatal("invalid caller accepted")
	}
	if _, err := tracked.EnsureComputerSession(context.Background(), computerRuntimeOwner); err != nil {
		t.Fatal(err)
	}
	if starts, _, _ := bridge.counts(); starts != 1 {
		t.Fatalf("starts=%d, want 1", starts)
	}
}

func TestDesktopComputerUseQueryLostACKCannotCleanUpAnotherQuery(t *testing.T) {
	bridge := newQueryComputerBridge(t)
	bridge.ensureACK = func(context.Context, string) (string, error) { return "", errors.New("lost ACK") }
	first, cleanupFirst := newTrackedQueryComputer(t, bridge)
	if _, err := first.EnsureComputerSession(context.Background(), computerRuntimeOwner); err == nil {
		t.Fatal("expected lost ACK")
	}
	// A new query can acquire the host grant, but the old query cannot prove
	// that Lookup's current answer is the session started by its failed IPC.
	bridge.ensureACK = nil
	second, _ := newTrackedQueryComputer(t, bridge)
	id, err := second.EnsureComputerSession(context.Background(), computerRuntimeOwner)
	if err != nil {
		t.Fatal(err)
	}
	cleanupFirst()
	if _, err := second.Observe(context.Background(), computerRuntimeOwner, cu.ObserveRequest{SessionID: id}); err != nil {
		t.Fatalf("old unknown-start cleanup revoked new query: %v", err)
	}
	if starts, lookups, stops := bridge.counts(); starts != 2 || lookups != 0 || len(stops) != 0 {
		t.Fatalf("uncertain authority recovery: starts=%d lookups=%d stops=%v", starts, lookups, stops)
	}
}
