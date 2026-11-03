# Tenant Skills Runtime TODO

## 目标

让 API Server / Mobile Chat 在多租户模式下真正按当前 `X-Tenant-Key` + `X-User-Id` 使用租户配置的 skills：

- 租户 A 与租户 B 可配置不同 skills，互不影响。
- 用户级 `tenant_user_skill_overrides` 可启停或覆盖配置。
- skill 配置热加载，不需要重启 server。
- runtime 仍保持 Claude Code skills 的渐进式加载：system prompt 只暴露 metadata，完整 `content_md` 只在模型调用 `Skill` tool 后加载。
- 保留现有本地 `.claude/skills` 行为，tenant skill 优先，本地 skill 作为 fallback。

## 当前状态

- DONE：MySQL schema 已有 `tenant_skills` 与 `tenant_user_skill_overrides`。
- DONE：Server 已有 `/tenant/skills`、`/tenant/skill-overrides`、`/tenant/effective-skills` API。
- DONE：API Server / OpenAI-compatible / Mobile Chat session 和 message 已持久化。
- DONE：query runtime 已支持 tenant skill provider，并在 chat/code 请求中注入 tenant effective skill metadata。
- DONE：`Skill` tool 已支持 tenant skill 优先加载，不存在时 fallback 到本地 `SKILL.md`。
- DONE：runtime telemetry 与 `skill_activated` stream event 已带出 active skill 的 source/version/fallback。
- DONE：WebUI Skills 面板已提供 tenant skills/effective skills/override 管理入口。
- DONE：新增真实 curl smoke 脚本，可验证 tenant skill CRUD、effective skills、可选 query 和 MySQL 查库。

## 技术方案

1. 在 `internal/skills` 中暴露通用转换/格式化能力：
   - 从 markdown content 解析 frontmatter 到 `skills.Skill`。
   - 从 `[]Skill` 构造 metadata catalog prompt。
2. 在 `internal/query` 增加 `TenantSkillProvider`：
   - `ListTenantSkills(ctx, prompt, limit)` 返回当前 tenant/user 的 enabled effective skills。
   - `GetTenantSkill(ctx, name)` 返回指定 tenant skill 的完整内容。
3. 在 server query runner 注入 provider：
   - provider 直接复用现有 `TenantService.ListEffectiveSkills/GetEffectiveSkill`。
   - 每轮 query 查询 DB，不做进程缓存，天然热加载。
4. query runtime catalog 注入：
   - chat/code 模式都先注入 tenant skill metadata。
   - code 模式继续注入本地 skills catalog。
   - tenant skill 和本地 skill 同名时，tenant skill 优先。
5. `Skill` tool 和 active skill runtime：
   - `tools.Context` 携带 tenant skill provider。
   - `Skill` tool 优先加载 tenant skill；不存在时 fallback 到本地 `skills.Load`。
   - `query.Session.applySkillRuntime` 同样优先从 tenant provider 加载 metadata，确保 `allowed-tools/model/context/agent/effort/hooks` 生效。
6. 运行时可观测：
   - `tool.execution.started/finished` telemetry properties 写入 `active_skill`、`active_skill_source`、`active_skill_version`、`active_skill_fallback`。
   - `skill_activated` SSE event 写入 `skill_source`、`skill_version`、`skill_fallback`，便于 WebUI/trace 排查 tenant 或 local fallback 来源。
7. WebUI 管理入口：
   - Skills 面板分为 tenant skills、effective skills 与编辑区。
   - 支持选择编辑、新建、保存 tenant skill、保存 user override、启停和刷新。
8. 真实 smoke：
   - `scripts/tenant-skills-runtime-smoke.sh` 默认使用 curl 验证 `/tenant/user`、`/tenant/skills`、`/tenant/skill-overrides` 和 `/tenant/effective-skills`。
   - 设置 `GOLANG_CLAUDE_CODE_TENANT_SKILLS_SMOKE_QUERY=1` 后继续验证 `/query` tenant skill runtime 和 session 持久化；脚本兼容 `/query` JSON 与 SSE 两种返回形态，JSON 模式会检查 `tool_calls[].name == "Skill"` 和 tenant skill 正文；设置 `GOLANG_CLAUDE_CODE_TENANT_SKILLS_SMOKE_MYSQL_DSN` 后额外查 MySQL。

## 验收清单

- [x] tenant skill metadata 会出现在 chat prompt catalog，且不泄漏正文。
- [x] `Skill` tool 能加载 tenant skill 的完整 `content_md`。
- [x] tenant skill frontmatter 会影响 active skill runtime，例如 `effort`、`allowed-tools`。
- [x] tenant skill 不存在时仍 fallback 到本地 filesystem skill。
- [x] runtime telemetry 和 SSE 能看到 active skill source/version/fallback。
- [x] WebUI 提供 tenant/effective/override 管理入口。
- [x] 提供真实 curl smoke 脚本。
- [x] server query runner 会为 tenant request 注入 tenant skill provider。
- [x] `/tenant/effective-skills` 等 API 文档已补充 runtime 行为说明；本次未改变 API schema，无需重新生成 Swagger。
- [x] 单元测试通过：`go test ./internal/skills ./internal/tools/skill ./internal/query ./internal/server -count=1`。
- [x] 真实 MySQL E2E 通过：`GOLANG_CLAUDE_CODE_MYSQL_E2E_DSN=... go test ./internal/server -run TestMySQLE2ETenantSkillsRuntime -count=1 -v`。
- [x] 本地配置 provider 真实 `/query` 验收通过：`provider=custom`、`model=glm-5.1`，`GOLANG_CLAUDE_CODE_TENANT_SKILLS_SMOKE_QUERY=1 scripts/tenant-skills-runtime-smoke.sh`。
- [x] 全量测试通过：`go test ./... -count=1`。
- [x] `git diff --check` 通过。

## 进度

- 2026-06-26：创建技术方案与 TODO 文档。
- 2026-06-26：实现 tenant skill provider、tenant catalog 注入、`Skill` tool tenant 优先加载、active skill runtime metadata，并补充 targeted tests。
- 2026-06-26：`go test ./... -count=1` 和 `git diff --check` 通过。
- 2026-06-26：新增并通过真实 MySQL tenant skills runtime E2E，验证 metadata-only catalog、tenant `Skill` lazy-load、frontmatter runtime metadata 和 message/skill SQL 落库。
- 2026-06-26：补齐 runtime skill observability、WebUI Skills 管理入口、curl smoke 脚本和相关文档。
- 2026-06-26：使用本地配置文件 provider `custom` / `glm-5.1` 跑通真实 `/query` tenant skill runtime；模型调用 `Skill` 工具加载 MySQL tenant skill 正文，assistant 响应与 user message 均落库。
