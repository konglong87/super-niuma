# Web Agent Codex-Grade Revision Plan

本文档记录 2026-06-30 对 `/webui/agent` 的现状复盘和下一轮产品级改造方案。当前目标不是马上改代码，而是先把问题、原因、方案、验收口径闭环，避免继续在局部视觉和演示数据上打补丁。

## 结论

当前 Web Agent 已经具备“Web 工作台骨架”，但还不是 Codex/Cursor 级别的本地 agent 产品页。最需要先处理的不是再加几个卡片，而是三件基础问题：

1. 页面必须只展示真实 session 状态，不能让 seed/mock/测试 session 混进用户主视图。
2. workspace/cwd 必须成为用户可选择、可手动输入、可验证的一级控制项。
3. 主对话区和右侧 inspector 必须有稳定滚动模型，长消息、长工具输出、长 trace 不得把页面撑坏或无法查看。

这三件事解决后，再做 Codex-like 的视觉密度、composer 控制台、右侧 inspector 和快捷键体系，才有产品级基础。

## 当前现状

代码核对范围：

- `web/src/components/WebAgentWorkbench.tsx`
- `web/src/styles/app.css`
- `web/src/lib/api.ts`
- `web/src/lib/types.ts`
- `internal/server/agent.go`
- `docs/web_agent/web_agent_workbench_ui_plan.md`
- `docs/web_agent/web_agent_workbench_visual_spec.md`

已经实现的能力：

- Web Agent 主页面、顶部栏、左侧 session rail、中间对话区、右侧 inspector、底部 drawer。
- session list/create/detail、turn SSE、cancel、permission resolve、slash commands、rewind candidates、recent workspaces 等 `/agent/*` API 连接。
- 左侧已有项目/session grouping、搜索、状态过滤、rail collapse。
- composer 已有 slash strip、附件折叠、permission segment、context meter、model/sandbox meta、send/cancel。
- inspector 已有 Permissions/Tools/Diff/Output/Checkpoints/Trace/Usage/Goal/Context tabs。
- CSS 里已经用了 `minmax(0, 1fr)`、`overflow: auto`、drawer/inspector 基础滚动。

明显问题：

- `WebAgentWorkbench.tsx` 仍以内置 `seedStatus`、`seedSessions`、`seedWorkspaces`、`seedPermission` 初始化。后端请求失败时会进入 “mock preview” 心智，用户会看到像真实数据一样的示例 session。
- 当前 `/agent/sessions` 和 `/agent/workspaces/recent` 已经按 server workspace 默认过滤，但 UI 仍缺少明确的 workspace chooser/manual cwd 输入，用户不能主动切换到另一个项目。
- 截图显示主对话区域被 composer 和页面外层挤压，用户反馈“右侧的对话消息不支持滑动查看”。这说明当前滚动职责虽然在 CSS 中存在，但 viewport 高度、外层 dashboard、center workbench、conversation plane、composer/footer 的约束没有形成稳定闭环。
- 右侧 inspector 是 overlay drawer，默认隐藏；当打开时不是完整三栏固定工作区，和 Codex 当前产品页的“主区 + 可停靠详情面板”体验仍有差距。
- 当前视觉还偏“高亮大卡片演示页”：消息块颜色过重、阴影偏多、列表密度不够稳定、中文/英文状态混排，和开发工具需要的白底/浅灰/少阴影/高密度扫描还有差距。
- command palette 只是入口和简单命令列表，还没有完整动作模型、键盘导航和焦点管理。

## 产品目标

Web Agent 应该像 Codex 当前产品页一样，是“本地 agent 控制台”，不是普通 chat UI。

核心原则：

- 可信：页面显示的 session、cwd、permission、工具调用必须来自真实后端状态；演示态必须显式标注，不得伪装成真实状态。
- 稳定：左侧列表、中间对话、右侧 inspector、底部 drawer 各自滚动，不互相抢高度。
- 高密度：信息靠层级、分隔线、tab、sticky header、monospace pane 组织，而不是靠大面积彩色卡片。
- 可控：workspace/cwd、model、permission mode、sandbox、context 使用率应在 composer 附近或 session 创建流程里可见可控。
- 安全：cwd 手动输入必须验证路径，permission mode 的高风险操作必须有二次确认和清晰提示。

## 是否参考 Codex 当前页面

当前实现不是对 Codex 当前页面的像素级复刻，也不应该直接照搬私有产品 UI。它目前更像“参考 Codex/Cursor 信息架构后做出的 Web Agent 工作台雏形”。

下一轮应该参考 Codex 当前产品级页面的这些原则，而不是复制皮肤：

- 左侧是工作对象导航，不是普通聊天历史堆叠。
- 中间是主要工作流，消息、工具、审批、输出在同一条时间线上有清晰层级。
- composer 是控制台，包含运行上下文，而不是单纯 textarea。
- 右侧是结构化 inspector，承载 diff、trace、usage、context、permissions，不做装饰性侧栏。
- 页面滚动由固定 viewport shell 管理，长内容在所属 pane 内滚动。

## 改造总方案

### P0. 状态可信度：移除伪真实 seed 主链路

问题：

- seed session 和 seed permission 现在直接作为初始状态，容易让用户误以为是自己的真实 session。
- 后端失败时 UI 仍能显示完整假会话，调试时很难判断是 API 问题还是 UI 预览态。

方案：

- 前端主链路初始状态改为空状态：`runtime=null`、`sessions=[]`、`recentWorkspaces=[]`、`permission=null`。
- seed 数据只允许出现在明确的 `demo`/storybook/Playwright visual mock 环境中。
- 后端不可用时展示连接错误空态，不自动塞假 session。
- session list 空时显示“当前 workspace 没有 Web Agent session”，并展示当前 cwd 和 New Session。
- Playwright visual regression 保留 route mock，但真实 `/webui/agent` 不再内置假状态。

原因：

- Codex 级产品页的第一要求是状态可信。用户看到的每一个 session 都应该能追溯到 transcript/API。
- 这个改动能直接避免“全是 001 / 全是测试会话”的二次误判。

验收：

- 后端 `/agent/sessions?limit=30` 返回 0 时，UI 不显示 seed session。
- API 失败时页面显示错误态，不显示 `web-agent-seed-*`。
- 测试 mock 中仍能渲染视觉回归页面。

### P1. Workspace/CWD 控制：可选择、可手填、可验证

问题：

- 当前 cwd 主要来自 server 启动参数和最近 workspace chip，用户不能手动选择任意项目路径。
- New Session 只用 `workspaceFilter || runtime.cwd`，没有明确告诉用户新会话会挂在哪个 cwd 下。

方案：

- 左侧 rail 顶部加入 Workspace Control：
  - 当前 workspace name + absolute cwd。
  - Recent Workspaces dropdown。
  - `Use current server cwd` 快捷动作。
  - `Enter path...` 手动输入入口。
- New Session 改成 popover/modal，而不是直接用 rail 内标题框：
  - `cwd`：必填，默认当前 workspace，可从 recent 选，也可手填 absolute path。
  - `title`：可选。
  - `model`：默认当前配置。
  - `permission_mode`：默认 ask。
  - `sandbox`：只读显示当前 local/remote。
- 后端增加 workspace validation 能力，建议两种实现之一：
  - 轻量：`POST /agent/workspaces/validate`，输入 `{ "cwd": "/abs/path" }`，返回 normalized cwd、workspace name、exists、is_dir、git_root。
  - 或者在 `POST /agent/sessions` 内严格校验 cwd，并把错误返回给 UI。
- Codex-like UX 调整：
  - 左侧 workspace 区不要保留常驻大号 `Validate/验证` 按钮；这个按钮是工程校验心智，不是 Codex 类工作台心智。
  - recent workspace 点击后立即选择并后台校验。
  - 手动输入 cwd 时 debounce 自动校验，输入框旁显示 `checking/ready/error` 等轻量状态。
  - New Session modal 创建前必须兜底校验 cwd；失败时阻止创建并显示 inline error。
  - 校验成功才更新 `selectedWorkspaceCwd` 和 New Session 默认 cwd。
- cwd 校验规则：
  - 必须是 absolute path。
  - `filepath.Clean` 后存在。
  - 必须是目录。
  - 不自动创建目录。
  - remote mode 下禁止或二次确认高风险路径。

原因：

- cwd 是本地 agent 的安全边界和工作对象，必须比普通 chat 的 title 更重要。
- 用户选择工作区后，session list、new session、context、permission scope 都应该围绕这个 cwd 变化。

验收：

- 用户可以从 recent workspace 切换 session list。
- 用户可以手动输入一个真实目录并新建 session。
- 输入不存在路径时，UI 阻止创建并显示明确错误。
- 新建 session 后，session metadata 里的 `cwd` 等于 normalized cwd。
- 左侧不再出现醒目的 `Validate/验证` 主按钮；校验状态以轻量 inline 状态呈现。
- 自动校验和创建前兜底校验都有前端单测覆盖。

### P2. 稳定滚动模型：消息、inspector、drawer 各自滚动

问题：

- 截图和反馈显示主对话消息无法稳定滑动查看。
- 现有 CSS 中 `.conversation-plane` 有 `overflow: auto`，但整体 shell 是 `min-height`，上层 `dashboard-shell/app-shell` 和 composer/footer 仍可能让页面整体滚动，导致中间 pane 没有固定剩余高度。

方案：

- 定义 Web Agent 专用 viewport shell：
  - `/webui/agent` 页面根容器使用 `height: calc(100dvh - appChromeHeight)` 或让 app content 在该路由下占满 `100dvh`。
  - `.web-agent-workbench` 使用 `height`，不是只用 `min-height`。
  - `.web-agent-layout` 使用 `min-height: 0; overflow: hidden`。
  - `.web-agent-center` 使用 `grid-template-rows: auto minmax(0, 1fr) auto`，固定 composer 在底部。
  - `.conversation-plane` 是唯一的主消息滚动容器。
- active session header sticky 在 `.conversation-plane` 内部，composer 不进入消息滚动区。
- long message/long tool output：
  - 消息正文允许 pane 内滚动，不撑破页面。
  - 工具输出默认只显示摘要，长输出进入 inspector/drawer 的 monospace scroll pane。
- 右侧 inspector：
  - desktop 支持 docked 模式：打开后 layout 从二栏变三栏，而不是只 overlay。
  - inspector 内每个 tab 独立 `overflow: auto`。
- mobile/tablet：
  - 左侧 session rail 和 inspector 都变 bottom sheet/drawer。
  - 主对话和 composer 仍在一个 viewport 内稳定。

原因：

- Codex/Cursor 这类工作台的关键是 pane-based scrolling。外层页面滚动会破坏“边看输出边输入/审批”的工作流。
- 只给 `.conversation-plane` 加 `overflow: auto` 不够，必须让所有父级都有确定高度和 `min-height: 0`。

验收：

- 构造 100 条消息后，中间消息区可滚动，topbar、left rail、composer 不跟随滚动。
- 构造 500 行 tool output 后，主消息只显示摘要，详情 pane 内滚动。
- 打开 inspector 后，conversation 仍可滚动，inspector tab 内容也可滚动。
- Playwright 桌面和移动端都验证滚动条/scrollTop 变化。

### P3. Composer 控制台化

问题：

- 当前 composer 已有控制项，但布局仍像“输入框 + 一排按钮”。
- permission mode、model、sandbox、context meter 虽然出现了，但不是完整的运行控制台。
- Web Agent 过去固定使用 `chat` prompt mode，和 TUI 默认 `code` prompt mode 不一致；同样 cwd 下，TUI 能读取本机 `~/.claude/skills`/项目 `.claude/skills`，Web Agent 则按多租户 chat 边界不读取本地 code-development context，容易让用户误以为 skills 丢失。

方案：

- composer 拆成三层：
  - Top command row：slash command chips、attachment、checkpoint/rewind、branch、clear/compact。
  - Main input：稳定高度 textarea，支持 `Cmd+Enter` 发送、`Esc` 取消/收起 palette。
  - Runtime control row：左侧 permission mode + evidence drawer，右侧 context meter + model + sandbox + send/cancel。
- 增加 prompt mode 显式选择：
  - `chat`：默认值，保持 Web 多租户/隔离会话语义；不读取本机 repo、CLAUDE.md、本地 skills 或开发机配置，只使用会话、显式提供上下文、tenant/chat 工具。
  - `code`：本地代码 agent 模式；使用当前 cwd 加载 code profile，可读取本机用户级/项目级 skills catalog 和代码上下文，适合需要接近 TUI/Codex 本地 agent 的场景。
  - New Session modal 和 composer runtime row 都显示当前 mode；创建/续聊时写入 task `metadata_json.prompt_mode`。
  - 后端 runner 从 task metadata 读取 `prompt_mode`，只接受 `chat`/`code`；缺省或非法值回落 `chat`，避免历史任务和错误输入破坏隔离默认值。
- waiting permission 时：
  - composer 显示 paused 状态。
  - send 降权或提示“先处理权限请求”。
  - permission banner 和 inspector actions 是推进 turn 的主路径。
- bypass：
  - danger 样式。
  - 第一次点击需要 confirm popover，说明影响范围。

原因：

- agent composer 不是聊天输入框，而是本地执行控制台。运行上下文必须和输入动作绑定在同一区域。

验收：

- running 时 send 变 cancel。
- waiting permission 时用户能立即找到审批入口。
- context meter、model、sandbox 在窄屏不溢出。
- 新建会话选择 `code` 后，后端 `QueryRequest.PromptMode` 为 `code`；选择 `chat` 或历史任务缺省时为 `chat`。
- 右侧 Progress 能看到该 session 当前 prompt mode，便于解释 skills/context 是否应该可见。

### P4. 左侧项目/会话层级产品化

问题：

- 当前已有 grouping，但 Workspace Control、session create、filter 三者逻辑还没有闭环。
- rail collapse 后信息损失过大，缺少 tooltip 和当前 workspace 反馈。

方案：

- 左侧结构调整：
  - Workspace Control。
  - New Session。
  - Search + status filters。
  - Project groups。
  - Session items。
- session item 降低卡片感：
  - 使用 compact row/list。
  - 状态 badge、title、recap、cwd、time、tool/error/permission 小图标。
  - active 用左侧 accent bar，不用大面积蓝底。
- collapsed rail：
  - 只显示 workspace/project icons、active indicator、pending badge。
  - hover tooltip 展示 workspace/session title。

原因：

- 用户的工作对象是项目，不是孤立聊天。cwd 选择、新会话归属、session list 过滤必须在同一个层级内解释清楚。

验收：

- 切换 workspace 后 session list 只显示该 workspace。
- New Session 默认挂在当前 workspace。
- collapsed rail 仍能看出当前 workspace 和 pending 状态。

### P5. Inspector 完整化：从 overlay 变结构化运行面板

问题：

- 当前 inspector tab 很全，但内容仍基础；Diff 仍同时存在 bottom drawer 和 inspector，职责没有清晰划分。

方案：

- desktop 支持 docked inspector：
  - 默认隐藏。
  - 打开后为第三栏，宽度 340-380px。
  - 小屏仍为 overlay/bottom sheet。
- tab 职责：
  - Permissions：审批队列、风险、scope、决策历史。
  - Tools：本轮 tool timeline，包含 status/duration/input/output/trace id。
  - Diff：文件列表 + diff summary，长 diff 可打开 bottom drawer。
  - Output：长命令输出 scroll pane。
  - Checkpoints：rewind candidates 和恢复动作。
  - Trace：SSE events、turn id、session id、transcript path。
  - Usage：tokens/cache/model/turn count。
  - Context：cwd、prompt mode、memory、skills、MCP、capabilities。
- bottom drawer 保留为“大内容阅读区”，inspector 是“结构化摘要和动作区”。

原因：

- 右侧面板应该承载工作状态和可操作证据，不只是 tab 容器。Diff/Output/Trace 的长内容需要更大的 drawer，但入口和摘要应该在 inspector。

验收：

- 有 pending permission 时自动打开 Permissions tab。
- 有工具错误时 Tools/Output 有 error 标记。
- Diff tab 能看到 changed files summary，drawer 能看完整内容。

### P6. Command Palette 和快捷键体系

问题：

- 当前 `Cmd/Ctrl+K` 只打开简单 palette，复用 search state，容易把 session 搜索和 command 搜索混在一起。

方案：

- 独立 palette state，不复用左侧 session search。
- 支持 action registry：
  - New Session
  - Switch Workspace
  - Search Session
  - Open Inspector tab
  - Open Diff/Output/Checkpoints
  - Run slash command
  - Toggle rail
  - Toggle inspector
  - Focus composer
  - Cancel turn
- 键盘：
  - `Cmd/Ctrl+K` palette
  - `Cmd/Ctrl+Enter` send
  - `Esc` close modal/palette or cancel focus state
  - `Cmd/Ctrl+B` toggle rail
  - `Cmd/Ctrl+I` toggle inspector
- palette item 必须有 title、subtitle、icon、shortcut、disabled reason。

原因：

- Codex 级开发工具必须键盘优先。Palette 不是装饰入口，而是所有动作的统一索引。

验收：

- palette 可用键盘上下选择和 Enter 执行。
- palette 搜索不影响左侧 session search。
- 每个主动作可从 palette 找到。

## API 改动建议

P0/P1 最小 API 增量：

```http
POST /agent/workspaces/validate
```

Request:

```json
{
  "cwd": "/path/to/golang-cc"
}
```

Response:

```json
{
  "cwd": "/path/to/golang-cc",
  "workspace_name": "golang-claude-code",
  "exists": true,
  "is_dir": true,
  "git_root": "/path/to/golang-cc"
}
```

错误：

- `400 cwd must be an absolute path`
- `400 cwd does not exist`
- `400 cwd is not a directory`

同时强化：

- `POST /agent/sessions` 对 `cwd` 做同样校验。
- `GET /agent/sessions?cwd=...` 对 cwd normalize 后过滤。
- `GET /agent/workspaces/recent` 返回 current workspace + recent，不暴露测试 temp workspace，除非用户显式选择。

## 前端改动建议

核心组件拆分：

- `WebAgentWorkbench`
  - 只保留数据装配和整体布局。
- `WorkspaceSwitcher`
  - recent workspace、manual cwd、validate state。
- `SessionRail`
  - search/filter/project groups/session list/collapse。
- `ConversationPane`
  - session header/messages/tool summary/empty/error/loading。
- `AgentComposer`
  - slash row/input/runtime controls/send-cancel。
- `RuntimeInspector`
  - tabs + structured panels。
- `CommandPalette`
  - action registry + keyboard navigation。

状态模型：

- `selectedWorkspaceCwd`
- `workspaceValidation`
- `sessionsState: loading | ready | empty | error`
- `selectedSessionId`
- `turnState: idle | running | waiting_permission | cancelling | failed`
- `inspectorMode: hidden | docked | overlay`

原因：

- 当前单文件组件已经承担过多职责。继续堆会让滚动、workspace、palette、permission 状态互相污染。
- 拆分不是为了抽象漂亮，而是为了让每个 pane 的滚动和状态边界可测试。

## 视觉规格调整

目标风格：

- 白底/浅灰底。
- 少阴影，更多 border/divider。
- 8px 以内圆角。
- 高列表密度。
- 状态色只用于状态，不用于大面积装饰。
- tool/output/trace 使用 monospace 和结构化行。

具体调整：

- 消息不再使用大面积蓝/绿渐变卡片，改成轻边框 + role icon + 内容块。
- active session 用左侧 3px accent bar，不用整卡强蓝阴影。
- topbar 降低高度和阴影，更多像 app chrome。
- composer 保留轻微 elevation，但不能遮挡消息区。
- inspector docked 时不要盖住主对话。
- 所有按钮 label 保证中文/英文都不换行溢出。

## 分阶段执行计划

### 阶段 1：可信数据和 CWD

- 移除主链路 seed 初始状态。
- 增加 workspace validation。
- 增加 Workspace Control 和 New Session cwd 表单。
- 后端 cwd 校验和测试。
- 验证 live API 和浏览器真实状态。

### 阶段 2：滚动和布局骨架

- 固定 Web Agent route viewport shell。
- 修复 center conversation/composer/inspector/drawer 滚动职责。
- desktop inspector docked，mobile overlay。
- 加 Playwright 长消息/长输出滚动测试。

### 阶段 3：Composer 控制台和权限流

- 重构 composer 三层布局。
- waiting permission 状态闭环。
- bypass confirm。
- send/cancel/focus 快捷键。

### 阶段 4：Inspector 和 Drawer

- 结构化 Tools/Diff/Output/Trace/Usage/Context。
- drawer 只承载长内容阅读。
- 工具错误、diff、output badge。

### 阶段 5：Command Palette 和视觉 polish

- action registry。
- 键盘导航。
- 高密度视觉收敛。
- desktop/tablet/mobile 截图回归。

## 验证计划

后端：

```bash
go test ./internal/server -count=1
git diff --check
```

前端：

```bash
npm --prefix web test
npm --prefix web run build
```

真实服务：

```bash
go run ./cmd/golang-cc --cwd /path/to/golang-cc web --host 127.0.0.1 --port 18080 --auth-token test-token --no-open
curl -sS http://127.0.0.1:18080/health -H 'Authorization: Bearer test-token'
curl -sS 'http://127.0.0.1:18080/agent/sessions?limit=30' -H 'Authorization: Bearer test-token'
curl -sS 'http://127.0.0.1:18080/agent/workspaces/recent?limit=20' -H 'Authorization: Bearer test-token'
```

浏览器 E2E：

- `/webui/agent?token=test-token` 首屏不显示 seed session。
- 手动输入 cwd 并 create session。
- 长消息滚动。
- 长工具输出 drawer/inspector 滚动。
- inspector docked/overlay 响应式。
- command palette 键盘选择。

## 不做的事

- 不做 Codex 私有 UI 的像素级复制。
- 不把 mobile chat API 当作 Web Agent 本地工作 API。
- 不继续扩大 seed/mock 在真实页面的使用范围。
- 不为了视觉好看弱化 permission/bypass 风险提示。
- 不在 cwd 不明确时创建 session。

## 下一步建议

如果确认本方案，建议先做阶段 1 和阶段 2。原因是它们解决用户当前已经遇到的真实问题：假数据/测试会话污染、cwd 不可选、对话不能稳定滚动。视觉 polish 和 palette 可以排在这两项之后，否则会在不可信和不稳定的基础上继续堆 UI。
