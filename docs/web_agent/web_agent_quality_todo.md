# Web Agent Quality TODO

本文件记录 2026-06-30 真机反馈中的 Web Agent 页面质量问题、根因和修复验收计划。目标不是只修视觉，而是把对话展示、流式滚动、状态兜底和 usage 显示做到可验证、可回归。

## P0-1 Markdown 显示是原始文本

现象：

- AI 回复中的 `##`、`>`、`---`、`- [x]` 等 Markdown 直接显示为纯文本。
- 长回复看起来像未渲染日志，不像 Codex/Chat 类产品的可读消息。

根因：

- `WebAgentPage.tsx` 当前用 `<p>{message.content}</p>` 直接渲染整段文本。
- CSS 用 `white-space: pre-wrap` 保留原始格式，但没有块级 Markdown 解析、inline code、bold、blockquote、list、hr、code fence 等结构。

修复：

- 增加安全的 React Markdown lite renderer，不使用 `dangerouslySetInnerHTML`。
- 支持 heading、paragraph、blockquote、ordered/unordered/task list、hr、code fence、inline code、bold。
- 为 `.agent-markdown` 增加紧凑、白底、可读的样式。

验收：

- 单测覆盖 heading、blockquote、task list、bold、inline code。
- 真机滚动长 Markdown 回复时不再出现原始 `##`/`>` 作为主要视觉形态。

## P0-2 流式回复速度和页面显示不同步，底部内容被 composer 遮挡

现象：

- AI 回复很快时，底部一部分内容需要手动滚动才能看到。
- 页面显示跟不上 SSE 的 `text_delta`，打字机体验不如 Codex。

根因：

- 自动滚动 effect 只依赖 `conversationMessages.length`，而流式输出会持续追加到同一个 assistant message，消息数量不变，导致后续 delta 不触发滚动。
- `scrollIntoView({ block: "end", behavior: "smooth" })` 会在高频 delta 下排队动画，且目标元素对齐不稳定。
- conversation bottom padding 只按固定值设置，没有给快速流式输出预留足够尾部缓冲。

修复：

- 自动滚动依赖最新消息内容长度和事件数量，而不是只依赖消息数量。
- 追随最新消息时直接滚动容器 `scrollTop = scrollHeight`；用户查看历史时保持当前位置并显示“回到最新”。
- 流式更新使用 `auto` 行为，避免连续 smooth 动画造成明显延迟。
- 增加底部 scroll padding，确保最后一条消息不会贴着 composer。

验收：

- Playwright 长消息 stub 流式测试，发送后视口稳定在最新 assistant 内容。
- 手动滚动到历史后不强行拉回底部，并显示“回到最新”。

## P0-3 回复过程中卡住，不能继续输入，AI 也不再回复

现象：

- 页面显示运行/取消状态，但没有新的输出。
- composer 不能发送新消息，用户感知为页面卡死。

根因：

- `handleSendForTask` 发送后只轮询 `getAgentTask` 20 次，每次 250ms，总计约 5 秒；真实模型超过 5 秒时前端会进入不一致状态。
- SSE stream 关闭后只设置 `streamState=closed`，没有强制 refresh 当前 task；如果终态事件错过或流异常，task 可能长期停留在 running。
- 没有前端长时间无事件兜底，也没有明确的超时状态提示。
- 后端 SSE stream 每秒轮询并在 task 终态时退出，但如果前端错过终态事件或连接异常，当前页面缺少恢复策略。
- 追加真机反馈确认：存在“AI 已回复一部分，但 provider/runner 不返回终态”的路径；这不是单纯前端问题，后端 task runner 没有总执行超时和流式 idle timeout，会让任务永久停留在 `running`，右下角一直显示“取消”。

修复：

- 发送后不再用 5 秒轮询决定 UI 完成；改为发送成功后交给 SSE/状态轮询收敛。
- 增加 running watchdog：运行中每 2 秒 refresh task/events；超过 90 秒无事件时提示并自动 refresh。
- stream `onDone` 后立即 refresh 当前 task，避免关闭后状态陈旧。
- composer 只有 selected task 真实离开 running 后才回到可发送；终态后继续发送走 continuation task。
- 后端 agent task runner 增加默认 20 分钟总执行超时和默认 2 分钟流式 idle timeout；provider 中途挂住时自动取消上下文、写入 `failed` 事件和 failed task result，避免永久 running。可通过 `GOLANG_CLAUDE_CODE_AGENT_TASK_RUN_TIMEOUT_SECONDS` 和 `GOLANG_CLAUDE_CODE_AGENT_TASK_IDLE_TIMEOUT_SECONDS` 调整。

验收：

- 单测覆盖慢任务保持 running、不提前 ready。
- 单测覆盖 stream done 后 refresh 状态。
- 单测覆盖 stream 已输出 partial reply 后 idle 挂起，服务端自动落 failed 终态。
- 真机真实 provider E2E 能完成并回到可发送。

## P1-4 每次发消息都从第一条滚到当前消息

现象：

- 发送新消息后页面从历史第一条开始平滑滚到最新，看起来像无效滚动。

根因：

- `refresh(taskID)` 会重载 parent + continuation chain，DOM 重新渲染后滚动位置回到顶部。
- 随后 `scrollToLatest("smooth")` 造成明显从顶部到底部的动画。

修复：

- 选中/刷新会话和发送后收敛时使用 `auto` 滚动，不做长距离 smooth。
- 仅用户点击“回到最新”时使用 smooth。
- 使用 conversation key 追踪内容变化，避免重新渲染后错误动画。

验收：

- 发送 continuation 后页面直接保持最新消息，不出现从第一条滚到底部的长动画。

## P1-5 Composer 上下文一直 0%

现象：

- 底部始终显示 `上下文 0%`，即使真实回复已经有 `input_tokens/total_tokens`。

根因：

- 前端只读取 `metadata.context_percent` 或 `result.context_percent`。
- 后端 agent task `completed` result 当前写入 tokens，但不写 `context_length/context_percent`。
- 前端没有用 `total_tokens / context_length` 兜底计算。

修复：

- 后端 `runAgentTaskMessage` 根据 cwd/model 读取配置中的 context length，并在 result JSON 写入 `context_length` 和 `context_percent`。
- 前端从 task result 和 completed event 中读取 `input_tokens/output_tokens/total_tokens/context_length/context_percent`，计算百分比。
- 如果只有 tokens 没有 context length，前端 fallback 使用常见大上下文默认值，避免永久 0。

验收：

- 单测覆盖 result tokens 计算出非 0 context percent。
- 真实 E2E 输出 result JSON 中包含 `context_percent`，页面 composer 显示非 0。

## P1-6 Workspace 验证按钮不符合 Codex-like 工作台心智

现象：

- 左侧 Workspace 区显示路径输入框和醒目的蓝色 `Validate/验证` 按钮。
- 用户会疑惑这个按钮是在验证模型、权限、会话还是路径。
- 视觉上更像后台管理表单，不像 Codex 当前产品中的 workspace/thread 上下文选择。

根因：

- Web Agent 是浏览器页面，不能天然继承一个可信的本地 cwd，所以早期把路径校验显式暴露成按钮。
- 当前 `/agent/workspaces/validate` 的真实职责是校验 cwd：必须是绝对路径、存在、是目录，并返回 git root/workspace name。
- UI 把底层工程动作直接展示给用户，没有产品化成“选择当前工作区”的状态机。

方案：

- 左侧 Workspace 区去掉常驻大号 `Validate/验证` 按钮。
- recent workspace 点击即选择，并后台调用 `/agent/workspaces/validate`。
- 手动输入 cwd 时 debounce 300-500ms 自动校验。
- 输入框旁或下方展示轻量状态：
  - checking / 正在检查
  - ready / 可用
  - not found / 路径不存在
  - not absolute / 请输入绝对路径
  - not a directory / 不是目录
  - not a git repo / 可用但不是 Git 仓库
- New Session modal 创建前必须做一次兜底校验；校验失败不创建 session。
- 新建 session 的 metadata.cwd 必须使用 normalized cwd。

验收：

- 页面不再显示常驻大号 `Validate/验证` 主按钮。
- recent workspace 点击后 session list 切换，并在后台完成 cwd 校验。
- 手动输入有效 cwd 后自动显示 ready 状态。
- 输入相对路径、不存在路径、文件路径时显示对应错误，并阻止 New Session。
- New Session 创建成功后，DB/task metadata 中的 `cwd` 等于后端返回的 normalized cwd。
- 前端单测覆盖自动校验、失败提示、New Session 创建前兜底校验。
- 真机浏览器覆盖有效路径、无效路径、recent 切换和新建会话全流程。

## 验证清单

- `go test ./internal/server -count=1`
- `npm --prefix web test -- WebAgentPage.test.tsx`
- `npm --prefix web test`
- `npm --prefix web run build`
- `git diff --check`
- `scripts/web-agent-real-e2e.sh` 真实 provider 全生命周期。
- `scripts/web-agent-scroll-smoke.sh` 或等效 Playwright 长消息流式测试，验证滚动和 Markdown。

## 状态

- DONE: P0-1 Markdown renderer，已实现安全 React Markdown lite renderer，并补前端单测。
- DONE: P0-2 流式滚动同步，已改为内容/事件驱动的容器 auto scroll，并增加底部缓冲。
- DONE: P0-3 卡住兜底和超时恢复，已增加 running watchdog、stream done refresh、发送后按真实 task 状态恢复 composer。
- DONE: P1-4 去掉从第一条到最新的无效 smooth 滚动，发送/刷新收敛使用 auto scroll，用户手动回到底部仍保留按钮入口。
- DONE: P1-5 context 使用占比计算，后端 result 写入 context_length/context_percent，前端按 result/event tokens 兜底计算。
- DONE: P1-6 Workspace 验证按钮产品化，已改为 Codex-like 自动校验和轻量状态提示，去掉常驻 `Validate/验证` 主按钮。

## 本轮落地记录

- 后端：`runAgentTaskMessage` 读取 cwd/model 对应 settings context length，把 `total_tokens`、`context_length`、`context_percent` 写入 completed result JSON；server 单测用临时 `.claude/config.local.yaml` 锁定该行为。
- 后端高可用：agent task runner 增加总超时和流式 idle timeout；当模型/provider 中途无响应时自动落 failed 终态，避免页面长期卡在“取消”。
- 前端：`WebAgentPage` 新增 Markdown lite renderer、运行态 watchdog、stream done refresh、发送后状态收敛、上下文 token fallback 计算。
- 前端 Workspace：`WebAgentPage` 新增 workspace idle/checking/ready/error 状态机；recent workspace 点击会选择并后台校验，手动 cwd 输入 400ms debounce 自动校验，New Session 创建前兜底调用 `/agent/workspaces/validate`，并使用返回的 normalized cwd/workspace_name 写入 session/task metadata。
- 样式：`.agent-markdown` 支持标题、段落、引用、列表、任务列表、分割线、代码块、行内代码和粗体；conversation 增加底部 scroll padding，避免快速输出被 composer 遮挡。
- 回归：新增/更新 WebAgentPage 单测覆盖 Markdown 结构化渲染、context 非 0、发送后 ready/running 状态机和既有中英文/会话/continuation 行为。
- 验证：新增 WebAgentPage 单测覆盖自动校验、失败阻止创建、normalized cwd 创建 metadata；已通过 `npm --prefix web test -- WebAgentPage.test.tsx`、`npm --prefix web test`、`npm --prefix web run build`、`go test ./internal/server -count=1`；真实浏览器验证桌面和 390px 移动视口有效路径 Ready、相对路径错误、无常驻 Validate 按钮且输入不溢出。
