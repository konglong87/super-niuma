# Claude Code 原版 vs go-claude 深度差异诊断

本文对比本仓库 `go-claude` 与本机原版 Claude Code 源码目录：

- go-claude：`/path/to/golang-cc`
- 原版 Claude Code：`$HOME/GolandProjects/claude_code_src_2026`

目标是解释：在同模型、同任务、同工作区下，为什么两者表现可能出现巨大差异。

结论先行：**主因不是某一句 system prompt 文案，而是上下文运行时架构差异**。
原版 Claude Code 在每轮模型调用前有完整的动态上下文层，包括 attachments、todo/plan reminder、task status、relevant memory、skill listing delta、tool result budget、microcompact/autocompact、permission context 等。
当前 go-claude 虽然已有 system prompt、memory、skills、Todo、Task、Agent、compact 和 TUI 进度，
但这些能力更多是分散的工具或一次性上下文，尚未形成原版那种“每轮按状态重建可执行上下文”的闭环。

## 证据边界

本诊断只使用本地源码、文档、git commit 与测试证据；未修改原版源码。

必须明确：如果没有昨天那次两边运行的完整 transcript / model request dump，本文件不能证明“昨天具体哪一个 token 或哪一次 tool call 导致失败”。但源码已经能证明若干结构差异会必然改变模型看到的输入，从而足以解释同模型同任务下的行为差距。

要把“结构性根因”升级为“昨天具体根因”，还需要采集：

- 两边同一任务第一轮与后续每轮的完整 model request JSON：`system`、`messages`、`tools`、`tool_choice`、`thinking`、`response_format`。
- 两边完整 transcript：assistant text、tool_use input、tool_result output、compact summary、resume boundary。
- 启动入口与配置：CLI/TUI/headless/server/mobile、prompt mode、permission mode、feature flags、model id、max turns、tool result limit、compact settings。
- TUI 录屏或日志：是否出现工具进度不可见、permission prompt、Task/Agent 后台状态、Todo/Plan 状态丢失。

## 最近提交背景

这次分析重点参考了最近三个相关提交：

- `1d7f584 docs: 追加 Task/Agent 渲染改进 TODO + 审阅修正记录`
- `83e9edc docs: 补充 Claude Code 原版渲染结构参考 — 对比 go-claude 差异`
- `15e646b feat: improve tui tool and task progress`

前两个提交只更新 `docs/prompt_logic/tui_tool_progress_enhancement.md`，记录 TUI 工具/Task/Agent 渲染差异。第三个提交已经把部分 TUI 工具进度能力落到代码里，包括 `StreamEvent.Input`、tool input summary、Todo 面板、tool activity 归档等。因此，**TUI 输入摘要缺失可以解释历史体验差距，但不能再直接当作当前代码的完整事实**。当前更重要的未对齐点已经从“有没有显示工具输入”上移到“模型每轮是否收到同等质量的动态上下文”。

## 最终判断

### P0-1：动态上下文注入层缺失或不等价

**判断**：这是最核心的结构性差异。原版 Claude Code 每轮都会根据 app state、messages、toolUseContext 和 recent tool state 生成 attachments；go-claude 当前主循环没有等价层。

**为什么会导致差距**：

模型不是只看第一轮 system prompt。长任务表现强弱，取决于每一轮模型调用前是否能看到“当前任务状态、计划状态、已完成/未完成 todo、后台 task 新进展、相关 memory、可用 skill 变化、工具输出是否被安全压缩”。原版把这些作为消息级 attachment 注入；go-claude 主要在 run 开始时加载 memory 和 skills catalog，之后靠 tool_result 串起来。

**go-claude 证据**：

- `Session.run` 开始时组装 `InitialMessages`、`assembleContextMessages`、当前 user message，然后进入 tool loop；没有统一 attachments pass：`internal/query/query.go:948-952`。
- code profile 下 memory 只由 `assembleContextMessages` 通过 `memory.LoadCode(s.options.CWD, prompt)` 加载：`internal/query/query.go:2812-2825`。
- skills catalog 在 system prompt 组好后追加一次：`internal/query/query.go:978-987`。
- tool result 结束后只把 `trace.Output` 追加为下一轮 user `tool_result`：`internal/query/query.go:1182-1192`。

**原版证据**：

- `getAttachments` 会综合 user input、queued commands、changed files、nested memory、dynamic skill、skill listing、plan mode、plan exit、todo reminders、task status、verify plan reminder 等：`src/utils/attachments.ts:741-894`。
- plan mode 会按 human turns 节流反复注入，并区分 full/sparse reminder：`src/utils/attachments.ts:1186-1242`。
- todo reminder 会读取 appState.todos 并在长时间未写 todo 时注入：`src/utils/attachments.ts:3266-3315`。
- unified task status 会从 Task framework 生成 task_status attachment：`src/utils/attachments.ts:3435-3461`。
- relevant memory 会按输入检索最多 5 个 memory，并以 `<system-reminder>` 注入：`src/utils/attachments.ts:2196-2242`。

**置信度**：高。

**因果强度**：极强。它改变的是模型每轮输入，不是 UI 感知。

### P0-2：Task/Agent 工具的提示词契约明显弱于原版

**判断**：这是第二个高概率主因，尤其当昨天任务需要拆分、调查、并行验证、子 agent 审阅或后台执行时。

**为什么会导致差距**：

同模型是否能正确使用 sub-agent，不只取决于有没有 `Task` 工具，还取决于工具 prompt 是否告诉模型：

- agent 是否从零上下文开始；
- prompt 必须包含什么背景；
- 什么任务该 fork，什么任务不该 fork；
- 后台 agent 未返回时不能编造结果；
- sub-agent 结果默认不对用户可见，主 agent 必须总结；
- 简单搜索不要滥用 agent；
- agent list、permission、MCP 可用性如何影响选择。

这些都直接影响模型任务分解质量。

**go-claude 证据**：

- `Task.Description()` 只有同步/批量/后台、简单搜索优先本地工具、继承父工具权限等简要说明：`internal/tools/task/task.go:86-95`。
- `Task.Run()` 同步模式最终直接返回 `result.Content`：`internal/tools/task/task.go:187-203`。
- `AgentCreate.Description()` 只说明创建后台 sub-agent、用 AgentGet/AgentMessage 查进度：`internal/tools/agent/agent.go:40-45`。
- `AgentGet` 的 progress summary 只汇总 turns、tool calls、latest text 等，最新文本有截断：`internal/tools/agent/agent.go:145-179`、`internal/tools/agent/agent.go:330`。

**原版证据**：

- `AgentTool` schema 支持 `description`、`prompt`、`subagent_type`、`model`、`run_in_background`、`name`、`team_name`、`mode`、`isolation`、`cwd`：`src/tools/AgentTool/AgentTool.tsx:81-101`。
- Agent prompt 写明 fresh agent starts with zero context，必须说明目标、已知/排除、周边上下文、精确命令或问题：`src/tools/AgentTool/prompt.ts:99-113`。
- fork 语义写明“不要 peek、不要 race、不要伪造结果、通知是后续 user-role message”：`src/tools/AgentTool/prompt.ts:80-96`。
- usage notes 写明 agent result 不对用户可见，主 agent 要向用户总结：`src/tools/AgentTool/prompt.ts:255-264`。
- 原版 `AgentTool` `maxResultSizeChars` 为 `100_000`：`src/tools/AgentTool/AgentTool.tsx:229`。

**置信度**：高。

**因果强度**：极强。会直接改变是否会高质量委派、是否会把调查证据带回主线程、是否会编造未返回结果。

### P0-3：tool_result 处理策略导致后续规划输入不等价

**判断**：这是第三个高概率主因，尤其当任务包含大输出搜索、长文件读取、测试日志、diff、编译错误或多工具并发结果时。

**为什么会导致差距**：

工具输出不是 UI 细节，而是下一轮模型的输入。go-claude 当前对工具输出采用简单截断；原版有 per-message budget、持久化 preview/path、空结果保护、microcompact/autocompact 多层处理。大输出一旦截断在关键位置，模型下一步会基于残缺证据规划。

**go-claude 证据**：

- `runTool` 结束前调用 `truncateToolResult(res.Content, s.options.ToolResultLimit)`：`internal/query/query.go:1882`。
- `truncateToolResult` 只保留前 `limit` 字节并追加 omitted marker：`internal/query/query.go:2557-2563`。
- tool result 以 `trace.Output` 直接进入下一轮 user message：`internal/query/query.go:1182-1192`。
- recorder 记录的也是截断后的 `trace.Output`：`internal/query/query.go:1583-1594`。

**原版证据**：

- query 前会 `applyToolResultBudget`，并且在 microcompact/autocompact 前处理工具结果大小：`src/query.ts:365-394`。
- 大 tool result 可写入 session tool-results 目录，以 `<persisted-output>` preview/path 替换：`src/utils/toolResultStorage.ts:1-31`、`src/utils/toolResultStorage.ts:205-226`。
- 空 tool result 会替换为 `(<tool> completed with no output)`，避免模型误判 turn boundary：`src/utils/toolResultStorage.ts:280-294`。

**置信度**：高。

**因果强度**：强。长任务中一次关键日志截断就足以导致错误方向。

## P1 根因候选

### P1-1：system prompt 文案与 section 完整度不等价

**判断**：system prompt 不是唯一根因，但确实存在行为约束差异。

**go-claude 证据**：

- system prompt 优先级为 override > coordinator > main-thread agent > custom > default：`internal/query/query.go:2650-2668`。
- default prompt 的 enhanced planning/verification/recovery/workflow closure 受 `ENHANCED_SYSTEM_PROMPT` feature gate 控制：`internal/query/query.go:2740-2747`。
- `doingTasksSection` 有 read-before-change、安全、验证、unexpected state 等规则：`internal/query/query.go:3575-3604`。
- `usingToolsSection` 有专用工具优先、探索时直接调用工具、多工具并行、TodoWrite、Task 等规则：`internal/query/query.go:3750-3773`。

**原版证据**：

- `getSystemPrompt` 静态段 + 动态段，其中动态段包括 session guidance、memory、env、language、output style、MCP、scratchpad、function result clearing、tool result summary 等：`src/constants/prompts.ts:444-577`。
- `getSimpleDoingTasksSection` 比 go-claude 更细，包含“高度有能力”、不要估时、失败先诊断、不要 backward-compat hack、结果诚实汇报等：`src/constants/prompts.ts:199-253`。
- `getSessionSpecificGuidanceSection` 会根据当前工具注入 AskUserQuestion、Agent、skills、DiscoverSkills、verification-agent 合约：`src/constants/prompts.ts:352-400`。
- env info 包含 cwd、git repo、platform、shell、OS、model、Claude Code 可用形态、fast mode 等：`src/constants/prompts.ts:651-710`。

**结论**：

如果昨天任务主要失败在“没读文件就改、没验证、乱重试、口述太多、过早总结”，system prompt 文案可能是直接因素。但如果失败表现是“计划断、子任务差、上下文丢、后续方向错”，主因更可能是 P0 的上下文运行时架构。

**置信度**：中高。

### P1-2：Todo/Plan 是工具状态，但没有形成模型上下文闭环

**判断**：go-claude 已有 TodoWrite/TodoRead 和 PlanMode，但它们当前更像文件持久化工具 + TUI 展示，不等价于原版的计划/任务上下文机制。

**go-claude 证据**：

- `TodoWrite` 写 `.claude/todos.json`，返回 `Saved N todos to <path>`：`internal/tools/todowrite/todowrite.go:123-137`。
- `TodoRead` 只在模型主动调用时读取 `.claude/todos.json`：`internal/tools/todowrite/todowrite.go:43-58`。
- `EnterPlanMode` 写 `.claude/plan_mode.json`，返回 `Entered plan mode`：`internal/tools/planmode/planmode.go:42-55`。
- `ExitPlanMode` 写 active=false，返回 `Exited plan mode`：`internal/tools/planmode/planmode.go:75-88`。
- 搜索结果显示 TUI 会读取 todos 并展示，但没有 query 主循环自动读取 `plan_mode.json` 注入下一轮 prompt 的证据。

**原版证据**：

- TodoWrite 不做文件型输出，而是更新 appState.todos，并 map 成行为性 tool_result，提醒继续用 todo：`src/tools/TodoWriteTool/TodoWriteTool.ts:65-114`。
- Todo prompt 详细规定何时使用、何时不用、状态规则、必须 `content` + `activeForm`、完成后立即更新：`src/tools/TodoWriteTool/prompt.ts:3-184`。
- plan mode attachments 会根据 permission mode、turn count、是否重新进入、是否已有 plan 生成 reminder：`src/utils/attachments.ts:1186-1242`。
- plan exit attachment 会在退出计划模式后一次性告诉模型状态变化：`src/utils/attachments.ts:1245-1273`。

**置信度**：中高。

### P1-3：Skills progressive loading 约束弱于原版

**判断**：go-claude 有 skills catalog 和 Skill tool runtime，但原版对“何时必须调用 Skill”和“skills 如何跨 compact 保留”更强。

**go-claude 证据**：

- catalog 只预加载 metadata，提示相关时调用 Skill tool：`internal/skills/skills.go:330-362`。
- Skill tool 调用后，`applySkillRuntime` 设置 active skill 的 allowed tools、model、context、paths、args、agent、hooks 等：`internal/query/query.go:2232-2263`。
- tenant inline skills 可直接拼入 system prompt：`internal/query/query.go:1935-2003`。

**原版证据**：

- skill listing 有 1% context budget 与单 entry 250 chars 限制：`src/tools/SkillTool/prompt.ts:20-29`。
- Skill tool prompt 明确：匹配 skill 时是 blocking requirement，必须先调用 Skill tool；不要只提 skill 不调用：`src/tools/SkillTool/prompt.ts:188-194`。
- SkillTool 支持 forked skill execution：`src/tools/SkillTool/SkillTool.ts:122-146`、`src/tools/SkillTool/SkillTool.ts:619-631`。
- remote skill 加载会记录并注册 invoked skill，让内容在 compact 后保留：`src/tools/SkillTool/SkillTool.ts:963-1092`。

**置信度**：中。

### P1-4：Memory 机制目标不同

**判断**：go-claude 的 memory loader 覆盖面并不小，但更偏“加载项目指导文档”；原版还有 typed memory、index、relevant memory surfacing 与保存/更新规则。

**go-claude 证据**：

- `LoadCode` 加载 managed、`~/.claude/CLAUDE.md`、项目 `CLAUDE.md/AGENTS.md`、`.claude/CLAUDE.md`、rules、local、workflow、team/auto、ClaudeCodeProjectMemory：`internal/memory/memory.go:25-97`。
- `SystemAddendumFromDocuments` 把所有文档拼成 `# Memory` user-context reminder：`internal/memory/memory.go:116-129`。

**原版证据**：

- memory entrypoint `MEMORY.md` 有 200 行和 25KB 限制：`src/memdir/memdir.ts:34-39`。
- `buildMemoryLines` 明确 memory 类型、保存方式、不要把 plan/task 混成 memory、何时访问历史上下文：`src/memdir/memdir.ts:199-266`。
- relevant memory attachment 会按当前 input 检索、去重、限流、读取并注入：`src/utils/attachments.ts:2196-2340`。

**置信度**：中。

## P2 影响因素

### P2-1：入口和 prompt mode 不同会让“同模型同任务”不成立

**判断**：如果昨天一边走 TUI/CLI code mode，另一边走 server/mobile/chat mode，就不是同一上下文。

**go-claude 证据**：

- `newQuerySession` 默认把 `PromptMode` 解析为 code：`internal/cli/cli.go:2326`。
- 文档规定 CLI/TUI/headless 默认 `code`，`/v1/chat/completions` 和 `/mobile/chat/...` 默认 `chat`：`docs/prompt_logic/code_and_chat_prompt_modes.md`。
- mobile 测试明确断言 query req 的 `PromptMode == "chat"`：`internal/server/mobile_test.go:484-490`。

**影响**：

- code mode 会加载项目规则、git 快照、本地 skills。
- chat mode 不加载本地 `CLAUDE.md/AGENTS.md`、git、local skills。
- 如果对比时入口不同，则差距可能主要来自 prompt profile，而不是实现质量。

**置信度**：高，但是否命中昨天情况未知。

### P2-2：Permission、checkpoint/resume、compact 会制造隐藏上下文差异

**Permission 证据**：

- Go 工具通过 `tools.Guard` 和 `PermissionPrompt` 处理 allow/deny，permission audit 写 transcript：`internal/query/query.go:1835-1844`、`internal/query/query.go:2501-2536`。
- resume 转换只处理 message、thinking、tool_call、tool_result、compact_summary、recap_summary；没有把 permission entry 转成模型上下文：`internal/query/query.go:1317-1366`。
- 原版 query call 会把 `getToolPermissionContext` 传给 callModel 和 AgentTool prompt：`src/query.ts:659-669`、`src/tools/AgentTool/AgentTool.tsx:196-225`。

**Compact/resume 证据**：

- Go `MaybeCompact` 在每轮前执行，成功后用 compact messages 替换原 messages：`internal/query/query.go:1010-1027`。
- resume 时 `compact_summary` 会重置为 `"Conversation summary so far:\n..."`，`recap_summary` 明确不进入模型上下文：`internal/query/query.go:1358-1365`。
- 原版 query 前有 tool result budget、snip、microcompact、context collapse、autocompact 多层上下文压缩：`src/query.ts:365-535`。

**影响**：

这些不是必然坏，但会让两边上下文边界不同。特别是 resume/compact 后，哪些事实被保留、哪些 tool result 被替换、哪些 skill/memory 重新浮现，会直接影响后续执行。

**置信度**：中。

### P2-3：TUI 渲染影响用户感知，也可能间接影响模型行为

**判断**：TUI 不是模型能力主因，但会影响“看起来是否像原版”，也会影响模型是否用文字口述进度来补 UI 空白。

**go-claude 证据**：

- 最近两个背景提交 `1d7f584` 和 `83e9edc` 都只改 `docs/prompt_logic/tui_tool_progress_enhancement.md`，记录原版渲染结构和 go-claude 差异。
- 最新提交 `15e646b feat: improve tui tool and task progress` 已改 `internal/query/query.go`、`internal/tui/app.go`、`internal/tui/app_test.go`，说明 TUI 工具进度确实刚被修过。
- 当前 `StreamEvent` 已有 `Input` 字段：`internal/tui/app.go:179-195`。
- 当前 `toolInputSummary` 会从 tool input 生成 Read/Grep/Bash/Edit/Write/Glob/Task 摘要：`internal/tui/app.go:423-490`。
- 当前 TUI 仍有独立 tool activity view，并非完全原版 inline stream 渲染：`internal/tui/app.go:2864-2890`。

**原版证据**：

- tool result render 会拿到对应 tool_use input：`src/components/messages/UserToolResultMessage/UserToolSuccessMessage.tsx:65-73`。
- assistant tool use message 根据 tool input schema、userFacingName、progress message 渲染工具调用：`src/components/messages/AssistantToolUseMessage.tsx:61-80`。

**影响**：

如果昨天用户主要感受到“go-claude 不知道自己在干什么、一直口述”，TUI 是重要因素。但如果实际结果错误、计划断裂、工具选择差，TUI 只是表层。

**置信度**：中。

## 因果定责矩阵

下面把“症状 -> 最可能因素 -> 如何证明/排除”写成定责矩阵，避免把所有差异平均摊开。

| 观察到的症状 | 最可能主因 | 为什么 | 如何证明 | 如何排除 |
| --- | --- | --- | --- | --- |
| 第一轮就没有按项目规则行动，例如没遵守 `AGENTS.md`、没读相关文件、没加载本地 skills | 入口/prompt mode 或 memory 加载差异 | code mode 才加载项目 guidance、git、local skills；chat mode 不加载本地项目上下文 | dump 第一轮 request，看是否有 `# Memory`、项目 `AGENTS.md`、git context、skills catalog | 两边第一轮 request 都含同样项目 guidance，则排除入口/memory 为首因 |
| 第一轮还行，几轮工具调用后计划断裂、忘记 todo/plan、重复做已完成步骤 | 动态 attachments/reinjection 缺失 | 原版会把 todo/plan/task/memory 状态按轮次回灌；go-claude 主要依赖历史 messages 和 tool_result | dump 第 2/3/4 轮 request，看是否有 todo_reminder、plan_mode、task_status、relevant_memories 等等价消息 | 如果 go-claude 后续 request 也有等价状态注入，再看 tool_result/compact |
| 子 agent 结果浅、prompt 过短、缺少文件路径/已知事实，或后台 agent 未返回时主线程猜结果 | Task/Agent prompt contract 差异 | 原版 Agent prompt 明确 fresh agent 零上下文、如何写 prompt、不要 race/peek/编造；go-claude 工具说明短 | 对比 model 发出的 Task/Agent tool input，检查 prompt 是否包含已知/排除/路径/问题/输出要求 | 如果 Task prompt 质量等价，再看 agent runtime、权限和 tool visibility |
| 搜索/测试/编译输出很多，后续判断漏掉关键尾部证据 | tool_result 截断/持久化差异 | go-claude 简单保留前 N 字节；原版可 persisted preview/path 并做 budget | 构造关键证据在 20KB 后的大输出，看下一轮 request 是否有完整路径/preview | 如果输出小且完整进入两边 request，则排除该因素 |
| 模型知道有 skill 但不调用，或直接按普通方式做了应该由 skill 接管的任务 | Skill blocking requirement 差异 | 原版 Skill prompt 把匹配 skill 设为 blocking requirement；go-claude catalog 只说相关时调用 | dump first assistant turn，检查是否先调用 Skill；看 system/tool prompt 是否含 blocking requirement | 若没有匹配 skill 或两边都先调用 Skill，则排除 |
| 反复问权限、被拒后重试同样工具、permission 状态影响 agent 选择 | Permission context 差异 | 原版把 toolPermissionContext 传给 callModel/AgentTool；go-claude audit 记录不进入 resume 模型上下文 | dump request + transcript，检查 permission mode、denial、session allow/deny 是否进入模型可见上下文 | 如果没有 permission prompt/denial，则不是主因 |
| resume/compact 后丢事实、重复旧步骤、skill/memory 不再可见 | compact/resume 策略差异 | 原版有 budget/snip/microcompact/collapse/autocompact；go-claude compact_summary reset 更简单 | 找 transcript 是否出现 compact_summary；对比 compact 后第一轮 request 的 facts/files/tools 保留情况 | 未触发 resume/compact 时排除 |
| 用户主要感觉“过程不可见、一直口述、看不出在做什么”，但最终结果未必错 | TUI 渲染差异 | 历史 go-claude running 工具缺 input summary；当前已有部分修复但仍非完全 inline | 看 TUI 录屏、StreamToolStart/Result 是否带 Input、tool activity 是否 inline | 如果 prompt dump 显示模型输入已偏离，TUI 只是感知层 |

从定责矩阵看，**最能解释“表现差别巨大”的不是单个 prompt 文案，而是 P0-1、P0-2、P0-3 的组合**。其中 P0-1 是首要，因为它决定模型每轮看到什么；P0-2 决定复杂任务如何拆出去；P0-3 决定工具证据是否完整进入下一步规划。

## 多维度对照表

| 维度 | 原版 Claude Code | go-claude 当前状态 | 差异影响 |
| --- | --- | --- | --- |
| system prompt 架构 | 静态段 + 动态段 + section registry；session guidance 强 | 有等价优先级和 sections，但部分增强 behind feature gate | 中到强 |
| 每轮上下文注入 | attachments 层统一处理 todo/plan/task/memory/skill 等 | run 起始加载 memory/skills，缺少统一 per-turn attachments | 极强 |
| Task/Agent 工具 prompt | 详细说明零上下文、fork、后台、不要编造、结果不可见 | 工具描述短，功能有但模型契约弱 | 极强 |
| tool_result 处理 | budget、persist preview/path、empty guard、microcompact | 简单截断并直接作为 tool_result | 强 |
| Todo | appState + 行为性 tool_result + reminder attachment | 写 `.claude/todos.json`，TUI 展示，模型需主动 TodoRead | 强 |
| Plan | plan mode reminder/reentry/exit attachment | `.claude/plan_mode.json` 工具，未见自动 query 注入 | 强 |
| Skills | blocking requirement、budget listing、remote discovery、forked/preserve | metadata catalog + Skill runtime，blocking 文案弱 | 中到强 |
| Memory | typed memory、MEMORY.md index、relevant memory surfacing | 多路径 guidance loader，一次性注入为 Memory | 中 |
| Permission | permission context 参与 prompt/tool prompt | audit 记录但 resume 不转模型上下文 | 中 |
| Compact/resume | budget/snip/microcompact/collapse/autocompact | auto compact + compact_summary reset | 中 |
| TUI | inline tool use/result/progress，输入与输出关联 | 已改善 input summary，但仍非完全 inline | 中 |
| 入口模式 | 原版 CLI 默认 code agent 上下文 | go-claude server/mobile 默认 chat，CLI/TUI 默认 code | 取决于入口，可能极强 |

## 最可能的组合根因

如果昨天确实是“同模型、同任务、同工作区、同 CLI/TUI 代码入口”，最可能组合是：

1. **P0-1 动态上下文层缺失**：todo/plan/task/memory/skill 状态没有像原版一样每轮回灌。
2. **P0-2 Task/Agent prompt 契约不足**：复杂任务分解和子 agent 结果回收质量下降。
3. **P0-3 tool_result 截断/形状不等价**：后续规划基于不完整或不够结构化的工具结果。
4. **P1-1 system prompt 细节不足**：加剧 read-before-change、验证、失败诊断、输出节奏等差异。
5. **P2-3 TUI 展示不足**：让用户感知差距更明显，并可能诱导模型用文字补进度。

如果昨天一边走原版 CLI，另一边走 go-claude OpenAI-compatible server/mobile，则最可能主因先变成：

1. **入口 prompt profile 不同**：go-claude chat mode 不加载本地 code context。
2. **tenant/server inline skill 与 TUI local skills 差异**。
3. **server/mobile 历史消息 flatten、response_format、tenant context 与 TUI code context 差异**。

因此，下一步验证必须先确认入口。

## 高 ROI 优化路线

### 1. 先做最小 attachments layer

**改动**：

在 `query.Session.run` 每轮模型调用前增加一个 `assembleDynamicContextMessages(...)`，按节流注入：

- active todos：从 `.claude/todos.json` 读取，作为 `<system-reminder>` 注入；
- active plan：从 `.claude/plan_mode.json` 读取，plan mode/reentry/exit 注入；
- background/agent task delta：从 `TaskStore` 读取近期变更；
- skill listing delta：只注入新增或当前 prompt 命中的 skill；
- relevant memory：先做简单 grep/BM25 或文件名匹配，后续再接更复杂 selector。

**预期影响**：最大。让模型每轮看到当前状态，而不是只靠自己记忆和工具结果。

**风险**：

- prompt 变长；
- 重复注入污染上下文；
- chat mode 不能泄露本地 workspace。

**验证**：

- golden test：同一个 todo/plan/task 状态必须出现在下一轮 request messages。
- transcript test：compact/resume 后不会重复无限注入。
- 实跑对比：同任务前 3 轮 prompt dump，对比原版 attachment 类型覆盖率。

### 2. 移植 AgentTool prompt 的核心规则，不先重写 runtime

**改动**：

优先补 Go `Task.Description()` / AgentCreate prompt 文案：

- fresh sub-agent starts with zero context；
- prompt 必须包含目标、已知、排除、文件路径、精确问题；
- 不要写 “based on your findings, fix it”；
- 后台任务未返回不能编造；
- agent result 不对用户可见，主 agent 要总结；
- 简单 directed search 用本地 Grep/Glob/LS。

**预期影响**：明显提升复杂任务分解、验证 agent、审阅 agent质量。

**风险**：工具描述变长，可能增加 prompt tokens。

**验证**：

- golden request：Task tool description 包含核心约束。
- 行为测试：给模型一个多子任务需求，检查 Task prompt 是否包含文件路径/已知事实/明确输出格式。

### 3. 实现 tool_result persistence preview

**改动**：

替代简单 `truncateToolResult`：

- 小输出原样；
- 大输出写 `.claude/sessions/<id>/tool-results/<tool_use_id>.txt/json`；
- tool_result 中保留 preview、原始大小、路径、读取完整输出的方法；
- 空输出标准化为 `(<tool> completed with no output)`。

**预期影响**：减少关键日志/搜索结果被截断导致的错判。

**风险**：

- 文件路径暴露边界；
- session 生命周期清理；
- sandbox/read permission 一致性。

**验证**：

- 单测大输出、空输出、JSON 输出。
- 真实测试日志任务：截断前后模型是否能继续定位失败行。

### 4. 强化 Todo/Plan tool_result 与 schema

**改动**：

- Todo schema 增加 `activeForm` 或兼容支持；
- TodoWrite 成功结果改成行为性提醒，而不是只说保存路径；
- 全部 completed 时可提醒验证/总结；
- PlanMode 工具结果包含 plan path、active state、下一步规则。

**预期影响**：低成本增强执行节奏。

**风险**：旧 todo 文件兼容。

**验证**：

- TodoWrite 后下一轮模型继续更新 in_progress，而不是忘记清单。
- PlanMode 未退出时不进入文件修改。

### 5. Skill blocking requirement + listing budget

**改动**：

- Go skills catalog 文案加入 “匹配 skill 时必须先调用 Skill tool”；
- listing 按预算截断，避免过长 metadata；
- Skill 调用后把 loaded skill content 作为可 compact-preserve 的显式状态。

**预期影响**：提升技能命中率，减少“知道有 skill 但不用”。

**风险**：误调用 skill。

**验证**：

- 有明显 skill 命中的任务，第一轮必须 Skill。
- 无关任务不应被 skill 噪声干扰。

## 需要新增的诊断工具

为了以后不靠猜，建议增加：

1. `--dump-prompt-json <path>`：保存每轮发送给模型的 request JSON，脱敏 API key。
2. `--dump-context-manifest --verbose`：现有 manifest 只记录计数，应增加可选本地调试模式记录 section 名称、attachment 类型、tool_result 替换情况。
3. `go-claude compare-prompt --against <orig-dump>`：对比 system blocks、message roles、tool defs、tool_result size、attachments。
4. TUI debug panel：显示当前 prompt mode、permission mode、active todo、active plan、loaded skills、compact state。
5. Task/Agent trace summary：主线程可看到每个 sub-agent 的输入 prompt、返回摘要、输出文件、是否已总结给用户。

## 精准验证方案

### A. 同入口同任务 prompt dump 对照

步骤：

1. 原版 Claude Code 与 go-claude 都用 CLI/TUI code mode。
2. 同一 cwd、同一模型、同一 permission mode、清空 compact/resume 状态。
3. 运行同一任务，只跑前 3 个 assistant turns。
4. 保存每轮 request JSON。
5. 对比：
   - system prompt section 列表；
   - user-context messages；
   - attachment 类型；
   - tool definitions；
   - tool_use input；
   - tool_result content length 与 replacement；
   - token usage 与 cache read/create。

判定：

- 如果第一轮 request 就少了项目规则、git、skills，则入口/prompt profile 是主因。
- 如果第一轮相近但第二/三轮差异扩大，重点看 tool_result、todo/plan/task attachments。
- 如果 Task prompt 明显更空，重点看 Agent/Task 契约。

### B. 人工构造最小复现任务

构造一个任务，强制覆盖关键维度：

```text
请在本仓库中只读分析：
1. 找出 TodoWrite 的 schema 和 tool_result；
2. 找出 Task 工具 prompt；
3. 用一个子 agent 独立审阅 tool_result 截断风险；
4. 最后给出证据表，不要修改文件。
```

观察点：

- 是否先 TodoWrite；
- Task prompt 是否包含足够上下文；
- 子 agent 返回后主 agent 是否总结；
- 是否丢失 todo/plan；
- 大输出是否被截断；
- TUI 是否清楚显示工具输入。

### C. 大输出截断复现

构造 `rg` 或测试命令输出超过 20KB，并把关键证据放在尾部。

判定：

- go-claude 如果只看到前 20KB，会错过尾部证据；
- 原版如果使用 persisted preview/path，模型可通过路径追查完整输出。

## 结论

当前最准确的结论是：

**导致表现差距的首要因素是上下文运行时架构，而不是单纯 prompt 文本。**
具体落点是：原版每轮通过 attachments、tool result budget、Task/Agent prompt contract、Todo/Plan reminders、relevant memory、skill loading 和 compact/collapse 管理，把“当前工作状态”持续放回模型上下文；go-claude 当前这些能力分散存在，但没有形成同等级的 per-turn context orchestration。

如果只能先做 1 件事，做 **动态 attachments/reinjection layer**。
如果先做 3 件事，顺序是：

1. 动态 attachments/reinjection layer；
2. Agent/Task prompt contract parity；
3. tool_result persistence preview。

这三项比继续微调普通 system prompt 文案 ROI 更高，因为它们直接改变模型每轮可见信息和可执行状态。
