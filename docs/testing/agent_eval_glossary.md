# Agent 能力评测术语表

本文档统一解释 go-claude 能力评测、APG、OpenCode baseline、release gate 和相关证据链中常见术语。目标是避免把“能跑通”“能比较”“能证明超过原版”混为一谈。

## 核心结论

- **APG 是外部评测平台，不是 go-claude 本体功能。**
- **Smoke 只证明调用链路和环境基本可用，不证明能力优势。**
- **Report 证明某个 agent 在某批任务上的结果。**
- **Compare 证明多个 agent 在同一批任务上的相对结果。**
- **Release gate 是 go-claude 发布前消费多类证据的门禁。**
- **Bounded evidence 只能支撑有边界的优势结论；open-world superiority 需要更大样本和原版 Claude Code baseline。**

## 术语速查表

| 术语 | 全称/中文 | 含义 | 能证明什么 | 不能证明什么 |
| --- | --- | --- | --- | --- |
| APG | Agent Proving Ground / Agent 拉练平台 | 独立的 agent 评测仓库，用同一套 suite、case、scorer 和 artifact 评测不同 agent。 | 某个 agent 在指定任务集上的真实执行结果。 | go-claude 本体行为已改变；开放世界能力已超过所有 baseline。 |
| Smoke | 冒烟测试 | 小规模、低成本的连通性/可用性检查。 | adapter、CLI、模型、鉴权、report 采集链路是否基本可用。 | agent 在复杂任务上更强。 |
| APG OpenCode smoke | OpenCode 冒烟测试 | 用 APG 调用 `opencode-local` 跑一个或少量 case。 | OpenCode baseline 当前能不能被 APG 正常调用。 | go-claude 相对 OpenCode 或原版 Claude Code 更强。 |
| Suite | 测试套件 | 一组任务 case、预算、权限、scorer 和运行配置。 | 评测范围和规则固定。 | suite 之外的任务表现。 |
| Case | 单个评测任务 | suite 内的最小任务单元，例如读 marker、修 Go 测试、多文件修复。 | 单个任务是否完成。 | 其他任务族表现。 |
| Stub | 桩 / 替身实现 | 用简化、固定、可控的假实现替代真实外部依赖。 | 测试链路在稳定输入下是否正确。 | 真实模型、真实服务、真实网络环境下的完整表现。 |
| Scorer | 评分器 | 判断 case 是否通过的规则或命令，例如 rule/file/command scorer。 | 结果是否满足明确规则。 | 模型主观质量的全部维度。 |
| Hard gate | 硬门禁 | 必须满足的通过条件，不满足即失败。 | 防止“不完整结果”被算作通过。 | 不能替代更广泛任务覆盖。 |
| Report | 单 agent 报告 | 一个 agent 在一个 suite 上的 JSON/Markdown 结果。 | 该 agent 的 pass/fail、usage、artifact、failure category。 | 多 agent 相对优势。 |
| Compare | 多 agent 对比报告 | 聚合多个 report，对同一批 case 的多个 agent 结果做横向比较。 | 相同任务、相同 scorer 下的 status/efficiency 差异。 | 没有跑进 compare 的 case 或 baseline。 |
| Baseline | 对照基线 | 被拿来比较的 agent，例如原版 Claude Code、OpenCode、Codex CLI。 | go-claude 相对某个对照的差异。 | 没有纳入 baseline 的系统表现。 |
| Target agent | 目标 agent | 本轮重点评估的 agent，当前通常是 `golang-cc-local`。 | 目标在指定任务上的表现。 | 其他 agent 的表现。 |
| Adapter | Agent 适配器 | APG 调用不同 CLI/API agent 的封装层。 | APG 可以用统一协议运行不同 agent。 | agent 内部 prompt 或模型质量。 |
| Profile | Agent 配置档 | adapter 的具体配置，例如命令、模型、环境变量、权限。 | 某个本地 agent 实例如何启动。 | 配置以外的真实账号/模型状态一定可用。 |
| Artifact | 运行产物 | stdout、stderr、transcript、workspace diff、manifest、report 等证据文件。 | 复核一次运行发生了什么。 | 如果缺失或被污染，结论需要降级。 |
| Store | 报告/运行存储目录 | APG 保存 report 和 artifact 的目录。 | 支撑后续复查和 compare。 | 本身不代表任务通过。 |
| Transcript | 执行转录 | agent 的可见输出、工具调用或会话记录。 | 排查 agent 实际做了什么。 | 若 transcript 包含 objective echo，需要避免污染 scorer。 |
| Workspace diff | 工作区差异 | case 执行后产生的文件修改 diff。 | 任务是否按预期修改文件。 | 没有 diff 不代表只读任务失败。 |
| Usage | 使用量统计 | tokens、turns、tool calls、duration、cost 等。 | 效率、成本和闭环轮次。 | 单独不能证明任务质量。 |

## 对比与失败归因术语

这一组术语用于解释 APG 里“是否真的超过原版 Claude Code”的证据链。核心原则是：先固定任务、模型、repeat 次数和 scorer，再谈优势；如果模型、任务集或证据边界不同，只能给有边界的结论。

| 术语 | 中文解释 | 在 APG 里的含义 | 能证明什么 | 不能证明什么 |
| --- | --- | --- | --- | --- |
| Strict two-way compare | 严格两方对比 | 只比较两个 agent，通常是 `golang-cc-local` 和原版 Claude Code `ccsd`；要求同一 suite、同一 case 列表、同一 repeat 次数、同一模型或明确同模型配置。 | 在这个固定任务切片上，目标 agent 是否比一个明确 baseline 更好。 | 不能证明三方优势，也不能证明开放世界所有任务都更强。 |
| Mixed-model compare | 混合模型对比 | 多个 agent 使用的底层模型不完全一致，例如 Go Claude/`ccsd` 用 `sensenova-6.7-flash-lite`，OpenCode 用另一个模型 profile。 | 能作为真实参考和风险观察，帮助发现某些 agent 的明显失败簇。 | 不能直接作为严格能力胜出证据，因为模型差异会污染 agent runtime 差异。 |
| Three-way compare | 三方对比 | 同时比较 Go Claude、原版 Claude Code、OpenCode 等三个 agent 的 report。 | 能观察目标 agent 相对多个 baseline 的位置。 | 如果不是 strict same-model，只能作为 mixed-model reference。 |
| Real-engineering case | 真实工程任务 case | 需要 agent 读真实小项目源码、修实现、跑测试、保持 diff 范围的工程修复任务。 | 比 prompt dump 或 stub gate 更接近真实 coding agent 工作流。 | 仍然只覆盖该 case 设计的任务类型。 |
| Full-25 | 25 个真实工程 case 全量切片 | APG `p2-real-engineering` 当前用于真实工程评测的一组 25 个 base case。 | 在这 25 个任务上的 pass rate、repeat stability、duration 和 failure cluster。 | 不能代表所有语言、所有仓库、所有长任务。 |
| Repeat / Repeat3 | 重复运行 / 三次重复 | 同一个 base case 连续运行多次，例如 `--repeat 3` 会产生 `case#attempt-1..3`。 | 能观察稳定性，避免把一次偶然成功或失败误当成能力结论。 | repeat 次数少时仍不能覆盖模型随机性全貌。 |
| Same-model evidence | 同模型证据 | 被比较 agent 使用相同或可确认等价的模型配置。 | 更能隔离 agent runtime、prompt、工具链和执行策略差异。 | 仍不能消除账号、provider、环境、耗时和工具实现差异。 |
| Scorecard | 评分卡 / 结论面板 | compare 生成的结构化摘要，包含 pass rate、status wins/losses/ties、repeat stability、efficiency、claim level 等。 | 把多个 report 转成可机器读取的发布/判断依据。 | 不能替代原始 artifacts；scorecard 结论受输入 report 质量限制。 |
| Failure cluster | 失败簇 | 按 track、base case、agent 聚合同一类或同一任务上的失败，例如某 case `3/3` 都失败。 | 快速定位最高 ROI 的修复目标：哪个 agent 在哪个任务上反复输、输在什么原因。 | 单个 failure cluster 不能代表整体能力强弱。 |
| Failure taxonomy | 失败分类体系 | APG 对失败原因的结构化分类，例如 scorer failed、command failed、timeout、resource exhausted。 | 防止把环境问题、评分器问题、协议问题和真实能力失败混在一起。 | 分类依赖 artifact 和 scorer 质量，可能需要人工复核。 |
| Resource exhaustion marker | 资源耗尽标记 | 把磁盘不足、环境资源耗尽等问题显式标记到 report/compare 中。 | 说明某次失败可能被本机资源污染，不能轻易算作 agent 能力失败。 | 不证明 agent 本身没有问题，只说明结论需要降级。 |
| Verification claim mismatch | 验证声明不一致 | agent 声称测试或验证通过，但 artifact、命令结果或 scorer 证明并未通过。 | 识别“口头完成”与“实际证据”不一致的情况。 | 不一定说明模型完全不会做任务，可能是收尾、读取结果或报告合成问题。 |
| Forbidden test glob gate | 禁止测试文件门禁 | 当任务要求不要改测试时，用 glob 规则禁止修改或新建测试文件，例如 `*_test.go`。 | 防止 agent 通过改测试、加临时测试或 scratch helper 污染真实修复结果。 | 不能替代功能正确性测试；它只约束 diff 范围。 |
| Bounded status superiority | 有边界的状态优势 | scorecard 在固定任务、固定 baseline、同模型等条件满足时给出的有限优势结论。 | 目标 agent 在这个 bounded slice 上 pass/fail 状态优于 baseline。 | 不是 open-world superiority，也不是所有任务、所有模型、所有环境下的优势。 |

## 真实工程任务扩展术语

这一组术语用于解释 APG 真实工程任务扩展时常见的 fixture、suite、dry-run 和具体 bug 类型。它们的共同目标是：用一批小型但真实的软件工程修复任务，测试 agent 是否会读代码、理解需求、修改实现、运行验证，而不是只会输出说明或改 prompt。

| 术语 | 中文解释 | 在 APG 里的含义 | 典型考点 |
| --- | --- | --- | --- |
| Fixture | 测试夹具 / 任务工作区 | 一份故意写坏的小项目，包含源码、测试和必要配置。APG 会复制 fixture 到隔离 workspace，让 agent 修源码。 | agent 是否能基于真实文件和测试定位问题，而不是凭空回答。 |
| Suite | 测试套件 | 多个 case 的集合，统一定义 agent profiles、预算、权限、fixture、scorers 和通过条件。 | 是否能在同一规则下批量比较 go-claude、原版 Claude Code、OpenCode 等 agent。 |
| Dry-run | 试运行 | 不真正调用模型执行任务，只验证 suite/case/profile/scorer/fixture 路径等配置是否能被 APG 正确加载。 | 配置是否可执行；不能证明 agent 能力。 |
| Loader test | 加载测试 | 验证 APG 能正确读取 suite YAML、case 列表、agent profile 和 fixture 配置。 | 评测平台配置完整性；不能证明模型会修好代码。 |
| Final marker | 最终完成标记 / 验收标记 | case 要求 agent 在最终回答中输出的固定完成标记，例如 `ENGINEERING_DONE` 或某个能力 case 专用 marker。 | agent 是否按任务协议明确交付并闭环。 |
| Final marker missing | 最终完成标记缺失 | agent 可能已经改了代码或跑了测试，但最终回答没有包含要求的 marker，APG 会按协议判为未完整完成或失败。 | 区分“代码可能已修好”和“按评测协议完成交付”；缺 marker 不一定代表代码逻辑错误。 |
| Rate limit | 限流 | 控制某个 key、用户或租户在时间窗口内最多允许多少次请求。 | 独立 key 计数、窗口边界、过期请求清理、超限拒绝。 |
| URL canonicalization | URL 规范化 | 把 URL 转成稳定可比较的标准形式，例如小写 scheme/host、去掉默认端口、排序 query 参数。 | URL parsing、默认端口、重复 query、非法 URL 处理。 |
| LRU cache | Least Recently Used cache / 最近最少使用缓存 | 固定容量缓存，容量满时淘汰最久未访问的 key。 | `Get` 是否刷新 recency、更新已有 key 是否占容量、淘汰顺序是否正确。 |
| Token scope | 令牌权限范围 | token 拥有哪些操作权限，例如 `repo:read`、`billing:*`。 | 精确匹配、命名空间 wildcard、前缀误匹配、安全拒绝。 |
| Time window | 时间窗口 | 判断某个时间是否落在业务窗口内，例如 09:00-17:00 或跨天 22:00-06:00。 | inclusive start、exclusive end、跨天窗口、时区转换。 |
| Metrics aggregation | 指标聚合 | 把多条 metrics sample 按服务、状态或维度汇总成稳定输出。 | 分组汇总、忽略失败样本、排序稳定、计数和总量正确。 |
| Webhook signature | Webhook 签名校验 | 用 HMAC 等签名验证第三方回调没有被伪造或篡改。 | HMAC-SHA256、header 格式、hex 解析、constant-time comparison。 |
| Masking | 脱敏 / 掩码 | 隐藏敏感信息的一部分，同时保留必要识别信息，例如邮箱本地部分脱敏但保留域名。 | 无效输入拒绝、边界长度、隐私保护与可读性平衡。 |
| Order discount | 订单折扣计算 | 计算订单在百分比折扣、固定金额折扣和上限约束后的金额。 | 金额按分计算、折扣上限、不能出现负数、舍入规则。 |
| Inventory reservation | 库存预占 | 下单或支付前先锁定库存，失败或取消时释放库存。 | 可用库存扣减、释放恢复、超卖拒绝、失败操作不污染状态。 |

## 证据边界与阶段收束术语

这一组术语用于解释类似下面这类结论：

```text
这是 bounded evidence，不是 open-world superiority；本阶段停止，不 rerun 失败 case，不开新 fix loop。
```

核心逻辑是：评测证据必须和它的任务集、模型、agent、repeat 次数、环境和 scorer 绑定；如果证据边界有限，就只能给有限结论，不能扩大成“全面超过原版 Claude Code”。

| 术语 | 中文解释 | 在 APG / go-claude 里的含义 | 常见误用 |
| --- | --- | --- | --- |
| bounded evidence | 有边界的证据 | 只在指定 suite/case、agent、模型、repeat 次数、权限、scorer 和运行环境内成立的证据。 | 把某个 batch 的局部结果扩大成所有任务都更强。 |
| open-world superiority | 开放世界全面优越性 | 声称某个 agent 在真实世界各种任务、代码库、语言、模型和环境下整体超过另一个 agent。 | 用一次或几次 benchmark 直接证明“全面超过原版”。 |
| phase stop / wrap-up | 阶段停止 / 阶段收束 | 当前阶段已经到达证据收束点，只记录已有 report、compare、failure boundary 和剩余风险，不继续扩大范围。 | 看到失败后继续追加 run、case、suite 或修复，导致阶段边界不断漂移。 |
| rerun failed case | 重跑失败 case | 对已经失败的 case 再跑一次或多次，试图确认是否偶发或验证修复。 | 在要求收束的阶段补跑失败 case，把原始失败结果冲淡成更好看的结果。 |
| fix loop | 修复循环 | 进入“分析失败 -> 改代码/prompt -> 跑测试 -> 再分析 -> 再修”的闭环。 | 在只要求总结证据的阶段开启新修复，把 bounded evidence 变成持续变动的实验。 |
| evidence boundary | 证据边界 | 结论可适用的明确范围，例如 `newest-10 repeat3`、`sensenova-6.7-flash-lite`、`golang-cc-local vs ccsd`。 | 不写清边界，导致读者误以为结论适用于所有模型、所有任务或所有 agent。 |
| claim downgrade | 结论降级 | 当模型不一致、status loss 存在、artifact 缺失、成本不可比或环境污染时，把结论从 superiority 降为 parity、bounded evidence、not supported 等。 | 明知证据不完整，仍在文档里写成确定性优势。 |

### 判断口径

```text
能证明：当前固定条件下的 pass rate、failure cluster、duration、repeat stability。
不能证明：开放世界全面能力、所有任务族优势、所有模型/provider 下优势。
```

所以，“这是 bounded evidence，不是 open-world superiority” 的意思是：当前结果可以作为有限证据被引用，但必须带上任务、模型、baseline 和运行边界；不能把它包装成 go-claude 已经在开放世界全面超过原版 Claude Code。

## Usage / Cost Normalization 术语

这一组术语用于解释这类推进口径：

```text
继续推进 usage/cost normalization 的下一块。现在 blockers 已经结构化，下一步最高 ROI 是先查 ccsd 是否在 artifacts/stdout/stderr/transcript 里其实有可提取 usage；如果没有，再把它明确固化为“runtime-only blocker”，避免继续猜价格表。
```

核心逻辑是：先证明运行产物里有没有可机器提取的用量，再谈成本归一化；如果原始产物没有 usage，就把结论降级为 runtime blocker，而不是用猜测的价格表硬算成本。

| 术语 | 中文解释 | 在 APG / go-claude 里的含义 | 典型验证点 |
| --- | --- | --- | --- |
| usage/cost normalization | 用量/成本归一化 | 把不同 agent report 里的 token、turn、duration、tool calls、cost 等字段整理成可横向比较的统一口径。 | compare report 中同名字段含义一致；缺失字段要有明确 blocker，而不是 silently 当成 0。 |
| usage | 使用量 | 一次 agent 运行消耗的可计量资源，例如 input/output tokens、turns、tool calls、duration。 | report、artifact、transcript 或 provider response 中是否有可解析字段。 |
| cost | 成本 | 基于 usage 和模型/provider 价格计算出的费用估算。 | 必须同时有可靠 usage 和可信价格来源；缺任一项都只能标记 unknown。 |
| normalization | 归一化 | 把不同 CLI/provider 的不同字段名、单位和粒度映射到同一 schema。 | tokens 单位、duration 单位、attempt 级别、case 级别、agent 级别是否一致。 |
| blocker | 阻塞项 | 导致某个比较字段不能得出可信结论的明确原因。 | blocker 应该有 category、scope、evidence/source、impact，不能只写“没有数据”。 |
| structured blocker | 结构化阻塞项 | 用固定字段记录的 blocker，便于 compare、scorecard、release gate 读取。 | JSON 里应能区分 usage missing、pricing missing、runtime unsupported、resource exhausted 等。 |
| `ccsd` | 原版 Claude Code 本地命令 | 当前 APG baseline 里调用原版 Claude Code 的命令名。 | 先看 `ccsd` 运行 artifacts，而不是假设它一定有或没有 usage。 |
| artifact usage extraction | 从运行产物提取 usage | 从 stdout、stderr、transcript、manifest、report 等文件里解析 tokens/cost/duration。 | 用脚本或 jq/rg 证明字段存在、格式稳定、能对应到具体 attempt。 |
| stdout / stderr | 标准输出 / 标准错误 | CLI 运行时写到终端的输出流，APG 会保存为 artifact。 | 搜索是否出现 token、usage、cost、duration、model、request id 等字段。 |
| transcript | 会话转录 | agent 会话过程记录，可能包含模型请求摘要、工具调用、最终回答或内部事件。 | 检查是否包含 provider usage 或只包含用户可见文本。 |
| extractable usage | 可提取用量 | usage 字段不只是给人看的散文，而是有稳定格式、能被规则或 parser 可靠读取。 | 多个 attempt 中字段位置和格式一致；失败 run 也能给出明确缺失原因。 |
| runtime-only blocker | 仅运行时阻塞 | 缺失来自 agent runtime/CLI 没有暴露 usage，而不是 APG 解析器、价格表或 compare 逻辑的问题。 | artifacts 已确认没有可提取 usage；此时不应继续补价格表来伪造 cost。 |
| pricing table | 价格表 | 模型 input/output token 单价或 provider 计费规则。 | 只有 usage 已可靠时才有意义；usage 缺失时价格表不能解决成本比较。 |
| avoid guessing pricing | 避免猜价格 | 不用未经验证的模型价格或名称映射去生成看似精确的 cost。 | compare 应输出 unknown/blocker，而不是输出误导性的美元数。 |
| normalized compare | 归一化后的对比 | report 聚合时使用统一 usage/cost schema 做横向比较。 | status compare 可以继续；efficiency/cost claim 要根据 blocker 降级。 |

### 最小判断流程

```text
先查 ccsd artifacts/stdout/stderr/transcript
-> 有稳定 usage 字段：实现 extraction，再进入 cost normalization
-> 没有稳定 usage 字段：记录 runtime-only blocker，cost 维度保持 unknown
-> 不用猜价格表补洞
```

这个流程的目的不是否定 `ccsd` 作为 baseline，而是保护结论可信度：状态胜负可以继续比较，成本/效率维度必须在 usage 来源可证明后再比较。

## Agent 验收术语详解

这一组术语用于区分“模型请求层是否真的收到上下文”“运行产物是否能复查请求内容”“真实 CLI 用户链路是否跑通”。三者经常配合使用，但证明边界不同。

| 术语 | 中文解释 | 在 go-claude 里的含义 | 能证明什么 | 不能证明什么 |
| --- | --- | --- | --- | --- |
| Request-level test | 请求层测试 | 在单元测试或集成测试里捕获发送给模型 provider 的 `MessagesRequest`，直接断言 system/user/tool/tool_result 等请求内容。 | 证明某段上下文、工具结果、resume/compact evidence 在“下一轮真实模型请求”里存在。 | 不证明真实 CLI/TUI 全链路、终端渲染、文件 transcript 写入或用户可见性一定正确。 |
| Prompt dump | 提示词/请求转储 | 运行 CLI 或测试场景时，把即将发给模型的请求内容输出成 JSONL/JSON artifact，供人工和脚本复查。 | 证明真实运行路径下 provider request 的具体内容，适合排查 prompt/context 注入、resume、compact、tool_result 是否丢失。 | 不直接证明模型会做出正确决策；dump 也可能只覆盖某个场景、某次配置和某个 provider adapter。 |
| Deterministic gate | 确定性门禁 | 用固定输入、固定 marker、固定 stub/provider 响应和固定断言验证某条能力链路。 | 稳定证明上下文、证据、runtime gate 或状态转换没有回退。 | 不证明真实模型开放任务胜率，也不覆盖随机模型行为。 |
| Stub provider | 桩模型服务 | 本地启动一个假的 OpenAI/Anthropic-compatible provider，根据请求内容返回固定响应并记录请求。 | 稳定复现模型交互边界，证明真实 CLI/query/server 链路发出了预期请求。 | 不代表真实模型会按同样方式理解、规划或执行。 |
| CLI acceptance | CLI 验收测试 | 用真实 `golang-cc` CLI、stub provider、临时工作区和脚本跑完整用户链路，并检查退出码、dump、transcript、文件结果或关键日志。 | 证明用户从命令行触发的完整流程可用，例如创建任务、失败恢复、resume、AgentGet、最终请求上下文都按预期发生。 | 不等于覆盖 TUI/WebUI 交互细节，也不能单独证明开放世界任务能力超过所有 baseline。 |

### Deterministic gate / prompt dump / stub provider 的关系

这三个词经常一起出现，分别对应验收链路里的三个角色：

```text
stub provider 负责稳定模拟模型
prompt dump 负责记录真实发给模型的 request
deterministic gate 负责用固定规则判断这次链路是否通过
```

典型用途：

1. 用 stub provider 避免真实模型随机性、限流、账号状态和网络波动。
2. 用 prompt dump 回答“模型这一轮到底看到了什么”。
3. 用 deterministic gate 断言关键 marker、context section、tool_result、resume/compact evidence 或 runtime decision 是否存在。

最小例子：

```text
如果下一轮 request 里包含 GOAL_GATE_NEXT_ACTION
stub provider 返回 GOAL_STATUS: complete
deterministic gate 断言 runtime 仍把 decision.status 改成 continue
```

这能证明 runtime hard gate 生效；但它仍不是开放世界胜率证明。要证明 go-claude 在真实任务中更强，还需要真实模型、真实任务、同模型 A/B、repeat 和多 case scorecard。

## TUI/CLI runtime context panel 术语

这组术语用于解释这类实现描述：

```text
只改 TUI/CLI 已有 WelcomeInfo 视图链路，增加一个结构化 runtime context panel。
```

它的核心意思是：不先改模型推理、prompt 或 agent runtime 的决策逻辑，而是把 runtime 已经知道的关键状态，用稳定、可扫读、可测试的形式展示给终端用户。

| 术语 | 中文解释 | 在 go-claude 里的含义 | 为什么重要 |
| --- | --- | --- | --- |
| TUI | Terminal User Interface / 终端交互界面 | 用户在命令行里运行交互式 go-claude 时看到的全屏或半屏终端界面。 | 真实用户长任务中主要靠 TUI 判断当前状态、风险、下一步和是否需要接管。 |
| CLI | Command Line Interface / 命令行接口 | 一次性命令输出，例如 `status`、`goal status`、`goal run --once` 这类非全屏交互命令。 | CLI 输出适合脚本、验收和快速排查，不能只让信息在 TUI 里可见。 |
| WelcomeInfo | 欢迎/启动状态信息 | go-claude 启动、进入模式或显示状态时已有的欢迎信息数据和渲染入口。 | 复用已有入口能降低改动面，避免另造一套并行 UI 状态链路。 |
| 视图链路 | view chain / 展示链路 | 从 runtime 收集数据，到 CLI/TUI 组装字段，再到终端渲染的整条路径。 | 只改字符串不够；要确认数据来源、格式化、渲染和验收都连通。 |
| runtime context | 运行时上下文 | agent 当前执行时已经掌握或需要暴露的状态，例如 cwd、session、worktree、model、permissions、goal、recent evidence、pending follow-up、risks、next action。 | 用户和父 agent 都需要知道“现在基于什么状态继续执行”，否则长任务容易黑盒化。 |
| structured runtime context | 结构化运行时上下文 | 把 runtime context 拆成稳定字段，而不是写成一段自然语言总结。 | 字段稳定后，TUI/CLI 验收可以直接断言，也更利于后续 WebUI/API 复用。 |
| panel | 面板 / 信息块 | 终端里一块独立的分组展示区域，例如 `Runtime context`、`Goal`、`Evidence`、`Risks`。 | 让用户快速扫读关键状态，避免所有信息混在 welcome 文案里。 |
| runtime context panel | 运行时上下文面板 | 一个集中展示当前运行状态、证据、风险和下一步的结构化信息块。 | 把 agent “脑子里已有的关键状态”显式呈现出来，提升可观察性和可接管性。 |
| existing WelcomeInfo view chain | 已有 WelcomeInfo 展示链路 | 不新建独立 UI 系统，而是在现有 WelcomeInfo 数据/渲染路径上扩展字段。 | ROI 高、风险小，能减少对输入、滚动、权限提示等 TUI 交互的干扰。 |
| TUI/CLI parity | 终端交互与命令行输出一致性 | 同一类 runtime context 信息在 TUI 和 CLI 中含义一致，允许展示密度不同。 | 防止用户在 TUI 能看到风险，但脚本/CLI 验收看不到，或反过来。 |
| visibility gate | 可见性门禁 | 验证某个能力状态是否真的展示给用户，而不是只存在于内存、日志或 prompt dump。 | agent 能力不只要“内部存在”，还要让用户能审查、接管和验证。 |

### 这句话不是在说什么

这类改动通常不是：

- 改模型 provider、base URL 或 fallback 逻辑；
- 改子 agent prompt contract；
- 改 Goal evaluator 的 complete/continue 决策；
- 改 compact/resume 的证据保真；
- 新建一个独立 WebUI 页面。

它更像是一层可见性增强：

```text
runtime 已经有状态
-> WelcomeInfo 收集并归一化这些字段
-> TUI/CLI 用 panel 结构展示
-> 测试断言用户能看见关键字段
```

### 推荐验收口径

最小验收不应该只看代码里有没有字段名。应该同时确认：

1. TUI/CLI 的 WelcomeInfo 数据结构里有 runtime context 字段。
2. 渲染输出里能看到 panel 标题和关键字段，例如 goal、cwd、session、evidence、risk、next action。
3. 字段为空时不会显示噪音占位，例如 `None observed`。
4. 现有 welcome/status 信息没有被挤掉或重复展示。
5. 有 focused snapshot/golden/unit test 锁住展示文本，必要时再补 TUI acceptance。

## Goal Evidence / Workbench 术语详解

这一组术语主要用于解释 Goal Mode、Agent Capability Loop 和 WebUI Goal Workbench 之间的证据链。它们不是单纯的 UI 文案，而是为了让“子 agent 发现了什么、父 agent 如何继续、用户如何判断风险”有结构化记录。

源码入口：

- `internal/goal/plan.go` 定义 `GoalPlan`、`GoalStep`、`GoalCriterion`、`GoalRisk`、`GoalEvidence`。
- `internal/goal/evidence.go` 的 `EvidenceFromToolTraces` / `evidenceFromToolTrace` 会把工具调用转成 `GoalEvidence`。
- `internal/goal/evidence.go` 的 `agentGetCapabilityEvidence` 会识别 `AgentGet` 返回里的 `capability_loop`，并写入 `GoalEvidence.Payload`。
- `internal/server/server.go` 的 `/tenant/goals/{id}/plan` 和 `/tenant/goals/{id}/evidence` 负责把 plan/evidence 暴露给 WebUI。

| 术语 | 中文解释 | 在代码里的含义 | 为什么重要 |
| --- | --- | --- | --- |
| Goal | 长期目标 / 任务目标 | 一次可持续推进的目标对象，包含 objective、status、turn budget、token budget、last_next_action 等状态。 | 让 agent 不是只回答一轮，而是围绕目标持续推进、记录进度、恢复上下文。 |
| Goal Plan | 目标计划 | `GoalPlan`，包含 steps、acceptance criteria、dependencies、risks、current_step_id。 | 告诉 agent 和用户“当前应该做哪一步、验收标准是什么、还有哪些风险”。 |
| Goal Step | 目标步骤 | `GoalStep`，包含 id、title、status、rationale、depends_on、evidence_ids。 | 把大目标拆成可追踪步骤，避免 agent 漫无目的地执行。 |
| Acceptance Criteria | 验收标准 | `GoalCriterion`，包含 description、required、status、evidence_ids。 | 判断目标是否真的完成，而不是只凭模型主观说“完成了”。 |
| Goal Risk | 目标风险 | `GoalRisk`，包含 description、severity、mitigation、status。 | 暴露阻塞、残余风险和需要人工关注的点。 |
| Goal Evidence | 目标证据 | `GoalEvidence`，包含 summary、type、command、passed、payload、created_at。 | 把工具调用、测试、git、API、子 agent 结果等变成可复查证据。 |
| Evidence payload | 证据载荷 | `GoalEvidence.Payload`，类型是 `json.RawMessage`，保存结构化 JSON 原文。 | summary 只适合快速扫读，payload 保留更完整的上下文供父 agent、UI 或调试工具复用。 |
| Capability Loop | 能力闭环结构 | 子 agent 输出的结构化字段，通常包括 evidence、assumptions、unknowns、verification、risks、next_action。 | 强迫 agent 把“证据、假设、未知、验证、风险、下一步”说清楚，减少空泛结论。 |
| `capability_loop` | 能力闭环 JSON 字段 | `agentGetCapabilityEvidence` 从 `AgentGet` 输出中提取该字段，并写入 evidence payload。 | 父 agent 和 WebUI 可以直接读取结构化证据，而不是从自然语言里猜。 |
| `partial_evidence` | 部分证据标记 | 当子 agent 状态是 failed、cancelled 或 canceled 时，仍然把可用发现保存下来，并标记为 partial。 | 子任务失败不等于所有发现无效；这个字段让父 agent 能利用失败前已经找到的线索。 |
| `agent_status` | 子 agent 状态 | 写入 payload 的子任务状态，例如 completed、failed、cancelled、unknown。 | 用户和父 agent 能区分“完整成功证据”和“失败/取消后的部分证据”。 |
| `passed` | 证据是否通过 | `GoalEvidence.Passed`；普通工具调用通常取决于 `IsError`，AgentGet partial evidence 会被标为 false。 | 用于区分正向验收证据和失败/风险证据。 |
| `summary` | 证据摘要 | 人类快速阅读的一行摘要，例如 `AgentGet failed partial evidence | evidence: ... | next: ...`。 | UI、报告和日志可以先显示摘要，必要时再展开 payload。 |
| Workbench | 工作台 / WebUI 操作面板 | 这里指 WebUI 的 Goal Workbench，用来查看 goals、events、plan、evidence 并触发 run/resume/stop。 | 用户需要在长任务中实时看到当前目标、步骤、证据、风险和下一步，而不是只看最终回答。 |
| Helper function | 辅助函数 | 前端里用于格式化、解析 payload、压缩展示 capability_loop 的小函数。 | 避免 UI 直接展示大段 JSON，同时保留关键字段。 |
| i18n | internationalization / 国际化 | 前端文案翻译表，当前支持英文和中文。 | 新 UI 字段必须同时有中英文文案，否则界面会出现 key 或语言不一致。 |

### 前端加载链路术语

这句话：

```text
GoalPlan/GoalEvidence 类型和 API helper；GoalWorkbench 在选中 goal 时并行加载 plan/evidence/events；UI 增加 current step、criteria 通过数、最近 evidence 和 risks/next action；
```

可以拆成三层理解：

1. 数据形状：前端要知道后端返回的 plan 和 evidence 长什么样。
2. 数据获取：前端要有函数去请求 `/tenant/goals/{id}/plan`、`/tenant/goals/{id}/evidence` 和 events。
3. 数据展示：Workbench 要把这些字段翻译成人能看懂的目标进度、验收状态、证据和下一步。

| 术语 | 中文解释 | 在这句话里的意思 | 典型来源/展示 |
| --- | --- | --- | --- |
| `GoalPlan` 类型 | 目标计划类型 | 前端 TypeScript 里描述 `GoalPlan` 响应结构的类型，通常包含 `steps`、`acceptance_criteria`、`risks`、`current_step_id`。 | 后端 `internal/goal/plan.go::GoalPlan`，API `/tenant/goals/{id}/plan`。 |
| `GoalEvidence` 类型 | 目标证据类型 | 前端 TypeScript 里描述单条 evidence 的类型，通常包含 `id`、`goal_id`、`summary`、`passed`、`payload`、`created_at`。 | 后端 `internal/goal/plan.go::GoalEvidence`，API `/tenant/goals/{id}/evidence`。 |
| Type | 类型 | TypeScript/Go 里的数据结构约束，告诉代码某个对象有哪些字段、字段是什么含义。 | 例如 `GoalPlan`、`GoalEvidence`。 |
| API helper | API 辅助函数 | 前端封装好的请求函数，调用方不用手写 URL、headers 和 JSON 解析。 | 例如 `getGoalPlan(...)`、`listGoalEvidence(...)` 这类 helper。 |
| `GoalWorkbench` | Goal 工作台组件 | WebUI 里管理和查看 goals 的面板。 | 用户在 WebUI 中选择 goal、运行一次、恢复、停止，并查看状态。 |
| 选中 goal | selected goal | 用户当前正在查看的那个目标。 | Workbench 列表中 active 的 goal row。 |
| 并行加载 | parallel loading | 同时请求 plan、evidence、events，而不是一个请求完成后再请求下一个。 | 典型实现是 `Promise.all` / `Promise.allSettled`。 |
| `plan` | 计划数据 | 当前 goal 的步骤、验收标准、依赖、风险和当前步骤。 | 用来展示 current step、criteria、risks。 |
| `evidence` | 证据数据 | 当前 goal 已收集到的验证证据、失败证据、子 agent 部分证据。 | 用来展示最近 evidence、passed/failed、capability_loop 摘要。 |
| `events` | 事件流 / 事件记录 | goal 生命周期事件，例如 started、turn_started、turn_finished、status_changed。 | 用来展示时间线和运行过程。 |
| `current step` | 当前步骤 | `GoalPlan.CurrentStepID` 指向的步骤，或状态为 active 的步骤。 | 告诉用户 agent 现在应该聚焦哪一步。 |
| `criteria` | 验收标准 | `acceptance_criteria`，用于判断目标是否满足完成条件。 | 例如 `3/5 passed` 表示 5 条标准里已有 3 条通过。 |
| criteria 通过数 | 验收通过计数 | 已通过 criteria 数量 / 总 criteria 数量，也可单独统计 required criteria。 | 用来快速判断离完成还差多少。 |
| 最近 evidence | recent evidence | 最近 N 条目标证据，通常按创建时间或后端返回顺序展示。 | 用来快速复查最近验证、失败、风险或子 agent 输出。 |
| `risks` | 风险列表 | 计划中仍未关闭的风险，例如 open、escalated。 | 告诉用户还有什么可能导致目标失败。 |
| `next action` | 下一步动作 | agent 建议下一轮应该做什么，常来自 `last_next_action` 或 `capability_loop.next_action`。 | 用来指导父 agent 继续规划，也让用户知道下一步会发生什么。 |
| UI 增加 | 界面新增展示 | 不改变底层能力，只把已经存在或刚加载到的数据展示出来。 | 例如多几个 metric、证据列表、当前步骤卡片。 |

### 为什么要并行加载 plan/evidence/events

`plan`、`evidence`、`events` 是同一个 goal 的三个侧面：

- `plan` 回答“应该做什么、验收标准是什么”；
- `evidence` 回答“已经证明了什么、失败前留下了什么线索”；
- `events` 回答“运行过程中发生了什么”。

如果只加载 `events`，用户只能看到流水账，看不到目标是否接近完成。
如果只加载 `plan`，用户知道目标结构，但不知道哪些证据已经支持它。
如果只加载 `evidence`，用户能看到证据，但不知道这些证据对应哪个步骤或验收标准。

所以 Workbench 选中一个 goal 后，合理做法是把这三类数据一起加载，然后合并展示成一个可读状态：

```text
当前步骤：验证 WebUI Goal evidence 展示
验收标准：3/5 passed
最近证据：AgentGet cancelled partial evidence，包含 risk 和 next action
风险：1 open
下一步：补组件测试并跑 npm test
```

这类展示的价值不在于“多显示几个字段”，而是让用户能判断 agent 是否真的在闭环：

- 目标是否仍明确；
- 当前步骤是否合理；
- 验收标准是否有证据支持；
- 失败/取消的子任务是否留下可复用证据；
- 下一步是否能直接执行和验证。

### Resume / Tool Result Budget 证据衰减术语

这段话：

```text
同步 Task、foreground Agent 和 AgentGet 的 capability_loop 都会被提升成 runtime “Recent agent evidence decision context”。如果后续 tool-result history 被预算压缩，父线程仍应优先依赖这段 recent evidence context，而不是只依赖普通历史 tool_result。

tool result budget 把大工具结果替换为 <persisted-output> 预览时，目前预览只是前 2000 bytes。若 capability_loop 在长输出尾部，父模型下一轮可能只看到“文件保存路径”，看不到 evidence/risks/next_action。
```

可以拆成两类风险：

1. **上下文层级差异**：同样是 `capability_loop`，如果只留在普通历史 `tool_result` 里，它比被提升进 runtime status 的证据更容易被压缩、替换或忽略；当前高优先级路径会把同步 Task、foreground Agent 和 AgentGet 的结构化结果都提升为 recent evidence。
2. **可见内容截断**：大工具结果被 `<persisted-output>` 替换后，模型下一轮通常只看到预览和保存路径；如果关键结构在尾部，可能不在预览里。

| 术语 | 中文解释 | 在这段话里的意思 | 典型验证点 |
| --- | --- | --- | --- |
| 同步 Task | synchronous Task / 前台 Task | 父 agent 当前轮直接调用并等待结果的 Task。结果通常作为当前对话里的 `tool_result` 回到模型。 | 看 Task 返回内容是否包含 `<capability_loop>` wrapper，以及下一轮 request 是否仍能看到这些字段。 |
| 历史 `tool_result` | historical tool result / 历史工具结果 | transcript 或 resume 消息中重放的旧工具结果。 | resume 后检查 provider request，确认旧 Task result 是否仍原样、摘要化或被 persisted-output 替换。 |
| replay / 重放 | 恢复时重新放入上下文 | resume 时把历史 message、tool_call、tool_result 重新组织成模型可接受的消息序列。 | 检查 resume repair 和 transcript loader 输出。 |
| runtime status | 运行时状态上下文 | 每轮动态生成、放进请求里的状态段，例如后台 agent 任务状态、最近证据、下一步。 | 搜索或 dump `Recent agent evidence decision context`、`Background agent tasks` 等动态段。 |
| Recent agent evidence decision context | 最近 agent 证据决策上下文 | 给父 agent 的高优先级运行时提示，强调已经获得的 evidence、unknowns、verification、risks、next_action。 | prompt dump 中应出现结构化 evidence/risks/next_action，而不只是一段旧 tool_result。 |
| 提升 / promotion | 从普通结果提升为运行时上下文 | 把同步 Task、foreground Agent、`AgentGet` 或 agent task store 里的结构化证据抽取出来，变成独立的 runtime status。 | 检查是否从 Task/Agent tool_result、task store、AgentGet result 或 terminal task result 中抽取 capability fields。 |
| 证据衰减 | evidence weakening | 证据仍存在于某个文件或历史记录里，但在下一轮模型可见上下文中变短、变浅或消失。 | 对比原始 transcript、resume request、compact summary、prompt dump。 |
| tool-result history budget | 工具结果历史预算 | 针对历史工具结果的可见 token/byte 预算，超出时优先压缩旧的大结果。 | `internal/toolresult` 的 history/message budget 处理，以及 prompt dump 的 `persisted_output` 统计。 |
| tool result budget | 工具结果预算 | 控制单轮或历史工具结果可见长度的预算机制。 | 大输出是否被替换成 `<persisted-output>`，哪些工具被 skip，最新结果是否保留。 |
| `<persisted-output>` | 持久化输出占位符 | 大工具结果被保存到磁盘后，模型请求里用一个短占位块替代完整内容。 | 请求中看到 `<persisted-output>`，同时本地 `tool-results/*.txt` 保存完整输出。 |
| Preview | 预览 | `<persisted-output>` 里直接给模型看的前缀文本。当前常见策略是前 `2000` bytes。 | 如果关键字段在输出尾部，检查 preview 是否包含它。 |
| tail evidence | 尾部证据 | 位于长输出末尾的关键 evidence、risks、next_action。 | 构造长前缀 + 尾部 `<capability_loop>` 的测试，确认替换后是否仍可见摘要。 |
| persisted file path | 持久化文件路径 | `<persisted-output>` 告诉模型完整输出保存在哪里。 | 模型知道“有文件”，但不一定主动读取；因此不能只依赖路径承载关键决策证据。 |
| full output saved to | 完整输出保存到 | 占位符里提示完整内容落盘的位置。 | 用于人工或后续工具读取，不等同于下一轮模型已经看见完整内容。 |
| budget compression | 预算压缩 | 为了不超上下文预算，把长结果改成摘要、预览或 compact facts。 | 检查压缩前后 capability_loop 是否仍在请求中。 |
| extraction coverage | 抽取覆盖 | 对不同形态的 capability_loop 是否都能识别：纯 JSON、嵌套 JSON、`<capability_loop>` wrapper、单行 summary。 | 单测应覆盖 Task wrapper、AgentGet nested result、runtime status line、persisted-output summary。 |

### 为什么普通 Task 证据可能比 AgentGet 证据更弱

`AgentGet` 通常是父 agent 主动领取后台子任务结果的动作。它的结果更容易被系统识别成“agent evidence”，再提升到 runtime decision context。
同步 Task 的输出虽然也可能包含 `<capability_loop>`，但如果系统只把它当普通 `tool_result` 保存，那么它在后续 resume、compact、history budget 中的优先级就更低。

这不是说同步 Task 的证据一定会丢，而是说它需要额外验证：

- resume 后，Task wrapper 是否还在下一轮 request；
- compact 后，Task wrapper 是否进入 `Capability Loop Evidence` / `Risks` / `Next Actions`；
- tool-result budget 替换后，`<persisted-output>` 是否仍保留 capability_loop 摘要；
- 如果只剩保存路径，父 agent 是否会主动读取保存文件，还是直接基于不完整预览继续。

### 为什么只给前 2000 bytes 预览有风险

长工具输出常见结构是：

```text
大量日志、大量文件内容、大量测试输出...
...
<capability_loop>
{"capability_loop":{"evidence":["关键证据"],"risks":["关键风险"],"next_action":"关键下一步"}}
</capability_loop>
```

如果预算机制只保留前 2000 bytes 作为 preview，而 `<capability_loop>` 在尾部，下一轮模型看到的可能只是：

```text
<persisted-output>
Output too large. Full output saved to: /path/to/tool-results/toolu_task.txt

Preview (first 2000 bytes):
大量日志开头...
...
</persisted-output>
```

这时完整文件虽然存在，但父模型当前轮没有直接看到 `evidence`、`risks`、`next_action`。对 agent 能力来说，这会造成两个问题：

- 父 agent 可能低估子任务已经发现的风险；
- 父 agent 可能不知道下一步应该验证什么。

更稳的策略是：在 `<persisted-output>` 的 preview 前额外写入一段从完整输出中抽取出的 capability_loop summary，例如：

```text
Capability loop summary preserved from full output:
- evidence: 关键证据
- risks: 关键风险
- next_action: 关键下一步

Preview (first 2000 bytes):
...
```

这样即使完整输出被落盘，父模型下一轮仍能直接看到决策所需的最小结构化证据。

### 这类缺口应该怎么验证

不要只看源码里有没有 `capability_loop` 字符串。更可靠的验证顺序是：

1. 构造一个同步 Task，返回长输出，且 `<capability_loop>` 放在尾部。
2. 触发 tool result budget，让结果变成 `<persisted-output>`。
3. 检查下一轮 prompt dump：必须能看到 evidence、risks、next_action，而不只是 `Full output saved to`。
4. 再做 resume：确认重启/恢复后的 request 仍能看到这些字段。
5. 再做 compact：确认 compact summary 的 `Capability Loop Evidence`、`Risks`、`Next Actions` 仍保留这些字段。

最小验收标准：

```text
原始 Task 输出尾部有 capability_loop
-> persisted-output 替换后仍有 capability loop summary
-> resume 后 request 仍有 summary
-> compact 后 summary facts 仍有 evidence/risks/next_action
```

### persisted-output capability_loop 摘要 -> compact facts -> compacted main request

这条链路描述的是：一个长工具结果被压缩成 `<persisted-output>` 后，里面保留下来的 `capability_loop` 摘要，如何继续被 compact 机制抽取成事实，并最终进入压缩后的主模型请求。

可以拆成三步：

```text
<persisted-output> 中的 capability_loop 摘要
-> compact facts / Runtime Extracted Facts
-> compacted main request / Conversation summary so far
```

| 阶段 | 中文解释 | 在上下文里的作用 | 关键验证点 |
| --- | --- | --- | --- |
| persisted-output capability_loop 摘要 | 持久化输出里的能力闭环摘要 | 大工具结果被替换为 `<persisted-output>` 后，仍在可见占位块里放入 evidence、risks、next_action 等关键字段。 | 下一轮 request 里不只看到 `Full output saved to`，还看到 capability loop summary。 |
| compact facts | compact 前抽取出的硬事实 | compact 运行前，从旧消息、tool_result、persisted-output 摘要中抽取文件、命令、错误、Capability Loop Evidence/Risks/Next Actions 等事实。 | compact 结果里的 `Runtime Extracted Facts` 包含 capability loop 字段。 |
| compacted main request | 压缩后的主请求 | compact 后，旧历史被替换为 `Conversation summary so far:`，再加最近几轮消息，作为下一轮主模型请求。 | prompt dump 中的主请求含 `Conversation summary so far` 和 `Capability Loop Evidence` 等字段。 |
| Runtime Extracted Facts | 运行时抽取事实 | compact summary 之外额外附加的结构化事实块，避免 summary 模型漏写关键证据。 | compacted summary 中应有 `## Runtime Extracted Facts`。 |
| Conversation summary so far | 到目前为止的会话摘要 | compact 后插入主对话的摘要消息前缀。 | 下一轮模型请求应能看到这段摘要，而不是完整旧历史。 |
| compact prompt | 压缩摘要请求 | 发送给 summary model 的请求，要求保留目标、约束、决策、文件、命令、风险、原始事实。 | compact prompt 中应包含 `Hard facts extracted by the runtime`。 |
| summary model | 摘要模型 | 用来生成 compact summary 的模型，可以与主模型相同或不同。 | 即使 summary model 漏掉事实，Runtime Extracted Facts 仍应补上。 |
| preserved rounds | 保留的最近轮次 | compact 不会压缩的最近对话轮数，用于保留最新上下文。 | 检查旧证据是否在 compacted summary，最新结果是否仍原样保留。 |

#### 为什么需要这条链路

只解决 `<persisted-output>` 预览还不够。因为长会话继续运行后，历史消息还可能触发 compact。此时如果 compact 只能看到：

```text
Full output saved to: /path/to/tool-results/toolu_task.txt
Preview (first 2000 bytes): ...
```

但看不到 capability_loop 摘要，那么 compact summary 也无法可靠保留 evidence、risks、next_action。后续主模型请求就可能只知道“有个大文件被保存了”，不知道“子任务发现了什么、风险是什么、下一步是什么”。

因此更完整的保真链路是：

```text
长 Task 输出尾部 capability_loop
-> tool result budget 替换为 persisted-output
-> persisted-output 前部保留 capability_loop summary
-> compact ExtractFacts 识别 summary
-> Runtime Extracted Facts 写入 Capability Loop Evidence/Risks/Next Actions
-> compacted main request 带着这些事实继续运行
```

#### 三种容易混淆的“摘要”

| 摘要类型 | 谁生成 | 内容来源 | 风险 |
| --- | --- | --- | --- |
| persisted-output summary | tool result budget 代码生成 | 完整工具输出里的 capability_loop | 如果没有抽取尾部 capability_loop，关键证据可能只在落盘文件里。 |
| compact summary | summary model 生成 | 被压缩的旧对话和工具结果 | 模型可能漏掉结构化证据，所以不能只依赖自然语言摘要。 |
| Runtime Extracted Facts | runtime 规则抽取 | 正则/JSON/summary block 解析出的硬事实 | 规则覆盖不全时，某些 capability_loop 形态可能抽不到。 |

#### 一个最小例子

压缩前，一个大工具结果可能被替换为：

```text
<persisted-output>
Output too large. Full output saved to: /tmp/session/tool-results/toolu_task.txt

Capability loop summary preserved from full output:
- evidence: 子任务确认 GoalWorkbench 未展示 partial evidence
- risks: 用户可能看不到 failed 子任务留下的风险
- next_action: 补 Workbench evidence 展示和测试

Preview (first 2000 bytes):
...
</persisted-output>
```

compact facts 应该抽出：

```text
Capability Loop Evidence:
- 子任务确认 GoalWorkbench 未展示 partial evidence

Capability Loop Risks:
- 用户可能看不到 failed 子任务留下的风险

Capability Loop Next Actions:
- 补 Workbench evidence 展示和测试
```

compact 后进入主请求的摘要消息应该包含：

```text
Conversation summary so far:
...
## Runtime Extracted Facts
Capability Loop Evidence:
- 子任务确认 GoalWorkbench 未展示 partial evidence
Capability Loop Risks:
- 用户可能看不到 failed 子任务留下的风险
Capability Loop Next Actions:
- 补 Workbench evidence 展示和测试
```

这样父模型即使不读取落盘文件，也能继续看到最小决策上下文。

#### 这条链路的验收口径

不要只验证某一步存在。最小验收应同时覆盖：

1. persisted-output 里有 `Capability loop summary preserved from full output`。
2. compact facts 里有 `Capability Loop Evidence`、`Capability Loop Risks`、`Capability Loop Next Actions`。
3. compacted main request 里有 `Conversation summary so far` 和 `## Runtime Extracted Facts`。
4. prompt dump 中能看到具体 evidence/risks/next_action 文本，而不是只有保存路径。

如果任一环断掉，证据仍可能“存在于磁盘”，但不一定“存在于下一轮模型可见上下文”。

### AgentGet -> persisted-output -> auto compact -> resume

这条链路是一个更完整的证据保真验收场景：

```text
触发 AgentGet
-> persisted-output
-> auto compact
-> resume
```

它的意思是：故意让子 agent 结果经过几层最容易削弱上下文的机制，最后确认 `capability_loop` 里的 `evidence`、`risks`、`next_action` 仍然在模型下一轮可见上下文里。

| 阶段 | 中文解释 | 这一步在验证什么 | 如果失败会怎样 |
| --- | --- | --- | --- |
| 触发 | 人为构造一个真实运行场景，让机制真的发生。 | 不是只读源码，而是让 AgentGet、大输出替换、compact、resume 都跑起来。 | 只能证明代码看起来有路径，不能证明真实请求链路有效。 |
| AgentGet | 父 agent 领取子 agent 结果的工具。 | 子 agent 的状态、输出和 `capability_loop` 是否回到父 agent。 | 父 agent 可能不知道子 agent 发现了什么。 |
| persisted-output | 大工具结果落盘后，在模型上下文里用 `<persisted-output>` 占位摘要替代完整内容。 | 长结果被压缩后，关键 `capability_loop` 摘要是否仍可见。 | 模型可能只看到保存路径，看不到 evidence/risks/next_action。 |
| auto compact | 会话过长时自动触发上下文压缩。 | persisted-output 里的 capability_loop 摘要是否能被抽成 compact facts。 | compact summary 可能漏掉关键证据。 |
| resume | 恢复旧会话并继续下一轮请求。 | compact 后的 summary/facts 是否能在恢复后的主请求里继续出现。 | 重启或恢复后，父 agent 会丢失关键决策上下文。 |

这条链路比单独测试某个函数更强，因为它连续覆盖了四种真实长任务风险：

- `AgentGet` 结果是否结构化；
- 大结果是否被 `<persisted-output>` 替换后仍保留摘要；
- auto compact 是否把摘要抽成 `Runtime Extracted Facts`；
- resume 后主请求是否还能看到这些 facts。

用一句话概括：

```text
AgentGet 拿到 capability_loop
-> 结果太长，被替换成 persisted-output
-> 会话太长，触发 auto compact
-> 之后 resume
-> 模型下一轮仍能看到 evidence / risks / next_action
```

#### 为什么这个验收很关键

短任务里，父 agent 当轮看到 `capability_loop` 还不够。真实工程任务经常会继续运行很多轮，期间会发生：

- 子 agent 输出很长；
- 工具结果被预算机制压缩；
- 会话自动 compact；
- 用户稍后 resume。

如果这条链路中任何一环没保住证据，父 agent 可能只知道“有个结果保存到文件了”，但不知道子 agent 实际发现了什么、风险是什么、下一步该做什么。

真正稳定的 agent 能力要求：

```text
当轮能用证据
-> 压缩后还能用证据
-> 恢复后还能用证据
```

#### 最小验收断言

这条链路至少应该检查：

1. AgentGet 原始结果包含 `capability_loop`。
2. persisted-output 占位符中包含 capability_loop summary。
3. auto compact 后的 `Runtime Extracted Facts` 包含 `Capability Loop Evidence`、`Capability Loop Risks`、`Capability Loop Next Actions`。
4. resume 后的主模型 request 仍包含这些字段。
5. prompt dump 里不仅有 `Full output saved to`，还要有具体 evidence/risks/next_action 文本。

这类验收的通过标准不是“文件还在磁盘上”，而是“下一轮模型请求里仍能直接看到最小决策证据”。

### 一个完整例子

假设父 agent 创建了一个子 agent 做代码审查。子 agent 被取消了，但取消前已经发现一个真实风险。理想情况下，`AgentGet` 返回里会包含类似结构：

```json
{
  "result": {
    "status": "cancelled",
    "capability_loop": {
      "evidence": ["发现 WebUI 没有展示 failed 子任务的风险摘要"],
      "assumptions": ["父 agent 仍可利用这条部分发现继续定位"],
      "unknowns": ["还没有跑完整浏览器验收"],
      "verification": ["补组件测试并跑 npm test"],
      "risks": ["只看 completed 状态会误判任务没有有用输出"],
      "next_action": "把 partial evidence 展示到 Workbench"
    }
  }
}
```

`internal/goal/evidence.go` 会把它转成 `GoalEvidence`，其中：

- `summary` 变成一行可读摘要；
- `passed=false`，因为这是 cancelled partial evidence；
- `payload.agent_status="cancelled"`；
- `payload.partial_evidence=true`；
- `payload.capability_loop` 保留完整结构化字段。

### 为什么要在 Workbench 里短摘要展示

如果 UI 只显示 `summary`，用户能知道“有一条部分证据”，但不一定看得到 assumptions、unknowns、verification、risks、next_action。
如果 UI 直接显示完整 payload JSON，又会太长、难扫读、干扰长任务监控。

所以 Workbench 的合理展示方式是：

```text
status: cancelled | partial | evidence: 发现 WebUI 没有展示 failed 子任务的风险摘要 | risk: 只看 completed 状态会误判任务没有有用输出 | next: 把 partial evidence 展示到 Workbench
```

这个短摘要的目标是让用户快速判断三件事：

- 子 agent 是完整成功，还是失败/取消后的部分证据；
- 已经找到的关键证据和风险是什么；
- 下一步应该验证或执行什么。

### 常见误区

- `partial_evidence=true` 不是“任务成功”，而是“失败/取消前仍有可复用发现”。
- `passed=false` 不代表这条记录没价值，它可能正是定位问题的关键风险证据。
- `capability_loop` 不是普通展示字段，它是父 agent 继续规划和用户审查闭环的结构化上下文。
- Workbench 展示证据不等于能力已经提升；能力提升还需要真实 Task/Agent 任务、prompt dump、golden/verifier 或 APG report 验证。
- i18n 和样式不是能力本身，但会影响用户能否稳定看见这些能力状态。

### Goal hard gate / follow-up resolution 术语

这一组术语用于解释 Goal Mode 里的“完成判定”问题。上一层证据链解决的是 `capability_loop` 有没有进入下一轮模型上下文；这一层解决的是：模型看到证据后，如果仍然过早声称完成，runtime 是否会拦住，以及拦住后如何避免长期目标永远卡住。

| 术语 | 中文解释 | 在 go-claude 里的含义 | 为什么重要 |
| --- | --- | --- | --- |
| hard gate | 硬门禁 / 强制门禁 | 不只靠 prompt 提醒模型，而是在 runtime/evaluator 层检查结构化状态；如果还有未处理的 follow-up，就算模型输出 `GOAL_STATUS: complete`，也把决策改回 continue。 | 防止模型“嘴上说完成”导致目标提前结束。 |
| premature complete | 过早完成 | 目标还有未处理的 `next_action`、verification、unknowns 或 risks，但模型已经输出 complete。 | 长任务最常见的失败之一：没有真正闭环，却被 UI 或状态标成完成。 |
| completion decision | 完成判定 | `Evaluator` 对一次 Goal turn 的结果做出的状态决策，例如 continue、complete、blocked、failed。 | 决定 Goal 是继续跑、完成、阻塞还是失败，比自然语言总结更关键。 |
| pending follow-up | 待处理后续动作 | 从 `capability_loop.next_action`、`verification`、`unknowns`、`risks` 中提取出的未闭环事项。 | 父 agent 下一轮必须处理这些事项，或者明确说明为什么不能处理。 |
| Goal capability follow-up gate | Goal 能力闭环后续门禁 | Goal prompt 中的 `## Goal capability follow-up gate` 块，以及 evaluator 对 pending follow-up 的硬检查。 | 同时覆盖“模型看得见”和“runtime 会拦截”两层。 |
| evaluator hard gate | 评估器硬拦截 | `EvidenceEvaluator` 在 complete 路径上检查 pending capability follow-up。 | 即使模型忽略 prompt gate，runtime 仍能保持目标 active。 |
| structured evidence | 结构化证据 | 带明确字段的 evidence payload，例如 `capability_loop.evidence`、`verification`、`risks`、`next_action`，而不是自由文本总结。 | 机器能稳定读取和验证，不需要从自然语言里猜。 |
| follow-up resolution | 后续动作解除 / 闭环解除 | 新 evidence 明确证明某个旧 pending follow-up 已处理、已验证或被合理取代。 | 解决 hard gate 的副作用：不能只会拦截，还要能在证据足够时允许完成。 |
| supersede | 覆盖 / 取代旧证据 | 新 evidence 指向旧 evidence，表达“这条旧 follow-up 已被新证据处理或替代”。常见字段可设计为 `supersedes_evidence_id`。 | 避免旧风险长期留在上下文里，让目标一直无法 complete。 |
| resolution proof | 解除证明 | 用于证明 follow-up 已处理的正向证据，例如测试通过、prompt dump 验证、人工确认的风险接受记录。 | 防止模型只写“已处理”但没有证据，导致误解除 hard gate。 |
| stale follow-up | 陈旧后续动作 | 旧 evidence 里的 follow-up 已经被处理，但系统没有记录 resolution，导致 evaluator 继续认为它 pending。 | 这是长期目标卡死的典型根因。 |
| long-running goal stuck | 长期目标卡死 | Goal 一直保持 active/continue，无法 complete；可能是因为真实未闭环，也可能是 stale follow-up 没有解除。 | 需要区分“正确阻止过早完成”和“过度保守导致无法完成”。 |

#### 为什么 hard gate 不是终点

`hard gate` 解决的是第一类问题：

```text
模型：GOAL_STATUS: complete
runtime：还有 pending follow-up，所以不能 complete，继续 active
```

这能防止过早完成，但也会引入第二类问题：

```text
follow-up 已经被后续证据处理
runtime 没有识别 resolution
目标仍然继续 active
```

所以完整闭环需要两步：

1. **拦住过早 complete**：有 pending follow-up 时，complete 必须被 evaluator 改成 continue。
2. **允许证据解除 pending**：后续 evidence 明确 supersede 旧 follow-up，并提供 verification/proof 后，旧 follow-up 不应再阻止 complete。

#### 推荐验收口径

这类能力不要只验证 prompt 文案。最小验收应该覆盖：

1. prompt dump 中能看到 `## Goal capability follow-up gate` 和具体 pending follow-up。
2. stub provider 故意返回 `GOAL_STATUS: complete` 时，runtime 决策仍是 continue。
3. 注入明确 resolution evidence 后，同样的 complete 响应可以被允许。
4. 弱解除不能通过：只有自然语言“已处理”、`passed=false`、没有 verification/proof、或指向错误 evidence id，都不能解除 pending。
5. 长期目标场景要检查最终状态：该阻止时保持 active，该解除时允许 complete。

#### 最小理解

```text
prompt gate = 告诉模型不要过早完成
evaluator hard gate = 模型过早完成时 runtime 强制拦住
follow-up resolution = 新证据证明旧 follow-up 已闭环后，允许完成
```

如果只有 hard gate，没有 resolution，系统会更安全但可能更保守；如果只有 resolution，没有 hard gate，模型仍可能在未闭环时提前完成。两者一起，才是长期 Goal 能力闭环。

## Shared Baseline 详解

`shared baseline` 指多个 agent 或工具在同一套任务、同一套评分标准、尽量相同环境下跑出来的共同对照基准。它也可以叫 `shared compare baseline`。

在当前 go-claude/APG 语境里，它通常是：

- `golang-cc-local` 跑同一批 APG tasks，得到一个 report；
- `claude-code-local` 跑同一批 APG tasks，得到一个 report；
- `opencode-local` 跑同一批 APG tasks，得到一个 report；
- APG 再把这些 report 聚合成 compare report。

这组共同任务、共同 scorer、共同 artifact 规则和横向结果，就是 shared baseline。它的作用是避免只说：

```text
go-claude 自己跑了 10 个任务都通过，所以它很强。
```

而是进一步证明：

```text
在同样 10 个任务上，go-claude 相比 Claude Code / OpenCode 表现如何。
```

因此需要区分：

| 类型 | 含义 | 能证明什么 | 典型限制 |
| --- | --- | --- | --- |
| go-claude-only evidence | 只有 go-claude 自己跑某批任务的证据。 | go-claude 对这些任务可用、可闭环、没有明显退化。 | 不能证明相对 Claude Code / OpenCode 更强。 |
| shared baseline | go-claude、Claude Code、OpenCode 等在同一任务集和评分规则下的共同基准。 | 公平横向对比：status win/loss/tie、效率、usage/cost 置信度。 | 只覆盖进入 shared suite 的任务，不代表开放世界胜率。 |
| baseline blocker | 某个 baseline 暂时无法进入共同基准，例如原版 Claude Code 未登录、OpenCode 模型不可用、provider 权限失败。 | 说明横向对比暂时不完整，应降低结论强度。 | 不能把 baseline blocker 当成 go-claude 能力优势。 |

一句话：

```text
shared baseline = 公平横向对比的共同参考线。
```

## Suite 详解

`suite` 可以理解成“一组评测任务的配置包”，中文通常叫测试套件、评测套件或任务套件。

一个 suite 通常会定义：

- 要跑哪些 `case`；
- 每个 case 的任务目标；
- agent 可用的工具、权限、预算和超时；
- 用哪些 `scorer` 判断通过或失败；
- 需要保存哪些 artifact；
- 哪些失败应该归类为能力失败，哪些应该归类为环境/setup 失败。

在 APG 语境里，suite 的作用是把评测范围固定下来。只有固定 suite，多个 agent 的结果才可比较。

例子：

```text
local-agent-compare
```

这个 suite 可以包含 `read-marker`、`go-test-fix`、`go-multifile-fix` 等 case。go-claude 和 OpenCode 都跑同一个 suite，才能生成有意义的 compare report。

最小理解：

```text
suite = 一批 case + 统一规则 + 统一评分方式
case = suite 里的一个具体任务
scorer = 判断 case 是否通过的评分器
```

常见误区：

- 只跑一个 suite，不代表所有任务都通过；
- suite 越小，结论边界越窄；
- 两个 agent 如果跑的不是同一个 suite，就不能直接横向比较；
- suite 里没有覆盖的能力，不能从 report 里推出结论。

## Stub 详解

`stub` 可以理解成“假模型服务 / 替身服务 / 桩服务”。

`stub` 是一个普通英文单词：

```text
stub
```

它不是缩写。常见含义：

- 原义：残段、短截、存根、票根；
- 编程里：桩、桩代码、替身实现；
- 测试里：用来替代真实依赖的简化假对象或假服务。

所以：

```text
local stub = 本地桩服务 / 本地假服务 / 本地替身服务
model stub = 假模型 / 模型桩 / 模型替身
```

真实模型可能会随机回答、受网络影响、受 provider 状态影响，也可能因为 auth、quota、payment、model registry 失败而不可用。`stub` 则会按我们写死的规则返回固定结果，用来稳定测试。

例子：

```text
用户输入：请返回 OK
model stub 固定返回：OK
```

这类 stub 适合验证：

- CLI 请求是否发出；
- prompt dump 是否生成；
- parser 是否能解析响应；
- tool result 是否被正确注入上下文；
- release gate 或 scorer 的结构是否工作。

它不适合证明：

- 真实模型推理能力；
- agent 在开放任务中的规划质量；
- provider 网络、鉴权、计费和模型路由是否稳定；
- go-claude 相对原版 Claude Code 的真实能力优势。

最小理解：

```text
stub = 为了测试稳定性而造的假依赖
real model = 真正会推理、会波动、会受环境影响的模型服务
```

因此看到 `local stub` 或 `model stub` 时，要把它当作“测试替身证据”，不要把它当作“真实模型能力证据”。

## go-claude Release Gate 术语

| 术语 | 含义 | 典型证据 |
| --- | --- | --- |
| Release gate | 发布级能力门禁，把 prompt acceptance、A/B、final-report、TUI visibility、APG report/compare 合并为一个验收入口。 | `scripts/agent-capability-release-gate.sh` 或 `scripts/agent-capability-full-release-acceptance.sh` 生成的 `release-gate-report.json`。 |
| Full release acceptance | 更强的 release wrapper，要求完整证据链同时满足。 | full wrapper 输出 `ok=true`。 |
| Native matrix | go-claude 自身 prompt/runtime 场景矩阵。 | `matrix-report.json`，例如 Task/Agent、resume、compact、tool result 等场景通过数。 |
| A/B matrix | go-claude 与原版 Claude Code 的 side-by-side 对照场景矩阵。 | `scorecard.json`，包含 scenario count、advantage count、underperform count。 |
| Final-report quality gate | 检查最终报告是否包含 evidence、unknowns、verification、risks、next action 等结构。 | final-report score JSON。 |
| TUI visibility gate | 检查能力链路是否在终端界面对用户可见。 | TUI acceptance report。 |
| APG report gate | release gate 消费 APG 单 agent report。 | go-claude 在 APG suite 上的 pass rate。 |
| APG compare gate | release gate 消费 APG compare report。 | shared case count、status losses、efficiency advantages。 |
| Superiority evidence audit | release report 中对“优势结论是否足够”的结构化审计。 | `superiority_evidence` 字段。 |

## 证据强度分层

### L0：连通性证据

例子：OpenCode smoke、Claude Code login probe、adapter dry-run。

可以证明：

- 命令能否启动；
- profile 是否能加载；
- model/auth/payment 是否可用；
- APG 能否采集 stdout/stderr/report。

不能证明：

- agent 能力更强；
- release gate 可以通过；
- go-claude 超过原版。

### L1：单 agent 任务证据

例子：`golang-cc-local` 跑 APG P1 safety/observability suite 并通过。

可以证明：

- go-claude 能在指定 suite 上完成真实任务；
- scorer、artifact、report 链路完整；
- 某些能力族没有明显退化。

不能证明：

- 相对 baseline 更强；
- 共享 compare case count 已足够；
- open-world superiority。

### L2：共享 compare 证据

例子：go-claude 和 OpenCode 都跑 `local-agent-compare` 的同一批 case，然后生成 compare report。

可以证明：

- 同一 case、同一 scorer、同一 suite 下，target 与 baseline 的 status 差异；
- 是否有 status loss；
- 在都通过的任务上谁更快、turn/tool call 更少。

不能证明：

- 原版 Claude Code 的表现，除非原版也进入同一个 compare；
- 10 个以外任务的统计结论；
- 所有复杂工程任务都更强。

### L3：发布级有边界优势证据

例子：native matrix、Go-vs-original A/B、final-report、TUI、APG report、APG compare 同时通过。

可以证明：

- release gate 覆盖的能力族已达标；
- go-claude 在当前有边界的任务族中不低于 baseline，并在部分维度有优势；
- 当前实现没有在关键验收场景退化。

不能证明：

- 开放世界工程任务胜率已经超过原版；
- 未登录/未运行的原版 Claude Code APG baseline；
- 没有进入 suite 的任务族。

### L4：开放世界优势候选

需要同时满足：

- 原版 Claude Code 作为 APG baseline 可真实运行；
- go-claude、原版 Claude Code、其他 baseline 在足够多 shared cases 上比较；
- case 覆盖读写、修复、多文件、长上下文、resume/compact、失败恢复、安全、可观测、成本等维度；
- compare case count 和任务难度达到预设阈值；
- failure category 能区分能力失败与环境失败。

在这些条件满足前，`open_world_superiority_proven` 应保持 `false`。

## 常见字段解释

| 字段 | 位置 | 含义 |
| --- | --- | --- |
| `status` | APG report / compare | report 或 case 总体状态，例如 `passed`、`failed`。 |
| `closure_status` | APG case | 任务闭环状态，例如已关闭且通过。 |
| `failure.category` | APG case | 失败归因，例如 `environment_setup`、scorer failure、runtime failure。 |
| `pass_rate` | APG report summary | `passed / total`。 |
| `case_count` | APG compare / release report | compare 中共享 case 数。 |
| `status_losses` | release `apg_compare` | target agent 相比 baseline 状态更差的 case 数。 |
| `status_wins` | release `apg_compare` | target agent 相比 baseline 状态更好的 case 数。 |
| `efficiency_advantages` | release `apg_compare` | target 和 baseline 都通过时，target duration 更低的 case 数。 |
| `bounded_claim_ok` | release `superiority_evidence` | 当前证据足以支撑“有边界 release gate 优势”。 |
| `open_world_superiority_proven` | release `superiority_evidence` | 是否已证明开放世界优势；未满足大样本和原版 baseline 前必须为 `false`。 |
| `evidence_gaps` | release `superiority_evidence` | 仍阻止升级结论的证据缺口。 |
| `next_evidence` | release `superiority_evidence` | 下一步最应该补的证据。 |

## 常见报告类型

| 报告 | 说明 | 典型用途 |
| --- | --- | --- |
| `agent-proving-ground/report/v1` | APG 单 agent suite 运行报告。 | 证明某 agent 在某 suite 的 pass/fail。 |
| `agent-proving-ground/compare/v1` | APG 多 agent compare 报告。 | 证明同一批 case 下 target 与 baseline 的相对表现。 |
| `golang-cc/superiority-evidence-audit/v1` | go-claude release report 中的优势证据审计。 | 判断当前结论只能是 bounded，还是可以升级。 |
| prompt dump JSONL | go-claude prompt/context dump。 | 验证 system/user/tool/skills/runtime status 是否进入模型上下文。 |
| scorecard JSON | A/B 或综合能力评分卡。 | 汇总 scenario 是否通过、优势数、退化数。 |

## 典型命名解释

| 名称 | 含义 |
| --- | --- |
| `golang-cc-local` | APG 中本机 go-claude agent profile。 |
| `opencode-local` | APG 中本机 OpenCode agent profile。 |
| `claude-code-local` | APG 中本机原版 Claude Code agent profile。 |
| `local-agent-compare` | APG 本地对比 suite，常用于 shared compare。 |
| `p1-safety-observability` | APG P1 安全/可观测 suite。 |
| `read-marker` | 只读 marker 读取 case。 |
| `go-test-fix` | 单文件或小范围 Go 测试修复 case。 |
| `go-multifile-fix` | 多文件 Go 修复 case。 |

## 常见误区

### 误区 1：OpenCode smoke 通过，所以 go-claude 比 OpenCode 强

不成立。Smoke 只说明 OpenCode 能被 APG 调起来。必须进入 shared compare，并且同一批 case、同一套 scorer 下比较结果，才有相对证据。

### 误区 2：go-claude APG 10/10 通过，所以已经超过原版

不成立。go-claude-only report 只能证明 go-claude 自己完成了 10 个任务。要证明超过原版，需要原版 Claude Code 也跑同一批 case，或者至少有足够强的 baseline compare。

### 误区 3：baseline 失败就是 go-claude 优势

不一定。如果 baseline 失败是 `environment_setup`，例如未登录、模型不存在、无 payment method、provider 配置错误，这不能算作 agent 能力失败，也不能消费为 go-claude 优势证据。

### 误区 4：A/B side-by-side 等于 APG compare

不等价。A/B side-by-side 通常是 go-claude 仓库里的特定场景对照；APG compare 是独立平台按 suite/case/scorer 生成的多 agent 横向报告。两者都是证据，但证据边界不同。

### 误区 5：Release gate 通过就能宣称 open-world superiority

不一定。Release gate 可以支撑 bounded release gate advantage。只有当 shared case 数、任务覆盖、baseline 覆盖和失败归因都足够时，才可以把开放世界优势作为候选结论。

## 当前项目中的推荐用语

- 说 **“bounded release gate advantage”**，表示当前证据只覆盖 release gate 定义的任务族和 baseline。
- 说 **“shared compare case count”**，表示多个 agent 共同参与对比的 case 数。
- 说 **“go-claude-only real-task evidence”**，表示只有 go-claude 跑过的 APG 真实任务证据。
- 说 **“environment/setup blocker”**，表示失败来自环境、登录、模型、计费或配置，不归因于 agent 能力。
- 说 **“open-world superiority not proven”**，表示不能宣称开放世界已全面超过原版。

## 最小判断规则

1. 只跑 smoke：只说“链路可用/不可用”。
2. 只跑 go-claude report：只说“go-claude 在这些任务上通过/失败”。
3. 跑了 shared compare：可以说“在这些 shared cases 上相对某 baseline 无退化/有优势”。
4. 原版 Claude Code 没有进入 APG compare：不能说 APG 已证明超过原版 Claude Code。
5. `failure.category=environment_setup`：不能当作 baseline 能力失败消费。
6. `open_world_superiority_proven=false`：最终对外结论必须保持有边界。
