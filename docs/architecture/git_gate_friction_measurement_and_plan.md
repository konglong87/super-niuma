# Git 工作流 Gate 摩擦：度量方案与候选优化

**状态：待度量，未实施。** 本文给出一个候选优化（方案 A）和它的判定依据。**先量再决定，拿到数据前不要动代码。**

本文写成可以冷启动执行：换一台机器、换一个会话，照 §4 的步骤跑即可，不需要先读别的文档。

---

## 1. 要回答的问题

一次 commit + push 的完整工作流，当前被拆成 **3 次工具调用**：

```
① git status --short --branch && git diff --name-status && git diff --cached --name-status   ← 必须单独一次
② git add -A && git commit -m ... && git push                                                ← 明确要求合成一条
③ git status / git log / git rev-parse @{u}                                                  ← 必须单独一次
```

第 ① 步和第 ③ 步为什么不能和动作链在同一条命令里？两者的道理**强度不同**：

| 拆分 | 是否有道理 | 理由 |
| --- | --- | --- |
| ① 提交前范围核查单独一次 | **强。不该动** | 这是**决策点**：模型必须先把 diff 读进上下文、判断暂存范围是否符合用户要求，再决定提不提。写成 `git diff && git commit` 则 diff 显示什么都不影响 commit 会不会发生 —— 一个不看输出就往下走的检查不是检查，是装饰 |
| ② 动作合成一条 | 已经是这么设计的 | `internal/query/query.go` 的 preflight reminder 明确要求 "re-send your ENTIRE original command … keep git add/commit/push/tag together" |
| ③ 事后回读单独一次 | **弱。这是优化空间** | 它是**证据归属**问题，不是决策点 |

第 ③ 步的实现要求回读必须是一次**序号更大的独立 ToolTrace**：

```go
// internal/query/closure_gate.go  postCommitVerificationObserved / postPushVerificationObserved
for i := commitIndex + 1; i < len(calls); i++ {   // ← 回读必须在"更后面的一次工具调用"里
```

所以 `git push && git status -sb` 写成一条，gate 认不出后半段的 `git status`。

但 `&&` 在这里其实是安全的：动作失败则短路，回读不执行，回读证据自然缺失，gate 照样拦。**技术上可以放宽。**

---

## 2. 候选优化（方案 A）

**允许回读出现在同一条链式命令内，但只认 `&&` 分隔。**

`internal/query/closure_gate.go` 已经 import `internal/shellcmd`，具备真正的 shell AST 解析能力。做法：把单条命令按 `&&` 切段，要求回读段的位置在动作段之后。

### 只认 `&&` 是硬约束，不是保守

`|` 和 `;` 都会让 gate 被骗：

- **管道**：`git push 2>&1 | tail -2 && echo ok` —— 管道的退出码取最后一段，所以 push 失败时 `tail` 照样返回 0，`&&` 照样往下走，输出看起来一切正常。
- **分号**：`git push ; git status` —— push 失败后 status 照样执行，回读证据照样"齐全"。

**这两种正是 gate 要防的东西**，放宽时必须排除。

### 预估成本

- 约 2 个文件（`internal/query/closure_gate.go` + 测试），~40 行
- 爆炸半径 `B2_MODE`
- 收益上限：每个共享状态工作流省 1–2 个 turn

---

## 3. 关键背景：项目已有的度量口径

**不要另造判据。** [docs/superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md](../superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md) 已经定义了 closure gate 真实触发率的口径、基线和决策门。方案 A 和那份文档里**已冻结的候选机制 C（证据预注入）** 是同一个问题的不同解法，应当走同一个决策门。

该文档里有一条注记直接命中方案 A 的场景（§ "GatePreflights ≠ 全部摩擦"）：

> gate 拦截分 auto-preflight（带 marker）与 plain block（纯 reminder，**如模型把核查与 commit 链在一条命令里被拦**）两类。只数 marker 会把 plain block 误报成"零摩擦" —— 探针已用 `gate_blocks` / `gate_friction_total` 补齐口径（**首次实测就抓到 1 例**）

所以方案 A 针对的现象**已经被实测观察到**，不是推测。待确认的是**频率**。

已有基线：触发率 **70.5%**。

### 决策门（该文档 §七）

```
触发率 < 5%                          → 不做。简洁优先，不为不存在的问题加运行时复杂度
触发率 ≥ 20%，或绝对数持续两位数/周   → 启动
介于两者之间                          → 先看拦截样本的 reason 分布，
                                       判断是提示措辞问题（调提示）还是遵从性天花板（上机制）
```

---

## 4. 执行步骤

### 前置：工具链

Go 版本以 [`.tool-versions`](../../.tool-versions) 为准（当前 pin **1.26.10**）。

**不要用 `which go` 找 go** —— 很多机器上它解析到一个过旧的版本，Go 会去下载 `go.mod` 声明的 toolchain，而本机若配了 `GOSUMDB=off`（国内 proxy 环境常见）会失败并报
`verifying module: checksum database disabled by GOSUMDB=off`。

对策：装好对应版本、写绝对路径，并加 `GOTOOLCHAIN=local`：

```bash
export PATH=/path/to/go1.26.x/bin:$PATH
export GOTOOLCHAIN=local
go version    # 确认 >= .tool-versions 里 pin 的版本
```

### 第 1 步：被动统计（免费，任何机器都能跑）

```bash
python3 scripts/gate-trigger-rate.py --json
python3 scripts/gate-trigger-rate.py --since 2026-08-01 --json
```

数据源是本机已有的 transcript，位置在 **`~/.go-claude/`**（不是 `~/.claude/`）。

记录：总触发率，与基线 70.5% 对比。

### 第 2 步：拆 reason 分布（免费，**这一步才决定方案 A 的收益**）

总触发率高但原因都在别处，方案 A 也不值得做。要拆出：

1. 总拦截中 `plain block`（纯 reminder，无 auto-preflight marker）占多少
2. 其中 reason 属于"**核查与 commit 链在同一条命令**"的有多少

第 2 项的占比就是**方案 A 的收益上限**。

对应的 reason 来源是 `preCommitScopeGateReminder`（`internal/query/closure_gate.go`），其文案含
`Do not chain this verification with git commit in the same Bash command`，可据此在 transcript 的
`completion_gate` closure 事件里筛选。

### 第 3 步：真模型探针（**需要真实 API key，这一步是那台机器的价值所在**）

```bash
go run ./cmd/golang-cc eval agents \
  --profile golden-probe \
  --runs 5 \
  --provider <settings.json 里 fallback.providers 的 name> \
  --output /tmp/golden-probe.json
```

要点：

- 探针**不做预算断言**（`internal/agenteval/probe.go` 的注释：模型不遵从是数据，不是失败）。只有 fixture 构建失败或 API 传输错误才算 failed。
- 真模型是随机的，**必须 `--runs N` 重复**消方差。N=5 起。
- 关注输出里的 `gate_blocks` / `gate_friction_total` / `turns`，不要只看 `gate_preflights`（见 §3 的注记，只数 marker 会漏 plain block）。
- **先清环境变量再跑**：某些机器的 shell 会导出 `ANTHROPIC_BASE_URL`，它会把 provider 劫持到别的端点，症状是 404 或空 body：

```bash
env -u ANTHROPIC_BASE_URL -u ANTHROPIC_AUTH_TOKEN go run ./cmd/golang-cc eval agents --profile golden-probe --runs 5 ...
```

- 多模型对比更有价值：弱模型的遵从度天花板才是机制存在的理由。同一 provider 列表里换几个 `--provider` 各跑一轮。

### 第 4 步：按决策门裁决

照 §3 的三条判定。裁决结论**回写本文档**，并同步更新
[gate-trigger-rate-measurement.md](../superpowers/plans/2026-07-16-gate-trigger-rate-measurement.md) 的决策门小节 —— 方案 A 与机制 C 是同一个决策门下的兄弟方案，不要各记一处。

---

## 5. 不要用错的 instrument

**`eval agents --suite golden` 不能用来决定要不要做方案 A。**

它走 `newScriptedStreamer`（`internal/agenteval/agenteval.go`）—— 一个脚本化的假模型，永远按剧本规矩行事，所以 `GatePreflights` 恒为 0（这正是 golden suite 里 `ExpectMaxGatePreflights: &zeroGates` 断言的东西）。拿它当基线，答案永远是"零摩擦、不用优化"。

它的正确角色是**改动之后的回归守卫**：

```bash
go run ./cmd/golang-cc eval agents --suite golden
```

确认两个 case 的 turn 预算仍然成立，理想情况下变小：

| case | 现有 `ExpectMaxTurns` | 现有 `ExpectMaxGatePreflights` |
| --- | --- | --- |
| `git_commit_push` | 7 | 0 |
| `git_conflict_recovery` | 11 | 0 |

方案 A 若生效，这两个预算应当可以调低 —— **调低预算本身就是收益的证明**，而且是 `AGENTS.md` 要求的 turn/token 量化证据。

---

## 6. 诚实边界

以下几条来自度量文档自己的 §八，实施时不要忽略：

- **样本偏置**：这个口径度量的是本机（及同口径采集的其他机器）的真实使用，样本偏向本仓库的工作流，**不代表所有用户分布**。
- **分母被低估**：被 gate 拦住的调用本身可能不产生 `tool_call` 条目（拦截发生在工具执行前），因此真实触发率比算出来的略低。前后对比用同一口径趋势可信，**绝对值别当准数**。
- **`GatePreflights` 不等于全部摩擦**：必须同时看 `gate_blocks`。

另外一条属于本文：

- **方案 A 是放宽一个安全检查的位置要求**。放宽本身不改变"必须有回读证据"这个不变量，但它引入了"链内位置判定"这个新的正确性负担 —— `&&` / `;` / `|` 的语义差别就是它的全部风险。如果第 2 步的数据显示收益只有零星几个 turn，**宁可不做**：简洁优先。

---

## 7. 相关代码锚点

| 位置 | 作用 |
| --- | --- |
| `internal/query/closure_gate.go` `postCommitVerificationObserved` | 要求回读在更后一次 ToolTrace（方案 A 要动的地方） |
| `internal/query/closure_gate.go` `postPushVerificationObserved` | 同上 |
| `internal/query/closure_gate.go` `preCommitScopeGateReminder` | "不要把核查与 commit 链在一条命令里"的文案来源 |
| `internal/query/query.go` `gatePreflightToolOutput` | "verbatim 重发原命令、保持 add/commit/push 在一起"的文案来源 |
| `internal/agenteval/metrics.go` `CaseMetrics` | turns / tool_calls / gate_preflights / tokens |
| `internal/agenteval/golden.go` `GoldenSuite` | 两个 git 工作流 case 与预算断言 |
| `internal/agenteval/probe.go` `runGoldenProbeProfile` | 真模型探针 |
| `scripts/gate-trigger-rate.py` | 被动触发率统计 |
