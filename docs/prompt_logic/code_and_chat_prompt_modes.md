# Code and Chat Prompt Modes

本文定义 Go Claude 的两类 prompt profile，避免代码开发 agent 和普通多租户 chat agent 共用同一套本地项目上下文。

## 目标

Go Claude 需要同时服务两类场景：

- 代码模式：CLI、TUI、本地开发、review、修复、提交、内部 API 测试 cockpit。
- 普通多租户 chat 模式：Mobile Chat、OpenAI-compatible tenant chat、SaaS 普通助手。

这两类场景不能共享本地代码上下文。普通 chat 不能默认读取服务端进程 cwd 下的 `CLAUDE.md`、git branch、git status 或 `.claude/rules`，否则会污染用户聊天，甚至泄露部署机器的代码仓库信息。

## 模式

| 模式 | 代码名 | 默认入口 | 加载策略 |
| --- | --- | --- | --- |
| 代码模式 | `code` | CLI、TUI、headless print、本地 `/query` 兼容路径 | 加载项目规则、git 快照、开发工具 guidance。 |
| 普通 chat 模式 | `chat` | `/v1/chat/completions`、`/mobile/chat/...` | 不加载本地代码规则和 git 信息，只保留普通助手/租户上下文。 |

## Code Mode

代码模式加载：

- managed memory：`/etc/claude-code/CLAUDE.md`，或 `GOLANG_CLAUDE_CODE_MANAGED_MEMORY` / `CLAUDE_CODE_MANAGED_MEMORY` 指定的文件列表
- tenant DB managed memory：API Server tenant runtime 下，通过 `/tenant/managed-memory` 管理的 `managed` category memory
- `~/.claude/CLAUDE.md`
- `CLAUDE_CONFIG_DIR/team/CLAUDE.md`、`CLAUDE_CONFIG_DIR/team/TEAM.md`、`CLAUDE_CONFIG_DIR/memory/team.md`、`CLAUDE_CONFIG_DIR/TEAM.md`
- tenant DB team memory：API Server tenant runtime 下，通过 `/tenant/team-memory` 管理的 `team` category memory
- `CLAUDE_CONFIG_DIR/memory/auto.md`、`CLAUDE_CONFIG_DIR/memory/AUTOMEM.md`、`CLAUDE_CONFIG_DIR/AUTOMEM.md`
- cwd 向上查找的 `CLAUDE.md`
- cwd 向上查找的 `.claude/CLAUDE.md`
- cwd 向上查找的 `.claude/rules/*.md`
- cwd 向上查找的 `CLAUDE.local.md`
- memory 文件内的 `@include`
- frontmatter `paths`
- git branch、main branch、`git status --short`、最近 commits
- output style、language、MCP instructions、skills catalog、prompt cache boundary

代码模式的分层：

```text
system prompt:
  stable default behavior
  tool guidance
  output style
  environment
  git snapshot

user context meta message:
  CLAUDE.md
  .claude/CLAUDE.md
  .claude/rules/*.md
  CLAUDE.local.md
  managed/team/auto memory

system addendum in API Server tenant runtime:
  tenant DB managed/team memory
```

## Chat Mode

普通 chat 模式不加载：

- git status / git branch / recent commits
- 本地 cwd 项目路径 guidance
- `~/.claude/CLAUDE.md`
- 项目 `CLAUDE.md`
- `.claude/CLAUDE.md`
- `.claude/rules/*.md`
- `CLAUDE.local.md`
- 本地开发 skills catalog

普通 chat 模式保留：

- 明确的普通助手 system prompt
- 显式传入的 `system_prompt`
- response format / JSON schema 输出约束
- tenant session history / mobile attachments
- tenant memory、tenant profile、tenant `CLAUDE.md` document
- tenant knowledge base top chunks：`/tenant/knowledge/documents` 入库，`/tenant/knowledge/search` 检索，server query 运行时按当前 prompt 注入 top chunks
- 显式 remember 安全写回：用户明确说 `记住...` / `remember that...` 时，只写入 `explicit_pending` 待审候选；pending 不进入 prompt，approve 后才转为 active user memory。
- AutoMem 安全写回：默认关闭；设置 `GOLANG_CLAUDE_CODE_AUTOMEM_WRITEBACK=true` 或 `CLAUDE_CODE_AUTOMEM_WRITEBACK=true` 后，tenant query 会把明确的偏好/项目事实/约定写入 `automem_pending` 待审候选，approve 后才进入 prompt。

## 默认路由

| 入口 | 默认 prompt mode |
| --- | --- |
| CLI / TUI | `code` |
| `-p` / headless print | `code` |
| 本地 `/query` | `code`，保留兼容；可用 `GOLANG_CLAUDE_CODE_SERVER_DEFAULT_PROMPT_MODE=chat` 改成普通 chat 默认 |
| `/v1/chat/completions` | `chat` |
| `/mobile/chat/...` | `chat` |
| tenant agent tasks | 默认 `chat`，显式 code task 才可切换 |

显式开关：

```bash
--prompt-mode code
--prompt-mode chat
GOLANG_CLAUDE_CODE_PROMPT_MODE=code
GOLANG_CLAUDE_CODE_SERVER_DEFAULT_PROMPT_MODE=chat
```

API 字段：

```json
{
  "prompt_mode": "code"
}
```

Mobile Chat 默认不允许普通用户通过请求打开 `code` 模式；只有服务端受控配置或内部调试链路可以覆盖。

## 验收

- `code` 模式能看到 `.claude/rules/*.md` 和 git 快照。
- `chat` 模式看不到本地 `CLAUDE.md`、`.claude/rules`、git branch、git status。
- tenant chat 能看到当前认证 tenant/user 的 memories、profile 和 active `CLAUDE.md` document。
- `/v1/chat/completions` 和 `/mobile/chat/...` 默认是 `chat`。
- CLI/TUI 默认是 `code`。
- prompt cache boundary 仍保持静态段在前、动态段在后。

## 可观测性

每次 query 会发出 `query.prompt_context` telemetry event，并在本地 transcript 中写入 `prompt_context` entry。manifest 只记录来源、模式、计数和布尔状态，不记录 prompt 正文、memory 正文、知识库正文或文件路径。

manifest 覆盖：

- `mode` / `profile` / `query_source`
- system blocks、initial messages、user context messages、attachments
- code memory 文档数量、按类型计数、include/path/exclude 计数
- git context、MCP instructions、skills catalog、prompt cache、auto compact
- tenant memory/profile/document/knowledge chunk 计数
- tenant managed/team memory 是否注入
