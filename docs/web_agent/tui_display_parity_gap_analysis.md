# Web Agent vs TUI Display Parity Gap Analysis

本文档梳理 `/webui/agent` 相对 TUI 的显示能力差距，目标是为后续“让 Web Agent 输出效果接近 TUI”提供实现边界、优先级和验收标准。本文只做现状对比和方案拆解，不代表已经实现。

审查时间：2026-07-01
最近复核：2026-07-06，TUI workbench baseline 已完成状态栏、工具卡片、todo/sub-agent 进度、权限摘要、session/compact/checkpoint 可见性、真实 TUI 验收、自然 scrollback 工具 transcript 修复、80 列状态栏分段换行和 markdown ANSI 空白清理。`docs/todo.md` TODO-049 已标记 Web Agent/TUI P1 parity 完成；本文中剩余 Web Agent P2 项是浏览器产品能力扩展，不再阻塞本轮 TUI terminal workbench polish。

审查范围：

- TUI：`internal/tui/app.go`、`internal/cli/cli.go`、`internal/query/query.go`
- Web Agent 前端：`web/src/components/WebAgentPage.tsx`、`web/src/lib/api.ts`、`web/src/lib/types.ts`
- Web Agent 后端：`internal/server/server.go`、`internal/agenttasks/agenttasks.go`、`internal/agentruntime/runtime.go`
- 相关文档：`docs/web_agent/*`

## 总体结论

Web Agent 已经有工作台雏形：左侧 workspace/session，中心对话，右侧 progress/files/permissions/trace/usage，composer runtime，SSE 事件刷新，assistant 流式打字效果。但它还没有和 TUI 共用一套完整的“显示事件语义”。

当前代码事实：

- 前端 `web/src/components/WebAgentPage.tsx` 默认 `promptMode` 已改为 `code`；新建 session modal 独立默认 Code，新建 session 和 continuation 会把当前 `prompt_mode` 写入 task metadata。
- 后端 `internal/server/server.go` 的 `normalizeAgentTaskPromptMode` 已调整为 `code/chat` 显式保留、缺省回退 `code`、非法值回退 `chat`。
- TUI slash command provider 已抽到 `internal/slashcommands`；CLI/TUI 和 Web Agent API 共用同一套 builtin/skill/plugin skill/custom command 列表。Web Agent 已新增 `/agent/slash-commands` 和 composer slash autocomplete。
- Web Agent Progress tab 仍主要平铺 `agent_task_events`，没有 tool/sub-agent/file-change 专用 view model。
- Web Agent Progress tab 已基于 `tool_call/tool_result` 事件归并 Tool activity cards；server 会把 Web Agent runner 的 `result.ToolCalls` 补写为 task events，sub-agent/file-change 专用 view model 尚未完成。
- Web Agent completed payload 已有 usage/cache/context/duration 字段，但 Usage tab 只展示其中一部分。

当前主要差距不是单纯 CSS，而是数据协议和 view model 不一致：

1. TUI 直接消费 `query.Session.RunWithCallbacks` 的 thinking/tool/sub-agent/usage 事件；Web Agent 主要消费持久化后的 `tenant_agent_task_events`，很多信息被压扁或丢失。
2. TUI/Web Agent slash 命令列表已共用 `internal/slashcommands.List(cwd, prefix)`；当前只完成 Web 前端候选和补全，尚未完成 Web slash execution router。
3. TUI 工具活动是明确的 `tool_start/tool_result` 状态机；Web Agent 只有通用事件列表和少量 payload 推断，缺少“正在用哪个工具、运行多久、结果摘要、错误”的同构展示。
4. TUI sub-agent 进度有专门 `nested_agent_progress` 渲染规则；Web Agent 后端已有 sub-agent/task event 类型，但前端没有按 TUI 规则归并成 sub-agent 卡片。
5. Web Agent 已有 Files tab，但“编辑了哪些文件”主要从 payload 的 `file/path/filename/insertions/deletions` 猜测；TUI/query 已能记录 `file_change` 到 transcript，但 Web Agent task event 没有结构化 file change 事件。
6. TUI/CLI 默认 `code` prompt mode；Web Agent 当前前端默认 `chat`，只有用户手动选择 Code 后才写入 `metadata_json.prompt_mode=code`。
7. Web Agent 底层 query path 已接入 auto compact，但每次 continuation 是新 `agent_task`，当前 runner 没有把同一 Web Agent session 的历史轮次作为 `InitialMessages` 输入 query，因此不等同于 TUI 基于完整连续 transcript 的自动压缩。

因此建议后续不要只在前端硬写更多条件判断，而是先补齐 Web Agent 的 Agent UI Event 协议，把 TUI 已有显示语义映射到浏览器端。

## TUI 当前显示能力盘点

### 1. 启动和运行上下文

TUI 启动页和 header 展示：

- version、model、provider
- context length
- cwd
- permission mode
- sandbox
- tools summary
- MCP server 数量
- resume/goal 状态

Web Agent 现状：

- 中心 header 显示 cwd 和 session title。
- composer 显示 permission mode、workspace、prompt mode、state、context 百分比、cache hit、model、effort。
- 右侧 Progress/Usage 显示部分 status/model/trace/tokens/duration。

差距：

- Web Agent 没有统一的 top runtime bar 展示 provider、sandbox、tool summary、MCP 数量、goal/resume 状态。
- permission mode 是前端选择状态和 task metadata，不等价于 TUI 当前 `WelcomeInfo.PermissionMode` 的实时配置 reload 结果。
- Web Agent 的 context/cache 依赖 task result/event payload，运行中实时性弱于 TUI Usage panel。

优先级：P1。

### 2. Prompt Mode 默认值

TUI 行为：

- CLI/TUI/headless print 默认 `code` prompt mode。
- `code` mode 会加载项目规则、git snapshot、code memory、本地 skills catalog、开发工具 guidance。
- `chat` mode 用于 OpenAI-compatible、Mobile、多租户普通助手边界，不默认加载服务端 cwd 的本地代码上下文。

Web Agent 现状：

- 前端 `WebAgentPage` 当前默认 `promptMode = "code"`。
- New Session modal 和 composer 都能选择 `Chat / Code`。
- 新建 session 和 continuation 创建 task 时会写入 `metadata_json.prompt_mode`。
- 后端 `runAgentTaskMessage` 从 task metadata 读取 `prompt_mode`，`normalizeAgentTaskPromptMode` 显式接受 `code/chat`，缺省回退 `code`，非法值回退 `chat`。

差距：

- Web Agent 是本地 agent 工作台，用户预期更接近 TUI；P0-1 已把默认值改成 `code`，避免默认不加载本地代码上下文、项目规则、git context、本地 skills catalog。
- 历史任务如果缺少 `prompt_mode`，后端现在按 `code` 跑，和 TUI 默认对齐。
- 但非法值不能盲目回退 `code`，否则脏 metadata 或外部 API 输入可能意外打开本地代码上下文。

优先级：P0。

状态：已完成 P0-1。

建议实现：

1. 前端默认值改为 `code`：
   - `useState<PromptMode>("chat")` 改为 `"code"`。
   - New Session modal 默认显示 Code。
   - continuation 继续沿用当前 composer 的 prompt mode 写入 task metadata。
2. 后端 fallback 规则调整为：
   - `prompt_mode:"code"` -> `code`
   - `prompt_mode:"chat"` -> `chat`
   - 缺省 `""` -> `code`
   - 非法值 -> `chat`
3. 右侧 Progress 保持显示当前 task prompt mode，方便解释为什么 skills/context 是否可见。

验收：

- 新建 Web Agent session 未手动选择时，task metadata 写入 `prompt_mode:"code"`。
- Web Agent continuation 默认仍为 code。
- 显式选择 Chat 后，后端 `QueryRequest.PromptMode` 为 `chat`。
- 历史缺省 prompt mode task 后端按 `code` 跑。
- 非法 prompt mode 仍回退 `chat`，避免安全边界扩大。

P0-1 真实验证：

- 浏览器打开 `http://127.0.0.1:18091/webui/agent?token=test-token`，New Session modal 默认 active Code。
- 真实创建 `P0-1 Verify Default Code 1782908374909`，后端 `/tenant/agent-tasks` 返回 `metadata_json.prompt_mode:"code"`。
- 真实创建 `P0-1 Verify Explicit Chat 1782908482885`，modal 点击 Chat 后创建，composer select 为 `chat`，右侧 Progress 显示 `MODE Chat`，后端 `/tenant/agent-tasks` 返回 `metadata_json.prompt_mode:"chat"`。

### 3. Slash 命令待选列表和前缀匹配

TUI 行为：

- 输入框内容以 `/` 开头且没有空格时触发联想。
- 前缀匹配命令名，最多显示 8 条。
- 支持上下移动、Tab complete、Enter run。
- 展示 name、description、source。
- 来源包括 builtin、user-invocable skill、plugin skill、legacy `.claude/commands/*.md`。
- builtin 包括 `/help`、`/clear`、`/status`、`/tools`、`/sessions`、`/agent-tasks`、`/mcp`、`/skills`、`/plugins`、`/model`、`/permissions`、`/hooks`、`/usage`、`/diff`、`/branch`、`/review`、`/loop`、`/goal`、`/compact`、`/recap`、`/rewind`、`/checkpoint`、`/ps`、`/logs`、`/attach`、`/exit`、`/quit`。

Web Agent 现状：

- Composer 在输入纯 slash 前缀时会调用 `/agent/slash-commands?cwd=&prefix=&limit=8`。
- Slash suggestions 展示 name、description、source。
- 支持上下键选择，Tab 补全；Enter 对非精确前缀先补全，精确命令仍按普通 prompt 发送。
- 没有 slash 命令执行分流；`/diff`、`/branch`、`/usage` 等精确命令仍会作为普通 prompt 发给 agent。

差距：

- 已完成 `/agent/slash-commands?cwd=&prefix=` 和前端 dropdown。
- 已复用 TUI 的 source/rank/description 排序规则。
- 缺少 command execution router。对于展示型命令，例如 `/diff`、`/branch`、`/usage`，Web Agent 应走本地 API/工具结果展示；对于 skill/custom command，需要复用 CLI 的 `resolveSlashCommandPrompt` 逻辑或提取公共包。

优先级：P0。

状态：已完成 P0-2 的 list/complete；完整执行路由留到 P2。

建议实现：

1. 将 `listTUISlashCommands` 从 `internal/cli` 提取为可复用包，避免 server 反向依赖 CLI。
2. 新增只读 API：`GET /agent/slash-commands?cwd=<abs>&prefix=<prefix>`。
3. 前端 composer 增加 slash state：active、loading、items、selected、error。
4. 第一阶段只做待选和补全，不直接执行所有 slash 命令；`/help`、`/diff`、`/branch`、`/usage` 可优先接入。
5. 第二阶段再统一 slash execution contract。

验收：

- 输入 `/` 显示 builtin + skill + custom command。
- 输入 `/re` 只显示 `/review`、`/recap`、`/rewind` 等前缀匹配项。
- Tab 补全选中项，Enter 在未补全时补全，在已补全且有参数时按命令规则执行。
- cwd 切换后 command list 随 workspace 变化。

P0-2 真实验证：

- curl `/agent/slash-commands?cwd=/path/to/golang-cc&prefix=re&limit=8` 返回 `recap/resume/review/rewind` builtin，以及匹配前缀的 user skill。
- 浏览器 composer 输入 `/` 能展示候选；输入 `/re` 展示 `recap/resume/review/rewind/remotion-best-practices/retro`；ArrowDown + Tab 补全选中项为 `/resume `。

### 4. Thinking / reasoning 显示

TUI 行为：

- `StreamThinking` 显示为 thinking 消息。
- 运行状态切换为 `Go Claude is reasoning...`。
- thinking 文本会增量拼接。

Web Agent 现状：

- 有 `ThinkingPlaceholder`，显示 sending/connecting/thinking/running command/reading context。
- 不展示真实 thinking delta。
- 后端 Web Agent task event 目前主要写 `text_delta`，没有把 `thinking_delta` 持久化到 `tenant_agent_task_events`。

差距：

- Web Agent 的 thinking 是推断态，不是模型真实 reasoning stream。
- 没有 thinking 内容、signature、connector_text 等区分。

优先级：P1。

建议实现：

- 让 Web Agent runner 使用和 TUI 类似的 `RunWithCallbacks` 或扩展 `StreamQueryFunc` sink，写入 `thinking_delta` 事件。
- 前端把 thinking 作为 assistant 临时状态展示，终态可折叠或隐藏。

### 5. 工具调用活动

TUI 行为：

- `StreamToolStart` 进入 Tools panel，显示最多 5 个 running tool。
- 每条工具显示 tool name、running elapsed、detail。
- `StreamToolResult` 更新 done/error、耗时、结果摘要。
- 完成后工具活动归档到 assistant 消息下，历史消息仍能看到工具摘要。

Web Agent 现状：

- 右侧 Progress 只列 event type 和时间。
- Activity strip 只按 payload 里的 tool/action 文本推断 commands/files。
- Trace tab 能看到事件，但不是工具活动视图。
- `tenant_agent_task_events` 支持 `tool_call`、`tool_result` 类型；sub-agent runtime 也会追加工具事件。

差距：

- 前端没有 `ToolActivityViewModel`：tool_id、tool_name、status、started_at、finished_at、duration_ms、detail、is_error。
- 后端 Web Agent runner 对主 agent 工具调用是否完整落 `tool_call/tool_result`，取决于当前 query path；TUI 通过 callbacks 有强保证。
- 工具耗时当前主要由前端用事件时间差推断，缺少明确 `duration_ms`。
- 工具活动没有归档到 assistant 消息，也没有在中心对话下展示简洁摘要。

优先级：P0。

建议实现：

1. Web Agent runner 走 richer callbacks，把 tool start/result 写入 task event。
2. event payload 标准化：
   - `tool_id`
   - `tool_name`
   - `input_preview`
   - `output_preview`
   - `is_error`
   - `started_at`
   - `duration_ms`
3. 前端新增 `buildToolActivities(events)`，复刻 TUI 的 running/done/error 状态机。
4. 右侧 Progress tab 显示工具卡片，中心 assistant 消息显示折叠摘要。

验收：

- Bash/Read/Edit/Write/Task 等工具开始后 1 秒内出现在 Web Agent。
- running 状态显示动态 elapsed。
- tool_result 后显示 done/error 和耗时。
- 长输出只展示摘要，详情进 drawer/trace。

### 6. 编辑了哪些文件

TUI/Query 现状：

- `tools.Context.FileChange` 会在 Write/Edit/MultiEdit 后回调。
- `query.Session.recordFileChange` 把 file change 写到本地 transcript，类型为 `file_change`。
- TUI 主界面没有专门的“本轮编辑文件列表”常驻面板，常通过工具活动、assistant 总结、`/diff` 查看。

Web Agent 现状：

- 有 Files tab。
- `summarizeActivity` 从事件 payload 的 `file/path/filename`、`insertions/deletions/line_delta` 推断文件。
- 当前 task completed result 有 `changed_files` 时会显示数量，否则退化为 files set size。

差距：

- Web Agent 缺少结构化 `file_change` task event，所以 files tab 不可靠。
- 没有区分 read files 与 edited files。
- 没有 before/after existence、insertions/deletions、diff hunk preview、tool source。
- 没有本轮 touched files 与整个 conversation files 的分层。

优先级：P0。

建议实现：

1. 在 Web Agent query path 中把 `FileChange` 写入 `tenant_agent_task_events`，event type 建议为 `file_change`。
2. payload 字段：
   - `path`
   - `operation`: `create|edit|delete`
   - `before_exists`
   - `after_exists`
   - `insertions`
   - `deletions`
   - `tool_name`
   - `tool_id`
   - `diff_preview`
3. 前端 Files tab 拆成 Edited / Read / Commands 三组。
4. Center conversation 在每轮 assistant 完成后显示 changed files summary card。

验收：

- Write 新文件显示 create。
- Edit/MultiEdit 显示 edit 和 line delta。
- Files tab 不再把普通 Read 文件误当 edited file。
- `/diff` 或 diff drawer 能从 changed file card 进入。

### 7. Sub-agent 执行进度显示规则

TUI 行为：

- `nested_agent_progress` 按 task_id 聚合。
- 支持事件：`started`、`turn_start`、`text_delta`、`message`、`tool_call`、`tool_result`、`usage`、`cache_state`、`completed`、`failed`、`cancelled`。
- 卡片保留 agent、model、description、turn、messages、last tool、tokens/cache、duration、session_id。
- 排序规则：running/active 优先，再按 updated_at，再按 task_id。
- 默认显示 3-5 个，窗口大显示 5，展开显示 10。
- 完成后归档到 assistant 消息下。

Web Agent 现状：

- 后端 `agenttasks` 定义了上述大部分 event type。
- `agentruntime.Runtime.emitEvent` 可把 sub-agent progress 写入 store，并通过 `NestedAgentProgress` 回调给 TUI。
- 前端右侧只是平铺 event list，没有 sub-agent 聚合视图。
- Activity strip 没有 sub-agent 数量、当前子任务、失败/完成统计。

差距：

- 缺少 `buildSubAgentProgress(events)`。
- 缺少 TUI 的排序、折叠、展开规则。
- 缺少 sub-agent 与 parent tool call 的关联显示。
- 缺少 batch mode summary：total/completed/running/failed/retries/max_concurrency/schedule_strategy/duration。

优先级：P0。

建议实现：

1. 前端按 TUI `updateAgentProgress` 逻辑移植成 TS view model。
2. 右侧 Progress tab 顶部新增 Sub-agents section。
3. 中心 conversation 在 assistant live area 下显示 compact sub-agent rows。
4. 批量 Task 返回 batch JSON 时解析 summary，展示 batch progress。

验收：

- Task 工具启动后显示 sub-agent name/model/description。
- 子 agent 工具调用时显示 last tool。
- completed/failed/cancelled 显示 duration 和最终状态。
- 多 sub-agent 并发时排序稳定，active 优先。

### 8. 步骤执行耗时

TUI 行为：

- input mode hint 运行中显示 turn elapsed。
- response meta 显示 `Responded in Xms/Xs` 和中文时间。
- tool activity 显示 running/done/error elapsed。
- sub-agent completed/failed/cancelled 显示 duration。
- Usage tab 显示 speed/tier/geo 等性能信息。

Web Agent 现状：

- Activity strip 显示 task elapsed，即 task started_at 到 finished_at/now。
- Usage tab 显示 total duration。
- Progress list 显示 event created_at，但不显示事件间耗时。
- Tool/sub-agent step 没有独立 elapsed 规则。

差距：

- 缺少 step duration view model。
- 缺少 per-tool duration。
- 缺少 per-turn duration。
- 缺少 TTFT/first token latency 展示，虽然 query stream event 类型里已有 `ttftMs` 字段。
- 没有 response meta 的 `Responded in ... · time=... · model=... · turns=...` 同构展示。

优先级：P0/P1。P0 先做 tool/sub-agent/task duration；P1 做 TTFT/turn-level 性能。

建议实现：

- 标准化 task event 时间字段，前端统一计算：
  - task elapsed: `task.started_at -> task.finished_at|now`
  - tool elapsed: `tool_call.created_at -> tool_result.created_at` 或 payload `duration_ms`
  - sub-agent elapsed: payload `duration_ms`，缺失时用 started/completed event 时间差
  - response elapsed: completed payload `duration_ms`
- 在 assistant message meta 中显示 TUI 风格 response meta。

### 9. Usage / token / cache / context

TUI 行为：

- Usage panel 显示 model、turns/max_turns、tokens in/out、ctx 百分比、cache create/read/hit、tools/tool errors/last tool、max_tokens、stop reason、speed/tier/geo、session、cwd/context hint。
- response meta 也显示 tokens、cache、tools、stop reason。

Web Agent 现状：

- composer 显示 context 百分比和 cache hit。
- Usage tab 显示 total/input/output/tool calls/duration/run counts。
- completed result 写入 input/output/cache/context/tool_calls/duration/tier/geo/speed。

差距：

- Usage tab 没完整显示 cache create/read/hit、stop reason、turns、max turns、max tokens、last tool、tool errors、session id、tier/geo/speed。
- Center assistant message meta 没显示 response meta。
- 运行中 usage event 没有像 TUI 一样实时更新。

优先级：P1。

建议实现：

- 前端复刻 `usagePanel` 字段，合并 task metadata/result/completed/usage events。
- Usage tab 改成 TUI 等价字段 + Web-specific run counts。
- assistant message meta 增加 response duration/model/turns/tokens/cache/tools/stop。

### 10. Permission prompt 和权限状态

TUI 行为：

- pending permission 会替换 live transcript，显示 tool/request/reason/rule/source/input。
- 选项包括 allow once、allow session、allow project/local/global、deny 等。
- mode hint 常驻显示 permissions，并对 bypass/allow 高亮风险。
- shift+tab 可切换 permission mode。

Web Agent 现状：

- composer 有 permission mode select。
- right Permissions tab 显示 mode、pending 数、sandbox 文案。
- Web Agent 页面没有真实 pending permission resolution UI。

差距：

- 缺少 permission request SSE/event。
- 缺少 resolve endpoint 的前端接入。
- 缺少 tool input/request/rule 展示和 allow/deny 决策。
- 缺少与后端 query permission prompt 的同步；现在更多是 task metadata 配置。

优先级：P1，涉及安全边界，必须测试充分。

### 11. Resume / Rewind / Checkpoint

TUI 行为：

- `/resume` 有 picker。
- `/rewind`/`/checkpoint` 有 candidate picker。
- resume 后触发 `session_resume`，清空 UI 状态并 reload welcome。

Web Agent 现状：

- 左侧 session/conversation 列表可选历史 Web Agent conversation。
- continuation task 支持继续对话。
- 没有 TUI 的 rewind candidate picker。

差距：

- Web Agent session 模型是 tenant session + agent task chain，不等同于本地 transcript resume/rewind。
- 没有 conversation-only/files-only rewind。
- 没有 checkpoint 视图。

优先级：P2。先完成实时显示 parity，再做复杂历史控制。

### 12. Attachments / clipboard image

TUI 行为：

- attachment tray 显示已附加图片/文件。
- 支持 clipboard image detector/importer。
- mode hint 显示 image clipboard 状态和快捷键。

Web Agent 现状：

- 当前 Web Agent composer 没看到附件入口。

差距：

- 缺少附件上传/展示/移除。
- 缺少与 query attachments 的对接。

优先级：P2。

### 13. Background / loop / recap / goal 状态

TUI 行为：

- mode hint 和 slash command 支持 `/loop`、`/goal`、`/recap`、`/ps`、`/logs`、`/attach`。
- background watcher 可显示后台更新。
- recap 可插入 transcript。

Web Agent 现状：

- 左侧 quick action 有 Refresh/Permissions/Home；没有 background/goal/recap 专区。
- 右侧 trace/usage 不覆盖 loop/goal/recap。

差距：

- 缺少 background job panel。
- 缺少 goal 状态条。
- 缺少 recap 展示入口。

优先级：P2。

## Web Agent 已具备但需要收敛的能力

这些不是空白，但需要改成更接近 TUI 的规则：

| 能力 | 当前 Web Agent | 需要收敛到 |
| --- | --- | --- |
| 对话流式 | `text_delta` + typewriter | TUI text/thinking/tool/sub-agent 多通道 live transcript |
| activity strip | task elapsed/files/commands/current file/stream state | tool/sub-agent/permission 优先的实时状态 |
| files tab | payload 推断 files | 结构化 file_change/read_file/command events |
| usage | task result + usage aggregate | TUI usagePanel 字段 |
| trace | event list | event list 保留，但 progress tab 用语义化 cards |
| composer runtime | permission/workspace/prompt/context/cache/model/effort | 增加 slash、pending permission、running elapsed、risk highlight |

## 建议的目标架构

### A. 统一 Agent UI Event

后续建议新增一层 Web Agent UI event mapper，不让 React 直接理解所有原始 payload。

后端可以继续持久化 `tenant_agent_task_events`，但需要约定稳定 event type：

- `message`
- `text_delta`
- `thinking_delta`
- `tool_call`
- `tool_result`
- `file_change`
- `permission_request`
- `permission_decision`
- `nested_agent_progress`
- `usage`
- `cache_state`
- `completed`
- `failed`
- `cancelled`

前端将这些事件规整为：

- `ConversationMessage[]`
- `ToolActivity[]`
- `SubAgentProgress[]`
- `FileChangeSummary[]`
- `PermissionPrompt | null`
- `UsagePanel`
- `RunTiming`

### B. 复用 TUI 逻辑，而不是复制散落规则

可提取公共包：

- slash commands：从 `internal/cli` 提取到 `internal/slash` 或 `internal/commands/slash`。
- TUI progress formatting 的数据规则：Go 侧可定义 JSON schema，TS 侧实现 view mapper。
- file change stats：Go 侧统一计算 insertions/deletions，避免前端猜。

### C. Web Agent 连续会话上下文和 Auto Compact

当前 Web Agent 的 auto compact 状态要分清两层：

- 底层 query 支持：Web Agent task 最终走 `runServerQuery -> newQuerySession -> query.New`，`newQuerySession` 会传入 `compact.ConfigFromSettings(cfg.Settings, model)`，因此单次 query session 内部的多 turn/tool loop 可以触发 `MaybeCompact`。
- 连续 Web Agent session 不完整：Web Agent continuation 当前会创建新 `agent_task`，runner 使用 `SessionKey: web-agent-task-<taskID>`，并未把同一 Web Agent session 的前序 user/assistant 轮次作为 `InitialMessages` 喂给 query。因此它不是 TUI 那种基于同一个 transcript 的跨用户轮次完整自动压缩。

P1-1 事实复核：

- `query.Options.InitialMessages` 已存在，query 层会在本轮 user prompt 前追加这些历史消息。
- server 内部 `QueryRequest` 之前没有携带 `InitialMessages` 的字段，`runServerQuery` 也没有把 Web Agent 历史传给 `newQuerySession`。
- `tenant_agent_tasks` 返回结构已保存 `parent_session_id`、`status`、`result_json`，用户输入可从对应 task 的 `message` event 读取；两者足够先恢复同一 Web Agent session 内已完成轮次的 user/assistant 文本历史。
- 当前 task event 还不能完整还原 Anthropic `tool_use/tool_result` transcript，因此 P1-1 不伪造工具消息；工具级 transcript 和 compact summary 恢复留给 P1-2/P1 后续项。

目标：

- 同一个 Web Agent session 内，多轮用户输入应形成一条可恢复、可压缩的 conversation context。
- auto compact 触发后，压缩摘要应成为后续轮次的恢复边界，行为接近 TUI 的 `compact_summary` transcript entry。
- 不同 Web Agent session、不同 tenant/user、不同 cwd 不得串上下文。

建议实现：

1. 定义 Web Agent conversation context builder：
   - 按 `parent_session_id` / `web_agent_session_id` 找同一 Web Agent session 下的 task chain。
   - 按 `run_index`、`started_at`、task id 排序。
   - 从 task events/result 还原历史：
     - `message` from `webui` / `user` -> user message
     - `text_delta` 或 completed result `response` -> assistant message
     - 后续接入 tool events 后，再还原 `tool_use` / `tool_result`
     - `compact_summary` -> 替换其之前的历史，只保留 summary 后的轮次
2. 扩展 server 内部 `QueryRequest`：
   - 增加不暴露到外部 JSON 的 `InitialMessages []anthropic.MessageParam` 或等价内部字段。
   - `runAgentTaskMessage` 在调用 `StreamQueryFunc` 前构造同 session 历史，并放进 request。
   - `runServerQuery` 将该历史传给 `newQuerySession`。
3. 持久化 Web Agent compact summary：
   - 新增或约定 task event type：`compact_summary`。
   - payload 保存 `summary`、`trigger_tokens`、`token_after`、`compacted_messages`、`preserved_messages`、`model`、`trace_id`。
   - 下一轮 context builder 看到最新 `compact_summary` 后，从该 summary 开始拼上下文。
4. 保留现有配置入口：
   - 仍使用 `autoCompact.enabled`、`defaultThresholdRatio`、`preserveRecentRounds`、`modelContext`、`modelThresholdRatio`。
   - 不建议 Web Agent 强制开启 auto compact；默认是否开启继续由项目配置控制。
5. 可观测性：
   - Progress/Trace 显示 compact event。
   - Usage tab 显示 compact 前后 token 变化。
   - assistant meta 或右侧 activity 显示“已压缩上下文”摘要入口。

P1-1 实施边界：

1. server 内部 `QueryRequest` 增加 `InitialMessages []anthropic.MessageParam`，不暴露到外部 JSON。
2. `runAgentTaskMessage` 调 query 前按 `parent_session_id` 从当前 tenant/user 可见的 `ListAgentTasks` 结果中筛选历史 task：
   - 只取同一 `parent_session_id`。
   - 排除当前 task。
   - 只取 `status=completed`。
   - 只取同时具备 `message` event 用户输入和 completed result `response` 的轮次。
   - 最多保留最近 12 个已完成 task，按 `started_at/task_id` 恢复为旧到新。
3. `runServerQuery` 把 `req.InitialMessages` 传给 `newQuerySession`。
4. completed payload 写入 `initial_messages` 数量，方便真实浏览器/API 验证。

验收：

- 同一 Web Agent session 第二轮请求能收到第一轮 user/assistant history。
- 不同 session 的历史不会混入。
- P1-1 完成后，第二轮 completed event/result 的 `initial_messages` 应为上一轮 user/assistant 两条消息；第三轮至少能看到前两轮四条历史消息。
- 配置低阈值 autoCompact 后，多轮 Web Agent 对话会产生 `compact_summary` event。
- compact 后下一轮只携带 compact summary + preserved recent rounds + 当前用户输入。
- code mode 下自动压缩仍保留项目事实、文件路径、命令、工具调用、用户约束。

### D. 分阶段落地

P0：让用户“看得见 agent 在干嘛”

1. Web Agent 默认 code mode，并保留显式 Chat 选择。
2. Slash 待选列表 API + 前端 autocomplete。
3. Tool activity cards：start/result/elapsed/error/detail。
4. File change event + Files tab edited/read 分组。
5. Sub-agent progress 聚合卡片。
6. Duration：task/tool/sub-agent/response meta。

建议实施顺序和真机验收节奏：

| 阶段 | 内容 | 为什么先做 | 每阶段真实操作验收 |
| --- | --- | --- | --- |
| P0-1 | 默认 Code mode + 后端缺省 fallback 为 code、非法值仍回 chat | 影响面小，是 Web Agent 作为本地 coding workbench 的基础语义 | 已完成：浏览器确认 modal 默认 Code；真实创建默认 Code task id 40 写 `prompt_mode:"code"`；真实创建显式 Chat task id 41 写 `prompt_mode:"chat"`，composer 和 Progress 均显示 Chat |
| P0-2 | Slash command list API + 前端 autocomplete，只做 list/complete，不做完整执行 | 独立、可视化强，能先补齐 TUI 输入体验 | 已完成：curl 验证 `/re` 返回 builtin/user skill；浏览器输入 `/` 和 `/re` 展示候选，ArrowDown + Tab 补全为 `/resume ` |
| P0-3 | Tool activity view model + Progress cards | 用户最关心“agent 在干嘛”，但需要稳定 event mapping | 已完成：真实浏览器创建 `P0-3 Tool Bridge 1782910474581`，触发 LS tool，task id 45 落库 `tool_call/tool_result`，Progress 显示 `TOOL ACTIVITY`、`LS done` 和结果预览 |
| P0-4 | File change event + Files tab Edited/Read 分组 | 防止继续靠 payload 猜文件，减少误判 | 真实创建/编辑文件，确认 Files tab 只把写操作列为 Edited，Read 另列 |
| P0-5 | Sub-agent progress 聚合卡片 | 多 agent 任务可观测性，依赖 event 聚合规则 | 真实触发 Task/sub-agent，确认子任务 running/completed/duration/last tool |
| P0-6 | Duration/response meta | 收口体验，补齐 TUI meta 信息 | 完成任务后确认中心消息和右侧显示 response/tool/sub-agent/task duration |

P1：让体验接近 TUI 细节

1. Web Agent conversation context builder，让同 session 跨用户轮次进入 query context。P1-1 已完成：先恢复同 session 已完成 task 的 user/assistant 文本历史。
2. Web Agent `compact_summary` task event 和下一轮恢复。P1-2 已完成：query auto compact 成功后通过 runner callback 写入 `compact_summary` event；下一轮 context builder 会以同 session 最新 `compact_summary` 作为恢复边界，丢弃其之前的 user/assistant 轮次，仅注入 compact summary + 后续已完成轮次。
3. Thinking delta。P1-3 已完成：Web Agent runner 使用 `RunWithCallbacks` 持久化 `thinking_delta`，前端 conversation timeline 可显示 thinking 内容。
4. Usage panel 完整字段。P1-4 已完成：Usage tab 从 task result 和 `usage/message_stop` event 合并 input/output/total、cache creation/read、ephemeral 5m/1h、context length/percent、service tier、inference geo、speed、initial messages、stop reason、tool calls 和 duration。
5. Permission request/resolve。P1-5 已完成：runner permission prompt 写入 `permission_request` 并阻塞等待，新增 `PATCH /tenant/agent-tasks/{id}/permissions/{request_id}` 写入 `permission_resolved` 并唤醒 query；前端 Permissions tab 显示 pending/resolved 请求并真实调用 resolve API。
6. Assistant message meta 和 archived tool/sub-agent summaries。P1-6 已完成当前 P1 范围：`message_stop` event 持久化 stop reason/usage，completed payload 保留 duration、initial messages、tool call 数；前端 assistant/system timeline 显示 thinking/compact，Progress/Usage 保留 tool activity 和 message meta。更完整的 archived sub-agent transcript 仍属于 P2/P3 transcript/replay 深化。
7. Runtime top bar：provider/sandbox/tools/MCP/goal。P1-7 已完成：`/webui/agent` header 下方新增 runtime top bar，从 task metadata、started event 和 tool events 汇总 provider、model、sandbox/permission、tools、MCP、goal。

P1 实现证据：

- 后端：`internal/query.Session.RunWithCallbacks` 新增 thinking/tool/usage/message_stop/compact callbacks；`runServerQuery` 将 Web Agent sink 接入 callbacks 和 permission prompt；`agentTaskTextSink` 持久化 `thinking_delta`、`tool_call`、`tool_result`、`usage`、`message_stop`、`compact_summary`、`permission_request`、`permission_resolved`。
- 后端：`webAgentConversationInitialMessages` 已识别最新 `compact_summary` 作为恢复边界，测试 `TestTenantAgentTaskMessageUsesCompactSummaryAsConversationBoundary` 覆盖 compact 前历史不再进入下一轮。
- 前端：`WebAgentPage` 已解析 thinking/compact/permission/usage 事件，新增 permission resolve API helper、runtime top bar、完整 Usage 字段和真实 Permission approve/deny。
- 文档/API：`docs/api_server.md`、Swagger 和 `web/src/lib/generated/api-types.ts` 已包含 permission resolve endpoint。

P2：扩展 TUI 周边能力

1. Rewind/checkpoint picker。
2. Attachments/clipboard image。
3. Background/loop/goal/recap panes。
4. Slash command full execution router。

## 建议验收清单

单元测试：

- `list slash commands by cwd and prefix`
- `buildToolActivities pairs tool_call/tool_result and computes elapsed`
- `buildSubAgentProgress matches TUI status ordering`
- `buildFileChanges separates read vs edited files`
- `usage panel computes cache hit and context percent`

后端测试：

- Web Agent runner emits `tool_call/tool_result` for a fake tool call。
- Web Agent runner emits `file_change` for Write/Edit/MultiEdit。
- Web Agent runner persists thinking/tool/sub-agent/usage events with trace_id。
- Slash command API returns builtin + skill + custom command for temp cwd。

浏览器/E2E：

- 打开 `/webui/agent?token=test-token`，输入 `/re`，看到前缀匹配列表。
- 触发 Read/Edit/Bash 任务，中心和右侧实时显示 tool activity。
- 创建真实 Web Agent session `P1-1 Context 1782911277485`，连续三轮发送消息；后端 task 46/47/48 均 completed，completed result 中 `initial_messages` 分别为 0/2/4，页面第二/三轮能复述第一轮 marker。
- 触发文件修改，Files tab 显示 edited file、operation、line delta。
- 触发 Task sub-agent，Progress tab 显示 sub-agent running/completed/duration。
- 长任务运行中 elapsed 持续增长，完成后 assistant meta 显示 `Responded in ...`。

## 风险和边界

- 不建议只做前端推断增强。payload 来源不稳定会导致“看起来有，但关键场景不准”。
- 权限 prompt 属于安全边界，不能只做 UI 假按钮，必须接真实 query permission prompt 和 resolve path。
- slash execution 涉及大量 CLI 行为，第一阶段应先做 list/autocomplete，再拆 execution contract。
- Web Agent 是浏览器工作台，不需要 1:1 复制 TUI 快捷键；但显示语义、状态和证据应对齐。
