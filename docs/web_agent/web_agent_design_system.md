# Web Agent 设计系统(设计契约)

适用范围:`/webui/agent` 页面(`web/src/components/WebAgentPage.tsx`、`AgentControls.tsx`、`AgentCommandPalette.tsx` 及 `app.css` 中 `.web-agent-page` 之后的样式段)。

**这份文档是改 UI 前必读的契约。样式真相永远在代码里(token 定义),这里只写决策和规则;两者冲突时以代码为准并更新本文。**

## 一、设计原则(5 条)

1. **单一强焦点**:任何视图同时只有一个高亮焦点(如左栏=当前会话)。其余状态一律降级为弱提示(中性底、图标着色)。
2. **元信息低语级**:时间戳、耗时、trace 等元信息用 muted 色、12px 以下、不加粗、中点 `·` 分隔,合并成一行,绝不使用胶囊/边框抬高视觉重量。
3. **阴影只给浮层**:popover、命令面板、抽屉才允许 elevation;平面内容用发丝边框(`--agent-border`)分区,禁止到处投影。
4. **动效克制且可关**:120–240ms(`--agent-dur-*` + `--agent-ease-*`),只做透明度/位移/缩放;每个新增动画必须同步加入 `prefers-reduced-motion: reduce` 块。
5. **克制用色**:accent 蓝只用于「当前/可交互强调」;状态色(success/warning/danger)只用于状态语义;正文永远是 ink/muted 灰阶。

## 二、Token 速查(语义 → 用途)

Token 全部定义在 `app.css` 的 `.web-agent-page {}` 块(light)与 `.web-agent-page[data-agent-theme="dark"] {}` 块(dark)。React 侧把解析后的主题盖章在 `<main data-agent-theme>` 上(用户选择持久化到 localStorage,默认跟随系统)。**新增语义 token 时必须同时补 light 和 dark 两个值**;web-agent 段内禁止出现裸 hex 色值——需要新颜色时先加语义 token。

| 类别 | Token | 用途 |
| --- | --- | --- |
| 文字 | `--agent-ink-strong` / `--agent-ink` / `--agent-text-secondary` / `--agent-muted` / `--agent-muted-soft` | 标题强调 / 正文 / 次级文字 / 元信息 / 占位与弱图标 |
| 表面 | `--agent-surface` / `--agent-panel` / `--agent-panel-soft` / `--agent-control` / `--agent-control-hover` / `--agent-control-active` | 卡片白底 / 侧栏底 / 分区底 / 控件底 / hover / 按下 |
| 边线 | `--agent-border-subtle` / `--agent-border` / `--agent-border-strong` | 极弱分隔 / 默认边框 / 强调边框 |
| 强调 | `--agent-accent` / `--agent-accent-strong` / `--agent-accent-soft` / `--agent-accent-border` / `--agent-on-accent` / `--agent-focus` | 蓝主色 / 深蓝(文字) / 浅蓝底 / 浅蓝边 / 蓝底上的文字 / 焦点环 |
| 状态 | `--agent-{success,warning,danger}` 各配 `-strong` `-border` `-soft`(danger 另有 `-deep`) | 状态点/文字 / 深一档文字 / 状态边框 / 状态浅底 |
| 遮罩 | `--agent-scrim` | 命令面板、移动端抽屉的全屏遮罩 |
| 圆角 | `--agent-radius-sm/md/lg/pill` | 6 / 9 / 14 / 999px |
| 阴影 | `--agent-elevation-1/2/3` | 轻浮起 / popover / 命令面板与抽屉 |
| 动效 | `--agent-dur-fast/base/slow` + `--agent-ease-standard/emphasized` | 120/160/240ms;标准/强调缓动 |

## 三、模式决策表(什么时候用什么)

| 场景 | 用法 |
| --- | --- |
| 选中态(列表/树) | 强焦点:`accent-soft` 底 + `accent-strong` 标题;树内选中另在引导线位置压 2px accent 色段。弱上下文:`panel-soft` 底 + 图标 accent 着色。**禁止** inset 边条贴行内侧(与圆角打架)。 |
| 状态徽标(running/ok/fail) | 8px 圆点 + 状态色;running 加呼吸动画。文字型状态用 `-soft` 底 + `-strong` 文字 + `-border` 边的小胶囊。 |
| 小型选择控件 | 2–3 个互斥选项 → `SegmentedControl`;更多选项 → `PopoverSelect`。**禁止**原生 `<select>`。 |
| 展开细节 | 原生 `<details>/<summary>`(如时间线 payload),不引入 JS 折叠状态。 |
| 空态 | 紧凑标题(20–26px)+ 一句说明 + 可点击示例 chip;禁止 hero 大标题。 |
| 消息页脚 | 一行元信息(时间 `·` 耗时 `·` 操作图标),操作按钮 22×20 无边框、hover 才显。 |
| 表格/代码块 | Markdown 表格外包 `overflow-x: auto` 圆角容器;代码块带语言标签 + 复制按钮头部条。 |
| 浮层 | popover 用 `elevation-2`,命令面板/抽屉用 `elevation-3` + `--agent-scrim` 遮罩,Esc/点遮罩可关,键盘可达(listbox/roving)。 |

## 四、组件原语(优先复用,勿重写样式)

- `SegmentedControl` / `PopoverSelect`(`AgentControls.tsx`):可达性齐全的选择控件。
- `CommandPalette`(`AgentCommandPalette.tsx`):⌘K 面板。
- `MarkdownCodeBlock`、时间线条目、消息页脚等模式见 `WebAgentPage.tsx` 内实现。

新增 UI 时:先找原语组合;原语不够先补原语,再在页面里使用。

## 五、守护与验收

- **裸 hex 检查**:web-agent 样式段(`.web-agent-page` 之后)不允许新增裸 hex/rgb 色值;色值只能出现在 token 定义块。
- 每次 UI 改动的固定验收:`npm --prefix web test`、`npm --prefix web run build`、关键视口(1440 / 1180 / 390)真机截图、reduced-motion 检查。
- 历史决策与分阶段计划见 `web_agent_ui_enhancement_plan_2026_07_15.md`。
