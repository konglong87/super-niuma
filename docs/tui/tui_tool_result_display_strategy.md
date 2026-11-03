# TUI 工具结果正文展示策略

## 目标

让用户在对话正文中直接看到关键命令的可读输出，尤其是 `git diff`，同时保留现有工具摘要、权限交互和模型上下文行为。

## 第一版范围

- 自动展示 Bash 执行的 `git diff`、`git show`、`git status`、`git log`（包括常见 `--stat`、`-C`、`cd ... &&` 和环境变量前缀）。
- 普通 Bash 命令仍只显示摘要；`Ctrl+T` 继续用于工具详情展开。
- 关键输出默认最多 100 行、12,000 字符；超出显示截断提示，展开后最多 1,000 行、100,000 字符。
- Diff 的新增行、删除行和 hunk 行使用独立颜色，普通上下文使用正文色。

## 不变量

- 只消费已有 `toolActivityItem.RawOutput`，不重新执行命令。
- 不修改发送给模型的 tool result、prompt、token、turn、provider、transcript 协议或持久化内容。
- gate-preflight 结果继续使用原有友好摘要，不被误判为普通 Git 输出。
- 当前执行视图和归档 transcript 使用同一展示策略。

## 影响评估

变更节点为 `RT-OUTPUT`，爆炸半径为 `B1_SCENARIO`。工具执行、证据、持久化和协议消费者保持不变。回滚只需移除 TUI 关键输出策略和对应样式，不涉及数据迁移。
