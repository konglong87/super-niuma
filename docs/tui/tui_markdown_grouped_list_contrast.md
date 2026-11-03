# TUI Markdown 分组列表对比度设计

长 Markdown 表格在终端宽度不足或单元格过长时，会降级为分组列表。该列表属于助手
正文，不应和 recap 或 `Responded in` 等状态元信息使用同一视觉层级。

本次仅调整两种专用样式：

- 标题行（`• P0 · 问题标题`）使用接近正文的亮度。
- 详情行（`类型: Bug`、`修复难度: 中`）使用次级正文亮度。

表格识别、列表转换、换行宽度、recap、Thinking、普通 Status 和响应元信息均不变。
目标层级为：正式正文 > 分组列表标题 > 分组列表详情 > Thinking > recap/状态元信息。

该调整属于 `RT-OUTPUT` 的 `B1_SCENARIO` 局部视觉变更。无 provider、prompt、token、
turn、工具调用、持久化或协议影响；现有 runtime topology 节点和因果边保持准确。
