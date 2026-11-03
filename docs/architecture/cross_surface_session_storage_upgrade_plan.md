# 跨端统一会话存储与桌面端 SQLite 降级方案

## 1. 文档状态

- 状态：短期实现已落地，跨端只读与飞书完整链路仍按阶段推进
- 适用范围：TUI、CLI、桌面端 WebUI、普通 WebUI、飞书渠道
- 当前阶段：未正式发布，不承担历史会话迁移
- 核心目标：聊天事实统一进入 JSONL，SQLite 降级为可重建索引与低频控制数据
- 关键原则：先统一底层协议，再逐步开放跨端可见和跨端写入
- 发布默认：桌面端默认使用 JSONL；SQLite 仅作为显式配置的回退和验证模式
- 一致性策略：不实现 SQLite 与 JSONL 聊天正文的双写

### 1.1 当前实现快照（2026-09-20）

已落地并推送：

- `GOLANG_CC_DESKTOP_SESSION_BACKEND` 启动级开关，默认 `jsonl`，显式 `sqlite` 可快速回滚。
- `SessionEventStore` 接口和 JSONL transcript event store。
- 桌面端消息、流式文本、thinking、tool、file change、permission/question、完成/失败/取消/超时、pending input、handoff 事件路由。
- JSONL transcript 使用稳定的租户/用户/session 映射和文件锁，多个 session 不共享写锁。
- JSONL 事件投影保留 `source/surface/channel`，会话快照和 WebUI conversation DTO 暴露来源。
- SQLite 模式保持原有行为；没有启用 SQLite/JSONL 聊天正文双写。

当前明确未完成：

- TUI/CLI transcript 自动合并到桌面 tenant 列表并展示完整消息。
- 独立部署 WebUI2 tenant 模式迁移。
- 飞书生产 worker 全链路写入同一 JSONL transcript。
- 跨端继续对话、跨端 rename/compact、跨端实时 SSE。

因此，本次提交完成的是“桌面端 JSONL 主链路 + SQLite 快速回滚 + 事件来源可追踪”，不是一次性完成所有跨端互通能力。

## 2. 背景与问题

当前系统存在两套会话模型：

```text
TUI / CLI
  一个 session 对应一个 JSONL transcript
  internal/session.Store

WebUI / 桌面端 tenant session
  tenant_sessions
  tenant_session_messages
  tenant_agent_tasks
  tenant_agent_task_events
  SQLite 或 MySQL
```

当前已经存在一条有限的本地会话桥接：

```text
TUI JSONL -> LocalAdapter -> WebUI local session
```

但这条桥接目前只支持：

- 会话列表
- 标题
- 更新时间
- transcript 格式
- 事件数量
- 只读检查信息

它还不能把 TUI transcript 当作普通 WebUI 对话展示，也不能从 `local` 会话继续发送消息。WebUI 的实时 conversation 接口当前只读取 managed tenant session 的数据库事件。

飞书会话当前会创建 `tenant_sessions` 并写入 `tenant_session_messages`，但飞书渠道的 channel run/message 数据和 WebUI V2 的 agent task event 数据还没有统一成同一条桌面会话时间线。因此飞书会话目前可能出现在租户会话列表中，但完整聊天展示和来源标识不够统一。

## 3. 目标

### 3.1 主目标

1. TUI、CLI、桌面端 WebUI、普通 WebUI、飞书使用统一会话事件模型。
2. 一个逻辑会话只有一个稳定 `session_id`。
3. JSONL 成为聊天正文、运行事件、恢复信息和压缩摘要的权威事实源。
4. SQLite 只保存可重建的列表索引、标题和低频页面元数据。
5. 多租户、用户、权限、审计、配额、渠道控制面继续保留。
6. 飞书产生的会话必须出现在桌面端会话列表。
7. 飞书会话可以在桌面端打开并查看完整聊天内容。
8. 桌面端显示会话来源，例如：TUI、桌面端、WebUI、飞书。
9. 第一阶段支持跨端发现、打开和展示。
10. 第二阶段再支持跨端继续对话、追加事件、实时同步和跨端修改。

### 3.2 不做范围

本方案不处理历史会话迁移，因为当前产品尚未发布：

- 不迁移旧 SQLite 聊天正文。
- 不兼容已发布版本的旧 session ID。
- 不保留旧 SQLite 会话和新 JSONL 会话的长期双读逻辑。
- 不做旧版本回滚数据转换。
- 不保证新旧开发环境中的临时会话继续保留。

正式实现时可以直接切换为新的统一 JSONL schema；开发环境中的旧数据允许清理。

### 3.3 短期目标：最小化解决 SQLite 会话写入冲突

短期目标不做“所有会话全面移除 SQLite”，而是只处理最容易产生高频写入冲突的桌面端本地聊天链路：

```text
桌面端本地聊天
  -> 复用 TUI/CLI 的 internal/session.Store
  -> 一个 session 一个 JSONL
  -> SQLite 只保存会话列表、标题、状态和路径索引
```

短期必须满足：

1. TUI、CLI、桌面端本地会话共享现有 transcript 读取和写入能力。
2. 桌面端聊天正文、thinking、tool streaming 不再持续写入 SQLite。
3. SQLite 仅作为可重建的 `SessionIndex`，不是聊天事实源。
4. WebUI2 的 `local` 会话通过适配器读取同一套 JSONL，先实现只读展示。
5. 桌面端 UI、Session Control API、Composer、拖拽 `sourceRefs`、总结、压缩和 SSE 外部行为保持不变。
6. WebUI2 的 `tenant` 会话暂时继续使用现有 SQLite/MySQL 模型。
7. 飞书的租户控制面、inbox/outbox、幂等、重试、租约和审计暂时继续使用数据库。
8. 飞书会话先保证进入桌面端会话列表，并通过统一读取适配器展示完整聊天。
9. 第一阶段不开放跨端继续对话、跨端改名、跨端压缩和多客户端同时写入。

短期不要求：

- 立即把 WebUI2 tenant 会话全部迁移到 JSONL。
- 立即把飞书全部控制面迁移到 JSONL。
- 立即让所有客户端获得跨端写入能力。
- 立即引入全新的 schema 并替换所有现有 transcript。

短期优先复用现有 TUI transcript schema 和 `internal/session.Store`，只增加必要的来源、租户和用户元数据。是否升级 schema 版本，应在现有实现确认后单独评审，不能为了统一而扩大首期范围。

### 3.4 长期目标

长期目标仍然是：

```text
TUI / CLI / Desktop / WebUI / Feishu
  共享 canonical session event model
  共享稳定 session_id
  共享统一来源和租户元数据
  共享统一事件解析器
```

但是长期目标不等于短期必须完成的范围。长期迁移必须建立在短期 JSONL 链路稳定、权限边界验证通过、飞书展示链路闭环之后。

### 3.5 桌面端 backend 开关

由于本次改造影响桌面端会话主链路，增加桌面端启动级 backend 开关：

```text
GOLANG_CC_DESKTOP_SESSION_BACKEND=sqlite
GOLANG_CC_DESKTOP_SESSION_BACKEND=jsonl
```

也可以由桌面端配置文件提供：

```json
{
  "desktop": {
    "session_backend": "jsonl"
  }
}
```

开关只作用于 Wails 桌面端内嵌的本地 Go Server，不影响独立部署的 WebUI2 tenant 模式。
发布版本的默认值为 `jsonl`；只有显式指定 `sqlite` 时才进入 SQLite 回退模式。

#### sqlite 模式

保持当前实现：

```text
Desktop WebUI2
  -> 当前 ManagedAdapter
  -> SQLite tenant_sessions/messages/tasks/events
```

用途：

- 改造期间保留现状回退能力。
- 对比 SQLite 和 JSONL 模式的 UI、协议和行为。
- JSONL 模式出现阻断问题时快速恢复桌面端可用性。

#### jsonl 模式

目标实现：

```text
Desktop WebUI2
  -> 相同 Session Control API
  -> DesktopJsonlManagedAdapter
  -> TUI/CLI internal/session.Store
  -> 一个 session 一个 JSONL

SQLite
  -> 只保存 SessionIndex、标题、状态和路径
```

`local` 适配器仍然保持原来的只读语义，用于读取外部 TUI/CLI transcript；桌面端可写 tenant 会话不是通过切换成 `local` 实现，而是由可写的 `DesktopJsonlManagedAdapter` 使用 JSONL。

#### 开关约束

- 开关只能在桌面端启动时读取。
- 运行中不能热切换 backend。
- 不能按租户、按会话动态切换 backend。
- 同一桌面端进程生命周期内只能有一个权威聊天存储。
- 不配置时必须使用 `jsonl`，不能因为缺少配置而隐式回到 SQLite。
- SQLite 模式下 SQLite 是聊天权威来源。
- JSONL 模式下 JSONL 是聊天权威来源，SQLite 只是索引和控制面。
- 模式切换需要停止桌面端并重新启动。
- 当前未正式发布，不承担两种模式之间的历史会话迁移。

#### 明确禁止双写

不实现长期的：

```text
SQLite 聊天正文 + JSONL 聊天正文同时写入
```

禁止双写的原因：

- 两边写入顺序可能不同。
- 一边成功、一边失败会造成事实分裂。
- 事件 ID、sequence、SSE cursor 不一致。
- 崩溃恢复时无法判断哪一边是权威。

可以做短期只读影子对比，但不能把双写作为运行模式。

## 4. 架构决策

### 4.1 存储职责

```text
JSONL Transcript（短期：桌面本地聊天；长期：统一聊天事实）
  聊天正文
  thinking/tool/permission/question
  run 状态
  compact/recap
  幂等恢复所需事件
  跨端来源信息

SQLite Session Index（短期保留）
  会话列表
  标题副本
  更新时间
  cwd/project
  状态投影
  JSONL 路径
  搜索和排序索引

Tenant Control Plane
  租户与用户
  权限和 ACL
  飞书账号与外部身份
  inbox/outbox
  渠道 run 租约
  审计
  配额
  telemetry
  图片任务
```

SQLite 不再是聊天可用性的硬依赖。索引写失败不能让 JSONL 聊天发送失败。

### 4.2 单一权威存储不变量

```text
sqlite mode:
  SQLite 是当前聊天事实源

jsonl mode:
  JSONL 是当前聊天事实源
  SQLite 只保存可重建索引和低频控制数据
```

任何实现都不能让同一个桌面端进程同时把同一条聊天事实写入 SQLite 和 JSONL。

### 4.3 依赖方向

```text
TUI / WebUI / Desktop / Feishu adapter
                  |
          SessionService
                  |
        SessionRepository
          /                  \
 JsonlTranscriptStore     SessionIndex
                               |
                       SQLiteIndex 或 FileIndex
```

调用方只依赖接口，不依赖 JSONL、SQLite 或 MySQL 的具体实现。

建议拆成三个边界：

```go
type TranscriptStore interface {
    Create(context.Context, CreateSessionInput) (Session, error)
    Read(context.Context, SessionRef, ReadOptions) (TranscriptPage, error)
    Append(context.Context, SessionRef, []TranscriptEvent) error
    Compact(context.Context, SessionRef, CompactInput) (CompactResult, error)
    Rename(context.Context, SessionRef, string) error
    Archive(context.Context, SessionRef) error
}

type SessionIndex interface {
    Upsert(context.Context, SessionSummary) error
    List(context.Context, SessionListQuery) ([]SessionSummary, error)
    Remove(context.Context, SessionRef) error
    Rebuild(context.Context, SessionScope) error
}

type SessionRepository interface {
    Transcript() TranscriptStore
    Index() SessionIndex
}
```

实际业务层依赖 `SessionService`，不直接依赖上述实现：

```go
type SessionService interface {
    Create(context.Context, CreateRequest) (SessionDetail, error)
    List(context.Context, ListRequest) ([]SessionSummary, error)
    Get(context.Context, GetRequest) (SessionDetail, error)
    Send(context.Context, SendRequest) (OperationResult, error)
    ReadEvents(context.Context, ReadEventsRequest) (EventPage, error)
    Rename(context.Context, RenameRequest) error
    Compact(context.Context, CompactRequest) (CompactResult, error)
}
```

### 4.4 实现组合

TUI/CLI：

```text
JsonlTranscriptStore
FileIndex 或直接扫描 JSONL
```

桌面端：

```text
JsonlTranscriptStore
SQLiteSessionIndex
TenantControlPlane
```

普通 WebUI：

```text
服务端 JsonlTranscriptStore
服务端 SessionIndex
TenantControlPlane
```

飞书：

```text
ChannelControlPlane
JsonlTranscriptStore
SQLite/MySQL SessionIndex
```

飞书的 inbox/outbox、投递重试和幂等仍然保留数据库；只有聊天事实和展示时间线迁移到 JSONL。

## 5. 会话身份与作用域

### 5.1 稳定身份

```text
session_id：逻辑会话唯一 ID
run_id：一次模型执行唯一 ID
event_id：会话内事件唯一 ID
sequence：会话内单调递增游标
```

禁止继续使用“一次 task/run 一个 JSONL”作为主会话模型。

```text
一个 session
  多个 run
    多个 event
```

### 5.2 会话引用

保留现有引用形式：

```text
tenant:<session-key>
local:<session-id>
```

内部通过统一 `SessionRef` 解析，不让前端接触文件路径。

### 5.3 租户和用户边界

JSONL 文件必须按作用域隔离：

```text
transcripts/
  local/
    users/<os-user>/projects/<project-slug>/<session-id>.jsonl
  tenants/
    <tenant-key>/
      users/<user-key>/
        sessions/<session-id>.jsonl
```

路径只是物理隔离，不能替代授权。每次 `List/Get/Read/Append` 必须先经过 `AccessController` 检查：

```text
tenant_id
user_id
actor_user_id
session_id
session visibility / ACL
```

租户管理员可以读取租户范围内允许查看的渠道会话；普通用户默认只能读取本人会话或被授权会话。

## 6. 统一 JSONL Schema

### 6.1 版本策略

新方案使用独立 schema 版本：

```text
schema: golang-cc.transcript.v3
schema_version: 3
```

由于未正式发布，不做 v2 数据迁移。新实现禁止把 v2、v3 或未知格式混写到同一文件。

### 6.2 公共事件包络

所有事件共享以下字段：

```json
{
  "schema": "golang-cc.transcript.v3",
  "id": "evt_01...",
  "type": "message",
  "session_id": "sess_01...",
  "run_id": "run_01...",
  "sequence": 42,
  "timestamp": "2026-09-20T12:00:00Z",
  "producer": {
    "surface": "desktop",
    "channel": "desktop",
    "component": "session-control"
  },
  "payload": {}
}
```

字段约束：

| 字段 | 必填 | 说明 |
|---|---:|---|
| `schema` | 是 | 统一 schema 名称 |
| `id` | 是 | 事件唯一 ID |
| `type` | 是 | 事件类型常量 |
| `session_id` | 是 | 稳定逻辑会话 ID |
| `run_id` | 否 | 关联一次执行 |
| `sequence` | 是 | 会话内单调递增游标 |
| `timestamp` | 是 | UTC 时间 |
| `producer.surface` | 是 | `tui`、`cli`、`desktop`、`webui`、`feishu` |
| `producer.channel` | 是 | `local`、`desktop`、`web`、`feishu`、`api` |
| `producer.component` | 否 | 产生事件的组件 |
| `payload` | 是 | 事件专属内容 |

### 6.3 session_meta

第一行必须是 `session_meta`：

```json
{
  "schema": "golang-cc.transcript.v3",
  "id": "evt_meta_01",
  "type": "session_meta",
  "session_id": "sess_01",
  "sequence": 0,
  "timestamp": "2026-09-20T12:00:00Z",
  "producer": {
    "surface": "desktop",
    "channel": "desktop",
    "component": "session-control"
  },
  "payload": {
    "scope": "tenant",
    "tenant_key": "tenant-a",
    "user_key": "user-a",
    "cwd": "/workspace/project",
    "project_slug": "workspace-project",
    "title": "Release planning",
    "created_by_surface": "desktop",
    "created_by_channel": "desktop",
    "visibility": "owner"
  }
}
```

`tenant_key`、`user_key` 只能使用非敏感标识。禁止写入 access token、app secret、cookie 或模型供应商密钥。

### 6.4 标题策略

标题权威顺序：

1. 用户显式标题。
2. 最近一次 `session_renamed` 事件。
3. `session_meta.payload.title`。
4. 第一条用户消息截断生成。

SQLite 标题只是索引副本：

```text
先追加 JSONL
再异步 Upsert SQLite
```

### 6.5 核心事件类型

```text
session_meta
session_renamed
session_archived
run_started
run_queued
message
thinking
tool_call
tool_result
permission_requested
permission_resolved
question_requested
question_resolved
compact_summary
recap_summary
usage
run_completed
run_failed
run_cancelled
handoff
attachment
error
```

事件类型必须使用常量，不在业务代码中散落字符串字面量。

### 6.6 message 示例

```json
{
  "schema": "golang-cc.transcript.v3",
  "id": "evt_msg_01",
  "type": "message",
  "session_id": "sess_01",
  "run_id": "run_01",
  "sequence": 3,
  "timestamp": "2026-09-20T12:00:03Z",
  "producer": {
    "surface": "feishu",
    "channel": "feishu",
    "component": "channel-worker"
  },
  "payload": {
    "role": "user",
    "content": [
      {
        "type": "text",
        "text": "请检查当前项目状态"
      }
    ],
    "external": {
      "provider": "feishu",
      "account_key": "bot-a",
      "chat_type": "p2p"
    }
  }
}
```

### 6.7 compact_summary 示例

```json
{
  "schema": "golang-cc.transcript.v3",
  "id": "evt_compact_01",
  "type": "compact_summary",
  "session_id": "sess_01",
  "run_id": "run_03",
  "sequence": 88,
  "timestamp": "2026-09-20T12:20:00Z",
  "producer": {
    "surface": "desktop",
    "channel": "desktop",
    "component": "context-compactor"
  },
  "payload": {
    "content": "当前任务目标、已完成工作、未完成事项和约束...",
    "covers_until_sequence": 80,
    "token_budget": 12000,
    "content_sha256": "..."
  }
}
```

压缩只追加摘要事件，不直接破坏性重写历史正文。

## 7. 事件解析与展示模型

### 7.1 统一解析流程

```text
Load JSONL
  -> 校验 schema/version
  -> 校验 session_id/sequence
  -> 读取 active branch
  -> 按事件类型归并
  -> 生成 canonical SessionDetail
  -> 转换为 TUI/WebUI/Desktop DTO
```

### 7.2 Canonical SessionDetail

```go
type SessionDetail struct {
    Ref          SessionRef
    ID           string
    Title        string
    Scope        SessionScope
    Origin       SessionOrigin
    Status       SessionStatus
    CWD          string
    UpdatedAt    time.Time
    Messages     []SessionMessage
    Events       []SessionEvent
    Runs         []SessionRun
    Context      []ContextChip
    Attachments  []AttachmentRef
    ReadOnly     bool
}
```

现有前端 `SessionSummary`、`SessionDetail`、`SessionMessage`、`ConversationEvent` 继续作为 HTTP DTO；只增加必要的来源字段，不让 JSONL 字段直接泄露到前端。

### 7.3 来源显示

桌面端列表和详情中增加来源徽标：

```text
来源：TUI
来源：桌面端
来源：WebUI
来源：飞书
```

飞书会话可进一步展示：

```text
渠道：飞书
机器人：bot-a
会话类型：私聊 / 群聊
```

敏感的外部 chat ID 默认不直接展示，除非管理员查看诊断信息。

## 8. 飞书会话链路

### 8.1 当前问题

当前飞书运行时：

1. 通过 `SessionResolver` 创建或复用 `tenant_sessions`。
2. 用户消息写入 `tenant_session_messages`。
3. AI 回复写入 `tenant_session_messages`。
4. 渠道生命周期写入 `channel_conversations`、`channel_runs`、`channel_messages`。
5. WebUI V2 的完整对话流主要读取 `tenant_agent_task_events`。

因此列表和消息事实分散，桌面端无法稳定构造统一时间线。

### 8.2 新链路

```text
飞书入站事件
  -> Control Plane inbox 去重
  -> 解析 tenant/user/external identity
  -> 获取稳定 session_id
  -> 追加 source=feishu 的 message/run/tool/result 事件到 JSONL
  -> 更新 SQLite SessionIndex
  -> 保留 channel run/outbox/lease 数据库记录
  -> 桌面端按 tenant 权限列出会话
  -> 桌面端读取 JSONL 并展示完整聊天
```

### 8.3 权限策略

默认策略：

- 飞书会话属于创建它的 tenant。
- 普通用户只能看到自己有权限的飞书会话。
- 租户管理员可以看到租户范围内允许审计的飞书会话。
- 任何 `Get/Read` 都先做控制面授权，再打开 JSONL。

不能因为 transcript 文件在本机，就绕过租户权限。

## 9. SQLite SessionIndex

### 9.1 表设计

```sql
CREATE TABLE session_index (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scope_kind TEXT NOT NULL,
    tenant_key TEXT NOT NULL DEFAULT '',
    user_key TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    origin_surface TEXT NOT NULL,
    origin_channel TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'idle',
    cwd TEXT NOT NULL DEFAULT '',
    project_slug TEXT NOT NULL DEFAULT '',
    transcript_path TEXT NOT NULL,
    last_sequence INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    archived_at DATETIME NULL,
    index_version INTEGER NOT NULL DEFAULT 1,
    UNIQUE(scope_kind, tenant_key, user_key, session_id)
);

CREATE INDEX idx_session_index_list
    ON session_index(scope_kind, tenant_key, user_key, updated_at DESC);

CREATE INDEX idx_session_index_title
    ON session_index(scope_kind, tenant_key, user_key, title);
```

SQLite 表是 projection，不是聊天事实源。删除表后可以通过扫描 JSONL 重建。

### 9.2 更新策略

```text
Create:
  创建 JSONL -> 写 session_meta -> enqueue index upsert

Message/run:
  只写 JSONL -> 合并低频 index refresh

Rename:
  写 session_renamed -> async index upsert

Archive/delete:
  写权威事件或安全删除文件 -> 更新 index

SQLite 写失败:
  记录 observability -> retry/rebuild
  不阻断聊天
```

## 10. 跨端互通分阶段方案

### 10.1 第一阶段：桌面端最小改造和只读互通

必须完成：

```text
桌面端本地聊天复用 TUI transcript
SessionRepository 接口隔离底层实现
SQLite 降级为 SessionIndex
保留现有 Session Control API 和前端 DTO
统一读取事件解析器
```

短期能力范围：

```text
TUI -> 桌面端查看
CLI -> 桌面端查看
桌面端 -> TUI 查看
WebUI2 local -> 桌面端本地会话查看
飞书 -> 桌面端查看
```

只读互通的定义：

- 可以在列表中发现。
- 可以打开详情。
- 可以展示用户消息、AI 回复、工具、总结和来源。
- 可以查看运行状态和时间线。
- 不允许从只读来源直接发送新消息。
- 不允许跨端改名、压缩、删除。

短期不改变：

```text
WebUI2 tenant -> 继续使用现有租户数据库模型
飞书控制面 -> 继续使用 SQLite/MySQL
```

### 10.2 第二阶段：统一租户聊天事实

在短期方案稳定后，再评估是否把以下内容也迁移到统一 JSONL：

```text
WebUI2 tenant 会话
桌面端 tenant 会话
飞书聊天事实
统一 tenant session event cursor
```

这一阶段需要重新评估租户权限、pending input、permission、SSE、飞书控制面和审计边界，不属于当前 SQLite 冲突的最小修复。

### 10.3 第三阶段：跨端继续对话

后续再开放：

```text
从另一个客户端继续对话
跨端追加事件
跨端实时 SSE
跨端重命名
跨端压缩
跨端归档和删除
```

第二阶段必须增加：

- 写入能力授权。
- 单 session writer lease。
- 客户端能力声明。
- 并发 append 冲突处理。
- provider/model/cwd 权限校验。
- pending input 和 permission 的跨端接管规则。

### 10.4 长期互通目标

以下是长期目标，不是短期迁移的前置条件：

```text
TUI -> 桌面端查看
桌面端 -> TUI 查看
飞书 -> 桌面端查看
WebUI -> 桌面端查看
```

## 11. API 设计

### 11.1 列表

保留现有 session-control API：

```http
GET /tenant/session-control/sessions?source=tenant
GET /tenant/session-control/sessions?source=local
```

响应增加来源和作用域：

```json
{
  "ref": "tenant:session-01",
  "source": "tenant",
  "title": "Feishu project review",
  "status": "completed",
  "updated_at": "2026-09-20T12:00:00Z",
  "origin": {
    "surface": "feishu",
    "channel": "feishu"
  },
  "scope": {
    "kind": "tenant"
  },
  "read_only": true
}
```

### 11.2 详情

```http
GET /tenant/session-control/sessions/{source}/{id}
```

返回 canonical detail 的 HTTP 投影：

```json
{
  "ref": "tenant:session-01",
  "title": "Feishu project review",
  "origin": {
    "surface": "feishu",
    "channel": "feishu"
  },
  "messages": [],
  "events": [],
  "runs": [],
  "context": [],
  "read_only": true
}
```

### 11.3 事件分页和 SSE

第一阶段允许读取：

```http
GET /tenant/session-control/sessions/{source}/{id}/conversation?cursor=0
```

对于 `local` 和只读跨端 session，服务端从 JSONL 的 `sequence` 读取；对于统一后的 tenant session，同样从 JSONL 读取。

游标必须从数据库自增 ID 改为稳定的 session event sequence，避免不同客户端共享同一 SQLite event ID。

### 11.4 写入接口

第一阶段对来源会话拒绝写入：

```text
local_read_only
cross_surface_write_disabled
```

第二阶段再根据 session capability 放开。

## 12. 实现模块与代码边界

### 12.1 复用和扩展

- [internal/session/store.go](../../internal/session/store.go)：复用 JSONL 文件扫描、读取、标题、追加、compact、rename、delete 能力。
- [internal/sessioncontrol/local.go](../../internal/sessioncontrol/local.go)：从“只读元信息适配器”扩展为统一 transcript read adapter。
- [internal/sessioncontrol/managed.go](../../internal/sessioncontrol/managed.go)：保留服务编排和权限边界，替换底层聊天事实读取方式。
- [internal/server/session_conversation.go](../../internal/server/session_conversation.go)：统一 tenant/local/cross-surface 的事件分页和 cursor。
- [web/src/v2/api/sessionEventReducer.ts](../../web/src/v2/api/sessionEventReducer.ts)：继续负责前端展示归并，不直接理解存储实现。
- [web/src/v2/api/httpSessionControlClient.ts](../../web/src/v2/api/httpSessionControlClient.ts)：保留协议和 DTO，只增加来源展示字段。
- [internal/channel/runtime/service.go](../../internal/channel/runtime/service.go)：飞书入站、出站和运行事件接入 JSONL transcript。
- [internal/cli/cmd_channels.go](../../internal/cli/cmd_channels.go)：保留飞书控制面初始化，替换聊天事实写入入口。

### 12.2 新增建议模块

```text
internal/sessiontranscript/
  schema.go
  event_types.go
  codec.go
  reader.go
  writer.go
  scope.go
  projection.go

internal/sessionrepository/
  repository.go
  jsonl_repository.go
  index_repository.go
  service.go

internal/sessionindex/
  sqlite.go
  file.go
  rebuild.go
```

如果现有 `internal/session` 已能承载新增职责，不强制拆包；但 schema codec、索引和业务 service 必须保持边界清晰。

## 13. 开发顺序

### 阶段 A：短期契约和桌面 JSONL

- [x] 明确短期范围：只迁移桌面端本地聊天事实，不迁移全部 tenant 存储。
- [x] 复用现有 `internal/session.Store` 和 TUI transcript schema。
- [x] 增加必要的 `surface/channel/scope/tenant/user` 元数据。
- [x] 定义事件仓库边界，SQLite/JSONL 通过 `SessionEventStore` 解耦。
- [ ] SQLite index 的异步更新和 JSONL 扫描重建。
- [x] 完成桌面端三会话并发写入隔离测试。

### 阶段 B：只读跨端解析

- [ ] 完善 `local` 会话详情读取。
- 桌面端读取 TUI/CLI transcript。
- WebUI2 local 使用同一解析器展示完整聊天。
- 保持现有前端 DTO、拖拽和 Composer 行为。
- 明确只读能力和来源徽标。

### 阶段 C：飞书桌面可见

- [ ] 飞书 session resolver 返回稳定 session_id。
- 飞书消息和 AI 回复写入统一 transcript 读取链路。
- 保留 channel control plane 数据库。
- 桌面端显示飞书来源。
- 完成飞书入站、AI 回复、工具、失败、图片和问题事件展示。

### 阶段 D：长期租户统一

- 评估 WebUI2 tenant 会话迁移到 JSONL。
- 评估飞书聊天事实完全迁移到 JSONL。
- 统一 tenant event cursor。
- 补齐更复杂的租户权限和审计投影。

### 阶段 E：第二阶段跨端写入能力

- 设计 writer lease 和 session capability。
- 实现跨端继续对话。
- 实现跨端 SSE、rename、compact、archive。
- 此阶段不与第一阶段绑定上线。

## 14. 风险与控制措施

| 风险 | 等级 | 控制措施 |
|---|---:|---|
| JSONL 损坏导致会话无法打开 | P0 | append-only、单行校验、fsync、启动扫描、坏文件隔离 |
| 同一 session 多进程并发写乱序 | P0 | session 级文件锁、单调 sequence、追加前刷新 active leaf |
| 不同租户文件越权读取 | P0 | 先授权后解析路径，scope 路径隔离，禁止用户输入直接拼路径 |
| SQLite 索引与 JSONL 不一致 | P1 | JSONL 权威、SQLite 可重建、异步 upsert、启动 rebuild |
| 飞书会话漏进桌面列表 | P1 | channel session 创建后必须建立 index projection，增加端到端断言 |
| 飞书消息和 AI 回复时间线不完整 | P0 | 所有渠道事件写 canonical JSONL，禁止只写 channel 表 |
| TUI、WebUI 事件语义不同 | P1 | canonical event reducer 和跨端 golden fixture |
| title 来源分裂 | P1 | JSONL 标题权威，SQLite 只做副本 |
| 跨端写入造成事件竞争 | P0 | 第一阶段只读；第二阶段引入 writer lease |
| 外部 chat ID 或租户信息泄露 | P1 | 只写非敏感标识，前端默认脱敏，日志禁止 secret |
| 过度依赖 JSONL 扫描造成启动慢 | P2 | SQLite 索引加速，索引损坏时再扫描，增量 rebuild |
| 飞书控制面被错误移除 | P0 | inbox/outbox/lease/idempotency 仍保留数据库 |
| pending input / permission 无法恢复 | P0 | 事件写入 JSONL，控制面保存等待和授权状态 |

## 15. 兼容性策略

### 15.1 前端

保持不变：

- `SessionRef`
- `SessionSummary`
- `SessionDetail`
- `SessionMessage`
- `ConversationEvent`
- Composer 拖拽和 `sourceRefs`
- 现有 UI 布局、标题编辑界面和消息组件

只增加：

- `origin`
- `channel`
- `scope`
- `read_only`

### 15.2 API

保持现有路径和错误码语义。底层 cursor 可以从数据库 event ID 变成 JSONL sequence，但响应字段保持 `cursor` 字符串形式，避免前端协议变化。

### 15.3 会话功能

必须继续支持：

- 会话列表
- 标题展示和改名策略
- 拖拽会话到输入框
- sourceRefs
- 会话总结
- context compact
- 工具调用和工具结果
- permission/question
- 运行状态
- 图片附件
- handoff/context attach
- SSE 断线重连
- 多会话并行

第一阶段跨端来源为只读，不允许因为只读而破坏正常桌面 tenant 会话的发送能力。

## 16. 测试方案

### 16.1 单元测试

- v3 schema encode/decode。
- 未知 schema 拒绝追加。
- `session_meta` 必须是第一行。
- sequence 单调递增。
- origin/producer 字段完整。
- 标题权威顺序。
- compact_summary 进入当前上下文。
- branch/rewind/redo 不破坏 active chain。
- JSONL 损坏行处理。
- 文件锁和跨进程追加。

### 16.2 Repository 契约测试

同一组测试分别运行于：

```text
JsonlTranscriptStore
SQLiteIndex + JsonlTranscriptStore
FileIndex + JsonlTranscriptStore
```

验证接口行为一致，而不是验证实现细节。

### 16.3 多租户测试

- 租户 A 不能读取租户 B。
- 用户 A 不能读取用户 B 的 owner-only 会话。
- 管理员读取范围符合策略。
- local session 不被当成 tenant session 授权。
- JSONL 文件路径不能通过 `../` 越权。

### 16.4 并发测试

- 三个不同 session 同时持续写入。
- 同一个 session 两个 writer 竞争。
- SQLite index writer 与 JSONL writer 同时运行。
- SQLite 被锁时聊天仍成功。
- 进程崩溃后重新打开并继续追加。
- SSE 断线后按 sequence 恢复，不重复、不丢事件。

### 16.5 端到端测试

必须覆盖：

1. TUI 创建会话，桌面端列表可见。
2. 桌面端创建会话，TUI 列表可见。
3. TUI 会话在桌面端能展示完整用户消息、AI 消息、工具和 compact。
4. 飞书发送消息，桌面端列表出现“来源：飞书”。
5. 飞书完整对话可以在桌面端打开。
6. 飞书图片、工具、失败和问题事件可展示。
7. WebUI 能查看同一 JSONL 会话。
8. SQLite 删除后，扫描 JSONL 可以恢复列表。
9. SQLite 锁定时，发送消息不显示“会话暂时不可用”。
10. 三个会话并行执行时互不影响。
11. local 来源只读，发送按钮和改名按钮行为符合协议。
12. 前端截图验收：桌面端 UI 视觉结构和现有版本一致，只增加来源标识。

## 17. 验收标准

### 必须通过

- 桌面端本地聊天正文不再依赖 SQLite。
- 桌面端本地聊天使用现有 TUI transcript。
- SQLite 删除或损坏不会删除桌面端本地聊天内容。
- 飞书会话在桌面端可见。
- 飞书会话可以打开查看完整聊天。
- 飞书来源可溯源。
- TUI、CLI、桌面端本地会话使用统一 parser。
- WebUI2 local 可以读取桌面端本地 JSONL。
- 多租户权限边界有效。
- 三个并发会话互不争抢同一聊天锁。
- 现有桌面 UI 不发生结构性变化。
- 拖拽会话、总结、压缩、工具、权限、问题和 SSE 继续可用。

### 不作为第一阶段阻塞项

- WebUI2 tenant 会话全部迁移到 JSONL。
- 飞书控制面全部迁移到 JSONL。
- 从 TUI 打开会话后直接继续发送消息。
- 从飞书会话切换到桌面端继续执行。
- 跨端改名。
- 跨端压缩。
- 多客户端同时写同一个 session。

这些属于第二阶段跨端写入能力。

## 18. 最终结论

短期方案不是删除 SQLite，也不是把所有系统数据粗暴搬到本地文件。

短期职责划分是：

```text
TUI/CLI/桌面端本地 JSONL
  保存本地桌面聊天事实

SQLite
  保存桌面端本地会话列表、标题和索引

MySQL/SQLite 控制面
  继续保存 WebUI2 tenant、租户、权限、飞书渠道、投递、幂等、审计和配额
```

短期先完成：

```text
复用 TUI transcript
桌面端 JSONL 聊天
SQLite 低频索引
飞书会话进入桌面端列表并完整可见
WebUI2 local 只读解析
```

长期再评估：

```text
WebUI2 tenant 聊天事实统一 JSONL
飞书聊天事实统一 JSONL
跨端继续对话
跨端追加事件
跨端重命名和压缩
```

这样可以用较小改动解决最初的 SQLite 高频写入冲突，同时保留 SQLite 在多租户控制面、飞书可靠投递和结构化查询方面的价值。
