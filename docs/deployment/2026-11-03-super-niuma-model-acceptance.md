# Super Niuma 真实模型验收 — 单次试验未通过，已安全停止

## 最新结论（2026-11-03）

**FAILED / NOT ACCEPTED。已使用原授权的一次真实试验；不得自动再次调用模型。**

主 agent 修正了预算请求 ID（`attempt.model.N`，未放宽 `control.ValidID`）并修正 delivery settlement Claim 保留后，零网络预检通过：配置、真实 Runner 请求 ID、SQLite migration、Grant 均可用。两项不调用模型的 SQLite 并行 DAG/重启及 bus delivery 集成测试也通过。随后仅执行一次带 `-race` 的 opt-in 真实模型试验，没有切换 provider/model。

- 配置固定：`custom / glm-5.2`；路由指纹仍为 `eae79ff5b9fd15b22e4fac29f46e20583d8940f099106876d9ff9027b5369fe6`。测试已锁定这三个公开值，默认配置漂移会在网络前拒绝。
- 真实 intake ID：`71839e3c-8e3e-4721-9ed7-efdc13aa55fe`；run ID：`f7a898dd-4b46-4c1d-b6fd-186c97028a42`。
- 耗时：约 7.128 秒；上限 180 秒，根预算上限 40000 tokens。
- 2 个分析节点各启动 1 次 attempt；2 次模型请求预算获准，**仅 1 次有可信 usage/已确认响应**。未知项不冒充已确认成功响应。
- `analyze-sum` 成功：input 166、output 155，已知合计 321 tokens。
- `analyze-product` 失败，持久 safe code 为 `usage_unknown`。其 input/output 预留 5890/256，共 **6146 tokens 保留，不退款**；根账本计入 `6467` tokens。
- 根状态为 `needs_attention`。writer attempt=0、产物=0、verification hash=无、DesktopSender 调用=0、交付 receipt=无。没有执行返工或第二次付费试验。
- 两个请求 admission→settlement 时间区间确有交叠（由下方保留的纳秒时间戳推导），不是包级网络跟踪。不能据此宣称整个并行 DAG 已完成；原始测试成功门禁 flags 保持 false。
- SQLite 已关闭，HOME 私有 fixture 已删除；没有保留凭据、BaseURL、原始模型内容、原始响应错误、数据库或日志。仅保留脱敏事实收据。

### 失败解释与未决问题

原始测试显示 `analysis_state_read_failed`，这是一个**次级诊断**：执行器遇到模型执行失败/未知 usage 后立即取消总 context，随后的状态读取使用该已取消 context。独立清理上下文读取到真实持久状态为 `needs_attention / usage_unknown`。原始 JSON 不作改写。

本次证据只能确认该请求**未形成可信的完整 usage 结算**，不能据此断言 provider 一定漏发 usage，也不能断言具体 HTTP 状态、凭据错误、响应截断或协议解析原因。原始响应未保存，不追加网络诊断。后续应先完善安全错误码/响应元数据诊断，任何新的付费试验需单独授权。

原始请求明细中的 `settled=true` 表示 Budget.Settle 回调返回成功；未知 usage 不会因此退款或成为已知用量。保守预留仍体现在根账本的 6467 tokens 中。

测试将后续故障状态读取改为有界、只读的取消独立上下文，避免上述次级错误掩盖根状态；这项诊断修订没有触发重新调用模型。

### 已执行验证

- `SUPER_NIUMA_REAL_MODEL_PREFLIGHT_ONLY=1 go test ... TestSuperRealModelAcceptance`：预检通过后明确 SKIP；0 模型请求。
- `TestSQLiteIntegrationTwoWorkersDAGAndRestart`、`TestSQLiteIntegrationDeliveryViaDurableBus`：离线 fixture 测试通过，不充当真实模型验收。
- `SUPER_NIUMA_REAL_MODEL_ACCEPTANCE=1 go test -race ... TestSuperRealModelAcceptance -count=1 -timeout=4m`：**FAIL，且只运行一次**。
- 原生 App 未启动、截图未采集、Feishu 未接入；没有 commit/push。

### 验收边界

这次失败试验证实了真实模型请求进入持久控制面，以及未知用量被保守处理后停止后续执行；**未证明模型→writer→独立验收→交付的成功闭环**。即使后续本测试通过，也仍不是 Feishu/原生截图/同一工单统一串联的最终验收。

---

# 历史：零网络预检阻塞（首次真实试验之前）

日期：2026-11-03。

## 历史预检结论

**BLOCKED / 未验收通过。没有发起任何真实模型请求，没有创建付费验收工单。**
默认执行测试明确显示 SKIP；Go 测试进程的 PASS 只表示编译/跳过正常，不能作为真实模型验收通过。

## 脱敏配置检查

配置来源严格为当前用户 `config.LoadForCWD(os.UserHomeDir())`；未修改配置，未猜测或切换模型。

```json
{
  "configured": true,
  "provider": "custom",
  "model": "glm-5.2",
  "route_fingerprint": "eae79ff5b9fd15b22e4fac29f46e20583d8940f099106876d9ff9027b5369fe6"
}
```

未输出凭据、BaseURL、URL query、原始错误或用户会话数据。

## 已确认的集成阻塞

- Runner 逐请求预算 ID 采用 `attempt/model/N`。
- `control.ValidID` 只允许字母、数字、`_ - . :`，拒绝 `/`；`requestBudget.Reserve` 首先调用该校验。
- 预检通过真实 Runner 的真实 query 路径，使用必定拒绝的 Budget 记录实际请求 ID，**在 HTTP 请求之前停止**，再用真实控制面校验函数验证该 ID。
- 预检结果：`runner_request_id_rejected_by_control_ValidID`。如果不修复，真实控制面会在每次模型请求之前拒绝预算，无法完成执行。
- 建议主 agent 统一为例如 `attempt.model.N` 并更新执行器对应测试；无需放宽所有控制面标识符的字符集。本子任务只允许改验收测试和本文，未修改范围外实现。

## 已准备的验收测试

测试路径：`$REPO/internal/super/platform/real_acceptance_test.go`，包名 `platform_test`。

- 默认跳过；真实调用需 `SUPER_NIUMA_REAL_MODEL_ACCEPTANCE=1`。
- `SUPER_NIUMA_REAL_MODEL_CONFIG_ONLY=1`：只报告上述脱敏配置，零模型请求。
- `SUPER_NIUMA_REAL_MODEL_PREFLIGHT_ONLY=1`：零网络 Runner/预算契约预检，随后才检查 SQLite 和 Grant；当前在预算 ID 校验处停止。
- 真实闭环准备为一个 workorder intake → 私有真实 SQLite/Engine → 两个并行只读分析节点 → 一个依赖 writer → `FileVerifier` 独立语义 hash 校验和当前 Claim.Token 的 WriteReceipt freshness → 持久事件/outbox → 真实 `DesktopSender` → 关闭/重开数据库验证 receipt/ACK。
- 分析节点各 `MaxTurns=1 / MaxOutputTokens=256`；writer 为 `3 / 512`；根预算上限 40000 tokens，整次 deadline 180 秒。
- 任一模型/凭据/usage/控制面错误立即取消；手动分阶段驱动 Engine，不执行自动返工，也不自动重试付费工单。
- 使用 HOME 下短路径、0700 私有临时 fixture；正常/失败均关闭 SQLite 并删除 fixture。
- 真实试验启动时才向本文件追加一次性 paid-attempt marker；目前尚未声明该 marker，修复零网络阻塞后仍可使用原授权的一次试验。已有 marker 时拒绝自动二次付费运行。

## 历史预检事实收据

| 项目 | 事实 |
|---|---|
| 真实工单数 / run 数 | 0 / 0 |
| worker 执行 attempts | 0 |
| 真实模型调用 / usage | 0 / 0 |
| 零网络 Runner admission probe | 1，预算主动拒绝，无 HTTP 调用 |
| 产物 / verification hash / delivery receipt | 未产生 |
| SQLite 端到端迁移和执行验收 | 尚未执行；被更早的契约预检阻止 |
| 临时 fixture | 已清理 |
| 凭据、数据库、原始日志残留 | 无 |
| App / 截图 / Feishu | 未启动 / 未采集 / 未接入 |
| commit / push | 未执行，遵守本子任务要求 |

## 验收边界

即使后续本测试通过，也只能证明**真实模型＋持久控制面闭环**。`DesktopSender` 的本地回执不是原生 UI 点击或人类签收；本报告不等于 Feishu、原生截图和同一真实模型工单统一串联的最终端到端验收。该最终验收由主 agent 负责。

<!-- super-niuma-real-model-paid-attempt-claimed -->

Paid trial claimed. Outcome pending; do not rerun automatically.

# Super Niuma — real-model control-plane acceptance

## Outcome and boundary

- Outcome: **failed**. Failure code: analysis_state_read_failed.
- Exactly one authorized work order; no automatic second paid attempt or revision.
- This evidence covers real model responses, SQLite intake/control state, two read-only analysis workers, a dependent writer, trusted write receipts, independent semantic file verification, durable event/outbox delivery to DesktopSender, and restart readback **only where the flags below confirm success**.
- DesktopSender is the local receipt implementation, not native UI interaction or human acknowledgement. This is **not** the final unified Feishu + native screenshot + model end-to-end acceptance.
- Model-request counts are observed successful budget admissions; confirmed response counts require known usage and successful settlement. An unconfirmed admission is not claimed to prove a provider response. Parallel windows are admission-to-settlement observations, not packet-level traces.
- No credentials, URLs, raw model text, raw error bodies, database or logs are retained. The private HOME fixture is removed after closing SQLite.
- Opt-in: SUPER_NIUMA_REAL_MODEL_ACCEPTANCE=1. Default skip means **not accepted**, not a pass. The append-only receipt marker is a one-shot guard; do not remove it to retry without new authorization.
- Native app execution/screenshots and external-channel acceptance remain the main agent's responsibility. No commit or push performed by this task.

## Sanitized receipt

```json
{
  "configuration": {
    "configured": true,
    "provider": "custom",
    "model": "glm-5.2",
    "route_fingerprint": "eae79ff5b9fd15b22e4fac29f46e20583d8940f099106876d9ff9027b5369fe6"
  },
  "outcome": "failed",
  "failure_code": "analysis_state_read_failed",
  "model_attempt_started": true,
  "started_at": "2026-11-03T07:54:11.10857Z",
  "finished_at": "2026-11-03T07:54:18.237053Z",
  "budget_limit": 40000,
  "deadline_seconds": 180,
  "intake_id": "71839e3c-8e3e-4721-9ed7-efdc13aa55fe",
  "run_id": "f7a898dd-4b46-4c1d-b6fd-186c97028a42",
  "status": "needs_attention",
  "executor_attempts": 2,
  "admitted_model_requests": 2,
  "confirmed_model_responses": 1,
  "usage": {
    "input_tokens": 166,
    "output_tokens": 155
  },
  "budget_accounted": 6467,
  "jobs": [
    {
      "id": "analyze-sum",
      "status": "succeeded",
      "attempt_id": "6d79d558-1332-4809-849f-d427ab7504e3",
      "attempts": 1,
      "usage": {
        "input_tokens": 166,
        "output_tokens": 155
      },
      "usage_known": true
    },
    {
      "id": "analyze-product",
      "status": "failed",
      "attempt_id": "5ddf6bf5-ed51-4eac-9b11-a82a655445d3",
      "attempts": 1,
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false,
      "safe_code": "usage_unknown"
    },
    {
      "id": "write-artifact",
      "status": "queued",
      "attempt_id": "",
      "attempts": 0,
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false
    }
  ],
  "requests": [
    {
      "id": "5ddf6bf5-ed51-4eac-9b11-a82a655445d3.model.1",
      "job_id": "analyze-product",
      "attempt_id": "5ddf6bf5-ed51-4eac-9b11-a82a655445d3",
      "started_at_ns": 1793692451143498000,
      "settled_at_ns": 1793692458230816000,
      "reserved_input": 5890,
      "reserved_output": 256,
      "usage": {
        "input_tokens": 0,
        "output_tokens": 0
      },
      "usage_known": false,
      "settled": true
    },
    {
      "id": "6d79d558-1332-4809-849f-d427ab7504e3.model.1",
      "job_id": "analyze-sum",
      "attempt_id": "6d79d558-1332-4809-849f-d427ab7504e3",
      "started_at_ns": 1793692451148586000,
      "settled_at_ns": 1793692458008632000,
      "reserved_input": 5882,
      "reserved_output": 256,
      "usage": {
        "input_tokens": 166,
        "output_tokens": 155
      },
      "usage_known": true,
      "settled": true
    }
  ],
  "verification_attempt_id": "",
  "parallel_analysis_observed": false,
  "parallel_model_reservation_windows_observed": false,
  "freshness_verified": false,
  "semantic_json_verified": false,
  "durable_notice_acknowledged": false,
  "restart_persistence_verified": false,
  "desktop_send_count": 0,
  "database_closed": true,
  "fixture_removed": true
}
```
