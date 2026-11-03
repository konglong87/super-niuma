# Skills 渐进式加载机制

Go 版 skills 加载遵循三段式渐进披露：

1. **Metadata catalog**：启动 query 时只枚举 skill 名称、描述和 `when_to_use`，注入 system prompt。
2. **按需加载 `SKILL.md`**：模型判断任务匹配后，调用 `Skill` tool 并传入精确 skill name。
3. **资源按需读取**：`references/`、`scripts/`、`assets/` 等附加资源不预读，由 `SKILL.md` 指令引导后续工具按需读取或执行。

## 目录来源

当前发现顺序：

- 用户级 owned skills：`~/.golang-cc/skills` 或 configured owned global root 的 `skills`
- 用户级 legacy fallback：`~/.claude/skills`
- 用户 legacy command：`~/.golang-cc/commands/*.md`、`~/.claude/commands/*.md`
- marketplace cache：`~/.golang-cc/skills-marketplace`、legacy fallback `~/.claude/skills-marketplace`
- MCP skill cache：`~/.golang-cc/mcp-skills`、legacy fallback `~/.claude/mcp-skills`
- bundled skills：`CLAUDE_BUNDLED_SKILLS_PATHS` 或 `GOLANG_CC_BUNDLED_SKILLS_PATHS`
- MCP env skill roots：`CLAUDE_MCP_SKILL_PATHS` 或 `GOLANG_CC_MCP_SKILL_PATHS`
- 项目级：最近的 `.golang-cc/skills` 或 configured identity dir 的 `skills`，以及最近的 `.claude/skills`
- 项目 legacy command：最近的 `.golang-cc/commands/*.md`、`.claude/commands/*.md`
- 插件：`.claude/plugins/<plugin>/skills`
- 插件 legacy command：`.claude/plugins/<plugin>/commands/*.md`
- 插件自定义 skills 目录：`plugin.json` 的 `skills` 字段
- 动态额外根：`CLAUDE_SKILL_PATHS` 或 `GOLANG_CC_SKILL_PATHS`，使用系统 path-list 分隔符；`List` 每次都会重新扫描，因此新增 skill 不需要重启进程

插件 manifest 支持：

- `.claude-plugin/plugin.json`
- `.codex-plugin/plugin.json`
- `plugin.json`

## 命名规则

- standalone skill：`<skill-name>`
- plugin skill：`<plugin-name>:<skill-name>`
- legacy command：同名显示为 skill metadata，并标记 `legacy=true`
- source 会记录为 `user`、`project`、`plugin`、`bundled`、`marketplace` 或 `mcp`

插件 skill 使用命名空间，避免不同插件之间或插件与项目 skill 之间冲突。

## Marketplace Sync

`skills sync` 可以把本地或 HTTP(S) marketplace 索引同步到 skill cache：

```bash
go run ./cmd/golang-cc skills sync --source ./skills-index.yaml
go run ./cmd/golang-cc skills sync --source https://example.test/skills.yaml --target ~/.golang-cc/skills-marketplace
```

如果只想安装或更新 marketplace 里的单个 skill，可以用 `skills install`：

```bash
go run ./cmd/golang-cc skills install --source ./skills-index.yaml go-review
go run ./cmd/golang-cc skills install --source https://example.test/skills.yaml --name go-review --target ~/.golang-cc/skills-marketplace
```

修改 Skill 后可以先运行 report-only 静态检查：

```bash
go run ./cmd/golang-cc skills lint ./path/to/skill
go run ./cmd/golang-cc skills lint ./path/to/skill/SKILL.md --json
```

检查器会解析 Markdown shell fence 和 shell AST，报告跨 fence 依赖局部变量、内联 `BASH_SOURCE` 路径、空集合计数假成功和不稳定 Skill 相对路径。该命令当前只报告问题，不阻止用户、项目、租户或 marketplace Skill 加载。运行 filesystem-backed Skill 时，每个 Bash/PowerShell 子进程都会获得 `GOLANG_CC_SKILL_DIR`，并同时提供兼容别名 `CLAUDE_SKILL_DIR`；shell fence 之间的其他局部变量、函数和 `cd` 状态不会继承。

`install` 和 `sync` 都会写入 `SKILL.md`，再次运行会覆盖同名 marketplace skill，用于更新本地缓存。index 可以提供 `version`、`content_sha256`、`signature_alg`、`public_key` 和 `signature`；设置 hash 后，安装/同步会在写入前校验内容，hash 不匹配会失败；设置签名后会校验原始 content/path 载入内容的 Ed25519 签名，签名或公钥缺失、格式错误或验签失败都会拒绝写入。

索引格式：

```yaml
skills:
  - name: go-review
    description: Review Go code.
    version: 1.0.0
    content_sha256: optional_sha256_hex
    signature_alg: ed25519
    public_key: optional_base64_ed25519_public_key
    signature: optional_base64_ed25519_signature
    content: |
      # Go Review

      Review the current Go changes.
```

也可以使用 `path` 指向同目录下的 `SKILL.md` 内容。同步后 `List` 会按 marketplace source 发现，不需要重启进程。

`Search` 会基于 metadata 做轻量搜索，匹配 name、local name、description、when_to_use、source 和 plugin，不读取完整 `SKILL.md` 正文，保持渐进式加载语义。

插件校验会检查 manifest 中声明的 skills/agents 目录是否存在，以及 MCP server 是否设置了 `url` 或 `command`，避免坏插件悄悄进入发现链路。

Marketplace index 可直接搜索，不必先同步：

```bash
go run ./cmd/golang-cc skills marketplace-search --source ./skills-index.yaml --query review
```

安装前可以先审计 marketplace 索引，查看 skill 数量、hash 覆盖数、签名覆盖数、version、path/inline 内容来源：

```bash
go run ./cmd/golang-cc skills marketplace-info --source ./skills-index.yaml
```

安装或同步前后，可以对比本地 marketplace root 与索引，查看 current / outdated / missing 状态：

```bash
go run ./cmd/golang-cc skills marketplace-status --source ./skills-index.yaml
```

长驻进程可以复用 `internal/skills.Watch` 或 CLI `skills watch` 监听本地 user/project/plugin/bundled/marketplace/MCP/env roots。事件会包含 skill 名称、来源、插件名、文件路径和文件系统操作，用于 TUI/server 实时刷新 catalog：

```bash
go run ./cmd/golang-cc skills watch --once
```

可以用 `skills context` 预览某个 prompt 会注入模型上下文的轻量 skill catalog，用于排查 `paths`、`disable-model-invocation` 和动态 roots 是否生效：

```bash
go run ./cmd/golang-cc skills context --prompt "review internal/query/query.go"
```

本地 skill 改进反馈可用 `skills feedback` 记录到 `~/.golang-cc/skill-feedback.jsonl` 或 configured owned global root，便于后续人工汇总或接入远端 marketplace survey：

```bash
go run ./cmd/golang-cc skills feedback go-review --rating 5 --comment "useful review checklist"
```

含 `SKILL.md` 的目录可打包，便于 marketplace/MCP skill 分发：

```bash
go run ./cmd/golang-cc skills package ./my-skill --output ./my-skill.skill.zip
```

Bundled catalog 可用 expected catalog 做字段/hash 校验：

```bash
go run ./cmd/golang-cc skills validate-bundled --source ./bundled-catalog.yaml
```

`bundled-catalog.yaml` 支持 `name`、`description`、`when_to_use`、`allowed_tools`、`model`、`context`、`agent`、`effort` 和 `content_sha256`。

## Frontmatter

`SKILL.md` 使用 YAML frontmatter。当前 metadata 解析字段包括：

- `name`
- `description`
- `when_to_use` / `whenToUse`
- `allowed-tools` / `allowed_tools` / `allowedTools`
- `argument-hint` / `argument_hint` / `argumentHint`
- `arguments`
- `version`
- `model`
- `disable-model-invocation` / `disable_model_invocation` / `disableModelInvocation`
- `user-invocable` / `user_invocable`
- `context`
- `agent`
- `effort`
- `hooks`
- `paths`

常见示例：

```yaml
---
name: go-review
description: Review Go code and suggest focused fixes.
when_to_use: Use after editing Go files.
allowed-tools:
  - Read
  - Bash(go test:*)
hooks:
  PreToolUse:
    - command: "printf '{}'"
      matcher: "Bash:go test*"
---
```

也支持 block scalar 描述：

```yaml
---
description: |
  Review Go code.
  Prefer minimal patches.
---
```

如果没有 frontmatter description，会回退到正文第一段作为描述，保持旧 skill 文件可用。

## Query 集成

`query.Session.run` 会调用 `skills.CatalogPrompt(cwd)`，把轻量 catalog 附加到 system prompt：

- catalog 中只包含 name/description/when_to_use 级信息。
- 不包含完整 `SKILL.md` 正文。
- 不预读 references/scripts/assets。
- full skill 内容只通过 `Skill` tool 按名称加载。
- `disable-model-invocation: true` 的 skill 仍可 `skills show` 或 `Skill` tool 精确加载，但不会主动注入模型 catalog。
- `paths` 会参与 catalog 过滤：带路径约束的 skill 只在 prompt 提到匹配路径时暴露给模型。

API Server 多租户模式会额外接入 tenant skill provider：

- 当请求带 `X-Tenant-Key` 和 `X-User-Id` 且已配置 MySQL tenant storage 时，runtime 会读取当前 tenant/user 的 enabled effective skills。
- tenant skill catalog 同样只注入 metadata，不注入 `content_md` 正文。
- `Skill` tool 优先按 `skill_key` 加载当前 tenant effective skill；不存在时 fallback 到本地 filesystem skill。
- tenant skill `content_md` 使用同一套 YAML frontmatter 解析，支持 `description`、`when_to_use`、`allowed-tools`、`model`、`context`、`agent`、`effort`、`hooks` 和 `paths`。
- 每轮 query 直接读取 tenant effective skills，不做进程内长缓存；通过 `/tenant/skills` 或 `/tenant/skill-overrides` 更新后，下个请求即可生效。
- chat mode 只加载 tenant skills 和 tenant context，不加载服务端本地 `.claude/skills`；code mode 会在 tenant skill catalog 之后继续加载本地 skill catalog。
- OpenAI-compatible `response_format.type=json_schema` 会进入结构化输出 fast path：禁工具调用、单模型调用、跳过自动标题生成。为了仍然支持 tenant skill 指令，runtime 可按 schema/prompt 识别业务 skill 并把当前 tenant 的 skill 正文内联进 system prompt；AI Study 的 `teach_decision_v1` / `teach_context_v1` 会内联 `teach` skill。

AI Study `teach` tenant skill 的结构化输出约束必须与 `teach_decision_v1` 对齐：

- `skill_version` 必须输出 integer，当前版本为 `1`，不能输出 `"unknown"`。
- `decision_schema_version` 必须是 `teach_decision_v1`。
- `contract_version` 必须是 `teach_context_v1`。
- `profile_item_upsert.payload` 必须使用 `item_type`、`concept`、`note`。
- 禁止在 `profile_item_upsert.payload` 中输出 `key`、`value`、`source`。

## Runtime frontmatter

Go 版已经把部分 frontmatter 接入运行时：

- `allowed-tools`：调用 `Skill` 后会作为 active skill 约束后续工具调用。
- `model`：active skill 可覆盖后续 query turn 的模型；`inherit` 表示沿用当前模型。
- `context: fork`：`Skill` tool 会在隔离的 forked skill context 中执行，并返回 forked 结果。
- `paths`：参与 skill catalog 暴露过滤。
- `hooks`：active skill 会把 skill-scoped hooks 叠加到全局 hooks，作用于后续工具调用；hook 支持 `matcher`、`tool`、`tools` 字段，按工具名和 `Tool:qualifier` 输入过滤。
- `agent`：active skill 设置 agent 时，后续 `Task` 未显式指定 `subagent_type` 会默认使用该 agent。
- `effort`：保留在 active skill runtime metadata 中，供 provider 推理强度映射和审计使用。

## 验证覆盖

- `internal/skills`：metadata-only list、metadata search、YAML frontmatter、block scalar、plugin namespace、legacy command catalog、bundled/marketplace/MCP roots、marketplace install/sync/search、skill zip packaging、bundled catalog validation、动态额外根、`disable-model-invocation`、`paths` 过滤、hook matcher 解析。
- `internal/plugins`：plugin skills/agents/MCP 贡献校验。
- `internal/query`：system prompt 只注入 metadata，不泄露 skill body；skill-scoped hooks 会在 active skill 后作用于后续工具调用。
- `internal/tools/skill`：按 name 加载完整 `SKILL.md`，并覆盖 forked skill 执行。
- `internal/server` + `internal/query`：tenant effective skills 以 provider 方式注入 runtime，`Skill` tool tenant 优先、本地 fallback。
- `internal/tools`：active skill `allowed-tools` runtime 约束。
