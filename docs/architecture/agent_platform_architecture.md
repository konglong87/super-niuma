# go-e2e 产品架构总览

这是一张面向用户、贡献者和运维人员的产品级架构图。它回答一个简单问题：

> 用户从 CLI、桌面、WebUI 或飞书发来一句话后，系统怎样把它变成可验证的结果？

图中的 `Runtime Core` 是概念上的运行时边界，不表示仓库中一定存在一个名为
`runtime-core` 的目录。它代表一组共同协作的能力：会话装配、Agent Loop、工具、
模型、权限、持久化和输出适配。

## 总览图

GitHub 会直接渲染下面的 Mermaid 图；也可以打开独立的 Mermaid 源文件：
[agent-platform-architecture.mmd](../../diagrams/agent-platform-architecture.mmd)。

```mermaid
flowchart TB
  subgraph CHANNELS["用户从哪里进入"]
    direction LR
    CLI["CLI<br/>一次性任务 / 脚本"]
    TUI["TUI<br/>终端交互"]
    DESKTOP["Desktop<br/>桌面工作台"]
    WEB["WebUI<br/>浏览器"]
    FEISHU["Feishu Worker<br/>飞书机器人常驻进程"]
  end

  subgraph ACCESS["接入与治理"]
    AUTH["鉴权与权限<br/>谁可以做什么"]
    TENANT["租户与用户隔离<br/>数据属于哪个组织"]
    ROUTE["路由与入口分配<br/>这次请求使用哪个 Profile"]
    ADAPTER["渠道适配器<br/>把外部消息转换成统一格式"]
  end

  subgraph CORE["Runtime Core：真正执行任务的核心"]
    SESSION["Session / Goal<br/>会话、任务、长期目标"]
    PROFILE["Profile / Team<br/>智能体定义与协作编排"]
    LOOP["Agent Loop<br/>理解 → 计划 → 调用 → 验证 → 继续或完成"]
    CONTEXT["Context Assembly<br/>Prompt、项目规则、租户上下文"]
    subgraph CAPABILITIES["可插拔能力"]
      direction LR
      SKILLS["Skills<br/>专业做法与工作流"]
      MCP["MCP<br/>外部服务工具"]
      TOOLS["Tools<br/>文件、搜索、命令、浏览器"]
      MEMORY["Memory<br/>长期记忆与项目经验"]
      IMAGE["Image Tool<br/>图片生成、编辑与理解"]
    end
    PROVIDER["Provider<br/>模型、协议与多模态服务"]
    GATE["安全与完成检查<br/>权限、预算、证据、结果校验"]
  end

  subgraph STATE["状态、成本与可靠性"]
    PERSIST["Persistence<br/>SQLite / MySQL / Transcript / Inbox / Outbox"]
    OBSERVE["Telemetry / Audit / Quota<br/>日志、追踪、审计、用量与配额"]
  end

  subgraph OUTPUTS["输出适配"]
    OUT_CLI["CLI / TUI 输出"]
    OUT_DESKTOP["Desktop / WebUI 输出"]
    OUT_FEISHU["Feishu Card / Message / Reaction"]
    OUT_API["API / JSON / SSE / WebSocket"]
  end

  CLI --> AUTH
  TUI --> AUTH
  DESKTOP --> AUTH
  WEB --> AUTH
  FEISHU --> ADAPTER
  ADAPTER --> AUTH
  AUTH --> TENANT
  TENANT --> ROUTE
  ROUTE --> SESSION
  ROUTE --> PROFILE
  SESSION --> LOOP
  PROFILE --> CONTEXT
  SESSION --> CONTEXT
  CONTEXT --> LOOP
  LOOP --> PROVIDER
  PROVIDER --> LOOP
  LOOP --> GATE
  GATE -->|继续| LOOP
  GATE -->|完成| OUTPUTS
  LOOP --> CAPABILITIES
  CAPABILITIES --> LOOP
  SESSION --> PERSIST
  LOOP --> PERSIST
  PERSIST --> SESSION
  AUTH -.-> OBSERVE
  TENANT -.-> OBSERVE
  LOOP -.-> OBSERVE
  GATE -.-> OBSERVE
  PERSIST -.-> OBSERVE
  OBSERVE -.-> ROUTE
  OUTPUTS --> OUT_CLI
  OUTPUTS --> OUT_DESKTOP
  OUTPUTS --> OUT_FEISHU
  OUTPUTS --> OUT_API
  OUT_FEISHU --> FEISHU
```

## 小白版：每一层在做什么

可以把系统想成一个“会执行任务的工作台”：

1. **入口**：CLI、TUI、桌面、WebUI 和飞书，都是用户递交任务的不同窗口。
2. **接入与治理**：先确认用户是谁、属于哪个租户、是否有权限，再决定这次请求走哪个入口配置。
3. **Runtime Core**：系统真正理解任务、拆步骤、调用能力、检查结果并决定是否继续的地方。
4. **能力插件**：Skills 提供专业工作方法，MCP 连接外部服务，Tools 操作文件和命令，
   Memory 提供长期经验，Image Tool 处理图片。
5. **Provider**：真正提供语言或视觉模型能力的服务。它可以是不同厂商、不同协议或不同模型。
6. **状态与可靠性**：保存会话、任务、消息、重试和外发记录，并记录日志、审计、用量和配额。
7. **输出适配**：把结果变成终端文本、桌面/WebUI 消息、飞书卡片，或 API 的 JSON/SSE/WebSocket。

最重要的闭环是：

```text
输入
-> 鉴权与租户隔离
-> 选择 Profile
-> Agent Loop 调用模型和能力
-> 权限与完成检查
-> 保存过程和结果
-> 输出到原来的渠道
```

## 设置项怎么理解

设置中心里的对象分别位于不同层次。它们有关联，但不是同一个东西：

| 设置项 | 一句话理解 | 负责什么 | 不负责什么 |
| --- | --- | --- | --- |
| **Profile** | “这个智能体是什么样、怎么工作” | 身份、提示词、模型、工具、Skills、Memory、预算和权限策略 | 不负责决定哪个入口使用它，也不负责启动飞书进程 |
| **Team** | “多个智能体怎样分工” | 成员、角色、协作顺序、协调者和最终输出 | 不复制成员 Profile 的详细能力配置 |
| **Assignment** | “哪个入口使用哪个已发布 Profile” | 把租户、用户或入口映射到某个 Profile 版本 | 不修改 Profile 内容，也不连接飞书账号 |
| **Feishu** | “连接哪一个飞书机器人和会话” | App、Bot Account、群聊/私聊、消息适配和外部回传 | 不定义智能体的工作方式，也不等于一个运行进程 |
| **Worker** | “让飞书机器人持续在线的进程” | 启动、停止、重启、健康检查、日志和消息消费 | 不替代 Profile、Assignment 或 Feishu 账号配置 |

可以用四个问题快速区分：

```text
Profile：智能体怎么工作？
Assignment：哪个入口用它？
Feishu：连接哪个飞书机器人和会话？
Worker：这个机器人现在有没有在运行？
```

### Profile、Assignment、Feishu、Worker 的关系

```text
Profile（定义能力）
    -> Publish（发布不可变版本）
Assignment（把入口映射到版本）
    -> Runtime Core（执行任务）
Feishu Bot Account（外部身份）
    -> Feishu Adapter
Worker（常驻进程）
    -> 接收消息并把请求交给 Runtime Core
```

一个 Feishu Worker 可以承载一个 Bot Account 的长连接；它不等于一个 Profile。
一个 Profile 可以被多个入口复用；它也不等于一个 Worker。Assignment 是中间的路由关系，
用来决定“这次请求应该使用哪个已发布版本”。

### Team 与飞书群不是一回事

- **飞书群**是外部聊天容器，里面有用户、机器人和消息。
- **Agent Team**是系统内部的协作配置，可以把多个 Profile 绑定成 coordinator、coder、
  reviewer 等角色。
- 一个飞书群可以绑定一个 Team，也可以只使用普通的单 Agent 路由。

## 与已有专题文档的关系

- [Profile、Team 与渠道术语](agent_profile_channel_glossary.md)
- [WebUI 2.0 设置中心](webui_v2_settings_center.md)
- [渠道 Worker 运维](channel_worker_screen_operations.md)
- [全局运行时拓扑 Mermaid 源文件](../../diagrams/global-runtime-topology.mmd)
- [架构图 Mermaid 源文件](../../diagrams/agent-platform-architecture.mmd)
