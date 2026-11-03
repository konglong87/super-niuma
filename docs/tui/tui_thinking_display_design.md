# TUI Thinking 展示设计

## 目标与边界

TUI 支持 `full / summary / hidden` 三种 Thinking 展示模式。`full` 使用低视觉权重
的思考轨道展示模型已经产生的 thinking 内容，`summary` 只在主 transcript 固化摘要，
`hidden` 不在主 UI 留下 Thinking。该模式只控制对话正文展示，不改变 provider 请求、
`thinking_delta` 生成、模型推理预算、Web Agent UI 或会话协议。

拓扑影响为 `RT-OUTPUT`，爆炸半径为 `B1_SCENARIO`。现有 runtime topology 的节点、
路径和因果边仍然准确，因此不新增 registry 节点。

## 展示规则

- `tui.thinkingMode` 优先于旧 `tui.showThinking`。新字段未配置时，
  `showThinking=true/false` 分别兼容映射为 `full/hidden`。
- `full`：流式阶段使用 `◌ Thinking`，完成后使用 `◇ Thought`，正文使用弱化轨道。
- `summary`：流式/完成阶段只显示回合、phase、行数、耗时和 token 可用状态；完整正文
  仍保存在既有 session transcript 和 TUI 内存 archive。
- `hidden`：主 transcript 不渲染 Thinking，也不拆分连续 assistant phase；完整正文
  仍由既有 session transcript 持久化，当前进程内可供显式详情查看。
- 正文使用弱化颜色和左侧 `│` 轨道，与最终回答建立清晰层级。
- thinking delta 原样拼接，保留换行、列表和代码缩进；仅在渲染边界清理首尾空白。
- Markdown 使用独立的弱化主题，live preview 与 transcript 共用同一渲染入口。
- 折叠/展开不创建 user message，不调用模型，不增加 token、turn 或 tool call。

## 折叠与展开命令

```text
/thinking
/thinking full
/thinking summary
/thinking hide
/thinking show latest
/thinking show <turn>
/thinking summary <turn>
```

- `/thinking` 查看当前模式和命令提示。
- `/thinking summary` 让后续 Thinking 在主 transcript 中只显示摘要。
- `/thinking show <turn>` 打开独立、可滚动的完整详情；详情由 `DisplayTimeline` 或
  session transcript 的 UI-only Thinking archive 提供，不进入模型上下文。
- `/thinking summary <turn>` 收起详情并回到摘要模式。
- `Esc` 优先关闭详情，不退出 TUI。
- `/resume` 会恢复完整 session 中所有已完成 turn 的 Thinking 详情，而不仅是最近
  `resumeHistoryLimit` 条消息；不完整尾部 Thinking 不恢复。
- 命令不启用 mouse tracking，`Ctrl+O` 的 copy/scroll 模式保持原行为。

## 阶段边界固化

Thinking 与正文不再在同一个持续增长的 live viewport 中竞争可见行。事件 reducer
在以下边界关闭当前 streaming text segment：

```text
Thinking -> assistant text
assistant text -> Thinking
assistant/Thinking text -> tool 或 sub-agent
```

关闭后的 segment 通过现有 transcript command lane 进入 terminal scrollback；live
viewport 只保留当前未固化阶段。`displayTimeline` 仍是 live/transcript 的共同顺序
事实源，不从 `messages + toolActivity` 重新拼接顺序。

阶段提交有两个重要边界：

1. `transcriptPhaseCommittingSeq` 在 `tea.Println` 完成前隐藏已提交阶段，避免同一
   segment 同时出现在 viewport 和 terminal scrollback。
2. phase flush 完成前不会重新 arm stream channel reader。这样 terminal renderer
   先处理 Thinking 的 `tea.Println`，再消费正文事件，保证 scrollback 顺序不会因
   provider 快速发送而反转。

phase flush 只发生在阶段切换，不逐 token 写 terminal；正文阶段内部仍使用已有的
自适应 live render throttle。turn 完成 flush 仍是最终兜底，处理尚未跨越阶段边界的
最后一个 assistant segment。

阶段提交期间，后台任务/回顾等普通 transcript flush 会暂缓，不能抢占 phase
command lane；普通 flush 的 sequence 截止点也只推进到第一个未 ready 的 timeline
segment，不能跨过仍在 streaming 的正文。这样即使后台状态更新与阶段 ack 交错，
`printedSeq` 也不会把尚未写入 scrollback 的正文误标记为已打印。

终端 scrollback 一旦由 `tea.Println` 写出就不能可靠撤回或重绘。因此：

- `summary` 模式在 scrollback 中只打印摘要。
- 展开使用独立 viewport，不修改已经打印的摘要。
- 收起只关闭 viewport，不删除或重排 terminal history。
- 已经以 `full` 模式打印的旧 Thinking 不会因随后切到 `summary` 而被伪装成已收起；
  新模式只影响尚未提交及后续 phase。

## 收益、代价与回滚

正收益是提高长思考内容的可读性，并修复增量级 `TrimSpace` 导致的格式损坏。额外
成本仅为 TUI 展示阶段的行前缀和 Markdown 样式渲染，不增加模型 turn、token、工具
调用或 provider 延迟。窄终端会为轨道预留两列，因此正文每行比原来少两列。

回归重点覆盖隐藏开关、增量格式保真、窄屏换行、live/transcript 状态一致和自然
scrollback。若需回滚，只需恢复 TUI 的 thinking renderer 和增量拼接方式，不涉及
配置迁移、持久化迁移或 provider 兼容处理。

## 验证证据

自动化测试：

```text
go test ./internal/tui -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
```

新增单测覆盖 Thinking→正文、正文→tool、phase acknowledgement、sequence ownership
和已有 assistant/tool/agent 顺序回归。

折叠/展开真实 PTY：

```text
go test ./internal/tui -run TestThinkingCollapseExpandPTY -count=1 -v
```

该测试在 tmux 中启动真实 Bubble Tea，验证 summary-only scrollback、按 turn 展开全文、
再次收起、摘要不重复、正文不进入 scrollback，以及 `mouse=copy` 保持不变。

真实 PTY 验收：`TestStreamPhaseBoundaryPTY` 使用 tmux 启动真实 Bubble Tea 程序，
输入一条 prompt，流式产生 18 行 Thinking、18 行正文和最终哨兵。验收断言：

- `THINKING_LINE_01`、`THINKING_LINE_18`、`ANSWER_LINE_01`、`ANSWER_LINE_18` 和
  `PHASE_BOUNDARY_ANSWER_TAIL` 每个只出现一次；
- Thinking 尾部先于正文头部，正文头部先于最终正文哨兵；
- completed Thinking 在正文 live 阶段不再重复显示。

PTY 依赖 `tmux` 和可执行 TTY；没有 tmux 时测试会显式 skip，不会伪造通过。
