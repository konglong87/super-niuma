# Web Agent UI Polish Audit

本文档记录 2026-06-30 对 `/webui/agent` 当前页面的真实浏览器审查结果。目标是先把“哪里丑、为什么丑、应该怎么改、怎么验收”说清楚，再进入实现，避免继续做零散视觉补丁。

## 审查范围

- 页面：`http://127.0.0.1:18112/webui/agent?token=test-token`
- 会话：`Overall Web Agent UI polish`
- 代码路径：
  - `web/src/components/WebAgentPage.tsx`
  - `web/src/styles/app.css`
- 浏览器验证：
  - desktop `1440x980`
  - desktop narrow `1180x860`
  - desktop right collapsed `1440x980`
  - mobile `390x844`
- 验证结论：
  - 页面可打开。
  - desktop/mobile 均无 console error。
  - desktop/mobile 均无 horizontal overflow。
  - 但窄 desktop 三栏打开时 composer 控制行换成两行。
  - 右侧隐藏后仍占用 48px 空列。

## 截图证据

- Desktop 三栏打开：`docs/web_agent/images/ui-audit-desktop-open.png`
- Desktop 三栏窄宽：`docs/web_agent/images/ui-audit-desktop-narrow-open.png`
- Desktop 右侧收起：`docs/web_agent/images/ui-audit-desktop-right-collapsed.png`
- Mobile：`docs/web_agent/images/ui-audit-mobile.png`

## 本轮落地结果

2026-06-30 已先落地高 ROI 的 UI polish：

- 右侧详情收起后不再保留 48px 空列，`right-collapsed` 下右侧 pane `display:none`，主区释放到右边界。
- `1180x860` 左右栏同时打开时，composer runtime 从两行压回一行。
- 左侧 workspace rail 去掉常驻 cwd 输入和“验证”表单，改为轻量 current workspace + `切换/Change` 入口。
- 用户发送后立即出现本地 pending 用户气泡，不再等 SSE 后突然插入。
- 用户消息下方增加 assistant thinking placeholder，显示 Agent 正在工作，并随发送/连接/工具阶段切换文案。
- Mobile composer 从 `242px` 压缩到 `167px`，无横向溢出。

最终验证截图：

- Right collapsed：`docs/web_agent/images/ui-polish-final-right-collapsed.png`
- Narrow desktop：`docs/web_agent/images/ui-polish-final-narrow-open.png`
- Mobile：`docs/web_agent/images/ui-polish-final-mobile.png`

## 总体判断

当前页面已经从“普通 WebUI 页面”进化到“Web Agent 工作台雏形”，但还没有达到 Codex 类产品的简洁密度和空间控制。核心问题不是颜色不够好看，而是三个结构性问题：

1. 左侧 rail 信息层级太重，像表单和管理后台，不像高密度项目导航。
2. 右侧 panel 收起后仍作为一列参与布局，视觉上像一条空白废列。
3. composer 的运行控制项没有稳定的一行策略，在三栏打开时会换行，压低主区高度，显得拥挤。

下一轮应优先做结构和密度改造，再做细节颜色、圆角、hover 等视觉润色。

## P0 问题

### 1. 右侧隐藏后仍占 48px 空列

现象：

- 点击隐藏右侧详情后，页面仍保留一条 48px 宽的右侧窄栏。
- 这条窄栏只有一个箭头按钮，上下都是空白。
- 用户视觉上会觉得“右侧没隐藏干净”，而且浪费主工作区宽度。

实测数据：

- `1440x980`，右侧收起后：
  - left: `296px`
  - center: `1096px`
  - right: `48px`

代码来源：

- `web/src/styles/app.css`
  - `.web-agent-page.right-collapsed { grid-template-columns: 296px minmax(0, 1fr) 48px; }`
- `web/src/components/WebAgentPage.tsx`
  - right panel 收起后仍渲染 `<aside className="web-agent-right">` 和 header button。

建议：

- desktop 收起右侧时不要保留布局列，grid 应变为 `left + center`。
- 把右侧展开入口放到中间 header 的 `PanelRight` icon 上。
- 如果一定要保留“可展开把手”，应做成悬浮 icon button，贴在右边缘，不参与 grid 列宽。
- center-only 以外也应允许 `right-collapsed` 释放全部右侧空间。

验收：

- 右侧收起后 `.web-agent-right` 不占布局宽度，主区右边直接到 viewport 边界。
- 右侧展开入口仍可见，但不形成整高空白列。
- Playwright 验证 right collapsed 状态下 right visible width 为 `0` 或仅悬浮按钮宽度不参与布局。

### 2. 三栏打开时 composer 控制行换成两行

现象：

- 左右侧栏都打开时，中间列变窄。
- bottom composer 的 runtime control 在 `1180x860` 下变成两行。
- 信息明明可以容下，但因为控件宽度、gap、文字、select 都按普通表单设计，导致视觉上拥挤和断裂。

实测数据：

- `1180x860`，左右栏打开：
  - left: `296px`
  - center: `532px`
  - right: `352px`
  - composer runtime: `438px x 76px`
  - left controls: `345px x 30px`
  - right controls: `305px x 34px`
- runtime 总宽无法在 `438px` 内一行容纳，最终上下两行。

代码来源：

- `web/src/styles/app.css`
  - `.agent-composer-runtime { display: flex; justify-content: space-between; flex-wrap: wrap; gap: 12px; }`
  - `.composer-left-controls` 和 `.composer-right-controls` 都是内容宽度优先。

建议：

- composer runtime 改成 Codex-like 单行控制台：
  - 左侧：permission icon + mode + cwd scope。
  - 中间：prompt mode segmented control 或 compact select。
  - 右侧：context/model/effort/send。
- 在三栏打开且 center 小于阈值时，自动进入 compact mode：
  - 隐藏低价值文字，例如 `Context` 只保留 `0%`。
  - `workspace` 改成 folder icon + tooltip。
  - `gpt-5.5` 和 effort 合并为一个 model pill 或 popover。
  - `running/ready` 状态移到 send button 左侧的小 dot 或 header activity strip。
- 控制行必须有单行优先策略；只有 mobile 才允许垂直堆叠。

验收：

- `1180x860` 左右栏打开时，composer runtime 保持一行。
- `1440x980` 左右栏打开时，composer runtime 不显得稀疏或断裂。
- `390x844` mobile 允许分组换行，但每组边界清楚，不像随机折行。

### 3. 左侧导航不像 Codex：层级过重，表单感强

现象：

- 左侧顶部有返回、标题、语言切换，首屏占用大。
- quick actions 每个按钮高度 36px，文字偏粗，和项目/session 列表层级接近。
- workspace 区域展示 current card、cwd input、验证按钮、filter 输入框，像管理后台配置表单。
- session 列表本身反而被挤到下方，信息容量不够。

与 Codex-like 目标的差距：

- Codex 左侧更像“项目和任务列表”，常用动作轻量，表单不常驻。
- 当前页面把 cwd 验证作为常驻表单放在 rail 里，占用了太多导航空间。
- active session 用白卡 + 蓝色左线还可以，但 project row 的蓝底和 workspace current card 让层级重复。

建议：

- 左侧改为三段：
  - 顶部 compact toolbar：collapse、brand、new session、search。
  - 项目列表：project name + active count + compact session rows。
  - 底部 settings/language/back to WebUI。
- cwd 输入和验证不要常驻在左侧：
  - 放进 New Session modal。
  - 或做成 workspace popover。
  - 左侧只显示当前 workspace 的 name/path 摘要。
- quick actions 从五个大文本按钮降级为 icon list 或 compact command rows：
  - New Session 保留主入口。
  - Search 用搜索框或 icon。
  - Refresh 放到列表 header 右侧小 icon。
  - Permissions 放到右侧 inspector 或 header 状态。
  - Main WebUI 放到底部。
- session row 提升信息密度：
  - title 一行。
  - status/time 一行。
  - active 用 2px left bar。
  - 去掉大面积 card/panel 视觉。

验收：

- `296px` 左栏下至少能舒适显示 8-12 条 session。
- 左侧首屏不再出现常驻 cwd 输入框和验证按钮。
- 用户仍能一眼看到当前 workspace 和当前 session。

## P1 问题

### 4. 中间空态过大，像宣传页而不是工作台

现象：

- 空态 icon 和标题居中占据大量空间。
- 标题“我们该构建什么？”在三栏工作台里仍偏 hero 化。
- 当左右栏打开时，中间主区已经很窄，大标题会进一步放大“空”和“粗糙”的感觉。

建议：

- 空态改成更接近 Codex 的起始工作区：
  - 标题降到 22-26px。
  - 增加 2-3 个 compact suggestion rows，但不要做大卡片。
  - 与 composer 形成视觉连接，让用户知道下一步是在底部输入。
- 当 session 已存在但没有消息时，空态应更像“ready prompt”，不是 onboarding hero。

验收：

- 三栏打开时空态不抢主视觉。
- 中文和英文标题都不会像落地页 headline。

### 5. 右侧 inspector 信息密度低，字段像日志堆叠

现象：

- 右侧详情 header 高度 64px，tabs 高度 50px 左右。
- Progress tab 中每个字段独占一块大行，分隔线明显。
- TRACE 一整串 UUID 直接显示在主层级，压过 status/model/mode。

建议：

- inspector header 更 compact：
  - 标题 14px。
  - collapse icon 靠右。
  - tabs 降到 30-32px。
- Progress 改成 definition list 或 2-column compact facts：
  - Status / Mode / Model / Started。
  - Trace ID 默认截断，中间可复制。
- 运行事件流比静态字段更重要，Progress tab 应优先展示最近事件，而不是只展示固定字段。

验收：

- 右侧首屏能同时看到 status、mode、model、started、最近 3-5 条事件。
- Trace ID 不再把右侧视觉重心拉歪。

### 6. 顶部 header 信息分散，左右 panel 控制重复

现象：

- 左侧 rail 自己有 collapse button。
- 中间 header 又有 left panel icon。
- 右侧 panel 自己有 collapse button。
- 右侧收起后还有独立箭头列。

建议：

- desktop 统一在 center header 放 pane controls：
  - left rail toggle
  - right inspector toggle
  - optional layout preset
- panel 内部只保留必要关闭按钮，收起后不占列。
- 左侧顶部不需要同时出现返回箭头和 center header left toggle，保留一个主模型即可。

验收：

- 用户能明确知道哪个 icon 控制哪个 pane。
- 同一个 pane 不出现 2-3 个等价开关。

### 7. 用户消息发送后突然插入会话区，过渡生硬

现象：

- 用户点击发送后，composer 里的文字被清空。
- 等后端事件返回后，用户消息气泡突然出现在 conversation 区域。
- 这个过程没有 pending 状态、入场过渡、滚动缓冲或视觉连续性，用户会感觉消息是“跳出来”的。

代码来源：

- `web/src/components/WebAgentPage.tsx`
  - `handleSendForTask` 调用 `sendAgentTaskMessage` 后清空 `composerText`。
  - 用户消息主要通过 `conversationTimelineEvents` 和 `buildConversationMessages` 从事件流重建。
  - 当前没有本地 optimistic message 状态，也没有专门的 sent/pending transition class。
- `web/src/styles/app.css`
  - `.agent-message` 和 `.agent-message-bubble` 是静态布局。
  - 只有 assistant live typewriter caret，没有 user message enter transition。

建议：

- 增加 optimistic user message：
  - 点击发送后立即在本地 timeline 追加一条 `pending` user message。
  - 气泡显示轻量 pending 状态，例如右下角小 spinner 或 `sending...`。
  - 后端真实 `message` event 到达后用 event id 替换 pending message，避免重复显示。
  - 失败时 pending 气泡变成 failed 状态，支持 retry。
- 增加自然入场动效：
  - user bubble 从 composer 上方轻微 `translateY(8px)` + `opacity 0` 过渡到最终位置。
  - 动效控制在 `140-180ms`，只做位移和透明度，避免花哨。
  - 尊重 `prefers-reduced-motion`，系统减少动效时直接显示。
- 发送后滚动更柔和：
  - 如果用户在底部，使用 `scrollToLatest("smooth")`。
  - 如果用户正在看历史，不强行跳底，只显示 `Jump to latest`。
  - 插入 pending message 前预留底部空间，避免气泡和 composer 贴得太近。
- assistant 响应前增加轻量状态：
  - user pending 确认后，在下方显示一行 compact `Agent is working...` 或流状态 pill。
  - 不要用大卡片，避免进一步占空间。

验收：

- 点击发送后 100ms 内用户能看到自己的 pending 消息，不需要等 SSE 返回。
- 后端事件返回后 pending 消息不会重复，状态平滑变成 sent。
- 网络失败时用户消息保留在 timeline 并显示 failed/retry，而不是直接消失。
- `prefers-reduced-motion: reduce` 下没有明显动画。
- Playwright 通过真实发送消息验证：发送后立即出现 pending 气泡，SSE 完成后气泡状态正确，console 无 error。

### 8. 缺少 AI 正在工作的 thinking placeholder

现象：

- 用户发送消息后，页面只看到顶部或局部状态，例如“已处理 16s”“正在思考”。
- 主会话时间线里没有一个稳定的 AI 占位气泡承接用户消息。
- 用户感知上会出现断层：我发完了，但 AI 到底是否收到、是否在思考、是否在执行工具，不够直观。

参考目标：

- 类似 Codex 的“正在思考”行或 AI placeholder bubble。
- 不是大 loading 卡片，而是轻量、低对比、贴在 assistant 消息位置的时间线元素。
- 它应该让用户明确知道 agent 正在工作，同时不打断阅读。

建议：

- 在 optimistic user message 之后立即渲染一个 assistant placeholder：
  - 状态文案可按阶段变化：`正在思考`、`正在读取上下文`、`正在执行命令`、`正在生成回复`。
  - 左侧可用小圆点 / bot icon / spinner，右侧显示 elapsed，例如 `16s`。
  - 用三点 pulse 或细微 shimmer 表示活跃，但不要大面积骨架屏。
- placeholder 与事件流绑定：
  - 刚发送后：`thinking`。
  - 收到 `tool` / `command` / `file` 事件后：切换成更具体的运行状态。
  - 收到第一段 assistant `text_delta` 后：placeholder 平滑替换为真实 assistant message。
  - 任务完成但无 assistant 文本时：placeholder 变成 compact completed status，而不是突然消失。
  - cancel/fail 时：placeholder 变成 cancelled/failed 状态，并保留错误摘要。
- 与右侧 Progress 配合：
  - timeline 只显示一句 human-readable 状态。
  - 详细 event stream 仍在右侧 inspector。
  - 不要把完整工具日志塞进 thinking bubble。
- 动效原则：
  - placeholder 入场跟 user bubble 同一套 `140-180ms` transition。
  - 三点 pulse 频率慢一点，避免焦虑感。
  - 尊重 `prefers-reduced-motion`。

验收：

- 点击发送后 100ms 内，用户消息下方出现 assistant thinking placeholder。
- placeholder 会根据 SSE 事件从 `正在思考` 进入更具体状态。
- 第一段 assistant 文本到达时，placeholder 不闪烁、不重复，平滑变成 assistant message。
- cancel/fail/completed-without-text 都有明确状态，不留下无限 loading。
- Playwright 真实发送消息验证：pending user bubble、thinking placeholder、assistant delta、completed/cancel 状态全链路可见。

## P2 问题

### 9. 视觉系统仍有“后台表单感”

表现：

- 边框过多：workspace current、input、filter、tabs、detail row、textarea 都是类似边框。
- select 控件偏原生表单。
- button 字重偏粗，中文下显得更重。
- textarea 有阴影，和“白底/浅灰/少阴影”的目标不完全一致。

建议：

- 建立 Web Agent 专用 tokens：
  - surface: `#ffffff`
  - rail: `#f7f7f4` 或更中性的浅灰
  - border: 更弱的 `#e8e8e3`
  - text: 降低大面积深蓝感
  - shadow: 默认无阴影，只在 modal/popover 使用
- select 尽量改成 segmented control、icon button、popover menu。
- 中文 UI 字重整体降低一级，避免所有控件都像 bold。

验收：

- 页面第一眼更像工具，不像后台管理表单。
- 截图中不再有多个同权重边框框住每个小区域。

### 10. Mobile 可用但不像最终形态

现象：

- `390x844` 无横向溢出。
- composer 高度达到 `250px`，占据接近 30% viewport。
- right/left 都隐藏后，header 仍保留两个 panel icons，但实际打开方式需要更清晰。

建议：

- mobile composer 收敛：
  - textarea 64-72px。
  - runtime controls 使用水平滚动 chips。
  - send button 固定在右下。
- left/right panel 在 mobile 上改为 drawer/bottom sheet，并增加遮罩。
- header title 在 mobile 下只显示 session title，cwd 进入 tooltip/popover。

验收：

- `390x844` 下 composer 不超过 190px。
- mobile 上展开 left/right 后有清晰遮罩和关闭路径。

## 建议实施顺序

### Phase 1：空间模型和最丑问题

1. 右侧收起释放 48px 空列。
2. composer runtime 一行 compact 策略。
3. 删除左侧常驻 cwd 验证表单，迁移到 modal/popover。

这三项能最快解决用户截图里最刺眼的问题。

### Phase 2：Codex-like 左侧 rail

1. 重排左侧信息架构。
2. 提升 session list 密度。
3. 将语言、Back to WebUI、settings 类低频入口移到底部或菜单。

### Phase 3：Inspector 和空态精修

1. 右侧 Progress tab 改成 compact facts + recent events。
2. 用户消息发送增加 optimistic pending 状态和自然入场动效。
3. 增加 assistant thinking placeholder，并绑定 SSE 阶段状态。
4. 空态从 hero 改成 workbench ready state。
5. 统一 header/pane controls。

### Phase 4：视觉 token 和响应式收尾

1. 减少边框和阴影。
2. 降低中文字重。
3. mobile composer 和 drawer 验收。

## 后续实现必须验证

每轮 UI 改动后至少验证：

```bash
go test ./internal/server -count=1
go test ./... -count=1
npm --prefix web test
npm --prefix web run build
git diff --check
```

还要用 Playwright 真浏览器验证：

- `1440x980` 左右栏打开。
- `1440x980` 右栏收起。
- `1180x860` 左右栏打开，composer 不换行。
- `390x844` mobile，无横向溢出。
- 创建 chat mode session。
- 创建 code mode session。
- 发送真实消息，验证 SSE、完成态、cancel 状态、右侧 Progress mode 和事件流。
- 发送消息后立即出现 pending 用户气泡，真实事件返回后不重复、不跳动。
- 用户消息下方立即出现 assistant thinking placeholder，并在 assistant delta 到达后平滑替换。

## 本轮不建议马上做的事

- 不建议先调颜色、圆角、阴影。结构问题不解决，调色只会掩盖问题。
- 不建议继续增加底部卡片或更多 tabs。当前首要矛盾是密度和空间。
- 不建议照搬 Codex 像素。应借鉴 Codex 的信息架构和 pane 行为，而不是复制皮肤。
