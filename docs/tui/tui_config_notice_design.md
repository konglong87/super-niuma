# TUI 配置热更新通知设计

## 目标与边界

配置热更新的 readback 必须保留在 transcript 中，但不应以两行普通 `Status` 抢占
对话正文。TUI 将真实配置 reload 标记为专用 `config_notice`，以单行弱提示展示；
session、Goal、rewind 等普通状态保持现有渲染。

该变更属于 `RT-OUTPUT`，爆炸半径为 `B1_SCENARIO`。它不改变配置加载、权限切换、
provider、Thinking、Web UI、持久化协议或运行时 gate，现有 topology 节点和因果边仍
然准确。

## 展示与合并规则

- 宽屏格式为 `↻ Config · permissions ask → allow`。
- 窄屏按终端显示宽度换行，后续行与变化详情起点对齐，不截断字段。
- 连续且尚未提交到 scrollback 的通知合并为一个 segment；已经进入提交边界的通知
  不再修改，后续变化追加新 segment。
- 事件通过 `StreamNoticeConfigReload` 表达语义，渲染层不匹配英文文案来判断类型。
- 通知继续写入自然 scrollback，不做会自动消失、无法审计的 toast。

## 收益、代价与回滚

正收益是减少一行固定标题并降低视觉权重，同时保留配置变化证据。成本仅限 TUI
segment 合并和文本换行，不增加模型 turn、token、工具调用或配置 reload 延迟。

回归重点覆盖宽屏单行、窄屏悬挂缩进、连续通知合并、提交边界，以及普通 Status
隔离。回滚只需恢复 `StreamConfigReload` 到普通 status 的映射，无配置或数据迁移。
