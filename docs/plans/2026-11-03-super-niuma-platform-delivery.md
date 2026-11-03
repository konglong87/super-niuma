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
| 2 | A2 Router/admission/inbox/grants | 未开始 | 未开始 | 后续 A7 | 快路径零模型 | 待 A1 |
| 3 | A3 单Worker持久执行/预算/取消 | 未开始 | 未开始 | 后续 A7 | 需受限运行配置 | 待 A2 |
| 4 | A4 并行 Worker/Subagent | 未开始 | 未开始 | 后续 A7 | 需总预算 | 待 A3 |
| 5 | A5 独立验收/交付 outbox | 未开始 | 未开始 | 后续 A7 | 不接受自报成功 | 待 A4 |
| 6 | A6 外部渠道/授权互通 | 未开始 | 未开始 | 后续 A7 | 需凭据/白名单/验收目标 | 待 A2/A5 |
| 7 | A7 Desktop完整控制面 | 未开始 | 未开始 | 必须真实 app | 不以假数据替代 | 待 A1–A6 |
| 8 | A8 全链路故障/真实集成验收 | 未开始 | 未开始 | 必须 | 真实配置不可伪造 | 待 A7 |

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
