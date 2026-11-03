# TUI 工具进度验收流程

本文档固化 TUI 工具进度展示的验收体系。目标是让每次改动后都能用同一套场景验证：用户是否能在真实 TUI 中快速知道当前任务、工具调用、失败原因和完成状态。

## 验收目标

- 工具进度只在 assistant 消息流内 inline 展示，不再出现重复的底部 `Tools` 面板或消息底部第二份工具列表。
- `TodoWrite` 展示任务数量和当前任务，不展示原始 JSON。
- 成功工具显示高信号结果，例如 `Read README.md ✓ → 3 lines`、`Bash go test ./... ✓ → exit 0`。
- 失败工具显示真实失败状态，例如 `Read missing-readme.md ✗ → not found`、`Bash ls missing-file ✗ → exit 1`，不能出现 `✗ → exit 0`。
- `Tasks done/total` 面板能反映 TodoWrite 更新，状态栏能切到当前 `Task: ...`。
- `ctrl+t` 和 `ctrl+y` 的展开/折叠提示仍可用。
- **等待期心跳**：turn 忙碌但没有工具/子代理在运行、也没有文本在流式输出时（等模型 API 返回），主体区底部实时显示一行动画心跳 `⠙ Go Claude is ...  <elapsed>`（盲文圈转动、省略号逐点点亮、秒数递增）；有工具在跑时不重复显示（复用工具行自己的 spinner），turn 结束即消失且不落入 scrollback。

## 一键准备

```bash
scripts/tui-tool-progress-acceptance.sh
```

默认行为：

- 创建报告目录：`reports/tui-tool-progress/<timestamp>/`
- 生成三个 fixture：`success`、`failure`、`multi`
- 生成可复制提示词：`prompts.md`
- 生成验收记录模板：`report-template.md`
- 运行关键测试：`go test ./internal/tui/... ./internal/query/... ./internal/cli/... -count=1`
- 构建验收二进制：`golang-cc-test`

可选全量测试：

```bash
TUI_TOOL_PROGRESS_ACCEPTANCE_FULL_TESTS=1 scripts/tui-tool-progress-acceptance.sh
```

## 打开真实 TUI

按场景打开：

```bash
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=success scripts/tui-tool-progress-acceptance.sh
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=failure scripts/tui-tool-progress-acceptance.sh
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=multi scripts/tui-tool-progress-acceptance.sh
```

脚本会打印对应 `cwd` 和提示词位置。进入 TUI 后从 `prompts.md` 复制对应提示词，完成后输入 `/exit` 退出。

如果要连续验收三类场景：

```bash
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=all scripts/tui-tool-progress-acceptance.sh
```

## 场景和通过标准

### success

覆盖 TodoWrite、Read、Bash 成功路径。

通过标准：

- 屏幕出现 inline 工具行：`TodoWrite`、`Read README.md`、`Read calc.go`、`Bash go test ./...`。
- `Bash go test ./...` 显示 `✓ → exit 0`。
- `Tasks 3/3 all completed` 可见。
- 不出现独立底部 `Tools` 面板。
- 不出现 raw JSON，例如 `{"todos"`。

### failure

覆盖 Read 失败和 Bash 失败路径。

通过标准：

- `Read missing-readme.md` 显示 `✗ → not found`。
- `Bash ls missing-file-for-tui-acceptance` 显示 `✗ → exit 1` 或 `✗ → failed`。
- 不出现 `✗ → exit 0`。
- `TodoWrite` 行是 `2 todos: ...` 或 `2 todos, 2 completed`，不是 JSON。

### multi

覆盖多工具折叠和展开。

通过标准：

- 默认只显示最近几条 inline 工具行，并出现 `... +N tool uses ctrl+t expand`。
- 按 `ctrl+t` 后显示更多工具行，并出现 `ctrl+t collapse`。
- 按 `ctrl+y` 后任务面板能展开/折叠。
- 工具行仍只出现一次，不在底部重复。

## 证据记录

每轮验收把关键可见行记录到 `report-template.md`：

```text
TodoWrite 2 todos: 读取 README.md ✓ → saved 2 todos
Read README.md ✓ → 3 lines
Bash ls missing-clean-panel-test ✗ → exit 1
Tasks 2/2 all completed ✓
```

如果出现以下任一情况，视为失败：

- 看到独立 `Tools` 面板重复展示工具。
- 看到 `TodoWrite {"todos": ...`。
- 看到错误工具行显示成功式摘要，例如 `✗ → exit 0`、`✗ → 1 lines`。
- Todo 面板停在旧任务，状态栏与当前 in_progress 不一致。
- 展开/折叠快捷键提示消失或按键无效。

## 推荐提交流程

TUI 工具进度相关改动完成后，至少执行：

```bash
scripts/tui-tool-progress-acceptance.sh
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=failure scripts/tui-tool-progress-acceptance.sh
git diff --check
```

发版或大改前再执行：

```bash
TUI_TOOL_PROGRESS_ACCEPTANCE_FULL_TESTS=1 scripts/tui-tool-progress-acceptance.sh
TUI_TOOL_PROGRESS_ACCEPTANCE_RUN=all scripts/tui-tool-progress-acceptance.sh
```
