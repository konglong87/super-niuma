# Super Niuma 全链路交付账本

用户要求：先详细设计，独立 review 方案，消除阻断问题后按方案实现；最终完成 Router、多 Worker、多 Subagent、总 Bus、多渠道互通及独立验收交付。

## 当前状态

- 起始 main：ce58405；已 pull；无预先存在的 dirty 文件。
- 原 niuma 仓库不作为写入目标；不创建分支/worktree。
- 架构草案：`docs/architecture/2026-11-03-super-niuma-control-plane.md`。
- 外部参考核实：deepseek-ai/dsh-superagent-prototype @6337fc823ce20251c8de8085948165c81aaa701e。
- A0：设计、两项复用审计及两位独立 reviewer 复核通过。**开始A1；产品完成门禁尚未达到。**

## 按依赖排序的队列

| 优先级 | 阶段 | 实现 | 自动测试 | 原生实操 | 模型/外部验收 | 提交/push |
| --- | --- | --- | --- | --- | --- | --- |
| 0 | A0 架构与方案 review | 双review通过 | 基线测试/边界通过 | 不适用 | 官方来源已核实 | 本切片提交/push |
| 1 | A1 持久 bus + migration + intake原子事件 | 已实现 | focused/race通过 | 不改变 UI | 无模型 | 本切片提交/push |
| 2 | A2 Router/admission/inbox/grants | Implemented; final review | Race PASS | Native approval/cancel exercised | Rule fast path uses no LLM | Pending current slice |
| 3 | A3 Worker/budget/cancel | Implemented; safe diagnostic closure | Race PASS | Native scoped authorization exercised | First real-model trial FAILED; retained | Pending current slice |
| 4 | A4 Parallel Worker/Subagent | Bounded DAG/shared budget implemented | SQLite overlap/race PASS | Two workers visible | Real-model closure not established | Pending current slice |
| 5 | A5 Verification/outbox | Implemented; concurrent-cancel fixed | Receipt/restart/race PASS | Report/delivery states visible | No worker self-certification | Pending current slice |
| 6 | A6 External channels | Webhook/Feishu/scoped bindings implemented | Router/adapter/race PASS | No live account used | BLOCKED: live Feishu account/recipient | Pending current slice |
| 7 | A7 Desktop control plane | Implemented; native UI polish in progress | Focused UI/typecheck PASS | Intake/config/consent/cancel exercised; false cancellation alarm being fixed | Native evidence only | Pending current slice |
| 8 | A8 End-to-end acceptance | Failure/recovery integration implemented | Full Super race/vet PASS | Final build recheck required | NOT PASSED: model failure and missing external channel credentials | Pending evidence |

每阶段：检查未完成队列 → 评审具体变更 → focused tests/race → 提交/push coherent slice → 继续最高优先级无阻塞项。不能因某个外部验收缺配置就放弃能继续完成的实现；也不能将缺配置的验收说成通过。

## 独立验收门禁

- 自动测试：纯函数/事务/并发/集成 fixture，不能代替以下三类。
- 原生脚本验收：真实隔离 Desktop `.app` 点击和截图，禁止误用用户日常进程。
- 自主模型验收：真实模型自己产生候选产物，无人工写业务状态/答案；持久 run/attempt 与 usage 可追溯。
- 外部传输验收：真实授权账户与收件人收发，provider receipt 对应同一工单；Webhook fixture不代替真实飞书。
- 最终贯通：至少一张工单覆盖以上路径；关联源码/build/进程窗口/事件/产物/验收/发送回执和截图 hash。

## 明确验收边界

当前已交付仅是 Desktop intake。任何新阶段只有代码、相应测试和发布状态实际完成才标记已交付。外部账号/模型的真实运行需可用配置及授权目标；没有则记录具体阻塞，保持该验收未完成。正式签名/分发和第二显示器仍是先前明确延期项，而不是本次暗中删减范围。

## 方案 Review 记录

- 第一轮 reliability：2 P0 / 3 P1，已按架构§17修订；复核通过。
- 第一轮 security：6 P1，已按架构§16修订；复核通过。
- 基线验证：`GOSUMDB=sum.golang.org go test ./internal/super/...` 全通过；boundary guard通过。
- 两位 reviewer 均确认无未关闭方案阻断，允许A1；A1不得启动真实模型/工具/渠道。

## A1 交付切片

- 变更：eventbus契约、SQLite v1→v2及历史回填、双订阅/ACK/NACK/lease/fencing/retry/DLQ，接单与取消+bus原子提交。
- 实测：`go test -race ./internal/super/eventbus ./internal/super/sqlitestore -skip 'TestControl|TestRouter'` 通过（A2控制层仍并行实施，明确排除未集成部分）。
- 下一优先级：A2/A3/A4持久CAS/资源/预算/执行器接线；A5只读验收器和A7前端已有task-owned代码，尚未交付。
- 原生、真实模型及真实外部通道验收尚未执行；不得以本切片测试替代。

## 隔离执行/验收内核切片

- 新执行适配器复用真实 query 循环，每attempt新上下文；限定文件工具、每请求预算、路径能力、可信写回执、路由指纹；无继承会话、shell、MCP、hooks或隐式重试。
- 独立验收器只读可信策略与候选文件，重新计算hash并执行结构化断言；拒绝symlink/越界/超量/重复JSON键。不把候选截图或模型自报当验收。
- 验证：execution与verification的 `go test -race`、`go vet`通过；测试使用本地fixture，不代表真实模型或外部账户验收。
- 该切片为A3/A5内核，调度/平台/渠道/原生UI集成仍进行中，不能标全平台交付。

## 原生验收新增阻断（用户反馈）

- 用户截图确认A7存在checkbox/label样式串扰、竖排窄列、异常留白/贴边；不能以此前UI单测替代视觉验收。
- 已将UI根因修复与布局美化列为当前P0体验门禁，前端agent专门修复；修复后必须重新build并实际截图。
- 原生QA副本曾被LaunchServices再次启动而丢失隔离环境；发现目录不一致后未点击业务按钮，已停止该副本进程。现测试Info.plist+open --env双重隔离并确认实际workspace为私有fixture。

## Resume and delivery ledger - 2026-11-03

- Resumed on main after status/diff review and `git pull --ff-only origin main` (already current). All dirty files are owned by this continuous task. The original niuma repository is outside the write scope.
- Pushed foundations: e9053e1 (reviewed architecture), c41690d (durable bus), 2acd5ea (isolated executor/independent verifier).
- Native QA exercised the Desktop Super entry, persisted one intake, saved an isolated profile, checked consent, submitted an awaiting-approval run and cancelled it. Attempts remain zero: no model was invoked.
- Native screenshots confirm the checkbox/layout fix. QA found a normal cancellation mislabeled as a backend issue; fixing this and WebKit select appearance is a pre-commit UI gate.
- macOS LaunchServices overrides HOME. QA now explicitly verifies NIUMA_CONFIG_DIR, NIUMA_DESKTOP_CONFIG_DIR and SUPER_NIUMA_STATE_DIR all point to its private fixture. The initial configuration-save failure started no execution.
- Regression coverage adds simultaneous notice settlement during cancellation, retained accepted receipts, and recovery of still-sending notices under needs_attention. Unknown side effects must never be blindly retried.
- Remaining priority: close security review; commit/push control-plane slice; final UI build/native recheck; broad regression from a short private path; clean private fixtures; final commit/push.
- Live model failure remains in the acceptance receipt. Real Feishu credentials and an authorized recipient are not configured; no claim of fully accepted multichannel end-to-end delivery.

### Verified checkpoint

- `fb22e1b` is committed and pushed: failed model outputs retain trustworthy metering and safe diagnostic enums; unknown usage still retains its reservation.
- Full Go regression from the private short-path source snapshot passed: 132 tested packages, no FAIL. This avoids the known Unix-socket path-length issue without relaxing an existing test.
- Intake HTTP smoke passed all 18 checks, including idempotency/authentication/cancellation/restart. This is intake-only evidence, not model or external-channel acceptance.
- Original niuma repository remains clean at `6b021afb71de9b6b43fa8de0e35e62638df75581`.
- Final independent integration review raised R06/R07 (expired siblings under Failed; scoped Desktop operator approval); implementation and focused regression are required before platform commit.

- `9cd8b41` committed/pushed: Super control UI, fixed checkbox/label grid, scoped native select styling, explicit cancellation messages, scope/owner labels; 81 focused tests + typecheck + boundary guards passed. Native screenshots must still be refreshed against the final build.
- Final frontend regression on the frozen UI passed: `npm test` = 96 files / 941 tests. The prior 81 focused tests, typecheck, 6 boundary-guard checks and 7 public-document checks also passed.
- R06 follow-on review found that an expired verification claim could be reclassified every scheduler tick, repeatedly increasing the root revision (duplicate bus emission was suspected but not confirmed). One-time recovery without releasing unknown isolation is required before final acceptance; the verification-claim provenance must be retained.
- Native UI acceptance will use an updated, ad-hoc signed QA clone with all three explicit config/state-root overrides. No daily-use app process will be modified or terminated.

### Integration review closed

- R06/R07 and concurrent-delivery cancellation findings are closed at implementation/automated-test level. Independent reviewer found no remaining P0/P1 in the reviewed slice.
- Expired verification claims now enter needs_attention once; 64 subsequent scheduler steps across restart preserve revision, bus count and isolation. The proven old failure was repeated revision; the existing event gate already filtered unchanged-state bus entries.
- The trusted Desktop operator can enumerate/approve/cancel/retry/abandon persisted external scopes and inspect their events/DLQ, with unique-ID resolution and stored-principal derivation. Router privileges remain unchanged.
- All Super packages and Desktop focused/broader race tests passed; Super + Desktop vet passed. The long-repository socket-path case is separately covered by full short-path Go regression rather than a production/test relaxation.
- Next: final native build/screenshots, one separately preregistered bounded real-model recheck, no live Feishu action without an authorized configured destination.
