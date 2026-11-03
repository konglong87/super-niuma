# TUI Permission, Markdown, and Live Spacing Bugfix Plan

## 背景

当前 TUI 模式有 3 类用户可见问题：

1. AI 回复中的 Markdown 表格渲染不完整，表头分隔线和行线不明显，宽表格看起来像原始文本。
2. 权限模式已经切到 `allow` / permission on 后，当前运行中的工具调用仍然继续弹授权框。
3. `TodoWrite` 选择 session/project/global 授权后仍然反复弹窗，表现为历史授权没有命中。
4. 实时回复过程中，AI 消息底部与 `Usage` 行之间没有空隙，视觉上挤在一起。

这些问题都集中在 TUI 实时显示和权限交互链路，必须小范围修复并补回归测试，避免再次影响输入框、scrollback、权限安全边界。

## 根因判断

### Markdown 表格

TUI 当前通过 `glamour` 渲染 Markdown。`glamour` 会识别 GFM 表格语法，但终端样式不会稳定画出完整表格线，宽表格还会受 word wrap 影响，导致 `|---|` 分隔线被消费后没有用户期望的横线效果。

### Permission on 运行中不热生效

Shift+Tab 切换权限模式后，底部状态会显示 `>> allow permissions on`，但当前正在运行的 `query.Session` 已经在本轮开始时按旧的 `ask` policy 创建。后续 tool check 仍然使用旧 policy，因此同一轮运行中还会继续弹窗。

### TodoWrite 授权规则过窄

`TodoWrite` 的 permission request 当前使用完整 todos JSON 作为 request qualifier。todos 每次状态变化都会改变 JSON，导致保存的规则类似 `TodoWrite(<完整 JSON>)`，下一次请求无法命中，所以 session/project/global 授权看起来失效。

### 实时回复与 Usage 间距

TUI `View()` 将 `liveTranscriptView()`、`toolActivityView()`、`usageView()`、`inputBoxView()` 直接用单个换行拼接。实时消息有内容时，`Usage` 会紧跟在消息下一行，没有视觉间隔。

## 修复方案

### 1. 权限模式热生效

- 在 interactive TUI 中提供运行时权限模式读取函数。
- `query.Session` / guarded tool 每次 tool permission check 前能读取最新 mode，而不是只依赖 session 创建时的静态 settings。
- 当运行中切到 `allow` / `bypass` 时，当前 turn 后续 tool call 应立即放行。
- 如果已有 pending permission prompt，切到 `allow` 时应自动批准当前 pending request，避免 UI 显示 allow 但仍卡在弹窗。

安全边界：
- 只对用户显式切到 allow/bypass 的当前 TUI session 生效。
- `deny` 和 `ask` 仍按原权限策略执行。
- 不扩大 Bash/Edit/Write 的持久化授权匹配范围。

### 2. TodoWrite 授权规则归一化

- 对 `TodoWrite` 的持久授权规则生成做特殊处理：session/project/global 保存 `TodoWrite`，不保存完整 todos JSON。
- `TodoWrite` 命中 `TodoWrite` 后即可通过。
- 其他工具保持现有规则粒度。

### 3. 实时消息与 Usage 增加空隙

- 当 live transcript 和 Usage 同时存在时，在两者之间插入一个空行。
- 同步更新 fixed chrome 高度计算，避免 viewport 计算少一行导致内容被挤压。
- 不改变已 flush 到 scrollback 的历史 transcript 格式。

### 4. Markdown 表格专用渲染

- 在 `renderMarkdownForWidth()` 前对 GFM table block 做预处理或局部渲染。
- 对连续 table block 使用 TUI 表格 renderer：
  - 画表头分隔线；
  - 保留列边界；
  - 支持中文宽字符的列宽计算；
  - 超宽时按列截断，避免整屏错位。
- 非表格 Markdown 仍交给 `glamour`。

## 验收标准

### 权限

- TUI 运行中从 ask 切到 allow 后，后续 `TodoWrite` 不再弹窗。
- 当前已有 pending prompt 时切到 allow，会自动返回 allowed decision。
- `TodoWrite` global/session/project 授权后，不同 todos JSON 的后续请求不再弹窗。
- Bash/Edit/Write 仍不能因为 TodoWrite 修复而被宽泛放行。

### Markdown

- AI 回复包含 GFM 表格时，TUI 输出中能看到稳定的表头横线和列边界。
- 表格中包含中文、inline code、长文本时不会破坏列结构。

### UI 间距

- streaming 状态下，AI 实时回复和 Usage 之间有一个空行。
- input box、Usage、scrollback 不被新 spacer 顶错位。

## 测试计划

- `go test ./internal/permissions ./internal/tools ./internal/query ./internal/tui ./internal/cli -count=1`
- `go test ./... -count=1`
- `git diff --check`
- TUI 相关 golden/regression tests：
  - permission mode live switch test；
  - pending prompt auto-allow test；
  - TodoWrite broad persistent rule test；
  - live message / Usage spacing test；
  - Markdown table rendering golden test。
