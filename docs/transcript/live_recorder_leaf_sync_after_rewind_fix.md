# 修复：/rewind 未同步 live recorder，导致「回退后不产生分叉」

> 状态：已修复并验证。发现于真实会话验收：`/rewind` 后 `/branches` 只显示 1 条分支。

## 1. 现象

真实 TUI 会话里 `/rewind <msg>`（提示「Conversation moved back N messages; previous branch preserved」）之后继续对话，再 `/branches`，**只列出 1 条分支**——被"回退"的旧分支没有作为第二条分支出现。

## 2. 根因（铁证）

在出问题的会话文件里，`rewind` 的 `branch_head` 之后紧接的新一轮 checkpoint，其 `parent_id` 指向的是**回退前的旧 tip**，而不是 rewind 目标：

```
[105] message     id=a876fa67 parent=a95331fd     ← rewind 前的旧叶（老 tip）
[106] branch_head leaf=df156529 reason=rewind      ← /rewind 写入的指针（目标 df156529）
[107] checkpoint  id=23144aa8 parent=a876fa67 ★    ← 下一轮却挂到老 tip a876fa67，而非 df156529
```

`/rewind`（`rewindSlashCommand`）通过 `session.DefaultStore().RewindConversationToMessage` **旁路**把 `branch_head` 追加进文件，但**没有更新仍然打开的 live `Recorder` 的内存 `leaf`**——它还停在回退前的旧 tip。于是：

1. 下一轮的 checkpoint 从旧 tip 线性接了下去（第 107 行）；
2. per-turn 的 `new_turn` `branch_head` 又把 active leaf 拉回这条线；

净效果：**rewind 没有产生分叉**，整条会话仍是一条直线、被"回退"的消息还留在当前链上，因此 `/branches` 只有 1 条。

> 这与之前 `/compact` 的问题同类：**任何绕过 live recorder、直接往文件追加 `branch_head` 的操作（rewind / redo / by-id compact），都必须让 live recorder 的 `leaf` 重新对齐文件的 active leaf**，否则后续写入会从过期 tip 接续，悄悄抵消该操作。

## 3. 方案（系统性收口，非逐命令打补丁）

该 bug 已两次复发（先 `/compact`、再 `/rewind`），根源都是「旁路写 `branch_head` 后 live recorder 的 `leaf` 过期」。因此不再逐个命令补 sync，而是**在唯一的 turn 执行入口做一次系统性对齐**：

- 新增 `Recorder.SyncLeafFromDisk()`：重读 transcript，把 v2 recorder 的 `leaf` 置为文件当前 active leaf（`ActiveLeaf`）；v1 恒为 no-op。
- 在 `query.Session.run` 的**每个 turn 起点、首次 append 之前**调用它。这样自上一轮以来任何旁路移动过 active leaf 的操作——`/rewind`、`/redo`、by-id `compact`、甚至另一个终端的 CLI 操作——都会在下一轮被自动"认账"：新一轮从当前 leaf 接续 → 正确分叉（旧分支保留为第二条叶、`/branches` 可见、`/redo` 可回）。

`run` 是 CLI/TUI/server/goal 共用的唯一 turn 执行器，一处对齐即覆盖全部路径；`/rewind`、`/redo` 处不再各自 sync（读路径本就走文件、命令与下一轮之间不经 recorder 追加，故无需）。

## 4. 建议与理由

- **为何系统性（turn 起点重算）而非逐命令 sync**：逐命令是"打地鼠"——每新增一个旁路写 `branch_head` 的入口就得记得补一次，漏一个就复发。turn 起点统一重算把不变量收敛到一处：**live recorder 每轮开工前都信任磁盘的 active leaf**，从根上杜绝这类。
- **成本可忽略**：每轮多一次文件读+解析（仅 v2 recorder；v1 直接 no-op）。turn 由人/模型步进，相对一次模型调用可忽略；且 turn loop 本就在近处读取 transcript 构建上下文。
- **不影响** files-only rewind（不移动对话叶，重载幂等）、v1 会话（no-op）、rewind 的上下文重建（本就正确）。

## 5. 影响 / 存量会话

- 修复后：`/rewind` 后继续对话会正确分叉；`/branches` 显示旧分支 + 新分支；`/redo` 可回旧分支。
- **已损坏的存量会话无法自动修复**：在修复前发生的 rewind 已经把会话写成了一条直线（旧内容仍在当前链上），这属于既成事实、不影响数据完整性，只是那一次 rewind 事实上没生效。新会话与后续 rewind 均正确。

## 6. 验证

- 新增 `TestV2RewindThenContinueForksAfterLeafSync`（[internal/session](../../internal/session)）：录两轮 → 旁路 `RewindConversationToMessage` → `SyncLeafFromDisk` → 继续一轮，断言形成**两条叶**（旧分支保留）、当前链只含新分支、不含被回退内容。移除 sync 该测试即失败（正是本 bug）。
- `go test ./...` 全量绿。

## 7. 落点

| 改动 | 位置 |
|---|---|
| `Recorder.SyncLeafFromDisk()` | [internal/session/store.go](../../internal/session/store.go) |
| **turn 起点系统性对齐**（唯一入口） | [internal/query/query.go](../../internal/query/query.go) `Session.run`（首次 append 前） |
| 回归测试 | [internal/session/rewind_v2_test.go](../../internal/session/rewind_v2_test.go) `TestV2RewindThenContinueForksAfterLeafSync` |
