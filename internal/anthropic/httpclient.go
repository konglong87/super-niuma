package anthropic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// provider 侧 HTTP 超时预算。
//
// 这里刻意不用 http.Client.Timeout：那是一个覆盖「连接 + 请求 + 读完整个 body」的
// 硬 deadline，会把一次几分钟的长流式回答直接砍断。超时语义因此拆成三段：
//
//   - 建流（providerResponseHeaderTimeout）：从请求发出到响应头返回。
//     注意没有用 http.Transport.ResponseHeaderTimeout —— 它只在 HTTP/1 生效，
//     而官方端点会协商到 HTTP/2，所以这一段自己在 httpTraceClient.Do 里计时。
//   - 整个流（providerStreamIdleTimeout）：流建立后相邻两次读之间的最大间隔。
//     服务端既不推 chunk 也不关连接时，这一段负责把会话叫醒，而不是无限等待。
//   - 非流式 body（providerResponseBodyTimeout）：读完整个响应体的总时长。
const (
	providerDialTimeout           = 10 * time.Second
	providerKeepAlive             = 30 * time.Second
	providerTLSHandshakeTimeout   = 10 * time.Second
	providerExpectContinueTimeout = 1 * time.Second
	providerIdleConnTimeout       = 90 * time.Second
	providerMaxIdleConns          = 128
	providerMaxIdleConnsPerHost   = 32
	providerResponseHeaderTimeout = 120 * time.Second
	providerStreamIdleTimeout     = 120 * time.Second
	providerResponseBodyTimeout   = 120 * time.Second
)

// errProviderTimeout 标记「分段超时守卫主动取消了请求」。守卫实现超时的唯一手段是
// cancel request context（见 responseGuard.trip），所以这类错误链里必然含
// context.Canceled，和用户按 ESC 取消长得一模一样。上层必须靠这个标记把两者分开：
// 网关卡死要能切到备用 provider，用户取消要立刻停下。
var errProviderTimeout = errors.New("provider timeout")

// providerTimeoutError 同时挂在底层网络错误和 errProviderTimeout 两条链上，因此
// errors.Is(err, context.Canceled) 和 errors.As(err, &urlErr) 都保持原有行为。
type providerTimeoutError struct {
	phase   string
	timeout time.Duration
	err     error
}

func newProviderTimeoutError(phase string, timeout time.Duration, err error) error {
	return &providerTimeoutError{phase: phase, timeout: timeout, err: err}
}

func (e *providerTimeoutError) Error() string {
	return fmt.Sprintf("%s after %s: %v", e.phase, e.timeout, e.err)
}

func (e *providerTimeoutError) Unwrap() []error {
	return []error{e.err, errProviderTimeout}
}

// providerHTTPTimeouts 是 httpTraceClient 的超时预算；<=0 表示该段不设限。
type providerHTTPTimeouts struct {
	responseHeader time.Duration
	streamIdle     time.Duration
	responseBody   time.Duration
}

// providerTimeouts 是所有 provider 客户端的分段超时预算。做成变量是为了让测试能把
// 120s 压到毫秒级 —— 否则超时相关的路径根本跑不起来。
var providerTimeouts = defaultProviderHTTPTimeouts()

func defaultProviderHTTPTimeouts() providerHTTPTimeouts {
	return providerHTTPTimeouts{
		responseHeader: providerResponseHeaderTimeout,
		streamIdle:     providerStreamIdleTimeout,
		responseBody:   providerResponseBodyTimeout,
	}
}

func (t providerHTTPTimeouts) enabled() bool {
	return t.responseHeader > 0 || t.streamIdle > 0 || t.responseBody > 0
}

// newProviderTransport 取代 http.DefaultTransport：默认值的 MaxIdleConnsPerHost=2
// 会让并发子代理场景反复做 TCP+TLS 握手。
func newProviderTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   providerDialTimeout,
			KeepAlive: providerKeepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          providerMaxIdleConns,
		MaxIdleConnsPerHost:   providerMaxIdleConnsPerHost,
		IdleConnTimeout:       providerIdleConnTimeout,
		TLSHandshakeTimeout:   providerTLSHandshakeTimeout,
		ExpectContinueTimeout: providerExpectContinueTimeout,
	}
}

type retryAfterCaptureKey struct{}

// retryAfterCapture 把错误响应上的 Retry-After 从 HTTP 层顺给上层的重试决策。
// go-openai 的 APIError/RequestError 只带状态码和 body，响应头在别处拿不到
// （AUDIT-P1-08）。
type retryAfterCapture struct {
	mu    sync.Mutex
	delay time.Duration
}

func (c *retryAfterCapture) set(delay time.Duration) {
	if c == nil || delay <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delay = delay
}

func (c *retryAfterCapture) get() time.Duration {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.delay
}

func withRetryAfterCapture(ctx context.Context, capture *retryAfterCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, retryAfterCaptureKey{}, capture)
}

func retryAfterCaptureFromContext(ctx context.Context) *retryAfterCapture {
	capture, _ := ctx.Value(retryAfterCaptureKey{}).(*retryAfterCapture)
	return capture
}

// retryAfterFromHeader 解析标准的 Retry-After（秒数或 HTTP-date），以及 OpenAI 实际会
// 下发的毫秒级 retry-after-ms。返回 0 表示响应没给出可用的等待时长。
func retryAfterFromHeader(header http.Header, now time.Time) time.Duration {
	if raw := strings.TrimSpace(header.Get("Retry-After-Ms")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if delay := at.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}

func isEventStreamResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}

// responseGuard 给一次 provider HTTP 调用挂上分段超时。超时后调用 cancel 取消
// request context —— 取消是唯一能唤醒阻塞在 body 读上的 goroutine 的手段。
type responseGuard struct {
	cancel func()

	mu      sync.Mutex
	timer   *time.Timer
	timeout time.Duration
	perRead bool
	fired   bool
	closed  bool
}

// arm 切换到下一段超时；perRead 为 true 时每次读到数据都会续期（空闲检测）。
func (g *responseGuard) arm(timeout time.Duration, perRead bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.fired {
		return
	}
	g.perRead = perRead
	g.timeout = timeout
	if timeout <= 0 {
		g.stopLocked()
		return
	}
	if g.timer == nil {
		g.timer = time.AfterFunc(timeout, g.trip)
		return
	}
	g.timer.Reset(timeout)
}

func (g *responseGuard) trip() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.fired = true
	g.mu.Unlock()
	g.cancel()
}

func (g *responseGuard) touch() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.fired || !g.perRead || g.timer == nil || g.timeout <= 0 {
		return
	}
	g.timer.Reset(g.timeout)
}

func (g *responseGuard) state() (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fired, g.timeout
}

func (g *responseGuard) close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.stopLocked()
	g.mu.Unlock()
	g.cancel()
}

func (g *responseGuard) stopLocked() {
	if g.timer != nil {
		g.timer.Stop()
		g.timer = nil
	}
}

// guardedBody 把 responseGuard 的生命周期绑到响应体上：读推进计时器，关闭释放 context。
type guardedBody struct {
	body    io.ReadCloser
	guard   *responseGuard
	message string
}

func (b *guardedBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.guard.touch()
	}
	if err != nil && !errors.Is(err, io.EOF) {
		if fired, timeout := b.guard.state(); fired {
			return n, newProviderTimeoutError(b.message, timeout, err)
		}
	}
	return n, err
}

func (b *guardedBody) Close() error {
	b.guard.close()
	return b.body.Close()
}
