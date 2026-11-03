package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

// Emitter.Emit 顺序调用每个 sink，所以任何一个慢 sink 都直接计入调用方的延迟：
// RecorderSink 是一次 MySQL INSERT，HTTPSink 是一次外呼。API 中间件每个请求发
// started + finished 两个事件，等于每请求两次同步写库加两次同步外呼，外部 APM
// 抖动会原样变成 API 延迟(AUDIT-P1-24)。
//
// AsyncSink 把这类 sink 挪到后台：Emit 只做一次入队，真正的 I/O 交给固定数量的
// worker。代价有两个，都是有意接受的：
//   - Workers > 1 时投递顺序不再等于 Emit 顺序。事件自带 OccurredAt，消费端按时间
//     排序，不依赖到达顺序。
//   - 队列有界。塞满时丢弃并计数，而不是回压请求路径 —— 但丢弃会经由 Emit 的返回值
//     冒泡成一条 error 日志，不会静默。
type AsyncSink struct {
	inner   Sink
	queue   chan asyncEnvelope
	timeout time.Duration
	wg      sync.WaitGroup

	mu     sync.RWMutex
	closed bool

	dropped atomic.Uint64
}

type asyncEnvelope struct {
	ctx   context.Context
	event Event
}

// AsyncConfig 的零值可用，缺省值见 normalize。
type AsyncConfig struct {
	// QueueSize 是待投递事件的上限，超出即丢弃。
	QueueSize int
	// Workers 是后台投递协程数。
	Workers int
	// EmitTimeout 是单个事件在后台的投递期限；<=0 表示不额外设限。
	EmitTimeout time.Duration
}

const (
	defaultAsyncQueueSize   = 1024
	defaultAsyncWorkers     = 2
	defaultAsyncEmitTimeout = 5 * time.Second
)

// ErrAsyncQueueFull 表示事件因缓冲已满被丢弃。
var ErrAsyncQueueFull = errors.New("telemetry async queue is full")

func (c AsyncConfig) normalize() AsyncConfig {
	if c.QueueSize <= 0 {
		c.QueueSize = defaultAsyncQueueSize
	}
	if c.Workers <= 0 {
		c.Workers = defaultAsyncWorkers
	}
	if c.EmitTimeout == 0 {
		c.EmitTimeout = defaultAsyncEmitTimeout
	}
	return c
}

// NewAsyncSink 包装 inner 并立即启动 worker。调用方必须在退出前调用 Close，
// 否则缓冲里的事件不会落地。inner 为 nil 时返回 nil，方便调用点直接透传。
func NewAsyncSink(inner Sink, cfg AsyncConfig) *AsyncSink {
	if inner == nil {
		return nil
	}
	cfg = cfg.normalize()
	sink := &AsyncSink{
		inner:   inner,
		queue:   make(chan asyncEnvelope, cfg.QueueSize),
		timeout: cfg.EmitTimeout,
	}
	sink.wg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		go sink.run()
	}
	return sink
}

func (s *AsyncSink) run() {
	defer s.wg.Done()
	for envelope := range s.queue {
		if err := s.deliver(envelope); err != nil {
			observability.Error(envelope.ctx, nil, "telemetry.async_deliver_error", "telemetry.AsyncSink.run", "async telemetry sink failed",
				"event_name", envelope.event.Name,
				"sink", fmt.Sprintf("%T", s.inner),
				"error", err,
			)
		}
	}
}

// Emit 入队后立即返回。Close 之后到达的事件同步投递，宁可慢也不静默丢弃。
func (s *AsyncSink) Emit(ctx context.Context, event Event) error {
	if s == nil || s.inner == nil {
		return nil
	}
	envelope := asyncEnvelope{ctx: detachContext(ctx), event: event}
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return s.deliver(envelope)
	}
	select {
	case s.queue <- envelope:
		s.mu.RUnlock()
		return nil
	default:
		s.mu.RUnlock()
		s.dropped.Add(1)
		return fmt.Errorf("%w (event %q dropped)", ErrAsyncQueueFull, event.Name)
	}
}

// Close 停止收新事件、把缓冲里剩下的全部投递完再返回。ctx 到期时返回
// ctx.Err()，此时仍有 worker 在跑 —— 调用方应把这当成「没能在预算内 flush 完」
// 的告警，而不是可以忽略的收尾错误。
func (s *AsyncSink) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.queue)
	s.mu.Unlock()

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()
	if ctx == nil {
		<-drained
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dropped 返回因缓冲已满被丢弃的事件数。
func (s *AsyncSink) Dropped() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

func (s *AsyncSink) deliver(envelope asyncEnvelope) error {
	ctx := envelope.ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	return s.inner.Emit(ctx, envelope.event)
}

// detachContext 保留 ctx 上的值（tenant / user / trace 都靠它解析）但摘掉取消与
// 超时：请求早就返回了，带着它的 cancel 进后台等于保证写库失败。
func detachContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
