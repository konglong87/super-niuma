package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingReader models a server that accepted the request and then went away
// without ever answering and without closing its stdout.
type blockingReader struct {
	release chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.release
	return 0, io.EOF
}

// lockedWriter is a bytes.Buffer that the reader goroutine may touch
// concurrently with the test body.
type lockedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// scriptedServer drives the client end of a stdio conn from the test: the test
// reads whatever the client wrote and pushes frames back on its own schedule.
type scriptedServer struct {
	toClient   *io.PipeWriter
	fromClient *lockedWriter
	conn       *rpcConn
}

func newScriptedServer(t *testing.T) *scriptedServer {
	t.Helper()
	reader, writer := io.Pipe()
	server := &scriptedServer{toClient: writer, fromClient: &lockedWriter{}}
	server.conn = newRPCConn("scripted", reader, server.fromClient)
	t.Cleanup(func() {
		_ = writer.Close()
		server.conn.close(errors.New("test finished"))
	})
	return server
}

func (s *scriptedServer) send(t *testing.T, frame string) {
	t.Helper()
	if _, err := io.WriteString(s.toClient, frame+"\n"); err != nil {
		t.Fatalf("send %s: %v", frame, err)
	}
}

func shortTimeouts(t *testing.T, budget mcpTimeoutBudget) {
	t.Helper()
	mcpTimeoutsMu.Lock()
	previous := mcpTimeouts
	mcpTimeouts = budget
	mcpTimeoutsMu.Unlock()
	t.Cleanup(func() {
		mcpTimeoutsMu.Lock()
		mcpTimeouts = previous
		mcpTimeoutsMu.Unlock()
	})
}

func TestRPCConnSnapshotsTimeoutBudget(t *testing.T) {
	shortTimeouts(t, mcpTimeoutBudget{request: 25 * time.Millisecond})
	reader := &blockingReader{release: make(chan struct{})}
	t.Cleanup(func() { close(reader.release) })
	conn := newRPCConn("snapshot", reader, &lockedWriter{})

	mcpTimeoutsMu.Lock()
	mcpTimeouts.request = time.Hour
	mcpTimeoutsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	var out listToolsResult
	err := conn.call(ctx, "tools/list", map[string]any{}, &out)
	if !errors.Is(err, errMCPTimeout) {
		t.Fatalf("err = %v, want snapshotted timeout", err)
	}
}

// testBudget is generous enough to spawn a shell but far below the production
// defaults, so a test that wedges fails fast instead of hanging the suite.
// Tests that exercise a timeout path shorten the phase they care about.
func testBudget() mcpTimeoutBudget {
	return mcpTimeoutBudget{
		request:      5 * time.Second,
		toolCallIdle: 5 * time.Second,
		shutdown:     500 * time.Millisecond,
	}
}

// A server that never answers must not pin the calling goroutine forever.
// Before the fix rpcConn.call did a blocking ReadBytes under the connection
// mutex, so this call never returned at all.
func TestStdioCallTimesOutWhenServerNeverResponds(t *testing.T) {
	budget := testBudget()
	budget.request = 100 * time.Millisecond
	shortTimeouts(t, budget)
	reader := &blockingReader{release: make(chan struct{})}
	t.Cleanup(func() { close(reader.release) })
	conn := newRPCConn("hung", reader, &lockedWriter{})

	done := make(chan error, 1)
	go func() {
		var out listToolsResult
		done <- conn.call(context.Background(), "tools/list", map[string]any{}, &out)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
		if !errors.Is(err, errMCPTimeout) {
			t.Fatalf("err = %v, want errMCPTimeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call never returned: stdio read still has no timeout")
	}
}

// The guard against a naive fix: a single blanket deadline would kill a long
// but perfectly healthy tool call. tools/call is budgeted on server silence,
// not on total elapsed time, so it must survive well past the request budget.
func TestStdioToolCallOutlivesTheMetadataRequestBudget(t *testing.T) {
	budget := testBudget()
	budget.request = 20 * time.Millisecond
	budget.toolCallIdle = 3 * time.Second
	shortTimeouts(t, budget)

	server := newScriptedServer(t)
	done := make(chan error, 1)
	go func() {
		var out callToolResult
		done <- server.conn.call(context.Background(), "tools/call", map[string]any{"name": "slow"}, &out)
	}()

	// Ten times the metadata budget with the server completely silent.
	time.Sleep(200 * time.Millisecond)
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"finished"}]}}`)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("long tool call was killed by the metadata budget: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tool call never returned")
	}
}

// Server-sent notifications prove the server is alive, so they must push the
// idle deadline out. Before the fix frames without an id were dropped on the
// floor entirely.
func TestStdioToolCallIdleTimerIsResetByServerNotifications(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = 250 * time.Millisecond
	shortTimeouts(t, budget)

	server := newScriptedServer(t)
	done := make(chan error, 1)
	go func() {
		var out callToolResult
		done <- server.conn.call(context.Background(), "tools/call", map[string]any{"name": "slow"}, &out)
	}()

	// Keep the call alive for ~4x the idle budget using progress pings only.
	for i := 0; i < 8; i++ {
		time.Sleep(120 * time.Millisecond)
		server.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":%d,"total":8}}`, i))
	}
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"finished"}]}}`)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("progress notifications did not hold the idle timer open: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tool call never returned")
	}
}

// Cancelling the caller context must unblock the call. Before the fix the
// context was only sampled once, before the request was even written.
func TestStdioCallReturnsWhenCallerContextIsCancelled(t *testing.T) {
	budget := testBudget()
	budget.toolCallIdle = time.Hour
	shortTimeouts(t, budget)

	server := newScriptedServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var out callToolResult
		done <- server.conn.call(ctx, "tools/call", map[string]any{"name": "slow"}, &out)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not propagate into the stdio read")
	}
}

// Two calls on one connection must be in flight simultaneously and must each
// get their own response, even when the server answers out of order. Before
// the fix the whole round trip ran under a mutex, so call B could not even be
// written until call A had returned.
func TestStdioCallsAreMultiplexedNotSerialized(t *testing.T) {
	shortTimeouts(t, mcpTimeoutBudget{
		request:      5 * time.Second,
		toolCallIdle: 5 * time.Second,
		shutdown:     500 * time.Millisecond,
	})
	server := newScriptedServer(t)

	type outcome struct {
		text string
		err  error
	}
	first := make(chan outcome, 1)
	second := make(chan outcome, 1)
	run := func(sink chan outcome, tool string) {
		var out callToolResult
		err := server.conn.call(context.Background(), "tools/call", map[string]any{"name": tool}, &out)
		var text string
		if len(out.Content) > 0 {
			text = out.Content[0].Text
		}
		sink <- outcome{text: text, err: err}
	}
	go run(first, "a")
	// Wait until the first request is on the wire so the ids are deterministic.
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"a"`) })
	go run(second, "b")
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"b"`) })

	// Answer in reverse order.
	server.send(t, `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"second"}]}}`)
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"first"}]}}`)

	got1 := receive(t, first)
	got2 := receive(t, second)
	if got1.err != nil || got1.text != "first" {
		t.Fatalf("first call = %+v", got1)
	}
	if got2.err != nil || got2.text != "second" {
		t.Fatalf("second call = %+v", got2)
	}
}

// A server that dies mid-call must surface a real error instead of leaving the
// caller blocked on a pipe that will never produce another byte.
func TestStdioCallFailsWhenServerClosesTheConnection(t *testing.T) {
	shortTimeouts(t, mcpTimeoutBudget{
		request:      5 * time.Second,
		toolCallIdle: 5 * time.Second,
		shutdown:     500 * time.Millisecond,
	})
	server := newScriptedServer(t)

	done := make(chan error, 1)
	go func() {
		var out callToolResult
		done <- server.conn.call(context.Background(), "tools/call", map[string]any{"name": "boom"}, &out)
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"boom"`) })
	_ = server.toClient.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after the server closed stdout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("call never returned after the server closed stdout")
	}
}

// A response delivered just before the server hangs up must still win over the
// connection-closed error: the demultiplexing reader dispatches the response
// and then immediately closes the connection, so the two race.
func TestStdioCallPrefersADeliveredResponseOverConnectionClose(t *testing.T) {
	shortTimeouts(t, testBudget())
	for i := 0; i < 100; i++ {
		reader, writer := io.Pipe()
		sink := &lockedWriter{}
		conn := newRPCConn("hangup", reader, sink)
		done := make(chan error, 1)
		var out listToolsResult
		go func() { done <- conn.call(context.Background(), "tools/list", map[string]any{}, &out) }()
		// Only answer once the request is on the wire, then hang up immediately.
		waitFor(t, func() bool { return strings.Contains(sink.String(), `"tools/list"`) })
		_, _ = io.WriteString(writer, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo"}]}}`+"\n")
		_ = writer.Close()

		if err := receive(t, done); err != nil {
			t.Fatalf("attempt %d: hangup beat the delivered response: %v", i, err)
		}
		if len(out.Tools) != 1 || out.Tools[0].Name != "echo" {
			t.Fatalf("attempt %d: result = %+v", i, out)
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

func receive[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		var zero T
		t.Fatal("timed out waiting for a result")
		return zero
	}
}

// A server that stops reading its stdin must not be able to block the writer
// forever. The write happens before await, holding writeMu, so neither the
// timeout budget nor the caller's context can reach it.
func TestStdioWriteToAServerThatStoppedReadingStdinTimesOut(t *testing.T) {
	budget := testBudget()
	budget.request = 300 * time.Millisecond
	budget.toolCallIdle = 300 * time.Millisecond
	shortTimeouts(t, budget)
	client := startStub(t, "deaf", "ignore-stdin")

	// Far more than the pipe buffer, so the write cannot simply be absorbed.
	huge, err := json.Marshal(map[string]string{"blob": strings.Repeat("x", 1024*1024)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, callErr := client.CallToolWithCallback(context.Background(), "anything", huge, nil)
		done <- callErr
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the blocked write to fail")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("writeFrame blocked forever: a server that stops reading stdin wedges the client")
	}
}

// MCP does not correlate server->client requests with a specific call. With
// one call in flight the attribution is certain; with two it is a guess, and
// guessing wrong shows the wrong tool name in the approval prompt and records
// the permission rule against the wrong tool.
func TestServerCallbackIsRefusedWhenItCannotBeAttributed(t *testing.T) {
	shortTimeouts(t, mcpTimeoutBudget{
		request:      5 * time.Second,
		toolCallIdle: 5 * time.Second,
		shutdown:     500 * time.Millisecond,
	})
	server := newScriptedServer(t)

	handler := func(string, json.RawMessage) (json.RawMessage, *rpcError) {
		t.Error("handler must not be consulted when attribution is ambiguous")
		return json.RawMessage(`{}`), nil
	}
	run := func(tool string) chan error {
		sink := make(chan error, 1)
		go func() {
			var out callToolResult
			sink <- server.conn.callWithCallback(context.Background(), "tools/call",
				map[string]any{"name": tool}, &out, handler)
		}()
		return sink
	}
	first := run("a")
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"a"`) })
	second := run("b")
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"b"`) })

	server.send(t, `{"jsonrpc":"2.0","id":77,"method":"elicitation/create","params":{"message":"which call?"}}`)
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"id":77`) })

	callback := findResponseByID(t, server.fromClient.String(), 77)
	if callback.Error == nil || !strings.Contains(callback.Error.Message, "cannot be attributed") {
		t.Fatalf("callback = %+v", callback)
	}

	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	server.send(t, `{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
	if err := receive(t, first); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, second); err != nil {
		t.Fatal(err)
	}
}

// JSON-RPC 2.0 permits string ids. Decoding straight into int64 makes the
// whole frame fail to parse, so the response is dropped and the caller waits
// out its full budget.
func TestStdioAcceptsAStringResponseId(t *testing.T) {
	shortTimeouts(t, testBudget())
	server := newScriptedServer(t)

	done := make(chan error, 1)
	var out listToolsResult
	go func() { done <- server.conn.call(context.Background(), "tools/list", map[string]any{}, &out) }()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "tools/list") })
	server.send(t, `{"jsonrpc":"2.0","id":"1","result":{"tools":[{"name":"echo"}]}}`)

	if err := receive(t, done); err != nil {
		t.Fatalf("a string id made the response unroutable: %v", err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", out.Tools)
	}
}

// A server->client request carrying a string id must be answered with that
// same id, verbatim, or the server cannot match up our reply.
func TestServerCallbackEchoesAStringIdVerbatim(t *testing.T) {
	shortTimeouts(t, testBudget())
	server := newScriptedServer(t)

	done := make(chan error, 1)
	go func() {
		var out callToolResult
		done <- server.conn.callWithCallback(context.Background(), "tools/call", map[string]any{"name": "a"}, &out,
			func(string, json.RawMessage) (json.RawMessage, *rpcError) {
				return json.RawMessage(`{"decision":"allow"}`), nil
			})
	}()
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), `"name":"a"`) })
	server.send(t, `{"jsonrpc":"2.0","id":"req-abc","method":"permissions/request","params":{}}`)
	waitFor(t, func() bool { return strings.Contains(server.fromClient.String(), "req-abc") })

	if !strings.Contains(server.fromClient.String(), `"id":"req-abc"`) {
		t.Fatalf("string id was not echoed verbatim: %s", server.fromClient.String())
	}
	server.send(t, `{"jsonrpc":"2.0","id":1,"result":{"content":[]}}`)
	if err := receive(t, done); err != nil {
		t.Fatal(err)
	}
}
