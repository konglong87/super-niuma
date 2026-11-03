# Go Claude 配置与状态文件隔离方案

## 背景

Go Claude 当前已经具备自己的运行时身份和部分自有路径：

- 默认产品标识：`go-claude`
- 默认配置目录：`.go-claude`
- 默认项目指导文件：`go-e2e.md`
- 全局 settings 写入：`~/.go-claude/settings.json`
- transcript 默认写入：`~/.go-claude/projects/...`

但仓库里仍有多条代码路径会默认写入 `.claude` 或受 `CLAUDE_CONFIG_DIR` 控制。这会和原版 Claude Code 的配置、状态、插件、skills、todos、agents、worktrees 混在一起，造成互相污染。

目标是：

1. Go Claude 自己产生的配置和运行状态默认只写 Go Claude 标识路径。
2. Legacy Claude Code 文件可以作为 fallback 读取，但默认不能写入。
3. 产品标识可集中配置。以后如果要把 `go-claude` 改为 `go-code`，应优先通过一个中心静态配置或一处默认值完成，而不是在各包分散修改。

## 核心原则

- 写入路径必须是 Go Claude owned path：默认 `~/.go-claude/...` 或 `<project>/.go-claude/...`。
- `.claude/...`、`CLAUDE.md`、`CLAUDE_CONFIG_DIR` 只允许作为 legacy compatibility read source，除非用户显式传入目标路径并确认要写 legacy。
- Skills 是特殊兼容面：`.claude/skills` 不是可有可无的 fallback，而是每次都要纳入 skill discovery。Go Claude 必须聚合加载 `.go-claude/skills`、`.claude/skills`、npx/marketplace/MCP/bundled/env skills 等所有有效来源。
- 自动写入、CLI mutation、工具 mutation、后台任务、agent worktree、marketplace install、debug log、snapshot、goal state 都属于 Go Claude owned state。
- 配置合并顺序可以继续支持 legacy project fallback，但写入目标必须按 active identity 生成。
- 新会话或首次 mutation 时，如果只发现 legacy Claude 配置文件，例如 `<project>/.claude/settings.json` 或 `<project>/.claude/settings.local.json`，Go Claude 可以读取它们作为初始配置来源，但必须创建自己的 `<project>/.go-claude/settings.json` 或 `<project>/.go-claude/settings.local.json` 作为后续写入目标，不能继续写回 legacy 文件。
- 迁移必须可观测：`status` / `doctor` / trace 中要能看到每个配置来源是 `go-claude` 还是 `legacy-claude`。

## 当前已有的正确基础

| 能力 | 当前行为 | 代码位置 |
| --- | --- | --- |
| 全局 settings 写入 | 默认写 `~/.go-claude/settings.json` | `internal/config/config.go:401-425` |
| 项目 settings 写入 | `ProjectSettingsPath` 根据 identity 的 `ConfigDirName` 生成，默认 `.go-claude/settings*.json` | `internal/config/config.go:436-455` |
| settings 读取顺序 | 先全局 `.go-claude`，再 legacy 项目 `.claude`，再项目 `.go-claude`，最后 `config/*.yaml` | `internal/config/config.go:470-497` |
| transcript 默认写入 | 默认 `GOLANG_CLAUDE_CODE_CONFIG_DIR` 或 `~/.go-claude/projects` | `internal/session/store.go:121-132` |
| 项目指导文件 | 优先 `go-e2e.md`，再 fallback `CLAUDE.md`，再 `AGENTS.md` | `internal/memory/memory.go:472-489` |
| identity 默认值 | 默认产品名、配置目录、指导文件集中在 `internal/identity` | `internal/identity/identity.go:10-17` |

这说明配置隔离方向已经开始落地，但还没有覆盖所有运行状态和扩展目录。

## 会写 legacy Claude 路径的生产代码清单

下面只列生产代码路径，不把测试 fixture、验收脚本临时目录、文档示例作为必须迁移项。

### P0：直接写 `.claude` 或 `~/.claude`，必须迁移

| 模块 | 当前写入 | 风险 | 代码位置 | 目标路径 |
| --- | --- | --- | --- | --- |
| TodoWrite | `<cwd>/.claude/todos.json` | 污染原版 todo 状态；TUI 启动也会读到旧 todo | `internal/tools/todowrite/todowrite.go:144-160`, `internal/tools/todowrite/todowrite.go:197-198` | `<project>/.go-claude/todos.json` |
| PlanMode | `<cwd>/.claude/plan_mode.json` | plan mode 状态与原版混用 | `internal/tools/planmode/planmode.go:104-123` | `<project>/.go-claude/plan_mode.json` |
| Goal store | `CLAUDE_CONFIG_DIR` 或 `~/.claude` 下的 `goals/*` | 长期目标、plan、evidence 写入原版配置根 | `internal/goal/goal.go:159-166`, `internal/goal/store.go:432-530` | `~/.go-claude/goals/*` |
| Background store | `CLAUDE_CONFIG_DIR` 或 `~/.claude/background_sessions.json`、`background/logs/*.log` | 后台任务与原版配置根混用 | `internal/background/background.go:80-87`, `internal/background/background.go:129`, `internal/background/background.go:362-378` | `~/.go-claude/background/*` |
| Scheduler store | `CLAUDE_CONFIG_DIR` 或 `~/.claude/schedules.json`、`scheduler/*` | 计划任务、daemon pid/log/meta 写入原版配置根 | `internal/scheduler/scheduler.go:124-137`, `internal/scheduler/scheduler.go:324-345`, `internal/scheduler/scheduler.go:520-537`, `internal/scheduler/scheduler.go:610-617` | `~/.go-claude/scheduler/*` |
| Agent worktree | `<git-root>/.claude/worktrees/<slug>` | Go Claude agent worktree 出现在原版 Claude 目录 | `internal/agentworktree/worktree.go:59-72` | `<git-root>/.go-claude/worktrees/<slug>` |
| Skills marketplace | `~/.claude/skills-marketplace/<skill>/SKILL.md` | marketplace 安装污染原版 user skills 生态 | `internal/skills/skills.go:546-568`, `internal/skills/skills.go:718-795` | `~/.go-claude/skills-marketplace/...` |
| Plugin install | `~/.claude/plugins` 或 `<project>/.claude/plugins` | 插件安装污染原版 plugin 目录 | `internal/plugins/plugins.go:164-180`, `internal/plugins/plugins.go:399-413` | `~/.go-claude/plugins` 或 `<project>/.go-claude/plugins` |
| Skill feedback | `~/.claude/skill-feedback.jsonl` | 用户反馈写到原版配置根 | `internal/skills/feedback.go:28-57` | `~/.go-claude/skill-feedback.jsonl` |
| Large file snapshots | `CLAUDE_CONFIG_DIR/snapshots` 或 `~/.claude/snapshots` | 文件 snapshot 写到原版配置根 | `internal/files/largefile.go:861-869` | `~/.go-claude/snapshots` |
| Update check | `CLAUDE_CONFIG_DIR/update/last_check` 或 `~/.claude/go-claude/update/last_check` | Go Claude update 状态写进 `.claude` | `internal/updater/updater.go:352-359` | `~/.go-claude/update/last_check` |
| TUI debug log | `~/.claude/debug/golang-claude-code-tui.log` | debug log 写入原版 debug 目录 | `internal/cli/cli.go:1731-1733` | `~/.go-claude/debug/golang-claude-code-tui.log` |

### P1：写入目标由配置函数决定，需要确认不会回落到 `.claude`

| 模块 | 当前行为 | 风险 | 代码位置 | 目标 |
| --- | --- | --- | --- | --- |
| CLI `auth login/logout` | 通过 `SaveGlobalSettings` 写全局 settings | 当前已写 `.go-claude`，需测试锁定 | `internal/cli/cli.go`, `internal/config/config.go:409-425` | 保持 `~/.go-claude/settings.json` |
| CLI `config set/unset` | 默认全局写 `SaveGlobalSettings`，`--project/--local` 写 `ProjectSettingsPath` | 若 identity 被 legacy settings 影响，要保证写 active Go Claude path | `internal/cli/cli.go`, `internal/config/config.go:436-455` | 默认 `.go-claude` |
| 权限弹窗 project/local 持久化 | `applyPermissionUpdate` 的 `project` / `local` 分支写 `ProjectSettingsPath` | 必须禁止写 legacy `.claude/settings*.json` | `internal/query/query.go:3341-3362` | `.go-claude/settings*.json` |
| `/init --settings` | 写 `ProjectSettingsPath` | 当前正确，但要锁测试 | `internal/cli/cli.go:6924-6948` | `.go-claude/settings.json` |
| 新会话仅存在 legacy settings | 读取 `.claude/settings*.json` 后没有自动 materialize `.go-claude/settings*.json` | 后续 mutation 可能继续依赖 legacy 来源，用户难以确认 Go Claude 自有配置已接管 | `internal/config/config.go`, `internal/cli/cli.go`, `internal/query/query.go` | 创建 `.go-claude/settings*.json`，内容来自 legacy 合并结果或最小标识配置 |

### P2：当前只读 legacy/source discovery，可以保留但要调整优先级

| 模块 | 当前读取 | 建议 |
| --- | --- | --- |
| Project memory | `go-e2e.md` 优先，`CLAUDE.md` fallback，`.claude/CLAUDE.md`、`.claude/rules/*.md` 仍读取 | 增加 `.go-claude/rules/*.md`、`.go-claude/workflows/*.md` 优先读取；`.claude/*` 保留 fallback |
| User memory | 仍读 `~/.claude/CLAUDE.md` | 增加 `~/.go-claude/go-e2e.md` 或 `~/.go-claude/memory/*.md` 优先；`~/.claude/CLAUDE.md` fallback |
| Agent memory | 项目 `.claude/agent-memory*` | 增加 `.go-claude/agent-memory*` 优先；legacy fallback |
| Skills / commands | `~/.claude/skills`、`~/.claude/commands`、项目 `.claude/skills`、`.claude/commands` | 必须聚合加载所有 skill 来源：`.go-claude/skills`、`.claude/skills`、npx/marketplace/MCP/bundled/env skills；commands 可按 `.go-claude/commands` 优先、`.claude/commands` fallback |
| Agents | `~/.claude/agents`、项目 `.claude/agents` | 增加 `.go-claude/agents` 优先；legacy fallback |
| Plugins | `~/.claude/plugins`、项目 `.claude/plugins` | 增加 `.go-claude/plugins` 优先；legacy fallback |
| Output styles | `~/.claude/output-styles`、项目 `.claude/output-styles` | 增加 `.go-claude/output-styles` 优先；legacy fallback |
| Workflow tool | `.claude/workflows/*.yaml` | 增加 `.go-claude/workflows/*.yaml` 优先；legacy fallback |

## Skills 加载特殊规则

Skills 的目标不是隔离后少加载，而是隔离写入后全量聚合读取。原因是 skills 生态可能来自原版 Claude Code、Go Claude 自有目录、npx 安装目录、marketplace、MCP server、本地 env 指定路径或 bundled runtime。任何单一路径优先级都不应该让其他来源消失。

推荐行为：

1. 默认写入和安装：
   - Go Claude marketplace / sync / install 默认写 `~/.go-claude/skills-marketplace` 或 `~/.go-claude/skills`。
   - Go Claude 项目级新增 skills 默认写 `<project>/.go-claude/skills`。
   - 不默认写 `~/.claude/skills` 或 `<project>/.claude/skills`。
2. 默认读取：
   - 每次 discovery 都扫描 user + project 的 `.go-claude/skills`。
   - 每次 discovery 都扫描 user + project 的 `.claude/skills`。
   - 每次 discovery 都扫描 npx/marketplace/MCP/bundled/env roots，包括 `GOLANG_CLAUDE_CODE_SKILL_PATHS`、legacy `CLAUDE_SKILL_PATHS`、bundled skills 和 MCP skills。
3. 去重和冲突：
   - 以 skill name 作为主键去重。
   - Go Claude owned source 可以在同名冲突时优先，但不能阻止其他不同名 legacy skills 被加载。
   - status/doctor/skills list 要显示 source：`go-claude-user`、`go-claude-project`、`legacy-claude-user`、`legacy-claude-project`、`npx`、`marketplace`、`mcp`、`bundled`、`env`。
4. 软连接策略：
   - 可以支持 `<project>/.go-claude/skills -> <project>/.claude/skills` 或 `~/.go-claude/skills -> ~/.claude/skills` 这种用户显式创建的软连接。
   - Go Claude 不应默认自动创建指向 `.claude/skills` 的软连接，因为自动创建本身也是对 legacy Claude 生态的隐式耦合。
   - 如果要提供软连接命令，必须是显式命令，例如 `go-claude skills link-legacy --project`，并在输出中说明会共享同一批 skill 文件。

## `CLAUDE_CONFIG_DIR` 处理规则

`CLAUDE_CONFIG_DIR` 是原版 Claude Code 的配置根。Go Claude 不能再把它作为默认写入根。

推荐规则：

1. 新增统一函数 `identity.RuntimeLayoutFromEnv()` 或等价 API：
   - 写入根优先 `GOLANG_CLAUDE_CODE_CONFIG_DIR`
   - 否则 `~/<identity.ConfigDirName>`
   - 默认 `~/.go-claude`
2. `CLAUDE_CONFIG_DIR` 只允许进入 `LegacyReadRoots`。
3. 任何 store 的默认构造不能再读取 `CLAUDE_CONFIG_DIR` 作为写入 root。
4. 如必须支持 legacy 写入，必须显式参数，例如 `--legacy-claude-write` 或 `--target-root <path>`，并在输出中标注 `legacy write enabled`。

## 标识可配置化方案

当前 `internal/identity/identity.go` 已有默认值：

```go
const (
    defaultProductName      = "go-claude"
    defaultProductKey       = "go-claude"
    defaultConfigDirName    = ".go-claude"
    defaultGuidanceFilename = "go-e2e.md"
)
```

建议把这些默认值升级为一个中心静态配置，而不是各包各自拼字符串：

```go
var DefaultProductIdentity = identity.Settings{
    ProductName:      "go-claude",
    ProductKey:       "go-claude",
    ConfigDirName:    ".go-claude",
    GuidanceFilename: "go-e2e.md",
}
```

后续如果要改成 `go-code`，只改这一处：

```go
var DefaultProductIdentity = identity.Settings{
    ProductName:      "go-code",
    ProductKey:       "go-code",
    ConfigDirName:    ".go-code",
    GuidanceFilename: "go-code.md",
}
```

所有路径都从 identity 派生：

| 语义 | 默认路径 |
| --- | --- |
| Global settings | `~/<ConfigDirName>/settings.json` |
| Project settings | `<project>/<ConfigDirName>/settings.json` |
| Project local settings | `<project>/<ConfigDirName>/settings.local.json` |
| Guidance file | `<project>/<GuidanceFilename>` |
| Todos | `<project>/<ConfigDirName>/todos.json` |
| Plan mode | `<project>/<ConfigDirName>/plan_mode.json` |
| Worktrees | `<git-root>/<ConfigDirName>/worktrees/<slug>` |
| Goals | `~/<ConfigDirName>/goals/...` |
| Background | `~/<ConfigDirName>/background/...` |
| Scheduler | `~/<ConfigDirName>/scheduler/...` |
| Skills | `~/<ConfigDirName>/skills` and `<project>/<ConfigDirName>/skills` |
| Agents | `~/<ConfigDirName>/agents` and `<project>/<ConfigDirName>/agents` |
| Plugins | `~/<ConfigDirName>/plugins` and `<project>/<ConfigDirName>/plugins` |
| Output styles | `~/<ConfigDirName>/output-styles` and `<project>/<ConfigDirName>/output-styles` |
| Snapshots | `~/<ConfigDirName>/snapshots` |
| Debug logs | `~/<ConfigDirName>/debug/...` |

实现上应避免每个包重复写 `filepath.Join(home, ".go-claude", ...)`。建议提供一个小的 runtime layout API：

```go
type RuntimeLayout struct {
    Identity identity.Identity
    HomeRoot string
}

func DefaultRuntimeLayout(cwd string) RuntimeLayout
func (l RuntimeLayout) GlobalSettingsPath() string
func (l RuntimeLayout) ProjectSettingsPath(cwd string, local bool) string
func (l RuntimeLayout) ProjectStatePath(cwd string, rel ...string) string
func (l RuntimeLayout) GlobalStatePath(rel ...string) string
func (l RuntimeLayout) LegacyReadRoots(cwd string) []string
```

## 迁移方案

### Phase 1：禁止新写 `.claude`

最小高 ROI 改动：

1. 增加统一路径 API，先不大改业务逻辑。
2. 替换所有 P0 写入默认路径：
   - todo
   - plan mode
   - goal
   - background
   - scheduler
   - worktree
   - marketplace install
   - plugin install
   - feedback
   - snapshot
   - update check
   - TUI debug log
3. 保留 `.claude` 兼容读取；其中 `.claude/skills` 必须每次纳入聚合 discovery。
4. 增加 legacy settings materialization：只存在 `.claude/settings*.json` 时，新会话读取后创建 `.go-claude/settings*.json`，后续写入只落到 `.go-claude`。
5. 增加测试：默认运行时不会创建 `.claude` 文件。

### Phase 2：Go Claude 优先读取，skills 聚合加载

1. settings 已基本满足，继续锁定。
2. 给 memory、agents、plugins、output styles、workflow 增加 `.go-claude` 优先目录。
3. skills 改成 union discovery：`.go-claude/skills`、`.claude/skills`、npx/marketplace/MCP/bundled/env skills 全部扫描。
4. legacy `.claude` 对非 skills 资源只在 Go Claude 路径不存在或未命中时读取；对 skills 资源必须始终扫描。
5. status/doctor 输出 sources，并标注 `go-claude` / `legacy-claude` / `npx` / `marketplace` / `mcp` / `bundled` / `env`。

### Phase 3：迁移和导入工具

1. 增加只读扫描命令：列出 legacy `.claude` 中可迁移项。
2. 增加显式迁移命令：
   - `go-claude migrate legacy-config --dry-run`
   - `go-claude migrate legacy-config --apply`
3. 迁移采用 copy，不原地移动；保留原版 Claude Code 文件。
4. 对冲突文件采用 Go Claude 优先，不覆盖用户已有 `.go-claude` 文件，除非显式 `--force`。

## 验收标准

### 单元测试

- `auth login`、`config set`、`model set`、`permissions allow`、`hooks add` 默认只写 `~/.go-claude/settings.json`。
- permission prompt 选择 `project` / `local` 只写 `<project>/.go-claude/settings*.json`。
- 只有 `<project>/.claude/settings.json` 时，新会话可以读取 legacy 配置，但会创建 `<project>/.go-claude/settings.json`；后续 `config set --project` 和 permission project mutation 只写 `.go-claude/settings.json`。
- 只有 `<project>/.claude/settings.local.json` 时，新会话可以读取 legacy local 配置，但 local mutation 只写 `<project>/.go-claude/settings.local.json`。
- TodoWrite 只写 `<project>/.go-claude/todos.json`。
- PlanMode 只写 `<project>/.go-claude/plan_mode.json`。
- Goal/background/scheduler 默认 root 是 `~/.go-claude`，并忽略 `CLAUDE_CONFIG_DIR` 作为写入根。
- Agent worktree 默认创建在 `<git-root>/.go-claude/worktrees`。
- Marketplace/plugin/feedback/snapshot/update/debug log 默认写 `~/.go-claude`。
- Skills discovery 同时加载 `.go-claude/skills`、`.claude/skills`、npx/marketplace/MCP/bundled/env roots；同名冲突可按 Go Claude owned source 优先，不同名来源不能丢失。

### 集成检查

在隔离 HOME 下运行：

```bash
HOME="$(mktemp -d)" go run ./cmd/golang-cc init --settings --cwd /tmp/demo
find "$HOME/.claude" -maxdepth 4 -type f -print
find "$HOME/.go-claude" -maxdepth 4 -type f -print
```

期望：

- 默认流程不创建 `~/.claude`。
- 如果项目已有 `.claude/settings.json`，可以读取为 fallback，但 mutation 仍写 `.go-claude/settings.json`。
- 如果同时存在 `.claude/skills` 和 `.go-claude/skills`，两边 skills 都会进入 discovery 结果。
- 如果同时存在 `.claude` 和 `.go-claude` 的非 skills 资源，Go Claude 来源优先。

### 回归保护

增加一个 guard test，扫描生产代码中的硬编码写入目标：

- 允许出现 `.claude` 的读取 fallback、文档字符串、测试 fixture。
- 禁止生产代码默认写入 `.claude`。
- 禁止生产代码把 `CLAUDE_CONFIG_DIR` 作为默认写入 root。

## 非目标

- 不删除用户现有 `.claude` 文件。
- 不破坏 legacy Claude Code project memory 的读取兼容。
- 不要求原版 Claude Code 读取 Go Claude 的新状态文件。
- 不在本方案阶段实现迁移，只定义边界和改造顺序。

## 推荐优先级

1. P0 写入隔离：todo、plan mode、goal/background/scheduler、worktree、install/feedback/snapshot/update/log。
2. identity/runtime layout API：所有路径从中心静态默认值派生。
3. P2 读取优先级：非 skills 资源 `.go-claude` 优先、`.claude` fallback；skills 资源必须聚合加载所有来源。
4. 迁移命令和可观测输出。
5. 清理文档和配置模板中的旧默认路径。
