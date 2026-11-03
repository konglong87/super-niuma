# TUI Render Budget Architecture Fix Plan

本文档记录 TUI 显示不全问题的彻底修复方案。它不是再修某一条 footer、某一种中文换行或某一个 resume 场景，而是把 TUI 当前缺失的“真实可见渲染预算”补成一层稳定架构。

## 背景

近期已连续修过几类 TUI 显示问题：

1. 完成态 transcript 被底部 chrome 重绘挤住。
2. completed assistant tail 在 recap/status 追加后丢失锚点。
3. 中文长句、ANSI 样式和终端软换行导致视觉行高漏算。
4. bottom chrome 的 usage/controls 按 reported width 换行，但真实终端仍裁掉右侧字符。

最新截图暴露的问题：

```text
Usage ... tools=19/32 err=5 last=B
controls ... ctrl+v paste imag
```

用户粘出的完整 footer 应为：

```text
status  Ready
runtime  ui=tui  model=glm-5.1  provider=custom/openai-compatible  tools=31  cwd=~/GolandProjects/anything-ai
safety  permissions=allow active  sandbox=off
session  resume=b692c129-c693-4afe-a74e-4b499bc0ebe9  state=resumed b692c129-c693-4afe-a74e-4b499bc0ebe9
goal  id=bc18207c active turns 1/3 tokens 44657/200000  step=Execute the next useful change  criteria=0/1 passed req 0/1
evidence  evidence=doc pass Read passed: {"file_path": "goal_complete.txt"}  next=collect evidence for pending acceptance criteria
controls  shift+tab perm=allow  mouse=copy  ctrl+o mouse  enter send  ctrl+j newline  ctrl+c stop/exit  ctrl+u clear  /exit to quit
  Image in clipboard · ctrl+v to paste
```

这说明底部内容不是字符串生成丢失，而是在真实屏幕上被裁剪或挤出。

## 当前根因

TUI 现在没有单一可信的 render budget。不同区域各自使用不同宽度和高度假设：

- `WindowSizeMsg.Width` 直接写入 `m.width`。
- textarea 使用 `msg.Width-2`。
- viewport 使用 `msg.Width-2`，但 `contentWidth()` 又返回 `viewport.Width-2`。
- usage 用 `contentWidth()` 换行。
- status/runtime/session/goal/evidence/controls 用 `m.width` 换行。
- divider line 用 `m.width`。
- fixed chrome height 用 `terminalWidth()` 估算。

这些值在普通宽屏下接近，所以问题不明显；在 Codex Desktop、tmux、字体缩放、截图容器、窄终端或底部信息很长时，它们会偏离真实可见区域。

结果链路：

```text
reported width 偏大
  -> footer 按偏大的 width 认为一行能放下
  -> terminal/app 实际可见宽度更小
  -> 右侧字符被裁剪，或软换行成额外物理行
  -> fixedChromeHeight() 仍按偏大 width 估算，少算 footer 高度
  -> viewport 留得过高
  -> 底部 chrome 把 live 内容或 footer 尾行挤出屏幕
```

所以之前的修复没有彻底解决，是因为它们分别修了局部函数：

- bottom guard 解决 scrollback 末尾被 footer 贴住。
- completed tail anchor 解决最终回答锚点。
- visual line count 解决视觉行高漏算。
- wrap usage/controls 解决“按 reported width 不截断”。

但它们仍然共享同一个漏洞：没有统一的“真实可见宽高预算”和“footer 超预算降级策略”。

## 彻底修复目标

本次修复完成后，必须满足以下不变量：

1. `View()` 生成的当前屏视觉宽度不能超过 safe render width。
2. `View()` 生成的当前屏视觉高度不能超过 safe render height。
3. 所有 viewport、usage、input、controls、runtime、session、goal、evidence、divider 都从同一个 render budget 读取宽度。
4. 任何新增 bottom chrome 面板都必须进入同一个 height accounting。
5. 当 footer 信息太多时，必须自动进入 compact mode，而不是继续挤压 live viewport。
6. 当前回答尾部至少保留一个可读 viewport 高度；屏幕太小时也要优先保证 input/status/最小上下文，不允许沉默裁剪。
7. 测试不能再用同一个错误的 `model.width` 同时作为渲染输入和验收标准；必须覆盖 reported width 与 safe width 不一致的场景。
8. 真实 PTY/tmux/Codex Desktop 截图验收必须作为最终证据之一。

非目标：

1. 不修 provider 真的返回半句、网络中断或 `max_tokens` 截断。
2. 不把完整历史长期塞回 live viewport。
3. 不改变 transcript JSONL、export markdown、provider request/response 格式。
4. 不默认打开 mouse tracking，不破坏终端原生选择复制。

## 架构方案

### 1. 引入 Render Budget

新增集中结构，不再让各个 view 函数自由读取 `m.width`、`m.viewport.Width`、`m.textarea` 宽度。

示意：

```go
type renderBudget struct {
    ReportedWidth  int
    ReportedHeight int

    SafeWidth  int
    SafeHeight int

    ContentWidth int
    ChromeWidth  int
    InputWidth   int

    MinViewportHeight int
    MaxChromeHeight   int
}
```

唯一入口：

```go
func (m Model) renderBudget() renderBudget
```

原则：

- `ReportedWidth/Height` 只表示 Bubble Tea 上报值，不直接用于布局。
- `SafeWidth` 是所有当前屏渲染的最大可见列宽。
- `ContentWidth`、`ChromeWidth`、`InputWidth` 都从 `SafeWidth` 派生。
- 所有 wrapping、divider、viewport width、height accounting 都只使用 budget。
- `m.width` 和 `m.height` 只能在 `renderBudget()` 内部作为输入，不能散落在 view 函数里。

建议初始策略：

```text
SafeWidth = clamp(ReportedWidth - safetyMargin, minWidth, ReportedWidth)
ContentWidth = max(minContentWidth, SafeWidth - 4)
ChromeWidth = max(minChromeWidth, SafeWidth - 2)
InputWidth = max(minInputWidth, SafeWidth - 2)
SafeHeight = max(0, ReportedHeight)
MinViewportHeight = 3
MaxChromeHeight = min(12, max(5, SafeHeight-MinViewportHeight))
```

`safetyMargin` 初始可取 2 到 4 列，并集中配置。这个 margin 的价值是吸收 Codex Desktop 容器边缘、字体测量差异、ANSI 样式边界和 terminal emulator 的细小偏差。

后续如果能从真实 PTY 读到更准确的 cell width，可以替换 `SafeWidth` 计算；调用方不需要改。

### 2. 统一 Bottom Chrome Builder

当前 `bottomChromeParts(status, hasLive)` 只返回字符串数组，没有携带每块区域的身份、优先级和是否可折叠。需要升级为结构化 builder。

示意：

```go
type chromeBlock struct {
    ID       string
    Priority int
    Required bool
    Compactable bool
    Render func(renderBudget, chromeMode) string
}

type chromeMode string

const (
    chromeFull chromeMode = "full"
    chromeCompact chromeMode = "compact"
    chromeMinimal chromeMode = "minimal"
)
```

底部块按优先级：

```text
P0 required:
  input divider
  textarea
  input divider
  minimal status/controls

P1 important:
  usage compact
  permission prompt
  slash suggestions
  attachment tray

P2 diagnostic:
  runtime
  safety
  session/resume
  goal
  evidence
  full controls
```

builder 职责：

1. 先用 `chromeFull` 渲染并计算视觉高度。
2. 如果 `chromeHeight <= MaxChromeHeight`，使用 full。
3. 如果超预算，切到 `chromeCompact`：
   - usage 压成一到两行。
   - runtime/session/goal/evidence 合并为短 summary。
   - controls 保留常用键，长说明移到 `/status` 或 `ctrl+o` 面板。
4. 如果仍超预算，切到 `chromeMinimal`：
   - 只保留 status、核心 controls、input。
   - diagnostic 块隐藏，但必须有可访问入口，例如 `ctrl+o details` 或 `/status`。

关键点：footer 不允许无限增长。它必须服从高度预算。

### 3. 统一 Width Wrapping

所有底部行都必须走同一个 wrapper：

```go
func wrapChromeParts(parts []string, budget renderBudget) []string
func wrapChromeLine(line string, budget renderBudget) []string
```

替换范围：

- `wrapUsageParts`
- `wrapModeHintParts`
- `wrapModeHintLine`
- runtime context panel lines
- controls line
- attachment/image hint
- permission/status hints

规则：

- 单行视觉宽度必须 `<= budget.ChromeWidth`。
- 长 token、UUID、path、JSON snippet 必须可断行。
- continuation indent 要算入宽度，不能只算 payload。
- ANSI escape 不参与宽度计算。
- CJK/emoji 使用 `runewidth` 或 `lipgloss.Width`，但只能通过统一 helper 调用。

这可以避免 usage 用一套换行、controls 用另一套换行、runtime 又用第三套换行。

### 4. 统一 Height Accounting

所有当前屏高度都必须通过同一个视觉行高函数：

```go
func visualLineCountForWidth(text string, width int) int
```

使用规则：

- live viewport content 用 `budget.ContentWidth`。
- bottom chrome 用 `budget.ChromeWidth`。
- input box 用 `budget.InputWidth`。
- 整体 `View()` fit 检查用 `budget.SafeWidth`。

禁止在布局路径直接使用逻辑 `lineCount()`。

必须覆盖：

- `normalViewportHeight()`
- `viewportHeightForContent()`
- `fixedChromeHeight()`
- `transcriptBottomGuardLinesFor(...)`
- resume/rewind picker start Y
- todo progress
- slash suggestions
- attachment tray
- permission prompt
- usage
- mode hint/runtime context panel

### 5. View() 最后一层裁剪保护

即使前面预算正确，`View()` 也需要最后一道 invariant gate：

```go
func (m Model) renderFrame() string {
    budget := m.renderBudget()
    frame := buildFrame(budget)
    return fitFrame(frame, budget)
}
```

`fitFrame` 不是用来隐藏正常 bug，而是兜底防止当前屏返回超过真实预算：

- 任意视觉行超过 `SafeWidth`，测试失败；运行时可 hard wrap。
- 视觉高度超过 `SafeHeight`，优先裁剪 live viewport，不裁剪 input/status。
- 如果必须裁剪 diagnostic footer，切 compact/minimal，而不是裁剪字符串右侧。

这层保护让未来新增面板时也不容易破坏当前屏。

### 6. Diagnostic 状态移出常驻 Footer

当前 footer 常驻显示：

```text
runtime / safety / session / goal / evidence / full controls
```

这在调试时有价值，但不应该无条件占据当前屏。建议改成：

默认常驻：

```text
status Ready  model=glm-5.1  goal=bc18207c  ctx=28.8%  tools=19/32
controls shift+tab perm=allow  ctrl+o details  enter send  ctrl+j newline  ctrl+c stop/exit
```

详细信息进入 `ctrl+o details` 或 `/status`：

```text
runtime  ui=tui  provider=custom/openai-compatible  cwd=...
session  resume=...  state=resumed ...
goal     id=... step=... criteria=...
evidence ...
safety   permissions=allow active sandbox=off
```

这样可以从根上降低 footer 高度压力。彻底修复不能只靠更聪明的换行；信息架构也要降噪。

## 实施步骤

### Phase 0: 诊断和金标样例

1. 增加一个只在测试或 debug build 使用的 render budget dump helper。
2. 固化本次截图状态为单元测试 fixture：
   - width around 148/150/160
   - height around screenshot height
   - usage 包含 `last=Bash`
   - controls 包含 image paste hint
   - resume/session/goal/evidence 都存在
3. 测试必须复现：
   - `Usage` 不允许只显示到 `last=B`
   - `controls` 不允许只显示到 `imag`
   - `View()` 视觉高度不超过 terminal height

### Phase 1: Render Budget 接入

1. 新增 `renderBudget` 和 `renderBudget()`。
2. `WindowSizeMsg` 中只更新 raw size，然后调用 budget 派生 textarea/viewport 宽高。
3. 替换 `contentWidth()`、`terminalWidth()` 的散落使用，保留兼容方法但内部转调 budget。
4. divider、textarea、viewport、usage、mode hint 全部使用 budget 宽度。

验收：

```bash
go test ./internal/tui -run 'RenderBudget|BottomChrome|VisualLine|Viewport' -count=1
```

### Phase 2: Structured Chrome Builder

1. 引入 `chromeBlock` / `chromeMode`。
2. 将 usage、input、mode hint、runtime context、attachment tray 迁移到 builder。
3. `fixedChromeHeight()` 从 builder 的实际渲染结果计算，不再重新拼一套近似字符串。
4. `View()` 和 `fixedChromeHeight()` 必须使用同一个 builder 结果，避免估算和实际渲染分叉。

验收：

```bash
go test ./internal/tui -run 'ChromeBuilder|FixedChromeHeight|ShortTerminal|WrappedBottomChrome' -count=1
```

### Phase 3: Footer Compact/Minimal Mode

1. 定义 full/compact/minimal 三档。
2. 超过 `MaxChromeHeight` 时自动降级。
3. diagnostic 信息隐藏时，常驻 footer 必须提供可访问入口。
4. 截图场景在 compact mode 下仍显示完整 input/status/controls 核心信息。

验收：

```bash
go test ./internal/tui -run 'CompactChrome|MinimalChrome|FooterBudget|ControlsVisible' -count=1
```

### Phase 4: Frame Fit Invariant

1. 新增 `assertFrameFitsBudget` 测试 helper。
2. 所有 TUI layout 回归测试都用 safe width/height 断言，而不是只用 `model.width`。
3. 增加 fuzz/property 风格测试：
   - widths: 40, 56, 72, 80, 96, 120, 148, 150, 160
   - heights: 8, 10, 12, 14, 18, 24, 40
   - content: CJK、emoji、UUID、long path、JSON、markdown table、ANSI styled text

验收：

```bash
go test ./internal/tui -run 'FrameFits|LayoutMatrix|NoLineExceedsSafeWidth' -count=1
```

### Phase 5: 真实终端验收

必须做真实 PTY 验收，不只靠单元测试：

1. tmux 设定固定尺寸，例如 `150x40`、`120x32`、`96x24`。
2. 启动 TUI，加载和截图同类状态：
   - resumed session
   - active goal
   - goal evidence
   - clipboard image hint
   - long usage
3. 截取 pane 文本和截图。
4. 断言：
   - `last=Bash` 完整可见，不能停在 `last=B`。
   - `ctrl+v paste image` 或 image clipboard hint 完整可见，不能停在 `imag`。
   - final assistant tail 可见。
   - footer 没有把 viewport 压到 0 行，除非 terminal height 小于最小支持高度。

建议脚本：

```bash
scripts/tui-render-budget-acceptance.sh
```

脚本输出：

```text
/tmp/gocc-tui-render-budget-<timestamp>/
  pane-150x40.txt
  pane-120x32.txt
  pane-96x24.txt
  screenshot-150x40.png
  report.json
```

## 测试矩阵

必须新增或更新以下测试：

| 测试 | 目的 |
| --- | --- |
| `TestModelRenderBudgetUsesSafeWidth` | reported width 与 safe width 分离 |
| `TestModelBottomChromeUsesSameBudgetAsView` | `View()` 和 chrome height accounting 不分叉 |
| `TestModelFooterCompactsWhenDiagnosticsOverflow` | session/goal/evidence 很长时自动 compact |
| `TestModelControlsTailVisibleWithClipboardImageHint` | 防止 `ctrl+v paste imag` 复发 |
| `TestModelUsageTailVisibleWithLongStats` | 防止 `last=B` 复发 |
| `TestModelViewNeverExceedsSafeFrame` | 当前屏视觉宽高不超预算 |
| `TestModelViewportKeepsMinimumHeightUnderFooterPressure` | footer 不把回答区挤没 |
| `TestModelRenderBudgetMatrix` | 多宽高、多内容类型矩阵 |
| `TestModelDiagnosticDetailsAccessibleWhenFooterCompacted` | compact 后详细信息仍可访问 |

常规验证：

```bash
go test ./internal/tui -count=1
go test ./... -count=1
git diff --check
```

真实验收：

```bash
scripts/tui-render-budget-acceptance.sh
```

## 代码改动边界

主要文件：

```text
internal/tui/app.go
internal/tui/view_format.go
internal/tui/app_test.go
```

可选新增文件：

```text
internal/tui/layout_budget.go
internal/tui/layout_budget_test.go
internal/tui/chrome_builder.go
internal/tui/chrome_builder_test.go
scripts/tui-render-budget-acceptance.sh
```

不应修改：

```text
internal/query/*
internal/session/*
provider request/response schema
transcript JSONL schema
```

除非实现中发现当前显示问题有新的跨层证据，否则本修复只应收敛在 TUI layout 层。

## 风险和规避

| 风险 | 规避 |
| --- | --- |
| safe margin 过大导致浪费宽度 | 初始 margin 小，集中配置，真实验收后调整 |
| compact footer 隐藏了用户需要的信息 | 提供 `ctrl+o details` 或 `/status` 查看完整诊断 |
| 新 builder 引入行为变化 | 先用测试锁住现有 full mode 文本，再引入 compact |
| 宽高计算仍与 terminal 有差异 | 单元测试加真实 PTY 验收；运行时最后 `fitFrame` 兜底 |
| 多行 textarea 高度变化漏算 | textarea 也必须成为 chrome block，由 builder 统一计高 |
| permission/slash/picker 特殊态漏算 | 特殊态必须进入 chrome block 或 overlay accounting |

## 完成定义

只有同时满足下面条件，才能宣称彻底修复：

1. 截图同类状态下 `Usage`、`controls`、assistant tail 都完整显示或进入明确 compact 模式。
2. 任意 `View()` 当前屏不超过 safe width/height。
3. 所有 bottom chrome 高度由实际渲染结果计算。
4. footer 超预算时自动 compact/minimal，而不是裁剪右侧或挤掉 live viewport。
5. `go test ./internal/tui -count=1` 通过。
6. `go test ./... -count=1` 通过。
7. `git diff --check` 通过。
8. 真实 PTY/tmux acceptance 脚本通过并保存证据。

## 后续维护规则

新增任何 TUI 底部元素时，必须回答三个问题：

1. 它属于哪个 `chromeBlock`？
2. full/compact/minimal 三档如何展示？
3. 哪个测试证明它不会让 `View()` 超出 safe frame？

如果不能回答，不能直接把字符串 append 到 `bottomChromeParts()`。
