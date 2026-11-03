# TUI Sub-agent Progress UX 修复方案

本文档针对 TUI 在并发 `Task` / sub-agent 场景下反复显示 `Sub-agent / nested agent progress` 的问题，固化根因、交互目标、实施步骤、验收标准和实施记录。

## 背景

用户在 TUI 中验证 `Task` batch 并发能力时，3 个子任务可以正常完成，但主界面历史区出现大量重复块：

```text
Sub-agent
nested agent progress
```

实测截图中搜索 `nested` 约 111 次。并发功能本身可用，问题集中在 TUI 对 nested progress 事件的展示策略：进度事件被当作聊天历史消息追加，导致主历史被状态噪声淹没。

## 代码路径和根因

当前 TUI 对 `StreamNestedProgress` 分两条路径处理：

- 有 `TaskID`：调用 `updateAgentProgress(event)`，更新 `agentProgress` 聚合面板。
- 无 `TaskID`：把事件追加为 `message{role:"agent"}`，渲染标题为 `Sub-agent`。

关键路径：

- `internal/tui/app.go`：`StreamNestedProgress` 分支在 `TaskID == 0` 时 append 历史消息。
- `internal/tui/app.go`：`messageView` 对 `role == "agent"` 渲染标题 `Sub-agent`。
- `internal/query/query.go`：`Task` 工具结果返回时额外发出 `nested_agent_progress completed`，该事件带 tool id/name/output，但不带 `task_id`。
- `internal/agentruntime/runtime.go`：当 `TaskStore == nil` 或 `taskID == 0` 时，仍会通过 progress 回调把事件推给 TUI。
- `internal/cli/cli.go`：TUI 每轮注册 `nestedAgentProgress` 回调，但 `agentTaskStore` 只在 server/tenant 路径接入，普通本地 TUI 可能没有持久 task store。

因此重复块不是并发执行失败，而是事件语义降级后展示层没有做聚合：

```text
sub-agent runtime events
        |
        v
nested_agent_progress
        |
        +-- task_id != 0 -> Sub-agents panel
        |
        +-- task_id == 0 -> append role=agent message -> repeated history blocks
```

## 设计原则

- 进度是状态，不是聊天消息。
- P0 先止血，避免刷屏；P1 再改善本地 TUI 的 task id 和折叠面板。
- 不改变 `Task` 并发执行、retry、timeout、batch 调度。
- 不改变 stream-json 对外协议，避免破坏 CLI/API 消费者。
- 不丢失败、取消、完成等重要状态，只改变展示位置和聚合方式。
- running/failed/cancelled 优先可见，completed 默认可折叠。

## 目标和非目标

### P0 目标

- `TaskID == 0` 的 `nested_agent_progress` 不再追加为主聊天历史。
- 连续 100 个无 id progress 不会产生 100 个 `Sub-agent` 消息块。
- 有 `TaskID` 的 sub-agent progress 仍进入现有 `Sub-agents` 面板。
- `Task` 工具最终结果仍能在工具结果或 assistant 汇总中可见。

### P1 目标

- 普通本地 TUI 即使没有 MySQL / tenant task store，也能给 sub-agent 进度分配稳定的本地临时 id。
- 支持默认折叠态：只显示最近 3-5 个任务。
- 支持展开态：查看更多任务和单任务详情。
- failed/cancelled/running 不被 completed 任务挤出默认视图。

### 非目标

- 不实现完整 WebUI Agents cockpit。
- 不改变 transcript/session 持久化格式。
- 不把每个 text delta 写入 terminal scrollback。
- 不把无 id 事件伪造成真实持久化 task 记录。

## 推荐交互方案

### 默认折叠态

默认把 sub-agent 进度显示成固定高度的状态面板，占 4-6 行：

```text
Sub-agents  3 running / 12 done / 0 failed      [e expand]
#108  running    46s   turn=5  tool=Read       统计项目 .go 文件
#107  completed  15s   107 files               统计 service/
#106  completed  23s   155 files               统计 model/
+9 older hidden
```

默认显示规则：

- 默认显示最近 3 个；终端高度足够时最多显示 5 个。
- 最近按 `updated_at` 排序，而不是创建时间。
- 排序优先级：`failed/cancelled` > `running` > `completed`。
- completed 任务只保留摘要，不展开长内容。
- `text_delta` 只更新对应 task 的 latest summary，不进入主聊天历史。
- 隐藏任务用一行 `+N older hidden` 表示。

### 展开态

按 `e` 切换展开/折叠。展开后显示更多任务和关键字段：

```text
Sub-agents  expanded                         [e collapse] [up/down select]
#108 reviewer (glm-5.1)  running  46s
  desc: 统计项目 .go 文件
  progress: turn=5 messages=18 tool=Read
  tokens: in=101036 out=631 cache=0/21632
  stop: /agent-stop 108

#107 reviewer (glm-5.1)  completed  15s
  desc: 统计 service/
  result: 107 files
  session: e1e2...
```

展开态规则：

- 面板最多占终端高度的 30%-40%，避免挤压输入框。
- 展开后最多显示 10 个任务；更多任务走面板内部滚动。
- `up/down` 选择任务。
- `enter` 打开选中任务详情。
- `esc` 或再次按 `e` 回到折叠态。

### 单任务详情态

选中任务后按 `enter` 打开详情：

```text
Sub-agent #108 reviewer
status: running  elapsed: 46s  model: glm-5.1
description: 统计项目 .go 文件
latest: running turn=5 messages=18
last tool: Read internal/tools/task/task.go
tokens: in=101036 out=631 cache read=21632
```

详情态规则：

- `esc` 返回展开态。
- failed 任务展示 error。
- completed 任务展示 result summary、turns、tool calls、session id。
- running 任务展示 stop 提示。

## 数据模型建议

在 TUI model 中扩展 sub-agent UI 状态：

```go
type Model struct {
    // existing fields...
    agentProgress map[uint64]agentProgress
    agentOrder    []uint64

    agentPanelExpanded bool
    agentPanelSelected uint64
    agentPanelScroll   int
    localAgentSeq      uint64
}
```

`agentProgress` 保留现有用途，新增字段可按需补充：

```go
type agentProgress struct {
    taskID      uint64
    localOnly   bool
    status      string
    agent       string
    model       string
    description string
    detail      string
    lastTool    string
    updatedAt   time.Time
    startedAt   string
    durationMS  string
    tokens      string
    cache       string
    sessionID   string
}
```

本地临时 id 方案：

- 如果 event 有 `TaskID`，使用真实 id。
- 如果 event 没有 `TaskID` 但能从 tool id、session id 或 payload 中取到稳定 key，则映射到本地临时 id。
- 如果无法取稳定 key，P0 直接忽略或只更新 running status；不要追加历史消息。

## 实施步骤

### Step 1: P0 止血

修改 `StreamNestedProgress` 的 `TaskID == 0` 分支：

- 不再 append `message{role:"agent"}`。
- `event.Event == "completed"` 的 Task 工具级 completion 只更新 `runningStatus` 或丢弃。
- 如果 `event.Output` 是错误并且 `IsError == true`，可转成短错误状态，但不能刷历史。

验收：

- 100 个无 id nested progress 不产生 100 条历史消息。
- 有 id 的 progress 面板不变。

### Step 2: 聚合排序和显示上限

调整 `agentProgressView()`：

- 按状态优先级和更新时间排序。
- 折叠态显示 3-5 个。
- 超出显示 `+N older hidden`。
- failed/cancelled/running 优先显示。

验收：

- 10 个 completed + 1 个 failed 时，failed 默认可见。
- 10 个 running 时，只显示最近 3-5 个，并显示隐藏数量。

### Step 3: 展开/折叠快捷键

新增 TUI 键位：

- `e`：当存在 sub-agent 面板时切换展开/折叠。
- `up/down`：展开态选择任务。
- `enter`：进入单任务详情态。
- `esc`：从详情态返回，或从展开态折叠。

注意不要影响：

- 文本输入中的普通字符 `e`。
- slash suggestions / resume picker / permission prompt 的优先级。
- 多行输入和快捷键已有行为。

推荐处理方式：

- 只有在 `busy == true` 且 input 未聚焦特殊 picker，或用户按明确组合键时，才拦截面板快捷键。
- 如果单字符 `e` 与正常输入冲突，改用 `ctrl+e` 或 `tab`。

### Step 4: 本地临时 id

为普通本地 TUI 增加进程内 id 映射，优先级低于真实 task id：

- key 优先取 `payload.session_id`。
- 其次取 stream/tool id。
- 最后才使用递增本地 id。

本地 id 只用于展示，不写入 transcript、MySQL 或 API。

### Step 5: 文档和回归

实现完成后更新：

- `docs/subagent_multiagent/subagent_multiagent_parity_todo.md` 中 `SMA-108` 的 UX 说明。
- 必要时补 `docs/tui/` 的验证记录。

## 测试计划

### 单元测试

新增或扩展 `internal/tui/app_test.go`：

- `TestModelIgnoresNestedProgressWithoutTaskIDInHistory`
  - 输入 100 个 `StreamNestedProgress{TaskID:0}`。
  - 断言 `messages` 不新增 100 条 `role=agent`。
  - 断言 `View()` 不包含重复 `nested agent progress`。

- `TestModelKeepsTaskIDNestedProgressPanel`
  - 输入 3 个不同 `TaskID` 的 started/running/completed。
  - 断言 `Sub-agents` 面板包含 3 个任务。

- `TestModelCollapsesOlderAgentProgress`
  - 输入 10 个 completed。
  - 断言默认只显示 3-5 个。
  - 断言显示 `+N older hidden`。

- `TestModelPrioritizesFailedAndRunningAgents`
  - 输入多个 completed、一个 failed、一个 running。
  - 断言 failed/running 默认可见。

- `TestModelTogglesAgentPanelExpanded`
  - 模拟展开/折叠键。
  - 断言展示数量变化，但 `agentProgress` 数据不丢。

### 集成测试

目标包：

```bash
go test ./internal/tui ./internal/query ./internal/agentruntime ./internal/tools/task -count=1
git diff --check
```

全量验证：

```bash
go test ./... -count=1
git diff --check
```

### 人工验收

复用用户的 batch prompt：

- 3 个子任务并发成功。
- 主历史区不再出现大量 `Sub-agent / nested agent progress`。
- 默认面板只显示最近 3-5 个任务。
- 展开后可以看到更多任务。
- failed/cancelled 能在默认态优先露出。
- 输入框、权限弹窗、工具 activity、usage 行不重叠。

## 风险和控制

| 风险 | 控制 |
| --- | --- |
| 隐藏了真正重要的 sub-agent 错误 | failed/cancelled 优先显示；error 写入 task detail，不进噪声历史。 |
| 快捷键影响正常输入 | 优先用 `ctrl+e` 或仅在面板焦点态拦截；picker/permission/input 优先级高于面板。 |
| 改动破坏 stream-json 客户端 | 不改 `query` 对外事件协议，P0 只改 TUI 展示。 |
| 无 task store 时仍没有真实 id | P1 使用本地临时 id，仅用于展示，不伪造持久化记录。 |
| 面板太高遮挡输入 | 折叠态 4-6 行；展开态最多 30%-40% 终端高度。 |
| completed 任务太多淹没 running | 状态优先级排序，completed 默认靠后。 |

## 推荐落地顺序

1. 先做 Step 1，并跑 TUI 针对性测试，快速止血。
2. 再做 Step 2，把面板显示从无限列表改成折叠摘要。
3. 确认稳定后做 Step 3 展开/折叠。
4. 最后做 Step 4 本地临时 id，减少普通 TUI 和 tenant/server 路径的体验差异。

每一步都应独立可验证、可提交，避免一次性把执行、协议、展示和快捷键混在一起。

## 实施记录

2026-06-28 已完成 P0/P1 展示层修复：

- `TaskID == 0` 的 `nested_agent_progress` 不再追加为 `role=agent` 历史消息，避免主聊天区刷出大量 `Sub-agent / nested agent progress`。
- 普通本地 TUI 没有持久 `TaskStore` 时，runtime 为单次 sub-agent run 分配本地临时 progress id；该 id 只用于 TUI 聚合展示，不暴露为持久化 `task_id`。
- 有 `TaskID` 的 sub-agent progress 继续进入 `Sub-agents` 聚合面板。
- 聚合面板默认折叠显示最近 3 个；终端高度充足时显示 5 个；展开态最多显示 10 个。
- 面板排序优先级为 failed/cancelled、running、completed，避免失败和运行中任务被 completed 任务挤出默认视图。
- 新增 `ctrl+e` 切换 sub-agent 面板展开/折叠；只在已有 sub-agent progress 时提示和生效，避免影响普通输入。
- 新增 TUI 回归测试覆盖无 id progress 不进历史、默认折叠、failed/running 优先显示、展开/折叠不丢状态。

验证命令：

```bash
go test ./internal/tui -run 'TestModel(RendersNestedAgentProgressPanel|IgnoresNestedProgressWithoutTaskIDInHistory|CollapsesOlderAgentProgress|PrioritizesFailedAndRunningAgents|TogglesAgentPanelExpanded|RunningStatusFollowsStreamEvents)' -count=1
go test ./internal/tui -count=1
go test ./internal/query -run 'TestQueryGolden' -count=1
go test ./internal/agentruntime -run TestRuntimeAssignsLocalProgressTaskIDWithoutStore -count=1
```
