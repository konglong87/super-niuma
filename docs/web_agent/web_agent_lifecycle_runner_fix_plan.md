# Web Agent 生命周期、Runner 接入和全链路修复方案

本文档记录 2026-06-30 真机测试暴露出的 `/webui/agent` 产品链路问题，并给出可执行修复方案。它不是新的视觉稿，而是后续实现、验收和回归测试的基线。

## 结论

当前 `/webui/agent` 已能展示真实 tenant agent task、workspace、event stream 和 composer 状态，但还没有形成完整的 Codex-like agent 生命周期。根本问题是：

1. 新建会话被直接创建成 `running` task，导致用户没有输入时也显示取消按钮和运行态。
2. activity 区无条件展示，所以空会话显示 `0 文件 / 0 命令 / 0 改动`，不符合 Codex 新会话体验。
3. composer 发送的消息只追加为 `message` event，没有触发真实 agent/LLM runner，因此不会自动回复。
4. cancelled/completed task 仍允许追加 message event，生命周期边界不清晰。
5. 页面虽然已比早期版本更接近 Codex 工作台，但空白新会话和执行态之间的 UI 分层还不够清楚。
6. `/webui/` 主页面和 `/webui/agent` 页面之间缺少清晰入口与产品边界说明。
7. `/webui/agent` 需要像主 Web 页面一样支持中文，并且语言切换后不破坏布局。
8. 缺少 tenant storage 配置时，页面直接暴露 `tenant storage is not configured`，没有前置诊断和可操作提示。
9. 消息列表内容超过可视高度后，新发送消息不会自动滚动到可见区域，用户必须手动滚动。
10. 用户消息和 AI 回复没有像 Codex 那样分布在页面两侧，消息身份区分不够清晰。

目标不是把 Web Agent 做成普通 chat，而是做成 Codex 风格的本地 agent 工作台：空白时极简，开始执行后展示进度、文件、命令、权限、trace 和上下文。

## 页面关系和入口策略

### `/webui/`

`/webui/` 是当前通用 Web 控制台入口，承担健康检查、Trace、移动端聊天实验室、配置和其他 Web 工具的聚合入口职责。它应该继续作为总入口存在，不应该被 `/webui/agent` 替代。

### `/webui/agent`

`/webui/agent` 是面向本地代码任务的 Codex-like agent 工作台，关注 workspace、session、run、权限、工具执行、文件改动、SSE 和上下文使用。它的交互模型和信息密度明显不同于普通控制台。

### 是否互通

应该互通，但不是混成一个页面：

- `/webui/` 应提供明显入口进入 `/webui/agent?token=...`。
- `/webui/agent` 应提供返回主 Web 控制台的入口。
- token、语言、tenant context 应尽量透传，避免用户重复配置。
- 两个页面共享认证和基础 API client，但保持路由、页面状态和产品模型独立。

不建议把 `/webui/agent` 嵌进 `/webui/` 的卡片区里，因为 Web Agent 是高频操作工作台，需要完整三栏布局、独立滚动、composer 固定区和右侧详情面板；嵌入式页面会天然变成粗糙 dashboard。

## 中文适配策略

`/webui/agent` 必须支持中文，目标是和主 Web 页面保持一致：

1. 增加语言状态来源：
   - 优先读取主 Web 页面已有语言设置。
   - URL 或 localStorage 可覆盖。
   - 默认跟随浏览器语言，中文环境默认 `zh-CN`。
2. 所有用户可见文案进入 i18n 字典：
   - 左侧 workspace/session/new session。
   - 中间 blank state/composer/activity。
   - 右侧 Progress/Files/Output/Permissions/Trace/Usage/Context。
   - error、empty、loading、cancel、permission 提示。
3. 中英文布局都要真机检查：
   - 中文更短的按钮不能显得偏移。
   - 英文长词不能溢出。
   - composer 底部 permission/model/context/send/cancel 需要左右对齐。
4. 语言切换是 UI 状态，不应影响 task/session/run 数据。

验收标准：

- `/webui/` 可以切换中文后进入 `/webui/agent`，Web Agent 自动显示中文。
- `/webui/agent` 内可以切换语言，刷新后保持。
- 中文模式下截图、按钮、tab、composer、modal 无溢出、无错位。

## 已观察到的真实问题

### 0. tenant storage 未配置导致页面报错

真机现象：

- 页面显示 `API error: tenant storage is not configured`。

根因：

- Web Agent 依赖 tenant storage 读取和写入 agent task/session/event。
- 如果 server 启动时没有配置 MySQL tenant storage，相关 `/tenant/...` API 无法工作。
- 这不是前端渲染问题，而是后端运行环境未满足 Web Agent 的数据依赖。

修复策略：

1. 启动检查：
   - server 启动时在日志里明确输出 tenant storage 是否启用。
   - `/webui/agent` 初始化时调用健康/diagnostics API，识别 tenant storage 状态。
2. UI 提示：
   - 如果 tenant storage 缺失，显示配置诊断页。
   - 提示需要设置 `GOLANG_CLAUDE_CODE_MYSQL_DSN` 并重启 server。
   - 不显示空会话或假数据。
3. 文档同步：
   - 真机使用文档必须写清楚 MySQL、migration、DSN、server 启动命令。
4. 测试：
   - API 测试覆盖未配置 storage 时的错误码和错误信息。
   - 浏览器 E2E 覆盖 storage 缺失的诊断页。

### 1. 新建空会话显示取消按钮

真机现象：

- 用户新建 `测试005`，未输入任何 composer 内容。
- 左侧 session 显示 `running`。
- 右下角显示红色 `取消`。

根因：

- 前端 `handleCreateSession` 调用 `/tenant/agent-tasks` 时传入 `status: "running"`。
- 后端创建 task 后立即追加 `started` event。
- 前端只要 `selectedTask.status === "running"` 就显示取消按钮。

这不是后端异常，而是产品状态设计错误：`新建会话` 和 `开始执行` 被混成了同一个动作。

### 2. 空会话显示 0 文件、0 命令、0 改动

真机现象：

- 中间区域显示 `已读取 0 个文件`、`已运行 0 条命令`、`0 个文件已更改`。
- 顶部 activity strip 也显示类似的 0 值。

根因：

- activity strip 和 activity rows 当前无条件渲染。
- `summarizeActivity` 对没有 tool/file/command 事件的 task 返回 0。

这和 Codex 新会话不同。Codex 空白态先展示 composer 和引导语；文件、命令、耗时等只在执行过程中出现。

### 3. 发送多条消息没有回复

真机证据：

- task #6 的 event 表里已经出现 3 条 `message` event：
  - `{"content":"测试","from_agent":"webui"}`
  - `{"content":"测试","from_agent":"webui"}`
  - `{"content":"测试--回复","from_agent":"webui"}`
- 说明前端发送成功，后端也落库成功。

根因：

- `/tenant/agent-tasks/:id/message` 当前只执行 `AppendAgentTaskEvent`。
- 该接口没有调用 `query`、没有启动 `agentruntime.Runtime`、没有调用模型 provider，也没有生成 assistant message。
- SSE 只流式输出已有 task events，因此没有 runner 输出就没有回复。

结论：不是前端异常，也不是后端报错，而是 Web Agent runner 执行链路尚未接入。

### 4. cancelled task 仍可追加 message

真机现象：

- `测试005` 已经 cancelled 后仍能追加 message event。

根因：

- message handler 只检查 task 是否存在，没有检查 task status。

应该明确：终态 task 不能继续写入普通 message；如果用户想继续，应该创建 continuation run 或显式 reopen/new turn。

### 5. 新消息不会自动滚动到可见区域

真机现象：

- 当消息列表已经超过中间 conversation 区可视高度后，用户在 composer 发送新消息。
- 新消息虽然被追加到列表，但视口没有自动顶上去或滚到底部，用户需要手动滚动才能看到刚发送的内容。

根因：

- conversation 容器有独立滚动，但没有在新 message/text_delta/run event 到达后执行受控滚动。
- 页面没有区分“用户正在查看历史消息”和“用户处于底部跟随模式”。
- composer 固定在底部后，如果滚动容器高度或 padding 计算不稳定，新消息容易被 composer 遮挡或留在视口外。

修复策略：

1. 默认跟随底部：
   - 用户发送消息后立即滚动到最新用户消息。
   - assistant text_delta 到达时，如果用户仍在底部附近，持续跟随最新输出。
2. 尊重用户阅读历史：
   - 如果用户手动向上滚动超过阈值，不强制抢滚。
   - 显示 `Jump to latest` / `回到最新` 浮动按钮。
3. 发送完成定位：
   - 发送用户消息后，应至少让该用户消息完整可见。
   - runner 回复开始后，应让 AI 回复标题或首个 delta 可见。
4. 布局保护：
   - conversation 底部 padding 必须大于 composer 高度。
   - center-only focus mode、左右栏隐藏、移动宽度都要复测滚动行为。

### 6. 用户消息和 AI 回复身份区分不足

真机现象：

- 当前消息呈现更像事件列表或同侧流式日志。
- 用户消息、AI 回复、处理耗时和工具事件混在同一视觉轴上，无法像 Codex 一样一眼识别“我说的”和“agent 回复的”。

目标：

- 用户消息在一侧，AI 回复在另一侧或主回复区明确分层，视觉上接近 Codex 当前体验。
- 用户消息可使用浅灰气泡并靠右，保留时间、复制、编辑等轻量操作。
- AI 回复靠左或占主内容列，以正文、进度摘要、工具结果分块展示。
- lifecycle/progress/tool events 不伪装成聊天气泡，应进入 activity rows 或右侧 tabs。

验收：

- 截图中用户消息和 AI 回复不在同一种样式、同一侧、同一事件列表里混杂。
- 长中文、长英文、代码块、列表在两侧布局中都不溢出。
- mobile 宽度下可以退化为上下分层，但仍保留身份标识和视觉差异。

## 目标生命周期

Web Agent 应定义清晰生命周期，而不是只靠 `running/cancelled/completed` 三个状态撑完整产品。

| 状态 | 含义 | UI 行为 | 后端行为 |
| --- | --- | --- | --- |
| `draft` | 新会话草稿，尚未发送首条指令 | 中间极简，显示 composer；不显示取消；不显示 0 activity | 可暂不落库，或落库为 draft |
| `ready` | 已有会话但当前没有执行中的 run | 显示发送按钮；可继续输入 | 可接受新 run 请求 |
| `queued` | 已提交，等待 runner 调度 | 显示排队/准备中；可取消 | 创建 run 记录，等待执行 |
| `running` | runner 正在执行 | 显示取消、耗时、文件、命令、权限、trace | runner 写入 tool/message/progress events |
| `cancelling` | 用户已发取消请求，后端正在确认 | 禁用重复取消；显示短暂取消中 | controller cancel + task 状态收敛 |
| `cancelled` | run 已取消 | 显示发送/继续，而不是取消 | 终态，禁止继续写入同一 run |
| `completed` | run 完成 | 显示发送/继续 | 终态，可创建 continuation run |
| `failed` | run 失败 | 显示错误和重试/继续 | 终态，记录 error event |

建议区分 `session` 和 `run/task`：

- session 是用户看到的长期会话对象。
- run/task 是一次 agent 执行。
- 一个 session 可以有多次 run：首条指令、后续继续、重试、取消后继续。

如果短期不做 session/run 双层模型，也必须至少做到：空白新会话不创建 running task，发送后才进入 running。

## 产品目标状态

### 空白新会话

应接近 Codex 新会话样式：

- 页面中间保持留白。
- 显示简洁问题或标题，例如 `我们该构建什么？` / `What should the agent do?`
- composer 是主要视觉中心。
- 底部展示 permission、workspace、model、effort、send。
- 不展示：
  - `已读取 0 个文件`
  - `已运行 0 条命令`
  - `0 个文件已更改`
  - `运行中`
  - `取消`
  - `started {"source":"api"...}`

### 执行中

只有真正启动 runner 后才展示：

- 顶部 activity strip：elapsed、files read、commands、current file、delta、context、event stream。
- 中间 activity rows：正在读取、正在编辑、命令执行、权限请求。
- 右侧 Progress/Files/Permissions/Trace/Usage。
- 右下角显示 Cancel。
- 新发送消息和新回复默认自动滚动到可见区域。
- 用户消息和 AI 回复在视觉上分侧或分层，不混成同一种事件列表。

### 终态

completed/cancelled/failed 后：

- Cancel 按钮消失。
- composer 可输入。
- Enter 可发送，Shift+Enter 换行。
- 如果继续执行，应创建新 run，而不是把 message 写入已终止 run。

## 实施方案

### P0：修正生命周期和 UI 误导

目标：用户新建空会话时不再看到 running/cancel/0 activity，也不再误以为已经开始执行。

前端：

1. 新增 `WebAgentDraftSession` 本地状态：
   - `draftTitle`
   - `cwd`
   - `permissionMode`
   - `model`
   - `effort`
2. 点击 New Session 后：
   - 关闭 modal。
   - 进入本地 draft view。
   - 不调用 `/tenant/agent-tasks` 创建 running task，除非用户在 modal 中填了 first prompt 并点击明确的 `Start`。
3. composer 发送时：
   - 如果当前是 draft：调用新的 start/run API 或临时调用 create task。
   - 如果当前是 ready/terminal session：创建 continuation run。
4. 隐藏空会话 activity：
   - `conversationEvents.length === 0 && !hasExecutionEvents` 时不显示 activity strip 和 activity rows。
   - 空白态显示 Codex-like composer-first layout。
5. 按钮逻辑：
   - draft/ready/completed/cancelled/failed：Send。
   - queued/running/cancelling：Cancel。
6. 入口逻辑：
   - `/webui/` 增加 Web Agent 入口。
   - `/webui/agent` 增加返回 `/webui/` 的入口。
   - 保留 token 和语言参数。
7. 中文逻辑：
   - 接入和主 Web 页面一致的语言切换机制。
   - 所有新增 Web Agent 文案走字典。
8. storage 诊断：
   - tenant storage 缺失时进入诊断态。
   - 不渲染 mock session。
9. 消息滚动：
   - composer 发送成功后滚动到最新用户消息。
   - 如果用户当前在底部附近，SSE text_delta 持续跟随到底部。
   - 如果用户正在查看历史，显示回到最新按钮，不强制抢滚。

后端：

1. 至少禁止 cancelled/completed/failed task 继续追加普通 message。
2. `/tenant/agent-tasks/:id/message` 若 task 非 running/ready，应返回 409，并带明确错误：
   - `agent task is not accepting messages`
   - 前端据此提示“请选择继续/新建运行”。
3. SSE 对终态 task 发送已有事件后关闭。
4. 提供 tenant storage diagnostics：
   - enabled/disabled。
   - storage type。
   - migration/connection 错误摘要。
   - 不返回敏感 DSN。

验收：

- 新建空会话后不出现 running。
- 不显示 Cancel。
- 不显示 0 文件/0 命令/0 改动。
- composer 可输入。
- Enter 发送能启动一次 run。
- 新发送消息无需手动滚动即可看到。
- storage 未配置时显示诊断页，不显示假会话。
- `/webui/` 可以进入 `/webui/agent`。
- 中文切换后 Web Agent 全页面显示中文。

### P1：接入真实 runner 执行链路

目标：用户发送消息后，真的有 agent 回复和工具事件。

后端建议新增 API：

```text
POST /tenant/agent-runs
POST /tenant/agent-runs/:id/cancel
GET  /tenant/agent-runs/:id/events/stream
```

或者最小化复用现有 task API：

```text
POST /tenant/agent-tasks/:id/run
POST /tenant/agent-tasks/:id/cancel
GET  /tenant/agent-tasks/:id/events/stream
```

runner 责任：

1. 接收 cwd、prompt、model、effort、permission mode、tenant/user/session context。
2. 调用现有 `query.Session` 或 `agentruntime.Runtime`。
3. 将模型 text delta 写入 `message/text_delta` event。
4. 将 tool call、file read、file edit、command、permission request 写入结构化 event。
5. 完成后写入 completed event 和 result_json。
6. 失败后写入 failed event 和 error payload。
7. cancel 时触发 `AgentTaskController.Cancel`，并最终写入 cancelled event。

关键要求：

- runner 要有 trace_id。
- runner 写入 events 时必须带 tenant/user/task。
- runner 不能只返回 HTTP response；必须通过 SSE/event store 让页面实时更新。
- 权限模式必须从 composer 进入后端，不只存在前端 UI。

验收：

- 发送 `请读取 README.md 并总结` 后，右侧 Files 出现 README.md。
- 中间出现 assistant 回复。
- SSE 中出现 text_delta/message/tool/file events。
- 后端 DB 中 `agent_task_events` 有完整生命周期。
- task 最终 completed。

### P2：产品级空白态和执行态视觉

目标：空白态接近 Codex 的简洁大方，执行态才展示密集信息。

前端：

1. 新增 `AgentBlankState`：
   - 大标题：`我们该构建什么？`
   - composer 居中、宽度受控。
   - 下方显示 workspace、permission、model、effort。
2. activity strip 延迟出现：
   - 只有 `hasExecutionEvents || running || completed || failed || cancelled` 时出现。
   - draft 状态完全隐藏。
3. conversation 区分类：
   - user prompt：用户消息，靠右或独立用户侧，使用轻量气泡。
   - assistant text：模型回复，靠左或主内容列，适合长文、列表和代码块。
   - lifecycle/progress/tool events：右侧 Progress/Files/Trace，不进入主对话气泡。
4. 左侧 session row 区分：
   - draft：无状态点或淡灰点。
   - running：橙点。
   - completed：绿点。
   - failed/cancelled：红点或灰红点。
5. 消息身份操作：
   - 用户消息显示时间、复制、编辑/重发入口。
   - AI 回复显示处理耗时、复制、引用/继续入口。
   - 两侧操作按钮尺寸、线宽、hover 状态保持统一。

验收：

- 与 Codex 空白页相比，第一眼不是 dashboard，不是卡片堆。
- 无空白态 0 指标。
- 执行态信息密度高但不乱。
- 中英文切换后布局不溢出。
- 用户消息和 AI 回复左右/分层清晰，截图中身份一眼可辨。
- 消息满屏后继续发送，最新消息自动可见。

### P3：多 run 历史和继续执行

目标：一个 session 支持多轮执行，而不是单 task 无限追加。

数据模型建议：

```text
agent_sessions
  id
  tenant_id
  user_id
  cwd
  title
  metadata_json

agent_runs
  id
  session_id
  status
  prompt
  model
  effort
  permission_mode
  started_at
  finished_at
  result_json

agent_run_events
  id
  run_id
  event_type
  payload_json
  trace_id
  created_at
```

如果短期继续使用 `agent_tasks`，也要在 metadata 中明确 `session_id/run_id`，避免 UI 把一次执行和长期会话混为一谈。

## API 契约建议

### Create Draft Session

```http
POST /tenant/agent-sessions
```

Request:

```json
{
  "title": "测试005",
  "cwd": "/path/to/golang-cc",
  "metadata_json": {
    "source": "webui-agent"
  }
}
```

Response:

```json
{
  "id": 12,
  "status": "draft"
}
```

### Start Run

```http
POST /tenant/agent-sessions/:id/runs
```

Request:

```json
{
  "prompt": "读取 README.md 并总结",
  "model": "gpt-5.5",
  "effort": "medium",
  "permission_mode": "ask"
}
```

Response:

```json
{
  "run_id": 44,
  "status": "queued",
  "trace_id": "webui-..."
}
```

### Run SSE

```http
GET /tenant/agent-runs/:id/events/stream?cursor=0
```

Events:

```text
event: connected
data: {"run_id":44}

event: agent_run_event
data: {"id":1,"event_type":"started","payload_json":"..."}

event: agent_run_event
data: {"id":2,"event_type":"text_delta","payload_json":"{\"text\":\"...\"}"}

event: agent_run_event
data: {"id":3,"event_type":"file_read","payload_json":"{\"path\":\"README.md\"}"}

event: agent_run_event
data: {"id":4,"event_type":"completed","payload_json":"..."}
```

## 真机操作验收方案

必须用真实 server、真实 MySQL、真实浏览器验证，不只跑 mock E2E。

### 环境启动

```bash
npm --prefix web run build

GOLANG_CLAUDE_CODE_WEBUI_DIR=web/dist \
GOLANG_CLAUDE_CODE_MYSQL_DSN='root@tcp(127.0.0.1:3306)/golang_cc_web_agent_e2e_zh?multiStatements=true&parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27&charset=utf8mb4' \
go run ./cmd/golang-cc server --host 127.0.0.1 --port 18085 --auth-token test-token
```

测试地址：

```text
http://127.0.0.1:18085/webui/agent?token=test-token
http://127.0.0.1:18085/webui/?token=test-token
```

启动后先验证：

```bash
curl -sS http://127.0.0.1:18085/health -H 'Authorization: Bearer test-token'
curl -sS http://127.0.0.1:18085/tenant/agent-tasks -H 'Authorization: Bearer test-token'
```

如果第二个命令返回 `tenant storage is not configured`，本轮不能称为 Web Agent 全链路通过；必须修复启动环境或诊断 UI 后再继续。

### 真机流程 0：主 Web 页面到 Web Agent 入口

步骤：

1. 打开 `http://127.0.0.1:18085/webui/?token=test-token`。
2. 切换到中文。
3. 点击进入 Web Agent 的入口。

预期：

- 跳转到 `/webui/agent?token=test-token` 或等价路由。
- token 不丢失。
- 中文语言状态不丢失。
- 可以从 Web Agent 返回主 Web 页面。

### 真机流程 1：空白新会话

步骤：

1. 打开 Web Agent 页面。
2. 点击 `新会话`。
3. 选择当前 cwd。
4. 标题填 `测试空白态`。
5. 不填首条指令。
6. 创建/进入会话。

预期：

- 中间是 Codex-like 空白态。
- 右下角是发送按钮，不是取消。
- 不显示 `running`。
- 不显示 `已读取 0 个文件`。
- 不显示 `已运行 0 条命令`。
- 不显示 `0 个文件已更改`。
- DB 中没有 running task，或只有 draft session。

### 真机流程 2：首条消息启动执行

步骤：

1. 在 composer 输入：`读取 README.md 并总结项目目标`。
2. 按 Enter。

预期：

- 按 Enter 后才进入 running。
- 右下角切换为取消。
- SSE 连接 live。
- Progress 出现 started。
- 如果 runner 已接入，出现 file/tool/text events。
- 如果 runner 未接入，页面必须显示清晰错误或未配置提示，不能沉默。

### 真机流程 3：取消

步骤：

1. 在 running 状态点击取消。

预期：

- 短暂显示取消中。
- 后端返回后变成 cancelled。
- 右下角恢复发送。
- SSE 关闭或进入 closed。
- 再点发送不会写入同一个 cancelled run，而是创建 continuation run 或提示需要继续。

### 真机流程 4：多轮继续

步骤：

1. completed/cancelled 后输入 `继续`。
2. 按 Enter。

预期：

- 创建新 run。
- 左侧仍属于同一个 session。
- 右侧可看到新 run progress。
- 旧 run events 不丢失。

### 真机流程 5：权限

步骤：

1. permission mode 设为 ask。
2. 发送一个需要文件编辑或命令执行的请求。

预期：

- 权限请求进入右侧 Permissions。
- composer 左下角权限状态常驻可见。
- 用户批准/拒绝后 runner 收到结果。
- 审计表有记录。

### 真机流程 6：视觉和对称性审查

步骤：

1. 用真实浏览器分别打开桌面宽屏、窄屏和移动宽度。
2. 截图空白态、running 态、cancelled/completed 终态、中文态、英文态。
3. 构造多条消息，让 conversation 区出现滚动条。
4. 人工检查并用 Playwright 截图归档。

预期：

- composer 居中，不偏左或偏右。
- 左右面板 toggle icon 对称。
- SVG/icon 尺寸、线宽、垂直对齐一致。
- 底部 permission、model、context、send/cancel 视觉重心稳定。
- 页面不是粗糙卡片堆叠，白底/浅灰、少阴影、边框统一。
- 中文按钮和英文按钮均不撑破容器。
- 用户消息和 AI 回复分别位于不同侧或不同视觉层级，身份区分清楚。
- 复制、编辑、时间等消息操作图标对齐，不遮挡正文。

### 真机流程 7：消息满屏后的自动滚动

步骤：

1. 准备一个已有多条消息的 session，使中间 conversation 区已经可滚动。
2. 手动滚动到最底部。
3. 在 composer 输入 `继续测试滚动` 并按 Enter。
4. 观察用户消息是否立即可见。
5. 等待 SSE/text_delta 或 mock runner 输出追加。

预期：

- 发送后无需手动滚动即可看到刚发送的用户消息。
- 如果用户保持在底部附近，AI 回复追加时持续跟随到底部。
- composer 不遮挡最后一条消息。
- 如果用户先手动向上查看历史，再有新输出，页面不强制抢滚，而是显示回到最新入口。

### 真机流程 8：截图和使用文档归档

步骤：

1. 将关键截图保存到 `docs/web_agent/images/`。
2. 在中文使用文档中说明每张图对应的测试步骤和预期。
3. 在英文使用文档中同步关键入口和验证说明。
4. 在 `README.md` 和 `docs/README.md` 保持索引。

预期：

- 截图不是临时散落文件。
- 使用文档可以指导用户复现真机流程。
- README 能直接找到 Web Agent 使用说明和修复计划。

## 自动化测试方案

### Unit Tests

前端：

- `draft` 状态显示 send，不显示 cancel。
- `draft` 状态隐藏 activity strip/rows。
- `running` 状态显示 cancel。
- `cancelled/completed/failed` 状态显示 send。
- Enter 发送，Shift+Enter 换行。
- lifecycle events 不进入 conversation bubble。
- message/text_delta 进入 conversation bubble。
- 发送新消息后调用最新消息滚动逻辑。
- 用户向上查看历史时不强制自动滚动。
- 用户消息和 assistant 消息使用不同 role class/布局属性。

后端：

- message endpoint 拒绝 cancelled/completed task。
- start run 创建 running/queued 状态。
- cancel run 更新状态并追加 cancelled event。
- SSE 对终态 run 发完已有事件后关闭。
- runner 写入 completed/failed 后状态一致。

### API E2E

使用 `httptest` 和真实 MySQL E2E：

1. 创建 draft session。
2. start run。
3. SSE 读取 started。
4. append text/tool/file events。
5. cancel 或 complete。
6. 查询 DB 验证 session/run/event/audit/telemetry。

### Browser E2E

Mock E2E：

- 覆盖视觉状态和交互状态，不依赖模型。
- 断言空白态没有 0 指标。
- 断言 running 才有 cancel。
- 断言 lifecycle JSON 不污染 conversation。
- 断言 conversation 满屏后发送消息会自动显示最新消息。
- 断言用户消息和 AI 回复位于不同侧或不同视觉层级。

Live E2E：

- 启动真实 server + MySQL。
- Playwright 打开 `http://127.0.0.1:18085/webui/agent?token=test-token`。
- 不拦截 API。
- 完成新建、发送、SSE、取消、继续、DB 查询。
- 构造长会话后验证自动滚动、回到最新按钮、composer 不遮挡最后消息。

### Golden Tests

如果 runner 接入影响 prompt 或 tool event 格式，补充或更新相关 golden：

- server golden：API response envelope。
- query golden：runner prompt mode。
- task/tool golden：event payload shape。

## 全生命周期验收矩阵

| 阶段 | 用户动作 | 后端状态 | SSE | UI | DB 证据 |
| --- | --- | --- | --- | --- | --- |
| Draft | 新建空会话 | draft/no task | 无 | Send、空白态 | session draft |
| Submit | Enter 发送 | queued/running | connected + started | Cancel、显示运行态 | run created |
| Scroll | 满屏后继续发送 | running/ready | message/text_delta | 最新消息自动可见 | message event |
| Tool | 读取/命令/编辑 | running | file/tool/command | activity 出现 | events |
| Permission | 需审批 | running/waiting_permission | permission_request | Permissions tab | audit/event |
| Text | 模型输出 | running | text_delta/message | assistant bubble | message event |
| Complete | 执行完成 | completed | completed then closed | Send/continue | result_json |
| Cancel | 用户取消 | cancelled | cancelled then closed | Send/continue | cancelled event |
| Continue | 继续输入 | new run | new stream | 同 session 新 run | linked run |
| Fail | runner 出错 | failed | failed then closed | 错误提示 + retry | error payload |

## 优先级

### P0 必须先做

1. 新会话 draft 化，不再默认 running。
2. 空白态隐藏 0 activity。
3. running 才显示 cancel。
4. 终态 task 禁止追加普通 message。
5. 消息满屏后发送新消息自动滚动到可见区域。
6. 页面明确提示 runner 未接入时不会回复。

P0 完成后，至少不会继续误导用户。

### P1 下一步

1. 接入真实 runner。
2. 发送消息后产生 assistant 回复。
3. runner events 进入 SSE。
4. DB/trace/telemetry 全链路可查。

P1 完成后，Web Agent 才是可实际操作的 agent 页面。

### P2 体验打磨

1. Codex-like 空白态视觉。
2. 活动区渐进展示。
3. Progress/Files/Permissions/Trace/Usage 右侧完整化。
4. 用户消息和 AI 回复分侧/分层展示，身份清晰。
5. mobile/desktop 响应式细节。

### P3 扩展

1. session/run 双层模型。
2. continuation runs。
3. run 历史切换。
4. replay/debug trace。

## 不做什么

- 不把 `/webui/agent` 伪装成已接入真实 agent runner。
- 不再用 mock/seed 数据污染真实页面。
- 不把 lifecycle event JSON 放到主对话气泡。
- 不用前端假回复模拟 assistant。
- 不在 cancelled/completed run 上继续追加用户 message。

## 交付完成标准

一轮实现只有同时满足以下条件才算完成：

1. 代码通过：
   - `go test ./internal/server -count=1`
   - `go test ./... -count=1`
   - `npm --prefix web test`
   - `npm --prefix web run build`
   - `npm --prefix web run test:e2e`
   - `git diff --check`
2. 真机通过：
   - 真实 server。
   - 真实 MySQL。
   - 真实浏览器。
   - 不拦截 API。
   - 截图保存到 `docs/web_agent/images/` 或 `/tmp` 并在报告中引用。
3. DB 证据通过：
   - session/run/task 状态正确。
   - events 顺序正确。
   - message/tool/file/permission/completed/cancelled 全部可查。
4. 用户体验通过：
   - 空白态简洁。
   - 执行态信息完整。
   - 终态可继续。
   - 错误/未配置 runner 不沉默。
