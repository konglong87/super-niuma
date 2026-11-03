# Context Compaction Fidelity

本文档解释 go-claude 在两类上下文压缩场景中如何降低关键信息丢失风险，以及当前用哪些测试证明这些机制没有回退：

- `tool_result budget`：单个或历史工具结果过大时，把大输出持久化为 `<persisted-output>`。
- `auto compact`：会话接近上下文阈值时，把较早历史压缩成 compact summary。

核心结论：压缩无法保证 100% 不丢信息。go-claude 的策略是把对父 agent 决策最重要的信息结构化，再在压缩前后强制保留，并用 request-level test、prompt dump 和 CLI acceptance 验证这些字段仍进入下一轮模型请求。

## Agent Loop 能力模型

强 agent 不是每一轮都重新聪明一次，而是在长时间执行中持续看见正确目标、正确环境、正确工具、正确证据，并把这些信息低损传到下一步决策。

可以把 agent loop 的核心能力拆成：

```text
Agent loop capability =
  accurate user intent understanding
+ live environment awareness
+ correct tool / skill / permission / memory loading
+ fidelity across tool calls, sub-agents, resume, compact, and UI surfaces
+ continuous verification against the user's goal
+ recovery from uncertainty, failures, interruptions, and context compression
```

其中“信息保真链路”是长时任务的关键放大器。短任务里，模型可能靠一次完整上下文完成；长任务里，真正决定成败的是：

- 子 agent 找到的 evidence 是否回到父 agent。
- 大 `tool_result` 被预算压缩后，risks 和 next_action 是否仍在下一轮 request 中。
- resume 后是否还能接着之前的真实任务状态继续。
- compact 后是否只剩泛泛总结，还是保留 hard facts。
- 用户、父 agent、子 agent、TUI/WebUI 看到的是不是同一套事实。

因此，本项目把 `evidence / assumptions / unknowns / verification / risks / next_action` 当作高优先级保真字段。压缩可以丢掉冗余原文，但不能丢掉影响下一步决策的 hard facts。

## 风险模型

压缩最大的风险不是“原文变短”，而是父 agent 下一轮看不到能继续工作的决策字段：

- evidence：已经确认了什么事实。
- assumptions：哪些前提仍是假设。
- unknowns：还不知道什么。
- verification：已经跑过或应该跑的验证。
- risks：继续推进有什么风险。
- next_action：下一步应该做什么。

如果这些字段只存在于长输出尾部，而模型请求里只保留前 2000 bytes preview，父 agent 可能会重复调查、忽略风险，或者基于不完整上下文做结论。

## Tool Result Budget 保真

实现入口：

- `internal/toolresult/toolresult.go::Process`
- `internal/toolresult/toolresult.go::persistedMessage`
- `internal/toolresult/toolresult.go::capabilityLoopSummary`

当工具输出超过限制时，当前机制会：

1. 把完整输出保存到 session 附近的 `tool-results/<tool_use_id>.txt`。
2. 在模型请求中用 `<persisted-output>` 代替完整输出。
3. 在 `<persisted-output>` 中写入完整文件路径。
4. 如果 shell runner 已经截断输出，提前写入警告，提示重新用更窄过滤或重定向完整输出。
5. 从完整输出里抽取 `capability_loop` 摘要，并放在 preview 前面。
6. 最后才写入前 `PreviewSizeBytes`，当前是 2000 bytes。

保真的关键点是第 5 步。它避免 `capability_loop` 在长输出尾部时被 preview 截掉。当前 extractor 覆盖这些形态：

- `<capability_loop>...</capability_loop>` wrapper。
- 顶层 JSON：`capability_loop`。
- 嵌套 JSON：`result.capability_loop`。
- 嵌套 JSON：`task.result.capability_loop`。
- 文本行：`capability_loop: evidence: ... | risks: ... | next_action: ...`。

示意：

```text
<persisted-output>
Output too large (... bytes). Full output saved to: /path/to/tool-results/toolu_task.txt

Capability loop summary preserved from full output:
- evidence: ...
- unknowns: ...
- verification: ...
- risks: ...
- next_action: ...

Preview (first 2000 bytes):
...
</persisted-output>
```

## Auto Compact 保真

实现入口：

- `internal/compact/facts.go::ExtractFacts`
- `internal/compact/compactor.go::summaryPrompt`
- `internal/compact/compactor.go::buildPersistedSummary`
- `internal/compact/compactor.go::adjustStartForToolPair`

auto compact 的保真不是完全依赖总结模型。当前流程是：

1. `partitionMessages(...)` 把较早历史划为 compact 区，把最近轮次划为 keep 区。
2. `adjustStartForToolPair(...)` 调整边界，避免把 `tool_use` 和对应 `tool_result` 切开。
3. `ExtractFacts(...)` 从 compact 区抽取 hard facts：
   - files / URLs / commands / tools / errors / constraints。
   - capability evidence / assumptions / unknowns / verification / risks / next actions。
4. `summaryPrompt(...)` 把 hard facts 明确写入总结请求，要求 summary 模型保留。
5. `validateSummary(...)` 至少检查关键 heading、前几个 file facts、command facts 是否出现在 summary 中。
6. `buildPersistedSummary(...)` 把 `## Runtime Extracted Facts` 直接追加到 compact summary 后面。

第 6 步很重要：即使 summary 模型没有完美总结，runtime 抽出的 hard facts 仍会作为下一轮请求的一部分进入 `Conversation summary so far:`。

## 测试层级

| 层级 | 代表测试/脚本 | 证明什么 |
| --- | --- | --- |
| tool_result 单元测试 | `internal/toolresult/toolresult_test.go::TestProcessPreservesCapabilityLoopSummaryOutsidePreview` | 长输出尾部的 `capability_loop` 在 `<persisted-output>` 替换后仍以前置 summary 形式可见，完整原文也落盘。 |
| tool_result budget 单元测试 | `TestApplyMessageBudgetPersistsLargestResultsUntilUnderLimit`、`TestApplyHistoryBudgetPersistsOlderResultsAndKeepsLatest` | 超预算时优先压缩大结果和旧结果，最新结果不会被错误压缩。 |
| compact facts 单元测试 | `internal/compact/compactor_test.go::TestMaybeCompactPersistsCapabilityLoopFacts` | `AgentGet` nested `result.capability_loop` 会进入 compact 后的 persisted summary。 |
| Task wrapper compact 单元测试 | `TestMaybeCompactPersistsToolResultCapabilityLoopWrapper` | 同步 Task 返回的 `<capability_loop>` wrapper 会进入 `Capability Loop Evidence/Unknowns/Risks/Next Actions`。 |
| persisted-output compact 单元测试 | `TestExtractFactsCapturesPersistedOutputCapabilityLoopSummary` | 已被 `<persisted-output>` 替换过的结果，仍能被下一次 compact 从 preserved summary 中抽取 facts。 |
| persisted-output compact/resume 单元测试 | `internal/query/query_test.go::TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary` | auto compact 后的 capability facts 不只进入当轮 request，也会写入 transcript，并在 `MessagesFromTranscript` 恢复的 resume request 中继续可见。 |
| CLI compact acceptance | `scripts/agent-capability-loop-compact-acceptance.sh` | completed / failed / cancelled agent task 在真实 auto compact 后，下一轮 request 和 AgentGet 结果仍包含 `capability_loop` markers。 |
| compact hard-facts acceptance | `scripts/agent-capability-loop-compact-facts-acceptance.sh` | resume transcript 中的 AgentGet evidence、child report paths、risk blocker、hold decision 穿过 auto compact 后仍进入 provider request。 |
| long-output compact/resume acceptance | `scripts/agent-long-output-resume-acceptance.sh --auto-compact` | 长 Agent 输出经过 resume + auto compact 后，父 request 保留 `content_preview/content_bytes/output_file`，不重新塞入长正文，也不退化成 generic `<persisted-output>`。 |

## 常用验证命令

局部验证：

```bash
go test ./internal/toolresult -count=1
go test ./internal/compact -count=1
go test ./internal/query -run TestSessionAutoCompactExtractsPersistedCapabilityLoopSummary -count=1
```

真实 CLI / prompt dump 验证：

```bash
scripts/agent-capability-loop-compact-acceptance.sh --force
scripts/agent-capability-loop-compact-facts-acceptance.sh --force
scripts/agent-long-output-resume-acceptance.sh --force --auto-compact
```

矩阵验证：

```bash
scripts/prompt-acceptance-matrix.sh \
  --scenarios agent-capability-loop-compact,agent-capability-loop-compact-facts,agent-capability-loop-failed-compact,agent-capability-loop-cancelled-compact,agent-long-output-compact-resume \
  --force
```

## 当前边界

这些机制证明的是 go-claude 自身上下文治理不回退，不等于开放世界能力一定超过所有 baseline。

仍需注意：

- extractor 只能保留它能识别的结构。如果关键事实没有出现在 `capability_loop`、路径、命令、错误或约束等可抽取形态中，仍可能在 compact 中弱化。
- `ExtractFacts` 对每类 capability 字段有数量上限，例如 evidence 保留前 12 条，unknowns/risks/next actions 等保留前 8 条。
- `<persisted-output>` 保存路径不等于模型已经读过完整文件。关键决策信息必须出现在 preserved summary 或 runtime extracted facts 中。
- CLI acceptance 使用 deterministic stub provider 和 marker 审计，适合防回退；真实模型是否主动利用这些信息，还需要更高层任务评测或 A/B scorecard。

## 学习方法

阅读这个链路时建议按下面顺序：

1. 先读 `internal/toolresult/toolresult.go::persistedMessage`，理解大工具输出如何被替换。
2. 再读 `internal/toolresult/toolresult_test.go::TestProcessPreservesCapabilityLoopSummaryOutsidePreview`，理解为什么尾部 evidence 需要前置摘要。
3. 再读 `internal/compact/facts.go::ExtractFacts`，理解 compact 前 hard facts 如何抽取。
4. 再读 `internal/compact/compactor.go::buildPersistedSummary`，理解为什么 hard facts 会被强制附加到 summary。
5. 最后读三个 acceptance 脚本，重点看 verifier 检查的是下一轮 provider request，而不是只检查源码里有没有字符串。
