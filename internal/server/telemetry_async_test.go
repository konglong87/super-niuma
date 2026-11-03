package server

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/konglong87/super-niuma/internal/telemetry"
)

// slowTelemetrySink 站在 RecorderSink（一次 MySQL 写）和 HTTPSink（一次外呼）的
// 位置上：这两个才是请求路径上真正的成本。
type slowTelemetrySink struct {
	delay time.Duration

	mu     sync.Mutex
	events []string
}

func (s *slowTelemetrySink) Emit(_ context.Context, event telemetry.Event) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.events = append(s.events, event.Name)
	s.mu.Unlock()
	return nil
}

func (s *slowTelemetrySink) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func telemetryTestOptions(sink telemetry.Sink) Options {
	return Options{
		AuthToken:       "token",
		Workspace:       "/tmp/work",
		ShutdownTimeout: 10 * time.Second,
		TelemetrySinks:  []telemetry.Sink{sink},
	}
}

func getHealth(t *testing.T, baseURL string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}
}

// 中间件每个请求发 started + finished 两个事件；同步投递时这两次 sink 延迟会原样
// 加到 API 延迟上(AUDIT-P1-24)。
func TestRequestLatencyIsNotChargedForSlowTelemetrySink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &slowTelemetrySink{delay: 300 * time.Millisecond}
	baseURL, wait := startTestServer(t, ctx, telemetryTestOptions(sink), nil)

	start := time.Now()
	getHealth(t, baseURL)
	elapsed := time.Since(start)

	cancel()
	if err := wait(15 * time.Second); err != nil {
		t.Fatalf("serve: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("request took %s; the two telemetry events (300ms each) leaked into the request path", elapsed)
	}
}

// 反方向断言：异步之后事件不能丢，退出时缓冲必须被排空。
func TestTelemetryBufferIsFlushedOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sink := &slowTelemetrySink{delay: 150 * time.Millisecond}
	baseURL, wait := startTestServer(t, ctx, telemetryTestOptions(sink), nil)

	getHealth(t, baseURL)
	// 请求已经返回，但两个事件加起来要 300ms，此刻必然还压在缓冲里。
	if delivered := len(sink.names()); delivered == 2 {
		t.Fatalf("sink drained before shutdown, the test would not prove anything")
	}

	cancel()
	if err := wait(15 * time.Second); err != nil {
		t.Fatalf("serve: %v", err)
	}

	names := sink.names()
	if len(names) != 2 {
		t.Fatalf("delivered %v after shutdown, want both api.request.started and api.request.finished", names)
	}
}

func TestTelemetrySinksFallBackToSynchronousWithoutPump(t *testing.T) {
	sink := &slowTelemetrySink{}
	opts := telemetryTestOptions(sink)
	// NewHandler 那条路没有退出钩子，缓冲永远不会 flush，所以必须保持同步。
	sinks := telemetrySinksFor(opts, false)
	if len(sinks) != 1 || sinks[0] != telemetry.Sink(sink) {
		t.Fatalf("sinks = %#v, want the raw sink", sinks)
	}
}

func TestTelemetryPumpGatesRecorderOnAuth(t *testing.T) {
	sink := &slowTelemetrySink{}
	opts := telemetryTestOptions(sink)
	opts.TenantService = &fakeTenantService{}
	pump := newTelemetryPump(opts)
	if pump == nil {
		t.Fatal("newTelemetryPump returned nil despite a tenant service and an exporter")
	}
	t.Cleanup(func() { _ = pump.Close(context.Background()) })

	if got := len(pump.sinks(true)); got != 2 {
		t.Fatalf("authenticated sinks = %d, want recorder + exporter", got)
	}
	if got := len(pump.sinks(false)); got != 1 {
		t.Fatalf("unauthenticated sinks = %d, want exporter only", got)
	}
}

func TestTelemetryPumpIsNilWhenThereIsNothingSlowToWrap(t *testing.T) {
	if pump := newTelemetryPump(Options{}); pump != nil {
		t.Fatalf("newTelemetryPump = %#v, want nil", pump)
	}
}
