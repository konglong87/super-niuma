# 子代理 Closure Gate 设计方案

本文记录一个已定位但**尚未实现**的缺口：子代理有循环熔断和硬工具策略，但没有
closure gate。文档的目的是把实测事实、两个方向相反的问题、以及分步落地路径固定
下来，避免后续实现时凭印象动手。

**状态：方案，未实现。** 只有"命名纠正"这一步已落地（`runtimeAgentEvidenceFollowUpReminderStatus`）。

## 1. 实测事实

以下均已核验，实现时不需要重复确认。

### 1.1 子代理有什么、没什么

`internal/agentruntime/runtime.go`：

| 机制 | 状态 | 位置 |
| --- | --- | --- |
| 循环熔断 | **有**，每个 run 独立 `Tracker` | `runtime.go:752` |
| 熔断软提示注入 | **有** | `runtime.go:1449` |
| 硬工具策略 `toolpolicy.Check` | **有** | `runtime.go:2329` |
| task contract（L0–L5 分级） | **无** | — |
| completion gate | **无** | — |
| pre-tool closure gate | **无** | — |

`grep Gate internal/agentruntime/runtime.go` 零命中。

### 1.2 硬 gate 对子代理无感知

`internal/query/closure_gate.go`：

- `grep capabilityloop` = **0 命中**
- 无 `recentAgentEvidence` 引用

也就是说 closure gate 不知道子代理这个概念存在。

### 1.3 父级证据收集器把 Task 当零证据

全部按工具名白名单，`Task` 不在任何一个里面：

| 函数 | 位置 | 认可的工具 |
| --- | --- | --- |
| `collectReadEvidence` | `closure_gate.go:1121` | read / grep / glob / ls / bash |
| `searchEvidenceObserved` | `closure_gate.go:1760` | 同上 |
| `broadSearchEvidenceObserved` | `closure_gate.go:1777` | 同上 |
| `successfulBashCommandObserved` | `closure_gate.go:2065` | 仅 `strings.EqualFold(call.Name, "Bash")` |

### 1.4 唯一的缓解是软提示，不是强制

`runtimeAgentEvidenceFollowUpReminderStatus`（`internal/query/query.go:5813`）文案是
"Do not finalize until each listed sub-agent next_action is executed..."，但它只是
`runtimeStatusText` 的一个 section（`query.go:4801` append），仅 code 模式注入，
**没有任何代码路径依据它阻断**。原名带 `Gate` 字样，读起来像强制，已改名纠正。

## 2. 两个方向相反的问题

### 问题 A：合法委派被误拦（假阳性）

```
父 agent 把跑测试委派给子代理
  → 子代理真的执行了 go test
  → 父 agent 如实回报"测试通过"
  → finalTextClaimsTested 命中（词表含 "tests pass" / "测试通过"，closure_gate.go:1634）
  → successfulTestEvidenceObserved 在父级 ToolCalls 里找不到 Bash 测试命令
  → block_continue 打回
```

**越正确地使用子代理，越容易被 gate 拦。** 同理影响 committed / pushed / tagged /
searched / readEvidence 等全部 claim。

### 问题 B：子代理可谎报（漏拦）

子代理侧没有 completion gate，它的最终文本不经 `final_claim` 校验就作为 tool result
进入父上下文。

**这条相对安全**：因为 §1.3 的白名单，父级不会把子代理的声明当成自己的证据，所以
"借子代理洗白声明"这条路是堵住的。残余风险是父模型读到"子代理说测试通过"后据此决策。

### 为什么不能各自单独修

两者修法互相冲突：

- 给 `Task` 开证据白名单 → 解决 A，但**打开 B 的洗白路径**
- 只在 agentruntime 接 completion gate → 解决 B，但 A 依旧

只有先让子代理的证据"经过校验"，父级才敢采信它。所以顺序是固定的：**先 B 后 A。**

## 3. 抽包可行性（已调研）

### 3.1 没有循环依赖

```
go list -deps ./internal/agentruntime | grep golang-cc/internal/query   → 0
go list -deps ./internal/query | grep golang-cc/internal/agentruntime   → 0
```

两个包互不依赖。**障碍不是 import 环，而是要搬多少共享类型。**

### 3.2 `closure_gate.go` 依赖的 query 包内标识符

| 标识符 | 引用次数 |
| --- | --- |
| `ToolTrace` | 51 |
| `closureLevel` | 9 |
| `completionVerification` | 6 |
| `runtimeTaskType` | 3 |
| `shellIntent` | 1 |

### 3.3 两边的 `ToolTrace` 几乎同构

```
query.ToolTrace        (query.go:321)   ID Name Input Output IsError FileChanges verification contextMessages
agentruntime.ToolTrace (runtime.go:128) ID Name Input Output IsError                         contextMessages
```

agentruntime 版是 query 版的子集，少 `FileChanges` 和 `verification`。而 closure gate
需要：

- Name / Input / Output / IsError → 证据收集
- FileChanges → `delta_record`
- verification → `repair_evidence`

所以抽出的包应自带 trace 类型，两边转换过去；agentruntime 侧需要补 `FileChanges`
（`tools.FileChange` 已由 tools 包提供，子代理执行工具时本就能拿到）。

## 4. 分步落地路径

| 步 | 内容 | 爆炸半径 | 验收 |
| --- | --- | --- | --- |
| 0 | 命名纠正 + 注释说明软提示无强制 | `B0_LOCAL` | **已完成** |
| 1 | 新建叶子包 `internal/closure`，搬入 `closureLevel`、`taskContract`、gate 规则表、证据收集器与自带 trace 类型；`query` 改为调用它，行为零变化 | `B3_GLOBAL_RUNTIME` | 全量测试 + golden test 无漂移；prompt bytes / token / turn / tool call 对比无变化（纯搬迁，任何差异都是 bug） |
| 2 | `agentruntime` 接入 pre-tool + completion gate，子代理返回结构化 evidence 清单 | `B2_MODE` | 子代理谎报"tests pass"但无 Bash 证据 → 被拦；正常子代理任务不退化 |
| 3 | 父级采信"经子代理 gate 校验过"的 evidence，解决问题 A | `B5_SHARED_STATE` | 父级委派子代理跑测试后如实回报 → 不被拦；同时子代理未校验的声明仍不算证据 |

**第 1 步是纯搬迁，必须与行为变更分开提交。** 把搬迁和语义变更混在一个 commit 里，
出问题时无法二分定位。

按 [AGENTS.md](../../AGENTS.md) 的规则，第 1 步作为 `B3_GLOBAL_RUNTIME` 必须补主流
任务回归，并给出 prompt bytes、token、turn、tool call、延迟和 cache 的对比证据；第 3 步
作为 `B5_SHARED_STATE` 必须验证授权、负向路径、执行后 readback 和审计。

## 5. 相关代码锚点

| 位置 | 作用 |
| --- | --- |
| `internal/query/closure_gate.go:368` | `completionGate` 五条规则入口 |
| `internal/query/closure_gate.go:905` | `finalClaimGateResult` 声称 vs 证据比对 |
| `internal/query/closure_gate.go:434` | `preToolClosureGate` 事前门控 |
| `internal/query/query.go:5813` | `runtimeAgentEvidenceFollowUpReminderStatus` 软提示 |
| `internal/agentruntime/runtime.go:752` | 子代理循环熔断接入点 |
| `internal/agentruntime/runtime.go:2329` | 子代理 `toolpolicy.Check` 接入点 |
