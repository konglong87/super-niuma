# Computer Use Runtime 重构与修复方案

> Historical architecture plan. The P0-A through P0-F implementation work is
> reflected in the current code and the current implementation ledger. Read
> [Computer Use 使用说明](../usage/computer-use.md) for public scope, limitations
> and current acceptance status. The original planning instructions and paths
> below are retained only as engineering history; they are not user-facing
> operational commands.

- 日期：2026-10-10
- 基线：`main` / `origin/main`，`HEAD=108f365`
- 目标：停止重复 fresh model retry，先把 Computer Use 的 turn contract、runtime 生命周期、截图资产生命周期和证据闭环重构正确，再做一次真实模型验收。
- 适用范围：`internal/computeruse`、`internal/computerbridge`、`internal/tools/computeruse`、`desktop-v2`、`internal/computerbackend/macos`、`native/macos` 及对应测试/验收脚本。
- 明确不改变：不放宽 focus/window/permission 安全校验；不重放未知输入；不把 near-pass 记为 pass；第二显示器、正式签名/公证/分发继续按用户要求延期。

> 这不是“再调一次 prompt”的方案，而是把 Computer Use 从“多处拼接结果的工具调用”改成“由一个长生命周期 runtime 原子地产生并发布一个 turn result”。

---

## 1. 先给结论：为什么之前重复执行很多次，却没有实质进展

### 1.1 真正的瓶颈不是模型，而是 turn contract

当前模型已经完成过以下动作链：

```text
launch_app
→ observe
→ click
→ command+a
→ type 1+1=2
→ click send
→ wait
```

并且 WorkBuddy 已显示真实回复；失败发生在 `wait` 后的 observation/image 闭环，而不是模型不会点击或不会输入。

当前链路是：

```text
provider event
→ tool wrapper
→ session-control / local service
→ Unix HTTP bridge
→ Go Controller
→ macOS Go backend
→ Swift helper
→ observation / after-image
→ 再次请求 image asset
→ wrapper 事后拼接 timeline
→ provider 下一轮
```

这条链路存在多个独立状态源：

- provider conversation event；
- SQLite/session-control 状态；
- Wails 内的 `computerManager` / `Controller`；
- Swift helper session/generation；
- Go backend 的 observation 和 image map；
- bridge 的单次 HTTP response；
- wrapper 事后保存的 receipt/screenshot/evidence。

所以会出现“动作已经执行，但下一张图没有回传”“receipt 已存在，但 image 二次读取失败”“失败结果返回全零 receipt”“Wails UI 生命周期杀掉 helper”“外部 CUA 抢焦点”等问题。继续刷新模型只能改变时序，不能消除状态分裂。

### 1.2 之前反复 retry 的根因

1. **把模型验收当成诊断手段**：每次 fresh run 同时改变了窗口焦点、provider latency、Wails 生命周期和截图时序，无法把失败归因到一个层级。
2. **结果不是原子的**：`wait` 返回 receipt，截图又通过另一个请求读取，二次读取失败时无法保证 provider 看到同一个 turn 的完整结果。
3. **wrapper 承担了 runtime 的职责**：wrapper 在末尾从多个接口拼接 action timeline，导致“事后推断”替代了“实时 authoritative event”。
4. **运行时依赖 UI context**：`computerAgentService.lifetime` 当前绑定 Wails context；UI/host shutdown 会影响 provider 使用的 active runtime。
5. **证据和权限混在一起**：receipt、after-action preview、下一次可输入 observation 的语义没有彻底分离。
6. **还有环境噪声**：同名 app、外部 CUA 保活、focus 变化和 Mac 锁屏会污染模型验收；这些应由 deterministic scripted regression 先隔离。

### 1.3 现有代码哪些不要重写

现有基础并非全部错误，以下应保留并复用：

- `TargetID` / `TargetRegistry` / `TargetBinding`；
- session owner 校验；
- observation 一次性消费；
- action ID 去重和 unknown outcome 禁止重放；
- native generation/epoch、Pause/Stop、permission fail-closed；
- native dispatch state：`not_started` / `complete` / `partial` / `unknown`；
- `PublicErrorCode` 脱敏；
- macOS helper 的输入前校验、目标窗口校验和 screenshot dimension 校验；
- 已通过的 native lifecycle、single-input、Stop 和 helper safety tests。

重构的原则是：**不重造 native input safety，而是重构结果发布、生命周期和边界。**

---

## 2. 目标架构

### 2.1 目标拓扑

```text
Provider / Query runtime
        │
        │ one ComputerUse tool call = one ComputerTurnResult
        ▼
ComputerUse Tool Adapter
        │ 只做 schema/鉴权/Provider message mapping，不拼接结果
        ▼
Computer Runtime（长生命周期、按 conversation/owner 隔离）
 ├── TurnCoordinator       串行化一个 turn，分配 TurnID/ActionID
 ├── SessionStore           session FSM、observation authority、receipt
 ├── TargetRegistry         target_id → trusted launch/window policy
 ├── NativeBackend          Go backend + macOS helper
 ├── MediaStore             turn-scoped PNG、TTL、hash、读取状态
 ├── EventBus               实时事件，不依赖 UI polling
 ├── CancellationController Pause/Stop/timeout 的统一状态机
 └── RuntimeSupervisor      helper/bridge health、重启、shutdown
        │
        ├── Unix bridge client/server：只传 trusted command/result
        ├── Wails UI client：panel、preview、controls、status
        └── acceptance observer：订阅事件、保存证据、只读校验
```

### 2.2 生命周期原则

- Provider runtime 不由 Wails WebView 的 DOM、panel 或 polling 驱动。
- Wails UI 是 client，不是 Computer Use authority。
- `runtimeCtx` 独立于 `windowContext()`；UI 关闭只影响 UI client，不应自动杀 active turn。
- 只有显式 `Stop`、runtime shutdown、permission revoke、不可恢复 helper fault 才能终止 runtime。
- helper 由 `RuntimeSupervisor` 管理；helper 替换后旧 observation/action 全部失效，但不自动重放。
- 所有操作都经过同一个 `TurnCoordinator`，不允许 UI、provider、acceptance 各自直接调用 backend。

### 2.3 一次 turn 的目标时序

```text
1. Tool 收到 provider action
2. Runtime 校验 owner/session/observation/action
3. SessionStore 原子预留 action_id + 消费 observation
4. NativeBackend dispatch
5. NativeBackend 返回 dispatch_state + outcome + after evidence metadata
6. Runtime 立即落盘/内存提交 ActionReceipt（无论截图是否成功）
7. 若允许，Runtime 在同一 turn 内生成 next observation
8. MediaStore 在同一 turn 内登记并验证 PNG；不再依赖 wrapper 二次猜测
9. Runtime 组装 ComputerTurnResult
10. Bridge 一次返回 turn result；Tool 一次映射成 provider tool_result + image
11. EventBus 广播同一个 turn result；UI/acceptance 只订阅，不补写事实
```

关键点：**receipt 先提交，observation/screenshot 是同一 turn 的附属结果；截图失败不能抹掉 receipt，也不能生成全零 receipt。**

---

## 3. 新的核心契约：`ComputerTurnResult`

### 3.1 建议新增类型

建议新增 `internal/computeruse/turn.go`，不要把所有字段继续堆到 `ActionReceipt`：

```go
type ObservationState string

const (
    ObservationNotRequested ObservationState = "not_requested"
    ObservationReady        ObservationState = "ready"
    ObservationUnavailable  ObservationState = "unavailable"
    ObservationInvalidated  ObservationState = "invalidated"
)

type ScreenshotState string

const (
    ScreenshotNotRequested ScreenshotState = "not_requested"
    ScreenshotReady        ScreenshotState = "ready"
    ScreenshotUnavailable  ScreenshotState = "unavailable"
    ScreenshotExpired      ScreenshotState = "expired"
)

type ComputerTurnResult struct {
    ProtocolVersion string         `json:"protocol_version"`
    TurnID          string         `json:"turn_id"`
    SessionID       string         `json:"session_id"`
    ActionID        string         `json:"action_id"`
    ActionKind      ActionKind     `json:"action_kind"`
    DispatchState   DispatchState  `json:"dispatch_state"`
    Outcome         Outcome        `json:"outcome"`
    Verification    VerificationStatus `json:"verification"`

    Receipt         ActionReceipt  `json:"receipt"`
    Observation     *Observation   `json:"observation,omitempty"`
    ObservationState ObservationState `json:"observation_state"`
    Screenshot      *MediaRef      `json:"screenshot,omitempty"`
    ScreenshotState ScreenshotState `json:"screenshot_state"`

    ErrorCode       string         `json:"error_code,omitempty"`
    RetryPolicy     RetryPolicy    `json:"retry_policy"`
    Sequence        uint64         `json:"sequence"`
    StartedAt       time.Time      `json:"started_at"`
    CompletedAt     time.Time      `json:"completed_at"`
    Duration        time.Duration  `json:"duration"`
}
```

字段命名可以按现有项目风格调整，但语义必须保留。`ActionReceipt` 继续作为 action 事实；`ComputerTurnResult` 负责表达这一次 provider turn 的完整闭环。

### 3.2 结果不变量

每一个返回结果必须满足：

1. `TurnID`、`SessionID`、`ActionID` 非空且稳定；
2. `Receipt.ActionID == ActionID`，`Receipt.SessionID == SessionID`；
3. `Receipt.IsTerminal()` 为真；
4. `Outcome == unknown` 时 `Verification == unknown`，且 `RetryPolicy == never`；
5. action 已 dispatch 后，即使 native/bridge/PNG 失败，也必须返回非零 receipt；
6. 失败工具结果不能使用全零 `ActionReceipt` 伪装成真实 receipt；
7. `ObservationState == ready` 时，`Observation` 非空、session/ID/TTL/geometry 合法；
8. `ScreenshotState == ready` 时，`Screenshot` 非空且能在本 turn 内通过 MediaStore 读取；
9. `Observation` 是下一次可输入 authority；`Receipt.After` 只是 after-action evidence，除非显式通过 `ObservationState=ready` 发布；
10. `Screenshot` 失败不得触发 input replay；
11. provider、UI、wrapper 都只能消费结果，不能事后改变结果事实。

### 3.3 retry policy

```go
type RetryPolicy string

const (
    RetryNever       RetryPolicy = "never"
    RetryObserveOnly RetryPolicy = "observe_only"
    RetryResumeOnly  RetryPolicy = "resume_only"
)
```

- `unknown` / `partial` / dispatch 后 transport error：`never`；
- focus/geometry/permission 的 pre-dispatch rejection：可由模型重新 observe，但不能重放原 action；
- screenshot unavailable 且 receipt 已 executed：`observe_only`，且必须告诉模型“输入已执行，禁止重复输入”；
- Pause 后 Resume：必须显式 resume + fresh observe；
- Stop：终止，不产生 retry。

---

## 4. 分层职责与改造边界

### 4.1 `internal/computeruse`：协议与状态机

职责：

- 定义 `ComputerTurnResult`、state enums、retry policy；
- 提供 `TurnCoordinator` / `TurnStore` 接口；
- 在 session 内原子记录 `BeginAction → Receipt → Observation`；
- 保证 action/result 顺序和 unknown 不重放；
- 不依赖 macOS、Wails、provider、SQLite。

不负责：

- PNG 编码/截图 API；
- provider message 格式；
- Wails 生命周期。

### 4.2 `internal/computerbackend/macos`：native adapter

职责保持单一：

- helper RPC；
- capture/input/focus/window/permission；
- 返回完整 dispatch/outcome/after image metadata；
- 提供受控的 `ImageReader` / `MediaReader`。

改动原则：

- 不删除现有 generation、epoch、input broker、安全校验；
- 把“after image 已生成但读不到”的状态显式暴露；
- 不在 backend 内猜测 provider 是否需要重试；
- native error code 保持可诊断但经过公共码映射。

### 4.3 `internal/computerbridge`：一次 RPC 返回完整 turn

新增高优先级操作：

```text
OpExecuteTurn = "execute_turn"
```

它返回：

- `ComputerTurnResult` 的 JSON；
- 可选的 PNG bytes（base64 或受控 asset ref）；
- `MediaRef` 的 hash/尺寸/TTL；
- 不再要求 tool 先 execute，再单独 image，再由 wrapper 拼接。

兼容策略：

- 保留旧 `OpExecute` / `OpImage`，仅用于迁移期和 UI preview；
- provider tool 一旦切换到 `OpExecuteTurn`，禁止 fallback 到“execute + image + posthoc merge”；
- 旧接口不能生成新的模型成功路径，避免两套语义继续漂移。

### 4.4 `internal/tools/computeruse`：纯 adapter

Tool 只负责：

1. 解码和校验 schema；
2. 绑定可信 owner/session/target；
3. 调用 `TurnService.ExecuteTurn`；
4. 将一个 `ComputerTurnResult` 映射为一个 provider tool result；
5. 在 `ScreenshotState=ready` 时附带图片；
6. 在失败/unknown 时输出稳定 error code 和禁止 replay 的指示。

Tool 不再负责：

- 先执行再主动调用 image；
- 事后再 Observe 来弥补缺失结果；
- 合并 provider event 与 native receipt；
- 推断 action 是否发生；
- 用全零 receipt 填补错误结果。

迁移期可保留 fallback，但必须由 feature flag 限制在测试和旧客户端，不能进入新的 fast path。

### 4.5 `desktop-v2`：runtime host 与 UI client 分离

当前 `computerManager` 同时承担：

- UI session manager；
- provider bridge authority；
- helper lifetime；
- acceptance control plane。

应拆成：

```text
internal/computerruntime/       // 可被 headless runtime 和 Wails client 复用
desktop-v2/runtime_host.go       // runtime 启动、supervisor、bridge
 desktop-v2/computer_panel*.go  // 只做 UI panel/client
```

第一阶段可以保留 `computerManager` 名字作为兼容 façade，但新代码不能继续把 provider runtime 绑定到 Wails `windowContext()`。

---

## 5. 分阶段实施计划（按优先级）

### P0-A：冻结契约，先写失败回归（最高优先级）

**目标**：在不动真实模型的情况下，先让所有丢 receipt/丢 screenshot/unknown replay 场景可确定复现。

**改动**：

- 新增 `internal/computeruse/turn.go`；
- 为 `Service` 增加独立 `TurnService` 接口，避免直接破坏旧 fake；
- `ComputerSession` 增加 turn sequence/current result/last committed receipt 读取；
- 增加 `ComputerTurnResult.Validate()`；
- 增加 scripted backend failure matrix。

**必须覆盖的用例**：

| 场景 | 期望 |
|---|---|
| pre-dispatch rejection | `OutcomeRejected` + `DispatchNotStarted` + 非零 receipt |
| native complete + after screenshot decode 失败 | `OutcomeExecuted` + `ScreenshotUnavailable`，禁止 replay |
| native partial/unknown | `OutcomeUnknown` + `RetryNever`，session quarantine |
| bridge response 丢失但 native 已 dispatch | runtime 恢复为 unknown/receipt，不生成 rejected/zero receipt |
| observation 已生成、asset 读取失败 | receipt 保留；observation metadata 状态明确；不伪造 image |
| Pause/Stop 与 input race | control wins；action 不自动重放 |
| duplicate action ID | 明确拒绝，不再次到 native |

**验收门**：focused Go tests + `go test -race`，所有结果不变量通过。

### P0-B：实现 Controller 原子 turn

**目标**：把“receipt + next observation + screenshot availability”从多个调用收敛为 Controller 内一次操作。

**建议 API**：

```go
func (c *Controller) ExecuteTurn(
    ctx context.Context,
    owner SessionOwner,
    action Action,
) (ComputerTurnResult, error)
```

内部顺序必须是：

```text
authorize
→ BeginAction
→ backend.Execute
→ 立刻 RecordReceipt
→ 若 outcome 允许，backend.Observe
→ 立即读取/登记 image
→ CommitTurnResult
→ 发布 Event
→ 返回
```

注意：

- `RecordReceipt` 必须先于任何可能失败的 screenshot/image 操作；
- backend error 与 receipt 同时返回；
- `error != nil` 不代表 receipt 可以丢弃；
- 已 dispatch 的 input 永远不自动重试；
- post-action Observe 失败只会使 `ObservationState=unavailable`，不会把已执行 action 变成 rejected。

### P0-C：新增 `execute_turn` bridge RPC

**目标**：消除 `execute → image → wrapper merge` 的网络/状态断裂。

**改动文件方向**：

- `internal/computerbridge/protocol.go`：新增 op/request/response；
- `internal/computerbridge/server.go`：dispatch `ExecuteTurn`；
- `internal/computerbridge/client.go`：单次 call 返回 turn result；
- `internal/computerbridge/validation.go`：验证 turn/result/media；
- `desktop-v2/computer_agent_service.go`：暴露 `ExecuteTurn`；
- 相关 protocol/client/server tests。

**响应限制**：

- 继续限制 PNG 大小、像素数和 MIME；
- response 必须包含 session/action/turn 绑定；
- asset 若不内嵌，必须带 `asset_ref`、TTL、hash，并保证当前 turn 内可读；
- 不允许超时后由 caller 猜测是否成功。

### P0-D：Tool 只消费原子结果

**目标**：provider 一次 tool call 获取可继续决策的完整信息。

**改动方向**：

- `internal/tools/computeruse/tool.go` 的 execute 分支改为优先调用 `TurnService`；
- 成功 action：直接返回 receipt + `observation` + observation image；
- action evidence 可以作为附加 metadata，但不再拿 evidence ID 当下一次 observation ID；
- observation/image 不可用：返回 executed receipt + 明确 `stop_without_replay`；
- 删除/禁用 tool 内二次 `captureObservation` 和 `observationImage` 串联 fast path；
- 更新 provider prompt，使其只相信同一个 `ComputerTurnResult`。

**验收门**：tool unit/golden tests 证明一个 input tool call 最多对应一个 `ExecuteTurn` bridge call，不再出现“wrapper 事后拼接”。

### P0-E：runtime 生命周期与 Wails 解耦

**目标**：关闭/重绘/替换 Wails UI 不杀掉 provider 正在使用的 Computer Use runtime。

**短期实现**：

- 从 `computerAgentService.lifetime` 移除 `windowContext()` 作为 helper lifetime；
- 引入 `computerRuntimeCtx`，由 runtime host 创建和取消；
- UI 方法只持有 client/context，不拥有 runtime cancel 权；
- `OnShutdown` 变成显式 runtime shutdown：先发 `Stop`/drain，再关闭 bridge/helper；
- runtime 每个 owner 只能有一个 active controller；replacement 先 revoke 旧 authority。

**完整实现**：

- 把 runtime/supervisor/bridge 从 Wails `main` 拆到可独立运行的 headless `computer-runtime`；
- Wails app、local provider server、acceptance observer 都作为 client；
- runtime 进程退出才代表 helper/runtime 退出；
- UI 关闭、WebView reload、panel hide 不再改变 provider session。

**验收门**：在 active turn 期间关闭/reload panel，provider 仍能收到同一个 turn result；显式 Stop 后 helper 才终止。

### P0-F：确定性验收与证据系统

**目标**：让每次失败都可归因，禁止混用旧证据。

每次 run 必须使用唯一目录：

```text
desktop-v2/build/validation/20261010/model-<commit>-<run-id>/
```

至少保存：

```text
00-build.json
01-launch.png
02-ready.png
03-new-task.png
04-input-before.png
05-input-after.png
06-send-after.png
07-reply.png
08-stop.png
turns.ndjson
provider-events.ndjson
runtime-events.ndjson
```

规则：

- 先保存 turn result，再保存图片；
- 图片必须带 `session_id/turn_id/action_id/observation_id/build_sha` metadata sidecar；
- 目录已存在直接失败，不覆盖；
- 不从旧目录、旧 process、旧 model run 取补充证据；
- screenshot 失败必须保留 receipt/turn result。

### P1：native panel / UI 事件桥重构

前置：P0 全部完成。

- UI 改成订阅 `EventBus` / runtime snapshot；
- preview 只读 MediaStore，不得刷新 observation 或改变 authority；
- Pause/Resume/Stop 走同一个 runtime control path；
- UI 显示 `dispatch_state/outcome/observation_state/screenshot_state`，不显示模糊的“执行中”；
- 旧 DOM polling 只作为兼容 fallback，最终删除。

### P2：更广 fixture、第二显示器、签名分发

- Calculator/TextEdit 等非 WorkBuddy fixture；
- 第二物理显示器；
- formal signing/distribution；
- optional GitHub binary release。

这些不阻塞 P0 Computer Use turn closure。

---

## 6. 建议的文件级任务拆分

### 核心协议层

- `internal/computeruse/turn.go`（新增）
- `internal/computeruse/model.go`
- `internal/computeruse/session.go`
- `internal/computeruse/controller.go`
- `internal/computeruse/service.go`
- `internal/computeruse/events.go`
- `internal/computeruse/turn_test.go`（新增）
- `internal/computeruse/controller_turn_test.go`（新增）

### Bridge 层

- `internal/computerbridge/protocol.go`
- `internal/computerbridge/server.go`
- `internal/computerbridge/client.go`
- `internal/computerbridge/validation.go`
- `internal/computerbridge/turn_test.go`（新增或拆分现有测试）

### Tool 层

- `internal/tools/computeruse/tool.go`
- `internal/tools/computeruse/tool_test.go`
- provider prompt/fast-path 相关 `internal/query/*_test.go`

### Desktop/runtime 层

- `desktop-v2/computer.go`
- `desktop-v2/computer_agent_service.go`
- `desktop-v2/computer_bridge.go`
- `desktop-v2/main.go`
- `desktop-v2/window.go`
- `desktop-v2/computer_test.go`
- `desktop-v2/computer_agent_service_test.go`
- 后续新建 `internal/computerruntime/*` 或 `cmd/computer-runtime/*`

### Native/backend 层

- `internal/computerbackend/macos/backend.go`
- `internal/computerbackend/macos/backend_test.go`
- `native/macos/Engine.swift`
- `native/macos/Protocol.swift`
- `native/macos/Diagnostics.swift`

native 只在 contract 需要暴露更多明确状态时修改；不要因为模型 retry 失败就重写输入路径。

---

## 7. 每个 coherent slice 的提交顺序

每一片都必须 focused test 通过后立即 commit + push 到 `origin/main`，不要堆到最后：

1. `refactor(computeruse): add atomic computer turn result contract`
2. `test(computeruse): add deterministic receipt and screenshot failure matrix`
3. `refactor(computerbridge): add execute turn rpc`
4. `refactor(computeruse): route tool through atomic turn result`
5. `refactor(desktop): detach computer runtime from Wails window lifetime`
6. `test(computeruse): verify runtime event ordering and no replay`
7. `test(computeruse): close real desktop turn with reply screenshot and stop`

每个 commit 的交付门：

```text
git status --short
git diff --check
go test <focused packages>
go test -race <focused packages>
git commit
 git push origin main
```

不要把 generated validation evidence、真实凭据、SQLite、日志提交进 Git。

---

## 8. 验收矩阵

### 8.1 自动化

```bash
go test ./internal/computeruse ./internal/computerbridge ./internal/tools/computeruse ./desktop-v2

go test -race ./internal/computeruse ./internal/computerbridge ./internal/tools/computeruse ./desktop-v2
python3 -m unittest scripts/computer-use-120s-acceptance_test.py
./scripts/build-computer-helper-macos.sh
./scripts/build-desktop-v2.sh
codesign --verify --deep --strict desktop-v2/build/bin/go-e2e.app
```

### 8.2 Native scripted acceptance

必须覆盖并看真实截图：

- cold launch/bind/observe；
- target window identity 稳定；
- position-only geometry drift 的 bounded recovery；
- click/type/key/hotkey 各一次，不能 replay；
- after screenshot 失败但 receipt 保留；
- focus change / permission revoke；
- Pause/Resume/Stop race；
- helper disconnect 后 unknown quarantine；
- UI panel hide/reopen 不影响 runtime；
- Stop receipt 和 helper close。

### 8.3 Autonomous model acceptance

唯一最终模型闭环：

```text
fresh tagged build
→ launch/ensure
→ observe ready
→ create/new task
→ click input
→ command+a
→ type 1+1=2
→ observe returned by same turn result
→ click send once
→ wait once
→ reply screenshot
→ ComputerUse stop
→ stop receipt
```

通过标准：

- 真实 WorkBuddy reply screenshot；
- 每个 input action 恰好一次 dispatch；
- 没有 unknown action replay；
- `wait` 后不依赖 wrapper 二次拼接；
- 最终有合法 Stop receipt；
- 所有 screenshots、turns、provider events 来自同一个 run/build/session；
- 证据目录和最终 JSON 均能互相校验。

任何一个条件缺失，都只能报告 near-pass/blocked，不能报告 complete。

---

## 9. 风险与明确的取舍

| 风险 | 处理方式 |
|---|---|
| response 内嵌 PNG 导致 bridge response 过大 | 保留严格大小/像素限制；必要时返回 turn-scoped asset ref，但同一 turn 必须可读 |
| action 已执行但 next observation 失败 | 保留 executed receipt，发布 `ObservationUnavailable`，禁止 replay |
| bridge timeout 无法判断 dispatch | 标记 unknown/quarantine，不猜 rejected，不重放 |
| runtime 重启 | 旧 session/observation/action 全部失效，要求显式新 session + observe |
| UI 需要 preview | 订阅 event + 只读 MediaStore，不访问 model authority |
| 兼容旧客户端 | 迁移期保留旧接口，但禁止旧接口参与新的 provider fast path |
| native helper 仍有真实 focus/capture 波动 | 用 scripted failure matrix 和 diagnostics 隔离；不通过放宽安全检查“修复” |
| 任务范围变大 | 先交付 P0 atomic turn + bridge + tool；完整独立 runtime 作为紧随其后的 P0-E slice |

---

## 10. 新会话启动提示词（可直接复制）

```text
你是这个仓库的主实现者。继续在当前工作区完成 Computer Use runtime 重构，不要再重复 fresh model retry，也不要只给方案不改代码。

工作区：<repository-root>
目标文档：<repository-root>/docs/plans/2026-10-10-computer-use-runtime-rearchitecture.md
当前基线：main，HEAD/origin/main=108f365（启动时必须再次 git pull --ff-only origin main 确认）。

强制规则：
1. 先读取 AGENTS.md、本目标文档、docs/plans/2026-09-27-computer-use-product-functions.md、docs/plans/2026-10-08-computer-use-stability.md。
2. 开始前执行 git status --short、git log -8 --oneline --decorate、git diff --stat，并记录所有 pre-existing dirty files。不要覆盖、回滚或偷偷提交它们。
3. 继续使用 main，不创建 branch/worktree；不要使用 Superpowers；不要重放任何 unknown/已 dispatch 输入。
4. 先做架构和依赖检查，再动代码。不要为了让模型验收通过而放宽 focus/window/permission/screenshot safety guard。
5. 每个 coherent slice：先写失败回归，再实现，跑 focused Go/native/race tests，审查 git diff --check，立即 commit + push origin main。commit user 必须是 konglong <konglong@com>。
6. 测试必须匹配真实用户表面：desktop 改动必须 build 当前 `.app`，native acceptance 必须看真实截图；不能只用 DOM 或日志宣布通过。
7. 所有 validation evidence 放在 <repository-root>/desktop-v2/build/validation/YYYYMMDD/ 下的全新唯一目录；禁止混用旧 run、旧 session、旧 build；完成后清理未请求的临时产物。
8. 不要把 near-pass 记为 pass。最终闭环必须同时有真实 reply screenshot、每个输入 action 一次 dispatch、合法 Stop receipt、同一 build/session 的 turn/provider/runtime evidence。

不要重写已经通过的 native safety 基础：TargetID/TargetRegistry、TargetBinding、owner 校验、observation 一次性消费、action 去重、unknown 禁止 replay、native generation/epoch、Pause/Stop/permission fail-closed、dispatch state 和公共 error code 都要保留并复用。

执行顺序（严格按优先级，不要跳到模型验收）：

P0-A：实现原子 ComputerTurnResult
- 在 internal/computeruse 新增 turn contract，明确 dispatch/outcome/verification/observation_state/screenshot_state/retry_policy。
- receipt 必须先提交；screenshot/image 失败不能丢 receipt、不能返回全零 receipt。
- unknown/partial/bridge ambiguity 永远 RetryNever，不能重放。
- 添加 deterministic fake backend failure matrix：pre-dispatch reject、complete+after-image failure、unknown、bridge lost ack、observation asset failure、Pause/Stop race、duplicate action。

P0-B：实现 Controller.ExecuteTurn
- 一次调用内部完成 authorize → BeginAction → native Execute → RecordReceipt → 可选 next Observe → MediaStore/image read → commit/publish turn result。
- 不允许 tool/wrapper 事后拼 action timeline。
- post-action observe 失败只能让 ObservationUnavailable，不能把 executed action 改成 rejected。

P0-C：新增 computerbridge execute_turn RPC
- 在 internal/computerbridge 增加 OpExecuteTurn、严格 request/response validation、session/action/turn 绑定。
- 允许 inline PNG 或 turn-scoped asset ref，但在同一个 turn 内必须可读并验证 hash/尺寸/TTL。
- 保留旧 execute/image 仅做迁移兼容；新的 provider path 不得 fallback 到旧拼接链路。

P0-D：让 internal/tools/computeruse 只消费原子结果
- tool 一次调用得到 receipt + next observation + screenshot。
- 删除/禁用 fast path 中 execute 后再 image、再 captureObservation 的 posthoc stitching。
- 失败/unknown 明确告诉模型输入是否已 dispatch；已 dispatch 绝不能 replay。

P0-E：把 runtime 生命周期从 Wails windowContext 解耦
- 引入独立 computerRuntimeCtx 和 RuntimeSupervisor；UI/panel/DOM/polling 只能做 client。
- 短期先让 provider/helper 不依赖 windowContext；随后把 runtime/bridge/helper 提取为可独立运行的 headless computer-runtime，Wails 只负责 UI。
- 显式 Stop、permission revoke、不可恢复 helper fault 才终止 runtime；替换 helper 时旧 observation/action 必须失效且不能 replay。

P0-F：自动化 + 真实验收
- focused Go tests、race tests、Python acceptance tests、helper build、tagged desktop build、codesign verify。
- 先完成 deterministic/native gates，再只跑一次新的 gpt-6-sol/high model closure：launch/observe/new task/click/command+a/type 1+1=2/send once/wait once/reply screenshot/stop。
- 必须看真实截图并保留完整闭环 evidence；失败时报告精确层级（provider/tool/bridge/controller/backend/native/focus/environment），不要再盲目 retry。

建议的提交顺序：
1. refactor(computeruse): add atomic computer turn result contract
2. test(computeruse): add deterministic receipt and screenshot failure matrix
3. refactor(computerbridge): add execute turn rpc
4. refactor(computeruse): route tool through atomic turn result
5. refactor(desktop): detach computer runtime from Wails window lifetime
6. test(computeruse): verify runtime event ordering and no replay
7. test(computeruse): close real desktop turn with reply screenshot and stop

现在开始：先完成启动检查和代码链路梳理，然后直接实施 P0-A；不要先再次运行模型验收。
```

---

## 11. 外部参考与不能照搬的边界

可以参考公开实现的架构原则，但不能假设公开仓库包含 Codex App 的私有 macOS native helper、权限服务、截图服务或内部 provider orchestration。

可借鉴的原则：

- `openai/codex` 的 app-server：把 session/thread/turn 生命周期放在长期运行的 server 中，client 通过受控协议连接，并通过事件流观察状态；
- Codex 的 app-server test client：支持连接、观察原始入站事件和在 turn 进行中重新连接/恢复，这正是本项目 wrapper 不应再事后拼接 timeline 的反例；
- Anthropic computer-use client contract：模型发出 tool call 后由 client 执行，必须返回与该调用匹配的 `tool_result`；截图是 tool result 的一部分，而不是另一次无关联的“补查”。

本项目要复用的是：

```text
long-lived runtime
+ typed request/result
+ ordered event stream
+ explicit cancellation
+ matching tool result
+ deterministic recovery
```

本项目不能照搬的是：

```text
private native capture/focus implementation
private TCC/permission broker
private provider scheduler
private Codex/Claude production retry heuristics
```

因此正确做法是把这些公开架构原则落到本仓库已有的 `Controller`、`computerbridge`、macOS helper 和 evidence system 上，而不是继续修改 prompt 或复制不可见的内部实现。

---

## 12. 完成定义

只有以下全部满足，才可以把本轮重构标记为完成：

- `ComputerTurnResult` 成为 provider Computer Use 的唯一 authoritative result；
- receipt、observation、screenshot availability、error/retry policy 在一个 turn 内有一致语义；
- action 已 dispatch 时永不返回全零 receipt，永不自动 replay；
- tool/bridge/wrapper 不再事后拼接结果；
- runtime 生命周期与 Wails UI 解耦；
- deterministic failure matrix、Go race、native safety、desktop build 全通过；
- fresh model run 得到真实 reply screenshot + Stop receipt；
- `git status --short` 干净，`HEAD == origin/main`，所有 task-owned commit 已 push。

这套顺序的 ROI 最高：先修复一次 turn 的一致性，后续 focus、截图延迟、helper 重启、UI 重绘、模型速度变化都落在同一套可验证状态机内；如果继续先跑模型，只会继续重复消耗时间而不产生新的架构信息。
