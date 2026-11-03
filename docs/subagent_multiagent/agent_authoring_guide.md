# Agent Authoring Guide

本文档面向 golang-cc 的 subagent / multi-agent 作者，说明 agent 文件位置、schema、运行时语义、限制、常见错误和验证方式。目标是功能等价 Claude Code 的公开 agent 行为，不追求上游私有源码结构逐行一致。

## 文件位置和优先级

golang-cc 会从三个来源加载 Markdown agent 文件：

| 来源 | 路径 | 说明 |
| --- | --- | --- |
| user agent | `~/.claude/agents/*.md` | 当前用户全局可用。 |
| project agent | `<project>/.claude/agents/*.md` | 从当前工作目录向上查找最近的 `.claude` 目录。 |
| plugin agent | `<project>/.claude/plugins/<plugin>/agents/*.md` 或 plugin manifest 中的 `agents` 目录 | 随项目 plugin 提供。 |

同名 agent 的优先级固定为：

```text
project agents > plugin agents > user agents
```

`golang-cc agents list` 和 `golang-cc agents show <name>` 会显示最终生效的 agent。JSON 输出包含 `source`，plugin agent 还会包含 `plugin`。

## Agent 文件格式

Agent 文件是带 YAML frontmatter 的 Markdown。Frontmatter 负责声明 schema 字段，正文是该 agent 的 system prompt。

```markdown
---
name: reviewer
description: Review focused Go changes and report concrete risks.
tools: [Read, Grep, Task]
disallowedTools: [Bash]
mcpServers:
  - docs
skills:
  - review
model: inherit
maxTurns: 4
permissionMode: ask
effort: high
memory: project
---

You are a focused code reviewer.

Report findings first, ordered by severity, with file and line references.
```

如果未声明 `name`，会使用文件名去掉 `.md` 后的值。如果未声明 `description`，会使用正文第一条非空行。

## Schema 字段

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `name` | string | Agent 名称。`Task` / `AgentCreate` 的 `subagent_type` 使用该名称。 |
| `description` | string | 用于列表、模型工具描述和人工识别。 |
| `tools` | string 或 string list | Agent 允许使用的工具集合。支持 `tools: Read, Grep` 和 YAML 数组。 |
| `disallowedTools` | string 或 string list | Agent 禁止使用的工具集合，优先级高于 `tools`。 |
| `mcpServers` | string list 或 object list | Agent-local MCP server spec。字符串表示继承/引用名；对象表示本 agent 局部注册。 |
| `criticalSystemReminder_EXPERIMENTAL` | string | 追加到子 agent system prompt 的关键提醒。 |
| `skills` | string 或 string list | 预加载指定 skill 的 `SKILL.md` 到子 agent prompt。 |
| `model` | string | 子 agent 模型。`inherit` 或空值表示继承当前模型。 |
| `initialPrompt` | string | main-thread `--agent <name>` 的首轮初始 prompt 前缀。 |
| `maxTurns` | integer | 覆盖子 agent 或 main-thread agent 的最大轮数。 |
| `background` | boolean | `Task` 调用该 agent 时以后台任务运行，立即返回 task handle。 |
| `memory` | string/object | 记录并注入 agent scoped memory。非字符串会以 YAML 文本保留。 |
| `effort` | string/object | reasoning effort。会进入运行时 metadata、cache-safe signature，并在 Anthropic provider 映射为 SDK thinking config。 |
| `permissionMode` | string | Agent 默认工具权限模式。 |

支持这些 snake_case alias：

| Canonical | Alias |
| --- | --- |
| `disallowedTools` | `disallowed_tools` |
| `mcpServers` | `mcp_servers` |
| `initialPrompt` | `initial_prompt` |
| `maxTurns` | `max_turns` |
| `permissionMode` | `permission_mode` |

未知 frontmatter 字段不会丢弃；`agents show --json` 会在 `unknown_fields` 中展示，方便迁移和 strict/warning 模式后续扩展。

## 工具权限语义

工具可用性按以下顺序收敛：

1. 当前会话或父 agent 的工具 registry。
2. Agent `tools` allow list。
3. Agent `disallowedTools` deny list。
4. Agent / session permission policy。
5. coordinator mode 的专用 allowlist。

`disallowedTools` 永远不能被 `tools` 重新放开。guarded tools 会在执行层再次检查 agent policy，因此即使模型硬调被禁止工具，也会被拒绝或提示。

`permissionMode` 支持 golang-cc 已归一化的权限模式，例如 `ask`、`deny`、`allow`、`auto`、`plan`、`bypassPermissions`。为保持安全边界，agent 级 `bypassPermissions` 会降级为 `allow`，不能越过父会话或 session deny 规则。

## MCP、skills 和 memory

`mcpServers` 可以写成字符串引用：

```yaml
mcpServers:
  - docs
```

也可以写成局部配置对象：

```yaml
mcpServers:
  - local-docs:
      command: docs-mcp
      args: ["--root", "."]
```

对象配置只注册到当前 agent 的局部 registry，不污染父线程。字符串引用会作为继承 MCP context 进入 prompt。

`skills` 会把指定 skill 的 `SKILL.md` 内容预加载到子 agent system prompt；skill 的 allowed tools、model、agent metadata 不会污染 main thread。

Agent scoped memory 支持三个位置：

```text
~/.claude/agent-memory/<agent>/MEMORY.md
<project>/.claude/agent-memory/<agent>/MEMORY.md
<project>/.claude/agent-memory-local/<agent>/MEMORY.md
```

这些 memory 只注入目标 agent，不注入其他 agent 或主线程。

## 运行方式

同步子 agent：

```json
{
  "description": "review the current diff",
  "prompt": "Read the diff and report bugs only.",
  "subagent_type": "reviewer"
}
```

批量同步子 agent：

```json
{
  "tasks": [
    {"description": "review api", "prompt": "Review API changes and report bugs only."},
    {"description": "review tui", "prompt": "Review TUI changes and report bugs only."}
  ],
  "max_concurrency": 2,
  "timeout_ms": 60000
}
```

多任务 batch 会并发启动多个独立模型 loop，包含模型启动、工具定义下发和工具往返开销。`timeout_ms` 省略或为 `0` 表示不设置超时；如果显式设置，多任务 batch 必须不小于 `30000`。简单文件搜索、计数、路径匹配等轻量工作应优先由父 agent 直接调用 `Glob` / `Grep` / `LS`，不要为了测试并发而绕到 sub-agent。

后台 agent：

```markdown
---
name: researcher
description: Research independently in the background.
background: true
maxTurns: 8
---

Work independently and persist progress through task events.
```

当 `Task` 调用 `researcher` 时，golang-cc 返回 `status=running`、`task_id`、`session_id`、`agent_name`、`model`、`permission_mode`。调用方可继续用 `AgentGet`、`AgentMessage`、`AgentStop` 管理。

main-thread agent：

```bash
golang-cc --agent reviewer "review the last commit"
```

`--agent` 会加载 agent prompt、model、maxTurns、tools、disallowedTools 和 permissionMode。用户显式传入的 `--model`、`--permission-mode` 优先于 agent 配置。`initialPrompt` 会作为普通 prompt 文本拼接到首轮用户输入前；当前不会把其中的 slash command 特判为交互式命令执行。

coordinator / teammate：

```json
{"prompt":"Run the release checklist.", "mode":"teammate", "subagent_type":"release-coordinator"}
```

`AgentCreate mode=teammate` 使用同进程 background runtime，保留 agent policy 和 registry clone 隔离。coordinator prompt 生效时，只暴露协调工具：`Task`、`TaskOutput`、`AgentCreate`、`AgentList`、`AgentGet`、`AgentStop`、`AgentMessage`、`AskUserQuestion`、`TodoRead`、`TodoWrite`。

## Plugin agent 示例

Project plugin 可直接提供 `agents/` 目录：

```text
.claude/plugins/demo/plugin.json
.claude/plugins/demo/agents/reviewer.md
```

也可以在 manifest 中声明 agent contribution 目录：

```json
{
  "name": "demo",
  "agents": ["custom-agents"]
}
```

Plugin agent 使用同一套 parser 和完整 schema。执行 `plugins validate` 时，manifest 中声明的 agent path 必须存在且必须是目录。

## 常见错误

| 错误 | 原因 | 修复 |
| --- | --- | --- |
| `yaml: ...` | Frontmatter 不是合法 YAML。 | 用 `---` 包住 frontmatter，并检查缩进和引号。 |
| `expected string list item` | `tools`、`skills`、`disallowedTools` 数组里出现对象或嵌套数组。 | 改成字符串数组或逗号分隔字符串。 |
| `agent not found` / 未加载目标 agent | `subagent_type` 与有效 `name` 不一致，或文件不在加载路径。 | 用 `agents list` 和 `agents show <name>` 确认最终名称和来源。 |
| `plugin ... agent path is not a directory` | plugin manifest 中的 `agents` path 不存在或不是目录。 | 修正 manifest 或创建目录。 |
| 工具被拒绝 | 被 `disallowedTools`、父会话 deny、agent permission policy 或 coordinator allowlist 拦截。 | 用 `agents show --json` 检查工具配置，并查看 trace/task events。 |
| background agent 报 requires task store | 当前运行入口没有 agent task store。 | 使用 API Server / tenant task store 或带 task store 的 runtime 入口。 |

## 验证命令

基础 authoring 验证：

```bash
golang-cc agents list
golang-cc agents show reviewer
golang-cc agents show reviewer --json
golang-cc agents doctor
golang-cc agents doctor --json
```

`agents doctor` 会检查常见 authoring 问题，包括空名称、空 prompt、无效 `maxTurns`、无效 `permissionMode`、未知 frontmatter 字段、`tools` 与 `disallowedTools` 冲突、异常 `memory` / `effort` 值，以及 `background` 搭配 `initialPrompt` 这类容易混淆的配置。

实现回归验证：

```bash
go test ./internal/agents ./internal/plugins ./internal/agentruntime ./internal/tools -count=1
go run ./cmd/golang-cc eval agents --json
```

项目级最终验证仍建议运行：

```bash
go test ./... -count=1
git diff --check
```

## 当前功能等价边界

以下行为已经作为 golang-cc 的公开功能语义实现并有测试覆盖：完整 agent schema 解析、project/plugin/user 优先级、tools/disallowedTools、agent-local MCP、skills preload、agent memory、maxTurns、background task、permissionMode、SubagentStart/SubagentStop hooks、message bus、coordinator allowlist、teammate mode、task API、WebUI Agents cockpit、deterministic parity eval。

以下属于有意降级或后续非主干增强：

- `initialPrompt` 中的 slash command 当前按普通 prompt 文本进入 query，不执行交互式 slash command 处理器。
- `effort` 已解析、记录 metadata，并纳入 cache-safe signature；Anthropic provider 会映射到 SDK thinking config，OpenAI-compatible provider 保持标准 Chat Completions payload，不注入非标准字段。
- Agent 级 `bypassPermissions` 会降级为 `allow`，不能覆盖父会话或 session deny。
- HTTP create agent task API 只创建管理/观测元数据，不直接启动模型运行；真正执行由 `Task` / `AgentCreate` runtime 工具负责。
- Teammate 的 cron trigger 不属于当前 subagent/multi-agent parity 主干，已有 `/loop` scheduler 可覆盖定时后台任务场景。
