# Claude Code 兼容性深度核查

核查日期：2026-06-10

本次核查范围包括 Claude Code 核心行为面：plugins/skills、工具执行、流式对话、权限、沙箱、会话，以及当前 Go TUI 的权限审批和流式渲染主链路。

结论：go-claude 借鉴了 Claude Code 的核心行为面，但不能诚实标记为“100% 一比一对齐 Claude Code”。它已经具备可测试的核心闭环，并补齐了自动 checkpoint、TUI/headless 权限审批与持久授权入口、MCP callback TUI/headless 审批、permission classifier、forked skill 执行、skill runtime frontmatter 主链路、hook lifecycle/matcher/schema、AskUserQuestion/TaskOutput、LSP/WebBrowser/Workflow/Worktree/PowerShell 工具、stream-json thinking/partial/input_json_delta/hook/nested token progress、错误 envelope 与 Claude-style envelope 字段，以及 macOS Bash/Seatbelt 与 Linux Bash/bubblewrap/seccomp OS sandbox 主链路；但仍有若干原版行为没有实现，尤其是 Windows/WSL/PowerShell 真实 OS sandbox、部分云端/市场生态能力、上游 bundled catalog 精确源数据和未知 upstream-exact 长尾字段。

## 核查依据

- 本项目 Go 实现：`internal/skills`、`internal/plugins`、`internal/query`、`internal/tools`、`internal/permissions`、`internal/sandbox`、`internal/session`、`internal/server`、`internal/cli`。
- 参考源码：`$HOME/GolandProjects/claude_code_src_2026/src/skills/loadSkillsDir.ts`、`src/tools/SkillTool/SkillTool.ts`、`src/tools.ts`、`src/Tool.ts`、`src/types/permissions.ts`、`src/utils/permissions/permissionSetup.ts`、`src/utils/conversationRecovery.ts`、`src/commands/rewind/index.ts`、`src/main.tsx`。
- 官方文档核查点：Claude Code skills、plugins、permissions、hooks、CLI、checkpointing、streaming output。
- 自动化验证：`go test ./internal/permissions ./internal/query ./internal/config ./internal/hooks ./internal/skills ./internal/plugins ./internal/tools/webbrowser ./internal/cli -count=1` 已通过；最终提交前必须再跑 `go test ./... -count=1`。

## Plugins / Skills

已实现：

- 用户、项目、插件、bundled、marketplace 和 MCP 来源的 skill 发现。
- `~/.claude/skills`、项目 `.claude/skills`、legacy `.claude/commands/*.md`、插件 `skills`、插件 `commands/*.md`。
- 插件 manifest 支持 `.claude-plugin/plugin.json`、`.codex-plugin/plugin.json`、`plugin.json`。
- plugin skill 使用 `<plugin>:<skill>` 命名，避免插件与项目 skill 冲突。
- bundled/marketplace/MCP skill roots 可从默认目录和环境变量发现，`skills install --source <index> <name>` 可按 name 安装/更新单个 marketplace skill，`skills sync --source <index.yaml|json|url>` 可把 marketplace 索引同步成本地 `SKILL.md`；`skills marketplace-search --source <index> --query <q>` 可搜索远程/本地 marketplace index；`skills marketplace-status --source <index>` 可对比本地安装和索引的 current/outdated/missing 状态；`skills package <dir>` 可把含 `SKILL.md` 的 skill 目录打包为 zip。
- `skills validate-bundled --source <catalog.yaml>` 可对 bundled skills 做名称、metadata 字段、allowed-tools 和 `content_sha256` 校验，用于接入上游 bundled catalog 金标。
- 支持按 name/local name/description/when_to_use/source/plugin 做 metadata 搜索。
- 插件贡献的 skills、agents 和 MCP server 配置已有校验底座。
- query 启动只注入 metadata catalog，不预读完整 `SKILL.md`。
- `Skill` tool 按需加载完整 `SKILL.md`。
- YAML frontmatter 解析，包括 `name`、`description`、`when_to_use`、`allowed-tools`、`argument-hint`、`arguments`、`version`、`model`、`disable-model-invocation`、`user-invocable`、`context`、`agent`、`effort`、`hooks`、`paths`。
- `disable-model-invocation: true` 的 skill 不注入模型 catalog。
- `paths` 参与 catalog 过滤。
- `allowed-tools` 会在 skill invocation 后约束后续工具调用。
- `model` 会覆盖 active skill 后续 query turn 的模型。
- `context: fork` 会通过 `Skill` tool 在隔离 forked skill context 中执行。

仍有差距：

- upstream bundled catalog 的校验机制已具备，但仍需要持续维护来自上游的 expected catalog 数据；远程 marketplace 已支持 search/install/sync、版本字段、`content_sha256` 内容校验和 Ed25519 签名校验，仍可继续增强 richer registry UX。
- forked skill 已有闭环，但未完整等同原版 UI 进度、可视化和全部工具隔离策略。
- skill-scoped `hooks`、`agent`、`effort` 已接入 active skill runtime：hook 会叠加到后续工具调用，支持 `matcher` / `tool` / `tools` 过滤，`agent` 会作为 Task 默认 subagent，`effort` 被保留在运行时元数据中供后续 provider 映射。
- Skill catalog 每次 `List` / `Load` / query 注入都会重新扫描 user/project/plugin/bundled/marketplace/MCP/env roots，可发现运行期新增 skill；长驻进程可通过 `internal/skills.Watch` / `skills watch` 监听本地 skill 和 legacy command 变更；`skills context` 可预览 prompt 对应的 metadata catalog；`skills feedback` 可记录本地 skill improvement survey JSONL。仍未接入远端 survey 后台。

判定：渐进式加载主链路已对齐，完整 skill 生态未 100% 对齐。

## Tool Execution

已实现：

- Anthropic messages loop：assistant tool_use -> 本地 tool 执行 -> user tool_result -> 下一轮。
- 工具注册表、工具 schema、unknown tool 错误、tool result 截断。
- SessionStart、Notification、PreToolUse、PostToolUse、PostToolUseFailure、PreCompact、PostCompact、Stop、SubagentStop、UserPromptSubmit hook lifecycle。
- hook JSON stdin/env payload、matcher/tool-specific filtering、schema validation、`permissionDecision`、`permissionDecisionReason`、`updatedInput` 输出；`UserPromptSubmit` 支持 prompt 重写/deny，`Stop` 和 `SubagentStop` 用于生命周期收尾通知，`Notification` 当前用于自动压缩成功/失败通知。
- 权限审计和 file_change snapshot 回调。
- Read、Write、Edit、MultiEdit、NotebookRead、NotebookEdit、LS、Glob、Grep、TodoRead、TodoWrite、Bash、PowerShell、WebFetch、WebSearch、Task、TaskOutput、AskUserQuestion、PlanMode、Skill、LSP、WebBrowser、Workflow、Worktree、MCP resources、MCP tools。
- Read 对超过 100MB 的文件生成 chunk manifest，包含 byte range 与 `line_start` / `line_end` 行范围提示，支持 `chunk_index`、byte range、line offset/limit 逐段读取；二进制/媒体文件会返回 MIME、sha 前缀和 chunk 摘要，不内联原始 payload；Grep 已改为 streaming；Edit/MultiEdit 对大文件使用临时文件流式替换，并通过 snapshot path 接入 checkpoint rewind。
- Task 已升级为 sub-agent runtime：独立 messages 上下文、独立 transcript、agent `model` override、agent `tools` 过滤、sub-agent tool loop、hooks、guarded tool execution、task/event store 接口、MySQL task/event 表、进程内 cancel controller，以及 tenant server list/events/cancel API。
- MCP HTTP / streamable-http 基础能力。
- query/tool 统一结构化日志。

仍有差距：

- 原版工具集合更大，Go 版已补 LSP、WebBrowser、Workflow、Worktree、PowerShell 主链路；WebBrowser 已支持 open/navigate/text/links/forms/click/input/submit/screenshot 的 headless-like fallback，并提供 `scripts/playwright-browser-runner.mjs` 真实 Chromium/浏览器 runner。`GOLANG_CLAUDE_CODE_WEBBROWSER_MODE=playwright` + `GOLANG_CLAUDE_CODE_PLAYWRIGHT_RUNNER` 会启用该链路，真实浏览器 golden 覆盖 JS 渲染快照、input/submit、PNG screenshot，以及 console/network 观测字段。
- Task/sub-agent 主链路已具备隔离执行、工具循环、事件落库底座、进程内取消控制器、server/CLI/slash 外部取消入口、持久化 cancel 轮询、stream-json nested progress/token 回传和 TUI nested sub-agent progress 面板；`Task` tool 支持 `tasks[]`、`max_concurrency`、priority scheduling、timeout、retry/backoff 和 batch summary。仍缺原版级并发调度可视化细节。
- 大文件主链路已具备 100MB chunk manifest、line_start/line_end 行范围、二进制/媒体摘要、streaming grep/edit 和 snapshot-backed rewind；仍缺上游未知媒体专用 affordance。
- hook matcher、tool-specific hook filtering、permission update persistence 和长尾生命周期事件已接入主链路；仍缺上游私有 StructuredIO 控制协议 exact 细节和 UI 可视化。
- 工具执行已有日志、hook、nested progress 和 golden 覆盖；仍缺工具调用中的细粒度 progress UI 和原版级中断恢复 UX。

判定：核心工具 loop 可用且可测，WebBrowser 已有真实浏览器 e2e 金标；工具生态和 hook 私有控制协议仍未 100% 对齐。

## Streaming Chat

已实现：

- Anthropic SSE text/tool_use 解析。
- `-p --output-format stream-json` 输出 text delta、tool_call、tool_result、done。
- OpenAI-compatible `/v1/chat/completions` 普通响应和 SSE。
- OpenAI response-side `tool_calls`、SSE `delta.tool_calls`、`finish_reason=tool_calls`。
- go-openai 客户端兼容测试已覆盖服务端普通和流式调用。

仍有差距：

- `stream-json` 已输出 turn/message start/stop、Claude-style `message` / `content_block` / `delta` envelope、content block start/stop、text delta、thinking delta、`signature_delta`、`citations_delta`、connector text delta、分片 `input_json_delta`、usage delta、message_delta、error envelope、upstream-style `result` envelope、tool、skill activation 和 Task nested started/turn/text_delta/tool/completed/failed/cancelled progress 事件；`--include-stream-events` 可额外输出 upstream-compatible `{type:"stream_event", event:{...}, session_id, parent_tool_use_id:null, ttftMs}` 外层，`--include-partial-messages` 输出 accumulated partial message，`--include-hook-events` 输出带 `permissionDecision`、`permissionDecisionReason` 和 `updatedInput` 的 hook_start/hook_result。正常、失败/取消 nested agent、signature/citations/connector delta 都有 golden。
- 仍缺 ant 内部 `research` 字段透传和更细的 TUI 侧 nested agent progress UI 事件顺序金标。
- TUI 已接入流式文本渲染、权限请求事件通道和 nested sub-agent progress 面板。

判定：服务端、headless 和 TUI 流式主链路可用，upstream-compatible stream_event 主链路已接入；公开 stream-json delta 主链路已覆盖，内部 ant-only 字段仍需持续追踪上游。

## Permissions

已实现：

- allow/deny/defaultMode 策略。
- deny 优先于 allow。
- `--allowedTools`、`--disallowedTools`、`--permission-mode`、`--dangerously-skip-permissions`。
- `Tool:qualifier` 级规则匹配。
- mutating tool 在 ask 模式下非交互阻断。
- TUI ask-mode 可弹出权限审批请求，并支持一次允许、session/project/global 持久允许和拒绝。
- `--permission-prompt-tool` 可让 headless 模式调用指定 tool 审批 ask 模式下的 mutating tool。
- Claude Code 原生 `permissions.ask`、legacy `permissions.alwaysAsk`、`permissions.source`、`permissions.preference` 和逐规则 `RuleSources` 已接入策略和审计；ask/alwaysAsk 优先于 allow。
- 敏感路径 classifier 会对 `.ssh`、`.env`、token/credential、`.git/config`、`.claude/settings` 等请求触发审批。
- 危险 shell classifier 会对 destructive filesystem、git reset/clean/force push、sudo/su、mount/mkfs/dd/raw device、systemctl/launchctl/service、curl/wget pipe-to-shell、shell/interpreter eval、npx/bunx/tsx、iptables/pfctl/nft、nc/socat/ssh tunnel、credential exfil 等高风险 Bash 命令触发审批；legacy wildcard allow 规则命中高风险请求时会降级到审批，原版整工具 allow 如 `Bash` 直接作为显式授权生效。
- 其中一部分规则同时是 Bash 工具的**硬拒绝**：`rm -r -f` 指向 `/`、`*`、`~`、`$HOME`、`.` 或系统关键目录（`/usr`、`/etc`、`/System`、`/Users`、`/home` 等），`git reset --hard`、`git clean -f/-d`、`mkfs`、raw device 写入、`chmod -R 777`、broad `chown -R`、git hook 路径重定向。硬拒绝在 Bash 工具内部返回，不经权限策略，因此**连 `--dangerously-skip-permissions` 也推不翻**（显式 `permissions.deny` 规则同样推不翻，它在 bypass 短路之前求值）。系统关键目录只匹配目录本身，不匹配子孙：`rm -rf /Users` 拦，`rm -rf /Users/me/app/build` 不拦，后者归可写根检查管。
- git hook 路径重定向（`core.hooksPath`、`core.fsmonitor`）之所以也是硬拒绝：默认写保护把 `.git/hooks` 和 `.git/config` 列为不可写，但这两个配置项能在**不提到任何文件路径**的情况下达到同样效果，路径级 deny 看不到它们。两种写法都拦 —— `git config core.hooksPath <dir>`（持久，写 `.git/config`）和 `git -c core.hooksPath=<dir> <cmd>`（仅本次调用，不写文件）。后者是关键：`Operands` 会丢掉 `-c` 并跳过 `key=value` 词，所以该命令的子命令解析结果是真实子命令（`status`、`commit`…），按子命令匹配的规则必然漏掉它。读取和清除不拦（`--get`、`--get-all`、`--list`、`--unset`、裸 key），无关 config 写入（`user.email` 等）也不拦。合法的仓库内共享 hooks（`git config core.hooksPath .githooks`）同样会被拦，需要时由用户自己执行或用 `GOLANG_CC_ALLOW_DESTRUCTIVE=1` 覆盖 —— 这与 `git reset --hard`（同样是正常操作但硬拒绝）的校准一致。`core.editor`、`core.pager`、`sequence.editor`、`diff.external` 和 `!` 前缀 alias 属同一类但正当使用远多于此，未纳入。
- `GOLANG_CC_ALLOW_DESTRUCTIVE=1` 是关闭上述硬拒绝层的唯一开关。它只影响硬拒绝，不影响权限策略、可写根检查和 OS sandbox。由于它关掉的正是那道"权限模式推不翻"的闩，**每次它真正改变了结果时都会写一条审计记录** —— 通过 `PermissionAudit` 回调落成 `permission.decision` telemetry 事件和 transcript entry，`rule=destructive_command_override`、`source=GOLANG_CC_ALLOW_DESTRUCTIVE`，`reason` 里带上被放弃的那条拒绝，可在 `session inspect` 和 Trace Viewer 中查到。规则表本就不反对的普通命令不会产生该记录。
- PowerShell classifier 会对 `Remove-Item -Recurse -Force`、`Clear-Disk`/`Format-Volume`、`Set-ExecutionPolicy`、`Start-Process -Verb RunAs`、service/firewall mutation、download-to-`iex`、encoded command、listener/tunnel 等高风险命令触发审批。**PowerShell 只有审批层，没有硬拒绝层**：`dangerousCommandReason` 仅在 Bash 工具调用，PowerShell 的破坏性命令在 bypass 模式下由可写根检查兜底。
- permission prompt tool 可返回 `allow_session`、`allow_global`、`allow_project`、`allow_local`、`deny_session`、`deny_global`、`deny_project`，分别写入当前 session 或 settings 文件。
- MCP tool 在 ask 模式下默认视为需要审批，并进入同一套 audit / prompt / update 闭环。
- MCP stdio server 发来的 `sampling/createMessage`、`elicitation/*`、`permissions/request` callback 会被 client 安全拒绝并返回 JSON-RPC error，避免无审批地升级为模型采样或权限弹窗。
- 权限结果写入 transcript audit。
- `acceptEdits`、`bypassPermissions`、`auto` 等模式做了非 TUI 归一化。

仍有差距：

- MCP server-initiated `permissions` / `elicitation` / `sampling` callback 已通过同一套 TUI/headless permission prompt 路由，审批请求会携带 callback method/params，允许时可返回 JSON payload。
- permission source/preference 已进入审计和审批来源字段，并支持 allow/deny/ask/alwaysAsk/defaultMode 的逐规则来源 provenance。
- auto mode classifier 的核心高风险类别已补齐为 deterministic 本地规则，覆盖 Bash/PowerShell 主风险面；上游 ant-only LLM classifier 的内部 prompt/实验分流仍不作为功能依赖。

判定：TUI/headless allow/deny/ask、legacy alwaysAsk、逐规则来源审计、MCP callback 审批闭环和本地高风险 classifier 主规则集已实现；若未来要求完全复刻 ant-only classifier 的实验 prompt 与 side-query 流程，需要单独接入模型判别器。

## Sandbox

已实现：

- 文件工具限制在 cwd 和 additional directories。
- 写入路径 canonical resolve，覆盖 symlink 逃逸测试。
- Bash 使用 shell AST 检查高风险写命令、重定向、`sh -c` / `bash -lc` 嵌套写入。
- Bash mutating 命令和写重定向中的动态路径会被拒绝，避免 `$TARGET` 这类运行时逃逸。
- Workflow shell step 复用同一套 Bash guard 和 OS sandbox，避免 workflow 执行绕过 Bash 主链路。
- `sandbox.network.disabled`、`sandbox.network.allowDomains`、`sandbox.network.denyDomains`、`sandbox.network.proxy` 和 `sandbox.network.mitm` 会约束 Go 进程内 WebFetch/WebSearch/WebBrowser，避免 web 工具绕过 Bash 的 bwrap network namespace；proxy `required` 会拒绝无 proxy URL 的请求，`direct/off` 会清空 HTTP proxy，MITM CA 会注入 TLS RootCAs。
- Bash/Workflow 在 proxy/MITM required 下会拒绝裸 socket 命令、proxy bypass 参数和 TLS verify bypass 参数，避免常见命令绕过审计。
- PowerShell 工具接入静态写路径审计，覆盖常见 mutating cmdlet、alias 和重定向；strict sandbox 配置下拒绝不受 OS sandbox 保护的 PowerShell 执行。
- Bash 拦截明显危险命令。
- 新增 `sandbox` settings 解析和运行时传递：`enabled`、`failIfUnavailable`、`allowUnsandboxedCommands`、`enabledPlatforms`、`excludedCommands`、`filesystem.allowRead/denyRead/allowWrite/denyWrite`。
- macOS Bash 在 `sandbox.enabled=true` 时通过系统 `sandbox-exec`/Seatbelt profile 启动，OS 层默认只允许写 cwd、`additionalDirectories`、`sandbox.filesystem.allowWrite` 和 Claude 运行时必要临时路径。
- macOS profile 会额外 deny `.golang-cc/settings*.json`、`.go-claude/settings*.json`、`.claude/settings*.json`、`.claude/skills`、`.git/hooks`、`.git/config`，并支持 `sandbox.filesystem.denyRead` / `allowRead` / `denyWrite`。该列表由 `internal/tools` 的单一来源派生，与文件工具和 shell 工具的默认写保护同源 —— 此前 sandbox 侧自带一份且在改名后只剩 `.claude/`，导致沙箱内 `.golang-cc/settings.json` 仍被 bind 成可写。
- 默认写保护（settings 文件与 `.git/hooks`、`.git/config`）不依赖 `sandbox.enabled`，对 Write/Edit 和 Bash/PowerShell 同时生效；`.claude/skills` 例外，沙箱关闭时保持可写以便 skill authoring。deny 模式按 cwd 解析，因此 `additionalDirectories` 内的同名路径不在保护范围内。
- Linux Bash 在 `sandbox.enabled=true` 时通过 `bwrap`/bubblewrap 启动，使用只读根挂载、cwd/additional/allowWrite 可写 bind、denyWrite read-only 覆盖、denyRead tmpfs 或 `/dev/null` 覆盖、`--unshare-pid`、独立 `/proc`、可选 `--unshare-net`、Unix socket deny bind，并在 `sandbox.seccomp.enabled=true` 时通过 `--seccomp FD` attach classic seccomp BPF profile，阻断 ptrace、mount/module/kexec/bpf/perf/keyring 等高风险 syscall。
- Linux 支持 `sandbox.enableWeakerNestedSandbox=true` 时省略独立 `/proc`，用于不支持完整 namespace 的嵌套容器环境。
- Bash tool 支持原版风格的 `dangerouslyDisableSandbox`，仅在 `sandbox.allowUnsandboxedCommands=true` 时允许降级到非 sandbox 执行。
- 已加入真实 OS sandbox 测试：`awk` 这类静态审计无法识别的运行时写逃逸会被 macOS Seatbelt / Linux bwrap 阻止，同时 workspace 内写入保持可用；Linux seccomp 会通过 `unshare -U true` 高风险 syscall e2e 用例验证；非当前平台测试会自动跳过。

仍有差距：

- macOS 和 Linux Bash 主链路已有真实 OS sandbox；WSL 和 Windows sandbox 仍未实现，仅在 `failIfUnavailable=false` 且允许 unsandboxed 时明确降级到非 sandbox。
- Linux network namespace 隔离、Go web tool network-disabled/domain/proxy/MITM policy、Bash/Workflow proxy/TLS bypass guard、Unix socket deny bind 和 bwrap seccomp BPF attach 已接入。
- Bash 静态分析仍保留为前置防线；Go 进程内写文件工具已接入 sandbox allowWrite/denyWrite 和 sensitive 默认 deny 路径，OS sandbox 仍主要覆盖 Bash/Workflow 子进程。
- PowerShell 已有静态写路径安全语义，strict sandbox 下拒绝无 OS sandbox 的执行；允许显式降级时会携带 proxy/MITM 环境变量。但仍没有 Windows Job Object/AppContainer 级真实 OS sandbox。

判定：macOS Bash/Seatbelt 与 Linux Bash/bubblewrap OS sandbox 主链路已接入并有真实阻断测试；Linux 已补 seccomp BPF attach，Go web 工具已补 proxy/MITM policy，Go 写文件工具已补 sandbox filesystem policy；PowerShell 已有静态安全检查和 strict 降级拒绝；完整 sandbox-runtime parity 仍待 Windows Job Object/AppContainer、WSL 特化和 PowerShell 真实 OS sandbox 补齐。

## Sessions / Rewind / Checkpoint

已实现：

- JSONL transcript 持久化。
- `session list/show/search/rename/delete/clear`。
- `session checkpoint`、`session rewind`、`session fork`。
- 每个新 user prompt 自动记录 checkpoint，并给 transcript entry 分配 id。
- file_change snapshot，rewind 可恢复 checkpoint 后的多文件变更并截断 transcript。
- `--rewind-files <message-id>` 可只恢复文件并保留 transcript。
- TUI `/rewind` / `/checkpoint` 可列出最近 user message，并按 message id 恢复 code + conversation、conversation-only 或 files-only。
- `--resume-session-at <entry-id>` 可只恢复到指定 transcript entry。
- resume / continue 消息重建会合并相邻 role block、去掉孤儿 tool_result/thinking、为缺失 tool_result 补合成 error block，并追加 continuation / no-response sentinel。
- TUI 在 `--resume` / `--continue` 进入时会显示恢复状态和修复摘要，便于用户理解当前恢复的是 interrupted turn、interrupted prompt 还是普通历史。
- assistant thinking/signature blocks 会进入 transcript；resume 时只恢复后面仍跟随 text/tool_use 等安全 assistant content 的 thinking，尾部/孤儿 thinking 会被过滤，避免触发 thinking block API 约束。
- compact summary。

仍有差距：

- TUI `/rewind` 已接入 message-id 闭环，但仍是文本候选/显式 id 形态，不是原生 Claude Code 的 fullscreen message selector modal。
- resume 已有 interrupted turn 修复金标，覆盖 unresolved tool_use、orphaned tool_result、orphaned thinking、synthetic tool_result、continuation prompt 和 trailing user no-response sentinel。
- thinking/redacted_thinking 已做结构级持久化和安全恢复；真正 upstream-exact 的 proprietary signature validation 仍需要 live Anthropic 行为验证。
- 原生 message selector 的像素级 UI、键盘选择体验和 summarize 选项仍未完全对齐。

判定：checkpoint/rewind 数据模型和 CLI 已可用；非 TUI 与 TUI resume/interrupted-turn 主功能已对齐到可恢复、可解释、API-valid 的语义，剩余主要是 proprietary thinking signature 的 live upstream 校验。

## API / OpenAI Compatibility

已实现：

- `/query` 和 `/v1/chat/completions`。
- OpenAI-compatible messages 输入映射，包括 content parts、tool_calls、tool_call_id/name。
- OpenAI-compatible 普通响应、SSE 响应和 response-side `tool_calls`。
- tenant persistence 保存 tool call 元数据。

仍有差距：

- 不是完整 OpenAI API surface，只覆盖 chat completions 和 models。
- OpenAI tool_calls 参数兼容以客户端测试覆盖为准，未覆盖所有第三方 SDK 的边缘字段。

判定：当前目标 API 子集兼容，非完整 OpenAI API。

## 日志和可观测性

已实现：

- `internal/observability` 统一日志入口。
- 默认 zap JSON 输出，同时保留 slog 兼容。
- 支持 debug、info、error、panic。
- 标准字段：`traceid`、`userid`、`tenantkey`、`action`、`function`。
- Gin middleware、server、query、tool、tenant repository/service 关键路径已补日志。
- 结构化 telemetry 事件体系已接入 API request、query run、model request、tool execution、permission decision、mobile chat stream。
- telemetry 支持 LoggerSink、MemorySink、tenant RecorderSink、Prometheus MetricsSink 和 HTTP exporter；鉴权通过且存在 tenant/user context 时写入 MySQL `tenant_telemetry_events`。
- `/tenant/telemetry` 提供 owner/admin 查询和自定义事件写入，支持分页和搜索。
- telemetry properties 会脱敏 API key、JWT、Authorization、token、password、secret、prompt、content、transcript、private URL 等敏感字段。

仍有差距：

- usage 汇总会单独统计 prompt cache creation/read tokens，便于排查缓存命中和成本变化。
- tracing span 级 vendor exporter 仍可按生产需要继续扩展；事件 taxonomy 未来可继续按上游新增事件扩展。

判定：排障日志、核心 telemetry 事件体系、Prometheus metrics 和 HTTP exporter 已落地；vendor-specific tracing exporter 仍是生产化扩展项。

## 结论 Todo

P0 仍建议保留：

- Session resume 细节：TUI 恢复体验和 thinking/redacted_thinking 结构级恢复已补；剩余是 proprietary thinking signature 的 live upstream 校验。
- Permission：TUI/headless 普通工具、逐规则来源审计、MCP callback 审批和本地 classifier 主规则集已接入；如需完全复刻 ant-only 实验 classifier，再补 side-query LLM classifier。
- Sandbox：继续补 Windows Job Object/AppContainer、WSL 特化和 PowerShell 真实 OS sandbox；Linux/macOS 主链路继续补更多金标阻断样例。
- Multi-agent：TUI 已展示 sub-agent task card、description、turn/messages、最后工具、session、耗时和失败/取消摘要；剩余是 pixel-exact upstream styling 与未知 swarm UI affordance。

P1 建议：

- Prompt：本地 GrowthBook-compatible 动态配置源、远端 `growthbook.url` refresh/cache、ant-only override、命名 section registry、stable/volatile 缓存语义和 proactive/Kairos prompt path 已接入；后续主要是跟踪上游新增 section。
- Skill runtime frontmatter：provider 级 effort 已映射到 Anthropic SDK thinking config；后续继续补更多 skill-scoped policy，并用 live Anthropic 环境校验 proprietary thinking signature 长尾行为。
- Hook lifecycle：继续补 matcher、permission update destination、细粒度 schema。
- stream-json 的 ant 内部 `research` 字段和新增上游实验字段。
- MCP skill builder、remote marketplace search UX 和 upstream bundled catalog 逐项对齐。
- 更完整的工具矩阵，尤其 Chromium 级浏览器自动化和 feature-gated 工具。

本次提交可以作为“P0 TUI/stream/sandbox/network 闭环增强”提交，但不能标注为“100% 完成”。
