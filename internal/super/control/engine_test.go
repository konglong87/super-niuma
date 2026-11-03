package control

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

type memoryStore struct {
	mu   sync.Mutex
	runs map[string]Run
}

func cloneRun(r Run) Run {
	b, _ := json.Marshal(r)
	var out Run
	_ = json.Unmarshal(b, &out)
	return out
}
func testKey(s, id string) string { return s + "/" + id }
func (m *memoryStore) CreateRun(ctx context.Context, r Run, in *Ingress) (Run, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runs == nil {
		m.runs = map[string]Run{}
	}
	for _, v := range m.runs {
		if v.Scope == r.Scope && v.RequestKey == r.RequestKey {
			if !SameIntent(v, r) {
				return Run{}, false, ErrConflict
			}
			return cloneRun(v), false, nil
		}
	}
	m.runs[testKey(r.Scope, r.ID)] = cloneRun(r)
	return r, true, nil
}
func (m *memoryStore) GetRun(ctx context.Context, scope, id string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[testKey(scope, id)]
	if !ok {
		return Run{}, ErrNotFound
	}
	return cloneRun(r), nil
}
func (m *memoryStore) ListRuns(ctx context.Context, scope string) ([]Run, error) {
	all, _ := m.ActiveRuns(ctx)
	out := []Run{}
	for _, r := range all {
		if r.Scope == scope {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *memoryStore) ActiveRuns(context.Context) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Run{}
	for _, r := range m.runs {
		out = append(out, cloneRun(r))
	}
	return out, nil
}
func (m *memoryStore) SaveRun(ctx context.Context, v uint64, n Run) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.runs[testKey(n.Scope, n.ID)]
	if !ok {
		return Run{}, ErrNotFound
	}
	if old.Version != v {
		return Run{}, ErrConflict
	}
	if !SameIntent(old, n) || n.Version != v+1 {
		return Run{}, ErrInvalid
	}
	if err := ValidateRun(n); err != nil {
		return Run{}, err
	}
	m.runs[testKey(n.Scope, n.ID)] = cloneRun(n)
	return n, nil
}
func (m *memoryStore) CheckClaim(ctx context.Context, c Claim) error {
	r, err := m.GetRun(ctx, c.Scope, c.RunID)
	if err != nil {
		return err
	}
	if IsTerminal(r.Status) || r.Status == Cancelling {
		return ErrDenied
	}
	var got Claim
	if c.JobID == verificationJobID {
		got = r.VerificationClaim
	} else if i := jobIndex(r, c.JobID); i >= 0 {
		got = r.Jobs[i].Claim
	} else {
		for _, n := range r.Notices {
			if n.Claim.JobID == c.JobID {
				got = n.Claim
			}
		}
	}
	if !claimEqual(got, c) || got.UntilMS <= time.Now().UnixMilli() {
		return ErrDenied
	}
	return nil
}

type allowAuthority struct{}

func (allowAuthority) Current(context.Context, Principal, Grant) error { return nil }

type fakeExecutor struct {
	fn func(context.Context, execution.Request) (execution.Result, error)
}

func (f fakeExecutor) Execute(c context.Context, r execution.Request) (execution.Result, error) {
	if f.fn != nil {
		return f.fn(c, r)
	}
	return execution.Result{Text: "fixture", UsageKnown: true}, nil
}

type fakeVerifier struct{ report verification.Report }

func (f fakeVerifier) Verify(context.Context, string, verification.Policy) (verification.Report, error) {
	return f.report, nil
}
func testGrant(t *testing.T) Grant {
	t.Helper()
	return Grant{ProfileID: "p", Revision: "v1", Workspace: canonicalTemp(t), Model: "fixture-model", MaxTokens: 10000, TimeoutMS: 10000, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), AutoApprove: true, Nodes: []Node{{ID: "work", Instruction: "fixture", MaxTurns: 2, MaxOutputTokens: 100, WritePaths: []string{"artifacts"}}}, Verification: verification.Policy{Version: "1", Rules: []verification.Rule{{Path: "artifacts/result.txt", Kind: "text_contains", Expected: "verified"}}}, Targets: []Target{{ID: "desktop", Provider: ProviderDesktop}}}
}
func testEngine() *Engine {
	return &Engine{Store: &memoryStore{}, Executor: fakeExecutor{}, Authority: allowAuthority{}, Verifier: fakeVerifier{}, Sender: DesktopSender{}}
}
func TestAdmissionApprovalIsolationAndReplay(t *testing.T) {
	ctx := context.Background()
	e := testEngine()
	g := testGrant(t)
	g.AutoApprove = false
	p := Principal{Scope: "local", Owner: "local", LocalAdmin: true}
	r, created, err := e.Start(ctx, p, "key", "objective", "", g, nil)
	if err != nil || !created || r.Status != AwaitingApproval {
		t.Fatalf("start %v %v", r.Status, err)
	}
	again, newer, err := e.Start(ctx, p, "key", "objective", "", g, nil)
	if err != nil || newer || again.ID != r.ID {
		t.Fatalf("retry %v", err)
	}
	if _, err = e.Get(ctx, Principal{Scope: "local", Owner: "other"}, r.ID); !errors.Is(err, ErrDenied) {
		t.Fatalf("other owner read: %v", err)
	}
	if _, err = e.Approve(ctx, Principal{Scope: "local", Owner: "local"}, r.ID, r.Version, r.GrantHash); !errors.Is(err, ErrDenied) {
		t.Fatalf("external approve: %v", err)
	}
	approved, err := e.Approve(ctx, p, r.ID, r.Version, r.GrantHash)
	if err != nil || !approved.Approved {
		t.Fatal(err)
	}
	if _, err = e.Approve(ctx, p, r.ID, r.Version, r.GrantHash); !errors.Is(err, ErrConflict) {
		t.Fatalf("replayed approval: %v", err)
	}
}
func TestTwoWorkersActuallyOverlapAndBudgetIsShared(t *testing.T) {
	ctx := context.Background()
	e := testEngine()
	g := testGrant(t)
	g.Nodes = []Node{{ID: "one", Instruction: "first", MaxTurns: 1, MaxOutputTokens: 10}, {ID: "two", Instruction: "second", MaxTurns: 1, MaxOutputTokens: 10}}
	g.MaxTokens = 150
	entered := make(chan string, 2)
	release := make(chan struct{})
	reservations := make(chan error, 2)
	e.Executor = fakeExecutor{fn: func(ctx context.Context, r execution.Request) (execution.Result, error) {
		reservations <- r.Budget.Reserve(ctx, r.AttemptID, 90, 10)
		entered <- r.JobID
		<-release
		return execution.Result{UsageKnown: true}, nil
	}}
	r, _, err := e.Start(ctx, Principal{Scope: "local", Owner: "local"}, "concurrent", "objective", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { _, err := e.Step(ctx, "w1"); done <- err }()
	go func() { _, err := e.Step(ctx, "w2"); done <- err }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("workers did not overlap")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	a, b := <-reservations, <-reservations
	if (a == nil) == (b == nil) {
		t.Fatalf("shared reservation accepted wrong count: %v %v", a, b)
	}
	current, _ := e.Store.GetRun(ctx, r.Scope, r.ID)
	if ReservedTokens(current) > g.MaxTokens {
		t.Fatal("budget overspent")
	}
}
func TestCancellationRevokesAuthorityBeforeWorkerReturns(t *testing.T) {
	e := testEngine()
	g := testGrant(t)
	started := make(chan struct{})
	stopped := make(chan error, 1)
	e.Executor = fakeExecutor{fn: func(ctx context.Context, r execution.Request) (execution.Result, error) {
		close(started)
		<-ctx.Done()
		stopped <- r.CheckAuthority(context.Background())
		return execution.Result{UsageKnown: true}, ctx.Err()
	}}
	p := Principal{Scope: "local", Owner: "local"}
	r, _, err := e.Start(context.Background(), p, "cancel", "objective", "", g, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := e.Step(context.Background(), "worker"); done <- err }()
	<-started
	if _, err = e.Cancel(context.Background(), p, r.ID, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err == nil {
			t.Fatal("cancel did not revoke")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation watcher did not stop worker")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	r, _ = e.Get(context.Background(), p, r.ID)
	if r.Status != Cancelled {
		t.Fatalf("state %s", r.Status)
	}
}
func TestNoSelfCertifiedOrStaleArtifacts(t *testing.T) {
	g := testGrant(t)
	r := Run{Grant: g, Jobs: []Job{{Status: JobSucceeded, Claim: Claim{Token: "current"}, Result: execution.Result{Writes: []execution.WriteReceipt{{Path: "artifacts/result.txt", SHA256: "digest", Bytes: 8, AttemptID: "old"}}}}}}
	report := verification.Report{Passed: true, Artifacts: []verification.Artifact{{Path: "artifacts/result.txt", SHA256: "digest", Bytes: 8}}}
	if freshArtifacts(r, report) {
		t.Fatal("old attempt accepted")
	}
	r.Jobs[0].Result.Writes[0].AttemptID = "current"
	if !freshArtifacts(r, report) {
		t.Fatal("current trusted write rejected")
	}
	report.Artifacts[0].SHA256 = "tampered"
	if freshArtifacts(r, report) {
		t.Fatal("changed snapshot accepted")
	}
}
func TestGrantRejectsCyclesAndPathEscape(t *testing.T) {
	g := testGrant(t)
	g.Nodes[0].DependsOn = []string{"work"}
	if ValidateGrant(g) == nil {
		t.Fatal("cycle accepted")
	}
	g.Nodes[0].DependsOn = nil
	g.Nodes[0].WritePaths = []string{"../escape"}
	if ValidateGrant(g) == nil {
		t.Fatal("escape accepted")
	}
}

func canonicalTemp(t *testing.T) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
