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
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

const (
	// 读侧的硬上限：Slowloris 只需要一条握了手却迟迟不发完请求头的连接。
	defaultReadHeaderTimeout = 15 * time.Second
	defaultReadTimeout       = 60 * time.Second
	defaultIdleTimeout       = 120 * time.Second

	// 写侧刻意不用 http.Server.WriteTimeout：那个 deadline 从请求开始计时，会砍断
	// 「跑完整个 agent turn 再一次性返回 JSON」的 /query，也会砍断 SSE 长连接。
	// 改成由 guardedWriter 在首次写出时才武装、每次写出续期，对 SSE / WebSocket
	// 则彻底清掉 —— 慢速读攻击照样挡住（没有进展就到期），慢 handler 不受影响。
	defaultResponseWriteTimeout = 60 * time.Second

	// 优雅退出的总预算。SSE 连接永远不会变成 idle，所以 Shutdown 必须带 deadline，
	// 并且在 Shutdown 之前先主动终止这些流，否则只能等到 deadline 再硬断。
	defaultShutdownTimeout = 20 * time.Second

	// 终止事件写出去的宽限期：SSE 已经清掉写 deadline，这里临时补一个，
	// 避免一个不读数据的客户端把 shutdown 卡死。
	streamTerminateGrace = 3 * time.Second

	defaultMaxRequestBodyBytes = 10 << 20 // 10 MiB
	defaultMaxMultipartMemory  = 8 << 20  // 8 MiB

	// shutdown 时给 SSE 客户端发的终止事件。EventSource 会忽略没有监听器的
	// 自定义事件类型，所以对现有前端是安全的增量。
	sseShutdownEvent = "event: server_shutdown\ndata: {\"reason\":\"server_shutdown\"}\n\n"
)

// ShutdownSignals 是触发优雅退出的信号集合。
//
// SIGTERM 必须在内：systemd / kubernetes / `docker stop` 默认发的都是 SIGTERM，
// 只捕 SIGINT 等于在容器里完全没有优雅退出 —— 进程会被直接杀掉，
// 下面所有 drain 逻辑一行都不会执行。
func ShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func newHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		IdleTimeout:       defaultIdleTimeout,
		// WriteTimeout 交给 guardedWriter，见 defaultResponseWriteTimeout 说明。
		WriteTimeout: 0,
	}
}

func maxRequestBodyBytes(opts Options) int64 {
	switch {
	case opts.MaxRequestBodyBytes > 0:
		return opts.MaxRequestBodyBytes
	case opts.MaxRequestBodyBytes < 0:
		return 0 // 显式关闭上限
	default:
		return defaultMaxRequestBodyBytes
	}
}

func shutdownTimeout(opts Options) time.Duration {
	if opts.ShutdownTimeout > 0 {
		return opts.ShutdownTimeout
	}
	return defaultShutdownTimeout
}

// requestGuardHandler 是最外层中间件：限制请求体大小，并把 ResponseWriter 换成
// guardedWriter，让长连接（SSE / WebSocket）在 shutdown 时能被主动终止。
//
// 它装在 gin 之外，因此只需要实现 http.ResponseWriter 的几个可选接口；
// gin 会在它之上再包一层自己的 ResponseWriter。
func requestGuardHandler(next http.Handler, streams *streamRegistry, opts Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxBodyBytes := maxRequestBodyBytes(opts)
		if opts.MaxRequestBodyBytes == 0 && isSessionControlMessageRequest(r) {
			maxBodyBytes = sessionControlImageMaxRequestBytes
		}
		if maxBodyBytes > 0 {
			if r.ContentLength > maxBodyBytes {
				writeRequestBodyTooLarge(w, maxBodyBytes)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			}
		}
		ctx, cancel := context.WithCancel(withClientIP(r.Context(), rateLimitKeyFromRequest(r)))
		defer cancel()
		guarded := &guardedWriter{ResponseWriter: w, streams: streams, cancel: cancel}
		defer guarded.finish()
		next.ServeHTTP(guarded, r.WithContext(ctx))
	})
}

func writeRequestBodyTooLarge(w http.ResponseWriter, limit int64) {
	payload, err := json.Marshal(map[string]any{
		"error":      fmt.Sprintf("request body exceeds the %d byte limit", limit),
		"error_type": "request_body_too_large",
		"max_bytes":  limit,
	})
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	_, _ = w.Write(payload)
}

// isRequestBodyTooLarge 判断解码失败是否源于请求体上限，用于把 400 纠正成 413。
func isRequestBodyTooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.As(err, &maxBytes)
}

// streamRegistry 跟踪在途的长连接。SSE 连接不会变 idle，http.Server.Shutdown
// 单靠自己只能干等到 deadline，所以退出前必须由这里主动收口。
type streamRegistry struct {
	mu       sync.Mutex
	draining bool
	streams  map[*guardedWriter]struct{}
}

func newStreamRegistry() *streamRegistry {
	return &streamRegistry{streams: map[*guardedWriter]struct{}{}}
}

// add 登记一条长连接；返回 false 表示已经在退出，调用方应立即终止它。
func (r *streamRegistry) add(w *guardedWriter) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.draining {
		return false
	}
	r.streams[w] = struct{}{}
	return true
}

func (r *streamRegistry) remove(w *guardedWriter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.streams, w)
}

func (r *streamRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// drain 给每条长连接发终止事件并取消它的 request context，让 handler 自己返回。
// 快照后立即释放 registry 锁：terminate 需要取 guardedWriter 的锁，
// 两把锁不能同时持有（register 的加锁顺序正好相反）。
func (r *streamRegistry) drain() int {
	r.mu.Lock()
	r.draining = true
	pending := make([]*guardedWriter, 0, len(r.streams))
	for stream := range r.streams {
		pending = append(pending, stream)
	}
	r.mu.Unlock()
	for _, stream := range pending {
		stream.terminate()
	}
	return len(pending)
}

// guardedWriter 包住底层 ResponseWriter，负责三件事：
//   - 识别 SSE / WebSocket 长连接并清掉它们的读写 deadline；
//   - 给普通响应武装一个「写空闲」deadline；
//   - shutdown 时串行地插入终止事件，然后取消 request context。
type guardedWriter struct {
	http.ResponseWriter

	streams *streamRegistry
	cancel  context.CancelFunc

	mu         sync.Mutex // 串行化 handler 写出与 shutdown 终止事件
	classified bool
	longLived  bool
	registered bool
	hijacked   bool
	terminated bool
	deadline   time.Time
}

func (w *guardedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *guardedWriter) WriteHeader(code int) {
	w.mu.Lock()
	newStream := w.classifyLocked()
	w.ResponseWriter.WriteHeader(code)
	w.mu.Unlock()
	if newStream {
		w.register()
	}
}

func (w *guardedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	newStream := w.classifyLocked()
	n, err := w.ResponseWriter.Write(p)
	w.mu.Unlock()
	if newStream {
		w.register()
	}
	return n, err
}

func (w *guardedWriter) WriteString(s string) (int, error) {
	w.mu.Lock()
	newStream := w.classifyLocked()
	n, err := io.WriteString(w.ResponseWriter, s)
	w.mu.Unlock()
	if newStream {
		w.register()
	}
	return n, err
}

func (w *guardedWriter) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	flusher.Flush()
}

// CloseNotify 必须转发：gin 的 ResponseWriter.CloseNotify 是无保护的类型断言，
// 少了这一层包装会直接 panic。
func (w *guardedWriter) CloseNotify() <-chan bool {
	if notifier, ok := w.ResponseWriter.(http.CloseNotifier); ok {
		return notifier.CloseNotify()
	}
	return nil
}

// Hijack 之后连接脱离 http.Server 的 deadline 管理，但请求开始时按 ReadTimeout
// 设置的 deadline 仍然挂在 net.Conn 上，会在 60s 后打断 WebSocket。这里清掉它，
// 同时把连接登记进 registry，让 shutdown 能取消它的 context。
func (w *guardedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	conn, buf, err := hijacker.Hijack()
	if err != nil {
		return conn, buf, err
	}
	_ = conn.SetDeadline(time.Time{})
	w.mu.Lock()
	w.hijacked = true
	w.classified = true
	w.longLived = true
	w.mu.Unlock()
	w.register()
	return conn, buf, nil
}

// classifyLocked 在首次写出时判断这是不是 SSE 长连接；返回 true 表示调用方
// 需要在释放 w.mu 之后调用 register。调用方必须持有 w.mu。
func (w *guardedWriter) classifyLocked() bool {
	if w.classified {
		w.refreshWriteDeadlineLocked()
		return false
	}
	w.classified = true
	w.longLived = strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream")
	if !w.longLived {
		w.refreshWriteDeadlineLocked()
		return false
	}
	// SSE：写 deadline 会在第一次长间隔里误杀连接，读 deadline 会取消 request
	// context，两个都得清掉。
	w.clearConnDeadlines()
	return true
}

// register 把长连接登记进 registry；若已经在退出，立刻终止它。
// 必须在不持有 w.mu 的情况下调用（terminate 会反向加锁）。
func (w *guardedWriter) register() {
	if w.streams == nil {
		return
	}
	if !w.streams.add(w) {
		w.terminate()
		return
	}
	w.mu.Lock()
	w.registered = true
	w.mu.Unlock()
}

func (w *guardedWriter) finish() {
	w.mu.Lock()
	registered := w.registered
	w.registered = false
	w.mu.Unlock()
	if registered && w.streams != nil {
		w.streams.remove(w)
	}
}

// terminate 先补一个短写 deadline（防止不读数据的客户端卡死 shutdown），
// 再串行写出终止事件，最后取消 request context 让 handler 自己收尾。
func (w *guardedWriter) terminate() {
	w.setConnWriteDeadline(time.Now().Add(streamTerminateGrace))
	w.mu.Lock()
	if w.terminated {
		w.mu.Unlock()
		return
	}
	w.terminated = true
	if !w.hijacked {
		_, _ = fmt.Fprint(w.ResponseWriter, sseShutdownEvent)
		if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
	}
}

// refreshWriteDeadlineLocked 把写 deadline 续到 now+defaultResponseWriteTimeout。
// 只在过半时才真正下 syscall，避免每次 Write 都打一次 SetWriteDeadline。
func (w *guardedWriter) refreshWriteDeadlineLocked() {
	if w.longLived {
		return
	}
	now := time.Now()
	if !w.deadline.IsZero() && w.deadline.Sub(now) > defaultResponseWriteTimeout/2 {
		return
	}
	w.deadline = now.Add(defaultResponseWriteTimeout)
	w.setConnWriteDeadline(w.deadline)
}

// setConnWriteDeadline / clearConnDeadlines 直接作用在底层连接上，不经过 w.mu。
// httptest.ResponseRecorder 不支持 deadline，返回的错误可以忽略。
func (w *guardedWriter) setConnWriteDeadline(at time.Time) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(at)
}

func (w *guardedWriter) clearConnDeadlines() {
	controller := http.NewResponseController(w.ResponseWriter)
	_ = controller.SetWriteDeadline(time.Time{})
	_ = controller.SetReadDeadline(time.Time{})
}

// serveHTTPLifecycle 承载 Run 的服务生命周期（不含 scheduler daemon），
// 便于测试真正的优雅退出。
func serveHTTPLifecycle(ctx context.Context, listener net.Listener, opts Options, queryFn QueryFunc) error {
	timeout := shutdownTimeout(opts)
	preparePendingInputRuntime(ctx, &opts, queryFn)
	if opts.nextStepsDispatcher == nil {
		opts.nextStepsDispatcher = newAgentTaskNextStepsDispatcher(ctx, opts)
	}
	// 慢 telemetry sink 在这里进后台缓冲，请求路径只入队(AUDIT-P1-24)。flush 挂在
	// defer 上，跑在 shutdown 排空在途请求之后 —— 那些请求还在往缓冲里写。
	opts.telemetryAsync = newTelemetryPump(opts)
	defer flushTelemetryPump(ctx, opts.telemetryAsync, timeout)
	// Stop optional next-steps work before flushing telemetry so no worker can
	// enqueue observations after the telemetry pump has been closed. This defer
	// is registered last because defers execute in reverse registration order.
	defer opts.nextStepsDispatcher.stop(context.Background())
	if drainer, ok := opts.SessionControl.(SessionControlDrainer); ok {
		defer func() {
			drainCtx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			_ = drainer.Drain(drainCtx)
		}()
	}

	handler, streams := newHandlerWithStreams(opts, queryFn)
	srv := newHTTPServer(handler)

	serveDone := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-serveDone:
			shutdownDone <- nil
			return
		}
		shutdownDone <- shutdownHTTPServer(ctx, srv, streams, timeout)
	}()

	serveErr := srv.Serve(listener)
	close(serveDone)
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	// Serve 在 Shutdown 关闭 listener 的瞬间就返回 ErrServerClosed，此时在途请求
	// 和 SSE 还在跑。必须在这里等 Shutdown 真正排空 —— 否则等待逻辑写了没人等，
	// 进程照样立刻退出并硬断所有连接。
	if err := <-shutdownDone; err != nil {
		observability.Error(ctx, nil, "server.shutdown", "server.serveHTTPLifecycle",
			"graceful shutdown deadline exceeded; remaining connections force-closed",
			"timeout", timeout.String(), "error", err)
	}
	return nil
}

// flushTelemetryPump 用独立预算排空 telemetry 缓冲：退出时父 ctx 已经取消，
// 沿用它等于一件都写不出去。
func flushTelemetryPump(ctx context.Context, pump *telemetryPump, timeout time.Duration) {
	if pump == nil {
		return
	}
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := pump.Close(flushCtx); err != nil {
		observability.Error(ctx, nil, "telemetry.flush", "server.flushTelemetryPump",
			"telemetry buffer was not fully flushed before exit",
			"timeout", timeout.String(), "error", err)
	}
	if dropped := pump.dropped(); dropped > 0 {
		observability.Error(ctx, nil, "telemetry.dropped", "server.flushTelemetryPump",
			"telemetry events were dropped because the async buffer was full", "dropped", dropped)
	}
}

func shutdownHTTPServer(ctx context.Context, srv *http.Server, streams *streamRegistry, timeout time.Duration) error {
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	// 先收口长连接：SSE 永远不 idle，Shutdown 单靠自己只会耗光 deadline 再硬断。
	drained := streams.drain()
	observability.Info(ctx, nil, "server.shutdown", "server.shutdownHTTPServer", "graceful shutdown started",
		"timeout", timeout.String(), "long_lived_streams", drained)
	err := srv.Shutdown(shutdownCtx)
	if err != nil {
		// deadline 到了还有连接没排空：强制关闭，避免需要 SIGKILL 才能退出。
		_ = srv.Close()
	}
	return err
}
