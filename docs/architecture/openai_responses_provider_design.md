# OpenAI Responses 上游 Provider 接入设计

> 2026-09-16 开源脱敏：验收网关地址已替换为 example.com 示例，不是可调用的
> 原始证据端点；协议、模型、状态与验收结论保持不变，原记录留在私有历史。

状态：Review 已通过，第一阶段已实现；指定真实端点 L0/L1 验收通过，L2 部分通过

更新日期：2026-08-25

## 1. 背景与目标

golang-cc 当前支持两类上游模型协议：

- `anthropic` / `anthropic-compatible` 通过 Anthropic Messages API 调用模型。
- `custom` / `openai` / `openai-compatible` / `openai-chat-completions` 通过 OpenAI Chat Completions API 调用模型。

OpenAI Responses API 使用 `/v1/responses`、typed items 和语义化 SSE 事件，不能只靠修改
`baseURL` 接入当前 Chat Completions client。与此同时，Azure OpenAI、Amazon Bedrock Mantle、
vLLM、Ollama、OpenRouter、LiteLLM 等服务已经提供不同程度的 Responses 兼容端点，但它们对
`store`、`previous_response_id`、Conversations、reasoning、built-in tools 和 background execution
的支持并不一致。

本方案的目标是：

1. 在不破坏现有 Anthropic Messages 和 OpenAI Chat Completions 行为的前提下，增加原生
   OpenAI Responses 上游 provider adapter。
2. 保持 `query.Session`、`agentruntime.Runtime` 和工具系统继续使用统一的内部消息模型，协议差异
   收敛在 provider 边界。
3. 第一阶段只交付无状态、客户端管理历史的 Responses 公共子集；同时预留
   `previous-response-id` 的常量、能力位、continuation schema 和存储接口，后续不改主架构即可接入。
4. 复用现有 provider fallback、熔断、分段超时、重试、partial stream、usage 和 telemetry 机制，
   避免形成第二套模型调用主链路。
5. 为未来接入 Gemini Interactions、Bedrock Converse 等其他原生 Agent 协议保留清晰的 adapter
   扩展点，但本期不实现这些协议。
6. 把现有 provider 行为兼容设为发布硬门禁：未配置 `providerProtocol` / `protocol` 时，`custom`、
   `openai`、`openai-compatible`、`openai-chat-completions` 和 Anthropic provider 必须继续走原有
   client、请求映射、SSE解析、重试、fallback、usage 和错误处理路径。
7. 协议扩展通过 resolver、backend factory/registry 和 capability 声明完成；以后增加新协议时，不在
   Agent loop 或 provider fallback 主循环中继续堆叠厂商特例。

## 2. 核心判断

### 2.1 Chat Completions 仍然适合 Agent

接入 Responses 的理由不是“Chat Completions 不能做 Agent”。golang-cc 当前已经基于 Chat
Completions 完成文本流、工具调用、多轮 tool result 回灌、上下文压缩、会话恢复、fallback 和
completion gate，证明该协议能够支撑完整 Agent loop。

Responses 的优势主要在协议表达和生态演进：

- 用 typed items 分离 message、function call、function output、reasoning 等动作。
- 用语义化流事件替代主要依赖 `choices[].delta` 的增量结构。
- 可以选择客户端重放 items、`previous_response_id` 或 Conversations 管理续轮状态。
- OpenAI 的新 Agent 能力优先围绕 Responses 演进。
- 部分模型服务只提供或优先提供 Responses 端点。

这些优势不会自动提升模型质量，也不会自动让 golang-cc 的本地工具系统变强。因此采用并存式
接入，不迁移或废弃当前 Chat Completions provider。

### 2.2 推荐原生 adapter，不使用隐式协议转换

可选路径比较如下：

| 路径 | 优点 | 问题 | 结论 |
| --- | --- | --- | --- |
| 外置 Chat/Responses 转换代理 | 接入快 | 丢失或弱化 typed items、reasoning/state 语义；增加部署点 | 仅作为临时外部方案 |
| 请求失败后自动切换协议 | 配置少 | 可能重复计费、重复生成或重复工具动作；错误不可预测 | 禁止 |
| 原生 Responses adapter | 协议语义完整；可测试；可复用现有 runtime | 需要新增转换和事件状态机 | 推荐 |
| 全量替换 Chat Completions | 单一 OpenAI 路径 | 破坏大量只支持 Chat Completions 的兼容网关 | 不采用 |

## 3. 范围与非目标

### 3.1 本期范围

- 增加明确的 `openai-responses` 上游协议选择。
- 支持自定义 Responses-compatible `baseURL` 和现有 API key/auth token 认证方式。
- 支持文本、图片、system instructions、function tools、tool results、reasoning effort、structured
  output、usage 和 SSE streaming。
- 将 Responses 输出转换为现有 `ContentBlock`、`StreamResult` 和 `StreamCallbacks`。
- 支持 provider retry、fallback、cooldown、partial stream、telemetry 和错误归一化。
- 支持本地 transcript/resume/branch/rewind/compact 所需的协议续轮状态持久化边界。
- 增加配置、provider、stream、tool loop、resume 和 fallback 测试。
- 更新配置示例、兼容矩阵和用户文档。

### 3.2 非目标

- 本期不让 golang-cc server 对外暴露 `POST /v1/responses`。该能力属于下游 API compatibility，
  应单独设计和验收。
- 不删除或修改 `/v1/chat/completions` 对外 API。
- 不默认迁移已有 `custom` / `openai` provider 到 Responses。
- 第一阶段不实现或对外宣称支持 `store=true`、`previous_response_id` 或 Conversations；相关配置
  即使已预留也必须明确返回 not implemented，不能静默降级成 stateless。
- 不在第一阶段接入 OpenAI hosted `web_search`、`file_search`、computer use、code interpreter、
  image generation、remote MCP、background response 或 Responses compaction。
- 不把 provider 端 Conversations 作为 golang-cc session 的事实来源。
- 不把 reasoning summary 当作完整思维链，也不记录服务端未明确允许展示的 reasoning 内容。
- 不在同一批改动中重命名整个 `internal/anthropic` 包；该包名是既有架构债，但大范围搬迁会显著
  扩大回归面。
- 不承诺所有宣称 OpenAI-compatible 的服务都支持相同 Responses 能力。

## 4. 现状与约束

### 4.1 当前统一模型

`internal/anthropic/types.go` 已定义 provider 中立的核心对象：

- `MessagesRequest`
- `MessageParam`
- `ContentBlock`
- `ToolDefinition`
- `ThinkingConfig`
- `ResponseFormat`
- `StreamCallbacks`
- `StreamResult`
- `Usage`

`internal/query` 和 `internal/agentruntime` 只依赖 `StreamMessages(...)`，因此不需要让上层直接理解
Responses SDK 类型。

### 4.2 当前 provider client 的耦合

`internal/anthropic/client.go` 当前同时持有 Anthropic SDK client 和 Chat Completions client，并在
`streamMessagesWithProvider` 中按 provider kind 分派。Chat Completions 的请求转换、流读取、
tool-call 累积、usage 转换和重试也集中在同一个文件中。

直接继续增加第三套分支会让 client 文件承担过多职责。新增 Responses 前，应把“provider 选择与
fallback 编排”和“具体协议转换”解耦，但要通过同包小步拆分控制改动范围。

### 4.3 会话和状态约束

golang-cc 支持本地 transcript、resume、message graph branch、rewind、compact 和 provider fallback。
这些能力决定了远端 response ID 不能成为唯一上下文来源：

- response ID 绑定产生它的 provider、endpoint 和 model。
- fallback provider 不能假定可以读取另一个 provider 的 response ID。
- rewind 或切换 branch 后，旧 response ID 可能指向错误的历史分支。
- compact 会改变发送给模型的历史形状。
- 第三方 Responses-compatible 服务可能完全不支持状态存储。

因此本地 transcript 始终是会话事实来源，远端状态只能作为可失效的 provider continuation。

## 5. 总体架构

### 5.1 分层

```text
query.Session / agentruntime.Runtime
                |
                v
      canonical MessagesRequest
                |
                v
 Provider Client (routing/fallback/policy)
      |              |               |
      v              v               v
 Anthropic       OpenAI Chat      OpenAI Responses
 Messages        Completions      Backend
 Backend         Backend
      |              |               |
      +--------------+---------------+
                     |
                     v
      StreamCallbacks / StreamResult
```

职责边界：

| 层 | 职责 | 不负责 |
| --- | --- | --- |
| query/agentruntime | Agent loop、工具执行、权限、context、session | 上游 wire format |
| Provider Client | provider 顺序、model 路由、cooldown、fallback、安全重试 | 协议字段拼装 |
| Protocol Backend | 请求转换、SDK调用、SSE事件归一化、usage/stop reason 转换 | 工具真实执行 |
| Session | 本地消息事实、branch/rewind/resume、continuation 持久化 | 隐式远端会话缓存 |

### 5.2 协议解析与 Backend 扩展接口

建议在 `internal/anthropic` 包内增加未导出的协议扩展接口，避免第一阶段搬迁所有公共类型。配置层
只负责保存和校验协议值；provider kind 与 protocol 的解析集中由 resolver 完成；协议实现由 registry
中的 factory 创建：

```go
type protocolSource string

const (
    protocolSourceLegacyDerived protocolSource = "legacy-derived"
    protocolSourceExplicit      protocolSource = "explicit"
)

type resolvedProviderProtocol struct {
    Kind     string
    Protocol config.ProviderProtocol
    Source   protocolSource
}

type protocolResolver interface {
    Resolve(kind string, configured config.ProviderProtocol) (resolvedProviderProtocol, error)
}

type providerCapability string

const (
    capabilityFunctionCalling    providerCapability = "function-calling"
    capabilityReasoningSummary   providerCapability = "reasoning-summary"
    capabilityPreviousResponseID providerCapability = "previous-response-id"
)

type providerCapabilities map[providerCapability]bool

type providerBackend interface {
    Protocol() config.ProviderProtocol
    Capabilities() providerCapabilities
    StreamMessages(
        ctx context.Context,
        req MessagesRequest,
        cb StreamCallbacks,
    ) (*StreamResult, error)
}

type backendFactory interface {
    NewBackend(provider providerSpec) (providerBackend, error)
}
```

具体接口签名允许在实现 Review 时根据 SDK client 生命周期微调，但职责不能重新混合：

| 抽象 | 单一职责 | 禁止承担 |
| --- | --- | --- |
| `protocolResolver` | 解析显式 protocol、推导 legacy protocol、校验 kind/protocol 冲突 | 创建 SDK client、发 HTTP 请求 |
| backend registry | `ProviderProtocol -> backendFactory` 注册与查找 | 根据 endpoint 猜协议、运行时自动降级协议 |
| `backendFactory` | 根据单个 provider 配置创建隔离的 backend/client | provider 排序、fallback 策略 |
| `providerBackend` | 单一 wire protocol 的请求/流/错误转换 | Agent tool 执行、session 事实管理 |
| `providerCapabilities` | 声明经过实现和验收的可选能力 | 把厂商名当作能力判断依据 |

capability 只能控制可选字段和显式校验，不能在运行时用一次失败结果永久“学习”端点能力。第三方
兼容服务的能力仍以配置和兼容矩阵中的真实验收结果为准。

registry 在 `NewClient` / provider 装配阶段一次性构造，随后只读；重复注册和未知 protocol 直接
报错。不要使用可被请求并发修改的全局 map，也不在本期引入动态插件加载。

### 5.3 老 Provider 零侵入接入策略

第一阶段采用 additive path，不先重写稳定的 Anthropic/Chat Completions 实现：

1. `providerClient` 只新增解析后的 protocol/source，以及仅新协议需要的 backend 引用。
2. 未显式配置 protocol 时，调用现有 legacy dispatcher；保留当前 `providerKindOpenAI(kind)` 分支和
   `streamOpenAIChatCompletion` 行为，不经过新的 Responses factory。
3. 显式配置 `openai-responses` 时，才从 registry 创建 Responses backend。
4. 显式配置 `openai-chat-completions` / `anthropic-messages` 时，仍委托现有实现，不迁移 SDK、不改
   请求映射。
5. 将老实现完整包装成 backend 是后续独立重构任务；必须先有 contract tests，不能与 Responses
   首次接入绑定发布。

伪代码边界如下：

```go
if provider.protocolSource == protocolSourceLegacyDerived {
    return c.streamMessagesWithLegacyDispatch(ctx, provider, req, cb)
}
if provider.protocol == config.ProviderProtocolOpenAIResponses {
    return provider.backend.StreamMessages(ctx, req, cb)
}
return c.streamMessagesWithExistingProtocol(ctx, provider, req, cb)
```

其中 `streamMessagesWithLegacyDispatch` 表示现有分派行为的保留边界，不要求第一阶段为了函数命名而
机械搬动大段稳定代码。这个兼容分支是有意设计的迁移护栏，不是永久复制两套实现。

建议拆分为：

```text
internal/anthropic/client.go                    # provider 编排及原 Anthropic/Chat legacy 实现
internal/anthropic/provider_protocol.go         # resolver、冲突校验、capability 常量
internal/anthropic/backend_registry.go          # immutable registry 与 factory 装配
internal/anthropic/backend_openai_responses.go  # Responses
internal/anthropic/provider_error.go            # 错误归一化
```

第一阶段不移动当前 `client.go` 中 Anthropic/Chat 的大段稳定代码，只增加新协议接入点。未来若要
统一为 `backend_anthropic.go` / `backend_openai_chat.go`，应作为独立重构，在 contract tests 保护下
逐步委托，避免一次大重构与新功能叠加。

### 5.4 SDK选择

当前 `github.com/sashabaranov/go-openai v1.41.2` 不提供 Responses API。Responses backend 确定使用
OpenAI 官方 Go SDK `github.com/openai/openai-go/v3`：

- 提供 `Responses.NewStreaming`。
- 提供 typed input/output items 和 stream event union。
- 支持自定义 base URL、API key、HTTP client 和 request options。
- 覆盖 `previous_response_id`、function call output、reasoning 和 usage 类型。

第一阶段允许两个 OpenAI SDK 并存：现有 Chat Completions 保持旧 SDK，Responses 使用官方 SDK。
本期不迁移 Chat Completions；未来迁移必须另开任务，不能和本功能绑定。

新增依赖前必须检查许可证、Go版本、间接依赖和二进制体积，并固定经过测试的具体版本，不使用浮动
`latest`。

## 6. 配置设计

### 6.1 分离 provider kind 与 wire protocol

现有 `provider` / `ProviderConfig.Type` 同时承担厂商类别和协议分派。为了避免继续增加含义混杂的
type 字符串，新增显式协议字段：

```go
type ProviderProtocol string

const (
    ProviderProtocolAnthropicMessages     ProviderProtocol = "anthropic-messages"
    ProviderProtocolOpenAIChatCompletions ProviderProtocol = "openai-chat-completions"
    ProviderProtocolOpenAIResponses       ProviderProtocol = "openai-responses"
)
```

建议新增：

```go
type Settings struct {
    Provider         string                   `json:"provider,omitempty" yaml:"provider,omitempty"`
    ProviderProtocol ProviderProtocol         `json:"providerProtocol,omitempty" yaml:"providerProtocol,omitempty"`
    Responses        *ResponsesProviderSettings `json:"responses,omitempty" yaml:"responses,omitempty"`
    // existing fields...
}

type Config struct {
    ProviderProtocol ProviderProtocol
    Responses        *ResponsesProviderSettings
    // existing fields...
}

type ProviderConfig struct {
    Protocol  ProviderProtocol          `json:"protocol,omitempty" yaml:"protocol,omitempty"`
    Responses *ResponsesProviderSettings `json:"responses,omitempty" yaml:"responses,omitempty"`
    // existing fields...
}
```

这里两个字段回答不同问题：

- `provider` / fallback `type`：这个逻辑 provider 是谁，继续承载现有 provider 选择、显示、定价
  分类、模型绑定、认证默认值等兼容语义。
- `providerProtocol` / fallback `protocol`：和端点通信时使用哪一种 wire contract，决定请求路径、
  JSON schema、SSE事件和错误结构。

分离后的主要收益：

| 维度 | 不分离：继续增加 provider type | 分离 provider 与 protocol |
| --- | --- | --- |
| 同一服务多协议 | 需要 `openai-chat`、`openai-responses` 等多个 type | `provider=openai`，显式选择 protocol |
| 同一协议多实现 | 容易出现 `azure-responses`、`custom-responses`、`openrouter-responses` 组合爆炸 | 多个 provider 复用同一 protocol backend |
| 路由与协议转换 | provider 选择、定价、telemetry、wire 分派混在一个字符串 | 路由看 provider，adapter 看 protocol |
| 新协议扩展 | 修改主循环中的多个 `if/switch` | 注册新的 protocol 常量和 backend factory |
| 配置可读性 | `custom` 无法说明端点究竟是 Chat 还是 Responses | endpoint 的 wire contract 明确且可启动时校验 |
| 兼容迁移 | 改旧 type 含义会让存量配置静默换协议 | protocol 缺省保持 legacy；新协议必须显式 opt-in |

这一分离不是为了让配置“更抽象”，而是为了避免两个独立变化维度做笛卡尔积。OpenAI、Azure 或
自建网关可能同时提供 Chat Completions 和 Responses；OpenAI、Azure、Bedrock Mantle、vLLM、
Ollama、OpenRouter、LiteLLM 又可能实现同一种 Responses wire protocol。provider 和 protocol
并不是一一对应关系。

代价是多一个配置字段，因此必须同时提供明确默认值、冲突校验和单一优先级，不能让两个字段互相
覆盖或靠 endpoint 探测猜测。

`providerProtocol` 放在顶层而不是藏进 `responses` 子配置，是因为它是选择 backend 的
discriminator，必须先于协议专属参数解析：加载器先确定 `openai-responses`，再校验
`responses.stateMode/store`。如果用“出现 `responses` 对象”隐式选择协议，空对象、merge 后残留
字段和未来新增 `gemini` / `bedrock` 设置都会产生歧义。协议专属子对象只保存调优项，不承担路由。

顶层字段命名为 `providerProtocol` 是为了在整个 settings namespace 中说明它属于模型 provider；
fallback item 已经处于 provider 对象内，所以简写为 `protocol` 即可。两者语义一致，只是作用域不同。

顶层 settings 使用 `providerProtocol`，fallback provider 使用 `protocol`：

```yaml
model: gpt-5.5
provider: custom
providerProtocol: openai-responses
env:
  ANTHROPIC_BASE_URL: https://gateway.example.com/v1
  ANTHROPIC_API_KEY: ${RESPONSES_API_KEY}

fallback:
  enabled: true
  providers:
    - name: local-responses
      type: custom
      protocol: openai-responses
      baseURL: http://127.0.0.1:8000/v1
      apiKey: ${LOCAL_RESPONSES_API_KEY}
      model: local-model
      responses:
        stateMode: stateless
        store: false
```

顶层继续复用既有 `ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY` 是为了保持当前配置加载路径兼容，
不是推荐的长期中立命名。统一 primary/fallback provider 配置属于后续 provider-neutral config 重构。

### 6.2 旧配置兼容

未填写 protocol 时按现有 type 推导 effective protocol，但该推导只用于校验、telemetry 和未来迁移；
第一阶段的实际请求仍走 5.3 节定义的 legacy dispatcher，不能因为“推导结果相同”就顺带替换老
SDK、client 初始化或事件处理：

| 现有 type | 默认 protocol |
| --- | --- |
| 空、`anthropic`、`anthropic-compatible` | `anthropic-messages` |
| `custom`、`openai`、`openai-compatible`、`openai-chat-completions` | `openai-chat-completions` |

只有显式 `providerProtocol: openai-responses` 或 fallback `protocol: openai-responses` 才进入
Responses backend。不得通过 endpoint 探测，也不得在 404/400 后自动切换协议。

配置解析和冲突规则：

1. primary 的唯一协议来源是顶层 `providerProtocol`；fallback 的唯一协议来源是该项自己的
   `protocol`，二者不继承。
2. 字段缺省表示 legacy，不等于把缺省值写回配置文件，也不能改变现有 merge、环境变量优先级、
   provider 选择或 fallback 顺序。
3. 显式 `openai-responses` 允许与 `custom`、`openai`、`openai-compatible` 搭配。
4. `anthropic` / `anthropic-compatible` 与 OpenAI 协议冲突；`openai-chat-completions` 与
   `openai-responses` 也语义冲突，启动/加载时明确报错，不静默以其中一个为准。
5. 未知 protocol 必须 fail fast；禁止回退成 Chat Completions。

兼容门禁要求未设置 protocol 的老配置在以下可观察行为上保持不变：

- `Config.Provider`、`ProviderConfig.Type`、selected provider、fallback 顺序和 model override。
- base URL 默认值/去尾斜杠、API key/auth token 继承及环境变量展开。
- `custom` 等别名继续使用现有 `sashabaranov/go-openai` Chat Completions client。
- HTTP path、请求体、headers、SSE回调顺序、usage/stop reason、retry/fallback/cooldown 和错误分类。
- pricing、TUI/provider display、telemetry 中依赖 provider kind 的既有分类；新增 protocol 维度不能
  反向改变这些行为。

### 6.3 Responses 能力配置

不同服务兼容范围不同。第一阶段不增加大量布尔字段，先定义每个 provider 独立的 Responses 设置和
状态模式：

```go
type ResponsesStateMode string

const (
    ResponsesStateModeStateless          ResponsesStateMode = "stateless"
    // Reserved for the next phase; first-phase validation rejects this mode.
    ResponsesStateModePreviousResponseID ResponsesStateMode = "previous-response-id"
)

type ResponsesProviderSettings struct {
    StateMode ResponsesStateMode `json:"stateMode,omitempty" yaml:"stateMode,omitempty"`
    Store     *bool              `json:"store,omitempty" yaml:"store,omitempty"`
}
```

建议配置：

```yaml
providerProtocol: openai-responses
responses:
  stateMode: stateless
  store: false
```

规则：

- 第一阶段唯一可运行模式是 `stateMode=stateless`、`store=false`，覆盖面最大且本地可恢复。
- primary 和每个 fallback provider 独立解析 Responses 设置；不得把 primary 的 state mode 套到
  fallback。
- `previous-response-id` 枚举值为后续阶段预留；第一阶段若配置该值，启动/加载阶段返回明确的
  `not implemented` 错误，不发起请求。
- 第一阶段配置 `store=true` 同样明确报错；不静默改为 `false`，也不做运行时自动探测。
- 后续启用 `previous-response-id` 时必须要求 `store=true`，该组合校验入口在第一阶段预留。
- hosted tools、background、conversation、compaction 等尚未实现的字段不得透传，避免文档和真实能力不符。

## 7. 请求映射

### 7.1 基础字段

| 内部字段 | Responses 字段 | 规则 |
| --- | --- | --- |
| `MessagesRequest.Model` | `model` | provider 配置 model 仍可覆盖 |
| effective system text | `instructions` | 合并现有 `System` / `SystemBlocks` 语义 |
| `MaxTokens` | `max_output_tokens` | 保留现有默认值和 thinking 预算逻辑 |
| `Thinking.Effort` | `reasoning.effort` | 仅发送服务明确支持的合法档位 |
| `ResponseFormat` | `text.format` | JSON object/schema 按 Responses 结构转换 |
| `Tools` | `tools[].type=function` | 参数 schema 必须保持原 JSON，不降级为字符串 |
| `Stream` | streaming method | runtime 始终走流式主链路 |

### 7.2 消息和内容块

| `ContentBlock` | Responses input item | 说明 |
| --- | --- | --- |
| user text | message + `input_text` | 保持消息顺序 |
| assistant text | message + `output_text`/等价 input representation | 用于无状态历史重放 |
| image URL | `input_image.image_url` | 保留 URL |
| base64 image | data URL 或 SDK支持的 image input | 保留 media type |
| `tool_use` | `function_call` | `ID` 映射为 `call_id` |
| `tool_result` | `function_call_output` | `ToolUseID` 映射为 `call_id` |
| thinking/signature | 不直接转普通文本 | 只能走协议允许的 opaque continuation |
| redacted thinking | 不直接展示 | 仅在协议要求时原样回传 |

转换函数必须保持小函数和单一职责，例如：

```text
responsesRequest(...)
responsesInputItems(...)
responsesMessageItem(...)
responsesFunctionCallItem(...)
responsesFunctionOutputItem(...)
responsesTool(...)
responsesTextFormat(...)
responsesReasoning(...)
```

任何无法安全映射的内容块必须采用明确策略：可降级为用户可见占位文本、明确忽略并记录 telemetry，
或直接返回 unsupported error；不能静默丢失会影响工具闭环或安全边界的内容。

## 8. 流事件与结果映射

### 8.1 事件状态机

Responses SSE 不是 Chat Completions chunk。backend 必须按事件类型处理，至少覆盖：

| Responses 事件 | runtime 行为 |
| --- | --- |
| `response.output_text.delta` | 调用 `OnText` 并累积最终文本 |
| `response.reasoning_summary_text.delta` | 调用 `OnThinking`，不得混入最终文本 |
| `response.function_call_arguments.delta` | 按 output/item/call 维度累积参数 |
| output item added/done | 建立/完成 function call accumulator |
| `response.completed` | 读取最终 status、output、usage 和 response ID |
| `response.incomplete` | 转换 incomplete reason；保留已输出 partial result |
| `response.failed` | 转换 provider error；保留已输出 partial result |
| `error` / `response.error` | 转换 provider protocol error；保留已输出 partial result |
| stream transport error | 走现有 partial stream error 语义 |

function call accumulator 不能只按数组下标识别，至少要兼容 `output_index`、`item_id` 和 `call_id`，
最终输出：

```go
ContentBlock{
    Type:  "tool_use",
    ID:    callID,
    Name:  functionName,
    Input: argumentsJSON,
}
```

参数完成时必须校验 JSON。非法 JSON 作为 provider protocol error 返回，不能把损坏参数交给工具执行。

### 8.2 Stop reason

| Responses 结果 | `StreamResult.StopReason` |
| --- | --- |
| completed 且包含 function call | `tool_use` |
| completed 且只有最终输出 | `end_turn` |
| incomplete: max output tokens | `max_tokens` |
| caller cancellation | 保持 cancellation error |
| failed | 返回错误，不伪造成正常 stop |

### 8.3 Usage

| Responses usage | 内部 `Usage` |
| --- | --- |
| `input_tokens` | `InputTokens` |
| `input_tokens_details.cached_tokens` | `CacheReadInputTokens` |
| `output_tokens` | `OutputTokens` |
| `output_tokens_details.reasoning_tokens` | `ReasoningOutputTokens` |

`InputTokensIncludeCacheRead` 必须按 Responses 的计数语义设置。无法确定的第三方扩展字段不猜测、
不套用 Anthropic cache write 倍率。

## 9. Reasoning 与续轮状态

### 9.1 展示边界

Responses reasoning 需要区分：

- 可展示的 reasoning summary：可以通过 `OnThinking` 进入现有 thinking UI/stream。
- 不可展示但需要续轮的 opaque/encrypted reasoning state：只能持久化并原样回传。
- reasoning token usage：只进入 usage/telemetry。

禁止把 encrypted content、原始 opaque state 或服务端未声明可展示的内容写入普通 assistant text、日志或
错误消息。

### 9.2 Stateless 模式

默认模式由 golang-cc 重放本地历史：

```text
local messages + prior function calls + function outputs + required opaque state
    -> POST /v1/responses (store=false)
```

优点：

- 兼容明确无状态的 OpenRouter、Ollama 等实现。
- fallback、resume、branch、rewind 更可控。
- 不依赖 provider 的保留周期和服务端存储。

要求：

- 不能只重放可见文本；reasoning model 在工具续轮中要求回传的 opaque item 必须被保留。
- opaque state 必须带 protocol、provider identity、model 和所属 assistant turn，防止跨 provider 误用。
- compact 时不能把仍参与当前工具闭环的 opaque state 压成自然语言摘要。

### 9.3 Previous response ID 预留边界（后续阶段）

第一阶段不发送 `previous_response_id`，也不使用远端 response state。这里定义的是后续接入必须遵守
的架构契约，避免届时修改 Agent loop、store 接口或 transcript 基础格式。

后续阶段显式开启后，下一轮才可发送：

```text
previous_response_id + new input/function_call_output
```

continuation 至少包含：

```go
type ProviderContinuation struct {
    Protocol   ProviderProtocol
    Provider   string
    EndpointID string
    Model      string
    ResponseID string // Reserved; empty in the first-phase stateless mode.
    BranchID   string
}
```

`EndpointID` 应保存规范化 endpoint 的不可逆 hash，不保存带凭证 URL。continuation 只有在 protocol、
provider、endpoint、model 和当前 branch 都匹配时才可使用。

后续启用 response ID 后，以下情况必须使远端 continuation 失效并回退到本地重放，或返回明确错误：

- provider/fallback 切换。
- model 或 endpoint 改变。
- rewind 到 response 产生前。
- session branch 切换。
- provider 返回 response not found/expired。
- compact 结果与 response 所代表的远端历史不再一致。

第一阶段已经要求 continuation 进入 transcript/message graph，并经过 resume、branch 和 rewind 测试，
不得用进程内 map 作为唯一存储。这样后续增加非空 `ResponseID` 时不需要重新设计持久化主链路。

### 9.4 Continuation 存储抽象

CLI/TUI 的本地 transcript 和 API server 的多实例部署不是同一种持久化环境。为了让第一阶段的
stateless opaque items 和后续 stateful response ID 使用同一生命周期边界，定义窄接口：

```go
type ProviderContinuationStore interface {
    Load(ctx context.Context, key ContinuationKey) (*ProviderContinuation, error)
    Save(ctx context.Context, key ContinuationKey, value ProviderContinuation) error
    Invalidate(ctx context.Context, key ContinuationKey, reason string) error
}
```

实现边界：

- CLI/TUI 可以由 transcript/message graph adapter 实现。
- 第一阶段由该接口存储 stateless 工具续轮所需的 opaque items；接受本地 transcript 新增
  `provider_continuation` entry type。
- API server 后续若启用 stateful Responses，必须使用与 session 生命周期一致的共享持久化实现；
  不能依赖单实例内存或仅本机可见文件。
- 第一阶段无条件拒绝 `previous-response-id`；后续阶段启用时，还必须检查可用的 continuation store，
  缺失时在启动或 session 初始化阶段失败。
- store key 必须包含 tenant/user/session/branch/provider/model/endpoint identity，避免跨租户或跨分支
  读取。
- continuation 不是聊天正文，不应混入普通 message content；共享存储实现必须继承现有 tenant/user
  隔离、加密、访问控制和审计边界。

本期 stateless 公共子集不要求新增 MySQL schema。未来若 API server 的 stateful 模式采用 MySQL 持久化，
必须另行设计 migration、repository、tenant isolation、过期清理和并发更新测试，不能只修改 Go struct。

### 9.5 建议的持久化格式

不要把 provider continuation 塞入普通 `ContentBlock.Text`。本地 transcript adapter 建议新增明确
entry type，例如 `provider_continuation`，字段采用版本化 JSON envelope：

```json
{
  "version": 1,
  "protocol": "openai-responses",
  "provider": "primary",
  "endpoint_hash": "...",
  "model": "gpt-5.5",
  "branch_id": "...",
  "opaque_items": []
}
```

envelope schema 预留可选 `response_id` 字段，但第一阶段不写入；读取端必须允许该字段缺失。后续增加
response ID 时只提升 capability/validation 和字段写入，不改变 entry type 或 store 接口。

持久化前应限定最大字节数；日志和 telemetry 只记录是否存在、大小、hash 和状态，不记录完整 opaque
payload。

## 10. Retry、Fallback 与幂等

### 10.1 安全原则

沿用现有规则：

- 建流失败且尚未产生任何 output item/delta 时，才允许按错误分类重试或 fallback。
- Responses reasoning summary 在 attempt 提交前由 provider orchestration 层缓冲；失败 attempt 的 reasoning 不进入
  TUI/API/query sink，成功 attempt 的 reasoning 只提交一次。
- `responses_sse_json_decode` 在只收到 reasoning、没有 text/tool item/tool arguments 时，允许初次失败后额外重试
  6 次，即最多 7 次 golang-cc orchestration stream attempts。
- 退避从 300ms 指数增长并封顶 5s：300ms、600ms、1.2s、2.4s、4.8s、5s；caller cancellation
  会立即中断退避。
- 没有任何 delta 的 JSON decode error 和其他既有可重试 stream failure 保持 1 次额外重试；显式
  `response.failed`/`error` 保持直接 fallback，不增加原地重试。
- 一旦收到 text、function call item 或 arguments delta，当前 attempt 即提交：禁止原地重试和 provider fallback；
  reasoning 先按原顺序提交，provider failure 进入 cooldown，避免重复文本或工具语义。
- 明确 4xx、schema error、unsupported parameter 不进行无意义重试。
- 429、408、409、5xx 和网络瞬时故障复用统一 retry policy 与 `Retry-After`。
- caller cancellation 不进入 cooldown。

### 10.2 重复请求风险

Responses 请求可能触发 provider hosted tools。虽然本期不开放 hosted tools，仍需把未来风险写入边界：

- retry 不等价于服务端绝对没有执行。当前六次预算只用于 stateless Responses、未启用 hosted tools、且没有
  text/tool 语义的 reasoning-only decode failure；未来开放 hosted tools/background 前必须重新评估幂等边界。
- 六次预算的潜在负作用是最多增加 14.3s 退避、最多 6 次额外模型请求及相应 token/费用；通过
  `retry_attempt`、`retry_limit`、`retry_outcome` 和 request id 观测恢复率、耗时与成本。
- 不允许自动从 Responses 降级为 Chat Completions。
- 自定义 function call 只生成调用意图，真实本地工具仍由 golang-cc 执行一次，并沿用现有权限、
  sandbox 和 closure gate。
- 后续若开放 hosted tools/background，必须单独设计 request idempotency 和取消语义。

## 11. 错误与可观测性

### 11.1 错误归一化

建议新增内部 provider error：

```go
type ProviderError struct {
    Provider   string
    Protocol   ProviderProtocol
    Phase      string
    Code       string
    HTTPStatus int
    Retryable  bool
    RetryAfter time.Duration
    Err        error
}
```

保留 `Unwrap()`，让现有 context cancellation、timeout 和 SDK error 判断继续工作。对外错误不能包含 API
key、Authorization header、完整私有 endpoint query 或完整响应正文。

### 11.2 Telemetry

复用现有 model phase 事件，增加/确认以下属性：

- `provider_protocol=openai-responses`
- `response_state_mode`
- `response_id_present`，不记录完整 response ID
- `output_items`
- `text_deltas`
- `reasoning_deltas`
- `function_call_deltas`
- `function_calls`
- `usage.input_tokens/output_tokens/cached_tokens/reasoning_tokens`
- `incomplete_reason`
- `retry_attempt`
- `events`、`first_event_seen`、`first_delta_seen`
- `first_event_gap_ms`、`first_delta_gap_ms`
- `failure_kind`、`retryable`
- `provider_request_id`、`http_status`、`response_mime`（仅诊断元数据，不记录响应正文）
- `retry_reason`、`retry_limit`、`retry_outcome`
- `last_failed_provider_request_id`（retry outcome 关联最后一次失败请求，不冒充成功 attempt 的 request id）

endpoint 继续使用现有脱敏逻辑，禁止记录 API key、auth token、完整 prompt、tool result 或 opaque reasoning。

## 12. 第三方兼容分级

“OpenAI-compatible Responses”不是一个能力完全一致的行业标准。兼容声明应拆成层级：

| 层级 | 能力 | 第一阶段要求 |
| --- | --- | --- |
| L0 Transport | `POST /v1/responses`、JSON error、SSE | 必须 |
| L1 Core Items | text message、function call/output、usage | 必须 |
| L2 Reasoning | effort、summary、opaque continuation、reasoning usage | 支持时启用 |
| L3 State | `store`、`previous_response_id`、Conversations | 本期不交付，仅预留扩展点 |
| L4 Hosted Agent | web/file search、MCP、background、compaction | 本期不做 |

已核实的代表性差异：

| 服务 | 公开兼容边界 | golang-cc 默认策略 |
| --- | --- | --- |
| OpenAI | 协议基准，支持 state/items/tools | 本期只使用 stateless；stateful 后续接入 |
| Azure OpenAI | v1 Responses，认证/区域/model deployment 不同 | 自定义 base URL；单独实测 |
| Amazon Bedrock Mantle | OpenAI-compatible Responses，支持 state；模型/endpoint 范围不同 | 自定义 base URL；按模型实测 |
| vLLM | 声明 Responses compatible；能力受版本、模型、启动参数影响 | stateless 基线；固定验收版本 |
| Ollama | 只支持无状态 Responses，不支持 previous response/conversation | stateless |
| OpenRouter | 无状态；拒绝 `store=true` 和非空 `previous_response_id` | stateless |
| LiteLLM | 提供 Responses endpoint 和跨 provider 转换 | stateless 基线；按后端能力验收 |

Gemini Interactions、Anthropic Messages、Bedrock Converse 等是功能相近但 wire format 不兼容的独立
协议，未来需要各自 backend，不能归入 `openai-responses`。

不能仅凭服务返回 2xx 就宣称兼容。至少要完成 text streaming、function call loop、usage、错误和中途
断流验收后，才能在兼容矩阵中标记相应层级。

## 13. 渐进式开发计划

### 阶段 0：Review 与契约锁定

产出：

- Review 本文档并关闭配置命名、默认 state mode、SDK和 continuation 持久化方式等开放问题。
- 真实目标端点由用户在开发完成后提供；端点未知不阻塞 stub/fixture 开发，也不允许在代码里预写
  厂商特例。
- 固化 legacy provider contract fixtures，明确哪些现有行为必须逐项保持。

验收：

- 架构、范围和兼容声明得到人工确认。
- 没有未决问题会改变配置或 transcript schema。
- `custom` / OpenAI Chat Completions 和 Anthropic Messages 的关键 legacy fixture 已能在改造前运行，
  作为后续新旧代码对比基线。

### 阶段 1：协议分类与 Backend 骨架

产出：

- `ProviderProtocol` 常量与 legacy type 推导。
- primary/fallback protocol 配置解析、合并和校验。
- protocol resolver、backend registry/factory、capability 常量与 Responses backend 骨架。
- 官方 OpenAI Go SDK依赖评估和固定版本。
- 保留现有 Chat Completions SDK；本期允许两个 OpenAI Go SDK 并存，不执行 SDK 迁移。

验收：

- 旧配置解析和运行时分派结果不变，仍走 legacy dispatcher。
- Responses 配置进入新 backend。
- 未知 protocol 和冲突 state 配置明确报错。
- `custom` 等老 provider 的 contract fixtures 在改造前后无差异。

### 阶段 2：无状态文本、图片和 Structured Output

产出：

- `/v1/responses` request 映射。
- text/image/instructions/max output/reasoning effort/text format 映射。
- text/reasoning/usage/completed/incomplete/failed stream 处理。

验收：

- 本地 stub 断言真实路径和完整请求体。
- text、image、JSON schema、reasoning summary 和 usage 测试通过。
- malformed/midstream error 返回 partial result。

### 阶段 3：Function Calling 闭环

产出：

- function tool schema 转换。
- function call arguments accumulator。
- function call output 回传。
- 并行 tool calls 和非法 arguments 处理。

验收：

- stub 驱动 `function_call -> local tool -> function_call_output -> final text` 全链路。
- 多工具 ID/顺序正确，工具只执行一次。
- permission、sandbox、hooks 和 completion gate 行为不回归。

### 阶段 4：Continuation、Resume 与 Branch

产出：

- opaque continuation envelope 和大小限制。
- `ProviderContinuationStore` 及本地 transcript adapter。
- stateless reasoning continuation 重放。
- 预留 `previous-response-id` 常量、capability、envelope 可选字段和校验入口，但不发送
  `previous_response_id`。
- transcript、resume、branch、rewind、compact 失效策略。

验收：

- 进程重启后工具续轮仍可恢复。
- `provider_continuation` entry 能随 transcript 正确保存、读取和兼容旧 transcript。
- provider/model/endpoint 切换使 continuation 安全失效。
- fallback 不跨 provider 传递 continuation。
- `previous-response-id` 和 `store=true` 在配置阶段明确返回 not implemented。

### 阶段 5：错误、Fallback、文档和真实端点验收

产出：

- ProviderError、retry/fallback/telemetry 对齐。
- 配置示例、README、compatibility matrix 更新。
- 本地可复现 acceptance script。
- 用户提供目标端点后，执行真实 curl/CLI 工具闭环验证。

验收：

- 无输出故障可以安全 fallback；有输出故障不切 provider。
- 429/5xx/timeout/cancel 分类正确。
- 用户尚未提供端点时，可以完成代码与本地验收，但不得宣称真实端点验收完成。
- 用户提供端点后，按 L0-L4 如实记录能力，不把部分兼容写成完全兼容。

## 14. 测试与验收清单

### 14.1 Config

- legacy provider type 推导保持原行为。
- protocol 缺省时保持 legacy source，不写回默认值，也不进入 Responses factory。
- primary 与 fallback protocol JSON/YAML 解析。
- primary 与各 fallback 的 Responses settings 相互独立。
- environment expansion 与 auth inheritance。
- unknown protocol、state/store 冲突、空 base URL 默认值。
- 第一阶段 `previous-response-id` 和 `store=true` 均返回明确 not implemented，不产生 HTTP 请求。
- provider kind/protocol 合法组合和冲突矩阵。
- model-based provider ordering 不因 protocol 改变。

### 14.2 Legacy Provider Contract

在增加 Responses 前先从现有行为录制/整理稳定 fixture，并在改造后逐项对比：

- 顶层与 fallback 的 `custom`、`openai`、`openai-compatible`、`openai-chat-completions`。
- `anthropic`、`anthropic-compatible` 和空 provider 的默认行为。
- Chat Completions 的 URL、headers、request JSON、文本 delta、tool call delta、usage 和 stop reason。
- Anthropic Messages 的 URL、headers、request JSON、thinking/signature/tool use 和 usage。
- 建流失败重试、`Retry-After`、超时、取消、partial stream、cooldown 和 fallback 切换边界。
- selected provider、provider model override、credential inheritance、pricing/provider-kind 查询和 TUI 显示。

测试目标不是只断言 effective protocol 字符串相同，而是锁定真正的外部行为。尤其是用户所称的
`openai-custom` 路径，在当前代码中的实际类型集合是 `custom` / `openai-compatible` 等 Chat
Completions aliases，必须全部覆盖，不能只测 `openai`。

### 14.3 Request Mapping

- instructions 和 system blocks 顺序。
- user text 编码为 `input_text`；assistant 历史文本编码为 typed output message + `output_text`，
  并保留服务端 message ID。旧 transcript 没有 message ID 时生成稳定 replay ID。
- image URL、base64 image、无有效 source 的降级文本。
- function tool schema 完整保留。
- tool use/result call ID 对齐。
- JSON object/JSON schema structured output。
- reasoning effort 和 max output tokens。
- 不支持 content block 的显式行为。

### 14.4 Stream State Machine

- text delta 和 reasoning summary delta 分流。
- 单个、多个、并行 function calls。
- arguments 跨多个 delta 拼接。
- output item done 与 completed 顺序变化。
- usage/cached/reasoning tokens。
- incomplete/failed/error event。
- `[DONE]`、EOF、keepalive/comment、未知向前兼容事件。
- malformed JSON、非法 arguments、连接中断、callback error。

### 14.5 Retry 与 Fallback

- 建流前网络错误、408/409/429/5xx。
- `Retry-After` 秒数和日期格式。
- caller cancel、provider timeout 和 cooldown。
- 首个有意义事件前允许 fallback；reasoning-only JSON decode error 在 attempt-local reasoning 缓冲保护下最多额外
  重试 6 次，耗尽后 cooldown 并按既有 provider fallback 恢复。
- text/function item/function arguments 后禁止 retry 和 fallback；tool commit 前先提交已缓冲 reasoning。
- 无 delta JSON/non-JSON stream failure 保持 1 次重试；terminal provider event 不原地重试。
- 中途失败的 runtime span 保留事件计数、delta 计数和脱敏的 provider request id，便于与网关日志对照。
- 不自动切换 Chat Completions/Responses 协议。

回滚时移除 attempt-local reasoning buffer，并把 JSON reasoning-only retry budget 恢复为 1；不需要 transcript、
数据库或 API 协议迁移。

### 14.6 Agent Loop 与 Session

- Responses function call 驱动真实测试工具并完成下一轮。
- tool error 正确回传 `function_call_output`。
- transcript record/resume。
- interrupted tool call synthetic result 修复不回归。
- schema v1/v2 session replay。
- branch、rewind、compact、provider/model switch continuation 失效。
- tenant/user/session/branch continuation store key 隔离。
- `provider_continuation` 新 entry type 的旧 transcript 兼容、未知字段兼容和大小限制。
- 预留 `response_id` 字段缺失时正常读取；第一阶段不得写入非空 response ID。
- opaque state 不进入普通文本、日志和未脱敏 telemetry。

### 14.7 回归与全量验证

实现完成后至少执行：

```bash
gofmt -w <changed-go-files>
go test ./internal/config ./internal/anthropic ./internal/query ./internal/agentruntime ./internal/session -count=1
go test ./... -count=1
git diff --check
```

用户提供真实端点后，验收至少覆盖：

1. 普通文本流。
2. 一次 function call 和 function output 续轮。
3. 多个 function calls。
4. reasoning summary/usage（端点支持时）。
5. structured output。
6. 认证失败、无效模型、限流和取消。
7. 记录端点是否支持 `store` / `previous_response_id`，但第一阶段不启用或验收 stateful 调用。

## 15. 风险、降级与回滚

| 风险 | 后果 | 处理 |
| --- | --- | --- |
| 第三方只兼容部分事件 | 丢文本、工具参数或 usage | L0-L4 能力验收；未知事件可观测；关键事件缺失时报错 |
| 后续 response ID 跨 branch/provider 复用 | 上下文串线或数据泄漏 | 本期预留 identity/失效契约；stateful 上线前补真实测试 |
| opaque reasoning 未持久化 | reasoning model 工具续轮失败 | stateless continuation 测试和版本化 envelope |
| opaque payload 进入日志 | 敏感信息泄漏 | 日志只记 hash/size/presence |
| SDK严格类型不兼容非标准网关 | 无法解析流 | 用真实 fixture 验收；必要时只在 adapter 内增加受控兼容层 |
| retry 导致重复执行 | 重复计费或 hosted side effect | 仅无输出建流阶段重试；本期不开放 hosted tools |
| 大重构叠加新协议 | Chat/Anthropic 回归 | 同包小步抽取；旧路径回归测试；不搬迁公共包 |
| protocol 缺省却经过新 factory | `custom` 等老配置产生隐性行为差异 | 保留 legacy source/dispatcher；contract fixtures 作为发布门禁 |
| provider kind 与 protocol 冲突 | 请求发往错误路径或使用错误 schema | resolver 启动时校验；禁止 endpoint 猜测和运行时切协议 |
| 默认启用远端 state | 隐私、锁定、resume 不一致 | 默认 stateless/store=false |

降级策略：

- Responses 是显式配置，不影响未启用用户。
- 发生兼容问题时，用户可以切回原 `openai-chat-completions` protocol；不需要数据 migration。
- continuation schema 新字段全部向后兼容读取；不能解析时忽略远端 continuation，使用本地历史重放，
  但必须产生可观测 warning。
- 不通过静默字段丢弃维持“看似可用”。关键工具或状态字段不兼容时快速失败并给出可操作错误。

## 16. 文档与兼容性同步

实现完成时同步：

- `README.md`：上游 provider 协议与最小配置。
- `docs/examples/settings_quickstart.md`：primary/fallback Responses 示例和能力说明。
- `docs/examples/settings.example.json`：可复制配置。
- `docs/compatibility_matrix.md`：Responses L0-L4 实现状态和测试证据。
- `docs/architecture/go_claude_agent_capability_boundaries.md`：模型后端从两类更新为三类。
- `CHANGELOG.md`：用户可见配置和行为变化。

由于本期不新增或修改 golang-cc 对外 HTTP API，不需要生成 Swagger。未来若增加下游
`POST /v1/responses`，必须按项目 API 规则另行补 handler/service tests、Swagger、API 文档和 curl
全链路验证。

## 17. 参考资料

- [OpenAI：Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [OpenAI：Responses API reference](https://platform.openai.com/docs/api-reference/responses)
- [OpenAI 官方 Go SDK](https://github.com/openai/openai-go)
- [Azure OpenAI Responses API](https://learn.microsoft.com/en-us/azure/foundry/openai/how-to/responses)
- [Amazon Bedrock：Inference using Responses API](https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-mantle.html)
- [vLLM OpenAI-compatible server](https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [OpenRouter Responses API](https://openrouter.ai/docs/api_reference/responses/overview)
- [LiteLLM Responses API](https://docs.litellm.ai/docs/response_api)
- [Gemini Interactions API](https://ai.google.dev/gemini-api/docs/interactions-overview)

兼容能力具有版本和部署差异。实现与验收时必须重新核对目标服务的当前文档，并把实际测试版本、
模型、endpoint 和能力层级记录到 compatibility matrix，不能只依赖本文的时间点结论。

## 18. Review 决议与验收输入

### 18.1 已确认决议

1. 配置字段采用顶层 `providerProtocol`、fallback provider 内 `protocol`，不新增
   `openai-responses` 一类 provider type 来混合厂商/路由身份与 wire protocol。
2. 修改 provider client 接入点时，未配置 protocol 的老 provider 必须保持原行为，尤其覆盖
   `custom` / `openai-compatible` Chat Completions 路径；这是实现和发布硬门禁。
3. 采用 resolver、registry/factory、backend 和 capabilities 的分层扩展点；第一阶段保持内部接口，
   不为“未来可能”扩大公共 API，也不在本次大规模搬迁 `internal/anthropic`。
4. 第一阶段只交付 stateless。预留 `previous-response-id` 的枚举常量、capability、continuation
   schema 可选字段和配置校验入口，但不发送 `previous_response_id`；配置启用时明确报未实现。
5. 接受 `ProviderContinuationStore` 抽象，并在本地 transcript 增加版本化
   `provider_continuation` entry type；第一阶段用于持久化 stateless opaque continuation。
6. 接受第一阶段同时存在两个 OpenAI Go SDK。现有 Chat Completions 保留
   `sashabaranov/go-openai`，Responses 使用官方 `openai-go/v3`；本期不迁移老 SDK。
7. 真实验收端点由用户在开发完成后提供。它不阻塞本地 stub、fixture、contract tests 和代码开发，
   但在端点验收完成前不得宣称真实服务兼容性已验证。
8. 2026-07-31 已收到明确开发口令，按本文第一阶段边界完成实现与本地测试。

### 18.2 真实端点验收（2026-07-31）

验收目标：

- Base URL：`https://responses-gateway.example.com/v1`
- Model：`gpt-5.6sol`
- 认证：Bearer API key，仅通过环境变量注入；凭据不写入仓库、测试日志或本文。
- 模式：`openai-responses`、`stateMode=stateless`、`store=false`。

| 层级/能力 | 结果 | 真实证据与边界 |
| --- | --- | --- |
| L0 Transport | 通过 | `POST /v1/responses`、SSE 文本 delta、completed usage、401 JSON error、caller cancellation 均通过。 |
| L1 Core Items | 通过 | 普通文本、单 function call、`function_call_output` 续轮、同轮两个 function calls、usage 均通过。 |
| Structured output | 通过 | strict JSON Schema 返回可解析且满足必填字段。 |
| L2 Reasoning | 部分通过 | `effort=low` 被接受并返回非零 reasoning tokens；本次未观察到 reasoning summary delta 或 encrypted reasoning continuation。完整本地 transcript 重放下的 stateless 工具续轮成功。 |
| L3 State | 未验收 | 第一阶段禁止 `store=true` / `previous_response_id`，不因端点可能支持而绕过本地能力门禁。 |
| L4 Hosted Agent | 未验收 | hosted tools、background、Conversations 不在本期范围。 |

附加观察：

- 端点接受带随机无效后缀的 model 名称并返回结果，说明网关可能忽略、改写或按 alias 路由 model；
  不能依赖它做严格模型存在性校验。
- 未主动制造 429，因此真实限流阈值和 `Retry-After` 行为仍未认证；本地 contract test 已覆盖
  429/retry/fallback 逻辑。
- 首次真实工具流暴露出同一 `call_id` 被 `output_item` 与 arguments 事件重复物化的问题；adapter
  已按 `call_id` 规范化并补回归测试，避免 Agent 重复执行同一工具。
- 完整 Agent session `<session-id>` 暴露出 assistant 文本历史被错误编码为
  `input_text` 的问题：首轮请求正常，后续轮被目标网关包装为 502。adapter 已改为 typed output
  message + `output_text`，保留流式 message ID，并为旧 transcript 生成稳定 replay ID。修复后最小
  assistant-history live contract 和该 session 的完整只读 replay 均通过。
- 顶层 provider 和命名 fallback provider 均通过真实 `golang-cc -p` 验证；同时修复了 runtime
  `--settings` 没有整体重建 provider 字段、以及提前 provider 校验早于 settings 合并的问题。

### 18.3 Reasoning-only retry 增强验收（2026-08-25）

验收目标：

- Base URL：`https://ai-gateway.example.com/v1`
- Model：`gpt-5.6-sol`
- 认证：Bearer API key，仅从本机权限为 `0600` 的全局 settings 读取；凭据不写入仓库、命令输出或本文。
- 模式：`openai-responses`、`stateMode=stateless`、`store=false`。

真实端到端结果：

| 场景 | 结果 | 证据边界 |
| --- | --- | --- |
| 直连 Responses SSE | 通过 | HTTP 200，收到 `response.created`、`response.output_text.delta` 和 `response.completed`。 |
| golang-cc 显式 provider 单轮 | 通过 | 返回 `PONG`；trace 的 create/first_event/first_delta/read 均为 `gpt-5.6-sol`，无 fallback。 |
| golang-cc Read 工具多轮 | 通过 | 2 个 model turn、1 次真实 Read tool execution，最终返回 `github.com/konglong87/go-e2e`；两个 model turn 均为 `gpt-5.6-sol`，无 fallback。 |
| 真实网关 retry | 未自然触发 | 本次真实请求全部完整结束，`stream.retry` 为 0；不能据此声称真实网关已触发并恢复。六次 retry、耗尽 fallback、reasoning 去重、text/tool 零重放由确定性 malformed SSE fixtures 验证。 |

真实验收与确定性故障注入各自证明不同边界：前者证明当前 Jiuan endpoint、认证、模型、Responses SSE 和
tool continuation 可用；后者证明不可稳定要求真实网关复现的截断恢复逻辑。两类证据不可互相替代。

可重复的 live contract test 默认跳过，不影响离线 CI：

```bash
GOLANG_CC_RESPONSES_LIVE_ENDPOINT=https://responses-gateway.example.com/v1 \
GOLANG_CC_RESPONSES_LIVE_API_KEY='由安全环境注入!' \
GOLANG_CC_RESPONSES_LIVE_MODEL=gpt-5.6sol \
go test ./internal/anthropic -run TestOpenAIResponsesLiveContract -count=1 -v
```

测试入口：`internal/anthropic/backend_openai_responses_live_test.go`。该测试覆盖文本、reasoning/usage、
单工具及 stateless 续轮、并行工具续轮、连续 user 消息、assistant 文本历史、structured output、
无效模型观测、认证失败和取消。
