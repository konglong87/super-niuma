# Go Claude 本地 Coding Agent 能力边界说明

更新日期：2026-07-11

本文用于如实回答“go-claude 作为本地 Coding Agent，从任务输入到代码修改的完整链路、模型后端、工具系统、上下文治理、记忆污染、评测和日常 AI 开发流程是什么”。结论基于当前仓库实现，而不是理想化设计。

## 总体判断

go-claude 的核心不是“调用一次 LLM API”，而是一个本地 agent runtime：CLI/TUI/API 入口把用户任务转换为 `query.Session`，由模型流式输出文本或 `tool_use`，本地工具执行真实文件、命令、搜索、子 agent、skills、MCP、Web 等动作，再把 `tool_result` 回灌给模型，循环到最终回答。主链路在 `internal/query/query.go`，工具接口在 `internal/tools/tool.go`，模型适配在 `internal/anthropic`，会话、压缩和恢复在 `internal/session`、`internal/compact`。

当前优点：

- 本地工程链路较完整：CLI/TUI/API Server、stream-json、OpenAI-compatible API、tools、permissions、sandbox、session、resume、checkpoint、auto compact、sub-agent、skills、tenant runtime、telemetry 都有实现。
- 对“闭环”投入较多：工具调用、文件变更、完成声明、compact/resume、sub-agent evidence、prompt dump、golden/acceptance scripts 都有可验证路径。
- 工具系统比较清晰：新工具只要实现统一 `Tool` 接口，注册后会自动进入模型工具定义和运行时调度。
- 具备平台化扩展基础：多模型 provider、fallback provider、OpenAI-compatible server、多租户、tenant skill、trace/telemetry、agent task API 已经不是纯 CLI 单机架构。

当前短板：

- 与官方 Claude Code 仍未 100% 对齐。尤其是一些非公开内部字段、完整 thinking signature 语义、部分多模态/PDF 渲染、WebUI 与 TUI parity、Windows/PowerShell 真 OS sandbox 等仍有边界。
- 工具失败后的恢复主要是“结构化错误回传 + 模型继续决策 + 部分 runtime gate”，不是所有错误都有确定性自动重试策略。
- 长期记忆的“污染治理”有 review/active 边界，但还不能完全自动证明记忆结论一定正确；最终仍需要来源、时间、验证命令和人工/测试纠错。
- 评测体系已经覆盖主链路回归，但还没有完全成熟的开放世界 benchmark，不能用当前 deterministic eval 证明“所有真实任务都变强”。

## 1. 从输入任务到最终修改代码，完整链路是怎样的？

完整链路可以拆成 10 步：

1. 用户从 CLI/TUI/API Server 输入任务。CLI 入口会加载配置、权限、hooks、MCP、tools、session recorder、resume 初始消息等。
2. `internal/cli/cli.go` 构造核心工具列表，例如 `Read`、`Write`、`Edit`、`MultiEdit`、`Grep`、`Glob`、`Bash`、`Task`、`Agent*`、`Skill`、`WebSearch`、`WebFetch`、`MCP resources`。
3. 工具被 `tools.GuardAll(policy, ...)` 包装，加入权限策略、active skill 工具限制、agent policy、session allow/deny。
4. CLI 创建 `query.Session`，传入模型 client、工具 registry、CWD、model、max turns、prompt mode、auto compact、tenant/user/session、trace id、hooks、sandbox 等。
5. `query.Session.run` 组装 system prompt、code memory、skills catalog、tenant context、runtime status、prompt cache breakpoint、历史 `InitialMessages`。
6. 每一轮请求调用模型后端 `StreamMessages`。模型可能输出普通文本，也可能输出 `tool_use`。
7. 如果模型输出 `tool_use`，runtime 根据工具名从 registry 找工具，先跑 PreToolUse hook、权限和 sandbox 检查，再执行工具。
8. 工具返回 `tools.Result{Content, IsError, ContextMessages}`。runtime 记录 `ToolTrace`、telemetry、transcript，把结果包装成 `tool_result` 回到下一轮模型上下文。
9. 对文件写入、命令执行、commit/push、完成声明等高风险闭环，`CompletionGate` / pre-tool closure gate 会要求 evidence，不允许模型过早声称完成。
10. 没有新的 tool_use 且通过 gate 后，runtime 才输出最终回答并记录 assistant transcript。代码修改本身由 `Write` / `Edit` / `MultiEdit` / `Bash` 等工具真实落盘。

所以 go-claude 的实际工作流是：

```text
User Task
  -> CLI/TUI/API
  -> config + memory + session + tools + permissions
  -> query.Session
  -> model stream
  -> text or tool_use
  -> local tool execution
  -> tool_result back to model
  -> repeat until no tool_use
  -> completion gate
  -> final answer + transcript/telemetry
```

## 2. 是否支持不同模型后端？多模型服务如何抽象？

当前支持三类上游协议后端：

- Anthropic-compatible Messages API。
- OpenAI-compatible Chat Completions API。
- 显式配置的 OpenAI Responses-compatible API（第一阶段 stateless）。

内部统一抽象是 `internal/anthropic/types.go` 里的 `MessagesRequest`、`MessageParam`、`ContentBlock`、`ToolDefinition`、`StreamResult`、`Usage`。Chat Completions 与 Responses 各有独立 protocol adapter，将内部消息、tools、tool_result 转成各自 wire format，再把 stream 归一化为内部 `tool_use` / `ContentBlock`。Responses 的 stateless encrypted reasoning 通过 `internal/provider` 的 continuation envelope 持久化，不进入普通文本。

当前已经支持 primary provider + fallback providers。`StreamMessages` 会按 provider 列表尝试；如果某个 provider 在还没输出内容前失败，且错误允许 fallback，就尝试下一个 provider。OpenAI-compatible 的 stream create 遇到 429 会做一次 retry，并支持从错误消息里解析 retry-after。

如实边界：

- 当前 provider 抽象已经能跑 Anthropic、OpenAI Chat Completions 与 OpenAI Responses，但不是所有能力完全等价。例如 Anthropic thinking/signature、citation、connector text 与 Responses typed items/continuation 的能力并不完全一致。
- Responses 当前固定 stateless `store=false`；尚未支持 `previous_response_id`、Conversations、hosted tools 或 background response。第三方兼容端点必须真实验收，不能由名称推断能力。
- 错误码抽象还不够统一。现在部分错误是 Go error、部分是 OpenAI SDK error、部分是工具 `IsError`，还没有一个全局标准的 `ProviderError{code, status, retryable, retry_after, provider, phase}` 贯穿所有后端。
- 多 provider fallback 主要适合“请求尚未开始输出”的失败；stream 已经输出内容后再切换 provider 容易破坏上下文一致性，所以当前不会盲目 fallback。

更理想的多模型抽象应分四层：

| 层 | 统一对象 | 责任 |
| --- | --- | --- |
| Request | `ModelRequest` | system、messages、tools、response_format、thinking、metadata |
| Response | `ModelStreamEvent` / `ModelResult` | text、thinking、tool_use、usage、finish_reason |
| Error | `ProviderError` | code、HTTP status、retryable、retry_after、phase、provider |
| Policy | `RetryPolicy` / `FallbackPolicy` | 只在安全阶段重试，避免重复执行工具或重复写入 |

## 3. 工具系统怎么做？新工具完整流程是什么？

工具统一实现 `internal/tools/tool.go` 里的接口：

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() json.RawMessage
    Run(ctx context.Context, input json.RawMessage, toolContext Context) Result
}
```

一个新工具从定义到调用的流程：

1. 新建工具包，例如 `internal/tools/foo/foo.go`。
2. 实现 `Name`、`Description`、`InputSchema`、`Run`。
3. 在 `Run` 里解析 JSON input，使用 `tools.Context` 里的 `CWD`、`WritableRoots`、`Sandbox`、`PermissionPrompt`、`FileChange`、`TaskStore`、`SkillProvider` 等上下文。
4. 写单元测试，覆盖输入校验、成功路径、失败路径、权限/文件边界。
5. 在 `coreRuntimeTools(...)` 注册工具，或通过 MCP/skills/plugin 动态发现。
6. CLI 构建 registry 时用 `tools.NewRegistry(...)` 收集工具，再用 `tools.GuardAll(policy, ...)` 包装权限。
7. `registry.Definitions()` 把工具转成模型可见的 `ToolDefinition{Name, Description, InputSchema}`。
8. 模型输出 `tool_use{name,input}` 后，`query.Session.runTool` 根据 name 找到工具并执行。
9. 工具结果被记录为 `ToolTrace`、transcript、telemetry，并以 `tool_result` 回灌模型。

优点是链路简单、扩展成本低；缺点是工具能力声明主要依赖自然语言 description 和 JSON schema，复杂工具的语义约束仍需要测试、runtime guard 和 acceptance script 锁住。

## 4. 工具调用失败后，是报错还是恢复重试？

当前策略不是简单 crash，也不是所有错误都自动重试，而是分层处理：

- 文件不存在、JSON 解析失败、PDF pages 未实现、权限拒绝、sandbox 拒绝、命令超时等，工具返回 `IsError=true` 和错误文本。
- `query.Session` 会把失败工具结果作为 `tool_result` 回给模型，且附加验证失败恢复提醒，让模型决定下一步是改路径、补读文件、换命令、延长 timeout、请求用户授权，还是说明限制。
- `Bash` 有默认 120 秒 timeout，超时返回 `command timed out after ...`，不会假装成功。
- unknown tool 会返回 `unknown tool: ...`，不会执行任意未知动作。
- PreToolUse / PostToolUse hook 失败会阻断工具或把结果改成错误。
- 模型流式输出如果在已经有部分文本后失败，有 partial stream recovery；但这不是工具重试。

如实边界：

- 工具级自动 retry 不是全局默认，因为很多工具不是幂等的，例如写文件、运行 migration、git commit、push。
- 可以对读操作、网络检索、模型 429、临时进程失败做专门 retry，但必须有幂等判断、最大次数、退避、可观测事件和用户可见证据。
- 当前更偏“错误显式进入上下文，让 agent 自我修复”，不是“runtime 自动替 agent 做所有恢复”。

## 5. 多轮任务上下文治理的真实场景是什么？

真实场景：一个任务先读 20 个文件、跑多次测试、执行子 agent，然后上下文越来越长。此时如果继续把所有历史原样塞给模型，会触发上下文上限、成本变高、模型注意力下降。

go-claude 的处理方式：

- `AutoCompact` 开启后，`query.Session` 每轮请求前估算 token。
- 超过模型上下文阈值后，`compact.Compactor` 把较早消息分区为 compact 部分，保留最近若干轮。
- compact prompt 要求摘要固定包含 `Current Goal`、`User Preferences / Constraints`、`Files / Code Changed`、`Commands / Test Results`、`Open Tasks`、`Known Issues / Risks`、`Important Raw Facts`。
- runtime 先从旧消息里提取硬事实：文件路径、URL、命令、工具名、错误、约束、sub-agent capability_loop evidence 等。
- 生成摘要后校验关键 heading、文件事实、命令事实是否存在。
- 通过校验后，用一条 “Conversation summary so far” 消息替换旧历史，保留最近轮次继续执行。
- `recordCompact` 把有效摘要写入 transcript，后续 resume 能恢复这个 reset boundary。

这不是简单丢历史，而是“摘要 + 硬事实 + 最近轮次”的治理。

## 6. 压缩后怎么判断没有破坏任务？

当前有三类校验：

- 结构校验：compact summary 必须包含关键 heading，至少有 `Current Goal`、`Open Tasks`、`Important Raw Facts`。
- 硬事实校验：runtime 提取到的前若干文件路径和命令必须出现在摘要里，否则判定 `summary_invalid`。
- 回归校验：`internal/compact`、`internal/query`、`docs/compatibility_matrix.md` 里的 golden 和 acceptance scripts 覆盖 compact 后继续响应、tool pair 边界、capability_loop facts 恢复、compact resume follow-up gate。

如实边界：

- 这能防止很多机械丢失，但不能数学证明摘要完整。
- 对高风险任务，压缩后仍应靠实际文件读取、git diff、测试、prompt dump、transcript 恢复测试来确认。
- 如果摘要里只有自然语言，没有来源和验证命令，可信度要降低。

## 7. 任务摘要、文件摘要、过程笔记怎么生成和维护？

当前主要有几类产物：

- session transcript：记录 message、tool_call、tool_result、checkpoint、rewind、compact_summary、recap_summary 等。
- auto compact summary：模型生成，但由 runtime 注入硬事实并做校验。
- recap summary：面向 TUI/session 的 recap，可手动或异步生成；默认不应无条件塞回下一轮 prompt。
- memory/code memory：code mode 会通过 `memory.LoadCode(cwd, prompt)` 加载项目规则/记忆，并作为 `<system-reminder>` 注入。
- tool-result replacement：大工具结果会持久化或替换为 `<persisted-output>`，避免重复把长输出塞进上下文。
- prompt dump / telemetry / trace：用于验证上下文里到底出现了什么。

避免关键信息被压掉的手段：

- runtime 抽取硬事实，而不是完全相信模型摘要。
- 摘要固定 heading。
- 最近若干轮不压缩。
- tool_use 和 tool_result 配对边界不能被拆坏。
- sub-agent evidence 使用结构化 capability_loop：Evidence、Assumptions、Unknowns、Verification、Risks、Next action、artifact anchors。
- 对超长工具结果做可追溯替换，而不是直接截断到无法恢复。

## 8. 如果摘要遗漏关键约束，导致后续 Agent 做错，怎么发现并修正？

发现方式：

- 看实际行为：测试失败、git diff 不符合目标、CompletionGate 拦截、用户指出约束冲突。
- 看证据链：查 transcript、prompt dump、compact_summary、`## Runtime Extracted Facts`，确认约束是在压缩前就没出现，还是压缩时丢了，还是恢复时没加载。
- 对 compact/resume bug，跑 `internal/compact`、`internal/query` 和相关 acceptance scripts，验证摘要恢复路径。

修正方式：

- 立即重新读取真实文件、AGENTS.md、用户原始需求和 git diff，用事实覆盖摘要。
- 如果是摘要规则缺失，把约束纳入 hard facts 或 summary validation。
- 如果是 resume 没恢复，把 `MessagesFromTranscript` / `recordCompact` 路径补测试。
- 如果是任务已经做错，按当前 diff 做最小修复，必要时用 checkpoint/rewind 或 git 恢复自己本轮改动，但不能回滚用户无关改动。

## 9. 长期记忆里有过时文件摘要，后续 Agent 又拿它判断，怎么处理？

原则：长期记忆只能做路由和提醒，不能替代当前仓库事实。

当前项目里已经有几个防污染方向：

- memory 里强调“re-check current files, branch state, live runtime evidence”。
- code mode 的 memory 是项目上下文补充，真正改代码前仍要求读取当前文件。
- trace/memory-review 候选和 active memory 应区分 pending、approved、rejected、archived；只有 approved active memory 才应进入 prompt。
- 对易漂移信息，例如配置、端口、auth、branch、runtime 状态，必须用 `status`、`/health`、`git status`、实际文件重新验证。

如实边界：

- 当前无法保证所有历史摘要天然不过时。
- 如果发现污染，应新增更强的“失效条件”：文件 hash、mtime、commit id、生成时间、适用路径、验证命令。
- 对关键判断，最终以当前文件、当前进程、当前数据库、当前 git 状态为准。

## 10. 模型把错误结论写进记忆，后续一直沿用，怎么纠错？

纠错流程应该是：

1. 找到错误结论来源：memory entry、compact summary、recap、doc、transcript 还是 agent final。
2. 用当前代码和测试证明它错在哪里。
3. 写入更高优先级的纠错记忆或更新 review 状态，把错误记忆标成 rejected/archived。
4. 如果错误来自文档，直接修文档并加验证命令。
5. 如果错误来自 compact/resume 提取逻辑，补测试防复发。

当前边界是：go-claude 已有 memory review 和 trace 方向，但“自动发现模型写错记忆”不是完全解决的问题。工程上应该让记忆带证据、来源、时间、适用范围和校验命令，避免一句结论长期无条件生效。

## 11. 多轮执行中怎么识别重复状态？

当前有几类重复治理：

- `toolResultSeenIDs` 和 content replacement 避免历史大 tool_result 反复进入 prompt。
- session transcript、tool trace、telemetry 能看到同一轮读了什么、跑了什么。
- `ToolResultMessageBudget` / `ToolResultHistoryBudget` 控制旧工具结果重复注入。
- compact 后以 summary reset boundary 替代长历史。
- sub-agent evidence 使用 `follow_up_id`、`resolved_follow_up`、`supersedes_evidence_id`，避免 compact/resume 后重新激活已经解决的 follow-up。
- prompt dump verifier 能检查 required/forbidden text、tool_result 是否重复泄漏或超限。

如实边界：

- 当前没有一个全局“重复文件读取去重调度器”阻止模型再次读同一文件。
- 读文件是否重复，很多时候靠 prompt 约束、TUI/tool trace 可见性和模型自我规划。
- 可以进一步做 `ActionLedger` / `EvidenceLedger`：同一路径、同一 hash、同一范围、同一目的的读操作可提示“已有证据”，但写操作和验证操作不能简单去重。

## 12. 怎么看 RAG、Memory 和 Context Engineering？

三者解决的问题不同：

| 概念 | 解决的问题 | 在 go-claude 里的对应 |
| --- | --- | --- |
| RAG | 从外部知识库按需检索相关材料 | tenant documents/knowledge、WebSearch、Grep/Glob/Read、MCP resources |
| Memory | 跨会话保留用户偏好、项目规则、已验证事实 | code memory、project memory、memory review、session recap、compact summary |
| Context Engineering | 决定本轮模型到底看见什么、顺序如何、预算如何、哪些必须验证 | `query.Session` prompt assembly、tools、runtime status、prompt cache、auto compact、tool-result budget、CompletionGate |

RAG 是取资料，Memory 是保留长期状态，Context Engineering 是把资料、状态、工具、约束和当前任务按优先级拼成一次可执行请求。Coding Agent 里三者必须配合：RAG 找事实，Memory 提醒历史偏好，Context Engineering 决定哪些事实进入本轮，工具和测试再验证事实是否仍然成立。

## 13. Agent Harness 是什么？和单纯调用 LLM API 的区别？

单纯调用 LLM API：

```text
prompt -> model -> text
```

Agent Harness：

```text
goal/task
  -> planner/context builder
  -> model
  -> tool execution
  -> observation/evidence
  -> state update
  -> verification/evaluator
  -> retry/recover/stop
  -> report
```

Harness 的重点是让 agent 可控、可测、可重复：

- 统一工具协议和权限边界。
- 记录每一步 evidence。
- 处理失败、timeout、取消、resume、compact。
- 用 evaluator 判断完成、继续或 blocked。
- 产出 JSON/Markdown 报告，支持 benchmark 和回归。

go-claude 里已经有局部 harness：`internal/agenteval`、prompt acceptance matrix、subagent/multi-agent acceptance、goal evaluator、trace/telemetry。它还不是一个完全独立、通用、跨 agent 的 APG 平台；项目文档里也明确了更通用的 Agent Proving Ground 应该是独立项目，而不是塞进当前内部 eval 单文件。

## 14. 是否有评测体系？怎么判断 agent 变强？

当前有评测体系，但要分清层次：

- 单元测试：各工具、权限、provider、session、compact、query、server。
- golden tests：query transcript、stream-json、server OpenAI response、session export/compact、大文件处理等。
- acceptance scripts：prompt dump、Task/Agent、skills、resume replacement、heavy tool-result、background agent、compact follow-up、failed agent recovery。
- `internal/agenteval`：deterministic local eval，覆盖 query、tool、skills、sub-agent、auto compact、permission、trace/telemetry。
- live profiles：可选验证真实 API Server、agent task API、Anthropic thinking。
- APG 设计文档：更通用的跨 agent benchmark/scoring 方案。

判断一次改动真的变强，不能只看“这次模型回答更好”。至少要有：

- 同一模型、同一任务、同一输入条件下对比。
- 成功率、完成时间、工具调用次数、重复读写次数、测试通过率、回滚次数、人工介入次数。
- evidence 质量：是否读了正确文件、是否跑了正确测试、是否暴露未知和风险。
- 回归不过线不能合并：golden、acceptance、`go test ./...`、`git diff --check`。
- 对真实任务，要看 diff 是否正确、测试是否通过、用户目标是否闭环，而不只是 final answer 漂亮。

如实边界：当前 deterministic eval 更像“主链路不退化”的回归体系，不足以单独证明开放世界 coding 能力全面提升。开放世界能力需要 APG 类多任务、多模型、可复跑评分。

## 15. 生成错误 patch 时怎么回滚、验证和再尝试？

当前正确流程：

1. 先看 `git status --short` 和相关 diff，确认哪些改动是本轮 agent 产生的，哪些是用户/其他 agent 的无关改动。
2. 不使用 `git reset --hard` 或 `git checkout --` 这类破坏性命令，除非用户明确要求。
3. 对自己本轮错误 patch，用反向 `apply_patch`、再次 `Edit`、或基于 checkpoint/rewind 做最小恢复。
4. 重新读取被修改文件，确认没有覆盖用户改动。
5. 跑目标测试、必要 golden、`git diff --check`。
6. 如果错误原因是上下文遗漏，补读文件/补写测试/补文档，避免再次靠猜。

能力边界：

- go-claude 有 checkpoint、rewind、session transcript、file change tracking，但不是所有文件修改都能自动语义回滚。
- Git 是最后防线，但不能粗暴重置整个工作区。

## 16. 平时用什么 AI？一个需求通常怎么用 AI 辅助开发？

在这个项目语境里，合理的 AI 使用方式不是“把需求丢给模型直接改”，而是把 AI 当成可调用工具链的工程助手：

- 用 go-claude/Codex/Claude Code 类 agent 做代码阅读、方案拆解、局部实现、测试执行、回归检查。
- 用强模型做复杂设计、review、边界分析和失败根因定位。
- 用本地工具确认真实状态：`rg`、`go test`、`git diff`、`git status`、curl、浏览器、数据库查询。
- 对高风险接口、数据库、权限、沙箱、WebUI，不接受只基于模型文字的结论。

典型流程：

1. 先让 AI 总结需求，但要求列出不确定点。
2. 让 AI 读相关代码和文档，输出证据。
3. 先定方案和测试点，再实现。
4. 让 AI 做小步 patch。
5. 运行测试和真实链路验证。
6. 让 AI review diff，重点看回归、边界、遗漏测试。
7. 通过后提交、push，并记录剩余风险。

## 17. 使用 AI 时如何拆需求、读代码、改代码、跑测试和 review？

推荐流程：

1. 需求拆解：把目标拆成输入、输出、行为变化、非目标、风险、验收标准。
2. 读代码：先 `rg` 定位入口，再读调用链、测试和文档，不直接全局重构。
3. 方案设计：明确改哪些文件、为什么、失败路径、兼容性、测试命令。
4. 小步实现：每次只改与目标强相关的模块，优先复用现有抽象。
5. 测试验证：先跑相关包测试，再跑必要 golden/acceptance，最后 `git diff --check`；代码改动较大再跑 `go test ./... -count=1`。
6. Review diff：看是否混入无关文件、是否破坏 API/schema、是否绕过权限、是否缺少错误处理、是否文档同步。
7. 闭环交付：说明改了什么、验证了什么、没验证什么、剩余风险是什么；默认在测试通过后 commit/push，但如果用户明确说先不要提交，就停在 diff 和验证结果。

## 能力边界清单

| 领域 | 当前能力 | 当前边界 |
| --- | --- | --- |
| 本地 coding loop | 多轮 tool_use、文件读写、命令、测试、final gate | 仍依赖模型正确规划，runtime 不能证明所有语义正确 |
| 多模型 | Anthropic Messages + OpenAI Chat Completions + OpenAI Responses + fallback | provider error taxonomy 还不统一；Responses stateful/hosted tools 未接入；能力不完全等价 |
| 工具 | 统一 Tool 接口、registry、permission guard、hooks、sandbox | 工具语义主要靠 description/schema/test，不是形式化证明 |
| 失败恢复 | 工具错误显式回传、恢复提醒、partial stream recovery、compact circuit | 非幂等工具没有全局自动 retry |
| 上下文压缩 | auto compact、硬事实、summary validation、resume boundary | 摘要完整性不能 100% 自动证明 |
| 记忆 | code memory、session recap、memory review 方向 | 过期/错误记忆仍需来源、时间、验证和人工/测试纠错 |
| 重复状态 | tool-result budget、replacement、follow_up_id、prompt dump | 没有全局动作去重调度器 |
| 评测 | unit/golden/acceptance/agenteval/live profiles | 开放世界 benchmark 仍需要 APG 类独立体系 |
| 回滚 | git diff、checkpoint/rewind、最小反向 patch | 不能粗暴 reset 用户工作区 |
| UI/API | TUI、WebUI、API Server、Mobile、tenant | WebUI/TUI parity 和真机覆盖仍有 backlog |

## 面试式总结

如果要一句话介绍 go-claude：它是一个用 Go 实现的通用 Agent runtime，借鉴了 Claude Code 的部分设计思想，已经具备模型适配、工具执行、权限/沙箱、会话恢复、上下文压缩、sub-agent、skills、API Server 和评测回归等工程骨架；它的优势是链路真实、可观测、可测试、可平台化，短板是部分借鉴能力尚未完全对齐所声称的兼容边界，错误恢复和长期记忆治理还需要更多确定性 runtime 约束和开放世界 benchmark 来继续强化。
