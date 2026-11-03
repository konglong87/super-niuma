# 如何使用 `--bare` 运行最小代码 Agent

`--bare` 是 CLI/TUI query 会话的可选 runtime profile。它保留文件读写、Shell、权限、会话和用户显式传入的配置，同时关闭自动发现和后台增强，适合需要可预测上下文与最小工具面的脚本、CI 和本地排查。

## 快速使用

启动 bare TUI：

```bash
niuma --bare
```

运行一次 headless 查询：

```bash
niuma --bare -p "读取 go.mod 并说明模块信息"
```

从源码运行时，把 `niuma` 替换为 `go run ./cmd/niuma`：

```bash
go run ./cmd/niuma --bare -p "总结 README.md"
```

`--bare` 是全局参数。与顶层子命令组合时应放在子命令之前；但 bare V1 只支持 query/TUI，会拒绝 `niuma --bare review` 这类普通顶层子命令。`--help` 和 `--version` 例外。

## 行为契约

| 能力 | bare 行为 |
| --- | --- |
| system prompt | 默认只包含 `niuma` 身份、CWD 和日期；仍可用 `--system-prompt[-file]` 替换或用 `--append-system-prompt[-file]` 追加 |
| 内置工具 | 默认仅 `Read`、`Edit`、`Bash` |
| 自动上下文 | 不加载 cwd、父目录、用户目录或 managed memory，不注入 Git snapshot、auto-memory protocol 和 skill catalog |
| hooks 与扩展 | 不执行 hooks，不装配 LSP，不自动发现 custom agents、plugins 或 MCP |
| 启动增强 | 跳过 startup update、recap、next-steps 和 TUI background watcher |
| 显式输入 | 保留 `--settings`、`--mcp-config`、`--add-dir`、权限参数、provider/model 和自定义 system prompt |
| 会话 | transcript 默认仍持久化，支持 `--resume`、`--continue`、`--session-id`；用 `--no-session-persistence` 才关闭持久化 |
| 后台运行 | 支持 `--bg` / `--background`，runtime profile 与影响 query 的显式参数会传播到后台子进程 |
| 安全边界 | 权限、sandbox、显式 deny 和危险命令判断保持不变；bare 不代表跳过权限 |

bare 的原则是：跳过没有显式请求的 runtime contributor，但保留显式输入和安全策略。

## 控制工具范围

`--tools` 只能从 bare 的三种内置工具中继续缩小，不能恢复默认 profile 的完整工具集：

```bash
# 只暴露 Read 和 Bash
niuma --bare --tools Read,Bash -p "检查项目但不要修改文件"

# Task 不属于 bare baseline，最终只暴露 Read
niuma --bare --tools Task,Read -p "读取 README.md"
```

`--allowedTools` 和 `--disallowedTools` 属于权限层，也不能扩大工具面。显式 `--mcp-config` 或显式 `--settings` 中声明的 MCP tools 可以加入 bare 会话，但仍受 `--tools` 和权限规则过滤。

## 显式加载目录与配置

`--add-dir` 在 bare 中同时表示额外可写目录和显式 context root：

```bash
niuma --bare \
  --add-dir /path/to/context-root \
  -p "按显式目录中的项目规则检查当前代码"
```

bare 只在给定 root 内读取受支持的指导文件和 rules，不向该目录的父目录扩散；越界的相对 include、绝对 include 和 `~/` include 会被拒绝。

`--settings` 仍可提供 model、provider、base URL、permissions、sandbox 和显式 MCP。即使 settings 中声明了 hooks，bare 也不会执行它们。自动发现的 settings MCP、additional directories 和项目配置物化不会重新进入 bare 会话。

## TUI 中可用的 slash command

bare TUI 只展示并接受与最小运行、会话和后台任务管理直接相关的 builtin command：

```text
/help /clear /status /tools /sessions /resume /model /permissions
/usage /compact /rewind /checkpoint /branches /redo
/ps /logs /attach /exit /quit
```

bundled skill 和 `--add-dir` 显式目录中的 slash skill 仍可按名称调用。project、user、plugin 和 marketplace skill 不会自动发现；`/plugins`、`/hooks`、`/recap` 等被关闭的 builtin 会返回 unavailable 错误。

## 支持与限制

| 组合 | 结果 |
| --- | --- |
| `--bare -p "..."` / `--bare` TUI | 支持 |
| `--bare --bg -p "..."` | 支持 |
| `--bare --resume ...` / `--continue` | 支持 |
| `--bare --prompt-mode code` | 支持 |
| `--bare --prompt-mode chat` | 拒绝；bare 是本地 code runtime profile |
| `--bare --agent <built-in>` | 支持显式内置 main-thread agent |
| `--bare --agent <custom>` | 拒绝；custom agent 自动发现已关闭 |
| `--bare <普通顶层子命令>` | 拒绝；V1 只支持 query/TUI |
| `--bare --tools default` | 使用 bare 默认三工具，不恢复完整 registry |

## 验证与排错

确认当前二进制公开该参数：

```bash
niuma --bare --help
```

在 bare TUI 中输入 `/tools`，默认应只看到 `Read`、`Edit`、`Bash`。如果工具更少，检查 `--tools`、`--allowedTools`、`--disallowedTools` 和 settings 中的权限规则；如果显式 MCP tool 没出现，同时检查 `--mcp-config` 是否加载成功以及工具过滤规则。

常见报错：

- `--bare requires --prompt-mode code`：移除 `--prompt-mode chat`，或不使用 bare。
- `--bare is only supported for query sessions`：改用 `--bare -p "..."` 或 bare TUI；普通子命令不接受 bare profile。
- `agent unavailable in --bare mode`：改用内置 agent，或退出 bare 以启用 custom agent discovery。
- `Task`、`WebSearch`、`Skill` 等没有出现在工具列表：这是预期行为；`--tools` 不能扩大 bare baseline。

## 与 Claude Code bare 的已知差异

niuma V1 没有 Claude Code 的 `--agents <json>` inline agent 和 `--plugin-dir` escape hatch。凭据继续使用 niuma 的 provider-neutral env/settings 解析，不硬编码 Anthropic API-key-only 规则。transcript 首写保持同步正确性，没有采用上游的异步首写优化。

当前自动化验证已经证明 bare 的 system bytes 和 tool definitions 少于 default，且无显式 MCP 时工具数为 3；30 次冷/暖进程级 p50/p95 基准仍是发布验收项，因此这里不承诺实际启动延迟或 provider 响应延迟收益。

架构、影响面、测试矩阵和回滚策略见 [bare runtime profile 技术方案](../architecture/bare_runtime_profile_design.md)，兼容状态见 [兼容性矩阵](../compatibility_matrix.md)。

---

返回 [CLI 模式使用说明](cli.md) 或 [使用说明总入口](README.md)。
