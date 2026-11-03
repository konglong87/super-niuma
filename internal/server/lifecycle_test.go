package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/konglong87/super-niuma/internal/query"
	"github.com/konglong87/super-niuma/internal/telemetry"
)

type lifecycleOrderingSink struct {
	release chan struct{}
	mu      *sync.Mutex
	order   *[]string
}

func (s *lifecycleOrderingSink) Emit(context.Context, telemetry.Event) error {
	<-s.release
	s.mu.Lock()
	*s.order = append(*s.order, "telemetry_flush")
	s.mu.Unlock()
	return nil
}

func TestShutdownSignalsIncludeSIGTERM(t *testing.T) {
	signals := ShutdownSignals()
	wanted := map[os.Signal]bool{os.Interrupt: false, syscall.SIGTERM: false}
	for _, sig := range signals {
		if _, ok := wanted[sig]; ok {
			wanted[sig] = true
		}
	}
	for sig, found := range wanted {
		if !found {
			t.Fatalf("%v missing from ShutdownSignals() = %v; systemd/k8s/docker default to SIGTERM", sig, signals)
		}
	}
}

func TestNewHTTPServerSetsReadSideTimeouts(t *testing.T) {
	srv := newHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 {
		t.Fatalf("ReadHeaderTimeout must be set to close the Slowloris hole")
	}
	if srv.ReadTimeout <= 0 {
		t.Fatalf("ReadTimeout must be set")
	}
	if srv.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout must be set")
	}
	// WriteTimeout 故意为 0：它从请求开始计时，会砍断长跑的 /query 和 SSE。
	// 写侧的保护由 guardedWriter 提供，见 TestGuardedWriterDeadlinePolicy。
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 (guardedWriter owns the write deadline)", srv.WriteTimeout)
	}
}

// deadlineRecorder 让 http.NewResponseController 能在测试里观测 deadline 调用。
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	writeDeadlines []time.Time
	readDeadlines  []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(at time.Time) error {
	d.writeDeadlines = append(d.writeDeadlines, at)
	return nil
}

func (d *deadlineRecorder) SetReadDeadline(at time.Time) error {
	d.readDeadlines = append(d.readDeadlines, at)
	return nil
}

func TestGuardedWriterDeadlinePolicy(t *testing.T) {
	t.Run("plain response arms a write deadline", func(t *testing.T) {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		guarded := &guardedWriter{ResponseWriter: rec, streams: newStreamRegistry(), cancel: func() {}}
		guarded.Header().Set("content-type", "application/json")
		if _, err := guarded.Write([]byte(`{}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(rec.writeDeadlines) != 1 || rec.writeDeadlines[0].IsZero() {
			t.Fatalf("write deadlines = %v, want one non-zero deadline", rec.writeDeadlines)
		}
	})

	t.Run("sse response clears both deadlines", func(t *testing.T) {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		streams := newStreamRegistry()
		guarded := &guardedWriter{ResponseWriter: rec, streams: streams, cancel: func() {}}
		guarded.Header().Set("content-type", "text/event-stream")
		if _, err := guarded.Write([]byte("event: hi\ndata: {}\n\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if len(rec.writeDeadlines) != 1 || !rec.writeDeadlines[0].IsZero() {
			t.Fatalf("write deadlines = %v, want a single zero value (cleared)", rec.writeDeadlines)
		}
		if len(rec.readDeadlines) != 1 || !rec.readDeadlines[0].IsZero() {
			t.Fatalf("read deadlines = %v, want a single zero value (cleared)", rec.readDeadlines)
		}
		if streams.size() != 1 {
			t.Fatalf("registry size = %d, want the SSE stream registered for shutdown", streams.size())
		}
		guarded.finish()
		if streams.size() != 0 {
			t.Fatalf("registry size = %d after finish, want 0", streams.size())
		}
	})

	t.Run("drain writes a terminal event and cancels the request", func(t *testing.T) {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		streams := newStreamRegistry()
		cancelled := make(chan struct{})
		guarded := &guardedWriter{ResponseWriter: rec, streams: streams, cancel: func() { close(cancelled) }}
		guarded.Header().Set("content-type", "text/event-stream")
		if _, err := guarded.Write([]byte("event: hi\ndata: {}\n\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if drained := streams.drain(); drained != 1 {
			t.Fatalf("drained = %d, want 1", drained)
		}
		if !strings.Contains(rec.Body.String(), "event: server_shutdown") {
			t.Fatalf("body = %q, want the shutdown event appended", rec.Body.String())
		}
		select {
		case <-cancelled:
		default:
			t.Fatalf("drain must cancel the request context so the SSE handler returns")
		}
	})
}

// closeNotifyRecorder 复现 *http.response 会实现的可选接口。
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	notify chan bool
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool { return c.notify }

// gin 的 ResponseWriter.CloseNotify / Flush 都是无保护的类型断言，guardedWriter
// 插在中间时必须把它们原样转发，否则会在运行时 panic。
func TestGuardedWriterForwardsOptionalInterfaces(t *testing.T) {
	notify := make(chan bool, 1)
	rec := &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder(), notify: notify}
	guarded := &guardedWriter{ResponseWriter: rec, streams: newStreamRegistry(), cancel: func() {}}

	if _, ok := any(guarded).(http.Flusher); !ok {
		t.Fatalf("guardedWriter must implement http.Flusher")
	}
	notifier, ok := any(guarded).(http.CloseNotifier)
	if !ok {
		t.Fatalf("guardedWriter must implement http.CloseNotifier")
	}
	if notifier.CloseNotify() == nil {
		t.Fatalf("CloseNotify must forward the underlying channel")
	}
	if _, ok := any(guarded).(http.Hijacker); !ok {
		t.Fatalf("guardedWriter must implement http.Hijacker for the websocket route")
	}
	if n, err := guarded.WriteString("hello"); err != nil || n != 5 {
		t.Fatalf("WriteString = (%d, %v)", n, err)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if guarded.Unwrap() != http.ResponseWriter(rec) {
		t.Fatalf("Unwrap must expose the underlying writer for http.ResponseController")
	}
}

// startTestServer 在随机端口上跑真正的 http.Server 生命周期，返回 base URL 与
// 一个等待 serve 退出的函数。
func startTestServer(t *testing.T, ctx context.Context, opts Options, queryFn QueryFunc) (string, func(time.Duration) error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTPLifecycle(ctx, listener, opts, queryFn) }()
	wait := func(within time.Duration) error {
		select {
		case err := <-done:
			return err
		case <-time.After(within):
			return fmt.Errorf("serve did not return within %s", within)
		}
	}
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			_ = listener.Close()
		}
	})
	return "http://" + listener.Addr().String(), wait
}

func TestServeCompletesInFlightRequestBeforeExiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	release := make(chan struct{})
	queryFn := func(ctx context.Context, req QueryRequest) (query.Result, error) {
		close(started)
		<-release
		return query.Result{Response: "finished after shutdown began", Model: "m"}, nil
	}
	baseURL, wait := startTestServer(t, ctx, Options{AuthToken: "token", Workspace: "/tmp/work", ShutdownTimeout: 10 * time.Second}, queryFn)

	type result struct {
		status int
		body   string
		err    error
	}
	responses := make(chan result, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/query", strings.NewReader(`{"prompt":"hi"}`))
		if err != nil {
			responses <- result{err: err}
			return
		}
		req.Header.Set("authorization", "Bearer token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			responses <- result{err: err}
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		responses <- result{status: resp.StatusCode, body: string(body), err: err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("query handler never started")
	}

	// 请求还在跑的时候触发退出。
	cancel()

	// 修复前：Serve 立刻返回 ErrServerClosed，Run 直接 return nil，在途请求被硬断。
	if err := wait(300 * time.Millisecond); err == nil {
		t.Fatalf("serve returned while a request was still in flight")
	}

	close(release)
	select {
	case got := <-responses:
		if got.err != nil {
			t.Fatalf("in-flight request failed instead of completing: %v", got.err)
		}
		if got.status != http.StatusOK {
			t.Fatalf("status = %d body = %s", got.status, got.body)
		}
		var payload query.Result
		if err := json.Unmarshal([]byte(got.body), &payload); err != nil {
			t.Fatalf("response body is not a complete JSON result (%v): %s", err, got.body)
		}
		if payload.Response != "finished after shutdown began" {
			t.Fatalf("response = %q, want the handler's full result", payload.Response)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("in-flight request never completed")
	}

	if err := wait(5 * time.Second); err != nil {
		t.Fatalf("serve did not exit after draining: %v", err)
	}
}

func TestServeStopsNextStepsBeforeTelemetryFlush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var orderMu sync.Mutex
	order := make([]string, 0, 2)
	releaseTelemetry := make(chan struct{})
	telemetrySink := &lifecycleOrderingSink{release: releaseTelemetry, mu: &orderMu, order: &order}
	dispatcherStopped := make(chan struct{})
	var stopOnce sync.Once
	dispatcher := &agentTaskNextStepsDispatcher{
		enqueue: func(agentTaskNextStepsJob) bool { return true },
		stop: func(context.Context) {
			stopOnce.Do(func() {
				orderMu.Lock()
				order = append(order, "dispatcher_stop")
				orderMu.Unlock()
				close(dispatcherStopped)
			})
		},
	}
	opts := telemetryTestOptions(telemetrySink)
	opts.nextStepsDispatcher = dispatcher
	baseURL, wait := startTestServer(t, ctx, opts, nil)
	getHealth(t, baseURL)
	cancel()
	select {
	case <-dispatcherStopped:
	case <-time.After(time.Second):
		t.Fatal("dispatcher stop was not invoked during lifecycle shutdown")
	}
	orderMu.Lock()
	if len(order) != 1 || order[0] != "dispatcher_stop" {
		t.Fatalf("shutdown order before telemetry release = %v, want [dispatcher_stop]", order)
	}
	orderMu.Unlock()
	close(releaseTelemetry)
	if err := wait(15 * time.Second); err != nil {
		t.Fatalf("serve: %v", err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) < 2 || order[0] != "dispatcher_stop" || order[1] != "telemetry_flush" {
		t.Fatalf("shutdown order = %v, want dispatcher_stop before telemetry_flush", order)
	}
}

func TestServeTerminatesSSEWithinShutdownDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streaming := make(chan struct{})
	var streamCtxDone atomic.Bool
	opts := Options{
		AuthToken: "token",
		Workspace: "/tmp/work",
		// deadline 设得比断言窗口大：必须证明流是被主动终止的，不是耗光 deadline 后硬断。
		ShutdownTimeout: 30 * time.Second,
		StreamQueryFunc: func(ctx context.Context, req QueryRequest, _ io.Writer) (query.Result, error) {
			close(streaming)
			<-ctx.Done()
			streamCtxDone.Store(true)
			return query.Result{}, ctx.Err()
		},
	}
	baseURL, wait := startTestServer(t, ctx, opts, func(context.Context, QueryRequest) (query.Result, error) {
		return query.Result{}, nil
	})

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"Hello"}]}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("content-type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want an SSE stream", got)
	}

	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("read first sse chunk: %v", err)
	}
	select {
	case <-streaming:
	case <-time.After(5 * time.Second):
		t.Fatalf("stream query never started")
	}

	cancel()

	tail, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read the rest of the stream: %v", err)
	}
	if !strings.Contains(string(tail), "event: server_shutdown") {
		t.Fatalf("stream tail = %q, want an explicit shutdown event before the close", string(tail))
	}
	if !streamCtxDone.Load() {
		t.Fatalf("the SSE handler's context was never cancelled")
	}
	// 若只依赖 Shutdown 自己等 SSE 变 idle，这里会一直等到 30s deadline。
	if err := wait(10 * time.Second); err != nil {
		t.Fatalf("serve did not exit: %v", err)
	}
}

func TestServeShutsDownGracefullyOnSIGTERM(t *testing.T) {
	ctx, stop := signal.NotifyContext(context.Background(), ShutdownSignals()...)
	defer stop()

	started := make(chan struct{})
	release := make(chan struct{})
	queryFn := func(context.Context, QueryRequest) (query.Result, error) {
		close(started)
		<-release
		return query.Result{Response: "survived sigterm", Model: "m"}, nil
	}
	baseURL, wait := startTestServer(t, ctx, Options{AuthToken: "token", Workspace: "/tmp/work", ShutdownTimeout: 10 * time.Second}, queryFn)

	responses := make(chan string, 1)
	go func() {
		req, err := http.NewRequest(http.MethodPost, baseURL+"/query", strings.NewReader(`{"prompt":"hi"}`))
		if err != nil {
			responses <- "request error: " + err.Error()
			return
		}
		req.Header.Set("authorization", "Bearer token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			responses <- "request error: " + err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		responses <- string(body)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("query handler never started")
	}

	// 修复前 main 只捕 os.Interrupt，SIGTERM 会直接杀掉进程。
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for ctx.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatalf("SIGTERM did not cancel the shutdown context")
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(release)
	select {
	case body := <-responses:
		if !strings.Contains(body, "survived sigterm") {
			t.Fatalf("in-flight response after SIGTERM = %q", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("in-flight request never completed after SIGTERM")
	}
	if err := wait(5 * time.Second); err != nil {
		t.Fatalf("serve did not exit after SIGTERM: %v", err)
	}
}

// countingReader 生成一个永远不结束的合法 JSON 前缀（一个不闭合的字符串），
// 并记录被真正读掉的字节数，用来证明超大请求体没有被整体读进内存。
type countingReader struct {
	remaining int64
	read      int64
	prefix    string
	prefixPos int
}

func newOversizedJSONBody(size int64) *countingReader {
	return &countingReader{remaining: size, prefix: `{"prompt":"`}
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := int64(0); i < n; i++ {
		if r.prefixPos < len(r.prefix) {
			p[i] = r.prefix[r.prefixPos]
			r.prefixPos++
			continue
		}
		p[i] = 'a'
	}
	r.remaining -= n
	r.read += n
	return int(n), nil
}

func (r *countingReader) Close() error { return nil }

func TestRequestBodyLimitRejectsOversizedBodies(t *testing.T) {
	const limit = 4096
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/tmp/work", MaxRequestBodyBytes: limit},
		func(context.Context, QueryRequest) (query.Result, error) {
			t.Fatalf("query handler must not run for an oversized body")
			return query.Result{}, nil
		})

	t.Run("declared content-length is rejected without reading the body", func(t *testing.T) {
		body := newOversizedJSONBody(64 << 20)
		req := httptest.NewRequest(http.MethodPost, "/query", body)
		req.ContentLength = 64 << 20
		req.Header.Set("authorization", "Bearer token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if body.read != 0 {
			t.Fatalf("read %d bytes from an over-long body; it must be rejected on Content-Length alone", body.read)
		}
	})

	t.Run("unknown length is capped mid-read", func(t *testing.T) {
		body := newOversizedJSONBody(64 << 20)
		req := httptest.NewRequest(http.MethodPost, "/query", body)
		req.ContentLength = -1
		req.Header.Set("authorization", "Bearer token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		if body.read > limit+4096 {
			t.Fatalf("buffered %d bytes for a %d byte limit; MaxBytesReader is not in the path", body.read, limit)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("413 body is not JSON (%v): %s", err, rec.Body.String())
		}
		if payload["error_type"] != "request_body_too_large" {
			t.Fatalf("413 payload = %v", payload)
		}
	})
}

func TestRequestBodyLimitAllowsNormalRequests(t *testing.T) {
	handler := NewHandler(Options{AuthToken: "token", Workspace: "/tmp/work"},
		func(_ context.Context, req QueryRequest) (query.Result, error) {
			return query.Result{Response: "ok:" + req.Prompt, Model: "m"}, nil
		})
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set("authorization", "Bearer token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ok:hello") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestMaxRequestBodyBytesResolution(t *testing.T) {
	if got := maxRequestBodyBytes(Options{}); got != defaultMaxRequestBodyBytes {
		t.Fatalf("default = %d, want %d", got, defaultMaxRequestBodyBytes)
	}
	if got := maxRequestBodyBytes(Options{MaxRequestBodyBytes: 123}); got != 123 {
		t.Fatalf("explicit = %d, want 123", got)
	}
	if got := maxRequestBodyBytes(Options{MaxRequestBodyBytes: -1}); got != 0 {
		t.Fatalf("negative must disable the limit, got %d", got)
	}
}

// 日志中间件原先用无上限的 io.ReadAll 把整个请求体缓存进内存。现在只采样开头，
// 但 handler 必须仍然拿到完整请求体。
func TestMobileBodySummaryLogMiddlewareSamplesWithoutBufferingWholeBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bodySize := mobileBodyLogSampleMaxBytes * 3
	body := strings.Repeat("x", bodySize)

	var seen int
	router := gin.New()
	router.Use(mobileBodySummaryLogMiddleware())
	router.POST("/echo", func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Errorf("handler read body: %v", err)
		}
		seen = len(got)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/echo", strings.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if seen != bodySize {
		t.Fatalf("handler saw %d bytes, want the full %d byte body", seen, bodySize)
	}
}

func TestMobileBodySummaryAttrsFlagTruncation(t *testing.T) {
	attrs := attrsMap(mobileBodySummaryAttrs([]byte(strings.Repeat("x", 10)), true))
	if attrs["body_truncated"] != true {
		t.Fatalf("attrs = %+v, want body_truncated=true", attrs)
	}
	if attrs["body_bytes"] != 10 {
		t.Fatalf("attrs = %+v, want the sampled length", attrs)
	}
}

func TestIsRequestBodyTooLarge(t *testing.T) {
	if isRequestBodyTooLarge(nil) {
		t.Fatalf("nil error must not be reported as an oversized body")
	}
	if isRequestBodyTooLarge(errors.New("boom")) {
		t.Fatalf("unrelated error must not be reported as an oversized body")
	}
	if !isRequestBodyTooLarge(fmt.Errorf("wrapped: %w", &http.MaxBytesError{Limit: 10})) {
		t.Fatalf("wrapped MaxBytesError must be detected")
	}
}
