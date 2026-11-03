package control_test

// These are fixture E2E tests: the database, scheduler, filesystem, verifier,
// resource fences and bus are real. No model, external provider or desktop app
// is invoked. Write receipts below come from the trusted fixture executor, not
// from parsing an untrusted model's textual claims.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/eventbus"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/sqlitestore"
	"github.com/konglong87/super-niuma/internal/super/verification"
	"github.com/konglong87/super-niuma/internal/super/workorder"
)

type sqliteAuthority struct{ revoked atomic.Bool }

func (a *sqliteAuthority) Current(ctx context.Context, _ control.Principal, _ control.Grant) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if a.revoked.Load() {
		return control.ErrDenied
	}
	return nil
}

type sqliteExecutor func(context.Context, execution.Request) (execution.Result, error)

func (f sqliteExecutor) Execute(ctx context.Context, r execution.Request) (execution.Result, error) {
	return f(ctx, r)
}

type sqliteSender struct {
	mu    sync.Mutex
	calls []string
	fail  error
}

func (s *sqliteSender) Send(ctx context.Context, target control.Target, id, _ string) (control.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return control.Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, id)
	if s.fail != nil {
		return control.Receipt{}, s.fail
	}
	return control.Receipt{MessageID: "fixture." + id, RequestID: "fixture-request." + id, Chat: target.Chat, DeliveredAtMS: time.Now().UnixMilli()}, nil
}
func (s *sqliteSender) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.calls) }

type sqliteHarness struct {
	store             *sqlitestore.Store
	dbPath, workspace string
	engine            *control.Engine
	authority         *sqliteAuthority
	sender            *sqliteSender
	principal         control.Principal
}

func newSQLiteHarness(t *testing.T) *sqliteHarness {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "private", "control.db")
	store, err := sqlitestore.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	authority, sender := &sqliteAuthority{}, &sqliteSender{}
	h := &sqliteHarness{store: store, dbPath: dbPath, workspace: workspace, authority: authority, sender: sender, principal: control.Principal{Scope: control.LocalScope, Owner: control.LocalOwner, LocalAdmin: true}}
	h.engine = &control.Engine{Store: store, Authority: authority, Executor: sqliteExecutor(sqliteWork), Verifier: control.FileVerifier{}, Sender: sender, Workers: 2, Lease: 2 * time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	t.Cleanup(func() { _ = h.store.Close() })
	return h
}
func (h *sqliteHarness) grant(nodes ...control.Node) control.Grant {
	if len(nodes) == 0 {
		nodes = []control.Node{sqliteNode("write", nil, nil, []string{"result.txt"})}
	}
	return control.Grant{ProfileID: "fixture", Revision: "v1", Workspace: h.workspace, Provider: "fixture-no-network", Model: "fixture-no-model", MaxTokens: 1000, TimeoutMS: 4000, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), AutoApprove: true, Nodes: nodes, Verification: verification.Policy{Version: "v1", Rules: []verification.Rule{{Path: "result.txt", Kind: verification.KindTextContains, Expected: "fixture accepted"}}}, Targets: []control.Target{{ID: "desktop", Provider: control.ProviderDesktop}}}
}
func sqliteNode(id string, deps, reads, writes []string) control.Node {
	return control.Node{ID: id, Instruction: "fixture " + id, DependsOn: deps, ReadPaths: reads, WritePaths: writes, MaxTurns: 2, MaxOutputTokens: 20}
}
func (h *sqliteHarness) start(t *testing.T, key string, g control.Grant) control.Run {
	t.Helper()
	svc := workorder.NewService(h.store, nil)
	intake, _, err := svc.Create(context.Background(), key, workorder.CreateInput{Title: "SQLite integration fixture", Objective: "fixture accepted", AcceptanceCriteria: []string{"result independently verified"}, Workspace: g.Workspace})
	if err != nil {
		t.Fatal(err)
	}
	r, created, err := h.engine.Start(context.Background(), h.principal, key, "fixture accepted", intake.ID, g, nil)
	if err != nil || !created {
		t.Fatalf("start: created=%v err=%v", created, err)
	}
	return r
}
func (h *sqliteHarness) get(t *testing.T, id string) control.Run {
	t.Helper()
	r, err := h.store.GetRun(context.Background(), control.LocalScope, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (h *sqliteHarness) step(t *testing.T, owner string) {
	t.Helper()
	_, err := h.engine.Step(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
}
func (h *sqliteHarness) reopen(t *testing.T) {
	t.Helper()
	if err := h.store.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := sqlitestore.Open(context.Background(), h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	h.store = s
	h.engine.Store = s
}
func sqliteWork(ctx context.Context, r execution.Request) (execution.Result, error) {
	if err := r.CheckAuthority(ctx); err != nil {
		return execution.Result{}, err
	}
	id := r.AttemptID + ".request"
	if err := r.Budget.Reserve(ctx, id, 20, 10); err != nil {
		return execution.Result{UsageKnown: true}, err
	}
	result := execution.Result{Text: r.JobID, UsageKnown: true, Usage: execution.Usage{InputTokens: 5, OutputTokens: 3}}
	for _, path := range r.ReadPaths {
		// ReadPaths is an authority ceiling, not a requirement to read a file
		// that this fixture is about to create/overwrite.
		writes := false
		for _, out := range r.WritePaths {
			if out == path {
				writes = true
			}
		}
		if writes {
			continue
		}
		if _, err := os.ReadFile(filepath.Join(r.Workspace, path)); err != nil {
			return result, err
		}
	}
	for _, path := range r.WritePaths {
		if err := r.CheckAuthority(ctx); err != nil {
			return result, err
		}
		data := []byte("fixture accepted: " + r.JobID)
		if err := os.WriteFile(filepath.Join(r.Workspace, path), data, 0600); err != nil {
			return result, err
		}
		sum := sha256.Sum256(data)
		result.Writes = append(result.Writes, execution.WriteReceipt{Path: path, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data)), AttemptID: r.AttemptID})
	}
	if err := r.Budget.Settle(ctx, id, result.Usage, true); err != nil {
		return result, err
	}
	return result, nil
}
func sqliteWaitRun(t *testing.T, h *sqliteHarness, id string, want control.Status) control.Run {
	t.Helper()
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		r := h.get(t, id)
		if r.Status == want {
			return r
		}
		if control.IsTerminal(r.Status) {
			t.Fatalf("unexpected terminal status=%s code=%s jobs=%+v", r.Status, r.SafeCode, sqliteJobStates(r))
		}
		select {
		case <-deadline.C:
			t.Fatalf("run did not reach %s; status=%s code=%s jobs=%+v", want, r.Status, r.SafeCode, sqliteJobStates(r))
		case <-tick.C:
		}
	}
}
func sqliteAssertReceipt(t *testing.T, h *sqliteHarness, r control.Run) {
	t.Helper()
	if r.Status != control.Delivered || r.Verification == nil || !r.Verification.Passed || r.VerificationAttemptID == "" || len(r.Verification.Artifacts) != 1 {
		t.Fatalf("incomplete verification: %+v", r)
	}
	if len(r.Notices) != 1 || r.Notices[0].Status != control.DeliverySent || r.Notices[0].Receipt == nil || r.Notices[0].Receipt.MessageID == "" || r.Notices[0].Receipt.RequestID == "" || r.Notices[0].Receipt.DeliveredAtMS <= 0 {
		t.Fatalf("incomplete receipt: %+v", r.Notices)
	}
	n := r.Notices[0]
	if n.Claim.Epoch != uint64(n.Attempts) || n.Claim.Token == "" || n.Claim.Owner == "" || n.Claim.UntilMS <= 0 || n.Claim.Scope != r.Scope || n.Claim.RunID != r.ID || n.Claim.JobID != "super.delivery."+n.Target.ID {
		t.Fatal("settled notice lost immutable claim provenance or monotonic epoch")
	}
	// Persisted provenance is not live authority: execution must consult the
	// durable state gate even while the historical lease deadline is future.
	if err := h.store.CheckClaim(context.Background(), n.Claim); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("settled delivery claim retained authority: %v", err)
	}
	a := r.Verification.Artifacts[0]
	matched := false
	for _, j := range r.Jobs {
		for _, w := range j.Result.Writes {
			if w.Path == a.Path && w.SHA256 == a.SHA256 && w.Bytes == a.Bytes && w.AttemptID == j.Claim.Token {
				matched = true
			}
		}
	}
	if !matched {
		t.Fatal("verifier evidence lacks a current trusted write receipt")
	}
}

func TestSQLiteIntegrationTwoWorkersDAGAndRestart(t *testing.T) {
	h := newSQLiteHarness(t)
	nodes := []control.Node{sqliteNode("seed", nil, []string{"seed.txt"}, []string{"seed.txt"}), sqliteNode("read-left", []string{"seed"}, []string{"seed.txt"}, nil), sqliteNode("read-right", []string{"seed"}, []string{"seed.txt"}, nil), sqliteNode("final", []string{"read-left", "read-right"}, nil, []string{"result.txt"})}
	nodes[1].ParentID = "seed"
	nodes[2].ParentID = "seed"
	var active, peak, readers atomic.Int32
	barrier := make(chan struct{})
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if r.JobID == "read-left" || r.JobID == "read-right" {
			if readers.Add(1) == 2 {
				close(barrier)
			}
			select {
			case <-barrier:
			case <-ctx.Done():
				return execution.Result{}, ctx.Err()
			}
		}
		if r.JobID == "final" && (len(r.Dependencies) != 2 || r.Dependencies[0].Result == "" || r.Dependencies[1].Result == "") {
			return execution.Result{}, errors.New("DAG dependency results missing")
		}
		return sqliteWork(ctx, r)
	})
	r := h.start(t, "dag", h.grant(nodes...))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.engine.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("fixture workers did not stop")
		}
	})
	r = sqliteWaitRun(t, h, r.ID, control.Delivered)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	done <- nil
	if peak.Load() != 2 || readers.Load() != 2 {
		t.Fatalf("two workers never overlapped: peak=%d readers=%d", peak.Load(), readers.Load())
	}
	if len(r.Reservations) != 4 || control.ReservedTokens(r) != 32 {
		t.Fatalf("root budget mismatch: reservations=%d tokens=%d", len(r.Reservations), control.ReservedTokens(r))
	}
	for _, j := range r.Jobs {
		if j.Status != control.JobSucceeded || j.Attempts != 1 {
			t.Fatalf("sequential writer/DAG failed: %+v", j)
		}
	}
	sqliteAssertReceipt(t, h, r)
	if h.sender.count() != 1 {
		t.Fatalf("delivery calls=%d", h.sender.count())
	}
	before, _ := json.Marshal(r)
	h.reopen(t)
	after, _ := json.Marshal(h.get(t, r.ID))
	if string(before) != string(after) {
		t.Fatal("JSON changed across Store reopen")
	}
	h.step(t, "after-restart")
	if h.sender.count() != 1 {
		t.Fatal("restart redelivered finished work")
	}
}

func TestSQLiteIntegrationDeliveryViaDurableBus(t *testing.T) {
	h := newSQLiteHarness(t)
	h.engine.DeliveryViaBus = true
	sub := eventbus.Subscription{Scope: control.LocalScope, Name: "fixture-delivery", Topics: []string{control.EventDeliveryReady}, Start: eventbus.StartBeginning, MaxAttempts: 3}
	if err := h.store.RegisterSubscription(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	r := h.start(t, "bus", h.grant())
	h.step(t, "worker")
	h.step(t, "verifier")
	r = h.get(t, r.ID)
	if r.Status != control.DeliveryPending || h.sender.count() != 0 {
		t.Fatalf("premature delivery: status=%s calls=%d", r.Status, h.sender.count())
	}
	h.step(t, "poller")
	if h.sender.count() != 0 {
		t.Fatal("production polling path sent despite DeliveryViaBus")
	}
	before, _ := json.Marshal(h.get(t, r.ID))
	h.reopen(t)
	after, _ := json.Marshal(h.get(t, r.ID))
	if string(before) != string(after) {
		t.Fatal("pending root JSON changed across independent Store reopen")
	}
	delivery, ok, err := h.store.ClaimEvent(context.Background(), sub.Scope, sub.Name, "subscriber", time.Second)
	if err != nil || !ok {
		t.Fatalf("claim delivery event: %v %v", ok, err)
	}
	var payload struct {
		RunID string `json:"run_id"`
	}
	if err = json.Unmarshal(delivery.Event.Payload, &payload); err != nil || payload.RunID != r.ID {
		t.Fatal("event did not identify durable root")
	}
	terminal, err := h.engine.DeliverRun(context.Background(), sub.Scope, payload.RunID, "bus-worker")
	if err != nil || !terminal {
		persisted := h.get(t, r.ID)
		t.Fatalf("DeliverRun: terminal=%v err=%v sender_calls=%d root=%s notice=%s receipt_persisted=%v", terminal, err, h.sender.count(), persisted.Status, persisted.Notices[0].Status, persisted.Notices[0].Receipt != nil)
	}
	if err = h.store.AckEvent(context.Background(), delivery.Token); err != nil {
		t.Fatal(err)
	}
	sqliteAssertReceipt(t, h, h.get(t, r.ID))
	h.reopen(t)
	if _, ok, err = h.store.ClaimEvent(context.Background(), sub.Scope, sub.Name, "subscriber2", time.Second); err != nil || ok {
		t.Fatalf("ACK not durable: %v %v", ok, err)
	}
	if terminal, err = h.engine.DeliverRun(context.Background(), r.Scope, r.ID, "replayed-bus-call"); err != nil || !terminal || h.sender.count() != 1 {
		t.Fatalf("bus replay resent: %v %v calls=%d", terminal, err, h.sender.count())
	}
}

func TestSQLiteIntegrationConcurrentRootBudgetAcrossStores(t *testing.T) {
	h := newSQLiteHarness(t)
	g := h.grant(sqliteNode("one", nil, nil, nil), sqliteNode("two", nil, nil, nil))
	g.MaxTokens = 150
	reservations := make(chan error, 2)
	release := make(chan struct{})
	var requestAdmissions atomic.Int32
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		err := r.Budget.Reserve(ctx, r.AttemptID+".request", 90, 10)
		if err == nil {
			requestAdmissions.Add(1)
		}
		reservations <- err
		select {
		case <-release:
		case <-ctx.Done():
			return execution.Result{}, ctx.Err()
		}
		return execution.Result{UsageKnown: true}, err
	})
	r := h.start(t, "budget", g)
	second, err := sqlitestore.Open(context.Background(), h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	other := *h.engine
	other.Store = second
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { _, err := h.engine.Step(ctx, "worker-one"); done <- err }()
	go func() { _, err := other.Step(ctx, "worker-two"); done <- err }()
	admitted, denied := 0, 0
	for i := 0; i < 2; i++ {
		select {
		case err := <-reservations:
			if err == nil {
				admitted++
			} else if errors.Is(err, execution.ErrBudget) {
				denied++
			} else {
				cancel()
				t.Fatalf("wrong reservation error: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("budget workers did not overlap")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	r = h.get(t, r.ID)
	if admitted != 1 || denied != 1 || requestAdmissions.Load() != 1 || len(r.Reservations) != 1 || control.ReservedTokens(r) != 100 {
		t.Fatalf("root budget admission failed: accepted=%d denied=%d reservations=%d tokens=%d", admitted, denied, len(r.Reservations), control.ReservedTokens(r))
	}
	h.reopen(t)
	if control.ReservedTokens(h.get(t, r.ID)) != 100 {
		t.Fatal("unsettled reservation lost on restart")
	}
}

func TestSQLiteIntegrationApprovalReplayAndConcurrentCancel(t *testing.T) {
	h := newSQLiteHarness(t)
	g := h.grant()
	g.AutoApprove = false
	r := h.start(t, "approval", g)
	if _, err := h.engine.Approve(context.Background(), control.Principal{Scope: control.LocalScope, Owner: "other", LocalAdmin: true}, r.ID, r.Version, r.GrantHash); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("foreign owner approval: %v", err)
	}
	approved, err := h.engine.Approve(context.Background(), h.principal, r.ID, r.Version, r.GrantHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.engine.Approve(context.Background(), h.principal, r.ID, r.Version, r.GrantHash); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("approval replay: %v", err)
	}
	if _, err = h.engine.Cancel(context.Background(), h.principal, r.ID, approved.Version); err != nil {
		t.Fatal(err)
	}
	r = h.start(t, "approval-race", g)
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, err := h.engine.Approve(context.Background(), h.principal, r.ID, r.Version, r.GrantHash)
		done <- err
	}()
	go func() {
		<-start
		_, err := h.engine.Cancel(context.Background(), h.principal, r.ID, r.Version)
		done <- err
	}()
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-done
		if err == nil {
			wins++
		} else if errors.Is(err, control.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	current := h.get(t, r.ID)
	if wins != 1 || conflicts != 1 || (current.Status != control.Queued && current.Status != control.Cancelled) {
		t.Fatalf("approval/cancel CAS: wins=%d conflicts=%d status=%s", wins, conflicts, current.Status)
	}
	if _, err = h.engine.Approve(context.Background(), h.principal, r.ID, r.Version, r.GrantHash); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("raced approval replay: %v", err)
	}
	if h.sender.count() != 0 {
		t.Fatal("approval caused delivery")
	}
}

func TestSQLiteIntegrationCancellationBlocksInFlightAuthority(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "engine"
		if legacy {
			name = "legacy-intake"
		}
		t.Run(name, func(t *testing.T) {
			h := newSQLiteHarness(t)
			started := make(chan execution.Request, 1)
			var calls atomic.Int32
			h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
				calls.Add(1)
				started <- r
				<-ctx.Done()
				return execution.Result{UsageKnown: true}, ctx.Err()
			})
			r := h.start(t, "cancel", h.grant())
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() { _, err := h.engine.Step(ctx, "running-worker"); done <- err }()
			var req execution.Request
			select {
			case req = <-started:
			case <-ctx.Done():
				t.Fatal("worker never started")
			}
			if legacy {
				if _, err := workorder.NewService(h.store, nil).Cancel(context.Background(), r.IntakeID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := h.engine.Cancel(context.Background(), h.principal, r.ID, 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := req.CheckAuthority(context.Background()); !errors.Is(err, control.ErrDenied) {
				t.Fatalf("cancelled authority remained live: %v", err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cancellation watcher did not stop executor")
			}
			current := h.get(t, r.ID)
			if !control.IsTerminal(current.Status) {
				t.Fatalf("cancel did not settle: %s", current.Status)
			}
			if !legacy && current.Status != control.Cancelled {
				t.Fatalf("explicit cancellation=%s", current.Status)
			}
			if _, err := os.Stat(filepath.Join(h.workspace, "result.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cancelled fixture wrote artifact")
			}
			h.reopen(t)
			h.step(t, "after-cancel")
			if calls.Load() != 1 || h.sender.count() != 0 {
				t.Fatal("cancelled work was repeated or delivered")
			}
		})
	}
}

func TestSQLiteIntegrationTimeoutAndUnknownDoNotAutoRetry(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "timeout"
		if unknown {
			name = "unknown-usage"
		}
		t.Run(name, func(t *testing.T) {
			h := newSQLiteHarness(t)
			g := h.grant()
			g.TimeoutMS = 1000
			var calls atomic.Int32
			h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
				calls.Add(1)
				if err := r.Budget.Reserve(ctx, r.AttemptID+".request", 90, 10); err != nil {
					return execution.Result{}, err
				}
				if unknown {
					return execution.Result{UsageKnown: false}, errors.New("fixture unknown request outcome")
				}
				<-ctx.Done()
				return execution.Result{UsageKnown: true}, ctx.Err()
			})
			r := h.start(t, "stopped", g)
			h.step(t, "worker")
			r = h.get(t, r.ID)
			want := control.Failed
			if unknown {
				want = control.NeedsAttention
			}
			if r.Status != want || control.ReservedTokens(r) != 100 || len(r.Reservations) != 1 || r.Reservations[0].Settled {
				t.Fatalf("unsafe timeout/unknown settlement: status=%s budget=%d", r.Status, control.ReservedTokens(r))
			}
			h.reopen(t)
			for i := 0; i < 3; i++ {
				h.step(t, "recovery")
			}
			if calls.Load() != 1 || h.sender.count() != 0 {
				t.Fatal("uncertain outcome was blindly redone")
			}
		})
	}
}

func TestSQLiteIntegrationBusUnknownAndSafeRetry(t *testing.T) {
	for _, retryable := range []bool{false, true} {
		name := "unknown"
		if retryable {
			name = "definitely-not-accepted"
		}
		t.Run(name, func(t *testing.T) {
			h := newSQLiteHarness(t)
			h.engine.DeliveryViaBus = true
			if retryable {
				h.sender.fail = &control.SendError{Code: "throttled", Retryable: true, DefinitelyNotAccepted: true}
			} else {
				h.sender.fail = context.DeadlineExceeded
			}
			sub := eventbus.Subscription{Scope: control.LocalScope, Name: "delivery", Topics: []string{control.EventDeliveryReady}, Start: eventbus.StartBeginning, MaxAttempts: 3}
			if err := h.store.RegisterSubscription(context.Background(), sub); err != nil {
				t.Fatal(err)
			}
			r := h.start(t, "delivery-error", h.grant())
			h.step(t, "worker")
			h.step(t, "verifier")
			d, ok, err := h.store.ClaimEvent(context.Background(), sub.Scope, sub.Name, "subscriber", time.Second)
			if err != nil || !ok {
				t.Fatalf("bus claim: %v %v", ok, err)
			}
			terminal, err := h.engine.DeliverRun(context.Background(), r.Scope, r.ID, "sender")
			if err != nil {
				t.Fatal(err)
			}
			r = h.get(t, r.ID)
			if retryable {
				if terminal || r.Notices[0].Status != control.DeliveryRetry || h.sender.count() != 1 {
					t.Fatalf("safe retry not durable: terminal=%v notice=%s", terminal, r.Notices[0].Status)
				}
				// The root is still live/delivery_pending here, so this specifically
				// proves the notice status gate, not only the terminal-root gate.
				if err = h.store.CheckClaim(context.Background(), r.Notices[0].Claim); !errors.Is(err, control.ErrDenied) {
					t.Fatalf("retryable settled claim retained authority: %v", err)
				}
				if err = h.store.NackEvent(context.Background(), d.Token, eventbus.CodeConsumerFailed, 0); err != nil {
					t.Fatal(err)
				}
				h.reopen(t)
				h.sender.mu.Lock()
				h.sender.fail = nil
				h.sender.mu.Unlock()
				h.engine.Now = func() time.Time { return time.Now().Add(2 * time.Second) }
				d, ok, err = h.store.ClaimEvent(context.Background(), sub.Scope, sub.Name, "retry-subscriber", time.Second)
				if err != nil || !ok || d.Attempt != 2 {
					t.Fatalf("retry claim: %v %v attempt=%d", ok, err, d.Attempt)
				}
				terminal, err = h.engine.DeliverRun(context.Background(), r.Scope, r.ID, "retry-sender")
				if err != nil || !terminal {
					t.Fatalf("retry did not settle: %v %v", terminal, err)
				}
				sqliteAssertReceipt(t, h, h.get(t, r.ID))
				if h.sender.count() != 2 {
					t.Fatalf("retry calls=%d", h.sender.count())
				}
			} else {
				if !terminal || r.Status != control.NeedsAttention || r.Notices[0].Status != control.DeliveryUnknown {
					t.Fatalf("unknown outcome lost: terminal=%v status=%s", terminal, r.Status)
				}
				h.reopen(t)
				if done, err := h.engine.DeliverRun(context.Background(), r.Scope, r.ID, "no-blind-resend"); err != nil || !done || h.sender.count() != 1 {
					t.Fatalf("unknown outcome retried: %v %v", done, err)
				}
			}
			if err = h.store.AckEvent(context.Background(), d.Token); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSQLiteIntegrationVerifierRejectsStaleWriteReceipt(t *testing.T) {
	h := newSQLiteHarness(t)
	r := h.start(t, "stale-artifact", h.grant())
	h.step(t, "worker")
	// The content assertion still passes, but bytes no longer match the receipt
	// of this attempt. Independent acceptance must not bless those changed bytes.
	if err := os.WriteFile(filepath.Join(h.workspace, "result.txt"), []byte("fixture accepted, modified after worker completion"), 0600); err != nil {
		t.Fatal(err)
	}
	h.step(t, "verifier")
	r = h.get(t, r.ID)
	if r.Status != control.Queued || r.Verification == nil || r.Verification.Passed || len(r.Notices) != 0 || h.sender.count() != 0 {
		t.Fatalf("stale receipt accepted: status=%s report=%+v", r.Status, r.Verification)
	}
	h.reopen(t)
	h.step(t, "revision-worker")
	h.step(t, "revision-verifier")
	r = h.get(t, r.ID)
	if r.Status != control.DeliveryPending || r.Jobs[0].Attempts != 2 || r.Verification == nil || !r.Verification.Passed || h.sender.count() != 0 {
		t.Fatalf("fresh revision did not independently pass: status=%s attempts=%d", r.Status, r.Jobs[0].Attempts)
	}
}

func TestSQLiteIntegrationLostLeaseKeepsUnknownWriterFenced(t *testing.T) {
	h := newSQLiteHarness(t)
	started := make(chan execution.Request, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		if calls.Add(1) > 1 {
			return execution.Result{UsageKnown: true}, errors.New("unexpected writer reacquisition")
		}
		if err := r.Budget.Reserve(ctx, r.AttemptID+".request", 90, 10); err != nil {
			return execution.Result{}, err
		}
		started <- r
		select {
		case <-release:
		case <-ctx.Done():
			<-release
		}
		return execution.Result{UsageKnown: false}, errors.New("fixture owner stopped after loss of authority")
	})
	r := h.start(t, "lost-lease", h.grant())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.engine.Step(ctx, "old-owner"); done <- err }()
	var req execution.Request
	select {
	case req = <-started:
	case <-ctx.Done():
		close(release)
		t.Fatal("old worker did not start")
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		cancel()
	})
	recovery := *h.engine
	recovery.Now = func() time.Time { return time.Now().Add(3 * time.Second) }
	if _, err := recovery.Step(context.Background(), "recovery"); err != nil {
		t.Fatal(err)
	}
	current := h.get(t, r.ID)
	if current.Status != control.NeedsAttention || current.Jobs[0].Status != control.JobUnknown || control.ReservedTokens(current) != 100 {
		t.Fatalf("lease loss not durable: status=%s job=%s", current.Status, current.Jobs[0].Status)
	}
	if err := req.CheckAuthority(context.Background()); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("stale owner authority: %v", err)
	}
	successor := h.start(t, "successor", h.grant())
	if worked, err := recovery.Step(context.Background(), "replacement"); err != nil || worked || calls.Load() != 1 {
		t.Fatalf("unknown writer replaced: worked=%v calls=%d err=%v", worked, calls.Load(), err)
	}
	close(release)
	released = true
	select {
	case err := <-done:
		if !errors.Is(err, control.ErrConflict) {
			t.Fatalf("stale settlement accepted: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("old worker did not stop")
	}
	h.reopen(t)
	if worked, err := h.engine.Step(context.Background(), "after-restart"); err != nil || worked || calls.Load() != 1 {
		t.Fatalf("restart lost unknown writer fence: %v %v calls=%d", worked, err, calls.Load())
	}
	if h.get(t, successor.ID).Jobs[0].Attempts != 0 {
		t.Fatal("blocked replacement consumed an execution attempt")
	}
}

func TestSQLiteIntegrationRevokedAuthorityNeverExecutes(t *testing.T) {
	h := newSQLiteHarness(t)
	var calls atomic.Int32
	h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
		calls.Add(1)
		return sqliteWork(ctx, r)
	})
	r := h.start(t, "revoked", h.grant())
	h.authority.revoked.Store(true)
	h.step(t, "revocation-check")
	if got := h.get(t, r.ID); got.Status != control.Cancelled || calls.Load() != 0 || h.sender.count() != 0 {
		t.Fatalf("revocation executed work: status=%s calls=%d", got.Status, calls.Load())
	}
	h.reopen(t)
	h.authority.revoked.Store(false)
	h.step(t, "after-reauthorization")
	if calls.Load() != 0 {
		t.Fatal("reauthorization resurrected cancelled work")
	}
}

func sqliteJobStates(r control.Run) map[string]control.JobStatus {
	out := make(map[string]control.JobStatus, len(r.Jobs))
	for _, j := range r.Jobs {
		out[j.Spec.ID] = j.Status
	}
	return out
}

func TestSQLiteIntegrationOperatorAbandonAfterInspection(t *testing.T) {
	t.Run("expired_unknown_writer_requires_exact_local_confirmation", func(t *testing.T) {
		h := newSQLiteHarness(t)
		ctx := context.Background()
		var executions atomic.Int32
		h.engine.Executor = sqliteExecutor(func(ctx context.Context, r execution.Request) (execution.Result, error) {
			executions.Add(1)
			return sqliteWork(ctx, r)
		})
		r := h.start(t, "operator-old-writer", h.grant())
		// Crash fixture: persist a real claim/resource lock and request reservation,
		// then simulate process death before execution. No old executor is running;
		// the stop confirmation below truthfully attests it has already stopped.
		claimed := r
		claimed.Version++
		claimed.Status = control.Running
		claimed.UpdatedAtMS = time.Now().UnixMilli()
		claim := control.Claim{Scope: r.Scope, RunID: r.ID, JobID: r.Jobs[0].Spec.ID, Owner: "crashed-fixture", Token: "crashed-fixture-claim", Epoch: 1, UntilMS: time.Now().Add(250 * time.Millisecond).UnixMilli()}
		claimed.Jobs[0].Status = control.JobRunning
		claimed.Jobs[0].Attempts = 1
		claimed.Jobs[0].Claim = claim
		claimed.Jobs[0].StartedAtMS = claimed.UpdatedAtMS
		claimed.Reservations = []control.Reservation{{ID: "uncertain-request", JobID: claim.JobID, AttemptID: claim.Token, Input: 90, Output: 10}}
		var err error
		r, err = h.store.SaveRun(ctx, r.Version, claimed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, r.Version, control.StopConfirmation); !errors.Is(err, control.ErrConflict) {
			t.Fatalf("non-needs_attention run abandoned: %v", err)
		}
		time.Sleep(time.Until(time.UnixMilli(claim.UntilMS)) + 5*time.Millisecond)
		if err = h.store.CheckClaim(ctx, claim); !errors.Is(err, control.ErrDenied) {
			t.Fatalf("expired claim remains authorized: %v", err)
		}
		h.step(t, "recovery")
		r = h.get(t, r.ID)
		if r.Status != control.NeedsAttention || r.Jobs[0].Status != control.JobUnknown || control.ReservedTokens(r) != 100 {
			t.Fatalf("crash recovery: status=%s jobs=%v budget=%d", r.Status, sqliteJobStates(r), control.ReservedTokens(r))
		}
		successor := h.start(t, "operator-successor", h.grant())
		h.reopen(t)
		assertBlocked := func() {
			t.Helper()
			worked, err := h.engine.Step(ctx, "blocked-successor")
			if err != nil || worked || executions.Load() != 0 || h.get(t, successor.ID).Jobs[0].Attempts != 0 {
				t.Fatalf("unknown resource released without confirmed stop: worked=%v executions=%d err=%v", worked, executions.Load(), err)
			}
		}
		assertBlocked()
		before, _ := json.Marshal(h.get(t, r.ID))
		cases := []struct {
			name         string
			p            control.Principal
			version      uint64
			confirmation string
			want         error
		}{
			{"wrong-confirmation", h.principal, r.Version, "yes", control.ErrDenied},
			{"not-exact-confirmation", h.principal, r.Version, control.StopConfirmation + " ", control.ErrDenied},
			{"not-admin", control.Principal{Scope: control.LocalScope, Owner: control.LocalOwner}, r.Version, control.StopConfirmation, control.ErrDenied},
			{"other-scope", control.Principal{Scope: "external", Owner: control.LocalOwner, LocalAdmin: true}, r.Version, control.StopConfirmation, control.ErrDenied},
			{"other-owner", control.Principal{Scope: control.LocalScope, Owner: "other", LocalAdmin: true}, r.Version, control.StopConfirmation, control.ErrDenied},
			{"stale-version", h.principal, r.Version - 1, control.StopConfirmation, control.ErrConflict},
		}
		for _, tc := range cases {
			if _, err = h.engine.AbandonAfterInspection(ctx, tc.p, r.ID, tc.version, tc.confirmation); !errors.Is(err, tc.want) {
				t.Fatalf("%s: %v", tc.name, err)
			}
			after, _ := json.Marshal(h.get(t, r.ID))
			if string(before) != string(after) {
				t.Fatalf("%s mutated aggregate", tc.name)
			}
			assertBlocked()
		}
		out, err := h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, r.Version, control.StopConfirmation)
		if err != nil {
			t.Fatal(err)
		}
		sqliteAssertOperatorResolution(t, r, out)
		if out.Jobs[0].Status != control.JobCancelled || out.Jobs[0].Claim != claim || control.ReservedTokens(out) != 100 || out.Reservations[0].Settled {
			t.Fatal("abandon lost writer provenance or refunded unknown request")
		}
		if err = h.store.CheckClaim(ctx, claim); !errors.Is(err, control.ErrDenied) {
			t.Fatalf("operator cancellation revived old claim: %v", err)
		}
		h.reopen(t)
		persisted := h.get(t, r.ID)
		sqliteAssertOperatorResolution(t, r, persisted)
		if control.ReservedTokens(persisted) != 100 || persisted.Reservations[0].Settled {
			t.Fatal("restart lost unknown budget")
		}
		for _, version := range []uint64{r.Version, persisted.Version} {
			if _, err = h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, version, control.StopConfirmation); !errors.Is(err, control.ErrConflict) {
				t.Fatalf("operator resolution replay: %v", err)
			}
		}
		worked, err := h.engine.Step(ctx, "replacement-after-confirmation")
		if err != nil || !worked || executions.Load() != 1 || h.get(t, successor.ID).Jobs[0].Status != control.JobSucceeded {
			t.Fatalf("confirmed stop did not release global writer lock: worked=%v executions=%d err=%v", worked, executions.Load(), err)
		}
		if h.sender.count() != 0 || h.get(t, r.ID).Jobs[0].Attempts != 1 {
			t.Fatal("operator abandon retried or delivered abandoned work")
		}
	})
	t.Run("unknown_delivery_remains_unknown_without_refund_or_resend", func(t *testing.T) {
		h := newSQLiteHarness(t)
		ctx := context.Background()
		h.engine.DeliveryViaBus = true
		h.sender.fail = context.DeadlineExceeded
		r := h.start(t, "operator-unknown-delivery", h.grant())
		h.step(t, "worker")
		h.step(t, "verifier")
		terminal, err := h.engine.DeliverRun(ctx, r.Scope, r.ID, "uncertain-sender")
		if err != nil || !terminal {
			t.Fatalf("unknown delivery fixture: %v %v", terminal, err)
		}
		r = h.get(t, r.ID)
		if r.Status != control.NeedsAttention || len(r.Notices) != 1 || r.Notices[0].Status != control.DeliveryUnknown || r.Notices[0].Receipt != nil || h.sender.count() != 1 {
			t.Fatalf("delivery did not persist uncertainty: status=%s calls=%d", r.Status, h.sender.count())
		}
		noticesBefore, _ := json.Marshal(r.Notices)
		budgetBefore, _ := json.Marshal(r.Reservations)
		tokensBefore := control.ReservedTokens(r)
		// Send has returned: no sender remains running before the explicit attestation.
		out, err := h.engine.AbandonAfterInspection(ctx, h.principal, r.ID, r.Version, control.StopConfirmation)
		if err != nil {
			t.Fatal(err)
		}
		sqliteAssertOperatorResolution(t, r, out)
		h.reopen(t)
		out = h.get(t, r.ID)
		sqliteAssertOperatorResolution(t, r, out)
		noticesAfter, _ := json.Marshal(out.Notices)
		budgetAfter, _ := json.Marshal(out.Reservations)
		if string(noticesBefore) != string(noticesAfter) || string(budgetBefore) != string(budgetAfter) || control.ReservedTokens(out) != tokensBefore {
			t.Fatal("operator abandonment rewrote unknown delivery or budget history")
		}
		if out.Notices[0].Status != control.DeliveryUnknown || out.Notices[0].Receipt != nil {
			t.Fatal("operator abandonment manufactured successful delivery")
		}
		h.step(t, "post-abandon-worker")
		if done, err := h.engine.DeliverRun(ctx, out.Scope, out.ID, "post-abandon-sender"); err != nil || !done || h.sender.count() != 1 {
			t.Fatalf("abandoned unknown delivery resent: done=%v calls=%d err=%v", done, h.sender.count(), err)
		}
		if _, err = h.engine.Retry(ctx, h.principal, out.ID, out.Version); !errors.Is(err, control.ErrConflict) {
			t.Fatalf("abandoned run retried: %v", err)
		}
	})
}

func sqliteAssertOperatorResolution(t *testing.T, old, next control.Run) {
	t.Helper()
	if next.Status != control.Cancelled || next.Version != old.Version+1 || next.Resolution == nil || next.Resolution.Kind != control.ResolutionAbandoned || next.Resolution.Actor != control.LocalOwner || next.Resolution.PreviousVersion != old.Version || next.Resolution.AtMS < old.UpdatedAtMS || !control.ValidStopResolution(old, next) {
		t.Fatalf("invalid durable stop resolution: status=%s resolution=%+v", next.Status, next.Resolution)
	}
	oldBudget, _ := json.Marshal(old.Reservations)
	newBudget, _ := json.Marshal(next.Reservations)
	if string(oldBudget) != string(newBudget) {
		t.Fatal("operator resolution altered budget reservations")
	}
}
