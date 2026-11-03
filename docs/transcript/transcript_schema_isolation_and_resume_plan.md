# Transcript Schema 隔离与 Resume 修复技术方案

## 背景

go-claude 当前主会话 transcript 路径基本对齐原版 Claude Code：

```text
~/.claude/projects/<project-slug>/<session-id>.jsonl
```

这个路径对齐本身不是问题。真正的问题是：**路径相同，但 JSONL schema、resume 语义和 sub-agent transcript 结构不一致**。

go-claude 当前 transcript entry 以简化事件为主：

```json
{"type":"message","role":"user","content":"hello"}
{"type":"tool_call","tool_id":"toolu_1","tool_name":"Read","content":"{\"file_path\":\"README.md\"}"}
{"type":"tool_result","tool_id":"toolu_1","tool_name":"Read","content":"ok"}
```

原版 Claude Code transcript 更接近完整 message chain：

```json
{
  "type": "user",
  "uuid": "...",
  "parentUuid": "...",
  "sessionId": "...",
  "cwd": "...",
  "version": "...",
  "message": {
    "role": "user",
    "content": []
  }
}
```

两者共用 `~/.claude/projects` 会让各自的 resume/session list 看到对方文件。由于 schema 不一致，可能造成空会话、断链、错误恢复、甚至同一个 JSONL 被两套系统混写。

## 问题定义

### P0：默认路径污染原版 Claude Code

go-claude 默认写入 `~/.claude/projects/<slug>`，而原版 Claude Code 也扫描该目录。这会导致：

- 原版 session picker 可能显示 go-claude transcript。
- go-claude resume list 可能显示原版 transcript。
- 用户如果误 resume，可能把两种 schema 写入同一个 `.jsonl`。

### P0：schema 不可识别

go-claude 当前 transcript 没有稳定 schema meta。读取方只能靠 entry 字段猜测格式，无法可靠判断：

- 文件来自 go-claude 还是原版 Claude Code。
- 文件是主会话、sub-agent 会话还是 fork 会话。
- 文件 schema version 是 v1、v2 还是未知格式。
- 当前 parser 是否可以安全 append。

### P0：resume 兼容边界不清晰

go-claude `MessagesFromTranscript` 只理解 go-claude 的 `message/tool_call/tool_result/compact_summary` 简化事件。原版 Claude Code 依赖 `uuid/parentUuid/message/sessionId` chain。两边不能直接互相 resume。

### P1：sub-agent transcript 关联不完整

原版 Claude Code sub-agent transcript 位于主 session 目录下：

```text
~/.claude/projects/<slug>/<session-id>/subagents/agent-<agent-id>.jsonl
```

go-claude 当前 sub-agent 有自己的 `TranscriptPath`，但主 transcript 和 sub-agent transcript 的可恢复关联还不够强。主会话 resume 后可以看到 Task 工具结果，但不一定能稳定找到完整 sub-agent 过程。

## 目标

1. go-claude 新会话默认不再污染原版 Claude Code transcript 目录。
2. go-claude transcript 有明确 schema meta，可被稳定识别、过滤、迁移。
3. go-claude resume/list 只默认处理自己支持的 transcript。
4. 原版 Claude Code transcript 只能通过显式 import/read-only compat 流程处理，不能默认混入。
5. go-claude v1 transcript 可迁移到 v2。
6. sub-agent transcript 与父会话、父 tool use、task id 有稳定关联。
7. resume 后主 agent 能恢复主消息、工具调用、工具结果、compact summary、sub-agent summary 和 sub-agent transcript 索引。
8. 保留诊断能力：从主 transcript 可以定位每个 sub-agent transcript 和 persisted tool result。

## 非目标

- 不在本阶段承诺 100% 复刻原版 Claude Code transcript schema。
- 不让原版 Claude Code 原地 resume go-claude transcript。
- 不让 go-claude 默认原地 append 原版 Claude Code transcript。
- 不自动导入所有历史原版 transcript，避免误转换 sidechain、content replacement、remote agent、compact 等复杂语义。

## 总体方案

采用三层架构：

```text
Storage Namespace
  隔离 go-claude 默认写入路径，避免污染原版 Claude Code。

Transcript Schema
  定义 go-claude transcript v2，支持主会话、工具、sub-agent、compact、file change、usage。

Compatibility Layer
  识别 go-claude v1、go-claude v2、Claude Code 原版和未知格式；默认只 resume 安全格式。
```

## 路径隔离方案

### 默认新路径

go-claude 默认 transcript root 改为：

```text
~/.go-claude/projects/<project-slug>/<session-id>.jsonl
```

sub-agent transcript：

```text
~/.go-claude/projects/<project-slug>/<session-id>/subagents/agent-<agent-id>.jsonl
```

persisted tool result：

```text
~/.go-claude/projects/<project-slug>/<session-id>/tool-results/<tool-use-id>.txt
```

### 路径与配置语义

新增 go-claude 专属环境变量，避免复用原版 `CLAUDE_CONFIG_DIR` 产生误写：

```text
GOLANG_CLAUDE_CODE_CONFIG_DIR
GOLANG_CLAUDE_CODE_TRANSCRIPT_PROJECTS_DIR
```

这里必须区分两个概念，避免实现时出现 `projects/projects` 这类路径错误：

- `ConfigRoot`：go-claude 配置根目录，例如 `~/.go-claude`。
- `TranscriptProjectsRoot`：transcript projects 目录，例如 `~/.go-claude/projects`。

`session.Store` 这类底层结构如果仍表达为 config root，就应继续在内部拼接 `projects/<project-slug>`；如果表达为 projects root，就只能拼接 `<project-slug>`，不能再追加一层 `projects`。

推荐实现：

```go
type Store struct {
    ConfigRoot             string
    TranscriptProjectsRoot string
}
```

解析优先级：

1. `GOLANG_CLAUDE_CODE_TRANSCRIPT_PROJECTS_DIR`，直接作为 `TranscriptProjectsRoot`。
2. `GOLANG_CLAUDE_CODE_CONFIG_DIR/projects`。
3. `~/.go-claude/projects`。

`CLAUDE_CONFIG_DIR` 只保留用于读取 legacy 配置或用户显式兼容模式，不再作为 transcript 默认写入 root。

### Legacy 读取策略

旧 go-claude transcript 仍可能存在：

```text
~/.claude/projects/<slug>/<session-id>.jsonl
```

go-claude 可以在 resume list 中显示“可迁移 legacy go-claude transcript”，但必须明确标注来源，并在 resume 前迁移到新目录。不得原地 append 旧路径，除非用户显式指定 `--unsafe-legacy-transcript-append`。

## go-claude Transcript v2 Schema

### 设计原则

- JSONL append-only。
- 第一条必须是 `session_meta`。
- 每条 entry 都有 `schema`、`id`、`timestamp`、`session_id`、`turn_id`。
- message 使用 `parent_id` 串联，工具调用使用 `tool_use_id` 保持 Anthropic 协议关联。
- tool call/result 必须记录 `assistant_message_id`，用于把并行工具结果恢复成紧随原 assistant tool_use 后的一条 user tool_result message。
- sub-agent 以父会话事件和子 transcript 文件双重记录。
- request-only runtime reminder 不写入 transcript。

### session_meta

```json
{
  "type": "session_meta",
  "schema": "golang-cc.transcript.v2",
  "app": "go-claude",
  "schema_version": 2,
  "id": "entry_...",
  "session_id": "uuid",
  "cwd": "/path/to/golang-cc",
  "project_slug": "Users-example-GolandProjects-golang-claude-code",
  "created_at": "2026-07-02T12:00:00Z",
  "source": "repl_main_thread"
}
```

### message

```json
{
  "type": "message",
  "schema": "golang-cc.transcript.v2",
  "id": "msg_...",
  "session_id": "uuid",
  "turn_id": 1,
  "parent_id": "msg_previous_or_null",
  "role": "user",
  "content": [
    {"type": "text", "text": "分析这个仓库"}
  ],
  "timestamp": "2026-07-02T12:00:01Z"
}
```

### assistant tool_use message

```json
{
  "type": "message",
  "schema": "golang-cc.transcript.v2",
  "id": "msg_assistant_tool",
  "session_id": "uuid",
  "turn_id": 1,
  "parent_id": "msg_user",
  "role": "assistant",
  "content": [
    {
      "type": "tool_use",
      "id": "toolu_1",
      "name": "Read",
      "input": {"file_path": "README.md"}
    }
  ],
  "timestamp": "2026-07-02T12:00:02Z"
}
```

### tool_call

`tool_call` 是 go-claude runtime 执行工具前后的可观测事件，不替代 assistant message 中的 `tool_use`。

```json
{
  "type": "tool_call",
  "schema": "golang-cc.transcript.v2",
  "id": "call_...",
  "session_id": "uuid",
  "turn_id": 1,
  "parent_id": "msg_assistant_tool",
  "assistant_message_id": "msg_assistant_tool",
  "tool_use_id": "toolu_1",
  "tool_name": "Read",
  "input": {"file_path": "README.md"},
  "timestamp": "2026-07-02T12:00:02Z"
}
```

### tool_result

```json
{
  "type": "tool_result",
  "schema": "golang-cc.transcript.v2",
  "id": "result_...",
  "session_id": "uuid",
  "turn_id": 1,
  "parent_id": "call_...",
  "assistant_message_id": "msg_assistant_tool",
  "tool_use_id": "toolu_1",
  "tool_name": "Read",
  "content": "# README...",
  "is_error": false,
  "persisted_output": {
    "path": "tool-results/toolu_1.txt",
    "bytes": 120000,
    "sha256": "..."
  },
  "timestamp": "2026-07-02T12:00:03Z"
}
```

并行工具调用必须按同一个 `assistant_message_id` 分组恢复。例如一个 assistant message 同时请求 `Read` 和 `Grep`，v2 可以有两条 `tool_call` 和两条 `tool_result` entry，但 resume 时必须生成：

```json
{
  "role": "assistant",
  "content": [
    {"type": "tool_use", "id": "toolu_read", "name": "Read", "input": {}},
    {"type": "tool_use", "id": "toolu_grep", "name": "Grep", "input": {}}
  ]
}
```

紧随其后：

```json
{
  "role": "user",
  "content": [
    {"type": "tool_result", "tool_use_id": "toolu_read", "content": "..."},
    {"type": "tool_result", "tool_use_id": "toolu_grep", "content": "..."}
  ]
}
```

不能把每个 tool_result 分散到独立 user message 后再插入其他文本，否则会破坏 Anthropic tool protocol 的相邻性约束。

### context_message

用于 SkillTool 等工具把正文以模型上下文方式注入，而不是塞进普通 tool result。

```json
{
  "type": "context_message",
  "schema": "golang-cc.transcript.v2",
  "id": "ctx_...",
  "session_id": "uuid",
  "turn_id": 1,
  "parent_id": "result_...",
  "role": "user",
  "scope": "skill",
  "content": [
    {"type": "text", "text": "<system-reminder>Skill demo instructions are now active...</system-reminder>"}
  ],
  "timestamp": "2026-07-02T12:00:04Z"
}
```

### compact_summary

```json
{
  "type": "compact_summary",
  "schema": "golang-cc.transcript.v2",
  "id": "compact_...",
  "session_id": "uuid",
  "turn_id": 8,
  "parent_id": "msg_before_compact",
  "content": "Conversation summary so far...",
  "metadata": {
    "trigger_tokens": 180000,
    "token_after": 60000
  },
  "timestamp": "2026-07-02T12:10:00Z"
}
```

### subagent_start

```json
{
  "type": "subagent_start",
  "schema": "golang-cc.transcript.v2",
  "id": "subagent_start_...",
  "session_id": "parent-session-id",
  "turn_id": 2,
  "parent_id": "call_task",
  "task_id": 101,
  "tool_use_id": "toolu_task",
  "agent_id": "101",
  "agent_name": "reviewer",
  "subagent_session_id": "subagent-session-id",
  "agent_transcript_path": "parent-session-id/subagents/agent-101.jsonl",
  "timestamp": "2026-07-02T12:00:05Z"
}
```

### subagent_finish

```json
{
  "type": "subagent_finish",
  "schema": "golang-cc.transcript.v2",
  "id": "subagent_finish_...",
  "session_id": "parent-session-id",
  "turn_id": 2,
  "parent_id": "subagent_start_...",
  "task_id": 101,
  "agent_id": "101",
  "status": "completed",
  "result_preview": "Reviewed files...",
  "is_error": false,
  "timestamp": "2026-07-02T12:02:00Z"
}
```

### usage

```json
{
  "type": "usage",
  "schema": "golang-cc.transcript.v2",
  "id": "usage_...",
  "session_id": "uuid",
  "turn_id": 1,
  "model": "claude-sonnet-4-5",
  "input_tokens": 1000,
  "output_tokens": 200,
  "timestamp": "2026-07-02T12:00:06Z"
}
```

## Resume 语义

### v2 -> MessagesRequest

resume parser 从 v2 transcript 生成 API-ready `messages`：

1. 读取 `session_meta`，校验 `schema=golang-cc.transcript.v2`。
2. 按 `parent_id` 或 append 顺序重建主 message chain。
3. `message` entry 转为 Anthropic `MessageParam`。
4. `tool_call/tool_result` 按 `assistant_message_id` 和 `tool_use_id` 校验完整性。
5. 同一个 assistant message 的所有 tool results 合并成紧随其后的一个 user role `tool_result` message。
6. `context_message` entry 转为 user role text content block，但不能插入 assistant tool_use 与对应 tool_result 之间。
7. `compact_summary` 重置上下文为 summary message。
8. `subagent_start/subagent_finish` 默认不直接进模型上下文，只用于 UI、debug、resume 关联；Task 的结果仍通过父工具 `tool_result` 进入模型。
9. 修复 dangling `tool_use`：如果 assistant tool_use 没有匹配 tool_result，注入 synthetic error tool_result，避免 API 400。
10. 丢弃 orphaned tool_result，并写入 resume report。

### request-only 不落盘

以下内容不写入 transcript：

- final-turn reminder
- tool planning reminder
- transient runtime todo/plan/agent status reminder
- prompt cache breakpoint
- TUI progress tick

这些只属于 `requestMessages`，不是长期历史事实。

## Schema Detection

新增 `internal/session/schema` 或同等模块：

```text
DetectTranscriptSchema(path) -> SchemaInfo
```

返回：

```go
type SchemaKind string

const (
    SchemaGoClaudeV2 SchemaKind = "go_claude_v2"
    SchemaGoClaudeV1 SchemaKind = "go_claude_v1"
    SchemaClaudeCode SchemaKind = "claude_code_original"
    SchemaUnknown    SchemaKind = "unknown"
)

type SchemaInfo struct {
    Kind      SchemaKind
    App       string
    Version   int
    SessionID string
    CWD       string
    SafeToAppend bool
    SafeToMigrate bool
}
```

检测规则：

- 第一行 `type=session_meta` 且 `schema=golang-cc.transcript.v2`：v2。
- 存在 go-claude v1 `type=message/tool_call/tool_result` 且无原版 `uuid/message`：v1。该规则只适用于 legacy go-claude 已知搜索目录；如果文件来自任意外部路径，必须至少满足多条 entry 的 go-claude 字段组合和 tool_call/tool_result 配对规则，否则标为 unknown。
- 存在原版 `uuid/parentUuid/sessionId/message`：Claude Code original。
- 其他：unknown。

## List / Resume / Import 行为

### go-claude list

默认只列：

- go-claude v2
- 可迁移 go-claude v1，标记为 `legacy-migratable`

不列：

- 原版 Claude Code transcript
- unknown transcript

可选 diagnostic flag：

```bash
go-claude session list --all-transcripts
```

显示所有 schema，但原版和 unknown 标记为 read-only。

### go-claude resume

默认行为：

- v2：直接 resume。
- v1：先迁移到 v2，再 resume。
- Claude Code original：拒绝直接 resume，提示使用 import。
- unknown：拒绝 resume。

示例错误：

```text
This transcript appears to be an original Claude Code session. go-claude will not append to it.
Run `go-claude transcript import-claude-code <path>` to import a read-only copy.
```

### import Claude Code

显式命令：

```bash
go-claude transcript import-claude-code <path> --cwd <project>
```

行为：

- 读取原版 JSONL。
- 解析 `uuid/parentUuid/message` chain。
- 转换主 user/assistant/tool_use/tool_result 到 go-claude v2。
- 原版 sidechain、content replacement、remote agent、subagent metadata 暂按 best-effort metadata 保存，不自动承诺完全可继续执行。
- 写入 `~/.go-claude/projects/<slug>/<new-session-id>.jsonl`。
- 原文件只读，不修改。

### export Claude Code

后续可选，不在 P0：

```bash
go-claude transcript export-claude-code <session-id>
```

只有当 v2 schema 足够表达原版需要的 `uuid/parentUuid/message` 时再做。

## v1 -> v2 迁移

### 迁移输入

go-claude v1 旧格式：

```json
{"type":"message","role":"user","content":"hello"}
{"type":"tool_call","tool_id":"toolu_1","tool_name":"Read","content":"{\"file_path\":\"README.md\"}"}
{"type":"tool_result","tool_id":"toolu_1","tool_name":"Read","content":"ok"}
```

### 迁移输出

迁移器生成：

- `session_meta`
- `message` entries
- assistant `message` with `tool_use` content block
- `tool_call`
- `tool_result`
- `usage`
- `compact_summary`
- `recap_summary` metadata

### 迁移策略

- 不覆盖旧文件。
- 新文件写入 `~/.go-claude/projects/<slug>/<new-session-id>.jsonl`。
- 在新文件 `session_meta` 中记录：

```json
{
  "migrated_from": {
    "schema": "go-claude.transcript.v1",
    "path": "$HOME/.claude/projects/.../old.jsonl",
    "session_id": "old-session-id"
  }
}
```

- 迁移后默认 resume 新 session。

## sub-agent 关联模型

### 目录结构

```text
~/.go-claude/projects/<slug>/<parent-session-id>.jsonl
~/.go-claude/projects/<slug>/<parent-session-id>/subagents/agent-<agent-id>.jsonl
~/.go-claude/projects/<slug>/<parent-session-id>/subagents/agent-<agent-id>.meta.json
```

`agent_id` 存储纯 id，例如 `101`；文件名再统一加 `agent-` 前缀，例如 `agent-101.jsonl`。不要存储 `agent_id=agent_101` 后再生成 `agent-agent_101.jsonl`。

### 父 transcript 记录

父 transcript 必须记录：

- `tool_use_id`
- `task_id`
- `agent_id`
- `agent_name`
- `subagent_session_id`
- `agent_transcript_path`
- status
- result preview

### 子 transcript 记录

子 transcript 第一条 `session_meta` 增加：

```json
{
  "type": "session_meta",
  "schema": "golang-cc.transcript.v2",
  "app": "go-claude",
  "session_kind": "subagent",
  "parent_session_id": "parent-session-id",
  "parent_tool_use_id": "toolu_task",
  "task_id": 101,
  "agent_id": "101",
  "agent_name": "reviewer"
}
```

### Resume 行为

主会话 resume：

- 必须恢复 Task 工具结果。
- 必须保留 sub-agent transcript 路径索引。
- 不自动把完整 sub-agent transcript 注入主模型上下文，避免污染。
- 用户或模型显式要求检查 sub-agent 过程时，可通过专用工具或 read-only path 查阅。

子会话 resume：

- 后续可以提供：

```bash
go-claude agent resume <agent-id-or-task-id>
```

P0 只要求能定位和展示，不要求继续执行。

## 与原版 Claude Code 的兼容边界

### 默认隔离

go-claude 默认不写原版目录。这样原版 Claude Code 的 resume list 不会看到 go-claude 新 transcript。

### Read-only compat

go-claude 可以读取原版 transcript 做诊断、导入、对比，但不得默认 append。

### 显式 unsafe 模式

只为开发排查保留：

```bash
go-claude --transcript-root ~/.claude/projects --unsafe-transcript-schema-mixing
```

该模式必须打印明显 warning，并不作为用户默认路径。

## 实施计划

### Phase 1：路径隔离与 schema detection

改动：

- 新增 go-claude transcript root 配置。
- 新增 `SchemaInfo` / `DetectTranscriptSchema`。
- 新会话写入 `~/.go-claude/projects`。
- session list 默认过滤原版/unknown。
- 允许 Phase 1 期间新目录仍写当前 v1 entry 结构，但必须可被 schema detection 标记为 `go_claude_v1`；Phase 1 不能声称完成 v2 schema，只解决污染和误读边界。

验收：

- 新会话不在 `~/.claude/projects` 下创建 transcript。
- `~/.claude/projects` 中原版 transcript 不出现在默认 go-claude resume list。
- schema detection 单测覆盖 v1/v2/original/unknown。
- 新目录 v1 过渡 transcript 可被列为 `go_claude_v1`，不会被误判成 v2。

### Phase 2：Transcript v2 recorder

改动：

- 新增 v2 Entry 类型。
- Recorder 第一行写 `session_meta`。
- 主消息、assistant tool_use、tool_call、tool_result、usage、compact 按 v2 写入。
- 保持当前 in-memory `messages` 逻辑不变，先只替换落盘格式。

验收：

- 新 transcript 第一行是 `session_meta`。
- tool_use/tool_result 可按 `tool_use_id` 重建。
- 并行 tool_use 的多个 tool_result 按 `assistant_message_id` 合并恢复为一条相邻 user tool_result message。
- request-only reminder 不落盘。
- `go test ./internal/session ./internal/query -count=1`。

### Phase 3：v2 resume parser

改动：

- v2 -> `[]anthropic.MessageParam`。
- dangling tool_use 修复。
- orphan tool_result 丢弃。
- compact summary 重建。
- resume report 增加 schema 信息。

验收：

- v2 resume 能恢复多轮工具调用。
- 并行 tool_use/tool_result 顺序正确。
- malformed transcript 不导致 API 400。

### Phase 4：v1 migration

改动：

- 识别当前 go-claude v1。
- 自动迁移到 v2 新目录。
- 旧文件只读，不原地 append。

验收：

- 旧 v1 transcript 可迁移并 resume。
- 迁移后 `migrated_from` metadata 正确。
- 不修改旧文件 mtime/content。

### Phase 5：sub-agent transcript linkage

改动：

- 子 agent transcript 写入 `<parent-session-id>/subagents/agent-*.jsonl`。
- 父 transcript 写 `subagent_start/subagent_finish`。
- Task result 保留简洁 tool_result，完整子过程只通过路径索引查阅。

验收：

- 主 session resume 后能看到 Task result。
- 主 transcript 能定位 agent transcript。
- 子 transcript meta 能反查 parent session/tool_use/task。
- sub-agent prompt dump 与 transcript 路径一致。

### Phase 6：Claude Code import

改动：

- 显式 `import-claude-code` 命令。
- 原版 main chain best-effort 转 v2。
- 原版特殊 entry 以 metadata 保留。

验收：

- 导入不会修改原版文件。
- 导入后生成 go-claude v2 新 session。
- unsupported 原版结构有清晰 report。

## 测试计划

### 单元测试

- `ProjectSlug` / original slug 对照测试。
- schema detection：
  - go-claude v2
  - go-claude v1
  - Claude Code original
  - unknown
- v2 recorder 第一行 meta。
- request-only reminder 不落盘。
- v2 resume tool_use/tool_result pairing。
- v1 migration。
- subagent path linkage。

### 集成测试

- 新会话写入 `~/.go-claude`。
- legacy v1 resume 自动迁移。
- 原版 transcript 默认不进入 resume list。
- import 原版 transcript 生成新 v2 session。
- 主会话 + Task 子 agent 完整跑一轮，检查父/子路径和 resume report。

### 回归测试

```bash
go test ./internal/session ./internal/query ./internal/agentruntime ./internal/cli/... -count=1
go test ./... -count=1
git diff --check
```

### 手工验收

1. 启动 go-claude 新 session。
2. 确认 `~/.go-claude/projects/<slug>/<session-id>.jsonl` 创建。
3. 确认 `~/.claude/projects/<slug>` 没有新增 go-claude 文件。
4. 原版 Claude Code `/resume` 不显示 go-claude 新 session。
5. go-claude resume list 不显示原版 session，除非 `--all-transcripts`。
6. 执行 Task 子 agent，确认父 transcript 和子 transcript 双向可定位。

## 回滚方案

- 保留旧 `session.Store` 读取能力。
- 新写入 root 可通过 env 临时切回：

```bash
GOLANG_CLAUDE_CODE_TRANSCRIPT_PROJECTS_DIR=$HOME/.claude/projects
```

- 即使回滚代码，新 v2 transcript 不应污染原版目录，因为默认 root 已隔离。
- v1 旧文件未被修改，可继续用旧版本读取。

## 风险与权衡

### 风险：用户已有历史分散在两个目录

解决：

- resume list 增加 legacy section。
- 提供迁移命令。
- 文档明确新旧目录。

### 风险：不能直接原地 resume 原版 Claude Code session

这是刻意边界。直接原地 append 原版 transcript 风险更高。正确方式是显式 import。

### 风险：v2 schema 与原版越来越不同

go-claude 的目标是行为对齐，不是盲目复刻内部文件格式。v2 schema 应优先保证 go-claude 自身稳定、可观测、可迁移；原版兼容通过 import/export 层实现。

## 最终判断

彻底修复的关键不是继续让 go-claude “看起来像 Claude Code 写在同一个目录”，而是：

- 默认隔离，避免污染。
- schema 显式，避免误读。
- resume 分层，避免混写。
- sub-agent 可追踪，避免长任务诊断断链。
- 原版兼容走 import/export，不走默认混用。

这个方案能同时降低用户本地原版 Claude Code 被污染的风险，并把 go-claude 自己的 transcript 升级成更适合长期 agent 能力演进的基础设施。
