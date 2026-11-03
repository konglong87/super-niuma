package server

import (
	"context"
	"net/http"
	"time"

	"github.com/konglong87/super-niuma/internal/observability"
)

// AUDIT-P1-22: 此前只有一个 /health，既不区分 liveness/readiness、不探依赖，还要
// Bearer token —— k8s 的 livenessProbe/readinessProbe 和 LB 健康检查都得注入 token
// 才能用，而它们本来就不该持有业务凭证。
//
// 现在分成三个端点，职责各不相同：
//
//	/livez   进程还活着吗          无鉴权，不碰依赖，永远 200
//	/readyz  能接流量吗            无鉴权，逐个探 MySQL/Redis，不健康返回 503
//	/health  运维自查（含 workspace 等信息）  仍需 token，行为一字未改
//
// /livez 与 /readyz 之所以敢不鉴权：它们只回答「是/否」，不吐任何配置、路径或
// 错误详情。真实错误只进服务端日志。

// readinessProbeTimeout 是单个依赖探活的上限。探活必须比 probe 的 timeout 先返回，
// 否则 k8s 看到的是超时而不是「依赖不健康」。
const readinessProbeTimeout = 2 * time.Second

// ReadinessProbe 是 /readyz 逐个执行的依赖探活。Name 会出现在响应里，所以不要
// 放 DSN、地址这类信息。
type ReadinessProbe struct {
	Name  string
	Check func(ctx context.Context) error
}

func livenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}
}

func readinessHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		checks, ready := runReadinessProbes(r.Context(), opts.ReadinessProbes)
		status := http.StatusOK
		state := "ok"
		if !ready {
			status = http.StatusServiceUnavailable
			state = "unavailable"
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(status)
			return
		}
		writeJSONStatus(w, status, map[string]any{"status": state, "checks": checks})
	}
}

// runReadinessProbes 顺序执行探活。依赖数量个位数、每个都有 2s 上限，串行足够，
// 也让日志顺序可读。返回的 map 只有 "ok"/"unavailable" 两种值：/readyz 不鉴权，
// 把底层错误原文吐出去等于向匿名调用者泄漏拓扑。
func runReadinessProbes(ctx context.Context, probes []ReadinessProbe) (map[string]string, bool) {
	checks := make(map[string]string, len(probes))
	ready := true
	for _, probe := range probes {
		if probe.Check == nil {
			continue
		}
		probeCtx, cancel := context.WithTimeout(ctx, readinessProbeTimeout)
		err := probe.Check(probeCtx)
		cancel()
		if err != nil {
			ready = false
			checks[probe.Name] = "unavailable"
			observability.Error(ctx, nil, "health.readiness", "server.runReadinessProbes", "readiness probe failed", "probe", probe.Name, "error", err)
			continue
		}
		checks[probe.Name] = "ok"
	}
	return checks, ready
}
