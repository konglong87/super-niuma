# Quota Settle 抗 panic + Fail-Open 修复方案

> 日期:2026-07-15
> 状态:缺陷 1、2 已实施并通过 TDD(2026-07-15);缺陷 3(MemoryStore TTL 兜底)作为可选后续未实施。详见第 8 节。
> 关联:[../architecture/module_capability_assessment.md](../architecture/module_capability_assessment.md)、[tenant_quota_usage_technical_plan.md](tenant_quota_usage_technical_plan.md)
> 结论前置:quota 是本次评估中**唯一有真实代码缺陷**的模块,三个缺陷均已代码核实。ROI 最高、风险最低,建议最先修。

## 1. 缺陷清单(均已核实)

| # | 缺陷 | 严重性 | 证据 |
| --- | --- | --- | --- |
| 1 | `Settle` 不经 `defer`,panic 时并发计数永久泄漏 | 🔴 正常路径就能触发生产事故 | `server.go:518`、`mobile.go:850-919`、`quota.go:280-298` |
| 2 | `fail-open` 逃生阀文档承诺、代码缺失 | 🔴 Redis 一挂全租户 429 | `grep FAIL_OPEN` 空;文档 `tenant_quota_usage_technical_plan.md` 承诺 |
| 3 | `MemoryStore` 并发计数无 TTL,泄漏后需重启 | 🟠 纵深防御缺失 | `quota.go:225-232` 无过期;对比 Redis `redis.go:68,181` 有 `EXPIRE` |

## 2. 缺陷 1:Settle 不抗 panic

### 2.1 根因

`Reserve` 成功后 `concurrent++`(`quota.go:276`),必须由 `Settle` 的 `concurrent--`(`quota.go:287-289`)配对回收。但所有调用点都是**业务函数返回后直接调用**,不是 `defer`:

```go
// server.go:512-518 —— /query 现状
reservation, reserved := reserveQueryQuota(r.Context(), opts, quota.SourceQuery, "/query", req)
if reserved.err != nil {
    writeQuotaHTTPError(w, reserved.err)
    return
}
res, err := queryFn(r.Context(), req)      // ← 若此处 panic
settleQueryQuota(r.Context(), opts, reservation, res, err)  // ← 这一行被跳过,concurrent 泄漏
```

`mobile.go` 更危险:`StreamQueryFunc`(`mobile.go:850`)是长连接,其后到 `settleQueryQuota`(`mobile.go:919`)之间还有 `UpsertMessage`、`writeMobileRealtimeEvent`、`telemetry.Emit` 等大量代码,任一 panic 都会跳过 Settle。

后果:`MemoryStore`(默认部署,无 Redis 时)的 `concurrent` 只增不减 → 租户被 `ErrConcurrentLimitExceeded`(`quota.go:270`)**永久锁死**,`MemoryStore` 无 TTL 必须重启进程。Redis 有 24h TTL 自愈,但 24h 也不可接受。

### 2.2 修复(方案 B:defer 兜底 + once 防重)

**为什么不能简单 double-settle**:`Settle` 会 `concurrent--` 且经 `SettleTenantQuota` 写 ledger,重复调用会重复扣减 + 重复记账。因此用 `once` 保证恰好一次。

**为什么不用纯 defer 替换显式调用**:显式调用记录了准确的 `result`/`err`/`status`;保留它、只在 panic 或异常退出时由 defer 兜底,不改变正常记账时序,最安全。

统一封装(建议新增 `server.go` helper):

```go
// settleGuard 保证一次 reserve 恰好一次 settle,并在 panic 时兜底回收并发计数。
// 用法:
//   settle := newSettleGuard(ctx, opts, reservation)
//   defer settle.recover()          // panic 兜底
//   res, err := queryFn(...)
//   settle.do(res, err)             // 正常记账(准确 status)
type settleGuard struct {
    ctx         context.Context
    opts        Options
    reservation quota.Reservation
    once        sync.Once
}

func newSettleGuard(ctx context.Context, opts Options, r quota.Reservation) *settleGuard {
    return &settleGuard{ctx: ctx, opts: opts, reservation: r}
}

func (g *settleGuard) do(res query.Result, err error) {
    g.once.Do(func() { settleQueryQuota(g.ctx, g.opts, g.reservation, res, err) })
}

// recover 必须以 defer 调用;panic 时先兜底 settle 再 re-panic 交给上层 recovery。
func (g *settleGuard) recover() {
    if rec := ffrecover(); rec != nil {
        g.do(query.Result{}, fmt.Errorf("panic during query: %v", rec))
        panic(rec)
    }
}
```

> 注:`recover()` 只能在 defer 直接调用的函数内生效,故 `settleGuard.recover` 本身要作为 `defer g.recover()` 调用;上面的 `ffrecover()` 仅为示意,实际实现要把 `recover()` 内联进 defer 闭包。见 2.3 的落地写法。

### 2.3 各调用点落地写法

**/query(`server.go:512-525`)**:

```go
reservation, reserved := reserveQueryQuota(r.Context(), opts, quota.SourceQuery, "/query", req)
if reserved.err != nil {
    writeQuotaHTTPError(w, reserved.err)
    return
}
settle := newSettleGuard(r.Context(), opts, reservation)
defer func() {
    if rec := recover(); rec != nil {
        settle.do(query.Result{}, fmt.Errorf("panic during query: %v", rec))
        panic(rec) // 交给 gin.Recovery(),需确认已挂载
    }
}()
res, err := queryFn(r.Context(), req)
settle.do(res, err) // 正常记账,once 保证不与 defer 重复
if err != nil {
    observability.Error(...)
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
}
```

其余两个同构调用点同样处理:`server.go:1155`、`server.go:1603`。

**mobile streaming(`mobile.go:850-919`)**:在 `StreamQueryFunc` 调用前建 guard + defer,把 error 分支的 `settleQueryQuota(...,err)`(`mobile.go:853`)和 success 分支的 `settleQueryQuota(...,nil)`(`mobile.go:919`)都改为 `settle.do(result, <err>)`:

```go
settle := newSettleGuard(ctx, opts, reservation)
defer func() {
    if rec := recover(); rec != nil {
        settle.do(query.Result{}, fmt.Errorf("panic during mobile stream: %v", rec))
        panic(rec)
    }
}()
result, err := opts.StreamQueryFunc(ctx, ..., writer)
// ... error 分支:settle.do(result, err)
// ... success 分支:settle.do(result, nil)
```

`settleReservedMobileQuota`(`mobile.go:978`)内部的 `settleQueryQuota` 同样改走 guard(若该路径独立于上面的 stream,则各自建自己的 guard)。

### 2.4 re-panic 安全性前提

`defer` 里 `panic(rec)` 会把 panic 交回上层。**必须确认 router 已挂载 `gin.Recovery()`**(或等价 recovery middleware)。核实方法:

```bash
grep -n "gin.Recovery\|gin.Default\|Use(.*[Rr]ecover" internal/server/*.go
```

- 若已挂 recovery:按上文 re-panic。
- 若未挂:defer 里不要 re-panic,改为记录 error + 尝试写 500(注意 `w` 可能已部分写入),并补挂 `gin.Recovery()`。

## 3. 缺陷 2:Fail-Open 逃生阀

### 3.1 根因

`RedisStore.Reserve` 存储故障时原样返回 err(`redis.go:109-110`)→ `writeQuotaHTTPError` 变 429/402。Redis 抖动 = 全体启用配额的租户被拒。文档 `tenant_quota_usage_technical_plan.md` 承诺过 `GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN`,但 `grep FAIL_OPEN` 证实**代码里不存在**。

### 3.2 修复

关键:必须区分**配额超限**(应继续 fail-closed 拒绝)与**存储故障**(fail-open 时放行)。在 `reserveQueryQuota`(server.go)或 `ReserveTenantQuota`(`tenant/service.go:1750`)拿到 err 后:

```go
func isQuotaLimitError(err error) bool {
    return errors.Is(err, quota.ErrRateLimited) ||
        errors.Is(err, quota.ErrDailyTokenLimitExceeded) ||
        errors.Is(err, quota.ErrDailyMessageLimitExceeded) ||
        errors.Is(err, quota.ErrConcurrentLimitExceeded)
}

func quotaFailOpen() bool {
    return strings.EqualFold(os.Getenv("GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN"), "true")
}

// Reserve 之后:
if err != nil {
    if isQuotaLimitError(err) {
        return reservation, reserveResult{err: err} // 配额超限:永远 fail-closed
    }
    // 存储/基础设施故障
    if quotaFailOpen() {
        observability.Warn(ctx, nil, "quota.fail_open", "server.reserveQueryQuota",
            "quota store unavailable, failing open", "error", err)
        telemetry.Emit(ctx, telemetry.Event{Name: "quota.fail_open", Category: telemetry.CategoryTenant, Status: telemetry.StatusError, Error: err.Error()})
        return reservation, reserveResult{} // 放行,但本次不计并发(需在 Settle 侧对应处理)
    }
    return reservation, reserveResult{err: err} // 默认 fail-closed
}
```

注意:fail-open 放行的请求没有成功 `Reserve`,其 `reservation.RequestID` 应保持空,使后续 `Settle` 因 `reservation.RequestID == ""`(`server.go:2124`)自动跳过,避免对未预扣的请求做 `concurrent--`。

### 3.3 默认值与安全

- 默认 `false`(fail-closed),只在运维确认 Redis 故障时临时开启。
- 开启期间必须有 telemetry 告警,避免长期裸奔。

## 4. 缺陷 3:MemoryStore 并发计数 TTL 兜底(P1 纵深防御)

即使缺陷 1 修好,仍建议给 `MemoryStore` 加"in-flight 超时回收",对齐 Redis 的 TTL 语义:

```go
type inflight struct {
    startedAt time.Time
}
type tenantCounters struct {
    // ... 现有字段
    inflightByRequest map[string]inflight // 新增:按 RequestID 跟踪 in-flight
}
```

- `Reserve` 记录 `requestID → startedAt`,`concurrent = len(inflight)`(清理超时后)。
- `Settle` 删除该 `requestID`。
- 惰性清理:每次 `Reserve`/`Settle` 时回收 `now - startedAt > 30min`(可配置)的 in-flight。

这样即便某次 Settle 丢失,超时后自动回收,不再"永久"锁死。优先级低于缺陷 1(defer 修复直接消除主因),作为加固。

## 5. 补测试清单(quota 包 7% → 目标 ≥70%)

| 测试 | 验证点 |
| --- | --- |
| `TestMemoryStoreReserveSettleRoundtrip` | Reserve 后 Settle 使 concurrent 归零 |
| `TestMemoryStoreConcurrentLimitEnforced` | 达到 MaxConcurrentRequests 时拒绝(无 check-then-act 竞态) |
| `TestMemoryStoreSettleTokenReconciliation` | `actual < reserved` 与 `actual > reserved` 的 token 回补(`quota.go:292-296`) |
| `TestMemoryStoreConcurrentReserveNoOversell` | 多 goroutine 并发 Reserve 不超卖 |
| `TestSettleGuardSettlesOnPanic` | 注入 panic 的 queryFn,验证 concurrent 归零(不泄漏) |
| `TestSettleGuardOnceNoDoubleSettle` | 显式 do + defer 兜底只结算一次(ledger 一条) |
| `TestQuotaFailOpenOnStoreError` | Redis 故障 + `FAIL_OPEN=true` → 放行 + 告警 |
| `TestQuotaFailClosedOnLimitError` | 配额超限即使 `FAIL_OPEN=true` 也拒绝 |

## 6. 验收标准

- [ ] `go test ./internal/quota ./internal/server -count=1` 全绿。
- [ ] `go test ./internal/quota -cover` 语句覆盖率 ≥70%。
- [ ] panic 注入测试证明:queryFn panic 后 tenant 的 concurrent 计数归零,不再锁死。
- [ ] 无 double-settle:同一请求 ledger 恰好一条(SQL 校验 `SELECT count(*) FROM tenant_quota_ledger WHERE request_id=?` = 1)。
- [ ] fail-open 真机验证:
  ```bash
  # 关闭 Redis / 指向不可达地址后
  GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN=true  → curl /query 返回 200 + telemetry quota.fail_open
  GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN=false → curl /query 返回 429/402
  ```
- [ ] 文档 `tenant_quota_usage_technical_plan.md` 的 fail-open 描述与实现一致(不再是承诺)。

## 7. 风险与回滚

- **re-panic 依赖上层 recovery**:落地前先核实 `gin.Recovery()` 已挂载(见 2.4)。
- **fail-open 是安全敏感开关**:默认 `false`,变更需 code review;开启必须伴随告警。
- **改动范围小**:缺陷 1、2 集中在 `server.go`、`mobile.go`、`tenant/service.go`,不动 `Reserve`/`Settle` 的核心判定逻辑(那部分是正确的),回滚只需还原 helper 接入点。
- **顺序建议**:先缺陷 1(defer,消除主因)→ 补测试 → 缺陷 2(fail-open)→ 缺陷 3(TTL 兜底,可延后)。

## 8. 实施状态(2026-07-15)

缺陷 1、2 已按本方案 TDD 实施并通过验证;缺陷 3 作为可选纵深防御未实施。

- **缺陷 1 已实施**:新增 `quotaSettler`(`server.go`,即方案中的 settleGuard),以 `defer settleOnPanic()` + `once` 语义覆盖四个泄漏点——`/query`、OpenAI 非流式、OpenAI 流式、`mobileStreamChat`。测试 `TestQueryEndpointSettlesQuotaWhenRunnerPanics` 先复现泄漏(RED:`settleCalls=0`)再验证修复(GREEN:`settleCalls=1`)。
- **缺陷 2 已实施**:`reserveQueryQuota` 用 `isQuotaLimitError` 区分配额超限(fail-closed)与存储/基础设施故障(`GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN=true` 时 fail-open),降级放行记 `observability.Error`(项目无 Warn 级)。测试:`TestQueryEndpointFailsOpenWhenQuotaStoreErrors`、`TestQueryEndpointFailsClosedOnLimitEvenWhenFailOpen`。
- **缺陷 3 未实施**:缺陷 1 已消除泄漏主因,MemoryStore in-flight TTL 兜底暂缓。
- **测试与覆盖率**:新增 `internal/quota/memory_test.go`、`internal/quota/config_test.go`,quota 包覆盖率 **7% → 88.0%**;`go test ./internal/quota ./internal/server -count=1` 全绿,`go vet` / `gofmt` / `git diff --check` 通过。
- **前提已确认**:`gin.Recovery()` 挂载于 `server.go:410`,`defer` 中 re-panic 安全。
