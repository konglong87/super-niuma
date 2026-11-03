# 2026-07-25 全项目能力审计与修复 backlog

本文档是 2026-07-25 对 go-claude 做的一次**独立全量审计**结果。审计分 6 个维度并行执行（provider 层、
服务端生产就绪度、工程基建、agent loop 与上下文治理、工具层与安全边界、界面层与文档真实性），
所有结论均带 `file:line` 证据；其中安全类前三条由主审计者二次复核代码确认。

本文件同时是后续修复的**唯一执行清单**：每个条目有稳定 ID、验收标准和测试命令。修复一项即在此更新状态，
并按 [AGENTS.md](../../AGENTS.md) 要求同步 [todo.md](../todo.md)。

## 状态约定

| 状态 | 含义 |
| --- | --- |
| TODO | 尚未开始。 |
| IN_PROGRESS | 正在开发。 |
| PARTIAL | 该条目包含多个独立缺陷，其中一部分已闭合；条目正文标注哪些已做、哪些仍待做。 |
| DONE | 已实现、已测试、已提交。 |
| WONTFIX | 评估后明确不做，需写明理由。 |

---

## 0. 总体判断

**代码实现质量显著高于工程基建与产品化程度。**

审计期实测基线（Go 1.26.4）：

| 检查 | 结果 |
| --- | --- |
| `go build ./...` | 通过，无输出 |
| `go vet ./...` | 通过，无输出 |
| `go test ./... -count=1` | **69 包全 ok，0 FAIL**（约 8 分钟） |
| `gofmt -l .` | 干净 |
| `go test -race`（session / permissions / tools/task） | 全部 ok |

核心包覆盖率：`permissions` 86.5%、`tools/task` 86.0%、`agentruntime` 82.6%、`query` 82.5%、
`tui` 82.5%、`session` 80.6%、`server` 74.0%。

文档诚信度核验（这是最硬的指标）：

| 核验项 | 结果 |
| --- | --- |
| `compatibility_matrix.md` 引用的 21 个测试函数名 | 21/21 真实存在 |
| 引用的 11 个 acceptance 脚本 | 11/11 真实存在 |
| 抽查 6 条 DONE 条目的代码 + 测试实跑 | 6/6 通过 |
| `internal/cli` 中的 stub 标记 | 0 处（所有命令都有真实 handler） |
| 全仓 TODO/FIXME/未实现标记 | 7 处，且均为诚实的能力声明 |

**没有系统性文档造假。** 真正的问题是三个结构性断层：

1. **安全边界有真实可利用的漏洞**（权限模式折叠、deny 子树失效、危险命令分类器可绕过）。
2. **运行时外壳仍是"本地顺手起个 server"**（shutdown 失效、无超时、无连接池、只能单实例）。
3. **零 CI 让所有质量成果无人固化**（5 个已调用 CVE、swagger 漂移 19 个 commit、80 个验收脚本已死）。
   —— CI 与 5 个已调用 CVE 已于 2026-07-25 闭合（[§3 完成记录](#完成记录audit-p0-16--p0-17--p0-202026-07-25)）；
   swagger 漂移（AUDIT-P0-18）和死脚本（AUDIT-P0-19）仍未处理。

---

## 1. P0 · 安全正确性

用户按文档正常操作即会丧失防护，优先级最高。

| ID | 状态 | 模块 | 问题 | 证据 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| AUDIT-P0-01 | DONE | Permissions | **`--permission-mode acceptEdits` / `default` / `auto` / `delegate` 全部被 `NormalizeMode` 折叠成 `allow`，进而在 `guarded.go` 置 `policy.Bypass=true`；而 `CheckRequest` 的 `if p.Bypass` 短路在 Deny 检查之前** —— deny 列表、alwaysAsk、危险命令分类器、敏感路径检查全部不执行。CLI help 文本却把 `acceptEdits` 与 `bypassPermissions` 列为两个不同模式，用户以为只是"自动接受文件编辑" | `internal/permissions/policy.go:215`（NormalizeMode）、`internal/tools/guarded.go:66`（Bypass 赋值 + 塞 `"*"` 进 allow）、`internal/permissions/policy.go:61`（Bypass 短路先于 Deny） | `acceptEdits` 映射到独立模式，仅对 Write/Edit/MultiEdit/NotebookEdit 自动放行，Bash 仍走完整策略；`Bypass` 只能由显式 `bypassPermissions` / `--dangerously-skip-permissions` 置位；Deny 检查移到 Bypass 短路**之前**。补测试覆盖"Bypass 模式下 deny 规则仍生效"和"acceptEdits 不放行 Bash" |
| AUDIT-P0-02 | DONE | Permissions | **deny 子树规则对 Write/Edit/Bash 静默失效**。`isPathQualifierTool` 只含 `LS/Read/Glob/Grep`，其余工具落回 `path.Match`，而 Go 的 `*` 不跨 `/`：`Write(~/.ssh/**)` 挡得住 `~/.ssh/x`，挡不住 `~/.ssh/keys/id_rsa`。静默失效的 deny 比没有 deny 更危险 | `internal/permissions/policy.go:281`、`internal/permissions/policy.go:377`（isPathQualifierTool 名单） | `isPathQualifierTool` 扩展到 Write/Edit/MultiEdit/NotebookEdit/Bash；补 `/**` 深层路径的 deny 金标测试 |
| AUDIT-P0-03 | DONE | Permissions | **危险命令分类器 17 例实测 16 例绕过**。根因：所有 pattern 锚定 `(^\|[;&\|]\s*)`，而 `normalizeCommandForRisk` 用 `strings.Fields`+`Join(" ")` 把换行压成空格 —— 多行脚本第二行起永远匹配不到锚点，而这正是模型最自然的输出形式。实测绕过：换行分隔、子 shell `(...)`、花括号组、`if/for` 块、`nohup`/`env`/`doas` 前缀、`\rm`、`/bin/rm`、长选项 `--recursive --force`、`ksh -c`、heredoc。另外两套 pattern 不一致：`policy.go` 有 `-r\s+-f` 分支，`security.go` 没有，`rm -r -f /` 能穿过硬拒绝层 | `internal/permissions/policy.go:340-372`、`internal/permissions/policy.go:390-393`（normalizeCommandForRisk）、`internal/tools/bash/security.go:9-20` | 分类器改用仓库已依赖的 `mvdan.cc/sh` AST（`internal/sandbox/command.go` 已在用该路径），废弃锚定正则；复用并补全 `unwrapCommand` 的前缀剥离（补 `nohup/timeout/xargs/nice/setsid/stdbuf/doas`）；上述 17 例全部写成表驱动测试 |
| AUDIT-P0-04 | DONE | Sandbox | **macOS 沙箱完全没有网络隔离**。生成的 seatbelt profile 唯一 deny 原语是 `(deny file-write* (subpath "/"))`，`cfg.NetworkDisabled` 在 darwin 分支从未被引用。实际唯一执行者是 `CheckShellNetworkPolicy` —— 一个 20 个命令名的白名单，`python3 -c` / `node -e` / `$(echo curl)` / 任意编译二进制直接穿透。（写路径隔离经实测确认真实有效） | `internal/sandbox/runtime.go:357-397`、`internal/sandbox/command.go:18-58` | macOS profile 在 `NetworkDisabled` 时补 `(deny network*)`；若平台不支持，`UnavailableReason` 必须显式报告"网络隔离不可用"，绝不让配置项静默失效 |
| AUDIT-P0-05 | DONE | Sandbox/Tools | **沙箱默认关闭时，默认写保护同时失效**。`defaultSandboxDenyWritePaths()` 被 `if sandbox.Enabled` 包住，而沙箱默认 `false` —— 即默认状态下 `.claude/settings.json`、`.claude/settings.local.json`、`.claude/skills`、`.git/hooks`、`.git/config` 均可写。agent（或经 prompt injection 的 agent）可直接给自己加 allow 规则或写 git hook 持久化 | `internal/tools/path.go:51-53`、`internal/cli/cli.go:840`（`Enabled: boolValue(settings.Enabled, false)`） | 默认写保护与沙箱开关解耦：无论沙箱是否开启，`.claude/settings*.json` 与 `.git/hooks` 一律拒绝写入 |
| AUDIT-P0-06 | PARTIAL | Secrets | 仓库内 `config/config.local.yaml` 含 4 处明文凭据，权限 `0644`。缓解项：已在 `.gitignore:7`，`git log --all` 确认从未提交。但同机任意用户/进程可读，且它在 config loader 搜索路径上。对比代码对 `settings.json` 是严格 `0600` 原子写 —— 该文件是个例外 | `config/config.local.yaml`、`internal/config/config.go:868`（搜索路径）、`internal/config/config.go:648`（settings.json 的 0600 对照） | 轮换该 key；`chmod 600`；在 config loader 中对本地凭据文件做权限检查并告警 |

### 修复证据 · AUDIT-P0-01 / 02 / 05（2026-07-25）

三条同属权限/沙箱边界，改动区域重叠，一次提交完成。

**AUDIT-P0-01 · 权限模式拆分 + Deny 前置**

- `NormalizeMode` 不再折叠：新增 canonical 常量 `ModeAllow` / `ModeAsk` / `ModeDeny` /
  `ModeAcceptEdits` / `ModeBypassPermissions`（[policy.go](../../internal/permissions/policy.go)）。
  `acceptEdits` / `accept-edits` → `acceptEdits`；`bypassPermissions` / `bypass-permissions` /
  `bypass` → `bypassPermissions`。
- **`default` / `delegate` 语义重新确认为 `ask`**（不再等于 `allow`）：上游 `default` 的语义是
  「每个工具首次使用时提示」，`delegate` 的语义是「把决定交给用户」，两者都是 ask 流程。
  **`auto` 保留 `allow`**：它是本项目自有模式，含义是「自动执行，危险命令仍由分类器拦」，
  从来不是 bypass，且 `Policy.AutoMode` 标志继续区分它。
- `CheckRequest` 的 Deny 检查移到 `if p.Bypass` 短路**之前** —— 即便 bypass，显式 deny 仍生效。
- `acceptEdits` 只对 `Write/Edit/MultiEdit/NotebookEdit`（`IsFileEditTool`）走宽松分支，
  其余工具（Bash / PowerShell / MCP）全部走 ask 流程；即便是文件编辑，敏感路径仍由分类器拦。
- `Bypass` 只由显式 bypass 置位：`ModeGrantsBypass` 是唯一判定入口，
  `guarded.go` 的 runtime 分支不再用 `mode == "allow"` 推导 bypass；
  `--permission-mode bypassPermissions` 在 `cli.go` 显式置 `Permissions.Bypass`。
- `--dangerously-skip-permissions` 现在**保留** settings 里的 deny 列表（原先整体清空，
  会让 Deny 前置失去意义），TODO-014 的「高风险 Bash 不触发 prompt」行为不变。
- CLI help 与 `help.txt` 金标同步说明两个模式的真实区别；`agents.go` 的 permissionMode
  校验补上 `acceptEdits`。

**AUDIT-P0-02 · deny 子树规则**

`isPathQualifierTool` 扩展到 `Write/Edit/MultiEdit/NotebookRead/NotebookEdit/Bash/PowerShell`
（notebook 与 shell 成对补齐，避免留下同类静默失效）。

**AUDIT-P0-05 · 默认写保护与沙箱解耦**

`EnsureWritablePathWithSandbox` 拆成两层（[path.go](../../internal/tools/path.go)）：
`defaultDenyWritePaths()`（`.claude/settings*.json`、`.go-claude/settings*.json`、
`.git/hooks`、`.git/config`）**无条件**拒绝 —— `.git/config` 必须同列，否则可用
`core.hooksPath` 绕开 `.git/hooks` 保护；`sandboxOnlyDenyWritePaths()`（`.claude/skills`）
维持仅沙箱开启时拦截，让默认配置下的 skill 编写仍然可用。

**测试**（新增用例均已验证「移除修复后失败、加上修复后通过」）

| 测试 | 覆盖 |
| --- | --- |
| `TestPolicyBypassStillHonoursDenyRules` | bypass 模式下 deny 规则（含 `/**` 子树）仍生效 |
| `TestPolicyAcceptEditsOnlyReleasesFileEdits` | acceptEdits 放行 4 个编辑工具、不放行 Bash/PowerShell/MCP、敏感路径写入仍提示 |
| `TestPolicyDenySubtreeMatchesDeepPaths` | 10 组 `/**` 深层路径金标 + 同前缀兄弟目录不误伤 |
| `TestModeGrantsBypass` | 仅 bypassPermissions 家族返回 true |
| `TestNormalizeOriginalPermissionModes` | 新 canonical 映射表 |
| `TestEnsureWritablePathDefaultProtectionWithSandboxDisabled` | 沙箱关闭时 6 个默认保护路径全部拒绝，普通路径与 skill 仍可写 |
| `TestGuardRuntimeAcceptEditsDoesNotBypassBash` | runtime acceptEdits 不越过 deny 子树、不放行危险 Bash |
| `TestGuardBypassScope` | TODO-014 不倒退（bypass 不提示）+ session deny 仍生效 + 收窄模式撤销 bypass |
| `TestRuntimePermissionAndDirectoryOptions` | `--permission-mode acceptEdits/bypassPermissions` 的 settings 落地 |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/permissions ./internal/tools ./internal/cli ./internal/query -count=1` | 4/4 ok |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

### 修复证据 · AUDIT-P0-03 / 04（2026-07-25）

**AUDIT-P0-03 · 危险命令分类器改 AST**

根因确认（修复前实测 18 例，16 例绕过）：所有 pattern 锚定 `(^|[;&|]\s*)`，而
`normalizeCommandForRisk` 用 `strings.Fields`+`Join(" ")` 把换行压成空格，多行脚本第二行起
永远匹配不到锚点。

- 新增 [internal/shellcmd](../../internal/shellcmd/shellcmd.go)：用仓库已依赖的 `mvdan.cc/sh`
  把脚本摊平成「实际会执行的 simple command」列表。AST 天然覆盖换行、子 shell `(...)`、
  花括号组、`if/for/while/case`、函数体、命令替换 `$(...)`；额外递归展开 `bash -c` payload
  和喂给 shell 的 heredoc（深度上限 4）。程序名归一化为 base name 并去掉反斜杠转义，
  于是 `rm` / `/bin/rm` / `\rm` / `"rm"` 收敛到同一个名字。
- **`UnwrapCommand` 前缀剥离补全并复用**：除原有 `sudo`/`env`/`command`/`builtin`/`noglob`，
  补 `doas`/`nohup`/`setsid`/`timeout`/`nice`/`ionice`/`stdbuf`/`xargs`/`time`/`exec`/
  `eatmydata`，并正确跳过带值的 flag（`sudo -u root rm -rf /` 现在解析到 `rm` 而不是 `root`）。
  `internal/sandbox/command.go` 的本地 `unwrapCommand` 改为委托 `shellcmd.UnwrapCommand`，
  沙箱的 mutating-path 检查同步获得这些前缀的覆盖。
- **两套 pattern 的不一致从结构上消除**：新增 [shellrisk.go](../../internal/permissions/shellrisk.go)
  的单一规则表，每条规则带 `id`/`reason`/`deny`。`ClassifyRequestRisk`（提示层）与新增的
  `HardDenyShellReason`（Bash 硬拒绝层）读同一张表，`internal/tools/bash/security.go` 从
  9 条独立正则改为薄封装。原先 `rm -r -f /` 在提示层算 destructive、在硬拒绝层漏网的
  漂移不再可能发生。
- 规则改为**结构化判定**而非文本匹配：`rm` 的递归/强制 flag 同时认 `-rf`、`-r -f`、`-Rf`、
  `--recursive --force`；`"$HOME"` 解析为 `$HOME`；`curl … | sh` 通过 pipeline 连边识别，
  `bash <(curl …)` 通过 process substitution 连边识别。
- **解析失败不放行**：`shellScriptCommands` 在 `mvdan.cc/sh` 报错时回退到按 shell 分隔符切片 +
  跳过控制流关键字后跑同一套规则，避免出现「构造一个解析器不接受但真 shell 接受的命令」
  这一类新绕过。
- PowerShell 仍走正则（`mvdan.cc/sh` 不解析 PowerShell），但 `normalizePowerShellForRisk`
  先把换行/回车转成 `;` 再压空白，锚点对每条语句重新生效。
- 顺带删除死代码 `isDangerousShellCommand`（无任何调用方，且位于被重写的函数中）。

**AUDIT-P0-04 · macOS 沙箱网络隔离**

- `macOSSandboxProfile` 在 `NetworkDisabled` 时输出 `(deny network*)`
  （[runtime.go](../../internal/sandbox/runtime.go)）。**已用真实 `sandbox-exec` 验证**：
  同一个 loopback listener，未沙箱连接成功、沙箱内连接被拒（`Operation not permitted` / `nc` 非零退出）；
  移除该规则后沙箱内连接恢复成功，证明这条规则确实是唯一执行者。同时确认
  `ls`/`git`/`go`/`python3` 等本地工具在 `(deny network*)` 下不受影响。
- 新增 `NetworkPolicyGaps(cfg)`：显式列出**本平台无法对任意 shell 命令强制**的网络配置项 ——
  非 darwin/linux 平台的 `network.disabled`、`network.allowDomains/denyDomains`
  （只作用于内置 HTTP 工具，shell 命令不过滤）、`network.proxy.required`/`mitm.required`
  （只对已知命令名生效，编译好的二进制或 `python3 -c` 可绕过）。
  `UnavailableReason` 汇总上报；`PrepareShell` 在 `sandbox.failIfUnavailable` 为真时直接
  报错拒绝启动，与既有三处「sandbox.enabled 但 X 不可用」错误保持同一契约。
- **遗留问题（已登记，未在本次修复）**：`IsAvailable` 与 `UnavailableReason` 全仓**零调用方**，
  用户可见的沙箱状态只有 `tuiSandboxLabel`，且它直接读 settings、不反映真实可执行性。
  即本条的「显式上报」目前只到 API 层。已登记为 AUDIT-P1-35。

**测试**（新增用例均已验证「移除修复后失败、加上修复后通过」）

| 测试 | 覆盖 |
| --- | --- |
| `TestBashClassifierCatchesAuditBypasses` | 24 行金标表：换行/空行/子 shell/花括号/if/for/while/命令替换/heredoc、`\rm`、`/bin/rm`、`"rm"`、`nohup`/`env`/`doas`/`timeout`/`nice setsid`/`xargs` 前缀、`--recursive --force`/`-r -f`/`-Rf`/`"$HOME"`、`ksh -c`/`dash -c` |
| `TestBashClassifierRuleCoverage` | 33 条规则逐条可达，reason 字符串稳定 |
| `TestBashClassifierLeavesOrdinaryCommandsAlone` | 24 条日常命令（`rm -rf build/`、`git push origin main`、`chmod -R 755 static`、`dd if=/dev/zero of=/tmp/blob` 等）不误报 |
| `TestHardDenyMatchesPromptLayer` | 硬拒绝层 ⊆ 提示层；`rm -r -f /` 等 16 例两层一致；`bash -c`/`npx`/`nc -l` 只提示不拒绝 |
| `TestBashClassifierHandlesUnparseableInput` | 解析失败仍分类 |
| `TestParseSeesCommandsRegexAnchorsMiss` 等 9 个 shellcmd 测试 | AST 摊平、程序名归一化、pipeline/procsubst 连边、redirect 归属、展开保真、递归有界 |
| `TestMacOSProfileDeniesNetworkWhenDisabled` / `...LeavesNetworkAloneWhenNotDisabled` | profile 只在 `NetworkDisabled` 时含 `(deny network*)` |
| `TestMacOSSandboxActuallyBlocksOutboundConnect` | 真跑 `sandbox-exec` + loopback listener 的端到端拦截 |
| `TestNetworkPolicyGapsReportsUnenforceableOptions` | 不可强制项被上报，`UnavailableReason` 汇总 |
| `TestPrepareShellRefusesUnenforceableNetworkPolicyWhenStrict` | strict 模式拒绝启动；非 strict 保持 best-effort |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/permissions ./internal/tools ./internal/tools/bash ./internal/sandbox ./internal/shellcmd ./internal/cli ./internal/query -count=1` | 7/7 ok |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go test -race ./internal/permissions ./internal/shellcmd ./internal/sandbox ./internal/tools/bash -count=1` | 4/4 ok |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

---

## 2. P0 · 稳定性

会挂进程、会打爆依赖、会烧钱。

| ID | 状态 | 模块 | 问题 | 证据 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| AUDIT-P0-07 | DONE | Provider | **LLM HTTP client 没有任何 `Timeout`，连接池用默认值**（`http.DefaultTransport`，`MaxIdleConnsPerHost`=2）。一个 hang 住的网关能永久挂死会话（CLI 主循环不设 deadline）；并发子代理场景反复 TCP+TLS 握手。对照组：同仓 `websearch`、`mcp/http_rpc` 都设了 timeout。讽刺的是 DNS/connect/TLS/write/TTFB 全链路遥测埋得极完整 —— **能精确测量自己怎么挂的，但挂了不会自愈** | `internal/anthropic/client.go:697-702`（裸 `&http.Client{}`）、`internal/anthropic/client.go:97`、对照 `internal/tools/websearch/websearch.go:32` | client 设置总超时 + 自定义 Transport（`MaxIdleConnsPerHost`、`IdleConnTimeout`、`TLSHandshakeTimeout`）；补 stream 空闲/停滞检测 |
| AUDIT-P0-08 | DONE | Compact | **auto-compact 默认关闭，且没有任何 overflow 反应式兜底**（全仓 grep `context_length_exceeded` / `prompt is too long` 零命中），加上**只有 CLI 路径接了 compactor** —— server / 子代理 / goal / scheduler 长会话零溢出保护，估算偏低就直接 API 硬失败终止 | `internal/compact/settings.go:8`（需显式 `*Enabled`）、`config/config.yaml:34`、唯一 wiring `internal/cli/cli.go:781`；`internal/agentruntime/runtime.go`、`internal/server/runtime.go`、`internal/goal` 均无 Compactor | auto-compact 默认开启；新增 overflow 错误识别 → 强制压缩后重试一次的降级路径；server / agentruntime / goal 三条路径接入 compactor |
| AUDIT-P0-09 | DONE | Server | **graceful shutdown 三重失效**：①`srv.Serve` 在 `Shutdown` 关闭 listener 的瞬间返回 `ErrServerClosed`，`Run` 立刻 `return nil` → 进程退出，后台 goroutine 的 `Shutdown` 等待被直接丢弃，在途请求/SSE 一律硬断；②`main` 只捕 `os.Interrupt`，**不捕 SIGTERM**，systemd/k8s/docker 默认信号下上述代码根本不执行；③`Shutdown(context.Background())` 无 deadline，只要有一条 SSE 打开就永久 hang | `internal/server/server.go:237-246`、`cmd/golang-cc/main.go:25` | 三点必须一起改：`Serve` 返回后等待 shutdown 完成；`NotifyContext` 加 `syscall.SIGTERM`；`Shutdown` 带 deadline 并对 SSE 主动发终止事件 |
| AUDIT-P0-10 | DONE | Server | **detached agent runner 无 `recover()`** → 一次 panic 打死整个进程。全仓仅 1 处 `recover()`（`server.go:1067`，且是 re-panic 交给 gin）。且 runner 用 `context.WithoutCancel` 脱离 server 生命周期，进程退出后任务永久停在 `running`，**全仓无任何 stale/reaper 回收逻辑**。同类裸 goroutine：`mobile.go:1432` 异步标题生成 | `internal/server/handlers_agent_tasks.go:402-416`、`internal/server/mobile.go:1432` | 所有后台 goroutine 加 `recover()` 并记录 telemetry；新增启动时 stale task reaper（超过 `agentTaskRunTimeout` 仍 `running` 的置 failed）；给 detached runner 加并发上限 |
| AUDIT-P0-11 | PARTIAL | Storage | ~~**DB 连接池全默认**~~（四参数已可配且有默认，见 [修复证据](#修复证据--audit-p0-11--p0-12--p1-24--p1-262026-07-26)；**压测证据仍缺**，本机无 MySQL 也无 docker，转 [TODO-071](../todo.md)）**DB 连接池全默认**（`SetMaxOpenConns` / `SetMaxIdleConns` / `SetConnMaxLifetime` 全仓零命中）→ MaxOpenConns 无上限打爆 MySQL `max_connections`、MaxIdleConns=2 反复建连、ConnMaxLifetime 无限拿到被 `wait_timeout` 踢掉的死连接。与下一条叠加尤其危险 | `internal/storage/mysql/gorm_repository.go:365` | 连接池四参数可配置且有合理默认；补压测证据 |
| AUDIT-P0-12 | DONE | Server | ~~**agent task SSE 每流每秒 8 次 DB 查询**~~（DONE，见 [修复证据](#修复证据--audit-p0-11--p0-12--p1-24--p1-262026-07-26)：空转指数退避到 2s 上限 + 有事件的那一轮不再多打状态查询，10 秒空转从 80 次查询降到 ≤20）**agent task SSE 每流每秒 8 次 DB 查询**（250ms ticker × 2 条查询）。100 条并发流 = 800 QPS 常驻 | `internal/server/handlers_agent_tasks.go:291-322` | 改为事件驱动或指数退避轮询；补并发流下的 QPS 上界测试 |
| AUDIT-P0-13 | DONE | Server | **请求体无大小限制**：`/query` 直接 `json.NewDecoder(r.Body).Decode`，mobile 中间件 `io.ReadAll` 无上限并整体缓存进内存。全仓 `http.MaxBytesReader` 零命中；`http.Server` 的 `ReadTimeout`/`WriteTimeout`/`IdleTimeout`/`ReadHeaderTimeout` 也全未设（Slowloris 敞开） | `internal/server/server.go:364`、`internal/server/mobile_middleware.go:96-106`、`internal/server/server.go:237` | 全局 `MaxBytesReader` + `http.Server` 四个超时；mobile body 日志改流式或限长 |
| AUDIT-P0-14 | DONE | Subagent | **子代理递归深度无限 + batch 并发无钳制 + 后台 agent 数量无上限 + 无累计 token/成本预算**。`general-purpose` 是 `Tools:["*"]`；`Explore`/`Plan` 只 deny 了 `Agent`，**没 deny `Task`/`AgentCreate`** → 可无限套娃。`max_concurrency: 500` 会照单全收。一次错误的批量委派可以耗尽机器 | `internal/agents/builtin.go:107,115-121,131-137`、`internal/cli/cli.go:673-682`、`internal/tools/task/task.go:467`、`internal/agentruntime/runtime.go:149` | 引入递归深度计数并设上限；`max_concurrency` clamp；后台 agent 数量上限；会话级累计 token/成本预算与熔断 |
| AUDIT-P0-15 | DONE | Cost | **cache token 按满额 input 单价计费**：`inputTokens = Input + CacheCreation + CacheRead` 后乘满额单价，而真实计价 cache read=0.1×、cache write(5m)=1.25×、cache write(1h)=2× —— 重缓存会话成本**高估近 10 倍**。另：未知模型静默返回成本 0，无告警；价目表用 `strings.Contains` 子串匹配，第三方网关取名 `my-sonnet-4-proxy` 会误命中官方价 | `internal/session/store.go:1896`+`:1915`、`internal/session/store.go:1963-1974`、`internal/query/query.go:2709` | 分档计价（input / cache_write_5m / cache_write_1h / cache_read / output）；未知模型显式告警而非静默 0；子串匹配改精确前缀或配置优先 |

### 修复证据 · AUDIT-P0-10（2026-07-25）

三个缺陷同属 detached agent runner 的健壮性，一次提交完成。

**① 后台 goroutine 的 panic 兜底**

- 新增 [background.go](../../internal/server/background.go)：`goSafe` / `recoverBackgroundPanic`
  把 panic 收敛成 `server.background.panic` 遥测事件（`CategorySystem` + `StatusError`），
  properties 只放标识性字段（`goroutine`、`stack`、调用方给的 ID），**不放 prompt 正文、
  响应正文或 token**；`telemetry.Emit` 的 `SanitizeProperties` 是二道防线而非唯一防线。
  stack 截断到 4000 字符——它只含函数名与 `file:line`，不含请求数据。
- `handlers_agent_tasks.go` 的 detached runner 用专门的 `recoverAgentTaskRun`：除记录遥测外
  **还把任务显式置 failed**（panic 后任务同样会永久停在 `running`），落库走
  `context.WithoutCancel` 副本，避免 runCtx 已超时导致写不进去。
- `mobile.go:1432` 的异步标题生成改用 `goSafe`；顺带把 `context.Background()` 换成
  `context.WithoutCancel(ctx)` —— 前者会丢掉请求作用域的 telemetry emitter，panic 记录
  落不进租户遥测表。
- `agentTaskTextSink.startIdleWatchdog` 的看门狗 goroutine 同样纳入 `goSafe`。
- 全仓 `go func(` 复查（排除 `_test.go`）：本条已覆盖 `internal/server` 下所有脱离请求的
  goroutine。**仍裸奔但不属本条范围**的 5 处，留给对应 owner：
  `internal/tools/task/task.go:282,488`（子代理执行，AUDIT-P0-14；**仍未处理**）、
  `internal/agentruntime/runtime.go:149`（AUDIT-P0-14；**已随该条补 `recover()`**）、
  `internal/scheduler/scheduler.go:703`、`internal/skills/watch.go:35`、
  `internal/cli/cli.go:4391`、`internal/tools/bash/bash.go:289`；
  `internal/server/server.go:238` 属 AUDIT-P0-09（graceful shutdown）。

**② stale task reaper**

- 新增 [agent_task_reaper.go](../../internal/server/agent_task_reaper.go)：启动时先扫一轮
  （回收上次进程留下的孤儿），之后按 `clamp(runTimeout/4, 1min, 10min)` 周期巡检。
- **多租户边界**：`StaleAgentTaskStore.ListStaleRunningAgentTasks` 跨租户扫描，但每行都带
  `tenant_id` / `user_id`；写回 `FailStaleAgentTask` 的 WHERE 同时锁定
  `tenant_id AND user_id AND id AND status = 'running'`。reaper 自己从不构造租户上下文，
  只把读到的那一行的归属传回去，因此不可能跨租户改数据。
  `status = 'running'` 这一项还兼作 compare-and-set：真正的 runner 抢先收尾时返回
  `ErrNotFound`，reaper 不覆盖它的结果。
- **失败原因不伪装成正常完成**：`result_json` 写
  `source=stale_task_reaper` / `status=failed` / `stale=true` / `reason=runner_lost` +
  「process restart or exceeded run timeout」原文，并同步 append 一条 `failed` 事件。
- **生命周期**：`startAgentTaskReaper(ctx, opts)` 返回的 stop 函数等 goroutine 真正退出；
  `server.go` 只加 1 行 `defer startAgentTaskReaper(ctx, opts)()`。reaper 完全由 ctx 取消
  驱动，不引入新的「进程退不掉」来源。

**③ detached runner 并发上限**

- 新增 [agent_task_concurrency.go](../../internal/server/agent_task_concurrency.go)：
  `agentTaskRunLimiter` 每 handler 一个，默认 16，可用
  `GOLANG_CLAUDE_CODE_AGENT_TASK_MAX_CONCURRENT_RUNS` 覆盖。
- **超限即拒**，不排队（排队等于把压力转成内存占用）：返回 `503` +
  `Retry-After: 5` + 带具体上限的错误文案，并记 `agent.run.rejected` 遥测。
- 容量检查放在**改 status 之前**：被拒的任务原样留在 `ready` 可重试，
  不会留下一个没人跑的 `running` 任务。槽位在 goroutine 退出时释放，
  提前 return 的路径由 `handedOff` 标志归还。

**测试**（4 处新增用例均已用 mutation check 验证「移除修复后失败」）

| 测试 | 覆盖 | mutation check |
| --- | --- | --- |
| `TestTenantAgentTaskMessagePanicDoesNotKillProcess` | runner panic 被 recover、任务置 failed、遥测有 stack、properties 不含敏感键 | 去掉 `recoverAgentTaskRun` → 测试进程直接 panic 退出 |
| `TestTenantAgentTaskMessageRejectsBeyondConcurrencyLimit` | 超限返回 503 + Retry-After + 上限文案；被拒任务仍是 `ready`；槽位释放后可重试 | 去掉上限判断 → 第二次请求返回 200 |
| `TestAgentTaskReaperFailsStaleTasksWithinTenantBoundary` | 两租户孤儿被置 failed；窗口内 / 终态任务不动；每次写回带本行 tenant/user | 传错 user_id → `reaped = 0` |
| `TestGormRepositoryFailStaleAgentTaskIsTenantScoped` | SQL 层 WHERE 带 tenant/user/id/status；RowsAffected=0 → `ErrNotFound` | — |
| `TestAgentTaskReaperSkipsTasksFinishedByTheRunner` | runner 抢先收尾时不覆盖、不写事件 | — |
| `TestStartAgentTaskReaperStopsOnContextCancel` | ctx 取消后 stop 不阻塞；无 store 时为 no-op | — |
| `TestGoSafeRecoversAndRecordsPanic` / `TestAgentTaskRunLimiterReleaseIsIdempotent` / `TestAgentTaskReaperIntervalClamped` / `TestGormRepositoryListStaleRunningAgentTasks` / `TestAgentTaskReaperSurvivesListErrors` | 各组件单元行为 | — |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/server ./internal/agenttasks ./internal/storage/mysql ./internal/cli -count=1` | 4/4 ok |
| `go test -race ./internal/server ./internal/agenttasks ./internal/storage/mysql -count=1` | 3/3 ok，无 race |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

**合并前验收补丁：reaper 的 `stop()` 在父 ctx 存活时会死锁**

合并 review 发现并修掉的一处缺陷，作为独立 commit 跟在上面那次提交之后。

- 原实现里 reaper 直接跑在父 `ctx` 上，`stop` 是 `func() { <-done }`，而 `run` 只在
  `ctx.Done()` 时返回。`Run` 里这是 `defer startAgentTaskReaper(ctx, opts)()` ——
  **`Run` 有不经过 ctx 取消的返回路径**（`Serve` 因 listener 错误返回时父 ctx 仍然活着），
  那条路径上 `stop` 永远等不到 `done`，`Run` 永久挂死：进程既不报错也不退出。
- 已有的 `TestStartAgentTaskReaperStopsOnContextCancel` 只覆盖「ctx 取消后能停」，
  恰好落在这个缺口外，所以两边测试都是绿的。
- 修法：reaper 跑在 `context.WithCancel(ctx)` 派生的 context 上，`stop` 先 `cancel()`
  再 `<-done`，无论父 ctx 什么状态都能收干净。
- 新增 `TestStartAgentTaskReaperStopReturnsWithoutParentCancel`，**已验证修复前失败**
  （`stop blocked while the parent context was still live`，卡满 2s 超时）、修复后通过；
  原 `TestStartAgentTaskReaperStopsOnContextCancel` 仍通过。
- 这条与 AUDIT-P0-09（graceful shutdown 重写 `Run` 的返回路径）直接相关：每给 `Run`
  多加一条提前返回路径，这个 `<-done` 就多一处挂死机会，两条分支各自测试都绿、合到
  一起才炸。合并 AUDIT-P0-09 时必须复核 reaper 的 `stop` 仍被调用且不阻塞。

**把「`Run` 启动了 reaper」固化成不变量**

reaper 的启动在 `server.go` 里只是一行 `defer startAgentTaskReaper(ctx, opts)()`，而所有
reaper 测试都直接调 `startAgentTaskReaper` —— **删掉那一行不会让任何测试变红**。
AUDIT-P0-09 恰好要重写紧挨着的那几行（`Run` 改为委托 `serveHTTPLifecycle`），
解冲突时整块取一边就会把 wiring 静默弄丢。

新增 `TestRunStartsAgentTaskReaper`：真起一次 `Run`（`go test` 下 executable 以 `.test`
结尾，`schedulerDaemonDisabled` 为真，不会 fork scheduler daemon），播一条 stale running
任务，断言它被 reaper 收成 failed，再 cancel 并断言 `Run` 干净返回。**已验证移除 wiring
行后失败**（`Run did not start the stale task reaper`）、还原后通过。

### 修复证据 · AUDIT-P0-07 / 09 / 13（2026-07-25）

三条同属「无超时 / 不优雅退出」，改动区域相邻，一次提交完成。

**AUDIT-P0-07 · provider HTTP client 超时与连接池**

新增 [internal/anthropic/httpclient.go](../../internal/anthropic/httpclient.go)。刻意**没有**用
`http.Client.Timeout` —— 它是覆盖「连接 + 请求 + 读完 body」的硬 deadline，会把一次几分钟的
长流式回答直接砍断。超时语义拆成三段，全部由 `httpTraceClient.Do` 按响应类型切换：

| 段 | 默认值 | 覆盖什么 |
| --- | --- | --- |
| 建流 `providerResponseHeaderTimeout` | 120s | 请求发出 → 响应头返回。**没有**用 `http.Transport.ResponseHeaderTimeout`：那个字段只在 HTTP/1 生效，官方端点会协商到 HTTP/2 |
| 整个流 `providerStreamIdleTimeout` | 120s | 流建立后**相邻两次读之间**的最大间隔；总时长不设上限 |
| 非流式 body `providerResponseBodyTimeout` | 120s | 读完整个响应体的总时长 |

- 流式 / 非流式按**响应** `Content-Type: text/event-stream` 判定（不能看请求头：go-openai 会设
  `Accept: text/event-stream`，anthropic SDK 不设）。
- `responseGuard` 超时后 `cancel()` request context —— 这是唯一能唤醒阻塞在 body 读上的
  goroutine 的手段；`guardedBody.Read` 把由此产生的 `context.Canceled` 翻译成
  `provider stream stalled after 2m0s` 这类可诊断错误。
- `newProviderTransport()` 取代 `http.DefaultTransport`：`MaxIdleConnsPerHost` 32（默认只有 2）、
  `MaxIdleConns` 128、`IdleConnTimeout` 90s、`TLSHandshakeTimeout` 10s、
  `ExpectContinueTimeout` 1s、`DialContext` 10s。
- **DNS/connect/TLS/write/TTFB 遥测一行未动**：`httpTraceRoundTripper` 与 `httpPhaseTrace`
  保持原样，`TestHTTPTraceClientKeepsPhaseTelemetryWiring` 钉住这一点。

**AUDIT-P0-09 · graceful shutdown 三重失效**

新增 [internal/server/lifecycle.go](../../internal/server/lifecycle.go)，三点一起改：

1. `serveHTTPLifecycle` 在 `Serve` 返回 `ErrServerClosed` 后**等 `shutdownDone`**。原先
   `Serve` 在 listener 关闭的瞬间就返回、`Run` 立刻 `return nil`，后台 goroutine 的等待被丢弃。
2. `ShutdownSignals()` 返回 `os.Interrupt` + `syscall.SIGTERM`，
   [main.go](../../cmd/golang-cc/main.go) 改用它。
3. `Shutdown` 带 `ShutdownTimeout`（默认 20s）deadline，**且在 Shutdown 之前先 drain 长连接**：
   `streamRegistry.drain()` 给每条 SSE 写一条 `event: server_shutdown`，然后取消它的 request
   context 让 handler 自己返回。SSE 连接永远不会变 idle，只带 deadline 不主动收口的话仍然要
   干等 20s 再硬断。deadline 到了还有连接没排空则 `srv.Close()` 强制关闭，不再需要 SIGKILL。

WebSocket（`/ws` 走 `Hijack`）同样登记进 registry：`guardedWriter.Hijack` 清掉 `net.Conn` 上
按 `ReadTimeout` 设置的 deadline，并在 shutdown 时取消其 context（不写 SSE 事件）。

**AUDIT-P0-13 · 请求体上限与 http.Server 超时**

- `requestGuardHandler` 作为最外层中间件统一挂 `http.MaxBytesReader`
  （`Options.MaxRequestBodyBytes`，默认 10 MiB，负数关闭）；声明的 `Content-Length` 超限时
  **一个字节都不读**就返回 413。`/query` 的 `json.Decode` 失败会经 `isRequestBodyTooLarge`
  纠正为 413 而不是含义错误的 400。`gin.Engine.MaxMultipartMemory` 设为 8 MiB。
- `newHTTPServer` 设 `ReadHeaderTimeout` 15s / `ReadTimeout` 60s / `IdleTimeout` 120s。
  **`WriteTimeout` 故意留 0**：它从请求开始计时，会砍断「跑完整个 agent turn 再一次性返回
  JSON」的 `/query`，也会砍断 SSE。写侧改由 `guardedWriter` 实现成「写空闲」deadline
  —— 首次写出才武装、每次写出续期（过半才下 syscall），SSE / WebSocket 则彻底清掉。
  慢速读攻击照样挡（没有进展就到期），慢 handler 与长连接不受影响。
- `mobileBodySummaryLogMiddleware` 的无上限 `io.ReadAll` 改为只采样开头 64 KiB，
  用 `io.MultiReader` 把采样片段拼回 body，handler 仍然看到完整请求体；日志新增
  `body_truncated` 标记。日志层不再是内存放大器。

**测试**（新增用例均已验证「移除修复后失败」）

| 测试 | 覆盖 |
| --- | --- |
| `TestServeCompletesInFlightRequestBeforeExiting` | shutdown 期间在途 `/query` 跑完并返回完整 JSON；改回旧实现即报 `serve returned while a request was still in flight` |
| `TestServeTerminatesSSEWithinShutdownDeadline` | SSE 收到 `event: server_shutdown`、handler context 被取消、`serve` 在 deadline（30s）远未到时就退出；改回旧实现则**永久 hang**（实测 143s 未退出，需强杀） |
| `TestServeShutsDownGracefullyOnSIGTERM` | 真发 `SIGTERM` → context 取消 → 在途请求跑完 → `serve` 退出 |
| `TestShutdownSignalsIncludeSIGTERM` | 信号集合含 SIGTERM |
| `TestRequestBodyLimitRejectsOversizedBodies` | 声明 `Content-Length` 超限时读取 0 字节即 413；未知长度时读取量不超过上限（证明没撑爆内存）+ 413 JSON 载荷 |
| `TestRequestBodyLimitAllowsNormalRequests` | 正常请求不受影响 |
| `TestNewHTTPServerSetsReadSideTimeouts` | 三个读侧超时已设、`WriteTimeout` 保持 0 |
| `TestGuardedWriterDeadlinePolicy` | 普通响应武装写 deadline；SSE 清掉读写 deadline 并登记 registry；`drain` 写终止事件 + 取消 context |
| `TestGuardedWriterForwardsOptionalInterfaces` | `Flusher`/`CloseNotifier`/`Hijacker`/`WriteString`/`Unwrap` 全部转发（gin 的 `CloseNotify` 是无保护类型断言） |
| `TestMobileBodySummaryLogMiddlewareSamplesWithoutBufferingWholeBody` | 3× 采样上限的请求体，handler 仍看到完整字节数 |
| `TestProviderTransportOverridesDefaultPoolAndHandshakeTimeouts` | 连接池四参数 + DialContext |
| `TestDefaultHTTPTraceClientHasNoTotalClientTimeout` | `http.Client.Timeout` 必须为 0，三段超时必须都设 |
| `TestHTTPTraceClientFailsWhenGatewayNeverSendsHeaders` | 永不回应的网关被建流超时叫醒 |
| `TestHTTPTraceClientStopsStalledStreamButNotSlowOne` | 头到了之后建流超时解除（第一次读成功）；随后停滞的流被空闲超时中止 |
| `TestHTTPTraceClientKeepsLongStreamAlive` | 总时长远超任何单段超时的持续推进流不被砍 |
| `TestHTTPTraceClientBoundsNonStreamingBodyRead` | 非流式响应体读取有总上限 |
| `TestHTTPTraceClientKeepsPhaseTelemetryWiring` | phase 遥测未被超时改动破坏 |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/server ./internal/anthropic ./internal/cli -count=1` | 3/3 ok |
| `go test ./internal/server ./internal/anthropic -race -count=1` | 2/2 ok |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

**已知残留**：mobile 路由上未声明 `Content-Length` 的超大请求体会由各自的 bind 错误路径报
400（而非 413）—— 上限本身照样生效，只是状态码不统一；改状态码需要动
`internal/server/mobile.go`，留给该文件的 owner。

### 修复证据 · AUDIT-P0-14（2026-07-25）

子代理这条路径的四个维度（递归深度、batch 并发、后台 agent 数、累计预算）一次收口，
外加 AUDIT-P1-20 的两条同源缺陷（见本节末）。

**① 递归深度计数**

- 计数器放在 [`tools.Context.SubagentDepth`](../../internal/tools/tool.go)，因为它已经是
  唯一能从父会话一路流到任意深度子代理的载体：`agentruntime.runTool` 用
  `childContext := parent` 构造子工具上下文，深度自然随之下传。
- `Runtime.Run` 开头 `depth := toolContext.SubagentDepth + 1`，超限即 `return`，
  **检查放在建 task row / recorder / MCP server 之前** —— 被拒的递归不留任何残留。
  通过后 `toolContext.SubagentDepth = depth` 回写，`runTool` 的 childContext 自动继承。
- 上限默认 2（父会话 0，子代理 1，孙代理 2 是最后被接受的一层），
  `GOLANG_CLAUDE_CODE_MAX_SUBAGENT_DEPTH` 可覆盖。选 2 而非 1 是因为一层再委派仍是
  真实的分工；再深就只是扇出乘数。
- 错误文案明确告诉模型该怎么办（"do the remaining work directly instead of delegating
  again"），**不静默降级** —— 静默把 Task 从 registry 里摘掉会让模型反复尝试同一件事。
- **`internal/cli/cli.go` 未改动**。审计把 `cli.go:673-682`（把 `Task` 注册进传给
  `Task` 自己的那个 registry）列为证据，但那个自引用本身是对的：子代理在深度限内用
  `Task` 是正常能力。真正缺的是计数器，所以修在 `agentruntime`，与 compact 分支零冲突。

**② `Explore` / `Plan` 的 deny 列表**

- 两者的 prompt 都写着 "READ-ONLY ... STRICTLY PROHIBITED"，却只 deny 了 `Agent`
  —— `Task` / `AgentCreate` / `AgentMessage` 全都还在，一个只读 agent 可以派生一个
  可写的 `general-purpose` 子代理，绕过自己的全部约束。
- 抽成 [`readOnlyAgentDisallowedTools()`](../../internal/agents/builtin.go)，
  两个 built-in 共用：新增 deny `Task` / `AgentCreate` / `AgentMessage` / `SendMessage`。
  写工具（`Edit`/`Write`/`NotebookEdit`/`ExitPlanMode`）原样保留。
- `general-purpose` 刻意保持 `Tools:["*"]` 且无 deny 列表 —— 约束它的是深度计数，
  不是能力裁剪；`TestGeneralPurposeKeepsFullToolAccess` 锁住这个方向，防止后人
  "顺手"收窄它。

**③ `max_concurrency` clamp**

- 默认仍是 4，上限 16（`GOLANG_CLAUDE_CODE_MAX_BATCH_CONCURRENCY` 可覆盖）。
  InputSchema 同步加上 `minimum`/`maximum` 与说明。
- 这里选 **clamp 而不是拒绝**（与 ④ 相反）：batch 的 N 个任务是调用方真实想做的工作，
  只是节奏该由运行时定；拒绝整个 batch 会逼模型手工分批，纯粹的体验损失。
- **但不静默**：`batchSummary.concurrency_clamp_note` 报告原始值、生效值和 env 名。
  静默 clamp 读起来就像"你那 500 个 worker 跑过了"。

**④ 后台 agent 数量上限**

- [`internal/agentruntime/limits.go`](../../internal/agentruntime/limits.go) 的
  `backgroundAgentLimiter`，默认 16，`GOLANG_CLAUDE_CODE_MAX_BACKGROUND_AGENTS` 可覆盖。
  风格与理由沿用 `internal/server/agent_task_concurrency.go`：**超限即拒，不排队**
  （排队等于把压力转成内存占用并且对调用方隐藏过载）。
- 计数器必须是进程级：detached run 活得比启动它的那次调用长，per-call 限流器看不见它们。
- 占位在 `RunBackground` 最前面，拒绝时不建 task row、不起 goroutine、不建 detached ctx。
  `release` 由 goroutine 的 `defer` 归还且幂等。
- 顺带给这个 goroutine 补 `recover()` —— 它是 AUDIT-P0-10 明确留给本条的裸 goroutine
  之一，panic 会打死整个进程。recover 后转成一次 start 失败交回调用方。

**⑤ 会话级累计 token / 成本预算与熔断**

- 新增 [`internal/agentbudget`](../../internal/agentbudget/budget.go)：互斥锁保护的累加器
  + `Check()` 熔断。`nil *Budget` 是合法的"无限预算"，调用点因此不需要 nil 判断。
- 作用域：`query.Session` 建一个，经 `tools.Context.AgentBudget` 下发；子代理树内所有
  嵌套子代理共用同一个指针，**不是每层一份新配额**。server / agenteval 路径没有会话级
  wiring（`internal/server/` 不在本次改动范围），`Runtime.Run` 在收到 nil 时兜底建一个
  **按子代理树**作用域的预算 —— 至少一棵树内是累计的。
- 只计子代理用量，**父会话主循环不计**：跑到一半熔断主对话比它要防的失控更糟，
  而 AUDIT-P0-14 的失控在子代理侧（递归 × batch 并发 × 轮数 × max_tokens）。
- 熔断点在每轮请求**之前**，所以是"不再付钱"而不是"付完再报错"。
- 默认上限 2000 万 token（远高于任何正常会话，但能在几秒内截住失控扇出）；
  成本上限默认 0（关闭）。
- **与 AUDIT-P0-15 的已知偏差**：喂进来的 `InputTokens` 含 cache read/creation 且按满额
  input 单价折算，所以 token 与成本都是**高估**。对熔断器而言高估是安全方向（宁可早跳），
  但这两个数字不能拿去计费；包注释已写明，AUDIT-P0-15 落地后把分档数字直接喂进来即可，
  本包不依赖那个偏高的口径。

**顺带闭合 AUDIT-P1-20 的两条**（该条其余两项仍 TODO，见 §5）

- **`AgentMessage` 未被强制 deny**：`SendMessage` 只是解析收件人后转调 `AgentMessage`
  （`internal/tools/agent/agent.go` 的 `SendMessageTool.Run`），所以只 deny `SendMessage`
  等于没 deny。`runtime.go` 现在两个一起 deny，A→B 横向通道对子代理彻底关闭。
- **batch 重试对 `context.Canceled` 照重试**：父 ctx 已死，后续每次尝试都会同样失败，
  而每次尝试都会覆盖上一次的 partial evidence。现在遇到 `context.Canceled`
  （或 `ctx.Err() != nil`）立即 break，保住本次的 partial。**deadline 仍然重试** ——
  单次尝试超时不代表下一次也会超时，`TestTaskToolBatchStillRetriesDeadlines` 锁住这个边界。

**测试**（每条均已 mutation check 验证"移除修复后失败"）

| 测试 | 覆盖 | mutation check |
| --- | --- | --- |
| `TestRuntimeRejectsSubagentBeyondDepthLimit` | 到达上限即拒、错误可读且指向 env、不发请求 | 去掉深度判断 → 无错误返回 |
| `TestRuntimeRejectsNestedSubagentsAtDepthLimit` | 真实三层嵌套：depth 2 接受、depth 3 被拒 | 去掉判断或去掉深度回写 → 嵌套无限，测试自带 runaway guard 报错（不去掉 guard 会直接挂死，这正是缺陷的形状） |
| `TestRuntimeStampsSubagentDepthOnToolContext` | **wiring 断言**：工具真收到 `SubagentDepth=1` 与非 nil budget | 去掉 `toolContext.SubagentDepth = depth` / budget 兜底 → 收到 0 / nil |
| `TestReadOnlyBuiltInsDenyDelegationTools` | `Explore`/`Plan` 拿不到 `Task`/`AgentCreate`/`AgentMessage`/`SendMessage`，写工具仍被 deny | 还原成原来的 5 项 deny → 失败 |
| `TestGeneralPurposeKeepsFullToolAccess` | 反方向守卫：`general-purpose` 不被顺手收窄 | — |
| `TestTaskToolClampsOversizedMaxConcurrency` | `max_concurrency: 500` 被 clamp、**实测峰值并发不超上限**、clamp 有 note、任务不丢 | 去掉 clamp → 峰值超限 |
| `TestTaskToolKeepsRequestedConcurrencyUnderCeiling` | 上限内的请求不被改也不误报 clamp | — |
| `TestRunBackgroundRejectsBeyondBackgroundAgentLimit` | 超限拒绝、错误可读、**不留 task row**、释放后可复用 | 去掉限流器 → 第二次请求成功 |
| `TestBackgroundAgentLimiterReleaseIsIdempotent` | 重复 release 不把计数压成负数 | — |
| `TestRuntimeStopsWhenAgentBudgetExhausted` | 熔断在第 3 轮请求**之前**触发（只发出 2 次）、`errors.Is(ErrExhausted)`、错误指向 env | 去掉轮内 `Check()` → 跑满 20 轮；去掉 `Add()` → 永不熔断 |
| `TestRuntimeSharesOneBudgetAcrossNestedSubagents` | 嵌套子代理累加进同一个预算（60 而非每层 30） | 去掉 `Add()` → 计数为 0 |
| `TestSessionHandsSubagentBudgetToTools` | **wiring 断言**：`query.Session` 建了预算且真的传给工具，且是同一个指针 | 去掉两行 wiring 中任意一行 → 工具收到 nil |
| `TestTaskToolBatchDoesNotRetryAfterCancellation` | 取消后只尝试 1 次、partial evidence 保留 | 去掉 cancel break → 尝试 4 次 |
| `TestTaskToolBatchStillRetriesDeadlines` | 反方向守卫：deadline 仍然重试 | — |
| `TestBudgetTripsOnTokenCeiling` / `TestBudgetTripsOnCostCeiling` / `TestBudgetZeroLimitsNeverTrip` / `TestNilBudgetIsUnlimitedAndSafe` / `TestDefaultLimitsHonourEnvOverrides` / `TestBudgetAddIsConcurrencySafe` | 预算包本身：两种上限、0=关闭、nil 安全、env 覆盖（含"显式 0 关闭"与"垃圾值回落"）、并发累加 | — |
| `TestMaxSubagentDepthEnvOverrideRejectsNonsense` / `TestMaxBatchConcurrencyEnvOverrideRejectsNonsense` | env 覆盖生效，`0`/负数/非数字回落默认 | — |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/agents ./internal/agentruntime ./internal/agentbudget ./internal/tools/task ./internal/cli ./internal/query -count=1` | 6/6 ok |
| `go test -race ./internal/agentruntime ./internal/tools/task ./internal/agentbudget -count=1` | 3/3 ok，无 race |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

**已知残留 / 交接**

- `internal/server/` 的两条子代理入口没有会话级预算 wiring（该目录不在本次范围）。
  `Runtime.Run` 的 nil 兜底让每棵子代理树至少是累计的，但同一 server 会话内多棵树
  各自独立。要真正做到"会话级"，需要 server 侧把一个 `*agentbudget.Budget` 放进
  它构造的 `tools.Context`。
- `internal/tools/task/task.go` 的另两个裸 goroutine（`runSingleWithAutoBackground`
  与 batch worker）仍无 `recover()`。它们属 AUDIT-P0-10 列出的清单，本次只处理了
  `agentruntime` 那个（因为限流器的 `defer` 正好落在同一处）。
- **与 AUDIT-P0-08 合并后新出现的口径缺口**：`maybeCompact` / `compactAfterOverflow`
  各自是一次独立的 LLM 调用，走 compactor 自己的 client，因此**不计入本条的预算**。
  熔断器只统计子代理主循环的 turn 用量。子代理压缩是每轮至多一次、且只在超阈值时发生，
  量级远小于被它挡住的失控扇出，所以不是紧急问题；要做到全口径，需要 compactor
  把它那次调用的 usage 也回灌进 `tools.Context.AgentBudget`。
- AUDIT-P1-20 剩余两项未做：batch 子代理写同一棵工作树无锁；单 Task 无重试而 batch 有。

---

## 3. P0 · 工程根因

**一次投入解决一大片**，且今天就能全绿。

| ID | 状态 | 模块 | 问题 | 证据 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| AUDIT-P0-16 | DONE | CI | **零 CI**：无 `.github/`、无 Makefile、无 Dockerfile、无 `.golangci.yml`、无 git hooks。341 个 Go 文件、69 个测试包、80 个脚本，**全部质量信号靠人肉执行** | 仓库根目录 | 新增 GitHub Actions：`go build` + `go vet` + `gofmt -l`（非空即失败）+ `go test -race ./...` + `govulncheck`。基线今天即可全绿 |
| AUDIT-P0-17 | DONE | Security/Deps | **5 个你的代码实际调用到的 CVE**（`govulncheck` 实测）：`GO-2026-5970` x/text 无限循环、`GO-2026-5320` goldmark XSS（经 glamour 从 `internal/tui/app.go:6803` 可达）、`GO-2026-5676` + `GO-2025-4233` quic-go DoS、`GO-2026-5856` stdlib crypto/tls。另有 16 个直接依赖过期，`anthropic-sdk-go` 落后 15 个 minor（v1.46.0 → v1.61.0）、`mcp-go` 落后 12 个 | `govulncheck` 输出 | 全部升级到 fixed 版本；`govulncheck` 进 CI |
| AUDIT-P0-18 | DONE | Docs/API | **swagger 漂移 19 个 commit**（`docs/swagger.json` 最后生成于 `9c3f5081` / 07-15，此后 `internal/server` 有 19 个 commit）。缺失端点：`/v1/providers`、`/runtime/settings`、`/tenant/agent-tasks/{id}/events/stream`、`/ws`。根因不在生成器 —— 这几个 handler **根本没写 `@Router` 注解**，重跑 `swag init` 也拿不到。前端已在用手写内联类型兜底 | `docs/swagger.json`、`internal/server/swagger_annotations.go`、`web/src/lib/api.ts:152` | 补齐 4 个端点的 swagger 注解；重跑 `swag init`；重新生成 `web/src/lib/generated/api-types.ts`；CI 加"swagger 未同步即失败"检查 |
| AUDIT-P0-19 | DONE | Scripts | **69 个 acceptance 脚本无人跑、无分类、前置条件未文档化**（本条结论已于 2026-07-25 更正，原判断「`claude_code_src_2026` 与 `agent-proving-ground` 本机已不存在、验收基建已死」**是错的** —— 这两个是有意维护的配套项目，见下方「结论更正」）。真实问题：① `agent-capability-full-release-acceptance.sh` 依赖 11 个 `reports/**/*.json`，那些是**跑完 APG 之后的产物**而非源码，脚本没有说明这一点；② 硬编码绝对路径（含指向仓库旧名 `golang-claude-code` 的死路径、和 `GO_BIN=/usr/local/go/bin/go` 这个比 `go.mod` 要求更旧的默认值）；③ 23 个需真实 `ANTHROPIC_API_KEY`；④ 四个重叠的部分聚合器、无统一根入口；⑤ 没有任何一个接进 CI，所以脚本烂掉了也没有信号 | `scripts/agent-capability-full-release-acceptance.sh`、`scripts/agent-capability-release-gate.sh`、`scripts/tui-*-acceptance.sh` 的 `GO_BIN` 默认值等 20+ 处 | 更正审计结论；release gate 加前置检查并说明产物来源；三类脚本分类进 `scripts/README.md`；可脱机部分收进 `scripts/offline-acceptance.sh` 并接进 CI；绝对路径改为环境变量可覆盖 |
| AUDIT-P0-20 | DONE | Toolchain | **构建不可复现**：`/usr/local/go` 是 1.22.11 而 `go.mod` 要 1.24.2，`GOSUMDB=off` 让 toolchain 自动下载失败；无 `.tool-versions`。前端要 Node 20+（`vite@8` / `vitest@4`）但 `web/package.json` **既无 `engines` 也无 `.nvmrc`**，本机 Node 18.20.8 既跑不了测试也 build 不了 | `go.mod:3`、`web/package.json` | 补 `.tool-versions` / `engines` / `.nvmrc`；README 写明工具链要求 |

### 完成记录：AUDIT-P0-16 / P0-17 / P0-20（2026-07-25）

三项一起做，因为互为前提：CVE 修复需要先确定 Go 版本，CI 又必须 pin 同一个版本才有意义。

**AUDIT-P0-16 · CI** —— 新增 [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)，push / PR / 手动触发，
四个独立 job：

| job | 内容 | timeout |
| --- | --- | --- |
| `build` | `go build ./...`、`go vet ./...`、`gofmt -l .`（输出非空即失败）、`go mod tidy` 后 `git diff --exit-code -- go.mod go.sum` | 20 min |
| `test` | `go test -race ./... -count=1` | 60 min |
| `govulncheck` | `govulncheck ./...`，独立 job 以免新 CVE 遮蔽 build/test 信号 | 20 min |
| `web` | `npm ci --legacy-peer-deps` + `npm run build` + `npm run test` | 20 min |

`GO_VERSION` 显式 pin `1.26.5`，与 `.tool-versions` 同源。

**AUDIT-P0-17 · 5 个已调用 CVE** —— 只升级修复所需的最小集合，`anthropic-sdk-go`（v1.46→v1.61）和
`mcp-go`（v0.45→v0.57）**未动**，跨度大、需独立评估，仍为 TODO：

| CVE | 模块 | Have → Now |
| --- | --- | --- |
| GO-2026-5970 | golang.org/x/text | v0.31.0 → **v0.39.0** |
| GO-2026-5676 + GO-2025-4233 | github.com/quic-go/quic-go | v0.54.0 → **v0.59.1** |
| GO-2026-5320 | github.com/yuin/goldmark | v1.7.13 → **v1.7.17** |
| GO-2026-5856 | stdlib crypto/tls | 由 CI 与 `.tool-versions` pin 的 **Go 1.26.5** 修复 |

连带升级（`go get` 传递解析）：qpack v0.5.1→v0.6.0、x/net v0.47→v0.56、x/crypto v0.45→v0.53、
x/sys v0.38→v0.46、x/sync v0.18→v0.21、x/term v0.37→v0.44、x/mod v0.29→v0.37、x/tools v0.38→v0.47、
mock v0.5.0→v0.5.2。quic-go v0.59.1 要求 Go 1.25，因此 `go.mod` 的 `go` 指令由 `1.24.2` 抬到 `1.25.0`。

**AUDIT-P0-20 · 工具链** —— 新增 [`.tool-versions`](../../.tool-versions)（`golang 1.26.5` / `nodejs 22.23.1`）、
[`web/.nvmrc`](../../web/.nvmrc)（`22.23.1`）、`web/package.json` 的 `engines.node`
（`^20.19.0 || >=22.12.0`，即 `vite@8` 的真实下界），README 新增「工具链要求」章节。

`go.mod` **故意不加 `toolchain` 指令**：本机全局 `GOSUMDB=off`，一旦 go.mod 要求一个未安装的 toolchain，
`go` 会拒绝校验下载物并让**仓库内所有 go 命令失败**（实测报
`golang.org/toolchain@v0.0.1-go1.26.5.darwin-amd64: verifying module: checksum database disabled by GOSUMDB=off`，
且即使 toolchain 已在本地缓存仍会复验失败）。版本约束改由 CI + `.tool-versions` + README 承担，
临时下载 toolchain 的办法写进了 README。

**验证证据**（Go 1.26.5，与 CI 同版本；在 `HEAD=376f77e1` 的独立 worktree 中执行，
未受同期并发修改 `internal/permissions` 的工作影响）：

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过（darwin/amd64 与 `GOOS=linux GOARCH=amd64` 交叉编译均通过） |
| `go vet ./...` | 通过（同上，两个平台） |
| `gofmt -l .` | 无输出 |
| `go test -race ./... -count=1` | **69 包全 ok，0 FAIL，无 DATA RACE**，exit 0 |
| `go mod tidy` 幂等性 | 再跑一次 `go.mod` / `go.sum` 逐字节不变 |
| `govulncheck ./...` | **`Your code is affected by 0 vulnerabilities.`**（原为 5），darwin/amd64 与 linux/amd64 一致 |
| `npm ci --legacy-peer-deps && npm run build && npm run test`（Node 22.23.1） | build 通过；**11 个测试文件、116 个测试全 passed** |

**顺带闭合 AUDIT-P2-07** —— 新增 [`third_party/termenv/PATCH.md`](../../third_party/termenv/PATCH.md)：
声明该 fork 已冻结、记录相对上游 v0.16.0 的唯一改动（`termenv_unix.go` 的 `backgroundColor()` 删掉 OSC 11
查询）及原因，并给出可复跑的 diff 校验命令（实跑确认 `diff -r -q` 仅输出 `termenv_unix.go` 一行）。

**CI 首跑就抓到一个原审计漏掉的真实缺陷**（已修，见下）：

`internal/tui` 有 5 个测试**只在原作者机器上能过**。`welcome header` 与 `mode hint` 的路径经
`abbreviateHome()`（[app.go](../../internal/tui/app.go) 的 `os.UserHomeDir()`，即读 `$HOME`），
而 fixture 与 3 个 golden 固定了某个开发者的 home 前缀，并断言渲染结果是
`工作区 ~/GolandProjects/...`。只有当运行环境的 `$HOME` 与 fixture 中的固定前缀一致时，该前缀才会被缩写成 `~`；
GitHub runner 的 home 是 `/home/runner`，于是
`TestModelWelcomeSuppressesWorkbenchGoalAndCompletedTodos`、`TestModelWelcomeHeaderGoldens`
（3 个子测试）、`TestModelModeHintShowsRuntimeContextStatus`、
`TestModelModeHintWrapsNarrowTerminalRows` 全部失败。

修法是给该包加 `TestMain` 把 `HOME` / `USERPROFILE` pin 成 fixture 假设的前缀
（只影响测试进程，不动任何生产代码，goldens 保持原样）。
**本地用 `HOME=<临时目录> go test ./...` 复现了完全一致的失败，并确认全仓只有 `internal/tui`
这一个包依赖 ambient `$HOME`**；修复后在 CI-like `$HOME` 与真实 `$HOME` 下均通过。

这条正好印证 AUDIT-P0-16 的价值：审计期「69 包全 ok」的基线是在原作者机器上取的，
**「本机全绿」从来不等于「可复现地绿」**。

**本次发现的、原审计未记录的新问题**（均未修，建议单独立项）：

- `web/` 存在**真实的 peer dependency 冲突**：`typescript@6.0.3` 与 `openapi-typescript@7.13.0` 声明的
  `peer typescript@^5.x` 不兼容，裸 `npm ci` 直接 `ERESOLVE` 失败。当前用 `--legacy-peer-deps` 绕过（CI 与
  README 均已注明）。真正的修法是等 `openapi-typescript` 支持 TS 6，与 AUDIT-P0-18 的 api-types
  生成链路一并处理。
- **`docs/todo.md` 的 `PARITY-001` 现在具备解除 BLOCKED 的条件**：它标 BLOCKED 的理由是
  「需要 Linux CI 或 Linux 主机」来跑真实 bubblewrap/seccomp 金标，而本次已经有了 `ubuntu-latest`
  runner。CI 里两个 Linux sandbox 测试目前仍会 SKIP（runner 默认不带 bubblewrap），
  补一个装 `bubblewrap` 的 Linux sandbox job 即可闭合，建议单独立项。

---

## 4. P1 · Agent 实际效果

| ID | 状态 | 模块 | 问题 | 证据 |
| --- | --- | --- | --- | --- |
| AUDIT-P1-01 | DONE | Query | **压缩顺序错位**：compact 判定跑在工具结果外化**之前** —— 免费的外化本可解决，却触发了昂贵的 LLM 压缩。**改动约 20 行，纯收益，性价比最高的单点修复** | `internal/query/query.go:1386`（compact）vs `:1420-1436`（budget） |
| AUDIT-P1-02 | DONE | Query | 原审计发现工具完全串行。现已实现 policy 驱动的 bounded read-only parallel：只有整轮工具全部显式 `read_only`，且权限、skill、hook、pre-tool gate 均可无副作用预检时才并行；结果、transcript 和 model context 仍按模型顺序提交，`--max-parallel-read-only-tools -1` 可恢复串行。真实 60-run A/B correctness 不退化，但 task p95 回退 17.45%，主要来自 provider `stream.read` 长尾，因此只闭合“运行时不支持安全并行”的能力缺口，不宣称端到端提速，也不继续提高并行度。 | `internal/query/tool_batch.go`；`internal/tools/concurrency.go`；`docs/observability/runtime_performance_observability_plan.md` |
| AUDIT-P1-03 | DONE | Session | **`/compact` 保留最旧的 12KB、丢弃最近对话**：`buildSummary` 从最旧 entry 正向遍历 + `if out.Len() >= maxBytes { break }`，语义与用户预期完全相反。且该路径不调用任何模型，只是把 transcript 渲染成 `ROLE: text` 拼接 | `internal/session/store.go:2058-2095`（尤其 `:2069-2071`） |
| AUDIT-P1-04 | DONE | Query | **loop guard 抓不到纯文本循环**：completion gate 不通过就 `continue`，而 loop guard 判定块在工具执行之后 —— 模型反复输出不合规收尾文本会一路跑到 MaxTurns=100，全程无 streak 计数；且 nudge 每轮 `append` 从不移除，100 轮 = 100 条重复 reminder 堆积。**子代理完全没有 loop guard** | `internal/query/query.go:1671`、`:1720-1723`、`:1728`、`:1836`；`internal/agentruntime/runtime.go` 无 loopGuard |
| AUDIT-P1-05 | DONE | Compact | **token 估算失真**：base64 图片按文本估算（1MB 图 ≈ 35 万虚假 token，单图即可强制压缩）；中文按 1 rune = 1 token 高估 2–4×；**无 `count_tokens` API**（全仓零命中）；真实 `usage.input_tokens` 已记录却从不回灌校正估算器 | `internal/compact/tokens.go:58-63`、`:74-92`；`internal/query/query.go:1622` |
| AUDIT-P1-06 | DONE | Compact | 压缩熔断器**永不复位**（`failures` 只在成功时清零）→ 累计 3 次失败后本会话压缩永久关闭；`CooldownTurns` 是唯一漏配默认值的字段，冷却实际为 0；facts 按字母序截断前 40，与重要性/时近性无关 | `internal/compact/compactor.go:65,105`；`internal/compact/config.go:28-42`；`internal/compact/facts.go:456-472` |
| AUDIT-P1-07 | DONE | Provider | **thinking 块回传退化为空 text 块**：`sdkMessageParam` 的 switch 只处理 image/tool_use/tool_result，`"thinking"` 落到 `default:` → `NewTextBlock(block.Text)`，而 thinking 块的 `Text` 恒为空（内容在 `.Thinking`、签名在 `.Signature`）。后果是丢 signature + 空 text 块被 API 拒。两条真实触发路径：多轮 tool use + 启用 thinking；resume 重建。目前是潜伏 bug（thinking 仅在 active skill 设 `effort` 时启用），**仓内无回传测试** | `internal/anthropic/client.go:1377-1408`；`internal/query/query.go:1655`、`:2294-2302` |
| AUDIT-P1-08 | DONE | Provider | ~~**OpenAI-compatible 路径实质无重试**：仅建流阶段 1 次、仅覆盖 429；退避是固定 65 秒或从错误文案正则抠数字，**不读 `Retry-After` 响应头**。5xx / 网络抖动在单 provider 配置下一次失败即返回错误。另无熔断冷却，fallback 失败后下一轮仍从 primary 重试~~（DONE：重试与 `Retry-After` 见 [第一批修复证据](#修复证据--audit-p1-07--p1-08--p1-09--p1-102026-07-26)；**熔断冷却已补**，见 [本条收尾](#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)：`providerBreaker` 挂在会话级 `Client` 上，可 fallback 的失败让该 provider 冷却 60s，成功即清零；4xx 与调用方取消不入冷却；全员冷却时忽略冷却照常从队首试；跳过原因写进聚合错误文案） | `internal/anthropic/client.go:152-215`；`internal/anthropic/cooldown.go` |
| AUDIT-P1-09 | DONE | Provider | Anthropic 路径 **`ResponseFormat` 静默丢弃**（仅 server 层有 prompt 兜底 + 一次重试，CLI/子代理路径完全没有）；`ANTHROPIC_PROVIDER=bedrock` 是**指向不存在 provider 的死分支**（`providerKindSupported` 会直接拒绝）；OpenAI 路径不映射 thinking/reasoning_effort | `internal/anthropic/client.go:1274-1299`；`internal/query/query.go:7341`；`internal/server/handlers_openai.go:334,401` |
| AUDIT-P1-10 | DONE | Provider/Test | **`parseSSE` 是死代码** —— 唯一调用者是测试。生产 Anthropic 路径走 SDK，OpenAI 路径走 go-openai。即两个"SSE 健壮性测试"验证的是不上生产的解析器，**生产 mid-stream error event 零覆盖**；OpenAI 路径不识别 chunk 内嵌 `{"error":{...}}`，会静默产出空响应 | `internal/anthropic/client.go:1565-1728` vs `client_test.go:40,91`；`internal/anthropic/client.go:394-464` |
| AUDIT-P1-11 | DONE | Memory/KB | **零语义检索**（全仓无 embedding；`embedding_ref` 是从未写入的 schema 占位符 —— 这是 TODO-025 的既定取舍）。但有两个可直接修的 bug：①租户 KB 的 FULLTEXT 索引**缺 `WITH PARSER ngram`** → MySQL 默认分词器不切中文，中文 `MATCH` 恒为 0，静默退化成无排序全表 LIKE；②KB 分块用**字节切 UTF-8**，中文分块碎字 | `migrations/mysql/000003_tenant_knowledge.up.sql:36`；`internal/tenant/service.go:1968`；`internal/storage/mysql/gorm_repository.go:1049` |
| AUDIT-P1-12 | DONE | Memory | `memory.paths:` frontmatter **事实失效** —— `promptMatchesPattern` 拿 pattern 去 `strings.Contains(prompt, needle)`，匹配的是用户输入字符串而非工作文件集，`paths:[internal/memory/**]` 只在用户字面输入 "internal/memory" 时才生效。且 `CLAUDE.md`/user/team/managed 记忆**从不截断**（字节预算只对 `Type=="Workflow"` 生效），500KB 的 CLAUDE.md 会整个进 prompt | `internal/memory/memory.go:697-704`、`:15,205-215` |

### 修复证据 · AUDIT-P1-01 / P0-08 / P1-05 / P1-06（2026-07-25）

四条同属上下文治理链路，互为前提，一次提交完成。顺序刻意是「先换序 → 再定默认值 → 再校准估算 → 再修熔断」：
换序之后 token 数变小，压缩触发频率本身就下降，P0-08 的默认值和阈值判断必须基于换序后的行为。

**AUDIT-P1-01 · 压缩判定与工具结果外化的顺序**

- `internal/query/query.go`：`MaybeCompact` 现在跑在 `toolresult.ApplyMessageBudget` /
  `ApplyHistoryBudget` **之后**。外化是免费的（大工具结果落盘，上下文里只留路径 + 预览），
  压缩要花一整轮 LLM 调用且永久丢细节；先判阈值等于为「外化本可解决的膨胀」付费。
- **审计对这条的收益估计偏大，实测边界收窄了**：单个超大工具结果其实在**工具执行时**就已经被
  `toolresult.Process` 外化了 —— `tools.EffectiveResultLimit` 把任何单条上限夹到
  `toolresult.DefaultLimit`（50 000 字符），所以 200 KB 的单条结果根本进不到 `messages`。
  换序真正影响的是**累积**场景：多条各自低于 50 000 字符、但合起来超过
  `ToolResultMessageBudget` / `ToolResultHistoryBudget` 的结果，以及跨轮累积的历史。
  这一点直接决定了测试怎么写：金标里的 blob 必须**小于** `DefaultLimit`，否则两种顺序行为
  完全一致、测试无法区分（第一版测试正是这么写的，mutation check 抓出它对换序不敏感）。

**AUDIT-P0-08 · 默认开启 + overflow 反应式兜底 + 路径接入**

- **默认开启**：`internal/compact/settings.go` 改为「未显式配置即开启」
  （`cfg.Enabled = true`，仅 `autoCompact.enabled` 显式给值时才覆盖）；`config/config.yaml`
  同步 `enabled: true`。显式 `enabled: false` 仍然生效，有测试锁住两个方向。
- **overflow 兜底**：新增 [internal/compact/overflow.go](../../internal/compact/overflow.go)
  的 `IsContextOverflowError`，以及 `Compactor.ForceCompact`（忽略阈值与冷却，但仍尊重
  「显式关闭」和熔断）。`query.Session.run` 与 `agentruntime.Runtime.Run` 在流失败时各自
  尝试**一次**强制压缩并重试当前轮。
  - **不与 provider fallback 打架**：context overflow 是确定性的 400 `invalid_request_error`，
    `canFallbackAfterError`（`internal/anthropic/client.go:1002`）本来就判定它不可 fallback，
    所以走到这里时 client 侧的重试/切换已经穷尽，两条路径不重叠。
  - marker 名单刻意**收窄**：误判会为一个压缩救不了的错误白丢历史。非 overflow 错误
    （rate limit、网络抖动）绝不触发压缩，两个包各有测试锁住。
  - 恢复**只允许一次**：压缩后仍超长就直接失败，不会把 MaxTurns 烧光。
- **路径接入 —— 实际只有两个 wiring 点，不是四个**：审计列的
  `internal/server/runtime.go`、`internal/goal`、`internal/scheduler` 都不自己建 query session：
  - `internal/cli/cmd_server.go:276` 的 `runServerQuery` 走 `newQuerySession`，server 的
    `/query`、mobile、agent task 都用它；
  - tenant goal（`internal/server/handlers_goals.go:302` 的 `goal.Runner{Query: ...}`）用的是
    同一个 `StreamQueryFunc`；
  - scheduler 用 `ChildProcessExecutor` **重新 exec 本二进制**，落回 CLI 路径；
    `internal/server/runtime.go` 的 loop job 同理。
  → 因此 `internal/cli/cli.go:781` 那一行 `AutoCompact:` 一改默认值，四条路径同时获得保护。
  真正缺失的是**子代理**：新增 [internal/agentruntime/compact.go](../../internal/agentruntime/compact.go)，
  `Runtime.AutoCompact` 未显式给值时按 `config.LoadForCWD(req.CWD).Settings` 加载
  （与既有的 `SubagentModelTiers` / `ModelPricing` 同一套 cwd-scoped 取配置模式），
  **不必改 `internal/tools/task/task.go` 或 `agent.go`** —— 那两个文件属并行任务 AUDIT-P0-14。
- **压缩不会掩盖子代理循环**：子代理仍无 loop guard（AUDIT-P1-04，本次不修）。压缩只在
  token 超阈值时动作，不改变轮次计数，`maxTurns` 上限与 `sub-agent max turns reached` 报错
  路径原样保留，因此纯文本循环该怎么撞上限还是怎么撞。

**AUDIT-P1-05 · token 估算失真**

- **base64 图片不再按文本估算**：`internal/compact/tokens.go` 新增
  `estimateInlineSourceTokens`。图片按「单图上限」常量 1 600 token 计
  （provider 按像素而非传输体积计价，且有效分辨率上限约 1.15 MP ≈ 1 600 token）；
  非图片内联附件（PDF）按体积粗估并以同一常量兜底。1 MB 截图从 ~35 万虚假 token 降到 1 600。
- **真实 usage 回灌**：`Compactor.ObservePromptTokens` 用 provider 报告的精确 prompt 大小
  校正估算器（EWMA + 夹在 0.25×–4× 之间；只在估算值 ≥ 1 000 token 时更新；
  **压缩过的轮次不参与校准**，否则会把「刚压掉的量」当成估算器的系统性高估）。
  `query.go` 与 `agentruntime` 都在拿到 usage 后回灌。
- **两项刻意没做，需要说明**：
  1. **中文 per-rune 权重未改**。审计说「中文按 1 rune = 1 token 高估 2–4×」，但同一条里
     给出的实测区间是 0.6–1.5 token/rune —— 这两个说法互相矛盾，1.0 落在给出的区间内。
     在没有真实 tokenizer 可比对的情况下，把它改成某个凭感觉的更小常量属于用一个猜测
     替换另一个猜测。上面的 usage 回灌恰好让这个静态常量变得不重要：任何系统性偏差都会被
     实测比值吃掉。这一项等真接了 `count_tokens` 或本地 tokenizer 再动。
  2. **未接 `count_tokens` API**。仍是零命中，属独立工作量（需 provider 能力探测 +
     非 Anthropic provider 的降级路径），本次未做。
- 顺带说明估算偏差的**方向性**现在是安全的：低估的代价是一次强制重压 + 重试（P0-08 已兜住），
  高估的代价是此后每轮都白付一次 LLM 压缩。所以这些常量刻意偏保守而非偏高。

**AUDIT-P1-06 · 熔断器与冷却**

- **熔断可复位**：`Compactor` 记 `lastFailureAt`，`circuitOpen` 在距上次失败超过
  `FailureResetAfter`（默认 10 分钟）时清零。此前 `failures` 只在成功时归零，
  而熔断打开后压缩根本不再执行 → **永远不可能有下一次成功**，等于本会话永久关闭。
- **`CooldownTurns` 补上默认值**：`WithDefaults` 加 `DefaultCooldownTurns = 1`
  （此前是唯一漏配默认值的字段，冷却实际为 0）。
- **hard facts 按时近性截断**：`uniqueSortedLimit` 改为**先按时近性去重取前 N（从尾部倒扫），
  再排序渲染**。此前是先按字母序排序再切头 40 条 —— 一个刚编辑的 `web/**` 文件会仅因为
  路径排序靠后而被丢掉，而第一轮的 `.github/**` 却留着。渲染顺序仍是排序后的，保持输出稳定。

**测试**（每条都用 mutation check 验证「移除修复后失败、加上修复后通过」，15/15 全部被抓）

| 测试 | 覆盖 | mutation check（移除修复后的表现） |
| --- | --- | --- |
| `TestSessionCompactsExternalizedToolResultsNotRawBlobs` | 压缩记录的 `trigger_tokens` 反映外化后的 stub，且没有任何请求携带原始 blob | 把压缩块移回 budget 之前 → 失败 |
| `TestSessionSkipsCompactionWhenExternalizationFreesEnoughContext` | 外化已够时**一次 LLM 压缩都不付** | 同上 → 失败 |
| `TestSessionForcesCompactionAfterContextOverflowAndRetries` | overflow → 强制压缩 → 带压缩后历史重试 → 会话成功 | 去掉 `compactAfterOverflow` 调用 → 会话直接报错 |
| `TestSessionOverflowRecoveryIsAttemptedOnlyOnce` | 压不下去时只恢复一次，不烧光 MaxTurns | — |
| `TestSessionDoesNotCompactOnNonOverflowErrors` | rate limit 不触发压缩 | — |
| `TestSessionFeedsProviderUsageBackIntoCompactor` | provider 报 4× 估算值时下一轮真的触发压缩 | 删掉 `ObservePromptTokens` 调用 → 失败 |
| `TestSubagentRunCompactsLongHistory` | **子代理路径确实会压缩** | 删掉 `maybeCompact` 调用 → 失败 |
| `TestSubagentRunForcesCompactionAfterContextOverflow` | 子代理 overflow 恢复 | 删掉子代理 `compactAfterOverflow` → 失败 |
| `TestSubagentOverflowRecoveryIsAttemptedOnlyOnce` / `TestSubagentDoesNotCompactOnNonOverflowErrors` | 子代理侧的边界与主循环一致 | — |
| `TestSubagentCompactorDefaultsFromSettings` | **wiring 断言**：`Run` 无显式配置也会按 cwd settings 建出 compactor | 让 `newCompactor` 不读 settings → 失败 |
| `TestNewQuerySessionWiresAutoCompactByDefault` | **wiring 断言**：CLI / server / goal / scheduler 共用的 `newQuerySession` 默认带 compactor | 删掉 `cli.go` 的 `AutoCompact:` 行 → 失败；把默认值改回 opt-in → 也失败 |
| `TestNewQuerySessionHonorsExplicitAutoCompactDisable` | 显式 `enabled:false` 仍被尊重 | — |
| `TestConfigFromSettingsEnablesAutoCompactByDefault` | 无配置 / 空 `autoCompact` 块 / 显式 true / 显式 false 四种情形 | 默认值改回 false → 失败 |
| `TestEstimateDoesNotBillBase64ImagesAsText` | 1 MiB base64 图片估算 < 4 000 token，且远小于按文本估算的结果 | 恢复 `estimateTextTokens(Source.Data)` → 失败 |
| `TestEstimateScalesNonImageInlineSourcesWithSize` | PDF 按体积缩放并有下限 | — |
| `TestObservePromptTokensCalibratesEstimate` | 校准后的估算值跨过阈值 | 让 `calibrated` 直接返回原值 → 失败 |
| `TestObservePromptTokensIgnoresCompactedTurns` | 压缩过的轮次不污染校准比值 | — |
| `TestCircuitBreakerResetsAfterFailureWindow` | 窗口内保持打开、窗口外复位并能再次成功压缩 | 让复位条件恒假 → 失败 |
| `TestCooldownTurnsHasDefault` | 默认值存在且真的生效（第二次压缩被冷却拦下） | 删掉默认值 → 失败 |
| `TestForceCompactIgnoresThresholdAndCooldown` / `TestForceCompactHonorsDisabled` | 强制压缩绕过阈值与冷却，但不绕过显式关闭 | 让 `ForceCompact` 也吃冷却 → 失败 |
| `TestIsContextOverflowError` | 6 个 overflow 措辞 + 5 个非 overflow 错误 + nil + 包装错误 | — |
| `TestExtractFactsKeepsRecentFilesOverAlphabeticalOrder` | 最近触达的文件不再因字母序被截断，渲染顺序仍有序 | 恢复正向扫描 → 失败 |
| `TestDefsForCompactHandlesNilRegistry` | 无工具子代理不 panic（`*tools.Registry` 的 nil receiver + 接口装箱的 typed-nil 陷阱） | 首版实现踩了这个坑，被既有 golden 测试当场抓出 |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/compact ./internal/query ./internal/cli ./internal/agentruntime ./internal/server ./internal/goal -count=1` | 6/6 ok |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go test -race ./internal/compact ./internal/query ./internal/agentruntime -count=1` | 3/3 ok，无 race |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |
| mutation check（15 项，逐条移除修复后重跑对应测试） | 15/15 被测试抓到 |

### 修复证据 · AUDIT-P1-04 / P1-03（2026-07-26）

两条同属「循环与压缩语义」层，一次提交完成。

**AUDIT-P1-04 · loop guard 的三个缺口**

前置动作是把机制从 `internal/query/query.go` **下沉成 `internal/loopguard` 包**（`Tracker` + `CallSignature` / `TurnFingerprint` / `TextFingerprint` / `Describe` / `Awareness`）。理由是缺口 ③ 要求子代理也具备同一套判定，而需要共享的是 ~85 行（4 个指纹/标签 helper + 窗口计数 + 三档升级文案）—— 复制两份不可接受。下沉是**纯搬迁**：窗口(16)/软限(3)/硬限(6)、"首个重复记为 2" 的计数写法、剔除 `tool_id`、纳入结果内容这四条决策一字未动，既有 6 个 loop-guard 测试**未做任何修改**即全绿，这就是搬迁无行为漂移的证明。`renderLoopAwareness` 的单测随函数迁到 `loopguard.TestAwarenessTiers`（原处留注释指路）。

- **缺口 ①·纯文本循环**：`Session.run` 的 `len(toolUses) == 0` 分支被 completion gate 拦下后 `continue`，永远到不了工具执行之后的判定块。现在该分支用 `loopguard.TextFingerprint(拦截规则, 收尾文本)` 走**同一个 `Tracker`、同一个窗口**：streak 2 起在 runtime-status 带上升级的 `## Loop check`，streak 6 硬熔断并置 `StopReason=loop_guard_abort`。两条判定路径抽成 `run` 内的 `applyLoopGuard(fingerprint, label, auditSignature, recurring)` 闭包，保证工具轮与文本轮的升级/审计行为**不可能漂移**。把拦截规则拍进指纹，是为了让"同一段文本换了另一个理由被拦"算新情况而非重复。
- **缺口 ②·nudge 无限累积**：旧实现每轮把「模型草稿」和「gate reminder」**双双 append 进 `messages` 且从不移除**。现在草稿**回滚出 `messages`**（它本已从 UI / `result.Response` / transcript 三处撤回，`messages` 是最后一个漏的地方），reminder 改由 `pendingGateNudge` 变量承载、只在下一次请求组装时经 `withPendingGateNudge` 追加，模型改用工具后立即清空。**两件事必须一起做**：只删 reminder 会让两条 assistant 草稿相邻、破坏 role 交替。副作用是想要的——模型看不到自己刚被拒的草稿，因此更可能原样重新生成，恰好让缺口 ① 的指纹更容易识别出重复。
- **缺口 ③·子代理无 loop guard**：`Runtime.Run` 在工具结果入 `messages` 之后接入独立 `Tracker`（每个子代理 run 一个）。熔断落 `StatusFailed` + `sub-agent loop guard: no progress for N turns …`，`EventFailed` payload 带 `reason=loop_guard`。同时给 `subagentRuntimeStatusRequest` 加 `LoopStreak`/`LoopCall` 并渲染 `Loop check` —— 只加硬熔断会让子代理在毫无预警的情况下被杀，没有自纠机会。软限记 `agent.loop_guard_warn` 日志（子代理侧没有 closure event 通道）。
- **审计说法的一处收窄**：条目写"nudge 每轮 append……100 轮 = 100 条"。修完缺口 ① 之后，**文案完全相同**的重复其实在第 6 轮就熔断了，堆到 100 需要文案每轮微异（噪声）才做得到。所以缺口 ② 不是被 ① 覆盖的冗余修复，而是覆盖"熔断抓不住的带噪声循环"那一档——这也正是 §8 已知边界的第 1 条。
- **`internal/agentbudget` 不能替代它**：预算管"花掉多少"，熔断管"有没有进展"；一个不消耗多少 token 的纯文本空转照样跑满轮次。已写入 `docs/loop_guard.md` §9 的分层表（agentbudget 记为旁路而非中游）。

**AUDIT-P1-03 · `/compact` 语义反了**

- `internal/session/store.go` 的 `buildSummary` 改为**从最新 entry 反向收集、再按会话顺序输出**，即保留**最近**的对话、丢弃最旧的。渲染单条 entry 的分支抽成 `summaryLine`，正/反两个方向共用同一份文案，避免两套渲染漂移。
- 旧实现的截断标记 `[summary truncated]` 换成 `[older turns omitted to fit the summary budget]`，并**在预算里预留它的长度**，所以结果恒 ≤ `maxBytes`（旧实现实测会超：500 字节预算产出 520 字节）。
- **新增边界处理**：最新一条 entry 单独就超预算（12KB 默认预算下，一条大工具结果很常见）。旧实现在正向扫描下不会遇到，反向扫描会——处理是保留它的**头部**并用 `strings.ToValidUTF8` 丢掉被切成半个的 rune（旧实现的 `out.String()[:maxBytes]` 会留下坏字节）。
- 上一次 `compact_summary` 仍作为 header、它覆盖的 entry 仍不重复渲染（这部分行为不变，用测试钉住）。
- **刻意没做**：这条路径仍**不调用任何模型**，只是把 transcript 渲染成 `ROLE: text` 拼接。审计条目提到了这个事实，但本次只修"保留哪一端"这个语义反转；LLM 压缩已由 `internal/compact/`（auto-compact，AUDIT-P0-08 已修）承担，手动 `/compact` 要不要也走 LLM 是独立决策，不在本条范围。**未动 `internal/compact/`。**

**测试**（每条都验证「移除修复后失败、加上修复后通过」）

| 测试 | 覆盖 | mutation check（移除修复后的表现） |
| --- | --- | --- |
| `loopguard.TestTrackerTripsOnDocumentedTurns` | A / A-B / A-B-C-D 分别第 6 / 7 / 9 轮熔断 —— 直接钉住 `docs/loop_guard.md` §6 的轮次表 | 包不存在时编译失败；改动窗口计数即失败 |
| `loopguard.TestTrackerTripsOnOutOfOrderRepeats` | 乱序 A,B,B,A,A,B,A 第 7 轮 | — |
| `loopguard.TestTrackerNeverTripsWhenEveryTurnIsNew` | **反方向**：每轮都是新指纹 → 永不熔断 | — |
| `loopguard.TestTrackerForgetsPeriodsLongerThanWindow` | **反方向**：周期 17 > 窗口 16 → 按设计放行给 MaxTurns | — |
| `loopguard.TestTrackerResetsStreakAndWarningOnProgress` | 清零语义 + `TakeWarning` 每段区间只报一次、区间断开后可再报 | — |
| `loopguard.TestTrackerTreatsEmptyFingerprintAsProgress` | 指纹为空按有进展放行，不冤枉模型 | — |
| `loopguard.TestCallSignatureIgnoresToolIDButKeepsKeyOrderStable` | 钉住"剔除 tool_id"这条决策（否则熔断永不触发） | — |
| `loopguard.TestTurnFingerprintIncludesResults` | 钉住"纳入结果内容"这条决策（否则合法轮询被误杀） | — |
| `loopguard.TestDescribeNamesToolsWithoutIDs` / `TestAwarenessTiers` | 标签不泄漏 tool_id；三档升级文案 | 由 `query_test.go` 迁入 |
| `query.TestLoopGuardAbortsGatedTextOnlyLoop` | 纯文本循环：MaxTurns=30 下第 6 轮熔断、`StopReason=loop_guard_abort`、第 3 轮请求已带 `Loop check` | 回滚 `query.go` → `got nil`，跑满 30 轮 |
| `query.TestCompletionGateNudgeDoesNotAccumulateAcrossTurns` | 任一轮请求里 reminder 与被撤回草稿各至多 1 份，且 user/assistant 仍交替 | 回滚 `query.go` → 第 3 轮请求已有 2 份 reminder |
| `query.TestLoopGuardKeepsGatedTextRetriesThatChange` | **反方向**：每轮换一份不同的被拦文本不被熔断，最终正常收尾 | 修复前后均绿（证明新判定不过度触发） |
| `query.TestLoopGuard*`（既有 6 个） | 下沉后行为零漂移 | **测试文件未改一行**即全绿 |
| `agentruntime.TestSubagentLoopGuardAbortsIdenticalToolCallLoop` | 子代理：MaxTurns=30 下第 6 轮熔断，第 3 轮请求已带 `Loop check` | 回滚 `runtime.go` → `sub-agent max turns reached (30)` |
| `agentruntime.TestSubagentLoopGuardIgnoresPollingWithChangingResults` | **反方向**：子代理侧的合法轮询不被误杀 | 修复前后均绿 |
| `session.TestBuildCompactSummaryKeepsMostRecentTurns` | 40 轮 / 500 字节预算下保留 turn-039/040、丢弃 turn-001/002，且显式声明有省略、总长 ≤ 预算 | 回滚 `store.go` → "dropped the most recent turn turn-040" |
| `session.TestBuildCompactSummaryKeepsTurnsInChronologicalOrder` | 反向收集后仍按会话顺序输出 | 修复前后均绿（防反转把顺序也搞乱） |
| `session.TestBuildCompactSummaryCarriesPreviousSummaryAndRecentTail` | 上一次 summary 仍作 header、其覆盖的 entry 不重复渲染、最新一轮保留 | 回滚 → 保留的是新段里最旧的那批 |
| `session.TestBuildCompactSummaryKeepsHeadOfAnOversizedNewestEntry` | 单条超预算的最新 entry 保留头部而非整段丢弃，且总长 ≤ 预算 | 回滚 → 420 > 400 字节 |
| `session.TestBuildCompactSummaryRendersEverythingThatFits` | 小 transcript 逐字节不变（与既有 golden 互为双保险） | — |
| `cli.TestCompactSlashCommandKeepsTheRecentConversation` | **真实 `/compact` 路径**：400 轮 / 默认 12KB 预算，summary 含 turn-400、不含 turn-001 | 回滚 `store.go` → "dropped the newest turn" |

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/query ./internal/session ./internal/agentruntime ./internal/cli ./internal/loopguard -count=1` | 5/5 ok |
| `go test -race ./internal/query ./internal/agentruntime ./internal/loopguard -count=1` | 3/3 ok，无 race |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |
| mutation check（逐条 `git stash` 掉生产文件后重跑对应测试） | 见上表；`query.go` / `runtime.go` / `store.go` 三份回滚各自抓到对应测试 |

---

### 修复证据 · AUDIT-P1-07 / P1-08 / P1-09 / P1-10（2026-07-26）

四条都落在 `internal/anthropic/`（provider 边界），一起做是为了共用一次全量验证。

**AUDIT-P1-07 · thinking 块回传（唯一一条真 bug）**

- `internal/anthropic/client.go` 的 `sdkMessageParam` 新增 `case "thinking"` /
  `case "redacted_thinking"`。原来 `"thinking"` 落到 `default:` → `NewTextBlock(block.Text)`，
  而 thinking 块的 `Text` **恒为空**（正文在 `.Thinking`、签名在 `.Signature`），后果是
  丢 signature + 空 text 块被 API 以 `text content blocks must be non-empty` 拒掉。
- **没有 signature 的 thinking 块选择丢弃而不是回传**：Anthropic 一定会拒掉无签名的 thinking
  块，而 resume 老会话日志时确实可能取到这种块。丢掉它比发一个必错的块好；退回空 text 块
  则是原来那个 bug 本身。
- **顺带补齐 `redacted_thinking` 的另一半**：`streamResultFromSDKMessage` 原来只认
  `text`/`thinking`/`tool_use`，被安全系统打码的推理块整块丢弃，于是下一轮根本没有东西可回传。
  `ContentBlock` 因此新增 `Data` 字段（`omitempty`，对既有会话日志向后兼容）。
- **修的是 provider 层，没有碰 `internal/query/query.go` 的 agent loop**：回传路径本身是对的，
  错的是序列化。

**AUDIT-P1-08 · OpenAI-compatible 建流重试（PARTIAL）**

- 重试分类改成两套独立预算（`openAIStreamCreateRetries`）：429 沿用原来的 1 次，
  5xx / 408 / 409 / 无状态码的网络抖动新增 2 次、指数退避 500ms→8s 封顶。
  两套预算刻意分开 —— 量级差三个数量级，共用一个计数器会互相饿死。
- **`Retry-After` 现在真的被读了**：go-openai 的 `APIError` / `RequestError` 都只带状态码和
  body，响应头在别处拿不到，所以在 `httpTraceClient.Do` 这一层截下来，经 context 交给重试
  决策（`retryAfterCapture`）。同时支持秒数、HTTP-date 和 OpenAI 实际下发的 `retry-after-ms`。
  优先级：环境变量（测试注入）> 响应头 > 错误文案正则 > 65s 兜底。
- **和既有两层机制的边界都显式守住了，各自有测试**：
  - 不和 AUDIT-P0-07 的分段超时打架 —— `errProviderTimeout` 明确**不**重试。守卫主动取消请求
    说明网关卡住了，原地重试只会再赔一个 `responseHeader` 超时，该做的是尽快切 provider。
  - 不和 provider fallback 重复兜底 —— 明确的 4xx 不重试，持续性故障留给
    `canFallbackAfterError`；这一层只兜同一个 provider 上的瞬时抖动。
- **未做：熔断冷却。** 审计原文提的「fallback 失败后下一轮仍从 primary 重试」没有实现。
  原因是 `anthropic.NewClient` 在 `internal/cli` 有 4 个调用点、`internal/agenteval` 有 2 个，
  Client 的生命周期不统一，进程内状态未必跨轮存活；做对需要先定 Client 的所有权，属于独立
  改动而非本条的收尾。故本条记 **PARTIAL**。

**AUDIT-P1-09 · 静默丢弃三连**

- **`ResponseFormat`**：Anthropic 的 Messages API 没有 `response_format` 字段，唯一落地方式是
  写进 system prompt。现在在 `sdkMessageParams` 里补一个 system block，**放在 provider 边界上
  所有调用方都覆盖**，而不是指望每个上层各写一遍。
  - 带幂等哨兵 `responseFormatInstructionMarker`：`internal/server` 和 CLI 的 `--json-schema`
    已经各自塞过同样约束，共用这句话判断要不要再补，避免同一条指令出现两遍。
  - **诚实标注一处 wart**：指令文案在 `internal/anthropic` 和 `internal/server` 各有一份，
    靠哨兵句子耦合。没有让 server 委托过来，是因为 server 侧的类型转换 helper 住在
    `internal/cli/cmd_server.go`，跨三个包重排布的风险大于收益。
  - 事实修正：审计说「CLI/子代理路径完全没有」，实际 CLI 的 `--json-schema` 走
    `internal/cli/cli.go` 自己的注入；真正为空的是任何直接设 `ResponseFormat` 的调用方 ——
    而目前只有 server 路径会设。所以这条修的是**分层正确性**，不是当下的线上故障。
- **`ANTHROPIC_PROVIDER=bedrock` 死分支**：已删（`internal/query/query.go`
  的 `shouldUsePromptCache1hTTL`，3 行）。全仓 `ANTHROPIC_PROVIDER` 只有这一个读取点，
  而 `providerKindSupported` 只认 `anthropic*` / `openai*`，bedrock 会被直接拒，
  所以那条短路指向一个这个 runtime 根本连不上的 provider。删除处留了注释说明来龙去脉。
  这是本批唯一一处 `internal/query/query.go` 改动。
- **OpenAI 路径的 thinking 映射**：`openAIReasoningEffort` 把 `ThinkingConfig` 映射成
  `reasoning_effort`（先认 effort 名字，裸 token 数则按预算分档，档位与 `thinkingBudgetTokens`
  对齐）。字段带 `omitempty`，**只在调用方显式配了 thinking 时才出现在请求体里** ——
  不支持推理参数的网关不会因为这个改动突然收到未知字段。

**AUDIT-P1-10 · `parseSSE` 的去留：删掉**

- `parseSSE`（约 165 行）连同 `rawEvent`、`mergeUsage`（删掉 `parseSSE` 后成为孤儿）
  和两个只测它的测试一起删除，共 -239 行生产代码 / -109 行测试。
- **为什么删而不是接回生产**：接回去意味着用自己手写的 SSE 解析器替换 SDK 已经在维护的那套
  （`ssestream` 处理了 `event: error`、rich API error、多行 data 拼接）。留着它则是维护第二份
  会和 SDK 漂移的 Anthropic SSE 解析器，且让两个测试持续提供虚假信心。两条路都比删更差。
- **生产路径的 mid-stream error 补了真实覆盖**，且过程中把两半分开了：
  - **Anthropic 路径本来就是对的** —— SDK 的 `ssestream` 认 `event: error` 并写进
    `stream.Err()`。这半边缺的**只是测试**（正是审计说的「生产 mid-stream error event 零覆盖」）。
    新增两个测试：已吐文本时错误包成 `PartialStreamError` 且 partial 保留；未吐文本时
    overloaded 能正常切 fallback。两个都在**未改任何生产代码**的情况下由新测试直接变绿。
  - **OpenAI 路径确有真 bug** —— go-openai 只认 `data: {"error":` 这种「error 是整行首个 key」
    的形式，`ChatCompletionStreamResponse` 里也没有 `error` 字段建模。网关把错误塞进一个形状
    正常的 chunk（`{"id":...,"choices":[],"error":{...}}`）时，反序列化后 `Choices` 为空，
    读循环只看 `Choices`/`Usage`，于是**静默产出空响应**。改为走 `stream.RecvRaw()` + 自己
    反序列化到内嵌 `error` 字段的 `openAIStreamChunk`。
    相邻那半边（错误单独成行）go-openai 自己认得，另加一个测试锁住别在改读循环时弄丢。

**测试与 mutation check**

新增 4 个测试文件、17 个测试函数（含子测试 22 项）。每条修复都做了「移除修复后重跑」验证：

| mutation | 受影响测试 | 结果 |
| --- | --- | --- |
| `sdkMessageParam` 去掉 `case "thinking"` | `TestThinkingBlockRoundTripsSignatureToAnthropic` | FAIL：回传块变成 `{Type:text Text:}`，signature 丢失 |
| `streamResultFromSDKMessage` 去掉 `redacted_thinking` | `TestRedactedThinkingBlockRoundTripsToAnthropic` | FAIL：首轮 content 为空 |
| 不丢无签名 thinking 块 | `TestUnsignedThinkingBlockIsDroppedNotSentAsEmptyText` | FAIL：多出一个空 text 块 |
| `openAIStreamCreateTransientError` 恒返回 false | `...RetriesServerError` / `...RetriesNetworkError` | FAIL：503 与 EOF 直接返错 |
| `openAIRateLimitRetryDelay` 去掉响应头分支 | `...HonorsRetryAfterHeader` | FAIL：退回 65s 兜底，测试超时 |
| 去掉 system block 注入 | `...InjectsResponseFormatInstruction` ×2 | FAIL：system 里没有约束 |
| 去掉 `params.ReasoningEffort` 赋值 | `...MapsThinkingToReasoningEffort` / `...OnTheWire` | FAIL：字段为空 |

两个测试是**边界守卫而非修复锁**，本身在修复前后都绿，作用是防后续回归：
`TestAnthropicPathDoesNotDuplicateResponseFormatInstruction`（指令不重复）、
`TestAnthropicPathWithoutResponseFormatLeavesSystemAlone`（没配就不加）。

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/anthropic ./internal/query ./internal/server -count=1` | 3/3 ok |
| `go test -race ./internal/anthropic -count=1` | ok，无 race（首轮抓到一个测试自身的 race：卡住的 handler goroutine 与测试读同一个计数器，已改 `atomic.Int32`） |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

### 修复证据 · AUDIT-P1-11 / P1-12（2026-07-26）

两条同属「说是检索/过滤，实际不是」—— 都不是缺功能，而是**每轮 prompt 都在生效的真 bug**。
**明确不做**：embedding / 向量检索。那是 TODO-025 的既定取舍（「先用 MySQL LIKE/简单评分检索」），
本次只修「声称是检索、实际退化成子串匹配」和「声称能过滤、实际过滤不了」。

**AUDIT-P1-11 ① FULLTEXT 缺 ngram parser（TODO-075）**

- `migrations/mysql/000003` 建的是普通 `FULLTEXT`。MySQL 默认分词器只按空格和标点切词，
  中文不产生 token → 中文 query 的 `MATCH` **恒为 0**。
- 不能改已应用的 migration，新增
  [migrations/mysql/000009_knowledge_fulltext_ngram.up.sql](../../migrations/mysql/000009_knowledge_fulltext_ngram.up.sql)：
  `DROP INDEX ft_knowledge_chunks_content` → `ADD FULLTEXT KEY ... WITH PARSER ngram`；down 还原为普通 parser。
- **`ngram_token_size` 是服务器级只读参数，migration 改不了**，必须由 DBA 在建索引**之前**写进
  `my.cnf` 并重启，顺序反了要 DROP 重建。前提、限制（短于 `ngram_token_size` 的 query 仍命不中）
  和验证方法写在 [docs/deployment/mysql_fulltext_ngram.md](../deployment/mysql_fulltext_ngram.md)，
  并从生产部署 checklist 的数据库小节指了过去。
- **实测边界比审计描述更糟**：审计说「退化成无排序全表 LIKE」，实际是**返回 0 行**——
  见下面 ③，LIKE 的 pattern 是整条 prompt，永远匹配不上任何 chunk。

**AUDIT-P1-11 ② KB 分块按字节切 UTF-8（TODO-076）**

- `internal/tenant/service.go` 的 `buildKnowledgeChunks` 里 `maxChunkChars = 1200` 名为字符，
  实际用 `len(paragraph)`（字节）判断并用字节切片截取。后果两重：中文分块**碎字**（写进 MySQL 的是
  replacement character），且中文实际只拿到约 1/3 的分块预算。
- 改为 `utf8.RuneCountInString` 计数 + 新增 `splitAtRune` 按 rune 起始偏移切分。

**AUDIT-P1-11 ③ 检索串是整条 prompt（TODO-079，审计未列，评估后决定做）**

- `SearchKnowledgeChunks` 把整条用户 prompt 同时当作 `MATCH` 参数**和** `LIKE '%…%'` 的 pattern。
  真实 prompt 比任何 chunk 都长 → LIKE 兜底**恒不命中**，所以 ngram 之前中文检索不是"排序差"而是"没有结果"。
- 新增 [internal/storage/mysql/knowledge_search.go](../../internal/storage/mysql/knowledge_search.go)：
  `buildKnowledgeSearchQuery` 按非字母数字切词、去重、丢掉单字与超长子句，最多 8 个词项做 LIKE；
  `MATCH` 参数用清洗后的全串并按 rune 边界截到 512 字节。评分口径不变（MATCH×20 + content LIKE 10 + title LIKE 3）。
- **不做**中文 ngram 词项合成：那会让 LIKE 命中一切，反而毁掉排序。中文召回质量仍然取决于 ngram FULLTEXT，
  LIKE 只是 best-effort 兜底，文档里写明了。
- **不做**：code 模式接入 KB 检索。KB 检索目前只在 chat 模式（`internal/server/tenant_context.go`）执行 ——
  这是功能缺口而非虚假声称，且要先解决 prompt 预算与相关性门槛，属于产品决策，另立条目。

**AUDIT-P1-12 ① `paths:` frontmatter 事实失效（TODO-077）**

- 旧 `promptMatchesPattern` 是 `strings.Contains(prompt, needle)` —— 匹配**用户输入的字符串**，不是文件集。
  三重后果：只有字面输入目录名才生效；任何含该子串的散文误命中；pattern 中间的通配符永远匹配不上
  （只剥结尾的 `/**` `/*`，`internal/**/*_test.go` 被整串当子串找）。
- 新增 [internal/memory/scope.go](../../internal/memory/scope.go)：从 prompt 抽出路径形态 token
  （含 `/` 或有扩展名，剥 `@` 前缀、`:行号` 后缀、引号括号，跳过 URL）构成本轮文件集，
  再用真 glob 匹配（`**` 跨分隔符、其余段 `path.Match`、无通配符 pattern 覆盖整棵子树）。
- **作用域未知时不隐藏**：识别不出路径 → 文件集为空 → 带 `paths:` 的文档照常加载。
  作用域未知不等于不匹配，靠猜测藏掉用户写下的指导代价更高。`excludes:` 反之从严（任一文件命中即丢弃）。
- **未做，且需要前置条件**：`paths:` 的理想语义是「本轮**实际改到**的文件」，但 frontmatter 在**装配提示词时**
  求值 —— 那时一次工具调用都没发生，将要读写的文件**尚不存在**，可用信息只有 prompt。
  要覆盖「用户只说『修一下检索的 bug』、Agent 随后编辑 `internal/memory/memory.go`」需要三步跨层改动：
  ①`query.Session` 维护本轮文件集（`tools.FileChange` + 读类工具 input path）；②回灌给 memory 层；
  ③**在一轮之内重新求值 frontmatter 并重建 system addendum**。第三步最重 —— 当前 addendum 只在 turn
  起点装配一次，改成中途可变会影响 prompt 缓存命中和 `codePromptReport` 语义。明确超出本条范围，另立条目。
  一并评估过给 `LoadCodeOptions` 加 `Files` 字段做接缝：**放弃**，query 层唯一能给的是
  `s.options.Attachments`（截图临时文件路径），传进去会让文件集非空却全是 `/tmp` 路径，
  反而**错误地收窄**记忆 —— 无调用方的选项比没有选项更糟。

**AUDIT-P1-12 ② 记忆从不截断（TODO-078）**

- 字节预算只对 `Type=="Workflow"` 生效，`CLAUDE.md`/user/team/managed/项目记忆**从不截断**，
  且全局无总字节上限。
- 现在三层预算，均以字节计、设 `0` 关闭：workflow 结构化压缩 4096（不变）→
  单文档 `GOLANG_CLAUDE_CODE_MEMORY_DOCUMENT_BUDGET_BYTES=16384` → 合计
  `GOLANG_CLAUDE_CODE_MEMORY_PROMPT_BUDGET_BYTES=65536`。文档按优先级顺序消耗总预算，
  靠前的保留正文，预算耗尽后靠后的收缩成一行指针（保留引用不保留字节）。
- 截断处一定有显式标记并指出该读哪个文件：
  `[Memory truncated for prompt budget] N bytes total; read <path> before relying on rules not shown here.`
- 截断落在行边界或 rune 边界上。顺带修掉同类缺陷：`truncateAtLineBoundary` 无换行时会按字节切断
  （中文规则碎字），`limitProjectMemoryContent` 的 25KB 上限同样是裸字节切片。
- `status --json` 的 `workflowBudgetedDocuments` 此前统计的是**全部**被预算的文档；预算扩展到所有类型后
  这个 key 会名不符实，故改为只数 workflow，并新增 `memoryDocumentBudgetBytes`、
  `memoryPromptBudgetBytes`、`memoryBudgetedDocuments`。
- 语义与开关见 [docs/prompt_logic/memory_scope_and_budget.md](../prompt_logic/memory_scope_and_budget.md)。

**测试：每条都验证过「移除修复后失败、加上修复后通过」**

| 测试 | 移除修复后的失败 |
| --- | --- |
| `TestBuildKnowledgeChunksKeepsChineseRunesIntact` | `chunk 0 is not valid UTF-8: "…知识库检\xe7\xb4"` |
| `TestBuildKnowledgeChunksCountsCharactersNotBytes` | `1200 CJK characters should fit one chunk, got 3 chunks` |
| `TestBuildKnowledgeChunksSplitsOversizedParagraphsOnCharacterBudget` | `should split into 2 chunks, got 4` |
| `TestKnowledgeChunkFulltextIndexUsesNgramParser` | migration 000009 不存在 |
| `TestSearchKnowledgeChunksScoresOnMatchAndPerTermLike` | sqlmock 打出旧 SQL：`LIKE '%知识库 检索？%'`（整条 prompt 当 pattern），`expected 17, but got 13 arguments` |
| `TestBuildKnowledgeSearchQuery*`（5 条） | 函数不存在（编译失败） |
| `TestLoadCodeDoesNotMatchPatternAsBareSubstring` | `root-anchored pattern matched a nested path`（`api/**` 命中 `docs/api/README.md`） |
| `TestLoadCodeDoesNotMatchPathPrefixAcrossSegmentBoundaries` | `segment-crossing prefix matched`（命中 `internal/memory-mapped/cache.go`） |
| `TestLoadCodeHonoursDoubleStarAndSuffixGlobs` | `mid-pattern ** did not match` |
| `TestLoadCodeKeepsPathScopedDocumentsWhenNoFilesAreKnown` | `path-scoped doc was hidden although no file was identifiable` |
| `TestPromptFileCandidates*`、`TestPathMatchesPatternSubtreeAndExactForms` | 函数不存在（编译失败） |
| `TestPreparePromptDocumentsTruncatesLargeProjectMemory` | `prompt bytes = 104007, want <= 2048` |
| `TestPreparePromptDocumentsTruncatesEveryDocumentType` | `Managed kept 8000 bytes, want <= 1024` |
| `TestPreparePromptDocumentsEnforcesTotalBudget` | `document 0 has no marker after the budget ran out` |
| `TestPreparePromptDocumentsTruncationIsRuneSafe` | `truncation split a rune: "…中文规则很长中\xe6…"` |
| `TestLimitProjectMemoryContentIsRuneSafe` | `project memory cap split a rune at the end` |
| `TestStatusContextSeparatesWorkflowAndMemoryBudgets` | 函数不存在（编译失败） |

对 `paths:` 那 4 条 LoadCode 级测试，验证方式是把 `documentLoader.matches` 与 `promptMatchesPattern`
原样还原（文件集拼回一个字符串喂给旧匹配器）后重跑，4/4 全红；对预算那几条是摘掉通用截断分支重跑。

**验收命令实跑**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/memory ./internal/tenant ./internal/storage/... ./internal/server ./internal/query -count=1` | 全 ok |
| `go test ./... -count=1` | 全 ok，0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

**未真机验证**：ngram FULLTEXT 那条。本机没有 MySQL（`mysql`/`mysqld`/`docker` 全无），
无法起一个配好 `ngram_token_size` 的服务器。因此提供两层证据：
in-process 用 sqlmock 断言生成的 SQL 确实走 `MATCH` 分支且 LIKE 是逐词项的（不是整条 prompt）；
真机侧留 opt-in e2e `TestMySQLE2EKnowledgeChineseSearchUsesFulltext`，对中文 query 断言
`search_mode == "fulltext"`、`score > 0`、存回的 chunk 是合法 UTF-8 —— 走不到 MATCH 就会失败。
配好 `GOLANG_CLAUDE_CODE_MYSQL_E2E_DSN` 后 `scripts/tenant-mysql-e2e.sh` 会连带跑到（`-run 'TestMySQLE2E'` 匹配）。


---

## 5. P1 · 工具层与 MCP

| ID | 状态 | 模块 | 问题 | 证据 |
| --- | --- | --- | --- | --- |
| AUDIT-P1-13 | DONE | Tools | ~~**缺 `BashOutput` / `KillShell`** —— 有后台任务 store 和 `run_in_background`，但模型只能靠 `Read(log_path)` 轮询，**没有任何工具能杀掉后台进程**~~（DONE，见 [修复证据](#修复证据--audit-p1-132026-07-26)：新增 `BashOutput`（持久化读游标的增量读取 + 真实进程存活探测）与 `KillShell`（killed / not_running / not_found 三态），走 `coreRuntimeTools` → `GuardAll` 权限链路，并把 Bash 后台返回体与描述从「读 log_path」改指向 `BashOutput`） | 全量工具名清单核实无此二者 |
| AUDIT-P1-14 | DONE | Tools | **`WebBrowser` 的 `screenshot` 是假的** —— 合成一段只含标题和 URL 的 SVG 再 base64 返回，从不渲染任何东西，模型会以为拿到了页面图像（DONE，见 [完成记录](#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26)：fallback 模式下 `screenshot` 明确报错并给出启用真浏览器的两个 env，合成 SVG 的 `screenshot()`/`escapeXML()` 已删；工具描述改为写明「两种模式」） | `internal/tools/webbrowser/webbrowser.go:517-526` |
| AUDIT-P1-15 | DONE | Tools | 三处名不副实/有损：`LSP` 不启 language server，是进程内 `go/parser`、仅支持 Go、`references()` 是裸字符串匹配；`WebSearch` 抓 DuckDuckGo HTML 且正则抓页面上**每一个** `<a href>`（导航/页脚会被当结果）；`NotebookEdit` 静默丢掉 nbformat 4.5 必需的 cell `id` 和所有未知键，产出 schema 非法的 notebook（DONE，见 [完成记录](#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26)：notebook 改为保留整份文档 + `UseNumber()`，round-trip 不丢 `id`/未知键/数字精度；WebSearch 抓取限定到 `result__a` 结果容器，无标记的自定义端点回退到「绝对的站外链接」；LSP 与 WebSearch 的描述改为如实陈述实现） | `internal/tools/lsp/lsp.go:136,181,231-243`；`internal/tools/websearch/websearch.go:213-233`；`internal/tools/notebook/notebook.go:20-26` |
| AUDIT-P1-16 | DONE | Hooks/Secrets | **Hooks 是权限 / 沙箱 / 脱敏的三重旁路**：拿完整父进程环境（含 `ANTHROPIC_API_KEY`，而 Bash 工具明确剥离了 `providerSecretEnvKeys`）、直接 `/bin/sh -c` 不过沙箱不过策略、可来自项目级 `.claude/settings.json`（clone 一个仓库即可能被攻击者控制）。同类 env 泄漏：powershell / workflow / webbrowser / mcp stdio。三套互不一致的脱敏名单，`GITHUB_TOKEN` / `AWS_SECRET_ACCESS_KEY` / `OPENAI_API_KEY` 全部穿透（DONE，见 [完成记录](#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26)：新增 `internal/procenv` 作为唯一脱敏实现，bash/hooks/powershell/workflow/webbrowser/mcp 六处复用；名单改成**按名字形状**匹配而非枚举，三个漏网的 key 全部拦下。**沙箱与策略旁路两条未做**，见完成记录的「明确未做」） | `internal/hooks/hooks.go:178`；对照 `internal/tools/bash/bash.go:334-343,357`；`internal/tools/powershell/powershell.go:80`、`internal/tools/workflow/workflow.go:206`、`internal/mcp/client.go:49` |
| AUDIT-P1-17 | DONE | MCP | **stdio 完全无超时**：持锁做阻塞 `ReadBytes('\n')`，无 deadline 无 context → server 挂起 = 调用方 goroutine 永久死锁。**实践中最可能咬人的一条**。且 stderr 被丢弃、加载失败裸 `continue` 无日志 → 配错的 server 完全静默失败，可调试性为零；`StartStdio` 从不 `cmd.Wait()`，崩溃后工具永久注册永久报错并漏僵尸进程 | `internal/mcp/rpc.go:38-74`；`internal/mcp/client.go:43-73`；`internal/mcp/tool.go:176-184` |
| AUDIT-P1-18 | DONE | MCP | ~~协议完成度缺口：无 SSE 传输、无 `Mcp-Session-Id`、无 protocolVersion 协商、无通知机制、无分页、无 OAuth、非文本内容静默丢弃、命名冲突静默覆盖、取消不传播~~（八项已闭合见 [完成记录](#完成记录audit-p1-17--p1-182026-07-26mcp-可靠性与协议完成度)，旧版 SSE 传输 / OAuth / `tools/list_changed` 热替换三项**明确不做**且理由未变；**非文本内容的另一半已补完**，见 [本条收尾](#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)：白名单图片类型升级为真正的 image 内容块，经 `Result.ContextMessages` 送达模型（`internal/query/query.go` 0 行改动），带三道 base64 上限，超限与不可投递的仍留占位行且写明原因；音频因 Messages API 无 audio 块永远留占位行。TODO-061 闭合；resume 保真登记为 TODO-080） | `internal/mcp/media.go`；`internal/mcp/client.go`；`internal/mcp/tool.go` |
| AUDIT-P1-19 | DONE | Tools/Coverage | ~~覆盖率洼地正好在工具执行面：`mcpresources` 12.0%、`powershell` 18.0%、`worktree` 45.2%、`workflow` 50.9%、`taskoutput` 56.2%。另 5 个工具缺 `MaxResultSizeChars()`；`Task` 的 InputSchema 无顶层 `required[]`~~（覆盖率与 `MaxResultSizeChars` 见 [完成记录](#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26工具诚实性)；**顶层 `required[]` 已决策**，见 [本条收尾](#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)：不用根级 `anyOf`（各 provider 的 tool schema 校验兼容性不一），改为 `validateTaskForm` 在代码里挡掉「两种形式都不完整」与「两种形式同时给」并给模型可照改的错误；schema 文案写明二选一） | `go test -cover` 实测；`internal/tools/task/task.go` |
| | | | 覆盖率四条与 `MaxResultSizeChars` 已闭合（见 [完成记录](#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26)）；`Read` 经核实是**故意**不进预算（`SkipToolResultBudget()`，自己按 2000 行/manifest 限流），已改为锁定该意图而非补一个永远走不到的 `MaxResultSizeChars`。**`Task` 的顶层 `required[]` 未做**：单任务与 `tasks` 批量是二选一，写死 `required:[description,prompt]` 会禁掉批量形式，正解是根级 `anyOf`，但那有 provider 兼容风险，需单独决策 | |
| AUDIT-P1-35 | DONE | Sandbox/Observability | ~~**沙箱可执行性从不上报给用户**~~（DONE，见 [修复证据](#修复证据--audit-p1-352026-07-26)）。`sandbox.IsAvailable` 与 `sandbox.UnavailableReason` 全仓零调用方；用户可见的沙箱状态只有 `tuiSandboxLabel`，而它直接读 settings（`Network.Disabled` 有值就打印 `network`），不反映 sandbox-exec/bwrap 是否存在、也不反映 AUDIT-P0-04 新增的 `NetworkPolicyGaps`。结果是「配了就以为生效」——AUDIT-P0-04 的显式上报目前只到 API 层 | `internal/sandbox/runtime.go:113`（IsAvailable 无调用方）、`internal/sandbox/runtime.go:124`（UnavailableReason 无调用方）、`internal/cli/interactive.go:685`（tuiSandboxLabel 只读 settings） | `tuiSandboxLabel` 改为基于 `tools.SandboxConfig` 判定，沙箱二进制缺失或存在 `NetworkPolicyGaps` 时显式降级标注；`/status` 与 TUI 欢迎卡同时展示 `UnavailableReason`；补 label 降级与 gap 上报的回归测试 |
| AUDIT-P1-37 | DONE | Sandbox | ~~**跨平台沙箱选项在不支持的平台上静默空操作，标签却照旧显示**~~（DONE，见 [修复证据](#修复证据--audit-p1-372026-07-26)）。`sandbox.seccomp.enabled` 全仓只有两个读取点，都在 linux 分支（`linuxSandboxSpec` 拼 `--seccomp` fd、`linuxSandboxEnv` 置 `SANDBOX_SECCOMP`；`createSeccompProfileFile` 的唯一调用点也在前者），**macOS profile 完全不看它** —— seccomp 是 Linux 内核特性，在 darwin 上不可能生效。但 [AUDIT-P1-35](#修复证据--audit-p1-352026-07-26) 修完后标签仍无条件为 `SeccompEnabled` 打印 `seccomp`，macOS 用户看到 `on/seccomp/network` 会以为系统调用过滤是开着的 —— 与 P1-35 同一类错觉，只是 P1-35 的验收范围限于 `NetworkPolicyGaps` 契约内的网络项，非网络的跨平台空操作没纳入。**镜像情形**：`sandbox.allowPty` 只被 `macOSSandboxProfile` 读取，linux 侧从不查询该选项（bwrap 下 pty 可达性实际由 `--dev /dev` 决定，与该配置无关），只是它当前不进标签，所以是配置层面的空承诺而非标签层面的谎报。**建议**：把「本平台不支持的选项」并入 `sandbox.Describe` 的降级判定（`PolicyGap` 已经是结构化的，加一类 `platform` gap 即可），`seccomp` 只在 linux 且真的拼进 bwrap 参数时才出现在标签里；`allowPty` 同理需要在 linux 侧明确「未实现」而不是沉默。 | `internal/sandbox/runtime.go:153`、`:181`（`SeccompEnabled` 仅此两处读取，均在 linux 分支）；`internal/sandbox/seccomp.go:40`（`createSeccompProfileFile` 唯一调用点在 `:154`）；`internal/sandbox/runtime.go:368`（`AllowPty` 仅在 `macOSSandboxProfile` 内，函数起于 `:329`）；`internal/sandbox/state.go:128`（标签无条件打印 `seccomp`） |
| AUDIT-P1-20 | DONE | Subagent | ~~`SendMessage` 被强制 deny 但 `AgentMessage` 没有~~（DONE，随 [AUDIT-P0-14](#修复证据--audit-p0-142026-07-25) 修复）；~~batch 重试对 `context.Canceled` 也照重试并丢弃 partial~~（DONE，同上）；~~batch 并发的多个子代理**写同一棵工作树无锁**；单 Task 无重试而 batch 有~~（DONE，见 [本条收尾](#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)：真正的并发 bug 在 `agentworktree` —— `NewSlug` 只用纳秒时间戳，并发调用会撞同一个 slug，而 `Create` 对已存在的树是**复用**，于是两个子代理无声共用一棵树；已补进程级 slug 计数器 + 每 gitRoot 一把互斥锁。Task batch 侧不加锁（没有正确的粒度），改为 schema 与工具描述不再谎称 `isolated`，per-item 工作树隔离登记为 TODO-081。单 Task 与 batch 现在共用同一个 `worthRetrying`、同一套退避与 clamp） | `internal/tools/task/task.go`；`internal/agentworktree/lock.go` |

---

## 6. P1 · 服务端与多实例

| ID | 状态 | 模块 | 问题 | 证据 |
| --- | --- | --- | --- | --- |
| AUDIT-P1-21 | DONE | Server/Auth | 静态 token **空值 fail-open**（`if token == "" { return true }`）+ 非常量时间比较；单一共享 token 无过期无轮转无吊销。**且 token 可通过 `?token=` query 传递**（会进 Nginx access log / 浏览器历史 / Referer），而 `authorizeTrace` 守的是 `/trace/api/sessions*`（完整会话）和 `/prompt-dump/api/records`（**完整 prompt 正文**）—— token 泄漏 = 全量对话泄漏 | `internal/server/server.go:1285-1298`；`internal/server/trace.go:268-276`；`internal/server/webui.go:56-75`；`internal/server/prompt_dump.go:126-149` |
| AUDIT-P1-22 | DONE | Server/Health | `/health` 单端点，不区分 liveness/readiness、不探 MySQL/Redis、**且需要 Bearer token**（k8s probe 与 LB 健康检查都要注入 token）。Redis 无启动探活（只 `NewClient` 不 Ping） | `internal/server/server.go:274-284`；`internal/quota/redis.go:89-92`；`internal/server/mobile_redis.go:89-92` |
| AUDIT-P1-23 | TODO | Observability | **无延迟分位**（`MetricsSink` 只有 4 个 counter + `DurationSum`，只能算平均值，无 histogram/summary）；**无队列深度/并发度指标**（活跃 SSE 数、在跑 task 数、DB 连接池使用率全无）；**无 pprof / expvar**；**只有日志导出无 trace 导出**（go.mod 无 opentelemetry，`otelLogPayload` 产的是 OTLP 日志不是 span） | `internal/telemetry/metrics.go:23-28,79-90`；`internal/telemetry/sinks.go:175+` |
| AUDIT-P1-24 | DONE | Observability | ~~**所有 telemetry sink 同步阻塞在请求路径上**~~（DONE，见 [修复证据](#修复证据--audit-p0-11--p0-12--p1-24--p1-262026-07-26)：慢 sink 走 `telemetry.AsyncSink` 有界缓冲 + 后台 worker，退出时 flush 保证不丢事件）**所有 telemetry sink 同步阻塞在请求路径上**：顺序遍历、无 buffer 无 batch 无 async，其中 `RecorderSink.Emit` 是一次 MySQL INSERT、`HTTPSink.Emit` 是一次同步 HTTP POST。每请求 2 个事件 = **每请求 2 次同步 DB 写 + 2 次同步外呼**，外部 APM 抖动直接变成 API 延迟 | `internal/telemetry/telemetry.go:90-101`；`internal/telemetry/sinks.go:86-95,125-157` |
| AUDIT-P1-25 | TODO | Server/Scale | **当前只能单实例跑**。进程内状态清单：①`agenttasks.Controller.cancels` map → cancel 落到非执行实例静默失败；②`mobile_streams` 的 `cancels`/`subscribers`/`goalSubs` → 跨实例停止生成失效、WS 推送收不到别 pod 的事件；③`AgentTaskPermissionRegistry.pending` map → 权限确认打到别实例则 agent 卡到 idle timeout；④未配 Redis 时 quota/limiter 默认内存 → N 实例 N 倍配额，**且无启动告警**；⑤每实例 fork 一个本地 scheduler daemon → 定时任务重复执行、PID 文件互相打架、daemon 孤儿化 | `internal/agenttasks/controller.go:8-14`；`internal/server/mobile_streams.go:17-22`；`internal/server/handlers_agent_tasks.go:1363-1370`；`internal/server/server.go:250-259`、`:224-231`；`internal/scheduler/scheduler.go:514-545` |
| AUDIT-P1-27 | DONE | Server/Quota | 配额可被完全绕过：`reserveQueryQuota` 开头 `if opts.TenantService == nil \|\| !tenantPersistenceRequested(ctx) { return }`，而后者要求 tenant/user header 非默认值 → **不带 `X-Tenant-Key` 的调用者绕过全部配额**，且 `/query` 与 `/v1/chat/completions` 无任何 IP/全局兜底限流。另：请求体的 `cwd` 无 allowlist 无 workspace 根约束，直接决定服务端工具执行目录 | `internal/server/server.go:997-999`、`:906-908`；`internal/cli/cmd_server.go:175-177` |

| AUDIT-P1-26 | DONE | Storage | （LIKE 与两处 N+1 见 [修复证据](#修复证据--audit-p0-11--p0-12--p1-24--p1-262026-07-26)；事务边界一项见 [修复证据](#修复证据--audit-p1-26-事务边界2026-07-26)）前导通配 LIKE 打在最大的两张表上（telemetry 10 列 OR、audit 4 列 OR，pattern 是 `%x%`，索引全废），而 timeline 明明拿着精确的 trace_id/session_id 却走这条路径退化成扫表；N+1：conversation 逐 task 查事件；事务边界稀疏（全局关 GORM 默认事务，全库仅 2 处显式事务，`persistTenantQuery` 连写 3 张表不在同一事务） | `internal/storage/mysql/gorm_repository.go:1790,1853`；`internal/server/timeline.go:120-135,171-215`；`internal/server/handlers_sessions.go:287-296`；`internal/server/server.go:529-568` |

---

## 7. P1 · 第一公里（新用户第一印象）

| ID | 状态 | 模块 | 问题 | 证据 |
| --- | --- | --- | --- | --- |
| AUDIT-P1-28 | DONE | UX/CLI | **首次运行喷 19 行 JSON + 3 段 Go stacktrace**，真正有用的 `ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN is required` 在最后一行。默认 log level=info；设 `GOLANG_CLAUDE_CODE_LOG_LEVEL=error` 仍有 5 行带 stacktrace；设 `=fatal` **不被识别、落回 Info 又是 19 行**；该环境变量 README 中 0 次提及 | 实测；`internal/observability/context.go:259-268` |
| AUDIT-P1-29 | DONE | Docs | **README 第一条命令曾指向作者本机项目目录**，且使用旧仓库名 `golang-claude-code`。同类陈旧路径当时全仓 **1108 处 / 22 个文件** | `README.md:10` |
| AUDIT-P1-30 | DONE | Release | ~~**零安装路径**：无 install.sh / brew / goreleaser / Dockerfile / Makefile，README 与 docs 中 `go install` / `brew install` / `curl \| sh` 命中数为 0。已有的只是 `scripts/build.sh`（19 行，`git describe` 注入版本）。版本机制本身是真实的（31 个 tag）~~（DONE，见 [修复证据](#修复证据--audit-p1-30--p1-342026-07-26)：新增 `scripts/install.sh`（源码装到 PATH）、`scripts/release.sh`（5 平台归档 + SHA256SUMS）、`.github/workflows/release.yml`（tag 触发，自校验产物 `--version`），README 补 [安装](../../README.md#安装) 章节。两个脚本都 shell out 到 `build.sh`，全仓仍只有一处 `-X ...cli.Version`。`go install` / `brew` / `curl \| sh` **明确不做**并写清原因） | 仓库根目录 |
| AUDIT-P1-31 | DONE | CLI | **所有子命令都没有 `--help`**：`parseArgs` 在首个非 `-` token 处停止扫描，`--help` 被透传 → `session --help` 报 `unknown session command: --help`，`goal/skills/mcp/plugin/hooks/agents/tenant/transcript` 同样。另：三份互相漂移的命令清单（dispatch switch / help 字符串 / completion 列表，`completion zsh` 缺 `goal`/`goals`/`transcript`/`eval`）；`--fork-session` 是死 flag（只影响一个显示字符串）；19 个真实命令在文档中 0 条可运行示例 | 实测；`internal/cli/parse.go:255-256`；`internal/cli/cli.go:97,148,3939,5204`；`internal/cli/interactive.go:709` |
| AUDIT-P1-32 | DONE | WebUI | ~~**Web UI 出厂不可用**：`web/dist` 被 gitignore 且**未 `go:embed`**（对照 `/trace` 和 `/prompt-dump` 两个 viewer 是 embed 的），`GOLANG_CLAUDE_CODE_WEBUI_DIR` 为空则整个 `/webui` 路由不注册 —— 用户拿到 404 且无任何提示。叠加 Node 20+ 要求，本机既跑不了也 build 不了~~（DONE，见 [修复证据](#修复证据--audit-p1-32--p2-10--p2-052026-07-26)：`/webui` 改为无条件注册，未 build 时返回 503 + 内嵌的可操作构建说明页；`WEBUI_DIR` 未设置时自动发现 CWD / 可执行文件旁的 `web/dist`；配置了但缺 `index.html` 同样落到说明页。Vite 产物本身**不** embed，理由见修复证据） | `internal/server/webui.go:16-18` |
| AUDIT-P1-33 | TODO | i18n | 按界面割裂且用户无法选择：CLI help 与全部 CLI 错误 **100% 英文**（`cli.go` CJK 字面量 0），**TUI 硬编码中文**（`app.go` CJK 字面量 178），CLI interactive 混杂（`interactive.go:1706` 字面写 `"Slash commands / 斜杠命令:"`，参数占位符都是中文），Web 有 en/zh 切换。**Go 侧完全无 i18n 机制**，`Settings.Language` 只喂给模型 system prompt，对程序自身输出零影响，且无 `--language` flag | `internal/cli/interactive.go:1706`；`internal/config/config.go:429`；`internal/query/query.go:4415` |
| AUDIT-P1-34 | DONE | Docs | ~~**docs/usage 引用 29 张截图，26 张不在仓库里**（`tui-01`~`tui-09`、`cli-01`~`cli-05`、`webui-02/05/06` 全系列缺失），而 README:458 把这套文档作为头号入口宣传"每个场景配截图和要点"。另 8 个顶层文档不在 `docs/README.md` 索引里~~（DONE，见 [修复证据](#修复证据--audit-p1-30--p1-342026-07-26)：**删引用改文字**，不补假图也不留「待补」占位；全仓 `![](...)` 本地引用坏链从 21 处降到 **0**；README / docs/README 的"每个场景配截图"宣传同批改掉；8 个顶层文档全部进索引） | `docs/usage/*.md` |
| AUDIT-P1-36 | DONE | Cost/Provider | **cache 计价倍率是 Anthropic 形状，且非 Anthropic provider 无法覆盖**。AUDIT-P0-15 把 cache token 拆成了分档，但倍率写死为 Anthropic 的 `read 0.1× / write5m 1.25× / write1h 2×`（`pricing.go:70` 的注释自陈假设「every provider that follows Anthropic's cache pricing」）。而各家折扣比例并不一致 —— go-claude 是通用 runtime，gpt-5.5 / glm-5.1 / deepseek-v4 都是常用后端。**逃生口事实上不存在**：`session.Rate` 有 `CacheReadPerMTok` / `CacheWrite5mPerMTok` / `CacheWrite1hPerMTok` 三个字段，但全仓只读不写 —— `config.ModelPrice` 只有 `Input` / `Output`，配置层根本表达不了 cache 单价。缓解项：未配 `modelPricing` 的模型返回 unknown 而非静默 0，所以错误比例只在配了之后才生效，且只影响 cache read 部分（会话越依赖缓存偏差越大）。**另有一条待核实**：OpenAI 路径只读 `prompt_tokens_details.cached_tokens`，而 DeepSeek 用 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` —— 若其不同时填前者，则 `CacheReadInputTokens` 恒为 0，cache 命中全按满额 input 计（方向是高估，安全但不准）。**token 计数本身无此问题**：`Usage.InputTokensIncludeCacheRead` 已正确建模两家语义差异，`Tiers()` 的减法对 Anthropic 与 OpenAI 兼容路径都成立，已逐条核验 | `internal/session/pricing.go:70-74`（Rate 的 cache 字段无 setter）、`internal/config/config.go:179-182`（ModelPrice 只有 Input/Output）、`internal/anthropic/client.go:413-418`（只读 `prompt_tokens_details.cached_tokens`）；对照正确的一侧 `internal/anthropic/client.go:415`、`internal/query/query.go:2838` | `config.ModelPrice` 增加可选的 `cacheRead` / `cacheWrite5m` / `cacheWrite1h` 并接到已有的 `session.Rate`；默认倍率按 provider kind 分组而非全局一套；抓真实 deepseek / glm 响应确认 cache 字段名，必要时补映射。补测试覆盖「配置的 cache 单价优先于推导倍率」和「非 Anthropic provider 不套用 Anthropic 倍率」 |

### 修复证据 · AUDIT-P1-28 / P1-29 / P1-31（2026-07-26）

三条同属「日常可读性」，改动集中在 `internal/cli` 与 `internal/observability`，一次提交完成。

**AUDIT-P1-28 · 默认安静，诊断显式开启**

- 新增 [internal/observability/level.go](../../internal/observability/level.go)：`ParseLevel` 认全
  `debug/info/warn/error/dpanic/panic/fatal/silent`（`off`、`none` 是 `silent` 的别名），
  **无法识别的名字返回 error 而不再静默落回 Info** —— 旧行为正是 `=fatal` 反而喷 19 行的根因。
  `EnvLogLevel()` 额外返回「是否显式设过」，因为 CLI 要区分「没设」和「设成 info」。
- `NewCLILogger()`：未设级别 → `zap.NewNop()`（一条不写）；显式设了 → 照设的级别输出，
  但 **debug 之外一律 `AddStacktrace(LevelSilent)`**，Go 调用栈对 CLI 用户是纯噪音。
- `cli.Run` 在 `runStartupUpdate` 之前按命令决定日志形态：`commandSpec.structuredLogs`
  为 true 的 `server` 与 5 个 `__*` 后台入口**完全不动**，其余走 CLI 安静模式。
  服务端 JSON 结构化日志因此逐字节不变（已用真实 `server --port` + `curl /health` 复核）。
- 实测（干净 env、无 key）：**stderr 从 21 行 JSON / 3 段 stacktrace 变成 1 行人话**；
  `=error` 从「5 行带 stacktrace」变成「5 行、0 stacktrace」；`=fatal` 被识别；
  `=fatalx` 直接报错并列出合法值。
- 错误质量双峰同批拉平：`--port abc` → `--port must be a number between 0 and 65535, got "abc"`
  （不再漏 `strconv.Atoi`）；`--system-prompt-file` / `--append-system-prompt-file` 的读文件错误
  经 `readFlagFile` 带上 flag 名；`invalid permission mode: X` 改为列出
  `permissions.AcceptedModes()` 的全部 14 个合法拼写（新增该导出函数，并用测试锁住
  「列出来的每个值 `NormalizeMode` 都认」）。

**AUDIT-P1-31 · 单一命令表 + 全量 `--help`**

- 新增 [internal/cli/commands.go](../../internal/cli/commands.go)：`commandTable()` 是
  dispatch、全局 help 的 usage 行、shell completion 词表的**唯一来源**。
  `cli.go` 里那个 60 行的 switch、`printHelp` 里手写的 36 行 usage、`completionCommandWords`
  里手写的 32 个词全部删除，改为从表派生 —— 三份清单不再可能漂移。
  实测 `completion zsh` 现在包含审计指出缺失的 `transcript`/`goal`/`goals`/`eval`。
- 每个可见命令的 `--help` / `-h` 在 dispatch 前被拦截并打印该命令的 usage + 一句话说明 + 别名；
  隐藏的 `__*` 入口不拦截（参数由调用方完全控制）。
- `--fork-session` 从死 flag 变成真分叉：`resumeRecorderSessionID` 在该 flag 下
  **先照常解析 resume 目标**（所以不存在的 session id 仍然报错），再返回「不复用原 recorder」，
  于是 resume 上下文照常注入、这一轮记进新会话、原 transcript 一个字节不动。

**AUDIT-P1-29 · 陈旧绝对路径**

- README 首条命令改为 `git clone … && cd golang-cc`；`--cwd` 示例改 `"$PWD"`；
  另一处示例的私人项目路径改为 `/path/to/other-project`。
- docs 下 1152 处按两类分别处理：带子路径的（`…/golang-claude-code/internal/x.go`，共 1071 处）
  **剥掉前缀变成仓库相对路径**，从此可直接点开；裸指仓库根的（81 处）换成
  `/path/to/golang-cc` 占位符。
- **刻意未动**：`docs/swagger.{json,yaml}` 与 `docs/docs.go` 里的那处 example ——
  它们由 `internal/server/swagger_types.go:313` 生成，只改产物会立刻造成 AUDIT-P0-18
  刚修好的 swagger 漂移；该源文件属另一并行任务的所有权范围。`web/`（33 处）与
  `internal/tui/app_test.go`（8 处）同理不在本次范围内。

**测试**（每条都做过「移除修复 → 转红 → 还原 → 转绿」的 A/B）

- [internal/observability/level_test.go](../../internal/observability/level_test.go)：级别解析全表、
  未知级别必须报错、CLI logger 默认静默 / 显式级别无 stacktrace / debug 保留 stacktrace，
  以及 `TestServerLoggerKeepsInfoAndStacktrace` 守住「服务端日志没被削弱」。
- [internal/cli/commands_test.go](../../internal/cli/commands_test.go)：命令表 ↔ help ↔ completion
  三向一致、隐藏命令不外泄、**每个可见命令的 `--help` 与 `-h` 都返回帮助**、
  flag 错误必须带 flag 名且不含 `strconv.Atoi`、permission mode 必须列合法值、
  `--fork-session` 真分叉且仍校验 session 存在。
- golden `first_run_no_api_key.txt`：干净环境无 key 的整段 stderr **只有一行人话**。
  移除「默认静默」这一项后该 golden 立刻转红，实测多出 7 行 JSON —— 即审计描述的现象。
- 同步更新 golden `help.txt`、`completion_zsh.txt`、`process_completion_zsh_success.json`
  （逐行 diff 确认新增内容正确后才更新，未为了变绿改断言）。

验收命令：`go test ./internal/cli ./internal/observability ./internal/permissions -count=1`

### 修复证据 · AUDIT-P1-30 / P1-34（2026-07-26）

两条都属于「拿到这个仓库的人能装上、文档不骗人」，改动全在文档、`scripts/` 和一个新 workflow，
**零 Go 代码改动**。

**AUDIT-P1-30 · 一条可复现的安装路径**

选型的硬约束是「不能出现第二套版本注入」。`scripts/build.sh` 那一行
`-X ...cli.Version=$(git describe --tags --always --dirty)` 保持一字不改，新增的两个脚本都
**shell out 到它**，而不是自己写 `go build`：

- 新增 [scripts/install.sh](../../scripts/install.sh)：从当前 checkout 编译并装到
  `$PREFIX/bin`（默认 `~/.local/bin`）。编译前查 Go 版本，两个下界**含义不同因此处理不同** ——
  `go.mod` 的 `go` 指令是硬下界（低于它编译过不去，直接 exit 1），`.tool-versions` pin 的
  1.26.5 是安全下界（GO-2026-5856），低于它**只告警**：能编过，产物带 CVE，把这个事实说出来
  比拒绝安装有用。两个版本号都从各自文件 `awk` 出来，脚本没有成为第三个写死版本号的地方。
  目标目录可写性在**编译前**检查 —— 编译要几十秒，跑完才说目录只读是纯浪费。
- 新增 [scripts/release.sh](../../scripts/release.sh)：darwin/linux（amd64+arm64）+ windows/amd64
  五个目标，每个一个归档，外加 `SHA256SUMS`。`VERSION` 只算一次并 export，否则 `build.sh`
  会按目标各跑一次 `git describe`，跑到一半打 tag 就会产出版本互不一致的归档。
  `CGO_ENABLED=0`（全仓 `import "C"` 零命中），所以产物是静态二进制。
- 新增 [.github/workflows/release.yml](../../.github/workflows/release.yml)：`v*` tag 或手动触发。
  **刻意独立成文件**，ci.yml 那五个 job 一字未动 —— 质量门每次 push 都跑，发布链路只在打 tag
  时跑，两者失败的含义不同。Go 版本从 `.tool-versions` 里 `awk`（不是第三个版本源），
  `fetch-depth: 0`（浅克隆拿不到 tag，`git describe` 会退化成裸 sha）。发布前有一步
  **自证**：解开 linux 产物跑 `--version`，输出不含该 tag 就 fail —— 把本条的验收标准焊进 CI，
  以后不会随时间退化。凭证只用 Actions 自带的 `GITHUB_TOKEN`，无额外账号/密钥。
- README 新增 [安装](../../README.md#安装) 章节：源码安装 / 预编译产物 / 只构建三条路径。

**明确不做，并在 README 写清原因**（这是本条最容易变成谎的地方）：

- **`go install` 装不上，且不是漏写文档。** 审计当时 `go.mod` 的 module path 是
  `github.com/konglong/golang-claude-code` 而仓库实际在 `github.com/konglong87/go-e2e`，
  proxy 按 module path 拉代码拉不到；`go.mod` 还有 `replace ... => ./third_party/termenv`，
  而 `go install pkg@version` 不应用 replace；即便路径对上，`go install` 也传不了 `ldflags`，
  `--version` 会是 `dev`。登记为 TODO-088。
  **2026-07-26 更新**：三条里的第一条已修，module path 全仓替换为
  `github.com/konglong87/go-e2e`。**但 `go install` 依然装不上** —— replace 指令和
  `ldflags` 这两条没动，本条结论不变，TODO-088 记为 PARTIAL。
- **`curl | sh` / `brew install` 不做**：两者都需要公开可下载的产物地址，而本仓库当前非公开，
  Release 需要仓库权限。现在写出来就是一条跑不通的命令。登记为 TODO-087。
- **Dockerfile 不做**：本机无 docker，无法按本条的验收标准实跑验证。宁可不给，也不给一个
  没验证过的安装路径。登记为 TODO-089。

**实跑验证**（本机 Go 1.26.4，工作区脏所以版本带 `-dirty`，这正是 `build.sh` 的语义）：

- `scripts/release.sh` → 5 个归档 + `SHA256SUMS`，5/5 校验和复算一致；
  解开 darwin/amd64 跑 `--version` → `v0.1.48-go-dirty (Go Claude)`；
  linux/arm64 产物 `file` 报 `ELF 64-bit LSB executable, ARM aarch64, statically linked`；
  windows 归档内是 `.exe`。
- `PREFIX=<tmp> scripts/install.sh` → 先打印 Go 1.26.4 低于 pin 的告警，再编译、安装，
  末行 `v0.1.48-go-dirty (Go Claude)`，并因目标目录不在 PATH 里打出该加的 `export`。
- 失败路径：无 `go` → exit 1 且提示装哪个版本；`BIN_DIR` 不可写 → **编译前** exit 1；
  未知参数 → exit 2 + usage。版本比较器 7 组用例全对，含 `1.26.5 <= 1.26.10`
  （字符串比较会答错的那组）。
- 干净工作区上的基线（改动前实测）：`scripts/build.sh` → `v0.1.48-go`，
  `bin/golang-cc --version` → `v0.1.48-go (Go Claude)`，即真实 tag 而非 `dev`。
- `scripts/offline-acceptance.sh --static-only` → 188 checks passed。两个新脚本自动被这个
  CI job 覆盖（`bash -n`、`--help` 必须 exit 0 且有输出、sibling 引用可解析、无写死 home 路径）。

**未验证边界**：`gh release create` 那一步无法在本机验证（本机无 `gh`，且不该为了验证去推 tag）。
产物生成与 `--version` 自校验已本地验证，上传是否成功要等第一次真实推 tag。

**AUDIT-P1-34 · 删引用改文字，不补假图**

审计说 26 张缺失；改动前实测坏引用 **21 处**（`docs/usage/tui.md` 13、`cli.md` 5、`webui.md` 3，
其中 `tui-skill-create-commit-push.png` 被引用两次）。差额是此前已有人删掉一部分引用。

**选了「删引用改文字」而不是补图或标注**，理由三条：

1. **这个仓库的 UI 每天在改。** TUI 显示架构、渲染预算、viewport 遮挡、交互卡片层级近期都在改
   （`docs/tui/` 下一批修复方案）。今天拍的图下周就和界面不一致，而**过期截图比没有截图更能骗人**
   —— 它看上去是证据。
2. **多数场景拍不出诚实的截图**：网络搜索、多 subagent、Goal/Loop、自动压缩都要真实 key 和真实
   模型轮次。为配图造一张假界面图，是这套文档最不该做的事。
3. **「截图待补」标记已经试过了，没用。** 上一版 `docs/usage/README.md` 的清单里逐行标了
   `📷 待补`，21 个坏引用照样留在正文。标记没有阻止文档骗人。

处置结果：每个被删的 `![](...)` 都把原来的图注**提升为正文**（原图注本身就在描述界面/行为），
而不是留一段空白。保留的图只有真实存在的 3 张（`tui-11-code-review-a/b`、
`review-compare-claude-code`，展示的是"多步工具编排 + 错误恢复"这类不随皮肤变化的行为）
和 `docs/web_agent/images/` 下 6 张真机 E2E 验收证据图。

同批一起改掉的「宣传口径」：README 的"每个场景配截图和要点"、文档索引表里的"场景 + 截图"、
`docs/README.md` 分类目录的"配命令、截图和要点" —— 图删了而宣传留着，等于换个地方继续骗。

8 个未进索引的顶层文档全部登记进 `docs/README.md`：`session_quickstart.md` 进「稳定入口」
（根 README 直接引用它），其余 7 个（`loop_guard.md`、`provider_neutrality_plan.md`、
两个 askuserquestion、三个 tui fix）进新增的「其他顶层文档」表，并写明它们本该收进子目录、
为什么这次不搬（外部引用与 git 历史指向，移动收益不抵改链接风险）。

**顺带修掉的两处同类问题**（都在本次已在改的文件里，且都属于「文档骗人」）：

- `docs/usage/cli.md` 的 `--cwd` 示例曾指向作者本机的另一个项目目录 —— AUDIT-P1-29
  收口时漏了这一处，现已改为 `/path/to/other-project`。
- `docs/usage/tui.md` 的 `README.md#常用-tui-操作` 锚点从来没解析过（README 里那是一个普通段落
  而非标题），改为不带 fragment 的链接。
- README 说"CI 跑四个 job"，实际 ci.yml 有 6 个（漏了 swagger 和 scripts），本地复现清单也只列了
  4 条 —— 补齐并说明新增的 release workflow 不参与每次 push。

**自查**：全仓 `.md` 的本地 `![](...)` 引用共 36 处，坏链 **0**（改动前 21）；
本次触达的 7 个文件里的相对链接与标题锚点全部解析通过。

---

## 8. P2 · 架构与可维护性

| ID | 状态 | 模块 | 问题 | 证据 |
| --- | --- | --- | --- | --- |
| AUDIT-P2-01 | DONE | Query | ~~**`internal/query/query.go` 7834 行 god object**~~（**7910 → 5976 行**，顶层声明 370 → 251，见 [修复证据](#修复证据--audit-p2-01--p2-022026-07-26)）。按本条既定顺序做完 ①②③④ 四步**纯移动**：①→ 新包 `internal/capabilityloop`（783 行）；②→ `internal/query/systemsections.go`（548 行）；③→ `internal/query/resume.go`（321 行）；④→ 并入 `internal/query/closure_gate.go`（282 行）。**②③④ 留在 package query 而非新包**，理由见证据小节（换包要么复制 `getenv`/`isEnvTruthy`/`firstNonEmpty` 三份助手，要么改约 30 处跨文件调用，都比同包分文件差）。**主循环 `run` 未动**，按 §9 既定取舍 | 佐证：`query_test.go` 9189 行、`app_test.go` 7144 行，测试比源文件还大 |
| AUDIT-P2-02 | DONE | Refactor | ~~**三份重复的解析器实现**~~（**前提已更正 + 已收口**：三处从来不是重复——零个函数体字节相同——而是同一协议的三份**行为分叉**实现，同一份子代理输出会被解读成不同结果。TODO-091 先把结构性 token 收成一份 `protocol.go`；本批 TODO-092/093/094 逐条**做决定**并统一：候选串提取合成一个 `capabilityloop.JSONCandidates`（大小写不敏感 + 取全部标签对 + 子串守卫 + 双兜底，并修掉 `ToLower` 导致的偏移漂移 bug）、placeholder 集合合成 `IsPlaceholder`（只收 completed 默认文案，failure 三条是真义务故意不收）、「字段别名」经查是 gate 行标签而非子代理拼写故改为 `FollowUpField*` 单一来源、**字段覆盖面是真 bug**：外化后 summary 丢 supersede 关系使已解决的 follow-up 重新变 pending，已补两侧 + 端到端。见 [修复证据](#修复证据--audit-p2-02-收口2026-07-26todo-092--093--094)） | `internal/capabilityloop/`、`internal/toolresult/toolresult.go`、`internal/compact/facts.go` |
| AUDIT-P2-03 | TODO | Refactor | **无共享 util 包**：`firstNonEmpty` 完全相同的实现**复制了 20 份**；`nearestClaudeDir` 4 份、`runGit` 4 份；最危险的是 **`parseFrontmatter` 4 份且有 3 种不同签名** —— `memory`/`agents`/`skills`/`outputstyle` 的 frontmatter 解析行为已经分叉，一处修复不会传播 | 全仓 grep |
| AUDIT-P2-04 | PARTIAL | TUI | ~~`internal/tui/app.go` 9051 行~~（**TUI 侧已闭合**：审计后又涨到 9099 行，现按职责纯移动拆成 30 个文件，`app.go` 降到 **364 行**，最大新文件 574 行；测试与 golden 一字未改，见 [修复证据](#修复证据--audit-p2-04-tui-侧2026-07-26)）；**仍 TODO**：前端 `WebAgentPage.tsx` 3417 行 + `InspectorPanels.tsx` 3197 行 + 单一无作用域 `app.css` 8430 行 = 前端全部代码的 40%（转 [TODO-097](../todo.md)）；`app_test.go` 7173 行未拆（转 [TODO-095](../todo.md)） | — |
| AUDIT-P2-05 | PARTIAL | Frontend | ~~无 linter、无独立 typecheck script、无 coverage、无 router；3 个死依赖；`e2e/visual-regression.spec.ts` 名不副实~~（Biome 2.5 lint、独立 `typecheck` 覆盖 `e2e/` 与两个 config、3 个死依赖已删、`visual-regression.spec.ts` 改名 `navigation-smoke.spec.ts`，全部进 CI，见 [第一批修复证据](#修复证据--audit-p1-32--p2-10--p2-052026-07-26)；**存量 warning 清理已做完可机械化的部分**，见 [本条收尾](#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)：216 → 125 warnings，`useButtonType` 47 / `noArrayIndexKey` 9 / `noAssignInExpressions` 2 / a11y 四族 25 / 四条 complexity 全部清零并提为 `error`，`noImportantStyles` 改 `off`（4 处是 `prefers-reduced-motion` 的必需覆盖，删掉就是 a11y 回归）。**仍 TODO**：coverage、router / 深链接与前进后退、真 visual baseline（需固定容器），以及刻意保留为 `warn` 的 `useExhaustiveDependencies` 104 处（TODO-082，手改会改动全应用 effect 时序）与 `noDescendingSpecificity` 21 处（TODO-083，依赖 visual baseline 先落地）） | `web/` |
| AUDIT-P2-06 | TODO | Test | 无 fuzz 测试（全仓 `func Fuzz` 零命中）；仅 2 个 benchmark；`-race` 只在 docs 里被提及，无任何脚本或自动化执行 | 全仓 grep |
| AUDIT-P2-07 | DONE | Deps | `third_party/termenv` 的 fork **不是维护负担但是隐形的**：相对上游 v0.16.0 只改了 `termenv_unix.go` 的 `backgroundColor()` 一个函数（删掉 OSC 11 查询，修 TUI 输入泄漏，改动合理），其余字节相同。风险不是 rebase 成本而是 go.mod 里写着 `v0.16.0` 看起来是最新的，**没人会注意到它已冻结、永远收不到上游修复**。补一个 `third_party/termenv/PATCH.md` 即可闭合 | `third_party/termenv/`，引入于 commit `0aa51f1a` |
| AUDIT-P2-08 | TODO | Architecture | **全部代码在 `internal/` 下，无 `pkg/`** —— 第三方无法作为库 import。而 README 声称产品方向包含"Agent 开发框架"。若该定位成立，需要规划一层公开 API surface；若不成立，应在 README 中修正措辞 | `cmd/` 仅 1 个 32 行 main；无 `pkg/` |
| AUDIT-P2-09 | TODO | Docs | 文档一致性问题：`tui_display_parity_gap_analysis.md` 自相矛盾（第 21 行说前端默认已改 `code`，第 35 行说当前默认 `chat`，旧结论没删）；同文档 builtin slash 清单列 26 条而实际 31 条（漏 `/init`/`/resume`/`/branches`/`/redo`）；`transcript_checkpoint_optimization_plan.md` 有 45 条 `[...](../../internal/xxx.go:123)` 形式的伪链接（任何 Markdown 渲染器里都是死链）；`docs/api_server.md` 停在 07-16 未收录 3 个新端点；`docs/web_agent/web_agent_codex_grade_revision_plan.md:477` 写了不存在的 `web` 命令 | 见各文档 |
| AUDIT-P2-10 | DONE | Docs/Process | ~~**backlog 与代码脱节**：最近一轮工作（AskUserQuestion 真交互，5 个 commit）在 `docs/todo.md` 中 0 次提及。且 TODO-049 标 DONE 但其 P0-4 子项（结构化 `file_change` task event + Files tab 分组）**根本没实现** —— `agenttasks.go` 的 17 个 event 常量中没有 `EventFileChange`，`WebAgentPage.tsx:1916` 仍在 `payload.file ?? payload.path ?? payload.filename` 猜字段，正是 gap 文档自己批判的原状~~（DONE，见 [修复证据](#修复证据--audit-p1-32--p2-10--p2-052026-07-26)：新增 `agenttasks.EventFileChange`，复用既有 `tools.FileChange` 协议派生真字段；Files tab 改 Edited / Read 分组；`docs/todo.md` 的 TODO-049 审计更正已改为已闭合，并新增 TODO-062/063/064） | `internal/agenttasks/agenttasks.go:21-36`；`web/src/components/WebAgentPage.tsx:1916,1929` |
| AUDIT-P2-11 | TODO | Agent | 无 reflection / self-critique 循环（全仓 6 个 LLM 调用点无一用于自我审查）；无工具结果缓存（同一 `Read` 同一文件调 10 次执行 10 次，`internal/promptcache` 只是观测器不缓存任何东西）；`goal` 的 `DeterministicPlanner` 生成的是固定三步模板而非任务感知规划；self-verification 与 prompt 分类器**全靠中英文硬编码短语表**（含 `"改"` 这种单字匹配），换个说法即绕过 | `internal/goal/planner.go:26-60`；`internal/query/closure_gate.go:1297-1341`；`internal/query/query.go:4969-5069`；`internal/promptcache/promptcache.go:61` |
| AUDIT-P2-12 | TODO | Cleanup | 死代码与死配置：`recap.IncludeSessionMemory` 声明+默认+接线齐全但**从不被读取**；`internal/memory` 三个导出函数无非测试调用者；`MessagesRequest.Stream` 字段从未被读取；`openAIToolArguments` 无调用者；`agents.Load` 无缓存，每次 spawn 全量重读目录**两遍** | `internal/recap/`；`internal/memory/memory.go:45,138,142`；`internal/anthropic/types.go:49`、`client.go:1224`；`internal/agentruntime/runtime.go:185,222` |

---

## 9. 明确不建议现在做

| 项 | 理由 |
| --- | --- |
| 多实例水平扩展改造（AUDIT-P1-25 的完整解法） | P0 单实例稳定性问题（shutdown / 超时 / 连接池 / panic）未解决前，投入分布式改造收益为负。先把单实例做扎实 |
| 引入 embedding / 向量检索 | 先修 AUDIT-P1-11 的 ngram parser 缺失和 UTF-8 分块碎字两个 bug，中文检索质量的提升立竿见影且成本极低。embedding 是更大的架构决策，应在此之后独立立项 |
| 主循环 `run` 的拆分 | 见 AUDIT-P2-01，内部状态耦合真实。先做 ①②③④，主循环最后动 |
| 并行工具调用（AUDIT-P1-02） | 必须先给 `Session` 的共享可变字段加保护，否则直接加 goroutine 会立刻 data race。属于"想做但有硬前提"，不是可以顺手做的 |

---

## 10. 建议执行顺序

### 第 1 周 —— 堵住会造成损害的

1. **AUDIT-P0-01 + AUDIT-P0-02**（同一文件，一起改）：权限模式拆分 + Deny 移到 Bypass 之前 + `isPathQualifierTool` 扩展
2. ~~**AUDIT-P0-16 + AUDIT-P0-17**：建 CI，同时升级依赖修 5 个已调用 CVE~~ —— **已完成**（连同 AUDIT-P0-20、
   AUDIT-P2-07），证据见 [§3 完成记录](#完成记录audit-p0-16--p0-17--p0-202026-07-25)。剩余：`anthropic-sdk-go`
   与 `mcp-go` 的大版本跨越仍待独立评估
3. **AUDIT-P0-07 + AUDIT-P0-13 + AUDIT-P0-09**：LLM client 超时/连接池；server 四个超时 + `MaxBytesReader`；shutdown 三件套 + SIGTERM
4. **AUDIT-P0-10**：detached runner 加 `recover()` + stale task reaper

### 第 2–3 周 —— agent 实际效果

5. **AUDIT-P1-01**（约 20 行，纯收益）→ **AUDIT-P0-08**（auto-compact 默认开启 + overflow 兜底 + server/子代理接入）
6. ~~**AUDIT-P0-14**：递归深度上限 + batch 并发钳制 + 累计 token 预算~~ —— 已完成，见 [修复证据](#修复证据--audit-p0-142026-07-25)
7. **AUDIT-P0-03**：危险命令分类器改 AST
8. **AUDIT-P1-13 + AUDIT-P1-17**：补 `BashOutput`/`KillShell`；MCP stdio 加超时 + 接 stderr

### 第 4 周起 —— 产品化

9. ~~**AUDIT-P1-30 + AUDIT-P1-29 + AUDIT-P1-28**：Dockerfile + goreleaser + install 说明；修 README 路径；首跑体验~~ —— 已完成，见 [P1-28/29/31 修复证据](#修复证据--audit-p1-28--p1-29--p1-312026-07-26) 与 [P1-30/34 修复证据](#修复证据--audit-p1-30--p1-342026-07-26)。落地形态与原计划不同：**没用 goreleaser**（`scripts/build.sh` + `release.sh` 已经够，引入它会多一个要 pin 的工具版本），**没做 Dockerfile**（本机无 docker，无法按验收标准实跑）
10. **AUDIT-P1-31 + AUDIT-P1-32**：子命令 `--help`；Web dist embed
11. **AUDIT-P2-01 + AUDIT-P2-02**：拆 `query.go` 的 ①②，顺带合并三份 capability_loop 解析器

---

## 11. 审计方法与边界

**方法**：6 个维度并行独立审计，全部要求 `file:line` 证据；实跑 `go build` / `go vet` / `go test ./...` / `go test -cover` / `go test -race` / `gofmt -l` / `govulncheck`；实跑二进制验证首次运行体验；实测危险命令分类器 17 例、deny 路径匹配 4 例、macOS 沙箱写逃逸 2 例；静态核查 172 个 md 文件的链接与引用；抽查 6 条 DONE 条目的代码与测试。

**明确的边界（未验证项，不得据此宣称结论）**：

- ~~**前端测试未执行** —— 本机 Node 18.20.8 低于 `vite@8` / `vitest@4` 要求的 20+，仅做静态核查。~~
  **已在 AUDIT-P0-20 修复时补跑**：Node 22.23.1 下 `npm run build` 通过、`vitest run` 11 文件 116 测试全绿。
- **Linux 沙箱未验证** —— 本机无 bubblewrap，两个 Linux sandbox 测试 SKIP。`--unshare-net` 的代码路径存在，但真机行为未经确认。
- **Windows / WSL / PowerShell 沙箱未验证** —— 平台不可得，与既有 `PARITY-002` 结论一致。
- **未做真实压测** —— 连接池、SSE 轮询 QPS、LIKE 全表扫描的影响是从代码推导的量级估算，非实测数字。
- **未连接真实 MCP server** —— MCP 协议缺口来自代码阅读与协议规范对照。
- 审计全程只读，未修改任何仓库文件，未执行破坏性命令。

---

## 完成记录：AUDIT-P0-06 / P0-15 / P0-18 / P0-19（2026-07-25，第二批 P0 零散项）

四条互相独立，一起做只是为了共用一次全量验证。

### AUDIT-P0-15 · cache token 分档计价

**根因** —— [`internal/session/store.go`](../../internal/session/store.go) 把
`InputTokens + CacheCreationInputTokens + CacheReadInputTokens` 合成一个数，再乘满额
input 单价。真实计价里 cache read 是 0.1×、5m write 1.25×、1h write 2×，所以重缓存会话
被高估近 10 倍。

**修法** —— 新增 [`internal/session/pricing.go`](../../internal/session/pricing.go)：

- `TokenUsage` 五个**互斥**档位（uncached input / cache write 5m / cache write 1h /
  cache read / output），`EstimateCost` 逐档计价。倍率是导出常量
  `CacheWrite5mMultiplier` / `CacheWrite1hMultiplier` / `CacheReadMultiplier`。
- `ReportedUsage.Tiers()` 负责把「`InputTokens` 含全部 input 档位」的记账口径拆成计费档位。
  **这个求和口径本身没有改**：`usageFromAnthropic` 的 `InputTokens` 是整个 prompt 大小，
  `compactor.ObservePromptTokens` 依赖它校准上下文估算器。两处 `usageFromAnthropic` 都补了
  注释说明「不要把它改成排除 cache 档位」，否则会静默弄坏压缩阈值。
- 未知模型不再静默返回 0：`EstimateCost` 返回 `ok=false`，`UsageSummary.UnknownPricingModels`
  列出这些模型，`ModelUsage.CostKnown` / `agentruntime.Result.CostKnown` 区分「$0」和「不知道」，
  子代理侧首次遇到未定价模型时 `observability.Warn`（本次给 observability 补了缺失的 `Warn`）。
- 子串匹配换成「必须是真的 Anthropic model id」：先剥 Bedrock/Vertex 的 vendor 前缀
  （`us.anthropic.` / `anthropic.` / `anthropic/`），再要求 `claude-` 前缀，最后按 family 前缀匹配。
  `my-sonnet-4-proxy` 现在落到「未知定价」而不是套用官方价。配置的 `modelPricing` 始终优先。
- `cost` / `usage` 命令改走 `UsageWithRates(config.ModelPricing(cwd))`，非 Anthropic provider
  配了单价就不再进 `UnknownPricingModels`。
- 顺带发现内置表里 `haiku-3-5` / `haiku-3` 两行对真实 legacy id（`claude-3-5-haiku-20241022`）
  从来匹配不上（版本号在 family 之前），一并支持两种写法。

**金标测试** —— `TestEstimateCostPricesCacheHeavyTurnAtATenthOfFlatRate` 断言同一份
usage（1k uncached + 200k cache read）在分档前后成本比落在 9–10×；
`TestSubagentCostPricesCacheReadAtTieredRate` 在 `agentruntime` 端断言 `0.063` 而不是旧口径的
`0.603`。把倍率改回 1.0 + 恢复 `strings.Contains` 后，这两条与
`TestCacheMultipliersMatchPublishedPricing`、`TestEstimateCostRejectsThirdPartyGatewayNamesThatMerelyContainAFamily`、
`TestStoreUsagePricesCacheReadAtTieredRate`、`TestSubagentCostFlagsUnknownModelPricing` 全部失败，
恢复后全绿。

**与 `internal/agentbudget` 的结算** —— 该包原注释说「喂进来的数字是高估值」，现已重写：

- **token 侧没变**，仍是整个 prompt 大小。这是刻意的：token 上限衡量扇出做了多少工，
  cache read 也照样占上下文窗口。所以**默认唯一武装的熔断器（token）跳闸时机完全不变**。
- **cost 侧变小了**（重缓存场景约 1/10），所以成本上限会**更晚**跳闸。这是正确方向：
  旧数字本身是错的，货币上限只有对着真实花费才有意义。
- `DefaultMaxCostUSD` 仍是 0（关闭），所以**没有任何默认行为变化**，不需要调默认值。
  新增 `TestDefaultLimitsArmOnlyTheTokenCeiling` 把这个推理钉住：一旦有人给成本上限设非零默认值，
  这条测试会提醒他这个默认值现在比 AUDIT-P0-15 之前晚约 10 倍才触发。

### AUDIT-P0-18 · swagger 漂移

**核对结论有两处修正** ——

1. `/ws` **本来就有注解**（`@Router /mobile/chat/ws [get]`，真实路径带 `/mobile/chat` 前缀），
   `docs/swagger.json` 里也在。原审计把它列为缺失，是误报。真正缺注解的是 3 个：
   `/v1/providers`、`/runtime/settings`、`/tenant/agent-tasks/{id}/events/stream`。
2. `add5796c` 改的是 `/v1/models` **返回哪些模型**，不是响应结构 ——
   `openAIModelsResponse` 与 `SwaggerOpenAIModelsResponse` 字段逐个对得上，生成类型的 schema
   没有过期。过期的是描述（读起来像固定的 `claude-*` 列表），已改写。

**修法** —— 补 4 条注解（`/v1/providers`、`/runtime/settings` 的 GET 与 PUT、SSE stream）
+ `SwaggerProviderListResponse` / `SwaggerProviderOption`；重跑 `swag init`（`v1.16.4`，
与 CI pin 同版本，实测同输入两次生成 byte-for-byte 一致）；重新生成 `api-types.ts`（+287 行，
`docs/swagger.json` 路径数 80 → 83）。前端 [`web/src/lib/api.ts`](../../web/src/lib/api.ts)
的手写内联兜底类型换成生成 schema 派生的 `ProviderListResponse` / `OpenAIModelsResponse`。

**防复发用了两层，因为两层挡的不是同一件事** ——

- **CI `swagger` job**：重跑生成器再 `git diff --exit-code`，挡「注解改了但没重新生成」。
- **`TestEveryRegisteredRouteIsInSwagger`**：把 gin 路由表（`newRouter` 从
  `newHandlerWithStreams` 里抽出来，就是为了让路由表可枚举）逐条对 `docs/swagger.json`，
  挡**本条的真正根因**「新端点从来没写注解」—— 这种情况重跑生成器永远是干净的，CI job 抓不到。
  把 `docs/swagger.json` 回退到 HEAD 后该测试精确报出 3 条缺失路径并给出修复命令；
  刻意不文档化的路径进 `swaggerUndocumentedRoutes` 白名单（目前只有 Swagger UI 自身）。

### AUDIT-P0-19 · acceptance 脚本（**结论更正**）

**原结论是错的，先更正**：审计写「`claude_code_src_2026` 和 `agent-proving-ground` 两个目录
本机不存在，最精心的验收基建已经死了」。这两个目录**都存在**，是有意维护的配套项目
（前者是 Claude 源码参考，后者是 agent 拉练测试平台），都在 `$HOME/GolandProjects/` 下。
10 个 side-by-side 类脚本因此**不是死脚本**。

**真实问题及修法** ——

- **未文档化的前置条件**（这才是核心）：`agent-capability-full-release-acceptance.sh` 依赖 11 个
  `agent-proving-ground/reports/**/*.json`，那些是**跑完 APG 之后的产物**。原来的前置检查一次只报
  一条、且不说产物从哪来。现在一次收集全部缺口，说明「这些由 APG 产出，不是本仓库的东西」，
  给出 `cd <apg>` 提示和 `--verify-only` 出路，并明确「什么都没跑，没有写任何产物」，退出码 2。
- **硬编码绝对路径**：新增 [`scripts/lib/external-repos.sh`](../../scripts/lib/external-repos.sh)，
  `GO_CLAUDE_UPSTREAM_DIR` / `GO_CLAUDE_APG_DIR` / `GO_CLAUDE_COMPANION_ROOT` 可覆盖，
  默认仍指向主 checkout 的同级目录（用 `git rev-parse --git-common-dir` 解析，所以在 worktree 里也对）。
  12 个脚本改完。顺带清掉 5 处审计没提到的死路径：两处指向仓库**旧名** `golang-claude-code`
  （早已改名 `golang-cc`，所以那个路径根本不存在）、两处指向无关项目 `anything-ai`。
- **`GO_BIN` 默认值把能跑的脚本弄成了不能跑**：7 个 TUI 脚本硬编码
  `GO_BIN=/usr/local/go/bin/go`，而本机系统 Go 是 1.22.11，比 `go.mod` 要求的更旧 ——
  这些脚本一跑就是 `go.mod requires go >= 1.25.0`，看着像脚本坏了，其实只是默认值坏了。
  全部改成 `${GO_BIN:-go}`（从 `PATH` 解析）。改完 3 个脚本立刻能完整脱机跑过。
- **分类 + 统一入口**：[`scripts/README.md`](../../scripts/README.md) 按「可脱机 / 需 API key /
  需配套仓库」三类逐个列出，并写明每个不进 CI 的脚本**具体为什么**不进。
  [`scripts/offline-acceptance.sh`](../../scripts/offline-acceptance.sh) 是唯一脱机入口，
  已接进 CI（新 `scripts` job）：`bash -n` 全量解析、每个 `--help` 退出 0、
  跨脚本引用的兄弟文件存在、release gate 无前置条件时干净拒绝并带指引、无残留 home 目录路径，
  然后跑 3 个已验证可脱机的确定性 TUI 场景。本机实测 185 checks 通过。

**顺带暴露一个真实缺陷**（未修，已登记）：`GO_BIN` 修好后
`tui-render-budget-acceptance.sh` 能跑了，但它断言自己的 `last=Bash` 标记失败。
这是**之前一直红着但因为脚本跑不起来所以没人知道**的问题，不是本次改动引入的。
已在 `scripts/offline-acceptance.sh` 和 `scripts/README.md` 两处写明排除原因。

### AUDIT-P0-06 · 本地凭据文件权限（PARTIAL）

`config/config.local.yaml` 已 `chmod 600`（`0644` → `0600`，仅权限，未读取也未改动内容）。

新增 [`internal/config/credential_permissions.go`](../../internal/config/credential_permissions.go)：
扫描 settings 搜索路径上的每个文件，**同时**满足「权限位放开了 group/other」和「确实含明文凭据」
才告警。两个条件都必要 —— 只看权限会让 committed 的 `config/config.yaml`（`0644`、无凭据）天天报噪音；
`${VAR}` 形式的间接引用也不算明文。告警走 `Config.Warnings`，`status` / `doctor` 的
`configWarnings` 字段呈现（无告警时该字段不出现，所以健康安装的输出一字未变）。

**刻意只告警不拒绝**：拒绝加载会打断所有依赖这类文件的现有工作流，比一个已被明确告知的
可读文件更糟。`TestLoadForCWDReportsCredentialPermissionWarnings` 同时断言告警出现**且**
`cfg.APIKey` 仍然加载成功。告警文案经测试断言不含凭据本身。

**为什么是 PARTIAL** —— 这条的第一项要求是「轮换该 key」，那是人工动作，不在本次范围。
`chmod` 和 loader 告警已闭合；**key 本身仍需用户手工轮换**，轮换完可改 DONE。

### 验证

```
go test ./... -count=1        # 全绿
go vet ./...                 # 干净
gofmt -l .                   # 空
git diff --check             # 干净
scripts/offline-acceptance.sh # 185 checks passed
npm --prefix web run build    # 通过（tsc + vite）
npm --prefix web run test     # 116 passed
swag init ... && git diff     # 生成物与注解一致（CI 新 swagger job 自证）
```

---

## 完成记录：AUDIT-P1-36（2026-07-25，cache 计价倍率 provider 中立化）

由 AUDIT-P0-15 的合并 review 引出。**本条只关计价比例，token 计数一行未动** ——
`Usage.InputTokensIncludeCacheRead`（`internal/anthropic/client.go:415`）与
`usageFromAnthropic`（`internal/query/query.go`）的求和口径刻意保持原样，压缩阈值靠它校准。

### ① 配置层现在能表达 cache 单价

`config.ModelPrice` 增加可选的 `cacheRead` / `cacheWrite5m` / `cacheWrite1h`
（`internal/config/config.go`），跟随现有 camelCase 的 json/yaml 风格。
零值仍表示「按倍率推导」，所以只配 `input`/`output` 的既有 settings 行为不变。

新增 `session.ConfiguredRates(cwd)`（`internal/session/pricing.go`）承接转换，
替掉 `internal/cli/cli.go` 和 `internal/agentruntime/runtime.go` 里**两份逐字重复**、
且都只搬运 `Input`/`Output` 的 converter —— 那份重复正是「`session.Rate` 的三个 cache
字段全仓只读不写」的直接原因，留着它就是留着下次漂移。

### ② 默认倍率按 provider kind 分组，查不到就落「未知」

`session.CacheMultipliers` + `CacheMultipliersForProviderKind(kind)`：

| provider kind | 默认倍率 | 依据 |
| --- | --- | --- |
| `anthropic` / `anthropic-compatible` / 空 | `read 0.1× / write5m 1.25× / write1h 2×` | Anthropic 官方定价页逐字给出这三个倍率，2026-07-25 核验：[prompt-caching § Pricing](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)、[pricing § Prompt caching](https://platform.claude.com/docs/en/about-claude/pricing)。逐模型价目表也自洽（Haiku 4.5：$1 / $1.25 / $2 / $0.10） |
| `custom` / `openai` / `openai-compatible` / `openai-chat-completions` | **未知** | 「OpenAI 兼容」是传输协议不是厂商。DeepSeek 公开 V4 价目的 cache-hit 约 **0.02×**（Flash：$0.0028 vs $0.14）、Pro 更低于 0.01×，且**根本没有 cache write 档**；同一 kind 下还同时挂着 GLM / Kimi / OpenAI 自己。没有任何单一数字能代表这一组，硬编一个就是编造 |
| 其他（`bedrock` / `vertex` / 未知串） | **未知** | 未核实，不猜 |

「未知」沿用 AUDIT-P0-15 建立的机制：`EstimateCost` 返回 `ok=false`，
模型进 `UsageSummary.UnknownPricingModels`。**只在该轮真的用到了那个 cache 档时才不可计价** ——
零 cache token 的轮次照常出价，所以不会把整份成本报表拖成 unknown。
每个档位独立解析：只配了 `cacheRead` 时，5m/1h 档仍是未知而不是回退到借来的倍率。

真 Anthropic model id（`builtinAnthropicRate`）恒带 Anthropic 倍率 —— 那条路径的
id 形状检查已经确立了厂商，无需 provider 配置配合。

**provider kind 判定复用既有分派**：`providerKindOpenAI` / `providerKindSupported`
的字符串表从 `internal/anthropic/client.go` 下沉到 `internal/config`
（`ProviderKindAnthropic` / `ProviderKindOpenAI` / `NormalizeProviderKind`），
client 侧改为委托。新增 `config.ProviderKindForModel(cwd, model)` 把 model id
映射回服务它的 provider kind（主 provider + fallback 列表），找不到时返回
`found=false` 而不是猜一个 —— 猜就是把第三方后端当 Anthropic 算钱。

### ③ DeepSeek cache 字段：核实结论为**假警报，不需要补映射**

审计怀疑 DeepSeek 只报 `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`，
而 OpenAI 路径只读 `prompt_tokens_details.cached_tokens`，导致 `CacheReadInputTokens` 恒为 0。

**抓真实响应验证（2026-07-25，本机 `config/config.local.yaml` 的 provider）**：
同一 ~3.1k token 前缀连发两次，看 `usage`。

| provider / model | `prompt_tokens_details.cached_tokens`（第 1 次 → 第 2 次） | `prompt_cache_hit_tokens` |
| --- | --- | --- |
| `deepseek-v4-pro`（ai-gateway 网关） | 0 → **3072** | 未出现 |
| `glm-5.1`（ai-gateway 网关，主 provider） | 0 → **2944** | 未出现 |
| `kimi-k2.6`（ai-gateway 网关） | 0 → **3072** | 未出现 |
| `gpt-5.5`（ai-gateway 网关） | 字段存在，两次均 0（该窗口未命中） | 未出现 |
| `deepseek-v4-flash`（sensenova 网关） | 0 → 0 → **3072**（第 3 次起稳定命中） | 未出现 |

**五个 provider 全部走 `prompt_tokens_details.cached_tokens`，没有一个用
`prompt_cache_hit_tokens`**，现有读取路径正确、cache read 确实被记到账上。

sensenova 一项初次探测时误判为「403 无权限」。**实际根因是配置里 `apiKey: ${SENSENOVA_API_KEY}`
的环境变量从未设置**：Go 侧 `os.ExpandEnv` 把它展开成空串，`StreamMessages` 直接以
「缺少凭据」跳过该 provider、根本不发请求；而首轮探测脚本用 awk/python 直读配置原文、
绕过了展开，把字面量 `${SENSENOVA_API_KEY}` 当 Bearer token 发出去才拿到 401（gRPC code 16
是 UNAUTHENTICATED，不是权限不足）。设置该变量后一次通过。**教训**：探测第三方 provider
时必须走 Go loader 解析后的凭据，直读配置原文会把「变量未展开」误诊成「服务端拒绝」。

另外 sensenova 比 ai-gateway 慢一轮才建好缓存（后者第 2 次即命中，它要到第 3 次），
所以「连发两次没命中」不足以断定某 provider 不支持缓存。

**边界必须说清**：这些都是脱敏后的示例网关（`ai-gateway.example.com` / `model-gateway.example.com`），
不是 `api.deepseek.com` 直连。DeepSeek 官方端点的字段名未验证（本机无直连 key），
第三方资料提到官方 `usage` 用 `prompt_cache_hit_tokens`、而 LiteLLM 一类路由器会归一化成
OpenAI 形状 —— 若日后接官方直连，需重新核实。**顺带确认成本**：
`sashabaranov/go-openai` 的 `Usage` 结构（v1.41.2 `common.go`）只有
`PromptTokensDetails.CachedTokens`，没有 DeepSeek 私有字段，补映射需要额外解析原始 JSON。
既然实测无一 provider 需要它，本轮不做；真需要时再单开条目。

### 关于既有测试锁住错误行为

`TestConfiguredRateDerivesCacheTiersFromInputRate`（`internal/session/pricing_test.go`）
原本用一个**没有任何 provider 信息**的 `glm-5.1` rate 断言「cache 档从 input 按 Anthropic
倍率推导」—— 这恰好把本条要修的错误行为钉成了契约。已改为显式带上
`CacheMultipliers: AnthropicCacheMultipliers()`，让它继续守「推导机制本身」，
而新的 `TestEstimateCostReportsUnknownWhenCacheDiscountIsUnknown` 守新契约。
三个倍率常量随之改名为 `AnthropicCacheWrite5mMultiplier` 等，名字不再暗示「全局默认」。

### 新增测试（每条都做过「移除修复→红 / 加上修复→绿」A/B）

| 测试 | 移除哪一处修复后转红 |
| --- | --- |
| `TestCacheMultipliersGroupedByProviderKind` | `CacheMultipliersForProviderKind` 改成对所有 kind 返回 Anthropic 倍率 |
| `TestEstimateCostReportsUnknownWhenCacheDiscountIsUnknown` | `Rate.derived` 在倍率为 0 时静默回退到硬编常量 |
| `TestConfiguredRatesDerivesCacheTiersOnlyForAnthropicProviders` | 上述任一处；另外 `ProviderKindForModel` 恒返回 not-found 时也红 |
| `TestConfiguredRatesPreferExplicitCachePriceOverDerivedMultiplier` | `ConfiguredRates` 不搬运 `price.CacheRead/CacheWrite5m/CacheWrite1h` |
| `TestConfiguredRatesWithoutCacheFieldsStayBackwardCompatible` | `ProviderKindForModel` 恒返回 not-found（向后兼容会被误伤） |
| `TestBuiltinAnthropicRateCarriesAnthropicCacheMultipliers` | `builtinAnthropicRate` 不挂 `CacheMultipliers`（连带 3 条既有 P0-15 测试转红） |
| `TestExplicitCacheRatesPriceAnUnknownProvider` | 分档独立解析被去掉（只配 cacheRead 时 5m 档回退借来的倍率） |
| `TestModelPriceCarriesCacheRatesFrom{JSON,YAML}Settings`、`TestModelPriceWithoutCacheRatesStaysZero` | `ModelPrice` 的三个 cache 字段（移除后无法编译） |
| `TestProviderKindForModelResolvesPrimaryAndFallbacks`、`TestProviderKindClassifiersCoverEverySupportedKind` | `config` 侧的 kind 分类器（移除后无法编译） |
| `TestEstimateCostStillPricesCacheFreeTurnsWithUnknownDiscount`、`TestConfiguredRatesReturnsNilWhenNothingIsPriced` | 过严实现的守卫（不是回归探测器，如实标注） |

### 验证

```
go test ./internal/session ./internal/config ./internal/query ./internal/agentruntime ./internal/cli -count=1  # 全绿
go test ./... -count=1   # 全绿
go vet ./...             # 干净
gofmt -l .               # 空
git diff --check         # 干净
```

---

## 修复证据 · AUDIT-P1-13（2026-07-26）

后台命令此前是「起得来、看不清、停不下」：`Bash(run_in_background=true)` 建了 job、
写了日志，之后模型唯一的手段是 `Read(log_path)` 全量重读，而**任何工具都杀不掉那个进程**
（`Store.Kill` 只被 `/kill` 斜杠命令和 `/runtime/background/{id}/stop` 用，模型碰不到）。

### 修法

**① `BashOutput`（增量读，不重放）**

- 读游标持久化在 job 自己身上：[`background.Job.OutputOffset`](../../internal/background/background.go)，
  由 [`Store.ReadNewOutput`](../../internal/background/output.go) 推进。放在 job JSON 里而不是
  工具实例的内存里，是因为同一个 job 会被 CLI / server / TUI 三个进程看到，
  内存游标在换进程后就退化成全量重读。`Store.Logs` 保持全量语义不变，`/logs` 与 WebUI 不受影响。
- **游标只前进到真正返回过的字节**：单次读取上限 `MaxOutputChunkBytes`（30 000，与 Bash 前台
  `maxResultSizeChars` 同量级），超出部分置 `more_output=true` 让模型再调一次。
  如果按整份日志推进游标，结果又会被工具结果层截断，被截掉的尾巴就永久丢了。
- 日志变短（截断/轮转）时游标复位并置 `log_restarted`，否则游标卡在 EOF 之后会永远返回空。
- `running` **不只看 status**：`Job.Running()` = 非终态 status **且** `processAlive(PID)`
  信号 0 探测。只信 status 会把「监管进程自己死了、status 永远停在 running」的 job
  报成仍在跑（`Store.Start` 走 `cmd.Process.Release()`，本来就没人 `Wait`）。
  探测同时兼容 Unix 的 `ESRCH` 与 Windows/pidfd 的 `os.ErrProcessDone`，
  `GOOS=windows go vet ./internal/background` 与 `GOOS=linux/arm64 go build` 都验过。

**② `KillShell`（三态，不撒谎）**

- [`Store.Terminate`](../../internal/background/output.go) 返回 `killed` / `not_running` /
  `not_found`。三态不是装饰：**只有确认进程活着才报 `killed`**，否则模型会以为自己停下了
  一个早就退出的进程。status 仍标 killed（非终态 job 不该继续被当成在跑），outcome 报实话。
- `not_found` 是 `IsError: true`（id 打错了要让模型看见），`not_running` **不是** error
  —— 「已经结束了」是幂等成功，报错会诱导模型重试。
- 既有 `Store.Kill` 改为委托 `Terminate`，保住 `(bool, error)` 契约。顺带修掉一个潜在缺陷：
  旧 `Kill` 对 completed/failed 的 job 也会去 `proc.Kill()` 那个陈旧 PID（PID 复用时会杀错进程）。

**③ 工具描述让模型知道什么时候用**

- `BashOutput` 的描述明确写「这是轮询后台命令的正确方式，不要为此 `Read` 日志文件，
  因为 Read 每次都重放整份日志」，并指向 `KillShell`；`KillShell` 反向指回 `BashOutput`。
- 更关键的是 **Bash 自己的后台返回体**：`instructions` 从「read log_path」改成
  「Poll it with BashOutput (bash_id=…)」+「Stop it with KillShell」。模型看返回体的概率
  远高于回头重读工具描述，这一条才是让 P1-13 在实践中真正闭合的地方。
  `internal/query/query_test.go` 里两处把旧文案钉成契约的断言随之更新（唯一的跨边界改动，
  均为断言字符串，不碰 `query.go`）。

**④ 注册与截断保护**

- 注册点在 [`coreRuntimeTools`](../../internal/cli/interactive.go)，即 `cli.go:673` 传给
  `tools.GuardAll(policy, …)` 的那份列表 —— 权限链路自动生效，`cli.go` 一行未改
  （compact 分支零冲突）。默认权限落在 `allowUnlessRisky`：两者都不弹窗，与上游一致；
  `IsMutatingTool` 刻意未改动。
- 两个工具都实现 `MaxResultSizeChars()`（`ClaudeCodeDefaultDeclaredMaxResultSizeChars`），
  并加进 `TestClaudeCodeDefaultDeclaredToolResultLimits` 这份 AUDIT-P1-19 的清单，
  避免重蹈「漏实现即绕过 10 万字符截断」的覆辙。

### 测试与「删掉修复就变红」的对应关系

每条都实测过：先写测试看它红，再实现看它绿；实现完再逐条把修复点改坏，确认对应测试变红。

| 测试 | 删掉哪一处修复会让它变红 |
| --- | --- |
| `TestReadNewOutputReturnsOnlyBytesAppendedSinceLastRead`、`TestBashOutputReturnsOnlyOutputAppendedSinceLastCall` | 不持久化/不推进 `OutputOffset`（实测：改成不写回游标 → 两条全红，每次重放整份日志） |
| `TestReadNewOutputChunksLargeLogsWithoutLosingBytes`、`TestBashOutputChunksLongLogsAcrossCalls` | 去掉 `io.LimitReader` 分块（实测红）；或按整份日志推进游标 |
| `TestReadNewOutputRestartsWhenLogShrinks` | 去掉 `offset > size` 复位 |
| `TestReadNewOutputReportsNotRunningForStalePID`、`TestTerminateReportsNotRunningForStalePID` | `Job.Running()` / `Terminate` 里去掉 `processAlive` 探测（实测：`return true` → 前者报仍在跑、后者把没杀的说成 killed） |
| `TestTerminateKillsLiveProcess`、`TestKillShellTerminatesRunningProcess`、`TestBashOutputAfterKillDrainsRemainingOutput` | `Terminate` 只改 store 不发信号（实测：三条全红，`sleep 30` 5 秒后仍在跑） |
| `TestTerminateReportsAlreadyFinishedJob`、`TestKillShellReportsAlreadyFinishedJobWithoutError` | 把 `not_running` 也当 error，或让 `Terminate` 覆写已完成 job 的 status |
| `TestKillShellReportsUnknownID`、`TestBashOutputRejectsUnknownAndEmptyID` | 未知 id 不置 `IsError`（实测红） |
| `TestBashOutputReportsRunningThenFinishedProcess` | 结束后不带 `exit_code`（实测红） |
| `TestNewQuerySessionOffersBackgroundShellTools` | **删掉 `coreRuntimeTools` 里那两行注册**（实测：加之前就是红的 —— 这正是 wiring guard 的意义，注册只有一行，删掉不会让任何别的测试变红） |
| `TestBackgroundShellToolsGoThroughPermissionChain` | 绕过 `GuardAll` 注册；或去掉 `MaxResultSizeChars()`（同一条里一起断言） |
| `TestToolsDeclareResultSizeLimit`、`TestClaudeCodeDefaultDeclaredToolResultLimits/{BashOutput,KillShell}` | 去掉 `MaxResultSizeChars()`（实测红，AUDIT-P1-19 的同款形状） |
| `TestBashToolRunInBackgroundPersistsJobAndLogs`、`TestBashDescriptionPointsAtBashOutputForBackgroundPolling` | 后台返回体 / Bash 描述不提 `BashOutput`、`KillShell`（实测红） |

### 验证

```
go test ./internal/tools/... ./internal/background ./internal/cli -count=1   # 全绿
go test -race ./internal/background ./internal/tools/bash -count=1           # 全绿
go test ./... -count=1   # 全绿
go vet ./...             # 干净
gofmt -l .               # 空
git diff --check         # 干净
GOOS=windows go vet ./internal/background；GOOS=linux GOARCH=arm64 go build ./internal/background ./internal/tools/bashoutput
```

**边界**：Windows / Linux 只做了编译与 vet，未在真机上验证信号 0 探测与 kill 行为
（与既有 `PARITY-002` 的平台边界一致）。`processAlive` 对刚退出未被回收的僵尸进程会报「仍在跑」，
因此测试里显式 `Wait` 回收后再断言；实际路径上 Bash 的 waiter goroutine 会立即回收并写 status。

---

## 完成记录：AUDIT-P1-17 / P1-18（2026-07-26，MCP 可靠性与协议完成度）

两条都在 `internal/mcp/`，一起做是因为 P1-17 的修法（后台读循环）**同时**是 P1-18
里通知机制的前提 —— 帧不再由某次调用独占地读走，才谈得上处理无 id 的帧。

### 一、根因：一把锁圈住了整个往返

旧 `rpcConn.callWithCallback`（`rpc.go:38-74`）在一把 mutex 里做「写请求 → 阻塞
`ReadBytes('\n')` 直到读到自己的响应」。这一个结构同时导致了审计列出的五条里的四条：

- 读没有 deadline 也不看 context → **server 挂起 = 调用方 goroutine 永久死锁**；
- 锁跨越整个往返 → 同一 server 的并发工具调用互相排队；
- 没有地方处理无 id 的帧 → 通知全丢；
- 取消无从注入 → `tool.go` 只能在发请求**之前**看一眼 `ctx.Done()`。

所以改法是把读独立出来：`newRPCConn` 起一个后台读循环，按 id 把响应多路分解到各个
在途调用（`pendingCall`），写请求只在 `writeMu` 下串行化。锁的粒度从「一次往返」
降到「一次写」。

### 二、超时：分段，不是一个总 deadline

参考 AUDIT-P0-07 在 `internal/anthropic/httpclient.go` 立下的原则 —— 一个覆盖全程的
硬 deadline 会把合法的长响应砍断。MCP 的 `tools/call` 有完全一样的问题：跑测试、
编译、抓网页的工具调用几分钟很正常。预算因此拆成三段（`rpc.go` 的 `mcpTimeoutBudget`）：

| 阶段 | 默认值 | 语义 |
| --- | --- | --- |
| `request` | 60s | `initialize` 与各类 list/read/get 的**总时长**。元数据往返答不上来就是坏了 |
| `toolCallIdle` | 10min | `tools/call` 的**服务端静默上限**，不是总时长。收到任何一帧都续期 |
| `shutdown` | 5s | `Close` 时等子进程自己退出的宽限期，超时才 SIGKILL |

「收到任何一帧都续期」把 P1-18 的通知机制变成了 P1-17 的一部分：server 推
`notifications/progress` 就等于在说「我还活着」，长调用因此不会被误杀。反过来，
一个既不出声也不退出的 server 会在 10 分钟后拿到 `errMCPTimeout`，而不是永远吊着。

`errMCPTimeout` 是独立的哨兵错误，和用户按 ESC 的 `context.Canceled` 分得开 ——
和 `errProviderTimeout` 同样的理由。

**HTTP 侧同一个坑也修了**：`newHTTPRPC` 原本设 `http.Client{Timeout: 30 * time.Second}`，
那是个覆盖「连接 + 读完整个 body」的硬 deadline，任何超过 30s 的工具调用必挂。
现在 `http.Client` 不设 `Timeout`，改由每次调用按同一份分段预算加 context deadline。

### 三、进程监管与 stderr

- `StartStdio` 现在接 `cmd.StderrPipe()`，一个 goroutine 边记日志边留最后 8KB 尾巴
  （`stderr.go`）。旧实现从不给 `cmd.Stderr` 赋值，server 唯一的诊断信息直接进
  `/dev/null`，配错的 server 只会静默消失。
- `supervise` 负责唯一的一次 `cmd.Wait()`（按 exec 文档要求，等 stderr 排空之后再调），
  收尸不留僵尸，并在非主动关闭时 `observability.Warn`。
- `explainStreamEnd` 把「stdout 断了」升级成带退出码和 stderr 的具体错误。读循环在
  进程退出的瞬间就会看到 EOF，而退出码要等 `cmd.Wait` 返回，所以给它一个**有界**的
  等待窗口（`shutdown`），换来用户真正拿得去排障的一句话，而不是裸 EOF。
- `Close` 改为先关 stdin 让 server 自己收摊，赖着不走才升级到 SIGKILL，然后等
  `supervise` 收掉。旧实现直接 Kill 并自己 `Wait`，会和监管 goroutine 抢 `Wait`。
- `LoadTools` 的两处裸 `continue` 换成 `observability.Warn`，并按 server 名排序遍历，
  让加载顺序可复现。

### 四、AUDIT-P1-18：做了什么，没做什么，为什么

排序标准是「不做会导致真实 server 直接用不了」。

**已做**：

| 缺口 | 修法 |
| --- | --- |
| 无 `Mcp-Session-Id` | 从 initialize 的响应头取会话 id，之后每个请求回带；HTTP 404 且已有会话时报「会话过期」而不是裸状态码。**这条最致命** —— 合规 streamable-http server 的第二次调用必然被拒 |
| 无 protocolVersion 协商 | initialize 结果不再丢弃，采纳 server 回的版本并在后续请求带 `MCP-Protocol-Version` |
| server capabilities 从不读取 | 解析并据此拦截：只提供工具的 server 调 `resources/list` 会得到「does not support resources」而不是裸 `-32601`。这不是形式主义 —— `ListMcpResources` 会遍历所有配置的 server，之前每个只有工具的 server 都贡献一行没头没尾的 ERROR |
| 无通知机制 | 无 id 的帧不再丢弃：记进日志，并给在途调用的空闲计时器续期（见上） |
| 无分页 | `tools/list`/`resources/list`/`prompts/list` 跟随 `nextCursor`，带 100 页上限和「游标重复即停」兜底 |
| 非文本内容静默丢弃（**只做了一半**，见 TODO-061） | `renderToolContent` 给 image/audio/resource 留占位行。之前一个只返回图片的工具，在模型看来就是**什么都没返回**；现在模型至少知道有东西、能如实说明。**但模型仍然看不到内容** —— `tools.Result.Content` 是个 `string`，工具层没有通路把图片变成真正的 image block（provider 层支持，缺的是中间管道）。截图类、图表类工具因此基本残废，只是从「像是没返回」变成「说得清但拿不到」 |
| 命名冲突静默覆盖 | `safeName` 非单射（`get-item` 和 `get.item` 都变成 `get_item`），注册表 `Register` 后到者覆盖先到者。`LoadTools` 现在加数字后缀消歧并告警；适配器仍用 server 侧原名调用 |
| 取消不传播 | ctx 一路穿到 RPC 层；HTTP 侧 `http.NewRequest` → `NewRequestWithContext` |
| （审计未列）SSE 响应体里的通知 | `firstSSEData` 只取第一个 data 事件。server 合法地可以先推几条 progress 再给结果，那样解析出来的是通知的空壳。改为按 id 挑出应答帧，匹配不到才退回第一帧 |

**明确不做，理由如下**：

- **旧版 HTTP+SSE 传输**（`type: "sse"`）—— 那是两个端点的协议（GET 开流 + POST 发消息），
  和 streamable-http 不是一回事。硬映射到 streamable-http 只会以难懂的方式失败，所以
  改成**明确拒绝并给出可操作提示**（"use type \"http\" if the server also speaks
  streamable-http"），而不是假装支持。真要做需要独立的传输实现，单开条目。
- **OAuth** —— 需要授权码流、回调监听、token 存储与刷新，是独立的一块工作，
  和本条的可靠性主题无关。当前只支持静态 `headers`。
- **`tools/list_changed` 后重新注册工具** —— 通知现在**收得到**了，但收到之后不会热
  替换工具注册表。注册表（`internal/tools.Registry`）是跨包共享的、会话中途替换涉及
  正在进行的请求，改动面远大于本条。已在 `observeNotification` 的注释里写明。
- **图片/音频真正送达模型** —— 上面那条只做到「不再静默丢弃」。要让模型真看见图片，
  得把 `tools.Result` 的纯 `string` 内容扩到能带 image block（provider 层已支持
  `NewImageBlockBase64`，缺的是工具层的管道）。那是工具返回值契约的改动，影响所有工具，
  不在本条范围。**已登记为 TODO-061**。
- **一次调用内的多路复用仅限同一连接** —— 实现的是「同一 server 的并发调用不再互相
  排队」；server→client 回调因为协议不做关联，路由到最近发起的带 handler 的调用。

### 五、新增测试（每条都做过「移除修复→红 / 加上修复→绿」A/B）

**如实标注方法学**：P1-17 的超时/取消/多路复用几条是先写测试看它红再实现的；
P1-18 的若干条（分页、非文本内容、会话 id、命名消歧）是在重写 `rpc.go` 时一并落的代码，
测试后补，因此**逐条做了「机械移除该处修复 → 复跑 → 确认转红 → 还原」的 A/B**，
下表的第二列就是每次实际移除的东西。capability 拦截那条是严格先红后绿。

| 测试 | 移除哪一处修复后转红 |
| --- | --- |
| `TestStdioCallTimesOutWhenServerNeverResponds` | `budgetFor` 一律返回 `limit: 0`（旧行为：完全无超时）→ 调用永不返回 |
| `TestStdioToolCallOutlivesTheMetadataRequestBudget`、`TestHTTPToolCallGetsTheIdleBudgetNotTheRequestBudget` | `tools/call` 改用 `request` 总预算（即「一刀切总超时」这个错误修法）→ 长调用被砍 |
| `TestStdioToolCallIdleTimerIsResetByServerNotifications` | `dispatch` 里去掉 `touchPending()`（旧行为：无 id 的帧丢弃）→ 空闲计时器不续期 |
| `TestStdioCallReturnsWhenCallerContextIsCancelled`、`TestStdioToolCallStopsWhenTheCallerCancels` | `await` 去掉 `case <-ctx.Done()` |
| `TestHTTPCallStopsWhenTheCallerCancels`、`TestReadHonoursACancelledContext`（mcpresources） | `NewRequestWithContext` 换回 `http.NewRequest` |
| `TestStdioCallsAreMultiplexedNotSerialized` | 给整个往返重新加上一把锁（旧结构）→ 第二个请求写不出去 |
| `TestStartStdioSurfacesServerStderrWhenTheHandshakeFails`、`TestStdioCallReportsServerCrashWithExitStatusAndStderr` | 不接 `StderrPipe` |
| `TestCloseReapsTheServerProcess` | 不起 `supervise`（旧行为：从不 `cmd.Wait`）→ 连 `Close` 都挂死 |
| `TestLoadToolsWarnsWhenAServerCannotStart`、`TestLoadToolsWarnsWhenListToolsFails` | 两处 `observability.Warn` 换回裸 `continue` |
| `TestHTTPClientReplaysTheSessionIdIssuedAtInitialize` | 不回带 `Mcp-Session-Id` → 第二次调用被 404 |
| `TestHTTPClientAdoptsTheProtocolVersionTheServerAnswered` | 不发 `MCP-Protocol-Version` |
| `TestListToolsFollowsThePaginationCursor` | 不跟随 `nextCursor` |
| `TestCallToolKeepsNonTextContentVisible` | `renderToolContent` 改回只留 `type=="text"` |
| `TestLoadToolsDisambiguatesCollidingToolNames`、`TestRenamedToolStillCallsItsOriginalServerSideName` | 去掉 `uniqueToolName` |
| `TestListResourcesReportsUnsupportedInsteadOfARawMethodNotFound` | capability 拦截（**这条是严格先红后绿**） |
| `TestSSEResponsePicksTheAnswerNotAnEarlierNotification` | `sseResponseData` 换回「取第一个 data 事件」（**先红后绿**） |

**不是回归探测器、如实标注为守卫的**：`TestStdioCallPrefersADeliveredResponseOverConnectionClose`
（防新架构自身引入的「响应与断连竞争」丢包）、`TestPaginationStopsOnARepeatedCursor`
（防我自己加的分页循环转不出来）、`TestStdioCallFailsWhenServerClosesTheConnection`
与 `TestLoadToolsKeepsHealthyServersWhenOneFails`（旧实现也满足）、
`TestSSETransportIsRejectedWithAnActionableMessage`（措辞从「unsupported type」改善为可操作提示）。

**测试基建**：stdio 用例改用「把测试二进制自己 re-exec 成 stub server」
（`stubserver_test.go` 的 `TestMain`），不再写临时 shell 脚本 —— macOS 对新写入的可执行
文件首次 exec 要做约 2 秒的 Gatekeeper 扫描，每个 fixture 都会付这笔钱（实测单条用例
2.1s → 0.03s）。既有的 4 条 `rpc_test.go` 用例原本喂一个**预置好响应**的
`strings.Reader`，那种 fixture 在后台读循环下会在调用方登记之前就把响应读掉，
已改用 pipe fixture：测的契约没变，只是不再依赖「读发生在 `call` 内部」这个实现细节。

**顺带补 AUDIT-P1-19 的一角**：`internal/tools/mcpresources` 覆盖率 **12.0% → 78.0%**
（新增 8 条用例）。它依赖的 `StartConfigured` 签名未变，但 `ListResources`/`ReadResource`
改用了新的 `...Context` 变体，让工具侧的 ctx 真正传到传输层。
`internal/mcp` 覆盖率 78.9%。

### 五点五、合并前 review 揪出的 7 个新缺陷（都已修 + A/B）

后台读循环 + 子进程监管是新写的并发代码，`-race` 跑绿并不代表没问题 —— 竞态检测器
只能看见测试真的走到的路径，而下面这些全都要求**子进程行为不端**才会触发。

| # | 缺陷 | 后果 | 修法 |
| --- | --- | --- | --- |
| 1 | `Close` 无界等 `c.exited`，而 `supervise` 要先等 stderr 排空。`Kill` 只杀直接子进程，**孙进程**（`npx` → node、包装脚本）继承 fd 2 后管道永不 EOF | `Close` 永久挂起 + goroutine/fd 泄漏。`LoadTools` 的 cleanup 是串行关的，一个这样的 server 就能挂住整个进程退出。**这是相对旧实现的回归** —— 旧代码不接 StderrPipe，所以不会 | `cmd.WaitDelay` 让 `Wait` 在进程退出后强制收管；`Close` 与 `supervise` 的每一处等待都加上界 |
| 2 | `supervise` 只等 stderr 排空就调 `cmd.Wait`，而 `Wait` 结束时会关掉 **StdoutPipe** 的读端 | 与读循环竞争，可能把 server 的最后一帧撕掉 —— 那一帧往往正是解释它为何退出的错误；且报出的错误会变成 `file already closed` 而不是走 EOF 分支 | `supervise` 同时等读循环的 `readStopped`（读循环在问退出码之前就发这个信号，避免互等） |
| 3 | stderr 排空用 `bufio.Scanner`，遇到超过 256KB 的单行就 `ErrTooLong` 并**永久停止读取** | 64KB 的 stderr 管道填满，子进程**永久阻塞在 `write(2)`**；随后每次 `tools/call` 都要等满 10 分钟空闲超时 | 扫描器循环之后无条件 `io.Copy(io.Discard, teed)`，行日志的长度上限不再影响是否继续读 |
| 4 | `writeFrame` 无时限且在 `writeMu` 里，而写发生在 `await` **之前** | ①超时预算和 context 都够不着，不再读 stdin 的 server 让调用永久阻塞、ESC 无效；②真死锁：server 发 `sampling/createMessage` 后停止读 stdin 等应答，客户端同时在写一个大 `tools/call` 占着 `writeMu`，回调应答永远拿不到锁 | 给 stdin（可轮询管道）设写 deadline；写失败后流里可能只剩半行 JSON，直接判连接死亡让所有调用方快速失败 |
| 5 | HTTP 侧 `ctx.Err() != nil` 就报超时，但 ctx 是**包了 WithTimeout 的调用方 ctx** | 用户按 ESC 被报成「server 10 分钟没答」，`errors.Is(err, context.Canceled)` 变成 false，与 stdio 侧行为不一致 | 只在 `errors.Is(ctx.Err(), context.DeadlineExceeded)` 时才转成 `errMCPTimeout` |
| 6 | `activeHandler` 把 server→client 请求路由到「最大 id 的带 handler 调用」 | 旧的串行实现下永远只有一个在途调用，所以总是对的；多路复用之后会**归错工具**：审批弹窗显示另一个工具的名字，写回的权限规则也记到那个工具头上 | 恰好一个候选才回答，多于一个就带明确理由拒绝（此前不可能出现两个，所以不构成回归） |
| 7 | id 直接解成 `int64`，而 JSON-RPC 2.0 允许字符串 id | 整帧解析失败 → 帧被静默丢弃 → 调用方等满预算。多路分解现在完全以 id 为键，比以前更吃这个 | 收帧时 id 用 `json.RawMessage`；应答的数字 id 兼容字符串写法，server→client 请求的 id **原样回传**（新增 `callbackResponse`，因为 `rpcResponse.ID` 是 `int64` 且带 `omitempty`） |

新增的对应测试同样做了 A/B（移除该处修复即转红）：
`TestCloseDoesNotWedgeOnAServerThatLeaksTheStderrPipe`、
`TestStdioSurvivesAnOverlongStderrLine`、
`TestStdioWriteToAServerThatStoppedReadingStdinTimesOut`、
`TestHTTPCancellationIsNotReportedAsATimeout`、
`TestServerCallbackIsRefusedWhenItCannotBeAttributed`、
`TestStdioAcceptsAStringResponseId`、
`TestServerCallbackEchoesAStringIdVerbatim`。

**如实标注两条**：`TestStdioKeepsTheLastFrameFromAServerThatAnswersThenExits`（#2）
即使去掉修复也**跑不红** —— `bufio` 在子进程退出前就把那一行吸进缓冲区了，竞争窗口
太窄。缺陷本身是实的（独立探针复现了 `read |0: file already closed`），修也修了，
但这条测试只能算守卫，不是回归探测器。`TestHTTPTimeoutIsStillReportedAsATimeout`
同理，它守的是 #5 修复不要矫枉过正。

**review 提出但本轮不改的两条**：

- `ToolAdapter.Run` 把错误 `err.Error()` 成字符串塞进 `tools.Result`，于是取消
  变成一条内容为 `context canceled` 的普通工具失败喂回模型，上层再也做不了
  `errors.Is`。**这是既有行为**（旧实现一样），要改得动 `tools.Tool` 接口的返回
  约定，涉及所有工具，超出本条范围。
- `Client.Initialize` 是导出方法却写无锁字段 `negotiated`。当前不可达 ——
  只有 `StartStdio` / `StartHTTP` 在 `*Client` 逸出之前同步调用它。

### 六、风险与边界

- **未连接真实 MCP server**。所有协议行为都是对着 spec 和 stub server 验证的，
  与原审计同一条边界。
- **`request` 默认 60s 是新增的硬上限**。此前元数据调用可以无限等；现在一个在
  60s 内答不出 `tools/list` 的 server 会被判失败。这是刻意的取舍（原行为是死锁），
  但确实是行为变更。
- **server→client 回调的路由是启发式的**：协议不给关联信息，选最近发起的带 handler
  的调用。单调用场景（绝大多数）与旧行为一致。
- 回调现在在独立 goroutine 里处理，不再阻塞读循环 —— 否则一次要等用户批准的
  elicitation 会把同一 server 上其它在途调用一起挂起。代价是「请求帧与回调应答帧
  的写入顺序」不再保证，两条既有用例已相应改为等待而非断言行号。

### 验证

```
go test ./internal/mcp ./internal/tools/mcpresources -count=1   # 全绿
go test -race ./internal/mcp -count=1                           # 全绿
go test ./... -count=1                                          # 全绿
go vet ./...                                                    # 干净
gofmt -l .                                                      # 空
git diff --check                                                # 干净
```

---

## 完成记录：AUDIT-P1-14 / P1-15 / P1-16 / P1-19（2026-07-26，工具诚实性）

主题一句话：**工具要么诚实，要么明确报错，绝不伪造。** 四条的共同点是工具对模型撒谎 ——
伪造截图、静默损坏用户文件、名字与实现不符、把导航链接当搜索结果 —— 或者在模型看不见的地方
把凭据递给子进程。

### AUDIT-P1-15 · NotebookEdit 静默损坏用户文件（本批危害最高）

`rawCell` 只有 5 个固定字段，`json.Unmarshal` → `json.MarshalIndent` 的往返把其余一切丢掉：
nbformat 4.5 **必需**的 cell `id`、`attachments`、任何厂商扩展、以及顶层未知键。每编辑一次
就产出一份 schema 非法的 notebook，而且没有任何报错。这不是功能缺失，是破坏用户数据。

改为保留整份解码后的文档（`map[string]any` + `[]map[string]any` 的 cells 视图），只覆写被编辑
那一格的 `source` / `cell_type`。另外用 `json.Decoder.UseNumber()` 解码：默认路径会把所有数字
过一遍 `float64`，大整数和高精度小数在保存时会被改写 —— 同一类静默损坏，只是更隐蔽。

键顺序变成字典序。这是无损的，而且正是 `nbformat` 自己写文件的顺序（它用 `sort_keys=True`），
所以没有引入 diff 噪音。

### AUDIT-P1-14 · WebBrowser 的假截图

`screenshot()` 从不渲染任何东西：它拼一段只含标题和 URL 文本的 SVG，base64 编码后按
`data:image/svg+xml;base64,...` 返回。模型收到一个 data URL，合理地认为自己拿到了页面图像。

fallback 模式下改为**明确报错**，并给出启用真浏览器的具体两步（`GOLANG_CLAUDE_CODE_WEBBROWSER_MODE`
+ `GOLANG_CLAUDE_CODE_PLAYWRIGHT_RUNNER` 指向 `scripts/playwright-browser-runner.mjs`），以及
「现在能用什么」（`action=text` / `action=links`）。真浏览器模式下 `screenshot` 走 adapter，行为不变。
`screenshot()` 和只服务于它的 `escapeXML()` 一并删除；工具描述现在开宗明义写清两种模式的区别。

### AUDIT-P1-16 · 五条路径把含 API key 的完整环境透传给子进程

只有 Bash 工具剥离凭据，而且剥的是一份**枚举出来的 8 个名字**。其余五条路径连这个都没有：
`hooks`（可来自项目级 `.claude/settings.json` —— clone 一个仓库就可能被拿走 key）、
`powershell`（Windows 上是主 shell，等于脱敏完全缺席）、`workflow`、`webbrowser` 的 playwright
runner、以及每个 stdio MCP server（`cmd.Env` 只在 `len(cfg.Env) > 0` 时设置，否则 nil = 全量继承）。

新增 `internal/procenv` 作为唯一实现，六处复用（Bash 的 `commandEnv` 保留为薄封装，继续叠加它
自己的 `GIT_EDITOR`/`GIT_TERMINAL_PROMPT`）。选成 zero-dep 叶子包而不是从 `internal/tools/bash`
导出：`internal/mcp` 和 `internal/hooks` 依赖 `internal/tools` 会引入一大片无关依赖。

**关于「denylist 改 allowlist」**：原始要求里这一条与同一条要求的「别破坏 hooks/workflow 依赖的
正常环境变量」直接冲突 —— 严格白名单会掐掉 `GOPATH`/`GOCACHE`/`NODE_ENV`/`CI` 以及用户 hook
自定义的一切变量。经确认后改为**按名字形状匹配的 denylist**：子串 `SECRET`/`PASSWORD`/`PASSWD`/
`CREDENTIAL`，后缀 `_TOKEN`/`_KEY`，加上原有的精确名单。三个漏网的 key（`GITHUB_TOKEN`、
`AWS_SECRET_ACCESS_KEY`、`OPENAI_API_KEY`）全部拦下，且未来新出现的凭据自动覆盖，同时
`PATH`/`HOME`/`GOPATH`/`SSH_AUTH_SOCK` 等照常透传。

`extra` 参数（MCP server 配置的 `env`、sandbox spec 的 `Env`）**刻意不过滤** —— 那是调用方
明确要传的，MCP server 配一个 `GITHUB_TOKEN` 正是它的用途，不是本条要防的泄漏。

**明确未做**：审计条目还说 hooks 是「权限 / 沙箱 / 脱敏的三重旁路」。本次只闭合脱敏这一重。
hooks 仍然直接 `/bin/sh -c`、不过沙箱、不过权限策略 —— 那两条改动面大得多（要决定 hook 该在
哪个沙箱 profile 下跑、项目级 hook 要不要单独提示授权），应单独立项。

### AUDIT-P1-15 · WebSearch 与 LSP 的名不副实

**WebSearch**：`parseResults` 用一条 `<a href>` 正则扫全页，没有任何结果容器限定，于是引擎自己的
导航栏和页脚被当成搜索结果返回。改为：页面含 `class="result__a"`（DuckDuckGo HTML 端点的结果
标记）时只取这些锚点；自定义端点无此标记时回退到「绝对的站外链接」，至少排掉相对路径的站内
chrome 和指回引擎自身的链接。描述同步写明这是**抓 HTML 页面而非搜索 API**，只有标题和 URL，
引擎改版会降级。

**LSP**：名字承诺一个 language server，实现是进程内 `go/parser`。不启 gopls、不做类型解析、
只支持 Go、`references()` 是按标识符名字裸匹配（跨包同名符号、局部变量、结构体字段全混在一起，
它分不出来）。行为没改 —— 这是本条要求的「至少在工具描述里说清它实际能做什么」—— 描述现在
逐条如实陈述，包括 `diagnostics` 只有语法错误、没有类型错误也没有 vet。

### AUDIT-P1-19 · 覆盖率与结果截断（PARTIAL）

`MaxResultSizeChars()` 补给 `ls` / `notebook`（Read+Edit）/ `webbrowser` / `workflow`，并加进
`TestClaudeCodeDefaultDeclaredToolResultLimits` 那份清单。

**`Read` 是误报**：它没有 `MaxResultSizeChars()`，但有 `SkipToolResultBudget() = true` ——
它自己按 2000 行默认上限 + 大文件 manifest 限流，是**故意**不进共享预算的。补一个
`MaxResultSizeChars()` 会是死代码（`EffectiveResultLimit` 的 skip 分支先短路）。改为新增
`TestReadOptsOutOfTheResultBudgetDeliberately` 把这个意图钉住，并在测试里写明原委。

覆盖率（`go test -cover`）：

| 包 | 审计实测 | 现在 |
| --- | --- | --- |
| `internal/tools/powershell` | 18.0% | 58.0% |
| `internal/tools/worktree` | 45.2% | 85.7% |
| `internal/tools/workflow` | 50.9% | 84.5% |
| `internal/tools/taskoutput` | 56.2% | 93.8% |

`powershell` 的天花板是本机/CI 无 `pwsh`：需要真进程的分支只能 skip，所以补的是输入校验、
沙箱拒绝、超时、以及 `limitedBuffer` 的截断语义。`worktree` 的 `add`/`remove` 此前**完全未测**，
现在用真 git 仓库跑通 add → list → remove 与 `--force` 路径。

**`Task` 的顶层 `required[]` 未做**：单任务（`description` + `prompt`）与 `tasks` 批量是二选一，
直接写 `required: ["description","prompt"]` 会把批量形式禁掉。正解是根级 `anyOf`，但根级 `anyOf`
在各 provider 的 schema 校验里兼容性不一，需要单独决策，不适合夹在本批里猜。故本条记 PARTIAL。

### 顺带核实、决定不动的一处

`workflow` 的失败步骤返回 `IsError: false`，只在 JSON 里给 `"ok": false` + `failed_step`。
初看是同一类「不诚实」，但既有测试 `TestWorkflowRejectsUnsafeWriteStep` 用
「workflow returns structured failure, not tool error」把它钉成了显式契约，且结构化失败对模型
是可读的、不构成伪造。新增测试改为断言该契约，未改行为。

### 方法学

每条修复都先写红测试再改代码。P1-16 的五条路径做了统一的 A/B：把 `procenv.Sanitized` 临时改成
`return append(os.Environ(), extra...)`，五个断言同时转红，还原后同时转绿 —— 证明五条各自真的
接上了同一份实现，而不是某条恰好因为别的原因通过。

红态证据（移除修复后的实际失败）：

| 测试 | 移除修复后 |
| --- | --- |
| `notebook.TestNotebookEditPreservesUnknownFields` | 4 项同时红：顶层未知键被丢、被编辑 cell 的 `id` 为 `<nil>`、`attachments` 被丢、未被编辑 cell 的 `id` 也被丢 |
| `webbrowser.TestWebBrowserScreenshotRefusesWithoutRealBrowser` | 返回 `IsError:false` 且带 `data:image/svg+xml;base64,...` |
| `hooks.TestRunnerDoesNotLeakSecretsToHookCommands` | hook 看到 `leaked-ANTHROPIC_API_KEY\|leaked-GITHUB_TOKEN\|leaked-AWS_SECRET_ACCESS_KEY\|leaked-OPENAI_API_KEY\|leaked-MY_DB_PASSWORD` |
| `workflow.TestWorkflowRunDoesNotLeakSecretsToSteps` | 步骤输出四个 key 的真实值 |
| `webbrowser.TestBrowserAdapterDoesNotLeakSecrets` | runner 收到四个 key 的真实值 |
| `mcp.TestStartStdioDoesNotLeakSecretsToServers` | stdio server 收到四个 key 的真实值 |
| `powershell.TestCommandEnvStripsSecrets` | 子进程 env 含四个 key |
| `websearch.TestWebSearchIgnoresNonResultLinks` | 7 条「结果」里 4 条是 Settings / All Regions / About / Privacy Policy |
| `lsp.TestDescriptionStatesActualCapability` | 描述不含 not a language server / go/parser / no type resolution / Go only |
| `websearch.TestDescriptionStatesActualSource` | 描述不含 duckduckgo / scrap / not a search api |
| `tools.TestClaudeCodeDefaultDeclaredToolResultLimits/{LS,NotebookRead,NotebookEdit,WebBrowser,Workflow}` | `EffectiveResultLimit() = 200000, want 50000` |

五条 env 路径各有一条独立断言，且每条都断言 `GOPATH=/keep/gopath` 仍然透传 —— 只证明「secret
没了」而不证明「正常变量还在」，会把一个坏成空环境的实现也判绿。

### 验收命令

```
go test ./internal/tools/... ./internal/hooks ./internal/mcp ./internal/procenv -count=1   # 全绿
go test ./... -count=1                                                                      # 全绿
go vet ./...                                                                                # 干净
gofmt -l .                                                                                  # 空
git diff --check                                                                            # 干净

## 修复证据 · AUDIT-P1-35（2026-07-26）

本条是 AUDIT-P0-04 修复过程中登记的：**引擎修好了，状态从不上报**。P0-04 让
darwin profile 真的 `(deny network*)`、让不可强制的网络选项在 `failIfUnavailable`
下拒绝启动，但那条显式上报只到 API 层。用户能看到的沙箱状态只有一个标签，而它
直接读 settings。

### 一、原来的谎言

`tuiSandboxLabel`（`internal/cli/interactive.go`）此前的逻辑是「`Network.Disabled`
或 `AllowDomains` 或 `DenyDomains` 任一有值 → 打印 `network`」。于是：

- 一台 PATH 里没有 `sandbox-exec` 的 mac 上，标签照样打印 `on/network` ——
  实际上**没有任何一条 shell 命令进了沙箱**。
- 只配 `allowDomains` 时标签打印 `on/network`，而域名过滤**只作用于内置 HTTP 工具**，
  shell 命令直连网络。

`sandbox.IsAvailable` / `sandbox.UnavailableReason` 全仓零调用方，所以这两个本来
就能说出真相的函数从未被问过。这是最危险的一类错觉：**用户以为自己被隔离了，
并据此行事。**

### 二、改法

沿用 AUDIT-P0-06（`Config.Warnings`）的取舍：**只告警不拒绝**，且**无告警时字段
不出现、健康安装输出一字未变**。

- 新增 `internal/sandbox/state.go`：`Describe(cfg) State`，返回
  `{Configured, Active, Label, Warnings}`。`Active` 是「OS 沙箱真的会包住 shell
  命令」，`Warnings` 每条都说清**哪一项没生效、为什么、在这台机器上能不能补救**。
- **平台是参数而不是直接查 `runtime.GOOS`**：`Platform{GOOS, HasBinary}`。这是为了
  让 linux 分支能在 darwin 主机上被测到（见 §四）。`IsAvailable` /
  `UnavailableReason` / `NetworkPolicyGaps` 全部改为薄封装，于是「零调用方」也
  一并消掉。
- `NetworkPolicyGaps` 的字符串拆成结构化 `PolicyGap{Key, Detail, Remedy}`：`Key`
  是标签里用的短名（`network.domains` / `network.proxy` / `network.disabled`），
  `Detail`+`Remedy` 进 warnings。既有的 `PrepareShell` 强制路径和 P0-04 的两个
  测试**未受影响**（`String()` 保留了 `domain-filtered` / `mitm.required` 这两个
  被测试钉住的子串）。
- 标签语法：
  - `off` — 没开，输出与旧实现一致。
  - `on[/seccomp][/network][/sockets]` — **完全生效时与旧实现逐字节一致**。
  - `degraded/not-enforced` — 二进制缺失 / 平台不支持 / 本平台不在
    `enabledPlatforms`。刻意不以 `on` 开头，因为它不能被读成「在保护你」。
  - `on[/…]/degraded:network.domains,network.proxy` — OS 沙箱在跑，但被点名的
    选项对 shell 命令是空操作。
- **不再打印没生效的部件**：旧实现让 `allowDomains` 也点亮 `network`。现在 `network`
  只在 `NetworkDisabled` 且真的被强制时出现，否则那就是同一个谎言换了小号字体。
- 上报面：`/status` 的 `sandbox` + `sandboxWarnings`（仅在有 warning 时出现），
  以及 TUI 欢迎卡。欢迎卡**宽窄两种布局都加了** —— 终端窄不是隐藏「沙箱没生效」
  的理由。

### 三、实跑输出

```
# 默认安装（sandbox.enabled=false）与完全生效（network.disabled=true）
$ golang-cc --cwd <tmp> status | grep -c sandbox
0          # 两种情况都是 0：健康安装一个字都不多

# allowDomains + proxy.required（本机 macOS，sandbox-exec 存在）
sandbox label: on/degraded:network.domains,network.proxy
 - sandbox.network.allowDomains/denyDomains filter the built-in HTTP tools only:
   shell commands are not domain-filtered. Set sandbox.network.disabled=true to
   cut shell network access entirely, or enforce the domain list at the network
   layer; it cannot be enforced in-process.
 - sandbox.network.proxy.required/mitm.required are checked for known network
   commands only: a compiled binary or inline interpreter bypasses them. Enforce
   the proxy at the network layer, or set sandbox.network.disabled=true; it
   cannot be enforced in-process.

# 真机制造二进制缺失：PATH=/nonexistent
sandbox label: degraded/not-enforced
 - sandbox.enabled is set but sandbox-exec was not found on PATH, so no shell
   command is sandboxed on this machine. sandbox-exec ships with macOS, so a PATH
   that drops /usr/bin is the usual cause: repair PATH, or set
   sandbox.enabled=false so the configuration stops implying protection you do
   not have.
```

### 四、测试（每条都验证「移除修复后失败、加上修复后通过」）

| 测试 | 覆盖 | mutation check（移除修复后的表现） |
| --- | --- | --- |
| `sandbox.TestDescribeStaysQuietOnHealthyInstall` | **反方向**：darwin+linux 全配齐且二进制在位 → 标签恰为 `on/seccomp/network/sockets`、warnings 为空、`Active` 为真 | 新 API 不存在时编译失败；把 warning 改成无条件产出即红 |
| `sandbox.TestDescribeStaysQuietWhenSandboxIsOff` | **反方向**：`enabled=false` 时即便配了 3 个不可强制项也一律 `off`、零 warning | 同上 |
| `sandbox.TestDescribeDegradesLabelWhenSandboxBinaryMissing` | darwin/linux 二进制缺失：标签不以 `on` 开头、含 `not-enforced`；warnings 点名 `sandbox-exec`/`bwrap`、说出后果、给出各平台可行的补救 | 同上 |
| `sandbox.TestDescribeDegradesLabelWhenPlatformNotEnabled` | 本平台不在 `enabledPlatforms` → 降级并点名 `sandbox.enabledPlatforms` 与平台名 | 同上 |
| `sandbox.TestDescribeDegradesLabelOnUnsupportedPlatform` | 不支持的 GOOS → 降级并如实说明本平台无解 | 同上 |
| `sandbox.TestDescribeNamesUnenforceableNetworkOptionInLabel` | `allowDomains` / `proxy.required` 各自让标签出现 `degraded:<key>`，且 `Active` 仍为真（文件系统沙箱在干活） | 同上 |
| `sandbox.TestDescribeDropsMisleadingNetworkPartFromLabel` | 标签不再为未强制的域名列表点亮 `network` | 同上 |
| `sandbox.TestUnavailableReasonSummarisesState` | 健康时返回 `""`；缺二进制时点名二进制 | 同上 |
| `cli.TestTUISandboxLabelUnchangedOnHealthyInstall` | **反方向**：全配齐时 `tuiSandboxLabel` 逐字节等于旧输出、`tuiSandboxWarnings` 为空 | 把 `tuiSandboxLabel` 还原成读 settings 的旧实现后**仍绿** —— 这正是「健康安装输出未变」的证明 |
| `cli.TestTUISandboxLabelOffWhenDisabled` | `nil` settings → `off`、零 warning | 同上 |
| `cli.TestTUISandboxLabelDegradesWhenNetworkPolicyUnenforceable` | 审计点名的那个配置：`allowDomains` 必须让标签降级并点名 `network.domains`，且不再声称 `network` | **还原旧实现 → `label = "on/network"`**，逐字复现审计描述的谎言 |
| `cli.TestStatusReportsSandboxWarnings` | 端到端跑真实 `status` 命令：`sandboxWarnings` 点名选项、原因，并带可执行的补救（断言含 `sandbox.network.disabled=true`） | 去掉 statusPayload 里的字段即红 |
| `cli.TestStatusOmitsSandboxWarningsWhenNothingIsWrong` | **反方向**：默认安装的 `status` 输出里 `sandboxWarnings` 键**不存在**，且整份输出不含 `sandbox` 字样 | **把字段改为无条件输出 → 立刻红**，所以这条反向断言不是摆设 |
| `tui.TestWelcomeCardShowsSandboxWarnings` | 宽（120）窄（80）两种布局都渲染出前缀、原因和后果 | 摘掉两处渲染循环 → 卡片只剩 `sandbox degraded/not-enforced`，无原因（已实跑确认转红） |
| `tui.TestWelcomeCardUnchangedWithoutSandboxWarnings` | **反方向**：`nil` 与空切片渲染结果完全相同、健康卡片不含 `not enforced`、沙箱标签仍在 | 保证新字段不给健康安装引入噪音 |
| `cli.TestTUIWelcomeInfoReflectsRuntimeConfig`（既有） | 期望值从 `on/seccomp/network/sockets` 改为 `on/seccomp/sockets/degraded:network.domains` | **旧期望值本身就是这条 bug 的化石**：该配置只有 `denyDomains` 没有 `network.disabled`，注释已写明 |

### 五、风险与边界

- **Linux 侧未经真机验证**。本机是 macOS 且无 bubblewrap，所以 linux 分支
  （`bwrap` 缺失的降级与文案）是靠注入 `Platform{GOOS:"linux", HasBinary:…}`
  覆盖的，**没有在真实 Linux 主机上跑过**。这与 PARITY-001 是同一条既有边界。
  darwin 侧的二进制缺失路径则是真机验证过的（`PATH=/nonexistent`）。
- **`seccomp` 部件在 darwin 上仍是空操作**，标签照旧打印它。`SeccompEnabled` 只被
  `linuxSandboxSpec` 使用，macOS profile 完全不看 —— 这与本条的主题（配了就以为
  在保护你）同源，但它不属于 `NetworkPolicyGaps` 的契约，也不在本条验收范围内，
  **当时刻意未改**，登记为 AUDIT-P1-37 并已随后闭合（见 [修复证据](#修复证据--audit-p1-372026-07-26)），同批还收了镜像情形：`allowPty` 只被 darwin profile 读取，linux 侧从不查询。
- **标签变长了**。`on/degraded:network.domains,network.proxy` 比 `on/network` 长得多，
  窄终端里状态行可能被截断。取舍是明确的：宁可截断也不要一个看起来生效的短标签，
  而完整原因始终在欢迎卡与 `/status` 里。
- `Describe` 每次调用做 1 次 `exec.LookPath`（一个 stat）。调用点是欢迎卡组装与
  `/status`，不在渲染热路径上。

### 验证

```
go test ./internal/sandbox ./internal/cli ./internal/tools -count=1   # 全绿
go test ./internal/tui -count=1                                       # 全绿
go test ./... -count=1                                                # 全绿，0 FAIL
go vet ./...                                                          # 干净
gofmt -l .                                                            # 空
git diff --check                                                      # 干净
```

---

## 修复证据 · AUDIT-P1-37（2026-07-26）

紧接 [AUDIT-P1-35](#修复证据--audit-p1-352026-07-26)。P1-35 把「配了就以为在保护你」
这件事修到了网络项，但它的验收范围限于 `NetworkPolicyGaps` 契约，于是留下了同类的
非网络残留 —— 这条就是那个残留，由 P1-35 的合并 review 自己发现并登记。

### 一、原来的谎言

`sandbox.seccomp.enabled` 全仓只有两个生产读取点，都在 linux 分支
（`linuxSandboxSpec` 拼 `--seccomp` fd、`linuxSandboxEnv` 置 `SANDBOX_SECCOMP`，
`createSeccompProfileFile` 的唯一调用点也在前者）。macOS profile 完全不看它 ——
seccomp 是 Linux 内核设施，在 darwin 上**不可能**生效。而 P1-35 修完后标签仍然
无条件为 `SeccompEnabled` 打印 `seccomp`：

```
# 修复前，macOS + seccomp.enabled: true + network.disabled: true
label: on/seccomp/network        # "seccomp" 是纯装饰，零系统调用过滤
```

**镜像情形**：`sandbox.allowPty` 只被 `macOSSandboxProfile` 读取，linux 侧从不查询。

### 二、改法

- `PolicyGap` 增加 `Network bool`，把 gap 分成两类。`NetworkPolicyGaps` 只返回
  `Network` 那一类，**`PrepareShell` 在 `failIfUnavailable` 下的拒绝契约（AUDIT-P0-04）
  因此一字未变**。
- 新增 `platformPolicyGaps`：本平台根本不读的选项。`seccomp` 在非 linux、`allowPty`
  在非 darwin 时各产出一条 gap。
- 标签只在 `p.supportsSeccomp()` 时才打印 `seccomp`；否则进 `degraded:seccomp`。
- `supportsSeccomp()` / `readsAllowPty()` 两个断言**直接对应唯一的生产读取点**
  （`linuxSandboxSpec` / `macOSSandboxProfile`），改动那两处时不改这里会立刻测试转红。

```
# 修复后，同一份配置
label: on/network/degraded:seccomp
 - sandbox.seccomp.enabled is ignored on macos: seccomp is a Linux kernel facility
   and only the bubblewrap path passes a filter, so no syscall filtering is applied
   here. There is no fix on this platform: drop the option so it stops implying
   syscall filtering, or run on Linux with bubblewrap.

# 不配 seccomp 的健康 macOS 安装：status 里 sandbox 字样 0 次（未变）
```

### 三、两个刻意的取舍

1. **平台 gap 不进 `failIfUnavailable` 的拒绝门。** 网络 gap 会让
   `PrepareShell` 拒绝启动，平台 gap 只上报。理由：`failIfUnavailable: true` +
   `seccomp.enabled: true` 的 macOS 配置**今天能正常跑**，让它突然启动失败是回归，
   而收益只是把一条已经显示出来的警告变成硬失败。这个不一致是有意的，写进了
   `PolicyGap.Network` 的注释。
2. **`allowPty` 的 warning 只在显式设为 true 时触发，且不声称 pty 因此被拒。**
   `AllowPty` 默认 false，按默认值告警会让每个 linux 用户永远看到一条警告 ——
   那正是 P1-35 极力避免的噪音。文案也刻意只说「这个选项在 linux 不被读取」，
   **不说 pty 是否因此不可用**：bwrap 下 pty 可达性由 `--dev /dev` 决定，而这一点
   **没有在真实 Linux 主机上验证过**，所以不写进结论。

### 四、测试（每条都验证「移除修复后失败、加上修复后通过」）

| 测试 | 覆盖 | mutation check（移除修复后的表现） |
| --- | --- | --- |
| `sandbox.TestDescribeReportsSeccompIgnoredOnDarwin` | darwin+seccomp：标签不含 `/seccomp`、含 `degraded:seccomp`；warning 点名选项并说明本平台无解；`Active` 仍为真 | 标签条件还原为无条件 → `label = "on/seccomp/degraded:seccomp"`（**同时暴露两种写法的差别**）；删掉 `platformPolicyGaps` → `label = "on"` |
| `sandbox.TestDescribeKeepsSeccompOnLinux` | **反方向**：linux+seccomp → 标签恰为 `on/seccomp`、零 warning | 若把 `supportsSeccomp` 写反即红 |
| `sandbox.TestDescribeReportsAllowPtyIgnoredOnLinux` | linux+allowPty → `degraded:allowPty` + 点名 `sandbox.allowPty` | 删掉 `platformPolicyGaps` → `label = "on"` |
| `sandbox.TestDescribeStaysQuietAboutAllowPtyWhenNotRequested` | **反方向**：未显式请求 allowPty 时 linux/darwin 都零 warning；darwin 显式请求也零 warning（那里真的生效） | 若按默认值告警即红 |
| `sandbox.TestNetworkPolicyGapsExcludesPlatformGaps` | **`NetworkPolicyGaps` 不含平台 gap**，但真网络 gap 仍在 —— 直接守住 P0-04 的拒绝契约 | 把 `if gap.Network` 改成 `if true` → seccomp 泄漏进网络契约，立刻红 |
| `sandbox.TestDescribeStaysQuietOnHealthyInstall`（改） | 「全配齐」现在按平台取不同集合：linux `on/seccomp/network/sockets`、darwin `on/network/sockets` | 旧写法在两个平台用同一份配置，本身就是这条 bug 的化石 |
| `cli.TestTUISandboxLabelDegradesForPlatformIgnoredOption` | 真实 wiring，按 `runtime.GOOS` 各断言一半（darwin 断 seccomp、linux 断 allowPty） | 同上 |
| `cli.TestTUISandboxLabelUnchangedOnHealthyInstall`（改） | **反方向**：健康配置里**去掉了 seccomp** —— 这个测试原先设了 seccomp 并称之为 healthy，P1-37 证明那在 macOS 上从来不成立 | — |
| `cli.TestTUIWelcomeInfoReflectsRuntimeConfig`（既有，改） | 期望值 → `on/sockets/degraded:seccomp,network.domains`，两条 warning 逐条断言 | **旧期望值 `on/seccomp/network/sockets` 的两半都是谎报**（seccomp + denyDomains） |

### 五、风险与边界

- **Linux 侧仍未经真机验证**（同 P1-35、PARITY-001）。`allowPty` 在 linux 的
  gap、`seccomp` 在 linux 的正常路径，都只有注入式覆盖。darwin 侧
  （`seccomp` 被忽略）是真机跑过的。
- **`cli.TestTUIWelcomeInfoReflectsRuntimeConfig` 现在带 `runtime.GOOS != "darwin"`
  的 skip**。标签里含平台判定，写死一个字符串就必然是平台相关的；skip 而不是分支
  断言，是因为该测试的主体不是沙箱。Linux CI 上这条会跳过 —— 沙箱行为由
  `internal/sandbox` 的注入式测试覆盖。
- **仍未纳入的同类项**：`enableWeakerNestedSandbox` 也只被 linux 路径读取
  （`runtime.go` 的 `--proc` 分支）。它不进标签、也不产 gap。没有一并做是因为它
  影响的是沙箱内部强度而非「用户以为配了什么」，且与本条的 seccomp/allowPty 不同，
  它在 darwin 上不代表任何用户可感知的承诺。**如需收口应另起条目。**

## 修复证据 · AUDIT-P1-32 / P2-10 / P2-05（2026-07-26）

三条的共同主题是「WebUI 真的能用、且显示的是真数据」，一起做只为共用一次全量验证。

### AUDIT-P1-32：`/webui` 不再静默 404

`registerWebUIRoutes` 原来在 `opts.WebUIDir == ""` 时直接 `return`，整个 `/webui` 路由不注册。
用户拿到的是裸 404 —— 而 404 和「这个 build 根本没有 web UI」完全无法区分，没有任何线索指向
「你需要先 build 前端」。

改为无条件注册，构建目录按三级解析：

1. `opts.WebUIDir`（来自 `GOLANG_CLAUDE_CODE_WEBUI_DIR` / `WEBUI_DIR`），**且必须含
   `index.html`**；
2. 自动发现 CWD 与可执行文件旁的 `web/dist` / `dist` —— 于是 `npm --prefix web run build`
   之后不设任何环境变量也能用；
3. 都没有时返回 **503 + 内嵌的构建说明页**（`internal/server/webui_unavailable.html`），页面给出
   确切命令（含 `--legacy-peer-deps` 的原因）、`WEBUI_DIR` 用法，并指向此刻就能用的
   `/trace` 与 `/prompt-dump`。

顺带修掉第二个静默 404：`WEBUI_DIR` 指向一个存在但没有 `index.html` 的目录时，旧代码会对每个
资源 `http.ServeFile` 一个不存在的 `index.html`，同样是 404。现在落到说明页。

`webUIAPIPrefixHandler` 也改为跟随**解析后**的目录 —— 前端所有 fetch 都走 `/api` 前缀，若仍以
原始 option 为准，自动发现出来的 `web/dist` 会渲染出一个所有 API 调用都 404 的 UI。

**为什么不 `go:embed web/dist`**（对照 `/trace`、`/prompt-dump` 确实是 embed 的）——三个独立
阻断，任一条都足以否决：

1. `go:embed` 的模式**不能跨出包目录**，`..` 是非法的，`internal/server` 物理上够不到
   `web/dist`；
2. embed 一个**不存在**的目录是**编译错误**，而 `web/dist` 被 gitignore —— 默认 clone 会直接
   编译失败；
3. 绕过 (2) 就意味着把压缩产物提交进仓库：每次前端改动都产生二进制 churn，review 和 bisect
   都变差。

`/trace` 与 `/prompt-dump` 的类比不成立：它们 embed 的是**单个手写 HTML 源文件**，不是 build
产物。真正的缺陷是「静默 404」，说明页以接近零的成本精确修掉它。

新增 5 个测试（`internal/server/webui_test.go`），把修复移除后全部失败：

| 测试 | 断言 |
| --- | --- |
| `TestWebUIWithoutDirServesActionableSetupPage` | 不配置目录时非 404、503、正文含 `npm` / `run build` / 环境变量名 |
| `TestWebUIStaleDirServesSetupPage` | 配置了但缺 `index.html` 的目录同样落说明页 |
| `TestWebUISetupPageRequiresAuth` | 说明页仍在 auth 门后，不是未鉴权信息泄露 |
| `TestWebUIAutoDiscoversBuiltDist` | 只 build 不设 env 也能拿到页面 |
| `TestWebUIExplicitDirWinsOverDiscovery` | 显式目录优先于自动发现 |

路由无条件注册后 `TestEveryRegisteredRouteIsInSwagger` 立刻变红（正如它该做的）。补了
`/webui`、`/webui/{path}` 注解并重新生成 `docs/swagger.json` 与
`web/src/lib/generated/api-types.ts`。同时修掉该测试自身的一个盲区：`swaggerPathForRoute` 只把
gin 的 `:id` 转成 `{id}`，不认 `*wildcard` —— 此前唯一的 wildcard 路由 `/swagger/*any` 在跳过
名单里，所以这个 bug 一直没被触发，任何被正确注解的 catch-all 路由都会被误报为缺失。

### AUDIT-P2-10：Files tab 显示真字段

这是 TODO-049 标 DONE 但实际未做的 P0-4 子项。旧前端读
`payload.file ?? payload.path ?? payload.filename`，并把 `insertions`/`deletions`/`line_delta`
取绝对值相加 —— **这四个 key 没有任何 Go writer 会写**（`changed_files` 同样全仓零 writer），
所以 Files tab 对 web agent run 恒为空。

关键发现是不需要动 `internal/tools` 或 `internal/agentruntime`：`agentTaskTextSink.OnToolResult`
拿到的 `query.ToolTrace` **已经**带着填好的 `FileChanges`（`internal/query/query.go:3096`），
只是被丢掉了。因此复用既有 `tools.FileChange` 协议（AUDIT-P0-54/55 那批），而不是另造一套：

- **写侧**来自 `trace.FileChanges`，逐条派生 `path` / `access:"edited"` /
  `change:"created"|"modified"|"deleted"` / `object` / `tool_name` / `source`（取
  `FileChange.Source` 记录的 edit/write/bash 边界）；
- **读侧**没有 `FileChange`，改从读类工具（`Read` / `NotebookRead`）**真实 input 里的
  `file_path`** 取路径 —— 那是工具实际收到的参数，不是猜的 payload key。errored 调用整条跳过：
  失败的读什么都没读到；
- `line_delta` / `before_lines` / `after_lines` 只在两侧内容都 inline 可用时给出，**实测**而非
  估计；内容被外化到 snapshot store（或 after-scan 放弃的 placeholder）时发
  `content_available:false` 并**省略** `line_delta`，因为把缺失的测量报成 0 就是把谎言包装成
  数据。目录与符号链接同理不报行数。

streaming sink 与非 streaming 的 `appendAgentTaskToolEvents` 两条路径都发，否则没走 streaming
的 run 恰好是 Files tab 空的那批。`file_change` 事件也**不计入** command 计数：一条改了三个文件
的 Bash 命令是一条命令，不是四条。

前端 `summarizeActivity` 删掉全部猜测逻辑，改为只消费 `file_change`，Files tab 按 Edited / Read
分组。同一路径只列一行：重复编辑折叠到第一行并保留最初的分类（本轮新建的文件仍显示为新建），
但实测 delta 继续累加；先读后写的路径只算 edited。

测试：Go 侧 9 个（含跨 HTTP 边界的
`TestAgentTaskFileChangeEventsReachTheEventsEndpoint`，直接打 `GET /tenant/agent-tasks/7/events`
并断言 payload 的 key 集合、以及 legacy key **不**存在），移除 emission 后 4 个失败；前端 9 个
（`web/src/components/WebAgentPage.files.test.ts`），把旧猜测逻辑放回后 7 个失败。两侧断言同一
组 key，任一侧改名都会红。

### AUDIT-P2-05：前端质量基建（PARTIAL）

- **Linter**：选 Biome 2.5 而非 ESLint。硬约束是 typescript-eslint 的 peerDeps 上限低于本仓 pin
  的 `typescript@6.0.3`，叠在既有的 `openapi-typescript` peer 冲突上会让 `npm ci` 更脆；Biome
  是 1 个 devDep 且不声明 TypeScript peer。**只开 lint 不开 format**：实测无论 `lineWidth` 取
  180/240/320，Biome 默认排版与现有代码都差 78 个文件，格式化会淹没本次真实改动。formatter 配置
  留在 `biome.json` 里但 `enabled:false`，将来要做一次性 sweep 时改一个字段即可。
- **规则分级**：会 fail CI 的是死代码族（`noUnusedImports` / `noUnusedVariables` /
  `noUnusedFunctionParameters`）与 `useIterableCallbackReturn`；存量的 a11y 三族、
  `noArrayIndexKey`、`noAssignInExpressions`、`useExhaustiveDependencies` 降为 warning —— 把它们
  提为 error 需要改写大量与本次无关的 render 代码才能落地一个 linter。存量清理记为 TODO-064
  的剩余项。清理了 11 个存量 error（都是未用 import / 变量）。
  - 一个教训写在这里：`biome lint --write --unsafe` 不受规则分级约束，会顺手重写 React 依赖
    数组（把 10 个依赖换成 `[refreshAll]`、把 `[effectiveTraceId]` 清空）并从
    `prefers-reduced-motion` 块里删掉 `!important` —— 都是真实行为回归。`lint:fix` script 因此
    用 `--only=` 把可写范围钉死在那三条死代码规则上。
- **Typecheck**：新增 `typecheck` script（`tsc -b --force`），把 `vite.config.ts`、
  `playwright.config.ts` 和整个 `e2e/` 纳入检查。此前 `build` 里只有
  `tsc -p tsconfig.app.json`，而该 config `include:["src"]` —— 这些文件永不被类型检查。验证方式是
  往 `e2e/` 塞一个 `const n: number = "…"`：新 `typecheck` exit 2，旧的
  `tsc -p tsconfig.app.json` 对同一份坏文件 **exit 0**，正是这条 gap。
- **`visual-regression.spec.ts` 名不副实**：它只 `page.screenshot()` 写临时 PNG，从不
  `toHaveScreenshot()`，无 baseline 目录，永远发现不了视觉回归 —— 一个不存在的安全网。改名
  `navigation-smoke.spec.ts`，文件头写明它实际断言什么（每个 section 可达、heading 渲染、
  dashboard shell 挂载不抛错），截图降级为失败排查产物。**没有**改成真 visual regression：
  baseline 是渲染环境相关的，macOS 生成的 PNG 与 ubuntu-latest 的字体栅格化不匹配，需要固定容器
  才能既生成又校验，单列为下一步而不是在这里造假。
- **死依赖**：`@assistant-ui/react`、`@testing-library/react`、`@testing-library/jest-dom` 全部
  零 import 且无 `setupFiles`，已删（连带 101 个传递依赖）。现有测试用裸
  `createRoot` + `act`，删除比补 `setupFiles` 更符合既有风格。
- **CI**：`web` job 从「web build」扩展为 lint / typecheck / build / test 四步。

**仍 TODO**：coverage、router / 深链接与前进后退、真 visual baseline、warning 族存量清理。

### 验证

```
go test ./internal/sandbox ./internal/cli ./internal/tui ./internal/tools -count=1   # 全绿
go test ./... -count=1                                                              # 全绿，0 FAIL
go vet ./...                                                                        # 干净
gofmt -l .                                                                          # 空
git diff --check                                                                    # 干净

go test ./internal/server ./internal/agenttasks -count=1                # ok
go test ./... -count=1                                                  # exit 0，73 个包全绿
go vet ./...                                                            # 干净
gofmt -l .                                                              # 空
git diff --check                                                        # 干净
npm --prefix web run lint                                               # exit 0
npm --prefix web run typecheck                                          # exit 0
npm --prefix web run build                                              # ok
npm --prefix web test                                                   # 13 files / 127 tests 全绿
```

负向验证（证明门禁真的会拦）：

```
往 src/ 注入未用 import + 未用变量        → npm run lint      exit 1
往 e2e/ 注入未用 import                   → npm run lint      exit 1
往 e2e/ 注入类型错误                      → npm run typecheck exit 2（旧 app-only tsc exit 0）
往 playwright.config.ts 注入类型错误      → npm run typecheck exit 2
移除 /webui 无条件注册                    → 4 个 webui 测试失败
移除 file_change emission                 → 4 个 Go 测试失败
恢复前端旧猜测逻辑                        → 7 个前端测试失败
```

## 修复证据 · AUDIT-P1-08 / P1-18 / P1-19 / P1-20 / P2-05（2026-07-26，五条 PARTIAL 的收尾）

这一批只处理前几批留下的尾巴。每条都给出「做了什么 / 没做什么 / 为什么」，
并把状态推到 DONE 或保持 PARTIAL 且把剩余描述换成当下的真实情况。

### 一、AUDIT-P1-08 · provider 熔断冷却（PARTIAL → DONE）

上一批把 OpenAI 兼容路径的建流重试做完了，明确留下的是审计原文那句
「无熔断冷却，fallback 失败后下一轮仍从 primary 重试」。

**做了**：[`internal/anthropic/cooldown.go`](../../internal/anthropic/cooldown.go) 的
`providerBreaker` 挂在 `Client` 上，可 fallback 的失败让该 provider 进入 60s 冷却，
下一轮直接跳过；成功即清零。

- **上一批说做不了的理由不成立，这里推翻它并说明**：当时记的是「`Client` 生命周期不统一，
  进程内状态未必跨轮存活」。实测 6 个 `anthropic.NewClient` 调用点里，
  [`internal/cli/cli.go:609`](../../internal/cli/cli.go) 那个是**会话级长命对象**，
  一个会话建一次、跨轮共享、并传给 `query` / `task` / `agentruntime` —— 正好就是需要失败
  记忆的作用域。剩下 5 个（3 个 recap、2 个 agenteval）是一次性或几次调用就丢的短命对象，
  在那里"没有记忆"不是缺陷而是无从记忆。`internal/server/` 全仓零 `NewClient`，
  它拿的是传进去的那个。所以状态放 `Client` 上是对的，包级全局只会污染测试。
- **必须加锁**：同一个 `Client` 会被 batch 子代理并发使用（Task 默认 4 路、最多 16 路），
  所以 `providerBreaker` 带互斥锁，且 `providerCooldown` 做成变量供测试压到毫秒级
  （与 `providerTimeouts`、`openAIStreamCreateBackoff` 同一个理由；置 0 即关闭）。
- **两个方向的安全阀**：
  - 只有**明确的 4xx 与调用方取消不进冷却** —— 它们不是 provider 健康度的信号，而且这两种
    在 `StreamMessages` 里本来就直接返回，重试也是同样结果。复用既有的
    `canFallbackAfterError` 做分类，没有放松那个分类器（`internal/compact/overflow.go`
    与 `internal/query/query.go` 都依赖它对确定性 400 的判断）。
  - **全员冷却时忽略全部冷却**，照常从队首试。否则一次短暂的全域抖动会变成"一个都不试就
    报错"，把抖动放大成整段会话不可用。只有一个 provider 时同理。
- **冷却跳过写进聚合错误文案**（`skipped: cooling down for another 59s after a recent failure`）。
  静默跳过读起来像"只试了 fallback"，用户不知道 primary 为什么没试。

**没做 / 已知边界**：跨进程不共享（两个 go-claude 进程各自记忆）；冷却时长写死 60s，
没加 env 旋钮 —— 目前没有需要调它的场景，`providerCooldown` 变量已经够改。

### 二、AUDIT-P1-18 · MCP 图片真正送达模型（PARTIAL → DONE，TODO-061 闭合）

上一批的八项协议缺口都已闭合，明确不做的三项（旧版 SSE 传输、OAuth、
`tools/list_changed` 热替换）**理由未变，本批不推翻**。真正的剩余是 TODO-061：
`renderToolContent` 只做到"不再静默丢弃、留占位行"，模型仍然看不到图。

**做了**：[`internal/mcp/media.go`](../../internal/mcp/media.go)。

- **关键发现：不需要动 `internal/query/query.go`（0 行）**。`tools.Result.ContextMessages`
  这条通路已经存在，且 `query.go:1941-1951` 与 `agentruntime/runtime.go:739-745` 都已经把它
  接到消息序列上（`internal/tools/skill` 就是既有用例）。上一批记的"缺的是中间管道"
  其实只缺**最后一段**：把 MCP 的 image 块变成 `anthropic.ContentBlock{Type:"image"}`。
  所以本条的改动全部落在 `internal/mcp` 里，加 `internal/anthropic` 的 4 行。
- `renderToolCallContent` 把 content 块拆成「文本 + 可投递图片」两半，
  `renderToolContent` 退化成取 `.Text`，`CallToolWithCallback` 签名不变 ——
  `internal/cli` 的 `mcp call` 子命令没有模型在场，只要文本，不需要跟着改。
- 占位行按结果分化：投递成功写 `attached below`，投递不了写 `omitted: … ; <原因>`。
  "看不到"必须连"为什么看不到"一起说，否则又回到上一批要修的那种含糊。
- **三道上限，因为图片绕过了字符截断预算**：`tools.Result.ContextMessages` 不经
  `toolresult.Process`（那一层只数 `tool_result` 块里的字符串），所以截断保护必须自己做：
  单图 5MB 原始字节的 base64 等值、单次结果最多 4 张、总 base64 10MB。超限的退回占位行，
  **不静默丢**。
- **只白名单 `image/png|jpeg|gif|webp`**。别的（`image/svg+xml` 等）Anthropic 的 Messages API
  不收，硬塞换来一个 400 —— 那比占位行更糟，因为整轮请求都废了。
- **顺手修 OpenAI 路径的一处真丢弃**：`openAIChatCompletionMessages` 的 `case "image"` 在
  `Source` 为空时整块消失（Anthropic 路径同样情形会回落到 `block.Text`）。现在两边对齐，
  且每个内联图片块都带一行 `Text` 降级说明。

**没做，理由**：

- **音频不投递**。Anthropic 的 Messages API 没有 audio 内容块，本仓 `ContentBlock` 也没有。
  这不是管道缺失而是 API 不支持，所以音频永远留占位行 —— 记在这里而不是记成 TODO。
- **图片不写进会话日志**。`recordMessage` 只记 `text` / `tool_result` 块，所以图片是
  "本轮可见、resume 后消失"（解释性的文本块会留下，降级是平滑的）。**这是刻意的**：
  把 MB 级 base64 写进 session transcript 会让日志按张膨胀。要 resume 保真需要先决定图片
  的落盘形式（外链文件而不是内联 base64），已登记为 **TODO-080**。
  **2026-07-26 已闭合（TODO-080）**：落盘形式选了外链侧车文件 ——
  `<transcript 目录>/<sessionID>/media/<sha256>.b64`，transcript 里只留一条 `MediaRef`
  （见 `internal/session/media.go`）。此处「先决定落盘形式」的谨慎是对的，而且理由比
  当时写的更硬：内联 base64 除了让日志膨胀，还会让一条未登记的 `image` 行把整个 v1
  transcript 判成 `Mixed`，`ValidateResumeFormat` 会**直接拒绝 resume**，
  即「图片丢了」升级成「会话打不开了」。所以 `image` 同时登记进了
  `isGoClaudeV1EntryType`。上面「依赖每个图片块自带的 `Text` 降级说明」这一点在
  resume 侧也补齐了：侧车读不回来时降级文本会被改写成"不再可用"，
  而不是留着记录时那句 "attached below"。
- **没有 provider 能力门禁**。全仓没有"这个 model 支不支持图片"的判断可用
  （`config.MultimodalSettings` 只在 server 路径按用户附件改路由，工具层拿不到）。
  依赖的是每个图片块自带的 `Text` 降级说明。

### 三、AUDIT-P1-19 · `Task` 顶层 `required[]`（PARTIAL → DONE）

覆盖率四条与 `MaxResultSizeChars` 上一批已闭合，剩下的是被明确推给"单独决策"的那条。

**决策：不用根级 `anyOf`，在代码里校验。**

- 上一批的判断（根级 `anyOf` 在各 provider 的 tool schema 校验里兼容性不一）成立，本批不推翻。
- 但那条缺陷的**本质是"畸形输入被静默接受"**，不是"schema 不够花哨"：`{}` 和
  `{"description":"x"}` 都会被接受，然后拿一个空 prompt 去起子代理；
  `{"description","prompt","tasks"}` 三个都给时顶层那两个被静默忽略。
  `validateTaskForm` 直接把这两种挡掉，并给模型一句能照着改的错误
  （"either description and prompt (single task) or a non-empty tasks array (batch)"）。
- **有先例**：`internal/tools/agent/agent.go` 的 `CompatTool` 就是 schema 声明 required +
  代码校验各做一半。
- 顺带把 schema 文案改成能读出"二选一"（`description`/`prompt` 标注 "Required in
  single-task form; ignored when tasks[] is present"），以及修一处小的不诚实：
  `max_concurrency` 写着 `maximum: 16`，而 `GOLANG_CLAUDE_CODE_MAX_BATCH_CONCURRENCY`
  能把实际上限抬高，描述里现在写明了。

### 四、AUDIT-P1-20 · 剩余两项（PARTIAL → DONE）

**① batch 子代理写同一棵工作树无锁**

先把事实核清楚：`internal/tools/task/task.go` 对 `agentworktree` **零引用**，
batch 的每个 worker 都用同一个 `CWD: toolContext.CWD`。只有 `Agent` 工具
（`agent.go` 的 `CompatTool`，`isolation:"worktree"`）会开工作树，而它默认不注册。
所以这条其实是两个不同的缺陷叠在一起，分开处理：

- **真正的并发 bug 在 `internal/agentworktree`**，而且比"无锁"更糟：`NewSlug` 只用
  `time.Now().UnixNano()`，macOS 的时钟粒度下并发调用会拿到**同一个 slug**，而
  `CreateWithHooks` 对已存在的工作树是**复用**而不是报错 —— 于是两个子代理无声地共用
  一棵树互相踩文件。修法是 [`lock.go`](../../internal/agentworktree/lock.go)：
  slug 加进程级原子计数器后缀（保证唯一），外加**每个 gitRoot 一把互斥锁**包住
  `Create` 的 read-modify-write 整段与 `Remove`（关掉 `rev-parse` 检查与
  `worktree add` 之间的 TOCTOU，也不再和 git 自己的 index.lock 抢）。
  锁的范围刻意只覆盖 git 元数据写入这几秒，不覆盖子代理的实际工作 —— batch 并发本身
  是调用方真实想要的。只管进程内：同一仓库上跑两个 go-claude 进程仍会撞 git 的锁，
  那种情况下 git 的报错就是正确答案。
- **Task batch 侧不加锁，而是不再说谎**。给 N 个子代理共用的一棵树加锁没有正确的粒度：
  粗锁把 batch 串成串行（等于取消这个功能），细锁需要知道每个子代理要碰哪些文件（做不到）。
  真正的缺陷是 schema 把 `tasks` 描述成 "isolated sub-agent tasks"，工具描述里也写着
  "multiple isolated sub-agent tasks" —— 模型照着这个词就会把 8 个写同一批文件的任务扇出去。
  两处文案现在都写明「各自独立会话，但共用同一棵工作树且无文件锁，不要把写同一批文件的
  任务放进同一个 batch」。**给 batch 加 per-item 工作树隔离是新功能**（要设计清理生命周期、
  结果合并、prompt 提示），不是这条尾巴，登记为 **TODO-081**。

**② 单 Task 无重试而 batch 有**

`runSingle` 现在按 `retry_attempts` / `retry_backoff_ms` 重试，语义与 batch **完全一致**：
共用同一个 `worthRetrying`（取消不重试、deadline 重试）、同一套退避、同一个 0..10 clamp。
batch 侧原来那段内联判断替换成调用 `worthRetrying`，两条路径因此不可能再漂移。
schema 的 `retry_attempts` 文案从"for each batch task"改成覆盖两种形式。

**auto-background 路径刻意不重试**：一次尝试可能已经翻成后台任务并返回了句柄，
再起一次就是两个子代理跑同一件事。该路径靠 env 显式开启、默认关闭，代码里写了注释。

### 五、AUDIT-P2-05 · 前端存量 lint 清理（仍 PARTIAL）

见下方 §六的实测数字。做的是能机械化且确实是修正的那些并把对应规则提为 `error`；
明确不做两族并说明。

**做了**（全部手改，**没有**用 `biome lint --write --unsafe`）：
`a11y/useButtonType`（47 处补 `type="button"`，默认 `submit` 是真行为缺陷）、
`suspicious/noArrayIndexKey`（9 处，见下方注记）、`suspicious/noAssignInExpressions`（2 处）、
`a11y/useAriaPropsSupportedByRole`（12 处）、`a11y/useFocusableInteractive` +
`useKeyWithClickEvents`（7 处补 `tabIndex` 与 Enter/Space 键盘路径）、
`a11y/useSemanticElements`（6 处，其中 resizer 与 CSS-grid 表格保留 role 并写
`biome-ignore` 说明理由）、`useOptionalChain` / `noEmptyBlock` / `noUselessFragments` /
`noExtraBooleanCast`。清完的规则全部提为 `error`，不然清了也会回来。

**`noArrayIndexKey` 这 9 处值得单独说清楚，因为第一版做法被我否掉了**：

- 只有 **3 处真的有稳定身份可用**，改成了它：`ChatLab` 的附件 chip 用
  `attachment_id || sha256 || type-name/url`；`QuotaPanel` 的单元格用该列的表头文本；
  `SettingsPanel` 的 JSON 行号沟槽用行号本身（行号就是那一行的身份）。
- 余下 **6 处（markdown 渲染的列表项 / 表头 / 表行 / 表格单元 / 软换行，以及思考态的状态 chip）
  没有任何稳定身份可用**：这些内容每次变更都从原始字符串**重新解析**出来，子元素无状态，
  位置本身就是身份 —— React 的 index-key 隐患针对的是「重排/插入 + 有状态子元素」，这里两条都不成立。
  所以这 6 处**保留位置 key**，配 `biome-ignore` 写明上述理由。顺带把原来裸 `key={index}` 的
  表格 key 加上 block 前缀（跨 block 不再可能撞）。
- **被否掉的第一版**把 key 计算提前到一个额外的 `.map` 里
  （`.map((item, i) => ({item, itemKey: `${block.key}-${i}`})).map(({item, itemKey}) => …)`），
  这样 Biome 的静态分析看不见 index。**key 一模一样还是位置 key**，只是多了一次数组分配、
  代码更难读 —— 那不是修复，是骗 linter。已全部改回并换成带理由的 `biome-ignore`。

**明确不做**：

- **`correctness/useExhaustiveDependencies`（104 处）**保持 `warn`。`--unsafe` 会把 10 个依赖
  换成 `[refreshAll]`、把 `[effectiveTraceId]` 清空 —— 上一批已经踩过并记进 CHANGELOG。
  手改 104 个依赖数组会改动全应用的 effect/memo 时序，而现有测试覆盖不到这些时序。
  登记为 **TODO-082**。
- **`style/noDescendingSpecificity`（21 处 CSS）**保持 `warn`。修它要重排层叠顺序，
  而本仓**还没有真的 visual regression baseline**（AUDIT-P2-05 自己记着这一条），
  改完没有任何东西能发现视觉回归。登记为 **TODO-083**，依赖 baseline 先落地。
- **`complexity/noImportantStyles`（5 处）**改为 `off`。其中 4 处是
  `@media (prefers-reduced-motion: reduce)` 的覆盖，`!important` 在那里是**必须**的
  （要赢过组件自己的 transition）；删掉它就是 a11y 回归。一个规则如果对本仓的正确写法
  永远报警，留着只是噪音。CSS 一行没动。

**P2-05 仍 PARTIAL 的剩余**：coverage、router / 深链接与前进后退、真 visual baseline
（这三条是 TODO-064 的既有剩余项，本批未动），加上 TODO-082 / TODO-083 两族存量 warning。

### 六、验证

**Go 侧**

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/anthropic ./internal/mcp ./internal/tools/task ./internal/agentworktree -count=1` | 4/4 ok |
| `go test ./internal/anthropic ./internal/mcp ./internal/tools/task ./internal/agentworktree -count=1 -race` | 4/4 ok，无 race |
| `go test ./... -count=1` | exit 0，74 个包 ok / 8 个无测试文件 / 0 FAIL |
| `go vet ./...` / `gofmt -l .` / `git diff --check` | 均无输出 |

**前端**（node 22.23.1，`npm ci --legacy-peer-deps`）

| 命令 | 结果 |
| --- | --- |
| `npm --prefix web run lint` | exit 0；warnings 216 → **125** |
| `npm --prefix web run typecheck` | exit 0 |
| `npm --prefix web run build` | ok |
| `npm --prefix web test` | 全绿 |

剩下的 125 条**正好**是刻意保留的两族：`useExhaustiveDependencies` 104 + `noDescendingSpecificity` 21。
被清空的规则全部已提为 `error`，所以它们不会悄悄回来。

**每条新增测试的 A/B（移除修复 → 实测转红）**

| 测试 | 移除哪一处修复后转红 | 实测红态 |
| --- | --- | --- |
| `TestFailedProviderIsSkippedOnTheNextTurn` | `StreamMessages` 里的 `cooling := c.breaker.cooling(providers)` 改成恒 nil | FAIL：`primary attempts grew from 3 to 6` |
| `TestCooldownSkipIsReportedInTheAggregatedFailure` | 同上 | FAIL：`primary attempts grew from 3 to 6` |
| `TestToolAdapterDeliversImageContentToTheModel` | `ToolAdapter.Run` 里的 `result.ContextMessages = mediaContextMessage(...)` | FAIL：`ContextMessages = 0, want 1` |
| `TestSingleTaskRetriesLikeBatch` | `runSingle` 的 `totalAttempts` 改回恒 1 | FAIL：`streamer called 1 times, want 3` |
| `TestTaskRejectsInputThatIsNeitherFormCompletely`（4 个子测试） | 去掉 `validateTaskForm` 调用 | 4/4 FAIL |
| `TestTaskRejectsBothFormsAtOnce` | 同上 | FAIL |
| `TestTaskDescribesThatBatchTasksShareOneWorkingTree` | 工具描述改回 "multiple isolated sub-agent tasks" | FAIL：`description still calls batch tasks isolated` |
| `TestConcurrentCreateWithTheSameSlugReusesOneWorktree` | 去掉两处 `lockGitRoot` | **8/8 次运行全红**：`git worktree add … exit status 128 / 255` |
| `TestConcurrentCreateOnTheSameGitRootAllSucceed` | `newSlugSuffix` 改回只用 `time.Now().UnixNano()` | 6 次运行里 1 次红：`workers 0 and 2 share worktree …`（**概率性检测器，如实标注**） |

**方法学如实标注三处**

- `TestConcurrentCreateOnTheSameGitRootAllSucceed` 是**概率性**的：修复前 6 次运行里 1–2 次转红
  （取决于时钟粒度与调度），修复后 `-count=15 -race` 连续 15 次全绿。同一族的
  `TestConcurrentCreateWithTheSameSlugReusesOneWorktree` 是确定性的（8/8），两条一起才覆盖住这条缺陷。
- **只移除 gitRoot 互斥锁、保留 slug 计数器时，`...OnTheSameGitRootAllSucceed` 10 次全绿** ——
  也就是说这条测试检测的是 slug 碰撞，不是 git index.lock 争用。锁的价值由那条同 slug 的测试覆盖。
  最初观察到的一次 `exit status 128` 事后核实也是 slug 碰撞导致的，不是索引锁争用。
- 下列是**守卫而非回归探测器**（修复前后都绿，作用是防后续改动走偏），如实标注：
  `TestSuccessfulProviderIsNotCooledDown`、`TestSoleProviderIsStillTriedWhileCoolingDown`、
  `TestAllProvidersCoolingDownAreStillTried`、`TestCallerCancellationDoesNotCoolDownTheProvider`、
  `TestProviderCooldownDisabledByZeroDuration`、`TestProviderBreakerIsConcurrencySafe`、
  `TestSingleTaskWithoutRetryAttemptsIsTriedOnce`、`TestSingleTaskDoesNotRetryAfterCancellation`、
  `TestNoMediaMeansNoContextMessage`、`TestInlinedImageBlockCarriesAPlaceholderText`、
  `TestOpenAIPathCarriesImageBlocks`。
  其中 `TestUndeliverableImageTypeStaysAPlaceholder` / `TestAudioContentStaysAPlaceholder` /
  `TestOversizedImageIsNotInlined` / `TestOnlyTheFirstFewImagesAreInlined` /
  `TestImageReturnedAsAnEmbeddedResourceIsInlined` / `TestErrorResultCarriesNoImages` /
  `TestOpenAIPathFallsBackToTextWhenAnImageHasNoSource` 覆盖的是本批**新增**的行为分支，
  没有"修复前"可言，钉的是每条上限与降级路径。

**未改动的边界**：`internal/query/query.go` 0 行；`internal/server/`、`internal/storage/`、
`internal/telemetry/`、`internal/memory/`、`migrations/`、`README`、`docs/usage/`、发布脚本
均未触碰。`internal/cli` 只读未改（`CallToolWithCallback` 签名刻意保持不变，`mcp call` 子命令
不需要跟着改）。

---

## 修复证据 · AUDIT-P1-21 / P1-22 / P1-27（2026-07-26）

主题是「server 对外暴露前必须成立的安全与可运维前提」。三条各自独立，但共享同一个
判断：**默认配置绑到公网时不能是安全的，只能是启动不了的。**

### P1-21 · 认证不再 fail-open，`?token=` 不再能换到对话

四处改动，按危险程度排序：

1. **空 token + 非回环绑定 = 拒绝启动**（`internal/server/security.go` 的
   `validateServerBind`，在 `Run` 里排在所有副作用之前）。此前 `authorize` 对空
   token 直接 `return true`，而部署文档第 4 节明确教用户 `--host 0.0.0.0` ——
   一个忘了 `--auth-token` 的远程部署会把 `/trace`（完整会话）和 `/prompt-dump`
   （完整 prompt 正文）匿名暴露给整个网络。**刻意没有环境变量逃生口**：逃生口存在，
   这个组合就仍然可能出现，而它正是本条要消灭的东西。非 IP 主机名一律按「远端可达」
   处理，不做 DNS 解析 —— 宁可多要一个 token，也不让一次解析失败把判断翻成「安全」。
2. **`?token=` 收窄到 HTML 外壳**。`/trace/api/sessions*` 与
   `/prompt-dump/api/records` 改走只认 `Authorization` 头的 `authorize`；
   `/trace`、`/prompt-dump` 这两个不含任何会话内容的静态外壳保留 `?token=`
   （浏览器打开链接时没有别的地方能塞 header），改名为 `authorizeViewerShell`
   以免下一个人再把它接到吐数据的端点上。`prompt_dump_viewer.html` 相应改为发
   `Authorization` 头。
3. **`/prompt-dump` 加一道本机门槛**。它落的是完整 prompt 正文（system prompt、
   全部历史消息、工具结果），泄漏面比 `/trace` 还大，而它本身就是本机调试视图。
   现在只服务本机直连请求；**带 `X-Forwarded-For` / `X-Real-Ip` / `Forwarded` 的
   请求一律拒绝** —— 否则同机部署的 Nginx 会让 `RemoteAddr` 恰好是回环，整道门被
   代理链穿透。需要远程看就开 SSH 隧道。
4. **常量时间比较**。全部 token 比较收敛到 `secureTokenEqual`
   （`subtle.ConstantTimeCompare`），覆盖 header、WebUI cookie 和外壳 query。

**WebUI 的 `?token=` → Cookie 流程刻意保留**：浏览器加载 SPA bundle 时没有地方能塞
header，而这条路径换到的 Cookie `Path=/webui/`，浏览器不会带到 `/api/*`，且
`authorize` 根本不读 Cookie。测试 `TestWebUICookieCannotReachConversationEndpoints`
把这个边界钉死。

**未纳入**：token 过期、轮转、按租户区分、吊销。这些需要一套凭证模型（签发、存储、
撤销列表），不是本条能顺手做完的；单一共享静态 token 的局限仍然存在，应另起条目。

### P1-22 · liveness / readiness 分离，probe 不再需要 token

| 端点 | 鉴权 | 探依赖 | 语义 |
| --- | --- | --- | --- |
| `/livez` | 无 | 否 | 进程还在响应 HTTP 就 200 |
| `/readyz` | 无 | 是 | 任一依赖不可用 → 503 |
| `/health` | 需要 token | 否 | 运维自查，**行为一字未改**（仍返回 workspace） |

`/livez` 与 `/readyz` 敢不鉴权，是因为它们只回答是/否：`/readyz` 的 `checks` 只有
`ok` / `unavailable` 两种值，底层错误（DSN、地址、连接错误原文）只进服务端日志。
`TestReadinessFailsWhileLivenessStaysUpWhenDependencyIsDown` 里有一条专门断言
响应体不含 `10.0.0.5` 和 `connection refused`。

依赖探活通过 `Options.ReadinessProbes` 注入，`cmd_server.go` 装配 MySQL
（`ListTenants(ctx, 1)`，而不是纯 Ping —— 连接池活着但库被删/权限被收也必须算 not
ready）、quota Redis、mobile Redis。单个探活 2s 上限，必须比 k8s 的 probe timeout
先返回。

**Redis 启动探活**：`quota.RedisStore.Ping` 与 `server.RedisMobileUsageStore.Ping`
新增，`cmd_server.go` 在装配后立即 ping，失败即启动失败。go-redis 的 `NewClient`
是懒连接，配错地址照样构造成功，此前要等到第一条真实请求才暴露 —— 而那时配额路径
会走 fail-open/fail-closed 分支，两种都不是运维想要的结果。MySQL 侧本来就在
`OpenGormRepository` 里 Ping 过，这次把 Redis 拉齐。

### P1-27 · 配额不再能靠「不带 header」绕过

`reserveQueryQuota` 在 `TenantService == nil || !tenantPersistenceRequested(ctx)`
时不再直接放行，而是落到按客户端的兜底限流（`internal/server/rate_limit.go`）。
它**不是配额**，只是让「没有配额」不等于「没有上限」；带 tenant 上下文的请求仍然走
`ReserveTenantQuota`，完全不经过这里。`/query` 与 `/v1/chat/completions` 共用
`reserveQueryQuota`，所以两条都被覆盖。

两个关键取舍：

- **按 TCP 对端地址计数，不看 `X-Forwarded-For`。** gin 的 `c.ClientIP()` 默认信任
  所有代理，拿它当限流 key 的话，攻击者每次换一个伪造的转发头就能拿回无限额度 ——
  那样这道门等于不存在。代价是真架在反向代理后面时匿名请求会落进同一个桶；这是
  可接受甚至想要的，多用户部署本来就该带 tenant header。
  `TestRateLimitKeyIgnoresForwardedForSpoofing` 锁住这一点。
- **默认 120/min 而不是更小。** 这条路径覆盖的正是本机自用的 `/query` 调用（本机
  不带 tenant header），限太紧会打断仓库主人每天在跑的工作流。
  `GOLANG_CLAUDE_CODE_QUERY_RATE_LIMIT_PER_MINUTE` 可调，负数关闭。

**`cwd` 约束**：请求体的 `cwd` 直接决定服务端工具的执行目录，此前无 allowlist 无
workspace 根约束。现在默认只允许 workspace 子树，`GOLANG_CLAUDE_CODE_SERVER_ALLOWED_CWD_ROOTS`
可显式加根。校验放在 `cmd_server.go` 的 `runServerQuery` 里 —— 那是 `/query`、
`/v1/chat/completions`、agent task、mobile 全部入口唯一的收敛点。**先解符号链接再
比较**，否则 workspace 内一个指向 `/etc` 的软链就能把整棵允许子树撑开
（`TestRequestCWDRejectsSymlinkEscape`）。workspace 为空且没配环境变量时保持原行为，
而不是假装拦住了。

**已知不足**：cwd 被拒时 `/query` 返回 500 而非 400 —— 错误从 `queryFn` 穿回来，
现有 handler 一律映射成 500。安全属性成立（请求确实被拒），状态码不精确，值得另起
一条小项收口。

### 反向验证（移除修复 → 测试必须红）

```text
移除 validateServerBind 调用              → TestServerRunRefusesNonLoopbackBindWithoutAuthToken
                                            4 个子用例全红（"started; it must refuse"）
API 端点改回 authorizeViewerShell         → TestTraceAPIRejectsQueryStringToken 红，
  + 移除 requireLocalClient                 且 200 响应体里是真实的全量会话列表
                                            TestPromptDumpRejectsNonLocalClients 6 个子用例全红
                                            TestPromptDumpAPIRejectsQueryStringToken 红
移除 /livez + /readyz 路由                → TestProbesDoNotRequireAuthToken 红（404）
                                            TestReadinessFailsWhileLivenessStaysUp... 红
移除 reserveQueryQuota 的兜底限流分支     → TestQueryWithoutTenantHeadersIsRateLimited 红（第 6 次仍 200）
                                            TestOpenAIChatWithoutTenantHeadersIsRateLimited 红
                                            TestRateLimitKeyIgnoresForwardedForSpoofing 红
resolveRequestCWD 短路成无约束            → TestRequestCWDOutsideWorkspaceIsRejected 红
                                            TestRequestCWDRejectsSymlinkEscape 红
                                            TestAllowedCWDRootsHonorsEnvOverride 红
```

**反方向断言**（本机 `127.0.0.1` 自用路径不能被打断）：
`TestServerBindAllowsLoopbackWithoutAuthToken`（含 `""`/`localhost`/`::1`/`127.0.0.2`）、
`TestLocalNoTokenWorkflowStillWorks`（不配 token 时 `/health`、`/trace`、
`/trace/api/sessions`、`/prompt-dump` 全部可达）、
`TestTraceUIShellStillAcceptsQueryStringToken`、
`TestLocalWorkflowStaysUnderDefaultRateLimit`（默认额度下连打 120 次全 200）、
`TestRequestCWDInsideWorkspaceIsAccepted`、`TestRateLimiterIsPerClient`。

## 修复证据 · AUDIT-P0-11 / P0-12 / P1-24 / P1-26（2026-07-26）

主题：**server 在真实负载下不自伤**。四条都不是功能缺失，而是「单请求看不出来、
并发一上就互相放大」的成本项，且相互叠加 —— SSE 的常驻 QPS 打在没有上限的连接池
上，telemetry 的同步写又和它们抢同一批连接。

### 一、AUDIT-P1-24 telemetry 同步阻塞（四条里唯一本机自用就在付钱的）

`Emitter.Emit` 顺序遍历 sink，其中 `RecorderSink.Emit` 是一次 MySQL INSERT、
`HTTPSink.Emit` 是一次 2s 超时的同步 HTTP POST。API 中间件每请求发
`api.request.started` + `api.request.finished` 两个事件，于是每请求两次同步写库
加两次同步外呼，外部 APM 抖动原样变成 API 延迟。

新增 `internal/telemetry/async.go` 的 `AsyncSink`：有界队列 + 固定 worker，
`Emit` 只入队。三个设计点值得写下来：

- **ctx 用 `context.WithoutCancel` 摘掉取消但留下值**。请求 ctx 在 handler 返回时
  就被取消，直接带进后台等于保证写库失败；而 `RecorderSink` 又必须靠 ctx 上的
  tenant/user 值解析租户，所以不能换成 `context.Background()`。
- **Close 之后到达的事件同步投递**，不静默丢。
- **队列塞满时丢弃并计数**，而不是回压请求路径 —— 但丢弃经由 `Emit` 的返回值冒泡
  成一条 error 日志，退出时再汇总打印一次，不会静默。

装配点在 `internal/server/telemetry_async.go` 的 `telemetryPump`：Emitter 是每请求
新建的，异步 worker 必须比单个请求活得久，所以在服务启动时构造一次。
**只有 `serveHTTPLifecycle` 会装配它**；`NewHandler` 那条路没有退出钩子，缓冲永远
不会被 flush，静默丢事件比同步写慢更糟，因此那边保持同步。flush 挂在 defer 上，
跑在 shutdown 排空在途请求之后 —— 那些请求还在往缓冲里写。

### 二、AUDIT-P0-12 SSE 每流每秒 8 次查询

原来是固定 250ms ticker，每 tick 打 `ListAgentTaskEventsAfter` + `GetAgentTask`
两条查询，无论有没有新事件。两处改动：

- **空转时指数退避** 250ms → 500 → 1000 → 2000（上限），来了新事件立刻回到 250ms。
  上限同时是「任务收尾被察觉」的最坏延迟，所以没有取更大的值。
- **有事件的那一轮不查状态**。事件还在流说明任务显然还活着，那条查询是白花的。

轮询循环抽成 `agentTaskStream.run` 并把 sleep 做成可注入的 `policy.wait`，测试因此
完全确定、不占真实时间：记录每次要睡多久，累计到预算就当客户端断开。

### 三、AUDIT-P0-11 连接池全默认

`applyPoolConfig` 在第一次用连接**之前**（早于 `PingContext`）设四个参数，
默认 `25 / 10 / 30m / 5m`，四个环境变量可覆盖：
`GOLANG_CLAUDE_CODE_MYSQL_{MAX_OPEN_CONNS,MAX_IDLE_CONNS,CONN_MAX_LIFETIME,CONN_MAX_IDLE_TIME}`。
解析不了的值退回默认而不是拦住启动，实际生效的配置在 `OpenGormRepository` 打一条
日志。**刻意不提供「不限制」选项** —— 无上限正是要修的那个 bug，所以 `<=0` 一律
补成默认值，`MaxIdleConns` 超过 `MaxOpenConns` 时显式压平（`database/sql` 本来也会
静默这么干）。

测试用 `connPool` 接口作缝，断言四个 setter 都被调过、零值不会退化成
`database/sql` 的默认值；另有一条测试拿真的 `*sql.DB` 走一遍，保证那个缝没有和真实
类型脱节。**压测证据仍缺**（见下方「未做的部分」）。

### 四、AUDIT-P1-26 前导通配 LIKE 与两处 N+1

`Search` 加一层 `field:value` 限定符：命中白名单的字段走等值或
`a,b,c` 的 `IN`，落到既有索引（`idx_tenant_audit_trace`、`idx_telemetry_trace`、
`idx_telemetry_session`，**无需迁移**）；剩下的自由文本仍走原来的 LIKE，老用法不变。
列名只可能来自代码里的白名单，不会来自用户输入。写错的数值限定符（`session_id:abc`）
退回自由文本而不是静默返回空结果。

选这个形态而不是给 `ListOptions` 加字段，是因为 `Search` 被 `tenant.Service` 原样
透传，改动可以完全收在 `internal/storage/mysql` 与调用方两侧。副作用是 WebUI 搜索框
白得了一套限定符语法。

`timeline.go` 随之从「每个 trace 一条查询」收敛成一条 `IN`，额度按 trace 数放大以
保持与过去相当（`traceScopedLimit`，仍收在仓储层的 500 上限内）。两处
`ListAgentTaskEvents` 的 N+1 换成新的 `ListAgentTaskEventsForTasks`；该方法的
`limitPerTask` **沿用逐任务查询时的语义**（每个任务多少条），内部换算成总额度并
兜在 5000，这样调用方换过来不会静默丢事件。

三个 trace + 两个任务的 timeline，查询数从 3+3+2=8 降到固定的 2+2+1=5，且都走
等值/IN。

### 五、每条新测试的红/绿验证

全部按「机械移除该处修复 → 复跑确认转红 → 还原」做过 A/B：

```
移除 AsyncSink 的入队（退回同步 Emit）
  → TestAsyncSinkDoesNotBlockCaller            401ms（阈值 50ms）
  → TestAsyncSinkFlushesBufferOnClose          "sink drained before Close"
  → TestAsyncSinkDropsWhenQueueIsFull...       死锁，25s 超时 panic

serveHTTPLifecycle 不装配 pump
  → TestRequestLatencyIsNotChargedForSlow...   请求 606ms（两个事件各 300ms）
  → TestTelemetryBufferIsFlushedOnShutdown     "sink drained before shutdown"

退回固定 250ms + 每轮都查状态
  → TestIdleAgentTaskStreamQueryRateHasUpper...  10s 空转 80 次查询（上界 20）
  → TestAgentTaskStreamBacksOffWhenIdle...       wait[1]=250ms（期望 500ms）
  → TestAgentTaskStreamSkipsStatusQuery...       getCalls=3（期望 0）

退回两处 LIKE OR 拼接
  → TestAuditSearchQualifierUsesEquality         生成的 SQL 仍是 4 列 LIKE OR
  → TestAuditSearchQualifierCollapses...         无 IN 谓词
  → TestTelemetrySessionQualifierUsesEquality    生成的 SQL 仍是 10 列 LIKE OR

timeline 退回逐 trace / 逐 task 查询
  → TestTenantSessionTimelineCollapsesPer...     audit 查询 4 条（期望 2）
  → TestTenantSessionTimelineSendsExactKey...    Search="trace-a"（期望 trace_id: 限定符）
```

连接池那批是新代码，没有可移除的旧路径，所以红/绿证明换成两条等价断言：
零值配置不得退化成 `database/sql` 的默认值（`MaxOpenConns` 必须有界），
以及 `*sql.DB` 必须真的满足 `connPool` 那个缝。

### 六、明确未做的部分

- **AUDIT-P0-11 的压测证据**。本机既无 MySQL 也无 docker，
  `scripts/tenant-mysql-e2e.sh` 与新增的
  `TestMySQLE2EOpenGormRepositoryAppliesPoolConfig`（断言
  `OpenGormRepository` 真的把环境变量里的值设进了池，而不只是
  `applyPoolConfig` 自己被测到）**跑不了**，如实标注。默认值 `25/10/30m/5m` 是
  按常见 MySQL `max_connections=151` 与 `wait_timeout` 取的保守值，不是实测值。
  转 [TODO-071](../todo.md)。
- **AUDIT-P1-26 的事务边界一项**。`persistTenantQuery` 连写 3 张表不在同一事务，
  位置在 `internal/server/server.go`，属另一条并行任务的归属文件，且「补事务」与
  「查询走索引」是彼此独立的议题。转 [TODO-070](../todo.md)，
  **已于同日闭合**，见 [修复证据](#修复证据--audit-p1-26-事务边界2026-07-26)。
- **SSE 没有改成事件驱动**。审计给的是「事件驱动**或**指数退避」，取了后者：
  事件驱动要引入进程内 pub/sub，而 AUDIT-P1-25 已经记着「当前只能单实例跑」——
  在多实例形态定下来之前，进程内订阅会变成又一处要拆的单机状态。

---

## 修复证据 · AUDIT-P2-04 TUI 侧（2026-07-26）

**这是一次纯重构，行为零变化。** 判据不是「测试绿」而是「既有测试一行都不用改就仍然绿」——
`internal/tui` 的 7173 行测试与 `internal/cli/testdata/golden/` 全部 golden **一字未改**。

### 一、为什么按这些面切

先通读 `app.go` 归纳出实际存在的职责块，而不是按行数机械切。同目录已有的
`display_timeline.go` / `view_format.go` 就是这条线的先例。三条划分依据：

1. **沿数据流向切，不沿类型切。** 流事件进来后要经过「写进时间线 → 落进 scrollback →
   渲染活跃块」三段，它们各自有独立的状态机和测试，切成
   `display_segments.go` / `transcript.go` / `live_display.go`。反过来，把所有
   `render*` 函数归到一个 `render.go` 会把这三段的状态机搅在一起。
2. **弹窗各自成文件。** 权限审批、AskUserQuestion、resume picker、rewind picker
   四个弹窗都是「状态 + 按键 + 鼠标 + 渲染」的闭包，彼此不共享逻辑，是最干净的切口。
3. **纯函数与 Model 方法分开。** 工具摘要、capability_loop 解析、markdown 表格这些
   零 `Model` 依赖的纯函数占了近 1900 行，它们是「数据处理」不是「界面」，单独成文件后
   剩下的 `Model` 方法才看得出结构。

### 二、拆分结果

`app.go` **9099 → 364 行**，只留 Bubble Tea 骨架：`Model` 状态、内部消息类型、
`NewModel` / `Run` / `Init` / `View`。`Update` 单独成文件（它是事件分派入口，
和骨架是两件事）。

| 新文件 | 行数 | 职责 |
| --- | --- | --- |
| `types.go` | 237 | 对外暴露的数据结构与回调签名（`internal/cli` 只依赖这一层） |
| `app.go`（保留） | 364 | Bubble Tea 骨架：`Model`、内部消息类型、构造、`Init`/`View` |
| `update.go` | 390 | `Update` 事件分派 |
| `layout.go` | 252 | 尺寸预算、底部 chrome 组装、viewport 刷新节流 |
| `mode_hint.go` | 269 | 输入框与模式提示行（full/compact/minimal 三档降级） |
| `textarea_input.go` | 103 | 输入框样式、取值、随内容伸缩 |
| `status.go` | 174 | 状态行文案、耗时标签、省略号/spinner 动画 |
| `background.go` | 150 | 后台任务轮询与 away recap |
| `display_segments.go` | 291 | 流事件 → display timeline segment（时间线写入侧） |
| `transcript.go` | 286 | 已完成块冲进真实 scrollback（时间线落盘侧） |
| `live_display.go` | 562 | viewport 内仍在变化的活跃块渲染 |
| `message_render.go` | 141 | 单条消息（用户/助手/recap）渲染 |
| `markdown.go` | 378 | markdown 渲染管线 + rich inline 富文本标签 |
| `markdown_table.go` | 429 | 表格流式稳定化、网格渲染、窄屏改列表 |
| `stream.go` | 445 | 驱动一次 prompt 执行并套用流事件 |
| `usage.go` | 320 | token/成本/上下文用量面板 + 流错误文案 |
| `tool_summary.go` | 552 | 工具入参/输出压成一行摘要 |
| `tool_gate.go` | 158 | 权限网关拦截类输出识别（单独成卡片而非当报错） |
| `tool_activity.go` | 434 | 工具调用生命周期 + 工具卡片渲染 |
| `capability_loop.go` | 368 | capability_loop 协议包裹的输出与 agent evidence 解析 |
| `todo.go` | 335 | TodoWrite 状态与待办进度渲染 |
| `agent_progress.go` | 570 | 子代理进度状态与面板渲染 |
| `permission_prompt.go` | 284 | 权限审批弹窗 + `--permission-mode` 切换 |
| `question_prompt.go` | 154 | AskUserQuestion 弹窗（常驻输入框自由作答） |
| `slash.go` | 158 | slash 命令补全与裸命令归一化 |
| `resume_picker.go` | 248 | `/resume` 会话选择弹窗 |
| `rewind_picker.go` | 231 | `/rewind`、`/checkpoint` 检查点选择弹窗 |
| `attachments.go` | 290 | 附件托盘与剪贴板图片导入 |
| `welcome.go` | 574 | 欢迎卡与头部（含沙箱告警、吉祥物动画） |
| `util.go` | 269 | 包内通用小工具（换行、截断、payload 取值、数值夹取） |

### 三、怎么保证是「移动」而不是「重写」

- 全部 518 个顶层声明**整块搬迁**，函数体、注释、空行逐字不变；每个新文件的导入按
  实际引用重算。**没有一处签名、可见性或函数体改动**（同包拆分，本来就不需要改可见性）。
- 机器校验：把原 `app.go` 与 30 个新文件各自剥掉 `package` 行与 import 块后，
  非空正文行的**多重集完全相同**（8555 = 8555），且 30 个文件的 import 并集
  与原文件的 import 集合**对称差为空**（没有多引一个包，也没有漏掉一个包）。
- 唯一的新增内容：每个新文件顶部一行说明该文件职责的注释（与 `package` 之间空一行，
  因此不是包文档注释）。这是本次唯一不属于「移动」的改动，逐条列在此处。
- 刚落地的三处敏感区域原样搬迁：权限审批弹窗与 `--permission-mode` 交互（AUDIT-P0-01）
  进 `permission_prompt.go`；AskUserQuestion 常驻输入框弹窗进 `question_prompt.go`；
  沙箱状态标签与欢迎卡告警（AUDIT-P1-35）进 `welcome.go` —— 反方向断言
  `TestWelcomeCardUnchangedWithoutSandboxWarnings` 未改仍绿。
- `internal/cli/interactive.go` **一字未动**（含 `tuiSandboxLabel`）。
- `firstNonEmpty` 按要求**没有合并**（AUDIT-P2-03 留给独立批次），随 `util.go` 原样搬走。

### 四、验证

| 检查 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./... -count=1` | 全绿 |
| `go test -race ./internal/tui ./internal/cli -count=1` | 全绿 |
| `gofmt -l .` | 干净 |
| `git diff --check` | 干净 |
| 测试断言改动 | **0 处**（`git status` 下无任何 `*_test.go` 被修改） |
| golden 文件改动 | **0 处**（`internal/tui/testdata/`、`internal/cli/testdata/golden/` 均未出现在 diff 中） |

### 五、明确未做

- **`app_test.go` 7173 行未拆**（[TODO-095](../todo.md)）。试过按「测试引用的符号归属哪个新文件」
  自动投票，结果不可用：`Model`、`message`、`busy` 这类高频共享标识符让 `app.go` / `status.go`
  在大多数测试上都排第一，而近半数 `TestModelXxx` 本来就是跨模块的集成测试，归属需要逐个判断。
  在把 218 个测试逐个读完之前，机械拆分只会把「一个大文件」换成「三十个归属可疑的文件」。
- **前端部分未动**（[TODO-097](../todo.md)）：`WebAgentPage.tsx` / `InspectorPanels.tsx` / `app.css`
  是另一套技术栈与另一套验收手段，不适合和 Go 侧塞进同一次纯重构。
- **`welcome.go` / `agent_progress.go` / `live_display.go` 仍在 560–575 行**
  （[TODO-098](../todo.md)）。它们内部确实还能再切（欢迎卡 vs 吉祥物动画、子代理状态 vs 面板渲染），
  但再切一层就要开始拆函数而不只是移动函数，超出本次「纯重构」的边界。
- **`util.go` 里 `min` / `max` 遮蔽 Go 1.21 内置函数**（[TODO-096](../todo.md)）。
  发现即登记，本次不顺手改 —— 删掉这两个函数会改变全包的调用解析，属于行为面的改动。

## 修复证据 · AUDIT-P2-01 / P2-02（2026-07-26）

**纯重构批次**。验收标准和功能开发不同：判据不是「测试绿」，而是「既有测试一行都不用改就仍然绿」。
本批**没有改动任何既有测试的断言**，也没有顺手修 bug、优化或改公开行为。

### 一、`query.go` 7910 → 5976 行（−1934，−24.5%），顶层声明 370 → 251

按 §8 给的投入产出顺序做完 ①②③④，每一步都是移动而非重写：

| 步 | 内容 | 声明数 | 行数 | 去处 |
| --- | --- | --- | --- | --- |
| ① | agent evidence / capability_loop 解析 | 46 | 783 | 新包 [internal/capabilityloop](../../internal/capabilityloop/capabilityloop.go) |
| ② | 系统提示词正文 + runtime task strategy | 38 | 548 | [internal/query/systemsections.go](../../internal/query/systemsections.go) |
| ③ | transcript↔messages + resume 清洗 | 15 | 321 | [internal/query/resume.go](../../internal/query/resume.go) |
| ④ | completion verification | 19 | 282 | 并入 [internal/query/closure_gate.go](../../internal/query/closure_gate.go)（1817 → 2109） |

**主循环 `run`（713 行）没有动**，按 §9 的既定取舍。

### 二、②③④ 为什么留在 `package query` 而不是各自建包

审计对 ② 的建议是「独立包或 embed 的模板文件」。实测后选了同包分文件，理由是可度量的：

- **② 的 section 正文并不纯**。`antModelOverrideSection`/`mcpInstructionsSection`/`scratchpadSection`/
  `briefSection`/`functionResultClearingSection` 读 `getenv`、`isEnvTruthy`、`firstNonEmpty`，
  而这三个助手在 `package query` 内另有 **25 / 18 / 40** 处调用。换包就必须复制三份助手 ——
  而 `firstNonEmpty` 正是 AUDIT-P2-03 要收敛的重复项，等于一边拆一边制造它要修的东西。
- **④ 换包更亏**。`bashCommandFromTrace`、`looksLikeVerificationCommand`、`displayChangePath`、
  `finalTextClaimsCompletion` 定义在 `query.go` 却被 `closure_gate.go` 调用约 30 次 ——
  这正是审计说的「同一语义散在两处」。同包合并**零改调用点**；换包要改 30 处并导出 4 个函数。
- **改 embed 模板被否**：section 正文里大量 `strings.TrimSpace` / 拼接 / 条件分支，
  转模板必然改动空白与换行，而这些字符会**逐字进 prompt**。属于改行为，不是重构。

① 是真的换了包 —— 那 46 个声明零 `Session` 依赖（审计说「只有 `rememberRecentAgentEvidence` 摸
`s.recentAgentEvidence`」，实测该区间里有 **8 个 `Session` 方法**，但它们都是*调用方*，留在 `query.go`；
移走的是被调用的纯函数闭包）。

### 三、怎么证明是「移动」而不是「重写」

用 AST 打指纹逐函数比对（`go/printer` 规范化函数体后哈希），拿 `HEAD` 的 `query.go` 对上
拆完后的 5 个文件：**210 个函数体全部逐字相同，0 个丢失**。唯一 5 处差异是本批 P2-02 的
字面量→同值常量替换（`"<capability_loop>"` → `OpenTag` 等），已逐条人工 diff 确认值相同。
`toolresult` / `compact` 侧同样比对，6 处差异全部是同一类替换。

### 四、AUDIT-P2-02：本条的前提是错的

审计说「三份重复的解析器实现」，并预期拆完 ① 就能合并。**实测：三个文件之间零个函数体字节相同。**
它们不是复制粘贴，而是同一协议的三份**独立实现**，产出形态本就不同：

| 实现 | 产出 |
| --- | --- |
| `internal/capabilityloop` | `HintData` + `ArtifactHints`，喂 runtime status 与 follow-up gate |
| `internal/toolresult` | 一段人读 summary，在大工具结果被外化时保留 |
| `internal/compact/facts` | `Facts` 的分类切片，跨压缩保留 |

而且它们**对同一 payload 的解析结果不一致**。四类分叉，每一类合并都是行为变更：

1. **候选串提取**（→ [TODO-092](../todo.md)）。`capabilityloop` 版：先 `ToLower` 再匹配标签、**只取第一对**、
   无子串守卫、兜底取最外层 `{...}`。`toolresult` 版：大小写敏感、**取全部**标签对、有子串守卫、
   兜底取整段内容。四处差异都会改变「哪些 payload 解析得出来」。
2. **placeholder 集合**（→ [TODO-093](../todo.md)）。`capabilityloop` 多丢一条默认 next_action，
   `compact` 不丢 —— 同一条默认文案在 runtime status 被过滤、在压缩 facts 被保留。
3. **字段别名**（→ [TODO-093](../todo.md)）。`compact` 额外认 `unknown_to_resolve_or_disclose`、
   `verification_required`、`risk_to_account_for`、`must_handle_next_action`、`supersedes` 和空格拼写；
   另两处只认规范名。~~子代理用别名拼写，压缩后 facts 有值而 runtime status 没有。~~
   **此后果经查不存在**：前四个名字是 `FollowUpLine` 与 `internal/goal` 自己发出的 follow-up gate
   行标签、由 compact 读回，不是子代理的拼法。详见
   [修复证据 §三](#三todo-093--原判断有误--那些不是字段别名)。
4. **字段覆盖面**（→ [TODO-094](../todo.md)）。`toolresult` 只渲染 6 个字段，从不读
   `follow_up_id`/`resolved_follow_up`/`supersedes_evidence_*`。后果是走「外化 / 持久化 summary 反解」
   这条路时 supersede 关系会丢，已解决的 follow-up 可能重新变 pending。

**本批只做了安全的那部分**：把三者确实一致的结构性 token 收成一份
[internal/capabilityloop/protocol.go](../../internal/capabilityloop/protocol.go)
（`Key`/`OpenTag`/`CloseTag`/`LineMarker` + 规范字段名常量），19 处字面量替换为同值常量。
结构性 token 从此不可能再各自漂移；字段级分叉在该文件的包注释里列成表，留给上面三条各自立项。

**没做跨包一致性守卫测试**：`toolresult` 与 `compact` 的解析入口都不导出，
只能经 `Process` / `ExtractFacts` 间接触达，为写测试而导出新 API 属于改公开面，不在纯重构范围内。

### 五、测试改动清单（逐条）

- **断言改动：0 处。**
- `TestAgentEvidenceFollowUpSkipsPlaceholderFields`、`TestAgentEvidenceFollowUpSkipsDefaultNextAction`
  从 `query_test.go` 迁到 [internal/capabilityloop/capabilityloop_test.go](../../internal/capabilityloop/capabilityloop_test.go)
  —— 它们测的是已移走的纯函数。函数体逐字不变，只改了随包移动的标识符名
  （`agentTaskCapabilityLoopHint` → `TaskHint`、`agentEvidenceFollowUpLine` → `FollowUpLine`）。
- `TestAgentEvidenceFollowUpResolutionClearsSupersededPending`、
  `TestAgentEvidenceFollowUpResolutionRequiresProof` 留在 `query_test.go`（它们构造 `&Session{}`），
  只把类型名换成限定名 `capabilityloop.DecisionContext` / `capabilityloop.HintData`。
- 其余测试文件（`resume_v2_test.go`、`golden_test.go`、`compact_order_test.go`、
  `toolresult_test.go`、`compact` 的 facts 测试）**一个字符都没改**。

### 六、顺带发现，已登记未修

- `trimRuntimeStatusText` 与 `firstNonEmpty` 在新包里各留了一份**逐字副本**并带
  `// TODO(AUDIT-P2-03)` 注释 —— 按既定分工不在本批合并，避免与 AUDIT-P2-03 及并行任务三方冲突。
- 结构体 tag 里的 `json:"capability_loop"`（`capabilityloop.go` 两处）无法用常量表达，是 Go 的限制，保留字面量。

### 验证

```
go test ./... -count=1                                                              # 全绿
go test -race ./internal/query ./internal/compact ./internal/toolresult ./internal/capabilityloop -count=1
go vet ./...                                                                        # 无输出
gofmt -l .                                                                          # 无输出
git diff --check                                                                    # 无输出
```

---

## 修复证据 · AUDIT-P1-26 事务边界（2026-07-26）

原 [TODO-070](../todo.md)，AUDIT-P1-26 的第三项。至此 P1-26 三项全部闭合。

### 一、缺陷

`persistTenantQuery` 连写会话 / 用户消息 / 助手消息三张表，三次独立提交。
仓储全局 `SkipDefaultTransaction: true`，所以每条语句自己就是一个事务 ——
第二或第三次写入失败时前面的行已经落库，留下半写会话：
会话行在、消息缺失，或用户消息在、助手消息缺失。

### 二、接缝放在存储层，不暴露事务句柄

给仓储加一个「原子写入一次查询结果」的方法
`SaveQueryTurn(ctx, QueryTurnInput) (QueryTurnResult, error)`，
逐层加到 `mysql.GormRepository` → `tenant.Repository` / `tenant.Service` →
`server.TenantService`，`persistTenantQuery` 从三次调用收敛成一次。

选它而不是向上暴露事务句柄，理由有三条：

1. **句柄会把 GORM 漏进 `internal/server`**，并且 `tenant.Repository` 的每个
   假实现都得跟着模拟事务语义 —— 这个接口有 70+ 方法，两处测试夹具实现它。
2. **死锁重试必须包住整个事务**。重试的是「重新 BEGIN 并重写三行」，
   如果由服务端驱动事务，重试就得回放服务端逻辑。放在仓储里才写得成
   `withRetryableTransaction(ctx, func() error { tx... })`，与
   `RollbackSkillVersion`（`gorm_repository.go:785`）同构。
3. 记忆写入依赖会话 ID 和用户消息 ID，但写的是第四张表、不属于这次查询的
   原子边界，所以留在事务外、挪到提交之后。

**没有动全局的 `SkipDefaultTransaction: true`** —— 那是刻意的性能选择，
改它会影响全库每一次写入；这里只给这一条路径开显式事务。

### 三、租户隔离没有被事务绕过去

2026-06-26 数据隔离 review 加的「写消息前校验会话归属」保留在事务内：
`UpsertSession` / `GetSession` / `UpsertMessage` 的 SQL 各抽出一个收 `*gorm.DB`
的辅助函数（`upsertSessionTx` / `getSessionTx` / `upsertMessageRowTx`），
普通路径和事务路径共用同一段 SQL。`SaveQueryTurn` 在会话 upsert 之后、
两条消息之前调一次 `getSessionTx`，两条消息的 tenant/user/session 三个字段
统一由仓储填成刚写的那一行，调用方填不进来。
服务层的 `TenantID`/`UserID` 只来自 `ResolveContext`。

比原来少一次 SELECT：原路径两条消息各查一次会话，现在整批查一次。

### 四、红/绿验证

验收标准要求「新增测试须先红」。两层各自见过红：

**服务端接缝（`internal/server/persist_query_test.go`）** —— 对**未改动的生产代码**
直接跑，`tenantTurnStore` 在第 2 / 第 3 次写入注入失败：

```
--- FAIL: TestPersistTenantQueryLeavesNoRowsWhenALaterWriteFails
    persist_query_test.go:72: failAt=2: session rows = 1, want 0 (half-written session left behind)
```

夹具能不能做到原子本身就是被测性质：三次独立调用无论如何凑不出原子性。
夹具同时保留逐张写的 `UpsertSession`/`UpsertMessage`，一旦 `persistTenantQuery`
退回三次独立写入就会被打到，该测试立刻重新变红。

**存储层（`internal/storage/mysql/query_turn_test.go`）** —— 先把
`SaveQueryTurn` 按今天的三次独立写入实现，确认 5 个测试都在「没有事务」上失败：

```
--- FAIL: TestGormRepositorySaveQueryTurnRollsBackWhenUserMessageFails
    there is a remaining expectation which was not matched:
    ExpectedBegin => expecting database transaction Begin
（另 4 个同因失败：...RollsBackWhenAssistantMessageFails / ...RejectsForeignSession
  / ...CommitsAllThreeRows / ...RetriesWholeTransactionOnDeadlock）
```

换成显式事务后转绿。`sqlmock` 的 `ExpectRollback` 只有在驱动真的发过 ROLLBACK
时才通过，即会话行不会提交。`...RetriesWholeTransactionOnDeadlock` 用 MySQL 1213
让第一次尝试失败，断言第二次尝试**重新 BEGIN 并重新写会话行** —— 守的是
「重试整个事务而不是事务内的单条语句」，否则消息会挂到已回滚的会话上。

**服务层租户隔离（`TestServiceSaveQueryTurnUsesResolvedIDs`）** —— 这个测试写在
实现之后，所以另做了变异验证证明它不是空测：把 `TenantID: resolved.TenantID`
改成 `0`，测试转红（`session input = {... TenantID:0 UserID:5 ...}`），改回后转绿。

### 五、明确未做 / 未验证的部分

- **真机 MySQL e2e 跑不了**。本机既无 MySQL 也无 docker（`mysql`/`docker`/`mysqld`
  都不在 PATH，3306 未监听），`GOLANG_CLAUDE_CODE_MYSQL_E2E_DSN` 未设置。
  新增的 `TestMySQLE2ESaveQueryTurnRollsBackHalfWrittenSession`（`internal/tenant/mysql_e2e_test.go`）
  **只验证过它能编译并正确 skip，没有在真 InnoDB 上跑过**。它是唯一一处真正断言
  「第一张表也没留下行」的测试（用超长 `role` 触发 1406，非死锁所以不会被重试），
  留给有 DSN 的人执行，转 TODO-105（与 TODO-071 是同一个环境缺口）。
  已验证的证据是上面的 sqlmock 与服务端接缝两层。
  **2026-07-26 补充**：TODO-106 同批新增的
  `TestMySQLE2EForkSessionRollsBackHalfCopiedBranch` 处境完全相同 ——
  同样用超长 `role`（第 3 条消息）触发 1406，同样只验证过能编译并正确 skip，
  同样没在真 InnoDB 上跑过。环境缺口仍是 TODO-105。
- **同形态的另一处未纳入**。`mobile.go:410-450` 的会话 fork/branch 先建新会话再
  循环复制消息，中途失败会留下只复制了一半的分支会话 —— 比 `/query` 那条更糟，
  因为消息条数不定。它是独立端点、不在 TODO-070 范围内，且按协作边界应尽量少动
  `internal/server` 的其它部分，转 TODO-106。复用 `SaveQueryTurn` 并不合适：
  fork 的形状是「一个会话 + N 条消息」，更接近 `SaveKnowledgeDocument(doc, chunks)`。
  `handlers_sessions.go:43` 与 `:555` **不在此列** —— 那是两个独立端点、两次 HTTP
  请求，不存在跨表原子性问题。
  **2026-07-26 已闭合（TODO-106）**：`internal/storage/mysql/session_fork.go` 的
  `ForkSession` 按上面判断的形状新写（不复用 `SaveQueryTurn`），三处刻意选择与
  TODO-070 一致：只给这条路径开事务、重试整个事务、归属校验留在事务内。
  「消息条数不定」这一点在实现时收紧成了显式上限 `maxForkedMessages = 500`：
  条数其实早就被 `normalizeLimit` 夹在 500，但那是两层之外的巧合，
  夹取一旦调大，没有上限的 fork 就是几万行的单事务。选拒绝而非分批，
  因为分批写会重新打开「半写会话」这个正要修掉的窗口。
- **错误日志粒度变粗**。原来三条分别的 `persist tenant {session,user message,
  assistant message} failed` 合成一条 `persist tenant query turn failed` ——
  这一层已经没有「哪张表失败」这个概念了，具体语句错误由 GORM 的错误原样带出。

### 验证

```
go test ./internal/server ./internal/storage/... -count=1                  # 全绿
go test ./... -count=1                                                     # 全绿
go test -race ./internal/server ./internal/storage/... ./internal/tenant -count=1   # 全绿
go vet ./...                                                               # 无输出
gofmt -l .                                                                 # 无输出
git diff --check                                                           # 无输出
scripts/tenant-mysql-e2e.sh                                                # 未跑：本机无 MySQL/docker
```

## 修复证据 · AUDIT-P2-02 收口（2026-07-26，TODO-092 / 093 / 094）

上一批（`93b8720`）只做了物理合并，把三份实现之间的四类**行为分叉**如实登记成
TODO-092/093/094 并把本条留在 PARTIAL。本批**做决定**：分叉逐条定谁对，然后统一。
**行为会变，而且应该变** —— 下面每条都写明「原来两种行为分别是什么、选了哪个、为什么」。

### 一、TODO-092：候选串提取的四处差异

统一成 `capabilityloop.JSONCandidates`（导出，三个包共用）。逐处决定：

| 差异 | `capabilityloop` 原状 | `toolresult` 原状 | 决定 | 为什么 |
| --- | --- | --- | --- | --- |
| 标签大小写 | 不敏感（`ToLower` 后 Index） | 敏感 | **不敏感**，但换实现 | 宽松严格占优：子代理写 `<Capability_Loop>` 时，敏感的那一侧把整份证据丢掉，而没有任何合法 payload 依赖大小写。**但原实现本身有 bug**：`ToLower` 会改变字节长度（`İ` → `i̇`），偏移量随之整体右移，标签体被切歪。改用 `indexFold`（ASCII 折叠、偏移量落在原串上）保留语义、去掉 bug |
| 取几对标签 | 只取第一对 | 取全部 | **取全部** | 严格占优：消费方是「按序解析、第一个成功者胜出」，多给候选只可能变好。原来第一对畸形就会丢掉后面那对合法 payload |
| 子串守卫 | 无 | `Contains(content, Key)` | **有，且改为大小写不敏感** | **行为中性**的快路径：下游每个 decoder 都要求 `capability_loop` 键，不含该子串的内容永远解析不出 hint。省掉对 50KB 工具结果的一次 `ToLower` 分配 + `Unmarshal` |
| 无标签兜底 | 最外层 `{...}` 切片 | 整段内容 | **两者都要，整段在前** | **这处不是「取并集」**：两边都不占优，且两种失败都丢证据。整段是唯一能解析数组根（`[{"capability_loop":…}]` 的 `{...}` 切片是 `{…},{…}`，非法 JSON）的；`{...}` 切片是唯一能捞出散文里嵌的 JSON 的。它们是同一件事（「找没被标签包住的 JSON」）的两次有序尝试，不会同时有效且不同 —— 对象根时切片就是 trim 后的整段 |

另加候选去重（同串只留一份），使 compact 不会把同一 payload 走两遍。

**测试**（各一个，去掉修复即挂）：`TestJSONCandidatesTakesEveryTagPair`（多标签）、
`TestPersistedSummaryMatchesTagsCaseInsensitively`（大小写，落在 `toolresult` —— 行为真正变的一侧）、
`TestJSONCandidatesSurvivesWidthChangingLowercase`（偏移漂移；用尾部 `{...}` 堵掉兜底，
证明这不是理论问题：修复前该用例是**失败**的，原来靠兜底侥幸救回）、
`TestJSONCandidatesFallbackCoversArrayRootAndEmbeddedJSON`（数组根 + 散文内嵌）。

### 二、TODO-093 ①：placeholder 集合 —— compact 采纳 capabilityloop 的集合

`agentruntime` 在子代理没写 next_action 时按状态注入默认文案。**completed 那条过滤**，
理由不止「是样板话」：它单独就能让 `HasHint` 成立，于是压缩恢复出的 decision context
会挤进父代理只有 3 格的证据环，**顶掉真证据**；而 runtime status 侧本就已经过滤它
（`TestAgentEvidenceFollowUpSkipsDefaultNextAction`、`internal/tui/app_test.go:5289`），
压缩不该把活路径已压制的东西复活。

**刻意只收这一条。** `agentruntime` 另有 4 条状态默认文案（failed/cancelled/timeout/unknown），
它们**故意不算 placeholder**：那是「父代理必须处理一次失败」的真义务，且 follow-up gate
有测试要求它们出现（`query_test.go:5722`、`scripts/task-partial-evidence-acceptance.sh:125`）。
两个测试把这条界钉住：`TestExtractFactsDropsDefaultCompletedNextAction` 与
`TestExtractFactsKeepsFailurePathDefaultNextAction`。

### 三、TODO-093 ②：**原判断有误** —— 那些不是「字段别名」

审计与 TODO 都说这是「子代理用别名拼写」导致的分叉。**实测不是。**
`unknown_to_resolve_or_disclose`/`verification_required`/`risk_to_account_for`/`must_handle_next_action`
是 `capabilityloop.FollowUpLine`（`capabilityloop.go:613-624`）与 `internal/goal/runner.go:463-472`
**自己发出**的 follow-up gate 行标签 —— gate 行本身进 transcript，compact 再把它读回来。
所以：

- **不给 `capabilityloop`/`toolresult` 补这些名字**：它们读不到这个面。`toolresult` 读子代理 JSON，
  `capabilityloop` 读 `toolresult` 产的规范名 summary 块。补了是死代码。
- **改为单一来源**：`protocol.go` 新增 `FollowUpField*` 四个常量，`FollowUpLine` 发、compact 认，
  同一份常量，改名不可能只改一侧。**行为零变化**（同值常量替换）。
- 空格拼写（`next action`、`follow up id`、`resolved follow up`、`supersedes evidence id`）与裸
  `supersedes` **保留，且不是分叉**：它们没有发出方，是对**模型撰写**的面的刻意宽容 ——
  compact 读的压缩摘要是模型写的，另两处读的都是机器产的文本。判据是输入面，不是包。

原文「子代理若用别名拼写，压缩后 facts 有值而 runtime status 没有」这个后果**不存在**，已在正文更正。

### 四、TODO-094：真 bug，两处都补（附端到端）

链路：子代理结果过大 → `toolresult` 外化 → payload 被 persisted-output 桩替换 →
**保留的 summary 成为 supersede 关系的唯一载体** → 父代理反解它。原状两处都漏：

1. `formatCapabilityLoopSummary` 只渲染 6 个字段，**不渲染** `resolved_follow_up`/`follow_up_id`/
   `supersedes_evidence_id(s)`。补齐；且 `supersedes_evidence_ids` **渲染全部 id 而非第一个**
   —— supersede 是集合关系，丢尾巴等于静默把那些 follow-up 重新变 pending。
2. `addCapabilityLoopSummaryField` 不认复数键，且把 `TaskHint` 发出的逗号拼接值
   （`supersedes_evidence_id: id1,id2`）整串塞进单数字段，产出**一个匹配不到任何 follow-up 的假 id**
   —— 这是同族的第二个真 bug。改为两个键都按逗号切开进复数切片（证据 id 形如
   `task:<n>`/`tool:<id>`，不含逗号）。compact 的 `addCapabilityFact` 同样切开，
   因为 `query` 读 `CapabilitySupersedes[0]` 当单数 id。

**端到端**（TODO-094 原文点名要求）：
`internal/query/capability_loop_externalized_test.go`。
`TestExternalizedTaskResultKeepsFollowUpResolved` 先立一个 pending follow-up，再让解决它的结果
真的走一遍 `toolresult.Process` 外化（前缀把 payload 顶出 2000 字节预览窗，保证只有 summary 能携带），
然后断言 gate 里那条 pending **消失**。**已实测两半各自可证伪**：单独回退 `toolresult` 渲染 → 挂；
单独回退 `capabilityloop` 复数键 + 逗号切分 → 挂。
`TestExternalizedTaskResultKeepsSupersedeFactsForCompaction` 覆盖同一个桩再走压缩那一跳。

### 五、golden：一个都没改，且这才是对的

`internal/query/testdata/golden/code_mode_recovery_matrix.json` 等 capability_loop 金标**一字未改**。
不是为了变绿而不动 —— 而是本批的行为变化方向决定了它们不该动：四类修复要么
**只加宽「什么能解析成功」**（大小写、多标签、数组根），要么**只往 summary 里加字段**，
而这些 golden 断言的是「某个 section / 字段**存在**」的布尔值，原本已为 `true`，加宽后仍为 `true`。
若有 golden 变了，反而说明改动误伤了既有能解析的 payload。

### 六、顺带发现，已登记未修

> 前三条已于 2026-07-26 闭合，见
> [修复证据 · TODO-102 / 101 / 100](#修复证据--todo-102--101--1002026-07-26import-环与两处证据判定分叉)。

- **placeholder 集合全仓共 5 份，本批只统一了归属内的 2 份**（`capabilityloop` + `compact`；
  `internal/tui/capability_loop.go:343` 本就一致）。剩下 `internal/goal/evidence.go:223` 与
  `internal/agentruntime/runtime.go:1018` **仍缺 completed 默认文案那条**，于是同一句样板话在
  goal 侧的 gate 里会被当成 `must_handle_next_action` 输出，而 query 侧压制它 ——
  **同一份子代理输出在两种驱动方式下结论不同**。→ TODO-101（goal 侧还受下一条的 import 环限制）。**已闭合**
- **`agentruntime` 的 `defaultNextActions`（`runtime.go:1004`）**
  只列了 5 条注入默认里的 4 条 —— **漏了 timeout 那条**。后果：内容全空的超时子代理被
  `CapabilityLoopHasActionableEvidence` 判为「有可用证据」，而同样全空的 completed 判为没有。
  → TODO-100 / TODO-101。不在本批：`internal/agentruntime` 不属本批归属，且该判定直接决定
  Task/AgentGet 是否呈现结果，需自带测试单独决策。**已闭合**
- **`internal/goal/runner.go` 的四个 gate 标签只能保持字面量**：它被 `internal/storage/mysql` 引用，
  而后者被 `capabilityloop` 引用，引用常量会闭合 import 环（已实测）。根因是
  `capabilityloop`（纯协议包）依赖 `storage/mysql` → TODO-102。**已闭合**
- `firstNonEmpty` 全仓重复**未动**，留给 AUDIT-P2-03（协作边界）。

### 验证

```
go test ./... -count=1                                                              # 全绿
go test -race ./internal/capabilityloop ./internal/query ./internal/toolresult ./internal/compact -count=1
go vet ./...                                                                        # 无输出
gofmt -l .                                                                          # 无输出
git diff --check                                                                    # 无输出
```

---

## 修复证据 · TODO-102 / 101 / 100（2026-07-26，import 环与两处证据判定分叉）

三条是一条线，按 102 → 101 → 100 的顺序做：102 拆掉 import 环，101 才可能引用单一来源。
**102 是纯重构（既有测试一行未改），101 与 100 行为会变，而且应该变。**

### 一、TODO-102：把纯协议包从存储层摘出来（纯重构）

环是 `capabilityloop` → `internal/storage/mysql` → `internal/goal` → 回到 `capabilityloop`。
根因只有一处：`DecisionContextFromStoredTask` 为了接一个 `mysqlstore.AgentTask` 参数，
让**纯协议包 import 了存储包**。而它实际只读该结构 15 个字段里的 5 个
（`ID` / `AgentName` / `Description` / `Status` / `ResultJSON`）。

改法是把入参降为协议包自有的 `capabilityloop.StoredTask`（只有那 5 个字段），
唯一调用方 `internal/query/query.go:4896`（agent task 通知行的构建处，**不是**并行任务持有的
`recordMessage`）在调用点填结构体。**没有动 `internal/storage/mysql` 一行** ——
依赖是单向的，摘掉引用不需要改被引用方。

判据是「既有测试一行都不用改就仍然绿」，已实测：改完后
`git ls-files 'internal/**/*_test.go' | xargs shasum | shasum` 与改动前**同一摘要**
（`c4bd08e6…`），`go test ./... -count=1` 全绿。

环已消失，两个方向都验证过：

```
go list -deps ./internal/capabilityloop | grep golang-claude-code
# 改前：… internal/goal、internal/quota、internal/storage/mysql、internal/capabilityloop
# 改后：internal/agenttasks internal/identity internal/config internal/observability
#       internal/telemetry internal/anthropic internal/capabilityloop
#       —— storage/mysql 与 goal 都不在闭包里了
```

于是 `internal/goal/runner.go` 的四个 gate 标签从字面量改成
`capabilityloop.FollowUpField*`（同值常量替换，行为零变化），双份维护消失 ——
改名不再会静默让 compact 读不回该 fact。`internal/agentruntime` 也因此能引用该包，这是 101 的前提。

### 二、TODO-101：placeholder 集合，剩下 2 份收口

`agentruntime` 在子代理没写 next_action 时按状态注入默认文案。completed 那条是**机器写的样板话**，
`capabilityloop` / `compact` / `internal/tui` 都把它当噪音过滤，但 `internal/goal` 与
`internal/agentruntime` 各自的私有集合**漏了它**，于是同一句话在这两处算真信号。

后果不是「少过滤一句话」，而是**同一份子代理输出在两种驱动方式下得出不同结论**：
goal 驱动把它当成 `must_handle_next_action` 发给父代理（一个不存在的义务），query 驱动压制它。
父代理该做什么，取决于它恰好跑在哪条驱动路径上。

两处都改成 delegate 到 `capabilityloop.IsPlaceholder`，私有副本删除。
**failure/cancelled/timeout 三条默认文案仍然不算 placeholder** —— 那是「必须处理一次失败」的真义务，
follow-up gate 有测试要求它们出现；两个测试的「不可过度过滤」半边把这条界钉住。

**核心测试是跨驱动方式的**，因为缺陷本身是「两侧不一致」，各自对着硬编码期望断言抓不到它：
`TestCapabilityFollowUpGateAgreesAcrossGoalAndQueryDrivers`
（`internal/goal/capability_divergence_test.go`）拿**同一份** loop 分别过
`goalCapabilityFollowUpLine` 与 `capabilityloop.FollowUpLine`，断言两者对
`must_handle_next_action` 的结论**相同**，且都不是「有义务」。

**先红已实测**（把修复换回改动前的私有集合，即只去掉 completed 那条）：

```
--- FAIL: TestCapabilityFollowUpGateAgreesAcrossGoalAndQueryDrivers
    drivers disagree on must_handle_next_action for the same sub-agent output:
      goal  (claims=true):  … | must_handle_next_action: Parent agent should synthesize …
      query (claims=false):
--- FAIL: TestGoalActionableTextFiltersExactlyTheProtocolPlaceholders
--- FAIL: TestAgentRuntimePlaceholderSetMatchesProtocol
--- FAIL: TestCompletedDefaultBoilerplateAloneIsNotActionableEvidence
```

`internal/tui/capability_loop.go:343` 曾是**第四份私有副本**，值与协议一致但没有单一来源，
本批按协作边界只读不动 → TODO-110，**已随后闭合**（见下节）。

### 三、TODO-100：`defaultNextActions` 漏了 timeout，判断是反的

`capabilityLoopFromContent` 按 5 个状态注入默认 next_action，但
`CapabilityLoopHasActionableEvidence` 里的 `defaultNextActions` 只列了 4 条 —— **漏了 timeout**。
于是内容全空的 timeout 子代理，靠 runtime 自己刚写进去的样板话通过判定，
被当成「有可用证据」；而同样全空的 completed 正确判为没有。**判断只在这一个状态上是反的。**

收在「都判为没有」这一侧。理由不是就近：查过两个调用方
（`runtime.go:931`、`internal/tools/task/task.go:762`），该判定**只决定要不要附上结构化
`capability_loop` 块**，`status` / `is_error` / `error` / `Content` 都另行送达。
所以对全空 timeout 判 false **不会**让父代理看不见「任务超时了」，只是不再附一块
100% 由 runtime 自己写的样板。相反，若收在「都判为有」，父代理会为空内容收到一块假证据。

顺带把 completed 那条从 `defaultNextActions` 删掉：101 之后
`isCapabilityLoopPlaceholder` 已在前面的早退里拦住它，留着是重复维护。
这一行删除**行为等价**，剩下的 4 条正好是「失败路径的注入默认」这一组，语义自洽。

**先红已实测**（只删回 timeout 那一行字符串）：

```
--- FAIL: TestEmptySubAgentHasNoActionableEvidenceForEveryTerminalStatus
    status "timeout": an empty sub-agent must not count as actionable evidence
--- FAIL: TestTaskToolGoldenBatchTimeout   (golden mismatch for batch_timeout.json)
```

两个方向都覆盖：`TestEmptySubAgentHasNoActionableEvidenceForEveryTerminalStatus`
遍历 completed/failed/cancelled/timeout/未识别五个状态断言结论一致（只钉 timeout 一侧的话，
将来把 completed 翻成 true 也能过，仍然分叉）；
`TestTimeoutWithPartialContentStillHasActionableEvidence` 是反向护栏 ——
**带真实部分内容**的 timeout 仍然是可用证据，证明修复没有把判定改瞎。

### 四、golden：改了一个，且这才是对的

与上一批相反，本批**必须**动 golden：`internal/tools/task/testdata/golden/batch_timeout.json`
原本就把这个 bug 冻在文件里 —— 一个全空 timeout 任务，挂着一整块
evidence/assumptions/unknowns/verification/next_action **全部由 runtime 注入**的
`capability_loop`，零子代理信号。删掉该块后 `status: "timeout"`、`is_error: true`、
`error: "context deadline exceeded"` 全部保留，父代理照样知道任务超时了。

三个 golden 里**只有 timeout 这一个**带该块，`batch_priority_retry.json`（completed）
与 `batch_invalid_short_timeout.json` 都没有 —— 这个不对称本身就是那个反了的判定，
一直躺在仓库里。该 golden 也实测可证伪（去掉修复即 mismatch）。

### 验证

```
go test ./... -count=1                                                          # 全绿
go test -race ./internal/capabilityloop ./internal/agentruntime ./internal/goal ./internal/query -count=1
go vet ./...                                                                    # 无输出
gofmt -l .                                                                      # 无输出
git diff --check                                                                # 无输出
```

**两处偶发失败，均判定为负载敏感的既有 flaky，不是本批回归** —— 两个包在本批的改动都不涉及
goroutine、计时或它们所测的路径：

- `internal/tools/bash` 的 `TestBashToolRunInBackgroundPersistsJobAndLogs`，一次全量跑中报
  `unexpected end of JSON input`（读到**半写的后台 job 文件**）。该包本批**零改动**，
  单独连跑 3 次全绿 → TODO-111。
- `internal/agentruntime` 的 `TestRuntimeRunBackgroundReturnsTaskHandle`，四包并发 `-race` 时失败一次。
  该测试有三个硬编码 `time.Second` 窗口，与单包 88s 的 `internal/query` 同跑时会被挤爆；
  本批在该包只改了 `defaultNextActions` 的字符串字面量与 `isCapabilityLoopPlaceholder` 的 delegate，
  而它走的是 `RunBackground` 与 task store 事件。同一条命令本分支 4/5 通过、`origin/main` 5/5 通过 → TODO-112。
  **证据缺口如实记下**：首次失败只抓到 `--- FAIL` 行，未留下具体哪个 deadline 断言，后续 4 次未复现。


---

## 修复证据 · TODO-110（2026-07-26，最后一份 placeholder 私有副本）

TODO-101 收完后全仓只剩 `internal/tui` 一份私有 placeholder 集合。**它不是活着的 bug** ——
把它和 `capabilityloop.IsPlaceholder` 的函数体各自抽出、把函数名归一化后 `diff` 为空
（各 490 字节，连 `strings.Trim(value, ".。")` 的归一化都逐字相同），所以删掉它**不可能改变任何显示结果**。

删它的理由是**结构而不是当前值**：一份「恰好还一致」的副本，正是 TODO-101 那两处分叉长出来的同一个形状。
协议侧再加一条 placeholder（TODO-093 就干过这件事），compact / goal / agentruntime 自动继承，它不会 ——
分叉会以完全相同的机制重现一次。

**影响面比 TODO-101 小，这也是它当初被定 P3 的实际理由**：这份副本只喂
`firstCapabilityLoopSignal`，一条**显示**路径，分叉的后果是 TUI 上多显示一行样板话；
TODO-101 那两份喂的是 follow-up gate，分叉的后果是父代理被告知一个不存在的义务。不是一个量级。

改动本身：该副本**只有一个调用点**，所以没有留 wrapper —— 直接在 `firstCapabilityLoopSignal`
里改成 `capabilityloop.IsPlaceholder` 并删掉整个函数（净 −10 行）。`internal/tui` 原先完全没有
import 该协议包；加这个 import **不成环**，`capabilityloop` 的依赖闭包里没有 `internal/tui`（已用
`go list -deps` 核实）。

**没有补新测试，这是刻意的**：本条行为零变化，写不出一个「移除修复后会失败」的行为测试 ——
delegate 之后任何「两份集合一致」的断言都是同义反复。判据因此是既有测试全绿且**一行未改**
（`git status` 中无任何 `_test.go`），加上上面那个函数体 diff 为空。

### 验证

```
go test ./internal/tui -count=1            # 绿，且无任何测试文件改动
go test ./... -count=1                     # 全绿
go test -race ./internal/tui ./internal/capabilityloop -count=1
go vet ./...                               # 无输出
gofmt -l .                                 # 无输出
git diff --check                           # 无输出
```
