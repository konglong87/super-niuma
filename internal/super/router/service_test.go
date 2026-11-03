package router

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/konglong87/super-niuma/internal/super/control"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu             sync.Mutex
	runs           map[string]control.Run
	keys           map[string]string
	receipts       map[string]CommandReceipt
	creates, saves int
}

func freshStore() *memoryStore {
	return &memoryStore{runs: map[string]control.Run{}, keys: map[string]string{}, receipts: map[string]CommandReceipt{}}
}
func cloneRun(r control.Run) control.Run {
	data, _ := json.Marshal(r)
	var out control.Run
	_ = json.Unmarshal(data, &out)
	return out
}
func (m *memoryStore) CreateRun(_ context.Context, r control.Run, in *control.Ingress) (control.Run, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates++
	key := r.Scope + ":" + r.RequestKey
	if id, ok := m.keys[key]; ok {
		return cloneRun(m.runs[id]), false, nil
	}
	m.keys[key] = r.ID
	m.runs[r.ID] = cloneRun(r)
	return r, true, nil
}
func (m *memoryStore) GetRun(_ context.Context, scope, id string) (control.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok || r.Scope != scope {
		return control.Run{}, control.ErrNotFound
	}
	return cloneRun(r), nil
}
func (m *memoryStore) ListRuns(_ context.Context, scope string) ([]control.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []control.Run
	for _, r := range m.runs {
		if r.Scope == scope {
			out = append(out, cloneRun(r))
		}
	}
	return out, nil
}
func (m *memoryStore) ActiveRuns(context.Context) ([]control.Run, error) { return nil, nil }
func (m *memoryStore) SaveRun(_ context.Context, version uint64, r control.Run) (control.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.runs[r.ID]
	if !ok || old.Version != version {
		return control.Run{}, control.ErrConflict
	}
	m.saves++
	m.runs[r.ID] = cloneRun(r)
	return r, nil
}
func (m *memoryStore) CheckClaim(context.Context, control.Claim) error { return nil }
func (m *memoryStore) BeginCommand(_ context.Context, q CommandRequest) (CommandReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := q.Ingress.Account + ":" + q.Ingress.MessageID
	if r, ok := m.receipts[key]; ok {
		if r.Request.Ingress.ContentHash != q.Ingress.ContentHash {
			return CommandReceipt{}, control.ErrConflict
		}
		return r, nil
	}
	r := CommandReceipt{Request: q, Version: q.ExpectedVersion, Status: m.runs[q.RunID].Status}
	m.receipts[key] = r
	return r, nil
}
func (m *memoryStore) FinishCommand(_ context.Context, r CommandReceipt, d Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.Decision = &d
	m.receipts[r.Request.Ingress.Account+":"+r.Request.Ingress.MessageID] = r
	return nil
}

type fakeExecutor struct{}

func (fakeExecutor) Execute(context.Context, execution.Request) (execution.Result, error) {
	panic("router must never execute a model")
}

type fakeAuthority struct{}

func (fakeAuthority) Current(context.Context, control.Principal, control.Grant) error { return nil }

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, string, verification.Policy) (verification.Report, error) {
	panic("router must never verify")
}

type fakeSender struct{}

func (fakeSender) Send(context.Context, control.Target, string, string) (control.Receipt, error) {
	panic("router must never send directly")
}
func fixture(t *testing.T) (*Service, *memoryStore, *[]Binding, Envelope) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := control.Grant{ProfileID: "profile", Revision: "v1", Workspace: root, Provider: "test", Model: "test", MaxTokens: 1000, TimeoutMS: 1000, ExpiresAtMS: time.Now().Add(time.Hour).UnixMilli(), Nodes: []control.Node{{ID: "job", Instruction: "bounded", MaxTurns: 1, MaxOutputTokens: 32}}, Verification: verification.Policy{Version: "v1", Rules: []verification.Rule{{Path: "result.txt", Kind: verification.KindTextContains, Expected: "ok"}}}, Targets: []control.Target{{ID: "desktop", Provider: control.ProviderDesktop}}, AutoApprove: true}
	m := freshStore()
	e := &control.Engine{Store: m, Executor: fakeExecutor{}, Authority: fakeAuthority{}, Verifier: fakeVerifier{}, Sender: fakeSender{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	bindings := []Binding{{ID: "binding", Provider: control.ProviderWebhook, Account: "account", Chat: "chat", Sender: "sender", Scope: "scope", Owner: "owner", ProfileID: "profile", Enabled: true}}
	s := New(e, func() []Binding { return bindings }, func(_ context.Context, p control.Principal, id string) (control.Grant, error) {
		if p.LocalAdmin || p.Owner != "owner" || id != "profile" {
			return control.Grant{}, control.ErrDenied
		}
		return g, nil
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	envelope := Envelope{Provider: control.ProviderWebhook, Account: "account", EventID: "event", MessageID: "message", Chat: "chat", Sender: "sender", Text: "/run private objective", Authenticated: true, ThreadKnown: true}
	return s, m, &bindings, envelope
}
func TestRouterAdmissionAndSafeResponses(t *testing.T) {
	s, m, _, env := fixture(t)
	d, err := s.Handle(context.Background(), env)
	if err != nil || d.Action != ActionRun || d.Code != CodeAccepted || d.RunID == "" || d.UsedLLM {
		t.Fatalf("%+v %v", d, err)
	}
	data, _ := json.Marshal(d)
	if string(data) == "" || len(m.runs) != 1 {
		t.Fatal("missing durable run")
	}
	again, err := s.Handle(context.Background(), env)
	if err != nil || again != d || len(m.runs) != 1 {
		t.Fatalf("replay %+v %v", again, err)
	}
	for _, r := range m.runs {
		if r.Owner != "owner" || r.Scope != "scope" || r.Status != control.Queued {
			t.Fatalf("authority=%+v", r)
		}
	}
}
func TestRouterFastGatesNeverInvokeGrantOrModel(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Envelope)
		code   string
	}{{"unauth", func(e *Envelope) { e.Authenticated = false }, CodeUnauthenticated}, {"bot", func(e *Envelope) { e.Bot = true }, CodeBot}, {"self", func(e *Envelope) { e.Self = true }, CodeBot}, {"thread unknown", func(e *Envelope) { e.ThreadKnown = false }, CodeThreadUnknown}, {"other sender", func(e *Envelope) { e.Sender = "attacker" }, CodeUnbound}, {"other thread", func(e *Envelope) { e.Thread = "foreign" }, CodeUnbound}, {"missing id", func(e *Envelope) { e.EventID = "" }, CodeInvalid}, {"natural", func(e *Envelope) { e.Text = "please do something" }, CodeClarification}, {"approve", func(e *Envelope) { e.Text = "/approve run" }, CodeCommand}, {"link", func(e *Envelope) { e.Text = "/link run" }, CodeCommand}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, m, _, e := fixture(t)
			s.grant = func(context.Context, control.Principal, string) (control.Grant, error) {
				t.Fatal("grant on cheap gate")
				return control.Grant{}, nil
			}
			tc.change(&e)
			d, _ := s.Handle(context.Background(), e)
			if d.Code != tc.code || d.UsedLLM || m.creates != 0 || d.RunID != "" {
				t.Fatalf("%+v creates=%d", d, m.creates)
			}
		})
	}
}
func TestBindingAmbiguityWildcardAndMentionFailClosed(t *testing.T) {
	for _, kind := range []string{"duplicate", "wildcard", "scope owner", "mention", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			s, _, bs, e := fixture(t)
			switch kind {
			case "duplicate":
				*bs = append(*bs, (*bs)[0])
			case "wildcard":
				(*bs)[0].Sender = "*"
			case "scope owner":
				b := (*bs)[0]
				b.ID = "other"
				b.Sender = "other"
				b.Owner = "other"
				*bs = append(*bs, b)
			case "mention":
				(*bs)[0].RequireMention = true
			case "disabled":
				(*bs)[0].Enabled = false
			}
			d, err := s.Handle(context.Background(), e)
			if !errors.Is(err, control.ErrDenied) || d.RunID != "" {
				t.Fatalf("%+v %v", d, err)
			}
		})
	}
}
func TestStatusAndCancelHaveDurableStableReceipts(t *testing.T) {
	s, m, _, e := fixture(t)
	created, err := s.Handle(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	e.Text = "/status " + created.RunID
	e.EventID = "status-event"
	e.MessageID = "status-message"
	status, err := s.Handle(context.Background(), e)
	if err != nil || status.Code != "status_queued" {
		t.Fatalf("%+v %v", status, err)
	}
	cancel := e
	cancel.Text = "/cancel " + created.RunID
	cancel.EventID = "cancel-event"
	cancel.MessageID = "cancel-message"
	d, err := s.Handle(context.Background(), cancel)
	if err != nil || d.Code != CodeCancelled || m.saves != 1 {
		t.Fatalf("%+v %v saves=%d", d, err, m.saves)
	}
	for i := 0; i < 3; i++ {
		again, err := s.Handle(context.Background(), cancel)
		if err != nil || again != d || m.saves != 1 {
			t.Fatalf("cancel replay %+v %v", again, err)
		}
	}
	again, err := s.Handle(context.Background(), e)
	if err != nil || again != status {
		t.Fatalf("status receipt changed %+v %v", again, err)
	}
}
func TestCommandsCannotReadOrCancelAnotherOwner(t *testing.T) {
	s, m, _, e := fixture(t)
	d, err := s.Handle(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	r := m.runs[d.RunID]
	r.Owner = "another"
	m.runs[d.RunID] = r
	m.mu.Unlock()
	for _, cmd := range []string{"/status ", "/cancel "} {
		e.Text = cmd + d.RunID
		out, err := s.Handle(context.Background(), e)
		if err == nil || out.RunID != "" || m.saves != 0 {
			t.Fatalf("leak %+v %v", out, err)
		}
	}
}
func TestPendingCancelResumesOriginalRevision(t *testing.T) {
	s, m, bs, e := fixture(t)
	d, err := s.Handle(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	e.Text = "/cancel " + d.RunID
	e.EventID = "cancel-event"
	e.MessageID = "cancel-message"
	p := control.Principal{Scope: "scope", Owner: "owner"}
	q := CommandRequest{Principal: p, Ingress: ingress(e, (*bs)[0]), Action: ActionCancel, RunID: d.RunID, ExpectedVersion: 1}
	if _, err = m.BeginCommand(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if _, err = s.engine.Cancel(context.Background(), p, d.RunID, 1); err != nil {
		t.Fatal(err)
	}
	out, err := s.Handle(context.Background(), e)
	if err != nil || out.Code != CodeCancelled || m.saves != 1 {
		t.Fatalf("lost receipt %+v %v saves=%d", out, err, m.saves)
	}
}
func TestTrustedEnvelopeCannotBeJSONManufactured(t *testing.T) {
	var e Envelope
	if json.Unmarshal([]byte(`{"Authenticated":true,"ThreadKnown":true}`), &e) == nil || e.Authenticated {
		t.Fatal("JSON manufactured authority")
	}
}

func TestSourceBindingIsServerDerivedAndConfigurationIsSnakeCase(t *testing.T) {
	s, m, bindings, e := fixture(t)
	original := s.grant
	s.grant = func(ctx context.Context, p control.Principal, id string) (control.Grant, error) {
		g, err := original(ctx, p, id)
		g.SourceBindingID = "forged"
		g.SourceBindingHash = "forged"
		return g, err
	}
	d, err := s.Handle(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	r := m.runs[d.RunID]
	if r.Grant.SourceBindingID != (*bindings)[0].ID || r.Grant.SourceBindingHash != control.Digest((*bindings)[0]) {
		t.Fatal("source binding not derived from actual binding")
	}
	var b Binding
	if err = json.Unmarshal([]byte(`{"id":"binding","provider":"webhook","account":"account","chat":"chat","thread":"","sender":"sender","scope":"scope","owner":"owner","profile_id":"profile","enabled":true,"require_mention":true}`), &b); err != nil || b.ProfileID != "profile" || !b.RequireMention {
		t.Fatalf("binding tags %+v %v", b, err)
	}
}
