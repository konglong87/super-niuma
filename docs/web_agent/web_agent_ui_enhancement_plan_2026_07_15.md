# Web Agent UI 交互与质感增强执行方案

日期：2026-07-15
范围：`/webui/agent` 页面（`web/src/components/WebAgentPage.tsx` + `InspectorPanels.tsx` + `styles/app.css`）
目标：在既有 Codex-like 工作台骨架上，把「交互 / 实时交互 / 格局 / 质感」提升到成熟产品级，而不是重做布局。

## 一、现状盘点（先说清楚基线）

结构性问题在 2026-06-30 两份文档中已基本解决（`web_agent_ui_polish_audit`、`web_agent_quality_todo` 全部 DONE）：

- 三栏空间模型、右侧收起不占列、composer 单行策略：已完成。
- SSE 流式、打字机、乐观用户气泡、thinking placeholder、watchdog、idle timeout 兜底：已完成。
- Markdown-lite 渲染、context 占比、workspace 自动校验、evidence / capability-loop 展示：已完成。

也就是说，功能与结构已经就绪。**当前的差距恰好是本轮四个目标**，且都能在现有骨架上增量落地。

真实代码中确认的、可直接改进的点：

| 现象 | 位置 | 归属 |
| --- | --- | --- |
| composer 仍用原生 `<select>`（permission / prompt / effort） | `WebAgentPage.tsx:1069/1077/1099` | 质感 |
| 全站 light-only，无主题系统，色值硬编码 | `app.css:1-29` `color-scheme: light` | 质感 / 格局 |
| 右侧 Progress tab 直接堆叠原始 `text_delta` 事件 | `InspectorPanels.tsx` / `ProgressTab` | 实时交互 |
| web-agent 三栏为固定 grid，不可拖拽/记忆宽度（仅主 dashboard 侧栏可拖） | `app.css` `.web-agent-page` | 格局 / 交互 |
| 无命令面板、无消息级操作（复制/重试）、键盘交互仅限 composer | `WebAgentPage.tsx:835` | 交互 |
| 动效仅 4 个 keyframes（spin/caret/thinking-dot/message-enter），单一 radius(8px)，边框密集 | `app.css` | 质感 |

## 二、设计原则（贯穿全程）

1. **增量不重构**：沿用现有三栏与组件边界，只加 token 层、替换控件、增强事件呈现。
2. **token 先行**：所有新样式走 CSS 变量，禁止再堆硬编码色值，为主题化铺路。
3. **动效克制**：位移+透明度为主，`120-180ms`，一律尊重 `prefers-reduced-motion`。
4. **每阶段可验收**：先写验收标准（Playwright + 单测），再改代码，循环到通过。

## 三、分阶段执行方案

### Phase 0 — 设计 token 与动效地基（质感基座）

**做什么**
- 扩展 `:root` token：间距 scale（`--space-1..8`）、圆角 scale（`--radius-sm/md/lg/full`）、高度/阴影 scale（`--elevation-0..3`，仅浮层用阴影）、动效 token（`--dur-fast/base/slow` + `--ease-standard/emphasized`）。
- 收敛边框：默认更弱的 `--border`，去掉「每个小块都框一圈」的后台表单感。
- 中文字重整体降一级；建立标题/正文/说明三档字号节奏。
- 抽出一层可切换主题的语义 token（`--bg / --fg / --panel / --hairline` 等），当前先只落 light，为 Phase 4 dark 预留。

**验收**
- `npm --prefix web run build` 通过；web-agent 组件内无新增硬编码 hex。
- 视觉回归截图对比：无功能变化、边框噪音下降。

**成本**：中低（改 CSS + token 梳理）。这是后续所有阶段的前置。

### Phase 1 — 控件质感：替换原生 select（质感最高 ROI）

**做什么**
- permission mode / prompt mode(ask·code) → **segmented control**（分段按钮）。
- effort、model → **compact popover menu**（自定义下拉，键盘可达，`role=listbox` + roving tabindex + `aria-activedescendant`）。
- 统一 hover / focus-visible / active(press) 微交互与过渡；发送按钮加轻量按压反馈。

**验收**
- composer 内无原生 `<select>`；键盘（↑↓/Enter/Esc）可完成全部选择。
- `1180x860` 左右栏打开时 composer runtime 保持一行（沿用旧验收线）。
- Playwright：切换 permission/prompt/effort/model 后真实发送，值正确传到后端。
- `npm --prefix web test` 覆盖新控件选择逻辑。

**成本**：中。视觉与「像不像成熟产品」提升最直接。

### Phase 2 — 实时交互内核：Progress 从事件日志变成活动时间线

**做什么**（本轮重点）
- 右侧 `ProgressTab` 不再逐条堆 `text_delta`：合并连续 delta，改为**人类可读的活动时间线**——工具调用 chip（running/done/failed，带 lucide 图标 + 动态 spinner，已有 `spin`）、文件编辑（+/- 行数）、命令执行、阶段切换，附 live elapsed。
- composer 顶部/发送键旁增加 **实时阶段 pill**：`思考中 → 读取上下文 → 执行 <tool> → 生成回复`，绑定 SSE 事件流（复用现有 `summarizeActivity` / `buildToolActivities`）。
- context / token **动画计量条**（数值平滑过渡，非跳变）。
- 会话流中插入 **inline 工具 chip**（紧凑、可展开查看 payload），让「Agent 正在做什么」在主区可见，而非只在右栏。

**验收**
- 真实任务运行时，右侧首屏能看到「状态 + 最近 3-5 条可读步骤」，不再是 `text_delta ×N`。
- `scripts/web-agent-real-e2e.sh` 中出现工具 chip、阶段 pill 正确随事件切换。
- reduced-motion 下计量条/spinner 静态显示。
- 单测覆盖 delta 合并、工具活动聚合、阶段推导。

**成本**：中高（主要在事件聚合与呈现，逻辑已有基础）。

### Phase 3 — 交互深度（交互）

**做什么**
- **命令面板（⌘K / Ctrl+K）**：跳转 session、New Session、切换 workspace、切换 model、开合左右栏。轻量、无新依赖（自绘 + 现有 API）。
- **消息级操作**：assistant 气泡 hover 出现 复制 / 重试（重发）；键盘上下选中消息。
- **可拖拽 / 可记忆的三栏宽度**：复用主 dashboard 的 resizer + localStorage 模式（`App.tsx:542` 已有实现，抽成可复用 hook）。

**验收**
- ⌘K 打开面板，每个动作可用且键盘可完成。
- 复制/重试在 Playwright 中可验证；重试真实触发 continuation task。
- 拖拽后刷新页面宽度保持。

**成本**：中。命令面板和消息操作是「成熟感」的关键交互补齐。

### Phase 4 — 格局收尾 + 响应式 + 可选 dark（质感/格局）

**做什么**
- 会话区：正文最大可读宽度、user/assistant 容器层级更清晰、区块垂直节奏统一。
- 空态从 hero 改为 workbench-ready（沿用旧建议，标题 22-26px + 少量 compact suggestion）。
- Mobile：左右栏改 drawer / bottom-sheet + 遮罩，composer 收敛到 ≤190px。
- **（可选，需你拍板）dark 主题**：基于 Phase 0 语义 token，`prefers-color-scheme` + `[data-theme]` 手动切换。工作量取决于 `app.css` 硬编码清理程度，建议单独作为一档决定。

**验收**
- `1440x980`/`1180x860`/`390x844` 三视口无横向溢出、无换行退化。
- mobile 抽屉有清晰遮罩与关闭路径。
- （若做 dark）两套主题对比度达 WCAG AA，切换无闪烁。

**成本**：格局/响应式 中；dark 主题 中高（视 token 化彻底程度）。

## 四、统一验证清单（每阶段收尾必跑）

```bash
go test ./internal/server -count=1
npm --prefix web test
npm --prefix web run build
git diff --check
scripts/web-agent-real-e2e.sh        # 真实 provider 全生命周期
scripts/web-agent-scroll-smoke.sh    # 流式滚动/打字机压测
```

真实浏览器覆盖：`1440x980` 三栏 / 右栏收起 / `1180x860` 不换行 / `390x844` mobile；发送真实消息看 SSE、完成态、cancel、阶段 pill、工具 chip、右侧活动时间线。

## 五、建议顺序与理由

**推荐路径：Phase 0 → 1 → 2 →（3 与 4 视精力）**

- 0 是地基，必须先做，否则后续继续堆硬编码。
- 1 是**肉眼可见、投入产出最高**的质感跃升（原生 select 是当前最刺眼的「后台感」来源）。
- 2 是本轮「实时交互」的核心价值，也是与 TUI 拉开体验差的关键。
- 3/4 是深度与收尾，可按你时间取舍。dark 主题作为独立决策项。

## 六、本轮确定范围（2026-07-15 拍板）

已确认：

- **主轴：Phase 0 + Phase 1**（token/动效地基 + 原生 select 替换为自定义控件）。
- **dark 主题：仅预留语义 token，本轮只落 light**，不做双主题切换。
- **深度交互三项全做**（并入本轮）：
  - ⌘K / Ctrl+K 命令面板；
  - 消息级操作（assistant 气泡复制 / 重试）；
  - 三栏可拖拽 + 宽度记忆。
- **本轮不做**：Phase 2（右侧活动时间线 / 阶段 pill / inline 工具 chip）、Phase 4（响应式收尾 + dark 落地）。留待下一轮。

### 本轮执行顺序

1. **Phase 0** — token / 动效 / 语义层地基（dark token 预留不启用）。前置，低风险先落。
2. **Phase 1** — permission/prompt → segmented control；effort/model → popover menu；统一微交互。
3. **Phase 3a** — 三栏可拖拽 + localStorage 记忆（抽 `App.tsx:542` resizer 为可复用 hook）。
4. **Phase 3b** — assistant 消息 hover 复制 / 重试（重试走 continuation task）。
5. **Phase 3c** — ⌘K 命令面板（跳转 session / 新建 / 切 workspace·model / 开合面板）。

每步收尾跑第四节验证清单，并在关键视口做真实浏览器检查。
