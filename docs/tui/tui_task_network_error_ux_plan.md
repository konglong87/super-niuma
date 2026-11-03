# TUI Task List and Network Error UX Plan

## Background

当前 TUI 有两个用户可见问题：

1. `Tasks` 面板未展开时只显示 `in_progress` 任务，7 个任务时默认只露出 1 个，其余需要 `ctrl+y` 才能看见。
2. 模型流式读取发生网络错误时，界面只显示原始 `read tcp ... operation timed out`，同时任务面板仍像在运行，用户难以判断当前 turn 是否已经停止。

真实日志显示该类错误发生在 `stream.read` 阶段，并且可能已经收到 text/tool delta。此时不应盲目自动重试，否则可能重复执行工具或放大半截工具调用的风险。

## Goals

- 默认展示最多 4 个任务，超过 4 个才折叠。
- 折叠视图必须优先包含当前执行任务，并尽量保留上下文。
- 网络错误显示用户可理解的中文说明，同时保留原始错误详情。
- 网络错误后，TUI 状态必须真实反映“本轮已停止”，不能继续显示 running/spinner。
- 保持 `ctrl+y` 展开全部任务的现有交互。

## Non-Goals

- 不在本次改动里实现自动网络重试。
- 不改变 query loop、工具执行、transcript 持久化语义。
- 不把 todo 计划状态写成 failed；网络错误只影响当前 TUI turn 的呈现状态。

## Design

### Task collapsed view

新增默认折叠上限：

- `<=4` 个任务：默认全量展示，不提示展开。
- `>4` 个任务：默认展示 4 个，并提示 `还有 N 个，ctrl+y 展开`。
- 展示窗口选择：
  - 优先包含第一个 `in_progress`。
  - 以当前任务为中心，补齐前后任务。
  - 保持原始顺序，避免用户读到跳序任务。

### Task running truth

`in_progress` 是任务计划状态，不等于 runtime 正在运行：

- `busy=true`：当前任务显示 spinner。
- `busy=false && err=nil`：当前任务显示静态当前标记。
- `busy=false && err!=nil`：当前任务显示中断标记，并提示需要继续。

### Network error copy

将底层网络错误分类为用户友好文案：

```text
网络连接超时，本轮已停止
模型流式响应中断，已保留当前进度。可以直接输入“继续”让 Go Claude 从当前任务继续。
详情：read tcp ...
```

识别范围：

- `operation timed out`
- `connection reset by peer`
- `i/o timeout`
- `context deadline exceeded`
- `stream error`

### Error finalization

`StreamFinished` 携带错误时：

- `busy=false`
- `streamingActive=false`
- 清空 `runningStatus`
- running tool 标记为 `interrupted`
- running sub-agent 标记为 `interrupted`
- 当前 todo 面板显示中断状态

成功路径保持现有 archive 行为。

## Verification

- Unit tests:
  - collapsed todo 默认最多显示 4 个。
  - `<=4` 个任务默认全显示。
  - `ctrl+y` 仍展开全部。
  - stream timeout 显示友好中文文案。
  - stream timeout 后 running tool/sub-agent 不再显示 running。
- Manual screenshot:
  - 导出 TUI 渲染视图，截图确认默认 4 条任务和网络错误文案可读。
