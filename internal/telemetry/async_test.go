package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

// blockingSink 模拟一次慢 I/O（MySQL 写 / 外呼 APM）。
type blockingSink struct {
	delay   time.Duration
	err     error
	mu      sync.Mutex
	events  []Event
	ctxErrs []error
	release chan struct{}
}

func (s *blockingSink) Emit(ctx context.Context, event Event) error {
	if s.release != nil {
		<-s.release
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	s.mu.Unlock()
	return s.err
}

func (s *blockingSink) snapshot() ([]Event, []error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...), append([]error(nil), s.ctxErrs...)
}

func closeAsyncSink(t *testing.T, sink *AsyncSink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// 同步 sink 下这段耗时至少是 2 * delay；异步后调用方应当近似零成本。
func TestAsyncSinkDoesNotBlockCaller(t *testing.T) {
	inner := &blockingSink{delay: 200 * time.Millisecond}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1})
	t.Cleanup(func() { closeAsyncSink(t, sink) })

	start := time.Now()
	for i := 0; i < 2; i++ {
		if err := sink.Emit(context.Background(), Event{Name: "api.request.started"}); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Emit blocked the caller for %s, want well under the 200ms sink delay", elapsed)
	}
}

// 可观测性的反方向断言：异步之后事件不能丢，退出时缓冲必须被 flush。
func TestAsyncSinkFlushesBufferOnClose(t *testing.T) {
	inner := &blockingSink{delay: 2 * time.Millisecond}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 2, QueueSize: 256})

	const total = 100
	for i := 0; i < total; i++ {
		if err := sink.Emit(context.Background(), Event{Name: "api.request.finished"}); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	if events, _ := inner.snapshot(); len(events) == total {
		t.Fatalf("sink drained before Close, the test would not prove anything")
	}

	closeAsyncSink(t, sink)

	events, _ := inner.snapshot()
	if len(events) != total {
		t.Fatalf("delivered %d events after Close, want %d", len(events), total)
	}
}

// Close 之后仍可能有零星事件（shutdown 期的日志）；它们同步投递而不是被吞掉。
func TestAsyncSinkDeliversEventsEmittedAfterClose(t *testing.T) {
	inner := &blockingSink{}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1})
	closeAsyncSink(t, sink)

	if err := sink.Emit(context.Background(), Event{Name: "server.shutdown"}); err != nil {
		t.Fatalf("Emit after Close: %v", err)
	}
	events, _ := inner.snapshot()
	if len(events) != 1 || events[0].Name != "server.shutdown" {
		t.Fatalf("events after Close = %+v", events)
	}
}

// 请求 ctx 在 handler 返回时就被取消；后台投递必须摘掉取消但留下 tenant/user 值，
// 否则 RecorderSink 既解析不出租户也写不进库。
func TestAsyncSinkDetachesRequestCancellation(t *testing.T) {
	inner := &blockingSink{release: make(chan struct{})}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1})

	ctx, cancel := context.WithCancel(observability.WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1"))
	if err := sink.Emit(ctx, Event{Name: "api.request.finished"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	cancel() // 请求结束，早于后台投递。
	close(inner.release)
	closeAsyncSink(t, sink)

	events, ctxErrs := inner.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	if ctxErrs[0] != nil {
		t.Fatalf("delivery ctx was cancelled: %v", ctxErrs[0])
	}
}

func TestAsyncSinkKeepsContextValues(t *testing.T) {
	var got string
	done := make(chan struct{})
	inner := SinkFunc(func(ctx context.Context, _ Event) error {
		got = observability.TenantKey(ctx)
		close(done)
		return nil
	})
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1})
	t.Cleanup(func() { closeAsyncSink(t, sink) })

	ctx := observability.WithRequestValues(context.Background(), "trace-1", "user-1", "tenant-1")
	if err := sink.Emit(ctx, Event{Name: "api.request.finished"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sink was never called")
	}
	if got != "tenant-1" {
		t.Fatalf("tenant key in delivery ctx = %q, want tenant-1", got)
	}
}

// sink 失败既不能拖垮调用方，也不能连累队列里其他事件。
func TestAsyncSinkSurvivesSinkFailure(t *testing.T) {
	inner := &blockingSink{err: errors.New("mysql is down")}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1})

	for i := 0; i < 5; i++ {
		if err := sink.Emit(context.Background(), Event{Name: "api.request.finished"}); err != nil {
			t.Fatalf("Emit %d: %v", i, err)
		}
	}
	closeAsyncSink(t, sink)

	if events, _ := inner.snapshot(); len(events) != 5 {
		t.Fatalf("delivered %d events, want 5 despite the sink erroring", len(events))
	}
}

// 缓冲塞满时丢弃而不是回压请求路径，但丢弃必须计数且从 Emit 返回。
func TestAsyncSinkDropsWhenQueueIsFullInsteadOfBlocking(t *testing.T) {
	inner := &blockingSink{release: make(chan struct{})}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 1, QueueSize: 2})

	// 1 个被 worker 取走并卡在 release 上，2 个占满队列，第 4 个开始丢。
	var dropped int
	start := time.Now()
	for i := 0; i < 16; i++ {
		if err := sink.Emit(context.Background(), Event{Name: "api.request.finished"}); err != nil {
			if !errors.Is(err, ErrAsyncQueueFull) {
				t.Fatalf("Emit %d: %v", i, err)
			}
			dropped++
		}
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Emit blocked for %s once the queue filled up", elapsed)
	}
	if dropped == 0 {
		t.Fatal("expected overflow to be reported through Emit")
	}
	if sink.Dropped() != uint64(dropped) {
		t.Fatalf("Dropped() = %d, want %d", sink.Dropped(), dropped)
	}

	close(inner.release)
	closeAsyncSink(t, sink)
}

func TestAsyncSinkCloseIsIdempotent(t *testing.T) {
	sink := NewAsyncSink(&blockingSink{}, AsyncConfig{Workers: 1})
	closeAsyncSink(t, sink)
	closeAsyncSink(t, sink)
}

func TestAsyncSinkNilInnerIsNil(t *testing.T) {
	if sink := NewAsyncSink(nil, AsyncConfig{}); sink != nil {
		t.Fatalf("NewAsyncSink(nil) = %v, want nil", sink)
	}
	var sink *AsyncSink
	if err := sink.Emit(context.Background(), Event{Name: "x"}); err != nil {
		t.Fatalf("nil AsyncSink Emit: %v", err)
	}
	if err := sink.Close(context.Background()); err != nil {
		t.Fatalf("nil AsyncSink Close: %v", err)
	}
}

// 多 goroutine 并发 Emit 与 Close 竞争，配合 -race 使用。
func TestAsyncSinkConcurrentEmitAndClose(t *testing.T) {
	inner := &blockingSink{}
	sink := NewAsyncSink(inner, AsyncConfig{Workers: 4, QueueSize: 64})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 32; j++ {
				_ = sink.Emit(context.Background(), Event{Name: "api.request.finished"})
			}
		}()
	}
	wg.Wait()
	closeAsyncSink(t, sink)
}
