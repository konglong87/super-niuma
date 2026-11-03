# Goal Mode 文档入口

本目录是 Goal Mode 基线设计、后续方案、进度和 API/Mobile 扩展的主入口。早期基线设计已归入本目录的 `goal_mode_design.md`；新增优化、进度和验收证据也应继续放在本目录。

## 推荐阅读顺序

1. [Goal Mode 基线设计](goal_mode_design.md)：状态机、基础 runner、CLI/TUI、本地存储和早期非目标。
2. [Goal API / Mobile TODO](goal_api_mobile_todo.md)：API Server、MySQL tenant store、Mobile WebSocket 的历史扩展计划和验证记录。
3. [Goal Mode 优化方案](goal_mode_optimization_plan.md)：结构化 plan、可达性分析、evidence-first evaluator、预算策略和 API/Mobile/WebUI 后续架构。
4. [Goal Mode 优化 TODO](goal_mode_optimization_todo.md)：当前实施状态、验收标准、建议测试和剩余项。

## 维护规则

- 新的 Goal 优化方案、TODO、进度和验证证据放在 `docs/goal_mode/`。
- 行为基线或兼容性说明可链接到 `docs/goal_mode/goal_mode_design.md`，后续阶段内容不要散落到顶层 docs。
- API 行为变化必须同步 [../api_server.md](../api_server.md) 和 Swagger 生成文件。
- MySQL、Mobile/WebSocket、budget/evaluator、continuous runner 等高风险改动必须在 TODO 文档记录状态和验证命令。
