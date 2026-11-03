package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/observability"
	"github.com/konglong87/super-niuma/internal/tools"
)

// A server that dies before completing the handshake must report what it
// printed on stderr. Before the fix cmd.Stderr was never assigned, so the only
// diagnostic the server produced went straight to /dev/null.
func TestStartStdioSurfacesServerStderrWhenTheHandshakeFails(t *testing.T) {
	shortTimeouts(t, testBudget())

	_, err := StartStdio(t.Context(), "broken", stubServer("stderr-then-exit"))
	if err == nil {
		t.Fatal("expected the handshake to fail")
	}
	if !strings.Contains(err.Error(), "MISSING_TOKEN") {
		t.Fatalf("error does not surface the server's stderr: %v", err)
	}
}

// A server that crashes mid-session must produce an error naming the exit
// status and its stderr, not a bare EOF and not an indefinite hang.
func TestStdioCallReportsServerCrashWithExitStatusAndStderr(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "crasher", "crash-on-tools-list")

	_, err := client.ListToolsContext(t.Context())
	if err == nil {
		t.Fatal("expected an error after the server crashed")
	}
	if !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("error does not name the exit status: %v", err)
	}
	if !strings.Contains(err.Error(), "index corrupted") {
		t.Fatalf("error does not carry the server's stderr: %v", err)
	}
}

// Close must reap the child. Before the fix StartStdio never called cmd.Wait,
// so every stdio server left a zombie behind.
func TestCloseReapsTheServerProcess(t *testing.T) {
	shortTimeouts(t, testBudget())
	client, err := StartStdio(t.Context(), "tidy", stubServer("idle"))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	// Close returns only once supervise() has reaped the child, so the process
	// state must already be populated.
	if client.cmd.ProcessState == nil {
		t.Fatal("child was never waited for: it is a zombie")
	}
	select {
	case <-client.conn.readDone:
	default:
		t.Fatal("Close returned before the MCP read loop finished")
	}
}

// Close must not hang on a server that ignores its stdin closing; the shutdown
// grace period exists precisely for that case.
func TestCloseKillsAServerThatIgnoresStdinClose(t *testing.T) {
	shortTimeouts(t, testBudget())
	client, err := StartStdio(t.Context(), "stubborn", stubServer("ignore-stdin"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = client.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Close hung on a server that ignores stdin close")
	}
	if client.cmd.ProcessState == nil {
		t.Fatal("child was never reaped after the kill")
	}
}

// A server that leaves a grandchild holding the stderr pipe open must not be
// able to wedge Close. Killing the server does not close that pipe, so the
// stderr drain never sees EOF and cmd.Wait is never reached; every wait on the
// shutdown path therefore has to be bounded.
func TestCloseDoesNotWedgeOnAServerThatLeaksTheStderrPipe(t *testing.T) {
	shortTimeouts(t, testBudget())
	client, err := StartStdio(t.Context(), "leaky", stubServer("leak-stderr-to-grandchild"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = client.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Close hung: the stderr drain outlives the killed server")
	}
}

// exec's Wait closes the parent's ends of StdoutPipe/StderrPipe when it
// returns. If the supervisor calls Wait while the read loop is still draining
// stdout, the server's last frame is torn away mid-flight -- and that frame is
// often the very error explaining why the server is about to exit.
// It is a race between Wait and the read loop, so repeat it.
func TestStdioKeepsTheLastFrameFromAServerThatAnswersThenExits(t *testing.T) {
	shortTimeouts(t, testBudget())
	for i := 0; i < 25; i++ {
		client, err := StartStdio(t.Context(), "farewell", stubServer("answer-then-exit"))
		if err != nil {
			t.Fatal(err)
		}
		out, isError, err := client.CallToolWithCallback(t.Context(), "anything", nil, nil)
		_ = client.Close()
		if err != nil {
			t.Fatalf("attempt %d: the final response was lost to the pipe teardown: %v", i, err)
		}
		if isError || out != "last words" {
			t.Fatalf("attempt %d: out=%q isError=%v", i, out, isError)
		}
	}
}

// The stderr drain must never stop reading while the server is alive. A single
// line longer than the scanner's cap makes bufio.Scanner give up permanently;
// the 64KB pipe then fills and the server blocks forever in write(2).
func TestStdioSurvivesAnOverlongStderrLine(t *testing.T) {
	shortTimeouts(t, testBudget())
	client := startStub(t, "flooder", "flood-one-stderr-line")

	out, _, err := client.CallToolWithCallback(t.Context(), "anything", nil, nil)
	if err != nil {
		t.Fatalf("server wedged writing a long stderr line: %v", err)
	}
	if out != "survived" {
		t.Fatalf("out = %q", out)
	}
	// A second call proves the drain is still consuming, not just buffered.
	if _, _, err := client.CallToolWithCallback(t.Context(), "anything", nil, nil); err != nil {
		t.Fatalf("second call after the stderr flood failed: %v", err)
	}
}

// A misconfigured server must leave a trace. Before the fix both failure paths
// in LoadTools were bare `continue`s, so the server silently vanished.
func TestLoadToolsWarnsWhenAServerCannotStart(t *testing.T) {
	shortTimeouts(t, testBudget())
	logs := captureLogs(t)

	loaded, cleanup := LoadTools(t.Context(), map[string]config.MCPServerConfig{
		"typo-in-path": {Command: filepath.Join(t.TempDir(), "does-not-exist")},
	})
	defer cleanup()

	if len(loaded) != 0 {
		t.Fatalf("loaded = %d tools, want 0", len(loaded))
	}
	if !logs.mentions("typo-in-path") {
		t.Fatalf("the failed server was not logged: %v", logs.all())
	}
}

// Same for a server that starts but cannot list its tools.
func TestLoadToolsWarnsWhenListToolsFails(t *testing.T) {
	shortTimeouts(t, testBudget())
	logs := captureLogs(t)

	loaded, cleanup := LoadTools(t.Context(), map[string]config.MCPServerConfig{
		"no-tools": stubServerConfig("tools-list-error"),
	})
	defer cleanup()

	if len(loaded) != 0 {
		t.Fatalf("loaded = %d tools, want 0", len(loaded))
	}
	if !logs.mentions("tools are disabled") {
		t.Fatalf("the tools/list failure was not logged: %v", logs.all())
	}
}

// One broken server must not take the healthy ones down with it.
func TestLoadToolsKeepsHealthyServersWhenOneFails(t *testing.T) {
	shortTimeouts(t, testBudget())
	captureLogs(t)

	loaded, cleanup := LoadTools(t.Context(), map[string]config.MCPServerConfig{
		"broken":  {Command: filepath.Join(t.TempDir(), "does-not-exist")},
		"healthy": stubServerConfig("ok"),
	})
	defer cleanup()

	if len(loaded) != 1 || loaded[0].Name() != "mcp__healthy__echo" {
		t.Fatalf("loaded = %v", toolNames(loaded))
	}
}

// A cancelled context must stop an in-flight stdio tool call.
func TestStdioToolCallStopsWhenTheCallerCancels(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = time.Hour
	shortTimeouts(t, budget)
	client := startStub(t, "slowpoke", "ignore-stdin")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := client.CallToolWithCallback(ctx, "anything", nil, nil)
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the cancelled call to fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling the context did not stop the tool call")
	}
}

func toolNames(loaded []tools.Tool) []string {
	out := make([]string, 0, len(loaded))
	for _, tool := range loaded {
		out = append(out, tool.Name())
	}
	return out
}

type logCapture struct {
	logs *observer.ObservedLogs
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	core, recorded := observer.New(zapcore.DebugLevel)
	restore := observability.SetDefaultZapLogger(zap.New(core))
	t.Cleanup(restore)
	return &logCapture{logs: recorded}
}

func (c *logCapture) all() []string {
	var out []string
	for _, entry := range c.logs.All() {
		line := entry.Message
		for _, field := range entry.Context {
			line += " " + field.Key + "=" + fieldText(field)
		}
		out = append(out, line)
	}
	return out
}

func (c *logCapture) mentions(needle string) bool {
	for _, line := range c.all() {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func fieldText(field zapcore.Field) string {
	if field.String != "" {
		return field.String
	}
	if err, ok := field.Interface.(error); ok {
		return err.Error()
	}
	return ""
}

// TestStartStdioDoesNotLeakSecretsToServers locks AUDIT-P1-16. Every stdio MCP
// server used to inherit the full parent environment, credentials included.
func TestStartStdioDoesNotLeakSecretsToServers(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "OPENAI_API_KEY"} {
		t.Setenv(key, "leaked-"+key)
	}
	t.Setenv("GOPATH", "/keep/gopath")

	_, err := StartStdio(t.Context(), "reporter", stubServer("report-env"))
	if err == nil {
		t.Fatal("expected the reporting stub to fail the handshake")
	}
	if !strings.Contains(err.Error(), "A= G= W= O= P=/keep/gopath") {
		t.Fatalf("stdio server saw secrets: %v", err)
	}
}
