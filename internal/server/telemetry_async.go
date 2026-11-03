package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/konglong87/super-niuma/internal/telemetry"
)

// telemetryPump 把请求路径上的慢 sink 集中包一层异步缓冲(AUDIT-P1-24)。
//
// 每个请求都会新建一个 telemetry.Emitter，所以异步 worker 不能挂在 Emitter 上 ——
// 它必须比单个请求活得久。这里在服务启动时构造一次，退出时由 serveHTTPLifecycle
// flush。
type telemetryPump struct {
	recorder  telemetry.Sink
	exporters []telemetry.Sink
	async     []*telemetry.AsyncSink
}

// newTelemetryPump 包装 opts 里所有会做 I/O 的 sink：写库的 RecorderSink 和外呼的
// 导出 sink。没有可包的东西时返回 nil，调用点会退回同步路径。
func newTelemetryPump(opts Options) *telemetryPump {
	pump := &telemetryPump{}
	wrap := func(sink telemetry.Sink) telemetry.Sink {
		async := telemetry.NewAsyncSink(sink, telemetry.AsyncConfig{})
		if async == nil {
			return sink
		}
		pump.async = append(pump.async, async)
		return async
	}
	if opts.TenantService != nil {
		pump.recorder = wrap(telemetry.RecorderSink{Recorder: opts.TenantService})
	}
	for _, sink := range opts.TelemetrySinks {
		if sink == nil {
			continue
		}
		pump.exporters = append(pump.exporters, wrap(sink))
	}
	if len(pump.async) == 0 {
		return nil
	}
	return pump
}

// Close 排空缓冲。ctx 到期返回 ctx.Err()，代表没能在预算内 flush 完。
func (p *telemetryPump) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	for _, async := range p.async {
		if err := async.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// dropped 汇总因缓冲塞满被丢弃的事件数，供测试与排查使用。
func (p *telemetryPump) dropped() uint64 {
	if p == nil {
		return 0
	}
	var total uint64
	for _, async := range p.async {
		total += async.Dropped()
	}
	return total
}

// telemetrySinksFor 交出该挂到本次请求 Emitter 上的慢 sink。
//
// 只有 serveHTTPLifecycle 会装配 pump —— NewHandler 那条路没有退出钩子，缓冲永远
// 不会被 flush，静默丢事件比同步写慢更糟，所以那边保持同步。
func telemetrySinksFor(opts Options, withRecorder bool) []telemetry.Sink {
	if opts.telemetryAsync != nil {
		return opts.telemetryAsync.sinks(withRecorder)
	}
	sinks := make([]telemetry.Sink, 0, len(opts.TelemetrySinks)+1)
	if withRecorder && opts.TenantService != nil {
		sinks = append(sinks, telemetry.RecorderSink{Recorder: opts.TenantService})
	}
	return append(sinks, opts.TelemetrySinks...)
}

func (p *telemetryPump) sinks(withRecorder bool) []telemetry.Sink {
	if p == nil {
		return nil
	}
	sinks := make([]telemetry.Sink, 0, len(p.exporters)+1)
	if withRecorder && p.recorder != nil {
		sinks = append(sinks, p.recorder)
	}
	return append(sinks, p.exporters...)
}

// requestTelemetrySinks 是 HTTP 请求路径的入口：未鉴权的请求不该记到租户名下。
func requestTelemetrySinks(opts Options, r *http.Request) []telemetry.Sink {
	return telemetrySinksFor(opts, requestHasTelemetryAuth(opts, r))
}
