# Transcript / Checkpoint 文档索引

golang-cc 会话存储（transcript / checkpoint / 文件历史 / rewind）的设计与修复文档集合。本文件是导航入口。

## 用户视角先看这里

- **怎么用**（命令、v2 消息图、非破坏 rewind/redo/branches、compact、resume）：[../session_quickstart.md](../session_quickstart.md)（§7.5「v2 消息图」）。
- **变更历史 / 版本**：[../../CHANGELOG.md](../../CHANGELOG.md)。

## 设计方案（按阅读顺序）

| 文档 | 内容 | 状态 |
|---|---|---|
| [transcript_schema_isolation_and_resume_plan.md](transcript_schema_isolation_and_resume_plan.md) | 路径隔离（`~/.golang-cc`）、schema detection（v1/v2/native/unknown/mixed）、**v2 entry 字段格式**（`session_meta`/`message`/`tool_call`/`tool_result`/`parent_id`/…）、sub-agent transcript 关联 | 路径隔离 + schema detection + v2 字段：已落地 |
| [transcript_checkpoint_optimization_plan.md](transcript_checkpoint_optimization_plan.md) | 存储职责拆分与文件历史生命周期：P0（正文外置内容寻址 blob、导出剥离）、P1（turn 去重、独立 GC）、并作为 P2 的入口 | P0/P1 已交付；P2 已实现 |
| [transcript_v2_message_graph_and_nondestructive_rewind_plan.md](transcript_v2_message_graph_and_nondestructive_rewind_plan.md) | **权威 P2 规格**：消息图（append-only 树 + `parent_id` + `branch_head`）、leaf-walk resume、非破坏 rewind/redo、messageId 键控文件历史、A–F 六阶段实施状态 | **已实现并默认开启** |
| [redo_file_restore_plan.md](redo_file_restore_plan.md) | redo 文件还原（best-effort + tombstone 守门）：`v2FileRestores` 两链差集、`afterAsBefore` 投影、tombstone 术语与两成因、best-effort vs 增强② 权衡、三项决策 + **实现记录（§10）** | **已实现并默认开启**（默认还原文件 + `--conversation-only` 逃生口） |

## 缺陷修复记录（v2 默认开启后发现）

| 文档 | 问题 | 状态 |
|---|---|---|
| [system_reminder_rewind_leak_fix.md](system_reminder_rewind_leak_fix.md) | 完成度闸门 `<system-reminder>` 被持久化进 transcript，污染 resume/inspect 与 `/rewind` 候选（非对外泄露） | 已修复（不再落盘 + 候选过滤） |
| [live_recorder_leaf_sync_after_rewind_fix.md](live_recorder_leaf_sync_after_rewind_fix.md) | 长驻 TUI 里 `/rewind` 后不产生分叉（旁路写 `branch_head` 未同步 live recorder 的 active leaf） | 已修复（turn 起点系统性重算 leaf，根治该类） |

## 核心不变量（实现约束）

- **transcript 永远 append-only**：rewind/redo/新 turn 只 append 一条 `branch_head`，什么都不删。
- **物理行序 ≠ 逻辑对话序**：所有消费方先由 active leaf 沿 `parent_id` 回溯出「当前链」再操作，统一入口 `session.CurrentChain` / `session.LoadConversation`（v1 恒等）。
- **绝不混写**：resume 恒随磁盘既有 schema 续写；v1 与 v2 靠 `session_meta.schema` 共存。
- **旁路写 `branch_head` 必同步 recorder**：任何绕过 live recorder 直接追加 `branch_head` 的操作（rewind/redo/by-id compact/外部 CLI），其 active leaf 由每个 turn 起点的 `Recorder.SyncLeafFromDisk` 统一对齐。

## 版本对应

- `v0.1.33-go`：transcript/checkpoint 存储优化 P0+P1；P2 方案就绪（未实现）。
- `v0.1.35-go`：**v2 消息图 + 非破坏 rewind 默认开启**（P2 A–F）+ 完成度闸门泄漏修复。
- `v0.1.36-go`：`/rewind` 后正确分叉 + recorder active leaf 系统性收口。
- `v0.1.37-go`：**redo 带文件还原**（默认开 + `--conversation-only`）——`v2FileRestores` 两链差集共用于 rewind/redo，命中 tombstone 严格原子中止。

## 代码落点（速查）

| 关注点 | 位置 |
|---|---|
| 图工具（CurrentChain/ActiveLeaf/ChainToLeaf/Leaves/LoadConversation/SyncLeafFromDisk） | [internal/session/graph.go](../../internal/session/graph.go)、[internal/session/store.go](../../internal/session/store.go) |
| 非破坏 rewind/redo/branches/compare | [internal/session/store.go](../../internal/session/store.go) |
| leaf-walk resume / turn 起点 leaf 对齐 | [internal/query/query.go](../../internal/query/query.go) |
| schema detection / resume 守门 | [internal/session/transcript_format.go](../../internal/session/transcript_format.go) |
| 原版 CC import | [internal/session/import_claude_code.go](../../internal/session/import_claude_code.go) |
| CLI（session branches/redo、transcript import-claude-code、/rewind /branches /redo 斜杠） | [internal/cli/cmd_session.go](../../internal/cli/cmd_session.go)、[internal/cli/cmd_transcript.go](../../internal/cli/cmd_transcript.go)、[internal/cli/cli.go](../../internal/cli/cli.go)、[internal/cli/interactive.go](../../internal/cli/interactive.go) |
