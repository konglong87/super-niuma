# TypeScript 到 Go 迁移方案

## 目标

把原 TypeScript/React Ink 项目迁移成 100% Go 实现：

- 不依赖 Node、Bun、TypeScript、React、Ink。
- 保留 CLI 行为、参数、输出格式、配置路径和会话格式。
- 保留 Claude API 流式对话、工具调用、MCP、插件、skills、权限、沙箱能力。
- 最终交付单个可分发 Go binary。

## 分层设计

| 原模块 | Go 模块 | 说明 |
| --- | --- | --- |
| `src/entrypoints/cli.tsx` | `cmd/golang-cc` + `internal/cli` | 启动快路径、参数解析、命令分发 |
| `src/main.tsx` | `internal/cli` | 命令树、非交互/交互入口 |
| `src/query.ts` | `internal/query` | 主循环、tool_use、max turns |
| `src/tools/*` | `internal/tools/*` | Go 工具接口和具体工具 |
| `src/services/mcp/*` | `internal/mcp` | MCP client/server |
| `src/components/*` | `internal/tui` | Bubble Tea 终端 UI |
| `src/utils/config/settings` | `internal/config` | settings、policy、managed config |
| `src/utils/sessionStorage` | `internal/session` | transcript、resume、history |
| `src/utils/permissions` | `internal/permissions` | 权限规则、危险操作检测 |
| `src/utils/sandbox` | `internal/sandbox` | 沙箱策略 |
| `src/plugins` / `src/skills` | `internal/plugins` / `internal/skills` | 扩展系统 |

## 第一阶段验收

- `go test ./...` 通过。
- `golang-cc --version` 可用。
- `golang-cc doctor` 可用，并输出 auth/config/session/git/shell 诊断。
- `golang-cc -p "..."` 可请求 Anthropic API。
- 模型可以调用 Go 实现的文件、搜索、todo、notebook、web、bash、MCP 和 task 工具并继续下一轮。
- 会话 transcript、resume/latest、compact、export markdown/json、usage/cost 已实现。
- MCP stdio/http tools/resources/prompts 和 CLI 管理命令已实现。
- plugins/skills/agents 发现与 show/list 命令已实现。
- background job registry、子进程执行、logs/attach/kill/ps 已实现。

## 后续开发优先级

1. 增加快照测试，对齐原版 `--help`、`doctor`、`-p` 输出和错误码。
2. 用 Bubble Tea/Lip Gloss 补完整交互式 TUI。
3. 补完整权限弹窗和更细粒度沙箱策略。
4. 扩展 MCP transport 到 SSE/streamable edge cases，并补 prompts/resources 快照。
5. 补插件安装/更新/市场语义。
6. 建立原 TypeScript 版本行为金标测试，逐命令对齐。
