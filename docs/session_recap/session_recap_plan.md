# Session Recap 实现方案

本文档记录 Go Claude 的 session recap 功能：在每个 TUI session 底部展示类似 Claude Code `※ recap:` 的短总结，帮助用户快速知道本会话目标、已完成内容和下一步。

## 当前实现状态

- P0 已完成：新增 `internal/recap` 包、`recap_summary` transcript entry、`/recap` 和 `/recap show`、TUI `※ recap:` 渲染、resume 后展示最新 recap。
- P1 已完成：新增 `recap` 配置；支持 `mode=post_turn` assistant turn 成功后异步刷新；支持 `mode=away` 在 TUI 回答完成后空闲超时刷新；记录 `session.recap.started`、`session.recap.finished`、`session.recap.failed` telemetry。
- 安全边界：`internal/query.MessagesFromTranscriptWithReport` 明确忽略 `recap_summary`，recap 不进入下一轮模型上下文；telemetry 不记录完整 prompt 或 recap 正文；rewind 会删除目标点之后的旧 recap，files-only rewind 会写入 `status=invalidated` recap marker 防止继续展示旧总结。
- 全链路 smoke：`scripts/session-recap-smoke.sh` 使用 fake Anthropic server 跑真实 CLI session，覆盖首轮对话、`/recap` 生成、transcript `recap_summary`、resume 初始 UI recap，以及下一轮模型请求不包含 recap。
- P2 已完成为可测试的 idle-away：不依赖 terminal focus/blur，而是在回答完成后输入区空闲超过 `awayDelaySeconds` 时刷新。

## 背景

用户期望对齐 Claude Code 里会话底部 recap 的体验，例如：

```text
※ recap: 恐龙🦖

  本次会话目标：为两个测试患者配置数据让 GetCardQuestionNew 返回 Type4/5/6 卡片，并修复 Type4 条件判断的两个 bug。

  已完成：数据库数据配置、Type4/5/6 条件梳理验证、3个月内90→91待改、急性发作onsetDay取月末、急性用药新老版本兼容。

  下一步：把3处 `daysDiff <= 90` 改为 `daysDiff <= 91`。
```

已确认 `$HOME/GolandProjects/claude_code_src_2026` 中存在相近功能：

| 上游文件 | 结论 |
| --- | --- |
| `src/services/awaySummary.ts` | 使用最近 30 条消息和 session memory，通过小模型生成 1-3 句 recap。 |
| `src/hooks/useAwaySummary.ts` | terminal 失焦 5 分钟、当前不在加载、上一个 user turn 后未生成过 summary 时触发。 |
| `src/components/messages/SystemTextMessage.tsx` | `system / away_summary` 用 `※` 图标灰色渲染。 |
| `src/constants/figures.ts` | `REFERENCE_MARK = '\u203b'`，注释为 away-summary recap marker。 |
| `src/entrypoints/sdk/coreSchemas.ts` | 另有 `post_turn_summary` 结构化事件，但不是截图中的 TUI recap 主路径。 |

Go Claude 不应直接照搬上游的 GrowthBook/Bun feature gate。推荐先实现本项目可控的 P0/P1：每个 session 维护一个最新 recap，并在 TUI 底部展示。

## 目标

P0 目标：

- TUI 会话底部显示 `※ recap:` 区块。已完成。
- `/recap` 手动生成或刷新 recap。已完成。
- recap 写入本地 session transcript，恢复 session 后仍能显示。已完成。
- 默认关闭或低风险开启策略明确，不阻塞主对话。已完成。
- 提供 `/recap` slash command 手动生成、查看、关闭提示。已完成。

P1 目标：

- 支持配置开关和参数。已完成。
- 支持“turn 结束后异步生成”的策略。已完成。
- Trace/telemetry 记录 recap 生成耗时、模型、状态和失败原因。已完成。
- 与 auto compact、memory、skills、hooks、rewind/resume 不冲突。主链路已加测试覆盖。

非目标：

- 不改变主模型对话上下文，recap 默认不参与下一轮模型输入。
- 不修改 Mobile API / OpenAI-compatible API 响应契约。
- 不把完整聊天正文写入日志。
- 不在生成 recap 时执行工具。

## 推荐架构

新增一个独立服务包，复用现有 query/session/config/telemetry 架构：

```text
internal/recap/
  config.go          # 默认值、配置解析后的运行时 Config
  prompt.go          # recap prompt 构建
  generator.go       # 通过 MessageStreamer 非流式或流式聚合生成 recap
  transcript.go      # 从 session.Entry 提取上下文、查找最新 recap
  types.go           # Result、Status、Metadata
  recap_test.go
```

核心数据流：

```text
TUI user input
  -> internal/cli interactive loop
  -> internal/query.Session.Run(...)
  -> assistant turn 完成
  -> recap generator 使用最近 transcript entries 生成短总结
  -> session.Recorder.Append(Type="recap_summary")
  -> TUI append/update role="recap" message
  -> session resume 时从 transcript 读取最新 recap 并作为 InitialMessage 显示
```

## Transcript 设计

在 `internal/session.Entry` 里复用现有通用字段，不需要新增数据库表：

```go
session.Entry{
    Type:    "recap_summary",
    Role:    "system",
    Content: recapText,
    Model:   recapModel,
    Metadata: json.RawMessage(`{
      "version": 1,
      "source": "post_turn",
      "summarizes_entry_id": "...",
      "recent_message_window": 30,
      "duration_ms": 1234,
      "status": "ok"
    }`),
}
```

需要同步调整：

- `internal/session/store.go`
  - `shouldAutoID` 增加 `recap_summary`。
  - `ExportMarkdown` 可选择渲染 recap，建议放在末尾。
  - `Summary`/session list 默认不把 recap 当作最近对话 preview。
- `internal/query/MessagesFromTranscriptWithReport`
  - 忽略 `recap_summary`，避免 recap 进入下一轮模型上下文。
- `rewind`
  - conversation/full rewind 会随 transcript 截断删除目标点之后的旧 recap。
  - files-only rewind 保留 transcript，但会追加 `recap_summary` invalidated marker；UI 的 latest recap 查询遇到 invalidated marker 后不再展示旧 recap。

## 配置设计

在 `internal/config.Settings` 新增：

```go
type RecapSettings struct {
    Enabled                *bool   `json:"enabled,omitempty" yaml:"enabled,omitempty"`
    Mode                   string  `json:"mode,omitempty" yaml:"mode,omitempty"` // "manual" | "post_turn" | "away"
    Model                  string  `json:"model,omitempty" yaml:"model,omitempty"`
    RecentMessageWindow    *int    `json:"recentMessageWindow,omitempty" yaml:"recentMessageWindow,omitempty"`
    MaxTokens              *int    `json:"maxTokens,omitempty" yaml:"maxTokens,omitempty"`
    AwayDelaySeconds       *int    `json:"awayDelaySeconds,omitempty" yaml:"awayDelaySeconds,omitempty"`
    IncludeSessionMemory   *bool   `json:"includeSessionMemory,omitempty" yaml:"includeSessionMemory,omitempty"`
    IncludeCompactSummary  *bool   `json:"includeCompactSummary,omitempty" yaml:"includeCompactSummary,omitempty"`
}
```

并在 `Settings` 加：

```go
Recap *RecapSettings `json:"recap,omitempty" yaml:"recap,omitempty"`
```

默认建议：

```yaml
recap:
  enabled: false
  mode: manual
  recentMessageWindow: 30
  maxTokens: 512
  includeSessionMemory: true
  includeCompactSummary: true
```

说明：

- P0 可先默认 `enabled=false`，通过 `/recap` 手动生成，降低额外模型调用成本。
- P1 再支持 `mode: post_turn`，每轮结束后后台刷新。
- `mode: away` 可后续对齐 Claude Code 的 terminal blur 5 分钟行为；Bubble Tea 下需要可靠检测 focus/blur，不能影响复制、鼠标滚动、粘贴图片和快捷键。

## Prompt 设计

推荐中文优先、简短稳定格式：

```text
你要为一个 Go Claude TUI 会话生成底部 recap。

要求：
- 只输出 recap 正文，不要寒暄。
- 使用用户主要语言；中英混合时优先中文。
- 2-5 行，简短具体。
- 必须包含：本次会话目标、已完成、下一步。
- 不要编造未发生的文件修改、测试结果、提交或外部状态。
- 不要输出敏感信息、API key、JWT、完整私有 URL。
- 不要重复长代码。

上下文如下：
...
```

上下文来源优先级：

1. 最近 N 条 `message`、`tool_call`、`tool_result`。
2. 最近 `compact_summary`。
3. 可选 session memory / code memory 摘要。

工具结果需要截断：

- 单条 tool result 最多 1000-2000 字符。
- 错误和命令摘要优先保留。
- 大文件内容、长日志只保留开头和结尾。

## 生成策略

P0 推荐手动生成：

- `/recap`：生成并显示最新 recap。
- `/recap show`：显示 transcript 中最新 recap。
- `/recap off`：提示用户在配置中关闭或写入 project local 配置，具体是否直接写配置可 P1 再做。

P1 推荐 post-turn 异步生成：

- assistant 最终响应完成且没有 pending tool_use 时触发。
- 不阻塞主响应显示；失败只记录 status message 或 debug log。
- 同一 user turn 只生成一次，避免重复计费。
- 当前 turn 被 Ctrl+C 中断、max turns error、模型错误时不自动生成；可手动 `/recap`。

需要避免的问题：

- 不要在 query loop 内同步等待 recap，避免用户感觉回答结束后卡住。
- 不要把 recap 生成调用再写成普通 assistant message。
- 不要触发 hooks 工具执行，也不要走权限审批。
- 如果 provider 不支持非流式，可用 stream 聚合，但必须关闭工具。

## TUI UX

`internal/tui/app.go` 当前用内部 `message{role, content, meta, tools}` 渲染消息。建议新增 role：

```go
message{role: "recap", content: recapText}
```

渲染：

```text
※ recap:
  本次会话目标：...
  已完成：...
  下一步：...
```

约束：

- recap 固定在消息列表底部，但仍属于 viewport 内容，可滚动、可复制。
- 样式使用 dim/status 风格，不要抢占 assistant/user 主视觉。
- 不开启 mouse tracking，不影响 terminal 原生拖选复制。
- `Ctrl+O` 鼠标模式、`Ctrl+V` 图片粘贴、权限审批选择、slash suggestions 不能回归。

恢复 session：

- `resumeSessionItems` 继续只展示最近 user/assistant preview。
- `--resume` 或 TUI `/resume` 加载历史时，InitialMessage 可追加最新 recap。
- 如果当前 TUI 已有 recap，再生成新 recap 时更新最后一个 recap message，而不是无限追加多个 UI recap。

## API / WebUI 边界

P0/P1 不改对外 API。P2 已新增只读可视化/API 能力：

- `/trace` Event Stream 展示 `recap_summary` entry，并支持 Recap-only 事件筛选。
- Mobile session detail 返回 `latest_recap`，来源为 tenant messages 中最新 `role=recap` 消息；该字段只读，不进入 chat prompt。

## Telemetry / Trace

P1 增加事件：

| 事件 | 时机 | 关键字段 |
| --- | --- | --- |
| `session.recap.started` | 开始生成 | `session_id`、`model`、`mode`、`recent_message_window` |
| `session.recap.finished` | 成功 | `duration_ms`、`content_bytes` |
| `session.recap.failed` | 失败 | `duration_ms`、`error_category` |

日志字段：

- `session_id`
- `cwd`
- `model`
- `mode`
- `duration_ms`
- `status`

不要记录完整 prompt 或完整 recap 内容到日志。

## 实现步骤

### P0：手动 recap 和持久化

1. 新增 `docs/session_recap/session_recap_plan.md` 并索引。
2. 新增 `internal/recap` 包：
   - transcript entry 过滤。
   - prompt 构建。
   - generator 接口，测试可注入 fake streamer。
3. 扩展 `session.Entry` 类型处理：
   - `shouldAutoID("recap_summary")`。
   - transcript load/export/list 忽略或展示规则。
4. 新增 `/recap` slash command：
   - `handleInteractiveSlash` 增加分支。
   - `builtinTUISlashCommands`、`printSlashHelp` 更新。
   - active session 选择逻辑复用 `/rewind` 的 active session 方法。
5. TUI 支持 `role="recap"` 渲染。
6. 手动测试：
   - 启动 TUI，进行两轮对话，执行 `/recap`。
   - 退出后 `--resume` 或 `/resume`，确认 recap 仍存在。

### P1：自动 post-turn 刷新

1. `config.RecapSettings` 与 merge 测试。
2. CLI interactive loop 读取 config，构造 recap runtime config。
3. assistant turn 成功结束后异步生成 recap：
   - 推荐在 CLI/TUI 编排层触发，而不是深塞到 query loop。
   - query loop 只负责主对话；recap 是 UI/session 附加能力。
4. 生成时追加 `recap_summary`，并通过 TUI event 更新底部 recap。
5. 增加 telemetry/trace。
6. 增加 `mode: manual/post_turn/away` 文档。

### P2：Away summary 对齐

1. 已实现 `recap.mode=away` 和 `awayDelaySeconds`。
2. Go Claude 采用 TUI idle-away：assistant turn 成功结束后进入待刷新状态；如果用户没有继续输入、没有打开权限审批或 resume picker，且空闲时间超过 `awayDelaySeconds`，后台生成 recap。
3. 当前不依赖 terminal focus/blur 事件，因为不同 terminal 对 focus reporting 支持不一致，且可能影响复制/鼠标滚动体验。
4. 后续如要进一步对齐上游 `hasSummarySinceLastUserTurn`，可在 transcript metadata 中记录 latest recap 对应的 user turn。

## 测试计划

必须新增/更新：

```bash
go test ./internal/recap -count=1
go test ./internal/session -count=1
go test ./internal/tui -count=1
go test ./internal/cli -run 'TestInteractive.*Recap|TestListTUISlashCommandsIncludesRecap|TestLoadSettingsMergesRecapConfig' -count=1
scripts/session-recap-smoke.sh
go test ./... -count=1
git diff --check
```

建议测试用例：

| 模块 | 测试 |
| --- | --- |
| `internal/recap` | 最近 N 条上下文截断；忽略旧 recap；包含 compact summary；敏感字段基础脱敏；fake model 生成结果。 |
| `internal/session` | `recap_summary` 自动 ID；export markdown 末尾展示；session list preview 不被 recap 覆盖。 |
| `internal/query` | `MessagesFromTranscriptWithReport` 忽略 recap，确保 resume 不把 recap 注入模型。 |
| `internal/tui` | recap role 渲染、换行、底部更新、不影响 mouse copy/scroll 和 slash suggestions。 |
| `internal/cli` | `/recap` 无 active session 报错；有 active session 写 transcript；`/recap show` 展示最新 recap；slash list 包含 recap。 |
| `scripts/session-recap-smoke.sh` | fake provider 全链路验证 `/recap` 写入、resume 展示和 recap 不进入下一轮模型请求。 |
| `internal/config` | recap config merge；local config 覆盖；显式 false/0 可覆盖。 |

## 风险与规避

| 风险 | 规避 |
| --- | --- |
| 额外模型调用增加成本和延迟 | P0 手动，P1 默认仍可关闭；异步生成；小模型和低 max tokens。 |
| recap 污染下一轮上下文 | `MessagesFromTranscriptWithReport` 明确忽略 `recap_summary`。 |
| resume 后 recap 过期 | conversation/full rewind 删除目标点后的 recap；files-only rewind 写 invalidated marker；每次新 turn 后刷新。 |
| TUI 交互回归 | 只新增 message role，不改输入、mouse、permission、clipboard 主路径；跑 `internal/tui` 全量测试。 |
| 生成内容编造测试/提交状态 | prompt 明确禁止；只给最近 transcript，不给未验证外部状态。 |
| 日志泄漏正文 | telemetry/log 只写长度、状态、耗时，不写完整 prompt/recap。 |

## 验收标准

P0 完成标准：

- `/recap` 可在 TUI active session 中生成 recap。
- transcript 有 `type=recap_summary`。
- TUI 底部显示 `※ recap:`。
- resume 后能看到最新 recap。
- recap 不进入下一轮模型 messages。
- 相关测试、`go test ./... -count=1`、`git diff --check` 通过。
- README/docs/todo 同步。

P1 完成标准：

- `config/config.yaml` 支持 `recap.enabled`、`recap.mode`、`recap.model` 等配置。
- `mode=post_turn` 时 assistant 完整结束后自动刷新 recap。
- 生成失败不影响主对话。
- telemetry 能看到 recap started/finished/failed。
- TUI 复制、鼠标滚动、粘贴图片、权限审批、快捷键、slash commands 无回归。

## 新会话开发提示词

可以把下面提示词直接发给新 Codex 会话：

```text
你是 Codex，请继续开发 /path/to/golang-cc 项目。

先阅读 AGENTS.md、README.md、docs/README.md、docs/todo.md、docs/session_recap/session_recap_plan.md，遵循项目规则：
- 先理解现有代码路径，保持最小化改动。
- 不要破坏 TUI 复制、鼠标滚动、粘贴图片、权限审批、快捷键、slash commands。
- 每个功能块完成后跑相关测试，最终跑 go test ./... -count=1 和 git diff --check。
- 文档同步更新，提交前 git status，只提交本次相关文件，不碰 .claude/ 和 gocc1。

目标：实现 Session Recap P0，并尽量完成 P1。

优先级：
1. P0：新增 internal/recap 包、recap_summary transcript entry、/recap slash command、TUI 底部 ※ recap 渲染、resume 后展示最新 recap，确保 recap 不进入下一轮模型上下文。
2. P1：新增 recap config，支持 mode=post_turn 自动异步刷新，增加 telemetry，更新文档和 TODO。

请先给出基于当前代码的短计划，然后开始实现、测试、文档、提交。
```
