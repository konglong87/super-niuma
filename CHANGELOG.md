# Changelog

## v0.5.0 — 2026-10-23

### 新功能

- 编码模式支持基于项目 Markdown 记忆文件的有界词法召回；改进中文短查询、中英混合词元和英文词边界匹配，并按 frontmatter 与索引提示排序。无需租户记忆数据库或向量服务。

### 修复

- 修复配置目录位于符号链接下、项目记忆目录尚不存在时首次 Write 被拒绝的问题，同时保留路径逃逸防护。
- frontmatter 仅将独占一行的未缩进 `---` 视为结束标记；合法 YAML 块标量中的缩进分隔符不再截断预览，伪分隔符不再误解析。
- 将 MEMORY.md 的每个索引标题和 hook 绑定到对应链接；忽略 HTML 注释，重复别名不再叠加弱匹配或把无关记忆带入上下文。

### 维护与兼容性

- 升级 `golang.org/x/net` 等相关依赖以处理安全公告；Go 模块最低版本更新为 1.26.0，源码构建需 Go 1.26 或更高版本。
- 更新 CI 与质量维护工作流，并扩充文件记忆生命周期、检索边界和真实模型验收覆盖。

### 验收边界

- 项目记忆验收包含真实模型读写、更新、遗忘、隔离和召回测试，以及原生桌面新会话冷启动写入/召回验证；它不代表任意语义改写都保证自动命中。
- 文件记忆的 Forget 不会删除独立的桌面会话历史或任务标题；默认 GLM-5.2 路由仍未切换到新网关。
- 桌面 Release 的 macOS 安装包是否签名/公证取决于仓库发布 Secrets；Android/iOS 不包含在本次桌面发布中。

## v0.3.5 — 2026-10-15

### Computer Use milestone

- 完成 Computer Use runtime 的原子 turn result、receipt/observation/screenshot 一致性、execute_turn bridge 和禁止 replay 安全语义。
- 当前 macOS 单屏 Developer Preview 完成 deterministic/native gates，以及一次真实 `gpt-6-sol/high` `launch → click → Command+A → type → send → wait → reply screenshot → Stop` 闭环。
- runtime 生命周期与 Wails UI 解耦，显式 Stop receipt、helper/runtime supervisor 和 native pointer cue 已纳入当前桌面构建。

### Public documentation and privacy

- 更新中英文 README 和 Computer Use 使用说明，明确开发者预览范围及第二显示器、正式签名/公证/分发、跨平台 GUI 的边界。
- 修正公开使用文档中的旧 `cmd/niuma` 入口，统一为 `cmd/niuma`。
- 删除过时的验收报告，清理 tracked test fixtures 中的本机用户路径，并新增公开文档隐私/链接回归。

### Known limitations

- 项目仍处于 `0.x` 快速迭代阶段；正式 Developer ID 签名、公证、clean install/upgrade、第二块物理显示器和部分 permission/helper-fault 生命周期矩阵仍未完成。
- 运行 Agent 和 Computer Use 需要用户自行配置 Provider、凭据及 macOS Accessibility/Screen Recording 权限。

## v0.3.4 — 2026-10-13

### Public release reliability

- 发布验收在卸载前清理由桌面 Host 启动的测试进程，避免 Windows 锁定 sidecar 导致卸载残留。

## v0.3.3 — 2026-10-13

### Public release reliability

- 未配置 Apple Developer ID 时，macOS 发布流程跳过 ad-hoc nested code signing，生成并明确标注为未签名 DMG。
- 保留正式签名和 notarization 路径；配置完整 Apple Secrets 后仍执行完整签名校验。

## v0.3.2 — 2026-10-13

### Public release reliability

- 修复 Windows 安装程序卸载注册表键名与发布验收脚本不一致的问题。
- 允许未配置 Apple Developer ID 时发布未签名 macOS 安装包，并在对应验收路径中明确跳过签名校验。

本文件汇总 niuma 的重要改动；每项的详细设计与验收见对应 `docs/**` 方案文档。

## v0.3.0 — 2026-10-13

### Public release

- 发布当前主线的 Desktop、CLI、TUI 和 WebUI 使用入口，补充首次安装、Provider、MCP、FAQ 和使用场景文档。
- 增加社区入口、Issue 模板、Discussion 引导和低门槛首次贡献路径。
- 延续 v0.2.0 之后的 Computer Use、桌面端、Agent Team 和运行时可靠性改进。

### Known limitations

- 项目仍处于 `0.x` 快速迭代阶段；具体平台能力和兼容性以 Release Notes、文档和实际验证为准。
- 未配置 Apple Developer Secrets 的 macOS 构建可能需要手动允许打开。
- 运行 Agent 需要用户自行配置可用的模型服务、协议和 API 凭据。

## v0.2.0 — 2026-09-30

### 新功能与修复

- macOS Computer Use：桌面 host 管理的截图与真实输入、拖拽、窗口目标识别与定向操作，以及可拖动控制浮层。
- 自动会话编排：模型按需创建会话，桌面展示模型会话并提供 Stop；启动 ACK 恢复绑定精确 attempt，避免猜测或重放。
- 输入安全：焦点、截图有效期、窗口几何与权限检查；暂停/停止、helper 崩溃后的不确定结果处理，以及按键/鼠标释放保护。
- 权限生命周期：以桌面 host 的权限为准；记录真实撤销、失败关闭和恢复验收，以及 macOS 运行中授权状态可能延迟刷新的限制。
- 多显示器软件支持：显示器路由、负坐标、混合 DPI 和拓扑变化；物理第二屏验收延期。
- 桌面 SQLite 持久化、飞书渠道会话与工作进程协调、设置/技能管理等上一 tag 之后的改进。

### 验收和发布边界

- 已记录原生输入/安全 fixture、窗口/拖拽验收及真实模型自动启动 WorkBuddy、导航、点击“新建任务”、截图和 Stop 的独立证据。
- 此 tag 用于开源源码交付；正式 Developer ID 签名、公证、签名版本首次安装/升级和第二块物理显示器验收仍延期。不能据此承诺所有环境零干预或与其他产品完全等价。
- 本地桌面为 ad-hoc 签名构建，首次使用仍需 macOS 授权。CI 二进制产物是否发布以 Release workflow 实际结果为准。


## 2026-08-13（共享状态与权限边界收口）

- **shell 工具接入默认写保护**：Bash / PowerShell 的写路径检查此前走
  `tools.EnsureWritablePath`，只校验 workspace 边界不带默认写保护，导致
  `.niuma/settings.json`、`.git/hooks`、`.git/config` 对 shell 可写而对 Write/Edit 不可写 ——
  自提权与持久化路径。`CheckShellCommand` / `CheckPowerShellCommand` 增加必填
  `tools.SandboxConfig` 参数并改用 `EnsureWritablePathWithSandbox`；两份漂移的 deny 列表
  （sandbox 侧改名后只剩 `.claude/`）合并为 `internal/tools` 单一来源。
- **git hook 路径重定向硬拒绝**：`core.hooksPath` / `core.fsmonitor` 能在不提到任何文件路径的
  情况下达到写 `.git/hooks` 的效果，路径级 deny 看不到。两种写法都拦，其中
  `git -c core.hooksPath=X <cmd>` 是按子命令匹配必然漏掉的形式。读取与 `--unset` 放行。
- **系统关键目录纳入破坏性命令硬拒绝**：`rootishTargets` 原先只有字面量"一切"目标，
  `rm -rf /usr`、`chmod -R 777 /etc` 只靠可写根一层兜。新增 `isCatastrophicTarget` 统一
  `destructive_rm` / `broad_chmod` / `broad_chown` 三条规则，只匹配目录本身不匹配子孙。
- **破坏性命令覆盖开关留痕**：`NIUMA_ALLOW_DESTRUCTIVE=1` 关闭的是唯一一道连
  `--dangerously-skip-permissions` 都推不翻的闩，此前静默生效。现在只在它真正改变结果时
  经 `PermissionAudit` 记一条 `permission.decision`（`rule=destructive_command_override`），
  可在 `session inspect` 与 Trace Viewer 查到。
- **closure gate 不再误拦否定句**：11 个 final-text claim matcher 从整段 `containsAny` 改为
  从句级、否定在短语之前才抑制，如实报告 "I have not committed the changes" 不再被判为
  声称已提交。
- **提示词优先级措辞收口**：`# Information Priority Hierarchy` 原把 stored instructions 排在
  "level 1 alongside the user's input"，与同一 prompt 内 `# System` 的"用户当前消息永远最高"
  冲突。改为排在用户当前输入之下、项目现有代码之上，并把"与用户冲突"（有定论）和
  "两条 stored rule 互相冲突"（不定论）分开处理。
- **测试环境隔离**：`internal/config` 5 个测试此前受本机 `ANTHROPIC_BASE_URL` 等环境变量污染
  而在干净 checkout 上失败；新增共享 helper 清理全部 7 个变量，并覆盖 `product.Getenv` 的
  4 个前缀别名。
- 新增两份待办方案文档：子代理 closure gate 缺口（`docs/architecture/subagent_closure_gate_design.md`）
  与 git 工作流 gate 摩擦度量（`docs/architecture/git_gate_friction_measurement_and_plan.md`）。

## 2026-07-31（OpenAI Responses 上游 provider，第一阶段 stateless）

- 新增独立 `providerProtocol` / fallback `protocol`，显式 `openai-responses` 才进入官方
  `openai-go/v3` backend；未配置 protocol 的 `custom` / `openai-compatible` 继续使用原 Chat
  Completions SDK、dispatcher、重试和 SSE 路径。
- Responses 支持文本、图片、instructions、function tools/results、reasoning summary、JSON schema、
  usage 与 typed SSE；错误继续遵守“输出前可 fallback、输出后返回 partial 且不切换”。
- 第一阶段固定 `store=false`，不发送 `previous_response_id`。新增版本化
  `ProviderContinuationStore` 与 `provider_continuation` transcript entry，用于本地持久化和重放
  stateless encrypted reasoning；`store=true` / `previous-response-id` 配置明确报未实现。
- 已用指定真实 endpoint/model 完成 L0 Transport、L1 Core Items、strict JSON Schema，以及顶层/命名
  fallback 两条 `niuma -p` 路径验收。L2 reasoning usage 可用，但未观察到 summary delta 或
  encrypted continuation；L3/L4 仍不在本期范围。
- 真实工具流发现并修复同一 `call_id` 被兼容网关重复物化、可能导致工具重复执行的问题；同时修复
  runtime `--settings` 未整体重建 provider 配置及提前 provider 校验早于 settings 合并的问题。
  设计、真实能力边界与复验命令见 [OpenAI Responses 上游 Provider 接入设计](docs/architecture/openai_responses_provider_design.md)。

## 2026-07-30（v0.1.58-go：内部产品身份统一为 niuma）

- 项目名、Go module、CLI、发布产物、状态目录、环境变量、Redis namespace、MySQL 默认库、transcript
  schema、WebUI storage 和 APG adapter/agent ID 统一为 `niuma`。
- 架构上采用单一 canonical identity 和“新写旧读”：新增 `internal/product` 作为产品身份的单一事实源；
  `.go-claude`、旧环境变量、旧 schema、旧 WebUI key、APG alias 和 legacy metrics 继续承担迁移兼容。
- Claude Code、`.claude`、Anthropic/provider/model 等外部协议概念保持不变，避免把兼容对象误改为本项目品牌。
- 主仓库提交 `23480b3e`，APG 配套提交 `0b6cb38`；两个仓库均快进合并到 `main`，主仓库发布
  `v0.1.58-go`。
- 全量 Go 测试、vet、194 项离线验收、141 项 Web 单测、浏览器 mock/live、真实模型、真实 MySQL、
  真实 Redis 和手机真机验证通过。完整范围、命令、结果、已知 warning、回滚边界和过程复盘见
  [发布归档](docs/archive/2026-07-30_golang_cc_rename_release.md)。

## 2026-07-27（静默夹取的最后一处，也是唯一花钱的一处 · TODO-118 DONE）

`normalizeLimit` 静默夹取的最后一个落点，后果与前几处不同：前面是**丢数据**、**查不到**、
**看不见**，这一处是**重复计费与重复执行**。`message_key` 是移动端的幂等键，判断「这条请求是不是
已经处理过」的写法是 `ListMessages(ctx, sessionID, 1000)` 再在返回切片里线性查找 —— limit 超过
500 被夹到 500，于是长会话里客户端重试**找不到**已有记录：重新跑一次查询、重新写一条消息、
重新计一次量。

- **加生成列 + 索引，不走 JSON 路径查询**。`message_key` 埋在 `content_json` 的
  `$.mobile.message_key` 下，没有独立列。新增 migration `000010`，给
  `tenant_session_messages` 加 `mobile_message_key`（`STORED` 生成列）与
  `idx_session_messages_mobile_key (session_id, role, mobile_message_key(191))`。
  JSON 路径表达式用不上索引，每次幂等检查都要扫完整个会话再逐行解 JSON —— 那只是把线性扫描
  从 Go 搬进 MySQL。
- **列型是 `LONGTEXT` 而不是 `VARCHAR(n)`，理由是写入路径不能多一种失败**。`message_key` 由客户端
  提供、长度没有上限，而这张表在每条消息的写入路径上：`VARCHAR(n)` 下任何超长 key 在 strict 模式
  会让 INSERT 直接报错，存量数据里若已有超长 key，`ALTER` 的回填也会当场失败。`LONGTEXT` 装得下
  任何输入，两种失败都不存在。代价是索引必须给前缀长度（191 字符，utf8mb4 下 764 字节，同时低于
  767 和 3072 两个上限）；**相等判断比的仍然是完整列值**，前缀只用于缩小候选，所以不存在
  「前缀相同就误判为重放」。
- **新增 `MessageByKey(role, messageKey)` 定点查询**，返回 `(Message, bool, error)` 而不是
  `ErrNotFound`：这条路径上「查不到」是**预期的常态**（第一次请求本来就不是重放），用错误表达它
  容易在正常分支里把真的查询失败一起吞掉 —— 那就等于数据库出问题时静默重复执行一次。
  `role` 是必须的判别条件而非可选过滤：同一个 key 同时落在一条 user 和一条 assistant 上。
- **stream 里顺带修掉一个同族的覆盖写**。「上一次写下了 user 消息但流断了」这一支复用那条 user
  消息的轮次是对的，但 assistant 的轮次原先取自 `mobileMaxTurn(existingMessages)` —— 那就是被夹到
  500 的列表，算出来的「下一轮」是 501，而唯一键是 `(session_id, turn_index)`、写入走
  `ON DUPLICATE KEY UPDATE`，新 assistant 直接**覆盖掉第 501 条已有消息**（TODO-116 的同一族后果，
  落在这个分支上）。改成问数据库要最大轮次。这不是顺手改：删掉那个列表之后它没有别的来源。
- **断言的是「没有重复执行、没有重复写入」，不是状态码**。重放走通时状态码本来就是 200，
  只看响应抓不到重复计费。测试断言 `StreamQueryFunc` 的调用计数为 0、`UpsertMessage` 一次都没被
  调用，以及回放的是哪一条消息（`message_id`）。
- **`migration 未经真实 MySQL 验证`**。本机既无 MySQL 也无 docker，`000010` 一行都没有真跑过。
  `internal/storage/mysql/schema_test.go` 的既有静态校验手段（`readMigration` + `assertContains`）
  可以复用，本批复用了它 —— 但它只比对 migration 的**文本**。具体证明不了三件事：
  ① MySQL 接受这段 DDL（最没底的是「`STORED` 生成列上建前缀索引」）；② 回填在存量数据上不失败；
  ③ 查询真的走了 `idx_session_messages_mobile_key` 而不是退化成扫全会话。已登记为 **TODO-130**。
  失败方向是响的而不是静默的：列不存在时查询报错 → 500，不会悄悄重复计费。
- **补了一条把 SQL 和 Go 钉在一起的测试**，因为这是本批唯一「全绿也可能已经坏掉」的缝：生成列的
  取值路径写在 SQL 里，`message_key` 的写入位置在 `mobileMetadataJSONWithAttachments` 里，两边没有
  共享定义。路径写错（漏掉 `$.mobile` 这一层是最顺手的错法）会让生成列在每一行都是 NULL、所有
  幂等查询漏判 —— 而 handler 测试走夹具、sqlmock 只钉 SQL 形状，两边都看不见。
  `TestMobileMessageKeyMigrationPathMatchesTheWriter` 从 migration 里抽出路径，按它走一遍**真实
  写入产生的** JSON，走不到就报红。
- **验证**：三条 handler 断言先红 —— `stream ran 1 more times on replay, want 0`（stream 与
  regenerate 各一次）与 `resume wrote a duplicate user message`。变异检查逐处把实现退回
  `ListMessages(…, 1000)` + 线性扫描，三处各自复现对应的红，而既有的短会话幂等测试
  `TestMobileChatMessageKeyIsIdempotent` **三次都是绿的** —— 这正是这个 bug 能长期存在的原因。
  另把 migration 的 JSON 路径改坏成 `$.message_key`，上面那条新测试与静态校验各自报红。
  `go test ./... -count=1` 与 `-race` 全绿。
- **夹具诚实性自检**：把 resume 那一步的 `role` 实参改坏（`"user"` → `"assistant"`），忠实夹具报红；
  再让 `fakeTenantService.MessageByKey` 无视 `role`（复刻上一批 `ListMessages` 夹具犯的错），
  **正确实现下 3 条测试有 2 条转红** —— 也就是夹具对 `role` 的忠实是承重的，不是装饰。
  如实记一句：预想的「坏实现 + 马虎夹具 = 假绿」没有出现，因为无视 `role` 的夹具会把**第一次**
  查询一起带坏，于是仍然红，只是红在另一条断言上；regenerate 那条测试对 `role` 忠实度不敏感
  （夹具里只有一行带那个 key）。

## 2026-07-26（同一个静默夹取的剩余两处：查不到与看不见 · TODO-117 / TODO-119 DONE）

接着上一批（TODO-115/116）继续收 `normalizeLimit` 静默夹取的落点。上一批修的是**丢数据**的两处，
本批修的是**查不到**和**看不见**的两处。共同的形状是：把 `ListMessages(ctx, sessionID, 1000)`
当"整个会话"用，然后在返回的切片里线性查找 —— 而 limit 超过 500 会被静默夹到 500。

- **cancel/regenerate 不再对存在的消息报 404（TODO-117）**。这两条路径要的一直是「按 id 取这一条」，
  却先拉一页回来再筛。新增 `GetMessage`（按 `(tenant_id, user_id, session_id, id)` 定点查）与
  `PreviousUserMessage`（`role = 'user' AND turn_index < ? ORDER BY turn_index DESC LIMIT 1`）。
  **没有改成走 `ListAllMessages`**：这两条路径只要一条消息，把 5000 条消息连 `content` 一起拉进
  内存是纯浪费。**归属校验没有随着换查询丢掉** —— 消息 id 全表唯一，光按 id 查也查得到，
  等于把越权读打开，所以 `tenant_id`/`user_id` 仍在 WHERE 里，并在 service 层断言解析出来的
  那对 id 真的传下去了。
- **`regenerate` 必须两处都改，只修一半比 404 更糟**。目标查询修好之后，夹取窗口里仍然有 user
  消息（第 499 条），`mobileFindPreviousUserMessage` 会挑中它当成第 600 条的提示 ——
  **静默拿错的提示重新生成**，状态码还是 200。变异检查复现过这一幕（`Prompt:msg-499`，
  应为 `msg-599`），所以测试断言的是 Prompt 而不只是状态码。
- **会话详情的 `latest_recap` 不再取自最旧的 500 条（TODO-119）**。原先是升序 + 夹取，求出来的是
  「最旧 500 条里的最新」，长会话里新生成的 recap 根本不出现。新增 `ListRecentMessages`
  （`ORDER BY turn_index DESC LIMIT n`，返回前翻回升序，因为本仓储的 `[]Message` 一律按
  `turn_index` 升序，从尾部往前找的调用方依赖这个不变量），窗口 200 条（约最近 100 轮往返）。
- **这一处引入了一个新盲区，如实记下来**：窗口之外的 recap 看不见 —— 620 条消息的会话如果只有
  一条第 3 轮写的 recap，新读法返回空，旧读法反而能看到。判断是值得：字段叫 `latest_recap`，
  一条 600 轮之前的 recap 不是"最新"，当成当前摘要展示比不展示更糟；且旧盲区随会话增长
  **永久恶化**，新盲区只取决于"多久没生成过 recap"。彻底消掉盲区的写法是
  `WHERE role = 'recap' ORDER BY turn_index DESC LIMIT 1`，已登记为 **TODO-125**。
- **夹具的忠实性是这批 bug 能长期存在的原因，本批专门验证了这一点**：把 recap 窗口改坏成 1，
  忠实夹具（如实按 limit 取尾部）报红；而让 `fakeTenantService.ListRecentMessages` 无视 limit
  返回全部行之后，**同一个坏实现变成绿的**。这正是上一批 `ListMessages` 夹具犯的错。
- **验证**：三条断言先红，红的输出是 `status=404 body={"error":"message not found"}`（cancel、
  regenerate 各一次）与 `latest recap came from the oldest 500 messages`。变异检查逐处把实现退回
  `ListMessages(…, 1000)`，对应测试各自复现红。`go test ./... -count=1` 与 `-race` 全绿。
- **sqlmock 证明不了什么，说清楚**：本机既无 MySQL 也无 docker，仓储层只能用 sqlmock 断言生成的
  SQL 形状（确实按 id 定点查、确实是 `ORDER BY turn_index DESC LIMIT ?`、WHERE 里确实还带着
  `tenant_id`/`user_id`）。sqlmock 只回放我们发出的语句、**不校验语法、没有 MySQL 语义**，而且
  本仓用的是 `QueryMatcherRegexp`（子串匹配，非锚定）。所以这些用例证明「我们发出的 SQL 是这个
  形状」，**不能证明「这条 SQL 在 MySQL 上跑得通」**，也不能证明查询计划没有退化成全表扫。
- **没有碰 TODO-118**（`mobileMessageStreamGin` 的 `message_key` 幂等重放，仍扫被夹取的列表）：
  `message_key` 存在 `content_json` 里而不是独立列，定点查询要么走 JSON 路径要么加生成列 + 索引，
  涉及 migration，需要先决策。`regenerate` 内同源的那一处也照原样留着，只是因为上面两处改成定点
  查询后该列表只剩这一个分支在用，故改为按需读取。

## 2026-07-26（后台任务注册表的写入既不原子也无锁 · TODO-111 DONE）

登记在 TODO-111 的是「`TestBashToolRunInBackgroundPersistsJobAndLogs` 偶发 `unexpected end of JSON input`」，
记成 P3 flaky test。**这个登记低估了问题**：出问题的不是测试夹具，而是
`~/.go-claude/background_sessions.json` —— 真实用户的后台任务注册表。它是一个会损坏持久数据的并发 bug，
本批按 P1 处理。

- **根因一：`O_TRUNC` 覆写。** `Store.save` 用 `os.WriteFile` 直接覆写注册表，而 `O_TRUNC` 会**先把文件清空**
  再写入新字节。任何撞进这个窗口的读者拿到的是半个文件（常见是零字节），`json.Unmarshal` 于是报
  `unexpected end of JSON input`。原登记猜的是「测试等待条件不足」，方向不对 —— 等待多久都没用，
  问题在写入方。
- **根因二：无锁的 read-modify-write。** `Store` 是只有一个 `Root` 字段的值类型，没有任何锁；
  而 `update` 和 `CreateWithOptions` 都是「`List()` 读全量 → 改 → `save()` 写全量」。
  两个并发调用会**互相覆盖**，丢的是任务记录和状态更新。这一半原登记完全没提到。
  并发是真实存在的而非理论风险：`internal/scheduler/scheduler.go:739` 在 goroutine 里另建一个
  `background.Store{Root: ...}` 调 `RecordLoopRun`。
- **写入改为 write-temp-then-rename。** 同目录临时文件 → 写 → `Sync()` → `os.Rename` 覆盖。
  rename 在同一文件系统内是原子的，读者只能看到旧的完整文件或新的完整文件，不存在中间状态。
- **read-modify-write 整段套 `flock`**（新增 `internal/background/lock.go`）。**只锁 write 半段不解决丢更新**，
  所以锁覆盖的是 read 到 write 的整个区间。选 flock 而不是只上进程内 mutex，是因为这个文件
  **跨进程共享** —— 同时开两个 go-claude 进程时进程内 mutex 完全无效，而那是真实场景。
  锁文件单独一个（`background_sessions.lock`），**不锁数据文件本身**：rename 会换掉 inode，
  锁在旧 inode 上的持有者和锁在新 inode 上的下一个写者不互斥。
- **`List()` 不加锁**，这是有意的：rename 已经保证读者看不到撕裂状态，给读路径加锁是没必要的复杂度。
- **flock 之外还留了一层进程内 mutex**，按 lock 路径存在包级 map 里（`Store` 是值类型，
  字段里的 mutex 每份拷贝都是新的、什么也守不住）。变异检查显示**在 darwin 上 flock 单独就够**
  （flock 附着于 open file description，同进程两次 `OpenFile` 也互斥），保留它的真实理由是
  **`!unix` 构建下没有文件锁**，那里 mutex 是唯一防线 —— 这一点是实测出来的，不是推测。
- **不做 Windows 的跨进程锁，理由写在代码里（TODO-120）。** `lock_other.go` 是 `//go:build !unix` 的 no-op：
  写入在那里**仍然原子**，进程内**仍然串行**，缺的只有两个进程之间的互斥。Windows 的对应物 `LockFileEx`
  语义差得不少（强制锁而非劝告锁、字节区间而非整文件），本项目在 darwin 开发、CI 在 ubuntu，
  这个分支在这里无法验证 —— 写一个看起来合理但没跑过的 Windows 分支比一个写清楚的缺口更糟。
  `GOOS=windows go build ./...` 改动前后都通过，可构建性没有回归。
- **变异检查（四项，都跑了）**：(a) rename 退回 `os.WriteFile` → 只有 `TestStoreListNeverReadsPartialFile`
  红（`after 2 clean reads: unexpected end of JSON input`），另三条仍绿；(b) flock 换成 no-op →
  只有 `TestStoreConcurrentCreatesAcrossProcesses` 红（`registry kept 10 jobs, want 32`），三条进程内测试仍绿；
  (c) 只去掉进程内 mutex、保留 flock → **四条全绿**，所以 mutex 在 darwin 上不是承重件（如实记录）；
  (d) flock 与 mutex 同时去掉（等价于 `!unix` 构建）→ 两条进程内测试红（`kept 2 jobs, want 16`）。
  每项的对应关系都是**一对一**的，不是「一改就全红」。
- **原始症状**：`go test ./internal/tools/bash -run TestBashToolRunInBackground -count=20`（`-race` 同跑）
  不再复现。因果链核实到具体代码：`internal/tools/bash/bash.go:290` 的 goroutine 调 `store.Finish`，
  与测试主 goroutine 轮询的 `store.Find`→`List()` **同进程并发**，读者正撞在 `O_TRUNC` 窗口上 ——
  与 `TestStoreListNeverReadsPartialFile` 复现的机制是同一个，所以这不是「顺带好了」而是同一个根因。

## 2026-07-26（一个静默夹取造成的两处丢数据 · TODO-115 / TODO-116 DONE）

上一批（TODO-106）把 mobile 会话 fork 收进事务，收口时发现同一个 handler 还有一处更隐蔽的问题：
**根因是 `normalizeLimit` 把任何 >500 的 limit 静默夹到 500**，而 mobile 侧有五处把
`ListMessages(ctx, sessionID, 1000)` 当"整个会话"用。本批修掉其中两处 —— 会丢数据的那两处。

- **fork 不再静默截断（TODO-115）**。超过 500 条消息的会话原先被复制成一个悄悄缺了尾巴的分支，
  还照样返回 200 和 `copied_messages: 500`。而且不止少了消息：`until_turn` 缺省时从被截断的列表里
  取最大轮次，所以**分支点本身**也是错的，还被当成事实写进响应；`until_message_id` 指向第 500 条
  之后的消息时报 404，而那条消息其实存在。新增 `ListAllMessages`：多取一条判断"还有更多"，
  超限报 `ErrTooManyMessages` 而不是返回一个截断切片，端点映射成 422 —— 响亮失败而非静默少复制。
- **上限从 500 提到 5000，并且读写共用一个数**。`maxForkedMessages` 导出为 `MaxForkedMessages`。
  500 本来是列表分页的读上限，跟"一次 fork 能有多大"没有关系；沿用它会让超过 500 条的会话
  直接不能 fork。**没有改成库内 `INSERT ... SELECT`**（那才是真正去掉上限的做法）：本机没有 MySQL
  也没有能跑 MySQL 方言的进程内引擎，sqlmock 只回放我们发出的语句、不校验语法，手写的
  `INSERT ... SELECT` 在提交前无法验证，一旦有错就是整个 fork 端点不可用。
- **修掉一个静默覆盖用户消息的 bug（TODO-116）**，比 fork 那条更糟且不在 fork 路径上：
  `mobileNextTurns` 靠扫同一个被夹取的列表找最大 `turn_index`。在 620 条消息的会话里它算出的
  "下一个"轮次是 501 —— 而 501 早就存在。消息表唯一键是 `(session_id, turn_index)` 且写入走
  `ON DUPLICATE KEY UPDATE`，于是**新消息直接覆盖掉一条已有消息**。改为 `MaxMessageTurn`
  （`SELECT COALESCE(MAX(turn_index), 0)`）把最大值交给数据库。
- **顺带修掉了"为什么这个 bug 一直躲过测试"**：`fakeTenantService.ListMessages` 原来忽略 limit
  直接返回全部行 —— 夹取不建模，夹取造成的 bug 就永远测不出来。现在夹取被如实复刻，
  既有测试全部不受影响。
- **验证**：四条断言全部先红后绿，红的输出分别是
  `branch silently truncated: copied_messages:500 / until_turn:500`、`status=200`（应为 422）、
  `status=404 message not found`、`new message reused turn 501, overwriting an existing row`。
- **同族三处已登记未修**：cancel/regenerate 对第 500 条之后的消息报 404（TODO-117，要的是按 id
  定点查询，不是把 5000 条拉进内存）；`message_key` 幂等重放在长会话里失效导致**重复执行与重复
  计费**（TODO-118，`message_key` 存在 `content_json` 里，定点查询需要先决定走 JSON 路径还是加
  生成列 + 索引）；会话详情的 `latest_recap` 取自**最旧**的 500 条（TODO-119，需要倒序取最近 N 条）。
- **另外定位了 TODO-111 这个既有 flaky 的根因，并更正了它的位置判断**：不在 `internal/tools/bash`，
  而在 `internal/background` 的 `Store.save` —— 用 `os.WriteFile`（先截断再写，非原子），
  而 `Store.List` 直接 `ReadFile` + `Unmarshal`。**这不只是测试问题**：该 store 跨进程共享
  （detached runner 写、CLI/TUI 读），且 `update` 是整数组的读改写，并发下会整条丢 job。
  本批没修（另一个子系统，且需要并发测试）。

## 2026-07-26（module path 与仓库地址对齐 · TODO-088 PARTIAL）

- **`go.mod` 的 module path 从 `github.com/konglong/golang-claude-code` 改为
  `github.com/konglong87/niuma`**，与仓库实际地址一致。全仓机械替换 852 处 / 279 文件，
  除 `go.mod` 首行外全部是 import 语句、`build.sh` 的 `-ldflags -X` 路径与文档引用。
- **`cmd/niuma` 目录名与 `bin/golang-claude-code` 产物名未改**：它们不属于
  module path。替换用的搜索串带完整 `github.com/konglong/` 前缀，只搜 `golang-claude-code`
  会连目录名一起改掉、直接编译不过。
- **`go install` 仍然不可用，本条只记 PARTIAL。** 原先三条阻塞项只解掉一条，
  `replace github.com/muesli/termenv => ./third_party/termenv`（`go install pkg@version`
  不应用 replace）和 `go install` 传不了 `-ldflags`（`--version` 会是 `dev`）都还在。
  README / `scripts/install.sh` / 审计文档里描述这个 bug 的四处文字已按事实改写，
  没有写成「已修复」。
- 验收：`go build ./...`、`go test ./... -count=1`（75 包全绿）、`go vet ./...`、
  `gofmt -l .`、`go mod verify`、`git diff --check` 全过；`go.sum` 零改动（module 自身路径
  不进 go.sum，有改动就说明替换越界了）。`scripts/build.sh` 产物 `--version` 输出
  `v0.1.52-go-1-g15261b71-dirty` 而非 `dev` —— 这证明新的 `-X` 路径解析正确，
  路径错会静默回落成 `dev`。

## 2026-07-26（收掉最后一份 placeholder 私有副本 · TODO-110 DONE）

**行为零变化**，纯粹是拆掉一个「下一次分叉」的温床。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--todo-1102026-07-26最后一份-placeholder-私有副本)。

- **`internal/tui` 不再自带 placeholder 集合**。TODO-101 收完后全仓只剩它一份私有副本。
  **它不是活着的 bug** —— 两份函数体归一化函数名后 `diff` 为空（各 490 字节），删掉不可能改变任何显示结果。
  删它的理由是**结构不是当前值**：一份「恰好还一致」的副本，正是 TODO-101 那两处分叉长出来的同一个形状 ——
  协议侧再加一条 placeholder（TODO-093 干过），compact / goal / agentruntime 自动继承，它不会。
- **只有一个调用点，所以没留 wrapper**：直接在 `firstCapabilityLoopSignal` 里改成
  `capabilityloop.IsPlaceholder` 并删掉整个函数（净 −10 行）。`internal/tui` 原先完全没 import 该协议包，
  加这个 import 不成环（已用 `go list -deps` 核实）。
- **没补新测试，这是刻意的**：行为零变化写不出「移除修复后会失败」的行为测试，delegate 之后
  任何「两份集合一致」的断言都是同义反复。判据是既有测试全绿且**一行未改**，加上函数体 diff 为空。
- 至此 `capabilityloop.IsPlaceholder` 对**全部五个消费方**单一来源，全仓不再有该集合的任何私有副本。

## 2026-07-26（同一份子代理输出的两处结论分叉 · TODO-102 / 101 / 100 DONE）

**三条是一条线，按 102 → 101 → 100 做：102 拆掉 import 环，101 才可能引用单一来源。**
102 是纯重构（既有测试一行未改），101 与 100 **行为会变，而且应该变**。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--todo-102--101--1002026-07-26import-环与两处证据判定分叉)。

- **纯协议包不再依赖存储层（TODO-102）**：环是 `capabilityloop` → `internal/storage/mysql` →
  `internal/goal` → 回到 `capabilityloop`，根因只有一处 —— `DecisionContextFromStoredTask` 为了接一个
  `mysqlstore.AgentTask` 参数，而它其实只读该结构 15 个字段里的 5 个。入参降为协议包自有的
  `capabilityloop.StoredTask`，**`internal/storage/mysql` 一行未动**（依赖是单向的）。
  于是 `internal/goal` 的四个 gate 标签从字面量改成 `FollowUpField*` 常量，双份维护消失。
  判据「既有测试一行都不用改就仍然绿」已实测：测试文件摘要与改动前**逐字节相同**。
- **同一份子代理输出，两种驱动方式结论不同（TODO-101）**：`internal/goal` 与 `internal/agentruntime`
  的私有 placeholder 集合都漏了 completed 的默认 next_action，于是那句**机器写的样板话**在这两处算真信号。
  后果不是「少过滤一句话」——**goal 驱动把它当 `must_handle_next_action` 发给父代理（一个不存在的义务），
  query 驱动压制它**，父代理该做什么取决于它恰好跑在哪条驱动路径上。两处都收到 `IsPlaceholder`。
  **核心测试是跨驱动方式的**：缺陷本身是「两侧不一致」，各自对着硬编码期望断言抓不到它，
  所以拿同一份 loop 过两条驱动、断言结论相同。failure/cancelled/timeout 三条默认文案仍不算 placeholder。
- **判断反了，只反在一个状态上（TODO-100）**：`defaultNextActions` 只列了 5 条注入默认里的 4 条，
  **漏了 timeout**，于是**内容全空的超时子代理靠 runtime 自己刚写进去的样板话被判为「有可用证据」**，
  而同样全空的 completed 正确判为没有。收在「都判为没有」这一侧 —— 查过两个调用方，该判定**只决定要不要附
  结构化 `capability_loop` 块**，`status` / `is_error` / `error` / `Content` 都另行送达，
  所以父代理照样知道任务超时了，只是不再收到一块 100% 由 runtime 自写的假证据。
- **改了一个 golden，且这才是对的**：与上一批相反，本批**必须**动 ——
  `internal/tools/task/testdata/golden/batch_timeout.json` 原本就把这个 bug 冻在文件里。
  三个 golden 里**只有 timeout 这个**带那块全样板 `capability_loop`，completed 的两个都没有；
  **这个不对称本身就是那个反了的判定**，一直躺在仓库里。
- **每处行为变化都实测可证伪**：逐个把修复换回改动前的代码，对应测试立刻失败
  （goal 侧报 `drivers disagree on must_handle_next_action`、agentruntime 侧指名 `status "timeout"`、
  golden mismatch），再恢复转绿。
- **两处顺带发现，登记未修**：`internal/tui` 是 placeholder 集合的第四份私有副本
  （值一致但无单一来源，本批按协作边界只读不动）→ TODO-110；
  `internal/tools/bash` 的后台 job 测试偶发读到半写文件 → TODO-111（该包本批零改动，判定为既有 flaky）。

## 2026-07-26（两处「持久化漏了东西」收口 · TODO-106 / TODO-080 DONE）

- **mobile 会话 fork 不再留下半写分支会话（TODO-106）**。`/mobile/chat/sessions/:id/branch` 原先先
  `UpsertSession` 建新会话、再循环 `UpsertMessage` 逐条复制，全程不在事务里 —— 中途失败留下一个只
  复制了一半消息的分支会话。新增 `internal/storage/mysql/session_fork.go` 的 `ForkSession`，把整批写入
  收进一个显式事务，形态与 TODO-070 的 `SaveQueryTurn` 同构：**只给这条路径开事务**（不翻转全局
  `SkipDefaultTransaction`，那是刻意的性能选择）、**重试整个事务**而非事务内单条语句（回滚后单独重发
  一条消息会把它挂到已不存在的会话上）、**归属校验留在事务内**（不得把数据隔离 review 那道 `getSessionTx`
  绕过去）。
- **长事务的取舍写成了显式上限**。`maxForkedMessages = 500`。条数其实早就被 `normalizeLimit` 夹在 500，
  但那是两层之外的巧合 —— 读路径的夹取一旦调大，没有上限的 fork 就是几万行的单事务。
  **选择拒绝而不是分批**：分批写会重新打开「半写会话」这个正要修掉的窗口。
- **MCP 图片 resume 后仍可见（TODO-080）**，但**没按原文估的「6 行」做，因为那个判断不成立**。
  直接给 `recordMessage` 加 `image` 分支除了让 transcript 按张膨胀（单图 base64 可达 6.7MB，而 transcript
  是每次读会话都整文件扫回来的），还会让一条未登记的 `image` 行把整个 v1 transcript 判成 `Mixed`，
  `ValidateResumeFormat` **直接拒绝 resume** —— 「图片丢了」升级成「会话打不开了」。已实测到
  `format = mixed`，故 `image` 同时登记进 `isGoClaudeV1EntryType`。
- **改为外化到侧车文件只留引用**，沿用 `internal/toolresult` 对大工具结果那套机制：新增
  `internal/session/media.go`，载荷落到 `<transcript 目录>/<sessionID>/media/<sha256>.b64`，日志里只留一条
  不到 200 字节的 `MediaRef`。内容寻址所以同一张图重复记录只落一个文件，也不需要计数器起名；
  存 base64 文本而非解码字节，回放时少一轮编解码。resume 侧读侧车重建真 image 块；读不回来时降级成
  文本块，并**改写**说明文案 —— 记录时那句 "attached below" 在图片已丢时是句谎话。
- **context 撑爆不是这条的风险**：`compact.estimateInlineSourceTokens` 对内联图片按像素封顶
  ~1600 token/张，不按 base64 长度算（AUDIT-P1-05 已修）。
- **`recordMessage` 与 `recordCompactMessage` 抽出共用的 `recordUserContentBlock`**。两个 switch 原本逐字
  相同，图片正是在其中一条上漏掉才有了这条 TODO；合并后两条路径不可能再对「什么会被持久化」给出
  不同答案。
- **验证**：两条都先红后绿。TODO-106 的红是 `half-written branch session left behind`（handler 层）与
  `expecting database transaction Begin`（仓储层非事务版本连 ROLLBACK 都没发生过）；TODO-080 的红是
  `no image entry recorded` / `resume dropped the image block` / `format = mixed`。真机 MySQL e2e
  `TestMySQLE2EForkSessionRollsBackHalfCopiedBranch` 已随本批落地，但**本机无 MySQL/docker 故只验证过
  正确 skip，没在真 InnoDB 上跑过**（环境缺口仍是 TODO-105）。

## 2026-07-26（capability_loop 三处解析分叉收口 · AUDIT-P2-02 DONE）

**这批不是纯重构 —— 行为变了，而且应该变。** 详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p2-02-收口2026-07-26todo-092--093--094)。
上一批只做了物理合并、把差异如实登记成 TODO-092/093/094；本批**做决定**。

- **修掉一个真 bug（TODO-094）**：子代理结果过大被外化后，`toolresult` 保留的 summary 只渲染 6 个字段，
  丢掉 `resolved_follow_up` / `supersedes_evidence_id(s)` / `follow_up_id` —— 而那份 summary 是 supersede 关系的
  **唯一载体**，于是**子代理刚解决掉的 follow-up 会重新变成 pending**，父代理被告知仍不能宣布完成。
  同族第二个 bug：`TaskHint` 发出的是逗号拼接的 id 列表，反解侧整串塞进单数字段，
  产出**一个匹配不到任何 follow-up 的假 id**。两侧都补，并加了真的走一遍 `toolresult.Process` 外化的端到端
  用例；**已实测两半各自可证伪**（单独回退任一半即挂）。
- **候选串提取合成一份 `capabilityloop.JSONCandidates`（TODO-092）**，四处差异逐个定：标签**大小写不敏感**、
  **取全部**标签对、**保留**子串守卫（行为中性快路径）、无标签兜底**整段 + `{...}` 切片**。
  顺带修掉原实现的偏移漂移 bug —— `ToLower` 会改变字节长度（`İ` → `i̇`），标签体被切歪，
  原先只是靠兜底侥幸救回。**加宽的是「什么能解析成功」**：以前子代理写 `<Capability_Loop>`
  或第一对标签畸形，整份证据就静默丢掉。
- **placeholder 集合合成 `IsPlaceholder`（TODO-093 ①）**：compact 开始过滤 completed 的默认 next_action。
  它单独就能让 `HasHint` 成立，压缩恢复出的空 decision context 会挤进父代理只有 3 格的证据环**顶掉真证据**。
  **failure/cancelled/timeout 三条默认文案故意不收** —— 那是「必须处理一次失败」的真义务。
- **更正了审计的一处判断（TODO-093 ②）**：所谓「compact 多认的字段别名」不是子代理拼法，
  而是 `FollowUpLine` 自己发出、compact 再读回的 follow-up gate 行标签。因此**没给另两处补这些名字**
  （补了是死代码），改为 `protocol.go` 的 `FollowUpField*` 单一来源。原文声称的后果不存在。
- **golden 一个都没改，且这才是对的**：本批的变化方向要么只加宽「什么能解析成功」、
  要么只往 summary 加字段，而 capability_loop 相关 golden 断言的是「字段/section **存在**」的布尔值，
  原本已为 `true`。若有 golden 变了，反说明改动误伤了原本能解析的 payload。
- **顺带发现，已登记未修**：`agentruntime` 是第四份 placeholder 实现，且其 `defaultNextActions`
  只列了 5 条注入默认里的 4 条，**漏了 timeout** —— 内容全空的超时子代理被判为「有可用证据」，
  全空的 completed 判为没有（TODO-100 / TODO-101）；`internal/goal` 的四个 gate 标签只能保持字面量，

- **顺带发现，已登记未修**：placeholder 集合全仓其实有 **5 份**，本批只统一了归属内的 2 份，
  `internal/goal/evidence.go` 与 `internal/agentruntime` 仍缺 completed 默认文案那条 ——
  同一句样板话在 goal 侧的 gate 里会被输出、query 侧被压制（TODO-101）；`agentruntime` 的
  `defaultNextActions` 只列了 5 条注入默认里的 4 条，**漏了 timeout** —— 内容全空的超时子代理被判为
  「有可用证据」，全空的 completed 判为没有（TODO-100）；`internal/goal` 的四个 gate 标签只能保持字面量，
  因为引用常量会闭合 `capabilityloop` → `storage/mysql` → `goal` 的 import 环（TODO-102）。
## 2026-07-26（`persistTenantQuery` 的事务边界 · AUDIT-P1-26 收尾）

AUDIT-P1-26 的第三项，至此该条三项全部闭合。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-26-事务边界2026-07-26)。

- **一次查询的三张表改为原子写入**。`persistTenantQuery` 原本连写会话 / 用户消息 / 助手消息
  三次独立提交，中途失败会留下半写会话（会话行在、消息缺失，或用户消息在、助手消息缺失）。
  现在收敛成一次 `SaveQueryTurn` 调用，由存储层用一个显式事务保证要么全成功要么全回滚。
- **接缝放在存储层，不向上暴露事务句柄**。给仓储加「原子写入一次查询结果」的方法，
  逐层加到 `tenant.Repository` / `tenant.Service` / `server.TenantService`。
  暴露句柄会把 GORM 漏进 `internal/server`，还会逼 70+ 方法接口的每个测试夹具模拟事务语义；
  更关键的是死锁重试必须包住整个事务（重新 BEGIN 并重写三行），由服务端驱动就得回放服务端逻辑。
- **没有动全局的 `SkipDefaultTransaction: true`** —— 那是刻意的性能选择，
  改它会影响全库每一次写入；只给这一条需要原子性的路径开事务。
  死锁重试沿用 `withRetryableTransaction`，与 `RollbackSkillVersion` 同构。
- **租户隔离没有被事务绕过去**。2026-06-26 数据隔离 review 加的会话归属校验抽成
  `getSessionTx`，普通路径与事务路径共用同一段 SQL；两条消息的 tenant/user/session
  统一由仓储填成刚写的那一行，调用方填不进来。顺带比原来少一次 SELECT
  （原本两条消息各查一次会话，现在整批查一次）。
- **先红后绿有两层证据**。服务端接缝的测试对**未改动的生产代码**直接跑出
  `session rows = 1, want 0`；存储层 5 个 sqlmock 测试先在「没有事务」上失败
  （`ExpectedBegin => expecting database transaction Begin`）再转绿，其中一个专门守
  「死锁重试重跑整个事务而不是单条语句」。
- **明确未做**：真机 MySQL e2e 跑不了（本机无 MySQL/docker），新增的 e2e 测试
  只验证过能编译并正确 skip（TODO-105）；`mobile.go` 的会话 fork 是同形态的另一处
  半写风险，不在本条范围内（TODO-106）。

## 2026-07-26（拆分 TUI 巨型文件 · AUDIT-P2-04 TUI 侧）

**纯重构，行为零变化。** 详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p2-04-tui-侧2026-07-26)。

- **`internal/tui/app.go` 9099 → 364 行**，按职责拆成 30 个文件（审计记的 9051 行在其后五轮修复中又涨了 48）。
  `app.go` 只留 Bubble Tea 骨架：`Model` 状态、内部消息类型、`NewModel` / `Run` / `Init` / `View`；
  `Update` 单独成 `update.go`（事件分派是独立职责）。最大的新文件 574 行。
- **划分依据是数据流向和闭包边界，不是行数**。流事件的三段旅程各自成文件
  （`display_segments.go` 写时间线 → `transcript.go` 落 scrollback → `live_display.go` 渲染活跃块）；
  四个弹窗各自是「状态 + 按键 + 鼠标 + 渲染」的闭包，各成一文件；近 1900 行零 `Model` 依赖的纯函数
  （工具摘要、capability_loop 解析、markdown 表格）单独析出，剩下的 `Model` 方法才看得出结构。
- **判据不是「测试绿」而是「既有测试一行都不用改就仍然绿」**。7173 行测试与
  `internal/cli/testdata/golden/` 全部 golden **一字未改**。机器校验：原文件与 30 个新文件
  剥掉 `package` 行和 import 块后，非空正文行的多重集完全相同（8555 = 8555），
  30 个文件的 import 并集与原 import 集合对称差为空。唯一非「移动」的新增是每文件顶部一行职责注释。
- **刻意没顺手做的四件事**，均已登记：`app_test.go` 7173 行未拆（TODO-095 —— 按符号投票自动归属
  不可靠，`Model`/`message`/`busy` 让多数测试都投给同两个文件，且近半数 `TestModelXxx` 本就是
  跨模块集成测试）；`util.go` 的 `min`/`max` 遮蔽 Go 1.21 内置（TODO-096 —— 删掉会改变全包调用解析，
  是行为面改动）；AUDIT-P2-04 的前端部分（TODO-097）；三个 560+ 行新文件的二次细分（TODO-098 ——
  再切就要拆函数而不只是移动函数）。按协作边界，`firstNonEmpty` 未合并（留给 AUDIT-P2-03），
  `internal/cli/interactive.go` 一字未动。

## 2026-07-26（拆 query god object · AUDIT-P2-01 / P2-02）

**纯重构，行为零变化。** 验收判据不是「测试绿」，而是「既有测试一行都不用改就仍然绿」——
本批没有改动任何既有测试的断言。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p2-01--p2-022026-07-26)。

- **`internal/query/query.go` 7910 → 5976 行**（−1934，−24.5%），顶层声明 370 → 251。按审计 §8
  既定顺序做完四步：①agent evidence / capability_loop 的 46 个纯声明（783 行）拆到新包
  `internal/capabilityloop`；②系统提示词正文与 runtime task strategy（548 行）拆到
  `internal/query/systemsections.go`；③transcript↔messages 与 resume 清洗（321 行）拆到
  `internal/query/resume.go`；④completion verification（282 行）并入 `internal/query/closure_gate.go`。
  **主循环 `run` 没有动**，按审计 §9 的既定取舍 —— 里面每段都有真实状态耦合，强拆会引入超长参数列表。
- **②③④ 刻意留在 `package query` 而不是各自建包**。②的 section 正文读 `getenv`/`isEnvTruthy`/
  `firstNonEmpty`，这三个助手在包内另有 25/18/40 处调用，换包就得复制三份 —— 而 `firstNonEmpty`
  正是 AUDIT-P2-03 要收敛的重复项。④的四个助手本就被 `closure_gate.go` 跨文件调用约 30 次，
  同包合并零改调用点。**改 embed 模板被否**：section 正文的空白与换行会逐字进 prompt，转模板就是改行为。
- **怎么证明是「移动」不是「重写」**：用 AST 给每个函数体打指纹逐一比对，拆前拆后
  **210 个函数体全部逐字相同、0 个丢失**；唯一 5 处差异是下一条的字面量→同值常量替换，已逐条人工核对。
- **AUDIT-P2-02 的前提是错的，已在审计文档更正**。审计说「三份重复的解析器实现」，实测
  **三个文件之间零个函数体字节相同** —— 它们不是复制粘贴，而是同一协议的三份**行为分叉**的独立实现，
  产出形态本就不同（`HintData`+`ArtifactHints` / 人读 summary / `Facts` 分类切片），
  而且对同一 payload 的解析结果**不一致**。合并解析器等于改行为，不是重构。
  本批只把三者确实一致的结构性 token 收成一份 `internal/capabilityloop/protocol.go`
  （19 处字面量替换为同值常量），结构性 token 从此不可能再各自漂移。
- **四类字段级分叉已列表并各自立项**（TODO-092/093/094）：候选串提取语义（大小写敏感性、
  取第一对还是全部标签、兜底范围）、placeholder 集合、字段别名表、字段覆盖面。
  其中第四条是真功能缺口 —— `toolresult` 的 summary 从不读 `supersedes_evidence_*`，
  走外化/反解这条路时 supersede 关系会丢，已解决的 follow-up 可能重新变 pending。
- **测试改动**：2 个纯函数测试随代码迁到新包（只改随包移动的标识符名），
  2 个构造 `&Session{}` 的测试只改类型限定名，其余测试文件一个字符没改。**断言改动 0 处。**

## 2026-07-26（记忆作用域与 KB 检索 · AUDIT-P1-11 / P1-12）

主题是「说是检索/过滤，实际不是」—— 五条都不是缺功能，而是**每轮 prompt 都在生效的真 bug**。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-11--p1-122026-07-26)。

- **中文 KB 检索此前返回 0 行，比审计描述更糟**。审计说「静默退化成无排序全表 LIKE」，实测是**没有结果**：
  `MATCH` 恒为 0（默认全文分词器不切中文），而 LIKE 的 pattern 是**整条用户 prompt**，比任何 chunk
  都长，永远命不中。所以修复必须两条同时做 —— 新增 migration `000009` 把 KB 的 `FULLTEXT` 换成
  `WITH PARSER ngram`（不动已应用的 `000003`），并把检索串切成有界词项（最多 8 个）分别做 LIKE。
- **`ngram_token_size` 是服务器级只读参数，migration 改不了**。必须由 DBA 在建索引**之前**写进
  `my.cnf` 并重启，顺序反了要 DROP 重建。前提、限制（短于 `ngram_token_size` 的 query 仍命不中）
  和验证方法见 [docs/deployment/mysql_fulltext_ngram.md](docs/deployment/mysql_fulltext_ngram.md)。
  **本机没有 MySQL，这条未真机验证**：sqlmock 断言生成的 SQL 确实走 `MATCH` 分支，真机侧留 opt-in
  e2e 对中文 query 断言 `search_mode == "fulltext"`。
- **KB 分块不再把中文切碎**：`maxChunkChars = 1200` 名为字符实际按字节判断和切片，中文分块碎字
  且只拿到约 1/3 的预算。改为按 rune 计数与切分。
- **`paths:` frontmatter 从「匹配 prompt 字符串」改成「匹配本轮文件集」**。旧实现是
  `strings.Contains(prompt, needle)`：只有用户字面输入目录名才生效、任何含该子串的散文误命中
  （`api/**` 会命中 `docs/api/README.md`）、pattern 中间的通配符永远匹配不上。现在从 prompt 抽路径
  形态 token 构成文件集，用真 glob 匹配。**作用域未知时不隐藏** —— 识别不出路径就照常加载，
  作用域未知不等于不匹配。
- **记忆有了字节预算，截断处有明确标记**。此前预算只对 `Type=="Workflow"` 生效，`CLAUDE.md`/user/
  team/managed/项目记忆**从不截断**，500KB 的 `CLAUDE.md` 会整个进 prompt。现在单文档 16KB、
  合计 64KB（环境变量可调，设 `0` 关闭），按优先级顺序消耗总预算，截断处一定有
  `[Memory truncated for prompt budget] … read <path> …`，且落在行/rune 边界上。
- **明确不做**：embedding / 向量检索（TODO-025 既定取舍）；中文 ngram 词项合成（会让 LIKE 命中
  一切、反而毁掉排序）；code 模式接入 KB 检索（功能缺口而非虚假声称，需先定 prompt 预算与相关性门槛）。
- **明确未做且写清了前置**：`paths:` 想表达的「本轮**实际改到**的文件」做不到 —— frontmatter 在
  装配提示词时求值，那时一次工具调用都没发生，将要读写的文件尚不存在。要覆盖需要①query 层维护本轮
  文件集 ②回灌 memory 层 ③**一轮之内重新求值 frontmatter 并重建 system addendum**，第三步会影响
  prompt 缓存命中与 `codePromptReport` 语义，需另立条目。也评估并放弃了加 `LoadCodeOptions.Files`
  做接缝：query 层唯一能给的是截图临时文件路径，传进去会让文件集非空却全是 `/tmp` 路径，
  反而**错误地收窄**记忆 —— 无调用方的选项比没有选项更糟。

## 2026-07-26（server 对外暴露前提 · AUDIT-P1-21 / P1-22 / P1-27）

三条一起收口，共享同一个判断：**默认配置绑到公网时不能是安全的，只能是启动不了的。**
详见[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-21--p1-22--p1-272026-07-26)。

- **认证不再 fail-open**：`authorize` 此前对空 token 直接 `return true`，而部署文档第 4 节
  明确教用户 `--host 0.0.0.0` —— 一个忘了 `--auth-token` 的远程部署，会把 `/trace`
  （完整会话）和 `/prompt-dump`（**完整 prompt 正文**）匿名暴露给整个网络。现在
  「空 token + 非回环绑定」在监听之前就被拒绝，错误信息给出两条可操作的出路。
  **刻意没有环境变量逃生口**：逃生口存在，这个组合就仍然可能出现，而它正是本条要
  消灭的东西。非 IP 主机名一律按「远端可达」处理，不做 DNS 解析 —— 宁可多要一个
  token，也不让一次解析失败把判断翻成「安全」。
- **`?token=` 收窄到拿不到对话的端点**：query 里的 token 会进 Nginx access log、
  浏览器历史和 Referer。`/trace/api/sessions*` 与 `/prompt-dump/api/records` 改为只认
  `Authorization` 头；`/trace`、`/prompt-dump` 这两个**不含任何会话内容的 HTML 外壳**
  保留 `?token=`（浏览器打开链接时没有别的地方能塞 header），函数改名为
  `authorizeViewerShell`，免得下一个人再把它接到吐数据的端点上。
- **`/prompt-dump` 加一道本机门槛**：它落的是 system prompt、全部历史消息和工具结果，
  泄漏面比 `/trace` 还大，而它本身就是本机调试视图。现在只服务本机直连请求，
  **带 `X-Forwarded-For` / `X-Real-Ip` / `Forwarded` 的请求一律拒绝** —— 否则同机部署的
  Nginx 会让 `RemoteAddr` 恰好是回环，整道门被代理链穿透。远程查看请开 SSH 隧道。
- **常量时间比较**：header、WebUI cookie、外壳 query 三处 token 比较统一走
  `subtle.ConstantTimeCompare`。WebUI 的 `?token=` → Cookie 流程**刻意保留**：浏览器
  加载 SPA bundle 时没地方塞 header，而那个 Cookie `Path=/webui/`，不会被带到 `/api/*`，
  `authorize` 也根本不读 Cookie。新测试把这个边界钉死。
- **liveness / readiness 分离，probe 不再需要 token**：新增无鉴权的 `/livez`（不探依赖）
  与 `/readyz`（探 MySQL、quota Redis、mobile Redis，任一不可用返回 503）。`/health`
  **行为一字未改**，仍需 token、仍返回 workspace。`/readyz` 敢不鉴权是因为它只回
  `ok`/`unavailable`，底层错误（DSN、地址、连接错误原文）只进服务端日志 —— 有一条
  专门的断言在检查响应体不含这些。
- **Redis 启动探活**：`quota.RedisStore` 与 `RedisMobileUsageStore` 补 `Ping`，装配后
  立即探活，失败即启动失败。go-redis 的 `NewClient` 是懒连接，配错地址照样构造成功，
  此前要等到第一条真实请求才暴露 —— 而那时配额路径会走 fail-open/fail-closed 分支，
  两种都不是运维想要的结果。MySQL 侧本来就 Ping 过，这次把 Redis 拉齐。
- **配额不再能靠「不带 header」绕过**：`reserveQueryQuota` 在没有租户上下文时不再直接
  放行，而是落到按客户端的兜底限流，覆盖 `/query` 与 `/v1/chat/completions`。
  **按 TCP 对端地址计数，不看 `X-Forwarded-For`** —— gin 的 `c.ClientIP()` 默认信任所有
  代理，拿它当 key 的话攻击者每次换一个伪造转发头就能拿回无限额度，那样这道门等于
  不存在。默认 120/min（`GOLANG_CLAUDE_CODE_QUERY_RATE_LIMIT_PER_MINUTE` 可调，负数关闭）：
  这条路径覆盖的正是本机自用的调用，限太紧会打断仓库主人每天在跑的工作流。
- **请求体 `cwd` 约束到 workspace 子树**：它直接决定服务端工具的执行目录，此前无任何
  约束。校验放在 `runServerQuery` —— 所有入口唯一的收敛点。**先解符号链接再比较**，
  否则 workspace 内一个指向 `/etc` 的软链就能把整棵允许子树撑开。
  `GOLANG_CLAUDE_CODE_SERVER_ALLOWED_CWD_ROOTS` 可显式加根；workspace 为空且没配环境
  变量时保持原行为，而不是假装拦住了。
- **本机自用路径一步没退**：`127.0.0.1` 不配 token 仍然全端点可达、`/trace` 外壳仍能用
  链接打开、默认限流额度下连打 120 次全部 200 —— 6 条反方向断言专门锁这些。
- **两个既有测试是旧行为的化石**：`TestTraceAPILocalSessionsAndDetail` 与
  `TestPromptDumpViewerAndAPI` 都在用 `?token=` 调吐数据的 API，已按新契约更新。
  反向验证时那条红态尤其说明问题：200 响应体里直接是真实的全量会话列表。
- **未纳入**：token 过期、轮转、按租户区分、吊销 —— 需要一套完整的凭证模型，不是本条
  能顺手做完的，单一共享静态 token 的局限仍然存在。另：cwd 被拒时 `/query` 返回 500
  而非 400（错误从 `queryFn` 穿回来，现有 handler 一律映射成 500），安全属性成立但
  状态码不精确。两项都应另起条目。

## 2026-07-26（server 在真实负载下不自伤 · AUDIT-P0-11 / P0-12 / P1-24 / P1-26）

四条都不是功能缺失，而是「单请求看不出来、并发一上就互相放大」的成本项，且相互叠加 ——
SSE 的常驻 QPS 打在没有上限的连接池上，telemetry 的同步写又和它们抢同一批连接。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p0-11--p0-12--p1-24--p1-262026-07-26)。

- **telemetry 不再阻塞请求路径（AUDIT-P1-24）**：`Emitter.Emit` 顺序遍历 sink，其中
  `RecorderSink` 是一次 MySQL INSERT、`HTTPSink` 是一次同步外呼；API 中间件每请求发两个事件，
  于是每请求两次同步写库加两次同步外呼，外部 APM 抖动原样变成 API 延迟。新增
  `telemetry.AsyncSink`（有界队列 + 固定 worker）把慢 sink 挪到后台，`Emit` 只入队。
  这是四条里唯一**本机自用就在付钱**的 —— 开 tenant MySQL 模式时 WebUI 每次点击都在付。
- **异步之后事件不能丢**：后台投递用 `context.WithoutCancel` 摘掉请求的取消但留下值
  （请求 ctx 在 handler 返回时就被取消，直接带进后台等于保证写库失败；而 `RecorderSink`
  又必须靠 ctx 上的 tenant/user 值解析租户，所以不能换成 `context.Background()`）；
  退出时 flush 缓冲，且 flush 跑在 shutdown 排空在途请求之后 —— 那些请求还在往缓冲里写；
  Close 之后到达的零星事件同步投递；队列塞满时丢弃并计数，但经 `Emit` 的返回值冒泡成
  error 日志、退出时再汇总一次，不会静默。
- **只有 `Run`/`serveHTTPLifecycle` 那条路是异步的**：`NewHandler` 没有退出钩子，缓冲永远
  不会被 flush，静默丢事件比同步写慢更糟，所以那边**刻意保持同步**。
- **SSE 轮询有了上界（AUDIT-P0-12）**：agent task 事件流原来是固定 250ms ticker × 每 tick
  两条查询，100 条并发流就是 800 QPS 常驻。现在空转时指数退避 250ms → 2s 上限（有新事件立刻
  回到 250ms，前端打字机的实时感不受影响），且有事件的那一轮不再多打状态查询 —— 事件还在流
  说明任务显然还活着。10 秒空转的查询数从 80 降到 ≤20。**没有改成事件驱动**：审计给的是
  「事件驱动或指数退避」，而进程内 pub/sub 会变成又一处要拆的单机状态（见 AUDIT-P1-25）。
- **MySQL 连接池不再全默认（AUDIT-P0-11，PARTIAL）**：四参数默认 `25 / 10 / 30m / 5m`，
  由 `GOLANG_CLAUDE_CODE_MYSQL_{MAX_OPEN_CONNS,MAX_IDLE_CONNS,CONN_MAX_LIFETIME,CONN_MAX_IDLE_TIME}`
  覆盖，在第一次用连接之前（早于 `PingContext`）设好。**刻意不提供「不限制」选项** —— 无上限
  正是要修的那个 bug。压测证据仍缺（本机无 MySQL 也无 docker），记为 TODO-071。
- **精确 trace_id / session_id 走索引（AUDIT-P1-26，PARTIAL）**：audit 与 telemetry 的列表
  查询原来把 `Search` 一律翻成 `%x%` 铺在 4–10 个列上，索引全废；而 timeline 手里攥着的正是
  精确 ID。`Search` 现在支持 `field:value` 限定符（`trace_id:abc`、`session_id:42`、
  `trace_id:a,b,c` → `IN`），命中白名单的字段走等值并落到既有索引，**无需迁移**；自由文本仍
  走原来的 LIKE，老用法不变。WebUI 搜索框因此白得一套限定符语法。
- **两处 N+1 收敛**：timeline 从「每个 trace 一条查询」变成一条 `IN`；timeline 与会话详情里
  「每个任务查一次事件」换成 `ListAgentTaskEventsForTasks`。三 trace + 两任务的 timeline
  查询数从 8 降到 5。事务边界一项未做（`persistTenantQuery` 连写 3 张表不在同一事务），
  记为 TODO-070。
## 2026-07-26（安装路径与文档截图 · AUDIT-P1-30 / P1-34）

主题「拿到这个仓库的人能装上、文档不骗人」。**零 Go 代码改动**，全部落在文档、`scripts/` 和一个新
workflow。详见 [审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-30--p1-342026-07-26)。

- **有了安装路径，而且只有一处版本注入**：新增 `scripts/install.sh`（从当前 checkout 编译并装到
  `$PREFIX/bin`，默认 `~/.local/bin`）和 `scripts/release.sh`（5 个平台的归档 + `SHA256SUMS`）。
  两者都 **shell out 到 `scripts/build.sh`**，而不是各写一行 `go build` —— `-X ...cli.Version` 全仓
  仍然只有一处，所以不存在「某条安装路径装出来的 `--version` 是 `dev`」这种事。实测产物报
  `v0.1.48-go-dirty (Go Claude)`（工作区脏，这正是 `build.sh` 的 `--dirty` 语义）。
- **`release.sh` 的 `VERSION` 只算一次并 export**：否则 `build.sh` 会按目标各跑一次
  `git describe`，跑到一半打 tag 就会产出版本互不一致的归档。
- **`install.sh` 的两个 Go 下界含义不同，所以处理不同**：低于 `go.mod` 的 `go` 指令直接 exit 1
  （编译根本过不去），低于 `.tool-versions` pin 的版本**只告警** —— 能编过，产物带
  GO-2026-5856，把这个事实说出来比拒绝安装有用。两个版本号都从各自文件读，脚本没有成为第三个
  写死版本号的地方。目标目录可写性在**编译前**检查，编译要几十秒。
- **发布 workflow 自证**：新增 `.github/workflows/release.yml`（`v*` tag 或手动触发），**独立成
  文件**，ci.yml 那五个 job 一字未动 —— 质量门每次 push 都跑，发布链路只在打 tag 时跑。发布前
  解开 linux 产物跑 `--version`，输出不含该 tag 就 fail：本条的验收标准被焊进 CI，不会随时间退化。
  凭证只用 Actions 自带的 `GITHUB_TOKEN`。
- **明确不做的三条，README 里写清了原因**：`go install` 装不上不是漏写文档 —— `go.mod` 的 module
  path 与仓库地址不一致，proxy 拉不到；还有 `replace` 指令；且 `go install` 传不了 ldflags
  （TODO-088）。`curl | sh` 和 `brew` 需要公开可下载的产物地址，而本仓库当前非公开（TODO-087）。
  Dockerfile 因本机无 docker 无法按验收标准实跑（TODO-089）。**宁可不给，也不给一个没验证过的
  安装路径。**
- **26 张不存在的截图：删引用改文字，不补假图**。`docs/usage/*.md` 有 21 处 `![](...)` 指向仓库里
  没有的文件（审计记 26，差额是此前已删掉一部分），渲染就是 21 个碎图标。理由三条：这个仓库 UI
  每天在改，**过期截图比没有截图更能骗人**，它看上去是证据；多数场景（WebSearch、多 subagent、
  Goal/Loop、自动压缩）要真实 key 才拍得出来，为配图造假界面图是这套文档最不该做的事；而
  「📷 待补」标记上一版就逐行标过，21 个坏引用照样留着。每个被删的图都把原图注**提升为正文**。
  全仓本地图片坏链 **21 → 0**。
- **宣传口径同批改掉**：README 和 `docs/README.md` 里「每个场景配截图和要点」的说法一并改了 ——
  图删了而宣传留着，等于换个地方继续骗。
- **保留的图是有证据兜底的那些**：真实存在的 3 张（code-review 两张 + 模型对比一张，展示的是
  「多步工具编排 + 错误恢复」这类不随皮肤变化的行为）和 `docs/web_agent/images/` 下 6 张真机
  E2E 验收证据图。
- **顺带修掉三处同类问题**：`docs/usage/cli.md` 残留的作者私人绝对路径（AUDIT-P1-29 漏的一处）、
  `docs/usage/tui.md` 里从来没解析过的 `README.md#常用-tui-操作` 锚点（README 里那是普通段落不是
  标题）、README「CI 跑四个 job」（ci.yml 实际有 6 个，漏了 swagger 和 scripts）。
- **8 个未进 `docs/README.md` 索引的顶层文档全部登记**：`session_quickstart.md` 进「稳定入口」，
  其余 7 个进新增的「其他顶层文档」表，并写明它们本该收进子目录、这次为什么不搬。
## 2026-07-26（五条 PARTIAL 的收尾 · AUDIT-P1-08 / P1-18 / P1-19 / P1-20 / P2-05）

前几批做了大半留下的尾巴。四条转 DONE，P2-05 仍 PARTIAL。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-08--p1-18--p1-19--p1-20--p2-052026-07-26五条-partial-的收尾)。

- **provider 熔断冷却（P1-08 → DONE）**：可 fallback 的失败让该 provider 冷却 60 秒，下一轮直接
  跳过，而不是每轮都先赔一整个建流超时才切到 fallback。**上一批记的「做不了」理由不成立**：当时说
  `Client` 生命周期不统一，实测 6 个 `anthropic.NewClient` 调用点里 `internal/cli/cli.go:609` 那个
  是会话级长命对象（一个会话建一次、跨轮共享、传给 query/task/agentruntime），正好是需要失败记忆
  的作用域；其余 5 个（3 个 recap、2 个 agenteval）是一次性对象，那里「没有记忆」不是缺陷而是无从
  记忆；`internal/server/` 全仓零 `NewClient`。两个方向的安全阀：**4xx 与调用方取消不入冷却**
  （不是 provider 健康度的信号），**全员冷却时忽略全部冷却**照常从队首试（否则一次短暂的全域抖动
  会被放大成整段会话不可用）。冷却状态带互斥锁 —— 同一个 client 会被 batch 子代理并发使用。
  跳过原因写进聚合错误文案，静默跳过读起来像「只试了 fallback」。
- **MCP 图片真正送达模型（P1-18 → DONE，TODO-061 闭合）**：上一批只做到「不再静默丢弃、留占位行」，
  模型仍然看不到图。**关键发现是不需要动 `internal/query/query.go`（0 行）** ——
  `tools.Result.ContextMessages` 这条通路已经存在且 query 与 agentruntime 两侧都已接好
  （`internal/tools/skill` 是既有用例），缺的只是「MCP image 块 → `anthropic.ContentBlock`」这最后
  一段。白名单 `image/png|jpeg|gif|webp` 升级为真正的 image 内容块；其余仍留占位行，**且现在会写明
  为什么看不到**。三道 base64 上限（单图 5MB 原始字节等值、单次最多 4 张、总 10MB）由这一层自己守 ——
  `ContextMessages` 不经 `toolresult.Process` 的字符截断预算。顺带修 OpenAI 路径的一处真丢弃：
  `case "image"` 在 `Source` 为空时整块消失，而 Anthropic 路径同样情形会回落到 `block.Text`。
  **音频永远留占位行**：Messages API 没有 audio 内容块，这是 API 限制不是管道缺失。
  **图片不写进会话日志**，所以是「本轮可见、resume 后消失」（解释性文本块会留下）——
  刻意的，把 MB 级 base64 写进 transcript 会让日志按张膨胀；已拆为 TODO-080。
- **`Task` 的顶层 `required[]`（P1-19 → DONE）**：**决定不用根级 `anyOf`**（各 provider 的 tool
  schema 校验兼容性不一，沿用上一批的判断），改为在代码里校验。那条缺陷的本质是「畸形输入被静默
  接受」而不是「schema 不够花哨」：`{}` 和只给一半的输入都会被接受然后拿一个空 prompt 起子代理，
  三个字段都给时顶层 `description`/`prompt` 被静默忽略。`validateTaskForm` 把两种情形都挡掉，
  并给模型一句能照着改的错误。schema 文案同步写明二选一，并修一处小的不诚实：`max_concurrency`
  写着 `maximum: 16` 而 `GOLANG_CLAUDE_CODE_MAX_BATCH_CONCURRENCY` 能把实际上限抬高。
- **子代理工作树与重试对称（P1-20 → DONE）**：「batch 子代理写同一棵工作树无锁」经核清是两个缺陷
  叠在一起。`internal/tools/task/task.go` 对 `agentworktree` **零引用**，真正的并发 bug 在
  `internal/agentworktree`，而且比「无锁」更糟 —— `NewSlug` 只用 `time.Now().UnixNano()`，
  macOS 的时钟粒度下并发调用会拿到**同一个 slug**，而 `Create` 对已存在的工作树是**复用**而不是
  报错，于是两个子代理无声地共用一棵树互相踩文件（实测 6 次运行里 1–2 次复现）。修法是 slug 加
  进程级原子计数器后缀，外加**每个 gitRoot 一把互斥锁**包住 `Create` 的 read-modify-write 整段与
  `Remove`（关掉 `rev-parse` 检查与 `worktree add` 之间的 TOCTOU）。锁只覆盖 git 元数据写入这几秒，
  不覆盖子代理的实际工作 —— batch 并发本身是调用方真实想要的。
  **Task batch 侧不加锁，而是不再说谎**：给 N 个子代理共用的一棵树加锁没有正确的粒度（粗锁把 batch
  串成串行等于取消这个功能，细锁需要预知每个子代理要碰哪些文件）。真正的缺陷是 schema 把 `tasks`
  描述成 "isolated sub-agent tasks"、工具描述里也这么写 —— 模型照着这个词就会把 8 个写同一批文件的
  任务扇出去。两处文案现在都写明「各自独立会话，但共用同一棵工作树且无文件锁」。per-item 工作树
  隔离是新功能，拆为 TODO-081。
  **单 Task 现在也按 `retry_attempts` 重试**，与 batch 共用同一个 `worthRetrying`（取消不重试、
  deadline 重试）、同一套退避、同一个 0..10 clamp，两条路径因此不可能再漂移。auto-background 路径
  刻意不重试：一次尝试可能已经翻成后台任务并返回了句柄，再起一次就是两个子代理跑同一件事。
- **前端存量 lint 清理（P2-05 仍 PARTIAL）**：清完能机械化且确实是修正的那些并把对应规则提为
  `error`（清了不提 error 等于会回来）：`a11y/useButtonType` 47 处补 `type="button"`（默认 `submit`
  是真行为缺陷）、`noArrayIndexKey` 9 处（3 处换成真有稳定身份的 key，6 处是 markdown 重解析内容与状态 chip ——
  没有身份可用、子元素无状态、位置就是身份，保留位置 key 并配 `biome-ignore` 写明理由；
  **第一版把 key 计算提前到额外的 `.map` 里骗过静态分析、key 一模一样还是位置 key，已否掉改回**）、
  `noAssignInExpressions` 2 处、a11y 四族 25 处
  （补 `tabIndex` 与 Enter/Space 键盘路径，resizer 与 CSS-grid 表格保留 role 并写 `biome-ignore`
  说明理由）、`useOptionalChain`/`noEmptyBlock`/`noUselessFragments`/`noExtraBooleanCast`。
  **全部手改，一次都没用 `biome lint --write --unsafe`。**
  **两族明确不做**：`useExhaustiveDependencies`（104 处）保持 `warn` —— 手改 104 个依赖数组会改动
  全应用的 effect/memo 时序，而现有测试覆盖不到这些时序，拆为 TODO-082；
  `noDescendingSpecificity`（21 处 CSS）保持 `warn` —— 修它要重排层叠顺序，而本仓还没有真的
  visual regression baseline，改完没有任何东西能发现视觉回归，拆为 TODO-083。
  `complexity/noImportantStyles`（5 处）改为 `off` 而非清理：其中 4 处是
  `@media (prefers-reduced-motion: reduce)` 的覆盖，`!important` 在那里是**必须**的，删掉就是 a11y
  回归 —— 一个对本仓正确写法永远报警的规则留着只是噪音。**CSS 一行未动。**

## 2026-07-26（平台空操作选项 · AUDIT-P1-37）

AUDIT-P1-35 的合并 review 自己发现的残留：P1-35 只把「配了就以为在保护你」修到了网络项。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-372026-07-26)。

- **`seccomp` 不再在 macOS 上假装生效**：`sandbox.seccomp.enabled` 全仓只有两个生产读取点，
  都在 linux 分支（bubblewrap 的 `--seccomp` fd 与 `SANDBOX_SECCOMP` 环境变量），macOS profile
  完全不看它 —— seccomp 是 Linux 内核设施，在 darwin 上不可能生效。但标签一直无条件打印
  `seccomp`，于是 macOS 用户看到 `on/seccomp/network` 会以为系统调用过滤开着。现在标签只在
  linux 打印它，darwin 上改为 `on/network/degraded:seccomp` 并给出「本平台无解」的说明。
- **镜像情形 `allowPty`**：只被 `macOSSandboxProfile` 读取，linux 侧从不查询，现在在 linux
  上报为 `degraded:allowPty`。**只在显式设为 true 时触发**（默认 false，按默认值告警会让每个
  linux 用户永远看到一条警告），且文案**不声称 pty 因此被拒** —— bwrap 下 pty 可达性由
  `--dev /dev` 决定，而这一点未在真实 Linux 主机上验证过。
- **AUDIT-P0-04 的拒绝契约一字未变**：`PolicyGap` 加了 `Network` 分类，`NetworkPolicyGaps`
  只返回网络那一类，所以 `PrepareShell` 在 `failIfUnavailable` 下拒绝启动的条件没有扩大。
  这是**刻意的不一致**：`failIfUnavailable: true` + `seccomp.enabled: true` 的 macOS 配置
  今天能跑，让它突然启动失败是回归，而收益只是把一条已显示的警告变成硬失败。
- **健康安装仍然一字不多**：不配 seccomp 的 macOS 安装，`status` 里 `sandbox` 字样 0 次。
  两个测试改动值得单独说 —— `TestTUISandboxLabelUnchangedOnHealthyInstall`（P1-35 时我自己写的）
  原先设了 seccomp 并称之为 healthy，`TestTUIWelcomeInfoReflectsRuntimeConfig` 的旧期望值
  `on/seccomp/network/sockets` 两半都是谎报。**它们本身就是这条 bug 的化石。**
- **未纳入**：`enableWeakerNestedSandbox` 同样只被 linux 路径读取，但它不进标签也不产 gap ——
  影响的是沙箱内部强度而非「用户以为配了什么」，如需收口应另起条目。

## 2026-07-26（沙箱状态上报 · AUDIT-P1-35）

这条是 AUDIT-P0-04 修复过程中登记的：**引擎修好了，但状态从不上报给用户**。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-352026-07-26)。

- **沙箱标签改为基于实际判定，不再照抄 settings**：`tuiSandboxLabel` 此前的逻辑是
  「`Network.Disabled` 或 `AllowDomains` 或 `DenyDomains` 任一有值 → 打印 `network`」。
  于是一台 PATH 里没有 `sandbox-exec` 的 mac 照样打印 `on/network`，而实际上**没有任何一条
  shell 命令进了沙箱**；只配 `allowDomains` 时也打印 `on/network`，而域名过滤只作用于内置
  HTTP 工具，shell 命令直连网络。这是最危险的一类错觉：用户以为自己被隔离了，并据此行事。
  现在标签来自新增的 `sandbox.Describe(cfg)`，二进制缺失 / 平台不支持 / 本平台不在
  `enabledPlatforms` 时给出 `degraded/not-enforced`（**刻意不以 `on` 开头**），存在
  `NetworkPolicyGaps` 时给出 `on/…/degraded:network.domains,network.proxy`（**点名是哪一项**）。
  同时**不再打印没生效的部件** —— 让 `allowDomains` 点亮 `network` 是同一个谎言换了小号字体。
- **`/status` 与 TUI 欢迎卡展示可执行的原因**：`sandbox.IsAvailable` 和
  `sandbox.UnavailableReason` 此前全仓零调用方，两个本来就能说出真相的函数从未被问过。
  现在 `/status` 多出 `sandbox` + `sandboxWarnings`，欢迎卡渲染同一批文案；每条 warning
  都说清**哪一项没生效、为什么、在这台机器上能不能补救**（例如 macOS 缺 `sandbox-exec`
  会指向 PATH 丢了 `/usr/bin`，域名列表会指向改用 `sandbox.network.disabled=true`）。
  欢迎卡的**宽窄两种布局都加了** —— 终端窄不是隐藏「沙箱没生效」的理由。
- **沿用 AUDIT-P0-06 的取舍：只告警不拒绝，健康安装输出一字未变**。完全生效时标签与旧实现
  逐字节一致，`sandboxWarnings` 字段**不出现**，默认安装（`sandbox.enabled=false`）的
  `status` 输出里连 `sandbox` 字样都没有。这两点各有反方向断言钉住：把字段改成无条件输出，
  测试立刻转红。
- **两项如实标注的边界**：本机是 macOS 且无 bubblewrap，所以 **Linux 侧（`bwrap` 缺失）只有
  注入式覆盖、未经真机验证**（同 PARITY-001），darwin 侧则用 `PATH=/nonexistent` 真机验证过；
  `seccomp` 部件在 darwin 上仍是空操作却照旧打印，与本条同源但不属 `NetworkPolicyGaps`
  契约，当时**刻意未改**，登记为 AUDIT-P1-37 并已在同一分支闭合（见下一条）。

## 2026-07-26（工具诚实性四条 · AUDIT-P1-14 / P1-15 / P1-16 / P1-19）

主题：**工具要么诚实，要么明确报错，绝不伪造。** 详见
[审计文档的完成记录](docs/audit/2026-07-25_capability_audit.md#完成记录audit-p1-14--p1-15--p1-16--p1-192026-07-26)。

- **NotebookEdit 不再静默损坏用户文件（P1-15，本批危害最高）**：`rawCell` 只有 5 个固定字段，
  每次编辑的 unmarshal→marshal 往返都丢掉 nbformat 4.5 **必需**的 cell `id`、`attachments`、
  厂商扩展和顶层未知键 —— 产出 schema 非法的 notebook，且没有任何报错。改为保留整份解码后的
  文档、只覆写被编辑那一格的 `source`/`cell_type`。另用 `json.Decoder.UseNumber()` 挡住同一类
  更隐蔽的损坏：默认路径把所有数字过一遍 `float64`，大整数和高精度小数会在保存时被改写。
  键顺序变为字典序，这是无损的，也正是 `nbformat` 自己（`sort_keys=True`）写出的顺序。
- **WebBrowser 的 `screenshot` 不再返回假图（P1-14）**：它从不渲染任何东西，只是把标题和 URL
  拼进一段 SVG 再 base64 成 `data:image/svg+xml;base64,...` 返回，模型会合理地认为拿到了页面
  图像。fallback 模式改为明确报错，并给出启用真浏览器的具体两步
  （`GOLANG_CLAUDE_CODE_WEBBROWSER_MODE` + `GOLANG_CLAUDE_CODE_PLAYWRIGHT_RUNNER` 指向
  `scripts/playwright-browser-runner.mjs`）以及「现在能用什么」（`action=text`/`links`）。
  真浏览器模式行为不变；合成 SVG 的函数已删；描述改为开宗明义写清两种模式的区别。
- **六条子进程路径统一脱敏（P1-16）**：此前只有 Bash 剥离凭据，剥的还是一份枚举出来的 8 个
  名字；`hooks`（**可来自项目级 `.claude/settings.json` —— clone 一个仓库就可能被拿走 key**）、
  `powershell`（Windows 上是主 shell，等于脱敏完全缺席）、`workflow`、`webbrowser` 的 playwright
  runner、以及每个 stdio MCP server（`cmd.Env` 只在 `len(cfg.Env)>0` 时设置，否则 nil = 全量继承）
  都没有。新增 zero-dep 叶子包 `internal/procenv` 作为唯一实现，六处复用。
  **denylist 改为按名字形状匹配**而不是严格 allowlist —— 后者与「别破坏 hooks/workflow 依赖的
  正常环境变量」直接冲突（会掐掉 `GOPATH`/`GOCACHE`/`NODE_ENV`/`CI`）。现在子串
  `SECRET`/`PASSWORD`/`PASSWD`/`CREDENTIAL` 与后缀 `_TOKEN`/`_KEY` 一律剥离，三个漏网的
  `GITHUB_TOKEN`/`AWS_SECRET_ACCESS_KEY`/`OPENAI_API_KEY` 全部拦下，未来新凭据自动覆盖。
  调用方显式传入的 `extra`（MCP server 配置的 `env`、sandbox spec）刻意不过滤。
  **hooks 的沙箱旁路与策略旁路未做**，应单独立项。
- **WebSearch 与 LSP 名副其实（P1-15）**：WebSearch 的 `parseResults` 原来用一条 `<a href>`
  正则扫全页，引擎自己的导航栏和页脚被当成搜索结果；现在限定到 DuckDuckGo 的 `result__a`
  结果标记，无标记的自定义端点回退到「绝对的站外链接」。LSP **行为未改**，改的是描述 ——
  它不启 gopls、不做类型解析、只支持 Go、`references` 是按标识符名字裸匹配（跨包同名符号、
  局部变量、结构体字段全混在一起），`diagnostics` 只有语法错误；描述现在逐条如实陈述。
- **结果截断与覆盖率（P1-19，PARTIAL）**：`MaxResultSizeChars()` 补给 `ls`/`notebook`(Read+Edit)/
  `webbrowser`/`workflow`。**`Read` 是审计误报** —— 它有 `SkipToolResultBudget()=true`，自己按
  2000 行 + 大文件 manifest 限流，故意不进共享预算，补 `MaxResultSizeChars()` 会是死代码；
  改为新增测试锁定该意图。覆盖率：`powershell` 18.0%→58.0%（天花板是无 `pwsh`）、
  `worktree` 45.2%→85.7%（`add`/`remove` 此前完全未测）、`workflow` 50.9%→84.5%、
  `taskoutput` 56.2%→93.8%。**`Task` 的顶层 `required[]` 未做**：单任务与 `tasks` 批量二选一，
  写死 `required` 会禁掉批量形式，正解是根级 `anyOf` 但 provider 兼容性需单独决策。
## 2026-07-26（WebUI 出厂可用 + Files tab 真数据 + 前端质量基建 · AUDIT-P1-32 / P2-10 / P2-05）

共同主题是「WebUI 真的能用、且显示的是真数据」。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-32--p2-10--p2-052026-07-26)。

- **`/webui` 不再静默 404（P1-32）**：`registerWebUIRoutes` 原来在 `WEBUI_DIR` 为空时直接
  `return`，整个路由不注册 —— 而裸 404 与「这个 build 没有 web UI」无法区分。改为无条件注册，
  目录三级解析：显式 `WEBUI_DIR`（且必须含 `index.html`）→ 自动发现 CWD / 可执行文件旁的
  `web/dist` → 内嵌的 503 构建说明页（给出确切命令，并指向此刻可用的 `/trace`、
  `/prompt-dump`）。于是 `npm --prefix web run build` 之后不设任何环境变量也能用。顺带修掉
  第二个静默 404：目录存在但缺 `index.html` 时旧代码对每个资源 ServeFile 一个不存在的
  `index.html`。**Vite 产物本身不 `go:embed`**：`go:embed` 模式不能跨出包目录（`..` 非法），
  且 embed 缺失目录是编译错误而 `web/dist` 被 gitignore —— 要 embed 就得把压缩产物提交进仓库。
  `/trace`、`/prompt-dump` embed 的是单个手写 HTML 源文件，不是 build 产物，不可类比。
- **Files tab 显示真字段（P2-10）**：这是 TODO-049 标 DONE 但实际未做的子项。前端旧代码读
  `payload.file ?? payload.path ?? payload.filename` 并累加 `insertions`/`deletions` ——
  这些 key 没有任何 Go writer 会写（`changed_files` 同样），所以该 tab 恒为空。新增
  `agenttasks.EventFileChange`，**复用既有 `tools.FileChange` 协议**而非另造一套：写侧来自
  `agentTaskTextSink.OnToolResult` 早就拿到却一直丢掉的 `query.ToolTrace.FileChanges`，读侧来自
  读类工具真实 input 的 `file_path`；streaming 与非 streaming 两条路径都发。行数是实测的，内容
  被外化到 snapshot store 时发 `content_available:false` 并省略 `line_delta`，不把缺失的测量
  报成 0。前端改为 Edited / Read 分组。无需改动 `internal/tools` 或 `internal/agentruntime`。
- **前端质量基建（P2-05，PARTIAL）**：引入 Biome 2.5 lint（选它而非 ESLint 是因为
  typescript-eslint 的 peer 上限低于本仓 pin 的 `typescript@6`）；**只 lint 不 format**，因为
  现有排版与 Biome 默认在 78 个文件上不同。新增 `typecheck`（`tsc -b --force`）把
  `vite.config.ts`、`playwright.config.ts` 和整个 `e2e/` 纳入检查 —— 此前 `build` 里的
  `tsc -p tsconfig.app.json` 只 `include:["src"]`，这些文件永不被类型检查。
  `e2e/visual-regression.spec.ts` 从不调 `toHaveScreenshot()`、无 baseline，改名
  `navigation-smoke.spec.ts` 并写明实际断言什么。删除 3 个死依赖（连带 101 个传递依赖）。
  CI `web` job 扩展为 lint / typecheck / build / test。
- **顺带修掉一个测试盲区**：`swaggerPathForRoute` 只认 gin 的 `:id` 不认 `*wildcard`，此前唯一的
  wildcard 路由在跳过名单里所以从未触发 —— 任何被正确注解的 catch-all 都会被误报为缺失。

## 2026-07-26（日常可读性三条 · AUDIT-P1-28 / P1-29 / P1-31）

三条都是仓库主人每天在付的税：错误被日志淹没、子命令不认 `--help`、README 第一条命令指向一个
已经不存在的目录。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-28--p1-29--p1-312026-07-26)。

- **CLI 默认安静，诊断显式开启（P1-28）**：干净环境无 API key 跑 `-p "hi"`，stderr 从
  **21 行 JSON + 3 段 Go stacktrace** 变成**一行人话**。日志级别解析补齐
  `fatal`/`dpanic`/`silent`，**无法识别的级别名现在直接报错并列出合法值**，不再静默落回 Info
  （`=fatal` 反而喷最吵输出正是这个 bug）；`debug` 之外不再输出 Go 调用栈，`=error` 从
  「5 行带 stacktrace」变成「5 行、0 stacktrace」。`GOLANG_CLAUDE_CODE_LOG_LEVEL` 补进
  `--help` 的 Environment 段和 README。**`server` 与 5 个后台入口不受影响**，
  JSON 结构化日志逐字节不变，并有测试守住。
- **标准库错误不再原样漏出（P1-28）**：`server --port abc` 从 `strconv.Atoi: parsing "abc"`
  变成 `--port must be a number between 0 and 65535, got "abc"`；
  `--system-prompt-file` / `--append-system-prompt-file` 的读文件错误带上 flag 名；
  非法 `--permission-mode` 列出全部合法拼写（新增 `permissions.AcceptedModes()`）。
- **每个子命令都认 `--help`（P1-31）**：`session --help` 以前报
  `unknown session command: --help`，`goal`/`skills`/`mcp`/`plugin`/`hooks`/`agents`/`tenant`/
  `transcript` 同样。现在全部返回该命令的用法。
- **三份命令清单收敛成一份（P1-31）**：dispatch switch、help 里手写的 usage 行、completion 词表
  改为全部从新的 `commandTable()` 派生，结构上不再可能漂移 —— 实测
  `completion zsh` 补回了此前缺失的 `transcript`/`goal`/`goals`/`eval`。
- **`--fork-session` 不再是死 flag（P1-31）**：以前只影响 TUI 顶部一个显示字符串。现在
  resume 上下文照常注入，但这一轮记进新会话，原 transcript 不被追加；不存在的 session id
  仍然报错。
- **陈旧绝对路径（P1-29）**：README 首条命令改为 `git clone … && cd niuma`；docs 下 1152 处
  `$HOME/GolandProjects/golang-claude-code` 按「带子路径」剥成仓库相对路径、
  「裸指仓库根」换成占位符。`docs/swagger.*` 与 `docs/docs.go` 里的那处**刻意未动**：
  它们由 `internal/server/swagger_types.go` 生成，只改产物会立刻造成 swagger 漂移。

## 2026-07-26（provider 长尾四条 · AUDIT-P1-07 / P1-08 / P1-09 / P1-10）

四条都在 `internal/anthropic/` 这一层 provider 边界上，一起做只是为了共用一次全量验证。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-07--p1-08--p1-09--p1-102026-07-26)。

- **thinking 块回传不再退化成空 text 块（P1-07，本批唯一一条真 bug）**：`sdkMessageParam` 的
  switch 原来只处理 `image`/`tool_use`/`tool_result`，`"thinking"` 落到 `default:` →
  `NewTextBlock(block.Text)`，而 thinking 块的 `Text` 恒为空（正文在 `.Thinking`、签名在
  `.Signature`）。后果是丢 signature（多轮 tool use 时 Anthropic 要求原样回传）+ 空 text 块被
  API 以 `text content blocks must be non-empty` 拒掉。无 signature 的块（resume 老日志）选择
  丢弃而不是回传，因为 Anthropic 一定会拒。顺带补齐 `redacted_thinking`：它原来在
  `streamResultFromSDKMessage` 里被整块丢弃，下一轮根本没有东西可回传，`ContentBlock` 因此
  新增 `Data` 字段（`omitempty`，向后兼容既有会话日志）。
- **OpenAI-compatible 建流有了真重试，并且真的读 `Retry-After`（P1-08，PARTIAL）**：
  429 沿用 1 次重试，新增 5xx / 408 / 409 / 网络抖动 2 次、指数退避 500ms→8s 封顶（两套预算
  独立，量级差三个数量级）。`Retry-After` 以前完全不读 —— go-openai 的错误类型不带响应头，
  所以改为在 `httpTraceClient.Do` 截获后经 context 交给重试决策，支持秒数、HTTP-date 和 OpenAI
  实际下发的 `retry-after-ms`。边界都有测试守住：`errProviderTimeout` 明确不重试（不和
  AUDIT-P0-07 的分段超时打架），明确的 4xx 不重试（持续性故障留给 provider fallback）。
  **熔断冷却未做**：Client 生命周期在 `internal/cli` / `internal/agenteval` 有 6 个构造点、
  进程内状态未必跨轮存活，做对需要先定 Client 所有权，属独立改动。
- **三处静默丢弃（P1-09）**：① Anthropic 路径的 `ResponseFormat` 现在会翻译成 system prompt
  约束并在 **provider 边界**注入，所有调用方一次覆盖；带幂等哨兵，避免和 server 层 / CLI
  `--json-schema` 已有的注入叠成两遍。② `ANTHROPIC_PROVIDER=bedrock` 死分支已删 ——
  全仓只有一个读取点，而 `providerKindSupported` 只认 `anthropic*`/`openai*`，那条短路指向一个
  这个 runtime 根本连不上的 provider。③ OpenAI 路径新增 `thinking` → `reasoning_effort` 映射，
  字段带 `omitempty`，只在调用方显式配了 thinking 时才出现，不会给不支持的网关塞未知参数。
- **`parseSSE` 删除，生产路径的 mid-stream error 补上真实覆盖（P1-10）**：`parseSSE` 唯一调用者
  是测试，生产 Anthropic 路径走 SDK、OpenAI 路径走 go-openai，两个「SSE 健壮性测试」验证的是
  不上生产的解析器。选择删（-239 行生产 / -109 行测试）而不是接回生产：接回去等于用手写解析器
  替换 SDK 已维护的那套，留着则是维护第二份会漂移的解析器 + 持续提供虚假信心。补测过程中把两半
  分开了 —— **Anthropic 路径本来就是对的**（SDK 认 `event: error`），缺的只是测试；
  **OpenAI 路径确有真 bug**：go-openai 只认 `data: {"error":` 这种 error 作为整行首个 key 的
  形式，网关把错误塞进形状正常的 chunk 时会静默产出空响应，现改为 `RecvRaw()` + 自己反序列化
  内嵌 `error` 字段。

## 2026-07-26（循环熔断补全 + `/compact` 语义修正 · AUDIT-P1-04 / P1-03）

两条同属「循环与压缩语义」层。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-04--p1-032026-07-26)
与 [docs/loop_guard.md](docs/loop_guard.md)。

**AUDIT-P1-04 · loop guard 的三个缺口**

- 机制下沉为 **`internal/loopguard`** 包（`Tracker` + 指纹/标签 helper + 三档升级文案）。
  这是**纯搬迁**：窗口(16) / 软限(3) / 硬限(6)、"首个重复记为 2" 的计数写法、
  以及"剔除 `tool_id`"和"纳入结果内容"这两条关键决策一字未动。
  既有 6 个 loop-guard 测试**未改一行**即全绿，就是无行为漂移的证明。
- **纯文本循环现在会被抓住**：completion gate 拦下后 `continue` 的分支此前永远到不了
  工具执行之后的判定块，模型反复输出同一份不合规收尾文本会一路跑到 `MaxTurns=100`，
  全程无计数、无提醒。现在该分支用 `TextFingerprint(拦截规则, 收尾文本)` 走
  **同一个 Tracker、同一个窗口**：streak 2 起带升级的 `## Loop check`，6 硬熔断。
  工具轮与文本轮共用一个 `applyLoopGuard` 闭包，升级与审计行为不可能各自漂移。
  **反方向守卫**：每轮换一份不同的收尾文本再试算有进展，不会被误杀。
- **completion gate reminder 不再堆积**：旧实现每轮把「模型草稿」和「gate reminder」
  双双 `append` 进 `messages` 且从不移除。现在草稿**回滚出 `messages`**
  （它本已从 UI、`result.Response`、transcript 三处撤回，`messages` 是最后一个漏的地方），
  reminder 改由 `pendingGateNudge` 承载、只在下一次请求组装时追加，模型改用工具后立即清空。
  **两件事必须一起做**：只删 reminder 会让两条 assistant 草稿相邻、破坏 role 交替。
- **子代理补上 loop guard**：`agentruntime.Runtime.Run` 接入独立 `Tracker`（每个 run 一个），
  熔断落 `StatusFailed` + `reason=loop_guard`；同时给 runtime-status 加 `Loop check`，
  否则子代理会在毫无预警的情况下被杀、没有自纠机会。
  `internal/agentbudget` 挡不住这一类空转 —— 预算管"花掉多少"，熔断管"有没有进展"。
- **收窄了审计的一处说法**：条目写"100 轮 = 100 条重复 reminder"。修完纯文本熔断之后，
  **文案完全相同**的重复第 6 轮就停了；堆到 100 需要文案每轮微异（噪声）才做得到。
  所以 reminder 那条修复覆盖的是「熔断按设计抓不住的带噪声循环」，不是冗余。

**AUDIT-P1-03 · `/compact` 保留的是最旧而非最近的对话**

- `session.buildSummary` 改为**从最新 entry 反向收集、再按会话顺序输出**。
  旧实现从最旧正向遍历 + `if out.Len() >= maxBytes { break }`，
  等于保留最古老的 12KB、丢弃用户刚说过的话。渲染单条 entry 抽成 `summaryLine`
  供两个方向共用，避免两套渲染漂移。
- 截断标记从 `[summary truncated]` 换成 `[older turns omitted to fit the summary budget]`，
  并**在预算里预留它的长度**，结果恒 ≤ `maxBytes`（旧实现实测会超：500 字节预算产出 520）。
- 新增边界：**最新一条 entry 单独就超预算**（默认 12KB 下一条大工具结果很常见）。
  保留它的头部并用 `strings.ToValidUTF8` 丢掉被切成半个的 rune
  （旧实现的 `out.String()[:maxBytes]` 会留下坏字节）。
- 上一次 `compact_summary` 仍作 header、其覆盖的 entry 仍不重复渲染，行为不变。
- **刻意没做**：这条路径仍不调用任何模型，只是把 transcript 渲染成 `ROLE: text` 拼接。
  本次只修"保留哪一端"这个语义反转；LLM 压缩由 auto-compact（`internal/compact/`）承担，
  手动 `/compact` 要不要也走 LLM 是独立决策。**未动 `internal/compact/`。**

## 2026-07-26（后台命令可观测可终止 · AUDIT-P1-13）

`Bash(run_in_background=true)` 此前是「起得来、看不清、停不下」：模型只能 `Read(log_path)`
全量重读日志，且**没有任何工具能杀掉后台进程**（`Store.Kill` 只有 `/kill` 斜杠命令和
`/runtime/background/{id}/stop` 用得到）。详见
[审计文档的修复证据](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-132026-07-26)。

- **新增 `BashOutput`**：返回自上次调用以来新增的输出 + 进程是否仍在跑。读游标持久化在
  `background.Job.OutputOffset`（放 job 里而不是工具内存里，因为同一个 job 会被 CLI / server / TUI
  三个进程看到）。单次上限 30 000 字节，**游标只前进到真正返回过的字节**，剩余部分置
  `more_output=true` 让模型再调一次 —— 按整份日志推进会让被工具结果层截掉的尾巴永久丢失。
  日志被截断/轮转时游标复位。
- **新增 `KillShell`**：`killed` / `not_running` / `not_found` 三态。只有确认进程活着才报
  `killed`；`not_running` 不算 error（「已经结束了」是幂等成功，报错会诱导模型重试），
  `not_found` 才是 error。
- `running` **不只看 status**：非终态 status **且** 信号 0 探测 PID 存活。只信 status 会把
  「监管进程自己死了、status 永远停在 running」的 job 报成仍在跑。
- **Bash 后台返回体与描述改指向新工具**：`instructions` 从「read log_path」改成
  「Poll it with BashOutput (bash_id=…)」+「Stop it with KillShell」。这一条才是让 P1-13
  在实践中真正闭合的地方 —— 模型看返回体的概率远高于回头重读工具描述。
- 注册点是 `coreRuntimeTools`（`cli.go` 一行未改），所以自动走 `GuardAll` 权限链路；
  两个工具都实现 `MaxResultSizeChars()` 并加进 AUDIT-P1-19 的清单，不重蹈「漏实现即绕过
  10 万字符截断」的覆辙。
- 既有 `Store.Kill` 改为委托新的 `Store.Terminate`，契约不变，顺带修掉它对已完成 job
  仍去 `proc.Kill()` 陈旧 PID 的隐患（PID 复用时会杀错进程）。
## 2026-07-26（MCP 可靠性与协议完成度 · AUDIT-P1-17 / P1-18）

MCP stdio 客户端此前在一把锁里做「写请求 → 阻塞 `ReadBytes('\n')` 直到读到自己的响应」，
**server 挂起就等于调用方 goroutine 永久死锁**；同时 stderr 被丢弃、`cmd.Wait()` 从不调用、
加载失败是裸 `continue`。详见
[审计文档的完成记录](docs/audit/2026-07-25_capability_audit.md#完成记录audit-p1-17--p1-182026-07-26mcp-可靠性与协议完成度)。

- **后台读循环 + 按 id 多路分解**取代持锁往返。锁的粒度从「一次往返」降到「一次写」，
  同一 server 的并发工具调用不再互相排队，取消和超时也终于有地方注入。
- **分段超时，不是一个总 deadline**（沿用 AUDIT-P0-07 在 provider 侧立的原则）：
  元数据往返 60s 总时长；`tools/call` 用 10 分钟的**服务端静默上限**，收到任何一帧
  （含 `notifications/progress`）都续期，所以合法的长工具调用不会被砍。
  `errMCPTimeout` 与用户取消的 `context.Canceled` 分得开。
  HTTP 侧同样去掉了 `http.Client{Timeout: 30s}` 这个会砍断长调用的硬 deadline。
- **进程监管**：接 `StderrPipe` 并保留最后 8KB 尾巴，唯一的一次 `cmd.Wait()` 由
  `supervise` 负责（不留僵尸），server 崩溃时报出退出码 + stderr 而不是裸 EOF；
  `Close` 先关 stdin 让 server 自己收摊，超过宽限期才 SIGKILL。
- **加载失败不再静默**：`LoadTools` 两处裸 `continue` 换成 `observability.Warn`。
- **协议完成度（P1-18 为 PARTIAL）**：补齐 `Mcp-Session-Id` 回带（**合规
  streamable-http server 的第二次调用此前必然被拒**）、protocolVersion 协商与
  `MCP-Protocol-Version` 头、server capabilities 解析（只提供工具的 server 调
  `resources/list` 现在说人话而不是裸 `-32601`）、通知帧不再丢弃、list 类接口跟随
  `nextCursor` 分页、非文本内容留占位行而非静默丢弃（**只到「不再静默」为止** —— 模型仍看不到图片内容，工具返回值还是纯字符串，已登记 TODO-061）、`safeName` 冲突加后缀消歧、
  ctx 全链路传播；另修一处审计未列的问题：SSE 响应体里先到的 progress 通知会被
  当成应答帧解析。
- **明确未做并已记入审计文档**：旧版 HTTP+SSE 双端点传输（改为**明确拒绝并给可操作
  提示**，而不是假装支持）、OAuth、`tools/list_changed` 后热替换工具注册表。
- 顺带把 `internal/tools/mcpresources` 覆盖率从 12.0% 提到 78.0%（AUDIT-P1-19 的一角）。
- **合并前 review 又揪出 7 个新并发缺陷，均已修**（`-race` 跑绿看不见它们 —— 全都要求
  子进程行为不端才触发）：`Kill` 之后孙进程攥着 stderr 管道导致 `Close` 永久挂起
  （**相对旧实现的回归**，靠 `cmd.WaitDelay` + 全部等待加上界修掉）；`cmd.Wait` 抢在读
  循环前面关掉 stdout 管道、可能撕掉 server 的最后一帧；`bufio.Scanner` 遇超长 stderr
  行永久停读、把子进程堵死在 `write(2)`；`writeFrame` 无时限且占着写锁，既绕过超时
  也会和回调应答形成死锁；HTTP 侧把用户取消误报成超时；server→client 回调在并发下
  被归错工具（审批弹窗显示别的工具名）；字符串形式的 JSON-RPC id 让整帧解析失败。

## 2026-07-25（cache 计价倍率 provider 中立化 · AUDIT-P1-36）

AUDIT-P0-15 落地后合并 review 发现的后继问题：cache 分档倍率写死为 Anthropic 的
`read 0.1× / write5m 1.25× / write1h 2×`，且配置层根本表达不了别家的比例
（`session.Rate` 的三个 cache 单价字段全仓只读不写，`config.ModelPrice` 只有 `input`/`output`）。
**token 计数一行未动** —— `InputTokensIncludeCacheRead` 与 `usageFromAnthropic` 的求和口径
刻意保持原样，压缩阈值靠它校准。详见
[审计文档的完成记录](docs/audit/2026-07-25_capability_audit.md#完成记录audit-p1-362026-07-25cache-计价倍率-provider-中立化)。

- `settings.modelPricing` 每个模型新增可选的 `cacheRead` / `cacheWrite5m` / `cacheWrite1h`，
  接到已有的 `session.Rate`。零值仍表示「按倍率推导」，只配 `input`/`output` 的既有配置行为不变。
- **默认倍率按 provider kind 分组**：`anthropic` / `anthropic-compatible` 用 Anthropic 官方倍率
  （2026-07-25 核验官方定价页，逐字给出这三个数）；`custom` / `openai*` 落「未知」——
  「OpenAI 兼容」是协议不是厂商，DeepSeek 公开 V4 的 cache-hit 约 0.02×、且没有 cache write 档，
  同一 kind 下还挂着 GLM / Kimi / OpenAI 自己，没有能代表整组的数字。
  未知沿用 P0-15 的 `EstimateCost` 返回 `ok` / `UnknownPricingModels` 机制，且**只在该轮真的
  用到那个 cache 档时才不可计价**，零 cache token 的轮次照常出价。三个档位独立解析。
- provider kind 分类器（`providerKindOpenAI` 等）从 `internal/anthropic` 下沉到 `internal/config`，
  client 侧改为委托，避免两份字符串表漂移；新增 `config.ProviderKindForModel` 把 model id
  映射回服务它的 provider kind，找不到时明确 not-found 而不是猜。
- `internal/cli` 与 `internal/agentruntime` 里两份逐字重复的 pricing converter 合并为
  `session.ConfiguredRates(cwd)` —— 那份重复正是 cache 字段「只读不写」的直接原因。
- 三个倍率常量改名 `AnthropicCacheWrite5mMultiplier` / `...Write1h...` / `...Read...`，
  名字不再暗示「全局默认」。既有 `TestConfiguredRateDerivesCacheTiersFromInputRate` 原本用
  无 provider 信息的 `glm-5.1` 把错误行为钉成契约，已改为显式带 Anthropic 倍率。
- **DeepSeek cache 字段核实结论：假警报，不需要补映射。** 抓本机全部 5 个 provider 的真实响应
  （同前缀重复调用），`deepseek-v4-pro` / `glm-5.1` / `kimi-k2.6` / `deepseek-v4-flash` 都在
  `prompt_tokens_details.cached_tokens` 报出 2944–3072 命中，无一使用 `prompt_cache_hit_tokens`，
  现有读取路径正确。边界：这些都是网关而非 `api.deepseek.com` 直连，官方端点未验证。
  sensenova 那一项初次误判为「403 无权限」，实际是 `${SENSENOVA_API_KEY}` 未设置导致凭据为空
  （运行时会直接跳过该 provider，不发请求）；探测脚本直读配置原文绕过了 `os.ExpandEnv` 才看到 401。

## 2026-07-25（P0 零散项第二批 · AUDIT-P0-15 / P0-18 / P0-19 / P0-06）

四条互相独立，一起做只是为了共用一次全量验证。详见
[审计文档的完成记录](docs/audit/2026-07-25_capability_audit.md#完成记录audit-p0-06--p0-15--p0-18--p0-192026-07-25第二批-p0-零散项)。

### cache token 分档计价（AUDIT-P0-15）

`InputTokens + CacheCreation + CacheRead` 合成一个数再乘满额 input 单价，
而真实计价是 cache read 0.1×、5m write 1.25×、1h write 2× —— **重缓存会话成本被高估近 10 倍**。

- 新增 `internal/session/pricing.go`：`TokenUsage` 五个**互斥**档位，`EstimateCost` 逐档计价，
  倍率是导出常量。`ReportedUsage.Tiers()` 负责从记账口径拆出计费档位。
- **`usageFromAnthropic` 的求和口径刻意没动** —— 那个 `InputTokens` 是整个 prompt 大小，
  `compactor.ObservePromptTokens` 依赖它校准上下文估算器。两处都补了「不要改成排除 cache 档位」
  的注释，否则会静默弄坏压缩阈值。
- 未知模型不再静默算 0：`EstimateCost` 返回 `ok`，`UsageSummary.UnknownPricingModels` 列名，
  `CostKnown` 区分「$0」和「不知道」，子代理侧首次遇到时 `observability.Warn`
  （顺带给 observability 补了缺失的 `Warn`）。
- 子串匹配换成「必须是真 Anthropic model id」：剥 Bedrock/Vertex vendor 前缀后要求 `claude-` 前缀。
  `my-sonnet-4-proxy` 不再套用官方价。配置的 `modelPricing` 始终优先，`cost` 命令现在也会读它。
- **`internal/agentbudget` 的偏差注释随之重写**：token 侧不变（那是「做了多少工」而非计费口径，
  cache read 也照样占上下文窗口），所以**默认唯一武装的 token 熔断器跳闸时机完全不变**；
  cost 侧变准了所以更晚跳闸，但默认成本上限本就是 0（关闭），因此没有任何默认行为变化，
  不需要调默认值。新增测试把这个推理钉住。

### swagger 漂移（AUDIT-P0-18）

补 4 条注解（`/v1/providers`、`/runtime/settings` 的 GET/PUT、agent-task SSE stream）,
重跑 `swag init` 与 `api-types.ts`（路径数 80 → 83），
前端 `web/src/lib/api.ts` 的手写内联兜底类型换成生成 schema 派生的类型。

**防复发用两层，因为两层挡的不是同一件事**：CI 新增 `swagger` job 重跑生成器再 diff，
挡「改了注解没重新生成」；`TestEveryRegisteredRouteIsInSwagger` 把 gin 路由表逐条对
`docs/swagger.json`，挡本条**真正的根因**「新端点从来没写注解」—— 后者重跑生成器永远是干净的，
CI job 抓不到。为此把 `newRouter` 从 `newHandlerWithStreams` 里抽出来，让路由表可枚举。

修正审计两处误报：`/ws` 本来就有注解（真缺的是 3 个而非 4 个）；
`add5796c` 改的是 `/v1/models` 返回哪些模型而非响应结构（生成类型 schema 并未过期，过期的是描述）。

### acceptance 脚本（AUDIT-P0-19，**审计结论已更正**）

原结论「两个配套仓库本机不存在、验收基建已死」**是错的** —— 它们都在，
10 个 side-by-side 脚本不是死脚本。真实问题及修法：

- **未文档化的前置条件**（核心）：release gate 依赖的 11 个 `agent-proving-ground/reports/**/*.json`
  是**跑完 APG 之后的产物**。前置检查改成一次收集全部缺口 + 说明产物来源 + 给出 `--verify-only` 出路
  + 明确「什么都没跑」。
- **`GO_BIN` 默认值把能跑的脚本弄成了跑不起来**：7 个 TUI 脚本硬编码
  `/usr/local/go/bin/go`，比 `go.mod` 要求的更旧，一跑就报 `requires go >= 1.25.0`。
  改成从 `PATH` 解析后，3 个脚本立刻能完整脱机跑过。
- **硬编码绝对路径**：新增 `scripts/lib/external-repos.sh`，
  `GO_CLAUDE_UPSTREAM_DIR` / `GO_CLAUDE_APG_DIR` / `GO_CLAUDE_COMPANION_ROOT` 可覆盖，
  默认仍指向主 checkout 同级目录（`git rev-parse --git-common-dir`，worktree 里也对）。
  顺带清掉 5 处审计没提到的死路径（两处指向仓库**旧名** `golang-claude-code`）。
- **分类 + 统一入口**：`scripts/README.md` 按「可脱机 / 需 API key / 需配套仓库」三类逐个列出，
  并写明每个不进 CI 的脚本**具体为什么**；`scripts/offline-acceptance.sh` 是唯一脱机入口并接进 CI。

顺带暴露一个**未修的真实缺陷**：`tui-render-budget-acceptance.sh` 的 `last=Bash` 断言一直是红的，
只是因为脚本此前跑不起来所以没人知道。

### 本地凭据文件权限（AUDIT-P0-06，PARTIAL）

`config/config.local.yaml` 已 `chmod 600`。新增 `internal/config/credential_permissions.go`：
**同时**满足「权限放开了 group/other」和「确实含明文凭据」才告警 —— 只看权限会让 committed 的
`config/config.yaml`（`0644`、无凭据）天天报噪音。走 `Config.Warnings`，
`status` / `doctor` 的 `configWarnings` 呈现（无告警时字段不出现，健康安装输出一字未变）。
**刻意只告警不拒绝**：拒绝加载会打断所有依赖这类文件的现有工作流。

仍是 PARTIAL —— **key 本身需用户手工轮换**。

## 2026-07-25（子代理四个上限 · AUDIT-P0-14，顺带闭合 AUDIT-P1-20 两条）

闭合 [AUDIT-P0-14](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p0-142026-07-25)：
子代理这条路径此前**递归深度、batch 并发、后台 agent 数、累计 token/成本四个维度全都没有上限**，
一次错误的批量委派可以耗尽机器。

- **递归深度计数**：`tools.Context` 新增 `SubagentDepth`，`agentruntime.Run` 自增并回写，
  `runTool` 的 `childContext := parent` 把它带给子代理再委派的下一层。默认上限 2
  （`GOLANG_CLAUDE_CODE_MAX_SUBAGENT_DEPTH` 可覆盖）—— 选 2 而非 1 是因为一层再委派仍是
  真实分工，再深只是扇出乘数。超限**在建 task row / recorder / MCP server 之前**就拒绝，
  被拒的递归不留残留；错误文案明确要求模型自己做完剩下的活，而不是静默把 `Task`
  从 registry 摘掉（静默降级会让模型反复撞同一面墙）。
- **`Explore` / `Plan` 的 deny 列表**：两者 prompt 都写着 "READ-ONLY ... STRICTLY
  PROHIBITED"，却只 deny 了 `Agent` —— `Task` / `AgentCreate` / `AgentMessage` 全都还在，
  **一个只读 agent 可以派生一个可写的 `general-purpose` 子代理，绕过自己的全部约束**。
  现在共用 `readOnlyAgentDisallowedTools()`。`general-purpose` 刻意保持 `Tools:["*"]`
  且无 deny 列表：约束它的是深度计数而非能力裁剪，另加反方向守卫测试防止后人顺手收窄。
- **`max_concurrency` clamp 到 16**（此前 `max_concurrency: 500` 照单全收，与递归叠加是
  指数级扇出）。这里**选 clamp 而不是拒绝**（与下一条相反）：batch 的 N 个任务是调用方
  真实想做的工作，只是节奏该由运行时定，拒绝整批会逼模型手工分批。**但不静默** ——
  `batchSummary.concurrency_clamp_note` 报告原始值、生效值和 env 名，
  否则读起来就像"你那 500 个 worker 跑过了"。
- **后台 agent 数量上限 16**：新增 `internal/agentruntime/limits.go` 的
  `backgroundAgentLimiter`，**超限即拒，不排队**（排队等于把压力转成内存占用并对调用方
  隐藏过载），风格与理由沿用 `internal/server/agent_task_concurrency.go`。计数器必须是
  进程级 —— detached run 活得比启动它的那次调用长，per-call 限流器看不见它们。顺带给
  这个 goroutine 补上 `recover()`：它是 AUDIT-P0-10 明确留给本条的裸 goroutine 之一。
- **会话级累计 token / 成本预算与熔断**：新增 `internal/agentbudget`。`query.Session`
  建一个，经 `tools.Context.AgentBudget` 下发，**子代理树内所有嵌套子代理共用同一指针**
  而不是每层一份新配额。熔断检查在每轮请求**之前**，是"不再付钱"而不是"付完再报错"。
  只计子代理用量，**父会话主循环刻意不计** —— 跑到一半熔断主对话比它要防的失控更糟。
  默认 2000 万 token（远高于正常会话，但能在几秒内截住失控扇出），成本上限默认关闭。
  **与 AUDIT-P0-15 的已知偏差**：喂进来的 token/成本含 cache read 且按满额单价折算，
  是**高估**；对熔断器而言高估是安全方向（宁可早跳），但这两个数字不能拿去计费，
  包注释已写明，P0-15 落地后把分档数字直接喂进来即可。
- **顺带闭合 AUDIT-P1-20 两条**：①`SendMessage` 只是解析收件人后转调 `AgentMessage`，
  所以只 deny 前者等于没 deny —— 两个一起强制 deny，A→B 横向通道对子代理彻底关闭；
  ②batch 重试对 `context.Canceled` 照重试：父 ctx 已死，后续尝试必然同样失败，
  而每次尝试都覆盖上一次的 partial evidence —— 现在遇取消立即 break，**deadline 仍然重试**
  （单次超时不代表下次也超时），并有反方向守卫测试锁住这个边界。
- **`internal/cli` 零改动**：审计把 `cli.go:673-682`（把 `Task` 注册进传给 `Task` 自己的
  那个 registry）列为证据，但那个自引用本身是对的 —— 子代理在深度限内用 `Task` 是正常
  能力，真正缺的只是计数器。修在 `agentruntime` 因此与并行的 compact 分支零冲突。
- **已知残留**：`internal/server/` 的两条子代理入口没有会话级预算 wiring（该目录不在本次
  范围），`Runtime.Run` 的 nil 兜底让每棵子代理树至少是累计的；
  `internal/tools/task/task.go` 的另两个裸 goroutine 仍无 `recover()`；
  AUDIT-P1-20 余下两项（batch 子代理写同一棵工作树无锁、单 Task 与 batch 重试不对称）未做。
  另有一个**与 AUDIT-P0-08 合并后才出现的口径缺口**：子代理压缩（`maybeCompact` /
  `compactAfterOverflow`）走 compactor 自己的 client，那次 LLM 调用不计入本条的预算 ——
  量级远小于被挡住的失控扇出，但要全口径需要 compactor 把 usage 回灌进
  `tools.Context.AgentBudget`。
- 验证：`go test ./... -count=1` 全 ok / 0 FAIL；
  `go test -race ./internal/agentruntime ./internal/tools/task ./internal/agentbudget -count=1`
  通过无 race；`go vet ./...`、`gofmt -l .`、`git diff --check` 均无输出。
  **14 个新增测试逐条做过 mutation check**（移除对应修复后失败、还原后通过），包括两条
  wiring 断言（`TestRuntimeStampsSubagentDepthOnToolContext`、
  `TestSessionHandsSubagentBudgetToTools`）—— 深度回写和 budget 下发各只有一行，
  丢掉不会让任何其他测试变红，因为 nil budget 只是"无限"。

## 2026-07-25（上下文治理链路：压缩换序 + 默认开启 + overflow 兜底 + 估算校准 · AUDIT-P1-01 / P0-08 / P1-05 / P1-06）

闭合 [AUDIT-P1-01 / P0-08 / P1-05 / P1-06](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p1-01--p0-08--p1-05--p1-062026-07-25)。四条互为前提，顺序是「先换序 → 再定默认值 → 再校准估算 → 再修熔断」：换序后 token 数变小、压缩触发频率本身就降了，后面几条的阈值判断必须基于换序后的行为。

- **压缩判定挪到工具结果外化之后**（AUDIT-P1-01）：外化是免费的（大结果落盘，上下文只留路径 + 预览），压缩要花一整轮 LLM 调用且永久丢细节。**但审计对这条的收益估计偏大**：单条超大工具结果其实在工具执行时就被 `toolresult.Process` 外化了（`tools.EffectiveResultLimit` 把任何单条上限夹到 `DefaultLimit` = 50 000 字符），根本进不到 `messages`。换序真正影响的是**累积**场景 —— 多条各自低于 50 000 字符、合起来超过 message/history budget 的结果。这直接决定了金标怎么写：测试里的 blob 必须小于 `DefaultLimit`，否则两种顺序行为完全一致；第一版测试正是这么写的，被 mutation check 抓出对换序不敏感。
- **auto-compact 默认开启**（AUDIT-P0-08）：`compact.ConfigFromSettings` 改为「未显式配置即开启」，`config/config.yaml` 同步。此前不显式配就是 `false`，开箱即用的长会话**没有任何上下文溢出保护**，而失败模式是一个直接终止会话的 API 硬错误。显式 `enabled: false` 仍然生效。
- **新增 overflow 反应式兜底**（AUDIT-P0-08）：全仓此前 grep `context_length_exceeded` / `prompt is too long` 零命中。新增 `compact.IsContextOverflowError` + `Compactor.ForceCompact`（忽略阈值与冷却，仍尊重「显式关闭」与熔断），主循环和子代理各自在流失败时尝试**一次**强制压缩并重试当轮。**不与 provider fallback 打架**：context overflow 是确定性 400，`canFallbackAfterError` 本来就判它不可 fallback，走到这里时 client 侧已穷尽。marker 名单刻意收窄 —— 误判会为一个压缩救不了的错误白丢历史，rate limit / 网络抖动绝不触发压缩。
- **接入路径：实际只有两个 wiring 点，不是审计说的四个**（AUDIT-P0-08）：`internal/server/runtime.go`、`internal/goal`、`internal/scheduler` 都不自己建 query session —— server 的 `runServerQuery` 走 `newQuerySession`，tenant goal 用同一个 `StreamQueryFunc`，scheduler 用 `ChildProcessExecutor` 重新 exec 本二进制后落回 CLI 路径。所以 `cli.go` 那一行 `AutoCompact:` 一改默认值，四条路径同时获得保护。真正缺失的是**子代理**：新增 `internal/agentruntime/compact.go`，`Runtime.AutoCompact` 未给值时按 `config.LoadForCWD(req.CWD).Settings` 加载（与既有 `SubagentModelTiers` / `ModelPricing` 同一套 cwd-scoped 模式），**因此不必改 `tools/task/task.go` 或 `agent.go`**（属并行任务 AUDIT-P0-14）。压缩不掩盖子代理循环：它只在超阈值时动作，不改轮次计数，`maxTurns` 上限原样保留。
- **base64 图片不再按文本估算**（AUDIT-P1-05）：新增 `estimateInlineSourceTokens`。图片按单图上限常量 1 600 token（provider 按像素而非传输体积计价，有效分辨率上限约 1.15 MP）；PDF 等非图片内联附件按体积粗估并以同一常量兜底。1 MB 截图从 ~35 万虚假 token 降到 1 600 —— 此前单张图就能强制压缩。
- **真实 usage 回灌校正估算器**（AUDIT-P1-05）：`Compactor.ObservePromptTokens` 用 provider 报告的精确 prompt 大小做 EWMA 校准（夹在 0.25×–4×，估算值 ≥ 1 000 token 才更新）。**压缩过的轮次不参与校准**，否则会把「刚压掉的量」误当成估算器的系统性高估。
- **中文 per-rune 权重刻意未改**（AUDIT-P1-05 残留）：审计说「1 rune = 1 token 高估 2–4×」，但同一条给出的实测区间是 0.6–1.5 token/rune —— 两个说法互相矛盾，1.0 落在区间内。没有真实 tokenizer 可比对时改成另一个凭感觉的常量是用猜测换猜测；上面的 usage 回灌恰好让这个静态常量变得不重要。**`count_tokens` API 仍未接**，属独立工作量。两项都记在审计文档里。
- **压缩熔断器可复位**（AUDIT-P1-06）：此前 `failures` 只在成功时归零，而熔断打开后压缩根本不再执行 → **永远不可能有下一次成功**，累计 3 次失败等于本会话永久关闭压缩。改为记 `lastFailureAt`，距上次失败超过 `FailureResetAfter`（默认 10 分钟）时清零。
- **`CooldownTurns` 补默认值**（AUDIT-P1-06）：此前是唯一漏配默认值的字段，冷却实际为 0。
- **hard facts 改按时近性截断**（AUDIT-P1-06）：`uniqueSortedLimit` 先从尾部倒扫去重取前 N、再排序渲染。此前先排字母序再切头 40 条 —— 一个刚编辑的 `web/**` 文件会仅因路径排序靠后被丢掉，而第一轮的 `.github/**` 却留着。
- **wiring 类改动都有断言测试**：`TestNewQuerySessionWiresAutoCompactByDefault` 与 `TestSubagentCompactorDefaultsFromSettings`，参照 `TestRunStartsAgentTaskReaper` 的思路 —— 单独一行 `AutoCompact:` 被重构删掉时不能让测试继续全绿。为此给 `query.Session` 加了 `AutoCompactEnabled()` 访问器，并把包内原有两处 `s.compactor != nil` 改为调用它。
- 验证：`go test ./... -count=1` 全 ok / 0 FAIL；`go test -race ./internal/compact ./internal/query ./internal/agentruntime -count=1` 通过无 race；`go vet ./...`、`gofmt -l .`、`git diff --check` 均无输出；**mutation check 15 项逐条移除修复后重跑，15/15 均被对应测试抓到**。

## 2026-07-25（真优雅退出 + 请求体上限 + provider 分段超时 · AUDIT-P0-07 / 09 / 13）

闭合 [AUDIT-P0-07 / 09 / 13](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p0-07--09--132026-07-25)，第 1 周 P0 清单至此全部关闭。

- **graceful shutdown 三重失效一起修好**（AUDIT-P0-09）：①`Serve` 在 `Shutdown` 关闭 listener 的瞬间就返回 `ErrServerClosed`，原实现立刻 `return nil`，后台那个 `Shutdown` 的等待被直接丢弃 —— 现在 `serveHTTPLifecycle` 真的等排空完成；②`main.go` 改用 `server.ShutdownSignals()`，**补上 SIGTERM**（systemd / k8s / `docker stop` 默认发的都是它，只捕 SIGINT 等于在容器里完全没有优雅退出）；③`Shutdown` 带 deadline，且**先主动终止 SSE/WebSocket**（这类连接永远不会变 idle，单靠 `Shutdown` 只会耗光 deadline 再硬断），给 SSE 发 `server_shutdown` 事件后再关，deadline 到了 `srv.Close()` 兜底，不需要 SIGKILL。shutdown context 用 `context.WithoutCancel(ctx)`，否则父 ctx 已取消会让 drain 瞬间中止。
- **请求体上限与读侧超时**（AUDIT-P0-13）：`MaxBytesReader` 默认 10 MiB（`MaxRequestBodyBytes<0` 可显式关闭），超限报 413 而非语义错误的 400；`MaxMultipartMemory` 8 MiB；`ReadHeaderTimeout` / `ReadTimeout` / `IdleTimeout` 补齐挡住 Slowloris。**`WriteTimeout` 刻意保持 0**，改由 `guardedWriter` 在首次写出时才武装、每次写出续期、对 SSE/WebSocket 彻底清掉 —— 固定的 `WriteTimeout` 从请求开始计时，会砍断「跑完整个 agent turn 再一次性返回 JSON」的 `/query`。
- **provider HTTP 分段超时与连接池**（AUDIT-P0-07）：新增 `internal/anthropic/httpclient.go`。**刻意不用 `http.Client.Timeout`** —— 那是覆盖「连接 + 请求 + 读完整个 body」的硬 deadline，会把几分钟的长流式回答直接砍断。超时拆成建流 / 流空闲 / 非流式 body 三段；建流那段自己计时而非用 `http.Transport.ResponseHeaderTimeout`，因为后者**只在 HTTP/1 生效**而官方端点会协商到 HTTP/2。自定义 Transport 取代 `http.DefaultTransport`（其 `MaxIdleConnsPerHost=2` 会让并发子代理反复做 TCP+TLS 握手）。原有的 DNS/connect/TLS/TTFB phase 遥测保持不变并有测试锁住。
- **已知残留**：mobile 路由上未声明 `Content-Length` 的超大请求体由各自 bind 错误路径报 400 而非 413 —— 上限本身照样生效，只是状态码不统一。
- 合并 review：`server.go` 的 `Run` 同时被本条（改为委托 `serveHTTPLifecycle`）和 AUDIT-P0-10（插入 reaper 的 `defer`）改动，rebase 必冲突且**取任一边都会静默丢掉 reaper wiring**。已手工解为两者并存，并由 `TestRunStartsAgentTaskReaper` 与 `TestStartAgentTaskReaperStopReturnsWithoutParentCancel` 双向锁住 —— 后者尤其关键：本条新增的 `if !errors.Is(serveErr, http.ErrServerClosed) { return serveErr }` 正是一条「父 ctx 仍存活的提前返回路径」，若 reaper 的 `stop()` 还是裸 `<-done`，合入当天就会在 listener 出错时让进程永久挂死。
- 验证：`go test ./... -count=1` 全 ok / 0 FAIL；`go test -race ./internal/server ./internal/anthropic ./internal/agenttasks -count=1` 通过无 race；`go vet ./...`、`gofmt -l .`、`git diff --check` 均无输出。

## 2026-07-25（detached agent runner 抗 panic 与重启 · AUDIT-P0-10）

闭合 [AUDIT-P0-10](docs/audit/2026-07-25_capability_audit.md#修复证据--audit-p0-102026-07-25)：detached agent task runner 此前一次 panic 就能打死整个进程，进程退出后任务永久停在 `running` 且无人回收，起 runner 也没有任何并发上限。

- **后台 goroutine panic 兜底**：新增 `internal/server/background.go` 的 `goSafe` / `recoverBackgroundPanic`，把 panic 收敛成 `server.background.panic` 遥测事件；properties 只放标识性字段与截断栈，不放 prompt/响应正文/token。detached runner 另用 `recoverAgentTaskRun`，除记录外**还把任务显式置 failed**（落库走 `context.WithoutCancel` 副本，避免 runCtx 已超时写不进去）。`gin.Recovery` 覆盖不到这些脱离请求的 goroutine，这是它们此前唯一的兜底缺口。
- **stale task reaper**：新增 `internal/server/agent_task_reaper.go`，启动时先扫一轮回收上次进程留下的孤儿，之后按 `clamp(run_timeout/4, 1min, 10min)` 巡检。跨租户扫描但**写回按 `(tenant_id, user_id, id)` 精确匹配自己读到的那一行**，且仅在任务仍为 `running` 时才置 failed，不会覆盖真 runner 的正常结果。
- **并发上限**：新增 `agentTaskRunLimiter`，默认 16，超限返回 `503` + `Retry-After: 5`，任务原样留在 `ready` 可重试 —— **不排队**，排队等于把压力转成内存占用。容量检查刻意放在改 status 之前，否则会留下没人跑的 `running`。可配 `GOLANG_CLAUDE_CODE_AGENT_TASK_MAX_CONCURRENT_RUNS`。
- **合并前 review 修掉的死锁**：reaper 原本直接跑在父 ctx 上、`stop` 是裸 `<-done`，而 `Run` 里它是 `defer start(...)()`。`Run` 存在不经过 ctx 取消的返回路径（`Serve` 因 listener 错误返回时父 ctx 仍活着），那条路径上 `Run` 会**永久挂死**。已有测试只覆盖「ctx 取消后能停」，恰好落在缺口外。改为 reaper 跑在派生 context 上、`stop` 先 `cancel()` 再等；新增 `TestStartAgentTaskReaperStopReturnsWithoutParentCancel`，已验证修复前失败（卡满 2s 超时）、修复后通过。**这条与 AUDIT-P0-09 直接相关**：graceful shutdown 每给 `Run` 多加一条提前返回路径，就多一处挂死机会，两边各自测试都绿、合到一起才炸。
- 验证：`go test ./... -count=1` 全 ok / 0 FAIL；`go test -race ./internal/server ./internal/agenttasks -count=1` 通过无 race；`go vet ./...`、`gofmt -l .`、`git diff --check` 均无输出。

## 2026-07-25（首个 CI + 修 5 个已调用 CVE + 工具链可复现）

闭合审计 [AUDIT-P0-16 / P0-17 / P0-20](docs/audit/2026-07-25_capability_audit.md#3-p0--工程根因) 与 AUDIT-P2-07。
三项一起做，因为互为前提：CVE 修复要先定 Go 版本，CI 又必须 pin 同一版本才有意义。

- **首个 CI**（`.github/workflows/ci.yml`）：push / PR / 手动触发，四个独立 job —— `build`（`go build` + `go vet` + `gofmt -l` 非空即失败 + `go mod tidy` 无 diff）、`test`（`go test -race ./... -count=1`，60 min timeout）、`govulncheck`、`web`（`npm ci` + build + vitest）。`GO_VERSION` 显式 pin `1.26.5`，与 `.tool-versions` 同源。`govulncheck` 独立成 job，避免日后新 CVE 遮蔽 build/test 信号。
- **5 个已调用 CVE 清零**：`golang.org/x/text` v0.31.0→v0.39.0（GO-2026-5970 无限循环）、`quic-go` v0.54.0→v0.59.1（GO-2026-5676 + GO-2025-4233 QPACK DoS）、`goldmark` v1.7.13→v1.7.17（GO-2026-5320 XSS，经 glamour 从 TUI 可达）、stdlib `crypto/tls` GO-2026-5856 由 pin 的 Go 1.26.5 修复。`govulncheck ./...` 从「affected by 5 vulnerabilities」变为 **`0 vulnerabilities`**，darwin 与 linux 一致。**只升级修 CVE 所需的最小集合**：`anthropic-sdk-go`（落后 15 个 minor）与 `mcp-go`（落后 12 个）跨度大、需独立评估，本次未动。
- **`go` 指令 1.24.2 → 1.25.0**：quic-go v0.59.1 的要求，非主动抬高。
- **工具链可复现**：新增 `.tool-versions`（`golang 1.26.5` / `nodejs 22.23.1`）、`web/.nvmrc`、`web/package.json` 的 `engines.node`（`^20.19.0 || >=22.12.0`，`vite@8` 的真实下界），README 新增「工具链要求」章节。**故意不加 `go.mod` 的 `toolchain` 指令**——本机 `GOSUMDB=off` 会让 Go 拒绝校验下载的 toolchain（即使已在缓存中），从而让仓库内所有 go 命令失败；约束改由 CI + `.tool-versions` + README 承担。
- **termenv fork 不再隐形**（AUDIT-P2-07）：新增 `third_party/termenv/PATCH.md`，声明 fork 已冻结、记录相对上游 v0.16.0 的唯一改动（`termenv_unix.go` 的 `backgroundColor()` 删掉 OSC 11 查询以修 TUI 输入泄漏）及原因，并给出可复跑的 diff 校验命令。
- **顺带补上审计的一处未验证边界**：前端测试首次实跑（Node 22.23.1，11 文件 116 测试全 passed）。同时发现 `web/` 有**真实的 peer dependency 冲突**（`typescript@6` vs `openapi-typescript@7.13` 要的 `^5.x`），裸 `npm ci` 必然 ERESOLVE 失败，当前以 `--legacy-peer-deps` 绕过并在 CI 与 README 注明；真修法随 AUDIT-P0-18 的 api-types 链路一并处理。
- **CI 首跑即抓到一个真实缺陷并修掉**：`internal/tui` 有 5 个测试只在原作者机器上能过 —— welcome header 与 mode hint 的路径经 `abbreviateHome()`（读 `$HOME`），而 fixture 和 3 个 golden 固定了某个开发者的 home 前缀，并断言渲染成 `~/GolandProjects/...`；GitHub runner 的 home 是 `/home/runner`，前缀不再被缩写，测试全挂。给该包加 `TestMain` 把 `HOME`/`USERPROFILE` pin 成 fixture 假设的前缀（**仅测试进程，不动生产代码，goldens 原样保留**）。本地用 `HOME=<临时目录> go test ./...` 复现了同样的失败，并确认全仓仅 `internal/tui` 依赖 ambient `$HOME`。这条印证了 CI 的价值：审计期「69 包全 ok」是在单台机器上取的，**本机全绿 ≠ 可复现地绿**。
- 验证（Go 1.26.5，与 CI 同版本）：`go build ./...`、`go vet ./...`（darwin 与 `GOOS=linux` 交叉均通过）、`gofmt -l .` 无输出、`go test -race ./... -count=1` **69 包全 ok / 0 FAIL / 无 DATA RACE**（真实 `$HOME` 与 CI-like `$HOME` 两种环境下各跑一遍）、`go mod tidy` 幂等、`govulncheck ./...` 0 漏洞、前端 build + 116 测试全绿。

## 2026-07-25（危险命令分类器改 AST + macOS 网络隔离 · AUDIT-P0-03 / 04）

- **危险命令分类器改 mvdan.cc/sh AST**（AUDIT-P0-03）：修复前实测 18 例、16 例绕过。根因是所有 pattern 锚定 `(^|[;&|]\s*)`，而 `normalizeCommandForRisk` 把换行压成空格，多行脚本第二行起永远匹配不到锚点。新增 [internal/shellcmd](internal/shellcmd/shellcmd.go) 用仓库已依赖的解析器把脚本摊平成「实际会执行的命令」列表，天然覆盖换行、子 shell、花括号组、`if/for/while/case`、函数体、命令替换，并递归展开 `bash -c` payload 与喂给 shell 的 heredoc；程序名归一化让 `rm` / `/bin/rm` / `\rm` / `"rm"` 收敛为同一个名字。
- **`UnwrapCommand` 前缀剥离补全并被三处复用**（AUDIT-P0-03）：补 `doas`/`nohup`/`setsid`/`timeout`/`nice`/`ionice`/`stdbuf`/`xargs`/`time`/`exec`，并正确跳过带值 flag（`sudo -u root rm -rf /` 现在解析到 `rm` 而非 `root`）。`internal/sandbox/command.go` 改为委托同一实现，沙箱的 mutating-path 检查同步获得覆盖。
- **提示层与硬拒绝层合并为单一规则表**（AUDIT-P0-03）：新增 [shellrisk.go](internal/permissions/shellrisk.go)，每条规则带 `id`/`reason`/`deny`；`ClassifyRequestRisk` 与新增的 `HardDenyShellReason` 读同一张表，`internal/tools/bash/security.go` 从 9 条独立正则改为薄封装。此前两套 pattern 已漂移——`rm -r -f /` 在提示层算 destructive、在硬拒绝层漏网——这种不一致现在从结构上不可能发生。规则改为结构化判定：`rm` 同时认 `-rf`/`-r -f`/`-Rf`/`--recursive --force`，`"$HOME"` 解析为 `$HOME`，`curl … | sh` 与 `bash <(curl …)` 通过 pipeline / process substitution 连边识别。解析失败不放行，回退到分隔符切片后跑同一套规则。
- **PowerShell 仍走正则但修掉换行**（AUDIT-P0-03）：`mvdan.cc/sh` 不解析 PowerShell，`normalizePowerShellForRisk` 先把换行转成 `;` 再压空白，锚点对每条语句重新生效。
- **macOS 沙箱补上网络隔离**（AUDIT-P0-04）：darwin 的 seatbelt profile 此前唯一 deny 原语是 `(deny file-write* (subpath "/"))`，`NetworkDisabled` 在 darwin 分支从未被引用，实际执行者只是一个 20 个命令名的白名单。现在 `NetworkDisabled` 时输出 `(deny network*)`。**已用真实 `sandbox-exec` 端到端验证**：同一个 loopback listener，未沙箱连接成功、沙箱内被拒；移除该规则后沙箱内连接恢复成功，证明它确实是唯一执行者。`ls`/`git`/`go`/`python3` 等本地工具不受影响。
- **不可强制的网络配置不再静默失效**（AUDIT-P0-04）：新增 `NetworkPolicyGaps(cfg)` 显式列出本平台无法对任意 shell 命令强制的选项（非 darwin/linux 的 `network.disabled`、`allowDomains/denyDomains` 只作用于内置 HTTP 工具、`proxy.required`/`mitm.required` 只对已知命令名生效）；`UnavailableReason` 汇总上报，`PrepareShell` 在 `sandbox.failIfUnavailable` 为真时直接报错拒绝启动。
- **新登记 AUDIT-P1-35**：`sandbox.IsAvailable` 与 `sandbox.UnavailableReason` 全仓零调用方，用户可见的沙箱状态只有 `tuiSandboxLabel` 且它直接读 settings，所以上述「显式上报」目前只到 API 层。
- 顺带删除死代码 `isDangerousShellCommand`（无调用方，位于被重写的函数中）。
- 验证：新增 15 个测试（含 24 行绕过金标表、33 条规则可达性、24 条日常命令不误报、两层一致性、真跑 `sandbox-exec` 的端到端拦截），每个均已验证「移除修复后失败、加上修复后通过」。`go test ./... -count=1`、`go test -race`（permissions / shellcmd / sandbox / tools/bash）、`go vet ./...`、`gofmt -l .`、`git diff --check` 全部通过。

## 2026-07-25（权限/沙箱边界修复 · AUDIT-P0-01 / 02 / 05）

修复 [docs/audit/2026-07-25_capability_audit.md](docs/audit/2026-07-25_capability_audit.md) 中三条同属权限/沙箱边界的 P0 漏洞（改动区域重叠，一次提交完成）。

- **权限模式不再折叠成 `allow`**（AUDIT-P0-01）：`NormalizeMode` 引入 canonical 常量 `allow`/`ask`/`deny`/`acceptEdits`/`bypassPermissions`。`acceptEdits` 成为独立模式，只自动放行 `Write/Edit/MultiEdit/NotebookEdit`，Bash / PowerShell / MCP 一律走 ask 流程，敏感路径写入即便是文件编辑也仍提示。**语义重新确认**：`default` 与 `delegate` 归为 `ask`（上游 `default` 是「首次使用时提示」，`delegate` 是「交给用户决定」，都不是 allow）；`auto` 保留 `allow`（本项目自有的「自动执行 + 分类器兜底」模式，从来不是 bypass）。
- **`Bypass` 只能被显式置位**（AUDIT-P0-01）：新增 `permissions.ModeGrantsBypass` 作为唯一判定入口，`guarded.go` 不再用 `mode == "allow"` 推导 bypass；收窄 runtime 模式会撤销 session 级 bypass。
- **Deny 检查移到 Bypass 短路之前**（AUDIT-P0-01）：即便 `--dangerously-skip-permissions`，显式 deny 规则仍然生效；`applyRuntimeOptions` 相应改为**保留** settings 里的 deny 列表（原先整体清空会让这条修复失去意义）。TODO-014 的「高风险 Bash 在 skip 模式下不触发 prompt」与 TODO-016 的持久授权行为均不变。
- **deny 子树规则对写入工具生效**（AUDIT-P0-02）：`isPathQualifierTool` 扩展到 `Write/Edit/MultiEdit/NotebookRead/NotebookEdit/Bash/PowerShell`。此前 Go 的 `path.Match` 的 `*` 不跨 `/`，`Write(~/.ssh/**)` 挡得住 `~/.ssh/x` 却挡不住 `~/.ssh/keys/id_rsa` —— 静默失效的 deny 比没有 deny 更危险。
- **默认写保护与沙箱开关解耦**（AUDIT-P0-05）：沙箱默认关闭，此前把默认保护包在 `if sandbox.Enabled` 里等于默认状态下 `.claude/settings*.json`、`.git/hooks`、`.git/config` 全部可写，agent 可自我提权或写 git hook 持久化。现在这四类路径**无条件**拒绝（`.git/config` 必须同列，否则可用 `core.hooksPath` 绕开 `.git/hooks`）；`.claude/skills` 维持仅沙箱开启时拦截，让默认配置下的 skill 编写仍可用。
- CLI `--permission-mode` help 文本与 `help.txt` 金标同步说明两个模式的真实区别；`agents.go` 的 `permissionMode` 校验补上 `acceptEdits`。
- 验证：新增 9 个测试（`TestPolicyBypassStillHonoursDenyRules`、`TestPolicyAcceptEditsOnlyReleasesFileEdits`、`TestPolicyDenySubtreeMatchesDeepPaths` 10 组深层路径金标、`TestModeGrantsBypass`、`TestEnsureWritablePathDefaultProtectionWithSandboxDisabled`、`TestGuardRuntimeAcceptEditsDoesNotBypassBash`、`TestGuardBypassScope` 等），每个均已验证「移除修复后失败、加上修复后通过」。`go test ./internal/permissions ./internal/tools ./internal/cli ./internal/query -count=1`、`go test ./... -count=1`、`go vet ./...`、`gofmt -l .`、`git diff --check` 全部通过。

## 2026-07-25（全项目独立审计）

新增 [docs/audit/2026-07-25_capability_audit.md](docs/audit/2026-07-25_capability_audit.md)，作为后续修复的唯一执行清单（34 条 P0/P1 + 12 条 P2，每条带 `file:line` 证据、稳定 ID 和验收标准）。纯文档改动，不触碰任何代码路径。

- **基线（实测）**：`go build ./...`、`go vet ./...`、`go test ./... -count=1`（69 包全 ok / 0 FAIL）、`gofmt -l .`、`go test -race` 全部通过；核心包覆盖率 `permissions` 86.5% / `query` 82.5% / `tui` 82.5% / `session` 80.6% / `server` 74.0%。
- **文档诚信度核验**：`compatibility_matrix.md` 引用的 21 个测试函数与 11 个脚本 32/32 真实存在，抽查 6 条 DONE 条目实跑全过，`internal/cli` 零 stub。**无系统性文档造假**。
- **三个结构性断层**：①安全边界有真实可利用漏洞（`acceptEdits`/`default`/`auto` 被折叠成 `allow` 并触发 `Bypass`，而 `Bypass` 短路在 Deny 之前；deny 子树规则对 Write/Edit/Bash 静默失效；危险命令分类器 17 例实测 16 例绕过）；②运行时外壳仍是单机形态（graceful shutdown 三重失效且不捕 SIGTERM、LLM client 无 timeout、DB 连接池全默认、请求体无上限）；③零 CI 让质量成果无人固化（5 个已调用 CVE、swagger 漂移 19 个 commit、80 个 acceptance 脚本中的 release-gate 类已指向不存在的目录）。
- **诚实标注更正**：TODO-049 标 DONE 但其 P0-4（结构化 `file_change` task event）实际未实现，已在 `docs/todo.md` 就地更正并登记为 AUDIT-P2-10。
- **审计边界**（未验证项，不得据此宣称结论）：前端测试未跑（本机 Node 18 < 要求的 20）、Linux/Windows/WSL 沙箱未验证（平台不可得）、未做真实压测、未连接真实 MCP server。

## 2026-07-25（AskUserQuestion 真交互）

AskUserQuestion 从「无条件返回 `is_error` 结果、TUI 显示 × 未完成：操作未完成」升级为真交互：TUI 拦截调用弹窗让用户作答，答案以正常（非 error）tool_result 返回模型。

- **四层链路，完整镜像 PermissionPrompt**（`f6bca00`/`856b546`/`a24fe27`/`a19b434`）：`tools.Context.UserQuestion` 回调 → `query.Options.UserQuestionPrompt` 透传 → cli `tuiUserQuestionPrompt`（`StreamUserQuestion` 事件 + buffered reply channel 往返）→ TUI `pendingQuestion` 弹窗。作答返回 `User answered: <answer>`；取消/非交互模式（print/headless/子代理）退回原 `User input required` error 兜底，行为平滑降级。
- **弹窗交互二版：常驻输入框**（`069e14c`）：选项列表末尾固定一行 `N. 输入: _▌`，在选项行敲非数字字符自动跳输入框并录入（"打字即输入"）；数字 1-9 快选、Enter 确认、Esc 有内容先清空再取消；无 choices 时输入框即唯一行默认聚焦。无独立文本模式状态。
- **工具描述改渠道优先**（`7dd321b`）：提问门槛保持严格（不问就无法继续才问），但一旦决定问用户——含闲聊追问——必须走工具弹窗，禁止正文列编号选项；补上交互模式真实语义。
- **TUI 摘要渲染**（`da5542f`）：工具调用行 detail 显示问题文本（rune 安全截断）；`User input required` 兜底摘要改「等待用户输入」；作答结果显示「用户选择：<answer>」。
- **truncate 中文乱码修复**（`b477fa3`）：`truncate()` 委托 rune-width 安全的 `truncateDisplay`，杜绝工具参数预览把多字节字符切半显示 �。
- 真机验收：session `add052c4` 两次调用（选项选择 8.5s / 自由输入 11s）均 `success: true`、非 error 结果。设计与计划见 [docs/askuserquestion_interactive_design.md](docs/askuserquestion_interactive_design.md)、[docs/askuserquestion_interactive_plan.md](docs/askuserquestion_interactive_plan.md)。

## 2026-07-23（redo 带文件还原）

`redo` 从「只移动对话指针」升级为「同时把工作区文件重放成目标分支末端的状态」，成为 rewind 的真正逆操作——此前 redo 回旧分支后对话回来了、磁盘文件却仍停在 rewind 还原后的状态（对话与文件分裂）。

- **默认开 + 逃生口**：`session redo <id> <leaf>` 与 `/redo <leaf>` 默认还原文件 + 移动指针；加 `--conversation-only` 只移动指针（旧行为）。与 rewind 的 `--conversation-only|--files-only` 对称。
- **两链差集共用**：新增 `v2FileRestores(fromChain, toChain)`——`commonPrefixLen` 求分叉点，规则①「仅在被弃分支出现的 path → 还原到分叉点 before」＋规则②「目标分支出现的 path → 重放到该分支末端 after（`afterAsBefore` 把 `After*` 投影成 `Before*`）」。`rewindConversationV2` 改走同一函数；rewind 时目标后缀为空 → 退化为原 `parseFileChanges`，行为等价、既有 rewind 文件测试全绿。
- **tombstone 严格原子中止**：目标分支某 path 的末端 after 不可取（turn 去重丢弃的 lite 条 / blob 被 GC 回收）时，**整体中止 redo（指针也不动）**并报清晰错误，复用 `runFileRestoreTransaction`「全有或全无、失败全回滚」，绝不静默写坏文件。给 `fileChangeSnapshot` 补 `AfterSHA256` 以区分「真·空文件」与「被截断的 lite 末态」。
- **纯 best-effort**：未反转 turn 级去重与「GC 按当前链回收放弃分支」两处刻意的省空间设计（若日后 tombstone 过于常见，再单独立项「不 GC 存活分支 blob」增强）。

详见 [docs/transcript/redo_file_restore_plan.md](docs/transcript/redo_file_restore_plan.md) §10。

## 2026-07-23（transcript v2 消息图默认开启）

本轮工作主线：**transcript v2 消息图 + 非破坏性 rewind**（P2 阶段 A–F 落地并**默认开启**）。

transcript 从「一条线性流水账」升级为「append-only 消息图」：每条消息带 `id`+`parent_id` 连成树，一条 append-only 的 `branch_head` 指针记录「当前叶」；所有消费方先 leaf-walk 出「当前链」再处理（`session.CurrentChain`/`LoadConversation`）。**物理行序 ≠ 逻辑对话序**。

### 阶段 A–F

- **A 图写入** (`04c6890`)：Entry 加 `schema`/`parent_id`/`leaf_id`/`reason`；首行 `session_meta`；Recorder v2 模式（打 schema、按 parent_id 串链、每 turn append `branch_head`）；active leaf 由文件序回放 branch_head + 链增长解析。
- **B leaf-walk resume** (`95ffe29`)：`ValidateResumeFormat` 放开 v2；resume 从当前链构建 messages（复用既有 sanitize），`--resume-at` 用 `ChainToLeaf`。
- **C 非破坏 rewind + redo + branch list** (`757a393`)：`RewindConversationToMessage` 改 append `branch_head`（不再 O_TRUNC），旧分支保留可 redo；新增 `Redo`/`Branches` 与 `session redo`/`session branches`。
- **D messageId 键控文件历史** (`94b21b5`)：`file_change` 记 `message_id`；rewind 文件还原＝两链节点差集；`GCFileHistory` 窗口按当前链 turn 计数；命中被回收 blob 报 tombstone。
- **E 分支查看/对比** (`d428b99`)：`BranchInfo.ForkPoint`、`CompareBranches`；`session branches [--compare A B]`。
- **F 原版 Claude Code import** (`3c2e58a`)：`transcript import-claude-code` 只读导入原版 uuid/parentUuid 主链 → best-effort 生成可 resume 的 v2 新会话，sidechain/system 跳过并报告。

### 默认开启 + 写侧收口

- **默认开启** (`ece4563`)：新会话默认写 v2；opt-out 走 `GOLANG_CLAUDE_CODE_FEATURE_TRANSCRIPT_V2=0` 或 `featureFlags:{transcript_v2:false}`；新增 `config.FeatureEnabledDefault`。resume 永远随磁盘既有 schema 续写，绝不混写。
- **对话读路径统一 `LoadConversation`** (`6f6ce6d`)：compact/recap/export/rewind 候选走当前链，被放弃分支不折进只读输出；会话内续跑 `resumeContinuationContext` 按当前链取增量；TUI 增 `/branches`、`/redo` (`a77af59`)。
- **写侧一致性** (`ece4563`,`27c940f`)：schema 检测 v1/v2 同族归 v2（杜绝 auxiliary 追加行判 mixed）；`compact_summary` 经 live recorder 追加以推进 active leaf；recap 打 v2 tag 但不入链、查找走整文件；checkpoint 版 `session rewind` 对 v2 也走非破坏、`Store.Checkpoint` v2 入链。

真实 provider（deepseek-v4-flash）live 端到端验收通过：写入→resume 续跑→非破坏 rewind→并列分支→compare→redo→compact→compact 后 resume，全绿。

### 修复

- **`/rewind` 后不产生分叉（回退被静默抵消）+ 系统性收口**：长驻 TUI 进程里 `/rewind` 旁路把 `branch_head` 写入文件，但未同步仍打开的 live recorder 内存 `leaf`，导致下一轮从回退前旧 tip 线性接续、`/branches` 只剩 1 条。**根治**：新增 `Recorder.SyncLeafFromDisk()`，并在 `query.Session.run` **每个 turn 起点、首次 append 前**统一重算 recorder 的 active leaf——一处覆盖 `/rewind`、`/redo`、by-id `compact`、乃至其他终端 CLI 等一切旁路改动，从根上杜绝「旁路写 branch_head 未同步 recorder」这类。详见 [docs/transcript/live_recorder_leaf_sync_after_rewind_fix.md](docs/transcript/live_recorder_leaf_sync_after_rewind_fix.md)。
- **完成度闸门 `<system-reminder>` 泄漏进 transcript 与 /rewind 选择器**：闸门续跑提醒（`Post-Action Delta Gate` 等）本是请求态运行时提醒，却被 `recordMessage` 持久化成 user 消息，污染 resume/inspect 且被 `/rewind` 当成可回退轮次列出（用户误以为提示词外泄）。修复：① 闸门分支不再持久化 nudge（仍留 live 请求驱动重试，审计由 `completion_gate` 事件承担）；② `rewindCandidates` 过滤 `<system-reminder>` 开头的运行时提醒，兼容已污染的历史会话。详见 [docs/transcript/system_reminder_rewind_leak_fix.md](docs/transcript/system_reminder_rewind_leak_fix.md)。

详见 [docs/transcript/transcript_v2_message_graph_and_nondestructive_rewind_plan.md](docs/transcript/transcript_v2_message_graph_and_nondestructive_rewind_plan.md)、[docs/session_quickstart.md](docs/session_quickstart.md) §7.5。

## 2026-07-23

本轮工作主线：**TUI 实时状态** + **Sub-agent 模型分级** + **Provider 中立化**。

### TUI

- **等待期动画心跳** (`55d73a5`)
  长任务等模型 API 返回（工具已跑完、下一条 assistant 消息未到）时，主体区曾完全静止——`liveTranscriptView` 因 `timelineLiveView` 非空而短路了唯一绘制动画状态行的分支。现在 busy 且无活跃 running 片段时，在主体区底部追加一行盲文 spinner + 阶段文案 + 整轮 elapsed 的心跳（纯 viewport 覆盖层，turn 结束即消失、不入 scrollback）。
  详见 [docs/prompt_logic/tui_tool_progress_enhancement.md](docs/prompt_logic/tui_tool_progress_enhancement.md)。

### Sub-agent 模型分级（model tiering）

- **子代理模型 provider 感知解析 + 可配置档位映射** (`3e08320`)
  修复非 Anthropic provider（如 GLM）下内置 Explore（`Model:"haiku"`）被解析成 `claude-haiku-4-5` 并发给 GLM 端点的问题。tier 别名（sonnet/opus/haiku）仅在父模型为 Anthropic 时展开成家族模型；否则命中 `settings.subagentModelTiers` 用配置模型，未配置则继承会话模型。
- **Task 工具动态 model 参数** (`fe48ba4`)
  Task/批量项新增 `model` 档位参数，透传到 `agentruntime.Request.Model`，由上面的解析器按「请求 > agent 定义 > 父」优先级处理。
- **Task 工具 effort 透传 + 批量成本可观测性** (`1804a96`)
  新增 `effort`（low/medium/high/max）参数注入思考预算；批量响应每项带 `model`/`input_tokens`/`output_tokens`/`cost_usd`，summary 带 `total_*`。
  详见 [docs/subagent_multiagent/subagent_model_tiering_plan.md](docs/subagent_multiagent/subagent_model_tiering_plan.md)。

### Provider 中立化

- **P0① `/v1/models` 反映真实 providers** (`add5796`)
  无显式 Models 时兜底 `config.ConfiguredModels(cwd)`（主模型 + fallback provider models），不再列 Anthropic `KnownModels`。
- **P0② 计价配置化** (`add5796`)
  新增 `settings.modelPricing`（每百万 token 单价）；`session.EstimateCostUSDWithRates` 命中配置单价即用、否则回退内置 Anthropic 表，让非 Anthropic 的成本可观测性不再恒为 0。
- **P1③ 出厂默认模型去 Anthropic** (`53665dc`)
  `ResolveModel` 在无 explicit/project/env/顶层 model 时优先取已配置 provider 的 model，再退内置默认，避免非 Anthropic 会话默认到无法服务的 `claude-sonnet-4-6`。
  完整方案与剩余项（P2⑤ cache_control 门控、P3 不做）见 [docs/provider_neutrality_plan.md](docs/provider_neutrality_plan.md)。

### 新增用户可配置项（settings.json）

- `subagentModelTiers`：非 Anthropic provider 下 tier 别名 → 具体模型映射。
- `modelPricing`：per-model 每百万 token 单价，用于成本可观测性。
