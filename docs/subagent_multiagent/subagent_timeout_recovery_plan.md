# Sub-agent timeout and partial recovery plan

## 背景

这份文档针对最近一次 `anything-ai` 会话里大量 sub-agent 显示 `cancelled` 的问题。目标不是简单把超时时间调大，而是借鉴原版 Claude Code 的 sub-agent lifecycle 模式，把 go-claude 的 sub-agent 生命周期语义、超时策略、部分结果恢复、UI 展示和验证闭环一起补齐。

本轮只做调研和方案，不修改运行时代码。

## 证据

### 原版 Claude Code 行为

原版源码路径：`$HOME/GolandProjects/claude_code_src_2026`。

关键证据：

- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:81` 到 `:88` 的 Agent/Task 输入 schema 只有 `description`、`prompt`、`subagent_type`、`model`、`run_in_background`，没有 `timeout_ms`。
- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:70` 到 `:76` 的 auto-background 阈值在特性开启时是 `120_000ms`，语义是把长时间前台 agent 转为后台任务，不是把 sub-agent 作为失败/取消杀掉。
- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/runAgent.ts:747` 到 `:757` 调用 `query(...)` 时传入的是 `maxTurns`，没有给每个 subagent 设置固定 60s deadline。
- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/AgentTool.tsx:686` 到 `:752` 的 async agent 会注册 background task，并用独立 abort controller 管理生命周期；注释明确 background agent 不跟随父线程 ESC 取消。
- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/agentToolUtils.ts:508` 到 `:686` 的 background lifecycle 区分 completed、failed、killed，并在 killed 时用 `extractPartialResult(...)` 尽量把 partial result 放进通知。
- `$HOME/GolandProjects/claude_code_src_2026/src/tasks/LocalAgentTask/LocalAgentTask.tsx:197` 到 `:262` 会把 task 终态和 result 包装成 `<task-notification>` 回灌给主线程。
- `$HOME/GolandProjects/claude_code_src_2026/src/tools/AgentTool/prompt.ts:263` 到 `:264` 明确区分 foreground 和 background：需要结果时用 foreground，独立长任务用 background；没有让模型靠固定短 timeout 管理复杂扫描。

结论：原版的设计重心是生命周期和通知语义，而不是让模型随手给 Task 填 `timeout_ms=60000`。它主要靠 `maxTurns`、abort controller、background task、progress notification、partial result 来约束和恢复。

可以抽象成以下模式：

```text
foreground subagent
  parent waits for final result
  bounded by maxTurns, user cancellation, provider errors, and runtime safety limits
  no fixed default 60s wall-clock deadline
  may be surfaced as background when auto-background is enabled, instead of being killed

background subagent
  registered as a task
  independent abort controller
  reports progress while running
  sends terminal notification when completed/failed/killed
  preserves partial result when stopped
```

这个模式值得借鉴的核心不是“永不超时”，而是把 sub-agent 当成有生命周期的工作单元，而不是当成一个必须在固定秒数内返回的普通函数调用。

### go-claude 当前行为

关键代码：

- `internal/tools/task/task.go:108` 到 `:110` 已经提示单个同步 Task 应省略 `timeout_ms`，`0` 或省略代表无 Task 级 timeout。
- `internal/tools/task/task.go:627` 到 `:633` 的 `contextWithTimeout` 也确实只有在 `timeoutMS > 0` 时才创建 deadline。
- `internal/tools/task/task.go:581` 到 `:623` 的 batch item 层会把 deadline exceeded 标成 `status="timeout"`。
- `internal/tools/task/task.go:717` 到 `:803` 已经会在失败时返回 partial answer、metadata、completed tool calls，并生成 capability loop 供父线程继续判断。
- `internal/agentruntime/runtime.go:475` 到 `:478` 会把 `context.Canceled` 和 `context.DeadlineExceeded` 都归为 `StatusCancelled`。
- `internal/agentruntime/runtime.go:1667` 到 `:1670` 的 `finishTask` 会再次把 `context.Canceled` 或 `context.DeadlineExceeded` 统一覆盖为 `cancelled`。
- `internal/tui/app.go:2111` 到 `:2151` 的 summary 只有 run/fail/cancel/done，没有 timeout。
- `internal/tui/app.go:4095` 到 `:4102` 的 sub-agent progress 也只区分 failed 和 cancelled。
- `internal/query/query.go:4939` 到 `:4953` 已经提醒父线程 failed/cancelled 仍可能包含 partial evidence，但没有单独 timeout 语义。

真实会话证据：

- `$HOME/.go-claude/projects/Users-example-GolandProjects-anything-ai/5c407d20-c670-455c-a650-3b84e05a0af1.jsonl` 中 4 个 Task 调用都显式带了 `timeout_ms: 60000`。
- 对应 state 文件都落成 `status: "cancelled"`，其中一个任务已经到第 6 turn，并有部分文本输出。

## 根因判断

这不是“多 sub-agent 扫描项目”这个策略本身有问题。对大仓库、多文件、跨语言文档 audit，分配多个只读 sub-agent 并行扫描是合理策略。

真正的问题有四层：

1. 状态语义被合并：deadline exceeded 和用户主动取消都被持久化成 `cancelled`，TUI 也只显示 `cancelled`，用户无法判断是“超时停止”还是“主动取消”。
2. 模型自设短 deadline：虽然 Task 描述建议单个 Task 省略 `timeout_ms`，但 schema 仍允许模型给复杂 audit 填 `60000`，运行时按硬 deadline 执行。
3. 缺少 audit profile：repo-wide audit、链接校验、CN/EN 对齐、VitePress 配置比对这类任务天然可能超过 60s。当前没有基于任务类型的 timeout/profile/soft-deadline 策略。
4. partial recovery 不够前置：go-claude 已能保存部分结果，但 UI、runtime status、父线程提示和 batch summary 没有把 timeout partial evidence 作为一等恢复输入。

因此，正确修复方向不是“把 60s 改成 180s/300s”这么单点的参数调优，而是把 go-claude 从 fixed-deadline-first 调整为 lifecycle-first：

```text
默认不为复杂 sub-agent 设置短 wall-clock deadline
用 maxTurns 限制 agentic loop
用 idle watchdog 判断是否卡死
用 absolute max 防止极端失控
用 user cancel/store cancel 保留主动取消能力
用 task notification 和 partial evidence 支撑恢复
```

## 设计目标

- 借鉴原版 lifecycle-first 模式：foreground 需要结果时等待，background 独立运行并通知，默认不使用固定 60s 截断复杂 sub-agent。
- 语义准确：`timeout`、`cancelled/user_stop`、`failed`、`completed` 分开。
- 不丢证据：超时、取消、失败都保留 partial content、tool calls、transcript、output file、capability loop。
- 不误导用户：TUI/WebUI/API 显示“deadline exceeded/timeout”，不能让用户误以为是自己取消。
- 不放弃安全：用户显式 deadline 要被尊重；后台长任务仍可被取消；权限和工具边界不扩大。
- 面向复杂 audit：支持多 sub-agent 并行扫描，但给足合理时长、进度、恢复和重试路径。
- 可验证：每个状态转换、UI 展示、真实任务行为都能用测试或真机操作证明。

## 修复方案

### P0：采用 lifecycle-first 执行模型

go-claude 应把 Task/Agent sub-agent 统一到一个生命周期模型下：

```text
created -> running -> completed
                  -> failed
                  -> cancelled
                  -> timeout
```

执行约束分层：

| 约束 | 作用 | 默认建议 |
| --- | --- | --- |
| `maxTurns` | 限制模型-工具循环次数 | 始终启用 |
| user/store cancel | 用户或系统主动停止 | 始终启用 |
| idle watchdog | 长时间没有 text/tool/progress 时停止 | 对 background/deep audit 启用 |
| absolute max | 防止极端失控和成本失控 | 对 background/deep audit 启用 |
| explicit `timeout_ms` | 用户明确 deadline 或模型显式输入 | 只作为显式硬 deadline，不作为复杂任务默认值 |

实现要求：

- foreground Task 未提供 `timeout_ms` 时，不创建 Task 级 wall-clock deadline；保持当前 `0/omitted` 语义。
- 如果前台 sub-agent 运行时间过长，应优先考虑 auto-background/notification 或明确进度提示，而不是把“长时间运行”直接折叠成 cancelled。
- background Agent/Task 不应跟随父线程普通取消自动终止，除非是用户明确停止该 task 或全局 shutdown。
- 任何终态都必须写入 task store、output state file、event stream，并可被 TUI/WebUI/query runtime status 读取。
- timeout/cancelled/failed 都要走 partial evidence 提取路径。

### P0：引入 timeout 终态语义

新增或标准化终态：

```text
running
completed
failed
cancelled   # 用户/父上下文/存储取消
timeout     # deadline exceeded / watchdog timeout
```

建议实现：

- 在 `internal/agenttasks/agenttasks.go` 增加 `StatusTimeout = "timeout"` 和 `EventTimeout = "timeout"`。
- 在 `agentruntime.Runtime.Run` 的 stream error 分支中区分：
  - `ctx.Err() == context.DeadlineExceeded` -> `StatusTimeout`
  - `ctx.Err() == context.Canceled` -> `StatusCancelled`
- 在 `finishTask` 中不要把 `DeadlineExceeded` 覆盖为 `cancelled`，而是保留调用方传入状态，或按 `ctx.Err()` 映射成 timeout/cancelled。
- result JSON 增加轻量字段：

```json
{
  "status": "timeout",
  "partial": true,
  "stop_reason": "deadline_exceeded",
  "timeout_ms": 60000
}
```

兼容要求：

- `completed/failed/cancelled/timeout` 都是 terminal status。
- 老数据里的 `cancelled` 保持可读，不做批量迁移。
- 如果数据库 status 是自由字符串，不需要 schema migration；如果有 enum/check，需要同步 migration。

### P0：TUI/WebUI/API 展示 timeout

TUI：

- `agentProgressSummary()` 增加 `timeout:N`，不要把 timeout 计入 `cancel:N`。
- `updateAgentProgress()` 增加 `case "timeout"`，显示 `deadline exceeded`、`timeout_ms`、`duration_ms`。
- collapsed/expanded sub-agent panel 对 timeout 使用独立状态文案。

WebUI/API：

- `isValidAgentTaskStatus`、`isTerminalAgentTaskStatus`、conversation summary、SSE event 允许 `timeout`。
- Web Agent progress 卡片显示 `timed out` 或 `timeout`，保留 result preview、transcript_path、output_file。
- 移动端/聊天流已有 `timed_out` 概念时，应统一映射，避免一个端叫 cancelled、另一个端叫 timed_out。

### P0：timeout partial evidence 进入父线程上下文

修改 runtime status 和 evidence gate：

- `internal/query/query.go` 中 terminal status 增加 timeout。
- `agentTaskStatusAction("timeout")` 返回：

```text
timeout notification: preserve partial evidence, inspect result preview/transcript/output_file, and decide whether to retry with a longer profile or narrow the scope.
```

- capability loop 对 timeout 给默认字段：
  - evidence：如果已有 content，标为 partial evidence。
  - unknowns：说明哪些范围未完成。
  - verification：建议读取 transcript/output_file 或重跑更窄任务。
  - risks：partial result incomplete。
  - next_action：retry/narrow/continue from partial evidence。

### P1：Task timeout policy 从硬数值升级为生命周期策略

保留 `timeout_ms`，但不要只靠它表达复杂任务策略。新增内部执行策略概念：

```go
type TimeoutPolicy struct {
    ExplicitTimeoutMS int
    Profile           string // none, short, standard, deep_audit
    IdleTimeout       time.Duration
    AbsoluteMax       time.Duration
    Source            string // user_explicit, model_input, inferred, config_default
    AllowBackground    bool
}
```

建议默认：

| 场景 | 默认策略 |
| --- | --- |
| 单个同步 Task，未给 timeout | 无 Task 级硬超时，只受 maxTurns、provider/client timeout、用户取消约束 |
| 单个同步 Task，显式给 timeout | 尊重用户/模型输入，但低于安全下限时返回可解释错误 |
| batch 简单检索 | 推荐父线程直接用 Glob/Grep/LS；如果仍使用 Task，允许短 timeout |
| batch repo-wide audit/deep scan | `deep_audit`，建议 idle timeout 3-5min，absolute max 10-20min |
| background agent | 不跟随父线程普通取消；用 AgentGet/通知拿结果 |

关键点：优先用 idle timeout/watchdog，而不是绝对 60s 硬切。只要 agent 持续产生 text_delta/tool_call/tool_result/usage，就不应被短 deadline 杀掉。

watchdog 更新时机：

- model stream text delta。
- assistant message stop。
- tool call started。
- tool result completed。
- usage/cache/progress event。
- sub-agent heartbeat 或 background summary。

如果超过 idle timeout 且没有任何进展，终态应为 `timeout`，`stop_reason` 应为 `idle_timeout`。如果超过 absolute max，终态也为 `timeout`，`stop_reason` 为 `absolute_timeout`。

### P1：复杂 audit 的 timeout guard

防止模型再次给大范围 audit 填 `60000`：

- 对 Task batch 增加 `task_profile` 或内部推断：
  - 命中 `audit`、`scan`、`cross-reference`、`alignment`、`docs`、`VitePress`、`repo-wide`、`所有文件`、`全量` 等关键词。
  - `tasks.length >= 2` 且 prompt 包含目录级检查。
- 如果 profile 是 deep audit 且 `timeout_ms < 180000`：
  - 不直接执行。
  - 返回 `invalid_timeout`，提示 omit timeout_ms 或使用 `timeout_profile: "deep_audit"`。
- 如果用户明确要求 60s 内完成，则允许执行，但 result/status 必须清楚写 `timeout`，不能显示 `cancelled`。

同时调整 Task tool prompt/schema 文案：

- `timeout_ms` 描述改为“explicit hard deadline”，强调不是复杂 audit 的推荐默认值。
- 对 repo-wide/deep audit 建议省略 `timeout_ms` 或使用 `timeout_profile: "deep_audit"`。
- 对并行大任务建议优先 background/AgentCreate，并说明完成后通过 AgentGet/notification 取结果。

### P1：部分结果恢复和后续调度

timeout 后父线程需要有明确恢复路径：

- batch result 每个 timeout task 返回：
  - `status: "timeout"`
  - `partial: true`
  - `content` 或 `partial_content`
  - `turns`
  - `tool_calls`
  - `session_id`
  - `transcript_path`
  - `output_file`
  - `capability_loop`
- 对 `retry_attempts` 增加规则：
  - provider/network transient error 可自动 retry。
  - timeout 不默认盲目 retry。
  - 只有 `retry_on_timeout=true` 或 profile 指定允许时，才用更长 timeout 或更窄 continuation prompt retry。
- 父线程 runtime reminder 明确要求：
  - 不要把 timeout 任务当完全失败扔掉。
  - 先整合 partial evidence。
  - 对未覆盖范围补一个更窄 Task 或直接用本地工具验证。

### P2：面向多 sub-agent audit 的 orchestration profile

后续可引入 `AuditBatch` 内部策略，不一定暴露为新工具。这个 profile 是对原版“多 agent 并行 + notification”模式的 go-claude 化增强：

- 自动拆分任务时带 scope、expected evidence、max file count、output contract。
- 默认 `max_concurrency` 根据 provider 限流、CPU、repo size 配置动态决定。
- 每个 sub-agent 周期性写 heartbeat/progress summary。
- 如果某个 task timeout，主线程拿 partial 后只补缺口，不重复扫描已完成范围。
- TUI/WebUI 展示 batch-level summary：

```text
Sub-agents  run:1/done:2/timeout:1/fail:0
```

## 实施顺序

1. P0 lifecycle 语义：
   - 明确 foreground/background 生命周期。
   - 保持单 Task omitted timeout 不创建固定 deadline。
   - 建立 timeout/cancelled/failed/completed 终态矩阵。
2. P0 状态语义：
   - 增加 `StatusTimeout/EventTimeout`。
   - 修复 `agentruntime` deadline -> timeout。
   - 修复 `finishTask` 覆盖逻辑。
   - 更新 query terminal status/action。
3. P0 展示：
   - TUI summary/detail 支持 timeout。
   - WebUI/API status allowlist 支持 timeout。
4. P0 测试：
   - runtime、Task、query、TUI、server 相关单元测试。
5. P1 策略：
   - 增加 timeout policy helper。
   - 对 deep audit 短 timeout 做 guard。
   - 增加 idle watchdog。
6. P1/P2 恢复：
   - timeout partial evidence 字段结构化。
   - batch recovery prompt/status 改进。

## 验证方案

### 单元测试

- `internal/agentruntime`：
  - fake streamer 等待 context deadline，期望 task status/result status/output state 都是 `timeout`。
  - fake streamer 先 emit partial text 再 deadline，期望 partial content 和 capability_loop 保留。
  - 用户取消仍是 `cancelled`。
- `internal/tools/task`：
  - single Task `timeout_ms=1` 返回 timeout partial，而不是 cancelled。
  - single Task 省略 `timeout_ms` 时不创建 Task 级 deadline。
  - batch item timeout status 是 `timeout`，summary 增加 timeout count 或 failed count 中明确 timeout。
  - deep audit batch + `timeout_ms=60000` 触发 guard。
- `internal/query`：
  - runtime status 中 timeout terminal task 被识别为 partial evidence。
  - action 文案要求 retry/narrow/inspect transcript。
- `internal/tui`：
  - sub-agent panel 显示 `timeout`。
  - summary 显示 `timeout:N` 而不是 `cancel:N`。
- `internal/server` / WebUI：
  - tenant agent task `timeout` 是合法 terminal status。
  - SSE/event/conversation summary 不把 timeout 映射成 cancelled。

### 真机验证

1. 在隔离 fixture 仓库启动 go-claude。
2. 触发 2-4 个只读 sub-agent batch：
   - 一个快速 completed。
   - 一个 fake provider sleep 超过 60s 或测试环境用 1s deadline 模拟 timeout。
   - 一个用户主动 stop/cancel。
   - 一个 provider error failed。
   - 一个持续输出 heartbeat/progress 的长任务，证明不会被固定 60s wall-clock 截断。
3. TUI 验证：
   - Sub-agents panel 同时展示 completed/timeout/cancelled/failed。
   - timeout 行显示 duration、session/output 信息。
   - summary 不再把 timeout 算作 cancel。
4. 父线程验证：
   - 下一轮 prompt/context 能看到 timeout partial evidence。
   - 父线程能基于 partial evidence 继续补查，而不是报告“被取消所以没结果”。
5. WebUI 验证：
   - Progress tab / Agent Cockpit status 显示 timeout。
   - terminal task 禁止继续写入同一 run 的规则不被破坏。

建议命令：

```bash
go test ./internal/agentruntime ./internal/tools/task ./internal/query ./internal/tui ./internal/server -count=1
go test ./... -count=1
git diff --check
```

真实交互验收可新增脚本：

```bash
scripts/subagent-timeout-recovery-acceptance.sh --fixture
```

脚本应断言：

- transcript/state file 包含 `status":"timeout"`。
- output file 包含 partial marker。
- TUI stream event 包含 `nested_agent_progress event=timeout`。
- query final request 包含 timeout partial evidence reminder。
- 长任务持续产生 progress 时不会因为默认固定 60s 被停止。

## 风险和边界

- 不能简单把默认 timeout 拉长到很大：这会隐藏挂死任务，影响交互可控性。更好的方案是 lifecycle-first + idle watchdog + absolute max + progress heartbeat。
- 不能简单取消所有 timeout：用户显式 deadline、idle timeout、absolute max 仍然需要存在。
- 不能把所有 `context.Canceled` 都当 timeout：用户取消、父线程停止、store cancel 必须仍是 cancelled。
- 不能让 timeout 自动无限 retry：大 audit timeout 很可能是范围过大，应该先保留 partial，再补缺口。
- 不能只改 UI 文案：持久化 result/status、runtime reminder、AgentGet、WebUI/API 都要一致。

## 最小可交付定义

P0 完成后，最近这类问题应该满足：

- 复杂 sub-agent 未显式 deadline 时不会被默认固定 60s wall-clock 截断。
- 60s deadline 导致的停止显示为 `timeout`，不是 `cancelled`。
- 用户主动取消仍显示 `cancelled`。
- timeout task 的 partial content、turns、tool calls、session/output/transcript 能被看到。
- 父线程收到 timeout partial evidence 提示，能继续恢复。
- 单元测试和真实 TUI/fixture 验证都能复现并证明修复。
