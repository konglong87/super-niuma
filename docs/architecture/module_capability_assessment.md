# Go Claude 模块能力评估报告

> 评估日期:2026-07-15
> 方法:对 11 个核心模块做代码级审计(读实现 + 测试 + 文档 + commit),所有结论带 `file:line` 或文档路径证据。全程只读,未修改任何代码。
> 范围:agent 内核、工具执行、会话状态、交互前端、服务化/多租户、生态扩展。

## 0. 评估口径说明(重要)

- 本报告的"测试覆盖率"分两种口径,不要混用:
  - **测试行数占比** = `测试代码行数 / 业务代码行数`,只是粗略代理指标,用于快速筛查。
  - **语句覆盖率** = `go test -cover` 实测,是权威口径。
  - 例:`internal/session` 测试行数占比约 97%,但 Go 1.26.4 实测**语句覆盖率仅 76.2%**。**以实测语句覆盖率为准。**
- 评级分档:🟢 强 / 🟡 中 / 🔴 中偏弱。评级同时看两个维度——**能力广度**(功能全不全)和**质量/风险**(正确性、边界、测试),二者分别说明。

## 1. 模块分层

| 层 | 包 | 职责 |
| --- | --- | --- |
| ① Agent 内核 | `query` `compact` `promptcache` `toolresult` | agent loop、tool_use 编排、stream-json、上下文压缩、prompt 装配 |
| ② 工具执行 | `tools/*`(23 子包) `sandbox` `permissions` `hooks` | 文件/Bash/Web/Task/MCP 工具 + 安全边界 |
| ③ 会话状态 | `session` `recap` `goal` `scheduler` `background` | transcript/checkpoint/rewind/fork/resume + Goal/Loop |
| ④ 交互前端 | `tui` + `web/`(React) | 终端 UI + 浏览器 Web Agent |
| ⑤ 服务化/多租户 | `server` `tenant` `quota` `storage/mysql` `telemetry` | Gin API、OpenAI 兼容、Mobile Chat、配额、计费、观测 |
| ⑥ 生态扩展 | `skills` `plugins` `mcp` `agents` `agentruntime` | skill 加载、MCP、subagent runtime |

## 2. 能力强弱总览

| 模块 | 评级 | 一句话根据 |
| --- | --- | --- |
| prompt 加载链路 | 🟢 强 | 优先级链完整、cache 四层分层(attribution/identity/static/dynamic + 1h/5m TTL)、memory 15+ 源、golden 测试密集 |
| permissions | 🟢 强 | deny>ask>allow>default 优先级正确、具体规则压过宽泛规则、危险命令即使 default-allow 也强制审批、16 个针对性测试 |
| resume 修复 | 🟢 强 | 中断/dangling tool_use/orphan result/残缺 thinking 全修复,8+ 专测——全场最成熟 |
| subagent runtime | 🟢 强 | 测试行数超过业务代码,独立 transcript/tool loop/model |
| TUI | 🟡 中偏强 | 功能 17/17 齐全;渲染稳定性经 DisplayTimeline+RenderBudget 两层重构已收敛,191 测试全绿 |
| compact 自动压缩 | 🟡 中 | 结构化摘要 + 硬事实追加 + tool-pair 保护做得好;但单级无分层、thinking 丢弃、手动 /compact 纯拼接 |
| sandbox | 🟡 中 | Linux 强(真 bwrap)/macOS 中/Windows 弱;seccomp 不含 socket、断网只靠 `--unshare-net` |
| web-agent | 🟡 中 | 后端 runner/权限阻塞扎实;前端单体巨石、markdown 弱、sub-agent 进度零渲染 |
| transcript / checkpoint | 🟡 中 | v1/v2 均已落地(v2 消息图 + 非破坏 rewind 默认开启);仍存:损坏零容忍、checkpoint 名不副实 |
| closure gate 闭环 | 🟡 中(偏脆) | 真接入主循环、解决假完成;但 1759 行纯关键词启发式,误伤面结构性偏大 |
| **quota 配额** | 🔴 中偏弱 | **唯一有真实代码缺陷的模块**:Settle 不抗 panic 会永久锁死租户;fail-open 文档承诺但代码缺失 |

## 3. 逐模块详评

### 3.1 prompt 加载链路 — 🟢 强

- system prompt 优先级链完整:`effectiveSystemBlocks`(`query.go:3919`)实现 `OverrideSystemPrompt > CoordinatorPrompt > MainThreadAgentPrompt > SystemPrompt > default`。
- cache breakpoint 是真实工程:`buildSystemPromptBlocks`(`query.go:6764`)→ `splitSystemPromptPrefix`(`query.go:6781`)切成 attribution/identity/static(global-cached)/dynamic 四类;`cacheControlForScope`(`query.go:6868`)支持 1h/5m TTL,`shouldUsePromptCache1hTTL`(`query.go:6885`)带 user-eligible + querySource allowlist 门控。
- memory 加载覆盖 15+ 源(`~/.claude/CLAUDE.md`、managed memory、project `go-e2e.md`、`CLAUDE.md`、`MEMORY.md` index、`AGENTS.md` fallback 等),`AGENTS.md` fallback 有 per-directory 门控。
- **结论**:扎实强项,剩余 gap 是逐字文案级别(system/auto_memory prompt 比原版短约 1/3)。

### 3.2 permissions — 🟢 强

- 规则优先级 `deny > alwaysAsk > allow > defaultMode`(`policy.go:59-93`)正确。
- 具体规则压过宽泛规则(`matchBestAllow` `policy.go:247-264`),即使宽泛规则更靠前。
- 宽泛 allow + 危险命令兜底(`policy.go:71-73`):命中通配 allow 但被分类器判高危时强制转审批;精确 allow 才放行。
- 危险命令即使 `defaultMode=allow` 也触发审批(敏感路径 `policy.go:442-470`、26 条 bash + 11 条 PowerShell 正则 `policy.go:477-521`)。
- **潜在弱点(非绕过)**:危险命令识别是正则黑名单,可被未覆盖变体(base64+eval 等)绕过,但真正隔离靠 sandbox 兜底。三块安全模块里最健壮。

### 3.3 resume 修复 — 🟢 强(全场最成熟)

- `MessagesFromTranscriptWithReport`(`query.go:2028`)+ `sanitizeResumeEntriesWithReport`(`query.go:2104`):dangling `tool_use` → 注入 synthetic error result(避免 API 400);orphaned `tool_result` → 丢弃计数;尾部残缺 thinking → `dropInvalidTrailingThinking`(`query.go:2252`)。
- 区分 `interrupted_turn` / `interrupted_prompt`,`ResumeReport` 上报 UI。
- **8+ 专测**(`query_test.go:8058-8116`)。

### 3.4 TUI — 🟡 中偏强(功能强,渲染稳定性已收敛)

- **功能广度:强**。17/17 核心功能齐全(流式、slash、权限审批、工具面板、subagent 嵌套进度、usage、图片粘贴、鼠标模式、resume/rewind 选择器、todo、away recap、后台任务轮询、markdown 表格),app.go 415 函数 / 191 测试。
- **渲染稳定性:曾长期不稳,现已收敛**。反复返工的四个表象(视口重叠/行高错/渲染预算/底部遮挡)是**同一根因**:
  - 根因 A:没有统一的"真实可见宽高预算"(各组件各用一套宽度假设)。
  - 根因 B/C:显示顺序由 `m.messages + m.toolActivity + m.liveDisplayBlocks` 三套可变状态拼接 + live 层与 transcript 层双渲染路径分叉。
- **已系统性解决**:`RenderBudget`(`app.go:8242`,三档降级)+ `DisplayTimeline`(`display_timeline.go`,不可变 segment)两层架构;191 测试全绿,4 个真实 session replay 通过。
- **遗留**:架构 Phase 5"删旧兼容层"未执行(`m.toolActivity`/`m.liveDisplayBlocks` 仍在 `app.go:507-508`,技术债非 bug);欢迎页 mascot 美术未定稿(审美非 bug);app.go 单文件 8507 行过大(可维护性)。
- **结论**:"TUI 差"不成立——是**高复杂度模块经密集迭代后趋于稳定**,不是能力弱。

### 3.5 compact 自动压缩 — 🟡 中

- **做得好**:结构化 8-heading 摘要(`compactor.go:166-183`)+ runtime 硬事实无条件追加(`compactor.go:210-216`)+ tool_use/result 配对保护(`adjustStartForToolPair` `compactor.go:292-304`)+ capability 事实穿过 resume 重建待办 gate(`query.go:5194`)+ 熔断冷却。超出多数"复刻玩具"。
- **缺陷**:
  - 只有单级压缩,**无 microcompact/分层**(全项目 grep 无 `microcompact`)。
  - thinking 压缩时直接 `[thinking omitted]` 丢弃(`compactor.go:189`),图片/文档同样丢弃。
  - token 是自研粗算(`tokens.go:74-92`),非真实 tokenizer,触发点可能偏差。
  - 事实抽取硬上限(files 40 / commands 30),超出静默丢弃(`facts.go:58-73`)。
  - **手动 `/compact` 远弱于 auto**:纯字符串拼接 + 12KB 硬截断(`store.go:878-917`),不调 LLM、不抽事实、可能切断 tool 配对。
  - 熔断/冷却路径**无单元测试**。
- **可优化方向**:分级/microcompact、手动路径升级为 LLM 摘要、保留关键 thinking、补熔断测试。

### 3.6 sandbox — 🟡 中(Linux 强 / macOS 中 / Windows 弱)

- **两道独立防线**:防线一(静态写路径/网络策略检查 `CheckShellCommand`,无条件执行);防线二(OS 级沙箱 `PrepareShell`,真的被 `exec` 执行)。
- **Linux(强)**:真 `bwrap`,`--ro-bind / /` 整根只读 + 逐个 `--bind` 放开写(`runtime.go:160-167`)、`--unshare-pid`、seccomp BPF 黑名单(`seccomp.go:105-156`)。symlink 逃逸有校验。
- **macOS(中)**:Seatbelt `(allow default)` 起手,只收紧写(`runtime.go:371-393`)——**不隔离网络**。
- **Windows(弱)**:`default` 分支返回裸 `cmd.exe`(`runtime.go:530-532`),**无 OS 级隔离**,只有静态检查。
- **能力/预期差(应文档化而非当 bug)**:seccomp 黑名单**不含 socket/connect/execve**,断网只靠 `--unshare-net`(仅 `NetworkDisabled=true` 时加);macOS 不隔离网络;Windows 无 OS 沙箱。
- **32% 覆盖是合理低覆盖**(OS 级隔离依赖真机,CI 难测;静态检查部分测得扎实)。

### 3.7 web-agent — 🟡 中(后端强,前端粗)

- **"差劲"判断不成立**:有真 runner(`runAgentTaskMessage` `server.go:4147`)、真权限阻塞(`server.go:4837`)、真流式修复、真 i18n、真测试文件、清晰 session 分层。
- **半截回复根因**:事件读取 `ORDER BY created_at LIMIT 200` 截断(不是模型生成截断)。**P0+P1 已彻底修好**(游标 `id>?` `repository.go:443` + completed result 兜底 `WebAgentPage.tsx:2695`),改法正确。
- **真缺口(仍未解决)**:
  - **sub-agent 进度前端零渲染**,后端也没把 `nested_agent_progress` 落 task event(文档标 P0 却未完成)。TUI 有完整聚合卡片,Web 端为 0。
  - 实时性仍靠 **1 秒 DB 轮询**(P2 live broker 未做),delta 无聚合(P3 未做,DB 写放大)——**讽刺**:同项目 mobile chat 有真 broker(`mobile_streams.go`),未复用。
  - markdown 无表格/链接/图片(手写 `MarkdownLite` `WebAgentPage.tsx:1528`)。
  - 前端 `WebAgentPage.tsx` **3522 行单体**、无路由、无全局状态、`@assistant-ui/react` 死依赖。
- **文档"已完成"标注比代码乐观**(尤其 sub-agent P0、codex-grade 全系;codex plan 审查的 `WebAgentWorkbench.tsx` 已被删除重写)。

### 3.8 transcript / checkpoint — 🟡 中

- **transcript 做得好**:JSONL append-only;格式检测 5 种(`transcript_format.go:22`)+ resume schema 隔离(放行 v1/v2、拒绝 native/mixed/unknown `transcript_format.go:56`)+ `~/.go-claude` 路径隔离(P0 已交付,4 专测)。**v2 消息图已落地并默认开启**(P2 阶段 A–F):append-only 树 + `parent_id`/`branch_head`、leaf-walk resume、非破坏 rewind/redo、branches/compare、messageId 键控文件历史、原版 CC import;详见 `docs/transcript/transcript_v2_message_graph_and_nondestructive_rewind_plan.md`。
- **transcript 缺陷**:
  - **损坏容忍为零**:`Load`(`store.go:221-239`)遇第一个坏 JSON 行直接整会话报废,断电/半行写入无兜底,且无测试。
  - 跨 project 的 rewind/fork **不校验 cwd**(全走全局 `List()`)。
- **checkpoint 缺陷(用户怀疑成立)**:
  - **名不副实**:只 append 一条 JSONL 书签(`store.go:359-371`),**不存任何状态快照**,回退能力全靠独立的 `file_change` 条目链。
  - **file_change 只覆盖 go-claude 工具的编辑**;Bash 直接 `rm`/`>` 或外部进程改的文件,**rewind 无法还原**(git-conflict 场景正是踩此)。
  - rewind/fork 用 `O_TRUNC` 重写(`store.go:682`),**无原子性、无并发锁**(session 包 `grep Mutex` = 0)。
  - fork 不隔离文件系统。
- **checkpoint 做得好**:每轮自动 checkpoint(`query.go:1221`)、代码/对话三态分离回退(`store.go:421-431`)、TUI/CLI 三模式完整且有测试。核心逻辑没写错,是"有边界漏洞"非"坏了"。

### 3.9 closure gate 闭环 — 🟡 中(偏脆)

- **机制**:自研 Task Closure Engine(`closure_gate.go`,1759 行),把 prompt 分 L0-L5 六级;两个真实拦截点接入主循环——`completionGate`(`query.go:1512`,剥离过早的假完成)+ `preToolClosureGate`(`query.go:1580`,阻断危险工具、auto-preflight)。
- **解决真问题**:LLM 四类假完成(没读就总结、搜索失败下全局结论、改了不验证说 done、没跑测试却声称 passed)。有 real-CLI acceptance 背书。
- **偏脆**:claim 检测全靠中英文关键词字符串匹配(`finalTextClaimsAuditSynthesis` `closure_gate.go:733` 匹配"唯一/整个/全局/所有/审计"等极宽泛词),误伤率结构性偏高;shell 副作用分类是 best-effort。反复出的 recovery/fix-plan(`2>/dev/null` 被误判等)证明"规则漏一个补一个"。
- **定性**:是"用工程手段补 Go 模型行为差距"的补偿层,净正向但复杂度高、需持续维护,不是护城河。

### 3.10 quota 配额 — 🔴 中偏弱(唯一有真实代码缺陷)

详见专项方案 [../tenant/quota_settle_defer_fix_plan.md](../tenant/quota_settle_defer_fix_plan.md)。三个缺陷均已代码核实:

1. **Settle 不经 `defer`(可用性事故)**:`/query`(`server.go:518`)、mobile streaming(`mobile.go:850-919`)在业务函数返回后直接调 Settle。若 `StreamQueryFunc` 或其后代码 panic,`MemoryStore.Settle` 的 `concurrent--`(`quota.go:287-289`)不执行 → 并发计数只增不减 → 租户被 `ErrConcurrentLimitExceeded` **永久锁死**;`MemoryStore`(默认部署)无 TTL(`quota.go:225-232`),必须重启进程才能恢复。Redis 有 24h TTL 自愈(`redis.go:68,181`)。
2. **fail-open 逃生阀缺失(文档撒谎)**:文档承诺 `GOLANG_CLAUDE_CODE_QUOTA_FAIL_OPEN`,`grep FAIL_OPEN` **代码里根本不存在**。Redis 一挂,所有启用配额的租户全部 429/402。
3. **7% 覆盖 = 计费核心关键路径裸奔**:默认的 `MemoryStore` Reserve/Settle 零测试;Settle 的 token 回补差额(`quota.go:292-296`)无测试;fail-closed 传播无测试;并发防超卖无测试。
- **做得对的**:核心 check-and-reserve 在 Memory(同锁内)/ Redis(同 Lua 内)无 check-then-act 竞态,不能被并发绕过。

## 4. 最该优先的问题(不在初始怀疑清单里)

| 优先级 | 问题 | 严重性 | 状态 / 成本 |
| --- | --- | --- | --- |
| ✅ 已修复 | quota Settle 不抗 panic + fail-open 缺失 | 正常路径就能触发生产事故(租户永久锁死);计费/安全核心 | 2026-07-15 已实施(`quotaSettler` + fail-open,覆盖率 7%→88%),见 [quota_settle_defer_fix_plan.md](../tenant/quota_settle_defer_fix_plan.md) 第 8 节 |
| 🟠 P1 | web-agent sub-agent 进度零渲染 | TUI/Web parity 最大功能缺口 | 待修,中(前后端各补一段) |
| ✅ 已修复 | git rebase 死锁:`bash.go` 强制 `GIT_EDITOR=true`/`GIT_SEQUENCE_EDITOR=true` 并剔除交互式 editor | agent 卡死循环重试十几分钟 | 595ee33(2026-07-15,带测试) |
| ✅ 已修复 | transcript 盲搜:系统提示 Session Guidance 增加 `session show` 引导 | agent 花 6-7 分钟盲搜文件系统 | 595ee33(2026-07-15,带测试) |

> 时间线说明:本报告初稿基于 `8cc1682`(审计时两个 pending bug 均未修复);其后 `595ee33`(2026-07-15 23:11)已修复 git rebase 死锁与 transcript 盲搜。表格状态列以 `595ee33` 后为准。

## 5. 修复优先级建议

1. ~~先做 quota(P0):Settle 改 defer + recover、实现 fail-open、补 MemoryStore/Settle 单测~~ **已于 2026-07-15 完成:`quotaSettler` 覆盖四个泄漏点、fail-open 逃生阀、quota 覆盖率 7%→88%。**
2. ~~清两个 pending 一行改动:`GIT_EDITOR` 注入、系统提示补 `session show` 提示~~ **已在 `595ee33`(2026-07-15)完成,带测试。**
3. **transcript 损坏容忍**:`Load` 对坏行改为跳过+计数(对齐 `Search`/`transcriptTitle` 的 `continue` 行为),而非整会话报废。
4. **web-agent sub-agent 进度**:后端落 `nested_agent_progress` 事件 + 前端聚合卡片;顺带评估复用 mobile `MobileStreamRegistry` broker 替代 1 秒轮询。
5. **compact 分级**(收益中):手动 `/compact` 复用 auto 的结构化摘要;评估 microcompact。

## 6. 一句话结论

- **强项(护城河)**:prompt 装配、resume 修复、permissions、subagent runtime——真下功夫且测透。
- **软肋**:quota(有真 bug + 文档撒谎)> transcript v2/损坏容忍 + checkpoint 对 Bash 改动无能为力 > web-agent 前端产品化 + sub-agent 零渲染 > compact 分级/手动路径。
- **认知纠偏**:TUI 不差(是难);web-agent 后端不差(前端粗);初始怀疑清单外的 **quota 才是最危险的**。

## 附:证据来源

- 覆盖率:`go test ./internal/session -cover`(Go 1.26.4 实测 76.2%);测试行数占比为 `find + wc` 粗算。
- 每条结论的 `file:line` 见正文;跨模块审计基于 2026-07-15 的六路并行代码审计。
- 相关既有文档:`docs/testing/context_compaction_fidelity.md`、`docs/web_agent/tui_display_parity_gap_analysis.md`、`docs/tui/tui_display_timeline_architecture_plan.md`、`docs/transcript/transcript_schema_isolation_and_resume_plan.md`、`docs/pending-fixes/git-conflict-and-transcript-locating/`、`docs/tenant/tenant_quota_usage_technical_plan.md`。
