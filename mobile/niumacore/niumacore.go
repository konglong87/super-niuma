// Package niumacore is the Go core of the Super Niuma Android app. It is
// bound into the app with gomobile, so every exported function takes and
// returns plain strings (JSON) that the Kotlin side can pass through as is.
//
// This is the M0 feasibility slice: it proves the core runs on a phone. It
// reports the runtime, opens SQLite with the same driver Super uses, makes an
// unauthenticated HTTPS request (DNS, TLS and the system's root certificates)
// and runs one model attempt through Super's isolated execution adapter.
// Phone tools, policy and approvals come in later milestones.
package niumacore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/super"
	"github.com/konglong87/super-niuma/internal/super/execution"
	"github.com/konglong87/super-niuma/internal/super/verification"
)

const (
	routeName          = "mobile-m0"
	modelCallTimeout   = 120 * time.Second
	httpsProbeTimeout  = 20 * time.Second
	maxReportTextRunes = 500
	maxBudgetTokens    = 20000
)

// Version names this core build. It changes whenever the JSON contract does.
func Version() string { return "m0-2" }

var (
	initMu   sync.Mutex
	stateDir string
)

type initReport struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Init points Super's protected state directory at dir, an existing absolute
// directory private to the app. Android app processes have no HOME, so the
// default ~/.super-niuma cannot be resolved and execution refuses to run
// (fail closed). Call it once at startup; calling it again with the same dir
// is a no-op, and with another dir is refused.
func Init(dir string) string {
	initMu.Lock()
	defer initMu.Unlock()
	if stateDir != "" {
		if dir == stateDir {
			return encode(initReport{OK: true})
		}
		return encode(initReport{Error: "state directory already set"})
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return encode(initReport{Error: "state directory must be a clean absolute path"})
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return encode(initReport{Error: "state directory missing"})
	}
	if err := os.Setenv(super.StateDirectoryEnv, dir); err != nil {
		return encode(initReport{Error: "set state directory"})
	}
	stateDir = dir
	return encode(initReport{OK: true})
}

type probeReport struct {
	OK            bool   `json:"ok"`
	Core          string `json:"core"`
	GoVersion     string `json:"go_version"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	CPUs          int    `json:"cpus"`
	SQLiteVersion string `json:"sqlite_version,omitempty"`
	SQLiteMillis  int64  `json:"sqlite_ms,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Probe reports the Go runtime and checks that SQLite can create, write and
// read a database inside dataDir, which must be an existing absolute directory
// (the app's private files directory). The probe database is removed again.
func Probe(dataDir string) string {
	r := probeReport{Core: Version(), GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU()}
	start := time.Now()
	version, err := sqliteRoundTrip(dataDir)
	if err != nil {
		r.Error = "sqlite: " + err.Error()
		return encode(r)
	}
	r.OK, r.SQLiteVersion, r.SQLiteMillis = true, version, time.Since(start).Milliseconds()
	return encode(r)
}

func sqliteRoundTrip(dataDir string) (string, error) {
	if !filepath.IsAbs(dataDir) {
		return "", errors.New("data directory must be absolute")
	}
	if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
		return "", errors.New("data directory missing")
	}
	path := filepath.Join(dataDir, "niumacore-probe.sqlite3")
	_ = os.Remove(path)
	defer os.Remove(path)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE probe (id INTEGER PRIMARY KEY, note TEXT NOT NULL)`); err != nil {
		return "", err
	}
	if _, err := db.Exec(`INSERT INTO probe (note) VALUES (?)`, "niumacore"); err != nil {
		return "", err
	}
	var note, version string
	if err := db.QueryRow(`SELECT note, sqlite_version() FROM probe WHERE id = 1`).Scan(&note, &version); err != nil {
		return "", err
	}
	if note != "niumacore" {
		return "", errors.New("read back a different row")
	}
	return version, nil
}

type httpsReport struct {
	OK     bool   `json:"ok"`
	Status int    `json:"status,omitempty"`
	Millis int64  `json:"ms"`
	Error  string `json:"error,omitempty"`
}

// HTTPSProbe sends one GET without credentials to an https URL and reports
// the status. Any HTTP status, 401 included, proves DNS, TLS and the root
// certificates work; only a transport error is a failure.
func HTTPSProbe(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return encode(httpsReport{Error: "an https URL without credentials is required"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpsProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return encode(httpsReport{Error: err.Error()})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	resp, err := client.Do(req)
	r := httpsReport{Millis: time.Since(start).Milliseconds()}
	if err != nil {
		r.Error = err.Error()
		return encode(r)
	}
	resp.Body.Close()
	r.OK, r.Status = true, resp.StatusCode
	return encode(r)
}

// Route is the model endpoint for one attempt. Only an OpenAI-compatible
// chat completions route is accepted in M0. The token is a credential for
// that endpoint; the core never logs or reports it.
type route struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	Token   string `json:"token"`
}

type modelReport struct {
	OK           bool   `json:"ok"`
	Text         string `json:"text,omitempty"`
	InputTokens  int    `json:"input_tokens,omitempty"`
	OutputTokens int    `json:"output_tokens,omitempty"`
	UsageKnown   bool   `json:"usage_known"`
	Millis       int64  `json:"ms"`
	Code         string `json:"code,omitempty"`
	HTTPStatus   int    `json:"http_status,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ModelCall runs one Super execution attempt with no file tools: the model
// answers objective in text. routeJSON describes the endpoint (see route);
// workspace is the attempt's root and must be an existing, canonical absolute
// directory, as execution requires. Execution itself refuses plain http
// except to a literal loopback address and never retries or falls back.
func ModelCall(routeJSON, workspace, objective string) string {
	var rt route
	if err := json.Unmarshal([]byte(routeJSON), &rt); err != nil {
		return encode(modelReport{Error: "route is not valid JSON"})
	}
	if strings.TrimSpace(rt.BaseURL) == "" || strings.TrimSpace(rt.Model) == "" || rt.Token == "" {
		return encode(modelReport{Error: "route needs base_url, model and token"})
	}
	// Execution would refuse too, but only as revoked authority; say why.
	if _, err := super.StateDirectory(); err != nil {
		return encode(modelReport{Error: "Super state directory unavailable: call Init first"})
	}
	cfg := config.Config{
		Provider:         "openai",
		ProviderProtocol: config.ProviderProtocolOpenAIChatCompletions,
		BaseURL:          rt.BaseURL,
		APIKey:           rt.Token,
		SelectedProvider: routeName,
		Settings:         config.Settings{Model: rt.Model},
	}
	runner := execution.New(func(context.Context) (config.Config, error) { return cfg, nil }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), modelCallTimeout)
	defer cancel()
	attempt := fmt.Sprintf("attempt-%d", time.Now().UnixNano())
	start := time.Now()
	result, err := runner.Execute(ctx, execution.Request{
		RunID: "mobile-m0", JobID: "model-call", AttemptID: attempt,
		Workspace: workspace, Objective: objective,
		Instruction:  "Answer in plain text. No tools are available.",
		SystemPrompt: "You are the Super Niuma phone core running a feasibility check.",
		Provider:     routeName, Model: rt.Model,
		MaxTurns: 1, MaxOutputTokens: 512,
		CheckAuthority: func(ctx context.Context) error { return ctx.Err() },
		Budget:         &tokenBudget{limit: maxBudgetTokens},
	})
	r := modelReport{
		Millis:       time.Since(start).Milliseconds(),
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		UsageKnown:   result.UsageKnown,
	}
	if result.Diagnostic != nil {
		r.Code, r.HTTPStatus = string(result.Diagnostic.Code), result.Diagnostic.HTTPStatus
	}
	if err != nil {
		r.Error = execution.FailureCode(err)
		return encode(r)
	}
	r.OK, r.Text = true, truncateRunes(result.Text, maxReportTextRunes)
	return encode(r)
}

type verifyReport struct {
	OK     bool   `json:"ok"`
	Passed bool   `json:"passed"`
	Checks int    `json:"checks"`
	Millis int64  `json:"ms"`
	Error  string `json:"error,omitempty"`
}

// VerifyProbe writes one artifact into workspace and runs Super's
// independent file verifier on it, so the verifier is exercised with the
// app's own permissions (SELinux domain and uid), which adb shell does not
// share. The artifact is removed again.
func VerifyProbe(workspace string) string {
	dir := filepath.Join(workspace, "verify-probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return encode(verifyReport{Error: "create probe directory"})
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "artifact.txt"), []byte("phone verifier ok\n"), 0o600); err != nil {
		return encode(verifyReport{Error: "write probe artifact"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), httpsProbeTimeout)
	defer cancel()
	start := time.Now()
	report, err := verification.Verify(ctx, workspace, verification.Policy{Version: "mobile-probe-1", Rules: []verification.Rule{
		{Path: "verify-probe/artifact.txt", Kind: verification.KindTextContains, Expected: "verifier ok"},
	}})
	r := verifyReport{Millis: time.Since(start).Milliseconds(), Checks: len(report.Checks), Passed: report.Passed}
	if err != nil {
		r.Error = err.Error()
		return encode(r)
	}
	r.OK = report.Passed
	return encode(r)
}

// screenNode is one accessibility node as the app's service reports it.
// Parent is the index of the parent node, -1 for a window root.
type screenNode struct {
	Parent    int    `json:"parent"`
	Class     string `json:"cls"`
	Text      string `json:"text,omitempty"`
	Desc      string `json:"desc,omitempty"`
	ViewID    string `json:"rid,omitempty"`
	Bounds    [4]int `json:"b"`
	Clickable bool   `json:"click,omitempty"`
	Editable  bool   `json:"edit,omitempty"`
	Scrolls   bool   `json:"scroll,omitempty"`
}

type screenSnapshot struct {
	Package string       `json:"package"`
	Nodes   []screenNode `json:"nodes"`
}

type screenReport struct {
	OK          bool   `json:"ok"`
	Package     string `json:"package,omitempty"`
	Nodes       int    `json:"nodes"`
	Interactive int    `json:"interactive"`
	Labeled     int    `json:"labeled"`
	Micros      int64  `json:"parse_us"`
	Error       string `json:"error,omitempty"`
}

// ScreenSummary parses one screen snapshot from the app's accessibility
// service and counts what an agent could use: nodes it can act on and nodes
// with a label. In M0 it measures the hand-off from Kotlin to Go; later
// milestones build the agent's screen model here.
func ScreenSummary(snapshotJSON string) string {
	start := time.Now()
	var s screenSnapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &s); err != nil {
		return encode(screenReport{Error: "snapshot is not valid JSON"})
	}
	r := screenReport{OK: true, Package: s.Package, Nodes: len(s.Nodes)}
	for i, n := range s.Nodes {
		if n.Parent < -1 || n.Parent >= i {
			return encode(screenReport{Error: fmt.Sprintf("node %d has an invalid parent", i)})
		}
		if n.Clickable || n.Editable || n.Scrolls {
			r.Interactive++
		}
		if strings.TrimSpace(n.Text) != "" || strings.TrimSpace(n.Desc) != "" {
			r.Labeled++
		}
	}
	r.Micros = time.Since(start).Microseconds()
	return encode(r)
}

// tokenBudget reserves conservatively before every model request and refuses
// once the attempt could exceed limit tokens. Unknown usage keeps the
// reservation, as execution's Budget contract requires.
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

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func encode(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"ok":false,"error":"encode report"}`
	}
	return string(data)
}
