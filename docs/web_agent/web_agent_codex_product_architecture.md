# Web Agent Codex Product Architecture

> 本文档描述现有 `/webui/agent` 的历史产品方案。新一代 `/webui/v2` 的已确认架构见 [go_cc_webui_v2_product_architecture.md](go_cc_webui_v2_product_architecture.md)；两者在验收期并行，本文档不会被新方案静默改写。

本文档定义 `/webui/agent` 下一轮产品级页面改造方案。目标是让 Web Agent 的操作页面接近 Codex 当前这种工作台体验：左侧是高频操作和工作对象区，中间最大区域是对话和 agent 主流程，右侧是实际进度、工具调用、文件改动、权限和证据区；左右两侧都允许用户隐藏、展示和恢复。

本文档只定义方案和交互闭环，不代表已经实现。

## 目标体验

Web Agent 页面应该给用户三个明确答案：

1. 我现在在哪个工作区工作？
2. Agent 当前在做什么、做到了哪一步、改了什么？
3. 我下一步能做什么：继续对话、批准权限、查看 diff、切换 session、停止任务？

页面不应该像普通聊天页，也不应该像 demo dashboard。它应该是本地 agent 的控制台。

## 总体页面架构

Desktop 默认三栏，中心优先：

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ Top Runtime Bar: workspace / model / sandbox / permission / global actions   │
├───────────────┬───────────────────────────────────────────────┬──────────────┤
│ Left Ops Rail │ Center Conversation Workspace                  │ Right Work   │
│               │                                               │ Progress     │
│ workspace     │ session header                                 │ current step │
│ sessions      │ messages + tool summaries                      │ tools        │
│ commands      │ composer console                               │ diff/output  │
│ shortcuts     │                                               │ permissions  │
├───────────────┴───────────────────────────────────────────────┴──────────────┤
│ Optional Bottom Drawer: full diff / long output / transcript / checkpoints   │
└──────────────────────────────────────────────────────────────────────────────┘
```

优先级：

- 中间对话区永远最大。
- 左侧服务于选择和发起操作。
- 右侧服务于理解进度和处理阻塞。
- 底部 drawer 只用于“大内容阅读”，不是常驻主区域。

## Codex 页面拆分观察

从当前 Codex 页面可以拆出 5 个稳定层级，Web Agent 应该复用这种产品结构，而不是复用旧 TUI 的快捷键心智。

```text
App Shell
  Left Activity + Project Rail
  Center Conversation Canvas
  Floating Composer Console
  Right Environment / Progress Card
  Global Overlay: command palette, workspace picker, review/diff drawer
```

### A. Left Activity + Project Rail

截图中的左侧不是单一 sidebar，而是三个层次：

1. 全局金刚操作：新对话、搜索、已安排、插件。
2. 项目列表：每个项目是一级工作对象。
3. 项目下二级会话：当前项目展开后显示最近会话，当前会话有选中态和时间。

对 Web Agent 的映射：

- 全局金刚操作对应 New Session、Search、Scheduled/Background Tasks、Plugins/MCP、Command Palette。
- 项目列表对应 workspace/project list。
- 二级会话对应该 workspace 下的 Web Agent sessions。
- 底部 Settings/Account 对应 runtime settings、identity、API token、provider config。

### B. Center Conversation Canvas

截图中的中间区域是主画布：

- 顶部是当前会话标题和少量操作。
- 中间是消息、引用、文件卡、改动卡。
- 底部是浮动 composer。
- composer 本身承担权限、模型、上下文和发送状态。

对 Web Agent 的映射：

- 普通对话消息用于用户意图和 assistant 总结。
- 文件引用卡用于附件、读取文件、生成文档、引用来源。
- 改动卡用于展示本轮 touched files、增删行、review/undo。
- 工具输出不直接铺满对话，而是摘要卡 + 右侧/底部详情入口。

### C. Floating Composer Console

截图里的 composer 是一个浮动控制台：

- 左下角显示当前权限模式，比如“完全访问”。
- 左侧有 `+` 附件/添加入口。
- 右下角显示模型、上下文/强度等运行信息。
- 最右是发送按钮。

对 Web Agent 的要求：

- 权限模式必须常驻 composer 左下角，用户不需要去设置页或按快捷键才知道当前权限。
- 模型和上下文使用占比必须常驻 composer 右下角。
- 用户可以直接点击权限、模型、上下文信息进入修改或查看详情。
- running/waiting permission/cancel 状态必须在 composer 上直接反馈。

### D. Right Environment / Progress Card

截图中的右侧不是宽大 dashboard，而是轻量浮层/停靠卡，包含：

- 环境信息。
- 变更统计。
- 本地运行环境。
- 当前分支。
- 提交或推送。
- GitHub CLI 状态。
- 任务命令。
- 来源。

对 Web Agent 的映射：

- Environment：cwd、sandbox、branch、provider、model、permission mode。
- Changes：changed files、insertions、deletions、review/diff。
- Tasks：当前运行命令、工具步骤、后台任务。
- Sources：引用文件、attachments、memory/skills/MCP 来源。
- Permissions：pending approval 作为最高优先级插入到右侧。

### E. Global Overlay

Codex 页面里的复杂信息不全塞进三栏：

- 打开方式 dropdown。
- review/diff。
- command palette。
- workspace/project picker。
- settings。

对 Web Agent 的映射：

- 大 diff、长 output、完整 transcript 用 drawer 或 modal。
- 高频状态常驻左右栏和 composer。
- 低频配置进 settings。
- 不要把所有内容都挤进右侧 inspector。

## 页面布局比例

Desktop 建议：

```text
left rail expanded: 300px
left rail collapsed: 64px
center min: 720px, flex: 1
right panel docked: 360px
right panel mini: 48px
composer max width: 880-1040px
conversation content max width: 920-1080px
```

布局规则：

- center 永远优先拿剩余宽度。
- 左右都展开但窗口不足时，优先压缩右侧为 mini，其次左侧 collapsed。
- 左侧项目列表和右侧进度面板各自滚动，中间 conversation 独立滚动。
- composer 固定在 center 底部，宽度跟随 center，不跨到左右栏。
- 右侧 panel 默认不遮挡中间；只有 tablet/mobile 使用 overlay。

## 三个核心区域

### 1. 左侧金刚操作区

左侧不是单纯 session 列表，而是“工作对象 + 高频动作”的金刚操作区。它要像 Codex 左侧工作流入口一样，让用户快速切换工作区、会话和动作。

默认宽度：

- 展开：280-320px。
- 收起：56-64px。
- 可选 P1：支持拖拽 resize，范围 240-380px。

内容结构：

```text
Global Activity Actions
Project Search / Workspace Switcher
Project List
Expanded Project -> Session List
Footer Settings / Account
```

推荐信息结构：

```text
[collapse] [back] [forward]

新对话
搜索
已安排
插件

项目
  New project
  golang-claude-code
    修正 TUI 角色混淆       8 分
    澄清需求                 9 分
    几点了                  48 分
  xiaoan-jiaoyu
  free-AI-free-to...

设置
账户
```

#### Global Activity Actions

常驻动作：

- New Chat/New Session：打开新会话创建流程。
- Search：搜索项目、session、文件引用和工具记录。
- Scheduled/Background：查看已安排任务、后台任务、loop/goal。
- Plugins/MCP：查看插件、MCP、skills、tool availability。

交互闭环：

- 点击 New Session：打开 New Session modal，默认 cwd 为当前项目。
- 点击 Search：打开全局搜索 overlay，不改变当前 session。
- 点击 Scheduled：右侧打开 Tasks tab 或全屏任务页。
- 点击 Plugins：打开 plugins/settings overlay。

收起态：

- 只显示图标。
- hover tooltip 显示名称和快捷键。
- 有 pending 状态时在图标右上角显示小 badge。

#### Workspace Switcher

展示：

- 当前 workspace 名称。
- 当前 absolute cwd，超长省略，hover 显示完整路径。
- 当前 workspace 是否有效。
- 是否是 git repo。

动作：

- 点击打开 workspace popover。
- 从 recent workspaces 选择。
- 手动输入 cwd。
- 自动验证 cwd；不要把“验证”做成常驻主按钮。
- 切换 workspace 后刷新 session tree。

闭环逻辑：

1. 用户选择或输入 cwd。
2. 前端 debounce 调用 workspace validate。
3. 成功后设置 `selectedWorkspaceCwd`。
4. 拉取 `/agent/sessions?cwd=<selectedWorkspaceCwd>`。
5. New Session 默认挂到该 cwd。
6. 右侧 Context 同步显示该 cwd。

失败态：

- 路径不存在：不允许切换。
- 不是目录：不允许切换。
- 非 absolute path：提示用户输入绝对路径。
- remote mode 下高风险路径：提示确认或禁用。

Codex-like 体验要求：

- 左侧 workspace 区不应长期展示一个醒目的蓝色 `Validate/验证` 按钮。这个按钮会让工作台看起来像后台表单，而不是本地 agent 上下文选择器。
- recent workspace 点击即选择，并在后台校验；校验成功后刷新该 workspace 下的 session tree。
- 手动输入路径时显示轻量状态，而不是要求用户理解“验证”动作：
  - `checking`：正在检查路径。
  - `ready`：路径有效。
  - `not found`：路径不存在。
  - `not a directory`：不是目录。
  - `not absolute`：不是绝对路径。
  - `not a git repo`：可用但不是 git 仓库，使用低风险提示，不阻断。
- New Session modal 中保留 cwd 可编辑能力，但创建前必须做一次兜底校验；校验失败时停留在 modal 并显示 inline error。
- 页面只把 workspace 当作上下文展示，不把 validation 暴露成主要 CTA。

#### Project List

项目列表是一层 workspace 入口，不是简单 cwd chip。

每个 project row 展示：

- project/workspace icon。
- workspace name。
- active/running/pending/error 状态点。
- 可选最近更新时间。

点击 project：

1. 设置 `selectedWorkspaceCwd`。
2. 展开该项目的 session list。
3. 中间保持当前 session，除非当前 session 不属于该 workspace。
4. 如果当前 session 不属于该 workspace，则选择该 workspace 最近 session；没有 session 则显示 empty session state。

项目更多菜单：

- New session here。
- Rename alias，P1。
- Remove from recent。
- Reveal in Finder，P1。
- Copy cwd。

#### New Session

左侧保留主动作，但点击后打开轻量 popover/modal，不直接静默创建。

字段：

- `cwd`：默认当前 workspace，可修改。
- `title`：可选。
- `model`：默认当前 model。
- `permission_mode`：默认 ask。

闭环逻辑：

1. 用户点击 New Session。
2. 表单显示当前 workspace。
3. 用户确认。
4. 后端创建 session。
5. 左侧 session tree 插入新 session。
6. 中间切换到新 session 空态。
7. composer 自动聚焦。

#### Session List

项目下的二级会话列表必须轻量、高密度，接近 Codex 截图里的行式列表，而不是大卡片。

每个 session row：

- title，一行省略。
- relative time，右侧对齐。
- status icon：running、waiting permission、failed。
- 小标记：changed files、pending permission、background task。

规则：

- active session 用浅灰背景，不用大面积彩色卡。
- failed/pending 用小红/橙图标，不整行变红。
- row 高度保持 36-44px，方便扫描。
- session title 不展示整段 recap；recap 可在 hover tooltip 或中间 header 展示。

点击 session：

1. 设置 `selectedSessionId`。
2. 拉取 session detail。
3. 中间 conversation 切换。
4. 右侧 Progress/Files/Context 同步。
5. conversation 滚动到最新消息。

#### Quick Actions Grid

这就是“金刚操作区”的核心，不是普通按钮堆叠。

建议首屏 6-8 个高频动作：

- New Session
- Command Palette
- Attach
- Checkpoints
- Diff
- Trace
- Permissions
- Settings

规则：

- 图标 + 短文字。
- 收起态只显示图标，hover tooltip。
- 有状态的动作显示 badge，例如 pending permission、changed files、running tools。
- 点击动作必须落到明确区域：打开右侧 tab、打开底部 drawer、聚焦 composer 或弹出 modal。

#### Project -> Session Tree

结构：

```text
Project A
  Running session
  Waiting permission session
  Idle session
Project B
  Idle session
```

排序：

- running / waiting permission 优先。
- 最近更新时间倒序。
- pinned session 固定在项目顶部。

session item 显示：

- status。
- title。
- latest recap 或 last message preview。
- cwd 短路径。
- updated time。
- tool/error/permission badges。

收起态：

- 显示项目 icon。
- active session 用左侧高亮条或小圆点。
- waiting permission 用 warning badge。
- hover tooltip 展示 session title 和 cwd。

#### Left Rail 状态闭环

左侧支持 3 个展示状态：

```text
expanded -> collapsed -> hidden
```

expanded：

- 显示完整全局动作、项目、二级会话。
- 当前项目展开。

collapsed：

- 显示全局动作图标和项目图标。
- 不显示二级会话文本。
- 当前 session 通过项目图标旁 badge 表示。

hidden：

- 左侧完全让出宽度。
- center header 左侧显示 rail open button。
- 有 running/waiting session 时 open button 带 badge。

恢复逻辑：

- 点击 center header 的 rail icon 恢复上一次状态。
- `Cmd/Ctrl+B` 在 expanded/collapsed 之间切换。
- 长按或菜单可选择 hidden。

### 2. 中间最大对话区域

中间是主工作区，必须比左右两侧都大。它承载用户输入、assistant 回复、工具摘要和当前 session 的主要时间线。

结构：

```text
Session Header
Permission / Error / Running Banner
Conversation Scroll Pane
Composer Console
```

#### Session Header

展示：

- session title。
- project/workspace icon。
- status。
- cwd 短路径。
- turns/tools/pending/error badges。
- right panel toggle。
- left rail toggle。
- more menu。

规则：

- sticky 在 conversation pane 顶部。
- 不跟随外层页面滚动丢失。
- 点击 cwd 可打开 workspace detail。
- 右上角保留右侧 progress panel toggle，接近 Codex 截图右上按钮。
- header 高度控制在 48-56px，不抢主画布。

#### Conversation Scroll Pane

这是唯一的主消息滚动容器。

必须支持：

- 大量消息滚动。
- 流式 assistant delta。
- tool summary inline。
- permission request inline marker。
- error/retry marker。
- thinking 折叠块。

滚动闭环：

- 新消息流式到达时，如果用户在底部，自动跟随到底部。
- 如果用户已经向上查看历史，不强制抢滚动；显示 “Jump to latest”。
- composer 固定在底部，不遮挡最后一条消息。
- 选择历史 session 后滚动到最新消息。
- rewind 后滚动到恢复点附近。

长内容规则：

- 普通 assistant 消息可完整显示，但在 pane 内滚动。
- tool output 不在 conversation 里直接铺 500 行，只显示摘要。
- 长输出进入右侧 Output tab 或底部 drawer。
- diff 不嵌入消息正文，只显示 changed files summary 和入口。

#### Center-Only Focus Mode

当用户手动关闭左侧和右侧区域后，页面进入中间单栏专注模式。这个模式不是“隐藏信息”，而是把关键运行信息折叠进中间会话流和 composer。

触发：

- `leftPanelMode = hidden`
- `rightPanelMode = hidden`

布局：

```text
Top compact session bar
Centered conversation column
Inline realtime activity rows
Floating composer console
Mini edge handles for left/right restore
```

规则：

- 中间 conversation content max width 建议 920-1040px，居中显示。
- 左右面板隐藏后，不在两侧留下大空白，只保留非常轻的 edge restore handle。
- 顶部 compact bar 保留当前 session title、left/right restore icons、running/pending/error badge。
- 右侧原本的 Progress/Files/Permissions 关键信息以内联 activity row、change pill、permission pause card 形式出现在中间。
- 如果出现 `permission_requested`，不强制把右侧 panel 打开；先在中间显示 Permission Pause Card，并提供 `Review` 按钮。用户点击后再打开右侧或弹出 permission popover。

中间单栏必须仍然能看到：

- 已处理耗时。
- 已读取文件数。
- 已运行命令数。
- 正在读取/编辑的文件名。
- 当前文件增删行数。
- 本轮总改动摘要。
- composer 左下角 permission。
- composer 右下角 context/model。

验收：

- 左右都 hidden 后，用户不需要打开任何侧栏，也能知道 agent 是否在运行、读了几个文件、改了哪个文件、增删多少行。
- 出现权限阻塞时，中间和 composer 都能看到阻塞原因。
- 单栏模式下 composer 不遮挡最后一条实时 activity。

#### Conversation 内容组件

中间消息流建议使用这些组件，而不是只有 user/assistant 两类气泡：

1. User Message
   - 用户输入文本。
   - 附件缩略图。
   - 时间和复制按钮低权重展示。

2. Assistant Message
   - assistant 正文。
   - 支持 markdown/code block。
   - 段落宽度适中，不铺满全屏。

3. File Reference Card
   - 文件名。
   - 类型/路径。
   - 打开方式。
   - 来源：用户附件、Read tool、生成文件。

4. Change Summary Card
   - “已编辑 N 个文件”。
   - `+added -deleted`。
   - 文件列表。
   - Review、Undo/Revert、Open Diff。

5. Tool Summary Row
   - tool name。
   - running/succeeded/failed。
   - 简短 input/output。
   - 点击打开右侧 Output/Trace。

6. Permission Pause Card
   - 当前需要审批的工具。
   - 简短 command/request。
   - “在右侧处理”按钮。

7. Error Recovery Card
   - 错误摘要。
   - Retry、Open Output、Open Trace。

这些组件的目的：

- 用户在主画布能理解 agent 做了什么。
- 复杂证据不挤占对话。
- 每个摘要都有明确落点：右侧 tab 或底部 drawer。

#### Realtime Activity Rows

中间会话流需要实时展示 agent 过程，形式应接近 Codex 截图里的轻量灰色行，而不是厚重卡片。

活动行类型：

```text
已处理 1m 59s
已读取 2 个文件 已运行 1 条命令
正在编辑 web_agent_codex_product_architecture.md +217 -27
正在读取 web_agent_codex_product_architecture.md
已编辑 1 个文件
1 个文件已更改 +120 -0
```

Activity row 结构：

- 左侧 16px icon：clock/read/edit/bash/diff/check/error。
- 主文案：动词 + 对象 + 数量。
- 次要信息：文件名、耗时、命令名。
- 右侧 diff stat：`+added -deleted`，added 绿色，deleted 红色。
- 可选 action：Open、Review、Output。

状态规则：

- running：文案使用“正在...”，颜色为 muted text，icon 可轻微 animated。
- completed：文案使用“已...”，颜色仍保持 muted，不大面积绿色。
- failed：只将 icon 和错误关键字标红，不整行变红。
- changed file：文件名用 link blue，diff stat 固定在同一行右侧或紧跟文件名。

交互闭环：

- 点击 `已读取 N 个文件`：打开 Sources/Context 或弹出已读文件列表。
- 点击 `已运行 N 条命令`：打开 Output。
- 点击 `正在编辑 file +x -y`：打开 Files/Diff，并定位该文件。
- 点击 `1 个文件已更改 +x -y`：打开 review/diff drawer。
- hover 显示完整路径、tool id、耗时。

格式规范：

- 行高 28-34px。
- icon 和文字 baseline 对齐。
- activity row 不加独立背景，最多 hover 时出现浅灰背景。
- 多条连续 activity row 之间用 8-10px gap。
- activity row 与正文之间留 12-16px 垂直距离，不能贴得太近。

#### Processing Time Display

处理耗时是中间区的重要实时状态。

展示位置：

- turn running 时，显示在 assistant 消息开始前或当前 assistant block 顶部。
- turn 完成后保留一行 `已处理 4m 39s`。
- 如果用户滚动到历史 turn，每个 turn 都可以看到自己的处理耗时。

更新规则：

- 每秒更新一次即可。
- 后端有 turn start/end timestamp 时使用后端时间。
- 前端临时流式阶段可以用本地 timer，turn finished 后以后端 duration 校准。

视觉：

- muted gray text。
- 左侧 clock 或 terminal icon。
- 下方可有细分隔线，像截图里的轻量 divider。
- 不使用进度条，除非后续有明确 step total。

#### File Read/Edit Summary

中间区需要展示“正在读取和修改的文件”，但不能变成日志刷屏。

聚合规则：

- 连续 Read events 聚合成 `已读取 N 个文件`。
- 连续 Bash events 聚合成 `已运行 N 条命令`。
- 当前正在执行的 event 单独展示：`正在读取 file` / `正在编辑 file`。
- 同一文件多次编辑，更新同一行 diff stat，不重复生成多行。
- turn finished 后折叠为 summary：`已读取 N 个文件 已运行 M 条命令 已编辑 K 个文件`。

文件名规则：

- 默认显示 basename 或 repo-relative path。
- hover 显示完整 absolute path。
- 点击打开 Files tab 或 diff drawer。

diff stat 规则：

- 单文件：`file +34 -5`。
- 多文件：`K 个文件已更改 +120 -0`。
- added 绿色，deleted 红色。
- 0 deletion 显示 `-0`，保持对齐和可读性。

#### Change Pill Near Composer

当 composer 附近出现文件改动摘要时，使用轻量 pill：

```text
1 个文件已更改 +120 -0
```

用途：

- 在单栏模式下替代右侧 Files badge。
- 在用户正在输入时提示本轮已有改动。
- 点击直接打开 review/diff drawer。

视觉：

- pill 背景白色或极浅灰。
- border `#e5e7eb`。
- radius 999px，但高度控制在 28-32px。
- 不使用大阴影。
- 位置在 composer 上方居中或靠右，不遮挡正文。

#### Composer Console

Composer 不是聊天输入框，而是 agent 控制台。

结构：

```text
┌────────────────────────────────────────────────────────────┐
│ textarea                                                   │
│                                                            │
├────────────────────────────────────────────────────────────┤
│ + attach   permission mode v        context %  model v  ↑  │
└────────────────────────────────────────────────────────────┘
```

交互：

- `Cmd/Ctrl+Enter` 发送。
- running 时 Send 变 Cancel。
- waiting permission 时显示 paused 状态，并引导处理右侧 Permissions。
- permission mode 改变后影响下一轮 turn。
- bypass 需要二次确认。
- context meter 接近阈值时提示 compact 或 recap。

#### Composer 左下角：Permission Control

这是 Web Agent 相比 TUI 的核心价值之一：权限模式常驻可见、可点、可改。

展示文案：

- `Ask`：每次高风险工具前询问。
- `Allow`：允许当前 session 内较低风险操作。
- `Deny`：拒绝写入/执行类操作。
- `Full Access` 或 `Bypass`：高风险，必须橙/红色警示。

点击后打开 popover：

```text
Permission Mode
  Ask before writes and commands        selected
  Allow approved project actions
  Deny writes and shell commands
  Full access / bypass safeguards       danger

Scope
  This turn
  This session
  This project

Recent decisions
  Bash go test ./...     allowed once
```

闭环逻辑：

1. 用户点击 composer 左下角 permission。
2. 选择 mode/scope。
3. UI 立即更新待应用状态。
4. 下一次 turn request 带上 permission mode。
5. 如果选择 Full Access/Bypass，弹出确认，说明会影响本地命令和文件写入。
6. 右侧 Environment 同步显示当前 permission。

状态：

- waiting permission：左下角变成 `Permission required`，点击直接打开右侧 Permissions。
- denied：左下角显示 `Denied`，下一轮发送前提示当前权限会阻止操作。
- remote mode：左下角额外显示 remote warning icon。

#### Composer 右下角：Model + Context

展示：

- context usage：例如 `37%`，带圆环或细进度。
- model：例如 `gpt-5.5 high`。
- 可选 reasoning/intensity：low/medium/high。
- send/cancel button。

点击 context：

- 打开 Context popover。
- 显示 input tokens、output tokens、cache read/create、max context、当前 session turns。
- 提供 `/compact`、recap、clear transient context 操作。

点击 model：

- 打开 Model popover。
- 显示当前 provider/model。
- 可选模型列表。
- reasoning effort。
- temperature/advanced settings 放二级。

闭环逻辑：

1. 用户发送前能看到本轮将使用的模型和上下文压力。
2. context 高于阈值时 composer 右下角变 warning。
3. 用户点击 context 后可直接 compact。
4. compact 成功后 context meter 降低，conversation 插入 compact summary marker。

#### Composer 附件和快捷动作

左侧 `+` 菜单：

- Attach file。
- Attach image。
- Paste from clipboard。
- Add workspace file path。
- Add command output，P1。

快捷 command chips 不应长期占据输入框上方空间。建议：

- 默认只保留 `+` 和 `/` command trigger。
- 用户输入 `/` 时出现 slash command menu。
- 常用命令可在 command palette 搜索。

#### Composer UI Format

Composer 要像 Codex 一样是轻量浮动输入控制台，不要做成表单面板。

尺寸：

- desktop max width：880-1040px。
- min height：120px，输入内容增多时最高到 40vh，再内部滚动。
- border radius：18-24px，允许 composer 比普通卡片更圆，但内部按钮仍保持克制。
- padding：顶部 16px，左右 18px，底部状态栏 12px。

视觉：

- 背景白色。
- border 使用浅灰。
- shadow 使用很轻的浮层阴影，只让 composer 从页面中浮起，不做厚重卡片。
- bottom status row 使用一行布局，不加分割线或只用极浅分割线。

底部左侧：

```text
[+]  [shield icon] 完全访问 v
```

- `+` 是 icon button，尺寸 28-32px。
- permission 使用 icon + text + chevron。
- 高风险 permission 使用橙色文字和 icon。
- permission label 不超过 5 个汉字或 2-3 个英文词，避免挤压输入区。

底部右侧：

```text
[context spinner/percent] [model 5.5] [effort 高 v] [send/cancel]
```

- context 是小圆环或短文字，hover 显示 token 详情。
- model 和 effort 是 compact controls。
- send 是黑色圆形按钮，running 时变 stop 方块。
- send/cancel 保持 36-40px，不随文案变宽。

状态颜色：

- normal：黑/灰。
- permission full access：橙。
- context warning：橙。
- failed：红点或小红 icon，不整条 composer 变红。
- running：send button 内部 icon 变 stop，context 可显示小 spinner。

交互：

- 点击 permission：打开 permission popover。
- 点击 model：打开 model popover。
- 点击 context：打开 context popover。
- 点击 `+`：打开 attachment menu。
- hover 每个 icon control 都有 tooltip。

键盘：

- Enter 默认换行。
- Cmd/Ctrl+Enter 发送。
- Esc 关闭当前 popover。
- 如果正在 running，Cmd/Ctrl+. 可 cancel，P1。

闭环逻辑：

1. 用户输入 prompt。
2. 发送后 composer 清空，turn state = running。
3. assistant message placeholder 出现。
4. SSE delta 更新 conversation。
5. tool events 同步进 conversation summary 和右侧 progress。
6. turn finished 后状态回 idle。
7. turn failed 后 composer 恢复输入，显示 retry/open trace。

#### Composer 状态表

| 状态 | 左下角 | 右下角 | 发送按钮 | 中间提示 | 右侧 |
| --- | --- | --- | --- | --- | --- |
| idle | 当前 permission | context + model | send | 无 | 保持当前 tab |
| typing | 当前 permission | context + model | send enabled | 无 | 保持当前 tab |
| running | 当前 permission | live context/model | cancel | running marker | Progress |
| waiting_permission | Permission required | model/context frozen | disabled 或 review | paused banner | Permissions |
| failed | 当前 permission | model/context | retry/send | error card | Output/Trace badge |
| cancelled | 当前 permission | model/context | send | cancelled marker | Progress done/cancelled |

### 3. 右侧实际进度和改动区域

右侧不是普通 inspector，而是“实际进度/改动区”。它回答：Agent 正在干什么，卡在哪里，改了哪些文件，有什么证据。

默认宽度：

- 展开：340-400px。
- 收起：0 或 48px mini rail。
- 可选 P1：支持拖拽 resize，范围 300-520px。

展示模式：

- Desktop：dock 在右侧，打开时参与布局，不遮挡中间对话。
- Tablet：overlay drawer。
- Mobile：bottom sheet。

Tabs：

```text
Progress | Files | Output | Permissions | Trace | Usage | Context
```

右侧默认视觉应接近截图中的“环境信息”卡：

```text
环境信息                         +
  变更                 +22,194 -5,507
  本地                 local
  分支                 codex/...
  提交或推送
  GitHub CLI           未认证

任务
  go run ./cmd/...

来源
  docs/...
```

区别是 Web Agent 需要把它产品化成可操作 sections，而不是静态信息。

#### Environment Summary

右侧顶部始终显示环境摘要：

- Changes：文件改动数量、插入/删除行。
- Runtime：local/remote/sandbox。
- Branch：当前 git branch。
- Permission：当前 permission mode。
- Model：当前 model。
- Commit/Push：git 状态和 GitHub CLI auth。

每一行都是可点击入口：

- Changes -> Files tab。
- Runtime -> Context tab。
- Branch -> Git section / Output。
- Permission -> Permissions tab 或 composer permission popover。
- Model -> Usage/Context。
- Commit/Push -> Git actions popover。

#### Progress Tab

默认 tab。展示当前 turn 的实际进度。

内容：

- 当前步骤：planning / reading / editing / testing / waiting permission / done / failed。
- tool timeline。
- 每个 tool 的状态、耗时、输入摘要、输出摘要。
- 当前 running tool 高亮。
- error step 提供 open output / open trace。

闭环逻辑：

- turn start 自动切到 Progress。
- tool_started 添加 running row。
- tool_result 更新 row 状态。
- error 自动标记并提供下一步动作。
- turn_finished 显示 done summary。

Progress item 结构：

```text
[running] Bash
  go test ./internal/server -count=1
  00:12 · streaming output

[done] Edit
  web/src/components/WebAgentWorkbench.tsx
  +214 -82

[failed] Bash
  npm --prefix web test
  exit 1 · open output
```

状态颜色：

- running：中性色 + spinner。
- done：绿色小点即可。
- failed：红色小点 + output action。
- waiting permission：橙色 + review action。

#### Files Tab

展示实际改动。

内容：

- changed files list。
- added/modified/deleted 状态。
- diff stat。
- 点击文件打开底部 diff drawer 或右侧 inline diff preview。

闭环逻辑：

- 有文件改动时右侧 Files 出 badge。
- 用户点击 changed file。
- 打开 diff drawer，定位该文件。
- drawer 关闭后仍回到 Files tab。

Files item 结构：

```text
M web/src/components/WebAgentWorkbench.tsx   +214 -82
A docs/web_agent/xxx.md                       +120 -0
D old/path.md                                 +0 -34
```

动作：

- Review。
- Open diff。
- Copy path。
- Revert file，P1，高风险需确认。
- Open in editor，P1。

#### Output Tab

展示长命令输出。

内容：

- 最近命令。
- stdout/stderr。
- exit code。
- copy output。
- open full output drawer。

闭环逻辑：

- Bash tool 开始后出现 output entry。
- 输出过长时右侧只显示 tail 和 expand。
- 失败时 Output tab 出 error badge。

Output 规则：

- 默认显示最近 command 的 tail。
- 每条 command 可展开。
- stdout/stderr 用 monospace。
- copy 和 open full drawer 常驻。
- 不在中间 conversation 直接显示完整 stdout。

#### Permissions Tab

展示待处理权限。

内容：

- tool name。
- command/request summary。
- risk。
- cwd。
- reason。
- allow once/session/project/global。
- deny。

闭环逻辑：

1. SSE 收到 permission_requested。
2. 右侧自动打开 Permissions。
3. 中间显示 paused banner。
4. composer 进入 waiting permission 状态。
5. 用户审批。
6. 后端 resolve。
7. banner 消失，turn 继续或失败。

Permission card 结构：

```text
Bash requires permission
cwd: /path/to/golang-cc
command: go test ./... -count=1
risk: executes local command

[Allow once] [Allow session] [Allow project] [Deny]
```

审批按钮规则：

- Allow once 是主按钮。
- Allow session/project 是次级按钮。
- Deny 清晰可见但不误触。
- Allow global 不默认展示，可放 more menu。

#### Trace Tab

展示证据链。

内容：

- session id。
- turn id。
- transcript path。
- SSE events。
- tool ids。
- request trace id。

用途：

- 调试 UI 和后端状态不一致。
- 给用户确认“这不是假数据”。

#### Usage Tab

展示：

- input tokens。
- output tokens。
- cache create/read。
- model。
- turns。
- context percent。

#### Context Tab

展示：

- cwd。
- prompt mode。
- model/provider。
- sandbox。
- capabilities。
- memory/skills/MCP 状态。

#### Sources Section

右侧需要有 Sources，不一定作为独立 tab，可以在 Context 或 Progress 下显示。

Sources 包含：

- 用户上传附件。
- 当前消息引用的文件。
- Read tool 读取过的文件。
- Memory/skills/MCP 注入来源。
- Web/search 来源，若后续支持。

闭环：

- 点击 source -> 中间定位相关引用卡，或打开详情。
- source 缺失时显示“暂无来源”，不要空白。

## 左右隐藏和展示逻辑

### 左侧 Rail Toggle

状态：

- `expanded`
- `collapsed`
- `hidden`，P1 可选

交互：

- 顶部或 session header 有左侧 toggle。
- `Cmd/Ctrl+B` 切换 expanded/collapsed。
- 用户状态写入 localStorage。
- 窄屏默认 hidden，通过按钮打开 drawer。

闭环：

- 收起后中间区域立即扩展。
- active workspace/session 仍有最小可识别状态。
- 有 pending permission 或 running session 时，收起态仍显示 badge。

### 左侧二级项目展开逻辑

规则：

- 同一时间默认只展开一个 active project，保持列表简洁。
- 用户手动 pin 的项目可保持展开，P1。
- 切换 session 时，如果 session 属于折叠项目，则自动展开该项目。
- 搜索时临时展示匹配项目和匹配 session，退出搜索恢复原展开状态。

状态保存：

- `leftPanelMode`
- `activeProjectCwd`
- `expandedProjectCwds`，P1。
- `sessionSearchQuery`

### 右侧 Work Progress Toggle

状态：

- `docked`
- `mini`
- `hidden`
- `overlay`，tablet/mobile

交互：

- session header 有右侧 toggle。
- `Cmd/Ctrl+I` 切换 docked/hidden。
- 点击 Progress/Files/Output/Permissions quick action 时自动打开右侧并切到对应 tab。
- 用户状态写入 localStorage。

闭环：

- 隐藏后中间区域扩展。
- 如果出现 permission_requested，右侧自动打开 Permissions，除非用户明确选择 “keep hidden for this turn”。
- 如果有 failed tool，右侧 mini rail 显示 error badge。
- turn finished 后不强制关闭，保留用户选择。

### 右侧自动打开优先级

右侧 panel 是否自动打开需要有优先级，避免打扰用户。

自动打开：

1. `permission_requested`：最高优先级，打开 Permissions。
2. `turn_started`：如果右侧未 hidden，切到 Progress。
3. `tool_failed`：打开或 mini badge 标红。
4. `files_changed`：Files tab 出 badge，不强制打开。

不自动打开：

- 普通 assistant delta。
- usage 更新。
- sources 更新。

用户覆盖：

- 如果用户明确隐藏右侧，本 turn 内只显示 mini badge。
- permission_requested 可以临时弹出轻量提示，用户点击后打开。

## 页面状态机

### Session State

```text
empty -> idle -> running -> waiting_permission -> running -> idle
                         -> failed
                         -> cancelled -> idle
```

UI 对应：

- empty：中间空态 + composer 聚焦。
- idle：可发送。
- running：send 变 cancel，右侧 Progress 活跃。
- waiting_permission：中间 banner + 右侧 Permissions + composer paused。
- failed：中间 error card + 右侧 Trace/Output 入口。
- cancelled：显示 cancelled marker，允许继续输入。

### Workspace State

```text
unset -> validating -> valid -> loading_sessions -> ready
                     -> invalid
```

UI 对应：

- unset：使用 server cwd。
- validating：workspace switcher 显示 loading。
- valid：更新 workspace 和 sessions。
- invalid：保留原 workspace，不切换。

### Panel State

```text
left: expanded | collapsed | hidden
right: docked | mini | hidden | overlay
drawer: closed | diff | output | checkpoints | transcript
```

规则：

- panel state 不影响真实任务运行。
- panel state 只影响展示，不改变 session/cwd/turn。
- 用户显式选择优先，但 permission 阻塞可以临时提升右侧显示优先级。

## 数据和 API 闭环

页面关键数据源：

- `/agent/status`
- `/agent/workspaces/recent`
- `/agent/workspaces/validate`，建议新增
- `/agent/sessions?cwd=...`
- `/agent/sessions`
- `/agent/sessions/:id`
- `/agent/sessions/:id/turns/stream`
- `/agent/sessions/:id/cancel`
- `/agent/sessions/:id/permissions/:request_id/resolve`
- `/agent/sessions/:id/trace`
- `/agent/sessions/:id/rewind-candidates`

前端禁止：

- 后端失败时伪造真实 session。
- cwd 不明确时创建 session。
- 把测试 seed 数据放进真实页面初始状态。

## 前端组件拆分建议

为了让交互闭环可维护，建议按区域拆组件，而不是继续把所有状态堆在单个 `WebAgentWorkbench`。

```text
WebAgentWorkbenchShell
  LeftActivityRail
    GlobalActivityActions
    ProjectSessionTree
    WorkspacePickerPopover
  CenterConversationWorkspace
    SessionHeader
    ConversationScrollPane
    ConversationEventCard
    AgentComposerConsole
      PermissionControl
      ModelContextControl
  RightProgressPanel
    EnvironmentSummary
    ProgressTimeline
    FilesPanel
    OutputPanel
    PermissionsPanel
    TracePanel
    UsagePanel
    ContextPanel
  GlobalOverlays
    CommandPalette
    DiffDrawer
    OutputDrawer
    NewSessionModal
```

关键状态归属：

- `selectedWorkspaceCwd`：Shell owns。
- `selectedSessionId`：Shell owns。
- `leftPanelMode`：Shell owns + localStorage。
- `rightPanelMode`：Shell owns + localStorage。
- `activeRightTab`：RightProgressPanel owns，Shell 可提升。
- `turnState`：Shell owns，由 SSE 更新。
- `permissionMode`：AgentComposerConsole owns draft，Shell/API owns committed。
- `model/context`：AgentComposerConsole 展示，数据来自 runtime/session usage。

这样拆的原因：

- 左侧只负责导航和选择，不处理 turn streaming。
- 中间只负责对话和输入，不直接管理 diff/output 长内容。
- 右侧只负责进度和证据，不改变用户输入。
- Shell 负责数据同步和跨区域状态。

## UI 状态字段建议

建议前端维护这些显式状态，避免 UI 通过散乱 boolean 推断：

```ts
type LeftPanelMode = "expanded" | "collapsed" | "hidden";
type RightPanelMode = "docked" | "mini" | "hidden" | "overlay";
type RightPanelTab = "progress" | "files" | "output" | "permissions" | "trace" | "usage" | "context";
type TurnState = "idle" | "typing" | "running" | "waiting_permission" | "failed" | "cancelled";
type WorkspaceState = "unset" | "validating" | "valid" | "loading_sessions" | "ready" | "invalid";
```

每个状态必须有明确 UI：

- `waiting_permission`：中间 paused banner、composer 左下角 Permission required、右侧 Permissions badge。
- `running`：composer send -> cancel、右侧 Progress running row、session row running indicator。
- `failed`：中间 error card、右侧 Output/Trace badge、session row failed icon。
- `invalid workspace`：workspace popover 显示错误，不切换 session list。

## Realtime Event Mapping

中间单栏和右侧面板都需要依赖同一套 turn event 归一化模型，避免 UI 各自推断。

建议前端将 SSE/tool/transcript 事件归一化为：

```ts
type ActivityEvent =
  | { type: "turn_started"; turnId: string; startedAt: string }
  | { type: "assistant_delta"; text: string }
  | { type: "file_read"; path: string; toolId?: string; durationMs?: number }
  | { type: "file_edit_started"; path: string; toolId?: string }
  | { type: "file_edit_finished"; path: string; added: number; deleted: number; toolId?: string }
  | { type: "command_started"; command: string; cwd: string; toolId?: string }
  | { type: "command_finished"; command: string; exitCode: number; durationMs?: number; toolId?: string }
  | { type: "permission_requested"; requestId: string; tool: string; summary: string }
  | { type: "turn_finished"; durationMs: number; usage?: WebAgentUsage }
  | { type: "turn_failed"; error: string; durationMs?: number };
```

映射到 UI：

- `turn_started` -> 显示处理耗时 timer，右侧 Progress running。
- `file_read` -> 更新 `已读取 N 个文件` 和 Sources。
- `file_edit_started` -> 显示 `正在编辑 file`。
- `file_edit_finished` -> 更新 `file +x -y` 和 change pill。
- `command_started` -> 更新 `已运行 N 条命令` 和 Output。
- `permission_requested` -> 中间 Permission Pause Card + composer Permission required。
- `turn_finished` -> 停止 timer，更新 context usage，生成 turn summary。
- `turn_failed` -> error recovery card + Output/Trace badge。

聚合数据结构：

```ts
type TurnActivitySummary = {
  durationMs: number;
  readFiles: string[];
  editedFiles: Array<{ path: string; added: number; deleted: number }>;
  commandCount: number;
  runningTool?: { name: string; target?: string; startedAt: string };
  pendingPermission?: { requestId: string; tool: string; summary: string };
  totalAdded: number;
  totalDeleted: number;
};
```

这个 summary 同时驱动：

- 中间 activity rows。
- 右侧 Progress/Files。
- composer 上方 change pill。
- session list 的 changed/pending/running badge。

## 响应式策略

Desktop >= 1200px：

- 左侧展开或收起。
- 中间最大。
- 右侧 docked/hidden。
- 底部 drawer 作为大内容阅读区。

Tablet 768-1199px：

- 左侧默认 collapsed 或 drawer。
- 右侧 overlay。
- 中间保持完整 composer 和 conversation。

Mobile < 768px：

- 单列。
- 左侧通过 workspace/session drawer 打开。
- 右侧 progress 通过 bottom sheet 打开。
- composer 固定底部。
- long output 进入 full-screen drawer。

## 视觉方向

使用开发工具的克制风格：

- 白底和浅灰背景。
- 少阴影，多分隔线。
- 8px 以内圆角。
- 高密度 session list。
- 状态色只用于状态：running、waiting、failed、changed、approved。
- 图标按钮必须有 tooltip。
- 不使用装饰性渐变、光球、营销 hero。

Codex-like 的重点不是颜色，而是：

- 信息层级稳定。
- 面板边界清晰。
- 对话和进度同时可见。
- 用户可以通过键盘和面板快速掌控任务。

### UI Formatting Rules

为避免后续实现变丑或变成 demo dashboard，统一这些格式规则：

整体：

- 背景：页面 `#ffffff` 或极浅灰，避免大面积蓝色/绿色底。
- 文本：主文字接近黑色，辅助文字使用中灰。
- 分隔：优先用 1px divider，不用厚阴影。
- 圆角：普通面板 8-12px，composer 18-24px，pill 999px。
- 阴影：只有 composer、popover、drawer 使用轻阴影；消息和 activity row 不使用厚阴影。

中间会话：

- 文本列宽限制，避免横向铺满大屏。
- activity row 使用浅灰文字，像系统旁注，不抢 assistant 正文。
- 文件名 link 使用统一蓝色。
- diff stat 统一：added 绿色，deleted 红色，数字不加背景。
- 处理耗时和工具统计使用同一 muted style。

卡片：

- 文件引用卡、改动卡可以有浅边框。
- 不使用嵌套卡片。
- 卡片内行高、icon 尺寸、按钮样式统一。
- “Review / Open / Output” 这类动作使用 compact button，不用大 CTA。

composer：

- 始终保持底部状态栏一行，不把 permission/model/context 做成多行表单。
- 左下 permission 和右下 model/context 是最重要的状态控件，不能隐藏进更多菜单。
- running/cancel 状态只改 send button 和 activity，不改变整体布局。

左右隐藏后：

- center-only 模式不能显得空；应通过 max-width、composer 居中、activity rows 和 edge handles 保持产品感。
- edge handles 要轻，不要做成明显悬浮按钮。
- 如果有 pending/error，edge handles 上可以有小 badge，但不抢主画布。

## 实施顺序

### Phase 1：布局骨架和面板状态

- 引入三栏 grid：left rail / center / right progress。
- 实现 left expanded/collapsed。
- 实现 right docked/hidden。
- 实现 center-only focus mode。
- localStorage 保存 panel state。
- 修复 conversation pane 独立滚动。

### Phase 2：左侧金刚操作区

- Workspace Switcher。
- New Session modal。
- Quick Actions Grid。
- Project -> Session tree。
- 收起态 badge 和 tooltip。

### Phase 3：右侧实际进度区

- Progress tab。
- Files tab。
- Output tab。
- Permissions tab 闭环。
- Trace/Usage/Context 基础信息。

### Phase 4：中间实时 Activity 和 Composer 控制台

- 处理耗时 `已处理 Xm Ys`。
- `已读取 N 个文件 已运行 M 条命令` 聚合行。
- `正在读取/正在编辑 file` 活动行。
- 单文件和多文件 diff stat。
- composer 上方 change pill。
- 三层 composer。
- running/cancel/waiting permission 状态。
- context meter。
- permission mode 二次确认。

### Phase 5：Command Palette 和快捷键

- action registry。
- keyboard navigation。
- toggle left/right。
- focus composer。
- open tabs/drawer。

### Phase 6：视觉和 E2E 验收

- 降低阴影和大色块。
- 提高列表密度。
- 统一 activity row、file link、diff stat、composer control 的格式。
- center-only 模式视觉回归。
- 桌面/tablet/mobile 截图回归。
- 长消息、长输出、权限阻塞、cwd 切换 E2E。

## 验收清单

- 左侧可展开/收起，收起后仍能识别 active workspace/session。
- 右侧可展示/隐藏，隐藏后中间对话区扩展。
- 左右都 hidden 后进入 center-only focus mode，中间居中且不留大空白。
- 中间对话区是最大区域，长消息可滚动查看。
- 中间能实时显示处理耗时。
- 中间能实时显示已读取文件数、已运行命令数。
- 中间能显示正在读取/编辑的文件名。
- 中间能显示单文件 `+added -deleted`。
- composer 附近能显示本轮总改动 pill，例如 `1 个文件已更改 +120 -0`。
- workspace 可选择 recent，也可手动输入 cwd。
- New Session 明确挂到当前 cwd。
- turn running 时右侧 Progress 实时更新。
- tool output 和 diff 有明确入口，不塞爆对话区。
- permission_requested 会打开右侧 Permissions，并让中间进入 paused 状态。
- 用户审批后 turn 能继续或明确失败。
- API 失败不显示假 session。
- panel 状态刷新页面后保持。
- mobile 下左/右区域变 drawer/bottom sheet，不挤压 composer。
- permission、model、context 三个 composer 底部控件始终可见、可点击。
- UI 不能使用厚重阴影、大面积状态色、卡片套卡片。

## 第一批最小闭环建议

第一批不要贪多，建议只做下面 7 件：

1. 固定三栏布局和左右 toggle。
2. 修复中间 conversation 独立滚动。
3. 左侧加入 Workspace Switcher 和 New Session modal。
4. 右侧做 Progress/Permissions/Files 三个核心 tab。
5. 移除真实页面 seed session，保证数据可信。
6. 实现 center-only focus mode 和中间 realtime activity rows。
7. 实现 composer 底部 permission/model/context 常驻控件和统一视觉。

这 7 件完成后，Web Agent 才会从“能看的工作台”进入“能实际操作的 Codex-like 产品页”。
