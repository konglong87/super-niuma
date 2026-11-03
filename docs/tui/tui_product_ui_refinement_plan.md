# TUI Product UI Refinement Plan

## 背景

2026-07-13 对比了两张真实截图：

- go-claude TUI：左侧截图中，回复内容以 Markdown 表格为主，顶部只有绿色 `Go Claude` 标识，正文、表格、状态之间的视觉层级较接近。
- Claude Code TUI：右侧截图中，顶部有品牌、版本、模型、上下文和目录信息；用户输入以灰色整行背景区隔；助手回复主要依靠标题、列表、缩进、弱状态文本和链接色形成层级。

本方案目标不是简单复刻 Claude Code，而是把 go-claude TUI 从“可用的终端输出”继续提升为“高级、大方、简洁的 agent workbench”。优化优先级以 ROI 为准：先做低风险、低侵入、用户可见收益高的显示层改造，再考虑更大范围的 renderer 架构调整。

## 本轮非目标

用户明确要求：先不做“降低表格边框亮度”。

因此本方案不把表格边框颜色变暗作为实施项。后续如果要继续处理表格，只讨论表格内容组织、长文本降级策略、中文宽度和响应式布局，不在本轮改表格线颜色。

如果本方案和 `docs/tui/tui_markdown_readability_optimization_plan.md` 中关于表格线颜色的建议存在执行顺序冲突，以本方案为准：本轮先不调整表格边框亮度。

同时本方案不改变：

- query runtime 行为；
- 模型调用、prompt、工具执行和权限策略；
- transcript 持久化格式；
- terminal 原生复制默认行为；
- mouse tracking 默认关闭策略；
- bottom chrome 的预算约束和遮挡保护。

## 截图差异分析

### 1. 信息架构

go-claude 当前更像把回复内容直接渲染到终端：品牌、运行环境、输入、输出和状态信息的边界不够稳定。截图中可见的主要视觉焦点是亮色标题和大表格。

Claude Code 更像一个固定产品界面：顶部身份区、用户输入条、助手回复、耗时状态和底部输入区各自有明确位置。用户能快速知道当前模型、目录、刚问了什么、助手回答到哪里、下一步在哪里输入。

### 2. 视觉重量

go-claude 的强视觉元素集中在标题和表格结构上，容易让内容显得“日志化”或“调试输出化”。当 AI 回复含有长中文、代码片段、emoji 和多列说明时，视觉密度会迅速升高。

Claude Code 的视觉重量更克制：状态信息使用低对比灰色，正文更突出，路径和链接用淡色强调，用户输入用背景区隔而不是靠大标题强调。

### 3. 阅读节奏

go-claude 截图里，表格把内容切成了规则网格，但中文长说明在单元格内换行后，阅读顺序不够自然。

Claude Code 截图里，助手回复以自然段、编号列表和短 bullet 为主，信息可以纵向扫描。它不是靠复杂框线表达结构，而是靠缩进、空行、轻量标题和强调色表达结构。

### 4. 产品感

go-claude 已经具备 workbench 能力，但视觉上还可以更像“运行中的智能体产品”：顶部身份更稳定，用户消息更像 conversation event，助手回复更像阅读内容，状态栏更像辅助信息。

## 设计原则

1. 内容优先：用户首先看懂回答内容，其次才看状态、工具和运行元信息。
2. 低噪声：减少重复 runtime 字段、重复提示和强色块。
3. 轻分隔：优先使用缩进、空行、弱背景和语义标签，少用强框线。
4. 稳定骨架：header、transcript、bottom chrome、input 位置稳定，避免每轮消息导致布局跳动。
5. 宽度友好：所有文案、状态、路径和 CJK 内容必须在窄终端中可读，不溢出、不遮挡。
6. 眼见为实：TUI 显示改动必须用真实 PTY 和 Terminal 截图验收，不能只依赖 `Model.View()` 或 ANSI dump。

## 信息密度规则

产品感不只来自颜色，也来自“少显示但显示对”。TUI 默认态应控制信息密度：

- welcome 空闲态只展示身份、模型、目录和最少操作提示。
- active turn 只展示当前运行状态、必要耗时、关键工具状态和输入区。
- completed turn 降噪，避免把 usage、cache、runtime、session 状态全部长期留在首屏。
- 详细工具输入输出、完整 usage、session id、checkpoint、resume 等信息优先走展开、命令或 trace，不默认压在主阅读流。
- 权限、错误、失败恢复和模型中断属于高优先级信息，允许临时提高视觉权重。

## 优化方向

### 1. 固定顶部产品信息区

目标：让 TUI 第一屏和 transcript header 都有稳定身份感。

建议展示：

```text
Go Claude  vX.Y.Z
model: glm-5.1 · provider: custom/openai-compatible · mode: code · context: 1M
cwd: ~/projects/golang-cc
```

实施要点：

- 继续复用 `WelcomeInfo`，不新增 runtime 数据来源。
- 收敛 `headerView()` 和 `compactWelcomeView()` 的字段顺序，让宽屏和窄屏语义一致。
- 宽屏可保留 wordmark，但不应让 wordmark 占用过高首屏；窄屏保持 compact。
- 顶部信息只在 welcome/transcript header 中承担身份识别，不和 active turn 的 bottom status 重复展示同一批 runtime 字段。
- 首屏预算需要硬约束：宽屏 welcome header 最多约 5-6 行，窄屏 header 最多约 3-4 行。
- conversation/transcript header 不显示大型 wordmark，只保留轻量身份行，避免多轮对话中反复抢正文焦点。
- active turn 期间不重复大号 `Go Claude` 标题，品牌只保留为小面积 accent。

涉及代码：

- `internal/tui/app.go`：`headerView()`、`compactWelcomeView()`、`fixedHeaderView()`、`transcriptHeaderView()`。
- `internal/cli/cli.go`：继续由 `tuiWelcomeInfo()` 填充真实运行信息。

### 2. 用户输入整行区隔

目标：让用户消息像明确的 conversation event，而不是混在普通文本里。

建议效果：

```text
› 你感觉当前项目有啥可以优化的吗？？
```

或在支持背景色的终端中使用弱灰背景整行显示，但背景必须低对比，不能抢助手正文。

实施要点：

- 用户消息使用统一 marker，例如 `›`。
- 用户消息可以使用 dim gray background 或弱前景，但不要使用强品牌色。
- 多行用户输入保持整体缩进一致。
- 输入区 `Ask Go Claude` 仍然保留 textarea 现有行为，不改变快捷键和附件管理。
- 单行用户消息优先渲染成低对比整行 band，形成明确 event boundary。
- 多行用户消息第一行显示 marker，后续行和正文保持同一缩进，避免每行重复 marker。
- 如果终端背景色兼容性不好，降级为 dim marker + 弱分隔，不强制背景色。

涉及代码：

- `internal/tui/app.go`：用户消息 display segment 渲染、`inputBoxViewForBudget()`、`textarea` style 初始化。
- `internal/tui/app_test.go`：补用户消息多行、窄屏、CJK wrap 的快照/断言。

### 3. 助手输出阅读化

目标：让助手回复更像 Claude Code 的自然阅读输出，而不是大块结构化日志。

建议：

- 助手消息开头只需要轻量 role marker，例如 `●` 或弱色 `Go Claude`，不在同一轮里重复大型标题。
- 普通正文亮度高于状态信息。
- 编号列表、bullet、短标题优先用于承载结构。
- 文件路径、命令、链接使用淡蓝紫强调。
- `Cooked for ...`、tokens、tool summary 等放在低对比状态行。

实施要点：

- 保持 `renderMarkdownForWidth()` 的语义，不对模型文本做业务重写。
- 优先调 renderer 的布局和主题，不通过 prompt 强迫模型输出某种 UI 格式。
- 对过长 heading 降低视觉权重，避免每个小标题都像一级标题。

涉及代码：

- `internal/tui/app.go`：`renderMarkdown()`、`newAssistantMarkdownStyle()`、assistant/text display segment 渲染。
- `internal/tui/view_format.go`：继续复用 display width wrap helper。

### 4. 色彩语义收敛

目标：颜色少，但每种颜色都有明确语义。

建议语义：

| Token | 建议颜色 | 用途 |
| --- | --- | --- |
| body | `252` / `253` | 助手回答主体；不加背景、不默认 bold |
| muted | `240` / `241` / `242` | status、elapsed、usage、cwd、hint |
| secondary | `245` / `246` / `248` | 次要说明、recap、历史状态、弱标签 |
| accent | `42` 或更克制的绿色 | `Go Claude` 品牌、小面积成功状态 |
| link-path | `75` / `81` | 文件路径、链接、可跳转对象 |
| inline-code | 淡蓝紫或正文色 + 极弱背景 | 高频字段、命令、路径；不能切碎中文正文 |
| warning | `220` / `214` | 可恢复风险、注意事项 |
| error | `196` / `203` | 错误、权限风险、失败状态 |

约束：

- 不做单一绿色主题。
- 不让品牌绿覆盖正文。
- 不把路径、inline code、状态全部做成同一颜色。
- permission bypass/allow 仍需明显，不能过度弱化安全状态。
- 背景色只用于输入 band、selected item、permission choice 等少数明确交互状态，避免大面积色块。

涉及代码：

- `internal/tui/app.go`：`titleStyle`、`userStyle`、`assistantStyle`、`statusStyle`、`errorStyle`、`newAssistantMarkdownStyle()`。

### 5. Markdown 渲染优先级

目标：让 Markdown 内容更适合终端阅读。

建议优先级：

1. 自然段和列表优先。
2. 短表格保留表格。
3. 长说明表格未来可考虑降级为分组列表，但本轮不改表格边框颜色。
4. inline code 克制显示，避免把中文正文切碎。
5. fenced code block 保持原样，不能被格式化为普通文本。

可分阶段处理：

- P1：统一正文、标题、inline code、链接颜色和 wrap。
- P1：让长 heading 和长 list item 在窄屏稳定换行。
- P2：增加“宽表格/长说明表格”的可选降级策略，例如把每一行渲染为 `#4 CHANGELOG...` 的分组块。
- P2：为模型回复中的 todo/priority 表格提供更适合终端的阅读视图。

已有相关文档：

- `docs/tui/tui_markdown_readability_optimization_plan.md`

### 6. Bottom chrome 去重与克制

目标：底部只显示当前操作最需要的信息。

建议：

- welcome 空闲态：只显示 `status` 和操作提示，避免重复 header 中已有 runtime。
- active turn：显示 running status、耗时、tokens、必要工具状态。
- completed turn：状态降噪，避免长 usage 或历史状态挤压输入栏。
- 错误/权限/选择器出现时，允许临时进入 full chrome。

现有实现已有 render budget 和 `chromeFull` / `chromeCompact` / `chromeMinimal`，后续优化应继续沿用这个架构，不绕开预算机制。

涉及代码：

- `internal/tui/app.go`：`bottomChromePartsForBudget()`、`modeHintViewForBudget()`、`welcomeModeHintViewForBudget()`、`quietConversationModeHintViewForBudget()`、`usageViewForBudget()`。

## 推荐实施顺序

### P0：文档和金标样例

- 固化本方案。
- 准备一组真实 TUI UI goldens：中文长回复、列表、路径、inline code、工具状态、权限提示、底部输入栏。
- 明确本轮不做表格边框变暗。

### P1：低风险视觉骨架

1. 收敛 header 字段顺序和宽/窄屏一致性。
2. 用户消息改为轻量整行区隔。
3. 助手消息 role marker 降噪，避免重复大标题。
4. status/elapsed/usage 弱化为辅助层级。

### P1：阅读主题

1. 调整正文、标题、inline code、link 的默认 Markdown 主题。
2. 保留 `<span>` / `<font>` 自定义颜色支持。
3. 保留 fenced code block 原样显示。
4. 不改表格边框颜色。

### P2：结构化内容适配

1. 评估长说明表格降级为分组列表的策略。
2. 对 todo/priority/review 类回复提供更自然的终端阅读布局。
3. 把渲染 helper 进一步从 `internal/tui/app.go` 拆到专门文件，降低后续 UI 调整成本。

## 验收标准

### 单元测试

建议覆盖：

- header 宽屏和窄屏字段顺序稳定；
- 用户消息多行/CJK wrap 不溢出；
- assistant marker 不重复；
- bottom chrome 在 busy、ready、error、permission、picker 场景下不遮挡输入；
- Markdown inline code、link、heading、list、fenced code block 不破坏语义；
- custom span/font color 仍生效。

建议命令：

```bash
go test ./internal/tui ./internal/cli -run 'Welcome|ModeHint|ConversationLayout|RenderMarkdown|Input|Usage' -count=1
go test ./internal/tui -count=1
git diff --check
```

### 真实 PTY 验收

必须使用真实 TUI 运行和截图检查：

```bash
scripts/tui-visual-regression-sop.sh
TUI_VISUAL_SOP_FULL=1 scripts/tui-visual-regression-sop.sh
```

重点看：

- welcome header 是否稳定、不过度占屏；
- 用户输入条是否清楚；
- 助手长中文回复是否可读；
- bottom chrome 是否不覆盖最后一行回答；
- 输入栏是否始终可见；
- 原生复制是否仍可用；
- 多轮对话中不会重复出现大号 `Go Claude` 标题。
- 同一段中文长回复在 80、120、160 列宽下都能保持清晰层级。
- 路径密集、inline code 密集、工具执行、权限弹窗和错误恢复场景都需要截图确认。
- 截图第一视觉焦点必须是助手正文，而不是边框、标题、usage、runtime 或状态提示。

## 风险与回滚

### 风险

- 背景色在不同终端主题下可能过强或过弱。
- 用户消息整行背景可能与 textarea 当前行背景冲突。
- 过度降噪可能隐藏权限、错误和工具失败信号。
- 修改 Markdown 主题可能影响已有 rich inline color 兼容。
- header/bottom chrome 去重如果边界不清，可能让用户找不到 model/cwd/session 信息。

### 回滚策略

- 分批提交：header、用户消息、assistant markdown、bottom chrome 分开落地。
- 每批保留针对性测试和真实截图证据。
- 一旦出现输入遮挡、复制退化、权限风险不可见，优先回滚该批 UI 改动。
- runtime、权限、工具和 transcript 数据结构不作为视觉优化的改动对象。

## 结论

最高 ROI 的方向是先建立稳定产品骨架：顶部身份清晰、用户消息有区隔、助手回复阅读化、状态信息降噪、颜色语义收敛。这样能明显提升高级感和简洁度，而且不需要动模型调用、工具执行或权限系统。

本轮 UI 质感提升的核心不是让正文更亮，也不是先调表格线，而是建立稳定的产品级视觉层级：正文为主、输入有边界、状态退后、品牌克制、风险醒目。

表格边框亮度本轮先不动；后续如果继续处理表格，应优先研究“长说明表格是否应该降级为列表/分组块”，而不是只调颜色。
