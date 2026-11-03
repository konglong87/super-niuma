# Super Niuma：同一个 Desktop 内的交付控制面

**状态：开发预览。** 左上角 **超级牛马干活 / Super Niuma** 与普通新建会话并存；不使用另一个网页作为产品入口，不改原 Session 生命周期。实现状态、测试结果及尚未通过的真实验收必须以[交付账本](../plans/2026-11-03-super-niuma-platform-delivery.md)和[模型验收记录](../deployment/2026-11-03-super-niuma-model-acceptance.md)为准。

## 从登记到执行

1. 源码构建：`npm --prefix web ci --legacy-peer-deps`，然后 `GOSUMDB=sum.golang.org bash scripts/build-desktop-v2.sh`。打开 `desktop-v2/build/bin/niuma.app`。
2. 在 Super 登记任务、目标、验收条件。**登记不会调用模型，也不授权文件路径**；可随时返回普通会话，原会话和草稿保留。
3. 控制台提供一个禁用的配置样例。修改授权工作根、已配置的模型/路由、读写路径、节点依赖、总 token/时间预算、有效期和具体结构化验收规则，然后保存。高级 JSON 只接受已知字段；禁止把 API key 放入平台配置。
4. 选择已登记工单和执行 profile，确认范围后提交。默认需要对冻结的授权摘要再批准；只有主动设置 `auto_approve` 的预授权 profile 才自动开始。
5. 两个只读分析节点可以并行，写入节点遵循依赖和全局工作根互斥。每个节点使用新执行上下文；不会劫持普通 Session。
6. 独立验收重新读取产物、计算 SHA-256、核对当前成功 attempt 的可信写入回执，再运行配置中的断言。**通过所配置规则不等于已证明任意自然语言要求**；需确保规则覆盖交付要求。初版不把模型自报、候选截图或 Worker 能改写的测试脚本当权威。
7. 通过后写入通知 outbox，由持久总线的独立订阅者派发。Desktop 回执表示本地接收，不表示人类阅读；外部回执也不代表人类签收。

## 能力及安全边界

- 首版工具是受限文件读取/列目录/原子写入。没有默认 shell、MCP、hooks、后台 Agent/worktree 或 Computer Use 能力；这不是 OS 沙箱。
- 每一次实际模型请求先原子预留根预算。子任务、返工共享额度；未知 usage 不按零消耗退款。没有隐式 provider 重试/fallback。
- profile、账户绑定、授权或公开路由发生变化，旧任务不能静默获得新权限。每次领取、续租、模型/工具调用与发送前检查实时权限。
- 同一或嵌套工作根的读写互斥；只读可并行。失租且不能证明停止的 writer 保持隔离，进入 `needs_attention`，不会自动重做未知副作用。
- 普通流量满容量不能阻止已有工单的取消和结算。原始记录、CAS revision、事件、预算、候选证据及回执均持久保存。
- 所有列表有界；Desktop 最近运行窗口明确提示是否还有历史。事件按 sequence 补读，唤醒/界面刷新不是消息权威。

## 状态语义

Intake 的 `recorded/cancelled` 表示登记事实，不是执行结果。独立运行使用：

- `awaiting_approval`：等待本地批准；`queued/running/verifying`：排队、执行、独立验收。
- `delivery_pending/delivered`：存在通知义务／所有配置目标均有接收回执。
- `failed`：已知失败；仅在条件满足且次数/预算仍允许时显式重试。
- `needs_attention`：用量、租约、证据或外部结果无法确认；**不盲重试**。
- `cancelling/cancelled`：撤销后阻止新动作、等待已知执行器收尾；不能撤回已发生的外部动作。

本地管理员可在明确核实旧 Worker/发送进程均停止后，放弃待人工核实的运行并释放占用。该操作有单次 revision 审计，**不宣称未知发送成功、不退款、不重新发送**；不得把确认框当作替代实际停机核实。

## 多渠道配置

平台配置可包含 `accounts` 和 `bindings`。每个 binding 精确关联 provider/account/chat/thread/sender → principal scope/owner/profile；账户身份来自受信 adapter，不来自正文或模型。跨渠道互通只允许显式批准的目标，默认外发安全状态摘要，不外发源码、工具原文或其他渠道内容。

- **Desktop**：本地管理与本地接收。
- **签名 Webhook**：`webhook_address` 只接受显式 loopback 地址。控制台显示的是签名机器接口 `/super/webhook/{account}`，**不是另一个产品页面**。验证固定签名串、时间窗、原始 body 摘要和持久 nonce/event/message 去重。出站仅发送到显式授权 HTTPS 目标，拒绝重定向和不安全地址。
- **飞书**：复用现有 SDK adapter，独立 Super 入站处理；不进入旧 channel/runtime 的 Session fallback。身份、白名单或线程无法确定时拒绝/待重试，不调用模型猜测授权。凭据只用环境变量引用，配置与 Git 不存真实密钥。

transport 已启动不等于实际账号连通，更不等于真实消息验收通过。需要授权测试账户、已安装权限、明确发送者/收件人和真实回执才能关闭飞书验收门禁。

## 存储和启动

- 独立私有目录：`~/.super-niuma`（0700），数据库 `work-orders.db` 和平台配置 `platform.json`（0600）。`SUPER_NIUMA_STATE_DIR` 可选择另一个绝对私有目录，不得指向旧 Session 数据库。
- 授权执行读取可信用户模型路由配置 `~/.niuma/settings.json`；Worker workspace 内的配置不能注入 hooks、模型端点或任意能力。
- SQLite v1→v2→v3 事务迁移保留原 intake、幂等键和事件；未来/异应用/损坏 schema 拒绝，不静默重建。
- Desktop 第一次进入 Super 时惰性打开；关闭会停止自己的 Worker/渠道。重启只恢复有权继续的队列，未知在途动作保持待核实。

## 可选开发调试入口

`go run ./cmd/super-niuma` 保留 loopback intake-only HTTP 控制台，便于黑盒调试。它不读取模型配置、不启动平台 Worker；传统 CLI 仍是 `go run ./cmd/niuma --help`。此前的 `127.0.0.1:61585` 是临时验收端口，不是产品入口。

不要把无外部鉴权的 intake API 反代到公网。签名渠道接口与本机 intake API 的安全语义不同，不能混用。

## 验收与运维

```sh
GOSUMDB=sum.golang.org go test -race ./internal/super/...
python3 scripts/super-niuma-boundary-test.py
```

真实模型测试需要显式 opt-in，默认 SKIP 绝不是通过。一次失败后先查安全诊断，不用无预算循环重试。原生验收必须重新 build、操作真实隔离 `.app` 并截图；DOM 测试不能替代。截图位于 `desktop-v2/build/validation/YYYYMMDD/`；严禁提交真实凭据、DB/WAL、原始日志或含私密请求的截图。

正式签名/分发、第二显示器及跨机器 HA 不在本轮已验证范围；Windows 文件能力当前 fail closed。没有正式发布安装包。
