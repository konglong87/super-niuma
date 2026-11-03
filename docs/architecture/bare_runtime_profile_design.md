# golang-cc `--bare` Runtime Profile 技术方案

更新时间：2026-08-05

状态：V1 功能已实现并通过全量回归；进程级性能基准待发布验收

## 1. 背景与目标

golang-cc 在 V1 实现前不接受 `--bare`。仓库原有 `GOLANG_CC_SIMPLE` / `CLAUDE_CODE_SIMPLE` 的简化 system prompt 路径，但该路径仍会装配完整工具、hooks、自动发现的 MCP、memory、skills、plugins 和后台增强，因此不能直接充当 Claude Code 风格的 bare runtime。

本方案已经落地一个显式、会话级、不可变的 `bare` runtime profile，使 CLI/TUI 在保留权限、安全边界和显式输入的前提下，跳过未被用户明确请求的启动工作和上下文发现。行为目标是：

1. `golang-cc --bare` 和 `golang-cc -p "..." --bare` 均可运行。
2. 默认 system prompt 最小化，内置工具默认只暴露 `Read`、`Edit`、`Bash`。
3. 跳过 hooks、LSP、plugin/MCP/skill/agent 自动发现、workspace memory、Git context、启动更新、recap/next-steps 等自动增强。
4. 保留显式 `--system-prompt[-file]`、`--append-system-prompt[-file]`、`--add-dir`、`--mcp-config`、`--settings`、权限参数和 resume/session 参数。
5. 默认 runtime 行为零变化；bare 是 opt-in profile，不扩散到 server、Mobile、OpenAI API、Goal 或 scheduler 入口。

## 2. 上游事实基线

本方案参考本地 `claude_code_src_2026` 源码，而不是只参考帮助文本。当前上游实现的事实如下：

| 上游能力 | 代码事实 | 对本方案的约束 |
| --- | --- | --- |
| 入口 | `src/entrypoints/cli.tsx` 在加载主模块前识别 `--bare`；`src/main.tsx` 再设置 `CLAUDE_CODE_SIMPLE=1` | golang-cc 必须在任何启动更新、配置物化和 runtime 装配之前确定 profile |
| prompt | `src/constants/prompts.ts:getSystemPrompt` 在 SIMPLE 下只返回身份、CWD、日期 | bare 默认 prompt 使用独立最小路径，不继续拼普通 code prompt 的动态 sections |
| 工具 | `src/tools.ts:getTools` 在 SIMPLE 下只返回 Bash、Read、Edit | bare 内置工具基线固定为 `Read/Edit/Bash` |
| hooks/LSP | `src/utils/hooks.ts`、`src/utils/sessionStart.ts`、`src/services/lsp/manager.ts` 均直接短路 | 不能只是不注册 SessionStart；所有 hook 生命周期都必须不可执行，LSP 不进入 registry |
| memory/context | `src/context.ts` 跳过 cwd 自动发现，但显式 `--add-dir` 仍加载；`src/memdir/paths.ts` 关闭 auto memory | bare 不读取 cwd/父目录/user/managed memory，不注入 auto-memory protocol；显式目录例外 |
| skills/plugins/agents | skills 仅保留 bundled 与显式目录；plugin 自动加载跳过；custom agent 目录遍历跳过 | 自动 catalog、动态 pivot、plugin MCP 和 custom agent discovery 均关闭 |
| MCP | `src/main.tsx` 跳过 `.mcp.json`、settings、plugin 和 claude.ai MCP，只保留显式 `--mcp-config` | golang-cc bare 不能调用 `mergedMCPServers` 的 plugin/auto-discovery 路径 |
| 后台增强 | 跳过 quota/bootstrap prefetch、plugin sync、release notes、memory extraction、prompt suggestion 等 | golang-cc 对应关闭 startup update、recap、next-steps 和 TUI background watcher |
| auth | Anthropic bare 只读取 `ANTHROPIC_API_KEY` 或显式 settings 的 helper，不读取 OAuth/keychain；第三方 provider 使用自己的凭据 | golang-cc 没有 keychain/helper 等价实现，V1 不伪造这一能力，见兼容差异 |
| persistence | transcript 仍写入，仅把首条持久化从 await 改为 fire-and-forget | bare 不等于 `--no-session-persistence`，也不牺牲 Go recorder 的同步正确性 |

上游原则可概括为：**skip what I did not ask for, preserve explicit input and security policy**。

## 3. 范围与不做范围

### 3.1 本期范围

- CLI/TUI 主会话入口的 `--bare`。
- `--background` 作业对 bare profile 的持久化与子进程传播。
- 最小 prompt、最小内置工具、显式 MCP、显式目录上下文和 bundled/显式目录 slash skill。
- hooks、自动发现、后台增强和启动更新的统一关闭。
- telemetry、prompt dump 和上下文 manifest 能证明 bare 实际生效。
- 文档、help、completion/golden、单元/集成/性能验收。

### 3.2 本期不做

- 不把 server、OpenAI API、Mobile、Goal、scheduler 增加 bare 参数。
- 不新增 Claude Code 的 `--agents <json>`、`--plugin-dir`、apiKeyHelper 或 keychain。
- 不把现有 `GOLANG_CC_SIMPLE` / `CLAUDE_CODE_SIMPLE` 改成完整 bare；它们继续只代表历史 simple-prompt 兼容路径。
- 不关闭 transcript；需要无持久化时仍显式使用 `--no-session-persistence`。
- 不改变权限、sandbox、显式 deny、危险命令判定或 completion gate。
- 不为 bare 新增数据库表、HTTP API 或 Swagger 变更。

## 4. 核心架构

### 4.1 Profile 是唯一事实来源

新增 `internal/runtimeprofile`，使用常量而不是字符串散落：

```go
type Profile string

const (
    ProfileDefault Profile = "default"
    ProfileBare    Profile = "bare"
)

type ToolSet string

const (
    ToolSetFull    ToolSet = "full"
    ToolSetMinimal ToolSet = "minimal"
)

type Policy struct {
    Profile                  Profile
    ToolSet                  ToolSet
    DiscoverWorkspaceContext bool
    DiscoverSkills           bool
    DiscoverAgents           bool
    DiscoverPlugins          bool
    DiscoverMCP              bool
    RunHooks                 bool
    RunStartupUpdate         bool
    RunBackgroundEnrichment  bool
    AllowAutoMemoryRoot      bool
}
```

`runtimeprofile.Resolve(Profile)` 返回完整 policy。各模块只消费 policy，不各自解析 CLI flag 或环境变量。这样以后新增自动发现或后台任务时，可以在 profile policy 和测试矩阵中显式登记，不会形成约 30 个互不一致的 `if bare`。

`options`、`query.Options` 和 `background.Job` 只传 `RuntimeProfile`；不得把 `bare bool`、`simple bool`、环境变量和若干 disable flag 同时作为多套事实来源。

### 4.2 装配顺序

```text
parseArgs
  -> validateRuntimeProfileCombination
  -> resolve runtimeprofile.Policy
  -> skip/execute startup update
  -> resolve provider/settings
  -> apply explicit inputs
  -> sanitize auto-discovered contributors
  -> build minimal/full tool registry
  -> build query.Session with immutable profile
  -> assemble profile-scoped prompt/context
  -> run and persist normally
```

关键不变量：profile 必须在 `runStartupUpdateForCommand`、`EnsureProjectSettingsMaterialized`、plugin/MCP discovery 和 query construction 之前可见。

复审后补充两个装配约束：

- `query.New` 也是 settings materialization 的入口，不能只在 CLI 层跳过；它必须按 profile 决定是否 materialize，避免其他构造路径把副作用重新带回 bare。
- inherited settings 与显式 `--settings` 不做事后来源猜测。bare 先从已加载配置中剥离 hooks、MCP 和 additional directories，再应用显式 runtime inputs，最后再次清空 hooks；这样 provider、model、permission、sandbox 等基础配置保留，而 runtime contributor 的来源可证明。

### 4.3 不使用进程级环境变量传播

`--bare` 不设置 `GOLANG_CC_SIMPLE` 或 `CLAUDE_CODE_SIMPLE`。原因：

- server 或同进程测试可能创建多个不同 profile 的 session；进程级环境变量会串扰。
- 当前 SIMPLE 只简化 prompt，直接改变其含义会造成兼容回归。
- background 子进程已有结构化 Job，应该显式传播 profile，而不是依赖环境继承。

## 5. 行为契约

### 5.1 能力矩阵

| 能力 | default | bare | bare 的显式例外 |
| --- | --- | --- | --- |
| 默认 system prompt | 完整 code/chat prompt | `golang-cc` 身份 + CWD + 日期 | `--system-prompt[-file]` 替换；append 参数追加 |
| Git snapshot | 开启 | 关闭 | 无 |
| cwd/父目录 memory | 开启 | 关闭 | 无 |
| auto-memory protocol/写目录 | profile 决定 | 关闭 | 无；用户仍可用工具写自己明确指定的普通路径 |
| `--add-dir` | writable root | writable root + 显式 context root | 只扫描该目录本身的约定文件，不向父目录扩散 |
| skill catalog/dynamic pivot | 开启 | 关闭 | bundled 或 `--add-dir` 下的 `/skill-name` 可显式调用 |
| plugin discovery/MCP | 开启 | 关闭 | V1 无 `--plugin-dir` |
| settings/project MCP | 开启 | 关闭 | `--mcp-config`，以及显式 `--settings` 内的 MCP 配置 |
| hooks | 开启 | 全生命周期关闭 | 显式 settings 也不能重新开启；bare 不是 hook 执行模式 |
| 内置工具 | 完整 registry | `Read`, `Edit`, `Bash` | `--tools` 只能取交集，不能扩大 baseline |
| MCP tools | 自动 + 显式 | 仅显式 | 仍受 `--tools` 和权限策略过滤 |
| LSP/Task/Agent/Web/Skill tool | 可用 | 不装配 | 无；slash skill 在 query 前解析，不依赖 Skill tool |
| startup update | 按设置执行 | 关闭 | 无 |
| recap/next-steps/background watcher | 按设置执行 | 关闭 | 手动 `/recap` 不在 V1 bare slash allowlist |
| transcript/resume | 开启 | 开启 | `--no-session-persistence` 显式关闭 |
| permission/sandbox/deny | 开启 | 原样保留 | 现有显式 CLI override 继续生效 |
| auto compact/tool-result externalization | 开启 | 保留 | 它们保护长会话正确性，不属于启动自动发现 |

### 5.2 最小工具的组合规则

定义常量：

```go
var BareBuiltinTools = []string{"Read", "Edit", "Bash"}
```

规则：

1. 未传 `--tools`：暴露三个 bare 内置工具，加显式 MCP tools。
2. 传 `--tools Read,Bash`：内置工具为 `Read/Bash`；显式 MCP 也按当前 filter 规则过滤。
3. 传 `--tools Task,Read`：只得到 `Read`，不得注册 Task 的派生工具或 agent task runtime。
4. `--allowedTools` / `--disallowedTools` 是权限层，不负责扩大工具暴露面。
5. 所有工具仍经过 `permissions.GuardAll`，bare 绝不隐含 bypass。

工具 registry 必须先选 profile baseline，再做 CLI filter，最后套 permission guard。不能先构建完整 registry 再依赖 deny 隐藏，因为完整构建本身会触发不必要的依赖和副作用。

### 5.3 Context 与显式目录

为 `memory.LoadCodeWithOptions` 增加 discovery mode 和 explicit roots，而不是在 query 中拼路径：

```go
type DiscoveryMode string

const (
    DiscoveryAuto     DiscoveryMode = "auto"
    DiscoveryExplicit DiscoveryMode = "explicit"
)

type LoadCodeOptions struct {
    DiscoveryMode DiscoveryMode
    ExplicitRoots []string
    // existing fields...
}
```

bare 使用 `DiscoveryExplicit`：

- `ExplicitRoots` 为空时返回空文档，不读取 `/etc`、home、cwd、父目录或 project memory。
- 每个 `--add-dir` 只读取该 root 下受支持的 `go-e2e.md`、`CLAUDE.md`、`.claude/CLAUDE.md`、`.claude/rules/*.md` 和必要 include。
- 不把 `AGENTS.md` 当作 implicit fallback；bare 的显式目录语义应窄且可预测。
- 不注入 `compatibleMemorySystemBlock`，不把 project memory dir 自动加入 writable roots。

skills/slashcommands 使用同类 scoped discovery options：bare 自动 catalog 始终为空；显式 `/name` 只在 bundled roots 和 `--add-dir/.claude/{skills,commands}` 中解析。不得调用 `plugins.List`、marketplace、MCP cache、user home 或 cwd project roots。

### 5.4 Settings 与 provider

V1 采用“保留 provider-neutral 配置，剥离自动 runtime contributors”的策略：

1. 现有 settings 仍可提供 model/provider/base URL、permissions、sandbox、fallback 和成本配置，避免 bare 只能服务 Anthropic。
2. 在应用显式 CLI inputs 前，清除自动发现的 hooks、MCP servers 和 additional directories。
3. 再合并显式 `--settings`、`--mcp-config`、`--add-dir`；最后强制 hooks 为空，并关闭 recap/next-steps 等 enrichment。
4. bare 跳过 `EnsureProjectSettingsMaterialized`，不得因一次最小运行写出迁移后的 project settings。
5. provider credential 使用 golang-cc 现有 env/settings 解析。golang-cc 当前没有 keychain，因此“不读 keychain”天然成立；V1 不额外禁止 `ANTHROPIC_AUTH_TOKEN`，以免破坏现有 provider-neutral 鉴权。

这与上游的 Anthropic API-key-only auth 存在已知差异。若未来需要 hermetic CI auth，应新增独立 `--auth-source env-only` 或 provider-neutral credential policy，而不是把 Anthropic 特例硬编码进 bare。

### 5.5 Session 与后台传播

- recorder 继续同步 append/close，保证 crash consistency；不复制上游 fire-and-forget transcript 优化。
- `--resume`、`--continue`、`--session-id` 和 `--no-session-persistence` 原语义不变。
- resume 是显式输入，因此历史消息可进入上下文；但当前轮不重新自动发现 memory/skills/MCP。
- `background.Options` / `background.Job` 新增可选 `runtime_profile` 字段。旧 Job 缺字段时解析为 `default`，保持向后兼容。
- `startBackgroundJob` 和 `backgroundRunCommand` 必须 round-trip profile；需要测试落盘 JSON 和 child options readback。

### 5.6 参数组合

| 组合 | 结果 |
| --- | --- |
| `--bare -p` / bare TUI | 支持 |
| `--bare --background` | 支持，profile 写入 Job 并传播 |
| `--bare --resume/--continue` | 支持 |
| `--bare --no-session-persistence` | 支持 |
| `--bare --prompt-mode code` | 支持 |
| `--bare --prompt-mode chat` | 拒绝；bare 的本地文件工具/CWD 语义与 tenant chat 隔离冲突 |
| `--bare --agent <builtin>` | 支持显式 built-in main-thread agent |
| `--bare --agent <custom>` | 拒绝并说明 custom agent discovery 在 bare 中关闭；V1 尚无 `--agents` inline escape hatch |
| `--bare <top-level-subcommand>` | 非 query 子命令拒绝，避免静默 no-op；hidden background child 例外 |
| `--bare --settings hooks=...` | settings 可解析，但 hooks 不执行 |
| `--bare --tools default` | `default` 表示 bare profile 的默认三工具，不恢复 full registry |

组合校验集中在 `validateRuntimeProfileCombination`，错误应在任何更新、配置物化或 provider 连接之前返回。

全局参数沿用现有 parser 契约，只在首个顶层子命令之前解析。因此 `golang-cc --bare review` 会被组合校验拒绝，`golang-cc review --bare` 则由 `review` 子命令按自己的参数规则报错；V1 不改变所有子命令的参数归属。`golang-cc --bare --help` 和 `--version` 仍可用。

## 6. 分层改动设计

### 6.1 Model / Constant

- 新增 `internal/runtimeprofile` 的 `Profile`、`ToolSet`、`Policy` 常量和解析方法。
- `internal/cli.options`、`query.Options` 增加 `RuntimeProfile runtimeprofile.Profile`。
- `background.Options` / `background.Job` 增加向后兼容的 `RuntimeProfile string` JSON 字段。
- `BareBuiltinTools` 使用集中常量，不在测试和实现重复写字面量。

### 6.2 CLI / Runtime Wiring

- `parseArgs` 识别 `--bare`；help、golden、shell completion 同步。
- `Run` 在 startup update 前解析 policy；bare 主会话直接跳过 update。
- `newQuerySession` 按 policy：
  - 不 materialize project settings；
  - 不加载 custom agents/plugins/auto MCP；
  - 使用空 hook runner；
  - 使用 minimal core tools；
  - 只加载显式 MCP；
  - 不初始化本地 agent task runtime；
  - 把 profile、explicit context roots 传给 query。
- TUI bare 关闭 background watcher、recap/next-steps runners，并使用 scoped slash provider。

### 6.3 Query / Prompt

- `query.Options` 中 profile 是显式字段；`defaultSystemPromptParts` 优先检查 profile，不读进程级 env 来判断 bare。
- bare 默认 system prompt 为 golang-cc 身份、CWD、日期；显式 SystemPrompt 和 SystemAddendum 继续走现有优先级。
- `assembleContextMessages` 使用 explicit discovery；无显式 roots 时零 memory I/O。
- 跳过初始 skill catalog、动态 skill pivot、auto-memory block、Git context 和 memory writable root。
- prompt dump 和 context manifest 增加独立的 `runtime_profile=bare`；既有 `prompt_mode` 继续记录 `code`，避免把运行时 profile 与 code/chat prompt mode 混成一个枚举。telemetry properties 同样增加低基数 `runtime_profile`，不新增稳定 API/SSE 字段。

### 6.4 Skills / Slash Commands / Agents / Plugins

- skills 增加带 discovery options 的 `List/Load` 内部入口，现有公开函数保持 auto 默认，避免全局回归。
- slashcommands 增加 `ResolveOptions` / `ListOptions`，bare 只传 bundled + explicit roots。
- agents 复用现有 `agents.BuiltIn` 做 built-in-only lookup，禁止 bare 误触 home/project/plugin 遍历。
- `mergedMCPServers` 接收 discovery policy；bare 不调用 `plugins.MCPServers`。
- 不需要修改 plugin 安装/同步逻辑，因为 bare query path 根本不进入这些入口。

### 6.5 Persistence / Observability

- background Job 是唯一持久化 schema 变化；`runtime_profile` 缺失时默认 `default`。
- Job 同时补齐 bare 正确执行所需的既有 runtime inputs：`prompt_mode`、`agent`、`tools_specified`、`enabled_tools`、`settings_inputs`、`mcp_config_inputs` 和 `strict_mcp_config`。否则父进程虽然记录了 bare，子进程仍会丢失显式例外或扩大工具面。字段均为 optional additive，registry 文件继续使用 0600 权限。
- transcript schema 不变，不向历史消息注入 profile 控制字段。
- `query.run.started/finished` 增加低基数 `runtime_profile` property。
- context manifest 在 bare 下必须显示 code memory、Git context、skills catalog 均未激活；显式 add-dir/MCP 要有可区分来源。
- 日志不得记录 prompt、凭据或 settings 内容。

## 7. 全局拓扑影响

### 7.1 受影响节点

| 节点 | 影响 |
| --- | --- |
| `RT-ENTRY` | 新 CLI flag、组合校验、startup update skip、background 传播 |
| `RT-WIRING` | 新 profile policy；settings、hooks、plugins、skills、MCP 装配收缩 |
| `RT-PROMPT` | 最小 prompt、explicit-only context、无 Git/memory/skill catalog |
| `RT-TOOLS` | minimal registry 与显式 MCP |
| `RT-CACHE` | recap/next-steps 关闭；auto compact 与 tool-result externalization 保留 |
| `RT-PERSIST` | background Job 增加可选 profile 字段；transcript 不变 |
| `RT-OUTPUT` | TUI runners/background watcher/slash provider 收缩，输出协议不变 |
| `RT-OBSERVE` | runtime profile、prompt bytes、tool count、startup latency 观测 |

### 7.2 爆炸半径

主要行为是 opt-in CLI 模式，属于 `B2_MODE`；但 background Job JSON 增加持久化字段，最高按 `B4_PROTOCOL` 管理。该字段是 optional additive change：旧记录读为 default，新版本写出的 bare 记录旧版本会忽略未知字段。

没有新增 gate。CLI 参数冲突检查只防止不可能或不安全的组合，不增加模型 turn、token 或 tool call。

实现时新增 `internal/runtimeprofile/`，必须同步：

- `docs/architecture/runtime_topology.yaml`：登记到 `RT-WIRING`，并增加测试命令。
- `docs/architecture/global_runtime_topology.md`：记录 profile 对 ENTRY/WIRING/PROMPT/TOOLS 的 routing。
- `diagrams/global-runtime-topology.mmd` 及其 SVG/PNG/Excalidraw 渲染物：仅在新增 profile routing 边无法由现有节点准确表达时更新。

## 8. 渐进式实施计划（已完成）

### Phase 1：Profile 与入口闭环（已完成）

产出：profile 常量/policy、CLI parse/help/validation、startup update skip、background Job 及全部 query-affecting runtime inputs round-trip。

验收：`--bare --help` 可见；无效组合在副作用前失败；旧 background JSON 可读；bare child readback 为 bare。

### Phase 2：Wiring 与最小工具（已完成）

产出：minimal tool builder、hooks/agent task/plugin/auto MCP 关闭、权限和显式 MCP 保留。

验收：registry 精确为 `Read/Edit/Bash`；explicit MCP 可见；plugin/settings auto MCP 不可见；deny 和 sandbox 负向路径通过。

### Phase 3：Prompt 与显式 Context（已完成）

产出：bare query profile、最小 prompt、explicit memory loader、scoped skills/slashcommands、无 auto-memory writable root。

验收：provider request 中没有 workspace memory、Git、skill catalog、MCP instructions 或普通 code sections；`--add-dir` 和显式 slash skill 生效且不向父目录扩散。

### Phase 4：TUI 与后台增强收缩（已完成）

产出：bare TUI provider/runners、recap/next-steps/background watcher 关闭、resume 回归。

验收：TUI 不启动相关 goroutine/provider call；transcript 和 resume 正常；默认 TUI 行为不变。

### Phase 5：观测、文档与全量回归（功能已完成，性能基准待发布验收）

产出：telemetry/prompt dump 标识、CLI 文档、compatibility matrix、拓扑 registry/文档/图件同步。

验收：性能和行为矩阵通过，runtime topology check、全量测试和 diff check 通过。

## 9. 测试与验收矩阵

### 9.1 单元与集成测试

| 层 | 必测项 |
| --- | --- |
| parse/help | `--bare` 解析、重复参数幂等、help/golden/completion、无效组合 |
| profile | default/bare policy 全字段表驱动测试，未知 profile fail closed |
| config | bare 不 materialize project settings；自动 hooks/MCP/add-dir 被剥离；显式 settings/MCP/add-dir 保留 |
| tools | 精确三工具；`--tools` 交集；explicit MCP；plugin MCP 缺失；Task 派生工具不注册 |
| permissions | explicit deny 仍阻断 Bash/Edit；bare 不产生 bypass；sandbox 行为与 default 一致 |
| hooks | SessionStart/UserPromptSubmit/PreTool/PostTool/Compact/Stop 的 sentinel 均不执行 |
| prompt | 最小 default；custom override/append；无 Git/memory/skills/auto-memory；manifest 与 dump 一致 |
| context | 无 add-dir 时零 workspace docs；一个/多个 add-dir；include/rules；不读取父目录和 home |
| slash skill | bundled/explicit root 可调用；project/user/plugin/marketplace 自动 skill 不可见 |
| session | transcript 默认落盘、`--no-session-persistence`、resume/continue、existing transcript context |
| background | Job JSON round-trip、旧 JSON default、child profile readback、bare 执行结果 |
| TUI | runners/watcher 为 nil；默认 TUI 仍启用；动态 slash provider scoped |

### 9.2 真实请求验收

使用本地 stub provider 捕获首轮 request，分别运行：

```bash
golang-cc -p "read README" --bare
golang-cc -p "read README" --bare --tools Read
golang-cc -p "use explicit mcp" --bare --mcp-config ./testdata/mcp.json
golang-cc -p "/bundled-skill arg" --bare
golang-cc -p "follow explicit context" --bare --add-dir ./testdata/context-root
```

断言：

- 默认 tools 精确为 `Bash/Edit/Read`（排序后）。
- system 只含 bare default 或显式 prompt/addendum。
- message 中没有 cwd/parent/home guidance；explicit add-dir 内容只出现一次。
- hook sentinel 文件不存在，未启动未请求的 MCP/plugin 子进程。
- JSON/stream-json envelope 与 default 兼容。

### 9.3 性能验收

在相同二进制、cwd、provider stub、冷/暖文件缓存下分别跑 default 与 bare 至少 30 次，记录：

- process start 到首个 provider request 的 p50/p95。
- 首轮 system bytes、input tokens、tool definitions 数量和 tool schema bytes。
- 启动期文件读取/子进程/MCP connect 次数。
- 总 turn、tool call、首次响应延迟。

准入标准：

- bare 不得比 default 增加启动 p50/p95。
- bare 首轮 tool count 必须为 3（无 explicit MCP 时）。
- bare system bytes 必须显著低于 default，并且没有被 addendum 之外的自动 section 回填。
- 默认模式的 prompt bytes、tool count、行为和性能不得发生不可解释变化。

## 10. 风险、降级与回滚

| 风险 | 防护/恢复 |
| --- | --- |
| 把 simple env 误当 bare，影响 server/测试 | profile 显式传递；不设置进程 env；simple env 保持旧语义 |
| 只关 prompt，实际仍扫描/连接 | registry/context/skills/MCP 各有负向 sentinel 测试和 request capture |
| bare 绕过权限 | profile policy 不包含 permission override；registry 仍统一 GuardAll；补 deny/sandbox 测试 |
| 显式 `--add-dir` 被错误忽略或扩大扫描 | exact-root loader；不向父目录扩散；manifest readback |
| background 丢失 profile | Job additive field + round-trip + child integration test |
| custom agent/skill 行为含糊 | unsupported 组合明确报错；文档列 V1 边界 |
| 关闭 transcript 导致不可恢复 | transcript 默认保留；不复制异步写优化 |
| 新 profile 分支长期漂移 | 单一 Policy、能力矩阵测试、拓扑登记、default/bare 双矩阵 |

回滚路径：bare 是纯 opt-in。若上线后发现问题，可先从 help 隐藏并让 parser 返回明确的 temporary unavailable 错误；background 旧/新记录仍因 optional 字段可读。不要让 bare 静默退化为 default，否则用户会在不知情下执行 hooks、MCP 或自动上下文发现。

## 11. 完成定义

实现只有同时满足以下条件才算完成：

1. 行为矩阵和负向 sentinel 测试全部通过。
2. `go test ./... -count=1`、`git diff --check` 通过。
3. `go run ./scripts/runtime-topology-check --base HEAD --working-tree --impact updated --blast-radius B4_PROTOCOL --reason "add opt-in bare runtime profile and backward-compatible background job field"` 通过。
4. prompt request capture 证明没有隐式 memory/skills/plugin/MCP/hooks/LSP。
5. default 与 bare 的 request bytes/tool count 行为对照进入自动化测试；发布前再完成 30 次冷/暖进程级性能报告，不只在聊天中声明。
6. `docs/compatibility_matrix.md` 诚实记录 V1 与上游的差异：无 `--agents` inline、无 `--plugin-dir` escape hatch、provider-neutral auth、不采用异步 transcript 首写。

## 12. 设计复审记录

复审结论：无阻断性架构问题，修正以下实现级缺口后可进入开发。

1. 后台传播不能只增加 profile 字段，必须传播决定 bare 有效工具和显式例外的 runtime inputs，否则 child 行为与父进程声明不一致。
2. settings 来源隔离必须由装配顺序保证，不能在最终合并对象上推断来源；hooks 在最终阶段再次清空，显式 settings 也不能重新开启。
3. `query.New` 的 settings materialization 是独立副作用入口，必须由同一个 profile policy 控制。
4. `--add-dir` 的 include 解析必须以显式 root 为边界：允许 root 内相对 include，拒绝 `..`、绝对路径或 `~/` 逃逸到 root 外。
5. bare TUI builtin slash allowlist 固定为 `help`、`clear`、`status`、`tools`、`sessions`、`resume`、`model`、`permissions`、`usage`、`compact`、`rewind`、`checkpoint`、`branches`、`redo`、`ps`、`logs`、`attach`、`exit`、`quit`；其余 builtin 不展示且直接调用时明确拒绝。bundled 与显式目录 skill 仍可按名称调用。
6. `prompt_mode` 和 `runtime_profile` 是正交维度，观测字段必须分开，避免后续 code/chat/bare 组合无法分析。

## 13. 实现与验证结果

V1 已按本方案落地。拓扑影响为 `B4_PROTOCOL`：后台 Job 增加 optional additive 字段，旧记录缺字段时仍按 default profile 运行。主要收益是 bare 首轮只装配 `Read`、`Edit`、`Bash`，并在 provider request capture 中证明 system bytes 和 tool definitions 均小于 default；默认 profile 的全量测试保持通过。

已执行：

```bash
go test ./... -count=1
git diff --check
go run ./scripts/runtime-topology-check --base HEAD --working-tree \
  --impact updated --blast-radius B4_PROTOCOL \
  --reason "add opt-in bare runtime profile and backward-compatible background job fields"
```

专项回归覆盖 CLI 组合、settings 来源隔离、最小工具交集、真实 provider request capture、hook sentinel、settings 非物化、显式目录与 symlink 越界、skills/slash scoped discovery、bare prompt/context、TUI enrichment 关闭和 background JSON round-trip。

潜在负作用与恢复路径：新增 profile 分支可能在未来 contributor 扩展时遗漏 policy 登记；因此 `internal/runtimeprofile.Policy` 与负向 request capture 测试作为统一守卫。发现线上问题时可隐藏 flag 并明确报 temporary unavailable；不得静默退回 default。30 次冷/暖进程级 p50/p95 基准仍属于发布性能验收项，本次本地验证只覆盖确定性的 request bytes/tool count 对比，不宣称真实 provider 延迟收益。
